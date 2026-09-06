package chat

import (
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/chat/hooks"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/chat/mcptools"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/chat/subagent"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/chat/uitypes"
)

// Type aliases for display types moved to uitypes package.
// These aliases ensure all existing chat code works without modification.
type MessageBlock = uitypes.MessageBlock
type SubAgentDisplay = uitypes.SubAgentDisplay
type ToolCallDisplay = uitypes.ToolCallDisplay
type ToolResultDisplay = uitypes.ToolResultDisplay
type HookExecutionDisplay = uitypes.HookExecutionDisplay
type Attachment = uitypes.Attachment
type SubAgentRenderStyles = uitypes.SubAgentRenderStyles

// Pulse text aliases
type PulseTextConfig = uitypes.PulseTextConfig

// Function aliases for pulse text utilities
var (
	DefaultPulseTextConfig  = uitypes.DefaultPulseTextConfig
	CalculatePulseIntensity = uitypes.CalculatePulseIntensity
	RenderPulseText         = uitypes.RenderPulseText
	RenderPulseTextWithDots = uitypes.RenderPulseTextWithDots
)

// SubAgent renderer aliases
type SubAgentRenderer = subagent.SubAgentRenderer
type SubAgentRenderConfig = subagent.SubAgentRenderConfig
type SubAgentManager = subagent.SubAgentManager
type SubAgentState = subagent.SubAgentState
type SubAgentCollapseLevel = subagent.SubAgentCollapseLevel
type SubAgentWidget = subagent.SubAgentWidget
type SubAgentWidgetConfig = subagent.SubAgentWidgetConfig
type SubAgentBoxConfig = subagent.SubAgentBoxConfig
type SubAgentBoxRenderer = subagent.SubAgentBoxRenderer
type SubAgentStreamRenderer = subagent.SubAgentStreamRenderer
type StreamConfig = subagent.StreamConfig
type StreamState = subagent.StreamState
type SubAgentTable = subagent.SubAgentTable
type SubAgentRow = subagent.SubAgentRow

// SubAgent constants
const (
	SubAgentLevelCollapsed = subagent.SubAgentLevelCollapsed
	SubAgentLevelExpanded  = subagent.SubAgentLevelExpanded
)

// SubAgent constructor aliases
var (
	NewSubAgentRenderer              = subagent.NewSubAgentRenderer
	NewSubAgentManager               = subagent.NewSubAgentManager
	NewSubAgentWidget                = subagent.NewSubAgentWidget
	NewDefaultSubAgentWidget         = subagent.NewDefaultSubAgentWidget
	NewSubAgentBoxRenderer           = subagent.NewSubAgentBoxRenderer
	NewSubAgentStreamRenderer        = subagent.NewSubAgentStreamRenderer
	NewDefaultSubAgentStreamRenderer = subagent.NewDefaultSubAgentStreamRenderer
	DefaultSubAgentRenderConfig      = subagent.DefaultSubAgentRenderConfig
	DefaultSubAgentWidgetConfig      = subagent.DefaultSubAgentWidgetConfig
	DefaultStreamConfig              = subagent.DefaultStreamConfig
	DefaultSubAgentBoxConfig         = subagent.DefaultSubAgentBoxConfig
	NewDefaultSubAgentBoxRenderer    = subagent.NewDefaultSubAgentBoxRenderer
	NewSubAgentTable                 = subagent.NewSubAgentTable
)

// Hooks package aliases
type HooksManager = hooks.HooksManager
type HooksConfig = hooks.HooksConfig
type HookTools = hooks.HookTools
type HooksAssistant = hooks.HooksAssistant
type HooksDashboard = hooks.HooksDashboard
type HooksAgentResult = hooks.HooksAgentResult
type HooksAgentToolCall = hooks.HooksAgentToolCall
type HooksAgentHookExecution = hooks.HooksAgentHookExecution
type HooksAgentResponse = hooks.HooksAgentResponse
type AgentToolCall = hooks.AgentToolCall
type AgentHookExecution = hooks.AgentHookExecution
type ChatMessage = hooks.ChatMessage
type ChatToolCall = hooks.ChatToolCall
type ClaudeCodeHooksConfig = hooks.ClaudeCodeHooksConfig
type ClaudeCodeHookMatcher = hooks.ClaudeCodeHookMatcher
type ClaudeCodeHook = hooks.ClaudeCodeHook
type HookNotFoundError = hooks.HookNotFoundError

// Hooks constructors
var (
	NewHooksManager            = hooks.NewHooksManager
	NewHooksConfig             = hooks.NewHooksConfig
	NewHooksConfigWithProject  = hooks.NewHooksConfigWithProject
	NewHookTools               = hooks.NewHookTools
	NewHooksAssistant          = hooks.NewHooksAssistant
	NewHooksDashboard          = hooks.NewHooksDashboard
	LoadHooksConfig            = hooks.LoadHooksConfig
	LoadHooksConfigWithProject = hooks.LoadHooksConfigWithProject
	CreateShellHooksFromConfig = hooks.CreateShellHooksFromConfig
	SanitizeHookWorkingDir     = hooks.SanitizeHookWorkingDir
)

// MCP package aliases
type MCPManager = mcptools.MCPManager
type MCPAssistant = mcptools.MCPAssistant
type MCPTools = mcptools.MCPTools
type MCPToolNameResolver = mcptools.MCPToolNameResolver

// MCP constructor aliases
var (
	NewMCPManager          = mcptools.NewMCPManager
	NewMCPAssistant        = mcptools.NewMCPAssistant
	NewMCPTools            = mcptools.NewMCPTools
	NewMCPToolNameResolver = mcptools.NewMCPToolNameResolver
)
