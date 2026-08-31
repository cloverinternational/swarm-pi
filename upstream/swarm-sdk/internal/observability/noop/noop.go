// Package noop provides no-op implementations of observability interfaces.
// These implementations satisfy the observability.Logger and observability.Tracer interfaces
// but perform no actual logging or tracing. Use them in tests or when observability is not needed.
package noop

import (
	"context"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/observability"
)

// ── Logger ────────────────────────────────────────────────────────────────────

type noopLogger struct{}

// NewLogger returns a no-op Logger that discards all log events.
// This is useful for tests and contexts where logging is not needed.
func NewLogger() observability.Logger { return &noopLogger{} }
func (l *noopLogger) Log(_ context.Context, _ observability.Level, _ string, _ ...observability.Field) {
}
func (l *noopLogger) Trace(_ context.Context, _ string, _ ...observability.Field) {}
func (l *noopLogger) Debug(_ context.Context, _ string, _ ...observability.Field) {}
func (l *noopLogger) Info(_ context.Context, _ string, _ ...observability.Field)  {}
func (l *noopLogger) Warn(_ context.Context, _ string, _ ...observability.Field)  {}
func (l *noopLogger) Error(_ context.Context, _ string, _ ...observability.Field) {}
func (l *noopLogger) Fatal(_ context.Context, _ string, _ ...observability.Field) {}
func (l *noopLogger) WithFields(_ ...observability.Field) observability.Logger    { return l }
func (l *noopLogger) SetLevel(_ observability.Level)                              {}

// ── Tracer ────────────────────────────────────────────────────────────────────

type noopTracer struct{}
type noopSpan struct{ ctx context.Context }

// NewTracer returns a no-op Tracer that discards all tracing spans and events.
// This is useful for tests and contexts where tracing is not needed.
func NewTracer() observability.Tracer { return &noopTracer{} }

func (t *noopTracer) StartSpan(ctx context.Context, _ string) (context.Context, observability.Span) {
	return ctx, &noopSpan{ctx: ctx}
}
func (t *noopTracer) StartSpanWithOptions(ctx context.Context, _ string, _ observability.SpanOptions) (context.Context, observability.Span) {
	return ctx, &noopSpan{ctx: ctx}
}
func (t *noopTracer) SpanFromContext(_ context.Context) observability.Span       { return &noopSpan{} }
func (t *noopTracer) InjectContext(_ context.Context, _ map[string]string) error { return nil }
func (t *noopTracer) ExtractContext(_ map[string]string) (context.Context, error) {
	return context.Background(), nil
}

func (s *noopSpan) End()                                           {}
func (s *noopSpan) SetAttribute(_ string, _ any)                   {}
func (s *noopSpan) SetAttributes(_ map[string]any)                 {}
func (s *noopSpan) SetStatus(_ observability.StatusCode, _ string) {}
func (s *noopSpan) RecordError(_ error)                            {}
func (s *noopSpan) SpanID() string                                 { return "" }
func (s *noopSpan) TraceID() string                                { return "" }
func (s *noopSpan) Context() context.Context {
	if s.ctx != nil {
		return s.ctx
	}
	return context.Background()
}

// ── Auditor ───────────────────────────────────────────────────────────────────

type noopAuditor struct{}

// NewAuditor returns a no-op Auditor that discards all audit events.
// Use as a default when audit logging is not required.
func NewAuditor() observability.Auditor { return &noopAuditor{} }

func (a *noopAuditor) Record(_ context.Context, _ observability.AuditEvent) error { return nil }
func (a *noopAuditor) Query(_ context.Context, _ observability.AuditCriteria) ([]observability.AuditEvent, error) {
	return nil, nil
}
