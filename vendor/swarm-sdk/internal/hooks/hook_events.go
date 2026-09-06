// Package hooks implements the event hook system.
// This file defines the complete set of hook events matching Gemini CLI.
package hooks

import (
	"time"
)

// HookEventName represents the lifecycle event types.
// These match the Gemini CLI hook events for compatibility.
type HookEventName string

const (
	// Session lifecycle events
	EventSessionStart HookEventName = "SessionStart"
	EventSessionEnd   HookEventName = "SessionEnd"

	// Agent loop events
	EventBeforeAgent  HookEventName = "BeforeAgent"
	EventAfterAgent   HookEventName = "AfterAgent"
	EventSubagentStop HookEventName = "SubagentStop" // Claude Code only

	// Model interaction events
	EventBeforeModel         HookEventName = "BeforeModel"
	EventAfterModel          HookEventName = "AfterModel"
	EventBeforeToolSelection HookEventName = "BeforeToolSelection"

	// Tool execution events (maps to existing tool.before_execute, tool.after_execute)
	EventBeforeTool HookEventName = "BeforeTool"
	EventAfterTool  HookEventName = "AfterTool"

	// Other events
	EventPreCompress  HookEventName = "PreCompress"
	EventNotification HookEventName = "Notification"
)

// ConfigSource represents where a hook configuration came from.
type ConfigSource string

const (
	SourceProject    ConfigSource = "project"
	SourceUser       ConfigSource = "user"
	SourceSystem     ConfigSource = "system"
	SourceExtensions ConfigSource = "extensions"
)

// HookDecision represents the outcome of a hook evaluation.
type HookDecision string

const (
	DecisionAllow   HookDecision = "allow"
	DecisionDeny    HookDecision = "deny"
	DecisionBlock   HookDecision = "block"
	DecisionAsk     HookDecision = "ask"
	DecisionApprove HookDecision = "approve"
)

// IsBlocking returns true if the decision blocks execution.
func (d HookDecision) IsBlocking() bool {
	return d == DecisionDeny || d == DecisionBlock
}

// NeedsConfirmation returns true if user confirmation is needed.
func (d HookDecision) NeedsConfirmation() bool {
	return d == DecisionAsk
}

// HookInput is the base input structure for all hook events.
type HookInput struct {
	SessionID      string    `json:"session_id"`
	TranscriptPath string    `json:"transcript_path"`
	Cwd            string    `json:"cwd"`
	HookEventName  string    `json:"hook_event_name"`
	Timestamp      time.Time `json:"timestamp"`
}

// HookResponse is the base output structure from all hooks (Gemini CLI compatible).
// Named HookResponse to avoid collision with existing HookOutput in registry.go.
type HookResponse struct {
	Continue           *bool          `json:"continue,omitempty"`
	StopReason         string         `json:"stopReason,omitempty"`
	SuppressOutput     bool           `json:"suppressOutput,omitempty"`
	SystemMessage      string         `json:"systemMessage,omitempty"`
	Decision           HookDecision   `json:"decision,omitempty"`
	Reason             string         `json:"reason,omitempty"`
	HookSpecificOutput map[string]any `json:"hookSpecificOutput,omitempty"`
}

// ShouldContinue returns whether execution should continue.
func (o *HookResponse) ShouldContinue() bool {
	if o.Continue != nil {
		return *o.Continue
	}
	return true
}

// IsBlockingDecision returns whether the decision blocks execution.
func (o *HookResponse) IsBlockingDecision() bool {
	return o.Decision.IsBlocking()
}

// GetAdditionalContext returns any additional context from the hook.
func (o *HookResponse) GetAdditionalContext() string {
	if o.HookSpecificOutput == nil {
		return ""
	}
	if ctx, ok := o.HookSpecificOutput["additionalContext"].(string); ok {
		return ctx
	}
	return ""
}

// ─────────────────────────────────────────────────────────────────────────────
// Event-Specific Input Types
// ─────────────────────────────────────────────────────────────────────────────

// SessionStartSource represents why a session started.
type SessionStartSource string

const (
	SessionSourceStartup SessionStartSource = "startup"
	SessionSourceResume  SessionStartSource = "resume"
	SessionSourceClear   SessionStartSource = "clear"
)

// SessionStartInput is the input for SessionStart events.
type SessionStartInput struct {
	HookInput
	Source SessionStartSource `json:"source"`
}

// SessionEndReason represents why a session ended.
type SessionEndReason string

const (
	SessionEndExit            SessionEndReason = "exit"
	SessionEndClear           SessionEndReason = "clear"
	SessionEndLogout          SessionEndReason = "logout"
	SessionEndPromptInputExit SessionEndReason = "prompt_input_exit"
	SessionEndOther           SessionEndReason = "other"
)

// SessionEndInput is the input for SessionEnd events.
type SessionEndInput struct {
	HookInput
	Reason SessionEndReason `json:"reason"`
}

// BeforeAgentInput is the input for BeforeAgent events.
type BeforeAgentInput struct {
	HookInput
	Prompt string `json:"prompt"`
}

// AfterAgentInput is the input for AfterAgent events.
type AfterAgentInput struct {
	HookInput
	Prompt         string `json:"prompt"`
	PromptResponse string `json:"prompt_response"`
	StopHookActive bool   `json:"stop_hook_active"`
}

// BeforeToolInput is the input for BeforeTool events.
type BeforeToolInput struct {
	HookInput
	ToolName   string          `json:"tool_name"`
	ToolInput  map[string]any  `json:"tool_input"`
	MCPContext *MCPToolContext `json:"mcp_context,omitempty"`
}

// AfterToolInput is the input for AfterTool events.
type AfterToolInput struct {
	HookInput
	ToolName     string          `json:"tool_name"`
	ToolInput    map[string]any  `json:"tool_input"`
	ToolResponse map[string]any  `json:"tool_response"`
	MCPContext   *MCPToolContext `json:"mcp_context,omitempty"`
}

// MCPToolContext provides MCP server details for MCP tool hooks.
type MCPToolContext struct {
	ServerName string   `json:"server_name"`
	ToolName   string   `json:"tool_name"`
	Command    string   `json:"command,omitempty"`
	Args       []string `json:"args,omitempty"`
	Cwd        string   `json:"cwd,omitempty"`
	URL        string   `json:"url,omitempty"`
	TCP        string   `json:"tcp,omitempty"`
}

// BeforeModelInput is the input for BeforeModel events.
type BeforeModelInput struct {
	HookInput
	LLMRequest *LLMRequest `json:"llm_request"`
}

// AfterModelInput is the input for AfterModel events.
type AfterModelInput struct {
	HookInput
	LLMRequest  *LLMRequest  `json:"llm_request"`
	LLMResponse *LLMResponse `json:"llm_response"`
}

// BeforeToolSelectionInput is the input for BeforeToolSelection events.
type BeforeToolSelectionInput struct {
	HookInput
	LLMRequest *LLMRequest `json:"llm_request"`
}

// PreCompressTrigger represents what triggered compression.
type PreCompressTrigger string

const (
	CompressTriggerManual PreCompressTrigger = "manual"
	CompressTriggerAuto   PreCompressTrigger = "auto"
)

// PreCompressInput is the input for PreCompress events.
type PreCompressInput struct {
	HookInput
	Trigger PreCompressTrigger `json:"trigger"`
}

// NotificationType represents the type of notification.
type NotificationType string

const (
	NotificationToolPermission NotificationType = "ToolPermission"
)

// NotificationInput is the input for Notification events.
type NotificationInput struct {
	HookInput
	NotificationType NotificationType `json:"notification_type"`
	Message          string           `json:"message"`
	Details          map[string]any   `json:"details"`
}

// ─────────────────────────────────────────────────────────────────────────────
// Stable LLM Types (Decoupled from SDK)
// ─────────────────────────────────────────────────────────────────────────────

// LLMRequest is the stable format for LLM requests passed to hooks.
type LLMRequest struct {
	Model      string               `json:"model"`
	Messages   []LLMMessage         `json:"messages"`
	Config     *LLMGenerationConfig `json:"config,omitempty"`
	ToolConfig *LLMToolConfig       `json:"toolConfig,omitempty"`
}

// LLMMessage represents a single message in the conversation.
type LLMMessage struct {
	Role    string `json:"role"` // "user", "model", "system"
	Content string `json:"content"`
}

// LLMGenerationConfig contains generation parameters.
type LLMGenerationConfig struct {
	Temperature     *float64 `json:"temperature,omitempty"`
	MaxOutputTokens *int     `json:"maxOutputTokens,omitempty"`
	TopP            *float64 `json:"topP,omitempty"`
	TopK            *int     `json:"topK,omitempty"`
}

// LLMToolConfig controls tool availability.
type LLMToolConfig struct {
	Mode                 string   `json:"mode,omitempty"` // "AUTO", "ANY", "NONE"
	AllowedFunctionNames []string `json:"allowedFunctionNames,omitempty"`
}

// LLMResponse is the stable format for LLM responses from hooks.
type LLMResponse struct {
	Text       string            `json:"text,omitempty"`
	Candidates []LLMCandidate    `json:"candidates"`
	Usage      *LLMUsageMetadata `json:"usageMetadata,omitempty"`
}

// LLMCandidate represents a response candidate.
type LLMCandidate struct {
	Content      LLMContent `json:"content"`
	FinishReason string     `json:"finishReason,omitempty"` // "STOP", "MAX_TOKENS", "SAFETY", etc.
}

// LLMContent represents the content of a response.
type LLMContent struct {
	Role  string   `json:"role"`
	Parts []string `json:"parts"`
}

// LLMUsageMetadata contains token usage information.
type LLMUsageMetadata struct {
	PromptTokenCount     int `json:"promptTokenCount,omitempty"`
	CandidatesTokenCount int `json:"candidatesTokenCount,omitempty"`
	TotalTokenCount      int `json:"totalTokenCount,omitempty"`
}
