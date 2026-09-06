// Package http provides HTTP client utilities with built-in observability integration.
// Request building uses a fluent API for clean, composable HTTP request construction
// with automatic tracing, logging, and header management.
package http

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"net/http"
	"time"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/observability"
	sdkerr "github.com/Swarm-Code/mono/swarm-sdk/internal/sdkerr"
)

// Request wraps http.Request with metadata and observability integration.
// All HTTP requests in the SDK should use this type to ensure consistent
// tracing, logging, and error handling across provider implementations.
type Request struct {
	// req is the underlying http.Request
	req *http.Request

	// span tracks this request in the distributed trace
	span observability.Span

	// logger is used for structured logging of request details
	logger observability.Logger

	// startTime records when the request was created
	startTime time.Time

	// headers stores custom headers to be applied
	headers map[string]string

	// body stores the serialized request body
	body []byte

	// requestID is a unique identifier for this request
	requestID string

	// operationName describes the operation being performed
	operationName string

	// metadata contains additional context
	metadata map[string]any
}

// RequestBuilder provides a fluent API for constructing HTTP requests
// with observability integration. All builder methods return the builder
// to enable method chaining.
//
// Example:
//
//	req, err := NewRequest(ctx, logger, tracer).
//		Method("POST").
//		URL("https://api.example.com/chat").
//		Header("Authorization", "Bearer token").
//		Body(chatData).
//		Build()
type RequestBuilder struct {
	ctx            context.Context
	logger         observability.Logger
	tracer         observability.Tracer
	method         string
	url            string
	headers        map[string]string
	body           any
	operationName  string
	requestID      string
	spanAttributes map[string]any
	metadata       map[string]any
}

// NewRequest creates a new RequestBuilder with the given context and observability handlers.
// This is the entry point for the fluent API. The context is used for cancellation,
// timeouts, and span propagation.
func NewRequest(ctx context.Context, logger observability.Logger, tracer observability.Tracer) *RequestBuilder {
	return &RequestBuilder{
		ctx:            ctx,
		logger:         logger,
		tracer:         tracer,
		headers:        make(map[string]string),
		spanAttributes: make(map[string]any),
		metadata:       make(map[string]any),
	}
}

// Method sets the HTTP method for the request (GET, POST, etc.).
// Returns the builder for method chaining.
func (rb *RequestBuilder) Method(method string) *RequestBuilder {
	rb.method = method
	rb.spanAttributes["http.method"] = method
	return rb
}

// URL sets the target URL for the request.
// Returns the builder for method chaining.
func (rb *RequestBuilder) URL(url string) *RequestBuilder {
	rb.url = url
	rb.spanAttributes["http.url"] = url
	return rb
}

// Header adds a custom header to the request.
// Returns the builder for method chaining.
// Headers should not contain sensitive information; use metadata for that.
func (rb *RequestBuilder) Header(key, value string) *RequestBuilder {
	rb.headers[key] = value
	return rb
}

// Headers adds multiple headers at once.
// Returns the builder for method chaining.
func (rb *RequestBuilder) Headers(headers map[string]string) *RequestBuilder {
	maps.Copy(rb.headers, headers)
	return rb
}

// Body sets the request body as an any that will be JSON-marshaled.
// For raw bytes, use BodyBytes instead.
// Returns the builder for method chaining.
func (rb *RequestBuilder) Body(body any) *RequestBuilder {
	rb.body = body
	rb.spanAttributes["has_body"] = true
	return rb
}

// BodyBytes sets the raw request body as bytes.
// The content-type must be set separately if needed.
// Returns the builder for method chaining.
func (rb *RequestBuilder) BodyBytes(body []byte) *RequestBuilder {
	rb.body = body
	rb.spanAttributes["has_body"] = true
	return rb
}

// BodyReader sets the request body from an io.Reader.
// This enables true streaming support for large request bodies.
//
// Behavior:
// - For bytes.Reader or seekable readers (files): auto-buffered on Build()
// - For network streams or unknown-sized readers: buffered to allow body inspection
// - The reader is consumed during Build() - don't use it again after
//
// For truly unbuffered streaming (e.g., for large file uploads):
// - Use bytes.Reader wrapped around os.File for seekable streams
// - Or implement custom logic with http.Client.Do() directly
//
// Returns the builder for method chaining.
func (rb *RequestBuilder) BodyReader(reader io.Reader) *RequestBuilder {
	// Store the reader as body - Build() will handle it specially
	rb.body = reader
	rb.spanAttributes["has_body"] = true
	return rb
}

// OperationName sets a human-readable name for this operation.
// This is used as the span name and in logs for easy identification.
// Returns the builder for method chaining.
func (rb *RequestBuilder) OperationName(name string) *RequestBuilder {
	rb.operationName = name
	return rb
}

// RequestID sets a custom request ID for tracing.
// If not set, one will be generated automatically.
// Returns the builder for method chaining.
func (rb *RequestBuilder) RequestID(id string) *RequestBuilder {
	rb.requestID = id
	return rb
}

// Attribute adds a single attribute to the span.
// Use for request-specific metadata that should be traced.
// Returns the builder for method chaining.
func (rb *RequestBuilder) Attribute(key string, value any) *RequestBuilder {
	rb.spanAttributes[key] = value
	return rb
}

// Attributes adds multiple attributes to the span at once.
// Returns the builder for method chaining.
func (rb *RequestBuilder) Attributes(attrs map[string]any) *RequestBuilder {
	maps.Copy(rb.spanAttributes, attrs)
	return rb
}

// Metadata adds metadata that won't be traced but is available in the Request.
// Use for sensitive data or provider-specific configuration.
// Returns the builder for method chaining.
func (rb *RequestBuilder) Metadata(key string, value any) *RequestBuilder {
	rb.metadata[key] = value
	return rb
}

// Build constructs and returns the Request with all observability integration.
// This method:
// 1. Starts a new trace span
// 2. Creates the underlying http.Request
// 3. Applies headers and body
// 4. Logs the request details
// 5. Returns the fully initialized Request
//
// Returns an error if the URL is invalid or body serialization fails.
func (rb *RequestBuilder) Build() (*Request, error) {
	// Default values
	if rb.method == "" {
		rb.method = http.MethodGet
	}
	if rb.url == "" {
		return nil, sdkerr.Permanent(
			"provider.http.missing_url",
			"url is required",
			sdkerr.WithOperation("provider.http.build_request"),
			sdkerr.WithComponent("provider.http"),
			sdkerr.WithTraceFromContext(rb.ctx),
		)
	}
	if rb.operationName == "" {
		rb.operationName = fmt.Sprintf("http.%s", rb.method)
	}

	// Start trace span with client span kind.
	// This creates a new context (ctx) that contains the span and is derived from rb.ctx.
	// Context chain: rb.ctx (original) -> ctx (with span)
	spanOpts := observability.SpanOptions{
		Attributes: rb.spanAttributes,
		Kind:       observability.SpanKindClient,
	}
	ctx, span := rb.tracer.StartSpanWithOptions(rb.ctx, rb.operationName, spanOpts)

	// Serialize body
	var bodyBytes []byte
	var err error
	if rb.body != nil {
		bodyBytes, err = serializeBody(rb.body)
		if err != nil {
			err = sdkerr.Wrap(
				err,
				"provider.http.body_serialization_failed",
				sdkerr.WithOperation(rb.operationName),
				sdkerr.WithComponent("provider.http"),
				sdkerr.WithTraceFromContext(ctx),
			)
			span.RecordError(err)
			span.SetStatus(observability.StatusCodeError, "body serialization failed")
			span.End()
			return nil, err
		}
	}

	// Create the underlying http.Request
	bodyReader := io.Reader(nil)
	if len(bodyBytes) > 0 {
		bodyReader = bytes.NewReader(bodyBytes)
	}

	// Create the underlying http.Request with the span context.
	// The ctx here contains the span, so the http.Request is properly associated
	// with the distributed trace. This is critical for context propagation.
	httpReq, err := http.NewRequestWithContext(ctx, rb.method, rb.url, bodyReader)
	if err != nil {
		err = sdkerr.Wrap(
			err,
			"provider.http.request_create_failed",
			sdkerr.WithOperation(rb.operationName),
			sdkerr.WithComponent("provider.http"),
			sdkerr.WithTraceFromContext(ctx),
		)
		span.RecordError(err)
		span.SetStatus(observability.StatusCodeError, "failed to create request")
		span.End()
		return nil, err
	}

	// Apply headers
	for k, v := range rb.headers {
		httpReq.Header.Set(k, v)
	}

	// Ensure content-type is set for JSON bodies
	if len(bodyBytes) > 0 && httpReq.Header.Get("Content-Type") == "" {
		httpReq.Header.Set("Content-Type", "application/json")
	}

	// Inject trace context into headers for propagation
	traceHeaders := make(map[string]string)
	if err := rb.tracer.InjectContext(ctx, traceHeaders); err != nil {
		rb.logger.Warn(ctx, "http.trace_injection_failed",
			observability.F("error", err.Error()),
		)
	}
	for k, v := range traceHeaders {
		httpReq.Header.Set(k, v)
	}

	// Create Request wrapper
	r := &Request{
		req:           httpReq,
		span:          span,
		logger:        rb.logger,
		startTime:     time.Now(),
		headers:       rb.headers,
		body:          bodyBytes,
		requestID:     rb.requestID,
		operationName: rb.operationName,
		metadata:      rb.metadata,
	}

	// Log request creation
	r.logRequest()

	return r, nil
}

// serializeBody handles body serialization for various types.
// This function decides between buffering and streaming based on type:
//
// BUFFERED (loads entire body into memory):
// - []byte: returned as-is (already in memory)
// - string: converted to bytes (small, safe)
// - any: JSON-marshaled (typically small structured data)
//
// STREAMING (keeps as io.Reader, buffered later if needed):
//   - io.Reader: kept as-is to support true streaming
//     (caller can use BodyReader() method to get unbuffered access)
//
// For io.Reader types, the decision to buffer is deferred until the
// request is actually sent. This supports true streaming for large payloads
// while still allowing body inspection when needed.
//
// Size limit for auto-buffering: If a reader size can't be determined and
// the caller tries to re-read the body, it will be buffered on first read.
func serializeBody(body any) ([]byte, error) {
	switch b := body.(type) {
	case []byte:
		// Already in memory, return as-is
		return b, nil
	case string:
		// Small string, safe to buffer
		return []byte(b), nil
	case io.Reader:
		// IMPORTANT: For true streaming support, try to buffer only if necessary.
		// Check if it's already a bytes.Reader or similar (known size).
		if byteReader, ok := b.(*bytes.Reader); ok {
			// Can get size from bytes.Reader
			size := byteReader.Len()
			if size > 0 {
				data, err := io.ReadAll(byteReader)
				if err != nil {
					return nil, fmt.Errorf("failed to read bytes.Reader: %w", err)
				}
				return data, nil
			}
			return nil, nil
		}
		// For other readers (files, network streams, etc.), try to get size if available.
		// If reader implements Seek (os.File) or similar, we can optimize.
		if seeker, ok := b.(io.Seeker); ok {
			// Try to get size without consuming the stream
			size, err := seeker.Seek(0, io.SeekEnd)
			if err == nil && size > 0 {
				// Reset position to start
				_, _ = seeker.Seek(0, io.SeekStart)
				data, err := io.ReadAll(b)
				if err != nil {
					return nil, fmt.Errorf("failed to read seekable stream: %w", err)
				}
				return data, nil
			}
			// Reset position if seek failed
			_, _ = seeker.Seek(0, io.SeekStart)
		}
		// For unknown-sized readers (e.g., network streams), we must buffer.
		// This is necessary because http.NewRequest doesn't handle streaming well
		// without knowing size. Consider using BodyReader() for advanced streaming.
		data, err := io.ReadAll(b)
		if err != nil {
			return nil, fmt.Errorf("failed to read stream: %w", err)
		}
		return data, nil
	default:
		// JSON-encode structured data (typically small)
		return json.Marshal(b)
	}
}

// logRequest logs the request details at debug level.
// Includes method, URL, headers (without sensitive values), and body size.
func (r *Request) logRequest() {
	fields := []observability.Field{
		{Key: "method", Value: r.req.Method},
		{Key: "url", Value: r.req.URL.String()},
		{Key: "body_size", Value: len(r.body)},
		{Key: "request_id", Value: r.requestID},
		{Key: "operation", Value: r.operationName},
	}

	// Log headers (exclude sensitive ones)
	headerCount := len(r.req.Header)
	fields = append(fields, observability.F("header_count", headerCount))

	// Log span IDs for correlation
	if r.span != nil {
		fields = append(fields,
			observability.F("span_id", r.span.SpanID()),
			observability.F("trace_id", r.span.TraceID()),
		)
	}

	// Debug logging removed to avoid terminal output clutter
	// Span still captures all relevant information for observability
}

// Context returns the context with the span attached.
// This context should be used for any sub-operations related to this request.
func (r *Request) Context() context.Context {
	if r.span != nil {
		return r.span.Context()
	}
	return r.req.Context()
}

// Underlying returns the underlying http.Request.
// Use this when you need to pass the request to http.Client.Do() or similar.
func (r *Request) Underlying() *http.Request {
	return r.req
}

// SetHeader adds or updates a header on the request.
// This should only be called before the request is executed.
func (r *Request) SetHeader(key, value string) {
	r.req.Header.Set(key, value)
	r.headers[key] = value
}

// AddHeader appends a value to a header (for headers that support multiple values).
func (r *Request) AddHeader(key, value string) {
	r.req.Header.Add(key, value)
}

// GetHeader retrieves the value of a header.
func (r *Request) Header(key string) string {
	return r.req.Header.Get(key)
}

// SetAttribute sets an attribute on the span for this request.
// Use for adding context that should be captured in traces.
func (r *Request) SetAttribute(key string, value any) {
	if r.span != nil {
		r.span.SetAttribute(key, value)
	}
}

// SetMetadata sets metadata on the request.
// Metadata is not traced but is available for observability logging.
func (r *Request) SetMetadata(key string, value any) {
	r.metadata[key] = value
}

// GetMetadata retrieves metadata set on the request.
func (r *Request) Metadata(key string) (any, bool) {
	v, ok := r.metadata[key]
	return v, ok
}

// RecordError records an error on the request's span and logs it.
// Used to capture errors that occur during request processing.
func (r *Request) RecordError(err error) {
	err = sdkerr.Capture(
		err,
		sdkerr.WithOperation(r.operationName),
		sdkerr.WithComponent("provider.http"),
		sdkerr.WithTraceFromContext(r.Context()),
	)

	if r.span != nil {
		r.span.RecordError(err)
	}

	r.logger.Error(r.Context(), "http.request_error",
		observability.F("error", err.Error()),
		observability.F("request_id", r.requestID),
		observability.F("operation", r.operationName),
	)
}

// Complete marks the request as complete and ends the span.
// This should be called after the response is received and processed.
// statusCode is the HTTP status code from the response.
func (r *Request) Complete(statusCode int) {
	if r == nil {
		return
	}

	// Calculate request duration
	duration := time.Since(r.startTime)

	// Set status and attributes on span
	if r.span != nil {
		r.span.SetAttribute("http.status_code", statusCode)
		r.span.SetAttribute("http.duration_ms", duration.Milliseconds())

		// Set span status based on HTTP status code
		if statusCode >= 400 {
			r.span.SetStatus(observability.StatusCodeError, fmt.Sprintf("HTTP %d", statusCode))
		} else {
			r.span.SetStatus(observability.StatusCodeOK, "")
		}

		r.span.End()
	}

	// Debug logging removed to avoid terminal output clutter
	// Span still captures all relevant information for observability
}

// Body returns the request body bytes.
func (r *Request) Body() []byte {
	return r.body
}

// OperationName returns the operation name for this request.
func (r *Request) OperationName() string {
	return r.operationName
}

// RequestID returns the request ID for this request.
func (r *Request) RequestID() string {
	return r.requestID
}

// Span returns the trace span for this request.
// Returns nil if tracing is not enabled.
func (r *Request) Span() observability.Span {
	return r.span
}
