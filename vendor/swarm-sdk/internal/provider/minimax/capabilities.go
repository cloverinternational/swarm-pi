package minimax

import (
	"github.com/Swarm-Code/mono/swarm-sdk/internal/provider"
)

// getCapabilities returns the capabilities for all MiniMax models.
func getCapabilities() provider.Capabilities {
	return provider.Capabilities{
		Streaming:            true,
		FunctionCalling:      true,
		Vision:               true,
		MaxContextWindow:     204800, // 204.8K per platform.minimax.io docs (M2.5/M2.1)
		MaxOutputTokens:      64000,
		SupportsSystemPrompt: true,
		SupportsTemperature:  true,
		SupportedModels: []string{
			ModelM25,
			ModelM21,
			ModelM2Her,
		},
		PromptCaching: false,
		SupportsJSON:  false,
	}
}
