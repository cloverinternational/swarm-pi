package compaction

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/tools"
)

// CompactionTodoToolName is the name of the compaction todo tool
const CompactionTodoToolName = "CompactionTodoUpdate"

// CompactionTodoTool enables updating todos during conversation summarization.
// When compacting, Claude can use this tool to explicitly update/complete todos
// that should be preserved in the compacted conversation.
type CompactionTodoTool struct {
	compCtx *CompactionContext
}

// NewCompactionTodoTool creates a new todo tool for compaction context.
// This tool is injected into the summarization context so Claude can update todos
// during the summarization process.
func NewCompactionTodoTool(compCtx *CompactionContext) *CompactionTodoTool {
	if compCtx == nil {
		compCtx = &CompactionContext{}
	}
	return &CompactionTodoTool{compCtx: compCtx}
}

// Name returns the tool name
func (t *CompactionTodoTool) Name() string {
	return CompactionTodoToolName
}

// DisplayName returns the human-readable display name
func (t *CompactionTodoTool) DisplayName() string {
	return "Update Todos During Compaction"
}

// Description returns a detailed description of what this tool does
func (t *CompactionTodoTool) Description() string {
	return `Update todos during conversation compaction.

This tool allows you to explicitly update which todos should be preserved in the compacted conversation summary.

Use this to:
- Mark todos as completed when they were finished in the session
- Update todo status (pending -> in_progress or in_progress -> completed)
- Ensure important tasks are preserved through compaction
- Remove todos that are no longer relevant

The updated todos will be included in the "Pending & Next Steps" section of the compaction summary,
ensuring the user knows exactly what work remains.`
}

// Parameters returns the JSON schema for tool parameters
func (t *CompactionTodoTool) Parameters() any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"action": map[string]any{
				"type":        "string",
				"description": "Action to perform: 'update' (modify todo status), 'remove' (delete todo), 'list' (show current todos)",
				"enum":        []string{"update", "remove", "list"},
			},
			"todo_id": map[string]any{
				"type":        "string",
				"description": "Identifier for the todo (index or content prefix) - required for 'update' and 'remove'",
			},
			"status": map[string]any{
				"type":        "string",
				"description": "New status for the todo: 'pending', 'in_progress', or 'completed' - required for 'update'",
				"enum":        []string{"pending", "in_progress", "completed"},
			},
			"content": map[string]any{
				"type":        "string",
				"description": "New content for the todo - optional for 'update'",
			},
			"active_form": map[string]any{
				"type":        "string",
				"description": "Present continuous form of the todo (e.g., 'Running tests') - optional for 'update'",
			},
		},
		"required": []string{"action"},
	}
}

// Validate checks if the parameters are valid
func (t *CompactionTodoTool) Validate(params map[string]any) error {
	if action, ok := params["action"].(string); !ok || action == "" {
		return fmt.Errorf("action is required and must be a string")
	}

	action := params["action"].(string)
	if action != "update" && action != "remove" && action != "list" {
		return fmt.Errorf("action must be 'update', 'remove', or 'list'")
	}

	if action == "update" || action == "remove" {
		if todoID, ok := params["todo_id"].(string); !ok || todoID == "" {
			return fmt.Errorf("todo_id is required for '%s' action", action)
		}
	}

	if action == "update" {
		if status, ok := params["status"].(string); !ok || status == "" {
			return fmt.Errorf("status is required for 'update' action")
		}
		status := params["status"].(string)
		if status != "pending" && status != "in_progress" && status != "completed" {
			return fmt.Errorf("status must be 'pending', 'in_progress', or 'completed'")
		}
	}

	return nil
}

// Execute performs the todo update operation
func (t *CompactionTodoTool) Execute(ctx context.Context, params map[string]any) (*tools.ToolResult, error) {
	if err := t.Validate(params); err != nil {
		return tools.NewErrorResult(err), nil
	}

	action := params["action"].(string)

	switch action {
	case "list":
		return t.listTodos()
	case "update":
		return t.updateTodo(params)
	case "remove":
		return t.removeTodo(params)
	default:
		return tools.NewErrorResult(fmt.Errorf("unknown action: %s", action)), nil
	}
}

// listTodos returns current todos in the compaction context
func (t *CompactionTodoTool) listTodos() (*tools.ToolResult, error) {
	if t.compCtx == nil {
		return tools.NewToolResult("No todos in current compaction context"), nil
	}

	type TodoSummary struct {
		ID       int    `json:"id"`
		Status   string `json:"status"`
		Content  string `json:"content"`
		FormForm string `json:"active_form"`
	}

	var summaries []TodoSummary
	for i, todo := range t.compCtx.ActiveTodos {
		summaries = append(summaries, TodoSummary{
			ID:       i,
			Status:   todo.Status,
			Content:  todo.Content,
			FormForm: todo.ActiveForm,
		})
	}

	data, _ := json.MarshalIndent(summaries, "", "  ")
	return tools.NewToolResult(string(data)), nil
}

// updateTodo modifies a todo's status or content
func (t *CompactionTodoTool) updateTodo(params map[string]any) (*tools.ToolResult, error) {
	todoID := params["todo_id"].(string)
	status := params["status"].(string)
	content, hasContent := params["content"].(string)
	activeForm, hasActiveForm := params["active_form"].(string)

	if t.compCtx == nil {
		return tools.NewErrorResult(fmt.Errorf("no compaction context available")), nil
	}

	// Find todo by ID (could be index or content prefix)
	var todoIndex int = -1
	for i, todo := range t.compCtx.ActiveTodos {
		if fmt.Sprintf("%d", i) == todoID || todo.Content == todoID {
			todoIndex = i
			break
		}
	}

	if todoIndex == -1 {
		return tools.NewErrorResult(fmt.Errorf("todo with ID '%s' not found", todoID)), nil
	}

	// Update the todo
	t.compCtx.ActiveTodos[todoIndex].Status = status
	if hasContent {
		t.compCtx.ActiveTodos[todoIndex].Content = content
	}
	if hasActiveForm {
		t.compCtx.ActiveTodos[todoIndex].ActiveForm = activeForm
	}

	// If status is completed, move to CompletedTodos
	if status == "completed" {
		completedTodo := t.compCtx.ActiveTodos[todoIndex]
		t.compCtx.CompletedTodos = append(t.compCtx.CompletedTodos, completedTodo)
		// Remove from active todos
		t.compCtx.ActiveTodos = append(t.compCtx.ActiveTodos[:todoIndex], t.compCtx.ActiveTodos[todoIndex+1:]...)
	}

	return tools.NewToolResult(fmt.Sprintf("Updated todo '%s' to status '%s'", todoID, status)), nil
}

// removeTodo deletes a todo from the active list
func (t *CompactionTodoTool) removeTodo(params map[string]any) (*tools.ToolResult, error) {
	todoID := params["todo_id"].(string)

	if t.compCtx == nil {
		return tools.NewErrorResult(fmt.Errorf("no compaction context available")), nil
	}

	// Find todo by ID
	var todoIndex int = -1
	for i, todo := range t.compCtx.ActiveTodos {
		if fmt.Sprintf("%d", i) == todoID || todo.Content == todoID {
			todoIndex = i
			break
		}
	}

	if todoIndex == -1 {
		return tools.NewErrorResult(fmt.Errorf("todo with ID '%s' not found", todoID)), nil
	}

	removedContent := t.compCtx.ActiveTodos[todoIndex].Content
	t.compCtx.ActiveTodos = append(t.compCtx.ActiveTodos[:todoIndex], t.compCtx.ActiveTodos[todoIndex+1:]...)

	return tools.NewToolResult(fmt.Sprintf("Removed todo: '%s'", removedContent)), nil
}

// IsIdempotent returns whether this tool is idempotent
func (t *CompactionTodoTool) IsIdempotent() bool {
	return false // Todo updates are not idempotent
}

// RequiresPermission returns the permissions this tool requires
func (t *CompactionTodoTool) RequiresPermission() []tools.Permission {
	// Todo updates during compaction don't require special permissions
	// since compaction is initiated by authorized operations
	return []tools.Permission{}
}

// SupportedContentTypes returns content types this tool supports
func (t *CompactionTodoTool) SupportedContentTypes() []tools.ContentType {
	return []tools.ContentType{tools.ContentTypeText}
}

// OptimizationHints returns hints for optimization
func (t *CompactionTodoTool) OptimizationHints() *tools.OptimizationHints {
	// Fast, low-latency operations since we're just updating in-memory state
	return &tools.OptimizationHints{
		PreferSequential:  false,
		EstimatedDuration: 0, // Essentially instant (in-memory updates)
		CanBatch:          true,
		BatchSize:         10,
		Priority:          70, // Higher priority since todos are important state
		MinimalLatency:    true,
		Cacheable:         false, // Todo state is constantly changing
		CacheTTL:          0,
	}
}

// Context keys for injecting the compaction todo tool
const compactionTodoToolKey = "compaction_todo_tool"
const compactionContextKey = "compaction_context"

// WithCompactionTodoTool injects the compaction todo tool into the context
func WithCompactionTodoTool(ctx context.Context, tool *CompactionTodoTool) context.Context {
	return context.WithValue(ctx, compactionTodoToolKey, tool)
}

// GetCompactionTodoTool retrieves the compaction todo tool from the context
func GetCompactionTodoTool(ctx context.Context) *CompactionTodoTool {
	if tool, ok := ctx.Value(compactionTodoToolKey).(*CompactionTodoTool); ok {
		return tool
	}
	return nil
}

// WithCompactionContext injects the compaction context into the context
func WithCompactionContext(ctx context.Context, compCtx *CompactionContext) context.Context {
	return context.WithValue(ctx, compactionContextKey, compCtx)
}

// GetCompactionContext retrieves the compaction context from the context
func GetCompactionContext(ctx context.Context) *CompactionContext {
	if compCtx, ok := ctx.Value(compactionContextKey).(*CompactionContext); ok {
		return compCtx
	}
	return nil
}
