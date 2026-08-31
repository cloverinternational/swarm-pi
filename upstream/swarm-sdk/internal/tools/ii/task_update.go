// Package ii provides the TaskUpdate tool for modifying individual tasks.
package ii

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/tools"
)

// TaskUpdate tool constants
const (
	TaskUpdateName        = "TaskUpdate"
	TaskUpdateDisplayName = "Update a task"
)

const taskUpdateDescription = `Use this tool to update a task in the task list.

## When to Use This Tool

**Mark tasks as resolved:**
- When you have completed the work described in a task
- When a task is no longer needed or has been superseded
- IMPORTANT: Always mark your assigned tasks as resolved when you finish them
- After resolving, use a TaskManage list operation to find your next task

- ONLY mark a task as completed when you have FULLY accomplished it
- If you encounter errors, blockers, or cannot finish, keep the task as in_progress
- When blocked, create a new task describing what needs to be resolved
- Never mark a task as completed if:
  - Tests are failing
  - Implementation is partial
  - You encountered unresolved errors
  - You couldn't find necessary files or dependencies

**Delete tasks:**
- When a task is no longer relevant or was created in error
- Setting status to ` + "`deleted`" + ` permanently removes the task

**Update task details:**
- When requirements change or become clearer
- When establishing dependencies between tasks

## Fields You Can Update

- **status**: The task status (see Status Workflow below)
- **subject**: Change the task title (imperative form, e.g., "Run tests")
- **description**: Change the task description
- **activeForm**: Present continuous form shown in spinner when in_progress (e.g., "Running tests")
- **owner**: Change the task owner (agent name)
- **metadata**: Merge metadata keys into the task (set a key to null to delete it)
- **addBlocks**: Mark tasks that cannot start until this one completes
- **addBlockedBy**: Mark tasks that must complete before this one can start

## Status Workflow

Status progresses: ` + "`pending`" + ` -> ` + "`in_progress`" + ` -> ` + "`completed`" + `

Use ` + "`deleted`" + ` to permanently remove a task.

## Examples

Mark task as in progress when starting work:
` + "```json" + `
{"taskId": "1", "status": "in_progress"}
` + "```" + `

Mark task as completed after finishing work:
` + "```json" + `
{"taskId": "1", "status": "completed"}
` + "```" + `

Delete a task:
` + "```json" + `
{"taskId": "1", "status": "deleted"}
` + "```" + `

Set up task dependencies:
` + "```json" + `
{"taskId": "2", "addBlockedBy": ["1"]}
` + "```" + `
`

// TaskUpdateTool implements task update with dependency support
type TaskUpdateTool struct {
	manager *TodoManager
}

// NewTaskUpdateTool creates a new TaskUpdateTool using the global todo manager
func NewTaskUpdateTool() *TaskUpdateTool {
	return &TaskUpdateTool{manager: GetTodoManager()}
}

// NewTaskUpdateToolWithManager creates a new TaskUpdateTool with a custom manager (for testing)
func NewTaskUpdateToolWithManager(manager *TodoManager) *TaskUpdateTool {
	return &TaskUpdateTool{manager: manager}
}

// Name returns the tool name
func (t *TaskUpdateTool) Name() string { return TaskUpdateName }

// DisplayName returns the human-readable display name
func (t *TaskUpdateTool) DisplayName() string { return TaskUpdateDisplayName }

// Description returns the tool description
func (t *TaskUpdateTool) Description() string { return taskUpdateDescription }

// Parameters returns the JSON schema for tool parameters
func (t *TaskUpdateTool) Parameters() any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"taskId": map[string]any{
				"type":        "string",
				"description": "The ID of the task to update",
			},
			"status": map[string]any{
				"type":        "string",
				"description": "New status for the task. Use 'deleted' to permanently remove.",
				"enum":        []string{"pending", "in_progress", "completed", "deleted"},
			},
			"category": map[string]any{
				"type":        "string",
				"enum":        []string{"researching", "planning", "acting", "verifying", "debugging", "documenting"},
				"description": "Type of work: researching (explore/read), planning (design/architect), acting (implement/code), verifying (test/validate), debugging (fix/trace), documenting (docs/comments)",
			},
			"subject": map[string]any{
				"type":        "string",
				"description": "New subject for the task",
			},
			"description": map[string]any{
				"type":        "string",
				"description": "New description for the task",
			},
			"activeForm": map[string]any{
				"type":        "string",
				"description": "Present continuous form shown in spinner when in_progress (e.g., \"Running tests\")",
			},
			"active": map[string]any{
				"type":        "boolean",
				"description": "Focus this task. Only one task is active at a time; focusing another transfers focus. The task must be in_progress.",
			},
			"parentTaskId": map[string]any{
				"type":        "string",
				"description": "Re-parent this task under the given parent (hierarchy edge). Empty string detaches to top-level.",
			},
			"metadata": map[string]any{
				"type":        "object",
				"description": "Merge metadata keys into the task (set a key to null to delete it).",
			},
			"addBlocks": map[string]any{
				"type":        "array",
				"items":       map[string]any{"type": "string"},
				"description": "Task IDs that this task blocks (those tasks depend on this one)",
			},
			"addBlockedBy": map[string]any{
				"type":        "array",
				"items":       map[string]any{"type": "string"},
				"description": "Task IDs that block this task (this task depends on those)",
			},
			"addNote": map[string]any{
				"type":        "string",
				"description": "Append an observation or progress note to the task. Add 'noteType' to record a typed note (decision, blocker, learning, milestone, question, observation, other).",
			},
			"noteType": map[string]any{
				"type":        "string",
				"enum":        []string{"decision", "blocker", "learning", "milestone", "question", "observation", "other"},
				"description": "Optional type for addNote; records a structured note in addition to the plain-string note.",
			},
		},
		"required": []string{"taskId"},
	}
}

// Execute adapts the legacy TaskUpdate contract to the canonical operation engine.
func (t *TaskUpdateTool) Execute(ctx context.Context, params map[string]any) (*tools.ToolResult, error) {
	return t.executeCanonical(ctx, params)
}

func (t *TaskUpdateTool) executeMutation(ctx context.Context, params map[string]any) (*tools.ToolResult, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}

	taskID, _ := params["taskId"].(string)
	if taskID == "" {
		return tools.NewToolResult("ERROR: 'taskId' is required"), nil
	}

	// Handle deletion
	if status, ok := params["status"].(string); ok && status == "deleted" {
		if err := t.manager.DeleteTodo(taskID); err != nil {
			return tools.NewToolResult(fmt.Sprintf("ERROR: %v", err)), nil
		}
		return tools.NewToolResult(fmt.Sprintf("Deleted task #%s", taskID)), nil
	}

	// Handle addBlocks: for each task in addBlocks, add this task's ID to their depends_on
	if blocksRaw, ok := params["addBlocks"].([]any); ok && len(blocksRaw) > 0 {
		// Phase 1: Validate all target tasks exist before making changes
		var blockIDs []string
		for _, blockRaw := range blocksRaw {
			blockID, ok := blockRaw.(string)
			if !ok {
				continue
			}
			if t.manager.ByID(blockID) == nil {
				return tools.NewToolResult(fmt.Sprintf("ERROR: task %s not found", blockID)), nil
			}
			blockIDs = append(blockIDs, blockID)
		}

		// Phase 2: Apply all changes
		for _, blockID := range blockIDs {
			err := t.manager.UpdateTodo(blockID, func(item *TodoItem) error {
				if slices.Contains(item.DependsOn, taskID) {
					return nil // already present
				}
				item.DependsOn = append(item.DependsOn, taskID)
				return nil
			})
			if err != nil {
				return tools.NewToolResult(fmt.Sprintf("ERROR: failed to add block on task %s: %v", blockID, err)), nil
			}
		}
	}

	// Handle addBlockedBy: add those IDs to this task's depends_on
	if blockedByRaw, ok := params["addBlockedBy"].([]any); ok && len(blockedByRaw) > 0 {
		// Phase 1: Validate all dependency tasks exist
		var newDeps []string
		for _, depRaw := range blockedByRaw {
			depStr, ok := depRaw.(string)
			if !ok {
				continue
			}
			if t.manager.ByID(depStr) == nil {
				return tools.NewToolResult(fmt.Sprintf("ERROR: dependency task %s not found", depStr)), nil
			}
			newDeps = append(newDeps, depStr)
		}

		// Phase 2: Apply all dependencies atomically
		if len(newDeps) > 0 {
			err := t.manager.UpdateTodo(taskID, func(item *TodoItem) error {
				for _, newDep := range newDeps {
					found := slices.Contains(item.DependsOn, newDep)
					if !found {
						item.DependsOn = append(item.DependsOn, newDep)
					}
				}
				return nil
			})
			if err != nil {
				return tools.NewToolResult(fmt.Sprintf("ERROR: failed to add dependencies: %v", err)), nil
			}
		}
	}

	// Handle addNote (plain string, optionally also a typed note).
	if note, ok := params["addNote"].(string); ok && note != "" {
		noteType, _ := params["noteType"].(string)
		err := t.manager.UpdateTodo(taskID, func(item *TodoItem) error {
			item.Notes = append(item.Notes, note)
			if noteType != "" {
				item.TypedNotes = append(item.TypedNotes, TodoNote{
					Type:      noteType,
					Content:   note,
					CreatedAt: time.Now().UTC(),
				})
			}
			return nil
		})
		if err != nil {
			return tools.NewToolResult(fmt.Sprintf("ERROR adding note: %v", err)), nil
		}
	}

	// Handle re-parenting explicitly (empty string detaches to top-level).
	if rawParent, ok := params["parentTaskId"]; ok {
		parentID, _ := rawParent.(string)
		if parentID != "" && parentID != taskID && t.manager.ByID(parentID) == nil {
			return tools.NewToolResult(fmt.Sprintf("ERROR: parent task %s not found", parentID)), nil
		}
		err := t.manager.UpdateTodo(taskID, func(item *TodoItem) error {
			item.ParentID = parentID
			return nil
		})
		if err != nil {
			return tools.NewToolResult(fmt.Sprintf("ERROR: failed to set parent: %v", err)), nil
		}
	}

	// Handle metadata merge (null value deletes a key).
	if rawMeta, ok := params["metadata"].(map[string]any); ok {
		err := t.manager.UpdateTodo(taskID, func(item *TodoItem) error {
			if item.Metadata == nil && len(rawMeta) > 0 {
				item.Metadata = make(map[string]any, len(rawMeta))
			}
			for key, value := range rawMeta {
				if value == nil {
					delete(item.Metadata, key)
					continue
				}
				item.Metadata[key] = value
			}
			return nil
		})
		if err != nil {
			return tools.NewToolResult(fmt.Sprintf("ERROR: failed to merge metadata: %v", err)), nil
		}
	}

	// Completion guard: refuse to complete a task with unfinished children.
	// This performs no partial mutation — it returns before the field update.
	if status, ok := params["status"].(string); ok && status == "completed" {
		for _, child := range t.manager.ChildrenOf(taskID) {
			if child.Status != TodoStatusCompleted {
				return tools.NewToolResult(fmt.Sprintf(
					"ERROR: cannot complete task #%s — subtask #%s (%s) is still %s. "+
						"Complete or remove open subtasks first.",
					taskID, child.ID, child.Content, child.Status)), nil
			}
		}
	}

	// Handle other field updates
	hasFieldUpdates := false
	for _, key := range []string{"status", "subject", "description", "activeForm", "category", "active"} {
		if _, ok := params[key]; ok {
			hasFieldUpdates = true
			break
		}
	}

	if hasFieldUpdates {
		err := t.manager.UpdateTodo(taskID, func(item *TodoItem) error {
			if status, ok := params["status"].(string); ok {
				item.Status = TodoStatus(status)
			}
			if subject, ok := params["subject"].(string); ok && subject != "" {
				item.Content = subject
			}
			if description, ok := params["description"].(string); ok {
				item.Description = description
			}
			if activeForm, ok := params["activeForm"].(string); ok {
				item.ActiveForm = activeForm
			}
			if category, ok := params["category"].(string); ok && category != "" {
				item.Category = TaskCategory(category)
			}
			if active, ok := params["active"].(bool); ok && active {
				// Manager transfers focus atomically for in_progress tasks.
				item.Active = true
			}
			return nil
		})
		if err != nil {
			return tools.NewToolResult(fmt.Sprintf("ERROR: %v", err)), nil
		}
	}

	// Explicit focus request for an already-in-progress task.
	if active, ok := params["active"].(bool); ok && active {
		if err := t.manager.FocusTodo(taskID); err != nil {
			return tools.NewToolResult(fmt.Sprintf("ERROR: %v", err)), nil
		}
	}

	// Build response
	task := t.manager.ByID(taskID)
	if task == nil {
		return tools.NewToolResult(fmt.Sprintf("Updated task #%s", taskID)), nil
	}

	var parts []string
	parts = append(parts, fmt.Sprintf("Updated task #%s", taskID))
	if task.Status != "" {
		parts = append(parts, fmt.Sprintf("status: %s", task.Status))
	}
	if task.Category != "" {
		parts = append(parts, fmt.Sprintf("category: %s", task.Category))
	}
	if len(task.DependsOn) > 0 {
		parts = append(parts, fmt.Sprintf("depends_on: [%s]", strings.Join(task.DependsOn, ", ")))
	}
	if len(task.Blocks) > 0 {
		parts = append(parts, fmt.Sprintf("blocks: [%s]", strings.Join(task.Blocks, ", ")))
	}

	var sb strings.Builder
	sb.WriteString(strings.Join(parts, " | "))

	// Append a snapshot of all currently in-progress tasks so the agent
	// always has visibility into active work after every update.
	inProgress := t.manager.ByStatus(TodoStatusInProgress)
	if len(inProgress) > 0 {
		sb.WriteString("\n\nIn progress:")
		for _, ip := range inProgress {
			line := fmt.Sprintf("\n  #%s. %s", ip.ID, ip.Content)
			if ip.Category != "" {
				line += fmt.Sprintf(" [%s]", ip.Category)
			}
			if ip.OwnerID != "" {
				line += fmt.Sprintf(" (owner: %s)", ip.OwnerID)
			}
			sb.WriteString(line)
		}
	}

	return tools.NewToolResult(sb.String()), nil
}

// Validate checks if the given parameters are valid
func (t *TaskUpdateTool) Validate(params map[string]any) error {
	return validateLegacyTaskOperation(TaskOperationUpdate, params)
}

// IsIdempotent returns false as updating tasks modifies state
func (t *TaskUpdateTool) IsIdempotent() bool { return false }

// IsReadOnly returns false as this tool modifies data
func (t *TaskUpdateTool) IsReadOnly() bool { return false }

// RequiresPermission returns no special permissions
func (t *TaskUpdateTool) RequiresPermission() []tools.Permission { return []tools.Permission{} }

// SupportedContentTypes returns text content type
func (t *TaskUpdateTool) SupportedContentTypes() []tools.ContentType {
	return []tools.ContentType{tools.ContentTypeText}
}

// OptimizationHints returns nil
func (t *TaskUpdateTool) OptimizationHints() *tools.OptimizationHints { return nil }

// ShouldConfirmExecute returns nil as task operations don't need confirmation
func (t *TaskUpdateTool) ShouldConfirmExecute(params map[string]any) *ConfirmationDetails {
	return nil
}

// Metadata returns tool metadata
func (t *TaskUpdateTool) Metadata() map[string]any {
	return map[string]any{"category": "productivity", "readOnly": false}
}
