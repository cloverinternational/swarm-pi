package builtin

import (
	"context"
	"strings"
	"testing"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/hooks"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/tools/ii"
)

func TestTaskEnforcementRequiresActiveFocus(t *testing.T) {
	tm := ii.GetTodoManager()
	tm.ClearTodos()
	// A pending task alone must NOT unlock side effects.
	if err := tm.SetTodos([]ii.TodoItem{{ID: "1", Content: "pending", Status: ii.TodoStatusPending, Priority: ii.TodoPriorityMedium}}); err != nil {
		t.Fatal(err)
	}
	hook := NewTaskEnforcementHookWithConfig(TaskEnforcementConfig{EnforcementMode: EnforcementModeBlock})
	event := hooks.Event{Type: hooks.EventToolBeforeExecute, Data: map[string]any{"tool_name": "bash"}}
	if res, _ := hook.OnEvent(context.Background(), event); res.Action != hooks.ActionBlock {
		t.Fatalf("pending-only should block, got %v", res.Action)
	}

	// Focusing an in-progress task unlocks side effects.
	if err := tm.SetTodos([]ii.TodoItem{{ID: "1", Content: "active", Status: ii.TodoStatusInProgress, Active: true, Priority: ii.TodoPriorityMedium}}); err != nil {
		t.Fatal(err)
	}
	if res, _ := hook.OnEvent(context.Background(), event); res.Action != hooks.ActionContinue {
		t.Fatalf("active focus should allow, got %v", res.Action)
	}
	tm.ClearTodos()
}

func TestTaskAuditHookAttachesSanitizedEvent(t *testing.T) {
	tm := ii.GetTodoManager()
	tm.ClearTodos()
	if err := tm.SetTodos([]ii.TodoItem{{ID: "1", Content: "active", Status: ii.TodoStatusInProgress, Active: true, Priority: ii.TodoPriorityMedium}}); err != nil {
		t.Fatal(err)
	}
	hook := NewTaskAuditHook()
	event := hooks.Event{
		Type:    hooks.EventToolAfterExecute,
		AgentID: "main",
		Data: map[string]any{
			"tool_name": "bash",
			"params":    map[string]any{"command": "go test ./...", "token": "sk-secret"},
		},
	}
	if _, err := hook.OnEvent(context.Background(), event); err != nil {
		t.Fatal(err)
	}
	task := tm.ByID("1")
	if len(task.AuditEvents) != 1 {
		t.Fatalf("expected 1 audit event, got %d", len(task.AuditEvents))
	}
	evt := task.AuditEvents[0]
	if evt.Metadata["tool"] != "bash" || evt.Metadata["outcome"] != "success" {
		t.Fatalf("unexpected metadata: %+v", evt.Metadata)
	}
	if evt.Summary == "" || strings.Contains(evt.Summary, "sk-secret") {
		t.Fatalf("summary leaked or empty: %q", evt.Summary)
	}
	tm.ClearTodos()
}

func TestTaskAuditHookIgnoresWithoutFocus(t *testing.T) {
	tm := ii.GetTodoManager()
	tm.ClearTodos()
	if err := tm.SetTodos([]ii.TodoItem{{ID: "1", Content: "pending", Status: ii.TodoStatusPending, Priority: ii.TodoPriorityMedium}}); err != nil {
		t.Fatal(err)
	}
	hook := NewTaskAuditHook()
	event := hooks.Event{Type: hooks.EventToolAfterExecute, Data: map[string]any{"tool_name": "bash", "params": map[string]any{"command": "ls"}}}
	if _, err := hook.OnEvent(context.Background(), event); err != nil {
		t.Fatal(err)
	}
	if len(tm.ByID("1").AuditEvents) != 0 {
		t.Fatal("event attached without an active focus")
	}
	tm.ClearTodos()
}
