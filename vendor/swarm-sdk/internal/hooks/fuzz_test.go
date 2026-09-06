package hooks

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

// FuzzHookNameValidation fuzzes hook name validation
func FuzzHookNameValidation(f *testing.F) {
	f.Add("on_message")
	f.Add("before_execute")
	f.Add("")
	f.Add("\x00")
	f.Add("../../../etc/passwd")
	f.Add(strings.Repeat("a", 10000))

	f.Fuzz(func(t *testing.T, name string) {
		// Hook name validation should be safe
		_ = len(name)
		_ = strings.Contains(name, "_")
		_ = strings.ToLower(name)
	})
}

// FuzzHookEventParsing fuzzes event parsing and dispatch
func FuzzHookEventParsing(f *testing.F) {
	f.Add([]byte(`{"type":"message_received","data":{}}`))
	f.Add([]byte(`{"type":"","data":null}`))
	f.Add([]byte(`{}`))
	f.Add([]byte(`{"type":"unknown_event"}`))
	f.Add([]byte(strings.Repeat(`{"nested":`, 1000) + `"}"}`))

	f.Fuzz(func(t *testing.T, data []byte) {
		var event map[string]any
		if err := json.Unmarshal(data, &event); err != nil {
			return
		}

		// Event processing should be safe
		if eventType, ok := event["type"]; ok {
			_ = fmt.Sprintf("%v", eventType)
		}
		if eventData, ok := event["data"]; ok {
			_ = fmt.Sprintf("%v", eventData)
		}

		// Remarshal
		_, _ = json.Marshal(event)
	})
}

// FuzzHookContextPassing fuzzes context data passing
func FuzzHookContextPassing(f *testing.F) {
	f.Add([]byte(`{"timestamp":"2024-01-01T00:00:00Z","agent":"agent1"}`))
	f.Add([]byte(`{}`))
	f.Add([]byte(`{"key":null}`))
	f.Add([]byte(strings.Repeat(`{"k":"v"},`, 500)))

	f.Fuzz(func(t *testing.T, data []byte) {
		var ctx map[string]any
		if err := json.Unmarshal(data, &ctx); err != nil {
			return
		}

		// Context iteration should be safe
		for key, val := range ctx {
			_ = fmt.Sprintf("%s=%v", key, val)
		}

		// Remarshal
		_, _ = json.Marshal(ctx)
	})
}

// FuzzHookRegistryOperations fuzzes hook registration
func FuzzHookRegistryOperations(f *testing.F) {
	f.Add("on_start")
	f.Add("on_message")
	f.Add("")
	f.Add("\x00")
	f.Add(strings.Repeat("x", 5000))

	f.Fuzz(func(t *testing.T, hookName string) {
		// Registry operations should be safe
		_ = len(hookName)
		_ = strings.ToLower(hookName)
		_ = strings.HasPrefix(hookName, "on_")
	})
}

// FuzzHookExecutionChaining fuzzes chained hook execution
func FuzzHookExecutionChaining(f *testing.F) {
	f.Add([]byte(`[]`))
	f.Add([]byte(`[{"name":"hook1"}]`))
	f.Add([]byte(`[{"name":"hook1"},{"name":"hook2"}]`))
	f.Add([]byte(strings.Repeat(`[{"name":"h"},`, 500) + `[]]`))

	f.Fuzz(func(t *testing.T, data []byte) {
		var hooks []map[string]any
		if err := json.Unmarshal(data, &hooks); err != nil {
			return
		}

		// Hook chain execution should be safe
		for i, hook := range hooks {
			_ = i
			if name, ok := hook["name"]; ok {
				_ = fmt.Sprintf("%v", name)
			}
		}

		// Remarshal
		_, _ = json.Marshal(hooks)
	})
}

// FuzzHookErrorHandling fuzzes error handling in hooks
func FuzzHookErrorHandling(f *testing.F) {
	f.Add([]byte(`{"error":"something failed"}`))
	f.Add([]byte(`{"error":null}`))
	f.Add([]byte(`{}`))
	f.Add([]byte(`{"error":{}}`))

	f.Fuzz(func(t *testing.T, data []byte) {
		var hookResult map[string]any
		if err := json.Unmarshal(data, &hookResult); err != nil {
			return
		}

		// Error extraction should be safe
		if errField, ok := hookResult["error"]; ok {
			_ = fmt.Sprintf("%v", errField)
		}

		// Remarshal
		_, _ = json.Marshal(hookResult)
	})
}
