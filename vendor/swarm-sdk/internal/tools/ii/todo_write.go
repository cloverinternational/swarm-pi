// Package ii provides the TodoWrite tool for creating and managing structured task lists.
// This tool enables agents to track progress, organize complex tasks, and demonstrate
// thoroughness to the user.
package ii

import (
	"context"
	"fmt"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/tools"
)

// TodoWrite tool constants
const (
	TodoWriteName        = "TodoWrite"
	TodoWriteDisplayName = "Write todo list"
	TodoWriteSuccessMsg  = "Todos have been modified successfully. Ensure that you continue to use the todo list to track your progress. Please proceed with the current tasks if applicable."
)

// todoWriteDescription provides detailed guidance on when and how to use the tool
const todoWriteDescription = `Use this tool to create and manage a structured task list for your current coding session. This helps you track progress, organize complex tasks, and demonstrate thoroughness to the user. It also helps the user understand the progress of the task and overall progress of their requests.

## When to Use This Tool
Use this tool proactively in these scenarios:

1. Complex multi-step tasks - When a task requires 3 or more distinct steps or actions
2. Non-trivial and complex tasks - Tasks that require careful planning or multiple operations
3. User explicitly requests todo list - When the user directly asks you to use the todo list
4. User provides multiple tasks - When users provide a list of things to be done (numbered or comma-separated)
5. After receiving new instructions - Immediately capture user requirements as todos
6. When you start working on a task - Mark it as in_progress BEFORE beginning work. Multiple tasks can be in_progress simultaneously when working in parallel.
7. After completing a task - Mark it as completed and add any new follow-up tasks discovered during implementation

## When NOT to Use This Tool

Skip using this tool when:
1. There is only a single, straightforward task
2. The task is trivial and tracking it provides no organizational benefit
3. The task can be completed in less than 3 trivial steps
4. The task is purely conversational or informational

NOTE that you should not use this tool if there is only one trivial task to do. In this case you are better off just doing the task directly.

## Task States and Management

1. **Task States**: Use these states to track progress:
   - pending: Task not yet started
   - in_progress: Currently working on (limit to ONE task at a time)
   - completed: Task finished successfully

2. **Task Management**:
   - Update task status in real-time as you work
   - Mark tasks complete IMMEDIATELY after finishing (don't batch completions)
   - Only have ONE task in_progress at any time
   - Complete current tasks before starting new ones
   - Remove tasks that are no longer relevant from the list entirely

3. **Task Completion Requirements**:
   - ONLY mark a task as completed when you have FULLY accomplished it
   - If you encounter errors, blockers, or cannot finish, keep the task as in_progress
   - When blocked, create a new task describing what needs to be resolved
   - Never mark a task as completed if:
     - Tests are failing
     - Implementation is partial
     - You encountered unresolved errors
     - You couldn't find necessary files or dependencies

4. **Task Breakdown**:
   - Create specific, actionable items
   - Break complex tasks into smaller, manageable steps
   - Use clear, descriptive task names

When in doubt, use this tool. Being proactive with task management demonstrates attentiveness and ensures you complete all requirements successfully.`

// TodoWriteTool implements the todo list writing functionality
type TodoWriteTool struct {
	manager *TodoManager
}

// NewTodoWriteTool creates a new TodoWriteTool using the global todo manager
func NewTodoWriteTool() *TodoWriteTool {
	return &TodoWriteTool{
		manager: GetTodoManager(),
	}
}

// NewTodoWriteToolWithManager creates a new TodoWriteTool with a custom manager.
// This is primarily useful for testing.
func NewTodoWriteToolWithManager(manager *TodoManager) *TodoWriteTool {
	return &TodoWriteTool{
		manager: manager,
	}
}

// Name returns the tool name
func (t *TodoWriteTool) Name() string {
	return TodoWriteName
}

// DisplayName returns the human-readable display name
func (t *TodoWriteTool) DisplayName() string {
	return TodoWriteDisplayName
}

// Description returns the tool description
func (t *TodoWriteTool) Description() string {
	return todoWriteDescription
}

// Parameters returns the JSON schema for tool parameters
func (t *TodoWriteTool) Parameters() any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"todos": map[string]any{
				"type": "array",
				"items": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"id": map[string]any{
							"type":        "string",
							"description": "Unique identifier for the todo item (e.g., '1', '2', '3')",
						},
						"content": map[string]any{
							"type":        "string",
							"description": "Description of what needs to be done",
						},
						"status": map[string]any{
							"type":        "string",
							"enum":        []string{"pending", "in_progress", "completed"},
							"description": "Current status of the task",
						},
						"priority": map[string]any{
							"type":        "string",
							"enum":        []string{"low", "medium", "high"},
							"description": "Priority level of the task",
						},
					},
					"required": []string{"id", "content", "status", "priority"},
				},
				"description": "The complete todo list. Each todo must have id, content, status (pending/in_progress/completed), and priority (low/medium/high).",
			},
		},
		"required": []string{"todos"},
	}
}

// Execute writes/updates the todo list
func (t *TodoWriteTool) Execute(ctx context.Context, params map[string]any) (*tools.ToolResult, error) {
	// Check context cancellation
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}

	// Extract the todos parameter
	todosRaw, ok := params["todos"]
	if !ok {
		return tools.NewToolResult("ERROR: 'todos' parameter is required"), nil
	}

	// Handle the case where todos might be nil
	if todosRaw == nil {
		return tools.NewToolResult("ERROR: 'todos' cannot be null"), nil
	}

	// Convert to slice
	todosSlice, ok := todosRaw.([]any)
	if !ok {
		return tools.NewToolResult(fmt.Sprintf("ERROR: 'todos' must be an array, got %T", todosRaw)), nil
	}

	// Parse the todo items
	todoItems, err := TodoItemsFromSlice(todosSlice)
	if err != nil {
		return tools.NewToolResult(fmt.Sprintf("ERROR: Invalid todo format: %v", err)), nil
	}

	// Set the todos (validation happens inside SetTodos)
	if err := t.manager.SetTodos(todoItems); err != nil {
		return tools.NewToolResult(fmt.Sprintf("ERROR: Failed to update todo list: %v", err)), nil
	}

	// Build success response with summary
	summary := t.manager.Summary()
	response := fmt.Sprintf(
		"%s\n\nUpdated: %d total (%d pending, %d in progress, %d completed)",
		TodoWriteSuccessMsg,
		summary["total"],
		summary["pending"],
		summary["in_progress"],
		summary["completed"],
	)

	return tools.NewToolResult(response), nil
}

// Validate checks if the given parameters are valid
func (t *TodoWriteTool) Validate(params map[string]any) error {
	todosRaw, ok := params["todos"]
	if !ok {
		return fmt.Errorf("'todos' parameter is required")
	}

	if todosRaw == nil {
		return fmt.Errorf("'todos' cannot be null")
	}

	todosSlice, ok := todosRaw.([]any)
	if !ok {
		return fmt.Errorf("'todos' must be an array")
	}

	// Parse and validate each todo item
	todoItems, err := TodoItemsFromSlice(todosSlice)
	if err != nil {
		return fmt.Errorf("invalid todo format: %w", err)
	}

	// Validate each item
	for i, item := range todoItems {
		if err := item.Validate(); err != nil {
			return fmt.Errorf("todo %d: %w", i, err)
		}
	}

	return nil
}

// IsIdempotent returns false as writing todos modifies state
func (t *TodoWriteTool) IsIdempotent() bool {
	return false
}

// IsReadOnly returns false as this tool modifies data
func (t *TodoWriteTool) IsReadOnly() bool {
	return false
}

// RequiresPermission returns the permissions needed to execute this tool.
// Writing todos requires no special permissions.
func (t *TodoWriteTool) RequiresPermission() []tools.Permission {
	return []tools.Permission{}
}

// SupportedContentTypes returns the content types this tool can produce
func (t *TodoWriteTool) SupportedContentTypes() []tools.ContentType {
	return []tools.ContentType{tools.ContentTypeText}
}

// OptimizationHints provides guidance for efficient tool use
func (t *TodoWriteTool) OptimizationHints() *tools.OptimizationHints {
	return nil
}

// ShouldConfirmExecute returns nil as todo operations don't need user confirmation
func (t *TodoWriteTool) ShouldConfirmExecute(params map[string]any) *ConfirmationDetails {
	return nil
}

// Metadata returns tool metadata
func (t *TodoWriteTool) Metadata() map[string]any {
	return map[string]any{
		"category": "productivity",
		"readOnly": false,
	}
}
