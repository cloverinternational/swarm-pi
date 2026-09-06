package chat

import (
	"context"
	"testing"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/provider"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/chat/commands"
)

type generationCaptureProvider struct {
	last provider.ChatRequest
}

func (p *generationCaptureProvider) Name() string { return "capture" }

func (p *generationCaptureProvider) Chat(_ context.Context, req provider.ChatRequest) (*provider.ChatResponse, error) {
	p.last = req
	return &provider.ChatResponse{}, nil
}

func (p *generationCaptureProvider) Stream(_ context.Context, req provider.ChatRequest) (<-chan provider.StreamChunk, error) {
	p.last = req
	ch := make(chan provider.StreamChunk)
	close(ch)
	return ch, nil
}

func (p *generationCaptureProvider) Capabilities() provider.Capabilities {
	return provider.Capabilities{}
}

func TestGenerationSettingsProviderAppliesOnlyMatchingModelOverrides(t *testing.T) {
	zeroFloat := 0.0
	zeroInt := 0
	settings := newModelGenerationSettings("qwen3")
	settings.applyModel(commands.Provider{Name: "Ollama", APIType: "ollama"}, commands.ModelConfig{
		ID:          "qwen3",
		Temperature: &zeroFloat,
		MaxTokens:   65536,
		TopP:        &zeroFloat,
		TopK:        &zeroInt,
	})
	capture := &generationCaptureProvider{}
	wrapped := newGenerationSettingsProvider(capture, settings)

	if _, err := wrapped.Chat(context.Background(), provider.ChatRequest{Model: "qwen3"}); err != nil {
		t.Fatal(err)
	}
	if capture.last.Temperature == nil || *capture.last.Temperature != 0 {
		t.Fatalf("temperature = %v, want explicit zero", capture.last.Temperature)
	}
	if capture.last.MaxTokens == nil || *capture.last.MaxTokens != 65536 {
		t.Fatalf("max tokens = %v, want 65536", capture.last.MaxTokens)
	}
	if capture.last.TopP == nil || *capture.last.TopP != 0 {
		t.Fatalf("top_p = %v, want explicit zero", capture.last.TopP)
	}
	if capture.last.TopK == nil || *capture.last.TopK != 0 {
		t.Fatalf("top_k = %v, want explicit zero", capture.last.TopK)
	}

	inheritedTemperature := 0.7
	if _, err := wrapped.Chat(context.Background(), provider.ChatRequest{
		Model:       "other-model",
		Temperature: &inheritedTemperature,
	}); err != nil {
		t.Fatal(err)
	}
	if capture.last.Temperature == nil || *capture.last.Temperature != inheritedTemperature {
		t.Fatalf("mismatched model temperature = %v, want inherited %v", capture.last.Temperature, inheritedTemperature)
	}
	if capture.last.MaxTokens != nil || capture.last.TopP != nil || capture.last.TopK != nil {
		t.Fatalf("overrides leaked to mismatched model: %+v", capture.last)
	}
}

func TestGenerationSettingsProviderOmitsSamplingForOpenAIReasoningMode(t *testing.T) {
	temperature := 0.7
	topP := 0.9
	settings := newModelGenerationSettings("gpt-5")
	settings.applyModel(commands.Provider{Name: "OpenAI", APIType: "openai"}, commands.ModelConfig{
		ID:              "gpt-5",
		ThinkingEnabled: true,
		Temperature:     &temperature,
		TopP:            &topP,
		MaxTokens:       4096,
	})
	capture := &generationCaptureProvider{}
	wrapped := newGenerationSettingsProvider(capture, settings)

	for _, call := range []struct {
		name string
		run  func(provider.ChatRequest) error
	}{
		{
			name: "chat",
			run: func(req provider.ChatRequest) error {
				_, err := wrapped.Chat(context.Background(), req)
				return err
			},
		},
		{
			name: "stream",
			run: func(req provider.ChatRequest) error {
				_, err := wrapped.Stream(context.Background(), req)
				return err
			},
		},
	} {
		t.Run(call.name, func(t *testing.T) {
			forcedTemperature := 1.2
			forcedTopP := 0.8
			if err := call.run(provider.ChatRequest{
				Model:       "gpt-5",
				Temperature: &forcedTemperature,
				TopP:        &forcedTopP,
			}); err != nil {
				t.Fatal(err)
			}
			if capture.last.Temperature != nil || capture.last.TopP != nil {
				t.Fatalf("reasoning request retained unsupported sampling: %+v", capture.last)
			}
			if capture.last.MaxTokens == nil || *capture.last.MaxTokens != 4096 {
				t.Fatalf("max tokens = %v, want 4096", capture.last.MaxTokens)
			}
		})
	}

	settings.applyModel(commands.Provider{Name: "OpenAI", APIType: "openai"}, commands.ModelConfig{
		ID:              "gpt-5",
		ThinkingEnabled: false,
		Temperature:     &temperature,
		TopP:            &topP,
	})
	if _, err := wrapped.Stream(context.Background(), provider.ChatRequest{Model: "gpt-5"}); err != nil {
		t.Fatal(err)
	}
	if capture.last.Temperature == nil || *capture.last.Temperature != temperature ||
		capture.last.TopP == nil || *capture.last.TopP != topP {
		t.Fatalf("sampling overrides not restored with thinking disabled: %+v", capture.last)
	}
}
