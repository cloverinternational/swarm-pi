package hooks

import (
	"context"
	"testing"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/tools"
)

func TestHooksManagerRegistersAndSurfacesAnnoyanceNudge(t *testing.T) {
	manager := NewHooksManager(nil, nil, t.TempDir())
	if manager.IsEnabled("annoyance-nudge") {
		t.Fatal("annoyance-nudge should wait until annoyed is registered")
	}
	if err := manager.EnableAnnoyanceNudge(); err != nil {
		t.Fatalf("EnableAnnoyanceNudge: %v", err)
	}

	results := manager.EmitToolAfterExecute(
		context.Background(),
		"HistorySearch",
		map[string]any{"query": "vault"},
		&tools.ToolResult{Output: "TOOL RESULT TOO LARGE", IsError: true},
		nil,
	)
	for _, result := range results {
		if result.HookName == "annoyance-nudge" {
			if result.AdditionalContext == "" {
				t.Fatal("annoyance nudge was not injected into agent context")
			}
			return
		}
	}
	t.Fatalf("annoyance-nudge result missing: %#v", results)
}
