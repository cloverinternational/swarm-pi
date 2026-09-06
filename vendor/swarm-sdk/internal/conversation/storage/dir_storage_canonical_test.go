package storage

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/conversation"
)

func TestDirectoryFileStorageCanonicalTranscriptRoundTrip(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	store := newTestDirectoryStorage(t, root)
	conv := &conversation.Conversation{
		ID:            "canonical-round-trip",
		WorkspacePath: "/workspace/persistence",
		Status:        conversation.StatusActive,
		Messages: []*conversation.Message{{
			ID:        "assistant-1",
			Role:      conversation.RoleAssistant,
			Content:   "persist me now",
			Timestamp: time.Date(2026, 7, 27, 12, 0, 0, 0, time.UTC),
			Tokens: &conversation.TokenUsage{
				Input:  123,
				Output: 45,
				Total:  168,
			},
		}},
	}

	if err := store.Save(ctx, conv); err != nil {
		t.Fatalf("Save: %v", err)
	}

	canonicalPath := filepath.Join(root, conv.ID, "conversation.json")
	data, err := os.ReadFile(canonicalPath)
	if err != nil {
		t.Fatalf("canonical transcript was not written incrementally: %v", err)
	}
	var persisted conversation.Conversation
	if err := json.Unmarshal(data, &persisted); err != nil {
		t.Fatalf("decode canonical transcript: %v", err)
	}
	if len(persisted.Messages) != 1 || persisted.Messages[0].Tokens == nil ||
		persisted.Messages[0].Tokens.Total != 168 {
		t.Fatalf("canonical transcript lost message usage: %#v", persisted.Messages)
	}

	// Prove that the conversation-owned file is the authoritative round-trip
	// path, rather than an exit-time or workspace-copy fallback.
	compatibilityPath := filepath.Join(root, EncodeWorkspacePath(conv.WorkspacePath), conv.ID+".json")
	if err := os.Remove(compatibilityPath); err != nil {
		t.Fatalf("remove compatibility copy: %v", err)
	}
	reloaded, err := store.Load(ctx, conv.ID)
	if err != nil {
		t.Fatalf("Load canonical transcript: %v", err)
	}
	if len(reloaded.Messages) != 1 || reloaded.Messages[0].Content != "persist me now" ||
		reloaded.Messages[0].Tokens == nil || reloaded.Messages[0].Tokens.Input != 123 {
		t.Fatalf("round-trip mismatch: %#v", reloaded.Messages)
	}
}
