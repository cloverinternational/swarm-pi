package taskstore

import (
	"testing"
	"time"
)

func TestSetTaskStatusMaintainsLifecycleAndSingleActiveInvariant(t *testing.T) {
	store := New(Config{ConversationID: "status", MetadataDir: t.TempDir()})
	completed := time.Now().Add(-time.Hour)
	for _, task := range []Task{
		{ID: "one", Subject: "One", Status: StatusInProgress, Active: true},
		{ID: "two", Subject: "Two", Status: StatusCompleted, CompletedAt: &completed},
	} {
		if err := store.AddTask(task); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.SetTaskStatus("two", StatusInProgress); err != nil {
		t.Fatal(err)
	}
	one, _ := store.GetTask("one")
	two, _ := store.GetTask("two")
	if one.Active || !two.Active {
		t.Fatalf("focus invariant broken: one=%+v two=%+v", one, two)
	}
	if two.CompletedAt != nil {
		t.Fatalf("reopened task retained CompletedAt: %+v", two)
	}
	if err := store.SetTaskStatus("two", StatusPending); err != nil {
		t.Fatal(err)
	}
	two, _ = store.GetTask("two")
	if two.Active || two.CompletedAt != nil {
		t.Fatalf("pending task has contradictory lifecycle: %+v", two)
	}
	if err := store.SetTaskStatus("one", StatusCompleted); err != nil {
		t.Fatal(err)
	}
	one, _ = store.GetTask("one")
	if one.Active || one.CompletedAt == nil {
		t.Fatalf("completed task has contradictory lifecycle: %+v", one)
	}
	completedAt := one.CompletedAt
	if err := store.SetTaskStatus("one", StatusCompleted); err != nil {
		t.Fatal(err)
	}
	one, _ = store.GetTask("one")
	if !one.CompletedAt.Equal(*completedAt) {
		t.Fatalf("reaffirming completed reset CompletedAt: before=%v after=%v", completedAt, one.CompletedAt)
	}
}

func TestSetTaskStatusLifecycleSurvivesSaveAndLoad(t *testing.T) {
	cfg := Config{ConversationID: "reload", MetadataDir: t.TempDir()}
	store := New(cfg)
	completed := time.Now().Add(-time.Hour)
	if err := store.AddTask(Task{ID: "one", Subject: "One", Status: StatusCompleted, Active: true, CompletedAt: &completed}); err != nil {
		t.Fatal(err)
	}
	if err := store.SetTaskStatus("one", StatusInProgress); err != nil {
		t.Fatal(err)
	}
	if err := store.Save(); err != nil {
		t.Fatal(err)
	}
	reloaded, err := Load(cfg)
	if err != nil {
		t.Fatal(err)
	}
	task, err := reloaded.GetTask("one")
	if err != nil {
		t.Fatal(err)
	}
	if task.Status != StatusInProgress || !task.Active || task.CompletedAt != nil {
		t.Fatalf("reloaded lifecycle is contradictory: %+v", task)
	}
}
