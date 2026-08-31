package http

import (
	"context"
	"fmt"
	"math"
	"math/rand"
	"net/http"
	"sync"
	"time"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/observability"
	sdkerr "github.com/Swarm-Code/mono/swarm-sdk/internal/sdkerr"
)

// RetryPolicy defines the interface for retry decision-making.
// Implementations determine whether a request should be retried and how long to wait.
type RetryPolicy interface {
	// ShouldRetry determines if a request should be retried given an error and attempt number.
	// Returns true if the request should be retried, false otherwise.
	ShouldRetry(ctx context.Context, err error, attempt int) bool

	// BackoffDuration calculates the duration to wait before the next retry attempt.
	// attempt is 0-indexed (0 for first retry, 1 for second, etc.)
	BackoffDuration(attempt int) time.Duration

	// MaxAttempts returns the maximum number of attempts (including the initial attempt).
	MaxAttempts() int
}

// ExponentialBackoff implements RetryPolicy with exponential backoff and jitter.
// It respects Retry-After headers from responses and only retries transient errors.
type ExponentialBackoff struct {
	// BaseDelay is the initial delay for the first retry (default: 1 second).
	BaseDelay time.Duration

	// MaxDelay is the maximum delay between retries (default: 32 seconds).
	MaxDelay time.Duration

	// MaxRetries is the maximum number of retries (default: 3, total attempts = 4).
	MaxRetries int

	// JitterFraction is the fraction of delay to add as random jitter (default: 0.1 = 10%).
	JitterFraction float64

	// Logger is used for logging retry attempts.
	Logger observability.Logger

	// random is used for jitter calculation (seeded for reproducibility in tests).
	// Protected by randomMu for thread-safe concurrent access.
	random *rand.Rand

	// randomMu protects random from concurrent access
	randomMu sync.Mutex
}

// NewExponentialBackoff creates a new ExponentialBackoff retry policy with sensible defaults.
func NewExponentialBackoff(logger observability.Logger) *ExponentialBackoff {
	return &ExponentialBackoff{
		BaseDelay:      1 * time.Second,
		MaxDelay:       32 * time.Second,
		MaxRetries:     3,
		JitterFraction: 0.1,
		Logger:         logger,
		random:         rand.New(rand.NewSource(time.Now().UnixNano())),
	}
}

// ShouldRetry implements RetryPolicy.ShouldRetry.
// Retries if:
// - Error is retryable (sdkerr.IsRetryable returns true)
// - We haven't exceeded max attempts
// - The error is a transient error (network, timeout, rate limit)
func (eb *ExponentialBackoff) ShouldRetry(ctx context.Context, err error, attempt int) bool {
	// Don't retry if we've exhausted attempts
	if attempt >= eb.MaxRetries {
		return false
	}

	// Don't retry if error is not retryable
	if !sdkerr.IsRetryable(err) {
		return false
	}

	// Don't retry permanent errors
	if sdkerr.IsPermanent(err) {
		return false
	}

	return true
}

// BackoffDuration implements RetryPolicy.BackoffDuration.
// Calculates exponential backoff with jitter:
// - Attempt 0: baseDelay * (2^0) = baseDelay
// - Attempt 1: baseDelay * (2^1) = baseDelay * 2
// - Attempt 2: baseDelay * (2^2) = baseDelay * 4
// - Jitter: ±(delay * jitterFraction)
// This method is thread-safe and can be called concurrently.
func (eb *ExponentialBackoff) BackoffDuration(attempt int) time.Duration {
	// Calculate exponential component: 2^attempt
	exponentialComponent := math.Pow(2, float64(attempt))

	// Calculate base duration
	baseDuration := min(
		// Cap at max delay
		time.Duration(float64(eb.BaseDelay)*exponentialComponent), eb.MaxDelay)

	// Add jitter: random value between -jitter and +jitter
	// Lock random access for thread safety
	jitterRange := time.Duration(float64(baseDuration) * eb.JitterFraction)

	eb.randomMu.Lock()
	jitter := time.Duration(eb.random.Int63n(int64(jitterRange*2))) - jitterRange
	eb.randomMu.Unlock()

	return baseDuration + jitter
}

// MaxAttempts implements RetryPolicy.MaxAttempts.
// Returns MaxRetries + 1 (because MaxRetries is the number of retries, not total attempts).
func (eb *ExponentialBackoff) MaxAttempts() int {
	return eb.MaxRetries + 1
}

// RetryableRequest wraps an HTTP request and applies retry logic to it.
type RetryableRequest struct {
	// Request is the underlying HTTP request to execute.
	Request *http.Request

	// Client is the HTTP client used to execute the request.
	Client *http.Client

	// Policy determines retry behavior.
	Policy RetryPolicy

	// Logger is used for logging retry attempts.
	Logger observability.Logger

	// OnRetry is an optional callback invoked before each retry.
	OnRetry func(attempt int, err error, nextDelay time.Duration)
}

// NewRetryableRequest creates a new RetryableRequest with default retry policy.
func NewRetryableRequest(req *http.Request, client *http.Client, logger observability.Logger) *RetryableRequest {
	return &RetryableRequest{
		Request: req,
		Client:  client,
		Policy:  NewExponentialBackoff(logger),
		Logger:  logger,
	}
}

// Do executes the request with retry logic applied.
// It performs the following algorithm:
//  1. Execute the request
//  2. If successful (no error), return the response
//  3. If error:
//     a. Check if error is retryable using the policy
//     b. If not retryable, return the error
//     c. If retryable and attempts remain:
//     - Log the retry attempt
//     - Wait for backoff duration
//     - Execute the request again
//     d. After max attempts, return the last error
//
// Special handling:
// - If response has Retry-After header, respect it over calculated backoff
// - Only retries on transient errors (network, timeout, rate limit)
// - Logs each retry with attempt number, delay, and error reason
func (rr *RetryableRequest) Do(ctx context.Context) (*http.Response, error) {
	var lastErr error
	var lastResponse *http.Response

	for attempt := 0; attempt < rr.Policy.MaxAttempts(); attempt++ {
		// Add attempt context to request using typed context key
		requestCtx := withRetryAttempt(ctx, attempt)
		rr.Request = rr.Request.WithContext(requestCtx)

		// Execute request
		resp, err := rr.Client.Do(rr.Request)

		// If no error and status is successful, return immediately
		if err == nil && resp != nil && resp.StatusCode < 400 {
			return resp, nil
		}

		// Store response for potential return
		lastResponse = resp
		lastErr = err

		// Check if we should retry
		shouldRetry := false
		var retryErr error

		if err != nil {
			shouldRetry = rr.Policy.ShouldRetry(ctx, err, attempt)
			retryErr = err
		} else if resp != nil && resp.StatusCode >= 500 {
			// Treat 5xx errors as transient and retryable
			shouldRetry = attempt < rr.Policy.MaxAttempts()-1
			retryErr = fmt.Errorf("HTTP %d: %s", resp.StatusCode, resp.Status)
		}

		// If we're not retrying, return what we have
		if !shouldRetry {
			if err != nil {
				return nil, err
			}
			return resp, nil
		}

		// If we're at the last attempt, don't retry
		if attempt >= rr.Policy.MaxAttempts()-1 {
			if err != nil {
				return nil, err
			}
			return resp, nil
		}

		// Calculate backoff duration
		backoffDuration := rr.Policy.BackoffDuration(attempt)

		// Check for Retry-After header (respects server's retry timing)
		if resp != nil {
			retryAfter := resp.Header.Get("Retry-After")
			if retryAfter != "" {
				// Try to parse as seconds
				if seconds := parseRetryAfter(retryAfter); seconds > 0 {
					backoffDuration = time.Duration(seconds) * time.Second
				}
			}
		}

		// Log the retry attempt
		rr.logRetry(ctx, attempt, retryErr, backoffDuration)

		// Invoke callback if provided
		if rr.OnRetry != nil {
			rr.OnRetry(attempt, retryErr, backoffDuration)
		}

		// Wait for backoff duration (respecting context cancellation)
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(backoffDuration):
			// Continue to next attempt
		}
	}

	// Return last error or response
	if lastErr != nil {
		return nil, lastErr
	}
	return lastResponse, nil
}

// logRetry logs a retry attempt with structured information.
func (rr *RetryableRequest) logRetry(ctx context.Context, attempt int, err error, nextDelay time.Duration) {
	if rr.Logger == nil {
		return
	}

	fields := []observability.Field{
		{Key: "attempt", Value: attempt + 1},
		{Key: "next_delay_ms", Value: nextDelay.Milliseconds()},
		{Key: "method", Value: rr.Request.Method},
		{Key: "url", Value: rr.Request.URL.String()},
	}

	if err != nil {
		fields = append(fields, observability.F("error", err.Error()))
		if sdkErr := sdkerr.GetType(err); sdkErr != "" {
			fields = append(fields, observability.F("error_type", sdkErr))
		}
	}

	rr.Logger.Info(ctx, "provider.http.retry", fields...)
}

// parseRetryAfter attempts to parse a Retry-After header value.
// Returns the number of seconds to wait, or 0 if parsing fails.
func parseRetryAfter(retryAfter string) int64 {
	// Try to parse as integer (seconds)
	var seconds int64
	if _, err := fmt.Sscanf(retryAfter, "%d", &seconds); err == nil && seconds > 0 {
		return seconds
	}

	// Could also parse as HTTP-date format (RFC7231), but for simplicity
	// we just handle the seconds case which is most common

	return 0
}

// WithPolicy returns a new RetryableRequest with a custom retry policy.
func (rr *RetryableRequest) WithPolicy(policy RetryPolicy) *RetryableRequest {
	rr.Policy = policy
	return rr
}

// WithOnRetry returns a new RetryableRequest with a callback for retry events.
func (rr *RetryableRequest) WithOnRetry(callback func(attempt int, err error, nextDelay time.Duration)) *RetryableRequest {
	rr.OnRetry = callback
	return rr
}

// CustomRetryPolicy allows fine-grained control over retry behavior.
// It can be embedded in other policies or used directly.
type CustomRetryPolicy struct {
	// ShouldRetryFunc is called to determine if a request should be retried.
	ShouldRetryFunc func(ctx context.Context, err error, attempt int) bool

	// BackoffDurationFunc calculates the delay before the next attempt.
	BackoffDurationFunc func(attempt int) time.Duration

	// MaxAttemptsValue is the maximum number of attempts.
	MaxAttemptsValue int
}

// ShouldRetry implements RetryPolicy.ShouldRetry.
func (crp *CustomRetryPolicy) ShouldRetry(ctx context.Context, err error, attempt int) bool {
	if crp.ShouldRetryFunc != nil {
		return crp.ShouldRetryFunc(ctx, err, attempt)
	}
	return false
}

// BackoffDuration implements RetryPolicy.BackoffDuration.
func (crp *CustomRetryPolicy) BackoffDuration(attempt int) time.Duration {
	if crp.BackoffDurationFunc != nil {
		return crp.BackoffDurationFunc(attempt)
	}
	return 0
}

// MaxAttempts implements RetryPolicy.MaxAttempts.
func (crp *CustomRetryPolicy) MaxAttempts() int {
	return crp.MaxAttemptsValue
}
