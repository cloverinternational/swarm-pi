package mcp

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

// FuzzMCPMessageParsing fuzzes MCP message parsing
func FuzzMCPMessageParsing(f *testing.F) {
	f.Add([]byte(`{"jsonrpc":"2.0","method":"tools/list"}`))
	f.Add([]byte(`{"jsonrpc":"2.0","id":1,"result":{}}`))
	f.Add([]byte(`{"jsonrpc":"2.0","error":{"code":-32000,"message":"error"}}`))
	f.Add([]byte(`{}`))
	f.Add([]byte(strings.Repeat(`{"nested":`, 1000) + `"}"}`))

	f.Fuzz(func(t *testing.T, data []byte) {
		var msg map[string]any
		if err := json.Unmarshal(data, &msg); err != nil {
			return
		}

		// Message processing should be safe
		if method, ok := msg["method"]; ok {
			_ = fmt.Sprintf("%v", method)
		}

		// Remarshal
		_, _ = json.Marshal(msg)
	})
}

// FuzzMCPToolDefinition fuzzes tool definition parsing
func FuzzMCPToolDefinition(f *testing.F) {
	f.Add([]byte(`{"name":"tool","description":"test"}`))
	f.Add([]byte(`{"name":"","description":""}`))
	f.Add([]byte(`{}`))
	f.Add([]byte(strings.Repeat(`{"field":`, 500) + `"value"}`))

	f.Fuzz(func(t *testing.T, data []byte) {
		var tool map[string]any
		if err := json.Unmarshal(data, &tool); err != nil {
			return
		}

		// Tool definition should be safe
		if name, ok := tool["name"]; ok {
			_ = fmt.Sprintf("%v", name)
		}
	})
}
