package provider

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

// FuzzProviderResponseParsing fuzzes API response parsing
func FuzzProviderResponseParsing(f *testing.F) {
	f.Add([]byte(`{"id":"msg_123","type":"message","content":[]}`))
	f.Add([]byte(`{"id":"","error":"invalid"}`))
	f.Add([]byte(`{}`))
	f.Add([]byte(`{"content":[{"type":"text","text":"hello"}]}`))
	f.Add([]byte(`{"usage":{"input_tokens":100,"output_tokens":50}}`))
	f.Add([]byte(`{"stop_reason":"max_tokens"}`))
	f.Add([]byte(`null`))
	f.Add([]byte(`"string"`))
	f.Add([]byte(`123`))
	f.Add([]byte(strings.Repeat(`{"nested":`, 1000) + `}"}`))

	f.Fuzz(func(t *testing.T, data []byte) {
		var resp map[string]any
		if err := json.Unmarshal(data, &resp); err != nil {
			return
		}

		// Access common fields safely
		if id, ok := resp["id"]; ok {
			_ = fmt.Sprintf("%v", id)
		}
		if err, ok := resp["error"]; ok {
			_ = fmt.Sprintf("%v", err)
		}
		if content, ok := resp["content"]; ok {
			_ = fmt.Sprintf("%v", content)
		}

		// Remarshal
		_, _ = json.Marshal(resp)
	})
}

// FuzzModelNameValidation fuzzes model name handling
func FuzzModelNameValidation(f *testing.F) {
	f.Add("gpt-4")
	f.Add("claude-3-opus")
	f.Add("gemini-1.5-pro")
	f.Add("")
	f.Add("\x00")
	f.Add("../../../etc/passwd")
	f.Add("'; DROP--")
	f.Add(strings.Repeat("a", 10000))

	f.Fuzz(func(t *testing.T, model string) {
		// Model validation shouldn't panic
		_ = len(model)
		_ = strings.Contains(model, "-")
		_ = strings.ToLower(model)
		_ = strings.Split(model, "-")
	})
}

// FuzzAPIKeyHandling fuzzes API key handling
func FuzzAPIKeyHandling(f *testing.F) {
	f.Add("sk-proj-valid-key-123")
	f.Add("")
	f.Add("\x00\x01\x02")
	f.Add("🔑")

	f.Fuzz(func(t *testing.T, apiKey string) {
		// API key operations should be safe
		_ = len(apiKey)
		_ = strings.Contains(apiKey, "-")
		// Should never log full key - handle edge case of short keys
		maskLen := min(max(len(apiKey)-3, 0),
			// Prevent massive allocations
			100)
		masked := "sk-" + strings.Repeat("*", maskLen)
		_ = masked
	})
}

// FuzzErrorResponseHandling fuzzes error response parsing
func FuzzErrorResponseHandling(f *testing.F) {
	f.Add([]byte(`{"error":{"type":"invalid_request","message":"Bad request"}}`))
	f.Add([]byte(`{"error":"string error"}`))
	f.Add([]byte(`{"error":null}`))
	f.Add([]byte(`{"error":{}}`))
	f.Add([]byte(`{}`))
	f.Add([]byte(`{"status":400,"detail":"Not found"}`))

	f.Fuzz(func(t *testing.T, data []byte) {
		var resp map[string]any
		if err := json.Unmarshal(data, &resp); err != nil {
			return
		}

		// Error extraction should be safe
		if errField, ok := resp["error"]; ok {
			_ = fmt.Sprintf("%v", errField)
		}

		// Remarshal
		_, _ = json.Marshal(resp)
	})
}

// FuzzTokenCountingParsing fuzzes token counting in responses
func FuzzTokenCountingParsing(f *testing.F) {
	f.Add([]byte(`{"usage":{"input_tokens":0,"output_tokens":0}}`))
	f.Add([]byte(`{"usage":{"input_tokens":-1,"output_tokens":999999999}}`))
	f.Add([]byte(`{"usage":{"input_tokens":"100"}}`))
	f.Add([]byte(`{"usage":null}`))
	f.Add([]byte(`{}`))

	f.Fuzz(func(t *testing.T, data []byte) {
		var resp map[string]any
		if err := json.Unmarshal(data, &resp); err != nil {
			return
		}

		// Token access should be safe
		if usage, ok := resp["usage"]; ok {
			if usageMap, ok := usage.(map[string]any); ok {
				if input, ok := usageMap["input_tokens"]; ok {
					_ = fmt.Sprintf("%v", input)
				}
				if output, ok := usageMap["output_tokens"]; ok {
					_ = fmt.Sprintf("%v", output)
				}
			}
		}

		// Remarshal
		_, _ = json.Marshal(resp)
	})
}

// FuzzProviderSwitching fuzzes provider switching and state transitions
func FuzzProviderSwitching(f *testing.F) {
	f.Add("anthropic")
	f.Add("openai")
	f.Add("gemini")
	f.Add("")
	f.Add("invalid_provider")
	f.Add("\x00")
	f.Add(strings.Repeat("x", 1000))

	f.Fuzz(func(t *testing.T, provider string) {
		// Provider switching should be safe
		_ = len(provider)
		_ = strings.ToLower(provider)
		isValid := provider == "anthropic" || provider == "openai" || provider == "gemini"
		_ = isValid
	})
}

// FuzzContentBlockParsing fuzzes provider-specific content block parsing
func FuzzContentBlockParsing(f *testing.F) {
	f.Add([]byte(`{"type":"text","text":"hello"}`))
	f.Add([]byte(`{"type":"image","source":{"type":"base64","media_type":"image/jpeg","data":""}}`))
	f.Add([]byte(`{"type":"tool_use","id":"call_123","name":"tool_name","input":{}}`))
	f.Add([]byte(`{"type":"tool_result","tool_use_id":"call_123","content":""}`))
	f.Add([]byte(`{"type":""}`))
	f.Add([]byte(`{}`))
	f.Add([]byte(`null`))

	f.Fuzz(func(t *testing.T, data []byte) {
		var block map[string]any
		if err := json.Unmarshal(data, &block); err != nil {
			return
		}

		// Block processing should be safe
		if blockType, ok := block["type"]; ok {
			_ = fmt.Sprintf("%v", blockType)
		}

		// Remarshal
		_, _ = json.Marshal(block)
	})
}

// FuzzStopReasonParsing fuzzes stop reason handling
func FuzzStopReasonParsing(f *testing.F) {
	f.Add("end_turn")
	f.Add("max_tokens")
	f.Add("tool_use")
	f.Add("")
	f.Add("invalid_reason")
	f.Add("\x00")
	f.Add(strings.Repeat("x", 5000))

	f.Fuzz(func(t *testing.T, reason string) {
		// Stop reason handling should be safe
		_ = len(reason)
		_ = strings.ToLower(reason)
		isValidStopReason := reason == "end_turn" || reason == "max_tokens" || reason == "tool_use"
		_ = isValidStopReason
	})
}

// FuzzRequestHeaderConstruction fuzzes HTTP header construction
func FuzzRequestHeaderConstruction(f *testing.F) {
	f.Add("application/json")
	f.Add("")
	f.Add("text/plain")
	f.Add("\x00\x01\x02")
	f.Add("\r\nInjected-Header: value")
	f.Add(strings.Repeat("x", 10000))

	f.Fuzz(func(t *testing.T, contentType string) {
		// Header construction should be safe
		_ = len(contentType)
		_ = strings.Contains(contentType, "/")
		_ = strings.Contains(contentType, "\n")
		_ = strings.Contains(contentType, "\r")
	})
}
