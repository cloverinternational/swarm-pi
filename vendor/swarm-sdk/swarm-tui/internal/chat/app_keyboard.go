package chat

import (
	"context"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/agent"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/fallback"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/mode"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/chat/commands"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/chat/settings"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/i18n"
)

func (a *App) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	key := msg.String()

	// DEBUG: Log all key presses
	logDebug("[KEY] handleKey received: key='%s' screen=%d sdk=%v", key, a.screen, a.sdk != nil)

	// HIGHEST PRIORITY: New chat modal gets ALL keys when active
	// Must be before any other handlers to capture space, shift+tab, etc.
	if a.newChatModal != nil {
		cmd := a.newChatModal.Update(msg)
		return a, cmd
	}
	if a.approvalModal != nil {
		a.approvalModal.Update(key)
		// Always force a repaint so the modal update (including dismiss) is
		// reflected immediately rather than waiting for the next tick.
		return a, forceRepaint()
	}
	// Detail viewer (Enter-to-view takeover for a bash command or sub-agent):
	// intercept all keys while it is active. Esc/q returns to the chat.
	if a.detailViewer != nil {
		switch key {
		case "esc", "q":
			a.detailViewer = nil
			return a, forceRepaint()
		case "up", "k":
			a.detailViewer.ScrollUp(1)
			return a, nil
		case "down", "j":
			a.detailViewer.ScrollDown(1)
			return a, nil
		case "pgup", "ctrl+b":
			a.detailViewer.PageUp()
			return a, nil
		case "pgdown", "ctrl+f":
			a.detailViewer.PageDown()
			return a, nil
		case "ctrl+u":
			a.detailViewer.HalfPageUp()
			return a, nil
		case "ctrl+d":
			a.detailViewer.HalfPageDown()
			return a, nil
		case "home", "g":
			a.detailViewer.GotoTop()
			return a, nil
		case "end", "G":
			a.detailViewer.GotoBottom()
			return a, nil
		}
		// Swallow other keys so nothing leaks into the chat/input behind it.
		return a, nil
	}
	// Task activity modal (Ctrl+T): intercept all keys when open
	if a.taskActivityModal != nil {
		if key == "esc" || key == "q" {
			a.taskActivityModal = nil
			return a, forceRepaint()
		}
		a.taskActivityModal.Update(key)
		return a, forceRepaint()
	}
	// Plan approval bar: intercept all keys when active (higher priority than regular questions)
	// But first, handle scroll keys for the plan viewer
	if a.planQuestionModal != nil {
		// Handle scroll keys for the dedicated plan viewer (legacy path).
		if a.planViewer != nil {
			switch key {
			case "up", "k":
				a.planViewer.ScrollUp(1)
				return a, nil
			case "down", "j":
				a.planViewer.ScrollDown(1)
				return a, nil
			case "pgup", "ctrl+b":
				a.planViewer.PageUp()
				return a, nil
			case "pgdown", "ctrl+f":
				a.planViewer.PageDown()
				return a, nil
			case "ctrl+u":
				a.planViewer.HalfPageUp()
				return a, nil
			case "home", "g":
				a.planViewer.GotoTop()
				return a, nil
			case "end", "G":
				a.planViewer.GotoBottom()
				return a, nil
			}
		} else if a.planQuestionModal.IsChoiceMode() && a.screen == ScreenChat {
			// Current plan-approval flow renders the plan as a scrollable
			// assistant message in the main chat viewport (planViewer is nil).
			// While the choice bar (y/c/n) is active we must still let the user
			// scroll the plan above it to read it in full before deciding.
			// Route navigation keys to msgViewport, and expose y/c/n as direct
			// hotkeys so the arrow keys stay free for scrolling.
			switch key {
			case "up", "k":
				a.msgViewport.ScrollUp(1)
				return a, forceRepaint()
			case "down", "j":
				a.msgViewport.ScrollDown(1)
				return a, forceRepaint()
			case "pgup", "ctrl+b":
				a.msgViewport.ScrollUp(a.msgViewport.Height)
				return a, forceRepaint()
			case "pgdown", "ctrl+f":
				a.msgViewport.ScrollDown(a.msgViewport.Height)
				return a, forceRepaint()
			case "ctrl+u":
				a.msgViewport.HalfPageUp()
				return a, forceRepaint()
			case "ctrl+d":
				a.msgViewport.HalfPageDown()
				return a, forceRepaint()
			case "home", "g":
				a.msgViewport.GotoTop()
				return a, forceRepaint()
			case "end", "G":
				a.msgViewport.GotoBottom()
				return a, forceRepaint()
			case "y", "Y", "c", "C", "n", "N":
				// Direct hotkey selection of the plan choice (y: Approve,
				// c: Approve + Clear Context, n: Reject / Revise).
				if a.planQuestionModal.SelectChoiceByPrefix(strings.ToLower(key)) {
					if a.pendingAnimCmd != nil {
						cmd := a.pendingAnimCmd
						a.pendingAnimCmd = nil
						return a, cmd
					}
					return a, forceRepaint()
				}
			}
		}
		a.planQuestionModal.Update(key)
		if a.pendingAnimCmd != nil {
			cmd := a.pendingAnimCmd
			a.pendingAnimCmd = nil
			return a, cmd
		}
		return a, forceRepaint()
	}
	if a.questionModal != nil {
		a.questionModal.Update(key)
		return a, forceRepaint()
	}

	// HIGHEST PRIORITY: Debug screen (Ctrl+D) - can toggle from any screen
	if key == "ctrl+d" {
		a.debugScreen.Toggle()
		logDebug("Debug screen toggled: visible=%v", a.debugScreen.IsVisible())
		// If we opened straight onto the Usage tab, kick a cached-first load so a
		// cold cache fills in without the user having to re-select the tab. The
		// callback is gated (see usageActivate in app_init.go) and is a no-op when
		// fresh cached data already exists.
		if a.debugScreen.IsVisible() {
			if cmd := a.debugScreen.maybeActivateUsage(); cmd != nil {
				return a, cmd
			}
		}
		return a, nil
	}

	// Attach & Monitor is a full-screen takeover. Modal/detail handlers above
	// deliberately get first refusal so Ctrl+Q cannot leak through them.
	if a.attachScreen != nil {
		handled, cmd := a.attachScreen.Update(msg)
		if !handled {
			a.attachScreen = nil
		}
		return a, cmd
	}
	if key == "ctrl+q" {
		if a.attachScreenFactory != nil {
			a.attachScreen = a.attachScreenFactory()
		}
		if a.attachScreen == nil {
			a.attachScreen = NewAttachScreen(nil, nil, nil)
		}
		a.attachScreen.SetSize(a.width, a.height)
		return a, tea.Batch(
			forceRepaint(),
			func() tea.Msg { return attachMonitorTickMsg{} },
		)
	}

	// HIGHEST PRIORITY outside the home screen: Shift+Tab cycles permission
	// level (Always Ask -> Balanced -> Permissive). ScreenHome multiplexes
	// several tabs and overlays, so it resolves Shift+Tab in handleHomeKey.
	if key == "shift+tab" && a.screen != ScreenHome {
		logDebug("[PERMISSIONS] Shift+Tab detected! sdk=%v", a.sdk != nil)
		a.cyclePermissionLevel()
		return a, nil
	}

	// Ctrl+Shift+Tab cycles operating mode (OFF -> PLAN -> ACT -> AUTO -> DEBUG -> OFF)
	if key == "ctrl+shift+tab" {
		{
			logDebug("[MODE] Ctrl+Shift+Tab detected! sdk=%v", a.sdk != nil)
			if a.sdk != nil {
				currentMode := a.sdk.GetOperatingMode()
				if currentMode == "" {
					currentMode = "off"
				}
				nextModeID := mode.NextMode(currentMode)
				logDebug("[MODE] Cycling from %s to %s", currentMode, nextModeID)
				return a, func() tea.Msg {
					return commands.ModeSwitchMsg{Mode: nextModeID}
				}
			}
			return a, nil
		}
	}

	// Note: Ctrl+T handler moved down to use ThinkingToggleMsg for proper SDK sync

	// Toggle full tool output display (Ctrl+O)
	if key == "ctrl+o" {
		// Save scroll anchor BEFORE changing display mode

		// Save scroll anchor BEFORE any changes
		anchor := a.saveScrollAnchor()
		if anchor != nil {
		}

		// Log BEFORE state

		// Toggle verbose mode
		a.showFullToolOutput = !a.showFullToolOutput

		// Tell updateViewportIncremental to skip its auto-restore
		a.skipScrollRestoreOnce = true

		// Invalidate and re-render with new display settings
		a.invalidateViewportCache()
		a.updateViewportContent()

		// Restore to the original viewing position using content-aware anchoring
		a.restoreScrollAnchor(anchor)

		return a, nil
	}

	// Toggle hook display (Ctrl+H)
	if key == "ctrl+h" {
		// Toggle hook visibility
		a.renderSettings.ShowHooks = !a.renderSettings.ShowHooks
		logDebug("[HOOKS] Ctrl+H pressed - hook display toggled to %v", a.renderSettings.ShowHooks)
		if a.renderSettings.ShowHooks {
			a.addNotification("info", i18n.T("residual.keyboard.hook_display_enabled"))
		} else {
			a.addNotification("info", i18n.T("residual.keyboard.hook_display_hidden"))
		}

		// Invalidate and re-render with new display settings
		a.invalidateViewportCache()
		a.updateViewportContent()
		_ = a.renderSettings.Save()

		return a, nil
	}

	// Ctrl+S (chat screen only) toggles steering hooks live, so the agent
	// can continue while the user removes guidance. Ctrl+Shift+S toggles ALL
	// hooks as a master kill-switch. Home screen keeps its existing Ctrl+S
	// binding (navigate to Settings tab) — we only claim Ctrl+S in chat.
	if key == "ctrl+s" && a.screen == ScreenChat {
		if a.sdk != nil && a.sdk.hooksManager != nil {
			count, enabled, err := a.sdk.hooksManager.ToggleGroupByPrefix("steering")
			if err != nil {
				a.addNotification("error", i18n.T("residual.keyboard.steering_toggle_failed", err))
				return a, forceRepaint()
			}
			if count == 0 {
				a.addNotification("info", i18n.T("residual.keyboard.no_steering_hooks"))
				return a, forceRepaint()
			}
			state := i18n.T("residual.keyboard.off")
			if enabled {
				state = i18n.T("residual.keyboard.on")
			}
			a.addNotification("info", i18n.T("residual.keyboard.steering_hooks_state", state, count))
		}
		// Repaint so the sidebar's steering badge and the notification toast
		// land in the same frame as the toggle — otherwise the badge lags
		// until the next unrelated event.
		return a, forceRepaint()
	}
	if key == "ctrl+shift+s" {
		if a.sdk != nil && a.sdk.hooksManager != nil {
			count, enabled, err := a.sdk.hooksManager.ToggleAllHooks()
			if err != nil {
				a.addNotification("error", i18n.T("residual.keyboard.hook_toggle_failed", err))
				return a, forceRepaint()
			}
			if count == 0 {
				a.addNotification("info", i18n.T("residual.keyboard.no_hooks"))
				return a, forceRepaint()
			}
			state := i18n.T("residual.keyboard.off")
			if enabled {
				state = i18n.T("residual.keyboard.on")
			}
			a.addNotification("info", i18n.T("residual.keyboard.all_hooks_state", state, count))
		}
		return a, forceRepaint()
	}

	// Ctrl+R toggles voice recording
	if key == "ctrl+r" {
		if a.voiceEnabled {
			return a.handleVoiceToggle()
		}
		return a, nil
	}
	// Alt+B toggles keyboard focus into the BASH side panel so its running and
	// backgrounded commands can be navigated and killed. Registered here (before
	// input handling) so the nav/kill keys are not swallowed by the text input.
	if key == "alt+b" && a.screen == ScreenChat {
		a.toggleBashPanelFocus()
		return a, nil
	}
	if a.bashPanelFocused && a.screen == ScreenChat {
		if handled, cmd := a.handleBashPanelKey(key); handled {
			return a, cmd
		}
	}
	// Toggle error lineage panel for the latest assistant failure.
	// Use Ctrl+G (and Alt+L as fallback) to avoid Ctrl+Shift+L collapsing to Ctrl+L
	// in some terminal emulators.
	// In debug lineage fixture mode, plain "l" is accepted so VHS can toggle deterministically.
	if (key == "ctrl+g" || key == "alt+l" || (a.debugLineageFixture && key == "l")) && a.screen == ScreenChat {
		if toggled, expanded := a.toggleActiveErrorLineage(); toggled {
			a.invalidateViewportCache()
			a.updateViewportContent()
			if expanded {
				a.msgViewport.GotoBottom()
			}
			return a, nil
		}
		a.addNotification("info", i18n.T("residual.keyboard.no_lineage_panel"))
		return a, nil
	}

	// Copy latest error lineage diagnostics to clipboard (Ctrl+Y).
	if key == "ctrl+alt+c" && a.screen == ScreenChat {
		if a.copyActiveErrorLineage() {
			return a, nil
		}
		a.addNotification("info", i18n.T("residual.keyboard.no_lineage_copy"))
		return a, nil
	}

	// Navigate to next tool (Ctrl+])
	if key == "ctrl+]" && a.screen == ScreenChat {
		if callID := a.collapseManager.FocusNext(); callID != "" {
			logDebug("Focused next tool: %s", callID)
			a.invalidateViewportCache()
			a.updateViewportContent()
			return a, nil
		}
	}

	// Navigate to previous tool (Ctrl+[)
	if key == "ctrl+[" && a.screen == ScreenChat {
		if callID := a.collapseManager.FocusPrev(); callID != "" {
			logDebug("Focused previous tool: %s", callID)
			a.invalidateViewportCache()
			a.updateViewportContent()
			return a, nil
		}
	}

	// Toggle expand/collapse focused tool (Ctrl+O)
	if key == "ctrl+o" && a.screen == ScreenChat {
		if callID := a.collapseManager.ToggleFocused(); callID != "" {
			logDebug("Toggled collapse for tool: %s", callID)
			a.invalidateViewportCache()
			a.updateViewportContent()
			return a, nil
		}
	}

	// Expand all tools (Ctrl+Shift+E)
	if key == "ctrl+shift+e" && a.screen == ScreenChat {
		a.collapseManager.ExpandAll()
		logDebug("Expanded all tool outputs")
		a.invalidateViewportCache()
		a.updateViewportContent()
		return a, nil
	}

	// Collapse all tools (Ctrl+Shift+C)
	if key == "ctrl+shift+c" && a.screen == ScreenChat {
		a.collapseManager.CollapseAll()
		logDebug("Collapsed all tool outputs")
		a.invalidateViewportCache()
		a.updateViewportContent()
		return a, nil
	}

	// Test loading indicator (Ctrl+Shift+T)
	if key == "ctrl+shift+t" {
		if a.loadingIndicator.IsActive() {
			a.loadingIndicator.Stop(a.animationClock)
			a.spinner.Stop(a.animationClock)
			a.streamingMessage = false
			a.streamingInProgress = false                                     // ← Reset streaming optimization state
			a.setUserScrolledAway(false, "debug_loading_indicator_test_stop") // ← Reset scroll tracking

			a.setActivityPhase(ActivityPhaseIdle, "")
			a.addNotification("info", i18n.T("residual.keyboard.loading_test_stopped"))
		} else {
			a.setActivityPhase(ActivityPhaseThinking, i18n.T("residual.keyboard.testing_spinner"))
			a.loadingIndicator.Start(a.animationClock)
			a.spinner.Start(a.animationClock)
			a.streamingMessage = true
			a.addNotification("info", i18n.T("residual.keyboard.loading_test_started"))
			a.updateViewportContent()
			// Animation clock handles refresh via Subscribe()
			return a, a.animationClock.Tick()
		}
		a.updateViewportContent()
		return a, nil
	}

	// Cycle loading indicator type (Ctrl+L)
	if key == "ctrl+l" {
		a.loadingIndicator.CycleIndicatorType()
		logDebug("Loading indicator type changed to: %s", a.loadingIndicator.GetIndicatorTypeString())
		a.addNotification("info", i18n.T("residual.keyboard.loading_indicator", a.loadingIndicator.GetIndicatorTypeString()))
		a.updateViewportContent() // Refresh display
		return a, nil
	}

	// Export current render + metadata (Ctrl+Shift+D)
	if key == "ctrl+shift+d" {
		if err := a.exportRenderSnapshot(); err != nil {
			logDebug("Render export failed: %v", err)
			a.addNotification("error", i18n.T("residual.keyboard.render_export_failed", err))
		}
		return a, nil
	}

	// HIGHEST PRIORITY: If there's an active interactive command (like /auth), route to it first
	if a.activeCommand != nil && a.activeCommand.IsInteractive() {
		updatedCmd, cmd := a.activeCommand.Update(msg)
		a.activeCommand = updatedCmd

		// Check if command finished
		if !a.activeCommand.IsInteractive() {
			a.activeCommand = nil
		}

		return a, cmd
	}

	if key == "alt+v" && a.screen == ScreenChat && a.activeModal == nil {
		a.openVaultControl()
		return a, nil
	}

	// Alt+R replays the latest completed agent message word-by-word in the chat viewport.
	// Ctrl+R is reserved for voice recording, so playback intentionally uses Alt+R.
	if key == "alt+r" && a.screen == ScreenChat {
		return a, a.toggleWordPlayback()
	}

	// Global keys
	if key == "ctrl+c" {
		// If a text selection is active, copy it immediately.
		// This takes absolute priority over input clearing and quit modal.
		if a.screen == ScreenChat && a.msgViewport.IsSelectionActive() {
			text := a.msgViewport.GetSelectedText()
			if text != "" {
				writeClipboard(text)
				a.addNotification("success", i18n.T("residual.keyboard.copied_chars", len(text)))
			}
			return a, nil
		}

		// If quit confirmation modal is already showing, quit now.
		if a.activeModal != nil && a.activeModal.Type == ModalQuitConfirm {
			return a, a.performQuit()
		}

		// If the input box has content, clear it (like shells do).
		if a.screen == ScreenChat && a.textInput != nil && len(a.textInput.Value()) > 0 {
			a.textInput.SetValue("")
			a.inputHistory.Reset()
			return a, nil
		}

		// On ScreenChat with empty input and no selection: show quit confirmation modal.
		if a.screen == ScreenChat {
			a.activeModal = NewQuitConfirmModal(func(option int) {
				a.activeModal = nil
				if option == 1 {
					// "Quit Now" selected
					a.pendingQuit = true
				}
			})
			return a, nil
		}

		// Not on ScreenChat: quit immediately.
		return a, a.performQuit()
	}

	// Command palette (Ctrl+~)
	if key == "ctrl+`" {
		if a.commandPalette.IsVisible() {
			a.commandPalette.Hide()
		} else {
			a.commandPalette.Show()
		}
		return a, nil
	}

	// Quick-access modal switchers (only when no other modal is active)
	if a.activeModal == nil && a.activeCommand == nil && !a.commandPalette.IsVisible() {
		switch key {
		case "ctrl+m":
			if a.modelSwitcher.IsVisible() {
				a.modelSwitcher.Hide()
			} else if a.settingsManager != nil {
				ms := a.settingsManager.GetModelSettings()
				a.modelSwitcher.Show(
					ms.GetProviders(),
					ms.CurrentProvider(),
					ms.CurrentModel(),
				)
			}
			return a, nil
		case "ctrl+j":
			if a.screen == ScreenChat || a.screen == ScreenHome {
				if a.agentSwitcher.IsVisible() {
					a.agentSwitcher.Hide()
				} else if a.settingsManager != nil {
					as := a.settingsManager.GetAgentsSettings()
					a.agentSwitcher.Show(
						as.GetAgents(),
						as.GetDefaultAgentID(),
					)
				}
				return a, nil
			}
		case "alt+p":
			if a.screen == ScreenChat || a.screen == ScreenHome {
				if a.promptSwitcher.IsVisible() {
					a.promptSwitcher.Hide()
				} else if a.settingsManager != nil {
					sp := a.settingsManager.GetSystemPromptSettings()
					a.promptSwitcher.Show(
						sp.GetPrompts(),
						sp.GetActivePromptName(),
					)
				}
				return a, nil
			}
		case "ctrl+p":
			if a.screen == ScreenChat || a.screen == ScreenHome {
				if a.profileSwitcher.IsVisible() {
					a.profileSwitcher.Hide()
				} else if a.settingsManager != nil && a.sdk != nil {
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
				return a, nil
			}
		case "alt+s":
			if a.screen == ScreenChat || a.screen == ScreenHome {
				if a.skillPicker.IsVisible() {
					a.skillPicker.Hide()
				} else if a.sdk != nil && a.sdk.skillsManager != nil && a.sdk.skillsManager.IsInitialized() {
					a.skillPicker.SetLoader(a.sdk.skillsManager.GetLoader())
					a.skillPicker.Show(a.sdk.skillsManager.GetAllSkills(), a.sdk.skillsManager.GetActiveSkills())
				}
				return a, nil
			}
		}
	}

	// Toggle workspace mode — TEMPORARILY DISABLED
	// if key == "ctrl+w" && a.screen != ScreenHome {
	// 	a.workspaceMode = !a.workspaceMode
	// 	logDebug("Workspace mode: %v (workspace %d/%d)", a.workspaceMode, a.currentWorkspace+1, len(a.workspaces))
	//
	// 	// If entering workspace mode, initialize focused pane
	// 	if a.workspaceMode && a.focusedPane == nil {
	// 		panes := a.getAllPanes()
	// 		if len(panes) > 0 {
	// 			panes[0].Focused = true
	// 			a.focusedPane = panes[0]
	// 		}
	// 	}
	// 	// Calculate layout when entering workspace mode
	// 	if a.workspaceMode {
	// 		a.calculateLayout()
	// 	}
	// 	return a, nil
	// }

	// Text selection is always enabled - no toggle needed

	// Ctrl+T: Toggle task activity modal
	if key == "ctrl+t" {
		if a.taskActivityModal != nil {
			a.taskActivityModal = nil
		} else if a.activeConv != nil {
			a.taskActivityModal = NewAgentTaskModal()
		}
		return a, forceRepaint()
	}

	// Alt+T: Toggle thinking mode (SDK only — enables/disables extended thinking at the API level)
	if key == "alt+t" {
		logDebug("[THINKING] Alt+T pressed - triggering ThinkingToggleMsg")
		return a, func() tea.Msg {
			return commands.ThinkingToggleMsg{}
		}
	}

	// Ctrl+Alt+T: Toggle thinking display visibility (independent of SDK thinking mode)
	if key == "ctrl+alt+t" {
		a.showThinking = !a.showThinking
		logDebug("[THINKING] Ctrl+Alt+T pressed - display toggle: showThinking=%v", a.showThinking)

		// Sync to render settings
		if a.renderSettings != nil {
			a.renderSettings.ShowThinking = a.showThinking
			if err := SaveRenderSettings(a.renderSettings); err != nil {
				logDebug("[THINKING] Failed to save render settings: %v", err)
			}
		}

		// Notify user
		if a.showThinking {
			a.addNotification("info", i18n.T("residual.keyboard.thinking_on"))
		} else {
			a.addNotification("info", i18n.T("residual.keyboard.thinking_off"))
		}

		// Force re-render
		a.invalidateViewportCache()
		a.updateViewportContent()

		return a, nil
	}

	// Workspace switching with Alt+1-3 — TEMPORARILY DISABLED
	// if a.screen != ScreenHome {
	// 	if key == "alt+1" {
	// 		a.switchToWorkspace(0)
	// 		return a, nil
	// 	} else if key == "alt+2" {
	// 		a.switchToWorkspace(1)
	// 		return a, nil
	// 	} else if key == "alt+3" {
	// 		a.switchToWorkspace(2)
	// 		return a, nil
	// 	}
	// }

	// Handle checkout modal input
	if a.checkoutModal != nil {
		a.checkoutModal.Update(key)
		return a, nil
	}

	// Handle git init modal input
	if a.gitInitModal != nil {
		a.gitInitModal.Update(key)
		return a, nil
	}

	if key == "esc" {
		// If modal is active, handle it
		if a.activeModal != nil {
			a.activeModal.Update(key)
			return a, nil
		}

		switch a.screen {
		case ScreenHome:
			// Delegate to home screen handler so settings/history/workflows
			// can do their own layered escape (nested → content → sidebar → exit)
			return a.handleHomeKey(msg)
		case ScreenChat:
			// Will be handled in handleChatKey (shows modal)
			m, cmd := a.handleChatKey(msg)
			if a.pendingQuit {
				a.pendingQuit = false
				return a, a.performQuit()
			}
			return m, cmd
		case ScreenChats:
			a.screen = ScreenHome
			if animCmd := a.focusHomeInput(); animCmd != nil {
				return a, animCmd
			}
		case ScreenViewer:
			a.screen = ScreenHome
			if animCmd := a.focusHomeInput(); animCmd != nil {
				return a, animCmd
			}
		case ScreenNewChatUI:
			a.screen = ScreenHome
			if animCmd := a.focusHomeInput(); animCmd != nil {
				return a, animCmd
			}
		}
		// Propagate any pending animation cmd (e.g. cursor blink subscription)
		if a.pendingAnimCmd != nil {
			cmd := a.pendingAnimCmd
			a.pendingAnimCmd = nil
			return a, cmd
		}
		return a, nil
	}

	// Screen-specific
	switch a.screen {
	case ScreenHome:
		return a.handleHomeKey(msg)
	case ScreenChats:
		if a.workspaceMode {
			return a.handleWorkspaceKey(msg)
		} else {
			return a.handleConversationsKey(key)
		}
	case ScreenChat:
		if a.workspaceMode {
			return a.handleWorkspaceKey(msg)
		} else {
			m, cmd := a.handleChatKey(msg)
			if a.pendingQuit {
				a.pendingQuit = false
				return a, a.performQuit()
			}
			return m, cmd
		}
	case ScreenViewer:
		return a.handleViewerKey(msg)
	case ScreenNewChatUI:
		return a.handleNewChatUIKey(msg)
	}

	return a, nil
}

func (a *App) handleHomeKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	key := msg.String()

	// Modal switchers capture all input when visible
	if a.modelSwitcher.IsVisible() {
		if key == "enter" {
			if provider, model, ok := a.modelSwitcher.SelectedModel(); ok && a.settingsManager != nil {
				a.settingsManager.GetModelSettings().SetModel(provider, model)
			}
			a.modelSwitcher.Hide()
			return a, nil
		}
		if key == "esc" {
			a.modelSwitcher.Hide()
			return a, nil
		}
		a.modelSwitcher.Update(key)
		return a, nil
	}
	if a.agentSwitcher.IsVisible() {
		if key == "enter" {
			if id, ok := a.agentSwitcher.SelectedAgent(); ok && a.settingsManager != nil {
				as := a.settingsManager.GetAgentsSettings()
				_ = as.SetDefaultAgent(id)
			}
			a.agentSwitcher.Hide()
			return a, nil
		}
		if key == "esc" {
			a.agentSwitcher.Hide()
			return a, nil
		}
		a.agentSwitcher.Update(key)
		return a, nil
	}
	if a.promptSwitcher.IsVisible() {
		if key == "enter" {
			if name, ok := a.promptSwitcher.SelectedPrompt(); ok && a.settingsManager != nil {
				sp := a.settingsManager.GetSystemPromptSettings()
				_ = sp.SetActivePrompt(name)
			}
			a.promptSwitcher.Hide()
			return a, nil
		}
		if key == "esc" {
			a.promptSwitcher.Hide()
			return a, nil
		}
		a.promptSwitcher.Update(key)
		return a, nil
	}
	if a.profileSwitcher.IsVisible() {
		if key == "enter" {
			if id, ok := a.profileSwitcher.SelectedProfile(); ok && a.sdk != nil {
				// Switch the profile using SDK
				if err := a.sdk.SwitchProfile(id); err != nil {
					a.addNotification("error", i18n.T("residual.keyboard.profile_switch_failed", err))
					// If this was an exhaustion-triggered switch, cancel the pending request.
					if a.pendingExhaustionResponse != nil {
						select {
						case a.pendingExhaustionResponse <- agent.FallbackDecision{Cancel: true}:
						default:
						}
						a.pendingExhaustionResponse = nil
					}
				} else {
					// Update the current model to match the profile's main role
					provider, model, err := a.sdk.GetModelForRole(settings.AliasMain)
					if err == nil && a.settingsManager != nil {
						a.settingsManager.GetModelSettings().SetModel(provider, model)
						a.currentProvider = provider
						a.currentModel = model
						a.modelContextWindow = a.sdk.GetModelContextWindow()
						a.addNotification("success", i18n.T("residual.keyboard.switched_profile", id))
					}

					// If this was triggered by provider exhaustion, unblock the agent
					// by sending a new chain built from the freshly-switched profile.
					if a.pendingExhaustionResponse != nil {
						var newChain *fallback.Chain
						if pm := a.sdk.GetProfileManager(); pm != nil {
							if chain, err := pm.ResolveChain(settings.AliasMain); err == nil {
								newChain = chain
							}
						}
						select {
						case a.pendingExhaustionResponse <- agent.FallbackDecision{NewChain: newChain, Cancel: newChain == nil}:
						default:
						}
						a.pendingExhaustionResponse = nil
					}

					// Handle sub-agent exhaustion: send FallbackDecision to all pending sub-agents
					if len(a.pendingSubAgentExhaustion) > 0 {
						var newChain *fallback.Chain
						if pm := a.sdk.GetProfileManager(); pm != nil {
							if chain, err := pm.ResolveChain(settings.AliasMain); err == nil {
								newChain = chain
							}
						}
						for agentID, respCh := range a.pendingSubAgentExhaustion {
							select {
							case respCh <- agent.FallbackDecision{NewChain: newChain, Cancel: newChain == nil}:
								logDebug("[ProfileSwitch] Sent FallbackDecision to sub-agent %s", agentID)
							default:
							}
						}
						a.pendingSubAgentExhaustion = make(map[string]chan agent.FallbackDecision)
					}
				}
			}
			a.profileSwitcher.Hide()
			return a, nil
		}
		if key == "esc" {
			// If the switcher was opened due to exhaustion, cancel the pending request.
			if a.pendingExhaustionResponse != nil {
				select {
				case a.pendingExhaustionResponse <- agent.FallbackDecision{Cancel: true}:
				default:
				}
				a.pendingExhaustionResponse = nil
			}
			if len(a.pendingSubAgentExhaustion) > 0 {
				for agentID, respCh := range a.pendingSubAgentExhaustion {
					select {
					case respCh <- agent.FallbackDecision{Cancel: true}:
						logDebug("[ProfileSwitch] Cancelled sub-agent %s", agentID)
					default:
					}
				}
				a.pendingSubAgentExhaustion = make(map[string]chan agent.FallbackDecision)
			}
			a.profileSwitcher.Hide()
			return a, nil
		}
		a.profileSwitcher.Update(key)
		return a, nil
	}
	if a.skillPicker.IsVisible() {
		if key == "enter" || key == " " {
			skillID, newState, ok := a.skillPicker.ToggleSelected()
			if ok {
				if newState {
					a.addNotification("success", i18n.T("residual.keyboard.skill_enabled", skillID))
				} else {
					a.addNotification("info", i18n.T("residual.keyboard.skill_disabled", skillID))
				}
			}
			return a, nil
		}
		if key == "esc" {
			a.skillPicker.Hide()
			return a, nil
		}
		a.skillPicker.Update(key)
		return a, nil
	}
	if a.skillPicker.IsVisible() {
		if key == "enter" || key == " " {
			// Toggle the selected skill
			skillID, newState, ok := a.skillPicker.ToggleSelected()
			if ok {
				if newState {
					a.addNotification("success", i18n.T("residual.keyboard.skill_enabled", skillID))
				} else {
					a.addNotification("info", i18n.T("residual.keyboard.skill_disabled", skillID))
				}
			} else {
				a.addNotification("error", i18n.T("residual.keyboard.skill_toggle_failed", skillID))
			}
			return a, nil
		}
		if key == "esc" {
			a.skillPicker.Hide()
			return a, nil
		}
		a.skillPicker.Update(key)
		return a, nil
	}

	// Command palette captures all input when visible
	if a.commandPalette.IsVisible() {
		if key == "enter" {
			if item := a.commandPalette.SelectedItem(); item != nil {
				a.commandPalette.Hide()
				a.executeCommand(item.ID)
			}
			return a, nil
		}
		if key == "esc" {
			a.commandPalette.Hide()
			return a, nil
		}
		a.commandPalette.Update(key)
		return a, nil
	}

	// The Git details sidebar belongs to the Prompt tab only. Resolve it here,
	// after home overlays have had first refusal, so Shift+Tab keeps its native
	// navigation behavior in History, Usage, and Settings.
	if key == "shift+tab" && a.homeButton == ButtonNewChat {
		a.homeSidebarCollapsed = !a.homeSidebarCollapsed
		return a, nil
	}

	// Two-mode home screen: input focused vs menu focused
	if a.homeInputFocused {
		return a.handleHomeInputKey(msg, key)
	}
	return a.handleHomeMenuKey(msg)
}

// handleHomeInputKey handles keys when the home input field has focus.
// Supports slash command autocomplete, @ mention autocomplete, and multi-line input.
func (a *App) handleHomeInputKey(msg tea.KeyMsg, key string) (tea.Model, tea.Cmd) {
	// ── Priority 1: Slash command autocomplete navigation ──
	if a.cmdAutocomplete.IsVisible() {
		switch key {
		case "down", "ctrl+n":
			a.cmdAutocomplete.Update(msg)
			return a, nil
		case "up", "ctrl+p":
			a.cmdAutocomplete.Update(msg)
			return a, nil
		case "tab":
			// Complete with selected command, preserving any message the user
			// has already typed after the command portion.
			completed := a.cmdAutocomplete.CompleteSelection()
			if completed != "" {
				a.homeInput.SetValue(completed)
				a.cmdAutocomplete.Hide()
			}
			return a, nil
		case "esc":
			a.cmdAutocomplete.Hide()
			return a, nil
		}
	}

	// ── Priority 2: Mention autocomplete navigation ──
	if a.mentionAutocomplete.IsVisible() {
		switch key {
		case "down", "ctrl+n":
			a.mentionAutocomplete.Update(msg)
			return a, nil
		case "up", "ctrl+p":
			a.mentionAutocomplete.Update(msg)
			return a, nil
		case "right":
			if navigated, newValue := a.mentionAutocomplete.NavigateInto(); navigated {
				a.homeInput.SetValue(newValue)
				a.mentionAutocomplete.SetInput(newValue)
			}
			return a, nil
		case "left":
			if navigated, newValue := a.mentionAutocomplete.NavigateOut(); navigated {
				a.homeInput.SetValue(newValue)
				a.mentionAutocomplete.SetInput(newValue)
			}
			return a, nil
		case "tab":
			newValue := a.mentionAutocomplete.CompleteSelection()
			a.homeInput.SetValue(newValue)
			a.mentionAutocomplete.SetInput(newValue)
			return a, nil
		case "esc":
			a.mentionAutocomplete.Hide()
			return a, nil
		}
	}

	// ── Priority 3: Special keys ──
	switch key {
	case "enter":
		inputText := ""
		if a.homeInput != nil {
			inputText = strings.TrimSpace(a.homeInput.GetSubmitValue())
		}
		if inputText != "" {
			// Handle slash commands
			if strings.HasPrefix(inputText, "/") {
				if isClearSlashCommand(inputText) {
					a.textInput.SetValue(inputText)
					a.homeInput.SetValue("")
					a.cmdAutocomplete.Hide()
					a.mentionAutocomplete.Hide()
					cmd := a.handleSlashCommand(inputText)
					return a, cmd
				}
				a.startNewChatDirect()
				a.textInput.SetValue(inputText)
				a.homeInput.SetValue("")
				a.cmdAutocomplete.Hide()
				cmd := a.handleSlashCommand(inputText)
				return a, cmd
			}
			// Regular message — start new chat and send
			a.startNewChatDirect()
			a.textInput.SetValue(inputText)
			a.homeInput.SetValue("")
			a.cmdAutocomplete.Hide()
			a.mentionAutocomplete.Hide()
			return a, a.handleSendMessage()
		}
		a.homeInput.SetValue("")
		a.startNewChatDirect()

	case "tab":
		// Switch to menu navigation mode AND advance to next tab immediately
		a.homeInputFocused = false
		if a.homeInput != nil {
			a.homeInput.Blur()
		}
		a.cmdAutocomplete.Hide()
		a.mentionAutocomplete.Hide()

		// CRITICAL FIX: Also advance the homeButton so first Tab press goes to History
		a.homeButton = nextVisibleHomeButton(a.homeButton, true)

		// If we switched to History tab, trigger lazy load
		if a.homeButton == ButtonConversations && !a.conversationsLoaded && !a.conversationsLoading {
			return a, a.loadConversationsAsync()
		}
		return a, nil

	case "esc":
		// Close autocompletes first, then clear input, then blur
		if a.cmdAutocomplete.IsVisible() {
			a.cmdAutocomplete.Hide()
			return a, nil
		}
		if a.mentionAutocomplete.IsVisible() {
			a.mentionAutocomplete.Hide()
			return a, nil
		}
		inputEmpty := a.homeInput == nil || a.homeInput.Value() == ""
		if !inputEmpty {
			a.homeInput.SetValue("")
			a.cmdAutocomplete.SetInput("")
			a.mentionAutocomplete.SetInput("")
		} else {
			a.homeInputFocused = false
			if a.homeInput != nil {
				a.homeInput.Blur()
			}
		}

	// Ctrl+Enter: resume last conversation
	case "ctrl+enter":
		if len(a.conversations) > 0 {
			a.openConversation(0)
		}
		return a, nil

	// Ctrl+key navigation shortcuts
	case "ctrl+b":
		a.homeSidebarCollapsed = !a.homeSidebarCollapsed
		return a, nil
	case "ctrl+h":
		a.screen = ScreenChats
		return a, a.loadConversationsAsync()
	case "ctrl+s":
		// Navigate to inline settings tab
		a.homeButton = ButtonSettings
		a.homeInputFocused = false
		if a.homeInput != nil {
			a.homeInput.Blur()
		}
		a.updateMCPSettingsData()
		return a, nil

	case "ctrl+u":
		// Navigate to usage tab
		a.homeButton = ButtonUsage
		a.homeInputFocused = false
		if a.homeInput != nil {
			a.homeInput.Blur()
		}
		// Auto-refresh usage data when entering the tab
		if !a.usageLoading {
			// Also load conversations if not loaded yet (needed for consumption tab)
			if !a.conversationsLoaded && !a.conversationsLoading {
				return a, tea.Batch(a.loadUsageAsync(), a.loadConversationsAsync())
			}
			return a, a.loadUsageAsync()
		}
		return a, nil

	default:
		// Forward to input
		if a.homeInput != nil {
			a.homeInput.Update(msg)
		}
	}

	// ── Update autocompletes after every keystroke ──
	if a.homeInput != nil {
		inputValue := a.homeInput.Value()
		a.cmdAutocomplete.SetInput(inputValue)
		a.mentionAutocomplete.SetInput(inputValue)
	}

	return a, nil
}

// handleHomeMenuKey handles keys when menu navigation has focus.
func (a *App) handleHomeMenuKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	key := msg.String()

	// If we're on the SETTINGS tab, route keys to settings manager
	if a.homeButton == ButtonSettings && a.settingsManager != nil {
		return a.handleHomeSettingsKey(key)
	}

	// If we're on the USAGE tab, handle 'r' for refresh and left/right for sub-tabs
	if a.homeButton == ButtonUsage {
		switch key {
		case "r":
			if !a.usageLoading {
				a.usageResult = nil
				return a, a.loadUsageAsync()
			}
		case "left", "h":
			if a.usageSubTab > 0 {
				a.usageSubTab--
				return a, nil
			}
		case "right", "l":
			if a.usageSubTab < UsageSubTabDiagnostics {
				a.usageSubTab++
				return a, nil
			}
		}
	}

	// If we're on the HISTORY tab, it renders the consolidated two-pane menu
	// (compact list + summary panel). Drive that menu's selection via selectedIdx
	// so the Home tab and ScreenChats share one navigation model.
	if a.homeButton == ButtonConversations && len(a.conversations) > 0 {
		totalConvs := len(a.conversations)
		visibleHeight := a.height - 12 // Header + footer space
		maxVisible := visibleHeight / 3
		if maxVisible < 5 {
			maxVisible = 5
		}
		if a.selectedIdx < 0 {
			a.selectedIdx = 0
		}

		switch key {
		case "j", "down":
			if a.selectedIdx < totalConvs-1 {
				a.selectedIdx++
			}
			return a, nil
		case "k", "up":
			if a.selectedIdx > 0 {
				a.selectedIdx--
			}
			return a, nil
		case "g":
			a.selectedIdx = 0
			return a, nil
		case "G":
			a.selectedIdx = totalConvs - 1
			return a, nil
		case "ctrl+d":
			a.selectedIdx += maxVisible / 2
			if a.selectedIdx >= totalConvs {
				a.selectedIdx = totalConvs - 1
			}
			return a, nil
		case "ctrl+u":
			a.selectedIdx -= maxVisible / 2
			if a.selectedIdx < 0 {
				a.selectedIdx = 0
			}
			return a, nil
		case "enter", " ":
			// Open the selected conversation full-screen.
			if a.selectedIdx >= 0 && a.selectedIdx < len(a.conversations) {
				a.openConversation(a.selectedIdx)
			}
			return a, nil
		}
	}

	switch key {
	case "tab":

		// Cycle to next tab
		a.homeButton = nextVisibleHomeButton(a.homeButton, true)

		a.selectedIdx = 0 // Reset selection when changing tabs

		// Lazy load conversations when HISTORY tab is selected for first time
		if a.homeButton == ButtonConversations && !a.conversationsLoaded && !a.conversationsLoading {
			cmd := a.loadConversationsAsync()
			return a, cmd
		}
		// Load usage data when USAGE tab is selected (always refresh)
		if a.homeButton == ButtonUsage && !a.usageLoading {
			return a, a.loadUsageAsync()
		}
		// Populate MCP/Tools data when SETTINGS tab is selected
		if a.homeButton == ButtonSettings {
			a.updateMCPSettingsData()
		}
		return a, nil
	case "shift+tab":
		// Navigate backward through non-prompt tabs. The Prompt tab's
		// Shift+Tab binding is handled in handleHomeKey before this point.
		if a.homeButton == ButtonNewChat {
			return a, nil
		}
		a.homeButton = nextVisibleHomeButton(a.homeButton, false)

		// Lazy load conversations when HISTORY tab is selected for first time
		if a.homeButton == ButtonConversations && !a.conversationsLoaded && !a.conversationsLoading {
			return a, a.loadConversationsAsync()
		}
		// Load usage data when USAGE tab is selected (always refresh)
		if a.homeButton == ButtonUsage && !a.usageLoading {
			return a, a.loadUsageAsync()
		}
		// Populate MCP/Tools data when SETTINGS tab is selected
		if a.homeButton == ButtonSettings {
			a.updateMCPSettingsData()
		}
		return a, nil
	case "/":
		a.homeInputFocused = true
		if a.homeInput != nil {
			a.homeInput.Focus()
		}
	case "enter", " ":
		switch a.homeButton {
		case ButtonNewChat:
			a.startNewChatDirect()
		case ButtonConversations:
			a.screen = ScreenChats
			a.selectedIdx = 0
			return a, a.loadConversationsAsync()
		case ButtonInbox:
			a.screen = ScreenChats
			a.selectedIdx = 0
			return a, a.loadConversationsAsync()
		case ButtonViewer:
			a.screen = ScreenViewer
		case ButtonSettings:
			// Settings is rendered inline — this case shouldn't be reached
			// because handleHomeSettingsKey handles all settings keys above,
			// but just in case, ensure MCP data is fresh.
			a.updateMCPSettingsData()
		}
	// Ctrl+Enter: resume last conversation
	case "ctrl+enter":
		if len(a.conversations) > 0 {
			a.openConversation(0)
		}
		return a, nil
	// Ctrl+key navigation (same as input mode)
	case "ctrl+b":
		a.homeSidebarCollapsed = !a.homeSidebarCollapsed
		return a, nil
	case "ctrl+h":
		a.screen = ScreenChats
		return a, a.loadConversationsAsync()
	case "ctrl+s":
		// Navigate to inline settings tab
		a.homeButton = ButtonSettings
		a.homeInputFocused = false
		if a.homeInput != nil {
			a.homeInput.Blur()
		}
		a.updateMCPSettingsData()
	case "q":
		if a.bgProcessManager != nil {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if err := a.bgProcessManager.Shutdown(ctx); err != nil {
				logDebug("Error shutting down background process manager: %v", err)
			} else {
				logDebug("Background process manager shutdown complete")
			}
		}
		a.preRenderBuffer.Stop()
		// Shut down visual picker servers
		if a.visualReg != nil {
			a.visualReg.ShutdownAll()
		}
		// Stop workspace hub
		if a.hub != nil {
			a.hub.Stop(context.Background())
		}
		// Mark app as quitting to prevent goroutine leaks
		a.quitting = true
		return a, tea.Quit
	default:

		// Clean up voice manager
		a.closeVoice()
		// Stop background pre-render buffer
		a.preRenderBuffer.Stop()
		// Any printable character switches to input mode
		if len(key) == 1 && key[0] >= 32 && key[0] < 127 {
			a.homeInputFocused = true
			if a.homeInput != nil {
				a.homeInput.Focus()
				a.homeInput.SetValue(key)
				// Trigger autocomplete for the typed character (e.g. "/" or "@")
				a.cmdAutocomplete.SetInput(key)
				a.mentionAutocomplete.SetInput(key)
			}
		}
	}
	return a, nil
}

// handleHomeSettingsKey handles keyboard input when the settings tab is active in the home screen.
// Tab/Shift+Tab navigate within settings first; only escape from the outermost level (sidebar)
// cycles home tabs. All other keys delegate to the settings manager.
func (a *App) handleHomeSettingsKey(key string) (tea.Model, tea.Cmd) {
	state := a.settingsManager.GetState()

	// ── Tab / Shift+Tab: layered navigation, mirrors ESC behaviour ──────
	if (key == "tab" || key == "shift+tab") && !a.settingsManager.IsEditingText() {
		// While in a nested sub-screen (form, sub-menu), let the section handle it.
		if a.settingsManager.IsInNestedState() {
			cmd := a.settingsManager.HandleKey(key, a.cmdRegistry)
			return a, cmd
		}

		if key == "tab" {
			// Tab in sidebar → enter content.
			// Tab in content → fall through to the delegate so sections can use it
			// internally (e.g. MCP panel cycling).
			if state.Focus == settings.FocusSidebar {
				state.Focus = settings.FocusContent
				state.SelectedItem = 0
				state.ScrollOffset = 0
				return a, nil
			}
			// In content: exit settings, cycle to next home tab.
			a.leaveSettingsTab()
			a.homeButton = nextVisibleHomeButton(a.homeButton, true)
			if a.homeButton == ButtonConversations && !a.conversationsLoaded && !a.conversationsLoading {
				return a, a.loadConversationsAsync()
			}
			if a.homeButton == ButtonUsage && !a.usageLoading && a.usageResult == nil {
				return a, a.loadUsageAsync()
			}
			return a, nil
		} else {
			// Shift+Tab in content → step back to sidebar.
			if state.Focus == settings.FocusContent {
				state.Focus = settings.FocusSidebar
				return a, nil
			}
			// Shift+Tab in sidebar → exit settings to previous home tab.
			a.leaveSettingsTab()
			a.homeButton = nextVisibleHomeButton(a.homeButton, false)
			if a.homeButton == ButtonConversations && !a.conversationsLoaded && !a.conversationsLoading {
				return a, a.loadConversationsAsync()
			}
			return a, nil
		}
	}

	// ── Exit modal ─────────────────────────────────────────────────────
	if state.ShowExitModal {
		switch key {
		case "up", "k", "left", "h":
			if state.ExitModalChoice > 0 {
				state.ExitModalChoice--
			}
			return a, nil
		case "down", "j", "right", "l":
			if state.ExitModalChoice < 2 {
				state.ExitModalChoice++
			}
			return a, nil
		case "y", "Y": // Quick key: Save & Exit
			a.settingsManager.SavePendingChanges()
			a.leaveSettingsTab()
			a.homeButton = ButtonNewChat
			state.ShowExitModal = false
			return a, nil
		case "n", "N": // Quick key: Exit Without Saving
			a.leaveSettingsTab()
			a.homeButton = ButtonNewChat
			state.ShowExitModal = false
			return a, nil
		case "enter", " ":
			switch state.ExitModalChoice {
			case 0: // Save & Exit → flush pending form edits then go back to prompt tab
				a.settingsManager.SavePendingChanges()
				a.leaveSettingsTab()
				a.homeButton = ButtonNewChat
				state.ShowExitModal = false
			case 1: // Exit Without Saving → discard in-progress edits, go back to prompt tab
				a.leaveSettingsTab()
				a.homeButton = ButtonNewChat
				state.ShowExitModal = false
			case 2: // Cancel
				state.ShowExitModal = false
			}
			return a, nil
		case "esc", "c", "C": // Cancel / close modal
			state.ShowExitModal = false
			return a, nil
		}
		return a, nil
	}

	// ── Quick exit keys ────────────────────────────────────────────────
	// 'q' exits settings tab unless editing text
	if key == "q" {
		if a.settingsManager.IsEditingText() {
			cmd := a.settingsManager.HandleKey(key, a.cmdRegistry)
			return a, cmd
		}
		// Always show exit modal when leaving settings
		state.ShowExitModal = true
		state.ExitModalChoice = 0
		return a, nil
	}

	// 'esc' — layered escape: nested state → content → sidebar → exit settings
	if key == "esc" {
		if a.settingsManager.IsInNestedState() {
			cmd := a.settingsManager.HandleKey(key, a.cmdRegistry)
			return a, cmd
		}
		// If in content, go back to sidebar
		if state.Focus == settings.FocusContent {
			state.Focus = settings.FocusSidebar
			return a, nil
		}
		// In sidebar — exit settings (always show save-changes modal)
		state.ShowExitModal = true
		state.ExitModalChoice = 0
		return a, nil
	}

	// ── Delegate to settings manager ───────────────────────────────────
	prevSection := state.SelectedSection
	cmd := a.settingsManager.HandleKey(key, a.cmdRegistry)

	// Re-get state (may have changed)
	state = a.settingsManager.GetState()

	// Manage animation clock subscription for Display section
	if prevSection != settings.SectionDisplay && state.SelectedSection == settings.SectionDisplay {
		if a.animationClock != nil {
			clockCmd := a.animationClock.Subscribe()
			if cmd != nil {
				return a, tea.Batch(cmd, clockCmd)
			}
			return a, clockCmd
		}
	} else if prevSection == settings.SectionDisplay && state.SelectedSection != settings.SectionDisplay {
		if a.animationClock != nil {
			a.animationClock.Unsubscribe()
		}
	}

	return a, cmd
}

// leaveSettingsTab cleans up animation state when leaving the settings tab.
func (a *App) leaveSettingsTab() {
	if a.settingsManager != nil {
		state := a.settingsManager.GetState()
		if state.SelectedSection == settings.SectionDisplay && a.animationClock != nil {
			a.animationClock.Unsubscribe()
		}
		// Reset dirty so the next visit starts clean.
		a.settingsManager.ResetDirty()
		// Reset focus to the sidebar. Leaving the tab with content focus made
		// the NEXT visit route keys into the stale section's content while the
		// render highlighted the sidebar — arrows/Enter did invisible things
		// (e.g. opening "Create New Agent" from what looked like the sidebar),
		// and Tab immediately exited the settings tab again.
		state.Focus = settings.FocusSidebar
	}
}

// visibleHomeButtons lists the navigable buttons in display order.
var visibleHomeButtons = []HomeButton{
	ButtonNewChat,
	ButtonConversations,
	ButtonUsage,
	ButtonSettings,
}

// nextVisibleHomeButton steps forward or backward through visibleHomeButtons with wrap.
func nextVisibleHomeButton(current HomeButton, forward bool) HomeButton {
	idx := 0
	for i, b := range visibleHomeButtons {
		if b == current {
			idx = i
			break
		}
	}
	n := len(visibleHomeButtons)
	if forward {
		idx = (idx + 1) % n
	} else {
		idx = (idx + n - 1) % n
	}
	return visibleHomeButtons[idx]
}

// performQuit shuts down all subsystems and returns tea.Quit.
// Extracted so both direct ctrl+c and modal "Quit Now" can use the same cleanup path.
func (a *App) performQuit() tea.Cmd {
	if a.workspaceLease != nil {
		_ = a.workspaceLease.Close()
		a.workspaceLease = nil
	}
	if a.sdk != nil {
		if err := a.sdk.Close(); err != nil {
			logDebug("Error closing SDK integration: %v", err)
		}
	}
	if a.bgProcessManager != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := a.bgProcessManager.Shutdown(ctx); err != nil {
			logDebug("Error shutting down background process manager: %v", err)
		}
	}
	a.closeVoice()
	a.preRenderBuffer.Stop()
	if a.visualReg != nil {
		a.visualReg.ShutdownAll()
	}
	if a.hub != nil {
		a.hub.Stop(context.Background())
	}
	a.quitting = true
	return tea.Quit
}
