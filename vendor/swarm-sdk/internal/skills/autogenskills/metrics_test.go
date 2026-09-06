package autogenskills

import (
	"sync"
	"testing"
)

// TestMetrics_Incrementors verifies all increment methods work.
func TestMetrics_Incrementors(t *testing.T) {
	m := &Metrics{}

	m.IncrementTurn()
	if snap := m.Snapshot(); snap.TurnCount != 1 {
		t.Errorf("TurnCount = %d, want 1", snap.TurnCount)
	}

	m.IncrementToolCall()
	if snap := m.Snapshot(); snap.ToolCallCount != 1 {
		t.Errorf("ToolCallCount = %d, want 1", snap.ToolCallCount)
	}

	m.IncrementError()
	if snap := m.Snapshot(); snap.ErrorCount != 1 {
		t.Errorf("ErrorCount = %d, want 1", snap.ErrorCount)
	}

	m.IncrementErrorResolved()
	if snap := m.Snapshot(); snap.ErrorResolvedCount != 1 {
		t.Errorf("ErrorResolvedCount = %d, want 1", snap.ErrorResolvedCount)
	}

	m.IncrementNudge()
	if snap := m.Snapshot(); snap.NudgeCount != 1 {
		t.Errorf("NudgeCount = %d, want 1", snap.NudgeCount)
	}

	m.IncrementSkillCreated()
	if snap := m.Snapshot(); snap.SkillCreatedCount != 1 {
		t.Errorf("SkillCreatedCount = %d, want 1", snap.SkillCreatedCount)
	}
}

// TestMetrics_Reset verifies reset clears all counters.
func TestMetrics_Reset(t *testing.T) {
	m := &Metrics{}
	m.IncrementTurn()
	m.IncrementToolCall()
	m.IncrementError()
	m.Reset()

	snap := m.Snapshot()
	if snap.TurnCount != 0 || snap.ToolCallCount != 0 || snap.ErrorCount != 0 {
		t.Error("Reset did not clear counters")
	}
}

// TestMetrics_Concurrent increments from many goroutines safely.
func TestMetrics_Concurrent(t *testing.T) {
	m := &Metrics{}
	var wg sync.WaitGroup
	workers := 100
	perWorker := 1000

	for range workers {
		wg.Go(func() {
			for range perWorker {
				m.IncrementTurn()
				m.IncrementToolCall()
				m.IncrementError()
				m.IncrementErrorResolved()
				m.IncrementNudge()
				m.IncrementSkillCreated()
			}
		})
	}
	wg.Wait()

	snap := m.Snapshot()
	want := uint64(workers * perWorker)
	if snap.TurnCount != want {
		t.Errorf("TurnCount = %d, want %d", snap.TurnCount, want)
	}
	if snap.ToolCallCount != want {
		t.Errorf("ToolCallCount = %d, want %d", snap.ToolCallCount, want)
	}
	if snap.ErrorCount != want {
		t.Errorf("ErrorCount = %d, want %d", snap.ErrorCount, want)
	}
	if snap.ErrorResolvedCount != want {
		t.Errorf("ErrorResolvedCount = %d, want %d", snap.ErrorResolvedCount, want)
	}
	if snap.NudgeCount != want {
		t.Errorf("NudgeCount = %d, want %d", snap.NudgeCount, want)
	}
	if snap.SkillCreatedCount != want {
		t.Errorf("SkillCreatedCount = %d, want %d", snap.SkillCreatedCount, want)
	}
}

// TestMetrics_ToNudgeContext verifies conversion to NudgeContext.
func TestMetrics_ToNudgeContext(t *testing.T) {
	m := &Metrics{}
	m.IncrementTurn()
	m.IncrementTurn()
	m.IncrementToolCall()
	m.IncrementErrorResolved()

	ctx := m.ToNudgeContext([]string{"skill-a", "skill-b"}, 5)

	if ctx.TurnCount != 2 {
		t.Errorf("TurnCount = %d, want 2", ctx.TurnCount)
	}
	if ctx.ToolCallCount != 1 {
		t.Errorf("ToolCallCount = %d, want 1", ctx.ToolCallCount)
	}
	if ctx.ErrorResolvedCount != 1 {
		t.Errorf("ErrorResolvedCount = %d, want 1", ctx.ErrorResolvedCount)
	}
	if len(ctx.ExistingSkillNames) != 2 {
		t.Errorf("ExistingSkillNames len = %d, want 2", len(ctx.ExistingSkillNames))
	}
	if ctx.LastNudgeTurn != 5 {
		t.Errorf("LastNudgeTurn = %d, want 5", ctx.LastNudgeTurn)
	}
}

// TestMetricsSnapshot_Immutability ensures snapshot doesn't change with future increments.
func TestMetricsSnapshot_Immutability(t *testing.T) {
	m := &Metrics{}
	m.IncrementTurn()

	snap := m.Snapshot()
	m.IncrementTurn()

	if snap.TurnCount != 1 {
		t.Errorf("Snapshot TurnCount mutated: %d", snap.TurnCount)
	}
}

// TestMetrics_ZeroValueIsValid ensures zero-value Metrics works.
func TestMetrics_ZeroValueIsValid(t *testing.T) {
	var m Metrics
	snap := m.Snapshot()
	if snap.TurnCount != 0 || snap.ToolCallCount != 0 {
		t.Error("zero-value Metrics should have 0 counters")
	}
}
