package hooks

import (
	"context"
	"strings"
	"sync"
)

// MetaNudgePriority defines arbitration order for competing harness nudges.
type MetaNudgePriority int

const (
	MetaNudgeTask        MetaNudgePriority = 10
	MetaNudgeMaintenance MetaNudgePriority = 20
	MetaNudgeSkillReview MetaNudgePriority = 30
	MetaNudgeBlock       MetaNudgePriority = 40
	DefaultNudgeInterval                   = 5
)

type metaNudgeState struct {
	turn         uint
	lastAt       uint
	lastSeq      int
	lastPriority MetaNudgePriority
}

// MetaNudgeBudget limits all harness meta-nudges to one per turn window.
type MetaNudgeBudget struct {
	mu       sync.Mutex
	interval uint
	states   map[string]metaNudgeState
}

func NewMetaNudgeBudget(interval uint) *MetaNudgeBudget {
	if interval == 0 {
		interval = DefaultNudgeInterval
	}
	return &MetaNudgeBudget{interval: interval, states: make(map[string]metaNudgeState)}
}

// RecordUserTurn advances one conversation's cadence clock.
func (b *MetaNudgeBudget) RecordUserTurn(session string) uint {
	b.mu.Lock()
	defer b.mu.Unlock()
	if strings.TrimSpace(session) == "" {
		return 0
	}
	session = normalizeNudgeSession(session)
	state := b.states[session]
	state.turn++
	state.lastPriority = 0
	b.states[session] = state
	return state.turn
}

// TryClaim reserves the current window and returns the envelope sequence.
// Callers execute in priority order (block, skill, maintenance, task); a later
// lower-priority claimant cannot create a second injection.
func (b *MetaNudgeBudget) TryClaim(session string, priority MetaNudgePriority) (int, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if strings.TrimSpace(session) == "" {
		state := b.states["__unscoped__"]
		state.lastSeq++
		b.states["__unscoped__"] = state
		return state.lastSeq, true
	}
	session = normalizeNudgeSession(session)
	state := b.states[session]
	if state.turn == 0 {
		return 0, false
	}
	if state.lastAt != 0 && state.turn-state.lastAt < b.interval {
		return 0, false
	}
	state.lastAt = state.turn
	state.lastSeq++
	state.lastPriority = priority
	b.states[session] = state
	return state.lastSeq, true
}

func normalizeNudgeSession(session string) string {
	if session = strings.TrimSpace(session); session != "" {
		return session
	}
	return "default"
}

// NudgeSessionID resolves the stable root/conversation identity used by all
// hooks participating in the shared budget.
func NudgeSessionID(ctx context.Context, event Event) string {
	for _, key := range []string{"root_session_id", "conversation_id", "session_id"} {
		if id, _ := ctx.Value(key).(string); strings.TrimSpace(id) != "" {
			return id
		}
	}
	if id := strings.TrimSpace(event.ConversationID); id != "" {
		return id
	}
	if id, _ := event.Data["session_id"].(string); strings.TrimSpace(id) != "" {
		return id
	}
	return ""
}

var (
	defaultMetaNudgeBudgetMu sync.RWMutex
	defaultMetaNudgeBudget   = NewMetaNudgeBudget(DefaultNudgeInterval)
)

func DefaultMetaNudgeBudget() *MetaNudgeBudget {
	defaultMetaNudgeBudgetMu.RLock()
	defer defaultMetaNudgeBudgetMu.RUnlock()
	return defaultMetaNudgeBudget
}

// ConfigureDefaultMetaNudgeBudget replaces the shared cadence budget. It is
// intended for startup/config reload before concurrent hook execution.
func ConfigureDefaultMetaNudgeBudget(interval uint) {
	defaultMetaNudgeBudgetMu.Lock()
	defer defaultMetaNudgeBudgetMu.Unlock()
	defaultMetaNudgeBudget = NewMetaNudgeBudget(interval)
}

// ResetDefaultMetaNudgeBudgetForTest isolates package tests.
func ResetDefaultMetaNudgeBudgetForTest(interval uint) {
	ConfigureDefaultMetaNudgeBudget(interval)
}
