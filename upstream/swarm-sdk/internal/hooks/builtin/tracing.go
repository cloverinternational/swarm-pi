package builtin

import (
	"context"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/hooks"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/observability"
)

// TracingHook creates distributed tracing spans for events
type TracingHook struct {
	tracer observability.Tracer
	filter *hooks.EventFilter
}

// NewTracingHook creates a new tracing hook
func NewTracingHook(tracer observability.Tracer) *TracingHook {
	return &TracingHook{
		tracer: tracer,
		filter: hooks.MatchAll(),
	}
}

// NewTracingHookWithFilter creates a tracing hook with custom filter
func NewTracingHookWithFilter(tracer observability.Tracer, filter *hooks.EventFilter) *TracingHook {
	return &TracingHook{
		tracer: tracer,
		filter: filter,
	}
}

// OnEvent implements Hook interface
func (h *TracingHook) OnEvent(ctx context.Context, event hooks.Event) (hooks.HookResult, error) {
	// Create span for this event
	_, span := h.tracer.StartSpan(ctx, "hook."+event.Type)
	defer span.End()

	// Add standard attributes
	span.SetAttribute("event.id", event.ID)
	span.SetAttribute("event.type", event.Type)
	span.SetAttribute("trace.id", event.TraceID)
	span.SetAttribute("conversation.id", event.ConversationID)
	span.SetAttribute("agent.id", event.AgentID)
	span.SetAttribute("mode.id", event.ModeID)
	span.SetAttribute("group.id", event.GroupID)

	// Add data attributes (sample only, to avoid bloat)
	if len(event.Data) > 0 {
		// Only add small, useful data
		for key, value := range event.Data {
			if key == "error" || key == "status" || key == "result" {
				span.SetAttribute("data."+key, value)
			}
		}
	}

	return hooks.Continue(), nil
}

// Filter implements Hook interface
func (h *TracingHook) Filter(event hooks.Event) bool {
	return h.filter.Matches(event)
}

// Priority implements Hook interface
func (h *TracingHook) Priority() int {
	return 95 // Very high priority - create spans early
}

// Name implements Hook interface
func (h *TracingHook) Name() string {
	return "builtin.tracing"
}

// SetFilter updates the event filter
func (h *TracingHook) SetFilter(filter *hooks.EventFilter) {
	h.filter = filter
}

// SamplingTracingHook creates spans only for sampled events
type SamplingTracingHook struct {
	tracer     observability.Tracer
	sampleRate float64 // 0.0 to 1.0
	filter     *hooks.EventFilter
	counter    int64
}

// NewSamplingTracingHook creates a tracing hook with sampling
func NewSamplingTracingHook(tracer observability.Tracer, sampleRate float64) *SamplingTracingHook {
	return &SamplingTracingHook{
		tracer:     tracer,
		sampleRate: sampleRate,
		filter:     hooks.MatchAll(),
	}
}

// OnEvent implements Hook interface
func (h *SamplingTracingHook) OnEvent(ctx context.Context, event hooks.Event) (hooks.HookResult, error) {
	// Simple sampling logic - every Nth event
	h.counter++
	if h.sampleRate < 1.0 {
		if float64(h.counter%100)/100.0 > h.sampleRate {
			return hooks.Continue(), nil
		}
	}

	// Create span
	_, span := h.tracer.StartSpan(ctx, "hook."+event.Type)
	defer span.End()

	span.SetAttribute("event.id", event.ID)
	span.SetAttribute("event.type", event.Type)
	span.SetAttribute("trace.id", event.TraceID)
	span.SetAttribute("sampled", true)

	return hooks.Continue(), nil
}

// Filter implements Hook interface
func (h *SamplingTracingHook) Filter(event hooks.Event) bool {
	return h.filter.Matches(event)
}

// Priority implements Hook interface
func (h *SamplingTracingHook) Priority() int {
	return 94 // High priority
}

// Name implements Hook interface
func (h *SamplingTracingHook) Name() string {
	return "builtin.sampling_tracing"
}

// SetFilter updates the event filter
func (h *SamplingTracingHook) SetFilter(filter *hooks.EventFilter) {
	h.filter = filter
}
