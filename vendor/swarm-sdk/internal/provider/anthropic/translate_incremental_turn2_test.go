package anthropic

import (
	"context"
	"testing"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/conversation"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/provider"
)

// TestIncrementalCache_Turn2KeepsAssistant reproduces the daemon "turn-2
// transcript corruption" bug: with a persistent TranslationCache (as the
// long-lived provider instance holds across turns of one conversation), the
// SECOND turn must send [user, assistant, user] — NOT drop the turn-1
// assistant reply and merge the two user prompts into one message. When the
// assistant is dropped the model sees two "say exactly" instructions and
// answers both, which is what surfaced as corrupted stored replies.
func TestIncrementalCache_Turn2KeepsAssistant(t *testing.T) {
	cache := NewTranslationCache()
	ctx := context.Background()

	// Turn 1: the first user message PLUS an inline system message that gets
	// stripped during translation. This is the exact daemon shape that broke
	// the cache: canonicalCount (2) exceeds the translated prefix (1 user), so
	// turn 2's messages[cachedCount:] skipped the assistant.
	turn1 := provider.ChatRequest{
		Messages: []*conversation.Message{
			{Role: conversation.RoleUser, Content: "Say exactly: ONE"},
			{Role: conversation.RoleSystem, Content: "inline system note"},
		},
		Model: "claude-sonnet-4-6",
	}
	if _, _, err := translateRequest(ctx, turn1, true, "", nil, cache); err != nil {
		t.Fatalf("turn1 translate: %v", err)
	}

	// Turn 2: turn-1 assistant reply is now in history, plus the new prompt.
	turn2 := provider.ChatRequest{
		Messages: []*conversation.Message{
			{Role: conversation.RoleUser, Content: "Say exactly: ONE"},
			{Role: conversation.RoleAssistant, Content: "ONE"},
			{Role: conversation.RoleUser, Content: "Say exactly: TWO"},
		},
		Model: "claude-sonnet-4-6",
	}
	got, _, err := translateRequest(ctx, turn2, true, "", nil, cache)
	if err != nil {
		t.Fatalf("turn2 translate: %v", err)
	}

	roles := make([]string, len(got.Messages))
	for i, m := range got.Messages {
		roles[i] = m.Role
	}
	if len(got.Messages) != 3 {
		t.Fatalf("turn2 should send 3 messages [user, assistant, user], got %d with roles %v", len(got.Messages), roles)
	}
	if got.Messages[0].Role != "user" || got.Messages[1].Role != "assistant" || got.Messages[2].Role != "user" {
		t.Fatalf("turn2 role sequence wrong: %v (assistant reply dropped => model answers both prompts)", roles)
	}
}
