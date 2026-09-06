// Package chat — app_a2a_debug.go
//
// GetA2ADebug surfaces the App's live A2A integration state for the
// attached control socket (`swarmos swarm attach <handle> debug`). The
// information is read-only and best-effort: fields are sampled at the
// moment of the call without any locking guarantees beyond what each
// individual subsystem provides. This is debug-only — do not build
// product logic on top of it.
package chat

import (
	"context"
	"os"
	"runtime/debug"
	"time"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/a2a"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/headless/automation/protocol"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/version"
)

// GetA2ADebug implements server.A2ADebugger. It samples the App's live A2A
// state and packages it for the control socket caller.
func (a *App) GetA2ADebug() protocol.A2ADebugResponse {
	resp := protocol.A2ADebugResponse{
		ActiveConversationID: a.currentConvID,
		Version:              version.Version,
	}

	// Best-effort: own executable path for cross-checking which binary the
	// running TUI is actually using (this is precisely the kind of question
	// we kept getting wrong while debugging the async DM regression).
	if exe, err := os.Executable(); err == nil {
		resp.BinaryPath = exe
	}

	// Notes accumulate non-fatal diagnostic observations along the way so
	// callers can tell "this field is empty because not configured" apart
	// from "this field is empty because we couldn't reach the data".
	notes := []string{}

	if a.sdk == nil {
		notes = append(notes, "App.sdk is nil")
		resp.Notes = notes
		return resp
	}

	resp.A2AEnabled = a.sdk.A2AEnabled()
	if !resp.A2AEnabled {
		notes = append(notes, "A2A integration is disabled or runtime not initialised")
	}

	// Agent state (idle/executing/...).
	if a.sdk.activeAgent() != nil {
		stats := a.sdk.activeAgent().Stats()
		resp.AgentState = string(stats.State)
	} else {
		notes = append(notes, "SDK agent is nil")
	}

	// Pull the A2A runtime snapshot (handle, session, swarm status, pending
	// queue depth, active task IDs). This is the data that was previously
	// invisible to anything outside the agent process — making it visible
	// is the whole point of this debug surface.
	if a.sdk.activeAgent() != nil {
		if rt := a.sdk.activeAgent().A2ARuntime(); rt != nil {
			snap := rt.Debug()
			resp.Handle = snap.Handle
			resp.SessionID = snap.SessionID
			resp.EndpointURL = snap.EndpointURL
			resp.Workspace = snap.WorkspacePath
			resp.SwarmStatus = snap.SwarmStatus
			resp.SwarmCurrentTask = snap.SwarmCurrentTask
			resp.SwarmModel = snap.SwarmModel
			// Prefer the runtime's view of the active conversation when set —
			// it's what actually routes inbound projections. The App-level
			// currentConvID can drift if the user switches conversations.
			if snap.ActiveConversationID != "" {
				resp.ActiveConversationID = snap.ActiveConversationID
			}
			resp.PendingMessageCount = snap.PendingMessageCount
			resp.ActiveTaskIDs = snap.ActiveTaskIDs
			resp.ActiveTaskCount = len(snap.ActiveTaskIDs)
			// Propagate WS diagnostic counters.
			resp.WSUpgradeAttempts = snap.WSStats.UpgradeAttempts
			resp.WSUpgradeSuccess = snap.WSStats.UpgradeSuccess
			resp.WSUpgradeFailures = snap.WSStats.UpgradeFailures
			resp.WSMessagesReceived = snap.WSStats.MessagesReceived
			resp.WSMessagesDispatched = snap.WSStats.MessagesDispatched
			resp.WSRequestCount = snap.WSStats.RequestMethodCount
			resp.WSSendMessageCalls = snap.WSStats.SendMessageInvocations
			resp.WSSendMessageErrors = snap.WSStats.SendMessageErrors
			resp.WSUnknownTypeCount = snap.WSStats.UnknownTypeCount
			resp.WSActiveConnections = snap.WSStats.ActiveConnections
			resp.WSHasDelegate = snap.WSStats.HasDelegate
			resp.WSHasRequestHandler = snap.WSStats.HasRequestHandler
			resp.WSLastUpgradeError = snap.WSStats.LastUpgradeError
			resp.WSLastReceiveError = snap.WSStats.LastReceiveError
		} else {
			notes = append(notes, "A2A runtime is nil")
		}
	}

	// Discovery: which peers does this process see? Read directly from the
	// filesystem registry rather than the runtime so we still get useful
	// data when A2A is disabled or the runtime is broken.
	if peers, err := a.peerHandles(); err == nil {
		resp.KnownPeerCount = len(peers)
		resp.KnownPeerHandles = peers
	} else {
		notes = append(notes, "discovery listPeers failed: "+err.Error())
	}

	// Inbox count (filesystem). Useful for confirming a DM was at least
	// durably queued even if the WS delivery failed.
	if resp.Handle != "" {
		if msgs, err := a2a.ReadInbox(a2a.DefaultSwarmName, resp.Handle); err == nil {
			resp.InboxCount = len(msgs)
		}
	}

	// Defensive recover so a bad inspector doesn't crash the TUI when
	// called from the control socket.
	defer func() {
		if r := recover(); r != nil {
			notes = append(notes, "panic during snapshot: caller may have stale data")
			notes = append(notes, string(debug.Stack()))
		}
	}()

	resp.Notes = notes
	resp.Timestamp = time.Now().UTC().Format(time.RFC3339Nano)
	return resp
}

// peerHandles returns the handles of all peers currently visible in the
// filesystem registry, excluding self.
func (a *App) peerHandles() ([]string, error) {
	_ = context.Background() // future: SQLite-backed registry will take a ctx
	peers, err := a2a.ListPeers(a2a.DefaultSwarmName)
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(peers))
	selfHandle := ""
	if a.sdk != nil && a.sdk.activeAgent() != nil && a.sdk.activeAgent().A2ARuntime() != nil {
		selfHandle = a.sdk.activeAgent().A2ARuntime().Peer().Handle
	}
	for _, p := range peers {
		if p.Handle == selfHandle {
			continue
		}
		out = append(out, p.Handle)
	}
	return out, nil
}
