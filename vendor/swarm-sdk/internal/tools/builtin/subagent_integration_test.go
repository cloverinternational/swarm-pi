package builtin

import (
	"context"
	"strings"
	"testing"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/observability"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/provider"
	"github.com/Swarm-Code/mono/swarm-sdk/tests/agent/mocks"
)

// TestSubagentTool_IntegrationWithFuzzyMatching tests the integration of fuzzy model matching
func TestSubagentTool_IntegrationWithFuzzyMatching(t *testing.T) {
	// Create a minimal tool instance
	logger := mocks.NewMockLogger()
	tracer := mocks.NewMockTracer()
	factory := &mockFactory{}

	tool, err := NewSubagentTool(DelegateTaskConfig{
		Factory: factory,
		Logger:  logger,
		Tracer:  tracer,
		ProviderConfig: provider.Config{
			Name: "anthropic",
		},
	})
	if err != nil {
		t.Fatalf("Failed to create subagent tool: %v", err)
	}

	tests := []struct {
		name      string
		params    map[string]any
		wantError bool
		errorMsg  string
	}{
		{
			name: "valid fuzzy model match",
			params: map[string]any{
				"task":  "Test task",
				"model": "sonnet", // Should match to claude-3-5-sonnet-20241022
			},
			wantError: false,
		},
		{
			name: "empty model is fine",
			params: map[string]any{
				"task": "Test task",
			},
			wantError: false,
		},
		{
			name: "conflicting parameters caught",
			params: map[string]any{
				"task":     "Test task",
				"agent_id": "code-reviewer",
				"preset":   "text_summarizer",
			},
			wantError: true,
			errorMsg:  "Cannot specify both 'agent_id' and 'preset'",
		},
		{
			name: "conflicting background modes",
			params: map[string]any{
				"task":                    "Test task",
				"run_in_background":       true,
				"auto_background_seconds": 30,
			},
			wantError: true,
			errorMsg:  "Cannot specify both 'run_in_background' and 'auto_background_seconds'",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tool.Validate(tt.params)

			if tt.wantError {
				if err == nil {
					t.Errorf("Expected error containing '%s', but got no error", tt.errorMsg)
				} else if tt.errorMsg != "" && !contains(err.Error(), tt.errorMsg) {
					t.Errorf("Expected error containing '%s', got: %v", tt.errorMsg, err)
				}
			} else {
				if err != nil {
					t.Errorf("Unexpected error: %v", err)
				}
			}
		})
	}
}

// contains checks if s contains substr
func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && strings.Contains(s, substr))
}

// MockLogger implementation
type MockLogger struct{}

func (m *MockLogger) Debug(ctx context.Context, msg string, fields ...observability.Field) {}
func (m *MockLogger) Info(ctx context.Context, msg string, fields ...observability.Field)  {}
func (m *MockLogger) Warn(ctx context.Context, msg string, fields ...observability.Field)  {}
func (m *MockLogger) Error(ctx context.Context, msg string, fields ...observability.Field) {}
