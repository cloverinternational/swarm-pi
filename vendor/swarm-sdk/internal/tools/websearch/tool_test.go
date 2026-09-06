package websearch

import (
	"context"
	"testing"
	"time"
)

func TestToolName(t *testing.T) {
	tool := New()
	if tool.Name() != "websearch" {
		t.Errorf("Name() = %q, want %q", tool.Name(), "websearch")
	}
}

func TestToolDescription(t *testing.T) {
	tool := New()
	desc := tool.Description()
	if desc == "" {
		t.Error("Description() should not be empty")
	}
	if len(desc) < 50 {
		t.Error("Description() should be detailed (at least 50 chars)")
	}
}

func TestToolParameters(t *testing.T) {
	tool := New()
	params := tool.Parameters()
	if params == nil {
		t.Fatal("Parameters() should not return nil")
	}
	schema, ok := params.(map[string]any)
	if !ok {
		t.Fatal("Parameters() should return map[string]any")
	}
	if schema["type"] != "object" {
		t.Error("Schema type should be 'object'")
	}
	props, ok := schema["properties"].(map[string]any)
	if !ok {
		t.Fatal("Schema should have properties")
	}
	for _, field := range []string{"query", "max_results", "allowed_domains", "blocked_domains"} {
		if _, ok := props[field]; !ok {
			t.Errorf("Schema should have %q property", field)
		}
	}
}

func TestToolValidate(t *testing.T) {
	tool := New()
	tests := []struct {
		name    string
		params  map[string]any
		wantErr bool
	}{
		{"valid query", map[string]any{"query": "test search"}, false},
		{"missing query", map[string]any{}, true},
		{"empty query", map[string]any{"query": ""}, true},
		{"wrong type", map[string]any{"query": 123}, true},
		{"valid with max_results", map[string]any{"query": "test", "max_results": 5}, false},
		{"max_results too high", map[string]any{"query": "test", "max_results": 100}, true},
		{"max_results too low", map[string]any{"query": "test", "max_results": 0}, true},
		{"allowed_domains only", map[string]any{"query": "test", "allowed_domains": []string{"example.com"}}, false},
		{"blocked_domains only", map[string]any{"query": "test", "blocked_domains": []string{"example.com"}}, false},
		{"both domain filters (error)", map[string]any{
			"query":           "test",
			"allowed_domains": []string{"a.com"},
			"blocked_domains": []string{"b.com"},
		}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tool.Validate(tt.params)
			if (err != nil) != tt.wantErr {
				t.Errorf("Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestToolIsIdempotent(t *testing.T) {
	tool := New()
	if tool.IsIdempotent() {
		t.Error("IsIdempotent() should return false for web search")
	}
}

func TestToolRequiresPermission(t *testing.T) {
	tool := New()
	if len(tool.RequiresPermission()) == 0 {
		t.Error("RequiresPermission() should require at least network access")
	}
}

func TestToolSupportedContentTypes(t *testing.T) {
	tool := New()
	if len(tool.SupportedContentTypes()) == 0 {
		t.Error("SupportedContentTypes() should return at least one type")
	}
}

func TestToolOptimizationHints(t *testing.T) {
	tool := New()
	hints := tool.OptimizationHints()
	if hints == nil {
		t.Fatal("OptimizationHints() should not return nil")
	}
	if !hints.PreferSequential {
		t.Error("Web search should prefer sequential execution")
	}
}

func TestNewWithConfig(t *testing.T) {
	cfg := WebSearchConfig{
		Enabled:            true,
		MaxResultsPerQuery: 5,
		Timeout:            10 * time.Second,
		MaxPerSession:      50,
		MaxPerMinute:       5,
	}
	tool := NewWithConfig(cfg)
	if tool == nil {
		t.Fatal("NewWithConfig() should not return nil")
	}
	if tool.config.MaxResultsPerQuery != 5 {
		t.Error("Config should be applied")
	}
}

func TestNewWithExa(t *testing.T) {
	tool := NewWithExa("test-key")
	if tool == nil {
		t.Fatal("NewWithExa() should not return nil")
	}
	if tool.config.Backend != BackendExa {
		t.Errorf("Backend should be BackendExa, got %q", tool.config.Backend)
	}
	if tool.config.ExaAPIKey != "test-key" {
		t.Errorf("ExaAPIKey should be %q, got %q", "test-key", tool.config.ExaAPIKey)
	}
}

func TestDefaultWebSearchConfig(t *testing.T) {
	cfg := DefaultWebSearchConfig()
	if !cfg.Enabled {
		t.Error("Default config should be enabled")
	}
	if cfg.MaxResultsPerQuery <= 0 {
		t.Error("MaxResultsPerQuery should be positive")
	}
}

func TestGetWebSearchTools(t *testing.T) {
	tools := GetWebSearchTools()
	if len(tools) == 0 {
		t.Error("GetWebSearchTools() should return at least one tool")
	}
	if tools[0].Name() != "websearch" {
		t.Errorf("First tool should be websearch, got %s", tools[0].Name())
	}
}

func TestToStringSlice(t *testing.T) {
	tests := []struct {
		name string
		in   any
		want []string
	}{
		{"string slice", []string{"a", "b"}, []string{"a", "b"}},
		{"any slice", []any{"a", "b"}, []string{"a", "b"}},
		{"nil", nil, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := toStringSlice(tt.in)
			if len(got) != len(tt.want) {
				t.Errorf("toStringSlice() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestExecuteWithoutAuth(t *testing.T) {
	if IsAuthConfigured() {
		t.Skip("Skipping — auth is configured, would make real requests")
	}
	tool := New()
	_, err := tool.Execute(context.Background(), map[string]any{"query": "hello world"})
	if err == nil {
		t.Error("expected Execute to fail without auth configured, got nil error")
	}
}
