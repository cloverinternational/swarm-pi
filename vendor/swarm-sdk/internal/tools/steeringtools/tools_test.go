package steeringtools

import (
	"context"
	"sort"
	"testing"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/tools"
)

// expectedNames is the canonical ordered list of the Phase 1 steering tools.
// Keeping this in the test pins the surface so accidental renames or removals
// fail loudly.
var expectedNames = []string{
	"observe_only",
	"inject_system_note",
	"refocus",
	"block_next_tool",
	"halt_peer_loop",
	"ask_user",
	"log_concern",
}

func TestAllReturnsCanonicalSurface(t *testing.T) {
	got := All()
	if len(got) != len(expectedNames) {
		t.Fatalf("All() returned %d tools, want %d", len(got), len(expectedNames))
	}
	for i, tl := range got {
		if tl.Name() != expectedNames[i] {
			t.Errorf("All()[%d].Name() = %q, want %q", i, tl.Name(), expectedNames[i])
		}
		if tl.Description() == "" {
			t.Errorf("tool %q has empty Description()", tl.Name())
		}
		if tl.Parameters() == nil {
			t.Errorf("tool %q has nil Parameters()", tl.Name())
		}
	}
}

func TestAllToolsUnique(t *testing.T) {
	got := All()
	names := make([]string, len(got))
	for i, tl := range got {
		names[i] = tl.Name()
	}
	sort.Strings(names)
	for i := 1; i < len(names); i++ {
		if names[i] == names[i-1] {
			t.Errorf("duplicate tool name: %q", names[i])
		}
	}
}

func TestObserveOnlyExecuteOK(t *testing.T) {
	res, err := callTool(t, NewObserveOnlyTool(), map[string]any{"note": "looks fine"})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if res.Metadata["note"] != "looks fine" {
		t.Errorf("note metadata = %v, want %q", res.Metadata["note"], "looks fine")
	}
}

func TestInjectSystemNoteExecuteOK(t *testing.T) {
	res, err := callTool(t, NewInjectSystemNoteTool(), map[string]any{
		"text":     "refocus on the failing test",
		"priority": 50,
	})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if res.Metadata["text"] != "refocus on the failing test" {
		t.Errorf("text metadata = %v", res.Metadata["text"])
	}
}

func TestRefocusExecuteOK(t *testing.T) {
	res, err := callTool(t, NewRefocusTool(), map[string]any{
		"anchor":   "task-9",
		"reminder": "return to scaffolding",
	})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if res.Metadata["anchor"] != "task-9" {
		t.Errorf("anchor metadata = %v", res.Metadata["anchor"])
	}
}

func TestBlockNextToolExecuteOK(t *testing.T) {
	res, err := callTool(t, NewBlockNextToolTool(), map[string]any{
		"reason":    "redundant read",
		"tool_name": "read_file",
	})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if res.Metadata["reason"] != "redundant read" {
		t.Errorf("reason metadata = %v", res.Metadata["reason"])
	}
	if res.Metadata["tool_name"] != "read_file" {
		t.Errorf("tool_name metadata = %v", res.Metadata["tool_name"])
	}
}

func TestHaltPeerLoopClampsTTL(t *testing.T) {
	res, err := callTool(t, NewHaltPeerLoopTool(), map[string]any{
		"peer":        "pop-os-1",
		"reason":      "ping-pong",
		"ttl_seconds": 9999,
	})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	// Phase 1 clamp: anything above 600 is clipped to 600.
	if res.Metadata["ttl_seconds"] != 600 {
		t.Errorf("ttl_seconds = %v, want 600 (clamped)", res.Metadata["ttl_seconds"])
	}
}

func TestHaltPeerLoopDefaultTTL(t *testing.T) {
	res, err := callTool(t, NewHaltPeerLoopTool(), map[string]any{
		"peer":   "pop-os-1",
		"reason": "ping-pong",
	})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if res.Metadata["ttl_seconds"] != 60 {
		t.Errorf("ttl_seconds = %v, want 60 (default)", res.Metadata["ttl_seconds"])
	}
}

func TestAskUserReturnsDefault(t *testing.T) {
	res, err := callTool(t, NewAskUserTool(), map[string]any{
		"question": "continue?",
		"default":  "no",
	})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if res.Metadata["answer"] != "no" {
		t.Errorf("answer = %v, want %q", res.Metadata["answer"], "no")
	}
}

func TestLogConcernExecuteOK(t *testing.T) {
	res, err := callTool(t, NewLogConcernTool(), map[string]any{
		"tag":  "drift",
		"note": "two peers mirroring",
	})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if res.Metadata["tag"] != "drift" {
		t.Errorf("tag = %v", res.Metadata["tag"])
	}
}

// callTool is a thin helper that runs a tool's Execute with the given params
// and returns the typed *tools.ToolResult.
func callTool(t *testing.T, tl tools.Tool, params map[string]any) (*tools.ToolResult, error) {
	t.Helper()
	return tl.Execute(context.Background(), params)
}
