package builtin

import (
	"context"
	"fmt"
	"slices"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/hooks"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/observability"
)

// AuditHook logs sensitive events to audit trail
type AuditHook struct {
	auditor   observability.Auditor
	sensitive map[string]bool
}

// NewAuditHook creates a new audit hook
func NewAuditHook(auditor observability.Auditor) *AuditHook {
	return &AuditHook{
		auditor: auditor,
		sensitive: map[string]bool{
			// Tool events
			hooks.EventToolBeforeExecute:    true,
			hooks.EventToolAfterExecute:     true,
			hooks.EventToolExecutionFailed:  true,
			hooks.EventToolPermissionDenied: true,

			// Message events
			hooks.EventMessageEdited:  true,
			hooks.EventMessageDeleted: true,
			hooks.EventMessageAdded:   true,

			// Steering events
			hooks.EventSteeringIntervention:   true,
			hooks.EventSteeringOverride:       true,
			hooks.EventSteeringApprovalNeeded: true,

			// Mode transitions
			hooks.EventModeEntered:           true,
			hooks.EventModeExited:            true,
			hooks.EventModeTransitionBlocked: true,

			// Provider events
			hooks.EventProviderError:       true,
			hooks.EventProviderRateLimited: true,
		},
	}
}

// OnEvent implements Hook interface
func (h *AuditHook) OnEvent(ctx context.Context, event hooks.Event) (hooks.HookResult, error) {
	auditEvent := observability.AuditEvent{
		EventID:        event.ID,
		Timestamp:      event.Timestamp,
		TraceID:        event.TraceID,
		ConversationID: event.ConversationID,
		EventType:      event.Type,
		Actor:          event.AgentID,
		Action:         h.extractAction(event),
		Resource:       event.ConversationID,
		Outcome:        h.determineOutcome(event),
		Details:        h.sanitizeData(event.Data),
	}

	if err := h.auditor.Record(ctx, auditEvent); err != nil {
		return hooks.HookResult{}, fmt.Errorf("audit record failed: %w", err)
	}

	return hooks.Continue(), nil
}

// Filter implements Hook interface
func (h *AuditHook) Filter(event hooks.Event) bool {
	return h.sensitive[event.Type]
}

// Priority implements Hook interface
func (h *AuditHook) Priority() int {
	return 80 // High priority - audit before modifications
}

// Name implements Hook interface
func (h *AuditHook) Name() string {
	return "builtin.audit"
}

// extractAction extracts a human-readable action from event
func (h *AuditHook) extractAction(event hooks.Event) string {
	// Extract action based on event type
	switch event.Type {
	case hooks.EventToolBeforeExecute:
		toolName := event.Data["tool_name"]
		return fmt.Sprintf("Tool execution requested: %v", toolName)

	case hooks.EventToolAfterExecute:
		toolName := event.Data["tool_name"]
		return fmt.Sprintf("Tool executed: %v", toolName)

	case hooks.EventToolExecutionFailed:
		toolName := event.Data["tool_name"]
		return fmt.Sprintf("Tool execution failed: %v", toolName)

	case hooks.EventMessageEdited:
		return "Message edited"

	case hooks.EventMessageDeleted:
		return "Message deleted"

	case hooks.EventSteeringIntervention:
		return "Steering intervention occurred"

	case hooks.EventProviderError:
		return "Provider error occurred"

	default:
		return event.Type
	}
}

// sanitizeData removes sensitive information from data
func (h *AuditHook) sanitizeData(data map[string]any) map[string]any {
	sanitized := make(map[string]any)

	for key, value := range data {
		// Skip sensitive keys
		if h.isSensitiveKey(key) {
			sanitized[key] = "[REDACTED]"
			continue
		}

		// Copy safe values
		sanitized[key] = value
	}

	return sanitized
}

// isSensitiveKey checks if a key contains sensitive data
func (h *AuditHook) isSensitiveKey(key string) bool {
	sensitiveKeys := []string{
		"api_key",
		"password",
		"token",
		"secret",
		"credential",
		"private_key",
	}

	return slices.Contains(sensitiveKeys, key)
}

// determineOutcome determines audit outcome based on event
func (h *AuditHook) determineOutcome(event hooks.Event) observability.AuditOutcome {
	switch event.Type {
	case hooks.EventToolPermissionDenied,
		hooks.EventModeTransitionBlocked:
		return observability.AuditOutcomeDenied

	case hooks.EventToolExecutionFailed,
		hooks.EventProviderError:
		return observability.AuditOutcomeFailure

	case hooks.EventSteeringIntervention,
		hooks.EventSteeringOverride:
		return observability.AuditOutcomeBlocked

	default:
		return observability.AuditOutcomeSuccess
	}
}

// AddSensitiveEvent adds an event type to the sensitive list
func (h *AuditHook) AddSensitiveEvent(eventType string) {
	h.sensitive[eventType] = true
}

// RemoveSensitiveEvent removes an event type from the sensitive list
func (h *AuditHook) RemoveSensitiveEvent(eventType string) {
	delete(h.sensitive, eventType)
}

// ComplianceAuditHook provides compliance-focused auditing
type ComplianceAuditHook struct {
	auditor       observability.Auditor
	retentionDays int
}

// NewComplianceAuditHook creates a compliance audit hook
func NewComplianceAuditHook(auditor observability.Auditor, retentionDays int) *ComplianceAuditHook {
	return &ComplianceAuditHook{
		auditor:       auditor,
		retentionDays: retentionDays,
	}
}

// OnEvent implements Hook interface
func (h *ComplianceAuditHook) OnEvent(ctx context.Context, event hooks.Event) (hooks.HookResult, error) {
	auditEvent := observability.AuditEvent{
		EventID:        event.ID,
		Timestamp:      event.Timestamp,
		TraceID:        event.TraceID,
		ConversationID: event.ConversationID,
		EventType:      event.Type,
		Actor:          event.AgentID,
		Action:         event.Type,
		Resource:       event.ConversationID,
		Outcome:        h.determineOutcome(event.Type),
		Details:        event.Data,
	}

	if err := h.auditor.Record(ctx, auditEvent); err != nil {
		return hooks.HookResult{}, fmt.Errorf("compliance audit record failed: %w", err)
	}

	return hooks.Continue(), nil
}

// Filter implements Hook interface
func (h *ComplianceAuditHook) Filter(event hooks.Event) bool {
	// Audit all events for compliance
	return true
}

// Priority implements Hook interface
func (h *ComplianceAuditHook) Priority() int {
	return 85 // Very high priority
}

// Name implements Hook interface
func (h *ComplianceAuditHook) Name() string {
	return "builtin.compliance_audit"
}

// determineOutcome determines audit outcome for compliance
func (h *ComplianceAuditHook) determineOutcome(eventType string) observability.AuditOutcome {
	switch eventType {
	case hooks.EventToolPermissionDenied,
		hooks.EventModeTransitionBlocked:
		return observability.AuditOutcomeDenied

	case hooks.EventToolExecutionFailed,
		hooks.EventProviderError:
		return observability.AuditOutcomeFailure

	case hooks.EventSteeringIntervention,
		hooks.EventSteeringOverride:
		return observability.AuditOutcomeBlocked

	default:
		return observability.AuditOutcomeSuccess
	}
}
