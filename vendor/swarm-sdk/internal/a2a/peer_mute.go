// Package a2a — peer_mute.go.
//
// Phase 3 of the steering-agent-with-tools redesign
// (docs/steering-redesign/steering-redesign.pdf).
//
// PeerMuteStore tracks which peers are currently muted and when each mute
// expires. The A2A Runtime consults this store at the inbound DM entry
// point: muted peers' messages are dropped (returned as a completed task
// so the sender does not see a protocol error).
//
// The streaming steering driver pushes mute entries here via the agent
// package's PeerMuter interface — clean dependency direction (a2a does NOT
// import agent; agent defines the narrow interface).
//
// TTL clamp: [1s, 10m]. Values outside that range are clamped silently to
// keep mutes bounded.
package a2a

import (
	"sync"
	"time"
)

// PeerMuteStore tracks peer mutes by handle.
type PeerMuteStore struct {
	mu    sync.Mutex
	mutes map[string]*peerMute
}

// peerMute is a single mute entry.
type peerMute struct {
	reason    string
	expiresAt time.Time
}

const (
	peerMuteMinTTL = 1 * time.Second
	peerMuteMaxTTL = 10 * time.Minute
)

// NewPeerMuteStore constructs an empty mute store.
func NewPeerMuteStore() *PeerMuteStore {
	return &PeerMuteStore{
		mutes: make(map[string]*peerMute),
	}
}

// MutePeer installs or extends a mute for peer with the given reason and
// TTL. TTL is clamped to [1s, 10m]. Calling MutePeer on an already-muted
// peer overwrites the prior entry (last-write-wins, simpler than max-of).
func (s *PeerMuteStore) MutePeer(peer, reason string, ttl time.Duration) {
	if peer == "" {
		return
	}
	if ttl < peerMuteMinTTL {
		ttl = peerMuteMinTTL
	}
	if ttl > peerMuteMaxTTL {
		ttl = peerMuteMaxTTL
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.mutes[peer] = &peerMute{
		reason:    reason,
		expiresAt: time.Now().Add(ttl),
	}
}

// IsPeerMuted reports whether peer is currently muted. As a side effect,
// expired entries are evicted lazily (so the map stays bounded under
// churn).
func (s *PeerMuteStore) IsPeerMuted(peer string) bool {
	if peer == "" {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	m, ok := s.mutes[peer]
	if !ok {
		return false
	}
	if time.Now().After(m.expiresAt) {
		delete(s.mutes, peer)
		return false
	}
	return true
}

// UnmutePeer removes a mute early. No-op if not muted.
func (s *PeerMuteStore) UnmutePeer(peer string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.mutes, peer)
}

// MuteReason returns the recorded reason for a peer's mute, or "" if not
// muted (or expired).
func (s *PeerMuteStore) MuteReason(peer string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	m, ok := s.mutes[peer]
	if !ok {
		return ""
	}
	if time.Now().After(m.expiresAt) {
		delete(s.mutes, peer)
		return ""
	}
	return m.reason
}
