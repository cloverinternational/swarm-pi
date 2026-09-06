package ii

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

type rejectingTodoSyncer struct{ err error }

func (s rejectingTodoSyncer) Sync([]TodoItem) error { return s.err }

func transactionTodo(id string, status TodoStatus) TodoItem {
	return TodoItem{ID: id, Content: "task " + id, Description: "description", Status: status, Priority: TodoPriorityMedium}
}

func assertTodosEqual(t *testing.T, got, want []TodoItem) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("todos changed after rejected mutation\ngot:  %#v\nwant: %#v", got, want)
	}
}

func TestTodoManagerSyncFailuresRollbackMutations(t *testing.T) {
	syncErr := errors.New("persist failed")
	tests := []struct {
		name   string
		setup  []TodoItem
		mutate func(*TodoManager) error
	}{
		{name: "add", setup: []TodoItem{transactionTodo("1", TodoStatusPending)}, mutate: func(m *TodoManager) error {
			return m.AddTodo(transactionTodo("2", TodoStatusPending))
		}},
		{name: "auto id", setup: []TodoItem{transactionTodo("1", TodoStatusPending)}, mutate: func(m *TodoManager) error {
			_, err := m.AddTodoAutoID(transactionTodo("", TodoStatusPending))
			return err
		}},
		{name: "update blocks and focus", setup: []TodoItem{
			transactionTodo("1", TodoStatusCompleted),
			transactionTodo("2", TodoStatusPending),
		}, mutate: func(m *TodoManager) error {
			return m.UpdateTodo("2", func(todo *TodoItem) error {
				todo.DependsOn = []string{"1"}
				todo.Status = TodoStatusInProgress
				return nil
			})
		}},
		{name: "delete", setup: []TodoItem{transactionTodo("1", TodoStatusPending)}, mutate: func(m *TodoManager) error {
			return m.DeleteTodo("1")
		}},
		{name: "focus", setup: []TodoItem{
			transactionTodo("1", TodoStatusInProgress),
			{ID: "2", Content: "task 2", Description: "description", Status: TodoStatusInProgress, Priority: TodoPriorityMedium, Active: true},
		}, mutate: func(m *TodoManager) error { return m.FocusTodo("1") }},
		{name: "set", setup: []TodoItem{transactionTodo("1", TodoStatusPending)}, mutate: func(m *TodoManager) error {
			return m.SetTodos([]TodoItem{transactionTodo("2", TodoStatusPending)})
		}},
		{name: "audit", setup: []TodoItem{transactionTodo("1", TodoStatusInProgress)}, mutate: func(m *TodoManager) error {
			return m.AppendAuditEventToActive(TodoAuditEvent{Type: "tool", Summary: "changed"})
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := NewTodoManager()
			if err := m.SetTodos(tt.setup); err != nil {
				t.Fatal(err)
			}
			before := m.Todos()
			m.SetSyncer(rejectingTodoSyncer{err: syncErr})
			if err := tt.mutate(m); !errors.Is(err, syncErr) {
				t.Fatalf("error = %v, want %v", err, syncErr)
			}
			assertTodosEqual(t, m.Todos(), before)
		})
	}
}

func TestTodoManagerFailedAddDoesNotConsumeSequence(t *testing.T) {
	m := NewTodoManager()
	if err := m.AddTodo(transactionTodo("1", TodoStatusPending)); err != nil {
		t.Fatal(err)
	}
	m.SetSyncer(rejectingTodoSyncer{err: errors.New("persist failed")})
	if err := m.AddTodo(transactionTodo("2", TodoStatusPending)); err == nil {
		t.Fatal("expected sync failure")
	}
	m.SetSyncer(nil)
	if err := m.AddTodo(transactionTodo("2", TodoStatusPending)); err != nil {
		t.Fatal(err)
	}
	if got := m.ByID("2").Sequence; got != 2 {
		t.Fatalf("sequence = %d, want 2", got)
	}
}

func TestTodoManagerFailedAutoIDAddDoesNotConsumeSequence(t *testing.T) {
	m := NewTodoManager()
	if _, err := m.AddTodoAutoID(transactionTodo("", TodoStatusPending)); err != nil {
		t.Fatal(err)
	}
	m.SetSyncer(rejectingTodoSyncer{err: errors.New("persist failed")})
	if _, err := m.AddTodoAutoID(transactionTodo("", TodoStatusPending)); err == nil {
		t.Fatal("expected sync failure")
	}
	m.SetSyncer(nil)
	id, err := m.AddTodoAutoID(transactionTodo("", TodoStatusPending))
	if err != nil {
		t.Fatal(err)
	}
	if id != "2" {
		t.Fatalf("id = %q, want 2", id)
	}
	if got := m.ByID(id).Sequence; got != 2 {
		t.Fatalf("sequence = %d, want 2", got)
	}
}

func TestTodoManagerClearOwnerSyncFailureRollsBackTodosAndSequence(t *testing.T) {
	m := NewTodoManager()
	owned := transactionTodo("1", TodoStatusPending)
	owned.OwnerID = "worker"
	owned.Sequence = 7
	if err := m.SetTodos([]TodoItem{owned}); err != nil {
		t.Fatal(err)
	}
	before := m.Todos()
	m.SetSyncer(rejectingTodoSyncer{err: errors.New("persist failed")})
	if err := m.ClearOwner("worker"); err == nil {
		t.Fatal("expected sync failure")
	}
	assertTodosEqual(t, m.Todos(), before)
	m.SetSyncer(nil)
	if err := m.AddTodo(TodoItem{ID: "2", Content: "next", Description: "description", Status: TodoStatusPending, Priority: TodoPriorityMedium, OwnerID: "worker"}); err != nil {
		t.Fatal(err)
	}
	if got := m.ByID("2").Sequence; got != 8 {
		t.Fatalf("sequence = %d, want 8", got)
	}
}

func TestSetTodosRestoresSequenceCounters(t *testing.T) {
	m := NewTodoManager()
	if err := m.AddTodo(TodoItem{ID: "old", Content: "old", Description: "description", Status: TodoStatusPending, Priority: TodoPriorityMedium, OwnerID: "stale", Sequence: 99}); err != nil {
		t.Fatal(err)
	}
	restored := []TodoItem{
		{ID: "1", Content: "one", Description: "description", Status: TodoStatusPending, Priority: TodoPriorityMedium, OwnerID: "worker", Sequence: 4},
		{ID: "2", Content: "two", Description: "description", Status: TodoStatusPending, Priority: TodoPriorityMedium, OwnerID: "worker", Sequence: 9},
	}
	if err := m.SetTodos(restored); err != nil {
		t.Fatal(err)
	}
	if err := m.AddTodo(TodoItem{ID: "3", Content: "three", Description: "description", Status: TodoStatusPending, Priority: TodoPriorityMedium, OwnerID: "worker"}); err != nil {
		t.Fatal(err)
	}
	if got := m.ByID("3").Sequence; got != 10 {
		t.Fatalf("restored owner sequence = %d, want 10", got)
	}
	if err := m.AddTodo(TodoItem{ID: "4", Content: "four", Description: "description", Status: TodoStatusPending, Priority: TodoPriorityMedium, OwnerID: "stale"}); err != nil {
		t.Fatal(err)
	}
	if got := m.ByID("4").Sequence; got != 1 {
		t.Fatalf("stale owner sequence = %d, want 1", got)
	}
}

func TestSetTodosIgnoresActiveOnNonInProgressTasks(t *testing.T) {
	for _, invalidStatus := range []TodoStatus{TodoStatusPending, TodoStatusCompleted} {
		t.Run(string(invalidStatus), func(t *testing.T) {
			m := NewTodoManager()
			items := []TodoItem{
				{ID: "1", Content: "invalid active", Description: "description", Status: invalidStatus, Priority: TodoPriorityMedium, Active: true},
				{ID: "2", Content: "working", Description: "description", Status: TodoStatusInProgress, Priority: TodoPriorityMedium},
			}
			if err := m.SetTodos(items); err != nil {
				t.Fatal(err)
			}
			if m.ByID("1").Active || !m.ByID("2").Active {
				t.Fatalf("invalid focus normalization: %#v", m.Todos())
			}
		})
	}
}

type reentrantTodoSyncer struct {
	manager *TodoManager
	mu      sync.Mutex
	states  [][]TodoItem
}

func (s *reentrantTodoSyncer) Sync(items []TodoItem) error {
	_ = s.manager.Todos()
	_ = s.manager.ByID("1")
	s.mu.Lock()
	s.states = append(s.states, cloneTodos(items))
	s.mu.Unlock()
	return nil
}

func TestTodoManagerSyncerCanReenterReadsAndConcurrentWritesStayOrdered(t *testing.T) {
	m := NewTodoManager()
	syncer := &reentrantTodoSyncer{manager: m}
	m.SetSyncer(syncer)

	done := make(chan error, 2)
	go func() { done <- m.AddTodo(transactionTodo("1", TodoStatusPending)) }()
	go func() { done <- m.AddTodo(transactionTodo("2", TodoStatusPending)) }()
	for range 2 {
		select {
		case err := <-done:
			if err != nil {
				t.Fatal(err)
			}
		case <-time.After(2 * time.Second):
			t.Fatal("mutation deadlocked in re-entrant syncer")
		}
	}
	if got := len(m.Todos()); got != 2 {
		t.Fatalf("todos = %d, want 2", got)
	}
	syncer.mu.Lock()
	defer syncer.mu.Unlock()
	if len(syncer.states) != 2 || len(syncer.states[0]) != 1 || len(syncer.states[1]) != 2 {
		t.Fatalf("persisted states not ordered: %#v", syncer.states)
	}
}

func TestTaskUpdatePersistenceFailureRollsBackRichChanges(t *testing.T) {
	m := NewTodoManager()
	items := []TodoItem{
		transactionTodo("1", TodoStatusPending),
		transactionTodo("2", TodoStatusPending),
		transactionTodo("3", TodoStatusPending),
	}
	if err := m.SetTodos(items); err != nil {
		t.Fatal(err)
	}
	before := m.Todos()
	m.SetSyncer(rejectingTodoSyncer{err: errors.New("persist failed")})
	result, err := NewTaskUpdateToolWithManager(m).Execute(context.Background(), map[string]any{
		"taskId":       "1",
		"subject":      "changed",
		"description":  "changed description",
		"activeForm":   "Changing",
		"category":     "debugging",
		"metadata":     map[string]any{"changed": true},
		"addNote":      "new note",
		"noteType":     "decision",
		"addBlocks":    []any{"2"},
		"addBlockedBy": []any{"3"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result == nil || !strings.Contains(result.Output, "persist failed") {
		t.Fatalf("unexpected result: %#v", result)
	}
	assertTodosEqual(t, m.Todos(), before)
}

func TestTaskUpdateLateCompletionFailureRollsBackRichChanges(t *testing.T) {
	m := NewTodoManager()
	parent := transactionTodo("1", TodoStatusInProgress)
	child := transactionTodo("2", TodoStatusPending)
	child.ParentID = "1"
	blocked := transactionTodo("3", TodoStatusPending)
	if err := m.SetTodos([]TodoItem{parent, child, blocked}); err != nil {
		t.Fatal(err)
	}
	before := m.Todos()

	result, err := NewTaskUpdateToolWithManager(m).Execute(context.Background(), map[string]any{
		"taskId":      "1",
		"status":      "completed",
		"description": "must rollback",
		"metadata":    map[string]any{"changed": true},
		"addNote":     "must rollback",
		"noteType":    "decision",
		"addBlocks":   []any{"3"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result.Output, "cannot complete") {
		t.Fatalf("unexpected result: %s", result.Output)
	}
	assertTodosEqual(t, m.Todos(), before)
}
