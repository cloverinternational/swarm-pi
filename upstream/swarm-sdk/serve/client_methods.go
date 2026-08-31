// Package serve — client_methods.go
//
// Built-in registrations for the high-traffic *client.Client surface.  Each
// entry is a thin adapter: unmarshal the typed param struct → call the
// matching client method → return its result.  Adding more methods is
// straightforward (one Method literal per call).
package serve

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/Swarm-Code/mono/swarm-sdk/client"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/conversation/manager"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/identity"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/lifecycle"
)

// clientStatusViewSnapshot derives a lifecycle.Snapshot from a
// client.State. The client library itself carries no daemon-lifecycle
// authority (that machinery is owned by cmd/swarmos's daemon_cli.go /
// global_daemon.go — deliberately out of scope for this file this round,
// per the Phase 03 readiness-status-registry contract), so this is a
// deliberately narrow, additive inference limited to what client.State
// actually observes: whether the client currently has an in-flight turn.
// IsStreaming=true maps to `working` (an accepted active execution is
// running); otherwise this reports `ready` (idle, able to accept the next
// turn) — never `degraded`/`absent`/`stopped`, which this package has no
// evidence for. This mirrors ADR-005's "ready already means idle and able
// to accept work" and "working" definitions exactly, applied only to the
// evidence available at this layer.
func clientStatusViewSnapshot(st client.State) lifecycle.Snapshot {
	state := lifecycle.StateReady
	if st.IsStreaming {
		state = lifecycle.StateWorking
	}
	return lifecycle.Snapshot{
		State:      state,
		Intent:     lifecycle.IntentObserve,
		ObservedAt: st.UpdatedAt,
		// ActiveExecutionID intentionally stays empty: the client library
		// does not (yet) hand this layer a canonical execution identity —
		// see ADR-005 "Security": "Active execution is represented by
		// canonical identity, not `current_task` display text." Recording a
		// fabricated non-empty value here would misrepresent a real
		// identity that does not exist at this layer.
	}
}

// registerBuiltinMethods is called from NewMux to populate the default
// method table.
func registerBuiltinMethods(m *Mux) {
	// ── Lifecycle ───────────────────────────────────────────────────────
	m.Register(Method{Name: "client.start", Handler: handleStart})
	m.Register(Method{Name: "client.stop", Handler: handleStop})

	// ── Read-only state ────────────────────────────────────────────────
	m.Register(Method{Name: "client.snapshot", Handler: handleSnapshot})
	m.Register(Method{Name: "client.snapshotWire", Handler: handleSnapshotWire})
	m.Register(Method{Name: "client.statusView", Handler: handleStatusView})
	m.Register(Method{Name: "client.providerInfo", Handler: handleProviderInfo})
	m.Register(Method{Name: "client.activeAgent", Handler: handleActiveAgent})
	m.Register(Method{Name: "client.activeConversation", Handler: handleActiveConversation})
	m.Register(Method{Name: "client.activeProfile", Handler: handleActiveProfile})
	m.Register(Method{Name: "client.activeMode", Handler: handleActiveMode})
	m.Register(Method{Name: "client.toolRegistry", Handler: handleToolRegistry})

	// ── Messaging ───────────────────────────────────────────────────────
	m.Register(Method{Name: "client.sendMessage", Handler: handleSendMessage, Streams: true})
	m.Register(Method{Name: "client.cancel", Handler: handleCancel})

	// ── Conversation lifecycle ──────────────────────────────────────────
	m.Register(Method{Name: "client.createConversation", Handler: handleCreateConversation})
	m.Register(Method{Name: "client.switchConversation", Handler: handleSwitchConversation})
	m.Register(Method{Name: "client.deleteConversation", Handler: handleDeleteConversation})
	m.Register(Method{Name: "client.listConversations", Handler: handleListConversations})
	m.Register(Method{Name: "client.editMessage", Handler: handleEditMessage})

	// ── Mode / model / provider / profile ──────────────────────────────
	m.Register(Method{Name: "client.setMode", Handler: handleSetMode})
	m.Register(Method{Name: "client.setModel", Handler: handleSetModel})
	m.Register(Method{Name: "client.setProvider", Handler: handleSetProvider})
	m.Register(Method{Name: "client.switchProfile", Handler: handleSwitchProfile})

	// ── Compaction ──────────────────────────────────────────────────────
	m.Register(Method{Name: "client.compact", Handler: handleCompact})
	m.Register(Method{Name: "client.compactDetail", Handler: handleCompactDetail})

	// ── Agent / hook / tool / context-source CRUD ──────────────────────
	m.Register(Method{Name: "client.setAgent", Handler: handleSetAgent})
	m.Register(Method{Name: "client.createAgent", Handler: handleCreateAgent})
	m.Register(Method{Name: "client.updateAgent", Handler: handleUpdateAgent})
	m.Register(Method{Name: "client.deleteAgent", Handler: handleDeleteAgent})
	m.Register(Method{Name: "client.createHook", Handler: handleCreateHook})
	m.Register(Method{Name: "client.updateHook", Handler: handleUpdateHook})
	m.Register(Method{Name: "client.deleteHook", Handler: handleDeleteHook})
	m.Register(Method{Name: "client.toggleHook", Handler: handleToggleHook})
	m.Register(Method{Name: "client.toggleTool", Handler: handleToggleTool})
	m.Register(Method{Name: "client.setSystemPrompt", Handler: handleSetSystemPrompt})
	m.Register(Method{Name: "client.addContextSource", Handler: handleAddContextSource})
	m.Register(Method{Name: "client.removeContextSource", Handler: handleRemoveContextSource})
	m.Register(Method{Name: "client.toggleContextSource", Handler: handleToggleContextSource})
	m.Register(Method{Name: "client.createProfile", Handler: handleCreateProfile})
	m.Register(Method{Name: "client.updateProfile", Handler: handleUpdateProfile})
	m.Register(Method{Name: "client.deleteProfile", Handler: handleDeleteProfile})

	// ── Config persistence ─────────────────────────────────────────────
	m.Register(Method{Name: "client.setConfig", Handler: handleSetConfig})
	m.Register(Method{Name: "client.loadConfig", Handler: handleLoadConfig})
	m.Register(Method{Name: "client.saveConfig", Handler: handleSaveConfig})

	// ── UI/UX preferences ──────────────────────────────────────────────
	m.Register(Method{Name: "client.setTheme", Handler: handleSetTheme})
	m.Register(Method{Name: "client.toggleCompactMode", Handler: handleToggleCompactMode})

	// ── History ─────────────────────────────────────────────────────────
	m.Register(Method{Name: "client.clearHistory", Handler: handleClearHistory})
	m.Register(Method{Name: "client.searchHistory", Handler: handleSearchHistory})
	m.Register(Method{Name: "client.getMessages", Handler: handleGetMessages})

	// ── State getters (read-only; a UI renders these) ───────────────────
	m.Register(Method{Name: "client.tokenUsage", Handler: handleTokenUsage})
	m.Register(Method{Name: "client.cacheStats", Handler: handleCacheStats})
	m.Register(Method{Name: "client.listWorkspaces", Handler: handleListWorkspaces})
	m.Register(Method{Name: "client.setWorkspace", Handler: handleSetWorkspace})

	// ── Interactive tool approval (daemon G1) ───────────────────────────
	m.Register(Method{Name: "client.respondApproval", Handler: handleRespondApproval})
	m.Register(Method{Name: "client.pendingApprovals", Handler: handlePendingApprovals})

	// ── Config / conversation commands ──────────────────────────────────
	m.Register(Method{Name: "client.setMaxTokens", Handler: handleSetMaxTokens})
	m.Register(Method{Name: "client.setConversationTitle", Handler: handleSetConversationTitle})

	// ── Daemon diagnostics ──────────────────────────────────────────────
	m.Register(Method{Name: "daemon.getLogs", Handler: handleDaemonGetLogs})
}

// ── Param structs ────────────────────────────────────────────────────────

type sendMessageParams struct {
	// Historically this method's conversation-id key is "convId" while
	// getMessages/setConversationTitle use "convID". That split casing is a
	// footgun: a client that reuses the getMessages casing here would silently
	// send to the ACTIVE conversation instead of the intended one. We accept
	// BOTH spellings (see resolveConvID) so either casing routes correctly;
	// "convId" stays the canonical wire key for backward compatibility.
	ConvID    string                     `json:"convId"`
	ConvIDAlt string                     `json:"convID"`
	Message   string                     `json:"message"`
	Opts      *client.SendMessageOptions `json:"opts,omitempty"`
}

// resolveConvID returns whichever conversation-id spelling the caller supplied
// (canonical "convId" wins when both are set).
func (p sendMessageParams) resolveConvID() string {
	if p.ConvID != "" {
		return p.ConvID
	}
	return p.ConvIDAlt
}

type idParams struct {
	ID string `json:"id"`
}

type createConversationParams struct {
	ProjectID string   `json:"projectId,omitempty"`
	Tags      []string `json:"tags,omitempty"`
}

type listConversationsParams struct {
	ProjectID     string `json:"projectId,omitempty"`
	WorkspacePath string `json:"workspacePath,omitempty"`
	Limit         int    `json:"limit,omitempty"`
	Offset        int    `json:"offset,omitempty"`
}

type editMessageParams struct {
	ConvID    string `json:"convId"`
	MessageID string `json:"messageId"`
	NewText   string `json:"newText"`
}

type setModeParams struct {
	Mode string `json:"mode"`
}

type setWorkspaceParams struct {
	Dir string `json:"dir"`
	// ClientSessionID is OPTIONAL, additive, session-scoping input — see
	// CONTRACT.md section 3 ("Any new session-scoped RPC/wire field MUST
	// be named client_session_id"), matching
	// internal/journal/record.go:71's Record.ClientSessionID JSON tag
	// spelling exactly. When non-empty and it parses as a valid
	// identity.ClientSessionID, handleSetWorkspace records the workspace
	// selection in serve's per-session sessionScopeRegistry (see
	// session_scope.go) INSTEAD OF mutating the one shared *client.Client
	// passed to every dispatch (see mux.go's NewMux(c *client.Client) —
	// today ALL attached sessions dispatch through that single shared
	// instance). When empty/absent (a legacy caller that hasn't been
	// updated to send it yet), handleSetWorkspace falls back to exactly
	// today's global c.SetWorkspace behavior, unchanged — this field is
	// additive, not a breaking rename; see the fallback comment on
	// handleSetWorkspace itself for the full rationale.
	ClientSessionID string `json:"client_session_id,omitempty"`
}

// getWorkspaceParams is the minimal companion params struct for a
// session-scoped "what workspace is THIS session looking at" read,
// mirroring setWorkspaceParams' client_session_id field. No handler is
// registered for it this phase (out of scope: the P07.B brief only
// requires wiring handleSetWorkspace's write path plus the registry
// itself); it exists so the wire shape is defined and ready for a
// follow-on read-side handler to reuse without re-deriving the field
// name/JSON tag.
type getWorkspaceParams struct {
	ClientSessionID string `json:"client_session_id,omitempty"`
}

type setModelParams struct {
	Model string `json:"model"`
}

type setProviderParams struct {
	Provider string `json:"provider"`
	Model    string `json:"model,omitempty"`
}

type switchProfileParams struct {
	ProfileID string `json:"profileId"`
}

type compactParams struct {
	ConvID string `json:"convId,omitempty"`
}

type setAgentParams struct {
	Name string `json:"name"`
}

type agentSpecParams = client.AgentSpec
type profileSpecParams = client.ProfileSpec
type hookSpecParams = client.HookSpec
type contextSourceSpecParams = client.ContextSourceSpec

type nameParams struct {
	Name string `json:"name"`
}

type toggleToolParams struct {
	Name    string `json:"name"`
	Enabled bool   `json:"enabled"`
}

type setSystemPromptParams struct {
	Name    string `json:"name"`
	Content string `json:"content"`
}

type setConfigParams struct {
	Key   string          `json:"key"`
	Value json.RawMessage `json:"value"`
}

type setThemeParams struct {
	Theme string `json:"theme"`
}

type searchHistoryParams struct {
	Query string `json:"query"`
}

type getMessagesParams struct {
	ConvID string `json:"convID"` // empty = the active conversation
	Limit  int    `json:"limit,omitempty"`
	Offset int    `json:"offset,omitempty"`
}

type respondApprovalParams struct {
	CallID string `json:"callID"`
	Allow  bool   `json:"allow"`
}

type setMaxTokensParams struct {
	MaxTokens int `json:"maxTokens"`
}

type setConversationTitleParams struct {
	ConvID string `json:"convID"` // empty = active conversation
	Title  string `json:"title"`
}

// ── Handlers ─────────────────────────────────────────────────────────────

func decode[T any](raw json.RawMessage) (T, error) {
	var v T
	if len(raw) == 0 || string(raw) == "null" {
		return v, nil
	}
	if err := json.Unmarshal(raw, &v); err != nil {
		return v, NewError(ErrInvalidParams, err.Error(), nil)
	}
	return v, nil
}

func handleStart(ctx context.Context, c *client.Client, _ json.RawMessage) (any, error) {
	return nil, c.Start(ctx)
}

func handleStop(ctx context.Context, c *client.Client, _ json.RawMessage) (any, error) {
	return nil, c.Stop(ctx)
}

func handleSnapshot(_ context.Context, c *client.Client, _ json.RawMessage) (any, error) {
	return c.Snapshot(), nil
}

// handleSnapshotWire is the JSON-safe projection for remote clients. The
// regular client.snapshot method intentionally preserves the public Go State
// value for in-process callers, but that value contains time.Time fields which
// the bounded transport encoder rejects. Keep this projection scalar-only.
func handleSnapshotWire(_ context.Context, c *client.Client, _ json.RawMessage) (any, error) {
	snap := c.Snapshot()
	return map[string]any{
		"provider":           snap.Provider,
		"model":              snap.Model,
		"operatingMode":      snap.OperatingMode,
		"activeConversation": snap.ActiveConvID,
		"isStreaming":        snap.IsStreaming,
		"inputTokens":        snap.InputTokens,
		"outputTokens":       snap.OutputTokens,
		"totalTokens":        snap.TotalTokens,
	}, nil
}

// handleStatusView is the additive Phase 03 companion to client.snapshot:
// it surfaces internal/lifecycle's ONE canonical status DTO (the exact
// field names the healthz/readyz payload, presence display, and CLI
// status/list output all format from — see
// .swarmflow/swarm-attach-architecture/p03-readiness-status-registry/CONTRACT.md)
// so an RPC caller can read state/intent/reason/DisplayLabel in the same
// shape those other surfaces use, without changing client.snapshot's
// existing client.State return shape (client.snapshot is depended on by
// existing tests — see serve/mux_test.go's
// `res.(client.State)` type assertion — and by external consumers, so it
// stays untouched; this is a new, purely additive method instead of a
// breaking change to an existing one).
//
// hasDiscoveryEvidence is always false here: this handler runs inside the
// client's own process and has no presence/discovery evidence about
// itself (internal/a2a discovery is out of scope for this file this
// round). pendingUpgrade is always false for the same reason — no upgrade
// intent is tracked at this layer. Both are legitimate, honestly-reported
// "no evidence" inputs, not guesses.
func handleStatusView(_ context.Context, c *client.Client, _ json.RawMessage) (any, error) {
	snap := clientStatusViewSnapshot(c.Snapshot())
	return lifecycle.NewStatusView(snap, false, false), nil
}

func handleProviderInfo(_ context.Context, c *client.Client, _ json.RawMessage) (any, error) {
	return c.ProviderInfo(), nil
}

func handleActiveAgent(_ context.Context, c *client.Client, _ json.RawMessage) (any, error) {
	return c.ActiveAgent(), nil
}

func handleActiveConversation(_ context.Context, c *client.Client, _ json.RawMessage) (any, error) {
	return c.ActiveConversation(), nil
}

func handleActiveProfile(_ context.Context, c *client.Client, _ json.RawMessage) (any, error) {
	return c.ActiveProfile(), nil
}

func handleActiveMode(_ context.Context, c *client.Client, _ json.RawMessage) (any, error) {
	return c.ActiveMode(), nil
}

func handleToolRegistry(_ context.Context, c *client.Client, _ json.RawMessage) (any, error) {
	return c.ToolRegistry(), nil
}

func handleSendMessage(ctx context.Context, c *client.Client, raw json.RawMessage) (any, error) {
	p, err := decode[sendMessageParams](raw)
	if err != nil {
		return nil, err
	}
	if p.Message == "" {
		return nil, NewError(ErrInvalidParams, "message is required", nil)
	}
	var opts client.SendMessageOptions
	if p.Opts != nil {
		opts = *p.Opts
	}
	// Run the agent turn on a context that is NOT cancelled when the HTTP request
	// connection drops. Report turns can run many minutes; a remote engine's HTTP
	// client (e.g. Bun's ~5-minute fetch cap) may disconnect mid-turn. Tying the
	// turn to the request ctx would cancel it on that disconnect and lose the work.
	// Decoupling lets the turn finish server-side; clients detect completion via
	// client.snapshot (IsStreaming) + /sse. Retains request values (auth, etc.).
	turnCtx := context.WithoutCancel(ctx)
	if err := c.SendMessage(turnCtx, p.resolveConvID(), p.Message, opts); err != nil {
		return nil, err
	}
	return map[string]string{"convId": c.ActiveConversation()}, nil
}

func handleCancel(ctx context.Context, c *client.Client, _ json.RawMessage) (any, error) {
	return nil, c.Cancel(ctx)
}

func handleCreateConversation(ctx context.Context, c *client.Client, raw json.RawMessage) (any, error) {
	p, err := decode[createConversationParams](raw)
	if err != nil {
		return nil, err
	}
	id, err := c.CreateConversation(ctx, client.CreateOptions{
		ProjectID: p.ProjectID,
		Tags:      p.Tags,
	})
	if err != nil {
		return nil, err
	}
	return map[string]string{"id": id}, nil
}

func handleSwitchConversation(ctx context.Context, c *client.Client, raw json.RawMessage) (any, error) {
	p, err := decode[idParams](raw)
	if err != nil {
		return nil, err
	}
	return nil, c.SwitchConversation(ctx, p.ID)
}

func handleDeleteConversation(ctx context.Context, c *client.Client, raw json.RawMessage) (any, error) {
	p, err := decode[idParams](raw)
	if err != nil {
		return nil, err
	}
	return nil, c.DeleteConversation(ctx, p.ID)
}

func handleListConversations(ctx context.Context, c *client.Client, raw json.RawMessage) (any, error) {
	p, err := decode[listConversationsParams](raw)
	if err != nil {
		return nil, err
	}
	convs, err := c.ListConversations(ctx, client.ListOptions{
		ProjectID:     p.ProjectID,
		WorkspacePath: p.WorkspacePath,
		Limit:         p.Limit,
		Offset:        p.Offset,
	})
	if err != nil {
		return nil, err
	}
	return map[string]any{"conversations": convs}, nil
}

func handleEditMessage(ctx context.Context, c *client.Client, raw json.RawMessage) (any, error) {
	p, err := decode[editMessageParams](raw)
	if err != nil {
		return nil, err
	}
	return nil, c.EditMessage(ctx, p.ConvID, p.MessageID, p.NewText)
}

func handleSetMode(ctx context.Context, c *client.Client, raw json.RawMessage) (any, error) {
	p, err := decode[setModeParams](raw)
	if err != nil {
		return nil, err
	}
	return nil, c.SetMode(ctx, p.Mode)
}

func handleSetModel(ctx context.Context, c *client.Client, raw json.RawMessage) (any, error) {
	p, err := decode[setModelParams](raw)
	if err != nil {
		return nil, err
	}
	return nil, c.SetModel(ctx, p.Model)
}

func handleSetProvider(ctx context.Context, c *client.Client, raw json.RawMessage) (any, error) {
	p, err := decode[setProviderParams](raw)
	if err != nil {
		return nil, err
	}
	return nil, c.SetProvider(ctx, p.Provider, p.Model)
}

func handleSwitchProfile(ctx context.Context, c *client.Client, raw json.RawMessage) (any, error) {
	p, err := decode[switchProfileParams](raw)
	if err != nil {
		return nil, err
	}
	return nil, c.SwitchProfile(ctx, p.ProfileID)
}

func handleCompact(ctx context.Context, c *client.Client, raw json.RawMessage) (any, error) {
	p, err := decode[compactParams](raw)
	if err != nil {
		return nil, err
	}
	return nil, c.Compact(ctx, p.ConvID)
}

func handleCompactDetail(ctx context.Context, c *client.Client, raw json.RawMessage) (any, error) {
	p, err := decode[compactParams](raw)
	if err != nil {
		return nil, err
	}
	return c.CompactDetail(ctx, p.ConvID)
}

func handleSetAgent(ctx context.Context, c *client.Client, raw json.RawMessage) (any, error) {
	p, err := decode[setAgentParams](raw)
	if err != nil {
		return nil, err
	}
	return nil, c.SetAgent(ctx, p.Name)
}

func handleCreateAgent(ctx context.Context, c *client.Client, raw json.RawMessage) (any, error) {
	p, err := decode[agentSpecParams](raw)
	if err != nil {
		return nil, err
	}
	return nil, c.CreateAgent(ctx, p)
}

func handleUpdateAgent(ctx context.Context, c *client.Client, raw json.RawMessage) (any, error) {
	p, err := decode[agentSpecParams](raw)
	if err != nil {
		return nil, err
	}
	return nil, c.UpdateAgent(ctx, p)
}

func handleDeleteAgent(ctx context.Context, c *client.Client, raw json.RawMessage) (any, error) {
	p, err := decode[nameParams](raw)
	if err != nil {
		return nil, err
	}
	return nil, c.DeleteAgent(ctx, p.Name)
}

func handleCreateHook(ctx context.Context, c *client.Client, raw json.RawMessage) (any, error) {
	p, err := decode[hookSpecParams](raw)
	if err != nil {
		return nil, err
	}
	return nil, c.CreateHook(ctx, p)
}

func handleUpdateHook(ctx context.Context, c *client.Client, raw json.RawMessage) (any, error) {
	p, err := decode[hookSpecParams](raw)
	if err != nil {
		return nil, err
	}
	return nil, c.UpdateHook(ctx, p)
}

func handleDeleteHook(ctx context.Context, c *client.Client, raw json.RawMessage) (any, error) {
	p, err := decode[nameParams](raw)
	if err != nil {
		return nil, err
	}
	return nil, c.DeleteHook(ctx, p.Name)
}

func handleToggleHook(ctx context.Context, c *client.Client, raw json.RawMessage) (any, error) {
	p, err := decode[nameParams](raw)
	if err != nil {
		return nil, err
	}
	return nil, c.ToggleHook(ctx, p.Name)
}

func handleToggleTool(ctx context.Context, c *client.Client, raw json.RawMessage) (any, error) {
	p, err := decode[toggleToolParams](raw)
	if err != nil {
		return nil, err
	}
	return nil, c.ToggleTool(ctx, p.Name, p.Enabled)
}

func handleSetSystemPrompt(ctx context.Context, c *client.Client, raw json.RawMessage) (any, error) {
	p, err := decode[setSystemPromptParams](raw)
	if err != nil {
		return nil, err
	}
	return nil, c.SetSystemPrompt(ctx, p.Name, p.Content)
}

func handleAddContextSource(ctx context.Context, c *client.Client, raw json.RawMessage) (any, error) {
	p, err := decode[contextSourceSpecParams](raw)
	if err != nil {
		return nil, err
	}
	return nil, c.AddContextSource(ctx, p)
}

func handleRemoveContextSource(ctx context.Context, c *client.Client, raw json.RawMessage) (any, error) {
	p, err := decode[nameParams](raw)
	if err != nil {
		return nil, err
	}
	return nil, c.RemoveContextSource(ctx, p.Name)
}

func handleToggleContextSource(ctx context.Context, c *client.Client, raw json.RawMessage) (any, error) {
	p, err := decode[nameParams](raw)
	if err != nil {
		return nil, err
	}
	return nil, c.ToggleContextSource(ctx, p.Name)
}

func handleCreateProfile(ctx context.Context, c *client.Client, raw json.RawMessage) (any, error) {
	p, err := decode[profileSpecParams](raw)
	if err != nil {
		return nil, err
	}
	return nil, c.CreateProfile(ctx, p)
}

func handleUpdateProfile(ctx context.Context, c *client.Client, raw json.RawMessage) (any, error) {
	p, err := decode[profileSpecParams](raw)
	if err != nil {
		return nil, err
	}
	return nil, c.UpdateProfile(ctx, p)
}

func handleDeleteProfile(ctx context.Context, c *client.Client, raw json.RawMessage) (any, error) {
	p, err := decode[nameParams](raw)
	if err != nil {
		return nil, err
	}
	return nil, c.DeleteProfile(ctx, p.Name)
}

func handleSetConfig(ctx context.Context, c *client.Client, raw json.RawMessage) (any, error) {
	p, err := decode[setConfigParams](raw)
	if err != nil {
		return nil, err
	}
	if p.Key == "" {
		return nil, NewError(ErrInvalidParams, "key is required", nil)
	}
	// Decode value as a generic any so the underlying SetConfig sees the
	// caller's intended Go type (string / number / bool / object).
	var v any
	if len(p.Value) > 0 {
		if err := json.Unmarshal(p.Value, &v); err != nil {
			return nil, NewError(ErrInvalidParams, "value: "+err.Error(), nil)
		}
	}
	return nil, c.SetConfig(ctx, p.Key, v)
}

func handleLoadConfig(ctx context.Context, c *client.Client, _ json.RawMessage) (any, error) {
	return nil, c.LoadConfig(ctx)
}

func handleSaveConfig(ctx context.Context, c *client.Client, _ json.RawMessage) (any, error) {
	return nil, c.SaveConfig(ctx)
}

func handleSetTheme(ctx context.Context, c *client.Client, raw json.RawMessage) (any, error) {
	p, err := decode[setThemeParams](raw)
	if err != nil {
		return nil, err
	}
	return nil, c.SetTheme(ctx, p.Theme)
}

func handleToggleCompactMode(ctx context.Context, c *client.Client, _ json.RawMessage) (any, error) {
	return nil, c.ToggleCompactMode(ctx)
}

func handleClearHistory(ctx context.Context, c *client.Client, _ json.RawMessage) (any, error) {
	return nil, c.ClearHistory(ctx)
}

func handleSearchHistory(ctx context.Context, c *client.Client, raw json.RawMessage) (any, error) {
	p, err := decode[searchHistoryParams](raw)
	if err != nil {
		return nil, err
	}
	results, err := c.SearchHistory(ctx, p.Query)
	if err != nil {
		return nil, err
	}
	return map[string]any{"conversations": results}, nil
}

// handleGetMessages returns the full message transcript for a conversation so a
// remote UI (TUI/webapp) can render scrollback on connect or conversation
// switch.  Empty convID = the active conversation.  This is the daemon READ
// counterpart to the live event stream (which only carries NEW turns).
func handleGetMessages(ctx context.Context, c *client.Client, raw json.RawMessage) (any, error) {
	p, err := decode[getMessagesParams](raw)
	if err != nil {
		return nil, err
	}
	convID := p.ConvID
	explicit := convID != ""
	if convID == "" {
		convID = c.ActiveConversation()
	}
	msgs, err := c.GetConversationMessages(ctx, convID, manager.GetMessagesOptions{Limit: p.Limit, Offset: p.Offset})
	if err != nil {
		// A freshly-started or newly-attached session can have an active
		// conversation id whose transcript isn't stored yet (or no active
		// conversation at all). For an IMPLICIT "give me the active
		// conversation" request (empty convID) that is a normal empty state,
		// not an error — degrade to an empty transcript so an attaching UI (the
		// mobile PWA / peer inspector) renders "no messages yet" instead of a
		// scary "getMessages failed: conversation not found" RPC error. An
		// EXPLICIT convID that misses is a genuine lookup failure and still
		// surfaces the error.
		if !explicit {
			return map[string]any{"convID": convID, "messages": []any{}}, nil
		}
		return nil, err
	}
	return map[string]any{"convID": convID, "messages": msgs}, nil
}

// handleTokenUsage exposes the most-recent-turn token counts so a remote UI can
// render its token/context bar without owning the accounting.
func handleTokenUsage(_ context.Context, c *client.Client, _ json.RawMessage) (any, error) {
	in, out := c.TokenUsage()
	return map[string]any{"input": in, "output": out, "total": in + out}, nil
}

// handleCacheStats exposes prompt-cache creation/read counts for a UI cache
// indicator.
func handleCacheStats(_ context.Context, c *client.Client, _ json.RawMessage) (any, error) {
	creation, read := c.CacheStats()
	return map[string]any{"creation": creation, "read": read}, nil
}

// handleListWorkspaces exposes the workspace list for a UI workspace switcher.
func handleListWorkspaces(_ context.Context, c *client.Client, _ json.RawMessage) (any, error) {
	ws, err := c.ListWorkspaces()
	if err != nil {
		return nil, err
	}
	return map[string]any{"workspaces": ws}, nil
}

// handleSetWorkspace points the client at a workspace directory. New
// conversations created afterwards scope to it (NewConversation uses the
// client's workspace), so a thin UI / the one global daemon can re-scope to
// whatever directory the user opened. Returns the resolved workspace.
//
// Session-scoping fix (P07.B CONTRACT.md, "Session-scoped
// workspace/conversation + attempt identity wiring"): serve.NewMux wraps
// exactly ONE shared *client.Client instance for every attached RPC
// session (mux.go's NewMux(c *client.Client) — confirmed by reading
// mux.go before this change). Calling c.SetWorkspace(p.Dir) directly on
// that shared instance therefore has NO per-session isolation: two
// attachclient sessions selecting different workspaces concurrently race
// on client.Client's single mutable `workspace` field and cross-
// contaminate each other's selection — the exact bug
// docs/architecture/swarm-attach/package-boundaries.md:423's invariant
// ("Selection is session-scoped and cannot mutate another client.")
// documents but, before this change, did not actually enforce.
//
// Fix: when the caller supplies a non-empty, well-formed
// client_session_id (see setWorkspaceParams.ClientSessionID doc comment
// above), the workspace selection is written into serve's package-local
// sessionScopeRegistry (session_scope.go), keyed by that session's own
// identity.ClientSessionID, and the shared *client.Client is NOT
// mutated at all — so two sessions with different client_session_id
// values can never observe or clobber each other's workspace choice.
//
// COMPATIBILITY FALLBACK (per this phase's brief: "if the
// client_session_id field is empty/absent (legacy caller not yet
// updated), preserve TODAY'S global-field behavior unchanged"): when
// client_session_id is empty, absent, or fails to parse as a valid
// identity.ClientSessionID, this handler falls back to EXACTLY the
// pre-existing behavior (c.SetWorkspace(p.Dir) on the shared instance)
// so no existing caller (see serve/set_workspace_test.go, which never
// sends client_session_id and is NOT in P07.B's editable file list) is
// broken by this additive change.
func handleSetWorkspace(_ context.Context, c *client.Client, raw json.RawMessage) (any, error) {
	p, err := decode[setWorkspaceParams](raw)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(p.Dir) == "" {
		return nil, fmt.Errorf("client.setWorkspace: dir is required")
	}

	if strings.TrimSpace(p.ClientSessionID) != "" {
		sid, err := identity.ParseClientSessionID(p.ClientSessionID)
		if err == nil {
			globalSessionScopes.SetWorkspace(sid, p.Dir)
			return map[string]any{"workspace": p.Dir}, nil
		}
		// An unparseable client_session_id is a caller bug, but per this
		// phase's additive/non-breaking mandate we do not hard-fail an
		// otherwise-valid setWorkspace call over it — fall through to the
		// legacy global-field compatibility path below, exactly as if
		// client_session_id had been omitted.
	}

	// Legacy/compatibility fallback: no (valid) client_session_id supplied
	// — preserve today's global-field behavior unchanged (see the doc
	// comment above and CONTRACT.md section 3).
	c.SetWorkspace(p.Dir)
	return map[string]any{"workspace": c.WorkspaceDir()}, nil
}

// handleRespondApproval resolves an interactive tool-call approval announced via
// the EventApprovalRequested event — the daemon command half of gap G1.
func handleRespondApproval(_ context.Context, c *client.Client, raw json.RawMessage) (any, error) {
	p, err := decode[respondApprovalParams](raw)
	if err != nil {
		return nil, err
	}
	if err := c.RespondApproval(p.CallID, p.Allow); err != nil {
		return nil, err
	}
	return map[string]any{"ok": true}, nil
}

// handlePendingApprovals lists tool-call approvals awaiting a decision so a
// (re)connecting UI can re-render outstanding prompts.
func handlePendingApprovals(_ context.Context, c *client.Client, _ json.RawMessage) (any, error) {
	return map[string]any{"pending": c.PendingApprovals()}, nil
}

// handleSetMaxTokens lets a UI change the max output tokens per request.
func handleSetMaxTokens(ctx context.Context, c *client.Client, raw json.RawMessage) (any, error) {
	p, err := decode[setMaxTokensParams](raw)
	if err != nil {
		return nil, err
	}
	if err := c.SetMaxTokens(ctx, p.MaxTokens); err != nil {
		return nil, err
	}
	return map[string]any{"ok": true}, nil
}

// handleSetConversationTitle lets a UI rename a conversation (empty convID =
// active).
func handleSetConversationTitle(ctx context.Context, c *client.Client, raw json.RawMessage) (any, error) {
	p, err := decode[setConversationTitleParams](raw)
	if err != nil {
		return nil, err
	}
	convID := p.ConvID
	if convID == "" {
		convID = c.ActiveConversation()
	}
	if err := c.SetConversationTitle(ctx, convID, p.Title); err != nil {
		return nil, err
	}
	return map[string]any{"ok": true}, nil
}
