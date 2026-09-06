// Phase 01 (ADR-007 "Gateway authentication and outbound policy", see
// docs/architecture/swarm-attach/adr-007-gateway-security.md) disabled raw
// presence-URL transport for the standalone gateway command exactly as it
// did for the embedded (in-daemon) gateway (see gateway/proxy.go).
// handleProxy returned the stable authenticated_endpoint_unavailable result
// before consulting peer inventory, selected-peer state, request URL/query
// state, or raw ServeURL/EndpointURL values.
//
// Phase 02 introduces lan.AuthenticatedEndpoint and
// lan.GatewayPolicy.Transport (see internal/lan/endpoint.go and
// internal/lan/gateway_policy.go) and adds an ALLOW path here alongside the
// existing deny path. handleProxy dispatches through a configured
// lan.GatewayPolicy + endpoint-resolver hook only when both are installed
// (via configureGatewayPolicyForTesting) AND the resolver hook actually
// constructs a genuine lan.AuthenticatedEndpoint for the request AND the
// request carries an authenticated principal. main.go's production wiring
// does not call configureGatewayPolicyForTesting in this round -- composing
// real LAN advertisement/presence provenance into this standalone command is
// a deferred integration seam per CONTRACT.md ("Any remaining gateway route
// dispatch not explicitly listed in a worker's # FILES: header") -- so every
// currently-denied production case (no policy installed, no resolver hook
// installed, no constructible endpoint, no authenticated principal, or
// lan.GatewayPolicy.Transport itself denying) keeps returning the exact same
// stable authenticated_endpoint_unavailable result Phase 01 always returned,
// via lan.WriteUnavailable, before any request, peer, presence, registry, or
// endpoint state is inspected. proxy_test.go's new allow-path test installs
// the hooks directly to prove the capability end-to-end.
package main

import (
	"io"
	"net/http"
	"strings"
	"sync"

	"github.com/Swarm-Code/mono/swarm-sdk/gateway"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/lan"
)

// gatewayPolicyMu guards the package-level Phase 02 policy hooks below. They
// are package-level (rather than fields on state) because state is declared
// in main.go, which this worker's # FILES: header does not include; the
// hooks are unset (nil) in every real invocation of main() in this round.
var (
	gatewayPolicyMu           sync.RWMutex
	gatewayPolicyImpl         *lan.GatewayPolicy
	gatewayEndpointForRequest func(*http.Request) (lan.AuthenticatedEndpoint, bool)
)

// configureGatewayPolicyForTesting installs (or, when both arguments are
// nil, clears) the Phase 02 policy-bound peer transport handleProxy
// dispatches through. It returns a restore func a test defers to reset
// state. Production main() does not call this in this round -- see the
// package doc comment above.
func configureGatewayPolicyForTesting(policy *lan.GatewayPolicy, endpointForRequest func(*http.Request) (lan.AuthenticatedEndpoint, bool)) func() {
	gatewayPolicyMu.Lock()
	prevPolicy, prevFn := gatewayPolicyImpl, gatewayEndpointForRequest
	gatewayPolicyImpl, gatewayEndpointForRequest = policy, endpointForRequest
	gatewayPolicyMu.Unlock()
	return func() {
		gatewayPolicyMu.Lock()
		gatewayPolicyImpl, gatewayEndpointForRequest = prevPolicy, prevFn
		gatewayPolicyMu.Unlock()
	}
}

// handleProxy is the ONLY function in this file that reaches the network.
// See the package doc comment above for the exact deny/allow ordering.
func (gw *state) handleProxy(w http.ResponseWriter, r *http.Request) {
	gatewayPolicyMu.RLock()
	policy, endpointForRequest := gatewayPolicyImpl, gatewayEndpointForRequest
	gatewayPolicyMu.RUnlock()

	if policy == nil || endpointForRequest == nil || r == nil || r.URL == nil {
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
// lan.CredentialBinding gateway.Server.auth already bound to r's context --
// see gateway.CredentialBindingFromContext. It never reads, forwards, or
// reuses the inbound Authorization header or session cookie value itself.
func principalFromRequest(r *http.Request) (lan.Principal, bool) {
	binding, ok := gateway.CredentialBindingFromContext(r.Context())
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
// handshake.
func isWebSocketUpgradeRequest(r *http.Request) bool {
	return strings.EqualFold(r.Header.Get("Upgrade"), "websocket") &&
		strings.Contains(strings.ToLower(r.Header.Get("Connection")), "upgrade")
}

// dispatchPeerTransport forwards r through transport, rebuilding a fresh
// outbound request/handshake from the ADR-007 allowlists
// (lan.BuildOutboundHeaders / lan.BuildOutboundQuery) -- it never copies the
// inbound Authorization, Proxy-Authorization, Cookie, Set-Cookie, CSRF,
// Host, forwarding, or hop-by-hop headers, and the outbound query always
// starts empty.
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
// closes.
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
