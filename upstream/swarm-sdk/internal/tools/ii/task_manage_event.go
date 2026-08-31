package ii

import "encoding/json"

// This file gives hook code (in internal/hooks/builtin and
// internal/skills/autogenskills) a single, shared way to parse a
// TaskManage tool-call event without each package keeping its own copy of
// the parsing logic. It operates on the plain map[string]any event data
// shape (tool_name/name/params/tool_output) rather than any hooks.Event
// type, specifically so this package does not need to depend on
// internal/hooks — internal/hooks (via internal/tools) already depends on
// this package, so the reverse dependency would be an import cycle.

// IsTaskManageEventName reports whether name is the TaskManage
// control-plane tool (as opposed to one of its legacy single-purpose
// predecessors like TaskCreate/TaskUpdate).
func IsTaskManageEventName(name string) bool {
	return normalizeToolEventName(name) == "taskmanage"
}

func normalizeToolEventName(name string) string {
	out := make([]rune, 0, len(name))
	for _, r := range name {
		if r == '_' {
			continue
		}
		if r >= 'A' && r <= 'Z' {
			r += 'a' - 'A'
		}
		out = append(out, r)
	}
	return string(out)
}

// TaskManageEventOperations parses the operations a TaskManage tool call
// was invoked with, from a hook event's Data map (event.Data), returning
// false if the event isn't a TaskManage call or the params don't parse.
// eventData is expected to look like {"tool_name": "TaskManage", "params":
// {"operations": [...]}}, mirroring the shape internal/hooks.Event.Data
// uses for EventToolBeforeExecute/EventToolAfterExecute.
func TaskManageEventOperations(eventData map[string]any) ([]TaskOperation, bool) {
	name, _ := eventData["tool_name"].(string)
	if name == "" {
		name, _ = eventData["name"].(string)
	}
	if !IsTaskManageEventName(name) {
		return nil, false
	}
	params, ok := eventData["params"].(map[string]any)
	if !ok {
		return nil, false
	}
	operations, err := InspectTaskManageOperations(params)
	return operations, err == nil
}

// SuccessfulTaskManageEventOperations parses a TaskManage after-execute
// event (eventData containing "tool_output", the batch result JSON) and
// returns only the operations whose batch result actually succeeded, plus a
// key->resolved-task-ID map for operations that produced a task. Returns
// false if the event isn't a valid, parseable TaskManage after-execute
// event (e.g. missing tool_output).
func SuccessfulTaskManageEventOperations(eventData map[string]any) ([]TaskOperation, map[string]string, bool) {
	operations, ok := TaskManageEventOperations(eventData)
	if !ok {
		return nil, nil, false
	}
	output, _ := eventData["tool_output"].(string)
	var batch TaskBatchResult
	if output == "" || json.Unmarshal([]byte(output), &batch) != nil {
		return nil, nil, false
	}
	succeeded := make([]TaskOperation, 0, len(batch.Results))
	resolvedIDs := make(map[string]string, len(batch.Results))
	for index, result := range batch.Results {
		if index >= len(operations) {
			break
		}
		operation := operations[index]
		// Results are emitted in request order. Check identity as well so a
		// malformed output cannot be correlated to the wrong request.
		if result.Key != operation.Key || result.Op != operation.Kind || result.Status != "succeeded" {
			continue
		}
		succeeded = append(succeeded, operation)
		if result.Data != nil && result.Data.Task != nil {
			resolvedIDs[result.Key] = result.Data.Task.ID
		}
	}
	return succeeded, resolvedIDs, true
}

// TaskManageEventHasKind reports whether a TaskManage event's requested
// operations include any of the given kinds (regardless of whether they
// ultimately succeeded — this inspects the request, not the result).
func TaskManageEventHasKind(eventData map[string]any, kinds ...TaskOperationKind) bool {
	operations, ok := TaskManageEventOperations(eventData)
	if !ok {
		return false
	}
	for _, operation := range operations {
		for _, kind := range kinds {
			if operation.Kind == kind {
				return true
			}
		}
	}
	return false
}

// TaskManageEventIsReadOnly reports whether every operation in a TaskManage
// event's request is a read-only kind (get/list).
func TaskManageEventIsReadOnly(eventData map[string]any) bool {
	operations, ok := TaskManageEventOperations(eventData)
	if !ok || len(operations) == 0 {
		return false
	}
	for _, operation := range operations {
		if operation.Kind != TaskOperationGet && operation.Kind != TaskOperationList {
			return false
		}
	}
	return true
}
