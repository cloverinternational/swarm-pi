// Package ii provides the TaskList tool for listing all tasks with dependency information.
package ii

import (
	"context"
	"fmt"
	"strings"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/tools"
)

// TaskList tool constants
const (
	TaskListName        = "TaskList"
	TaskListDisplayName = "List all tasks"
)

const taskListDescription = `Use this tool to list all tasks in the task list.

## When to Use This Tool

- To see what tasks are available to work on (status: 'pending', no owner, not blocked)
- To check overall progress on the project
- To find tasks that are blocked and need dependencies resolved
- After completing a task, to check for newly unblocked work or claim the next available task
- **Prefer working on tasks in ID order** (lowest ID first) when multiple tasks are available, as earlier tasks often set up context for later ones

## Output

Returns a summary of each task:
- **id**: Task identifier (use with TaskManage get/update operations)
- **subject**: Brief description of the task
- **status**: 'pending', 'in_progress', or 'completed'
- **owner**: Agent ID if assigned, empty if available
- **blockedBy**: List of open task IDs that must be resolved first (tasks with blockedBy cannot be claimed until dependencies resolve)

Use a TaskManage get operation with a specific task ID to view full details including description and comments.
`

// TaskListTool implements task listing
type TaskListTool struct {
	manager *TodoManager
}

// NewTaskListTool creates a new TaskListTool using the global todo manager
func NewTaskListTool() *TaskListTool {
	return &TaskListTool{manager: GetTodoManager()}
}

// NewTaskListToolWithManager creates a new TaskListTool with a custom manager (for testing)
func NewTaskListToolWithManager(manager *TodoManager) *TaskListTool {
	return &TaskListTool{manager: manager}
}

// Name returns the tool name
func (t *TaskListTool) Name() string { return TaskListName }

// DisplayName returns the human-readable display name
func (t *TaskListTool) DisplayName() string { return TaskListDisplayName }

// Description returns the tool description
func (t *TaskListTool) Description() string { return taskListDescription }

// Parameters returns the JSON schema for tool parameters
func (t *TaskListTool) Parameters() any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"category": map[string]any{
				"type":        "string",
				"enum":        []string{"researching", "planning", "acting", "verifying", "debugging", "documenting"},
				"description": "Filter tasks by category type",
			},
		},
		"required": []string{},
	}
}

// Execute adapts the legacy TaskList contract to the canonical operation engine.
func (t *TaskListTool) Execute(ctx context.Context, params map[string]any) (*tools.ToolResult, error) {
	return t.executeCanonical(ctx, params)
}

func (t *TaskListTool) executeDirect(ctx context.Context, params map[string]any) (*tools.ToolResult, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}

	// Check for category filter
	var todos []TodoItem
	if categoryFilter, ok := params["category"].(string); ok && categoryFilter != "" {
		todos = t.manager.ByCategory(TaskCategory(categoryFilter))
		if len(todos) == 0 {
			return tools.NewToolResult(fmt.Sprintf("No tasks found with category '%s'.", categoryFilter)), nil
		}
	} else {
		todos = t.manager.Todos()
		if len(todos) == 0 {
			return tools.NewToolResult("No tasks found. The task list is empty."), nil
		}
	}

	// Build a status map for showing open blockers
	statusByID := make(map[string]TodoStatus, len(todos))
	for _, todo := range todos {
		statusByID[todo.ID] = todo.Status
	}

	var sb strings.Builder

	// Summary header
	summary := t.manager.Summary()
	sb.WriteString(fmt.Sprintf("Tasks: %d total (%d pending, %d in progress, %d completed)\n\n",
		summary["total"], summary["pending"], summary["in_progress"], summary["completed"]))

	// List each task
	for _, todo := range todos {
		// Status indicator
		var statusIcon string
		switch todo.Status {
		case TodoStatusPending:
			statusIcon = "[pending]"
		case TodoStatusInProgress:
			statusIcon = "[in_progress]"
		case TodoStatusCompleted:
			statusIcon = "[completed]"
		}

		// Category indicator
		categoryStr := ""
		if todo.Category != "" {
			categoryStr = fmt.Sprintf(" [%s]", todo.Category)
		}

		sb.WriteString(fmt.Sprintf("#%s. %s%s %s\n", todo.ID, statusIcon, categoryStr, todo.Content))

		// Show open blockers (dependencies not yet completed)
		if len(todo.DependsOn) > 0 {
			var openBlockers []string
			for _, depID := range todo.DependsOn {
				if status, ok := statusByID[depID]; ok && status != TodoStatusCompleted {
					openBlockers = append(openBlockers, depID)
				}
			}
			if len(openBlockers) > 0 {
				sb.WriteString(fmt.Sprintf("   blocked by: [%s]\n", strings.Join(openBlockers, ", ")))
			}
		}
	}

	return tools.NewToolResult(sb.String()), nil
}

// Validate checks if the given parameters are valid (no params needed)
func (t *TaskListTool) Validate(params map[string]any) error {
	return validateLegacyTaskOperation(TaskOperationList, params)
}

// IsIdempotent returns true as listing tasks doesn't modify state
func (t *TaskListTool) IsIdempotent() bool { return true }

// IsReadOnly returns true as this tool only reads data
func (t *TaskListTool) IsReadOnly() bool { return true }

// RequiresPermission returns no special permissions
func (t *TaskListTool) RequiresPermission() []tools.Permission { return []tools.Permission{} }

// SupportedContentTypes returns text content type
func (t *TaskListTool) SupportedContentTypes() []tools.ContentType {
	return []tools.ContentType{tools.ContentTypeText}
}

// OptimizationHints returns nil
func (t *TaskListTool) OptimizationHints() *tools.OptimizationHints { return nil }

// ShouldConfirmExecute returns nil as read operations don't need confirmation
func (t *TaskListTool) ShouldConfirmExecute(params map[string]any) *ConfirmationDetails {
	return nil
}

// Metadata returns tool metadata
func (t *TaskListTool) Metadata() map[string]any {
	return map[string]any{"category": "productivity", "readOnly": true}
}
