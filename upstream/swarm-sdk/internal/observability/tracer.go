package observability

import (
	"context"
	"time"
)

// Span represents a unit of work in a distributed trace.
// Spans form a tree structure with parent-child relationships.
type Span interface {
	// End completes the span and records its duration.
	End()

	// SetAttribute adds a key-value attribute to the span.
	SetAttribute(key string, value any)

	// SetAttributes adds multiple attributes at once.
	SetAttributes(attrs map[string]any)

	// SetStatus sets the status of the span (ok, error).
	SetStatus(code StatusCode, message string)

	// RecordError records an error on the span.
	RecordError(err error)

	// SpanID returns the unique identifier for this span.
	SpanID() string

	// TraceID returns the trace ID this span belongs to.
	TraceID() string

	// Context returns a context with this span attached.
	Context() context.Context
}

// StatusCode represents the status of a span.
type StatusCode int

const (
	// StatusCodeUnset indicates the status has not been set.
	StatusCodeUnset StatusCode = iota

	// StatusCodeOK indicates the operation completed successfully.
	StatusCodeOK

	// StatusCodeError indicates the operation encountered an error.
	StatusCodeError
)

// Tracer defines the interface for distributed tracing.
// Follows OpenTelemetry semantics for compatibility.
type Tracer interface {
	// StartSpan creates a new span with the given name.
	// Returns a new context with the span attached and the span itself.
	StartSpan(ctx context.Context, name string) (context.Context, Span)

	// StartSpanWithOptions creates a new span with additional options.
	StartSpanWithOptions(ctx context.Context, name string, opts SpanOptions) (context.Context, Span)

	// SpanFromContext extracts the current span from the context.
	// Returns nil if no span is present.
	SpanFromContext(ctx context.Context) Span

	// InjectContext injects trace context into a carrier (for propagation).
	// Used when making HTTP requests or other cross-process calls.
	InjectContext(ctx context.Context, carrier map[string]string) error

	// ExtractContext extracts trace context from a carrier.
	// Used when receiving HTTP requests or other cross-process calls.
	ExtractContext(carrier map[string]string) (context.Context, error)
}

// SpanOptions provides additional configuration for span creation.
type SpanOptions struct {
	// Attributes are initial attributes to set on the span.
	Attributes map[string]any

	// StartTime sets a custom start time (defaults to time.Now()).
	StartTime time.Time

	// Kind indicates the role of the span (client, server, internal, etc.).
	Kind SpanKind
}

// SpanKind represents the role of a span in the trace.
type SpanKind int

const (
	// SpanKindInternal indicates an internal operation.
	SpanKindInternal SpanKind = iota

	// SpanKindServer indicates a server handling a request.
	SpanKindServer

	// SpanKindClient indicates a client making a request.
	SpanKindClient

	// SpanKindProducer indicates a producer sending a message.
	SpanKindProducer

	// SpanKindConsumer indicates a consumer receiving a message.
	SpanKindConsumer
)

// StandardSpanAttributes defines standard attribute keys.
const (
	// Component identifies which SDK component created the span.
	AttrComponent = "component"

	// Operation identifies the operation being performed.
	AttrOperation = "operation"

	// Provider identifies the LLM provider.
	AttrProvider = "provider"

	// Model identifies the LLM model.
	AttrModel = "model"

	// Agent identifies the agent.
	AttrAgent = "agent"

	// Conversation identifies the conversation.
	AttrConversation = "conversation"

	// Mode identifies the mode.
	AttrMode = "mode"

	// Group identifies the agent group.
	AttrGroup = "group"

	// Tool identifies the tool being executed.
	AttrTool = "tool"

	// Tokens tracks token usage.
	AttrTokens = "tokens"

	// Cost tracks cost in USD.
	AttrCost = "cost"

	// CacheHit indicates if result was from cache.
	AttrCacheHit = "cache_hit"

	// ErrorType tracks the error type.
	AttrErrorType = "error.type"

	// ErrorCategory tracks the error category.
	AttrErrorCategory = "error.category"

	// Retryable indicates if the operation can be retried.
	AttrRetryable = "retryable"
)
