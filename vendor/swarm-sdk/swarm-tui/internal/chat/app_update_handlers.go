package chat

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/Swarm-Code/mono/swarm-core/core"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/conversation"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/mode"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/safego"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/tools"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/chat/commands"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/i18n"
)

// ============================================================================
// STREAMING HANDLERS
// ============================================================================

func (a *App) handleStreamChunk(msg streamChunkMsg) (tea.Model, tea.Cmd) {
	// Append chunk to stream buffer
	a.streamBuffer += msg.content

	// Track output character count for real-time token estimation (Claude Code approach)
	a.streamingOutputChars += len(msg.content)

	// STRATEGY: Use REAL tokens from provider when available, estimate as fallback
	// Providers (Anthropic, OpenAI, Gemini) send real token counts during streaming

	// Initialize streamingInputTokens from last known real value
	if a.streamingInputTokens == 0 {
		// Prefer lastRealTokenCount (verified API-reported value), fall back to tokenCount
		if a.lastRealTokenCount > 0 {
			a.streamingInputTokens = a.lastRealTokenCount
			logDebug("[STREAM-CHUNK] Initialized streamingInputTokens=%d from lastRealTokenCount", a.streamingInputTokens)
		} else if a.tokenCount > 0 {
			a.streamingInputTokens = a.tokenCount
			logDebug("[STREAM-CHUNK] Initialized streamingInputTokens=%d from tokenCount", a.streamingInputTokens)
		}
	}

	// Update REAL input tokens from provider (Anthropic message_start, OpenAI chunks with usage)
	if msg.usage != nil && msg.usage.Input > 0 {
		a.streamingInputTokens = msg.usage.Input
		logDebug("[CHUNK-USAGE] REAL input tokens from provider: %d", a.streamingInputTokens)
	}

	// Update REAL output tokens from provider (if available)
	var outputTokens int
	if msg.usage != nil && msg.usage.Output > 0 {
		// Provider sent REAL output token count - use it!
		outputTokens = msg.usage.Output
		logDebug("[CHUNK-USAGE] REAL output tokens from provider: %d", outputTokens)
	} else {
		// Fallback: estimate output tokens from character count (~4 chars = 1 token)
		outputTokens = a.streamingOutputChars / 4
		logDebug("[CHUNK-USAGE] Estimated output tokens: %d (from %d chars)", outputTokens, a.streamingOutputChars)
	}

	// Calculate new token count: REAL input + REAL/estimated output
	newTokenCount := a.streamingInputTokens + outputTokens

	// Only invalidate cache if token count changed significantly (every ~10 tokens)
	// This prevents excessive re-renders while still showing progress
	tokenDelta := newTokenCount - a.tokenCount
	if tokenDelta >= 10 || (tokenDelta > 0 && a.streamingOutputChars < 100) {
		a.tokenCount = newTokenCount
		// Mark as real if we got real usage from provider, otherwise it's estimated
		a.tokenCountIsEstimate = !(msg.usage != nil && (msg.usage.Input > 0 || msg.usage.Output > 0))
		a.sidePanelCache.valid = false
		logDebug("[STREAM-TOKEN-UPDATE] Cache invalidated: input=%d + output=%d = %d total (delta=+%d)",
			a.streamingInputTokens, outputTokens, a.tokenCount, tokenDelta)
	} else {
		// Update token count silently without invalidating cache
		a.tokenCount = newTokenCount
		// Mark as real if we got real usage from provider, otherwise it's estimated
		a.tokenCountIsEstimate = !(msg.usage != nil && (msg.usage.Input > 0 || msg.usage.Output > 0))
	}

	// Update the last message (assistant) with accumulated content
	if len(a.messages) > 0 && a.messages[len(a.messages)-1].Role == "assistant" {
		// Invalidate caches BEFORE modifying content so subsequent renders never
		// use stale cached content. A streaming chunk only mutates the LAST
		// message's Content, so invalidate just that message instead of every
		// message — invalidating all messages defeats the per-message pre-render
		// cache and forces an O(N) full re-render on every chunk (perf + flicker).
		a.invalidateLastMessageCache()

		a.messages[len(a.messages)-1].Content = a.streamBuffer
		if a.streamingInProgress {
			a.updateStreamingMessageIncremental()
		} else {
			wasAtBottom := a.msgViewport.AtBottom()
			a.updateViewportContent()
			if wasAtBottom {
				a.msgViewport.GotoBottom()
			}
		}
	}

	// Wait for next chunk
	return a, nil
}

func (a *App) handleStreamDone(msg streamDoneMsg) (tea.Model, tea.Cmd) {
	// Streaming complete
	a.streamingMessage = false

	a.streamingInProgress = false               // Reset streaming optimization state
	a.setUserScrolledAway(false, "stream_done") // Reset scroll tracking
	a.animationClock.SetStreaming(false)        // Switch to idle animation
	a.loadingIndicator.Stop(a.animationClock)
	a.spinner.Stop(a.animationClock)
	if a.memoryWatcher != nil {
		a.memoryWatcher.RecordActivity()
	}
	// Update conversation status through centralized activity state.
	a.setActivityPhase(ActivityPhaseIdle, "")
	a.streamBuffer = ""

	// Capture the char-based output estimate before the trackers reset — the
	// metrics block below falls back to it when the provider reported no usage
	// (e.g. local vLLM sends no usage in streaming responses).
	estimatedOutputTokens := a.streamingOutputChars / 4

	// Reset streaming token estimation trackers
	a.streamingInputTokens = 0
	a.streamingOutputChars = 0

	logDebug("Streaming complete - Input=%d, Output=%d, Total=%d", msg.inputTokens, msg.outputTokens, msg.totalTokens)

	// CRITICAL: Flush any pending debounced updates before final render
	if a.inputProtection.pendingUpdate {
		logDebug("[streamDoneMsg] Flushing pending update before final render")
		// Force an immediate update by resetting the timestamp
		a.inputProtection.lastUpdateTime = time.Time{}
		a.inputProtection.pendingUpdate = false
		// Do a final incremental update to show the last streaming content
		if len(a.messages) > 0 && a.messages[len(a.messages)-1].Role == "assistant" {
			a.updateStreamingMessageIncremental()
		}
	}

	// Use inputTokens directly as the current context size (this is what was sent to the model)
	if msg.inputTokens > 0 {
		prevCount := a.tokenCount
		a.tokenCount = msg.inputTokens
		a.lastRealTokenCount = msg.inputTokens // CRITICAL: Track real value for next streaming session
		a.tokenCountIsEstimate = false         // Mark as real, not estimated
		a.recordTokenChange(prevCount, msg.inputTokens, "api_response", fmt.Sprintf("model=%s", a.currentModel))
		logDebug("[APP] Updated tokenCount=%d, lastRealTokenCount=%d (source: msg.inputTokens after streaming)", a.tokenCount, a.lastRealTokenCount)

		// CRITICAL: Persist tokens to conversation JSON so they survive reload
		// This fixes the gap where streaming tokens were only in-memory
		if a.sdk != nil && a.currentConvID != "" {
			ctx := context.Background()
			if err := a.sdk.UpdateConversationTokens(ctx, a.currentConvID, msg.inputTokens); err != nil {
				logDebug("[APP] Failed to persist tokens: %v", err)
			}
		}
	} else if a.sdk != nil && a.currentConvID != "" {
		// Fallback to conversation's stored context size
		ctx := context.Background()
		prevCount := a.tokenCount
		a.tokenCount = a.sdk.GetConversationTokens(ctx, a.currentConvID)
		a.recordTokenChange(prevCount, a.tokenCount, "sdk_fallback", "after_streaming_no_input_tokens")
		if a.tokenCount > 0 {
			a.lastRealTokenCount = a.tokenCount // Track as real value
			a.tokenCountIsEstimate = false
		}
		logDebug("[APP] Updated tokenCount=%d (source: SDK.GetConversationTokens fallback)", a.tokenCount)
	}

	// Finalize metrics after streaming completes.
	//
	// Two paths run in tandem:
	//
	//  1. The legacy TPS delta path (StartStreaming → UpdateTokens → StopStreaming →
	//     FlushCurrentSession) tracks live tokens-per-second for the side panel.
	//     It is brittle: if StartStreaming was never called or UpdateTokens never
	//     fired with a value above TokensAtStart, the flush attributes 0 tokens.
	//     This used to be the only token recorder and produced the all-zero
	//     totals seen in model_metrics.json (see metrics/persistence.go for the
	//     overflow guards added 2026-05-01).
	//
	//  2. RecordTurnTokens (added 2026-05-01) directly attributes the
	//     authoritative per-turn input+output counts reported by the provider
	//     to the current model's lifetime stats. This is the source of truth
	//     for the usage tab's totals — independent of whether streaming
	//     started/ended cleanly.
	//
	// Both paths run; flushCurrentSessionLocked's guards prevent it from
	// double-counting when sessionTokens is 0 or duration is bogus.
	if a.metrics != nil {
		// Fall back to the char-based estimate when the provider reported no
		// usage — otherwise providers like local vLLM never feed the TPS
		// tracker or the per-model history at all.
		outputTokens := msg.outputTokens
		if outputTokens == 0 && estimatedOutputTokens > 0 {
			outputTokens = estimatedOutputTokens
			logDebug("[METRICS] Provider reported no usage; using char-based output estimate: %d", outputTokens)
		}

		// Final TPS feed: OUTPUT tokens only. Counting input+output here used
		// to inject the whole conversation context as a fake token burst.
		if outputTokens > 0 {
			a.metrics.TPS.UpdateOutputTokens(outputTokens)
			logDebug("[METRICS] Updated TPS with final output tokens: %d (in=%d excluded from rate)", outputTokens, msg.inputTokens)
		}

		// Authoritative attribution. Duration: generation time (first output
		// token → last update), so prompt processing doesn't dilute the rate.
		streamDur := time.Duration(a.metrics.TPS.GenerationSeconds() * float64(time.Second))
		modelID := a.currentModel
		modelName := a.currentModelDisplay
		if modelID == "" {
			modelID = a.metrics.CurrentModel
		}
		if modelID != "" && (msg.inputTokens+outputTokens) > 0 {
			a.metrics.RecordTurnTokens(modelID, modelName, msg.inputTokens, outputTokens, streamDur)
			logDebug("[METRICS] Recorded turn tokens for %s: in=%d out=%d genDur=%s",
				modelID, msg.inputTokens, outputTokens, streamDur)
		}

		a.metrics.TPS.StopStreaming()
		a.metrics.FlushCurrentSession()
		if err := a.metrics.SaveToDisk(); err != nil {
			logDebug("[METRICS] Failed to save metrics to disk: %v", err)
		}
	}

	// Publish token update for real-time UI updates
	if a.sdk != nil {
		// Principle 3: State Preservation
		// Store the original provider payload in the final message so we can
		// "restore registers" later.
		if len(msg.rawPayload) > 0 && len(a.messages) > 0 {
			lastMsg := a.messages[len(a.messages)-1]
			if lastMsg.Role == "assistant" {
				lastMsg.RawPayload = msg.rawPayload
				logDebug("[APP] Stored RawPayload in final assistant message (len=%d)", len(msg.rawPayload))
			}
		}

		a.sdk.publishTokenUpdate(TokenUpdate{
			InputTokens:    msg.inputTokens,
			OutputTokens:   msg.outputTokens,
			TotalTokens:    msg.inputTokens + msg.outputTokens,
			IsStreaming:    false,
			IsFinal:        true,
			ConversationID: a.currentConvID,
		})

		// Update cache metrics from streaming response (for cache hit rate tracking)
		if msg.cacheMetrics != nil {
			ctx := context.Background()
			a.sdk.UpdateCacheMetrics(ctx, msg.cacheMetrics)

			// Record cache operations in metrics store
			a.recordCacheMetricsFromResponse(msg.cacheMetrics)
		}
	}
	// CRITICAL: Invalidate cache so spinner is removed
	a.invalidateViewportCache()
	a.updateViewportContent()

	return a, nil
}

func (a *App) handleStreamError(msg streamErrorMsg) (tea.Model, tea.Cmd) {
	// Handle streaming error
	a.streamingMessage = false

	a.streamingInProgress = false                // Reset streaming optimization state
	a.setUserScrolledAway(false, "stream_error") // Reset scroll tracking
	a.animationClock.SetStreaming(false)         // Switch to idle animation
	a.loadingIndicator.Stop(a.animationClock)
	a.spinner.Stop(a.animationClock)
	if a.memoryWatcher != nil {
		a.memoryWatcher.RecordActivity()
	}
	// Update conversation status through centralized activity state.
	a.setActivityPhase(ActivityPhaseIdle, "")
	a.streamBuffer = ""

	// Reset streaming token estimation trackers
	a.streamingInputTokens = 0
	a.streamingOutputChars = 0

	logDebug("Streaming error: %v", msg.err)

	// Update last message to show error
	if len(a.messages) > 0 && a.messages[len(a.messages)-1].Role == "assistant" {
		a.messages[len(a.messages)-1].Content = i18n.T("classic_chat.stream.error", msg.err)
	}
	// CRITICAL: Invalidate cache so spinner is removed
	a.invalidateViewportCache()
	a.updateViewportContent()
	return a, nil
}

// ============================================================================
// THINKING COMMAND HANDLERS
// ============================================================================

func (a *App) handleThinkingToggle(msg commands.ThinkingToggleMsg) (tea.Model, tea.Cmd) {
	// Toggle thinking mode
	logDebug("[THINKING] ThinkingToggleMsg received, sdk=%v", a.sdk != nil)
	if a.sdk != nil {
		enabled := a.sdk.ToggleThinking()
		status := "disabled"
		statusIcon := "🔴"
		if enabled {
			status = fmt.Sprintf("enabled (budget: %d tokens)", a.sdk.GetThinkingBudget())
			statusIcon = "🧠"
		}
		logDebug("[THINKING] Toggled to: %s (sdk.thinkingEnabled=%v)", status, a.sdk.IsThinkingEnabled())

		// NOTE: showThinking (display) is now independent from SDK thinking mode.
		// Use Ctrl+Alt+T to toggle thinking display visibility separately.

		// Force rebuild with new display settings
		a.invalidateViewportCache()
		a.updateViewportContent()

		// Log to debug screen for visibility
		if a.debugScreen != nil {
			a.debugScreen.AddLog(fmt.Sprintf("[THINKING] %s Extended Thinking %s", statusIcon, status))
		}

		// Persist SDK mode to render settings (not display toggle)
		if a.renderSettings != nil {
			a.renderSettings.ThinkingBudget = a.sdk.GetThinkingBudget()
			if err := SaveRenderSettings(a.renderSettings); err != nil {
				logDebug("[THINKING] Failed to save render settings: %v", err)
			}
		}
	}
	return a, nil
}

func (a *App) handleThinkingEnable(msg commands.ThinkingEnableMsg) (tea.Model, tea.Cmd) {
	// Enable thinking with specific budget
	logDebug("[THINKING] ThinkingEnableMsg received, sdk=%v, budget=%d", a.sdk != nil, msg.Budget)
	if a.sdk != nil {
		a.sdk.EnableThinking(msg.Budget)
		logDebug("[THINKING] Enabled with budget: %d (sdk.thinkingEnabled=%v)", msg.Budget, a.sdk.IsThinkingEnabled())

		// Force rebuild with new display settings
		a.invalidateViewportCache()
		a.updateViewportContent()

		// Log to debug screen for visibility
		if a.debugScreen != nil {
			a.debugScreen.AddLog(fmt.Sprintf("[THINKING] 🧠 Extended Thinking enabled (budget: %d tokens)", msg.Budget))
		}

		// Persist SDK mode to render settings (not display toggle)
		if a.renderSettings != nil {
			a.renderSettings.ThinkingBudget = msg.Budget
			if err := SaveRenderSettings(a.renderSettings); err != nil {
				logDebug("[THINKING] Failed to save render settings: %v", err)
			}
		}
	}
	return a, nil
}

func (a *App) handleThinkingDisable(msg commands.ThinkingDisableMsg) (tea.Model, tea.Cmd) {
	// Disable thinking
	logDebug("[THINKING] ThinkingDisableMsg received, sdk=%v", a.sdk != nil)
	if a.sdk != nil {
		a.sdk.DisableThinking()
		logDebug("[THINKING] Disabled (sdk.thinkingEnabled=%v)", a.sdk.IsThinkingEnabled())

		// Force rebuild with new display settings
		a.invalidateViewportCache()
		a.updateViewportContent()

		// Log to debug screen for visibility
		if a.debugScreen != nil {
			a.debugScreen.AddLog("[THINKING] 🔴 Extended Thinking disabled")
		}

		// Persist SDK mode to render settings (not display toggle)
		if a.renderSettings != nil {
			if err := SaveRenderSettings(a.renderSettings); err != nil {
				logDebug("[THINKING] Failed to save render settings: %v", err)
			}
		}
	}
	return a, nil
}

// ============================================================================
// MODE COMMAND HANDLERS
// ============================================================================

func (a *App) handleModeSwitch(msg commands.ModeSwitchMsg) (tea.Model, tea.Cmd) {
	// Switch operating mode
	logDebug("[MODE] ModeSwitchMsg received: mode=%s, sdk=%v", msg.Mode, a.sdk != nil)
	if a.sdk != nil {
		prevMode := a.operatingMode

		a.sdk.SetOperatingMode(msg.Mode)
		a.operatingMode = msg.Mode // Update local state for sidepanel
		modeUpper := strings.ToUpper(msg.Mode)
		if modeUpper == "" {
			modeUpper = "OFF"
		}

		// ── AUTO mode permission coupling ────────────────────────────────────
		// When entering AUTO mode: escalate permissions to YOLO so tools
		// execute without interrupting the agent for approvals.
		// When leaving AUTO mode: restore the permission level that was active
		// before AUTO was entered, so the user isn't left in YOLO unexpectedly.
		enteringAuto := msg.Mode == mode.ModeAuto && prevMode != mode.ModeAuto
		leavingAuto := prevMode == mode.ModeAuto && msg.Mode != mode.ModeAuto

		if enteringAuto {
			// Save current level before escalating.
			a.preAutoPermissionLevel = a.currentPermissionLevel()
			if err := a.sdk.UpdatePermissionLevel(tools.LevelYOLO); err != nil {
				logDebug("[MODE] Failed to set YOLO permission for AUTO mode: %v", err)
			} else {
				a.addNotification("success", i18n.T("classic_chat.mode.auto_yolo"))
				logDebug("[MODE] AUTO mode entered — permissions escalated to YOLO (was %s)",
					a.preAutoPermissionLevel)
			}
		} else if leavingAuto {
			// Restore the pre-AUTO permission level.
			restoreTo := a.preAutoPermissionLevel
			if restoreTo == "" {
				restoreTo = tools.LevelBalanced // safe fallback
			}
			if err := a.sdk.UpdatePermissionLevel(restoreTo); err != nil {
				logDebug("[MODE] Failed to restore permission level after AUTO mode: %v", err)
			} else {
				a.preAutoPermissionLevel = ""
				a.addNotification("info", i18n.T("classic_chat.mode.auto_left", strings.ToUpper(string(restoreTo))))
				logDebug("[MODE] AUTO mode exited — permissions restored to %s", restoreTo)
			}
		} else {
			// Normal mode switch (not involving AUTO).
			a.addNotification("success", i18n.T("classic_chat.mode.switched", modeUpper))
		}

		logDebug("[MODE] Mode set to: %s", msg.Mode)
		if a.debugScreen != nil {
			a.debugScreen.AddLog(fmt.Sprintf("[MODE] ✓ Switched to %s mode", modeUpper))
		}

		// NOTE: Do NOT add mode message to history here.
		// Mode switching via hotkey is UI-only - it updates the sidepanel
		// and tool filtering, but does NOT modify message history.
		// Mode messages are only appended when a message is actually sent
		// (see appendModeMessageIfChanged in message send logic).
		// This preserves cache prefix stability for Anthropic's prompt caching.
	}
	return a, nil
}

func (a *App) handleModeShow() (tea.Model, tea.Cmd) {
	// Show current mode
	logDebug("[MODE] ModeShowMsg received, sdk=%v", a.sdk != nil)
	if a.sdk != nil {
		currentMode := a.sdk.GetOperatingMode()
		modeUpper := strings.ToUpper(currentMode)
		if modeUpper == "" {
			modeUpper = "OFF"
		}
		a.addNotification("info", i18n.T("classic_chat.mode.current", modeUpper))
	}
	return a, nil
}

// ============================================================================
// AUTO-MODE COMMAND HANDLERS
// ============================================================================

// handleAutoModeToggle flips the auto-mode opt-in state. Auto-mode uses the
// builtin.AutoModeHook classifier to auto-approve / auto-deny tool calls
// (with the risk banner surfaced on the modal for the "prompt" verdict).
func (a *App) handleAutoModeToggle() (tea.Model, tea.Cmd) {
	if a.sdk == nil || a.sdk.GetHooksManager() == nil {
		a.addNotification("error", i18n.T("classic_chat.automode.hooks_uninitialized"))
		return a, nil
	}
	h := a.sdk.GetHooksManager().GetAutoModeHook()
	if h == nil {
		a.addNotification("error", i18n.T("classic_chat.automode.hook_missing"))
		return a, nil
	}
	cfg := h.GetConfig()
	cfg.SkipAutoPermissionPrompt = !cfg.SkipAutoPermissionPrompt
	h.UpdateConfig(cfg)
	if cfg.SkipAutoPermissionPrompt {
		a.addNotification("success", i18n.T("classic_chat.automode.on_detail"))
	} else {
		a.addNotification("info", i18n.T("classic_chat.automode.off_detail"))
	}
	return a, nil
}

// handleAutoModeSet sets the opt-in state explicitly.
func (a *App) handleAutoModeSet(enabled bool) (tea.Model, tea.Cmd) {
	if a.sdk == nil || a.sdk.GetHooksManager() == nil {
		a.addNotification("error", i18n.T("classic_chat.automode.hooks_uninitialized"))
		return a, nil
	}
	h := a.sdk.GetHooksManager().GetAutoModeHook()
	if h == nil {
		a.addNotification("error", i18n.T("classic_chat.automode.hook_missing"))
		return a, nil
	}
	cfg := h.GetConfig()
	cfg.SkipAutoPermissionPrompt = enabled
	h.UpdateConfig(cfg)
	if enabled {
		a.addNotification("success", i18n.T("classic_chat.automode.enabled"))
	} else {
		a.addNotification("info", i18n.T("classic_chat.automode.disabled"))
	}
	return a, nil
}

// handleAutoModeStatus reports the current opt-in state and rule counts.
func (a *App) handleAutoModeStatus() (tea.Model, tea.Cmd) {
	if a.sdk == nil || a.sdk.GetHooksManager() == nil {
		a.addNotification("error", i18n.T("classic_chat.automode.unavailable"))
		return a, nil
	}
	h := a.sdk.GetHooksManager().GetAutoModeHook()
	if h == nil {
		a.addNotification("error", i18n.T("classic_chat.automode.unavailable"))
		return a, nil
	}
	cfg := h.GetConfig()
	state := "OFF"
	if cfg.SkipAutoPermissionPrompt {
		state = "ON"
	}
	a.addNotification("info", i18n.T("classic_chat.automode.status",
		state, len(cfg.AutoMode.Allow), len(cfg.AutoMode.SoftDeny), cfg.UseAutoModeDuringPlan))
	return a, nil
}

// =============================================================================
// THEME COMMAND HANDLERS
// =============================================================================

func (a *App) handleThemeSelected(msg commands.ThemeSelectedMsg) (tea.Model, tea.Cmd) {
	logDebug("[THEME] ThemeSelectedMsg received: theme=%s", msg.ThemeName)

	// Apply the theme directly (same logic as the theme change callback in app_init.go)
	currentBGMode := "solid"
	if a.renderSettings != nil {
		currentBGMode = a.renderSettings.BackgroundMode
	}

	// Apply the new theme to the live app
	newTheme := ThemeByName(msg.ThemeName)
	if currentBGMode == "none" {
		// Clear ALL background fields for transparent mode
		newTheme.BG = ""
		newTheme.BGLight = ""
		newTheme.BGLighter = ""
	}
	a.theme = newTheme

	// Propagate new theme colours to every component that caches them
	a.propagateTheme(newTheme)

	// Invalidate all render caches so next frame picks up new colours
	if a.messageCache != nil {
		a.messageCache.Clear()
	}

	// Update settings manager state if available
	if a.settingsManager != nil {
		a.settingsManager.SetTheme(msg.ThemeName, currentBGMode)
	}

	// Persist the selection
	if a.renderSettings != nil {
		a.renderSettings.ThemeName = msg.ThemeName
		a.renderSettings.BackgroundMode = currentBGMode
		if err := SaveRenderSettings(a.renderSettings); err != nil {
			logDebug("[THEME] Failed to save render settings: %v", err)
		}
	}

	a.addNotification("success", i18n.T("classic_chat.theme.changed", msg.ThemeName))
	return a, nil
}

// COMPACTION HANDLERS
// ============================================================================

func (a *App) handleCompactRequest(msg commands.CompactRequestMsg) (tea.Model, tea.Cmd) {
	// Compaction requested - either manual (/compact) or auto (threshold triggered)

	// Get current token count for logging
	tokenCount := 0
	messageCount := len(a.messages)
	if a.sdk != nil && a.currentConvID != "" {
		ctx := context.Background()
		tokenCount = a.sdk.GetConversationTokens(ctx, a.currentConvID)
	}

	// === DEBUG: Log compaction trigger ===
	a.compactDebugSection("COMPACTION TRIGGERED")
	a.compactDebugKeyValue("Trigger Type", map[bool]string{true: "Manual (/compact)", false: "Auto (threshold)"}[msg.Manual])
	a.compactDebugKeyValue("Strategy", msg.Strategy)
	a.compactDebugKeyValue("Current Tokens", tokenCount)
	a.compactDebugKeyValue("Message Count", messageCount)
	a.compactDebugKeyValue("Conversation ID", a.currentConvID)

	// Always compact when requested - no threshold check for /compact.
	if a.compactionService != nil && a.sdk != nil && a.currentConvID != "" {
		strategyName := string(msg.Strategy)
		if strategyName == "" {
			strategyName = "standard"
		}
		compactionModelDisplay := a.currentModel
		if a.currentProvider != "" {
			compactionModelDisplay = fmt.Sprintf("%s/%s", a.currentProvider, a.currentModel)
		}

		a.compactDebugKeyValue("Active Compaction Model", compactionModelDisplay)
		a.compactDebugLog("  ⏳ Starting background compaction process...")

		// Set compacting state and show loading indicator
		a.isCompacting = true
		a.lastCompactionAttempt = time.Now() // cooldown baseline for deferred auto-compaction
		// Switch the shared loading indicator to determinate-bar mode for the
		// duration of compaction so real pipeline-stage progress (see
		// compaction.CompactionConfig.ProgressFunc, wired in performCompaction)
		// renders instead of a plain spinner. Restored in handleCompactCompleted
		// / handleCompactError so the user's chosen streaming-spinner style
		// (Ctrl+cycle) is unaffected once compaction finishes.
		a.preCompactIndicatorType = a.loadingIndicator.GetIndicatorType()
		a.loadingIndicator.SetIndicatorType(LoadingBar)
		a.loadingIndicator.ClearProgress()
		a.loadingIndicator.SetText(i18n.T("classic_chat.compaction.progress", strategyName))
		tickCmd := a.loadingIndicator.Start(a.animationClock)

		// Add notification to show compaction is in progress
		a.addNotification("info", i18n.T("classic_chat.compaction.started", tokenCount, strategyName, compactionModelDisplay))

		// Capture the request and conversation ID for the closure — the cmd
		// runs in a goroutine and must not read a.currentConvID from there.
		compactReq := msg
		convID := a.currentConvID
		runSnapshot := a.snapshotCompactionRun(compactReq, convID)

		// Execute compaction in background
		// Use tea.Batch to combine the animation tick command with the compaction command
		// This ensures the spinner animates while compaction runs
		compactCmd := func() tea.Msg {
			result, compCtx, err := a.performCompaction(compactReq, convID, runSnapshot)
			if err != nil {
				return commands.CompactErrorMsg{Error: err.Error(), Manual: compactReq.Manual}
			}
			if result.Error != nil {
				return commands.CompactErrorMsg{Error: result.Error.Error(), Manual: compactReq.Manual}
			}

			// Build list of recovered file paths
			var recoveredPaths []string
			for _, f := range result.RecoveredFiles {
				recoveredPaths = append(recoveredPaths, f.Path)
			}

			// Get mode and todo counts from context
			modeName := ""
			todoCount := 0
			modifiedCount := 0
			if compCtx != nil {
				modeName = compCtx.CurrentMode
				todoCount = len(compCtx.ActiveTodos)
				modifiedCount = len(compCtx.ModifiedFiles)
			}

			return commands.CompactCompletedMsg{
				OriginalTokens:    result.OriginalTokens,
				CompactedTokens:   result.CompactedTokens,
				Summary:           result.Summary,
				FileCount:         len(result.RecoveredFiles),
				NewConvID:         result.NewConvID,
				Manual:            compactReq.Manual,
				RecoveryMethod:    result.RecoveryMethod,
				SummaryAttempts:   result.SummaryAttempts,
				SizeWarning:       result.SizeWarning,
				FallbackUsed:      result.FallbackUsed,
				UsedProvider:      result.UsedProvider,
				UsedModel:         result.UsedModel,
				Strategy:          compactReq.Strategy,
				Mode:              modeName,
				ActiveTodoCount:   todoCount,
				ModifiedFileCount: modifiedCount,
				RecoveredFiles:    recoveredPaths,
			}
		}

		// Batch both commands - tickCmd starts the animation, compactCmd runs compaction
		return a, tea.Batch(tickCmd, compactCmd)
	}

	// No SDK or conversation - nothing to compact
	a.compactDebugLog("  ⚠️ No active conversation to compact")
	return a, nil
}

// applyPostCompactionTokenCounts syncs the live TUI token counters to a
// (possibly estimated) post-compaction context size.
//
// lastRealTokenCount is reset UNCONDITIONALLY when the size is > 0 — even
// though a compacted size is flagged estimated. AdvanceActiveContext ALWAYS
// sets CurrentContextSizeEstimated=true (a compacted size is inherently an
// estimate; only a real provider response clears the flag). The previous
// `!estimated` guard therefore never ran post-compaction and left
// lastRealTokenCount at the STALE pre-compaction total. The pre-send
// auto-compaction predictor (sendMessage) reads lastRealTokenCount, so a stale
// value makes predictedTotal exceed the threshold and re-fires compaction on
// the very next send (compaction "runs twice"). The estimate is the correct
// current-context magnitude; a stale real value is not. Mirrors the SDK-side
// Agent.ResetContextTokens performed at the compaction commit.
func (a *App) applyPostCompactionTokenCounts(conv *conversation.Conversation) {
	if conv == nil {
		return
	}
	a.tokenCount = conv.CurrentContextSize
	a.tokenCountIsEstimate = conv.CurrentContextSizeEstimated
	if conv.CurrentContextSize > 0 {
		a.lastRealTokenCount = conv.CurrentContextSize
	}
}

func (a *App) handleCompactCompleted(msg commands.CompactCompletedMsg) (tea.Model, tea.Cmd) {
	// Stop loading indicator and reset compacting state
	a.isCompacting = false
	a.loadingIndicator.Stop(a.animationClock)
	a.loadingIndicator.ClearProgress()
	a.loadingIndicator.SetIndicatorType(a.preCompactIndicatorType)
	a.setActivityPhase(ActivityPhaseIdle, "")

	// Switch to the new compacted conversation
	if msg.NewConvID != "" {
		oldConvID := a.currentConvID
		a.setCurrentConversationID(msg.NewConvID)
		if a.activeConv != nil {
			a.activeConv.ID = msg.NewConvID
		}
		logDebug("[COMPACT] Switched from conversation %s to %s", oldConvID, msg.NewConvID)

		// Rebuild the visible message list from the compacted conversation.
		// This mutation used to live inside performCompaction, which runs in a
		// tea.Cmd goroutine and raced the render loop; a.messages is owned by
		// the Update loop, so the rebuild happens here.
		ctx := context.Background()
		if sdkMessages, err := a.sdk.GetMessages(ctx, msg.NewConvID); err == nil {
			ptrMessages := convertSDKMessages(sdkMessages)
			rebuilt := make([]Message, 0, len(ptrMessages)+1)
			for _, m := range ptrMessages {
				rebuilt = append(rebuilt, *m)
			}
			a.messages = rebuilt
			// Re-inject the system prompt at the top of the list — the
			// compacted messages are all user/assistant role, so it would
			// otherwise disappear from the view.
			a.addSystemPromptToHistory()
			// The slice shrank: drop cached positions/indices that referenced
			// the old message list.
			a.focusedMessageIdx = 0
			a.messageLinePositions = nil
		} else {
			logDebug("[handleCompactCompleted] Failed to reload messages from %s: %v", msg.NewConvID, err)
		}
	}

	// Success closes the proactive-compaction circuit breaker.
	a.compactionFailCount = 0
	a.compactionBlocked = false
	// Re-arm in-loop auto-compaction in case the circuit breaker disabled it
	// on the live agent (handleAutoCompactionFailed).
	a.syncAutoCompactionConfig()

	// === DEBUG: Log final completion summary ===
	a.logCompactionComplete(
		msg.OriginalTokens,
		msg.CompactedTokens,
		msg.Strategy,
		msg.Mode,
		msg.FileCount,
		msg.ActiveTodoCount,
	)

	// Log recovered files if any
	if len(msg.RecoveredFiles) > 0 {
		a.compactDebugLog("  Recovered Files:")
		for i, path := range msg.RecoveredFiles {
			a.compactDebugLog("    [%d] %s", i+1, path)
		}
	}

	reduction := 0
	if msg.OriginalTokens > 0 {
		reduction = 100 - (msg.CompactedTokens * 100 / msg.OriginalTokens)
	}

	// Finalize compaction immediately - no animation delay
	// Build simple post-compaction message
	var sysMsg strings.Builder
	sysMsg.WriteString(i18n.T("classic_chat.compaction.system_summary",
		msg.OriginalTokens, msg.CompactedTokens, reduction))
	if msg.FileCount > 0 {
		sysMsg.WriteString(i18n.T("classic_chat.compaction.files_recovered", msg.FileCount))
	}

	// Add post-compaction system message to chat
	a.messages = append(a.messages, Message{
		Role:    "system",
		Content: sysMsg.String(),
	})

	// Add success notification
	a.addNotification("success", i18n.T("classic_chat.compaction.completed",
		msg.OriginalTokens, msg.CompactedTokens, reduction))
	if msg.RecoveryMethod == "deterministic" {
		a.addNotification("warning", i18n.T("classic_chat.compaction.deterministic_recovery"))
	} else if msg.RecoveryMethod == "chunked" {
		a.addNotification("info", i18n.T("classic_chat.compaction.chunked", msg.SummaryAttempts))
	}
	if msg.SizeWarning != "" {
		a.addNotification("warning", msg.SizeWarning)
	}
	if msg.FallbackUsed {
		a.addNotification("warning", i18n.T("classic_chat.compaction.fallback_model", msg.UsedProvider, msg.UsedModel))
	}

	// Update token count and preserve whether it is still only a post-compact
	// estimate. The next provider response clears the estimated flag.
	if a.sdk != nil && a.currentConvID != "" {
		ctx := context.Background()
		if conv := a.sdk.GetConversation(ctx, a.currentConvID); conv != nil {
			a.applyPostCompactionTokenCounts(conv)
		}
		logDebug("[APP] Updated tokenCount=%d lastRealTokenCount=%d (source: SDK.GetConversationTokens after compaction)", a.tokenCount, a.lastRealTokenCount)
	}

	// Clear and rebuild viewport
	a.invalidateViewportCache()
	a.updateViewportContent()

	// CRITICAL: Reload conversation list so the new compacted conversation appears with correct timestamp
	// Without this, the compacted conversation won't show in the sidebar until you navigate away
	a.loadConversationsFromSDK()

	// Find and select the new compacted conversation in the list
	if msg.NewConvID != "" {
		for i, conv := range a.conversations {
			if conv.ID == msg.NewConvID {
				a.selectedIdx = i
				logDebug("[handleCompactCompleted] Selected new compacted conversation at index %d", i)
				break
			}
		}
	}

	// A manual or pre-send compaction can finish while user/system prompts are
	// queued. Drain them here so they continue against the compacted
	// conversation: queued user messages first, then leftover system
	// notifications (cron/goal continuations).
	a.pendingMsgMu.Lock()
	pending := a.pendingUserMessages
	a.pendingUserMessages = nil
	a.pendingMsgMu.Unlock()
	if len(pending) > 0 {
		combined := strings.Join(pending, "\n\n---\n\n")
		logDebug("[handleCompactCompleted] Auto-sending %d queued messages (%d chars)", len(pending), len(combined))
		a.textInput.SetValue(combined)
		return a, a.handleSendMessage()
	}
	if wakeCmd := a.wakePendingSystemMessages(); wakeCmd != nil {
		logDebug("[handleCompactCompleted] Waking agent for queued system notifications")
		return a, wakeCmd
	}
	// An explicit /compact is also a continuation trigger: the user asked the
	// agent to compact the active context, so leave it at the same
	// compacted-context boundary and resume without requiring a second prompt.
	// Automatic compaction remains opt-in for continuation because it may have
	// been configured only as a safety compaction.
	if msg.Manual {
		logDebug("[handleCompactCompleted] Resuming after explicit /compact")
		return a, a.startBgProcessWakeCmd("Continue the interrupted task from the compacted context.")
	}
	if !msg.Manual && a.settingsManager != nil {
		if compactionSettings := a.settingsManager.GetCompactionSettings(); compactionSettings != nil &&
			compactionSettings.GetAutoCompactionContinueIfRunning() {
			logDebug("[handleCompactCompleted] Resuming interrupted task after automatic compaction")
			return a, a.startBgProcessWakeCmd("Continue the interrupted task from the compacted context.")
		}
	}

	return a, nil
}

func (a *App) handleCompactError(msg commands.CompactErrorMsg) (tea.Model, tea.Cmd) {
	// Stop loading indicator and reset compacting state
	a.isCompacting = false
	a.compactionBlocked = true
	// Count toward the proactive-compaction circuit breaker (reset on the next
	// successful compaction in handleCompactCompleted).
	a.compactionFailCount++
	a.loadingIndicator.Stop(a.animationClock)
	a.loadingIndicator.ClearProgress()
	a.loadingIndicator.SetIndicatorType(a.preCompactIndicatorType)
	a.setActivityPhase(ActivityPhaseIdle, "")

	// === DEBUG: Log compaction error ===
	a.logCompactionError("execution", fmt.Errorf("%s", msg.Error))

	// Add error notification
	a.addNotification("error", i18n.T("classic_chat.compaction.failed", msg.Error))

	// Keep queued messages intact. Sending them into the same oversized context
	// would transform this causal error into an opaque provider failure.
	a.addNotification("error", i18n.T("classic_chat.compaction.blocked"))
	return a, nil
}

// handleReindexRequest processes a request to reindex conversation titles
func (a *App) handleReindexRequest(msg commands.ReindexRequestMsg) (tea.Model, tea.Cmd) {
	if a.sdk == nil {
		a.addNotification("error", i18n.T("classic_chat.reindex.sdk_unavailable"))
		return a, nil
	}

	// Perform reindexing in background
	safego.Go("chat.reindexConversationTitles", func() {
		processed := a.reindexConversationTitles(context.Background(), msg.ScopeAll)
		// Send completion message via runtime
		a.sendToRuntime(commands.ReindexCompletedMsg{Processed: processed})
	})

	if msg.ScopeAll {
		a.addNotification("info", i18n.T("classic_chat.reindex.started_all", maxReindexBatch))
	} else {
		a.addNotification("info", i18n.T("classic_chat.reindex.started_folder", maxReindexBatch))
	}
	return a, nil
}

// handleReindexCompleted handles completion of title reindexing
func (a *App) handleReindexCompleted(msg commands.ReindexCompletedMsg) (tea.Model, tea.Cmd) {
	// Reload conversations to show new titles
	a.loadConversationsFromSDK()

	if msg.Processed > 0 {
		a.addNotification("success", i18n.T("classic_chat.reindex.completed", msg.Processed))
	} else {
		a.addNotification("info", i18n.T("classic_chat.reindex.none"))
	}
	return a, nil
}

// handleReindexError handles errors during title reindexing
func (a *App) handleReindexError(msg commands.ReindexErrorMsg) (tea.Model, tea.Cmd) {
	a.addNotification("error", i18n.T("classic_chat.reindex.failed", msg.Error))
	return a, nil
}

func (a *App) handleClaudeImportResult(msg commands.ClaudeImportResultMsg) (tea.Model, tea.Cmd) {
	// Handle import completion and save conversations
	if msg.Error != "" {
		a.addNotification("error", i18n.T("classic_chat.import.failed", msg.Error))
		return a, nil
	}

	// Save imported conversations and load the first one
	if a.sdk != nil && a.sdk.sdkClient != nil && len(msg.ImportedConversations) > 0 {
		ctx := context.Background()
		var firstConvID string

		for i, conv := range msg.ImportedConversations {
			if err := a.sdk.saveConv(ctx, conv); err != nil {
				logDebug("[IMPORT] Error saving conversation %d: %v", i, err)
				a.addNotification("error", i18n.T("classic_chat.import.save_failed", conv.ID, err))
			} else {
				logDebug("[IMPORT] Successfully saved conversation %d: %s", i, conv.ID)
				if i == 0 {
					firstConvID = conv.ID
				}
			}
		}

		// Load the first imported conversation into the chat view
		if firstConvID != "" {
			logDebug("[IMPORT] Loading conversation %s into chat view", firstConvID)

			// Set as active conversation with minimal TUI Conversation object
			a.activeConv = &Conversation{
				ID:     firstConvID,
				Title:  i18n.T("classic_chat.import.title"),
				Status: "idle",
			}
			a.setCurrentConversationID(firstConvID)

			// Load messages from SDK storage
			a.loadMessagesFromSDK(firstConvID)
			logDebug("[IMPORT] Loaded %d messages for imported conversation", len(a.messages))

			// Switch to chat screen
			a.screen = ScreenChat
			a.msgViewport.GotoBottom()
			a.viewNeedsRefresh = true

			a.addNotification("success", i18n.T("classic_chat.import.completed_displaying", msg.ImportedCount))
		} else {
			a.addNotification("success", i18n.T("classic_chat.import.completed", msg.ImportedCount))
		}
	}

	return a, nil
}

func (a *App) handleClaudeExportRequest() (tea.Model, tea.Cmd) {
	// Handle export request - export the current conversation to Claude Code format
	if a.activeConv == nil || a.currentConvID == "" {
		a.addNotification("error", i18n.T("classic_chat.export.none_selected"))
		return a, nil
	}

	logDebug("[EXPORT] Exporting conversation %s", a.currentConvID)

	// Get the conversation from SDK
	ctx := context.Background()
	sdkConv := a.sdk.GetConversation(ctx, a.currentConvID)
	if sdkConv == nil {
		a.addNotification("error", i18n.T("classic_chat.export.load_failed"))
		return a, nil
	}

	// Perform export async
	return a, func() tea.Msg {
		// Use a helper function in commands to perform the export
		exportPath, exportErr := commands.DoClaudeExport(sdkConv, a.currentConvID)
		if exportErr != nil {
			logDebug("[EXPORT] Error: %v", exportErr)
			return commands.ClaudeExportResultMsg{
				ConvID: a.currentConvID,
				Error:  exportErr.Error(),
			}
		}

		logDebug("[EXPORT] Successfully exported to %s", exportPath)
		return commands.ClaudeExportResultMsg{
			ConvID:       a.currentConvID,
			ExportedPath: exportPath,
		}
	}
}

func (a *App) handleClaudeExportResult(msg commands.ClaudeExportResultMsg) (tea.Model, tea.Cmd) {
	// Handle export completion
	if msg.Error != "" {
		a.addNotification("error", i18n.T("classic_chat.export.failed", msg.Error))
		return a, nil
	}

	a.addNotification("success", i18n.T("classic_chat.export.completed", msg.ExportedPath))
	return a, nil
}

func (a *App) handleMicroCompactToggle(msg commands.MicroCompactToggleMsg) (tea.Model, tea.Cmd) {
	// Toggle or set micro-compaction state
	currentConfig := a.GetConfig()
	if currentConfig == nil {
		a.addNotification("error", i18n.T("classic_chat.config.load_failed"))
		return a, nil
	}

	newState := !currentConfig.EnableMicroCompaction
	if !msg.Toggle {
		newState = msg.Enable
	}

	// Update config
	if err := a.UpdateConfig(func(cfg *core.Config) {
		cfg.EnableMicroCompaction = newState
	}); err != nil {
		a.addNotification("error", i18n.T("classic_chat.config.save_failed", err))
		return a, nil
	}

	// Show notification
	status := i18n.T("classic_chat.common.enabled")
	if !newState {
		status = i18n.T("classic_chat.common.disabled")
	}
	a.addNotification("success", i18n.T("classic_chat.microcompaction.changed",
		status, currentConfig.MicroRetentionCount))

	// Invalidate sidepanel cache to show updated status
	a.sidePanelCache.valid = false
	return a, nil
}

func (a *App) handleMicroCompactStatusRequest() (tea.Model, tea.Cmd) {
	// Return current micro-compaction status
	currentConfig := a.GetConfig()
	if currentConfig == nil {
		a.addNotification("error", i18n.T("classic_chat.config.load_failed"))
		return a, nil
	}

	// TODO: Get actual stats from micro-compaction service when implemented
	statusMsg := commands.MicroCompactStatusMsg{
		Enabled:        currentConfig.EnableMicroCompaction,
		RetentionCount: currentConfig.MicroRetentionCount,
		MessagesSaved:  0,  // Will be populated from service
		TokensSaved:    0,  // Will be populated from service
		LastCompaction: "", // Will be populated from service
	}

	a.addNotification("info", statusMsg.FormatStatusMessage())
	return a, nil
}

// ============================================================================
// CODE MODE HANDLERS
// ============================================================================

func (a *App) handleCodeModeToggle(msg commands.CodeModeToggleMsg) (tea.Model, tea.Cmd) {
	currentConfig := a.GetConfig()
	if currentConfig == nil {
		a.addNotification("error", i18n.T("classic_chat.config.load_failed"))
		return a, nil
	}

	newState := !currentConfig.EnableCodeMode
	if !msg.Toggle {
		newState = msg.Enable
	}

	// Update config
	if err := a.UpdateConfig(func(cfg *core.Config) {
		cfg.EnableCodeMode = newState
	}); err != nil {
		a.addNotification("error", i18n.T("classic_chat.config.save_failed", err))
		return a, nil
	}

	// Show notification
	status := i18n.T("classic_chat.common.enabled")
	detail := i18n.T("classic_chat.codemode.tools_sandbox")
	if !newState {
		status = i18n.T("classic_chat.common.disabled")
		detail = i18n.T("classic_chat.codemode.tools_individual")
	}

	// Apply the code mode change live on the in-place tool registry. The same
	// agent picks up the new tool list (and the run_code instruction) on its
	// next request — no agent recreation is needed.
	if a.sdk != nil {
		if err := a.sdk.RecreateAgent(newState); err != nil {
			logDebug("handleCodeModeToggle: failed to apply code mode: %v", err)
			// Report to Sentry so provider-name mismatches and other
			// failures are tracked — they are silent from the user's perspective.
			CaptureSentryError(err, a.currentProvider, a.currentModel, a.currentConvID)
			a.addNotification("warning", i18n.T("classic_chat.codemode.apply_failed", status, err))
		} else {
			a.addNotification("success", i18n.T("classic_chat.codemode.changed_next_message", status, detail))
		}
	} else {
		a.addNotification("success", i18n.T("classic_chat.codemode.changed_next_agent", status, detail))
	}

	// Invalidate sidepanel cache
	a.sidePanelCache.valid = false

	// Trigger SDK reinit so code mode takes effect immediately
	return a, func() tea.Msg {
		return commands.CodeModeUpdatedMsg{
			Enabled: newState,
			Message: i18n.T("classic_chat.codemode.message", status),
		}
	}
}

func (a *App) handleCodeModeStatusRequest() (tea.Model, tea.Cmd) {
	currentConfig := a.GetConfig()
	if currentConfig == nil {
		a.addNotification("error", i18n.T("classic_chat.config.load_failed"))
		return a, nil
	}

	a.addNotification("info", commands.FormatCodeModeStatus(currentConfig.EnableCodeMode))
	return a, nil
}

// ============================================================================
// PASTE HANDLER
// ============================================================================

func (a *App) handlePaste(msg tea.PasteMsg) (tea.Model, tea.Cmd) {
	// Forward paste events - prioritize modals, then active command, then chat input
	logDebug("*** App received PasteMsg: '%s' ***", msg.Content)

	// Forward to approval modal if active
	if a.approvalModal != nil {
		a.approvalModal.HandlePaste(msg.Content)
		return a, nil
	}

	// Forward to question modal if active
	if a.questionModal != nil {
		a.questionModal.HandlePaste(msg.Content)
		return a, nil
	}

	// If there's an active interactive command (like /auth), forward to it
	if a.activeCommand != nil && a.activeCommand.IsInteractive() {
		logDebug("Forwarding PasteMsg to active command")
		updatedCmd, cmd := a.activeCommand.Update(msg)
		a.activeCommand = updatedCmd
		return a, cmd
	}

	// Forward to settings manager if on settings tab (inline within home)
	if a.screen == ScreenHome && a.homeButton == ButtonSettings && a.settingsManager != nil {
		logDebug("Forwarding PasteMsg to settings manager")
		return a, a.settingsManager.HandleMessage(msg, a.cmdRegistry)
	}

	// Forward to home screen input
	if a.screen == ScreenHome {
		logDebug("Forwarding PasteMsg to homeInput")
		if a.homeInput != nil {
			cmd := a.homeInput.Update(msg)
			return a, cmd
		}
		return a, nil
	}

	// Otherwise, handle on chat screen
	if a.screen == ScreenChat {
		// STEP 1: Try to detect clipboard image FIRST (highest priority)
		clipContent, err := readClipboardContent()
		if err == nil && clipContent != nil && clipContent.IsImage {
			// We have an image from clipboard!
			logDebug("✓ Detected clipboard image (%d bytes)", len(clipContent.ImageData))

			// Validate size
			const maxAttachmentSize = 5 * 1024 * 1024 // 5MB
			if int64(len(clipContent.ImageData)) > maxAttachmentSize {
				a.notifications = append(a.notifications, Notification{
					Kind:      "error",
					Text:      i18n.T("classic_chat.image.too_large_decimal", float64(len(clipContent.ImageData))/(1024*1024)),
					CreatedAt: time.Now(),
				})
				return a, nil
			}

			// Check max attachments
			const maxAttachments = 5
			if len(a.currentAttachments) >= maxAttachments {
				a.notifications = append(a.notifications, Notification{
					Kind:      "error",
					Text:      i18n.T("classic_chat.image.limit", maxAttachments),
					CreatedAt: time.Now(),
				})
				return a, nil
			}

			// Add to SimpleInput (creates [Image N] placeholder)
			placeholder := a.textInput.AddImageAttachment(clipContent.ImageData, clipContent.MimeType)
			a.textInput.InsertImagePlaceholder(placeholder)

			// Add to app-level attachments for message sending
			attachment := Attachment{
				FilePath: "", // Empty for clipboard images
				FileName: fmt.Sprintf("clipboard_image_%d.png", a.textInput.imageCounter),
				MimeType: clipContent.MimeType,
				Content:  clipContent.ImageData,
				Size:     int64(len(clipContent.ImageData)),
			}
			a.currentAttachments = append(a.currentAttachments, attachment)

			a.notifications = append(a.notifications, Notification{
				Kind:      "success",
				Text:      i18n.T("classic_chat.image.clipboard_added", attachment.FileName, float64(attachment.Size)/1024),
				CreatedAt: time.Now(),
			})

			logDebug("✓ Added clipboard image as attachment: %s (%d bytes)", attachment.FileName, attachment.Size)
			return a, nil
		}

		// STEP 2: Try to parse as image file path (pasted from file browser)
		path := strings.ReplaceAll(msg.Content, "\\ ", " ")
		path = strings.TrimSpace(path)

		// Check if it's a file path that exists
		if absPath, err := filepath.Abs(path); err == nil {
			if info, err := os.Stat(absPath); err == nil && !info.IsDir() {
				// Check if it's an allowed image type
				ext := strings.ToLower(filepath.Ext(absPath))
				supportedTypes := []string{".jpg", ".jpeg", ".png", ".gif", ".webp"}
				isImage := false
				for _, t := range supportedTypes {
					if ext == t {
						isImage = true
						break
					}
				}

				if isImage {
					const maxAttachmentSize = 5 * 1024 * 1024 // 5MB
					if info.Size() > maxAttachmentSize {
						a.notifications = append(a.notifications, Notification{
							Kind:      "error",
							Text:      i18n.T("classic_chat.image.too_large_integer", info.Size()/(1024*1024)),
							CreatedAt: time.Now(),
						})
						return a, nil
					}

					// Read image content
					content, err := os.ReadFile(absPath)
					if err != nil {
						a.notifications = append(a.notifications, Notification{
							Kind:      "error",
							Text:      i18n.T("classic_chat.image.read_failed", err),
							CreatedAt: time.Now(),
						})
						return a, nil
					}

					// Detect MIME type
					mimeBufferSize := min(512, len(content))
					mimeType := http.DetectContentType(content[:mimeBufferSize])

					// Add attachment
					attachment := Attachment{
						FilePath: absPath,
						FileName: filepath.Base(absPath),
						MimeType: mimeType,
						Content:  content,
						Size:     info.Size(),
					}

					const maxAttachments = 5
					if len(a.currentAttachments) >= maxAttachments {
						a.notifications = append(a.notifications, Notification{
							Kind:      "error",
							Text:      i18n.T("classic_chat.image.limit", maxAttachments),
							CreatedAt: time.Now(),
						})
						return a, nil
					}

					a.currentAttachments = append(a.currentAttachments, attachment)

					a.notifications = append(a.notifications, Notification{
						Kind:      "success",
						Text:      i18n.T("classic_chat.image.added", attachment.FileName, attachment.Size/1024),
						CreatedAt: time.Now(),
					})

					logDebug("Added image attachment: %s (%s, %d bytes)", attachment.FileName, attachment.MimeType, attachment.Size)
					return a, nil
				}
			}
		}

		// STEP 3: Not an image, forward to text input (paste text)
		logDebug("Forwarding PasteMsg to textInput (text paste)")
		cmd := a.textInput.Update(msg)
		return a, cmd
	}
	return a, nil
}

// handleRefreshPreviewsRequest processes a request to regenerate conversation previews
func (a *App) handleRefreshPreviewsRequest(msg commands.RefreshPreviewsRequestMsg) (tea.Model, tea.Cmd) {
	if a.sdk == nil {
		a.addNotification("error", i18n.T("classic_chat.previews.sdk_unavailable"))
		return a, nil
	}

	// Perform refresh in background
	safego.Go("chat.refreshConversationPreviews", func() {
		processed, updated, errors := a.refreshConversationPreviews(context.Background(), msg.Count)
		// Send completion message via runtime
		a.sendToRuntime(commands.RefreshPreviewsCompletedMsg{
			Processed: processed,
			Updated:   updated,
			Errors:    errors,
		})
	})

	a.addNotification("info", i18n.T("classic_chat.previews.started", msg.Count))
	return a, nil
}

// handleRefreshPreviewsCompleted handles completion of preview regeneration
func (a *App) handleRefreshPreviewsCompleted(msg commands.RefreshPreviewsCompletedMsg) (tea.Model, tea.Cmd) {
	// Reload conversations to show new previews
	a.loadConversationsFromSDK()

	if msg.Updated > 0 {
		a.addNotification("success", i18n.T("classic_chat.previews.completed", msg.Updated, msg.Processed, msg.Errors))
	} else if msg.Errors > 0 {
		a.addNotification("warning", i18n.T("classic_chat.previews.none_updated", msg.Processed, msg.Errors))
	} else {
		a.addNotification("info", i18n.T("classic_chat.previews.none_needed"))
	}
	return a, nil
}

// refreshConversationPreviews regenerates preview text for the last N conversations.
// This fixes broken previews that show system text like "new tasks" instead of content.
// Returns the number of conversations processed, updated, and errors encountered.
func (a *App) refreshConversationPreviews(ctx context.Context, count int) (processed, updated, errors int) {
	if a.sdk == nil {
		logDebug("[refreshConversationPreviews] SDK not available")
		return 0, 0, 0
	}

	// List all conversations
	convs, err := a.sdk.ListConversations(ctx, "")
	if err != nil {
		logDebug("[refreshConversationPreviews] Failed to list conversations: %v", err)
		return 0, 0, 1
	}

	// Sort by updated time (most recent first)
	sort.Slice(convs, func(i, j int) bool {
		return convs[i].UpdatedAt.After(convs[j].UpdatedAt)
	})

	processed = 0
	updated = 0
	errors = 0

	for _, conv := range convs {
		if processed >= count {
			break
		}

		// Load the full conversation with messages
		fullConv := a.sdk.GetConversation(ctx, conv.ID)
		if fullConv == nil {
			logDebug("[refreshConversationPreviews] Failed to load conversation %s", conv.ID)
			errors++
			continue
		}

		// Generate new preview from messages
		newPreview := conversation.GenerateConversationPreview(fullConv.Messages)

		// Log what the preview would be (actual preview is computed dynamically in bridge.go)
		if newPreview != "" {
			logDebug("[refreshConversationPreviews] Preview for %s: %q", conv.ID, newPreview)
			updated++
		}

		processed++
	}

	return processed, updated, errors
}
