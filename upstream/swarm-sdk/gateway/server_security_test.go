package gateway

// ADR-007 gateway authentication and listener-grant tests.
//
// These exercise gateway.Server end to end via its real http.Handler():
// restrictive listener grants (loopback-only implicit bind; non-loopback
// requires provisioned credentials + verified TLS or the explicit
// insecure_static_only override), the five-state credential matrix
// (absent/invalid/expired/revoked/valid) applied uniformly whether the
// credential arrives as a bearer header or a browser session cookie,
// unconditional query-credential rejection, the immutable
// authenticated_control / insecure_static_only route masks, HTTPS-only
// browser session bootstrap (even on loopback), session cookie attributes,
// and Origin/Host/CSRF enforcement. Every denial path is paired with a
// downstream-call counter proving no proxy/business-logic handler ever runs
// on a denied request.

import (
	"bufio"
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Swarm-Code/mono/swarm-sdk/a2a"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/lan"
	"github.com/Swarm-Code/mono/swarm-sdk/serve"
)

// counterHandler counts every request that actually reaches it, so tests can
// assert "zero downstream calls on denial" instead of only checking status
// codes / error strings.
type counterHandler struct{ n int64 }

func (c *counterHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	atomic.AddInt64(&c.n, 1)
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(`{"ok":true}`))
}

func (c *counterHandler) count() int64 { return atomic.LoadInt64(&c.n) }

// tlsRequest marks r as if it arrived over a verified TLS connection, the
// way it would on a real *http.Server configured with ListenAndServeTLS.
func tlsRequest(r *http.Request) *http.Request {
	r.TLS = &tls.ConnectionState{HandshakeComplete: true}
	return r
}

func testAuthority(t *testing.T, token string, expires time.Time) *lan.CredentialAuthority {
	t.Helper()
	a, err := lan.NewMemoryCredentialAuthority([]lan.CredentialRecord{{
		ID: "test-client", Secret: token, Revision: 1, ExpiresAt: expires,
	}})
	if err != nil {
		t.Fatal(err)
	}
	return a
}

func testTLSEvidence(t *testing.T, now time.Time, identity string) *lan.ListenerTLSEvidence {
	t.Helper()
	dir := t.TempDir()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: identity},
		NotBefore:    now.Add(-time.Hour),
		NotAfter:     now.Add(time.Hour),
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		KeyUsage:     x509.KeyUsageDigitalSignature,
	}
	if ip := net.ParseIP(identity); ip != nil {
		template.IPAddresses = []net.IP{ip}
	} else {
		template.DNSNames = []string{identity}
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	certPath := filepath.Join(dir, "listener.crt")
	keyPath := filepath.Join(dir, "listener.key")
	if err := os.WriteFile(certPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keyPath, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER}), 0o600); err != nil {
		t.Fatal(err)
	}
	evidence, err := lan.LoadListenerTLSEvidence(certPath, keyPath, net.JoinHostPort(identity, "8787"), now)
	if err != nil {
		t.Fatal(err)
	}
	return evidence
}

// ─── Listener grants ──────────────────────────────────────────────────────

func TestIsLoopbackAddr(t *testing.T) {
	cases := []struct {
		addr string
		want bool
	}{
		{"127.0.0.1:8787", true},
		{"127.5.5.5:8787", true}, // whole 127.0.0.0/8 is loopback
		{"localhost:8787", true},
		{"LOCALHOST:8787", true},
		{"[::1]:8787", true},
		{":8787", false},        // unspecified port-only form: non-loopback
		{"0.0.0.0:8787", false}, // wildcard IPv4
		{"[::]:8787", false},    // wildcard IPv6
		{"192.168.1.5:8787", false},
		{"example.com:8787", false}, // unresolved hostname: never assumed loopback
	}
	for _, tc := range cases {
		t.Run(tc.addr, func(t *testing.T) {
			if got := IsLoopbackAddr(tc.addr); got != tc.want {
				t.Fatalf("IsLoopbackAddr(%q)=%v, want %v", tc.addr, got, tc.want)
			}
		})
	}
}

func TestAuthorizeListener_LoopbackIsOnlyImplicitGrant(t *testing.T) {
	grant, err := AuthorizeListener("", Options{})
	if err != nil {
		t.Fatalf("empty addr (unspecified bind address) must default to loopback, got err: %v", err)
	}
	if !grant.Loopback || grant.Mask != AuthenticatedControlMask {
		t.Fatalf("empty-addr grant = %+v, want loopback authenticated_control", grant)
	}

	grant, err = AuthorizeListener("127.0.0.1:8787", Options{})
	if err != nil || !grant.Loopback || grant.Mask != AuthenticatedControlMask {
		t.Fatalf("explicit loopback grant = %+v, err=%v, want loopback authenticated_control, nil", grant, err)
	}
}

func TestAuthorizeListener_NonLoopbackWithoutCredentialsDenied(t *testing.T) {
	_, err := AuthorizeListener("0.0.0.0:8787", Options{})
	if err != ErrCredentialsRequired {
		t.Fatalf("non-loopback bind with no Token and no Insecure override: err=%v, want ErrCredentialsRequired", err)
	}
}

func TestAuthorizeListener_OpenLANControlIsExplicit(t *testing.T) {
	grant, err := AuthorizeListener(":8787", Options{AllowUnauthenticatedLAN: true})
	if err != nil {
		t.Fatalf("open LAN control must be granted when explicitly requested: %v", err)
	}
	if grant.Loopback || grant.Mask != AuthenticatedControlMask || !grant.AllowUnauthenticated {
		t.Fatalf("grant=%+v, want open non-loopback authenticated_control grant", grant)
	}
}

func TestAuthorizeListener_NonLoopbackWithoutTLSDenied(t *testing.T) {
	now := time.Now()
	_, err := AuthorizeListener("0.0.0.0:8787", Options{
		Authority: testAuthority(t, "creds", now.Add(time.Hour)),
	})
	if err != ErrTLSRequired {
		t.Fatalf("non-loopback bind with credentials but no TLS: err=%v, want ErrTLSRequired", err)
	}
}

func TestAuthorizeListener_NonLoopbackAuthenticatedControl(t *testing.T) {
	now := time.Now()
	grant, err := AuthorizeListener("0.0.0.0:8787", Options{
		Authority:          testAuthority(t, "creds", now.Add(time.Hour)),
		TLSEvidence:        testTLSEvidence(t, now, "192.0.2.10"),
		AdvertisedListener: "192.0.2.10:8787",
	})
	if err != nil {
		t.Fatalf("valid credentials + TLS must be granted, got err: %v", err)
	}
	if grant.Loopback || grant.Mask != AuthenticatedControlMask || !grant.RequireTLS {
		t.Fatalf("grant = %+v, want non-loopback authenticated_control with RequireTLS", grant)
	}
}

func TestAuthorizeListener_LegacyAssertionsCannotAuthorizeNonLoopback(t *testing.T) {
	_, err := AuthorizeListener("0.0.0.0:8787", Options{
		Token:         "arbitrary-secret",
		TLSConfigured: true,
	})
	if err != ErrCredentialsRequired {
		t.Fatalf("legacy Token+TLSConfigured err=%v, want ErrCredentialsRequired", err)
	}
}

func TestAuthorizeListener_ExplicitInsecureOverrideIsNeverInferred(t *testing.T) {
	// Empty token, wildcard bind, and no TLS must NOT silently become
	// insecure_static_only -- it must be denied outright.
	if _, err := AuthorizeListener("0.0.0.0:8787", Options{}); err == nil {
		t.Fatal("absent credentials must deny, never silently fall back to insecure_static_only")
	}
	// Only an explicit Insecure:true grants it, and it wins even when valid
	// credentials/TLS are ALSO present -- it is never escalated back to
	// authenticated_control by anything short of a fresh grant.
	grant, err := AuthorizeListener("0.0.0.0:8787", Options{Insecure: true, Token: "creds", TLSConfigured: true})
	if err != nil {
		t.Fatalf("explicit insecure override must be granted, got err: %v", err)
	}
	if grant.Mask != InsecureStaticOnlyMask || !grant.Insecure {
		t.Fatalf("grant = %+v, want immutable insecure_static_only", grant)
	}
}

func TestRouteMask_Immutable(t *testing.T) {
	if AuthenticatedControlMask.AllowsSensitive() != true {
		t.Fatal("authenticated_control must allow sensitive routes")
	}
	if InsecureStaticOnlyMask.AllowsSensitive() != false {
		t.Fatal("insecure_static_only must never allow sensitive routes")
	}
	if AuthenticatedControlMask.String() != "authenticated_control" {
		t.Fatalf("AuthenticatedControlMask.String()=%q", AuthenticatedControlMask.String())
	}
	if InsecureStaticOnlyMask.String() != "insecure_static_only" {
		t.Fatalf("InsecureStaticOnlyMask.String()=%q", InsecureStaticOnlyMask.String())
	}
}

// ─── Credential state matrix (absent/invalid/expired/revoked/valid) ──────

func TestBearerCredentialMatrix_SensitiveRoute(t *testing.T) {
	const token = "the-real-gateway-credential"

	newServerWithCounter := func(opts Options) (*Server, *counterHandler) {
		ch := &counterHandler{}
		opts.RPCHandler = ch
		return New(opts), ch
	}

	t.Run("absent", func(t *testing.T) {
		srv, ch := newServerWithCounter(Options{Token: token})
		w := newDeadlineRecorder()
		r := httptest.NewRequest(http.MethodPost, "/rpc", nil)
		srv.Handler().ServeHTTP(w, r)
		assertDenied(t, w.ResponseRecorder, http.StatusUnauthorized, "unauthenticated")
		if ch.count() != 0 {
			t.Fatalf("absent credential must not reach downstream handler, count=%d", ch.count())
		}
	})

	t.Run("invalid", func(t *testing.T) {
		srv, ch := newServerWithCounter(Options{Token: token})
		w := newDeadlineRecorder()
		r := httptest.NewRequest(http.MethodPost, "/rpc", nil)
		r.Header.Set("Authorization", "Bearer wrong-guess")
		srv.Handler().ServeHTTP(w, r)
		assertDenied(t, w.ResponseRecorder, http.StatusUnauthorized, "unauthenticated")
		if ch.count() != 0 {
			t.Fatalf("invalid credential must not reach downstream handler, count=%d", ch.count())
		}
	})

	t.Run("expired", func(t *testing.T) {
		srv, ch := newServerWithCounter(Options{Token: token, CredentialExpiresAt: time.Now().Add(-time.Hour)})
		w := newDeadlineRecorder()
		r := httptest.NewRequest(http.MethodPost, "/rpc", nil)
		r.Header.Set("Authorization", "Bearer "+token)
		srv.Handler().ServeHTTP(w, r)
		assertDenied(t, w.ResponseRecorder, http.StatusUnauthorized, "expired")
		if ch.count() != 0 {
			t.Fatalf("expired credential must not reach downstream handler, count=%d", ch.count())
		}
	})

	t.Run("revoked", func(t *testing.T) {
		srv, ch := newServerWithCounter(Options{Token: token, CredentialRevoked: true})
		w := newDeadlineRecorder()
		r := httptest.NewRequest(http.MethodPost, "/rpc", nil)
		r.Header.Set("Authorization", "Bearer "+token)
		srv.Handler().ServeHTTP(w, r)
		assertDenied(t, w.ResponseRecorder, http.StatusUnauthorized, "revoked")
		if ch.count() != 0 {
			t.Fatalf("revoked credential must not reach downstream handler, count=%d", ch.count())
		}
	})

	t.Run("valid", func(t *testing.T) {
		srv, ch := newServerWithCounter(Options{Token: token})
		w := newDeadlineRecorder()
		r := httptest.NewRequest(http.MethodPost, "/rpc", nil)
		r.Header.Set("Authorization", "Bearer "+token)
		srv.Handler().ServeHTTP(w, r)
		if w.Code != http.StatusOK {
			t.Fatalf("valid credential: status=%d body=%s, want 200", w.Code, w.Body.String())
		}
		if ch.count() != 1 {
			t.Fatalf("valid credential must reach downstream handler exactly once, count=%d", ch.count())
		}
	})
}

// TestBearerCredentialMatrix_LoopbackAndNonLoopbackParity proves the exact
// same five-state matrix applies identically on a loopback grant and a
// granted non-loopback authenticated_control grant -- loopback bind confers
// no authentication.
func TestBearerCredentialMatrix_LoopbackAndNonLoopbackParity(t *testing.T) {
	const token = "shared-secret"
	now := time.Now()
	grants := map[string]Options{
		"loopback": {Token: token, ListenAddr: "127.0.0.1:8787"},
		"non-loopback": {
			ListenAddr:         "0.0.0.0:8787",
			Authority:          testAuthority(t, token, now.Add(time.Hour)),
			TLSEvidence:        testTLSEvidence(t, now, "192.0.2.10"),
			AdvertisedListener: "192.0.2.10:8787",
		},
	}
	for name, base := range grants {
		t.Run(name, func(t *testing.T) {
			ch := &counterHandler{}
			opts := base
			opts.RPCHandler = ch
			srv := New(opts)
			w := newDeadlineRecorder()
			r := httptest.NewRequest(http.MethodPost, "/rpc", nil) // absent credential
			srv.Handler().ServeHTTP(w, r)
			assertDenied(t, w.ResponseRecorder, http.StatusUnauthorized, "unauthenticated")
			if ch.count() != 0 {
				t.Fatalf("%s: absent credential must deny before dispatch, count=%d", name, ch.count())
			}

			ch2 := &counterHandler{}
			opts2 := base
			opts2.RPCHandler = ch2
			srv2 := New(opts2)
			w2 := newDeadlineRecorder()
			r2 := httptest.NewRequest(http.MethodPost, "/rpc", nil)
			r2.Header.Set("Authorization", "Bearer "+token)
			srv2.Handler().ServeHTTP(w2, r2)
			if w2.Code != http.StatusOK || ch2.count() != 1 {
				t.Fatalf("%s: valid credential must dispatch exactly once, status=%d count=%d", name, w2.Code, ch2.count())
			}
		})
	}
}

// ─── Query credentials: always rejected, even alongside a valid header ────

func TestQueryCredential_AlwaysRejected(t *testing.T) {
	const token = "the-real-gateway-credential"

	t.Run("query only", func(t *testing.T) {
		ch := &counterHandler{}
		srv := New(Options{Token: token, RPCHandler: ch})
		w := newDeadlineRecorder()
		r := httptest.NewRequest(http.MethodPost, "/rpc?token="+token, nil)
		srv.Handler().ServeHTTP(w, r)
		assertDenied(t, w.ResponseRecorder, http.StatusUnauthorized, "query_credential")
		if ch.count() != 0 {
			t.Fatalf("query-only credential must not reach downstream handler, count=%d", ch.count())
		}
	})

	t.Run("query alongside a VALID header must still be denied", func(t *testing.T) {
		ch := &counterHandler{}
		srv := New(Options{Token: token, RPCHandler: ch})
		w := newDeadlineRecorder()
		r := httptest.NewRequest(http.MethodPost, "/rpc?token="+token, nil)
		r.Header.Set("Authorization", "Bearer "+token)
		srv.Handler().ServeHTTP(w, r)
		assertDenied(t, w.ResponseRecorder, http.StatusUnauthorized, "query_credential")
		if ch.count() != 0 {
			t.Fatalf("presence of a query credential must deny even with a valid header present, count=%d", ch.count())
		}
	})
}

// ─── insecure_static_only: immutable route mask ────────────────────────────

func TestInsecureStaticOnly_MasksSensitiveRoutes(t *testing.T) {
	ch := &counterHandler{}
	srv := New(Options{
		ListenAddr: "0.0.0.0:8787",
		Insecure:   true,
		RPCHandler: ch,
	})
	h := srv.Handler()

	sensitivePaths := []struct {
		method, path string
	}{
		{http.MethodPost, "/rpc"},
		{http.MethodGet, "/sse"},
		{http.MethodGet, "/ws"},
		{http.MethodGet, "/api/peers"},
		{http.MethodPost, "/api/select"},
		{http.MethodPost, "/api/session/bootstrap"},
	}
	for _, sp := range sensitivePaths {
		t.Run(sp.path, func(t *testing.T) {
			w := newDeadlineRecorder()
			r := httptest.NewRequest(sp.method, sp.path, nil)
			// Even a caller presenting a bearer credential must be denied --
			// the static-only mask is immutable and does not authenticate
			// its way into control routes.
			r.Header.Set("Authorization", "Bearer whatever")
			h.ServeHTTP(w, r)
			if w.Code != http.StatusForbidden {
				t.Fatalf("%s %s: status=%d, want 403", sp.method, sp.path, w.Code)
			}
			var body map[string]string
			if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
				t.Fatalf("body not JSON: %v", err)
			}
			if body["error"] != "insecure_static_only" {
				t.Fatalf("%s %s: error=%q, want insecure_static_only", sp.method, sp.path, body["error"])
			}
		})
	}
	if ch.count() != 0 {
		t.Fatalf("no sensitive-route request may ever reach the downstream handler under insecure_static_only, count=%d", ch.count())
	}

	// Public subset remains reachable.
	w := newDeadlineRecorder()
	srv.Handler().ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/version", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("/api/version must remain public under insecure_static_only, status=%d", w.Code)
	}

	w2 := newDeadlineRecorder()
	srv.Handler().ServeHTTP(w2, httptest.NewRequest(http.MethodGet, "/", nil))
	if w2.Code != http.StatusOK {
		t.Fatalf("static assets must remain public under insecure_static_only, status=%d", w2.Code)
	}
}

func TestGatewayUnavailable_FailsClosedIncludingStaticAssets(t *testing.T) {
	// Non-loopback, no credentials, no TLS, no explicit insecure override:
	// AuthorizeListener denies the grant outright. Handler() must fail
	// closed for EVERY path, including static assets and /api/version --
	// this is not the insecure_static_only grant, it is no grant at all.
	srv := New(Options{ListenAddr: "0.0.0.0:8787"})
	h := srv.Handler()

	for _, path := range []string{"/", "/api/version", "/api/peers", "/rpc"} {
		w := newDeadlineRecorder()
		h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
		if w.Code != http.StatusServiceUnavailable {
			t.Fatalf("%s: status=%d, want 503 gateway_unavailable", path, w.Code)
		}
	}
}

// ─── Browser HTTPS-only bootstrap/session, cookie attributes, CSRF, Origin ─

const (
	testOrigin = "https://127.0.0.1:8787"
	testHost   = "127.0.0.1:8787"
)

func newBrowserTestServer(token string) (*Server, *counterHandler, *counterHandler) {
	rpcCounter := &counterHandler{}
	selectCounter := &counterHandler{}
	srv := New(Options{
		Token:          token,
		AllowedOrigins: []string{testOrigin},
		AllowedHosts:   []string{testHost},
		RPCHandler:     rpcCounter,
		SSEHandler:     rpcCounter,
		WSHandler:      rpcCounter,
		Select: func(h string) {
			atomic.AddInt64(&selectCounter.n, 1)
		},
	})
	return srv, rpcCounter, selectCounter
}

func TestSessionBootstrap_RequiresVerifiedHTTPSEvenOnLoopback(t *testing.T) {
	srv, _, _ := newBrowserTestServer("the-real-credential")
	h := srv.Handler()

	w := newDeadlineRecorder()
	r := httptest.NewRequest(http.MethodPost, "/api/session/bootstrap", nil) // no r.TLS: plaintext, even though this is effectively loopback
	r.Header.Set("Authorization", "Bearer the-real-credential")
	h.ServeHTTP(w, r)

	assertDenied(t, w.ResponseRecorder, http.StatusUnauthorized, "browser_https_required")
	if w.Result().Header.Get("Set-Cookie") != "" {
		t.Fatal("no session cookie may be set over a plaintext connection")
	}
}

func TestSessionBootstrap_IssuesStrictCookieOverHTTPS(t *testing.T) {
	srv, _, _ := newBrowserTestServer("the-real-credential")
	h := srv.Handler()

	w := newDeadlineRecorder()
	r := tlsRequest(httptest.NewRequest(http.MethodPost, "/api/session/bootstrap", nil))
	r.Header.Set("Authorization", "Bearer the-real-credential")
	r.Header.Set("Origin", testOrigin)
	r.Host = testHost
	h.ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("bootstrap over verified HTTPS with valid credential + matching Origin/Host: status=%d body=%s", w.Code, w.Body.String())
	}
	resp := w.Result()
	cookies := resp.Cookies()
	if len(cookies) != 1 {
		t.Fatalf("expected exactly one Set-Cookie, got %d", len(cookies))
	}
	c := cookies[0]
	if c.Name != serve.SessionCookieName {
		t.Fatalf("cookie name=%q, want %q", c.Name, serve.SessionCookieName)
	}
	if !c.HttpOnly {
		t.Error("session cookie must be HttpOnly")
	}
	if !c.Secure {
		t.Error("session cookie must always be Secure")
	}
	if c.SameSite != http.SameSiteStrictMode {
		t.Errorf("session cookie SameSite=%v, want Strict", c.SameSite)
	}

	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("bootstrap response not JSON: %v", err)
	}
	if _, ok := body["csrf"]; !ok {
		t.Fatal("bootstrap response must include a csrf token")
	}
	// The credential itself must never appear in the response body.
	if strings.Contains(w.Body.String(), "the-real-credential") {
		t.Fatal("bootstrap response must never echo the raw credential")
	}
}

func TestSessionBootstrap_RejectsQueryCredentialAndMismatchedOrigin(t *testing.T) {
	srv, _, _ := newBrowserTestServer("the-real-credential")
	h := srv.Handler()

	w := newDeadlineRecorder()
	r := tlsRequest(httptest.NewRequest(http.MethodPost, "/api/session/bootstrap?token=the-real-credential", nil))
	h.ServeHTTP(w, r)
	assertDenied(t, w.ResponseRecorder, http.StatusUnauthorized, "query_credential")

	w2 := newDeadlineRecorder()
	r2 := tlsRequest(httptest.NewRequest(http.MethodPost, "/api/session/bootstrap", nil))
	r2.Header.Set("Authorization", "Bearer the-real-credential")
	r2.Header.Set("Origin", "https://evil.example.com")
	r2.Host = testHost
	h.ServeHTTP(w2, r2)
	assertDenied(t, w2.ResponseRecorder, http.StatusUnauthorized, "origin")
}

// bootstrapSession drives a full bootstrap exchange and returns the minted
// session cookie value and CSRF token for use by subsequent requests.
func bootstrapSession(t *testing.T, h http.Handler, token string) (sessionID, csrf string) {
	t.Helper()
	w := newDeadlineRecorder()
	r := tlsRequest(httptest.NewRequest(http.MethodPost, "/api/session/bootstrap", nil))
	r.Header.Set("Authorization", "Bearer "+token)
	r.Header.Set("Origin", testOrigin)
	r.Host = testHost
	h.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("bootstrap failed: status=%d body=%s", w.Code, w.Body.String())
	}
	cookies := w.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatalf("expected one cookie, got %d", len(cookies))
	}
	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("bad bootstrap body: %v", err)
	}
	csrfVal, _ := body["csrf"].(string)
	return cookies[0].Value, csrfVal
}

func TestCookieSession_ReadOnlyRoute_RequiresHTTPSOriginHostNoCSRF(t *testing.T) {
	srv, rpcCounter, _ := newBrowserTestServer("the-real-credential")
	h := srv.Handler()
	sessionID, _ := bootstrapSession(t, h, "the-real-credential")

	// Plaintext + cookie: denied regardless of a valid session existing.
	w := newDeadlineRecorder()
	r := httptest.NewRequest(http.MethodPost, "/rpc", nil)
	r.AddCookie(&http.Cookie{Name: serve.SessionCookieName, Value: sessionID})
	h.ServeHTTP(w, r)
	assertDenied(t, w.ResponseRecorder, http.StatusUnauthorized, "browser_https_required")

	// HTTPS + cookie + mismatched Origin: denied.
	w2 := newDeadlineRecorder()
	r2 := tlsRequest(httptest.NewRequest(http.MethodPost, "/rpc", nil))
	r2.AddCookie(&http.Cookie{Name: serve.SessionCookieName, Value: sessionID})
	r2.Header.Set("Origin", "https://evil.example.com")
	r2.Host = testHost
	h.ServeHTTP(w2, r2)
	assertDenied(t, w2.ResponseRecorder, http.StatusUnauthorized, "origin")

	if rpcCounter.count() != 0 {
		t.Fatalf("neither plaintext-cookie nor mismatched-origin request may reach downstream, count=%d", rpcCounter.count())
	}

	// HTTPS + cookie + matching Origin/Host on a safe (read-only) route
	// succeeds with NO CSRF token required.
	w3 := newDeadlineRecorder()
	r3 := tlsRequest(httptest.NewRequest(http.MethodGet, "/sse", nil))
	r3.AddCookie(&http.Cookie{Name: serve.SessionCookieName, Value: sessionID})
	r3.Header.Set("Origin", testOrigin)
	r3.Host = testHost
	h.ServeHTTP(w3, r3)
	if w3.Code != http.StatusOK {
		t.Fatalf("valid HTTPS cookie + matching Origin/Host on a safe GET route: status=%d body=%s", w3.Code, w3.Body.String())
	}
}

func TestCookieSession_StateChangingRoute_RequiresCSRF(t *testing.T) {
	srv, _, selectCounter := newBrowserTestServer("the-real-credential")
	h := srv.Handler()
	sessionID, csrf := bootstrapSession(t, h, "the-real-credential")

	// Missing CSRF token: denied, Select never called.
	w := newDeadlineRecorder()
	r := tlsRequest(httptest.NewRequest(http.MethodPost, "/api/select", strings.NewReader(`{"handle":"some-peer"}`)))
	r.AddCookie(&http.Cookie{Name: serve.SessionCookieName, Value: sessionID})
	r.Header.Set("Origin", testOrigin)
	r.Host = testHost
	h.ServeHTTP(w, r)
	assertDenied(t, w.ResponseRecorder, http.StatusUnauthorized, "csrf")
	if selectCounter.count() != 0 {
		t.Fatalf("missing CSRF must not reach Select, count=%d", selectCounter.count())
	}

	// Wrong CSRF token: denied.
	w2 := newDeadlineRecorder()
	r2 := tlsRequest(httptest.NewRequest(http.MethodPost, "/api/select", strings.NewReader(`{"handle":"some-peer"}`)))
	r2.AddCookie(&http.Cookie{Name: serve.SessionCookieName, Value: sessionID})
	r2.Header.Set("Origin", testOrigin)
	r2.Host = testHost
	r2.Header.Set(serve.CSRFHeaderName, "wrong-token")
	h.ServeHTTP(w2, r2)
	assertDenied(t, w2.ResponseRecorder, http.StatusUnauthorized, "csrf")
	if selectCounter.count() != 0 {
		t.Fatalf("wrong CSRF must not reach Select, count=%d", selectCounter.count())
	}

	// Correct CSRF token: succeeds.
	w3 := newDeadlineRecorder()
	r3 := tlsRequest(httptest.NewRequest(http.MethodPost, "/api/select", strings.NewReader(`{"handle":"some-peer"}`)))
	r3.AddCookie(&http.Cookie{Name: serve.SessionCookieName, Value: sessionID})
	r3.Header.Set("Origin", testOrigin)
	r3.Host = testHost
	r3.Header.Set(serve.CSRFHeaderName, csrf)
	h.ServeHTTP(w3, r3)
	if w3.Code != http.StatusOK {
		t.Fatalf("valid CSRF token: status=%d body=%s", w3.Code, w3.Body.String())
	}
	if selectCounter.count() != 1 {
		t.Fatalf("valid CSRF must reach Select exactly once, count=%d", selectCounter.count())
	}
}

func TestCookieSession_BearerHeaderRouteNeverNeedsCSRF(t *testing.T) {
	// ADR-007: "an Authorization bearer header ... is therefore inherently
	// resistant to cross-site request forgery" -- a bearer-authenticated
	// state-changing request must succeed with NO CSRF header at all.
	srv, _, selectCounter := newBrowserTestServer("the-real-credential")
	h := srv.Handler()

	w := newDeadlineRecorder()
	r := httptest.NewRequest(http.MethodPost, "/api/select", strings.NewReader(`{"handle":"some-peer"}`))
	r.Header.Set("Authorization", "Bearer the-real-credential")
	h.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("bearer-authenticated state-changing request without CSRF: status=%d body=%s", w.Code, w.Body.String())
	}
	if selectCounter.count() != 1 {
		t.Fatalf("expected Select to run once, count=%d", selectCounter.count())
	}
}

type fakeClock struct {
	mu      sync.Mutex
	now     time.Time
	waiters []fakeWaiter
}

type fakeWaiter struct {
	at time.Time
	ch chan time.Time
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) After(d time.Duration) <-chan time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	ch := make(chan time.Time, 1)
	at := c.now.Add(d)
	if d <= 0 {
		ch <- c.now
		return ch
	}
	c.waiters = append(c.waiters, fakeWaiter{at: at, ch: ch})
	return ch
}

func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	c.now = c.now.Add(d)
	now := c.now
	remaining := c.waiters[:0]
	for _, waiter := range c.waiters {
		if !now.Before(waiter.at) {
			waiter.ch <- now
		} else {
			remaining = append(remaining, waiter)
		}
	}
	c.waiters = remaining
	c.mu.Unlock()
}

type boundStream struct {
	started  chan lan.CredentialBinding
	canceled chan struct{}
}

func newBoundStream() (*boundStream, http.Handler) {
	stream := &boundStream{
		started:  make(chan lan.CredentialBinding, 1),
		canceled: make(chan struct{}),
	}
	return stream, http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		binding, ok := CredentialBindingFromContext(r.Context())
		if !ok {
			stream.started <- lan.CredentialBinding{}
			return
		}
		stream.started <- binding
		<-r.Context().Done()
		close(stream.canceled)
	})
}

func awaitSignal(t *testing.T, ch <-chan struct{}, what string) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(time.Second):
		t.Fatalf("timed out waiting for %s", what)
	}
}

func TestBoundStreamCanceledAtCredentialExpiry(t *testing.T) {
	now := time.Date(2026, 8, 7, 12, 0, 0, 0, time.UTC)
	clock := &fakeClock{now: now}
	authority := testAuthority(t, "secret", now.Add(time.Hour))
	stream, handler := newBoundStream()
	srv := New(Options{Authority: authority, Clock: clock, SSEHandler: handler})
	req := httptest.NewRequest(http.MethodGet, "/sse", nil)
	req.Header.Set("Authorization", "Bearer secret")
	done := make(chan struct{})
	go func() {
		srv.Handler().ServeHTTP(newDeadlineRecorder(), req)
		close(done)
	}()
	binding := <-stream.started
	if binding.ID != "test-client" || binding.Revision != 1 || !binding.ExpiresAt.Equal(now.Add(time.Hour)) {
		t.Fatalf("downstream binding = %+v", binding)
	}
	clock.Advance(time.Hour)
	awaitSignal(t, stream.canceled, "stream expiry cancellation")
	awaitSignal(t, done, "expired request return")
}

func TestBoundStreamsCanceledOnRevocationAndRotationBeyondOverlap(t *testing.T) {
	now := time.Date(2026, 8, 7, 12, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name   string
		rotate bool
	}{
		{name: "revocation"},
		{name: "rotation beyond overlap", rotate: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			clock := &fakeClock{now: now}
			old := lan.CredentialRecord{
				ID: "client", Secret: "old", Revision: 1,
				ExpiresAt: now.Add(time.Hour), OverlapUntil: now.Add(5 * time.Minute),
			}
			authority, err := lan.NewMemoryCredentialAuthority([]lan.CredentialRecord{old})
			if err != nil {
				t.Fatal(err)
			}
			stream, handler := newBoundStream()
			srv := New(Options{Authority: authority, Clock: clock, WSHandler: handler})
			req := httptest.NewRequest(http.MethodGet, "/ws", nil)
			req.Header.Set("Authorization", "Bearer old")
			done := make(chan struct{})
			go func() {
				srv.Handler().ServeHTTP(newDeadlineRecorder(), req)
				close(done)
			}()
			if binding := <-stream.started; binding.ID != "client" || binding.Revision != 1 {
				t.Fatalf("binding = %+v", binding)
			}
			if tc.rotate {
				current := lan.CredentialRecord{
					ID: "client", Secret: "new", Revision: 2, ExpiresAt: now.Add(time.Hour),
				}
				if err := authority.Replace([]lan.CredentialRecord{old, current}); err != nil {
					t.Fatal(err)
				}
				clock.Advance(5 * time.Minute)
			} else {
				old.Revoked = true
				if err := authority.Replace([]lan.CredentialRecord{old}); err != nil {
					t.Fatal(err)
				}
			}
			awaitSignal(t, stream.canceled, tc.name+" cancellation")
			awaitSignal(t, done, tc.name+" request return")
		})
	}
}

func TestCookieSessionStreamBoundToCredentialAndCanceledOnRevocation(t *testing.T) {
	now := time.Date(2026, 8, 7, 12, 0, 0, 0, time.UTC)
	clock := &fakeClock{now: now}
	authority := testAuthority(t, "browser-secret", now.Add(time.Hour))
	stream, handler := newBoundStream()
	srv := New(Options{
		Authority:      authority,
		Clock:          clock,
		AllowedOrigins: []string{testOrigin},
		AllowedHosts:   []string{testHost},
		SSEHandler:     handler,
	})
	h := srv.Handler()
	sessionID, _ := bootstrapSession(t, h, "browser-secret")
	req := tlsRequest(httptest.NewRequest(http.MethodGet, "/sse", nil))
	req.AddCookie(&http.Cookie{Name: serve.SessionCookieName, Value: sessionID})
	req.Header.Set("Origin", testOrigin)
	req.Host = testHost
	done := make(chan struct{})
	go func() {
		h.ServeHTTP(newDeadlineRecorder(), req)
		close(done)
	}()
	if binding := <-stream.started; binding.ID != "test-client" || binding.Revision != 1 {
		t.Fatalf("session stream binding = %+v", binding)
	}
	if err := authority.Replace([]lan.CredentialRecord{{
		ID: "test-client", Secret: "browser-secret", Revision: 1,
		ExpiresAt: now.Add(time.Hour), Revoked: true,
	}}); err != nil {
		t.Fatal(err)
	}
	awaitSignal(t, stream.canceled, "session revocation cancellation")
	awaitSignal(t, done, "revoked session request return")
}

// ─── Never log credentials ─────────────────────────────────────────────────

func TestPrintBanner_NeverEmbedsCredentialInOutput(t *testing.T) {
	// PrintBanner writes to stdout; capture it and assert the raw credential
	// value never appears in the printed URLs/QR text.
	const secret = "must-never-appear-in-banner-output"

	origStdout := os.Stdout
	rPipe, wPipe, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	os.Stdout = wPipe

	PrintBanner("test", ":8787", secret)

	_ = wPipe.Close()
	os.Stdout = origStdout
	captured, _ := io.ReadAll(rPipe)

	if strings.Contains(string(captured), secret) {
		t.Fatalf("PrintBanner leaked the credential into its output: %q", string(captured))
	}
}

func TestPeerRoute_Phase01DeniesRemoteBeforePresenceResolution(t *testing.T) {
	var localCalls atomic.Int32
	local := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		localCalls.Add(1)
		w.WriteHeader(http.StatusNoContent)
	})
	srv := New(Options{
		SelfHandle: "daemon-self",
		Selected:   func() string { return "remote-with-untrusted-presence-url" },
	})

	req := httptest.NewRequest(http.MethodPost, "/rpc", nil)
	rec := httptest.NewRecorder()
	srv.peerRoute(local, true).ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("remote route status = %d, want %d; body=%s", rec.Code, http.StatusServiceUnavailable, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "authenticated_endpoint_unavailable") {
		t.Fatalf("remote route body = %q, want stable Phase 01 denial", rec.Body.String())
	}
	if got := localCalls.Load(); got != 0 {
		t.Fatalf("remote denial dispatched local handler %d times", got)
	}
}

func TestPeerRoute_Phase01KeepsSelfRouteLocal(t *testing.T) {
	var localCalls atomic.Int32
	local := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		localCalls.Add(1)
		w.WriteHeader(http.StatusNoContent)
	})
	srv := New(Options{SelfHandle: "daemon-self"})

	req := httptest.NewRequest(http.MethodPost, "/rpc?peer=daemon-self", nil)
	rec := httptest.NewRecorder()
	srv.peerRoute(local, true).ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("self route status = %d, want %d; body=%s", rec.Code, http.StatusNoContent, rec.Body.String())
	}
	if got := localCalls.Load(); got != 1 {
		t.Fatalf("self route dispatched local handler %d times, want 1", got)
	}
}

// panicOnCallSelector returns a Selected callback that panics the instant it
// is invoked. Tests that must prove peerRoute's no-query + selector-present
// denial NEVER calls Selected() use this instead of a callback that merely
// returns an inconvenient value: an inconvenient-but-tolerated return value
// only proves the denial happened to still occur, not that the callback
// itself was never reached. If peerRoute ever regresses to calling Selected
// before deciding to deny (the exact R5 bug this file repairs), this
// callback panics and the test fails loudly instead of silently passing.
func panicOnCallSelector() func() string {
	return func() string {
		panic("peerRoute must never invoke Selected() when denying a no-query remote route")
	}
}

// TestPeerRoute_NoQueryWithSelectorDeniesWithoutInvokingCallback is the
// direct regression test for the R5 CONTRACT.md fix: "A no-query route with
// a selection callback must return the existing stable remote denial
// without invoking the callback, local handler, resolver, or any other side
// effect." It exercises peerRoute directly (bypassing auth/Handler(), which
// is covered separately below) with a panic-on-call Selected and an
// atomic-counted local handler, on a request with NO ?peer= query string.
func TestPeerRoute_NoQueryWithSelectorDeniesWithoutInvokingCallback(t *testing.T) {
	var localCalls atomic.Int32
	local := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		localCalls.Add(1)
		w.WriteHeader(http.StatusNoContent)
	})
	srv := New(Options{
		SelfHandle: "daemon-self",
		Selected:   panicOnCallSelector(),
	})

	req := httptest.NewRequest(http.MethodPost, "/rpc", nil) // no ?peer= at all
	rec := httptest.NewRecorder()

	// If peerRoute ever calls Selected() again before denying, the panic
	// propagates out of ServeHTTP and this test fails with a stack trace
	// pointing straight at the regression, instead of merely a wrong status.
	srv.peerRoute(local, true).ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("no-query+selector route status = %d, want %d; body=%s", rec.Code, http.StatusServiceUnavailable, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "authenticated_endpoint_unavailable") {
		t.Fatalf("no-query+selector route body = %q, want stable Phase 01 denial", rec.Body.String())
	}
	if got := localCalls.Load(); got != 0 {
		t.Fatalf("no-query+selector denial dispatched the local handler %d times, want 0", got)
	}
}

// TestPeerRoute_NoQueryNoSelectorRoutesLocally is the selector-ABSENT half
// of the same ordering matrix: CONTRACT.md "With no explicit peer and no
// selection capability, route locally." Selected is nil (not merely a
// no-op), so any panic from a stray call would come from a nil-func
// invocation -- proving the code path taken is the "no capability at all"
// branch, not a Selected call that happens to be cheap.
func TestPeerRoute_NoQueryNoSelectorRoutesLocally(t *testing.T) {
	var localCalls atomic.Int32
	local := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		localCalls.Add(1)
		w.WriteHeader(http.StatusNoContent)
	})
	srv := New(Options{SelfHandle: "daemon-self"}) // Selected: nil

	req := httptest.NewRequest(http.MethodPost, "/rpc", nil) // no ?peer=
	rec := httptest.NewRecorder()
	srv.peerRoute(local, true).ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("no-query+no-selector route status = %d, want %d; body=%s", rec.Code, http.StatusNoContent, rec.Body.String())
	}
	if got := localCalls.Load(); got != 1 {
		t.Fatalf("no-query+no-selector route dispatched local handler %d times, want 1", got)
	}
}

// TestPeerRoute_OuterHandlerNoQueryWithSelectorNeverInvokesCallback repeats
// the panic-on-call proof above through the COMPLETE, authenticated, outer
// response-write-bound stack -- srv.Handler(), not peerRoute in isolation --
// per the R5 instruction to cover "including the outer
// response-write-bound route." This proves the ordering fix holds through
// auth (Server.auth), route registration, and
// serve.WithResponseWriteBounds, not merely when peerRoute is invoked
// directly by a test.
func TestPeerRoute_OuterHandlerNoQueryWithSelectorNeverInvokesCallback(t *testing.T) {
	const token = "outer-no-query-selector-token"
	ch := &counterHandler{}
	srv := New(Options{
		Token:      token,
		SelfHandle: "daemon-self",
		Selected:   panicOnCallSelector(),
		RPCHandler: ch,
	})
	h := srv.Handler()

	req := httptest.NewRequest(http.MethodPost, "/rpc", nil) // no ?peer=
	req.Header.Set("Authorization", "Bearer "+token)
	rec := newDeadlineRecorder()

	h.ServeHTTP(rec, req)

	assertWriteDeadlineInstalled(t, rec)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("outer no-query+selector status = %d, want %d; body=%s", rec.Code, http.StatusServiceUnavailable, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "authenticated_endpoint_unavailable") {
		t.Fatalf("outer no-query+selector body = %q, want stable Phase 01 denial", rec.Body.String())
	}
	if ch.count() != 0 {
		t.Fatalf("outer no-query+selector denial dispatched the local RPC handler %d times, want 0", ch.count())
	}
}

// ─── test helpers ───────────────────────────────────────────────────────

func assertDenied(t *testing.T, w *httptest.ResponseRecorder, wantStatus int, wantReason string) {
	t.Helper()
	if w.Code != wantStatus {
		t.Fatalf("status=%d body=%s, want %d", w.Code, w.Body.String(), wantStatus)
	}
	var body map[string]string
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("body not JSON: %v (%s)", err, w.Body.String())
	}
	if body["reason"] != wantReason && body["error"] != wantReason {
		t.Fatalf("body=%v, want reason or error %q", body, wantReason)
	}
}

// ─── serve.WithResponseWriteBounds: outer write-bound coverage ────────────
//
// The gateway's complete Handler() (and, in cmd/swarm-gateway, the
// standalone command's http.Server.Handler assignment) is wrapped with
// serve.WithResponseWriteBounds so every synchronous write or upgrader this
// package can ever reach -- outer auth denial, grant/bootstrap/control
// routes, missing-handler responses, and the stable Phase 01 remote-policy
// denial -- gets a write deadline installed before the wrapped handler runs.
// These tests prove that wrap without altering any previously-established
// status/body schema, route registration, or authentication ordering (every
// scenario below mirrors an existing assertion above, just observed through
// a deadline-aware writer instead of a plain httptest.ResponseRecorder).

// deadlineRecorder wraps httptest.ResponseRecorder and additionally
// implements the SetWriteDeadline(time.Time) error method
// http.ResponseController looks for, so a test can prove
// serve.WithResponseWriteBounds actually attempted to install a deadline --
// not merely that the response body/status still look right. A plain
// httptest.ResponseRecorder cannot distinguish "no deadline was ever
// attempted" from "the writer legitimately doesn't support one" because
// http.ResponseController silently reports http.ErrNotSupported for it
// either way.
type deadlineRecorder struct {
	*httptest.ResponseRecorder
	setCalls int
	deadline time.Time
}

func newDeadlineRecorder() *deadlineRecorder {
	return &deadlineRecorder{ResponseRecorder: httptest.NewRecorder()}
}

func (d *deadlineRecorder) SetWriteDeadline(t time.Time) error {
	d.setCalls++
	d.deadline = t
	return nil
}

func assertWriteDeadlineInstalled(t *testing.T, rec *deadlineRecorder) {
	t.Helper()
	if rec.setCalls == 0 {
		t.Fatal("expected serve.WithResponseWriteBounds to install a write deadline before dispatch, but SetWriteDeadline was never called")
	}
	if until := time.Until(rec.deadline); until <= 0 || until > 15*time.Second {
		t.Fatalf("installed deadline = %v (%.2fs from now), want a bound roughly 10s in the future", rec.deadline, until.Seconds())
	}
}

// TestResponseWriteBounds_InstallsDeadlineAcrossOwnedPaths re-exercises the
// gateway's owned denial/control surface -- auth, query-credential, Origin,
// CSRF, listener-grant denial, HTTPS session bootstrap, a public control
// route, a missing-handler response, and the Phase 01 remote-peer denial --
// through a deadline-aware writer, proving every one of those synchronous
// writes gets a bounded deadline installed first while preserving the exact
// same status/body schema the non-wrapped assertions above already require.
func TestResponseWriteBounds_InstallsDeadlineAcrossOwnedPaths(t *testing.T) {
	const token = "outer-write-bound-token"

	t.Run("auth", func(t *testing.T) {
		ch := &counterHandler{}
		srv := New(Options{Token: token, RPCHandler: ch})
		rec := newDeadlineRecorder()
		r := httptest.NewRequest(http.MethodPost, "/rpc", nil)
		srv.Handler().ServeHTTP(rec, r)
		assertWriteDeadlineInstalled(t, rec)
		assertDenied(t, rec.ResponseRecorder, http.StatusUnauthorized, "unauthenticated")
		if ch.count() != 0 {
			t.Fatalf("absent credential must not reach downstream handler, count=%d", ch.count())
		}
	})

	t.Run("query", func(t *testing.T) {
		ch := &counterHandler{}
		srv := New(Options{Token: token, RPCHandler: ch})
		rec := newDeadlineRecorder()
		r := httptest.NewRequest(http.MethodPost, "/rpc?token="+token, nil)
		srv.Handler().ServeHTTP(rec, r)
		assertWriteDeadlineInstalled(t, rec)
		assertDenied(t, rec.ResponseRecorder, http.StatusUnauthorized, "query_credential")
		if ch.count() != 0 {
			t.Fatalf("query credential must not reach downstream handler, count=%d", ch.count())
		}
	})

	t.Run("origin", func(t *testing.T) {
		srv, rpcCounter, _ := newBrowserTestServer(token)
		h := srv.Handler()
		sessionID, _ := bootstrapSession(t, h, token)
		rec := newDeadlineRecorder()
		r := tlsRequest(httptest.NewRequest(http.MethodPost, "/rpc", nil))
		r.AddCookie(&http.Cookie{Name: serve.SessionCookieName, Value: sessionID})
		r.Header.Set("Origin", "https://evil.example.com")
		r.Host = testHost
		h.ServeHTTP(rec, r)
		assertWriteDeadlineInstalled(t, rec)
		assertDenied(t, rec.ResponseRecorder, http.StatusUnauthorized, "origin")
		if rpcCounter.count() != 0 {
			t.Fatalf("origin mismatch must not reach downstream handler, count=%d", rpcCounter.count())
		}
	})

	t.Run("csrf", func(t *testing.T) {
		srv, _, selectCounter := newBrowserTestServer(token)
		h := srv.Handler()
		sessionID, _ := bootstrapSession(t, h, token)
		rec := newDeadlineRecorder()
		r := tlsRequest(httptest.NewRequest(http.MethodPost, "/api/select", strings.NewReader(`{"handle":"some-peer"}`)))
		r.AddCookie(&http.Cookie{Name: serve.SessionCookieName, Value: sessionID})
		r.Header.Set("Origin", testOrigin)
		r.Host = testHost
		h.ServeHTTP(rec, r)
		assertWriteDeadlineInstalled(t, rec)
		assertDenied(t, rec.ResponseRecorder, http.StatusUnauthorized, "csrf")
		if selectCounter.count() != 0 {
			t.Fatalf("missing CSRF must not reach Select, count=%d", selectCounter.count())
		}
	})

	t.Run("grant", func(t *testing.T) {
		// Non-loopback bind, no credentials, no TLS, no explicit insecure
		// override: AuthorizeListener denies the grant outright, and
		// Handler() fails closed for every path via handleGatewayUnavailable.
		srv := New(Options{ListenAddr: "0.0.0.0:8787"})
		rec := newDeadlineRecorder()
		r := httptest.NewRequest(http.MethodGet, "/", nil)
		srv.Handler().ServeHTTP(rec, r)
		assertWriteDeadlineInstalled(t, rec)
		if rec.Code != http.StatusServiceUnavailable {
			t.Fatalf("status=%d body=%s, want 503 gateway_unavailable", rec.Code, rec.Body.String())
		}
	})

	t.Run("bootstrap", func(t *testing.T) {
		srv, _, _ := newBrowserTestServer(token)
		h := srv.Handler()
		rec := newDeadlineRecorder()
		r := tlsRequest(httptest.NewRequest(http.MethodPost, "/api/session/bootstrap", nil))
		r.Header.Set("Authorization", "Bearer "+token)
		r.Header.Set("Origin", testOrigin)
		r.Host = testHost
		h.ServeHTTP(rec, r)
		assertWriteDeadlineInstalled(t, rec)
		if rec.Code != http.StatusOK {
			t.Fatalf("bootstrap status=%d body=%s, want 200", rec.Code, rec.Body.String())
		}
		var body map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatalf("bootstrap body not JSON: %v", err)
		}
		if _, ok := body["csrf"]; !ok {
			t.Fatal("bootstrap response must still include a csrf token")
		}
	})

	t.Run("control", func(t *testing.T) {
		srv := New(Options{Token: token})
		rec := newDeadlineRecorder()
		r := httptest.NewRequest(http.MethodGet, "/api/version", nil)
		srv.Handler().ServeHTTP(rec, r)
		assertWriteDeadlineInstalled(t, rec)
		if rec.Code != http.StatusOK {
			t.Fatalf("control route status=%d body=%s, want 200", rec.Code, rec.Body.String())
		}
	})

	t.Run("missing-handler", func(t *testing.T) {
		srv := New(Options{Token: token}) // no RPCHandler configured
		rec := newDeadlineRecorder()
		r := httptest.NewRequest(http.MethodPost, "/rpc", nil)
		r.Header.Set("Authorization", "Bearer "+token)
		srv.Handler().ServeHTTP(rec, r)
		assertWriteDeadlineInstalled(t, rec)
		if rec.Code != http.StatusServiceUnavailable {
			t.Fatalf("missing-handler status=%d body=%s, want 503", rec.Code, rec.Body.String())
		}
		if !strings.Contains(rec.Body.String(), "no data handler configured") {
			t.Fatalf("missing-handler body=%q, want the stable no-handler message", rec.Body.String())
		}
	})

	t.Run("remote denial", func(t *testing.T) {
		ch := &counterHandler{}
		srv := New(Options{
			Token:      token,
			SelfHandle: "daemon-self",
			Selected:   func() string { return "remote-with-untrusted-presence-url" },
			RPCHandler: ch,
		})
		rec := newDeadlineRecorder()
		r := httptest.NewRequest(http.MethodPost, "/rpc", nil)
		r.Header.Set("Authorization", "Bearer "+token)
		srv.Handler().ServeHTTP(rec, r)
		assertWriteDeadlineInstalled(t, rec)
		if rec.Code != http.StatusServiceUnavailable {
			t.Fatalf("remote denial status=%d body=%s, want 503", rec.Code, rec.Body.String())
		}
		if !strings.Contains(rec.Body.String(), "authenticated_endpoint_unavailable") {
			t.Fatalf("remote denial body=%q, want the stable Phase 01 denial", rec.Body.String())
		}
		if ch.count() != 0 {
			t.Fatalf("remote denial must never dispatch the local handler, count=%d", ch.count())
		}
	})
}

// failingDeadlineWriter simulates a genuinely unsupported production
// writer: it implements SetWriteDeadline (so http.ResponseController does
// not take the ordinary http.ErrNotSupported "no deadline capability at
// all" path) but returns a real, non-http.ErrNotSupported error, and its
// Write/WriteHeader methods panic if they are ever invoked. This proves
// serve.WithResponseWriteBounds fails closed -- it never delegates a
// synchronous write to a writer whose deadline it could not install --
// instead of the alternative of silently attempting an unbounded write
// anyway.
type failingDeadlineWriter struct {
	header http.Header
}

func (w *failingDeadlineWriter) Header() http.Header {
	if w.header == nil {
		w.header = make(http.Header)
	}
	return w.header
}

func (w *failingDeadlineWriter) Write([]byte) (int, error) {
	panic("failingDeadlineWriter.Write must never be called on an unsupported writer")
}

func (w *failingDeadlineWriter) WriteHeader(int) {
	panic("failingDeadlineWriter.WriteHeader must never be called on an unsupported writer")
}

func (w *failingDeadlineWriter) SetWriteDeadline(time.Time) error {
	return errors.New("simulated: this writer cannot honor a write deadline")
}

// TestResponseWriteBounds_UnsupportedWriterNeverInvokesPanicOnWrite proves
// the panic-on-write methods of a writer that cannot honor a write deadline
// are never invoked through the gateway's wrapped Handler(): if
// serve.WithResponseWriteBounds ever delegated a Write/WriteHeader call to
// such a writer, this test would panic and fail instead of merely asserting
// on a status code.
func TestResponseWriteBounds_UnsupportedWriterNeverInvokesPanicOnWrite(t *testing.T) {
	srv := New(Options{})
	h := srv.Handler()
	w := &failingDeadlineWriter{}
	r := httptest.NewRequest(http.MethodGet, "/api/version", nil)

	defer func() {
		if rec := recover(); rec != nil {
			t.Fatalf("an unsupported writer's Write/WriteHeader must never be invoked, but the handler panicked: %v", rec)
		}
	}()
	h.ServeHTTP(w, r)
}

// deadlineCapableBase is a minimal http.ResponseWriter that also implements
// SetWriteDeadline, standing in for the concrete production writer type an
// Unwrap() chain must ultimately reach.
type deadlineCapableBase struct {
	header   http.Header
	body     bytes.Buffer
	status   int
	deadline time.Time
	setCalls int
}

func (b *deadlineCapableBase) Header() http.Header {
	if b.header == nil {
		b.header = make(http.Header)
	}
	return b.header
}

func (b *deadlineCapableBase) Write(p []byte) (int, error) {
	if b.status == 0 {
		b.status = http.StatusOK
	}
	return b.body.Write(p)
}

func (b *deadlineCapableBase) WriteHeader(code int) { b.status = code }

func (b *deadlineCapableBase) SetWriteDeadline(t time.Time) error {
	b.setCalls++
	b.deadline = t
	return nil
}

// unwrapOnlyWrapper embeds http.ResponseWriter BY INTERFACE (not by the
// concrete *deadlineCapableBase type), so Go's method-promotion rules do NOT
// promote SetWriteDeadline to this wrapper even though the underlying
// concrete value implements it -- only Header/Write/WriteHeader (the
// interface's declared method set) are promoted. This forces
// http.ResponseController (and therefore serve.WithResponseWriteBounds) to
// call Unwrap() to reach a deadline-capable base, exactly like a real
// middleware ResponseWriter wrapper (logging, compression, ...) that does
// not itself implement SetWriteDeadline.
type unwrapOnlyWrapper struct {
	http.ResponseWriter
	base *deadlineCapableBase
}

func (u *unwrapOnlyWrapper) Unwrap() http.ResponseWriter { return u.base }

// TestResponseWriteBounds_UnwrapCapableWrapperReachesDeadlineCapableBase
// proves serve.WithResponseWriteBounds follows an Unwrap() chain to install
// its deadline on the real underlying writer instead of silently no-op'ing
// because the immediate ResponseWriter it was handed does not itself
// implement SetWriteDeadline.
func TestResponseWriteBounds_UnwrapCapableWrapperReachesDeadlineCapableBase(t *testing.T) {
	srv := New(Options{})
	h := srv.Handler()
	base := &deadlineCapableBase{}
	w := &unwrapOnlyWrapper{ResponseWriter: base, base: base}
	r := httptest.NewRequest(http.MethodGet, "/api/version", nil)
	h.ServeHTTP(w, r)

	if base.setCalls == 0 {
		t.Fatal("expected the write deadline to reach the unwrapped base writer via Unwrap()")
	}
	if until := time.Until(base.deadline); until <= 0 || until > 15*time.Second {
		t.Fatalf("deadline = %v (%.2fs from now), want a bound roughly 10s in the future", base.deadline, until.Seconds())
	}
	if base.status != http.StatusOK {
		t.Fatalf("status=%d, want 200", base.status)
	}
	if base.body.Len() == 0 {
		t.Fatal("expected a response body to have been written through the unwrap chain")
	}
}

// TestResponseWriteBounds_NonReadingTCPClientCannotStallHandlerOrShutdown
// proves the write bound is enforced against a REAL non-reading TCP client
// over a real *http.Server -- not merely against an in-memory recorder. An
// 8 MiB response body cannot fit in any realistic kernel socket send buffer,
// so a client that writes the request and then never reads the response can
// only be unblocked by the installed deadline; if the deadline were never
// applied, both the handler goroutine and a subsequent graceful
// http.Server.Shutdown would hang until this test's own generous outer
// context timeout, which this test treats as failure.
func TestResponseWriteBounds_NonReadingTCPClientCannotStallHandlerOrShutdown(t *testing.T) {
	const token = "outer-write-bound-tcp-token"
	handlerReturned := make(chan struct{})
	handlerStarted := make(chan struct{})
	blocked := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		close(handlerStarted)
		w.Header().Set("Content-Type", "text/plain")
		w.WriteHeader(http.StatusOK)
		// Write 1 MiB chunks, flushing each one to the real socket, until
		// either the write bound cuts the connection off or we've pushed far
		// more data than any realistic kernel send/receive buffer tuning
		// could ever absorb without the non-reading client's TCP window
		// closing. A non-reading client's receive window stops growing
		// (nothing frees space in its socket receive buffer), so this loop
		// is the deterministic mechanism that forces the handler goroutine
		// to actually block on a real, unbuffered network write -- not a
		// sleep or a timing ratio.
		chunk := bytes.Repeat([]byte{'x'}, 1<<20) // 1 MiB
		flusher, _ := w.(http.Flusher)
		for i := 0; i < 4096; i++ { // cap: 4 GiB, far beyond any buffer tuning
			if _, err := w.Write(chunk); err != nil {
				break
			}
			if flusher != nil {
				flusher.Flush()
			}
		}
		close(handlerReturned)
	})
	srv := New(Options{Token: token, RPCHandler: blocked})
	h := srv.Handler()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	httpSrv := &http.Server{Handler: h}
	serveDone := make(chan struct{})
	go func() {
		_ = httpSrv.Serve(ln)
		close(serveDone)
	}()

	conn, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	req, _ := http.NewRequest(http.MethodPost, "http://"+ln.Addr().String()+"/rpc", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	if err := req.Write(conn); err != nil {
		t.Fatalf("write request: %v", err)
	}
	// Deliberately never read the response: this is the "non-reading TCP
	// client" the write bound must survive against.
	select {
	case <-handlerStarted:
	case <-time.After(5 * time.Second):
		t.Fatal("handler never started -- Shutdown would race an idle/not-yet-accepted connection instead of proving the write bound")
	}

	start := time.Now()
	shutCtx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	shutErr := httpSrv.Shutdown(shutCtx)
	elapsed := time.Since(start)
	_ = conn.Close()
	<-serveDone

	if shutErr != nil {
		t.Fatalf("graceful Shutdown did not complete (the write deadline never unblocked the handler): %v (after %s)", shutErr, elapsed)
	}
	if elapsed < 2*time.Second {
		t.Fatalf("Shutdown returned suspiciously fast (%s); the write bound may not actually be the thing that unblocked it", elapsed)
	}
	if elapsed > 18*time.Second {
		t.Fatalf("Shutdown took %s, want well under the ~10s write bound plus graceful-shutdown slack", elapsed)
	}
	select {
	case <-handlerReturned:
	default:
		t.Fatal("handler goroutine did not return despite Shutdown completing")
	}
}

// TestWSRoute_HandshakeSucceedsThroughResponseWriteBounds proves the write
// bound wrapper does not break the WebSocket handshake path: the
// ResponseWriter it hands to the injected WSHandler must still support
// Hijacker (real network I/O, not httptest), and a successful 101 Switching
// Protocols handshake must still reach a real, unbuffered TCP client.
func TestWSRoute_HandshakeSucceedsThroughResponseWriteBounds(t *testing.T) {
	const token = "outer-write-bound-ws-token"
	upgraded := make(chan struct{})
	wsHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hj, ok := w.(http.Hijacker)
		if !ok {
			t.Errorf("ResponseWriter passed to the WS handler does not support Hijacker")
			return
		}
		conn, bufrw, err := hj.Hijack()
		if err != nil {
			t.Errorf("hijack: %v", err)
			return
		}
		defer conn.Close()
		_, _ = bufrw.WriteString("HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\n\r\n")
		_ = bufrw.Flush()
		close(upgraded)
	})
	srv := New(Options{Token: token, WSHandler: wsHandler})
	h := srv.Handler()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()
	httpSrv := &http.Server{Handler: h}
	go func() { _ = httpSrv.Serve(ln) }()
	defer func() {
		shutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = httpSrv.Shutdown(shutCtx)
	}()

	conn, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	req, _ := http.NewRequest(http.MethodGet, "http://"+ln.Addr().String()+"/ws", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Connection", "Upgrade")
	req.Header.Set("Upgrade", "websocket")
	if err := req.Write(conn); err != nil {
		t.Fatalf("write request: %v", err)
	}

	select {
	case <-upgraded:
	case <-time.After(5 * time.Second):
		t.Fatal("WS handshake handler never ran / hijacked the connection")
	}

	br := bufio.NewReader(conn)
	resp, err := http.ReadResponse(br, req)
	if err != nil {
		t.Fatalf("reading upgrade response: %v", err)
	}
	if resp.StatusCode != http.StatusSwitchingProtocols {
		t.Fatalf("status=%d, want 101 Switching Protocols", resp.StatusCode)
	}
}

// ─── /api/peers: bounded encoding + remote URL clearing (CONTRACT.md) ─────
//
// These tests cover two independent R5 requirements for handlePeers:
//
//   - "Replace unrestricted /api/peers JSON streaming with worker 1's exact
//     exported bounded encoder API. Encode the entire response before
//     setting headers or writing. Oversize uses fixed bounded JSON and no
//     partial peer list; cancellation writes nothing; other encoding
//     failures use a fixed bounded error."
//   - "As defense in depth for legacy registry records, clear ServeURL and
//     EndpointURL in DTOs for remote peers... Add tests with an over-4-MiB
//     selected value/list string and a remote record carrying attacker
//     URLs; assert bounded non-partial output and no raw URL disclosure."
//
// testPeersSwarm registers peers in an isolated, per-CALL swarm namespace.
// Unlike proxy_test.go's testSwarm -- which intentionally shares ONE
// PID-scoped name across every caller in the same test binary run, fine for
// that file's small, fixed, disjoint set of peer handles -- these
// handlePeers tests register peers with huge multi-megabyte fields and run
// one after another in the same process, so sharing a single namespace
// would let one test's (deliberately oversize) fixture leak into another
// test's peer list merely because both ran in the same binary. The swarm
// name below is unique per call (PID + a monotonic counter + a timestamp),
// so no two tests -- or, if this package ever adds t.Parallel, two
// concurrently-running tests -- ever share a registry directory.
var testPeersSwarmSeq int64

// testOpaqueRemoteHandle deterministically derives a valid process-generated
// -shaped remote handle (fixed-length lowercase hex origin~alias) from a
// short, readable seed. a2a.JoinSwarm now fails closed on a non-opaque
// PeerTypeRemote handle (it routes remote joins through the same
// retention-lock/cap/opaque-handle machinery lan_registry.go's LAN ingest
// uses), so every remote fixture in this file needs a handle shaped like
// this instead of a bare literal.
func testOpaqueRemoteHandle(seed string) string {
	sum := sha256.Sum256([]byte(seed))
	origin := hex.EncodeToString(sum[:16])
	alias := hex.EncodeToString(sum[16:32])
	return origin + "~" + alias
}

func testPeersSwarm(t *testing.T, peers ...a2a.PeerPresence) (string, func()) {
	t.Helper()
	seq := atomic.AddInt64(&testPeersSwarmSeq, 1)
	name := fmt.Sprintf("gwtest-peers-%d-%d-%d", os.Getpid(), seq, time.Now().UnixNano())
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

// TestHandlePeers_ClearsRemoteURLsAndNeverEmitsThemRaw registers a REMOTE
// peer (a2a.PeerTypeRemote) carrying attacker-controlled ServeURL/
// EndpointURL values -- the exact shape a legacy/pre-fix LAN registry
// record, or a maliciously crafted one, could still contain on disk -- and
// proves handlePeers' DTO conversion clears both fields unconditionally:
// the raw attacker URL string must not appear anywhere in the response
// body, and the JSON-decoded DTO's serveURL/endpointURL fields must be
// exactly empty, never merely redacted-but-present.
func TestHandlePeers_ClearsRemoteURLsAndNeverEmitsThemRaw(t *testing.T) {
	const attackerServeURL = "http://attacker.example.com:9999/steal"
	const attackerEndpointURL = "http://attacker.example.com:9999/steal/rpc"
	remoteHandle := testOpaqueRemoteHandle("hostile-host-legacy-remote")
	swarm, cleanup := testPeersSwarm(t, a2a.PeerPresence{
		Handle:      remoteHandle,
		Type:        a2a.PeerTypeRemote,
		ServeURL:    attackerServeURL,
		EndpointURL: attackerEndpointURL,
	})
	defer cleanup()

	srv := New(Options{Swarm: swarm})
	r := httptest.NewRequest(http.MethodGet, "/api/peers", nil)
	rec := httptest.NewRecorder()
	srv.handlePeers(rec, r)

	if rec.Code != http.StatusOK {
		t.Fatalf("handlePeers status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	raw := rec.Body.String()
	if strings.Contains(raw, "attacker.example.com") {
		t.Fatalf("raw attacker URL leaked into /api/peers response: %s", raw)
	}

	var body struct {
		Peers []struct {
			Handle      string `json:"handle"`
			ServeURL    string `json:"serveURL"`
			EndpointURL string `json:"endpointURL"`
		} `json:"peers"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("response not JSON: %v; body=%s", err, raw)
	}
	if len(body.Peers) != 1 {
		t.Fatalf("expected exactly 1 peer, got %d: %s", len(body.Peers), raw)
	}
	if body.Peers[0].ServeURL != "" {
		t.Fatalf("remote peer ServeURL not cleared: %q", body.Peers[0].ServeURL)
	}
	if body.Peers[0].EndpointURL != "" {
		t.Fatalf("remote peer EndpointURL not cleared: %q", body.Peers[0].EndpointURL)
	}
}

// TestHandlePeers_OversizeSelectedValueUsesFixedBoundedErrorNotPartialList
// forces EncodeBoundedJSON to fail with ErrResponseTooLarge by making the
// "selected" field alone exceed the 4 MiB structural cap, while a normal,
// small, otherwise-valid peer is ALSO present in the registry. It asserts
// the response is the small fixed bounded-error body -- not a truncated
// prefix of the oversize encode, and not a "here's the peer list but not
// selected" partial document.
func TestHandlePeers_OversizeSelectedValueUsesFixedBoundedErrorNotPartialList(t *testing.T) {
	swarm, cleanup := testPeersSwarm(t, a2a.PeerPresence{Handle: "ordinary-small-peer"})
	defer cleanup()

	huge := strings.Repeat("A", 5<<20) // 5 MiB > the 4 MiB structural cap
	srv := New(Options{
		Swarm:    swarm,
		Selected: func() string { return huge },
	})
	r := httptest.NewRequest(http.MethodGet, "/api/peers", nil)
	rec := httptest.NewRecorder()
	srv.handlePeers(rec, r)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("oversize status = %d, want %d", rec.Code, http.StatusInternalServerError)
	}
	if got := rec.Body.String(); got != peersResponseTooLargeBody {
		t.Fatalf("oversize body = %q, want fixed %q", got, peersResponseTooLargeBody)
	}
	if got := rec.Body.Len(); got > 4096 {
		t.Fatalf("oversize fallback body is %d bytes, want a small fixed constant (never a partial/near-cap document)", got)
	}
	if strings.Contains(rec.Body.String(), "ordinary-small-peer") {
		t.Fatalf("oversize fallback leaked peer list content: %s", rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Fatalf("oversize Content-Type = %q, want application/json", ct)
	}
}

// TestHandlePeers_OversizePeerFieldUsesFixedBoundedErrorNotPartialList is
// the "list string" half of the same requirement: instead of an oversize
// selected value, one peer's own field (Workspace) alone exceeds the 4 MiB
// cap. The complete peers array -- not just the one huge field -- must
// still collapse to the same small fixed fallback, never a partial array
// containing the other, small peers.
func TestHandlePeers_OversizePeerFieldUsesFixedBoundedErrorNotPartialList(t *testing.T) {
	huge := strings.Repeat("B", 5<<20) // 5 MiB > the 4 MiB structural cap
	swarm, cleanup := testPeersSwarm(t,
		a2a.PeerPresence{Handle: "small-peer-before"},
		a2a.PeerPresence{Handle: "huge-workspace-peer", Workspace: huge},
		a2a.PeerPresence{Handle: "small-peer-after"},
	)
	defer cleanup()

	srv := New(Options{Swarm: swarm})
	r := httptest.NewRequest(http.MethodGet, "/api/peers", nil)
	rec := httptest.NewRecorder()
	srv.handlePeers(rec, r)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("oversize-list status = %d, want %d; body head=%.200s", rec.Code, http.StatusInternalServerError, rec.Body.String())
	}
	if got := rec.Body.String(); got != peersResponseTooLargeBody {
		t.Fatalf("oversize-list body = %q, want fixed %q", got, peersResponseTooLargeBody)
	}
	if strings.Contains(rec.Body.String(), "small-peer-before") || strings.Contains(rec.Body.String(), "small-peer-after") {
		t.Fatalf("oversize-list fallback leaked partial peer list content: %s", rec.Body.String())
	}
}

// TestHandlePeers_ContextCanceledWritesNothing proves the cancellation path
// of the bounded encode writes NOTHING at all -- no header, no status line
// commitment via WriteHeader, no body byte -- per CONTRACT.md "cancellation
// writes nothing". httptest.ResponseRecorder's zero-value Code is 200 and
// its Header() map is empty until first touched, so asserting an entirely
// untouched recorded response header map plus an empty body is the
// strongest available proof that handlePeers took the early "return" branch
// instead of writing any fallback.
func TestHandlePeers_ContextCanceledWritesNothing(t *testing.T) {
	swarm, cleanup := testPeersSwarm(t, a2a.PeerPresence{Handle: "irrelevant-peer"})
	defer cleanup()

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // canceled before handlePeers ever calls EncodeBoundedJSON

	srv := New(Options{Swarm: swarm})
	r := httptest.NewRequest(http.MethodGet, "/api/peers", nil).WithContext(ctx)
	rec := httptest.NewRecorder()
	srv.handlePeers(rec, r)

	if rec.Body.Len() != 0 {
		t.Fatalf("canceled request wrote a body: %q", rec.Body.String())
	}
	if got := rec.Result().Header; len(got) != 0 {
		t.Fatalf("canceled request set response headers: %v", got)
	}
}

// TestHandlePeers_SmallListEncodesNormallyThroughBoundedEncoder is the
// control case: an ordinary, small peer list (including a remote peer with
// normal -- not attacker -- URLs, to prove clearing is remote-type-specific
// and does not blank a LOCAL peer's URLs) still round-trips through
// EncodeBoundedJSON exactly as it did through the previous unbounded
// json.Encoder path, with a normal 200 and both peers present.
func TestHandlePeers_SmallListEncodesNormallyThroughBoundedEncoder(t *testing.T) {
	remoteHandle := testOpaqueRemoteHandle("remote-peer")
	swarm, cleanup := testPeersSwarm(t,
		a2a.PeerPresence{Handle: "local-peer", Type: a2a.PeerTypeLocal, ServeURL: "http://127.0.0.1:9001"},
		a2a.PeerPresence{Handle: remoteHandle, Type: a2a.PeerTypeRemote, ServeURL: "http://192.168.1.50:9001"},
	)
	defer cleanup()

	srv := New(Options{Swarm: swarm, Selected: func() string { return "local-peer" }})
	r := httptest.NewRequest(http.MethodGet, "/api/peers", nil)
	rec := httptest.NewRecorder()
	srv.handlePeers(rec, r)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var body struct {
		Peers []struct {
			Handle   string `json:"handle"`
			ServeURL string `json:"serveURL"`
		} `json:"peers"`
		Selected string `json:"selected"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("response not JSON: %v; body=%s", err, rec.Body.String())
	}
	if body.Selected != "local-peer" {
		t.Fatalf("selected = %q, want %q", body.Selected, "local-peer")
	}
	if len(body.Peers) != 2 {
		t.Fatalf("expected 2 peers, got %d: %s", len(body.Peers), rec.Body.String())
	}
	byHandle := make(map[string]string, len(body.Peers))
	for _, p := range body.Peers {
		byHandle[p.Handle] = p.ServeURL
	}
	if byHandle["local-peer"] != "http://127.0.0.1:9001" {
		t.Fatalf("local peer ServeURL = %q, want preserved", byHandle["local-peer"])
	}
	if byHandle[remoteHandle] != "" {
		t.Fatalf("remote peer ServeURL = %q, want cleared", byHandle[remoteHandle])
	}
}
