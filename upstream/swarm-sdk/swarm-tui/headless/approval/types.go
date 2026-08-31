// Package approval provides the approval broker for permission requests.
package approval

// Decision represents a user's approval decision.
type Decision string

const (
	// DecisionApproveOnce allows the action once.
	DecisionApproveOnce Decision = "approve_once"

	// DecisionApproveSession allows the action for the current session.
	DecisionApproveSession Decision = "approve_session"

	// DecisionApproveAlways adds a permanent tool override.
	DecisionApproveAlways Decision = "approve_always"

	// DecisionDeny denies the action.
	DecisionDeny Decision = "deny"

	// DecisionDenyStop denies and aborts the current task.
	DecisionDenyStop Decision = "deny_stop"
)

// Outcome represents the final outcome of a permission request.
type Outcome string

const (
	// OutcomeApproved means the request was approved.
	OutcomeApproved Outcome = "approved"

	// OutcomeDenied means the request was denied.
	OutcomeDenied Outcome = "denied"

	// OutcomeTimeout means the request timed out.
	OutcomeTimeout Outcome = "timeout"
)

// PermissionRequest represents a request for user approval.
type PermissionRequest struct {
	// RequestID is the unique identifier for this request.
	RequestID string `json:"requestId"`

	// ConversationID is the current conversation.
	ConversationID string `json:"conversationId"`

	// Tool is the tool requesting permission.
	Tool string `json:"tool"`

	// Permission is the permission type being requested.
	Permission string `json:"permission"`

	// Target is the file path, command, or URL.
	Target string `json:"target"`

	// Reason explains why permission is needed.
	Reason string `json:"reason"`

	// Preview contains optional contextual preview.
	Preview *ApprovalPreview `json:"preview,omitempty"`

	// Context contains additional request context.
	Context *ApprovalContext `json:"context,omitempty"`

	// Timeout is seconds until auto-deny.
	Timeout int `json:"timeout"`

	// BatchID is non-empty if part of a batch.
	BatchID string `json:"batchId,omitempty"`
}

// ApprovalPreview contains preview content for the approval card.
type ApprovalPreview struct {
	// Type is "diff", "command", or "text".
	Type string `json:"type"`

	// Content is the preview content.
	Content string `json:"content"`
}

// ApprovalContext contains additional context for the request.
type ApprovalContext struct {
	// AgentID identifies the requesting agent.
	AgentID string `json:"agentId,omitempty"`

	// TaskDescription describes the current task.
	TaskDescription string `json:"taskDescription,omitempty"`
}

// ApprovalResponse represents the response to a permission request.
type ApprovalResponse struct {
	// Decision is the user's decision.
	Decision Decision `json:"decision"`

	// Outcome is the final outcome (set by broker).
	Outcome Outcome `json:"outcome,omitempty"`

	// GrantedScope indicates what scope was granted.
	GrantedScope string `json:"grantedScope,omitempty"`
}

// BrokerConfig holds the approval broker configuration.
type BrokerConfig struct {
	// Timeout is the default timeout in seconds.
	Timeout int `json:"timeout"`

	// TimeoutBehavior is what happens on timeout: "stop" or "continue".
	TimeoutBehavior string `json:"timeoutBehavior"`

	// BatchingEnabled enables smart batching of similar requests.
	BatchingEnabled bool `json:"batchingEnabled"`

	// BatchWindow is the time window in milliseconds for batching.
	BatchWindow int `json:"batchWindow"`

	// AutoApproveAll bypasses all interactive permission checks and immediately
	// approves every request. Used when the active environment's permissionMode
	// is "auto" or "bypassPermissions".
	AutoApproveAll bool `json:"autoApproveAll,omitempty"`
}

// DefaultConfig returns the default broker configuration.
func DefaultConfig() BrokerConfig {
	return BrokerConfig{
		Timeout:         300,
		TimeoutBehavior: "stop",
		BatchingEnabled: true,
		BatchWindow:     100,
	}
}
