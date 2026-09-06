package serve

import (
	"context"
	"crypto/subtle"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// AuthFunc authenticates an HTTP request.  On success it returns a (possibly
// derived) context carrying user identity; the returned context is forwarded
// to method handlers.  On failure it returns a non-nil error and the caller
// short-circuits with a 401 response.
type AuthFunc func(r *http.Request) (context.Context, error)

// ErrUnauthenticated is the standard auth-failure sentinel.
var ErrUnauthenticated = errors.New("unauthenticated")

// ErrRevoked is the stable sentinel for a credential/session that was valid
// but has since been revoked. It wraps ErrUnauthenticated so callers that
// only check errors.Is(err, ErrUnauthenticated) keep working, while callers
// that need to distinguish "revoked" (re-provisioning required) from
// "expired" (re-issuance suffices) can check errors.Is(err, ErrRevoked) or
// inspect CredentialState directly. Per ADR-007, no credential material is
// ever included in either sentinel or any error derived from it.
var ErrRevoked = errors.New("revoked")

// authContextKey is the context key under which BearerAuth stores the
// validated user id.
type authContextKey struct{}

// UserIDFromContext extracts the user id stored by BearerAuth, returning
// ("", false) if not present.
func UserIDFromContext(ctx context.Context) (string, bool) {
	v, ok := ctx.Value(authContextKey{}).(string)
	return v, ok
}

// BearerAuth returns an AuthFunc that requires an "Authorization: Bearer
// <token>" header and resolves it via verify.  verify returns the user id
// on success (or "" + nil if the verifier doesn't track identity).
//
// Empty tokens, missing headers, or non-Bearer schemes all fail with
// ErrUnauthenticated.  verify errors are wrapped with the same sentinel.
func BearerAuth(verify func(token string) (userID string, err error)) AuthFunc {
	return func(r *http.Request) (context.Context, error) {
		h := r.Header.Get("Authorization")
		if h == "" {
			return nil, ErrUnauthenticated
		}
		const prefix = "Bearer "
		if !strings.HasPrefix(h, prefix) {
			return nil, ErrUnauthenticated
		}
		token := strings.TrimSpace(h[len(prefix):])
		if token == "" {
			return nil, ErrUnauthenticated
		}
		userID, err := verify(token)
		if err != nil {
			return nil, ErrUnauthenticated
		}
		ctx := r.Context()
		if userID != "" {
			ctx = context.WithValue(ctx, authContextKey{}, userID)
		}
		return ctx, nil
	}
}

// applyAuth runs the configured AuthFunc (if any) and returns the gated
// context.  Returns (ctx, true) when the request is authenticated; on
// failure writes a 401 to w and returns (_, false).
func (m *Mux) applyAuth(w http.ResponseWriter, r *http.Request) (context.Context, bool) {
	if m.auth == nil {
		return r.Context(), true
	}
	ctx, err := m.auth(r)
	if err != nil {
		// Install the write deadline before the 401 write itself, matching
		// every other early-denial path in this package (method-not-allowed,
		// body-too-large, parse error, upgrade rejection): an
		// authentication failure must not be able to write unbounded or
		// hang the connection any more than a successful response can.
		if !setResponseWriteDeadline(w) {
			return nil, false
		}
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return nil, false
	}
	return ctx, true
}

// ---------------------------------------------------------------------------
// ADR-007 hardening primitives.
//
// The functions and types below are dependency-light building blocks used by
// gateway.Server (and available to any other serve.Mux-based transport) to
// implement the restrictive listener/authentication defaults required by
// docs/architecture/swarm-attach/adr-007-gateway-security.md: constant-time
// bearer comparison, a stable five-state credential-outcome vocabulary,
// HttpOnly/Secure/SameSite=Strict session cookies, and Origin/Host/CSRF
// checks for browser-facing state-changing routes. They intentionally do NOT
// introduce AuthenticatedEndpoint, signed discovery, or any Phase 02 identity
// type; they operate purely on the single shared credential a caller already
// holds (e.g. gateway.Options.Token) and an opaque session value the caller
// mints and stores itself.
// ---------------------------------------------------------------------------

// CredentialState is the stable, machine-checkable outcome of validating a
// presented gateway credential or session. The same five states apply
// uniformly across HTTP, SSE, and WebSocket transports and across loopback
// and non-loopback listeners -- loopback bind confers no authentication by
// itself (see ADR-007 "Local authentication model for sensitive loopback
// routes").
type CredentialState string

const (
	// CredentialValid means the presented secret matched, is unexpired, and
	// is unrevoked. The caller may proceed to capability authorization.
	CredentialValid CredentialState = "valid"
	// CredentialAbsent means no header, cookie, or other credential was
	// presented at all. Distinct from CredentialInvalid so audit trails and
	// remediation text can say "no credential" instead of "wrong credential".
	CredentialAbsent CredentialState = "absent"
	// CredentialInvalid means a credential was presented but failed the
	// constant-time comparison (malformed, unknown, or simply wrong).
	CredentialInvalid CredentialState = "invalid"
	// CredentialExpired means the credential matched but its expiry has
	// passed. Distinguishable from CredentialInvalid so a legitimate client
	// knows to trigger re-issuance rather than treat it as a hard failure.
	CredentialExpired CredentialState = "expired"
	// CredentialRevoked means the credential matched but has been explicitly
	// revoked. Effective immediately, independent of expiry.
	CredentialRevoked CredentialState = "revoked"
)

// ConstantTimeEqual reports whether a and b are byte-for-byte equal using a
// constant-time comparison (crypto/subtle), so bearer/session-token checks do
// not leak timing information about how many leading bytes matched. A length
// mismatch is detected and returned as false without a variable-time byte
// scan; crypto/subtle.ConstantTimeCompare itself already declines to compare
// mismatched-length slices for anything beyond the length check, which is the
// same accepted length-only tradeoff every constant-time secret comparison in
// the standard library makes.
func ConstantTimeEqual(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}

// EvaluateCredential resolves the CredentialState for a presented secret
// against the single shared expected secret plus expiry/revocation facts.
// presented == "" is always CredentialAbsent, checked before any comparison
// so an empty credential never takes the constant-time compare path (there is
// nothing secret to leak about "no credential offered").  A non-matching
// presented value is CredentialInvalid regardless of expiry/revocation state
// (an attacker guessing a wrong value must not learn whether the REAL
// credential happens to be expired or revoked).  Only an exact match is
// further classified as expired, revoked, or valid.
func EvaluateCredential(presented, expected string, revoked bool, expiresAt time.Time) CredentialState {
	if presented == "" {
		return CredentialAbsent
	}
	if expected == "" || !ConstantTimeEqual(presented, expected) {
		return CredentialInvalid
	}
	if revoked {
		return CredentialRevoked
	}
	if !expiresAt.IsZero() && time.Now().After(expiresAt) {
		return CredentialExpired
	}
	return CredentialValid
}

// StableAuthError pairs the standard ErrUnauthenticated/ErrRevoked sentinels
// with a CredentialState so a caller can respond with a stable, machine
// checkable category ("unauthenticated", "expired", "revoked", ...) without
// ever formatting the presented credential into an error string.
type StableAuthError struct {
	State CredentialState
}

// Error never includes credential material -- only the fixed state label.
func (e *StableAuthError) Error() string { return "unauthenticated: " + string(e.State) }

// Unwrap lets errors.Is(err, ErrUnauthenticated) and errors.Is(err,
// ErrRevoked) both work depending on State, so existing callers that only
// understand the coarse sentinel keep functioning.
func (e *StableAuthError) Unwrap() error {
	if e.State == CredentialRevoked {
		return ErrRevoked
	}
	return ErrUnauthenticated
}

// NewStableAuthError wraps a non-valid CredentialState as a *StableAuthError.
// Calling it with CredentialValid is a caller bug; it still returns a non-nil
// error (fail closed) rather than silently succeeding.
func NewStableAuthError(state CredentialState) *StableAuthError {
	return &StableAuthError{State: state}
}

// SessionCookieName is the default name gateway.Server (and any other
// serve.Mux-based transport) uses for the browser session cookie minted by
// the bootstrap flow. Callers may use a different name; this constant only
// documents the expected default.
const SessionCookieName = "swarm_gw_session"

// NewSessionCookie returns a session cookie that is always HttpOnly, always
// Secure, and always SameSite=Strict, scoped to path and expiring at
// expiresAt. Per ADR-007 "Issuance", browser session bootstrap is only ever
// reachable over a verified HTTPS origin, so this constructor does not take a
// "secure" parameter to disable the Secure attribute -- there is no policy
// path under which a gateway session cookie may be sent over plaintext.
func NewSessionCookie(name, value, path string, expiresAt time.Time) *http.Cookie {
	return &http.Cookie{
		Name:     name,
		Value:    value,
		Path:     path,
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteStrictMode,
		Expires:  expiresAt,
	}
}

// ExpiredSessionCookie returns a cookie that instructs the browser to delete
// the named session cookie immediately (used when a session is revoked or
// expires server-side and the response can still set headers).  It carries
// the same HttpOnly/Secure/SameSite=Strict attributes as NewSessionCookie so
// it overrides exactly the cookie that was set.
func ExpiredSessionCookie(name, path string) *http.Cookie {
	return &http.Cookie{
		Name:     name,
		Value:    "",
		Path:     path,
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteStrictMode,
		MaxAge:   -1,
		Expires:  time.Unix(0, 0),
	}
}

// RequestOrigin returns the request's Origin header, falling back to the
// Referer's scheme+host only when Origin is absent -- matching ADR-007's
// "Origin (falling back to Referer only when Origin is absent)" rule. It
// returns "" when neither header yields a usable origin, which callers must
// treat as a denial for any Origin-gated route, never as an implicit match.
func RequestOrigin(r *http.Request) string {
	if origin := r.Header.Get("Origin"); origin != "" {
		return origin
	}
	referer := r.Header.Get("Referer")
	if referer == "" {
		return ""
	}
	u, err := url.Parse(referer)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return ""
	}
	return u.Scheme + "://" + u.Host
}

// AllowedOrigin reports whether r's Origin (or Referer fallback per
// RequestOrigin) exactly matches one entry of allowed. An empty allowed list
// always denies (fail closed) -- an Origin allowlist must be explicitly
// configured before any cookie-authenticated or CSRF-protected route can
// succeed. Matching is case-insensitive on the scheme+host string and never
// a substring/prefix match, so "https://evil.com/https://good.com" cannot be
// confused with an allowed origin.
func AllowedOrigin(r *http.Request, allowed []string) bool {
	if len(allowed) == 0 {
		return false
	}
	origin := RequestOrigin(r)
	if origin == "" {
		return false
	}
	for _, a := range allowed {
		if strings.EqualFold(origin, a) {
			return true
		}
	}
	return false
}

// AllowedHost reports whether r.Host exactly matches one entry of allowed
// (case-insensitive). An empty allowed list always denies (fail closed),
// matching AllowedOrigin's posture: a Host allowlist must be explicitly
// configured before a browser-facing sensitive route accepts requests.
func AllowedHost(r *http.Request, allowed []string) bool {
	if len(allowed) == 0 {
		return false
	}
	for _, a := range allowed {
		if strings.EqualFold(r.Host, a) {
			return true
		}
	}
	return false
}

// CSRFHeaderName is the header a cookie-authenticated state-changing request
// must carry a synchronizer/double-submit CSRF token in.
const CSRFHeaderName = "X-CSRF-Token"

// CSRFTokenValid performs a constant-time comparison of the request's
// X-CSRF-Token header against bound, the token issued alongside the session
// by the bootstrap flow. An empty bound token (no session, or a session that
// never got a CSRF token) or an empty header always fails closed.
func CSRFTokenValid(r *http.Request, bound string) bool {
	if bound == "" {
		return false
	}
	presented := r.Header.Get(CSRFHeaderName)
	if presented == "" {
		return false
	}
	return ConstantTimeEqual(presented, bound)
}

// IsStateChangingMethod reports whether method is one that requires CSRF
// protection on a cookie-authenticated route (everything except the safe,
// idempotent methods GET/HEAD/OPTIONS).
func IsStateChangingMethod(method string) bool {
	switch method {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		return false
	default:
		return true
	}
}

// QueryCredentialParams is the closed set of query-string parameter names
// that ADR-007 forbids as a credential transport ("Query credentials are
// always rejected"). It is checked case-sensitively against the exact
// parameter name; servers must reject the request outright the moment any of
// these are present, even when a valid header or cookie is also present.
var QueryCredentialParams = []string{"token", "access_token", "auth", "authorization", "bearer"}

// HasQueryCredential reports whether r's query string carries any parameter
// named in QueryCredentialParams with a non-empty value.
func HasQueryCredential(r *http.Request) bool {
	if r.URL.RawQuery == "" {
		return false
	}
	q := r.URL.Query()
	for _, p := range QueryCredentialParams {
		if q.Get(p) != "" {
			return true
		}
	}
	return false
}
