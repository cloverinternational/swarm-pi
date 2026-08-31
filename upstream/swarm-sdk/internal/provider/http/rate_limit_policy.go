package http

import (
	"context"
	"time"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/observability"
	sdkerr "github.com/Swarm-Code/mono/swarm-sdk/internal/sdkerr"
)

// RateLimitAwarePolicy extends ExponentialBackoff with rate limit awareness.
// It respects RetryAfter durations from SDK errors when available,
// falling back to exponential backoff otherwise.
type RateLimitAwarePolicy struct {
	*ExponentialBackoff

	// RespectProviderDelay when true, uses RetryAfter from SDK errors
	// instead of calculated backoff. Default is true.
	RespectProviderDelay bool

	// MinimumDelay is the minimum delay to apply even when respecting
	// provider delays (prevents immediate retries). Default is 1 second.
	MinimumDelay time.Duration

	// MaximumDelay caps the delay even when respecting provider delays.
	// Default is 5 minutes (300 seconds).
	MaximumDelay time.Duration
}

// NewRateLimitAwarePolicy creates a new RateLimitAwarePolicy with sensible defaults.
func NewRateLimitAwarePolicy(logger observability.Logger) *RateLimitAwarePolicy {
	return &RateLimitAwarePolicy{
		ExponentialBackoff:   NewExponentialBackoff(logger),
		RespectProviderDelay: true,
		MinimumDelay:         1 * time.Second,
		MaximumDelay:         5 * time.Minute,
	}
}

// ShouldRetry extends the base policy to check for rate limit errors specifically.
func (p *RateLimitAwarePolicy) ShouldRetry(ctx context.Context, err error, attempt int) bool {
	// First check the base policy
	if !p.ExponentialBackoff.ShouldRetry(ctx, err, attempt) {
		return false
	}

	// Additional check: if it's a transient error, we should retry
	if sdkerr.IsTransient(err) {
		return true
	}

	return sdkerr.IsRetryable(err)
}

// BackoffDurationWithError calculates the backoff duration, respecting
// RetryAfter from SDK errors when available.
func (p *RateLimitAwarePolicy) BackoffDurationWithError(attempt int, err error) time.Duration {
	// Check if error has a RetryAfter duration
	if p.RespectProviderDelay && err != nil {
		if retryAfter := sdkerr.GetRetryAfter(err); retryAfter > 0 {
			// Apply bounds
			delay := min(max(retryAfter, p.MinimumDelay), p.MaximumDelay)

			// Add jitter to avoid thundering herd
			jitterRange := time.Duration(float64(delay) * p.JitterFraction)
			p.randomMu.Lock()
			jitter := time.Duration(p.random.Int63n(int64(jitterRange*2))) - jitterRange
			p.randomMu.Unlock()

			return delay + jitter
		}
	}

	// Fall back to exponential backoff
	return p.ExponentialBackoff.BackoffDuration(attempt)
}

// WithMinimumDelay sets the minimum delay for the policy.
func (p *RateLimitAwarePolicy) WithMinimumDelay(d time.Duration) *RateLimitAwarePolicy {
	p.MinimumDelay = d
	return p
}

// WithMaximumDelay sets the maximum delay for the policy.
func (p *RateLimitAwarePolicy) WithMaximumDelay(d time.Duration) *RateLimitAwarePolicy {
	p.MaximumDelay = d
	return p
}

// WithRespectProviderDelay sets whether to respect provider delays.
func (p *RateLimitAwarePolicy) WithRespectProviderDelay(respect bool) *RateLimitAwarePolicy {
	p.RespectProviderDelay = respect
	return p
}
