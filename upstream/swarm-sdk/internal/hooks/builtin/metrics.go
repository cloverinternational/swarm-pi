package builtin

import (
	"context"
	"fmt"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/hooks"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/observability"
)

// MetricsHook collects metrics for events
type MetricsHook struct {
	metrics observability.Metrics
	filter  *hooks.EventFilter
}

// NewMetricsHook creates a new metrics hook
func NewMetricsHook(metrics observability.Metrics) *MetricsHook {
	return &MetricsHook{
		metrics: metrics,
		filter:  hooks.MatchAll(),
	}
}

// NewMetricsHookWithFilter creates a metrics hook with custom filter
func NewMetricsHookWithFilter(metrics observability.Metrics, filter *hooks.EventFilter) *MetricsHook {
	return &MetricsHook{
		metrics: metrics,
		filter:  filter,
	}
}

// OnEvent implements Hook interface
func (h *MetricsHook) OnEvent(ctx context.Context, event hooks.Event) (hooks.HookResult, error) {
	// Increment event counter
	labels := map[string]string{
		"event_type": event.Type,
		"agent_id":   event.AgentID,
		"mode_id":    event.ModeID,
	}
	h.metrics.Counter("hook.events.total", 1, labels)

	// Record event timing if available
	if durationMS, ok := event.Metadata["duration_ms"].(int64); ok {
		h.metrics.Histogram("hook.events.duration", float64(durationMS), map[string]string{
			"event_type": event.Type,
		})
	}

	if durationMS, ok := event.Metadata["duration_ms"].(float64); ok {
		h.metrics.Histogram("hook.events.duration", durationMS, map[string]string{
			"event_type": event.Type,
		})
	}

	// Record specific metrics based on event type
	h.recordTypeSpecificMetrics(event)

	return hooks.Continue(), nil
}

// recordTypeSpecificMetrics records metrics specific to event types
func (h *MetricsHook) recordTypeSpecificMetrics(event hooks.Event) {
	switch event.Type {
	case hooks.EventProviderAfterResponse:
		// Track token usage
		if usage, ok := event.Data["usage"].(map[string]any); ok {
			if input, ok := usage["input"].(int); ok {
				h.metrics.Histogram("provider.tokens.input", float64(input), map[string]string{
					"agent_id": event.AgentID,
				})
			}
			if output, ok := usage["output"].(int); ok {
				h.metrics.Histogram("provider.tokens.output", float64(output), map[string]string{
					"agent_id": event.AgentID,
				})
			}
		}

	case hooks.EventToolAfterExecute:
		// Track tool execution
		h.metrics.Counter("tool.executions.total", 1, map[string]string{
			"tool_name": fmt.Sprintf("%v", event.Data["tool_name"]),
		})

	case hooks.EventToolExecutionFailed:
		// Track tool failures
		h.metrics.Counter("tool.failures.total", 1, map[string]string{
			"tool_name":  fmt.Sprintf("%v", event.Data["tool_name"]),
			"error_type": fmt.Sprintf("%v", event.Data["error_type"]),
		})

	case hooks.EventProviderRateLimited:
		// Track rate limits
		h.metrics.Counter("provider.rate_limited.total", 1, map[string]string{
			"agent_id": event.AgentID,
		})

	case hooks.EventProviderError:
		// Track provider errors
		h.metrics.Counter("provider.errors.total", 1, map[string]string{
			"agent_id":   event.AgentID,
			"error_type": fmt.Sprintf("%v", event.Data["error_type"]),
		})

	case hooks.EventContextWindowExceeded:
		// Track context window issues
		h.metrics.Counter("context.window_exceeded.total", 1, map[string]string{
			"conversation_id": event.ConversationID,
		})

	case hooks.EventGroupConsensusReached:
		// Track consensus
		h.metrics.Counter("group.consensus.reached.total", 1, map[string]string{
			"group_id": event.GroupID,
		})

	case hooks.EventGroupConsensusFailed:
		// Track consensus failures
		h.metrics.Counter("group.consensus.failed.total", 1, map[string]string{
			"group_id": event.GroupID,
		})
	}
}

// Filter implements Hook interface
func (h *MetricsHook) Filter(event hooks.Event) bool {
	return h.filter.Matches(event)
}

// Priority implements Hook interface
func (h *MetricsHook) Priority() int {
	return 85 // High priority - collect metrics early
}

// Name implements Hook interface
func (h *MetricsHook) Name() string {
	return "builtin.metrics"
}

// SetFilter updates the event filter
func (h *MetricsHook) SetFilter(filter *hooks.EventFilter) {
	h.filter = filter
}

// PerformanceMetricsHook tracks performance metrics
type PerformanceMetricsHook struct {
	metrics observability.Metrics
}

// NewPerformanceMetricsHook creates a performance metrics hook
func NewPerformanceMetricsHook(metrics observability.Metrics) *PerformanceMetricsHook {
	return &PerformanceMetricsHook{
		metrics: metrics,
	}
}

// OnEvent implements Hook interface
func (h *PerformanceMetricsHook) OnEvent(ctx context.Context, event hooks.Event) (hooks.HookResult, error) {
	// Track only performance-related events
	switch event.Type {
	case hooks.EventProviderAfterResponse:
		if latency, ok := event.Data["latency_ms"].(int64); ok {
			h.metrics.Histogram("performance.provider.latency", float64(latency), map[string]string{
				"agent_id": event.AgentID,
			})
		}

	case hooks.EventToolAfterExecute:
		if latency, ok := event.Data["latency_ms"].(int64); ok {
			h.metrics.Histogram("performance.tool.latency", float64(latency), map[string]string{
				"tool_name": fmt.Sprintf("%v", event.Data["tool_name"]),
			})
		}
	}

	return hooks.Continue(), nil
}

// Filter implements Hook interface
func (h *PerformanceMetricsHook) Filter(event hooks.Event) bool {
	// Only performance-related events
	switch event.Type {
	case hooks.EventProviderAfterResponse,
		hooks.EventToolAfterExecute,
		hooks.EventGroupCompleted:
		return true
	default:
		return false
	}
}

// Priority implements Hook interface
func (h *PerformanceMetricsHook) Priority() int {
	return 80
}

// Name implements Hook interface
func (h *PerformanceMetricsHook) Name() string {
	return "builtin.performance_metrics"
}
