package builtin

import (
	"context"
	"sync"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/hooks"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/tools/ii"
)

// MidSessionRecoveryHook surfaces a planning suggestion mid-session
// if the agent has created 5+ tasks but has no plan artifact/mode usage.
//
// This is a one-time gentle nudge that respects the agent's autonomy
// while giving them a chance to capture their mental model.
type MidSessionRecoveryHook struct {
	planSuggestionGiven bool
	mu                  sync.Mutex
}

// NewMidSessionRecoveryHook creates a new mid-session recovery hook
func NewMidSessionRecoveryHook() *MidSessionRecoveryHook {
	return &MidSessionRecoveryHook{}
}

// Name returns the hook name
func (h *MidSessionRecoveryHook) Name() string {
	return "mid-session-recovery"
}

// Priority runs late to avoid interfering with main execution
func (h *MidSessionRecoveryHook) Priority() int {
	return 20
}

// Filter listens for task creation events
func (h *MidSessionRecoveryHook) Filter(event hooks.Event) bool {
	return event.Type == hooks.EventToolAfterExecute
}

// OnEvent checks if we should surface the planning nudge
func (h *MidSessionRecoveryHook) OnEvent(ctx context.Context, event hooks.Event) (hooks.HookResult, error) {
	h.mu.Lock()
	if h.planSuggestionGiven {
		h.mu.Unlock()
		return hooks.Continue(), nil
	}
	h.mu.Unlock()

	// Only check on task creation events
	toolName, _ := event.Data["tool_name"].(string)
	if toolName == "" {
		if tn, ok := event.Data["name"].(string); ok {
			toolName = tn
		}
	}

	// Check if this was a successful task creation/update, including TaskManage batches.
	lower := normalizedTaskTool(toolName)
	isTaskTool := lower == "taskcreate" || lower == "taskupdate"
	if isTaskManageTool(toolName) {
		operations, _, ok := successfulTaskManageOperations(event)
		isTaskTool = ok
		if ok {
			isTaskTool = false
			for _, operation := range operations {
				if operation.Kind == ii.TaskOperationCreate || operation.Kind == ii.TaskOperationUpdate {
					isTaskTool = true
					break
				}
			}
		}
	}
	if !isTaskTool {
		return hooks.Continue(), nil
	}

	// Get task manager to count tasks
	tm := ii.GetTodoManager()
	if tm == nil {
		return hooks.Continue(), nil
	}

	allTasks := tm.Todos()
	if len(allTasks) < 5 {
		// Not enough tasks yet
		return hooks.Continue(), nil
	}

	// Check if agent has used plan mode (via global plan mode state)
	if PlanModeEverUsed() {
		// Agent has already planned
		return hooks.Continue(), nil
	}

	// 5+ tasks, no planning - suggest plan snapshot
	h.mu.Lock()
	h.planSuggestionGiven = true
	h.mu.Unlock()

	msg := `⚠️ [Mid-Session Check]

You've created 5+ tasks but haven't captured a plan artifact.

If you already have a clear mental model of how these fit together, no problem — carry on.
But if you're mid-execution and realizing the approach isn't working, now is a good time to:

  1. Create a quick snapshot of your current understanding
  2. Use enter_plan_mode() to sketch out how tasks relate to each other
  3. Update tasks with this context

This helps you recover if assumptions change mid-stream.

[This suggestion will only appear once per session.]`

	return hooks.ContinueWithMessage(msg), nil
}
