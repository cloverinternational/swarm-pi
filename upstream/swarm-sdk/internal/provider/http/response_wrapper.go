package http

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/observability"
	sdkerr "github.com/Swarm-Code/mono/swarm-sdk/internal/sdkerr"
)

// Response wraps an HTTP response with the associated request for observability integration.
// It provides convenient methods for checking status, parsing body, and cleanup.
type Response struct {
	// httpResp is the underlying HTTP response
	httpResp *http.Response

	// req is the request that generated this response
	req *Request

	// logger for structured logging
	logger observability.Logger

	// body is cached response body for reuse
	// Protected by bodyMu for thread-safe concurrent access
	body []byte

	// bodyRead tracks if body has been read
	// Protected by bodyMu for thread-safe concurrent access
	bodyRead bool

	// bodyMu protects body and bodyRead from concurrent access
	bodyMu sync.Mutex

	// statusCode caches the HTTP status code
	statusCode int

	// receivedAt records when the response was received
	receivedAt time.Time
}

// NewResponse creates a new Response wrapper around an HTTP response.
// It associates the response with the request that generated it.
func NewResponse(httpResp *http.Response, req *Request) *Response {
	resp := &Response{
		httpResp:   httpResp,
		req:        req,
		logger:     req.logger,
		statusCode: httpResp.StatusCode,
		receivedAt: time.Now(),
	}

	return resp
}

// StatusCode returns the HTTP status code from the response.
func (r *Response) StatusCode() int {
	if r == nil || r.httpResp == nil {
		return 0
	}
	return r.httpResp.StatusCode
}

// IsSuccess returns true if the response status code is in the 2xx range.
func (r *Response) IsSuccess() bool {
	code := r.StatusCode()
	return code >= 200 && code < 300
}

// IsError returns true if the response status code is in the 4xx or 5xx range.
func (r *Response) IsError() bool {
	code := r.StatusCode()
	return code >= 400
}

// Is4xxError returns true if the response status code is in the 4xx range (client error).
func (r *Response) Is4xxError() bool {
	code := r.StatusCode()
	return code >= 400 && code < 500
}

// Is5xxError returns true if the response status code is in the 5xx range (server error).
func (r *Response) Is5xxError() bool {
	code := r.StatusCode()
	return code >= 500 && code < 600
}

// Header returns the value of a response header.
func (r *Response) Header(name string) string {
	if r == nil || r.httpResp == nil {
		return ""
	}
	return r.httpResp.Header.Get(name)
}

// Headers returns all response headers.
func (r *Response) Headers() http.Header {
	if r == nil || r.httpResp == nil {
		return http.Header{}
	}
	return r.httpResp.Header
}

// Body returns the response body as bytes.
// The body is read once and cached for subsequent calls.
// This method is thread-safe and ensures the body is only read once even under concurrent access.
func (r *Response) Body() ([]byte, error) {
	traceCtx := context.Background()
	if r != nil && r.req != nil {
		traceCtx = r.req.Context()
	}

	if r == nil || r.httpResp == nil {
		return nil, sdkerr.Permanent(
			"provider.http.response_nil",
			"response is nil",
			sdkerr.WithOperation("provider.http.response_body"),
			sdkerr.WithComponent("provider.http"),
		)
	}

	// Lock for thread-safe access to body cache
	r.bodyMu.Lock()
	defer r.bodyMu.Unlock()

	// Return cached body if already read
	if r.bodyRead {
		return r.body, nil
	}

	// Read body (only happens once due to lock)
	body, err := io.ReadAll(r.httpResp.Body)
	if err != nil {
		return nil, sdkerr.Wrap(err, "provider.http.response_body_read_failed",
			sdkerr.WithOperation("provider.http.response_body"),
			sdkerr.WithComponent("provider.http"),
			sdkerr.WithTraceFromContext(traceCtx))
	}

	// Restore body for potential re-reads by wrapping the cached bytes in a fresh reader
	// This allows multiple calls to Body() to work correctly
	r.httpResp.Body = io.NopCloser(bytes.NewReader(body))

	r.body = body
	r.bodyRead = true

	return body, nil
}

// Text returns the response body as a string.
func (r *Response) Text() (string, error) {
	body, err := r.Body()
	if err != nil {
		return "", err
	}
	return string(body), nil
}

// JSON unmarshals the response body as JSON into the provided destination.
// This method automatically detects and handles error responses from the provider.
func (r *Response) JSON(dest any) error {
	traceCtx := context.Background()
	if r != nil && r.req != nil {
		traceCtx = r.req.Context()
	}

	body, err := r.Body()
	if err != nil {
		return err
	}

	if len(body) == 0 {
		return sdkerr.Permanent(
			"provider.http.empty_response",
			"response body is empty",
			sdkerr.WithOperation("provider.http.response_json"),
			sdkerr.WithComponent("provider.http"),
			sdkerr.WithTraceFromContext(traceCtx),
		)
	}

	// Use HTTPResponse for proper error detection and categorization
	httpResp, err := NewHTTPResponse(r.httpResp)
	if err != nil {
		return err
	}

	// ParseJSON will check for error responses and return categorized errors
	if err := httpResp.ParseJSON(dest); err != nil {
		return err
	}

	return nil
}

// JSONMap is a convenience method to unmarshal the response body into a map.
func (r *Response) JSONMap() (map[string]any, error) {
	var result map[string]any
	if err := r.JSON(&result); err != nil {
		return nil, err
	}
	return result, nil
}

// JSONArray is a convenience method to unmarshal the response body into a slice.
func (r *Response) JSONArray() ([]any, error) {
	var result []any
	if err := r.JSON(&result); err != nil {
		return nil, err
	}
	return result, nil
}

// Close closes the response body.
func (r *Response) Close() error {
	if r == nil || r.httpResp == nil || r.httpResp.Body == nil {
		return nil
	}
	return r.httpResp.Body.Close()
}

// ContentType returns the Content-Type header value.
func (r *Response) ContentType() string {
	return r.Header("Content-Type")
}

// ContentLength returns the Content-Length header value as an integer.
func (r *Response) ContentLength() int64 {
	if r == nil || r.httpResp == nil {
		return 0
	}
	return r.httpResp.ContentLength
}

// Duration returns the time elapsed from when the request was created to when the response was received.
func (r *Response) Duration() time.Duration {
	if r == nil || r.req == nil {
		return 0
	}
	return r.receivedAt.Sub(r.req.startTime)
}

// Request returns the request that generated this response.
func (r *Response) Request() *Request {
	if r == nil {
		return nil
	}
	return r.req
}

// RawResponse returns the underlying http.Response for advanced use cases like streaming.
// CAUTION: Direct access to http.Response bypasses the wrapper's caching and thread-safety.
func (r *Response) RawResponse() *http.Response {
	if r == nil {
		return nil
	}
	return r.httpResp
}

// Log logs the response details at info level.
func (r *Response) Log() {
	if r == nil || r.logger == nil {
		return
	}

	fields := []observability.Field{
		{Key: "status_code", Value: r.StatusCode()},
		{Key: "content_type", Value: r.ContentType()},
		{Key: "content_length", Value: r.ContentLength()},
		{Key: "duration_ms", Value: r.Duration().Milliseconds()},
	}

	if r.req != nil {
		fields = append(fields,
			observability.F("method", r.req.req.Method),
			observability.F("url", r.req.req.URL.String()),
		)
	}

	r.logger.Info(nil, "http.response_received", fields...)
}

// String returns a string representation of the response.
func (r *Response) String() string {
	if r == nil {
		return "Response{nil}"
	}
	return fmt.Sprintf("Response{status: %d, content_type: %s, size: %d bytes}",
		r.StatusCode(),
		r.ContentType(),
		len(r.body),
	)
}
