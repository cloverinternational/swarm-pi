package observability

import (
	"context"
	"time"
)

// Field represents a key-value pair for structured logging.
type Field struct {
	Key   string
	Value any
}

// F creates a log Field. It is a short-form alias for Field{Key: k, Value: v}
// that keeps log call-sites concise:
//
//	logger.Info(ctx, "agent.started", observability.F("id", id), observability.F("model", model))
func F(key string, value any) Field {
	return Field{Key: key, Value: value}
}

// Logger defines the interface for structured logging.
// All logging in the SDK goes through this interface.
type Logger interface {
	// Log emits a structured log entry with the given level, event name, and fields.
	// The event name should be dot-notation (e.g., "provider.request", "tool.executed").
	Log(ctx context.Context, level Level, event string, fields ...Field)

	// Trace logs at trace level.
	Trace(ctx context.Context, event string, fields ...Field)

	// Debug logs at debug level.
	Debug(ctx context.Context, event string, fields ...Field)

	// Info logs at info level.
	Info(ctx context.Context, event string, fields ...Field)

	// Warn logs at warn level.
	Warn(ctx context.Context, event string, fields ...Field)

	// Error logs at error level.
	Error(ctx context.Context, event string, fields ...Field)
}

// LogEntry represents a single structured log entry.
// This is the canonical format that all logger implementations must produce.
type LogEntry struct {
	// Timestamp when the log was created (RFC3339 format).
	Timestamp time.Time `json:"timestamp"`

	// Level is the severity level.
	Level string `json:"level"`

	// Event is the dot-notation event name.
	Event string `json:"event"`

	// TraceID links to distributed trace.
	TraceID string `json:"trace_id,omitempty"`

	// SpanID identifies the current span.
	SpanID string `json:"span_id,omitempty"`

	// ParentSpanID identifies the parent span.
	ParentSpanID string `json:"parent_span_id,omitempty"`

	// ConversationID identifies the conversation.
	ConversationID string `json:"conversation_id,omitempty"`

	// ModeID identifies the active mode.
	ModeID string `json:"mode_id,omitempty"`

	// GroupID identifies the agent group.
	GroupID string `json:"group_id,omitempty"`

	// AgentID identifies the agent.
	AgentID string `json:"agent_id,omitempty"`

	// Provider identifies the LLM provider.
	Provider string `json:"provider,omitempty"`

	// Model identifies the LLM model.
	Model string `json:"model,omitempty"`

	// Component identifies which SDK component generated the log.
	Component string `json:"component,omitempty"`

	// Data contains additional structured fields.
	Data map[string]any `json:"data,omitempty"`

	// Error contains error information if present.
	Error *ErrorInfo `json:"error,omitempty"`
}

// ErrorInfo contains structured error information for logs.
type ErrorInfo struct {
	Type       string         `json:"type"`
	Message    string         `json:"message"`
	Retryable  bool           `json:"retryable"`
	Category   string         `json:"category"`
	Context    map[string]any `json:"context,omitempty"`
	StackTrace string         `json:"stack_trace,omitempty"`
}
