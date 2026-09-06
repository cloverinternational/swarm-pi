package tools_test

import (
	"encoding/json"
	"testing"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/tools"
)

type simpleSchema struct {
	Query      string   `json:"query"       description:"Search query"   required:"true"`
	MaxResults int      `json:"max_results"  description:"Max results"`
	Format     string   `json:"format"       description:"Output format"   enum:"json,text,csv"`
	Verbose    bool     `json:"verbose"      description:"Verbose output"`
	Tags       []string `json:"tags"       description:"Filter tags"`
}

func TestSchemaFor_TopLevel(t *testing.T) {
	s := tools.SchemaFor[simpleSchema]()

	if s["type"] != "object" {
		t.Errorf("top-level type = %q, want %q", s["type"], "object")
	}

	props, ok := s["properties"].(map[string]any)
	if !ok {
		t.Fatalf("properties is not map[string]any, got %T", s["properties"])
	}

	for _, key := range []string{"query", "max_results", "format", "verbose", "tags"} {
		if _, exists := props[key]; !exists {
			t.Errorf("missing property %q", key)
		}
	}
}

func TestSchemaFor_FieldTypes(t *testing.T) {
	s := tools.SchemaFor[simpleSchema]()
	props := s["properties"].(map[string]any)

	cases := []struct {
		field    string
		wantType string
	}{
		{"query", "string"},
		{"max_results", "integer"},
		{"format", "string"},
		{"verbose", "boolean"},
		{"tags", "array"},
	}

	for _, tc := range cases {
		prop := props[tc.field].(map[string]any)
		if prop["type"] != tc.wantType {
			t.Errorf("%s type = %q, want %q", tc.field, prop["type"], tc.wantType)
		}
	}
}

func TestSchemaFor_Description(t *testing.T) {
	s := tools.SchemaFor[simpleSchema]()
	props := s["properties"].(map[string]any)

	queryProp := props["query"].(map[string]any)
	if queryProp["description"] != "Search query" {
		t.Errorf("query description = %q, want %q", queryProp["description"], "Search query")
	}
}

func TestSchemaFor_Required(t *testing.T) {
	s := tools.SchemaFor[simpleSchema]()

	req, ok := s["required"].([]string)
	if !ok {
		t.Fatalf("required is %T, want []string", s["required"])
	}
	if len(req) != 1 || req[0] != "query" {
		t.Errorf("required = %v, want [query]", req)
	}
}

func TestSchemaFor_Enum(t *testing.T) {
	s := tools.SchemaFor[simpleSchema]()
	props := s["properties"].(map[string]any)
	formatProp := props["format"].(map[string]any)

	enum, ok := formatProp["enum"].([]any)
	if !ok {
		t.Fatalf("enum is %T, want []any", formatProp["enum"])
	}
	if len(enum) != 3 {
		t.Errorf("enum len = %d, want 3", len(enum))
	}
}

func TestSchemaFor_ArrayItems(t *testing.T) {
	s := tools.SchemaFor[simpleSchema]()
	props := s["properties"].(map[string]any)
	tagsProp := props["tags"].(map[string]any)

	items, ok := tagsProp["items"].(map[string]any)
	if !ok {
		t.Fatalf("tags.items is %T, want map[string]any", tagsProp["items"])
	}
	if items["type"] != "string" {
		t.Errorf("tags items type = %q, want string", items["type"])
	}
}

func TestSchemaFor_OmitEmptyNotRequired(t *testing.T) {
	type withOptional struct {
		Name     string `json:"name"          required:"true"`
		Nickname string `json:"nickname,omitempty" required:"true"` // omitempty overrides required
	}
	s := tools.SchemaFor[withOptional]()
	req, ok := s["required"].([]string)
	if !ok {
		t.Fatalf("required is %T", s["required"])
	}
	// Nickname has omitempty so should NOT be in required even though required:"true"
	for _, r := range req {
		if r == "nickname" {
			t.Error("nickname should not be required because it has omitempty")
		}
	}
}

func TestSchemaFor_PointerType(t *testing.T) {
	// SchemaFor[*T] should work the same as SchemaFor[T]
	type p struct {
		X string `json:"x" required:"true"`
	}
	s1 := tools.SchemaFor[p]()
	s2 := tools.SchemaFor[*p]()

	b1, _ := json.Marshal(s1)
	b2, _ := json.Marshal(s2)
	if string(b1) != string(b2) {
		t.Errorf("pointer and value schemas differ:\n%s\n%s", b1, b2)
	}
}

func TestSchemaFor_MapField(t *testing.T) {
	type withMap struct {
		Env map[string]string `json:"env" description:"Environment variables"`
	}
	s := tools.SchemaFor[withMap]()
	props := s["properties"].(map[string]any)
	envProp := props["env"].(map[string]any)
	if envProp["type"] != "object" {
		t.Errorf("env type = %q, want object", envProp["type"])
	}
}

func TestSchemaFor_Default(t *testing.T) {
	type withDefault struct {
		MaxTokens int `json:"max_tokens" default:"1024" description:"Max output tokens"`
	}
	s := tools.SchemaFor[withDefault]()
	props := s["properties"].(map[string]any)
	prop := props["max_tokens"].(map[string]any)
	if prop["default"] != "1024" {
		t.Errorf("default = %v, want 1024", prop["default"])
	}
}

func TestSchemaFor_SkipsUnexported(t *testing.T) {
	type withPrivate struct {
		Public  string `json:"public"`
		private string //nolint:unused
	}
	s := tools.SchemaFor[withPrivate]()
	props := s["properties"].(map[string]any)
	if _, exists := props["private"]; exists {
		t.Error("unexported field 'private' should be skipped")
	}
}

func TestSchemaFor_SkipsJSONDash(t *testing.T) {
	type withSkip struct {
		Keep string `json:"keep"`
		Skip string `json:"-"`
	}
	s := tools.SchemaFor[withSkip]()
	props := s["properties"].(map[string]any)
	if _, exists := props["Skip"]; exists {
		t.Error("field with json:\"-\" should be skipped")
	}
	if _, exists := props["skip"]; exists {
		t.Error("field with json:\"-\" should be skipped")
	}
}
