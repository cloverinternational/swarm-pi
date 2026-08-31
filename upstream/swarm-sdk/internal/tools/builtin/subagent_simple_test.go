package builtin

import (
	"context"
	"strings"
	"testing"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/agent"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/pkg/models"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/provider"
	"github.com/Swarm-Code/mono/swarm-sdk/tests/agent/mocks"
)

func TestSubagentTool_FuzzyModelMatching(t *testing.T) {
	tests := []struct {
		name        string
		modelInput  string
		wantModel   string
		shouldMatch bool
	}{
		{
			name:        "sonnet alias",
			modelInput:  "sonnet",
			wantModel:   "claude-3-5-sonnet-20241022",
			shouldMatch: true,
		},
		{
			name:        "claude 3.5",
			modelInput:  "claude 3.5",
			wantModel:   "claude-3-5-sonnet-20241022",
			shouldMatch: true,
		},
		{
			name:        "haiku",
			modelInput:  "haiku",
			wantModel:   "claude-3-5-haiku-20241022",
			shouldMatch: true,
		},
		{
			name:        "gpt4",
			modelInput:  "gpt4",
			wantModel:   "gpt-4-turbo-preview",
			shouldMatch: true,
		},
		{
			name:        "invalid model passes through",
			modelInput:  "claude-99-ultra",
			wantModel:   "claude-99-ultra", // Should pass through unchanged
			shouldMatch: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			matched, ok := models.FuzzyModelMatch(tt.modelInput)

			if tt.shouldMatch && !ok {
				t.Errorf("Expected model %q to match, but it didn't", tt.modelInput)
			}

			if !tt.shouldMatch && ok {
				t.Errorf("Expected model %q not to match, but it did", tt.modelInput)
			}

			if tt.shouldMatch && matched != tt.wantModel {
				t.Errorf("Expected model %q to match to %q, got %q", tt.modelInput, tt.wantModel, matched)
			}

			if !tt.shouldMatch && ok {
				// If it matched when it shouldn't, that's already an error caught above
			}
		})
	}
}

func TestSubagentTool_ParameterSchema(t *testing.T) {
	// Create a minimal tool instance
	logger := mocks.NewMockLogger()
	tracer := mocks.NewMockTracer()

	// Create a mock factory that doesn't actually create agents
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

	// Get parameters schema
	params := tool.Parameters()
	schema, ok := params.(map[string]any)
	if !ok {
		t.Fatal("Expected parameters to be a map")
	}

	// Check properties exist
	properties, ok := schema["properties"].(map[string]any)
	if !ok {
		t.Fatal("Expected properties in schema")
	}

	// Verify agent_id is present and has enum values
	agentIDProp, ok := properties["agent_id"].(map[string]any)
	if !ok {
		t.Fatal("Expected agent_id property in schema")
	}

	agentEnum, ok := agentIDProp["enum"].([]string)
	if !ok {
		t.Fatal("Expected agent_id to have enum values")
	}

	// Should include both custom and built-in agents
	expectedAgents := []string{
		"general-assistant",
		"code-reviewer",
		"research-agent",
		"background-worker",
		"code_formatter",
		"text_summarizer",
	}

	for _, expected := range expectedAgents {
		found := false
		for _, agent := range agentEnum {
			if agent == expected {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("Expected agent %s in enum, not found", expected)
		}
	}

	// Verify model description mentions fuzzy matching
	modelProp, ok := properties["model"].(map[string]any)
	if !ok {
		t.Fatal("Expected model property in schema")
	}

	modelDesc, ok := modelProp["description"].(string)
	if !ok || !strings.Contains(modelDesc, "fuzzy") {
		t.Error("Expected model description to mention fuzzy matching")
	}

	// Verify preset is marked as deprecated
	presetProp, ok := properties["preset"].(map[string]any)
	if !ok {
		t.Fatal("Expected preset property in schema")
	}

	presetDesc, ok := presetProp["description"].(string)
	if !ok || !strings.Contains(presetDesc, "DEPRECATED") {
		t.Error("Expected preset description to mention it's deprecated")
	}
}

// mockFactory implements a minimal agent.Factory for testing
type mockFactory struct{}

func (m *mockFactory) CreateFromDefinition(ctx context.Context, def *agent.Definition, config provider.Config) (*agent.Agent, error) {
	return nil, nil
}

func (m *mockFactory) CreateSubAgent(ctx context.Context, config agent.SubAgentConfig) (*agent.Agent, error) {
	return nil, nil
}

func (m *mockFactory) CreateWorker(ctx context.Context, config agent.WorkerConfig) (*agent.Agent, error) {
	return nil, nil
}

func (m *mockFactory) CreateSteering(ctx context.Context, config agent.SteeringAgentConfig) (*agent.Agent, error) {
	return nil, nil
}

func (m *mockFactory) CreateBackground(ctx context.Context, config agent.BackgroundConfig) (*agent.Agent, error) {
	return nil, nil
}

// TestExploreAgent_ReadOnly is a regression guard for the built-in read-only
// "explore" agent (#5). It asserts the agent exists and that its ToolHints
// allow-list contains ONLY read-only tools — no bash, no write/edit/mutation
// tool may ever be exposed to it. ToolHints is an allow-list applied at
// execution time, so any forbidden name appearing here is a real escape hatch.
func TestExploreAgent_ReadOnly(t *testing.T) {
	defs := getBuiltinAgentDefinitions()

	def, ok := defs["explore"]
	if !ok {
		t.Fatal("built-in 'explore' agent is not registered in getBuiltinAgentDefinitions()")
	}

	if def.HasAllTools() {
		t.Fatal("explore agent must NOT have '*' (all tools); it must be a read-only allow-list")
	}
	if len(def.ToolHints) == 0 {
		t.Fatal("explore agent must declare an explicit read-only ToolHints allow-list")
	}

	// Exactly the read-only tools, nothing else. Covers both registry naming
	// conventions: forge ("Read"/"Grep") and builtin ("file_read"/"grep"/"list_dir").
	readOnly := map[string]bool{
		"repository_inspect": true,
	}
	// Any tool that can mutate state or escape the sandbox must never appear.
	// Includes both forge ("Write"/"Edit"/"Shell"/"Undo") and builtin names.
	forbidden := map[string]bool{
		"bash":           true,
		"Shell":          true,
		"file_write":     true,
		"Write":          true,
		"str_replace":    true,
		"Edit":           true,
		"edit":           true,
		"write":          true,
		"Undo":           true,
		"apply_patch":    true,
		"Subagent":       true,
		"SubagentOutput": true,
		"delegate_task":  true,
	}
	for _, name := range def.ToolHints {
		if forbidden[name] {
			t.Errorf("explore agent exposes forbidden mutation/escape tool %q", name)
		}
		if !readOnly[name] {
			t.Errorf("explore agent exposes unexpected tool %q (not in the read-only set)", name)
		}
	}

	if def.Metadata == nil || def.Metadata["type"] != "sub_agent" {
		t.Error("explore agent must carry Metadata type=sub_agent so the factory configures auto-compaction")
	}
}
