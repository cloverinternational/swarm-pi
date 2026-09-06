package lan

import (
	"context"
	"crypto/tls"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"
)

func validPrincipal() Principal {
	return Principal{ID: "user-1", CredentialID: "cred-1"}
}

func staticIssuer(token string) PeerCredentialIssuer {
	return PeerCredentialIssuerFunc(func(_ context.Context, _ Principal, endpoint AuthenticatedEndpoint) (PeerCredential, error) {
		return NewPeerBearerCredential(endpoint, token)
	})
}

// ─── Transport success: well-formed LOCAL endpoint ─────────────────────────
//
// This exercises the FULL production dial path (real loopback TCP, no fake
// seams) end-to-end: GatewayPolicy.Transport -> PeerTransport.RoundTrip
// against a real httptest.Server bound to 127.0.0.1.
func TestGatewayPolicy_TransportSucceedsForLocalEndpoint(t *testing.T) {
	var hits int
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		if got := r.Header.Get("Authorization"); got != "Bearer peer-scoped-token" {
			t.Errorf("upstream saw Authorization = %q, want the peer-scoped credential", got)
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	}))
	defer upstream.Close()

	now := time.Now()
	endpoint, err := NewLocalAuthenticatedEndpoint(upstream.URL, validLocalProof(now), now)
	if err != nil {
		t.Fatalf("NewLocalAuthenticatedEndpoint: %v", err)
	}

	policy := NewGatewayPolicy(GatewayPolicyOptions{Issuer: staticIssuer("peer-scoped-token")})
	transport, err := policy.Transport(context.Background(), validPrincipal(), endpoint)
	if err != nil {
		t.Fatalf("Transport denied a well-formed local endpoint: %v", err)
	}
	if transport == nil {
		t.Fatalf("Transport returned a nil PeerTransport with a nil error")
	}

	resp, err := transport.RoundTrip(context.Background(), PeerRequest{Method: http.MethodGet, Path: "/"})
	if err != nil {
		t.Fatalf("RoundTrip: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if hits != 1 {
		t.Fatalf("upstream hits = %d, want 1", hits)
	}
}

// ─── Transport success: well-formed REMOTE https endpoint ──────────────────
//
// Address classification/dial policy for a REMOTE destination correctly
// forbids loopback (ADR-007: "remote peers cannot target loopback... this
// includes IPv4 and IPv6 forms"), so this test proves Transport's complete
// pre-flight authorization chain (principal, endpoint validity/schema,
// scheme/provenance match, TLS identity presence, peer-credential issuance
// and binding) succeeds and returns a usable PeerTransport for a
// structurally well-formed remote https endpoint, without attempting a real
// non-loopback network dial from a sandboxed test environment. The SPKI-pin
// TLS handshake logic remoteDialer produces is separately proven against a
// REAL TLS connection by TestGatewayPolicy_RemoteDialerVerifiesSPKIPin below.
func TestGatewayPolicy_TransportSucceedsForRemoteEndpoint(t *testing.T) {
	now := time.Now()
	endpoint, err := NewRemoteAuthenticatedEndpoint("https://peer.example:9443/rpc", validRemoteProof(now), now)
	if err != nil {
		t.Fatalf("NewRemoteAuthenticatedEndpoint: %v", err)
	}

	policy := NewGatewayPolicy(GatewayPolicyOptions{Issuer: staticIssuer("peer-scoped-token")})
	transport, err := policy.Transport(context.Background(), validPrincipal(), endpoint)
	if err != nil {
		t.Fatalf("Transport denied a well-formed remote endpoint: %v", err)
	}
	if transport == nil {
		t.Fatalf("Transport returned a nil PeerTransport with a nil error")
	}
	pt, ok := transport.(*peerTransport)
	if !ok {
		t.Fatalf("Transport returned %T, want *peerTransport", transport)
	}
	if pt.tlsCfg == nil {
		t.Fatalf("a remote transport must carry a non-nil TLS config")
	}
	if pt.tlsCfg.MinVersion != tls.VersionTLS13 {
		t.Fatalf("tlsCfg.MinVersion = %v, want TLS 1.3", pt.tlsCfg.MinVersion)
	}
	if pt.cred.isZero() || !pt.cred.boundTo(endpoint) {
		t.Fatalf("remote transport must carry peer-scoped credential bound to the selected endpoint")
	}
}

// ─── Explicit denial: unauthenticated principal ─────────────────────────────

func TestGatewayPolicy_TransportDeniesUnauthenticatedPrincipal(t *testing.T) {
	now := time.Now()
	endpoint, err := NewLocalAuthenticatedEndpoint("http://127.0.0.1:9099", validLocalProof(now), now)
	if err != nil {
		t.Fatalf("NewLocalAuthenticatedEndpoint: %v", err)
	}
	policy := NewGatewayPolicy(GatewayPolicyOptions{Issuer: staticIssuer("tok")})

	cases := []struct {
		name      string
		principal Principal
	}{
		{"empty principal", Principal{}},
		{"missing credential id", Principal{ID: "user-1"}},
		{"missing id", Principal{CredentialID: "cred-1"}},
		{"expired", Principal{ID: "user-1", CredentialID: "cred-1", ExpiresAt: now.Add(-time.Minute)}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := policy.Transport(context.Background(), tc.principal, endpoint); err == nil {
				t.Fatalf("expected denial for %s", tc.name)
			}
		})
	}
}

// ─── Explicit denial: plaintext non-loopback ────────────────────────────────
//
// NewLocalAuthenticatedEndpoint already refuses to construct a plain
// http/ws endpoint whose host is not an explicit loopback literal (see
// endpoint_test.go's TestNewLocalAuthenticatedEndpoint_PlainHTTPRequiresLoopbackLiteral),
// so a production caller can never hand Transport a non-loopback plaintext
// AuthenticatedEndpoint in the first place. This test proves
// GatewayPolicy.Transport carries its OWN independent, defense-in-depth
// non-loopback check by constructing a hypothetical malformed endpoint
// directly (this file is inside package lan and can set unexported fields)
// as if some future/alternate constructor produced one, and confirms
// Transport still denies it before any dial.
func TestGatewayPolicy_TransportDeniesPlaintextNonLoopback(t *testing.T) {
	now := time.Now()
	u, err := url.Parse("http://93.184.216.34:9099")
	if err != nil {
		t.Fatalf("url.Parse: %v", err)
	}
	malformed := AuthenticatedEndpoint{
		normalized:     *u,
		peerIdentity:   "peer-1",
		daemonInstance: "instance-1",
		provenance:     ProvenanceLocal,
		signer:         "signer-1",
		schemaVersion:  EndpointSchemaVersion,
		issuedAt:       now.Add(-time.Minute),
		receivedAt:     now,
	}

	policy := NewGatewayPolicy(GatewayPolicyOptions{Issuer: staticIssuer("tok")})
	if _, err := policy.Transport(context.Background(), validPrincipal(), malformed); err == nil {
		t.Fatalf("expected denial for a plaintext non-loopback endpoint")
	}
}

// ─── Explicit denial: missing SPKI (structural) ─────────────────────────────
//
// Constructed the same defense-in-depth way as the plaintext test above:
// NewRemoteAuthenticatedEndpoint can never itself produce an endpoint with a
// nil TLS identity, but Transport must still refuse one if it ever saw it.
func TestGatewayPolicy_TransportDeniesMissingSPKI(t *testing.T) {
	now := time.Now()
	u, err := url.Parse("https://peer.example:9443")
	if err != nil {
		t.Fatalf("url.Parse: %v", err)
	}
	malformed := AuthenticatedEndpoint{
		normalized:     *u,
		peerIdentity:   "peer-1",
		daemonInstance: "instance-1",
		provenance:     ProvenanceRemote,
		signer:         "signer-1",
		schemaVersion:  EndpointSchemaVersion,
		issuedAt:       now.Add(-time.Minute),
		expiresAt:      now.Add(time.Hour),
		receivedAt:     now,
		// tls intentionally left nil: missing SPKI/server-name identity.
	}

	policy := NewGatewayPolicy(GatewayPolicyOptions{Issuer: staticIssuer("tok")})
	if _, err := policy.Transport(context.Background(), validPrincipal(), malformed); err == nil {
		t.Fatalf("expected denial for a remote endpoint missing its TLS identity")
	}
}

// ─── Explicit denial: wrong SPKI (real TLS handshake) ───────────────────────
//
// remoteDialer's *tls.Config is exercised directly against a REAL TLS 1.3
// connection (httptest.NewTLSServer) to prove the SPKI pin is genuinely
// enforced during the handshake: a correct pin succeeds, a mismatched pin
// fails, with no other change. This is independent of, and complementary
// to, address-classification policy (already covered by AddressAllowedRemote's
// own tests) which is why this test dials the server directly rather than
// going through GatewayPolicy.Transport's remote-classification gate.
func TestGatewayPolicy_RemoteDialerVerifiesSPKIPin(t *testing.T) {
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	cert := upstream.Certificate()
	correctSPKI := SPKIFingerprintFromDER(cert.RawSubjectPublicKeyInfo)

	u, err := url.Parse(upstream.URL)
	if err != nil {
		t.Fatalf("url.Parse: %v", err)
	}
	host, _, err := net.SplitHostPort(u.Host)
	if err != nil {
		t.Fatalf("net.SplitHostPort: %v", err)
	}

	policy := NewGatewayPolicy(GatewayPolicyOptions{})

	t.Run("correct pin succeeds", func(t *testing.T) {
		_, cfg := policy.remoteDialer(u, RemoteTLSIdentity{ServerName: host, SPKI: correctSPKI}, time.Now)
		conn, err := net.Dial("tcp", u.Host)
		if err != nil {
			t.Fatalf("net.Dial: %v", err)
		}
		tlsConn := tls.Client(conn, cfg)
		defer tlsConn.Close()
		if err := tlsConn.Handshake(); err != nil {
			t.Fatalf("expected TLS handshake success with the correct SPKI pin, got: %v", err)
		}
	})

	t.Run("wrong pin denied", func(t *testing.T) {
		var wrongSPKI SPKIFingerprint
		copy(wrongSPKI[:], correctSPKI[:])
		wrongSPKI[0] ^= 0xFF // guaranteed to differ from correctSPKI

		_, cfg := policy.remoteDialer(u, RemoteTLSIdentity{ServerName: host, SPKI: wrongSPKI}, time.Now)
		conn, err := net.Dial("tcp", u.Host)
		if err != nil {
			t.Fatalf("net.Dial: %v", err)
		}
		tlsConn := tls.Client(conn, cfg)
		defer tlsConn.Close()
		if err := tlsConn.Handshake(); err == nil {
			t.Fatalf("expected TLS handshake failure for a mismatched SPKI pin, got success")
		}
	})
}

// ─── Peer-scoped authority never available -> fails before any bytes ───────

func TestGatewayPolicy_TransportDeniesWithoutIssuer(t *testing.T) {
	now := time.Now()
	endpoint, err := NewLocalAuthenticatedEndpoint("http://127.0.0.1:9099", validLocalProof(now), now)
	if err != nil {
		t.Fatalf("NewLocalAuthenticatedEndpoint: %v", err)
	}
	policy := NewGatewayPolicy(GatewayPolicyOptions{}) // no Issuer configured
	if _, err := policy.Transport(context.Background(), validPrincipal(), endpoint); err == nil {
		t.Fatalf("expected denial when no peer credential issuer is configured")
	}
}

func TestGatewayPolicy_TransportDeniesWhenIssuerErrors(t *testing.T) {
	now := time.Now()
	endpoint, err := NewLocalAuthenticatedEndpoint("http://127.0.0.1:9099", validLocalProof(now), now)
	if err != nil {
		t.Fatalf("NewLocalAuthenticatedEndpoint: %v", err)
	}
	issuer := PeerCredentialIssuerFunc(func(context.Context, Principal, AuthenticatedEndpoint) (PeerCredential, error) {
		return PeerCredential{}, errCredentialIssuanceFailed
	})
	policy := NewGatewayPolicy(GatewayPolicyOptions{Issuer: issuer})
	if _, err := policy.Transport(context.Background(), validPrincipal(), endpoint); err == nil {
		t.Fatalf("expected denial when the issuer errors")
	}
}

// TestGatewayPolicy_TransportDeniesCredentialBoundToDifferentEndpoint proves
// a credential issued for one endpoint can never silently authorize
// transport to a different one -- the exact "distinct peer-scoped
// authentication bound to the selected AuthenticatedEndpoint" requirement.
func TestGatewayPolicy_TransportDeniesCredentialBoundToDifferentEndpoint(t *testing.T) {
	now := time.Now()
	endpointA, err := NewLocalAuthenticatedEndpoint("http://127.0.0.1:9099", validLocalProof(now), now)
	if err != nil {
		t.Fatalf("NewLocalAuthenticatedEndpoint: %v", err)
	}
	proofB := validLocalProof(now)
	proofB.LeaseID = "lease-2"
	endpointB, err := NewLocalAuthenticatedEndpoint("http://127.0.0.1:9100", proofB, now)
	if err != nil {
		t.Fatalf("NewLocalAuthenticatedEndpoint: %v", err)
	}

	issuer := PeerCredentialIssuerFunc(func(_ context.Context, _ Principal, _ AuthenticatedEndpoint) (PeerCredential, error) {
		// Always issues a credential bound to endpointB, regardless of which
		// endpoint was actually requested.
		return NewPeerBearerCredential(endpointB, "tok")
	})
	policy := NewGatewayPolicy(GatewayPolicyOptions{Issuer: issuer})
	if _, err := policy.Transport(context.Background(), validPrincipal(), endpointA); err == nil {
		t.Fatalf("expected denial when the issued credential is bound to a different endpoint")
	}
}

var errCredentialIssuanceFailed = &staticError{"lan: test peer credential issuance failed"}

type staticError struct{ s string }

func (e *staticError) Error() string { return e.s }
