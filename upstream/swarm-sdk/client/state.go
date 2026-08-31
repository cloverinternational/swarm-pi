// Package client — state.go
//
// Public state types and snapshot/active-getter methods for the unified
// client.Client surface.  These types lifted from session/types.go so
// that *client.Client can expose Snapshot()/ActiveAgent()/etc. natively.
//
// session.State, session.ConversationSummary, etc. remain valid as
// type aliases for transitional backwards compatibility — see
// session/types.go.
package client

import (
	"encoding/json"
	"strings"
	"time"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/configbundle"
)

// ─── Public state ─────────────────────────────────────────────────────────────

// State is an immutable snapshot of the client's view of the world.
// Returned by Client.Snapshot().  Mutating the returned value has no
// effect on the underlying client.
type State struct {
	// Conversation
	ActiveConvID  string
	Conversations []ConversationSummary

	// Provider/model
	Provider           string
	Model              string
	ModelContextWindow int

	// Operating mode
	OperatingMode string // "plan" | "act" | "auto"

	// Active agent name (the agent currently selected in the workspace).
	ActiveAgent string

	// Active profile (e.g. "default", "fast", "deep").
	ActiveProfile string

	// Streaming
	IsStreaming bool

	// Token usage (most recent turn)
	InputTokens     int
	OutputTokens    int
	TotalTokens     int
	CurrentContext  int
	CompactionCount int

	// Error tracking
	LastError string

	// Timestamps
	UpdatedAt time.Time
}

// ConversationSummary mirrors the legacy core.ConversationSummary so the
// IPC/ACP servers can switch from one to the other with minimal field
// renaming.
type ConversationSummary struct {
	ID            string    `json:"id"`
	Title         string    `json:"title"`
	Preview       string    `json:"preview,omitempty"`
	Recap         string    `json:"recap,omitempty"`
	MessageCount  int       `json:"messageCount"`
	TotalTokens   int       `json:"totalTokens"`
	UpdatedAt     time.Time `json:"updatedAt"`
	Status        string    `json:"status,omitempty"`
	ProjectID     string    `json:"projectId,omitempty"`
	WorkspacePath string    `json:"workspacePath,omitempty"`
	Tags          []string  `json:"tags,omitempty"`
	Origin        string    `json:"origin,omitempty"`

	// Unfinished reports whether the conversation's last turn did not reach a
	// natural terminal stop (see conversation.Conversation.IsUnfinished). Set
	// when the summary is populated from a *conversation.Conversation so the
	// TUI history menu can render an indicator without loading messages.
	Unfinished bool `json:"unfinished"`
}

// CreateOptions configures a new conversation.
type CreateOptions struct {
	ProjectID string
	Tags      []string
}

// ListOptions filters the conversation list.
type ListOptions struct {
	ProjectID     string
	WorkspacePath string
	Limit         int
	Offset        int
	// IncludeHeadless is for explicit provenance queries such as
	// HistorySearch(origin=headless). Ordinary UI listings keep excluding them.
	IncludeHeadless bool
}

// SendMessageOptions tunes a single SendMessage call.  All fields are
// optional; if Model is empty the client's currently configured model
// is used.
type SendMessageOptions struct {
	Model    string
	Mode     string         // overrides State.OperatingMode for this turn
	Metadata map[string]any // image attachments etc.

	// SystemPrompt, when non-empty, REPLACES the agent's configured system
	// prompt for this turn only. The client's stored prompt is untouched, so
	// subsequent turns revert to the configured prompt. (Replace semantics,
	// not prepend — a clearly defined behaviour the agent layer supports
	// cleanly via ExecuteRequest.SystemPromptOverride.)
	SystemPrompt string `json:"SystemPrompt,omitempty"`

	// MaxTurns caps the agent loop for this message. 0 = unlimited/default
	// (the agent's configured limit, if any).
	MaxTurns int `json:"MaxTurns,omitempty"`

	// DisableTools, when true, runs the turn with NO tools offered to the
	// model — a single text-only completion. The tool registry is unchanged.
	DisableTools bool `json:"DisableTools,omitempty"`

	// DisableHooks, when true, skips before/after tool hook execution for this
	// turn. The hooks manager stays attached for other turns.
	DisableHooks bool `json:"DisableHooks,omitempty"`
}

// ToolRegistryInfo mirrors the legacy core.ToolRegistryInfo.
type ToolRegistryInfo struct {
	ToolCount int
	ToolNames []string
}

// ─── Input specifications ─────────────────────────────────────────────────────

// AgentSpec carries the fields required to create or update an agent.
// Only Name is required; all other fields are optional.
type AgentSpec struct {
	Name         string
	Description  string
	SystemPrompt string
	Model        string
	Provider     string
	Temperature  float64
	MaxTokens    int
	Profile      string
	IsDefault    bool
}

// ProfileSpec carries the fields required to create or update a profile.
// Only Name is required; all other fields are optional.
type ProfileSpec struct {
	Name         string
	Description  string
	SystemPrompt string
	Provider     string
	Model        string
	Temperature  float64
	MaxTokens    int
}

// HookSpec carries the fields required to create or update a hook.
// Name and Event are required.
type HookSpec struct {
	Name        string
	Description string
	Event       string // "tool_call", "tool_result", "message", etc.
	ToolMatch   string // glob pattern for tool name
	Phase       string // "before", "after", "both"
	Command     string
	Timeout     int
	Enabled     bool
	CanBlock    bool
	Environment []string
}

// ContextSourceSpec carries the fields required to add a context source.
// Name and Type are required.
type ContextSourceSpec struct {
	Name        string
	Type        string // "file", "url", "command", "mcp", "rag", "api", "custom"
	Path        string
	URL         string
	Command     string
	MCPServer   string
	Enabled     bool
	RefreshSecs int
}

// ─── Snapshot + active getters ───────────────────────────────────────────────

// Snapshot returns an immutable copy of the client's session state.
//
// Returns the zero State if Start has not been called or the client was
// constructed without a config manager — but the basic Provider/Model
// fields are still populated from the active provider configuration.
func (c *Client) Snapshot() State {
	c.stateMu.RLock()
	defer c.stateMu.RUnlock()
	out := c.sessState
	if len(c.sessState.Conversations) > 0 {
		out.Conversations = make([]ConversationSummary, len(c.sessState.Conversations))
		copy(out.Conversations, c.sessState.Conversations)
	}
	return out
}

// ActiveAgent returns the currently active agent name.
func (c *Client) ActiveAgent() string {
	c.stateMu.RLock()
	defer c.stateMu.RUnlock()
	return c.sessState.ActiveAgent
}

// ActiveConversation returns the active conversation ID.
func (c *Client) ActiveConversation() string {
	c.stateMu.RLock()
	defer c.stateMu.RUnlock()
	return c.sessState.ActiveConvID
}

// SyncActiveConversation updates the in-memory active conversation pointer
// synchronously. It is intended for UI adapters that have already validated the
// conversation and therefore must not pay for another storage lookup (or race a
// later UI selection with an asynchronous SwitchConversation call). This is a
// state mirror, not a navigation command, so it deliberately emits no switch
// event; SwitchConversation remains the event-emitting public operation.
func (c *Client) SyncActiveConversation(id string) {
	id = strings.TrimSpace(id)
	c.stateMu.Lock()
	if c.sessState.ActiveConvID != id {
		c.sessState.ActiveConvID = id
		c.sessState.UpdatedAt = time.Now()
	}
	c.stateMu.Unlock()
}

// ClearActiveConversation synchronously clears the in-memory active
// conversation pointer without touching storage or emitting a navigation event.
func (c *Client) ClearActiveConversation() {
	c.SyncActiveConversation("")
}

// ActiveProfile returns the active profile name/ID.
func (c *Client) ActiveProfile() string {
	c.stateMu.RLock()
	defer c.stateMu.RUnlock()
	return c.sessState.ActiveProfile
}

// ActiveMode returns the current operating mode ("plan"|"act"|"auto").
func (c *Client) ActiveMode() string {
	c.stateMu.RLock()
	defer c.stateMu.RUnlock()
	return c.sessState.OperatingMode
}

// ToolRegistry returns names of every registered tool on the client,
// plus a count.
func (c *Client) ToolRegistry() ToolRegistryInfo {
	// Prefer the LIVE agent tool registry — the actual set of callable tools.
	// def.ToolHints is a curated hint list that is frequently empty, which made
	// the desktop's Tool Selection screen report "0 tools" even though Bash /
	// Edit / Write / Read were registered and working. List() is the source of
	// truth for what the model can actually call.
	if reg := c.AgentToolRegistry(); reg != nil {
		if names := reg.List(); len(names) > 0 {
			out := append([]string(nil), names...)
			return ToolRegistryInfo{ToolCount: len(out), ToolNames: out}
		}
	}
	// Read hints through the agent's LOCK-RESPECTING accessor, never through the
	// raw c.agentDef alias. ToolHints is runtime-mutated (SetToolHints, driven by
	// the closed harness path as MCP servers register/unregister tools), and
	// c.agentDef aliases the very same *agent.Definition the agent mutates, so an
	// unlocked read here is a live data race. Agent.Definition() clones under
	// a.mu.RLock. Falls back to the alias only when no agent exists (nothing can
	// be mutating it in that case).
	var hints []string
	if c.agent != nil {
		if def := c.agent.Definition(); def != nil {
			hints = append([]string(nil), def.ToolHints...)
		}
	} else if def := c.AgentInfo(); def != nil {
		hints = append([]string(nil), def.ToolHints...)
	} else {
		return ToolRegistryInfo{}
	}
	return ToolRegistryInfo{
		ToolCount: len(hints),
		ToolNames: hints,
	}
}

// ─── Active config bundle (read view) ────────────────────────────────────────

// ConfigEntity is a lightweight, JSON-friendly descriptor of a configured
// entity (agent, profile, hook, skill) for read-only listing in UIs.
type ConfigEntity struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Enabled bool   `json:"enabled"`
}

// ConfigBundleView is a read-only snapshot of the active config bundle's
// collections. It lets consumers (e.g. desktop/IDE UIs) list configured
// agents, profiles, hooks, skills, prompts and tool enable/disable state
// without depending on the internal configbundle types. The write side
// remains the CreateAgent/CreateHook/SwitchProfile/ToggleTool/... methods.
type ConfigBundleView struct {
	Source        string         `json:"source"`
	DefaultAgent  string         `json:"defaultAgent"`
	Agents        []ConfigEntity `json:"agents"`
	Profiles      []ConfigEntity `json:"profiles"`
	Hooks         []ConfigEntity `json:"hooks"`
	Skills        []ConfigEntity `json:"skills"`
	PromptNames   []string       `json:"promptNames"`
	EnabledTools  []string       `json:"enabledTools"`
	DisabledTools []string       `json:"disabledTools"`
	// System is the active SystemConfig flattened to a JSON object (theme,
	// showThinking, compactMode, steeringConfig, voice, custom, …) so UIs can
	// read back the values they persist via SetConfig.
	System map[string]any `json:"system"`
}

// ActiveConfigBundle returns a read-only view of the active config bundle's
// collections. Returns an empty (non-nil slices) view when no config manager
// is attached or no bundle is active. Additive read accessor — pairs with the
// existing write-side CRUD so UIs can both list and mutate configured entities.
func (c *Client) ActiveConfigBundle() ConfigBundleView {
	view := ConfigBundleView{
		Agents:        []ConfigEntity{},
		Profiles:      []ConfigEntity{},
		Hooks:         []ConfigEntity{},
		Skills:        []ConfigEntity{},
		PromptNames:   []string{},
		EnabledTools:  []string{},
		DisabledTools: []string{},
	}
	if c.cfgMgr == nil {
		return view
	}
	b := c.cfgMgr.Active()
	if b == nil {
		return view
	}

	view.Source = string(b.Source)
	view.DefaultAgent = b.Agents.DefaultAgent

	for _, a := range b.Agents.Definitions {
		view.Agents = append(view.Agents, ConfigEntity{ID: a.ID, Name: a.Name, Enabled: true})
	}
	for _, p := range b.Profiles.Inline {
		view.Profiles = append(view.Profiles, ConfigEntity{ID: p.ID, Name: p.Name, Enabled: true})
	}

	hookDisabled := make(map[string]bool, len(b.Hooks.Disabled))
	for _, id := range b.Hooks.Disabled {
		hookDisabled[id] = true
	}
	for _, h := range b.Hooks.Definitions {
		enabled := !hookDisabled[h.ID] && !hookDisabled[h.Name]
		view.Hooks = append(view.Hooks, ConfigEntity{ID: h.ID, Name: h.Name, Enabled: enabled})
	}

	for _, s := range b.Skills.Installed {
		enabled := s.Enabled == nil || *s.Enabled
		view.Skills = append(view.Skills, ConfigEntity{ID: s.ID, Name: s.Name, Enabled: enabled})
	}

	for name := range b.Prompts.Custom {
		view.PromptNames = append(view.PromptNames, name)
	}
	view.EnabledTools = append(view.EnabledTools, b.Tools.Enabled...)
	view.DisabledTools = append(view.DisabledTools, b.Tools.Disabled...)

	// Flatten the typed SystemConfig to a generic map so consumers can read
	// back arbitrary settings (including extension keys under "custom").
	if raw, err := json.Marshal(b.System); err == nil {
		var sm map[string]any
		if json.Unmarshal(raw, &sm) == nil {
			view.System = sm
		}
	}

	return view
}

// ─── Internal state helpers ──────────────────────────────────────────────────

// SetSessionState re-seeds the embedded session-state struct with the
// supplied initial mode/agent/profile.  Used by the legacy
// session.New(Config{...}) compat shim to honour Config.InitialMode etc.
// when re-using an existing *Client.  Most callers should use the
// WithActiveMode / WithActiveAgent / WithActiveProfile options at
// construction time instead.
func (c *Client) SetSessionState(initialMode, initialAgent, initialProfile string) {
	c.initSessionState(initialMode, initialAgent, initialProfile)
}

// SetConfigManager attaches mgr to the client without going through
// Reconfigure (which would require a configured provider).  Intended for
// callers and tests that want CRUD methods to work on a bare Client
// constructed with `&Client{}` and no provider.  Most callers should
// use WithConfigManager at construction time instead.
func (c *Client) SetConfigManager(mgr *configbundle.Manager) {
	c.cfgMgr = mgr
}

// initSessionState seeds the embedded session-state struct with
// provider/mode/agent/profile values pulled from options or
// ProviderInfo().  Called from New() once after initAgent succeeds.
func (c *Client) initSessionState(initialMode, initialAgent, initialProfile string) {
	pi := c.ProviderInfo()
	mode := initialMode
	if mode == "" {
		mode = "act"
	}
	c.stateMu.Lock()
	defer c.stateMu.Unlock()
	c.sessState = State{
		Provider:           pi.Name,
		Model:              pi.Model,
		ModelContextWindow: pi.ContextWindow,
		OperatingMode:      mode,
		ActiveAgent:        initialAgent,
		ActiveProfile:      initialProfile,
		UpdatedAt:          time.Now(),
	}
}

func (c *Client) setStreaming(on bool) {
	c.stateMu.Lock()
	c.sessState.IsStreaming = on
	c.sessState.UpdatedAt = time.Now()
	c.stateMu.Unlock()
}

func (c *Client) recordError(err error, msg string) {
	c.stateMu.Lock()
	c.sessState.LastError = msg + ": " + err.Error()
	c.sessState.UpdatedAt = time.Now()
	c.stateMu.Unlock()
	c.dispatchEvent(Event{Kind: EventError, Payload: ErrorPayload{Err: err, Message: msg}, At: time.Now()})
}
