// ADR-007's narrow gateway policy interface names a single outbound
// capability this package must supply:
//
//	Transport(context.Context, Principal, AuthenticatedEndpoint) (PeerTransport, error)
//
// This file implements it. GatewayPolicy.Transport is the ONLY way a caller
// obtains a PeerTransport: it requires an authenticated Principal (no
// reusable secret) and a valid, unexpired AuthenticatedEndpoint (see
// endpoint.go), verifies the endpoint's scheme is eligible for its
// provenance (remote requires verified https/wss; plain http/ws is local
// same-user loopback only), mints or refuses distinct peer-scoped authority
// bound to that exact endpoint, and returns a PeerTransport that can only
// ever reach the destination(s) validated for that one endpoint. It never
// receives or reuses the inbound gateway credential/session, and it fails
// before any application byte would be sent when peer-scoped authority is
// unavailable.
package lan

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Principal is the authenticated caller identity GatewayPolicy.Transport
// authorizes against, per ADR-007: "Principal contains authenticated
// identity, credential ID, scopes, expiry, and exposure mode, but no
// reusable secret." It never carries the presented credential/session value
// itself.
type Principal struct {
	// ID is the authenticated principal's stable identity.
	ID string
	// CredentialID is the non-secret credential/session identifier that
	// authenticated this principal (see lan.CredentialBinding.ID).
	CredentialID string
	// Scopes is the closed set of capabilities granted to this principal.
	// Selecting a peer/route never broadens it.
	Scopes []string
	// ExpiresAt is when this principal's authentication expires. A zero
	// value means no expiry was bound.
	ExpiresAt time.Time
	// NonLoopback records whether this principal authenticated over a
	// non-loopback listener (evidence/status only; it never itself grants
	// authority).
	NonLoopback bool
}

// Authenticated reports whether p carries a non-empty identity and
// credential ID and, if ExpiresAt is set, has not yet expired as of now. An
// absent or expired Principal is never eligible for Transport.
func (p Principal) Authenticated(now time.Time) bool {
	if p.ID == "" || p.CredentialID == "" {
		return false
	}
	if !p.ExpiresAt.IsZero() && !now.Before(p.ExpiresAt) {
		return false
	}
	return true
}

// HasScope reports whether p was granted scope.
func (p Principal) HasScope(scope string) bool {
	for _, s := range p.Scopes {
		if s == scope {
			return true
		}
	}
	return false
}

// Errors returned by GatewayPolicy.Transport. They are wrapped, not
// replaced, so callers can use errors.Is while still reading a specific
// message; every one denies before any outbound connection is made.
var (
	// ErrPrincipalUnauthenticated denies Transport for an absent or expired
	// Principal.
	ErrPrincipalUnauthenticated = errors.New("lan: principal not authenticated")
	// ErrEndpointUnauthorized denies Transport for an endpoint that is
	// expired, schema-mismatched, or whose scheme/provenance combination is
	// not eligible (plain http/ws for anything but local loopback; anything
	// but https/wss for remote; missing/invalid TLS identity for remote).
	ErrEndpointUnauthorized = errors.New("lan: endpoint not authorized for transport")
	// ErrPeerAuthorityUnavailable denies Transport (or a subsequent
	// RoundTrip/DialWebSocket call) when distinct peer-scoped authority
	// could not be minted or was not bound to the selected endpoint. No
	// application byte is ever sent in this case.
	ErrPeerAuthorityUnavailable = errors.New("lan: peer-scoped authority unavailable")
)

// PeerCredential is opaque, peer-scoped authority bound to exactly one
// AuthenticatedEndpoint. It is never the inbound gateway credential/session:
// the only way to construct a non-zero PeerCredential is
// NewPeerBearerCredential (or an equivalent typed constructor added later),
// always bound to a specific AuthenticatedEndpoint.
type PeerCredential struct {
	boundKey string
	header   string
	value    string
}

// NewPeerBearerCredential builds a peer-scoped bearer credential bound to
// endpoint. token must be non-empty; it is never read back out of the
// returned value except by PeerTransport when it applies the header to an
// outbound peer request.
func NewPeerBearerCredential(endpoint AuthenticatedEndpoint, token string) (PeerCredential, error) {
	if token == "" {
		return PeerCredential{}, fmt.Errorf("%w: empty peer-scoped credential", ErrPeerAuthorityUnavailable)
	}
	return PeerCredential{boundKey: endpoint.key(), header: "Authorization", value: "Bearer " + token}, nil
}

func (c PeerCredential) isZero() bool { return c.boundKey == "" }

func (c PeerCredential) boundTo(e AuthenticatedEndpoint) bool {
	return !c.isZero() && c.boundKey == e.key()
}

func (c PeerCredential) applyTo(h http.Header) {
	if c.header != "" {
		h.Set(c.header, c.value)
	}
}

// PeerCredentialIssuer mints PeerCredential values distinctly scoped to one
// AuthenticatedEndpoint on behalf of an authenticated Principal. It must
// never return, wrap, or derive from the inbound gateway credential/session;
// GatewayPolicy.Transport treats a nil issuer, an error, or a credential not
// bound to the selected endpoint identically -- denial before any transport
// is returned.
type PeerCredentialIssuer interface {
	IssuePeerCredential(ctx context.Context, principal Principal, endpoint AuthenticatedEndpoint) (PeerCredential, error)
}

// PeerCredentialIssuerFunc adapts a function to PeerCredentialIssuer.
type PeerCredentialIssuerFunc func(ctx context.Context, principal Principal, endpoint AuthenticatedEndpoint) (PeerCredential, error)

// IssuePeerCredential implements PeerCredentialIssuer.
func (f PeerCredentialIssuerFunc) IssuePeerCredential(ctx context.Context, principal Principal, endpoint AuthenticatedEndpoint) (PeerCredential, error) {
	return f(ctx, principal, endpoint)
}

// PeerRequest is the allowlisted outbound peer request shape a PeerTransport
// accepts. Header and Query MUST already be pre-sanitized via
// BuildOutboundHeaders/BuildOutboundQuery (plus the closed PeerQueryFields
// allowlist) before being placed here -- PeerTransport itself does not
// re-derive them from any inbound request, and never reads an inbound
// Authorization/Cookie/CSRF/forwarding/hop-by-hop header or raw query byte.
type PeerRequest struct {
	Method string
	Path   string
	Header http.Header
	Query  url.Values
	Body   io.Reader
}

func (r PeerRequest) buildURL(base *url.URL) *url.URL {
	u := *base
	u.Path = singleJoiningSlash(base.Path, r.Path)
	u.RawPath = ""
	if len(r.Query) > 0 {
		u.RawQuery = r.Query.Encode()
	} else {
		u.RawQuery = ""
	}
	u.ForceQuery = false
	u.Fragment = ""
	u.RawFragment = ""
	return &u
}

func singleJoiningSlash(a, b string) string {
	aslash := strings.HasSuffix(a, "/")
	bslash := strings.HasPrefix(b, "/")
	switch {
	case aslash && bslash:
		return a + b[1:]
	case !aslash && !bslash:
		if a == "" {
			return b
		}
		return a + "/" + b
	default:
		return a + b
	}
}

// PeerTransport exposes the bounded HTTP round-trip and WebSocket-dial
// operations ADR-007 describes: "PeerTransport exposes bounded HTTP round
// trip and WebSocket connection operations. It encapsulates
// policy-controlled resolution, redirect handling, and dialing, so an
// adapter cannot validate one address and dial another." Every
// implementation is bound to exactly one AuthenticatedEndpoint and one
// PeerCredential at construction time (see GatewayPolicy.Transport); there
// is no method to redirect it at a different endpoint.
type PeerTransport interface {
	// RoundTrip issues one bounded HTTP request against the transport's
	// bound endpoint and returns its response. It fails, without sending
	// any application byte, if the transport has no bound peer-scoped
	// authority.
	RoundTrip(ctx context.Context, req PeerRequest) (*http.Response, error)
	// DialWebSocket returns a connection (already TLS-verified for a remote
	// endpoint) ready for an allowlisted WebSocket upgrade request to be
	// written to it by the caller. It fails, without sending any
	// application byte, if the transport has no bound peer-scoped
	// authority.
	DialWebSocket(ctx context.Context, req PeerRequest) (net.Conn, error)
}

// peerTransport is the sole production PeerTransport implementation.
type peerTransport struct {
	endpoint AuthenticatedEndpoint
	base     url.URL
	dial     func(ctx context.Context) (net.Conn, error)
	tlsCfg   *tls.Config
	cred     PeerCredential
}

func (t *peerTransport) dialConn(ctx context.Context) (net.Conn, error) {
	conn, err := t.dial(ctx)
	if err != nil {
		return nil, err
	}
	if t.tlsCfg == nil {
		return conn, nil
	}
	tlsConn := tls.Client(conn, t.tlsCfg)
	if err := tlsConn.HandshakeContext(ctx); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("lan: remote peer tls handshake failed: %w", err)
	}
	return tlsConn, nil
}

// RoundTrip implements PeerTransport.
func (t *peerTransport) RoundTrip(ctx context.Context, req PeerRequest) (*http.Response, error) {
	if t.cred.isZero() || !t.cred.boundTo(t.endpoint) {
		return nil, fmt.Errorf("%w: no peer-scoped authority bound to this transport", ErrPeerAuthorityUnavailable)
	}
	u := req.buildURL(&t.base)
	httpReq, err := http.NewRequestWithContext(ctx, req.Method, u.String(), req.Body)
	if err != nil {
		return nil, err
	}
	httpReq.Header = cloneHeader(req.Header)
	t.cred.applyTo(httpReq.Header)

	transport := &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return t.dialConn(ctx)
		},
		// Connection pooling is partitioned by peer identity, TLS
		// certificate identity, and policy revision per ADR-007; disabling
		// keep-alives on this per-endpoint transport is the simplest way to
		// guarantee no connection is ever reused across a different peer or
		// SPKI pin.
		DisableKeepAlives: true,
	}
	client := &http.Client{
		Transport: transport,
		// Redirects are disabled by default for peer RPC/SSE/WebSocket per
		// ADR-007; a specific compatibility contract that enables them owns
		// its own bounded, re-validated redirect loop, not this client.
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	return client.Do(httpReq)
}

// DialWebSocket implements PeerTransport.
func (t *peerTransport) DialWebSocket(ctx context.Context, req PeerRequest) (net.Conn, error) {
	if t.cred.isZero() || !t.cred.boundTo(t.endpoint) {
		return nil, fmt.Errorf("%w: no peer-scoped authority bound to this transport", ErrPeerAuthorityUnavailable)
	}
	return t.dialConn(ctx)
}

func cloneHeader(h http.Header) http.Header {
	out := make(http.Header, len(h))
	for k, v := range h {
		out[k] = append([]string(nil), v...)
	}
	return out
}

func defaultPort(u *url.URL) string {
	if port := u.Port(); port != "" {
		return port
	}
	if Scheme(strings.ToLower(u.Scheme)).secure() {
		return "443"
	}
	return "80"
}

// GatewayPolicyOptions configures NewGatewayPolicy.
type GatewayPolicyOptions struct {
	// RemotePolicy is the closed private-address allow-list applied to
	// every remote peer destination (see RemotePolicy/AddressAllowedRemote).
	RemotePolicy RemotePolicy
	// Issuer mints distinct peer-scoped authority per Transport call. A nil
	// Issuer makes every Transport call fail with
	// ErrPeerAuthorityUnavailable before any outbound connection.
	Issuer PeerCredentialIssuer
	// DialTimeout bounds every dial attempt. Zero uses a 10s default.
	DialTimeout time.Duration
	// Now, when set, overrides time.Now for expiry/validity checks
	// (deterministic tests only).
	Now func() time.Time

	// resolver and dialer are unexported production seams: nil selects
	// net.DefaultResolver and a *net.Dialer respectively. Only in-package
	// tests (same package "lan") can override them, mirroring transport.go's
	// existing resolveAllAnswers/dialApprovedPlan seam so gateway_policy_test.go
	// can prove every denial/allow path with deterministic synthetic
	// resolution and dialing instead of a real network.
	resolver resolver
	dialer   dialer
}

// GatewayPolicy implements ADR-007's GatewayPolicy.Transport capability.
// Production callers construct it with NewGatewayPolicy; there is no zero-
// value production behavior (a zero GatewayPolicy always denies Transport).
type GatewayPolicy struct {
	remote      RemotePolicy
	issuer      PeerCredentialIssuer
	resolver    resolver
	dialer      dialer
	dialTimeout time.Duration
	now         func() time.Time
}

// NewGatewayPolicy constructs a production-wired GatewayPolicy. A nil
// Resolver/Dialer seam (the common case outside this package's own tests)
// uses net.DefaultResolver and a *net.Dialer with DialTimeout.
func NewGatewayPolicy(opts GatewayPolicyOptions) *GatewayPolicy {
	dialTimeout := opts.DialTimeout
	if dialTimeout <= 0 {
		dialTimeout = 10 * time.Second
	}
	now := opts.Now
	if now == nil {
		now = time.Now
	}
	res := opts.resolver
	if res == nil {
		res = net.DefaultResolver
	}
	dl := opts.dialer
	if dl == nil {
		dl = &net.Dialer{Timeout: dialTimeout}
	}
	return &GatewayPolicy{
		remote:      opts.RemotePolicy,
		issuer:      opts.Issuer,
		resolver:    res,
		dialer:      dl,
		dialTimeout: dialTimeout,
		now:         now,
	}
}

// Transport implements ADR-007's GatewayPolicy.Transport capability:
//
//	Transport(context.Context, Principal, AuthenticatedEndpoint) (PeerTransport, error)
//
// It denies -- returning (nil, error) before any outbound connection -- for:
// an unauthenticated/expired principal; an expired or schema-mismatched
// endpoint; a plain http/ws endpoint that is not local-loopback provenance;
// a https/wss endpoint that is not remote provenance or lacks a verified TLS
// identity; any other scheme; and a missing, erroring, or wrongly-bound
// peer-scoped credential. Only after every one of those checks passes does
// it return a PeerTransport, and that transport can still only ever reach
// the destination(s) validated for the selected endpoint.
func (p *GatewayPolicy) Transport(ctx context.Context, principal Principal, endpoint AuthenticatedEndpoint) (PeerTransport, error) {
	now := p.now
	if now == nil {
		now = time.Now
	}
	nowVal := now()

	if !principal.Authenticated(nowVal) {
		return nil, ErrPrincipalUnauthenticated
	}
	if endpoint.Expired(nowVal) {
		return nil, fmt.Errorf("%w: authenticated endpoint expired", ErrEndpointUnauthorized)
	}
	if endpoint.SchemaVersion() != EndpointSchemaVersion {
		return nil, fmt.Errorf("%w: unsupported endpoint schema version %d", ErrEndpointUnauthorized, endpoint.SchemaVersion())
	}

	u := endpoint.URL()
	scheme := Scheme(strings.ToLower(u.Scheme))

	var dial func(ctx context.Context) (net.Conn, error)
	var tlsCfg *tls.Config

	switch scheme {
	case SchemeHTTP, SchemeWS:
		if endpoint.Provenance() != ProvenanceLocal {
			return nil, fmt.Errorf("%w: plain %s transport is local same-user loopback only", ErrEndpointUnauthorized, scheme)
		}
		if !hostIsLoopbackLiteral(u.Hostname()) {
			return nil, fmt.Errorf("%w: local endpoint host is not an explicit loopback literal", ErrEndpointUnauthorized)
		}
		dial = p.loopbackDialer(u)
	case SchemeHTTPS, SchemeWSS:
		if endpoint.Provenance() != ProvenanceRemote {
			return nil, fmt.Errorf("%w: verified %s transport requires remote provenance", ErrEndpointUnauthorized, scheme)
		}
		tlsID, ok := endpoint.TLSIdentity()
		if !ok || !tlsID.valid() {
			return nil, fmt.Errorf("%w: remote endpoint missing verified TLS identity", ErrEndpointUnauthorized)
		}
		d, cfg := p.remoteDialer(u, tlsID, now)
		dial = d
		tlsCfg = cfg
	default:
		return nil, fmt.Errorf("%w: unsupported endpoint scheme %q", ErrEndpointUnauthorized, u.Scheme)
	}

	if p.issuer == nil {
		return nil, fmt.Errorf("%w: no peer credential issuer configured", ErrPeerAuthorityUnavailable)
	}
	cred, err := p.issuer.IssuePeerCredential(ctx, principal, endpoint)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrPeerAuthorityUnavailable, err)
	}
	if cred.isZero() || !cred.boundTo(endpoint) {
		return nil, fmt.Errorf("%w: issued credential not bound to the selected endpoint", ErrPeerAuthorityUnavailable)
	}

	return &peerTransport{
		endpoint: endpoint,
		base:     *u,
		dial:     dial,
		tlsCfg:   tlsCfg,
		cred:     cred,
	}, nil
}

// loopbackDialer returns a dial func that resolves u's host, validates every
// answer is loopback (AddressAllowedLoopbackPeer), and dials only an address
// from that approved plan, re-checking the actual connected address before
// returning it (see transport.go's resolveAllAnswers/dialApprovedPlan).
func (p *GatewayPolicy) loopbackDialer(u *url.URL) func(ctx context.Context) (net.Conn, error) {
	host := u.Hostname()
	port := defaultPort(u)
	return func(ctx context.Context) (net.Conn, error) {
		plan, err := resolveAllAnswers(ctx, p.resolver, host, AddressAllowedLoopbackPeer)
		if err != nil {
			return nil, fmt.Errorf("lan: local loopback endpoint denied: %w", err)
		}
		return dialApprovedPlan(ctx, p.dialer, "tcp", port, plan, AddressAllowedLoopbackPeer)
	}
}

// remoteDialer returns a dial func that resolves u's host, validates every
// answer against p.remote (AddressAllowedRemote), and dials only an address
// from that approved plan, plus a *tls.Config that verifies TLS 1.3, the
// signed server name, certificate validity, and the exact SPKI SHA-256 pin
// before the handshake completes -- ADR-007: "InsecureSkipVerify,
// trust-on-first-use, and accepting an unadvertised certificate are
// forbidden." InsecureSkipVerify is set only to disable Go's default
// CA-chain verification (LAN peer certificates are not expected to chain to
// a system root); VerifyPeerCertificate below performs the actual identity
// check this package requires in its place -- validity window, SAN/hostname
// match, and exact SPKI pin -- so no unadvertised or unpinned certificate is
// ever accepted.
func (p *GatewayPolicy) remoteDialer(u *url.URL, tlsID RemoteTLSIdentity, now func() time.Time) (func(ctx context.Context) (net.Conn, error), *tls.Config) {
	host := u.Hostname()
	port := defaultPort(u)
	allowed := func(ip net.IP) bool { return AddressAllowedRemote(ip, p.remote) }
	dial := func(ctx context.Context) (net.Conn, error) {
		plan, err := resolveAllAnswers(ctx, p.resolver, host, allowed)
		if err != nil {
			return nil, fmt.Errorf("lan: remote endpoint denied: %w", err)
		}
		return dialApprovedPlan(ctx, p.dialer, "tcp", port, plan, allowed)
	}
	spki := tlsID.SPKI
	serverName := tlsID.ServerName
	cfg := &tls.Config{
		MinVersion: tls.VersionTLS13,
		ServerName: serverName,
		// See the function doc comment: default chain verification is
		// replaced, not skipped, by VerifyPeerCertificate below.
		InsecureSkipVerify: true,
		VerifyPeerCertificate: func(rawCerts [][]byte, _ [][]*x509.Certificate) error {
			if len(rawCerts) == 0 {
				return fmt.Errorf("lan: remote peer presented no certificate")
			}
			leaf, err := x509.ParseCertificate(rawCerts[0])
			if err != nil {
				return fmt.Errorf("lan: remote peer certificate unparsable: %w", err)
			}
			at := now()
			if at.Before(leaf.NotBefore) || !at.Before(leaf.NotAfter) {
				return fmt.Errorf("lan: remote peer certificate is not currently valid")
			}
			got := sha256.Sum256(leaf.RawSubjectPublicKeyInfo)
			if SPKIFingerprint(got) != spki {
				return fmt.Errorf("lan: remote peer certificate SPKI does not match the signed advertisement pin")
			}
			if err := leaf.VerifyHostname(serverName); err != nil {
				return fmt.Errorf("lan: remote peer certificate does not cover the signed server name %q: %w", serverName, err)
			}
			return nil
		},
	}
	return dial, cfg
}
