package gateway

// Peer-aware data-route resolution for the embedded (in-daemon) gateway.
//
// The daemon serves its own /rpc /sse /ws straight off its serve.Mux, but it
// is not the only peer on the box: the Swarm Desktop engine (swarm-ic backend)
// registers in the A2A swarm too and speaks the desktop/Android app's
// slash-style protocol on ITS /ws. The phone only ever dials the gateway port
// (:8787), so the gateway must be able to hand a connection to another peer:
//
//   - an explicit ?peer=<handle> always wins,
//   - the /api/select'ed peer is honored when it isn't the daemon itself,
//   - /ws with no selection prefers the Swarm Desktop engine peer when one is
//     alive — that is what the Android/desktop app expects on the other end —
//     and falls back to the daemon's own dot-protocol /ws otherwise.
//
// /rpc and /sse keep defaulting to the daemon itself (the PWA's protocol).
//
// Phase 01 (ADR-007 "Gateway authentication and outbound policy", see
// docs/architecture/swarm-attach/adr-007-gateway-security.md) disabled raw
// presence-URL transport: "every production peer HTTP/SSE/WebSocket route
// returns... authenticated_endpoint_unavailable... before URL parsing, DNS,
// or dialing and opens no outbound connection. Raw PeerPresence URLs never
// become transport." resolveDataPeer below still makes the pure, non-network
// routing decision the mobile PWA and desktop app's ?peer= contract and the
// "peer offline" 502 signal depend on (peerRoute in server.go dispatches on
// its result).
//
// Phase 02 introduces lan.AuthenticatedEndpoint and
// lan.GatewayPolicy.Transport (see internal/lan/endpoint.go and
// internal/lan/gateway_policy.go) and adds an ALLOW path here alongside the
// existing deny path: proxyTo now dispatches through
// Options.GatewayPolicy/Options.EndpointForRequest when -- and only when --
// both are configured AND EndpointForRequest actually constructs a genuine
// lan.AuthenticatedEndpoint for the request AND the request carries an
// authenticated principal. Every currently-denied case (no policy
// configured, no EndpointForRequest configured, EndpointForRequest declines,
// no authenticated principal in the request context, or
// lan.GatewayPolicy.Transport itself denies) keeps returning the exact same
// stable authenticated_endpoint_unavailable result Phase 01 returned, via
// lan.WriteUnavailable, before base is ever parsed, resolved, or dialed by
// this package. No current production Options wiring sets GatewayPolicy or
// EndpointForRequest, so peerRoute's existing calls are unaffected; this
// capability is exercised today only by proxy_test.go's explicit allow-path
// test.

import (
	"io"
	"net/http"
	"strings"

	"github.com/Swarm-Code/mono/swarm-sdk/a2a"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/lan"
)

// ServeBase returns a peer's dialable serve base URL: prefer ServeURL, else
// derive from EndpointURL by trimming a trailing "/rpc" (the A2A endpoint is
// advertised as ".../rpc"). Returns "" when the peer exposes no serve
// interface.
//
// This is a pure string derivation over presence data, used only to decide
// WHETHER a request targets another peer (see resolveDataPeer). It is NOT
// itself a transport decision: ADR-007 forbids raw PeerPresence URLs from
// becoming transport, and proxyTo below never parses, resolves, or dials the
// string this returns.
func ServeBase(p a2a.PeerPresence) string {
	if p.ServeURL != "" {
		return strings.TrimRight(p.ServeURL, "/")
	}
	if p.EndpointURL != "" {
		return strings.TrimSuffix(strings.TrimRight(p.EndpointURL, "/"), "/rpc")
	}
	return ""
}

// isDesktopEnginePeer reports whether p is a Swarm Desktop engine (swarm-ic
// backend) presence — the peer that speaks the desktop/Android app's protocol.
func isDesktopEnginePeer(p a2a.PeerPresence) bool {
	return strings.HasPrefix(p.Handle, "swarm-desktop-")
}

// resolveDataPeer picks the proxy target base URL for a data request. It
// returns ("", false) when the request should be served by the gateway's own
// injected handler (no peer specified, or the peer IS self), or ("", true) when
// an EXPLICITLY-requested ?peer= is offline / not serve-capable — the caller
// then returns an error rather than silently serving the gateway's own client
// (which would redirect the user to a DIFFERENT agent without them knowing).
//
// A non-empty return value means "this request targets another peer": per
// ADR-007 Phase 01, the caller (peerRoute in server.go) still routes that
// outcome through proxyTo, but proxyTo itself now denies with
// lan.ErrAuthenticatedEndpointUnavailable instead of dialing the returned
// base.
func (s *Server) resolveDataPeer(r *http.Request, preferDesktop bool) (string, bool) {
	peers, _ := a2a.ListPeers(s.opts.Swarm)
	byHandle := make(map[string]a2a.PeerPresence, len(peers))
	for _, p := range peers {
		byHandle[p.Handle] = p
	}

	// 1. explicit ?peer= — the mobile PWA sends this on every request.
	if h := r.URL.Query().Get("peer"); h != "" {
		if h == s.opts.SelfHandle {
			return "", false // the user IS attached to the gateway itself
		}
		if p, ok := byHandle[h]; ok {
			if b := ServeBase(p); b != "" {
				return b, false
			}
		}
		return "", true // requested peer is offline / has no serve interface
	}
	if len(peers) == 0 {
		return "", false
	}
	// 2. selected peer (when it isn't the daemon itself)
	if s.opts.Selected != nil {
		if sel := s.opts.Selected(); sel != "" && sel != s.opts.SelfHandle {
			if p, ok := byHandle[sel]; ok {
				if b := ServeBase(p); b != "" {
					return b, false
				}
			}
		}
	}
	// 3. /ws only: prefer the Swarm Desktop engine peer.
	if preferDesktop {
		for _, p := range peers {
			if isDesktopEnginePeer(p) && p.Handle != s.opts.SelfHandle {
				if b := ServeBase(p); b != "" {
					return b, false
				}
			}
		}
	}
	return "", false
}

// proxyTo is the ONLY function in this file that reaches the network. Phase
// 01 (ADR-007 "Compatibility") required every production peer HTTP/SSE/
// WebSocket route to return the stable authenticated_endpoint_unavailable
// result "before URL parsing, DNS, or dialing" and to open "no outbound
// connection" -- base itself is still NEVER parsed, resolved, or dialed by
// this function; it exists only as a legacy routing hint some callers still
// pass.
//
// Phase 02 adds the allow path described in this file's package doc comment:
// when s.opts.GatewayPolicy and s.opts.EndpointForRequest are both
// configured, EndpointForRequest constructs a genuine
// lan.AuthenticatedEndpoint for r, and r carries an authenticated principal
// (see principalFromRequest), proxyTo dispatches through
// lan.GatewayPolicy.Transport instead of denying. Any failure at any of
// those steps -- including Transport's own denial -- falls through to the
// exact same lan.WriteUnavailable call Phase 01 always made.
func (s *Server) proxyTo(w http.ResponseWriter, r *http.Request, base string) {
	_ = base // never parsed, resolved, or dialed -- see the doc comment above.

	policy := s.opts.GatewayPolicy
	endpointForRequest := s.opts.EndpointForRequest
	if policy == nil || endpointForRequest == nil {
		lan.WriteUnavailable(w)
		return
	}
	endpoint, ok := endpointForRequest(r)
	if !ok {
		lan.WriteUnavailable(w)
		return
	}
	principal, ok := principalFromRequest(r)
	if !ok {
		lan.WriteUnavailable(w)
		return
	}
	transport, err := policy.Transport(r.Context(), principal, endpoint)
	if err != nil || transport == nil {
		lan.WriteUnavailable(w)
		return
	}
	dispatchPeerTransport(w, r, transport)
}

// principalFromRequest derives a lan.Principal strictly from the non-secret
// lan.CredentialBinding Server.auth already bound to r's context -- it never
// reads, forwards, or reuses the inbound Authorization header or session
// cookie value itself (ADR-007: "PeerTransport... never receives or reuses
// the inbound gateway credential/session"). An unauthenticated request (no
// binding, or an empty binding ID) reports ok=false.
func principalFromRequest(r *http.Request) (lan.Principal, bool) {
	binding, ok := CredentialBindingFromContext(r.Context())
	if !ok || binding.ID == "" {
		return lan.Principal{}, false
	}
	return lan.Principal{
		ID:           binding.ID,
		CredentialID: binding.ID,
		ExpiresAt:    binding.ExpiresAt,
	}, true
}

// isWebSocketUpgradeRequest reports whether r is a WebSocket upgrade
// handshake (checked the same way both proxy.go and its tests already
// recognize one: Upgrade: websocket plus a Connection header naming
// "upgrade").
func isWebSocketUpgradeRequest(r *http.Request) bool {
	return strings.EqualFold(r.Header.Get("Upgrade"), "websocket") &&
		strings.Contains(strings.ToLower(r.Header.Get("Connection")), "upgrade")
}

// dispatchPeerTransport forwards r through transport, rebuilding a fresh
// outbound request/handshake from the ADR-007 allowlists
// (lan.BuildOutboundHeaders / lan.BuildOutboundQuery) -- it never copies the
// inbound Authorization, Proxy-Authorization, Cookie, Set-Cookie, CSRF,
// Host, forwarding, or hop-by-hop headers, and the outbound query always
// starts empty. An ordinary request's response is copied back to w
// verbatim; a WebSocket upgrade request is tunneled by hijacking w's
// underlying connection and proxying bytes bidirectionally with transport's
// dialed peer connection.
func dispatchPeerTransport(w http.ResponseWriter, r *http.Request, transport lan.PeerTransport) {
	req := lan.PeerRequest{
		Method: r.Method,
		Path:   r.URL.Path,
		Header: lan.BuildOutboundHeaders(r.Header),
		Query:  lan.BuildOutboundQuery(),
		Body:   r.Body,
	}

	if isWebSocketUpgradeRequest(r) {
		tunnelWebSocket(w, r, transport, req)
		return
	}

	resp, err := transport.RoundTrip(r.Context(), req)
	if err != nil {
		lan.WriteUnavailable(w)
		return
	}
	defer resp.Body.Close()
	for k, vs := range resp.Header {
		for _, v := range vs {
			w.Header().Add(k, v)
		}
	}
	w.WriteHeader(resp.StatusCode)
	_, _ = io.Copy(w, resp.Body)
}

// tunnelWebSocket dials transport's peer connection, hijacks r's underlying
// client connection, replays the allowlisted upgrade request onto the peer
// connection, and then proxies bytes bidirectionally until either side
// closes. It denies (without hijacking) when w does not support hijacking,
// or when the peer dial itself fails.
func tunnelWebSocket(w http.ResponseWriter, r *http.Request, transport lan.PeerTransport, req lan.PeerRequest) {
	hijacker, ok := w.(http.Hijacker)
	if !ok {
		lan.WriteUnavailable(w)
		return
	}
	peerConn, err := transport.DialWebSocket(r.Context(), req)
	if err != nil {
		lan.WriteUnavailable(w)
		return
	}
	clientConn, buf, err := hijacker.Hijack()
	if err != nil {
		_ = peerConn.Close()
		return
	}
	if upgradeReq, uerr := http.NewRequest(r.Method, req.Path, nil); uerr == nil {
		upgradeReq.Header = req.Header.Clone()
		upgradeReq.Header.Set("Connection", "Upgrade")
		upgradeReq.Header.Set("Upgrade", "websocket")
		_ = upgradeReq.Write(peerConn)
	}
	if buf != nil {
		if n := buf.Reader.Buffered(); n > 0 {
			_, _ = io.CopyN(peerConn, buf.Reader, int64(n))
		}
	}
	done := make(chan struct{})
	go func() {
		_, _ = io.Copy(peerConn, clientConn)
		_ = peerConn.Close()
		close(done)
	}()
	_, _ = io.Copy(clientConn, peerConn)
	_ = clientConn.Close()
	<-done
}
