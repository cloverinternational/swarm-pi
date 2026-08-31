package agent

import (
	"context"
	"errors"
	"testing"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/compaction"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/conversation"
)

// TestResetContextTokens verifies the post-compaction reset authoritatively
// overwrites a stale pre-compaction total.
func TestResetContextTokens(t *testing.T) {
	a := &Agent{inputTokens: 269_030}
	a.ResetContextTokens(38_070)
	if a.inputTokens != 38_070 {
		t.Fatalf("inputTokens after reset: got %d want 38070", a.inputTokens)
	}

	// Negative estimates clamp to zero.
	a.ResetContextTokens(-5)
	if a.inputTokens != 0 {
		t.Fatalf("inputTokens after negative reset: got %d want 0", a.inputTokens)
	}
}

// newGuardAgent builds a minimal Agent whose effective input window makes the
// blocking limit small and deterministic. contextWindow=100000 →
// effectiveInputWindow=100000 (no provider ⇒ no output reservation) → blocking
// limit = 100000 - 3000 = 97000.
func newGuardAgent(cfg AutoCompactionConfig) *Agent {
	a := &Agent{configuredContextWindow: 100_000}
	a.autoCompactionConfig = cfg
	return a
}

// TestCheckContextPressure_BlocksWhenNoCompactFunc verifies that an oversized
// provider request is never sent when no in-agent compactor is available.
func TestCheckContextPressure_BlocksWhenNoCompactFunc(t *testing.T) {
	a := newGuardAgent(AutoCompactionConfig{
		EnableAutoCompaction: true,
		CompactFunc:          nil,
	})
	a.inputTokens = a.getBlockingLimit() + 5_000 // safely over the hard limit

	var emitted *CompactionNeededUpdate
	a.intermediateCallback = func(_ context.Context, u IntermediateUpdate) error {
		if n, ok := u.(CompactionNeededUpdate); ok {
			cp := n
			emitted = &cp
		}
		return nil
	}

	msgs := []*conversation.Message{{Role: conversation.RoleUser, Content: "hi"}}
	notified := false
	_, err := a.checkContextPressure(context.Background(), &msgs, a.inputTokens, &notified)
	var cpErr *ContextPressureError
	if !errors.As(err, &cpErr) {
		t.Fatalf("expected *ContextPressureError, got %v", err)
	}
	if emitted == nil {
		t.Fatal("expected a CompactionNeededUpdate to be emitted")
	}
}

// TestCheckContextPressure_FatalWhenAutoCompactionDisabled verifies that when
// auto-compaction is disabled and the count is over the blocking limit, the
// typed ContextPressureError is still returned.
func TestCheckContextPressure_FatalWhenAutoCompactionDisabled(t *testing.T) {
	a := newGuardAgent(AutoCompactionConfig{
		EnableAutoCompaction: false,
		CompactFunc:          nil,
	})
	a.inputTokens = a.getBlockingLimit() + 5_000

	msgs := []*conversation.Message{{Role: conversation.RoleUser, Content: "hi"}}
	notified := false
	_, err := a.checkContextPressure(context.Background(), &msgs, a.inputTokens, &notified)
	var cpErr *ContextPressureError
	if !errors.As(err, &cpErr) {
		t.Fatalf("expected *ContextPressureError, got %v", err)
	}
	if cpErr.Stage != "context_limit_guard" {
		t.Fatalf("unexpected stage: %q", cpErr.Stage)
	}
}

// TestSeedTokenCountFromHistory_StopsAtCompactionBoundary verifies the backward
// scan never reads a pre-compaction assistant usage that sits before a
// compaction summary/handoff boundary.
func TestSeedTokenCountFromHistory_StopsAtCompactionBoundary(t *testing.T) {
	a := &Agent{}
	history := []*conversation.Message{
		// Pre-compaction assistant with a huge (stale) usage total.
		{Role: conversation.RoleAssistant, Content: "old", Tokens: &conversation.TokenUsage{Input: 269_030, Output: 400}},
		// Compaction summary/handoff: post-boundary, carries no usage.
		{Role: conversation.RoleUser, Content: compaction.SummaryPrefix + "\nhandoff details"},
	}
	a.seedTokenCountFromHistory(history)
	if a.inputTokens != 0 {
		t.Fatalf("seed must not read across a compaction boundary: got inputTokens=%d want 0", a.inputTokens)
	}
	if a.outputTokens != 0 {
		t.Fatalf("seed must not read across a compaction boundary: got outputTokens=%d want 0", a.outputTokens)
	}
}

// TestSeedTokenCountFromHistory_BoundaryViaMetadata verifies boundary detection
// also honors an explicit metadata flag.
func TestSeedTokenCountFromHistory_BoundaryViaMetadata(t *testing.T) {
	a := &Agent{}
	history := []*conversation.Message{
		{Role: conversation.RoleAssistant, Content: "old", Tokens: &conversation.TokenUsage{Input: 269_030}},
		{Role: conversation.RoleUser, Content: "resume", Metadata: map[string]any{"compaction_boundary": true}},
	}
	a.seedTokenCountFromHistory(history)
	if a.inputTokens != 0 {
		t.Fatalf("metadata boundary ignored: got inputTokens=%d want 0", a.inputTokens)
	}
}
