package chat

import (
	"errors"
	"testing"
)

// These tests guard the fallback in the agentResponseMsg handler that
// backfills the final assistant reply when no incremental content events
// arrived. The handler delegates to backfillEmptyAssistantFromFinal; if a
// regression breaks the narrow "fires only when message is empty" invariant,
// we will see incremental builds clobbered in production — these tests fail
// first.

func TestBackfillEmptyAssistantPopulatesContentAndBlock(t *testing.T) {
	messages := []Message{
		{Role: "user", Content: "hello"},
		{Role: "assistant"}, // empty placeholder, no blocks
	}
	changed := backfillEmptyAssistantFromFinal(messages, "final reply", nil)
	if !changed {
		t.Fatalf("expected backfill to fire for empty assistant, got false")
	}
	last := messages[len(messages)-1]
	if last.Content != "final reply" {
		t.Fatalf("Content not backfilled: got %q", last.Content)
	}
	if last.contentBuilder != nil {
		t.Fatalf("contentBuilder should be reset after backfill")
	}
	if len(last.OrderedBlocks) != 1 {
		t.Fatalf("expected 1 OrderedBlock, got %d", len(last.OrderedBlocks))
	}
	b := last.OrderedBlocks[0]
	if b.Type != "content" || b.GetContent() != "final reply" {
		t.Fatalf("block mismatch: type=%q content=%q", b.Type, b.GetContent())
	}
}

func TestBackfillSkipsWhenContentAlreadyPresent(t *testing.T) {
	messages := []Message{
		{Role: "assistant", Content: "already here"},
	}
	if backfillEmptyAssistantFromFinal(messages, "would clobber", nil) {
		t.Fatalf("backfill must not fire when Content is already populated")
	}
	if messages[0].Content != "already here" {
		t.Fatalf("Content was clobbered: %q", messages[0].Content)
	}
	if len(messages[0].OrderedBlocks) != 0 {
		t.Fatalf("OrderedBlocks should be untouched, got %d", len(messages[0].OrderedBlocks))
	}
}

func TestBackfillSkipsWhenContentBlockAlreadyStreamed(t *testing.T) {
	messages := []Message{
		{
			Role: "assistant",
			OrderedBlocks: []MessageBlock{
				{Type: "tool_call", ToolCall: &ToolCallDisplay{ID: "tc_1", Name: "Read"}},
				{Type: "content", Content: "streamed chunk"},
			},
		},
	}
	if backfillEmptyAssistantFromFinal(messages, "would clobber", nil) {
		t.Fatalf("backfill must not fire when a content OrderedBlock is present")
	}
	if len(messages[0].OrderedBlocks) != 2 {
		t.Fatalf("OrderedBlocks count changed: got %d", len(messages[0].OrderedBlocks))
	}
}

func TestBackfillSkipsOnError(t *testing.T) {
	messages := []Message{
		{Role: "assistant"},
	}
	if backfillEmptyAssistantFromFinal(messages, "final reply", errors.New("boom")) {
		t.Fatalf("backfill must not fire on error path (error handler owns content)")
	}
	if messages[0].Content != "" {
		t.Fatalf("Content mutated on error path: %q", messages[0].Content)
	}
}

func TestBackfillSkipsEmptyFinalContent(t *testing.T) {
	messages := []Message{
		{Role: "assistant"},
	}
	if backfillEmptyAssistantFromFinal(messages, "   \n\t", nil) {
		t.Fatalf("backfill must not fire for whitespace-only payload")
	}
	if len(messages[0].OrderedBlocks) != 0 {
		t.Fatalf("OrderedBlocks gained an empty block from whitespace input")
	}
}

func TestBackfillSkipsWhenLastNotAssistant(t *testing.T) {
	messages := []Message{
		{Role: "assistant", Content: "done"},
		{Role: "user", Content: "next question"},
	}
	if backfillEmptyAssistantFromFinal(messages, "final reply", nil) {
		t.Fatalf("backfill must not fire when last message isn't assistant")
	}
}

// TestLastAssistantContentScansBackward guards the AllMessages fallback used
// when ExecuteMessage returns an empty response string (common for multi-
// turn tool-heavy flows where the final SDK turn is tool_use only).
func TestLastAssistantContentScansBackward(t *testing.T) {
	tests := []struct {
		name string
		msgs []*Message
		want string
	}{
		{
			name: "finds last assistant text",
			msgs: []*Message{
				{Role: "user", Content: "hi"},
				{Role: "assistant", Content: "first answer"},
				{Role: "tool"},
				{Role: "assistant", Content: "final answer"},
			},
			want: "final answer",
		},
		{
			name: "skips trailing empty assistant (tool-only turn)",
			msgs: []*Message{
				{Role: "assistant", Content: "real reply"},
				{Role: "tool"},
				{Role: "assistant", Content: ""}, // tool_use only turn
			},
			want: "real reply",
		},
		{
			name: "ignores non-assistant roles",
			msgs: []*Message{
				{Role: "user", Content: "hello"},
				{Role: "system", Content: "sys"},
			},
			want: "",
		},
		{
			name: "empty slice",
			msgs: nil,
			want: "",
		},
		{
			name: "nil entries are tolerated",
			msgs: []*Message{
				{Role: "assistant", Content: "present"},
				nil,
			},
			want: "present",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := lastAssistantContent(tt.msgs)
			if got != tt.want {
				t.Fatalf("lastAssistantContent() = %q, want %q", got, tt.want)
			}
		})
	}
}

// TestBackfillWithAllMessagesSourceSimulatesHandlerFlow walks the full chain
// the agentResponseMsg handler runs: select finalContent from qm.content
// with an AllMessages fallback, then backfill. This guards the fix for the
// reported bug where the live UI renders an empty final reply but leaving
// and returning the conversation shows the content (because SDK persisted
// it, and AllMessages from agentResponseMsg mirrors that state).
func TestBackfillWithAllMessagesSourceSimulatesHandlerFlow(t *testing.T) {
	// Simulate in-memory TUI state: an assistant placeholder with tool blocks
	// already rendered but no text content.
	messages := []Message{
		{Role: "user", Content: "do the thing"},
		{
			Role: "assistant",
			OrderedBlocks: []MessageBlock{
				{Type: "tool_call", ToolCall: &ToolCallDisplay{ID: "tc_1", Name: "Read"}},
				{Type: "tool_result", ToolResult: &ToolResultDisplay{CallID: "tc_1", Output: "ok"}},
			},
		},
	}
	// Simulate the SDK snapshot handed to the handler — the true final reply
	// is in here even though ExecuteMessage returned an empty string.
	allMessages := []*Message{
		{Role: "user", Content: "do the thing"},
		{Role: "assistant", Content: "starting", ToolCalls: []ToolCallDisplay{{ID: "tc_1", Name: "Read"}}},
		{Role: "tool"},
		{Role: "assistant", Content: "all done — here is the answer"},
	}
	qmContent := "" // the bug: ExecuteMessage returned empty
	var qmErr error

	finalContent := qmContent
	if finalContent == "" {
		finalContent = lastAssistantContent(allMessages)
	}
	if !backfillEmptyAssistantFromFinal(messages, finalContent, qmErr) {
		t.Fatalf("expected backfill to fire using AllMessages fallback")
	}
	last := messages[len(messages)-1]
	if last.Content != "all done — here is the answer" {
		t.Fatalf("Content not backfilled from AllMessages: got %q", last.Content)
	}
	if n := len(last.OrderedBlocks); n != 3 {
		t.Fatalf("expected tool_call + tool_result + content = 3 blocks, got %d", n)
	}
	if last.OrderedBlocks[2].Type != "content" {
		t.Fatalf("expected final block type=content, got %q", last.OrderedBlocks[2].Type)
	}
}

// TestBackfillPreservesToolCallsWhenAddingContent verifies the common mixed
// case: the assistant emitted tool_call/tool_result blocks during the turn
// but the provider never streamed a closing text chunk. The fallback should
// append a trailing content block rather than replace the existing blocks.
func TestBackfillPreservesToolCallsWhenAddingContent(t *testing.T) {
	messages := []Message{
		{
			Role: "assistant",
			OrderedBlocks: []MessageBlock{
				{Type: "tool_call", ToolCall: &ToolCallDisplay{ID: "tc_1", Name: "Bash"}},
				{Type: "tool_result", ToolResult: &ToolResultDisplay{CallID: "tc_1", Output: "hi"}},
			},
			ToolCalls: []ToolCallDisplay{{ID: "tc_1", Name: "Bash"}},
			ToolResults: []ToolResultDisplay{
				{CallID: "tc_1", Output: "hi"},
			},
		},
	}
	if !backfillEmptyAssistantFromFinal(messages, "done!", nil) {
		t.Fatalf("expected backfill to fire for tool-only assistant with no text")
	}
	last := messages[len(messages)-1]
	if len(last.OrderedBlocks) != 3 {
		t.Fatalf("expected tool_call + tool_result + content = 3 blocks, got %d", len(last.OrderedBlocks))
	}
	if last.OrderedBlocks[0].Type != "tool_call" || last.OrderedBlocks[1].Type != "tool_result" {
		t.Fatalf("existing blocks reordered: %v", []string{last.OrderedBlocks[0].Type, last.OrderedBlocks[1].Type})
	}
	if last.OrderedBlocks[2].Type != "content" || last.OrderedBlocks[2].GetContent() != "done!" {
		t.Fatalf("final block wrong: type=%q content=%q",
			last.OrderedBlocks[2].Type, last.OrderedBlocks[2].GetContent())
	}
}

// Tests for finalizeAssistantFromComplete — wired to AssistantMessageUpdate.
// Current semantics delegate to backfillEmptyAssistantFromFinal: only fire
// when the current-turn content area is empty (tool-only turn, or a
// non-streaming single-shot response). Deliberately does NOT attempt
// prefix-repair or replace — per-turn finalization can't safely reconcile
// with in-flight deltas without introducing duplicates.

func TestFinalizeRepairsEmptyTurnUsingCompleteSignal(t *testing.T) {
	messages := []Message{
		{
			Role: "assistant",
			OrderedBlocks: []MessageBlock{
				{Type: "tool_call", ToolCall: &ToolCallDisplay{ID: "tc_1", Name: "Read"}},
				{Type: "tool_result", ToolResult: &ToolResultDisplay{CallID: "tc_1", Output: "ok"}},
			},
		},
	}
	if !finalizeAssistantFromComplete(messages, "here is the answer", nil) {
		t.Fatalf("expected finalize to backfill empty-content tool-only turn")
	}
	last := messages[0]
	if last.Content != "here is the answer" {
		t.Fatalf("Content not backfilled: %q", last.Content)
	}
	if len(last.OrderedBlocks) != 3 || last.OrderedBlocks[2].Type != "content" {
		t.Fatalf("expected trailing content block, got blocks=%d last=%q",
			len(last.OrderedBlocks), last.OrderedBlocks[len(last.OrderedBlocks)-1].Type)
	}
}

func TestFinalizeExtendsStreamedPrefixWithoutDuplicating(t *testing.T) {
	// Streaming delivered a prefix, then AssistantMessageUpdate arrived with the
	// authoritative full turn. Extend by suffix only so the final visible message
	// does not disappear/truncate and does not duplicate the prefix.
	messages := []Message{
		{
			Role: "assistant",
			OrderedBlocks: []MessageBlock{
				{Type: "content", Content: "Now I have a clear picture."},
			},
			Content: "Now I have a clear picture.",
		},
	}
	if !finalizeAssistantFromComplete(messages, "Now I have a clear picture. Let me design…", nil) {
		t.Fatalf("expected finalize to append missing suffix")
	}
	if len(messages[0].OrderedBlocks) != 1 {
		t.Fatalf("expected same content block, got %d", len(messages[0].OrderedBlocks))
	}
	if got := messages[0].OrderedBlocks[0].GetContent(); got != "Now I have a clear picture. Let me design…" {
		t.Fatalf("final content block mismatch: %q", got)
	}
	if got := messages[0].GetContent(); got != "Now I have a clear picture. Let me design…" {
		t.Fatalf("canonical content mismatch: %q", got)
	}
}

func TestFinalizeSkipsNonPrefixStreamedContent(t *testing.T) {
	messages := []Message{
		{
			Role:          "assistant",
			OrderedBlocks: []MessageBlock{{Type: "content", Content: "already streamed"}},
			Content:       "already streamed",
		},
	}
	if finalizeAssistantFromComplete(messages, "different final", nil) {
		t.Fatalf("finalize must not replace non-prefix streamed content")
	}
	if got := messages[0].OrderedBlocks[0].GetContent(); got != "already streamed" {
		t.Fatalf("streamed content clobbered: %q", got)
	}
}

func TestFinalizeSkipsOnErrorEmptyAndNonAssistant(t *testing.T) {
	msgs := []Message{{Role: "assistant"}}
	if finalizeAssistantFromComplete(msgs, "x", errors.New("boom")) {
		t.Fatalf("must skip on error")
	}
	if finalizeAssistantFromComplete(msgs, "   ", nil) {
		t.Fatalf("must skip on whitespace-only final")
	}
	nonAssistant := []Message{{Role: "user", Content: "hi"}}
	if finalizeAssistantFromComplete(nonAssistant, "reply", nil) {
		t.Fatalf("must skip when last message isn't assistant")
	}
}
