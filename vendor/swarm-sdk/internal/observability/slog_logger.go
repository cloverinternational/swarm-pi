package observability

import (
	"context"
	"log/slog"
)

// slog level constants for levels that don't map directly to slog's predefined levels.
const (
	// SlogLevelTrace is the slog level used for Trace events.
	// slog doesn't define Trace, so we use Debug-4.
	SlogLevelTrace = slog.Level(-8)

	// SlogLevelFatal is the slog level used for Fatal events.
	// slog doesn't define Fatal, so we use Error+4.
	SlogLevelFatal = slog.Level(12)
)

// slogLogger adapts a *slog.Logger to the observability.Logger interface.
// This lets you plug any slog-compatible backend (zerolog, zap, etc.) into the
// SDK without writing a custom adapter.
//
// Use NewSlogLogger to create one:
//
//	logger := observability.NewSlogLogger(slog.Default())
type slogLogger struct {
	l *slog.Logger
}

// NewSlogLogger wraps a *slog.Logger so it satisfies the observability.Logger
// interface used throughout the SDK.
//
// Level mapping:
//
//	observability.LevelTrace  →  slog.Level(-8)
//	observability.LevelDebug  →  slog.LevelDebug (-4)
//	observability.LevelInfo   →  slog.LevelInfo  (0)
//	observability.LevelWarn   →  slog.LevelWarn  (4)
//	observability.LevelError  →  slog.LevelError (8)
//	observability.LevelFatal  →  slog.Level(12)
//
// The structured event name (e.g. "agent.started") becomes the slog message.
// Each observability.Field becomes a slog.Any attribute keyed by Field.Key.
func NewSlogLogger(l *slog.Logger) Logger {
	if l == nil {
		l = slog.Default()
	}
	return &slogLogger{l: l}
}

// Log implements Logger.
func (s *slogLogger) Log(ctx context.Context, level Level, event string, fields ...Field) {
	if !s.l.Enabled(ctx, slogLevel(level)) {
		return
	}
	s.l.Log(ctx, slogLevel(level), event, fieldsToAttrs(fields)...)
}

// Trace implements Logger.
func (s *slogLogger) Trace(ctx context.Context, event string, fields ...Field) {
	s.Log(ctx, LevelTrace, event, fields...)
}

// Debug implements Logger.
func (s *slogLogger) Debug(ctx context.Context, event string, fields ...Field) {
	s.Log(ctx, LevelDebug, event, fields...)
}

// Info implements Logger.
func (s *slogLogger) Info(ctx context.Context, event string, fields ...Field) {
	s.Log(ctx, LevelInfo, event, fields...)
}

// Warn implements Logger.
func (s *slogLogger) Warn(ctx context.Context, event string, fields ...Field) {
	s.Log(ctx, LevelWarn, event, fields...)
}

// Error implements Logger.
func (s *slogLogger) Error(ctx context.Context, event string, fields ...Field) {
	s.Log(ctx, LevelError, event, fields...)
}

// slogLevel converts an observability.Level to a slog.Level.
func slogLevel(l Level) slog.Level {
	switch l {
	case LevelTrace:
		return SlogLevelTrace
	case LevelDebug:
		return slog.LevelDebug
	case LevelInfo:
		return slog.LevelInfo
	case LevelWarn:
		return slog.LevelWarn
	case LevelError:
		return slog.LevelError
	case LevelFatal:
		return SlogLevelFatal
	default:
		return slog.LevelInfo
	}
}

// fieldsToAttrs converts observability.Field values to slog.Attr arguments.
// Each field becomes a slog.Any attr so any value type is preserved.
func fieldsToAttrs(fields []Field) []any {
	if len(fields) == 0 {
		return nil
	}
	attrs := make([]any, 0, len(fields)*2)
	for _, f := range fields {
		attrs = append(attrs, slog.Any(f.Key, f.Value))
	}
	return attrs
}

// compile-time check: slogLogger must satisfy Logger.
var _ Logger = (*slogLogger)(nil)
