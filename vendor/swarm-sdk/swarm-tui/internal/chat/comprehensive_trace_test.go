package chat

import (
	"encoding/json"
	"testing"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/conversation"
)

// TestComprehensiveTrace demonstrates capturing complete message flow
func TestComprehensiveTrace(t *testing.T) {
	// Enable tracing for this test
	t.Setenv("TRACE_ENABLED", "1")

	sessionID := "test-session-123"
	messageID := "msg_001"

	trace := NewComprehensiveTrace(sessionID, messageID)

	// Step 1: Raw SSE from Anthropic (first event)
	rawSSE1 := `{
		"type":"message_start",
		"message":{
			"id":"msg_001",
			"type":"message",
			"role":"assistant",
			"model":"claude-opus-4-5-20251101",
			"content":[],
			"usage":{"input_tokens":10,"output_tokens":0,"cache_creation_input_tokens":15039,"cache_read_input_tokens":10349}
		}
	}`
	trace.LogRawSSEEvent("message_start", rawSSE1)

	// Step 2: Content block start
	rawSSE2 := `{
		"type":"content_block_start",
		"index":0,
		"content_block":{"type":"text","text":""}
	}`
	trace.LogRawSSEEvent("content_block_start", rawSSE2)

	// Step 3: Content block delta (text streaming)
	rawSSE3 := `{
		"type":"content_block_delta",
		"index":0,
		"delta":{"type":"text_delta","text":"Hello"}
	}`
	trace.LogRawSSEEvent("content_block_delta", rawSSE3)

	// Simulate accumulated state at this point
	accumulatedState := map[string]any{
		"accumulated_text":     "Hello",
		"accumulated_thinking": "",
		"tool_calls":           []any{},
		"cache_metrics": map[string]int{
			"cache_creation_tokens": 15039,
			"cache_read_tokens":     10349,
		},
	}
	trace.LogStreamChunk(map[string]any{
		"delta": "Hello",
		"type":  "content_block_delta",
		"done":  false,
	}, accumulatedState)

	// Step 4: Message delta (final)
	rawSSE4 := `{
		"type":"message_delta",
		"delta":{"stop_reason":"end_turn"},
		"usage":{"input_tokens":10,"output_tokens":6,"cache_creation_input_tokens":15039,"cache_read_input_tokens":10349}
	}`
	trace.LogRawSSEEvent("message_delta", rawSSE4)

	// Log cache metrics explicitly
	trace.LogCacheMetrics(map[string]any{
		"cache_creation_tokens": 15039,
		"cache_read_tokens":     10349,
		"total_input_tokens":    10 + 15039 + 10349, // This is the key insight!
	}, "after_message_delta")

	// Step 5: Final canonical response
	canonicalResp := map[string]any{
		"message": map[string]any{
			"role":    "assistant",
			"content": "Hello",
			"metadata": map[string]any{
				"cache_metrics": map[string]int{
					"cache_creation_tokens": 15039,
					"cache_read_tokens":     10349,
				},
			},
		},
		"finish_reason": "end_turn",
		"usage": map[string]any{
			"input":  10 + 15039 + 10349, // TOTAL including cache
			"output": 6,
			"total":  10 + 15039 + 10349 + 6,
		},
	}
	trace.LogCanonical(canonicalResp, accumulatedState)

	// Step 6: Final message ready for application
	finalMsg := &conversation.Message{
		Role:    "assistant",
		Content: "Hello",
		Metadata: map[string]any{
			"cache_metrics": map[string]int{
				"cache_creation_tokens": 15039,
				"cache_read_tokens":     10349,
			},
		},
	}
	trace.LogFinalMessage(finalMsg)

	// Get stats
	eventCount := trace.GetEventCount()
	traceFile := trace.WriteCompleteTrace()

	t.Logf("\nTrace Complete!\n")
	t.Logf("Events recorded: %d\n", eventCount)
	t.Logf("Trace directory: %s\n", trace.GetTraceDir())
	t.Logf("NDJSON file: %s\n", traceFile)
	t.Logf("\n%s", trace.Reconstruction())

	// Verify we can recreate the flow
	events := trace.GetAllEvents()
	if len(events) == 0 {
		t.Fatal("No events recorded")
	}

	// Verify event ordering
	if events[0].Stage != "raw_sse" {
		t.Errorf("First event should be raw_sse, got %s", events[0].Stage)
	}

	if events[len(events)-1].Stage != "final_message" {
		t.Errorf("Last event should be final_message, got %s", events[len(events)-1].Stage)
	}

	// Verify cache metrics are in final message
	lastEvent := events[len(events)-1]
	if lastEvent.FinalMessage == nil {
		t.Fatal("Final message not recorded")
	}

	// Pretty print the complete trace
	t.Logf("\n=== COMPLETE TRACE (NDJSON FORMAT) ===\n")
	for _, evt := range events {
		data, _ := json.MarshalIndent(evt, "", "  ")
		t.Logf("%s\n", string(data))
	}
}
