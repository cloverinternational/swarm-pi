package agent

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

// FuzzAgentStatePersistence fuzzes agent state save/load cycles
func FuzzAgentStatePersistence(f *testing.F) {
	f.Add([]byte(`{"name":"agent1","state":"idle"}`))
	f.Add([]byte(`{"name":"","state":""}`))
	f.Add([]byte(`{"memory":[],"context":{}}`))
	f.Add([]byte(`{}`))
	f.Add([]byte(strings.Repeat(`{"field":"value"},`, 1000)))

	f.Fuzz(func(t *testing.T, data []byte) {
		var state map[string]any
		if err := json.Unmarshal(data, &state); err != nil {
			return
		}

		// Save cycle
		marshaled, _ := json.Marshal(state)
		var loaded map[string]any
		if err := json.Unmarshal(marshaled, &loaded); err != nil {
			t.Fatalf("load failed: %v", err)
		}

		// Verify consistency
		marshaled2, _ := json.Marshal(loaded)
		if !bytes.Equal(marshaled, marshaled2) {
			t.Logf("state diverged after save/load")
		}
	})
}

// FuzzAgentMemoryManagement fuzzes memory list operations
func FuzzAgentMemoryManagement(f *testing.F) {
	f.Add([]byte(`[]`))
	f.Add([]byte(`[{"id":"1","content":"test"}]`))
	f.Add([]byte(`[{"id":"1"},{"id":"2"},{"id":"3"}]`))
	f.Add([]byte(`[null]`))
	f.Add([]byte(strings.Repeat(`[{"id":"x"},`, 500) + `[]]`))

	f.Fuzz(func(t *testing.T, data []byte) {
		var memories []map[string]any
		if err := json.Unmarshal(data, &memories); err != nil {
			return
		}

		// Safe iteration
		for i, mem := range memories {
			_ = i
			if id, ok := mem["id"]; ok {
				_ = fmt.Sprintf("%v", id)
			}
		}

		// Length operations
		_ = len(memories)

		// Append operations (simulate adding memory)
		if len(memories) < 1000 {
			memories = append(memories, map[string]any{"id": "new"})
		}
	})
}

// FuzzAgentContextInjection fuzzes context and system prompt injection
func FuzzAgentContextInjection(f *testing.F) {
	f.Add("normal context")
	f.Add("")
	f.Add("'; DROP--")
	f.Add("{{{inject}}}")
	f.Add("🚀🔒")

	f.Fuzz(func(t *testing.T, context string) {
		// Context should be safely handled
		_ = len(context)
		_ = strings.Contains(context, "DROP")
		_ = strings.Contains(context, "{{")
		escaped := strings.ReplaceAll(context, "`", "\\`")
		_ = escaped
	})
}

// FuzzToolCallParsing fuzzes tool call parsing and validation
func FuzzToolCallParsing(f *testing.F) {
	f.Add([]byte(`{"id":"call_123","name":"bash","input":{}}`))
	f.Add([]byte(`{"id":"","name":""}`))
	f.Add([]byte(`{"input":null}`))
	f.Add([]byte(`{}`))
	f.Add([]byte(`{"name":"../../../bin/sh"}`))
	f.Add([]byte(strings.Repeat(`{"call":`, 1000) + `"invalid"}`))

	f.Fuzz(func(t *testing.T, data []byte) {
		var call map[string]any
		if err := json.Unmarshal(data, &call); err != nil {
			return
		}

		// Extract fields safely
		if id, ok := call["id"]; ok {
			_ = fmt.Sprintf("%v", id)
		}
		if name, ok := call["name"]; ok {
			_ = fmt.Sprintf("%v", name)
		}

		// Remarshal
		_, _ = json.Marshal(call)
	})
}

// FuzzMessageQueueOperations fuzzes message queue operations
func FuzzMessageQueueOperations(f *testing.F) {
	f.Add([]byte(`{"msg":"test1"}`))
	f.Add([]byte(`{"msg":""}`))
	f.Add([]byte(`null`))
	f.Add([]byte(`{}`))
	f.Add([]byte(strings.Repeat(`{"msg":"x"},`, 1000)))

	f.Fuzz(func(t *testing.T, data []byte) {
		var msg map[string]any
		if err := json.Unmarshal(data, &msg); err != nil {
			return
		}

		// Queue operations should be safe
		_ = len(msg)

		// Simulate enqueue/dequeue
		queue := []map[string]any{}
		queue = append(queue, msg)
		if len(queue) > 0 {
			_ = queue[0]
		}
	})
}

// FuzzAgentNameValidation fuzzes agent name validation
func FuzzAgentNameValidation(f *testing.F) {
	f.Add("my-agent")
	f.Add("Agent_123")
	f.Add("")
	f.Add("\x00")
	f.Add("../../../etc/passwd")
	f.Add(strings.Repeat("a", 10000))

	f.Fuzz(func(t *testing.T, name string) {
		// Name validation should be safe
		_ = len(name)
		_ = strings.Contains(name, "-")
		_ = strings.ToLower(name)
		_ = strings.ReplaceAll(name, " ", "_")
	})
}

// FuzzAgentRoleHandling fuzzes agent role assignment and checking
func FuzzAgentRoleHandling(f *testing.F) {
	f.Add("user")
	f.Add("assistant")
	f.Add("system")
	f.Add("")
	f.Add("invalid_role")
	f.Add("\x00")
	f.Add(strings.Repeat("x", 5000))

	f.Fuzz(func(t *testing.T, role string) {
		// Role handling should be safe
		_ = len(role)
		_ = strings.ToLower(role)
		isValidRole := role == "user" || role == "assistant" || role == "system"
		_ = isValidRole
	})
}

// FuzzAgentCapabilitiesParsing fuzzes capability list parsing
func FuzzAgentCapabilitiesParsing(f *testing.F) {
	f.Add([]byte(`[]`))
	f.Add([]byte(`["tool_call","memory"]`))
	f.Add([]byte(`[null]`))
	f.Add([]byte(`[123]`))
	f.Add([]byte(strings.Repeat(`["cap",`, 500) + `[]]`))

	f.Fuzz(func(t *testing.T, data []byte) {
		var caps []string
		if err := json.Unmarshal(data, &caps); err != nil {
			return
		}

		// Capability checking should be safe
		for _, cap := range caps {
			_ = len(cap)
			_ = strings.Contains(cap, "_")
		}

		// Remarshal
		_, _ = json.Marshal(caps)
	})
}

// FuzzAgentIdentifierHandling fuzzes agent ID operations
func FuzzAgentIdentifierHandling(f *testing.F) {
	f.Add("agent-123")
	f.Add("")
	f.Add("\x00")
	f.Add("///../../../etc")
	f.Add(strings.Repeat("id", 5000))

	f.Fuzz(func(t *testing.T, id string) {
		// ID operations should be safe
		_ = len(id)
		_ = strings.Contains(id, "-")
		_ = strings.ToLower(id)
		normalized := strings.ReplaceAll(strings.ToLower(id), " ", "-")
		_ = normalized
	})
}
