package agent

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/cache/profiling"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/chrome"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/conversation"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/managed"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/observability"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/provider"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/provider/prompttrace"
	sdkerr "github.com/Swarm-Code/mono/swarm-sdk/internal/sdkerr"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/tools"
)

// Run is a convenience wrapper around Execute for the common case of sending a
// single message and getting a response.
//
// For full control over conversation history, context injection, turn limits,
// or timeouts, use Execute with an explicit ExecuteRequest.
func (a *Agent) Run(ctx context.Context, message string) (*ExecuteResponse, error) {
	return a.Execute(ctx, ExecuteRequest{Message: message})
}

// Execute runs the agent synchronously and returns the final response.
func (a *Agent) Execute(ctx context.Context, req ExecuteRequest) (*ExecuteResponse, error) {
	return a.execute(ctx, req, false)
}

// ExecuteWhenIdle waits for the agent to become idle and then runs the request.
// This is intended for internal queue-like integrations such as inbound A2A
// requests, where re-entering the live foreground execution would violate the
// agent's single-flight state machine.
func (a *Agent) ExecuteWhenIdle(ctx context.Context, req ExecuteRequest) (*ExecuteResponse, error) {
	return a.execute(ctx, req, true)
}

func (a *Agent) execute(ctx context.Context, req ExecuteRequest, waitForIdle bool) (*ExecuteResponse, error) {
	if a.browserFamily.Valid() {
		var err error
		ctx, err = chrome.Bind(ctx, a.browserFamily)
		if err != nil {
			return nil, fmt.Errorf("agent: bind inherited browser family: %w", err)
		}
	}
	// Start distributed trace span
	ctx, span := a.tracer.StartSpan(ctx, "agent.execute")
	defer span.End()

	observability.AddAgentAttributes(span, a.definition.ID, a.definition.Name, a.definition.Provider, a.definition.Model)

	// ─────────────────────────────────────────────────────────────
	// EXECUTION MODE CHECK: Delegate to managed execution if needed
	// ─────────────────────────────────────────────────────────────
	if a.definition.IsManaged() {
		return a.executeManaged(ctx, req)
	}

	// Update state
	for {
		a.mu.Lock()
		if a.state == StateIdle {
			// Hold the lock through the StateExecuting assignment below; the
			// matching Unlock happens after we read a2aRuntime. Earlier this
			// path Unlocked here too, which double-unlocked the mutex and
			// fatally panicked the agent on its very first Execute call.
			break
		}
		state := a.state
		a.mu.Unlock()
		if !waitForIdle {
			// The agent is busy — this is transient contention, not a permanent
			// failure. Use Transient so callers can retry rather than treating
			// this as an unrecoverable error.
			return nil, sdkerr.Transient("agent.busy",
				fmt.Sprintf("agent is in state %s, expected idle", state),
				sdkerr.WithOperation("agent.execute"),
				sdkerr.WithComponent("agent"),
				sdkerr.WithTraceFromContext(ctx))
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(20 * time.Millisecond):
		}
	}
	a.state = StateExecuting
	a.conversationID = req.ConversationID
	a.storagePath = req.StoragePath
	a.turnCount = 0
	a.toolCallsTotal = 0
	// Token counters are conversation-scoped and the request's history is their
	// source of truth: seedTokenCountFromHistory below re-seeds from the last
	// API-reported usage. Without this reset, a request whose history carries no
	// usage (fresh or freshly compacted conversation) inherits the previous
	// conversation's counts, and checkContextPressure then fires spurious
	// compaction / blocking-limit warnings against a tiny conversation.
	a.inputTokens = 0
	a.outputTokens = 0
	a.lastRequestEstimate = 0
	a.requestContext = req.Context // Store request context for buildProviderRequest
	// Capture per-request overrides for this turn (cleared in the idle-reset
	// defer below). These shape one execution without mutating the definition.
	a.reqSystemPromptOverride = req.SystemPromptOverride
	a.reqDisableTools = req.DisableTools
	a.reqDisableHooks = req.DisableHooks
	startTime := time.Now()

	// Cache profiling will be initialized per-turn in executeLoop
	a.logger.Debug(ctx, "agent.execute.started",
		observability.F("conversation_id", a.conversationID),
		observability.F("profiling_enabled", os.Getenv("DISABLE_CACHE_PROFILING") != "1"),
		observability.F("client_type", a.definition.ClientType),
		observability.F("machine_id_hash", a.definition.MachineIDHash))

	// SWA-19: emit client type and machine ID at Info level on every execution
	// so analytics can segment programmatic vs user-driven runs regardless of
	// log verbosity. Debug-only emission above is insufficient for reporting.
	a.logger.Info(ctx, "agent.session.identity",
		observability.F("conversation_id", a.conversationID),
		observability.F("client_type", a.definition.ClientType),
		observability.F("machine_id_hash", a.definition.MachineIDHash))

	a.logger.Info(ctx, "agent.execute.request_context",
		observability.F("context_nil", req.Context == nil),
		observability.F("context_len", len(req.Context)),
		observability.F("context_keys", getContextKeys(req.Context)),
	)

	a2aRuntime := a.a2aRuntime
	a.mu.Unlock()

	if a2aRuntime != nil {
		if err := a2aRuntime.UpdateConversationID(ctx, req.ConversationID); err != nil {
			a.logger.Warn(ctx, "agent.a2a_update_conversation_failed",
				observability.F("conversation_id", req.ConversationID),
				observability.F("error", err.Error()))
		}
	}

	// Ensure we return to idle state
	defer func() {
		a.mu.Lock()
		a.state = StateIdle
		a.lastActivityAt = time.Now()
		// Clear per-request overrides so the next turn starts from definition
		// defaults rather than inheriting this turn's one-shot settings.
		a.reqSystemPromptOverride = ""
		a.reqDisableTools = false
		a.reqDisableHooks = false
		a.mu.Unlock()
	}()

	// Determine execution limits
	maxTurns := req.MaxTurns
	if maxTurns == 0 && a.definition.Capabilities != nil {
		maxTurns = a.definition.Capabilities.MaxTurns
	}
	// No default turn limit - agents can run indefinitely until natural completion

	timeout := req.Timeout
	if timeout == 0 && a.definition.Capabilities != nil && a.definition.Capabilities.Timeout > 0 {
		timeout = a.definition.Capabilities.Timeout
	}
	if timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}

	// Prompt provenance trace: attach a per-request Collector to the context
	// when a writer is configured. Contribution sites (skills loader, context
	// orchestrator, headless broker, etc.) can then call prompttrace.From(ctx).
	// Append(...) to register what they added; the executeLoop emits a
	// [PROMPT PROVENANCE] banner just before each provider call. When no
	// writer is set, no collector is attached and the helpers are zero-cost.
	a.mu.RLock()
	traceWriter := a.promptTraceWriter
	a.mu.RUnlock()
	if traceWriter != nil {
		ctx = prompttrace.WithCollector(ctx, prompttrace.NewCollector())
		// Stash the ctx so buildProviderRequest (whose existing signature is
		// fixed by a race test and other callers) can reach the collector
		// without a new parameter. Cleared on return to avoid leaking the
		// per-request ctx into later, unrelated calls.
		a.mu.Lock()
		a.traceCtx = ctx
		seedFn := a.promptTraceSeedFn
		a.mu.Unlock()
		defer func() {
			a.mu.Lock()
			a.traceCtx = nil
			a.mu.Unlock()
		}()
		// Seed startup-time contributions (skills injection, dream contract
		// injection, headless custom prompt, etc.) into the collector. They
		// ran long before this ctx existed, so the SDK Integration layer
		// caches their sizes and replays them via this callback.
		if seedFn != nil {
			seedFn(ctx)
		}
	}

	// Build initial messages - include conversation history if provided
	var messages []*conversation.Message

	if len(req.ConversationHistory) > 0 {
		// Use provided conversation history
		messages = make([]*conversation.Message, len(req.ConversationHistory))
		copy(messages, req.ConversationHistory)
		a.seedTokenCountFromHistory(messages)
		// Provenance: each history message is a contribution from this caller.
		// We record one entry tagged "conversation_history" so the operator can see
		// how many messages came in pre-built and where (file:line of this site).
		if col := prompttrace.From(ctx); col != nil {
			for i, m := range messages {
				if m == nil {
					continue
				}
				col.AppendMsg("conversation_history", string(m.Role), m.Content, i)
			}
		}

		historySummary := conversation.SummarizeMessages(req.ConversationHistory)
		a.logger.Info(ctx, "agent.using_conversation_history",
			observability.F("conversation_id", req.ConversationID),
			observability.F("history_length", historySummary.MessageCount),
			observability.F("nil_message_count", historySummary.NilCount),
			observability.F("role_histogram", historySummary.Roles),
			observability.F("tool_call_count", historySummary.ToolCalls),
			observability.F("tool_result_count", historySummary.ToolResults),
			observability.F("message_id_sequence_hash", historySummary.IDSequenceHash))
	}

	// Add current user message (if not already in history).
	// Skip when req.Message is empty — callers that pre-populate ConversationHistory
	// with both the user message and any hook-context messages set req.Message = ""
	// to signal that nothing should be appended here.
	if req.Message != "" {
		shouldAppendUserMsg := true
		if len(messages) > 0 {
			lastMsg := messages[len(messages)-1]
			if lastMsg.Role == conversation.RoleUser && lastMsg.Content == req.Message {
				shouldAppendUserMsg = false
				a.logger.Info(ctx, "agent.skipping_duplicate_user_message",
					observability.F("conversation_id", req.ConversationID),
					observability.F("content_length", len(req.Message)))
			}
		}
		if shouldAppendUserMsg {
			messages = append(messages, &conversation.Message{
				Role:      conversation.RoleUser,
				Content:   req.Message,
				Timestamp: time.Now(),
			})
			prompttrace.From(ctx).AppendMsg("user_message", "user", req.Message, len(messages)-1)
		}
	}

	// Propagate the user message into the context so tools executed during this
	// turn can record "why" the change was made (used by checkpoint/undo system).
	if req.Message != "" {
		ctx = tools.WithUserMessage(ctx, req.Message)
	}

	// Execute agent loop
	finalMessage, finishReason, err := a.executeLoop(ctx, messages, maxTurns)
	if err != nil {
		causalErr := sdkerr.Wrap(
			err,
			"agent.execute.failed",
			sdkerr.WithOperation("agent.execute"),
			sdkerr.WithComponent("agent"),
			sdkerr.WithTraceFromContext(ctx),
		)
		// Log with provider+model so Sentry events are searchable by which
		// model hit a context limit, auth issue, or other provider error.
		a.logger.Error(ctx, "agent.execute.failed",
			observability.F("provider", a.definition.Provider),
			observability.F("model", a.definition.Model),
			observability.F("error", err.Error()),
		)
		observability.RecordError(ctx, a.logger, span, causalErr, "agent.execution_failed")
		return nil, causalErr
	}

	// Build response
	duration := time.Since(startTime)

	// Get captured metadata (includes cache metrics from provider)
	a.mu.RLock()
	metadata := a.responseMetadata
	a.mu.RUnlock()

	// Ensure metadata is not nil
	if metadata == nil {
		metadata = make(map[string]any)
	}

	response := &ExecuteResponse{
		Message:        finalMessage,
		ConversationID: a.conversationID,
		TurnCount:      a.turnCount,
		InputTokens:    a.inputTokens,
		OutputTokens:   a.outputTokens,
		TokensUsed:     a.inputTokens + a.outputTokens,
		CostUSD:        a.totalCost,
		Duration:       duration,
		FinishReason:   finishReason,
		Metadata:       metadata, // Includes cache_metrics from provider
	}

	// Add span attributes
	span.SetAttribute("agent.turns", a.turnCount)
	span.SetAttribute("agent.tokens", a.inputTokens+a.outputTokens)
	span.SetAttribute("agent.duration_ms", duration.Milliseconds())
	span.SetAttribute("agent.finish_reason", string(finishReason))

	// Log completion
	a.logger.Info(ctx, "agent.execute.completed",
		observability.F("agent_id", a.definition.ID),
		observability.F("turns", a.turnCount),
		observability.F("tokens", a.inputTokens+a.outputTokens),
		observability.F("duration_ms", duration.Milliseconds()))

	return response, nil
}

// executeWithChain executes a chat request using the model pool chain.
// It handles fallbacks, retries, and provider switching automatically.
// executeLoop is the core agent execution loop.
// It calls the provider, handles tool calls, and loops until done.
func (a *Agent) executeLoop(ctx context.Context, messages []*conversation.Message, maxTurns int) (string, provider.FinishReason, error) {
	var finalMessage string
	var finishReason provider.FinishReason
	completionConfirmCount := 0
	// compactionNotified is reset to false at the start of each Execute() call (one
	// executeLoop invocation). Once the threshold fires and we emit CompactionNeededUpdate,
	// we skip subsequent per-turn emissions so the TUI isn't flooded with notifications
	// while the async compaction goroutine is already running (or timing out).
	compactionNotified := false

	// consecutiveTruncations counts back-to-back FinishReasonLength turns.
	// A single truncated response is recoverable (the model retries with
	// smaller output); only repeated truncation ends the run via the
	// summarize-and-stop fallback.
	consecutiveTruncations := 0
	const maxConsecutiveTruncations = 3

	effectiveMaxTurns := maxTurns
	if effectiveMaxTurns > 0 && a.completionConfirm {
		// Confirmation calls are reserved separately from the caller's work-turn
		// budget so an injected verification prompt always reaches the model.
		effectiveMaxTurns += a.completionConfirmMax
	}
	for effectiveMaxTurns == 0 || a.turnCount < effectiveMaxTurns {
		a.turnCount++

		// Check context cancellation
		select {
		case <-ctx.Done():
			return "", provider.FinishReasonError, sdkerr.Wrap(ctx.Err(), "agent.timeout",
				sdkerr.WithOperation("agent.execute_loop"),
				sdkerr.WithComponent("agent"),
				sdkerr.WithTraceFromContext(ctx),
			)
		default:
		}

		// Check for tool availability changes and inject system message if needed.
		// Codex-backed requests reject system-role messages in input, so we must
		// drop these synthetic system updates for that transport.
		if a.toolChangeHandler != nil && a.toolChangeHandler.HasPendingChanges() {
			if codexSystemMessagesDisallowed(a.provider, a.definition.Model) {
				_ = a.toolChangeHandler.PendingChangesAndReset()
				a.logger.Info(ctx, "agent.tool_availability_changed.skipped_codex")
			} else {
				toolChangeMsg := a.toolChangeHandler.GenerateSystemMessage()
				if toolChangeMsg != nil {
					a.logger.Info(ctx, "agent.tool_availability_changed",
						observability.F("changes", toolChangeMsg.Metadata["changes"]))
					messages = append(messages, toolChangeMsg)
					prompttrace.From(ctx).AppendMsg("tool_availability_update", string(toolChangeMsg.Role), toolChangeMsg.Content, len(messages)-1)
				}
			}
		}

		// Check for injected user messages from the TUI (queued while agent was executing).
		// These are added as user messages so the agent sees them in the next turn.
		a.mu.RLock()
		richInjector := a.richMessageInjector
		injector := a.messageInjector
		a.mu.RUnlock()
		if richInjector != nil {
			if injected := richInjector(); len(injected) > 0 {
				for _, msg := range injected {
					if msg == nil {
						continue
					}
					injectedMsg := msg.Clone()
					if injectedMsg.Timestamp.IsZero() {
						injectedMsg.Timestamp = time.Now()
					}
					messages = append(messages, injectedMsg)
					prompttrace.From(ctx).AppendMsg("rich_injected_message", string(injectedMsg.Role), injectedMsg.Content, len(messages)-1)
					alreadyPersisted, _ := injectedMsg.Metadata[conversation.A2APersistedMetadataKey].(bool)
					if a.messageCallback != nil && !alreadyPersisted {
						if err := a.messageCallback(ctx, injectedMsg); err != nil {
							a.logger.Error(ctx, "agent.injected_rich_message_save_failed",
								observability.F("error", err.Error()))
						}
					}
				}
				a.logger.Info(ctx, "agent.injected_rich_messages",
					observability.F("count", len(injected)),
					observability.F("turn", a.turnCount))
			}
		}
		if injector != nil {
			if injected := injector(); len(injected) > 0 {
				for _, text := range injected {
					injMsg := &conversation.Message{
						Role:      conversation.RoleUser,
						Content:   text,
						Timestamp: time.Now(),
					}
					messages = append(messages, injMsg)
					prompttrace.From(ctx).AppendMsg("injected_user_message", string(injMsg.Role), text, len(messages)-1)
					// Persist injected messages to conversation history
					if a.messageCallback != nil {
						if err := a.messageCallback(ctx, injMsg); err != nil {
							a.logger.Error(ctx, "agent.injected_message_save_failed",
								observability.F("error", err.Error()))
						}
					}
				}
				a.logger.Info(ctx, "agent.injected_user_messages",
					observability.F("count", len(injected)),
					observability.F("turn", a.turnCount))
			}
		}

		// Create provider request
		providerReq := a.buildProviderRequest(messages)
		currentTokens := a.currentRequestTokens(providerReq)
		compacted, pressureErr := a.checkContextPressure(ctx, &messages, currentTokens, &compactionNotified)
		if pressureErr != nil {
			return "", provider.FinishReasonError, pressureErr
		}
		if compacted {
			providerReq = a.buildProviderRequest(messages)
			currentTokens = a.currentRequestTokens(providerReq)
			if err := a.checkHardContextLimit(currentTokens, "post_compaction_guard"); err != nil {
				return "", provider.FinishReasonError, err
			}
		}

		// Emit prompt provenance banner (when --raw is enabled). The
		// collector was attached to ctx in execute(); contribution sites
		// (skills loader, context orchestrator, headless prompt setter,
		// etc.) appended entries while we were building messages. We add
		// one synthetic "assembled" row at index 0 of the messages table
		// to record the final system prompt size — what actually goes on
		// the wire — alongside the per-section breakdown, then render the
		// banner and reset the collector for the next turn.
		if col := prompttrace.From(ctx); col != nil && a.promptTraceWriter != nil {
			col.AppendMsg("assembled_system_prompt", "system", providerReq.SystemPrompt, 0)
			prompttrace.WriteBanner(a.promptTraceWriter, col.Entries())
			col.Reset()
		}

		// ─────────────────────────────────────────────────────────────
		// CACHE PROFILING: Track message mutations before/after translation
		// ─────────────────────────────────────────────────────────────
		// Get profiler for this conversation and log message state
		// This detects if any code path is mutating the message list
		cacheProfiler := profiling.GetProfiler(a.conversationID)
		preTranslationHash, _ := cacheProfiler.BeforeTranslation(ctx, messages)
		_ = preTranslationHash // Currently informational; provider integration validates

		// Add conversation ID to metadata so provider layer can correlate
		// This allows profiler to track metrics per-conversation
		providerReq.Metadata["conversation_id"] = a.conversationID

		// Log current context size before every API call so token counts appear in
		// the log sequence alongside translate.messages / message_detail entries.
		// input_tokens_prev_turn is the API-reported count from the previous turn (0 on turn 1).
		a.mu.RLock()
		prevInputTokens := a.inputTokens
		a.mu.RUnlock()
		contextWindow := a.getContextWindow()
		effectiveWindow := a.getEffectiveInputWindow()
		autoCompactThreshold := a.getAutoCompactThreshold()
		blockingLimit := a.getBlockingLimit()
		var pctUsed float64
		if contextWindow > 0 && currentTokens > 0 {
			pctUsed = float64(currentTokens) / float64(contextWindow) * 100
		}
		a.logger.Info(ctx, "agent.turn.context_size",
			observability.F("turn", a.turnCount),
			observability.F("messages", len(messages)),
			observability.F("input_tokens_prev_turn", prevInputTokens),
			observability.F("input_tokens_current_request", currentTokens),
			observability.F("context_window", contextWindow),
			observability.F("effective_window", effectiveWindow),
			observability.F("auto_compact_threshold", autoCompactThreshold),
			observability.F("blocking_limit", blockingLimit),
			observability.F("pct_used", fmt.Sprintf("%.1f%%", pctUsed)),
		)

		// Call provider or pool chain
		ctx, span := a.tracer.StartSpan(ctx, "agent.provider_call")
		span.SetAttribute("agent.turn", a.turnCount)

		var resp *provider.ChatResponse
		var err error

		callProvider := func(req provider.ChatRequest) (*provider.ChatResponse, error) {
			if a.chain != nil {
				return a.executeWithChain(ctx, req)
			}
			if a.provider.Capabilities().Streaming {
				return a.streamChat(ctx, req)
			}
			return a.provider.Chat(ctx, req)
		}
		resp, err = callProvider(providerReq)

		span.End()

		if err != nil {
			observability.RecordError(ctx, a.logger, span, err, "agent.provider_call_failed")

			// Context-length errors are permanent for the current content. Reuse
			// the same validated, persisted compaction runner and retry once.
			originalErr := err
			a.mu.RLock()
			compactFunc := a.autoCompactionConfig.CompactFunc
			a.mu.RUnlock()
			if sdkerr.IsContextLengthError(originalErr) && compactFunc != nil {
				a.logger.Warn(ctx, "agent.context_length_exceeded_attempting_compaction",
					observability.F("error", originalErr.Error()),
					observability.F("input_tokens", currentTokens),
				)
				if _, compErr := a.runInLoopCompaction(
					ctx, &messages, currentTokens, a.getBlockingLimit(), compactFunc,
				); compErr != nil {
					a.logger.Warn(ctx, "agent.emergency_compaction_rejected",
						observability.F("error", compErr.Error()))
				} else {
					providerReq = a.buildProviderRequest(messages)
					retryTokens := a.currentRequestTokens(providerReq)
					if guardErr := a.checkHardContextLimit(retryTokens, "emergency_post_compaction_guard"); guardErr != nil {
						err = guardErr
					} else {
						retryResp, retryErr := callProvider(providerReq)
						if retryErr == nil {
							resp = retryResp
							err = nil
						} else {
							a.logger.Warn(ctx, "agent.emergency_compaction_retry_failed",
								observability.F("error", retryErr.Error()))
							err = retryErr
						}
					}
				}
			}

			if err != nil {
				return "", provider.FinishReasonError, sdkerr.Wrap(
					err,
					"agent.provider_call.failed",
					sdkerr.WithOperation("agent.provider_call"),
					sdkerr.WithComponent("agent"),
					sdkerr.WithTraceFromContext(ctx),
				)
			}
		}

		// Update token tracking
		if resp.Usage != nil {
			a.mu.Lock()
			// inputTokens uses = (not +=) because it represents the CURRENT context size
			// (the full conversation sent to the model), not accumulated across turns.
			// InputContextSize() = Input + CacheCreation + CacheRead — the true context
			// footprint, matching what the provider counted as "input" for capacity purposes.
			a.inputTokens = resp.Usage.InputContextSize()
			a.lastRequestEstimate = estimateProviderRequestTokens(providerReq)
			// outputTokens uses += because it's the total tokens generated across all turns
			a.outputTokens += resp.Usage.Output
			// TODO: Calculate cost based on model pricing
			a.mu.Unlock()

			// Emit real token count after every turn so the TUI can update immediately.
			// This is the authoritative value — InputContextSize is the full context sent to the model.
			{
				inputTok := resp.Usage.InputContextSize()
				ctxWin := a.getContextWindow()
				var pct float64
				if ctxWin > 0 && inputTok > 0 {
					pct = float64(inputTok) / float64(ctxWin) * 100
				}
				a.callIntermediateCallback(ctx, TokenCountUpdate{
					InputTokens:          inputTok,
					OutputTokens:         resp.Usage.Output,
					Turn:                 a.turnCount,
					ContextWindow:        ctxWin,
					EffectiveWindow:      a.getEffectiveInputWindow(),
					AutoCompactThreshold: a.getAutoCompactThreshold(),
					PctUsed:              pct,
					CacheReadTokens:      resp.Usage.CacheRead,
					CacheCreationTokens:  resp.Usage.CacheCreation,
					UncachedInputTokens:  resp.Usage.Input,
				})
			}

			// Debug: Log token tracking update
			a.logger.Debug(ctx, "agent.token_tracking.updated",
				observability.F("input_tokens", resp.Usage.Input),
				observability.F("output_tokens", resp.Usage.Output),
				observability.F("total_tokens", resp.Usage.Total),
				observability.F("turn", a.turnCount),
			)

		} else {
			// Debug: Log when Usage is nil
			a.logger.Warn(ctx, "agent.token_tracking.no_usage",
				observability.F("turn", a.turnCount),
				observability.F("message", "resp.Usage is nil"),
			)
		}

		// Capture response metadata (includes cache metrics)
		// This is stored on the last turn and returned in ExecuteResponse
		if resp.Metadata != nil {
			a.mu.Lock()
			a.responseMetadata = resp.Metadata
			a.mu.Unlock()
		}

		// Add assistant message to history
		if resp.Message != nil {
			// Populate tokens from usage for persistence
			if resp.Usage != nil {
				resp.Message.Tokens = resp.Usage
			}

			messages = append(messages, resp.Message)

			// Emit content update for real-time UI (if message has content)
			// Only emit if we didn't already stream it via streamChat or streamChatWithProvider.
			// The response's StreamedContent field tracks whether streaming was used,
			// regardless of whether it came from the main path or the chain path.
			if resp.Message.Content != "" && !resp.StreamedContent {
				// First turn replaces placeholder, subsequent turns append after tool results
				shouldAppend := a.turnCount > 1
				content := resp.Message.Content
				// Add spacing before appended content for readability
				if shouldAppend {
					content = "\n\n" + content
				}
				a.callIntermediateCallback(ctx, ContentUpdate{
					Content:  content,
					Append:   shouldAppend,
					Sequence: a.nextUpdateSequence(),
				})
			}

			// Emit thinking update if present (extended thinking from Anthropic).
			// Only emit here when NOT using streaming — when streaming is active,
			// streamChat() or streamChatWithProvider() already emits ThinkingUpdate
			// per-chunk as thinking arrives. Emitting again here would duplicate
			// the callback and cause the TUI to reset the thinking content after
			// streaming has already rendered it.
			if resp.Message.Thinking != "" && !resp.StreamedContent {
				a.callIntermediateCallback(ctx, ThinkingUpdate{
					Content:  resp.Message.Thinking,
					Append:   false,
					Sequence: a.nextUpdateSequence(),
				})
			}

			// Emit terminal turn-completion signal. Fires once per assistant
			// turn after all incremental Content/Thinking deltas have been sent,
			// so UIs can authoritatively finalize the placeholder for this turn
			// (e.g., backfill content that providers delivered non-incrementally)
			// without inferring completion from a scalar return value.
			var amuIn, amuOut int
			if resp.Usage != nil {
				amuIn = resp.Usage.Input
				amuOut = resp.Usage.Output
			}
			a.callIntermediateCallback(ctx, AssistantMessageUpdate{
				Content:      resp.Message.Content,
				Thinking:     resp.Message.Thinking,
				FinishReason: string(resp.FinishReason),
				Turn:         a.turnCount,
				InputTokens:  amuIn,
				OutputTokens: amuOut,
				Sequence:     a.nextUpdateSequence(),
			})

			// Save assistant message via callback if provided
			if a.messageCallback != nil {
				if err := a.messageCallback(ctx, resp.Message); err != nil {
					a.logger.Error(ctx, "agent.message_callback_failed",
						observability.F("error", err.Error()))
				}
			}
		} else {
			// Should not happen with valid providers, but handle safely
			a.logger.Warn(ctx, "agent.provider_returned_nil_message",
				observability.F("agent_id", a.definition.ID))
			return "", provider.FinishReasonError, sdkerr.Wrap(fmt.Errorf("provider returned nil message"), "agent.provider_error",
				sdkerr.WithOperation("agent.provider_call"),
				sdkerr.WithComponent("agent"),
				sdkerr.WithTraceFromContext(ctx),
			)
		}

		// Check finish reason
		finishReason = resp.FinishReason

		a.logger.Debug(ctx, "agent.executeLoop.turn",
			observability.F("turn", a.turnCount),
			observability.F("finish_reason", string(resp.FinishReason)),
			observability.F("tool_calls", len(resp.Message.ToolCalls)),
			observability.F("content_length", len(resp.Message.Content)),
		)

		switch resp.FinishReason {
		case provider.FinishReasonStop:
			// Natural completion - extract text response
			finalMessage = a.extractTextContent(resp.Message)
			if a.requestCompletionConfirmation(ctx, &messages, &completionConfirmCount) {
				continue
			}
			return finalMessage, finishReason, nil

		case provider.FinishReasonToolCalls:
			// Guard: If finish reason says tool_calls but message has none,
			// this is a provider/parsing mismatch. Treat as Stop to prevent
			// infinite empty loops.
			if len(resp.Message.ToolCalls) == 0 {
				a.logger.Warn(ctx, "agent.executeLoop.tool_calls_mismatch",
					observability.F("finish_reason", "tool_calls"),
					observability.F("actual_tool_calls", 0),
					observability.F("content_length", len(resp.Message.Content)),
					observability.F("turn", a.turnCount),
				)
				finalMessage = a.extractTextContent(resp.Message)
				if a.requestCompletionConfirmation(ctx, &messages, &completionConfirmCount) {
					continue
				}
				return finalMessage, provider.FinishReasonStop, nil
			}

			consecutiveTruncations = 0

			// Execute tools and continue loop
			toolResults, err := a.executeTools(ctx, resp.Message)
			if err != nil {
				// A hook-block or individual tool failure must NOT land here — those
				// are returned as tool-result messages (err == nil). Reaching this
				// means a structural failure (ctx cancelled, batch runtime error)
				// that aborts the whole turn. Trace it loudly.
				a.logger.Error(ctx, "agent.executeLoop.execute_tools_aborted",
					observability.F("turn", a.turnCount),
					observability.F("error", err.Error()),
				)
				return "", provider.FinishReasonError, sdkerr.Wrap(
					err,
					"agent.tool_execution.failed",
					sdkerr.WithOperation("agent.execute_tools"),
					sdkerr.WithComponent("agent"),
					sdkerr.WithTraceFromContext(ctx),
				)
			}

			a.logger.Debug(ctx, "agent.executeLoop.execute_tools_ok",
				observability.F("turn", a.turnCount),
				observability.F("result_messages", len(toolResults)),
			)

			// Add tool results to messages
			messages = append(messages, toolResults...)

			// Save tool result messages via callback if provided
			if a.messageCallback != nil {
				for _, msg := range toolResults {
					if err := a.messageCallback(ctx, msg); err != nil {
						a.logger.Error(ctx, "agent.message_callback_failed",
							observability.F("error", err.Error()))
					}
				}
			}

			// Continue loop for next turn

		case provider.FinishReasonLength:
			// A single response hit the per-response output-token cap. This
			// is recoverable inside an agentic loop — terminating here used
			// to abort whole background runs (e.g. the skill curator's
			// consolidation pass died mid SkillManage(create) with zero work
			// persisted). Give the model another turn with an explicit
			// notice; only bail out after repeated consecutive truncations.
			consecutiveTruncations++

			// If the provider still parsed complete tool calls out of the
			// truncated response, execute them — the work they represent is
			// intact even though trailing output was cut.
			if len(resp.Message.ToolCalls) > 0 {
				toolResults, err := a.executeTools(ctx, resp.Message)
				if err != nil {
					a.logger.Error(ctx, "agent.executeLoop.execute_tools_aborted",
						observability.F("turn", a.turnCount),
						observability.F("error", err.Error()),
					)
					return "", provider.FinishReasonError, sdkerr.Wrap(
						err,
						"agent.tool_execution.failed",
						sdkerr.WithOperation("agent.execute_tools"),
						sdkerr.WithComponent("agent"),
						sdkerr.WithTraceFromContext(ctx),
					)
				}
				messages = append(messages, toolResults...)
				if a.messageCallback != nil {
					for _, msg := range toolResults {
						if err := a.messageCallback(ctx, msg); err != nil {
							a.logger.Error(ctx, "agent.message_callback_failed",
								observability.F("error", err.Error()))
						}
					}
				}
				continue
			}

			if consecutiveTruncations >= maxConsecutiveTruncations {
				// Repeated truncation with no progress — summarize and stop
				// so the caller gets an honest account instead of a hang.
				partialContent := a.extractTextContent(resp.Message)
				summary, sumErr := a.doFinalSummarization(ctx, messages, "token_limit", partialContent)
				if sumErr != nil {
					a.logger.Warn(ctx, "agent.summarization_failed",
						observability.F("reason", "token_limit"),
						observability.F("error", sumErr.Error()),
					)
					if partialContent != "" {
						return partialContent, finishReason, nil
					}
					return "[Response truncated due to token limit]", finishReason, nil
				}
				return summary, provider.FinishReasonStop, nil
			}

			a.logger.Info(ctx, "agent.executeLoop.truncation_recovery",
				observability.F("turn", a.turnCount),
				observability.F("consecutive", consecutiveTruncations),
			)
			messages = append(messages, &conversation.Message{
				Role: conversation.RoleUser,
				Content: "[SYSTEM NOTICE: Your previous response was truncated because it hit the " +
					"per-response output-token limit, and any partial tool call in it was discarded. " +
					"Continue the task, keeping each response smaller: split large writes into several " +
					"steps (e.g. create with a shorter body, then extend it with follow-up patch/append " +
					"calls) instead of emitting everything in one response.]",
				Timestamp: time.Now(),
			})

		case provider.FinishReasonContentFilter:
			// Content was filtered
			if a.auditor != nil {
				a.auditor.Record(ctx, observability.AuditEvent{
					EventType:      observability.AuditEventSteeringIntervention,
					Actor:          "system",
					Action:         "content_filter",
					Resource:       a.definition.ID,
					Outcome:        observability.AuditOutcomeBlocked,
					ConversationID: a.conversationID,
					Details: map[string]any{
						"reason": "content_policy_violation",
					},
				})
			}
			return "", finishReason, sdkerr.Steering(
				"agent.content_filtered",
				"response was filtered by content policy",
				sdkerr.WithOperation("agent.provider_call"),
				sdkerr.WithComponent("agent"),
				sdkerr.WithTraceFromContext(ctx),
			)

		case provider.FinishReasonError:
			// Provider error
			return "", finishReason, sdkerr.Wrap(fmt.Errorf("provider returned error finish reason"), "agent.provider_error",
				sdkerr.WithOperation("agent.provider_call"),
				sdkerr.WithComponent("agent"),
				sdkerr.WithTraceFromContext(ctx),
			)

		default:
			// Unknown finish reason
			finalMessage = a.extractTextContent(resp.Message)
			return finalMessage, finishReason, nil
		}
	}

	// Exceeded max turns - do a final summarization call so the agent can
	// communicate what it accomplished and what work remains
	if a.auditor != nil {
		a.auditor.Record(ctx, observability.AuditEvent{
			EventType:      observability.AuditEventSteeringIntervention,
			Actor:          "system",
			Action:         "max_turns_limit",
			Resource:       a.definition.ID,
			Outcome:        observability.AuditOutcomeBlocked,
			ConversationID: a.conversationID,
			Details: map[string]any{
				"max_turns":    maxTurns,
				"actual_turns": a.turnCount,
			},
		})
	}

	a.logger.Info(ctx, "agent.max_turns_reached.summarizing",
		observability.F("max_turns", maxTurns),
		observability.F("actual_turns", a.turnCount),
	)

	summary, sumErr := a.doFinalSummarization(ctx, messages, "max_turns",
		fmt.Sprintf("Used %d of %d allowed turns.", a.turnCount, maxTurns))
	if sumErr != nil {
		// If summarization fails, return what we have with context
		a.logger.Warn(ctx, "agent.summarization_failed",
			observability.F("reason", "max_turns"),
			observability.F("error", sumErr.Error()),
		)
		if finalMessage != "" {
			return finalMessage, provider.FinishReasonStop, nil
		}
		return fmt.Sprintf("[Agent reached turn limit (%d turns). Last output may be incomplete.]", maxTurns),
			provider.FinishReasonStop, nil
	}
	return summary, provider.FinishReasonStop, nil
}

const completionConfirmationMessage = "You indicated the task is complete. Before finishing, carefully RE-VERIFY against the ACTUAL current state: run the relevant tests/build/checks and inspect the files or command output you changed. If ANYTHING is missing, wrong, or untested, keep working now. If it is genuinely complete and verified, briefly confirm and stop."

func (a *Agent) requestCompletionConfirmation(
	ctx context.Context,
	messages *[]*conversation.Message,
	confirmCount *int,
) bool {
	if !a.completionConfirm || *confirmCount >= a.completionConfirmMax {
		return false
	}

	msg := &conversation.Message{
		Role:      conversation.RoleUser,
		Content:   completionConfirmationMessage,
		Timestamp: time.Now(),
	}
	*messages = append(*messages, msg)
	*confirmCount++
	prompttrace.From(ctx).AppendMsg(
		"completion_confirmation",
		string(msg.Role),
		msg.Content,
		len(*messages)-1,
	)
	if a.messageCallback != nil {
		if err := a.messageCallback(ctx, msg); err != nil {
			a.logger.Error(ctx, "agent.completion_confirmation_save_failed",
				observability.F("error", err.Error()))
		}
	}
	a.logger.Info(ctx, "agent.completion_confirmation_requested",
		observability.F("count", *confirmCount),
		observability.F("max", a.completionConfirmMax))
	return true
}

// ─────────────────────────────────────────────────────────────
// MANAGED EXECUTION BRIDGE
// ─────────────────────────────────────────────────────────────

// executeManaged runs the agent on a remote managed compute node.
// This implements the same flow as Anthropic's Managed Agents API:
// 1. Create/configure managed client (or reuse existing)
// 2. Boot a session (container) if not already running
// 3. Send user message as an event
// 4. Stream SSE events back to local SDK callbacks
// 5. Return final response
//
// Sessions are kept alive across multiple Execute() calls to enable
// multi-turn conversations without re-booting containers.
func (a *Agent) executeManaged(ctx context.Context, req ExecuteRequest) (*ExecuteResponse, error) {
	startTime := time.Now()

	// Validate managed config
	cfg := a.definition.ManagedConfig
	if cfg == nil {
		return nil, sdkerr.Permanent("agent.managed_config_missing",
			"managed execution mode requires ManagedConfig",
			sdkerr.WithOperation("agent.execute_managed"),
			sdkerr.WithComponent("agent"),
			sdkerr.WithTraceFromContext(ctx))
	}
	if cfg.Endpoint == "" {
		return nil, sdkerr.Permanent("agent.managed_endpoint_missing",
			"managed execution requires an endpoint URL",
			sdkerr.WithOperation("agent.execute_managed"),
			sdkerr.WithComponent("agent"),
			sdkerr.WithTraceFromContext(ctx))
	}
	if cfg.APIKey == "" {
		return nil, sdkerr.Permanent("agent.managed_apikey_missing",
			"managed execution requires an API key",
			sdkerr.WithOperation("agent.execute_managed"),
			sdkerr.WithComponent("agent"),
			sdkerr.WithTraceFromContext(ctx))
	}

	// Check if we have an existing session to reuse
	a.mu.RLock()
	existingClient := a.managedClient
	existingSessionID := a.managedSessionID
	existingSessionPort := a.managedSessionPort
	a.mu.RUnlock()

	var managedClient *managed.Client
	var sessionID string
	var sessionPort int

	// Determine session ID from config or reuse existing
	if cfg.SessionID != "" {
		sessionID = cfg.SessionID
	} else if existingSessionID != "" {
		sessionID = existingSessionID
	} else {
		sessionID = fmt.Sprintf("sess_%s_%d", a.definition.ID, time.Now().UnixNano())
	}

	// Determine project ID
	projectID := cfg.ProjectID
	if projectID == "" {
		projectID = "default"
	}

	// Reuse existing client and session if available and healthy
	if existingClient != nil && existingSessionID == sessionID {
		// Check if session is still healthy
		if healthy, _ := existingClient.IsSessionHealthy(ctx, sessionID); healthy {
			a.logger.Info(ctx, "agent.managed.reusing_session",
				observability.F("session_id", sessionID),
				observability.F("port", existingSessionPort))
			managedClient = existingClient
			sessionPort = existingSessionPort
		} else {
			// Session is dead, need to re-boot
			a.logger.Warn(ctx, "agent.managed.session_dead",
				observability.F("session_id", sessionID))
			managedClient = existingClient
		}
	} else {
		// Create new managed client
		newClient, err := managed.NewClient(managed.Config{
			Endpoint: cfg.Endpoint,
			APIKey:   cfg.APIKey,
			Logger:   nil,
		})
		if err != nil {
			return nil, sdkerr.Wrap(err, "agent.managed_client_failed",
				sdkerr.WithOperation("agent.execute_managed"),
				sdkerr.WithComponent("agent"),
				sdkerr.WithTraceFromContext(ctx))
		}
		managedClient = newClient
	}

	// Boot session if we don't have a port yet
	if sessionPort == 0 {
		// Initialize project if needed (idempotent)
		if err := managedClient.InitProject(ctx, projectID); err != nil {
			a.logger.Warn(ctx, "agent.managed.project_init_warning",
				observability.F("error", err.Error()),
				observability.F("project_id", projectID))
		}

		// Boot session (container)
		sessionInfo, err := managedClient.BootSession(ctx, sessionID, projectID)
		if err != nil {
			return nil, sdkerr.Wrap(err, "agent.managed.boot_failed",
				sdkerr.WithOperation("agent.execute_managed"),
				sdkerr.WithComponent("agent"),
				sdkerr.WithTraceFromContext(ctx))
		}

		sessionPort = sessionInfo.Port

		a.logger.Info(ctx, "agent.managed.session_booted",
			observability.F("session_id", sessionID),
			observability.F("project_id", projectID),
			observability.F("status", sessionInfo.Status),
			observability.F("port", sessionPort))
	}

	// NOTE: We do NOT automatically stop the session here.
	// Sessions should stay running for the duration of the conversation.
	// The session will be stopped either:
	// 1. Explicitly by the caller using managedClient.StopSession()
	// 2. Automatically by the orchestrator after the 25-minute timeout
	// 3. When the agent's Close() method is called
	//
	// This allows multiple Execute() calls to reuse the same session,
	// enabling multi-turn conversations without re-booting containers.

	// Store the managed client and session ID on the agent for reuse
	a.mu.Lock()
	a.managedClient = managedClient
	a.managedSessionID = sessionID
	a.managedSessionPort = sessionPort
	a.mu.Unlock()

	// Start streaming with the message (POST to /v1/stream)
	eventCh, err := managedClient.StreamWithMessage(ctx, sessionID, a.definition.Model, req.Message)
	if err != nil {
		return nil, sdkerr.Wrap(err, "agent.managed.stream_failed",
			sdkerr.WithOperation("agent.execute_managed"),
			sdkerr.WithComponent("agent"),
			sdkerr.WithTraceFromContext(ctx))
	}

	// Process SSE events and convert to local SDK callbacks
	var (
		finalContent string
		thinking     string
		finishReason provider.FinishReason = provider.FinishReasonStop
		inputTokens  int
		outputTokens int
		toolCalls    int
		toolResults  int
	)

EventLoop:
	for event := range eventCh {
		// Log each event for debugging
		a.logger.Debug(ctx, "agent.managed.event",
			observability.F("type", event.Type),
			observability.F("content_len", len(event.Content)))

		// Convert managed event types to local SDK callbacks
		switch event.Type {
		case "content_block_delta":
			// Streaming text content
			if event.Data != nil {
				if text, ok := event.Data["text"].(string); ok && text != "" {
					finalContent += text
					// Emit to local callback for real-time UI updates
					a.callIntermediateCallback(ctx, ContentUpdate{
						Content:  text,
						Append:   len(finalContent) > len(text),
						Sequence: a.nextUpdateSequence(),
					})
				}
			}

		case "thinking_delta":
			// Extended thinking content
			if event.Data != nil {
				if text, ok := event.Data["thinking"].(string); ok && text != "" {
					thinking += text
					a.callIntermediateCallback(ctx, ThinkingUpdate{
						Content:  text,
						Append:   len(thinking) > len(text),
						Sequence: a.nextUpdateSequence(),
					})
				}
			}

		case "thinking":
			// Extended thinking content (mock server format)
			if event.Content != "" {
				thinking += event.Content
				a.callIntermediateCallback(ctx, ThinkingUpdate{
					Content:  event.Content,
					Append:   len(thinking) > len(event.Content),
					Sequence: a.nextUpdateSequence(),
				})
			} else if event.Data != nil {
				if text, ok := event.Data["content"].(string); ok && text != "" {
					thinking += text
					a.callIntermediateCallback(ctx, ThinkingUpdate{
						Content:  text,
						Append:   len(thinking) > len(text),
						Sequence: a.nextUpdateSequence(),
					})
				}
			}

		case "content", "text":
			// Regular content (mock server and Anthropic format)
			if event.Content != "" {
				finalContent += event.Content
				a.callIntermediateCallback(ctx, ContentUpdate{
					Content:  event.Content,
					Append:   len(finalContent) > len(event.Content),
					Sequence: a.nextUpdateSequence(),
				})
			} else if event.Data != nil {
				if text, ok := event.Data["content"].(string); ok && text != "" {
					finalContent += text
					a.callIntermediateCallback(ctx, ContentUpdate{
						Content:  text,
						Append:   len(finalContent) > len(text),
						Sequence: a.nextUpdateSequence(),
					})
				}
			}

		case "done", "complete":
			// Final chunk (mock server format)
			finishReason = provider.FinishReasonStop
			if event.Data != nil {
				if in, ok := event.Data["input_tokens"].(float64); ok {
					inputTokens = int(in)
				}
				if out, ok := event.Data["output_tokens"].(float64); ok {
					outputTokens = int(out)
				}
			}

		case "tool_use":
			// Tool call being made
			toolCalls++
			var toolName string
			if event.Data != nil {
				if name, ok := event.Data["name"].(string); ok {
					toolName = name
				}
			}
			a.callIntermediateCallback(ctx, ToolCallUpdate{
				Name:     toolName,
				Sequence: a.nextUpdateSequence(),
			})

		case "tool_result":
			// Tool execution completed
			toolResults++
			var toolName string
			if event.Data != nil {
				if name, ok := event.Data["name"].(string); ok {
					toolName = name
				}
			}
			outputMsg := "Tool executed successfully"
			if toolName != "" {
				outputMsg = fmt.Sprintf("Tool '%s' executed successfully", toolName)
			}
			a.callIntermediateCallback(ctx, ToolResultUpdate{
				ID:       fmt.Sprintf("tool_%d", toolResults),
				Output:   outputMsg,
				Sequence: a.nextUpdateSequence(),
			})

		case "message_delta":
			// Token usage update
			if event.Data != nil {
				if usage, ok := event.Data["usage"].(map[string]any); ok {
					if in, ok := usage["input_tokens"].(float64); ok {
						inputTokens = int(in)
					}
					if out, ok := usage["output_tokens"].(float64); ok {
						outputTokens = int(out)
					}
					// Emit token count update
					a.callIntermediateCallback(ctx, TokenCountUpdate{
						InputTokens:   inputTokens,
						OutputTokens:  outputTokens,
						Turn:          1,
						ContextWindow: a.getContextWindow(),
					})
				}
			}

		case "message_stop":
			// Message complete - check finish reason
			if event.Data != nil {
				if reason, ok := event.Data["finish_reason"].(string); ok {
					switch reason {
					case "stop":
						finishReason = provider.FinishReasonStop
					case "tool_calls":
						finishReason = provider.FinishReasonToolCalls
					case "length":
						finishReason = provider.FinishReasonLength
					case "content_filter":
						finishReason = provider.FinishReasonContentFilter
					}
				}
			}

		case "error":
			// Error from managed service
			errMsg := "unknown error"
			if event.Data != nil {
				if msg, ok := event.Data["message"].(string); ok {
					errMsg = msg
				}
			}
			return nil, sdkerr.Permanent("agent.managed.error",
				errMsg,
				sdkerr.WithOperation("agent.execute_managed"),
				sdkerr.WithComponent("agent"),
				sdkerr.WithTraceFromContext(ctx))

		case "session_status":
			// Status update from session
			if event.Data != nil {
				if status, ok := event.Data["status"].(string); ok {
					a.logger.Info(ctx, "agent.managed.status",
						observability.F("status", status))
					// If session goes idle or completes, we're done
					if status == "idle" || status == "completed" {
						break EventLoop
					}
				}
			}
		}
	}

	// Terminal turn-completion signal for the managed path. Mirrors the
	// emission in executeLoop so consumers see the same finalization event
	// regardless of which execution backend ran the turn.
	a.callIntermediateCallback(ctx, AssistantMessageUpdate{
		Content:      finalContent,
		Thinking:     thinking,
		FinishReason: string(finishReason),
		Turn:         1,
		InputTokens:  inputTokens,
		OutputTokens: outputTokens,
		Sequence:     a.nextUpdateSequence(),
	})

	// Build final response
	duration := time.Since(startTime)

	response := &ExecuteResponse{
		Message:        finalContent,
		ConversationID: sessionID,
		TurnCount:      1,
		InputTokens:    inputTokens,
		OutputTokens:   outputTokens,
		TokensUsed:     inputTokens + outputTokens,
		CostUSD:        0, // TODO: Calculate based on model pricing
		Duration:       duration,
		FinishReason:   finishReason,
		Metadata: map[string]any{
			"managed_session_id": sessionID,
			"managed_project_id": projectID,
			"managed_endpoint":   cfg.Endpoint,
			"tool_calls":         toolCalls,
			"tool_results":       toolResults,
			"thinking":           thinking,
		},
	}

	// Log completion
	a.logger.Info(ctx, "agent.managed.completed",
		observability.F("session_id", sessionID),
		observability.F("duration_ms", duration.Milliseconds()),
		observability.F("input_tokens", inputTokens),
		observability.F("output_tokens", outputTokens),
		observability.F("finish_reason", string(finishReason)))

	return response, nil
}
