package minimax

import (
	"testing"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/provider"
)

// TestConfigValidation tests that config validation works correctly.
func TestConfigValidation(t *testing.T) {
	tests := []struct {
		name      string
		config    Config
		expectErr bool
	}{
		{
			name: "empty api key",
			config: Config{
				APIKey:       "",
				BaseURL:      "https://api.minimax.io/anthropic",
				DefaultModel: "MiniMax-M2.5",
			},
			expectErr: true,
		},
		{
			name: "valid minimal config",
			config: Config{
				APIKey: "key-123",
			},
			expectErr: false,
		},
		{
			name: "full valid config",
			config: Config{
				APIKey:       "key-123",
				BaseURL:      "https://custom.minimax.io",
				DefaultModel: "MiniMax-M2.5",
				Timeout:      30,
				MaxTokens:    64000,
			},
			expectErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.config.Validate()
			if err != nil && !tt.expectErr {
				t.Errorf("unexpected error: %v", err)
			}
			if err == nil && tt.expectErr {
				t.Error("expected error but got nil")
			}
		})
	}
}

// TestDefaultModel tests that default model is set correctly.
func TestDefaultModel(t *testing.T) {
	if DefaultModel != "MiniMax-M2.5" {
		t.Errorf("expected default model 'MiniMax-M2.5', got %s", DefaultModel)
	}
}

// TestModelCapabilities tests that model capabilities are defined.
func TestModelCapabilities(t *testing.T) {
	if len(ModelCapabilities) == 0 {
		t.Error("expected model capabilities to be defined")
	}

	// Check that MiniMax-M2.5 is in the capabilities
	if _, ok := ModelCapabilities["MiniMax-M2.5"]; !ok {
		t.Error("expected MiniMax-M2.5 to be in model capabilities")
	}

	// Check properties
	caps := ModelCapabilities["MiniMax-M2.5"]
	if caps.MaxTokens <= 0 {
		t.Errorf("expected positive max tokens for MiniMax-M2.5, got %d", caps.MaxTokens)
	}
}

// TestTranslateTools tests tool translation.
func TestTranslateTools(t *testing.T) {
	t.Run("empty tools", func(t *testing.T) {
		got := translateTools(nil)
		if len(got) != 0 {
			t.Errorf("expected 0 tools for nil input, got %d", len(got))
		}
	})

	t.Run("maps fields", func(t *testing.T) {
		in := []provider.Tool{
			{
				Name:        "get_weather",
				Description: "Look up the weather",
				Parameters:  map[string]any{"type": "object"},
			},
		}
		got := translateTools(in)
		if len(got) != 1 {
			t.Fatalf("expected 1 translated tool, got %d", len(got))
		}
		if got[0].Name != "get_weather" {
			t.Errorf("Name: got %q want get_weather", got[0].Name)
		}
		if got[0].Description != "Look up the weather" {
			t.Errorf("Description: got %q", got[0].Description)
		}
		// Parameters must be carried into InputSchema unchanged.
		schema, ok := got[0].InputSchema.(map[string]any)
		if !ok || schema["type"] != "object" {
			t.Errorf("InputSchema not carried through: got %#v", got[0].InputSchema)
		}
	})
}
