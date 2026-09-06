// Package chat provides observability implementations for the TUI
package chat

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/conversation"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/observability"
	"github.com/getsentry/sentry-go"
)

// ============================================================================
// SIMPLE LOGGER - Console-based logger for TUI with Sentry integration
// ============================================================================

// SimpleLogger implements observability.Logger with file-based logging
type SimpleLogger struct {
	level observability.Level
}

// NewSimpleLogger creates a new logger that writes to debug log
func NewSimpleLogger() *SimpleLogger {
	return &SimpleLogger{
		level: observability.LevelInfo,
	}
}

func (l *SimpleLogger) Log(ctx context.Context, level observability.Level, event string, fields ...observability.Field) {
	if level < l.level {
		return
	}

	// Format: [LEVEL] event key=value key=value
	var msg strings.Builder
	msg.WriteString(fmt.Sprintf("[%s] %s", level, event))
	fieldData := make(map[string]any)
	for _, f := range fields {
		msg.WriteString(fmt.Sprintf(" %s=%v", f.Key, f.Value))
		fieldData[f.Key] = f.Value
	}

	logDebug("%s", msg.String())

	// Add Sentry breadcrumb for important events
	if shouldAddBreadcrumb(level, event) {
		AddSentryBreadcrumb(
			categorizeEvent(event),
			msg.String(),
			mapLevelToSentry(level),
			fieldData,
		)
	}
}

func (l *SimpleLogger) Trace(ctx context.Context, event string, fields ...observability.Field) {
	l.Log(ctx, observability.LevelTrace, event, fields...)
}

func (l *SimpleLogger) Debug(ctx context.Context, event string, fields ...observability.Field) {
	l.Log(ctx, observability.LevelDebug, event, fields...)
}

func (l *SimpleLogger) Info(ctx context.Context, event string, fields ...observability.Field) {
	l.Log(ctx, observability.LevelInfo, event, fields...)
}

func (l *SimpleLogger) Warn(ctx context.Context, event string, fields ...observability.Field) {
	l.Log(ctx, observability.LevelWarn, event, fields...)
}

func (l *SimpleLogger) Error(ctx context.Context, event string, fields ...observability.Field) {
	l.Log(ctx, observability.LevelError, event, fields...)

	// For errors, also add a more detailed breadcrumb
	fieldData := make(map[string]any)
	for _, f := range fields {
		fieldData[f.Key] = f.Value
	}
	AddSentryBreadcrumb("error", fmt.Sprintf("Error event: %s", event), sentry.LevelError, fieldData)
}

func (l *SimpleLogger) Fatal(ctx context.Context, event string, fields ...observability.Field) {
	l.Log(ctx, observability.LevelFatal, event, fields...)
}

func (l *SimpleLogger) WithFields(fields ...observability.Field) observability.Logger {
	return l // For simplicity, return same logger
}

func (l *SimpleLogger) SetLevel(level observability.Level) {
	l.level = level
}

// ============================================================================
// HELPER FUNCTIONS FOR SENTRY INTEGRATION
// ============================================================================

// shouldAddBreadcrumb determines if an event should create a Sentry breadcrumb
func shouldAddBreadcrumb(level observability.Level, event string) bool {
	// Add breadcrumbs for warnings and errors
	if level >= observability.LevelWarn {
		return true
	}

	// Add breadcrumbs for important lifecycle events
	importantEvents := []string{
		"agent.execute.started",
		"agent.execute.completed",
		"agent.execute.failed",
		"tool.execute.started",
		"tool.execute.completed",
		"tool.execute.failed",
		"conversation.created",
		"conversation.resumed",
		"provider.request.started",
		"provider.request.completed",
		"provider.request.failed",
	}

	return slices.Contains(importantEvents, event)
}

// categorizeEvent maps event names to breadcrumb categories
func categorizeEvent(event string) string {
	if len(event) == 0 {
		return "default"
	}

	// Extract category from event name (e.g., "agent.execute.started" -> "agent")
	for i, c := range event {
		if c == '.' {
			return event[:i]
		}
	}

	return "default"
}

// mapLevelToSentry maps SDK log levels to Sentry breadcrumb levels
func mapLevelToSentry(level observability.Level) sentry.Level {
	switch level {
	case observability.LevelTrace, observability.LevelDebug:
		return sentry.LevelDebug
	case observability.LevelInfo:
		return sentry.LevelInfo
	case observability.LevelWarn:
		return sentry.LevelWarning
	case observability.LevelError:
		return sentry.LevelError
	case observability.LevelFatal:
		return sentry.LevelFatal
	default:
		return sentry.LevelInfo
	}
}

// ============================================================================
// NO-OP TRACER - Minimal tracer implementation for TUI
// ============================================================================

// NoopTracer implements observability.Tracer with no-op operations
type NoopTracer struct{}

// NewNoopTracer creates a new no-op tracer
func NewNoopTracer() *NoopTracer {
	return &NoopTracer{}
}

func (t *NoopTracer) StartSpan(ctx context.Context, name string) (context.Context, observability.Span) {
	return ctx, &NoopSpan{ctx: ctx}
}

func (t *NoopTracer) StartSpanWithOptions(ctx context.Context, name string, opts observability.SpanOptions) (context.Context, observability.Span) {
	return ctx, &NoopSpan{ctx: ctx}
}

func (t *NoopTracer) SpanFromContext(ctx context.Context) observability.Span {
	return &NoopSpan{ctx: ctx}
}

func (t *NoopTracer) InjectContext(ctx context.Context, carrier map[string]string) error {
	return nil
}

func (t *NoopTracer) ExtractContext(carrier map[string]string) (context.Context, error) {
	return context.Background(), nil
}

// ============================================================================
// NO-OP SPAN - Minimal span implementation
// ============================================================================

// NoopSpan implements observability.Span with no-op operations
type NoopSpan struct {
	ctx context.Context
}

func (s *NoopSpan) End() {}

func (s *NoopSpan) SetAttribute(key string, value any) {}

func (s *NoopSpan) SetAttributes(attrs map[string]any) {}

func (s *NoopSpan) SetStatus(code observability.StatusCode, message string) {}

func (s *NoopSpan) RecordError(err error) {}

func (s *NoopSpan) SpanID() string {
	return "noop-span"
}

func (s *NoopSpan) TraceID() string {
	return "noop-trace"
}

func (s *NoopSpan) Context() context.Context {
	return s.ctx
}

// ============================================================================
// HELPER MESSAGES FOR BUBBLE TEA
// ============================================================================

// AgentResponseMsg carries agent execution results
type AgentResponseMsg struct {
	Message        string
	ConversationID string
	TokensUsed     int
	CostUSD        float64
	Duration       time.Duration
	AllMessages    []*Message // Full conversation for display
}

// AgentErrorMsg carries agent execution errors
type AgentErrorMsg struct {
	Err error
}

// ConversationsLoadedMsg carries loaded conversations
type ConversationsLoadedMsg struct {
	Conversations []*conversation.Conversation
	Error         error
}

// StreamStartMsg signals streaming has started
type StreamStartMsg struct {
	Stream chan string // Will be provider stream channel
}

// StreamChunkMsg carries streaming text chunks
type StreamChunkMsg struct {
	Delta string
}

// StreamCompleteMsg signals streaming is done
type StreamCompleteMsg struct{}

// StreamErrorMsg carries streaming errors
type StreamErrorMsg struct {
	Err error
}
