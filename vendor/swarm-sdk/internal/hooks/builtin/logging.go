// Package builtin provides built-in hook implementations.
package builtin

import (
	"context"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/hooks"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/observability"
)

// LoggingHook logs all events to structured logger
type LoggingHook struct {
	logger observability.Logger
	level  observability.Level
	filter *hooks.EventFilter
}

// NewLoggingHook creates a new logging hook
func NewLoggingHook(logger observability.Logger, level observability.Level) *LoggingHook {
	return &LoggingHook{
		logger: logger,
		level:  level,
		filter: hooks.MatchAll(), // Match all events by default
	}
}

// NewLoggingHookWithFilter creates a logging hook with custom filter
func NewLoggingHookWithFilter(logger observability.Logger, level observability.Level, filter *hooks.EventFilter) *LoggingHook {
	return &LoggingHook{
		logger: logger,
		level:  level,
		filter: filter,
	}
}

// OnEvent implements Hook interface
func (h *LoggingHook) OnEvent(ctx context.Context, event hooks.Event) (hooks.HookResult, error) {
	h.logger.Log(ctx, h.level, "hook.event",
		observability.F("event_id", event.ID),
		observability.F("event_type", event.Type),
		observability.F("trace_id", event.TraceID),
		observability.F("conversation_id", event.ConversationID),
		observability.F("agent_id", event.AgentID),
		observability.F("mode_id", event.ModeID),
		observability.F("group_id", event.GroupID),
		observability.F("timestamp", event.Timestamp),
	)

	// Log data if available
	if len(event.Data) > 0 {
		h.logger.Log(ctx, observability.LevelDebug, "hook.event.data",
			observability.F("event_id", event.ID),
			observability.F("data", event.Data),
		)
	}

	// Log metadata if available
	if len(event.Metadata) > 0 {
		h.logger.Log(ctx, observability.LevelDebug, "hook.event.metadata",
			observability.F("event_id", event.ID),
			observability.F("metadata", event.Metadata),
		)
	}

	return hooks.Continue(), nil
}

// Filter implements Hook interface
func (h *LoggingHook) Filter(event hooks.Event) bool {
	return h.filter.Matches(event)
}

// Priority implements Hook interface
func (h *LoggingHook) Priority() int {
	return 90 // High priority - log early in the pipeline
}

// Name implements Hook interface
func (h *LoggingHook) Name() string {
	return "builtin.logging"
}

// SetFilter updates the event filter
func (h *LoggingHook) SetFilter(filter *hooks.EventFilter) {
	h.filter = filter
}

// SetLevel updates the log level
func (h *LoggingHook) SetLevel(level observability.Level) {
	h.level = level
}

// ErrorLoggingHook logs only error events
type ErrorLoggingHook struct {
	logger observability.Logger
}

// NewErrorLoggingHook creates a hook that logs only error events
func NewErrorLoggingHook(logger observability.Logger) *ErrorLoggingHook {
	return &ErrorLoggingHook{
		logger: logger,
	}
}

// OnEvent implements Hook interface
func (h *ErrorLoggingHook) OnEvent(ctx context.Context, event hooks.Event) (hooks.HookResult, error) {
	h.logger.Log(ctx, observability.LevelError, "hook.error_event",
		observability.F("event_id", event.ID),
		observability.F("event_type", event.Type),
		observability.F("trace_id", event.TraceID),
		observability.F("conversation_id", event.ConversationID),
		observability.F("agent_id", event.AgentID),
		observability.F("data", event.Data),
		observability.F("metadata", event.Metadata),
	)

	return hooks.Continue(), nil
}

// Filter implements Hook interface
func (h *ErrorLoggingHook) Filter(event hooks.Event) bool {
	return hooks.MatchErrorEvents().Matches(event)
}

// Priority implements Hook interface
func (h *ErrorLoggingHook) Priority() int {
	return 95 // Very high priority - log errors immediately
}

// Name implements Hook interface
func (h *ErrorLoggingHook) Name() string {
	return "builtin.error_logging"
}

// DebugLoggingHook logs events with detailed information
type DebugLoggingHook struct {
	logger          observability.Logger
	includeData     bool
	includeMetadata bool
	filter          *hooks.EventFilter
}

// NewDebugLoggingHook creates a debug logging hook
func NewDebugLoggingHook(logger observability.Logger, includeData, includeMetadata bool) *DebugLoggingHook {
	return &DebugLoggingHook{
		logger:          logger,
		includeData:     includeData,
		includeMetadata: includeMetadata,
		filter:          hooks.MatchAll(),
	}
}

// OnEvent implements Hook interface
func (h *DebugLoggingHook) OnEvent(ctx context.Context, event hooks.Event) (hooks.HookResult, error) {
	fields := []observability.Field{
		{Key: "event_id", Value: event.ID},
		{Key: "event_type", Value: event.Type},
		{Key: "trace_id", Value: event.TraceID},
		{Key: "conversation_id", Value: event.ConversationID},
		{Key: "agent_id", Value: event.AgentID},
		{Key: "mode_id", Value: event.ModeID},
		{Key: "group_id", Value: event.GroupID},
		{Key: "timestamp", Value: event.Timestamp},
	}

	if h.includeData {
		fields = append(fields, observability.F("data", event.Data))
	}

	if h.includeMetadata {
		fields = append(fields, observability.F("metadata", event.Metadata))
	}

	h.logger.Log(ctx, observability.LevelDebug, "hook.debug_event", fields...)

	return hooks.Continue(), nil
}

// Filter implements Hook interface
func (h *DebugLoggingHook) Filter(event hooks.Event) bool {
	return h.filter.Matches(event)
}

// Priority implements Hook interface
func (h *DebugLoggingHook) Priority() int {
	return 85 // High priority
}

// Name implements Hook interface
func (h *DebugLoggingHook) Name() string {
	return "builtin.debug_logging"
}

// SetFilter updates the event filter
func (h *DebugLoggingHook) SetFilter(filter *hooks.EventFilter) {
	h.filter = filter
}
