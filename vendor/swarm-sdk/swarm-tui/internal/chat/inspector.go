// inspector.go provides deep state introspection for the App.
// This enables headless automation to inspect the full application state.
package chat

import (
	"fmt"
	"strings"
	"time"

	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/headless/automation/state"
)

// Compile-time interface verification
var (
	_ state.Inspector          = (*App)(nil)
	_ state.MessageInspector   = (*App)(nil)
	_ state.FocusInspector     = (*App)(nil)
	_ state.InputInspector     = (*App)(nil)
	_ state.ScrollInspector    = (*App)(nil)
	_ state.SelectionInspector = (*App)(nil)
	_ state.ModalInspector     = (*App)(nil)
	_ state.AppInspector       = (*App)(nil)
)

// GetScreen returns the current screen identifier.
// Implements state.Inspector.
func (a *App) GetScreen() string {
	switch a.screen {
	case ScreenHome:
		return state.ScreenHome
	case ScreenChats:
		return state.ScreenChats
	case ScreenChat:
		return state.ScreenChat
	case ScreenViewer:
		return state.ScreenViewer
	case ScreenNewChatUI:
		return "new_chat_ui"
	default:
		return fmt.Sprintf("unknown_%d", a.screen)
	}
}

// GetMessages returns all messages in the current view.
// Implements state.MessageInspector.
func (a *App) GetMessages() []state.MessageState {
	messages := make([]state.MessageState, len(a.messages))
	for i, msg := range a.messages {
		blocks := make([]state.BlockState, 0)

		// Convert content blocks
		if msg.Content != "" {
			blocks = append(blocks, state.BlockState{
				Type:    "content",
				Content: msg.Content,
			})
		}

		// Convert thinking blocks
		if msg.Thinking != "" {
			blocks = append(blocks, state.BlockState{
				Type:    "thinking",
				Content: msg.Thinking,
			})
		}

		// Convert tool calls
		for _, tc := range msg.ToolCalls {
			blocks = append(blocks, state.BlockState{
				Type:    "tool_call",
				Content: fmt.Sprintf("%s: %s", tc.Name, tc.ID),
			})
		}

		// Convert tool results
		for _, tr := range msg.ToolResults {
			blocks = append(blocks, state.BlockState{
				Type:    "tool_result",
				Content: tr.Output,
			})
		}

		messages[i] = state.MessageState{
			ID:        fmt.Sprintf("msg_%d", i),
			Role:      string(msg.Role),
			Content:   msg.Content,
			Blocks:    blocks,
			Timestamp: msg.Timestamp,
		}
	}
	return messages
}

// GetMessageCount returns the total number of messages.
// Implements state.MessageInspector.
func (a *App) GetMessageCount() int {
	return len(a.messages)
}

// GetFocusedComponent returns the ID of the focused component.
// Implements state.FocusInspector.
func (a *App) GetFocusedComponent() string {
	// Determine focus based on current state
	if a.approvalModal != nil {
		return "modal"
	}
	if a.activeModal != nil {
		return "modal"
	}
	if a.commandPalette != nil && a.commandPalette.visible {
		return "command_palette"
	}
	if a.modelSwitcher.visible {
		return "model_switcher"
	}
	if a.agentSwitcher.visible {
		return "agent_switcher"
	}
	if a.promptSwitcher.visible {
		return "prompt_switcher"
	}
	if a.profileSwitcher.visible {
		return "profile_switcher"
	}
	if a.skillPicker.visible {
		return "skill_picker"
	}
	if a.mentionAutocomplete != nil && a.mentionAutocomplete.IsVisible() {
		return "mention_autocomplete"
	}
	if a.cmdAutocomplete != nil && a.cmdAutocomplete.IsVisible() {
		return "cmd_autocomplete"
	}
	if a.debugScreen != nil && a.debugScreen.visible {
		return "debug_screen"
	}

	switch a.screen {
	case ScreenHome:
		if a.homeButton == ButtonSettings {
			return "settings"
		}
		return "home_menu"
	case ScreenChats:
		return "conversation_list"
	case ScreenChat:
		if a.messageNavMode {
			return "message_list"
		}
		return "input"
	case ScreenViewer:
		if a.viewerComponent != nil && a.viewerComponent.focusTree {
			return "file_tree"
		}
		return "file_viewer"
	default:
		return "unknown"
	}
}

// IsFocused returns true if the given component is focused.
// Implements state.FocusInspector.
func (a *App) IsFocused(componentID string) bool {
	return a.GetFocusedComponent() == componentID
}

// GetInputText returns the current input text.
// Implements state.InputInspector.
func (a *App) GetInputText() string {
	if a.textInput != nil {
		return a.textInput.Value()
	}
	return ""
}

// GetCursorPosition returns the cursor position.
// Implements state.InputInspector.
func (a *App) GetCursorPosition() int {
	if a.textInput != nil {
		return a.textInput.cursor
	}
	return 0
}

// IsInputFocused returns true if input is focused.
// Implements state.InputInspector.
func (a *App) IsInputFocused() bool {
	return a.screen == ScreenChat && !a.messageNavMode && a.activeModal == nil && a.approvalModal == nil
}

// GetScrollOffset returns the current scroll offset.
// Implements state.ScrollInspector.
func (a *App) GetScrollOffset() int {
	return a.scrollOffset
}

// GetScrollMax returns the maximum scroll value.
// Implements state.ScrollInspector.
func (a *App) GetScrollMax() int {
	if a.msgViewport != nil {
		return len(a.msgViewport.lines)
	}
	return 0
}

// GetVisibleRange returns first and last visible message indices.
// Implements state.ScrollInspector.
func (a *App) GetVisibleRange() (first, last int) {
	if len(a.messages) == 0 {
		return 0, 0
	}

	// Use message line positions if available
	if len(a.messageLinePositions) > 0 {
		viewportHeight := a.height - 6 // Approximate viewport height
		scrollEnd := a.scrollOffset + viewportHeight

		first = -1
		last = -1

		for i, pos := range a.messageLinePositions {
			if pos.StartLine <= scrollEnd && pos.EndLine >= a.scrollOffset {
				if first == -1 {
					first = i
				}
				last = i
			}
		}

		if first == -1 {
			first = 0
			last = 0
		}
		return first, last
	}

	return 0, len(a.messages) - 1
}

// GetSelectedIndex returns the currently selected index.
// Implements state.SelectionInspector.
func (a *App) GetSelectedIndex() int {
	return a.selectedIdx
}

// GetSelectedItem returns the currently selected item.
// Implements state.SelectionInspector.
func (a *App) GetSelectedItem() any {
	switch a.screen {
	case ScreenHome:
		return a.homeButton
	case ScreenChats:
		if a.selectedIdx >= 0 && a.selectedIdx < len(a.conversations) {
			return a.conversations[a.selectedIdx]
		}
	}
	return nil
}

// IsModalOpen returns true if a modal is open.
// Implements state.ModalInspector.
func (a *App) IsModalOpen() bool {
	return a.activeModal != nil ||
		a.approvalModal != nil ||
		a.newChatModal != nil ||
		a.taskActivityModal != nil ||
		a.checkoutModal != nil ||
		a.gitInitModal != nil
}

// GetModalID returns the ID of the open modal.
// Implements state.ModalInspector.
func (a *App) GetModalID() string {
	if a.approvalModal != nil {
		return state.ModalPermissionApproval
	}
	if a.activeModal != nil {
		switch a.activeModal.Type {
		case ModalExitChat:
			return state.ModalExit
		case ModalStopAgent:
			return state.ModalStopAgent
		case ModalNewChat:
			return state.ModalNewChat
		case ModalGitCheckout:
			return state.ModalGitCheckout
		case ModalGitInit:
			return "git_init"
		default:
			return fmt.Sprintf("modal_%d", a.activeModal.Type)
		}
	}
	if a.newChatModal != nil {
		return state.ModalNewChat
	}
	if a.checkoutModal != nil {
		return state.ModalGitCheckout
	}
	if a.gitInitModal != nil {
		return "git_init"
	}
	return state.ModalNone
}

// GetModalData returns the modal's data.
// Implements state.ModalInspector.
func (a *App) GetModalData() any {
	if a.approvalModal != nil {
		req := a.approvalModal.request
		data := map[string]any{
			"type":        "permission_approval",
			"tool":        req.Tool,
			"permission":  req.Permission,
			"target":      req.Target,
			"reason":      req.Reason,
			"request_id":  req.RequestID,
			"queue_index": a.approvalModal.queueIndex,
			"queue_total": a.approvalModal.queueTotal,
		}
		if req.Preview != nil {
			data["preview_type"] = req.Preview.Type
			data["preview"] = req.Preview.Content
		}
		return data
	}
	if a.activeModal != nil {
		return map[string]any{
			"type":           a.activeModal.Type,
			"title":          a.activeModal.Title,
			"message":        a.activeModal.Message,
			"selected_index": a.activeModal.Selected,
		}
	}
	return nil
}

// GetAppState returns the full application state.
// Implements state.AppInspector.
func (a *App) GetAppState() *state.AppState {
	activity := a.ensureActivityState().Snapshot()
	return &state.AppState{
		// Navigation
		Screen:          a.GetScreen(),
		SelectedIndex:   a.selectedIdx,
		ScrollOffset:    a.scrollOffset,
		HomeButton:      int(a.homeButton),
		MessageNavMode:  a.messageNavMode,
		FocusedMsgIndex: a.focusedMessageIdx,

		// Content
		ConversationID: a.currentConvID,
		Messages:       a.GetMessages(),
		MessageCount:   len(a.messages),

		// Input
		InputText:      a.GetInputText(),
		CursorPosition: a.GetCursorPosition(),

		// Modals
		ActiveModal: a.GetModalID(),
		ModalState:  a.GetModalData(),

		// Display
		ShowSidePanel:      a.showSidePanel,
		ShowThinking:       a.showThinking,
		ShowFullToolOutput: a.showFullToolOutput,
		OperatingMode:      a.operatingMode,

		// Streaming
		IsStreaming:      a.streamingMessage,
		UserScrolledAway: a.userScrolledAway,

		// Canonical activity
		ActivityPhase:  string(activity.Phase),
		ActivityLabel:  activity.Label,
		ActivityStatus: activity.ConvStatus,
		ActivityActive: activity.ConvIsActive,

		// Debug
		DebugVisible: a.debugScreen != nil && a.debugScreen.visible,

		// Performance
		ViewportDirty: a.viewportContentDirty,
		CacheValid:    !a.viewportContentDirty,

		// Git
		CurrentBranch: a.currentBranchCtx,
		BranchFilter:  branchFilterToString(a.branchFilter),
		Workspace:     a.appOptions.WorkspaceRoot,

		// Timestamp
		Timestamp: time.Now(),
	}
}

// GetConversations returns conversation list state.
// Implements state.AppInspector.
func (a *App) GetConversations() []state.ConversationState {
	conversations := make([]state.ConversationState, len(a.conversations))
	for i, conv := range a.conversations {
		conversations[i] = state.ConversationState{
			ID:          conv.ID,
			Title:       conv.Title,
			LastMessage: conv.Preview,
			UpdatedAt:   conv.LastMessage,
			Branch:      conv.Branch,
		}
	}
	return conversations
}

// GetTools returns tool state for current view.
// Implements state.AppInspector.
func (a *App) GetTools() []state.ToolState {
	var tools []state.ToolState

	// Collect tool calls from messages
	for msgIdx, msg := range a.messages {
		for _, tc := range msg.ToolCalls {
			expanded := true
			if a.collapseManager != nil {
				if st := a.collapseManager.GetState(msgIdx, tc.ID); st != nil {
					expanded = st.CollapseLevel == CollapseLevelFull
				}
			}

			tools = append(tools, state.ToolState{
				ID:        tc.ID,
				Name:      tc.Name,
				Status:    "complete",
				Expanded:  expanded,
				HasOutput: true,
			})
		}
	}

	return tools
}

// GetSettingsState returns settings state.
// Implements state.AppInspector.
func (a *App) GetSettingsState() *state.SettingsState {
	if a.settingsManager == nil {
		return nil
	}

	return &state.SettingsState{
		ActiveSection: "general", // Would need to track active section
		IsDirty:       false,
	}
}

// GetViewerState returns file viewer state.
// Implements state.AppInspector.
func (a *App) GetViewerState() *state.ViewerState {
	if a.viewerComponent == nil {
		return nil
	}

	return &state.ViewerState{
		TreeVisible: a.viewerComponent.showTree,
		TreeFocused: a.viewerComponent.focusTree,
	}
}

// GetAutocompleteState returns autocomplete state.
// Implements state.AppInspector.
func (a *App) GetAutocompleteState() *state.AutocompleteState {
	// Check mention autocomplete
	if a.mentionAutocomplete != nil && a.mentionAutocomplete.IsVisible() {
		return &state.AutocompleteState{
			Visible:       true,
			Query:         a.mentionAutocomplete.input,
			SelectedIndex: a.mentionAutocomplete.selectedIdx,
			Type:          "mention",
		}
	}

	// Check command autocomplete
	if a.cmdAutocomplete != nil && a.cmdAutocomplete.IsVisible() {
		return &state.AutocompleteState{
			Visible: true,
			Type:    "command",
		}
	}

	return &state.AutocompleteState{Visible: false}
}

// Helper functions

func branchFilterToString(bf BranchFilter) string {
	switch bf {
	case FilterAll:
		return "all"
	case FilterCurrentBranch:
		return "current"
	case FilterNoBranch:
		return "none"
	default:
		return "unknown"
	}
}

// ViewString returns the rendered UI for headless automation.
// This implements the capture.ViewStringer interface.
// The previous implementation rendered View.Layer through ultraviolet for a
// pixel-accurate snapshot; View.Layer was removed in the bubbletea v2
// upgrade, so we now return the cached rendered content directly.
func (a *App) ViewString() string {
	if a.width == 0 || a.height == 0 {
		return "Loading..."
	}

	view := a.View()
	if view.Content != "" {
		return view.Content
	}
	return a.lastRenderOutput
}

// GetStateSummary returns a state summary for debugging and testing.
func (a *App) GetStateSummary() string {
	var b strings.Builder
	b.WriteString(fmt.Sprintf("Screen: %s\n", a.GetScreen()))
	b.WriteString(fmt.Sprintf("Focus: %s\n", a.GetFocusedComponent()))
	b.WriteString(fmt.Sprintf("Messages: %d\n", len(a.messages)))

	if a.screen == ScreenChat {
		b.WriteString(fmt.Sprintf("Input: %s\n", a.GetInputText()))
		b.WriteString(fmt.Sprintf("Streaming: %v\n", a.streamingMessage))
	}

	if a.IsModalOpen() {
		b.WriteString(fmt.Sprintf("Modal: %s\n", a.GetModalID()))
	}

	// Add recent messages summary
	if len(a.messages) > 0 {
		b.WriteString("\nRecent messages:\n")
		start := len(a.messages) - 3
		if start < 0 {
			start = 0
		}
		for i := start; i < len(a.messages); i++ {
			msg := a.messages[i]
			content := msg.Content
			if len(content) > 50 {
				content = content[:50] + "..."
			}
			b.WriteString(fmt.Sprintf("  [%s] %s\n", msg.Role, content))
		}
	}

	return b.String()
}
