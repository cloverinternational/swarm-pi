package chat

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/tools"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/chat/commands"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/chat/settings"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/i18n"
)

// isWorkspaceCompatible checks if two workspace paths are compatible.
// Returns true only for exact match — conversations from parent or child directories are excluded.
func isWorkspaceCompatible(convWorkspace, currentWorkspace string) bool {
	// Normalize paths to handle trailing slashes
	convWorkspace = filepath.Clean(convWorkspace)
	currentWorkspace = filepath.Clean(currentWorkspace)

	// Exact match only — no parent/child directory leaking
	return convWorkspace == currentWorkspace
}

// saveCurrentChatState saves the current chat's input before switching away
func (a *App) saveCurrentChatState() {
	if a.activeConv != nil {
		// Save input buffer
		a.activeConv.InputBuffer = a.textInput.Value()
		logDebug("Saved chat state for conv %s: input='%s'", a.activeConv.ID, a.activeConv.InputBuffer)
	}
}

// loadChatState loads a conversation's state when switching to it
func (a *App) loadChatState(conv *Conversation) {
	if conv == nil {
		return
	}

	// Restore input buffer
	a.textInput.SetValue(conv.InputBuffer)
	// Clear undo/redo history so the user cannot Ctrl+Z back into a
	// different conversation's text (each conversation gets a clean slate).
	a.textInput.ClearUndoHistory()
	// Always reset history navigation index so the user starts fresh when
	// they return to a conversation — avoids "skipping" entries that were
	// browsed during the previous visit without submitting.
	a.inputHistory.Reset()
	a.pendingNavIdx = 0 // reset pending-message navigation cursor
	logDebug("Loaded chat state for conv %s: input='%s'", conv.ID, conv.InputBuffer)
}

// clearInputForNewChat clears input when starting a new chat
func (a *App) clearInputForNewChat() {
	a.textInput.SetValue("")
	a.textInput.ClearUndoHistory() // Fresh slate — no undo history from prior conv
	a.inputHistory.Reset()         // Reset navigation position for clean start
	logDebug("Cleared input for new chat")
}

// resetChatUIState resets all UI state for a clean new conversation.
// This MUST be called when starting a new chat to prevent old conversation
// state from bleeding into the new one (especially streaming/compaction state).
func (a *App) resetChatUIState() {
	logDebug("[resetChatUIState] Resetting all UI state for new conversation")

	// Clear streaming state - critical for preventing false compaction triggers
	a.streamingMessage = false
	a.streamingInProgress = false
	a.streamBuffer = ""
	a.streamingInputTokens = 0
	a.streamingOutputChars = 0
	a.setUserScrolledAway(false, "reset_chat_ui_state")

	// Stop all animation/loading indicators
	if a.animationClock != nil {
		a.animationClock.SetStreaming(false)
	}
	if a.loadingIndicator != nil {
		a.loadingIndicator.Stop(a.animationClock)
	}
	if a.spinner != nil {
		a.spinner.Stop(a.animationClock)
	}

	// Clear compaction state
	a.isCompacting = false

	// Clear messages array (will be fresh for new conversation)
	a.messages = []Message{}

	// Reset message tracking
	a.messageSequence = 0

	// Reset token tracking
	a.tokenCount = 0
	a.tokenCountIsEstimate = false
	a.lastRealTokenCount = 0

	// CRITICAL: Reset sidebar/preview cache state to prevent stale data from persisting
	// These caches can hold data from the previously active conversation
	a.cachedPreviewID = ""                     // Clear preview message cache
	a.cachedPreviewMsgs = nil                  // Clear preview messages list
	a.collapsedParents = make(map[string]bool) // Reset fork/compaction collapsed state
	a.scrollOffset = 0                         // Reset sidebar scroll position
	a.selectedIdx = 0                          // Reset sidebar selection index

	// Clear any active modal
	a.activeModal = nil
	a.taskActivityModal = nil
	a.detailViewer = nil

	// Update viewport to show empty state
	if a.msgViewport != nil {
		a.updateViewportContent()
		a.msgViewport.GotoBottom()
	}

	logDebug("[resetChatUIState] UI state reset complete")

	// Clear streaming state - critical for preventing false compaction triggers
	a.streamingMessage = false
	a.streamingInProgress = false
	a.streamBuffer = ""
	a.streamingInputTokens = 0
	a.streamingOutputChars = 0
	a.setUserScrolledAway(false, "reset_chat_ui_state_dup")

	// Stop all animation/loading indicators
	if a.animationClock != nil {
		a.animationClock.SetStreaming(false)
	}
	if a.loadingIndicator != nil {
		a.loadingIndicator.Stop(a.animationClock)
	}
	if a.spinner != nil {
		a.spinner.Stop(a.animationClock)
	}

	// Clear compaction state
	a.isCompacting = false

	// Clear messages array (will be fresh for new conversation)
	a.messages = []Message{}

	// Reset message tracking
	a.messageSequence = 0

	// Reset token tracking
	a.tokenCount = 0
	a.tokenCountIsEstimate = false
	a.lastRealTokenCount = 0

	// Clear any active modal
	a.activeModal = nil
	a.taskActivityModal = nil
	a.detailViewer = nil

	// Update viewport to show empty state
	if a.msgViewport != nil {
		a.updateViewportContent()
		a.msgViewport.GotoBottom()
	}

	logDebug("[resetChatUIState] UI state reset complete")
}

// stopAgent stops the currently running agent/LLM response
func (a *App) stopAgent() {
	if !a.streamingMessage {
		logDebug("[stopAgent] No streaming message, nothing to stop")
		return
	}

	logDebug("[stopAgent] Stopping agent for convID=%s", a.currentConvID)

	// Cancel the background agent
	if a.currentConvID != "" {
		a.bgManager.Cancel(a.currentConvID)
	}

	// Stop UI indicators - unsubscribe from shared animation clock
	a.loadingIndicator.Stop(a.animationClock)
	a.spinner.Stop(a.animationClock)
	a.streamingMessage = false

	// Update conversation status through the centralized activity state.
	a.setActivityPhase(ActivityPhaseIdle, "")

	// Notify user
	a.addNotification("info", i18n.T("classic_chat.agent.stopped"))

	// Refresh display
	a.updateViewportContent()

	logDebug("[stopAgent] Agent stopped successfully")
}

// showEscapeMenu displays the escape menu with edit message and leave options
func (a *App) showEscapeMenu() {
	if a.activeConv == nil {
		logDebug("[showEscapeMenu] No active conversation, returning")
		return
	}

	logDebug("[showEscapeMenu] Checking if agent is active for convID=%s", a.currentConvID)

	// Check if agent is actually running in background manager
	isActive := false
	if a.bgManager != nil && a.currentConvID != "" {
		isActive = a.bgManager.IsRunning(a.currentConvID)
		logDebug("[showEscapeMenu] bgManager.IsRunning(%s) = %v", a.currentConvID, isActive)
	}
	if !isActive {
		// Also check UI state for backwards compatibility
		isActive = a.activeConv.Status == "thinking" ||
			a.activeConv.Status == "waiting" ||
			a.activeConv.Status == "streaming"
		logDebug("[showEscapeMenu] UI status check: status=%s, isActive=%v", a.activeConv.Status, isActive)
	}

	// Check if there are user messages to edit
	hasUserMessages := false
	for _, msg := range a.messages {
		if msg.Role == "user" {
			hasUserMessages = true
			break
		}
	}

	// Build options dynamically based on state
	type menuAction string
	const (
		actionCancel      menuAction = "cancel"
		actionEditMessage menuAction = "edit"
		actionStop        menuAction = "stop"
		actionLeave       menuAction = "leave"
	)

	var options []string
	var actions []menuAction

	options = append(options, i18n.T("classic_chat.common.cancel"))
	actions = append(actions, actionCancel)

	if hasUserMessages && !a.streamingMessage {
		options = append(options, i18n.T("classic_chat.menu.edit_message"))
		actions = append(actions, actionEditMessage)
	}

	if isActive {
		options = append(options, i18n.T("classic_chat.common.stop"))
		actions = append(actions, actionStop)
	}

	options = append(options, i18n.T("classic_chat.common.leave"))
	actions = append(actions, actionLeave)

	a.activeModal = &Modal{
		Type:     ModalEscapeMenu,
		Title:    i18n.T("classic_chat.menu.title"),
		Message:  i18n.T("classic_chat.menu.message"),
		Options:  options,
		Selected: 0,
		OnSelect: func(option int) {
			if option < 0 || option >= len(actions) {
				a.activeModal = nil
				return
			}

			switch actions[option] {
			case actionCancel:
				a.activeModal = nil
				logDebug("[showEscapeMenu] Cancelled")

			case actionEditMessage:
				a.activeModal = nil
				a.enterEditMessageMode()

			case actionStop:
				a.activeModal = nil
				a.stopAgent()
				logDebug("[showEscapeMenu] Stopped agent immediately")

			case actionLeave:
				if isActive {
					// Agent is running — ask whether to stop or leave in background
					a.showLeaveConfirmModal()
				} else {
					a.exitCurrentChat(false)
				}
			}
		},
	}
}

// showTelemetryConsentModal shows the one-time telemetry opt-in consent dialog.
// On Accept it persists the opt-in, marks consent acknowledged, and exports
// SWARM_ANALYTICS_ENABLED=1 so the setting takes effect for this session (the
// user must still supply their own collector URL + token). On Decline it
// records that consent was answered without enabling telemetry.
func (a *App) showTelemetryConsentModal() {
	a.activeModal = NewTelemetryConsentModal(func(option int) {
		a.activeModal = nil
		if a.settingsManager == nil {
			return
		}
		gen := a.settingsManager.GetGeneralSettings()
		if gen == nil {
			return
		}
		if option == 1 { // Accept
			gen.SetTelemetryEnabled(true)
			// Apply for the current session so it takes effect without restart.
			// DefaultManager() is memoized via sync.Once, so this only matters
			// if analytics has not yet initialized; harmless otherwise.
			_ = os.Setenv("SWARM_ANALYTICS_ENABLED", "1")
			a.addNotification("info", i18n.T("classic_chat.telemetry.enabled"))
		} else { // Decline (or esc)
			gen.SetTelemetryEnabled(false)
			_ = os.Setenv("SWARM_ANALYTICS_ENABLED", "0")
		}
	})
}

// showLeaveConfirmModal asks whether to stop the agent or leave it running in background
func (a *App) showLeaveConfirmModal() {
	a.activeModal = &Modal{
		Type:    ModalExitChat,
		Title:   i18n.T("classic_chat.leave.title"),
		Message: i18n.T("classic_chat.leave.message"),
		Options: []string{
			i18n.T("classic_chat.common.cancel"),
			i18n.T("classic_chat.leave.stop"),
			i18n.T("classic_chat.leave.running"),
		},
		Selected: 0,
		OnSelect: func(option int) {
			switch option {
			case 0: // Cancel — back to chat
				a.activeModal = nil
			case 1: // Stop & Leave
				if a.currentConvID != "" && a.bgManager != nil {
					a.bgManager.Cancel(a.currentConvID)
					a.setActivityPhase(ActivityPhaseIdle, "")
					logDebug("[showLeaveConfirmModal] Stopped agent for %s", a.currentConvID)
				}
				a.exitCurrentChat(false)
			case 2: // Leave Running
				if a.activeConv != nil {
					a.activeConv.ContinueInBg = true
				}
				logDebug("[showLeaveConfirmModal] Leaving agent running in background")
				a.exitCurrentChat(true)
			}
		},
	}
}

// exitCurrentChat navigates away from current chat
// If continueInBackground is true, the agent keeps running
// Returns a tea.Cmd for async operations (conversation loading)
func (a *App) exitCurrentChat(continueInBackground bool) tea.Cmd {
	logDebug("[exitCurrentChat] Exiting conversation %s (continueInBackground=%v)", a.currentConvID, continueInBackground)

	// CRITICAL: Save tasks BEFORE leaving the conversation
	// The TodoManager is a global singleton. If we don't save now,
	// tasks will be lost when the next conversation sets up its own syncer.
	if a.currentConvID != "" && a.sdk != nil {
		if err := a.sdk.SaveTasksForConversation(a.currentConvID); err != nil {
			logDebug("[exitCurrentChat] Failed to save tasks for %s: %v", a.currentConvID, err)
		} else {
			logDebug("[exitCurrentChat] Saved tasks for conversation %s", a.currentConvID)
		}
	}

	// Save state before leaving
	a.saveCurrentChatState()

	// Unsubscribe from updates but don't cancel the agent if continuing in background
	if a.currentConvID != "" && a.bgManager != nil {
		logDebug("[exitCurrentChat] Unsubscribing from updates for %s", a.currentConvID)
		a.bgManager.Unsubscribe(a.currentConvID, "ui")

		if !continueInBackground {
			logDebug("[exitCurrentChat] Cancelling agent for %s", a.currentConvID)
			a.bgManager.Cancel(a.currentConvID)
		} else {
			logDebug("[exitCurrentChat] Leaving agent running in background for %s", a.currentConvID)
		}
	}

	// Stop loading indicator - unsubscribe from shared animation clock
	a.loadingIndicator.Stop(a.animationClock)
	a.streamingMessage = false

	// Navigate back
	a.screen = ScreenChats
	a.activeModal = nil
	a.activeConv = nil
	a.messages = nil
	// Don't clear currentConvID - might be needed for reference

	// Reload conversations async to avoid blocking UI with thousands of conversations
	logDebug("[exitCurrentChat] Starting async conversation reload")
	return a.loadConversationsAsync()
}

// showNewChatModal displays the new chat modal with branch selection
func (a *App) showNewChatModal() {
	// Create new chat modal with branch selection
	a.newChatModal = NewNewChatModal(
		a.gitHelper,
		func(prompt string, branch string, createNew bool, modeID string) {
			// Close modal
			a.newChatModal = nil
			a.activeModal = nil

			// Set the operating mode if specified
			if modeID != "" && modeID != "off" && a.sdk != nil {
				a.sdk.SetOperatingMode(modeID)
				a.operatingMode = modeID
			}

			// Start new chat with branch context
			a.startNewChatWithBranch(prompt, branch, createNew)

			// Auto-submit if there's a prompt
			if prompt != "" {
				a.pendingAutoSubmit = true
			}
		},
		func() {
			// Cancel - just close modal
			a.newChatModal = nil
			a.activeModal = nil
		},
	)

	// Mark modal as active
	a.activeModal = &Modal{Type: ModalNewChat}
}

// handleNewChatUIKey forwards key events to the new chat UI screen
func (a *App) handleNewChatUIKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if a.chatScreen == nil {
		return a, nil
	}

	// Forward the key event to the chat screen
	_, cmd := a.chatScreen.Update(msg)
	return a, cmd
}

// startNewChatWithBranch starts a new conversation with optional branch association
func (a *App) startNewChatWithBranch(initialPrompt string, branch string, createBranch bool) {
	// Save current chat state if any
	a.saveCurrentChatState()

	// Handle git branch creation if requested
	if createBranch && branch != "" && a.gitHelper != nil && a.gitHelper.IsRepo() {
		err := a.gitHelper.CreateAndCheckout(branch)
		if err != nil {
			logDebug("[startNewChatWithBranch] Failed to create branch %s: %v", branch, err)
			a.addNotification("error", i18n.T("classic_chat.branch.create_failed", err))
			branch = "" // Continue without branch on failure
		} else {
			logDebug("[startNewChatWithBranch] Created and checked out branch: %s", branch)
			a.addNotification("success", i18n.T("classic_chat.branch.created", branch))
		}
	}

	// CRITICAL: Reset all UI state to prevent old conversation bleeding into new one
	// This clears streaming state, messages, tokens, and animations
	a.resetChatUIState()

	// Create new conversation with branch
	a.activeConv = &Conversation{
		ID:           time.Now().Format("20060102150405"),
		Title:        i18n.T("classic_chat.chat.new"),
		Status:       "idle",
		IsActive:     false,
		InputBuffer:  "", // Start with empty input
		ContinueInBg: false,
		Branch:       branch, // Associate branch
	}
	a.screen = ScreenChat
	a.clearCurrentConversationID() // Clear SDK conversation ID to create new one

	// Refresh context window from current model config
	if a.sdk != nil {
		a.modelContextWindow = a.sdk.GetModelContextWindow()
	}

	// Clear input for new chat, then set initial prompt if provided
	a.clearInputForNewChat()
	if initialPrompt != "" {
		a.textInput.SetValue(initialPrompt)
	}

	// Log to debug screen

	// If in workspace mode, update chat pane
	if a.workspaceMode {
		panes := a.getAllPanes()
		for _, p := range panes {
			if p.Type == PaneChat {
				p.ConvID = a.activeConv.ID
				p.Focused = true
				a.focusedPane = p
				// Unfocus others
				for _, other := range panes {
					if other != p {
						other.Focused = false
					}
				}
				break
			}
		}
	}
}

// startNewChatDirect immediately drops the user into a new chat without any modal.
// Background agents handle naming and branch creation asynchronously.
func (a *App) startNewChatDirect() {
	// Save current chat state if any
	a.saveCurrentChatState()

	// CRITICAL: Reset all UI state to prevent old conversation bleeding into new one
	// This clears streaming state, messages, tokens, and animations
	a.resetChatUIState()

	// Auto-detect current git branch so conversations are properly grouped in sidebar
	currentBranch := ""
	if a.gitHelper != nil && a.gitHelper.IsRepo() {
		currentBranch, _ = a.gitHelper.CurrentBranch()
	}

	// Create new conversation immediately
	a.activeConv = &Conversation{
		ID:           time.Now().Format("20060102150405"),
		Title:        i18n.T("classic_chat.chat.new"), // Will be auto-named by background agent
		Status:       "idle",
		IsActive:     false,
		InputBuffer:  "",
		ContinueInBg: false,
		Branch:       currentBranch, // Auto-detect current git branch
	}
	a.screen = ScreenChat
	a.clearCurrentConversationID() // Clear SDK conversation ID to create new one
	a.branchPromptAsked = false    // Reset branch prompt flag for new conversation

	// Refresh context window from current model config
	if a.sdk != nil {
		a.modelContextWindow = a.sdk.GetModelContextWindow()
	}

	// Clear input for new chat
	a.clearInputForNewChat()

	// Log to debug screen

	// If in workspace mode, update chat pane
	if a.workspaceMode {
		panes := a.getAllPanes()
		for _, p := range panes {
			if p.Type == PaneChat {
				p.ConvID = a.activeConv.ID
				p.Focused = true
				a.focusedPane = p
				// Unfocus others
				for _, other := range panes {
					if other != p {
						other.Focused = false
					}
				}
				break
			}
		}
	}

	// Conversation title/recap generation is owned by the shared SDK client and
	// runs against the exact persisted conversation ID after every turn.
	go a.smartAskBranchCreationAsync()
}

func mcpLog(format string, args ...any) {
	f, err := os.OpenFile("/tmp/swarm_mcp_debug.log", os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return
	}
	defer f.Close()
	msg := fmt.Sprintf(format, args...)
	fmt.Fprintf(f, "[%s] %s\n", time.Now().Format("15:04:05.000"), msg)
}

func (a *App) updateMCPSettingsData() {
	if a.settingsManager == nil || a.sdk == nil {
		mcpLog("[updateMCPSettingsData] early return: settingsManager_nil=%v sdk_nil=%v", a.settingsManager == nil, a.sdk == nil)
		return
	}

	mcpLog("[updateMCPSettingsData] called: sdk.toolRegistry_nil=%v", a.sdk.toolRegistry == nil)

	// Load disabled builtin tools from config.
	// DisabledBuiltinTools lives on commands.SwarmOSConfig (TUI-specific extension),
	// not core.Config — load via the legacy ConfigManager.
	disabledBuiltinTools := make(map[string]bool)
	if configMgr, err := commands.NewConfigManager(); err == nil {
		if cfg, err := configMgr.LoadConfig(); err == nil && cfg != nil && cfg.DisabledBuiltinTools != nil {
			disabledBuiltinTools = cfg.DisabledBuiltinTools
			// Apply disabled state to registry
			if a.sdk.toolRegistry != nil {
				if simpleReg, ok := a.sdk.toolRegistry.(*tools.SimpleRegistry); ok {
					for toolName, disabled := range disabledBuiltinTools {
						if disabled {
							simpleReg.DisableTool(toolName)
						}
					}
				}
			}
		}
	}

	// Define which tools come from II package vs SDK builtin
	iiToolsMap := map[string]bool{
		"TodoRead": true, "TodoWrite": true,
		"TaskManage": true,
		"Read":       true, "Write": true, "Edit": true,
		"ReadLegacy":  true,
		"apply_patch": true, "str_replace": true, "file_undo": true, "Grep": true,
		"semantic_grep":   true,
		"save_checkpoint": true, "fullstack_project_init": true,
		"register_deployment": true,
	}

	toolCategoriesMap := map[string]string{
		"TodoRead": "productivity", "TodoWrite": "productivity",
		"TaskManage":    "productivity",
		"HistorySearch": "memory", "HistoryGet": "memory",
		"Read": "file", "Write": "file", "Edit": "file", "Grep": "file", "apply_patch": "file", "str_replace": "file", "file_undo": "file",
		"ReadLegacy":    "file",
		"semantic_grep": "file",
		"file_read":     "file", "file_write": "file", "grep": "file",
		"bash":            "shell",
		"save_checkpoint": "dev", "fullstack_project_init": "dev", "register_deployment": "dev",
		"Task": "agent", "BackgroundTask": "agent", "TaskOutput": "agent",
	}

	// Get builtin tools from tool registry (including disabled ones)
	var allTools []settings.ToolInfo
	if a.sdk.toolRegistry != nil {
		// Use ListAll to get all tools including disabled ones
		var toolNames []string
		if simpleReg, ok := a.sdk.toolRegistry.(*tools.SimpleRegistry); ok {
			toolNames = simpleReg.ListAll()
			mcpLog("[updateMCPSettingsData] SimpleRegistry.ListAll() returned %d tools: %v", len(toolNames), toolNames)
		} else {
			toolNames = a.sdk.toolRegistry.List()
			mcpLog("[updateMCPSettingsData] registry.List() (non-SimpleRegistry) returned %d tools: %v", len(toolNames), toolNames)
		}

		for _, toolName := range toolNames {
			// Only include actual builtin tools (not MCP wrappers)
			if !strings.HasPrefix(toolName, "mcp_") {
				// Get tool including disabled ones
				var tool tools.Tool
				var isEnabled bool
				if simpleReg, ok := a.sdk.toolRegistry.(*tools.SimpleRegistry); ok {
					var err error
					tool, isEnabled, err = simpleReg.IncludingDisabled(toolName)
					if err != nil {
						continue
					}
					if !isEnabled {
						disabledBuiltinTools[toolName] = true
					}
				} else {
					var err error
					tool, err = a.sdk.toolRegistry.Get(toolName)
					if err != nil {
						continue
					}
					isEnabled = true
				}

				// Determine source
				source := "sdk"
				if iiToolsMap[toolName] {
					source = "ii"
				}

				// Get category
				category := toolCategoriesMap[toolName]

				allTools = append(allTools, settings.ToolInfo{
					Name:        toolName,
					ServerName:  "builtin",
					Description: tool.Description(),
					IsBuiltin:   true,
					Source:      source,
					Category:    category,
				})
			}
		}
	}

	// Get MCP server states
	var servers []*commands.MCPServerState
	if a.sdk.mcpManager != nil {
		servers = a.sdk.mcpManager.GetServers()
	}

	mcpLog("[updateMCPSettingsData] allTools count=%d servers count=%d", len(allTools), len(servers))

	// Update settings manager with all tool data
	mcpSettings := a.settingsManager.GetMCPSettings()
	if mcpSettings != nil {
		mcpSettings.SetAllTools(allTools)
		mcpSettings.SetDisabledBuiltinTools(disabledBuiltinTools)
		// Use configBundle.Legacy() for persistence - this respects global/project switching
		if a.configBundle != nil {
			mcpSettings.SetConfigManager(a.configBundle.Legacy())
		}
		mcpSettings.SetServers(servers)
		mcpSettings.SetToolToggleCallback(func(serverName, toolName string, enabled bool) error {
			if a.sdk.mcpManager != nil {
				return a.sdk.mcpManager.ToggleTool(context.Background(), serverName, toolName, enabled)
			}
			return nil
		})

		// Set builtin tool toggle callback
		mcpSettings.SetBuiltinToolToggleCallback(func(toolName string, enabled bool) error {
			if a.sdk.toolRegistry != nil {
				// Get the registry implementation to access enable/disable
				if simpleReg, ok := a.sdk.toolRegistry.(*tools.SimpleRegistry); ok {
					if enabled {
						simpleReg.EnableTool(toolName)
					} else {
						simpleReg.DisableTool(toolName)
					}
				}
			}
			return nil
		})

		// Set server reconnect callback
		mcpSettings.SetServerReconnectCallback(func(serverName string) error {
			if a.sdk.mcpManager != nil {
				return a.sdk.mcpManager.ReconnectServer(context.Background(), serverName)
			}
			return fmt.Errorf("MCP manager not available")
		})

		// Set server toggle callback (enable/disable entire MCP server)
		mcpSettings.SetServerToggleCallback(func(serverName string, enabled bool) error {
			if a.sdk.mcpManager != nil {
				return a.sdk.mcpManager.ToggleServer(context.Background(), serverName, enabled)
			}
			return fmt.Errorf("MCP manager not available")
		})

		// Set server delete callback (delete entire MCP server)
		mcpSettings.SetServerDeleteCallback(func(serverName string) error {
			if a.sdk.mcpManager != nil {
				return a.sdk.mcpManager.DeleteServer(serverName)
			}
			return fmt.Errorf("MCP manager not available")
		})

		// Set SDK tools for the tool tester with proper metadata
		if a.sdk.toolRegistry != nil {
			// Reuse the maps defined earlier for consistency
			var toolEntries []settings.ToolEntry
			toolNames := a.sdk.toolRegistry.List()
			for _, toolName := range toolNames {
				tool, err := a.sdk.toolRegistry.Get(toolName)
				if err != nil {
					continue
				}

				entry := settings.ToolEntry{
					Tool:        tool,
					Name:        toolName,
					Description: tool.Description(),
					ParamCount:  countToolParams(tool),
				}

				// Determine source and package
				if iiToolsMap[toolName] {
					entry.Source = settings.ToolSourceII
					entry.Package = "sdk/tools/ii"
				} else if strings.HasPrefix(toolName, "mcp_") {
					entry.Source = settings.ToolSourceMCP
					entry.Package = "mcp"
					// Extract server name from tool name if possible
					parts := strings.SplitN(toolName, "_", 3)
					if len(parts) >= 2 {
						entry.ServerName = parts[1]
					}
				} else {
					entry.Source = settings.ToolSourceBuiltin
					entry.Package = "sdk/tools/builtin"
				}

				// Set category
				if cat, ok := toolCategoriesMap[toolName]; ok {
					entry.Category = cat
				}

				toolEntries = append(toolEntries, entry)
			}

			// Add MCP tools with proper metadata
			if a.sdk.mcpManager != nil {
				for _, server := range a.sdk.mcpManager.GetServers() {
					if !server.Connected {
						continue
					}
					for _, mcpTool := range server.Tools {
						entry := settings.ToolEntry{
							Name:        mcpTool.Name,
							Source:      settings.ToolSourceMCP,
							Package:     "mcp/" + server.Config.Name,
							ServerName:  server.Config.Name,
							Description: mcpTool.Description,
							Category:    "mcp",
						}
						// Try to get the actual tool from registry
						if tool, err := a.sdk.toolRegistry.Get("mcp_" + server.Config.Name + "_" + mcpTool.Name); err == nil {
							entry.Tool = tool
							entry.ParamCount = countToolParams(tool)
						}
						toolEntries = append(toolEntries, entry)
					}
				}
			}

			mcpSettings.GetToolTester().SetToolEntries(toolEntries)

			// Set up tool execution callback for tool tester
			if mcpSettings.GetToolTester() != nil {
				mcpSettings.GetToolTester().SetOnExecute(func(ctx context.Context, tool tools.Tool, params map[string]any) (string, error) {
					result, err := tool.Execute(ctx, params)
					if err != nil {
						return "", err
					}
					if result != nil && len(result.Content) > 0 {
						// Extract text from content blocks
						var texts []string
						for _, block := range result.Content {
							if block.Text != "" {
								texts = append(texts, block.Text)
							}
						}
						if len(texts) > 0 {
							return strings.Join(texts, "\n"), nil
						}
					}
					return "Tool executed successfully", nil
				})
			}
		}
	}

	securitySettings := a.settingsManager.GetSecuritySettings()
	if securitySettings != nil {
		// Sync level from live config (SetPermissions removed — permissions field was dead code).
		if a.sdk.permissionConfig != nil {
			securitySettings.SetLevel(a.sdk.permissionConfig.Config().Level)
		} else {
			securitySettings.SetLevel(tools.LevelBalanced)
		}

		// Set global and project configs for the rules-based UI.
		securitySettings.SetGlobalConfig(a.sdk.PermissionFullConfig())
		securitySettings.SetProjectConfig(a.sdk.ProjectPermissionConfig())

		// Callback: re-sync global config whenever the level or any rule changes.
		securitySettings.SetOnGlobalConfigRefresh(func() {
			securitySettings.SetGlobalConfig(a.sdk.PermissionFullConfig())
			securitySettings.SetProjectConfig(a.sdk.ProjectPermissionConfig())
		})

		// Build available tools list for the permission rule form (builtin + MCP).
		var securityToolNames []string
		seen := make(map[string]bool)
		for _, t := range allTools {
			if !seen[t.Name] {
				securityToolNames = append(securityToolNames, t.Name)
				seen[t.Name] = true
			}
		}
		// Add MCP tools from connected servers.
		if a.sdk.mcpManager != nil {
			for _, server := range a.sdk.mcpManager.GetServers() {
				if !server.Connected {
					continue
				}
				for _, mcpTool := range server.Tools {
					name := mcpTool.Name
					if !seen[name] {
						securityToolNames = append(securityToolNames, name)
						seen[name] = true
					}
				}
			}
		}
		securitySettings.SetAvailableTools(securityToolNames)

		securitySettings.SetOnPermissionChange(func(permission tools.Permission, policy tools.PermissionPolicy) error {
			return a.sdk.UpdatePermissionPolicy(permission, policy)
		})
		securitySettings.SetOnLevelChange(func(level tools.PermissionLevel) error {
			if err := a.sdk.UpdatePermissionLevel(level); err != nil {
				return err
			}
			// Refresh configs so the live level is reflected in the UI immediately.
			securitySettings.SetGlobalConfig(a.sdk.PermissionFullConfig())
			return nil
		})
		securitySettings.SetOnRuleAdd(func(rule tools.PermissionRule, workspace bool) error {
			if err := a.sdk.AddPermissionRule(rule, workspace); err != nil {
				return err
			}
			securitySettings.SetGlobalConfig(a.sdk.PermissionFullConfig())
			securitySettings.SetProjectConfig(a.sdk.ProjectPermissionConfig())
			return nil
		})
		securitySettings.SetOnRuleDelete(func(ruleIndex int, workspace bool) error {
			if err := a.sdk.DeletePermissionRule(ruleIndex, workspace); err != nil {
				return err
			}
			securitySettings.SetGlobalConfig(a.sdk.PermissionFullConfig())
			securitySettings.SetProjectConfig(a.sdk.ProjectPermissionConfig())
			return nil
		})
		securitySettings.SetOnOverrideDelete(func(tool string) error {
			if err := a.sdk.DeleteToolOverride(tool); err != nil {
				return err
			}
			securitySettings.SetGlobalConfig(a.sdk.PermissionFullConfig())
			return nil
		})
	}

	logDebug("Updated MCP settings data: %d builtin tools, %d servers", len(allTools), len(servers))
}

// countToolParams counts the number of parameters a tool has
func countToolParams(tool tools.Tool) int {
	schema := tool.Parameters()
	if schema == nil {
		return 0
	}
	schemaMap, ok := schema.(map[string]any)
	if !ok {
		return 0
	}
	props, ok := schemaMap["properties"].(map[string]any)
	if !ok {
		return 0
	}
	return len(props)
}
