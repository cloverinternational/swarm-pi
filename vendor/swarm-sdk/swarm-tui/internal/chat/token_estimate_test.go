package chat

import "testing"

// TestRoughTokenCountForMessageCountsToolPayloads guards the fix for the
// sidebar "jump" bug: tool-result messages had zero Content but carried large
// Output in ToolResults, and the prior estimator only counted Content — so
// the sidebar drastically under-reported between API calls. The block-aware
// estimator must see tool-call arguments, tool-result output, thinking, and
// images.
func TestRoughTokenCountForMessageCountsToolPayloads(t *testing.T) {
	msg := Message{
		Role:     "assistant",
		Content:  "",
		Thinking: "deliberation text about what to do next",
		ToolCalls: []ToolCallDisplay{{
			ID:   "call_1",
			Name: "Read",
			Parameters: map[string]any{
				"file_path": "/some/path/to/a/reasonably/long/file/name.go",
				"offset":    100,
			},
		}},
	}
	if got := roughTokenCountForMessage(msg); got == 0 {
		t.Fatalf("expected tool-call + thinking to count, got 0")
	}

	toolResult := Message{
		Role:    "tool",
		Content: "",
		ToolResults: []ToolResultDisplay{{
			CallID:   "call_1",
			ToolName: "Read",
			Output:   "line 1\nline 2\nline 3\n" + string(make([]byte, 4000)),
		}},
	}
	got := roughTokenCountForMessage(toolResult)
	if got < 800 {
		t.Fatalf("expected tool-result output (~4k chars) to yield ≥800 tokens, got %d", got)
	}
}

// TestTokenCountWithEstimationAnchorsOnLastUsage verifies the Claude Code
// style anchor behavior: when an assistant message carries real InputTokens,
// subsequent rough estimates are added on top, and we do not re-estimate
// history the API has already counted.
func TestTokenCountWithEstimationAnchorsOnLastUsage(t *testing.T) {
	messages := []Message{
		{Role: "user", Content: "a very long prompt " + string(make([]byte, 2000))},
		{Role: "assistant", Content: "ok", InputTokens: 5000, OutputTokens: 50},
		{Role: "tool", ToolResults: []ToolResultDisplay{{Output: string(make([]byte, 800))}}},
	}
	got := tokenCountWithEstimation(messages)
	if got < 5050 {
		t.Fatalf("expected anchored base (≥5050) plus tail estimate, got %d", got)
	}
	if got > 5050+400 {
		t.Fatalf("expected tail (~200 tokens for 800-byte output) to be added, got %d", got)
	}
}

// TestTokenCountWithEstimationFallback: no anchor → rough-estimate full slice.
func TestTokenCountWithEstimationFallback(t *testing.T) {
	messages := []Message{
		{Role: "user", Content: "hello world"},
		{Role: "assistant", Content: "hi"},
	}
	if got := tokenCountWithEstimation(messages); got == 0 {
		t.Fatalf("expected non-zero fallback estimate, got 0")
	}
}

// TestOldEstimatorMissesToolResults documents the bug we fixed: the prior
// implementation (msg.Content-only) would return 0 for a tool-result message
// regardless of Output size. We keep this as a signpost — if a regression
// reintroduces the blind-spot, this test fails first.
func TestOldEstimatorMissesToolResults(t *testing.T) {
	msg := Message{
		Role:    "tool",
		Content: "",
		ToolResults: []ToolResultDisplay{{
			Output: string(make([]byte, 10000)),
		}},
	}
	if got := estimateTokens(msg.Content); got != 0 {
		t.Fatalf("Content-only estimate should be 0 (that was the bug), got %d", got)
	}
	if got := roughTokenCountForMessage(msg); got < 2000 {
		t.Fatalf("block-aware estimate must see 10k-char tool output (≥2000 tokens), got %d", got)
	}
}
