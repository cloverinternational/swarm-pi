// Package gossip implements Phase 06 (P06.D)'s "genuinely signed LAN gossip
// v2" primitive: a signed advertisement wire frame, an Ed25519 signer, and
// (in verifier.go/replay.go) a verifier with a bounded-memory nonce replay
// cache. See CONTRACT.md's "Shared type seam" section for
// swarm-sdk/internal/lan/gossip and INDEX.md in this directory for the full
// scope statement, including the deliberate gaps this phase leaves open
// (key provisioning, discovery broker/interface ranking, and the fact that
// this package's VerifiedAdvertisement is never converted into a
// lan.RemoteAdvertisementProof or lan.AuthenticatedEndpoint by any
// production code path this phase).
//
// This is a genuinely NEW v2 wire shape. It intentionally does not reuse
// internal/a2a/lan_registry.go's existing unsigned peerSyncFrame
// Service/Version fields or JSON layout; the two frame types are unrelated
// on the wire and are ingested by separate UDP listeners on separate ports
// (see internal/a2a/lan_registry_v2.go).
package gossip

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// NonceBytes is the number of random bytes in a Frame's anti-replay nonce
// before hex-encoding (so the wire Nonce string is 2*NonceBytes hex chars).
const NonceBytes = 16

// Frame is the v2 signed LAN advertisement wire frame. It is JSON-
// serializable and distinct from lan_registry.go's unsigned v1
// peerSyncFrame. Field names deliberately mirror the vocabulary
// internal/lan.RemoteAdvertisementProof uses (PeerIdentity, DaemonInstance,
// Route, Signer, IssuedAt, ExpiresAt, Nonce) so a later phase's mapping from
// VerifiedAdvertisement to that proof type is a straight field copy; this
// package never constructs that type itself.
type Frame struct {
	// PeerIdentity is the advertising peer's claimed identity.
	PeerIdentity string `json:"peer_identity"`
	// DaemonInstance is the advertising daemon process's instance identity.
	DaemonInstance string `json:"daemon_instance"`
	// Route describes the interface/subnet/route this advertisement was
	// issued for (for example a LAN interface name or CIDR). It is
	// descriptive provenance, not yet a ranked/selected route -- see
	// INDEX.md's "Scope vs ADR-007/task-14" section: interface ranking is
	// deferred.
	Route string `json:"route"`
	// Signer is the lowercase-hex-encoded Ed25519 public key that produced
	// Signature. The verifier trusts only what this key actually signed;
	// it does not consult any external key registry or trust store this
	// phase (see INDEX.md's key-provisioning gap).
	Signer string `json:"signer"`
	// IssuedAt is the advertisement's signed issue time (UTC recommended,
	// but the verifier compares wall-clock instants, not zones).
	IssuedAt time.Time `json:"issued_at"`
	// ExpiresAt is the advertisement's mandatory signed expiry.
	ExpiresAt time.Time `json:"expires_at"`
	// Nonce is a lowercase-hex-encoded random anti-replay nonce, unique per
	// signed frame.
	Nonce string `json:"nonce"`
	// Signature is the lowercase-hex-encoded Ed25519 signature over the
	// canonical signed payload (every field above, marshaled by
	// canonicalSignedBytes -- see signedFields below). It is never itself
	// part of the signed payload.
	Signature string `json:"signature"`
}

// signedFields is the canonical payload that gets signed and verified. It
// is a distinct type (rather than Frame with Signature left zero) so the
// signed-bytes shape can never accidentally drift if Frame gains fields
// later without a corresponding signedFields update.
type signedFields struct {
	PeerIdentity   string    `json:"peer_identity"`
	DaemonInstance string    `json:"daemon_instance"`
	Route          string    `json:"route"`
	Signer         string    `json:"signer"`
	IssuedAt       time.Time `json:"issued_at"`
	ExpiresAt      time.Time `json:"expires_at"`
	Nonce          string    `json:"nonce"`
}

func (f *Frame) signedFields() signedFields {
	return signedFields{
		PeerIdentity:   f.PeerIdentity,
		DaemonInstance: f.DaemonInstance,
		Route:          f.Route,
		Signer:         f.Signer,
		IssuedAt:       f.IssuedAt,
		ExpiresAt:      f.ExpiresAt,
		Nonce:          f.Nonce,
	}
}

// canonicalSignedBytes returns the deterministic byte sequence that is
// signed and verified for sf. encoding/json marshals a struct's fields in
// fixed declaration order for a fixed Go type, so this is deterministic
// across calls/processes for the same field values -- no field is a map,
// so there is no key-ordering ambiguity to worry about.
func canonicalSignedBytes(sf signedFields) ([]byte, error) {
	b, err := json.Marshal(sf)
	if err != nil {
		return nil, fmt.Errorf("gossip: marshal canonical signed payload: %w", err)
	}
	return b, nil
}

// Signer owns Ed25519 key material for one process and signs advertisement
// frames with it.
//
// Key-provisioning gap (documented, in scope this phase only as an
// in-memory/process-lifetime primitive -- see INDEX.md): this type
// generates or holds a key entirely in memory. There is no durable
// keystore, rotation, or cross-process trust-distribution mechanism this
// phase; every process that wants to verify another process's frames must
// learn its Signer public key out of band (for example by the same
// mechanism deferred to the discovery-broker work in INDEX.md's "Scope vs
// ADR-007/task-14" section). A future phase is expected to replace
// NewSigner's in-memory generation with durable, rotate-able key storage
// without changing Frame's wire shape.
type Signer struct {
	public  ed25519.PublicKey
	private ed25519.PrivateKey
}

// NewSigner generates a fresh in-memory Ed25519 key pair for the lifetime
// of the calling process. See the Signer doc comment for the documented
// key-provisioning gap.
func NewSigner() (*Signer, error) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("gossip: generate signer key: %w", err)
	}
	return &Signer{public: pub, private: priv}, nil
}

// NewSignerFromSeed deterministically derives a Signer from a caller-
// supplied Ed25519 seed (ed25519.SeedSize bytes). This exists for tests
// that need reproducible key material (for example to construct a
// deliberately mismatched wrong-key frame); production code should
// normally call NewSigner instead.
func NewSignerFromSeed(seed []byte) (*Signer, error) {
	if len(seed) != ed25519.SeedSize {
		return nil, fmt.Errorf("gossip: signer seed must be %d bytes, got %d", ed25519.SeedSize, len(seed))
	}
	priv := ed25519.NewKeyFromSeed(seed)
	return &Signer{public: priv.Public().(ed25519.PublicKey), private: priv}, nil
}

// PublicKeyHex returns the Signer's public key, lowercase-hex-encoded, the
// same encoding used in Frame.Signer.
func (s *Signer) PublicKeyHex() string {
	return hex.EncodeToString(s.public)
}

// newNonce returns a fresh lowercase-hex-encoded random nonce.
func newNonce() (string, error) {
	var raw [NonceBytes]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", fmt.Errorf("gossip: generate nonce: %w", err)
	}
	return hex.EncodeToString(raw[:]), nil
}

// Sign builds and signs a new Frame for the given peer/daemon/route
// identity, valid from issuedAt until expiresAt, with a fresh random nonce.
func Sign(signer *Signer, peerIdentity, daemonInstance, route string, issuedAt, expiresAt time.Time) (*Frame, error) {
	if signer == nil {
		return nil, errors.New("gossip: sign requires a non-nil signer")
	}
	if !expiresAt.After(issuedAt) {
		return nil, errors.New("gossip: sign requires expiresAt after issuedAt")
	}
	nonce, err := newNonce()
	if err != nil {
		return nil, err
	}
	f := &Frame{
		PeerIdentity:   peerIdentity,
		DaemonInstance: daemonInstance,
		Route:          route,
		Signer:         signer.PublicKeyHex(),
		IssuedAt:       issuedAt,
		ExpiresAt:      expiresAt,
		Nonce:          nonce,
	}
	payload, err := canonicalSignedBytes(f.signedFields())
	if err != nil {
		return nil, err
	}
	f.Signature = hex.EncodeToString(ed25519.Sign(signer.private, payload))
	return f, nil
}

// Bytes serializes f to its wire (JSON) representation.
func (f *Frame) Bytes() ([]byte, error) {
	b, err := json.Marshal(f)
	if err != nil {
		return nil, fmt.Errorf("gossip: marshal frame: %w", err)
	}
	return b, nil
}

// ParseFrame parses a wire frame. It performs only structural JSON parsing
// and does not evaluate signature, skew, or replay -- callers that need a
// trust decision must use Verifier.Verify, which parses internally and
// checks the signature before trusting any other field.
func ParseFrame(data []byte) (*Frame, error) {
	var f Frame
	if err := json.Unmarshal(data, &f); err != nil {
		return nil, fmt.Errorf("gossip: parse frame: %w", err)
	}
	return &f, nil
}

// verifySignature checks f.Signature against f's canonical signed payload
// using the Ed25519 public key encoded in f.Signer. It returns a
// descriptive error for any structural problem (malformed hex, wrong key
// length) as well as for a genuine signature mismatch, and never trusts
// f.IssuedAt/ExpiresAt/Nonce for a policy decision -- signature validity is
// self-contained and checked first by design.
func (f *Frame) verifySignature() error {
	pubBytes, err := hex.DecodeString(f.Signer)
	if err != nil {
		return fmt.Errorf("gossip: signer is not valid hex: %w", err)
	}
	if len(pubBytes) != ed25519.PublicKeySize {
		return fmt.Errorf("gossip: signer key has wrong length %d, want %d", len(pubBytes), ed25519.PublicKeySize)
	}
	sigBytes, err := hex.DecodeString(f.Signature)
	if err != nil {
		return fmt.Errorf("gossip: signature is not valid hex: %w", err)
	}
	if len(sigBytes) != ed25519.SignatureSize {
		return fmt.Errorf("gossip: signature has wrong length %d, want %d", len(sigBytes), ed25519.SignatureSize)
	}
	payload, err := canonicalSignedBytes(f.signedFields())
	if err != nil {
		return err
	}
	if !ed25519.Verify(ed25519.PublicKey(pubBytes), payload, sigBytes) {
		return errors.New("gossip: signature verification failed")
	}
	return nil
}
