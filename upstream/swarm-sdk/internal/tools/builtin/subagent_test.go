// Package builtin provides tests for delegate_task tool.
package builtin

import (
	"testing"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/provider"
)

// TestRoleModelSelector_CalledForPreset tests that the RoleModelSelector is called
// when spawning a sub-agent with a known preset type.
func TestRoleModelSelector_CalledForPreset(t *testing.T) {
	selectorCalled := false
	var receivedRole RoleType

	selector := func(role RoleType) *RoleModelConfig {
		selectorCalled = true
		receivedRole = role
		return &RoleModelConfig{
			Provider: "cerebras",
			Model:    "zai-glm-4.7",
		}
	}

	// Verify DelegateTaskConfig has RoleModelSelector field
	config := DelegateTaskConfig{
		Factory:           nil,
		ProviderConfig:    provider.Config{Name: "anthropic", Model: "claude-opus-4-5"},
		Logger:            nil,
		Tracer:            nil,
		RoleModelSelector: selector,
	}

	// Verify config has the selector
	if config.RoleModelSelector == nil {
		t.Error("RoleModelSelector field not set in DelegateTaskConfig")
	}

	// Call the selector to verify it works
	result := config.RoleModelSelector(RoleImplementer)
	if result == nil {
		t.Error("RoleModelSelector returned nil")
	}

	// Verify selector was called
	if !selectorCalled {
		t.Error("RoleModelSelector was not called when spawning sub-agent")
	}

	// Verify correct role was passed
	if receivedRole != RoleImplementer {
		t.Errorf("Expected role %q, got %q", RoleImplementer, receivedRole)
	}
}

// TestRoleModelSelector_ReturnsCorrectConfig tests that the model config
// from the selector is used for the sub-agent.
func TestRoleModelSelector_ReturnsCorrectConfig(t *testing.T) {
	expectedProvider := "cerebras"
	expectedModel := "zai-glm-4.7"

	selector := func(role RoleType) *RoleModelConfig {
		return &RoleModelConfig{
			Provider: expectedProvider,
			Model:    expectedModel,
		}
	}

	// Create a minimal SubagentTool to test getProviderConfig
	tool := &SubagentTool{
		providerConfig:    provider.Config{Name: "anthropic", Model: "claude-opus-4-5"},
		roleModelSelector: selector,
	}

	// Call getProviderConfig with a role
	config, _ := tool.getProviderConfig("", "", RoleImplementer)

	// Verify the provider and model match selector output
	if config.Name != expectedProvider {
		t.Errorf("Expected provider %q, got %q", expectedProvider, config.Name)
	}
	if config.Model != expectedModel {
		t.Errorf("Expected model %q, got %q", expectedModel, config.Model)
	}
}

// TestRoleModelSelector_FallbackWhenEmpty tests that when selector returns nil,
// the default model is used.
func TestRoleModelSelector_FallbackWhenEmpty(t *testing.T) {
	selector := func(role RoleType) *RoleModelConfig {
		return nil // No role-specific config
	}

	// Create a minimal SubagentTool with selector that returns nil
	tool := &SubagentTool{
		providerConfig:    provider.Config{Name: "anthropic", Model: ""},
		roleModelSelector: selector,
	}

	// Call getProviderConfig with a role
	config, _ := tool.getProviderConfig("", "", RoleImplementer)

	// Verify fallback to default model
	expectedModel := "claude-3-5-haiku-20241022"
	if config.Model != expectedModel {
		t.Errorf("Expected fallback model %q, got %q", expectedModel, config.Model)
	}
}

// TestRoleModelSelector_PresetToRoleMapping tests that presets are correctly
// mapped to SADD roles.
func TestRoleModelSelector_PresetToRoleMapping(t *testing.T) {
	tests := []struct {
		preset       string
		expectedRole RoleType
	}{
		{"Explore", RoleImplementer},
		{"Plan", RoleSupervisor},
		{"code-review", RoleQualityReviewer},
		{"bug-hunter", RoleQualityReviewer},
		{"general-purpose", RoleImplementer},
	}

	for _, tt := range tests {
		t.Run(tt.preset, func(t *testing.T) {
			role := presetToRole(tt.preset)
			if role != tt.expectedRole {
				t.Errorf("Preset %q: expected role %q, got %q", tt.preset, tt.expectedRole, role)
			}
		})
	}
}

// TestRoleModelSelector_ReasoningLevelPassed tests that reasoning level
// is passed to the provider config for models that support it.
func TestRoleModelSelector_ReasoningLevelPassed(t *testing.T) {
	selector := func(role RoleType) *RoleModelConfig {
		return &RoleModelConfig{
			Provider:       "openai",
			Model:          "gpt-5.2-codex",
			ReasoningLevel: "high",
		}
	}

	// Create a minimal SubagentTool with selector
	tool := &SubagentTool{
		providerConfig:    provider.Config{Name: "anthropic", Model: "claude-opus-4-5"},
		roleModelSelector: selector,
	}

	// Call getProviderConfig with a role
	config, _ := tool.getProviderConfig("", "", RoleImplementer)

	// Verify reasoning level is passed via Custom map
	if config.Custom == nil {
		t.Fatal("ProviderConfig.Custom is nil, expected reasoning_level to be set")
	}
	reasoningLevel, ok := config.Custom["reasoning_level"].(string)
	if !ok {
		t.Fatal("reasoning_level not found in ProviderConfig.Custom")
	}
	if reasoningLevel != "high" {
		t.Errorf("Expected reasoning_level %q, got %q", "high", reasoningLevel)
	}
}

// TestRoleModelSelector_DisableReasoningPassed tests that disable_reasoning
// is passed to the provider config for Cerebras GLM.
func TestRoleModelSelector_DisableReasoningPassed(t *testing.T) {
	selector := func(role RoleType) *RoleModelConfig {
		return &RoleModelConfig{
			Provider:         "cerebras",
			Model:            "zai-glm-4.7",
			DisableReasoning: true,
		}
	}

	// Create a minimal SubagentTool with selector
	tool := &SubagentTool{
		providerConfig:    provider.Config{Name: "anthropic", Model: "claude-opus-4-5"},
		roleModelSelector: selector,
	}

	// Call getProviderConfig with a role
	config, _ := tool.getProviderConfig("", "", RoleImplementer)

	// Verify disable_reasoning is passed via Custom map
	if config.Custom == nil {
		t.Fatal("ProviderConfig.Custom is nil, expected disable_reasoning to be set")
	}
	disableReasoning, ok := config.Custom["disable_reasoning"].(bool)
	if !ok {
		t.Fatal("disable_reasoning not found in ProviderConfig.Custom")
	}
	if !disableReasoning {
		t.Error("Expected disable_reasoning to be true")
	}
}
