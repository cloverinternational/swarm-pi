package builtin

import (
	"context"
	"strings"
	"testing"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/hooks"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/tools/ii"
)

func TestTaskMaintenanceReminderHook_Name(t *testing.T) {
	h := NewTaskMaintenanceReminderHook()
	if h.Name() != "task-maintenance-reminder-hook" {
		t.Errorf("Expected name 'task-maintenance-reminder-hook', got '%s'", h.Name())
	}
}

func TestTaskMaintenanceReminderHook_Priority(t *testing.T) {
	h := NewTaskMaintenanceReminderHook()
	if h.Priority() != ReminderPriority {
		t.Errorf("Expected priority %d, got %d", ReminderPriority, h.Priority())
	}
}

func TestTaskMaintenanceReminderHook_Filter(t *testing.T) {
	h := NewTaskMaintenanceReminderHook()

	tests := []struct {
		name     string
		event    hooks.Event
		expected bool
	}{
		{
			name:     "matches message after receive",
			event:    hooks.Event{Type: hooks.EventMessageAfterReceive},
			expected: true,
		},
		{
			name:     "matches tool after execute",
			event:    hooks.Event{Type: hooks.EventToolAfterExecute},
			expected: true,
		},
		{
			name:     "does not match tool before execute",
			event:    hooks.Event{Type: hooks.EventToolBeforeExecute},
			expected: false,
		},
		{
			name:     "does not match message before send",
			event:    hooks.Event{Type: hooks.EventMessageBeforeSend},
			expected: false,
		},
		{
			name:     "does not match other events",
			event:    hooks.Event{Type: hooks.EventConversationCreated},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := h.Filter(tt.event)
			if result != tt.expected {
				t.Errorf("Filter() = %v, want %v", result, tt.expected)
			}
		})
	}
}

func TestTaskExecutionTracker(t *testing.T) {
	t.Run("increment and count", func(t *testing.T) {
		tracker := &TaskExecutionTracker{}

		if tracker.Count() != 0 {
			t.Errorf("Expected initial count 0, got %d", tracker.Count())
		}

		count := tracker.Increment()
		if count != 1 {
			t.Errorf("Expected count 1 after increment, got %d", count)
		}

		if tracker.Count() != 1 {
			t.Errorf("Expected Count() to return 1, got %d", tracker.Count())
		}
	})

	t.Run("reset", func(t *testing.T) {
		tracker := &TaskExecutionTracker{}

		tracker.Increment()
		tracker.Increment()
		tracker.Reset()

		if tracker.Count() != 0 {
			t.Errorf("Expected count 0 after reset, got %d", tracker.Count())
		}
	})

	t.Run("task ID tracking", func(t *testing.T) {
		tracker := &TaskExecutionTracker{}

		tracker.SetTaskID("task-1")
		if tracker.GetTaskID() != "task-1" {
			t.Errorf("Expected task ID 'task-1', got '%s'", tracker.GetTaskID())
		}

		// Increment a few times
		tracker.Increment()
		tracker.Increment()

		// Setting same task ID should not reset
		tracker.SetTaskID("task-1")
		if tracker.Count() != 2 {
			t.Errorf("Expected count to remain 2, got %d", tracker.Count())
		}

		// Setting different task ID should reset
		tracker.SetTaskID("task-2")
		if tracker.Count() != 0 {
			t.Errorf("Expected count to reset to 0, got %d", tracker.Count())
		}
		if tracker.GetTaskID() != "task-2" {
			t.Errorf("Expected task ID 'task-2', got '%s'", tracker.GetTaskID())
		}
	})

	t.Run("should remind at threshold", func(t *testing.T) {
		tracker := &TaskExecutionTracker{}

		// Should not remind before threshold
		for i := range ToolExecutionThreshold - 1 {
			tracker.Increment()
			if tracker.ShouldRemind() {
				t.Errorf("Should not remind at count %d", i+1)
			}
		}

		// Should remind at threshold
		tracker.Increment()
		if !tracker.ShouldRemind() {
			t.Errorf("Should remind at threshold %d", ToolExecutionThreshold)
		}

		// Should not remind immediately after
		tracker.Increment()
		if tracker.ShouldRemind() {
			t.Errorf("Should not remind immediately after threshold")
		}

		// Should remind again after another threshold
		for range ToolExecutionThreshold - 1 {
			tracker.Increment()
		}
		if !tracker.ShouldRemind() {
			t.Errorf("Should remind again after another %d tools", ToolExecutionThreshold)
		}
	})
}

func TestIsTaskTool(t *testing.T) {
	tests := []struct {
		toolName string
		expected bool
	}{
		{"TaskCreate", true},
		{"task_create", true},
		{"TaskList", true},
		{"task_list", true},
		{"TaskGet", true},
		{"TaskUpdate", true},
		{"TodoWrite", true},
		{"TodoRead", true},
		{"EnterPlanMode", true},
		{"ExitPlanMode", true},
		{"enter_plan_mode", true},
		{"exit_plan_mode", true},
		{"FSRead", false},
		{"Bash", false},
		{"SomeOtherTool", false},
	}

	for _, tt := range tests {
		t.Run(tt.toolName, func(t *testing.T) {
			result := isTaskTool(tt.toolName)
			if result != tt.expected {
				t.Errorf("isTaskTool(%q) = %v, want %v", tt.toolName, result, tt.expected)
			}
		})
	}
}

func TestTaskMaintenanceReminderHook_OnEvent_NoTaskManager(t *testing.T) {
	h := NewTaskMaintenanceReminderHook()

	// Reset and leave nil - the hook will create one via GetTodoManager()
	ii.ResetGlobalManager()

	event := hooks.Event{
		Type: hooks.EventMessageAfterReceive,
		Data: map[string]any{
			"role": "user",
		},
	}

	result, err := h.OnEvent(context.Background(), event)
	if err != nil {
		t.Errorf("Expected no error, got %v", err)
	}
	if result.Action != hooks.ActionContinue {
		t.Errorf("Expected ActionContinue, got %v", result.Action)
	}
	// With no tasks, there's no reminder message
	if result.Message != "" {
		t.Errorf("Expected no message when no tasks, got '%s'", result.Message)
	}
}

func TestTaskMaintenanceReminderHook_OnEvent_UserMessageWithInProgress(t *testing.T) {
	// Reset and get fresh todo manager
	ii.ResetGlobalManager()
	tm := ii.GetTodoManager()

	// Clear any existing tasks
	for _, todo := range tm.Todos() {
		tm.DeleteTodo(todo.ID)
	}

	// Add an in-progress task
	tm.AddTodoAutoID(ii.TodoItem{
		Content:  "Implement feature X",
		Status:   ii.TodoStatusInProgress,
		Priority: ii.TodoPriorityMedium,
	})

	h := NewTaskMaintenanceReminderHook()

	event := hooks.Event{
		Type: hooks.EventMessageAfterReceive,
		Data: map[string]any{
			"role":    "user",
			"content": "How's it going?",
		},
	}

	result, err := h.OnEvent(context.Background(), event)
	if err != nil {
		t.Errorf("Expected no error, got %v", err)
	}
	if result.Action != hooks.ActionContinue {
		t.Errorf("Expected ActionContinue, got %v", result.Action)
	}
	if result.Message == "" {
		t.Error("Expected reminder message for in-progress task")
	}
	if !strings.Contains(result.Message, "Task Maintenance Reminder") {
		t.Errorf("Expected message to contain 'Task Maintenance Reminder', got: %s", result.Message)
	}
}

func TestTaskMaintenanceReminderHook_OnEvent_UserMessageWithPendingOnly(t *testing.T) {
	// Reset and get fresh todo manager
	ii.ResetGlobalManager()
	tm := ii.GetTodoManager()

	// Clear any existing tasks
	for _, todo := range tm.Todos() {
		tm.DeleteTodo(todo.ID)
	}

	// Add only pending tasks
	tm.AddTodoAutoID(ii.TodoItem{
		Content:  "Task 1",
		Status:   ii.TodoStatusPending,
		Priority: ii.TodoPriorityMedium,
	})
	tm.AddTodoAutoID(ii.TodoItem{
		Content:  "Task 2",
		Status:   ii.TodoStatusPending,
		Priority: ii.TodoPriorityMedium,
	})

	h := NewTaskMaintenanceReminderHook()

	event := hooks.Event{
		Type: hooks.EventMessageAfterReceive,
		Data: map[string]any{
			"role":    "user",
			"content": "Start working",
		},
	}

	result, err := h.OnEvent(context.Background(), event)
	if err != nil {
		t.Errorf("Expected no error, got %v", err)
	}
	if result.Action != hooks.ActionContinue {
		t.Errorf("Expected ActionContinue, got %v", result.Action)
	}
	if result.Message == "" {
		t.Error("Expected reminder message for pending tasks")
	}
	if !strings.Contains(result.Message, "pending task(s)") {
		t.Errorf("Expected message about pending tasks, got: %s", result.Message)
	}
}

func TestTaskMaintenanceReminderHook_OnEvent_NonUserMessage(t *testing.T) {
	// Reset and get fresh todo manager
	ii.ResetGlobalManager()
	tm := ii.GetTodoManager()

	// Clear any existing tasks
	for _, todo := range tm.Todos() {
		tm.DeleteTodo(todo.ID)
	}

	// Add an in-progress task
	tm.AddTodoAutoID(ii.TodoItem{
		Content:  "Implement feature X",
		Status:   ii.TodoStatusInProgress,
		Priority: ii.TodoPriorityMedium,
	})

	h := NewTaskMaintenanceReminderHook()

	// Assistant message should not trigger reminder
	event := hooks.Event{
		Type: hooks.EventMessageAfterReceive,
		Data: map[string]any{
			"role":    "assistant",
			"content": "I'm working on it",
		},
	}

	result, err := h.OnEvent(context.Background(), event)
	if err != nil {
		t.Errorf("Expected no error, got %v", err)
	}
	if result.Message != "" {
		t.Errorf("Expected no message for assistant role, got: %s", result.Message)
	}
}

func TestTaskMaintenanceReminderHook_OnEvent_ToolExecutionTracking(t *testing.T) {
	// Reset and get fresh todo manager
	ii.ResetGlobalManager()
	tm := ii.GetTodoManager()

	// Clear any existing tasks
	for _, todo := range tm.Todos() {
		tm.DeleteTodo(todo.ID)
	}

	// Add an in-progress task
	tm.AddTodoAutoID(ii.TodoItem{
		Content:  "Implement feature X",
		Status:   ii.TodoStatusInProgress,
		Priority: ii.TodoPriorityMedium,
	})

	h := NewTaskMaintenanceReminderHook()

	// Execute tools below threshold - should not remind
	for i := range ToolExecutionThreshold - 1 {
		event := hooks.Event{
			Type: hooks.EventToolAfterExecute,
			Data: map[string]any{
				"tool_name": "FSRead",
			},
		}

		result, err := h.OnEvent(context.Background(), event)
		if err != nil {
			t.Errorf("Expected no error, got %v", err)
		}
		if result.Message != "" {
			t.Errorf("Expected no reminder at tool %d, got: %s", i+1, result.Message)
		}
	}

	// Execute the threshold tool - should remind
	event := hooks.Event{
		Type: hooks.EventToolAfterExecute,
		Data: map[string]any{
			"tool_name": "FSRead",
		},
	}

	result, err := h.OnEvent(context.Background(), event)
	if err != nil {
		t.Errorf("Expected no error, got %v", err)
	}
	if result.Message == "" {
		t.Error("Expected reminder at threshold")
	}
	if !strings.Contains(result.Message, "Task Maintenance Reminder") {
		t.Errorf("Expected 'Task Maintenance Reminder' in message, got: %s", result.Message)
	}
}

func TestTaskMaintenanceReminderHook_OnEvent_TaskToolsNotCounted(t *testing.T) {
	// Reset and get fresh todo manager
	ii.ResetGlobalManager()
	tm := ii.GetTodoManager()

	// Clear any existing tasks
	for _, todo := range tm.Todos() {
		tm.DeleteTodo(todo.ID)
	}

	// Add an in-progress task
	tm.AddTodoAutoID(ii.TodoItem{
		Content:  "Implement feature X",
		Status:   ii.TodoStatusInProgress,
		Priority: ii.TodoPriorityMedium,
	})

	h := NewTaskMaintenanceReminderHook()

	// Execute many task tools - should not count toward threshold
	for range ToolExecutionThreshold * 2 {
		event := hooks.Event{
			Type: hooks.EventToolAfterExecute,
			Data: map[string]any{
				"tool_name": "TaskList",
			},
		}

		result, err := h.OnEvent(context.Background(), event)
		if err != nil {
			t.Errorf("Expected no error, got %v", err)
		}
		if result.Message != "" {
			t.Errorf("TaskList should not trigger reminder, got: %s", result.Message)
		}
	}

	// Counter should still be 0
	if h.tracker.Count() != 0 {
		t.Errorf("Expected counter to be 0 after task tools, got %d", h.tracker.Count())
	}
}

func TestTaskMaintenanceReminderHook_OnEvent_NoInProgressTask(t *testing.T) {
	// Reset and get fresh todo manager
	ii.ResetGlobalManager()
	tm := ii.GetTodoManager()

	// Clear any existing tasks
	for _, todo := range tm.Todos() {
		tm.DeleteTodo(todo.ID)
	}

	// Add only completed tasks
	tm.AddTodoAutoID(ii.TodoItem{
		Content:  "Old task",
		Status:   ii.TodoStatusCompleted,
		Priority: ii.TodoPriorityMedium,
	})

	h := NewTaskMaintenanceReminderHook()
	// Pre-populate tracker with some count
	h.tracker.Increment()
	h.tracker.Increment()

	// Execute a tool - should reset tracker since no in-progress task
	event := hooks.Event{
		Type: hooks.EventToolAfterExecute,
		Data: map[string]any{
			"tool_name": "FSRead",
		},
	}

	result, err := h.OnEvent(context.Background(), event)
	if err != nil {
		t.Errorf("Expected no error, got %v", err)
	}
	if result.Message != "" {
		t.Error("Expected no reminder when no in-progress task")
	}

	// Tracker should be reset
	if h.tracker.Count() != 0 {
		t.Errorf("Expected tracker to be reset, got count %d", h.tracker.Count())
	}
}

func TestGetOpenBlockers(t *testing.T) {
	t.Run("no dependencies", func(t *testing.T) {
		task := ii.TodoItem{
			ID:        "task-2",
			DependsOn: nil,
			Status:    ii.TodoStatusPending,
		}
		allTasks := []ii.TodoItem{
			{ID: "task-1", Status: ii.TodoStatusCompleted},
		}

		blockers := getOpenBlockers(task, allTasks)
		if len(blockers) != 0 {
			t.Errorf("Expected no blockers, got %v", blockers)
		}
	})

	t.Run("all dependencies completed", func(t *testing.T) {
		task := ii.TodoItem{
			ID:        "task-3",
			DependsOn: []string{"task-1", "task-2"},
			Status:    ii.TodoStatusPending,
		}
		allTasks := []ii.TodoItem{
			{ID: "task-1", Status: ii.TodoStatusCompleted},
			{ID: "task-2", Status: ii.TodoStatusCompleted},
			{ID: "task-3", Status: ii.TodoStatusPending},
		}

		blockers := getOpenBlockers(task, allTasks)
		if len(blockers) != 0 {
			t.Errorf("Expected no blockers, got %v", blockers)
		}
	})

	t.Run("some dependencies pending", func(t *testing.T) {
		task := ii.TodoItem{
			ID:        "task-3",
			DependsOn: []string{"task-1", "task-2"},
			Status:    ii.TodoStatusPending,
		}
		allTasks := []ii.TodoItem{
			{ID: "task-1", Status: ii.TodoStatusCompleted},
			{ID: "task-2", Status: ii.TodoStatusPending},
			{ID: "task-3", Status: ii.TodoStatusPending},
		}

		blockers := getOpenBlockers(task, allTasks)
		if len(blockers) != 1 || blockers[0] != "task-2" {
			t.Errorf("Expected ['task-2'] blockers, got %v", blockers)
		}
	})

	t.Run("missing dependency", func(t *testing.T) {
		task := ii.TodoItem{
			ID:        "task-2",
			DependsOn: []string{"task-1", "task-3"},
			Status:    ii.TodoStatusPending,
		}
		allTasks := []ii.TodoItem{
			{ID: "task-1", Status: ii.TodoStatusCompleted},
			// task-3 is missing
		}

		blockers := getOpenBlockers(task, allTasks)
		// task-3 is not in allTasks, so it won't show as blocker
		// (this is expected behavior - can't block on unknown task)
		if len(blockers) != 0 {
			t.Errorf("Expected no blockers for missing dependency, got %v", blockers)
		}
	})
}

func TestMessageFormatting(t *testing.T) {
	h := NewTaskMaintenanceReminderHook()

	t.Run("in progress reminder with single task", func(t *testing.T) {
		inProgress := []ii.TodoItem{
			{Content: "Fix bug", Status: ii.TodoStatusInProgress},
		}
		pending := []ii.TodoItem{}

		msg := h.inProgressReminderMessage(inProgress, pending)

		if !strings.Contains(msg, "Fix bug") {
			t.Error("Message should contain task content")
		}
		if !strings.Contains(msg, "1 task in progress") {
			t.Error("Message should indicate 1 task in progress")
		}
	})

	t.Run("in progress reminder with multiple tasks", func(t *testing.T) {
		inProgress := []ii.TodoItem{
			{Content: "Task A", Status: ii.TodoStatusInProgress},
			{Content: "Task B", Status: ii.TodoStatusInProgress},
		}
		pending := []ii.TodoItem{
			{Content: "Task C", Status: ii.TodoStatusPending},
		}

		msg := h.inProgressReminderMessage(inProgress, pending)

		if !strings.Contains(msg, "2 tasks in progress") {
			t.Error("Message should indicate 2 tasks in progress")
		}
		if !strings.Contains(msg, "1 task(s) pending") {
			t.Error("Message should indicate 1 pending task")
		}
	})

	t.Run("start task reminder", func(t *testing.T) {
		pending := []ii.TodoItem{
			{ID: "1", Content: "Task A", Status: ii.TodoStatusPending},
			{ID: "2", Content: "Task B", Status: ii.TodoStatusPending},
			{ID: "3", Content: "Task C", Status: ii.TodoStatusPending, DependsOn: []string{"1"}},
		}

		msg := h.startTaskReminderMessage(pending)

		if !strings.Contains(msg, "3 pending task(s)") {
			t.Error("Message should indicate 3 pending tasks")
		}
		if !strings.Contains(msg, "Task A [available]") {
			t.Error("Message should show available task")
		}
		if !strings.Contains(msg, "Task C (blocked by: 1)") {
			t.Error("Message should show blocked task with blocker")
		}
	})

	t.Run("expanded work reminder", func(t *testing.T) {
		currentTask := ii.TodoItem{Content: "Big task"}
		pending := []ii.TodoItem{
			{Content: "Follow-up", Status: ii.TodoStatusPending},
		}

		msg := h.expandedWorkReminderMessage(10, currentTask, pending)

		if !strings.Contains(msg, "10 tools") {
			t.Error("Message should mention tool count")
		}
		if !strings.Contains(msg, "Big task") {
			t.Error("Message should mention current task")
		}
		if !strings.Contains(msg, "work expanded") {
			t.Error("Message should ask about expanded work")
		}
	})
}

func TestTaskMaintenanceReminderHook_GetTracker(t *testing.T) {
	h := NewTaskMaintenanceReminderHook()
	tracker := h.GetTracker()

	if tracker == nil {
		t.Error("GetTracker() should return non-nil tracker")
	}

	// Verify it's the same tracker
	tracker.Increment()
	if h.tracker.Count() != 1 {
		t.Error("GetTracker() should return the internal tracker")
	}
}
