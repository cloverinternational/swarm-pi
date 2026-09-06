// Package autogenskills implements the Hermes-style closed learning loop for
// the Swarm SDK skill system.
package autogenskills

import (
	"sync/atomic"
)

// Metrics tracks runtime counters for the autogenskills lifecycle.
//
// CONTRACT:
//   - All counters are monotonically increasing during a session.
//   - All operations are thread-safe via sync/atomic.
//   - Snapshot returns a consistent point-in-time view.
//   - Zero value is valid (all counters start at 0).
type Metrics struct {
	turnCount          atomic.Uint64
	toolCallCount      atomic.Uint64
	errorCount         atomic.Uint64
	errorResolvedCount atomic.Uint64
	nudgeCount         atomic.Uint64
	skillCreatedCount  atomic.Uint64
	skillPatchedCount  atomic.Uint64
}

// IncrementTurn adds one to the turn counter.
func (m *Metrics) IncrementTurn() {
	m.turnCount.Add(1)
}

// IncrementToolCall adds one to the tool call counter.
func (m *Metrics) IncrementToolCall() {
	m.toolCallCount.Add(1)
}

// IncrementError adds one to the error counter.
func (m *Metrics) IncrementError() {
	m.errorCount.Add(1)
}

// IncrementErrorResolved adds one to the error-resolved counter.
func (m *Metrics) IncrementErrorResolved() {
	m.errorResolvedCount.Add(1)
}

// IncrementNudge adds one to the nudge counter.
func (m *Metrics) IncrementNudge() {
	m.nudgeCount.Add(1)
}

// IncrementSkillCreated adds one to the skill-created counter.
func (m *Metrics) IncrementSkillCreated() {
	m.skillCreatedCount.Add(1)
}

// IncrementSkillPatched adds one to the skill-patched counter.
func (m *Metrics) IncrementSkillPatched() {
	m.skillPatchedCount.Add(1)
}

// Snapshot returns a consistent point-in-time copy of all counters.
func (m *Metrics) Snapshot() MetricsSnapshot {
	return MetricsSnapshot{
		TurnCount:          m.turnCount.Load(),
		ToolCallCount:      m.toolCallCount.Load(),
		ErrorCount:         m.errorCount.Load(),
		ErrorResolvedCount: m.errorResolvedCount.Load(),
		NudgeCount:         m.nudgeCount.Load(),
		SkillCreatedCount:  m.skillCreatedCount.Load(),
		SkillPatchedCount:  m.skillPatchedCount.Load(),
	}
}

// MetricsSnapshot is a point-in-time immutable view of Metrics.
type MetricsSnapshot struct {
	TurnCount          uint64
	ToolCallCount      uint64
	ErrorCount         uint64
	ErrorResolvedCount uint64
	NudgeCount         uint64
	SkillCreatedCount  uint64
	SkillPatchedCount  uint64
}

// Reset atomically resets all counters to zero.
// CONTRACT: Only use between sessions, never mid-session.
func (m *Metrics) Reset() {
	m.turnCount.Store(0)
	m.toolCallCount.Store(0)
	m.errorCount.Store(0)
	m.errorResolvedCount.Store(0)
	m.nudgeCount.Store(0)
	m.skillCreatedCount.Store(0)
	m.skillPatchedCount.Store(0)
}

// ToNudgeContext converts the current snapshot into a NudgeContext.
// The caller must provide skill names from the registry separately.
func (m *Metrics) ToNudgeContext(existingSkills []string, lastNudgeTurn uint) NudgeContext {
	snap := m.Snapshot()
	return NudgeContext{
		TurnCount:          uint(snap.TurnCount),
		ToolCallCount:      uint(snap.ToolCallCount),
		ErrorCount:         uint(snap.ErrorCount),
		ErrorResolvedCount: uint(snap.ErrorResolvedCount),
		ExistingSkillNames: existingSkills,
		LastNudgeTurn:      lastNudgeTurn,
	}
}
