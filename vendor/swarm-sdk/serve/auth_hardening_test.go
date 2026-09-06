package serve

// Focused tests for the ADR-007 hardening primitives added to auth.go:
// constant-time comparison, the five-state credential vocabulary, session
// cookie attributes, and Origin/Host/CSRF/query-credential helpers. These
// exercise the building blocks gateway.Server composes into its own
// request-time auth decision; see gateway/server_security_test.go for the
// end-to-end route-level matrix.

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// ─── ConstantTimeEqual ──────────────────────────────────────────────────

func TestConstantTimeEqual(t *testing.T) {
	cases := []struct {
		name string
		a, b string
		want bool
	}{
		{"equal", "secret-token-value", "secret-token-value", true},
		{"different same length", "secret-token-value", "SECRET-token-value", false},
		{"different length shorter", "short", "shorter-value", false},
		{"different length longer", "shorter-value", "short", false},
		{"both empty", "", "", true},
		{"one empty", "", "x", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ConstantTimeEqual(tc.a, tc.b); got != tc.want {
				t.Fatalf("ConstantTimeEqual(%q,%q)=%v, want %v", tc.a, tc.b, got, tc.want)
			}
		})
	}
}

// ─── EvaluateCredential: the five-state matrix ─────────────────────────

func TestEvaluateCredential_FiveStateMatrix(t *testing.T) {
	const expected = "the-real-credential"
	future := time.Now().Add(time.Hour)
	past := time.Now().Add(-time.Hour)

	cases := []struct {
		name      string
		presented string
		expected  string
		revoked   bool
		expiresAt time.Time
		want      CredentialState
	}{
		{"absent: empty presented", "", expected, false, time.Time{}, CredentialAbsent},
		{"invalid: wrong value", "wrong-guess", expected, false, time.Time{}, CredentialInvalid},
		{"invalid: no server credential provisioned", "anything", "", false, time.Time{}, CredentialInvalid},
		{"valid: exact match, no expiry", expected, expected, false, time.Time{}, CredentialValid},
		{"valid: exact match, not yet expired", expected, expected, false, future, CredentialValid},
		{"expired: exact match, past expiry", expected, expected, false, past, CredentialExpired},
		{"revoked: exact match, revoked (even if also expired)", expected, expected, true, past, CredentialRevoked},
		{"revoked wins over unexpired", expected, expected, true, future, CredentialRevoked},
		{"invalid presented does not leak revoked/expired state", "wrong-guess", expected, true, past, CredentialInvalid},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := EvaluateCredential(tc.presented, tc.expected, tc.revoked, tc.expiresAt)
			if got != tc.want {
				t.Fatalf("EvaluateCredential(%q,%q,revoked=%v,exp=%v)=%v, want %v",
					tc.presented, tc.expected, tc.revoked, tc.expiresAt, got, tc.want)
			}
		})
	}
}

// ─── StableAuthError ────────────────────────────────────────────────────

func TestStableAuthError_NeverIncludesCredential(t *testing.T) {
	err := NewStableAuthError(CredentialInvalid)
	if got := err.Error(); got != "unauthenticated: invalid" {
		t.Fatalf("Error()=%q, want stable %q", got, "unauthenticated: invalid")
	}
}

func TestStableAuthError_UnwrapsToStandardSentinels(t *testing.T) {
	if err := NewStableAuthError(CredentialExpired); !errIs(err, ErrUnauthenticated) {
		t.Fatalf("expired StableAuthError must satisfy errors.Is(_, ErrUnauthenticated)")
	}
	if err := NewStableAuthError(CredentialRevoked); !errIs(err, ErrRevoked) {
		t.Fatalf("revoked StableAuthError must satisfy errors.Is(_, ErrRevoked)")
	}
	if err := NewStableAuthError(CredentialRevoked); errIs(err, ErrUnauthenticated) {
		// Revoked wraps ErrRevoked, not the generic ErrUnauthenticated —
		// callers that need to distinguish revoked-vs-other must be able to.
		t.Fatalf("revoked StableAuthError must NOT satisfy errors.Is(_, ErrUnauthenticated) via Unwrap chain ambiguity")
	}
}

// errIs is a tiny local shim so this file only imports what it needs.
func errIs(err error, target error) bool {
	for err != nil {
		if err == target {
			return true
		}
		u, ok := err.(interface{ Unwrap() error })
		if !ok {
			return false
		}
		err = u.Unwrap()
	}
	return false
}

// ─── Session cookies ─────────────────────────────────────────────────────

func TestNewSessionCookie_AttributesAreAlwaysStrict(t *testing.T) {
	expires := time.Now().Add(time.Hour)
	c := NewSessionCookie(SessionCookieName, "opaque-session-id", "/", expires)

	if !c.HttpOnly {
		t.Error("session cookie must be HttpOnly")
	}
	if !c.Secure {
		t.Error("session cookie must always be Secure")
	}
	if c.SameSite != http.SameSiteStrictMode {
		t.Errorf("session cookie SameSite=%v, want SameSiteStrictMode", c.SameSite)
	}
	if c.Path != "/" {
		t.Errorf("session cookie Path=%q, want %q", c.Path, "/")
	}
	if c.Value != "opaque-session-id" {
		t.Errorf("session cookie Value=%q, want the opaque id, never the raw credential", c.Value)
	}
}

func TestExpiredSessionCookie_DeletesImmediately(t *testing.T) {
	c := ExpiredSessionCookie(SessionCookieName, "/")
	if c.MaxAge >= 0 {
		t.Fatalf("ExpiredSessionCookie MaxAge=%d, want negative (delete now)", c.MaxAge)
	}
	if !c.HttpOnly || !c.Secure || c.SameSite != http.SameSiteStrictMode {
		t.Fatalf("ExpiredSessionCookie must retain HttpOnly/Secure/SameSite=Strict so it overrides the original cookie")
	}
}

// ─── Origin / Host / CSRF ────────────────────────────────────────────────

func TestAllowedOrigin(t *testing.T) {
	allowed := []string{"https://127.0.0.1:8787", "https://localhost:8787"}

	cases := []struct {
		name    string
		origin  string
		referer string
		want    bool
	}{
		{"exact match via Origin", "https://127.0.0.1:8787", "", true},
		{"case-insensitive match", "HTTPS://127.0.0.1:8787", "", true},
		{"mismatched origin", "https://evil.example.com", "", false},
		{"no origin, no referer -> deny", "", "", false},
		{"referer fallback when origin absent", "", "https://127.0.0.1:8787/some/path", true},
		{"referer fallback mismatched", "", "https://evil.example.com/x", false},
		{"origin present wins over referer even if referer would match", "https://evil.example.com", "https://127.0.0.1:8787/x", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodGet, "/api/peers", nil)
			if tc.origin != "" {
				r.Header.Set("Origin", tc.origin)
			}
			if tc.referer != "" {
				r.Header.Set("Referer", tc.referer)
			}
			if got := AllowedOrigin(r, allowed); got != tc.want {
				t.Fatalf("AllowedOrigin()=%v, want %v", got, tc.want)
			}
		})
	}
}

func TestAllowedOrigin_EmptyAllowlistDeniesEverything(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/api/peers", nil)
	r.Header.Set("Origin", "https://127.0.0.1:8787")
	if AllowedOrigin(r, nil) {
		t.Fatal("an empty allowlist must fail closed, even for a plausible-looking Origin")
	}
}

func TestAllowedHost(t *testing.T) {
	allowed := []string{"127.0.0.1:8787"}
	r := httptest.NewRequest(http.MethodGet, "/api/peers", nil)
	r.Host = "127.0.0.1:8787"
	if !AllowedHost(r, allowed) {
		t.Fatal("expected exact Host match to be allowed")
	}
	r.Host = "attacker.example.com"
	if AllowedHost(r, allowed) {
		t.Fatal("mismatched Host must be denied")
	}
	r.Host = "127.0.0.1:8787"
	if AllowedHost(r, nil) {
		t.Fatal("an empty allowlist must fail closed")
	}
}

func TestCSRFTokenValid(t *testing.T) {
	r := httptest.NewRequest(http.MethodPost, "/api/select", nil)
	r.Header.Set(CSRFHeaderName, "bound-csrf-token")
	if !CSRFTokenValid(r, "bound-csrf-token") {
		t.Fatal("matching CSRF token must be valid")
	}

	r2 := httptest.NewRequest(http.MethodPost, "/api/select", nil)
	r2.Header.Set(CSRFHeaderName, "wrong-token")
	if CSRFTokenValid(r2, "bound-csrf-token") {
		t.Fatal("mismatched CSRF token must be denied")
	}

	r3 := httptest.NewRequest(http.MethodPost, "/api/select", nil)
	if CSRFTokenValid(r3, "bound-csrf-token") {
		t.Fatal("missing CSRF header must be denied")
	}

	r4 := httptest.NewRequest(http.MethodPost, "/api/select", nil)
	r4.Header.Set(CSRFHeaderName, "anything")
	if CSRFTokenValid(r4, "") {
		t.Fatal("an empty bound token (no session) must always deny, never treat as wildcard")
	}
}

func TestIsStateChangingMethod(t *testing.T) {
	safe := []string{http.MethodGet, http.MethodHead, http.MethodOptions}
	for _, m := range safe {
		if IsStateChangingMethod(m) {
			t.Errorf("%s must not be treated as state-changing", m)
		}
	}
	unsafe := []string{http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete}
	for _, m := range unsafe {
		if !IsStateChangingMethod(m) {
			t.Errorf("%s must be treated as state-changing (CSRF-protected)", m)
		}
	}
}

// ─── Query credential rejection ──────────────────────────────────────────

func TestHasQueryCredential(t *testing.T) {
	cases := []struct {
		name string
		url  string
		want bool
	}{
		{"no query at all", "/rpc", false},
		{"benign query only", "/rpc?peer=other-agent", false},
		{"token param", "/rpc?token=secret-value", true},
		{"access_token param", "/sse?access_token=secret-value", true},
		{"auth param", "/ws?auth=secret-value", true},
		{"bearer param", "/rpc?bearer=secret-value", true},
		{"empty token value does not count", "/rpc?token=", false},
		{"token alongside benign params", "/rpc?peer=x&token=secret", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodGet, tc.url, nil)
			if got := HasQueryCredential(r); got != tc.want {
				t.Fatalf("HasQueryCredential(%q)=%v, want %v", tc.url, got, tc.want)
			}
		})
	}
}

// ─── BearerAuth: existing exported behavior stays intact ─────────────────
// (regression guard — BearerAuth predates this hardening pass and other
// tests in mux_test.go already exercise WithAuth(BearerAuth(...)) end to
// end; this focuses specifically on the query-string-is-never-read
// invariant BearerAuth already had, which the hardening work must preserve.)

func TestBearerAuth_NeverReadsQueryString(t *testing.T) {
	af := BearerAuth(func(token string) (string, error) { return "", nil })
	r := httptest.NewRequest(http.MethodGet, "/rpc?token=leaked-in-query", nil)
	if _, err := af(r); err == nil {
		t.Fatal("BearerAuth must reject a request with only a query-string token; it never reads the query string")
	}
}
