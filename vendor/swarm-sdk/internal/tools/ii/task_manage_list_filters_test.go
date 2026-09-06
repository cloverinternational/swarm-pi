package ii

import (
	"fmt"
	"testing"
)

// TestTaskManageListBoundedByDefaultLimit proves the core of issue #202: an
// unbounded {"op":"list"} on a large registry does not return the entire
// task set (which blew past the 100,000-char tool-output ceiling in the
// report) — it is bounded by defaultTaskListLimit and reports pagination
// metadata so a caller can page through the rest.
func TestTaskManageListBoundedByDefaultLimit(t *testing.T) {
	manager := NewTodoManager()
	const total = defaultTaskListLimit + 25
	// Each TaskManage call is capped at maxTaskOperations operations, so seed
	// in chunks.
	for start := 0; start < total; start += maxTaskOperations {
		end := start + maxTaskOperations
		if end > total {
			end = total
		}
		ops := make([]any, 0, end-start)
		for i := start; i < end; i++ {
			ops = append(ops, createOperation(fmt.Sprintf("t%d", i), fmt.Sprintf("Task number %d", i)))
		}
		batch := executeTaskManage(t, manager, map[string]any{"operations": ops})
		if batch.Status != "succeeded" {
			t.Fatalf("seed chunk [%d,%d) failed: %+v", start, end, batch)
		}
	}

	listBatch := executeTaskManage(t, manager, map[string]any{"operations": []any{
		map[string]any{"key": "list", "op": "list"},
	}})
	if listBatch.Status != "succeeded" {
		t.Fatalf("list failed: %+v", listBatch)
	}
	data := listBatch.Results[0].Data
	if data == nil || data.Pagination == nil {
		t.Fatalf("expected pagination metadata: %+v", listBatch.Results[0])
	}
	if len(data.Tasks) != defaultTaskListLimit {
		t.Fatalf("unbounded list returned %d tasks, want the default limit %d", len(data.Tasks), defaultTaskListLimit)
	}
	if data.Pagination.Total != total {
		t.Fatalf("pagination.total = %d, want %d", data.Pagination.Total, total)
	}
	if !data.Pagination.More {
		t.Fatalf("pagination.more = false, want true (registry exceeds the default limit)")
	}
}

// TestTaskManageListHonorsExplicitLimitAndOffset covers the pagination
// acceptance test: limit=20 returns at most 20 tasks and offset pages
// through the remainder deterministically.
func TestTaskManageListHonorsExplicitLimitAndOffset(t *testing.T) {
	manager := NewTodoManager()
	for i := 0; i < 45; i++ {
		ops := []any{createOperation(fmt.Sprintf("p%d", i), fmt.Sprintf("Page task %d", i))}
		batch := executeTaskManage(t, manager, map[string]any{"operations": ops})
		if batch.Status != "succeeded" {
			t.Fatalf("seed create %d failed: %+v", i, batch)
		}
	}

	seen := make(map[string]bool)
	offset := 0
	for {
		batch := executeTaskManage(t, manager, map[string]any{"operations": []any{
			map[string]any{"key": "list", "op": "list", "limit": 20, "offset": offset},
		}})
		if batch.Status != "succeeded" {
			t.Fatalf("list at offset %d failed: %+v", offset, batch)
		}
		data := batch.Results[0].Data
		if len(data.Tasks) > 20 {
			t.Fatalf("page at offset %d returned %d tasks, want <= 20", offset, len(data.Tasks))
		}
		for _, task := range data.Tasks {
			if seen[task.ID] {
				t.Fatalf("task %s returned on more than one page", task.ID)
			}
			seen[task.ID] = true
		}
		if !data.Pagination.More {
			break
		}
		offset += 20
		if offset > 200 {
			t.Fatal("pagination did not terminate")
		}
	}
	if len(seen) != 45 {
		t.Fatalf("paged through %d distinct tasks, want 45", len(seen))
	}
}

// TestTaskManageListFiltersByCategoryAndSubjectSubstring covers the second
// #202 acceptance test: combining a category filter with a subject
// substring filter returns only matching tasks.
func TestTaskManageListFiltersByCategoryAndSubjectSubstring(t *testing.T) {
	manager := NewTodoManager()
	seed := []map[string]any{
		{"key": "a", "op": "create", "subject": "Fix login bug", "category": "debugging"},
		{"key": "b", "op": "create", "subject": "Fix logout bug", "category": "debugging"},
		{"key": "c", "op": "create", "subject": "Write login docs", "category": "documenting"},
		{"key": "d", "op": "create", "subject": "Unrelated task", "category": "debugging"},
	}
	ops := make([]any, len(seed))
	for i, s := range seed {
		ops[i] = s
	}
	batch := executeTaskManage(t, manager, map[string]any{"operations": ops})
	if batch.Status != "succeeded" {
		t.Fatalf("seed failed: %+v", batch)
	}

	listBatch := executeTaskManage(t, manager, map[string]any{"operations": []any{
		map[string]any{"key": "list", "op": "list", "category": "debugging", "subject": "login"},
	}})
	if listBatch.Status != "succeeded" {
		t.Fatalf("filtered list failed: %+v", listBatch)
	}
	data := listBatch.Results[0].Data
	if len(data.Tasks) != 1 {
		t.Fatalf("expected exactly 1 matching task, got %d: %+v", len(data.Tasks), data.Tasks)
	}
	if data.Tasks[0].Content != "Fix login bug" {
		t.Fatalf("expected the debugging+login task, got %q", data.Tasks[0].Content)
	}
}

// TestTaskManageListSmallRegistryUnboundedStillReturnsAll is the near-miss
// from #202: an unbounded list on a small registry must keep returning the
// complete set without requiring pagination parameters.
func TestTaskManageListSmallRegistryUnboundedStillReturnsAll(t *testing.T) {
	manager := NewTodoManager()
	ops := []any{createOperation("x", "One"), createOperation("y", "Two"), createOperation("z", "Three")}
	batch := executeTaskManage(t, manager, map[string]any{"operations": ops})
	if batch.Status != "succeeded" {
		t.Fatalf("seed failed: %+v", batch)
	}

	listBatch := executeTaskManage(t, manager, map[string]any{"operations": []any{
		map[string]any{"key": "list", "op": "list"},
	}})
	if listBatch.Status != "succeeded" {
		t.Fatalf("list failed: %+v", listBatch)
	}
	data := listBatch.Results[0].Data
	if len(data.Tasks) != 3 {
		t.Fatalf("expected all 3 tasks, got %d", len(data.Tasks))
	}
	if data.Pagination.More {
		t.Fatalf("pagination.more = true for a registry smaller than the limit")
	}
}
