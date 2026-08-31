package openai

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/conversation"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/observability"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/provider"
	sdkerr "github.com/Swarm-Code/mono/swarm-sdk/internal/sdkerr"
)

var (
	// openAIStreamFirstTokenTimeout caps the wait BEFORE the first SSE event
	// arrives. Cold starts on long-context routes (e.g. Fireworks Kimi) can
	// take well past the inter-token idle window; gating the cold start with
	// a tighter ceiling kills otherwise-recoverable streams. After the first
	// progress event, resetIdleTimer drops the budget back to the inter-token
	// idle timeout below.
	openAIStreamFirstTokenTimeout = 90 * time.Second
	// openAIStreamIdleTimeout caps the gap BETWEEN SSE events once the stream
	// has produced at least one. Generous enough to tolerate slow generation
	// and brief network hiccups. Models that support extended thinking
	// (reasoning blocks) can go silent for much longer; once reasoning
	// content has been observed the stream switches to
	// openAIStreamThinkingIdleTimeout instead.
	openAIStreamIdleTimeout = 90 * time.Second
	// openAIStreamThinkingIdleTimeout caps the gap between SSE events when
	// the stream has already produced reasoning/thinking content. Extended
	// thinking models (GLM, o1/o3, DeepSeek-R1, etc.) routinely go silent
	// for 30–120+ seconds between reasoning steps — the previous 20 s idle
	// timeout killed these streams mid-thought. 3 minutes gives thinking
	// models room to complete their reasoning while still detecting
	// genuinely dead connections.
	openAIStreamThinkingIdleTimeout = 180 * time.Second
	// openAIStreamPostUsageTimeout caps the wait after the usage chunk has
	// arrived (i.e. we already have the bill; only the [DONE] tail is left).
	openAIStreamPostUsageTimeout = 5 * time.Second
)

// stream implements streaming chat completion using Server-Sent Events (SSE).
func (p *Provider) stream(ctx context.Context, req provider.ChatRequest) (<-chan provider.StreamChunk, error) {
	// Start observability span
	ctx, span := p.tracer.StartSpan(ctx, "openai.stream")
	defer span.End()

	// Add span attributes
	span.SetAttribute("provider", p.name)
	span.SetAttribute("model", req.Model)
	span.SetAttribute("message_count", len(req.Messages))

	// Log request
	p.logger.Info(ctx, "openai.stream.request",
		observability.F("model", req.Model),
		observability.F("message_count", len(req.Messages)),
	)

	// Translate request to OpenAI format
	openaiReq, err := TranslateRequest(req)
	if err != nil {
		return nil, sdkerr.Wrap(
			err,
			"provider.openai.stream_request_translation_failed",
			sdkerr.WithOperation("openai.stream"),
			sdkerr.WithComponent("provider.openai"),
			sdkerr.WithTraceFromContext(ctx),
		)
	}

	// Enable streaming with usage tracking
	openaiReq.Stream = true
	openaiReq.StreamOptions = &StreamOptions{
		IncludeUsage: true, // Request token usage in streaming responses
	}

	// Make streaming request using the fluent API
	reqBuilder := p.client.BuildRequest(ctx).
		Method("POST").
		URL(p.baseURL + "/chat/completions").
		Body(openaiReq)

	resp, err := p.client.Do(ctx, reqBuilder)
	if err != nil {
		return nil, sdkerr.Wrap(
			err,
			"provider.openai.stream_request_failed",
			sdkerr.WithOperation("openai.stream"),
			sdkerr.WithComponent("provider.openai"),
			sdkerr.WithTraceFromContext(ctx),
		)
	}

	// Log status code for debugging
	statusCode := resp.StatusCode()
	isError := resp.IsError()
	p.logger.Info(ctx, "openai.stream.response_status",
		observability.F("status_code", statusCode),
		observability.F("is_error", isError),
	)

	// Check for HTTP error status codes BEFORE trying to process SSE
	if isError || statusCode >= 400 {
		// Read error body for proper error message
		body, _ := resp.Body()
		p.logger.Error(ctx, "openai.stream.http_error",
			observability.F("status_code", statusCode),
			observability.F("body", string(body)),
		)

		// Use the HTTP response parser to categorize errors (transient vs permanent)
		// This ensures 5xx/429 errors are retryable instead of always permanent.
		var parsed any
		err := resp.JSON(&parsed)
		resp.Close()
		if err != nil {
			return nil, sdkerr.Wrap(
				err,
				"provider.openai.stream_http_error_parse_failed",
				sdkerr.WithOperation("openai.stream"),
				sdkerr.WithComponent("provider.openai"),
				sdkerr.WithTraceFromContext(ctx),
			)
		}

		// Fallback (should not happen): return a generic error
		return nil, sdkerr.Wrap(fmt.Errorf("HTTP %d error", statusCode), "openai.stream_http_error",
			sdkerr.WithOperation("openai.stream"),
			sdkerr.WithComponent("provider.openai"),
			sdkerr.WithTraceFromContext(ctx),
		)
	}

	// Create output channel
	chunks := make(chan provider.StreamChunk, 10)

	// Start goroutine to process SSE stream
	go func() {
		defer close(chunks)
		defer resp.Close()

		// Access the underlying http.Response body directly for streaming
		rawResp := resp.RawResponse()
		if rawResp == nil || rawResp.Body == nil {
			chunks <- provider.StreamChunk{
				Error: sdkerr.Permanent(
					"openai.stream_no_body",
					"no response body for streaming",
					sdkerr.WithOperation("openai.stream"),
					sdkerr.WithComponent("provider.openai"),
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

		if err := p.processSSEStream(ctx, rawResp.Body, chunks, req.Model); err != nil {
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

// processSSEStream processes the SSE stream and sends chunks to the output channel.
func (p *Provider) processSSEStream(ctx context.Context, reader io.ReadCloser, chunks chan<- provider.StreamChunk, model string) error {
	// The scanner goroutine below parks on a channel send whenever this
	// function returns early ([DONE], provider error, read error) while an
	// event is still unsent. Its only escape is ctx.Done(), and the request
	// ctx belongs to the caller (a sub-agent), which is not cancelled on
	// success — only at program exit. Derive a ctx we own so every return
	// path releases the scanner.
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	scanner := bufio.NewScanner(reader)
	// Increase buffer size to handle large SSE events and avoid "token too long" errors
	buf := make([]byte, 0, 64*1024)
	scanner.Buffer(buf, 1024*1024) // 1MB max

	// Accumulate message data
	var accumulatedText strings.Builder
	var accumulatedReasoning strings.Builder
	var usage *conversation.TokenUsage
	var finishReason provider.FinishReason
	var sawDone bool
	var sawFinishReason bool
	var toolCalls []conversation.ToolCall
	var currentToolCalls map[int]*ToolCall // Track tool calls by index
	// responseID captures the completion ID from the first SSE chunk (e.g. "chatcmpl-xxx").
	// It is forwarded through Metadata so the bridge can populate TokenCountPayload.MessageID.
	var responseID string

	// Capture raw SSE events for diagnostics when tool_calls mismatch occurs
	var rawSSEEvents []string

	// seenReasoning tracks whether the stream has produced any
	// reasoning/thinking content. Once true, resetIdleTimer uses the
	// longer openAIStreamThinkingIdleTimeout so that extended-thinking
	// models (GLM, o1/o3, DeepSeek-R1, etc.) are not killed mid-thought
	// when they go silent between reasoning steps.
	var seenReasoning bool

	type scanEvent struct {
		line string
		err  error
		done bool
	}

	scanEvents := make(chan scanEvent, 1)
	go func() {
		defer close(scanEvents)
		emit := func(ev scanEvent) {
			select {
			case scanEvents <- ev:
			case <-ctx.Done():
			}
		}
		for scanner.Scan() {
			select {
			case scanEvents <- scanEvent{line: scanner.Text()}:
			case <-ctx.Done():
				return
			}
		}
		if err := scanner.Err(); err != nil {
			emit(scanEvent{err: err})
			return
		}
		emit(scanEvent{done: true})
	}()

	// currentIdleTimeout tracks which window the timer is currently armed
	// with so the timeout error message reports the actual budget that
	// fired (first-token vs. inter-token vs. post-usage).
	currentIdleTimeout := openAIStreamFirstTokenTimeout
	if currentIdleTimeout <= 0 {
		currentIdleTimeout = openAIStreamIdleTimeout
	}
	var idleTimer *time.Timer
	if currentIdleTimeout > 0 {
		idleTimer = time.NewTimer(currentIdleTimeout)
		defer idleTimer.Stop()
	}
	resetIdleTimer := func(postUsage bool) {
		if idleTimer == nil {
			return
		}
		nextTimeout := openAIStreamIdleTimeout
		// Use the longer thinking timeout once reasoning content has been
		// observed — extended-thinking models can go silent for minutes
		// between reasoning steps.
		if seenReasoning && openAIStreamThinkingIdleTimeout > 0 {
			nextTimeout = openAIStreamThinkingIdleTimeout
		}
		if postUsage && openAIStreamPostUsageTimeout > 0 {
			nextTimeout = openAIStreamPostUsageTimeout
		}
		currentIdleTimeout = nextTimeout
		if !idleTimer.Stop() {
			select {
			case <-idleTimer.C:
			default:
			}
		}
		idleTimer.Reset(nextTimeout)
	}

	for {
		var (
			ev scanEvent
			ok bool
		)
		select {
		case <-ctx.Done():
			_ = reader.Close()
			return ctx.Err()
		case <-idleTimerChan(idleTimer):
			_ = reader.Close()
			if recovered := p.recoverIdleTimedOutStream(ctx, chunks, &accumulatedText, &accumulatedReasoning, usage, finishReason, currentToolCalls, toolCalls, responseID); recovered {
				return nil
			}
			return sdkerr.Wrap(
				fmt.Errorf("stream idle timeout after %s", currentIdleTimeout),
				"openai.stream_idle_timeout",
				sdkerr.WithOperation("openai.process_sse_stream"),
				sdkerr.WithComponent("provider.openai"),
				sdkerr.WithTraceFromContext(ctx),
			)
		case ev, ok = <-scanEvents:
			if !ok {
				return nil
			}
		}

		if ev.err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return sdkerr.Wrap(ev.err, "openai.stream_read_error",
				sdkerr.WithOperation("openai.process_sse_stream"),
				sdkerr.WithComponent("provider.openai"),
				sdkerr.WithTraceFromContext(ctx))
		}
		if ev.done {
			break
		}
		line := ev.line

		// Skip empty lines and comments
		if line == "" || strings.HasPrefix(line, ":") {
			continue
		}

		// Parse SSE event
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))

		// VERBOSE: Log raw SSE event for debugging token counts and stream data
		p.logger.Debug(ctx, "openai.stream.sse_event_raw",
			observability.F("data", data),
			observability.F("length", len(data)),
		)

		// Capture raw events for diagnostic dump if tool_calls mismatch occurs
		if len(rawSSEEvents) < 200 { // Cap to prevent memory issues
			rawSSEEvents = append(rawSSEEvents, data)
		}

		// Check for stream end marker
		if data == "[DONE]" {
			p.logger.Debug(ctx, "openai.stream.done")
			sawDone = true

			// Convert accumulated tool calls to canonical format BEFORE sending final chunk.
			// Iterate in Index order so downstream OrderedBlocks and ToolCalls are
			// deterministic — map iteration alone is random and would scramble
			// parallel tool calls across runs.
			if len(currentToolCalls) > 0 {
				indices := make([]int, 0, len(currentToolCalls))
				for idx := range currentToolCalls {
					indices = append(indices, idx)
				}
				sort.Ints(indices)
				for _, idx := range indices {
					toolCalls = append(toolCalls, TranslateToolCallFromOpenAI(*currentToolCalls[idx]))
				}
				if finishReason != provider.FinishReasonToolCalls {
					finishReason = provider.FinishReasonToolCalls
				}
				p.logger.Info(ctx, "openai.stream.tool_calls_converted",
					observability.F("tool_call_count", len(toolCalls)),
				)
			}

			// Snapshot pristine text BEFORE the reasoning-as-text fallback below
			// mutates accumulatedText. OrderedBlocks derives from these snapshots
			// so the thinking block isn't double-counted as content.
			pristineText := accumulatedText.String()

			finalDelta := ""
			if accumulatedText.Len() == 0 && accumulatedReasoning.Len() > 0 {
				finalDelta = accumulatedReasoning.String()
				accumulatedText.WriteString(finalDelta)
			}

			// Diagnostic: dump raw SSE events when finish_reason says tool_calls but none parsed
			if finishReason == provider.FinishReasonToolCalls && len(toolCalls) == 0 {
				p.logger.Warn(ctx, "openai.stream.TOOL_CALLS_MISMATCH_SSE_DUMP",
					observability.F("finish_reason", string(finishReason)),
					observability.F("parsed_tool_calls", 0),
					observability.F("sse_event_count", len(rawSSEEvents)),
					observability.F("accumulated_text_length", accumulatedText.Len()),
					observability.F("accumulated_reasoning_length", accumulatedReasoning.Len()),
				)
				// Log last 10 SSE events to see what the provider actually sent
				start := max(len(rawSSEEvents)-10, 0)
				for i := start; i < len(rawSSEEvents); i++ {
					p.logger.Warn(ctx, "openai.stream.SSE_DUMP_EVENT",
						observability.F("event_index", i),
						observability.F("data", rawSSEEvents[i]),
					)
				}
			}

			// Send final chunk with accumulated data.
			// Include the completion ID in Metadata so the bridge can thread it
			// into TokenCountPayload.MessageID for print-mode events.
			meta := map[string]any{}
			if responseID != "" {
				meta["message_id"] = responseID
			}
			// OrderedBlocks uses the pre-fallback text so the reasoning-as-text
			// hack doesn't double-count (pristineText is empty when the hack
			// fires; the thinking block below carries the reasoning content).
			orderedBlocks := buildOpenAIOrderedBlocks(
				pristineText,
				accumulatedReasoning.String(),
				toolCalls,
			)
			chunks <- provider.StreamChunk{
				Delta:         finalDelta,
				Thinking:      accumulatedReasoning.String(),
				FinishReason:  finishReason,
				Usage:         usage,
				ToolCalls:     toolCalls,
				OrderedBlocks: orderedBlocks,
				Done:          true,
				Metadata:      meta,
			}
			p.logger.Info(ctx, "openai.stream.final_chunk_sent",
				observability.F("delta_length", len(finalDelta)),
				observability.F("thinking_length", accumulatedReasoning.Len()),
				observability.F("tool_calls", len(toolCalls)),
				observability.F("has_usage", usage != nil),
				observability.F("finish_reason", string(finishReason)),
			)
			return nil
		}

		// Parse chunk
		var chunk ChatCompletionChunk
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			p.logger.Warn(ctx, "openai.stream.parse_error",
				observability.F("error", err.Error()),
				observability.F("data", data),
			)
			continue
		}
		// Detect provider error events embedded in SSE data (e.g. xAI sends
		// {"error":{"message":"...","type":"..."}} when a request fails).
		if chunk.Error != nil && chunk.Error.Message != "" {
			p.logger.Error(ctx, "openai.stream.provider_error_event",
				observability.F("error_message", chunk.Error.Message),
				observability.F("error_type", chunk.Error.Type),
				observability.F("provider", p.name),
				observability.F("model", model),
			)
			return sdkerr.Transient(
				"openai.stream_provider_error",
				fmt.Sprintf("provider error in stream (provider: %s, model: %s): %s", p.name, model, chunk.Error.Message),
				sdkerr.WithOperation("openai.process_sse_stream"),
				sdkerr.WithComponent("provider.openai"),
				sdkerr.WithTraceFromContext(ctx),
			)
		}

		progress, postUsage := chunkHasMeaningfulProgress(chunk)
		if progress {
			resetIdleTimer(postUsage)
		}

		// VERBOSE: Log parsed chunk structure for debugging
		p.logger.Debug(ctx, "openai.stream.chunk_parsed",
			observability.F("id", chunk.ID),
			observability.F("model", chunk.Model),
			observability.F("choices_count", len(chunk.Choices)),
			observability.F("has_usage", chunk.Usage != nil),
		)

		// Capture the completion ID from the first chunk (e.g. "chatcmpl-xxx").
		// OpenAI attaches the same ID to every chunk; we only need it once.
		if responseID == "" && chunk.ID != "" {
			responseID = chunk.ID
		}

		// Extract usage information if present (OpenAI sends this with stream_options.include_usage=true)
		if chunk.Usage != nil {
			inputTok := chunk.Usage.InputCount()
			cachedTok := chunk.Usage.CachedTokens()
			uncached := inputTok
			if cachedTok > 0 && cachedTok <= inputTok {
				uncached = inputTok - cachedTok
			}
			usage = &conversation.TokenUsage{
				Input:     uncached,
				Output:    chunk.Usage.OutputCount(),
				Total:     chunk.Usage.TotalCount(),
				CacheRead: cachedTok,
			}
			p.logger.Debug(ctx, "openai.stream.usage_received",
				observability.F("input_tokens", usage.Input),
				observability.F("output_tokens", usage.Output),
				observability.F("total_tokens", usage.Total),
			)

			// VERBOSE: Log detailed usage breakdown for debugging
			p.logger.Info(ctx, "openai.stream.token_usage_update",
				observability.F("prompt_tokens", usage.Input),
				observability.F("completion_tokens", usage.Output),
				observability.F("total_tokens", usage.Total),
				observability.F("accumulated_text_length", accumulatedText.Len()),
			)

			// Send usage update chunk immediately for real-time token tracking
			// This allows the UI to update token counts during streaming
			if usage.Input > 0 || usage.Output > 0 || usage.CacheRead > 0 {
				chunks <- provider.StreamChunk{
					Delta: "",
					Usage: usage,
					Done:  false,
				}
				p.logger.Info(ctx, "openai.stream.usage_chunk_sent",
					observability.F("input_tokens", usage.Input),
					observability.F("output_tokens", usage.Output),
				)
			}
		}

		// Process choices
		for _, choice := range chunk.Choices {
			// VERBOSE: Log choice details
			textDelta, reasoningDelta := extractDeltaContent(choice.Delta)

			p.logger.Debug(ctx, "openai.stream.choice_processing",
				observability.F("choice_index", choice.Index),
				observability.F("has_content", textDelta != ""),
				observability.F("has_reasoning", reasoningDelta != ""),
				observability.F("has_tool_calls", len(choice.Delta.ToolCalls) > 0),
				observability.F("finish_reason", choice.FinishReason),
			)

			// Handle text content
			if textDelta != "" {
				accumulatedText.WriteString(textDelta)

				// VERBOSE: Log content delta for real-time tracking
				p.logger.Debug(ctx, "openai.stream.content_delta",
					observability.F("delta_length", len(textDelta)),
					observability.F("accumulated_length", accumulatedText.Len()),
					observability.F("delta_preview", truncateString(textDelta, 50)),
				)

				chunks <- provider.StreamChunk{
					Delta: textDelta,
					Done:  false,
				}
				p.logger.Info(ctx, "openai.stream.content_chunk_sent",
					observability.F("delta_length", len(textDelta)),
					observability.F("accumulated_length", accumulatedText.Len()),
				)
			}

			// Handle reasoning content (GLM, Cerebras, o1/o3 models)
			if reasoningDelta != "" {
				seenReasoning = true
				accumulatedReasoning.WriteString(reasoningDelta)
				// Re-arm the idle timer with the longer thinking timeout now
				// that we know this is a thinking stream. The initial reset
				// in chunkHasMeaningfulProgress fires before seenReasoning is
				// set, so it uses the shorter base idle timeout.
				resetIdleTimer(false)
				p.logger.Debug(ctx, "openai.stream.reasoning_delta",
					observability.F("reasoning_length", len(reasoningDelta)),
				)
			}

			// Handle tool calls
			if len(choice.Delta.ToolCalls) > 0 {
				if currentToolCalls == nil {
					currentToolCalls = make(map[int]*ToolCall)
				}

				for _, tc := range choice.Delta.ToolCalls {
					// Use the tool call's own index (not choice.Index) for proper
					// multi-tool-call accumulation across streaming chunks
					tcIndex := tc.Index
					existing, ok := currentToolCalls[tcIndex]
					if !ok {
						existing = &ToolCall{
							Index: tcIndex,
							ID:    tc.ID,
							Type:  tc.Type,
							Function: FunctionCall{
								Name:      tc.Function.Name,
								Arguments: "",
							},
						}
						currentToolCalls[tcIndex] = existing
					}

					// Update with new data
					if tc.ID != "" {
						existing.ID = tc.ID
					}
					if tc.Type != "" {
						existing.Type = tc.Type
					}
					if tc.Function.Name != "" {
						existing.Function.Name = tc.Function.Name
					}
					// Accumulate arguments (they come in chunks)
					existing.Function.Arguments += tc.Function.Arguments
				}
			}

			// Handle legacy function_call format (some providers like Z.AI/GLM)
			if choice.Delta.FunctionCall != nil {
				if currentToolCalls == nil {
					currentToolCalls = make(map[int]*ToolCall)
				}

				fc := choice.Delta.FunctionCall
				existing, ok := currentToolCalls[0]
				if !ok {
					existing = &ToolCall{
						ID:   fmt.Sprintf("call_%d", len(currentToolCalls)),
						Type: "function",
						Function: FunctionCall{
							Name:      fc.Name,
							Arguments: "",
						},
					}
					currentToolCalls[0] = existing
				}

				if fc.Name != "" {
					existing.Function.Name = fc.Name
				}
				existing.Function.Arguments += fc.Arguments

				p.logger.Info(ctx, "openai.stream.legacy_function_call",
					observability.F("name", fc.Name),
					observability.F("args_chunk_len", len(fc.Arguments)),
				)
			}

			// Handle finish reason
			if choice.FinishReason != nil && *choice.FinishReason != "" {
				finishReason = MapFinishReason(*choice.FinishReason)
				sawFinishReason = true
				p.logger.Debug(ctx, "openai.stream.finish_reason",
					observability.F("finish_reason", *choice.FinishReason),
				)
			}
		}

		// Check for context cancellation
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
	}

	// Convert accumulated tool calls to canonical format — iterate Index order
	// so output is deterministic across runs (see primary drain path above).
	if len(currentToolCalls) > 0 {
		indices := make([]int, 0, len(currentToolCalls))
		for idx := range currentToolCalls {
			indices = append(indices, idx)
		}
		sort.Ints(indices)
		for _, idx := range indices {
			toolCalls = append(toolCalls, TranslateToolCallFromOpenAI(*currentToolCalls[idx]))
		}

		// If we have tool calls, ensure finish reason reflects that
		if finishReason != provider.FinishReasonToolCalls {
			finishReason = provider.FinishReasonToolCalls
		}
		sawFinishReason = true
	}

	// If we reach here without [DONE] or a finish reason, treat as incomplete stream
	if !sawDone && !sawFinishReason {
		// Log diagnostic information to help debug provider-specific stream termination issues
		p.logger.Error(ctx, "openai.stream.incomplete_stream",
			observability.F("saw_done", sawDone),
			observability.F("saw_finish_reason", sawFinishReason),
			observability.F("accumulated_text_len", accumulatedText.Len()),
			observability.F("accumulated_reasoning_len", accumulatedReasoning.Len()),
			observability.F("tool_calls_count", len(currentToolCalls)),
			observability.F("sse_events_count", len(rawSSEEvents)),
			observability.F("has_usage", usage != nil),
		)

		// Dump last few SSE events at Warn level (visible in TUI debug panel) to diagnose
		// provider-specific stream termination (e.g. xAI sending an error SSE event).
		if len(rawSSEEvents) > 0 {
			start := max(len(rawSSEEvents)-5, 0)
			for i := start; i < len(rawSSEEvents); i++ {
				p.logger.Warn(ctx, "openai.stream.incomplete_sse_event",
					observability.F("event_index", i),
					observability.F("data", rawSSEEvents[i]),
				)
			}
		}

		// Return as Transient so streamChatWithProvider falls back to Chat().
		// Some providers (e.g. xAI) close the SSE stream without [DONE] when a
		// server-side error event is sent; Transient triggers the Chat() retry path.
		return sdkerr.Transient(
			"openai.stream_incomplete",
			fmt.Sprintf("stream ended without [DONE] or finish_reason (provider: %s, model: %s)", p.name, model),
			sdkerr.WithOperation("openai.process_sse_stream"),
			sdkerr.WithComponent("provider.openai"),
			sdkerr.WithTraceFromContext(ctx),
			sdkerr.WithContext("accumulated_text_length", accumulatedText.Len()),
			sdkerr.WithContext("tool_calls_count", len(currentToolCalls)),
			sdkerr.WithContext("sse_events_received", len(rawSSEEvents)),
		)
	}

	pristineText := accumulatedText.String()

	finalDelta := ""
	if accumulatedText.Len() == 0 && accumulatedReasoning.Len() > 0 {
		finalDelta = accumulatedReasoning.String()
		accumulatedText.WriteString(finalDelta)
	}

	// If we reach here, send final chunk (stream ended without [DONE] but with finish reason)
	if accumulatedText.Len() > 0 || len(toolCalls) > 0 || accumulatedReasoning.Len() > 0 {
		orderedBlocks := buildOpenAIOrderedBlocks(pristineText, accumulatedReasoning.String(), toolCalls)
		chunks <- provider.StreamChunk{
			Delta:         finalDelta,
			Thinking:      accumulatedReasoning.String(),
			FinishReason:  finishReason,
			Usage:         usage,
			ToolCalls:     toolCalls,
			OrderedBlocks: orderedBlocks,
			Done:          true,
		}
		p.logger.Info(ctx, "openai.stream.fallback_final_chunk_sent",
			observability.F("delta_length", len(finalDelta)),
			observability.F("thinking_length", accumulatedReasoning.Len()),
			observability.F("tool_calls", len(toolCalls)),
			observability.F("has_usage", usage != nil),
			observability.F("finish_reason", string(finishReason)),
		)
	}

	return nil
}

func chunkHasMeaningfulProgress(chunk ChatCompletionChunk) (progress bool, postUsage bool) {
	if chunk.Usage != nil {
		return true, true
	}
	for _, choice := range chunk.Choices {
		if choice.FinishReason != nil && strings.TrimSpace(*choice.FinishReason) != "" {
			return true, false
		}
		if chunkDeltaHasMeaningfulProgress(choice.Delta) {
			return true, false
		}
	}
	return false, false
}

func chunkDeltaHasMeaningfulProgress(delta ChunkDelta) bool {
	if strings.TrimSpace(delta.Role) != "" ||
		strings.TrimSpace(delta.Reasoning) != "" ||
		strings.TrimSpace(delta.ReasoningContent) != "" ||
		len(delta.ToolCalls) > 0 ||
		delta.FunctionCall != nil {
		return true
	}
	switch content := delta.Content.(type) {
	case string:
		return strings.TrimSpace(content) != ""
	case []any:
		return len(content) > 0
	case map[string]any:
		return len(content) > 0
	default:
		return content != nil
	}
}

func idleTimerChan(timer *time.Timer) <-chan time.Time {
	if timer == nil {
		return nil
	}
	return timer.C
}

func (p *Provider) recoverIdleTimedOutStream(
	ctx context.Context,
	chunks chan<- provider.StreamChunk,
	accumulatedText *strings.Builder,
	accumulatedReasoning *strings.Builder,
	usage *conversation.TokenUsage,
	finishReason provider.FinishReason,
	currentToolCalls map[int]*ToolCall,
	toolCalls []conversation.ToolCall,
	responseID string,
) bool {
	if usage == nil {
		p.logger.Warn(ctx, "openai.stream.idle_timeout_without_usage",
			observability.F("idle_timeout", openAIStreamIdleTimeout.String()),
			observability.F("accumulated_text_length", accumulatedText.Len()),
			observability.F("accumulated_reasoning_length", accumulatedReasoning.Len()),
			observability.F("tool_calls_count", len(currentToolCalls)+len(toolCalls)),
		)
		return false
	}

	if accumulatedText.Len() == 0 && accumulatedReasoning.Len() == 0 && len(currentToolCalls) == 0 && len(toolCalls) == 0 {
		p.logger.Warn(ctx, "openai.stream.idle_timeout_no_payload",
			observability.F("idle_timeout", openAIStreamIdleTimeout.String()),
		)
		return false
	}

	recoveredToolCalls := make([]conversation.ToolCall, 0, len(toolCalls)+len(currentToolCalls))
	recoveredToolCalls = append(recoveredToolCalls, toolCalls...)
	if len(currentToolCalls) > 0 {
		indices := make([]int, 0, len(currentToolCalls))
		for idx := range currentToolCalls {
			indices = append(indices, idx)
		}
		sort.Ints(indices)
		for _, idx := range indices {
			recoveredToolCalls = append(recoveredToolCalls, TranslateToolCallFromOpenAI(*currentToolCalls[idx]))
		}
	}

	if finishReason == "" {
		if len(recoveredToolCalls) > 0 {
			finishReason = provider.FinishReasonToolCalls
		} else {
			finishReason = provider.FinishReasonStop
		}
	}

	p.logger.Warn(ctx, "openai.stream.idle_timeout_recovered",
		observability.F("idle_timeout", openAIStreamIdleTimeout.String()),
		observability.F("accumulated_text_length", accumulatedText.Len()),
		observability.F("accumulated_reasoning_length", accumulatedReasoning.Len()),
		observability.F("tool_calls_count", len(recoveredToolCalls)),
		observability.F("finish_reason", string(finishReason)),
		observability.F("has_usage", usage != nil),
	)

	pristineText := accumulatedText.String()

	finalDelta := ""
	if accumulatedText.Len() == 0 && accumulatedReasoning.Len() > 0 {
		finalDelta = accumulatedReasoning.String()
		accumulatedText.WriteString(finalDelta)
	}

	meta := map[string]any{}
	if responseID != "" {
		meta["message_id"] = responseID
	}

	orderedBlocks := buildOpenAIOrderedBlocks(pristineText, accumulatedReasoning.String(), recoveredToolCalls)
	chunks <- provider.StreamChunk{
		Delta:         finalDelta,
		Thinking:      accumulatedReasoning.String(),
		FinishReason:  finishReason,
		Usage:         usage,
		ToolCalls:     recoveredToolCalls,
		OrderedBlocks: orderedBlocks,
		Done:          true,
		Metadata:      meta,
	}
	return true
}

// buildOpenAIOrderedBlocks assembles provider-native ordered blocks for an
// OpenAI-family assistant turn. OpenAI doesn't interleave blocks the way
// Anthropic does — response structure is [reasoning?, content?, tool_calls...].
// Sequence mirrors that order. Returns nil when nothing was produced so the
// agent's derivation fallback can step in.
func buildOpenAIOrderedBlocks(text, reasoning string, toolCalls []conversation.ToolCall) []conversation.MessageBlock {
	if text == "" && reasoning == "" && len(toolCalls) == 0 {
		return nil
	}
	blocks := make([]conversation.MessageBlock, 0, 2+len(toolCalls))
	seq := 0
	if reasoning != "" {
		blocks = append(blocks, conversation.MessageBlock{
			Type:     conversation.BlockTypeThinking,
			Content:  reasoning,
			Sequence: seq,
		})
		seq++
	}
	if text != "" {
		blocks = append(blocks, conversation.MessageBlock{
			Type:     conversation.BlockTypeContent,
			Content:  text,
			Sequence: seq,
		})
		seq++
	}
	for i := range toolCalls {
		tc := toolCalls[i]
		blocks = append(blocks, conversation.MessageBlock{
			Type:     conversation.BlockTypeToolCall,
			ToolCall: &tc,
			Sequence: seq,
		})
		seq++
	}
	return blocks
}

// truncateString truncates a string to maxLen characters for logging
func truncateString(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}

func extractDeltaContent(delta ChunkDelta) (string, string) {
	var textBuilder strings.Builder
	var reasoningBuilder strings.Builder

	appendText := func(s string) {
		if s != "" {
			textBuilder.WriteString(s)
		}
	}
	appendReasoning := func(s string) {
		if s != "" {
			reasoningBuilder.WriteString(s)
		}
	}

	handlePart := func(part map[string]any) {
		partType, _ := part["type"].(string)
		partType = strings.ToLower(partType)

		textVal, _ := part["text"].(string)
		contentVal, _ := part["content"].(string)
		val := textVal
		if val == "" {
			val = contentVal
		}

		switch partType {
		case "reasoning", "reasoning_content", "analysis", "thinking", "chain_of_thought":
			appendReasoning(val)
		default:
			appendText(val)
			if (partType == "" || strings.Contains(partType, "reason")) && textVal != "" {
				appendReasoning(textVal)
			}
		}
	}

	switch v := delta.Content.(type) {
	case string:
		appendText(v)
	case []any:
		for _, rawPart := range v {
			if partMap, ok := rawPart.(map[string]any); ok {
				handlePart(partMap)
				continue
			}
			if textVal, ok := rawPart.(string); ok {
				appendText(textVal)
			}
		}
	case map[string]any:
		handlePart(v)
	}

	appendReasoning(delta.Reasoning)
	appendReasoning(delta.ReasoningContent)

	return textBuilder.String(), reasoningBuilder.String()
}
