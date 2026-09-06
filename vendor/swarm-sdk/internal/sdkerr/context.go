package sdkerr

import "context"

type traceContextKey struct{}

type traceContext struct {
	TraceID string
	SpanID  string
}

// ContextWithTrace stores trace/span IDs in context for error construction helpers.
func ContextWithTrace(ctx context.Context, traceID, spanID string) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	if traceID == "" && spanID == "" {
		return ctx
	}
	return context.WithValue(ctx, traceContextKey{}, traceContext{TraceID: traceID, SpanID: spanID})
}

// TraceFromContext extracts trace/span IDs previously added with ContextWithTrace.
func TraceFromContext(ctx context.Context) (traceID string, spanID string) {
	if ctx == nil {
		return "", ""
	}
	value, ok := ctx.Value(traceContextKey{}).(traceContext)
	if !ok {
		return "", ""
	}
	return value.TraceID, value.SpanID
}
