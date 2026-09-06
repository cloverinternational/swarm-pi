package builtin

import (
	"context"
	"testing"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/hooks"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/tools/ii"
)

func TestTaskEnforcementImmediatelyAllowsAfterReaffirmingInProgress(t *testing.T) {
	tm := ii.GetTodoManager()
	_ = tm.ClearTodos()
	t.Cleanup(func() { _ = tm.ClearTodos() })
	if err := tm.SetTodos([]ii.TodoItem{
		{ID: "1", Content: "Reclaim", Status: ii.TodoStatusInProgress, Priority: ii.TodoPriorityMedium},
		{ID: "2", Content: "Focused", Status: ii.TodoStatusInProgress, Active: true, Priority: ii.TodoPriorityMedium},
	}); err != nil {
		t.Fatal(err)
	}
	result, err := ii.NewTaskManageTool().Execute(context.Background(), map[string]any{"operations": []any{
		map[string]any{"key": "reclaim", "op": "update", "taskId": "1", "status": "in_progress"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if tm.ByID("1") == nil || !tm.ByID("1").Active || tm.ByID("2").Active {
		t.Fatalf("status reaffirmation did not transfer focus: result=%s tasks=%+v", result.Output, tm.Todos())
	}
	event := hooks.Event{Type: hooks.EventToolBeforeExecute, Data: map[string]any{"tool_name": "bash"}}
	hookResult, err := NewTaskEnforcementHook().OnEvent(context.Background(), event)
	if err != nil {
		t.Fatal(err)
	}
	if hookResult.Action != hooks.ActionContinue {
		t.Fatalf("enforcement still blocked after documented recovery: %+v", hookResult)
	}
}
