package ii

import (
	"encoding/json"
	"testing"
)

func TestSuccessfulTaskManageEventOperationsCorrelatesDuplicateKeysByOrder(t *testing.T) {
	operations := []any{
		createOperation("same", "Created"),
		map[string]any{"key": "same", "op": "update", "status": "in_progress"},
		map[string]any{"key": "same", "op": "get"},
	}
	batch := TaskBatchResult{Status: "partial", Results: []TaskOperationResult{
		{Key: "same", Op: TaskOperationCreate, Status: "succeeded", Data: &TaskOperationData{Task: &TodoItem{ID: "1"}}},
		{Key: "same", Op: TaskOperationUpdate, Status: "failed", Error: &TaskOperationError{Code: "test"}},
		{Key: "same", Op: TaskOperationGet, Status: "succeeded", Data: &TaskOperationData{Task: &TodoItem{ID: "1"}}},
	}}
	output, err := json.Marshal(batch)
	if err != nil {
		t.Fatal(err)
	}
	got, ids, ok := SuccessfulTaskManageEventOperations(map[string]any{
		"tool_name":   TaskManageName,
		"params":      map[string]any{"operations": operations},
		"tool_output": string(output),
	})
	if !ok {
		t.Fatal("event did not parse")
	}
	if len(got) != 2 || got[0].Kind != TaskOperationCreate || got[1].Kind != TaskOperationGet {
		t.Fatalf("mis-correlated successful operations: %+v", got)
	}
	if ids["same"] != "1" {
		t.Fatalf("resolved IDs = %v", ids)
	}
}

func TestSuccessfulTaskManageEventOperationsRejectsMismatchedOrderedResult(t *testing.T) {
	batch := TaskBatchResult{Status: "succeeded", Results: []TaskOperationResult{
		{Key: "same", Op: TaskOperationUpdate, Status: "succeeded", Data: &TaskOperationData{Task: &TodoItem{ID: "1"}}},
	}}
	output, _ := json.Marshal(batch)
	got, _, ok := SuccessfulTaskManageEventOperations(map[string]any{
		"tool_name": TaskManageName,
		"params": map[string]any{"operations": []any{
			createOperation("same", "Created"),
		}},
		"tool_output": string(output),
	})
	if !ok || len(got) != 0 {
		t.Fatalf("mismatched result correlated: ok=%v got=%+v", ok, got)
	}
}
