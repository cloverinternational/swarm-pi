package http

import (
	"context"
	"testing"
	"time"

	sdkerr "github.com/Swarm-Code/mono/swarm-sdk/internal/sdkerr"
)

func TestRateLimitAwarePolicy_BackoffDurationWithError_RespectsRetryAfter(t *testing.T) {
	policy := NewRateLimitAwarePolicy(nil)

	// Create a transient error with RetryAfter
	err := sdkerr.Transient(
		"test.rate_limited", "",
		sdkerr.WithRetryAfter(45*time.Second),
	)

	// Should use the RetryAfter value (plus jitter)
	duration := policy.BackoffDurationWithError(0, err)

	// Allow for jitter (±10% of 45s = ±4.5s)
	minExpected := 40 * time.Second
	maxExpected := 50 * time.Second

	if duration < minExpected || duration > maxExpected {
		t.Errorf("expected duration between %v and %v, got %v", minExpected, maxExpected, duration)
	}
}

func TestRateLimitAwarePolicy_BackoffDurationWithError_FallbackToExponential(t *testing.T) {
	policy := NewRateLimitAwarePolicy(nil)

	// Create a transient error without RetryAfter
	err := sdkerr.Transient("test.error", "")

	// Should use exponential backoff
	duration := policy.BackoffDurationWithError(0, err)

	// Exponential backoff: attempt 0 = 1s base (with jitter)
	minExpected := 900 * time.Millisecond
	maxExpected := 1100 * time.Millisecond

	if duration < minExpected || duration > maxExpected {
		t.Errorf("expected duration between %v and %v, got %v", minExpected, maxExpected, duration)
	}
}

func TestRateLimitAwarePolicy_BackoffDurationWithError_EnforcesMinimum(t *testing.T) {
	policy := NewRateLimitAwarePolicy(nil)
	policy.MinimumDelay = 5 * time.Second

	// Create error with very short RetryAfter
	err := sdkerr.Transient(
		"test.rate_limited", "",
		sdkerr.WithRetryAfter(1*time.Second),
	)

	duration := policy.BackoffDurationWithError(0, err)

	// Should be at least minimum (minus jitter)
	minExpected := 4500 * time.Millisecond

	if duration < minExpected {
		t.Errorf("expected duration >= %v, got %v", minExpected, duration)
	}
}

func TestRateLimitAwarePolicy_BackoffDurationWithError_EnforcesMaximum(t *testing.T) {
	policy := NewRateLimitAwarePolicy(nil)
	policy.MaximumDelay = 30 * time.Second

	// Create error with very long RetryAfter
	err := sdkerr.Transient(
		"test.rate_limited", "",
		sdkerr.WithRetryAfter(10*time.Minute),
	)

	duration := policy.BackoffDurationWithError(0, err)

	// Should be at most maximum (plus jitter)
	maxExpected := 35 * time.Second

	if duration > maxExpected {
		t.Errorf("expected duration <= %v, got %v", maxExpected, duration)
	}
}

func TestRateLimitAwarePolicy_BackoffDurationWithError_NilError(t *testing.T) {
	policy := NewRateLimitAwarePolicy(nil)

	// Should fall back to exponential backoff
	duration := policy.BackoffDurationWithError(0, nil)

	minExpected := 900 * time.Millisecond
	maxExpected := 1100 * time.Millisecond

	if duration < minExpected || duration > maxExpected {
		t.Errorf("expected duration between %v and %v, got %v", minExpected, maxExpected, duration)
	}
}

func TestRateLimitAwarePolicy_BackoffDurationWithError_DisableProviderDelay(t *testing.T) {
	policy := NewRateLimitAwarePolicy(nil)
	policy.RespectProviderDelay = false

	// Create error with RetryAfter
	err := sdkerr.Transient(
		"test.rate_limited", "",
		sdkerr.WithRetryAfter(60*time.Second),
	)

	// Should ignore RetryAfter and use exponential
	duration := policy.BackoffDurationWithError(0, err)

	// Should be around 1s (exponential base) not 60s
	maxExpected := 2 * time.Second

	if duration > maxExpected {
		t.Errorf("expected duration <= %v (should ignore RetryAfter), got %v", maxExpected, duration)
	}
}

func TestRateLimitAwarePolicy_ShouldRetry_TransientError(t *testing.T) {
	policy := NewRateLimitAwarePolicy(nil)
	ctx := context.Background()

	err := sdkerr.Transient("test.rate_limited", "")

	if !policy.ShouldRetry(ctx, err, 0) {
		t.Error("expected ShouldRetry to return true for transient error")
	}
}

func TestRateLimitAwarePolicy_ShouldRetry_PermanentError(t *testing.T) {
	policy := NewRateLimitAwarePolicy(nil)
	ctx := context.Background()

	err := sdkerr.Permanent("test.auth_failed", "invalid credentials")

	if policy.ShouldRetry(ctx, err, 0) {
		t.Error("expected ShouldRetry to return false for permanent error")
	}
}

func TestRateLimitAwarePolicy_ShouldRetry_MaxAttemptsExceeded(t *testing.T) {
	policy := NewRateLimitAwarePolicy(nil)
	policy.MaxRetries = 3
	ctx := context.Background()

	err := sdkerr.Transient("test.rate_limited", "")

	// Should return false when attempts exceed MaxRetries
	if policy.ShouldRetry(ctx, err, 3) {
		t.Error("expected ShouldRetry to return false when max retries exceeded")
	}
}
