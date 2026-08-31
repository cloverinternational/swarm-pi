package builtin

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/hooks"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/tools/ii"
)

// TaskMaintenanceReminderHook provides gentle reminders to agents about task maintenance.
// It nudges agents to:
//   - Update task status after user messages (when tasks are in-progress)
//   - Add new tasks if work has expanded (after N tool executions)
//   - Mark completed tasks as done
//
// This hook NEVER blocks - it only provides informational reminders via ContinueWithMessage.
const (
	// ReminderPriority runs after task enforcement (95) but before most other hooks
	ReminderPriority = 90

	// ToolExecutionThreshold is the number of tool executions after which we remind
	// the agent to check if new tasks should be added
	ToolExecutionThreshold = 8

	// UserMessageReminderStride throttles the per-user-message in-progress
	// reminder so it fires every N user messages rather than on every turn.
	// Firing every turn in a multi-turn conversation buried real content under
	// repeated "you have an in-progress task" noise.
	UserMessageReminderStride = 5
)

// TaskExecutionTracker tracks tool executions for the current in-progress task.
// It resets when a task is completed or when there are no in-progress tasks.
type TaskExecutionTracker struct {
	count      int
	lastTaskID string
	remindedAt int // The count at which we last reminded
	msgCount   int // User messages seen for the current task (throttles per-message reminders)
	mu         sync.Mutex
}

// Increment adds 1 to the counter and returns the new count.
func (t *TaskExecutionTracker) Increment() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.count++
	return t.count
}

// Count returns the current counter value.
func (t *TaskExecutionTracker) Count() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.count
}

// Reset sets the counter back to 0.
func (t *TaskExecutionTracker) Reset() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.count = 0
	t.remindedAt = 0
	t.msgCount = 0
}

// ShouldRemindOnMessage returns true once every UserMessageReminderStride
// user messages, so per-turn reminders don't spam multi-turn conversations.
// The first message after a task becomes active (msgCount == 0) also emits
// to provide immediate context, then falls into the stride cadence.
func (t *TaskExecutionTracker) ShouldRemindOnMessage() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	shouldRemind := t.msgCount%UserMessageReminderStride == 0
	t.msgCount++
	return shouldRemind
}

// ShouldRemind returns true if we should show a reminder (every ToolExecutionThreshold tools)
func (t *TaskExecutionTracker) ShouldRemind() bool {
	t.mu.Lock()
	defer t.mu.Unlock()

	// Only remind if we've passed the threshold since last reminder
	if t.count-t.remindedAt >= ToolExecutionThreshold {
		t.remindedAt = t.count
		return true
	}
	return false
}

// SetTaskID updates the tracked task ID and resets if changed.
func (t *TaskExecutionTracker) SetTaskID(taskID string) {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.lastTaskID != taskID {
		t.lastTaskID = taskID
		t.count = 0
		t.remindedAt = 0
		t.msgCount = 0
	}
}

// GetTaskID returns the currently tracked task ID.
func (t *TaskExecutionTracker) GetTaskID() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.lastTaskID
}

// TaskMaintenanceReminderHook reminds agents to maintain their task list.
type TaskMaintenanceReminderHook struct {
	tracker *TaskExecutionTracker
}

// NewTaskMaintenanceReminderHook creates a new task maintenance reminder hook.
func NewTaskMaintenanceReminderHook() *TaskMaintenanceReminderHook {
	return &TaskMaintenanceReminderHook{
		tracker: &TaskExecutionTracker{},
	}
}

// Name returns the hook name for identification.
func (h *TaskMaintenanceReminderHook) Name() string {
	return "task-maintenance-reminder-hook"
}

// Priority returns the hook priority.
func (h *TaskMaintenanceReminderHook) Priority() int {
	return ReminderPriority
}

// Filter returns true for message after receive events and tool after execute events.
func (h *TaskMaintenanceReminderHook) Filter(event hooks.Event) bool {
	return event.Type == hooks.EventMessageAfterReceive ||
		event.Type == hooks.EventToolAfterExecute
}

// OnEvent is the main hook logic - provides gentle reminders about task maintenance.
//
// Logic Flow:
//  1. Handle message after receive events:
//     a. Check if this is a user message
//     b. If there are in-progress tasks, remind to update status
//  2. Handle tool after execute events:
//     a. Track tool execution count
//     b. If count exceeds threshold, remind to check for new tasks
func (h *TaskMaintenanceReminderHook) OnEvent(ctx context.Context, event hooks.Event) (hooks.HookResult, error) {
	tm := ii.TodoManagerFromContext(ctx)
	if tm == nil {
		tm = ii.GetTodoManager()
	}
	if tm == nil {
		// No task manager, can't provide reminders
		return hooks.Continue(), nil
	}

	switch event.Type {
	case hooks.EventMessageAfterReceive:
		return h.handleMessageEvent(ctx, event, tm)
	case hooks.EventToolAfterExecute:
		return h.handleToolEvent(ctx, event, tm)
	default:
		return hooks.Continue(), nil
	}
}

// handleMessageEvent processes message events and reminds about in-progress tasks.
func (h *TaskMaintenanceReminderHook) handleMessageEvent(ctx context.Context, event hooks.Event, tm *ii.TodoManager) (hooks.HookResult, error) {
	// Check if this is a user message
	roleVal, _ := event.Data["role"]
	role, _ := roleVal.(string)
	if role != "user" {
		return hooks.Continue(), nil
	}

	// Get task counts
	pending, inProgress := sessionTaskStatuses(tm)

	// If there are in-progress tasks, remind about them — but throttle so
	// multi-turn conversations don't get a reminder on every user prompt.
	if len(inProgress) > 0 {
		h.tracker.SetTaskID(inProgress[0].ID)
		if !h.tracker.ShouldRemindOnMessage() {
			return hooks.Continue(), nil
		}
		msg, ok := h.wrapReminder(ctx, event, h.inProgressReminderMessage(inProgress, pending))
		if !ok {
			return hooks.Continue(), nil
		}
		return hooks.ContinueWithMessage(msg), nil
	}

	// If there are only pending tasks (no in-progress), suggest starting one.
	// Same throttle: once every N user messages, not every message.
	if len(pending) > 0 && len(inProgress) == 0 {
		if !h.tracker.ShouldRemindOnMessage() {
			return hooks.Continue(), nil
		}
		msg, ok := h.wrapReminder(ctx, event, h.startTaskReminderMessage(pending))
		if !ok {
			return hooks.Continue(), nil
		}
		return hooks.ContinueWithMessage(msg), nil
	}

	return hooks.Continue(), nil
}

// handleToolEvent processes tool execution events and reminds about adding new tasks.
func (h *TaskMaintenanceReminderHook) handleToolEvent(ctx context.Context, event hooks.Event, tm *ii.TodoManager) (hooks.HookResult, error) {
	// Get the tool name
	toolNameVal, _ := event.Data["tool_name"]
	toolName, _ := toolNameVal.(string)
	if toolName == "" {
		return hooks.Continue(), nil
	}

	// Skip task tools themselves from counting
	if isTaskTool(toolName) {
		return hooks.Continue(), nil
	}

	// Check if there are in-progress tasks
	pending, inProgress := sessionTaskStatuses(tm)
	if len(inProgress) == 0 {
		// No in-progress task, reset tracker
		h.tracker.Reset()
		return hooks.Continue(), nil
	}

	// Update tracker with current task
	h.tracker.SetTaskID(inProgress[0].ID)

	// Increment tool count
	count := h.tracker.Increment()

	// Check if we should remind (every ToolExecutionThreshold tools)
	if h.tracker.ShouldRemind() {
		msg := h.expandedWorkReminderMessage(count, inProgress[0], pending)
		msg, ok := h.wrapReminder(ctx, event, msg)
		if !ok {
			return hooks.Continue(), nil
		}
		return hooks.ContinueWithMessage(msg), nil
	}

	return hooks.Continue(), nil
}

func sessionTaskStatuses(tm *ii.TodoManager) (pending, inProgress []ii.TodoItem) {
	for _, task := range tm.ByOwner("") {
		switch task.Status {
		case ii.TodoStatusPending:
			pending = append(pending, task)
		case ii.TodoStatusInProgress:
			inProgress = append(inProgress, task)
		}
	}
	return pending, inProgress
}

func (h *TaskMaintenanceReminderHook) wrapReminder(ctx context.Context, event hooks.Event, body string) (string, bool) {
	seq, ok := hooks.DefaultMetaNudgeBudget().TryClaim(
		hooks.NudgeSessionID(ctx, event), hooks.MetaNudgeMaintenance)
	if !ok {
		return "", false
	}
	return hooks.WrapReminder(h.Name(), "nudge", seq, body), true
}

// isTaskTool checks if the tool is a task management tool (or a plan-mode
// tool, which this hook also treats as "not real work" for its tool-count
// threshold). Delegates to the shared classifiers in the base hooks
// package — see hooks/toolclass.go.
func isTaskTool(toolName string) bool {
	return hooks.IsTaskManagementTool(toolName) || hooks.IsPlanModeTool(toolName)
}

// inProgressReminderMessage reminds about tasks in progress.
func (h *TaskMaintenanceReminderHook) inProgressReminderMessage(inProgress, pending []ii.TodoItem) string {
	var sb strings.Builder

	sb.WriteString("[Task Maintenance Reminder]\n\n")

	if len(inProgress) == 1 {
		sb.WriteString(fmt.Sprintf("You have 1 task in progress: %s\n", inProgress[0].Content))
	} else {
		sb.WriteString(fmt.Sprintf("You have %d tasks in progress.\n", len(inProgress)))
		for _, t := range inProgress {
			sb.WriteString(fmt.Sprintf("  - %s\n", t.Content))
		}
	}

	if len(pending) > 0 {
		sb.WriteString(fmt.Sprintf("\n%d task(s) pending after current work.\n", len(pending)))
	}

	sb.WriteString("\nRemember to:\n")
	sb.WriteString("  - Mark tasks completed when done (TaskManage update)\n")
	sb.WriteString("  - Add new tasks if work expands\n")
	sb.WriteString("  - Check tasks periodically with a TaskManage list operation")

	return sb.String()
}

// startTaskReminderMessage suggests starting a pending task.
func (h *TaskMaintenanceReminderHook) startTaskReminderMessage(pending []ii.TodoItem) string {
	var sb strings.Builder

	sb.WriteString("[Task Reminder]\n\n")
	sb.WriteString(fmt.Sprintf("You have %d pending task(s) but none in progress.\n\n", len(pending)))

	sb.WriteString("Available tasks:\n")
	for i, t := range pending {
		if i >= 3 { // Only show first 3
			sb.WriteString(fmt.Sprintf("  ... and %d more\n", len(pending)-3))
			break
		}
		// Check if task has open blockers
		blockers := getOpenBlockers(t, pending)
		if len(blockers) > 0 {
			sb.WriteString(fmt.Sprintf("  - %s (blocked by: %s)\n", t.Content, strings.Join(blockers, ", ")))
		} else {
			sb.WriteString(fmt.Sprintf("  - %s [available]\n", t.Content))
		}
	}

	sb.WriteString("\nUse a TaskManage update operation to mark a task as 'in_progress' before working on it.")

	return sb.String()
}

// getOpenBlockers returns the list of blockers that are not yet completed.
func getOpenBlockers(task ii.TodoItem, allTasks []ii.TodoItem) []string {
	if len(task.DependsOn) == 0 {
		return nil
	}

	// Build status map
	statusByID := make(map[string]ii.TodoStatus)
	for _, t := range allTasks {
		statusByID[t.ID] = t.Status
	}

	var openBlockers []string
	for _, depID := range task.DependsOn {
		if status, ok := statusByID[depID]; ok && status != ii.TodoStatusCompleted {
			openBlockers = append(openBlockers, depID)
		}
	}
	return openBlockers
}

// expandedWorkReminderMessage reminds about potentially expanded work.
func (h *TaskMaintenanceReminderHook) expandedWorkReminderMessage(toolCount int, currentTask ii.TodoItem, pending []ii.TodoItem) string {
	var sb strings.Builder

	sb.WriteString("[Task Maintenance Reminder]\n\n")
	sb.WriteString(fmt.Sprintf("You've used %d tools while working on: %s\n\n", toolCount, currentTask.Content))

	sb.WriteString("Has the work expanded beyond the original task?\n\n")

	sb.WriteString("Consider:\n")
	sb.WriteString("  - Creating new tasks for additional work discovered\n")
	sb.WriteString("  - Updating task descriptions if scope changed\n")
	sb.WriteString("  - Marking this task complete and starting follow-ups\n")

	if len(pending) > 0 {
		sb.WriteString(fmt.Sprintf("\n%d task(s) already pending. Add more if needed.", len(pending)))
	} else {
		sb.WriteString("\nNo pending tasks - use a TaskManage create operation if new work emerged.")
	}

	return sb.String()
}

// GetTracker returns the internal tracker for testing purposes.
func (h *TaskMaintenanceReminderHook) GetTracker() *TaskExecutionTracker {
	return h.tracker
}
