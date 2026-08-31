package vault

// Executor integration for Two-Person Integrity credentials.
//
// A two-person credential cannot be used through the normal single-principal
// Execute path (that path refuses it with ErrTwoPersonRequired). Instead the
// caller drives this three-step flow:
//
//	1. BeginTwoPerson(req, requesterIdentity) -> *TwoPersonRequest
//	   The requester's identity contributes their share. Not yet satisfiable.
//	2. ApproveTwoPerson(requestID, approverIdentity) -> satisfied bool
//	   A DISTINCT approver contributes a second share (repeat until satisfied).
//	3. FinalizeTwoPerson(requestID) -> *ExecutionResult
//	   Reconstructs the secret, runs the ORIGINAL command with it injected,
//	   redacts output, and zeroizes the secret.
//
// The original command+args are captured at Begin and replayed at Finalize, so
// approvers authorize a specific, immutable invocation.

import (
	"context"
	"strings"
	"time"

	"filippo.io/age"
)

// twoPersonPendingExec holds the original request bound to a broker request so
// Finalize can replay the exact approved command with the reconstructed secret.
type twoPersonPendingExec struct {
	req ExecutionRequest
}

// BeginTwoPerson starts a two-person approval for a sensitive credential. It
// validates constraints (tool/command/host) up front — same gates as the normal
// path — then registers a broker request seeded with the requester's share.
func (e *Executor) BeginTwoPerson(ctx context.Context, req ExecutionRequest, requester age.Identity) (*TwoPersonRequest, error) {
	cred, err := e.vault.ResolveCredential(ctx, req.CredentialID, req.ProjectID)
	if err != nil {
		return nil, err
	}
	if cred.IsExpired() {
		return nil, ErrExpired
	}
	// Enforce the same static constraints as the normal path before we even
	// start collecting approvals.
	if !cred.CanUseTool(req.Tool) {
		e.audit(cred, req, false, "tool_not_allowed")
		return nil, ErrToolNotAllowed
	}
	if !cred.CanUseCommand(req.EffectiveCommandLine()) {
		e.audit(cred, req, false, "command_not_allowed")
		return nil, ErrCommandNotAllowed
	}
	if len(cred.AllowedHosts) > 0 {
		host := req.Host
		if host == "" {
			return nil, ErrHostUnverifiable
		}
		if !cred.CanUseHost(host) {
			e.audit(cred, req, false, "host_not_allowed")
			return nil, ErrHostNotAllowed
		}
	}

	env, err := e.vault.ResolveTwoPersonEnvelope(req.CredentialID, req.ProjectID)
	if err != nil {
		return nil, err
	}

	command := canonicalCommand(req)
	tpReq, err := e.twoPerson.begin(req.CredentialID, command, req.Reason, env, requester)
	if err != nil {
		return nil, err
	}
	// Bind the original execution request to the broker request for replay.
	e.twoPerson.attach(tpReq.ID, twoPersonPendingExec{req: req})
	e.audit(cred, req, false, "two_person_begin")
	return tpReq, nil
}

// ApproveTwoPerson adds a distinct approver's share to a pending request.
// Returns whether the request is now satisfied (>= threshold approvers).
func (e *Executor) ApproveTwoPerson(requestID string, approver age.Identity) (bool, error) {
	return e.twoPerson.approve(requestID, approver)
}

// TwoPersonRequestInfo returns public metadata about a pending request.
func (e *Executor) TwoPersonRequestInfo(requestID string) (*TwoPersonRequest, bool) {
	return e.twoPerson.snapshot(requestID)
}

// CancelTwoPerson discards a pending request.
func (e *Executor) CancelTwoPerson(requestID string) {
	e.twoPerson.cancel(requestID)
}

// FinalizeTwoPerson reconstructs the secret for a satisfied request and runs the
// original command with it. The reconstructed secret is zeroized after use.
func (e *Executor) FinalizeTwoPerson(ctx context.Context, requestID string) (*ExecutionResult, error) {
	pend, ok := e.twoPerson.pendingExec(requestID)
	if !ok {
		return nil, ErrNotFound
	}
	req := pend.req

	cred, err := e.vault.ResolveCredential(ctx, req.CredentialID, req.ProjectID)
	if err != nil {
		return nil, err
	}

	// reconstruct consumes the request (single-use) and returns the plaintext.
	secret, err := e.twoPerson.reconstruct(requestID)
	if err != nil {
		return nil, err
	}
	defer zeroize(secret)

	// Build a credential carrying the reconstructed secret for this run only.
	runCred := *cred
	runCred.Secret = string(secret)

	start := time.Now()
	result, err := e.executeWithCredential(ctx, &runCred, req)
	if err != nil {
		return nil, err
	}
	result.Duration = time.Since(start)

	_ = e.vault.globalStorage.UpdateLastUsed(ctx, cred.ID)
	e.audit(cred, req, true, "two_person_finalized")
	return result, nil
}

// canonicalCommand returns a human-readable command+args string for display in
// approval prompts.
func canonicalCommand(req ExecutionRequest) string {
	return strings.TrimSpace(strings.Join(append([]string{req.Command}, req.Args...), " "))
}
