package openai

import (
	"github.com/Swarm-Code/mono/swarm-sdk/internal/provider"
)

// capabilities returns the capabilities of the OpenAI provider.
// Model-specific capabilities will be loaded from configuration.
func (p *Provider) capabilities() provider.Capabilities {
	return provider.Capabilities{
		Streaming:       true,
		FunctionCalling: true,
		Vision:          true,
		// Provider-level fallback when no per-model window is configured.
		// Every current GPT-5.x model publishes a window above the SDK's 300K
		// default policy ceiling (GPT-5.4/5.5 publish 1.05M). Legacy models
		// (gpt-4o 128K, o3 200K)
		// should carry an explicit per-model context window in config — user
		// config always overrides this fallback.
		MaxContextWindow:     provider.DefaultContextWindowCap,
		MaxOutputTokens:      4096, // Default maximum (model-specific will come from config)
		SupportsSystemPrompt: true,
		SupportsTemperature:  true,
		SupportedModels:      []string{}, // Model list loaded from config
		PromptCaching:        false,      // OpenAI doesn't expose prompt caching
		SupportsJSON:         true,
	}
}
