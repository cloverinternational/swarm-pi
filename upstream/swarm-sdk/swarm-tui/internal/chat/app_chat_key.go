package chat

import (
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/agent"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/fallback"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/bgprocess"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/chat/settings"
)

func (a *App) handleChatKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	key := msg.String()
	logDebug("[handleChatKey] Key pressed: %s", key)

	// Check for PageUp/PageDown which may not be in the string representation
	keyCode := msg.Key()
	if keyCode.Code == tea.KeyPgUp {
		logDebug("[handleChatKey] PageUp pressed - scrolling up %d lines", a.msgViewport.Height)
		a.msgViewport.ScrollUp(a.msgViewport.Height)
		if !a.msgViewport.AtBottom() {
			a.setUserScrolledAway(true, "pgup_scroll")
		}
		return a, nil
	}
	if keyCode.Code == tea.KeyPgDown {
		logDebug("[handleChatKey] PageDown pressed - scrolling down %d lines", a.msgViewport.Height)
		a.msgViewport.ScrollDown(a.msgViewport.Height)
		if a.msgViewport.AtBottom() {
			a.setUserScrolledAway(false, "pgdown_at_bottom")
		}
		return a, nil
	}

	// HIGHEST PRIORITY: Modal switchers capture all input when visible
	if a.modelSwitcher.IsVisible() {
		if key == "enter" {
			if provider, model, ok := a.modelSwitcher.SelectedModel(); ok && a.settingsManager != nil {
				a.settingsManager.GetModelSettings().SetModel(provider, model)
			}
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
		a.promptSwitcher.Update(key)
		return a, nil
	}
	if a.profileSwitcher.IsVisible() {
		if key == "enter" {
			if id, ok := a.profileSwitcher.SelectedProfile(); ok && a.sdk != nil {
				// Switch the profile using SDK
				if err := a.sdk.SwitchProfile(id); err != nil {
					a.addNotification("error", tr("classic.profile.switch_failed", err))
					// Cancel any pending exhaustion request.
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

						// Update display names
						a.currentProviderDisplay = provider
						a.currentModelDisplay = model
						// TODO: Get proper display names from provider registry

						// Get active profile name for notification
						profileName := id
						if pm := a.sdk.GetProfileManager(); pm != nil {
							if profile, err := pm.GetActiveProfile(); err == nil && profile != nil {
								profileName = profile.Name
							}
						}

						a.addNotification("success", tr("classic.profile.switched", profileName))

						// Force UI refresh
						a.viewNeedsRefresh = true
					}

					// If this was triggered by provider exhaustion, unblock the agent.
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
				}
			}
			a.profileSwitcher.Hide()
			return a, nil
		}
		a.profileSwitcher.Update(key)
		return a, nil
	}
	if a.skillPicker.IsVisible() {
		if key == "enter" || key == " " {
			// Toggle the selected skill
			skillID, newState, ok := a.skillPicker.ToggleSelected()
			if ok {
				if newState {
					a.addNotification("success", tr("classic.skill.enabled", skillID))
				} else {
					a.addNotification("info", tr("classic.skill.disabled", skillID))
				}
			} else {
				a.addNotification("error", tr("classic.skill.toggle_failed", skillID))
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

	// If command palette is active, route to it
	if a.commandPalette.IsVisible() {
		if key == "enter" {
			// Execute selected command
			selected := a.commandPalette.SelectedItem()
			if selected != nil {
				a.executeCommand(selected.ID)
			}
			a.commandPalette.Hide()
			return a, nil
		}
		a.commandPalette.Update(key)
		return a, nil
	}

	// If modal is active, route to it
	if a.activeModal != nil {
		a.activeModal.Update(key)
		return a, nil
	}

	// If the bash dock is focused, it owns navigation keys. Up/Down scroll the
	// background commands; Esc or Up-past-the-top returns focus to the input.
	if a.bashDockFocused {
		if a.bashDockEntryCount() == 0 {
			a.bashDockFocused = false
			return a, nil
		}
		switch key {
		case "enter":
			// Open the full-screen detail takeover for the selected entry
			// (bash command or sub-agent).
			if sel, ok := a.dockSelectedEntry(); ok {
				a.openDockEntryDetail(sel)
			}
			a.invalidateViewportCache()
			return a, nil
		case "down", "j", "ctrl+n":
			a.dockMoveSelection(1)
			a.invalidateViewportCache()
			return a, nil
		case "up", "k", "ctrl+p":
			// Up past the top row → leave the dock, back to the chat/input.
			if !a.dockMoveSelection(-1) {
				a.bashDockFocused = false
			}
			a.invalidateViewportCache()
			return a, nil
		case "esc":
			a.bashDockFocused = false
			a.invalidateViewportCache()
			return a, nil
		case "c":
			// Cancel the selected running command.
			a.cancelSelectedBashDockCommand()
			a.invalidateViewportCache()
			return a, nil
		}
		// Swallow other keys while focused so typing doesn't leak into input.
		return a, nil
	}

	// If in edit message mode, handle navigation
	if a.editMessageMode {
		switch key {
		case "up", "k":
			a.navigateEditMessage(-1)
		case "down", "j":
			a.navigateEditMessage(1)
		case "enter":
			a.confirmEditMessage()
		case "esc":
			a.exitEditMessageMode()
		}
		return a, nil
	}

	// If there's an active command, route to it
	if a.activeCommand != nil && a.activeCommand.IsInteractive() {
		updatedCmd, cmd := a.activeCommand.Update(msg)
		a.activeCommand = updatedCmd

		// Check if command finished
		if !a.activeCommand.IsInteractive() {
			a.activeCommand = nil
		}

		return a, cmd
	}

	// If autocomplete is visible, handle navigation
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
				a.textInput.SetValue(completed)
				a.cmdAutocomplete.Hide()
			}
			return a, nil
		}
	}

	// If mention autocomplete is visible, handle navigation
	if a.mentionAutocomplete.IsVisible() {
		switch key {
		case "down", "ctrl+n":
			a.mentionAutocomplete.Update(msg)
			return a, nil
		case "up", "ctrl+p":
			a.mentionAutocomplete.Update(msg)
			return a, nil
		case "right":
			// Enter selected directory (does nothing for files)
			if navigated, newValue := a.mentionAutocomplete.NavigateInto(); navigated {
				a.textInput.SetValue(newValue)
				a.mentionAutocomplete.SetInput(newValue)
			}
			return a, nil
		case "left":
			// Go up one directory level
			if navigated, newValue := a.mentionAutocomplete.NavigateOut(); navigated {
				a.textInput.SetValue(newValue)
				a.mentionAutocomplete.SetInput(newValue)
			}
			return a, nil
		case "tab":
			// Complete with selected file/folder path
			newValue := a.mentionAutocomplete.CompleteSelection()
			a.textInput.SetValue(newValue)
			a.mentionAutocomplete.SetInput(newValue)
			return a, nil
		case "esc":
			a.mentionAutocomplete.Hide()
			return a, nil
		}
	}

	// Handle Ctrl+E: pop the current prompt into the user's external editor
	// ($VISUAL / $EDITOR, falling back to nvim/vim/nano/vi). This mirrors
	// Claude Code's chat:externalEditor binding. The edited buffer is read
	// back into the prompt input when the editor exits (see
	// handleEditorFinished). tea.ExecProcess suspends bubbletea and hands the
	// terminal to the editor, so this is safe mid-TUI.
	if key == "ctrl+e" {
		logDebug("[handleChatKey] Ctrl+E pressed - opening prompt in external editor")
		return a, a.openPromptInExternalEditor()
	}

	// Handle Ctrl+V: explicit image-paste fallback for tmux environments.
	//
	// Inside tmux the bracketed-paste markers (\e[200~ / \e[201~) may not
	// reach the application because:
	//   - tmux's assume-paste-time intercepts rapid input as its own paste
	//   - The outer terminal's OSC 52 passthrough is blocked by allow-passthrough=off
	//
	// This handler lets Ctrl+V always trigger a direct X11/wl-paste clipboard
	// read regardless of whether tea.PasteMsg fires, so image paste works
	// identically inside and outside tmux.
	if key == "ctrl+v" {
		logDebug("[handleChatKey] Ctrl+V pressed - attempting direct clipboard image read (tmux-safe path)")
		// Re-use the same logic as handlePaste's STEP 1: try clipboard image first.
		clipContent, err := readClipboardContent()
		if err == nil && clipContent != nil && clipContent.IsImage {
			logDebug("[handleChatKey] Ctrl+V: clipboard contains image (%d bytes)", len(clipContent.ImageData))
			const maxAttachmentSize = 5 * 1024 * 1024 // 5 MB
			if int64(len(clipContent.ImageData)) > maxAttachmentSize {
				a.notifications = append(a.notifications, Notification{
					Kind:      "error",
					Text:      tr("classic.image.too_large", float64(len(clipContent.ImageData))/(1024*1024)),
					CreatedAt: time.Now(),
				})
				return a, nil
			}
			const maxAttachments = 5
			if len(a.currentAttachments) >= maxAttachments {
				a.notifications = append(a.notifications, Notification{
					Kind:      "error",
					Text:      tr("classic.image.too_many", maxAttachments),
					CreatedAt: time.Now(),
				})
				return a, nil
			}
			placeholder := a.textInput.AddImageAttachment(clipContent.ImageData, clipContent.MimeType)
			a.textInput.InsertImagePlaceholder(placeholder)
			attachment := Attachment{
				FilePath: "",
				FileName: fmt.Sprintf("clipboard_image_%d.png", a.textInput.imageCounter),
				MimeType: clipContent.MimeType,
				Content:  clipContent.ImageData,
				Size:     int64(len(clipContent.ImageData)),
			}
			a.currentAttachments = append(a.currentAttachments, attachment)
			a.notifications = append(a.notifications, Notification{
				Kind:      "success",
				Text:      tr("classic.image.clipboard_added", attachment.FileName, float64(attachment.Size)/1024),
				CreatedAt: time.Now(),
			})
			logDebug("[handleChatKey] Ctrl+V: added clipboard image attachment: %s (%d bytes)", attachment.FileName, attachment.Size)
			return a, nil
		}
		// No image in clipboard – fall through to text paste via CLI tools.
		logDebug("[handleChatKey] Ctrl+V: no image in clipboard (err=%v), reading text via CLI tools", err)
		textContent, textErr := readClipboardText()
		if textErr == nil && textContent != "" {
			logDebug("[handleChatKey] Ctrl+V: pasting text from clipboard (%d chars)", len(textContent))
			// Simulate a PasteMsg so the SimpleInput's paste indicator logic fires
			cmd := a.textInput.Update(tea.PasteMsg{Content: textContent})
			return a, cmd
		}
		logDebug("[handleChatKey] Ctrl+V: clipboard empty or read failed (textErr=%v)", textErr)
		return a, nil
	}

	// Handle Ctrl+B to background running Bash command
	if key == "ctrl+b" && a.streamingMessage {
		// Find if there's a running Bash tool call
		if len(a.messages) > 0 {
			lastMsg := &a.messages[len(a.messages)-1]
			if lastMsg.Role == "assistant" {
				for _, block := range lastMsg.OrderedBlocks {
					if block.Type == "tool_call" && block.ToolCall != nil && isBashToolName(block.ToolCall.Name) {
						// Check if this tool doesn't have a result yet (still running)
						hasResult := false
						for _, resBlock := range lastMsg.OrderedBlocks {
							if resBlock.Type == "tool_result" && resBlock.ToolResult != nil && resBlock.ToolResult.CallID == block.ToolCall.ID {
								hasResult = true
								break
							}
						}

						if !hasResult {
							// Get the BackgroundBashTool from the tool registry and trigger background
							if a.sdk != nil {
								if tool, err := a.sdk.toolRegistry.Get("Bash"); err == nil {
									if bgTool, ok := tool.(*bgprocess.BackgroundBashTool); ok {
										if handle, err := bgTool.TriggerBackground(); err == nil {
											a.addNotification("success", tr("classic.bash.backgrounded", handle.ID()))
											logDebug("[handleChatKey] Ctrl+B pressed - backgrounded Bash tool, task_id: %s", handle.ID())
											// Start bg process tick chain if not already running
											if !a.bgProcessTickActive {
												a.bgProcessTickActive = true
												return a, tea.Tick(time.Second, func(t time.Time) tea.Msg {
													return bgProcessTickMsg{}
												})
											}
										} else {
											// Tool exists but no command currently executing
											a.addNotification("warning", tr("classic.bash.none_executing"))
											logDebug("[handleChatKey] Ctrl+B pressed - no executing command: %v", err)
										}
									} else {
										// Bash tool exists but isn't BackgroundBashTool (shouldn't happen)
										a.addNotification("error", tr("classic.bash.unsupported"))
										logDebug("[handleChatKey] Ctrl+B pressed - Bash tool is not BackgroundBashTool")
									}
								} else {
									a.addNotification("error", tr("classic.bash.not_found"))
									logDebug("[handleChatKey] Ctrl+B pressed - Bash tool not found: %v", err)
								}
							} else {
								a.addNotification("error", tr("classic.sdk.not_initialized"))
							}
							return a, nil
						}
					}
				}
			}
		}
		// No running Bash command found in message blocks
		a.addNotification("info", tr("classic.bash.none_running"))
		return a, nil
	}

	// Handle special keys first
	switch key {
	case "alt+r":
		return a, a.toggleWordPlayback()

	case "ctrl+enter":
		// Force message: interrupt the agent's current turn (if any) and send
		// NOW, pulling any already-queued messages in ahead of the current
		// input. Plain Enter queues behind a busy agent; Ctrl+Enter jumps the
		// queue. Slash commands keep their normal (non-forced) dispatch below.
		logDebug("[handleChatKey] Ctrl+Enter pressed - force message (interrupt + send)")
		inputValue := strings.TrimSpace(a.textInput.GetSubmitValue())
		if strings.HasPrefix(inputValue, "/") {
			// A "/" command has nothing to force against the agent turn; treat
			// it like a normal Enter dispatch so behavior stays predictable.
			a.inputHistory.Add(inputValue)
			a.pendingNavIdx = 0
			return a, a.handleSlashCommand(inputValue)
		}
		return a, a.forceSendMessage()

	case "shift+enter", "alt+enter":
		// Insert a newline into the input instead of submitting.
		// "shift+enter" fires on terminals with Kitty/extended keyboard protocol.
		// "alt+enter"   fires on all terminals (Alt is always a distinct prefix).
		a.textInput.InsertNewline()
		return a, nil

	case "enter":
		logDebug("[handleChatKey] Enter pressed")

		// Shift modifier present (Kitty protocol encodes it on the "enter" keystroke
		// rather than producing a separate "shift+enter" string on some terminals).
		if msg.Key().Mod&tea.ModShift != 0 {
			a.textInput.InsertNewline()
			return a, nil
		}

		// Ctrl modifier present (Kitty/extended keyboard protocol encodes it on
		// the "enter" keystroke rather than producing a separate "ctrl+enter"
		// string on some terminals). Route to the same force-message path.
		if msg.Key().Mod&tea.ModCtrl != 0 {
			logDebug("[handleChatKey] Ctrl+Enter (via enter mod) - force message")
			forceValue := strings.TrimSpace(a.textInput.GetSubmitValue())
			if strings.HasPrefix(forceValue, "/") {
				a.inputHistory.Add(forceValue)
				a.pendingNavIdx = 0
				return a, a.handleSlashCommand(forceValue)
			}
			return a, a.forceSendMessage()
		}

		// Use GetSubmitValue() (not Value()) so any pasted text stored behind
		// paste-indicator chips (e.g. "[#1 Pasted 3 lines]") is expanded to its
		// real content before dispatch. Otherwise "/goal " + paste would send the
		// literal chip text and drop the pasted content. GetSubmitValue() returns
		// the raw value unchanged when there are no pastes.
		inputValue := strings.TrimSpace(a.textInput.GetSubmitValue())

		// Check if it's a slash command
		if strings.HasPrefix(inputValue, "/") {
			// Save the raw slash command to input history BEFORE dispatching.
			// Slash commands are intercepted here and return early, so they
			// never reach handleSendMessage() where regular messages are added
			// to history. Without this, pressing Up could not recall/resend a
			// previous "/goal ..." (or any "/") command.
			a.inputHistory.Add(inputValue)
			a.pendingNavIdx = 0
			cmd := a.handleSlashCommand(inputValue)
			return a, cmd
		}

		// Regular message
		cmd := a.handleSendMessage()
		return a, cmd

	case "up":
		// In message navigation mode, scroll line-by-line and update focus
		if a.messageNavMode {
			a.msgViewport.ScrollUp(1)
			a.updateFocusedMessageFromScroll()
			// Track scroll-away regardless of streaming state
			if !a.msgViewport.AtBottom() {
				a.setUserScrolledAway(true, "message_nav_up")
			}
			return a, nil
		}

		// Don't navigate history if autocomplete is active
		if a.cmdAutocomplete.IsVisible() || a.mentionAutocomplete.IsVisible() {
			return a, nil
		}

		// Use total wrapped line count for scrollability check
		totalLines := a.textInput.GetTotalWrappedLines()
		maxInputHeight := int(float64(a.height) * 0.4)
		if maxInputHeight < 3 {
			maxInputHeight = 3
		}

		// If input overflows the visible area, scroll it instead of navigating history
		if totalLines > maxInputHeight {
			a.textInput.ScrollUp()
			return a, nil
		}

		// Try moving the cursor up one visual wrap-line first.
		// MoveLineUp returns false only when already on the first visual line,
		// at which point we fall through to history navigation.
		if a.textInput.MoveLineUp() {
			return a, nil
		}

		// Cursor is on the first visual line.
		// Priority 1: navigate pending (queued) messages newest-first with Up.
		a.pendingMsgMu.Lock()
		pendingCount := len(a.pendingUserMessages)
		pendingEntry := ""
		if pendingCount > 0 && a.pendingNavIdx < pendingCount {
			pendingEntry = a.pendingUserMessages[pendingCount-1-a.pendingNavIdx]
			a.pendingNavIdx++
		}
		a.pendingMsgMu.Unlock()
		if pendingEntry != "" {
			a.textInput.SetValue(pendingEntry)
			return a, nil
		}
		// Priority 2: navigate to older history entry.
		currentInput := a.textInput.Value()
		if historyItem, ok := a.inputHistory.NavigateUp(currentInput); ok {
			a.textInput.SetValue(historyItem)
			return a, nil
		}

		return a, nil

	case "down":
		// In message navigation mode, scroll line-by-line and update focus
		if a.messageNavMode {
			a.msgViewport.ScrollDown(1)
			a.updateFocusedMessageFromScroll()
			// Clear scroll-away when user returns to bottom (regardless of streaming)
			if a.msgViewport.AtBottom() {
				a.setUserScrolledAway(false, "message_nav_down_at_bottom")
			}
			return a, nil
		}

		// Don't navigate history if autocomplete is active
		if a.cmdAutocomplete.IsVisible() || a.mentionAutocomplete.IsVisible() {
			return a, nil
		}

		// Use total wrapped line count for scrollability check
		totalLines := a.textInput.GetTotalWrappedLines()
		maxInputHeight := int(float64(a.height) * 0.4)
		if maxInputHeight < 3 {
			maxInputHeight = 3
		}

		// If input overflows the visible area, scroll it instead of navigating history
		if totalLines > maxInputHeight {
			a.textInput.ScrollDown(totalLines)
			return a, nil
		}

		// Try moving the cursor down one visual wrap-line first.
		// MoveLineDown returns false only when already on the last visual line,
		// at which point we fall through to history navigation.
		if a.textInput.MoveLineDown() {
			return a, nil
		}

		// Cursor is on the last visual line.
		// Priority 1: navigate back through pending messages toward the newest.
		if a.pendingNavIdx > 0 {
			a.pendingNavIdx--
			if a.pendingNavIdx > 0 {
				a.pendingMsgMu.Lock()
				pc := len(a.pendingUserMessages)
				var prev string
				if a.pendingNavIdx <= pc {
					prev = a.pendingUserMessages[pc-1-(a.pendingNavIdx-1)]
				}
				a.pendingMsgMu.Unlock()
				if prev != "" {
					a.textInput.SetValue(prev)
					return a, nil
				}
			} else {
				// Navigated all the way back — clear the input box.
				a.textInput.SetValue("")
				return a, nil
			}
		}
		// Priority 2: navigate to newer history entry.
		if historyItem, ok := a.inputHistory.NavigateDown(); ok {
			a.textInput.SetValue(historyItem)
			return a, nil
		}

		// Priority 3: nothing else consumed Down and the cursor is at the
		// bottom of the input — drop focus into the bash dock (if any bash
		// commands exist) so the user can scroll them with Up/Down.
		if a.bashDockEntryCount() > 0 {
			a.bashDockFocused = true
			// Anchor to the first (top) entry by ID.
			if entries := a.collectDockEntries(); len(entries) > 0 {
				a.bashDockSelectedID = entries[0].ID()
			}
			a.invalidateViewportCache()
			return a, nil
		}

		return a, nil
	case "ctrl+down":
		// In message nav mode, jump to next assistant message
		if a.messageNavMode {
			a.focusedMessageIdx = a.findNextAssistantMessage(a.focusedMessageIdx)
			a.scrollToFocusedMessage()
			a.invalidateViewportCache()
			a.updateViewportContent()
			return a, nil
		}
		a.msgViewport.ScrollDown(1)
		// Clear scroll-away when user returns to bottom (regardless of streaming)
		if a.msgViewport.AtBottom() {
			a.setUserScrolledAway(false, "ctrl_down_at_bottom")
		}
		return a, nil
	case "ctrl+up":
		// In message nav mode, jump to previous assistant message
		if a.messageNavMode {
			a.focusedMessageIdx = a.findPreviousAssistantMessage(a.focusedMessageIdx)
			a.scrollToFocusedMessage()
			a.invalidateViewportCache()
			a.updateViewportContent()
			return a, nil
		}
		a.msgViewport.ScrollUp(1)
		// Track scroll-away regardless of streaming state
		if !a.msgViewport.AtBottom() {
			a.setUserScrolledAway(true, "ctrl_up_scroll")
		}
		return a, nil
	case "ctrl+d":
		a.msgViewport.HalfPageDown()
		// Clear scroll-away when user returns to bottom (regardless of streaming)
		if a.msgViewport.AtBottom() {
			a.setUserScrolledAway(false, "ctrl_d_halfpage_at_bottom")
		}
		return a, nil
	case "ctrl+u":
		a.msgViewport.HalfPageUp()
		// Track scroll-away regardless of streaming state
		if !a.msgViewport.AtBottom() {
			a.setUserScrolledAway(true, "ctrl_u_halfpage_scroll")
		}
		return a, nil
	case "home":
		// Home goes to the bottom of the conversation
		logDebug("[handleChatKey] Home pressed - going to bottom")
		a.msgViewport.GotoBottom()
		a.setUserScrolledAway(false, "home_key")
		return a, nil
	case "end":
		// End also goes to the bottom (same as Home)
		logDebug("[handleChatKey] End pressed - going to bottom")
		a.msgViewport.GotoBottom()
		a.setUserScrolledAway(false, "end_key")
		return a, nil
	case "tab":
		// Toggle the right-side info panel. Autocomplete handlers above catch Tab
		// first when any autocomplete is visible, so this only fires in plain chat.
		a.showSidePanel = !a.showSidePanel
		if a.settingsManager != nil {
			if general := a.settingsManager.GetGeneralSettings(); general != nil {
				general.SetSidePanelEnabled(a.showSidePanel)
			}
		}
		a.sidePanelCache.valid = false
		a.applyChatAreaLayout(true)
		a.updateViewportContent()
		return a, nil

	case "esc":
		// Clear selection if active (before existing escape hierarchy)
		if a.msgViewport.IsSelectionActive() {
			a.msgViewport.ClearSelection()
			return a, nil
		}

		// Escape key hierarchy:
		// 1. Exit message navigation mode if active
		// 2. Close autocomplete if open
		// 3. Exit active command if present
		// 4. Show exit modal for chat

		if a.messageNavMode {
			a.messageNavMode = false
			return a, nil
		}

		if a.cmdAutocomplete.IsVisible() {
			a.cmdAutocomplete.Hide()
			return a, nil
		}

		if a.mentionAutocomplete.IsVisible() {
			a.mentionAutocomplete.Hide()
			return a, nil
		}

		if a.activeCommand != nil && a.activeCommand.IsInteractive() {
			// Let command handle escape (it should close itself)
			updatedCmd, cmd := a.activeCommand.Update(msg)
			a.activeCommand = updatedCmd
			if !a.activeCommand.IsInteractive() {
				a.activeCommand = nil
			}
			return a, cmd
		}

		// Show escape menu if we have an active conversation
		if a.activeConv != nil {
			a.showEscapeMenu()
			return a, nil
		}

		return a, nil
	}

	// Copy selection with 'c' key
	if key == "c" {
		if a.msgViewport.IsSelectionActive() {
			text := a.msgViewport.GetSelectedText()
			if text != "" {
				logDebug("[handleChatKey] 'c' pressed - copying %d chars to clipboard", len(text))
				writeClipboard(text)
				// Also send OSC 52 clipboard command for terminal-based clipboard
				cmd := tea.SetClipboard(text)
				// Optional: show a brief notification
				// (omitted to keep the UI minimal)
				return a, cmd
			}
			return a, nil
		}
		// If no selection, fall through to text input (user is typing 'c')
	}

	// Forward to text input
	logDebug("[handleChatKey] Forwarding to textInput.Update()")
	cmd := a.textInput.Update(msg)

	// If user typed a character (not arrow keys, etc), reset history navigation
	k := msg.Key()
	if k.Text != "" {
		a.inputHistory.Reset()
		a.pendingNavIdx = 0 // typing cancels pending-msg navigation
	}

	// Update autocomplete based on input
	inputValue := a.textInput.Value()
	a.cmdAutocomplete.SetInput(inputValue)
	a.mentionAutocomplete.SetInput(inputValue)

	logDebug("[handleChatKey] After Update - input value: '%s'", inputValue)
	return a, cmd
}
