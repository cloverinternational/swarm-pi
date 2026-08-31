package observability

import (
	"context"
	"fmt"
	"maps"
	"time"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/observability"
)

// TUITracer implements observability.Tracer with minimal overhead for TUI
type TUITracer struct {
	logger *TUILogger
}

// NewTUITracer creates a no-op tracer that optionally logs to debug
func NewTUITracer(logger *TUILogger) *TUITracer {
	return &TUITracer{
		logger: logger,
	}
}

// StartSpan creates a new span
func (t *TUITracer) StartSpan(ctx context.Context, name string) (context.Context, observability.Span) {
	return t.StartSpanWithOptions(ctx, name, observability.SpanOptions{})
}

// StartSpanWithOptions creates a new span with options
func (t *TUITracer) StartSpanWithOptions(ctx context.Context, name string, opts observability.SpanOptions) (context.Context, observability.Span) {
	span := &TUISpan{
		name:       name,
		spanID:     generateSpanID(),
		traceID:    extractOrGenerateTraceID(ctx),
		startTime:  time.Now(),
		attributes: make(map[string]any),
		logger:     t.logger,
	}

	// Copy attributes from options
	if opts.Attributes != nil {
		maps.Copy(span.attributes, opts.Attributes)
	}

	// Store span in context
	newCtx := context.WithValue(ctx, spanContextKey, span)
	return newCtx, span
}

// SpanFromContext extracts a span from context
func (t *TUITracer) SpanFromContext(ctx context.Context) observability.Span {
	if span, ok := ctx.Value(spanContextKey).(*TUISpan); ok {
		return span
	}
	return nil
}

// InjectContext injects trace context into carrier
func (t *TUITracer) InjectContext(ctx context.Context, carrier map[string]string) error {
	if span := t.SpanFromContext(ctx); span != nil {
		if tuiSpan, ok := span.(*TUISpan); ok {
			carrier["trace-id"] = tuiSpan.traceID
			carrier["span-id"] = tuiSpan.spanID
		}
	}
	return nil
}

// ExtractContext extracts trace context from carrier
func (t *TUITracer) ExtractContext(carrier map[string]string) (context.Context, error) {
	ctx := context.Background()
	if traceID, ok := carrier["trace-id"]; ok && traceID != "" {
		ctx = context.WithValue(ctx, traceIDKey, traceID)
	}
	return ctx, nil
}

// TUISpan implements observability.Span
type TUISpan struct {
	name       string
	spanID     string
	traceID    string
	startTime  time.Time
	endTime    time.Time
	attributes map[string]any
	status     observability.StatusCode
	message    string
	logger     *TUILogger
}

// End completes the span
func (s *TUISpan) End() {
	s.endTime = time.Now()
	duration := s.endTime.Sub(s.startTime)

	// Optionally log span completion (disabled by default for performance)
	_ = duration // Can be enabled for debugging
}

// SetAttribute adds an attribute
func (s *TUISpan) SetAttribute(key string, value any) {
	s.attributes[key] = value
}

// SetAttributes adds multiple attributes
func (s *TUISpan) SetAttributes(attrs map[string]any) {
	maps.Copy(s.attributes, attrs)
}

// SetStatus sets the span status
func (s *TUISpan) SetStatus(code observability.StatusCode, message string) {
	s.status = code
	s.message = message

	// Log errors
	if code == observability.StatusCodeError && s.logger != nil {
		s.logger.Error(context.Background(), "span.error",
			observability.F("span", s.name),
			observability.F("message", message),
		)
	}
}

// RecordError records an error
func (s *TUISpan) RecordError(err error) {
	if s.logger != nil {
		s.logger.Error(context.Background(), "span.error",
			observability.F("span", s.name),
			observability.F("error", err.Error()),
		)
	}
	s.SetStatus(observability.StatusCodeError, err.Error())
}

// SpanID returns the span ID
func (s *TUISpan) SpanID() string {
	return s.spanID
}

// TraceID returns the trace ID
func (s *TUISpan) TraceID() string {
	return s.traceID
}

// Context returns the context with this span
func (s *TUISpan) Context() context.Context {
	return context.WithValue(context.Background(), spanContextKey, s)
}

// Context keys
type contextKey string

const (
	spanContextKey contextKey = "tui_span"
	traceIDKey     contextKey = "trace_id"
)

// Helper functions
var spanCounter uint64

func generateSpanID() string {
	return fmt.Sprintf("span-%d-%d", time.Now().UnixNano(), spanCounter)
}

func extractOrGenerateTraceID(ctx context.Context) string {
	if traceID, ok := ctx.Value(traceIDKey).(string); ok && traceID != "" {
		return traceID
	}
	return fmt.Sprintf("trace-%d", time.Now().UnixNano())
}
