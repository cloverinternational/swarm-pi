package observability

import "context"

// NewNopLogger returns a logger that discards all log entries.
func NewNopLogger() Logger {
	return &nopLogger{}
}

type nopLogger struct{}

func (n *nopLogger) Log(ctx context.Context, level Level, event string, fields ...Field) {
}

func (n *nopLogger) Trace(ctx context.Context, event string, fields ...Field) {
}

func (n *nopLogger) Debug(ctx context.Context, event string, fields ...Field) {
}

func (n *nopLogger) Info(ctx context.Context, event string, fields ...Field) {
}

func (n *nopLogger) Warn(ctx context.Context, event string, fields ...Field) {
}

func (n *nopLogger) Error(ctx context.Context, event string, fields ...Field) {
}

func (n *nopLogger) Fatal(ctx context.Context, event string, fields ...Field) {
}

func (n *nopLogger) WithFields(fields ...Field) Logger {
	return n
}

func (n *nopLogger) SetLevel(level Level) {
}
