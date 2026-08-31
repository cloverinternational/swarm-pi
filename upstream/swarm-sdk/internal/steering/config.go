package steering

import (
	"errors"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/provider"
)

// ConfigFromModel creates steering config from a model name
// This is the easiest way to set up steering for downstream clients
func ConfigFromModel(modelName string, prov provider.Provider) *Config {
	if modelName == "" {
		return DefaultConfig()
	}
	return &Config{
		Enabled:          true,
		ModelName:        modelName,
		Provider:         prov,
		ModeBased:        true,
		EnableEfficiency: true,
		MaxFileReads:     3,
		LogDecisions:     true,
	}
}

// SetupFromModel is a convenience function that creates steering from a model name
func SetupFromModel(modelName string, prov provider.Provider) (*Steering, error) {
	if modelName != "" && prov == nil {
		return nil, errors.New("provider required when modelName is set")
	}
	cfg := ConfigFromModel(modelName, prov)
	return New(cfg)
}

// SetupWithModel creates steering with a specific model name
// This is the simplest way to get started with LLM-based steering
func SetupWithModel(modelName string, prov provider.Provider) (*Steering, error) {
	return SetupFromModel(modelName, prov)
}

// SetupModeOnly creates steering with mode-based rules only (no LLM)
// This is the lightest weight option - no extra API calls
func SetupModeOnly() (*Steering, error) {
	cfg := &Config{
		Enabled:          true,
		ModeBased:        true,
		EnableEfficiency: true,
		MaxFileReads:     3,
		LogDecisions:     false,
	}
	return New(cfg)
}
