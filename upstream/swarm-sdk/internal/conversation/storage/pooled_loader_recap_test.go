package storage

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/conversation"
)

func TestExtractMetaPreservesGeneratedRecap(t *testing.T) {
	input := &conversation.Conversation{
		ID:            "recap-meta-test",
		CreatedAt:     time.Now(),
		UpdatedAt:     time.Now(),
		Mode:          "chat",
		Status:        conversation.StatusActive,
		WorkspacePath: "/tmp/workspace",
		Summary: &conversation.ConversationSummary{
			MessageCount: 2,
			Recap:        "The agent traced the problem and completed the fix.",
		},
	}
	data, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	got, err := extractMeta(data, 1)
	if err != nil {
		t.Fatal(err)
	}
	if got.Summary == nil || got.Summary.Recap != input.Summary.Recap {
		t.Fatalf("metadata-only recap = %#v, want %q", got.Summary, input.Summary.Recap)
	}
}
