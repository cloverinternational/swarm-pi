package ii

import (
	"encoding/json"
	"strings"
	"testing"
)

// The published schema must let a caller compose a first call that the runtime
// validator actually accepts. Issues #179, #187, #192, #193, #201, #211, #216,
// #217, #218 and #226 all reduce to the same failure: the schema's "required"
// array does not describe the per-op requirements, so an agent guesses wrong.

func taskManageOperationSchema(t *testing.T) map[string]any {
	t.Helper()
	params, ok := NewTaskManageTool().Parameters().(map[string]any)
	if !ok {
		t.Fatalf("Parameters() is not a map[string]any: %T", NewTaskManageTool().Parameters())
	}
	properties, ok := params["properties"].(map[string]any)
	if !ok {
		t.Fatal("schema has no properties map")
	}
	operations, ok := properties["operations"].(map[string]any)
	if !ok {
		t.Fatal("schema has no operations property")
	}
	items, ok := operations["items"].(map[string]any)
	if !ok {
		t.Fatal("operations has no items schema")
	}
	return items
}

// TestTaskManageSchemaPublishesPerOperationRequirements asserts the conditional
// requirements are discoverable from the tool definition a model receives.
func TestTaskManageSchemaPublishesPerOperationRequirements(t *testing.T) {
	item := taskManageOperationSchema(t)
	description := NewTaskManageTool().Description()
	for _, want := range []string{"create needs a non-blank subject", "update/get need taskId or a registered key", "list needs only key/op"} {
		if !strings.Contains(description, want) {
			t.Fatalf("tool description missing %q: %s", want, description)
		}
	}

	// The unconditional "required" array must stay {key, op}: adding "subject"
	// there would forbid update/get/list, changing accepted semantics.
	required, ok := item["required"].([]string)
	if !ok {
		t.Fatalf("required is not []string: %T", item["required"])
	}
	if len(required) != 2 || required[0] != "key" || required[1] != "op" {
		t.Fatalf("required = %v, want [key op]", required)
	}
}

// TestTaskManageSchemaSurvivesJSONRoundTrip guards the schema against consumers
// that serialize it verbatim to a provider (anthropic/openai/gemini translate).
func TestTaskManageSchemaSurvivesJSONRoundTrip(t *testing.T) {
	encoded, err := json.Marshal(NewTaskManageTool().Parameters())
	if err != nil {
		t.Fatalf("schema is not JSON-serializable: %v", err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(encoded), `"create"`) {
		t.Fatalf("serialized schema does not carry the create operation enum: %s", encoded)
	}
}

func TestTaskManageSchemaMatchesPlanModeRuntimePolicy(t *testing.T) {
	tool := NewTaskManageTool()
	encoded, err := json.Marshal(tool.Parameters())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(tool.Description(), "mixed batches are allowed in plan mode") {
		t.Fatalf("description does not advertise plan-mode mutation policy: %s", tool.Description())
	}
	for name, text := range map[string]string{"description": tool.Description(), "schema": string(encoded)} {
		if strings.Contains(text, "only all-read-only") || strings.Contains(text, "limited to get/list") {
			t.Fatalf("%s still advertises obsolete read-only policy: %s", name, text)
		}
	}
}

// TestTaskManageCreateWithoutSubjectDescribesMinimalCreate is issue #226's
// acceptance test: the error must name a minimal valid create.
func TestTaskManageCreateWithoutSubjectDescribesMinimalCreate(t *testing.T) {
	manager := NewTodoManager()
	batch := executeTaskManage(t, manager, map[string]any{"operations": []any{
		map[string]any{"key": "build", "op": "create"},
	}})

	if batch.Status == "succeeded" {
		t.Fatalf("create without subject unexpectedly succeeded: %+v", batch)
	}
	if len(batch.Results) != 1 || batch.Results[0].Status != "failed" {
		t.Fatalf("unexpected results: %+v", batch.Results)
	}
	message := batch.Results[0].Error.Message
	for _, want := range []string{`op:"create"`, "subject", `"op":"create"`, "description"} {
		if !strings.Contains(message, want) {
			t.Fatalf("error message missing %q: %s", want, message)
		}
	}
	if manager.Count() != 0 {
		t.Fatalf("rejected create still mutated state: count = %d", manager.Count())
	}
}

// TestTaskManageCreateWithKeyOpSubjectSucceeds is issue #226's near-miss test:
// the minimal create the schema now advertises must keep working, with no
// "description" supplied.
func TestTaskManageCreateWithKeyOpSubjectSucceeds(t *testing.T) {
	manager := NewTodoManager()
	batch := executeTaskManage(t, manager, map[string]any{"operations": []any{
		map[string]any{"key": "build", "op": "create", "subject": "Build the thing"},
	}})

	if batch.Status != "succeeded" {
		t.Fatalf("minimal create failed: %+v", batch)
	}
	if len(batch.Results) != 1 || batch.Results[0].Status != "succeeded" {
		t.Fatalf("unexpected results: %+v", batch.Results)
	}
	task := manager.ByID("1")
	if task == nil || task.Content != "Build the thing" {
		t.Fatalf("task not created as advertised: %+v", task)
	}
	if task.Description != "" {
		t.Fatalf("description was invented for a minimal create: %q", task.Description)
	}
}

// TestTaskManageCreateWithBlankSubjectIsRejected pins the trim rule the schema
// prose claims ("non-blank").
func TestTaskManageCreateWithBlankSubjectIsRejected(t *testing.T) {
	manager := NewTodoManager()
	batch := executeTaskManage(t, manager, map[string]any{"operations": []any{
		map[string]any{"key": "build", "op": "create", "subject": "   "},
	}})
	if batch.Status == "succeeded" {
		t.Fatalf("blank subject accepted: %+v", batch)
	}
	if !strings.Contains(batch.Results[0].Error.Message, "non-blank") {
		t.Fatalf("error does not explain the non-blank rule: %s", batch.Results[0].Error.Message)
	}
}

// TestTaskManageUpdateByKeyAloneAcrossCalls covers issues #179/#217/#218: a
// later, SEPARATE TaskManage call may target a task by "key" with no taskId.
func TestTaskManageUpdateByKeyAloneAcrossCalls(t *testing.T) {
	manager := NewTodoManager()

	first := executeTaskManage(t, manager, map[string]any{"operations": []any{
		map[string]any{"key": "build", "op": "create", "subject": "Build the thing"},
	}})
	if first.Status != "succeeded" {
		t.Fatalf("setup create failed: %+v", first)
	}

	second := executeTaskManage(t, manager, map[string]any{"operations": []any{
		map[string]any{"key": "build", "op": "update", "status": "in_progress"},
	}})
	if second.Status != "succeeded" {
		t.Fatalf("update by key alone failed: %+v", second)
	}
	if task := manager.ByID("1"); task == nil || task.Status != TodoStatusInProgress {
		t.Fatalf("update by key did not apply: %+v", task)
	}

	third := executeTaskManage(t, manager, map[string]any{"operations": []any{
		map[string]any{"key": "build", "op": "get"},
	}})
	if third.Status != "succeeded" {
		t.Fatalf("get by key alone failed: %+v", third)
	}
}
