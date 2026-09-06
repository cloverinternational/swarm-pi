package chat

import (
	"context"
	"encoding/base64"
	"fmt"
	"runtime/debug"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/agent"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/conversation"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/observability"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/headless/automation/protocol"
)

type bashExecutionMsg struct {
	result *BashResult
	err    error
}

type automationImageMsg struct {
	text     string
	fileName string
	mimeType string
	content  []byte
	done     chan error
}

const maxAutomationImageBytes = 5 * 1024 * 1024

// forceRepaint returns a tea.Cmd that immediately delivers a no-op message so
// Bubble Tea schedules a View() call. Use this after in-place state mutations
// (e.g. modal updates) that don't otherwise produce a message.
func forceRepaint() tea.Cmd {
	return func() tea.Msg { return repaintMsg{} }
}

// repaintMsg is a no-op message whose sole purpose is to trigger a View() call.
type repaintMsg struct{}

// ReceiveAutomationImage implements the attached control-socket rich image input
// path used by Swarm Desktop Inspect. It validates on the control goroutine,
// then hands the actual UI/LLM send to the Bubble Tea update loop so app state
// is not mutated concurrently.
func (a *App) ReceiveAutomationImage(cmd protocol.SendImageCmd) error {
	if a == nil {
		return fmt.Errorf("TUI app is not ready")
	}
	if a.updateQueue == nil {
		return fmt.Errorf("TUI update queue is not ready")
	}
	mimeType := strings.ToLower(strings.TrimSpace(cmd.MimeType))
	if !strings.HasPrefix(mimeType, "image/") {
		return fmt.Errorf("only image attachments are supported")
	}
	encoded := strings.TrimSpace(cmd.Data)
	if strings.HasPrefix(encoded, "data:") {
		comma := strings.Index(encoded, ",")
		if comma < 0 {
			return fmt.Errorf("invalid data URL image payload")
		}
		encoded = encoded[comma+1:]
	}
	content, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return fmt.Errorf("decode image payload: %w", err)
	}
	if len(content) == 0 {
		return fmt.Errorf("image payload is empty")
	}
	if len(content) > maxAutomationImageBytes {
		return fmt.Errorf("image is too large: %.1f MB (max %.1f MB)", float64(len(content))/(1024*1024), float64(maxAutomationImageBytes)/(1024*1024))
	}
	fileName := strings.TrimSpace(cmd.FileName)
	if fileName == "" {
		fileName = "inspect-image"
	}
	text := strings.TrimSpace(cmd.Text)
	if text == "" {
		text = "Please analyze this image."
	}
	done := make(chan error, 1)
	msg := automationImageMsg{text: text, fileName: fileName, mimeType: mimeType, content: content, done: done}
	select {
	case a.updateQueue <- msg:
		a.wakeRuntimeAsync()
	case <-time.After(2 * time.Second):
		return fmt.Errorf("TUI input queue is full")
	}
	select {
	case err := <-done:
		return err
	case <-time.After(8 * time.Second):
		return fmt.Errorf("TUI did not accept image prompt in time")
	}
}

func (a *App) handleAutomationImageMessage(msg automationImageMsg) tea.Cmd {
	fail := func(err error) tea.Cmd {
		if err != nil {
			a.addNotification("error", fmt.Sprintf("Inspect image: %v", err))
		}
		if msg.done != nil {
			msg.done <- err
		}
		return nil
	}
	if a.textInput == nil {
		return fail(fmt.Errorf("input is not ready"))
	}
	if a.sseState != nil {
		return fail(fmt.Errorf("image prompts are unavailable while attached to a daemon session"))
	}
	if a.currentConvID != "" && a.bgManager != nil && a.bgManager.IsRunning(a.currentConvID) || a.streamingMessage || a.isCompacting {
		return fail(fmt.Errorf("agent is busy; wait for the current turn to finish"))
	}
	const maxAttachments = 5
	if len(a.currentAttachments) >= maxAttachments {
		return fail(fmt.Errorf("cannot add more than %d images", maxAttachments))
	}
	existing := strings.TrimSpace(a.textInput.GetSubmitValue())
	text := strings.TrimSpace(msg.text)
	if text == "" {
		text = "Please analyze this image."
	}
	if existing != "" {
		text = existing + "\n\n" + text
	}
	a.currentAttachments = append(a.currentAttachments, Attachment{
		FilePath: msg.fileName,
		FileName: msg.fileName,
		MimeType: msg.mimeType,
		Content:  msg.content,
		Size:     int64(len(msg.content)),
	})
	a.textInput.SetValue(text)
	a.addNotification("success", fmt.Sprintf("Inspect image attached: %s (%.1f KB)", msg.fileName, float64(len(msg.content))/1024))
	if msg.done != nil {
		msg.done <- nil
	}
	return a.handleSendMessage()
}

// handleBashCommand processes a bash command (starting with !)
func (a *App) handleBashCommand(command string) tea.Cmd {
	a.stopWordPlayback()
	// Add user message showing the command
	a.messages = append(a.messages, Message{
		Role:          "user",
		Content:       "!" + command,
		Timestamp:     time.Now(),
		IsBashCommand: true,
	})

	// Clear input
	a.textInput.SetValue("")

	// Add placeholder for result
	a.messages = append(a.messages, Message{
		Role:      "assistant",
		Content:   "",
		Timestamp: time.Now(),
		IsLoading: true,
	})

	// Update viewport
	a.updateViewportContent()
	a.msgViewport.GotoBottom()

	// Set loading state
	a.streamingMessage = true
	a.animationClock.SetStreaming(true) // Use fast animation while streaming
	if a.memoryWatcher != nil {
		a.memoryWatcher.RecordActivity()
	}
	a.loadingIndicator.Start(a.animationClock)

	// Execute command
	return a.executeBashCommand(command)
}

// executeBashCommand runs the bash command asynchronously
func (a *App) executeBashCommand(command string) tea.Cmd {
	return func() tea.Msg {
		// Get workspace root for execution directory
		workDir := ""
		if a.sdk != nil {
			workDir = a.sdk.WorkspaceRoot()
		}
		if workDir == "" {
			workDir = "." // Fallback to current directory
		}

		executor := NewBashExecutor(workDir)
		result, err := executor.Execute(context.Background(), command)

		return bashExecutionMsg{
			result: result,
			err:    err,
		}
	}
}

// handleHubChatMessage sends a peer-to-peer chat message via the WorkspaceHub.
// Syntax: $message          → broadcast to all peers
//
//	$@handle message  → direct message to a specific peer
func (a *App) handleHubChatMessage(raw string) tea.Cmd {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}

	// Parse optional "@handle" DM target
	to := ""
	content := raw
	if strings.HasPrefix(raw, "@") {
		parts := strings.SplitN(raw[1:], " ", 2)
		if len(parts) == 2 && strings.TrimSpace(parts[1]) != "" {
			to = strings.TrimSpace(parts[0])
			content = strings.TrimSpace(parts[1])
		}
	}

	// Show the sent message inline in the chat
	label := "→ all"
	if to != "" {
		label = "→ " + to
	}
	myHandle := a.a2aHandle
	if myHandle == "" {
		myHandle = "me"
	}
	a.messages = append(a.messages, Message{
		Role:      "user",
		Content:   fmt.Sprintf("📡 [%s] %s: %s", label, myHandle, content),
		Timestamp: time.Now(),
	})

	// Clear input
	a.textInput.SetValue("")
	a.pendingNavIdx = 0

	// Update viewport
	a.invalidateViewportCache()
	a.updateViewportContent()
	a.msgViewport.GotoBottom()
	a.viewNeedsRefresh = true

	// Send via hub in background (nil-safe)
	if a.hub == nil {
		a.addNotification("warn", "swarm: no hub connected (use --workspace to enable)")
		return nil
	}
	hub := a.hub
	finalTo := to
	finalContent := content
	return func() tea.Msg {
		hub.SendChat(finalTo, finalContent)
		return nil
	}
}

// forceSendMessage is the "force message" path bound to Ctrl+Enter. Unlike
// plain Enter — which QUEUES the input behind the agent's current turn while it
// is busy (see the agentBusy branch in handleSendMessage) — Ctrl+Enter
// interrupts the in-flight turn and delivers the message right now.
//
// Two things happen, in order:
//  1. INTERRUPT: if an agent is running for the current conversation (or a
//     stream is in progress) it is cancelled, exactly like the Ctrl+C stop
//     path (app_workspace.go / chat_messages.go). Cancel() flips the agent's
//     status to Cancelled so bgManager.IsRunning() returns false, which makes
//     the subsequent handleSendMessage() take the LIVE-send path instead of the
//     busy-queue path.
//  2. PUT THE QUEUE IN: any messages the user had already queued (Enter while
//     busy) in a.pendingUserMessages are pulled out and prepended to the
//     current input, so nothing is left stranded behind the cancelled turn.
//     The combined text is then sent as a single fresh message.
func (a *App) forceSendMessage() tea.Cmd {
	a.prepareForceSend()
	// handleSendMessage now takes the live-send path because the agent is no
	// longer busy (Cancel set status=Cancelled; streamingMessage=false).
	return a.handleSendMessage()
}

// prepareForceSend performs the interrupt + queue-merge half of the force-send
// path. It is separated from forceSendMessage so it can be unit-tested without
// a live SDK. Returns true if a running turn was interrupted.
func (a *App) prepareForceSend() bool {
	// ── Step 1: interrupt any running turn ────────────────────────────────
	interrupted := false
	if a.currentConvID != "" && a.bgManager != nil && a.bgManager.IsRunning(a.currentConvID) {
		a.bgManager.Cancel(a.currentConvID)
		interrupted = true
	}
	if a.streamingMessage {
		interrupted = true
	}
	if interrupted {
		a.streamingMessage = false
		a.streamingInProgress = false
		a.loadingIndicator.Stop(a.animationClock)
		a.spinner.Stop(a.animationClock)
		a.setActivityPhase(ActivityPhaseIdle, "")
		logDebug("[prepareForceSend] Interrupted running turn for conv %s", a.currentConvID)
	}

	// ── Step 2: pull the queued messages in ahead of the current input ────
	a.pendingMsgMu.Lock()
	pending := a.pendingUserMessages
	a.pendingUserMessages = nil
	a.pendingMsgMu.Unlock()
	a.pendingNavIdx = 0 // reset pending-message navigation cursor

	if len(pending) > 0 {
		combined := strings.Join(pending, "\n")
		current := a.textInput.GetSubmitValue()
		if strings.TrimSpace(current) != "" {
			combined = combined + "\n" + current
		}
		a.textInput.SetValue(combined)
		logDebug("[prepareForceSend] Merged %d queued message(s) into force send", len(pending))
	}

	if interrupted {
		a.addNotification("info", "Agent interrupted — sending now")
	}
	return interrupted
}

// handleSendMessage sends the current message and starts streaming response
func (a *App) handleSendMessage() tea.Cmd {
	msgText := strings.TrimSpace(a.textInput.GetSubmitValue())
	if msgText == "" {
		return nil
	}

	// Dismiss the intro animation the moment the user sends their first message.
	// The glow / TTE effect stays visible until this point so the user can
	// compose their message while the animation plays behind them.
	if !a.introComplete {
		a.dismissIntro()
	}

	// ── Attached to a daemon: route input to the DAEMON, not the local agent ──
	// Checked FIRST — before the agent-busy queue and before any local-agent
	// path — so while attached NOTHING is ever delivered to the local agent
	// (not even queued). A true tmux-style attach means messages run ON the
	// daemon and its stream renders here; the user bubble is appended locally
	// for immediate feedback and the reply arrives via the SSE stream
	// (handleDaemonSSEEvent). Slash commands (/detach etc.) are handled earlier
	// in the key path. The `!` (local bash) and `$` (peer chat) prefixes remain
	// local escapes even while attached, so the user keeps a shell/peer-chat
	// without detaching.
	if a.sseState != nil && !strings.HasPrefix(msgText, "!") && !strings.HasPrefix(msgText, "$") {
		a.messages = append(a.messages, Message{
			Role:      "user",
			Content:   msgText,
			Timestamp: time.Now(),
		})
		a.textInput.SetValue("")
		a.pendingNavIdx = 0
		a.inputHistory.Add(msgText)
		a.updateViewport()
		a.msgViewport.GotoBottom()
		return a.sendToDaemonCmd(msgText)
	}

	// Check if the CURRENT conversation has an agent running.
	// Instead of blocking, queue the message so the agent gets it after its current turn.
	agentBusy := false
	if a.currentConvID != "" && a.bgManager != nil && a.bgManager.IsRunning(a.currentConvID) {
		agentBusy = true
	} else if a.streamingMessage {
		agentBusy = true
	} else if a.isCompacting {
		// Compaction is about to replace the active conversation with a new
		// compacted one. Executing now would run against the OLD conversation
		// and strand the turn there when handleCompactCompleted switches.
		// Queue instead — handleCompactCompleted drains the queue against the
		// compacted conversation.
		agentBusy = true
	} else if a.compactionBlocked {
		// Preserve input while the active context is known unsafe. A successful
		// manual/automatic compaction will release the queue.
		agentBusy = true
	}

	if agentBusy {
		// Queue the message for delivery between agent turns (injected mid-execution)
		a.pendingMsgMu.Lock()
		a.pendingUserMessages = append(a.pendingUserMessages, msgText)
		queueLen := len(a.pendingUserMessages)
		a.pendingMsgMu.Unlock()
		a.textInput.SetValue("")
		a.pendingNavIdx = 0 // reset pending-message navigation cursor
		logDebug("[handleSendMessage] Agent busy, queued message #%d: %s", queueLen, truncateForLog(msgText, 60))
		a.addNotification("info", fmt.Sprintf("Message queued (%d pending)", queueLen))
		return nil
	}

	// Add to input history (before handling special commands)
	a.inputHistory.Add(msgText)

	// Check for bash command prefix
	if strings.HasPrefix(msgText, "!") {
		return a.handleBashCommand(msgText[1:]) // Remove the ! prefix
	}

	// Check for swarm peer-chat prefix: $message or $@handle message
	if strings.HasPrefix(msgText, "$") {
		return a.handleHubChatMessage(msgText[1:]) // Remove the $ prefix
	}

	a.stopWordPlayback()

	// CRITICAL: Append mode message if mode changed since last send.
	// This is append-only (never replaces) to preserve cache prefix stability.
	// Mode switching via Shift+Tab doesn't touch history - only sending does.
	a.appendModeMessageIfChanged()

	// Add user message — skip when the agent is woken by a background-process
	// completion (suppressNextUserBubble=true). The trigger text goes to the API
	// but must not appear as a user bubble in the conversation view.
	suppressed := a.suppressNextUserBubble
	a.suppressNextUserBubble = false
	userMsg := Message{
		Role:        "user",
		Content:     msgText,
		Timestamp:   time.Now(),
		Attachments: make([]Attachment, len(a.currentAttachments)),
	}
	copy(userMsg.Attachments, a.currentAttachments)
	// Also convert attachments to Images for user message display
	// This ensures pasted images are rendered in the chat history
	for _, att := range a.currentAttachments {
		if att.MimeType != "" && strings.HasPrefix(att.MimeType, "image/") && len(att.Content) > 0 {
			userMsg.Images = append(userMsg.Images, ImageAttachment{
				Data:     att.Content,
				MimeType: att.MimeType,
			})
		}
	}
	if !suppressed {
		a.messages = append(a.messages, userMsg)
	}

	// Clear input and attachments immediately
	a.textInput.SetValue("")
	a.pendingNavIdx = 0 // reset pending-message navigation cursor
	a.currentAttachments = nil
	a.textInput.ClearAttachments() // Clear image attachments from input

	// Add placeholder assistant message for streaming
	a.messages = append(a.messages, Message{
		Role:      "assistant",
		Content:   "",
		Timestamp: time.Now(),
	})

	a.streamingInProgress = true

	// Update viewport with user message
	a.updateViewport()
	a.msgViewport.GotoBottom()

	// Execute message using Agent if SDK is available
	if a.sdk != nil {
		a.syncAutoCompactionConfig()
		logDebug("Executing message with Agent using model: %s", a.currentModel)

		// Ensure we have a conversation ID
		if a.currentConvID == "" {
			// Create new conversation with branch if available
			ctx := context.Background()
			branch := ""
			if a.activeConv != nil {
				branch = a.activeConv.Branch
			}
			conv, err := a.sdk.CreateConversationWithBranch(ctx, "chat", branch)
			if err != nil {
				logDebug("Failed to create conversation: %v", err)
				a.invalidateViewportCache() // CRITICAL FIX: Invalidate before content change
				a.messages[len(a.messages)-1].Content = fmt.Sprintf("Error: %v", err)
				a.updateViewportContent()
				return nil
			}
			a.setCurrentConversationID(conv.ID)
			// rootSessionID is already set at App startup via uuid.New(). It is the
			// stable identifier for this TUI session and never changes — not even after
			// compaction creates a new convID.

			// Set up task persistence for the new conversation
			if err := a.sdk.SetupTaskPersistence(a.currentConvID); err != nil {
				logDebug("Failed to setup task persistence: %v", err)
			}

			// Add system prompt to message history for new conversation
			a.addSystemPromptToHistory()
		}

		// Show spinner while executing
		a.streamingMessage = true
		a.animationClock.SetStreaming(true) // Use fast animation while streaming
		a.messageSequence = 0               // Reset sequence counter for new message
		a.agentStartTime = time.Now()       // Record start time for elapsed calculation
		a.setActivityPhase(ActivityPhaseThinking, "")
		a.loadingIndicator.Start(a.animationClock) // Subscribe to shared animation clock
		a.spinner.Start(a.animationClock)          // Subscribe to shared animation clock

		// Initialize streaming token trackers BEFORE any content arrives
		// This prevents the token counter from showing 0 at the start
		// The SDK will send a TokenEstimateUpdate, but we need a fallback for the initial display
		if a.streamingInputTokens == 0 {
			// Use current token count as baseline
			if a.lastRealTokenCount > 0 {
				a.streamingInputTokens = a.lastRealTokenCount
				logDebug("[START-STREAMING] Initialized streamingInputTokens=%d from lastRealTokenCount", a.streamingInputTokens)
			} else if a.tokenCount > 0 {
				a.streamingInputTokens = a.tokenCount
				logDebug("[START-STREAMING] Initialized streamingInputTokens=%d from tokenCount", a.tokenCount)
			}
		}

		// Reset output character counter for this new streaming session
		a.streamingOutputChars = 0

		// Start TPS metrics tracking for this streaming session.
		// The tracker counts output tokens only (standard tok/s definition).
		if a.metrics != nil {
			a.metrics.TPS.StartStreaming()
			logDebug("[METRICS] Started TPS tracking (output tokens only)")
		}
		// Notify curator that agent is busy (cancel inactivity timer)
		if a.sdk != nil {
			if hm := a.sdk.GetHooksManager(); hm != nil {
				hm.NotifyAgentBusy()
			}
		}
		a.updateViewportContent()

		// Execute message with real-time updates via BackgroundAgentManager
		// This allows the agent to continue running if user exits the chat
		convID := a.currentConvID
		model := a.currentModel

		logDebug("[sendMessage] Registering agent with bgManager for convID=%s, model=%s", convID, model)

		// FLOW LOGGING: Start of message send flow

		// Start trace span for message execution
		var parentCtx context.Context = context.Background()
		if a.sdk != nil && a.sdk.Tracer() != nil {
			var span observability.Span
			parentCtx, span = a.sdk.Tracer().StartSpan(parentCtx, "agent.execute_message")
			span.SetAttributes(map[string]any{
				"conversation_id": convID,
				"model":           model,
				"provider":        a.sdk.GetProviderName(),
			})
			// Span will be ended when goroutine completes or agent fails
			// We store it in a local variable to be captured by the goroutine
			defer func() {
				// This is the outer defer, but we want the span to live during execution
				// Actually, we'll pass the context into the background manager
			}()
		}

		// Register agent with background manager - gets persistent context
		sourceChan, ctx, _ := a.bgManager.Register(parentCtx, convID, model, msgText)
		logDebug("[sendMessage] Registered agent, sourceChan=%v", sourceChan != nil)

		// Subscribe to updates for UI display
		uiChan, buffered := a.bgManager.Subscribe(convID, "ui")
		logDebug("[sendMessage] Subscribed to updates, uiChan=%v, buffered=%d", uiChan != nil, len(buffered))

		// Process any buffered updates that arrived before we subscribed
		// This is critical for hook execution updates that fire before Subscribe() is called
		if len(buffered) > 0 {
			for _, update := range buffered {
				a.queueAgentIntermediateUpdate(update)
			}
		}

		// WaitGroup so the execution goroutine can wait for listenForAgentUpdates to
		// finish draining uiChan before sending agentResponseMsg.  Without this,
		// agentResponseMsg races into updateQueue ahead of the last content chunks
		// (MarkComplete closes sourceUpdate → fanOut closes uiChan, but the
		// listenForAgentUpdates goroutine may not have drained it yet), causing the
		// drain chain to stop before the final content is rendered.
		var listenerDone sync.WaitGroup
		listenerDone.Add(1)

		// Launch goroutine to listen for updates and send them to Bubble Tea runtime
		if uiChan != nil {
			go func() {
				defer listenerDone.Done()
				a.listenForAgentUpdates(uiChan)
			}()
		} else {
			logDebug("[sendMessage] WARNING: uiChan is nil, updates won't be received")
			listenerDone.Done()
		}

		// Launch agent execution in background
		go func() {
			// Declare result variables at top for defer access
			var response string
			var allMessages []*Message
			var err error

			// CRITICAL: Ensure cleanup always happens, even on panic or early return
			defer func() {
				if r := recover(); r != nil {
					stack := debug.Stack()
					logDebug("[sendMessage] PANIC in agent execution: %v\n%s", r, stack)
					err = fmt.Errorf("panic in agent execution: %v", r)
					CapturePanicEvent(r, stack, "agent_execution", map[string]any{
						"conversation_id": convID,
					})
				}
				// Always mark complete and signal done, even on panic
				a.bgManager.MarkComplete(convID, response, allMessages, err)
				listenerDone.Wait()
				a.sendToRuntime(agentResponseMsg{
					content:        response,
					err:            err,
					traceID:        GetTraceID(ctx, err),
					conversationID: convID,
					AllMessages:    allMessages,
				})
			}()

			logDebug("[sendMessage] Starting agent execution for convID=%s", convID)

			// Execute message with source channel for updates
			logDebug("[SendMessage] Proceeding to execute message on convID=%s...", convID)

			// Record span if tracing is enabled
			if a.sdk != nil && a.sdk.Tracer() != nil {
				if span := a.sdk.Tracer().SpanFromContext(ctx); span != nil {
					defer span.End()
				}
			}

			// Set up message injector so queued user messages are injected between agent turns
			a.sdk.SetMessageInjector(func() []string {
				a.pendingMsgMu.Lock()
				defer a.pendingMsgMu.Unlock()
				if len(a.pendingUserMessages) == 0 {
					return nil
				}
				msgs := a.pendingUserMessages
				a.pendingUserMessages = nil
				// Notify TUI that messages were injected (for display update)
				for _, msg := range msgs {
					select {
					case a.updateQueue <- injectedUserMsg{content: msg}:
					default:
					}
				}
				return msgs
			})

			// Parallel rich injector for SYSTEM-role notifications (background task
			// done, background agent done, etc.). These MUST NOT be attributed to
			//	the user — prior behavior routed them through SetMessageInjector
			//	which wraps in conversation.RoleUser and created "[USER]: [Background
			//	Task Notification]" entries in the transcript (2026-04-27 survey).
			a.sdk.SetRichMessageInjector(func() []*conversation.Message {
				a.pendingMsgMu.Lock()
				defer a.pendingMsgMu.Unlock()
				if len(a.pendingSystemMessages) == 0 {
					return nil
				}
				msgs := make([]*conversation.Message, 0, len(a.pendingSystemMessages))
				for _, text := range a.pendingSystemMessages {
					msgs = append(msgs, &conversation.Message{
						Role:      conversation.RoleSystem,
						Content:   text,
						Timestamp: time.Now(),
					})
					select {
					case a.updateQueue <- injectedUserMsg{content: text}: // UI display only; tag doesn't affect role
					default:
					}
				}
				a.pendingSystemMessages = nil
				return msgs
			})

			response, err = a.sdk.ExecuteMessage(ctx, convID, msgText, model, sourceChan, userMsg.Attachments)

			// Record error in span if it exists
			if err != nil && a.sdk != nil && a.sdk.Tracer() != nil {
				if span := a.sdk.Tracer().SpanFromContext(ctx); span != nil {
					span.RecordError(err)
				}
			}
			logDebug("[sendMessage] Agent execution completed: err=%v, response=%d chars", err, len(response))
			if err != nil {
			}

			// After execution, retrieve full conversation to get final state
			if err == nil {
				sdkMessages, getErr := a.sdk.GetMessages(ctx, convID)
				logDebug("GetMessages: err=%v, count=%d", getErr, len(sdkMessages))
				if getErr == nil {
					// Convert SDK messages to TUI messages
					allMessages = convertSDKMessages(sdkMessages)
					logDebug("Converted %d SDK messages to TUI messages", len(allMessages))
				}
			}

		}()

		// Return batched commands: animation clock + queue drain tick
		// The clock subscription from Start() already triggers animation
		return tea.Batch(
			a.animationClock.Tick(), // Single animation clock for all animations
			// Guarded so a send during an in-flight stream does not start a
			// second, parallel drain-tick chain.
			a.scheduleAgentTick(100*time.Millisecond),
		)
	}

	// Fallback: demo mode without SDK
	msg := "SDK not initialized - running in demo mode. Run /auth to connect a provider."
	if a.sdkInitError != "" {
		msg = fmt.Sprintf("SDK unavailable: %s. Run /auth to connect a provider.", a.sdkInitError)
	}
	a.invalidateViewportCache() // CRITICAL FIX: Invalidate before content change
	a.messages[len(a.messages)-1].Content = msg
	a.addNotification("error", msg)
	a.updateViewportContent()

	return nil
}

// listenForAgentUpdates monitors the agent update channel and sends UI updates to Bubble Tea
func (a *App) listenForAgentUpdates(updateChan <-chan agent.IntermediateUpdate) {

	updateCount := 0
	for update := range updateChan {
		updateCount++
		a.queueAgentIntermediateUpdate(update)
	}
}

// sendToRuntime sends a message to the Bubble Tea runtime for UI updates.
// Permission and question broker messages must never be dropped — they carry a
// blocking goroutine on the other end waiting for a UI response. For those we
// block until the slot is free. All other messages use a non-blocking send and
// are dropped only when the queue is completely full.

// startBgProcessWakeCmd starts the agent to process queued background-task
// notifications without adding a visible user bubble. The notification text is
// passed to the API as the user-turn trigger (required for alternating roles)
// but the user bubble is suppressed via suppressNextUserBubble so it never
// appears in the conversation view. The actual notification content is also
// delivered as a system message via the richMessageInjector.
func (a *App) startBgProcessWakeCmd(triggerText string) tea.Cmd {
	if a.streamingMessage || a.sdk == nil {
		return nil
	}
	if a.currentConvID == "" && a.activeConv == nil {
		return nil // no active conversation yet
	}
	// Save and restore any text the user was composing
	savedInput := a.textInput.Value()
	a.suppressNextUserBubble = true
	a.textInput.SetValue(triggerText)
	cmd := a.handleSendMessage()
	if savedInput != "" {
		// Restore user's draft so we don't discard what they were typing
		a.textInput.SetValue(savedInput)
	}
	return cmd
}

// wakePendingSystemMessages drains ALL queued system notifications (background
// completions, scheduled cron/goal prompts) and wakes the idle agent with the
// combined text as a suppressed trigger — the same mechanism background bash
// completions use to turn the agent back on.
//
// Draining (rather than waking with a copy and leaving the queue populated)
// is what makes this safe to call at end-of-turn: a queued copy that survived
// the wake would re-trigger on every subsequent turn. If the wake can't start
// (no conversation yet, agent raced back to busy), the messages are requeued
// so the mid-run rich injector or the next end-of-turn drain delivers them.
func (a *App) wakePendingSystemMessages() tea.Cmd {
	if a.streamingMessage || a.sdk == nil {
		return nil
	}
	a.pendingMsgMu.Lock()
	msgs := a.pendingSystemMessages
	a.pendingSystemMessages = nil
	a.pendingMsgMu.Unlock()
	if len(msgs) == 0 {
		return nil
	}

	combined := strings.Join(msgs, "\n\n")
	cmd := a.startBgProcessWakeCmd(combined)
	if cmd == nil {
		// Couldn't wake — put the messages back for later delivery.
		a.pendingMsgMu.Lock()
		a.pendingSystemMessages = append(msgs, a.pendingSystemMessages...)
		a.pendingMsgMu.Unlock()
	}
	return cmd
}

func (a *App) sendToRuntime(msg tea.Msg) {
	msgType := fmt.Sprintf("%T", msg)
	isA2A := strings.Contains(msgType, "a2a")

	// Classify messages that must not be dropped.
	isCritical := false
	switch m := msg.(type) {
	case permissionApprovalMsg, permissionApprovalResolvedMsg,
		questionRequestMsg, questionResolvedMsg,
		planEnterMsg, planApprovalRequestMsg,
		agentResponseMsg,    // CRITICAL: clears streaming state; dropping causes UI stuck on "thinking..."
		cronPromptInjectMsg: // CRITICAL: a fired cron/wakeup prompt has no retry — dropping it loses the scheduled run
		isCritical = true
	case agentSubAgentUpdateMsg:
		// A SubAgentCompleteUpdate is the ONLY signal that flips a
		// sub-agent block from "in flight" to "done". If it is dropped
		// because the queue happens to be full at that millisecond, the
		// box stays at the spinner forever — exactly the "agents seem
		// stuck in the background" bug the user reports. Treat completion
		// events as critical so they survive back-pressure.
		if _, ok := m.update.(agent.SubAgentCompleteUpdate); ok {
			isCritical = true
		}
	}

	if isCritical {
		if isA2A && a.sdk != nil && a.sdk.logger != nil {
			a.sdk.logger.Info(context.Background(), "tui.a2a.enqueue_critical",
				observability.F("message_type", msgType),
				observability.F("program_set", a.program != nil),
				observability.F("queue_len", len(a.updateQueue)),
				observability.F("queue_cap", cap(a.updateQueue)),
			)
		}
		// Blocking send: the broker goroutine is waiting for a UI response;
		// dropping the message would deadlock the agent indefinitely.
		a.updateQueue <- msg
		logDebug("sendToRuntime: Critical message queued %T", msg)
		// Nudge the bubbletea runtime so the queued message is drained even when
		// the TUI is otherwise idle. Without this, a critical message (e.g. a
		// fired cron/wakeup prompt) sits in updateQueue until the NEXT bubbletea
		// event — a keypress or other user interaction — because no animation/tick
		// chain is running while idle. That was the "cron doesn't auto-run until I
		// interact with the TUI" bug. The non-critical path below already wakes;
		// the critical path must too. wakeRuntimeAsync is idempotent (CAS-guarded).
		a.wakeRuntimeAsync()
		return
	}

	// Non-critical: best-effort, drop if queue is full to avoid back-pressure.
	queueLen := len(a.updateQueue)
	queueCap := cap(a.updateQueue)
	if isA2A && a.sdk != nil && a.sdk.logger != nil {
		a.sdk.logger.Info(context.Background(), "tui.a2a.enqueue_attempt",
			observability.F("message_type", msgType),
			observability.F("program_set", a.program != nil),
			observability.F("queue_len", queueLen),
			observability.F("queue_cap", queueCap),
		)
	}
	select {
	case a.updateQueue <- msg:
		if isA2A && a.sdk != nil && a.sdk.logger != nil {
			a.sdk.logger.Info(context.Background(), "tui.a2a.enqueued",
				observability.F("message_type", msgType),
				observability.F("program_set", a.program != nil),
				observability.F("queue_len", len(a.updateQueue)),
				observability.F("queue_cap", cap(a.updateQueue)),
			)
		}
		logDebug("sendToRuntime: Queued message type %T", msg)
		a.wakeRuntimeAsync()
	default:
		if isA2A && a.sdk != nil && a.sdk.logger != nil {
			a.sdk.logger.Warn(context.Background(), "tui.a2a.enqueue_dropped",
				observability.F("message_type", msgType),
				observability.F("program_set", a.program != nil),
				observability.F("queue_len", queueCap),
				observability.F("queue_cap", queueCap),
			)
		}
		logDebug("sendToRuntime: Update queue full, dropping message type %T", msg)
	}
}

// wakeRuntimeAsync nudges Bubble Tea to drain queued updates without letting
// background goroutines block on Program.Send. This matters for A2A inbound
// callbacks and fired cron/wakeup prompts, where the TUI is otherwise idle and
// nothing else would run Update() until the next terminal event (a keypress or
// focus click).
//
// It is a lock-free coalescing signal with guaranteed re-arm:
//
//   - wakePending (the "signal") is set to 1 on every request.
//   - wakeInProgress (the "ownership token") ensures exactly one goroutine runs.
//   - The goroutine loops: consume the pending signal (Swap→0) and, if it was
//     set, Program.Send(drainQueueMsg{}). Before exiting it releases ownership
//     and RE-CHECKS pending, re-acquiring if a request arrived in the tiny
//     window between the last consume and the release. This closes the classic
//     lost-wakeup race, so a message enqueued while a wake is in-flight ALWAYS
//     triggers at least one more drainQueueMsg after it — it can never be
//     stranded until a focus click.
//
// Why no timeout/nested goroutine (the old approach): Program.Send selects on
// the program's context, so it is a no-op after the program terminates and only
// blocks BEFORE the event loop starts — in which case it unblocks the instant
// Run() begins and delivers the message. A single owned goroutine calling Send
// directly therefore cannot leak. The old inner-goroutine + 100ms timeout could
// abandon a still-blocked sender AND reset the guard while that send was still
// outstanding, allowing duplicate/needless wakes and, worse, dropping the
// re-arm — the residual "cron doesn't run until I click" window.
func (a *App) wakeRuntimeAsync() {
	if a == nil || a.quitting {
		return
	}
	// Test-only seam: observe wake requests without a real *tea.Program.
	if a.wakeNotify != nil {
		a.wakeNotify()
		return
	}
	if a.program == nil {
		return
	}
	// Arm the signal, then try to become the sole wake goroutine. If one is
	// already running it will observe this pending flag before it exits.
	atomic.StoreInt32(&a.wakePending, 1)
	if !atomic.CompareAndSwapInt32(&a.wakeInProgress, 0, 1) {
		return
	}
	program := a.program
	go func() {
		defer func() {
			if r := recover(); r != nil {
				stack := debug.Stack()
				logDebug("wakeRuntimeAsync recovered from panic: %v\n%s", r, stack)
				CapturePanicEvent(r, stack, "wake_runtime_async", nil)
			}
		}()
		for {
			if a.quitting {
				atomic.StoreInt32(&a.wakeInProgress, 0)
				return
			}
			if atomic.SwapInt32(&a.wakePending, 0) == 1 {
				// A wake was requested — deliver exactly one drain nudge. The
				// drain loop in Update() consumes the ENTIRE queue, so one
				// nudge covers every message enqueued up to this point.
				program.Send(drainQueueMsg{})
				continue
			}
			// No pending work: release ownership, then re-check to catch a
			// request that arrived after our Swap but before this release.
			atomic.StoreInt32(&a.wakeInProgress, 0)
			if atomic.LoadInt32(&a.wakePending) == 0 {
				return
			}
			// A late request appeared; try to re-acquire. If someone else won
			// the CAS they now own the signal and will service it.
			if !atomic.CompareAndSwapInt32(&a.wakeInProgress, 0, 1) {
				return
			}
		}
	}()
}

// invalidateViewportCache marks the viewport content as needing a full rebuild.
// Call this when display settings change (showThinking, showFullToolOutput, theme, etc.)
// Invalidates MessageList caches to prevent stale content artifacts.
// invalidateViewportCache invalidates the entire viewport cache.
// Use invalidateMessageCache for per-message invalidation when possible.
func (a *App) invalidateViewportCache() {
	logDebug("[CACHE] Invalidating render caches (messagelist)")
	// Mark content as needing re-render (skips redundant renders in View())
	a.viewportContentDirty = true
	// Mark all messages as dirty for full re-render
	for i := range a.messages {
		a.messages[i].MarkDirty()
	}
	// MessageList cache
	if a.msgViewport != nil {
		a.msgViewport.cachedDirty = true
	}
}

// invalidateMessageCache invalidates only a specific message.
// This is more efficient than invalidating the entire viewport.
func (a *App) invalidateMessageCache(msgIdx int) {
	if msgIdx < 0 || msgIdx >= len(a.messages) {
		return
	}
	logDebug("[CACHE] Invalidating message %d", msgIdx)
	a.messages[msgIdx].MarkDirty()
	a.viewportContentDirty = true
	if a.msgViewport != nil {
		a.msgViewport.cachedDirty = true
	}
}

// invalidateViewportCacheForWidth re-points the per-message render cache at a
// new viewport width. Wrapping is a pure function of (content, width), so any
// message that still holds a wrap for the target width — the common case when
// the side panel is toggled back — keeps its cached lines and is not
// re-rendered. Only messages without a matching wrap are marked dirty.
//
// This replaces a blanket MarkDirty on side-panel toggle, which forced a full
// markdown re-render of the entire transcript on every press.
func (a *App) invalidateViewportCacheForWidth(width int) {
	if width <= 0 {
		a.invalidateViewportCache()
		return
	}
	a.viewportContentDirty = true
	lastIdx := len(a.messages) - 1
	for i := range a.messages {
		// Always re-render the last message. Every in-place content mutation
		// in this package targets len(messages)-1 (streaming appends, bash
		// results, error text). The blanket MarkDirty this function replaced
		// used to mask any such mutation that forgot to mark itself dirty;
		// reusing a cached wrap here would surface that as stale text. One
		// message render per toggle is a negligible price for removing the
		// entire failure class.
		if i == lastIdx {
			a.messages[i].MarkDirty()
			continue
		}
		if !a.messages[i].UsePreRenderForWidth(width) {
			a.messages[i].MarkDirty()
		}
	}
	if a.msgViewport != nil {
		a.msgViewport.cachedDirty = true
	}
}

// invalidateLastMessageCache invalidates only the last message.
// This is useful for streaming updates where only the last message changes.
func (a *App) invalidateLastMessageCache() {
	if len(a.messages) == 0 {
		return
	}
	a.invalidateMessageCache(len(a.messages) - 1)
}

// collapseContentOnlyBlocksToFinal replaces content-only ordered blocks with a single final content block.
// This prevents stale partial streaming blocks from overriding the final assistant content.
func collapseContentOnlyBlocksToFinal(msg *Message, finalContent string, sequence int) bool {
	if msg == nil || finalContent == "" || len(msg.OrderedBlocks) == 0 {
		return false
	}

	var combined strings.Builder
	contentCount := 0
	for _, block := range msg.OrderedBlocks {
		if block.Type != "content" {
			return false
		}
		contentCount++
		combined.WriteString(block.GetContent())
	}

	if contentCount == 0 {
		return false
	}
	if contentCount == 1 && combined.String() == finalContent {
		return false
	}

	msg.OrderedBlocks = []MessageBlock{{
		Type:     "content",
		Content:  finalContent,
		Sequence: sequence,
	}}
	return true
}

// shouldIgnoreLateAssistantTextUpdate returns true when the most recent assistant
// message is already complete and should no longer accept streaming text/thinking deltas.
func shouldIgnoreLateAssistantTextUpdate(messages []Message) bool {
	if len(messages) == 0 {
		return false
	}
	last := messages[len(messages)-1]
	return last.Role == "assistant" && last.IsComplete
}

// latestAssistantContent returns the last non-empty assistant content from loaded history.
func latestAssistantContent(messages []*Message) string {
	for i := len(messages) - 1; i >= 0; i-- {
		msg := messages[i]
		if msg == nil || msg.Role != "assistant" {
			continue
		}
		content := strings.TrimSpace(msg.GetContent())
		if content != "" {
			return content
		}
	}
	return ""
}

// hasStructuredAssistantActivity reports whether the message contains tool/sub-agent blocks.
// For those messages we preserve interleaved UI blocks and avoid replacing with flat history text.
func hasStructuredAssistantActivity(msg *Message) bool {
	if msg == nil {
		return false
	}
	if len(msg.ToolCalls) > 0 || len(msg.ToolResults) > 0 {
		return true
	}
	for _, block := range msg.OrderedBlocks {
		switch block.Type {
		case "tool_call", "tool_result", "sub_agent_activity", "hook_execution":
			return true
		}
	}
	return false
}

// resolveFinalAssistantContent chooses the authoritative final assistant content.
func resolveFinalAssistantContent(current *Message, directContent string, history []*Message) string {
	finalContent := strings.TrimSpace(directContent)
	fromHistory := latestAssistantContent(history)

	// Tool/sub-agent heavy turns keep the direct final content to preserve block ordering semantics.
	if hasStructuredAssistantActivity(current) {
		if finalContent != "" {
			return finalContent
		}
		return fromHistory
	}

	// For flat content turns, prefer non-empty direct content.
	// Use history only as a fallback (or when it clearly extends direct content).
	if finalContent == "" {
		return fromHistory
	}
	if fromHistory == "" {
		return finalContent
	}
	if strings.HasPrefix(fromHistory, finalContent) && len(fromHistory) > len(finalContent) {
		return fromHistory
	}
	return finalContent
}

// performCompaction executes the compaction process on the current conversation
