package taskstore

import (
	"bytes"
	"errors"
	"os"
	"reflect"
	"testing"
	"time"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/tools/ii"
)

func newSyncTestStore(t *testing.T) *Store {
	t.Helper()
	return New(Config{ConversationID: "conv", MetadataDir: t.TempDir()})
}

func TestSyncTodosRoundTripPreservesAllFields(t *testing.T) {
	store := newSyncTestStore(t)
	created := time.Now().Add(-time.Hour).UTC().Truncate(time.Second)
	completed := time.Now().UTC().Truncate(time.Second)
	item := ii.TodoItem{
		ID: "1", Content: "Ship feature", Description: "detailed spec",
		Status: ii.TodoStatusCompleted, Priority: ii.TodoPriorityHigh,
		Category: ii.TaskCategoryActing, ActiveForm: "Shipping feature",
		Metadata: map[string]any{"pr": "123"}, Notes: []string{"legacy"},
		TypedNotes:  []ii.TodoNote{{Type: "decision", Content: "chose approach", CreatedAt: created, Metadata: map[string]any{"why": "simplest"}}},
		AuditEvents: []ii.TodoAuditEvent{{Type: "tool", Timestamp: completed, Actor: "main", Summary: "Edit file.go", Metadata: map[string]any{"tool": "Edit"}}},
		OwnerID:     "main", Sequence: 2, ParentID: "", CreatedAt: created, UpdatedAt: completed,
		CompletedAt: &completed, SourceTurn: 5, PlanID: "plan-1", DecomposedBy: "user",
	}
	if err := SyncTodos(store, []ii.TodoItem{item}); err != nil {
		t.Fatal(err)
	}
	got, err := store.GetTask("1")
	if err != nil {
		t.Fatal(err)
	}
	if got.Description != "detailed spec" || got.ActiveForm != "Shipping feature" {
		t.Fatalf("description/active form lost: %+v", got)
	}
	if got.Metadata["pr"] != "123" || len(got.TypedNotes) != 1 || len(got.AuditEvents) != 1 {
		t.Fatalf("rich fields lost: %+v", got)
	}
	if got.Category != "acting" || got.PlanID != "plan-1" || got.SourceTurn != 5 {
		t.Fatalf("provenance lost: %+v", got)
	}
	if !got.CreatedAt.Equal(created) {
		t.Fatalf("CreatedAt changed: %v", got.CreatedAt)
	}

	// Second sync must not regenerate CreatedAt.
	if err := SyncTodos(store, []ii.TodoItem{item}); err != nil {
		t.Fatal(err)
	}
	again, _ := store.GetTask("1")
	if !again.CreatedAt.Equal(created) {
		t.Fatalf("CreatedAt churned on re-sync: %v", again.CreatedAt)
	}
}

func TestSyncTodosSaveFailureLeavesStoreAndExistingFileUnchanged(t *testing.T) {
	metadataDir := t.TempDir()
	cfg := Config{ConversationID: "conv", MetadataDir: metadataDir}
	store := New(cfg)
	original := Task{ID: "1", Subject: "original", Description: "d", Status: StatusPending, Priority: PriorityMedium}
	if err := store.AddTask(original); err != nil {
		t.Fatal(err)
	}
	if err := store.Save(); err != nil {
		t.Fatal(err)
	}
	beforeTasks := store.GetAllTasks()
	beforeBytes, err := os.ReadFile(store.filePath)
	if err != nil {
		t.Fatal(err)
	}
	beforeLoaded, err := Load(cfg)
	if err != nil {
		t.Fatal(err)
	}
	originalWrite := writeTaskStoreFile
	writeTaskStoreFile = func(string, []byte) error {
		return errors.New("injected atomic-write failure")
	}
	t.Cleanup(func() { writeTaskStoreFile = originalWrite })

	err = SyncTodos(store, []ii.TodoItem{{ID: "2", Content: "replacement", Description: "d", Status: ii.TodoStatusPending, Priority: ii.TodoPriorityMedium}})
	if err == nil {
		t.Fatal("expected persistence failure")
	}
	if got := store.GetAllTasks(); !reflect.DeepEqual(got, beforeTasks) {
		t.Fatalf("store changed after failed save\ngot:  %#v\nwant: %#v", got, beforeTasks)
	}
	afterBytes, err := os.ReadFile(store.filePath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(afterBytes, beforeBytes) {
		t.Fatal("existing task file changed after failed sync")
	}
	afterLoaded, err := Load(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(afterLoaded.GetAllTasks(), beforeLoaded.GetAllTasks()) {
		t.Fatalf("loaded task store changed after failed sync\ngot:  %#v\nwant: %#v", afterLoaded.GetAllTasks(), beforeLoaded.GetAllTasks())
	}
}

func TestSyncTodosBuildsCompleteCandidateBeforeValidation(t *testing.T) {
	store := newSyncTestStore(t)
	child := ii.TodoItem{ID: "2", Content: "child", Description: "d", Status: ii.TodoStatusPending, Priority: ii.TodoPriorityMedium, ParentID: "1"}
	parent := ii.TodoItem{ID: "1", Content: "parent", Description: "d", Status: ii.TodoStatusPending, Priority: ii.TodoPriorityMedium}
	if err := SyncTodos(store, []ii.TodoItem{child, parent}); err != nil {
		t.Fatalf("sync complete candidate: %v", err)
	}
	if got, err := store.GetTask("2"); err != nil || got.ParentID != "1" {
		t.Fatalf("child not persisted with parent: task=%+v err=%v", got, err)
	}
}

func TestRestoreTodosNormalizesLegacyFocus(t *testing.T) {
	store := newSyncTestStore(t)
	now := time.Now().UTC()
	for _, id := range []string{"1", "2"} {
		if err := store.AddTask(Task{ID: id, Subject: "Task " + id, Status: StatusInProgress, Priority: PriorityMedium, CreatedAt: now, UpdatedAt: now}); err != nil {
			t.Fatal(err)
		}
	}
	manager := ii.NewTodoManager()
	if err := RestoreTodos(store, manager); err != nil {
		t.Fatal(err)
	}
	if !manager.ByID("1").Active || manager.ByID("2").Active {
		t.Fatal("legacy focus was not normalized on restore")
	}
}
