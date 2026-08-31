package ii

import (
	"context"
	"strings"
	"testing"
)

func TestTaskCreateStoresAllAdvertisedFields(t *testing.T) {
	m := NewTodoManager()
	create := NewTaskCreateToolWithManager(m)
	res, err := create.Execute(context.Background(), map[string]any{
		"subject":     "Ship",
		"description": "acceptance criteria",
		"activeForm":  "Shipping",
		"metadata":    map[string]any{"pr": "9"},
		"category":    "acting",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.Output, "created successfully") {
		t.Fatalf("unexpected result: %s", res.Output)
	}
	task := m.ByID("1")
	if task.Description != "acceptance criteria" || task.ActiveForm != "Shipping" || task.Metadata["pr"] != "9" {
		t.Fatalf("advertised fields not stored: %+v", task)
	}
}

func TestTaskCreateSubtaskAndCompletionGuard(t *testing.T) {
	m := NewTodoManager()
	create := NewTaskCreateToolWithManager(m)
	update := NewTaskUpdateToolWithManager(m)

	if _, err := create.Execute(context.Background(), map[string]any{"subject": "Parent", "description": "d"}); err != nil {
		t.Fatal(err)
	}
	childRes, err := create.Execute(context.Background(), map[string]any{"subject": "Child", "description": "d", "parentTaskId": "1"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(childRes.Output, "under parent #1") {
		t.Fatalf("subtask not linked: %s", childRes.Output)
	}

	// Start and try to complete the parent while the child is open.
	if _, err := update.Execute(context.Background(), map[string]any{"taskId": "1", "status": "in_progress"}); err != nil {
		t.Fatal(err)
	}
	res, err := update.Execute(context.Background(), map[string]any{"taskId": "1", "status": "completed"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.Output, "cannot complete") {
		t.Fatalf("expected completion rejection, got: %s", res.Output)
	}
	if m.ByID("1").Status != TodoStatusInProgress {
		t.Fatal("parent was completed despite open child")
	}

	// Complete the child, then the parent succeeds.
	if _, err := update.Execute(context.Background(), map[string]any{"taskId": "2", "status": "in_progress"}); err != nil {
		t.Fatal(err)
	}
	if _, err := update.Execute(context.Background(), map[string]any{"taskId": "2", "status": "completed"}); err != nil {
		t.Fatal(err)
	}
	if _, err := update.Execute(context.Background(), map[string]any{"taskId": "1", "status": "completed"}); err != nil {
		t.Fatal(err)
	}
	if m.ByID("1").Status != TodoStatusCompleted {
		t.Fatal("parent should complete once children are done")
	}
}

func TestTaskUpdateAppliesRichFieldsAndTypedNotes(t *testing.T) {
	m := NewTodoManager()
	create := NewTaskCreateToolWithManager(m)
	update := NewTaskUpdateToolWithManager(m)
	if _, err := create.Execute(context.Background(), map[string]any{"subject": "Task", "description": "d"}); err != nil {
		t.Fatal(err)
	}
	if _, err := update.Execute(context.Background(), map[string]any{
		"taskId":      "1",
		"description": "new details",
		"activeForm":  "Doing it",
		"metadata":    map[string]any{"k": "v"},
		"addNote":     "chose approach",
		"noteType":    "decision",
	}); err != nil {
		t.Fatal(err)
	}
	task := m.ByID("1")
	if task.Description != "new details" || task.ActiveForm != "Doing it" || task.Metadata["k"] != "v" {
		t.Fatalf("update did not apply fields: %+v", task)
	}
	if len(task.Notes) != 1 || len(task.TypedNotes) != 1 || task.TypedNotes[0].Type != "decision" {
		t.Fatalf("notes not recorded: %+v", task)
	}
}
