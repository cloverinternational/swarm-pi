// Package client — events.go
//
// Themed event bus and event types lifted from session/types.go.
// *client.Client now exposes Subscribe(EventHandler) Unsubscribe alongside
// the lower-level SubscribeUpdates(...) for raw IntermediateUpdate access.
package client

import (
	"context"
	"time"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/agent"
)

// SourceKind identifies where an Event originated.
type SourceKind string

const (
	// SourceLocal is the default: the event came from this client's own agent.
	SourceLocal SourceKind = "local"
	// SourcePeer means the event was relayed from a remote A2A peer.
	SourcePeer SourceKind = "peer"
	// SourceSubAgent means the event came from a delegated sub-agent.
	SourceSubAgent SourceKind = "subagent"
	// SourceBackground means the event came from a background agent managed
	// by BackgroundAgentManager.
	SourceBackground SourceKind = "background"
)

// EventSource carries provenance metadata for every Event.
// Consumers can filter or label events based on which agent or peer produced them.
type EventSource struct {
	// Kind identifies the class of source (local agent, remote peer, sub-agent, etc.).
	Kind SourceKind

	// PeerHandle is the A2A handle of the remote peer when Kind == SourcePeer.
	PeerHandle string

	// AgentID is the sub-agent's unique identifier when Kind == SourceSubAgent.
	AgentID string

	// ConvID is the conversation this event belongs to.
	ConvID string

	// WorkspaceID is an optional label binding this event to a specific workspace
	// or issue (used by the conductor to correlate events with dispatched work).
	WorkspaceID string
}

// EventKind classifies events emitted via Subscribe.
type EventKind string

const (
	// EventAgent wraps an agent.IntermediateUpdate forwarded from the
	// underlying client subscription.  Inspect Event.Agent for the
	// concrete update type (ContentUpdate, ToolCallUpdate, etc.).
	EventAgent EventKind = "agent"

	// EventStreamStart fires when SendMessage begins an agent turn.
	EventStreamStart EventKind = "stream_start"

	// EventStreamEnd fires when an agent turn completes (success or error).
	EventStreamEnd EventKind = "stream_end"

	// EventConvSwitched fires when SwitchConversation succeeds.
	EventConvSwitched EventKind = "conv_switched"

	// EventConvCreated fires when CreateConversation succeeds.
	EventConvCreated EventKind = "conv_created"

	// EventConvDeleted fires when DeleteConversation succeeds.
	EventConvDeleted EventKind = "conv_deleted"

	// EventConvUpdated fires when a conversation's metadata changes (e.g. title)
	// so a thin UI re-renders the conversation list. Payload is a ConvPayload.
	EventConvUpdated EventKind = "conv_updated"

	// EventModelChanged fires when SetModel / SetProvider succeed.
	EventModelChanged EventKind = "model_changed"

	// EventModeChanged fires when SetMode succeeds.
	EventModeChanged EventKind = "mode_changed"

	// EventProfileChanged fires when SwitchProfile / CreateProfile /
	// UpdateProfile / DeleteProfile succeed.  Inspect Payload.Action for
	// the variant (set / created / updated / deleted).
	EventProfileChanged EventKind = "profile_changed"

	// EventAgentChanged fires when CreateAgent / UpdateAgent / DeleteAgent /
	// SetAgent succeed.
	EventAgentChanged EventKind = "agent_changed"

	// EventHookChanged fires when CreateHook / UpdateHook / DeleteHook /
	// ToggleHook succeed.
	EventHookChanged EventKind = "hook_changed"

	// EventToolChanged fires when ToggleTool succeeds.
	EventToolChanged EventKind = "tool_changed"

	// EventSystemPromptChanged fires when SetSystemPrompt succeeds.
	EventSystemPromptChanged EventKind = "system_prompt_changed"

	// EventContextSourceChanged fires when AddContextSource /
	// RemoveContextSource / ToggleContextSource succeed.
	EventContextSourceChanged EventKind = "context_source_changed"

	// EventConfigChanged fires when SetConfig / ToggleCompactMode succeed
	// (i.e. any single-key system config change).
	EventConfigChanged EventKind = "config_changed"

	// EventConfigLoaded fires when LoadConfig succeeds.
	EventConfigLoaded EventKind = "config_loaded"

	// EventConfigSaved fires when SaveConfig succeeds.
	EventConfigSaved EventKind = "config_saved"

	// EventCompaction fires when Compact / CompactDetail completes, so UIs can
	// observe context compaction. Payload is a CompactionPayload.
	EventCompaction EventKind = "compaction"

	// EventThemeChanged fires when SetTheme succeeds.
	EventThemeChanged EventKind = "theme_changed"

	// EventHistoryCleared fires when ClearHistory succeeds.
	EventHistoryCleared EventKind = "history_cleared"

	// EventHistoryResult fires when SearchHistory completes.
	EventHistoryResult EventKind = "history_result"

	// EventApprovalRequested fires when the interactive PermissionChecker needs
	// a remote UI to approve/deny a tool call. The UI replies via
	// RespondApproval(CallID, allow). Payload is an ApprovalRequest.
	EventApprovalRequested EventKind = "approval_requested"

	// EventError fires for any operational error during async dispatch.
	EventError EventKind = "error"

	// ── Peer lifecycle (A2A discovery, emitted by PeerDiscoveryPoller) ──────

	// EventPeerJoined fires when a new peer appears in the swarm.
	// Payload: PeerJoinedPayload
	EventPeerJoined EventKind = "peer_joined"

	// EventPeerLeft fires when a peer disappears from the swarm (PID dead or
	// entry removed).
	// Payload: PeerLeftPayload
	EventPeerLeft EventKind = "peer_left"

	// EventPeerStatus fires when a known peer's status or current_task field
	// changes.
	// Payload: PeerStatusPayload
	EventPeerStatus EventKind = "peer_status"

	// ── Conductor lifecycle (emitted by the Conductor/Orchestrator) ──────────

	// EventTaskDispatched fires when the conductor assigns an issue to a peer.
	// Payload: TaskDispatchedPayload
	EventTaskDispatched EventKind = "task_dispatched"

	// EventTaskCompleted fires when a peer finishes a task successfully.
	// Payload: TaskCompletedPayload
	EventTaskCompleted EventKind = "task_completed"

	// EventTaskFailed fires when a peer task fails after all retries are
	// exhausted.
	// Payload: TaskFailedPayload
	EventTaskFailed EventKind = "task_failed"

	// EventTaskRetrying fires when a failed task is scheduled for retry.
	// Payload: TaskRetryingPayload
	EventTaskRetrying EventKind = "task_retrying"
)

// Event is the unit of output emitted by Client.Subscribe.
//
// For EventAgent, the Agent field carries the underlying
// agent.IntermediateUpdate (ContentUpdate, ToolCallUpdate,
// ToolResultUpdate, ThinkingUpdate, AssistantMessageUpdate,
// TokenCountUpdate, etc.).
//
// For all other event kinds, the Payload field carries an
// EventKind-specific payload struct (see ConvPayload, ModelPayload,
// AgentPayload, HookPayload, etc.).
//
// Source identifies who produced this event — a local agent, a remote A2A
// peer, a background agent, etc. Consumers can filter or route events
// based on Source.Kind and Source.PeerHandle without needing separate
// subscription channels.
type Event struct {
	Kind    EventKind
	Agent   agent.IntermediateUpdate // populated when Kind == EventAgent
	Payload any                      // populated for non-agent events
	At      time.Time
	Source  EventSource // provenance: local, peer, subagent, background
}

// ─── Non-agent payloads ───────────────────────────────────────────────────────

// ConvPayload describes a conversation lifecycle change.
type ConvPayload struct {
	ConvID string `json:"convId"`
}

// ModelPayload describes a model/provider change.
type ModelPayload struct {
	Provider      string `json:"provider"`
	Model         string `json:"model"`
	ContextWindow int    `json:"contextWindow"`
}

// ModePayload describes an operating-mode change.
type ModePayload struct {
	Mode string `json:"mode"`
}

// CompactionPayload describes a completed compaction (EventCompaction).
type CompactionPayload struct {
	ConvID       string `json:"convId"`
	BeforeTokens int    `json:"beforeTokens"`
	AfterTokens  int    `json:"afterTokens"`
	NewConvID    string `json:"newConvId,omitempty"`
}

// ProfilePayload describes a profile change.  Action distinguishes the
// variant emitted by SwitchProfile ("set"), CreateProfile ("created"),
// UpdateProfile ("updated"), and DeleteProfile ("deleted").
type ProfilePayload struct {
	ProfileID   string `json:"profileId"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Action      string `json:"action"` // "set" | "created" | "updated" | "deleted"
}

// AgentPayload describes an agent lifecycle change.  Action is one of
// "set", "created", "updated", or "deleted".
type AgentPayload struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Model       string `json:"model,omitempty"`
	Provider    string `json:"provider,omitempty"`
	Profile     string `json:"profile,omitempty"`
	IsDefault   bool   `json:"isDefault,omitempty"`
	Action      string `json:"action"`
}

// HookPayload describes a hook lifecycle change.  Action is one of
// "created", "updated", "deleted", or "toggled".
type HookPayload struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Event       string `json:"event,omitempty"`
	ToolMatch   string `json:"toolMatch,omitempty"`
	Phase       string `json:"phase,omitempty"`
	Enabled     bool   `json:"enabled"`
	CanBlock    bool   `json:"canBlock,omitempty"`
	Action      string `json:"action"`
}

// ToolPayload describes a tool toggle.
type ToolPayload struct {
	Name    string `json:"name"`
	Enabled bool   `json:"enabled"`
}

// SystemPromptPayload describes a system prompt change.
type SystemPromptPayload struct {
	Name    string `json:"name"`
	Content string `json:"content,omitempty"`
}

// ContextSourcePayload describes a context-source change.  Action is one
// of "added", "removed", or "toggled".
type ContextSourcePayload struct {
	Name    string `json:"name"`
	Type    string `json:"type,omitempty"`
	Enabled bool   `json:"enabled"`
	Action  string `json:"action"`
}

// ConfigPayload describes a single-field system-config change.
type ConfigPayload struct {
	Key      string `json:"key"`
	OldValue any    `json:"oldValue,omitempty"`
	NewValue any    `json:"newValue,omitempty"`
}

// ThemePayload describes a theme change.
type ThemePayload struct {
	Theme string `json:"theme"`
}

// HistoryResultPayload reports SearchHistory results.
type HistoryResultPayload struct {
	Query   string                `json:"query"`
	Results []ConversationSummary `json:"results"`
	Total   int                   `json:"total"`
}

// ErrorPayload describes a runtime error.
type ErrorPayload struct {
	Err     error  `json:"-"`
	Message string `json:"message"`
}

// ── Peer lifecycle payloads ───────────────────────────────────────────────────

// PeerJoinedPayload carries the presence data for a newly discovered peer.
type PeerJoinedPayload struct {
	Handle      string `json:"handle"`
	EndpointURL string `json:"endpoint_url"`
	Workspace   string `json:"workspace,omitempty"`
	Model       string `json:"model,omitempty"`
	PID         int    `json:"pid,omitempty"`
}

// PeerLeftPayload carries the handle of a peer that has gone away.
type PeerLeftPayload struct {
	Handle string `json:"handle"`
}

// PeerStatusPayload carries an updated status snapshot for a known peer.
type PeerStatusPayload struct {
	Handle      string `json:"handle"`
	Status      string `json:"status"`       // "idle", "working", "busy", "away"
	CurrentTask string `json:"current_task"` // human-readable description
	Workspace   string `json:"workspace,omitempty"`
}

// ── Conductor lifecycle payloads ─────────────────────────────────────────────

// TaskDispatchedPayload is emitted when the conductor assigns an issue to a peer.
type TaskDispatchedPayload struct {
	IssueID     string `json:"issue_id"`
	Identifier  string `json:"identifier"` // human-readable key e.g. "SWRM-42"
	PeerHandle  string `json:"peer_handle"`
	WorkspaceID string `json:"workspace_id,omitempty"`
	Attempt     int    `json:"attempt"` // 0 = first run, 1+ = retry
}

// TaskCompletedPayload is emitted when a peer finishes a task successfully.
type TaskCompletedPayload struct {
	IssueID    string `json:"issue_id"`
	Identifier string `json:"identifier"`
	PeerHandle string `json:"peer_handle"`
}

// TaskFailedPayload is emitted when a task fails after all retries.
type TaskFailedPayload struct {
	IssueID    string `json:"issue_id"`
	Identifier string `json:"identifier"`
	PeerHandle string `json:"peer_handle"`
	Error      string `json:"error"`
	Attempts   int    `json:"attempts"`
}

// TaskRetryingPayload is emitted when a failed task is queued for retry.
type TaskRetryingPayload struct {
	IssueID    string `json:"issue_id"`
	Identifier string `json:"identifier"`
	Attempt    int    `json:"attempt"`
	DelayMs    int64  `json:"delay_ms"`
	Error      string `json:"error"`
}

// ─── Subscription handler types ──────────────────────────────────────────────

// EventHandler receives session-level events for a single subscription.
// Handlers are invoked sequentially on the dispatching goroutine; long-
// running handlers should buffer or hand off to a worker to avoid
// back-pressuring the agent stream.  Returning a non-nil error does not
// unsubscribe; it is logged and otherwise ignored.
type EventHandler func(Event) error

// Unsubscribe removes a subscriber.  Safe to call from within a handler
// and idempotent.
type Unsubscribe func()

// ─── Themed Subscribe ────────────────────────────────────────────────────────

// Subscribe registers handler to receive every Event emitted by the
// client (mode/model/agent/profile/hook/tool/context-source/config
// changes plus EventAgent for raw agent updates and
// EventStreamStart/End for turn boundaries).
//
// Multiple subscribers are permitted; Subscribe returns an idempotent
// unsubscribe func.
//
// For lower-level access to raw agent.IntermediateUpdate without the
// session-level wrapping, use SubscribeUpdates instead.
func (c *Client) Subscribe(handler EventHandler) Unsubscribe {
	if handler == nil {
		return func() {}
	}
	id := c.nextEventSubID.Add(1)
	c.eventSubMu.Lock()
	if c.eventSubs == nil {
		c.eventSubs = make(map[uint64]EventHandler)
	}
	c.eventSubs[id] = handler
	c.eventSubMu.Unlock()
	return func() {
		c.eventSubMu.Lock()
		delete(c.eventSubs, id)
		c.eventSubMu.Unlock()
	}
}

// dispatchEvent fans out ev to every registered EventHandler.  Errors
// from handlers are intentionally ignored — they must not block fan-out
// to other subscribers.
func (c *Client) dispatchEvent(ev Event) {
	if c.sessStopped.Load() {
		return
	}
	c.eventSubMu.RLock()
	if len(c.eventSubs) == 0 {
		c.eventSubMu.RUnlock()
		return
	}
	handlers := make([]EventHandler, 0, len(c.eventSubs))
	for _, h := range c.eventSubs {
		handlers = append(handlers, h)
	}
	c.eventSubMu.RUnlock()
	for _, h := range handlers {
		_ = h(ev)
	}
}

// applyAgentUpdate taps off TokenCountUpdate so Snapshot() reflects
// per-turn token counts even when no caller subscribes.
func (c *Client) applyAgentUpdate(u agent.IntermediateUpdate) {
	if tc, ok := u.(agent.TokenCountUpdate); ok {
		c.stateMu.Lock()
		c.sessState.InputTokens = tc.InputTokens
		c.sessState.OutputTokens = tc.OutputTokens
		c.sessState.TotalTokens = tc.InputTokens + tc.OutputTokens
		c.sessState.CurrentContext = tc.InputTokens
		c.sessState.UpdatedAt = time.Now()
		c.stateMu.Unlock()
	}
}

// startAgentBridge wires the existing low-level subscriber pipeline
// into the themed-event dispatcher.  Idempotent; subsequent calls do
// nothing.
//
// Called from Start().  The unsubscribe func is held in
// c.sessAgentUnsub so Stop() can detach it.
func (c *Client) startAgentBridge() {
	c.sessStartOnce.Do(func() {
		stopCtx, stopCanc := context.WithCancel(context.Background())
		c.sessStopCtx = stopCtx
		c.sessStopCanc = stopCanc

		c.sessAgentUnsub = c.SubscribeUpdates(func(ctx context.Context, u agent.IntermediateUpdate) error {
			c.applyAgentUpdate(u)
			// Preserve EventSource from context — set by InjectUpdate for remote
			// sources (peers, background agents). Falls back to SourceLocal so
			// every event always has a valid, non-zero Source.
			src := sourceFromContext(ctx)
			// Sub-agent activity (forwarded by the Subagent tool as SubAgentUpdate)
			// is tagged so a UI can route it to a per-sub-agent window.
			if sa, ok := u.(agent.SubAgentUpdate); ok {
				src.Kind = SourceSubAgent
				src.AgentID = sa.AgentID
			} else if src.Kind == "" {
				src.Kind = SourceLocal
			}
			// Stamp the conversation id on local main-turn events when the
			// context/remote source didn't already carry one. Multiplexing
			// clients (e.g. a remote engine driving the daemon for several
			// conversations) need this to filter the /sse stream reliably;
			// previously local events omitted convID entirely. Additive: a
			// non-empty ConvID from a remote source is preserved.
			if src.ConvID == "" {
				c.stateMu.RLock()
				src.ConvID = c.sessState.ActiveConvID
				c.stateMu.RUnlock()
			}
			c.dispatchEvent(Event{Kind: EventAgent, Agent: u, At: time.Now(), Source: src})
			return nil
		})

		// Persist generated messages to the active conversation so getMessages /
		// a reattaching UI sees the transcript (not just the live stream).
		c.wireMessagePersistence()
	})
}

// stopAgentBridge tears down the themed-event bridge.  Idempotent.
func (c *Client) stopAgentBridge() {
	c.sessStopOnce.Do(func() {
		c.sessStopped.Store(true)

		// Cancel any active turn.
		c.sessExecMu.Lock()
		if c.sessExecCancel != nil {
			c.sessExecCancel()
			c.sessExecCancel = nil
		}
		c.sessExecMu.Unlock()

		if c.sessAgentUnsub != nil {
			c.sessAgentUnsub()
			c.sessAgentUnsub = nil
		}
		if c.sessStopCanc != nil {
			c.sessStopCanc()
		}
	})
}
