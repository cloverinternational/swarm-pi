package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Swarm-Code/mono/swarm-sdk/gateway"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/lan"
)

func TestHandleProxyDeniesBeforeInspectingRequestOrState(t *testing.T) {
	var trapRequests atomic.Int64
	trap := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		trapRequests.Add(1)
		http.Error(w, "unexpected outbound request", http.StatusInternalServerError)
	}))
	t.Cleanup(trap.Close)

	swarmHome := filepath.Join(t.TempDir(), "nonexistent-swarm-home")
	t.Setenv("SWARM_HOME", swarmHome)

	tests := []struct {
		name string
		req  *http.Request
	}{
		{
			name: "ordinary request",
			req:  httptest.NewRequest(http.MethodPost, trap.URL+"/rpc?peer=trap", nil),
		},
		{
			name: "WebSocket upgrade request",
			req: func() *http.Request {
				req := httptest.NewRequest(http.MethodGet, trap.URL+"/ws?peer=trap", nil)
				req.Header.Set("Connection", "Upgrade")
				req.Header.Set("Upgrade", "websocket")
				return req
			}(),
		},
		{
			name: "nil URL request",
			req: &http.Request{
				Method: http.MethodGet,
				Header: make(http.Header),
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := httptest.NewRecorder()

			var st *state
			st.handleProxy(rec, tt.req)

			res := rec.Result()
			defer res.Body.Close()
			if res.StatusCode != http.StatusServiceUnavailable {
				t.Fatalf("status = %d, want %d", res.StatusCode, http.StatusServiceUnavailable)
			}
			if got := res.Header.Get("Content-Type"); got != "application/json" {
				t.Fatalf("Content-Type = %q, want application/json", got)
			}
			if got := rec.Body.String(); got != lan.UnavailableBody {
				t.Fatalf("body = %q, want %q", got, lan.UnavailableBody)
			}
		})
	}

	if got := trapRequests.Load(); got != 0 {
		t.Fatalf("trap server received %d requests, want 0", got)
	}
	if _, err := os.Stat(swarmHome); !os.IsNotExist(err) {
		t.Fatalf("SWARM_HOME was created or stat failed unexpectedly: %v", err)
	}
}

// ─── Phase 02: policy-bound allow path (ADR-007 / CONTRACT.md) ─────────────
//
// TestHandleProxyDeniesBeforeInspectingRequestOrState above proves the
// Phase 01 denial paths are unaffected when no Phase 02 hooks are installed
// (configureGatewayPolicyForTesting is never called there, so
// gatewayPolicyImpl/gatewayEndpointForRequest stay nil exactly as in
// production). The tests below install those hooks explicitly to prove the
// NEW allow path handleProxy gained in Phase 02, and that every way it can
// still fail keeps the exact same stable authenticated_endpoint_unavailable
// result.

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

// deadlineRecorder wraps httptest.ResponseRecorder and additionally
// implements SetWriteDeadline(time.Time) error, the method
// http.ResponseController looks for. gateway.Server's Handler() wraps its
// entire composed mux with serve.WithResponseWriteBounds, which silently
// returns without dispatching when the underlying http.ResponseWriter does
// not support a write deadline (a plain httptest.ResponseRecorder does
// not) -- see gateway/server_security_test.go's identical helper for the
// full rationale. Driving gw.Handler().ServeHTTP directly (as the
// allow-path tests below do, to mint a real authenticated
// lan.CredentialBinding through gateway.Server's own auth()) therefore
// requires this recorder instead of a bare httptest.NewRecorder().
type deadlineRecorder struct {
	*httptest.ResponseRecorder
}

func newDeadlineRecorder() *deadlineRecorder {
	return &deadlineRecorder{ResponseRecorder: httptest.NewRecorder()}
}

func (d *deadlineRecorder) SetWriteDeadline(time.Time) error { return nil }

// TestHandleProxyPolicyBoundAllowPathSucceeds is the required "succeeds end
// to end without a live remote peer" proof for the standalone command: it
// drives a REAL gateway.Server().Handler() stack (so authentication mints
// the same lan.CredentialBinding production traffic gets) with RPCHandler
// wired to handleProxy exactly like main.go's runGateway does, and proves a
// bearer-authenticated /rpc request reaches a plain httptest.Server standing
// in for the peer -- through the Phase 02 policy hooks, never through base.
func TestHandleProxyPolicyBoundAllowPathSucceeds(t *testing.T) {
	var hits int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		if got := r.Header.Get("Authorization"); got != "Bearer peer-scoped-token" {
			t.Errorf("upstream request Authorization = %q, want the distinct peer-scoped credential", got)
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
	restore := configureGatewayPolicyForTesting(policy, func(*http.Request) (lan.AuthenticatedEndpoint, bool) {
		return endpoint, true
	})
	defer restore()

	const token = "standalone-gateway-token"
	st := &state{swarm: "gwtest-phase02"}
	proxy := http.HandlerFunc(st.handleProxy)
	gw := gateway.New(gateway.Options{
		Token:      token,
		RPCHandler: proxy,
		SSEHandler: proxy,
		WSHandler:  proxy,
	})

	req := httptest.NewRequest(http.MethodGet, "/rpc", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := newDeadlineRecorder()
	gw.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if got := rec.Body.String(); got != "hello from peer" {
		t.Fatalf("body = %q, want %q", got, "hello from peer")
	}
	if got := atomic.LoadInt32(&hits); got != 1 {
		t.Fatalf("upstream hits = %d, want 1", got)
	}
}

// TestHandleProxyPolicyBoundAllowPathStillDeniesWithoutEndpoint proves that
// installing a GatewayPolicy alone is not enough: an endpoint-resolver hook
// that declines (ok=false) keeps the exact Phase 01 denial.
func TestHandleProxyPolicyBoundAllowPathStillDeniesWithoutEndpoint(t *testing.T) {
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
	restore := configureGatewayPolicyForTesting(policy, func(*http.Request) (lan.AuthenticatedEndpoint, bool) {
		return lan.AuthenticatedEndpoint{}, false
	})
	defer restore()

	const token = "standalone-gateway-token-2"
	st := &state{swarm: "gwtest-phase02-no-endpoint"}
	proxy := http.HandlerFunc(st.handleProxy)
	gw := gateway.New(gateway.Options{Token: token, RPCHandler: proxy, SSEHandler: proxy, WSHandler: proxy})

	req := httptest.NewRequest(http.MethodGet, "/rpc", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := newDeadlineRecorder()
	gw.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusServiceUnavailable, rec.Body.String())
	}
	if got := rec.Body.String(); got != lan.UnavailableBody {
		t.Fatalf("body = %q, want stable %q", got, lan.UnavailableBody)
	}
	if got := atomic.LoadInt32(&hits); got != 0 {
		t.Fatalf("upstream reached %d time(s), want 0", got)
	}
}

// TestHandleProxyPolicyBoundAllowPathStillDeniesUnauthenticated proves a
// configured policy+endpoint is still not enough: an UNAUTHENTICATED
// request (missing bearer token) never reaches handleProxy's Phase 02 hooks
// at all -- it is denied by gateway.Server's own auth() before dispatch, so
// this also proves handleProxy's hooks can never bypass authentication.
func TestHandleProxyPolicyBoundAllowPathStillDeniesUnauthenticated(t *testing.T) {
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
	restore := configureGatewayPolicyForTesting(policy, func(*http.Request) (lan.AuthenticatedEndpoint, bool) {
		return endpoint, true
	})
	defer restore()

	const token = "standalone-gateway-token-3"
	st := &state{swarm: "gwtest-phase02-unauth"}
	proxy := http.HandlerFunc(st.handleProxy)
	gw := gateway.New(gateway.Options{Token: token, RPCHandler: proxy, SSEHandler: proxy, WSHandler: proxy})

	req := httptest.NewRequest(http.MethodGet, "/rpc", nil) // no Authorization header
	rec := newDeadlineRecorder()
	gw.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusUnauthorized, rec.Body.String())
	}
	if got := atomic.LoadInt32(&hits); got != 0 {
		t.Fatalf("upstream reached %d time(s), want 0", got)
	}
}

// TestHandleProxyPolicyBoundAllowPathStillDeniesWithoutPolicy proves an
// endpoint-resolver hook alone (no GatewayPolicy installed) keeps the exact
// Phase 01 denial via handleProxy directly.
func TestHandleProxyPolicyBoundAllowPathStillDeniesWithoutPolicy(t *testing.T) {
	now := time.Now()
	endpoint, err := lan.NewLocalAuthenticatedEndpoint("http://127.0.0.1:1", testLocalProof(now), now)
	if err != nil {
		t.Fatalf("NewLocalAuthenticatedEndpoint: %v", err)
	}
	restore := configureGatewayPolicyForTesting(nil, func(*http.Request) (lan.AuthenticatedEndpoint, bool) {
		return endpoint, true
	})
	defer restore()

	st := &state{swarm: "gwtest-phase02-no-policy"}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/rpc", nil)
	st.handleProxy(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusServiceUnavailable, rec.Body.String())
	}
	if got := rec.Body.String(); got != lan.UnavailableBody {
		t.Fatalf("body = %q, want stable %q", got, lan.UnavailableBody)
	}
}
