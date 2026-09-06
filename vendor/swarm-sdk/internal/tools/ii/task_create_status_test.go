package ii

import "testing"

// TestTaskManageCreateAcceptsInitialStatus verifies the first-use pattern where
// an agent creates a task already in progress in a single operation. This used to
// fail with "field status is not valid for create" even though the task-enforcement
// hook explicitly tells agents to set status="in_progress".
func TestTaskManageCreateAcceptsInitialStatus(t *testing.T) {
	manager := NewTodoManager()
	batch := executeTaskManage(t, manager, map[string]any{"operations": []any{
		map[string]any{
			"key":         "c1",
			"op":          "create",
			"subject":     "Investigate bug",
			"description": "Investigate the reported bug",
			"status":      "in_progress",
			"active":      true,
		},
	}})
	if batch.Status != "succeeded" {
		t.Fatalf("status = %q, want succeeded: %+v", batch.Status, batch)
	}
	task := batch.Results[0].Data.Task
	if task == nil {
		t.Fatalf("expected created task, got nil: %+v", batch.Results[0])
	}
	if task.Status != TodoStatusInProgress {
		t.Fatalf("status = %q, want in_progress", task.Status)
	}
	if !task.Active {
		t.Fatal("expected created task to be active")
	}
}

// TestTaskManageCreateIgnoresDeletedStatus ensures a nonsensical initial status of
// "deleted" is ignored rather than creating a pre-deleted task.
func TestTaskManageCreateIgnoresDeletedStatus(t *testing.T) {
	manager := NewTodoManager()
	batch := executeTaskManage(t, manager, map[string]any{"operations": []any{
		map[string]any{
			"key":         "c1",
			"op":          "create",
			"subject":     "Task",
			"description": "Task details",
			"status":      "deleted",
		},
	}})
	if batch.Status != "succeeded" {
		t.Fatalf("status = %q, want succeeded: %+v", batch.Status, batch)
	}
	if got := batch.Results[0].Data.Task.Status; got != TodoStatusPending {
		t.Fatalf("status = %q, want pending (deleted ignored on create)", got)
	}
}
