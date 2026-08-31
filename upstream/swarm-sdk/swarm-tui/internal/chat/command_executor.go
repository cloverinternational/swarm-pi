package chat

import tea "charm.land/bubbletea/v2"

// executeCommand handles command palette selections
func (a *App) executeCommand(commandID string) {
	logDebug("Executing command: %s", commandID)

	switch commandID {
	case "model":
		// Open model command
		if modelCmd, ok := a.cmdRegistry.Get("model"); ok {
			modelCmd.Execute([]string{})
			if modelCmd.IsInteractive() {
				cmdWidth := a.width
				if a.showSidePanel && a.width >= MinWidthForSidePanel {
					cmdWidth = a.width - SidePanelWidth
				}
				// Send window size to initialize dimensions
				modelCmd.Update(tea.WindowSizeMsg{Width: cmdWidth, Height: a.height})
				a.activeCommand = modelCmd
				a.cmdAutocomplete.Hide()
				a.mentionAutocomplete.Hide()
			}
		}

	case "auth":
		// Open auth command
		if authCmd, ok := a.cmdRegistry.Get("auth"); ok {
			authCmd.Execute([]string{})
			if authCmd.IsInteractive() {
				cmdWidth := a.width
				if a.showSidePanel && a.width >= MinWidthForSidePanel {
					cmdWidth = a.width - SidePanelWidth
				}
				// Send window size to initialize dimensions
				authCmd.Update(tea.WindowSizeMsg{Width: cmdWidth, Height: a.height})
				a.activeCommand = authCmd
				a.cmdAutocomplete.Hide()
				a.mentionAutocomplete.Hide()
			}
		}

	case "render":
		// Open render settings command
		if renderCmd, ok := a.cmdRegistry.Get("render"); ok {
			renderCmd.Execute([]string{})
			if renderCmd.IsInteractive() {
				cmdWidth := a.width
				if a.showSidePanel && a.width >= MinWidthForSidePanel {
					cmdWidth = a.width - SidePanelWidth
				}
				// Send window size to initialize dimensions
				renderCmd.Update(tea.WindowSizeMsg{Width: cmdWidth, Height: a.height})
				a.activeCommand = renderCmd
				a.cmdAutocomplete.Hide()
				a.mentionAutocomplete.Hide()
			}
		}

	case "mcp":
		// Open MCP server management command
		if mcpCmd, ok := a.cmdRegistry.Get("mcp"); ok {
			mcpCmd.Execute([]string{})
			if mcpCmd.IsInteractive() {
				cmdWidth := a.width
				if a.showSidePanel && a.width >= MinWidthForSidePanel {
					cmdWidth = a.width - SidePanelWidth
				}
				// Send window size to initialize dimensions
				mcpCmd.Update(tea.WindowSizeMsg{Width: cmdWidth, Height: a.height})
				a.activeCommand = mcpCmd
				a.cmdAutocomplete.Hide()
				a.mentionAutocomplete.Hide()
			}
		}

	case "hooks":
		// Open hooks dashboard command
		if hooksCmd, ok := a.cmdRegistry.Get("hooks"); ok {
			hooksCmd.Execute([]string{})
			if hooksCmd.IsInteractive() {
				cmdWidth := a.width
				if a.showSidePanel && a.width >= MinWidthForSidePanel {
					cmdWidth = a.width - SidePanelWidth
				}
				// Send window size to initialize dimensions
				hooksCmd.Update(tea.WindowSizeMsg{Width: cmdWidth, Height: a.height})
				a.activeCommand = hooksCmd
				a.cmdAutocomplete.Hide()
				a.mentionAutocomplete.Hide()
			}
		}

	// Quick-access switchers (triggered from command palette)
	case "switch_model":
		if a.settingsManager != nil {
			ms := a.settingsManager.GetModelSettings()
			a.modelSwitcher.Show(ms.GetProviders(), ms.CurrentProvider(), ms.CurrentModel())
		}
	case "switch_agent":
		if a.settingsManager != nil {
			as := a.settingsManager.GetAgentsSettings()
			a.agentSwitcher.Show(as.GetAgents(), as.GetDefaultAgentID())
		}
	case "switch_prompt":
		if a.settingsManager != nil {
			sp := a.settingsManager.GetSystemPromptSettings()
			a.promptSwitcher.Show(sp.GetPrompts(), sp.GetActivePromptName())
		}
	case "switch_profile":
		if a.settingsManager != nil && a.sdk != nil {
			pm := a.sdk.GetProfileManager()
			if pm != nil {
				profiles := pm.ListProfiles()
				config := pm.GetConfig()
				activeProfile, _ := pm.GetActiveProfile()
				activeProfileID := ""
				if activeProfile != nil {
					activeProfileID = activeProfile.ID
				}
				defaultProfileID := ""
				if config != nil {
					defaultProfileID = config.DefaultProfile
				}
				a.profileSwitcher.Show(profiles, activeProfileID, defaultProfileID)
			}
		}

	// Navigation
	case "settings":
		a.screen = ScreenHome
		a.homeButton = ButtonSettings
		a.homeInputFocused = false
		if a.homeInput != nil {
			a.homeInput.Blur()
		}
		a.updateMCPSettingsData()
	case "new_chat":
		a.showNewChatModal()
	case "conversations":
		a.screen = ScreenChats
	}
}
