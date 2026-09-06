package anthropic

import (
	"os"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/observability"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/observability/noop"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/provider"
)

// Register registers the Anthropic provider with the given registry.
// This should be called during initialization to make the provider available.
func Register(registry provider.Registry) error {
	return registry.Register("anthropic", NewFromRegistry)
}

// NewFromRegistry creates a new Anthropic provider from registry configuration.
// This is used by the provider registry for dynamic instantiation.
func NewFromRegistry(config provider.Config) (provider.Provider, error) {
	anthropicConfig := WithDefaults()

	// Extract API key from config or, unless explicitly sealed, environment.
	if config.APIKey != "" {
		anthropicConfig.APIKey = config.APIKey
	} else if !config.NoAmbientEnv {
		anthropicConfig.APIKey = os.Getenv("ANTHROPIC_API_KEY")
	}

	// Extract base URL
	if config.BaseURL != "" {
		anthropicConfig.BaseURL = config.BaseURL
	}

	// Extract default model
	if config.Model != "" {
		anthropicConfig.DefaultModel = config.Model
	}

	// Extract timeout
	if config.Timeout != 0 {
		anthropicConfig.Timeout = config.Timeout
	}

	// Extract max retries
	if config.MaxRetries > 0 {
		anthropicConfig.MaxRetries = config.MaxRetries
	}

	// Extract beta headers from custom config
	if betaHeaders, ok := config.Custom["beta_headers"].([]string); ok {
		anthropicConfig.BetaHeaders = betaHeaders
	}

	// Extract OAuth flag from custom config
	if isOAuth, ok := config.Custom["is_oauth"].(bool); ok {
		anthropicConfig.IsOAuth = isOAuth
	}
	// Extract account ID from custom config (used for per-account DeviceIdentity)
	if accountID, ok := config.Custom["account_id"].(string); ok {
		anthropicConfig.AccountID = accountID
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

	anthropicConfig.Logger = logger
	anthropicConfig.Tracer = tracer

	// Honour SAC_RAW_DUMP for indexer debugging. Explicit paths isolate
	// captures; boolean values retain the legacy /tmp/sac-raw.log behavior.
	if !config.NoAmbientEnv {
		anthropicConfig.RawDebugWriter = openRawDumpWriterFromEnv()
	}
	return newProvider(anthropicConfig, config.NoAmbientEnv)
}

// NewFromConfig creates a new Anthropic provider from a generic config map.
// This is a legacy method for backward compatibility.
func NewFromConfig(configMap map[string]any, logger observability.Logger, tracer observability.Tracer) (provider.Provider, error) {
	config := WithDefaults()

	// Extract API key from config or environment
	if apiKey, ok := configMap["api_key"].(string); ok && apiKey != "" {
		config.APIKey = apiKey
	} else {
		config.APIKey = os.Getenv("ANTHROPIC_API_KEY")
	}

	// Extract base URL
	if baseURL, ok := configMap["base_url"].(string); ok && baseURL != "" {
		config.BaseURL = baseURL
	}

	// Extract default model
	if model, ok := configMap["default_model"].(string); ok && model != "" {
		config.DefaultModel = model
	}

	// Extract timeout
	if timeout, ok := configMap["timeout"].(int); ok && timeout != 0 {
		config.Timeout = timeout
	}

	// Extract max retries
	if maxRetries, ok := configMap["max_retries"].(int); ok && maxRetries >= 0 {
		config.MaxRetries = maxRetries
	}

	// Extract beta headers
	if betaHeaders, ok := configMap["beta_headers"].([]string); ok {
		config.BetaHeaders = betaHeaders
	}

	// Extract OAuth flag
	if isOAuth, ok := configMap["is_oauth"].(bool); ok {
		config.IsOAuth = isOAuth
	}

	config.Logger = logger
	config.Tracer = tracer
	return New(config)
}
