package observability

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"maps"
	"strings"
	"sync"
	"time"

	sdkerr "github.com/Swarm-Code/mono/swarm-sdk/internal/sdkerr"
)

type spanContextKey struct{}

type propagatedTraceContextKey struct{}

type propagatedTraceContext struct {
	TraceID string
	SpanID  string
}

// LocalTracerConfig controls local-first trace recording behavior.
type LocalTracerConfig struct {
	Sink        Sink
	LookupStore LookupStore
	Redactor    Redactor
	Component   string
}

// NewLocalTracer builds a context-aware tracer with optional sink/store.
func NewLocalTracer(config LocalTracerConfig) Tracer {
	redactor := config.Redactor
	if redactor == nil {
		redactor = NewDefaultRedactor()
	}

	return &localTracer{
		sink:      config.Sink,
		lookup:    config.LookupStore,
		redactor:  redactor,
		component: config.Component,
		now:       time.Now,
	}
}

// NewLocalJSONLTracer is a convenience constructor for local JSONL diagnostics.
func NewLocalJSONLTracer(path string) (Tracer, *InMemoryLookupStore, Sink, error) {
	sink, err := NewJSONLSink(path)
	if err != nil {
		return nil, nil, nil, err
	}
	store := NewInMemoryLookupStore()
	tracer := NewLocalTracer(LocalTracerConfig{
		Sink:        sink,
		LookupStore: store,
		Redactor:    NewDefaultRedactor(),
	})
	return tracer, store, sink, nil
}

type localTracer struct {
	sink      Sink
	lookup    LookupStore
	redactor  Redactor
	component string
	now       func() time.Time
}

func (t *localTracer) StartSpan(ctx context.Context, name string) (context.Context, Span) {
	return t.StartSpanWithOptions(ctx, name, SpanOptions{})
}

func (t *localTracer) StartSpanWithOptions(ctx context.Context, name string, opts SpanOptions) (context.Context, Span) {
	if ctx == nil {
		ctx = context.Background()
	}

	start := opts.StartTime
	if start.IsZero() {
		start = t.now().UTC()
	}

	traceID := ""
	parentSpanID := ""
	if parent, ok := ctx.Value(spanContextKey{}).(*localSpan); ok && parent != nil {
		traceID = parent.traceID
		parentSpanID = parent.spanID
	} else if propagated, ok := ctx.Value(propagatedTraceContextKey{}).(propagatedTraceContext); ok {
		traceID = propagated.TraceID
		parentSpanID = propagated.SpanID
	} else {
		tid, sid := sdkerr.TraceFromContext(ctx)
		traceID = tid
		parentSpanID = sid
	}
	if traceID == "" {
		traceID = newTraceID()
	}
	spanID := newSpanID()

	attrs := copyAttributes(opts.Attributes)
	if attrs == nil {
		attrs = make(map[string]any)
	}
	if opts.Kind != SpanKindInternal {
		attrs["span.kind"] = int(opts.Kind)
	}

	component := t.componentFrom(name, attrs)

	span := &localSpan{
		tracer:        t,
		name:          name,
		traceID:       traceID,
		spanID:        spanID,
		parentSpanID:  parentSpanID,
		component:     component,
		startTime:     start,
		attributes:    attrs,
		statusCode:    StatusCodeUnset,
		statusMessage: "",
	}

	spanCtx := context.WithValue(ctx, spanContextKey{}, span)
	spanCtx = sdkerr.ContextWithTrace(spanCtx, traceID, spanID)
	span.ctx = spanCtx

	t.emit(spanCtx, TraceEvent{
		EventType:    EventTypeSpanStart,
		Timestamp:    start,
		TraceID:      traceID,
		SpanID:       spanID,
		ParentSpanID: parentSpanID,
		Operation:    name,
		Component:    component,
		Status:       statusLabel(StatusCodeUnset),
		Attributes:   copyAttributes(attrs),
	})

	return spanCtx, span
}

func (t *localTracer) SpanFromContext(ctx context.Context) Span {
	if ctx == nil {
		return nil
	}
	if span, ok := ctx.Value(spanContextKey{}).(*localSpan); ok {
		return span
	}
	return nil
}

func (t *localTracer) InjectContext(ctx context.Context, carrier map[string]string) error {
	if carrier == nil {
		return fmt.Errorf("carrier is required")
	}

	var traceID, spanID string
	if span, ok := t.SpanFromContext(ctx).(*localSpan); ok && span != nil {
		traceID = span.traceID
		spanID = span.spanID
	} else {
		traceID, spanID = sdkerr.TraceFromContext(ctx)
	}

	if traceID != "" {
		carrier["x-trace-id"] = traceID
	}
	if spanID != "" {
		carrier["x-span-id"] = spanID
	}
	return nil
}

func (t *localTracer) ExtractContext(carrier map[string]string) (context.Context, error) {
	traceID := carrier["x-trace-id"]
	spanID := carrier["x-span-id"]
	if traceID == "" {
		if traceparent := carrier["traceparent"]; traceparent != "" {
			parts := strings.Split(traceparent, "-")
			if len(parts) >= 3 {
				traceID = parts[1]
				spanID = parts[2]
			}
		}
	}

	ctx := context.Background()
	if traceID == "" {
		return ctx, nil
	}

	ctx = context.WithValue(ctx, propagatedTraceContextKey{}, propagatedTraceContext{
		TraceID: traceID,
		SpanID:  spanID,
	})
	ctx = sdkerr.ContextWithTrace(ctx, traceID, spanID)
	return ctx, nil
}

func (t *localTracer) emit(ctx context.Context, event TraceEvent) {
	if event.Timestamp.IsZero() {
		event.Timestamp = t.now().UTC()
	}
	if event.Attributes != nil && t.redactor != nil {
		event.Attributes = t.redactor.RedactAttributes(event.Attributes)
	}

	if t.lookup != nil {
		_ = t.lookup.StoreEvent(event)
	}
	if t.sink != nil {
		_ = t.sink.WriteEvent(ctx, event)
	}
}

func (t *localTracer) componentFrom(operation string, attrs map[string]any) string {
	if attrs != nil {
		if v, ok := attrs[AttrComponent].(string); ok && v != "" {
			return v
		}
		if v, ok := attrs["component"].(string); ok && v != "" {
			return v
		}
	}
	if t.component != "" {
		return t.component
	}
	if operation == "" {
		return "sdk"
	}
	if i := strings.Index(operation, "."); i > 0 {
		return operation[:i]
	}
	return "sdk"
}

type localSpan struct {
	mu            sync.Mutex
	tracer        *localTracer
	ctx           context.Context
	name          string
	component     string
	traceID       string
	spanID        string
	parentSpanID  string
	startTime     time.Time
	attributes    map[string]any
	statusCode    StatusCode
	statusMessage string
	ended         bool
}

func (s *localSpan) End() {
	s.mu.Lock()
	if s.ended {
		s.mu.Unlock()
		return
	}
	s.ended = true
	duration := time.Since(s.startTime)
	attributes := copyAttributes(s.attributes)
	status := statusLabel(s.statusCode)
	message := s.statusMessage
	s.mu.Unlock()

	s.tracer.emit(s.ctx, TraceEvent{
		EventType:    EventTypeSpanEnd,
		Timestamp:    time.Now().UTC(),
		TraceID:      s.traceID,
		SpanID:       s.spanID,
		ParentSpanID: s.parentSpanID,
		Operation:    s.name,
		Component:    s.component,
		Status:       status,
		DurationMs:   duration.Milliseconds(),
		Message:      message,
		Attributes:   attributes,
	})
}

func (s *localSpan) SetAttribute(key string, value any) {
	if key == "" {
		return
	}
	s.mu.Lock()
	if s.attributes == nil {
		s.attributes = make(map[string]any)
	}
	s.attributes[key] = value
	s.mu.Unlock()
}

func (s *localSpan) SetAttributes(attrs map[string]any) {
	if len(attrs) == 0 {
		return
	}
	s.mu.Lock()
	if s.attributes == nil {
		s.attributes = make(map[string]any, len(attrs))
	}
	maps.Copy(s.attributes, attrs)
	s.mu.Unlock()
}

func (s *localSpan) SetStatus(code StatusCode, message string) {
	s.mu.Lock()
	s.statusCode = code
	s.statusMessage = message
	s.mu.Unlock()
}

func (s *localSpan) RecordError(err error) {
	if err == nil {
		return
	}

	s.SetStatus(StatusCodeError, err.Error())

	captured := sdkerr.Capture(
		err,
		sdkerr.WithTraceID(s.traceID),
		sdkerr.WithSpanID(s.spanID),
		sdkerr.WithOperation(s.name),
		sdkerr.WithComponent(s.component),
		sdkerr.WithTraceFromContext(s.ctx),
	)
	if original, ok := err.(*sdkerr.Error); ok && captured != nil {
		*original = *captured
	}

	attrs := s.snapshotAttributes()
	if captured != nil && captured.Code != "" {
		if attrs == nil {
			attrs = make(map[string]any)
		}
		attrs["error_code"] = captured.Code
	}

	errorID := sdkerr.GetErrorID(captured)
	if errorID == "" {
		errorID = sdkerr.GetErrorID(err)
	}

	s.tracer.emit(s.ctx, TraceEvent{
		EventType:     EventTypeSpanError,
		Timestamp:     time.Now().UTC(),
		TraceID:       s.traceID,
		SpanID:        s.spanID,
		ParentSpanID:  s.parentSpanID,
		Operation:     s.name,
		Component:     s.component,
		ErrorID:       errorID,
		ParentErrorID: sdkerr.GetParentErrorID(captured),
		Status:        statusLabel(StatusCodeError),
		Message:       err.Error(),
		Attributes:    attrs,
	})
}

func (s *localSpan) SpanID() string {
	return s.spanID
}

func (s *localSpan) TraceID() string {
	return s.traceID
}

func (s *localSpan) Context() context.Context {
	if s.ctx == nil {
		return context.Background()
	}
	return s.ctx
}

func (s *localSpan) snapshotAttributes() map[string]any {
	s.mu.Lock()
	defer s.mu.Unlock()
	return copyAttributes(s.attributes)
}

func copyAttributes(input map[string]any) map[string]any {
	if len(input) == 0 {
		return nil
	}
	out := make(map[string]any, len(input))
	maps.Copy(out, input)
	return out
}

func newTraceID() string {
	var buf [16]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return fmt.Sprintf("trace_%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(buf[:])
}

func newSpanID() string {
	var buf [8]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return fmt.Sprintf("span_%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(buf[:])
}

func statusLabel(code StatusCode) string {
	switch code {
	case StatusCodeOK:
		return "ok"
	case StatusCodeError:
		return "error"
	default:
		return "unset"
	}
}
