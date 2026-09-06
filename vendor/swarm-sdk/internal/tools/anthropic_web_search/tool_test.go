package anthropic_web_search

import (
	"context"
	"testing"
)

func TestToolName(t *testing.T) {
	tool := New()
	expected := "anthropic_web_search"
	if tool.Name() != expected {
		t.Errorf("Name() = %q, want %q", tool.Name(), expected)
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

	if _, ok := props["query"]; !ok {
		t.Error("Schema should have 'query' property")
	}
}

func TestToolValidate(t *testing.T) {
	tool := New()

	tests := []struct {
		name    string
		params  map[string]any
		wantErr bool
	}{
		{
			name:    "valid query",
			params:  map[string]any{"query": "test search"},
			wantErr: false,
		},
		{
			name:    "missing query",
			params:  map[string]any{},
			wantErr: true,
		},
		{
			name:    "empty query",
			params:  map[string]any{"query": ""},
			wantErr: true,
		},
		{
			name:    "wrong type",
			params:  map[string]any{"query": 123},
			wantErr: true,
		},
		{
			name:    "valid with max_results",
			params:  map[string]any{"query": "test", "max_results": 5},
			wantErr: false,
		},
		{
			name:    "max_results too high",
			params:  map[string]any{"query": "test", "max_results": 100},
			wantErr: true,
		},
		{
			name:    "max_results too low",
			params:  map[string]any{"query": "test", "max_results": 0},
			wantErr: true,
		},
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
	// Web search is NOT idempotent (results change over time)
	if tool.IsIdempotent() {
		t.Error("IsIdempotent() should return false for web search")
	}
}

func TestToolRequiresPermission(t *testing.T) {
	tool := New()
	perms := tool.RequiresPermission()
	if len(perms) == 0 {
		t.Error("RequiresPermission() should require at least network access")
	}
}

func TestToolSupportedContentTypes(t *testing.T) {
	tool := New()
	types := tool.SupportedContentTypes()
	if len(types) == 0 {
		t.Error("SupportedContentTypes() should return at least one type")
	}
}

func TestToolOptimizationHints(t *testing.T) {
	tool := New()
	hints := tool.OptimizationHints()
	if hints == nil {
		t.Error("OptimizationHints() should not return nil")
	}
	// Web search should prefer sequential execution
	if !hints.PreferSequential {
		t.Error("Web search should prefer sequential execution")
	}
}

func TestNewWithConfig(t *testing.T) {
	config := WebSearchConfig{
		Enabled:            true,
		MaxResultsPerQuery: 5,
		Timeout:            10000000000, // 10 seconds
		MaxPerSession:      50,
		MaxPerMinute:       5,
	}
	tool := NewWithConfig(config)
	if tool == nil {
		t.Fatal("NewWithConfig() should not return nil")
	}
	if tool.config.MaxResultsPerQuery != 5 {
		t.Error("Config should be applied")
	}
}

func TestDefaultWebSearchConfig(t *testing.T) {
	config := DefaultWebSearchConfig()
	if !config.Enabled {
		t.Error("Default config should be enabled")
	}
	if config.MaxResultsPerQuery <= 0 {
		t.Error("MaxResultsPerQuery should be positive")
	}
	if config.MaxPerSession <= 0 {
		t.Error("MaxPerSession should be positive")
	}
	if config.MaxPerMinute <= 0 {
		t.Error("MaxPerMinute should be positive")
	}
}

func TestGetWebSearchTools(t *testing.T) {
	tools := GetWebSearchTools()
	if len(tools) == 0 {
		t.Error("GetWebSearchTools() should return at least one tool")
	}
	// Verify the first tool is the web search tool
	if tools[0].Name() != "anthropic_web_search" {
		t.Errorf("First tool should be anthropic_web_search, got %s", tools[0].Name())
	}
}

// TestExecuteWithoutAuth tests that Execute fails gracefully without auth.
// This test doesn't require actual OAuth setup.
func TestExecuteWithoutAuth(t *testing.T) {
	// Skip if OAuth is configured (we don't want to make real requests).
	if IsAuthConfigured() {
		t.Skip("Skipping - OAuth is configured, would make real requests")
	}

	tool := New()
	_, err := tool.Execute(context.Background(), map[string]any{"query": "hello world"})
	if err == nil {
		t.Error("expected Execute to fail without OAuth configured, got nil error")
	}
}
