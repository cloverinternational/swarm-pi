package chat

import (
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/tools/ii"
)

func isClearSlashCommand(input string) bool {
	fields := strings.Fields(strings.TrimSpace(input))
	if len(fields) == 0 {
		return false
	}
	return fields[0] == "/clear"
}

// handleClearConversation implements /clear: cancel the current conversation's
// active turn (if any) and start a clean new chat without restarting Swarm.
func (a *App) handleClearConversation() (tea.Model, tea.Cmd) {
	oldConvID := a.currentConvID
	oldActiveID := ""
	if a.activeConv != nil {
		oldActiveID = a.activeConv.ID
	}

	logDebug("[clear] Clearing conversation: currentConvID=%s activeConvID=%s", oldConvID, oldActiveID)

	// Persist per-conversation task state before switching away. The todo manager
	// is a singleton, so leaving it unsaved risks bleeding or losing tasks.
	if oldConvID != "" && a.sdk != nil {
		if err := a.sdk.SaveTasksForConversation(oldConvID); err != nil {
			logDebug("[clear] Failed to save tasks for conv %s: %v", oldConvID, err)
		} else {
			logDebug("[clear] Saved tasks for conv %s", oldConvID)
		}
	}

	// Cancel the active local agent/turn per the product decision. Unsubscribe the
	// UI as well so buffered updates from the old conversation do not repaint the
	// new chat.
	if oldConvID != "" && a.bgManager != nil {
		if a.bgManager.IsRunning(oldConvID) {
			logDebug("[clear] Cancelling running agent for conv %s", oldConvID)
			a.bgManager.Cancel(oldConvID)
		}
		a.bgManager.Unsubscribe(oldConvID, "ui")
	}

	// If the session is attached to a daemon, /clear is local: detach the SSE stream
	// so remote events cannot continue writing into the fresh local chat. This does
	// not kill the daemon process.
	if a.sseState != nil {
		addr := a.sseState.addr
		a.sseState.cancel()
		a.sseState = nil
		logDebug("[clear] Detached SSE session from %s", addr)
	}

	// Drop any queued user messages intended for the old turn before creating the
	// new conversation.
	a.pendingMsgMu.Lock()
	a.pendingUserMessages = nil
	a.pendingMsgMu.Unlock()
	a.pendingNavIdx = 0

	// Clear interactive overlays/commands tied to the old chat.
	a.activeCommand = nil
	a.activeModal = nil
	a.taskActivityModal = nil
	a.detailViewer = nil

	// Stop transient activity indicators now; startNewChatDirect/resetChatUIState
	// repeats this safely, but doing it before the switch makes cancellation visible
	// immediately and closes the race with late stream ticks.
	a.streamingMessage = false
	a.streamingInProgress = false
	if a.loadingIndicator != nil {
		a.loadingIndicator.Stop(a.animationClock)
	}
	if a.spinner != nil {
		a.spinner.Stop(a.animationClock)
	}

	// Clear singleton task state so the fresh conversation starts with no inherited
	// tasks. When an existing conversation is opened later, openConversation will
	// restore its saved tasks via SetupTaskPersistence.
	if tm := ii.GetTodoManager(); tm != nil {
		tm.ClearTodos()
	}

	// Reuse the existing new-chat path so branch detection, workspace pane updates,
	// context-window refresh, input clearing, and SDK active-conversation syncing
	// stay consistent with the rest of the TUI.
	a.startNewChatDirect()

	// startNewChatDirect launches naming/branch helper goroutines that wait for the
	// first persisted SDK conversation. /clear itself must not create an empty
	// persisted conversation; the first new user message will do that.
	a.addNotification("info", "Conversation cleared — new chat started")

	return a, forceRepaint()
}
