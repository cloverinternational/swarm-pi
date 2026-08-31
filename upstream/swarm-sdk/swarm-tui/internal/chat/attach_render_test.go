package chat

import "testing"

// TestAppendDaemonAssistantText verifies that streamed assistant text from an
// attached daemon is rendered into the trailing assistant message in the
// transcript — replace vs append semantics, and creation of a new assistant
// message when none is trailing.
func TestAppendDaemonAssistantText(t *testing.T) {
	app := &App{messages: []Message{}}

	// No trailing assistant message yet → one is created.
	app.appendDaemonAssistantText("Hello", false)
	if n := len(app.messages); n != 1 {
		t.Fatalf("expected 1 message after first content, got %d", n)
	}
	if app.messages[0].Role != "assistant" || app.messages[0].Content != "Hello" {
		t.Fatalf("expected assistant 'Hello', got role=%q content=%q",
			app.messages[0].Role, app.messages[0].Content)
	}

	// Append mode adds to the same message.
	app.appendDaemonAssistantText(" world", true)
	if got := app.messages[0].Content; got != "Hello world" {
		t.Fatalf("append mode: expected 'Hello world', got %q", got)
	}

	// Replace mode overwrites the trailing assistant message.
	app.appendDaemonAssistantText("replaced", false)
	if got := app.messages[0].Content; got != "replaced" {
		t.Fatalf("replace mode: expected 'replaced', got %q", got)
	}

	// A non-assistant trailing message forces a new assistant message.
	app.messages = append(app.messages, Message{Role: "user", Content: "hi"})
	app.appendDaemonAssistantText("next turn", true)
	if n := len(app.messages); n != 3 {
		t.Fatalf("expected 3 messages, got %d", n)
	}
	if last := app.messages[2]; last.Role != "assistant" || last.Content != "next turn" {
		t.Fatalf("expected new assistant 'next turn', got role=%q content=%q",
			last.Role, last.Content)
	}
}

// TestApplyDaemonEvent_FullStreamedTurn feeds the EXACT daemon /sse payload
// shapes through the parse→render path and asserts the transcript ends up
// correct — proving leg 2 (render the daemon's stream into the TUI transcript)
// end-to-end without needing a live daemon.
func TestApplyDaemonEvent_FullStreamedTurn(t *testing.T) {
	app := &App{messages: []Message{{Role: "user", Content: "say PONG"}}}

	// Sequence as emitted by the daemon serve /sse stream.
	events := []string{
		`{"kind":"stream_start"}`,
		`{"kind":"agent","agent":{"type":"content","update":{"Content":"PO","Append":true}}}`,
		`{"kind":"agent","agent":{"type":"content","update":{"Content":"NG","Append":true}}}`,
		`{"kind":"agent","agent":{"type":"tool_call","update":{"Name":"Bash"}}}`,
		`{"kind":"stream_end"}`,
	}
	changed := 0
	for _, e := range events {
		if app.applyDaemonEvent(e) {
			changed++
		}
	}
	if changed != len(events) {
		t.Fatalf("expected all %d events to mutate/refresh, got %d", len(events), changed)
	}

	// Transcript: user, assistant("PONG"), tool("→ Bash").
	if n := len(app.messages); n != 3 {
		var roles []string
		for _, m := range app.messages {
			roles = append(roles, m.Role+":"+m.Content)
		}
		t.Fatalf("expected 3 messages, got %d: %v", n, roles)
	}
	if app.messages[1].Role != "assistant" || app.messages[1].Content != "PONG" {
		t.Fatalf("expected assistant 'PONG', got role=%q content=%q",
			app.messages[1].Role, app.messages[1].Content)
	}
	if app.messages[2].Role != "tool" || app.messages[2].Content != "→ Bash" {
		t.Fatalf("expected tool '→ Bash', got role=%q content=%q",
			app.messages[2].Role, app.messages[2].Content)
	}
}

// TestApplyDaemonEvent_IgnoresGarbage proves malformed/unknown events do not
// mutate the transcript or panic.
func TestApplyDaemonEvent_IgnoresGarbage(t *testing.T) {
	app := &App{messages: []Message{}}
	for _, e := range []string{
		`not json at all`,
		`{"kind":"thinking"}`,
		`{"kind":"agent","agent":{"type":"token_count","update":{"InputTokens":5}}}`,
		`{"kind":"agent","agent":{"type":"tool_call","update":{"Name":""}}}`,
	} {
		if app.applyDaemonEvent(e) {
			t.Fatalf("event %q should not have mutated the transcript", e)
		}
	}
	if len(app.messages) != 0 {
		t.Fatalf("expected no messages, got %d", len(app.messages))
	}
}
