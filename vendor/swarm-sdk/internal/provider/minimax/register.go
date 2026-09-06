package minimax

import (
	"os"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/observability/noop"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/provider"
)

// ProviderFactory creates a new MiniMax provider from configuration.
func ProviderFactory(config provider.Config) (provider.Provider, error) {
	// Extract MiniMax-specific configuration
	minimaxConfig := Config{
		APIKey:       config.APIKey,
		BaseURL:      config.BaseURL,
		DefaultModel: config.Model,
		Timeout:      config.Timeout,
		MaxTokens:    64000,
	}

	// Load API key from environment if not set
	if minimaxConfig.APIKey == "" {
		minimaxConfig.APIKey = os.Getenv("MINIMAX_API_KEY")
	}

	// Resolve logger: use typed field or noop default.
	logger := config.Logger
	if logger == nil {
		logger = noop.NewLogger()
	}

	// Resolve tracer: use typed field or noop default.
	tracer := config.Tracer
	if tracer == nil {
		tracer = noop.NewTracer()
	}

	return New(minimaxConfig, logger, tracer)
}

// Register registers the MiniMax provider with the given registry.
func Register(registry *provider.SimpleRegistry) error {
	return registry.Register("minimax", ProviderFactory)
}
