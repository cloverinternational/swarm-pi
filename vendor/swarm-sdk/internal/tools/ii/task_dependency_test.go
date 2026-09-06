package ii

import (
	"context"
	"strings"
	"testing"
)

func TestTodoItem_ValidateSelfReference(t *testing.T) {
	item := TodoItem{
		ID:        "1",
		Content:   "Task 1",
		Status:    TodoStatusPending,
		Priority:  TodoPriorityMedium,
		DependsOn: []string{"1"},
	}
	err := item.Validate()
	if err == nil {
		t.Fatal("expected error for self-referencing dependency")
	}
	if !strings.Contains(err.Error(), "cannot depend on itself") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestTodoItem_CloneWithDependencies(t *testing.T) {
	item := TodoItem{
		ID:        "1",
		Content:   "Task 1",
		Status:    TodoStatusPending,
		Priority:  TodoPriorityMedium,
		DependsOn: []string{"2", "3"},
		Blocks:    []string{"4"},
	}

	clone := item.Clone()

	// Verify deep copy
	if len(clone.DependsOn) != 2 {
		t.Fatalf("expected 2 dependencies, got %d", len(clone.DependsOn))
	}
	if len(clone.Blocks) != 1 {
		t.Fatalf("expected 1 blocks, got %d", len(clone.Blocks))
	}

	// Modify original and ensure clone is unaffected
	item.DependsOn[0] = "modified"
	if clone.DependsOn[0] == "modified" {
		t.Error("clone was affected by modification to original")
	}
}

func TestValidateDependencies_MissingID(t *testing.T) {
	manager := NewTodoManager()
	todos := []TodoItem{
		{ID: "1", Content: "Task 1", Status: TodoStatusPending, Priority: TodoPriorityMedium},
		{ID: "2", Content: "Task 2", Status: TodoStatusPending, Priority: TodoPriorityMedium, DependsOn: []string{"99"}},
	}

	err := manager.ValidateDependencies(todos)
	if err == nil {
		t.Fatal("expected error for dependency on non-existent task")
	}
	if !strings.Contains(err.Error(), "non-existent task 99") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestValidateDependencies_CircularDirect(t *testing.T) {
	manager := NewTodoManager()
	todos := []TodoItem{
		{ID: "1", Content: "Task 1", Status: TodoStatusPending, Priority: TodoPriorityMedium, DependsOn: []string{"2"}},
		{ID: "2", Content: "Task 2", Status: TodoStatusPending, Priority: TodoPriorityMedium, DependsOn: []string{"1"}},
	}

	err := manager.ValidateDependencies(todos)
	if err == nil {
		t.Fatal("expected error for circular dependency")
	}
	if !strings.Contains(err.Error(), "circular dependency") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestValidateDependencies_CircularTransitive(t *testing.T) {
	manager := NewTodoManager()
	todos := []TodoItem{
		{ID: "1", Content: "Task 1", Status: TodoStatusPending, Priority: TodoPriorityMedium, DependsOn: []string{"3"}},
		{ID: "2", Content: "Task 2", Status: TodoStatusPending, Priority: TodoPriorityMedium, DependsOn: []string{"1"}},
		{ID: "3", Content: "Task 3", Status: TodoStatusPending, Priority: TodoPriorityMedium, DependsOn: []string{"2"}},
	}

	err := manager.ValidateDependencies(todos)
	if err == nil {
		t.Fatal("expected error for transitive circular dependency")
	}
}

func TestValidateDependencies_ValidDAG(t *testing.T) {
	manager := NewTodoManager()
	todos := []TodoItem{
		{ID: "1", Content: "Task 1", Status: TodoStatusPending, Priority: TodoPriorityMedium},
		{ID: "2", Content: "Task 2", Status: TodoStatusPending, Priority: TodoPriorityMedium, DependsOn: []string{"1"}},
		{ID: "3", Content: "Task 3", Status: TodoStatusPending, Priority: TodoPriorityMedium, DependsOn: []string{"1", "2"}},
	}

	err := manager.ValidateDependencies(todos)
	if err != nil {
		t.Fatalf("unexpected error for valid DAG: %v", err)
	}
}

func TestComputeBlocks(t *testing.T) {
	manager := NewTodoManager()
	todos := []TodoItem{
		{ID: "1", Content: "Task 1", Status: TodoStatusPending, Priority: TodoPriorityMedium},
		{ID: "2", Content: "Task 2", Status: TodoStatusPending, Priority: TodoPriorityMedium, DependsOn: []string{"1"}},
		{ID: "3", Content: "Task 3", Status: TodoStatusPending, Priority: TodoPriorityMedium, DependsOn: []string{"1"}},
	}

	err := manager.SetTodos(todos)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// After SetTodos with deps, blocks should be computed
	result := manager.Todos()

	// Task 1 should block tasks 2 and 3
	task1 := findTodoByID(result, "1")
	if task1 == nil {
		t.Fatal("task 1 not found")
	}
	if len(task1.Blocks) != 2 {
		t.Errorf("expected task 1 to block 2 tasks, got %d", len(task1.Blocks))
	}

	// Tasks 2 and 3 should have no blocks
	task2 := findTodoByID(result, "2")
	if task2 == nil {
		t.Fatal("task 2 not found")
	}
	if len(task2.Blocks) != 0 {
		t.Errorf("expected task 2 to block 0 tasks, got %d", len(task2.Blocks))
	}
}

func TestSetTodos_StrictEnforcement(t *testing.T) {
	manager := NewTodoManager()

	// Task 2 depends on task 1, but task 2 is in_progress while task 1 is pending
	todos := []TodoItem{
		{ID: "1", Content: "Task 1", Status: TodoStatusPending, Priority: TodoPriorityMedium},
		{ID: "2", Content: "Task 2", Status: TodoStatusInProgress, Priority: TodoPriorityMedium, DependsOn: []string{"1"}},
	}

	err := manager.SetTodos(todos)
	if err == nil {
		t.Fatal("expected error: task 2 depends on incomplete task 1")
	}
	if !strings.Contains(err.Error(), "dependency 1 is not completed") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestSetTodos_DepsMetAllowed(t *testing.T) {
	manager := NewTodoManager()

	// Task 2 depends on task 1, and task 1 is completed
	todos := []TodoItem{
		{ID: "1", Content: "Task 1", Status: TodoStatusCompleted, Priority: TodoPriorityMedium},
		{ID: "2", Content: "Task 2", Status: TodoStatusInProgress, Priority: TodoPriorityMedium, DependsOn: []string{"1"}},
	}

	err := manager.SetTodos(todos)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestUpdateTodo_BlockedByDependency(t *testing.T) {
	manager := NewTodoManager()
	todos := []TodoItem{
		{ID: "1", Content: "Task 1", Status: TodoStatusPending, Priority: TodoPriorityMedium},
		{ID: "2", Content: "Task 2", Status: TodoStatusPending, Priority: TodoPriorityMedium, DependsOn: []string{"1"}},
	}
	err := manager.SetTodos(todos)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Try to start task 2 while task 1 is pending
	err = manager.UpdateTodo("2", func(item *TodoItem) error {
		item.Status = TodoStatusInProgress
		return nil
	})
	if err == nil {
		t.Fatal("expected error: cannot start task 2 while task 1 is pending")
	}
	if !strings.Contains(err.Error(), "dependency 1 is not completed") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestUpdateTodo_DepsMetCanStart(t *testing.T) {
	manager := NewTodoManager()
	todos := []TodoItem{
		{ID: "1", Content: "Task 1", Status: TodoStatusCompleted, Priority: TodoPriorityMedium},
		{ID: "2", Content: "Task 2", Status: TodoStatusPending, Priority: TodoPriorityMedium, DependsOn: []string{"1"}},
	}
	err := manager.SetTodos(todos)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Task 1 is completed, so task 2 can start
	err = manager.UpdateTodo("2", func(item *TodoItem) error {
		item.Status = TodoStatusInProgress
		return nil
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	task2 := manager.ByID("2")
	if task2.Status != TodoStatusInProgress {
		t.Errorf("expected task 2 to be in_progress, got %s", task2.Status)
	}
}

func TestUpdateTodo_AddDependencyCircular(t *testing.T) {
	manager := NewTodoManager()
	todos := []TodoItem{
		{ID: "1", Content: "Task 1", Status: TodoStatusPending, Priority: TodoPriorityMedium, DependsOn: []string{"2"}},
		{ID: "2", Content: "Task 2", Status: TodoStatusPending, Priority: TodoPriorityMedium},
	}
	err := manager.SetTodos(todos)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Try to add a circular dependency: task 2 depends on task 1 (which already depends on task 2)
	err = manager.UpdateTodo("2", func(item *TodoItem) error {
		item.DependsOn = []string{"1"}
		return nil
	})
	if err == nil {
		t.Fatal("expected error for circular dependency")
	}
	if !strings.Contains(err.Error(), "circular dependency") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestAddTodo_WithDependencies(t *testing.T) {
	manager := NewTodoManager()

	// Add first task
	err := manager.AddTodo(TodoItem{
		ID: "1", Content: "Task 1", Status: TodoStatusPending, Priority: TodoPriorityMedium,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Add second task depending on first
	err = manager.AddTodo(TodoItem{
		ID: "2", Content: "Task 2", Status: TodoStatusPending, Priority: TodoPriorityMedium,
		DependsOn: []string{"1"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify blocks were computed
	task1 := manager.ByID("1")
	if len(task1.Blocks) != 1 || task1.Blocks[0] != "2" {
		t.Errorf("expected task 1 to block [2], got %v", task1.Blocks)
	}
}

func TestAddTodo_DuplicateID(t *testing.T) {
	manager := NewTodoManager()
	err := manager.AddTodo(TodoItem{
		ID: "1", Content: "Task 1", Status: TodoStatusPending, Priority: TodoPriorityMedium,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Try to add another task with the same ID
	err = manager.AddTodo(TodoItem{
		ID: "1", Content: "Duplicate", Status: TodoStatusPending, Priority: TodoPriorityMedium,
	})
	if err == nil {
		t.Fatal("expected error for duplicate ID")
	}
}

func TestAddTodo_MissingDependency(t *testing.T) {
	manager := NewTodoManager()
	err := manager.AddTodo(TodoItem{
		ID: "1", Content: "Task 1", Status: TodoStatusPending, Priority: TodoPriorityMedium,
		DependsOn: []string{"99"},
	})
	if err == nil {
		t.Fatal("expected error for dependency on non-existent task")
	}
}

func TestAddTodo_InProgressWithUnmetDeps(t *testing.T) {
	manager := NewTodoManager()
	err := manager.AddTodo(TodoItem{
		ID: "1", Content: "Task 1", Status: TodoStatusPending, Priority: TodoPriorityMedium,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Try to add task 2 as in_progress when task 1 (its dep) is not completed
	err = manager.AddTodo(TodoItem{
		ID: "2", Content: "Task 2", Status: TodoStatusInProgress, Priority: TodoPriorityMedium,
		DependsOn: []string{"1"},
	})
	if err == nil {
		t.Fatal("expected error: cannot create in_progress task with unmet dependencies")
	}
}

func TestDeleteTodo_BlockedByDependency(t *testing.T) {
	manager := NewTodoManager()
	todos := []TodoItem{
		{ID: "1", Content: "Task 1", Status: TodoStatusPending, Priority: TodoPriorityMedium},
		{ID: "2", Content: "Task 2", Status: TodoStatusPending, Priority: TodoPriorityMedium, DependsOn: []string{"1"}},
	}
	err := manager.SetTodos(todos)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Try to delete task 1 which is depended on by task 2
	err = manager.DeleteTodo("1")
	if err == nil {
		t.Fatal("expected error: task 1 is depended on by task 2")
	}
	if !strings.Contains(err.Error(), "task 2 depends on it") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestDeleteTodo_NoDependents(t *testing.T) {
	manager := NewTodoManager()
	todos := []TodoItem{
		{ID: "1", Content: "Task 1", Status: TodoStatusPending, Priority: TodoPriorityMedium},
		{ID: "2", Content: "Task 2", Status: TodoStatusPending, Priority: TodoPriorityMedium},
	}
	err := manager.SetTodos(todos)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Delete task 2 (no dependents)
	err = manager.DeleteTodo("2")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if manager.Count() != 1 {
		t.Errorf("expected 1 todo remaining, got %d", manager.Count())
	}
}

func TestCheckDependenciesMet(t *testing.T) {
	manager := NewTodoManager()
	todos := []TodoItem{
		{ID: "1", Content: "Task 1", Status: TodoStatusPending, Priority: TodoPriorityMedium},
		{ID: "2", Content: "Task 2", Status: TodoStatusPending, Priority: TodoPriorityMedium, DependsOn: []string{"1"}},
	}
	err := manager.SetTodos(todos)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Check deps for task 2 - should fail (task 1 is pending)
	err = manager.CheckDependenciesMet("2")
	if err == nil {
		t.Fatal("expected error: dependency 1 is not completed")
	}

	// Complete task 1
	err = manager.UpdateTodo("1", func(item *TodoItem) error {
		item.Status = TodoStatusInProgress
		return nil
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	err = manager.UpdateTodo("1", func(item *TodoItem) error {
		item.Status = TodoStatusCompleted
		return nil
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Now deps should be met
	err = manager.CheckDependenciesMet("2")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestTaskCreateTool_Execute(t *testing.T) {
	manager := NewTodoManager()
	tool := NewTaskCreateToolWithManager(manager)
	ctx := context.Background()

	params := map[string]any{
		"subject":     "Test task",
		"description": "A test task description",
	}

	result, err := tool.Execute(ctx, params)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.Contains(result.Output, "ERROR") {
		t.Errorf("unexpected error in output: %s", result.Output)
	}
	if manager.Count() != 1 {
		t.Errorf("expected 1 todo, got %d", manager.Count())
	}
}

func TestTaskUpdateTool_AddBlockedBy(t *testing.T) {
	manager := NewTodoManager()

	// Set up initial tasks
	err := manager.SetTodos([]TodoItem{
		{ID: "1", Content: "Task 1", Status: TodoStatusPending, Priority: TodoPriorityMedium},
		{ID: "2", Content: "Task 2", Status: TodoStatusPending, Priority: TodoPriorityMedium},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	tool := NewTaskUpdateToolWithManager(manager)
	ctx := context.Background()

	// Add task 1 as a blocker for task 2
	params := map[string]any{
		"taskId":       "2",
		"addBlockedBy": []any{"1"},
	}

	result, err := tool.Execute(ctx, params)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.Contains(result.Output, "ERROR") {
		t.Errorf("unexpected error in output: %s", result.Output)
	}

	// Verify task 2 now depends on task 1
	task2 := manager.ByID("2")
	if len(task2.DependsOn) != 1 || task2.DependsOn[0] != "1" {
		t.Errorf("expected task 2 to depend on [1], got %v", task2.DependsOn)
	}

	// Verify task 1 blocks task 2
	task1 := manager.ByID("1")
	if len(task1.Blocks) != 1 || task1.Blocks[0] != "2" {
		t.Errorf("expected task 1 to block [2], got %v", task1.Blocks)
	}
}

func TestTaskUpdateTool_AddBlocks(t *testing.T) {
	manager := NewTodoManager()

	err := manager.SetTodos([]TodoItem{
		{ID: "1", Content: "Task 1", Status: TodoStatusPending, Priority: TodoPriorityMedium},
		{ID: "2", Content: "Task 2", Status: TodoStatusPending, Priority: TodoPriorityMedium},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	tool := NewTaskUpdateToolWithManager(manager)
	ctx := context.Background()

	// Task 1 blocks task 2 (meaning task 2 should depend on task 1)
	params := map[string]any{
		"taskId":    "1",
		"addBlocks": []any{"2"},
	}

	result, err := tool.Execute(ctx, params)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.Contains(result.Output, "ERROR") {
		t.Errorf("unexpected error in output: %s", result.Output)
	}

	// Verify task 2 now depends on task 1
	task2 := manager.ByID("2")
	if len(task2.DependsOn) != 1 || task2.DependsOn[0] != "1" {
		t.Errorf("expected task 2 to depend on [1], got %v", task2.DependsOn)
	}
}

func TestTaskUpdateTool_StatusTransitionBlocked(t *testing.T) {
	manager := NewTodoManager()

	err := manager.SetTodos([]TodoItem{
		{ID: "1", Content: "Task 1", Status: TodoStatusPending, Priority: TodoPriorityMedium},
		{ID: "2", Content: "Task 2", Status: TodoStatusPending, Priority: TodoPriorityMedium, DependsOn: []string{"1"}},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	tool := NewTaskUpdateToolWithManager(manager)
	ctx := context.Background()

	// Try to start task 2 while task 1 is pending
	params := map[string]any{
		"taskId": "2",
		"status": "in_progress",
	}

	result, err := tool.Execute(ctx, params)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result.Output, "ERROR") {
		t.Error("expected error: should not be able to start task with unmet dependencies")
	}
}

func TestTaskUpdateTool_DeleteTask(t *testing.T) {
	manager := NewTodoManager()

	err := manager.SetTodos([]TodoItem{
		{ID: "1", Content: "Task 1", Status: TodoStatusPending, Priority: TodoPriorityMedium},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	tool := NewTaskUpdateToolWithManager(manager)
	ctx := context.Background()

	params := map[string]any{
		"taskId": "1",
		"status": "deleted",
	}

	result, err := tool.Execute(ctx, params)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.Contains(result.Output, "ERROR") {
		t.Errorf("unexpected error: %s", result.Output)
	}
	if manager.Count() != 0 {
		t.Errorf("expected 0 todos, got %d", manager.Count())
	}
}

func TestTaskGetTool_Execute(t *testing.T) {
	manager := NewTodoManager()

	err := manager.SetTodos([]TodoItem{
		{ID: "1", Content: "Task 1", Status: TodoStatusPending, Priority: TodoPriorityMedium},
		{ID: "2", Content: "Task 2", Status: TodoStatusPending, Priority: TodoPriorityMedium, DependsOn: []string{"1"}},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	tool := NewTaskGetToolWithManager(manager)
	ctx := context.Background()

	result, err := tool.Execute(ctx, map[string]any{"taskId": "2"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.Contains(result.Output, "ERROR") {
		t.Errorf("unexpected error: %s", result.Output)
	}
	if !strings.Contains(result.Output, "Task 2") {
		t.Error("expected output to contain task content")
	}
	if !strings.Contains(result.Output, "blockedBy") {
		t.Error("expected output to show blockedBy info")
	}
}

func TestTaskGetTool_NotFound(t *testing.T) {
	manager := NewTodoManager()
	tool := NewTaskGetToolWithManager(manager)
	ctx := context.Background()

	result, err := tool.Execute(ctx, map[string]any{"taskId": "99"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result.Output, "ERROR") {
		t.Error("expected error for non-existent task")
	}
}

func TestTaskListTool_Execute(t *testing.T) {
	manager := NewTodoManager()

	err := manager.SetTodos([]TodoItem{
		{ID: "1", Content: "Task 1", Status: TodoStatusCompleted, Priority: TodoPriorityMedium},
		{ID: "2", Content: "Task 2", Status: TodoStatusPending, Priority: TodoPriorityMedium, DependsOn: []string{"1"}},
		{ID: "3", Content: "Task 3", Status: TodoStatusPending, Priority: TodoPriorityHigh, DependsOn: []string{"2"}},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	tool := NewTaskListToolWithManager(manager)
	ctx := context.Background()

	result, err := tool.Execute(ctx, map[string]any{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Should show all tasks
	if !strings.Contains(result.Output, "Task 1") {
		t.Error("expected output to contain Task 1")
	}
	if !strings.Contains(result.Output, "Task 2") {
		t.Error("expected output to contain Task 2")
	}
	if !strings.Contains(result.Output, "Task 3") {
		t.Error("expected output to contain Task 3")
	}

	// Task 3 should show blocker info (task 2 is not completed)
	if !strings.Contains(result.Output, "blocked by") {
		t.Error("expected output to show blocked by info for task 3")
	}
}

func TestTaskListTool_Empty(t *testing.T) {
	manager := NewTodoManager()
	tool := NewTaskListToolWithManager(manager)
	ctx := context.Background()

	result, err := tool.Execute(ctx, map[string]any{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result.Output, "empty") {
		t.Error("expected empty list message")
	}
}

func TestBackwardCompatibility_NoDependencies(t *testing.T) {
	manager := NewTodoManager()

	// Test that tasks without dependencies work exactly as before
	todos := []TodoItem{
		{ID: "1", Content: "Task 1", Status: TodoStatusPending, Priority: TodoPriorityHigh},
		{ID: "2", Content: "Task 2", Status: TodoStatusInProgress, Priority: TodoPriorityMedium},
		{ID: "3", Content: "Task 3", Status: TodoStatusCompleted, Priority: TodoPriorityLow},
	}

	err := manager.SetTodos(todos)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	result := manager.Todos()
	if len(result) != 3 {
		t.Errorf("expected 3 todos, got %d", len(result))
	}

	// Verify no dependency fields are set
	for _, todo := range result {
		if len(todo.DependsOn) != 0 {
			t.Errorf("expected no dependencies for task %s, got %v", todo.ID, todo.DependsOn)
		}
		if len(todo.Blocks) != 0 {
			t.Errorf("expected no blocks for task %s, got %v", todo.ID, todo.Blocks)
		}
	}
}

func TestTodoItemFromMap_WithDependencies(t *testing.T) {
	m := map[string]any{
		"id":         "2",
		"content":    "Task 2",
		"status":     "pending",
		"priority":   "medium",
		"depends_on": []any{"1"},
	}

	item, err := TodoItemFromMap(m)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(item.DependsOn) != 1 || item.DependsOn[0] != "1" {
		t.Errorf("expected depends_on [1], got %v", item.DependsOn)
	}
}

func TestTodoItemToMap_WithDependencies(t *testing.T) {
	item := TodoItem{
		ID:        "2",
		Content:   "Task 2",
		Status:    TodoStatusPending,
		Priority:  TodoPriorityMedium,
		DependsOn: []string{"1"},
		Blocks:    []string{"3"},
	}

	m := TodoItemToMap(item)
	deps, ok := m["depends_on"].([]string)
	if !ok || len(deps) != 1 {
		t.Errorf("expected depends_on [1], got %v", m["depends_on"])
	}
	blocks, ok := m["blocks"].([]string)
	if !ok || len(blocks) != 1 {
		t.Errorf("expected blocks [3], got %v", m["blocks"])
	}
}

func TestTodoItemToMap_NoDependencies(t *testing.T) {
	item := TodoItem{
		ID:       "1",
		Content:  "Task 1",
		Status:   TodoStatusPending,
		Priority: TodoPriorityMedium,
	}

	m := TodoItemToMap(item)
	if _, ok := m["depends_on"]; ok {
		t.Error("expected no depends_on key for task without dependencies")
	}
	if _, ok := m["blocks"]; ok {
		t.Error("expected no blocks key for task without blocks")
	}
}

// Helper to find a todo by ID in a slice
func findTodoByID(todos []TodoItem, id string) *TodoItem {
	for _, todo := range todos {
		if todo.ID == id {
			return &todo
		}
	}
	return nil
}
