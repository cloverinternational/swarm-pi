// Package quirks provides provider-specific adapters for OpenAI-compatible APIs.
package quirks

import (
	"fmt"
	"sync"
)

// Registry manages quirk adapters.
type Registry struct {
	mu       sync.RWMutex
	adapters map[string]Adapter
}

// NewRegistry creates a new quirk adapter registry.
func NewRegistry() *Registry {
	r := &Registry{
		adapters: make(map[string]Adapter),
	}

	// Register built-in adapters
	r.Register(NewPassthroughAdapter())

	return r
}

// Register adds a new adapter to the registry.
func (r *Registry) Register(adapter Adapter) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.adapters[adapter.Name()] = adapter
}

// Get retrieves an adapter by name.
func (r *Registry) Get(name string) (Adapter, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	adapter, ok := r.adapters[name]
	return adapter, ok
}

// GetOrDefault retrieves an adapter by name, or returns passthrough if not found.
func (r *Registry) Lookup(name string) Adapter {
	if name == "" {
		return r.adapters["passthrough"]
	}

	r.mu.RLock()
	defer r.mu.RUnlock()

	adapter, ok := r.adapters[name]
	if !ok {
		return r.adapters["passthrough"]
	}
	return adapter
}

// Has checks if an adapter exists.
func (r *Registry) Has(name string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	_, ok := r.adapters[name]
	return ok
}

// List returns all registered adapter names.
func (r *Registry) List() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()

	names := make([]string, 0, len(r.adapters))
	for name := range r.adapters {
		names = append(names, name)
	}
	return names
}

// MustGet retrieves an adapter by name or panics if not found.
func (r *Registry) MustGet(name string) Adapter {
	adapter, ok := r.Get(name)
	if !ok {
		panic(fmt.Sprintf("quirk adapter not found: %s", name))
	}
	return adapter
}
