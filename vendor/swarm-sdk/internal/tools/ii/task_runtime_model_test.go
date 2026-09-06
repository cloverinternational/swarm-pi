package ii

import (
	"testing"
	"time"
)

func runtimeTask(id string, status TodoStatus) TodoItem {
	return TodoItem{ID: id, Content: "Task " + id, Status: status, Priority: TodoPriorityMedium}
}

func TestTodoItemCloneDeepCopiesRichFields(t *testing.T) {
	now := time.Now().UTC()
	item := runtimeTask("1", TodoStatusInProgress)
	item.Active = true
	item.Description = "details"
	item.ActiveForm = "Testing task"
	item.Metadata = map[string]any{"nested": map[string]any{"value": "original"}}
	item.TypedNotes = []TodoNote{{Type: "decision", Content: "keep", CreatedAt: now, Metadata: map[string]any{"k": "v"}}}
	item.AuditEvents = []TodoAuditEvent{{Type: "tool", Timestamp: now, Metadata: map[string]any{"tool": "Read"}}}

	clone := item.Clone()
	clone.Metadata["nested"].(map[string]any)["value"] = "changed"
	clone.TypedNotes[0].Metadata["k"] = "changed"
	clone.AuditEvents[0].Metadata["tool"] = "changed"

	if got := item.Metadata["nested"].(map[string]any)["value"]; got != "original" {
		t.Fatalf("metadata was shallow copied: %v", got)
	}
	if got := item.TypedNotes[0].Metadata["k"]; got != "v" {
		t.Fatalf("typed note metadata was shallow copied: %v", got)
	}
	if got := item.AuditEvents[0].Metadata["tool"]; got != "Read" {
		t.Fatalf("audit metadata was shallow copied: %v", got)
	}
}

func TestTodoManagerFocusTransfersWithoutCompletingParent(t *testing.T) {
	m := NewTodoManager()
	parent := runtimeTask("1", TodoStatusInProgress)
	parent.Active = true
	if err := m.AddTodo(parent); err != nil {
		t.Fatal(err)
	}
	child := runtimeTask("2", TodoStatusPending)
	child.ParentID = "1"
	if err := m.AddTodo(child); err != nil {
		t.Fatal(err)
	}
	if err := m.UpdateTodo("2", func(task *TodoItem) error {
		task.Status = TodoStatusInProgress
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	if got := m.ByID("1"); got.Status != TodoStatusInProgress || got.Active {
		t.Fatalf("parent should remain in progress without focus: %+v", got)
	}
	if got := m.ByID("2"); !got.Active {
		t.Fatalf("child should own focus: %+v", got)
	}
	if err := m.FocusTodo("1"); err != nil {
		t.Fatal(err)
	}
	if !m.ByID("1").Active || m.ByID("2").Active {
		t.Fatal("focus did not transfer back to parent")
	}
}

func TestTodoManagerParentGuardsAndOwnerInheritance(t *testing.T) {
	m := NewTodoManager()
	parent := runtimeTask("1", TodoStatusInProgress)
	parent.OwnerID = "main"
	if err := m.AddTodo(parent); err != nil {
		t.Fatal(err)
	}
	child := runtimeTask("", TodoStatusPending)
	child.ParentID = "1"
	childID, err := m.AddTodoAutoID(child)
	if err != nil {
		t.Fatal(err)
	}
	if got := m.ByID(childID).OwnerID; got != "main" {
		t.Fatalf("child owner = %q, want inherited main", got)
	}
	if err := m.UpdateTodo("1", func(task *TodoItem) error {
		task.Status = TodoStatusCompleted
		return nil
	}); err == nil {
		t.Fatal("completed parent with unfinished child")
	}
	if err := m.DeleteTodo("1"); err == nil {
		t.Fatal("deleted parent with existing child")
	}
}

func TestSetTodosNormalizesLegacyFocus(t *testing.T) {
	m := NewTodoManager()
	tasks := []TodoItem{runtimeTask("1", TodoStatusInProgress), runtimeTask("2", TodoStatusInProgress)}
	if err := m.SetTodos(tasks); err != nil {
		t.Fatal(err)
	}
	if !m.ByID("1").Active || m.ByID("2").Active {
		t.Fatal("legacy focus was not normalized to first in-progress task")
	}
}
