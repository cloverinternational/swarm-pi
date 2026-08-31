package settings

import (
	"testing"

	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/chat/commands"
)

// TestModelSettings_SetModel_NotifiesCallback tests that SetModel calls the callback
// to notify the app when a model is changed.
// This is critical for SDK synchronization - without this, the SDK doesn't know
// about model changes made through the settings page.
func TestModelSettings_SetModel_NotifiesCallback(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	// Create a model settings instance
	ms := NewModelSettings("ClaudeCode", "claude-opus-4-20250514")

	// Track callback invocations
	var callbackProvider, callbackModel string
	callbackCalled := false

	// Set the callback
	ms.SetOnModelSelect(func(provider, model string) {
		callbackCalled = true
		callbackProvider = provider
		callbackModel = model
	})

	// Change the model
	ms.SetModel("CustomProvider", "custom-model-123")

	// Verify callback was called
	if !callbackCalled {
		t.Fatal("SetModel did not call the onModelSelect callback - SDK won't be notified of model change!")
	}

	// Verify callback received correct values
	if callbackProvider != "CustomProvider" {
		t.Errorf("Expected provider 'CustomProvider', got '%s'", callbackProvider)
	}
	if callbackModel != "custom-model-123" {
		t.Errorf("Expected model 'custom-model-123', got '%s'", callbackModel)
	}
}

// TestModelSettings_SetModel_SavesConfig tests that SetModel saves to config
func TestModelSettings_SetModel_SavesConfig(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	// Create a model settings instance with a config manager
	cm, err := commands.NewConfigManager()
	if err != nil {
		t.Skipf("Cannot create config manager: %v", err)
	}

	ms := NewModelSettings("ClaudeCode", "claude-opus-4-20250514")
	ms.SetConfigManager(cm)

	// Set a no-op callback to avoid nil pointer
	ms.SetOnModelSelect(func(provider, model string) {})

	// Change the model
	ms.SetModel("TestProvider", "test-model")

	// Load config and verify
	config, err := cm.LoadConfig()
	if err != nil {
		t.Fatalf("Failed to load config: %v", err)
	}

	if config.CurrentProvider != "TestProvider" {
		t.Errorf("Expected provider 'TestProvider' in config, got '%s'", config.CurrentProvider)
	}
	if config.CurrentModel != "test-model" {
		t.Errorf("Expected model 'test-model' in config, got '%s'", config.CurrentModel)
	}
}

// TestModelSettings_SwitchBetweenProviders tests the full flow of switching
// from one provider to another and back.
func TestModelSettings_SwitchBetweenProviders(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	ms := NewModelSettings("ClaudeCode", "claude-opus-4-20250514")

	// Track all callback invocations
	var callHistory []struct {
		provider string
		model    string
	}

	ms.SetOnModelSelect(func(provider, model string) {
		callHistory = append(callHistory, struct {
			provider string
			model    string
		}{provider, model})
	})

	// Simulate the problematic scenario:
	// 1. Start with Claude Code
	// 2. Switch to custom provider
	// 3. Switch back to Claude Code

	// Step 1: Initial state is already Claude Code

	// Step 2: Switch to custom provider
	ms.SetModel("MyCustomProvider", "custom-model-x")

	// Step 3: Switch back to Claude Code
	ms.SetModel("ClaudeCode", "claude-opus-4-20250514")

	// Verify all switches were notified
	if len(callHistory) != 2 {
		t.Fatalf("Expected 2 callback calls, got %d", len(callHistory))
	}

	// Verify first switch to custom provider
	if callHistory[0].provider != "MyCustomProvider" || callHistory[0].model != "custom-model-x" {
		t.Errorf("First switch incorrect: got provider=%s model=%s",
			callHistory[0].provider, callHistory[0].model)
	}

	// Verify second switch back to Claude Code
	if callHistory[1].provider != "ClaudeCode" || callHistory[1].model != "claude-opus-4-20250514" {
		t.Errorf("Second switch incorrect: got provider=%s model=%s",
			callHistory[1].provider, callHistory[1].model)
	}
}
