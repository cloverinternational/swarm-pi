package agent

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"time"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/conversation"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/fallback"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/observability"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/provider"
	sdkerr "github.com/Swarm-Code/mono/swarm-sdk/internal/sdkerr"
)

const harnessChainProviderConfigsMetadataKey = "harness.chain_provider_configs"

// SetChainProviderConfigs installs sealed provider configuration for exact
// fallback-chain entries. Non-harness callers never invoke this method and
// retain the existing ambient provider behavior.
func (a *Agent) SetChainProviderConfigs(cfgs map[string]provider.Config) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.definition == nil {
		return
	}
	if len(cfgs) == 0 {
		if a.definition.Metadata != nil {
			delete(a.definition.Metadata, harnessChainProviderConfigsMetadataKey)
		}
		return
	}
	if a.definition.Metadata == nil {
		a.definition.Metadata = make(map[string]any, 1)
	}
	cp := make(map[string]provider.Config, len(cfgs))
	maps.Copy(cp, cfgs)
	a.definition.Metadata[harnessChainProviderConfigsMetadataKey] = cp
}

func (a *Agent) chainProviderConfig(providerName, modelID string) (provider.Config, bool) {
	a.mu.RLock()
	defer a.mu.RUnlock()
	if a.definition == nil || a.definition.Metadata == nil {
		return provider.Config{}, false
	}
	raw, ok := a.definition.Metadata[harnessChainProviderConfigsMetadataKey]
	if !ok {
		return provider.Config{}, false
	}
	cfgs, ok := raw.(map[string]provider.Config)
	if !ok {
		return provider.Config{}, false
	}
	cfg, ok := cfgs[(fallback.ModelRef{Provider: providerName, Model: modelID}).Key()]
	return cfg, ok
}

// HasChainProviderConfig reports only whether an exact chain entry is sealed;
// it never exposes the credential-bearing configuration.
func (a *Agent) HasChainProviderConfig(providerName, modelID string) bool {
	_, ok := a.chainProviderConfig(providerName, modelID)
	return ok
}

// effectiveChain returns the chain that should actually be executed, after
// applying two safety filters:
//
//  1. No-fallback: when a.noFallback is set (caller pinned a provider/model and
//     wants the real primary error, not a cascade), every fallback is dropped.
//  2. Unregistered-provider pruning: any fallback whose provider is NOT
//     registered in the agent's provider registry is removed, with a logged
//     warning. This is the core of the "no-fallback safety" fix — without it a
//     pinned/primary failure cascades into an entry like `cerebras llama-3.3-70b`
//     on a box where cerebras isn't registered, and the surfaced error becomes
//     the misleading `provider "cerebras" not registered` instead of the real
//     primary failure.
//
// The PRIMARY entry is always preserved verbatim (even if its provider looks
// unregistered) so that its genuine error is what surfaces. Returns the
// original chain pointer unchanged when no filtering is needed, so the common
// case allocates nothing.
func (a *Agent) effectiveChain() *fallback.Chain {
	a.mu.RLock()
	src := a.chain
	noFallback := a.noFallback
	reg := a.providerRegistry
	a.mu.RUnlock()

	if src == nil {
		return nil
	}
	if len(src.Fallbacks) == 0 {
		return src // nothing to filter
	}

	if noFallback {
		a.logger.Info(a.ctx, "agent.chain.no_fallback",
			observability.F("primary", src.Primary.String()),
			observability.F("dropped_fallbacks", len(src.Fallbacks)))
		return &fallback.Chain{Primary: src.Primary}
	}

	// Prune fallbacks whose provider isn't registered. When reg is nil we can't
	// verify, so leave the chain untouched (preserve existing behaviour).
	if reg == nil {
		return src
	}
	kept := make([]fallback.ModelRef, 0, len(src.Fallbacks))
	for _, fb := range src.Fallbacks {
		if reg.IsRegistered(fb.Provider) {
			kept = append(kept, fb)
			continue
		}
		a.logger.Warn(a.ctx, "agent.chain.fallback_provider_unregistered",
			observability.F("provider", fb.Provider),
			observability.F("model", fb.Model),
			observability.F("action", "dropped_from_chain"))
	}
	if len(kept) == len(src.Fallbacks) {
		return src // all fallbacks valid — no copy needed
	}
	return &fallback.Chain{Primary: src.Primary, Fallbacks: kept}
}

// executeWithChain executes a chat request using the model pool chain.
// It handles fallbacks, retries, and provider switching automatically.
func (a *Agent) executeWithChain(ctx context.Context, req provider.ChatRequest) (*provider.ChatResponse, error) {
	// Start span for chain execution
	ctx, span := a.tracer.StartSpan(ctx, "agent.execute_with_chain")
	defer span.End()

	// Resolve the chain to actually execute: drops unregistered-provider
	// fallbacks (and all fallbacks when noFallback is set) so a pinned-model
	// failure surfaces the real error instead of "provider X not registered".
	execChain := a.effectiveChain()

	// Define execution function for the pool.
	// If a CredentialStore is available, this function iterates over available
	// credentials for the provider before returning an error. This allows
	// rotating tokens within the same provider (e.g., two Claude OAuth tokens)
	// before the outer fallback chain moves to a different provider entirely.
	execFn := func(attemptCtx context.Context, providerName, modelID string) (*provider.ChatResponse, error) {
		// Build the list of credentials to try.
		// Default: a single zero-value credential (uses provider's built-in auth).
		creds := []Credential{{Provider: providerName}}
		if a.credentialStore != nil {
			if providerCreds := a.credentialStore.Credentials(providerName); len(providerCreds) > 0 {
				creds = providerCreds
			}
		}

		var lastErr error
		for _, cred := range creds {
			provConfig := provider.Config{
				Name:  providerName,
				Model: modelID,
			}
			if sealedCfg, sealed := a.chainProviderConfig(providerName, modelID); sealed {
				provConfig.APIKey = sealedCfg.APIKey
				provConfig.BaseURL = sealedCfg.BaseURL
				provConfig.NoAmbientEnv = sealedCfg.NoAmbientEnv
				provConfig.HTTPMaxRetries = sealedCfg.HTTPMaxRetries
				if len(sealedCfg.Custom) > 0 {
					provConfig.Custom = maps.Clone(sealedCfg.Custom)
				}
			}
			// If the credential carries an explicit token, pass it via APIKey
			// and signal OAuth via the Custom map.
			if cred.Token != "" {
				provConfig.APIKey = cred.Token
				provConfig.Custom = map[string]any{
					"is_oauth":   cred.IsOAuth,
					"account_id": cred.AccountID,
				}
			}

			prov, err := a.providerRegistry.Create(provConfig)
			if err != nil {
				lastErr = err
				continue
			}

			// Override request model with the chain entry's model.
			attemptReq := req
			attemptReq.Model = modelID

			// Use streaming if the provider supports it. Some providers
			// (e.g. Fireworks) require stream=true for large max_tokens
			// and will reject synchronous requests with HTTP 400.
			var resp *provider.ChatResponse
			if prov.Capabilities().Streaming {
				resp, err = a.streamChatWithProvider(attemptCtx, prov, attemptReq)
			} else {
				resp, err = prov.Chat(attemptCtx, attemptReq)
			}
			if err == nil {
				return resp, nil
			}

			lastErr = err

			// Check if we should rotate to the next credential for this provider.
			if a.credentialStore != nil && a.credentialStore.ShouldRotate(err) {
				a.credentialStore.MarkCooldown(providerName, cred.AccountID)
				// Emit credential rotation event.
				// AttemptIndex = -1 signals credential rotation (not chain rotation).
				a.callIntermediateCallback(ctx, FallbackUpdate{
					Provider:     providerName,
					Model:        modelID,
					AttemptIndex: -1,
					Status:       "credential_rotated",
					Error:        err,
				})
				continue // try next credential
			}

			// Non-rotatable error — let the outer chain handle it.
			return nil, err
		}
		return nil, lastErr
	}

	// Track primary info for fallback notifications
	primary := execChain.Primary

	onProgress := func(attempt fallback.AttemptInfo) {
		if attempt.Error == nil && attempt.Duration == 0 {
			// Before attempt — emit "attempting"
			a.callIntermediateCallback(ctx, FallbackUpdate{
				Provider:     attempt.Provider,
				Model:        attempt.Model,
				AttemptIndex: attempt.Index,
				Status:       "attempting",
			})
		} else if attempt.Error != nil {
			// After failed attempt
			a.callIntermediateCallback(ctx, FallbackUpdate{
				Provider:     attempt.Provider,
				Model:        attempt.Model,
				AttemptIndex: attempt.Index,
				Status:       "failed",
				Error:        attempt.Error,
				Duration:     attempt.Duration,
			})
		} else {
			// After successful attempt
			update := FallbackUpdate{
				Provider:     attempt.Provider,
				Model:        attempt.Model,
				AttemptIndex: attempt.Index,
				Status:       "success",
				Duration:     attempt.Duration,
			}
			if attempt.Index > 0 {
				update.FromProvider = primary.Provider
				update.FromModel = primary.Model
			}
			a.callIntermediateCallback(ctx, update)
		}
	}

	// Use pool executor from fallback package
	var chainTimeout time.Duration
	if a.definition.Capabilities != nil {
		chainTimeout = a.definition.Capabilities.Timeout
	}

	// onExhausted is called when every entry in the chain has failed.
	// If a TUI callback is registered it emits ExhaustedUpdate, which opens the
	// profile picker modal and blocks until the user makes a choice.
	// In headless mode (no callback) it returns nil immediately, letting the
	// normal failure path handle the error.
	onExhausted := func(exhaustCtx context.Context, r fallback.Result) *fallback.Chain {
		if a.intermediateCallback == nil || a.isHeadless {
			return nil // headless — nothing to ask
		}

		errs := make([]error, len(r.Attempts))
		for i, att := range r.Attempts {
			errs[i] = att.Error
		}

		// Buffered so the send never blocks if the agent context is already
		// cancelled by the time the user responds (or never responds).
		respCh := make(chan FallbackDecision, 1)

		a.callIntermediateCallback(exhaustCtx, ExhaustedUpdate{
			Provider: execChain.Primary.Provider,
			Model:    execChain.Primary.Model,
			Attempts: len(r.Attempts),
			Errors:   errs,
			Response: respCh,
		})

		// Block until the TUI sends a decision or the context expires.
		//
		// SAFETY TIMEOUT: an interactive TUI answers this prompt, but a daemon /
		// PWA / SSE client has an intermediateCallback yet NO way to send a
		// FallbackDecision — so without a bound this select blocks FOREVER when
		// the provider chain exhausts (e.g. a failed provider request), which
		// leaves the agent stuck in StateExecuting and wedges every future turn
		// (Execute never returns, so the idle-reset defer never runs). Cap the
		// wait so a non-responding UI degrades to a normal chain-exhausted error
		// instead of a permanent hang.
		const exhaustedResponseTimeout = 25 * time.Second
		select {
		case <-exhaustCtx.Done():
			return nil
		case <-time.After(exhaustedResponseTimeout):
			a.logger.Warn(exhaustCtx, "agent.fallback.exhausted_no_response",
				observability.F("timeout", exhaustedResponseTimeout.String()))
			return nil
		case decision := <-respCh:
			if decision.Cancel || decision.NewChain == nil {
				return nil
			}
			// Apply the new chain immediately so the retry uses it and
			// subsequent turns in this session also use it.
			a.SetChain(decision.NewChain)
			return decision.NewChain
		}
	}

	resp, result := fallback.ExecuteWithResult[*provider.ChatResponse](ctx, execChain, execFn, fallback.ExecuteOptions{
		OnProgress:  onProgress,
		Timeout:     chainTimeout,
		OnExhausted: onExhausted,
	})

	if !result.Success {
		return nil, result.FinalError
	}

	return resp, nil
}

// streamChat streams a request using the agent's default provider.
func (a *Agent) streamChat(ctx context.Context, req provider.ChatRequest) (*provider.ChatResponse, error) {
	return a.streamChatWithProvider(ctx, a.provider, req)
}

// streamChatWithProvider streams a request through the given provider and accumulates
// the result into a ChatResponse, emitting intermediate callbacks (content, thinking,
// token updates) along the way.
// This is used both by the normal execution path (via streamChat) and by the fallback
// chain execution path (executeWithChain) so that providers which require streaming
// (e.g. Fireworks for max_tokens > 4096) work correctly through all code paths.
func (a *Agent) streamChatWithProvider(ctx context.Context, prov provider.Provider, req provider.ChatRequest) (*provider.ChatResponse, error) {
	requestStart := time.Now()
	stream, err := prov.Stream(ctx, req)
	if err != nil {
		if sdkerr.IsRetryable(err) || sdkerr.IsTransient(err) {
			a.logger.Warn(ctx, "agent.streamChat.stream_error_fallback_to_chat",
				observability.F("error", err.Error()),
			)
			if resp, chatErr := prov.Chat(ctx, req); chatErr == nil {
				return resp, nil
			}
		}
		a.logger.Error(ctx, "agent.streamChat.stream_error",
			observability.F("error", err.Error()),
		)
		return nil, sdkerr.Wrap(
			err,
			"agent.stream.request_failed",
			sdkerr.WithOperation("agent.stream_chat"),
			sdkerr.WithComponent("agent"),
			sdkerr.WithTraceFromContext(ctx),
		)
	}

	var (
		accumulatedContent  string
		accumulatedThinking string
		thinkingSignature   string
		toolCalls           []conversation.ToolCall
		orderedBlocks       []conversation.MessageBlock // provider-native block ordering; nil when provider hasn't upgraded
		usage               *conversation.TokenUsage
		finishReason        provider.FinishReason
		firstChunk          = true
		firstThinkingChunk  = true         // tracks whether a ThinkingUpdate has been emitted yet
		responseMetadata    map[string]any // Captures cache metrics and other metadata
		streamHadNonContent bool           // Avoid fallback if tools/thinking already streamed
	)

	for chunk := range stream {
		a.logger.Info(ctx, "agent.streamChat.chunk_received",
			observability.F("delta_length", len(chunk.Delta)),
			observability.F("thinking_length", len(chunk.Thinking)),
			observability.F("tool_calls", len(chunk.ToolCalls)),
			observability.F("has_usage", chunk.Usage != nil),
			observability.F("done", chunk.Done),
			observability.F("has_error", chunk.Error != nil),
		)
		if chunk.Error != nil {
			if !streamHadNonContent &&
				!(errors.Is(chunk.Error, context.Canceled) || errors.Is(chunk.Error, context.DeadlineExceeded)) &&
				(sdkerr.IsRetryable(chunk.Error) || sdkerr.IsTransient(chunk.Error)) {
				a.logger.Warn(ctx, "agent.streamChat.chunk_error_fallback_to_chat",
					observability.F("error", chunk.Error.Error()),
					observability.F("partial_chars", len(accumulatedContent)),
				)
				// Use prov (the provider serving this stream), not a.provider (the
				// default primary). When the chain has already switched to a fallback,
				// retrying against a.provider sends the request to the wrong endpoint.
				if resp, chatErr := prov.Chat(ctx, req); chatErr == nil {
					if resp != nil && resp.Message != nil && resp.Message.Content != "" {
						a.callIntermediateCallback(ctx, ContentUpdate{
							Content:  resp.Message.Content,
							Append:   false,
							Sequence: a.nextUpdateSequence(),
						})
					}
					if resp != nil && resp.Message != nil && resp.Message.Thinking != "" {
						a.callIntermediateCallback(ctx, ThinkingUpdate{
							Content:  resp.Message.Thinking,
							Append:   false,
							Sequence: a.nextUpdateSequence(),
						})
					}
					return resp, nil
				}
			}
			a.logger.Error(ctx, "agent.streamChat.chunk_error",
				observability.F("error", chunk.Error.Error()),
			)
			return nil, chunk.Error
		}

		// Handle content delta
		if chunk.Delta != "" {
			accumulatedContent += chunk.Delta

			delta := chunk.Delta
			// Add spacing for first chunk of subsequent turns
			if firstChunk && a.turnCount > 1 {
				delta = "\n\n" + delta
			}

			// Logic for append: if this is the VERY FIRST chunk of the turn, append=false (replace).
			// If we are in streamChat, we are emitting chunks.
			// Turn 1, Chunk 1: Append=false.
			// Turn 1, Chunk 2: Append=true.
			// Turn 2, Chunk 1: Append=true (append to Turn 1).

			shouldAppend := !firstChunk || a.turnCount > 1

			a.logger.Info(ctx, "agent.streamChat.content_callback_start",
				observability.F("delta_length", len(delta)),
				observability.F("append", shouldAppend),
			)
			a.callIntermediateCallback(ctx, ContentUpdate{
				Content:  delta,
				Append:   shouldAppend,
				Sequence: a.nextUpdateSequence(),
			})
			a.logger.Info(ctx, "agent.streamChat.content_callback_done",
				observability.F("delta_length", len(delta)),
				observability.F("append", shouldAppend),
			)
			firstChunk = false
		}

		// Handle thinking content (for providers that support extended thinking).
		// Emit ThinkingUpdate immediately so consumers (TUI, agent.Stream channel)
		// receive thinking in the correct order relative to text chunks.
		// Without this call, ThinkingUpdate is only fired by executeLoop after
		// streamChat returns — placing it after all ContentUpdate emissions and
		// causing the TUI to render thinking only once the full response is done.
		if chunk.Thinking != "" {
			accumulatedThinking += chunk.Thinking
			a.callIntermediateCallback(ctx, ThinkingUpdate{
				Content:  chunk.Thinking,
				Append:   !firstThinkingChunk,
				Sequence: a.nextUpdateSequence(),
			})
			firstThinkingChunk = false
			streamHadNonContent = true
		}
		if chunk.ThinkingSignature != "" {
			thinkingSignature = chunk.ThinkingSignature
			streamHadNonContent = true
		}

		// Handle tool calls
		if len(chunk.ToolCalls) > 0 {
			toolCalls = append(toolCalls, chunk.ToolCalls...)
			streamHadNonContent = true
		}

		// Capture provider-native block ordering. Providers emit this only on
		// the final chunk (Done=true) — the same event that carries ToolCalls.
		// Overwrite (not append) so we always use the authoritative final list.
		if len(chunk.OrderedBlocks) > 0 {
			orderedBlocks = chunk.OrderedBlocks
		}

		// Handle metadata
		if chunk.Usage != nil {
			usage = chunk.Usage
		}
		// Emit real-time token count update for any chunk that carries usage data.
		// Works for all providers: Anthropic emits on message_start (input known instantly)
		// and message_delta (output grows during streaming); OpenAI, Gemini, etc. emit at
		// stream end. This fires a TokenCountUpdate immediately rather than waiting for
		// streamChat to return, so the TUI token display updates mid-stream on every turn.
		if chunk.Usage != nil {
			inputTok := chunk.Usage.InputContextSize()
			outputTok := chunk.Usage.Output
			ctxWin := a.getContextWindow()
			var pct float64
			if ctxWin > 0 && inputTok > 0 {
				pct = float64(inputTok) / float64(ctxWin) * 100
			}
			a.callIntermediateCallback(ctx, TokenCountUpdate{
				InputTokens:          inputTok,
				OutputTokens:         outputTok,
				Turn:                 a.turnCount,
				ContextWindow:        ctxWin,
				EffectiveWindow:      a.getEffectiveInputWindow(),
				AutoCompactThreshold: a.getAutoCompactThreshold(),
				PctUsed:              pct,
				CacheReadTokens:      chunk.Usage.CacheRead,
				CacheCreationTokens:  chunk.Usage.CacheCreation,
				UncachedInputTokens:  chunk.Usage.Input,
			})
			a.logger.Debug(ctx, "agent.streamChat.token_update",
				observability.F("input_tokens", inputTok),
				observability.F("output_tokens", outputTok),
				observability.F("context_window", ctxWin),
				observability.F("pct_used", fmt.Sprintf("%.1f%%", pct)),
				observability.F("turn", a.turnCount),
			)
		}
		if chunk.FinishReason != "" {
			finishReason = chunk.FinishReason
		}
		// Handle metadata — merge (not replace) so that fields from earlier chunks
		// (e.g. message_id from message_start) are not lost when a later chunk
		// carries the raw_payload.
		if chunk.Metadata != nil {
			if responseMetadata == nil {
				responseMetadata = make(map[string]any, len(chunk.Metadata))
			}
			maps.Copy(responseMetadata, chunk.Metadata)
		}
	}

	// Construct response
	msg := &conversation.Message{
		Role:          conversation.RoleAssistant,
		Content:       accumulatedContent,
		ToolCalls:     toolCalls,
		Thinking:      accumulatedThinking,
		OrderedBlocks: orderedBlocks, // nil when provider hasn't populated; derivation fallback applies
		Timestamp:     time.Now(),
	}

	// Store thinking signature in metadata for translateMessages to use
	if thinkingSignature != "" {
		msg.Metadata = map[string]any{
			"thinking_signature": thinkingSignature,
		}
	}

	// Preserve the raw ordered Codex output items (encrypted reasoning +
	// paired items) on the message so the codex provider can replay them
	// verbatim, in original order, next turn (store:false).
	if outputItems, ok := responseMetadata["codex_output_items"]; ok {
		if msg.Metadata == nil {
			msg.Metadata = make(map[string]any, 1)
		}
		msg.Metadata["codex_output_items"] = outputItems
	}

	// CRITICAL FIX: Override finish reason if there are tool calls.
	// Gemini API returns "STOP" as the finish reason even when it wants to call tools.
	// This differs from OpenAI ("tool_calls") and Anthropic ("tool_use") which have
	// explicit finish reasons for tool calls. Without this fix, the agent loop sees
	// FinishReasonStop and terminates instead of executing the requested tools.
	if len(toolCalls) > 0 && finishReason == provider.FinishReasonStop {
		finishReason = provider.FinishReasonToolCalls
	}

	// Debug: Log what we're returning (elevated to Warn for empty/mismatched responses)
	if len(toolCalls) == 0 && finishReason == provider.FinishReasonToolCalls {
		a.logger.Warn(ctx, "agent.streamChat.MISMATCH_finish_reason_tool_calls_but_empty",
			observability.F("content_length", len(accumulatedContent)),
			observability.F("tool_calls", 0),
			observability.F("finish_reason", string(finishReason)),
		)
	} else if len(accumulatedContent) == 0 && len(toolCalls) == 0 {
		a.logger.Warn(ctx, "agent.streamChat.EMPTY_response",
			observability.F("content_length", 0),
			observability.F("tool_calls", 0),
			observability.F("finish_reason", string(finishReason)),
		)
	} else {
		a.logger.Debug(ctx, "agent.streamChat.complete",
			observability.F("content_length", len(accumulatedContent)),
			observability.F("tool_calls", len(toolCalls)),
			observability.F("finish_reason", string(finishReason)),
			observability.F("has_usage", usage != nil),
		)
	}

	// Emit provider.after_response so bronze captures per-request latency + token data.
	if a.hooksManager != nil {
		var inputTok, outputTok int
		if usage != nil {
			inputTok = usage.Input
			outputTok = usage.Output
		}
		a.hooksManager.EmitProviderResponse(ctx, prov.Name(), req.Model, inputTok, outputTok, time.Since(requestStart).Milliseconds())
	}

	return &provider.ChatResponse{
		Message:         msg,
		FinishReason:    finishReason,
		Usage:           usage,
		Metadata:        responseMetadata, // Pass through cache metrics and other metadata
		StreamedContent: true,             // Content was delivered via streaming callbacks
	}, nil
}
