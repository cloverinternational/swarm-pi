package builtin

import (
	"context"
	"strings"
	"testing"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/hooks"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/taskstore"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/tools/ii"
)

func TestTaskAuditSummaryRedactsEmbeddedSecretsInPreferredFields(t *testing.T) {
	tests := []struct {
		key   string
		value string
	}{
		{"command", `curl -H "Authorization: Bearer audit-sentinel-token-value" example.test`},
		{"query", "password=audit-sentinel-password"},
		{"url", "https://example.test/?token=audit-sentinel-token"},
		{"path", "/tmp/token=audit-sentinel-path"},
	}
	for _, test := range tests {
		t.Run(test.key, func(t *testing.T) {
			got := summarizeToolArgs("tool", map[string]any{test.key: test.value})
			if strings.Contains(got, "audit-sentinel") || !strings.Contains(got, "[REDACTED]") {
				t.Fatalf("summary was not safely redacted: %q", got)
			}
		})
	}
	if got := summarizeToolArgs("bash", map[string]any{"command": "go test ./internal/taskstore"}); got != "bash: go test ./internal/taskstore" {
		t.Fatalf("ordinary command metadata changed: %q", got)
	}
}

func TestTaskAuditPersistsOnlyRedactedSummaryAndNeverToolOutput(t *testing.T) {
	tm := ii.GetTodoManager()
	_ = tm.ClearTodos()
	storeCfg := taskstore.Config{ConversationID: "audit", MetadataDir: t.TempDir()}
	store := taskstore.New(storeCfg)
	tm.SetSyncer(taskstore.NewTodoSyncer(store))
	t.Cleanup(func() {
		tm.SetSyncer(nil)
		_ = tm.ClearTodos()
	})
	if err := tm.SetTodos([]ii.TodoItem{{ID: "1", Content: "active", Status: ii.TodoStatusInProgress, Active: true, Priority: ii.TodoPriorityMedium}}); err != nil {
		t.Fatal(err)
	}
	event := hooks.Event{Type: hooks.EventToolAfterExecute, Data: map[string]any{
		"tool_name":   "bash",
		"params":      map[string]any{"command": "echo token=audit-sentinel-persisted"},
		"tool_output": "audit-sentinel-output",
	}}
	if _, err := NewTaskAuditHook().OnEvent(context.Background(), event); err != nil {
		t.Fatal(err)
	}
	reloaded, err := taskstore.Load(storeCfg)
	if err != nil {
		t.Fatal(err)
	}
	task, err := reloaded.GetTask("1")
	if err != nil {
		t.Fatal(err)
	}
	if len(task.AuditEvents) != 1 {
		t.Fatalf("persisted audit events = %d", len(task.AuditEvents))
	}
	persisted := task.AuditEvents[0].Summary
	if strings.Contains(persisted, "audit-sentinel-persisted") || strings.Contains(persisted, "audit-sentinel-output") || !strings.Contains(persisted, "[REDACTED]") {
		t.Fatalf("unsafe persisted summary: %q", persisted)
	}
}
