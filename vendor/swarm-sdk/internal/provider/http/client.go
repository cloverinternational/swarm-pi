package http

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/observability"
	sdkerr "github.com/Swarm-Code/mono/swarm-sdk/internal/sdkerr"
)

// Client provides a higher-level HTTP client with observability.
// It wraps http.Client and handles common patterns like retries,
// timeouts, and automatic response handling.
type Client struct {
	// httpClient is the underlying HTTP client
	httpClient *http.Client

	// logger for structured logging
	logger observability.Logger

	// tracer for distributed tracing
	tracer observability.Tracer

	// defaultHeaders applied to all requests
	// Protected by defaultHeadersMu for thread-safe concurrent access
	defaultHeaders map[string]string

	// defaultHeadersMu protects defaultHeaders from concurrent access
	defaultHeadersMu sync.RWMutex

	// defaultTimeout applied to all requests
	defaultTimeout time.Duration

	// defaultOperationPrefix prepended to operation names
	defaultOperationPrefix string

	// retryPolicy applied to every Do() call. Nil disables retries.
	retryPolicy RetryPolicy
}

// ClientConfig provides options for creating a Client.
type ClientConfig struct {
	// HTTPClient is the underlying client to use.
	// If nil, a default client is created.
	HTTPClient *http.Client

	// Logger for structured logging.
	Logger observability.Logger

	// Tracer for distributed tracing.
	Tracer observability.Tracer

	// DefaultHeaders applied to all requests from this client.
	DefaultHeaders map[string]string

	// DefaultTimeout applied to all requests (0 means no timeout).
	DefaultTimeout time.Duration

	// OperationPrefix prepended to all operation names.
	OperationPrefix string

	// RetryPolicy applied to retryable failures (429, 5xx, transport errors).
	// If nil, NewClient installs a NewRateLimitAwarePolicy by default. Set
	// DisableRetry to true to opt out entirely.
	RetryPolicy RetryPolicy
	// HTTPMaxRetries overrides the default HTTP retry count. Nil preserves the
	// environment-driven default, zero disables retries, and positive values
	// select a rate-limit-aware policy with that many retries.
	HTTPMaxRetries *int

	// DisableRetry skips the default retry policy. Use when callers manage
	// retries themselves.
	DisableRetry bool
}

// NewClient creates a new HTTP Client with observability.
// Returns an error if logger or tracer is nil.
func NewClient(config ClientConfig) (*Client, error) {
	if config.Logger == nil {
		return nil, fmt.Errorf("logger is required")
	}
	if config.Tracer == nil {
		return nil, fmt.Errorf("tracer is required")
	}

	httpClient := config.HTTPClient
	if httpClient == nil {
		// No default timeout - let providers be as slow as they need to be
		httpClient = &http.Client{}
	}

	policy := config.RetryPolicy
	if policy == nil && !config.DisableRetry {
		policy = retryPolicy(config.Logger, config.HTTPMaxRetries)
	}

	return &Client{
		httpClient:             httpClient,
		logger:                 config.Logger,
		tracer:                 config.Tracer,
		defaultHeaders:         config.DefaultHeaders,
		defaultTimeout:         config.DefaultTimeout,
		defaultOperationPrefix: config.OperationPrefix,
		retryPolicy:            policy,
	}, nil
}

// defaultRetryPolicy returns a RateLimitAwarePolicy tuned via env vars:
//
//	SWARM_HTTP_MAX_RETRIES   integer, default 4
//	SWARM_HTTP_RETRY_BASE_MS integer ms, default 1000
//	SWARM_HTTP_RETRY_MAX_MS  integer ms, default 32000
//
// Set SWARM_HTTP_MAX_RETRIES=0 to disable retries without touching code.
func defaultRetryPolicy(logger observability.Logger) RetryPolicy {
	return retryPolicy(logger, nil)
}

func retryPolicy(logger observability.Logger, maxRetries *int) RetryPolicy {
	if maxRetries != nil && *maxRetries <= 0 {
		return nil
	}
	policy := NewRateLimitAwarePolicy(logger)
	if maxRetries == nil {
		policy.MaxRetries = envIntOr("SWARM_HTTP_MAX_RETRIES", 4)
	} else {
		policy.MaxRetries = *maxRetries
	}
	if policy.MaxRetries <= 0 {
		return nil
	}
	if v := envIntOr("SWARM_HTTP_RETRY_BASE_MS", 0); v > 0 {
		policy.BaseDelay = time.Duration(v) * time.Millisecond
	}
	if v := envIntOr("SWARM_HTTP_RETRY_MAX_MS", 0); v > 0 {
		policy.MaxDelay = time.Duration(v) * time.Millisecond
	}
	return policy
}

func envIntOr(key string, fallback int) int {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return fallback
	}
	if n, err := strconv.Atoi(raw); err == nil {
		return n
	}
	return fallback
}

// SetRetryPolicy replaces the active retry policy. Pass nil to disable retries.
func (c *Client) SetRetryPolicy(policy RetryPolicy) {
	c.retryPolicy = policy
}

// Get creates and executes a GET request.
// Returns the response or an error.
func (c *Client) Get(ctx context.Context, url string) (*Response, error) {
	return c.Do(ctx, NewRequest(ctx, c.logger, c.tracer).
		Method("GET").
		URL(url))
}

// Post creates and executes a POST request with a body.
// Returns the response or an error.
func (c *Client) Post(ctx context.Context, url string, body any) (*Response, error) {
	return c.Do(ctx, NewRequest(ctx, c.logger, c.tracer).
		Method("POST").
		URL(url).
		Body(body))
}

// Put creates and executes a PUT request with a body.
// Returns the response or an error.
func (c *Client) Put(ctx context.Context, url string, body any) (*Response, error) {
	return c.Do(ctx, NewRequest(ctx, c.logger, c.tracer).
		Method("PUT").
		URL(url).
		Body(body))
}

// Patch creates and executes a PATCH request with a body.
// Returns the response or an error.
func (c *Client) Patch(ctx context.Context, url string, body any) (*Response, error) {
	return c.Do(ctx, NewRequest(ctx, c.logger, c.tracer).
		Method("PATCH").
		URL(url).
		Body(body))
}

// Delete creates and executes a DELETE request.
// Returns the response or an error.
func (c *Client) Delete(ctx context.Context, url string) (*Response, error) {
	return c.Do(ctx, NewRequest(ctx, c.logger, c.tracer).
		Method("DELETE").
		URL(url))
}

// Do executes a request prepared with the given RequestBuilder.
// On 429 / 5xx / transport failures, applies the configured RetryPolicy with
// exponential backoff (and Retry-After honoring). Non-retryable 4xx responses
// are returned to the caller as today, so callers can still inspect bodies.
func (c *Client) Do(ctx context.Context, builder *RequestBuilder) (*Response, error) {
	// Apply client defaults
	if builder.method == "" {
		builder.method = "GET"
	}

	// Apply default headers with read lock for thread safety
	c.defaultHeadersMu.RLock()
	for k, v := range c.defaultHeaders {
		if _, exists := builder.headers[k]; !exists {
			builder.Header(k, v)
		}
	}
	c.defaultHeadersMu.RUnlock()

	// Set operation name with prefix if provided
	if builder.operationName == "" {
		prefix := ""
		if c.defaultOperationPrefix != "" {
			prefix = c.defaultOperationPrefix + "."
		}
		builder.operationName = prefix + fmt.Sprintf("http.%s", builder.method)
	}

	policy := c.retryPolicy
	maxAttempts := 1
	if policy != nil {
		if m := policy.MaxAttempts(); m > maxAttempts {
			maxAttempts = m
		}
	}

	var lastErr error
	for attempt := 0; attempt < maxAttempts; attempt++ {
		isLastAttempt := attempt == maxAttempts-1

		req, buildErr := builder.Build()
		if buildErr != nil {
			return nil, sdkerr.Wrap(
				buildErr,
				"provider.http.request_build_failed",
				sdkerr.WithOperation(builder.operationName),
				sdkerr.WithComponent("provider.http"),
				sdkerr.WithTraceFromContext(ctx),
			)
		}

		// Per-attempt timeout context, chained off the request span context.
		execCtx := req.Context()
		var cancel context.CancelFunc
		if c.defaultTimeout > 0 {
			execCtx, cancel = context.WithTimeout(execCtx, c.defaultTimeout)
		}

		httpResp, doErr := c.httpClient.Do(req.Underlying().WithContext(execCtx))

		// Classify the outcome
		var (
			attemptErr error
			retryable  bool
			retryAfter time.Duration
		)
		switch {
		case doErr != nil:
			attemptErr = doErr
			retryable = true // transport errors are typically transient
		case httpResp == nil:
			attemptErr = fmt.Errorf("nil response without error")
			retryable = true
		case httpResp.StatusCode == http.StatusTooManyRequests:
			retryAfter = parseRetryAfterDuration(httpResp.Header.Get("Retry-After"))
			attemptErr = sdkerr.Transient(
				"provider.http.rate_limited",
				fmt.Sprintf("HTTP 429: %s", trimResponseBodyPreview(httpResp.Body)),
				sdkerr.WithOperation(req.OperationName()),
				sdkerr.WithComponent("provider.http"),
				sdkerr.WithTraceFromContext(execCtx),
				sdkerr.WithRetryAfter(retryAfter),
			)
			retryable = true
		case httpResp.StatusCode >= 500:
			attemptErr = sdkerr.Transient(
				"provider.http.server_error",
				fmt.Sprintf("HTTP %d: %s", httpResp.StatusCode, trimResponseBodyPreview(httpResp.Body)),
				sdkerr.WithOperation(req.OperationName()),
				sdkerr.WithComponent("provider.http"),
				sdkerr.WithTraceFromContext(execCtx),
			)
			retryable = true
		default:
			// 2xx, 3xx, or non-retryable 4xx — return Response as today.
			if cancel != nil {
				// Don't cancel: the body may still be streamed by the caller.
				_ = cancel
			}
			req.Complete(httpResp.StatusCode)
			return NewResponse(httpResp, req), nil
		}

		lastErr = attemptErr

		// Decide whether to retry
		shouldRetry := retryable && !isLastAttempt && policy != nil && policy.ShouldRetry(execCtx, attemptErr, attempt)

		if !shouldRetry {
			// Out of retries (or none configured). For 429/5xx, return the response so
			// the caller can read the body (preserves prior contract). For transport
			// errors, return the wrapped error.
			if cancel != nil {
				// Same caveat: do not cancel on a returned response.
				_ = cancel
			}
			if doErr != nil {
				req.RecordError(doErr)
				req.Complete(0)
				return nil, sdkerr.Wrap(
					doErr,
					"provider.http.request_failed",
					sdkerr.WithOperation(req.OperationName()),
					sdkerr.WithComponent("provider.http"),
					sdkerr.WithTraceFromContext(execCtx),
				)
			}
			req.Complete(httpResp.StatusCode)
			return NewResponse(httpResp, req), nil
		}

		// Retry path: drain + close any body, free per-attempt cancel, sleep, continue.
		if httpResp != nil && httpResp.Body != nil {
			_, _ = io.Copy(io.Discard, io.LimitReader(httpResp.Body, 4096))
			_ = httpResp.Body.Close()
		}
		if cancel != nil {
			cancel()
		}

		var delay time.Duration
		if rl, ok := policy.(*RateLimitAwarePolicy); ok {
			delay = rl.BackoffDurationWithError(attempt, attemptErr)
		} else {
			delay = policy.BackoffDuration(attempt)
		}

		c.logger.Warn(ctx, "provider.http.retry",
			observability.F("attempt", attempt+1),
			observability.F("max_attempts", maxAttempts),
			observability.F("delay_ms", delay.Milliseconds()),
			observability.F("operation", req.OperationName()),
			observability.F("error", attemptErr.Error()),
		)

		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(delay):
		}
	}

	// Loop should always return inside; this is defensive.
	if lastErr != nil {
		return nil, sdkerr.Wrap(lastErr,
			"provider.http.retries_exhausted",
			sdkerr.WithOperation(builder.operationName),
			sdkerr.WithComponent("provider.http"),
			sdkerr.WithTraceFromContext(ctx),
		)
	}
	return nil, fmt.Errorf("retry loop exited without resolution")
}

// parseRetryAfterDuration parses the Retry-After header into a duration. The
// HTTP spec allows either delta-seconds (integer) or an HTTP-date; we handle
// the integer form, which is what every LLM provider in practice uses.
func parseRetryAfterDuration(value string) time.Duration {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0
	}
	if seconds, err := strconv.Atoi(value); err == nil && seconds > 0 {
		return time.Duration(seconds) * time.Second
	}
	return 0
}

// trimResponseBodyPreview pulls a short preview of an error response body for
// inclusion in the error message. The body is consumed (caller will close).
func trimResponseBodyPreview(r io.Reader) string {
	if r == nil {
		return ""
	}
	b, err := io.ReadAll(io.LimitReader(r, 1024))
	if err != nil {
		return ""
	}
	s := strings.TrimSpace(string(b))
	if len(s) > 512 {
		s = s[:512] + "..."
	}
	return s
}

// DoRequest is a convenience method that takes a fully prepared Request.
// This is useful when you need fine-grained control over the request.
func (c *Client) DoRequest(req *Request) (*Response, error) {
	httpResp, err := c.httpClient.Do(req.Underlying())
	if err != nil {
		req.RecordError(err)
		req.Complete(0)
		return nil, sdkerr.Wrap(
			err,
			"provider.http.request_failed",
			sdkerr.WithOperation(req.OperationName()),
			sdkerr.WithComponent("provider.http"),
			sdkerr.WithTraceFromContext(req.Context()),
		)
	}

	resp := NewResponse(httpResp, req)
	req.Complete(resp.StatusCode())

	return resp, nil
}

// BuildRequest creates a RequestBuilder with client defaults applied.
// This is a convenience method for building custom requests.
func (c *Client) BuildRequest(ctx context.Context) *RequestBuilder {
	return NewRequest(ctx, c.logger, c.tracer)
}

// Close closes any idle connections in the underlying client.
func (c *Client) Close() {
	if c.httpClient != nil {
		c.httpClient.CloseIdleConnections()
	}
}

// SetDefaultHeader sets a default header for all subsequent requests.
// This method is thread-safe and can be called concurrently.
func (c *Client) SetDefaultHeader(key, value string) {
	c.defaultHeadersMu.Lock()
	defer c.defaultHeadersMu.Unlock()

	// Skip empty header names
	if key == "" {
		// Silently skip - this is expected for optional headers
		return
	}

	if c.defaultHeaders == nil {
		c.defaultHeaders = make(map[string]string)
	}
	c.defaultHeaders[key] = value
}

// GetDefaultHeader retrieves a default header.
// This method is thread-safe and can be called concurrently.
func (c *Client) DefaultHeader(key string) (string, bool) {
	c.defaultHeadersMu.RLock()
	defer c.defaultHeadersMu.RUnlock()

	if c.defaultHeaders == nil {
		return "", false
	}
	v, ok := c.defaultHeaders[key]
	return v, ok
}

// RemoveDefaultHeader removes a default header.
// This method is thread-safe and can be called concurrently.
func (c *Client) RemoveDefaultHeader(key string) {
	c.defaultHeadersMu.Lock()
	defer c.defaultHeadersMu.Unlock()

	if c.defaultHeaders != nil {
		delete(c.defaultHeaders, key)
	}
}

// SetDefaultTimeout sets the default timeout for all requests.
func (c *Client) SetDefaultTimeout(timeout time.Duration) {
	c.defaultTimeout = timeout
}

// JSONRequest is a convenience method for making JSON POST requests.
// It automatically sets Content-Type to application/json.
func (c *Client) JSONRequest(ctx context.Context, method, url string, body any) (*Response, error) {
	return c.Do(ctx, NewRequest(ctx, c.logger, c.tracer).
		Method(method).
		URL(url).
		Header("Content-Type", "application/json").
		Body(body))
}

// BearerAuth sets a Bearer token for authorization.
// Returns a modified RequestBuilder for the request being built.
func BearerAuth(builder *RequestBuilder, token string) *RequestBuilder {
	return builder.Header("Authorization", fmt.Sprintf("Bearer %s", token))
}

// BasicAuth sets basic authentication credentials.
// Returns a modified RequestBuilder for the request being built.
func BasicAuth(builder *RequestBuilder, username, password string) *RequestBuilder {
	// Build a temporary request to generate the basic auth header
	tempReq, err := http.NewRequest("GET", "http://localhost", nil)
	if err == nil {
		tempReq.SetBasicAuth(username, password)
		// Extract the Authorization header that was set
		if authHeader := tempReq.Header.Get("Authorization"); authHeader != "" {
			builder.Header("Authorization", authHeader)
		}
	}
	return builder
}

// ResponseHandler provides common response handling patterns.
type ResponseHandler struct {
	resp *Response
	err  error
}

// NewResponseHandler wraps a response for fluent error handling.
func NewResponseHandler(resp *Response, err error) *ResponseHandler {
	return &ResponseHandler{resp: resp, err: err}
}

// Error returns the error if one occurred during request execution.
func (h *ResponseHandler) Error() error {
	return h.err
}

// Response returns the response if available.
func (h *ResponseHandler) Response() *Response {
	return h.resp
}

// IsOK returns true if the response status is in the 2xx range.
func (h *ResponseHandler) IsOK() bool {
	if h.resp == nil {
		return false
	}
	return h.resp.IsSuccess()
}

// IsError returns true if an error occurred or status is 4xx/5xx.
func (h *ResponseHandler) IsError() bool {
	if h.err != nil {
		return true
	}
	if h.resp == nil {
		return true
	}
	return h.resp.IsError()
}

// JSON unmarshals the response body as JSON.
// Returns an error if the handler has an error or JSON parsing fails.
func (h *ResponseHandler) JSON(dest any) error {
	if h.err != nil {
		return h.err
	}
	if h.resp == nil {
		return fmt.Errorf("no response available")
	}
	return h.resp.JSON(dest)
}

// Text returns the response body as a string.
// Returns an error if the handler has an error or body reading fails.
func (h *ResponseHandler) Text() (string, error) {
	if h.err != nil {
		return "", h.err
	}
	if h.resp == nil {
		return "", fmt.Errorf("no response available")
	}
	return h.resp.Text()
}

// Status returns the HTTP status code from the response.
// Returns 0 if no response is available.
func (h *ResponseHandler) Status() int {
	if h.resp == nil {
		return 0
	}
	return h.resp.StatusCode()
}

// Close closes the response body if available.
func (h *ResponseHandler) Close() error {
	if h.resp == nil {
		return nil
	}
	return h.resp.Close()
}

// String returns a string representation of the handler state.
func (h *ResponseHandler) String() string {
	if h.err != nil {
		return fmt.Sprintf("ResponseHandler{error: %v}", h.err)
	}
	if h.resp == nil {
		return "ResponseHandler{empty}"
	}
	return fmt.Sprintf("ResponseHandler{status: %d}", h.resp.StatusCode())
}
