package main

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/vocab"
)

func TestStreamAnalyzerJoinsFailureToNextReaction(t *testing.T) {
	prose, _ := vocab.Embedded("prose")
	thinking, _ := vocab.Embedded("thinking")
	var out bytes.Buffer
	a := analyzer{
		prose: prose, thinking: thinking,
		calls: map[string]string{}, out: json.NewEncoder(&out),
	}
	var call streamEvent
	call.Type = "assistant"
	call.Message.Model = "gpt-5.6-sol"
	call.Message.Content = []block{{Type: "tool_use", ID: "call-1", Name: "apply_patch"}}
	a.consume(call)

	var failed streamEvent
	failed.Type, failed.SessionID = "user", "session-1"
	failed.Message.Content = []block{{
		Type: "tool_result", ToolUseID: "call-1", IsError: true,
		Content: `{"error":{"type":"tools.runtime.execution_failed","message":"context not found"}}`,
	}}
	a.consume(failed)

	var reaction streamEvent
	reaction.Type = "assistant"
	reaction.Message.Model = "gpt-5.6-sol"
	reaction.Message.Content = []block{
		{Type: "thinking", Thinking: "Perhaps split this into smaller atomic patches."},
		{Type: "text", Text: "The context did not match. Let me narrow the patch."},
		{Type: "tool_use", ID: "call-2", Name: "Read"},
	}
	a.consume(reaction)

	var success streamEvent
	success.Type, success.SessionID = "user", "session-1"
	success.Message.Content = []block{{
		Type: "tool_result", ToolUseID: "call-2", IsError: false, Content: "ok",
	}}
	a.consume(success) // closes and flushes the prior reaction

	if a.n != 1 {
		t.Fatalf("want one observation, got %d", a.n)
	}
	if bytes.Contains(out.Bytes(), []byte("context did not match")) ||
		bytes.Contains(out.Bytes(), []byte("smaller atomic patches")) {
		t.Fatalf("raw language leaked: %s", out.String())
	}
	var got observation
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Tool != "apply_patch" || got.NextTool != "Read" ||
		got.ErrorType != "tools.runtime.execution_failed" {
		t.Fatalf("join/provenance wrong: %+v", got)
	}
	if got.Terminal {
		t.Fatal("reaction followed by another tool must not be terminal")
	}
	if got.Prose.ElevatedHits == 0 || got.Thinking.ElevatedHits == 0 {
		t.Fatalf("channel scores missing: prose=%+v thinking=%+v", got.Prose, got.Thinking)
	}
}

func TestStreamAnalyzerFlushesFinalReactionAtEOF(t *testing.T) {
	prose, _ := vocab.Embedded("prose")
	thinking, _ := vocab.Embedded("thinking")
	var out bytes.Buffer
	a := analyzer{prose: prose, thinking: thinking, calls: map[string]string{"c": "Bash"}, out: json.NewEncoder(&out)}
	var fail streamEvent
	fail.Type = "user"
	fail.Message.Content = []block{{
		Type: "tool_result", ToolUseID: "c", IsError: true,
		Content: `{"error":{"type":"tool.blocked_by_hook","message":"blocked by hook"}}`,
	}}
	a.consume(fail)
	var final streamEvent
	final.Type = "assistant"
	final.Message.Content = []block{{Type: "text", Text: "The policy hook rejected the command."}}
	a.consume(final)
	a.flush(true)
	var got observation
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if !got.Terminal || !got.HookBlock {
		t.Fatalf("EOF/hook stratum lost: %+v", got)
	}
}
