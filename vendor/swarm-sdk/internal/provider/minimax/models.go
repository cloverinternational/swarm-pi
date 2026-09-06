package minimax

// Model definitions and token limits for MiniMax models.
// Based on: https://platform.minimax.io/docs/guides/models-intro

const (
	// ModelM25 is MiniMax-M2.5 - a SOTA large language model for real-world productivity.
	// Achieves 80.2% SWE-Bench Verified while completing tasks 37% faster than M2.1.
	// Best for: Agentic workflows, tool use, complex reasoning, coding.
	ModelM25 = "MiniMax-M2.5"

	// ModelM21 is MiniMax-M2.1 - an improved version of the M2 model.
	// Best for: Text generation, conversations, reasoning tasks.
	ModelM21 = "MiniMax-M2.1"

	// ModelM2Her is M2-her - designed for role-playing and multi-turn conversations.
	// Best for: Role-playing, dialogue scenarios, creative writing.
	ModelM2Her = "M2-her"
)

// ModelInfo contains metadata about a MiniMax model.
type ModelInfo struct {
	ID             string // Model identifier
	MaxTokens      int    // Maximum output tokens
	ContextWindow  int    // Context window size
	SupportsTools  bool   // Whether the model supports tool/function calling
	SupportsVision bool   // Whether the model supports vision/image input
}

// ModelCapabilities maps model IDs to their capabilities.
var ModelCapabilities = map[string]ModelInfo{
	ModelM25: {
		ID:             ModelM25,
		MaxTokens:      64000,
		ContextWindow:  204800, // 204.8K per platform.minimax.io model docs (M2.5, Feb 2026)
		SupportsTools:  true,
		SupportsVision: true,
	},
	ModelM21: {
		ID:             ModelM21,
		MaxTokens:      64000,
		ContextWindow:  204800, // 204.8K per platform.minimax.io model docs (same as M2.5)
		SupportsTools:  true,
		SupportsVision: true,
	},
	ModelM2Her: {
		ID:             ModelM2Her,
		MaxTokens:      32000,
		ContextWindow:  64000,
		SupportsTools:  false,
		SupportsVision: false,
	},
}

// GetModelInfo returns model information for the given model ID.
// If the model is not recognized, returns a default configuration.
func GetModelInfo(modelID string) ModelInfo {
	if info, ok := ModelCapabilities[modelID]; ok {
		return info
	}

	// Default configuration for unknown models
	return ModelInfo{
		ID:             modelID,
		MaxTokens:      64000,
		ContextWindow:  128000,
		SupportsTools:  true,
		SupportsVision: false,
	}
}

// IsMiniMaxModel checks if the given model ID is a known MiniMax model.
func IsMiniMaxModel(modelID string) bool {
	_, ok := ModelCapabilities[modelID]
	return ok
}
