// Package cache defines the interface for multi-layer caching.
// This is Ring 0 - pure interface definitions with no implementations.
package cache

import (
	"context"
	"strings"
	"time"
)

// Cache defines the interface for caching operations.
// Implementations support different backends (in-memory, Redis, etc.)
type Cache interface {
	// Get retrieves a value from the cache.
	// Returns nil if the key doesn't exist or has expired.
	Get(ctx context.Context, key string) ([]byte, error)

	// Set stores a value in the cache with the given TTL.
	// If TTL is 0, the value never expires (use with caution).
	Set(ctx context.Context, key string, value []byte, ttl time.Duration) error

	// Delete removes a value from the cache.
	// No error if key doesn't exist (idempotent).
	Delete(ctx context.Context, key string) error

	// Clear removes all entries from the cache.
	Clear(ctx context.Context) error

	// Exists checks if a key exists in the cache.
	Exists(ctx context.Context, key string) (bool, error)

	// GetWithTTL retrieves a value and its remaining TTL.
	// Returns nil, 0 if key doesn't exist.
	GetWithTTL(ctx context.Context, key string) ([]byte, time.Duration, error)

	// SetNX sets a value only if the key doesn't exist.
	// Returns true if the value was set, false if key already exists.
	SetNX(ctx context.Context, key string, value []byte, ttl time.Duration) (bool, error)

	// Close closes the cache backend and releases resources.
	Close() error
}

// CacheLayer identifies different cache layers in the system.
type CacheLayer string

const (
	// LayerPrompt is provider-native prompt caching (L1).
	LayerPrompt CacheLayer = "prompt"

	// LayerResponse is SDK-level response caching (L2).
	LayerResponse CacheLayer = "response"

	// LayerTool is tool result caching (L3).
	LayerTool CacheLayer = "tool"

	// LayerContext is context window caching (L4).
	LayerContext CacheLayer = "context"

	// LayerMode is mode definition caching (L5).
	LayerMode CacheLayer = "mode"
)

// CacheConfig provides configuration for cache instances.
type CacheConfig struct {
	// Enabled indicates if caching is enabled.
	Enabled bool

	// Backend is the cache backend type.
	// Supported: "memory", "redis", "memcached"
	Backend string

	// MaxMemoryMB is the maximum memory to use (for in-memory caches).
	MaxMemoryMB int

	// MaxEntries is the maximum number of entries.
	MaxEntries int

	// DefaultTTL is the default time-to-live for entries.
	DefaultTTL time.Duration

	// EvictionPolicy determines how entries are evicted.
	// Supported: "lru", "lfu", "fifo", "random"
	EvictionPolicy string

	// ConnectionString is the backend connection string (for Redis, etc.).
	ConnectionString string
}

// CacheMetrics provides statistics about cache performance.
type CacheMetrics struct {
	// Layer identifies which cache layer these metrics are for.
	Layer CacheLayer

	// HitCount is the total number of cache hits.
	HitCount int64

	// MissCount is the total number of cache misses.
	MissCount int64

	// HitRate is the cache hit rate (0.0 to 1.0).
	HitRate float64

	// SizeBytes is the current cache size in bytes.
	SizeBytes int64

	// EntryCount is the current number of entries.
	EntryCount int64

	// EvictionCount is the total number of evictions.
	EvictionCount int64

	// AverageLookupTimeMS is the average lookup time in milliseconds.
	AverageLookupTimeMS float64
}

// MetricsProvider provides cache statistics.
// This is optional - implementations may return nil if not supported.
type MetricsProvider interface {
	Metrics(ctx context.Context) (*CacheMetrics, error)
}

// CacheKey generates standardized cache keys for different operations.
type CacheKey struct {
	// Layer identifies the cache layer.
	Layer CacheLayer

	// Components are the parts that make up the key.
	Components []string
}

// String returns the cache key as a string.
// Format: "layer:component1:component2:..."
func (k CacheKey) String() string {
	var result strings.Builder
	result.WriteString(string(k.Layer))
	for _, component := range k.Components {
		result.WriteString(":" + component)
	}
	return result.String()
}

// CacheEntry represents a cached entry with metadata.
type CacheEntry struct {
	// Key is the cache key.
	Key string

	// Value is the cached value.
	Value []byte

	// CreatedAt is when the entry was created (Unix timestamp).
	CreatedAt int64

	// ExpiresAt is when the entry expires (Unix timestamp).
	// 0 means never expires.
	ExpiresAt int64

	// AccessCount is how many times this entry has been accessed.
	AccessCount int64

	// LastAccessedAt is when the entry was last accessed (Unix timestamp).
	LastAccessedAt int64

	// SizeBytes is the size of the value in bytes.
	SizeBytes int64
}
