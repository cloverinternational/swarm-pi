// Package ii provides the TodoRead tool for reading the current session's task list.
// This tool enables agents to track progress and maintain awareness of pending tasks.
package ii

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/tools"
)

// TodoRead tool constants
const (
	TodoReadName        = "TodoRead"
	TodoReadDisplayName = "Read todo list"
	TodoReadEmptyMsg    = "No todos found. The task list is empty."
	TodoReadSuccessMsg  = "Remember to continue to use update and read from the todo list as you make progress. Here is the current list:"
)

// todoReadDescription provides detailed guidance on when and how to use the tool
const todoReadDescription = `Use this tool to read the current to-do list for the session. This tool should be used proactively and frequently to ensure that you are aware of the status of the current task list.

You should make use of this tool as often as possible, especially in the following situations:
- At the beginning of conversations to see what's pending
- Before starting new tasks to prioritize work
- When the user asks about previous tasks or plans
- Whenever you're uncertain about what to do next
- After completing tasks to update your understanding of remaining work
- After every few messages to ensure you're on track

Usage:
- This tool takes no parameters. Call it without any input.
- Returns a list of todo items with their id, status, priority, and content
- Use this information to track progress and plan next steps
- If no todos exist yet, an empty list message will be returned

Each todo item contains:
- id: Unique identifier for the task
- content: Description of what needs to be done
- status: Current state (pending, in_progress, completed)
- priority: Importance level (low, medium, high)`

// TodoReadTool implements the todo list reading functionality
type TodoReadTool struct {
	manager *TodoManager
}

// NewTodoReadTool creates a new TodoReadTool using the global todo manager
func NewTodoReadTool() *TodoReadTool {
	return &TodoReadTool{
		manager: GetTodoManager(),
	}
}

// NewTodoReadToolWithManager creates a new TodoReadTool with a custom manager.
// This is primarily useful for testing.
func NewTodoReadToolWithManager(manager *TodoManager) *TodoReadTool {
	return &TodoReadTool{
		manager: manager,
	}
}

// Name returns the tool name
func (t *TodoReadTool) Name() string {
	return TodoReadName
}

// DisplayName returns the human-readable display name
func (t *TodoReadTool) DisplayName() string {
	return TodoReadDisplayName
}

// Description returns the tool description
func (t *TodoReadTool) Description() string {
	return todoReadDescription
}

// Parameters returns the JSON schema for tool parameters.
// This tool requires no parameters.
func (t *TodoReadTool) Parameters() any {
	return map[string]any{
		"type":       "object",
		"properties": map[string]any{},
		"required":   []string{},
	}
}

// Execute reads and returns the current todo list
func (t *TodoReadTool) Execute(ctx context.Context, params map[string]any) (*tools.ToolResult, error) {
	// Check context cancellation
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}

	todos := t.manager.Todos()

	if len(todos) == 0 {
		return tools.NewToolResult(TodoReadEmptyMsg), nil
	}

	// Format todos as JSON for LLM consumption
	todoMaps := TodoItemsToSlice(todos)
	jsonBytes, err := json.MarshalIndent(todoMaps, "", "  ")
	if err != nil {
		return tools.NewErrorResult(fmt.Errorf("failed to format todos: %w", err)), nil
	}

	// Build the response with summary and detailed list
	summary := t.manager.Summary()
	response := fmt.Sprintf(
		"%s\n\nSummary: %d total (%d pending, %d in progress, %d completed)\n\n%s",
		TodoReadSuccessMsg,
		summary["total"],
		summary["pending"],
		summary["in_progress"],
		summary["completed"],
		string(jsonBytes),
	)

	return tools.NewToolResult(response), nil
}

// Validate checks if the given parameters are valid.
// Since this tool takes no parameters, validation always passes.
func (t *TodoReadTool) Validate(params map[string]any) error {
	// No parameters to validate
	return nil
}

// IsIdempotent returns true as reading todos doesn't modify state
func (t *TodoReadTool) IsIdempotent() bool {
	return true
}

// IsReadOnly returns true as this tool only reads data
func (t *TodoReadTool) IsReadOnly() bool {
	return true
}

// RequiresPermission returns the permissions needed to execute this tool.
// Reading todos requires no special permissions.
func (t *TodoReadTool) RequiresPermission() []tools.Permission {
	return []tools.Permission{}
}

// SupportedContentTypes returns the content types this tool can produce
func (t *TodoReadTool) SupportedContentTypes() []tools.ContentType {
	return []tools.ContentType{tools.ContentTypeText}
}

// OptimizationHints provides guidance for efficient tool use
func (t *TodoReadTool) OptimizationHints() *tools.OptimizationHints {
	return nil
}

// ShouldConfirmExecute returns nil as read operations don't need confirmation
func (t *TodoReadTool) ShouldConfirmExecute(params map[string]any) *ConfirmationDetails {
	return nil
}

// Metadata returns tool metadata
func (t *TodoReadTool) Metadata() map[string]any {
	return map[string]any{
		"category": "productivity",
		"readOnly": true,
	}
}
