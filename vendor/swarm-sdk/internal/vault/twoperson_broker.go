package vault

// Two-person approval broker.
//
// This orchestrates the cross-principal approval required to use a sensitive
// (Two-Person Integrity) credential. It sits beside the Executor and enforces:
//
//   - The REQUESTER contributes their share automatically (their identity
//     decrypts one share of the envelope).
//   - A distinct APPROVER (different recipient key, in the roster) must
//     contribute a second share before the secret can be reconstructed.
//   - Reconstruction combines >= threshold shares, opens the AEAD envelope, and
//     the recovered secret + data key are zeroized after use.
//
// The broker never persists reconstructed plaintext. A pending request holds
// only already-decrypted shares (each individually useless below threshold) and
// the set of contributing principals, so approver != requester can be enforced.

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"sync"
	"time"

	"filippo.io/age"
)

// TwoPersonRequest is a pending sensitive-credential access awaiting the second
// (or Kth) approver. It is safe to surface metadata about it to the UI/agent —
// it contains no reconstructable secret.
type TwoPersonRequest struct {
	ID           string   // opaque request id
	CredentialID string   // which credential
	Command      string   // canonical command+args (display)
	Reason       string   // requester-supplied justification
	Threshold    int      // K shares needed
	RequesterKey string   // requester's recipient public key
	Contributors []string // recipient keys that have contributed a share
	CreatedAt    time.Time
	ExpiresAt    time.Time

	shares [][]byte // decrypted shares gathered so far (each useless alone)
	env    *TwoPersonEnvelope
	exec   interface{} // opaque bound execution request (twoPersonPendingExec)
}

// twoPersonBroker manages pending two-person requests.
type twoPersonBroker struct {
	mu         sync.Mutex
	pending    map[string]*TwoPersonRequest
	ttl        time.Duration
	maxPending int
}

// defaultMaxPendingTwoPerson bounds the number of concurrent pending requests to
// prevent unbounded memory growth from abandoned (never-approved) requests.
const defaultMaxPendingTwoPerson = 256

func newTwoPersonBroker(ttl time.Duration) *twoPersonBroker {
	if ttl <= 0 {
		ttl = DefaultGrantTTL
	}
	return &twoPersonBroker{
		pending:    make(map[string]*TwoPersonRequest),
		ttl:        ttl,
		maxPending: defaultMaxPendingTwoPerson,
	}
}

// sweepExpiredLocked drops (and zeroizes) all expired pending requests. Caller
// must hold b.mu. Called opportunistically on begin so abandoned requests don't
// linger with their decrypted shares for the full TTL window.
func (b *twoPersonBroker) sweepExpiredLocked(now time.Time) {
	for _, req := range b.pending {
		if now.After(req.ExpiresAt) {
			b.discardLocked(req)
		}
	}
}

// begin starts a two-person request: the requester's identity decrypts their
// own share and the request is registered awaiting further approvers. Returns
// the request (with its ID) which is NOT yet satisfiable.
func (b *twoPersonBroker) begin(credID, command, reason string, env *TwoPersonEnvelope, requester age.Identity) (*TwoPersonRequest, error) {
	if env == nil {
		return nil, fmt.Errorf("twoperson: nil envelope for %q", credID)
	}
	share, recipientKey, err := twoPersonDecryptShare(env, requester)
	if err != nil {
		return nil, fmt.Errorf("twoperson: requester cannot access this credential: %w", err)
	}

	now := time.Now()
	req := &TwoPersonRequest{
		ID:           newTwoPersonRequestID(),
		CredentialID: credID,
		Command:      command,
		Reason:       reason,
		Threshold:    env.Threshold,
		RequesterKey: recipientKey,
		Contributors: []string{recipientKey},
		CreatedAt:    now,
		ExpiresAt:    now.Add(b.ttl),
		shares:       [][]byte{share},
		env:          env,
	}

	b.mu.Lock()
	defer b.mu.Unlock()
	// Opportunistically drop expired requests so abandoned ones don't pin their
	// decrypted shares in memory, then enforce the pending cap.
	b.sweepExpiredLocked(now)
	if len(b.pending) >= b.maxPending {
		zeroize(share)
		return nil, fmt.Errorf("twoperson: too many pending approval requests (%d); try again later", len(b.pending))
	}
	b.pending[req.ID] = req
	return req, nil
}

// approve adds an approver's share to a pending request. The approver's
// identity must decrypt a share belonging to a DISTINCT recipient (not already
// a contributor). Returns whether the request is now satisfied (>= threshold).
func (b *twoPersonBroker) approve(requestID string, approver age.Identity) (satisfied bool, err error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	req, ok := b.pending[requestID]
	if !ok {
		return false, fmt.Errorf("twoperson: unknown or expired request %q", requestID)
	}
	if time.Now().After(req.ExpiresAt) {
		b.discardLocked(req)
		return false, fmt.Errorf("twoperson: request %q expired", requestID)
	}

	share, recipientKey, err := twoPersonDecryptShare(req.env, approver)
	if err != nil {
		return false, fmt.Errorf("twoperson: approver is not a recipient of this credential: %w", err)
	}

	// Enforce separation of duties: the approver must be a DISTINCT principal.
	for _, c := range req.Contributors {
		if c == recipientKey {
			zeroize(share)
			return false, fmt.Errorf("twoperson: this principal already contributed; a DIFFERENT approver is required")
		}
	}

	req.Contributors = append(req.Contributors, recipientKey)
	req.shares = append(req.shares, share)
	return len(req.shares) >= req.Threshold, nil
}

// reconstruct finalizes a satisfied request: combines shares, opens the
// envelope, and returns the plaintext secret. The request is consumed (single
// use) and all held shares are zeroized. Callers MUST zeroize the returned
// secret after use.
func (b *twoPersonBroker) reconstruct(requestID string) ([]byte, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	req, ok := b.pending[requestID]
	if !ok {
		return nil, fmt.Errorf("twoperson: unknown or expired request %q", requestID)
	}
	if time.Now().After(req.ExpiresAt) {
		b.discardLocked(req)
		return nil, fmt.Errorf("twoperson: request %q expired", requestID)
	}
	if len(req.shares) < req.Threshold {
		return nil, fmt.Errorf("twoperson: request %q needs %d approvers, has %d", requestID, req.Threshold, len(req.shares))
	}

	secret, err := twoPersonReconstruct(req.env, req.CredentialID, req.shares)
	// Consume single-use regardless of outcome and scrub shares.
	b.discardLocked(req)
	if err != nil {
		return nil, err
	}
	return secret, nil
}

// snapshot returns a copy of a pending request's public metadata (no shares).
func (b *twoPersonBroker) snapshot(requestID string) (*TwoPersonRequest, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	req, ok := b.pending[requestID]
	if !ok {
		return nil, false
	}
	cp := *req
	cp.shares = nil
	cp.env = nil
	cp.Contributors = append([]string(nil), req.Contributors...)
	return &cp, true
}

// cancel discards a pending request and scrubs its shares.
func (b *twoPersonBroker) cancel(requestID string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if req, ok := b.pending[requestID]; ok {
		b.discardLocked(req)
	}
}

// discardLocked removes a request and zeroizes its held shares. Caller holds mu.
func (b *twoPersonBroker) discardLocked(req *TwoPersonRequest) {
	for _, s := range req.shares {
		zeroize(s)
	}
	req.shares = nil
	delete(b.pending, req.ID)
}

// attach binds an opaque execution payload to a pending request so the executor
// can replay the exact approved command at finalize time.
func (b *twoPersonBroker) attach(requestID string, payload interface{}) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if req, ok := b.pending[requestID]; ok {
		req.exec = payload
	}
}

// pendingExec returns the bound execution payload for a request, if present.
func (b *twoPersonBroker) pendingExec(requestID string) (twoPersonPendingExec, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	req, ok := b.pending[requestID]
	if !ok {
		return twoPersonPendingExec{}, false
	}
	p, ok := req.exec.(twoPersonPendingExec)
	return p, ok
}

// newTwoPersonRequestID returns a random hex request identifier.
func newTwoPersonRequestID() string {
	buf := make([]byte, 12)
	if _, err := rand.Read(buf); err != nil {
		return fmt.Sprintf("2pr-%d", time.Now().UnixNano())
	}
	return "2pr-" + hex.EncodeToString(buf)
}
