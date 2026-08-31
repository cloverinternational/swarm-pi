package converters

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

// FuzzJSONTypeConversion fuzzes JSON type conversion operations
func FuzzJSONTypeConversion(f *testing.F) {
	f.Add([]byte(`"string"`))
	f.Add([]byte(`123`))
	f.Add([]byte(`123.45`))
	f.Add([]byte(`true`))
	f.Add([]byte(`null`))
	f.Add([]byte(`{}`))
	f.Add([]byte(`[]`))
	f.Add([]byte(strings.Repeat(`[`, 1000) + `]`))

	f.Fuzz(func(t *testing.T, data []byte) {
		var value any
		if err := json.Unmarshal(data, &value); err != nil {
			return
		}

		// Type conversions should be safe
		switch v := value.(type) {
		case string:
			_ = len(v)
		case float64:
			_ = int(v)
		case bool:
			_ = v
		case map[string]any:
			_ = len(v)
		case []any:
			_ = len(v)
		case nil:
			// OK
		}

		// Remarshal
		_, _ = json.Marshal(value)
	})
}

// FuzzStringConversion fuzzes string encoding/decoding
func FuzzStringConversion(f *testing.F) {
	f.Add("simple")
	f.Add("")
	f.Add("\x00\x01\x02")
	f.Add("\\\"\\n\\r\\t")
	f.Add("🚀🔒🔓")
	f.Add(strings.Repeat("a", 10000))

	f.Fuzz(func(t *testing.T, s string) {
		// String conversions should be safe
		_ = len(s)
		_ = strings.ToLower(s)
		_ = strings.ToUpper(s)

		// JSON round-trip
		data, _ := json.Marshal(s)
		var s2 string
		_ = json.Unmarshal(data, &s2)

		if s != s2 {
			t.Fatalf("string conversion failed: %q != %q", s, s2)
		}
	})
}

// FuzzNumberConversion fuzzes number type conversions
func FuzzNumberConversion(f *testing.F) {
	f.Add([]byte(`0`))
	f.Add([]byte(`-1`))
	f.Add([]byte(`999999999`))
	f.Add([]byte(`1.5`))
	f.Add([]byte(`-1.5e10`))
	f.Add([]byte(`"not a number"`))
	f.Add([]byte(`null`))

	f.Fuzz(func(t *testing.T, data []byte) {
		var num any
		if err := json.Unmarshal(data, &num); err != nil {
			return
		}

		// Number operations should be safe
		switch n := num.(type) {
		case float64:
			_ = int(n)
			_ = int64(n)
			_ = n > 0
		}
	})
}

// FuzzMapConversion fuzzes map type conversions
func FuzzMapConversion(f *testing.F) {
	f.Add([]byte(`{}`))
	f.Add([]byte(`{"key":"value"}`))
	f.Add([]byte(`{"nested":{"deep":"value"}}`))
	f.Add([]byte(`{"array":[1,2,3]}`))
	f.Add([]byte(strings.Repeat(`{"k":`, 500) + `"v"}`))

	f.Fuzz(func(t *testing.T, data []byte) {
		var m map[string]any
		if err := json.Unmarshal(data, &m); err != nil {
			return
		}

		// Map operations should be safe
		for k, v := range m {
			_ = fmt.Sprintf("%s=%v", k, v)
		}

		// Remarshal
		_, _ = json.Marshal(m)
	})
}

// FuzzArrayConversion fuzzes array type conversions
func FuzzArrayConversion(f *testing.F) {
	f.Add([]byte(`[]`))
	f.Add([]byte(`[1,2,3]`))
	f.Add([]byte(`["a","b","c"]`))
	f.Add([]byte(`[{"a":1},{"b":2}]`))
	f.Add([]byte(strings.Repeat(`[`, 500) + `]`))

	f.Fuzz(func(t *testing.T, data []byte) {
		var arr []any
		if err := json.Unmarshal(data, &arr); err != nil {
			return
		}

		// Array operations should be safe
		for i, v := range arr {
			_ = fmt.Sprintf("[%d]=%v", i, v)
		}

		// Remarshal
		_, _ = json.Marshal(arr)
	})
}

// FuzzProtobufConversion fuzzes protobuf-like conversions
func FuzzProtobufConversion(f *testing.F) {
	f.Add([]byte(`{"field1":"value","field2":123}`))
	f.Add([]byte(`{}`))
	f.Add([]byte(`{"field1":null}`))

	f.Fuzz(func(t *testing.T, data []byte) {
		var msg map[string]any
		if err := json.Unmarshal(data, &msg); err != nil {
			return
		}

		// Protobuf conversion should be safe
		for key, val := range msg {
			_ = fmt.Sprintf("%s=%v", key, val)
		}
	})
}

// FuzzEncodingEscaping fuzzes various encoding escape sequences
func FuzzEncodingEscaping(f *testing.F) {
	f.Add("plain")
	f.Add(`quote"test`)
	f.Add("backslash\\test")
	f.Add("newline\ntest")
	f.Add("unicode\u0000\uffff")
	f.Add(strings.Repeat("x", 10000))

	f.Fuzz(func(t *testing.T, s string) {
		// Escape operations should be safe
		_ = len(s)
		data, _ := json.Marshal(s)
		var s2 string
		_ = json.Unmarshal(data, &s2)
	})
}
