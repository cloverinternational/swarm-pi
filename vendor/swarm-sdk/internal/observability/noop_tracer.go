package observability

import "context"

// NewNoopTracer returns a no-op tracer implementation.
func NewNoopTracer() Tracer {
	return &noopTracer{}
}

type noopTracer struct{}

func (t *noopTracer) StartSpan(ctx context.Context, _ string) (context.Context, Span) {
	return ctx, &noopSpan{ctx: ctx}
}

func (t *noopTracer) StartSpanWithOptions(ctx context.Context, _ string, _ SpanOptions) (context.Context, Span) {
	return ctx, &noopSpan{ctx: ctx}
}

func (t *noopTracer) SpanFromContext(ctx context.Context) Span {
	return &noopSpan{ctx: ctx}
}

func (t *noopTracer) InjectContext(_ context.Context, _ map[string]string) error {
	return nil
}

func (t *noopTracer) ExtractContext(_ map[string]string) (context.Context, error) {
	return context.Background(), nil
}

type noopSpan struct {
	ctx context.Context
}

func (s *noopSpan) End() {}

func (s *noopSpan) SetAttribute(_ string, _ any) {}

func (s *noopSpan) SetAttributes(_ map[string]any) {}

func (s *noopSpan) SetStatus(_ StatusCode, _ string) {}

func (s *noopSpan) RecordError(_ error) {}

func (s *noopSpan) SpanID() string {
	return ""
}

func (s *noopSpan) TraceID() string {
	return ""
}

func (s *noopSpan) Context() context.Context {
	if s.ctx == nil {
		return context.Background()
	}
	return s.ctx
}
