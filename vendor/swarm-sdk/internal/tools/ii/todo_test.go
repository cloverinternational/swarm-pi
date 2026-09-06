// Package ii provides tests for the todo tools
package ii

import (
	"context"
	"testing"
)

func TestTodoManager_SetAndGetTodos(t *testing.T) {
	manager := NewTodoManager()

	// Test empty state
	todos := manager.Todos()
	if len(todos) != 0 {
		t.Errorf("expected empty todos, got %d", len(todos))
	}

	// Test setting todos
	testTodos := []TodoItem{
		{ID: "1", Content: "First task", Status: TodoStatusPending, Priority: TodoPriorityHigh},
		{ID: "2", Content: "Second task", Status: TodoStatusInProgress, Priority: TodoPriorityMedium},
		{ID: "3", Content: "Third task", Status: TodoStatusCompleted, Priority: TodoPriorityLow},
	}

	err := manager.SetTodos(testTodos)
	if err != nil {
		t.Fatalf("unexpected error setting todos: %v", err)
	}

	// Verify todos were set
	todos = manager.Todos()
	if len(todos) != 3 {
		t.Errorf("expected 3 todos, got %d", len(todos))
	}

	// Verify the data is correct
	if todos[0].Content != "First task" {
		t.Errorf("expected 'First task', got '%s'", todos[0].Content)
	}
}

func TestTodoManager_ValidationErrors(t *testing.T) {
	manager := NewTodoManager()

	tests := []struct {
		name    string
		todos   []TodoItem
		wantErr bool
	}{
		{
			name: "empty content",
			todos: []TodoItem{
				{ID: "1", Content: "", Status: TodoStatusPending, Priority: TodoPriorityHigh},
			},
			wantErr: true,
		},
		{
			name: "empty id",
			todos: []TodoItem{
				{ID: "", Content: "Task", Status: TodoStatusPending, Priority: TodoPriorityHigh},
			},
			wantErr: true,
		},
		{
			name: "invalid status",
			todos: []TodoItem{
				{ID: "1", Content: "Task", Status: "invalid", Priority: TodoPriorityHigh},
			},
			wantErr: true,
		},
		{
			name: "invalid priority",
			todos: []TodoItem{
				{ID: "1", Content: "Task", Status: TodoStatusPending, Priority: "invalid"},
			},
			wantErr: true,
		},
		{
			name: "multiple in_progress",
			todos: []TodoItem{
				{ID: "1", Content: "Task 1", Status: TodoStatusInProgress, Priority: TodoPriorityHigh},
				{ID: "2", Content: "Task 2", Status: TodoStatusInProgress, Priority: TodoPriorityHigh},
			},
			wantErr: false, // Multiple in_progress tasks are now allowed
		},
		{
			name: "valid todos",
			todos: []TodoItem{
				{ID: "1", Content: "Task 1", Status: TodoStatusPending, Priority: TodoPriorityHigh},
				{ID: "2", Content: "Task 2", Status: TodoStatusInProgress, Priority: TodoPriorityMedium},
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := manager.SetTodos(tt.todos)
			if (err != nil) != tt.wantErr {
				t.Errorf("SetTodos() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestTodoManager_Summary(t *testing.T) {
	manager := NewTodoManager()

	testTodos := []TodoItem{
		{ID: "1", Content: "Task 1", Status: TodoStatusPending, Priority: TodoPriorityHigh},
		{ID: "2", Content: "Task 2", Status: TodoStatusPending, Priority: TodoPriorityMedium},
		{ID: "3", Content: "Task 3", Status: TodoStatusInProgress, Priority: TodoPriorityLow},
		{ID: "4", Content: "Task 4", Status: TodoStatusCompleted, Priority: TodoPriorityHigh},
	}

	err := manager.SetTodos(testTodos)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	summary := manager.Summary()

	if summary["total"] != 4 {
		t.Errorf("expected total 4, got %d", summary["total"])
	}
	if summary["pending"] != 2 {
		t.Errorf("expected pending 2, got %d", summary["pending"])
	}
	if summary["in_progress"] != 1 {
		t.Errorf("expected in_progress 1, got %d", summary["in_progress"])
	}
	if summary["completed"] != 1 {
		t.Errorf("expected completed 1, got %d", summary["completed"])
	}
}

func TestTodoReadTool_Execute(t *testing.T) {
	manager := NewTodoManager()
	tool := NewTodoReadToolWithManager(manager)

	ctx := context.Background()

	// Test empty list
	result, err := tool.Execute(ctx, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Output != TodoReadEmptyMsg {
		t.Errorf("expected empty message, got: %s", result.Output)
	}

	// Add some todos
	testTodos := []TodoItem{
		{ID: "1", Content: "Test task", Status: TodoStatusPending, Priority: TodoPriorityHigh},
	}
	err = manager.SetTodos(testTodos)
	if err != nil {
		t.Fatalf("unexpected error setting todos: %v", err)
	}

	// Test with todos
	result, err = tool.Execute(ctx, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Output) == 0 {
		t.Error("expected non-empty output")
	}
}

func TestTodoWriteTool_Execute(t *testing.T) {
	manager := NewTodoManager()
	tool := NewTodoWriteToolWithManager(manager)

	ctx := context.Background()

	// Test writing todos
	params := map[string]any{
		"todos": []any{
			map[string]any{
				"id":       "1",
				"content":  "First task",
				"status":   "pending",
				"priority": "high",
			},
			map[string]any{
				"id":       "2",
				"content":  "Second task",
				"status":   "in_progress",
				"priority": "medium",
			},
		},
	}

	result, err := tool.Execute(ctx, params)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Errorf("expected success, got error: %s", result.Output)
	}

	// Verify todos were saved
	todos := manager.Todos()
	if len(todos) != 2 {
		t.Errorf("expected 2 todos, got %d", len(todos))
	}
}

func TestTodoWriteTool_ValidationErrors(t *testing.T) {
	manager := NewTodoManager()
	tool := NewTodoWriteToolWithManager(manager)

	ctx := context.Background()

	tests := []struct {
		name        string
		params      map[string]any
		expectError bool
	}{
		{
			name:        "missing todos parameter",
			params:      map[string]any{},
			expectError: true,
		},
		{
			name:        "null todos",
			params:      map[string]any{"todos": nil},
			expectError: true,
		},
		{
			name:        "invalid todos type",
			params:      map[string]any{"todos": "not an array"},
			expectError: true,
		},
		{
			name: "missing required field",
			params: map[string]any{
				"todos": []any{
					map[string]any{
						"id":      "1",
						"content": "Task",
						// missing status and priority
					},
				},
			},
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := tool.Execute(ctx, tt.params)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if tt.expectError && !containsError(result.Output) {
				t.Errorf("expected error in output, got: %s", result.Output)
			}
		})
	}
}

func TestTodoItemFromMap(t *testing.T) {
	m := map[string]any{
		"id":       "1",
		"content":  "Test task",
		"status":   "pending",
		"priority": "high",
	}

	item, err := TodoItemFromMap(m)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if item.ID != "1" {
		t.Errorf("expected ID '1', got '%s'", item.ID)
	}
	if item.Content != "Test task" {
		t.Errorf("expected content 'Test task', got '%s'", item.Content)
	}
	if item.Status != TodoStatusPending {
		t.Errorf("expected status pending, got '%s'", item.Status)
	}
	if item.Priority != TodoPriorityHigh {
		t.Errorf("expected priority high, got '%s'", item.Priority)
	}
}

func TestTodoItemToMap(t *testing.T) {
	item := TodoItem{
		ID:       "1",
		Content:  "Test task",
		Status:   TodoStatusPending,
		Priority: TodoPriorityHigh,
	}

	m := TodoItemToMap(item)

	if m["id"] != "1" {
		t.Errorf("expected id '1', got '%v'", m["id"])
	}
	if m["content"] != "Test task" {
		t.Errorf("expected content 'Test task', got '%v'", m["content"])
	}
	if m["status"] != "pending" {
		t.Errorf("expected status 'pending', got '%v'", m["status"])
	}
	if m["priority"] != "high" {
		t.Errorf("expected priority 'high', got '%v'", m["priority"])
	}
}

func containsError(s string) bool {
	return len(s) >= 5 && s[:5] == "ERROR"
}
