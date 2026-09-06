// Package hosted defines the SDK-owned hosted session protocol contract.
// This is Ring 0 - shared protocol and metadata types that can be consumed by
// runtimes, control planes, and clients without embedding hosted semantics in
// any one transport or service.
package hosted

import (
	"encoding/json"
	"fmt"
	"maps"
	"time"
)

// ProtocolVersion identifies the hosted wire contract version.
type ProtocolVersion string

const (
	// ProtocolVersionV1Alpha1 is the initial hosted protocol contract.
	ProtocolVersionV1Alpha1 ProtocolVersion = "hosted.v1alpha1"

	// CurrentProtocolVersion is the canonical protocol version for new events.
	CurrentProtocolVersion ProtocolVersion = ProtocolVersionV1Alpha1
)

// EventType identifies a hosted protocol event.
// For wrapped agent updates this intentionally reuses the existing UpdateType()
// string instead of creating a parallel vocabulary.
type EventType string

const (
	EventTypeGeneric           EventType = "event"
	EventTypeSessionAttach     EventType = "session_attach"
	EventTypeSessionResume     EventType = "session_resume"
	EventTypeLifecycleStart    EventType = "lifecycle_start"
	EventTypeLifecycleComplete EventType = "lifecycle_complete"
	EventTypeLifecycleError    EventType = "lifecycle_error"
	EventTypeQuestionRequest   EventType = "question_request"
	EventTypeQuestionResponse  EventType = "question_response"
	EventTypeApprovalRequest   EventType = "approval_request"
	EventTypeApprovalResponse  EventType = "approval_response"
	EventTypeNotification      EventType = "notification"
	EventTypeCheckpoint        EventType = "checkpoint"
	EventTypeWorkspaceExport   EventType = "workspace_export"
)

// UpdateTyper is implemented by payloads that already expose an update type.
// agent.IntermediateUpdate values satisfy this contract without creating a
// dependency from this package back to Ring 2.
type UpdateTyper interface {
	UpdateType() string
}

// ReplayCursor identifies a replay point for reconnect and catch-up flows.
type ReplayCursor struct {
	// Value is the opaque cursor value suitable for transport.
	Value string `json:"value,omitempty"`

	// Sequence is the last acknowledged or emitted monotonic sequence number.
	Sequence int64 `json:"sequence,omitempty"`
}

// ResumeToken carries session attach or reconnect authorization.
type ResumeToken struct {
	// Value is the opaque token value.
	Value string `json:"value,omitempty"`

	// IssuedAt is when the token was created.
	IssuedAt *time.Time `json:"issued_at,omitempty"`

	// ExpiresAt is when the token becomes invalid.
	ExpiresAt *time.Time `json:"expires_at,omitempty"`
}

// HostedEvent is the typed payload carried over the hosted protocol.
type HostedEvent struct {
	Type    EventType `json:"type"`
	Payload any       `json:"payload,omitempty"`
}

// EventEnvelope is the SDK-owned hosted session event envelope.
type EventEnvelope struct {
	// ProtocolVersion identifies the hosted protocol contract.
	ProtocolVersion ProtocolVersion `json:"protocol_version"`

	// Sequence is the monotonic stream sequence number for this event.
	Sequence int64 `json:"sequence"`

	// Cursor identifies the replay position associated with this event.
	Cursor *ReplayCursor `json:"cursor,omitempty"`

	// ResumeToken can be used by clients to resume or re-attach safely.
	ResumeToken *ResumeToken `json:"resume_token,omitempty"`

	// Timestamp records when the envelope was created.
	Timestamp time.Time `json:"timestamp"`

	// Session identifies the hosted session scope.
	Session *SessionMetadata `json:"session,omitempty"`

	// Execution carries additive hosted execution context.
	Execution *ExecutionMetadata `json:"execution,omitempty"`

	// Interaction carries approval/question/notification metadata.
	Interaction *InteractionMetadata `json:"interaction,omitempty"`

	// Policy carries action classes, capabilities, provenance, and risk.
	Policy *PolicyMetadata `json:"policy,omitempty"`

	// Redaction carries additive redaction guidance for downstream consumers.
	Redaction *RedactionMetadata `json:"redaction,omitempty"`

	// Event is the actual event payload.
	Event HostedEvent `json:"event"`
}

// SessionMetadata identifies the hosted session scope for an event.
type SessionMetadata struct {
	SessionID     string `json:"session_id,omitempty"`
	ProjectID     string `json:"project_id,omitempty"`
	TenantID      string `json:"tenant_id,omitempty"`
	WorkspaceID   string `json:"workspace_id,omitempty"`
	TraceID       string `json:"trace_id,omitempty"`
	ParentTraceID string `json:"parent_trace_id,omitempty"`
	RunID         string `json:"run_id,omitempty"`
}

// ExecutionMetadata carries additive hosted execution metadata.
type ExecutionMetadata struct {
	SessionID     string            `json:"session_id,omitempty"`
	ProjectID     string            `json:"project_id,omitempty"`
	TenantID      string            `json:"tenant_id,omitempty"`
	WorkspaceID   string            `json:"workspace_id,omitempty"`
	RunID         string            `json:"run_id,omitempty"`
	RequestID     string            `json:"request_id,omitempty"`
	TraceID       string            `json:"trace_id,omitempty"`
	ParentTraceID string            `json:"parent_trace_id,omitempty"`
	ActorID       string            `json:"actor_id,omitempty"`
	NodeID        string            `json:"node_id,omitempty"`
	PolicyContext map[string]string `json:"policy_context,omitempty"`
	Labels        map[string]string `json:"labels,omitempty"`
}

// InteractionKind identifies the class of hosted interaction.
type InteractionKind string

const (
	InteractionKindQuestion     InteractionKind = "question"
	InteractionKindApproval     InteractionKind = "approval"
	InteractionKindNotification InteractionKind = "notification"
)

// InteractionStatus identifies the state of a hosted interaction.
type InteractionStatus string

const (
	InteractionStatusPending   InteractionStatus = "pending"
	InteractionStatusAnswered  InteractionStatus = "answered"
	InteractionStatusDelivered InteractionStatus = "delivered"
	InteractionStatusCanceled  InteractionStatus = "canceled"
	InteractionStatusTimedOut  InteractionStatus = "timed_out"
)

// InteractionMetadata carries additive hosted interaction state.
type InteractionMetadata struct {
	ID          string            `json:"id,omitempty"`
	Kind        InteractionKind   `json:"kind,omitempty"`
	Status      InteractionStatus `json:"status,omitempty"`
	SessionID   string            `json:"session_id,omitempty"`
	ClientID    string            `json:"client_id,omitempty"`
	RequestedAt *time.Time        `json:"requested_at,omitempty"`
	ExpiresAt   *time.Time        `json:"expires_at,omitempty"`
	Cursor      *ReplayCursor     `json:"cursor,omitempty"`
	ResumeToken *ResumeToken      `json:"resume_token,omitempty"`
}

// InteractionHandle represents a durable pending interaction handle.
type InteractionHandle struct {
	ID          string            `json:"id,omitempty"`
	Kind        InteractionKind   `json:"kind,omitempty"`
	Status      InteractionStatus `json:"status,omitempty"`
	SessionID   string            `json:"session_id,omitempty"`
	Sequence    int64             `json:"sequence,omitempty"`
	Cursor      *ReplayCursor     `json:"cursor,omitempty"`
	ResumeToken *ResumeToken      `json:"resume_token,omitempty"`
	ExpiresAt   *time.Time        `json:"expires_at,omitempty"`
}

// ActionClass describes a hosted policy action class.
type ActionClass string

const (
	ActionClassRead            ActionClass = "read"
	ActionClassWrite           ActionClass = "write"
	ActionClassExecute         ActionClass = "execute"
	ActionClassExternalNetwork ActionClass = "external_network"
	ActionClassDeploy          ActionClass = "deploy"
	ActionClassSecretAccess    ActionClass = "secret_access"
)

// Capability describes a hosted capability classification.
type Capability string

const (
	CapabilityRead             Capability = "read"
	CapabilityWrite            Capability = "write"
	CapabilityExecute          Capability = "execute"
	CapabilityExternalNetwork  Capability = "external_network"
	CapabilityDeploy           Capability = "deploy"
	CapabilitySecretAccess     Capability = "secret_access"
	CapabilityQuestion         Capability = "question"
	CapabilityApproval         Capability = "approval"
	CapabilityNotification     Capability = "notification"
	CapabilitySnapshot         Capability = "snapshot"
	CapabilityCredentialBroker Capability = "credential_broker"
	CapabilitySessionReplay    Capability = "session_replay"
)

// RiskLevel describes the hosted risk level for an action or payload.
type RiskLevel string

const (
	RiskLevelLow      RiskLevel = "low"
	RiskLevelMedium   RiskLevel = "medium"
	RiskLevelHigh     RiskLevel = "high"
	RiskLevelCritical RiskLevel = "critical"
)

// ProvenanceKind identifies the origin of model-visible or logged content.
type ProvenanceKind string

const (
	ProvenanceUserPrompt        ProvenanceKind = "user_prompt"
	ProvenanceSystemInstruction ProvenanceKind = "system_instruction"
	ProvenanceRepository        ProvenanceKind = "repository_content"
	ProvenanceToolOutput        ProvenanceKind = "tool_output"
	ProvenanceExternalContent   ProvenanceKind = "external_content"
	ProvenanceModelOutput       ProvenanceKind = "model_output"
	ProvenanceCredential        ProvenanceKind = "credential_material"
)

// ProvenanceMarker identifies the origin and trust characteristics of data.
type ProvenanceMarker struct {
	Kind    ProvenanceKind `json:"kind"`
	Source  string         `json:"source,omitempty"`
	Trusted bool           `json:"trusted,omitempty"`
}

// PolicyMetadata describes hosted action classes, capabilities, provenance, and risk.
type PolicyMetadata struct {
	ContextID        string             `json:"context_id,omitempty"`
	ActionClasses    []ActionClass      `json:"action_classes,omitempty"`
	Capabilities     []Capability       `json:"capabilities,omitempty"`
	Risk             RiskLevel          `json:"risk,omitempty"`
	RequiresApproval bool               `json:"requires_approval,omitempty"`
	Provenance       []ProvenanceMarker `json:"provenance,omitempty"`
	Labels           map[string]string  `json:"labels,omitempty"`
}

// RedactionType identifies a redaction category.
type RedactionType string

const (
	RedactionTypeSecret     RedactionType = "secret"
	RedactionTypeToken      RedactionType = "token"
	RedactionTypeCredential RedactionType = "credential"
	RedactionTypePII        RedactionType = "pii"
	RedactionTypeRepository RedactionType = "repository_content"
	RedactionTypePath       RedactionType = "path"
)

// RedactionHint carries additive redaction guidance.
type RedactionHint struct {
	Type        RedactionType `json:"type"`
	Placeholder string        `json:"placeholder,omitempty"`
	Pattern     string        `json:"pattern,omitempty"`
	Reason      string        `json:"reason,omitempty"`
}

// RedactionMetadata carries additive redaction context for events and outputs.
type RedactionMetadata struct {
	Hints             []RedactionHint `json:"hints,omitempty"`
	ContainsSensitive bool            `json:"contains_sensitive,omitempty"`
	TenantScoped      bool            `json:"tenant_scoped,omitempty"`
	AuditOnly         bool            `json:"audit_only,omitempty"`
}

// OutputMode identifies how a result is delivered to the client.
type OutputMode string

const (
	OutputModeInline   OutputMode = "inline"
	OutputModeArtifact OutputMode = "artifact"
	OutputModeMixed    OutputMode = "mixed"
)

// TaskClass identifies the execution class for a hosted task.
type TaskClass string

const (
	TaskClassForeground  TaskClass = "foreground"
	TaskClassBackground  TaskClass = "background"
	TaskClassHeavyOutput TaskClass = "heavy_output"
)

// ArtifactKind describes the class of a bulk-output artifact.
type ArtifactKind string

const (
	ArtifactKindLog  ArtifactKind = "log"
	ArtifactKindDiff ArtifactKind = "diff"
	ArtifactKindFile ArtifactKind = "file"
)

// ArtifactReference identifies lazily fetched hosted output.
type ArtifactReference struct {
	ID          string       `json:"id,omitempty"`
	Name        string       `json:"name,omitempty"`
	Kind        ArtifactKind `json:"kind,omitempty"`
	MimeType    string       `json:"mime_type,omitempty"`
	SizeBytes   int64        `json:"size_bytes,omitempty"`
	PreviewText string       `json:"preview_text,omitempty"`
	Truncated   bool         `json:"truncated,omitempty"`
}

// ResultMetadata carries additive hosted metadata for tool or action outputs.
type ResultMetadata struct {
	Policy      *PolicyMetadata     `json:"policy,omitempty"`
	Redaction   *RedactionMetadata  `json:"redaction,omitempty"`
	OutputMode  OutputMode          `json:"output_mode,omitempty"`
	TaskClass   TaskClass           `json:"task_class,omitempty"`
	HeavyOutput bool                `json:"heavy_output,omitempty"`
	Artifacts   []ArtifactReference `json:"artifacts,omitempty"`
}

// ArtifactFetchRequest identifies an artifact fetch for hosted clients.
type ArtifactFetchRequest struct {
	SessionID  string `json:"session_id,omitempty"`
	ArtifactID string `json:"artifact_id,omitempty"`
}

// ArtifactFetchResult contains the fetched artifact body.
type ArtifactFetchResult struct {
	Artifact ArtifactReference `json:"artifact"`
	Text     string            `json:"text,omitempty"`
	MimeType string            `json:"mime_type,omitempty"`
}

// CredentialKind identifies the type of credential grant requested.
type CredentialKind string

const (
	CredentialKindRepository      CredentialKind = "repository"
	CredentialKindModelProvider   CredentialKind = "model_provider"
	CredentialKindCapabilityToken CredentialKind = "capability_token"
)

// CredentialGrantRequest describes a hosted credential broker request.
type CredentialGrantRequest struct {
	Kind       CredentialKind    `json:"kind"`
	SessionID  string            `json:"session_id,omitempty"`
	ProjectID  string            `json:"project_id,omitempty"`
	TenantID   string            `json:"tenant_id,omitempty"`
	ToolName   string            `json:"tool_name,omitempty"`
	Provider   string            `json:"provider,omitempty"`
	AccountKey string            `json:"account_key,omitempty"`
	Reason     string            `json:"reason,omitempty"`
	Scopes     []string          `json:"scopes,omitempty"`
	TTLSeconds int64             `json:"ttl_seconds,omitempty"`
	Policy     *PolicyMetadata   `json:"policy,omitempty"`
	Metadata   map[string]string `json:"metadata,omitempty"`
}

// CredentialGrant is a brokered credential response for hosted mode.
type CredentialGrant struct {
	ID        string             `json:"id,omitempty"`
	Kind      CredentialKind     `json:"kind"`
	Provider  string             `json:"provider,omitempty"`
	Token     string             `json:"token,omitempty"`
	Scopes    []string           `json:"scopes,omitempty"`
	IssuedAt  time.Time          `json:"issued_at"`
	ExpiresAt time.Time          `json:"expires_at"`
	Policy    *PolicyMetadata    `json:"policy,omitempty"`
	Redaction *RedactionMetadata `json:"redaction,omitempty"`
	Metadata  map[string]string  `json:"metadata,omitempty"`
}

// NewEnvelope creates a new hosted event envelope with the current protocol version.
func NewEnvelope(eventType EventType, payload any) *EventEnvelope {
	return &EventEnvelope{
		ProtocolVersion: CurrentProtocolVersion,
		Timestamp:       time.Now().UTC(),
		Event: HostedEvent{
			Type:    eventType,
			Payload: payload,
		},
	}
}

// NewUpdateEnvelope wraps an existing update payload using its UpdateType string.
func NewUpdateEnvelope(sequence int64, payload any) *EventEnvelope {
	return NewEnvelope(EventTypeForPayload(payload), payload).WithSequence(sequence)
}

// EventTypeForPayload derives the event type from a payload.
func EventTypeForPayload(payload any) EventType {
	if updater, ok := payload.(UpdateTyper); ok {
		if updateType := updater.UpdateType(); updateType != "" {
			return EventType(updateType)
		}
	}
	return EventTypeGeneric
}

// WithSequence sets the monotonic sequence number and a default replay cursor.
func (e *EventEnvelope) WithSequence(sequence int64) *EventEnvelope {
	e.Sequence = sequence
	if sequence > 0 && e.Cursor == nil {
		e.Cursor = &ReplayCursor{
			Value:    fmt.Sprintf("%d", sequence),
			Sequence: sequence,
		}
	}
	return e
}

// WithReplayCursor sets the replay cursor.
func (e *EventEnvelope) WithReplayCursor(cursor *ReplayCursor) *EventEnvelope {
	e.Cursor = cursor
	return e
}

// WithResumeToken sets the resume token.
func (e *EventEnvelope) WithResumeToken(token *ResumeToken) *EventEnvelope {
	e.ResumeToken = token
	return e
}

// WithSession sets the session metadata.
func (e *EventEnvelope) WithSession(session *SessionMetadata) *EventEnvelope {
	e.Session = session
	return e
}

// WithExecution sets the execution metadata.
func (e *EventEnvelope) WithExecution(execution *ExecutionMetadata) *EventEnvelope {
	e.Execution = execution
	return e
}

// WithInteraction sets the interaction metadata.
func (e *EventEnvelope) WithInteraction(interaction *InteractionMetadata) *EventEnvelope {
	e.Interaction = interaction
	return e
}

// WithPolicy sets the policy metadata.
func (e *EventEnvelope) WithPolicy(policy *PolicyMetadata) *EventEnvelope {
	e.Policy = policy
	return e
}

// WithRedaction sets the redaction metadata.
func (e *EventEnvelope) WithRedaction(redaction *RedactionMetadata) *EventEnvelope {
	e.Redaction = redaction
	return e
}

// PayloadJSON marshals the payload into JSON for transport or decoding.
func (e *EventEnvelope) PayloadJSON() (json.RawMessage, error) {
	if e.Event.Payload == nil {
		return nil, nil
	}
	raw, err := json.Marshal(e.Event.Payload)
	if err != nil {
		return nil, err
	}
	return json.RawMessage(raw), nil
}

// DecodePayload decodes the payload into a caller-provided destination.
func (e *EventEnvelope) DecodePayload(target any) error {
	raw, err := e.PayloadJSON()
	if err != nil {
		return err
	}
	if len(raw) == 0 {
		return nil
	}
	return json.Unmarshal(raw, target)
}

// IsZero reports whether the execution metadata is empty.
func (m *ExecutionMetadata) IsZero() bool {
	if m == nil {
		return true
	}
	return m.SessionID == "" &&
		m.ProjectID == "" &&
		m.TenantID == "" &&
		m.WorkspaceID == "" &&
		m.RunID == "" &&
		m.RequestID == "" &&
		m.TraceID == "" &&
		m.ParentTraceID == "" &&
		m.ActorID == "" &&
		m.NodeID == "" &&
		len(m.PolicyContext) == 0 &&
		len(m.Labels) == 0
}

// ToSessionMetadata converts execution metadata into session metadata.
func (m *ExecutionMetadata) ToSessionMetadata() *SessionMetadata {
	if m == nil || m.IsZero() {
		return nil
	}
	return &SessionMetadata{
		SessionID:     m.SessionID,
		ProjectID:     m.ProjectID,
		TenantID:      m.TenantID,
		WorkspaceID:   m.WorkspaceID,
		TraceID:       m.TraceID,
		ParentTraceID: m.ParentTraceID,
		RunID:         m.RunID,
	}
}

// IsZero reports whether the result metadata is empty.
func (m *ResultMetadata) IsZero() bool {
	if m == nil {
		return true
	}
	return m.Policy == nil &&
		m.Redaction == nil &&
		m.OutputMode == "" &&
		m.TaskClass == "" &&
		!m.HeavyOutput &&
		len(m.Artifacts) == 0
}

// ExecutionMetadataFromMap extracts hosted execution metadata from a generic context map.
func ExecutionMetadataFromMap(values map[string]any) *ExecutionMetadata {
	if len(values) == 0 {
		return nil
	}

	metadata := &ExecutionMetadata{
		SessionID:     stringFromKeys(values, "session_id", "sessionID"),
		ProjectID:     stringFromKeys(values, "project_id", "projectID"),
		TenantID:      stringFromKeys(values, "tenant_id", "tenantID"),
		WorkspaceID:   stringFromKeys(values, "workspace_id", "workspaceID"),
		RunID:         stringFromKeys(values, "run_id", "runID"),
		RequestID:     stringFromKeys(values, "request_id", "requestID"),
		TraceID:       stringFromKeys(values, "trace_id", "traceID"),
		ParentTraceID: stringFromKeys(values, "parent_trace_id", "parentTraceID"),
		ActorID:       stringFromKeys(values, "actor_id", "actorID"),
		NodeID:        stringFromKeys(values, "node_id", "nodeID"),
		PolicyContext: stringMapFromKeys(values, "policy_context", "policyContext"),
		Labels:        stringMapFromKeys(values, "labels"),
	}

	if metadata.IsZero() {
		return nil
	}
	return metadata
}

func stringFromKeys(values map[string]any, keys ...string) string {
	for _, key := range keys {
		value, ok := values[key]
		if !ok {
			continue
		}
		switch v := value.(type) {
		case string:
			return v
		case fmt.Stringer:
			return v.String()
		}
	}
	return ""
}

func stringMapFromKeys(values map[string]any, keys ...string) map[string]string {
	for _, key := range keys {
		value, ok := values[key]
		if !ok {
			continue
		}
		switch typed := value.(type) {
		case map[string]string:
			return cloneStringMap(typed)
		case map[string]any:
			result := make(map[string]string, len(typed))
			for nestedKey, nestedValue := range typed {
				switch v := nestedValue.(type) {
				case string:
					result[nestedKey] = v
				case fmt.Stringer:
					result[nestedKey] = v.String()
				}
			}
			if len(result) > 0 {
				return result
			}
		}
	}
	return nil
}

func cloneStringMap(values map[string]string) map[string]string {
	if len(values) == 0 {
		return nil
	}
	cloned := make(map[string]string, len(values))
	maps.Copy(cloned, values)
	return cloned
}
