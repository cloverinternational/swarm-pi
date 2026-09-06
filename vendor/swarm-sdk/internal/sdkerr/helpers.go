package sdkerr

import (
	"context"
	"errors"
	"strings"
	"time"
)

// transientTransportPatterns matches common network/transport level errors that
// are retryable because they're temporary (server reset, timeout, refused, etc.)
var transientTransportPatterns = []string{
	"connection reset",
	"connection refused",
	"i/o timeout",
	"dial tcp",
	"no such host",
	"network is unreachable",
	"tls handshake",
	"broken pipe",
	"use of closed",
	"context deadline",
	"unexpected eof",
	"eof",
}

// contextLengthPatterns matches provider errors that indicate the prompt/context
// exceeded the model's maximum input length.  These are permanent errors that
// should NOT be retried with the same content — compaction must happen first.
var contextLengthPatterns = []string{
	"prompt is too long",
	"context_length_exceeded",
	"maximum context length",
	"context window",
	"too many tokens",
	"token limit exceeded",
	"input too long",
	"request too large",
	"reduce the length",
}

// IsContextLengthError returns true when the error signals that the prompt
// exceeded the model's context window.  Unlike transient transport errors,
// retrying with identical content will always fail; the caller must compact
// or truncate the conversation before making another request.
func IsContextLengthError(err error) bool {
	if err == nil {
		return false
	}
	// Native SDK errors may carry an explicit code.
	if code := GetCode(err); code != "" {
		if strings.HasSuffix(code, ".context_length_exceeded") ||
			strings.HasSuffix(code, ".prompt_too_long") {
			return true
		}
	}
	msg := strings.ToLower(err.Error())
	for _, pattern := range contextLengthPatterns {
		if strings.Contains(msg, pattern) {
			return true
		}
	}
	return false
}

// isTransientTransportError checks if a raw Go error represents a transient
// network/transport failure that should be retried.
func isTransientTransportError(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	for _, pattern := range transientTransportPatterns {
		if strings.Contains(msg, pattern) {
			return true
		}
	}
	return false
}

// GetCategory returns SDK category, inferring from context/stdlib errors when
// the error is not a native *sdkerror.Error. Context cancellation and deadline
// errors are classified as transient; transport-level errors (connection reset,
// timeout, refused, etc.) are also transient. Everything else defaults to permanent.
func GetCategory(err error) Category {
	sdkErr := asError(err)
	if sdkErr != nil {
		return sdkErr.Category
	}
	// Context errors are transient — the operation may succeed if retried.
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return CategoryTransient
	}
	// Transport-level errors (connection reset, timeout, etc.) are transient
	if isTransientTransportError(err) {
		return CategoryTransient
	}
	return CategoryPermanent
}

// IsRetryable returns true if the error is retryable.
// Native SDK errors use their Retryable flag; raw transport errors are also
// retryable to handle provider-side connection resets (e.g. Anthropic throttle).
func IsRetryable(err error) bool {
	sdkErr := asError(err)
	if sdkErr != nil {
		return sdkErr.Retryable
	}
	// Raw transport errors (not wrapped as sdkerr.Error) are still retryable
	return isTransientTransportError(err)
}

// IsTransient reports transient category.
func IsTransient(err error) bool {
	return GetCategory(err) == CategoryTransient
}

// IsPermanent reports permanent category.
func IsPermanent(err error) bool {
	return GetCategory(err) == CategoryPermanent
}

// IsSteering reports steering category.
func IsSteering(err error) bool {
	return GetCategory(err) == CategorySteering
}

// IsCritical reports critical category.
func IsCritical(err error) bool {
	return GetCategory(err) == CategoryCritical
}

// GetType returns legacy type (alias of Code) for compatibility.
func GetType(err error) string {
	sdkErr := asError(err)
	if sdkErr == nil {
		return ""
	}
	if sdkErr.Code != "" {
		return sdkErr.Code
	}
	return sdkErr.Code
}

// GetCode returns canonical code.
func GetCode(err error) string {
	sdkErr := asError(err)
	if sdkErr == nil {
		return ""
	}
	return sdkErr.Code
}

// GetErrorID returns canonical error ID.
func GetErrorID(err error) string {
	sdkErr := asError(err)
	if sdkErr == nil {
		return ""
	}
	return sdkErr.ErrorID
}

// GetContext returns attributes map.
func GetContext(err error) map[string]any {
	sdkErr := asError(err)
	if sdkErr == nil {
		return nil
	}
	return sdkErr.Attributes
}

// GetAttributes returns attributes map.
func GetAttributes(err error) map[string]any {
	return GetContext(err)
}

// GetTraceID returns trace ID.
func GetTraceID(err error) string {
	sdkErr := asError(err)
	if sdkErr == nil {
		return ""
	}
	return sdkErr.TraceID
}

// GetSpanID returns span ID.
func GetSpanID(err error) string {
	sdkErr := asError(err)
	if sdkErr == nil {
		return ""
	}
	return sdkErr.SpanID
}

// GetParentErrorID returns parent error ID.
func GetParentErrorID(err error) string {
	sdkErr := asError(err)
	if sdkErr == nil {
		return ""
	}
	return sdkErr.ParentErrorID
}

// GetRetryAfter returns retry delay.
func GetRetryAfter(err error) time.Duration {
	sdkErr := asError(err)
	if sdkErr == nil {
		return 0
	}
	return sdkErr.RetryAfter
}

// GetOperation returns operation metadata.
func GetOperation(err error) string {
	sdkErr := asError(err)
	if sdkErr == nil {
		return ""
	}
	return sdkErr.Operation
}

// GetComponent returns component metadata.
func GetComponent(err error) string {
	sdkErr := asError(err)
	if sdkErr == nil {
		return ""
	}
	return sdkErr.Component
}

// GetOccurredAt returns canonical timestamp.
func GetOccurredAt(err error) time.Time {
	sdkErr := asError(err)
	if sdkErr == nil {
		return time.Time{}
	}
	return sdkErr.OccurredAt
}

// GetFile returns the source file where the error was created, or empty string.
func GetFile(err error) string {
	sdkErr := asError(err)
	if sdkErr == nil {
		return ""
	}
	return sdkErr.File
}

// GetLine returns the source line number where the error was created, or 0.
func GetLine(err error) int {
	sdkErr := asError(err)
	if sdkErr == nil {
		return 0
	}
	return sdkErr.Line
}

// IsRateLimitError checks if the error is a rate limit or quota exceeded error.
// Uses suffix matching on the error code to be provider-agnostic.
func IsRateLimitError(err error) bool {
	code := GetCode(err)
	return strings.HasSuffix(code, ".rate_limited") || strings.HasSuffix(code, ".quota_exceeded")
}

// IsAuthError checks if the error is an authentication or authorization error.
// Matches unauthorized, forbidden, and generic auth_error suffixes.
func IsAuthError(err error) bool {
	code := GetCode(err)
	return strings.HasSuffix(code, ".unauthorized") || strings.HasSuffix(code, ".forbidden") ||
		strings.HasSuffix(code, ".auth_error")
}

// IsPaymentError checks if the error is a billing or payment error (e.g., HTTP 402).
func IsPaymentError(err error) bool {
	code := GetCode(err)
	return strings.HasSuffix(code, ".payment_required") || strings.HasSuffix(code, ".billing_error")
}
