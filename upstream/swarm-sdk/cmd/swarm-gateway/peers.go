package main

// resolvePeer reads peer inventory and selected-peer/request state to retain
// the existing ?peer= selection contract for a future policy-bound transport.
// It does not itself dial a resolved peer, but it is not pure or in-memory-only:
// a2a.ListPeers may consult registry-backed presence state. Phase 01 proxy
// routes must not call it; proxy.go's handleProxy returns the stable
// authenticated_endpoint_unavailable result before any such lookup.
// serveBase remains the source-compatible raw presence-URL selector that a
// future authenticated transport may replace or constrain.

import (
	"net/http"
	"strings"

	"github.com/Swarm-Code/mono/swarm-sdk/a2a"
)

// resolvePeer picks the target peer for a proxied request. Precedence:
//  1. explicit ?peer= query param,
//  2. the selected handle,
//  3. auto-select: first serve-capable peer, preferring Type=="daemon".
//
// It returns the resolved peer's serve base URL (see serveBase) or "".
func (st *state) resolvePeer(r *http.Request) (string, string) {
	peers, err := a2a.ListPeers(st.swarm)
	if err != nil || len(peers) == 0 {
		return "", ""
	}
	byHandle := make(map[string]a2a.PeerPresence, len(peers))
	for _, p := range peers {
		byHandle[p.Handle] = p
	}

	// 1. explicit ?peer=
	if h := r.URL.Query().Get("peer"); h != "" {
		if p, ok := byHandle[h]; ok {
			return p.Handle, serveBase(p)
		}
	}
	// 2. selected
	st.mu.RLock()
	sel := st.selected
	st.mu.RUnlock()
	if sel != "" {
		if p, ok := byHandle[sel]; ok {
			if b := serveBase(p); b != "" {
				return p.Handle, b
			}
		}
	}
	// 3. auto-select, preferring daemons.
	var fallback a2a.PeerPresence
	var haveFallback bool
	for _, p := range peers {
		if serveBase(p) == "" {
			continue
		}
		if p.Type == a2a.PeerTypeDaemon {
			return p.Handle, serveBase(p)
		}
		if !haveFallback {
			fallback, haveFallback = p, true
		}
	}
	if haveFallback {
		return fallback.Handle, serveBase(fallback)
	}
	return "", ""
}

// serveBase returns a peer's dialable serve base URL: prefer ServeURL, else
// derive from EndpointURL by trimming a trailing "/rpc" (the A2A endpoint is
// advertised as ".../rpc"). Returns "" when the peer exposes no serve interface.
func serveBase(p a2a.PeerPresence) string {
	if p.ServeURL != "" {
		return strings.TrimRight(p.ServeURL, "/")
	}
	if p.EndpointURL != "" {
		return strings.TrimSuffix(strings.TrimRight(p.EndpointURL, "/"), "/rpc")
	}
	return ""
}
