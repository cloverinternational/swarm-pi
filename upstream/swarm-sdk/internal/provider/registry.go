package provider

import "github.com/Swarm-Code/mono/swarm-sdk/internal/observability"

// Registry manages provider instances and creation.
// This is Ring 0 - interface only, implementation in Ring 1.
type Registry interface {
	// Register adds a provider factory to the registry.
	// The factory function creates provider instances from configuration.
	Register(name string, factory ProviderFactory) error

	// Get retrieves a provider by name.
	// Returns error if provider is not registered.
	Get(name string) (Provider, error)

	// List returns all registered provider names.
	List() []string

	// IsRegistered checks if a provider is registered.
	IsRegistered(name string) bool
}

// ProviderFactory creates provider instances from configuration.
type ProviderFactory func(config Config) (Provider, error)

// Config represents provider-specific configuration.
// Each provider implementation defines what fields it needs.
type Config struct {
	// Name is the provider identifier.
	Name string

	// APIKey is the authentication key.
	APIKey string

	// BaseURL is the API endpoint (optional, defaults to provider's standard URL).
	BaseURL string

	// NoAmbientEnv requires provider construction and runtime to use only
	// explicitly supplied configuration. The zero value preserves legacy
	// provider behavior.
	NoAmbientEnv bool

	// Model is the default model to use.
	Model string

	// ContextWindow is the selected model's resolved context window in tokens.
	// It takes precedence over provider-wide capabilities because one provider
	// can serve models with different limits. Zero means unknown.
	ContextWindow int

	// Timeout is the request timeout in seconds.
	Timeout int

	// MaxRetries is the maximum number of retry attempts.
	MaxRetries int
	// HTTPMaxRetries controls transport-level retries. Nil keeps provider
	// defaults; zero disables retries.
	HTTPMaxRetries *int

	// Logger is the observability logger. Defaults to a noop logger when nil.
	Logger observability.Logger

	// Tracer is the observability tracer. Defaults to a noop tracer when nil.
	Tracer observability.Tracer

	// Custom contains provider-specific configuration options not covered by the
	// fields above (e.g. "beta_headers", "is_oauth", "organization_id").
	// Do NOT use Custom["logger"] or Custom["tracer"] — use the typed fields above.
	Custom map[string]any
}
