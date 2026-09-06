package builtin

import (
	"testing"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/hooks"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/tools/ii"
)

func TestSuccessfulTaskManageOperationsFiltersFailuresAndResolvesIDs(t *testing.T) {
	event := hooks.Event{Data: map[string]any{
		"tool_name": "TaskManage",
		"params": map[string]any{"operations": []any{
			map[string]any{"key": "created", "op": "create", "subject": "Build", "description": "Build it"},
			map[string]any{"key": "failed", "op": "update", "taskId": "99", "status": "completed"},
		}},
		"tool_output": `{"status":"partial","results":[{"key":"created","op":"create","status":"succeeded","data":{"task":{"id":"17","content":"Build","status":"pending","priority":"medium"}}},{"key":"failed","op":"update","status":"failed","error":{"code":"not_found","message":"task not found","retryable":false}}]}`,
	}}

	operations, resolvedIDs, ok := successfulTaskManageOperations(event)
	if !ok {
		t.Fatal("expected valid TaskManage result")
	}
	if len(operations) != 1 || operations[0].Kind != ii.TaskOperationCreate {
		t.Fatalf("unexpected successful operations: %+v", operations)
	}
	if resolvedIDs["created"] != "17" {
		t.Fatalf("resolved task ID = %q, want 17", resolvedIDs["created"])
	}
}

func TestSuccessfulTaskManageOperationsRejectsMissingResult(t *testing.T) {
	event := hooks.Event{Data: map[string]any{
		"tool_name": "TaskManage",
		"params": map[string]any{"operations": []any{
			map[string]any{"key": "created", "op": "create", "subject": "Build", "description": "Build it"},
		}},
	}}
	if _, _, ok := successfulTaskManageOperations(event); ok {
		t.Fatal("missing tool output must not be treated as a successful mutation")
	}
}
