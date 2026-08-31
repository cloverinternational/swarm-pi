package chat

import (
	"testing"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/conversation"
)

// TestApplyPostCompactionTokenCountsResetsStaleLastReal locks the fix for the
// "compaction runs twice" bug. After a compaction commit, the conversation's
// CurrentContextSize is the small compacted size but CurrentContextSizeEstimated
// is ALWAYS true (AdvanceActiveContext sets it). The old code guarded the
// lastRealTokenCount reset behind `!estimated`, so it never ran and left the
// stale pre-compaction total, which the pre-send auto-compaction predictor then
// used to immediately re-fire compaction.
func TestApplyPostCompactionTokenCountsResetsStaleLastReal(t *testing.T) {
	// Build a realistic post-compaction conversation via the same path the
	// TUI uses so the estimated flag is set exactly as in production.
	conv := &conversation.Conversation{}
	conv.AddMessage(&conversation.Message{Role: conversation.RoleUser, Content: "original long turn"})
	summary := &conversation.Message{Role: conversation.RoleAssistant, Content: "compacted handoff summary"}
	conv.AdvanceActiveContext([]*conversation.Message{summary}, "compacted handoff summary", 7396)

	if !conv.CurrentContextSizeEstimated {
		t.Fatalf("precondition failed: AdvanceActiveContext must mark size estimated")
	}
	if conv.CurrentContextSize != 7396 {
		t.Fatalf("precondition failed: CurrentContextSize = %d, want 7396", conv.CurrentContextSize)
	}

	// Simulate the stale pre-compaction real total left over from the last API
	// response.
	a := &App{
		tokenCount:         252772,
		lastRealTokenCount: 252772,
	}

	a.applyPostCompactionTokenCounts(conv)

	if a.lastRealTokenCount != 7396 {
		t.Errorf("lastRealTokenCount = %d, want 7396 (stale pre-compaction total not reset -> compaction re-fires)", a.lastRealTokenCount)
	}
	if a.tokenCount != 7396 {
		t.Errorf("tokenCount = %d, want 7396", a.tokenCount)
	}
	if !a.tokenCountIsEstimate {
		t.Errorf("tokenCountIsEstimate = false, want true (post-compaction size is an estimate)")
	}
}

// TestApplyPostCompactionTokenCountsNilAndZeroAreSafe verifies the helper does
// not panic on a nil conversation and does not clobber lastRealTokenCount to
// zero when the reported size is zero/unknown.
func TestApplyPostCompactionTokenCountsNilAndZeroAreSafe(t *testing.T) {
	a := &App{tokenCount: 1234, lastRealTokenCount: 1234}

	a.applyPostCompactionTokenCounts(nil) // must be a no-op, no panic
	if a.lastRealTokenCount != 1234 || a.tokenCount != 1234 {
		t.Fatalf("nil conv mutated counters: tokenCount=%d lastReal=%d", a.tokenCount, a.lastRealTokenCount)
	}

	zero := &conversation.Conversation{}
	// CurrentContextSize defaults to 0; lastRealTokenCount must be preserved.
	a.applyPostCompactionTokenCounts(zero)
	if a.lastRealTokenCount != 1234 {
		t.Errorf("zero-size conv clobbered lastRealTokenCount to %d, want preserved 1234", a.lastRealTokenCount)
	}
}
