package ii

import (
	"context"
	"fmt"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/tools"
)

const (
	TaskManageName        = "TaskManage"
	TaskManageDisplayName = "Manage tasks"
	maxTaskOperations     = 50
	defaultTaskListLimit  = 50
	maxTaskListLimit      = 500
)

const (
	// minimalTaskCreateExample is quoted back to the caller when a create is
	// rejected for a missing subject, so the error itself describes a valid call.
	minimalTaskCreateExample = `{"key":"<your-key>","op":"create","subject":"<short imperative title>"}`
)

// TaskManageTool executes an ordered program of canonical task operations.
type TaskManageTool struct {
	manager *TodoManager
}

// NewTaskManageTool creates a TaskManage tool using the global manager.
func NewTaskManageTool() *TaskManageTool {
	return &TaskManageTool{manager: GetTodoManager()}
}

// NewTaskManageToolWithManager creates a TaskManage tool with a custom manager.
func NewTaskManageToolWithManager(manager *TodoManager) *TaskManageTool {
	return &TaskManageTool{manager: manager}
}

func (t *TaskManageTool) Name() string        { return TaskManageName }
func (t *TaskManageTool) DisplayName() string { return TaskManageDisplayName }
func (t *TaskManageTool) Description() string {
	return "Manage ordered tasks. sequential commits the successful prefix; atomic commits all or rolls back. {\"ref\":key} resolves an earlier operation key or a key registered by a successful prior call this session. create needs a non-blank subject; update/get need taskId or a registered key; list needs only key/op. List defaults to limit 50/offset 0 (max 500); follow pagination.total/more. All operations and mixed batches are allowed in plan mode."
}

func taskReferenceSchema() map[string]any {
	return map[string]any{
		"oneOf": []any{
			map[string]any{"type": "string"},
			map[string]any{
				"type": "object",
				"properties": map[string]any{
					"ref":   map[string]any{"type": "string"},
					"field": map[string]any{"type": "string", "enum": []string{"taskId"}},
				},
				"required":             []string{"ref"},
				"additionalProperties": false,
			},
		},
	}
}

func taskOperationSchema() map[string]any {
	categories := []string{"researching", "planning", "acting", "verifying", "debugging", "documenting"}
	ref := taskReferenceSchema()
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"key": map[string]any{"type": "string"},
			// NOTE: "key" only needs to be unique WITHIN this call's operations
			// array (parseTaskOperations enforces that). Reusing a key from a
			// previous, separate TaskManage call is intentional and expected —
			// see Description() and taskReferenceSchema(): a ref falls back to
			// the durable per-session key registry when not found in this call.
			"op":            map[string]any{"type": "string", "enum": []string{"create", "update", "get", "list"}},
			"taskId":        ref,
			"subject":       map[string]any{"type": "string", "description": "Task title or list filter."},
			"description":   map[string]any{"type": "string"},
			"activeForm":    map[string]any{"type": "string"},
			"category":      map[string]any{"type": "string", "enum": categories},
			"metadata":      map[string]any{"type": "object"},
			"parentTaskId":  taskReferenceSchema(),
			"owner_id":      map[string]any{"type": "string"},
			"status":        map[string]any{"type": "string", "enum": []string{"pending", "in_progress", "completed", "deleted"}},
			"active":        map[string]any{"type": "boolean"},
			"limit":         map[string]any{"type": "integer", "minimum": 1, "maximum": maxTaskListLimit},
			"offset":        map[string]any{"type": "integer", "minimum": 0},
			"addBlocks":     map[string]any{"type": "array", "items": taskReferenceSchema()},
			"addBlockedBy":  map[string]any{"type": "array", "items": taskReferenceSchema()},
			"addNote":       map[string]any{"type": "string"},
			"noteType":      map[string]any{"type": "string", "enum": []string{"decision", "blocker", "learning", "milestone", "question", "observation", "other"}},
			"include_audit": map[string]any{"type": "boolean"},
		},
		"required":             []string{"key", "op"},
		"additionalProperties": false,
	}
}

func (t *TaskManageTool) Parameters() any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"operations": map[string]any{
				"type":     "array",
				"minItems": 1,
				"maxItems": maxTaskOperations,
				"items":    taskOperationSchema(),
			},
			"mode": map[string]any{
				"type": "string",
				"enum": []string{"sequential", "atomic"},
			},
		},
		"required":             []string{"operations"},
		"additionalProperties": false,
	}
}

// InspectTaskManageOperations parses and validates raw TaskManage parameters without
// executing any operation or changing task state. Hooks use this to apply
// operation-sensitive policy to a TaskManage batch.
func InspectTaskManageOperations(params map[string]any) ([]TaskOperation, error) {
	operations, _, err := parseTaskOperations(params)
	return operations, err
}

func parseTaskOperations(params map[string]any) ([]TaskOperation, string, error) {
	for field := range params {
		if field != "operations" && field != "mode" {
			return nil, "", taskFailure("validation_failed", "unknown top-level field %q", field)
		}
	}
	rawOperations, ok := params["operations"].([]any)
	if !ok || len(rawOperations) == 0 {
		return nil, "", taskFailure("validation_failed", "operations must contain at least one operation")
	}
	if len(rawOperations) > maxTaskOperations {
		return nil, "", taskFailure("validation_failed", "operations exceeds maximum of %d", maxTaskOperations)
	}
	mode := "sequential"
	if value, present := params["mode"]; present {
		var valid bool
		mode, valid = value.(string)
		if !valid {
			return nil, "", taskFailure("validation_failed", "mode must be a string")
		}
	}
	if mode != "sequential" && mode != "atomic" {
		return nil, "", taskFailure("validation_failed", "unsupported mode %q", mode)
	}
	operations := make([]TaskOperation, 0, len(rawOperations))
	// keyFirstKind records the operation kind that first claimed each key.
	// The documented targeting rule for update/get is "omit taskId to
	// target the task registered under this operation's key" — i.e. a
	// create establishes a key's identity, and later update/get operations
	// in the SAME batch are meant to reuse that exact key to address the
	// task the create just made (resolveTaskOperation in task_operation.go
	// already implements this lookup via manager.ResolveTaskKey). Rejecting
	// every duplicate key outright made that documented pattern impossible
	// (issues #286, #296). Only a second CREATE under the same key is
	// actually ambiguous — it's unclear which of the two new tasks a later
	// {"ref": key} should resolve to — so that (and any other kind reusing
	// a key that wasn't first established by a create) still fails, but with
	// an error that names the fix instead of just the symptom.
	keyFirstKind := make(map[string]TaskOperationKind, len(rawOperations))
	for index, raw := range rawOperations {
		object, valid := raw.(map[string]any)
		if !valid {
			return nil, "", taskFailure("validation_failed", "operation %d must be an object", index)
		}
		op, err := parseTaskOperation(object, index)
		if err != nil {
			return nil, "", err
		}
		if firstKind, seen := keyFirstKind[op.Key]; seen {
			if firstKind != TaskOperationCreate || op.Kind == TaskOperationCreate {
				return nil, "", taskFailure("validation_failed",
					"duplicate operation key %q: keys must be unique per operation, except that "+
						"an update/get MAY reuse the exact key an earlier create in this batch used, "+
						"to target the task that create just made (e.g. "+
						`[{"key":%[1]q,"op":"create",...},{"key":%[1]q,"op":"update",...}]`+
						"). Give this operation a distinct key instead, and if it needs to target "+
						"another operation's task, use taskId:{\"ref\":\"<that operation's key>\"}.",
					op.Key)
			}
			// Reusing a create's key from a later update/get is the
			// documented targeting pattern — allow it, and keep the
			// recorded kind as "create" so a THIRD operation reusing the
			// same key is also accepted.
		} else {
			keyFirstKind[op.Key] = op.Kind
		}
		operations = append(operations, op)
	}
	return operations, mode, nil
}

func (t *TaskManageTool) Execute(ctx context.Context, params map[string]any) (*tools.ToolResult, error) {
	operations, mode, err := parseTaskOperations(params)
	if err != nil {
		return taskManageFailureResult("batch", "", err)
	}
	var result TaskBatchResult
	if mode == "atomic" {
		result = executeAtomicTaskBatch(ctx, t.manager, operations)
	} else {
		result = executeSequentialTaskBatch(ctx, t.manager, operations)
	}
	prepareTaskManageResponse(&result, operations)
	output, err := marshalTaskBatchResult(result)
	if err != nil {
		return nil, err
	}
	return tools.NewToolResult(output), nil
}

func executeSequentialTaskBatch(ctx context.Context, manager *TodoManager, operations []TaskOperation) TaskBatchResult {
	result := TaskBatchResult{Status: "succeeded", Results: make([]TaskOperationResult, 0, len(operations))}
	byKey := make(map[string]TaskOperationResult, len(operations))
	failed := false
	for _, op := range operations {
		if failed {
			entry := TaskOperationResult{Key: op.Key, Op: op.Kind, Status: "skipped"}
			result.Results = append(result.Results, entry)
			byKey[op.Key] = entry
			continue
		}
		if err := ctx.Err(); err != nil {
			entry := failedTaskOperationResult(op, taskFailure("cancelled", "%s", err))
			result.Results = append(result.Results, entry)
			byKey[op.Key] = entry
			failed = true
			continue
		}
		resolved, err := resolveTaskOperation(op, byKey, manager)
		if err != nil {
			entry := failedTaskOperationResult(op, err)
			result.Results = append(result.Results, entry)
			byKey[op.Key] = entry
			failed = true
			continue
		}
		data, err := executeTaskOperation(manager, resolved)
		if err != nil {
			entry := failedTaskOperationResult(op, err)
			result.Results = append(result.Results, entry)
			byKey[op.Key] = entry
			failed = true
			continue
		}
		entry := TaskOperationResult{Key: op.Key, Op: op.Kind, Status: "succeeded", Data: data}
		result.Results = append(result.Results, entry)
		byKey[op.Key] = entry
		// Each operation here has already committed (this is not atomic
		// mode), so it's safe to remember its key immediately for
		// resolution by a later, separate TaskManage call — see issue #73.
		if data != nil && data.Task != nil {
			manager.RememberTaskKey(op.Key, data.Task.ID)
		}
	}
	result.Status = taskBatchStatus(result.Results)
	return result
}

func executeAtomicTaskBatch(ctx context.Context, manager *TodoManager, operations []TaskOperation) TaskBatchResult {
	result := TaskBatchResult{Status: "succeeded", Results: make([]TaskOperationResult, 0, len(operations))}
	byKey := make(map[string]TaskOperationResult, len(operations))
	failureIndex := -1
	var operationFailure error
	// Key registrations are deferred and only applied after the atomic
	// batch actually commits — an operation that only "succeeded" inside
	// a batch that later rolls back must NOT leave a durable key behind.
	var pendingKeyRegistrations []keyRegistration
	var pendingDeletedIDs []string
	err := manager.mutateAtomically(func(candidate *TodoManager) error {
		for index, op := range operations {
			if err := ctx.Err(); err != nil {
				failureIndex, operationFailure = index, taskFailure("cancelled", "%s", err)
				return operationFailure
			}
			resolved, err := resolveTaskOperation(op, byKey, manager)
			if err != nil {
				failureIndex, operationFailure = index, err
				return err
			}
			data, err := executeTaskOperation(candidate, resolved)
			if err != nil {
				failureIndex, operationFailure = index, err
				return err
			}
			entry := TaskOperationResult{Key: op.Key, Op: op.Kind, Status: "succeeded", Data: data}
			result.Results = append(result.Results, entry)
			byKey[op.Key] = entry
			if data != nil && data.Task != nil {
				pendingKeyRegistrations = append(pendingKeyRegistrations, keyRegistration{key: op.Key, id: data.Task.ID})
			}
			if op.Kind == TaskOperationUpdate && op.Status == "deleted" && resolved.TaskID != nil {
				deletedID := resolved.TaskID.ID
				pendingDeletedIDs = append(pendingDeletedIDs, deletedID)
				// A task produced/read earlier in this batch may already
				// have pending registrations. Do not resurrect those keys
				// after the later deletion commits.
				kept := pendingKeyRegistrations[:0]
				for _, registration := range pendingKeyRegistrations {
					if registration.id != deletedID {
						kept = append(kept, registration)
					}
				}
				pendingKeyRegistrations = kept
			}
		}
		if err := ctx.Err(); err != nil {
			failureIndex, operationFailure = len(operations), taskFailure("cancelled", "%s", err)
			return operationFailure
		}
		return nil
	})
	if err == nil {
		for _, id := range pendingDeletedIDs {
			manager.forgetTaskID(id)
		}
		for _, reg := range pendingKeyRegistrations {
			manager.RememberTaskKey(reg.key, reg.id)
		}
		return result
	}
	if failureIndex < 0 {
		failureIndex = len(result.Results)
		operationFailure = classifyTaskError(err)
	}
	rolledBack := &TaskOperationError{Code: "atomic_rollback", Message: "operation was rolled back because the atomic batch failed", Retryable: false}
	for index := range result.Results {
		result.Results[index].Status = "failed"
		result.Results[index].Data = nil
		result.Results[index].Error = rolledBack
	}
	if failureIndex == len(operations) && len(result.Results) > 0 {
		result.Results[len(result.Results)-1].Error = operationError(operationFailure)
	}
	if failureIndex < len(operations) {
		op := operations[failureIndex]
		result.Results = append(result.Results, failedTaskOperationResult(op, operationFailure))
		failureIndex++
	}
	for ; failureIndex < len(operations); failureIndex++ {
		op := operations[failureIndex]
		result.Results = append(result.Results, TaskOperationResult{Key: op.Key, Op: op.Kind, Status: "skipped"})
	}
	result.Status = "failed"
	return result
}

func failedTaskOperationResult(op TaskOperation, err error) TaskOperationResult {
	return TaskOperationResult{Key: op.Key, Op: op.Kind, Status: "failed", Error: operationError(err)}
}

// keyRegistration is a deferred (key -> task ID) mapping to persist into
// TodoManager.keyRegistry only once we know the enclosing batch actually
// committed. Used by executeAtomicTaskBatch.
type keyRegistration struct {
	key string
	id  string
}

func taskBatchStatus(results []TaskOperationResult) string {
	succeeded := 0
	failed := 0
	for _, result := range results {
		switch result.Status {
		case "succeeded":
			succeeded++
		case "failed":
			failed++
		}
	}
	if failed == 0 {
		return "succeeded"
	}
	if succeeded == 0 {
		return "failed"
	}
	return "partial"
}

func taskManageFailureResult(key string, op TaskOperationKind, err error) (*tools.ToolResult, error) {
	result := TaskBatchResult{Status: "failed", Results: []TaskOperationResult{{Key: key, Op: op, Status: "failed", Error: operationError(err)}}}
	output, marshalErr := marshalTaskBatchResult(result)
	if marshalErr != nil {
		return nil, fmt.Errorf("task manage failure: %w", marshalErr)
	}
	return tools.NewToolResult(output), nil
}

func (t *TaskManageTool) Validate(params map[string]any) error {
	_, _, err := parseTaskOperations(params)
	return err
}
func (t *TaskManageTool) IsIdempotent() bool                     { return false }
func (t *TaskManageTool) IsReadOnly() bool                       { return false }
func (t *TaskManageTool) RequiresPermission() []tools.Permission { return []tools.Permission{} }
func (t *TaskManageTool) SupportedContentTypes() []tools.ContentType {
	return []tools.ContentType{tools.ContentTypeText}
}
func (t *TaskManageTool) OptimizationHints() *tools.OptimizationHints              { return nil }
func (t *TaskManageTool) ShouldConfirmExecute(map[string]any) *ConfirmationDetails { return nil }
func (t *TaskManageTool) Metadata() map[string]any {
	return map[string]any{"category": "productivity", "readOnly": false, "batch": true}
}
