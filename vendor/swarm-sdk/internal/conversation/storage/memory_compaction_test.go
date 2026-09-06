package storage

import (
	"context"
	"testing"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/conversation"
)

func TestMemoryStoragePreservesCompactionBoundary(t *testing.T) {
	store := NewMemoryStorage()
	conv := &conversation.Conversation{
		ID:                          "compacted",
		CurrentContextSize:          1_200,
		CurrentContextSizeEstimated: true,
		Messages: []*conversation.Message{
			{ID: "archived", Role: conversation.RoleUser, Content: "old"},
			{ID: "summary", Role: conversation.RoleUser, Content: "summary"},
		},
		CompactionState: &conversation.CompactionState{
			ActiveContextStart: 1,
			Generation:         2,
			Tasks: []conversation.TaskEntry{{
				ID:        "task",
				Content:   "verify",
				Status:    "in_progress",
				DependsOn: []string{"prior"},
			}},
		},
	}
	if err := store.Save(context.Background(), conv); err != nil {
		t.Fatal(err)
	}
	loaded, err := store.Load(context.Background(), conv.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !loaded.CurrentContextSizeEstimated {
		t.Fatal("estimated context-size provenance was lost")
	}
	if loaded.CompactionState == nil || loaded.CompactionState.Generation != 2 {
		t.Fatalf("compaction state lost: %+v", loaded.CompactionState)
	}
	active := loaded.ActiveMessages()
	if len(active) != 1 || active[0].ID != "summary" {
		t.Fatalf("active boundary lost: %#v", active)
	}

	loaded.CompactionState.Tasks[0].DependsOn[0] = "mutated"
	again, err := store.Load(context.Background(), conv.ID)
	if err != nil {
		t.Fatal(err)
	}
	if again.CompactionState.Tasks[0].DependsOn[0] != "prior" {
		t.Fatal("compaction state clone aliases stored task dependencies")
	}
}
