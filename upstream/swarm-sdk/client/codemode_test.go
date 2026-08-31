package client

import (
	"testing"
	"time"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/tools/codemode"
)

// TestWithCodeModeDefault verifies that WithCodeMode() enables code mode
// and the option is stored correctly.
func TestWithCodeModeDefault(t *testing.T) {
	var o options
	WithCodeMode()(&o)

	if !o.enableCodeMode {
		t.Fatal("WithCodeMode should set enableCodeMode = true")
	}
	if o.codeModeConfig != nil {
		t.Fatal("WithCodeMode should leave codeModeConfig nil (use defaults)")
	}
}

// TestWithCodeModeConfig verifies that WithCodeModeConfig() stores the
// custom configuration.
func TestWithCodeModeConfig(t *testing.T) {
	cfg := &codemode.Config{
		Enabled:    true,
		Timeout:    30 * time.Second,
		MaxRetries: 5,
		Persist:    true,
		ToolNames:  []string{"grep", "bash"},
	}

	var o options
	WithCodeModeConfig(cfg)(&o)

	if !o.enableCodeMode {
		t.Fatal("WithCodeModeConfig should set enableCodeMode = true")
	}
	if o.codeModeConfig == nil {
		t.Fatal("WithCodeModeConfig should set codeModeConfig")
	}
	if o.codeModeConfig.Timeout != 30*time.Second {
		t.Errorf("expected 30s timeout, got %v", o.codeModeConfig.Timeout)
	}
	if o.codeModeConfig.MaxRetries != 5 {
		t.Errorf("expected 5 retries, got %d", o.codeModeConfig.MaxRetries)
	}
	if len(o.codeModeConfig.ToolNames) != 2 {
		t.Errorf("expected 2 tool names, got %d", len(o.codeModeConfig.ToolNames))
	}
}

// TestWithCodeModeInFullAgent verifies that WithCodeMode composes with
// other options without conflict.
func TestWithCodeModeInFullAgent(t *testing.T) {
	var o options
	WithCodeMode()(&o)
	WithApprovalMode("yolo")(&o)
	WithSystemPrompt("test prompt")(&o)

	if !o.enableCodeMode {
		t.Fatal("code mode should be enabled")
	}
	if o.approvalMode != "yolo" {
		t.Errorf("expected yolo, got %s", o.approvalMode)
	}
	if o.systemPrompt != "test prompt" {
		t.Errorf("expected 'test prompt', got %s", o.systemPrompt)
	}
}
