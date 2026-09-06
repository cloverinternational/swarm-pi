package agent

import (
	"context"
	"testing"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/provider"
)

type testProviderNameOnly struct {
	name string
}

func (p testProviderNameOnly) Name() string { return p.name }

func (p testProviderNameOnly) Chat(context.Context, provider.ChatRequest) (*provider.ChatResponse, error) {
	return nil, nil
}

func (p testProviderNameOnly) Stream(context.Context, provider.ChatRequest) (<-chan provider.StreamChunk, error) {
	return nil, nil
}

func (p testProviderNameOnly) Capabilities() provider.Capabilities {
	return provider.Capabilities{}
}

func TestCodexSystemMessagesDisallowed(t *testing.T) {
	tests := []struct {
		name       string
		provider   provider.Provider
		model      string
		disallowed bool
	}{
		{
			name:       "codex model by name",
			provider:   testProviderNameOnly{name: "openai"},
			model:      "gpt-5.2-codex",
			disallowed: true,
		},
		{
			name:       "codex provider name",
			provider:   testProviderNameOnly{name: "codex"},
			model:      "gpt-5.2",
			disallowed: true,
		},
		{
			name:       "non codex model/provider",
			provider:   testProviderNameOnly{name: "openai"},
			model:      "gpt-5.2",
			disallowed: false,
		},
		{
			name:       "nil provider still checks model",
			provider:   nil,
			model:      "gpt-5.1-codex-max",
			disallowed: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := codexSystemMessagesDisallowed(tt.provider, tt.model)
			if got != tt.disallowed {
				t.Fatalf("codexSystemMessagesDisallowed()=%v want %v", got, tt.disallowed)
			}
		})
	}
}
