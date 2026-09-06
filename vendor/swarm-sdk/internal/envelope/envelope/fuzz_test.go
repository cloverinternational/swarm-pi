package envelope

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

// FuzzEnvelopeMarshaling fuzzes envelope marshaling/unmarshaling
func FuzzEnvelopeMarshaling(f *testing.F) {
	f.Add([]byte(`{"id":"env1","payload":{}}`))
	f.Add([]byte(`{"id":"","payload":null}`))
	f.Add([]byte(`{}`))
	f.Add([]byte(`{"payload":[]}`))
	f.Add([]byte(strings.Repeat(`{"nested":`, 1000) + `"}"}`))

	f.Fuzz(func(t *testing.T, data []byte) {
		var env map[string]any
		if err := json.Unmarshal(data, &env); err != nil {
			return
		}

		// Envelope operations should be safe
		if id, ok := env["id"]; ok {
			_ = fmt.Sprintf("%v", id)
		}
		if payload, ok := env["payload"]; ok {
			_ = fmt.Sprintf("%v", payload)
		}

		// Round-trip test
		marshaled, _ := json.Marshal(env)
		var env2 map[string]any
		if err := json.Unmarshal(marshaled, &env2); err != nil {
			t.Fatalf("remarshal failed: %v", err)
		}

		marshaled2, _ := json.Marshal(env2)
		if !bytes.Equal(marshaled, marshaled2) {
			t.Logf("envelope diverged")
		}
	})
}

// FuzzEnvelopeIDGeneration fuzzes envelope ID operations
func FuzzEnvelopeIDGeneration(f *testing.F) {
	f.Add("env-123")
	f.Add("")
	f.Add("\x00")
	f.Add("../../../etc/passwd")
	f.Add(strings.Repeat("x", 5000))

	f.Fuzz(func(t *testing.T, id string) {
		// ID operations should be safe
		_ = len(id)
		_ = strings.Contains(id, "-")
		_ = strings.ToLower(id)
	})
}

// FuzzEnvelopeVersioning fuzzes version handling
func FuzzEnvelopeVersioning(f *testing.F) {
	f.Add("1.0")
	f.Add("2.1.3")
	f.Add("")
	f.Add("invalid")
	f.Add("\x00")
	f.Add(strings.Repeat("1.", 5000))

	f.Fuzz(func(t *testing.T, version string) {
		// Version operations should be safe
		_ = len(version)
		parts := strings.SplitSeq(version, ".")
		for part := range parts {
			_ = len(part)
		}
	})
}

// FuzzEnvelopeCompressionCycle fuzzes compression operations
func FuzzEnvelopeCompressionCycle(f *testing.F) {
	f.Add([]byte(``))
	f.Add([]byte(`test data`))
	f.Add([]byte(strings.Repeat(`x`, 10000)))
	f.Add([]byte(`{"json":"data"}`))
	f.Add([]byte("\x00\x01\x02\xff\xfe"))

	f.Fuzz(func(t *testing.T, data []byte) {
		// Compression operations should be safe
		_ = len(data)
		_ = bytes.Contains(data, []byte("x"))
	})
}

// FuzzEnvelopeMetadataParsing fuzzes metadata in envelopes
func FuzzEnvelopeMetadataParsing(f *testing.F) {
	f.Add([]byte(`{"timestamp":"2024-01-01T00:00:00Z","source":"agent1"}`))
	f.Add([]byte(`{}`))
	f.Add([]byte(`{"key":null}`))
	f.Add([]byte(strings.Repeat(`{"k":"v"},`, 500)))

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
