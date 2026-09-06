package ii

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/tools"
)

const legacyTaskOperationKey = "legacy"

func legacyTaskOperation(kind TaskOperationKind, params map[string]any) (TaskOperation, error) {
	raw := make(map[string]any, len(params)+2)
	for key, value := range params {
		raw[key] = value
	}
	raw["key"] = legacyTaskOperationKey
	raw["op"] = string(kind)
	return parseTaskOperation(raw, 0)
}

func validateLegacyTaskOperation(kind TaskOperationKind, params map[string]any) error {
	_, err := legacyTaskOperation(kind, params)
	return err
}

func executeLegacyTaskOperation(ctx context.Context, manager *TodoManager, kind TaskOperationKind, params map[string]any) (*TaskOperationData, *TaskOperationError, error) {
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	operation, err := legacyTaskOperation(kind, params)
	if err != nil {
		return nil, operationError(err), nil
	}
	batch := executeSequentialTaskBatch(ctx, manager, []TaskOperation{operation})
	if len(batch.Results) != 1 {
		return nil, operationError(taskFailure("operation_failed", "legacy task adapter returned no result")), nil
	}
	result := batch.Results[0]
	if result.Status != "succeeded" {
		return nil, result.Error, nil
	}
	return result.Data, nil, nil
}

func legacyTaskError(resultErr *TaskOperationError) *tools.ToolResult {
	if resultErr == nil {
		return tools.NewToolResult("ERROR: task operation failed")
	}
	return tools.NewToolResult("ERROR: " + resultErr.Message)
}

func (t *TaskCreateTool) executeCanonical(ctx context.Context, params map[string]any) (*tools.ToolResult, error) {
	data, resultErr, err := executeLegacyTaskOperation(ctx, t.manager, TaskOperationCreate, params)
	if err != nil {
		return nil, err
	}
	if resultErr != nil {
		return legacyTaskError(resultErr), nil
	}
	task := data.Task
	if task.ParentID != "" {
		return tools.NewToolResult(fmt.Sprintf("Task #%s created successfully under parent #%s: %s", task.ID, task.ParentID, task.Content)), nil
	}
	return tools.NewToolResult(fmt.Sprintf("Task #%s created successfully: %s", task.ID, task.Content)), nil
}

func (t *TaskGetTool) executeCanonical(ctx context.Context, params map[string]any) (*tools.ToolResult, error) {
	data, resultErr, err := executeLegacyTaskOperation(ctx, t.manager, TaskOperationGet, params)
	if err != nil {
		return nil, err
	}
	if resultErr != nil {
		return legacyTaskError(resultErr), nil
	}
	return formatLegacyTaskGet(data.Task)
}

func (t *TaskListTool) executeCanonical(ctx context.Context, params map[string]any) (*tools.ToolResult, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if resultErr := operationError(validateLegacyTaskOperation(TaskOperationList, params)); resultErr != nil {
		return legacyTaskError(resultErr), nil
	}
	category, _ := params["category"].(string)
	tasks := t.manager.Todos()
	if category != "" {
		tasks = t.manager.ByCategory(TaskCategory(category))
	}
	return formatLegacyTaskList(tasks, category), nil
}

func formatLegacyTaskGet(task *TodoItem) (*tools.ToolResult, error) {
	result := map[string]any{
		"id": task.ID, "subject": task.Content, "status": string(task.Status),
		"priority": string(task.Priority), "active": task.Active,
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
	if len(task.Notes) > 0 {
		result["notes"] = task.Notes
	}
	if len(task.TypedNotes) > 0 {
		result["typedNotes"] = task.TypedNotes
	}
	if len(task.AuditEvents) > 0 {
		result["auditEvents"] = task.AuditEvents
	}
	if task.Category != "" {
		result["category"] = string(task.Category)
	}
	if len(task.DependsOn) > 0 {
		result["blockedBy"] = task.DependsOn
	}
	if len(task.Blocks) > 0 {
		result["blocks"] = task.Blocks
	}
	body, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return tools.NewToolResult(fmt.Sprintf("ERROR: failed to format task: %v", err)), nil
	}
	var output strings.Builder
	fmt.Fprintf(&output, "Task #%s: %s\n", task.ID, task.Content)
	fmt.Fprintf(&output, "Status: %s | Priority: %s", task.Status, task.Priority)
	if task.Category != "" {
		fmt.Fprintf(&output, " | Category: %s", task.Category)
	}
	output.WriteString("\n")
	if len(task.DependsOn) > 0 {
		fmt.Fprintf(&output, "Blocked by: %s\n", strings.Join(task.DependsOn, ", "))
	}
	if len(task.Blocks) > 0 {
		fmt.Fprintf(&output, "Blocks: %s\n", strings.Join(task.Blocks, ", "))
	}
	output.WriteString("\n")
	output.Write(body)
	return tools.NewToolResult(output.String()), nil
}

func formatLegacyTaskList(tasks []TodoItem, category string) *tools.ToolResult {
	if len(tasks) == 0 {
		if category != "" {
			return tools.NewToolResult(fmt.Sprintf("No tasks found with category '%s'.", category))
		}
		return tools.NewToolResult("No tasks found. The task list is empty.")
	}
	counts := map[TodoStatus]int{}
	statusByID := make(map[string]TodoStatus, len(tasks))
	for _, task := range tasks {
		counts[task.Status]++
		statusByID[task.ID] = task.Status
	}
	var output strings.Builder
	fmt.Fprintf(&output, "Tasks: %d total (%d pending, %d in progress, %d completed)\n\n",
		len(tasks), counts[TodoStatusPending], counts[TodoStatusInProgress], counts[TodoStatusCompleted])
	for _, task := range tasks {
		categoryLabel := ""
		if task.Category != "" {
			categoryLabel = fmt.Sprintf(" [%s]", task.Category)
		}
		fmt.Fprintf(&output, "#%s. [%s]%s %s\n", task.ID, task.Status, categoryLabel, task.Content)
		openBlockers := make([]string, 0, len(task.DependsOn))
		for _, dependency := range task.DependsOn {
			if status, exists := statusByID[dependency]; exists && status != TodoStatusCompleted {
				openBlockers = append(openBlockers, dependency)
			}
		}
		if len(openBlockers) > 0 {
			fmt.Fprintf(&output, "   blocked by: [%s]\n", strings.Join(openBlockers, ", "))
		}
	}
	return tools.NewToolResult(output.String())
}

func (t *TaskUpdateTool) executeCanonical(ctx context.Context, params map[string]any) (*tools.ToolResult, error) {
	data, resultErr, err := executeLegacyTaskOperation(ctx, t.manager, TaskOperationUpdate, params)
	if err != nil {
		return nil, err
	}
	if resultErr != nil {
		return legacyTaskError(resultErr), nil
	}
	taskID, _ := params["taskId"].(string)
	if status, _ := params["status"].(string); status == "deleted" {
		return tools.NewToolResult(fmt.Sprintf("Deleted task #%s", taskID)), nil
	}
	if data == nil || data.Task == nil {
		return tools.NewToolResult(fmt.Sprintf("Updated task #%s", taskID)), nil
	}
	task := data.Task
	parts := []string{fmt.Sprintf("Updated task #%s", taskID)}
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
	var output strings.Builder
	output.WriteString(strings.Join(parts, " | "))
	inProgress := t.manager.ByStatus(TodoStatusInProgress)
	if len(inProgress) > 0 {
		output.WriteString("\n\nIn progress:")
		for _, active := range inProgress {
			fmt.Fprintf(&output, "\n  #%s. %s", active.ID, active.Content)
			if active.Category != "" {
				fmt.Fprintf(&output, " [%s]", active.Category)
			}
			if active.OwnerID != "" {
				fmt.Fprintf(&output, " (owner: %s)", active.OwnerID)
			}
		}
	}
	return tools.NewToolResult(output.String()), nil
}
