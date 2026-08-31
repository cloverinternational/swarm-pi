package ii

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

type cancelAfterFinalOperationContext struct {
	context.Context
	errCalls int
}

func (c *cancelAfterFinalOperationContext) Err() error {
	c.errCalls++
	if c.errCalls > 1 {
		return context.Canceled
	}
	return nil
}

func executeTaskManage(t *testing.T, manager *TodoManager, params map[string]any) TaskBatchResult {
	t.Helper()
	result, err := NewTaskManageToolWithManager(manager).Execute(context.Background(), params)
	if err != nil {
		t.Fatal(err)
	}
	var batch TaskBatchResult
	if err := json.Unmarshal([]byte(result.Output), &batch); err != nil {
		t.Fatalf("unmarshal %q: %v", result.Output, err)
	}
	return batch
}

func executeTaskManageOutput(t *testing.T, manager *TodoManager, params map[string]any) (TaskBatchResult, string) {
	t.Helper()
	result, err := NewTaskManageToolWithManager(manager).Execute(context.Background(), params)
	if err != nil {
		t.Fatal(err)
	}
	var batch TaskBatchResult
	if err := json.Unmarshal([]byte(result.Output), &batch); err != nil {
		t.Fatalf("unmarshal %q: %v", result.Output, err)
	}
	return batch, result.Output
}

func createOperation(key, subject string) map[string]any {
	return map[string]any{"key": key, "op": "create", "subject": subject, "description": subject + " details"}
}

func taskRef(key string) map[string]any {
	return map[string]any{"ref": key, "field": "taskId"}
}

func TestTaskManageSequentialReferencesAndCommittedPrefix(t *testing.T) {
	manager := NewTodoManager()
	batch := executeTaskManage(t, manager, map[string]any{"operations": []any{
		createOperation("build", "Build"),
		map[string]any{"key": "start", "op": "update", "taskId": taskRef("build"), "status": "in_progress", "active": true},
		map[string]any{"key": "missing", "op": "get", "taskId": "999"},
		createOperation("skipped", "Skipped"),
	}})
	if batch.Status != "partial" {
		t.Fatalf("status = %q, want partial: %+v", batch.Status, batch)
	}
	gotStatuses := []string{batch.Results[0].Status, batch.Results[1].Status, batch.Results[2].Status, batch.Results[3].Status}
	if want := []string{"succeeded", "succeeded", "failed", "skipped"}; !reflect.DeepEqual(gotStatuses, want) {
		t.Fatalf("statuses = %v, want %v", gotStatuses, want)
	}
	task := manager.ByID("1")
	if task == nil || task.Status != TodoStatusInProgress || !task.Active {
		t.Fatalf("committed prefix not preserved: %+v", task)
	}
	if manager.Count() != 1 {
		t.Fatalf("count = %d, want 1", manager.Count())
	}
}

func TestTaskManageCreateHierarchyAndDependencyReferences(t *testing.T) {
	manager := NewTodoManager()
	batch := executeTaskManage(t, manager, map[string]any{"operations": []any{
		createOperation("parent", "Parent"),
		map[string]any{"key": "child", "op": "create", "subject": "Child", "description": "Child details", "parentTaskId": taskRef("parent")},
		map[string]any{"key": "verify", "op": "create", "subject": "Verify", "description": "Verify details"},
		map[string]any{"key": "link", "op": "update", "taskId": taskRef("verify"), "addBlockedBy": []any{taskRef("child")}},
		map[string]any{"key": "list", "op": "list"},
	}})
	if batch.Status != "succeeded" {
		t.Fatalf("batch failed: %+v", batch)
	}
	if got := manager.ByID("2").ParentID; got != "1" {
		t.Fatalf("child parent = %q, want 1", got)
	}
	if got := manager.ByID("3").DependsOn; !reflect.DeepEqual(got, []string{"2"}) {
		t.Fatalf("dependencies = %v, want [2]", got)
	}
	if got := len(batch.Results[4].Data.Tasks); got != 3 {
		t.Fatalf("list count = %d, want 3", got)
	}
}

func TestTaskManageCreateAcceptsDependencyReferences(t *testing.T) {
	manager := NewTodoManager()
	batch := executeTaskManage(t, manager, map[string]any{"operations": []any{
		createOperation("prerequisite", "Prerequisite"),
		createOperation("target", "Target"),
		map[string]any{
			"key":          "blocked",
			"op":           "create",
			"subject":      "Blocked",
			"description":  "Blocked details",
			"addBlockedBy": []any{taskRef("prerequisite")},
		},
		map[string]any{
			"key":         "blocker",
			"op":          "create",
			"subject":     "Blocker",
			"description": "Blocker details",
			"addBlocks":   []any{taskRef("target")},
		},
	}})
	if batch.Status != "succeeded" {
		t.Fatalf("batch failed: %+v", batch)
	}
	if got := manager.ByID("3").DependsOn; !reflect.DeepEqual(got, []string{"1"}) {
		t.Fatalf("addBlockedBy dependencies = %v, want [1]", got)
	}
	if got := manager.ByID("2").DependsOn; !reflect.DeepEqual(got, []string{"4"}) {
		t.Fatalf("addBlocks dependencies = %v, want [4]", got)
	}
}

func TestTaskManageCreateDependencyFailureDoesNotLeakTask(t *testing.T) {
	for _, field := range []string{"addBlockedBy", "addBlocks"} {
		t.Run(field, func(t *testing.T) {
			manager := NewTodoManager()
			batch := executeTaskManage(t, manager, map[string]any{"operations": []any{
				map[string]any{
					"key":         "create",
					"op":          "create",
					"subject":     "Create",
					"description": "Create details",
					field:         []any{"999"},
				},
			}})
			if batch.Status != "failed" {
				t.Fatalf("status = %q, want failed: %+v", batch.Status, batch)
			}
			if manager.Count() != 0 {
				t.Fatalf("failed create leaked %d task(s)", manager.Count())
			}
			if batch.Results[0].Error == nil || batch.Results[0].Error.Code != "not_found" {
				t.Fatalf("error = %+v, want not_found", batch.Results[0].Error)
			}
		})
	}
}

func TestTaskManageRejectsForwardAndInvalidReferences(t *testing.T) {
	tests := []struct {
		name       string
		operations []any
		code       string
	}{
		{"forward", []any{map[string]any{"key": "first", "op": "get", "taskId": taskRef("later")}, createOperation("later", "Later")}, "reference_failed"},
		{"bad field", []any{createOperation("one", "One"), map[string]any{"key": "two", "op": "get", "taskId": map[string]any{"ref": "one", "field": "subject"}}}, "validation_failed"},
		{"duplicate", []any{createOperation("same", "One"), createOperation("same", "Two")}, "validation_failed"},
		{"invalid array member", []any{createOperation("one", "One"), map[string]any{"key": "two", "op": "update", "taskId": taskRef("one"), "addBlockedBy": []any{42}}}, "validation_failed"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			batch := executeTaskManage(t, NewTodoManager(), map[string]any{"operations": tt.operations})
			if batch.Status != "failed" && batch.Status != "partial" {
				t.Fatalf("unexpected status: %+v", batch)
			}
			var found bool
			for _, result := range batch.Results {
				if result.Error != nil && result.Error.Code == tt.code {
					found = true
				}
			}
			if !found {
				t.Fatalf("missing error code %q: %+v", tt.code, batch)
			}
		})
	}
}

type countingTaskSyncer struct {
	mu    sync.Mutex
	calls int
	err   error
}

func (s *countingTaskSyncer) Sync([]TodoItem) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls++
	return s.err
}

func (s *countingTaskSyncer) Calls() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls
}

func TestTaskManageAtomicPersistsOnceAndReadsDraft(t *testing.T) {
	manager := NewTodoManager()
	syncer := &countingTaskSyncer{}
	manager.SetSyncer(syncer)
	batch := executeTaskManage(t, manager, map[string]any{"mode": "atomic", "operations": []any{
		createOperation("one", "One"),
		map[string]any{"key": "start", "op": "update", "taskId": taskRef("one"), "status": "in_progress"},
		map[string]any{"key": "read", "op": "get", "taskId": taskRef("one")},
	}})
	if batch.Status != "succeeded" {
		t.Fatalf("atomic batch failed: %+v", batch)
	}
	if syncer.Calls() != 1 {
		t.Fatalf("sync calls = %d, want 1", syncer.Calls())
	}
	if got := batch.Results[2].Data.Task.Status; got != TodoStatusInProgress {
		t.Fatalf("draft read status = %q", got)
	}
}

func TestTaskManageAtomicRollsBackOnOperationAndPersistenceFailure(t *testing.T) {
	t.Run("operation", func(t *testing.T) {
		manager := NewTodoManager()
		batch := executeTaskManage(t, manager, map[string]any{"mode": "atomic", "operations": []any{
			createOperation("one", "One"),
			map[string]any{"key": "missing", "op": "update", "taskId": "999", "status": "completed"},
			createOperation("later", "Later"),
		}})
		if batch.Status != "failed" || manager.Count() != 0 {
			t.Fatalf("atomic operation failure leaked state: batch=%+v count=%d", batch, manager.Count())
		}
		if batch.Results[0].Error == nil || batch.Results[0].Error.Code != "atomic_rollback" {
			t.Fatalf("first operation was not marked rolled back: %+v", batch.Results)
		}
	})

	t.Run("persistence", func(t *testing.T) {
		manager := NewTodoManager()
		syncer := &countingTaskSyncer{err: errors.New("persist failed")}
		manager.SetSyncer(syncer)
		batch := executeTaskManage(t, manager, map[string]any{"mode": "atomic", "operations": []any{createOperation("one", "One")}})
		if batch.Status != "failed" || manager.Count() != 0 || syncer.Calls() != 1 {
			t.Fatalf("persistence failure leaked state: batch=%+v count=%d calls=%d", batch, manager.Count(), syncer.Calls())
		}
		if batch.Results[0].Error == nil || batch.Results[0].Error.Code != "persistence_failed" {
			t.Fatalf("persistence error was not surfaced: %+v", batch.Results)
		}
	})
}

func TestTaskManageLimitAndCancellation(t *testing.T) {
	tooMany := make([]any, maxTaskOperations+1)
	for index := range tooMany {
		tooMany[index] = createOperation(fmt.Sprintf("op-%d", index), "Task")
	}
	batch := executeTaskManage(t, NewTodoManager(), map[string]any{"operations": tooMany})
	if batch.Status != "failed" || batch.Results[0].Error.Code != "validation_failed" {
		t.Fatalf("limit result: %+v", batch)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	result, err := NewTaskManageToolWithManager(NewTodoManager()).Execute(ctx, map[string]any{"operations": []any{createOperation("one", "One")}})
	if err != nil {
		t.Fatal(err)
	}
	var cancelled TaskBatchResult
	if err := json.Unmarshal([]byte(result.Output), &cancelled); err != nil {
		t.Fatal(err)
	}
	if cancelled.Results[0].Error == nil || cancelled.Results[0].Error.Code != "cancelled" {
		t.Fatalf("cancel result: %+v", cancelled)
	}
}

func TestTaskManageAtomicCancellationAfterFinalOperationDoesNotDuplicateResult(t *testing.T) {
	manager := NewTodoManager()
	ctx := &cancelAfterFinalOperationContext{Context: context.Background()}
	result, err := NewTaskManageToolWithManager(manager).Execute(ctx, map[string]any{
		"mode":       "atomic",
		"operations": []any{createOperation("one", "One")},
	})
	if err != nil {
		t.Fatal(err)
	}
	var batch TaskBatchResult
	if err := json.Unmarshal([]byte(result.Output), &batch); err != nil {
		t.Fatal(err)
	}
	if len(batch.Results) != 1 {
		t.Fatalf("result count = %d, want 1: %+v", len(batch.Results), batch.Results)
	}
	if batch.Results[0].Status != "failed" || batch.Results[0].Error == nil || batch.Results[0].Error.Code != "cancelled" {
		t.Fatalf("unexpected cancellation result: %+v", batch.Results[0])
	}
	if manager.ByID("1") != nil {
		t.Fatal("cancelled atomic operation was published")
	}
}

func TestTaskManageUpdateClearsFieldsAndTransfersFocus(t *testing.T) {
	manager := NewTodoManager()
	batch := executeTaskManage(t, manager, map[string]any{"operations": []any{
		map[string]any{"key": "one", "op": "create", "subject": "One", "description": "details", "activeForm": "Doing one"},
		createOperation("two", "Two"),
		map[string]any{"key": "start-one", "op": "update", "taskId": taskRef("one"), "status": "in_progress"},
		map[string]any{"key": "clear", "op": "update", "taskId": taskRef("one"), "description": "", "activeForm": ""},
		map[string]any{"key": "start-two", "op": "update", "taskId": taskRef("two"), "status": "in_progress"},
	}})
	if batch.Status != "succeeded" {
		t.Fatalf("batch failed: %+v", batch)
	}
	one, two := manager.ByID("1"), manager.ByID("2")
	if one.Description != "" || one.ActiveForm != "" {
		t.Fatalf("fields not cleared: %+v", one)
	}
	if one.Active || !two.Active {
		t.Fatalf("focus not transferred: one=%+v two=%+v", one, two)
	}
}

func TestTaskManageRejectsInapplicableAndWrongTypedFields(t *testing.T) {
	tests := []map[string]any{
		{"key": "list", "op": "list", "limit": "many"},
		{"key": "get", "op": "get", "taskId": 42},
		{"key": "create", "op": "create", "subject": 42, "description": "details"},
		{"key": "ref", "op": "get", "taskId": map[string]any{"ref": "one", "field": 42}},
		{"key": "unknown-ref", "op": "get", "taskId": map[string]any{"ref": "one", "extra": true}},
	}
	for index, operation := range tests {
		batch := executeTaskManage(t, NewTodoManager(), map[string]any{"operations": []any{operation}})
		if batch.Status != "failed" || batch.Results[0].Error == nil || batch.Results[0].Error.Code != "validation_failed" {
			t.Fatalf("case %d accepted invalid input: %+v", index, batch)
		}
	}
}

func TestTaskManageRejectsInvalidTopLevelFields(t *testing.T) {
	tests := []map[string]any{
		{"operations": []any{createOperation("one", "One")}, "unexpected": true},
		{"operations": []any{createOperation("one", "One")}, "mode": 42},
	}
	for index, params := range tests {
		batch := executeTaskManage(t, NewTodoManager(), params)
		if batch.Status != "failed" || batch.Results[0].Error == nil || batch.Results[0].Error.Code != "validation_failed" {
			t.Fatalf("case %d accepted invalid top-level input: %+v", index, batch)
		}
	}
}

func TestTaskManageOutputIsDeterministicJSON(t *testing.T) {
	params := map[string]any{"operations": []any{createOperation("one", "One"), map[string]any{"key": "read", "op": "get", "taskId": taskRef("one")}}}
	batch := executeTaskManage(t, NewTodoManager(), params)
	firstJSON, _ := json.Marshal(batch)
	secondJSON, _ := json.Marshal(batch)
	if string(firstJSON) != string(secondJSON) {
		t.Fatalf("nondeterministic marshal:\n%s\n%s", firstJSON, secondJSON)
	}
	if batch.Results[0].Key != "one" || batch.Results[1].Key != "read" {
		t.Fatalf("operation order changed: %+v", batch.Results)
	}
}

// --- Issue #73: ref-based task references silently fail across separate
// TaskManage tool calls. These tests cover the fix: a key produced by one
// TaskManage call must resolve in a later, separate call against the same
// manager (the real-world scoping of the global TodoManager singleton).

func TestTaskManageRefResolvesAcrossSeparateCalls(t *testing.T) {
	manager := NewTodoManager()
	// Call 1: create a task under key "build".
	first := executeTaskManage(t, manager, map[string]any{"operations": []any{createOperation("build", "Build")}})
	if first.Status != "succeeded" {
		t.Fatalf("first call failed: %+v", first)
	}

	// Call 2 (separate TaskManage invocation): reference "build" by its key.
	second := executeTaskManage(t, manager, map[string]any{"operations": []any{
		map[string]any{"key": "start", "op": "update", "taskId": taskRef("build"), "status": "in_progress", "active": true},
	}})
	if second.Status != "succeeded" {
		t.Fatalf("cross-call ref did not resolve: %+v", second)
	}
	task := manager.ByID("1")
	if task == nil || task.Status != TodoStatusInProgress || !task.Active {
		t.Fatalf("cross-call update did not apply: %+v", task)
	}

	// Call 3: the key from call 2 ("start") should also now be usable, and
	// the most recent write for "build" still resolves to task 1.
	third := executeTaskManage(t, manager, map[string]any{"operations": []any{
		map[string]any{"key": "finish", "op": "update", "taskId": taskRef("start"), "status": "completed"},
	}})
	if third.Status != "succeeded" {
		t.Fatalf("cross-call ref to an update-produced key did not resolve: %+v", third)
	}
	if got := manager.ByID("1").Status; got != TodoStatusCompleted {
		t.Fatalf("status = %q, want completed", got)
	}
}

func TestTaskManageRefFallbackErrorIsActionable(t *testing.T) {
	manager := NewTodoManager()
	batch := executeTaskManage(t, manager, map[string]any{"operations": []any{
		map[string]any{"key": "op1", "op": "get", "taskId": taskRef("never-created")},
	}})
	if batch.Status != "failed" {
		t.Fatalf("expected failure: %+v", batch)
	}
	err := batch.Results[0].Error
	if err == nil || err.Code != "reference_failed" {
		t.Fatalf("unexpected error: %+v", batch.Results[0])
	}
	for _, want := range []string{"previous TaskManage call", "literal taskId", "op:\"list\"/op:\"get\""} {
		if !strings.Contains(err.Message, want) {
			t.Fatalf("error message missing %q guidance: %q", want, err.Message)
		}
	}
}

func TestTaskManageAtomicRollbackDoesNotLeakKeyAcrossCalls(t *testing.T) {
	manager := NewTodoManager()
	// Atomic batch where "one" succeeds locally but the batch as a whole
	// rolls back because "missing" fails — "one"'s key must NOT be
	// resolvable from a later call, since task 1 never actually committed.
	batch := executeTaskManage(t, manager, map[string]any{"mode": "atomic", "operations": []any{
		createOperation("one", "One"),
		map[string]any{"key": "missing", "op": "update", "taskId": "999", "status": "completed"},
	}})
	if batch.Status != "failed" || manager.Count() != 0 {
		t.Fatalf("rollback leaked task state: %+v count=%d", batch, manager.Count())
	}

	second := executeTaskManage(t, manager, map[string]any{"operations": []any{
		map[string]any{"key": "op1", "op": "get", "taskId": taskRef("one")},
	}})
	if second.Status != "failed" || second.Results[0].Error == nil || second.Results[0].Error.Code != "reference_failed" {
		t.Fatalf("rolled-back atomic key leaked into the durable registry: %+v", second)
	}
}

func TestTaskManageClearTodosClearsKeyRegistry(t *testing.T) {
	manager := NewTodoManager()
	executeTaskManage(t, manager, map[string]any{"operations": []any{createOperation("build", "Build")}})
	if err := manager.ClearTodos(); err != nil {
		t.Fatalf("ClearTodos: %v", err)
	}
	batch := executeTaskManage(t, manager, map[string]any{"operations": []any{
		map[string]any{"key": "op1", "op": "get", "taskId": taskRef("build")},
	}})
	if batch.Status != "failed" || batch.Results[0].Error == nil || batch.Results[0].Error.Code != "reference_failed" {
		t.Fatalf("stale key survived ClearTodos: %+v", batch)
	}
}

func TestTaskManageListIsBoundedAndPaginated(t *testing.T) {
	manager := NewTodoManager()
	for index := 0; index < 500; index++ {
		if _, err := manager.AddTodoAutoID(TodoItem{
			Content:  fmt.Sprintf("Task %03d", index),
			Status:   TodoStatusPending,
			Priority: TodoPriorityMedium,
			Category: TaskCategoryActing,
		}); err != nil {
			t.Fatal(err)
		}
	}

	batch, output := executeTaskManageOutput(t, manager, map[string]any{"operations": []any{
		map[string]any{"key": "list", "op": "list"},
	}})
	data := batch.Results[0].Data
	if len(output) >= 100000 {
		t.Fatalf("bounded list output length = %d, want < 100000", len(output))
	}
	if got := len(data.Tasks); got != defaultTaskListLimit {
		t.Fatalf("default page length = %d, want %d", got, defaultTaskListLimit)
	}
	if data.Pagination == nil {
		t.Fatal("list response omitted pagination metadata")
	}
	if data.Pagination.Total != 500 || data.Pagination.Offset != 0 || data.Pagination.Limit != defaultTaskListLimit || !data.Pagination.More {
		t.Fatalf("unexpected pagination: %+v", data.Pagination)
	}
}

func TestTaskManageListPagesDeterministicallyWithoutGaps(t *testing.T) {
	manager := NewTodoManager()
	for index := 0; index < 20; index++ {
		if _, err := manager.AddTodoAutoID(TodoItem{Content: fmt.Sprintf("Task %02d", index), Status: TodoStatusPending, Priority: TodoPriorityMedium}); err != nil {
			t.Fatal(err)
		}
	}
	list := func(offset, limit int) *TaskOperationData {
		batch := executeTaskManage(t, manager, map[string]any{"operations": []any{
			map[string]any{"key": fmt.Sprintf("page-%d", offset), "op": "list", "offset": offset, "limit": limit},
		}})
		return batch.Results[0].Data
	}
	first, second, full := list(0, 10), list(10, 10), list(0, 20)
	joined := append(append([]TodoItem{}, first.Tasks...), second.Tasks...)
	if !reflect.DeepEqual(joined, full.Tasks) {
		t.Fatalf("adjacent pages do not equal full query:\njoined=%+v\nfull=%+v", joined, full.Tasks)
	}
	got := make([]string, 0, 20)
	for _, task := range joined {
		got = append(got, task.ID)
	}
	want := make([]string, 20)
	for index := range want {
		want[index] = fmt.Sprintf("%d", index+1)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("adjacent page IDs = %v, want %v", got, want)
	}
	if first.Pagination.Total != 20 || !first.Pagination.More {
		t.Fatalf("first pagination = %+v, want total 20 and more", first.Pagination)
	}
	if second.Pagination.Total != 20 || second.Pagination.More {
		t.Fatalf("second pagination = %+v, want total 20 and no more", second.Pagination)
	}
}

func TestTaskManageListFiltersStatusActiveAndCategory(t *testing.T) {
	manager := NewTodoManager()
	fixtures := []TodoItem{
		{Content: "active acting", Status: TodoStatusInProgress, Priority: TodoPriorityMedium, Active: true, Category: TaskCategoryActing},
		{Content: "inactive acting", Status: TodoStatusInProgress, Priority: TodoPriorityMedium, Category: TaskCategoryActing},
		{Content: "active research", Status: TodoStatusInProgress, Priority: TodoPriorityMedium, Active: true, Category: TaskCategoryResearching},
		{Content: "completed acting", Status: TodoStatusCompleted, Priority: TodoPriorityMedium, Category: TaskCategoryActing},
	}
	for _, fixture := range fixtures {
		if _, err := manager.AddTodoAutoID(fixture); err != nil {
			t.Fatal(err)
		}
	}
	tests := []struct {
		name   string
		filter map[string]any
		match  func(TodoItem) bool
	}{
		{"status", map[string]any{"status": "completed"}, func(task TodoItem) bool { return task.Status == TodoStatusCompleted }},
		{"active", map[string]any{"active": true}, func(task TodoItem) bool { return task.Active }},
		{"category", map[string]any{"category": "researching"}, func(task TodoItem) bool { return task.Category == TaskCategoryResearching }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			operation := map[string]any{"key": tt.name, "op": "list"}
			for key, value := range tt.filter {
				operation[key] = value
			}
			batch := executeTaskManage(t, manager, map[string]any{"operations": []any{operation}})
			tasks := batch.Results[0].Data.Tasks
			if len(tasks) == 0 {
				t.Fatal("filter returned no tasks")
			}
			for _, task := range tasks {
				if !tt.match(task) {
					t.Fatalf("filter returned non-matching task: %+v", task)
				}
			}
		})
	}
}

func TestTaskManageListAndDefaultGetOmitAuditHistory(t *testing.T) {
	manager := NewTodoManager()
	large := strings.Repeat("audit detail ", 100)
	now := time.Now().UTC()
	for index := 0; index < 500; index++ {
		audit := make([]TodoAuditEvent, 20)
		notes := make([]string, 20)
		typed := make([]TodoNote, 20)
		for historyIndex := range audit {
			audit[historyIndex] = TodoAuditEvent{Type: "updated", Timestamp: now, Summary: large}
			notes[historyIndex] = large
			typed[historyIndex] = TodoNote{Type: "observation", Content: large, CreatedAt: now}
		}
		if _, err := manager.AddTodoAutoID(TodoItem{
			Content:     fmt.Sprintf("Task %03d", index),
			Status:      TodoStatusPending,
			Priority:    TodoPriorityMedium,
			AuditEvents: audit,
			Notes:       notes,
			TypedNotes:  typed,
		}); err != nil {
			t.Fatal(err)
		}
	}

	list, output := executeTaskManageOutput(t, manager, map[string]any{"operations": []any{
		map[string]any{"key": "list", "op": "list"},
	}})
	if len(output) >= 100000 {
		t.Fatalf("history-heavy list output length = %d, want < 100000", len(output))
	}
	for _, forbidden := range []string{"audit_events", "typed_notes", `"notes"`} {
		if strings.Contains(output, forbidden) {
			t.Fatalf("list output contains historical field %q", forbidden)
		}
	}
	if len(list.Results[0].Data.Tasks) != defaultTaskListLimit {
		t.Fatalf("list returned %d tasks, want %d", len(list.Results[0].Data.Tasks), defaultTaskListLimit)
	}

	get, getOutput := executeTaskManageOutput(t, manager, map[string]any{"operations": []any{
		map[string]any{"key": "get", "op": "get", "taskId": "1"},
	}})
	task := get.Results[0].Data.Task
	if len(task.Notes) != 20 || len(task.TypedNotes) != 0 || len(task.AuditEvents) != 0 {
		t.Fatalf("default get history: notes=%d typed=%d audit=%d", len(task.Notes), len(task.TypedNotes), len(task.AuditEvents))
	}
	for _, forbidden := range []string{"audit_events", "typed_notes"} {
		if strings.Contains(getOutput, forbidden) {
			t.Fatalf("default get output contains %q", forbidden)
		}
	}

	audited, auditedOutput := executeTaskManageOutput(t, manager, map[string]any{"operations": []any{
		map[string]any{"key": "get-audit", "op": "get", "taskId": "1", "include_audit": true},
	}})
	auditedTask := audited.Results[0].Data.Task
	if len(auditedTask.Notes) != 20 || len(auditedTask.TypedNotes) != 20 || len(auditedTask.AuditEvents) != 20 {
		t.Fatalf("audited get omitted history: notes=%d typed=%d audit=%d", len(auditedTask.Notes), len(auditedTask.TypedNotes), len(auditedTask.AuditEvents))
	}
	for _, required := range []string{"audit_events", "typed_notes", `"notes"`} {
		if !strings.Contains(auditedOutput, required) {
			t.Fatalf("audited get output omitted %q", required)
		}
	}
}

func TestTaskManageCreateAndUpdateUseTerseAcknowledgements(t *testing.T) {
	manager := NewTodoManager()
	_, createOutput := executeTaskManageOutput(t, manager, map[string]any{"operations": []any{
		map[string]any{
			"key": "build", "op": "create", "subject": "Build API",
			"description": "Long acceptance criteria", "metadata": map[string]any{"secret": "not echoed"},
		},
	}})
	for _, forbidden := range []string{"audit_events", "typed_notes", `"notes"`, `"description"`, `"metadata"`, "created_at", "updated_at"} {
		if strings.Contains(createOutput, forbidden) {
			t.Fatalf("create acknowledgement contains %q: %s", forbidden, createOutput)
		}
	}
	for _, required := range []string{`"id":"1"`, `"subject":"Build API"`, `"status":"pending"`, `"active":false`, `"parent_id":""`} {
		if !strings.Contains(createOutput, required) {
			t.Fatalf("create acknowledgement omitted %q: %s", required, createOutput)
		}
	}

	_, updateOutput := executeTaskManageOutput(t, manager, map[string]any{"operations": []any{
		map[string]any{
			"key": "start", "op": "update", "taskId": "1", "status": "in_progress",
			"description": "Updated criteria", "addNote": "private progress",
		},
	}})
	for _, forbidden := range []string{"audit_events", "typed_notes", `"notes"`, "private progress", "created_at", "updated_at"} {
		if strings.Contains(updateOutput, forbidden) {
			t.Fatalf("update acknowledgement contains %q: %s", forbidden, updateOutput)
		}
	}
	for _, required := range []string{`"subject":"Build API"`, `"status":"in_progress"`, `"description":"Updated criteria"`, `"note_added":true`} {
		if !strings.Contains(updateOutput, required) {
			t.Fatalf("update acknowledgement omitted %q: %s", required, updateOutput)
		}
	}
	if persisted := manager.ByID("1"); persisted == nil || len(persisted.Notes) != 1 || persisted.Metadata["secret"] != "not echoed" {
		t.Fatalf("terse responses damaged complete task state: %+v", persisted)
	}
}

func TestTaskManageOwnKeyResolvesAcrossSeparateCalls(t *testing.T) {
	manager := NewTodoManager()
	first := executeTaskManage(t, manager, map[string]any{"operations": []any{
		map[string]any{"key": "build", "op": "create", "subject": "Build"},
	}})
	if first.Status != "succeeded" {
		t.Fatalf("create without description failed: %+v", first)
	}
	if first.Results[0].Data.Task.Description != "" {
		t.Fatalf("omitted description = %q, want empty", first.Results[0].Data.Task.Description)
	}

	second := executeTaskManage(t, manager, map[string]any{"operations": []any{
		map[string]any{"key": "build", "op": "update", "status": "in_progress", "active": true},
	}})
	if second.Status != "succeeded" {
		t.Fatalf("own-key cross-call update failed: %+v", second)
	}
	task := manager.ByID("1")
	if task.Status != TodoStatusInProgress || !task.Active {
		t.Fatalf("own-key update did not apply: %+v", task)
	}
}

func TestTaskOperationsAreReadOnly(t *testing.T) {
	for _, test := range []struct {
		name       string
		operations []TaskOperation
		want       bool
	}{
		{name: "get and list", operations: []TaskOperation{{Kind: TaskOperationGet}, {Kind: TaskOperationList}}, want: true},
		{name: "create", operations: []TaskOperation{{Kind: TaskOperationCreate}}, want: false},
		{name: "update", operations: []TaskOperation{{Kind: TaskOperationUpdate}}, want: false},
		{name: "mixed", operations: []TaskOperation{{Kind: TaskOperationList}, {Kind: TaskOperationUpdate}}, want: false},
		{name: "empty", operations: nil, want: false},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := TaskOperationsAreReadOnly(test.operations); got != test.want {
				t.Fatalf("TaskOperationsAreReadOnly() = %v, want %v", got, test.want)
			}
		})
	}
}
