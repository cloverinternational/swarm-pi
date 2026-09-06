package http

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/observability"
)

// ExampleBasicRetry demonstrates the simplest usage of RetryableRequest.
// This example shows how to make an HTTP request with automatic retry logic.
func ExampleBasicRetry(logger observability.Logger) error {
	ctx := context.Background()

	// Create a basic HTTP request
	req, err := http.NewRequest(http.MethodGet, "https://api.example.com/data", nil)
	if err != nil {
		return err
	}

	// Create HTTP client with timeout
	client := &http.Client{Timeout: 30 * time.Second}

	// Wrap with retry logic
	rr := NewRetryableRequest(req, client, logger)

	// Execute with automatic retries
	resp, err := rr.Do(ctx)
	if err != nil {
		return fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	// Process response
	body, _ := io.ReadAll(resp.Body)
	fmt.Printf("Response: %s\n", string(body))

	return nil
}

// ExampleCustomBackoffPolicy demonstrates configuring custom exponential backoff.
// This example shows how to adjust retry behavior for different use cases.
func ExampleCustomBackoffPolicy(logger observability.Logger) error {
	ctx := context.Background()

	req, _ := http.NewRequest(http.MethodGet, "https://api.example.com/data", nil)
	client := &http.Client{Timeout: 30 * time.Second}

	rr := NewRetryableRequest(req, client, logger)

	// Customize the exponential backoff policy
	policy := NewExponentialBackoff(logger)
	policy.BaseDelay = 500 * time.Millisecond // Start with 500ms
	policy.MaxDelay = 1 * time.Minute         // Cap at 1 minute
	policy.MaxRetries = 5                     // Allow 5 retries (6 total attempts)
	policy.JitterFraction = 0.2               // 20% jitter

	rr.WithPolicy(policy)

	resp, err := rr.Do(ctx)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	return nil
}

// ExampleRetryCallback demonstrates using the OnRetry callback for monitoring.
// This example shows how to track retry attempts for logging/metrics.
func ExampleRetryCallback(logger observability.Logger) error {
	ctx := context.Background()

	req, _ := http.NewRequest(http.MethodGet, "https://api.example.com/data", nil)
	client := &http.Client{Timeout: 30 * time.Second}

	rr := NewRetryableRequest(req, client, logger)

	// Track retry attempts with callback
	retryAttempts := 0
	totalBackoffMs := int64(0)

	rr.WithOnRetry(func(attempt int, err error, nextDelay time.Duration) {
		retryAttempts++
		totalBackoffMs += nextDelay.Milliseconds()

		logger.Info(ctx, "http.request.retrying",
			observability.F("attempt", attempt+1),
			observability.F("error", err.Error()),
			observability.F("next_delay_ms", nextDelay.Milliseconds()),
		)
	})

	resp, err := rr.Do(ctx)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	logger.Info(ctx, "http.request.completed",
		observability.F("retries", retryAttempts),
		observability.F("total_backoff_ms", totalBackoffMs),
	)

	return nil
}

// ExampleContextTimeout demonstrates using context with overall timeout.
// This example shows how to set a deadline for the entire operation.
func ExampleContextTimeout(logger observability.Logger) error {
	// Create context with 5-minute timeout
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	req, _ := http.NewRequest(http.MethodGet, "https://api.example.com/data", nil)
	client := &http.Client{Timeout: 30 * time.Second}

	rr := NewRetryableRequest(req, client, logger)

	resp, err := rr.Do(ctx)
	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			logger.Warn(ctx, "http.request.timeout",
				observability.F("reason", "context deadline exceeded"),
			)
			return fmt.Errorf("request timeout after 5 minutes")
		}
		return err
	}
	defer resp.Body.Close()

	return nil
}

// ExampleCustomRetryPolicy demonstrates implementing custom retry logic.
// This example shows how to create a completely custom retry policy.
func ExampleCustomRetryPolicy(logger observability.Logger) error {
	ctx := context.Background()

	req, _ := http.NewRequest(http.MethodGet, "https://api.example.com/data", nil)
	client := &http.Client{Timeout: 30 * time.Second}

	// Create custom policy that:
	// - Uses linear backoff instead of exponential
	// - Only retries specific error types
	// - Has a maximum of 4 attempts
	policy := &CustomRetryPolicy{
		MaxAttemptsValue: 4,
		ShouldRetryFunc: func(ctx context.Context, err error, attempt int) bool {
			if err == nil {
				return false
			}

			// Only retry network-related errors
			errType := err.Error()
			retryableErrors := []string{
				"connection refused",
				"connection reset",
				"timeout",
				"temporary failure",
			}

			for _, retryable := range retryableErrors {
				if len(errType) >= len(retryable) &&
					errType[:len(retryable)] == retryable {
					return attempt < 3
				}
			}

			return false
		},
		BackoffDurationFunc: func(attempt int) time.Duration {
			// Linear backoff: 1s, 2s, 3s
			return time.Duration(attempt+1) * time.Second
		},
	}

	rr := NewRetryableRequest(req, client, logger)
	rr.WithPolicy(policy)

	resp, err := rr.Do(ctx)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	return nil
}

// ExampleConservativeRetryForFlakyServices demonstrates minimal retry configuration.
// This example shows how to configure retries for services known to be flaky.
func ExampleConservativeRetryForFlakyServices(logger observability.Logger) error {
	ctx := context.Background()

	req, _ := http.NewRequest(http.MethodGet, "https://flaky-service.example.com/data", nil)
	client := &http.Client{Timeout: 30 * time.Second}

	rr := NewRetryableRequest(req, client, logger)

	// Conservative: wait longer between attempts
	policy := NewExponentialBackoff(logger)
	policy.BaseDelay = 2 * time.Second // Start with 2s
	policy.MaxDelay = 30 * time.Second // Cap at 30s
	policy.MaxRetries = 2              // Only 2 retries (3 total attempts)
	policy.JitterFraction = 0.1

	rr.WithPolicy(policy)

	resp, err := rr.Do(ctx)
	if err != nil {
		logger.Error(ctx, "request.failed",
			observability.F("error", err.Error()),
		)
		return err
	}
	defer resp.Body.Close()

	return nil
}

// ExampleAggressiveRetryForCriticalRequests demonstrates maximum retry configuration.
// This example shows how to configure retries for critical operations.
func ExampleAggressiveRetryForCriticalRequests(logger observability.Logger) error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	req, _ := http.NewRequest(http.MethodPost, "https://api.example.com/critical-operation", nil)
	client := &http.Client{Timeout: 30 * time.Second}

	rr := NewRetryableRequest(req, client, logger)

	// Aggressive: many retries with short initial delay
	policy := NewExponentialBackoff(logger)
	policy.BaseDelay = 100 * time.Millisecond // Start fast
	policy.MaxDelay = 2 * time.Minute         // But cap reasonably
	policy.MaxRetries = 10                    // Many retries
	policy.JitterFraction = 0.15

	rr.WithPolicy(policy)

	resp, err := rr.Do(ctx)
	if err != nil {
		logger.Error(ctx, "critical.operation.failed",
			observability.F("error", err.Error()),
		)
		return err
	}
	defer resp.Body.Close()

	return nil
}

// ExampleRetryWithMetrics demonstrates integrating retries with metrics collection.
// This example shows how to track retry behavior for monitoring/alerting.
type MetricsCollector interface {
	IncrementCounter(name string, value int64)
	RecordHistogram(name string, value float64)
}

func ExampleRetryWithMetrics(logger observability.Logger, metrics MetricsCollector) error {
	ctx := context.Background()

	req, _ := http.NewRequest(http.MethodGet, "https://api.example.com/data", nil)
	client := &http.Client{Timeout: 30 * time.Second}

	rr := NewRetryableRequest(req, client, logger)

	// Track metrics during retries
	startTime := time.Now()
	retryCount := 0

	rr.WithOnRetry(func(attempt int, err error, nextDelay time.Duration) {
		retryCount++
		metrics.IncrementCounter("http_retries_total", 1)
		metrics.RecordHistogram("http_retry_delay_ms", float64(nextDelay.Milliseconds()))

		if attempt > 0 {
			metrics.IncrementCounter("http_retries_multiple", 1)
		}
	})

	resp, err := rr.Do(ctx)

	// Record completion metrics
	elapsed := time.Since(startTime).Milliseconds()
	metrics.RecordHistogram("http_request_total_time_ms", float64(elapsed))
	metrics.RecordHistogram("http_request_retry_count", float64(retryCount))

	if err != nil {
		metrics.IncrementCounter("http_requests_failed", 1)
		return err
	}

	metrics.IncrementCounter("http_requests_successful", 1)
	defer resp.Body.Close()

	return nil
}

// ExampleRetryWithCircuitBreaker demonstrates combining retry with circuit breaker pattern.
// This example shows how to use retries alongside circuit breaking.
type CircuitBreaker interface {
	Allow() bool
	RecordSuccess()
	RecordFailure()
}

func ExampleRetryWithCircuitBreaker(logger observability.Logger, breaker CircuitBreaker) error {
	ctx := context.Background()

	// Check circuit breaker first
	if !breaker.Allow() {
		return fmt.Errorf("circuit breaker open")
	}

	req, _ := http.NewRequest(http.MethodGet, "https://api.example.com/data", nil)
	client := &http.Client{Timeout: 30 * time.Second}

	rr := NewRetryableRequest(req, client, logger)

	resp, err := rr.Do(ctx)
	if err != nil {
		breaker.RecordFailure()
		return err
	}

	breaker.RecordSuccess()
	defer resp.Body.Close()

	return nil
}

// ExampleRetryWithRequestBody demonstrates retrying POST requests with body.
// This example shows how to handle body re-reading for retries.
func ExampleRetryWithRequestBody(logger observability.Logger, bodyData []byte) error {
	ctx := context.Background()

	// Create request with body
	req, _ := http.NewRequest(http.MethodPost, "https://api.example.com/submit", nil)

	// For proper retry support, create a new body reader each time
	// (HTTP client will handle this automatically if we use proper setup)
	req.Body = io.NopCloser(bytes.NewReader(bodyData))
	req.ContentLength = int64(len(bodyData))
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 30 * time.Second}

	rr := NewRetryableRequest(req, client, logger)

	resp, err := rr.Do(ctx)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	return nil
}

// ExampleMultipleEndpointsWithDifferentPolicies demonstrates different retry strategies
// for different API endpoints.
func ExampleMultipleEndpointsWithDifferentPolicies(logger observability.Logger) error {
	// Policy for stable, well-funded API
	stablePolicy := NewExponentialBackoff(logger)
	stablePolicy.BaseDelay = 100 * time.Millisecond
	stablePolicy.MaxRetries = 2

	// Policy for known-flaky service
	flakyPolicy := NewExponentialBackoff(logger)
	flakyPolicy.BaseDelay = 2 * time.Second
	flakyPolicy.MaxRetries = 1

	// Policy for critical infrastructure
	criticalPolicy := NewExponentialBackoff(logger)
	criticalPolicy.BaseDelay = 500 * time.Millisecond
	criticalPolicy.MaxRetries = 5

	// Use appropriate policy for each endpoint
	endpoints := []struct {
		name   string
		url    string
		policy RetryPolicy
	}{
		{"google-maps", "https://maps.googleapis.com/api/data", stablePolicy},
		{"flaky-service", "https://flaky.example.com/api/data", flakyPolicy},
		{"critical-db", "https://critical.example.com/api/data", criticalPolicy},
	}

	client := &http.Client{Timeout: 30 * time.Second}
	ctx := context.Background()

	for _, ep := range endpoints {
		req, _ := http.NewRequest(http.MethodGet, ep.url, nil)
		rr := NewRetryableRequest(req, client, logger)
		rr.WithPolicy(ep.policy)

		resp, err := rr.Do(ctx)
		if err != nil {
			logger.Error(ctx, "endpoint.request.failed",
				observability.F("endpoint", ep.name),
				observability.F("error", err.Error()),
			)
			continue
		}
		resp.Body.Close()

		logger.Info(ctx, "endpoint.request.succeeded",
			observability.F("endpoint", ep.name),
		)
	}

	return nil
}

// These examples can be compiled and run with:
//   go run ./provider/http examples.go
// (with appropriate setup code)

// Note: These are example functions demonstrating patterns.
// In real usage, you would:
// 1. Create actual HTTP requests with proper URLs
// 2. Handle errors appropriately
// 3. Process responses according to your needs
// 4. Integrate with your monitoring/logging systems
