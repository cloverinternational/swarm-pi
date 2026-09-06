package profiling

import (
	"os"
	"sync"
)

// Factory creates CacheProfiler instances based on configuration.
type Factory struct{}

// NewProfiler creates a configured cache profiler.
// Behavior depends on environment variables:
//   - DISABLE_CACHE_PROFILING="1": Disables profiling (uses no-op profiler)
//   - Default (not set): Enables profiling with file logging
//
// Returns a CacheProfiler ready for use. Errors are handled gracefully:
// if file logging fails, profiling will still work in-memory.
func NewProfiler() CacheProfiler {
	disabled := os.Getenv("DISABLE_CACHE_PROFILING") == "1"
	if disabled {
		return NewNoOpProfiler()
	}

	// Create validator
	validator := NewValidator()

	// Try to wrap with logging
	logger, err := NewLoggingProfiler(validator)
	if err != nil {
		// Fall back to validator without logging
		return validator
	}

	return logger
}

// Registry maintains global profiler instances by conversation ID.
// This allows different profilers for different conversations.
type Registry struct {
	mu         sync.RWMutex
	profilers  map[string]CacheProfiler
	defaultKey string
}

var globalRegistry = &Registry{
	profilers:  make(map[string]CacheProfiler),
	defaultKey: "default",
}

// GetProfiler returns the profiler for a conversation.
// Creates one if it doesn't exist.
func GetProfiler(conversationID string) CacheProfiler {
	if conversationID == "" {
		conversationID = globalRegistry.defaultKey
	}

	globalRegistry.mu.RLock()
	if profiler, ok := globalRegistry.profilers[conversationID]; ok {
		globalRegistry.mu.RUnlock()
		return profiler
	}
	globalRegistry.mu.RUnlock()

	// Create new profiler
	profiler := NewProfiler()

	globalRegistry.mu.Lock()
	globalRegistry.profilers[conversationID] = profiler
	globalRegistry.mu.Unlock()

	return profiler
}

// SetProfiler sets the profiler for a conversation.
// Useful for testing or custom configurations.
func SetProfiler(conversationID string, profiler CacheProfiler) {
	if conversationID == "" {
		conversationID = globalRegistry.defaultKey
	}

	globalRegistry.mu.Lock()
	globalRegistry.profilers[conversationID] = profiler
	globalRegistry.mu.Unlock()
}

// ClearProfiler removes the profiler for a conversation.
func ClearProfiler(conversationID string) {
	if conversationID == "" {
		conversationID = globalRegistry.defaultKey
	}

	globalRegistry.mu.Lock()
	delete(globalRegistry.profilers, conversationID)
	globalRegistry.mu.Unlock()
}

// ClearAll removes all registered profilers.
func ClearAll() {
	globalRegistry.mu.Lock()
	globalRegistry.profilers = make(map[string]CacheProfiler)
	globalRegistry.mu.Unlock()
}
