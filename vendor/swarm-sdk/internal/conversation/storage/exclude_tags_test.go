package storage

import (
	"context"
	"testing"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/conversation"
)

// mkConv builds a minimal conversation with the given id and tags.
func mkConv(id string, tags ...string) *conversation.Conversation {
	return &conversation.Conversation{
		ID:     id,
		Status: conversation.StatusActive,
		Metadata: conversation.ConversationMetadata{
			Tags: tags,
		},
	}
}

// TestQueryExcludeTags verifies that Filter.ExcludeTags drops conversations
// carrying any excluded tag from listing results, while leaving untagged /
// other-tagged conversations intact. This is the mechanism that keeps headless
// (`swarm -p`) runs out of the browsable/searchable TUI history.
func TestQueryExcludeTags(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStorage()

	convs := []*conversation.Conversation{
		mkConv("tui-1", "tui"),
		mkConv("headless-1", conversation.HeadlessTag),
		mkConv("plain-1"),
		mkConv("mixed-1", "tui", conversation.HeadlessTag),
	}
	for _, c := range convs {
		if err := store.Save(ctx, c); err != nil {
			t.Fatalf("Save(%s): %v", c.ID, err)
		}
	}

	// With ExcludeTags=[headless], the headless-only and mixed convs disappear.
	got, err := store.Query(ctx, Filter{ExcludeTags: []string{conversation.HeadlessTag}})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	gotIDs := map[string]bool{}
	for _, c := range got {
		gotIDs[c.ID] = true
	}
	if !gotIDs["tui-1"] || !gotIDs["plain-1"] {
		t.Fatalf("expected tui-1 and plain-1 to remain, got %v", gotIDs)
	}
	if gotIDs["headless-1"] {
		t.Fatalf("headless-1 should be excluded, got %v", gotIDs)
	}
	if gotIDs["mixed-1"] {
		t.Fatalf("mixed-1 (carries excluded tag) should be excluded, got %v", gotIDs)
	}

	// Empty ExcludeTags is a no-op: everything is returned.
	all, err := store.Query(ctx, Filter{})
	if err != nil {
		t.Fatalf("Query(no filter): %v", err)
	}
	if len(all) != len(convs) {
		t.Fatalf("empty ExcludeTags should return all %d convs, got %d", len(convs), len(all))
	}

	// Excluded conversations are still reachable by direct ID lookup, proving
	// the exclusion only affects listing, not persistence/resume.
	if _, err := store.Load(ctx, "headless-1"); err != nil {
		t.Fatalf("Load(headless-1) should still succeed after listing exclusion: %v", err)
	}
}
