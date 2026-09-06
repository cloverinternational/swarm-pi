package agent

import (
	"testing"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/conversation"
)

// TestSeedTokenCountFromHistory_UsesLastAssistantUsage verifies the backward
// scan picks the most recent assistant message carrying API-reported usage.
func TestSeedTokenCountFromHistory_UsesLastAssistantUsage(t *testing.T) {
	a := &Agent{}
	history := []*conversation.Message{
		{Role: conversation.RoleUser, Content: "hi"},
		{Role: conversation.RoleAssistant, Content: "old", Tokens: &conversation.TokenUsage{Input: 1_000, Output: 50}},
		{Role: conversation.RoleUser, Content: "again"},
		{Role: conversation.RoleAssistant, Content: "new", Tokens: &conversation.TokenUsage{Input: 42_000, Output: 700}},
	}
	a.seedTokenCountFromHistory(history)
	if a.inputTokens != history[3].Tokens.InputContextSize() {
		t.Fatalf("inputTokens: got %d want %d", a.inputTokens, history[3].Tokens.InputContextSize())
	}
	if a.outputTokens != 700 {
		t.Fatalf("outputTokens: got %d want 700", a.outputTokens)
	}
}

// TestExecuteTokenReset_NoUsageHistoryDoesNotInheritStaleCounts is the
// regression test for the cross-conversation token bleed: execute() zeroes
// inputTokens/outputTokens before seeding from the request history, so a
// history with no API-reported usage (fresh or freshly compacted conversation)
// must end with zeroed counters — not the previous conversation's values,
// which used to trip checkContextPressure's blocking-limit/auto-compact
// thresholds against a tiny conversation. This mirrors the exact statement
// sequence in execute() (reset, then seed).
func TestExecuteTokenReset_NoUsageHistoryDoesNotInheritStaleCounts(t *testing.T) {
	a := &Agent{
		inputTokens:  190_000, // stale counters from a previous, large conversation
		outputTokens: 9_000,
	}
	history := []*conversation.Message{
		{Role: conversation.RoleUser, Content: "compaction handoff summary"},
		{Role: conversation.RoleAssistant, Content: "resuming"}, // no Tokens: summary messages carry no usage
	}

	// The sequence under test, as performed at the top of execute().
	a.inputTokens = 0
	a.outputTokens = 0
	a.seedTokenCountFromHistory(history)

	if a.inputTokens != 0 || a.outputTokens != 0 {
		t.Fatalf("counters after reset+seed with usage-free history: input=%d output=%d, want 0/0",
			a.inputTokens, a.outputTokens)
	}
}
