package builtin

import (
	"context"
	"sync"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/hooks"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/tools/ii"
)

// PostActingHook prompts agents to create documenting and verifying tasks
// after completing a series of acting tasks.
//
// This hook enforces the workflow: Research → Plan → Act → Verify → Document
//
// When an acting task is marked as completed, this hook:
//  1. Tracks the completed acting task count
//  2. After threshold acting tasks are completed, emits a prompt
//  3. The prompt reminds the agent to create verifying and documenting tasks

const (
	// DefaultActingThreshold is the number of acting tasks to complete before prompting
	DefaultActingThreshold = 2
	// PostActingPriority runs after task enforcement (95)
	PostActingPriority = 90
)

// ActingTaskCounter tracks completed acting tasks per session
type ActingTaskCounter struct {
	count int
	mu    sync.Mutex
}

func (c *ActingTaskCounter) Increment() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.count++
	return c.count
}

func (c *ActingTaskCounter) Count() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.count
}

func (c *ActingTaskCounter) Reset() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.count = 0
}

// PostActingHook monitors acting task completion and prompts for follow-up tasks
type PostActingHook struct {
	counter   *ActingTaskCounter
	threshold int
}

// NewPostActingHook creates a new post-acting hook with default settings
func NewPostActingHook() *PostActingHook {
	return &PostActingHook{
		counter:   &ActingTaskCounter{},
		threshold: DefaultActingThreshold,
	}
}

// Name returns the hook name
func (h *PostActingHook) Name() string {
	return "post-acting-hook"
}

// Priority returns the hook priority
func (h *PostActingHook) Priority() int {
	return PostActingPriority
}

// Filter returns true for tool after-execute events (specifically TaskUpdate)
func (h *PostActingHook) Filter(event hooks.Event) bool {
	return event.Type == hooks.EventToolAfterExecute
}

// OnEvent checks if an acting task was completed and prompts for follow-up
func (h *PostActingHook) OnEvent(ctx context.Context, event hooks.Event) (hooks.HookResult, error) {
	// Extract tool name from event data
	toolName, _ := event.Data["tool_name"].(string)
	if toolName == "" {
		return hooks.Continue(), nil
	}

	// Only process legacy TaskUpdate events or TaskManage update sub-operations.
	if !isTaskUpdateTool(toolName) && !isTaskManageTool(toolName) {
		return hooks.Continue(), nil
	}

	// Get the TodoManager
	tm := ii.GetTodoManager()
	if tm == nil {
		return hooks.Continue(), nil
	}

	// Get params - this is where status and category are passed
	params, ok := event.Data["params"].(map[string]any)
	if !ok {
		return hooks.Continue(), nil
	}

	completedTaskIDs := make([]string, 0, 1)
	if isTaskManageTool(toolName) {
		operations, resolvedIDs, ok := successfulTaskManageOperations(event)
		if !ok {
			return hooks.Continue(), nil
		}
		for _, operation := range operations {
			if operation.Kind == ii.TaskOperationUpdate && operation.Status == "completed" {
				taskID := taskTargetID(operation.TaskID)
				if resolvedIDs[operation.Key] != "" {
					taskID = resolvedIDs[operation.Key]
				}
				completedTaskIDs = append(completedTaskIDs, taskID)
			}
		}
	} else if status, _ := params["status"].(string); status == "completed" {
		taskID, _ := params["taskId"].(string)
		completedTaskIDs = append(completedTaskIDs, taskID)
	}
	if len(completedTaskIDs) == 0 {
		return hooks.Continue(), nil
	}

	var guidance hooks.HookResult
	for _, taskID := range completedTaskIDs {
		result := h.afterCompletedTask(tm, taskID)
		if result.Message != "" {
			guidance = result
		}
	}
	if guidance.Message != "" {
		return guidance, nil
	}
	return hooks.Continue(), nil
}

func (h *PostActingHook) afterCompletedTask(tm *ii.TodoManager, taskID string) hooks.HookResult {
	// Look up the actual task to get its real category. TaskUpdate params
	// don't carry category (it's set at creation time), so reading it from
	// params always returns "".  Checking the TodoManager gives us the truth.
	var taskCategory string
	for _, t := range tm.Todos() {
		if t.ID == taskID {
			taskCategory = string(t.Category)
			break
		}
	}

	// Only prompt for acting tasks (or tasks with no category set at all,
	// which we treat conservatively — don't nudge on research/debugging/etc.)
	if taskCategory != string(ii.TaskCategoryActing) && taskCategory != "" {
		return hooks.Continue()
	}

	// Task is completed and was an acting task — increment counter
	count := h.counter.Increment()

	// Check if we have documenting or verifying tasks already
	docTasks := tm.ByCategory(ii.TaskCategoryDocumenting)
	verifyingTasks := tm.ByCategory(ii.TaskCategoryVerifying)

	// If threshold reached and no follow-up tasks, prompt
	if count >= h.threshold && len(docTasks) == 0 && len(verifyingTasks) == 0 {
		return hooks.ContinueWithMessage(h.promptMessage(taskID))
	}

	return hooks.Continue()
}

// isTaskUpdateTool checks if the tool is TaskUpdate.
func isTaskUpdateTool(toolName string) bool {
	lowerTool := normalizedTaskTool(toolName)
	return lowerTool == "taskupdate"
}

// promptMessage returns the prompt for creating follow-up tasks
func (h *PostActingHook) promptMessage(completedTaskID string) string {
	return `[POST-ACTING WORKFLOW REMINDER]

You have completed ` + string(rune('0'+h.threshold)) + `+ acting (implementation) tasks.

═══════════════════════════════════════════════════════════════════════════════
              THE WORK IS NOT DONE UNTIL IT IS VERIFIED AND DOCUMENTED
═══════════════════════════════════════════════════════════════════════════════

The implementation you just completed needs follow-up work:

┌─────────────────────────────────────────────────────────────────────────────┐
│  REQUIRED NEXT STEPS:                                                       │
├─────────────────────────────────────────────────────────────────────────────┤
│  1. Create VERIFYING tasks for the code you wrote:                          │
│     - Run the test suite                                                    │
│     - Verify edge cases                                                     │
│     - Check integration points                                              │
│                                                                             │
│  2. Create DOCUMENTING tasks for the changes:                              │
│     - Update relevant README sections                                       │
│     - Add/update code comments                                              │
│     - Update changelog if applicable                                        │
│     - Document any new APIs or configuration                                │
└─────────────────────────────────────────────────────────────────────────────┘

═══════════════════════════════════════════════════════════════════════════════
                              EXAMPLE TASKS:
═══════════════════════════════════════════════════════════════════════════════

  TaskManage create operation (
    subject: "Run test suite and verify all tests pass",
    category: "verifying",
    description: "Execute the full test suite to validate the implementation"
  )

  TaskManage create operation (
    subject: "Document the new API endpoints",
    category: "documenting",
    description: "Add documentation for the endpoints introduced in this change"
  )

═══════════════════════════════════════════════════════════════════════════════
                                WHY THIS MATTERS:
═══════════════════════════════════════════════════════════════════════════════

  Creation ≠ Verification. The agent made something — that's 50% of the job.
  The other 50% is proving it works and documenting it for future developers.

  Unverified code is broken code.
  Undocumented code is unmaintainable code.

═══════════════════════════════════════════════════════════════════════════════

If you have already created verifying/documenting tasks, this reminder can be ignored.
Otherwise, CREATE THEM NOW before proceeding with more acting tasks.`
}

// SetThreshold allows adjusting the threshold at runtime
func (h *PostActingHook) SetThreshold(threshold int) {
	h.threshold = threshold
}

// GetCounter returns the internal counter for testing
func (h *PostActingHook) GetCounter() *ActingTaskCounter {
	return h.counter
}
