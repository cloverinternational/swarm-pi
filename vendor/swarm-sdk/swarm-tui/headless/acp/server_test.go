package acp

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// ── Protocol type tests ───────────────────────────────────────────────────────

func TestRequestIsNotification(t *testing.T) {
	cases := []struct {
		raw     string
		wantNot bool
	}{
		{`{"jsonrpc":"2.0","method":"session/cancel","params":{}}`, true},
		{`{"jsonrpc":"2.0","id":1,"method":"session/new","params":{}}`, false},
		{`{"jsonrpc":"2.0","id":null,"method":"session/cancel"}`, true},
	}
	for _, tc := range cases {
		var req Request
		if err := json.Unmarshal([]byte(tc.raw), &req); err != nil {
			t.Fatalf("unmarshal %q: %v", tc.raw, err)
		}
		if got := req.IsNotification(); got != tc.wantNot {
			t.Errorf("IsNotification(%q) = %v, want %v", tc.raw, got, tc.wantNot)
		}
	}
}

func TestSessionNewResult_JSON(t *testing.T) {
	res := &SessionNewResult{
		SessionID: "sess_abc123",
		Modes: SessionModes{
			CurrentModeID:  "code",
			AvailableModes: availableModes,
		},
	}
	data, err := json.Marshal(res)
	if err != nil {
		t.Fatal(err)
	}
	var back SessionNewResult
	if err := json.Unmarshal(data, &back); err != nil {
		t.Fatal(err)
	}
	if back.SessionID != "sess_abc123" {
		t.Errorf("sessionId = %q, want sess_abc123", back.SessionID)
	}
	if len(back.Modes.AvailableModes) != 3 {
		t.Errorf("availableModes len = %d, want 3", len(back.Modes.AvailableModes))
	}
}

func TestSessionUpdate_JSON(t *testing.T) {
	content, _ := json.Marshal(MessageContent{Type: "text", Text: "hello"})
	update := SessionUpdate{
		SessionUpdateType: "agent_message_chunk",
		Content:           content,
	}
	data, err := json.Marshal(update)
	if err != nil {
		t.Fatal(err)
	}
	s := string(data)
	if !strings.Contains(s, `"sessionUpdate":"agent_message_chunk"`) {
		t.Errorf("missing sessionUpdate field: %s", s)
	}
	if !strings.Contains(s, `"text":"hello"`) {
		t.Errorf("missing text field: %s", s)
	}
}

func TestBuildPromptText(t *testing.T) {
	items := []PromptContent{
		{Type: "text", Text: "Analyze this:"},
		{Type: "resource", Resource: &PromptResource{
			URI:      "file:///src/main.go",
			MimeType: "text/x-go",
			Text:     "package main",
		}},
	}
	got := buildPromptText(items)
	if !strings.HasPrefix(got, "Analyze this:") {
		t.Errorf("expected text prefix: %q", got)
	}
	if !strings.Contains(got, "```go file:///src/main.go") {
		t.Errorf("expected resource fence: %q", got)
	}
	if !strings.Contains(got, "package main") {
		t.Errorf("expected resource content: %q", got)
	}
}

func TestToolKindFromName(t *testing.T) {
	cases := map[string]string{
		"ReadFile":   "read",
		"WriteFile":  "write",
		"EditFile":   "write",
		"DeleteFile": "delete",
		"BashExec":   "execute",
		"RunCommand": "execute",
		"ListDir":    "read",
		"Unknown":    "read",
	}
	for name, want := range cases {
		if got := toolKindFromName(name); got != want {
			t.Errorf("toolKindFromName(%q) = %q, want %q", name, got, want)
		}
	}
}

func TestMimeToLang(t *testing.T) {
	cases := map[string]string{
		"text/x-go":        "go",
		"text/x-python":    "python",
		"application/json": "json",
		"text/html":        "html",
		"unknown/type":     "",
	}
	for mime, want := range cases {
		if got := mimeToLang(mime); got != want {
			t.Errorf("mimeToLang(%q) = %q, want %q", mime, got, want)
		}
	}
}

// ── Session registry tests ────────────────────────────────────────────────────

func TestSessionRegistry(t *testing.T) {
	reg := newSessionRegistry()

	sess := newSession("conv-1", "/project")
	if sess.id == "" {
		t.Fatal("session ID must not be empty")
	}
	if !strings.HasPrefix(sess.id, "sess_") {
		t.Errorf("session ID must start with sess_: %s", sess.id)
	}

	reg.add(sess)
	got, ok := reg.get(sess.id)
	if !ok || got != sess {
		t.Error("get after add failed")
	}

	reg.remove(sess.id)
	_, ok = reg.get(sess.id)
	if ok {
		t.Error("get after remove should return false")
	}
}

func TestSessionPromptConcurrency(t *testing.T) {
	sess := newSession("conv-1", "/project")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ch, err := sess.startPrompt(cancel)
	if err != nil {
		t.Fatalf("startPrompt: %v", err)
	}

	// Second call should fail.
	_, err = sess.startPrompt(cancel)
	if err == nil {
		t.Error("second startPrompt should return error")
	}

	// Finish the prompt.
	sess.finishPrompt(promptResult{stopReason: "end_turn"})
	result := <-ch
	if result.stopReason != "end_turn" {
		t.Errorf("stopReason = %q, want end_turn", result.stopReason)
	}

	_ = ctx
}

// ── Wire serialisation test ───────────────────────────────────────────────────

func TestServerWritesValidJSON(t *testing.T) {
	var buf bytes.Buffer
	// We only test the write helpers, not the full engine setup.
	s := &Server{writer: &buf}
	msgContent, _ := json.Marshal(MessageContent{Type: "text", Text: "hi"})
	s.writeNotification("session/update", SessionUpdateParams{
		SessionID: "sess_abc",
		Update: SessionUpdate{
			SessionUpdateType: "agent_message_chunk",
			Content:           msgContent,
		},
	})

	line := strings.TrimSpace(buf.String())
	var msg struct {
		JSONRPC string `json:"jsonrpc"`
		Method  string `json:"method"`
	}
	if err := json.Unmarshal([]byte(line), &msg); err != nil {
		t.Fatalf("invalid JSON: %v\nraw: %s", err, line)
	}
	if msg.JSONRPC != "2.0" {
		t.Errorf("jsonrpc = %q, want 2.0", msg.JSONRPC)
	}
	if msg.Method != "session/update" {
		t.Errorf("method = %q, want session/update", msg.Method)
	}
}

func TestHandleSessionSetConfigRejectsInvalidMode(t *testing.T) {
	s := NewServer(ServerConfig{})
	sess := newSession("conv-1", "/project")
	s.sessions.add(sess)

	raw, err := json.Marshal(SessionSetConfigParams{
		SessionID: sess.id,
		Updates: map[string]string{
			"mode": "nope",
		},
	})
	if err != nil {
		t.Fatalf("marshal params: %v", err)
	}

	_, rpcErr := s.handleSessionSetConfig(raw)
	if rpcErr == nil {
		t.Fatal("expected error for invalid mode")
	}
	if rpcErr.Code != ErrInvalidParams {
		t.Fatalf("error code = %d, want %d", rpcErr.Code, ErrInvalidParams)
	}
}

func TestHandleSessionCancelSkipsNonActiveSession(t *testing.T) {
	s := NewServer(ServerConfig{})
	active := newSession("conv-1", "/project")
	other := newSession("conv-2", "/project")
	s.sessions.add(active)
	s.sessions.add(other)

	if !s.setActiveSession(active.id) {
		t.Fatal("failed to set active session")
	}

	var otherCanceled bool
	if _, err := other.startPrompt(func() { otherCanceled = true }); err != nil {
		t.Fatalf("startPrompt: %v", err)
	}

	raw, err := json.Marshal(SessionCancelParams{SessionID: other.id})
	if err != nil {
		t.Fatalf("marshal params: %v", err)
	}
	s.handleSessionCancel(raw)

	if otherCanceled {
		t.Fatal("expected non-active session cancel to be ignored")
	}
}
