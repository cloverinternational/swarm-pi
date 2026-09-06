package observability

import (
	"context"
	"time"
)

// AuditEvent represents a significant event that must be recorded immutably.
// Audit events are used for compliance, security, and debugging.
type AuditEvent struct {
	// EventID is a unique identifier for this event.
	EventID string `json:"event_id"`

	// Timestamp when the event occurred (RFC3339 format).
	Timestamp time.Time `json:"timestamp"`

	// TraceID links to the distributed trace.
	TraceID string `json:"trace_id,omitempty"`

	// ConversationID identifies the conversation.
	ConversationID string `json:"conversation_id,omitempty"`

	// EventType is the type of audit event (dot-notation).
	// Examples: "permission.granted", "steering.intervention", "tool.denied"
	EventType string `json:"event_type"`

	// Actor identifies who/what performed the action.
	// Could be user ID, agent ID, system component, etc.
	Actor string `json:"actor"`

	// Action describes what was done.
	Action string `json:"action"`

	// Resource identifies what was acted upon.
	Resource string `json:"resource,omitempty"`

	// Outcome indicates success or failure.
	Outcome AuditOutcome `json:"outcome"`

	// Details contains additional context about the event.
	Details map[string]any `json:"details,omitempty"`

	// Signature is a cryptographic signature for tamper detection (optional).
	Signature string `json:"signature,omitempty"`
}

// AuditOutcome represents the result of an audited action.
type AuditOutcome string

const (
	// AuditOutcomeSuccess indicates the action completed successfully.
	AuditOutcomeSuccess AuditOutcome = "success"

	// AuditOutcomeFailure indicates the action failed.
	AuditOutcomeFailure AuditOutcome = "failure"

	// AuditOutcomeDenied indicates the action was denied (permission/policy).
	AuditOutcomeDenied AuditOutcome = "denied"

	// AuditOutcomeBlocked indicates the action was blocked (steering/safety).
	AuditOutcomeBlocked AuditOutcome = "blocked"
)

// Auditor defines the interface for audit logging.
// Audit logs are immutable and must be tamper-resistant.
type Auditor interface {
	// Record records an audit event.
	// This should be durable and ideally append-only.
	Record(ctx context.Context, event AuditEvent) error

	// Query retrieves audit events matching the given criteria.
	Query(ctx context.Context, criteria AuditCriteria) ([]AuditEvent, error)
}

// AuditCriteria defines filters for querying audit events.
type AuditCriteria struct {
	// EventTypes filters by event type (supports wildcards).
	EventTypes []string

	// Actors filters by actor.
	Actors []string

	// ConversationID filters by conversation.
	ConversationID string

	// StartTime filters events after this time.
	StartTime time.Time

	// EndTime filters events before this time.
	EndTime time.Time

	// Outcome filters by outcome.
	Outcomes []AuditOutcome

	// Limit limits the number of results.
	Limit int
}

// StandardAuditEvents defines the standard audit events emitted by the SDK.
const (
	// Permission events
	AuditEventPermissionGranted = "permission.granted"
	AuditEventPermissionDenied  = "permission.denied"
	AuditEventPermissionChanged = "permission.changed"

	// Steering events
	AuditEventSteeringIntervention = "steering.intervention"
	AuditEventSteeringApproval     = "steering.approval"
	AuditEventSteeringDenial       = "steering.denial"
	AuditEventSteeringEscalation   = "steering.escalation"

	// Configuration events
	AuditEventConfigChanged = "config.changed"
	AuditEventModeLoaded    = "mode.loaded"
	AuditEventModeChanged   = "mode.changed"

	// Conversation events
	AuditEventConversationCreated  = "conversation.created"
	AuditEventConversationArchived = "conversation.archived"
	AuditEventConversationDeleted  = "conversation.deleted"

	// Tool events
	AuditEventToolExecuted = "tool.executed"
	AuditEventToolDenied   = "tool.denied"
	AuditEventToolFailed   = "tool.failed"

	// Security events
	AuditEventSecurityBreach = "security.breach"
	AuditEventAuthFailure    = "auth.failure"
	AuditEventRateLimitHit   = "rate_limit.hit"
	AuditEventAbuseDetected  = "abuse.detected"
)
