package a2a

import (
	"sort"
	"strings"
	"time"
)

// CanonicalPeers collapses duplicate live sessions that share the same handle.
// When multiple peers advertise the same handle, the most recently registered
// session wins so host UX can route mentions like @agent:peer:beta to the
// newest launched session deterministically.
func CanonicalPeers(peers []PeerIdentity) []PeerIdentity {
	if len(peers) <= 1 {
		return clonePeers(peers)
	}

	bestByKey := make(map[string]PeerIdentity, len(peers))
	for _, peer := range peers {
		key := NormalizeHandle(peer.Handle)
		if key == "" {
			key = strings.TrimSpace(peer.SessionID)
		}
		if key == "" {
			key = strings.TrimSpace(peer.EndpointURL)
		}
		if key == "" {
			continue
		}
		current, ok := bestByKey[key]
		if !ok || preferPeer(peer, current) {
			bestByKey[key] = peer
		}
	}

	canonical := make([]PeerIdentity, 0, len(bestByKey))
	for _, peer := range bestByKey {
		canonical = append(canonical, peer)
	}
	sort.SliceStable(canonical, func(i, j int) bool {
		left := NormalizeHandle(canonical[i].Handle)
		right := NormalizeHandle(canonical[j].Handle)
		if left == right {
			return canonical[i].SessionID < canonical[j].SessionID
		}
		return left < right
	})
	return canonical
}

func clonePeers(peers []PeerIdentity) []PeerIdentity {
	if len(peers) == 0 {
		return nil
	}
	cloned := make([]PeerIdentity, len(peers))
	copy(cloned, peers)
	return cloned
}

func preferPeer(candidate, current PeerIdentity) bool {
	if laterTime(candidate.RegisteredAt, current.RegisteredAt) {
		return true
	}
	if laterTime(current.RegisteredAt, candidate.RegisteredAt) {
		return false
	}
	if laterTime(candidate.LastSeenAt, current.LastSeenAt) {
		return true
	}
	if laterTime(current.LastSeenAt, candidate.LastSeenAt) {
		return false
	}
	return candidate.SessionID > current.SessionID
}

func laterTime(left, right time.Time) bool {
	if left.IsZero() {
		return false
	}
	if right.IsZero() {
		return true
	}
	return left.After(right)
}
