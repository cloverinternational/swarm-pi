// Package lifecycle implements Claude Code-style lifecycle hooks.
// These hooks intercept tool executions, session events, and agent stops.
package lifecycle

import (
	"regexp"
	"time"
)

// HookEvent represents the lifecycle event types.
type HookEvent string

const (
	// EventPreToolUse fires before a tool is executed.
	EventPreToolUse HookEvent = "PreToolUse"

	// EventPostToolUse fires after a tool completes.
	EventPostToolUse HookEvent = "PostToolUse"

	// EventStop fires when the agent considers stopping.
	EventStop HookEvent = "Stop"

	// EventSessionStart fires when a session begins.
	EventSessionStart HookEvent = "SessionStart"

	// EventNotification fires for user notifications.
	EventNotification HookEvent = "Notification"

	// EventPreMessage fires before sending a message.
	EventPreMessage HookEvent = "PreMessage"

	// EventPostMessage fires after receiving a response.
	EventPostMessage HookEvent = "PostMessage"
)

// HookType defines the execution type for a hook.
type HookType string

const (
	// HookTypeCommand executes a shell command.
	HookTypeCommand HookType = "command"

	// HookTypePrompt uses LLM evaluation.
	HookTypePrompt HookType = "prompt"
)

// HookConfig defines a single hook configuration.
type HookConfig struct {
	// Type is "command" or "prompt".
	Type HookType `json:"type" yaml:"type"`

	// Command is the shell command to execute (for type=command).
	Command string `json:"command,omitempty" yaml:"command,omitempty"`

	// Prompt is the LLM prompt for evaluation (for type=prompt).
	Prompt string `json:"prompt,omitempty" yaml:"prompt,omitempty"`

	// Timeout in seconds for execution.
	Timeout int `json:"timeout,omitempty" yaml:"timeout,omitempty"`

	// Environment variables to set.
	Env map[string]string `json:"env,omitempty" yaml:"env,omitempty"`
}

// HookMatcher defines a matcher with associated hooks.
type HookMatcher struct {
	// Matcher is a regex pattern or "*" for all.
	Matcher string `json:"matcher" yaml:"matcher"`

	// Hooks to execute when matcher matches.
	Hooks []HookConfig `json:"hooks" yaml:"hooks"`

	// compiled regex (internal)
	compiled *regexp.Regexp
}

// Compile compiles the matcher pattern into a regex.
func (h *HookMatcher) Compile() error {
	if h.Matcher == "*" {
		h.compiled = regexp.MustCompile(".*")
		return nil
	}
	re, err := regexp.Compile(h.Matcher)
	if err != nil {
		return err
	}
	h.compiled = re
	return nil
}

// Matches checks if the input matches this matcher.
func (h *HookMatcher) Matches(input string) bool {
	if h.compiled == nil {
		return h.Matcher == "*" || h.Matcher == input
	}
	return h.compiled.MatchString(input)
}

// LifecycleHooksConfig is the complete hooks configuration.
type LifecycleHooksConfig struct {
	PreToolUse   []HookMatcher `json:"PreToolUse,omitempty" yaml:"PreToolUse,omitempty"`
	PostToolUse  []HookMatcher `json:"PostToolUse,omitempty" yaml:"PostToolUse,omitempty"`
	Stop         []HookMatcher `json:"Stop,omitempty" yaml:"Stop,omitempty"`
	SessionStart []HookMatcher `json:"SessionStart,omitempty" yaml:"SessionStart,omitempty"`
	Notification []HookMatcher `json:"Notification,omitempty" yaml:"Notification,omitempty"`
	PreMessage   []HookMatcher `json:"PreMessage,omitempty" yaml:"PreMessage,omitempty"`
	PostMessage  []HookMatcher `json:"PostMessage,omitempty" yaml:"PostMessage,omitempty"`
}

// HookContext contains runtime context for hook execution.
type HookContext struct {
	// Event type being processed.
	Event HookEvent

	// ToolName for tool-related events.
	ToolName string

	// ToolInput for PreToolUse.
	ToolInput map[string]any

	// ToolOutput for PostToolUse.
	ToolOutput any

	// Message content for message events.
	Message string

	// SessionID for the current session.
	SessionID string

	// ConversationID for the current conversation.
	ConversationID string

	// WorkingDir for command execution.
	WorkingDir string

	// PluginRoot path for ${CLAUDE_PLUGIN_ROOT} substitution.
	PluginRoot string

	// Timestamp of the event.
	Timestamp time.Time

	// Metadata for additional context.
	Metadata map[string]any
}

// HookDecision represents the outcome of a hook evaluation.
type HookDecision struct {
	// Decision is "allow", "deny", "ask", "approve", "block".
	Decision string `json:"decision,omitempty"`

	// PermissionDecision for PreToolUse hooks.
	PermissionDecision string `json:"permissionDecision,omitempty"`

	// UpdatedInput contains modified tool inputs.
	UpdatedInput map[string]any `json:"updatedInput,omitempty"`

	// Reason explains the decision.
	Reason string `json:"reason,omitempty"`

	// SystemMessage is feedback for the agent.
	SystemMessage string `json:"systemMessage,omitempty"`

	// Output is the raw command output.
	Output string `json:"output,omitempty"`

	// ExitCode for command hooks.
	ExitCode int `json:"exitCode,omitempty"`

	// Error if execution failed.
	Error error `json:"error,omitempty"`

	// Duration of execution.
	Duration time.Duration `json:"duration,omitempty"`
}

// IsAllowed returns true if the decision allows proceeding.
func (d *HookDecision) IsAllowed() bool {
	// First check for explicit blocks
	if d.IsBlocked() || d.NeedsConfirmation() {
		return false
	}
	// Allow if explicitly allowed or empty (default)
	switch d.Decision {
	case "allow", "approve", "":
		switch d.PermissionDecision {
		case "allow", "approve", "":
			return true
		}
	}
	return false
}

// IsBlocked returns true if the decision blocks proceeding.
func (d *HookDecision) IsBlocked() bool {
	return d.Decision == "deny" || d.Decision == "block" ||
		d.PermissionDecision == "deny" || d.PermissionDecision == "block"
}

// NeedsConfirmation returns true if user confirmation is needed.
func (d *HookDecision) NeedsConfirmation() bool {
	return d.Decision == "ask" || d.PermissionDecision == "ask"
}
