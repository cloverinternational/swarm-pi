package gossip

import (
	"strings"
	"testing"
	"time"
)

func mustSigner(t *testing.T) *Signer {
	t.Helper()
	s, err := NewSigner()
	if err != nil {
		t.Fatalf("NewSigner: %v", err)
	}
	return s
}

func TestVerify_AcceptsFreshValidFrame(t *testing.T) {
	signer := mustSigner(t)
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	frame, err := Sign(signer, "peer-a", "daemon-1", "eth0", now, now.Add(time.Minute))
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	wire, err := frame.Bytes()
	if err != nil {
		t.Fatalf("Bytes: %v", err)
	}

	v := NewVerifier()
	got, err := v.Verify(wire, now.Add(time.Second))
	if err != nil {
		t.Fatalf("Verify: unexpected error: %v", err)
	}
	if got.PeerIdentity != "peer-a" || got.DaemonInstance != "daemon-1" || got.Route != "eth0" {
		t.Fatalf("unexpected VerifiedAdvertisement: %+v", got)
	}
	if !got.ReplayChecked {
		t.Fatalf("expected ReplayChecked=true on success, got %+v", got)
	}
	if got.Nonce != frame.Nonce {
		t.Fatalf("nonce mismatch: got %q want %q", got.Nonce, frame.Nonce)
	}
}

func TestVerify_RejectsTamperedPayload(t *testing.T) {
	signer := mustSigner(t)
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	frame, err := Sign(signer, "peer-a", "daemon-1", "eth0", now, now.Add(time.Minute))
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	wire, err := frame.Bytes()
	if err != nil {
		t.Fatalf("Bytes: %v", err)
	}

	// Tamper with exactly one byte inside the signed payload (the
	// peer_identity value), leaving the rest -- including Signature --
	// untouched, so this is a genuine single-byte content tamper, not a
	// structural corruption.
	tampered := []byte(strings.Replace(string(wire), `"peer-a"`, `"peer-b"`, 1))
	if string(tampered) == string(wire) {
		t.Fatalf("test setup failed to actually tamper the payload")
	}

	v := NewVerifier()
	if _, err := v.Verify(tampered, now.Add(time.Second)); err == nil {
		t.Fatalf("expected tampered frame to be rejected, got nil error")
	}
}

func TestVerify_RejectsWrongKey(t *testing.T) {
	signerA := mustSigner(t)
	signerB := mustSigner(t)
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)

	frame, err := Sign(signerA, "peer-a", "daemon-1", "eth0", now, now.Add(time.Minute))
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	// Swap in a DIFFERENT signer's public key so Signature (produced by
	// signerA's private key) no longer corresponds to the claimed Signer
	// public key -- a genuine "signed with the wrong key" mismatch, not a
	// mocked error type.
	frame.Signer = signerB.PublicKeyHex()
	wire, err := frame.Bytes()
	if err != nil {
		t.Fatalf("Bytes: %v", err)
	}

	v := NewVerifier()
	if _, err := v.Verify(wire, now.Add(time.Second)); err == nil {
		t.Fatalf("expected wrong-key frame to be rejected, got nil error")
	}
}

func TestVerify_RejectsExpiredFrame(t *testing.T) {
	signer := mustSigner(t)
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	frame, err := Sign(signer, "peer-a", "daemon-1", "eth0", now.Add(-time.Hour), now.Add(-time.Minute))
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	wire, err := frame.Bytes()
	if err != nil {
		t.Fatalf("Bytes: %v", err)
	}

	v := NewVerifier()
	if _, err := v.Verify(wire, now); err == nil {
		t.Fatalf("expected expired frame to be rejected, got nil error")
	}
}

func TestVerify_RejectsIssuedTooFarInFuture(t *testing.T) {
	signer := mustSigner(t)
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	// Issued well beyond MaxClockSkew ahead of "now".
	issuedAt := now.Add(MaxClockSkew + time.Hour)
	frame, err := Sign(signer, "peer-a", "daemon-1", "eth0", issuedAt, issuedAt.Add(time.Minute))
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	wire, err := frame.Bytes()
	if err != nil {
		t.Fatalf("Bytes: %v", err)
	}

	v := NewVerifier()
	if _, err := v.Verify(wire, now); err == nil {
		t.Fatalf("expected far-future-issued frame to be rejected, got nil error")
	}
}

func TestVerify_AcceptsFrameWithinSkewTolerance(t *testing.T) {
	signer := mustSigner(t)
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	// Issued slightly ahead of "now" but within MaxClockSkew -- must be
	// accepted (this is the ordinary clock-drift case the tolerance
	// exists for).
	issuedAt := now.Add(MaxClockSkew / 2)
	frame, err := Sign(signer, "peer-a", "daemon-1", "eth0", issuedAt, issuedAt.Add(time.Minute))
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	wire, err := frame.Bytes()
	if err != nil {
		t.Fatalf("Bytes: %v", err)
	}

	v := NewVerifier()
	if _, err := v.Verify(wire, now); err != nil {
		t.Fatalf("expected within-skew-tolerance frame to be accepted, got error: %v", err)
	}
}

func TestVerify_RejectsReplayedNonce(t *testing.T) {
	signer := mustSigner(t)
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	frame, err := Sign(signer, "peer-a", "daemon-1", "eth0", now, now.Add(time.Minute))
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	wire, err := frame.Bytes()
	if err != nil {
		t.Fatalf("Bytes: %v", err)
	}

	v := NewVerifier()
	if _, err := v.Verify(wire, now.Add(time.Second)); err != nil {
		t.Fatalf("first presentation should be accepted, got error: %v", err)
	}
	if _, err := v.Verify(wire, now.Add(2*time.Second)); err == nil {
		t.Fatalf("second presentation of the same nonce should be rejected as a replay, got nil error")
	}
}

func TestVerify_RejectionReturnsZeroValueNotPartial(t *testing.T) {
	signer := mustSigner(t)
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	frame, err := Sign(signer, "peer-a", "daemon-1", "eth0", now.Add(-time.Hour), now.Add(-time.Minute))
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	wire, err := frame.Bytes()
	if err != nil {
		t.Fatalf("Bytes: %v", err)
	}

	v := NewVerifier()
	got, err := v.Verify(wire, now)
	if err == nil {
		t.Fatalf("expected rejection")
	}
	zero := VerifiedAdvertisement{}
	if got != zero {
		t.Fatalf("expected zero-value VerifiedAdvertisement on rejection, got %+v", got)
	}
}
