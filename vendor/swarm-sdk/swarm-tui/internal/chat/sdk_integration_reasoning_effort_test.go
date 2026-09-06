package chat

import (
	"testing"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/provider"
)

func TestSDKIntegration_ShouldIncludeReasoningEffortInRequest(t *testing.T) {
	tests := []struct {
		name        string
		provider    string
		model       string
		effort      string
		wantInclude bool
	}{
		{
			name:        "anthropic never includes",
			provider:    "anthropic",
			model:       "claude-opus-4-20250514",
			effort:      provider.ReasoningEffortHigh,
			wantInclude: false,
		},
		{
			name:        "openai gpt-4 does not include",
			provider:    "openai",
			model:       "gpt-4-turbo",
			effort:      provider.ReasoningEffortHigh,
			wantInclude: false,
		},
		{
			name:        "openai gpt-5 auto omitted",
			provider:    "openai",
			model:       "gpt-5.1",
			effort:      provider.ReasoningEffortAuto,
			wantInclude: false,
		},
		{
			name:        "openai gpt-5 includes non-auto",
			provider:    "openai",
			model:       "gpt-5.1",
			effort:      provider.ReasoningEffortHigh,
			wantInclude: true,
		},
		{
			name:        "openrouter never includes",
			provider:    "openrouter",
			model:       "openai/gpt-5-turbo",
			effort:      provider.ReasoningEffortHigh,
			wantInclude: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sdk := &SDKIntegration{
				providerName:    tt.provider,
				currentModel:    tt.model,
				reasoningEffort: tt.effort,
			}

			if got := sdk.shouldIncludeReasoningEffortInRequest(); got != tt.wantInclude {
				t.Fatalf("shouldIncludeReasoningEffortInRequest()=%v want %v", got, tt.wantInclude)
			}
		})
	}
}

func TestFindProviderModelConfig_CaseInsensitiveMatch(t *testing.T) {
	providerCfg := &ProviderConfig{
		Name: "OpenAI",
		Models: []ProviderModel{
			{ID: "gpt-5.1"},
			{ID: "openai/gpt-4.1"},
		},
	}

	model := findProviderModelConfig(providerCfg, "GPT-5.1")
	if model == nil || model.ID != "gpt-5.1" {
		t.Fatalf("expected case-insensitive exact match, got %+v", model)
	}

	model = findProviderModelConfig(providerCfg, "openai/gpt-4.1")
	if model == nil || model.ID != "openai/gpt-4.1" {
		t.Fatalf("expected namespaced model match, got %+v", model)
	}
}
