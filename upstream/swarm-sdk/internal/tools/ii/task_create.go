// Package ii provides the TaskCreate tool for adding individual tasks with dependency support.
package ii

import (
	"context"
	"fmt"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/tools"
)

// TaskCreate tool constants
const (
	TaskCreateName        = "TaskCreate"
	TaskCreateDisplayName = "Create a task"
)

const taskCreateDescription = `Use this tool to create a structured task list for your current coding session. This helps you track progress, organize complex tasks, and demonstrate thoroughness to the user.
It also helps the user understand the progress of the task and overall progress of their requests.

## When to Use This Tool

Use this tool proactively in these scenarios:

- Complex multi-step tasks - When a task requires 3 or more distinct steps or actions
- Non-trivial and complex tasks - Tasks that require careful planning or multiple operations
- Plan mode - When using plan mode, create a task list to track the work
- User explicitly requests todo list - When the user directly asks you to use the todo list
- User provides multiple tasks - When users provide a list of things to be done (numbered or comma-separated)
- After receiving new instructions - Immediately capture user requirements as tasks
- When you start working on a task - Mark it as in_progress BEFORE beginning work
- After completing a task - Mark it as completed and add any new follow-up tasks discovered during implementation

## When NOT to Use This Tool

Skip using this tool when:
- There is only a single, straightforward task
- The task is trivial and tracking it provides no organizational benefit
- The task can be completed in less than 3 trivial steps
- The task is purely conversational or informational

NOTE that you should not use this tool if there is only one trivial task to do. In this case you are better off just doing the task directly.

## Plan Mode Integration

**IMPORTANT**: For complex tasks involving architecture changes, multi-file refactors, or new features:
1. First use the Plan Mode tool (enter_plan_mode) to design the approach
2. Plan mode helps you explore the codebase and create a detailed implementation plan
3. After user approval, create tasks from the plan and execute

## Task Categories

Tasks are categorized by the type of work. The category is automatically inferred from the subject, but you can explicitly set it:

| Category | Letter | Description | When to Use |
|----------|--------|-------------|-------------|
| researching | R | Exploration, reading, searching | Understanding codebase, finding files, analyzing patterns |
| planning | P | Design, architecture, strategy | Before implementing complex changes |
| acting | A | Implementation, coding, writing | Making changes to code (default) |
| verifying | V | Testing, validation, confirmation | Running tests, checking outputs |
| debugging | D | Tracing, fixing, error resolution | Fixing bugs, diagnosing issues |
| documenting | X | Docs, comments, README | Writing documentation |

## Recommended Workflow

Follow this task flow for complete implementation:
1. **Research** 	 Understand the codebase and requirements
2. **Plan** 	 Design the approach (use Plan Mode for complex tasks)
3. **Act** 	 Implement the changes
4. **Verify** 	 Run tests and validate outputs
5. **Document** 	 Update docs and comments

## Task Fields

- **subject**: A brief, actionable title in imperative form (e.g., "Fix authentication bug in login flow")
- **description**: Detailed description of what needs to be done, including context and acceptance criteria
- **activeForm**: Present continuous form shown in spinner when task is in_progress (e.g., "Fixing authentication bug"). This is displayed to the user while you work on the task.
- **category**: Optional task category (researching|planning|acting|verifying|debugging|documenting). Auto-inferred if not provided.

**IMPORTANT**: Always provide activeForm when creating tasks. The subject should be imperative ("Run tests") while activeForm should be present continuous ("Running tests"). All tasks are created with status ` + "`pending`" + `.

## Tips

- Create tasks with clear, specific subjects that describe the outcome
- Include enough detail in the description for another agent to understand and complete the task
- After creating tasks, use TaskManage update operations to set dependencies (blocks/blockedBy) if needed
- Use a TaskManage list operation first to avoid creating duplicate tasks
- Use Plan Mode (enter_plan_mode) for complex architectural changes
`

// TaskCreateTool implements task creation with dependency support
type TaskCreateTool struct {
	manager *TodoManager
}

// NewTaskCreateTool creates a new TaskCreateTool using the global todo manager
func NewTaskCreateTool() *TaskCreateTool {
	return &TaskCreateTool{manager: GetTodoManager()}
}

// NewTaskCreateToolWithManager creates a new TaskCreateTool with a custom manager (for testing)
func NewTaskCreateToolWithManager(manager *TodoManager) *TaskCreateTool {
	return &TaskCreateTool{manager: manager}
}

// Name returns the tool name
func (t *TaskCreateTool) Name() string { return TaskCreateName }

// DisplayName returns the human-readable display name
func (t *TaskCreateTool) DisplayName() string { return TaskCreateDisplayName }

// Description returns the tool description
func (t *TaskCreateTool) Description() string { return taskCreateDescription }

// Parameters returns the JSON schema for tool parameters
func (t *TaskCreateTool) Parameters() any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"subject": map[string]any{
				"type":        "string",
				"description": "A brief title for the task",
			},
			"description": map[string]any{
				"type":        "string",
				"description": "A detailed description of what needs to be done",
			},
			"activeForm": map[string]any{
				"type":        "string",
				"description": "Present continuous form shown in spinner when in_progress (e.g., \"Running tests\")",
			},
			"category": map[string]any{
				"type":        "string",
				"enum":        []string{"researching", "planning", "acting", "verifying", "debugging", "documenting"},
				"description": "Type of work: researching (explore/read), planning (design/architect), acting (implement/code, default), verifying (test/validate), debugging (fix/trace), documenting (docs/comments)",
			},
			"metadata": map[string]any{
				"type":        "object",
				"description": "Arbitrary metadata to attach to the task",
			},
			"parentTaskId": map[string]any{
				"type":        "string",
				"description": "Parent task ID; makes this a subtask (hierarchy edge, independent of dependencies).",
			},
			"owner_id": map[string]any{
				"type":        "string",
				"description": "Optional owner ID. If not set, uses the current context owner (set by subagent executor).",
			},
		},
		"required": []string{"subject"},
	}
}

// Execute adapts the legacy TaskCreate contract to the canonical operation engine.
func (t *TaskCreateTool) Execute(ctx context.Context, params map[string]any) (*tools.ToolResult, error) {
	return t.executeCanonical(ctx, params)
}

func (t *TaskCreateTool) executeDirect(ctx context.Context, params map[string]any) (*tools.ToolResult, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}

	subject, _ := params["subject"].(string)
	description, _ := params["description"].(string)
	ownerID, _ := params["owner_id"].(string)
	activeForm, _ := params["activeForm"].(string)
	parentTaskID, _ := params["parentTaskId"].(string)

	if subject == "" {
		return tools.NewToolResult("ERROR: 'subject' is required"), nil
	}

	var metadata map[string]any
	if raw, ok := params["metadata"].(map[string]any); ok {
		metadata = raw
	}

	if parentTaskID != "" && t.manager.ByID(parentTaskID) == nil {
		return tools.NewToolResult(fmt.Sprintf("ERROR: parent task %s not found", parentTaskID)), nil
	}

	// Parse category (optional)
	category := TaskCategory("")
	if cat, ok := params["category"].(string); ok && cat != "" {
		category = TaskCategory(cat)
	}
	// Infer category from content if not provided
	if category == "" {
		combinedContent := subject + " " + description
		category = InferCategory(combinedContent)
	}

	newTask := TodoItem{
		Content:     subject,
		Description: description,
		ActiveForm:  activeForm,
		Metadata:    metadata,
		Status:      TodoStatusPending,
		Priority:    TodoPriorityMedium,
		Category:    category,
		ParentID:    parentTaskID,
		OwnerID:     ownerID, // Will be set from context/parent if empty in AddTodoAutoID
	}

	// Use atomic ID generation to prevent race conditions
	generatedID, err := t.manager.AddTodoAutoID(newTask)
	if err != nil {
		return tools.NewToolResult(fmt.Sprintf("ERROR: %v", err)), nil
	}

	if parentTaskID != "" {
		return tools.NewToolResult(fmt.Sprintf("Task #%s created successfully under parent #%s: %s", generatedID, parentTaskID, subject)), nil
	}
	return tools.NewToolResult(fmt.Sprintf("Task #%s created successfully: %s", generatedID, subject)), nil
}

// Validate checks if the given parameters are valid
func (t *TaskCreateTool) Validate(params map[string]any) error {
	return validateLegacyTaskOperation(TaskOperationCreate, params)
}

// IsIdempotent returns false as creating tasks modifies state
func (t *TaskCreateTool) IsIdempotent() bool { return false }

// IsReadOnly returns false as this tool modifies data
func (t *TaskCreateTool) IsReadOnly() bool { return false }

// RequiresPermission returns no special permissions
func (t *TaskCreateTool) RequiresPermission() []tools.Permission { return []tools.Permission{} }

// SupportedContentTypes returns text content type
func (t *TaskCreateTool) SupportedContentTypes() []tools.ContentType {
	return []tools.ContentType{tools.ContentTypeText}
}

// OptimizationHints returns nil
func (t *TaskCreateTool) OptimizationHints() *tools.OptimizationHints { return nil }

// ShouldConfirmExecute returns nil as task operations don't need confirmation
func (t *TaskCreateTool) ShouldConfirmExecute(params map[string]any) *ConfirmationDetails {
	return nil
}

// Metadata returns tool metadata
func (t *TaskCreateTool) Metadata() map[string]any {
	return map[string]any{"category": "productivity", "readOnly": false}
}
