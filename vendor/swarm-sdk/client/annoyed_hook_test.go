package client

import (
	"context"
	"errors"
	"testing"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/hooks"
	dreambuiltin "github.com/Swarm-Code/mono/swarm-sdk/internal/hooks/builtin"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/tools"
	toolsbuiltin "github.com/Swarm-Code/mono/swarm-sdk/internal/tools/builtin"
)

func TestClientToolOutputNormalizesToolResultFailure(t *testing.T) {
	result := &tools.ToolResult{
		Output:     "TOOL RESULT TOO LARGE",
		IsError:    true,
		DurationMS: 17,
	}
	output := clientToolOutput(result, nil)
	if output["success"] != false {
		t.Fatalf("success = %#v", output["success"])
	}
	if output["error"] != result.Output {
		t.Fatalf("error = %#v", output["error"])
	}
	if output["duration_ms"] != int64(17) {
		t.Fatalf("duration_ms = %#v", output["duration_ms"])
	}
}

func TestClientToolOutputDoesNotMutateMapResult(t *testing.T) {
	original := map[string]any{"value": "kept"}
	execErr := errors.New("boom")
	output := clientToolOutput(original, execErr)
	if _, exists := original["success"]; exists {
		t.Fatal("normalization mutated the caller's result map")
	}
	if output["success"] != false || output["error"] != "boom" {
		t.Fatalf("normalized output = %#v", output)
	}
}

func TestClientHooksManagerSurfacesAnnoyanceNudge(t *testing.T) {
	manager := &clientHooksManager{
		extra: []hooks.Hook{dreambuiltin.NewAnnoyanceNudgeHook()},
	}
	results := manager.EmitToolAfterExecute(
		context.Background(),
		"HistorySearch",
		map[string]any{"query": "vault"},
		&tools.ToolResult{Output: "TOOL RESULT TOO LARGE", IsError: true},
		nil,
	)
	if len(results) != 1 || results[0].HookName != "annoyance-nudge" || results[0].Output == "" {
		t.Fatalf("hook results = %#v", results)
	}
}

func TestClientAnnoyanceNudgesAreConversationScoped(t *testing.T) {
	manager := &clientHooksManager{
		extra: []hooks.Hook{dreambuiltin.NewAnnoyanceNudgeHook()},
	}
	for _, conversationID := range []string{"conversation-a", "conversation-b"} {
		ctx := tools.WithOwnerInfo(context.Background(), "agent", "user", conversationID)
		results := manager.EmitToolAfterExecute(
			ctx, "HistorySearch", nil,
			&tools.ToolResult{Output: "TOOL RESULT TOO LARGE", IsError: true}, nil,
		)
		if len(results) != 1 || results[0].HookName != "annoyance-nudge" {
			t.Fatalf("%s hook results = %#v", conversationID, results)
		}
	}
}

func TestInjectedRegistryEnablesAndSurfacesAnnoyanceNudge(t *testing.T) {
	registry := tools.NewSimpleRegistry(nil, nil)
	if err := registry.Register(toolsbuiltin.NewAnnoyedTool()); err != nil {
		t.Fatal(err)
	}
	manager := &clientHooksManager{}
	if !enableAnnoyanceNudgeForRegistry(manager, registry) {
		t.Fatal("injected registry did not enable annoyance nudge")
	}
	results := manager.EmitToolAfterExecute(
		tools.WithOwnerInfo(context.Background(), "agent", "user", "conversation"),
		"vault_exec",
		nil,
		&tools.ToolResult{Output: "vault execution failed", IsError: true},
		nil,
	)
	if len(results) != 1 || results[0].HookName != "annoyance-nudge" || results[0].Output == "" {
		t.Fatalf("hook results = %#v", results)
	}
}
