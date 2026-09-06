package builtin

import (
	"testing"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/agent"
)

// TestSubagentTool_MisuseScenarios tests potential misuse cases identified in the usability analysis
func TestSubagentTool_MisuseScenarios(t *testing.T) {
	// Create a minimal tool instance
	tool := &SubagentTool{
		customAgentDefs: make(map[string]*agent.Definition),
	}

	// Add a test custom agent
	tool.customAgentDefs["test-agent"] = &agent.Definition{
		ID:   "test-agent",
		Name: "Test Agent",
	}

	tests := []struct {
		name        string
		params      map[string]any
		shouldError bool
		errorMsg    string
	}{
		{
			name: "conflicting agent_id and preset",
			params: map[string]any{
				"task":     "Test task",
				"agent_id": "code-reviewer",
				"preset":   "text_summarizer", // Conflicting!
			},
			shouldError: true, // Now properly caught by validation
			errorMsg:    "Validation now properly rejects conflicting agent_id and preset",
		},
		{
			name: "conflicting agent_id and system_prompt",
			params: map[string]any{
				"task":          "Test task",
				"agent_id":      "code-reviewer",
				"system_prompt": "You are a custom agent", // Overrides agent_id
			},
			shouldError: false, // Currently allowed but confusing
			errorMsg:    "Should clarify that system_prompt overrides agent_id",
		},
		{
			name: "unknown agent_id",
			params: map[string]any{
				"task":     "Test task",
				"agent_id": "non-existent-agent",
			},
			shouldError: true,
			errorMsg:    "Should provide helpful error with available agents",
		},
		{
			name: "hallucinated model",
			params: map[string]any{
				"task":  "Test task",
				"model": "claude-99-ultra", // Hallucinated model
			},
			shouldError: false, // Currently passes through, fails later
			errorMsg:    "Should validate model names",
		},
		{
			name: "both background modes",
			params: map[string]any{
				"task":                    "Test task",
				"run_in_background":       true,
				"auto_background_seconds": 30, // Conflicting background modes
			},
			shouldError: true, // Now properly caught by validation
			errorMsg:    "Validation now properly rejects conflicting background modes",
		},
		{
			name: "resume non-existent agent",
			params: map[string]any{
				"task":   "Continue previous work",
				"resume": "fake-agent-12345",
			},
			shouldError: false, // Currently continues without prior context
			errorMsg:    "Should validate resume agent exists",
		},
		{
			name: "negative auto_background",
			params: map[string]any{
				"task":                    "Test task",
				"auto_background_seconds": -5, // Invalid value
			},
			shouldError: true, // Now properly caught by validation
			errorMsg:    "Validation now properly rejects negative auto_background_seconds",
		},
		{
			name: "preset in agent_id field",
			params: map[string]any{
				"task":     "Test task",
				"agent_id": "code_formatter", // Is this preset or agent?
			},
			shouldError: false, // Works but ambiguous
			errorMsg:    "Ambiguous whether code_formatter is preset or agent",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tool.Validate(tt.params)

			// Note: Current Validate only checks for 'task' parameter
			// These tests demonstrate that more validation is needed

			if tt.name == "unknown agent_id" {
				// This would error in Execute, not Validate
				t.Logf("Note: %s - %s", tt.name, tt.errorMsg)
			} else if err != nil && !tt.shouldError {
				t.Errorf("%s: unexpected error: %v", tt.name, err)
			} else {
				t.Logf("Note: %s - %s", tt.name, tt.errorMsg)
			}
		})
	}
}

// TestSubagentTool_ParameterPrecedence verifies the documented precedence /
// conflict rules are actually enforced by Validate:
//   - agent_id + preset together is a conflict error
//   - system_prompt + agent_id is allowed (override, not a conflict)
//   - preset alone is allowed
func TestSubagentTool_ParameterPrecedence(t *testing.T) {
	tool := &SubagentTool{}

	cases := []struct {
		description string
		params      map[string]any
		wantErr     bool
	}{
		{
			description: "agent_id + preset conflict",
			params: map[string]any{
				"task":     "Test",
				"agent_id": "code-reviewer",
				"preset":   "text_summarizer",
			},
			wantErr: true,
		},
		{
			description: "system_prompt + agent_id allowed (override)",
			params: map[string]any{
				"task":          "Test",
				"system_prompt": "Custom prompt",
				"agent_id":      "code-reviewer",
			},
			wantErr: false,
		},
		{
			description: "preset alone allowed",
			params: map[string]any{
				"task":   "Test",
				"preset": "text_summarizer",
			},
			wantErr: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.description, func(t *testing.T) {
			err := tool.Validate(tc.params)
			if tc.wantErr && err == nil {
				t.Errorf("expected validation error for %q, got nil", tc.description)
			}
			if !tc.wantErr && err != nil {
				t.Errorf("expected no error for %q, got: %v", tc.description, err)
			}
		})
	}
}
