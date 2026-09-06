package interaction

import (
	"context"
	"time"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/hosted"
)

// UserInteractionBroker handles all user interaction requests (questions, approvals, notifications).
// TUI and other frontends implement this interface to provide their specific UI.
// This is Ring 0 - interface only.
type UserInteractionBroker interface {
	// AskQuestion presents a question to the user and waits for response.
	AskQuestion(ctx context.Context, request QuestionRequest) (QuestionResponse, error)

	// AskApproval is a specialized question for permission approval.
	// Implementations may optimize this for approval-specific UI.
	AskApproval(ctx context.Context, request ApprovalRequest) (ApprovalResponse, error)

	// Notify sends a non-blocking notification to the user (info, warning, error, success).
	Notify(ctx context.Context, notification Notification) error
}

// QuestionType defines the type of user interaction.
type QuestionType string

const (
	QuestionTypeText          QuestionType = "text"          // Free-form text input
	QuestionTypeChoice        QuestionType = "choice"        // Single choice from list
	QuestionTypeMultiChoice   QuestionType = "multi_choice"  // Multiple selections
	QuestionTypeConfirm       QuestionType = "confirm"       // Yes/No confirmation
	QuestionTypeNumber        QuestionType = "number"        // Numeric input
	QuestionTypeVisualChoice  QuestionType = "visual_choice" // Rich choice rendered by the active client
	QuestionTypeQuestionnaire QuestionType = "questionnaire" // A batch of related, structured questions
)

// QuestionOption is a stable, model-facing option for a questionnaire question.
type QuestionOption struct {
	Value       string `json:"value"`
	Label       string `json:"label"`
	Description string `json:"description,omitempty"`
}

// QuestionnaireQuestion is one item in a questionnaire request.
type QuestionnaireQuestion struct {
	ID         string           `json:"id"`
	Prompt     string           `json:"prompt"`
	Label      string           `json:"label,omitempty"`
	Options    []QuestionOption `json:"options"`
	AllowOther bool             `json:"allow_other"`
}

// QuestionnaireAnswer is a structured answer to one questionnaire question.
type QuestionnaireAnswer struct {
	ID        string `json:"id"`
	Value     string `json:"value"`
	Label     string `json:"label"`
	WasCustom bool   `json:"was_custom"`
	Index     *int   `json:"index,omitempty"`
}

// QuestionRequest represents a generic question to the user.
type QuestionRequest struct {
	// ID uniquely identifies this request.
	ID string

	// Question is the text of the question.
	Question string

	// Type is the type of question (text, choice, confirm, etc).
	Type QuestionType

	// Choices are the available options (for choice-based questions).
	Choices []string

	// Options are structured alternatives for a singleton choice question.
	// Choices remains supported for backwards compatibility.
	Options []QuestionOption

	// Questions contains the items when Type is QuestionTypeQuestionnaire.
	Questions []QuestionnaireQuestion

	// Default is the default value or selected choice.
	Default string

	// Timeout is the maximum time to wait for a response in seconds.
	// 0 means no timeout.
	Timeout int

	// Context contains additional context data (agentID, conversationID, tool, etc).
	Context map[string]any

	// Metadata provides rich information for UI rendering.
	Metadata QuestionMetadata

	// Visual carries a visual primitive to render when Type == QuestionTypeVisualChoice.
	// Stored as `any` at the interaction layer to avoid a dependency cycle on
	// interaction/visual; the tool layer populates it with a typed primitive.
	Visual any `json:"visual,omitempty"`

	// Hosted carries additive hosted interaction metadata.
	Hosted *hosted.InteractionMetadata
}

// QuestionMetadata provides rich context for UI rendering.
type QuestionMetadata struct {
	// Title is an optional title for the question dialog.
	Title string

	// Description is optional detailed context about the question.
	Description string

	// Tags categorize the question for organization and filtering.
	Tags []string

	// Priority is the importance level ("low", "medium", "high", "critical").
	Priority string

	// AgentID identifies which agent is asking the question.
	AgentID string

	// ToolName identifies which tool is asking the question.
	ToolName string

	// Source indicates where the question originated (sdk, agent, permission, etc).
	Source string

	// PlanID links the question to a plan screen when the agent is in plan mode.
	PlanID string
}

// QuestionResponse represents the user's answer to a question.
type QuestionResponse struct {
	// Answer is the user's response (for text, choice, number questions).
	Answer string

	// Answers are multiple responses (for multi-choice questions).
	Answers []string

	// QuestionnaireAnswers are structured responses to a questionnaire.
	QuestionnaireAnswers []QuestionnaireAnswer

	// Confirmed is whether the user confirmed (for confirm questions).
	Confirmed bool

	// Canceled indicates the user canceled the question.
	Canceled bool

	// Timeout indicates the request timed out without user response.
	Timeout bool

	// RespondedAt is when the user responded.
	RespondedAt time.Time

	// Hosted carries additive hosted interaction metadata.
	Hosted *hosted.InteractionMetadata
}

// ApprovalRequest represents a permission approval request.
type ApprovalRequest struct {
	// ID uniquely identifies this approval request.
	ID string

	// Tool is the name of the tool requesting permission.
	Tool string

	// Permission is the type of permission being requested.
	Permission string

	// Target is what is being requested access to (file path, command, URL, etc).
	Target string

	// Reason is the explanation for why permission is needed.
	Reason string

	// Preview contains content preview for the user to review.
	Preview *ApprovalPreview

	// Context provides additional context about the request.
	Context *ApprovalContext

	// Timeout is the maximum time to wait for approval in seconds.
	Timeout int

	// AllowedDecisions restricts which decisions the user can make.
	// If empty, all decisions are allowed.
	AllowedDecisions []ApprovalDecision

	// Hosted carries additive hosted interaction metadata.
	Hosted *hosted.InteractionMetadata
}

// ApprovalPreview contains content to preview for the user.
type ApprovalPreview struct {
	// Type is the content type (diff, command, text, file).
	Type string

	// Content is the preview content.
	Content string
}

// ApprovalContext provides additional context about an approval request.
type ApprovalContext struct {
	// AgentID identifies the agent requesting approval.
	AgentID string

	// TaskDescription describes what the agent is trying to do.
	TaskDescription string

	// ConversationID identifies the conversation context.
	ConversationID string
}

// ApprovalResponse represents the user's approval decision.
type ApprovalResponse struct {
	// Decision is the user's decision (approve, deny, cancel).
	Decision ApprovalDecision

	// Scope determines the scope of the approval (once, session, always).
	Scope ApprovalScope

	// Canceled indicates the user canceled the request.
	Canceled bool

	// Timeout indicates the request timed out without user response.
	Timeout bool

	// RespondedAt is when the user responded.
	RespondedAt time.Time

	// Hosted carries additive hosted interaction metadata.
	Hosted *hosted.InteractionMetadata
}

// ApprovalDecision defines the approval decision options.
type ApprovalDecision string

const (
	ApprovalApprove ApprovalDecision = "approve"
	ApprovalDeny    ApprovalDecision = "deny"
	ApprovalCancel  ApprovalDecision = "cancel"
)

// ApprovalScope defines the scope of an approval grant.
type ApprovalScope string

const (
	ApprovalScopeOnce    ApprovalScope = "once"    // Approve this operation once
	ApprovalScopeSession ApprovalScope = "session" // Approve for this session
	ApprovalScopeAlways  ApprovalScope = "always"  // Add permanent override
)

// NotificationLevel defines notification severity.
type NotificationLevel string

const (
	NotificationInfo    NotificationLevel = "info"
	NotificationWarning NotificationLevel = "warning"
	NotificationError   NotificationLevel = "error"
	NotificationSuccess NotificationLevel = "success"
)

// Notification represents a non-blocking message to the user.
type Notification struct {
	// ID uniquely identifies this notification.
	ID string

	// Level is the severity level.
	Level NotificationLevel

	// Message is the notification text.
	Message string

	// Duration is how long to display in seconds (0 = until dismissed).
	Duration int

	// CreatedAt is when the notification was created.
	CreatedAt time.Time

	// Hosted carries additive hosted interaction metadata.
	Hosted *hosted.InteractionMetadata
}
