package hooks

import (
	"encoding/json"
	"testing"
)

func decodeClaudeInput(t *testing.T, h *ShellHook, ev Event) map[string]any {
	t.Helper()
	var got map[string]any
	if err := json.Unmarshal(h.buildClaudeCodeInputImpl(ev), &got); err != nil {
		t.Fatalf("unmarshal hook input: %v", err)
	}
	return got
}

func TestTranscriptPathEmptyWithoutResolver(t *testing.T) {
	SetTranscriptPathResolver(nil)
	h := NewShellHook("t", "true", []string{EventToolBeforeExecute})
	got := decodeClaudeInput(t, h, Event{ConversationID: "conv-1", Data: map[string]any{}})
	if got["transcript_path"] != "" {
		t.Fatalf("want empty transcript_path, got %q", got["transcript_path"])
	}
}

func TestTranscriptPathUsesResolver(t *testing.T) {
	SetTranscriptPathResolver(func(id string) string { return "/store/" + id + ".json" })
	defer SetTranscriptPathResolver(nil)
	h := NewShellHook("t", "true", []string{EventToolBeforeExecute})
	got := decodeClaudeInput(t, h, Event{ConversationID: "conv-1", Data: map[string]any{}})
	if got["transcript_path"] != "/store/conv-1.json" {
		t.Fatalf("resolver not used, got %q", got["transcript_path"])
	}
}

func TestTranscriptPathProducerValueWins(t *testing.T) {
	SetTranscriptPathResolver(func(string) string { return "/resolver.json" })
	defer SetTranscriptPathResolver(nil)
	h := NewShellHook("t", "true", []string{EventToolBeforeExecute})
	ev := Event{ConversationID: "conv-1", Data: map[string]any{"transcript_path": "/explicit.json"}}
	if got := decodeClaudeInput(t, h, ev); got["transcript_path"] != "/explicit.json" {
		t.Fatalf("producer value should win, got %q", got["transcript_path"])
	}
}

// A resolver that cannot find the conversation must degrade to "" rather than
// leaking a sentinel, and an empty conversation id must not reach the resolver.
func TestTranscriptPathDegradesCleanly(t *testing.T) {
	called := false
	SetTranscriptPathResolver(func(string) string { called = true; return "" })
	defer SetTranscriptPathResolver(nil)
	h := NewShellHook("t", "true", []string{EventToolBeforeExecute})

	if got := decodeClaudeInput(t, h, Event{ConversationID: "missing"}); got["transcript_path"] != "" {
		t.Fatalf("want empty for unresolvable conversation, got %q", got["transcript_path"])
	}
	if !called {
		t.Fatal("resolver should be consulted for a non-empty conversation id")
	}

	called = false
	if got := decodeClaudeInput(t, h, Event{}); got["transcript_path"] != "" {
		t.Fatalf("want empty for missing conversation id, got %q", got["transcript_path"])
	}
	if called {
		t.Fatal("resolver must not be consulted for an empty conversation id")
	}
}
