package ii

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// TaskOperationKind identifies one canonical task-management operation.
type TaskOperationKind string

const (
	TaskOperationCreate TaskOperationKind = "create"
	TaskOperationUpdate TaskOperationKind = "update"
	TaskOperationGet    TaskOperationKind = "get"
	TaskOperationList   TaskOperationKind = "list"
)

// TaskOperationsAreReadOnly reports whether operations is a non-empty batch
// containing only get and list operations.
func TaskOperationsAreReadOnly(operations []TaskOperation) bool {
	if len(operations) == 0 {
		return false
	}
	for _, operation := range operations {
		if operation.Kind != TaskOperationGet && operation.Kind != TaskOperationList {
			return false
		}
	}
	return true
}

// TaskReference points to the task produced by an earlier successful operation.
type TaskReference struct {
	Ref   string `json:"ref"`
	Field string `json:"field,omitempty"`
}

// TaskTarget is either a literal task ID or a reference resolved by TaskManage.
type TaskTarget struct {
	ID        string         `json:"-"`
	Reference *TaskReference `json:"-"`
}

// TaskOperation is the typed canonical representation used after input parsing.
type TaskOperation struct {
	Key          string
	Kind         TaskOperationKind
	TaskID       *TaskTarget
	Subject      string
	Description  *string
	ActiveForm   *string
	Category     TaskCategory
	Metadata     map[string]any
	ParentTaskID *TaskTarget
	OwnerID      string
	Status       string
	Active       *bool
	Limit        int
	Offset       int
	AddBlocks    []TaskTarget
	AddBlockedBy []TaskTarget
	AddNote      string
	NoteType     string
	IncludeAudit bool
}

// TaskOperationError is the stable machine-readable error returned per operation.
type TaskOperationError struct {
	Code      string `json:"code"`
	Message   string `json:"message"`
	Retryable bool   `json:"retryable"`
}

// TaskOperationData contains the value produced by a successful operation.
type TaskOperationData struct {
	Task          *TodoItem        `json:"task,omitempty"`
	Tasks         []TodoItem       `json:"tasks,omitempty"`
	Pagination    *TaskPagination  `json:"pagination,omitempty"`
	responseTask  map[string]any   `json:"-"`
	responseTasks []map[string]any `json:"-"`
}

// TaskPagination describes the filtered task set and the returned page.
type TaskPagination struct {
	Total  int  `json:"total"`
	Offset int  `json:"offset"`
	Limit  int  `json:"limit"`
	More   bool `json:"more"`
}

// TaskOperationResult records one operation in a TaskManage batch.
type TaskOperationResult struct {
	Key    string              `json:"key"`
	Op     TaskOperationKind   `json:"op"`
	Status string              `json:"status"`
	Data   *TaskOperationData  `json:"data,omitempty"`
	Error  *TaskOperationError `json:"error,omitempty"`
}

// TaskBatchResult is the structured result returned by TaskManage and legacy adapters.
type TaskBatchResult struct {
	Status  string                `json:"status"`
	Results []TaskOperationResult `json:"results"`
}

type taskOperationFailure struct {
	code      string
	message   string
	retryable bool
}

func (e *taskOperationFailure) Error() string { return e.message }

func taskFailure(code, format string, args ...any) error {
	return &taskOperationFailure{code: code, message: fmt.Sprintf(format, args...)}
}

func operationError(err error) *TaskOperationError {
	if err == nil {
		return nil
	}
	var failure *taskOperationFailure
	if errors.As(err, &failure) {
		return &TaskOperationError{Code: failure.code, Message: failure.message, Retryable: failure.retryable}
	}
	return &TaskOperationError{Code: "operation_failed", Message: err.Error(), Retryable: false}
}

func marshalTaskBatchResult(result TaskBatchResult) (string, error) {
	data, err := json.Marshal(result)
	if err != nil {
		return "", fmt.Errorf("marshal task result: %w", err)
	}
	return string(data), nil
}

// MarshalJSON keeps TaskManage's wire response independent from the complete
// TodoItem used internally and persisted by TodoManager.
func (d TaskOperationData) MarshalJSON() ([]byte, error) {
	if d.responseTask != nil {
		return json.Marshal(struct {
			Task map[string]any `json:"task"`
		}{Task: d.responseTask})
	}
	if d.responseTasks != nil {
		return json.Marshal(struct {
			Tasks      []map[string]any `json:"tasks"`
			Pagination *TaskPagination  `json:"pagination,omitempty"`
		}{Tasks: d.responseTasks, Pagination: d.Pagination})
	}
	type alias TaskOperationData
	return json.Marshal(alias(d))
}

// UnmarshalJSON accepts both historical full task objects and terse response
// objects. Mapping "subject" back to Content preserves the typed helper API
// without changing TodoItem's persistence JSON.
func (d *TaskOperationData) UnmarshalJSON(data []byte) error {
	type wireData struct {
		Task       json.RawMessage   `json:"task"`
		Tasks      []json.RawMessage `json:"tasks"`
		Pagination *TaskPagination   `json:"pagination"`
	}
	var wire wireData
	if err := json.Unmarshal(data, &wire); err != nil {
		return err
	}
	d.Pagination = wire.Pagination
	decodeTask := func(raw json.RawMessage) (TodoItem, error) {
		var task TodoItem
		if err := json.Unmarshal(raw, &task); err != nil {
			return task, err
		}
		if task.Content == "" {
			var terse struct {
				Subject string `json:"subject"`
			}
			if err := json.Unmarshal(raw, &terse); err != nil {
				return task, err
			}
			task.Content = terse.Subject
		}
		return task, nil
	}
	if len(wire.Task) > 0 && string(wire.Task) != "null" {
		task, err := decodeTask(wire.Task)
		if err != nil {
			return err
		}
		d.Task = &task
	}
	d.Tasks = make([]TodoItem, 0, len(wire.Tasks))
	for _, raw := range wire.Tasks {
		task, err := decodeTask(raw)
		if err != nil {
			return err
		}
		d.Tasks = append(d.Tasks, task)
	}
	return nil
}

func parseTaskOperation(raw map[string]any, index int) (TaskOperation, error) {
	op := TaskOperation{}
	key, err := requiredTaskString(raw, "key")
	if err != nil {
		return op, taskFailure("validation_failed", "operation %d: %s", index, err)
	}
	op.Key = key
	kind, err := requiredTaskString(raw, "op")
	if err != nil {
		return op, taskFailure("validation_failed", "operation %q: %s", op.Key, err)
	}
	op.Kind = TaskOperationKind(kind)
	if err := validateTaskOperationFields(op.Kind, raw); err != nil {
		return op, taskFailure("validation_failed", "operation %q: %s", op.Key, err)
	}

	if value, ok := raw["taskId"]; ok {
		target, targetErr := parseTaskTarget(value, "taskId")
		if targetErr != nil {
			return op, targetErr
		}
		op.TaskID = &target
	}
	if value, ok := raw["parentTaskId"]; ok {
		target, targetErr := parseTaskTarget(value, "parentTaskId")
		if targetErr != nil {
			return op, targetErr
		}
		op.ParentTaskID = &target
	}
	if value, ok := raw["addBlocks"]; ok {
		op.AddBlocks, err = parseTaskTargets(value, "addBlocks")
		if err != nil {
			return op, err
		}
	}
	if value, ok := raw["addBlockedBy"]; ok {
		op.AddBlockedBy, err = parseTaskTargets(value, "addBlockedBy")
		if err != nil {
			return op, err
		}
	}

	if op.Subject, err = optionalTaskString(raw, "subject"); err != nil {
		return op, taskFailure("validation_failed", "operation %q: %s", op.Key, err)
	}
	if op.Description, err = optionalTaskStringPointer(raw, "description"); err != nil {
		return op, taskFailure("validation_failed", "operation %q: %s", op.Key, err)
	}
	if op.ActiveForm, err = optionalTaskStringPointer(raw, "activeForm"); err != nil {
		return op, taskFailure("validation_failed", "operation %q: %s", op.Key, err)
	}
	category, err := optionalTaskString(raw, "category")
	if err != nil {
		return op, taskFailure("validation_failed", "operation %q: %s", op.Key, err)
	}
	op.Category = TaskCategory(category)
	if value, ok := raw["metadata"].(map[string]any); ok {
		op.Metadata = cloneJSONMap(value)
	} else if _, present := raw["metadata"]; present {
		return op, taskFailure("validation_failed", "operation %q: metadata must be an object", op.Key)
	}
	if op.OwnerID, err = optionalTaskString(raw, "owner_id"); err != nil {
		return op, taskFailure("validation_failed", "operation %q: %s", op.Key, err)
	}
	if op.Status, err = optionalTaskString(raw, "status"); err != nil {
		return op, taskFailure("validation_failed", "operation %q: %s", op.Key, err)
	}
	if value, ok := raw["active"]; ok {
		active, valid := value.(bool)
		if !valid {
			return op, taskFailure("validation_failed", "operation %q: active must be a boolean", op.Key)
		}
		op.Active = &active
	}
	op.Limit = defaultTaskListLimit
	if value, ok := raw["limit"]; ok {
		op.Limit, err = taskInteger(value, "limit")
		if err != nil {
			return op, taskFailure("validation_failed", "operation %q: %s", op.Key, err)
		}
	}
	if value, ok := raw["offset"]; ok {
		op.Offset, err = taskInteger(value, "offset")
		if err != nil {
			return op, taskFailure("validation_failed", "operation %q: %s", op.Key, err)
		}
	}
	if op.AddNote, err = optionalTaskString(raw, "addNote"); err != nil {
		return op, taskFailure("validation_failed", "operation %q: %s", op.Key, err)
	}
	if op.NoteType, err = optionalTaskString(raw, "noteType"); err != nil {
		return op, taskFailure("validation_failed", "operation %q: %s", op.Key, err)
	}
	if value, ok := raw["include_audit"]; ok {
		includeAudit, valid := value.(bool)
		if !valid {
			return op, taskFailure("validation_failed", "operation %q: include_audit must be a boolean", op.Key)
		}
		op.IncludeAudit = includeAudit
	}

	if err := validateTaskOperation(op); err != nil {
		return op, err
	}
	return op, nil
}

func validateTaskOperationFields(kind TaskOperationKind, raw map[string]any) error {
	common := map[string]bool{"key": true, "op": true}
	allowed := map[TaskOperationKind][]string{
		TaskOperationCreate: {"subject", "description", "activeForm", "category", "metadata", "parentTaskId", "owner_id", "status", "active", "addBlocks", "addBlockedBy"},
		TaskOperationUpdate: {"taskId", "status", "category", "subject", "description", "activeForm", "active", "parentTaskId", "metadata", "addBlocks", "addBlockedBy", "addNote", "noteType"},
		TaskOperationGet:    {"taskId", "include_audit"},
		TaskOperationList:   {"category", "status", "active", "limit", "offset", "subject"},
	}
	fields, ok := allowed[kind]
	if !ok {
		return fmt.Errorf("unsupported op %q", kind)
	}
	for _, field := range fields {
		common[field] = true
	}
	for field := range raw {
		if !common[field] {
			return fmt.Errorf("field %q is not valid for %s", field, kind)
		}
	}
	return nil
}

func requiredTaskString(raw map[string]any, field string) (string, error) {
	value, ok := raw[field]
	if !ok {
		return "", fmt.Errorf("%s is required", field)
	}
	text, ok := value.(string)
	if !ok || strings.TrimSpace(text) == "" {
		return "", fmt.Errorf("%s must be a non-empty string", field)
	}
	return text, nil
}

func optionalTaskString(raw map[string]any, field string) (string, error) {
	value, ok := raw[field]
	if !ok {
		return "", nil
	}
	text, ok := value.(string)
	if !ok {
		return "", fmt.Errorf("%s must be a string", field)
	}
	return text, nil
}

func optionalTaskStringPointer(raw map[string]any, field string) (*string, error) {
	value, ok := raw[field]
	if !ok {
		return nil, nil
	}
	text, ok := value.(string)
	if !ok {
		return nil, fmt.Errorf("%s must be a string", field)
	}
	return &text, nil
}

func taskInteger(value any, field string) (int, error) {
	switch typed := value.(type) {
	case int:
		return typed, nil
	case float64:
		integer := int(typed)
		if typed != float64(integer) {
			return 0, fmt.Errorf("%s must be an integer", field)
		}
		return integer, nil
	default:
		return 0, fmt.Errorf("%s must be an integer", field)
	}
}

func validateTaskOperation(op TaskOperation) error {
	switch op.Kind {
	case TaskOperationCreate:
		if strings.TrimSpace(op.Subject) == "" {
			return taskFailure("validation_failed",
				"operation %q: op:\"create\" requires a non-blank \"subject\". "+
					"A minimal valid create is %s — \"description\" is optional and is never required. "+
					"Retry this operation with \"subject\" set to a short imperative title.",
				op.Key, minimalTaskCreateExample)
		}
	}
	if op.Category != "" && !isValidTaskOperationCategory(op.Category) {
		return taskFailure("validation_failed", "operation %q: invalid category %q", op.Key, op.Category)
	}
	if op.Status != "" && op.Status != string(TodoStatusPending) && op.Status != string(TodoStatusInProgress) && op.Status != string(TodoStatusCompleted) && op.Status != "deleted" {
		return taskFailure("validation_failed", "operation %q: invalid status %q", op.Key, op.Status)
	}
	if op.NoteType != "" {
		valid := map[string]bool{"decision": true, "blocker": true, "learning": true, "milestone": true, "question": true, "observation": true, "other": true}
		if !valid[op.NoteType] {
			return taskFailure("validation_failed", "operation %q: invalid noteType %q", op.Key, op.NoteType)
		}
	}
	if op.Kind == TaskOperationList {
		if op.Limit < 1 || op.Limit > maxTaskListLimit {
			return taskFailure("validation_failed", "operation %q: limit must be between 1 and %d", op.Key, maxTaskListLimit)
		}
		if op.Offset < 0 {
			return taskFailure("validation_failed", "operation %q: offset must be non-negative", op.Key)
		}
	}
	return nil
}

func isValidTaskOperationCategory(category TaskCategory) bool {
	for _, candidate := range ValidCategories() {
		if candidate == category {
			return true
		}
	}
	return false
}

func parseTaskTarget(value any, field string) (TaskTarget, error) {
	switch typed := value.(type) {
	case string:
		if typed == "" && field != "parentTaskId" {
			return TaskTarget{}, taskFailure("validation_failed", "%s must not be empty", field)
		}
		return TaskTarget{ID: typed}, nil
	case map[string]any:
		for key := range typed {
			if key != "ref" && key != "field" {
				return TaskTarget{}, taskFailure("validation_failed", "%s reference contains unknown field %q", field, key)
			}
		}
		refValue, ok := typed["ref"]
		if !ok {
			return TaskTarget{}, taskFailure("validation_failed", "%s reference requires ref", field)
		}
		ref, ok := refValue.(string)
		if !ok || strings.TrimSpace(ref) == "" {
			return TaskTarget{}, taskFailure("validation_failed", "%s reference ref must be a non-empty string", field)
		}
		refField := "taskId"
		if fieldValue, present := typed["field"]; present {
			var valid bool
			refField, valid = fieldValue.(string)
			if !valid {
				return TaskTarget{}, taskFailure("validation_failed", "%s reference field must be a string", field)
			}
		}
		if refField != "taskId" {
			return TaskTarget{}, taskFailure("validation_failed", "%s reference field must be taskId", field)
		}
		return TaskTarget{Reference: &TaskReference{Ref: ref, Field: refField}}, nil
	default:
		return TaskTarget{}, taskFailure("validation_failed", "%s must be a task ID or reference", field)
	}
}

func parseTaskTargets(value any, field string) ([]TaskTarget, error) {
	values, ok := value.([]any)
	if !ok {
		return nil, taskFailure("validation_failed", "%s must be an array", field)
	}
	result := make([]TaskTarget, 0, len(values))
	for i, raw := range values {
		target, err := parseTaskTarget(raw, fmt.Sprintf("%s[%d]", field, i))
		if err != nil {
			return nil, err
		}
		result = append(result, target)
	}
	return result, nil
}

func resolveTaskTarget(target TaskTarget, results map[string]TaskOperationResult, manager *TodoManager) (string, error) {
	if target.Reference == nil {
		return target.ID, nil
	}
	ref := target.Reference.Ref
	result, ok := results[ref]
	if !ok {
		// Not produced earlier in THIS call's batch. Fall back to keys
		// remembered from a previous, separate TaskManage call in this
		// session (see TodoManager.RememberTaskKey / issue #73).
		if manager != nil {
			if id, found := manager.ResolveTaskKey(ref); found {
				return id, nil
			}
		}
		return "", taskFailure("reference_failed",
			"reference %q does not name an earlier operation in this call, and no key %q was remembered from a previous TaskManage call in this session. "+
				"Keys are remembered once the operation that produced them succeeds — double-check the spelling, pass the literal taskId instead (e.g. the id returned by a prior create), or use op:\"list\"/op:\"get\" to look it up.",
			ref, ref)
	}
	if result.Status != "succeeded" || result.Data == nil || result.Data.Task == nil {
		return "", taskFailure("reference_failed", "reference %q did not produce a task", ref)
	}
	return result.Data.Task.ID, nil
}

func resolveTaskOperation(op TaskOperation, results map[string]TaskOperationResult, manager *TodoManager) (TaskOperation, error) {
	resolved := op
	if resolved.TaskID == nil && (op.Kind == TaskOperationUpdate || op.Kind == TaskOperationGet) {
		// Batch-local results take precedence over the durable registry. Atomic
		// batches intentionally defer durable key registration until commit,
		// and a newly created task must also shadow an older registration for
		// the same key.
		if entry, ok := results[op.Key]; ok && entry.Status == "succeeded" && entry.Data != nil && entry.Data.Task != nil {
			resolved.TaskID = &TaskTarget{ID: entry.Data.Task.ID}
		} else if manager != nil {
			if id, found := manager.ResolveTaskKey(op.Key); found {
				resolved.TaskID = &TaskTarget{ID: id}
			}
		}
		if resolved.TaskID == nil {
			return op, taskFailure("reference_failed", "operation key %q was not found in this TaskManage session; pass taskId explicitly or reuse a key from a successful previous TaskManage call", op.Key)
		}
	}
	resolveOne := func(target *TaskTarget) (*TaskTarget, error) {
		if target == nil {
			return nil, nil
		}
		id, err := resolveTaskTarget(*target, results, manager)
		if err != nil {
			return nil, err
		}
		return &TaskTarget{ID: id}, nil
	}
	var err error
	resolved.TaskID, err = resolveOne(resolved.TaskID)
	if err != nil {
		return op, err
	}
	resolved.ParentTaskID, err = resolveOne(op.ParentTaskID)
	if err != nil {
		return op, err
	}
	resolveMany := func(values []TaskTarget) ([]TaskTarget, error) {
		out := make([]TaskTarget, 0, len(values))
		for _, value := range values {
			id, resolveErr := resolveTaskTarget(value, results, manager)
			if resolveErr != nil {
				return nil, resolveErr
			}
			out = append(out, TaskTarget{ID: id})
		}
		return out, nil
	}
	resolved.AddBlocks, err = resolveMany(op.AddBlocks)
	if err != nil {
		return op, err
	}
	resolved.AddBlockedBy, err = resolveMany(op.AddBlockedBy)
	if err != nil {
		return op, err
	}
	return resolved, nil
}

func executeTaskOperation(manager *TodoManager, op TaskOperation) (*TaskOperationData, error) {
	switch op.Kind {
	case TaskOperationCreate:
		return executeTaskCreateOperation(manager, op)
	case TaskOperationUpdate:
		return executeTaskUpdateOperation(manager, op)
	case TaskOperationGet:
		task := manager.ByID(op.TaskID.ID)
		if task == nil {
			return nil, taskFailure("not_found", "task %s not found", op.TaskID.ID)
		}
		return &TaskOperationData{Task: task}, nil
	case TaskOperationList:
		matching := make([]TodoItem, 0)
		subjectFilter := strings.ToLower(strings.TrimSpace(op.Subject))
		for _, task := range manager.Todos() {
			if op.Category != "" && task.Category != op.Category {
				continue
			}
			if op.Status != "" && string(task.Status) != op.Status {
				continue
			}
			if op.Active != nil && task.Active != *op.Active {
				continue
			}
			if subjectFilter != "" && !strings.Contains(strings.ToLower(task.Content), subjectFilter) {
				continue
			}
			matching = append(matching, task)
		}
		total := len(matching)
		start := op.Offset
		if start > total {
			start = total
		}
		end := start + op.Limit
		if end > total {
			end = total
		}
		tasks := make([]TodoItem, 0, end-start)
		for _, task := range matching[start:end] {
			tasks = append(tasks, summarizeTask(task))
		}
		return &TaskOperationData{
			Tasks: tasks,
			Pagination: &TaskPagination{
				Total:  total,
				Offset: op.Offset,
				Limit:  op.Limit,
				More:   end < total,
			},
		}, nil
	default:
		return nil, taskFailure("validation_failed", "unsupported operation %q", op.Kind)
	}
}

func summarizeTask(task TodoItem) TodoItem {
	return TodoItem{
		ID:          task.ID,
		Content:     task.Content,
		ActiveForm:  task.ActiveForm,
		Status:      task.Status,
		Priority:    task.Priority,
		Category:    task.Category,
		DependsOn:   append([]string(nil), task.DependsOn...),
		Blocks:      append([]string(nil), task.Blocks...),
		Active:      task.Active,
		OwnerID:     task.OwnerID,
		Sequence:    task.Sequence,
		ParentID:    task.ParentID,
		CreatedAt:   task.CreatedAt,
		UpdatedAt:   task.UpdatedAt,
		CompletedAt: task.CompletedAt,
	}
}

func executeTaskCreateOperation(manager *TodoManager, op TaskOperation) (*TaskOperationData, error) {
	category := op.Category
	description := ""
	if op.Description != nil {
		description = *op.Description
	}
	activeForm := ""
	if op.ActiveForm != nil {
		activeForm = *op.ActiveForm
	}
	if category == "" {
		category = InferCategory(op.Subject + " " + description)
	}
	parentID := ""
	if op.ParentTaskID != nil {
		parentID = op.ParentTaskID.ID
	}
	item := TodoItem{
		Content:     op.Subject,
		Description: description,
		ActiveForm:  activeForm,
		Category:    category,
		Metadata:    cloneJSONMap(op.Metadata),
		ParentID:    parentID,
		OwnerID:     op.OwnerID,
		Status:      TodoStatusPending,
		Priority:    TodoPriorityMedium,
	}
	// Allow an initial status/active on create so the single most common
	// first-use pattern — "create a task already in progress" — works in one
	// operation. "deleted" is meaningless at creation and is ignored.
	if op.Status != "" && op.Status != "deleted" {
		item.Status = TodoStatus(op.Status)
	}
	if op.Active != nil && *op.Active {
		item.Active = true
	}

	var id string
	err := manager.mutateAtomically(func(candidate *TodoManager) error {
		// Validate all dependency targets before allocating the new task ID.
		// addBlockedBy becomes the new task's DependsOn list, while addBlocks
		// adds the newly allocated ID to each target's DependsOn list.
		for _, target := range op.AddBlockedBy {
			if candidate.ByID(target.ID) == nil {
				return taskFailure("not_found", "dependency task %s not found", target.ID)
			}
			if !containsTaskID(item.DependsOn, target.ID) {
				item.DependsOn = append(item.DependsOn, target.ID)
			}
		}
		for _, target := range op.AddBlocks {
			if candidate.ByID(target.ID) == nil {
				return taskFailure("not_found", "task %s not found", target.ID)
			}
		}

		var addErr error
		id, addErr = candidate.AddTodoAutoID(item)
		if addErr != nil {
			return addErr
		}
		for _, target := range op.AddBlocks {
			if err := candidate.UpdateTodo(target.ID, func(blocked *TodoItem) error {
				if !containsTaskID(blocked.DependsOn, id) {
					blocked.DependsOn = append(blocked.DependsOn, id)
				}
				return nil
			}); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return nil, classifyTaskError(err)
	}
	return &TaskOperationData{Task: manager.ByID(id)}, nil
}

func executeTaskUpdateOperation(manager *TodoManager, op TaskOperation) (*TaskOperationData, error) {
	id := op.TaskID.ID
	if op.Status == "deleted" {
		if err := manager.DeleteTodo(id); err != nil {
			return nil, classifyTaskError(err)
		}
		return &TaskOperationData{}, nil
	}
	err := manager.mutateAtomically(func(candidate *TodoManager) error {
		if candidate.ByID(id) == nil {
			return taskFailure("not_found", "task %s not found", id)
		}
		for _, target := range op.AddBlocks {
			if candidate.ByID(target.ID) == nil {
				return taskFailure("not_found", "task %s not found", target.ID)
			}
			if err := candidate.UpdateTodo(target.ID, func(item *TodoItem) error {
				if !containsTaskID(item.DependsOn, id) {
					item.DependsOn = append(item.DependsOn, id)
				}
				return nil
			}); err != nil {
				return err
			}
		}
		for _, target := range op.AddBlockedBy {
			if candidate.ByID(target.ID) == nil {
				return taskFailure("not_found", "dependency task %s not found", target.ID)
			}
		}
		return candidate.UpdateTodo(id, func(item *TodoItem) error {
			for _, target := range op.AddBlockedBy {
				if !containsTaskID(item.DependsOn, target.ID) {
					item.DependsOn = append(item.DependsOn, target.ID)
				}
			}
			if op.AddNote != "" {
				item.Notes = append(item.Notes, op.AddNote)
				if op.NoteType != "" {
					item.TypedNotes = append(item.TypedNotes, TodoNote{Type: op.NoteType, Content: op.AddNote, CreatedAt: time.Now().UTC()})
				}
			}
			if op.ParentTaskID != nil {
				item.ParentID = op.ParentTaskID.ID
			}
			if op.Metadata != nil {
				if item.Metadata == nil {
					item.Metadata = map[string]any{}
				}
				for key, value := range op.Metadata {
					if value == nil {
						delete(item.Metadata, key)
					} else {
						item.Metadata[key] = cloneJSONValue(value)
					}
				}
			}
			if op.Status != "" {
				item.Status = TodoStatus(op.Status)
				// Explicitly (re)setting in_progress is the documented focus
				// workflow, including when the task was already in progress.
				if item.Status == TodoStatusInProgress {
					item.Active = true
				}
			}
			if op.Subject != "" {
				item.Content = op.Subject
			}
			if op.Description != nil {
				item.Description = *op.Description
			}
			if op.ActiveForm != nil {
				item.ActiveForm = *op.ActiveForm
			}
			if op.Category != "" {
				item.Category = op.Category
			}
			if op.Active != nil {
				item.Active = *op.Active
			}
			return nil
		})
	})
	if err != nil {
		return nil, classifyTaskError(err)
	}
	return &TaskOperationData{Task: manager.ByID(id)}, nil
}

func classifyTaskError(err error) error {
	var failure *taskOperationFailure
	if errors.As(err, &failure) {
		return err
	}
	message := err.Error()
	if strings.Contains(message, "not found") {
		return taskFailure("not_found", "%s", message)
	}
	if strings.Contains(message, "persist") || strings.Contains(message, "sync") || strings.Contains(message, "write") || strings.Contains(message, "rename") {
		return taskFailure("persistence_failed", "%s", message)
	}
	return taskFailure("validation_failed", "%s", message)
}

func containsTaskID(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

// prepareTaskManageResponse swaps complete in-memory task records for
// operation-specific response DTOs. TodoManager and journal persistence retain
// the complete TodoItem values.
func prepareTaskManageResponse(result *TaskBatchResult, operations []TaskOperation) {
	for index := range result.Results {
		if index >= len(operations) || result.Results[index].Status != "succeeded" || result.Results[index].Data == nil {
			continue
		}
		op := operations[index]
		data := result.Results[index].Data
		switch op.Kind {
		case TaskOperationCreate:
			data.responseTask = terseTaskAck(data.Task)
		case TaskOperationUpdate:
			data.responseTask = terseUpdateAck(op, data.Task)
		case TaskOperationGet:
			if !op.IncludeAudit && data.Task != nil {
				data.responseTask = taskWithoutAudit(data.Task)
			}
		case TaskOperationList:
			data.responseTasks = make([]map[string]any, 0, len(data.Tasks))
			for taskIndex := range data.Tasks {
				data.responseTasks = append(data.responseTasks, compactTaskSummary(&data.Tasks[taskIndex]))
			}
		}
	}
}

func terseTaskAck(task *TodoItem) map[string]any {
	if task == nil {
		return map[string]any{"id": "", "subject": "", "status": "", "active": false, "parent_id": ""}
	}
	return map[string]any{
		"id": task.ID, "subject": task.Content, "status": task.Status,
		"active": task.Active, "parent_id": task.ParentID,
	}
}

func compactTaskSummary(task *TodoItem) map[string]any {
	summary := terseTaskAck(task)
	if task == nil {
		return summary
	}
	if task.ActiveForm != "" {
		summary["active_form"] = task.ActiveForm
	}
	if task.Priority != "" {
		summary["priority"] = task.Priority
	}
	if task.Category != "" {
		summary["category"] = task.Category
	}
	if len(task.DependsOn) > 0 {
		summary["depends_on"] = append([]string(nil), task.DependsOn...)
	}
	if len(task.Blocks) > 0 {
		summary["blocks"] = append([]string(nil), task.Blocks...)
	}
	if task.OwnerID != "" {
		summary["owner_id"] = task.OwnerID
	}
	return summary
}

func terseUpdateAck(op TaskOperation, task *TodoItem) map[string]any {
	ack := terseTaskAck(task)
	if task == nil {
		if op.TaskID != nil {
			ack["id"] = op.TaskID.ID
		}
		if op.Status != "" {
			ack["status"] = op.Status
		}
		return ack
	}
	if op.Description != nil {
		ack["description"] = task.Description
	}
	if op.ActiveForm != nil {
		ack["active_form"] = task.ActiveForm
	}
	if op.Category != "" {
		ack["category"] = task.Category
	}
	if op.Metadata != nil {
		ack["metadata"] = task.Metadata
	}
	if len(op.AddBlocks) > 0 {
		ack["blocks"] = append([]string(nil), task.Blocks...)
	}
	if len(op.AddBlockedBy) > 0 {
		ack["depends_on"] = append([]string(nil), task.DependsOn...)
	}
	if op.AddNote != "" {
		ack["note_added"] = true
	}
	return ack
}

func taskWithoutAudit(task *TodoItem) map[string]any {
	data, _ := json.Marshal(task)
	var response map[string]any
	_ = json.Unmarshal(data, &response)
	delete(response, "audit_events")
	delete(response, "typed_notes")
	return response
}
