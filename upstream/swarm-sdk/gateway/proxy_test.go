package gateway

// Contract tests for the peer-aware data-route resolver. The mobile PWA sends
// ?peer=<handle> on every /rpc, /sse and /ws request, so resolveDataPeer is the
// single decision point for "whose agent does this request talk to". Two of its
// outcomes are load-bearing and were the source of real mobile bugs:
//
//   - an EXPLICIT ?peer= that is offline / not serve-capable must report
//     offline=true so peerRoute returns HTTP 502 "peer offline" — NOT silently
//     fall back to the gateway's own in-process client (which answered as a
//     DIFFERENT agent; the phone got a reply from the wrong session). The mobile
//     app relies on that 502 to bounce the user back to the peer picker.
//   - ?peer=<self> and the no-peer case must resolve to the gateway itself
//     (offline=false, base=="") so the injected local handler serves the request.
//
// These tests use an isolated, PID-scoped swarm name and register the current
// test process's PID so the peers survive ListPeers' dead-process pruning.
//
// Phase 01 (ADR-007 "Gateway authentication and outbound policy") disables
// raw presence-URL transport: TestProxyTo* below replace the previous tests
// that expected proxyTo to actually reverse-proxy or tunnel to a peer's raw
// ServeURL. They instead prove proxyTo returns the stable
// authenticated_endpoint_unavailable result and makes zero outbound
// connection attempts, for both an ordinary request and a WebSocket upgrade
// request, even when base names a live, reachable, or malformed target.

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Swarm-Code/mono/swarm-sdk/a2a"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/lan"
)

// testSwarm registers the given peers in an isolated swarm and returns the swarm
// name plus a cleanup func. Peers register with the test process's own PID so
// ListPeers' liveness check keeps them.
func testSwarm(t *testing.T, peers ...a2a.PeerPresence) (string, func()) {
	t.Helper()
	name := fmt.Sprintf("gwtest-resolve-%d", os.Getpid())
	for i := range peers {
		if peers[i].PID == 0 {
			peers[i].PID = os.Getpid()
		}
		if peers[i].Type == "" {
			peers[i].Type = a2a.PeerTypeDaemon
		}
		if err := a2a.JoinSwarm(name, peers[i]); err != nil {
			t.Fatalf("JoinSwarm(%s): %v", peers[i].Handle, err)
		}
	}
	handles := make([]string, len(peers))
	for i, p := range peers {
		handles[i] = p.Handle
	}
	return name, func() {
		for _, h := range handles {
			_ = a2a.LeaveSwarm(name, h)
		}
	}
}

func TestResolveDataPeer(t *testing.T) {
	const desktopServe = "http://127.0.0.1:19099"
	swarm, cleanup := testSwarm(t,
		a2a.PeerPresence{Handle: "swarm-desktop-test", ServeURL: desktopServe},
	)
	defer cleanup()

	s := &Server{opts: Options{Swarm: swarm, SelfHandle: "gw-self"}}

	cases := []struct {
		name        string
		peerQuery   string
		prefer      bool
		wantBase    string
		wantOffline bool
	}{
		{"explicit live serve peer", "swarm-desktop-test", false, desktopServe, false},
		{"explicit offline peer -> 502 signal", "ghost-not-here", false, "", true},
		{"explicit self -> serve locally", "gw-self", false, "", false},
		{"no peer, no prefer -> serve locally", "", false, "", false},
		{"no peer, prefer desktop -> desktop peer", "", true, desktopServe, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			url := "/rpc"
			if tc.peerQuery != "" {
				url += "?peer=" + tc.peerQuery
			}
			r := httptest.NewRequest("POST", url, nil)
			base, offline := s.resolveDataPeer(r, tc.prefer)
			if base != tc.wantBase || offline != tc.wantOffline {
				t.Fatalf("resolveDataPeer=(%q,%v), want (%q,%v)",
					base, offline, tc.wantBase, tc.wantOffline)
			}
		})
	}
}

// TestResolveDataPeerServeURLlessPeerIsOffline guards the exact #56 regression:
// a peer that is present in the swarm but advertises NO serve interface (no
// ServeURL, no EndpointURL) must be treated as offline for an explicit ?peer=,
// not silently served by the gateway itself.
func TestResolveDataPeerServeURLlessPeerIsOffline(t *testing.T) {
	swarm, cleanup := testSwarm(t,
		a2a.PeerPresence{Handle: "no-serve-peer"}, // no ServeURL, no EndpointURL
	)
	defer cleanup()

	s := &Server{opts: Options{Swarm: swarm, SelfHandle: "gw-self"}}
	r := httptest.NewRequest("POST", "/rpc?peer=no-serve-peer", nil)
	base, offline := s.resolveDataPeer(r, false)
	if base != "" || !offline {
		t.Fatalf("serve-less explicit peer: got (%q,%v), want (\"\",true)", base, offline)
	}
}

// TestServeBase covers the pure ServeURL/EndpointURL derivation the resolver
// depends on: ServeURL wins; otherwise EndpointURL has its trailing /rpc
// trimmed; a peer with neither yields "".
func TestServeBase(t *testing.T) {
	cases := []struct {
		name string
		p    a2a.PeerPresence
		want string
	}{
		{"serve url wins", a2a.PeerPresence{ServeURL: "http://h:9099", EndpointURL: "http://h:9099/rpc"}, "http://h:9099"},
		{"serve url trailing slash trimmed", a2a.PeerPresence{ServeURL: "http://h:9099/"}, "http://h:9099"},
		{"endpoint url /rpc trimmed", a2a.PeerPresence{EndpointURL: "http://h:9099/rpc"}, "http://h:9099"},
		{"no serve interface", a2a.PeerPresence{Handle: "x"}, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ServeBase(tc.p); got != tc.want {
				t.Fatalf("ServeBase=%q, want %q", got, tc.want)
			}
		})
	}
}

// TestProxyToDeniesWithoutDialingLivePeer proves proxyTo never opens an
// outbound connection: it points base at a real, listening httptest.Server
// that increments a counter on any request, calls proxyTo directly, and
// asserts the counter stays at zero while the response carries the stable
// authenticated_endpoint_unavailable category. This is the connection-
// counter proof ADR-007's Verification section requires ("Tests use
// connection counters to prove this property, not only error strings").
func TestProxyToDeniesWithoutDialingLivePeer(t *testing.T) {
	var hits int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	s := &Server{}
	r := httptest.NewRequest("POST", "/rpc", nil)
	w := httptest.NewRecorder()

	s.proxyTo(w, r, upstream.URL)

	if got := atomic.LoadInt32(&hits); got != 0 {
		t.Fatalf("proxyTo reached the live peer target %d time(s); want 0 outbound attempts (ADR-007 Phase 01 forbids raw presence transport)", got)
	}
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("proxyTo status = %d, want %d", w.Code, http.StatusServiceUnavailable)
	}
	if got := w.Body.String(); got != lan.UnavailableBody {
		t.Fatalf("proxyTo body = %q, want stable %q", got, lan.UnavailableBody)
	}
	if got := w.Header().Get("Content-Type"); got != "application/json" {
		t.Fatalf("proxyTo content-type = %q, want application/json", got)
	}
}

// TestProxyToDeniesPromptlyForUnroutableOrMalformedBase proves proxyTo
// returns immediately -- never attempting to parse base as a URL or dial
// it -- even for inputs that would hang (an unroutable TEST-NET-3 address)
// or panic/fail a naive url.Parse+dial pipeline (garbage, empty, or
// IPv6-literal strings). A regression that reintroduced url.Parse/dial in
// proxyTo would make this test time out rather than merely fail an
// assertion.
func TestProxyToDeniesPromptlyForUnroutableOrMalformedBase(t *testing.T) {
	cases := []string{
		"",
		"not a url at all \x00 with a nul byte",
		"http://203.0.113.10:1/rpc", // TEST-NET-3: reserved, guaranteed unroutable
		"http://[::1]:1/rpc",
		"://missing-scheme",
	}
	for _, base := range cases {
		t.Run(base, func(t *testing.T) {
			s := &Server{}
			r := httptest.NewRequest("GET", "/ws", nil)
			r.Header.Set("Upgrade", "websocket")
			r.Header.Set("Connection", "Upgrade")
			w := httptest.NewRecorder()

			done := make(chan struct{})
			go func() {
				s.proxyTo(w, r, base)
				close(done)
			}()
			select {
			case <-done:
			case <-time.After(2 * time.Second):
				t.Fatalf("proxyTo(base=%q) did not return promptly -- it may be attempting to parse or dial base", base)
			}
			if w.Code != http.StatusServiceUnavailable {
				t.Fatalf("proxyTo(base=%q) status = %d, want %d", base, w.Code, http.StatusServiceUnavailable)
			}
			if got := w.Body.String(); got != lan.UnavailableBody {
				t.Fatalf("proxyTo(base=%q) body = %q, want stable %q", base, got, lan.UnavailableBody)
			}
		})
	}
}

// TestProxyToDeniesWebSocketUpgradeWithoutHijacking proves a WebSocket
// upgrade request through proxyTo is denied with an ordinary HTTP response
// -- no 101 Switching Protocols, no hijack attempt (httptest.ResponseRecorder
// does not implement http.Hijacker; a hijack attempt would panic/error this
// test) -- per ADR-007: raw PeerPresence URLs never become transport for
// WebSocket either.
func TestProxyToDeniesWebSocketUpgradeWithoutHijacking(t *testing.T) {
	s := &Server{}
	r := httptest.NewRequest("GET", "/ws", nil)
	r.Header.Set("Upgrade", "websocket")
	r.Header.Set("Connection", "Upgrade")
	w := httptest.NewRecorder()

	s.proxyTo(w, r, "http://127.0.0.1:1/ws")

	if w.Code == http.StatusSwitchingProtocols {
		t.Fatalf("proxyTo must never upgrade a WebSocket to a raw peer target")
	}
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("proxyTo status = %d, want %d", w.Code, http.StatusServiceUnavailable)
	}
}

// ─── Phase 02: policy-bound allow path (ADR-007 / CONTRACT.md) ─────────────
//
// The tests above prove every Phase 01 denial case is untouched: a bare
// &Server{} (no GatewayPolicy, no EndpointForRequest configured) still
// denies unconditionally, exactly as before. The tests below prove the NEW
// allow path proxyTo gained in Phase 02: when a valid AuthenticatedEndpoint
// and a configured GatewayPolicy are both available AND the request carries
// an authenticated principal, proxyTo now dispatches through
// lan.GatewayPolicy.Transport instead of denying -- end to end, against a
// real httptest.Server standing in for the "peer" -- and every way that
// dispatch can still fail keeps returning the same stable
// authenticated_endpoint_unavailable result.

func testLocalProof(now time.Time) lan.LocalEndpointProof {
	return lan.LocalEndpointProof{
		OwnerVerified:   true,
		PeerIdentity:    "peer-1",
		DaemonInstance:  "instance-1",
		LeaseID:         "lease-1",
		PublicationPath: "/fake/swarm/peers/peer-1.json",
		Signer:          "signer-1",
		IssuedAt:        now.Add(-time.Minute),
	}
}

func withCredentialBinding(r *http.Request, binding lan.CredentialBinding) *http.Request {
	return r.WithContext(context.WithValue(r.Context(), credentialBindingContextKey{}, binding))
}

// TestProxyToPolicyBoundAllowPathSucceeds is the required "succeeds end to
// end without a live remote peer" proof: upstream is a plain httptest.Server
// standing in for the peer, dialed only through a genuine, well-formed
// lan.AuthenticatedEndpoint (local, loopback) and a real lan.GatewayPolicy --
// never base, and never the inbound (absent) Authorization header.
func TestProxyToPolicyBoundAllowPathSucceeds(t *testing.T) {
	var hits int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		if got := r.Header.Get("Authorization"); got != "Bearer peer-scoped-token" {
			t.Errorf("upstream request Authorization = %q, want the distinct peer-scoped credential", got)
		}
		if got := r.Header.Get("Cookie"); got != "" {
			t.Errorf("upstream request must never see an inbound Cookie header, got %q", got)
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("hello from peer"))
	}))
	defer upstream.Close()

	now := time.Now()
	endpoint, err := lan.NewLocalAuthenticatedEndpoint(upstream.URL, testLocalProof(now), now)
	if err != nil {
		t.Fatalf("NewLocalAuthenticatedEndpoint: %v", err)
	}
	policy := lan.NewGatewayPolicy(lan.GatewayPolicyOptions{
		Issuer: lan.PeerCredentialIssuerFunc(func(_ context.Context, _ lan.Principal, e lan.AuthenticatedEndpoint) (lan.PeerCredential, error) {
			return lan.NewPeerBearerCredential(e, "peer-scoped-token")
		}),
	})

	s := &Server{opts: Options{
		GatewayPolicy:      policy,
		EndpointForRequest: func(*http.Request) (lan.AuthenticatedEndpoint, bool) { return endpoint, true },
	}}

	r := httptest.NewRequest("GET", "/rpc", nil)
	r.Header.Set("Cookie", "session=must-never-forward")
	r = withCredentialBinding(r, lan.CredentialBinding{ID: "cred-1", Revision: 1})
	w := httptest.NewRecorder()

	s.proxyTo(w, r, "")

	if w.Code != http.StatusOK {
		t.Fatalf("proxyTo status = %d, want 200; body=%s", w.Code, w.Body.String())
	}
	if got := w.Body.String(); got != "hello from peer" {
		t.Fatalf("proxyTo body = %q, want %q", got, "hello from peer")
	}
	if got := atomic.LoadInt32(&hits); got != 1 {
		t.Fatalf("upstream hits = %d, want 1", got)
	}
}

// TestProxyToPolicyBoundAllowPathStillDeniesWithoutEndpoint proves that
// configuring a GatewayPolicy alone is not enough: EndpointForRequest
// declining (ok=false) keeps the exact Phase 01 denial.
func TestProxyToPolicyBoundAllowPathStillDeniesWithoutEndpoint(t *testing.T) {
	var hits int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	policy := lan.NewGatewayPolicy(lan.GatewayPolicyOptions{
		Issuer: lan.PeerCredentialIssuerFunc(func(_ context.Context, _ lan.Principal, e lan.AuthenticatedEndpoint) (lan.PeerCredential, error) {
			return lan.NewPeerBearerCredential(e, "tok")
		}),
	})
	s := &Server{opts: Options{
		GatewayPolicy:      policy,
		EndpointForRequest: func(*http.Request) (lan.AuthenticatedEndpoint, bool) { return lan.AuthenticatedEndpoint{}, false },
	}}

	r := withCredentialBinding(httptest.NewRequest("GET", "/rpc", nil), lan.CredentialBinding{ID: "cred-1", Revision: 1})
	w := httptest.NewRecorder()
	s.proxyTo(w, r, upstream.URL)

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusServiceUnavailable)
	}
	if got := w.Body.String(); got != lan.UnavailableBody {
		t.Fatalf("body = %q, want stable %q", got, lan.UnavailableBody)
	}
	if got := atomic.LoadInt32(&hits); got != 0 {
		t.Fatalf("upstream reached %d time(s), want 0", got)
	}
}

// TestProxyToPolicyBoundAllowPathStillDeniesWithoutAuthenticatedPrincipal
// proves a configured policy+endpoint alone is still not enough: a request
// with no bound credential (unauthenticated) keeps the exact Phase 01
// denial and never reaches lan.GatewayPolicy.Transport.
func TestProxyToPolicyBoundAllowPathStillDeniesWithoutAuthenticatedPrincipal(t *testing.T) {
	var hits int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	now := time.Now()
	endpoint, err := lan.NewLocalAuthenticatedEndpoint(upstream.URL, testLocalProof(now), now)
	if err != nil {
		t.Fatalf("NewLocalAuthenticatedEndpoint: %v", err)
	}
	policy := lan.NewGatewayPolicy(lan.GatewayPolicyOptions{
		Issuer: lan.PeerCredentialIssuerFunc(func(_ context.Context, _ lan.Principal, e lan.AuthenticatedEndpoint) (lan.PeerCredential, error) {
			return lan.NewPeerBearerCredential(e, "tok")
		}),
	})
	s := &Server{opts: Options{
		GatewayPolicy:      policy,
		EndpointForRequest: func(*http.Request) (lan.AuthenticatedEndpoint, bool) { return endpoint, true },
	}}

	r := httptest.NewRequest("GET", "/rpc", nil) // no bound credential in context
	w := httptest.NewRecorder()
	s.proxyTo(w, r, "")

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusServiceUnavailable)
	}
	if got := w.Body.String(); got != lan.UnavailableBody {
		t.Fatalf("body = %q, want stable %q", got, lan.UnavailableBody)
	}
	if got := atomic.LoadInt32(&hits); got != 0 {
		t.Fatalf("upstream reached %d time(s), want 0", got)
	}
}

// TestProxyToPolicyBoundAllowPathStillDeniesWithoutPolicy proves an endpoint
// resolver alone (no GatewayPolicy) keeps the exact Phase 01 denial.
func TestProxyToPolicyBoundAllowPathStillDeniesWithoutPolicy(t *testing.T) {
	now := time.Now()
	endpoint, err := lan.NewLocalAuthenticatedEndpoint("http://127.0.0.1:1", testLocalProof(now), now)
	if err != nil {
		t.Fatalf("NewLocalAuthenticatedEndpoint: %v", err)
	}
	s := &Server{opts: Options{
		EndpointForRequest: func(*http.Request) (lan.AuthenticatedEndpoint, bool) { return endpoint, true },
	}}
	r := withCredentialBinding(httptest.NewRequest("GET", "/rpc", nil), lan.CredentialBinding{ID: "cred-1", Revision: 1})
	w := httptest.NewRecorder()
	s.proxyTo(w, r, "")

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusServiceUnavailable)
	}
	if got := w.Body.String(); got != lan.UnavailableBody {
		t.Fatalf("body = %q, want stable %q", got, lan.UnavailableBody)
	}
}
