// Package gateway is the reusable LAN-bridge core shared by the standalone
// swarm-gateway command and the in-process daemon gateway.
//
// It provides:
//   - the embedded mobile PWA (web/**) and a static handler for it,
//   - LAN-IP ranking so the banner QR and discovery beacon advertise a
//     reachable address (never a docker bridge or VPN tunnel),
//   - the UDP discovery beacon (StartBeacon) native apps / Swarm Desktop use,
//   - a Server that mounts the PWA + /api/* control routes and delegates the
//     data routes (/rpc, /sse, /ws) to handlers the caller injects.
//
// The daemon injects handlers straight off its own serve.Mux (it IS the peer,
// so there is no proxy hop). The standalone command injects a reverse-proxy
// handler so it can drive OTHER peers on the box.
package gateway

import (
	"context"
	"crypto/rand"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"net"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/Swarm-Code/mono/swarm-sdk/a2a"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/lan"
	"github.com/Swarm-Code/mono/swarm-sdk/serve"
)

// webFS embeds the PWA assets.
//
//go:embed all:web
var webFS embed.FS

// ---------------------------------------------------------------------------
// Restrictive listener grants (ADR-007 "Restrictive listener default").
//
// Loopback is the ONLY implicit listener grant. A non-loopback grant is
// authenticated control, explicitly open LAN control, or an explicit,
// conspicuously insecure static-only override. Neither the caller nor any
// request-time code can widen a RouteMask.
// ---------------------------------------------------------------------------

// RouteMask is an immutable route-capability mask carried by a ListenerGrant.
// The two package-level values below (AuthenticatedControlMask,
// InsecureStaticOnlyMask) are the only RouteMask values gateway ever
// constructs; there is no exported constructor, so a caller cannot fabricate
// a wider mask.
type RouteMask struct {
	name      string
	sensitive bool
}

// String returns the mask's stable name, also used as the machine-checkable
// error/status value ("authenticated_control" / "insecure_static_only").
func (m RouteMask) String() string { return m.name }

// AllowsSensitive reports whether this mask may expose control, streaming,
// peer-inventory, proxy, or outbound-capable routes (/api/peers,
// /api/select, /rpc, /sse, /ws, and the browser session bootstrap route).
func (m RouteMask) AllowsSensitive() bool { return m.sensitive }

// AuthenticatedControlMask is the full route mask: every route may be
// registered, gated by authentication.
var AuthenticatedControlMask = RouteMask{name: "authenticated_control", sensitive: true}

// InsecureStaticOnlyMask is the immutable static-diagnostic mask: it may
// serve only public static assets and the non-sensitive /api/version hash.
// It can never register /api/peers, /api/select, /rpc, /sse, /ws, session
// bootstrap, or any other control/data/proxy/outbound route.
var InsecureStaticOnlyMask = RouteMask{name: "insecure_static_only", sensitive: false}

// ListenerGrant is the exact approved network exposure for one gateway
// instance: whether the bind address is loopback, the immutable RouteMask in
// effect, and whether a verified TLS listener identity is mandatory before
// sensitive routes may be reached. Grants are computed once by
// AuthorizeListener and never widened afterward.
type ListenerGrant struct {
	// Loopback is true when the bind address is an explicit loopback
	// address (127.0.0.0/8, ::1, or "localhost"). Wildcard/unspecified
	// addresses and interface/LAN addresses are never loopback.
	Loopback bool
	// Mask is the immutable route-capability mask in effect.
	Mask RouteMask
	// RequireTLS is true when a verified TLS listener identity is mandatory
	// before this grant's sensitive routes may be registered/dispatched
	// (always true for a credentialed non-loopback authenticated_control grant).
	RequireTLS bool
	// Insecure is true when this grant is the explicit, conspicuously
	// insecure static-only override (never inferred).
	Insecure bool
	// AllowUnauthenticated is true only for the explicitly requested open LAN
	// daemon mode.
	AllowUnauthenticated bool
}

// ErrCredentialsRequired is returned by AuthorizeListener when a non-loopback
// bind is requested without provisioned gateway credentials, the explicit
// open-LAN option, or the insecure static-only override. The caller must not
// bind.
var ErrCredentialsRequired = errors.New("gateway: non-loopback authenticated_control requires provisioned credentials")

// ErrTLSRequired is returned by AuthorizeListener when a non-loopback bind is
// requested with credentials but no verified TLS listener identity. The
// caller must not bind; sensitive routes are never registered without TLS.
var ErrTLSRequired = errors.New("gateway: non-loopback authenticated_control requires a verified TLS listener identity")

// IsLoopbackAddr reports whether addr -- in net.Listen form ("127.0.0.1:8787",
// ":8787", "0.0.0.0:8787", "[::1]:8787") -- is an explicit loopback address.
// Wildcard addresses, unspecified IPv4/IPv6, interface-derived LAN addresses,
// and hostnames that are not literally "localhost" all count as non-loopback,
// per ADR-007's restrictive-default rule: an unresolved/unknown host must
// never be assumed loopback.
func IsLoopbackAddr(addr string) bool {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		host = addr
	}
	host = strings.TrimSuffix(strings.TrimPrefix(host, "["), "]")
	if host == "" {
		return false // wildcard/unspecified bind ("", ":8787") is non-loopback
	}
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return false // unresolved hostname: never assumed loopback
	}
	return ip.IsLoopback()
}

// AuthorizeListener computes the immutable ListenerGrant for binding at addr
// under opts, following ADR-007's restrictive default exactly:
//
//   - addr == "" is treated as loopback. This is the safe default for a
//     caller that has not yet threaded a real bind address through Options
//     (a deferred integration seam -- see CONTRACT.md); it never WIDENS
//     exposure, it only preserves the historical loopback-shaped default.
//   - Any other loopback address grants the full route mask; loopback bind
//     address is never itself authentication (every sensitive route still
//     authenticates -- see Server.auth).
//   - A non-loopback address grants authenticated_control with provisioned
//     credentials and verified TLS, explicitly open-LAN authenticated_control,
//     or (only when opts.Insecure is explicitly set) the immutable
//     insecure_static_only mask. Any other combination is denied and the
//     caller must not bind.
func AuthorizeListener(addr string, opts Options) (ListenerGrant, error) {
	if addr == "" || IsLoopbackAddr(addr) {
		return ListenerGrant{Loopback: true, Mask: AuthenticatedControlMask}, nil
	}
	if opts.AllowUnauthenticatedLAN {
		return ListenerGrant{
			Loopback:             false,
			Mask:                 AuthenticatedControlMask,
			AllowUnauthenticated: true,
		}, nil
	}
	if opts.Insecure {
		return ListenerGrant{Loopback: false, Mask: InsecureStaticOnlyMask, Insecure: true}, nil
	}
	now := opts.now()
	if opts.Authority == nil || opts.Authority.Ready(now) != nil {
		return ListenerGrant{}, ErrCredentialsRequired
	}
	advertised := opts.AdvertisedListener
	if advertised == "" {
		advertised = addr
	}
	if opts.TLSEvidence == nil || opts.TLSEvidence.ValidFor(advertised, now) != nil {
		return ListenerGrant{}, ErrTLSRequired
	}
	return ListenerGrant{Loopback: false, Mask: AuthenticatedControlMask, RequireTLS: true}, nil
}

// Clock is the deterministic time seam used for credential/session expiry.
type Clock interface {
	Now() time.Time
	After(time.Duration) <-chan time.Time
}

type realClock struct{}

func (realClock) Now() time.Time                         { return time.Now() }
func (realClock) After(d time.Duration) <-chan time.Time { return time.After(d) }

// Options configures a Server.
type Options struct {
	// Swarm is the swarm name used to answer /api/peers.
	Swarm string
	// Token, when non-empty, is the shared gateway credential required on
	// every sensitive route via "Authorization: Bearer <token>" or, for
	// browser clients over a verified HTTPS origin, an HttpOnly/Secure/
	// SameSite=Strict session minted by the /api/session/bootstrap flow.
	// It is NEVER accepted as a "?token=" (or any other) query parameter --
	// query credentials are rejected unconditionally, even alongside a valid
	// header or cookie. An empty Token means no credential is provisioned:
	// every sensitive route denies with the absent-credential category; it
	// no longer disables authentication.
	Token string
	// Authority is securely loaded provisioned credential authority. It is
	// mandatory for non-loopback authenticated control and, when supplied on
	// loopback, supersedes all legacy Token state.
	Authority *lan.CredentialAuthority
	// TLSEvidence is opaque evidence returned only by lan's exact keypair,
	// validity, server-auth, and listener-SAN validation path. It is mandatory
	// for non-loopback authenticated control.
	TLSEvidence *lan.ListenerTLSEvidence
	// AdvertisedListener is the concrete hostname/IP and port clients receive.
	// It must exactly match TLSEvidence; wildcard bind addresses cannot serve
	// as advertised identities.
	AdvertisedListener string
	// ListenAddr is the network address this gateway will bind, in
	// net.Listen form ("127.0.0.1:8787", ":8787", "0.0.0.0:8787"). Empty is
	// treated as loopback -- see AuthorizeListener.
	ListenAddr string
	// Insecure explicitly opts a non-loopback bind into the conspicuously
	// insecure, immutable static-only grant (public assets + version hash
	// only). It is NEVER inferred from an empty Token, a wildcard bind, or a
	// TLS/credential failure -- see AuthorizeListener.
	Insecure bool
	// AllowUnauthenticatedLAN explicitly opts a non-loopback listener into
	// unauthenticated control for an operator-managed open LAN deployment.
	AllowUnauthenticatedLAN bool
	// TLSConfigured reports whether a verified TLS listener identity is
	// active for this gateway instance. It is mandatory for any non-loopback
	// authenticated_control grant and independently gates the browser
	// session bootstrap/cookie routes even on loopback (ADR-007: sensitive
	// browser HTTP/SSE/WebSocket routes are HTTPS-only, including loopback).
	// The gateway itself never terminates TLS; the caller sets this only
	// once it has verified the *http.Server (or reverse proxy in front of
	// it) is actually serving this handler over TLS.
	TLSConfigured bool
	// Clock controls authentication/session expiry. Nil uses the system clock.
	Clock Clock
	// CredentialExpiresAt, when non-zero, is Token's expiry. A request
	// presenting a byte-for-byte-matching Token after this time is the
	// "expired" credential state, distinct from "invalid".
	CredentialExpiresAt time.Time
	// CredentialRevoked marks Token as revoked effective immediately. A
	// request presenting a byte-for-byte-matching Token is the "revoked"
	// credential state, distinct from "expired"/"invalid".
	CredentialRevoked bool
	// AllowedOrigins is the exact-match allowlist (e.g.
	// "https://127.0.0.1:8787") checked against a browser request's Origin
	// (or Referer fallback) before any cookie-authenticated or
	// CSRF-protected route is dispatched. Empty denies all cookie/browser
	// routes (fail closed) -- it must be explicitly configured.
	AllowedOrigins []string
	// AllowedHosts is the exact-match allowlist checked against a browser
	// request's Host header for the same routes as AllowedOrigins. Empty
	// denies all cookie/browser routes (fail closed).
	AllowedHosts []string
	// RPCHandler, SSEHandler, WSHandler serve /rpc, /sse and /ws respectively.
	// Any that are nil respond 503. The daemon passes its serve.Mux handlers;
	// the standalone binary passes a reverse-proxy handler.
	RPCHandler http.Handler
	SSEHandler http.Handler
	WSHandler  http.Handler
	// Selected reports the currently selected peer handle for /api/peers, and
	// Select updates it. Both may be nil (the daemon has a single implicit
	// peer — itself — and does not need selection).
	Selected func() string
	Select   func(handle string)
	// SelfHandle is the A2A handle of the process hosting this gateway (the
	// daemon). When set, data routes become peer-aware: ?peer=<other> and
	// non-self selections are proxied to that peer's serve interface, and /ws
	// with no selection prefers a live Swarm Desktop engine peer (the
	// desktop/Android app's protocol lives there, not on the daemon's mux).
	SelfHandle string
	// GatewayPolicy, when non-nil, enables the ADR-007 Phase 02 policy-bound
	// peer transport allow path in proxyTo (see proxy.go). It is nil in
	// every current production Options wiring, so peerRoute's existing
	// calls continue to deny with the stable authenticated_endpoint_unavailable
	// result exactly as before. It has no effect unless EndpointForRequest
	// is also set.
	GatewayPolicy *lan.GatewayPolicy
	// EndpointForRequest, when non-nil, attempts to construct the
	// lan.AuthenticatedEndpoint authorized for a peer proxy request. It MUST
	// only ever return ok==true for an endpoint built through
	// lan.NewLocalAuthenticatedEndpoint or lan.NewRemoteAuthenticatedEndpoint
	// -- never a raw presence URL wrapped as-is. A nil return (ok==false),
	// or a nil GatewayPolicy, keeps the request denied with the stable
	// authenticated_endpoint_unavailable result.
	EndpointForRequest func(r *http.Request) (lan.AuthenticatedEndpoint, bool)
}

// Server wires the gateway HTTP surface. Build its handler with Handler().
type Server struct {
	opts Options

	grantOnce sync.Once
	grantVal  ListenerGrant
	grantErr  error

	mu       sync.Mutex
	sessions map[string]gatewaySession
}

// gatewaySession is the server-side record for a browser session cookie
// minted by /api/session/bootstrap. It carries no long-lived secret -- the
// cookie value is an opaque, unguessable reference, never Token itself.
type gatewaySession struct {
	csrf      string
	binding   lan.CredentialBinding
	expiresAt time.Time
}

const sessionTTL = 12 * time.Hour

// New returns a Server for the given options.
func New(opts Options) *Server { return &Server{opts: opts} }

func (o Options) clock() Clock {
	if o.Clock != nil {
		return o.Clock
	}
	return realClock{}
}

func (o Options) now() time.Time { return o.clock().Now() }

// grant lazily computes (and memoizes) this Server's ListenerGrant.
func (s *Server) grant() (ListenerGrant, error) {
	s.grantOnce.Do(func() {
		s.grantVal, s.grantErr = AuthorizeListener(s.opts.ListenAddr, s.opts)
	})
	return s.grantVal, s.grantErr
}

// Handler returns the composed http.Handler: PWA static shell on /, control
// routes under /api, and the injected data routes /rpc /sse /ws (token-gated).
//
// Route composition is gated once, at construction, by the Server's
// ListenerGrant: a denied grant (non-loopback without valid credentials/TLS
// and without the explicit insecure override) serves nothing but a stable
// gateway_unavailable response -- it fails closed rather than falling back to
// any wider default. A granted insecure_static_only mask never registers a
// handler capable of serving /api/peers, /api/select, /rpc, /sse, /ws, or
// session bootstrap; those paths instead get a handler that always answers
// the stable insecure_static_only denial before any resolution happens.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()

	grant, grantErr := s.grant()
	if grantErr != nil {
		// Fail closed: an explicitly denied non-loopback grant serves
		// nothing, not even static assets. ADR-007 "Failure isolation":
		// startup/policy-read failure disables the affected gateway
		// capability; it never falls back to a wider default.
		mux.Handle("/", http.HandlerFunc(s.handleGatewayUnavailable))
		// serve.WithResponseWriteBounds installs a bounded write deadline
		// before ANY synchronous write this mux can produce -- including
		// the unconditional gateway_unavailable denial above -- so a
		// non-reading client can never hold the failed-closed response (or
		// the handler goroutine backing it) open indefinitely.
		return serve.WithResponseWriteBounds(mux)
	}

	if grant.Mask.AllowsSensitive() {
		mux.HandleFunc("/api/peers", s.auth(s.handlePeers))
		mux.HandleFunc("/api/select", s.auth(s.handleSelect))
		mux.HandleFunc("/api/session/bootstrap", s.handleSessionBootstrap)
		mux.Handle("/rpc", s.auth(s.peerRoute(s.opts.RPCHandler, false)))
		mux.Handle("/sse", s.auth(s.peerRoute(s.opts.SSEHandler, false)))
		mux.Handle("/ws", s.auth(s.peerRoute(s.opts.WSHandler, true)))
	} else {
		for _, path := range [...]string{
			"/api/peers", "/api/select", "/api/session/bootstrap", "/rpc", "/sse", "/ws",
		} {
			mux.HandleFunc(path, s.handleInsecureStaticOnly)
		}
	}
	mux.HandleFunc("/api/version", s.handleVersion) // public: no peer inventory, no config, no credential
	mux.Handle("/", StaticHandler())
	// The complete embedded gateway handler -- outer auth (Server.auth),
	// the insecure_static_only and gateway_unavailable denial paths, the
	// HTTPS-only session bootstrap flow, /api/peers /api/select control
	// routes, and every /rpc /sse /ws dispatch (including the Phase 01
	// pre-resolution remote-peer denial in peerRoute/proxyTo) -- is wrapped
	// here with serve.WithResponseWriteBounds so every synchronous write or
	// upgrader this Server can ever reach gets a deadline installed before
	// the wrapped handler runs, without altering any status/body schema,
	// route registration, or authentication ordering above.
	return serve.WithResponseWriteBounds(mux)
}

// peerRoute serves the local data handler while Phase 01 has no authenticated
// endpoint provenance. An explicit or selected non-self peer is denied before
// presence lookup, URL parsing, DNS, or dialing. Automatic desktop preference
// is likewise disabled until Phase 02 can prove endpoint provenance.
//
// Ordering exactly matches CONTRACT.md "Remote routing":
//
//   - An explicit ?peer= handle equal to SelfHandle routes locally.
//   - An explicit ?peer= handle that is not SelfHandle is denied via
//     s.proxyTo -- the stable authenticated_endpoint_unavailable response --
//     before s.opts.Selected, the local handler, or resolveDataPeer ever run.
//   - With NO explicit ?peer=, the mere PRESENCE of a selection callback
//     (s.opts.Selected != nil) represents a possible remote route and is
//     denied the same way, WITHOUT EVER INVOKING s.opts.Selected(): a prior
//     version of this function called Selected() to learn the selected
//     handle before deciding whether to deny, which is itself the exact
//     side effect this ordering must avoid (Selected can be arbitrary
//     caller-supplied code -- see the panic-on-call sentinel tests in
//     server_security_test.go).
//   - With no explicit peer AND no selection capability (Selected == nil),
//     the request routes locally -- this is the daemon's normal single-peer
//     shape, which must keep working with zero behavior change.
func (s *Server) peerRoute(h http.Handler, preferDesktop bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		_ = preferDesktop // remote auto-selection is disabled in Phase 01.
		if explicit := r.URL.Query().Get("peer"); explicit != "" {
			if explicit != s.opts.SelfHandle {
				s.proxyTo(w, r, "")
				return
			}
			// Explicit self: fall through to the local handler below.
		} else if s.opts.Selected != nil {
			// No explicit ?peer=, but a selection callback exists: deny
			// before calling it, the local handler, or any resolver --
			// invoking Selected here would itself be the prohibited side
			// effect this ordering exists to prevent.
			s.proxyTo(w, r, "")
			return
		}
		if h == nil {
			http.Error(w, `{"error":"no data handler configured"}`, http.StatusServiceUnavailable)
			return
		}
		h.ServeHTTP(w, r)
	}
}

// StaticHandler serves the embedded PWA with correct Content-Types for the
// manifest and JS. Static assets are public so the shell can load and then
// pass the token through on data requests.
func StaticHandler() http.Handler {
	sub, err := fs.Sub(webFS, "web")
	if err != nil {
		log.Fatalf("gateway: embed: %v", err)
	}
	fileServer := http.FileServer(http.FS(sub))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, ".webmanifest"):
			w.Header().Set("Content-Type", "application/manifest+json")
		case strings.HasSuffix(r.URL.Path, ".js"):
			w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
		}
		// Embedded files carry no mod time, so without this browsers
		// heuristically cache the shell and keep running STALE app.js after
		// the daemon binary is upgraded. no-cache forces revalidation (and,
		// with no validators, a refetch) on every load; the assets are tiny.
		w.Header().Set("Cache-Control", "no-cache")
		fileServer.ServeHTTP(w, r)
	})
}

// auth wraps a handler with ADR-007 authentication enforcement. Every
// sensitive route authenticates before dispatch -- loopback bind address is
// never treated as authentication. Query-string credentials are rejected
// unconditionally and first, before any other check, even when a valid
// header or cookie is also present. No downstream handler runs on denial.
func (s *Server) auth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if serve.HasQueryCredential(r) {
			s.audit(r, "query_credential")
			writeAuthError(w, "query_credential")
			return
		}
		if grant, err := s.grant(); err == nil && grant.AllowUnauthenticated {
			next(w, r)
			return
		}
		decision := s.authenticate(r)
		if !decision.ok {
			s.audit(r, decision.reason)
			writeAuthError(w, decision.reason)
			return
		}
		ctx, cancel := s.boundContext(r.Context(), decision.binding)
		defer cancel()
		next(w, r.WithContext(ctx))
	}
}

// authDecision is the internal outcome of Server.authenticate: whether the
// request may proceed, and -- when it may not -- the stable, machine
// checkable reason string reported in the response body and audit log. It
// never carries the presented credential.
type authDecision struct {
	ok      bool
	reason  string
	binding lan.CredentialBinding
}

// credentialReason maps a serve.CredentialState to the stable reason string
// used in denial responses and audit events. Absent and invalid both surface
// as the same "unauthenticated" 401-class category (ADR-007's table),
// distinguished only in the audit log's underlying CredentialState, never in
// a way that would help an attacker learn WHY a guess failed.
func credentialReason(st serve.CredentialState) string {
	switch st {
	case serve.CredentialExpired:
		return "expired"
	case serve.CredentialRevoked:
		return "revoked"
	default:
		return "unauthenticated"
	}
}

// credentialState resolves the CredentialState for a bearer/session value
// presented for comparison against the server's single shared credential,
// using a constant-time comparison (serve.EvaluateCredential /
// serve.ConstantTimeEqual).
func (s *Server) credentialState(presented string) serve.CredentialState {
	return serve.EvaluateCredential(presented, s.opts.Token, s.opts.CredentialRevoked, s.opts.CredentialExpiresAt)
}

func credentialStateFromLAN(st lan.CredentialState) serve.CredentialState {
	switch st {
	case lan.CredentialValid:
		return serve.CredentialValid
	case lan.CredentialAbsent:
		return serve.CredentialAbsent
	case lan.CredentialExpired:
		return serve.CredentialExpired
	case lan.CredentialRevoked:
		return serve.CredentialRevoked
	default:
		return serve.CredentialInvalid
	}
}

func (s *Server) authenticateBearer(presented string) (serve.CredentialState, lan.CredentialBinding) {
	if s.opts.Authority != nil {
		decision := s.opts.Authority.Authenticate(presented, s.opts.now())
		return credentialStateFromLAN(decision.State), decision.Binding
	}
	grant, err := s.grant()
	if err != nil || !grant.Loopback {
		return serve.CredentialInvalid, lan.CredentialBinding{}
	}
	state := s.credentialState(presented)
	if state != serve.CredentialValid {
		return state, lan.CredentialBinding{}
	}
	return state, lan.CredentialBinding{
		ID:        "legacy-loopback",
		Revision:  1,
		ExpiresAt: s.opts.CredentialExpiresAt,
	}
}

// authenticate implements ADR-007's "Request binding" rules for a single
// HTTP/SSE/WebSocket-upgrade request:
//
//   - Non-browser bearer clients present "Authorization: Bearer <token>" on
//     every request; this path works on any listener this Server actually
//     reaches (loopback, or a granted non-loopback authenticated_control
//     TLS listener).
//   - Browser clients present the HttpOnly/Secure/SameSite=Strict session
//     cookie automatically attached to a same-origin request. The cookie
//     path is available ONLY over a verified HTTPS connection (r.TLS !=
//     nil), even on loopback -- a plaintext listener never accepts a
//     cookie-authenticated sensitive route -- and additionally requires the
//     request's Origin/Host to match the configured allowlists and, for
//     state-changing methods, a matching CSRF token.
//   - Absent both forms, the outcome is the CredentialAbsent state.
//
// A request cannot opt into the non-browser bearer grant merely by omitting
// an Origin header: the two paths are distinguished by which credential form
// (Authorization header vs. session cookie) was presented, not by any
// browser-detection heuristic.
func (s *Server) authenticate(r *http.Request) authDecision {
	if h := r.Header.Get("Authorization"); strings.HasPrefix(h, "Bearer ") {
		token := strings.TrimPrefix(h, "Bearer ")
		st, binding := s.authenticateBearer(token)
		if st != serve.CredentialValid {
			return authDecision{reason: credentialReason(st)}
		}
		return authDecision{ok: true, binding: binding}
	}

	if c, err := r.Cookie(serve.SessionCookieName); err == nil {
		if r.TLS == nil {
			return authDecision{reason: "browser_https_required"}
		}
		if !serve.AllowedOrigin(r, s.opts.AllowedOrigins) || !serve.AllowedHost(r, s.opts.AllowedHosts) {
			return authDecision{reason: "origin"}
		}
		sessSt, csrf, binding := s.sessionState(c.Value)
		if sessSt != serve.CredentialValid {
			return authDecision{reason: credentialReason(sessSt)}
		}
		if serve.IsStateChangingMethod(r.Method) && !serve.CSRFTokenValid(r, csrf) {
			return authDecision{reason: "csrf"}
		}
		return authDecision{ok: true, binding: binding}
	}

	return authDecision{reason: "unauthenticated"} // absent: no header, no cookie
}

// sessionState resolves the CredentialState of a session id minted by
// /api/session/bootstrap, returning its bound CSRF token alongside. An
// unknown id is CredentialAbsent. A known id additionally inherits the
// server's current Token revocation/expiry facts on top of its own TTL, so
// revoking or expiring Token invalidates every outstanding session on the
// very next request without walking the session map or restarting the
// daemon.
func (s *Server) sessionState(id string) (serve.CredentialState, string, lan.CredentialBinding) {
	s.mu.Lock()
	sess, ok := s.sessions[id]
	s.mu.Unlock()
	if !ok {
		return serve.CredentialAbsent, "", lan.CredentialBinding{}
	}
	if s.opts.Authority != nil {
		state := credentialStateFromLAN(s.opts.Authority.ValidateBinding(sess.binding, s.opts.now()))
		if state != serve.CredentialValid {
			return state, sess.csrf, sess.binding
		}
	} else if s.opts.CredentialRevoked {
		return serve.CredentialRevoked, sess.csrf, sess.binding
	}
	now := s.opts.now()
	if !now.Before(sess.expiresAt) {
		return serve.CredentialExpired, sess.csrf, sess.binding
	}
	if !sess.binding.ExpiresAt.IsZero() && !now.Before(sess.binding.ExpiresAt) {
		return serve.CredentialExpired, sess.csrf, sess.binding
	}
	return serve.CredentialValid, sess.csrf, sess.binding
}

type credentialBindingContextKey struct{}

// CredentialBindingFromContext returns the non-secret credential/session
// identity, revision, and effective expiry bound to an authenticated request.
func CredentialBindingFromContext(ctx context.Context) (lan.CredentialBinding, bool) {
	binding, ok := ctx.Value(credentialBindingContextKey{}).(lan.CredentialBinding)
	return binding, ok
}

// boundContext cancels an authenticated downstream request at effective
// expiry or as soon as a credential-authority update invalidates its exact
// identity/revision (revocation, removal, or rotation beyond overlap).
func (s *Server) boundContext(parent context.Context, binding lan.CredentialBinding) (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithCancel(context.WithValue(parent, credentialBindingContextKey{}, binding))
	if binding.ID == "" {
		return ctx, cancel
	}
	var changed <-chan struct{}
	if s.opts.Authority != nil {
		// Subscribe synchronously before the downstream handler can observe
		// dispatch, preventing an immediate rotation/revocation from falling
		// between successful authentication and monitor registration.
		changed = s.opts.Authority.Changes()
	}
	go func() {
		for {
			var expiry <-chan time.Time
			if !binding.ExpiresAt.IsZero() {
				d := binding.ExpiresAt.Sub(s.opts.now())
				if d <= 0 {
					cancel()
					return
				}
				expiry = s.opts.clock().After(d)
			}
			select {
			case <-ctx.Done():
				return
			case <-expiry:
				cancel()
				return
			case <-changed:
				if s.opts.Authority == nil {
					cancel()
					return
				}
				decision := s.opts.Authority.BindingDecision(binding, s.opts.now())
				if decision.State != lan.CredentialValid {
					cancel()
					return
				}
				binding.ExpiresAt = decision.Binding.ExpiresAt
				changed = s.opts.Authority.Changes()
			}
		}
	}()
	return ctx, cancel
}

// audit records a denial with the path, method, and stable reason -- never
// the presented credential, cookie, or query string (ADR-007 "never logs
// credential material").
func (s *Server) audit(r *http.Request, reason string) {
	log.Printf("gateway: auth denied path=%s method=%s reason=%s", r.URL.Path, r.Method, reason)
}

// writeAuthError writes the stable, machine-checkable denial body ADR-007
// requires: a fixed "unauthenticated" top-level category, distinguishable
// via "reason" ("expired", "revoked", "query_credential",
// "browser_https_required", "origin", "csrf"), and never the presented
// credential.
func writeAuthError(w http.ResponseWriter, reason string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusUnauthorized)
	body := map[string]string{"error": "unauthenticated"}
	if reason != "" {
		body["reason"] = reason
	}
	_ = json.NewEncoder(w).Encode(body)
}

// handleInsecureStaticOnly answers every sensitive-route path with the
// stable insecure_static_only denial, before any peer/config/credential
// resolution happens. It is the ONLY handler ever registered at those paths
// when the effective ListenerGrant's mask does not allow sensitive routes.
func (s *Server) handleInsecureStaticOnly(w http.ResponseWriter, r *http.Request) {
	s.audit(r, "insecure_static_only")
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusForbidden)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": "insecure_static_only"})
}

// handleGatewayUnavailable answers every path when this Server's
// ListenerGrant was denied (non-loopback bind without valid credentials/TLS
// and without the explicit insecure override). Fails closed: no route,
// including static assets, is served.
func (s *Server) handleGatewayUnavailable(w http.ResponseWriter, r *http.Request) {
	s.audit(r, "gateway_unavailable")
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusServiceUnavailable)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": "gateway_unavailable"})
}

// handleSessionBootstrap exchanges a valid, non-query bearer credential for
// an HttpOnly/Secure/SameSite=Strict session cookie plus a CSRF token, over a
// verified HTTPS origin only -- ADR-007's bounded one-time bootstrap flow.
// It is unavailable (browser_https_required) on any non-TLS connection, even
// on loopback, and rejects query-string credentials exactly like every other
// sensitive route.
func (s *Server) handleSessionBootstrap(w http.ResponseWriter, r *http.Request) {
	if serve.HasQueryCredential(r) {
		s.audit(r, "query_credential")
		writeAuthError(w, "query_credential")
		return
	}
	if r.TLS == nil {
		s.audit(r, "browser_https_required")
		writeAuthError(w, "browser_https_required")
		return
	}
	h := r.Header.Get("Authorization")
	if !strings.HasPrefix(h, "Bearer ") {
		s.audit(r, "unauthenticated")
		writeAuthError(w, "unauthenticated")
		return
	}
	st, binding := s.authenticateBearer(strings.TrimPrefix(h, "Bearer "))
	if st != serve.CredentialValid {
		reason := credentialReason(st)
		s.audit(r, reason)
		writeAuthError(w, reason)
		return
	}
	if !serve.AllowedOrigin(r, s.opts.AllowedOrigins) || !serve.AllowedHost(r, s.opts.AllowedHosts) {
		s.audit(r, "origin")
		writeAuthError(w, "origin")
		return
	}

	id, err := randomSessionValue()
	if err != nil {
		s.audit(r, "session_unavailable")
		http.Error(w, `{"error":"session_unavailable"}`, http.StatusServiceUnavailable)
		return
	}
	csrf, err := randomSessionValue()
	if err != nil {
		s.audit(r, "session_unavailable")
		http.Error(w, `{"error":"session_unavailable"}`, http.StatusServiceUnavailable)
		return
	}
	expires := s.opts.now().Add(sessionTTL)
	if !binding.ExpiresAt.IsZero() && binding.ExpiresAt.Before(expires) {
		expires = binding.ExpiresAt
	}
	binding.ExpiresAt = expires

	s.mu.Lock()
	if s.sessions == nil {
		s.sessions = make(map[string]gatewaySession)
	}
	s.sessions[id] = gatewaySession{csrf: csrf, binding: binding, expiresAt: expires}
	s.mu.Unlock()

	http.SetCookie(w, serve.NewSessionCookie(serve.SessionCookieName, id, "/", expires))
	writeJSON(w, map[string]any{"csrf": csrf, "expires": expires.Unix()})
}

// randomSessionValue returns a high-entropy, hex-encoded opaque value
// suitable for a session id or CSRF token. It is never derived from Token,
// so a leaked session/CSRF value cannot be used to reconstruct the
// credential.
func randomSessionValue() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}

// peerDTO is the JSON shape the PWA consumes (see CONTRACT.md /api/peers).
type peerDTO struct {
	Handle      string `json:"handle"`
	Name        string `json:"name"`
	Type        string `json:"type"`
	Status      string `json:"status"`
	Model       string `json:"model"`
	Workspace   string `json:"workspace"`
	ServeURL    string `json:"serveURL"`
	EndpointURL string `json:"endpointURL"`
	PID         int    `json:"pid"`
}

// toDTO converts a2a.PeerPresence into the /api/peers wire shape. Per
// CONTRACT.md "LAN provenance": "Gateway DTO conversion also clears both URL
// fields for remote peers, covering legacy registry records. No raw remote
// URL may appear in /api/peers." This is defense in depth on top of the LAN
// registry's own ingestion-time clearing (internal/a2a/lan_registry.go): a
// legacy on-disk registry record written before that clearing existed, or
// any other path that persists a PeerPresence with Type == PeerTypeRemote
// and a populated ServeURL/EndpointURL, still cannot leak a raw remote peer
// URL through this handler -- the DTO conversion clears both fields again,
// unconditionally, for every remote-typed record regardless of how it was
// produced or what it currently contains.
func toDTO(p a2a.PeerPresence) peerDTO {
	serveURL, endpointURL := p.ServeURL, p.EndpointURL
	if p.Type == a2a.PeerTypeRemote {
		serveURL, endpointURL = "", ""
	}
	return peerDTO{
		Handle:      p.Handle,
		Name:        p.Name,
		Type:        string(p.Type),
		Status:      p.Status,
		Model:       p.Model,
		Workspace:   p.Workspace,
		ServeURL:    serveURL,
		EndpointURL: endpointURL,
		PID:         p.PID,
	}
}

// Fixed, hand-written (never themselves passed through the bounded encoder)
// JSON bodies /api/peers falls back to when the complete peers response
// cannot be bounded-encoded. Per CONTRACT.md "Shared bounded-encoding API":
// "oversize and unsupported data use fixed, bounded JSON errors and never
// emit a partial peer list." Each is a small, constant literal, so it always
// encodes/fits trivially and is unaffected by the very failure it reports.
const (
	peersResponseTooLargeBody      = `{"error":"response_too_large"}`
	peersResponseEncodingErrorBody = `{"error":"response_encoding_error"}`
)

// writePeersBoundedError writes one of the two fixed fallback bodies above
// with a stable 500 status. It is only ever called after EncodeBoundedJSON
// has already failed for a non-cancellation reason, and never partially --
// no header or byte for the real (oversize/unsupported) response is ever
// written first.
func writePeersBoundedError(w http.ResponseWriter, body string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusInternalServerError)
	_, _ = w.Write([]byte(body))
}

// handlePeers answers the full peer inventory. The entire response --
// including every peer DTO -- is bounded-encoded in full via
// serve.EncodeBoundedJSON BEFORE any header or byte of this response is
// written (CONTRACT.md "/api/peers must encode completely under the same
// structural 4 MiB bound before any response write"): unlike writeJSON's
// unbounded streaming json.Encoder, nothing here can ever produce a
// truncated/partial peer list on the wire, because the write only happens
// once the complete encode has already deterministically succeeded or
// failed.
//
//   - Success: the encoded bytes are written verbatim, once, after headers.
//   - Oversize (errors.Is ... serve.ErrResponseTooLarge): a fixed, small,
//     always-encodable bounded JSON error body replaces the peer list --
//     never a partial one.
//   - Context cancellation (errors.Is ... context.Canceled /
//     context.DeadlineExceeded, returned verbatim by EncodeBoundedJSON):
//     nothing is written at all, not even a fallback body -- a canceled
//     request must never race a partial/invalid write against whatever
//     unblocked the cancellation.
//   - Any other encoding failure (unsupported/cyclic value anywhere in the
//     peer list): the fixed response_encoding_error bounded JSON body.
func (s *Server) handlePeers(w http.ResponseWriter, r *http.Request) {
	peers, err := a2a.ListPeers(s.opts.Swarm)
	if err != nil {
		http.Error(w, `{"error":"`+err.Error()+`"}`, http.StatusInternalServerError)
		return
	}
	dtos := make([]peerDTO, 0, len(peers))
	for _, p := range peers {
		dtos = append(dtos, toDTO(p))
	}
	selected := ""
	if s.opts.Selected != nil {
		selected = s.opts.Selected()
	}

	payload, encErr := serve.EncodeBoundedJSON(r.Context(), map[string]any{"peers": dtos, "selected": selected})
	if encErr != nil {
		if errors.Is(encErr, context.Canceled) || errors.Is(encErr, context.DeadlineExceeded) {
			return // cancellation: write nothing at all.
		}
		if errors.Is(encErr, serve.ErrResponseTooLarge) {
			writePeersBoundedError(w, peersResponseTooLargeBody)
			return
		}
		writePeersBoundedError(w, peersResponseEncodingErrorBody)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(payload)
}

func (s *Server) handleSelect(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, `{"error":"POST only"}`, http.StatusMethodNotAllowed)
		return
	}
	var body struct {
		Handle string `json:"handle"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, `{"error":"bad json"}`, http.StatusBadRequest)
		return
	}
	handle := strings.TrimSpace(body.Handle)
	if s.opts.Select != nil {
		s.opts.Select(handle)
	}
	writeJSON(w, map[string]any{"selected": handle})
}

func (s *Server) handleVersion(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, map[string]any{
		"version":       assetVersion(),
		"beaconVersion": beaconVersion,
	})
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

// virtualIfacePrefixes are interface name prefixes that are NOT real LAN
// interfaces a phone or Swarm Desktop on the WiFi can route to: Docker/Podman
// bridges, virtual ethernet pairs, libvirt bridges, and VPN/overlay tunnels.
var virtualIfacePrefixes = []string{
	"docker", "br-", "veth", "virbr", "cni", "flannel", "cali",
	"tailscale", "wg", "tun", "tap", "zt", "utun",
}

func isVirtualIface(name string) bool {
	n := strings.ToLower(name)
	for _, p := range virtualIfacePrefixes {
		if strings.HasPrefix(n, p) {
			return true
		}
	}
	return false
}

// isCGNAT reports whether ip is in the 100.64.0.0/10 carrier-grade NAT range
// used by Tailscale and similar overlays — not reachable by a plain LAN client.
func isCGNAT(ip net.IP) bool {
	ip4 := ip.To4()
	return ip4 != nil && ip4[0] == 100 && ip4[1] >= 64 && ip4[1] <= 127
}

// LocalIPv4s returns this host's non-loopback IPv4 addresses ranked so the most
// likely LAN-reachable address comes FIRST. The banner's QR code and the
// discovery beacon both advertise index 0, so ordering directly determines
// whether phones and Swarm Desktop on the same WiFi can connect.
func LocalIPv4s() []string {
	type ranked struct {
		ip   string
		rank int
	}
	var found []ranked
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil
	}
	for _, iface := range ifaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		virtual := isVirtualIface(iface.Name)
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, a := range addrs {
			var ip net.IP
			switch v := a.(type) {
			case *net.IPNet:
				ip = v.IP
			case *net.IPAddr:
				ip = v.IP
			}
			if ip == nil || ip.IsLoopback() {
				continue
			}
			ip4 := ip.To4()
			if ip4 == nil {
				continue
			}
			rank := 0
			switch {
			case virtual || isCGNAT(ip4):
				rank = 100 // container bridges / VPN tunnels — last resort
			case ip4.IsPrivate():
				rank = 0 // real LAN address on a physical interface — best
			default:
				rank = 10 // public or link-local on a physical interface
			}
			found = append(found, ranked{ip: ip4.String(), rank: rank})
		}
	}
	sort.SliceStable(found, func(i, j int) bool { return found[i].rank < found[j].rank })
	out := make([]string, 0, len(found))
	for _, r := range found {
		out = append(out, r.ip)
	}
	return out
}

// PrintBanner prints the reachable LAN URLs and a scannable QR of the primary
// one. token is used ONLY to decide whether to print a "credential required"
// note; per ADR-007 "Credential lifecycle and storage" a gateway credential
// must never appear in a QR URL, printed URL, or any other log/output, so
// the token value itself is never interpolated into the printed text.
func PrintBanner(title, addr, token string) {
	_, port, err := net.SplitHostPort(strings.TrimPrefix(addr, "http://"))
	if err != nil || port == "" {
		port = strings.TrimPrefix(addr, ":")
	}
	ips := LocalIPv4s()

	fmt.Println()
	fmt.Printf("  %s\n", title)
	fmt.Println("  ────────────────────────────────────────────")
	fmt.Printf("  listening on %s\n", addr)
	if token != "" {
		fmt.Println("  credential required: send it as \"Authorization: Bearer <token>\"")
		fmt.Println("  (never as a URL query parameter — query credentials are always rejected)")
	}
	if len(ips) == 0 {
		fmt.Println("  (no non-loopback IPv4 found — are you on a network?)")
	}
	var primary string
	for i, ip := range ips {
		url := fmt.Sprintf("http://%s:%s", ip, port)
		if i == 0 {
			primary = url
		}
		fmt.Printf("    %s\n", url)
	}
	fmt.Println()
	if primary != "" {
		fmt.Println("  scan this on your phone (Chrome → open URL → ⋮ → Install app):")
		fmt.Println()
		PrintQR(primary)
		fmt.Println()
	}
}

// PortOf extracts the port from a listen address like ":8787" or "0.0.0.0:8787".
func PortOf(addr string) string {
	_, port, err := net.SplitHostPort(strings.TrimPrefix(addr, "http://"))
	if err != nil || port == "" {
		port = strings.TrimPrefix(addr, ":")
	}
	return port
}
