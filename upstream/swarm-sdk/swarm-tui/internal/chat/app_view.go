package chat

import (
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/i18n"
	"github.com/charmbracelet/x/ansi"
)

func normalizeFrameForTerminal(content string, width, height int) string {
	if height <= 0 {
		return content
	}
	lines := strings.Split(content, "\n")
	if len(lines) > height {
		lines = lines[:height]
	}
	for len(lines) < height {
		lines = append(lines, "")
	}
	if width > 0 {
		for i, line := range lines {
			if visualWidth := lipgloss.Width(line); visualWidth < width {
				lines[i] = line + strings.Repeat(" ", width-visualWidth)
			}
		}
	}
	return strings.Join(lines, "\n")
}

func (a *App) viewBootstrap() tea.View {
	var v tea.View
	width, height := a.width, a.height
	if width <= 0 {
		width = 80
	}
	if height <= 0 {
		height = 24
	}

	frameContent := a.introFrame
	if frameContent == "" {
		frameContent = a.introLogoForWidth(width)
	}
	lines := strings.Split(frameContent, "\n")
	for i, line := range lines {
		lines[i] = ansi.Truncate(line, width, "")
	}
	frameContent = strings.Join(lines, "\n")
	frame := lipgloss.Place(
		width,
		height,
		lipgloss.Center,
		lipgloss.Center,
		frameContent,
		lipgloss.WithWhitespaceStyle(
			lipgloss.NewStyle().Background(lipgloss.Color(a.theme.BG)),
		),
	)
	frame = normalizeFrameForTerminal(frame, width, height)
	v.SetContent(applyBackground(frame, a.theme.BG))
	v.AltScreen = true
	v.WindowTitle = i18n.T("classic_chat.window.starting")
	return v
}

func (a *App) View() tea.View {
	defer a.publishTerminalImageFrame()
	if a.bootstrapPending {
		return a.viewBootstrap()
	}

	var v tea.View

	if a.width == 0 || a.height == 0 {
		v.SetContent(i18n.T("classic_chat.common.loading"))
		v.AltScreen = true
		v.WindowTitle = i18n.T("classic_chat.window.loading")
		return v
	}

	// SimCity early-return path: reuse the last rendered string when nothing
	// visible changed. The previous implementation used tea.View.Layer for a
	// zero-cost idle frame via ultraviolet; that field was removed in the
	// bubbletea v2 upgrade, so we fall back to re-pushing the cached content.
	// Frame cap: even when a refresh IS wanted, don't compose a new frame
	// faster than the terminal can flush it. Bubble Tea calls View() once per
	// message, so the wheel-event rate (267-350/s while scrolling) became the
	// frame rate, while its renderer only flushes on a 60fps ticker
	// (tea.go:1394). Frames built in between were discarded, and composing
	// them blocked the event loop, which is what made input feel queued.
	//
	// viewNeedsRefresh is deliberately NOT cleared here, so the very next
	// View() rebuilds. frameDeferred asks Update() to schedule exactly one
	// trailing tick so the final frame of a burst is never left stale.
	if a.viewNeedsRefresh && a.lastRenderOutput != "" &&
		time.Since(a.lastFrameBuiltAt) < frameBudget {
		a.frameDeferred = true
		v.SetContent(a.lastRenderOutput)
		v.AltScreen = true
		v.MouseMode = tea.MouseModeCellMotion
		if newTitle := a.getWindowTitle(); newTitle != a.lastWindowTitle {
			v.WindowTitle = newTitle
			a.lastWindowTitle = newTitle
		}
		return v
	}

	if !a.viewNeedsRefresh && a.lastRenderOutput != "" {
		v.SetContent(a.lastRenderOutput)
		v.AltScreen = true
		v.MouseMode = tea.MouseModeCellMotion

		// Still need to update window title even in cached path
		newTitle := a.getWindowTitle()
		if newTitle != a.lastWindowTitle {
			v.WindowTitle = newTitle
			a.lastWindowTitle = newTitle
		}

		return v
	}
	a.viewNeedsRefresh = false
	a.lastFrameBuiltAt = time.Now()
	a.frameDeferred = false

	if a.attachScreen != nil {
		a.attachScreen.SetSize(a.width, a.height)
		content := normalizeFrameForTerminal(a.attachScreen.View(), a.width, a.height)
		a.lastRenderOutput = content
		a.screenLayer.Set(content)
		v.SetContent(content)
		v.AltScreen = true
		v.MouseMode = tea.MouseModeCellMotion
		return v
	}

	// Boot animation
	if !a.bootComplete {
		v.SetContent(applyBackground(a.viewBoot(), a.theme.BG))
		v.AltScreen = true
		v.WindowTitle = i18n.T("classic_chat.window.booting")
		return v
	}

	// Mandatory update blocking screen - takes over entire UI
	if a.updateMandatoryBlocked && a.updateAvailable != nil {
		mandatoryContent := a.renderMandatoryUpdateModal()
		v.SetContent(applyBackground(mandatoryContent, a.theme.BG))
		v.AltScreen = true
		v.WindowTitle = i18n.T("classic_chat.window.update_required")
		return v
	}

	// Debug screen overlay (takes over entire screen)
	if a.debugScreen.IsVisible() {
		// Set dimensions before rendering
		a.debugScreen.width = a.width
		a.debugScreen.height = a.height
		debugContent := a.debugScreen.View()
		final := normalizeFrameForTerminal(applyBackground(debugContent, a.theme.BG), a.width, a.height)
		// Update render cache so the SimCity model can skip
		// re-renders when nothing changed. Without this, every
		// AnimationTickMsg causes a full re-render (flicker) and
		// the stale lastRenderOutput from the chat screen can
		// flash through when viewNeedsRefresh is false.
		a.lastRenderOutput = final
		a.screenLayer.Set(final)
		v.SetContent(final)
		v.AltScreen = true
		v.MouseMode = tea.MouseModeCellMotion

		// Set window title for debug screen
		newTitle := a.getWindowTitle()
		if newTitle != a.lastWindowTitle {
			v.WindowTitle = newTitle
			a.lastWindowTitle = newTitle
		}

		return v
	}

	var content string
	switch a.screen {
	case ScreenHome:
		// Full-screen takeover for new chat modal
		if a.newChatModal != nil {
			content = a.newChatModal.View(a.width, a.height, a.theme)
		} else {
			content = a.viewHome(nil)
			// Overlay autocomplete dropdowns below home screen input
			if a.homeInputFocused {
				var acView string
				if a.cmdAutocomplete.IsVisible() {
					acView = a.cmdAutocomplete.View()
				} else if a.mentionAutocomplete.IsVisible() {
					acView = a.mentionAutocomplete.View()
				}
				if acView != "" {
					// Count input box height (rendered lines)
					inputBoxH := 3 // border top + content + border bottom (minimum)
					if a.homeInput != nil {
						inputBoxH = a.homeInput.GetContentHeight() + 2 // +2 for border
					}
					// Position below the input box (+1 for gap)
					startY := a.homeInputOverlayY + inputBoxH + 1
					acHeight := strings.Count(acView, "\n") + 1
					// If it would overflow bottom, flip to above
					if startY+acHeight > a.height {
						startY = a.homeInputOverlayY - acHeight - 1
						if startY < 0 {
							startY = 0
						}
					}
					content = overlayAt(content, acView, a.width, a.height, a.homeInputOverlayX, startY, "")
				}
			}
			// Overlay modal switchers on home screen
			if a.modelSwitcher.IsVisible() {
				if sv := a.modelSwitcher.Render(a.width, a.height, a.theme); sv != "" {
					content = overlayModal(content, sv, a.width, a.height)
				}
			}
			if a.agentSwitcher.IsVisible() {
				if sv := a.agentSwitcher.Render(a.width, a.height, a.theme); sv != "" {
					content = overlayModal(content, sv, a.width, a.height)
				}
			}
			if a.promptSwitcher.IsVisible() {
				if sv := a.promptSwitcher.Render(a.width, a.height, a.theme); sv != "" {
					content = overlayModal(content, sv, a.width, a.height)
				}
			}
			if a.profileSwitcher.IsVisible() {
				if sv := a.profileSwitcher.Render(a.width, a.height, a.theme); sv != "" {
					content = overlayModal(content, sv, a.width, a.height)
				}
			}
			if a.skillPicker.IsVisible() {
				if sv := a.skillPicker.Render(a.width, a.height, a.theme); sv != "" {
					content = overlayModal(content, sv, a.width, a.height)
				}
			}
			if a.commandPalette.IsVisible() {
				if pv := a.commandPalette.Render(a.width, a.height, a.theme); pv != "" {
					content = overlayModal(content, pv, a.width, a.height)
				}
			}
		}
	case ScreenChats:
		if a.workspaceMode {
			content = a.viewWorkspace()
		} else {
			// Full-screen takeover for new chat modal
			if a.newChatModal != nil {
				content = a.newChatModal.View(a.width, a.height, a.theme)
			} else {
				content = a.renderTwoPane(a.width, a.height)
			}
		}
	case ScreenViewer:
		content = a.renderViewerScreen()
	case ScreenChat:
		if a.workspaceMode {
			content = a.viewWorkspace()
		} else if a.newChatModal != nil {
			// Full-screen takeover for new chat modal
			content = a.newChatModal.View(a.width, a.height, a.theme)
		} else {
			// Keep the persisted side-panel preference and all chat sizing in sync.
			// The viewport size itself must remain at the effective chat width; only
			// a.width is temporarily narrowed so renderChatContent builds chrome at
			// chatWidth. Restoring msgViewport.Width here would leave cached
			// messages wrapped at a stale width until Tab/resize forces a rebuild.
			if a.syncSidePanelPreference() {
				a.applyChatAreaLayout(true)
			}
			layout := a.chatAreaLayout()
			chatWidth := layout.ChatWidth
			overlayWidth := layout.OverlayWidth
			showSidePanel := layout.ShowSidePanel

			oldWidth := a.width
			a.width = chatWidth

			// Render chat view with adjusted width
			chatContent := a.viewChat()

			// Restore original terminal width for overlays, side panel, and title/status.
			a.width = oldWidth
			interactiveCommand := a.activeCommand != nil && a.activeCommand.IsInteractive()

			// Overlay quick-access modal switchers
			if a.modelSwitcher.IsVisible() {
				if sv := a.modelSwitcher.Render(overlayWidth, a.height, a.theme); sv != "" {
					chatContent = overlayModal(chatContent, sv, chatWidth, a.height)
				}
			}
			if a.agentSwitcher.IsVisible() {
				if sv := a.agentSwitcher.Render(overlayWidth, a.height, a.theme); sv != "" {
					chatContent = overlayModal(chatContent, sv, chatWidth, a.height)
				}
			}
			if a.promptSwitcher.IsVisible() {
				if sv := a.promptSwitcher.Render(overlayWidth, a.height, a.theme); sv != "" {
					chatContent = overlayModal(chatContent, sv, chatWidth, a.height)
				}
			}
			if a.profileSwitcher.IsVisible() {
				if sv := a.profileSwitcher.Render(overlayWidth, a.height, a.theme); sv != "" {
					chatContent = overlayModal(chatContent, sv, chatWidth, a.height)
				}
			}
			if a.skillPicker.IsVisible() {
				if sv := a.skillPicker.Render(overlayWidth, a.height, a.theme); sv != "" {
					chatContent = overlayModal(chatContent, sv, chatWidth, a.height)
				}
			}

			// Overlay command palette if visible
			if a.commandPalette.IsVisible() {
				paletteView := a.commandPalette.Render(overlayWidth, a.height, a.theme)
				if paletteView != "" {
					chatContent = overlayModal(chatContent, paletteView, chatWidth, a.height)
				}
			}

			// Overlay modal if active
			if a.activeModal != nil {
				// Handle special modals that have their own View
				if a.checkoutModal != nil {
					modalView := a.checkoutModal.Render(overlayWidth, a.height, a.theme)
					if modalView != "" {
						chatContent = overlayModal(chatContent, modalView, chatWidth, a.height)
					}
				} else if a.gitInitModal != nil {
					modalView := a.gitInitModal.Render(overlayWidth, a.height, a.theme)
					if modalView != "" {
						chatContent = overlayModal(chatContent, modalView, chatWidth, a.height)
					}
				} else {
					// Regular modal
					modalView := a.activeModal.Render(overlayWidth, a.height, a.theme)
					if modalView != "" {
						chatContent = overlayModal(chatContent, modalView, chatWidth, a.height)
					}
				}
			}

			// Overlay autocomplete if visible
			if !interactiveCommand && a.cmdAutocomplete.IsVisible() {
				autocompleteView := a.cmdAutocomplete.View()
				if autocompleteView != "" {
					chatContent = overlayAutocomplete(chatContent, autocompleteView, chatWidth, a.height, a.inputOverlayY)
				}
			}

			// Overlay mention autocomplete if visible (triggered by @)
			if !interactiveCommand && a.mentionAutocomplete.IsVisible() {
				mentionAutocompleteView := a.mentionAutocomplete.View()
				if mentionAutocompleteView != "" {
					chatContent = overlayAutocomplete(chatContent, mentionAutocompleteView, chatWidth, a.height, a.inputOverlayY)
				}
			}

			// Overlay active command on chat content BEFORE adding side panel
			// This ensures the command is positioned relative to the chat width, not full screen
			if interactiveCommand {
				cmdView := a.activeCommand.View()
				if cmdView != "" {
					// Use lipgloss.Place for proper ANSI-aware centering instead of broken overlayContent
					// This centers the command view within the chat area dimensions
					chatContent = lipgloss.Place(
						chatWidth,
						a.height,
						lipgloss.Center,
						lipgloss.Center,
						cmdView,
						lipgloss.WithWhitespaceChars(" "),
					)
				}
			}

			// ALWAYS add side panel AFTER overlays (if enabled) to create the full base layout
			// ALWAYS add side panel AFTER overlays (if enabled) to create the full base layout
			if showSidePanel {
				sidePanel := NewSidePanel(SidePanelWidth, a.height)
				sidePanelContent := sidePanel.Render(a)

				// chatContent is already sized to chatWidth×a.height by renderChatContent,
				// so we can join directly without an extra lipgloss Width+Height pass.
				content = lipgloss.JoinHorizontal(
					lipgloss.Top,
					chatContent,
					sidePanelContent,
				)
			} else {
				content = chatContent
			}
		}
	case ScreenNewChatUI:
		// New modular chat UI (experimental)
		if a.chatScreen != nil {
			content = a.chatScreen.ViewString()
		} else {
			content = i18n.T("classic_chat.view.chat_ui_uninitialized")
		}
	}

	// Workspace bar removed for cleaner UX - workspace mode indicated by pane layout

	// Global overlay for permission approvals (fallback for non-chat screens)
	if a.approvalModal != nil && a.screen != ScreenChat && a.screen != ScreenChats {
		approvalView := a.approvalModal.Render(a.width, a.height, a.theme)
		if approvalView != "" {
			content = overlayModal(content, approvalView, a.width, a.height)
		}
	}

	// Global overlay for task activity modal (Ctrl+T)
	if a.taskActivityModal != nil {
		taskModalView := a.taskActivityModal.Render(a.width, a.height, a.theme, a.messages)
		if taskModalView != "" {
			content = overlayModal(content, taskModalView, a.width, a.height)
		}
	}

	// Global overlay for agent questions (fallback for non-chat screens)
	if a.questionModal != nil && a.approvalModal == nil && a.screen != ScreenChat && a.screen != ScreenChats {
		questionView := a.questionModal.Render(a.width, a.height, a.theme)
		if questionView != "" {
			content = overlayModal(content, questionView, a.width, a.height)
		}
	}

	// Overlay notifications on all screens (not just chat).
	// This makes profile-switch, proxy-reload, and other background notifications visible.
	if notif, _ := a.renderNotifications(a.width); notif != "" {
		content = overlayAt(content, notif, a.width, a.height, 2, 1, "")
	}

	// No base-background paint at the View level — the chat surface inherits
	// the terminal's default bg so the UI reads correctly on both light and
	// dark terminal themes. Explicit surfaces (user messages, badges, code
	// blocks, selection highlights) still paint their own bg where intended.
	final := normalizeFrameForTerminal(content, a.width, a.height)

	a.lastRenderOutput = final
	a.screenLayer.Set(final)

	v.SetContent(final)
	v.AltScreen = true
	v.MouseMode = tea.MouseModeCellMotion

	// Let Bubble Tea serialize title changes through the same renderer output
	// stream as the frame. Direct writes to stdout can interleave with native
	// image protocol packets and corrupt the terminal state.
	title := a.getWindowTitle()
	if title != a.lastWindowTitle {
		a.lastWindowTitle = title
		v.WindowTitle = title
	}

	return v
}

// getWindowTitle generates a terminal window title that shows the current status,
// model, and operating mode. This helps users identify Swarm TUI sessions in
// terminal multiplexers and Zed's agent panel.
func (a *App) getWindowTitle() string {
	// Determine current status
	status := i18n.T("classic_chat.window.waiting")

	// Check if we should show "Completed" status (within 5 seconds of completion)
	if !a.lastStreamingEndTime.IsZero() {
		timeSinceCompletion := time.Since(a.lastStreamingEndTime)
		if timeSinceCompletion < 5*time.Second {
			status = i18n.T("classic_chat.window.completed")
			// Schedule a refresh after the 5 second period
			if !a.completionNotified && timeSinceCompletion < 100*time.Millisecond {
				a.completionNotified = true
				go func() {
					time.Sleep(5 * time.Second)
					// Wake the UI to update the title back to "Waiting"
					if a.program != nil && !a.quitting {
						a.program.Send(func() tea.Msg { return nil })
					}
				}()
			}
		}
	} else if a.streamingInProgress || a.streamingMessage {
		status = i18n.T("classic_chat.window.active")
	}

	// Get model display name, fallback to model ID, then to "unknown"
	model := a.currentModelDisplay
	if model == "" {
		model = a.currentModel
		if model == "" {
			model = i18n.T("classic_chat.common.unknown")
		}
	}

	// Get operating mode, default to ACT
	mode := a.operatingMode
	if mode == "" {
		mode = "ACT"
	}

	// Format: Swarm TUI - [Status] - [Model] - [Mode]
	title := i18n.T("classic_chat.window.title", status, model, strings.ToUpper(mode))

	return title
}
