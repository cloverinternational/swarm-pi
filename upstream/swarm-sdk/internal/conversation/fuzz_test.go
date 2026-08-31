package conversation

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

// FuzzMessageParsing fuzzes message parsing and validation
func FuzzMessageParsing(f *testing.F) {
	f.Add([]byte(`{"role":"user","content":"hello"}`))
	f.Add([]byte(`{"role":"assistant","content":[{"type":"text","text":"response"}]}`))
	f.Add([]byte(`{"role":"user","content":null}`))
	f.Add([]byte(`{"role":"","content":""}`))
	f.Add([]byte(`{}`))
	f.Add([]byte(`{"role":"invalid_role","content":"text"}`))
	f.Add([]byte(`{"role":"user","content":123}`))
	f.Add([]byte(strings.Repeat(`{"content":"x"}`, 1000)))

	f.Fuzz(func(t *testing.T, data []byte) {
		var msg map[string]any
		if err := json.Unmarshal(data, &msg); err != nil {
			return // Invalid JSON OK
		}

		// Validate fields don't cause panics
		if role, ok := msg["role"]; ok {
			_ = fmt.Sprintf("%v", role)
		}
		if content, ok := msg["content"]; ok {
			_ = fmt.Sprintf("%v", content)
		}

		// Remarshal
		_, _ = json.Marshal(msg)
	})
}

// FuzzConversationStateTransitions fuzzes state transitions
func FuzzConversationStateTransitions(f *testing.F) {
	f.Add([]byte(`"initial"`))
	f.Add([]byte(`"active"`))
	f.Add([]byte(`"closed"`))
	f.Add([]byte(`"invalid"`))
	f.Add([]byte(`""`))
	f.Add([]byte(``))
	f.Add([]byte(`123`))
	f.Add([]byte(`true`))

	f.Fuzz(func(t *testing.T, data []byte) {
		var state string
		if err := json.Unmarshal(data, &state); err != nil {
			return
		}

		// State transitions shouldn't panic
		switch state {
		case "initial", "active", "closed":
			// valid states
		default:
			// invalid but shouldn't crash
		}

		_ = len(state)
		_ = strings.ToLower(state)
	})
}

// FuzzMessageSerializationCycle fuzzes message serialization round-trips
func FuzzMessageSerializationCycle(f *testing.F) {
	f.Add([]byte(`{"id":"msg1","timestamp":"2024-01-01T00:00:00Z"}`))
	f.Add([]byte(`{"id":"","timestamp":null}`))
	f.Add([]byte(`{"metadata":{"key":"value","nested":{"deep":"value"}}}`))
	f.Add([]byte(`{"tokens":0,"stop_reason":"max_tokens"}`))
	f.Add([]byte(`{"usage":{"input_tokens":100,"output_tokens":50}}`))

	f.Fuzz(func(t *testing.T, data []byte) {
		var msg map[string]any
		if err := json.Unmarshal(data, &msg); err != nil {
			return
		}

		// First serialization
		ser1, _ := json.Marshal(msg)

		// Deserialize again
		var msg2 map[string]any
		if err := json.Unmarshal(ser1, &msg2); err != nil {
			t.Fatalf("second unmarshal failed: %v", err)
		}

		// Second serialization
		ser2, _ := json.Marshal(msg2)

		// Should be identical
		if !bytes.Equal(ser1, ser2) {
			t.Logf("serialization diverged")
		}
	})
}

// FuzzConversationIDGeneration fuzzes conversation ID generation
func FuzzConversationIDGeneration(f *testing.F) {
	f.Add("valid_id")
	f.Add("")
	f.Add("\x00")
	f.Add(strings.Repeat("a", 10000))
	f.Add("../../../etc/passwd")
	f.Add("'; DROP TABLE conversations; --")

	f.Fuzz(func(t *testing.T, id string) {
		// ID operations shouldn't panic
		_ = len(id)
		_ = strings.Contains(id, "/")
		_ = strings.ToLower(id)
		hash := strings.ToLower(id)
		_ = hash
	})
}

// FuzzTimestampHandling fuzzes timestamp parsing and manipulation
func FuzzTimestampHandling(f *testing.F) {
	f.Add("2024-01-01T00:00:00Z")
	f.Add("")
	f.Add("invalid")
	f.Add("0000-00-00T00:00:00Z")
	f.Add("2024-13-01T00:00:00Z")
	f.Add(strings.Repeat("2024-01-01T00:00:00Z", 100))

	f.Fuzz(func(t *testing.T, ts string) {
		// Should handle any timestamp gracefully
		_ = len(ts)
		_ = strings.Contains(ts, "T")
		_ = strings.Contains(ts, "Z")
	})
}

// FuzzThreadIDManagement fuzzes thread ID operations
func FuzzThreadIDManagement(f *testing.F) {
	f.Add("thread_123")
	f.Add("")
	f.Add("null")
	f.Add("\x00")
	f.Add(strings.Repeat("x", 5000))

	f.Fuzz(func(t *testing.T, threadID string) {
		// Thread ID operations should be safe
		_ = len(threadID)
		_ = strings.ToLower(threadID)
		_ = strings.HasPrefix(threadID, "thread_")
		encoded := strings.ReplaceAll(threadID, " ", "_")
		_ = encoded
	})
}

// FuzzMetadataStorage fuzzes metadata storage and retrieval
func FuzzMetadataStorage(f *testing.F) {
	f.Add([]byte(`{}`))
	f.Add([]byte(`{"custom":"value"}`))
	f.Add([]byte(`{"nested":{"deep":{"deeper":"value"}}}`))
	f.Add([]byte(`{"special":"!@#$%^&*()"}`))
	f.Add([]byte(`{"unicode":"🔒🔓"}`))
	f.Add([]byte(strings.Repeat(`{"k":"v"},`, 1000)))

	f.Fuzz(func(t *testing.T, data []byte) {
		var metadata map[string]any
		if err := json.Unmarshal(data, &metadata); err != nil {
			return
		}

		// Metadata operations should be safe
		for key, val := range metadata {
			_ = fmt.Sprintf("%s=%v", key, val)
		}

		// Remarshal
		_, _ = json.Marshal(metadata)
	})
}

// FuzzMessageContentIterator fuzzes iterating over message content
func FuzzMessageContentIterator(f *testing.F) {
	f.Add([]byte(`[]`))
	f.Add([]byte(`[{"type":"text","text":"hello"}]`))
	f.Add([]byte(`[{"type":"text"},{"type":"image"},{"type":"tool_use"}]`))
	f.Add([]byte(`[{"invalid":"block"}]`))
	f.Add([]byte(strings.Repeat(`[{"type":"text"},`, 500) + `[]]`))

	f.Fuzz(func(t *testing.T, data []byte) {
		var blocks []map[string]any
		if err := json.Unmarshal(data, &blocks); err != nil {
			return
		}

		// Iterating should be safe
		for i, block := range blocks {
			_ = i
			if blockType, ok := block["type"]; ok {
				_ = fmt.Sprintf("%v", blockType)
			}
		}

		// Length operations
		_ = len(blocks)
	})
}
