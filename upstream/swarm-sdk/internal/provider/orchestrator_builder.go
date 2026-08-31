package provider

import (
	"fmt"
	"time"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/observability"
)

// OrchestratorBuilder provides a fluent API for building an Orchestrator.
type OrchestratorBuilder struct {
	providers []ProviderEntry
	config    OrchestratorConfig
	errors    []error
}

// NewOrchestratorBuilder creates a new OrchestratorBuilder.
func NewOrchestratorBuilder() *OrchestratorBuilder {
	return &OrchestratorBuilder{
		providers: nil,
		config: OrchestratorConfig{
			MaxRetriesPerProvider: 3,
			RetryAfterFallback:    30 * time.Second,
		},
	}
}

// AddProvider adds a provider to the orchestrator.
// The first provider added becomes the primary provider.
func (b *OrchestratorBuilder) AddProvider(name string, p Provider, maxRetries int) *OrchestratorBuilder {
	if name == "" {
		b.errors = append(b.errors, fmt.Errorf("provider name cannot be empty"))
		return b
	}
	if p == nil {
		b.errors = append(b.errors, fmt.Errorf("provider cannot be nil for '%s'", name))
		return b
	}
	if maxRetries < 0 {
		b.errors = append(b.errors, fmt.Errorf("maxRetries cannot be negative for '%s'", name))
		return b
	}

	b.providers = append(b.providers, ProviderEntry{
		Name:       name,
		Provider:   p,
		MaxRetries: maxRetries,
	})
	return b
}

// WithConfig sets the orchestrator configuration.
func (b *OrchestratorBuilder) WithConfig(cfg OrchestratorConfig) *OrchestratorBuilder {
	b.config = cfg
	return b
}

// WithMaxRetriesPerProvider sets the default max retries per provider.
func (b *OrchestratorBuilder) WithMaxRetriesPerProvider(retries int) *OrchestratorBuilder {
	if retries < 0 {
		b.errors = append(b.errors, fmt.Errorf("maxRetriesPerProvider cannot be negative"))
		return b
	}
	b.config.MaxRetriesPerProvider = retries
	return b
}

// WithRotateOnRateLimit sets whether to immediately rotate on rate limit.
func (b *OrchestratorBuilder) WithRotateOnRateLimit(rotate bool) *OrchestratorBuilder {
	b.config.RotateOnRateLimit = rotate
	return b
}

// WithRetryAfterFallback sets the default retry delay when provider doesn't specify one.
func (b *OrchestratorBuilder) WithRetryAfterFallback(d time.Duration) *OrchestratorBuilder {
	if d < 0 {
		b.errors = append(b.errors, fmt.Errorf("retryAfterFallback cannot be negative"))
		return b
	}
	b.config.RetryAfterFallback = d
	return b
}

// WithLogger sets the logger for the orchestrator.
func (b *OrchestratorBuilder) WithLogger(logger observability.Logger) *OrchestratorBuilder {
	b.config.Logger = logger
	return b
}

// OnRetry sets the retry callback for UI feedback.
func (b *OrchestratorBuilder) OnRetry(callback RetryCallback) *OrchestratorBuilder {
	b.config.OnRetry = callback
	return b
}

// OnProviderSwitch sets the provider switch callback.
func (b *OrchestratorBuilder) OnProviderSwitch(callback ProviderSwitchCallback) *OrchestratorBuilder {
	b.config.OnProviderSwitch = callback
	return b
}

// OnExhausted sets the callback for when all providers are exhausted.
func (b *OrchestratorBuilder) OnExhausted(callback ExhaustedCallback) *OrchestratorBuilder {
	b.config.OnExhausted = callback
	return b
}

// Build creates the Orchestrator with the configured settings.
// Returns an error if the configuration is invalid.
func (b *OrchestratorBuilder) Build() (*Orchestrator, error) {
	// Check for accumulated errors
	if len(b.errors) > 0 {
		return nil, fmt.Errorf("orchestrator builder errors: %v", b.errors)
	}

	// Validate providers
	if len(b.providers) == 0 {
		return nil, fmt.Errorf("at least one provider is required")
	}

	// Check for duplicate names
	names := make(map[string]bool)
	for _, p := range b.providers {
		if names[p.Name] {
			return nil, fmt.Errorf("duplicate provider name: %s", p.Name)
		}
		names[p.Name] = true
	}

	return NewOrchestrator(b.providers, b.config)
}

// MustBuild creates the Orchestrator or panics on error.
// Use only in initialization code where errors should be fatal.
func (b *OrchestratorBuilder) MustBuild() *Orchestrator {
	o, err := b.Build()
	if err != nil {
		panic(fmt.Sprintf("failed to build orchestrator: %v", err))
	}
	return o
}

// ProviderCount returns the number of providers currently added.
func (b *OrchestratorBuilder) ProviderCount() int {
	return len(b.providers)
}

// HasErrors returns true if there are accumulated builder errors.
func (b *OrchestratorBuilder) HasErrors() bool {
	return len(b.errors) > 0
}

// Errors returns all accumulated builder errors.
func (b *OrchestratorBuilder) Errors() []error {
	return b.errors
}
