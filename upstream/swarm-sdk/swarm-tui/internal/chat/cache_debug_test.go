package chat

import (
	"testing"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/conversation"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/provider"
)

// TestCacheDebugger demonstrates how to capture and compare cache formats
func TestCacheDebugger(t *testing.T) {
	debugger := NewCacheDebugger()

	// Example 1: Simulate raw Anthropic response
	rawAnthropicResp := map[string]any{
		"type": "message_delta",
		"delta": map[string]any{
			"stop_reason": "end_turn",
		},
		"usage": map[string]any{
			"input_tokens":                7,
			"output_tokens":               204,
			"cache_creation_input_tokens": 19111,
			"cache_read_input_tokens":     0,
		},
	}
	debugger.LogRawAnthropicResponse(rawAnthropicResp, "message_delta")

	// Example 2: Simulate canonical format after translation
	msg := &conversation.Message{
		Role:    "assistant",
		Content: "Hi! 👋",
		Metadata: map[string]any{
			"cache_metrics": map[string]int{
				"cache_creation_tokens": 19111,
				"cache_read_tokens":     0,
			},
		},
	}

	chatResp := &provider.ChatResponse{
		Message:      msg,
		FinishReason: "end_turn",
		Usage: &conversation.TokenUsage{
			Input:  7,
			Output: 204,
		},
	}
	debugger.LogCanonicalFormat(chatResp)

	// Example 3: Log final transformed format
	debugger.LogTransformedFormat(msg)

	t.Logf("\n%s", debugger.ComparisonReport())
}
