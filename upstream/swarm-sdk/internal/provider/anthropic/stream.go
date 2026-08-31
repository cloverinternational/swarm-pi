package anthropic

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/conversation"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/envelope"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/observability"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/provider"
	sdkerr "github.com/Swarm-Code/mono/swarm-sdk/internal/sdkerr"
)

// pendingBlock holds the partial state of a single Anthropic content block
// (keyed by the SSE block_index) while it is being assembled from deltas.
// It is the minimum state needed to preserve provider-native ordering without
// disturbing the existing flat accumulators.
type pendingBlock struct {
	typ      string // "text", "thinking", "tool_use"
	textBuf  strings.Builder
	toolID   string
	toolName string
}

// stream implements streaming chat completion using Server-Sent Events (SSE).
func (p *Provider) stream(ctx context.Context, req provider.ChatRequest) (<-chan provider.StreamChunk, error) {
	// Start observability span
	ctx, span := p.tracer.StartSpan(ctx, "anthropic.stream")
	defer span.End()

	// Add span attributes
	span.SetAttribute("provider", "anthropic")
	span.SetAttribute("model", req.Model)
	span.SetAttribute("message_count", len(req.Messages))

	// Log request
	p.logger.Info(ctx, "anthropic.stream.request",
		observability.F("model", req.Model),
		observability.F("message_count", len(req.Messages)),
	)

	// Use default model if not specified
	if req.Model == "" {
		req.Model = p.config.DefaultModel
	}

	// Add OAuth system prompt prefix if this is an OAuth provider
	if p.config.IsOAuth {
		// CRITICAL: OAuth tokens require CLI identifier at the BEGINNING of system prompt
		// This signals to Anthropic's infrastructure that this is an OAuth request
		oauthPrefix := GetCLISystemPromptPrefix()

		// Only add prefix if it's not already present (avoid duplication)
		if !strings.HasPrefix(req.SystemPrompt, oauthPrefix) {
			if req.SystemPrompt != "" {
				// IMPORTANT: When adding custom instructions after the OAuth prefix,
				// ensure proper formatting that Anthropic's OAuth system accepts
				req.SystemPrompt = oauthPrefix + "\n\n" + req.SystemPrompt
			} else {
				req.SystemPrompt = oauthPrefix
			}
			p.logger.Debug(ctx, "anthropic.stream.oauth_prefix_added",
				observability.F("is_oauth", true),
				observability.F("prefix", oauthPrefix),
				observability.F("full_prompt_length", len(req.SystemPrompt)),
			)
		} else {
			p.logger.Debug(ctx, "anthropic.stream.oauth_prefix_already_present",
				observability.F("is_oauth", true),
				observability.F("prompt_length", len(req.SystemPrompt)),
			)
		}
	}

	// Translate request to Anthropic format (with translation cache for incremental builds)
	anthropicReq, providerJSON, err := translateRequest(ctx, req, p.config.IsOAuth, p.config.AccountID, p.logger, p.translationCache)
	if err != nil {
		return nil, sdkerr.Wrap(
			err,
			"provider.anthropic.stream_request_translation_failed",
			sdkerr.WithOperation("anthropic.stream"),
			sdkerr.WithComponent("provider.anthropic"),
			sdkerr.WithTraceFromContext(ctx),
		)
	}

	// Store provider JSON for debugging (SDK can retrieve via GetLastProviderJSON)
	if rawJSON, err := json.Marshal(providerJSON); err == nil {
		p.lastProviderJSON = rawJSON
	}

	// Enable streaming
	anthropicReq.Stream = true

	// Add beta headers if needed
	headers := make(map[string]string)

	// For OAuth, build the complete set of required headers
	if p.config.IsOAuth {
		// Build beta header list
		var betaHeaders []string
		betaHeaders = append(betaHeaders, "oauth-2025-04-20") // Always required for OAuth

		// Add interleaved thinking beta header only when thinking is enabled
		if req.Metadata != nil {
			if thinkingEnabled, ok := req.Metadata["thinking_enabled"].(bool); ok && thinkingEnabled {
				betaHeaders = append(betaHeaders, OAuthInterleavedThinkingBeta)
			}
		}

		// Add additional beta headers from config if any
		if len(p.config.BetaHeaders) > 0 {
			betaHeaders = append(betaHeaders, p.config.BetaHeaders...)
		}

		// Set the combined beta header
		headers["anthropic-beta"] = strings.Join(betaHeaders, ",")

		// CRITICAL: x-app header is required for OAuth authentication
		headers["x-app"] = "cli"

		// Set User-Agent for OAuth requests (required for proper OAuth routing)
		headers["User-Agent"] = OAuthUserAgent
	} else if len(p.config.BetaHeaders) > 0 {
		// Non-OAuth with beta headers from config
		for _, beta := range p.config.BetaHeaders {
			if current := headers["anthropic-beta"]; current != "" {
				headers["anthropic-beta"] = current + "," + beta
			} else {
				headers["anthropic-beta"] = beta
			}
		}
	}

	// Auto-enable beta headers based on request features
	// Note: Extended thinking uses request parameters, not beta headers

	// Managed mode: reload the centrally-rotated OAuth token from disk before
	// sending (cheap; mtime-cached). No-op unless SWARMOS_OAUTH_MANAGED is set.
	p.reloadManagedToken(ctx, false)

	// Make streaming request using the fluent API
	reqBuilder := p.client.BuildRequest(ctx).Method("POST").URL(p.config.BaseURL + "/v1/messages").Body(anthropicReq)

	// Add custom headers
	for k, v := range headers {
		reqBuilder = reqBuilder.Header(k, v)
	}

	resp, err := p.client.Do(ctx, reqBuilder)
	if err != nil {
		return nil, sdkerr.Wrap(
			categorizeError(err, nil),
			"provider.anthropic.stream_request_failed",
			sdkerr.WithOperation("anthropic.stream"),
			sdkerr.WithComponent("provider.anthropic"),
			sdkerr.WithTraceFromContext(ctx),
		)
	}

	// Log status code for debugging
	statusCode := resp.StatusCode()
	isError := resp.IsError()
	p.logger.Info(ctx, "anthropic.stream.response_status",
		observability.F("status_code", statusCode),
		observability.F("is_error", isError),
		observability.F("check_400", statusCode >= 400),
	)

	// Managed mode 401 handling: do NOT refresh. Reload the centrally-rotated
	// token from disk once and retry the stream. If it still 401s, fall through
	// to the normal error path (the turn fails and the worker requeues).
	if statusCode == 401 && p.isManaged() {
		p.logger.Warn(ctx, "anthropic.stream.managed.token_unauthorized",
			observability.F("status_code", 401),
			observability.F("action", "reload_from_disk"),
		)
		resp.Close()
		p.reloadManagedToken(ctx, true)

		retryBuilder := p.client.BuildRequest(ctx).Method("POST").URL(p.config.BaseURL + "/v1/messages").Body(anthropicReq)
		for k, v := range headers {
			retryBuilder = retryBuilder.Header(k, v)
		}
		resp, err = p.client.Do(ctx, retryBuilder)
		if err != nil {
			return nil, sdkerr.Wrap(
				categorizeError(err, nil),
				"provider.anthropic.stream_request_failed",
				sdkerr.WithOperation("anthropic.stream"),
				sdkerr.WithComponent("provider.anthropic"),
				sdkerr.WithTraceFromContext(ctx),
			)
		}
		statusCode = resp.StatusCode()
		isError = resp.IsError()
		p.logger.Info(ctx, "anthropic.stream.managed.retry_status",
			observability.F("status_code", statusCode),
		)
	}

	// CRITICAL: Check for HTTP error status codes BEFORE trying to process SSE
	// Without this check, 4xx/5xx errors would be treated as empty SSE streams
	// Note: Using direct status code check as a fallback in case IsError() has issues
	if isError || statusCode >= 400 {
		rawResp := resp.RawResponse()
		if rawResp != nil {
			// Read the error body for proper error categorization
			p.logger.Error(ctx, "anthropic.stream.http_error",
				observability.F("status_code", statusCode),
			)

			// Attempt auto-retry when input + max_tokens exceeds context limit.
			// The API tells us exactly how many input tokens there are, so we
			// can clamp max_tokens = contextWindow - inputLength and retry.
			categorizedErr := categorizeError(nil, rawResp)
			if retryReq, ok := p.tryClampMaxTokens(ctx, categorizedErr, anthropicReq); ok {
				p.logger.Warn(ctx, "anthropic.stream.context_limit_retry",
					observability.F("original_max_tokens", retryReq.originalMaxTokens),
					observability.F("clamped_max_tokens", retryReq.clampedMaxTokens),
					observability.F("input_length", retryReq.inputLength),
					observability.F("context_size", retryReq.contextSize),
				)

				// Rebuild and retry with clamped max_tokens
				retryBuilder := p.client.BuildRequest(ctx).Method("POST").URL(p.config.BaseURL + "/v1/messages").Body(anthropicReq)
				for k, v := range headers {
					retryBuilder = retryBuilder.Header(k, v)
				}
				resp, err = p.client.Do(ctx, retryBuilder)
				if err != nil {
					return nil, sdkerr.Wrap(
						categorizeError(err, nil),
						"provider.anthropic.stream_request_failed",
						sdkerr.WithOperation("anthropic.stream"),
						sdkerr.WithComponent("provider.anthropic"),
						sdkerr.WithTraceFromContext(ctx),
					)
				}
				// If the retry also failed, fall through to return the original error
				if resp.IsError() || resp.StatusCode() >= 400 {
					return nil, sdkerr.Wrap(
						categorizedErr,
						"provider.anthropic.stream_http_error",
						sdkerr.WithOperation("anthropic.stream"),
						sdkerr.WithComponent("provider.anthropic"),
						sdkerr.WithTraceFromContext(ctx),
					)
				}
				// Retry succeeded — continue with this response
				statusCode = resp.StatusCode()
			} else {
				return nil, sdkerr.Wrap(
					categorizedErr,
					"provider.anthropic.stream_http_error",
					sdkerr.WithOperation("anthropic.stream"),
					sdkerr.WithComponent("provider.anthropic"),
					sdkerr.WithTraceFromContext(ctx),
				)
			}
		} else {
			return nil, sdkerr.Permanent(
				"anthropic.stream_http_error",
				fmt.Sprintf("HTTP %d error without response body", resp.StatusCode()),
				sdkerr.WithOperation("anthropic.stream"),
				sdkerr.WithComponent("provider.anthropic"),
				sdkerr.WithTraceFromContext(ctx),
			)
		}
	}

	// Create output channel
	chunks := make(chan provider.StreamChunk, 10)

	// Start goroutine to process SSE stream
	// CRITICAL: We need the raw http.Response.Body reader for streaming
	// resp.Body() would read everything into memory, defeating streaming
	go func() {
		defer close(chunks)
		defer resp.Close()

		// Access the underlying http.Response body directly for streaming
		rawResp := resp.RawResponse()
		if rawResp == nil || rawResp.Body == nil {
			chunks <- provider.StreamChunk{
				Error: sdkerr.Permanent(
					"anthropic.stream_no_body",
					"no response body for streaming",
					sdkerr.WithOperation("anthropic.stream"),
					sdkerr.WithComponent("provider.anthropic"),
					sdkerr.WithTraceFromContext(ctx),
				),
				Done: true,
			}
			return
		}

		// Monitor context cancellation and close the response body to
		// immediately unblock scanner.Scan() in processSSEStream.
		// Without this, cancellation is only detected between SSE events,
		// so the user must wait for the current API chunk to arrive.
		done := make(chan struct{})
		go func() {
			select {
			case <-ctx.Done():
				rawResp.Body.Close()
			case <-done:
				// Stream finished normally, no need to force-close
			}
		}()
		defer close(done)

		if err := p.processSSEStream(ctx, rawResp.Body, chunks); err != nil {
			// If context was cancelled, report that instead of the read error
			// caused by closing the response body
			if ctx.Err() != nil {
				chunks <- provider.StreamChunk{
					Error: ctx.Err(),
					Done:  true,
				}
			} else {
				chunks <- provider.StreamChunk{
					Error: err,
					Done:  true,
				}
			}
		}
	}()

	return chunks, nil
}

func (p *Provider) shouldWriteTrace() bool {
	return !p.noAmbientEnv &&
		(os.Getenv("ENVELOPE_TRACE") != "" || os.Getenv("CACHE_DEBUG") != "")
}

// processSSEStream processes the SSE stream and sends chunks to the output channel.
func (p *Provider) processSSEStream(ctx context.Context, reader io.Reader, chunks chan<- provider.StreamChunk) error {
	scanner := bufio.NewScanner(reader)
	// Increase buffer size to handle large SSE events and avoid "token too long" errors
	buf := make([]byte, 0, 64*1024)
	scanner.Buffer(buf, 1024*1024) // 1MB max

	// Accumulate message data
	var accumulatedText strings.Builder
	var accumulatedThinking strings.Builder // Accumulate thinking content
	var thinkingSignature string            // Signature for thinking block
	var usage *conversation.TokenUsage
	var finishReason provider.FinishReason
	var toolCalls []conversation.ToolCall // Accumulate tool calls
	var currentToolCall *conversation.ToolCall
	var currentToolJSON strings.Builder
	var cacheMetrics map[string]int      // Cache hit/creation metrics
	var contextManagement map[string]any // context_management beta field
	var rawEventsBuilder strings.Builder // Accumulate all raw events for State Preservation

	// Provider-native block ordering: SSE block_index → partial block state.
	// Emitted on the final chunk as StreamChunk.OrderedBlocks so downstream
	// renderers can preserve interleaved text/thinking/tool_use order.
	pendingBlocks := map[int]*pendingBlock{}

	// Initialize envelope handler for transformation (ALWAYS active)
	// This is the SINGLE source of truth for JSON transformation
	// Principle 1: Universal Envelope - wraps all provider JSON
	// Principle 2: Side Table - transformation rules as data
	// Principle 3: State Preservation - raw JSON preserved
	// Principle 4: Polymorphic Handler - one handler for all providers
	sessionID := fmt.Sprintf("anthropic-%d", time.Now().UnixNano())
	envHandler := envelope.NewHandler(sessionID)

	// Enable trace writing only when env var is set
	writeTrace := p.shouldWriteTrace()

	for scanner.Scan() {
		line := scanner.Text()

		// Skip empty lines and comments
		if line == "" || strings.HasPrefix(line, ":") {
			continue
		}

		// Parse SSE event
		if !strings.HasPrefix(line, "data: ") {
			continue
		}

		data := strings.TrimPrefix(line, "data: ")

		// Principle 3: State Preservation
		// Accumulate ALL raw events so we can store the complete provider-specific
		// "stack" in the final message.
		rawEventsBuilder.WriteString(data)
		rawEventsBuilder.WriteString("\n")

		// CRITICAL: Log raw API response before transformation (for debugging token parsing)
		// This captures the actual SSE event from API before any SDK processing
		if p.rawEventCallback != nil {
			p.rawEventCallback.OnRawEvent("sse_data", data)
		}

		// Process through Universal Envelope system (ALWAYS)
		// This is the SINGLE source of truth for JSON → canonical transformation
		env, envErr := envHandler.ProcessAnthropicSSE(ctx, data)
		if envErr == nil && env.Canonical != nil && env.Canonical.Usage != nil {
			// Extract cache metrics from canonical format (Side Table did the transformation)
			canonical := env.Canonical

			// Update cache metrics whenever cache token data is present.
			if canonical.Usage.CacheCreationTokens > 0 || canonical.Usage.CacheReadTokens > 0 {
				cacheMetrics = map[string]int{
					"cache_creation_tokens":    canonical.Usage.CacheCreationTokens,
					"cache_read_tokens":        canonical.Usage.CacheReadTokens,
					"cache_creation_1h_tokens": canonical.Usage.CacheCreation1hTokens,
					"cache_creation_5m_tokens": canonical.Usage.CacheCreation5mTokens,
				}
			}

			// Update usage whenever we have output tokens (from message_delta at end of stream)
			// OR cache tokens — do NOT gate on cache tokens alone, because a no-cache call
			// would otherwise leave output_tokens at zero for the entire response.
			if canonical.Usage.OutputTokens > 0 || canonical.Usage.CacheCreationTokens > 0 || canonical.Usage.CacheReadTokens > 0 {
				// Build usage with full field breakdown so callers can inspect cache components.
				usage = &conversation.TokenUsage{
					Input:           canonical.Usage.InputTokens, // pure input_tokens, excluding cache
					Output:          canonical.Usage.OutputTokens,
					CacheCreation:   canonical.Usage.CacheCreationTokens,
					CacheRead:       canonical.Usage.CacheReadTokens,
					CacheCreation5m: canonical.Usage.CacheCreation5mTokens,
					CacheCreation1h: canonical.Usage.CacheCreation1hTokens,
				}
				usage.Total = usage.Input + usage.CacheCreation + usage.CacheRead + usage.Output

				// Log at INFO level so it's visible in normal operation
				p.logger.Info(ctx, "anthropic.stream.envelope_extracted",
					observability.F("input_tokens", canonical.Usage.InputTokens),
					observability.F("output_tokens", canonical.Usage.OutputTokens),
					observability.F("cache_creation", canonical.Usage.CacheCreationTokens),
					observability.F("cache_read", canonical.Usage.CacheReadTokens),
					observability.F("total_with_cache", canonical.Usage.TotalInputWithCache),
				)
			}

			// Extract finish reason from canonical
			if canonical.FinishReason != "" {
				finishReason = translateFinishReason(canonical.FinishReason)
			}
		}

		// Parse event
		var event StreamEvent
		if err := json.Unmarshal([]byte(data), &event); err != nil {
			p.logger.Warn(ctx, "anthropic.stream.parse_error",
				observability.F("error", err.Error()),
				observability.F("data", data),
			)
			continue
		}

		// Process event based on type
		switch event.Type {
		case "message_start":
			// Message started - send initial usage with input_tokens immediately
			p.logger.Debug(ctx, "anthropic.stream.message_start",
				observability.F("model", event.Message.Model),
			)

			// Extract initial input_tokens from message_start event.
			// Cache fields may already be known at this point for cached conversations.
			if event.Message != nil {
				msgUsage := event.Message.Usage
				initialCacheCreation := 0
				initialCacheRead := 0
				if msgUsage.CacheCreationInputTokens != nil {
					initialCacheCreation = *msgUsage.CacheCreationInputTokens
				}
				if msgUsage.CacheReadInputTokens != nil {
					initialCacheRead = *msgUsage.CacheReadInputTokens
				}
				initialCacheCreation5m := 0
				initialCacheCreation1h := 0
				if cc := msgUsage.CacheCreation; cc != nil {
					initialCacheCreation5m = cc.Ephemeral5mInputTokens
					initialCacheCreation1h = cc.Ephemeral1hInputTokens
				}
				initialUsage := &conversation.TokenUsage{
					Input:           msgUsage.InputTokens, // pure input_tokens, excluding cache
					Output:          0,                    // no output yet
					CacheCreation:   initialCacheCreation,
					CacheRead:       initialCacheRead,
					CacheCreation5m: initialCacheCreation5m,
					CacheCreation1h: initialCacheCreation1h,
				}
				initialUsage.Total = initialUsage.Input + initialUsage.CacheCreation + initialUsage.CacheRead
				// Send a chunk with usage info and the provider message ID.
				// The message ID (e.g. "msg_01XYZ…") is threaded through metadata
				// so that the bridge can include it in UpdateTokenCount events,
				// enabling print-mode events to carry an accurate message.id field.
				chunks <- provider.StreamChunk{
					Delta: "",
					Usage: initialUsage,
					Done:  false,
					Metadata: map[string]any{
						"message_id": event.Message.ID,
					},
				}
				p.logger.Info(ctx, "anthropic.stream.initial_usage",
					observability.F("input_tokens", event.Message.Usage.InputTokens),
					observability.F("message_id", event.Message.ID),
				)
			}

		case "content_block_start":
			// Content block started
			p.logger.Debug(ctx, "anthropic.stream.content_block_start",
				observability.F("index", event.Index),
				observability.F("type", event.ContentBlock.Type),
			)

			// Reserve a pendingBlock at this block_index so interleaved deltas
			// can be accumulated in provider-native order.
			if event.Index != nil && event.ContentBlock != nil {
				pb := &pendingBlock{typ: event.ContentBlock.Type}
				if pb.typ == "tool_use" {
					pb.toolID = event.ContentBlock.ID
					pb.toolName = event.ContentBlock.Name
				}
				pendingBlocks[*event.Index] = pb
			}

			// If this is a tool_use block, start tracking it
			if event.ContentBlock != nil && event.ContentBlock.Type == "tool_use" {
				currentToolCall = &conversation.ToolCall{
					ID:   event.ContentBlock.ID,
					Name: event.ContentBlock.Name,
				}
				currentToolJSON.Reset()
			}

		case "content_block_delta":
			// Content delta received
			if event.Delta != nil {
				// Route delta into the pendingBlock at this index (if any) so
				// provider-native block text/thinking accumulates per-block.
				var pb *pendingBlock
				if event.Index != nil {
					pb = pendingBlocks[*event.Index]
				}
				switch event.Delta.Type {
				case "text_delta":
					// Text content
					accumulatedText.WriteString(event.Delta.Text)
					if pb != nil {
						pb.textBuf.WriteString(event.Delta.Text)
					}
					chunks <- provider.StreamChunk{
						Delta: event.Delta.Text,
						Done:  false,
					}

				case "thinking_delta":
					// Extended thinking content is both accumulated for the final
					// canonical message and emitted immediately for live consumers.
					accumulatedThinking.WriteString(event.Delta.Thinking)
					if pb != nil {
						pb.textBuf.WriteString(event.Delta.Thinking)
					}
					if event.Delta.Thinking != "" {
						chunks <- provider.StreamChunk{Thinking: event.Delta.Thinking}
					}
					p.logger.Debug(ctx, "anthropic.stream.thinking_delta",
						observability.F("thinking_length", len(event.Delta.Thinking)),
					)

				case "signature_delta":
					// Thinking block signature - must be preserved for future requests
					thinkingSignature = event.Delta.Signature
					p.logger.Debug(ctx, "anthropic.stream.signature_delta",
						observability.F("signature_length", len(event.Delta.Signature)),
					)

				case "input_json_delta":
					// Tool use JSON delta (accumulate but don't stream)
					if currentToolCall != nil {
						currentToolJSON.WriteString(event.Delta.PartialJSON)
					}
					p.logger.Debug(ctx, "anthropic.stream.tool_delta",
						observability.F("json", event.Delta.PartialJSON),
					)
				}
			}

		case "content_block_stop":
			// Content block completed
			p.logger.Debug(ctx, "anthropic.stream.content_block_stop",
				observability.F("index", event.Index),
			)

			// If we were tracking a tool call, finalize it
			if currentToolCall != nil {
				// Parse the accumulated JSON into parameters
				// NOTE: Parameterless tools (e.g. TodoRead) send empty partial_json deltas,
				// resulting in an empty string. Default to "{}" for valid JSON.
				jsonStr := currentToolJSON.String()
				if jsonStr == "" {
					jsonStr = "{}"
				}
				var params map[string]any
				if err := json.Unmarshal([]byte(jsonStr), &params); err != nil {
					p.logger.Warn(ctx, "anthropic.stream.tool_parse_error",
						observability.F("error", err.Error()),
						observability.F("json", jsonStr),
					)
				} else {
					currentToolCall.Parameters = params

					// Remove OAuth prefix from tool name if this is an OAuth request
					if p.config.IsOAuth {
						currentToolCall.Name = RemoveOAuthToolPrefix(currentToolCall.Name)
					}

					toolCalls = append(toolCalls, *currentToolCall)
					p.logger.Info(ctx, "anthropic.stream.tool_call_complete",
						observability.F("tool", currentToolCall.Name),
						observability.F("id", currentToolCall.ID),
					)
				}
				currentToolCall = nil
				currentToolJSON.Reset()
			}

		case "message_delta":
			// Message metadata update (finish reason)
			// NOTE: Cache metrics and usage are now extracted by the Universal Envelope system
			// above (lines ~250-280). The envelope's Side Table transforms the raw JSON to
			// canonical format, which includes TotalInputWithCache calculation.
			if event.Delta != nil {
				if event.Delta.StopReason != "" {
					finishReason = translateFinishReason(event.Delta.StopReason)
				}
			}
			// Capture context_management field from the context-management-2025-06-27 beta.
			if event.ContextManagement != nil {
				contextManagement = event.ContextManagement
				p.logger.Debug(ctx, "anthropic.stream.context_management",
					observability.F("fields", len(contextManagement)))
			}
			// Emit intermediate usage update so the TUI token counter ticks upward during
			// long responses. The envelope extraction above already updated `usage` when
			// OutputTokens > 0; send a non-Done chunk now so consumers see it immediately.
			if usage != nil && usage.Output > 0 {
				chunks <- provider.StreamChunk{
					Usage: usage,
					Done:  false,
				}
			}
			// Log for debugging (envelope already extracted these)
			if event.Usage != nil {
				p.logger.Debug(ctx, "anthropic.stream.message_delta_usage",
					observability.F("input_tokens", event.Usage.InputTokens),
					observability.F("output_tokens", event.Usage.OutputTokens),
				)
			}

		case "message_stop":
			// Message completed
			p.logger.Info(ctx, "anthropic.stream.message_stop",
				observability.F("finish_reason", finishReason),
				observability.F("tool_calls", len(toolCalls)),
				observability.F("thinking_length", accumulatedThinking.Len()),
				observability.F("has_signature", thinkingSignature != ""),
			)

			// Write envelope trace if enabled (complete NDJSON of all events)
			if writeTrace {
				traceFile, err := envHandler.WriteTrace()
				if err == nil {
					p.logger.Info(ctx, "anthropic.stream.envelope_trace_written",
						observability.F("trace_file", traceFile),
						observability.F("event_count", envHandler.Store().Count()),
					)
				}
			}

			// Build metadata with cache metrics if available
			var metadata map[string]any
			if len(cacheMetrics) > 0 || rawEventsBuilder.Len() > 0 || len(contextManagement) > 0 {
				metadata = make(map[string]any)
				if len(cacheMetrics) > 0 {
					metadata["cache_metrics"] = cacheMetrics
				}
				if len(contextManagement) > 0 {
					metadata["context_management"] = contextManagement
				}

				// Principle 3: State Preservation
				// Pass the accumulated raw SSE data back so it can be stored in the final message.
				if rawEventsBuilder.Len() > 0 {
					metadata["raw_payload"] = []byte(rawEventsBuilder.String())
				}
			}

			// Assemble OrderedBlocks from the pending-block map. Sort by
			// block_index so interleaved text/thinking/tool_use is preserved
			// in provider-native order. Sequence = block_index so downstream
			// sorting is stable across pairing passes.
			orderedBlocks := buildOrderedBlocks(pendingBlocks, toolCalls)

			// Thinking text was emitted incrementally above. Keep it in
			// OrderedBlocks for the canonical final message, but do not repeat the
			// full text here: streamChat treats every Thinking value as a delta.
			chunks <- provider.StreamChunk{
				Delta:             "",
				ThinkingSignature: thinkingSignature,
				FinishReason:      finishReason,
				Usage:             usage,
				ToolCalls:         toolCalls,
				OrderedBlocks:     orderedBlocks,
				Done:              true,
				Metadata:          metadata,
			}

		case "error":
			// Error occurred
			if event.Error != nil {
				p.logger.Error(ctx, "anthropic.stream.error",
					observability.F("error_type", event.Error.Error.Type),
					observability.F("error_message", event.Error.Error.Message),
				)

				return sdkerr.Permanent(
					"anthropic.stream_error",
					event.Error.Error.Message,
					sdkerr.WithOperation("anthropic.process_sse_stream"),
					sdkerr.WithComponent("provider.anthropic"),
					sdkerr.WithTraceFromContext(ctx),
				)
			}

		default:
			p.logger.Warn(ctx, "anthropic.stream.unknown_event",
				observability.F("event_type", event.Type),
			)
		}

		// Check for context cancellation
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
	}

	if err := scanner.Err(); err != nil {
		// If context was cancelled, the read error is from force-closing
		// the response body. Return the context error instead.
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return sdkerr.Wrap(err, "anthropic.stream_read_error",
			sdkerr.WithOperation("anthropic.process_sse_stream"),
			sdkerr.WithComponent("provider.anthropic"),
			sdkerr.WithTraceFromContext(ctx))
	}

	return nil
}

// buildOrderedBlocks converts the SSE pending-block map into the canonical
// conversation.MessageBlock slice, preserving provider-native block_index
// order. tool_use blocks are linked to finalized ToolCalls by ID so any
// name normalization (e.g. OAuth prefix removal) is reflected in the output.
// Empty text/thinking blocks are dropped; unknown block types are skipped.
func buildOrderedBlocks(pending map[int]*pendingBlock, toolCalls []conversation.ToolCall) []conversation.MessageBlock {
	if len(pending) == 0 {
		return nil
	}

	indices := make([]int, 0, len(pending))
	for idx := range pending {
		indices = append(indices, idx)
	}
	sort.Ints(indices)

	out := make([]conversation.MessageBlock, 0, len(indices))
	for _, idx := range indices {
		pb := pending[idx]
		if pb == nil {
			continue
		}
		switch pb.typ {
		case "text":
			if pb.textBuf.Len() == 0 {
				continue
			}
			out = append(out, conversation.MessageBlock{
				Type:     conversation.BlockTypeContent,
				Content:  pb.textBuf.String(),
				Sequence: idx,
			})
		case "thinking":
			if pb.textBuf.Len() == 0 {
				continue
			}
			out = append(out, conversation.MessageBlock{
				Type:     conversation.BlockTypeThinking,
				Content:  pb.textBuf.String(),
				Sequence: idx,
			})
		case "tool_use":
			// Link to the finalized ToolCall (with normalized name / parsed
			// params) rather than the partial state on pb itself.
			var tc *conversation.ToolCall
			for i := range toolCalls {
				if toolCalls[i].ID == pb.toolID {
					copy := toolCalls[i]
					tc = &copy
					break
				}
			}
			if tc == nil {
				// tool_use block never finalized (malformed JSON, etc.) —
				// skip rather than emit a ToolCall with nil fields.
				continue
			}
			out = append(out, conversation.MessageBlock{
				Type:     conversation.BlockTypeToolCall,
				ToolCall: tc,
				Sequence: idx,
			})
		}
	}
	return out
}
