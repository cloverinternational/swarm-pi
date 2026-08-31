package observability

import (
	"context"
	"time"
)

const (
	// EventTypeSpanStart is emitted when a span starts.
	EventTypeSpanStart = "span_start"
	// EventTypeSpanEnd is emitted when a span ends.
	EventTypeSpanEnd = "span_end"
	// EventTypeSpanError is emitted when a span records an error.
	EventTypeSpanError = "span_error"
)

// TraceEvent is the persisted/source-of-truth event schema for diagnostics.
type TraceEvent struct {
	Sequence      int64          `json:"sequence,omitempty"`
	EventType     string         `json:"event_type"`
	Timestamp     time.Time      `json:"timestamp"`
	TraceID       string         `json:"trace_id"`
	SpanID        string         `json:"span_id"`
	ParentSpanID  string         `json:"parent_span_id,omitempty"`
	Operation     string         `json:"operation,omitempty"`
	Component     string         `json:"component,omitempty"`
	ErrorID       string         `json:"error_id,omitempty"`
	ParentErrorID string         `json:"parent_error_id,omitempty"`
	Status        string         `json:"status,omitempty"`
	DurationMs    int64          `json:"duration_ms,omitempty"`
	Message       string         `json:"message,omitempty"`
	Attributes    map[string]any `json:"attributes,omitempty"`
	File          string         `json:"file,omitempty"`
	Line          int            `json:"line,omitempty"`
}

// Sink persists trace events (JSONL file, remote collector, etc.).
type Sink interface {
	WriteEvent(ctx context.Context, event TraceEvent) error
	Close() error
}

// LookupStore indexes events for ErrorID-driven diagnostics lookup.
type LookupStore interface {
	StoreEvent(event TraceEvent) error
	LookupError(errorID string) (*LineageReport, error)
}

// Redactor redacts sensitive payloads before persistence.
type Redactor interface {
	RedactAttributes(attrs map[string]any) map[string]any
	RedactValue(key string, value any) any
}

// Exporter is the forward-compatible external export contract.
// OTLP implementation is intentionally deferred.
type Exporter interface {
	Export(ctx context.Context, events []TraceEvent) error
}
