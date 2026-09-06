package settings

import (
	"fmt"
	"slices"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/chat/commands"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/i18n"
)

// TelemetryConsentRequestMsg is emitted when the user tries to enable the
// Telemetry toggle in General settings. The app catches it and shows the
// one-time consent modal; the modal's Accept/Decline callback then calls
// GetGeneralSettings().SetTelemetryEnabled(...).
type TelemetryConsentRequestMsg struct{}

func (m *Manager) HandleMessage(msg tea.Msg, cmdRegistry *commands.Registry) tea.Cmd {
	// Drain any cmd stashed by a section refresh (e.g. auth expired-token
	// refresh-on-open). SelectSection/RefreshSection callers don't propagate
	// cmds, so we dispatch it on the next message tick. BUG #1 wiring.
	if m.pendingCmd != nil {
		cmd := m.pendingCmd
		m.pendingCmd = nil
		return cmd
	}

	if plexusMsg, ok := msg.(plexusSyncResultMsg); ok {
		if m.model != nil {
			m.model.applyPlexusSyncResult(plexusMsg)
		}
		return nil
	}

	if readmeMsg, ok := msg.(commands.ModelReadmeMsg); ok {
		if m.model != nil {
			m.model.applyReadmeMsg(readmeMsg)
		}
		return nil
	}

	// Forward async auth messages to the inline OAuth flow handler.
	// We always route these (not just when SectionAuth is active) so that
	// background polling cmds (e.g., Gemini browser wait) are handled even
	// if the user navigated away — though IsInNestedState prevents that.
	if m.auth != nil {
		if cmd := m.auth.HandleMsg(msg); cmd != nil {
			return cmd
		}
	}

	// Handle voice settings messages
	if m.voice != nil {
		if cmd := m.voice.HandleMsg(msg); cmd != nil {
			return cmd
		}
	}

	// Handle paste messages - forward to active editor
	if pasteMsg, ok := msg.(tea.PasteMsg); ok {
		pastedText := pasteMsg.Content
		logDebug("[Settings] PasteMsg received: '%s'", pastedText)

		// Forward to auth settings if entering an authorization code
		if m.state.SelectedSection == SectionAuth && m.auth != nil && m.auth.IsEditingCode() {
			logDebug("[Settings] Forwarding paste to auth code input")
			m.auth.HandlePaste(pastedText)
			return nil
		}

		// Forward to model settings if in edit mode (covers both legacy SectionModel
		// and the unified SectionModels which reuses the same ModelSettings forms).
		if m.model != nil &&
			(m.state.SelectedSection == SectionModel || m.state.SelectedSection == SectionModels) &&
			m.model.formEditing {
			logDebug("[Settings] Forwarding paste to model settings form")
			return m.handleModelPaste(pastedText)
		}

		// Forward to system prompt settings if in create/edit mode
		if m.state.SelectedSection == SectionSystemPrompt &&
			(m.state.SystemPromptState == "create" || m.state.SystemPromptState == "edit") {
			logDebug("[Settings] Forwarding paste to system prompt form")
			return m.handleSystemPromptPaste(pastedText)
		}

		// Forward to hooks settings if in edit or chat mode
		if m.state.SelectedSection == SectionHooks &&
			(m.state.HooksState == "edit" || m.state.HooksState == "chat") {
			logDebug("[Settings] Forwarding paste to hooks form")
			return m.handleHooksPaste(pastedText)
		}

		// Forward to proxies settings if in form editing mode
		if m.state.SelectedSection == SectionProxies && m.proxies.formEditing {
			logDebug("[Settings] Forwarding paste to proxy form")
			return m.handleProxiesPaste(pastedText)
		}

		// Forward to MCP chat if in chat mode
		if m.state.SelectedSection == SectionMCP && m.state.MCPState == "mcp_chat" {
			logDebug("[Settings] Forwarding paste to MCP chat")
			return m.handleMCPChatPaste(pastedText)
		}

		// Forward to Agents chat if in chat mode
		if m.state.SelectedSection == SectionAgents && m.state.AgentsState == "chat" {
			logDebug("[Settings] Forwarding paste to agents chat")
			return m.handleAgentsChatPaste(pastedText)
		}

		return nil
	}

	// For other messages, return nil (not handled here)
	return nil
}

// handleSystemPromptPaste inserts pasted text into the currently editing form field
func (m *Manager) handleSystemPromptPaste(pastedText string) tea.Cmd {
	if m.state.SystemPromptEditingField == 0 {
		// Paste into name field
		m.state.SystemPromptFormName = m.state.SystemPromptFormName[:m.state.SystemPromptCursorPos] +
			pastedText +
			m.state.SystemPromptFormName[m.state.SystemPromptCursorPos:]
		m.state.SystemPromptCursorPos += len(pastedText)
		logDebug("[Settings] Pasted into System Prompt Name: cursor now at %d", m.state.SystemPromptCursorPos)
	} else {
		// Paste into content field
		m.state.SystemPromptFormContent = m.state.SystemPromptFormContent[:m.state.SystemPromptCursorPos] +
			pastedText +
			m.state.SystemPromptFormContent[m.state.SystemPromptCursorPos:]
		m.state.SystemPromptCursorPos += len(pastedText)
		logDebug("[Settings] Pasted into System Prompt Content: cursor now at %d", m.state.SystemPromptCursorPos)
	}
	return nil
}

// handleHooksPaste inserts pasted text into the currently editing hooks form field or chat input
func (m *Manager) handleHooksPaste(pastedText string) tea.Cmd {
	if m.state.HooksState == "chat" {
		// Paste into chat input
		m.state.HooksChatInput = m.state.HooksChatInput[:m.state.HooksCursorPos] +
			pastedText +
			m.state.HooksChatInput[m.state.HooksCursorPos:]
		m.state.HooksCursorPos += len(pastedText)
		logDebug("[Settings] Pasted into Hooks Chat: cursor now at %d", m.state.HooksCursorPos)
	} else if m.state.HooksState == "edit" {
		// Paste into form field based on current editing field
		switch m.state.HooksEditingField {
		case 0: // Name
			m.state.HooksFormName = m.state.HooksFormName[:m.state.HooksCursorPos] +
				pastedText +
				m.state.HooksFormName[m.state.HooksCursorPos:]
			m.state.HooksCursorPos += len(pastedText)
			logDebug("[Settings] Pasted into Hooks Name: cursor now at %d", m.state.HooksCursorPos)
		case 1: // Events
			m.state.HooksFormEvent = m.state.HooksFormEvent[:m.state.HooksCursorPos] +
				pastedText +
				m.state.HooksFormEvent[m.state.HooksCursorPos:]
			m.state.HooksCursorPos += len(pastedText)
			logDebug("[Settings] Pasted into Hooks Event: cursor now at %d", m.state.HooksCursorPos)
		case 2: // Command
			m.state.HooksFormCommand = m.state.HooksFormCommand[:m.state.HooksCursorPos] +
				pastedText +
				m.state.HooksFormCommand[m.state.HooksCursorPos:]
			m.state.HooksCursorPos += len(pastedText)
			logDebug("[Settings] Pasted into Hooks Command: cursor now at %d", m.state.HooksCursorPos)
		case 3: // Tool Matcher
			m.state.HooksFormToolMatcher = m.state.HooksFormToolMatcher[:m.state.HooksCursorPos] +
				pastedText +
				m.state.HooksFormToolMatcher[m.state.HooksCursorPos:]
			m.state.HooksCursorPos += len(pastedText)
			logDebug("[Settings] Pasted into Hooks ToolMatcher: cursor now at %d", m.state.HooksCursorPos)
		case 4: // Path Allowlist
			m.state.HooksFormPathAllowlist = m.state.HooksFormPathAllowlist[:m.state.HooksCursorPos] +
				pastedText +
				m.state.HooksFormPathAllowlist[m.state.HooksCursorPos:]
			m.state.HooksCursorPos += len(pastedText)
			logDebug("[Settings] Pasted into Hooks PathAllowlist: cursor now at %d", m.state.HooksCursorPos)
		case 5: // Path Denylist
			m.state.HooksFormPathDenylist = m.state.HooksFormPathDenylist[:m.state.HooksCursorPos] +
				pastedText +
				m.state.HooksFormPathDenylist[m.state.HooksCursorPos:]
			m.state.HooksCursorPos += len(pastedText)
			logDebug("[Settings] Pasted into Hooks PathDenylist: cursor now at %d", m.state.HooksCursorPos)
		case 6: // Action
			m.state.HooksFormAction = m.state.HooksFormAction[:m.state.HooksCursorPos] +
				pastedText +
				m.state.HooksFormAction[m.state.HooksCursorPos:]
			m.state.HooksCursorPos += len(pastedText)
			logDebug("[Settings] Pasted into Hooks Action: cursor now at %d", m.state.HooksCursorPos)
		case 7: // Timeout
			m.state.HooksFormTimeout = m.state.HooksFormTimeout[:m.state.HooksCursorPos] +
				pastedText +
				m.state.HooksFormTimeout[m.state.HooksCursorPos:]
			m.state.HooksCursorPos += len(pastedText)
			logDebug("[Settings] Pasted into Hooks Timeout: cursor now at %d", m.state.HooksCursorPos)
		}
	}
	return nil
}

// handleProxiesPaste inserts pasted text into the currently editing proxy form field
func (m *Manager) handleProxiesPaste(pastedText string) tea.Cmd {
	if !m.proxies.formEditing {
		return nil
	}

	// Use the proper method to handle paste
	m.proxies.HandlePaste(pastedText)

	// Log which field received the paste
	fieldNames := []string{"Name", "Display Name", "Base URL", "API Key", "Description"}
	fieldIndex := m.proxies.GetFormField()
	if fieldIndex < len(fieldNames) {
		logDebug("[Settings] Pasted into Proxy %s (field %d): '%s'", fieldNames[fieldIndex], fieldIndex, pastedText)
	}

	return nil
}

// handleMCPChatPaste inserts pasted text into the MCP chat input
func (m *Manager) handleMCPChatPaste(pastedText string) tea.Cmd {
	if m.state.MCPChatWaiting {
		return nil // Don't allow paste while waiting for response
	}

	// Append pasted text to the MCP chat input
	m.state.MCPChatInput += pastedText
	logDebug("[Settings] Pasted into MCP Chat: '%s' (total length: %d)", pastedText, len(m.state.MCPChatInput))
	return nil
}

// handleAgentsChatPaste inserts pasted text into the Agents chat input
func (m *Manager) handleAgentsChatPaste(pastedText string) tea.Cmd {
	if m.state.AgentsChatWaiting {
		return nil // Don't allow paste while waiting for response
	}

	// Append pasted text to the Agents chat input
	m.state.AgentsChatInput += pastedText
	logDebug("[Settings] Pasted into Agents Chat: '%s' (total length: %d)", pastedText, len(m.state.AgentsChatInput))
	return nil
}

// handleModelPaste inserts pasted text into the currently editing form field
func (m *Manager) handleModelPaste(pastedText string) tea.Cmd {
	// Check if we're in add_model or edit_model state (model form)
	if m.model.state == "add_model" || m.model.state == "edit_model" {
		if m.model.modelFormField == 0 { // Model ID
			m.model.modelFormID = m.model.modelFormID[:m.model.formCursorPos] + pastedText + m.model.modelFormID[m.model.formCursorPos:]
			m.model.formCursorPos += len(pastedText)
			logDebug("[Settings] Pasted into Model ID: cursor now at %d", m.model.formCursorPos)
		} else if m.model.modelFormField == 1 { // Model Display Name
			m.model.modelFormDisplayName = m.model.modelFormDisplayName[:m.model.formCursorPos] + pastedText + m.model.modelFormDisplayName[m.model.formCursorPos:]
			m.model.formCursorPos += len(pastedText)
			logDebug("[Settings] Pasted into Model Display Name: cursor now at %d", m.model.formCursorPos)
		} else if m.model.modelFormField == 2 { // Context
			m.model.modelFormContext = m.model.modelFormContext[:m.model.formCursorPos] + pastedText + m.model.modelFormContext[m.model.formCursorPos:]
			m.model.formCursorPos += len(pastedText)
			logDebug("[Settings] Pasted into Context: cursor now at %d", m.model.formCursorPos)
		}
	} else {
		// Provider form
		if m.model.formField == 1 { // Display Name
			m.model.formDisplayName = m.model.formDisplayName[:m.model.formCursorPos] + pastedText + m.model.formDisplayName[m.model.formCursorPos:]
			m.model.formCursorPos += len(pastedText)
			logDebug("[Settings] Pasted into Display Name: cursor now at %d", m.model.formCursorPos)
		} else if m.model.formField == 2 { // API Endpoint
			m.model.formAPIEndpoint = m.model.formAPIEndpoint[:m.model.formCursorPos] + pastedText + m.model.formAPIEndpoint[m.model.formCursorPos:]
			m.model.formCursorPos += len(pastedText)
			logDebug("[Settings] Pasted into API Endpoint: cursor now at %d", m.model.formCursorPos)
		} else if m.model.formField == 4 { // API Key
			m.model.formAPIKey = m.model.formAPIKey[:m.model.formCursorPos] + pastedText + m.model.formAPIKey[m.model.formCursorPos:]
			m.model.formCursorPos += len(pastedText)
			logDebug("[Settings] Pasted into API Key: cursor now at %d", m.model.formCursorPos)
		}
	}
	return nil
}

// HandleKey processes keyboard input for settings
func (m *Manager) HandleKey(key string, cmdRegistry *commands.Registry) tea.Cmd {
	// Handle based on focus
	if m.state.Focus == FocusSidebar {
		// Enter or 'l' in sidebar: if the current group is collapsed, expand it
		// instead of switching to content. Otherwise switch to content.
		if key == "enter" || key == "l" {
			m.state.Focus = FocusContent
			m.state.SelectedItem = 0
			m.state.ScrollOffset = 0
			return nil
		}
		return m.handleSidebarKey(key)
	}

	// Content focus - let content handlers process tab first
	var handled bool
	var cmd tea.Cmd

	// Content-specific handling
	switch m.state.SelectedSection {
	case SectionGeneral:
		cmd = m.handleGeneralKey(key)
		handled = cmd != nil || key != "tab"
	case SectionSecurity:
		cmd, handled = m.handleSecurityKeyWithResult(key)
	case SectionModel:
		cmd = m.handleModelKey(key, cmdRegistry)
		handled = cmd != nil || key != "tab"
	case SectionModels:
		cmd, handled = m.handleModelProfilesKeyWithResult(key)
	case SectionProxies:
		cmd = m.handleProxiesKey(key)
		handled = cmd != nil || key != "tab"
	case SectionAgents:
		cmd, handled = m.handleAgentsKeyWithResult(key)
	case SectionAgentProfiles:
		profileLog("[Manager.HandleKey] routing to handleProfilesKeyWithResult, key=%q", key)
		cmd, handled = m.handleProfilesKeyWithResult(key)
	case SectionConfigBundles:
		cmd, handled = m.handleConfigBundlesKeyWithResult(key)
	case SectionConfigSource:
		if m.configSource != nil {
			cmd = m.configSource.HandleKey(key, m.state)
			// Only mark as handled if it's not escape (escape goes to sidebar)
			handled = cmd != nil || (key != "tab" && key != "esc")
		}
	case SectionMCP:
		cmd = m.handleMCPKey(key)
		handled = cmd != nil || key != "tab"
	case SectionHooks:
		cmd, handled = m.handleHooksKeyWithResult(key)
	case SectionSkills:
		cmd, handled = m.handleSkillsKeyWithResult(key)
	case SectionPlugins:
		cmd, handled = m.handlePluginsKeyWithResult(key)
	case SectionSystemPrompt:
		cmd, handled = m.handleSystemPromptKeyWithResult(key)
	case SectionContext:
		cmd, handled = m.handleContextKeyWithResult(key)
	case SectionDisplay:
		cmd = m.handleDisplayKey(key, cmdRegistry)
		handled = cmd != nil || key != "tab"
	case SectionTheme:
		cmd = m.handleThemeKey(key)
		handled = true
	case SectionAuth:
		// Snapshot whether AuthSettings is in a sub-state that owns esc/h
		// (active OAuth flow, post-flow result screen, account picker, usage
		// view, Claude code entry) BEFORE dispatching, so esc still bubbles
		// to the sidebar only when we are at the top-level provider list.
		consumesBack := m.auth != nil && (m.auth.IsInFlow() || m.auth.IsShowingFlowResult() || m.auth.IsInAccountPicker() || m.auth.IsViewingUsage() || m.auth.IsEditingCode())
		cmd = m.handleAuthKey(key, cmdRegistry)
		if (key == "esc" || key == "h") && !consumesBack {
			handled = false
		} else {
			handled = cmd != nil || key != "tab"
		}
	case SectionCompaction:
		cmd = m.handleCompactionKey(key)
		handled = cmd != nil || key != "tab"
	case SectionPlan:
		handled = m.plan != nil && m.plan.HandleKey(key, m.state)
	case SectionWebSearch:
		handled = m.webSearch != nil && m.webSearch.HandleKey(key, m.state)
	case SectionReliability:
		cmd = m.handleReliabilityKey(key)
		handled = cmd != nil || key != "tab"
	case SectionAdvanced:
		cmd = m.handleAdvancedKey(key)
		handled = cmd != nil || key != "tab"
	case SectionCache:
		cmd = m.handleCacheKey(key)
		handled = cmd != nil || key != "tab"
	case SectionVoice:
		cmd = m.handleVoiceKey(key)
		handled = true
	case SectionUpdates:
		cmd = m.handleUpdatesKey(key)
		handled = true
	case SectionVault:
		handled = m.vault != nil && m.vault.HandleKey(key, m.state)
	case SectionComputerUse:
		if m.computerUse != nil {
			handled, _ = m.computerUse.HandleKey(key)
		}
	case SectionSteering:
		if m.steering != nil {
			cmd = m.steering.HandleKey(key, m.state)
		}
		handled = cmd != nil || key != "tab"
	}

	// 'h' or 'esc' goes back to sidebar when not editing text and key wasn't handled by content
	if (key == "h" || key == "esc") && !handled && !m.IsEditingText() {
		// If ESC is pressed, show exit confirmation modal
		if key == "esc" {
			m.state.ShowExitModal = true
			m.state.ExitModalChoice = 0 // Default to "Save & Exit"
			return nil
		}
		m.state.Focus = FocusSidebar
		return nil
	}

	// Mark dirty when a callback fired (cmd != nil) or an explicit save key was used.
	// Individual simple-toggle handlers also set Dirty directly.
	if cmd != nil || key == "ctrl+s" {
		m.state.Dirty = true
	}

	return cmd
}

// handleSidebarKey handles keyboard input when sidebar has focus
func (m *Manager) handleSidebarKey(key string) tea.Cmd {
	// Search input mode
	if m.state.SearchActive {
		switch key {
		case "esc":
			m.state.SearchActive = false
			m.state.SearchQuery = ""
		case "enter":
			m.state.SearchActive = false
			// Keep filter active, ensure selection is valid
			m.ensureValidSelection()
		case "backspace":
			if len(m.state.SearchQuery) > 0 {
				m.state.SearchQuery = m.state.SearchQuery[:len(m.state.SearchQuery)-1]
				m.ensureValidSelection()
			}
		default:
			if len(key) == 1 && key >= " " {
				m.state.SearchQuery += key
				m.ensureValidSelection()
			}
		}
		return nil
	}

	switch key {
	case "/":
		m.state.SearchActive = true
		m.state.SearchQuery = ""
		return nil
	case "esc":
		if m.state.SearchQuery != "" {
			m.state.SearchQuery = ""
			m.ensureValidSelection()
			return nil
		}
	case "up", "k":
		m.navigateSidebarUp()
	case "down", "j":
		m.navigateSidebarDown()
	}
	return nil
}

// navigateSidebarUp moves selection up through visible sections.
// When the current section is hidden (collapsed group), jumps to the nearest visible section.
// When ALL groups are collapsed, navigates between group representatives.
func (m *Manager) navigateSidebarUp() {
	visible := m.visibleSections()
	if len(visible) > 0 {
		currentIdx := -1
		for i, sec := range visible {
			if sec == m.state.SelectedSection {
				currentIdx = i
				break
			}
		}
		if currentIdx > 0 {
			m.SelectSection(visible[currentIdx-1])
		} else if currentIdx == -1 {
			// Current section is in a collapsed group — jump to last visible
			m.SelectSection(visible[len(visible)-1])
		}
		return
	}
	// All groups collapsed — navigate between group headers
	m.navigateCollapsedGroupUp()
}

// navigateSidebarDown moves selection down through visible sections.
// When the current section is hidden (collapsed group), jumps to the nearest visible section.
// When ALL groups are collapsed, navigates between group representatives.
func (m *Manager) navigateSidebarDown() {
	visible := m.visibleSections()
	if len(visible) > 0 {
		currentIdx := -1
		for i, sec := range visible {
			if sec == m.state.SelectedSection {
				currentIdx = i
				break
			}
		}
		if currentIdx >= 0 && currentIdx < len(visible)-1 {
			m.SelectSection(visible[currentIdx+1])
		} else if currentIdx == -1 {
			// Current section is in a collapsed group — jump to first visible
			m.SelectSection(visible[0])
		}
		return
	}
	// All groups collapsed — navigate between group headers
	m.navigateCollapsedGroupDown()
}

// navigateCollapsedGroupUp moves to the previous collapsed group's first section.
func (m *Manager) navigateCollapsedGroupUp() {
	groups := m.allGroupFirstSections()
	if len(groups) == 0 {
		return
	}
	currentGroup := groupForSection(m.state.SelectedSection)
	currentIdx := -1
	for i, g := range groups {
		if g.group == currentGroup {
			currentIdx = i
			break
		}
	}
	if currentIdx > 0 {
		m.SelectSection(groups[currentIdx-1].section)
	}
}

// navigateCollapsedGroupDown moves to the next collapsed group's first section.
func (m *Manager) navigateCollapsedGroupDown() {
	groups := m.allGroupFirstSections()
	if len(groups) == 0 {
		return
	}
	currentGroup := groupForSection(m.state.SelectedSection)
	currentIdx := -1
	for i, g := range groups {
		if g.group == currentGroup {
			currentIdx = i
			break
		}
	}
	if currentIdx >= 0 && currentIdx < len(groups)-1 {
		m.SelectSection(groups[currentIdx+1].section)
	}
}

type groupFirstSection struct {
	group   string
	section Section
}

// allGroupFirstSections returns the first section of each group (respecting search filter).
func (m *Manager) allGroupFirstSections() []groupFirstSection {
	grouped := m.groupedSections()
	filtered := m.filteredSections(grouped)
	var result []groupFirstSection
	for _, groupName := range SectionGroups {
		sections, exists := filtered[groupName]
		if !exists || len(sections) == 0 {
			continue
		}
		result = append(result, groupFirstSection{group: groupName, section: sections[0].ID})
	}
	return result
}

// ensureValidSelection ensures the selected section is visible after filter/collapse changes
func (m *Manager) ensureValidSelection() {
	visible := m.visibleSections()
	if len(visible) == 0 {
		return
	}
	// Check if current selection is still visible
	if slices.Contains(visible, m.state.SelectedSection) {
		return // Still visible
	}
	// Move to first visible section
	m.SelectSection(visible[0])
}

func (m *Manager) handleGeneralKey(key string) tea.Cmd {
	switch key {
	case "up", "k":
		if m.state.SelectedItem > 0 {
			m.state.SelectedItem--
			if m.state.SelectedItem < m.state.ScrollOffset {
				m.state.ScrollOffset = m.state.SelectedItem
			}
		}
	case "down", "j":
		if m.state.SelectedItem < 7 { // 8 items total (0-7)
			m.state.SelectedItem++
			if m.state.SelectedItem >= m.state.ScrollOffset+m.state.MaxVisible {
				m.state.ScrollOffset = m.state.SelectedItem - m.state.MaxVisible + 1
			}
		}
	case " ", "space", "enter":
		// Toggle or adjust based on item
		switch m.state.SelectedItem {
		case 0: // Side Panel
			m.general.ToggleSidePanel()
			m.state.Dirty = true
		case 1: // Compact Mode
			m.general.ToggleCompactMode()
			m.state.Dirty = true
		case 2: // Debug Mode
			m.general.ToggleDebugMode()
			m.state.Dirty = true
		case 3: // Telemetry — show consent modal before enabling
			if !m.general.GetTelemetryEnabled() {
				// Turning ON: request the one-time consent modal from the app.
				// The modal's Accept/Decline callback calls SetTelemetryEnabled.
				return func() tea.Msg { return TelemetryConsentRequestMsg{} }
			}
			// Turning OFF needs no consent.
			m.general.SetTelemetryEnabled(false)
			m.state.Dirty = true
		case 4: // Micro-Compaction
			m.general.ToggleMicroCompaction()
			m.state.Dirty = true
		case 5: // Retention Count - adjust with enter
			// Could open number input, for now just do nothing
		case 6: // Max History - adjust with enter
			// Could open number input, for now just do nothing
		case 7: // Language
			m.general.CycleLanguage(1)
			m.state.Dirty = true
		}
	case "left", "h":
		if m.state.SelectedItem == 5 { // Retention Count
			m.general.AdjustMicroRetentionCount(-1)
			m.state.Dirty = true
		} else if m.state.SelectedItem == 6 { // Max History
			m.general.AdjustMaxHistory(-10)
			m.state.Dirty = true
		} else if m.state.SelectedItem == 7 { // Language
			m.general.CycleLanguage(-1)
			m.state.Dirty = true
		}
	case "right", "l":
		if m.state.SelectedItem == 5 { // Retention Count
			m.general.AdjustMicroRetentionCount(1)
			m.state.Dirty = true
		} else if m.state.SelectedItem == 6 { // Max History
			m.general.AdjustMaxHistory(10)
			m.state.Dirty = true
		} else if m.state.SelectedItem == 7 { // Language
			m.general.CycleLanguage(1)
			m.state.Dirty = true
		}
	case "-":
		if m.state.SelectedItem == 5 {
			m.general.AdjustMicroRetentionCount(-1)
			m.state.Dirty = true
		} else if m.state.SelectedItem == 6 {
			m.general.AdjustMaxHistory(-10)
			m.state.Dirty = true
		} else if m.state.SelectedItem == 7 {
			m.general.CycleLanguage(-1)
			m.state.Dirty = true
		}
	case "+", "=":
		if m.state.SelectedItem == 5 {
			m.general.AdjustMicroRetentionCount(1)
			m.state.Dirty = true
		} else if m.state.SelectedItem == 6 {
			m.general.AdjustMaxHistory(10)
			m.state.Dirty = true
		} else if m.state.SelectedItem == 7 {
			m.general.CycleLanguage(1)
			m.state.Dirty = true
		}
	}

	return nil
}

func (m *Manager) handleModelKey(key string, cmdRegistry *commands.Registry) tea.Cmd {
	// Let model settings handle its own keys (it has multi-state picker)
	var handled bool = m.model.HandleKey(key)
	var readmeCmd tea.Cmd = m.model.ensureReadmeCmd()
	var pendingCmd tea.Cmd = m.model.TakePendingCmd()
	if handled {
		if pendingCmd != nil && readmeCmd != nil {
			return tea.Batch(pendingCmd, readmeCmd)
		}
		if pendingCmd != nil {
			return pendingCmd
		}
		return readmeCmd
	}

	// If not handled by model settings, check if we should trigger callback
	var cmd tea.Cmd
	if m.state.SelectedItem == 0 && (key == "enter" || key == " ") {
		if m.onModelChange != nil {
			cmd = m.onModelChange()
		}
	}
	if cmd != nil && readmeCmd != nil {
		return tea.Batch(cmd, readmeCmd)
	}
	if readmeCmd != nil {
		return readmeCmd
	}
	return cmd
}

func (m *Manager) handleDisplayKey(key string, cmdRegistry *commands.Registry) tea.Cmd {
	switch key {
	case "up", "k":
		if m.state.SelectedItem > 0 {
			m.state.SelectedItem--
		}
	case "down", "j":
		if m.state.SelectedItem < 6 { // 7 items (0-6): 0-5 toggles + item 6 spinner type
			m.state.SelectedItem++
		}
	case "left", "h":
		// Handle spinner type selection
		if m.state.SelectedItem == 6 {
			m.display.PrevSpinnerType()
			m.state.Dirty = true
			// Sync display settings to persistent storage
			if m.onDisplaySettingsSync != nil {
				m.onDisplaySettingsSync(
					m.display.GetShowThinking(),
					m.display.GetShowFullToolOutput(),
					m.display.GetRichAnimations(),
					m.display.GetCopySelectionShortcut(),
					m.display.GetAutoCopySelectionOnMouse(),
				)
			}
			if m.onSpinnerChange != nil {
				return m.onSpinnerChange(m.display.GetSpinnerType())
			}
		}
	case "right", "l":
		// Handle spinner type selection
		if m.state.SelectedItem == 6 {
			m.display.NextSpinnerType()
			m.state.Dirty = true
			// Sync display settings to persistent storage
			if m.onDisplaySettingsSync != nil {
				m.onDisplaySettingsSync(
					m.display.GetShowThinking(),
					m.display.GetShowFullToolOutput(),
					m.display.GetRichAnimations(),
					m.display.GetCopySelectionShortcut(),
					m.display.GetAutoCopySelectionOnMouse(),
				)
			}
			if m.onSpinnerChange != nil {
				return m.onSpinnerChange(m.display.GetSpinnerType())
			}
		}
	case " ", "space", "enter":
		switch m.state.SelectedItem {
		case 0: // Show Thinking
			m.display.ToggleShowThinking()
		case 1: // Show Full Tool Output
			m.display.ToggleShowFullToolOutput()
		case 2: // Rich Animations
			m.display.ToggleRichAnimations()
		case 3: // Copy Selection Shortcut
			m.display.ToggleCopySelectionShortcut()
		case 4: // Auto-copy Selection
			m.display.ToggleAutoCopySelectionOnMouse()
		case 5: // Simple Startup View
			m.display.ToggleStartupView()
			m.state.Dirty = true
			if m.onStartupViewChange != nil {
				return m.onStartupViewChange(m.display.GetStartupView())
			}
		case 6: // Spinner Type - cycle on enter/space as well
			m.display.NextSpinnerType()
			if m.onSpinnerChange != nil {
				return m.onSpinnerChange(m.display.GetSpinnerType())
			}
		}
		m.state.Dirty = true
		// Sync display settings to persistent storage
		if m.onDisplaySettingsSync != nil {
			return m.onDisplaySettingsSync(
				m.display.GetShowThinking(),
				m.display.GetShowFullToolOutput(),
				m.display.GetRichAnimations(),
				m.display.GetCopySelectionShortcut(),
				m.display.GetAutoCopySelectionOnMouse(),
			)
		}
		if m.onRenderChange != nil {
			return m.onRenderChange()
		}
	}
	return nil
}

// handleThemeKey handles keyboard input for the Theme & Background settings section.
func (m *Manager) handleThemeKey(key string) tea.Cmd {
	if m.theme == nil {
		return nil
	}
	presets := m.themePresets()
	totalItems := len(presets) + len(BackgroundModes)

	switch key {
	case "up", "k":
		if m.state.SelectedItem > 0 {
			m.state.SelectedItem--
		}
	case "down", "j":
		if m.state.SelectedItem < totalItems-1 {
			m.state.SelectedItem++
		}
	case " ", "enter":
		idx := m.state.SelectedItem
		if idx < len(presets) {
			// Selecting a colour palette
			m.theme.SetThemeName(presets[idx].Name)
		} else {
			// Selecting a background mode
			bgIdx := idx - len(presets)
			if bgIdx >= 0 && bgIdx < len(BackgroundModes) {
				m.theme.SetBackgroundMode(BackgroundModes[bgIdx].Key)
			}
		}
		m.state.Dirty = true
		if m.onThemeChange != nil {
			return m.onThemeChange(m.theme.GetThemeName(), m.theme.GetBackgroundMode())
		}
	}
	return nil
}

func (m *Manager) handleAuthKey(key string, cmdRegistry *commands.Registry) tea.Cmd {
	// Delegate directly to the AuthSettings instance — it owns the OAuth flow
	// state machines for Claude / Codex / Gemini and the account picker.
	if m.auth == nil {
		return nil
	}
	return m.auth.HandleKey(key)
}

func (m *Manager) handleMCPKey(key string) tea.Cmd {
	state := m.state

	// Handle configure_tools state
	if state.MCPState == "configure_tools" {
		switch key {
		case "esc", "backspace":
			state.MCPState = "main"
			state.MCPFocusedPanel = "buttons"
			state.MCPConfigureToolsSelected = 0
			state.MCPConfigureToolsScrollOffset = 0
			logDebug("[MCP] Returned to main view from configure_tools")
		default:
			if m.mcp != nil {
				m.mcp.HandleConfigureToolsKey(key, state)
			}
		}
		return nil
	}

	// Handle mcp_config state (MCP Servers screen)
	if state.MCPState == "mcp_config" {
		// If in add or edit mode, let the form handle ESC first
		if state.MCPServersAddMode || state.MCPServersEditMode {
			if m.mcp != nil {
				m.mcp.HandleMCPServersKey(key, state)
			}
		} else {
			switch key {
			case "esc", "backspace":
				state.MCPState = "main"
				state.MCPFocusedPanel = "buttons"
				state.MCPServersSelected = 0
				state.MCPServersScrollOffset = 0
				state.MCPServersAddMode = false
				state.MCPServersEditMode = false
				logDebug("[MCP] Returned to main view from mcp_config")
			default:
				if m.mcp != nil {
					m.mcp.HandleMCPServersKey(key, state)
				}
			}
		}
		return nil
	}

	// Handle tool_tester state
	if state.MCPState == "tool_tester" {
		if m.mcp != nil && m.mcp.toolTester != nil {
			// Let tool tester handle all keys except esc at list level
			if key == "esc" && m.mcp.toolTester.GetState() == "list" {
				state.MCPState = "main"
				state.MCPFocusedPanel = "buttons"
				logDebug("[MCP] Returned to main view from tool_tester")
			} else {
				m.mcp.toolTester.HandleKey(key)
			}
		}
		return nil
	}

	// Handle mcp_chat state
	if state.MCPState == "mcp_chat" {
		switch key {
		case "esc":
			state.MCPState = "main"
			state.MCPFocusedPanel = "buttons"
			logDebug("[MCP] Returned to main view from mcp_chat")
		case "up", "k":
			// Scroll chat history up
			if state.MCPChatScrollOffset > 0 {
				state.MCPChatScrollOffset--
			}
		case "down", "j":
			// Scroll chat history down
			state.MCPChatScrollOffset++
		case "ctrl+r":
			// Reset chat
			if m.mcp != nil {
				m.mcp.ResetChat()
				state.MCPChatScrollOffset = 0
			}
		case "enter":
			// Send message
			if !state.MCPChatWaiting && state.MCPChatInput != "" {
				if m.mcp != nil && m.mcp.onChatSend != nil {
					state.MCPChatWaiting = true
					m.mcp.onChatSend(state.MCPChatInput)
					state.MCPChatInput = ""
				}
			}
		case "backspace":
			// Delete character from input
			if len(state.MCPChatInput) > 0 {
				state.MCPChatInput = state.MCPChatInput[:len(state.MCPChatInput)-1]
			}
		case " ", "space":
			// Handle space key explicitly
			if !state.MCPChatWaiting {
				state.MCPChatInput += " "
			}
		default:
			// Handle text input - accept any printable string
			// This handles regular typing, unicode, and pasted text
			if !state.MCPChatWaiting && key != "" && !isControlKey(key) {
				state.MCPChatInput += key
			}
		}
		return nil
	}

	// Handle error_detail state
	if state.MCPState == "error_detail" {
		switch key {
		case "esc", "backspace":
			state.MCPState = "mcp_config"
			state.MCPErrorDetailScroll = 0
			logDebug("[MCP] Returned to mcp_config from error_detail")
		case "r", "R":
			// Retry connection
			if state.MCPCurrentServer != nil && m.mcp != nil && m.mcp.onServerReconnect != nil {
				err := m.mcp.onServerReconnect(state.MCPCurrentServer.Config.Name)
				if err != nil {
					logDebug("[MCP] Reconnect failed: %v", err)
				} else {
					logDebug("[MCP] Reconnecting server: %s", state.MCPCurrentServer.Config.Name)
					// Return to server list to see connection status
					state.MCPState = "mcp_config"
					state.MCPErrorDetailScroll = 0
				}
			}
		case "e", "E":
			// Edit server configuration
			if state.MCPCurrentServer != nil {
				// Find server index
				if m.mcp != nil {
					for i, server := range m.mcp.servers {
						if server.Config.Name == state.MCPCurrentServer.Config.Name {
							state.MCPServersSelected = i
							state.MCPServersEditMode = true
							state.MCPState = "mcp_config"
							state.MCPErrorDetailScroll = 0

							// Pre-populate form fields
							state.AddFormField = 0
							state.AddFormName = server.Config.Name
							state.AddFormType = string(server.Config.GetType())
							state.AddFormCommand = server.Config.Command
							state.AddFormArgs = strings.Join(server.Config.Args, " ")
							state.AddFormURL = server.Config.URL

							// Headers
							if len(server.Config.Headers) > 0 {
								var headerPairs []string
								for k, v := range server.Config.Headers {
									headerPairs = append(headerPairs, fmt.Sprintf("%s=%s", k, v))
								}
								state.AddFormHeaders = strings.Join(headerPairs, ",")
							} else {
								state.AddFormHeaders = ""
							}

							// OAuth
							if server.Config.OAuth != nil {
								state.AddFormClientID = server.Config.OAuth.ClientID
								state.AddFormScopes = server.Config.OAuth.Scopes
							} else {
								state.AddFormClientID = ""
								state.AddFormScopes = ""
							}

							// Env
							if len(server.Config.Env) > 0 {
								var envPairs []string
								for k, v := range server.Config.Env {
									envPairs = append(envPairs, fmt.Sprintf("%s=%s", k, v))
								}
								state.AddFormEnv = strings.Join(envPairs, ",")
							} else {
								state.AddFormEnv = ""
							}

							state.AddFormWorkDir = server.Config.WorkingDir
							if server.Config.Timeout > 0 {
								state.AddFormTimeout = fmt.Sprintf("%d", server.Config.Timeout)
							} else {
								state.AddFormTimeout = ""
							}
							break
						}
					}
				}
			}
		case "up", "k":
			// Scroll error message up
			if state.MCPErrorDetailScroll > 0 {
				state.MCPErrorDetailScroll--
			}
		case "down", "j":
			// Scroll error message down
			state.MCPErrorDetailScroll++
		}
		return nil
	}

	if state.MCPState == "main" && (key == "s" || key == "S") {
		m.SelectSection(SectionSecurity)
		m.state.Focus = FocusContent
		return nil
	}

	switch key {
	case "tab":
		// Cycle through: buttons -> enabled -> disabled -> buttons
		switch state.MCPFocusedPanel {
		case "buttons":
			state.MCPFocusedPanel = "enabled"
		case "enabled":
			state.MCPFocusedPanel = "disabled"
		case "disabled":
			state.MCPFocusedPanel = "buttons"
		default:
			state.MCPFocusedPanel = "buttons"
		}

	case "left", "h":
		if state.MCPFocusedPanel == "buttons" {
			if state.MCPSelectedButton > 0 {
				state.MCPSelectedButton--
			}
		}

	case "right", "l":
		if state.MCPFocusedPanel == "buttons" {
			if state.MCPSelectedButton < 3 { // 4 buttons (0-3)
				state.MCPSelectedButton++
			}
		}

	case "down", "j":
		if state.MCPFocusedPanel == "buttons" {
			// Move from buttons to enabled panel
			state.MCPFocusedPanel = "enabled"
		} else if state.MCPFocusedPanel == "enabled" {
			// Scroll down in enabled tools
			if m.mcp != nil && len(m.mcp.enabledTools) > 0 {
				state.MCPScrollOffsetEnabled++
			}
		} else if state.MCPFocusedPanel == "disabled" {
			// Scroll down in disabled tools
			if m.mcp != nil && len(m.mcp.disabledTools) > 0 {
				state.MCPScrollOffsetDisabled++
			}
		}

	case "up", "k":
		if state.MCPFocusedPanel == "enabled" {
			if state.MCPScrollOffsetEnabled > 0 {
				state.MCPScrollOffsetEnabled--
			} else {
				// Move back to buttons
				state.MCPFocusedPanel = "buttons"
			}
		} else if state.MCPFocusedPanel == "disabled" {
			if state.MCPScrollOffsetDisabled > 0 {
				state.MCPScrollOffsetDisabled--
			} else {
				// Move back to buttons
				state.MCPFocusedPanel = "buttons"
			}
		}

	case "pagedown", "ctrl+d":
		if state.MCPFocusedPanel == "enabled" {
			state.MCPScrollOffsetEnabled += 10
		} else if state.MCPFocusedPanel == "disabled" {
			state.MCPScrollOffsetDisabled += 10
		}

	case "pageup", "ctrl+u":
		if state.MCPFocusedPanel == "enabled" {
			state.MCPScrollOffsetEnabled -= 10
			if state.MCPScrollOffsetEnabled < 0 {
				state.MCPScrollOffsetEnabled = 0
			}
		} else if state.MCPFocusedPanel == "disabled" {
			state.MCPScrollOffsetDisabled -= 10
			if state.MCPScrollOffsetDisabled < 0 {
				state.MCPScrollOffsetDisabled = 0
			}
		}

	case "enter", " ":
		if state.MCPFocusedPanel == "buttons" {
			// Handle button activation - navigate to sub-screen
			switch state.MCPSelectedButton {
			case 0:
				// Configure Tools button
				state.MCPState = "configure_tools"
				logDebug("[MCP] Entered configure tools screen")
			case 1:
				// MCP Configuration button
				state.MCPState = "mcp_config"
				logDebug("[MCP] Entered MCP configuration screen")
			case 2:
				// Tool Tester button
				state.MCPState = "tool_tester"
				if m.mcp.toolTester != nil {
					m.mcp.toolTester.SetState("list")
				}
				logDebug("[MCP] Entered tool tester screen")
			case 3:
				// MCP Assistant button
				state.MCPState = "mcp_chat"
				state.MCPChatInput = ""
				state.MCPChatScrollOffset = 0
				state.MCPChatWaiting = false
				logDebug("[MCP] Entered MCP chat screen")
			}
		}

	case "esc", "backspace":
		// Navigate back to main view if in a sub-screen
		if state.MCPState != "main" {
			state.MCPState = "main"
			state.MCPFocusedPanel = "buttons"
			logDebug("[MCP] Returned to main view")
		}
	}

	return nil
}

func (m *Manager) handleCompactionKey(key string) tea.Cmd {
	// Let compaction settings handle the key
	if m.compaction.HandleKey(key, m.state) {
		return nil
	}
	return nil
}

func (m *Manager) handleReliabilityKey(key string) tea.Cmd {
	if m.reliability != nil {
		m.reliability.HandleKey(key, m.state)
	}
	return nil
}

func (m *Manager) handleAdvancedKey(key string) tea.Cmd {
	// First, let advanced settings handle it (for subsection navigation)
	if m.advanced.HandleKey(key, m.state) {
		return nil
	}

	// Handle main advanced menu navigation
	switch key {
	case "up", "k":
		if m.state.SelectedItem > 0 {
			m.state.SelectedItem--
			if m.state.SelectedItem < m.state.ScrollOffset {
				m.state.ScrollOffset = m.state.SelectedItem
			}
		}
	case "down", "j":
		if m.state.SelectedItem < 5 { // 6 items: Max Tokens, Temperature, Stream, Cache, Advanced Tool Mode, Defer Threshold
			m.state.SelectedItem++
			if m.state.SelectedItem >= m.state.ScrollOffset+m.state.MaxVisible {
				m.state.ScrollOffset = m.state.SelectedItem - m.state.MaxVisible + 1
			}
		}
	case " ", "space", "enter":
		switch m.state.SelectedItem {
		case 0: // Max Tokens - no action on enter
		case 1: // Temperature - no action on enter
		case 2: // Stream Response
			m.advanced.ToggleStreamResponse()
			m.state.Dirty = true
		case 3: // Cache Prompts
			m.advanced.ToggleCachePrompts()
			m.state.Dirty = true
		case 4: // Advanced Tool Mode
			m.advanced.ToggleAdvancedToolMode()
			m.state.Dirty = true
		case 5: // Defer Token Threshold - no action on enter
		}
	case "left", "h", "-":
		switch m.state.SelectedItem {
		case 0: // Max Tokens
			m.advanced.AdjustMaxTokens(-256)
			m.state.Dirty = true
		case 1: // Temperature
			m.advanced.AdjustTemperature(-0.1)
			m.state.Dirty = true
		case 5: // Defer Token Threshold
			m.advanced.AdjustDeferTokenThreshold(-50)
			m.state.Dirty = true
		}
	case "right", "l", "+", "=":
		switch m.state.SelectedItem {
		case 0: // Max Tokens
			m.advanced.AdjustMaxTokens(256)
			m.state.Dirty = true
		case 1: // Temperature
			m.advanced.AdjustTemperature(0.1)
			m.state.Dirty = true
		case 5: // Defer Token Threshold
			m.advanced.AdjustDeferTokenThreshold(50)
			m.state.Dirty = true
		}
	}

	return nil
}

// handleCacheKey handles keyboard input for cache settings
func (m *Manager) handleCacheKey(key string) tea.Cmd {
	if m.cache == nil {
		return nil
	}
	m.cache.HandleKey(key)
	return nil
}

// handleVoiceKey handles keyboard input for voice settings
func (m *Manager) handleVoiceKey(key string) tea.Cmd {
	if m.voice == nil {
		return nil
	}
	return m.voice.Update(key)
}

func (m *Manager) handleHooksKeyWithResult(key string) (tea.Cmd, bool) {
	// Delegate to hooks settings
	handled := m.hooks.HandleKey(key, m.state)
	return nil, handled
}

func (m *Manager) handleSkillsKeyWithResult(key string) (tea.Cmd, bool) {
	// Delegate to skills settings
	if m.skills != nil {
		handled := m.skills.HandleKey(key, m.state)
		return nil, handled
	}
	return nil, false
}

func (m *Manager) handlePluginsKeyWithResult(key string) (tea.Cmd, bool) {
	// Delegate to plugins settings
	if m.plugins != nil {
		handled := m.plugins.HandleKey(key, m.state)
		return nil, handled
	}
	return nil, false
}

func (m *Manager) handleSystemPromptKeyWithResult(key string) (tea.Cmd, bool) {
	// Delegate to system prompt settings
	if m.systemPrompt != nil {
		handled := m.systemPrompt.HandleKey(key, m.state)

		// If prompt was activated, trigger callback
		if key == "a" || key == " " || key == "enter" {
			if m.state.SystemPromptState == "list" && m.onPromptChange != nil {
				prompt := m.systemPrompt.GetActivePromptWithOAuth()
				return m.onPromptChange(prompt), handled
			}
		}
		return nil, handled
	}
	return nil, false
}

func (m *Manager) handleSecurityKeyWithResult(key string) (tea.Cmd, bool) {
	// Delegate to security settings
	if m.security != nil {
		handled := m.security.HandleKey(key, m.state)
		if handled {
			return nil, true
		}
		// If esc was not handled (no search/form active), switch to sidebar
		if key == "esc" {
			m.state.Focus = FocusSidebar
			return nil, true
		}
	}
	return nil, false
}

func (m *Manager) handleContextKeyWithResult(key string) (tea.Cmd, bool) {
	// Delegate to context settings
	if m.context != nil {
		handled := m.context.HandleKey(key, m.state)
		return nil, handled
	}
	return nil, false
}

func (m *Manager) handleAgentsKeyWithResult(key string) (tea.Cmd, bool) {
	// Delegate to agents settings
	if m.agents != nil {
		handled := m.agents.HandleKey(key, m.state)
		return nil, handled
	}
	return nil, false
}

func (m *Manager) handleProfilesKeyWithResult(key string) (tea.Cmd, bool) {
	profileLog("[Manager.handleProfilesKeyWithResult] key=%q profiles_nil=%v", key, m.profiles == nil)
	// Delegate to profile settings
	if m.profiles != nil {
		handled := m.profiles.HandleKey(key, m.state)
		profileLog("[Manager.handleProfilesKeyWithResult] HandleKey returned handled=%v", handled)
		// If a profile was activated as default, fire the model-switch callback.
		activatedID := m.profiles.TakeActivatedProfileID()
		profileLog("[Manager.handleProfilesKeyWithResult] activatedID=%q onProfileChange_nil=%v", activatedID, m.onProfileChange == nil)
		if activatedID != "" && m.onProfileChange != nil {
			profileLog("[Manager.handleProfilesKeyWithResult] firing onProfileChange(%s)", activatedID)
			return m.onProfileChange(activatedID), handled
		}

		// If the active profile's roles were just saved, re-sync the SDK model
		// so the in-memory agent uses the new configuration immediately.
		if savedID := m.profiles.TakeSavedProfileID(); savedID != "" {
			profileLog("[Manager.handleProfilesKeyWithResult] savedID=%q", savedID)

			// Resolve the profile name for the notification (best-effort).
			savedName := savedID
			if pm := m.profiles.GetManager(); pm != nil {
				if prof, err := pm.GetProfile(savedID); err == nil && prof != nil {
					savedName = prof.Name
				}
			}

			// Always fire the save notification so the user sees a toast.
			var notifCmd tea.Cmd
			if m.onProfileSaved != nil {
				notifCmd = m.onProfileSaved(savedID, savedName)
			}

			// If it's the active profile, also trigger a model re-sync.
			if m.profiles.GetManager() != nil &&
				m.profiles.GetManager().GetActiveProfileID() == savedID &&
				m.onProfileChange != nil {
				profileLog("[Manager.handleProfilesKeyWithResult] active profile saved, triggering model re-sync")
				syncCmd := m.onProfileChange(savedID)
				return tea.Batch(notifCmd, syncCmd), handled
			}

			return notifCmd, handled
		}

		return nil, handled
	}
	return nil, false
}

// handleModelProfilesKeyWithResult handles the unified Models section (profiles + providers)
func (m *Manager) handleModelProfilesKeyWithResult(key string) (tea.Cmd, bool) {
	if m.modelProfiles == nil {
		return nil, false
	}
	modelOwnsKey := m.modelProfiles.model != nil && m.modelProfiles.model.state == "confirm_delete"
	// When modelProfiles is showing the OAuth authentication screen, route
	// directly to the shared AuthSettings (v2 tea.Cmd) so flow-start commands
	// propagate to the bubbletea runtime — modelProfiles.HandleKey returns
	// a v1 tea.Cmd that we cannot forward.
	if m.modelProfiles.state == MPStateAuthentication && m.auth != nil {
		consumesBack := m.auth.IsInFlow() || m.auth.IsShowingFlowResult() || m.auth.IsInAccountPicker() || m.auth.IsViewingUsage() || m.auth.IsEditingCode()
		if (key == "esc" || key == "backspace") && !consumesBack {
			m.modelProfiles.state = MPStateMenu
			return nil, true
		}
		return m.auth.HandleKey(key), true
	}
	// HandleKey returns a tea.Cmd for async section work. Preserve that command
	// so provider actions such as Plexus "r" / Sync aliases actually execute.
	cmd := m.modelProfiles.HandleKey(key, m.state)
	// If a profile was activated as default, fire the model-switch callback.
	activatedID := m.modelProfiles.TakeActivatedProfileID()
	if activatedID != "" && m.onProfileChange != nil {
		profileCmd := m.onProfileChange(activatedID)
		if cmd != nil && profileCmd != nil {
			return tea.Batch(cmd, profileCmd), true
		}
		if profileCmd != nil {
			return profileCmd, true
		}
		return cmd, true
	}
	// Mark as handled for navigation keys so the sidebar doesn't steal them.
	handled := modelOwnsKey || key == "esc" || key == "enter" || key == "up" || key == "down" ||
		key == "k" || key == "j" || key == "n" || key == "e" || key == "d"

	// If editing text in a form (provider or model), mark printable characters as handled
	if m.modelProfiles.model != nil && (m.modelProfiles.model.formEditing ||
		(m.modelProfiles.model.modelFormField >= 0 && m.modelProfiles.model.modelFormField <= 2)) {
		// When editing text, mark single-character input as handled
		if len(key) == 1 && key[0] >= ' ' && key[0] <= '~' {
			handled = true
		}
		// Also mark common editing keys as handled
		if key == "backspace" || key == "delete" || key == "left" || key == "right" ||
			key == "home" || key == "end" || key == "ctrl+c" || key == "ctrl+h" ||
			key == "ctrl+d" || key == "ctrl+a" || key == "ctrl+e" || key == "ctrl+b" ||
			key == "ctrl+f" {
			handled = true
		}
	}
	// If ModelProfiles produced async work (for example Plexus sync), treat the
	// key as handled even if it is not one of the generic navigation keys.
	return cmd, handled || cmd != nil
}

// handleConfigBundlesKeyWithResult handles config bundles keys with result
func (m *Manager) handleConfigBundlesKeyWithResult(key string) (tea.Cmd, bool) {
	// Delegate to config bundles settings
	if m.configBundles != nil {
		// Convert key string to tea.KeyPressMsg for Update method
		var keyMsg tea.KeyPressMsg
		switch key {
		case "up", "k":
			keyMsg = tea.KeyPressMsg{Code: tea.KeyUp}
		case "down", "j":
			keyMsg = tea.KeyPressMsg{Code: tea.KeyDown}
		case "enter":
			keyMsg = tea.KeyPressMsg{Code: tea.KeyEnter}
		case "esc":
			keyMsg = tea.KeyPressMsg{Code: tea.KeyEscape}
		default:
			// For single character keys, use the first rune
			if len(key) > 0 {
				keyMsg = tea.KeyPressMsg{Code: rune(key[0]), Text: key}
			}
		}
		prevState := m.configBundles.state
		cmd := m.configBundles.Update(keyMsg)
		// If state changed or cmd returned, the key was handled
		handled := cmd != nil || m.configBundles.state != prevState
		// Navigation keys are always handled (up/down/enter/esc) even if state didn't change
		if key == "up" || key == "k" || key == "down" || key == "j" || key == "enter" || key == "esc" {
			handled = true
		}
		// In nested states (export/import/confirm), all keys are handled (form input)
		if m.configBundles.IsInNestedState() {
			handled = true
		}
		return cmd, handled
	}
	return nil, false
}

// renderHints renders keyboard hints at the bottom
func (m *Manager) renderHints(width int, th Theme) string {
	// At very narrow widths, skip hints entirely to save space
	if width < 45 {
		return ""
	}

	var hints string

	if m.state.Focus == FocusSidebar {
		if width < 65 {
			hints = i18n.T("settings.hints.sidebar.compact")
		} else {
			hints = i18n.T("settings.hints.sidebar")
		}
	} else {
		switch m.state.SelectedSection {
		case SectionGeneral:
			hints = i18n.T("settings.hints.general")
		case SectionSecurity:
			switch m.state.SecurityRulesState {
			case "add_form":
				hints = i18n.T("settings.hints.security.add")
			case "action_menu":
				hints = i18n.T("settings.hints.security.delete")
			default:
				if m.state.SecuritySearchActive {
					hints = i18n.T("settings.hints.security.search")
				} else {
					hints = i18n.T("settings.hints.security")
				}
			}
		case SectionModel:
			switch m.model.state {
			case "menu":
				hints = i18n.T("settings.hints.model.menu")
			case "browse_models":
				hints = i18n.T("settings.hints.model.browse")
			case "alias_variants":
				hints = i18n.T("settings.hints.model.aliases")
			case "manage_providers":
				hints = i18n.T("settings.hints.model.providers")
			case "add_provider", "edit_provider":
				hints = i18n.T("settings.hints.model.provider_form")
			case "provider_models":
				hints = i18n.T("settings.hints.model.provider_models")
			case "models":
				hints = i18n.T("settings.hints.model.models")
			default:
				hints = i18n.T("settings.hints.model.default")
			}
		case SectionModels:
			if m.modelProfiles != nil {
				switch m.modelProfiles.GetState() {
				case MPStateMenu:
					hints = i18n.T("settings.hints.model_profiles.menu")
				case MPStateProfileList:
					hints = i18n.T("settings.hints.model_profiles.list")
				case MPStateManageProviders:
					hints = i18n.T("settings.hints.model_profiles.providers")
				case MPStateAddProvider, MPStateEditProvider:
					hints = i18n.T("settings.hints.model_profiles.provider_form")
				case MPStateProviderModels:
					hints = i18n.T("settings.hints.model_profiles.provider_models")
				default:
					hints = i18n.T("settings.hints.model_profiles.default")
				}
			}
		case SectionMCP:
			switch m.state.MCPState {
			case "configure_tools":
				hints = i18n.T("settings.hints.mcp.tools")
			default:
				switch m.state.MCPFocusedPanel {
				case "buttons":
					hints = i18n.T("settings.hints.mcp.buttons")
				case "enabled", "disabled":
					hints = i18n.T("settings.hints.mcp.list")
				default:
					hints = i18n.T("settings.hints.mcp.default")
				}
			}
		case SectionHooks:
			switch m.state.HooksState {
			case "main":
				hints = i18n.T("settings.hints.hooks.main")
			case "chat":
				hints = i18n.T("settings.hints.hooks.chat")
			case "action_menu":
				hints = i18n.T("settings.hints.hooks.action")
			case "edit":
				hints = i18n.T("settings.hints.hooks.edit")
			default:
				hints = i18n.T("settings.hints.hooks.main")
			}
		case SectionSystemPrompt:
			switch m.state.SystemPromptState {
			case "list":
				hints = i18n.T("settings.hints.system_prompt.list")
			case "create", "edit":
				hints = i18n.T("settings.hints.system_prompt.edit")
			case "preview":
				hints = i18n.T("settings.hints.system_prompt.preview")
			default:
				hints = i18n.T("settings.hints.select")
			}
		case SectionAgents:
			switch m.state.AgentsState {
			case "list":
				hints = i18n.T("settings.hints.agents.list")
			case "action_menu":
				hints = i18n.T("settings.hints.confirm")
			case "create", "edit":
				switch m.state.AgentsFormTab {
				case 0:
					hints = i18n.T("settings.hints.agents.form")
				case 1:
					hints = i18n.T("settings.hints.agents.toggles")
				case 2:
					hints = i18n.T("settings.hints.agents.toggles")
				case 3:
					hints = i18n.T("settings.hints.agents.adjust")
				default:
					hints = i18n.T("settings.hints.agents.tabs")
				}
			case "preview":
				hints = i18n.T("settings.hints.back_to_menu")
			default:
				hints = i18n.T("settings.hints.select")
			}
		case SectionAgentProfiles:
			switch m.state.ProfilesState {
			case "edit_roles":
				if m.profiles != nil && m.profiles.IsPickerFocused() {
					hints = i18n.T("settings.hints.agent_profiles.chain")
				} else {
					hints = i18n.T("settings.hints.agent_profiles.roles")
				}
			default: // "list"
				hints = i18n.T("settings.hints.agent_profiles.list")
			}
		case SectionSkills:
			switch m.state.SkillsState {
			case "search":
				hints = i18n.T("settings.hints.extensions.search")
			case "detail":
				hints = i18n.T("settings.hints.extensions.detail")
			default:
				hints = i18n.T("settings.hints.skills")
			}
		case SectionPlugins:
			switch m.state.PluginsState {
			case "search":
				hints = i18n.T("settings.hints.extensions.search")
			case "detail":
				hints = i18n.T("settings.hints.extensions.detail")
			case "marketplace":
				hints = i18n.T("settings.hints.plugins.marketplace")
			default:
				hints = i18n.T("settings.hints.plugins")
			}
		case SectionContext:
			switch m.state.ContextState {
			case "mcp_servers":
				hints = i18n.T("settings.hints.context.servers")
			case "mcp_picker":
				hints = i18n.T("settings.hints.context.picker")
			case "mcp_prompt_args":
				hints = i18n.T("settings.hints.context.arguments")
			case "detail":
				hints = i18n.T("settings.hints.back")
			default:
				hints = i18n.T("settings.hints.context")
			}
		case SectionDisplay:
			hints = i18n.T("settings.hints.display")
		case SectionTheme:
			hints = i18n.T("settings.hints.theme")
		case SectionAuth:
			// Auth is now delegated to Models > Authentication
			// The actual UI and hints come from modelProfiles (SectionModels)
			hints = i18n.T("settings.hints.select")
		case SectionReliability:
			hints = i18n.T("settings.hints.reliability")
		case SectionPlan:
			hints = i18n.T("settings.hints.plan")
		case SectionWebSearch:
			hints = i18n.T("settings.hints.web_search")
		case SectionAdvanced:
			hints = i18n.T("settings.hints.advanced")
		case SectionCache:
			hints = i18n.T("settings.hints.cache")
		case SectionVoice:
			hints = i18n.T("settings.hints.voice")
		case SectionConfigSource:
			hints = i18n.T("settings.hints.config_source")
		case SectionVault:
			switch m.state.VaultState {
			case "locked":
				hints = i18n.T("settings.hints.vault.locked")
			case "unlocking":
				hints = i18n.T("settings.hints.vault.unlocking")
			case "unlocked":
				hints = i18n.T("settings.hints.vault.unlocked")
			case "error":
				hints = i18n.T("settings.hints.vault.error")
			default:
				hints = i18n.T("settings.hints.vault.locked")
			}
		}
	}

	hintStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(th.TextMuted)).
		Background(lipgloss.Color(th.BG)).
		Italic(true).
		Padding(0, 2).
		Width(width)

	return hintStyle.Render(hints)
}

// GetProxySettings returns proxy settings
func (m *Manager) GetProxySettings() *ProxySettings {
	return m.proxies
}

// handleProxiesKey handles keyboard input for proxy settings
func (m *Manager) handleProxiesKey(key string) tea.Cmd {
	if m.proxies.formEditing {
		// Handle form editing
		switch key {
		case "esc":
			m.proxies.CancelForm()
			return nil
		case "ctrl+s", "enter":
			// Enter saves the form
			if err := m.proxies.SaveForm(); err != nil {
				logDebug("Failed to save proxy: %v", err)
			}
			m.state.Dirty = true
			return nil
		case "tab", "down", "ctrl+n":
			// Move to next field
			m.proxies.NextFormField()
			return nil
		case "shift+tab", "up", "ctrl+p":
			// Move to previous field
			m.proxies.PrevFormField()
			return nil
		case "backspace", "ctrl+h":
			// Handle backspace in form
			currentValue := m.proxies.GetCurrentFormValue()
			if len(currentValue) > 0 {
				m.proxies.HandleFormInput(currentValue[:len(currentValue)-1])
			}
			return nil
		case "ctrl+u":
			// Clear current field
			m.proxies.ClearCurrentField()
			return nil
		case "space":
			// Handle space as text input
			currentValue := m.proxies.GetCurrentFormValue()
			m.proxies.HandleFormInput(currentValue + " ")
			return nil
		default:
			// Handle text input - accept printable characters and common keys
			// This includes letters, numbers, symbols, and spaces
			if len(key) == 1 || key == "space" {
				char := key
				if key == "space" {
					char = " "
				}
				currentValue := m.proxies.GetCurrentFormValue()
				m.proxies.HandleFormInput(currentValue + char)
			}
			// Consume all other keys to prevent them from propagating (e.g., 'q', 'Q')
			return nil
		}
	}

	// Normal proxy list navigation
	switch key {
	case "up", "k":
		if m.state.SelectedItem > 0 {
			m.state.SelectedItem--
			if m.state.SelectedItem < m.state.ScrollOffset {
				m.state.ScrollOffset = m.state.SelectedItem
			}
		}
		return nil
	case "down", "j":
		proxies := m.proxies.GetProxies()
		if m.state.SelectedItem < len(proxies)-1 {
			m.state.SelectedItem++
			if m.state.SelectedItem >= m.state.ScrollOffset+m.state.MaxVisible {
				m.state.ScrollOffset = m.state.SelectedItem - m.state.MaxVisible + 1
			}
		}
		return nil
	case "enter", "e", "E":
		// Enter or 'E' key opens the proxy for editing
		m.proxies.StartEditProxy(m.state.SelectedItem)
		return nil
	case " ", "space":
		// Space toggles proxy enabled/disabled
		m.proxies.ToggleProxy(m.state.SelectedItem)
		m.state.Dirty = true
		if m.onModelChange != nil {
			return m.onModelChange()
		}
		return nil
	case "d", "D":
		// Set as default
		m.proxies.SetDefaultProxy(m.state.SelectedItem)
		m.state.Dirty = true
		if m.onModelChange != nil {
			return m.onModelChange()
		}
		return nil
	case "a", "A":
		// Add new proxy
		m.proxies.StartAddProxy()
		return nil
	case "x", "X":
		// Delete proxy
		if err := m.proxies.DeleteProxy(m.state.SelectedItem); err != nil {
			logDebug("Failed to delete proxy: %v", err)
		} else {
			m.state.Dirty = true
		}
		return nil
	}

	return nil
}

// IsInNestedState returns true if any setting section is currently in a nested/editing state
// that should handle escape/q keys before allowing exit from settings
func (m *Manager) IsInNestedState() bool {
	state := m.state
	switch state.SelectedSection {
	case SectionModel:
		// Model has multi-state picker (browse_models, alias_variants, manage_providers, etc.)
		return m.model != nil && m.model.IsInNestedState()
	case SectionModels:
		// ModelProfiles has multi-state menu (menu, manage_profiles, manage_providers, add_provider, etc.)
		return m.modelProfiles != nil && m.modelProfiles.IsInNestedState()
	case SectionProxies:
		// Proxies has form editing mode
		return m.proxies != nil && m.proxies.IsFormEditing()
	case SectionHooks:
		// Hooks has multiple states: chat, action_menu, edit, add, templates
		// Base state is "main" (NOT "list")
		return state.HooksState != "main"
	case SectionSystemPrompt:
		// System prompt has: action_menu, create, edit, preview
		return state.SystemPromptState != "list"
	case SectionMCP:
		// MCP has: configure_tools, mcp_config, tool_tester, error_detail, mcp_chat
		// Also check for add/edit mode in MCP servers
		return state.MCPState != "main" || state.MCPServersAddMode || state.MCPServersEditMode
	case SectionPlugins:
		// Plugins has: search, detail, marketplace
		return state.PluginsState != "list"
	case SectionSkills:
		// Skills has: search, detail
		return state.SkillsState != "list"
	case SectionAgents:
		// Agents has: action_menu, create, edit, preview
		// Also check if form is being edited
		return state.AgentsState != "list" || state.AgentsFormEditing
	case SectionAgentProfiles:
		// Profiles has: list, edit_roles
		// Nested state when FallbackPicker is in select_provider/select_model
		return state.ProfilesState != "list"
	case SectionContext:
		// Context has: detail, mcp_servers, mcp_picker, mcp_prompt_args
		return state.ContextState != "list"
	case SectionSecurity:
		// Security has: action_menu, add_form
		return state.SecurityRulesState != "list"
	case SectionConfigBundles:
		// ConfigBundles has: export, import, confirm_apply, confirm_delete
		return m.configBundles != nil && m.configBundles.IsInNestedState()
	case SectionReliability:
		// Reliability has: fallback mode, rate limit edit mode
		return m.reliability != nil && m.reliability.IsInNestedState()
	case SectionAdvanced:
		return false
	case SectionCompaction:
		return false
	case SectionAuth:
		// Nested when any AuthSettings sub-view (flow / picker / usage / code
		// entry / post-flow result) is showing.
		return m.auth != nil && (m.auth.IsInFlow() || m.auth.IsShowingFlowResult() || m.auth.IsInAccountPicker() || m.auth.IsViewingUsage() || m.auth.IsEditingCode())
	// These sections have no nested states:
	// SectionGeneral, SectionDisplay, SectionCache
	default:
		return false
	}
}

// IsEditingText returns true if the current section is editing a text field
// This is used to determine if 'q' should be treated as text input
func (m *Manager) IsEditingText() bool {
	state := m.state
	switch state.SelectedSection {
	case SectionPlugins:
		return state.PluginsState == "search"
	case SectionSkills:
		return state.SkillsState == "search"
	case SectionHooks:
		return state.HooksState == "edit" || state.HooksState == "add"
	case SectionSystemPrompt:
		return state.SystemPromptState == "edit" || state.SystemPromptState == "create"
	case SectionModel:
		return m.model != nil && m.model.IsEditingText()
	case SectionModels:
		return m.modelProfiles != nil && m.modelProfiles.IsEditingText()
	case SectionProxies:
		return m.proxies != nil && m.proxies.IsFormEditing()
	case SectionAgents:
		return state.AgentsFormEditing
	case SectionAgentProfiles:
		return false // FallbackPicker handles its own text input internally
	case SectionSecurity:
		return state.SecurityRulesState == "add_form"
	case SectionContext:
		return state.ContextMCPFilterMode ||
			(state.ContextState == "mcp_prompt_args" &&
				state.ContextMCPArgsSelected < len(state.ContextMCPArgsPromptArgs))
	case SectionAdvanced:
		return false
	case SectionConfigBundles:
		return m.configBundles != nil && m.configBundles.IsEditingText()
	case SectionAuth:
		// AuthSettings is editing text only while the user is typing/pasting
		// the Claude OAuth code.
		return m.auth != nil && m.auth.IsEditingCode()
	default:
		return false
	}
}

// isControlKey returns true if the key is a control key that shouldn't be added to input
func isControlKey(key string) bool {
	controlKeys := map[string]bool{
		"ctrl+c": true, "ctrl+d": true, "ctrl+z": true,
		"ctrl+r": true, // reset (we handle separately)
		"up":     true, "down": true, "left": true, "right": true,
		"home": true, "end": true, "pageup": true, "pagedown": true,
		"tab": true, "shift+tab": true,
		"esc": true, "enter": true, "backspace": true,
		"delete": true, "insert": true,
		"f1": true, "f2": true, "f3": true, "f4": true,
		"f5": true, "f6": true, "f7": true, "f8": true,
		"f9": true, "f10": true, "f11": true, "f12": true,
	}
	return controlKeys[key]
}
