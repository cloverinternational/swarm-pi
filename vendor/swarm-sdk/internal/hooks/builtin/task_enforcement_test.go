package builtin

import (
	"context"
	"strings"
	"testing"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/hooks"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/tools/ii"
)

func TestTaskEnforcementHook_PlanModeDoesNotChangeAuthorization(t *testing.T) {
	defer ResetPlanModeForTest()
	hook := NewTaskEnforcementHook()
	ctx := context.Background()
	run := func(tool string, params map[string]any) hooks.HookResult {
		t.Helper()
		result, err := hook.OnEvent(ctx, hooks.Event{
			Type: hooks.EventToolBeforeExecute,
			Data: map[string]any{
				"tool_name": tool,
				"params":    params,
			},
		})
		if err != nil {
			t.Fatalf("OnEvent(%s): %v", tool, err)
		}
		return result
	}

	tests := []struct {
		name   string
		tool   string
		params map[string]any
	}{
		{name: "read remains allowed", tool: "Bash", params: map[string]any{"command": "git status --short"}},
		{name: "write follows ordinary task gate", tool: "apply_patch", params: map[string]any{"input": "*** Begin Patch\n*** End Patch"}},
		{name: "task management remains exempt", tool: "TaskManage", params: map[string]any{"operations": []any{map[string]any{"key": "list", "op": "list"}}}},
	}

	before := make(map[string]hooks.HookResult, len(tests))
	for _, test := range tests {
		before[test.name] = run(test.tool, test.params)
	}

	PlanModeEntered()
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			after := run(test.tool, test.params)
			want := before[test.name]
			if after.Action != want.Action ||
				normalizeReminderSequence(after.Message) != normalizeReminderSequence(want.Message) {
				t.Fatalf("plan mode changed authorization: before=%+v after=%+v", want, after)
			}
		})
	}
}

func normalizeReminderSequence(message string) string {
	const marker = ` seq="`
	start := strings.Index(message, marker)
	if start < 0 {
		return message
	}
	valueStart := start + len(marker)
	valueEnd := strings.IndexByte(message[valueStart:], '"')
	if valueEnd < 0 {
		return message
	}
	return message[:valueStart] + message[valueStart+valueEnd:]
}

func TestToolCounter(t *testing.T) {
	counter := &ToolCounter{}

	// Test initial count
	if counter.Count() != 0 {
		t.Errorf("expected initial count 0, got %d", counter.Count())
	}

	// Test increment
	if counter.Increment() != 1 {
		t.Errorf("expected count 1 after first increment, got %d", counter.Count())
	}
	if counter.Increment() != 2 {
		t.Errorf("expected count 2 after second increment, got %d", counter.Count())
	}
	if counter.Increment() != 3 {
		t.Errorf("expected count 3 after third increment, got %d", counter.Count())
	}

	// Test count after increments
	if counter.Count() != 3 {
		t.Errorf("expected count 3, got %d", counter.Count())
	}

	// Test reset
	counter.Reset()
	if counter.Count() != 0 {
		t.Errorf("expected count 0 after reset, got %d", counter.Count())
	}
}

func TestTaskEnforcementHook_Name(t *testing.T) {
	hook := NewTaskEnforcementHook()
	if hook.Name() != "task-enforcement-hook" {
		t.Errorf("expected name 'task-enforcement-hook', got %s", hook.Name())
	}
}

func TestTaskEnforcementHook_Priority(t *testing.T) {
	hook := NewTaskEnforcementHook()
	if hook.Priority() != EnforcementPriority {
		t.Errorf("expected priority %d, got %d", EnforcementPriority, hook.Priority())
	}
}

func TestTaskEnforcementHook_Filter(t *testing.T) {
	hook := NewTaskEnforcementHook()

	tests := []struct {
		eventType  string
		shouldPass bool
	}{
		{hooks.EventToolBeforeExecute, true},
		{hooks.EventToolAfterExecute, false},
		{"user.prompt_submit", false},
		{"agent.stop", false},
	}

	for _, tt := range tests {
		event := hooks.Event{Type: tt.eventType}
		result := hook.Filter(event)
		if result != tt.shouldPass {
			t.Errorf("Filter(%s) = %v, expected %v", tt.eventType, result, tt.shouldPass)
		}
	}
}

func TestTaskEnforcementHook_AllowsExemptTools(t *testing.T) {
	hook := NewTaskEnforcementHook()
	ctx := context.Background()

	// Clear todos first
	tm := ii.GetTodoManager()
	tm.ClearTodos()

	exemptTools := []string{
		"task_create", "task_list", "task_get", "task_update",
		"TodoWrite", "TodoRead",
		"enter_plan_mode", "exit_plan_mode",
		"enterplanmode", "ExitPlanMode",
	}

	for _, toolName := range exemptTools {
		event := hooks.Event{
			Type: hooks.EventToolBeforeExecute,
			Data: map[string]any{"tool_name": toolName},
		}
		result, err := hook.OnEvent(ctx, event)
		if err != nil {
			t.Errorf("OnEvent for tool %s returned error: %v", toolName, err)
		}
		if result.Action != hooks.ActionContinue {
			t.Errorf("expected ActionContinue for exempt tool %s, got %v", toolName, result.Action)
		}
	}

	// Clean up
	tm.ClearTodos()
}

func TestTaskEnforcementHook_BlocksImmediately(t *testing.T) {
	// Clear any existing todos first
	tm := ii.GetTodoManager()
	tm.ClearTodos()

	hook := NewTaskEnforcementHookWithConfig(TaskEnforcementConfig{EnforcementMode: EnforcementModeBlock})
	ctx := context.Background()

	// First non-exempt tool call - should BLOCK IMMEDIATELY
	event := hooks.Event{
		Type: hooks.EventToolBeforeExecute,
		Data: map[string]any{"tool_name": "bash"},
	}
	result, err := hook.OnEvent(ctx, event)
	if err != nil {
		t.Fatalf("OnEvent returned error: %v", err)
	}

	// Should block on first call
	if result.Action != hooks.ActionBlock {
		t.Errorf("expected ActionBlock on first call (immediate blocking), got %v", result.Action)
	}
	if result.Message == "" {
		t.Error("expected block message, got empty string")
	}

	// Clean up
	tm.ClearTodos()
}

func TestTaskEnforcementHook_AllowsWhenTasksExist(t *testing.T) {
	// Clear any existing todos
	tm := ii.GetTodoManager()
	tm.ClearTodos()

	// Create an ACTIVE in-progress task BEFORE hook check. A pending task
	// alone no longer unlocks side effects — the agent must focus a task.
	task := ii.TodoItem{
		ID:       "test-1",
		Content:  "Test task",
		Status:   ii.TodoStatusInProgress,
		Active:   true,
		Priority: ii.TodoPriorityMedium,
	}
	if err := tm.SetTodos([]ii.TodoItem{task}); err != nil {
		t.Fatalf("failed to set todos: %v", err)
	}

	hook := NewTaskEnforcementHook()
	ctx := context.Background()

	// Tool call should ALLOW because a task is focused
	event := hooks.Event{
		Type: hooks.EventToolBeforeExecute,
		Data: map[string]any{"tool_name": "bash"},
	}
	result, err := hook.OnEvent(ctx, event)
	if err != nil {
		t.Fatalf("OnEvent returned error: %v", err)
	}
	if result.Action != hooks.ActionContinue {
		t.Errorf("expected ActionContinue when a task is focused, got %v", result.Action)
	}

	// Counter should be reset
	if hook.GetCounter().Count() != 0 {
		t.Errorf("expected counter to be reset to 0, got %d", hook.GetCounter().Count())
	}

	// Clean up
	tm.ClearTodos()
}

func TestTaskEnforcementHook_BlocksAgainWhenTasksCleared(t *testing.T) {
	// Clear any existing todos
	tm := ii.GetTodoManager()
	tm.ClearTodos()

	hook := NewTaskEnforcementHookWithConfig(TaskEnforcementConfig{EnforcementMode: EnforcementModeBlock})
	ctx := context.Background()

	// First call - should block (no tasks)
	event := hooks.Event{
		Type: hooks.EventToolBeforeExecute,
		Data: map[string]any{"tool_name": "bash"},
	}
	result, _ := hook.OnEvent(ctx, event)
	if result.Action != hooks.ActionBlock {
		t.Errorf("expected ActionBlock on first call, got %v", result.Action)
	}

	// Create an active in-progress task
	task := ii.TodoItem{
		ID:       "test-1",
		Content:  "Test task",
		Status:   ii.TodoStatusInProgress,
		Active:   true,
		Priority: ii.TodoPriorityMedium,
	}
	if err := tm.SetTodos([]ii.TodoItem{task}); err != nil {
		t.Fatalf("failed to set todos: %v", err)
	}

	// Now should allow
	result, _ = hook.OnEvent(ctx, event)
	if result.Action != hooks.ActionContinue {
		t.Errorf("expected ActionContinue when a task is focused, got %v", result.Action)
	}

	// Clear tasks
	tm.ClearTodos()

	// Should block again
	result, _ = hook.OnEvent(ctx, event)
	if result.Action != hooks.ActionBlock {
		t.Errorf("expected ActionBlock after tasks cleared, got %v", result.Action)
	}

	// Clean up
	tm.ClearTodos()
}

func TestIsExemptTool(t *testing.T) {
	tests := []struct {
		toolName string
		expected bool
	}{
		// Task tools - exempt
		{"task_create", true},
		{"task_list", true},
		{"task_get", true},
		{"task_update", true},
		{"TodoWrite", true},
		{"TodoRead", true},
		{"TaskCreate", true},
		{"TaskList", true},
		// Plan mode tools - exempt
		{"enter_plan_mode", true},
		{"exit_plan_mode", true},
		{"enterplanmode", true},
		{"exitplanmode", true},
		{"EnterPlanMode", true},
		{"ExitPlanMode", true},
		// Skill tools - exempt (budget hook can force these; must not deadlock)
		{"SkillManage", true},
		{"skill_manage", true},
		{"Skill", true},
		{"skill", true},
		{"skill_invoke", true},
		{"use_skill", true},
		// Non-exempt tools
		{"bash", false},
		{"read", false},
		{"grep", false},
		{"edit", false},
		{"write", false},
		{"Bash", false},
		{"Read", false},
	}

	for _, tt := range tests {
		result := isExemptTool(tt.toolName)
		if result != tt.expected {
			t.Errorf("isExemptTool(%s) = %v, expected %v", tt.toolName, result, tt.expected)
		}
	}
}

func TestEnforcementMessage(t *testing.T) {
	hook := NewTaskEnforcementHook()
	msg := hook.enforcementMessage()

	// Check that message contains key phrases. The message was tightened in
	// 2026-04-27 to drop ASCII-art wallpaper ("WHY THIS MATTERS" section etc.)
	// while keeping every load-bearing instruction.
	keyPhrases := []string{
		"TASK ENFORCEMENT",
		"BLOCKED",
		"YOU CANNOT EXECUTE ANY TOOL WITHOUT A TASK",
		"TaskManage",
		"enter_plan_mode",
		"RECOMMENDED WORKFLOW",
	}

	for _, phrase := range keyPhrases {
		if !containsString(msg, phrase) {
			t.Errorf("enforcement message missing phrase: %s", phrase)
		}
	}
}

func containsString(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
