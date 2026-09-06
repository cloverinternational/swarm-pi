// Package http provides HTTP client utilities with built-in observability integration.
package http

import "context"

// contextKey is a private type for context keys to avoid collisions.
type contextKey string

const (
	// contextKeyRetryAttempt stores the current retry attempt number (0-indexed).
	contextKeyRetryAttempt contextKey = "http.retry.attempt"

	// contextKeyRequestID stores the unique request identifier.
	contextKeyRequestID contextKey = "http.request.id"

	// contextKeyOperationName stores the operation name for this request.
	contextKeyOperationName contextKey = "http.operation.name"
)

// withRetryAttempt adds the retry attempt number to the context.
// This should only be used internally by the retry logic.
func withRetryAttempt(ctx context.Context, attempt int) context.Context {
	return context.WithValue(ctx, contextKeyRetryAttempt, attempt)
}

// GetRetryAttempt retrieves the retry attempt number from the context.
// Returns -1 if not found (indicating this is the first attempt, not a retry).
func GetRetryAttempt(ctx context.Context) int {
	if v := ctx.Value(contextKeyRetryAttempt); v != nil {
		if attempt, ok := v.(int); ok {
			return attempt
		}
	}
	return -1
}

// GetRequestID retrieves the request ID from the context.
// Returns empty string if not found.
func GetRequestID(ctx context.Context) string {
	if v := ctx.Value(contextKeyRequestID); v != nil {
		if id, ok := v.(string); ok {
			return id
		}
	}
	return ""
}

// GetOperationName retrieves the operation name from the context.
// Returns empty string if not found.
func GetOperationName(ctx context.Context) string {
	if v := ctx.Value(contextKeyOperationName); v != nil {
		if name, ok := v.(string); ok {
			return name
		}
	}
	return ""
}
