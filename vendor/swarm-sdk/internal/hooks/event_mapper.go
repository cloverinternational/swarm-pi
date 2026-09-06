// Package hooks implements the event hook system.
// This file provides unified event mapping between Claude Code and Gemini CLI naming conventions.
package hooks

import (
	"strings"
)

// UnifiedHookEvent represents the canonical event names used internally.
// Users can use either Claude Code or Gemini CLI naming conventions.
type UnifiedHookEvent string

const (
	// Session lifecycle events
	UnifiedSessionStart UnifiedHookEvent = "SessionStart"
	UnifiedSessionEnd   UnifiedHookEvent = "SessionEnd"

	// Agent/Prompt events
	UnifiedBeforeAgent  UnifiedHookEvent = "BeforeAgent"  // Claude: UserPromptSubmit
	UnifiedAfterAgent   UnifiedHookEvent = "AfterAgent"   // Claude: Stop (main agent)
	UnifiedSubagentStop UnifiedHookEvent = "SubagentStop" // Claude only

	// Model interaction events (Gemini only)
	UnifiedBeforeModel         UnifiedHookEvent = "BeforeModel"
	UnifiedAfterModel          UnifiedHookEvent = "AfterModel"
	UnifiedBeforeToolSelection UnifiedHookEvent = "BeforeToolSelection"

	// Tool execution events
	UnifiedBeforeTool UnifiedHookEvent = "BeforeTool" // Claude: PreToolUse
	UnifiedAfterTool  UnifiedHookEvent = "AfterTool"  // Claude: PostToolUse

	// Other events
	UnifiedPreCompress  UnifiedHookEvent = "PreCompress" // Claude: PreCompact
	UnifiedNotification UnifiedHookEvent = "Notification"
)

// EventAliases maps various event name formats to canonical names.
// Supports Claude Code, Gemini CLI, and snake_case naming conventions.
var EventAliases = map[string]UnifiedHookEvent{
	// === Session Events ===
	// Canonical
	"SessionStart": UnifiedSessionStart,
	"SessionEnd":   UnifiedSessionEnd,
	// Snake case
	"session_start": UnifiedSessionStart,
	"session_end":   UnifiedSessionEnd,
	// Dot notation (internal)
	"session.start": UnifiedSessionStart,
	"session.end":   UnifiedSessionEnd,

	// === Agent/Prompt Events ===
	// Gemini CLI naming
	"BeforeAgent": UnifiedBeforeAgent,
	"AfterAgent":  UnifiedAfterAgent,
	// Claude Code naming
	"UserPromptSubmit": UnifiedBeforeAgent,
	"Stop":             UnifiedAfterAgent,
	"SubagentStop":     UnifiedSubagentStop,
	// Snake case
	"before_agent":       UnifiedBeforeAgent,
	"after_agent":        UnifiedAfterAgent,
	"user_prompt_submit": UnifiedBeforeAgent,
	"subagent_stop":      UnifiedSubagentStop,
	// Dot notation (internal)
	"user.prompt_submit": UnifiedBeforeAgent,
	"agent.stop":         UnifiedAfterAgent,

	// === Model Events (Gemini CLI) ===
	"BeforeModel":         UnifiedBeforeModel,
	"AfterModel":          UnifiedAfterModel,
	"BeforeToolSelection": UnifiedBeforeToolSelection,
	// Snake case
	"before_model":          UnifiedBeforeModel,
	"after_model":           UnifiedAfterModel,
	"before_tool_selection": UnifiedBeforeToolSelection,

	// === Tool Events ===
	// Gemini CLI naming
	"BeforeTool": UnifiedBeforeTool,
	"AfterTool":  UnifiedAfterTool,
	// Claude Code naming
	"PreToolUse":  UnifiedBeforeTool,
	"PostToolUse": UnifiedAfterTool,
	// Snake case
	"before_tool":   UnifiedBeforeTool,
	"after_tool":    UnifiedAfterTool,
	"pre_tool_use":  UnifiedBeforeTool,
	"post_tool_use": UnifiedAfterTool,
	// Dot notation (internal)
	"tool.before_execute": UnifiedBeforeTool,
	"tool.after_execute":  UnifiedAfterTool,

	// === Other Events ===
	// Gemini CLI naming
	"PreCompress":  UnifiedPreCompress,
	"Notification": UnifiedNotification,
	// Claude Code naming
	"PreCompact": UnifiedPreCompress,
	// Snake case
	"pre_compress": UnifiedPreCompress,
	"pre_compact":  UnifiedPreCompress,
	"notification": UnifiedNotification,
	// Dot notation (internal)
	"compact.before": UnifiedPreCompress,
}

// NormalizeEventName converts any event name format to the canonical unified format.
// Supports Claude Code, Gemini CLI, snake_case, and internal dot notation.
func NormalizeEventName(eventName string) UnifiedHookEvent {
	// Try direct lookup (case-sensitive)
	if canonical, ok := EventAliases[eventName]; ok {
		return canonical
	}

	// Try case-insensitive lookup
	lower := strings.ToLower(eventName)
	for alias, canonical := range EventAliases {
		if strings.ToLower(alias) == lower {
			return canonical
		}
	}

	// Return as-is if not found (custom event)
	return UnifiedHookEvent(eventName)
}

// ToHookEventName converts a unified event to HookEventName.
func (u UnifiedHookEvent) ToHookEventName() HookEventName {
	return HookEventName(u)
}

// ToClaudeCodeName returns the Claude Code naming convention for this event.
func (u UnifiedHookEvent) ToClaudeCodeName() string {
	switch u {
	case UnifiedSessionStart:
		return "SessionStart"
	case UnifiedSessionEnd:
		return "SessionEnd"
	case UnifiedBeforeAgent:
		return "UserPromptSubmit"
	case UnifiedAfterAgent:
		return "Stop"
	case UnifiedSubagentStop:
		return "SubagentStop"
	case UnifiedBeforeTool:
		return "PreToolUse"
	case UnifiedAfterTool:
		return "PostToolUse"
	case UnifiedPreCompress:
		return "PreCompact"
	case UnifiedNotification:
		return "Notification"
	// Gemini-only events (no Claude equivalent)
	case UnifiedBeforeModel, UnifiedAfterModel, UnifiedBeforeToolSelection:
		return string(u) // Use Gemini name
	default:
		return string(u)
	}
}

// ToGeminiCLIName returns the Gemini CLI naming convention for this event.
func (u UnifiedHookEvent) ToGeminiCLIName() string {
	switch u {
	case UnifiedSessionStart:
		return "SessionStart"
	case UnifiedSessionEnd:
		return "SessionEnd"
	case UnifiedBeforeAgent:
		return "BeforeAgent"
	case UnifiedAfterAgent:
		return "AfterAgent"
	case UnifiedSubagentStop:
		return "AfterAgent" // Map to AfterAgent (closest equivalent)
	case UnifiedBeforeTool:
		return "BeforeTool"
	case UnifiedAfterTool:
		return "AfterTool"
	case UnifiedBeforeModel:
		return "BeforeModel"
	case UnifiedAfterModel:
		return "AfterModel"
	case UnifiedBeforeToolSelection:
		return "BeforeToolSelection"
	case UnifiedPreCompress:
		return "PreCompress"
	case UnifiedNotification:
		return "Notification"
	default:
		return string(u)
	}
}

// ToInternalName returns the internal dot notation for this event.
func (u UnifiedHookEvent) ToInternalName() string {
	switch u {
	case UnifiedSessionStart:
		return "session.start"
	case UnifiedSessionEnd:
		return "session.end"
	case UnifiedBeforeAgent:
		return "user.prompt_submit"
	case UnifiedAfterAgent:
		return "agent.stop"
	case UnifiedSubagentStop:
		return "subagent.stop"
	case UnifiedBeforeTool:
		return "tool.before_execute"
	case UnifiedAfterTool:
		return "tool.after_execute"
	case UnifiedBeforeModel:
		return "model.before_request"
	case UnifiedAfterModel:
		return "model.after_response"
	case UnifiedBeforeToolSelection:
		return "model.before_tool_selection"
	case UnifiedPreCompress:
		return "compact.before"
	case UnifiedNotification:
		return "notification"
	default:
		return string(u)
	}
}

// EventMapper provides bidirectional event name mapping.
type EventMapper struct{}

// NewEventMapper creates a new event mapper.
func NewEventMapper() *EventMapper {
	return &EventMapper{}
}

// Normalize converts any event name to canonical format.
func (m *EventMapper) Normalize(eventName string) UnifiedHookEvent {
	return NormalizeEventName(eventName)
}

// ToClaudeCode converts event name to Claude Code format.
func (m *EventMapper) ToClaudeCode(eventName string) string {
	return NormalizeEventName(eventName).ToClaudeCodeName()
}

// ToGeminiCLI converts event name to Gemini CLI format.
func (m *EventMapper) ToGeminiCLI(eventName string) string {
	return NormalizeEventName(eventName).ToGeminiCLIName()
}

// ToInternal converts event name to internal dot notation.
func (m *EventMapper) ToInternal(eventName string) string {
	return NormalizeEventName(eventName).ToInternalName()
}

// GetAllAliases returns all known aliases for an event.
func (m *EventMapper) GetAllAliases(eventName string) []string {
	canonical := NormalizeEventName(eventName)
	var aliases []string

	for alias, event := range EventAliases {
		if event == canonical {
			aliases = append(aliases, alias)
		}
	}

	return aliases
}

// IsValidEvent checks if an event name is recognized.
func (m *EventMapper) IsValidEvent(eventName string) bool {
	_, ok := EventAliases[eventName]
	if ok {
		return true
	}

	// Try case-insensitive
	lower := strings.ToLower(eventName)
	for alias := range EventAliases {
		if strings.ToLower(alias) == lower {
			return true
		}
	}

	return false
}

// EventCompatibility describes which systems support an event.
type EventCompatibility struct {
	Event          UnifiedHookEvent
	ClaudeCodeName string
	GeminiCLIName  string
	SupportedBy    []string // "claude", "gemini", "both"
	Description    string
}

// GetEventCompatibilityTable returns compatibility info for all events.
func GetEventCompatibilityTable() []EventCompatibility {
	return []EventCompatibility{
		{
			Event:          UnifiedSessionStart,
			ClaudeCodeName: "SessionStart",
			GeminiCLIName:  "SessionStart",
			SupportedBy:    []string{"claude", "gemini"},
			Description:    "Fires when a session starts",
		},
		{
			Event:          UnifiedSessionEnd,
			ClaudeCodeName: "SessionEnd",
			GeminiCLIName:  "SessionEnd",
			SupportedBy:    []string{"claude", "gemini"},
			Description:    "Fires when a session ends",
		},
		{
			Event:          UnifiedBeforeAgent,
			ClaudeCodeName: "UserPromptSubmit",
			GeminiCLIName:  "BeforeAgent",
			SupportedBy:    []string{"claude", "gemini"},
			Description:    "Fires before processing user prompt",
		},
		{
			Event:          UnifiedAfterAgent,
			ClaudeCodeName: "Stop",
			GeminiCLIName:  "AfterAgent",
			SupportedBy:    []string{"claude", "gemini"},
			Description:    "Fires when agent completes/stops",
		},
		{
			Event:          UnifiedSubagentStop,
			ClaudeCodeName: "SubagentStop",
			GeminiCLIName:  "AfterAgent",
			SupportedBy:    []string{"claude"},
			Description:    "Fires when a subagent stops (Claude only)",
		},
		{
			Event:          UnifiedBeforeTool,
			ClaudeCodeName: "PreToolUse",
			GeminiCLIName:  "BeforeTool",
			SupportedBy:    []string{"claude", "gemini"},
			Description:    "Fires before tool execution",
		},
		{
			Event:          UnifiedAfterTool,
			ClaudeCodeName: "PostToolUse",
			GeminiCLIName:  "AfterTool",
			SupportedBy:    []string{"claude", "gemini"},
			Description:    "Fires after tool execution",
		},
		{
			Event:          UnifiedBeforeModel,
			ClaudeCodeName: "",
			GeminiCLIName:  "BeforeModel",
			SupportedBy:    []string{"gemini"},
			Description:    "Fires before LLM request (Gemini only)",
		},
		{
			Event:          UnifiedAfterModel,
			ClaudeCodeName: "",
			GeminiCLIName:  "AfterModel",
			SupportedBy:    []string{"gemini"},
			Description:    "Fires after LLM response (Gemini only)",
		},
		{
			Event:          UnifiedBeforeToolSelection,
			ClaudeCodeName: "",
			GeminiCLIName:  "BeforeToolSelection",
			SupportedBy:    []string{"gemini"},
			Description:    "Fires before tool selection (Gemini only)",
		},
		{
			Event:          UnifiedPreCompress,
			ClaudeCodeName: "PreCompact",
			GeminiCLIName:  "PreCompress",
			SupportedBy:    []string{"claude", "gemini"},
			Description:    "Fires before context compaction",
		},
		{
			Event:          UnifiedNotification,
			ClaudeCodeName: "Notification",
			GeminiCLIName:  "Notification",
			SupportedBy:    []string{"claude", "gemini"},
			Description:    "Fires for notifications/logging",
		},
	}
}

// GetClaudeCodeEvents returns all Claude Code compatible events.
func GetClaudeCodeEvents() []UnifiedHookEvent {
	return []UnifiedHookEvent{
		UnifiedSessionStart,
		UnifiedSessionEnd,
		UnifiedBeforeAgent, // UserPromptSubmit
		UnifiedAfterAgent,  // Stop
		UnifiedSubagentStop,
		UnifiedBeforeTool,  // PreToolUse
		UnifiedAfterTool,   // PostToolUse
		UnifiedPreCompress, // PreCompact
		UnifiedNotification,
	}
}

// GetGeminiCLIEvents returns all Gemini CLI compatible events.
func GetGeminiCLIEvents() []UnifiedHookEvent {
	return []UnifiedHookEvent{
		UnifiedSessionStart,
		UnifiedSessionEnd,
		UnifiedBeforeAgent,
		UnifiedAfterAgent,
		UnifiedBeforeModel,
		UnifiedAfterModel,
		UnifiedBeforeToolSelection,
		UnifiedBeforeTool,
		UnifiedAfterTool,
		UnifiedPreCompress,
		UnifiedNotification,
	}
}

// GetAllEvents returns all supported events.
func GetAllEvents() []UnifiedHookEvent {
	return []UnifiedHookEvent{
		UnifiedSessionStart,
		UnifiedSessionEnd,
		UnifiedBeforeAgent,
		UnifiedAfterAgent,
		UnifiedSubagentStop,
		UnifiedBeforeModel,
		UnifiedAfterModel,
		UnifiedBeforeToolSelection,
		UnifiedBeforeTool,
		UnifiedAfterTool,
		UnifiedPreCompress,
		UnifiedNotification,
	}
}
