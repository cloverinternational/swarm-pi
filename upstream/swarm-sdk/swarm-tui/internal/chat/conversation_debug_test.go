package chat

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/conversation"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/envelope"
)

// TestConversationCachePipeline simulates a conversation and shows the 3-stage transformation
func TestConversationCachePipeline(t *testing.T) {
	sessionID := "diag-session-" + time.Now().Format("150405")
	debugDir := filepath.Join(os.TempDir(), "conversation-debug", sessionID)
	os.MkdirAll(debugDir, 0755)

	t.Logf("Debug directory: %s", debugDir)

	registry := envelope.NewTransformRegistry()

	// Step 1: Simulate 1st turn (Request 1)
	t.Log(">>> Simulating Turn 1: 'say hi'")

	// Raw SSE from Anthropic (message_delta with usage)
	rawSSE1 := `{
		"type": "message_delta",
		"delta": {"stop_reason": "end_turn"},
		"usage": {
			"input_tokens": 10,
			"output_tokens": 6,
			"cache_creation_input_tokens": 7951,
			"cache_read_input_tokens": 7671
		}
	}`

	// Stage 1: Store Raw
	saveJSON(debugDir, "turn1_01_raw.json", rawSSE1)

	// Stage 2: Transform to Canonical (The side table lookup)
	env1 := envelope.NewEnvelope(envelope.ProviderAnthropic, envelope.EventTypeSSE, []byte(rawSSE1))
	env1.Transform(registry)
	saveObject(debugDir, "turn1_02_canonical.json", env1.Canonical)

	// Stage 3: Transform to Final Message (State Preservation)
	msg1 := &conversation.Message{
		Role:    conversation.RoleAssistant,
		Content: "Hi! 👋",
		Tokens: &conversation.TokenUsage{
			Input:  env1.Canonical.Usage.TotalInputWithCache,
			Output: env1.Canonical.Usage.OutputTokens,
			Total:  env1.Canonical.Usage.TotalInputWithCache + env1.Canonical.Usage.OutputTokens,
		},
		Metadata: map[string]any{
			"raw_payload": rawSSE1,
		},
	}
	saveObject(debugDir, "turn1_03_final.json", msg1)

	// Step 2: Simulate 2nd turn (Request 2)
	t.Log(">>> Simulating Turn 2: 'how are you?'")

	// Raw SSE from Anthropic (message_delta with usage)
	rawSSE2 := `{
		"type": "message_delta",
		"delta": {"stop_reason": "end_turn"},
		"usage": {
			"input_tokens": 10,
			"output_tokens": 19,
			"cache_creation_input_tokens": 7963,
			"cache_read_input_tokens": 7671
		}
	}`

	// Stage 1: Store Raw
	saveJSON(debugDir, "turn2_01_raw.json", rawSSE2)

	// Stage 2: Transform to Canonical
	env2 := envelope.NewEnvelope(envelope.ProviderAnthropic, envelope.EventTypeSSE, []byte(rawSSE2))
	env2.Transform(registry)
	saveObject(debugDir, "turn2_02_canonical.json", env2.Canonical)

	// Stage 3: Transform to Final Message
	msg2 := &conversation.Message{
		Role:    conversation.RoleAssistant,
		Content: "I'm doing great, thanks for asking! How can I help you today?",
		Tokens: &conversation.TokenUsage{
			Input:  env2.Canonical.Usage.TotalInputWithCache,
			Output: env2.Canonical.Usage.OutputTokens,
			Total:  env2.Canonical.Usage.TotalInputWithCache + env2.Canonical.Usage.OutputTokens,
		},
		Metadata: map[string]any{
			"raw_payload": rawSSE2,
		},
	}
	saveObject(debugDir, "turn2_03_final.json", msg2)

	// Show Diff between Turn 1 and Turn 2 Canonical Usage
	t.Log("\n=== CACHE METRICS DIFF ===")
	t.Logf("Turn 1 Cache Creation: %d", env1.Canonical.Usage.CacheCreationTokens)
	t.Logf("Turn 2 Cache Creation: %d", env2.Canonical.Usage.CacheCreationTokens)
	t.Logf("Creation Delta: %d tokens", env2.Canonical.Usage.CacheCreationTokens-env1.Canonical.Usage.CacheCreationTokens)

	t.Logf("\nTurn 1 Cache Read: %d", env1.Canonical.Usage.CacheReadTokens)
	t.Logf("Turn 2 Cache Read: %d", env2.Canonical.Usage.CacheReadTokens)
	t.Logf("Read Delta: %d tokens", env2.Canonical.Usage.CacheReadTokens-env1.Canonical.Usage.CacheReadTokens)

	t.Log("\nAll debug JSONs saved. You can now compare them to see exactly how the format changes.")
}

func saveJSON(dir, name, data string) {
	os.WriteFile(filepath.Join(dir, name), []byte(data), 0644)
}

func saveObject(dir, name string, obj any) {
	data, _ := json.MarshalIndent(obj, "", "  ")
	os.WriteFile(filepath.Join(dir, name), data, 0644)
}
