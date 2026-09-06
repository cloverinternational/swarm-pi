package a2a

import (
	"sync"
	"testing"
	"time"
)

func TestPeerMuteStore_MuteAndCheck(t *testing.T) {
	s := NewPeerMuteStore()

	if s.IsPeerMuted("alice") {
		t.Fatal("expected unmuted peer to report false")
	}

	s.MutePeer("alice", "loop", 60*time.Second)
	if !s.IsPeerMuted("alice") {
		t.Fatal("expected muted peer to report true")
	}
	if got := s.MuteReason("alice"); got != "loop" {
		t.Fatalf("expected reason='loop', got %q", got)
	}

	// Other peers unaffected.
	if s.IsPeerMuted("bob") {
		t.Fatal("only alice should be muted")
	}
}

func TestPeerMuteStore_Expiry(t *testing.T) {
	s := NewPeerMuteStore()
	// 1s minimum TTL (the clamp); use a sleep of 1.1s.
	// Faster test: install a mute then mutate the entry's expiresAt manually.
	s.MutePeer("alice", "loop", 60*time.Second)
	s.mu.Lock()
	s.mutes["alice"].expiresAt = time.Now().Add(-1 * time.Second)
	s.mu.Unlock()

	if s.IsPeerMuted("alice") {
		t.Fatal("expired mute should report unmuted")
	}

	// Lazy eviction: entry should be removed from the map.
	s.mu.Lock()
	_, exists := s.mutes["alice"]
	s.mu.Unlock()
	if exists {
		t.Fatal("expired mute should be evicted by IsPeerMuted")
	}
}

func TestPeerMuteStore_Unmute(t *testing.T) {
	s := NewPeerMuteStore()
	s.MutePeer("alice", "loop", 60*time.Second)
	s.UnmutePeer("alice")
	if s.IsPeerMuted("alice") {
		t.Fatal("expected unmuted after UnmutePeer")
	}

	// Unmute on a non-muted peer is a no-op.
	s.UnmutePeer("never-muted")
}

func TestPeerMuteStore_TTLClamp(t *testing.T) {
	s := NewPeerMuteStore()

	// Zero TTL → clamped to 1s.
	s.MutePeer("a", "r", 0)
	s.mu.Lock()
	expiry := s.mutes["a"].expiresAt
	s.mu.Unlock()
	delta := time.Until(expiry)
	if delta < 500*time.Millisecond || delta > 2*time.Second {
		t.Fatalf("zero TTL not clamped to ~1s, got %v", delta)
	}

	// Excessive TTL → clamped to 10m.
	s.MutePeer("b", "r", 1*time.Hour)
	s.mu.Lock()
	expiry = s.mutes["b"].expiresAt
	s.mu.Unlock()
	delta = time.Until(expiry)
	if delta > 11*time.Minute {
		t.Fatalf("excessive TTL not clamped to 10m, got %v", delta)
	}
}

func TestPeerMuteStore_EmptyPeer(t *testing.T) {
	s := NewPeerMuteStore()
	s.MutePeer("", "r", 60*time.Second)
	// Empty peer must not create an entry.
	if s.IsPeerMuted("") {
		t.Fatal("empty peer must not register a mute")
	}
}

func TestPeerMuteStore_Concurrent(t *testing.T) {
	s := NewPeerMuteStore()
	var wg sync.WaitGroup

	for i := range 32 {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			peer := "peer-" + string(rune('a'+(id%8)))
			s.MutePeer(peer, "r", 60*time.Second)
			_ = s.IsPeerMuted(peer)
			_ = s.MuteReason(peer)
		}(i)
	}

	for i := range 32 {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			peer := "peer-" + string(rune('a'+(id%8)))
			_ = s.IsPeerMuted(peer)
			if id%4 == 0 {
				s.UnmutePeer(peer)
			}
		}(i)
	}

	wg.Wait()
	// If we get here without -race firing, the mutex is doing its job.
}
