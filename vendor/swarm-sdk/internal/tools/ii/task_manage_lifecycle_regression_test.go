package ii

import "testing"

func TestTaskManageDeletionRemovesAllKeysAndAllowsReuse(t *testing.T) {
	for _, mode := range []string{"sequential", "atomic"} {
		t.Run(mode, func(t *testing.T) {
			manager := NewTodoManager()
			created := executeTaskManage(t, manager, map[string]any{"operations": []any{
				createOperation("one", "One"),
			}})
			id := created.Results[0].Data.Task.ID
			manager.RememberTaskKey("alias", id)

			deleted := executeTaskManage(t, manager, map[string]any{"mode": mode, "operations": []any{
				map[string]any{"key": "delete", "op": "update", "taskId": id, "status": "deleted"},
			}})
			if deleted.Status != "succeeded" {
				t.Fatalf("delete failed: %+v", deleted)
			}
			for _, key := range []string{"one", "alias"} {
				if got, ok := manager.ResolveTaskKey(key); ok {
					t.Fatalf("stale key %q survived deletion as %q", key, got)
				}
			}
			reused := executeTaskManage(t, manager, map[string]any{"operations": []any{
				createOperation("one", "Replacement"),
			}})
			if reused.Status != "succeeded" || reused.Results[0].Data.Task.Content != "Replacement" {
				t.Fatalf("key reuse failed: %+v", reused)
			}
			if got, ok := manager.ResolveTaskKey("one"); !ok || got != reused.Results[0].Data.Task.ID {
				t.Fatalf("reused key was not registered to replacement: found=%v id=%q", ok, got)
			}
		})
	}
}

func TestTaskManageAtomicRollbackPreservesDeletedTaskKeys(t *testing.T) {
	manager := NewTodoManager()
	created := executeTaskManage(t, manager, map[string]any{"operations": []any{
		createOperation("keep", "Keep"),
	}})
	id := created.Results[0].Data.Task.ID
	manager.RememberTaskKey("alias", id)
	batch := executeTaskManage(t, manager, map[string]any{"mode": "atomic", "operations": []any{
		map[string]any{"key": "delete", "op": "update", "taskId": id, "status": "deleted"},
		map[string]any{"key": "fail", "op": "get", "taskId": "missing"},
	}})
	if batch.Status != "failed" || manager.ByID(id) == nil {
		t.Fatalf("atomic deletion did not roll back: %+v", batch)
	}
	for _, key := range []string{"keep", "alias"} {
		if got, ok := manager.ResolveTaskKey(key); !ok || got != id {
			t.Fatalf("rollback lost key %q: found=%v id=%q", key, ok, got)
		}
	}
}

func TestTaskManageAtomicCreateThenDeleteDoesNotRegisterStaleKey(t *testing.T) {
	manager := NewTodoManager()
	batch := executeTaskManage(t, manager, map[string]any{"mode": "atomic", "operations": []any{
		createOperation("ephemeral", "Ephemeral"),
		map[string]any{"key": "ephemeral", "op": "update", "status": "deleted"},
	}})
	if batch.Status != "succeeded" || manager.Count() != 0 {
		t.Fatalf("create/delete batch failed: %+v tasks=%+v", batch, manager.Todos())
	}
	if id, ok := manager.ResolveTaskKey("ephemeral"); ok {
		t.Fatalf("deleted atomic task left stale key %q", id)
	}
}

func TestTaskManageInProgressFocusLifecycle(t *testing.T) {
	manager := NewTodoManager()
	batch := executeTaskManage(t, manager, map[string]any{"operations": []any{
		createOperation("one", "One"),
		createOperation("two", "Two"),
		map[string]any{"key": "start-one", "op": "update", "taskId": taskRef("one"), "status": "in_progress"},
		map[string]any{"key": "start-two", "op": "update", "taskId": taskRef("two"), "status": "in_progress"},
		map[string]any{"key": "refocus-one", "op": "update", "taskId": taskRef("one"), "status": "in_progress"},
		map[string]any{"key": "explicit-two", "op": "update", "taskId": taskRef("two"), "active": true},
	}})
	if batch.Status != "succeeded" {
		t.Fatalf("batch failed: %+v", batch)
	}
	if manager.ByID("1").Active || !manager.ByID("2").Active {
		t.Fatalf("explicit focus did not preserve one-active invariant: %+v", manager.Todos())
	}
}
