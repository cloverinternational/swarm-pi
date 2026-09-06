package builtin

import (
	"context"
	"strings"
	"testing"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/hooks"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/tools/ii"
)

func TestTaskMaintenanceReminderUsesSessionManagerAndMainOwner(t *testing.T) {
	hooks.ResetDefaultMetaNudgeBudgetForTest(1)
	t.Cleanup(func() { hooks.ResetDefaultMetaNudgeBudgetForTest(5) })

	global := ii.GetTodoManager()
	_ = global.ClearTodos()
	if err := global.AddTodo(ii.TodoItem{
		ID: "bg", Content: "BG agent: unrelated", Status: ii.TodoStatusInProgress,
		Priority: ii.TodoPriorityMedium, OwnerID: "worker-1",
	}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = global.ClearTodos() })

	session := ii.NewTodoManager()
	if err := session.AddTodo(ii.TodoItem{
		ID: "main", Content: "Current session task", Status: ii.TodoStatusPending,
		Priority: ii.TodoPriorityMedium,
	}); err != nil {
		t.Fatal(err)
	}
	ctx := ii.ContextWithTodoManager(context.Background(), session)
	event := hooks.Event{
		Type: hooks.EventMessageAfterReceive, ConversationID: "session-a",
		Data: map[string]any{"role": "user", "content": "continue"},
	}
	hooks.DefaultMetaNudgeBudget().RecordUserTurn("session-a")
	result, err := NewTaskMaintenanceReminderHook().OnEvent(ctx, event)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result.Message, "Current session task") {
		t.Fatalf("session task missing from reminder: %q", result.Message)
	}
	if strings.Contains(result.Message, "BG agent") || strings.Contains(result.Message, "No pending tasks") {
		t.Fatalf("foreign or false task state leaked into reminder: %q", result.Message)
	}
}

func TestTaskEnforcementDefaultIsAdvisory(t *testing.T) {
	tm := ii.GetTodoManager()
	_ = tm.ClearTodos()
	t.Cleanup(func() { _ = tm.ClearTodos() })
	hooks.ResetDefaultMetaNudgeBudgetForTest(1)
	t.Cleanup(func() { hooks.ResetDefaultMetaNudgeBudgetForTest(5) })
	hooks.DefaultMetaNudgeBudget().RecordUserTurn("advisory-session")

	result, err := NewTaskEnforcementHook().OnEvent(context.Background(), hooks.Event{
		Type:           hooks.EventToolBeforeExecute,
		ConversationID: "advisory-session",
		Data:           map[string]any{"tool_name": "Write"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Action != hooks.ActionContinue {
		t.Fatalf("default enforcement blocked tool: %v", result.Action)
	}
	for _, want := range []string{`source="task-enforcement-hook"`, `kind="nudge"`, `seq="1"`, "No active task"} {
		if !strings.Contains(result.Message, want) {
			t.Fatalf("advisory message %q missing %q", result.Message, want)
		}
	}
}
