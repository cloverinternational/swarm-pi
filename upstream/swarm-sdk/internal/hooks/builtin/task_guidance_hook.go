package builtin

import (
	"context"
	"fmt"
	"strings"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/hooks"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/tools/ii"
)

// TaskGuidanceHook is a post-tool hook that injects contextual guidance after
// task lifecycle operations (TaskCreate, TaskUpdate). This mirrors the
// TaskGuidanceHook pattern from the reference architecture:
//
//   - After TaskCreate: reminds the agent to activate the task via TaskUpdate
//   - After TaskUpdate → in_progress: confirms the task is active
//   - After TaskUpdate → completed: guides the agent to pick up the next task
//
// This prevents wasted tokens by immediately guiding agents after task ops
// rather than letting them discover constraints through trial and error.
type TaskGuidanceHook struct {
	priority int
}

const (
	// TaskGuidancePriority runs after post-acting hook (90) but before logging
	TaskGuidancePriority = 85
)

// NewTaskGuidanceHook creates a new task guidance hook
func NewTaskGuidanceHook() *TaskGuidanceHook {
	return &TaskGuidanceHook{
		priority: TaskGuidancePriority,
	}
}

// Name returns the hook name
func (h *TaskGuidanceHook) Name() string {
	return "task-guidance-hook"
}

// Priority returns the hook priority
func (h *TaskGuidanceHook) Priority() int {
	return h.priority
}

// Filter returns true for tool after-execute events
func (h *TaskGuidanceHook) Filter(event hooks.Event) bool {
	return event.Type == hooks.EventToolAfterExecute
}

// OnEvent checks if a task operation completed and injects guidance
func (h *TaskGuidanceHook) OnEvent(ctx context.Context, event hooks.Event) (hooks.HookResult, error) {
	toolName, _ := event.Data["tool_name"].(string)
	if toolName == "" {
		return hooks.Continue(), nil
	}

	switch {
	case isTaskCreateTool(toolName):
		return h.afterTaskCreate(event)
	case isTaskUpdateTool(toolName):
		return h.afterTaskUpdate(event)
	case isTaskManageTool(toolName):
		operations, resolvedIDs, ok := successfulTaskManageOperations(event)
		if !ok {
			return hooks.Continue(), nil
		}
		var selected *ii.TaskOperation
		for i := range operations {
			if operations[i].Kind == ii.TaskOperationCreate || operations[i].Kind == ii.TaskOperationUpdate {
				selected = &operations[i]
			}
		}
		if selected == nil {
			return hooks.Continue(), nil
		}
		if selected.Kind == ii.TaskOperationCreate {
			return h.afterTaskCreate(event)
		}
		taskID := taskTargetID(selected.TaskID)
		if resolvedIDs[selected.Key] != "" {
			taskID = resolvedIDs[selected.Key]
		}
		routed := event
		routed.Data = make(map[string]any, len(event.Data))
		for key, value := range event.Data {
			routed.Data[key] = value
		}
		routed.Data["params"] = map[string]any{"taskId": taskID, "status": selected.Status}
		return h.afterTaskUpdate(routed)
	default:
		return hooks.Continue(), nil
	}
}

// afterTaskCreate injects guidance after a task is created
func (h *TaskGuidanceHook) afterTaskCreate(event hooks.Event) (hooks.HookResult, error) {
	// Check if the result indicates success
	result, _ := event.Data["tool_output"].(string)
	if result == "" || strings.HasPrefix(result, "ERROR") {
		return hooks.Continue(), nil
	}

	// Check if there's already an in_progress task
	tm := ii.GetTodoManager()
	if tm == nil {
		return hooks.Continue(), nil
	}

	inProgress := tm.ByStatus(ii.TodoStatusInProgress)
	if len(inProgress) > 0 {
		// Already have an active task, no guidance needed
		return hooks.Continue(), nil
	}

	return hooks.ContinueWithMessage(
		"[TASK GUIDANCE] Task created successfully. " +
			"Use TaskManage with an update operation with status: \"in_progress\" to activate this task before proceeding with other tools.",
	), nil
}

// afterTaskUpdate injects guidance based on the status transition
func (h *TaskGuidanceHook) afterTaskUpdate(event hooks.Event) (hooks.HookResult, error) {
	params, ok := event.Data["params"].(map[string]any)
	if !ok {
		return hooks.Continue(), nil
	}

	status, _ := params["status"].(string)
	taskID, _ := params["taskId"].(string)

	switch status {
	case "in_progress":
		return hooks.ContinueWithMessage(fmt.Sprintf(
			"[TASK GUIDANCE] Task #%s is now ACTIVE. You can proceed with any tools.",
			taskID,
		)), nil

	case "completed":
		return h.afterTaskCompleted(taskID)

	default:
		return hooks.Continue(), nil
	}
}

// afterTaskCompleted guides the agent to pick up the next task
func (h *TaskGuidanceHook) afterTaskCompleted(completedID string) (hooks.HookResult, error) {
	tm := ii.GetTodoManager()
	if tm == nil {
		return hooks.Continue(), nil
	}

	// Check for remaining in_progress tasks
	inProgress := tm.ByStatus(ii.TodoStatusInProgress)
	if len(inProgress) > 0 {
		return hooks.ContinueWithMessage(fmt.Sprintf(
			"[TASK GUIDANCE] Task #%s completed. You still have %d active task(s). Continue with task #%s: %s",
			completedID, len(inProgress), inProgress[0].ID, inProgress[0].Content,
		)), nil
	}

	// Check for pending tasks
	pending := tm.ByStatus(ii.TodoStatusPending)
	if len(pending) > 0 {
		return hooks.ContinueWithMessage(fmt.Sprintf(
			"[TASK GUIDANCE] Task #%s completed. %d pending task(s) remaining. "+
				"Use TaskManage with an update operation to activate the next task, or create a new one.",
			completedID, len(pending),
		)), nil
	}

	return hooks.ContinueWithMessage(fmt.Sprintf(
		"[TASK GUIDANCE] Task #%s completed. All tasks done! Create a new task for your next objective.",
		completedID,
	)), nil
}

// isTaskCreateTool checks if the tool is TaskCreate
func isTaskCreateTool(toolName string) bool {
	return normalizedTaskTool(toolName) == "taskcreate"
}
