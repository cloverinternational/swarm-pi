package observability

import (
	"context"

	sdkerr "github.com/Swarm-Code/mono/swarm-sdk/internal/sdkerr"
)

// Standard attribute keys for observability
const (
	AttributeAgentID       = "agent.id"
	AttributeAgentName     = "agent.name"
	AttributeAgentModel    = "agent.model"
	AttributeAgentProvider = "agent.provider"
	AttributeToolName      = "tool.name"
	AttributeModeID        = "mode.id"
	AttributeModeName      = "mode.name"
	AttributeGroupID       = "group.id"
	AttributeGroupName     = "group.name"
	AttributeWorkflowID    = "workflow.id"
	AttributeTraceID       = "trace.id"
)

// EnsureTraceContext ensures that the context has a trace ID.
// If not, it generates a new one (conceptually - actual generation depends on the Tracer implementation).
// This helper is mainly for ensuring we have *some* tracing context.
func EnsureTraceContext(ctx context.Context, tracer Tracer, operationName string) (context.Context, Span) {
	// If the tracer supports extracting from context, it will do so.
	// Otherwise, it starts a new root span.
	return tracer.StartSpan(ctx, operationName)
}

// AddAgentAttributes adds standard agent attributes to a span
func AddAgentAttributes(span Span, id, name, provider, model string) {
	span.SetAttribute(AttributeAgentID, id)
	span.SetAttribute(AttributeAgentName, name)
	span.SetAttribute(AttributeAgentProvider, provider)
	span.SetAttribute(AttributeAgentModel, model)
}

// AddGroupAttributes adds standard group attributes to a span
func AddGroupAttributes(span Span, id, name string) {
	span.SetAttribute(AttributeGroupID, id)
	span.SetAttribute(AttributeGroupName, name)
}

// AddModeAttributes adds standard mode attributes to a span
func AddModeAttributes(span Span, id, name string) {
	span.SetAttribute(AttributeModeID, id)
	span.SetAttribute(AttributeModeName, name)
}

// RecordError records an error in the span and logs it
func RecordError(ctx context.Context, logger Logger, span Span, err error, msg string) {
	span.RecordError(err)
	span.SetStatus(StatusCodeError, err.Error())
	logger.Error(ctx, msg, Field{Key: "error", Value: err.Error()})
}

// Helper to format trace ID for logging if needed
func GetTraceID(ctx context.Context) string {
	traceID, _ := sdkerr.TraceFromContext(ctx)
	return traceID
}
