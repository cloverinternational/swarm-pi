// Package a2a — poller_test.go
//
// Probe tests for PeerDiscoveryPoller.  Each test uses a unique swarm name so
// it writes under ~/.swarm/swarms/test-probe-<nanos>/ and never collides with
// a live session.  PeerTypeRemote is used for most synthetic peers so the
// liveness PID check is skipped. Since remotePresenceProjection now only ever
// preserves a strict opaque origin~alias handle (no bare-handle fallback —
// see remote_presence_security_test.go), every remote fixture here uses
// opaqueHandle() instead of a bare literal like the old "agent-alpha".
package a2a_test

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/a2a"
)

// ─── helpers ──────────────────────────────────────────────────────────────────

func testSwarmName() string {
	return fmt.Sprintf("test-probe-%d", time.Now().UnixNano())
}

// opaqueHandle deterministically derives a valid process-generated-shaped
// remote handle (fixed-length lowercase hex origin~alias) from a short,
// readable seed, so poller tests can keep using distinct human-readable
// names for correlation without violating the strict opaque-handle contract
// remotePresenceProjection now enforces for every PeerTypeRemote record.
func opaqueHandle(seed string) string {
	sum := sha256.Sum256([]byte(seed))
	origin := hex.EncodeToString(sum[:16])
	alias := hex.EncodeToString(sum[16:32])
	return origin + "~" + alias
}

// writeRemotePeer writes a presence JSON file directly into the swarm peers dir.
// PeerTypeRemote is defaulted (when p.Type is unset) so no PID liveness check
// is performed. LastSeenAt is set to now because real remote peers are
// refreshed by the LAN gossip and ListPeers TTL-prunes remote peers whose
// LastSeenAt is stale (RemotePeerTTL). A caller that needs the legacy local
// PID-liveness path (e.g. the permission-migration test below) may set p.Type
// explicitly before calling; only an unset Type is defaulted to Remote.
func writeRemotePeer(t *testing.T, swarmName string, p a2a.PeerPresence) {
	t.Helper()
	dir := filepath.Join(a2a.SwarmPath(swarmName), "peers")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatalf("mkdir peers dir: %v", err)
	}
	if p.Type == "" {
		p.Type = a2a.PeerTypeRemote
	}
	if p.LastSeenAt.IsZero() {
		p.LastSeenAt = time.Now().UTC()
	}
	data, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		t.Fatalf("marshal peer: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, p.Handle+".json"), data, 0644); err != nil {
		t.Fatalf("write peer file: %v", err)
	}
}

func removePeer(t *testing.T, swarmName, handle string) {
	t.Helper()
	_ = os.Remove(filepath.Join(a2a.SwarmPath(swarmName), "peers", handle+".json"))
}

// ─── tests ────────────────────────────────────────────────────────────────────

// TestPeerDiscoveryPoller_JoinLeft verifies the full join→status→leave lifecycle.
//
// NOTE: NewPeerDiscoveryPoller clamps the interval to a minimum of 500 ms.
// Each "phase" therefore waits ≥700 ms to guarantee at least one full tick
// fires after the filesystem change.  The initial peer is written *before*
// Start() so the very first synchronous tick (which runs before the timer)
// captures the join immediately.
func TestPeerDiscoveryPoller_JoinLeft(t *testing.T) {
	swarm := testSwarmName()
	t.Cleanup(func() { _ = os.RemoveAll(a2a.SwarmPath(swarm)) })

	alpha := opaqueHandle("agent-alpha")
	var mu sync.Mutex
	var changes []a2a.PeerChange

	// ── Join: write peer before starting so the first tick captures it ────────
	writeRemotePeer(t, swarm, a2a.PeerPresence{Handle: alpha, Status: "idle"})

	poller := a2a.NewPeerDiscoveryPoller(swarm, 500*time.Millisecond, func(c a2a.PeerChange) {
		mu.Lock()
		changes = append(changes, c)
		mu.Unlock()
	})
	poller.Start()
	defer poller.Stop()

	// First tick fires synchronously in the goroutine before the timer starts.
	// A short sleep ensures it has run.
	time.Sleep(150 * time.Millisecond)

	mu.Lock()
	joins := countChanges(changes, a2a.PeerEventJoined, alpha)
	mu.Unlock()
	if joins != 1 {
		t.Errorf("join: expected 1 PeerEventJoined for agent-alpha, got %d", joins)
	}

	// ── Status change: modify file, wait >500 ms for the next tick ───────────
	writeRemotePeer(t, swarm, a2a.PeerPresence{
		Handle:      alpha,
		Status:      "working",
		CurrentTask: "fixing issue #7",
	})
	time.Sleep(700 * time.Millisecond)

	mu.Lock()
	statuses := countChanges(changes, a2a.PeerEventStatus, alpha)
	mu.Unlock()
	if statuses < 1 {
		t.Errorf("status: expected ≥1 PeerEventStatus for agent-alpha, got %d", statuses)
	}

	// ── Leave: remove file, wait >500 ms for the next tick ───────────────────
	removePeer(t, swarm, alpha)
	time.Sleep(700 * time.Millisecond)

	mu.Lock()
	lefts := countChanges(changes, a2a.PeerEventLeft, alpha)
	mu.Unlock()
	if lefts != 1 {
		t.Errorf("leave: expected 1 PeerEventLeft for agent-alpha, got %d", lefts)
	}
}

// TestPeerDiscoveryPoller_Snapshot verifies Snapshot returns a copy of the
// currently-known peer set after the first tick fires.
func TestPeerDiscoveryPoller_Snapshot(t *testing.T) {
	swarm := testSwarmName()
	t.Cleanup(func() { _ = os.RemoveAll(a2a.SwarmPath(swarm)) })

	for _, seed := range []string{"peer-1", "peer-2", "peer-3"} {
		writeRemotePeer(t, swarm, a2a.PeerPresence{Handle: opaqueHandle(seed), Status: "idle"})
	}

	poller := a2a.NewPeerDiscoveryPoller(swarm, 5*time.Second, func(a2a.PeerChange) {})
	poller.Start()
	defer poller.Stop()

	// The first tick fires immediately before the timer; allow a tiny moment.
	time.Sleep(100 * time.Millisecond)

	snap := poller.Snapshot()
	if len(snap) != 3 {
		t.Errorf("Snapshot: got %d peers, want 3", len(snap))
	}
}

// TestPeerDiscoveryPoller_NoDuplicateJoin verifies that a stable peer file
// that doesn't change only fires one PeerEventJoined across multiple ticks.
func TestPeerDiscoveryPoller_NoDuplicateJoin(t *testing.T) {
	swarm := testSwarmName()
	t.Cleanup(func() { _ = os.RemoveAll(a2a.SwarmPath(swarm)) })

	writeRemotePeer(t, swarm, a2a.PeerPresence{Handle: opaqueHandle("stable-bob"), Status: "idle"})

	var mu sync.Mutex
	var joinCount int

	poller := a2a.NewPeerDiscoveryPoller(swarm, 100*time.Millisecond, func(c a2a.PeerChange) {
		if c.Event == a2a.PeerEventJoined {
			mu.Lock()
			joinCount++
			mu.Unlock()
		}
	})
	poller.Start()
	defer poller.Stop()

	// Let it tick 4–5 times without any file changes.
	time.Sleep(550 * time.Millisecond)

	mu.Lock()
	got := joinCount
	mu.Unlock()
	if got != 1 {
		t.Errorf("expected exactly 1 PeerEventJoined (no duplicates), got %d", got)
	}
}

// TestPeerDiscoveryPoller_MultiplePeers verifies join/leave for >1 peer.
// All peers are written before Start() so the first synchronous tick captures
// all joins.  w2 is removed and a >500 ms wait ensures the next tick fires.
func TestPeerDiscoveryPoller_MultiplePeers(t *testing.T) {
	swarm := testSwarmName()
	t.Cleanup(func() { _ = os.RemoveAll(a2a.SwarmPath(swarm)) })

	handles := map[string]string{
		"w1": opaqueHandle("w1"),
		"w2": opaqueHandle("w2"),
		"w3": opaqueHandle("w3"),
	}

	// Pre-populate all three peers before starting the poller.
	for _, seed := range []string{"w1", "w2", "w3"} {
		writeRemotePeer(t, swarm, a2a.PeerPresence{Handle: handles[seed], Status: "idle"})
	}

	var mu sync.Mutex
	joined := map[string]int{}
	left := map[string]int{}

	poller := a2a.NewPeerDiscoveryPoller(swarm, 500*time.Millisecond, func(c a2a.PeerChange) {
		mu.Lock()
		switch c.Event {
		case a2a.PeerEventJoined:
			joined[c.Current.Handle]++
		case a2a.PeerEventLeft:
			left[c.Previous.Handle]++
		}
		mu.Unlock()
	})
	poller.Start()
	defer poller.Stop()

	// Allow first synchronous tick to complete.
	time.Sleep(150 * time.Millisecond)

	mu.Lock()
	for _, seed := range []string{"w1", "w2", "w3"} {
		if joined[handles[seed]] != 1 {
			t.Errorf("peer %s: expected 1 join after first tick, got %d", seed, joined[handles[seed]])
		}
	}
	mu.Unlock()

	// Remove w2 and wait for the timer tick (≥500 ms + margin).
	removePeer(t, swarm, handles["w2"])
	time.Sleep(700 * time.Millisecond)

	mu.Lock()
	defer mu.Unlock()

	if left[handles["w2"]] != 1 {
		t.Errorf("peer w2: expected 1 leave, got %d", left[handles["w2"]])
	}
	if left[handles["w1"]] != 0 || left[handles["w3"]] != 0 {
		t.Errorf("unexpected leave events for w1/w3: %v", left)
	}
}

// ─── helper ───────────────────────────────────────────────────────────────────

func countChanges(changes []a2a.PeerChange, evt a2a.PeerEvent, handle string) int {
	n := 0
	for _, c := range changes {
		if c.Event != evt {
			continue
		}
		switch evt {
		case a2a.PeerEventJoined:
			if c.Current.Handle == handle {
				n++
			}
		case a2a.PeerEventLeft:
			if c.Previous.Handle == handle {
				n++
			}
		case a2a.PeerEventStatus:
			if c.Current.Handle == handle {
				n++
			}
		}
	}
	return n
}

// ─── filesystem-security regression tests (exported API only) ──────────────

// TestJoinSwarm_RejectsMaliciousHandle_ExternalAPI proves the exported entry
// point used by every real caller (TUI, daemon, gateway) outside this
// package rejects a path-traversal handle rather than constructing a path
// from it, and that GetPeer does the same on the read side.
func TestJoinSwarm_RejectsMaliciousHandle_ExternalAPI(t *testing.T) {
	swarm := testSwarmName()
	t.Cleanup(func() { _ = os.RemoveAll(a2a.SwarmPath(swarm)) })

	for _, handle := range []string{"../../../etc/passwd", "a/b", "..", ""} {
		if err := a2a.JoinSwarm(swarm, a2a.PeerPresence{Handle: handle, PID: os.Getpid()}); err == nil {
			t.Errorf("JoinSwarm(handle=%q) = nil error, want rejection", handle)
		}
		if _, err := a2a.GetPeer(swarm, handle); err == nil {
			t.Errorf("GetPeer(handle=%q) = nil error, want rejection", handle)
		}
	}

	// A well-formed handle in the same swarm must still work normally.
	if err := a2a.JoinSwarm(swarm, a2a.PeerPresence{Handle: "legit-peer", PID: os.Getpid()}); err != nil {
		t.Fatalf("JoinSwarm with a valid handle should succeed: %v", err)
	}
	if p, err := a2a.GetPeer(swarm, "legit-peer"); err != nil || p == nil {
		t.Fatalf("GetPeer(legit-peer) = %v, %v; want a peer", p, err)
	}
}

// TestListPeers_MigratesLegacyDirectoryAndFilePermissions writes a peers
// directory and presence file the way OLDER code did (0755 dir, 0644 file —
// see writeRemotePeer above), then proves a single ListPeers call migrates
// both to the hardened 0700/0600 contract in place, without losing the peer.
// Uses PeerTypeLocal (with a live PID so the liveness check passes) rather
// than the file's usual Remote default: this test is about directory/file
// permission migration, orthogonal to remote-handle opaqueness, and a bare
// "legacy-peer" handle is exactly what this test wants to prove survives —
// which only a non-remote type still permits since remotePresenceProjection
// has no bare-handle fallback for PeerTypeRemote.
func TestListPeers_MigratesLegacyDirectoryAndFilePermissions(t *testing.T) {
	swarm := testSwarmName()
	t.Cleanup(func() { _ = os.RemoveAll(a2a.SwarmPath(swarm)) })

	writeRemotePeer(t, swarm, a2a.PeerPresence{
		Handle: "legacy-peer",
		Status: "idle",
		Type:   a2a.PeerTypeLocal,
		PID:    os.Getpid(),
	})

	peersDir := filepath.Join(a2a.SwarmPath(swarm), "peers")
	if fi, err := os.Stat(peersDir); err != nil || fi.Mode().Perm() != 0755 {
		t.Fatalf("precondition failed: expected legacy 0755 peers dir, got %v (err=%v)", fi, err)
	}
	peerPath := filepath.Join(peersDir, "legacy-peer.json")
	if fi, err := os.Stat(peerPath); err != nil || fi.Mode().Perm() != 0644 {
		t.Fatalf("precondition failed: expected legacy 0644 peer file, got %v (err=%v)", fi, err)
	}

	peers, err := a2a.ListPeers(swarm)
	if err != nil {
		t.Fatalf("ListPeers: %v", err)
	}
	if len(peers) != 1 || peers[0].Handle != "legacy-peer" {
		t.Fatalf("expected legacy-peer to survive migration, got %+v", peers)
	}

	if fi, err := os.Stat(peersDir); err != nil || fi.Mode().Perm() != 0700 {
		t.Errorf("peers dir not migrated to 0700: got %v (err=%v)", fi, err)
	}
	if fi, err := os.Stat(peerPath); err != nil || fi.Mode().Perm() != 0600 {
		t.Errorf("peer file not migrated to 0600: got %v (err=%v)", fi, err)
	}
}

// TestJoinSwarm_ConcurrentAtomicUpdates hammers JoinSwarm/UpdateStatus/GetPeer
// for the same handle from many goroutines simultaneously. Every GetPeer must
// observe a complete, parseable presence file — old or new, never torn or
// interleaved — proving the atomic (temp file + same-directory rename)
// publication holds under real concurrent registry traffic. Intended to run
// with -race.
func TestJoinSwarm_ConcurrentAtomicUpdates(t *testing.T) {
	swarm := testSwarmName()
	t.Cleanup(func() { _ = os.RemoveAll(a2a.SwarmPath(swarm)) })

	const handle = "hammered-peer"
	if err := a2a.JoinSwarm(swarm, a2a.PeerPresence{Handle: handle, PID: os.Getpid()}); err != nil {
		t.Fatalf("initial JoinSwarm: %v", err)
	}

	const writers = 6
	const readers = 6
	var wg sync.WaitGroup
	errs := make(chan error, writers+readers)

	for i := 0; i < writers; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			for j := 0; j < 20; j++ {
				if err := a2a.UpdateStatus(swarm, handle, fmt.Sprintf("status-%d-%d", n, j), "task"); err != nil {
					errs <- fmt.Errorf("UpdateStatus: %w", err)
					return
				}
			}
		}(i)
	}
	for i := 0; i < readers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 20; j++ {
				p, err := a2a.GetPeer(swarm, handle)
				if err != nil {
					errs <- fmt.Errorf("GetPeer: %w", err)
					return
				}
				if p == nil {
					errs <- fmt.Errorf("GetPeer returned nil for a live peer mid-update")
					return
				}
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}

	final, err := a2a.GetPeer(swarm, handle)
	if err != nil {
		t.Fatalf("final GetPeer: %v", err)
	}
	if final == nil {
		t.Fatalf("final GetPeer returned nil")
	}
}
