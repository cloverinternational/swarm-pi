// Type re-exports for the client facade (T201).
//
// These aliases let a consumer use everything that appears in the client
// package's exported signatures while importing only the client package. Each
// alias preserves type identity (Go type aliases), so a value of client.Tool is
// the very same type as tools.Tool — interface satisfaction, method sets, and
// assignability are unchanged, and code that already names the source package
// interoperates transparently.
//
// Scope rule (kept deliberately tight): a type is re-exported here only if it
// appears in an exported client signature, or is required to consume a type
// that does — specifically the method parameter/return types of the exported
// interfaces client returns (tools.Tool, provider.Provider) and the concrete
// variants of the agent.IntermediateUpdate interface that flows through the
// exported SubscribeUpdates / InjectUpdate callbacks. Types used only inside
// unexported function bodies (e.g. provider.Config) are intentionally NOT
// aliased.
package client

import (
	"github.com/Swarm-Code/mono/swarm-sdk/internal/agent"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/compaction"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/configbundle"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/conversation"
	convmanager "github.com/Swarm-Code/mono/swarm-sdk/internal/conversation/manager"
	convstorage "github.com/Swarm-Code/mono/swarm-sdk/internal/conversation/storage"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/hooks"
	dreambuiltin "github.com/Swarm-Code/mono/swarm-sdk/internal/hooks/builtin"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/mcp"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/mode"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/observability"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/provider"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/skills/autogenskills"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/tools"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/tools/codemode"
)

// --- package tools (github.com/Swarm-Code/mono/swarm-sdk/tools) ---

// Tool is the interface every SDK tool implements. DefaultTools returns
// []Tool, WithTool accepts one, and AgentToolRegistry's contents are Tools.
type Tool = tools.Tool

// ToolResult is the value a Tool.Execute method returns. A client-only
// consumer implementing a custom Tool names this as the Execute return type.
type ToolResult = tools.ToolResult

// Registry is the tool registry returned by AgentToolRegistry and accepted by
// WithToolRegistry.
type Registry = tools.Registry

// Permission is a tool permission token; RequestApprovalInteractive takes a
// []Permission.
type Permission = tools.Permission

// PermissionChecker decides whether a tool invocation is allowed.
// NewInteractiveApprovalChecker returns one.
type PermissionChecker = tools.PermissionChecker

// --- package hooks (github.com/Swarm-Code/mono/swarm-sdk/hooks) ---

// Hook is a lifecycle hook. WithHook and RegisterHook accept one.
type Hook = hooks.Hook

// --- package agent (github.com/Swarm-Code/mono/swarm-sdk/agent) ---

// Definition is an agent's definition, returned by AgentInfo.
type Definition = agent.Definition

// ExecuteRequest is the request passed to Execute / RunPrint / ApplyModeFilter.
type ExecuteRequest = agent.ExecuteRequest

// ExecuteResponse is the full response returned by Execute.
type ExecuteResponse = agent.ExecuteResponse

// AutoCompactionConfig configures automatic compaction; WithCompactionConfig
// accepts one.
type AutoCompactionConfig = agent.AutoCompactionConfig

// IntermediateUpdate is the interface delivered to SubscribeUpdates callbacks
// and accepted by InjectUpdate. The concrete variants below all implement it;
// consumers type-switch on them inside their callback.
//
// (The client package also exposes the equivalent alias Update for the same
// type; both name agent.IntermediateUpdate.)
type IntermediateUpdate = agent.IntermediateUpdate

// The concrete IntermediateUpdate variants. A client-only consumer must be able
// to name these to handle the values delivered to a SubscribeUpdates callback.
type (
	ToolCallUpdate         = agent.ToolCallUpdate
	ToolResultUpdate       = agent.ToolResultUpdate
	ThinkingUpdate         = agent.ThinkingUpdate
	ContentUpdate          = agent.ContentUpdate
	AssistantMessageUpdate = agent.AssistantMessageUpdate
	TokenCountUpdate       = agent.TokenCountUpdate
	TurnUsageUpdate        = agent.TurnUsageUpdate
	CompactionNeededUpdate = agent.CompactionNeededUpdate
	CompactionDoneUpdate   = agent.CompactionDoneUpdate
	ContextPressureError   = agent.ContextPressureError
	HookExecutionUpdate    = agent.HookExecutionUpdate
	ToolOutputChunk        = agent.ToolOutputChunk
	HookOutputChunk        = agent.HookOutputChunk
	SubAgentUpdate         = agent.SubAgentUpdate
	FallbackUpdate         = agent.FallbackUpdate
	ExhaustedUpdate        = agent.ExhaustedUpdate
	HeartbeatUpdate        = agent.HeartbeatUpdate
	SubAgentCompleteUpdate = agent.SubAgentCompleteUpdate
)

// --- package conversation (github.com/Swarm-Code/mono/swarm-sdk/conversation) ---

// Conversation is a stored conversation, returned by LoadConversation,
// NewConversation, ResumeConversation, ListAllConversations, etc.
type Conversation = conversation.Conversation

// Message is a single conversation message; AddMessageToConversation,
// GetConversationMessages and GenerateMessages operate on these.
type Message = conversation.Message

// --- package conversation/manager (.../conversation/manager) ---

// ConversationCreateOptions is the full create-options struct accepted by
// CreateConversationWithOptions. (Named with a Conversation prefix to avoid
// colliding with the client-local CreateOptions type.)
type ConversationCreateOptions = convmanager.CreateOptions

// GetMessagesOptions filters GetConversationMessages results.
type GetMessagesOptions = convmanager.GetMessagesOptions

// ConversationManagerType is the underlying conversation manager returned by
// ConversationManager(). (Named with a Type suffix to avoid colliding with the
// ConversationManager method.)
type ConversationManagerType = convmanager.Manager

// --- package conversation/storage (.../conversation/storage) ---

// Storage is the conversation storage backend returned by Storage().
type Storage = convstorage.Storage

// --- package provider (github.com/Swarm-Code/mono/swarm-sdk/provider) ---

// ProviderInterface is the LLM provider interface returned by NewProvider and
// accepted by WithProviderInstance. (Named ProviderInterface because the client
// package already defines a Provider string-enum type for provider identity.)
type ProviderInterface = provider.Provider

// ChatRequest / ChatResponse / StreamChunk / Capabilities form the method
// param/return surface of ProviderInterface; a consumer holding one from
// NewProvider names them to call Chat, Stream and Capabilities.
type (
	ChatRequest  = provider.ChatRequest
	ChatResponse = provider.ChatResponse
	StreamChunk  = provider.StreamChunk
	Capabilities = provider.Capabilities
)

// --- package mode (github.com/Swarm-Code/mono/swarm-sdk/mode) ---

// OperatingMode is the plan/act/auto operating mode accepted by
// WithOperatingMode and returned by OperatingMode().
type OperatingMode = mode.OperatingMode

// --- package mcp (github.com/Swarm-Code/mono/swarm-sdk/mcp) ---

// RuntimeManager is the MCP server runtime manager returned by MCPManager().
type RuntimeManager = mcp.RuntimeManager

// --- package compaction (github.com/Swarm-Code/mono/swarm-sdk/compaction) ---

// CompactionResult is the structured outcome returned by
// CompactConversationDetail / CompactDetail.
type CompactionResult = compaction.CompactionResult

// --- package configbundle (github.com/Swarm-Code/mono/swarm-sdk/configbundle) ---

// ConfigManager is the config-bundle manager accepted by WithConfigManager and
// SetConfigManager. (Named ConfigManager to disambiguate from the conversation
// Manager.)
type ConfigManager = configbundle.Manager

// --- package observability (github.com/Swarm-Code/mono/swarm-sdk/observability) ---

// Logger is the structured logger accepted by WithLogger and returned by
// Logger().
type Logger = observability.Logger

// Tracer is the tracer accepted by WithTracer and returned by Tracer().
type Tracer = observability.Tracer

// --- package hooks/builtin (.../hooks/builtin) ---

// AutoModeConfig configures the built-in auto-mode hook; WithAutoModeHookConfig
// accepts one.
type AutoModeConfig = dreambuiltin.AutoModeConfig

// RecapConfig configures the built-in recap hook; WithRecapHookConfig accepts
// one.
type RecapConfig = dreambuiltin.RecapConfig

// --- package skills/autogenskills (.../skills/autogenskills) ---

// AutogenSkillsConfig configures autogenerated skills; WithAutogenSkills
// accepts one.
type AutogenSkillsConfig = autogenskills.Config

// --- package tools/codemode (.../tools/codemode) ---

// CodeModeConfig configures code-mode tool execution; WithCodeModeConfig
// accepts one.
type CodeModeConfig = codemode.Config
