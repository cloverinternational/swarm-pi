package chat

import "testing"

func TestCollapseContentOnlyBlocksToFinalReplacesStaleContent(t *testing.T) {
	msg := &Message{
		OrderedBlocks: []MessageBlock{
			{Type: "content", Content: "#"},
		},
	}

	changed := collapseContentOnlyBlocksToFinal(msg, "# Mode: ACT\nHello world", 42)
	if !changed {
		t.Fatalf("expected ordered blocks to be reconciled")
	}
	if len(msg.OrderedBlocks) != 1 {
		t.Fatalf("expected one ordered block after reconcile, got %d", len(msg.OrderedBlocks))
	}
	if msg.OrderedBlocks[0].Type != "content" {
		t.Fatalf("expected content block, got %s", msg.OrderedBlocks[0].Type)
	}
	if msg.OrderedBlocks[0].Content != "# Mode: ACT\nHello world" {
		t.Fatalf("unexpected content after reconcile: %q", msg.OrderedBlocks[0].Content)
	}
	if msg.OrderedBlocks[0].Sequence != 42 {
		t.Fatalf("expected sequence 42, got %d", msg.OrderedBlocks[0].Sequence)
	}
}

func TestCollapseContentOnlyBlocksToFinalSkipsStructuredBlocks(t *testing.T) {
	msg := &Message{
		OrderedBlocks: []MessageBlock{
			{Type: "content", Content: "partial"},
			{Type: "tool_call", ToolCall: &ToolCallDisplay{ID: "call_1", Name: "Read"}},
		},
	}

	changed := collapseContentOnlyBlocksToFinal(msg, "final content", 7)
	if changed {
		t.Fatalf("expected structured ordered blocks to be preserved")
	}
	if len(msg.OrderedBlocks) != 2 {
		t.Fatalf("expected ordered blocks to remain unchanged, got %d", len(msg.OrderedBlocks))
	}
}

func TestCollapseContentOnlyBlocksToFinalNoopWhenAlreadyFinal(t *testing.T) {
	msg := &Message{
		OrderedBlocks: []MessageBlock{
			{Type: "content", Content: "final"},
		},
	}

	changed := collapseContentOnlyBlocksToFinal(msg, "final", 99)
	if changed {
		t.Fatalf("expected no change when ordered block already matches final content")
	}
	if len(msg.OrderedBlocks) != 1 || msg.OrderedBlocks[0].Content != "final" {
		t.Fatalf("ordered blocks were unexpectedly modified")
	}
}

func TestShouldIgnoreLateAssistantTextUpdate(t *testing.T) {
	tests := []struct {
		name     string
		messages []Message
		want     bool
	}{
		{
			name:     "no messages",
			messages: nil,
			want:     false,
		},
		{
			name: "last message is user",
			messages: []Message{
				{Role: "assistant", IsComplete: true},
				{Role: "user", IsComplete: false},
			},
			want: false,
		},
		{
			name: "assistant not complete",
			messages: []Message{
				{Role: "assistant", IsComplete: false},
			},
			want: false,
		},
		{
			name: "assistant complete",
			messages: []Message{
				{Role: "assistant", IsComplete: true},
			},
			want: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := shouldIgnoreLateAssistantTextUpdate(tt.messages)
			if got != tt.want {
				t.Fatalf("shouldIgnoreLateAssistantTextUpdate() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestLatestAssistantContent(t *testing.T) {
	messages := []*Message{
		{Role: "user", Content: "hello"},
		{Role: "assistant", Content: ""},
		{Role: "assistant", Content: "  full answer  "},
	}

	got := latestAssistantContent(messages)
	if got != "full answer" {
		t.Fatalf("latestAssistantContent() = %q, want %q", got, "full answer")
	}
}

func TestHasStructuredAssistantActivity(t *testing.T) {
	if hasStructuredAssistantActivity(nil) {
		t.Fatalf("expected nil message to be non-structured")
	}

	flat := &Message{
		Role: "assistant",
		OrderedBlocks: []MessageBlock{
			{Type: "content", Content: "story"},
		},
	}
	if hasStructuredAssistantActivity(flat) {
		t.Fatalf("expected content-only message to be non-structured")
	}

	structured := &Message{
		Role: "assistant",
		OrderedBlocks: []MessageBlock{
			{Type: "content", Content: "thinking"},
			{Type: "tool_call", ToolCall: &ToolCallDisplay{ID: "c1", Name: "Read"}},
		},
	}
	if !hasStructuredAssistantActivity(structured) {
		t.Fatalf("expected tool activity to be structured")
	}
}

func TestResolveFinalAssistantContentPrefersHistoryForFlatMessages(t *testing.T) {
	current := &Message{
		Role:    "assistant",
		Content: "In",
		OrderedBlocks: []MessageBlock{
			{Type: "content", Content: "In"},
		},
	}
	history := []*Message{
		{Role: "user", Content: "tell me a story"},
		{Role: "assistant", Content: "In a town that never put names on its streets..."},
	}

	got := resolveFinalAssistantContent(current, "In", history)
	want := "In a town that never put names on its streets..."
	if got != want {
		t.Fatalf("resolveFinalAssistantContent() = %q, want %q", got, want)
	}
}

func TestResolveFinalAssistantContentKeepsDirectForStructuredMessages(t *testing.T) {
	current := &Message{
		Role: "assistant",
		OrderedBlocks: []MessageBlock{
			{Type: "content", Content: "pre"},
			{Type: "tool_call", ToolCall: &ToolCallDisplay{ID: "c1", Name: "Read"}},
		},
	}
	history := []*Message{
		{Role: "assistant", Content: "final-only-from-history"},
	}

	got := resolveFinalAssistantContent(current, "direct-final", history)
	if got != "direct-final" {
		t.Fatalf("resolveFinalAssistantContent() = %q, want direct-final", got)
	}
}

func TestResolveFinalAssistantContentKeepsLongerDirectWhenHistoryIsShorter(t *testing.T) {
	current := &Message{
		Role: "assistant",
		OrderedBlocks: []MessageBlock{
			{Type: "content", Content: "In"},
		},
	}
	direct := "In a town that never put names on its streets..."
	history := []*Message{
		{Role: "assistant", Content: "In"},
	}

	got := resolveFinalAssistantContent(current, direct, history)
	if got != direct {
		t.Fatalf("resolveFinalAssistantContent() = %q, want direct content", got)
	}
}

func TestResolveFinalAssistantContentUsesHistoryWhenDirectEmpty(t *testing.T) {
	current := &Message{
		Role: "assistant",
		OrderedBlocks: []MessageBlock{
			{Type: "content", Content: ""},
		},
	}
	history := []*Message{
		{Role: "assistant", Content: "Recovered from history"},
	}

	got := resolveFinalAssistantContent(current, "", history)
	if got != "Recovered from history" {
		t.Fatalf("resolveFinalAssistantContent() = %q, want history fallback", got)
	}
}
