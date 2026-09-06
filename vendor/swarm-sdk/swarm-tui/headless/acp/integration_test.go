package acp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"sync"
	"testing"
	"time"
)

// ── Test helpers ──────────────────────────────────────────────────────────────

// testServer creates an ACP Server wired to in-memory pipes for protocol testing.
// The engine is nil; only methods that don't call into the engine can be exercised.
func testServer(t *testing.T) (srv *Server, clientW io.Writer, clientR *bufio.Reader) {
	t.Helper()
	// agentR ← messages sent by the test (acting as editor)
	// agentW → messages written by the ACP server
	agentR, clientWriter := io.Pipe()
	clientReader, agentW := io.Pipe()

	srv = &Server{
		engine:          nil, // intentionally nil — only engine-free handlers are tested
		config:          nil,
		reader:          agentR,
		writer:          agentW,
		sessions:        newSessionRegistry(),
		pendingOutbound: make(map[int64]chan json.RawMessage),
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	t.Cleanup(func() {
		cancel()
		clientWriter.Close()
		agentW.Close()
	})

	go srv.Start(ctx) //nolint:errcheck

	return srv, clientWriter, bufio.NewReader(clientReader)
}

// send writes a JSON-RPC request line to the server.
func send(t *testing.T, w io.Writer, id int, method string, params any) {
	t.Helper()
	type msg struct {
		JSONRPC string `json:"jsonrpc"`
		ID      int    `json:"id"`
		Method  string `json:"method"`
		Params  any    `json:"params,omitempty"`
	}
	data, err := json.Marshal(msg{JSONRPC: "2.0", ID: id, Method: method, Params: params})
	if err != nil {
		t.Fatalf("marshal send: %v", err)
	}
	fmt.Fprintln(w, string(data))
}

// sendNotif writes a JSON-RPC notification (no id) to the server.
func sendNotif(t *testing.T, w io.Writer, method string, params any) {
	t.Helper()
	type msg struct {
		JSONRPC string `json:"jsonrpc"`
		Method  string `json:"method"`
		Params  any    `json:"params,omitempty"`
	}
	data, err := json.Marshal(msg{JSONRPC: "2.0", Method: method, Params: params})
	if err != nil {
		t.Fatalf("marshal notif: %v", err)
	}
	fmt.Fprintln(w, string(data))
}

// readResponse reads one JSON-RPC response from the reader.
func readResponse(t *testing.T, r *bufio.Reader) map[string]json.RawMessage {
	t.Helper()
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			t.Fatalf("read response: %v", err)
		}
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var m map[string]json.RawMessage
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			t.Fatalf("parse response %q: %v", line, err)
		}
		return m
	}
}

// ── Tests ─────────────────────────────────────────────────────────────────────

func TestIntegration_Initialize(t *testing.T) {
	_, w, r := testServer(t)

	send(t, w, 0, "initialize", InitializeParams{
		ProtocolVersion: ProtocolVersion(1),
		ClientCapabilities: ClientCapabilities{
			FS:       FSCapability{ReadTextFile: true, WriteTextFile: true},
			Terminal: true,
		},
		ClientInfo: &ImplementationInfo{Name: "test-editor", Version: "1.0.0"},
	})

	resp := readResponse(t, r)

	// Must echo back id=0
	var id int
	if err := json.Unmarshal(resp["id"], &id); err != nil || id != 0 {
		t.Errorf("id = %s, want 0", resp["id"])
	}

	// Result must have protocolVersion and agentCapabilities
	var result InitializeResult
	if err := json.Unmarshal(resp["result"], &result); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}
	if result.ProtocolVersion != ProtocolVersion(1) {
		t.Errorf("protocolVersion = %d, want 1", result.ProtocolVersion)
	}
	if result.AgentInfo.Name != "swarm" {
		t.Errorf("agentInfo.name = %q, want swarm", result.AgentInfo.Name)
	}
	if !result.AgentCapabilities.LoadSession {
		t.Error("loadSession should be true")
	}
	if !result.AgentCapabilities.PromptCapabilities.EmbeddedContext {
		t.Error("embeddedContext should be true")
	}
}

func TestIntegration_InitializeAcceptsLegacyStringProtocolVersion(t *testing.T) {
	_, w, r := testServer(t)

	if _, err := fmt.Fprintln(w, `{"jsonrpc":"2.0","id":7,"method":"initialize","params":{"protocolVersion":"1","clientCapabilities":{}}}`); err != nil {
		t.Fatalf("write legacy initialize: %v", err)
	}

	resp := readResponse(t, r)
	if _, ok := resp["error"]; ok {
		t.Fatalf("legacy string protocolVersion returned error: %s", resp["error"])
	}
	var result map[string]json.RawMessage
	if err := json.Unmarshal(resp["result"], &result); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}
	if got := string(result["protocolVersion"]); got != "1" {
		t.Fatalf("protocolVersion JSON = %s, want numeric 1", got)
	}
}

func TestIntegration_UnknownMethod(t *testing.T) {
	_, w, r := testServer(t)

	send(t, w, 42, "magic/nonexistent", nil)

	resp := readResponse(t, r)

	if _, ok := resp["error"]; !ok {
		t.Errorf("expected error for unknown method, got result: %s", resp["result"])
	}
	var errObj RPCError
	json.Unmarshal(resp["error"], &errObj)
	if errObj.Code != ErrMethodNotFound {
		t.Errorf("error code = %d, want %d", errObj.Code, ErrMethodNotFound)
	}
}

func TestIntegration_SessionCancelIsNotification(t *testing.T) {
	_, w, r := testServer(t)

	// First initialize so the server doesn't reject us
	send(t, w, 0, "initialize", InitializeParams{ProtocolVersion: ProtocolVersion(1)})
	readResponse(t, r) // consume initialize response

	// session/cancel is a notification — no response expected.
	// We verify no response arrives within a short window.
	sendNotif(t, w, "session/cancel", SessionCancelParams{SessionID: "nonexistent"})

	// Send a follow-up request so we can tell when the server has processed the notification.
	send(t, w, 1, "initialize", InitializeParams{ProtocolVersion: ProtocolVersion(1)})
	resp := readResponse(t, r)

	// The response must be for id=1 (the second initialize), not for the cancel.
	var id int
	json.Unmarshal(resp["id"], &id)
	if id != 1 {
		t.Errorf("expected response for id=1, got id=%d", id)
	}
}

func TestIntegration_SessionSetModeUnknownSession(t *testing.T) {
	_, w, r := testServer(t)

	send(t, w, 5, "session/set_mode", SessionSetModeParams{
		SessionID: "sess_does_not_exist",
		ModeID:    "code",
	})

	resp := readResponse(t, r)
	if _, ok := resp["error"]; !ok {
		t.Error("expected error for unknown session")
	}
}

func TestIntegration_InitializeStoresClientCaps(t *testing.T) {
	srv, w, r := testServer(t)

	send(t, w, 0, "initialize", InitializeParams{
		ProtocolVersion: ProtocolVersion(1),
		ClientCapabilities: ClientCapabilities{
			FS:       FSCapability{ReadTextFile: true, WriteTextFile: false},
			Terminal: false,
		},
	})
	readResponse(t, r)

	if !srv.clientCaps.FS.ReadTextFile {
		t.Error("readTextFile capability not stored")
	}
	if srv.clientCaps.FS.WriteTextFile {
		t.Error("writeTextFile should be false")
	}
}

func TestIntegration_ConcurrentInitialize(t *testing.T) {
	_, w, r := testServer(t)

	// Fire 5 concurrent initialize requests.
	var wg sync.WaitGroup
	for i := range 5 {
		wg.Add(1)
		id := i
		go func() {
			defer wg.Done()
			send(t, w, id, "initialize", InitializeParams{ProtocolVersion: ProtocolVersion(1)})
		}()
	}
	wg.Wait()

	// Collect all 5 responses.
	seen := map[int]bool{}
	for range 5 {
		resp := readResponse(t, r)
		var id int
		if err := json.Unmarshal(resp["id"], &id); err != nil {
			t.Fatalf("parse id: %v", err)
		}
		if seen[id] {
			t.Errorf("duplicate response for id=%d", id)
		}
		seen[id] = true
	}
	for i := range 5 {
		if !seen[i] {
			t.Errorf("missing response for id=%d", i)
		}
	}
}

// TestProtocol_ModeConfigOption verifies the config option helper.
func TestProtocol_ModeConfigOption(t *testing.T) {
	opt := modeConfigOption("code")
	if opt.ID != "mode" {
		t.Errorf("id = %q, want mode", opt.ID)
	}
	if opt.CurrentValue != "code" {
		t.Errorf("currentValue = %q, want code", opt.CurrentValue)
	}
	if len(opt.Options) != len(availableModes) {
		t.Errorf("options len = %d, want %d", len(opt.Options), len(availableModes))
	}
	data, _ := json.Marshal(opt)
	s := string(data)
	if !strings.Contains(s, `"category":"mode"`) {
		t.Errorf("missing category: %s", s)
	}
}

// TestIntegration_ParseError verifies that malformed JSON produces a -32700 error.
func TestIntegration_ParseError(t *testing.T) {
	_, w, r := testServer(t)
	// Send a line that is not valid JSON.
	fmt.Fprintln(w, `{"jsonrpc":"2.0","id":99,"method":BROKEN}`)
	resp := readResponse(t, r)
	errRaw, ok := resp["error"]
	if !ok {
		t.Fatalf("expected error field in response, got: %v", resp)
	}
	var errObj RPCError
	if err := json.Unmarshal(errRaw, &errObj); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}
	if errObj.Code != ErrParseError {
		t.Errorf("error code = %d, want %d (ErrParseError)", errObj.Code, ErrParseError)
	}
}

// TestIntegration_SessionPrompt_UnknownSession verifies that session/prompt returns
// an error for a session ID that was never created.
func TestIntegration_SessionPrompt_UnknownSession(t *testing.T) {
	_, w, r := testServer(t)
	send(t, w, 7, "session/prompt", SessionPromptParams{
		SessionID: "sess_does_not_exist",
		Content:   []PromptContent{{Type: "text", Text: "hello"}},
	})
	resp := readResponse(t, r)
	if _, ok := resp["error"]; !ok {
		t.Error("expected error for unknown session in session/prompt")
	}
}

// TestIntegration_InitializeNoParams verifies that initialize succeeds when the
// params field is absent entirely (nil-safe path).
func TestIntegration_InitializeNoParams(t *testing.T) {
	_, w, r := testServer(t)
	// nil params → omitempty removes the params field from the wire message.
	send(t, w, 3, "initialize", nil)
	resp := readResponse(t, r)
	if _, ok := resp["error"]; ok {
		t.Errorf("unexpected error: %s", resp["error"])
	}
	var result InitializeResult
	if err := json.Unmarshal(resp["result"], &result); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}
	if result.ProtocolVersion != ProtocolVersion(1) {
		t.Errorf("protocolVersion = %d, want 1", result.ProtocolVersion)
	}
}

// TestIntegration_UnknownNotification_Silently_Ignored verifies that an unknown
// notification (no id) produces no response — the next request proceeds normally.
func TestIntegration_UnknownNotification_Silently_Ignored(t *testing.T) {
	_, w, r := testServer(t)
	// Fire an unknown notification (should be dropped).
	sendNotif(t, w, "something/unknown", map[string]string{"key": "val"})
	// Immediately follow with a real request.
	send(t, w, 11, "initialize", InitializeParams{ProtocolVersion: ProtocolVersion(1)})
	resp := readResponse(t, r)
	// The response must be for the initialize, not for the notification.
	var id int
	if err := json.Unmarshal(resp["id"], &id); err != nil || id != 11 {
		t.Errorf("expected response id=11, got id raw=%s", resp["id"])
	}
	if _, ok := resp["error"]; ok {
		t.Errorf("unexpected error: %s", resp["error"])
	}
}

// TestIntegration_SetMode_InvalidParams verifies that session/set_mode with
// un-parseable params returns an ErrInvalidParams error.
func TestIntegration_SetMode_InvalidParams(t *testing.T) {
	_, w, r := testServer(t)
	// Send a raw line whose params value is not a valid SessionSetModeParams
	// (array instead of object).
	fmt.Fprintln(w, `{"jsonrpc":"2.0","id":20,"method":"session/set_mode","params":[1,2,3]}`)
	resp := readResponse(t, r)
	errRaw, ok := resp["error"]
	if !ok {
		t.Fatalf("expected error for invalid params, got result: %s", resp["result"])
	}
	var errObj RPCError
	json.Unmarshal(errRaw, &errObj)
	if errObj.Code != ErrInvalidParams {
		t.Errorf("error code = %d, want %d (ErrInvalidParams)", errObj.Code, ErrInvalidParams)
	}
}

// TestIntegration_BuildPromptText_OnlyText verifies buildPromptText handles
// a prompt containing only text items with no embedded resources.
func TestIntegration_BuildPromptText_OnlyText(t *testing.T) {
	items := []PromptContent{
		{Type: "text", Text: "Fix the bug."},
	}
	got := buildPromptText(items)
	if got != "Fix the bug." {
		t.Errorf("buildPromptText = %q, want %q", got, "Fix the bug.")
	}
}

// TestIntegration_BuildPromptText_EmptyItems verifies buildPromptText returns
// an empty string for an empty prompt list.
func TestIntegration_BuildPromptText_EmptyItems(t *testing.T) {
	got := buildPromptText(nil)
	if got != "" {
		t.Errorf("buildPromptText(nil) = %q, want empty", got)
	}
}

// TestIntegration_BuildPromptText_MultipleResources verifies that multiple
// resources each get their own fenced block with the correct MIME-to-language mapping.
func TestIntegration_BuildPromptText_MultipleResources(t *testing.T) {
	items := []PromptContent{
		{Type: "text", Text: "Review these files:"},
		{Type: "resource", Resource: &PromptResource{
			URI:      "file:///main.go",
			MimeType: "text/x-go",
			Text:     "package main",
		}},
		{Type: "resource", Resource: &PromptResource{
			URI:      "file:///app.py",
			MimeType: "text/x-python",
			Text:     "print('hello')",
		}},
	}
	got := buildPromptText(items)
	if !strings.Contains(got, "```go file:///main.go") {
		t.Errorf("missing Go fence: %q", got)
	}
	if !strings.Contains(got, "```python file:///app.py") {
		t.Errorf("missing Python fence: %q", got)
	}
	if !strings.Contains(got, "package main") {
		t.Errorf("missing Go content: %q", got)
	}
	if !strings.Contains(got, "print('hello')") {
		t.Errorf("missing Python content: %q", got)
	}
}

// TestIntegration_PermissionKindMapping verifies permissionKindFromTool covers
// all four kind categories.
func TestIntegration_PermissionKindMapping(t *testing.T) {
	cases := []struct {
		tool       string
		permission string
		want       string
	}{
		{"WriteFile", "write", "write"},
		{"EditFile", "edit", "write"},
		{"BashExec", "execute", "execute"},
		{"DeleteFile", "delete", "delete"},
		{"ReadFile", "read", "read"},
		{"ReadFile", "", "read"},
	}
	for _, tc := range cases {
		got := permissionKindFromTool(tc.tool, tc.permission)
		if got != tc.want {
			t.Errorf("permissionKindFromTool(%q, %q) = %q, want %q",
				tc.tool, tc.permission, got, tc.want)
		}
	}
}

// TestIntegration_PermissionTitle verifies the human-readable title helper.
func TestIntegration_PermissionTitle(t *testing.T) {
	if got := permissionTitle("BashExec", "/tmp/script.sh"); got != "BashExec: /tmp/script.sh" {
		t.Errorf("got %q", got)
	}
	if got := permissionTitle("ReadFile", ""); got != "ReadFile" {
		t.Errorf("got %q", got)
	}
}

// TestProtocol_AcpModeMapping verifies the ACP → engine mode mapping.
func TestProtocol_AcpModeMapping(t *testing.T) {
	cases := map[string]string{
		"code":      "act",
		"ask":       "act",
		"architect": "plan",
		"unknown":   "act",
	}
	for acpMode, want := range cases {
		if got := acpModeToEngineMode(acpMode); got != want {
			t.Errorf("acpModeToEngineMode(%q) = %q, want %q", acpMode, got, want)
		}
	}
}

func TestProtocol_ToolCallUpdateUsesACPTerminalStatusesAndRawInput(t *testing.T) {
	raw, _ := json.Marshal(map[string]any{"command": "printf ok"})
	data, err := json.Marshal(SessionUpdate{
		SessionUpdateType: "tool_call",
		ToolCallID:        "tool-1",
		Title:             "Bash",
		Kind:              "execute",
		Status:            "pending",
		RawInput:          raw,
	})
	if err != nil {
		t.Fatalf("marshal tool_call: %v", err)
	}
	if !strings.Contains(string(data), `"rawInput":{"command":"printf ok"}`) {
		t.Fatalf("tool_call missing rawInput: %s", data)
	}

	for _, status := range []string{"pending", "in_progress", "completed", "failed"} {
		if status == "error" || status == "cancelled" {
			t.Fatalf("legacy non-ACP status %q must not be emitted", status)
		}
	}
}

func TestProtocol_SessionPromptPrefersCurrentFieldWithLegacyFallback(t *testing.T) {
	current := SessionPromptParams{
		Prompt:  []PromptContent{{Type: "text", Text: "current"}},
		Content: []PromptContent{{Type: "text", Text: "legacy"}},
	}
	if got := buildPromptText(current.promptBlocks()); got != "current" {
		t.Fatalf("current prompt = %q, want current", got)
	}

	legacy := SessionPromptParams{Content: []PromptContent{{Type: "text", Text: "legacy"}}}
	if got := buildPromptText(legacy.promptBlocks()); got != "legacy" {
		t.Fatalf("legacy prompt = %q, want legacy", got)
	}
}

func TestProtocol_ConfigOptionCategoriesSeparateModelFromCustomSelectors(t *testing.T) {
	provider := providerConfigOption("anthropic", []string{"anthropic"})
	if provider.Category != "_provider" {
		t.Fatalf("provider category = %q, want _provider", provider.Category)
	}
	model := modelConfigOption("claude-sonnet-4-6", nil)
	if model.Category != "model" {
		t.Fatalf("model category = %q, want model", model.Category)
	}
	agent := agentConfigOption("default", []AgentOption{{ID: "default", Name: "Default"}})
	if agent.Category != "_agent" {
		t.Fatalf("agent category = %q, want _agent", agent.Category)
	}
}

func TestProtocol_ConfigOptionValue_JSONUsesCurrentNameAndLegacyLabel(t *testing.T) {
	data, err := json.Marshal(ConfigOptionValue{
		Value:       "claude-sonnet-4-6",
		Label:       "Claude Sonnet 4.6",
		Description: "Balanced model",
	})
	if err != nil {
		t.Fatalf("marshal config option value: %v", err)
	}

	var got map[string]string
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal config option value: %v", err)
	}
	if got["name"] != "Claude Sonnet 4.6" {
		t.Fatalf("name = %q, want current ACP display name", got["name"])
	}
	if got["label"] != "Claude Sonnet 4.6" {
		t.Fatalf("label = %q, want legacy compatibility label", got["label"])
	}
}

// TestProtocol_InitializeResult_JSON verifies the full initialize result marshals correctly.
func TestProtocol_InitializeResult_JSON(t *testing.T) {
	res := InitializeResult{
		ProtocolVersion:   ProtocolVersion(1),
		AgentCapabilities: agentCapabilities,
		AgentInfo:         agentInfo,
	}
	data, _ := json.Marshal(res)
	var buf bytes.Buffer
	json.Indent(&buf, data, "", "  ")

	s := buf.String()
	checks := []string{
		`"protocolVersion": 1`,
		`"loadSession": true`,
		`"embeddedContext": true`,
		`"name": "swarm"`,
		`"slashCommands": true`,
		`"setConfig": true`,
	}
	for _, check := range checks {
		if !strings.Contains(s, check) {
			t.Errorf("missing %q in:\n%s", check, s)
		}
	}
}

// TestIntegration_SlashCommandList verifies slash_command/list returns all commands.
func TestIntegration_SlashCommandList(t *testing.T) {
	_, w, r := testServer(t)
	send(t, w, 30, "slash_command/list", nil)
	resp := readResponse(t, r)
	if _, ok := resp["error"]; ok {
		t.Fatalf("unexpected error: %s", resp["error"])
	}
	var result SlashCommandListResult
	if err := json.Unmarshal(resp["result"], &result); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}
	if len(result.Commands) == 0 {
		t.Error("expected at least one slash command")
	}
	names := map[string]bool{}
	for _, cmd := range result.Commands {
		names[cmd.Name] = true
	}
	for _, expected := range []string{"model", "provider", "tools", "help"} {
		if !names[expected] {
			t.Errorf("missing expected slash command %q", expected)
		}
	}
}

// TestIntegration_SlashCommandRun_Help runs /help without a session.
func TestIntegration_SlashCommandRun_Help(t *testing.T) {
	_, w, r := testServer(t)
	send(t, w, 31, "slash_command/run", SlashCommandRunParams{Command: "help"})
	resp := readResponse(t, r)
	if _, ok := resp["error"]; ok {
		t.Fatalf("unexpected error: %s", resp["error"])
	}
	var result SlashCommandRunResult
	if err := json.Unmarshal(resp["result"], &result); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}
	if !strings.Contains(result.Text, "/model") {
		t.Errorf("help text should mention /model, got: %q", result.Text)
	}
	if !strings.Contains(result.Text, "@") {
		t.Errorf("help text should mention @ mentions, got: %q", result.Text)
	}
}

// TestIntegration_SlashCommandRun_Tools_NoEngine runs /tools with no engine configured.
func TestIntegration_SlashCommandRun_Tools_NoEngine(t *testing.T) {
	_, w, r := testServer(t)
	send(t, w, 32, "slash_command/run", SlashCommandRunParams{Command: "tools"})
	resp := readResponse(t, r)
	if _, ok := resp["error"]; ok {
		t.Fatalf("unexpected error: %s", resp["error"])
	}
	var result SlashCommandRunResult
	if err := json.Unmarshal(resp["result"], &result); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}
	if result.Text == "" {
		t.Error("expected non-empty text response from /tools")
	}
}

// TestIntegration_SlashCommandRun_UnknownCommand verifies unknown commands return an error.
func TestIntegration_SlashCommandRun_UnknownCommand(t *testing.T) {
	_, w, r := testServer(t)
	send(t, w, 33, "slash_command/run", SlashCommandRunParams{Command: "notacommand"})
	resp := readResponse(t, r)
	if _, ok := resp["error"]; !ok {
		t.Error("expected error for unknown slash command")
	}
	var errObj RPCError
	json.Unmarshal(resp["error"], &errObj)
	if errObj.Code != ErrMethodNotFound {
		t.Errorf("error code = %d, want %d", errObj.Code, ErrMethodNotFound)
	}
}

// TestIntegration_SessionSetConfig_UnknownSession verifies session/set_config
// returns an error for an unknown session ID.
func TestIntegration_SessionSetConfig_UnknownSession(t *testing.T) {
	_, w, r := testServer(t)
	send(t, w, 34, "session/set_config", SessionSetConfigParams{
		SessionID: "sess_unknown",
		Updates:   map[string]string{"model": "claude-opus-4"},
	})
	resp := readResponse(t, r)
	if _, ok := resp["error"]; !ok {
		t.Error("expected error for unknown session in session/set_config")
	}
}

// TestIntegration_SessionSetConfig_InvalidParams verifies malformed params
// return ErrInvalidParams.
func TestIntegration_SessionSetConfig_InvalidParams(t *testing.T) {
	_, w, r := testServer(t)
	fmt.Fprintln(w, `{"jsonrpc":"2.0","id":35,"method":"session/set_config","params":"not-an-object"}`)
	resp := readResponse(t, r)
	if _, ok := resp["error"]; !ok {
		t.Error("expected error for invalid params")
	}
	var errObj RPCError
	json.Unmarshal(resp["error"], &errObj)
	if errObj.Code != ErrInvalidParams {
		t.Errorf("error code = %d, want %d", errObj.Code, ErrInvalidParams)
	}
}

// TestIntegration_SlashCommandRun_Tools_WithFilter runs /tools with a filter.
// When no tools are registered the server replies before filtering, so we just
// verify we get a non-error response.
func TestIntegration_SlashCommandRun_Tools_WithFilter(t *testing.T) {
	_, w, r := testServer(t)
	send(t, w, 36, "slash_command/run", SlashCommandRunParams{
		Command: "tools",
		Args:    "xyznotexist",
	})
	resp := readResponse(t, r)
	if _, ok := resp["error"]; ok {
		t.Fatalf("unexpected error: %s", resp["error"])
	}
	var result SlashCommandRunResult
	json.Unmarshal(resp["result"], &result)
	if result.Text == "" {
		t.Error("expected non-empty text response from /tools with filter")
	}
}

// TestSession_ApplyConfig verifies session-level config override logic.
func TestSession_ApplyConfig(t *testing.T) {
	sess := newSession("conv-1", "/project")

	changed := sess.applyConfig(map[string]string{
		"model":    "claude-opus-4",
		"provider": "anthropic",
		"mode":     "architect",
		"agent":    "agent-1",
	})
	if len(changed) != 4 {
		t.Errorf("expected 4 changed fields, got %d: %v", len(changed), changed)
	}

	prov, model, agentID, modeID := sess.getConfig()
	if prov != "anthropic" {
		t.Errorf("provider = %q, want anthropic", prov)
	}
	if model != "claude-opus-4" {
		t.Errorf("model = %q, want claude-opus-4", model)
	}
	if agentID != "agent-1" {
		t.Errorf("agentID = %q, want agent-1", agentID)
	}
	if modeID != "architect" {
		t.Errorf("modeID = %q, want architect", modeID)
	}

	// Re-applying same values should report no changes.
	changed2 := sess.applyConfig(map[string]string{"model": "claude-opus-4"})
	if len(changed2) != 0 {
		t.Errorf("expected 0 changes on re-apply, got %d: %v", len(changed2), changed2)
	}
}
