package builtin

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/hooks"
)

func TestVocabularyObserverCorrelatesAndStoresScoresOnly(t *testing.T) {
	dir := t.TempDir()
	ledger := filepath.Join(dir, "observations.jsonl")
	t.Setenv("SWARM_VOCAB_LEDGER", ledger)
	transcript := filepath.Join(dir, "conversation.json")
	if err := os.WriteFile(transcript, []byte(`{"messages":[
	  {"id":"a1","role":"assistant","model":"gpt-5.6-sol",
	   "timestamp":"2026-08-05T20:00:00Z",
	   "thinking":"Perhaps split this into smaller atomic patches.",
	   "content":"The context did not match. Let me narrow the patch."}
	]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	hooks.SetTranscriptPathResolver(func(string) string { return transcript })
	defer hooks.SetTranscriptPathResolver(nil)
	h := NewVocabularyObserverHook()
	fail := hooks.Event{
		Type:           hooks.EventToolAfterExecute,
		ConversationID: "c1",
		Data: map[string]any{
			"tool_name": "apply_patch",
			"error": map[string]any{
				"type": "tools.runtime.execution_failed", "message": "context not found",
			},
		},
	}
	if result, err := h.OnEvent(context.Background(), fail); err != nil || result.Action != hooks.ActionContinue {
		t.Fatalf("failure event perturbed execution: result=%+v err=%v", result, err)
	}
	if _, err := h.OnEvent(context.Background(), hooks.Event{
		Type: hooks.EventToolBeforeExecute, ConversationID: "c1",
	}); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(ledger)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "context did not match") ||
		strings.Contains(string(raw), "smaller atomic patches") {
		t.Fatalf("raw prose/thinking leaked into telemetry: %s", raw)
	}
	var got vocabularyObservation
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	if got.Conversation != "c1" || got.Model != "gpt-5.6-sol" ||
		got.ErrorType != "tools.runtime.execution_failed" {
		t.Fatalf("provenance missing: %+v", got)
	}
	if got.Prose.ElevatedHits == 0 || got.Thinking.ElevatedHits == 0 {
		t.Fatalf("scores missing: prose=%+v thinking=%+v", got.Prose, got.Thinking)
	}
	h.mu.Lock()
	_, stillPending := h.pending["c1"]
	h.mu.Unlock()
	if stillPending {
		t.Fatal("pending event not cleared after durable observation")
	}
}

func TestVocabularyObserverPolicyBlockAndTranscriptLag(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("SWARM_VOCAB_LEDGER", filepath.Join(dir, "ledger.jsonl"))
	hooks.SetTranscriptPathResolver(func(string) string { return filepath.Join(dir, "not-yet.json") })
	defer hooks.SetTranscriptPathResolver(nil)
	h := NewVocabularyObserverHook()
	_, _ = h.OnEvent(context.Background(), hooks.Event{
		Type:           hooks.EventToolAfterExecute,
		ConversationID: "c2",
		Data: map[string]any{
			"tool_name": "Bash",
			"error": map[string]any{
				"type": "tool.blocked_by_hook", "message": "blocked by hook: bare sleep",
			},
		},
	})
	_, _ = h.OnEvent(context.Background(), hooks.Event{
		Type: hooks.EventAgentStopped, ConversationID: "c2",
	})
	h.mu.Lock()
	got, ok := h.pending["c2"]
	h.mu.Unlock()
	if !ok || !got.HookBlock {
		t.Fatalf("policy block stratum or pending retry lost: %+v ok=%v", got, ok)
	}
	if _, err := os.Stat(h.ledger); !os.IsNotExist(err) {
		t.Fatalf("lagged transcript should not create ledger: %v", err)
	}
}

func TestVocabularyObserverIgnoresSuccess(t *testing.T) {
	h := NewVocabularyObserverHook()
	_, _ = h.OnEvent(context.Background(), hooks.Event{
		Type:           hooks.EventToolAfterExecute,
		ConversationID: "ok",
		Data:           map[string]any{"tool_name": "Read", "result": map[string]any{"success": true}},
	})
	h.mu.Lock()
	defer h.mu.Unlock()
	if len(h.pending) != 0 {
		t.Fatalf("successful tool created pending failure: %+v", h.pending)
	}
}
