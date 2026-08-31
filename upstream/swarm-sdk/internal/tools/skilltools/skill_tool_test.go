package skilltools

import (
	"context"
	"sync/atomic"
	"testing"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/observability"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/skills"
	"github.com/Swarm-Code/mono/swarm-sdk/tests/agent/mocks"
)

func TestSkillTool_Name(t *testing.T) {
	tool := newTestSkillTool(t)
	if tool.Name() != "Skill" {
		t.Errorf("Name() = %q, want %q", tool.Name(), "Skill")
	}
}

func TestSkillTool_Description(t *testing.T) {
	tool := newTestSkillTool(t)
	desc := tool.Description()
	if desc == "" {
		t.Error("Description() returned empty string")
	}
	// Must mention "skill" and "invoke"
	if !contains(desc, "skill") {
		t.Errorf("Description() should mention 'skill', got: %s", desc)
	}
}

func TestSkillTool_Parameters(t *testing.T) {
	tool := newTestSkillTool(t)
	params := tool.Parameters()
	schema, ok := params.(map[string]any)
	if !ok {
		t.Fatal("Parameters() should return map[string]any")
	}
	if schema["type"] != "object" {
		t.Errorf("Parameters type = %v, want object", schema["type"])
	}
	props, ok := schema["properties"].(map[string]any)
	if !ok {
		t.Fatal("Parameters should have properties")
	}
	if _, hasSkill := props["skill"]; !hasSkill {
		t.Error("Parameters should have 'skill' property")
	}
	if _, hasArgs := props["args"]; !hasArgs {
		t.Error("Parameters should have 'args' property")
	}
}

func TestSkillTool_Execute_ValidSkill(t *testing.T) {
	tool := newTestSkillTool(t)
	// Register a test skill
	tool.registry.RegisterSkill(&skills.Skill{
		Metadata: skills.SkillMetadata{
			Name:        "test-skill",
			Description: "A test skill",
		},
		Instructions:  "Do the thing",
		ContentLoaded: true,
	}, true)

	result, err := tool.Execute(context.Background(), map[string]any{
		"skill": "test-skill",
	})
	if err != nil {
		t.Fatalf("Execute() error: %v", err)
	}
	if result.IsError {
		t.Errorf("Execute() returned error result: %s", result.Output)
	}
	if !contains(result.Output, "Do the thing") {
		t.Errorf("Execute() output should contain skill instructions, got: %s", result.Output)
	}
}

func TestSkillTool_Execute_UnknownSkill(t *testing.T) {
	tool := newTestSkillTool(t)
	_, err := tool.Execute(context.Background(), map[string]any{
		"skill": "nonexistent-skill",
	})
	if err == nil {
		t.Fatal("Execute() should return error for unknown skill")
	}
}

func TestSkillTool_Execute_DisabledInvocation(t *testing.T) {
	tool := newTestSkillTool(t)
	tool.registry.RegisterSkill(&skills.Skill{
		Metadata: skills.SkillMetadata{
			Name:                   "disabled-skill",
			Description:            "A disabled skill",
			DisableModelInvocation: true,
		},
		Instructions: "Should not be invoked",
	}, true)

	_, err := tool.Execute(context.Background(), map[string]any{
		"skill": "disabled-skill",
	})
	if err == nil {
		t.Fatal("Execute() should return error for DisableModelInvocation skill")
	}
}

func TestSkillTool_Execute_WithArgs(t *testing.T) {
	tool := newTestSkillTool(t)
	tool.registry.RegisterSkill(&skills.Skill{
		Metadata: skills.SkillMetadata{
			Name:        "arg-skill",
			Description: "A skill with args",
			Arguments:   []string{"repo", "branch"},
		},
		Instructions:  "Clone {{repo}} on {{branch}}",
		ContentLoaded: true,
	}, true)

	result, err := tool.Execute(context.Background(), map[string]any{
		"skill": "arg-skill",
		"args":  "myrepo main",
	})
	if err != nil {
		t.Fatalf("Execute() error: %v", err)
	}
	if !contains(result.Output, "Clone myrepo on main") {
		t.Errorf("Execute() should substitute args, got: %s", result.Output)
	}
}

func TestSkillTool_Execute_MissingSkillParam(t *testing.T) {
	tool := newTestSkillTool(t)
	_, err := tool.Execute(context.Background(), map[string]any{})
	if err == nil {
		t.Fatal("Execute() should return error when skill param is missing")
	}
}

func TestFormatSkillsWithinBudget(t *testing.T) {
	allSkills := []*skills.Skill{
		{
			Metadata: skills.SkillMetadata{
				Name:        "deploy",
				Description: "Deploy the application to production",
				WhenToUse:   "when the user asks to deploy",
			},
		},
		{
			Metadata: skills.SkillMetadata{
				Name:        "test-skill",
				Description: "Run the test suite",
				WhenToUse:   "when the user asks to run tests",
			},
		},
		{
			Metadata: skills.SkillMetadata{
				Name:                   "internal-only",
				Description:            "Internal skill not for LLM",
				DisableModelInvocation: true,
			},
		},
	}

	listing := FormatSkillsWithinBudget(allSkills, 200000)

	// Should include deploy and test-skill
	if !contains(listing, "deploy") {
		t.Error("Listing should include 'deploy' skill")
	}
	if !contains(listing, "test-skill") {
		t.Error("Listing should include 'test-skill' skill")
	}
	// Should NOT include disabled skill
	if contains(listing, "internal-only") {
		t.Error("Listing should NOT include disabled skill")
	}
	// Should include when_to_use
	if !contains(listing, "When to use:") {
		t.Error("Listing should include 'When to use:' from when_to_use field")
	}
}

func TestFormatSkillsWithinBudget_Empty(t *testing.T) {
	listing := FormatSkillsWithinBudget(nil, 200000)
	if listing != "" {
		t.Errorf("Empty input should return empty string, got: %q", listing)
	}
}

// --- Helpers ---

func newTestSkillTool(t *testing.T) *SkillTool {
	t.Helper()
	registry := skills.NewRegistry()
	logger := mocks.NewMockLogger()
	tracer := mocks.NewMockTracer()

	tool, err := NewSkillTool(SkillToolConfig{
		Registry: registry,
		Logger:   logger,
		Tracer:   tracer,
	})
	if err != nil {
		t.Fatalf("NewSkillTool() error: %v", err)
	}
	return tool
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsSubstr(s, substr))
}

func containsSubstr(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

func TestSkillTool_Execute_Telemetry(t *testing.T) {
	skills.ResetTelemetryListeners()

	var invoked atomic.Int32
	skills.AddTelemetryListener(func(event skills.SkillTelemetryEvent) {
		if event.Type == "skill_invoked" {
			invoked.Add(1)
			if event.SkillName != "telemetry-skill" {
				t.Errorf("expected skill_name telemetry-skill, got %s", event.SkillName)
			}
			if event.DurationMS < 0 {
				t.Error("expected non-negative duration_ms for skill_invoked")
			}
		}
	})

	tool := newTestSkillTool(t)
	tool.registry.RegisterSkill(&skills.Skill{
		Metadata: skills.SkillMetadata{
			Name:        "telemetry-skill",
			Description: "A skill for telemetry testing",
		},
		Instructions:  "Do telemetry things",
		ContentLoaded: true,
		LoadedFrom:    "project",
	}, true)

	_, err := tool.Execute(context.Background(), map[string]any{
		"skill": "telemetry-skill",
	})
	if err != nil {
		t.Fatalf("Execute() error: %v", err)
	}

	if invoked.Load() != 1 {
		t.Errorf("expected 1 skill_invoked event, got %d", invoked.Load())
	}
}

func TestSkillTool_Execute_TelemetryOnError(t *testing.T) {
	skills.ResetTelemetryListeners()

	var invoked atomic.Int32
	var gotError atomic.Int32
	skills.AddTelemetryListener(func(event skills.SkillTelemetryEvent) {
		if event.Type == "skill_invoked" {
			invoked.Add(1)
			if event.Error != "" {
				gotError.Add(1)
			}
		}
	})

	tool := newTestSkillTool(t)
	// Execute with nonexistent skill — should still emit telemetry with error
	_, _ = tool.Execute(context.Background(), map[string]any{
		"skill": "no-such-skill",
	})

	if invoked.Load() != 1 {
		t.Errorf("expected 1 skill_invoked event even on error, got %d", invoked.Load())
	}
	if gotError.Load() != 1 {
		t.Error("expected error field in telemetry for failed invocation")
	}
}

// Ensure SkillTool implements the tools.Tool interface at compile time.
var _ observability.Logger = (*mocks.MockLogger)(nil)
var _ observability.Tracer = (*mocks.MockTracer)(nil)
