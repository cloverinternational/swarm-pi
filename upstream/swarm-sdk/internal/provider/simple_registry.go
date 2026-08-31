package provider

import (
	"fmt"
	"strings"
	"sync"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/observability"
)

// SimpleRegistry is a basic in-memory provider registry.
// This is Ring 1 implementation of the Registry interface.
type SimpleRegistry struct {
	mu        sync.RWMutex
	factories map[string]ProviderFactory
	aliases   map[string]string // alias → canonical name
	// compatibleWith maps provider names to their compatible base type,
	// e.g. "local" -> "openai" means "local" is OpenAI-compatible. This is
	// per-instance state so that isolated registries never share compatibility
	// mappings (multi-tenant embedding).
	compatibleWith map[string]string
	logger         observability.Logger
}

// NewSimpleRegistry creates a new provider registry.
func NewSimpleRegistry(logger observability.Logger) *SimpleRegistry {
	return &SimpleRegistry{
		factories:      make(map[string]ProviderFactory),
		aliases:        make(map[string]string),
		compatibleWith: make(map[string]string),
		logger:         logger,
	}
}

// RegisterAlias maps an alias to a registered provider name so that
// Create("ClaudeCode") resolves to the "anthropic" factory, etc.
// Both the alias and the target are stored lowercase for case-insensitive matching.
func (r *SimpleRegistry) RegisterAlias(alias, target string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.aliases[strings.ToLower(alias)] = target
}

// resolve maps a provider name to the canonical factory key.
// It tries: exact match → lowercase match → alias lookup.
func (r *SimpleRegistry) resolve(name string) (string, bool) {
	// 1. Exact match
	if _, ok := r.factories[name]; ok {
		return name, true
	}
	// 2. Case-insensitive match
	lower := strings.ToLower(name)
	if _, ok := r.factories[lower]; ok {
		return lower, true
	}
	// 3. Alias lookup
	if target, ok := r.aliases[lower]; ok {
		if _, ok := r.factories[target]; ok {
			return target, true
		}
	}
	return name, false
}

// Register adds a provider factory to the registry.
func (r *SimpleRegistry) Register(name string, factory ProviderFactory) error {
	if name == "" {
		return fmt.Errorf("provider name cannot be empty")
	}
	if factory == nil {
		return fmt.Errorf("provider factory cannot be nil")
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	if _, exists := r.factories[name]; exists {
		return fmt.Errorf("provider %q already registered", name)
	}

	r.factories[name] = factory

	if r.logger != nil {
		r.logger.Info(nil, "provider.registered",
			observability.F("provider", name),
		)
	}

	return nil
}

// Get retrieves a provider by name.
// Note: This method is deprecated in favor of Create() which requires configuration.
func (r *SimpleRegistry) Get(name string) (Provider, error) {
	r.mu.RLock()
	exists := r.factories[name] != nil
	r.mu.RUnlock()

	if !exists {
		return nil, fmt.Errorf("provider %q not registered", name)
	}

	// Provider creation requires configuration
	return nil, fmt.Errorf("provider creation requires configuration - use Create() instead")
}

// Create instantiates a provider with the given configuration.
func (r *SimpleRegistry) Create(config Config) (Provider, error) {
	if config.Name == "" {
		return nil, fmt.Errorf("provider name is required in config")
	}

	r.mu.RLock()
	resolved, exists := r.resolve(config.Name)
	var factory ProviderFactory
	if exists {
		factory = r.factories[resolved]
		config.Name = resolved // Use canonical name downstream
	}
	r.mu.RUnlock()

	if !exists {
		return nil, fmt.Errorf("provider %q not registered", config.Name)
	}

	provider, err := factory(config)
	if err != nil {
		if r.logger != nil {
			r.logger.Error(nil, "provider.creation_failed",
				observability.F("provider", config.Name),
				observability.F("error", err.Error()),
			)
		}
		return nil, fmt.Errorf("failed to create provider %q: %w", config.Name, err)
	}

	if r.logger != nil {
		r.logger.Info(nil, "provider.created",
			observability.F("provider", config.Name),
		)
	}

	return provider, nil
}

// List returns all registered provider names.
func (r *SimpleRegistry) List() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()

	names := make([]string, 0, len(r.factories))
	for name := range r.factories {
		names = append(names, name)
	}
	return names
}

// IsRegistered checks if a provider is registered (supports aliases and case-insensitive matching).
func (r *SimpleRegistry) IsRegistered(name string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()

	_, exists := r.resolve(name)
	return exists
}
