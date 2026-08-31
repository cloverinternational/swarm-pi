package client

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/agent"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/tools"
)

func TestRunPrintWriteToolCallText(t *testing.T) {
	var buf bytes.Buffer
	err := writeToolCall(&buf, RunPrintModeText, agent.ToolCallUpdate{Name: "Read", ID: "tc_1"})
	if err != nil {
		t.Fatalf("writeToolCall: %v", err)
	}
	if got := buf.String(); got != "→ Read\n" {
		t.Fatalf("text tool_call: got %q", got)
	}
}

func TestRunPrintWriteToolCallJSON(t *testing.T) {
	var buf bytes.Buffer
	err := writeToolCall(&buf, RunPrintModeJSON, agent.ToolCallUpdate{
		ID: "tc_1", Name: "Read", Parameters: map[string]any{"path": "/x"}, Sequence: 7,
	})
	if err != nil {
		t.Fatalf("writeToolCall: %v", err)
	}
	line := strings.TrimRight(buf.String(), "\n")
	var ev map[string]any
	if err := json.Unmarshal([]byte(line), &ev); err != nil {
		t.Fatalf("json unmarshal: %v (line=%q)", err, line)
	}
	if ev["type"] != "tool_call" || ev["name"] != "Read" || ev["id"] != "tc_1" {
		t.Fatalf("unexpected event: %v", ev)
	}
	input, ok := ev["input"].(map[string]any)
	if !ok || input["path"] != "/x" {
		t.Fatalf("input wrong: %v", ev["input"])
	}
}

func TestRunPrintWriteToolResultJSONError(t *testing.T) {
	var buf bytes.Buffer
	err := writeToolResult(&buf, RunPrintModeJSON, agent.ToolResultUpdate{
		ID: "tc_2", Output: "denied", Error: errors.New("permission denied"),
	}, 200)
	if err != nil {
		t.Fatalf("writeToolResult: %v", err)
	}
	var ev map[string]any
	if err := json.Unmarshal(bytes.TrimRight(buf.Bytes(), "\n"), &ev); err != nil {
		t.Fatalf("json unmarshal: %v", err)
	}
	if ev["ok"] != false || ev["error"] != "permission denied" {
		t.Fatalf("unexpected event: %v", ev)
	}
	if ev["preview"] != "denied" {
		t.Fatalf("preview missing/wrong: %v", ev["preview"])
	}
}

func TestRunPrintWriteFinalText(t *testing.T) {
	var buf bytes.Buffer
	err := writeFinal(&buf, RunPrintModeText, &RunPrintStats{Message: "hello"})
	if err != nil {
		t.Fatalf("writeFinal: %v", err)
	}
	if !strings.Contains(buf.String(), "hello") {
		t.Fatalf("text final missing message: %q", buf.String())
	}
}

func TestRunPrintWriteFinalJSON(t *testing.T) {
	var buf bytes.Buffer
	err := writeFinal(&buf, RunPrintModeJSON, &RunPrintStats{
		Message: "done", InputTokens: 10, OutputTokens: 3, TurnCount: 2,
	})
	if err != nil {
		t.Fatalf("writeFinal: %v", err)
	}
	var ev map[string]any
	if err := json.Unmarshal(bytes.TrimRight(buf.Bytes(), "\n"), &ev); err != nil {
		t.Fatalf("json unmarshal: %v", err)
	}
	if ev["type"] != "final" || ev["message"] != "done" {
		t.Fatalf("unexpected: %v", ev)
	}
	if ev["input_tokens"].(float64) != 10 || ev["output_tokens"].(float64) != 3 || ev["turn_count"].(float64) != 2 {
		t.Fatalf("token/turn fields wrong: %v", ev)
	}
}

func TestRunPrintTruncateForPreview(t *testing.T) {
	if got := truncateForPreview("hello world", 5); got != "hello…" {
		t.Fatalf("truncate: got %q", got)
	}
	if got := truncateForPreview("short", 50); got != "short" {
		t.Fatalf("no-trunc: got %q", got)
	}
	if got := truncateForPreview("anything", 0); got != "anything" {
		t.Fatalf("zero disables truncation: got %q", got)
	}
}

func TestDefaultToolsBundleNonEmpty(t *testing.T) {
	got := DefaultTools(t.TempDir())
	if len(got) == 0 {
		t.Fatal("DefaultTools returned empty bundle")
	}
	names := map[string]bool{}
	all := make([]string, 0, len(got))
	for _, tool := range got {
		names[tool.Name()] = true
		all = append(all, tool.Name())
	}
	// "bash" is a core member of the default bundle. (The legacy "swarm"
	// agent-to-agent tool was removed in SWA-20 and must NOT be present.)
	if !names["bash"] {
		t.Errorf("DefaultTools missing %q (have: %v)", "bash", all)
	}
	if names["swarm"] {
		t.Errorf("DefaultTools must not include the removed %q tool (SWA-20)", "swarm")
	}
}

func TestDefaultToolsWebFetchUsesRegistryPermissionBinder(t *testing.T) {
	registry := tools.NewRegistry()
	for _, tool := range DefaultTools(t.TempDir()) {
		if tool.Name() == "web_fetch" {
			if err := registry.Register(tool); err != nil {
				t.Fatalf("register %s: %v", tool.Name(), err)
			}
		}
	}
	if !registry.IsRegistered("web_fetch") {
		t.Fatal("DefaultTools catalog does not contain web_fetch")
	}

	executor := tools.NewExecutor(registry, tools.NewSimplePermissionChecker(tools.DefaultPermissionPolicies()))
	result, err := executor.Execute(context.Background(), "web_fetch", map[string]any{"url": "https://example.com"})
	if err == nil || !strings.Contains(err.Error(), "permission") {
		t.Fatalf("web_fetch permission denial = result %#v, error %v", result, err)
	}
}
