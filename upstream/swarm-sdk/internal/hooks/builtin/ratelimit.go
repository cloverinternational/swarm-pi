package builtin

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/hooks"
)

// RateLimitHook limits the frequency of events.
type RateLimitHook struct {
	name     string
	priority int
	limit    int           // Events per interval
	interval time.Duration // Time interval
	buckets  map[string]*tokenBucket
	mu       sync.Mutex
}

// tokenBucket implements a simple token bucket algorithm.
type tokenBucket struct {
	tokens     float64
	lastUpdate time.Time
}

// NewRateLimitHook creates a new rate limit hook.
func NewRateLimitHook(limit int, interval time.Duration) *RateLimitHook {
	return &RateLimitHook{
		name:     "rate_limit",
		priority: 90,
		limit:    limit,
		interval: interval,
		buckets:  make(map[string]*tokenBucket),
	}
}

// Name returns the hook name.
func (h *RateLimitHook) Name() string {
	return h.name
}

// Priority returns the hook priority.
func (h *RateLimitHook) Priority() int {
	return h.priority
}

// Filter checks if the event should be processed.
func (h *RateLimitHook) Filter(event hooks.Event) bool {
	// Rate limit provider requests and tool executions
	return event.Type == hooks.EventProviderBeforeRequest || event.Type == hooks.EventToolBeforeExecute
}

// OnEvent processes the event.
func (h *RateLimitHook) OnEvent(ctx context.Context, event hooks.Event) (hooks.HookResult, error) {
	h.mu.Lock()
	defer h.mu.Unlock()

	// Identify the bucket key (e.g., AgentID or Global)
	key := event.AgentID
	if key == "" {
		key = "global"
	}

	bucket, exists := h.buckets[key]
	if !exists {
		bucket = &tokenBucket{
			tokens:     float64(h.limit),
			lastUpdate: time.Now(),
		}
		h.buckets[key] = bucket
	}

	// Refill tokens
	now := time.Now()
	elapsed := now.Sub(bucket.lastUpdate).Seconds()
	refillRate := float64(h.limit) / h.interval.Seconds()
	bucket.tokens += elapsed * refillRate
	if bucket.tokens > float64(h.limit) {
		bucket.tokens = float64(h.limit)
	}
	bucket.lastUpdate = now

	// Consume token
	if bucket.tokens >= 1.0 {
		bucket.tokens -= 1.0
		return hooks.Continue(), nil
	}

	return hooks.Block(fmt.Sprintf("Rate limit exceeded for %s", key)), nil
}
