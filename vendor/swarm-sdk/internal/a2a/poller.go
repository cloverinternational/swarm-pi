// Package a2a — poller.go
//
// PeerDiscoveryPoller watches the swarm's peer presence directory and emits
// typed events (joined, left, status-changed) whenever the set of live peers
// changes.  It is the bridge that feeds peer lifecycle activity into the
// unified client.Event bus without requiring any network calls — peer presence
// is read from the filesystem (~/.swarm/swarms/<name>/peers/*.json).
package a2a

import (
	"sync"
	"time"
)

// PeerEvent is the type of change detected by the poller.
type PeerEvent string

const (
	PeerEventJoined PeerEvent = "joined"
	PeerEventLeft   PeerEvent = "left"
	PeerEventStatus PeerEvent = "status" // status or current_task changed
)

// PeerChange describes a single change detected during a poll tick.
type PeerChange struct {
	Event    PeerEvent
	Current  PeerPresence // populated for joined/status
	Previous PeerPresence // populated for left/status (Previous = last known)
}

// PeerChangeHandler is called for every change detected by the poller.
// Implementations must be non-blocking; heavy work should be handed off to a
// goroutine.
type PeerChangeHandler func(change PeerChange)

// PeerDiscoveryPoller periodically scans the peer presence directory for a
// named swarm and invokes a PeerChangeHandler for each detected change.
//
// Usage:
//
//	p := a2a.NewPeerDiscoveryPoller("default", 2*time.Second, func(c a2a.PeerChange) {
//	    switch c.Event {
//	    case a2a.PeerEventJoined:
//	        client.InjectEvent(client.Event{Kind: client.EventPeerJoined, ...})
//	    }
//	})
//	p.Start()
//	defer p.Stop()
type PeerDiscoveryPoller struct {
	swarmName string
	interval  time.Duration
	handler   PeerChangeHandler

	mu      sync.Mutex
	known   map[string]PeerPresence // handle → last seen presence
	stopCh  chan struct{}
	started bool
	stopped bool
}

// NewPeerDiscoveryPoller creates a new poller for the given swarm.
// interval is the polling cadence; values < 500ms are clamped to 500ms.
// handler is invoked synchronously on the poller's goroutine for each change.
func NewPeerDiscoveryPoller(swarmName string, interval time.Duration, handler PeerChangeHandler) *PeerDiscoveryPoller {
	if swarmName == "" {
		swarmName = DefaultSwarmName
	}
	if interval < 500*time.Millisecond {
		interval = 500 * time.Millisecond
	}
	return &PeerDiscoveryPoller{
		swarmName: swarmName,
		interval:  interval,
		handler:   handler,
		known:     make(map[string]PeerPresence),
		stopCh:    make(chan struct{}),
	}
}

// Start launches the polling loop in a background goroutine.
// Safe to call multiple times; subsequent calls are no-ops.
func (p *PeerDiscoveryPoller) Start() {
	p.mu.Lock()
	if p.started {
		p.mu.Unlock()
		return
	}
	p.started = true
	p.mu.Unlock()
	go p.loop()
}

// Stop halts the polling loop. Idempotent and safe to call before Start.
func (p *PeerDiscoveryPoller) Stop() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if !p.stopped {
		p.stopped = true
		close(p.stopCh)
	}
}

// Snapshot returns a copy of the most-recently observed peer set.
// Useful for initialising UI state without waiting for the first poll.
func (p *PeerDiscoveryPoller) Snapshot() []PeerPresence {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]PeerPresence, 0, len(p.known))
	for _, v := range p.known {
		out = append(out, v)
	}
	return out
}

// loop is the main polling goroutine.
func (p *PeerDiscoveryPoller) loop() {
	// Tick immediately so the first snapshot is available right away, then
	// on the configured interval.
	p.tick()
	t := time.NewTicker(p.interval)
	defer t.Stop()
	for {
		select {
		case <-p.stopCh:
			return
		case <-t.C:
			p.tick()
		}
	}
}

// tick performs one poll cycle: reads live peers, diffs against known set,
// and fires the handler for each change.
func (p *PeerDiscoveryPoller) tick() {
	current, err := ListPeers(p.swarmName)
	if err != nil {
		// Transient filesystem error — keep known state, try again next tick.
		return
	}

	// Build a lookup map for O(1) presence checks.
	currentMap := make(map[string]PeerPresence, len(current))
	for _, peer := range current {
		currentMap[peer.Handle] = peer
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	// Detect joins and status changes.
	for handle, newPeer := range currentMap {
		if old, existed := p.known[handle]; !existed {
			// New peer.
			p.known[handle] = newPeer
			if p.handler != nil {
				p.handler(PeerChange{Event: PeerEventJoined, Current: newPeer})
			}
		} else if peerStatusChanged(old, newPeer) {
			// Known peer with updated status or task.
			p.known[handle] = newPeer
			if p.handler != nil {
				p.handler(PeerChange{Event: PeerEventStatus, Current: newPeer, Previous: old})
			}
		} else {
			// No meaningful change — update timestamp silently so LastSeenAt
			// doesn't trigger spurious status events.
			p.known[handle] = newPeer
		}
	}

	// Detect departures.
	for handle, old := range p.known {
		if _, alive := currentMap[handle]; !alive {
			delete(p.known, handle)
			if p.handler != nil {
				p.handler(PeerChange{Event: PeerEventLeft, Previous: old})
			}
		}
	}
}

// peerStatusChanged returns true when the fields that consumers care about have
// changed between two presence snapshots.  Timestamp-only changes are ignored
// to avoid noisy status events on every poll tick.
func peerStatusChanged(old, next PeerPresence) bool {
	return old.Status != next.Status ||
		old.CurrentTask != next.CurrentTask ||
		old.Model != next.Model ||
		old.Workspace != next.Workspace
}
