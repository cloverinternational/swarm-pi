package ii

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"

	sdktools "github.com/Swarm-Code/mono/swarm-sdk/internal/tools"
)

func TestDefaultProductivitySurfaceOnlyAdvertisesTaskManage(t *testing.T) {
	tools := GetProductivityTools()
	if len(tools) != 1 {
		t.Fatalf("default productivity tool count = %d, want 1", len(tools))
	}
	if tools[0].Name() != TaskManageName {
		t.Fatalf("default productivity tool = %q, want %q", tools[0].Name(), TaskManageName)
	}
}

func TestLegacyCreateWrapperUsesCanonicalValidationAndExecution(t *testing.T) {
	manager := NewTodoManager()
	tool := NewTaskCreateToolWithManager(manager)
	invalid := map[string]any{
		"subject":     "Build API",
		"description": "Implement it",
		"unexpected":  true,
	}
	if err := tool.Validate(invalid); err == nil {
		t.Fatal("legacy wrapper accepted a field rejected by canonical validation")
	}
	result, err := tool.Execute(context.Background(), invalid)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(result.Output, "ERROR:") {
		t.Fatalf("invalid wrapper output = %q", result.Output)
	}
	if len(manager.Todos()) != 0 {
		t.Fatal("invalid compatibility call mutated task state")
	}

	valid := map[string]any{"subject": "Build API", "description": "Implement it"}
	result, err = tool.Execute(context.Background(), valid)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result.Output, "Task #1 created successfully") {
		t.Fatalf("legacy success output changed: %q", result.Output)
	}
	if task := manager.ByID("1"); task == nil || task.Content != "Build API" {
		t.Fatalf("canonical create did not publish expected task: %+v", task)
	}
}

func TestLegacyCreateSchemaAndRuntimeAllowMissingDescription(t *testing.T) {
	tool := NewTaskCreateToolWithManager(NewTodoManager())
	schema := tool.Parameters().(map[string]any)
	required := schema["required"].([]string)
	if !slices.Equal(required, []string{"subject"}) {
		t.Fatalf("required = %v, want [subject]", required)
	}
	if err := tool.Validate(map[string]any{"subject": "Minimal"}); err != nil {
		t.Fatalf("minimal legacy create rejected: %v", err)
	}
	result, err := tool.Execute(context.Background(), map[string]any{"subject": "Minimal"})
	if err != nil || !strings.Contains(result.Output, "created successfully") {
		t.Fatalf("minimal legacy create failed: result=%+v err=%v", result, err)
	}
}

func TestLegacyTaskListPreservesHistoricalAllItemsContract(t *testing.T) {
	manager := NewTodoManager()
	for i := 0; i < defaultTaskListLimit+1; i++ {
		if _, err := manager.AddTodoAutoID(TodoItem{Content: "Item", Status: TodoStatusPending, Priority: TodoPriorityMedium}); err != nil {
			t.Fatal(err)
		}
	}
	result, err := NewTaskListToolWithManager(manager).Execute(context.Background(), map[string]any{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result.Output, "Tasks: 51 total") || !strings.Contains(result.Output, "#51.") {
		t.Fatalf("legacy list silently paginated: %s", result.Output)
	}
}

func TestLegacyUpdateWrapperRollsBackWholeRichOperation(t *testing.T) {
	manager := NewTodoManager()
	if err := manager.AddTodo(TodoItem{ID: "1", Content: "Original", Status: TodoStatusPending, Priority: TodoPriorityMedium}); err != nil {
		t.Fatal(err)
	}
	result, err := NewTaskUpdateToolWithManager(manager).Execute(context.Background(), map[string]any{
		"taskId":       "1",
		"subject":      "Changed",
		"addBlockedBy": []any{"missing"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(result.Output, "ERROR:") {
		t.Fatalf("invalid update output = %q", result.Output)
	}
	if task := manager.ByID("1"); task == nil || task.Content != "Original" || len(task.DependsOn) != 0 {
		t.Fatalf("legacy update left partial mutation: %+v", task)
	}
}

func TestRegisterProductivityToolsRegistersOnlyCanonicalName(t *testing.T) {
	ResetGlobalManager()
	t.Cleanup(ResetGlobalManager)

	registry := sdktools.NewRegistry()
	if err := RegisterProductivityTools(registry); err != nil {
		t.Fatal(err)
	}
	if got := registry.List(); !slices.Equal(got, []string{TaskManageName}) {
		t.Fatalf("advertised tools = %v, want only %s", got, TaskManageName)
	}
	if got := registry.ListAll(); !slices.Equal(got, []string{TaskManageName}) {
		t.Fatalf("registered tools = %v, want only %s", got, TaskManageName)
	}
	for _, name := range []string{
		TaskCreateName, TaskUpdateName, TaskGetName, TaskListName,
		TodoReadName, TodoWriteName,
	} {
		if registry.IsRegistered(name) {
			t.Fatalf("legacy productivity tool %s remains registered", name)
		}
		if _, err := registry.Execute(context.Background(), name, nil); err == nil {
			t.Fatalf("legacy productivity tool %s remains executable", name)
		}
	}
}

func TestIIToolRegistryOnlyResolvesCanonicalTaskTool(t *testing.T) {
	registry, err := NewToolRegistry(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(registry.List(), TaskManageName) {
		t.Fatalf("unexpected advertised task surface: %v", registry.List())
	}
	for _, name := range []string{
		TaskCreateName, TaskUpdateName, TaskGetName, TaskListName,
		TodoReadName, TodoWriteName,
	} {
		if _, ok := registry.Get(name); ok {
			t.Fatalf("legacy productivity tool %s remains reachable", name)
		}
	}
}

func TestTaskManageConsolidationReducesTaskRecordSchemaTokens(t *testing.T) {
	type definition struct {
		Name        string `json:"name"`
		Description string `json:"description"`
		Parameters  any    `json:"input_schema"`
	}
	serializedBytes := func(toolList []sdktools.Tool) int {
		t.Helper()
		definitions := make([]definition, 0, len(toolList))
		for _, tool := range toolList {
			definitions = append(definitions, definition{
				Name: tool.Name(), Description: tool.Description(), Parameters: tool.Parameters(),
			})
		}
		encoded, err := json.Marshal(definitions)
		if err != nil {
			t.Fatal(err)
		}
		return len(encoded)
	}

	legacy := append(GetLegacyTaskTools(), NewTodoReadTool(), NewTodoWriteTool())
	canonical := []sdktools.Tool{NewTaskManageTool()}
	legacyBytes := serializedBytes(legacy)
	canonicalBytes := serializedBytes(canonical)
	estimateTokens := func(bytes int) int { return (bytes + 3) / 4 }

	t.Logf(
		"task-record schemas: legacy=%d bytes (~%d tokens), TaskManage=%d bytes (~%d tokens), reduction=%d bytes (~%d tokens)",
		legacyBytes, estimateTokens(legacyBytes),
		canonicalBytes, estimateTokens(canonicalBytes),
		legacyBytes-canonicalBytes, estimateTokens(legacyBytes)-estimateTokens(canonicalBytes),
	)
	if canonicalBytes >= legacyBytes {
		t.Fatalf("TaskManage schema did not reduce task-record context: canonical=%d legacy=%d", canonicalBytes, legacyBytes)
	}
}
