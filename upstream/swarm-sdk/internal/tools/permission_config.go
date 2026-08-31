package tools

import "context"

// PermissionLevel defines the strictness of permission checking.
type PermissionLevel string

const (
	// LevelAlwaysAsk requests approval for every tool call (most restrictive).
	LevelAlwaysAsk PermissionLevel = "always_ask"

	// LevelBalanced asks for first-time permissions and dangerous actions only.
	LevelBalanced PermissionLevel = "balanced"

	// LevelPermissive only asks for dangerous actions.
	LevelPermissive PermissionLevel = "permissive"

	// LevelYOLO bypasses all permission checks (most permissive).
	LevelYOLO PermissionLevel = "yolo"
)

// OverridePolicy defines per-tool permission behavior.
type OverridePolicy string

const (
	// OverrideAlwaysAllow grants permission without asking.
	OverrideAlwaysAllow OverridePolicy = "always_allow"

	// OverrideAlwaysAsk always requires user approval.
	OverrideAlwaysAsk OverridePolicy = "always_ask"

	// OverrideAlwaysDeny blocks the tool entirely.
	OverrideAlwaysDeny OverridePolicy = "always_deny"
)

// PermissionDefaults holds default permission policies.
type PermissionDefaults struct {
	// Policies maps permissions to their default policy.
	Policies map[Permission]PermissionPolicy `json:"policies"`
}

// PermissionOverrides holds per-tool and per-permission override policies.
type PermissionOverrides struct {
	// Tools maps tool names to override policies.
	Tools map[string]OverridePolicy `json:"tools,omitempty"`

	// Permissions maps permissions to override policies.
	Permissions map[Permission]OverridePolicy `json:"permissions,omitempty"`
}

// PermissionMetadata stores audit metadata for the config.
type PermissionMetadata struct {
	// UpdatedAt is an RFC3339 timestamp of the last update.
	UpdatedAt string `json:"updatedAt,omitempty"`

	// Source indicates where the config was last updated (tui/headless/etc).
	Source string `json:"source,omitempty"`
}

// PermissionConfig holds user permission settings.
type PermissionConfig struct {
	// Version is the schema version (must be 1).
	Version int `json:"version"`

	// Level is the permission strictness (always_ask, balanced, permissive, yolo).
	Level PermissionLevel `json:"level"`

	// TimeoutSeconds is the number of seconds before auto-deny (default 300).
	TimeoutSeconds int `json:"timeoutSeconds"`

	// TimeoutBehavior is what happens on timeout: "stop" or "continue".
	TimeoutBehavior string `json:"timeoutBehavior"`

	// Defaults contains default permission policies.
	Defaults PermissionDefaults `json:"defaults"`

	// Overrides contains per-tool/per-permission policies.
	Overrides PermissionOverrides `json:"overrides"`

	// Rules contains rule-based permission policies.
	Rules []PermissionRule `json:"rules,omitempty"`

	// Metadata contains optional audit metadata.
	Metadata PermissionMetadata `json:"metadata"`
}

// DefaultPermissionConfig returns the default permission configuration.
func DefaultPermissionConfig() PermissionConfig {
	var defaults PermissionDefaults = PermissionDefaults{
		Policies: DefaultPermissionPolicies(),
	}
	var overrides PermissionOverrides = PermissionOverrides{
		Tools:       make(map[string]OverridePolicy),
		Permissions: make(map[Permission]OverridePolicy),
	}
	return PermissionConfig{
		Version:         1,
		Level:           LevelBalanced,
		TimeoutSeconds:  300,
		TimeoutBehavior: "stop",
		Defaults:        defaults,
		Overrides:       overrides,
		Rules:           make([]PermissionRule, 0),
	}
}

// Decision represents a user's approval decision.
type Decision string

const (
	// DecisionApproveOnce allows the action once.
	DecisionApproveOnce Decision = "approve_once"

	// DecisionApproveSession allows the action for the current session.
	DecisionApproveSession Decision = "approve_session"

	// DecisionApproveAlways adds a permanent tool override.
	DecisionApproveAlways Decision = "approve_always"

	// DecisionSaveProject adds a project-level tool override.
	DecisionSaveProject Decision = "save_project"

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

// PermissionApprovalRequest represents a request for user approval.
type PermissionApprovalRequest struct {
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

// ApprovalResponse represents the user's response to a permission request.
type ApprovalResponse struct {
	// Decision is the user's decision.
	Decision Decision `json:"decision"`

	// Outcome is the final outcome (set by broker).
	Outcome Outcome `json:"outcome,omitempty"`

	// GrantedScope indicates what scope was granted.
	GrantedScope string `json:"grantedScope,omitempty"`

	// UserContext captures optional user-provided context from the UI.
	UserContext string `json:"userContext,omitempty"`
}

// ApprovalBroker manages permission request lifecycle.
type ApprovalBroker interface {
	// Request sends approval request to UI, blocks until response or timeout.
	Request(ctx context.Context, req PermissionApprovalRequest) (ApprovalResponse, error)

	// Respond handles response from UI.
	Respond(requestID string, decision Decision) error

	// SetConfig updates permission configuration.
	SetConfig(config PermissionConfig)

	// GetPendingRequests returns all pending requests.
	GetPendingRequests() []PermissionApprovalRequest
}
