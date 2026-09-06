package ii

import "testing"

// TestTaskManageAllowsCreateThenUpdateSameKey proves the documented pattern
// from the op-field schema text — "update — requires taskId, or omit it to
// target the task registered under this operation's key" — actually works
// end to end in a single batch: create(key=X) followed by update(key=X)
// with no taskId targets the task the create just made (issues #286, #296).
func TestTaskManageAllowsCreateThenUpdateSameKey(t *testing.T) {
	manager := NewTodoManager()
	batch := executeTaskManage(t, manager, map[string]any{"operations": []any{
		createOperation("fixpanic", "Fix panic"),
		map[string]any{"key": "fixpanic", "op": "update", "status": "in_progress"},
	}})
	if batch.Status != "succeeded" {
		t.Fatalf("status = %q, want succeeded: %+v", batch.Status, batch)
	}
	if len(batch.Results) != 2 {
		t.Fatalf("expected 2 results, got %d: %+v", len(batch.Results), batch)
	}
	created := batch.Results[0].Data.Task
	updated := batch.Results[1].Data.Task
	if created == nil || updated == nil {
		t.Fatalf("expected both results to carry a task: %+v", batch)
	}
	if created.ID != updated.ID {
		t.Fatalf("update targeted a different task: created %s, updated %s", created.ID, updated.ID)
	}
	if updated.Status != "in_progress" {
		t.Fatalf("update did not apply: status = %q", updated.Status)
	}
}

// TestTaskManageAllowsCreateThenGetSameKey covers the "get" half of the same
// documented targeting rule.
func TestTaskManageAllowsCreateThenGetSameKey(t *testing.T) {
	manager := NewTodoManager()
	batch := executeTaskManage(t, manager, map[string]any{"operations": []any{
		createOperation("audit", "Audit issues"),
		map[string]any{"key": "audit", "op": "get"},
	}})
	if batch.Status != "succeeded" {
		t.Fatalf("status = %q, want succeeded: %+v", batch.Status, batch)
	}
	if batch.Results[0].Data.Task.ID != batch.Results[1].Data.Task.ID {
		t.Fatalf("get targeted a different task than create made: %+v", batch)
	}
}

// TestTaskManageAllowsCreateThenMultipleUpdatesSameKey proves a THIRD
// operation may also reuse the same key established by the first create —
// the allowance isn't limited to exactly one follow-up operation.
func TestTaskManageAllowsCreateThenMultipleUpdatesSameKey(t *testing.T) {
	manager := NewTodoManager()
	batch := executeTaskManage(t, manager, map[string]any{"operations": []any{
		createOperation("x", "Task X"),
		map[string]any{"key": "x", "op": "update", "status": "in_progress"},
		map[string]any{"key": "x", "op": "update", "addNote": "progress note", "noteType": "observation"},
		map[string]any{"key": "x", "op": "get"},
	}})
	if batch.Status != "succeeded" {
		t.Fatalf("status = %q, want succeeded: %+v", batch.Status, batch)
	}
	for i, result := range batch.Results {
		if result.Status != "succeeded" {
			t.Fatalf("operation %d failed: %+v", i, result)
		}
	}
}

// TestTaskManageStillRejectsDuplicateCreateKey proves the fix did not widen
// the allowance to the one case that stays genuinely ambiguous: two creates
// sharing a key, where a later {"ref": key} could not say which new task it
// meant.
func TestTaskManageStillRejectsDuplicateCreateKey(t *testing.T) {
	manager := NewTodoManager()
	batch := executeTaskManage(t, manager, map[string]any{"operations": []any{
		createOperation("same", "One"),
		createOperation("same", "Two"),
	}})
	if batch.Status != "failed" && batch.Status != "partial" {
		t.Fatalf("status = %q, want failed/partial: %+v", batch.Status, batch)
	}
	var found bool
	for _, result := range batch.Results {
		if result.Error != nil && result.Error.Code == "validation_failed" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected a validation_failed error: %+v", batch)
	}
	if manager.Count() != 0 {
		t.Fatalf("rejected batch leaked %d task(s)", manager.Count())
	}
}

// TestTaskManageStillRejectsDuplicateUpdateKeyWithoutPriorCreate proves that
// reusing a key across two non-create operations, where no create in the
// same batch established it first, is still rejected (the allowance is
// specifically "create, then reuse", not "reuse a key any number of times").
func TestTaskManageStillRejectsDuplicateUpdateKeyWithoutPriorCreate(t *testing.T) {
	manager := NewTodoManager()
	seedBatch := executeTaskManage(t, manager, map[string]any{"operations": []any{
		createOperation("seed", "Seed"),
	}})
	if seedBatch.Status != "succeeded" {
		t.Fatalf("seed create failed: %+v", seedBatch)
	}
	seedID := seedBatch.Results[0].Data.Task.ID
	batch := executeTaskManage(t, manager, map[string]any{"operations": []any{
		map[string]any{"key": "dup", "op": "update", "taskId": seedID, "status": "in_progress"},
		map[string]any{"key": "dup", "op": "get"},
	}})
	if batch.Status != "failed" && batch.Status != "partial" {
		t.Fatalf("status = %q, want failed/partial: %+v", batch.Status, batch)
	}
	var found bool
	for _, result := range batch.Results {
		if result.Error != nil && result.Error.Code == "validation_failed" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected a validation_failed error: %+v", batch)
	}
}

func TestTaskManageSameKeyCreateThenUpdateAndGetInBothModes(t *testing.T) {
	for _, mode := range []string{"sequential", "atomic"} {
		t.Run(mode, func(t *testing.T) {
			manager := NewTodoManager()
			batch := executeTaskManage(t, manager, map[string]any{
				"mode": mode,
				"operations": []any{
					createOperation("work", "Do work"),
					map[string]any{"key": "work", "op": "update", "status": "in_progress"},
					map[string]any{"key": "work", "op": "get"},
				},
			})
			if batch.Status != "succeeded" || len(batch.Results) != 3 {
				t.Fatalf("batch failed: %+v", batch)
			}
			for i := 1; i < len(batch.Results); i++ {
				if batch.Results[i].Data.Task.ID != batch.Results[0].Data.Task.ID {
					t.Fatalf("result %d targeted stale task: %+v", i, batch)
				}
			}
		})
	}
}

func TestTaskManageSameBatchCreateShadowsOldRegistryKey(t *testing.T) {
	for _, mode := range []string{"sequential", "atomic"} {
		t.Run(mode, func(t *testing.T) {
			manager := NewTodoManager()
			if err := manager.AddTodo(TodoItem{ID: "old", Content: "Old", Status: TodoStatusPending, Priority: TodoPriorityMedium}); err != nil {
				t.Fatal(err)
			}
			if err := manager.AddTodo(TodoItem{ID: "dependency", Content: "Dependency", Status: TodoStatusPending, Priority: TodoPriorityMedium}); err != nil {
				t.Fatal(err)
			}
			manager.RememberTaskKey("reuse", "old")

			batch := executeTaskManage(t, manager, map[string]any{
				"mode": mode,
				"operations": []any{
					createOperation("reuse", "New"),
					map[string]any{"key": "reuse", "op": "update", "subject": "Updated new", "addBlockedBy": []any{"dependency"}},
				},
			})
			if batch.Status != "succeeded" {
				t.Fatalf("batch failed: %+v", batch)
			}
			if old := manager.ByID("old"); old.Content != "Old" || len(old.DependsOn) != 0 {
				t.Fatalf("stale registered task was mutated: %+v", old)
			}
			updated := batch.Results[1].Data.Task
			if updated.Content != "Updated new" || len(updated.DependsOn) != 1 || updated.DependsOn[0] != "dependency" {
				t.Fatalf("new task was not updated: %+v", updated)
			}
		})
	}
}
