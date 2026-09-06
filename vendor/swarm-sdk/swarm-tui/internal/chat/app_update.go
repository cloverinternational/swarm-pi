package chat

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/agent"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/observability"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/tools"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/chat/commands"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/chat/settings"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/chat/subagent"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/chatui/components/gitpanel"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/i18n"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/termimage"
	"github.com/charmbracelet/x/ansi"
	xterm "github.com/charmbracelet/x/term"
)

// ============================================================================
// TEA MODEL INTERFACE
// ============================================================================

type runtimeReadyMsg struct {
	generation uint64
	app        *App
}

func buildRuntimeCmd(opts AppOptions, generation uint64) tea.Cmd {
	return func() tea.Msg {
		return runtimeReadyMsg{
			generation: generation,
			app:        newAppWithOptions(opts),
		}
	}
}

func (a *App) Init() tea.Cmd {
	if a.bootstrapPending {
		return tea.Batch(
			func() tea.Msg { return tea.RequestBackgroundColor() },
			a.startIntro(),
			buildRuntimeCmd(a.appOptions, a.bootstrapGeneration),
		)
	}
	// Skip boot animation and menu fade-in — UI is fully visible immediately
	// so the TTE intro animation is not hidden by the opacity gate in viewHome().
	a.bootComplete = true
	a.menuReady = true
	if a.hooksRegistrationPending {
		a.hooksRegistrationPending = false
		go a.registerEnabledHooks()
	}

	// Query terminal size directly as fallback — Bubble Tea v2 may not
	// send WindowSizeMsg before the first View() call with AltScreen=true.
	if w, h, err := xterm.GetSize(os.Stdout.Fd()); err == nil && w > 0 && h > 0 {
		a.width = w
		a.height = h
	} else {
	}
	a.syncSidePanelPreference()
	a.applyChatAreaLayout(true)

	var cmds []tea.Cmd
	if manager := a.appOptions.ImageManager; manager != nil {
		switch manager.Capability() {
		case termimage.Unknown:
			a.imageCapabilityQueryPending = true
			probes, probeErr := manager.ProbeCommands(os.Getenv("TMUX") != "")
			if probeErr != nil {
			}
			// Also request the terminal cell pixel size (CSI 16 t) so images can
			// be sized at their true resolution instead of guessing. Kitty and
			// WezTerm both answer this.
			cmds = append(cmds, tea.Raw(probes+ansi.XTWINOPS(ansi.RequestCellSizeWinOp)+ansi.RequestPrimaryDeviceAttributes))
			cmds = append(cmds, terminalImageQueryTimeout())
		case termimage.Kitty:
			// Native mode forced via env override: still learn the real cell size.
			cmds = append(cmds, tea.Raw(ansi.XTWINOPS(ansi.RequestCellSizeWinOp)))
		}
	}
	// Ask Bubble Tea to query OSC 11 after the event loop has started. This
	// request is asynchronous, so terminals that do not answer cannot delay the
	// first frame.
	cmds = append(cmds, func() tea.Msg { return tea.RequestBackgroundColor() })
	// Initialize settings manager (for microphone discovery)
	if cmd := a.settingsManager.Init(); cmd != nil {
		cmds = append(cmds, cmd)
	}
	// Start voice event listener if voice is enabled.
	// listenForVoiceEvent returns a tea.Cmd that blocks on the channel and
	// delivers each event directly to Update(), bypassing updateQueue.
	// Each voice handler re-chains the listener to receive subsequent events.
	if a.voiceEnabled && a.voiceEventCh != nil {
		cmds = append(cmds, listenForVoiceEvent(a.voiceEventCh))
	}
	// Start background update check if updater is configured.
	// This checks GitHub Releases for a newer version and notifies the user.
	if a.updater != nil && a.updater.ShouldCheck() {
		cmds = append(cmds, a.checkForUpdateCmd())
	}
	// The reveal lives in the empty chat viewport. Advanced/home startup has its
	// own title animation, so do not run an invisible permanent tick loop there.
	if a.screen == ScreenChat {
		if !a.introFromBootstrap {
			cmds = append(cmds, a.startIntro())
		}
	} else {
		a.introComplete = true
		a.introGlowing = false
		a.introFromBootstrap = false
	}
	// Keep every providers.json writer in one background sequence so startup
	// configuration reads cannot contend with model catalog writes.
	cmds = append(cmds, tea.Sequence(
		refreshOpenRouterAndCapabilitiesCmd(),
		a.refreshAnthropicModelsCmd(),
		a.refreshCodexModelsCmd(),
	))
	return tea.Batch(cmds...)
}

func refreshOpenRouterAndCapabilitiesCmd() tea.Cmd {
	return func() tea.Msg {
		if _, err := RefreshOpenRouterModels(); err != nil {
			logDebug("[OpenRouter] startup model refresh failed: %v", err)
		}
		RefreshModelCapabilities()
		return nil
	}
}

func (a *App) registerEnabledHooks() {
	if a.sdk == nil || a.sdk.hooksManager == nil || a.hooksConfig == nil {
		return
	}

	if !a.sdk.HasPermission(tools.PermissionHookManage) {
		logDebug("Skipping custom hook registration: hook permissions are denied")
		return
	}

	enabledHooks := a.hooksConfig.GetEnabledHooks()
	logDebug("Found %d enabled custom hooks to register", len(enabledHooks))
	for _, hookConfig := range enabledHooks {
		shellHook, err := hookConfig.ToShellHook()
		if err != nil {
			logDebug("Failed to create shell hook '%s': %v", hookConfig.Name, err)
			continue
		}
		if err := a.sdk.hooksManager.RegisterCustomHook(shellHook); err != nil {
			logDebug("Failed to register hook '%s': %v", hookConfig.Name, err)
		} else {
			logDebug("Registered custom hook: %s (events: %v)", hookConfig.Name, hookConfig.EventPatterns)
		}
	}
}

type bootTickMsg struct{}
type menuTickMsg struct{}
type introTickMsg struct{}
type glowTickMsg struct{}      // drives the low-frequency settled-logo breathing state
type homeTitleTickMsg struct{} // drives the home-screen title burn animation

// bgProcessTickMsg fires every 1s while background processes are running to keep
// the side panel timer updated even when the agent is idle.
type bgProcessTickMsg struct{}

// attachMonitorTickMsg keeps the full-screen attach view connected to the
// live-session frame channel. LivePeerSession polls independently, so without
// an app event the frames would remain queued until the user pressed another
// key and the screen would appear blank/stale.
type attachMonitorTickMsg struct{}

const attachMonitorRefreshInterval = 75 * time.Millisecond

// voiceTickMsg fires every 100ms while voice is recording to animate the pulsing dot.
type voiceTickMsg struct{}

// voiceAutoRecordMsg fires after a delay to auto-restart recording.
type voiceAutoRecordMsg struct{}

// drainQueueMsg signals to continue draining the queue (for streaming real-time updates)
type drainQueueMsg struct{}

// updateQueueDrainBatchLimit caps the number of queue messages processed per
// Update() call.  When the limit is reached the remaining messages are left in
// the channel and a drainQueueMsg is scheduled so the event loop yields to
// bubbletea for a render pass before draining more.
const updateQueueDrainBatchLimit = 50

// bgOutputPreviewMaxBytes is the size cap for output included inline in the
// bgProcessDoneMsg notification. Values above this get truncated with head+tail
// slicing; the agent is told to call ReadBackgroundCommand for the full output.
const bgOutputPreviewMaxBytes = 4096

// bgOutputPreview returns a preview of output bounded by bgOutputPreviewMaxBytes
// along with a flag indicating whether the original was larger. Large outputs
// are sliced head+tail so the agent sees both the start and the tail (where
// error messages and final results typically live) rather than just the head.
func bgOutputPreview(output []byte) (string, bool) {
	if len(output) == 0 {
		return "", false
	}
	if len(output) <= bgOutputPreviewMaxBytes {
		return string(output), false
	}
	half := bgOutputPreviewMaxBytes / 2
	head := string(output[:half])
	tail := string(output[len(output)-half:])
	return head + "\n\n... [truncated " + fmt.Sprintf("%d", len(output)-bgOutputPreviewMaxBytes) + " bytes] ...\n\n" + tail, true
}

// bgProcessDoneMsg is sent when a background bash process reaches a terminal state
// (completed, failed, or cancelled).  The TUI injects a system message so the agent
// can react to the completion without the user having to prompt it.
//
// outputPreview carries a size-capped slice of the captured output so the
// agent can react to the result without a separate ReadBackgroundCommand
// round-trip. Surveys showed agents frequently skipped that follow-up,
// orphaning useful output. outputTruncated indicates the full output was
// larger than the preview cap and the agent should call ReadBackgroundCommand
// to fetch the rest.
type bgProcessDoneMsg struct {
	processID       string
	command         string
	state           string // "completed", "failed", "cancelled"
	exitCode        int
	outputPreview   string
	outputTruncated bool
}

// bgAgentDoneMsg is sent when a background sub-agent reaches a terminal state.
//
// resultPreview carries the subagent's final assistant message text (size-
// capped) so the parent agent can react without a separate TaskOutput
// round-trip. Surveys showed parent agents frequently skipped the TaskOutput
// follow-up, orphaning the subagent's conclusions. Same pattern as
// bgProcessDoneMsg.outputPreview.
type bgAgentDoneMsg struct {
	agentID         string
	task            string
	status          string // "completed", "failed", "cancelled"
	outputFilePath  string // path to the NDJSON output file (may be empty)
	errorMessage    string // non-empty on status=="failed"
	resultPreview   string
	resultTruncated bool
}

func localizedBackgroundState(state string) string {
	switch strings.ToLower(strings.TrimSpace(state)) {
	case "completed":
		return i18n.T("residual.update.background_completed")
	case "failed":
		return i18n.T("residual.update.background_failed")
	case "cancelled", "canceled":
		return i18n.T("residual.update.background_cancelled")
	default:
		return state
	}
}

// injectedUserMsg is sent from the message injector callback (running on the
// agent goroutine) to update the TUI display with user messages that were
// injected into the conversation between agent turns.
type injectedUserMsg struct {
	content string
}

// HooksChatResponseMsg signals that the hooks assistant has responded
type HooksChatResponseMsg struct{}

// AgentsChatResponseMsg signals that the agents assistant has responded
type AgentsChatResponseMsg struct{}
type MCPChatResponseMsg struct{}

func (a *App) updateBootstrap(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case runtimeReadyMsg:
		if msg.generation != a.bootstrapGeneration || msg.app == nil {
			return a, nil
		}

		program := a.program
		modelPromotedCallback := a.modelPromotedCallback
		runtimeReadyCallback := a.runtimeReadyCallback
		width, height := a.width, a.height
		hasDark := a.hasDarkTerminal
		notifications := append([]Notification(nil), a.bootstrapNotifications...)
		introAnim := a.introAnim
		introFrame := a.introFrame
		introDoneHold := a.introDoneHold
		introEffectIdx := a.introEffectIdx
		introGlowing := a.introGlowing
		introGlowPhase := a.introGlowPhase

		next := msg.app
		// Constructor-installed async callbacks close over next. Promote that
		// pointer to the live Bubble Tea model instead of copying App: App owns
		// mutexes whose state must never be copied after first use.
		next.program = program
		next.modelPromotedCallback = modelPromotedCallback
		next.runtimeReadyCallback = runtimeReadyCallback
		next.bootstrapPending = false
		if width > 0 {
			next.width = width
		}
		if height > 0 {
			next.height = height
		}
		if len(notifications) > 0 {
			next.notifications = append(next.notifications, notifications...)
		}
		next.introAnim = introAnim
		next.introFrame = introFrame
		next.introDoneHold = introDoneHold
		next.introEffectIdx = introEffectIdx
		next.introGlowing = introGlowing
		next.introGlowPhase = introGlowPhase
		next.introComplete = false
		next.introFromBootstrap = true
		next.syncSidePanelPreference()
		next.applyChatAreaLayout(true)
		if !hasDark {
			next.applyTerminalBackground(false)
		}
		if next.permissionBroker != nil {
			next.permissionBroker.SetDispatcher(next.sendToRuntime)
		}
		if next.questionBroker != nil {
			next.questionBroker.SetDispatcher(next.sendToRuntime)
		}
		if next.planBroker != nil {
			next.planBroker.SetDispatcher(next.sendToRuntime)
		}
		if next.interactionBrkr != nil {
			next.interactionBrkr.SetDispatcher(next.sendToRuntime)
		}
		if modelPromotedCallback != nil {
			modelPromotedCallback(next)
		}
		initCmd := next.Init()
		initialPromptCmd := next.initialPromptCmd()
		if runtimeReadyCallback == nil {
			if initialPromptCmd == nil {
				return next, initCmd
			}
			return next, tea.Batch(initCmd, initialPromptCmd)
		}
		callbackCmd := func() tea.Msg {
			runtimeReadyCallback()
			return nil
		}
		if initialPromptCmd == nil {
			return next, tea.Batch(initCmd, callbackCmd)
		}
		return next, tea.Batch(initCmd, initialPromptCmd, callbackCmd)

	case introTickMsg:
		return a.handleIntroTick()

	case glowTickMsg:
		return a.handleGlowTick()

	case tea.WindowSizeMsg:
		if msg.Width > 0 {
			a.width = msg.Width
		}
		if msg.Height > 0 {
			a.height = msg.Height
		}
		return a, nil

	case tea.BackgroundColorMsg:
		a.applyTerminalBackground(msg.IsDark())
		return a, nil

	case notificationMsg:
		n := Notification{Kind: msg.level, Text: msg.message, CreatedAt: time.Now()}
		a.bootstrapNotifications = append(a.bootstrapNotifications, n)
		return a, nil

	case tea.KeyMsg:
		if msg.String() == "ctrl+c" {
			return a, tea.Quit
		}
		return a, nil

	case tea.PasteMsg:
		return a, nil
	}
	return a, nil
}

func (a *App) Update(msg tea.Msg) (retModel tea.Model, retCmd tea.Cmd) {
	a.observeTerminalImageCapability(msg)
	a.observeTerminalCellSize(msg)
	if a.bootstrapPending {
		return a.updateBootstrap(msg)
	}
	// SAFETY NET: several paths flip spinner/loading state on (Spinner.Start,
	// LoadingIndicator.Start) from handlers that historically dropped the
	// Subscribe() command. If any animation is subscribed, guarantee a live
	// AnimationTickMsg chain. scheduleTick dedupes, so this cannot stack
	// parallel tick chains.
	defer func() {
		if a.animationClock == nil || !a.animationClock.IsActive() {
			return
		}
		// TickIfIdle, not Tick: Tick() is never nil while IsActive(), so it
		// would Batch on EVERY message. Each Batch costs a waiter goroutine
		// plus N workers blocked on the unbuffered msg channel.
		if tick := a.animationClock.TickIfIdle(); tick != nil {
			if retCmd == nil {
				retCmd = tick
			} else {
				retCmd = tea.Batch(retCmd, tick)
			}
		}
	}()

	// SAFETY NET: if View() skipped a frame to stay inside the frame budget,
	// make sure one more render happens after the burst ends. Guarded by a CAS
	// so at most one trailing tick is ever outstanding.
	defer func() {
		if a == nil || a.quitting {
			return
		}
		if flush := a.scheduleTrailingFrame(); flush != nil {
			if retCmd == nil {
				retCmd = flush
			} else {
				retCmd = tea.Batch(retCmd, flush)
			}
		}
	}()
	// SAFETY NET (registered BEFORE the drain loop so it runs on EVERY return
	// path, including the many early `return a, …` cases inside the loop): if
	// any message remains in updateQueue when Update() returns, schedule another
	// drainQueueMsg. Several drain-loop cases (streaming content/thinking/tool
	// results, agentResponse, automationImage, …) return early to let View()
	// render mid-stream; without this net the messages queued behind them —
	// including critical permission/question brokers and cronPromptInjectMsg —
	// would sit unprocessed until the next
	// Bubble Tea event (a keypress or focus click). This makes stranding
	// structurally impossible: the queue is always followed by a drain. It also
	// subsumes the old batch-limit continuation. drainQueueMsg is idempotent and
	// self-terminating (it stops rescheduling once the queue is empty).
	defer func() {
		if a == nil || a.quitting {
			return
		}
		if len(a.updateQueue) > 0 {
			retCmd = tea.Batch(retCmd, func() tea.Msg { return drainQueueMsg{} })
		}
	}()
	// Drain queued agent updates in a single Update() call for efficient rendering.
	// A batch limit prevents the drain from starving the bubbletea render loop when
	// many messages arrive in rapid succession (e.g. during streaming).
	drainCount := 0
	// cronWakePending records that a scheduled prompt (cron/wakeup/goal) was
	// drained while the agent was idle. We must NOT wake mid-drain via an early
	// return (that strands any other queued messages until the next Bubble Tea
	// event — a focus click); instead we wake once, after the drain, on the
	// normal return path. See the post-loop handling below.
	cronWakePending := false
queueDrain:
	for {
		// Yield after processing updateQueueDrainBatchLimit messages so bubbletea
		// can perform a render pass; a drainQueueMsg is returned below to continue.
		if drainCount >= updateQueueDrainBatchLimit {
			break queueDrain
		}
		select {
		case queuedMsg := <-a.updateQueue:
			drainCount++
			if a.sdk != nil && a.sdk.logger != nil {
				msgType := fmt.Sprintf("%T", queuedMsg)
				if strings.Contains(msgType, "a2a") {
					a.sdk.logger.Info(context.Background(), "tui.a2a.queue_drained",
						observability.F("message_type", msgType),
						observability.F("queue_len", len(a.updateQueue)),
						observability.F("queue_cap", cap(a.updateQueue)),
					)
				}
			}
			// Process queued message and continue to next
			switch qm := queuedMsg.(type) {
			case permissionApprovalMsg:
				a.enqueueApprovalRequest(qm.request)

			case permissionApprovalResolvedMsg:
				a.removeApprovalRequest(qm.requestID, qm.outcome)

			case questionRequestMsg:
				a.enqueueQuestionRequest(qm.request)

			case questionResolvedMsg:
				a.removeQuestionRequest(qm.requestID, qm.timedOut)

			case vaultUnlockRequestMsg:
				a.enqueueVaultUnlockRequest(qm.request)

			case vaultUnlockResolvedMsg:
				a.removeVaultUnlockRequest(qm.requestID)

			case notificationMsg:
				a.addNotification(qm.level, qm.message)

			case automationImageMsg:
				if cmd := a.handleAutomationImageMessage(qm); cmd != nil {
					return a, cmd
				}

			case a2aFocusConversationMsg:
				if qm.ConversationID == "" {
					break
				}
				if a.sdk != nil && a.sdk.logger != nil {
					a.sdk.logger.Info(context.Background(), "tui.a2a.focus_conversation",
						observability.F("conversation_id", qm.ConversationID),
					)
				}
				if !a.openConversationByID(qm.ConversationID) {
					if a.sdk != nil && a.sdk.logger != nil {
						a.sdk.logger.Warn(context.Background(), "tui.a2a.focus_conversation_failed",
							observability.F("conversation_id", qm.ConversationID),
						)
					}
					a.addNotification("warning", i18n.T("residual.update.a2a_open_failed"))
				} else if a.sdk != nil && a.sdk.logger != nil {
					a.sdk.logger.Info(context.Background(), "tui.a2a.focus_conversation_opened",
						observability.F("conversation_id", qm.ConversationID),
					)
				}

			case a2aPeerMessageMsg:
				if qm.Message == nil {
					break
				}
				if a.visibleA2AConversation(qm.ConversationID) && !a.streamingMessage {
					if a.appendPeerMessageIfMissing(qm.Message) {
						wasAtBottom := a.msgViewport.AtBottom()
						a.invalidateViewportCache()
						a.updateViewportContent()
						if wasAtBottom {
							a.msgViewport.GotoBottom()
						}
					}
					break
				}
				if qm.AutoFocus {
					a.addNotification("info", formatA2ANotification(qm.PeerHandle, i18n.T("residual.update.a2a_opened_chat")))
				}

			case a2aInboundStartedMsg:
				if !a.visibleA2AConversation(qm.ConversationID) {
					a.addNotification("info", formatA2ANotification(qm.PeerHandle, i18n.T("residual.update.a2a_started_request")))
					break
				}
				if a.streamingMessage {
					a.addNotification("info", formatA2ANotification(qm.PeerHandle, i18n.T("residual.update.a2a_background")))
					break
				}
				a.a2aInboundConvID = qm.ConversationID
				a.streamingMessage = true
				a.streamingInProgress = false
				a.animationClock.SetStreaming(true)
				a.setActivityPhase(ActivityPhasePeer, i18n.T("residual.update.peer_request_progress"))
				// Tick chain is guaranteed by the animation safety net in Update.
				a.loadingIndicator.Start(a.animationClock)
				a.spinner.Start(a.animationClock)
				a.ensureLastMessageIsAssistant()
				a.invalidateViewportCache()
				a.updateViewportContent()

			case a2aInboundUpdateMsg:
				if qm.Update == nil || qm.ConversationID == "" {
					break
				}
				if qm.ConversationID != a.a2aInboundConvID || !a.visibleA2AConversation(qm.ConversationID) {
					break
				}
				a.queueAgentIntermediateUpdateWithContext(a2aExecutionSourceInbound, qm.ConversationID, qm.Update)

			case a2aInboundFinishedMsg:
				trackedInbound := qm.ConversationID != "" && qm.ConversationID == a.a2aInboundConvID
				visibleConversation := a.visibleA2AConversation(qm.ConversationID)
				if qm.ConversationID == "" || (!trackedInbound && !visibleConversation) {
					if qm.Err != nil {
						a.addNotification("warning", formatA2ANotification(qm.PeerHandle, i18n.T("residual.update.failed")))
					} else {
						a.addNotification("info", formatA2ANotification(qm.PeerHandle, i18n.T("residual.update.completed")))
					}
					break
				}
				if trackedInbound {
					a.a2aInboundConvID = ""
					a.streamingMessage = false
					a.streamingInProgress = false
					a.clearToolActivities()
					a.animationClock.SetStreaming(false)
					a.loadingIndicator.Stop(a.animationClock)
					a.spinner.Stop(a.animationClock)
					a.setActivityPhase(ActivityPhaseIdle, "")
				}
				if !visibleConversation {
					if qm.Err != nil {
						a.addNotification("warning", formatA2ANotification(qm.PeerHandle, i18n.T("residual.update.failed")))
					} else {
						a.addNotification("info", formatA2ANotification(qm.PeerHandle, i18n.T("residual.update.completed")))
					}
					break
				}
				if !a.syncConversationFromSDK(qm.ConversationID) {
					if len(a.messages) > 0 && a.messages[len(a.messages)-1].Role == "assistant" {
						lastMsg := &a.messages[len(a.messages)-1]
						lastMsg.IsComplete = true
						lastMsg.Model = a.currentModel
						if qm.Err != nil && strings.TrimSpace(lastMsg.GetContent()) == "" {
							lastMsg.Content = i18n.T("classic_chat.stream.error", qm.Err)
						}
					}
					_ = a.syncPeerMessagesFromSDK(qm.ConversationID)
					a.invalidateViewportCache()
					a.updateViewportContent()
				}
				if qm.Err != nil {
					a.addNotification("warning", formatA2ANotification(qm.PeerHandle, i18n.T("residual.update.failed")))
				} else {
					a.addNotification("info", formatA2ANotification(qm.PeerHandle, i18n.T("residual.update.completed")))
				}

				// REMOVED: agentTokenEstimateMsg case
				// We now only use REAL token counts from API responses
				// See: TOKEN_COUNT_FIX_PLAN.md

			case agentToolCallMsg:
				if !a.shouldProcessA2AQueuedUpdate(qm.source, qm.conversationID) {
					break
				}
				logDebug("[Queue] Processing tool call: %s [seq=%d]", qm.toolName, qm.sequence)
				// Track tool activity for status display.
				a.beginToolActivity(qm.callID, qm.toolName, qm.parameters)
				a.ensureLastMessageIsAssistant()
				if len(a.messages) > 0 && a.messages[len(a.messages)-1].Role == "assistant" {
					lastMsg := &a.messages[len(a.messages)-1]
					toolCall := ToolCallDisplay{
						ID:         qm.callID,
						Name:       qm.toolName,
						Parameters: qm.parameters,
					}
					lastMsg.ToolCalls = append(lastMsg.ToolCalls, toolCall)
					// Add to ordered blocks for proper interleaved rendering
					lastMsg.OrderedBlocks = append(lastMsg.OrderedBlocks, MessageBlock{
						Type:     "tool_call",
						ToolCall: &toolCall,
						Sequence: qm.sequence, // Use sequence from queue message
					})
					// CRITICAL: Invalidate cache so the new tool_call block is rendered.
					// Without this, updateViewportIncremental() uses stale pre-rendered
					// content and the block is invisible until a full re-render fires.
					a.invalidateLastMessageCache()
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
				// Return immediately to allow View() to render this update
				// Schedule another queue drain to process remaining messages

			case agentToolResultMsg:
				if !a.shouldProcessA2AQueuedUpdate(qm.source, qm.conversationID) {
					break
				}
				logDebug("[Queue] Processing tool result: %s [seq=%d]", qm.callID, qm.sequence)
				// Remove completed tool from activity tracking.
				a.endToolActivity(qm.callID)
				a.ensureLastMessageIsAssistant()
				if len(a.messages) > 0 && a.messages[len(a.messages)-1].Role == "assistant" {
					lastMsg := &a.messages[len(a.messages)-1]
					errStr := ""
					if qm.err != nil {
						errStr = qm.err.Error()
					}

					// Find the tool name and parameters from the corresponding tool_call
					var toolName string
					var toolParams map[string]any
					for _, block := range lastMsg.OrderedBlocks {
						if block.Type == "tool_call" && block.ToolCall != nil {
							if block.ToolCall.ID == qm.callID {
								toolName = block.ToolCall.Name
								toolParams = block.ToolCall.Parameters
								break
							}
						}
					}

					// Check if streaming already created a result for this callID
					existingFound := false
					for i := len(lastMsg.OrderedBlocks) - 1; i >= 0; i-- {
						block := &lastMsg.OrderedBlocks[i]
						if block.Type == "tool_result" && block.ToolResult != nil &&
							block.ToolResult.CallID == qm.callID {
							// Update existing streaming result with final output
							// Extract images/attachments from final output text
							var attachments []Attachment
							cleanOutput := qm.output
							if a.toolResultParser != nil && qm.err == nil {
								attachments, cleanOutput = a.toolResultParser.ParseOutput(qm.output)
								if len(attachments) > 0 {
									logDebug("[Queue] Extracted %d attachment(s) from final tool output (block update)", len(attachments))
								}
							}

							// Also extract images from content blocks (e.g., from Read tool)
							if len(qm.contentBlocks) > 0 && qm.err == nil {
								imageAtts := extractImagesFromContentBlocks(qm.contentBlocks)
								if len(imageAtts) > 0 {
									attachments = append(attachments, imageAtts...)
									logDebug("[Queue] Extracted %d image(s) from content blocks (block update)", len(imageAtts))
								}
							}

							block.ToolResult.SetOutput(cleanOutput) // clears streaming builder
							block.ToolResult.Attachments = attachments
							block.ToolResult.Error = errStr
							block.ToolResult.ToolName = toolName
							block.ToolResult.IsStreaming = false // Final result arrived
							block.ToolResult.Metadata = qm.metadata
							// CRITICAL: Update the block's Sequence to the SDK-assigned sequence from the
							// final ToolResultUpdate. The block was created from a streaming chunk which
							// used a TUI-local sequence counter (MUCH higher than SDK sequences). Without
							// this overwrite, the block would sort after all subsequent hooks/tool_calls.
							block.Sequence = qm.sequence
							existingFound = true
							logDebug("[Queue] Updated existing streaming result for %s", qm.callID)
							// Sync the corresponding top-level ToolResults entry so both
							// views (OrderedBlocks and ToolResults) stay consistent.
							for j := range lastMsg.ToolResults {
								if lastMsg.ToolResults[j].CallID == qm.callID {
									lastMsg.ToolResults[j].SetOutput(cleanOutput)
									lastMsg.ToolResults[j].Attachments = attachments
									lastMsg.ToolResults[j].Error = errStr
									lastMsg.ToolResults[j].ToolName = toolName
									lastMsg.ToolResults[j].IsStreaming = false
									lastMsg.ToolResults[j].Metadata = qm.metadata
									break
								}
							}
							break
						}
					}

					if !existingFound {
						// No streaming result exists, create new one

						// Extract images/attachments from tool output text
						var attachments []Attachment
						cleanOutput := qm.output
						if a.toolResultParser != nil && qm.err == nil {
							attachments, cleanOutput = a.toolResultParser.ParseOutput(qm.output)
							if len(attachments) > 0 {
								logDebug("[ToolResult] Extracted %d attachment(s) from tool output text", len(attachments))
							}
						}

						// Also extract images from content blocks (e.g., from Read tool)
						if len(qm.contentBlocks) > 0 && qm.err == nil {
							imageAtts := extractImagesFromContentBlocks(qm.contentBlocks)
							if len(imageAtts) > 0 {
								attachments = append(attachments, imageAtts...)
								logDebug("[ToolResult] Extracted %d image(s) from content blocks", len(imageAtts))
							}
						}

						toolResult := ToolResultDisplay{
							CallID:      qm.callID,
							Output:      cleanOutput, // Use cleaned output (with placeholders)
							Error:       errStr,
							ToolName:    toolName,
							Attachments: attachments,
							Metadata:    qm.metadata,
						}
						lastMsg.ToolResults = append(lastMsg.ToolResults, toolResult)
						newTRBlock := MessageBlock{
							Type:       "tool_result",
							ToolResult: &toolResult,
							Sequence:   qm.sequence, // Use sequence from queue message
						}
						lastMsg.OrderedBlocks = append(lastMsg.OrderedBlocks, newTRBlock)
					}

					// Pre-process tool result for efficient rendering (if no error)
					if qm.err == nil && a.toolRegistry != nil {
						a.processToolResultForRendering(lastMsg, qm.callID, toolName, toolParams, qm.output, qm.metadata)
					}

					// ASSERT: Check for parallel sub-agent merge bug (Task tools).
					// Skip when the tool errored — a failed Task never creates sub-agent blocks.
					if toolName == "Task" && qm.err == nil {
						assertParallelSubAgentsNotMerged(lastMsg)
					}

					// CRITICAL: Invalidate cache so the new/updated tool_result block is rendered.
					// Without this, updateViewportIncremental() uses stale pre-rendered
					// content and the block is invisible until a full re-render fires.
					a.invalidateLastMessageCache()
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
				// Start bg process tick chain if a command was just backgrounded
				if !a.bgProcessTickActive && a.sdk != nil && a.sdk.GetActiveBackgroundProcessCount() > 0 {
					a.bgProcessTickActive = true
					return a, tea.Batch(
						func() tea.Msg { return drainQueueMsg{} },
						tea.Tick(time.Second, func(t time.Time) tea.Msg { return bgProcessTickMsg{} }),
					)
				}
				// Return immediately to allow View() to render this update

			case agentHookExecutionMsg:
				if !a.shouldProcessA2AQueuedUpdate(qm.source, qm.conversationID) {
					break
				}
				logDebug("[Queue] Processing hook execution: %s (%s) for %s [seq=%d]", qm.hookName, qm.phase, qm.toolName, qm.sequence)
				a.ensureLastMessageIsAssistant()
				if len(a.messages) > 0 && a.messages[len(a.messages)-1].Role == "assistant" {
					lastMsg := &a.messages[len(a.messages)-1]
					hookExec := HookExecutionDisplay{
						HookName:   qm.hookName,
						ToolName:   qm.toolName,
						ToolCallID: qm.toolCallID,
						Phase:      qm.phase,
						Success:    qm.success,
						Output:     qm.output,
						Blocked:    qm.blocked,
						Error:      qm.errMsg,
					}
					// Add to ordered blocks for rendering
					lastMsg.OrderedBlocks = append(lastMsg.OrderedBlocks, MessageBlock{
						Type:          "hook_execution",
						HookExecution: &hookExec,
						Sequence:      qm.sequence, // Use sequence from queue message
					})
					// CRITICAL: Invalidate cache so the new hook_execution block is rendered.
					// Without this, updateViewportIncremental() uses stale pre-rendered
					// content and the block is invisible until a full re-render fires.
					a.invalidateLastMessageCache()
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
				// Return immediately to allow View() to render this update

			case agentToolOutputChunkMsg:
				if !a.shouldProcessA2AQueuedUpdate(qm.source, qm.conversationID) {
					break
				}
				// Incremental tool output (streaming)
				logDebug("[Queue] Processing tool output chunk: callID=%s len=%d [seq=%d]", qm.callID, len(qm.chunk), qm.sequence)
				a.ensureLastMessageIsAssistant()
				if len(a.messages) > 0 && a.messages[len(a.messages)-1].Role == "assistant" {
					lastMsg := &a.messages[len(a.messages)-1]
					// Find existing tool_result block for this callID
					found := false
					for i := len(lastMsg.OrderedBlocks) - 1; i >= 0; i-- {
						block := &lastMsg.OrderedBlocks[i]
						if block.Type == "tool_result" && block.ToolResult != nil &&
							block.ToolResult.CallID == qm.callID {
							// Append chunk to existing result using efficient builder
							block.ToolResult.AppendOutput(qm.chunk)
							found = true
							break
						}
					}
					if !found {
						// Create new streaming result block (IsStreaming=true until final result arrives)
						// Use the matching tool_call's Sequence + 1 as a placeholder sequence so the
						// block sorts near its tool_call. When the final ToolResultUpdate arrives via
						// agentToolResultMsg, it will overwrite this sequence with the SDK-assigned one.
						placeholderSeq := qm.sequence
						for _, b := range lastMsg.OrderedBlocks {
							if b.Type == "tool_call" && b.ToolCall != nil && b.ToolCall.ID == qm.callID {
								placeholderSeq = b.Sequence + 1
								break
							}
						}
						lastMsg.ToolResults = append(lastMsg.ToolResults, ToolResultDisplay{
							CallID:      qm.callID,
							Output:      qm.chunk,
							IsStreaming: true,
						})
						lastMsg.OrderedBlocks = append(lastMsg.OrderedBlocks, MessageBlock{
							Type: "tool_result",
							ToolResult: &ToolResultDisplay{
								CallID:      qm.callID,
								Output:      qm.chunk,
								IsStreaming: true,
							},
							Sequence: placeholderSeq, // near tool_call until final result overwrites
						})
					}
					// CRITICAL: Invalidate cache since we modified tool output content
					a.invalidateLastMessageCache() // Only invalidate last message for streaming
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
				// CRITICAL: Return immediately to let View() render this chunk
				// Schedule another queue drain to process remaining messages

			case tokenUpdateMsg:
				if !a.shouldProcessA2AQueuedUpdate(qm.source, qm.conversationID) {
					break
				}
				// Real-time token count from the SDK — update side panel immediately.
				// This case MUST live in the queueDrain switch (not only in the outer
				// Update switch) because tokenUpdateMsg is dispatched via sendToRuntime
				// → updateQueue. Messages in the queue that have no matching case here
				// are silently dropped before the outer switch is ever reached.
				logDebug("[Queue] Processing tokenUpdateMsg: input=%d output=%d window=%d pct=%.1f%%",
					qm.inputTokens, qm.outputTokens, qm.contextWindow, qm.pctUsed)
				if qm.inputTokens > 0 {
					a.tokenCount = qm.inputTokens
					a.tokenCountIsEstimate = false
					a.lastRealTokenCount = qm.inputTokens
					a.sidePanelCache.valid = false
				}
				if qm.contextWindow > 0 && qm.contextWindow != a.modelContextWindow {
					a.modelContextWindow = qm.contextWindow
					a.sidePanelCache.valid = false
				}
				if qm.autoCompactThreshold > 0 && qm.autoCompactThreshold != a.autoCompactThresholdTokens {
					a.autoCompactThresholdTokens = qm.autoCompactThreshold
					a.sidePanelCache.valid = false
				}

			case agentAutoCompactionStartedMsg:
				if !a.shouldProcessA2AQueuedUpdate(qm.source, qm.conversationID) {
					break
				}
				a.handleAutoCompactionStarted(qm)

			case agentAutoCompactionDoneMsg:
				if !a.shouldProcessA2AQueuedUpdate(qm.source, qm.conversationID) {
					break
				}
				a.handleAutoCompactionDone(qm)

			case agentAutoCompactionFailedMsg:
				if !a.shouldProcessA2AQueuedUpdate(qm.source, qm.conversationID) {
					break
				}
				a.handleAutoCompactionFailed(qm)

			case agentSubAgentUpdateMsg:
				if !a.shouldProcessA2AQueuedUpdate(qm.source, qm.conversationID) {
					break
				}
				logDebug("[Queue] Processing sub-agent update: agentID='%s' agentName='%s' type=%s [seq=%d]", qm.agentID, qm.agentName, qm.update.UpdateType(), qm.sequence)
				a.ensureLastMessageIsAssistant()
				if len(a.messages) > 0 && a.messages[len(a.messages)-1].Role == "assistant" {
					lastMsg := &a.messages[len(a.messages)-1]

					// Find or create SubAgentDisplay block.
					// Search ALL blocks (not just the last) so parallel sub-agents each keep
					// their own block when updates interleave. Use AgentID as the primary key
					// when available, falling back to AgentName for backward compatibility.
					var subAgentBlock *SubAgentDisplay
					for i := range lastMsg.OrderedBlocks {
						block := &lastMsg.OrderedBlocks[i]
						if block.Type == "sub_agent_activity" && block.SubAgentActivity != nil {
							sa := block.SubAgentActivity
							if qm.agentID != "" && sa.AgentID == qm.agentID {
								logDebug("[Match] ✓ Matched by AgentID: incoming='%s' == existing='%s'", qm.agentID, sa.AgentID)
								subAgentBlock = sa
								break
							}
							// Fallback: name-only match when AgentID is absent (legacy)
							if qm.agentID == "" && sa.AgentID == "" && sa.AgentName == qm.agentName {
								logDebug("[Match] ✗ FALLBACK to name-only match: agentName='%s' (AgentID was empty!)", qm.agentName)
								subAgentBlock = sa
								break
							}
						}
					}

					if subAgentBlock == nil {
						logDebug("[Create] Creating NEW sub-agent block: agentID='%s' agentName='%s'", qm.agentID, qm.agentName)
						// Collect Subagent (sometimes named "Task" in legacy code paths)
						// tool-call instructions in document order so the Nth new sub-agent
						// block maps to the Nth Subagent tool call (parallel-safe assignment).
						var taskInstructions []string
						for _, b := range lastMsg.OrderedBlocks {
							if b.Type == "tool_call" && b.ToolCall != nil &&
								(b.ToolCall.Name == "Subagent" || b.ToolCall.Name == "Task") {
								if taskParam, ok := b.ToolCall.Parameters["task"].(string); ok {
									taskInstructions = append(taskInstructions, taskParam)
								}
							}
						}
						existingSubAgentCount := 0
						for _, b := range lastMsg.OrderedBlocks {
							if b.Type == "sub_agent_activity" {
								existingSubAgentCount++
							}
						}
						taskInstruction := ""
						if existingSubAgentCount < len(taskInstructions) {
							taskInstruction = taskInstructions[existingSubAgentCount]
						}

						subAgentBlock = &SubAgentDisplay{
							AgentID:         qm.agentID,
							AgentName:       qm.agentName,
							TaskInstruction: taskInstruction,
							Blocks:          make([]MessageBlock, 0, 16),
							StartTime:       time.Now(),
							SpinnerVerb:     subagent.SampleSpinnerVerb(qm.agentName),
							CompletionVerb:  subagent.SampleCompletionVerb(qm.agentName),
						}
						lastMsg.OrderedBlocks = append(lastMsg.OrderedBlocks, MessageBlock{
							Type:             "sub_agent_activity",
							SubAgentActivity: subAgentBlock,
							Sequence:         qm.sequence, // Use sequence from queue message
						})
						logDebug("[Queue] Created new sub_agent_activity block for agent=%s (id=%s) totalBlocks=%d task=%s", qm.agentName, qm.agentID, len(lastMsg.OrderedBlocks), taskInstruction)
					}

					// Liveness tracking: heartbeats prove the callback wiring is
					// alive without producing a visible block; everything else
					// counts as a real event so the renderer can distinguish
					// "wired but quiet" from "callback never fired".
					if _, isHeartbeat := qm.update.(agent.HeartbeatUpdate); isHeartbeat {
						subAgentBlock.LastHeartbeat = time.Now()
					} else {
						subAgentBlock.EventCount++
					}

					// Process inner update and append to subAgentBlock.Blocks
					switch inner := qm.update.(type) {
					case agent.ThinkingUpdate:
						// Handle thinking
						if inner.Append && len(subAgentBlock.Blocks) > 0 && subAgentBlock.Blocks[len(subAgentBlock.Blocks)-1].Type == "thinking" {
							subAgentBlock.Blocks[len(subAgentBlock.Blocks)-1].AppendContent(inner.Content)
						} else {
							subAgentBlock.Blocks = append(subAgentBlock.Blocks, MessageBlock{
								Type:    "thinking",
								Content: inner.Content,
							})
						}
						logDebug("[Queue] Sub-agent thinking update: len=%d", len(inner.Content))
					case agent.ContentUpdate:
						// Handle content
						if inner.Append && len(subAgentBlock.Blocks) > 0 && subAgentBlock.Blocks[len(subAgentBlock.Blocks)-1].Type == "content" {
							subAgentBlock.Blocks[len(subAgentBlock.Blocks)-1].AppendContent(inner.Content)
						} else {
							subAgentBlock.Blocks = append(subAgentBlock.Blocks, MessageBlock{
								Type:    "content",
								Content: inner.Content,
							})
						}
						logDebug("[Queue] Sub-agent content update: len=%d append=%v", len(inner.Content), inner.Append)
					case agent.ToolCallUpdate:
						// Handle tool call
						subAgentBlock.Blocks = append(subAgentBlock.Blocks, MessageBlock{
							Type: "tool_call",
							ToolCall: &ToolCallDisplay{
								ID:         inner.ID,
								Name:       inner.Name,
								Parameters: inner.Parameters,
							},
						})
						logDebug("[Queue] Sub-agent tool_call: %s", inner.Name)
					case agent.ToolResultUpdate:
						// Handle tool result - reuse any existing streaming block for this
						// call (created by ToolOutputChunk) rather than appending a duplicate.
						errStr := ""
						if inner.Error != nil {
							errStr = inner.Error.Error()
						}
						toolResultFound := false
						for i := len(subAgentBlock.Blocks) - 1; i >= 0; i-- {
							b := &subAgentBlock.Blocks[i]
							if b.Type == "tool_result" && b.ToolResult != nil &&
								b.ToolResult.CallID == inner.ID {
								b.ToolResult.SetOutput(inner.Output) // clears streaming builder
								b.ToolResult.Error = errStr
								b.ToolResult.IsStreaming = false
								toolResultFound = true
								logDebug("[Queue] Sub-agent tool_result: updated existing block id=%s outputLen=%d", inner.ID, len(inner.Output))
								break
							}
						}
						if !toolResultFound {
							subAgentBlock.Blocks = append(subAgentBlock.Blocks, MessageBlock{
								Type: "tool_result",
								ToolResult: &ToolResultDisplay{
									CallID: inner.ID,
									Output: inner.Output,
									Error:  errStr,
								},
							})
							logDebug("[Queue] Sub-agent tool_result: new block id=%s outputLen=%d", inner.ID, len(inner.Output))
						}
					case agent.ToolOutputChunk:
						// Handle streaming output
						// Find last tool result or create new one
						found := false
						for i := len(subAgentBlock.Blocks) - 1; i >= 0; i-- {
							if subAgentBlock.Blocks[i].Type == "tool_result" && subAgentBlock.Blocks[i].ToolResult.CallID == inner.ID {
								subAgentBlock.Blocks[i].ToolResult.AppendOutput(inner.Chunk)
								found = true
								break
							}
						}
						if !found {
							subAgentBlock.Blocks = append(subAgentBlock.Blocks, MessageBlock{
								Type: "tool_result",
								ToolResult: &ToolResultDisplay{
									CallID: inner.ID,
									Output: inner.Chunk,
								},
							})
						}
					case agent.AssistantMessageUpdate:
						// Per-turn finalization from the sub-agent's executeLoop.
						// Carries authoritative input/output token counts and a
						// 1-based turn index. We use it to update LIVE token and
						// turn counts during the sub-agent's run — matching
						// claude-code's `tokens && "${formatNumber(tokens)} tokens"`
						// summary on the sub-agent progress line. Without this,
						// these stats stay frozen at zero until SubAgentCompleteUpdate.
						if inner.Turn > subAgentBlock.TurnCount {
							subAgentBlock.TurnCount = inner.Turn
						}
						if inner.InputTokens > 0 || inner.OutputTokens > 0 {
							// InputTokens is the full context sent on this turn — it
							// is the authoritative measure of context size. Output
							// tokens are generated. We sum them for the "tokens"
							// summary the parent agent sees.
							subAgentBlock.TokenCount = inner.InputTokens + inner.OutputTokens
						}
						logDebug("[Queue] Sub-agent assistant message: agent=%s turn=%d in=%d out=%d finish=%s",
							qm.agentName, inner.Turn, inner.InputTokens, inner.OutputTokens, inner.FinishReason)
					case agent.TokenCountUpdate:
						// Real token counts emitted after each API call in the
						// sub-agent's execute loop. This is the authoritative
						// per-turn token count: InputTokens = full context sent.
						// Mirrors the parent's TokenCountUpdate handling so the
						// live progress display can show actual tokens consumed.
						if inner.Turn > subAgentBlock.TurnCount {
							subAgentBlock.TurnCount = inner.Turn
						}
						if inner.InputTokens > 0 || inner.OutputTokens > 0 {
							subAgentBlock.TokenCount = inner.InputTokens + inner.OutputTokens
						}
						logDebug("[Queue] Sub-agent token count: agent=%s turn=%d in=%d out=%d pct=%.1f",
							qm.agentName, inner.Turn, inner.InputTokens, inner.OutputTokens, inner.PctUsed)
					case agent.SubAgentCompleteUpdate:
						// Terminal event — collapse the live transcript into a
						// one-line completion summary. The renderer reads these
						// fields when IsComplete is true.
						subAgentBlock.IsComplete = true
						subAgentBlock.EndTime = time.Now()
						if inner.TurnCount > subAgentBlock.TurnCount {
							subAgentBlock.TurnCount = inner.TurnCount
						}
						subAgentBlock.ToolUseCount = inner.ToolUseCount
						// Only overwrite token count if the terminal event carries
						// a higher value — AssistantMessageUpdate already kept it
						// fresh during streaming and may report higher than what
						// the SubAgent's executor.Stats() aggregates at the end.
						if inner.TokensUsed > subAgentBlock.TokenCount {
							subAgentBlock.TokenCount = inner.TokensUsed
						}
						logDebug("[Queue] Sub-agent complete: agent=%s turns=%d tools=%d tokens=%d duration=%s err=%q",
							qm.agentName, inner.TurnCount, inner.ToolUseCount, inner.TokensUsed, inner.Duration, inner.Error)
					}

					// CRITICAL: Invalidate cache so sub-agent updates are rendered immediately
					a.invalidateLastMessageCache() // Only invalidate last message for streaming

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
				// Return immediately to allow View() to render this update

			case agentContentUpdateMsg:
				if !a.shouldProcessA2AQueuedUpdate(qm.source, qm.conversationID) {
					break
				}
				a.setActivityPhase(ActivityPhaseResponding, "")
				logDebug("[Queue] Processing content update: %d chars (append=%v) [seq=%d]", len(qm.content), qm.append, qm.sequence)

				// REAL-TIME TOKEN ESTIMATION: Track output characters for token counter
				a.streamingOutputChars += len(qm.content)
				estimatedOutputTokens := a.streamingOutputChars / 4

				// Initialize streamingInputTokens from current conversation if not set
				if a.streamingInputTokens == 0 && a.tokenCount > 0 {
					a.streamingInputTokens = a.tokenCount
					logDebug("[AGENT-TOKEN] Initialized streamingInputTokens=%d from tokenCount", a.streamingInputTokens)
				}

				// Update token count for real-time display
				if a.streamingInputTokens > 0 {
					newTokenCount := a.streamingInputTokens + estimatedOutputTokens
					tokenDelta := newTokenCount - a.tokenCount

					// Invalidate cache every ~10 tokens for real-time updates
					if tokenDelta >= 10 || (tokenDelta > 0 && a.streamingOutputChars < 100) {
						a.tokenCount = newTokenCount
						a.sidePanelCache.valid = false
						logDebug("[AGENT-TOKEN] Cache invalidated: input=%d + est_output=%d = %d (delta=+%d)",
							a.streamingInputTokens, estimatedOutputTokens, a.tokenCount, tokenDelta)
					} else {
						a.tokenCount = newTokenCount
					}
				}

				// Update TPS metrics during streaming. Output tokens ONLY — the
				// standard tok/s definition never counts prompt/input tokens.
				// Fed from observed output characters so it works even when the
				// provider reports no usage mid-stream (e.g. local vLLM).
				if a.metrics != nil {
					a.metrics.TPS.UpdateOutputTokens(estimatedOutputTokens)
					a.sidePanelCache.valid = false // Invalidate for metrics rendering
				}

				a.ensureLastMessageIsAssistant()
				if len(a.messages) > 0 && a.messages[len(a.messages)-1].Role == "assistant" {
					lastMsg := &a.messages[len(a.messages)-1]

					// Log BEFORE state

					// Always update the canonical content
					if qm.append {
						lastMsg.AppendContent(qm.content)
					} else {
						lastMsg.Content = qm.content
						lastMsg.contentBuilder = nil // Reset builder on replace
					}

					// Diffusion-reveal bookkeeping: record which content block the
					// update lands in and its rune length before/after, so the
					// denoising reveal animates only the newly-arrived suffix
					// (and never re-noises text it already revealed).
					revealTargetIdx := -1
					revealBaseRunes := 0  // runes already present (revealed prefix)
					revealTotalRunes := 0 // runes after this update

					// Now handle OrderedBlocks
					if qm.append {
						// When appending, check if the LAST block is a content block
						// If it is, append to it. Otherwise create a new content block.
						// This ensures content after tools creates a new block.
						if len(lastMsg.OrderedBlocks) > 0 &&
							lastMsg.OrderedBlocks[len(lastMsg.OrderedBlocks)-1].Type == "content" {
							// Last block is content, append to it
							lastIdx := len(lastMsg.OrderedBlocks) - 1
							revealBaseRunes = len([]rune(lastMsg.OrderedBlocks[lastIdx].GetContent()))
							lastMsg.OrderedBlocks[lastIdx].AppendContent(qm.content)
							revealTargetIdx = lastIdx
							revealTotalRunes = len([]rune(lastMsg.OrderedBlocks[lastIdx].GetContent()))
							logDebug("[Queue] Appended to last content block at index %d", lastIdx)
						} else {
							// Last block is NOT content (e.g., tool_call, tool_result), create new block
							lastMsg.OrderedBlocks = append(lastMsg.OrderedBlocks, MessageBlock{
								Type:     "content",
								Content:  qm.content,
								Sequence: qm.sequence,
							})
							revealTargetIdx = len(lastMsg.OrderedBlocks) - 1
							revealBaseRunes = 0
							revealTotalRunes = len([]rune(qm.content))
							logDebug("[Queue] Created new content block after non-content block (seq=%d)", qm.sequence)
						}
					} else {
						// Replace (not append): update the content block identified by
						// sequence, falling back to the first content block if no
						// exact sequence match exists (handles initial/unsequenced blocks).
						targetIdx := -1
						// 1. Find by exact sequence match
						if qm.sequence > 0 {
							for i := range lastMsg.OrderedBlocks {
								if lastMsg.OrderedBlocks[i].Type == "content" &&
									lastMsg.OrderedBlocks[i].Sequence == qm.sequence {
									targetIdx = i
									break
								}
							}
						}
						// 2. Fall back to first content block
						if targetIdx < 0 {
							for i := range lastMsg.OrderedBlocks {
								if lastMsg.OrderedBlocks[i].Type == "content" {
									targetIdx = i
									break
								}
							}
						}

						if targetIdx >= 0 {
							oldContent := lastMsg.OrderedBlocks[targetIdx].GetContent()
							// Reveal only the suffix that genuinely changed: the
							// shared leading runes were already on screen, so the
							// base is the common-prefix length of old vs new.
							revealBaseRunes = diffusionCommonPrefixRunes(oldContent, qm.content)
							// Reset the streaming builder so GetContent() returns the
							// new canonical value, not an accumulated stream tail.
							lastMsg.OrderedBlocks[targetIdx].ResetContentBuilder()
							lastMsg.OrderedBlocks[targetIdx].Content = qm.content
							revealTargetIdx = targetIdx
							revealTotalRunes = len([]rune(qm.content))
							logDebug("[Queue] Replaced content block at index %d (seq=%d)", targetIdx, qm.sequence)
						} else {
							// No existing content block — create a new one
							lastMsg.OrderedBlocks = append(lastMsg.OrderedBlocks, MessageBlock{
								Type:     "content",
								Content:  qm.content,
								Sequence: qm.sequence,
							})
							revealTargetIdx = len(lastMsg.OrderedBlocks) - 1
							revealBaseRunes = 0
							revealTotalRunes = len([]rune(qm.content))
							logDebug("[Queue] Created initial content block (seq=%d)", qm.sequence)
						}
					}

					// Diffusion models deliver the whole completion as one large
					// chunk — start the denoising reveal for the newly-arrived
					// suffix of the updated block. The guard inside
					// startDiffusionReveal makes identical / already-revealed
					// replace-updates a no-op, so each piece animates once.
					if a.sdk != nil && a.sdk.IsDiffusionModel() &&
						revealTargetIdx >= 0 &&
						(revealTotalRunes-revealBaseRunes) >= diffusionRevealMinChars {
						a.startDiffusionReveal(
							len(a.messages)-1,
							lastMsg.OrderedBlocks[revealTargetIdx].Sequence,
							revealBaseRunes,
							revealTotalRunes,
						)
					}

					// CRITICAL: Mark message as dirty so updateViewportIncremental() detects the change.
					// This ensures streaming content appears in the viewport without early return.
					// O(1) operation: sets bool under mutex. Rendering is already optimized in updateViewportIncremental().
					lastMsg.MarkDirty()
					// CRITICAL: Check if re-render will be triggered
					if a.streamingInProgress {
						a.updateStreamingMessageIncremental()
					} else {
						wasAtBottom := a.msgViewport.AtBottom()
						a.updateViewportContent()
						if wasAtBottom {
							a.msgViewport.GotoBottom()
						}
					}
				} else {
				}
				// Return immediately to allow View() to render this update

			case agentAssistantMessageCompleteMsg:
				if !a.shouldProcessA2AQueuedUpdate(qm.source, qm.conversationID) {
					break
				}
				// Terminal turn signal from SDK. Fires once per assistant turn
				// after all incremental deltas. Authoritative finalization:
				// reconcile the placeholder against the real final content
				// carried by AssistantMessageUpdate — repairs both the "empty
				// message" case (tool-only / non-streaming) and the "partial
				// tail" case (streaming delivered a prefix, real final is
				// longer).
				finalized := reconcileAssistantThinkingFromComplete(a.messages, qm.thinking, nil)
				if finalizeAssistantFromComplete(a.messages, qm.content, nil) {
					finalized = true
				}
				if finalized {
					logDebug("[AssistantMessageComplete] finalized content=%d thinking=%d chars turn=%d", len(qm.content), len(qm.thinking), qm.turn)
					if len(a.messages) > 0 {
						a.messages[len(a.messages)-1].MarkDirty()
					}
					a.invalidateLastMessageCache()
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

			case agentThinkingMsg:
				if !a.shouldProcessA2AQueuedUpdate(qm.source, qm.conversationID) {
					break
				}
				a.setActivityPhase(ActivityPhaseThinking, i18n.T("residual.update.reasoning"))
				a.ensureLastMessageIsAssistant()
				if len(a.messages) > 0 && a.messages[len(a.messages)-1].Role == "assistant" {
					lastMsg := &a.messages[len(a.messages)-1]

					// Update canonical thinking field
					if qm.append {
						lastMsg.AppendThinking(qm.content)
					} else {
						lastMsg.Thinking = qm.content
						lastMsg.thinkingBuilder = nil // Reset builder on replace
					}

					// Find the LAST thinking block in OrderedBlocks.
					// We want the most recent one, not the first — earlier thinking
					// blocks belong to previous turns and must not be overwritten.
					lastThinkingIdx := -1
					for i := range lastMsg.OrderedBlocks {
						if lastMsg.OrderedBlocks[i].Type == "thinking" {
							lastThinkingIdx = i // keep scanning — want the LAST one
						}
					}

					// Determine whether to create a NEW thinking block or update the
					// existing last one.
					//
					// Create NEW when append=false (start of a thinking stream) AND the
					// last thinking block has tool_calls/tool_results after it — meaning
					// it belongs to a previous turn. Also create NEW when none exist yet.
					shouldCreateNew := false
					if !qm.append {
						if lastThinkingIdx < 0 {
							shouldCreateNew = true // first thinking block ever
						} else {
							// Check if tool_calls or tool_results follow the last thinking block.
							// If so, it belongs to a previous turn → we need a new block.
							for i := lastThinkingIdx + 1; i < len(lastMsg.OrderedBlocks); i++ {
								t := lastMsg.OrderedBlocks[i].Type
								if t == "tool_call" || t == "tool_result" {
									shouldCreateNew = true
									break
								}
							}
						}
					}

					if shouldCreateNew {
						// New turn is starting its thinking stream — create a fresh block.
						lastMsg.OrderedBlocks = append(lastMsg.OrderedBlocks, MessageBlock{
							Type:     "thinking",
							Content:  qm.content,
							Sequence: qm.sequence,
						})
					} else if lastThinkingIdx >= 0 {
						// Update the current (last) thinking block.
						if qm.append {
							lastMsg.OrderedBlocks[lastThinkingIdx].AppendContent(qm.content)
						} else {
							// Replace: reset the builder so GetContent() returns the new value.
							lastMsg.OrderedBlocks[lastThinkingIdx].ResetContentBuilder()
							lastMsg.OrderedBlocks[lastThinkingIdx].Content = qm.content
						}
					} else {
						// Fallback: no existing block (shouldn't reach here due to shouldCreateNew
						// logic above, but be safe).
						lastMsg.OrderedBlocks = append(lastMsg.OrderedBlocks, MessageBlock{
							Type:     "thinking",
							Content:  qm.content,
							Sequence: qm.sequence,
						})
					}

					// CRITICAL: Invalidate cache so the new/updated thinking block is rendered.
					// Without this, updateViewportIncremental() uses stale pre-rendered
					// content and the block is invisible until a full re-render fires.
					a.invalidateLastMessageCache()
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
				// Return immediately to allow View() to render this update

			case agentResponseMsg:
				logDebug("[Queue] Processing final response")
				a.streamingMessage = false
				a.clearToolActivities() // Clear activity tracking

				a.streamingInProgress = false                           // ← Reset streaming optimization state
				a.setUserScrolledAway(false, "agent_response_complete") // ← Reset scroll tracking
				a.animationClock.SetStreaming(false)                    // Switch to idle animation
				a.loadingIndicator.Stop(a.animationClock)
				a.spinner.Stop(a.animationClock)

				// Track completion time for terminal title notification
				a.lastStreamingEndTime = time.Now()
				a.completionNotified = false
				a.viewNeedsRefresh = true

				// Send bell notification for task completion
				fmt.Print("\a") // ASCII bell character

				// Calculate elapsed time
				elapsed := time.Since(a.agentStartTime)

				// Set completion info on the assistant message
				a.ensureLastMessageIsAssistant()
				if len(a.messages) > 0 && a.messages[len(a.messages)-1].Role == "assistant" {
					lastMsg := &a.messages[len(a.messages)-1]
					lastMsg.IsComplete = true
					lastMsg.ElapsedTime = elapsed
					lastMsg.Model = a.currentModel
				}

				// Persist completion metadata to SDK message for loading later
				// NOTE: This is display-only metadata, NOT sent to the LLM
				if a.sdk != nil && a.currentConvID != "" {
					ctx := context.Background()
					if err := a.sdk.UpdateLastMessageCompletion(ctx, a.currentConvID, elapsed, a.currentModel); err != nil {
						logDebug("[APP] Failed to update message completion metadata: %v", err)
					}
				}

				// Update conversation status to idle through the centralized activity state.
				a.setActivityPhase(ActivityPhaseIdle, "")

				// Notify curator that agent is idle (starts inactivity timer)
				if a.sdk != nil {
					if hm := a.sdk.GetHooksManager(); hm != nil {
						hm.NotifyAgentIdle()
					}
				}
				if qm.err != nil {
					// Use structured Sentry integration with proper context
					// Only capture if it's not an intentional cancellation
					if !ShouldIgnoreError(qm.err) {
						// Create a context with the trace ID from the message
						ctx := context.Background()
						if qm.traceID != "" {
							// This is a bit of a hack since we don't have the tracer here,
							// but CaptureSentryErrorWithContext will extract it if we put it in the right key.
							// The key is "trace_id" in observability/tracer.go
							type contextKey string
							const traceIDKey contextKey = "trace_id"
							ctx = context.WithValue(ctx, traceIDKey, qm.traceID)
						}
						CaptureSentryErrorWithContext(ctx, qm.err, a.currentProvider, a.currentModel, a.currentConvID)
					}

					if len(a.messages) > 0 && a.messages[len(a.messages)-1].Role == "assistant" {
						lastMsg := &a.messages[len(a.messages)-1]
						lastMsg.ErrorLineage = buildErrorLineagePanel(qm.err, a.errorLineageExpandedForConversation(a.currentConvID))
						if lastMsg.ErrorLineage != nil {
							// Enrich the panel with the SDK's flight recorder lookup (trace events + timings),
							// when available. This is best-effort and should never block rendering.
							if a.sdk != nil && lastMsg.ErrorLineage.ErrorID != "" {
								if report, err := a.sdk.LookupErrorLineage(lastMsg.ErrorLineage.ErrorID); err == nil && report != nil {
									lastMsg.ErrorLineage.Report = report
									if lastMsg.ErrorLineage.TraceID == "" && report.TraceID != "" {
										lastMsg.ErrorLineage.TraceID = report.TraceID
									}
								} else if err != nil {
									logDebug("[Lineage] LookupErrorLineage failed: %v", err)
								}
							}

							// Keep the visible chat error line concise; the panel contains the full lineage.
							lastMsg.Content = formatChatErrorLine(lastMsg.ErrorLineage)

							if lastMsg.Metadata == nil {
								lastMsg.Metadata = make(map[string]any)
							}
							if lastMsg.ErrorLineage.ErrorID != "" {
								lastMsg.Metadata["error_id"] = lastMsg.ErrorLineage.ErrorID
							}
							if lastMsg.ErrorLineage.TraceID != "" {
								lastMsg.Metadata["trace_id"] = lastMsg.ErrorLineage.TraceID
							}
							if lastMsg.ErrorLineage.Code != "" {
								lastMsg.Metadata["error_code"] = lastMsg.ErrorLineage.Code
							}
						}
						if lastMsg.ErrorLineage == nil {
							lastMsg.Content = i18n.T("classic_chat.stream.error", qm.err)
						}
					}
					a.addNotification("error", i18n.T("residual.update.run_failed", qm.err))
				}
				// NOTE: We DON'T replace messages here because we already built them
				// incrementally via tool call/result/content/thinking updates.
				// The SDK AllMessages has the conversation split across multiple messages
				// (one per turn), but we merged everything into a single assistant message
				// for better UX. Replacing would break the display we carefully built.
				//
				// Fallback: when no incremental content events arrived (some non-
				// streaming providers / single-shot responses), the last assistant
				// message has an empty Content and no content-type OrderedBlock —
				// rendering an empty reply. Backfill from the final payload so the
				// user actually sees the response. This only fires when the
				// message is empty; incremental builds are left untouched.
				//
				// `qm.content` holds ExecuteMessage's return value, which is empty
				// for multi-turn tool-heavy responses (the SDK returns the last
				// turn's text, and that turn often has only tool_use). Fall back
				// to scanning `qm.AllMessages` — the SDK's full history snapshot
				// — for the most recent assistant text.
				finalContent := qm.content
				if strings.TrimSpace(finalContent) == "" {
					finalContent = lastAssistantContent(qm.AllMessages)
				}
				if backfillEmptyAssistantFromFinal(a.messages, finalContent, qm.err) {
					logDebug("[agentResponseMsg] Fallback: backfilled final content (%d chars) — no incremental events arrived", len(finalContent))
				}

				// Just update token count from final conversation state
				logDebug("[APP-TOKENS] agentResponseMsg handler - fetching conversation token count")
				if a.sdk != nil && a.currentConvID != "" {
					ctx := context.Background()
					if conv, err := a.sdk.ResumeConversation(ctx, a.currentConvID); err == nil {
						// Use CurrentContextSize (input_tokens from API) for context window display
						// DO NOT fall back to TotalTokens - it's cumulative output tokens, meaningless for context window
						logDebug("[APP-TOKENS] Loaded conv: CurrentContextSize=%d TotalTokens=%d",
							conv.CurrentContextSize, conv.TotalTokens)
						if conv.CurrentContextSize > 0 {
							prevTokenCount := a.tokenCount
							a.tokenCount = conv.CurrentContextSize
							a.tokenCountIsEstimate = conv.CurrentContextSizeEstimated
							// Always sync lastRealTokenCount to the reloaded size,
							// even when it is a post-compaction estimate. A compacted
							// CurrentContextSize is ALWAYS estimated
							// (AdvanceActiveContext sets CurrentContextSizeEstimated=
							// true), so the old `!estimated` guard left a stale
							// pre-compaction total here on reload — which the
							// pre-send auto-compaction predictor then used to
							// re-fire compaction. The estimate is the correct
							// current-context magnitude; a stale real value is not.
							a.lastRealTokenCount = conv.CurrentContextSize
							a.recordTokenChange(prevTokenCount, a.tokenCount, "conversation_reload", fmt.Sprintf("convID=%s", a.currentConvID[:8]))
							logDebug("[APP-TOKENS] Updated tokenCount: prev=%d new=%d lastReal=%d (source: CurrentContextSize)",
								prevTokenCount, a.tokenCount, a.lastRealTokenCount)
							// Sync back to the sidebar conversation list so the displayed
							// token count stays consistent with what was reported by the API.
							for i := range a.conversations {
								if a.conversations[i].ID == a.currentConvID {
									a.conversations[i].TotalTokens = conv.CurrentContextSize
									break
								}
							}
						} else {
							// No valid context size available - leave unchanged or it was set during streaming
							logDebug("[APP-TOKENS] WARNING: CurrentContextSize=0, tokenCount unchanged at %d", a.tokenCount)
						}
					} else {
						logDebug("[APP-TOKENS] Failed to resume conversation: %v", err)
					}
				} else {
					logDebug("[APP-TOKENS] sdk=%v currentConvID='%s' - cannot fetch token count",
						a.sdk != nil, a.currentConvID)
				}
				// CRITICAL: Invalidate cache so spinner is removed from display
				a.invalidateViewportCache()
				if a.streamingInProgress {
					a.updateStreamingMessageIncremental()
				} else {
					wasAtBottom := a.msgViewport.AtBottom()
					a.updateViewportContent()
					if wasAtBottom {
						a.msgViewport.GotoBottom()
					}
				}
				if a.syncPeerMessagesFromSDK(a.currentConvID) {
					a.invalidateViewportCache()
					wasAtBottom := a.msgViewport.AtBottom()
					a.updateViewportContent()
					if wasAtBottom {
						a.msgViewport.GotoBottom()
					}
				}
				// Surface the /goal evaluator's verdict for the turn that just
				// finished (notification + side panel state).
				a.maybeNotifyGoalEvaluation()

				// Check for queued user messages — combine and send all at once
				a.pendingMsgMu.Lock()
				pending := a.pendingUserMessages
				a.pendingUserMessages = nil
				a.pendingMsgMu.Unlock()
				if len(pending) > 0 {
					combined := strings.Join(pending, "\n\n---\n\n")
					logDebug("[Queue] Auto-sending %d queued messages (%d chars)", len(pending), len(combined))
					a.textInput.SetValue(combined)
					return a, a.handleSendMessage()
				}
				// Drain leftover SYSTEM notifications (background completions,
				// scheduled cron/goal prompts) that arrived too late for mid-run
				// injection. Without this, anything queued in the final moments
				// of a turn sat until the user's next manual message — which is
				// exactly when goal continuations and cron fires arrive, so
				// loops stalled here. The wake drains the queue, so this cannot
				// re-trigger on the next turn end.
				if wakeCmd := a.wakePendingSystemMessages(); wakeCmd != nil {
					logDebug("[Queue] Waking agent for queued system notifications")
					return a, wakeCmd
				}
				return a, nil

			case bgProcessDoneMsg:
				logDebug("[Queue] Background process done: id=%s state=%s cmd=%s exit=%d",
					qm.processID, qm.state, truncateForLog(qm.command, 40), qm.exitCode)
				// NOTE: Only explicitly-backgrounded processes reach here.
				// The manager filters at the source via MarkBackgrounded/IsBackgrounded.

				// Build a system message describing what happened
				var statusDesc string
				switch qm.state {
				case "completed":
					statusDesc = fmt.Sprintf("Background command completed successfully (exit code %d)", qm.exitCode)
				case "failed":
					statusDesc = fmt.Sprintf("Background command failed (exit code %d)", qm.exitCode)
				default:
					statusDesc = fmt.Sprintf("Background command %s", qm.state)
				}

				cmdPreview := qm.command
				if len(cmdPreview) > 120 {
					cmdPreview = cmdPreview[:117] + "..."
				}

				// Include an output preview inline so the agent can react to the
				// result without a separate ReadBackgroundCommand round-trip.
				// Only suggest ReadBackgroundCommand when the output was large
				// enough to be truncated — otherwise the agent already has
				// everything it needs.
				var outputSection string
				if qm.outputPreview != "" {
					outputSection = "\n\noutput:\n" + qm.outputPreview
				}
				var followup string
				if qm.outputTruncated {
					followup = fmt.Sprintf(
						" Output truncated above — call ReadBackgroundCommand with task_id %q for the full text.",
						qm.processID)
				}

				systemText := fmt.Sprintf(
					"[BACKGROUND] task_id=%s command=%s status=%s%s\n\n%s.%s",
					qm.processID, cmdPreview, qm.state, outputSection, statusDesc, followup,
				)

				// Route through pendingSystemMessages so the rich injector delivers this
				// as conversation.RoleSystem, NOT conversation.RoleUser. Survey 2026-04-27
				// flagged "[Background Task Notification]" blocks tagged [USER] in the
				// transcript as a correctness bug that trains the model to attribute
				// async system output to the human. This channel also suppresses the
				// idle-path spurious agent activation — we queue rather than auto-send.
				a.pendingMsgMu.Lock()
				a.pendingSystemMessages = append(a.pendingSystemMessages, systemText)
				a.pendingMsgMu.Unlock()
				if a.streamingMessage {
					logDebug("[bgProcessDone] Agent running, queued SYSTEM message for process %s", qm.processID)
					a.addNotification("info", i18n.T("residual.update.background_task_queued", qm.processID, localizedBackgroundState(qm.state)))
				} else {
					// Agent is idle — wake it so it processes the notification without
					// waiting for the user's next manual turn. The wake DRAINS the
					// queue (sending the queued text as the suppressed trigger), so
					// the message is never delivered twice — the old queue-and-also-
					// send-a-copy flow re-triggered the wake on the next turn end.
					logDebug("[bgProcessDone] Agent idle — waking to process notification for %s", qm.processID)
					return a, a.wakePendingSystemMessages()
				}

			case bgAgentDoneMsg:
				logDebug("[Queue] Background agent done: id=%s status=%s task=%s",
					qm.agentID, qm.status, truncateForLog(qm.task, 40))

				var statusDesc string
				switch qm.status {
				case "completed":
					statusDesc = "Background agent completed successfully"
				case "failed":
					statusDesc = "Background agent failed"
				case "cancelled":
					statusDesc = "Background agent was cancelled"
				default:
					statusDesc = fmt.Sprintf("Background agent %s", qm.status)
				}

				taskPreview := qm.task
				if len(taskPreview) > 120 {
					taskPreview = taskPreview[:117] + "..."
				}

				// Build injection text — mirrors Claude Code's OP4 task_status format.
				// Tells the LLM exactly what happened and includes the subagent's
				// final result inline so the parent doesn't need a follow-up
				// TaskOutput call. Tagged [BACKGROUND] and routed via pendingSystemMessages
				// so it is delivered as conversation.RoleSystem, not RoleUser.
				systemText := fmt.Sprintf(
					"[BACKGROUND] agent_id=%s task=%s status=%s\n",
					qm.agentID, taskPreview, qm.status)
				if qm.errorMessage != "" {
					systemText += fmt.Sprintf("delta: %s\n", qm.errorMessage)
				}
				if qm.outputFilePath != "" {
					systemText += fmt.Sprintf("output_file: %s\n", qm.outputFilePath)
				}
				if qm.resultPreview != "" {
					systemText += "\nresult:\n" + qm.resultPreview + "\n"
				}
				systemText += fmt.Sprintf("\n%s.", statusDesc)
				if qm.resultTruncated {
					systemText += fmt.Sprintf(
						" Result truncated above — call TaskOutput with agent_id=%q and action=%q for the full text.",
						qm.agentID, "result")
				}

				// System channel (see bgProcessDoneMsg comment for rationale).
				if a.streamingMessage {
					a.pendingMsgMu.Lock()
					a.pendingSystemMessages = append(a.pendingSystemMessages, systemText)
					a.pendingMsgMu.Unlock()
					logDebug("[bgAgentDone] Agent running, queued SYSTEM message for agent %s", qm.agentID)
					a.addNotification("info", i18n.T("residual.update.background_agent_queued", qm.agentID, localizedBackgroundState(qm.status)))
				} else {
					a.pendingMsgMu.Lock()
					a.pendingSystemMessages = append(a.pendingSystemMessages, systemText)
					a.pendingMsgMu.Unlock()
					a.addNotification("info", i18n.T("residual.update.background_agent_queued", qm.agentID, localizedBackgroundState(qm.status)))
					// Agent is idle — wake it so it processes the sub-agent's
					// completion without waiting for the user's next manual turn.
					// Mirrors bgProcessDoneMsg above. Without this, background
					// agents finished and the main loop never resumed on its own.
					logDebug("[bgAgentDone] Agent idle — waking to process notification for %s", qm.agentID)
					return a, a.wakePendingSystemMessages()
				}

			case injectedUserMsg:
				// A queued user message was injected into the agent conversation mid-execution.
				// Show it in the chat as a user message so the UI reflects what the agent sees.
				logDebug("[Queue] Injected user message mid-execution: %s", truncateForLog(qm.content, 60))

				// CRITICAL FIX: When a message is injected mid-stream, we must properly
				// close the current assistant message, display the user message, and
				// open a fresh assistant placeholder.  Without this, the last message
				// in the array is "user" and every subsequent streaming update
				// (tool calls, content, thinking) is silently dropped — the screen
				// freezes while the agent keeps running in the background.

				// Step 1: Finalize the current assistant message (if any).
				if a.streamingMessage && len(a.messages) > 0 && a.messages[len(a.messages)-1].Role == "assistant" {
					prevAssistant := &a.messages[len(a.messages)-1]
					prevAssistant.IsComplete = true
					prevAssistant.ElapsedTime = time.Since(a.agentStartTime)
					prevAssistant.Model = a.currentModel
					logDebug("[Queue] Finalized previous assistant message before injecting user msg")
				}

				// Step 2: Append the injected user message.
				a.messages = append(a.messages, Message{
					Role:      "user",
					Content:   qm.content,
					Timestamp: time.Now(),
				})

				// Step 3: Open a new assistant placeholder so the agent's next turn
				// has a target message for streaming updates.
				if a.streamingMessage {
					a.messages = append(a.messages, Message{
						Role:      "assistant",
						Content:   "",
						Timestamp: time.Now(),
					})
					// Reset the start time so the new assistant turn tracks its own elapsed time.
					a.agentStartTime = time.Now()
					logDebug("[Queue] Opened new assistant placeholder after injected user message")
				}

				if a.streamingInProgress {
					a.updateStreamingMessageIncremental()
				} else {
					wasAtBottom := a.msgViewport.AtBottom()
					a.updateViewportContent()
					if wasAtBottom {
						a.msgViewport.GotoBottom()
					}
				}

			case providerEventMsg:
				logDebug("[Queue] Processing provider event: type=%s", qm.Type)

				switch qm.Type {
				case "retry":
					msg := i18n.T("residual.update.retrying_provider",
						qm.ProviderName, qm.Attempt, qm.MaxAttempts, qm.WaitDuration)
					a.addNotification("warning", msg)

					// Inject retry notice unconditionally — do NOT gate on assistant role.
					// On first-turn failures there is no assistant message yet.
					retryNotice := lipgloss.NewStyle().
						Foreground(lipgloss.Color(a.theme.TextMuted)).
						Italic(true).
						Render(i18n.T("residual.update.retrying_inline", qm.ProviderName, qm.Attempt, qm.MaxAttempts))
					if len(a.messages) > 0 && a.messages[len(a.messages)-1].Role == "assistant" {
						a.messages[len(a.messages)-1].AppendContent("\n\n" + retryNotice)
					} else {
						a.messages = append(a.messages, Message{
							Role:      "assistant",
							Content:   retryNotice,
							Timestamp: time.Now(),
						})
					}

				case "account_rotate":
					// Stacked-account rotation: the active OAuth account hit its
					// subscription limit and the next stacked account took over.
					msg := i18n.T("residual.update.account_rotated",
						qm.ProviderName, qm.FromProvider, qm.ToProvider)
					a.addNotification("warning", msg)
					rotateNotice := lipgloss.NewStyle().
						Foreground(lipgloss.Color(a.theme.TextMuted)).
						Italic(true).
						Render(i18n.T("residual.update.account_rotated_inline", qm.FromProvider, qm.ToProvider))
					if len(a.messages) > 0 && a.messages[len(a.messages)-1].Role == "assistant" {
						a.messages[len(a.messages)-1].AppendContent("\n\n" + rotateNotice)
					} else {
						a.messages = append(a.messages, Message{
							Role:      "assistant",
							Content:   rotateNotice,
							Timestamp: time.Now(),
						})
					}

				case "usage_warning":
					// Approaching-limit warning derived from the passive rate-limit
					// snapshots; text is preformatted by the rotation wrapper.
					a.addNotification("warning", qm.Error)

				case "switch":
					msg := i18n.T("residual.update.switching_provider", qm.FromProvider, qm.ToProvider)
					a.addNotification("warning", msg)

					// Parse "provider/model[idx]" → toProvider and toModel.
					// reliabilityEntryName format: "ablated/kimi-k2.6[0]" or "anthropic/claude-opus-4[1]"
					toProvider := qm.ToProvider
					toModel := ""
					if bracketIdx := strings.LastIndex(toProvider, "["); bracketIdx >= 0 {
						toProvider = toProvider[:bracketIdx]
					}
					if slashIdx := strings.Index(toProvider, "/"); slashIdx >= 0 {
						toModel = toProvider[slashIdx+1:]
						toProvider = toProvider[:slashIdx]
					}

					// Update all four state fields — mirrors agentFallbackMsg{success}.
					a.currentProvider = toProvider
					if toModel != "" {
						a.currentModel = toModel
					}
					providerDisplay := toProvider
					modelDisplay := toModel
					if cm, cmErr := commands.NewConfigManager(); cmErr == nil {
						if providerConfigs, loadErr := cm.LoadProviders(); loadErr == nil {
							for _, prov := range providerConfigs {
								if strings.EqualFold(prov.Name, toProvider) {
									providerDisplay = prov.DisplayName
									for _, m := range prov.Models {
										if m.ID == toModel {
											modelDisplay = m.DisplayName
											break
										}
									}
									break
								}
							}
						}
					}
					if providerDisplay != "" {
						a.currentProviderDisplay = providerDisplay
					}
					if toModel != "" && modelDisplay != "" {
						a.currentModelDisplay = modelDisplay
					}
					a.sidePanelCache.valid = false

					// Sync model command and settings model page.
					if modelCmd, ok := a.cmdRegistry.Get("model"); ok {
						if mc, ok := modelCmd.(*commands.ModelCommand); ok {
							mc.SetCurrent(toProvider, toModel)
						}
					}
					if modelSettings := a.settingsManager.GetModelSettings(); modelSettings != nil {
						modelSettings.SyncCurrent(toProvider, toModel)
					}

					// Inject switch notice into chat unconditionally.
					// Do NOT gate on lastMsg.Role=="assistant" — on first-turn 404s there
					// is no assistant message yet, so that guard silently dropped the notice.
					noticeText := i18n.T("residual.update.provider_switched", qm.FromProvider, qm.ToProvider)
					if len(a.messages) > 0 && a.messages[len(a.messages)-1].Role == "assistant" {
						statusLine := lipgloss.NewStyle().
							Foreground(lipgloss.Color(a.theme.Primary)).
							Bold(true).
							Render("\n\n" + noticeText)
						a.messages[len(a.messages)-1].AppendContent(statusLine)
					} else {
						statusLine := lipgloss.NewStyle().
							Foreground(lipgloss.Color(a.theme.Primary)).
							Bold(true).
							Render(noticeText)
						a.messages = append(a.messages, Message{
							Role:      "assistant",
							Content:   statusLine,
							Timestamp: time.Now(),
						})
					}
				}

				a.updateViewportContent()

			case agentFallbackMsg:
				if !a.shouldProcessA2AQueuedUpdate(qm.source, qm.conversationID) {
					break
				}
				logDebug("[Queue] Processing fallback event: status=%s provider=%s/%s", qm.status, qm.provider, qm.model)

				switch qm.status {
				case "failed":
					// Primary or intermediate attempt failed — show warning
					label := i18n.T("residual.update.primary")
					if qm.attemptIndex > 0 {
						label = i18n.T("residual.update.fallback_number", qm.attemptIndex)
					}
					msg := i18n.T("residual.update.model_attempt_failed", label, qm.provider, qm.model, qm.errMsg)
					a.addNotification("warning", msg)

					// Add status line to chat
					if len(a.messages) > 0 {
						lastMsg := &a.messages[len(a.messages)-1]
						if lastMsg.Role == "assistant" {
							statusLine := lipgloss.NewStyle().
								Foreground(lipgloss.Color(a.theme.Warning)).
								Italic(true).
								Render(i18n.T("residual.update.model_failed_inline", label, qm.provider, qm.model))
							lastMsg.AppendContent(statusLine)
						}
					}

				case "success":
					if qm.attemptIndex > 0 {
						// Fallback succeeded — resolve friendly display names
						providerDisplay := qm.provider
						modelDisplay := qm.model
						if cm, err := commands.NewConfigManager(); err == nil {
							if providerConfigs, err := cm.LoadProviders(); err == nil {
								for _, prov := range providerConfigs {
									if prov.Name == qm.provider {
										providerDisplay = prov.DisplayName
										for _, m := range prov.Models {
											if m.ID == qm.model {
												modelDisplay = m.DisplayName
												break
											}
										}
										break
									}
								}
							}
						}

						msg := i18n.T("residual.update.using_fallback", providerDisplay, modelDisplay)
						a.addNotification("info", msg)

						// Update side panel to reflect actual provider/model
						a.currentProvider = qm.provider
						a.currentModel = qm.model
						a.currentProviderDisplay = providerDisplay
						a.currentModelDisplay = modelDisplay
						a.sidePanelCache.valid = false

						// Sync settings model overrides page
						if modelSettings := a.settingsManager.GetModelSettings(); modelSettings != nil {
							modelSettings.SyncCurrent(qm.provider, qm.model)
						}

						// Sync model command state
						if modelCmd, ok := a.cmdRegistry.Get("model"); ok {
							if mc, ok := modelCmd.(*commands.ModelCommand); ok {
								mc.SetCurrent(qm.provider, qm.model)
							}
						}

						// Add status line to chat
						if len(a.messages) > 0 {
							lastMsg := &a.messages[len(a.messages)-1]
							if lastMsg.Role == "assistant" {
								statusLine := lipgloss.NewStyle().
									Foreground(lipgloss.Color(a.theme.Primary)).
									Bold(true).
									Render(i18n.T("residual.update.fallback_inline",
										providerDisplay, modelDisplay, qm.fromProvider, qm.fromModel))
								lastMsg.AppendContent(statusLine)
							}
						}
					}
				}

				a.updateViewportContent()

			case agentExhaustedMsg:
				if !a.shouldProcessA2AQueuedUpdate(qm.source, qm.conversationID) {
					// Must not ignore this: the agent goroutine is blocking on the
					// response channel. Send cancel so it doesn't hang forever.
					if qm.response != nil {
						select {
						case qm.response <- agent.FallbackDecision{Cancel: true}:
						default:
						}
					}
					break
				}
				logDebug("[Queue] Processing exhausted event: provider=%s/%s attempts=%d err=%s",
					qm.provider, qm.model, qm.attempts, qm.errMsg)

				// Store so the keyboard handler can send the decision after profile selection.
				a.pendingExhaustionResponse = qm.response

				// Notification
				exhaustMsg := i18n.T("residual.update.models_exhausted", qm.provider, qm.model, qm.attempts)
				if qm.errMsg != "" {
					exhaustMsg += ": " + qm.errMsg
				}
				a.addNotification("error", exhaustMsg)

				// Open the profile switcher with a contextual banner.
				if a.sdk != nil {
					if pm := a.sdk.GetProfileManager(); pm != nil {
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
						banner := i18n.T("residual.update.pick_profile_retry", qm.provider, qm.model, qm.attempts)
						a.profileSwitcher.ShowWithBanner(profiles, activeProfileID, defaultProfileID, banner)
					}
				}

				a.updateViewportContent()

			case subAgentExhaustedMsg:
				if !a.shouldProcessA2AQueuedUpdate(qm.source, qm.conversationID) {
					if qm.response != nil {
						select {
						case qm.response <- agent.FallbackDecision{Cancel: true}:
						default:
						}
					}
					break
				}
				logDebug("[Queue] Sub-agent exhausted: agent=%s provider=%s/%s attempts=%d", qm.agentID, qm.provider, qm.model, qm.attempts)
				a.pendingSubAgentExhaustion[qm.agentID] = qm.response
				exhaustMsg := i18n.T("residual.update.subagent_exhausted", qm.agentName, qm.provider, qm.model, qm.attempts)
				if qm.errMsg != "" {
					exhaustMsg += ": " + qm.errMsg
				}
				a.addNotification("error", exhaustMsg)
				if a.sdk != nil {
					if pm := a.sdk.GetProfileManager(); pm != nil {
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
						banner := i18n.T("residual.update.subagent_pick_profile_retry", qm.agentName, qm.provider, qm.model, qm.attempts)
						a.profileSwitcher.ShowWithBanner(profiles, activeProfileID, defaultProfileID, banner)
					}
				}

			case conversationsLoadedMsg:
				if qm.err != nil {
					errMsg := fmt.Sprintf("[CONV] Failed to reload conversations: %v", qm.err)
					logDebug("%s", errMsg)
					if a.debugScreen != nil {
						a.debugScreen.AddLog(errMsg)
					}
					a.conversationsLoading = false
					a.conversationsLoaded = false
					a.animationClock.Unsubscribe()
				} else {
					// applyConversationsFirstPage sets conversationsLoading=false and unsubscribes
					a.applyConversationsFirstPage(qm.conversations)
					a.conversationsLoaded = true
				}
				a.viewNeedsRefresh = true

			case historyPreviewLoadedMsg:
				a.applyHistoryPreviewLoaded(qm)
				a.viewNeedsRefresh = true

			case conversationsNextPageMsg:
				if qm.err != nil {
					errMsg := fmt.Sprintf("[CONV] Next-page fetch failed offset=%d: %v", qm.offset, qm.err)
					logDebug("%s", errMsg)
					if a.debugScreen != nil {
						a.debugScreen.AddLog(errMsg)
					}
					// Roll back the tentative advance so the next scroll retries.
					a.storageOffset = qm.offset
				} else {
					a.appendConversationsPage(qm.conversations, qm.offset)
				}
				a.conversationsPageLoading = false
				a.viewNeedsRefresh = true

			case usageDataFetchedMsg:
				a.applyUsageDataFetched(qm)

			case usageEntryFetchedMsg:
				a.applyUsageEntryFetched(qm)

			case conversationUsageFetchedMsg:
				a.applyConversationUsageFetched(qm)

			// ── Plan Mode messages ──────────────────────────────────────────────────
			// MUST be handled here in the drain loop — the drain loop consumes them
			// from updateQueue before the outer Update() switch ever sees them.
			case planEnterMsg:
				if !a.inPlanMode {
					a.inPlanMode = true
					a.lastCommittedMode = a.operatingMode
					a.operatingMode = "plan"
					a.planContent = ""
					a.planQuestionModal = nil
					a.sidePanelCache.valid = false
					a.messages = append(a.messages, Message{
						Role:      "system",
						Content:   i18n.T("final.update.plan_mode_activated"),
						Timestamp: time.Now(),
					})
					a.invalidateViewportCache()
					a.updateViewportContent()
					logDebug("[PLAN] Plan mode entered via drain loop")
				}
				a.viewNeedsRefresh = true

			case planApprovalRequestMsg:
				a.planContent = qm.plan
				a.sidePanelCache.valid = false
				// Add plan as a scrollable assistant message then show the choice bar
				a.messages = append(a.messages, Message{
					Role:      "assistant",
					Content:   i18n.T("residual.update.plan_for_review") + "\n\n" + qm.plan,
					Timestamp: time.Now(),
				})
				a.invalidateViewportCache()
				a.updateViewportContent()
				if a.msgViewport != nil {
					a.msgViewport.GotoBottom()
				}
				a.createPlanChoiceModal()
				logDebug("[PLAN] Plan approval request via drain loop, plan_len=%d", len(qm.plan))
				a.viewNeedsRefresh = true
			case cronPromptInjectMsg:
				// A scheduled prompt fired (cron job, ScheduleWakeup, goal
				// continuation — PromptSink path). This case MUST live in the
				// queueDrain switch (not only in the outer Update switch)
				// because cronPromptInjectMsg is dispatched via sendToRuntime →
				// updateQueue; without a case here the message was silently
				// consumed and dropped, which broke /loop recurrence and
				// /goal continuation (the agent ran one turn and went silent).
				systemText := "[SCHEDULED] " + qm.prompt
				a.pendingMsgMu.Lock()
				a.pendingSystemMessages = append(a.pendingSystemMessages, systemText)
				a.pendingMsgMu.Unlock()
				if a.streamingMessage {
					a.addNotification("info", i18n.T("residual.update.scheduled_prompt_queued"))
				} else {
					// Agent idle — mark that we must wake it so the scheduled
					// prompt is processed without waiting for the user's next
					// manual turn. We deliberately do NOT return here: an early
					// return would abandon the drain loop and strand any other
					// messages still in updateQueue (e.g. a second cron/goal
					// prompt firing in the same tick) until the next Bubble Tea
					// event — i.e. until the user clicks/focuses the terminal.
					// The wake is dispatched once, after the drain, below.
					cronWakePending = true
				}
			}
		default:
			// Queue is empty, exit drain loop
			break queueDrain
		}
	}

	// The batch-limit continuation is now handled by the top-of-Update safety-net
	// defer above (it reschedules a drain whenever the queue is non-empty on any
	// return path), so no separate batch-overflow command is needed here.

	// If a scheduled prompt was drained while idle, start the turn that
	// consumes pendingSystemMessages now — but on the normal return path,
	// batched like batchOverflowCmd, so we never strand the rest of the queue.
	// wakePendingSystemMessages drains ALL pending system messages under lock,
	// so a single call here delivers every scheduled prompt drained in this
	// Update, not just the first.
	var cronWakeCmd tea.Cmd
	if cronWakePending && !a.streamingMessage {
		cronWakeCmd = a.wakePendingSystemMessages()
	}
	defer func() {
		if cronWakeCmd != nil {
			retCmd = tea.Batch(retCmd, cronWakeCmd)
		}
	}()

	// Dispatch a background line-wrapping cmd whenever the viewport's pre-wrapped
	// cache is absent or stale. The defer fires on every return path so a single
	// content update always triggers exactly one background pass regardless of how
	// many updateViewport() calls occurred during queue draining.
	// Only active on ScreenChat where msgViewport is the rendered component.
	defer func() {
		if cmd := a.nextViewportPrewrapCmd(); cmd != nil {
			retCmd = tea.Batch(retCmd, cmd)
		}
	}()

	// Give interactive command a chance to handle async (non-key/paste) messages first
	if a.activeCommand != nil && a.activeCommand.IsInteractive() {
		switch typed := msg.(type) {
		case tea.KeyMsg, tea.PasteMsg:
			// Let handleKey/paste routing manage these to avoid double-processing
		case tea.WindowSizeMsg:
			layout := computeChatAreaLayout(typed.Width, typed.Height, a.showSidePanel)
			updatedCmd, cmd := a.activeCommand.Update(tea.WindowSizeMsg{
				Width:  layout.ChatWidth,
				Height: typed.Height,
			})
			a.activeCommand = updatedCmd
			if !a.activeCommand.IsInteractive() {
				a.activeCommand = nil
			}
			if cmd != nil {
				return a, cmd
			}
		default:
			updatedCmd, cmd := a.activeCommand.Update(msg)
			a.activeCommand = updatedCmd
			if !a.activeCommand.IsInteractive() {
				a.activeCommand = nil
			}
			if cmd != nil {
				return a, cmd
			}
		}
	}

	switch msg := msg.(type) {
	case tea.BackgroundColorMsg:
		a.applyTerminalBackground(msg.IsDark())

	case initialPromptMsg:
		return a, a.handleInitialPrompt(msg)

	case frameFlushMsg:
		// A frame was skipped for the frame budget; compose it now.
		a.clearTrailingFrame()
		a.viewNeedsRefresh = true
		return a, nil

	case AnimationTickMsg:
		// SINGLE animation handler for ALL animations
		// The clock's goroutine increments frames - this just schedules next UI refresh
		// Only continues if animations are still active (prevents orphan tick chains)
		// Only trigger a full re-render when something on screen is actually animated.
		// When the UI is truly idle (no spinner, no streaming, no timers), the frame
		// is identical to the previous one — no reason to re-render at all.
		diffusionRevealAnimating := a.tickDiffusionReveal()
		wordPlaybackAnimating := a.wordPlayback.Active
		animatedContent := a.streamingMessage ||
			diffusionRevealAnimating ||
			a.isCompacting ||
			a.bgProcessTickActive ||
			a.voiceRecording ||
			a.voiceTranscribing ||
			wordPlaybackAnimating
		// When the debug screen is visible, it takes over rendering and is
		// purely static — it has no animations. Skip viewNeedsRefresh so
		// the SimCity render cache (lastRenderOutput) can skip re-renders
		// between animation ticks. The debug screen only needs to re-render
		// on user input or data changes (AddRequest/AddLog), which are
		// already handled by other message types that set viewNeedsRefresh.
		if animatedContent && !a.debugScreen.IsVisible() {
			a.viewNeedsRefresh = true

			// Streaming content can include animated/cached in-message regions such
			// as diffusion reveal, sub-agent progress, or live tool windows. Mark
			// the last message dirty on animation ticks so those regions advance.
			//
			// CANONICAL CADENCE RULE: this handler must never call
			// updateViewportIncremental() directly while a message is actively
			// streaming — that bypasses inputProtection's debounce and creates a
			// second, uncoordinated render cadence racing the chunk-arrival cadence
			// (updateStreamingMessageIncremental), which is what produced ±1 line
			// jitter/snapping during streaming. updateStreamingMessageIncremental
			// folds BOTH triggers (token chunks and animation ticks) through the
			// same inputProtection.debounceDelay gate, and outside of active
			// streaming it falls straight through to an immediate render — so
			// post-stream diffusion reveal / word playback still animate smoothly.
			if a.streamingMessage && len(a.messages) > 0 {
				a.messages[len(a.messages)-1].MarkDirty()
				a.updateStreamingMessageIncremental()
			}

			// Advance the diffusion denoising reveal each frame. This runs past
			// stream completion (the reveal holds its own clock subscription),
			// so it needs its own dirty-marking independent of streamingMessage.
			if diffusionRevealAnimating && a.diffusionReveal.msgIndex < len(a.messages) {
				a.messages[a.diffusionReveal.msgIndex].MarkDirty()
				a.updateStreamingMessageIncremental()
			}

			if a.wordPlayback.Active && a.advanceWordPlayback(time.Now()) {
				idx := a.wordPlayback.MessageIdx
				if idx >= 0 && idx < len(a.messages) {
					a.messages[idx].MarkDirty()
				}
				a.updateStreamingMessageIncremental()
			}
		}

		// Check if we have a pending debounced update that needs flushing
		if a.inputProtection.pendingUpdate && a.streamingInProgress {
			now := time.Now()
			timeSinceLastUpdate := now.Sub(a.inputProtection.lastUpdateTime)
			// If enough time has passed, flush the pending update
			if timeSinceLastUpdate >= a.inputProtection.debounceDelay {
				logDebug("[AnimationTick] Flushing pending update (elapsed: %v)", timeSinceLastUpdate)
				a.inputProtection.lastUpdateTime = now
				a.inputProtection.pendingUpdate = false
				a.updateStreamingMessageIncremental()
			}
		}

		// The tick chain is continued by the safety-net defer at the top of
		// Update() (TickIfIdle). Scheduling here too double-schedules: handler
		// cmd + defer cmd guarantees a tea.Batch on every animation frame.
		return a, nil

	case viewportPrewrappedMsg:
		// Background pre-wrap completed. A stale request must not clear the
		// in-flight state owned by a newer content/geometry target.
		current := a.applyViewportPrewrap(msg)
		// If the user hasn't scrolled away, snap to the real bottom now that the
		// accurate (post-wrap) line count is known. This corrects any position drift
		// that may have occurred while preWrappedLines were stale.
		if current && !a.userScrolledAway {
			a.msgViewport.GotoBottom()
		}
		a.viewNeedsRefresh = true // New pre-wrapped lines = new frame needed
		return a, nil

	case bgProcessTickMsg:
		// Periodic tick to keep the side panel timer + inline live terminal
		// blocks updated while background processes run. Fires even when the
		// agent is idle so npm servers, builds, etc. show live output.
		if a.sdk != nil && a.sdk.GetActiveBackgroundProcessCount() > 0 {
			// Background bash indicators live outside the agent streaming spinner
			// pipeline. Force a frame and side-panel rebuild on every bg tick so
			// the dock/sidebar wheel, elapsed time, and tails actually advance.
			a.viewNeedsRefresh = true
			a.sidePanelCache.valid = false

			// Keep the takeover detail viewer fresh while its command runs.
			if a.detailViewer != nil {
				a.detailViewer.Refresh()
			}
			// Refresh inline "terminal window" blocks by invalidating ONLY the
			// messages that hold a backgrounded bash result — the rest of the
			// transcript stays cached, avoiding the full-list flicker that
			// blanket invalidation used to cause. When such a block is on
			// screen we tick at ~10Hz for a smooth live feel; otherwise the
			// 1Hz cadence is enough to keep the side-panel timer moving.
			//
			// The fast cadence is now spent only while output is actually
			// moving. A live block whose process is quiet costs one cheap
			// signature check per tick instead of a full viewport rebuild.
			hasLiveTerminal, outputChanged := a.refreshLiveTerminals(false)
			tickInterval := 250 * time.Millisecond
			if hasLiveTerminal && outputChanged {
				// invalidateMessageCache already set viewportContentDirty for
				// each affected message; setting it again here is redundant.
				tickInterval = time.Second / 10
			}
			return a, tea.Tick(tickInterval, func(t time.Time) tea.Msg {
				return bgProcessTickMsg{}
			})
		}
		// No more active bg processes — do a final refresh so the terminal
		// blocks freeze with their exit code, then stop ticking.
		a.refreshLiveTerminals(true)
		a.bgProcessTickActive = false
		return a, nil

	case attachMonitorTickMsg:
		if a.attachScreen == nil {
			return a, nil
		}
		a.attachScreen.Refresh()
		a.viewNeedsRefresh = true
		return a, tea.Tick(attachMonitorRefreshInterval, func(time.Time) tea.Msg {
			return attachMonitorTickMsg{}
		})

	// Legacy message handlers - kept for compatibility but do nothing
	case SpinnerRefreshMsg, LoadingRefreshMsg:
		// Deprecated - use AnimationTickMsg
		return a, nil

	case drainQueueMsg:
		// Queue drain triggered by streaming chunk - queue already drained at top of Update
		// If still streaming, keep the tick loop alive for more drains
		// Animation is now driven by shared AnimationClock via AnimationTickMsg
		if a.streamingMessage {
			// Guarded: drainQueueMsg arrives far more often than every 50ms,
			// and an unguarded tick here started a new immortal chain per
			// message. scheduleAgentTick returns nil when one is already
			// pending.
			return a, a.scheduleAgentTick(50 * time.Millisecond)
		}
		// Workflow updates are now driven by AnimationTickMsg, no separate tick loop needed
		return a, nil

	case agentTickMsg:
		// Periodic tick to drain update queue during agent execution
		// Animation is now driven by shared AnimationClock via AnimationTickMsg
		if a.streamingMessage {
			// The pending flag was cleared by the tick that delivered this
			// message, so this continues the single chain rather than adding
			// a parallel one.
			return a, a.scheduleAgentTick(100 * time.Millisecond)
		}
		return a, nil

	case streamChunkMsg:
		return a.handleStreamChunk(msg)

	case StreamChunkMsg:
		// Public variant used by external callers (e.g. stress-test binary).
		return a.handleStreamChunk(streamChunkMsg{content: msg.Delta})

	case streamDoneMsg:
		return a.handleStreamDone(msg)

	case streamErrorMsg:
		return a.handleStreamError(msg)

	case commands.LoopInjectPromptMsg:
		// Inject the loop prompt as if the user typed it and pressed Enter.
		a.textInput.SetValue(msg.Prompt)
		a.cmdAutocomplete.Hide()
		a.mentionAutocomplete.Hide()
		return a, a.handleSendMessage()

	case commands.LoopUsageMsg:
		a.addNotification("info", i18n.T("residual.update.loop_usage"))
		return a, nil

	case commands.LoopErrorMsg:
		a.addNotification("error", msg.Error)
		return a, nil

	case commands.GoalCommandMsg:
		// /goal command — set, clear, or show the active goal stop-hook.
		// Always injects a visible message into chat so the user sees the effect.
		var prompt string
		if a.sdk == nil || a.sdk.hooksManager == nil {
			prompt = "Goal tracking is not available: SDK not initialized."
		} else if gh := a.sdk.hooksManager.GetGoalHook(); gh == nil {
			prompt = "Goal tracking is not available: hook not registered in this session."
		} else {
			switch msg.Action {
			case "clear":
				gh.Clear()
				prompt = "Goal cleared. I'll stop looping."
			case "status":
				g := gh.GetGoal()
				if g == nil || g.State == "cleared" {
					prompt = "No goal is currently set. Use `/goal <condition>` to set one."
				} else {
					prompt = fmt.Sprintf("Active goal: %q  [state: %s]", g.Condition, g.State)
					if g.LastReason != "" {
						prompt += fmt.Sprintf("\nLast evaluation: %s", g.LastReason)
					}
				}
			default: // "set"
				if err := gh.SetGoal(msg.Condition); err != nil {
					prompt = fmt.Sprintf("Could not set goal: %s", err.Error())
				} else {
					prompt = fmt.Sprintf("Goal set: %q\n\nI'll keep working until this condition is met. Use `/goal clear` to stop.\n\nWhat should I do first to work toward this goal?", msg.Condition)
				}
			}
		}
		return a, func() tea.Msg { return commands.LoopInjectPromptMsg{Prompt: prompt} }

	case commands.ProtectCommandMsg:
		// /protect — configure the protected-branch guardrail for this workspace.
		a.handleProtectCommand(msg)
		return a, nil

	case commands.ClearConversationMsg:
		// /clear — cancel the current turn and start a fresh conversation.
		return a.handleClearConversation()

	case commands.OpenContextTabMsg:
		// /context (/audit) — open the debug screen on the Context tab, a live
		// breakdown of the most recent assembled request incl. hidden/ephemeral.
		if a.debugScreen != nil {
			a.debugScreen.ShowContextTab()
			if !a.debugScreen.contextAuditRequestAvailable() {
				a.addNotification("info", i18n.T("residual.update.no_context_request"))
			}
		}
		return a, nil

	case commands.WorkspaceHandoffMsg:
		return a.handleWorkspaceHandoff(msg)

	case commands.WorkspaceErrorMsg:
		a.addNotification("error", i18n.T("residual.update.workspace_error", msg.Err))
		return a, nil

	// cronPromptInjectMsg is handled in the drain loop above (it arrives via
	// updateQueue/sendToRuntime). It will never reach this outer switch.

	// ── Plan Mode ─────────────────────────────────────────────────────────────
	// planEnterMsg and planApprovalRequestMsg are handled in the drain loop
	// above (they arrive via updateQueue/sendToRuntime).  They will never reach
	// this outer switch, but keep stubs to satisfy future type-switches if any.
	//
	// planApprovedMsg and planRejectedMsg come from planQuestionModal callbacks
	// (via pendingAnimCmd 	 tea.Cmd), so they DO reach this outer switch.

	case planApprovedMsg:
		return a.handlePlanApproved(msg)

	case editorFinishedMsg:
		// The external editor (Ctrl+E) has exited; read the edited prompt back
		// into the input and clean up the temp file.
		return a.handleEditorFinished(msg)

	case planRejectedMsg:
		return a.handlePlanRejected(msg)

	case tokenUpdateMsg:

		// Real-time token update - update the token count immediately
		if msg.inputTokens > 0 {
			wasEstimate := a.tokenCountIsEstimate

			a.tokenCount = msg.inputTokens
			a.tokenCountIsEstimate = false
			a.lastRealTokenCount = msg.inputTokens
			a.sidePanelCache.valid = false // Invalidate cache to force UI update

			logDebug("[APP] Updated tokenCount=%d (source: tokenUpdateMsg real-time, wasEstimate=%v)", a.tokenCount, wasEstimate)
			logDebug("Token update: input=%d, output=%d, final=%v (replaced estimate)", msg.inputTokens, msg.outputTokens, msg.isFinal)
		}
		// Update context window from the live SDK value when provided.
		// This keeps the TUI side panel denominator in sync with what the agent
		// actually sees (e.g. after a model switch mid-session) without needing
		// a separate GetModelContextWindow() call.
		if msg.contextWindow > 0 && msg.contextWindow != a.modelContextWindow {
			logDebug("[APP] modelContextWindow updated from tokenUpdateMsg: %d → %d", a.modelContextWindow, msg.contextWindow)
			a.modelContextWindow = msg.contextWindow
			a.sidePanelCache.valid = false
		}
		if msg.autoCompactThreshold > 0 && msg.autoCompactThreshold != a.autoCompactThresholdTokens {
			a.autoCompactThresholdTokens = msg.autoCompactThreshold
			a.sidePanelCache.valid = false
		}
		// If we're still streaming, continue waiting for more chunks
		if a.streamingMessage {
			return a, nil
		}
		return a, nil

	case lastMessageCompletionUpdatedMsg:
		if msg.err != nil {
			logDebug("[APP] Failed to update message completion metadata for %s: %v", msg.convID, msg.err)
		}
		return a, nil

	case conversationTokenCountMsg:
		if msg.err != nil {
			logDebug("[APP-TOKENS] Failed to resume conversation %s: %v", msg.convID, msg.err)
			return a, nil
		}
		if msg.convID != a.currentConvID {
			return a, nil
		}
		if a.streamingMessage || a.streamingInProgress {
			logDebug("[APP-TOKENS] Skipping token refresh for %s while streaming is active", msg.convID)
			return a, nil
		}
		if msg.currentContextSize <= 0 {
			logDebug("[APP-TOKENS] WARNING: CurrentContextSize=0, tokenCount unchanged at %d", a.tokenCount)
			return a, nil
		}

		prevTokenCount := a.tokenCount
		a.tokenCount = msg.currentContextSize
		a.lastRealTokenCount = msg.currentContextSize
		a.tokenCountIsEstimate = false
		a.sidePanelCache.valid = false
		logDebug("[APP-TOKENS] Updated tokenCount: prev=%d new=%d lastReal=%d (source: CurrentContextSize)",
			prevTokenCount, a.tokenCount, a.lastRealTokenCount)
		return a, nil

	case errorLineageLookupResultMsg:
		if msg.err != nil {
			logDebug("[Lineage] LookupErrorLineage failed for %s: %v", msg.errorID, msg.err)
			return a, nil
		}
		if msg.convID != a.currentConvID || msg.report == nil {
			return a, nil
		}

		lineageMsg := a.assistantMessageWithLineageErrorID(msg.errorID)
		if lineageMsg == nil || lineageMsg.ErrorLineage == nil {
			return a, nil
		}

		lineageMsg.ErrorLineage.Report = msg.report
		if lineageMsg.ErrorLineage.TraceID == "" && msg.report.TraceID != "" {
			lineageMsg.ErrorLineage.TraceID = msg.report.TraceID
		}

		// This is the only mutation in the package that targets a message
		// other than the last one, so it owns its own invalidation.
		// updateViewportContent() only marks the last message dirty.
		lineageMsg.MarkDirty()

		wasAtBottom := a.msgViewport.AtBottom()
		a.invalidateViewportCache()
		a.updateViewportContent()
		if wasAtBottom {
			a.msgViewport.GotoBottom()
		}
		return a, nil

	case flushPendingUpdateMsg:
		// Flush any pending debounced updates
		if a.inputProtection.pendingUpdate && a.streamingInProgress {
			logDebug("[flushPendingUpdateMsg] Flushing pending streaming update")
			a.inputProtection.lastUpdateTime = time.Time{}
			a.inputProtection.pendingUpdate = false
			a.updateStreamingMessageIncremental()
		}
		return a, nil

	case permissionApprovalMsg:
		a.enqueueApprovalRequest(msg.request)
		return a, nil

	case permissionApprovalResolvedMsg:
		a.removeApprovalRequest(msg.requestID, msg.outcome)
		return a, nil

	case questionRequestMsg:
		a.enqueueQuestionRequest(msg.request)
		return a, nil

	case questionResolvedMsg:
		a.removeQuestionRequest(msg.requestID, msg.timedOut)
		return a, nil

	case vaultUnlockRequestMsg:
		a.enqueueVaultUnlockRequest(msg.request)
		return a, nil

	case vaultUnlockResolvedMsg:
		a.removeVaultUnlockRequest(msg.requestID)
		return a, nil

	case notificationMsg:
		a.addNotification(msg.level, msg.message)
		return a, nil

	case vaultSubcommandResultMsg:
		// Surface /vault subcommand output both as a transient notification and
		// as a durable system message in the transcript so it isn't lost.
		a.addNotification(msg.level, firstLine(msg.text))
		a.messages = append(a.messages, Message{
			Role:      "system",
			Content:   "🔐 " + msg.text,
			Timestamp: time.Now(),
		})
		a.invalidateViewportCache()
		a.updateViewportContent()
		a.msgViewport.GotoBottom()
		a.viewNeedsRefresh = true
		return a, nil

	case commands.OpenConfigUIMsg:
		// Config slash commands (/profile, /agents, /prompt, /compaction,
		// /providers) emit this to open an overlay switcher or Settings section
		// (or apply a direct-arg change) without leaving chat.
		a.handleOpenConfigUI(msg)
		return a, nil

	case settings.VoiceRuntimeActionMsg:
		return a, a.handleVoiceRuntimeAction(msg)

	case voiceRuntimeResultMsg:
		return a.handleVoiceRuntimeResult(msg)

	case settings.TelemetryConsentRequestMsg:
		a.showTelemetryConsentModal()
		return a, nil

	// ── Swarm terminal chat (WorkspaceHub) ─────────────────────────────────

	case swarmChatInboundMsg:
		// Show incoming peer message inline in the current chat view
		from := msg.From
		if from == "" {
			from = i18n.T("residual.update.peer")
		}
		a.messages = append(a.messages, Message{
			Role:      "system",
			Content:   fmt.Sprintf("📡 **%s**: %s", from, msg.Content),
			Timestamp: time.Now(),
		})
		a.invalidateViewportCache()
		a.updateViewportContent()
		a.msgViewport.GotoBottom()
		a.viewNeedsRefresh = true
		return a, nil

	case swarmChatPeerJoinedMsg:
		if a.hub != nil {
			a.swarmChatPeers = a.hub.GetPeers()
		}
		a.addNotification("info", i18n.T("residual.update.swarm_joined", msg.Handle))
		a.viewNeedsRefresh = true
		return a, nil

	case swarmChatPeerLeftMsg:
		if a.hub != nil {
			a.swarmChatPeers = a.hub.GetPeers()
		}
		a.addNotification("info", i18n.T("residual.update.swarm_left", msg.Handle))
		a.viewNeedsRefresh = true
		return a, nil

	case commands.SwarmChatOpenMsg:
		// No separate screen — just remind the user how to use peer chat
		a.addNotification("info", i18n.T("residual.update.swarm_chat_hint"))
		a.viewNeedsRefresh = true
		return a, nil

	// TODO: implement doctor command
	// case commands.DoctorResultMsg:
	// 	// Add doctor output as a system message (or special message)
	// 	// For now, just add it as an assistant message so it renders nicely
	// 	a.messages = append(a.messages, Message{
	// 		Role:      "assistant",
	// 		Content:   msg.Output,
	// 		Timestamp: time.Now(),
	// 	})
	// 	a.updateViewportContent()
	// 	a.msgViewport.GotoBottom()
	// 	return a, nil

	case MCPPromptResultMsg:
		// Handle MCP prompt execution result
		if msg.Error != nil {
			logDebug("[MCP] Prompt execution failed: %v", msg.Error)
			a.addNotification("error", i18n.T("residual.update.mcp_prompt_error", msg.Error))
			return a, nil
		}

		if msg.Result == nil || len(msg.Result.Messages) == 0 {
			logDebug("[MCP] Prompt returned no messages")
			a.addNotification("info", i18n.T("residual.update.mcp_no_content", msg.ServerName, msg.PromptName))
			return a, nil
		}

		// Build the content from prompt messages
		var contentBuilder strings.Builder
		for _, pmsg := range msg.Result.Messages {
			if pmsg.Content.Type == "text" && pmsg.Content.Text != "" {
				if contentBuilder.Len() > 0 {
					contentBuilder.WriteString("\n\n")
				}
				contentBuilder.WriteString(pmsg.Content.Text)
			}
		}

		promptContent := contentBuilder.String()
		if promptContent == "" {
			a.addNotification("info", i18n.T("residual.update.mcp_empty_content", msg.ServerName, msg.PromptName))
			return a, nil
		}

		logDebug("[MCP] Prompt '%s:%s' returned %d characters", msg.ServerName, msg.PromptName, len(promptContent))

		// Insert the prompt content into the input and trigger send
		a.textInput.SetValue(promptContent)
		return a, a.handleSendMessage()

	case commands.ThinkingToggleMsg:
		return a.handleThinkingToggle(msg)
	case commands.OpenSkillPickerMsg:
		if a.sdk != nil && a.sdk.skillsManager != nil && a.sdk.skillsManager.IsInitialized() {
			a.skillPicker.SetLoader(a.sdk.skillsManager.GetLoader())
			a.skillPicker.Show(a.sdk.skillsManager.GetAllSkills(), a.sdk.skillsManager.GetActiveSkills())
		}
		return a, nil

	case commands.ThinkingEnableMsg:
		return a.handleThinkingEnable(msg)

	case commands.ThinkingDisableMsg:
		return a.handleThinkingDisable(msg)

	case commands.ThinkingErrorMsg:
		// Show error from thinking command
		logDebug("Thinking command error: %s", msg.Error)
		return a, nil

	case commands.MemStatsMsg:
		a.addNotification("info", msg.Text)
		return a, nil

	// Voice message handlers
	case VoiceToggleMsg:
		return a.handleVoiceToggle()
	case commands.VoiceToggleMsg:
		// Handle /voice command - same as keyboard toggle
		return a.handleVoiceToggle()
	case commands.VoiceAutoToggleMsg:
		a.voiceAutoRecord = !a.voiceAutoRecord
		if a.voiceAutoRecord {
			a.addNotification("info", i18n.T("residual.update.voice_auto_enabled"))
		} else {
			a.addNotification("info", i18n.T("residual.update.voice_auto_disabled"))
		}
		return a, nil
	case VoiceStateChangeMsg:
		return a.handleVoiceStateChange(msg)
	case VoiceInterimMsg:
		return a.handleVoiceInterim(msg)
	case VoiceFinalMsg:
		return a.handleVoiceFinal(msg)
	case VoiceTranscriptMsg:
		// Re-chain the listener; the raw transcript is handled via VoiceFinalMsg / VoiceInterimMsg.
		return a, listenForVoiceEvent(a.voiceEventCh)
	case VoiceCompleteMsg:
		return a.handleVoiceComplete(msg)
	case VoiceErrorMsg:
		return a.handleVoiceError(msg)

	case voiceTickMsg:
		if a.voiceRecording {
			a.voicePulseTick = !a.voicePulseTick
			return a, voiceTickCmd()
		}
		// Recording stopped — stop the tick loop
		a.voicePulseActive = false
		a.voicePulseTick = false
		return a, nil

	case voiceAutoRecordMsg:
		if a.voiceAutoRecord && !a.voiceRecording && !a.voiceTranscribing {
			return a.handleVoiceToggle()
		}
		return a, nil

	// Auto-update message handling
	case UpdateCheckMsg:
		return a.handleUpdateCheck(msg)
	case AnthropicModelsRefreshMsg:
		return a.handleAnthropicModelsRefresh(msg)
	case CodexModelsRefreshMsg:
		return a.handleCodexModelsRefresh(msg)
	case UpdateDownloadProgressMsg:
		return a.handleUpdateDownloadProgress(msg)
	case UpdateDownloadCompleteMsg:
		return a.handleUpdateDownloadComplete(msg)
	case UpdateApplyMsg:
		return a.handleUpdateApply(msg)

	case commands.ModeSwitchMsg:
		return a.handleModeSwitch(msg)

	case commands.ModeShowMsg:
		return a.handleModeShow()

	case commands.ModeErrorMsg:
		// Show mode error
		logDebug("[MODE] ModeErrorMsg: %s", msg.Error)
		a.addNotification("error", msg.Error)
		return a, nil

	case commands.AutoModeToggleMsg:
		return a.handleAutoModeToggle()
	case commands.AutoModeSetMsg:
		return a.handleAutoModeSet(msg.Enabled)
	case commands.AutoModeStatusMsg:
		return a.handleAutoModeStatus()
	case commands.AutoModeErrorMsg:
		a.addNotification("error", msg.Error)
		return a, nil

	// Theme command handlers
	case commands.ThemeSelectedMsg:
		return a.handleThemeSelected(msg)
	case commands.ThemeMenuOpenMsg:
		// Theme menu opened - no additional action needed
		return a, nil
	case commands.ThemeCancelMsg:
		// User cancelled theme selection - add notification
		a.addNotification("info", i18n.T("residual.update.theme_cancelled"))
		return a, nil
	case commands.ThemeErrorMsg:
		logDebug("[THEME] ThemeErrorMsg: %s", msg.Error)
		a.addNotification("error", msg.Error)
		return a, nil

	case commands.CommandsBrowserMsg:
		// Handle commands browser results
		if msg.Error != "" {
			a.addNotification("error", msg.Error)
		} else if msg.Result != "" {
			// For non-interactive mode, show the result
			// Display as a system-style message
			logDebug("[COMMANDS] %s", msg.Result)
			// Could also add to messages or show in a panel
			// For now, just log it (interactive mode handles its own display)
		}
		return a, nil

	case commands.BugReportMsg:
		return a.handleBugReport(msg)
	case commands.BugCancelMsg:
		return a, nil

	// ── Daemon attach/detach ──────────────────────────────────────
	case commands.AttachRequestMsg:
		cmd := a.startAttach(msg)
		a.addNotification("info", i18n.T("residual.update.attaching", msg.Addr))
		return a, cmd

	case commands.AttachConnectedMsg:
		if a.sseState != nil {
			a.sseState.connected = true
			a.sseState.lastEvent = time.Now()
		}
		a.addNotification("success", i18n.T("residual.update.daemon_connected", msg.Addr))
		return a, nil

	case commands.AttachErrorMsg:
		a.addNotification("error", msg.Error)
		return a, nil

	case commands.AttachDetachedMsg:
		a.addNotification("info", i18n.T("residual.update.detached", msg.Addr))
		if a.sseState != nil {
			a.sseState.cancel()
			a.sseState = nil
		}
		return a, nil

	case commands.DetachRequestMsg:
		if a.sseState == nil {
			a.addNotification("info", i18n.T("residual.update.not_attached"))
			return a, nil
		}
		addr := a.sseState.addr
		a.sseState.cancel()
		a.sseState = nil
		a.addNotification("info", i18n.T("residual.update.detached_daemon_running", addr))
		return a, nil

	case sseEventMsg:
		if a.sseState != nil {
			a.sseState.eventCount++
			a.sseState.lastEvent = time.Now()
			a.handleDaemonSSEEvent(msg)
		}
		return a, a.listenSSECmd()

	case commands.CompactRequestMsg:
		return a.handleCompactRequest(msg)

	case commands.CompactCompletedMsg:
		return a.handleCompactCompleted(msg)

	case commands.CompactErrorMsg:
		return a.handleCompactError(msg)

	case commands.ReindexRequestMsg:
		return a.handleReindexRequest(msg)

	case commands.ReindexCompletedMsg:
		return a.handleReindexCompleted(msg)

	case commands.ReindexErrorMsg:
		return a.handleReindexError(msg)

	case commands.RefreshPreviewsRequestMsg:
		return a.handleRefreshPreviewsRequest(msg)

	case commands.RefreshPreviewsCompletedMsg:
		return a.handleRefreshPreviewsCompleted(msg)

	case commands.ClaudeImportResultMsg:
		return a.handleClaudeImportResult(msg)

	case commands.ClaudeExportRequestMsg:
		return a.handleClaudeExportRequest()

	case commands.ClaudeExportResultMsg:
		return a.handleClaudeExportResult(msg)

	// Micro-compaction toggle handlers
	case commands.MicroCompactToggleMsg:
		return a.handleMicroCompactToggle(msg)

	case commands.MicroCompactStatusRequestMsg:
		return a.handleMicroCompactStatusRequest()

	// Code mode toggle handlers
	case commands.CodeModeToggleMsg:
		return a.handleCodeModeToggle(msg)

	case commands.CodeModeStatusRequestMsg:
		return a.handleCodeModeStatusRequest()

	case commands.CodeModeUpdatedMsg:
		// Code mode was already toggled live in handleCodeModeToggle via
		// SetCodeMode (tools hidden/unhidden in-place on the shared registry,
		// which the agent reads per-request — no reinit needed). This message
		// is purely a UI refresh so the side panel reflects the new state.
		a.sidePanelCache.valid = false
		return a, nil

	case tea.WindowSizeMsg:
		a.width = msg.Width
		a.height = msg.Height
		if a.attachScreen != nil {
			a.attachScreen.SetSize(a.width, a.height)
			a.viewNeedsRefresh = true
			return a, nil
		}
		a.syncSidePanelPreference()
		layout := a.applyChatAreaLayout(true)
		// CRITICAL: Immediately rebuild viewport content with the effective chat
		// width so messages/tools are re-wrapped consistently with side panel state.
		a.updateViewportContent()

		logDebug("[Resize] Viewport: %dx%d (chat: %d, total: %dx%d, sidePanel: %v)", layout.ViewportWidth, layout.ViewportHeight, layout.ChatWidth, a.width, a.height, layout.ShowSidePanel)

		// Update ChatScreen if active
		if a.chatScreen != nil && a.screen == ScreenNewChatUI {
			a.chatScreen.Update(msg)
		}

		logDebug("WindowSizeMsg: %dx%d", a.width, a.height)

		// If the home-screen burn animation is active and the terminal was resized,
		// restart it at the new dimensions so the canvas matches.
		if !a.homeTitleDone && a.homeTitleAnim != nil &&
			(a.width != a.homeBurnW || a.height != a.homeBurnH) {
			if cmd := a.startHomeTitleAnim(); cmd != nil {
				return a, cmd
			}
		}
		return a, nil

	case HooksChatResponseMsg:
		// Hooks assistant has responded - just trigger a re-render
		logDebug("Hooks chat response received")
		return a, nil

	case AgentsChatResponseMsg:
		// Agents assistant has responded - just trigger a re-render
		logDebug("Agents chat response received")
		return a, nil

	case bootTickMsg:
		if !a.bootComplete {
			a.bootFrame++
			midY := a.height / 2
			if a.bootFrame >= (midY*2)+4 {
				a.bootComplete = true
				a.viewNeedsRefresh = true
				// Boot scanlines complete — launch TTE intro animation.
				return a, a.startIntro()
			}
			return a, tea.Tick(time.Millisecond*8, func(t time.Time) tea.Msg {
				return bootTickMsg{}
			})
		}
		return a, nil
	case introTickMsg:
		return a.handleIntroTick()

	case glowTickMsg:
		return a.handleGlowTick()

	case homeTitleTickMsg:
		return a.handleHomeTitleTick()

	case menuTickMsg:
		if !a.menuReady {
			a.menuFrame++
			a.viewNeedsRefresh = true
			if a.menuFrame >= 30 { // 30 frames for menu reveal
				a.menuReady = true
				// Start title burn animation now that the menu is ready.
				titleCmd := a.startHomeTitleAnim()
				// Subscribe animation clock for cursor blink on home screen.
				if a.animationClock != nil {
					return a, tea.Batch(a.animationClock.Subscribe(), titleCmd)
				}
				return a, titleCmd
			}
			return a, tea.Tick(time.Millisecond*16, func(t time.Time) tea.Msg {
				return menuTickMsg{}
			})
		}
		return a, nil

	case tea.PasteMsg:
		a.viewNeedsRefresh = true
		return a.handlePaste(msg)

	case tea.PasteStartMsg:
		logDebug("*** App received PasteStartMsg ***")
		return a, nil

	case tea.PasteEndMsg:
		logDebug("*** App received PasteEndMsg ***")
		return a, nil

	case gitpanel.StatusRefreshedMsg:
		// Route git panel status messages to the gitPanel component
		if a.gitPanel != nil {
			updatedModel, cmd := a.gitPanel.Update(msg)
			a.gitPanel = &updatedModel
			return a, cmd
		}
		return a, nil

	case gitpanel.OperationCompleteMsg:
		// Route git operation completion messages to the gitPanel component
		if a.gitPanel != nil {
			updatedModel, cmd := a.gitPanel.Update(msg)
			a.gitPanel = &updatedModel
			return a, cmd
		}
		return a, nil

	case gitpanel.DiffLoadedMsg:
		// Route diff loaded messages to the gitPanel component
		if a.gitPanel != nil {
			updatedModel, cmd := a.gitPanel.Update(msg)
			a.gitPanel = &updatedModel
			return a, cmd
		}
		return a, nil

	case bashExecutionMsg:
		// Handle bash command execution result
		if len(a.messages) > 0 {
			// Update the last message with results
			lastIdx := len(a.messages) - 1
			a.messages[lastIdx].IsLoading = false
			a.streamingMessage = false
			a.streamingInProgress = false                                   // ← Reset streaming optimization state
			a.setUserScrolledAway(false, "bash_execution_message_complete") // ← Reset scroll tracking

			a.animationClock.SetStreaming(false) // Switch to idle animation
			a.loadingIndicator.Stop(a.animationClock)
			a.spinner.Stop(a.animationClock)

			// CRITICAL FIX: Invalidate caches before modifying message content
			a.invalidateViewportCache()

			if msg.err != nil {
				a.messages[lastIdx].Content = i18n.T("residual.update.command_error", msg.err)
			} else if msg.result != nil {
				a.messages[lastIdx].BashResult = msg.result
				a.messages[lastIdx].Content = "" // Clear content, use BashResult for rendering
			}

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
		return a, nil

	case commands.ModelReadmeMsg:
		if a.activeCommand != nil && a.activeCommand.IsInteractive() {
			updatedCmd, cmd := a.activeCommand.Update(msg)
			a.activeCommand = updatedCmd
			return a, cmd
		}
		if a.settingsManager != nil {
			return a, a.settingsManager.HandleMessage(msg, a.cmdRegistry)
		}
		return a, nil

	case commands.ReadmeLinkOpenMsg:
		if msg.Err != "" {
			a.addNotification("error", i18n.T("residual.update.open_link_failed", msg.Err))
		}
		return a, nil

	case tea.MouseWheelMsg:
		// Handle mouse wheel scrolling
		if a.screen == ScreenChats {
			a.handleConversationsScroll(msg)
			a.viewNeedsRefresh = true
			return a, nil
		}
		// Scroll chat viewport directly (MessageList.Update is a no-op for mouse)
		if a.screen == ScreenChat && !a.workspaceMode {
			switch msg.Mouse().Button {
			case tea.MouseWheelUp:
				a.msgViewport.ScrollUp(3)
			case tea.MouseWheelDown:
				a.msgViewport.ScrollDown(3)
			}
			a.viewNeedsRefresh = true
			// Track scroll-away state so auto-scroll resumes when back at bottom.
			// Always update regardless of streaming state so users can freely scroll.
			atBottom := a.msgViewport.AtBottom()
			if !atBottom {
				a.setUserScrolledAway(true, "mouse_wheel_scroll_away")
			} else {
				a.setUserScrolledAway(false, "mouse_wheel_at_bottom")
			}
			return a, nil
		}
	case tea.MouseClickMsg:
		// Start selection on left-click inside the chat viewport
		if a.screen != ScreenChat || a.workspaceMode {
			break
		}
		// Only handle left button; ignore when modals/pickers visible
		if msg.Mouse().Button != tea.MouseLeft {
			break
		}
		if a.modelSwitcher.IsVisible() || a.agentSwitcher.IsVisible() ||
			a.promptSwitcher.IsVisible() || a.profileSwitcher.IsVisible() ||
			a.skillPicker.IsVisible() || a.commandPalette.IsVisible() ||
			a.activeModal != nil || a.activeCommand != nil ||
			a.approvalModal != nil || a.questionModal != nil {
			break
		}
		layout := a.chatAreaLayout()
		if layout.ShowSidePanel && a.vaultChipWidth > 0 &&
			msg.Mouse().Y == 1 &&
			msg.Mouse().X >= a.width-a.vaultChipWidth-2 {
			a.openVaultControl()
			return a, nil
		}
		// Input-box region: a click at or below the input's first row selects /
		// positions within the input box rather than the message viewport.
		if a.textInput != nil && a.textInput.focused && msg.Mouse().Y >= a.inputOverlayY {
			if idx, ok := a.textInput.ScreenToIndex(msg.Mouse().X, msg.Mouse().Y); ok {
				// Detect double / triple click for word / line selection.
				streak := a.inputClickStreak
				now := time.Now()
				if now.Sub(a.inputLastClickTime) < 400*time.Millisecond &&
					abs(msg.Mouse().X-a.inputLastClickX) <= 1 &&
					msg.Mouse().Y == a.inputLastClickY {
					streak++
				} else {
					streak = 1
				}
				a.inputClickStreak = streak
				a.inputLastClickTime = now
				a.inputLastClickX = msg.Mouse().X
				a.inputLastClickY = msg.Mouse().Y

				switch {
				case msg.Mouse().Mod&tea.ModShift != 0:
					a.textInput.ExtendSelection(idx)
				case streak >= 3:
					a.textInput.SelectLineAt(idx)
				case streak == 2:
					a.textInput.SelectWordAt(idx)
				default:
					// Single click: clear any selection and place the cursor.
					a.textInput.ClearSelection()
					a.textInput.StartSelection(idx)
				}
				a.viewNeedsRefresh = true
			}
			break
		}
		// Message viewport selection: clicking the transcript clears any input
		// selection so the two regions don't show selections simultaneously.
		if a.textInput != nil {
			a.textInput.ClearSelection()
		}
		line, col, ok := a.msgViewport.ScreenToContent(msg.Mouse().X, msg.Mouse().Y)
		if ok {
			if msg.Mouse().Mod&tea.ModShift != 0 {
				// Shift+click: extend selection from anchor
				a.msgViewport.ExtendSelection(line, col)
			} else {
				// Normal click: start new selection
				a.msgViewport.StartSelection(line, col)
			}
			a.viewNeedsRefresh = true
		}

	case tea.MouseMotionMsg:
		// Update selection end during drag
		if a.screen != ScreenChat || a.workspaceMode {
			break
		}
		// Input-box drag takes priority while the input is actively selecting.
		if a.textInput != nil && a.textInput.IsSelecting() {
			if idx, ok := a.textInput.ScreenToIndex(msg.Mouse().X, msg.Mouse().Y); ok {
				a.textInput.UpdateSelectionEnd(idx)
				a.viewNeedsRefresh = true
			}
			break
		}
		if !a.msgViewport.IsSelecting() {
			break
		}
		// Use the CLAMPED mapping while dragging: if the cursor moves below the
		// last transcript row (into the separator/input area) the selection must
		// still extend to the last visible line instead of freezing several rows
		// short. Strict ScreenToContent is only for the initial click routing.
		line, col, ok := a.msgViewport.ScreenToContentClamped(msg.Mouse().X, msg.Mouse().Y)
		if ok {
			a.msgViewport.UpdateSelectionEnd(line, col)
			a.viewNeedsRefresh = true
		}

	case tea.MouseReleaseMsg:
		// Freeze selection on mouse release
		if a.screen != ScreenChat || a.workspaceMode {
			break
		}
		if a.textInput != nil && a.textInput.IsSelecting() {
			a.textInput.EndSelection()
			a.viewNeedsRefresh = true
			break
		}
		if a.msgViewport.IsSelecting() {
			// Apply the release position as the final selection end (clamped), so a
			// release that lands below the viewport still captures the last line.
			if line, col, ok := a.msgViewport.ScreenToContentClamped(msg.Mouse().X, msg.Mouse().Y); ok {
				a.msgViewport.UpdateSelectionEnd(line, col)
			}
			a.msgViewport.EndSelection()
			a.viewNeedsRefresh = true
		}

	case repaintMsg:
		// No-op: receiving this message is enough to trigger View().
		return a, nil

	case tea.KeyMsg:
		a.viewNeedsRefresh = true // Key input = new frame needed
		if !a.bootComplete {
			a.bootComplete = true
			return a, nil
		}
		// Note: typing is allowed during the TTE intro / ember glow — the animation
		// stays visible until the user actually sends a message.

		// CRITICAL: Handle mandatory update blocking screen first
		// This blocks ALL other key input until user updates or quits
		if a.updateMandatoryBlocked {
			switch msg.String() {
			case "u", "U":
				// Start update download
				if a.updater != nil && a.updateAvailable != nil && a.updateAvailable.Release != nil {
					a.updateDownloading = true
					a.updateMandatoryBlocked = false // Unblock UI to show download progress
					return a, a.downloadUpdateCmd()
				}
				return a, nil
			case "q", "Q", "ctrl+c", "esc":
				// Quit the application
				return a, tea.Quit
			default:
				// Ignore all other keys during mandatory update
				return a, nil
			}
		}

		// CRITICAL: If debug screen is visible, route ALL keys to it first
		if a.debugScreen.IsVisible() {
			cmd := a.debugScreen.Update(msg)
			return a, cmd
		}

		return a.handleKey(msg)
	}

	// Forward unhandled messages to the settings manager when on the settings tab.
	// This is needed for async OAuth auth messages (authURLReadyMsg, authOpenAIFlowMsg,
	// authGeminiFlowMsg, authCodeSuccessMsg, authErrorMsg, etc.) that are produced by
	// background cmds started from AuthSettings and need to be routed back to HandleMsg.
	if a.settingsManager != nil {
		if cmd := a.settingsManager.HandleMessage(msg, a.cmdRegistry); cmd != nil {
			return a, cmd
		}
	}

	// Check for pending auto-submit from new chat modal
	if a.pendingAutoSubmit {
		a.pendingAutoSubmit = false
		if a.screen == ScreenChat && strings.TrimSpace(a.textInput.GetSubmitValue()) != "" {
			return a, a.handleSendMessage()
		}
	}

	return a, nil
}

// ensureLastMessageIsAssistant ensures the last message in the array is an
// assistant message.  During streaming, injected user messages can leave the
// array ending with a "user" message, causing subsequent tool-call / content
// updates to be silently dropped.  This helper self-heals by inserting a
// placeholder assistant message when needed.
func (a *App) ensureLastMessageIsAssistant() {
	if len(a.messages) == 0 || a.messages[len(a.messages)-1].Role == "assistant" {
		return
	}
	logDebug("[ensureLastMessageIsAssistant] Last message is %q, inserting assistant placeholder",
		a.messages[len(a.messages)-1].Role)
	a.messages = append(a.messages, Message{
		Role:      "assistant",
		Content:   "",
		Timestamp: time.Now(),
	})
}

// lastAssistantContent walks the SDK-provided message snapshot in reverse
// and returns the most recent assistant message's Content. Used as a
// fallback source when ExecuteMessage returns an empty response string —
// which happens for multi-turn tool-heavy flows where the final turn from
// the SDK's perspective is a tool_use message with no trailing text, but
// an earlier assistant turn in the same execution DID produce text that
// the SDK already persisted.
func lastAssistantContent(allMessages []*Message) string {
	for i := len(allMessages) - 1; i >= 0; i-- {
		m := allMessages[i]
		if m == nil || m.Role != "assistant" {
			continue
		}
		if c := strings.TrimSpace(m.GetContent()); c != "" {
			return m.GetContent()
		}
	}
	return ""
}

// backfillEmptyAssistantFromFinal repairs the final-response blind spot: when
// a provider answers without emitting incremental content events, the last
// assistant message carries no Content and no content-type OrderedBlock, so
// the UI renders an empty reply even though the SDK reported a valid payload.
// This mutates the last assistant message in place, adding a content block
// sourced from finalContent, and returns true when a backfill occurred.
//
// It intentionally does nothing when:
//   - the turn errored (error path owns its own content)
//   - finalContent is empty or whitespace
//   - the last message isn't an assistant (safety)
//   - the message already has Content or a non-empty content OrderedBlock
//     (incremental builds must not be clobbered)
func backfillEmptyAssistantFromFinal(messages []Message, finalContent string, hadError error) bool {
	if hadError != nil || strings.TrimSpace(finalContent) == "" {
		return false
	}
	if len(messages) == 0 || messages[len(messages)-1].Role != "assistant" {
		return false
	}
	lastMsg := &messages[len(messages)-1]
	for i := range lastMsg.OrderedBlocks {
		if lastMsg.OrderedBlocks[i].Type == "content" &&
			strings.TrimSpace(lastMsg.OrderedBlocks[i].GetContent()) != "" {
			return false
		}
	}
	if strings.TrimSpace(lastMsg.GetContent()) != "" {
		return false
	}
	lastMsg.Content = finalContent
	lastMsg.contentBuilder = nil
	maxSeq := 0
	for i := range lastMsg.OrderedBlocks {
		if lastMsg.OrderedBlocks[i].Sequence > maxSeq {
			maxSeq = lastMsg.OrderedBlocks[i].Sequence
		}
	}
	lastMsg.OrderedBlocks = append(lastMsg.OrderedBlocks, MessageBlock{
		Type:     "content",
		Content:  finalContent,
		Sequence: maxSeq + 1,
	})
	return true
}

// reconcileAssistantThinkingFromComplete applies the terminal assistant turn's
// authoritative thinking payload. It repairs missed or partial incremental
// updates and keeps the canonical field and latest ordered block in sync.
func reconcileAssistantThinkingFromComplete(messages []Message, finalThinking string, hadError error) bool {
	if hadError != nil || strings.TrimSpace(finalThinking) == "" || len(messages) == 0 {
		return false
	}

	lastMsg := &messages[len(messages)-1]
	if lastMsg.Role != "assistant" {
		return false
	}

	changed := lastMsg.GetThinking() != finalThinking
	lastMsg.Thinking = finalThinking
	lastMsg.thinkingBuilder = nil

	lastThinkingIdx := -1
	maxSeq := 0
	for i := range lastMsg.OrderedBlocks {
		if lastMsg.OrderedBlocks[i].Sequence > maxSeq {
			maxSeq = lastMsg.OrderedBlocks[i].Sequence
		}
		if lastMsg.OrderedBlocks[i].Type == "thinking" {
			lastThinkingIdx = i
		}
	}
	updateExisting := lastThinkingIdx >= 0
	if updateExisting {
		// A tool block after the last thinking block marks a turn boundary;
		// terminal thinking belongs to the new turn and must not overwrite the
		// earlier block.
		for i := lastThinkingIdx + 1; i < len(lastMsg.OrderedBlocks); i++ {
			blockType := lastMsg.OrderedBlocks[i].Type
			if blockType == "tool_call" || blockType == "tool_result" {
				updateExisting = false
				break
			}
		}
	}
	if updateExisting {
		block := &lastMsg.OrderedBlocks[lastThinkingIdx]
		if block.GetContent() != finalThinking {
			block.ResetContentBuilder()
			block.Content = finalThinking
			changed = true
		}
	} else {
		lastMsg.OrderedBlocks = append(lastMsg.OrderedBlocks, MessageBlock{
			Type:     "thinking",
			Content:  finalThinking,
			Sequence: maxSeq + 1,
		})
		changed = true
	}
	return changed
}

// finalizeAssistantFromComplete is the AssistantMessageUpdate handler path —
// the SDK's terminal turn-completion signal.
//
// It repairs two production-visible edge cases:
//   - Empty/tool-only/non-streaming turns: backfill a final content block.
//   - Prefix-only streaming turns: if the canonical streamed content is a prefix
//     of the final SDK content, append only the missing suffix to the last content
//     block. This makes the last answer visible without duplicating the prefix.
//
// It deliberately skips non-prefix replacements because those can race with
// in-flight deltas and clobber already-rendered content.
//
// Returns true on mutation.
func finalizeAssistantFromComplete(messages []Message, finalContent string, hadError error) bool {
	if backfillEmptyAssistantFromFinal(messages, finalContent, hadError) {
		return true
	}
	if hadError != nil || strings.TrimSpace(finalContent) == "" {
		return false
	}
	if len(messages) == 0 || messages[len(messages)-1].Role != "assistant" {
		return false
	}
	lastMsg := &messages[len(messages)-1]
	existing := lastMsg.GetContent()
	if existing == "" || existing == finalContent || !strings.HasPrefix(finalContent, existing) {
		return false
	}
	suffix := finalContent[len(existing):]
	if suffix == "" {
		return false
	}
	lastContentIdx := -1
	for i := range lastMsg.OrderedBlocks {
		if lastMsg.OrderedBlocks[i].Type == "content" {
			lastContentIdx = i
		}
	}
	if lastContentIdx < 0 {
		maxSeq := 0
		for i := range lastMsg.OrderedBlocks {
			if lastMsg.OrderedBlocks[i].Sequence > maxSeq {
				maxSeq = lastMsg.OrderedBlocks[i].Sequence
			}
		}
		lastMsg.OrderedBlocks = append(lastMsg.OrderedBlocks, MessageBlock{
			Type:     "content",
			Content:  finalContent,
			Sequence: maxSeq + 1,
		})
	} else {
		lastMsg.OrderedBlocks[lastContentIdx].AppendContent(suffix)
	}
	lastMsg.Content = finalContent
	lastMsg.contentBuilder = nil
	return true
}
