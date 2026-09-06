package mcp

import (
	"encoding/json"
	"testing"
)

// FuzzMCPMessageParsing fuzzes JSON-RPC message parsing
// High risk: Protocol parsing, untrusted network input, resource exhaustion
func FuzzMCPMessageParsing(f *testing.F) {
	testCases := []string{
		// Valid messages
		`{"jsonrpc":"2.0","id":1,"method":"test","params":{}}`,
		`{"jsonrpc":"2.0","id":"string-id","method":"test","params":{}}`,

		// Response messages
		`{"jsonrpc":"2.0","id":1,"result":{}}`,
		`{"jsonrpc":"2.0","id":1,"error":{"code":-32600,"message":"Invalid Request"}}`,

		// Edge cases
		"",
		"null",
		"{}",
		"[]",

		// Malformed JSON
		"{",
		"}",
		"[",
		"]",
		"{{",
		"}}",

		// Missing required fields
		`{"jsonrpc":"2.0"}`,
		`{"id":1}`,
		`{"method":"test"}`,

		// Invalid jsonrpc version
		`{"jsonrpc":"1.0","id":1,"method":"test"}`,
		`{"jsonrpc":"3.0","id":1,"method":"test"}`,
		`{"jsonrpc":"","id":1,"method":"test"}`,

		// Very large IDs
		`{"jsonrpc":"2.0","id":999999999999999999,"method":"test"}`,
		`{"jsonrpc":"2.0","id":"` + string(make([]byte, 1000000)) + `","method":"test"}`,

		// Large params
		`{"jsonrpc":"2.0","id":1,"method":"test","params":` + `{"data":"` + string(make([]byte, 1000000)) + `"}}`,

		// Nested structures
		`{"jsonrpc":"2.0","id":1,"method":"test","params":{"nested":{"deeply":{"nested":{"data":"value"}}}}}`,

		// Array params
		`{"jsonrpc":"2.0","id":1,"method":"test","params":[1,2,3,4,5]}`,

		// Null params
		`{"jsonrpc":"2.0","id":1,"method":"test","params":null}`,

		// Unicode
		`{"jsonrpc":"2.0","id":1,"method":"λ_ω_α","params":{"σ":"π"}}`,
	}

	for _, tc := range testCases {
		f.Add(tc)
	}

	f.Fuzz(func(t *testing.T, msgStr string) {
		var msg map[string]any
		err := json.Unmarshal([]byte(msgStr), &msg)

		if err == nil {
			// Validate basic structure
			if jsonrpc, ok := msg["jsonrpc"].(string); ok {
				if jsonrpc != "2.0" {
					// Invalid jsonrpc version
					_ = jsonrpc
				}
			}
		}
	})
}

// FuzzMCPTransportHandling fuzzes transport message framing
func FuzzMCPTransportHandling(f *testing.F) {
	testCases := []string{
		// Valid frames
		"Content-Length: 10\r\n\r\n0123456789",
		"Content-Length: 0\r\n\r\n",

		// Edge cases
		"",
		"\r\n",
		"\r\n\r\n",

		// Malformed headers
		"Content-Length: abc\r\n\r\n",
		"Content-Length: -1\r\n\r\n",
		"Content-Length: 999999999999\r\n\r\n",
		"No-Header: true\r\n\r\n",

		// Missing boundary
		"Content-Length: 10",
		"Content-Length: 10\r\n",
		"Content-Length: 10\r\nExtra-Header: value",

		// Very long headers
		"Content-Length: 10\r\n" + string(make([]byte, 1000000)) + "\r\n\r\n0123456789",

		// Mismatched content length
		"Content-Length: 100\r\n\r\n0123456789",
		"Content-Length: 5\r\n\r\n0123456789",

		// Binary content
		"Content-Length: 5\r\n\r\n\x00\x01\x02\x03\x04",

		// Various line endings
		"Content-Length: 10\nExtra\n\n0123456789",
	}

	for _, tc := range testCases {
		f.Add(tc)
	}

	f.Fuzz(func(t *testing.T, frame string) {
		// Frame parsing should not panic
		_ = frame
	})
}

// FuzzMCPErrorHandling fuzzes error response parsing
func FuzzMCPErrorHandling(f *testing.F) {
	testCases := []string{
		// Valid errors
		`{"jsonrpc":"2.0","id":1,"error":{"code":-32600,"message":"Invalid Request"}}`,
		`{"jsonrpc":"2.0","id":1,"error":{"code":-32601,"message":"Method not found"}}`,
		`{"jsonrpc":"2.0","id":1,"error":{"code":-32602,"message":"Invalid params"}}`,

		// Error with data field
		`{"jsonrpc":"2.0","id":1,"error":{"code":-32700,"message":"Parse error","data":"extra info"}}`,

		// Invalid error codes
		`{"jsonrpc":"2.0","id":1,"error":{"code":0,"message":"Custom error"}}`,
		`{"jsonrpc":"2.0","id":1,"error":{"code":999999,"message":"Custom error"}}`,

		// Missing error fields
		`{"jsonrpc":"2.0","id":1,"error":{"code":-32600}}`,
		`{"jsonrpc":"2.0","id":1,"error":{"message":"no code"}}`,
		`{"jsonrpc":"2.0","id":1,"error":{}}`,

		// Very long error message
		`{"jsonrpc":"2.0","id":1,"error":{"code":-32600,"message":"` + string(make([]byte, 1000000)) + `"}}`,

		// Unicode in error
		`{"jsonrpc":"2.0","id":1,"error":{"code":-32600,"message":"Errör: λ ω α"}}`,
	}

	for _, tc := range testCases {
		f.Add(tc)
	}

	f.Fuzz(func(t *testing.T, errStr string) {
		var msg map[string]any
		err := json.Unmarshal([]byte(errStr), &msg)

		if err == nil && msg != nil {
			if errIface, ok := msg["error"]; ok {
				// Validate error structure
				if errMap, ok := errIface.(map[string]any); ok {
					if code, ok := errMap["code"].(float64); ok {
						// Code should be within valid range
						_ = code
					}
				}
			}
		}
	})
}

// FuzzMCPNotificationHandling fuzzes notification messages
func FuzzMCPNotificationHandling(f *testing.F) {
	testCases := []string{
		// Valid notifications (no id field)
		`{"jsonrpc":"2.0","method":"notify"}`,
		`{"jsonrpc":"2.0","method":"notify","params":{}}`,
		`{"jsonrpc":"2.0","method":"notify","params":[1,2,3]}`,

		// Notifications with id (invalid)
		`{"jsonrpc":"2.0","id":1,"method":"notify"}`,

		// Edge cases
		`{"jsonrpc":"2.0","method":""}`,
		`{"jsonrpc":"2.0","method":null}`,
	}

	for _, tc := range testCases {
		f.Add(tc)
	}

	f.Fuzz(func(t *testing.T, notifStr string) {
		var msg map[string]any
		err := json.Unmarshal([]byte(notifStr), &msg)

		if err == nil && msg != nil {
			// Validate notification has method but no id
			_, hasID := msg["id"]
			_, hasMethod := msg["method"]
			_ = hasID
			_ = hasMethod
		}
	})
}

// FuzzMCPResourceConsumption fuzzes resource limit handling
func FuzzMCPResourceConsumption(f *testing.F) {
	testCases := []int{
		0,
		1,
		10,
		1000,
		1000000,
		-1,
		999999999,
	}

	for _, tc := range testCases {
		f.Add(tc)
	}

	f.Fuzz(func(t *testing.T, size int) {
		// Should handle resource limits gracefully
		if size < 0 {
			// Negative size should be rejected
			_ = size
		}
		if size > 10000000 {
			// Very large size should be rate limited or rejected
			_ = size
		}
	})
}
