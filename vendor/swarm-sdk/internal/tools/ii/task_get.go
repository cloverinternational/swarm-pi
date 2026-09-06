// Package ii provides the TaskGet tool for retrieving individual task details.
package ii

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/tools"
)

// TaskGet tool constants
const (
	TaskGetName        = "TaskGet"
	TaskGetDisplayName = "Get task details"
)

const taskGetDescription = `Use this tool to retrieve a task by its ID from the task list.

## When to Use This Tool

- When you need the full description and context before starting work on a task
- To understand task dependencies (what it blocks, what blocks it)
- After being assigned a task, to get complete requirements

## Output

Returns full task details:
- **subject**: Task title
- **description**: Detailed requirements and context
- **status**: 'pending', 'in_progress', or 'completed'
- **blocks**: Tasks waiting on this one to complete
- **blockedBy**: Tasks that must complete before this one can start

## Tips

- After fetching a task, verify its blockedBy list is empty before beginning work.
- Use a TaskManage list operation to see all tasks in summary form.
`

// TaskGetTool implements task retrieval
type TaskGetTool struct {
	manager *TodoManager
}

// NewTaskGetTool creates a new TaskGetTool using the global todo manager
func NewTaskGetTool() *TaskGetTool {
	return &TaskGetTool{manager: GetTodoManager()}
}

// NewTaskGetToolWithManager creates a new TaskGetTool with a custom manager (for testing)
func NewTaskGetToolWithManager(manager *TodoManager) *TaskGetTool {
	return &TaskGetTool{manager: manager}
}

// Name returns the tool name
func (t *TaskGetTool) Name() string { return TaskGetName }

// DisplayName returns the human-readable display name
func (t *TaskGetTool) DisplayName() string { return TaskGetDisplayName }

// Description returns the tool description
func (t *TaskGetTool) Description() string { return taskGetDescription }

// Parameters returns the JSON schema for tool parameters
func (t *TaskGetTool) Parameters() any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"taskId": map[string]any{
				"type":        "string",
				"description": "The ID of the task to retrieve",
			},
		},
		"required": []string{"taskId"},
	}
}

// Execute adapts the legacy TaskGet contract to the canonical operation engine.
func (t *TaskGetTool) Execute(ctx context.Context, params map[string]any) (*tools.ToolResult, error) {
	return t.executeCanonical(ctx, params)
}

func (t *TaskGetTool) executeDirect(ctx context.Context, params map[string]any) (*tools.ToolResult, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}

	taskID, _ := params["taskId"].(string)
	if taskID == "" {
		return tools.NewToolResult("ERROR: 'taskId' is required"), nil
	}

	task := t.manager.ByID(taskID)
	if task == nil {
		return tools.NewToolResult(fmt.Sprintf("ERROR: task with ID %s not found", taskID)), nil
	}

	// Build response with full details
	result := map[string]any{
		"id":       task.ID,
		"subject":  task.Content,
		"status":   string(task.Status),
		"priority": string(task.Priority),
		"active":   task.Active,
	}

	if task.Description != "" {
		result["description"] = task.Description
	}
	if task.ActiveForm != "" {
		result["activeForm"] = task.ActiveForm
	}
	if len(task.Metadata) > 0 {
		result["metadata"] = task.Metadata
	}
	if task.ParentID != "" {
		result["parentTaskId"] = task.ParentID
	}
	if children := t.manager.ChildrenOf(task.ID); len(children) > 0 {
		childIDs := make([]string, len(children))
		for i, child := range children {
			childIDs[i] = child.ID
		}
		result["subtasks"] = childIDs
	}
	if len(task.Notes) > 0 {
		result["notes"] = task.Notes
	}
	if len(task.TypedNotes) > 0 {
		result["typedNotes"] = task.TypedNotes
	}
	if len(task.AuditEvents) > 0 {
		result["auditEvents"] = task.AuditEvents
	}

	// Include category if set
	if task.Category != "" {
		result["category"] = string(task.Category)
	}

	// Show dependency info
	if len(task.DependsOn) > 0 {
		// Filter to only show non-completed blockers
		var openBlockers []string
		for _, depID := range task.DependsOn {
			dep := t.manager.ByID(depID)
			if dep != nil && dep.Status != TodoStatusCompleted {
				openBlockers = append(openBlockers, depID)
			}
		}
		result["blockedBy"] = task.DependsOn
		if len(openBlockers) > 0 {
			result["openBlockers"] = openBlockers
		}
	}
	if len(task.Blocks) > 0 {
		result["blocks"] = task.Blocks
	}

	jsonBytes, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return tools.NewToolResult(fmt.Sprintf("ERROR: failed to format task: %v", err)), nil
	}

	var header strings.Builder
	header.WriteString(fmt.Sprintf("Task #%s: %s\n", task.ID, task.Content))
	header.WriteString(fmt.Sprintf("Status: %s | Priority: %s", task.Status, task.Priority))
	if task.Category != "" {
		header.WriteString(fmt.Sprintf(" | Category: %s", task.Category))
	}
	header.WriteString("\n")
	if len(task.DependsOn) > 0 {
		header.WriteString(fmt.Sprintf("Blocked by: %s\n", strings.Join(task.DependsOn, ", ")))
	}
	if len(task.Blocks) > 0 {
		header.WriteString(fmt.Sprintf("Blocks: %s\n", strings.Join(task.Blocks, ", ")))
	}
	header.WriteString("\n")
	header.Write(jsonBytes)

	return tools.NewToolResult(header.String()), nil
}

// Validate checks if the given parameters are valid
func (t *TaskGetTool) Validate(params map[string]any) error {
	return validateLegacyTaskOperation(TaskOperationGet, params)
}

// IsIdempotent returns true as reading tasks doesn't modify state
func (t *TaskGetTool) IsIdempotent() bool { return true }

// IsReadOnly returns true as this tool only reads data
func (t *TaskGetTool) IsReadOnly() bool { return true }

// RequiresPermission returns no special permissions
func (t *TaskGetTool) RequiresPermission() []tools.Permission { return []tools.Permission{} }

// SupportedContentTypes returns text content type
func (t *TaskGetTool) SupportedContentTypes() []tools.ContentType {
	return []tools.ContentType{tools.ContentTypeText}
}

// OptimizationHints returns nil
func (t *TaskGetTool) OptimizationHints() *tools.OptimizationHints { return nil }

// ShouldConfirmExecute returns nil as read operations don't need confirmation
func (t *TaskGetTool) ShouldConfirmExecute(params map[string]any) *ConfirmationDetails {
	return nil
}

// Metadata returns tool metadata
func (t *TaskGetTool) Metadata() map[string]any {
	return map[string]any{"category": "productivity", "readOnly": true}
}
