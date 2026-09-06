package gossip

import (
	"errors"
	"fmt"
	"time"
)

// MaxClockSkew bounds how far ahead of the verifier's own clock a frame's
// IssuedAt may be while still being accepted. This tolerates ordinary
// inter-host clock drift on a LAN without accepting an arbitrarily
// future-dated frame. It is a named, documented constant per the task
// brief; 30s matches the brief's own suggested example and is generous
// enough for unsynchronized-NTP LAN hosts while remaining tight enough to
// meaningfully bound a forged-future-timestamp frame.
const MaxClockSkew = 30 * time.Second

// VerifiedAdvertisement mirrors internal/lan.RemoteAdvertisementProof's
// field names EXACTLY (PeerIdentity, DaemonInstance, Route, Signer,
// IssuedAt, ExpiresAt, Nonce, ReplayChecked) per CONTRACT.md's shared type
// seam, so a later phase can construct a lan.RemoteAdvertisementProof from
// this struct's fields one-to-one without a rename. TLS is intentionally
// OMITTED this phase (this primitive is UDP-broadcast gossip, not a
// TLS-terminated connection; a later phase decides how/whether TLS
// identity attaches). This phase MUST NOT construct a
// lan.RemoteAdvertisementProof or call lan.NewRemoteAuthenticatedEndpoint
// from any production code path, and this package does not do so.
type VerifiedAdvertisement struct {
	PeerIdentity   string
	DaemonInstance string
	Route          string
	Signer         string
	IssuedAt       time.Time
	ExpiresAt      time.Time
	Nonce          string
	ReplayChecked  bool
}

// Verifier owns the signing key material and bounded replay-nonce cache
// for one process and verifies an inbound signed frame, returning a
// VerifiedAdvertisement only when the signature, skew bounds, and replay
// check all pass; ReplayChecked is always true on a successful return
// (this package owns and consults its own replay cache, unlike
// lan.RemoteAdvertisementProof's documented "caller owns the replay
// cache" contract -- that is fine, since this VerifiedAdvertisement is an
// internal proof-shaped record, not yet a lan.RemoteAdvertisementProof).
type Verifier interface {
	Verify(frame []byte, now time.Time) (VerifiedAdvertisement, error)
}

// verifier is the default Verifier implementation.
type verifier struct {
	replay *replayCache
}

// NewVerifier constructs a Verifier with the default bounded replay-cache
// capacity (defaultReplayCacheCapacity).
func NewVerifier() Verifier {
	return &verifier{replay: newReplayCache(defaultReplayCacheCapacity)}
}

// NewVerifierWithReplayCapacity constructs a Verifier whose replay cache is
// bounded to capacity entries instead of the default. Exposed mainly for
// tests that want to exercise cap-enforcement behavior through a Verifier
// rather than the unexported replayCache type directly; a non-positive
// capacity falls back to the default, same as newReplayCache.
func NewVerifierWithReplayCapacity(capacity int) Verifier {
	return &verifier{replay: newReplayCache(capacity)}
}

// Verify parses frame, verifies its Ed25519 signature over the canonical
// signed payload BEFORE trusting any other field, rejects it if now falls
// outside the [IssuedAt-MaxClockSkew, ExpiresAt) window, consults (and
// updates) the replay cache to reject a reused nonce, and only then returns
// a VerifiedAdvertisement with ReplayChecked true. Any rejection returns a
// descriptive error and the zero VerifiedAdvertisement -- never a partial
// one.
func (v *verifier) Verify(frame []byte, now time.Time) (VerifiedAdvertisement, error) {
	f, err := ParseFrame(frame)
	if err != nil {
		return VerifiedAdvertisement{}, err
	}

	// Signature verification happens first, before any other field is used
	// for a trust decision, per the task brief's explicit ordering
	// requirement.
	if err := f.verifySignature(); err != nil {
		return VerifiedAdvertisement{}, fmt.Errorf("gossip: reject frame: %w", err)
	}

	if f.ExpiresAt.IsZero() {
		return VerifiedAdvertisement{}, errors.New("gossip: reject frame: missing expiry")
	}
	// Allow IssuedAt to be up to MaxClockSkew ahead of our own clock (LAN
	// hosts are not guaranteed to share NTP-synced clocks), but reject
	// anything issued further in the future than that tolerance.
	if now.Before(f.IssuedAt.Add(-MaxClockSkew)) {
		return VerifiedAdvertisement{}, fmt.Errorf(
			"gossip: reject frame: issued_at %s is beyond the %s clock-skew tolerance ahead of now %s",
			f.IssuedAt, MaxClockSkew, now,
		)
	}
	// now must be strictly before ExpiresAt; "at or after" is expired.
	if !now.Before(f.ExpiresAt) {
		return VerifiedAdvertisement{}, fmt.Errorf(
			"gossip: reject frame: expires_at %s is at or before now %s", f.ExpiresAt, now,
		)
	}

	if f.Nonce == "" {
		return VerifiedAdvertisement{}, errors.New("gossip: reject frame: missing nonce")
	}
	if replayed := v.replay.seenOrRecord(f.Nonce, f.ExpiresAt, now); replayed {
		return VerifiedAdvertisement{}, fmt.Errorf("gossip: reject frame: nonce %q already seen (replay)", f.Nonce)
	}

	return VerifiedAdvertisement{
		PeerIdentity:   f.PeerIdentity,
		DaemonInstance: f.DaemonInstance,
		Route:          f.Route,
		Signer:         f.Signer,
		IssuedAt:       f.IssuedAt,
		ExpiresAt:      f.ExpiresAt,
		Nonce:          f.Nonce,
		ReplayChecked:  true,
	}, nil
}
