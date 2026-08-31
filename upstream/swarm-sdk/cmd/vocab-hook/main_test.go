package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func raw(value string) json.RawMessage { return json.RawMessage(value) }

func TestLifecycleCorrelatesFailureWithNextAssistantTurn(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("SWARM_VOCAB_STATE_DIR", dir)
	t.Setenv("SWARM_VOCAB_LEDGER", filepath.Join(dir, "ledger.jsonl"))
	transcript := filepath.Join(dir, "conversation.json")

	handlePost(payload{
		HookEventName: "PostToolUse",
		SessionID:     "conv-1",
		ToolName:      "apply_patch",
		CWD:           "/repo",
		Error: raw(`{"type":"tools.runtime.execution_failed",
			"message":"context not found"}`),
	})
	if _, err := os.Stat(pendingPath("conv-1")); err != nil {
		t.Fatalf("pending failure not written: %v", err)
	}

	writeTranscript(t, transcript, `{
	  "messages":[
	    {"id":"u1","role":"user","content":"change it"},
	    {"id":"a1","role":"assistant","model":"claude-opus-5",
	     "timestamp":"2026-08-05T20:00:00Z",
	     "thinking":"Perhaps I should narrow this to smaller atomic patches.",
	     "content":"The context did not match. Let me narrow the patch."}
	  ]}`)
	handleReaction(payload{
		HookEventName: "PreToolUse",
		SessionID:     "conv-1",
		Transcript:    transcript,
		ToolName:      "Read",
	})

	if _, err := os.Stat(pendingPath("conv-1")); !os.IsNotExist(err) {
		t.Fatalf("pending record not cleared after observation: %v", err)
	}
	lines := ledgerLines(t, filepath.Join(dir, "ledger.jsonl"))
	if len(lines) != 1 {
		t.Fatalf("want one observation, got %d", len(lines))
	}
	var got observation
	if err := json.Unmarshal([]byte(lines[0]), &got); err != nil {
		t.Fatal(err)
	}
	if got.Tool != "apply_patch" || got.ErrorType != "tools.runtime.execution_failed" {
		t.Fatalf("failure provenance lost: %+v", got)
	}
	if got.Model != "claude-opus-5" || got.AssistantID != "a1" {
		t.Fatalf("assistant provenance lost: %+v", got)
	}
	if got.Prose.ElevatedHits == 0 || got.Thinking.ElevatedHits == 0 {
		t.Fatalf("channel scoring missing: prose=%+v thinking=%+v", got.Prose, got.Thinking)
	}
	if got.Terminal {
		t.Fatal("PreToolUse reaction must not be terminal")
	}
}

func TestStopCapturesFinalReactionAndHookBlockStratum(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("SWARM_VOCAB_STATE_DIR", dir)
	t.Setenv("SWARM_VOCAB_LEDGER", filepath.Join(dir, "ledger.jsonl"))
	transcript := filepath.Join(dir, "conversation.json")
	writeTranscript(t, transcript, `{"messages":[
	  {"id":"a2","role":"assistant","model":"gpt-5.6-sol",
	   "timestamp":"2026-08-05T20:01:00Z",
	   "thinking":"The hook blocked a valid bounded command.",
	   "content":"I cannot continue because the policy hook rejected the bounded command."}
	]}`)

	handlePost(payload{
		HookEventName: "PostToolUse",
		SessionID:     "conv-2",
		ToolName:      "Bash",
		Error:         raw(`{"type":"tool.blocked_by_hook","message":"Blocked by hook: bare sleep"}`),
	})
	handleReaction(payload{HookEventName: "Stop", SessionID: "conv-2", Transcript: transcript})
	var got observation
	if err := json.Unmarshal([]byte(ledgerLines(t, filepath.Join(dir, "ledger.jsonl"))[0]), &got); err != nil {
		t.Fatal(err)
	}
	if !got.Terminal || !got.HookBlock {
		t.Fatalf("terminal/hook stratum lost: %+v", got)
	}
}

func TestSuccessDoesNotCreatePendingRecord(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("SWARM_VOCAB_STATE_DIR", dir)
	handlePost(payload{
		HookEventName: "PostToolUse",
		SessionID:     "conv-ok",
		ToolName:      "Read",
		ToolResponse:  raw(`{"success":true,"output":"ok"}`),
	})
	if _, err := os.Stat(pendingPath("conv-ok")); !os.IsNotExist(err) {
		t.Fatalf("success created pending failure: %v", err)
	}
}

func TestMissingTranscriptPreservesPendingForRetry(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("SWARM_VOCAB_STATE_DIR", dir)
	handlePost(payload{
		HookEventName: "PostToolUse",
		SessionID:     "conv-lag",
		ToolName:      "grep",
		ToolResponse:  raw(`{"is_error":true,"output":"too large"}`),
	})
	handleReaction(payload{
		HookEventName: "PreToolUse",
		SessionID:     "conv-lag",
		Transcript:    filepath.Join(dir, "not-yet-written.json"),
	})
	if _, err := os.Stat(pendingPath("conv-lag")); err != nil {
		t.Fatalf("lagged transcript should preserve pending: %v", err)
	}
}

func TestContentBlocksAndErrorShapes(t *testing.T) {
	got := contentText(raw(`[{"type":"thinking","text":"hidden"},{"type":"text","text":"visible"}]`))
	if got != "visible" {
		t.Fatalf("content blocks wrong: %q", got)
	}
	cases := []payload{
		{Error: raw(`"plain error"`)},
		{ToolResponse: raw(`{"error":{"type":"output_too_large","message":"large"}}`)},
		{ToolResponse: raw(`{"success":false,"output":"failed"}`)},
		{ToolResponse: raw(`{"isError":true,"output":"failed"}`)},
	}
	for i, p := range cases {
		if failed, _, _ := classifyError(p); !failed {
			t.Errorf("case %d not classified as failure", i)
		}
	}
}

func writeTranscript(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func ledgerLines(t *testing.T, path string) []string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return strings.FieldsFunc(strings.TrimSpace(string(raw)), func(r rune) bool { return r == '\n' })
}
