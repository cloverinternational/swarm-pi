package acp

import (
	"encoding/json"
	"fmt"
	"strconv"
)

// ============================================================
// JSON-RPC 2.0 base types
// ============================================================

// Request is an incoming JSON-RPC 2.0 request from the editor (client).
type Request struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"` // int or string; omitted for notifications
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

// IsNotification returns true when the message has no ID (i.e. is a notification).
func (r *Request) IsNotification() bool {
	return len(r.ID) == 0 || string(r.ID) == "null"
}

// Response is an outgoing JSON-RPC 2.0 response to the editor.
type Response struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   *RPCError       `json:"error,omitempty"`
}

// Notification is an outgoing JSON-RPC 2.0 notification (no ID, no response expected).
type Notification struct {
	JSONRPC string `json:"jsonrpc"`
	Method  string `json:"method"`
	Params  any    `json:"params,omitempty"`
}

// OutboundRequest is a JSON-RPC 2.0 request sent *from* the agent *to* the editor.
// Used for session/request_permission where the agent initiates the exchange.
type OutboundRequest struct {
	JSONRPC string `json:"jsonrpc"`
	ID      int    `json:"id"`
	Method  string `json:"method"`
	Params  any    `json:"params,omitempty"`
}

// RPCError represents a JSON-RPC 2.0 error object.
type RPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

// Error implements the error interface.
func (e *RPCError) Error() string { return e.Message }

// Standard JSON-RPC error codes.
const (
	ErrParseError     = -32700
	ErrInvalidRequest = -32600
	ErrMethodNotFound = -32601
	ErrInvalidParams  = -32602
	ErrInternalError  = -32603
)

// ============================================================
// session/new
// ============================================================

// SessionNewParams are the parameters for the session/new request.
type SessionNewParams struct {
	// CWD is the working directory for the session.
	CWD string `json:"cwd"`
	// MCPServers lists additional MCP servers to connect.
	MCPServers []MCPServerSpec `json:"mcpServers,omitempty"`
}

// MCPServerSpec describes an MCP server to connect for a session.
type MCPServerSpec struct {
	// Name is the human-readable server name.
	Name string `json:"name"`
	// Type is either "stdio" (default) or "http".
	Type string `json:"type,omitempty"`
	// Command is the executable for stdio servers.
	Command string `json:"command,omitempty"`
	// Args are the arguments for stdio servers.
	Args []string `json:"args,omitempty"`
	// URL is the endpoint for HTTP servers.
	URL string `json:"url,omitempty"`
	// Env lists environment variables to set for stdio servers.
	Env []MCPEnvVar `json:"env,omitempty"`
	// Headers are HTTP headers for HTTP servers.
	Headers []MCPHTTPHeader `json:"headers,omitempty"`
}

// MCPEnvVar is a name/value environment variable pair.
type MCPEnvVar struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

// MCPHTTPHeader is an HTTP header name/value pair.
type MCPHTTPHeader struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

// SessionNewResult is the response to session/new.
type SessionNewResult struct {
	// SessionID is the unique identifier for this session.
	SessionID string `json:"sessionId"`
	// Modes describes available operating modes and the current one.
	Modes SessionModes `json:"modes"`
	// ConfigOptions lists the user-configurable options for this session.
	ConfigOptions []ConfigOption `json:"configOptions,omitempty"`
}

// SessionModes describes the available modes for a session.
type SessionModes struct {
	CurrentModeID  string     `json:"currentModeId"`
	AvailableModes []ModeInfo `json:"availableModes"`
}

// ModeInfo describes a single operating mode.
type ModeInfo struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
}

// availableModes is the canonical list of modes advertised to editors.
var availableModes = []ModeInfo{
	{ID: "ask", Name: "Ask", Description: "Request permission before making changes"},
	{ID: "code", Name: "Code", Description: "Autonomous coding with full tool access"},
	{ID: "architect", Name: "Architect", Description: "Design and plan without implementation"},
}

// ============================================================
// session/prompt
// ============================================================

// SessionPromptParams are the parameters for the session/prompt request.
type SessionPromptParams struct {
	// SessionID identifies the active session.
	SessionID string `json:"sessionId"`
	// Prompt is the current ACP field containing the user message blocks.
	Prompt []PromptContent `json:"prompt,omitempty"`
	// Content is the legacy field emitted by older ACP clients.
	Content []PromptContent `json:"content,omitempty"`
}

// promptBlocks returns the standards-current prompt when present and otherwise
// falls back to the legacy content field.
func (p SessionPromptParams) promptBlocks() []PromptContent {
	if len(p.Prompt) > 0 {
		return p.Prompt
	}
	return p.Content
}

// PromptContent is a single item in a prompt (text or resource).
type PromptContent struct {
	// Type is "text" or "resource".
	Type string `json:"type"`
	// Text holds the text content when Type == "text".
	Text string `json:"text,omitempty"`
	// Resource holds file/resource content when Type == "resource".
	Resource *PromptResource `json:"resource,omitempty"`
}

// PromptResource is an embedded file resource in a prompt.
type PromptResource struct {
	URI      string `json:"uri"`
	MimeType string `json:"mimeType,omitempty"`
	Text     string `json:"text,omitempty"`
}

// SessionPromptResult is the response to session/prompt, sent when the turn ends.
type SessionPromptResult struct {
	// StopReason indicates why the agent stopped generating.
	// Values: "end_turn", "max_tokens", "cancelled", "max_turn_requests", "refusal"
	StopReason string `json:"stopReason"`
}

// ============================================================
// session/cancel
// ============================================================

// SessionCancelParams are the parameters for the session/cancel notification.
type SessionCancelParams struct {
	SessionID string `json:"sessionId"`
}

// ============================================================
// session/update (notification: agent → editor)
// ============================================================

// SessionUpdateParams wraps a session/update notification.
type SessionUpdateParams struct {
	SessionID string        `json:"sessionId"`
	Update    SessionUpdate `json:"update"`
}

// SessionUpdate is the polymorphic update payload.
// The SessionUpdateType field discriminates the concrete shape.
// The Content field holds different JSON shapes depending on the type:
//   - "agent_message_chunk": MessageContent object  {"type":"text","text":"..."}
//   - "tool_call_update":    []ToolCallItem array
//   - "plan":                absent (use Entries)
//   - "tool_call":           absent
//   - "current_mode_update": absent (use CurrentModeID)
//   - "config_option_update": absent (use ConfigOptions)
//
// AvailableCommand describes a slash command the agent exposes.
type AvailableCommand struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
}

type SessionUpdate struct {
	// SessionUpdateType discriminates the update kind.
	// Values: "agent_message_chunk", "plan", "tool_call", "tool_call_update", "current_mode_update", "config_option_update"
	SessionUpdateType string `json:"sessionUpdate"`

	// Content holds the update payload. Its JSON shape depends on SessionUpdateType:
	//   agent_message_chunk → MessageContent object
	//   tool_call_update    → []ToolCallItem array
	Content json.RawMessage `json:"content,omitempty"`
	// ContentType optionally annotates agent_message_chunk content (e.g. "thinking").
	ContentType string `json:"contentType,omitempty"`

	// ── plan ─────────────────────────────────────────────
	Entries []PlanEntry `json:"entries,omitempty"`

	// ── tool_call / tool_call_update ──────────────────────
	ToolCallID string         `json:"toolCallId,omitempty"`
	Title      string         `json:"title,omitempty"`
	Kind       string         `json:"kind,omitempty"`   // "read", "write", "execute", "delete"
	Status     string         `json:"status,omitempty"` // ACP v1: "pending", "in_progress", "completed", "failed"
	Locations  []FileLocation `json:"locations,omitempty"`
	// RawInput carries the tool invocation arguments for debugging/rendering.
	RawInput json.RawMessage `json:"rawInput,omitempty"`

	// ── current_mode_update ───────────────────────────────
	CurrentModeID string `json:"currentModeId,omitempty"`

	// ── config_option_update ──────────────────────────────
	ConfigOptions []ConfigOption `json:"configOptions,omitempty"`

	// ── availableCommands_update ─────────────────────────
	AvailableCommands []AvailableCommand `json:"availableCommands,omitempty"`

	// ── usage_update ─────────────────────────────────────
	Used int            `json:"used,omitempty"`
	Size int            `json:"size,omitempty"`
	Cost map[string]any `json:"cost,omitempty"`
}

// MessageContent is the content of an agent_message_chunk update.
type MessageContent struct {
	Type string `json:"type"` // always "text"
	Text string `json:"text"`
}

// PlanEntry is a single step in an agent execution plan.
type PlanEntry struct {
	Content  string `json:"content"`
	Priority string `json:"priority"` // "high", "medium", "low"
	Status   string `json:"status"`   // "completed", "in_progress", "pending"
}

// FileLocation describes a file affected by a tool call.
type FileLocation struct {
	Path string `json:"path"`
}

// ToolCallItem is an item in tool_call_update content array.
type ToolCallItem struct {
	Type    string          `json:"type"` // "content"
	Content *MessageContent `json:"content,omitempty"`
}

// ============================================================
// session/request_permission (outbound request: agent → editor)
// ============================================================

// SessionRequestPermissionParams are the parameters for session/request_permission.
type SessionRequestPermissionParams struct {
	SessionID string             `json:"sessionId"`
	ToolCall  PermissionToolCall `json:"toolCall"`
	Options   []PermissionOption `json:"options"`
}

// PermissionToolCall describes the tool call that needs approval.
type PermissionToolCall struct {
	ToolCallID string `json:"toolCallId"`
	Title      string `json:"title,omitempty"`
	Kind       string `json:"kind,omitempty"`   // e.g. "write", "execute", "delete"
	Status     string `json:"status,omitempty"` // "pending"
}

// PermissionOption is one selectable outcome for the user.
type PermissionOption struct {
	OptionID string `json:"optionId"`
	Name     string `json:"name"`
	// Kind hints at the appropriate UI treatment.
	// Values: "allow_once", "allow_always", "reject_once", "reject_always"
	Kind string `json:"kind"`
}

// SessionRequestPermissionResult is the editor's response to session/request_permission.
type SessionRequestPermissionResult struct {
	Outcome PermissionOutcome `json:"outcome"`
}

// PermissionOutcome describes the user's decision.
type PermissionOutcome struct {
	// Outcome is "selected" or "cancelled".
	Outcome  string `json:"outcome"`
	OptionID string `json:"optionId,omitempty"`
}

// defaultPermissionOptions returns the standard allow/reject option set.
func defaultPermissionOptions() []PermissionOption {
	return []PermissionOption{
		{OptionID: "allow-once", Name: "Allow once", Kind: "allow_once"},
		{OptionID: "allow-always", Name: "Always allow", Kind: "allow_always"},
		{OptionID: "reject", Name: "Reject", Kind: "reject_once"},
	}
}

// ============================================================
// initialize / initialized
// ============================================================

// ProtocolVersion is an ACP protocol version identifier. ACP v1 defines this
// value as a JSON number. UnmarshalJSON also accepts the legacy string form
// used by older Swarm clients so upgrades do not break existing integrations.
type ProtocolVersion int

// UnmarshalJSON accepts the current numeric ACP representation and the legacy
// quoted representation. Marshaling remains numeric because the underlying
// type is int and ProtocolVersion does not implement json.Marshaler.
func (v *ProtocolVersion) UnmarshalJSON(data []byte) error {
	var numeric int
	if err := json.Unmarshal(data, &numeric); err == nil {
		*v = ProtocolVersion(numeric)
		return nil
	}

	var legacy string
	if err := json.Unmarshal(data, &legacy); err != nil {
		return fmt.Errorf("protocol version must be a number: %w", err)
	}
	numeric, err := strconv.Atoi(legacy)
	if err != nil {
		return fmt.Errorf("invalid protocol version %q: %w", legacy, err)
	}
	*v = ProtocolVersion(numeric)
	return nil
}

// InitializeParams are the parameters for the initialize request.
type InitializeParams struct {
	// ProtocolVersion is the ACP protocol version the client supports.
	ProtocolVersion ProtocolVersion `json:"protocolVersion"`
	// ClientCapabilities describes what the client can do.
	ClientCapabilities ClientCapabilities `json:"clientCapabilities"`
	// ClientInfo identifies the connecting editor.
	ClientInfo *ImplementationInfo `json:"clientInfo,omitempty"`
}

// ClientCapabilities describes client-side ACP capabilities.
type ClientCapabilities struct {
	FS       FSCapability `json:"fs"`
	Terminal bool         `json:"terminal"`
}

// FSCapability describes file-system access the client can provide to the agent.
type FSCapability struct {
	ReadTextFile  bool `json:"readTextFile"`
	WriteTextFile bool `json:"writeTextFile"`
}

// ImplementationInfo identifies a software component (agent or client).
type ImplementationInfo struct {
	Name    string `json:"name"`
	Title   string `json:"title,omitempty"`
	Version string `json:"version,omitempty"`
}

// InitializeResult is the response to initialize.
type InitializeResult struct {
	ProtocolVersion   ProtocolVersion    `json:"protocolVersion"`
	AgentCapabilities AgentCapabilities  `json:"agentCapabilities"`
	AgentInfo         ImplementationInfo `json:"agentInfo"`
	AuthMethods       []AuthMethod       `json:"authMethods,omitempty"`
}

// AgentCapabilities advertises what the Swarm ACP server can do.
type AgentCapabilities struct {
	// LoadSession is true when session/load is supported.
	LoadSession bool `json:"loadSession"`
	// PromptCapabilities describes supported prompt content types.
	PromptCapabilities PromptCapabilities `json:"promptCapabilities"`
	// MCPCapabilities describes supported MCP transports.
	MCPCapabilities MCPCapabilities `json:"mcpCapabilities"`
	// SlashCommands is true when slash_command/list and slash_command/run are supported.
	SlashCommands bool `json:"slashCommands"`
	// SetConfig is true when session/set_config is supported.
	SetConfig bool `json:"setConfig"`
}

// PromptCapabilities describes which prompt content types the agent accepts.
type PromptCapabilities struct {
	Image           bool `json:"image"`
	Audio           bool `json:"audio"`
	EmbeddedContext bool `json:"embeddedContext"`
}

// MCPCapabilities describes which MCP transports the agent supports.
type MCPCapabilities struct {
	HTTP bool `json:"http"`
	SSE  bool `json:"sse"`
}

// AuthMethod describes an available authentication method.
type AuthMethod struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
}

// agentCapabilities is the static capability set advertised to editors.
var agentCapabilities = AgentCapabilities{
	LoadSession: true,
	PromptCapabilities: PromptCapabilities{
		Image:           false,
		Audio:           false,
		EmbeddedContext: true,
	},
	MCPCapabilities: MCPCapabilities{
		HTTP: false,
		SSE:  false,
	},
	SlashCommands: true,
	SetConfig:     true,
}

// agentInfo is the static agent identity advertised to editors.
var agentInfo = ImplementationInfo{
	Name:    "swarm",
	Title:   "Swarm AI Agent",
	Version: "1.0.0",
}

// ============================================================
// session/set_mode
// ============================================================

// SessionSetModeParams are the parameters for session/set_mode.
type SessionSetModeParams struct {
	SessionID string `json:"sessionId"`
	// ModeID must be one of the IDs in availableModes.
	ModeID string `json:"modeId"`
}

// SessionSetModeResult is the response to session/set_mode.
type SessionSetModeResult struct {
	CurrentModeID string `json:"currentModeId"`
}

// CurrentModeUpdate is the session/update payload for mode changes.
type CurrentModeUpdate struct {
	SessionUpdateType string `json:"sessionUpdate"` // always "current_mode_update"
	CurrentModeID     string `json:"currentModeId"`
}

// ============================================================
// session/load
// ============================================================

// SessionLoadParams are the parameters for session/load.
type SessionLoadParams struct {
	SessionID  string          `json:"sessionId"`
	CWD        string          `json:"cwd"`
	MCPServers []MCPServerSpec `json:"mcpServers,omitempty"`
}

// SessionLoadResult is the response to session/load.
type SessionLoadResult struct {
	SessionID     string         `json:"sessionId"`
	Modes         SessionModes   `json:"modes"`
	ConfigOptions []ConfigOption `json:"configOptions,omitempty"`
}

// ============================================================
// Config options (advertised in session/new and session/load)
// ============================================================

// ConfigOption describes a user-configurable session option.
type ConfigOption struct {
	ID           string              `json:"id"`
	Label        string              `json:"label"`
	Description  string              `json:"description,omitempty"`
	Category     string              `json:"category,omitempty"`
	Type         string              `json:"type"` // always "select"
	CurrentValue string              `json:"currentValue"`
	Options      []ConfigOptionValue `json:"options"`
}

// ConfigOptionValue is one selectable value within a ConfigOption.
type ConfigOptionValue struct {
	Value       string
	Label       string
	Description string
}

// MarshalJSON emits the current ACP `name` field and the legacy `label` field.
// Keeping both lets standards-current hosts such as Paseo validate the option
// while preserving compatibility with older clients that consumed `label`.
func (o ConfigOptionValue) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		Value       string `json:"value"`
		Name        string `json:"name"`
		Label       string `json:"label,omitempty"`
		Description string `json:"description,omitempty"`
	}{
		Value:       o.Value,
		Name:        o.Label,
		Label:       o.Label,
		Description: o.Description,
	})
}

// modeConfigOption builds the "mode" config option for the current session mode.
func modeConfigOption(currentModeID string) ConfigOption {
	opts := make([]ConfigOptionValue, len(availableModes))
	for i, m := range availableModes {
		opts[i] = ConfigOptionValue{Value: m.ID, Label: m.Name, Description: m.Description}
	}
	return ConfigOption{
		ID:           "mode",
		Label:        "Mode",
		Description:  "Controls how the agent requests permission before making changes",
		Category:     "mode",
		Type:         "select",
		CurrentValue: currentModeID,
		Options:      opts,
	}
}

// providerConfigOption builds the "provider" config option.
func providerConfigOption(currentProvider string, providers []string) ConfigOption {
	opts := make([]ConfigOptionValue, len(providers))
	for i, p := range providers {
		opts[i] = ConfigOptionValue{Value: p, Label: p}
	}
	return ConfigOption{
		ID:           "provider",
		Label:        "Provider",
		Description:  "AI provider to use for this session",
		Category:     "_provider",
		Type:         "select",
		CurrentValue: currentProvider,
		Options:      opts,
	}
}

// modelConfigOption builds the "model" config option.
// It includes a curated list of common models; the caller can prepend the current
// model if it isn't already in the list.
func modelConfigOption(currentModel string, providerModels []ModelOptionValue) ConfigOption {
	// Start from a curated fallback list; provider-specific models take precedence.
	defaults := []ModelOptionValue{
		{ID: "claude-opus-4-7", Name: "Claude Opus 4.7"},
		{ID: "claude-opus-4-6", Name: "Claude Opus 4.6"},
		{ID: "claude-sonnet-4-6", Name: "Claude Sonnet 4.6"},
		{ID: "claude-sonnet-4-5-20250929", Name: "Claude Sonnet 4.5"},
		{ID: "claude-haiku-4-5-20251001", Name: "Claude Haiku 4.5"},
		{ID: "claude-opus-4-5-20251101", Name: "Claude Opus 4.5"},
		{ID: "gpt-5.6-terra", Name: "GPT-5.6 Terra"},
		{ID: "gpt-5.6-sol", Name: "GPT-5.6 Sol"},
		{ID: "gpt-5.6-luna", Name: "GPT-5.6 Luna"},
		{ID: "gpt-4o", Name: "GPT-4o"},
		{ID: "gpt-4o-mini", Name: "GPT-4o Mini"},
		{ID: "gemini-2.5-pro", Name: "Gemini 2.5 Pro"},
		{ID: "gemini-2.5-flash", Name: "Gemini 2.5 Flash"},
		{ID: "gemini-2.5-flash-lite", Name: "Gemini 2.5 Flash Lite"},
	}
	pool := providerModels
	if len(pool) == 0 {
		pool = defaults
	}
	// Ensure the current model is present.
	if currentModel != "" {
		found := false
		for _, m := range pool {
			if m.ID == currentModel {
				found = true
				break
			}
		}
		if !found {
			pool = append([]ModelOptionValue{{ID: currentModel, Name: currentModel}}, pool...)
		}
	}
	opts := make([]ConfigOptionValue, len(pool))
	for i, m := range pool {
		opts[i] = ConfigOptionValue{Value: m.ID, Label: m.Name}
	}
	return ConfigOption{
		ID:           "model",
		Label:        "Model",
		Description:  "AI model to use for this session",
		Category:     "model",
		Type:         "select",
		CurrentValue: currentModel,
		Options:      opts,
	}
}

// agentConfigOption builds the "agent" config option.
func agentConfigOption(currentAgentID string, agents []AgentOption) ConfigOption {
	opts := make([]ConfigOptionValue, len(agents))
	for i, a := range agents {
		opts[i] = ConfigOptionValue{Value: a.ID, Label: a.Name, Description: a.Description}
	}
	return ConfigOption{
		ID:           "agent",
		Label:        "Agent",
		Description:  "Agent profile to use for this session",
		Category:     "_agent",
		Type:         "select",
		CurrentValue: currentAgentID,
		Options:      opts,
	}
}

// ============================================================
// session/set_config
// ============================================================

// SessionSetConfigParams are the parameters for session/set_config.
// The Updates map accepts keys: "provider", "model", "agent", "mode".
type SessionSetConfigParams struct {
	SessionID string            `json:"sessionId"`
	Updates   map[string]string `json:"updates"`
}

// SessionSetConfigResult is returned after a successful session/set_config.
type SessionSetConfigResult struct {
	ConfigOptions []ConfigOption `json:"configOptions"`
}

// =============================================================
// session/set_config_option (ZED ACP compliant)
// =============================================================

// SessionSetConfigOptionParams are the parameters for the session/set_config_option
// request. Each call updates a single config option identified by configId.
type SessionSetConfigOptionParams struct {
	SessionID string `json:"sessionId"`
	ConfigID  string `json:"configId"`
	Value     any    `json:"value"`
	// Type is optional: "select" (default) or "boolean".
	// Used by newer ACP clients to disambiguate value types.
	Type string `json:"type,omitempty"`
}

// SessionSetConfigOptionResult is returned after a successful session/set_config_option.
// It contains the complete set of all config options with their current values.
type SessionSetConfigOptionResult struct {
	ConfigOptions []ConfigOption `json:"configOptions"`
}

// ============================================================
// slash_command/list  +  slash_command/run
// ============================================================

// SlashCommandInfo describes one slash command the agent supports.
type SlashCommandInfo struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	// Args is a short argument hint shown in the editor's autocomplete UI.
	Args string `json:"args,omitempty"`
}

// SlashCommandListResult is the response to slash_command/list.
type SlashCommandListResult struct {
	Commands []SlashCommandInfo `json:"commands"`
}

// SlashCommandRunParams are the parameters for slash_command/run.
type SlashCommandRunParams struct {
	// SessionID is optional; some commands (e.g. /help) work without a session.
	SessionID string `json:"sessionId,omitempty"`
	Command   string `json:"command"`
	Args      string `json:"args,omitempty"`
}

// SlashCommandRunResult is the response to slash_command/run.
type SlashCommandRunResult struct {
	// Text is displayed in the agent panel (confirmation, list output, etc.).
	Text string `json:"text,omitempty"`
	// Insert is optional content placed into the editor's prompt input field.
	Insert []SlashInsertContent `json:"insert,omitempty"`
	// ConfigOptions are returned when a command changes session configuration so
	// the editor can refresh its dropdowns immediately.
	ConfigOptions []ConfigOption `json:"configOptions,omitempty"`
}

// SlashInsertContent is a piece of text to insert into the editor's prompt input.
type SlashInsertContent struct {
	Type string `json:"type"` // always "text"
	Text string `json:"text,omitempty"`
}

// ============================================================
// Agent / model option value helpers
// ============================================================

// AgentOption describes an available agent profile.
type AgentOption struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
}

// ModelOptionValue is a model entry used when building the model ConfigOption.
type ModelOptionValue struct {
	ID   string
	Name string
}
