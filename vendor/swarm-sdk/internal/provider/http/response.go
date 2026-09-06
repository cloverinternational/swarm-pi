// Package http provides HTTP utilities for provider implementations.
// This package handles HTTP response parsing, error categorization, and metadata extraction.
package http

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/observability"
	sdkerr "github.com/Swarm-Code/mono/swarm-sdk/internal/sdkerr"
)

// HTTPResponse wraps an http.Response with additional metadata and utilities
// for handling provider responses consistently across different API implementations.
// It extends the basic Response struct with response parsing, error categorization,
// and metadata extraction capabilities for provider APIs.
type HTTPResponse struct {
	// Raw is the underlying HTTP response.
	Raw *http.Response

	// Body contains the response body as bytes (for reuse).
	Body []byte

	// Metadata contains extracted provider-specific information.
	Metadata ResponseMetadata

	// ParsedAt records when the response was parsed.
	ParsedAt time.Time
}

// ResponseMetadata contains extracted metadata from HTTP responses.
type ResponseMetadata struct {
	// StatusCode is the HTTP status code.
	StatusCode int

	// ContentType is the Content-Type header value.
	ContentType string

	// RateLimitInfo contains rate limit headers extracted from the response.
	RateLimitInfo *RateLimitInfo

	// TokenUsage contains token consumption information if available.
	TokenUsage *TokenUsageInfo

	// Headers contains relevant response headers (lowercase keys).
	Headers map[string]string

	// RequestID is the provider's request identifier if available.
	RequestID string

	// Method is the HTTP method used for the request.
	Method string

	// URL is the full URL of the request.
	URL string

	// TraceID is the distributed trace ID if available.
	TraceID string

	// Duration is the time taken to receive the response.
	Duration time.Duration
}

// RateLimitInfo contains rate limit information extracted from headers.
type RateLimitInfo struct {
	// Limit is the maximum number of requests allowed in the window.
	Limit int

	// Remaining is the number of requests remaining in the current window.
	Remaining int

	// ResetAt is when the rate limit window resets (Unix timestamp).
	ResetAt int64

	// RetryAfter is the suggested retry delay (in seconds or as a timestamp).
	RetryAfter string

	// IsRetryAfterSeconds indicates if RetryAfter is in seconds (true) or datetime (false).
	IsRetryAfterSeconds bool
}

// TokenUsageInfo contains token consumption information.
type TokenUsageInfo struct {
	// InputTokens is the number of tokens in the request.
	InputTokens int

	// OutputTokens is the number of tokens in the response.
	OutputTokens int

	// TotalTokens is the combined token count.
	TotalTokens int

	// Provider is the name of the provider (for context in logging).
	Provider string
}

// NewHTTPResponse creates a new HTTPResponse wrapper around an http.Response.
// It automatically reads the body and extracts metadata.
func NewHTTPResponse(httpResp *http.Response) (*HTTPResponse, error) {
	if httpResp == nil {
		return nil, sdkerr.Permanent(
			"provider.http.invalid_response",
			"received nil http.Response",
			sdkerr.WithOperation("provider.http.new_response"),
			sdkerr.WithComponent("provider.http"),
		)
	}

	// Read and store the body
	bodyBytes, err := io.ReadAll(httpResp.Body)
	if err != nil {
		return nil, sdkerr.Wrap(err, "provider.http.body_read_error",
			sdkerr.WithOperation("provider.http.new_response"),
			sdkerr.WithComponent("provider.http"),
			sdkerr.WithContext("status_code", httpResp.StatusCode))
	}

	// Restore body for potential re-reads
	httpResp.Body = io.NopCloser(strings.NewReader(string(bodyBytes)))

	resp := &HTTPResponse{
		Raw:      httpResp,
		Body:     bodyBytes,
		ParsedAt: time.Now(),
	}

	// Extract metadata
	resp.extractMetadata()

	return resp, nil
}

// extractMetadata extracts and parses HTTP response metadata.
func (r *HTTPResponse) extractMetadata() {
	r.Metadata.StatusCode = r.Raw.StatusCode
	r.Metadata.ContentType = r.Raw.Header.Get("Content-Type")
	r.Metadata.Headers = make(map[string]string)

	// Extract request details if available
	if r.Raw.Request != nil {
		r.Metadata.Method = r.Raw.Request.Method
		r.Metadata.URL = r.Raw.Request.URL.String()
	}

	// Store normalized headers (lowercase)
	for key, values := range r.Raw.Header {
		if len(values) > 0 {
			r.Metadata.Headers[strings.ToLower(key)] = values[0]
		}
	}

	// Extract request ID (varies by provider)
	r.Metadata.RequestID = extractRequestID(r.Raw.Header)

	// Extract trace ID (varies by provider)
	r.Metadata.TraceID = extractTraceID(r.Raw.Header)

	// Extract rate limit information
	r.Metadata.RateLimitInfo = extractRateLimitInfo(r.Raw.Header)

	// Extract token usage information
	r.Metadata.TokenUsage = extractTokenUsageInfo(r.Raw.Header)
}

// ParseJSON deserializes the response body as JSON into the provided value.
// Returns an error if the response is not valid JSON or contains an error response.
func (r *HTTPResponse) ParseJSON(v any) error {
	if r.Body == nil || len(r.Body) == 0 {
		return sdkerr.Permanent(
			"provider.http.empty_response",
			"response body is empty",
			sdkerr.WithOperation("provider.http.parse_json"),
			sdkerr.WithComponent("provider.http"),
			sdkerr.WithTraceID(r.Metadata.TraceID),
			sdkerr.WithContext("status_code", r.Metadata.StatusCode),
		)
	}

	// Check for error responses before parsing
	if err := r.checkErrorResponse(); err != nil {
		return err
	}

	// Attempt to unmarshal JSON
	if err := json.Unmarshal(r.Body, v); err != nil {
		return sdkerr.Permanent(
			"provider.http.json_parse_error",
			fmt.Sprintf("failed to parse JSON response: %v", err),
			sdkerr.WithOperation("provider.http.parse_json"),
			sdkerr.WithComponent("provider.http"),
			sdkerr.WithTraceID(r.Metadata.TraceID),
			sdkerr.WithContext("status_code", r.Metadata.StatusCode),
			sdkerr.WithContext("body_sample", string(r.Body[:min(len(r.Body), 500)])),
		)
	}

	return nil
}

// ParseJSONStream deserializes multiple JSON objects from the response body.
// Each line is expected to contain a complete JSON object (JSONL format).
func (r *HTTPResponse) ParseJSONStream(handler func(any) error) error {
	if r.Body == nil || len(r.Body) == 0 {
		return sdkerr.Permanent(
			"provider.http.empty_response",
			"response body is empty",
			sdkerr.WithOperation("provider.http.parse_json_stream"),
			sdkerr.WithComponent("provider.http"),
			sdkerr.WithTraceID(r.Metadata.TraceID),
			sdkerr.WithContext("status_code", r.Metadata.StatusCode),
		)
	}

	lines := strings.Split(string(r.Body), "\n")
	for i, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		var obj any
		if err := json.Unmarshal([]byte(line), &obj); err != nil {
			return sdkerr.Permanent(
				"provider.http.json_stream_parse_error",
				fmt.Sprintf("failed to parse JSON at line %d: %v", i+1, err),
				sdkerr.WithOperation("provider.http.parse_json_stream"),
				sdkerr.WithComponent("provider.http"),
				sdkerr.WithTraceID(r.Metadata.TraceID),
				sdkerr.WithContext("status_code", r.Metadata.StatusCode),
				sdkerr.WithContext("line_number", i+1),
				sdkerr.WithContext("line_content", line[:min(len(line), 200)]),
			)
		}

		if err := handler(obj); err != nil {
			return err
		}
	}

	return nil
}

// checkErrorResponse checks if the response indicates an error condition
// and returns a properly categorized error if so.
func (r *HTTPResponse) checkErrorResponse() error {
	statusCode := r.Metadata.StatusCode

	// 2xx responses are not errors
	if statusCode >= 200 && statusCode < 300 {
		return nil
	}

	// Attempt to parse error from response body
	var errMsg string
	var errType string

	// Try common error response formats
	var errResponse map[string]any
	if err := json.Unmarshal(r.Body, &errResponse); err == nil {
		// Check for common error fields
		if msg, ok := errResponse["error"].(map[string]any); ok {
			if m, ok := msg["message"].(string); ok {
				errMsg = m
			}
			if t, ok := msg["type"].(string); ok {
				errType = t
			}
		} else if msg, ok := errResponse["error"].(string); ok {
			errMsg = msg
		} else if msg, ok := errResponse["message"].(string); ok {
			errMsg = msg
		}
	}

	if errMsg == "" {
		errMsg = http.StatusText(statusCode)
	}

	// Categorize the error based on status code
	category := categorizeStatusCode(statusCode)
	errorType := fmt.Sprintf("provider.http.%s", categorizeStatusCodeToType(statusCode))

	// Create appropriate error with retry information
	var sdkErr *sdkerr.Error
	switch category {
	case sdkerr.CategoryTransient:
		sdkErr = sdkerr.Wrap(fmt.Errorf("HTTP %d: %s", statusCode, errMsg), errorType,
			sdkerr.WithOperation("provider.http.check_error_response"),
			sdkerr.WithComponent("provider.http"),
			sdkerr.WithTraceID(r.Metadata.TraceID),
			sdkerr.WithContext("status_code", statusCode),
			sdkerr.WithContext("error_type", errType),
			sdkerr.WithRetryAfter(r.getRetryDelay()),
		)

	case sdkerr.CategoryPermanent:
		sdkErr = sdkerr.Permanent(
			errorType,
			fmt.Sprintf("HTTP %d: %s", statusCode, errMsg),
			sdkerr.WithOperation("provider.http.check_error_response"),
			sdkerr.WithComponent("provider.http"),
			sdkerr.WithTraceID(r.Metadata.TraceID),
			sdkerr.WithContext("status_code", statusCode),
			sdkerr.WithContext("error_type", errType),
		)

	default:
		sdkErr = sdkerr.Steering(
			errorType,
			fmt.Sprintf("HTTP %d: %s", statusCode, errMsg),
			sdkerr.WithOperation("provider.http.check_error_response"),
			sdkerr.WithComponent("provider.http"),
			sdkerr.WithTraceID(r.Metadata.TraceID),
			sdkerr.WithContext("status_code", statusCode),
			sdkerr.WithContext("error_type", errType),
		)
	}

	// Add request details to error context if available
	if r.Metadata.URL != "" {
		sdkErr.Attributes["url"] = r.Metadata.URL
		sdkErr.Attributes["method"] = r.Metadata.Method
	}

	// Add full response body to context for debugging
	if len(r.Body) > 0 {
		sdkErr.Attributes["raw_response"] = string(r.Body)
	}

	// Add request ID to error context if available
	if r.Metadata.RequestID != "" {
		sdkErr.Attributes["request_id"] = r.Metadata.RequestID
	}

	return sdkErr
}

// categorizeStatusCode maps HTTP status codes to error categories.
func categorizeStatusCode(statusCode int) sdkerr.Category {
	switch {
	case statusCode >= 200 && statusCode < 300:
		return sdkerr.CategoryTransient // Not an error, but shouldn't reach here
	case statusCode == 429:
		return sdkerr.CategoryTransient // Rate limit
	case statusCode >= 500 && statusCode < 600:
		return sdkerr.CategoryTransient // Server errors are transient
	case statusCode >= 400 && statusCode < 500:
		return sdkerr.CategoryPermanent // Client errors are permanent
	default:
		return sdkerr.CategorySteering // Unknown, let steering decide
	}
}

// categorizeStatusCodeToType maps HTTP status codes to error type suffixes.
func categorizeStatusCodeToType(statusCode int) string {
	switch statusCode {
	case 400:
		return "bad_request"
	case 401:
		return "unauthorized"
	case 403:
		return "forbidden"
	case 404:
		return "not_found"
	case 408:
		return "request_timeout"
	case 409:
		return "conflict"
	case 410:
		return "gone"
	case 413:
		return "payload_too_large"
	case 415:
		return "unsupported_media_type"
	case 422:
		return "unprocessable_entity"
	case 429:
		return "rate_limited"
	case 500:
		return "internal_server_error"
	case 501:
		return "not_implemented"
	case 502:
		return "bad_gateway"
	case 503:
		return "service_unavailable"
	case 504:
		return "gateway_timeout"
	default:
		if statusCode >= 400 && statusCode < 500 {
			return "client_error"
		} else if statusCode >= 500 && statusCode < 600 {
			return "server_error"
		}
		return "unknown_error"
	}
}

// getRetryDelay extracts the retry delay from rate limit headers.
func (r *HTTPResponse) getRetryDelay() time.Duration {
	if r.Metadata.RateLimitInfo == nil {
		return 0
	}

	rateLimitInfo := r.Metadata.RateLimitInfo
	if rateLimitInfo.RetryAfter == "" {
		return 0
	}

	if rateLimitInfo.IsRetryAfterSeconds {
		// Parse as seconds
		if seconds, err := strconv.ParseInt(rateLimitInfo.RetryAfter, 10, 64); err == nil {
			return time.Duration(seconds) * time.Second
		}
	} else {
		// Parse as HTTP-date or Unix timestamp
		if timestamp, err := strconv.ParseInt(rateLimitInfo.RetryAfter, 10, 64); err == nil {
			resetTime := time.Unix(timestamp, 0)
			if delay := time.Until(resetTime); delay > 0 {
				return delay
			}
		}
		// Try parsing as HTTP-date (RFC1123)
		if resetTime, err := time.Parse(time.RFC1123, rateLimitInfo.RetryAfter); err == nil {
			if delay := time.Until(resetTime); delay > 0 {
				return delay
			}
		}
	}

	return 0
}

// ExtractToken attempts to extract an authentication token from response headers.
// This is provider-specific and should be overridden for specific provider implementations.
// Common locations: Authorization header, x-auth-token, token header, etc.
func (r *HTTPResponse) ExtractToken() (string, error) {
	// Try common token header names
	tokenHeaderNames := []string{
		"authorization",
		"x-auth-token",
		"x-api-token",
		"x-access-token",
		"token",
	}

	for _, headerName := range tokenHeaderNames {
		if token := r.Raw.Header.Get(headerName); token != "" {
			// Remove "Bearer " prefix if present
			token = strings.TrimPrefix(token, "Bearer ")
			token = strings.TrimPrefix(token, "bearer ")
			return token, nil
		}
	}

	return "", sdkerr.Permanent(
		"provider.http.token_not_found",
		"no authentication token found in response headers",
		sdkerr.WithOperation("provider.http.extract_token"),
		sdkerr.WithComponent("provider.http"),
		sdkerr.WithTraceID(r.Metadata.TraceID),
		sdkerr.WithContext("status_code", r.Metadata.StatusCode),
	)
}

// LogMetadata logs the response metadata using the provided logger.
func (r *HTTPResponse) LogMetadata(logger observability.Logger) {
	if logger == nil {
		return
	}

	fields := []observability.Field{
		{Key: "status_code", Value: r.Metadata.StatusCode},
		{Key: "content_type", Value: r.Metadata.ContentType},
		{Key: "duration_ms", Value: r.Metadata.Duration.Milliseconds()},
	}

	if r.Metadata.RequestID != "" {
		fields = append(fields, observability.F("request_id", r.Metadata.RequestID))
	}

	if r.Metadata.TraceID != "" {
		fields = append(fields, observability.F("trace_id", r.Metadata.TraceID))
	}

	if r.Metadata.RateLimitInfo != nil {
		rateLimitInfo := r.Metadata.RateLimitInfo
		fields = append(fields,
			observability.F("rate_limit_remaining", rateLimitInfo.Remaining),
			observability.F("rate_limit_limit", rateLimitInfo.Limit),
			observability.F("rate_limit_reset_at", rateLimitInfo.ResetAt),
		)
	}

	if r.Metadata.TokenUsage != nil {
		tokenUsage := r.Metadata.TokenUsage
		fields = append(fields,
			observability.F("input_tokens", tokenUsage.InputTokens),
			observability.F("output_tokens", tokenUsage.OutputTokens),
			observability.F("total_tokens", tokenUsage.TotalTokens),
		)
	}

	logger.Info(nil, "provider.http.response", fields...)
}

// extractRequestID extracts the request ID from common header names.
func extractRequestID(headers http.Header) string {
	requestIDHeaders := []string{
		"x-request-id",
		"x-correlation-id",
		"x-trace-id",
		"request-id",
		"correlation-id",
	}

	for _, headerName := range requestIDHeaders {
		if value := headers.Get(headerName); value != "" {
			return value
		}
	}

	return ""
}

// extractTraceID extracts the distributed trace ID from headers.
func extractTraceID(headers http.Header) string {
	traceIDHeaders := []string{
		"x-trace-id",
		"traceparent",
		"x-b3-traceid",
		"uber-trace-id",
	}

	for _, headerName := range traceIDHeaders {
		if value := headers.Get(headerName); value != "" {
			// For traceparent format (W3C), extract just the trace ID
			if headerName == "traceparent" {
				parts := strings.Split(value, "-")
				if len(parts) >= 2 {
					return parts[1]
				}
			}
			return value
		}
	}

	return ""
}

// extractRateLimitInfo extracts rate limit information from response headers.
// Different providers use different header names and formats.
func extractRateLimitInfo(headers http.Header) *RateLimitInfo {
	info := &RateLimitInfo{}
	found := false

	// Try standard rate limit headers (various formats)
	rateLimitHeaders := []struct {
		limit      string
		remaining  string
		reset      string
		retryAfter string
	}{
		// OpenAI / Anthropic format
		{"x-ratelimit-limit-requests", "x-ratelimit-remaining-requests", "x-ratelimit-reset-requests", "retry-after"},
		{"x-ratelimit-limit-tokens", "x-ratelimit-remaining-tokens", "x-ratelimit-reset-tokens", "retry-after"},
		// Generic format
		{"ratelimit-limit", "ratelimit-remaining", "ratelimit-reset", "retry-after"},
		// CloudFlare format
		{"cf-ratelimit-limit", "cf-ratelimit-remaining", "cf-ratelimit-reset", "retry-after"},
		// Custom format (fallback)
		{"x-rate-limit-limit", "x-rate-limit-remaining", "x-rate-limit-reset", "x-rate-limit-retry-after"},
	}

	for _, headerSet := range rateLimitHeaders {
		if limit := headers.Get(headerSet.limit); limit != "" {
			if l, err := strconv.Atoi(limit); err == nil {
				info.Limit = l
				found = true
			}
		}

		if remaining := headers.Get(headerSet.remaining); remaining != "" {
			if r, err := strconv.Atoi(remaining); err == nil {
				info.Remaining = r
				found = true
			}
		}

		if reset := headers.Get(headerSet.reset); reset != "" {
			if resetTime, err := strconv.ParseInt(reset, 10, 64); err == nil {
				info.ResetAt = resetTime
				found = true
			}
		}

		if retryAfter := headers.Get(headerSet.retryAfter); retryAfter != "" {
			info.RetryAfter = retryAfter
			found = true

			// Determine if it's seconds or a date
			if _, err := strconv.ParseInt(retryAfter, 10, 64); err == nil {
				// Could be either seconds or Unix timestamp
				// If it's small, assume seconds; if large, assume Unix timestamp
				if val, _ := strconv.ParseInt(retryAfter, 10, 64); val < 100000 {
					info.IsRetryAfterSeconds = true
				}
			}
		}

		if found {
			return info
		}
	}

	// If we didn't find rate limit info, return nil
	if !found {
		return nil
	}

	return info
}

// extractTokenUsageInfo extracts token usage information from response headers.
// Format varies by provider.
func extractTokenUsageInfo(headers http.Header) *TokenUsageInfo {
	info := &TokenUsageInfo{}
	found := false

	// Common token usage header patterns
	tokenHeaders := []struct {
		input  string
		output string
		total  string
	}{
		// OpenAI / Anthropic format
		{"x-openai-input-tokens", "x-openai-output-tokens", "x-openai-total-tokens"},
		{"x-anthropic-input-tokens", "x-anthropic-output-tokens", ""},
		// Generic format
		{"x-usage-input", "x-usage-output", "x-usage-total"},
		{"usage-input-tokens", "usage-output-tokens", "usage-total-tokens"},
	}

	for _, headerSet := range tokenHeaders {
		if input := headers.Get(headerSet.input); input != "" {
			if i, err := strconv.Atoi(input); err == nil {
				info.InputTokens = i
				found = true
			}
		}

		if output := headers.Get(headerSet.output); output != "" {
			if o, err := strconv.Atoi(output); err == nil {
				info.OutputTokens = o
				found = true
			}
		}

		if headerSet.total != "" {
			if total := headers.Get(headerSet.total); total != "" {
				if t, err := strconv.Atoi(total); err == nil {
					info.TotalTokens = t
					found = true
				}
			}
		}

		if found {
			// Calculate total if not provided
			if info.TotalTokens == 0 {
				info.TotalTokens = info.InputTokens + info.OutputTokens
			}
			return info
		}
	}

	// If we didn't find token info, return nil
	if !found {
		return nil
	}

	return info
}

// StatusOK returns true if the response status code indicates success (2xx).
func (r *HTTPResponse) StatusOK() bool {
	return r.Metadata.StatusCode >= 200 && r.Metadata.StatusCode < 300
}
