package chat

import (
	"context"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/i18n"
)

// ============================================================================
// WORKSPACE MODE - Uses existing two-pane layout with splittable main area
// ============================================================================

// The workspace mode extends the existing two-pane layout to support
// multiple chat panes in the main area. The sidebar is always fixed
// on the left and uses the same conversation list rendering.

// calculateLayout computes positions and sizes for chat panes in the main area
func (a *App) calculateLayout() {
	if a.currentWorkspace < 0 || a.currentWorkspace >= len(a.workspaces) {
		return
	}

	ws := a.workspaces[a.currentWorkspace]
	if ws.Root == nil {
		return
	}

	// Ensure sidebar width is initialized with more space
	if a.sidebarWidth == 0 {
		// Use 38% for sidebar on larger screens, scale down for smaller screens
		if a.width >= 120 {
			a.sidebarWidth = a.width * 38 / 100
		} else if a.width >= 100 {
			a.sidebarWidth = a.width * 40 / 100
		} else {
			a.sidebarWidth = a.width * 35 / 100
		}
	}
	// Increased minimum width to prevent text wrapping
	if a.sidebarWidth < 35 {
		a.sidebarWidth = 35
	}

	// Calculate main area dimensions (accounting for sidebar)
	dividerWidth := 1
	mainX := a.sidebarWidth + dividerWidth
	mainWidth := a.width - a.sidebarWidth - dividerWidth

	if mainWidth < 40 {
		mainWidth = 40
		a.sidebarWidth = a.width - mainWidth - dividerWidth
	}

	// Calculate layout recursively for the main area only
	a.layoutContainer(ws.Root, mainX, 0, mainWidth, a.height)
}

// layoutContainer recursively calculates bounds for panes
func (a *App) layoutContainer(container *PaneContainer, x, y, width, height int) {
	if container.Pane != nil {
		// Leaf node - set pane bounds
		container.Pane.X = x
		container.Pane.Y = y
		container.Pane.Width = width
		container.Pane.Height = height
	} else if container.Split != nil {
		// Split node - divide space
		split := container.Split

		if split.Direction == SplitHorizontal {
			// Left | Right
			firstW := int(float64(width) * split.Ratio)
			secondW := width - firstW

			a.layoutContainer(split.First, x, y, firstW, height)
			a.layoutContainer(split.Second, x+firstW, y, secondW, height)
		} else {
			// Top / Bottom
			firstH := int(float64(height) * split.Ratio)
			secondH := height - firstH

			a.layoutContainer(split.First, x, y, width, firstH)
			a.layoutContainer(split.Second, x, y+firstH, width, secondH)
		}
	}
}

// getAllPanes returns all chat panes in the current workspace
func (a *App) getAllPanes() []*Pane {
	if a.currentWorkspace < 0 || a.currentWorkspace >= len(a.workspaces) {
		return nil
	}

	ws := a.workspaces[a.currentWorkspace]
	var panes []*Pane
	a.collectPanes(ws.Root, &panes)
	return panes
}

// collectPanes recursively collects all panes
func (a *App) collectPanes(container *PaneContainer, panes *[]*Pane) {
	if container == nil {
		return
	}

	if container.Pane != nil {
		*panes = append(*panes, container.Pane)
	} else if container.Split != nil {
		a.collectPanes(container.Split.First, panes)
		a.collectPanes(container.Split.Second, panes)
	}
}

// ============================================================================
// PANE STATE MANAGEMENT FOR PARALLEL EXECUTION
// ============================================================================

// savePaneState saves the current global conversation state to a pane
func (a *App) savePaneState(pane *Pane) {
	if pane == nil {
		return
	}

	pane.ConvID = a.currentConvID
	pane.Messages = make([]Message, len(a.messages))
	copy(pane.Messages, a.messages)
	pane.TokenCount = a.tokenCount
	pane.TokenCountIsEst = a.tokenCountIsEstimate
	pane.LastRealTokens = a.lastRealTokenCount
	pane.StreamingMessage = a.streamingMessage
	pane.InputBuffer = a.textInput.Value()

	logDebug("[savePaneState] Saved state for pane ConvID=%s: %d messages, streaming=%v",
		pane.ConvID, len(pane.Messages), pane.StreamingMessage)
}

// loadPaneState loads conversation state from a pane into global state
func (a *App) loadPaneState(pane *Pane) {
	if pane == nil {
		return
	}

	// Clear current UI state first
	a.stopWordPlayback()

	// Set the conversation ID
	a.setCurrentConversationID(pane.ConvID)

	// Set up task persistence for the conversation
	if a.sdk != nil && a.currentConvID != "" {
		if err := a.sdk.SetupTaskPersistence(a.currentConvID); err != nil {
			logDebug("[loadPaneState] Failed to setup task persistence: %v", err)
		}
	}

	// Restore messages
	if len(pane.Messages) > 0 {
		a.messages = make([]Message, len(pane.Messages))
		copy(a.messages, pane.Messages)
	} else {
		a.messages = []Message{}
	}

	// Restore token state
	a.tokenCount = pane.TokenCount
	a.tokenCountIsEstimate = pane.TokenCountIsEst
	a.lastRealTokenCount = pane.LastRealTokens

	// Restore input
	a.textInput.SetValue(pane.InputBuffer)

	// Set active conversation
	if pane.ConvID != "" {
		for i := range a.conversations {
			if a.conversations[i].ID == pane.ConvID {
				a.activeConv = &a.conversations[i]
				break
			}
		}
	}

	// Check if agent is running for this pane's conversation
	if pane.ConvID != "" && a.bgManager != nil && a.bgManager.IsRunning(pane.ConvID) {
		a.streamingMessage = true
		a.setActivityPhase(ActivityPhaseThinking, "")
		a.loadingIndicator.Start(a.animationClock)

		// Subscribe to updates
		uiChan, buffered := a.bgManager.Subscribe(pane.ConvID, "ui")
		// Process any buffered updates that arrived before we subscribed
		for _, update := range buffered {
			a.queueAgentIntermediateUpdate(update)
		}
		if uiChan != nil {
			go a.listenForAgentUpdates(uiChan)
		}
	}

	// Update viewport
	a.updateViewportContent()

	logDebug("[loadPaneState] Loaded state for pane ConvID=%s: %d messages, streaming=%v",
		pane.ConvID, len(a.messages), a.streamingMessage)
}

// ============================================================================
// WORKSPACE VIEW - Reuses two-pane layout with splittable main area
// ============================================================================

// viewWorkspace renders the workspace with fixed sidebar and splittable chat area
func (a *App) viewWorkspace() string {
	// Calculate sidebar width
	// Calculate sidebar width with more space
	if a.sidebarWidth == 0 {
		if a.width >= 120 {
			a.sidebarWidth = a.width * 38 / 100
		} else if a.width >= 100 {
			a.sidebarWidth = a.width * 40 / 100
		} else {
			a.sidebarWidth = a.width * 35 / 100
		}
	}

	minSidebarWidth := 35
	minMainWidth := 40

	if a.sidebarWidth < minSidebarWidth {
		a.sidebarWidth = minSidebarWidth
	}

	dividerWidth := 1
	mainWidth := a.width - a.sidebarWidth - dividerWidth

	if mainWidth < minMainWidth {
		mainWidth = minMainWidth
		a.sidebarWidth = a.width - mainWidth - dividerWidth
	}

	// Render sidebar (always fixed, uses existing conversation list)
	sidebarContent := a.renderWorkspaceSidebar(a.sidebarWidth, a.height)

	// Render main area (may be split into multiple panes)
	mainContent := a.renderWorkspaceMain(mainWidth, a.height)

	// Render divider
	divider := a.renderPaneDivider(a.height)

	// Join sidebar and main area (no workspace bar - cleaner UX)
	content := lipgloss.JoinHorizontal(
		lipgloss.Top,
		sidebarContent,
		divider,
		mainContent,
	)

	return content
}

// renderWorkspaceSidebar renders the conversations sidebar using existing components
func (a *App) renderWorkspaceSidebar(width, height int) string {
	th := a.theme
	innerHeight := height - 2 // -2 for borders (workspace bar removed)

	// Highlight border when sidebar is focused
	borderColor := th.Border
	if a.activePane == SidebarPane {
		borderColor = th.Primary // Bright border when focused
	}

	panelStyle := lipgloss.NewStyle().
		Width(width).
		Height(innerHeight).
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color(borderColor))

	// Render conversation list with help text
	content := a.renderEnhancedConversationList(width - 2)

	// Add helpful status line at bottom if sidebar is focused
	if a.activePane == SidebarPane {
		helpText := lipgloss.NewStyle().
			Foreground(lipgloss.Color(th.TextMuted)).
			Render("\n" + i18n.T("classic_chat.workspace.sidebar_hint"))
		content = content + helpText
	}

	return panelStyle.Render(content)
}

// renderWorkspaceMain renders the main area with chat pane(s)
func (a *App) renderWorkspaceMain(width, height int) string {
	th := a.theme
	innerHeight := height - 2 // -2 for borders (workspace bar removed)

	panes := a.getAllPanes()

	if len(panes) == 0 {
		// No panes - show empty state
		panelStyle := lipgloss.NewStyle().
			Width(width).
			Height(innerHeight).
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color(th.Border))

		emptyStyle := lipgloss.NewStyle().
			Foreground(lipgloss.Color(th.TextMuted)).
			Width(width-4).
			Padding(2, 2)

		return panelStyle.Render(emptyStyle.Render(i18n.T("classic_chat.workspace.select_sidebar")))
	}

	// Calculate pane positions (workspace bar removed - no height adjustment needed)
	a.calculateLayout()

	// Render each pane
	var paneContents []string
	for _, pane := range panes {
		content := a.renderWorkspaceChatPane(pane, th)
		paneContents = append(paneContents, content)
	}

	// Join panes horizontally
	if len(paneContents) == 1 {
		return paneContents[0]
	}
	return lipgloss.JoinHorizontal(lipgloss.Top, paneContents...)
}

// renderWorkspaceChatPane renders a single chat pane using optimized viewport rendering
// This provides the same quality and performance as the main chat view
func (a *App) renderWorkspaceChatPane(pane *Pane, th Theme) string {
	// Determine border style based on focus
	borderStyle := lipgloss.RoundedBorder()
	borderColor := th.Border
	if pane.Focused {
		borderColor = th.Primary
	}

	// Create the panel style
	panelStyle := lipgloss.NewStyle().
		Width(pane.Width).
		Height(pane.Height).
		Border(borderStyle).
		BorderForeground(lipgloss.Color(borderColor))

	contentW := pane.Width - 4 // Account for borders
	contentH := pane.Height - 4

	if contentW < 10 {
		contentW = 10
	}
	if contentH < 5 {
		contentH = 5
	}

	var content string

	if pane.ConvID == "" {
		// Empty pane - prompt to select conversation
		emptyStyle := lipgloss.NewStyle().
			Foreground(lipgloss.Color(th.TextMuted)).
			Width(contentW).
			Padding(1, 1)

		content = emptyStyle.Render(i18n.T("classic_chat.workspace.empty_pane"))
	} else {
		// Render pane using viewport (same as main chat for consistency and performance)
		content = a.renderPaneWithViewport(pane, contentW, contentH)
	}

	return panelStyle.Render(content)
}

// renderPaneWithViewport renders a pane using its viewport for optimized message rendering
// This ensures all panes get the same rendering quality and optimizations as the main chat
func (a *App) renderPaneWithViewport(pane *Pane, width, height int) string {
	th := a.theme

	// Find the conversation for this pane
	var conv *Conversation
	for i := range a.conversations {
		if a.conversations[i].ID == pane.ConvID {
			conv = &a.conversations[i]
			break
		}
	}

	// Build header with conversation title
	title := i18n.T("classic_chat.workspace.chat")
	if conv != nil {
		title = conv.Title
	}

	// Add focus indicator to title for clarity
	focusIndicator := ""
	if pane.Focused {
		focusIndicator = "● " // Active dot
	} else {
		focusIndicator = "○ " // Inactive circle
	}
	title = focusIndicator + title

	// Truncate title if needed
	maxTitleW := width - 10
	if len(title) > maxTitleW {
		title = title[:maxTitleW-3] + "..."
	}

	// Use brighter color for focused pane header
	headerColor := th.Primary
	if !pane.Focused {
		headerColor = th.TextMuted
	}

	headerStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(headerColor)).
		Bold(pane.Focused).
		Width(width)

	header := headerStyle.Render(title)

	// Divider
	divider := strings.Repeat("─", width)

	// Calculate space for messages, input, and status line
	inputH := 3  // Input box height
	statusH := 1 // Status line height
	reservedH := strings.Count(header, "\n") + strings.Count(divider, "\n") + inputH + statusH + 3
	msgsH := height - reservedH
	if msgsH < 3 {
		msgsH = 3
	}

	// Initialize viewport if needed
	if pane.Viewport == nil {
		pane.Viewport = NewMessageList(width, msgsH)
	}

	// Update viewport size (in case pane was resized)
	pane.Viewport.SetSize(width, msgsH)

	// Render messages using viewport (with full optimizations)
	var msgsContent string
	if len(pane.Messages) == 0 {
		msgsContent = lipgloss.NewStyle().
			Foreground(lipgloss.Color(th.TextMuted)).
			Render(i18n.T("classic_chat.workspace.no_messages"))
	} else {
		// Update viewport content if messages changed
		// Use the same rendering context as main chat for consistency
		previewCtx := a.NewPreviewMessageContext(width)
		msgLines := a.renderMessageListWithContext(pane.Messages, previewCtx)

		// Set viewport content and render
		pane.Viewport.SetContent(strings.Join(msgLines, "\n"))
		msgsContent = pane.Viewport.View()
	}

	// Render input box (show current input for focused pane, placeholder for others)
	var inputContent string
	if pane.Focused {
		// Show actual input with cursor (only for focused pane)
		if pane.StreamingMessage {
			spinner := "..."
			inputContent = lipgloss.NewStyle().
				Foreground(lipgloss.Color(th.Warning)).
				Render(spinner + " " + i18n.T("classic_chat.workspace.agent_working"))
		} else {
			inputContent = "> " + pane.InputBuffer
		}
	} else {
		// Show placeholder for non-focused panes
		inputContent = lipgloss.NewStyle().
			Foreground(lipgloss.Color(th.TextMuted)).
			Render(i18n.T("classic_chat.workspace.focus_input"))
	}

	// Add helpful status line showing available keybinds
	var statusLine string
	if pane.Focused {
		// Focused pane: show chat and scrolling controls
		statusLine = lipgloss.NewStyle().
			Foreground(lipgloss.Color(th.TextMuted)).
			Render(i18n.T("classic_chat.workspace.chat_hint"))
	} else {
		// Non-focused pane: show how to focus
		statusLine = lipgloss.NewStyle().
			Foreground(lipgloss.Color(th.TextMuted)).
			Render(i18n.T("classic_chat.workspace.navigation_hint"))
	}

	// Combine all parts
	parts := []string{header, divider, msgsContent, "", inputContent, statusLine}
	return lipgloss.JoinVertical(lipgloss.Left, parts...)
}

// DELETED: renderFocusedChatPane and renderPreviewPane
// These redundant custom renderers have been replaced by renderPaneWithViewport()
// which uses the optimized viewport system for consistent rendering across all panes

// ============================================================================
// WORKSPACE KEYBOARD HANDLING
// ============================================================================

// handleWorkspaceKey handles keyboard input in workspace mode
// Design philosophy: Simple, predictable, comfortable
//
// SIDEBAR MODE (when sidebar is focused):
//   - Up/Down or j/k: Navigate conversations
//   - Enter: Open selected conversation
//   - Tab: Move to first pane
//
// PANE MODE (when a chat pane is focused):
//   - Up/Down or j/k: Scroll messages
//   - Tab: Next pane (cycles back to sidebar)
//   - Shift+Tab: Previous pane
//   - Enter: Send message
//   - Type normally: Input goes to message box
//
// GLOBAL (works everywhere):
//   - Esc: Exit workspace mode
//   - Alt+1/2/3: Switch workspace layout
//   - Ctrl+C: Stop running agent
//   - n: New chat in current pane
func (a *App) handleWorkspaceKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	key := msg.String()

	// Get focused pane
	panes := a.getAllPanes()
	var focusedPane *Pane
	focusedIdx := -1
	for i, p := range panes {
		if p.Focused {
			focusedPane = p
			focusedIdx = i
			break
		}
	}

	// ============================================================================
	// GLOBAL KEYS (work regardless of focus)
	// ============================================================================

	switch key {
	case "esc":
		// Exit workspace mode (save current pane state first)
		if focusedPane != nil {
			a.savePaneState(focusedPane)
		}
		a.workspaceMode = false
		a.screen = ScreenChats
		return a, nil

	case "ctrl+c":
		// Stop agent if running (works in any pane)
		if a.streamingMessage && a.currentConvID != "" && a.bgManager != nil {
			a.bgManager.Cancel(a.currentConvID)
			a.streamingMessage = false
			a.loadingIndicator.Stop(a.animationClock)
			a.setActivityPhase(ActivityPhaseIdle, "")
			a.addNotification("info", i18n.T("classic_chat.agent.stopped"))
		}
		return a, nil

	case "n":
		// New chat in current pane (or first pane if sidebar focused)
		a.startNewChatInWorkspace()
		return a, nil

	case "tab":
		// Universal forward navigation: Sidebar → Pane1 → Pane2 → ... → Sidebar
		if a.activePane == SidebarPane {
			// Move from sidebar to first pane
			if len(panes) > 0 {
				a.activePane = MessagesPane
				panes[0].Focused = true
				a.focusedPane = panes[0]
				a.loadPaneState(panes[0])
			}
		} else if focusedPane != nil {
			// Move to next pane, or wrap back to sidebar
			if focusedIdx < len(panes)-1 {
				// Next pane
				a.savePaneState(focusedPane)
				focusedPane.Focused = false
				panes[focusedIdx+1].Focused = true
				a.focusedPane = panes[focusedIdx+1]
				a.loadPaneState(panes[focusedIdx+1])
			} else {
				// Wrap back to sidebar
				a.savePaneState(focusedPane)
				focusedPane.Focused = false
				a.activePane = SidebarPane
				a.focusedPane = nil
			}
		}
		return a, nil

	case "shift+tab":
		// Universal backward navigation: Sidebar ← Pane1 ← Pane2 ← ... ← Sidebar
		if a.activePane == SidebarPane {
			// Wrap from sidebar to last pane
			if len(panes) > 0 {
				a.activePane = MessagesPane
				lastIdx := len(panes) - 1
				panes[lastIdx].Focused = true
				a.focusedPane = panes[lastIdx]
				a.loadPaneState(panes[lastIdx])
			}
		} else if focusedPane != nil {
			// Move to previous pane, or wrap back to sidebar
			if focusedIdx > 0 {
				// Previous pane
				a.savePaneState(focusedPane)
				focusedPane.Focused = false
				panes[focusedIdx-1].Focused = true
				a.focusedPane = panes[focusedIdx-1]
				a.loadPaneState(panes[focusedIdx-1])
			} else {
				// Wrap back to sidebar
				a.savePaneState(focusedPane)
				focusedPane.Focused = false
				a.activePane = SidebarPane
				a.focusedPane = nil
			}
		}
		return a, nil
	}

	// ============================================================================
	// SIDEBAR MODE (conversation list navigation)
	// ============================================================================

	if a.activePane == SidebarPane {
		switch key {
		case "up", "k":
			// Navigate up in conversation list
			if a.selectedIdx > 0 {
				a.selectedIdx--
			}
			return a, nil

		case "down", "j":
			// Navigate down in conversation list
			if a.selectedIdx < len(a.conversations)-1 {
				a.selectedIdx++
				// Lazy loading: check if we need to load more conversations
				a.checkAndLoadNextPage()
			}
			return a, nil

		case "enter":
			// Open selected conversation in current/first pane
			a.openConversationInWorkspace(a.selectedIdx)
			return a, nil
		}
	}

	// ============================================================================
	// PANE MODE (chat interaction and scrolling)
	// ============================================================================

	if focusedPane != nil && a.activePane == MessagesPane {
		switch key {
		case "enter":
			// Send message (only if not streaming and has content)
			if !a.streamingMessage && a.currentConvID != "" {
				cmd := a.handleSendMessage()
				return a, cmd
			}
			return a, nil

		// Scrolling: Up/Down and j/k both work (accommodates different preferences)
		case "up", "k":
			// Scroll up one line
			if focusedPane.Viewport != nil {
				focusedPane.Viewport.ScrollUp(1)
			}
			return a, nil

		case "down", "j":
			// Scroll down one line
			if focusedPane.Viewport != nil {
				focusedPane.Viewport.ScrollDown(1)
			}
			return a, nil

		case "pgup":
			// Page up (fast scrolling)
			if focusedPane.Viewport != nil {
				focusedPane.Viewport.PageUp()
			}
			return a, nil

		case "pgdown":
			// Page down (fast scrolling)
			if focusedPane.Viewport != nil {
				focusedPane.Viewport.PageDown()
			}
			return a, nil

		case "home":
			// Jump to top of conversation
			if focusedPane.Viewport != nil {
				focusedPane.Viewport.GotoTop()
			}
			return a, nil

		case "end":
			// Jump to bottom of conversation
			if focusedPane.Viewport != nil {
				focusedPane.Viewport.GotoBottom()
			}
			return a, nil

		default:
			// All other keys: Forward to text input (typing messages)
			if !a.streamingMessage {
				a.textInput.Update(msg)
			}
			return a, nil
		}
	}

	return a, nil
}

// ============================================================================
// WORKSPACE CONVERSATION MANAGEMENT
// ============================================================================

// openConversationInWorkspace opens a conversation in a chat pane
func (a *App) openConversationInWorkspace(idx int) {
	a.stopWordPlayback()
	if idx < 0 || idx >= len(a.conversations) {
		return
	}

	conv := &a.conversations[idx]

	// Find an available chat pane
	panes := a.getAllPanes()
	var targetPane *Pane

	for _, p := range panes {
		if p.ConvID == "" {
			// Empty pane - use it
			targetPane = p
			break
		}
		if p.Focused {
			// Focused pane - replace it
			targetPane = p
		}
		if targetPane == nil {
			targetPane = p
		}
	}

	if targetPane == nil {
		a.addNotification("warning", i18n.T("classic_chat.workspace.no_chat_pane"))
		return
	}

	// Save current pane state if switching
	if a.focusedPane != nil && a.focusedPane != targetPane {
		a.savePaneState(a.focusedPane)
	}

	logDebug("[openConversationInWorkspace] Opening conv %s in pane", conv.ID)

	// Reset the target pane's state
	targetPane.ConvID = conv.ID
	targetPane.Messages = nil
	targetPane.TokenCount = 0
	// Viewport will be initialized if needed during rendering

	// Set global state for this conversation
	a.activeConv = conv
	a.setCurrentConversationID(conv.ID)

	// Set up task persistence for the conversation
	if a.sdk != nil && a.currentConvID != "" {
		if err := a.sdk.SetupTaskPersistence(a.currentConvID); err != nil {
			logDebug("[openConversationInWorkspace] Failed to setup task persistence: %v", err)
		}
	}

	// Reset UI state
	a.streamingMessage = false
	a.streamingInProgress = false
	a.streamBuffer = ""
	a.streamingInputTokens = 0
	a.streamingOutputChars = 0
	a.isCompacting = false
	if a.loadingIndicator != nil {
		a.loadingIndicator.Stop(a.animationClock)
	}
	if a.spinner != nil {
		a.spinner.Stop(a.animationClock)
	}

	// CRITICAL: Reset sidebar/preview cache state to prevent stale data from persisting
	// These caches can hold data from the previously active conversation
	a.cachedPreviewID = ""                     // Clear preview message cache
	a.cachedPreviewMsgs = nil                  // Clear preview messages list
	a.collapsedParents = make(map[string]bool) // Reset fork/compaction collapsed state
	a.scrollOffset = 0                         // Reset sidebar scroll position
	a.selectedIdx = 0                          // Reset sidebar selection index
	a.streamingMessage = false
	a.streamingInProgress = false
	a.streamBuffer = ""
	a.streamingInputTokens = 0
	a.streamingOutputChars = 0
	a.isCompacting = false
	if a.loadingIndicator != nil {
		a.loadingIndicator.Stop(a.animationClock)
	}
	if a.spinner != nil {
		a.spinner.Stop(a.animationClock)
	}

	// Load messages from SDK storage
	a.loadMessagesFromSDK(conv.ID)

	// Load token count
	if a.sdk != nil && a.currentConvID != "" {
		ctx := context.Background()
		if sdkConv, err := a.sdk.ResumeConversation(ctx, a.currentConvID); err == nil {
			if sdkConv.CurrentContextSize > 0 {
				a.tokenCount = sdkConv.CurrentContextSize
				a.tokenCountIsEstimate = false
				a.lastRealTokenCount = sdkConv.CurrentContextSize
			} else {
				a.tokenCount = a.getSystemPromptEstimate()
				a.tokenCountIsEstimate = true
			}
			a.modelContextWindow = a.sdk.GetModelContextWindow()
		}
	}

	// Check for running agent
	if a.bgManager != nil && a.currentConvID != "" && a.bgManager.IsRunning(a.currentConvID) {
		a.streamingMessage = true
		a.setActivityPhase(ActivityPhaseThinking, "")
		a.loadingIndicator.Start(a.animationClock)

		uiChan, buffered := a.bgManager.Subscribe(a.currentConvID, "ui")
		// Process any buffered updates that arrived before we subscribed
		for _, update := range buffered {
			a.queueAgentIntermediateUpdate(update)
		}
		if uiChan != nil {
			go a.listenForAgentUpdates(uiChan)
		}
	} else {
		a.setActivityPhase(ActivityPhaseIdle, "")
	}

	// Save to pane state
	a.savePaneState(targetPane)

	// Focus the pane
	for _, p := range panes {
		p.Focused = (p == targetPane)
	}
	targetPane.Focused = true
	a.focusedPane = targetPane
	a.activePane = MessagesPane

	// Update viewport
	a.updateViewportContent()

	logDebug("[openConversationInWorkspace] Completed: conv=%s, messages=%d", a.currentConvID, len(a.messages))
}

// startNewChatInWorkspace starts a new chat in the current pane
func (a *App) startNewChatInWorkspace() {
	panes := a.getAllPanes()
	var targetPane *Pane

	for _, p := range panes {
		if p.Focused || p.ConvID == "" {
			targetPane = p
			break
		}
	}

	if targetPane == nil && len(panes) > 0 {
		targetPane = panes[0]
	}

	if targetPane == nil {
		a.addNotification("warning", i18n.T("classic_chat.workspace.no_chat_pane"))
		return
	}

	// Save current state
	if a.focusedPane != nil {
		a.savePaneState(a.focusedPane)
	}

	// Reset UI state
	a.resetChatUIState()

	// Auto-detect current git branch so conversations are properly grouped in sidebar
	currentBranch := ""
	if a.gitHelper != nil && a.gitHelper.IsRepo() {
		currentBranch, _ = a.gitHelper.CurrentBranch()
	}

	// Create new conversation
	a.activeConv = &Conversation{
		ID:           time.Now().Format("20060102150405"),
		Title:        i18n.T("classic_chat.chat.new"),
		Status:       "idle",
		IsActive:     false,
		InputBuffer:  "",
		ContinueInBg: false,
		Branch:       currentBranch, // Auto-detect current git branch
	}
	a.clearCurrentConversationID()

	if a.sdk != nil {
		a.modelContextWindow = a.sdk.GetModelContextWindow()
	}

	a.clearInputForNewChat()

	// Clear pane state
	targetPane.ConvID = ""
	targetPane.Messages = nil
	targetPane.TokenCount = 0
	// Viewport will be cleared/reset automatically during rendering

	// Focus the pane
	for _, p := range panes {
		p.Focused = (p == targetPane)
	}
	targetPane.Focused = true
	a.focusedPane = targetPane
	a.activePane = MessagesPane

	logDebug("[startNewChatInWorkspace] Started new chat in pane")
}
