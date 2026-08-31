package vault

import (
	"bytes"
	"crypto/rand"
	"testing"

	"filippo.io/age"
)

// TestGF256Arithmetic checks field axioms on the log/exp tables.
func TestGF256Arithmetic(t *testing.T) {
	// a * 1 == a; a * 0 == 0; a / a == 1 (a != 0).
	for a := 0; a < 256; a++ {
		if got := gfMul(byte(a), 1); got != byte(a) {
			t.Fatalf("gfMul(%d,1)=%d", a, got)
		}
		if got := gfMul(byte(a), 0); got != 0 {
			t.Fatalf("gfMul(%d,0)=%d", a, got)
		}
		if a != 0 {
			q, err := gfDiv(byte(a), byte(a))
			if err != nil || q != 1 {
				t.Fatalf("gfDiv(%d,%d)=%d err=%v", a, a, q, err)
			}
		}
	}
	// Distributivity: a*(b+c) == a*b + a*c.
	for _, tc := range [][3]byte{{2, 3, 5}, {17, 200, 99}, {255, 1, 128}} {
		a, b, c := tc[0], tc[1], tc[2]
		l := gfMul(a, gfAdd(b, c))
		r := gfAdd(gfMul(a, b), gfMul(a, c))
		if l != r {
			t.Fatalf("distributivity failed for %v: %d != %d", tc, l, r)
		}
	}
}

// TestShamirRoundTrip verifies split/combine across thresholds and share subsets.
func TestShamirRoundTrip(t *testing.T) {
	secret := make([]byte, 32)
	if _, err := rand.Read(secret); err != nil {
		t.Fatal(err)
	}

	cases := []struct{ parts, threshold int }{
		{2, 2}, {3, 2}, {5, 2}, {5, 3}, {10, 7}, {255, 2},
	}
	for _, c := range cases {
		shares, err := shamirSplit(secret, c.parts, c.threshold)
		if err != nil {
			t.Fatalf("split(%d,%d): %v", c.parts, c.threshold, err)
		}
		if len(shares) != c.parts {
			t.Fatalf("expected %d shares, got %d", c.parts, len(shares))
		}
		// Exactly-threshold shares reconstruct.
		got, err := shamirCombine(shares[:c.threshold])
		if err != nil {
			t.Fatalf("combine(%d,%d): %v", c.parts, c.threshold, err)
		}
		if !bytes.Equal(got, secret) {
			t.Fatalf("round-trip mismatch for (%d,%d)", c.parts, c.threshold)
		}
		// A different subset of size threshold also reconstructs.
		if c.parts >= c.threshold+1 {
			got2, err := shamirCombine(shares[1 : c.threshold+1])
			if err != nil || !bytes.Equal(got2, secret) {
				t.Fatalf("alt subset failed for (%d,%d): %v", c.parts, c.threshold, err)
			}
		}
	}
}

// TestShamirBelowThresholdIsUseless proves the core security property: fewer
// than threshold shares cannot reconstruct the secret (combine either errors or
// yields something != secret).
func TestShamirBelowThresholdIsUseless(t *testing.T) {
	secret := []byte("AKIA-super-secret-datakey-000000")
	shares, err := shamirSplit(secret, 5, 3)
	if err != nil {
		t.Fatal(err)
	}
	// 2 shares when threshold is 3 must not recover the secret.
	got, err := shamirCombine(shares[:2])
	if err == nil && bytes.Equal(got, secret) {
		t.Fatal("SECURITY: reconstructed secret with fewer than threshold shares")
	}
}

// TestShamirRejectsBadInput covers guard rails.
func TestShamirRejectsBadInput(t *testing.T) {
	if _, err := shamirSplit([]byte("x"), 2, 1); err == nil {
		t.Fatal("threshold 1 must be rejected")
	}
	if _, err := shamirSplit([]byte("x"), 1, 2); err == nil {
		t.Fatal("parts < threshold must be rejected")
	}
	if _, err := shamirSplit(nil, 3, 2); err == nil {
		t.Fatal("empty secret must be rejected")
	}
	if _, err := shamirCombine([][]byte{{1, 2}}); err == nil {
		t.Fatal("single share must be rejected")
	}
	// Duplicate x-coordinate.
	dup := [][]byte{{9, 1}, {9, 1}}
	if _, err := shamirCombine(dup); err == nil {
		t.Fatal("duplicate x-coordinate must be rejected")
	}
}

// makeTestRecipients creates n age identities and returns identities +
// recipientWithKey slice.
func makeTestRecipients(t *testing.T, n int) ([]*age.X25519Identity, []recipientWithKey) {
	t.Helper()
	ids := make([]*age.X25519Identity, n)
	recs := make([]recipientWithKey, n)
	for i := 0; i < n; i++ {
		id, err := age.GenerateX25519Identity()
		if err != nil {
			t.Fatal(err)
		}
		ids[i] = id
		recs[i] = recipientWithKey{
			recipient: id.Recipient(),
			key:       id.Recipient().String(),
			comment:   "member",
		}
	}
	return ids, recs
}

// TestTwoPersonSealReconstruct is the end-to-end 2-of-N property: one identity
// yields one share (insufficient); two distinct identities reconstruct.
func TestTwoPersonSealReconstruct(t *testing.T) {
	ids, recs := makeTestRecipients(t, 3)
	secret := []byte("prod-aws-secret-key-value-123456")

	env, err := twoPersonSeal("aws-prod", secret, 2, recs)
	if err != nil {
		t.Fatalf("seal: %v", err)
	}
	// Envelope must not contain the plaintext anywhere obvious.
	if bytes.Contains(env.Ciphertext, secret) {
		t.Fatal("SECURITY: plaintext present in envelope ciphertext")
	}

	// Requester (id 0) decrypts one share.
	share0, _, err := twoPersonDecryptShare(env, ids[0])
	if err != nil {
		t.Fatalf("decrypt share0: %v", err)
	}
	// One share alone must not reconstruct.
	if _, err := twoPersonReconstruct(env, "aws-prod", [][]byte{share0}); err == nil {
		t.Fatal("SECURITY: reconstructed with a single share")
	}

	// Approver (id 1) decrypts a second, distinct share.
	share1, _, err := twoPersonDecryptShare(env, ids[1])
	if err != nil {
		t.Fatalf("decrypt share1: %v", err)
	}
	got, err := twoPersonReconstruct(env, "aws-prod", [][]byte{share0, share1})
	if err != nil {
		t.Fatalf("reconstruct: %v", err)
	}
	if !bytes.Equal(got, secret) {
		t.Fatal("reconstructed secret mismatch")
	}
}

// TestTwoPersonAADBinding verifies the ciphertext is bound to the credential ID
// (an envelope cannot be opened under a different credID).
func TestTwoPersonAADBinding(t *testing.T) {
	ids, recs := makeTestRecipients(t, 2)
	secret := []byte("bound-secret-value-0000000000000")
	env, err := twoPersonSeal("cred-A", secret, 2, recs)
	if err != nil {
		t.Fatal(err)
	}
	s0, _, _ := twoPersonDecryptShare(env, ids[0])
	s1, _, _ := twoPersonDecryptShare(env, ids[1])
	// Wrong credID must fail AEAD open.
	if _, err := twoPersonReconstruct(env, "cred-B", [][]byte{s0, s1}); err == nil {
		t.Fatal("SECURITY: envelope opened under wrong credential ID")
	}
	// Correct credID succeeds.
	if _, err := twoPersonReconstruct(env, "cred-A", [][]byte{s0, s1}); err != nil {
		t.Fatalf("correct credID should open: %v", err)
	}
}

// TestTwoPersonTamperDetected flips a ciphertext byte and expects auth failure.
func TestTwoPersonTamperDetected(t *testing.T) {
	ids, recs := makeTestRecipients(t, 2)
	env, err := twoPersonSeal("c", []byte("tamper-me-secret-value-00000000"), 2, recs)
	if err != nil {
		t.Fatal(err)
	}
	env.Ciphertext[0] ^= 0xFF
	s0, _, _ := twoPersonDecryptShare(env, ids[0])
	s1, _, _ := twoPersonDecryptShare(env, ids[1])
	if _, err := twoPersonReconstruct(env, "c", [][]byte{s0, s1}); err == nil {
		t.Fatal("SECURITY: tampered ciphertext passed authentication")
	}
}

// TestTwoPersonSealRejectsTooFewRecipients guards the policy floor.
func TestTwoPersonSealRejectsTooFewRecipients(t *testing.T) {
	_, recs := makeTestRecipients(t, 1)
	if _, err := twoPersonSeal("c", []byte("x"), 2, recs); err == nil {
		t.Fatal("threshold 2 with 1 recipient must be rejected")
	}
}
