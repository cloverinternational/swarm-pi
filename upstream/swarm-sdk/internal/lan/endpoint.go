// ADR-007 "Authenticated endpoint provenance" (see
// docs/architecture/swarm-attach/adr-007-gateway-security.md) and Phase 02's
// CONTRACT.md require a non-string-alias provenance type that only this
// package can construct, from either validated LOCAL presence ownership or a
// validated signed/authenticated REMOTE LAN advertisement -- never from a raw
// a2a.PeerPresence string/URL, AddedBy, a remote type string, a handle
// prefix, freshness alone, reachability alone, user selection, or successful
// DNS resolution.
//
// This file defines that type, AuthenticatedEndpoint, and its two
// constructors: NewLocalAuthenticatedEndpoint and
// NewRemoteAuthenticatedEndpoint. There is deliberately no exported
// constructor, method, or conversion that accepts only a URL string --
// callers MUST supply the full LocalEndpointProof or RemoteAdvertisementProof
// evidence bundle ADR-007 requires, and every required field is checked
// before an AuthenticatedEndpoint is returned.
package lan

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"net"
	"net/url"
	"strings"
	"time"
)

// EndpointSchemaVersion is the current AuthenticatedEndpoint source-record
// schema version. GatewayPolicy.Transport refuses any endpoint whose
// SchemaVersion() does not exactly match, so a future incompatible proof
// shape cannot silently be accepted by an older policy build.
const EndpointSchemaVersion = 1

// EndpointProvenance discriminates a LOCAL, same-user presence-owned
// endpoint from a REMOTE, signed-LAN-advertisement endpoint. It is set only
// by the constructor that validated the corresponding proof -- there is no
// exported setter.
type EndpointProvenance string

const (
	// ProvenanceLocal marks an endpoint constructed by
	// NewLocalAuthenticatedEndpoint from validated presence ownership,
	// instance identity, lease, and publication path.
	ProvenanceLocal EndpointProvenance = "local"
	// ProvenanceRemote marks an endpoint constructed by
	// NewRemoteAuthenticatedEndpoint from a validated signed/authenticated
	// LAN advertisement.
	ProvenanceRemote EndpointProvenance = "remote"
)

// Errors returned by the AuthenticatedEndpoint constructors and consulted by
// GatewayPolicy.Transport. They are wrapped, not replaced, so callers can use
// errors.Is while still reading a specific message.
var (
	// ErrEndpointProvenance covers every missing/invalid proof field: a
	// caller that supplies a raw URL and an empty or partially-filled proof
	// value gets this error, never a constructed AuthenticatedEndpoint.
	ErrEndpointProvenance = errors.New("lan: authenticated endpoint provenance unproven")
	// ErrEndpointExpired covers an expired local presence lease or an
	// expired (or not-yet-valid) remote advertisement.
	ErrEndpointExpired = errors.New("lan: authenticated endpoint provenance expired")
	// ErrEndpointScheme covers a URL scheme ineligible for the requested
	// provenance: plain http/ws for anything but an explicit loopback
	// literal, or anything other than https/wss for a remote advertisement.
	ErrEndpointScheme = errors.New("lan: authenticated endpoint scheme not eligible for this provenance")
	// ErrEndpointTLSIdentity covers a remote advertisement missing, or
	// mismatched against, its signed TLS server name/trust domain and SPKI
	// SHA-256 pin.
	ErrEndpointTLSIdentity = errors.New("lan: authenticated endpoint missing verified TLS identity")
)

// SPKIFingerprint is a typed SHA-256 hash over a certificate's DER-encoded
// SubjectPublicKeyInfo. It is deliberately a fixed-size byte array, not a
// string, so it cannot be constructed from an arbitrary short/guessable
// value by accident.
type SPKIFingerprint [sha256.Size]byte

// IsZero reports whether f is the unset fingerprint.
func (f SPKIFingerprint) IsZero() bool { return f == SPKIFingerprint{} }

// String returns f as lowercase hex.
func (f SPKIFingerprint) String() string { return fmt.Sprintf("%x", [sha256.Size]byte(f)) }

// SPKIFingerprintFromDER hashes a DER-encoded SubjectPublicKeyInfo (for
// example x509.Certificate.RawSubjectPublicKeyInfo) into a SPKIFingerprint.
func SPKIFingerprintFromDER(spkiDER []byte) SPKIFingerprint {
	return sha256.Sum256(spkiDER)
}

// RemoteTLSIdentity is the signed TLS server name/trust domain and SPKI
// SHA-256 pin a remote AuthenticatedEndpoint carries per ADR-007: "A remote
// endpoint also contains the signed TLS server name/trust domain and SPKI
// SHA-256 (or successor typed certificate identity) that the transport must
// verify." GatewayPolicy.Transport verifies both during the TLS handshake --
// see gateway_policy.go.
type RemoteTLSIdentity struct {
	// ServerName is the signed TLS server name (or trust-domain identity)
	// the advertisement bound to this endpoint's host.
	ServerName string
	// TrustDomain optionally further scopes ServerName (for example a
	// signer-controlled namespace distinct from the bare DNS name). It is
	// not required to be non-empty, unlike ServerName and SPKI.
	TrustDomain string
	// SPKI is the SHA-256 fingerprint of the certificate's
	// SubjectPublicKeyInfo the advertisement signed.
	SPKI SPKIFingerprint
}

func (t RemoteTLSIdentity) valid() bool {
	return t.ServerName != "" && !t.SPKI.IsZero()
}

// AuthenticatedEndpoint is ADR-007's non-string-alias provenance-bound peer
// transport target. It is never a bare string or *url.URL: every field below
// is unexported and reachable only through an accessor, and the only two
// ways to populate one are NewLocalAuthenticatedEndpoint and
// NewRemoteAuthenticatedEndpoint. Converting a raw a2a.PeerPresence
// string/URL directly into an AuthenticatedEndpoint is impossible from this
// package's public API -- there is no constructor that accepts only a URL
// string, and AddedBy, a remote type string, a handle prefix, freshness
// alone, reachability alone, user selection, and successful DNS resolution
// are never, by themselves, accepted as proof by either constructor.
type AuthenticatedEndpoint struct {
	normalized url.URL

	peerIdentity   string
	daemonInstance string
	provenance     EndpointProvenance
	signer         string
	schemaVersion  int
	issuedAt       time.Time
	receivedAt     time.Time
	expiresAt      time.Time
	routeProof     string

	// tls is nil for a local endpoint and always non-nil (and valid) for a
	// remote endpoint -- NewRemoteAuthenticatedEndpoint refuses to return an
	// endpoint with an invalid RemoteTLSIdentity.
	tls *RemoteTLSIdentity
}

// URL returns a copy of the normalized endpoint URL. Mutating the returned
// value never affects e.
func (e AuthenticatedEndpoint) URL() *url.URL {
	u := e.normalized
	return &u
}

// PeerIdentity returns the authenticated peer identity this endpoint is
// bound to.
func (e AuthenticatedEndpoint) PeerIdentity() string { return e.peerIdentity }

// DaemonInstance returns the authenticated daemon instance identity this
// endpoint is bound to.
func (e AuthenticatedEndpoint) DaemonInstance() string { return e.daemonInstance }

// Provenance reports whether e was constructed from local presence ownership
// or a remote signed advertisement.
func (e AuthenticatedEndpoint) Provenance() EndpointProvenance { return e.provenance }

// IsRemote reports whether e is a remote (signed-advertisement) endpoint.
func (e AuthenticatedEndpoint) IsRemote() bool { return e.provenance == ProvenanceRemote }

// IsLocal reports whether e is a local (presence-ownership) endpoint.
func (e AuthenticatedEndpoint) IsLocal() bool { return e.provenance == ProvenanceLocal }

// Signer returns the signer/credential identity that vouched for e.
func (e AuthenticatedEndpoint) Signer() string { return e.signer }

// SchemaVersion returns the source record schema version e was constructed
// under.
func (e AuthenticatedEndpoint) SchemaVersion() int { return e.schemaVersion }

// IssuedAt returns when the underlying presence lease or advertisement was
// issued.
func (e AuthenticatedEndpoint) IssuedAt() time.Time { return e.issuedAt }

// ReceivedAt returns when this package accepted/validated the proof and
// constructed e.
func (e AuthenticatedEndpoint) ReceivedAt() time.Time { return e.receivedAt }

// ExpiresAt returns when e's underlying proof expires. A zero value means
// "no expiry was supplied" -- only NewLocalAuthenticatedEndpoint permits that
// (an optional presence lease expiry); NewRemoteAuthenticatedEndpoint always
// requires a concrete expiry.
func (e AuthenticatedEndpoint) ExpiresAt() time.Time { return e.expiresAt }

// RouteProof returns the route proof this package accepted for e (a local
// presence lease identifier, or the signed advertisement's route claim).
func (e AuthenticatedEndpoint) RouteProof() string { return e.routeProof }

// TLSIdentity returns e's signed TLS server name/trust domain and SPKI
// SHA-256 pin, and whether e carries one at all (only remote endpoints do).
func (e AuthenticatedEndpoint) TLSIdentity() (RemoteTLSIdentity, bool) {
	if e.tls == nil {
		return RemoteTLSIdentity{}, false
	}
	return *e.tls, true
}

// Expired reports whether e's proof is expired as of now. A zero ExpiresAt
// is treated as "never expires" only for the fields that permit a zero
// expiry; see ExpiresAt.
func (e AuthenticatedEndpoint) Expired(now time.Time) bool {
	return !e.expiresAt.IsZero() && !now.Before(e.expiresAt)
}

// key returns a stable identity key combining provenance, peer identity,
// daemon instance, normalized URL, and (for remote endpoints) SPKI pin.
// GatewayPolicy uses it to bind an issued PeerCredential to exactly the
// AuthenticatedEndpoint it was issued for (ADR-007: "Connection pooling is
// partitioned by peer identity, TLS certificate identity, and policy
// revision; a connection authorized for one peer or SPKI pin cannot be
// reused for another").
func (e AuthenticatedEndpoint) key() string {
	spki := ""
	if e.tls != nil {
		spki = e.tls.SPKI.String()
	}
	return strings.Join([]string{
		string(e.provenance), e.peerIdentity, e.daemonInstance, e.normalized.String(), spki,
	}, "\x00")
}

// LocalEndpointProof is the proof of LOCAL presence ownership
// NewLocalAuthenticatedEndpoint requires, per ADR-007: "a local endpoint
// comes from a local presence record whose file ownership, instance
// identity, lease, peer type, and publication path were validated." Every
// field below is mandatory; a zero-value proof is always rejected.
type LocalEndpointProof struct {
	// OwnerVerified must be true only after the caller has independently
	// confirmed the presence record's file ownership matches the current
	// user (this package does not itself touch the filesystem here -- that
	// validation belongs to the presence package/caller that read the
	// record).
	OwnerVerified bool
	// PeerIdentity is the authenticated local peer's identity/handle.
	PeerIdentity string
	// DaemonInstance is the authenticated local daemon instance identity
	// (for example an internal/identity DaemonInstanceID string form).
	DaemonInstance string
	// LeaseID identifies the live presence lease this endpoint is bound to.
	LeaseID string
	// PublicationPath is the validated on-disk path the presence record was
	// read from (already canonicalized/validated by the caller).
	PublicationPath string
	// Signer is the local credential/signer identity vouching for this
	// record (for example the current-user local authority ID).
	Signer string
	// IssuedAt is when the presence record/lease was issued.
	IssuedAt time.Time
	// ExpiresAt is the presence lease's expiry. Zero means the lease has no
	// expiry -- this is the one field NewLocalAuthenticatedEndpoint accepts
	// as zero.
	ExpiresAt time.Time
}

func (p LocalEndpointProof) validate() error {
	switch {
	case !p.OwnerVerified:
		return fmt.Errorf("%w: local endpoint requires verified presence-record ownership", ErrEndpointProvenance)
	case p.PeerIdentity == "":
		return fmt.Errorf("%w: local endpoint requires a peer identity", ErrEndpointProvenance)
	case p.DaemonInstance == "":
		return fmt.Errorf("%w: local endpoint requires a daemon instance identity", ErrEndpointProvenance)
	case p.LeaseID == "":
		return fmt.Errorf("%w: local endpoint requires a live presence lease", ErrEndpointProvenance)
	case p.PublicationPath == "":
		return fmt.Errorf("%w: local endpoint requires a validated publication path", ErrEndpointProvenance)
	case p.Signer == "":
		return fmt.Errorf("%w: local endpoint requires a signer/credential identity", ErrEndpointProvenance)
	case p.IssuedAt.IsZero():
		return fmt.Errorf("%w: local endpoint requires an issue time", ErrEndpointProvenance)
	}
	return nil
}

// NewLocalAuthenticatedEndpoint constructs an AuthenticatedEndpoint from
// validated proof of LOCAL presence ownership. rawURL is normalized with the
// same NormalizeEndpointURL policy every endpoint URL goes through; a plain
// http/ws scheme is accepted only when the normalized host is an explicit
// loopback literal ("127.0.0.1", "::1", or "localhost") -- ADR-007: "Plain
// http/ws is eligible only for a locally authenticated, same-user loopback
// endpoint." A zero-value (or partially filled) proof always fails; there is
// no way to obtain a local AuthenticatedEndpoint from rawURL alone.
func NewLocalAuthenticatedEndpoint(rawURL string, proof LocalEndpointProof, now time.Time) (AuthenticatedEndpoint, error) {
	if err := proof.validate(); err != nil {
		return AuthenticatedEndpoint{}, err
	}
	if !proof.ExpiresAt.IsZero() && !now.Before(proof.ExpiresAt) {
		return AuthenticatedEndpoint{}, fmt.Errorf("%w: local presence lease expired", ErrEndpointExpired)
	}
	u, err := NormalizeEndpointURL(rawURL)
	if err != nil {
		return AuthenticatedEndpoint{}, err
	}
	scheme := Scheme(strings.ToLower(u.Scheme))
	if scheme == SchemeHTTP || scheme == SchemeWS {
		if !hostIsLoopbackLiteral(u.Hostname()) {
			return AuthenticatedEndpoint{}, fmt.Errorf(
				"%w: plain http/ws local endpoint requires an explicit loopback literal host", ErrEndpointScheme)
		}
	}
	return AuthenticatedEndpoint{
		normalized:     *u,
		peerIdentity:   proof.PeerIdentity,
		daemonInstance: proof.DaemonInstance,
		provenance:     ProvenanceLocal,
		signer:         proof.Signer,
		schemaVersion:  EndpointSchemaVersion,
		issuedAt:       proof.IssuedAt,
		receivedAt:     now,
		expiresAt:      proof.ExpiresAt,
		routeProof:     "local-presence-lease:" + proof.LeaseID,
	}, nil
}

// RemoteAdvertisementProof is the proof of a signed/authenticated LAN
// advertisement NewRemoteAuthenticatedEndpoint requires, per ADR-007: "a
// remote endpoint comes from a signed/authenticated LAN advertisement bound
// to peer identity, daemon instance, route, issue time, expiry,
// nonce/replay decision, TLS server name/trust domain, SPKI SHA-256 ..., and
// the credential/trust domain accepted by internal/lan." Every field below
// is mandatory; a zero-value proof is always rejected.
type RemoteAdvertisementProof struct {
	// PeerIdentity is the authenticated remote peer identity the
	// advertisement was signed for.
	PeerIdentity string
	// DaemonInstance is the authenticated remote daemon instance identity.
	DaemonInstance string
	// Route is the accepted route/interface proof for this advertisement
	// (for example the LAN interface/subnet it was observed on).
	Route string
	// Signer is the signer identity that produced the advertisement
	// signature.
	Signer string
	// IssuedAt is the advertisement's signed issue time.
	IssuedAt time.Time
	// ExpiresAt is the advertisement's signed expiry. Unlike
	// LocalEndpointProof.ExpiresAt, this is mandatory: a remote
	// advertisement with no expiry is never accepted.
	ExpiresAt time.Time
	// Nonce is the advertisement's anti-replay nonce.
	Nonce string
	// ReplayChecked must be true only after the caller has independently
	// checked Nonce against its replay cache and found no prior use. This
	// package does not itself own the replay cache; it only refuses to
	// proceed without an explicit affirmative decision.
	ReplayChecked bool
	// TLS is the signed TLS server name/trust domain and SPKI SHA-256 pin
	// the advertisement carries. TLS.ServerName and a non-zero TLS.SPKI are
	// both mandatory.
	TLS RemoteTLSIdentity
}

func (p RemoteAdvertisementProof) validate(now time.Time) error {
	switch {
	case p.PeerIdentity == "":
		return fmt.Errorf("%w: remote endpoint requires a peer identity", ErrEndpointProvenance)
	case p.DaemonInstance == "":
		return fmt.Errorf("%w: remote endpoint requires a daemon instance identity", ErrEndpointProvenance)
	case p.Route == "":
		return fmt.Errorf("%w: remote endpoint requires a route proof", ErrEndpointProvenance)
	case p.Signer == "":
		return fmt.Errorf("%w: remote endpoint requires a signer identity", ErrEndpointProvenance)
	case p.IssuedAt.IsZero():
		return fmt.Errorf("%w: remote endpoint requires an issue time", ErrEndpointProvenance)
	case p.ExpiresAt.IsZero():
		return fmt.Errorf("%w: remote endpoint requires an expiry time", ErrEndpointProvenance)
	case p.Nonce == "":
		return fmt.Errorf("%w: remote endpoint requires a replay nonce", ErrEndpointProvenance)
	case !p.ReplayChecked:
		return fmt.Errorf("%w: remote endpoint requires an explicit nonce/replay decision", ErrEndpointProvenance)
	case !p.TLS.valid():
		return fmt.Errorf("%w: remote endpoint requires a signed TLS server name and SPKI SHA-256 pin", ErrEndpointTLSIdentity)
	}
	if now.Before(p.IssuedAt) {
		return fmt.Errorf("%w: remote advertisement issued in the future", ErrEndpointProvenance)
	}
	if !now.Before(p.ExpiresAt) {
		return fmt.Errorf("%w: remote advertisement expired", ErrEndpointExpired)
	}
	return nil
}

// NewRemoteAuthenticatedEndpoint constructs an AuthenticatedEndpoint from
// validated proof of a signed/authenticated REMOTE LAN advertisement. rawURL
// is normalized with NormalizeEndpointURL and MUST be https or wss --
// ADR-007: "Remote peer endpoint URLs require https for HTTP/SSE and wss for
// WebSocket." The normalized host must equal the advertisement's signed TLS
// server name (case-insensitively); a mismatch is rejected before any
// AuthenticatedEndpoint is returned. A zero-value (or partially filled)
// proof always fails; there is no way to obtain a remote AuthenticatedEndpoint
// from rawURL alone.
func NewRemoteAuthenticatedEndpoint(rawURL string, proof RemoteAdvertisementProof, now time.Time) (AuthenticatedEndpoint, error) {
	if err := proof.validate(now); err != nil {
		return AuthenticatedEndpoint{}, err
	}
	u, err := NormalizeEndpointURL(rawURL)
	if err != nil {
		return AuthenticatedEndpoint{}, err
	}
	scheme := Scheme(strings.ToLower(u.Scheme))
	if scheme != SchemeHTTPS && scheme != SchemeWSS {
		return AuthenticatedEndpoint{}, fmt.Errorf("%w: remote endpoint requires https/wss", ErrEndpointScheme)
	}
	if !strings.EqualFold(u.Hostname(), proof.TLS.ServerName) {
		return AuthenticatedEndpoint{}, fmt.Errorf(
			"%w: endpoint host %q does not match signed TLS server name %q",
			ErrEndpointTLSIdentity, u.Hostname(), proof.TLS.ServerName)
	}
	tlsID := proof.TLS
	return AuthenticatedEndpoint{
		normalized:     *u,
		peerIdentity:   proof.PeerIdentity,
		daemonInstance: proof.DaemonInstance,
		provenance:     ProvenanceRemote,
		signer:         proof.Signer,
		schemaVersion:  EndpointSchemaVersion,
		issuedAt:       proof.IssuedAt,
		receivedAt:     now,
		expiresAt:      proof.ExpiresAt,
		routeProof:     proof.Route,
		tls:            &tlsID,
	}, nil
}

// hostIsLoopbackLiteral reports whether host is an explicit loopback literal
// ("localhost", or a numeric loopback address). It performs no DNS
// resolution; per ADR-007, "successful DNS resolution" is never itself
// authentication or a loopback grant.
func hostIsLoopbackLiteral(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
