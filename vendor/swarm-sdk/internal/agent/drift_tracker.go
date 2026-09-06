// Package agent — drift_tracker.go.
//
// Phase 3 of the steering-agent-with-tools redesign
// (docs/steering-redesign/steering-redesign.pdf).
//
// DriftTracker tracks cheap signals from the subject agent's event stream
// and appends a human-readable drift summary to the observer's transcript.
//
// DESIGN DECISION: DriftTracker does NOT mutate the SteeringTarget. It is
// metadata-only. The observer LLM remains the single writer to the target.
// This avoids dual-writer conflicts (DriftTracker auto-arming a block while
// the observer decides to let the tool through). Cheap-path auto-intervention
// can be layered on top in a future phase once we validate the metadata
// quality.
//
// Peer DM counting is intentionally omitted here because the steering hook
// only sees tool events (EventToolBeforeExecute, EventToolAfterExecute,
// EventAgentStopped) — it never sees A2A peer-DM events. Peer DM drift
// detection belongs in the A2A layer where that data is available.
package agent

import (
	"fmt"
	"sort"
	"strings"
	"sync"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/hooks"
)

// DriftConfig tunes the drift detector. All fields have sensible defaults
// when zero. DriftConfig is a value (not a pointer) — always present on the
// driver, gated by DriftEnabled on SteeringDriverConfig.
type DriftConfig struct {
	// ToolRepeatThreshold is the consecutive identical tool-name count that
	// constitutes a "repeated tool" signal. Default 4.
	ToolRepeatThreshold int

	// PeerDMThreshold is the per-peer cumulative DM count that constitutes
	// a "peer loop" signal. Counts inbound A2A DMs by peer handle, no
	// rolling window. Default 6.
	PeerDMThreshold int
}

// DriftTracker tracks cheap signals from the event stream. It does NOT
// mutate the SteeringTarget — it only appends a drift summary to the
// transcript so the observer can make informed decisions.
//
// Thread-safe: RecordEvent may be called from Enqueue (many goroutines);
// Summary may be called from flush (single goroutine, but concurrent with
// RecordEvent).
type DriftTracker struct {
	mu           sync.Mutex
	cfg          DriftConfig
	lastToolName string
	repeatCount  int // consecutive repeats of lastToolName
	totalEvents  int
	peerDMCounts map[string]int // peer handle → session-cumulative DM count
}

// NewDriftTracker constructs a tracker with the given config, applying
// defaults for zero-valued fields.
func NewDriftTracker(cfg DriftConfig) *DriftTracker {
	if cfg.ToolRepeatThreshold <= 0 {
		cfg.ToolRepeatThreshold = 4
	}
	if cfg.PeerDMThreshold <= 0 {
		cfg.PeerDMThreshold = 6
	}
	return &DriftTracker{
		cfg:          cfg,
		lastToolName: "",
		repeatCount:  0,
		totalEvents:  0,
		peerDMCounts: make(map[string]int),
	}
}

// RecordEvent updates counters from a single hook event. O(1).
//
// Tool events (EventToolBeforeExecute, EventToolAfterExecute):
//
//	extracts tool_name (with "name" fallback) and updates the consecutive-
//	repeat streak.
//
// Peer-DM events (EventA2AMessageProjected):
//
//	extracts peer_handle and increments per-peer session-cumulative count.
//	Empty peer handles are ignored.
//
// All other event types are no-ops.
func (dt *DriftTracker) RecordEvent(ev hooks.Event) {
	switch ev.Type {
	case hooks.EventToolBeforeExecute, hooks.EventToolAfterExecute:
		dt.recordToolEvent(ev)
	case hooks.EventA2AMessageProjected:
		dt.recordPeerDMEvent(ev)
	}
}

// recordToolEvent updates the tool-repeat streak.
func (dt *DriftTracker) recordToolEvent(ev hooks.Event) {
	toolName, _ := ev.Data["tool_name"].(string)
	if toolName == "" {
		toolName, _ = ev.Data["name"].(string)
	}
	if toolName == "" {
		return
	}

	dt.mu.Lock()
	defer dt.mu.Unlock()
	dt.totalEvents++

	if toolName == dt.lastToolName {
		dt.repeatCount++
	} else {
		dt.lastToolName = toolName
		dt.repeatCount = 1
	}
}

// recordPeerDMEvent increments the per-peer DM counter.
func (dt *DriftTracker) recordPeerDMEvent(ev hooks.Event) {
	peer, _ := ev.Data["peer_handle"].(string)
	if peer == "" {
		return
	}
	dt.mu.Lock()
	defer dt.mu.Unlock()
	dt.totalEvents++
	dt.peerDMCounts[peer]++
}

// Summary returns a human-readable drift status for the observer transcript.
// Format:
//
//	## Drift Status
//	tool_repeats: Bash x5 (threshold=4)
//	peer_dms: pop-os-695063 x8 (threshold=6)
//	total_events: 47
//
// Lines are omitted when their threshold is not crossed (keeps the
// transcript lean when things are healthy).
func (dt *DriftTracker) Summary() string {
	dt.mu.Lock()
	defer dt.mu.Unlock()

	var b strings.Builder
	b.WriteString("## Drift Status\n")

	if dt.repeatCount >= dt.cfg.ToolRepeatThreshold && dt.lastToolName != "" {
		b.WriteString(fmt.Sprintf("tool_repeats: %s x%d (threshold=%d)\n",
			dt.lastToolName, dt.repeatCount, dt.cfg.ToolRepeatThreshold))
	}

	// Peer DMs: emit one line per peer over threshold. Stable order via
	// sorted keys so the transcript is deterministic.
	if len(dt.peerDMCounts) > 0 {
		peers := make([]string, 0, len(dt.peerDMCounts))
		for p, n := range dt.peerDMCounts {
			if n >= dt.cfg.PeerDMThreshold {
				peers = append(peers, p)
			}
		}
		sort.Strings(peers)
		for _, p := range peers {
			b.WriteString(fmt.Sprintf("peer_dms: %s x%d (threshold=%d)\n",
				p, dt.peerDMCounts[p], dt.cfg.PeerDMThreshold))
		}
	}

	b.WriteString(fmt.Sprintf("total_events: %d\n", dt.totalEvents))
	return b.String()
}
