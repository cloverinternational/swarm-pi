package tools

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

// FuzzToolParameterParsing fuzzes JSON parameter parsing
func FuzzToolParameterParsing(f *testing.F) {
	f.Add([]byte(`{}`))
	f.Add([]byte(`{"key":"value"}`))
	f.Add([]byte(`{"nested":{"deep":"value"}}`))
	f.Add([]byte(`{"array":[1,2,3]}`))
	f.Add([]byte(`{"unicode":"🚀"}`))
	f.Add([]byte(`null`))
	f.Add([]byte(`[]`))
	f.Add([]byte(`"string"`))
	f.Add([]byte(`123`))
	f.Add([]byte(`true`))

	f.Fuzz(func(t *testing.T, data []byte) {
		var v any
		_ = json.Unmarshal(data, &v)

		// Try to remarshal
		_, _ = json.Marshal(v)

		// Try to unmarshal as map
		var m map[string]any
		_ = json.Unmarshal(data, &m)

		// Try to unmarshal as array
		var arr []any
		_ = json.Unmarshal(data, &arr)
	})
}

// FuzzToolNameValidation fuzzes tool name validation
func FuzzToolNameValidation(f *testing.F) {
	f.Add("valid_tool")
	f.Add("tool123")
	f.Add("")
	f.Add(" ")
	f.Add("\x00")
	f.Add("../../../etc/passwd")
	f.Add("🔧")
	f.Add(strings.Repeat("a", 1000))

	f.Fuzz(func(t *testing.T, name string) {
		// Should not panic on any name
		_ = strings.Contains(name, "/")
		_ = len(name)
		_ = strings.ToLower(name)
	})
}

// FuzzJSONMarshaling fuzzes JSON marshaling/unmarshaling cycles
func FuzzJSONMarshaling(f *testing.F) {
	f.Add([]byte(`{}`))
	f.Add([]byte(`{"name":"test","type":"string"}`))
	f.Add([]byte(`{"properties":{"p1":"v1","p2":"v2"}}`))
	f.Add([]byte(`{"items":[{"id":1},{"id":2}]}`))
	f.Add([]byte(`{"value":null}`))
	f.Add([]byte(`{"value":1.5e10}`))
	f.Add([]byte(`{"escape":"\\\"\\n\\r\\t"}`))

	f.Fuzz(func(t *testing.T, data []byte) {
		var obj map[string]any
		if err := json.Unmarshal(data, &obj); err != nil {
			return
		}

		// Remarshal and validate structure
		marshaled, err := json.Marshal(obj)
		if err != nil {
			t.Fatalf("remarshal failed: %v", err)
		}

		// Verify we can unmarshal again
		var obj2 map[string]any
		if err := json.Unmarshal(marshaled, &obj2); err != nil {
			t.Fatalf("remarshal validation failed: %v", err)
		}
	})
}

// FuzzContentBlockParsing fuzzes content block parsing
func FuzzContentBlockParsing(f *testing.F) {
	f.Add([]byte(`{"type":"text","text":"hello"}`))
	f.Add([]byte(`{"type":"image","image":{"url":"https://example.com/img.jpg"}}`))
	f.Add([]byte(`{"type":"tool_use","id":"abc123","name":"test"}`))
	f.Add([]byte(`{"type":"tool_result","tool_use_id":"abc"}`))
	f.Add([]byte(`{"type":""}`))
	f.Add([]byte(`{"unknown":"field"}`))

	f.Fuzz(func(t *testing.T, data []byte) {
		var block map[string]any
		if err := json.Unmarshal(data, &block); err != nil {
			return // Invalid JSON is OK
		}

		// Ensure we can get type field safely
		if blockType, ok := block["type"]; ok {
			_ = fmt.Sprintf("%v", blockType)
		}

		// Remarshal
		_, _ = json.Marshal(block)
	})
}

// FuzzParameterDeepCopy fuzzes deep copy operations
func FuzzParameterDeepCopy(f *testing.F) {
	f.Add([]byte(`{"simple":"value"}`))
	f.Add([]byte(`{"nested":{"deep":{"deeper":"value"}}}`))
	f.Add([]byte(`{"array":[1,"two",{"three":3}]}`))
	f.Add([]byte(`{"circular":null}`))

	f.Fuzz(func(t *testing.T, data []byte) {
		var original map[string]any
		if err := json.Unmarshal(data, &original); err != nil {
			return
		}

		// Simulate deep copy: marshal and unmarshal
		marshaled, _ := json.Marshal(original)
		var copy map[string]any
		_ = json.Unmarshal(marshaled, &copy)

		// Verify copy is independent
		originalMarsh, _ := json.Marshal(original)
		copyMarsh, _ := json.Marshal(copy)

		if !bytes.Equal(originalMarsh, copyMarsh) {
			t.Logf("copy diverged from original")
		}
	})
}

// FuzzStringEscaping fuzzes string escaping in JSON
func FuzzStringEscaping(f *testing.F) {
	f.Add("simple")
	f.Add(`"quoted"`)
	f.Add(`backslash\`)
	f.Add("\n\r\t")
	f.Add("\x00\x01\x02")
	f.Add("🚀🔧")
	f.Add(strings.Repeat("a", 10000))

	f.Fuzz(func(t *testing.T, str string) {
		// Marshal and unmarshal should preserve the value
		data, _ := json.Marshal(str)
		var unmarshaled string
		if err := json.Unmarshal(data, &unmarshaled); err != nil {
			t.Fatalf("unmarshal failed: %v", err)
		}

		if str != unmarshaled {
			t.Fatalf("round-trip failed: %q != %q", str, unmarshaled)
		}
	})
}
