package agent

import (
	"strings"
	"sync"
	"testing"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/hooks"
)

func TestDriftTracker_RepeatCounting(t *testing.T) {
	dt := NewDriftTracker(DriftConfig{ToolRepeatThreshold: 4})

	// Three Bash events → repeatCount = 3, below threshold.
	for range 3 {
		dt.RecordEvent(hooks.Event{
			Type: hooks.EventToolBeforeExecute,
			Data: map[string]any{"tool_name": "Bash"},
		})
	}

	s := dt.Summary()
	if strings.Contains(s, "tool_repeats") {
		t.Fatalf("repeatCount=3 should not exceed threshold=4, got: %s", s)
	}

	// Fourth Bash → repeatCount = 4, at threshold.
	dt.RecordEvent(hooks.Event{
		Type: hooks.EventToolBeforeExecute,
		Data: map[string]any{"tool_name": "Bash"},
	})
	s = dt.Summary()
	if !strings.Contains(s, "tool_repeats: Bash x4") {
		t.Fatalf("expected repeat signal at threshold, got: %s", s)
	}

	// Different tool resets the streak.
	dt.RecordEvent(hooks.Event{
		Type: hooks.EventToolBeforeExecute,
		Data: map[string]any{"tool_name": "Read"},
	})
	s = dt.Summary()
	if strings.Contains(s, "tool_repeats: Bash") {
		t.Fatalf("different tool should reset streak, got: %s", s)
	}
}

func TestDriftTracker_Defaults(t *testing.T) {
	dt := NewDriftTracker(DriftConfig{}) // zero ToolRepeatThreshold
	if dt.cfg.ToolRepeatThreshold != 4 {
		t.Fatalf("expected default threshold=4, got %d", dt.cfg.ToolRepeatThreshold)
	}
}

func TestDriftTracker_SummaryFormat(t *testing.T) {
	dt := NewDriftTracker(DriftConfig{ToolRepeatThreshold: 2})

	// Two same tool events → exceeds threshold.
	dt.RecordEvent(hooks.Event{
		Type: hooks.EventToolBeforeExecute,
		Data: map[string]any{"tool_name": "Write"},
	})
	dt.RecordEvent(hooks.Event{
		Type: hooks.EventToolBeforeExecute,
		Data: map[string]any{"tool_name": "Write"},
	})

	s := dt.Summary()
	if !strings.Contains(s, "## Drift Status") {
		t.Fatalf("summary missing header, got: %s", s)
	}
	if !strings.Contains(s, "tool_repeats: Write x2") {
		t.Fatalf("summary missing repeat line, got: %s", s)
	}
	if !strings.Contains(s, "total_events: 2") {
		t.Fatalf("summary missing total, got: %s", s)
	}
}

func TestDriftTracker_NameFallback(t *testing.T) {
	dt := NewDriftTracker(DriftConfig{ToolRepeatThreshold: 2})

	// event.Data uses "name" instead of "tool_name" (same fallback as the hook).
	dt.RecordEvent(hooks.Event{
		Type: hooks.EventToolBeforeExecute,
		Data: map[string]any{"name": "Grep"},
	})
	dt.RecordEvent(hooks.Event{
		Type: hooks.EventToolBeforeExecute,
		Data: map[string]any{"name": "Grep"},
	})

	s := dt.Summary()
	if !strings.Contains(s, "tool_repeats: Grep x2") {
		t.Fatalf("name fallback not working, got: %s", s)
	}
}

func TestDriftTracker_IgnoresNonToolEvents(t *testing.T) {
	dt := NewDriftTracker(DriftConfig{ToolRepeatThreshold: 2})

	dt.RecordEvent(hooks.Event{
		Type: hooks.EventAgentStopped,
	})
	s := dt.Summary()
	if !strings.Contains(s, "total_events: 0") {
		t.Fatalf("agent.stopped should not count as tool event, got: %s", s)
	}
}

func TestDriftTracker_ConcurrentRecordAndSummary(t *testing.T) {
	dt := NewDriftTracker(DriftConfig{ToolRepeatThreshold: 4})
	var wg sync.WaitGroup

	// Concurrent RecordEvent from many goroutines.
	for range 32 {
		wg.Go(func() {
			dt.RecordEvent(hooks.Event{
				Type: hooks.EventToolBeforeExecute,
				Data: map[string]any{"tool_name": "Bash"},
			})
		})
	}

	// Concurrent Summary calls.
	for range 8 {
		wg.Go(func() {
			_ = dt.Summary()
		})
	}

	wg.Wait()
	// If we get here without -race firing, the mutex is doing its job.
	// Also assert all 32 concurrent tool events were counted exactly once
	// (no lost updates under contention).
	if got := dt.Summary(); !strings.Contains(got, "total_events: 32") {
		t.Fatalf("expected all 32 concurrent events counted, got: %s", got)
	}
}

func TestDriftTracker_PeerDMCounting(t *testing.T) {
	dt := NewDriftTracker(DriftConfig{PeerDMThreshold: 3})

	// Two DMs from alice → below threshold.
	for range 2 {
		dt.RecordEvent(hooks.Event{
			Type: hooks.EventA2AMessageProjected,
			Data: map[string]any{"peer_handle": "alice"},
		})
	}
	if got := dt.Summary(); strings.Contains(got, "peer_dms") {
		t.Fatalf("below threshold should not emit peer_dms, got: %s", got)
	}

	// Third DM → at threshold.
	dt.RecordEvent(hooks.Event{
		Type: hooks.EventA2AMessageProjected,
		Data: map[string]any{"peer_handle": "alice"},
	})
	if got := dt.Summary(); !strings.Contains(got, "peer_dms: alice x3") {
		t.Fatalf("expected peer_dms line, got: %s", got)
	}
}

func TestDriftTracker_MultiPeerDMs(t *testing.T) {
	dt := NewDriftTracker(DriftConfig{PeerDMThreshold: 2})

	// Two peers each crossing threshold.
	for _, p := range []string{"alice", "bob", "alice", "bob"} {
		dt.RecordEvent(hooks.Event{
			Type: hooks.EventA2AMessageProjected,
			Data: map[string]any{"peer_handle": p},
		})
	}

	got := dt.Summary()
	if !strings.Contains(got, "peer_dms: alice x2") {
		t.Errorf("missing alice line in: %s", got)
	}
	if !strings.Contains(got, "peer_dms: bob x2") {
		t.Errorf("missing bob line in: %s", got)
	}

	// Deterministic ordering: alice comes before bob (sorted).
	aliceIdx := strings.Index(got, "alice")
	bobIdx := strings.Index(got, "bob")
	if aliceIdx > bobIdx {
		t.Errorf("expected alice before bob (sorted), got: %s", got)
	}
}

func TestDriftTracker_EmptyPeerHandleIgnored(t *testing.T) {
	dt := NewDriftTracker(DriftConfig{PeerDMThreshold: 1})

	dt.RecordEvent(hooks.Event{
		Type: hooks.EventA2AMessageProjected,
		Data: map[string]any{"peer_handle": ""},
	})
	dt.RecordEvent(hooks.Event{
		Type: hooks.EventA2AMessageProjected,
		Data: map[string]any{}, // no peer_handle at all
	})

	if got := dt.Summary(); strings.Contains(got, "peer_dms") {
		t.Fatalf("empty peer_handle should not emit peer_dms, got: %s", got)
	}
	// totalEvents should also stay 0 — empty peer events are no-ops.
	if !strings.Contains(dt.Summary(), "total_events: 0") {
		t.Errorf("empty peer events should not increment totalEvents, got: %s", dt.Summary())
	}
}

func TestDriftTracker_ToolAndPeerInteract(t *testing.T) {
	dt := NewDriftTracker(DriftConfig{ToolRepeatThreshold: 2, PeerDMThreshold: 2})

	dt.RecordEvent(hooks.Event{
		Type: hooks.EventToolBeforeExecute,
		Data: map[string]any{"tool_name": "Bash"},
	})
	dt.RecordEvent(hooks.Event{
		Type: hooks.EventToolBeforeExecute,
		Data: map[string]any{"tool_name": "Bash"},
	})
	dt.RecordEvent(hooks.Event{
		Type: hooks.EventA2AMessageProjected,
		Data: map[string]any{"peer_handle": "carol"},
	})
	dt.RecordEvent(hooks.Event{
		Type: hooks.EventA2AMessageProjected,
		Data: map[string]any{"peer_handle": "carol"},
	})

	got := dt.Summary()
	if !strings.Contains(got, "tool_repeats: Bash x2") {
		t.Errorf("missing tool line: %s", got)
	}
	if !strings.Contains(got, "peer_dms: carol x2") {
		t.Errorf("missing peer line: %s", got)
	}
	if !strings.Contains(got, "total_events: 4") {
		t.Errorf("expected total=4, got: %s", got)
	}
}
