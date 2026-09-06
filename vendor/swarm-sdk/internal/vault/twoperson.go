package vault

// Two-Person Integrity envelope encryption.
//
// A sensitive credential is protected so that reconstructing its plaintext
// requires cooperation of at least K distinct recipients (default K=2). This is
// enforced CRYPTOGRAPHICALLY, not by the executor's honesty: a single machine
// only ever holds one Shamir share of the data key, so it cannot decrypt alone
// even if the vault process is fully compromised.
//
// Envelope layout for one credential:
//
//	DK            = random 32-byte data key (never persisted in the clear)
//	ciphertext    = XChaCha20-Poly1305(DK, nonce, secret, AAD=credID)
//	shares[i]     = age-encrypt(recipient_i, shamirShare_i)   // one per recipient
//	threshold K   = how many shares must be combined to recover DK
//
// To use the secret: collect >= K decrypted shares (one auto from the
// requester's identity, the rest from approvers), shamirCombine -> DK,
// AEAD-open the ciphertext, then zeroize DK. See twoPersonReconstruct.

import (
	"bytes"
	"crypto/rand"
	"errors"
	"fmt"
	"io"

	"filippo.io/age"
	"golang.org/x/crypto/chacha20poly1305"
)

// twoPersonDataKeyLen is the length of the per-credential data key.
const twoPersonDataKeyLen = chacha20poly1305.KeySize // 32

// EncryptedShare is one Shamir share of the data key, age-encrypted to a single
// recipient's public key. Only the holder of the matching identity can open it.
type EncryptedShare struct {
	// Recipient is the age public key (recipients.txt entry) this share is for.
	Recipient string `json:"recipient"`
	// Comment is an optional human label (e.g. member name) copied from the roster.
	Comment string `json:"comment,omitempty"`
	// Ciphertext is age(recipient, shamirShare). Base64 is applied at the JSON layer.
	Ciphertext []byte `json:"ciphertext"`
}

// TwoPersonEnvelope is the on-disk protected form of a sensitive secret.
// It contains NO plaintext and NO single-holder-decryptable copy of the key.
type TwoPersonEnvelope struct {
	// Threshold is K: the minimum number of shares required to recover the key.
	Threshold int `json:"threshold"`
	// Nonce is the XChaCha20-Poly1305 nonce (24 bytes).
	Nonce []byte `json:"nonce"`
	// Ciphertext is the AEAD-sealed secret (AAD = credential ID).
	Ciphertext []byte `json:"ciphertext"`
	// Shares are the age-encrypted Shamir shares, one per recipient.
	Shares []EncryptedShare `json:"shares"`
}

// recipientWithKey pairs a parsed age recipient with its string form + comment.
type recipientWithKey struct {
	recipient age.Recipient
	key       string
	comment   string
}

// twoPersonSeal splits secret under a K-of-N policy and returns an envelope
// containing only ciphertext and age-encrypted shares. The data key is created
// here, used, and zeroized before returning — it never leaves this function.
func twoPersonSeal(credID string, secret []byte, threshold int, recipients []recipientWithKey) (*TwoPersonEnvelope, error) {
	n := len(recipients)
	if n < threshold {
		return nil, fmt.Errorf("twoperson: need >= %d recipients for threshold %d, have %d", threshold, threshold, n)
	}
	if threshold < 2 {
		return nil, fmt.Errorf("twoperson: threshold must be >= 2, got %d", threshold)
	}
	if len(secret) == 0 {
		return nil, errors.New("twoperson: empty secret")
	}

	// 1. Random data key.
	dk := make([]byte, twoPersonDataKeyLen)
	if _, err := rand.Read(dk); err != nil {
		return nil, fmt.Errorf("twoperson: rand dk: %w", err)
	}
	defer zeroize(dk)

	// 2. AEAD-seal the secret. AAD binds the ciphertext to this credential ID so
	//    an envelope cannot be swapped between credentials.
	aead, err := chacha20poly1305.NewX(dk)
	if err != nil {
		return nil, fmt.Errorf("twoperson: aead: %w", err)
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, fmt.Errorf("twoperson: rand nonce: %w", err)
	}
	ciphertext := aead.Seal(nil, nonce, secret, []byte(credID))

	// 3. Shamir-split the data key into N shares (threshold K).
	rawShares, err := shamirSplit(dk, n, threshold)
	if err != nil {
		return nil, err
	}
	defer func() {
		for _, s := range rawShares {
			zeroize(s)
		}
	}()

	// 4. age-encrypt each share to its recipient's public key.
	encShares := make([]EncryptedShare, n)
	for i, rec := range recipients {
		ct, err := ageEncryptTo(rec.recipient, rawShares[i])
		if err != nil {
			return nil, fmt.Errorf("twoperson: encrypt share for %s: %w", rec.key, err)
		}
		encShares[i] = EncryptedShare{
			Recipient:  rec.key,
			Comment:    rec.comment,
			Ciphertext: ct,
		}
	}

	return &TwoPersonEnvelope{
		Threshold:  threshold,
		Nonce:      nonce,
		Ciphertext: ciphertext,
		Shares:     encShares,
	}, nil
}

// twoPersonDecryptShare attempts to decrypt exactly ONE share of the envelope
// using the given identity. Returns the raw Shamir share AND the recipient
// public key that share belongs to. This is what a single principal (requester
// OR approver) can do alone — it yields one share, never enough by itself when
// threshold >= 2. The returned recipient key lets the broker enforce that two
// contributions came from DISTINCT principals.
func twoPersonDecryptShare(env *TwoPersonEnvelope, identity age.Identity) (share []byte, recipient string, err error) {
	var lastErr error
	for _, es := range env.Shares {
		r, derr := age.Decrypt(bytes.NewReader(es.Ciphertext), identity)
		if derr != nil {
			lastErr = derr
			continue
		}
		raw, rerr := io.ReadAll(r)
		if rerr != nil {
			lastErr = rerr
			continue
		}
		return raw, es.Recipient, nil
	}
	if lastErr == nil {
		lastErr = errors.New("twoperson: no share decryptable by this identity")
	}
	return nil, "", fmt.Errorf("twoperson: %w", lastErr)
}

// twoPersonReconstruct combines >= threshold raw shares to recover the data key,
// opens the AEAD ciphertext, and returns the plaintext secret. The data key is
// zeroized before returning. Callers MUST zeroize the returned secret when done.
func twoPersonReconstruct(env *TwoPersonEnvelope, credID string, shares [][]byte) ([]byte, error) {
	if len(shares) < env.Threshold {
		return nil, fmt.Errorf("twoperson: have %d shares, need %d", len(shares), env.Threshold)
	}
	dk, err := shamirCombine(shares)
	if err != nil {
		return nil, err
	}
	defer zeroize(dk)

	if len(dk) != twoPersonDataKeyLen {
		return nil, fmt.Errorf("twoperson: reconstructed key wrong length %d", len(dk))
	}
	aead, err := chacha20poly1305.NewX(dk)
	if err != nil {
		return nil, fmt.Errorf("twoperson: aead: %w", err)
	}
	secret, err := aead.Open(nil, env.Nonce, env.Ciphertext, []byte(credID))
	if err != nil {
		// AEAD failure here means wrong/tampered shares or ciphertext.
		return nil, fmt.Errorf("twoperson: authentication failed (wrong shares or tampered envelope): %w", err)
	}
	return secret, nil
}

// ageEncryptTo age-encrypts plaintext to a single recipient and returns the
// binary ciphertext.
func ageEncryptTo(recipient age.Recipient, plaintext []byte) ([]byte, error) {
	var buf bytes.Buffer
	w, err := age.Encrypt(&buf, recipient)
	if err != nil {
		return nil, err
	}
	if _, err := w.Write(plaintext); err != nil {
		return nil, err
	}
	if err := w.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
