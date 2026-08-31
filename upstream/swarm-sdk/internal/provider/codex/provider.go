package codex

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/conversation"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/observability"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/provider"
	httplib "github.com/Swarm-Code/mono/swarm-sdk/internal/provider/http"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/sdkerr"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/vision"
	"github.com/google/uuid"
)

const applyPatchToolDescription = "The `apply_patch` tool can be used to edit files. This is a FREEFORM tool, so do not wrap the patch in JSON."

const defaultRequestTimeout = 15 * time.Minute

const applyPatchLarkGrammar = `start: begin_patch hunk+ end_patch
begin_patch: "*** Begin Patch" LF
end_patch: "*** End Patch" LF?
hunk: add_hunk | delete_hunk | update_hunk
add_hunk: "*** Add File: " filename LF add_line+
delete_hunk: "*** Delete File: " filename LF
update_hunk: "*** Update File: " filename LF change_move? change?
filename: /(.+)/
add_line: "+" /(.*)/ LF -> line
change_move: "*** Move to: " filename LF
change: (change_context | change_line)+ eof_line?
change_context: ("@@" | "@@ " /(.+)/) LF
change_line: ("+" | "-" | " ") /(.*)/ LF
eof_line: "*** End of File" LF
%import common.LF`

// Config contains Codex (ChatGPT backend) credentials.
type Config struct {
	AccessToken string
	AccountID   string
	BaseURL     string
	Logger      observability.Logger
	Tracer      observability.Tracer
	// ExtraQueryParams appends raw query parameters to Codex requests.
	ExtraQueryParams map[string]string
	// ExtraHeaders appends raw HTTP headers to Codex requests.
	ExtraHeaders map[string]string

	// TransportMode controls the HTTP transport configuration.
	// Options: "default", "resilient", "aggressive", "unstable", "fast"
	// Default: "resilient"
	TransportMode string

	// Timeout is the request timeout in seconds.
	// 0 means use default (15m), -1 means no timeout.
	Timeout int

	// DialTimeout is the maximum time to establish a TCP connection in seconds.
	DialTimeout int

	// TCPKeepAlive is the interval for TCP keepalive probes in seconds.
	TCPKeepAlive int

	// RawDebugWriter, if set, enables raw JSON request/response logging
	// All HTTP request/response bodies will be written to this writer
	RawDebugWriter io.Writer

	// ModelOptions carries per-model request conventions from the fetched
	// catalog (see ModelWireOptionsFromCache). Models without an entry fall
	// back to prefix heuristics: gpt-5.6* is Responses-Lite and supports the
	// max effort; unknown models do not request parallel tool calls
	// (matching codex-rs's fallback metadata for unknown slugs).
	ModelOptions map[string]ModelWireOptions
}

// ModelWireOptions carries per-model request conventions derived from the
// backend model catalog.
type ModelWireOptions struct {
	// ResponsesLite selects the Responses-Lite request shape (empty
	// instructions, tools moved into input, reasoning.context=all_turns).
	ResponsesLite bool
	// ParallelToolCalls mirrors the catalog's supports_parallel_tool_calls.
	ParallelToolCalls bool
	// SupportsMaxEffort reports whether the model accepts the "max"
	// reasoning effort; when false, "max" is clamped to "xhigh".
	SupportsMaxEffort bool
}

// Provider implements provider.Provider for the ChatGPT Codex backend.
type Provider struct {
	accessToken string
	accountID   string
	baseURL     string
	logger      observability.Logger
	tracer      observability.Tracer
	httpClient  *http.Client
	extraQuery  map[string]string
	extraHeader map[string]string
	// sessionID is a stable per-provider-instance fallback identifier sent
	// as the session-id header when no conversation ID is available.
	sessionID    string
	modelOptions map[string]ModelWireOptions

	// limitsMu guards latestRateLimits, captured from response headers on
	// every request (see ratelimits.go).
	limitsMu         sync.Mutex
	latestRateLimits *RateLimitSnapshot
}

// New creates a Codex provider.
func New(cfg Config) (*Provider, error) {
	if cfg.AccessToken == "" {
		return nil, fmt.Errorf("codex access token is required")
	}
	if cfg.AccountID == "" {
		return nil, fmt.Errorf("codex account id is required")
	}

	baseURL := cfg.BaseURL
	if baseURL == "" {
		baseURL = "https://chatgpt.com/backend-api/codex"
	}

	// Create resilient HTTP client
	client := createResilientHTTPClientCodex(cfg)

	// Wrap with debug transport if RawDebugWriter is set
	if cfg.RawDebugWriter != nil {
		base := client.Transport
		if base == nil {
			base = http.DefaultTransport
		}
		client.Transport = provider.NewDebugTransport(base, cfg.RawDebugWriter)
	}

	modelOptions := make(map[string]ModelWireOptions, len(cfg.ModelOptions))
	for model, opts := range cfg.ModelOptions {
		modelOptions[model] = opts
	}

	return &Provider{
		accessToken:  cfg.AccessToken,
		accountID:    cfg.AccountID,
		baseURL:      strings.TrimRight(baseURL, "/"),
		logger:       cfg.Logger,
		tracer:       cfg.Tracer,
		httpClient:   client,
		extraQuery:   cloneStringMap(cfg.ExtraQueryParams),
		extraHeader:  cloneStringMap(cfg.ExtraHeaders),
		sessionID:    uuid.NewString(),
		modelOptions: modelOptions,
	}, nil
}

// wireOptions resolves the per-model request conventions. The
// catalog-provided map wins; without an entry, the gpt-5.6 family defaults
// to lite + max effort, and unknown models do not request parallel tool
// calls (matching codex-rs's fallback metadata for unknown slugs).
func (p *Provider) wireOptions(model string) ModelWireOptions {
	if opts, ok := p.modelOptions[model]; ok {
		return opts
	}
	isGpt56 := strings.HasPrefix(model, "gpt-5.6")
	return ModelWireOptions{
		ResponsesLite:     isGpt56,
		ParallelToolCalls: false,
		SupportsMaxEffort: isGpt56,
	}
}

// createResilientHTTPClientCodex creates an HTTP client with resilient transport settings.
func createResilientHTTPClientCodex(cfg Config) *http.Client {
	// Default transport mode to resilient
	transportMode := cfg.TransportMode
	if transportMode == "" {
		transportMode = "resilient"
	}

	// Select transport configuration based on mode
	var transportConfig httplib.TransportConfig
	switch transportMode {
	case "aggressive":
		transportConfig = httplib.AggressiveRetryTransportConfig()
	case "unstable":
		transportConfig = httplib.UnstableNetworkTransportConfig()
	case "fast":
		transportConfig = httplib.FastTransportConfig()
	case "default":
		// Use basic client with just timeout
		timeout := cfg.Timeout
		if timeout == 0 {
			timeout = int(defaultRequestTimeout / time.Second)
		}
		var clientTimeout time.Duration
		if timeout == -1 {
			clientTimeout = 0
		} else {
			clientTimeout = time.Duration(timeout) * time.Second
		}
		return &http.Client{Timeout: clientTimeout}
	default: // "resilient" or empty
		transportConfig = httplib.DefaultTransportConfig()
	}

	// Override specific settings from config if provided
	if cfg.DialTimeout > 0 {
		transportConfig.DialTimeout = time.Duration(cfg.DialTimeout) * time.Second
	}
	if cfg.TCPKeepAlive > 0 {
		transportConfig.TCPKeepAlive = time.Duration(cfg.TCPKeepAlive) * time.Second
	}

	// Create transport with our settings
	transport := httplib.NewTransport(transportConfig)

	// Determine client timeout
	timeout := cfg.Timeout
	if timeout == 0 {
		timeout = int(defaultRequestTimeout / time.Second)
	}

	var clientTimeout time.Duration
	if timeout == -1 {
		clientTimeout = 0 // No timeout
	} else {
		clientTimeout = time.Duration(timeout) * time.Second
	}

	return &http.Client{
		Transport: transport,
		Timeout:   clientTimeout,
	}
}

func cloneStringMap(input map[string]string) map[string]string {
	if len(input) == 0 {
		return nil
	}
	clone := make(map[string]string, len(input))
	for key, value := range input {
		var trimmedKey string = strings.TrimSpace(key)
		var trimmedValue string = strings.TrimSpace(value)
		if trimmedKey == "" || trimmedValue == "" {
			continue
		}
		clone[trimmedKey] = trimmedValue
	}
	if len(clone) == 0 {
		return nil
	}
	return clone
}

// Name implements provider.Provider.
func (p *Provider) Name() string {
	return "codex"
}

// Capabilities implements provider.Provider.
func (p *Provider) Capabilities() provider.Capabilities {
	return provider.Capabilities{
		Streaming:            true,
		FunctionCalling:      true,
		Vision:               false,
		MaxContextWindow:     0,
		MaxOutputTokens:      0,
		SupportsSystemPrompt: true,
		SupportsTemperature:  true,
		SupportedModels:      nil,
		PromptCaching:        false,
		SupportsJSON:         true,
	}
}

// Chat implements provider.Provider.
func (p *Provider) Chat(ctx context.Context, req provider.ChatRequest) (*provider.ChatResponse, error) {
	stream, err := p.Stream(ctx, req)
	if err != nil {
		return nil, err
	}

	var contentBuilder strings.Builder
	var toolCalls []conversation.ToolCall
	var finalUsage *conversation.TokenUsage
	var finishReason provider.FinishReason
	var finalMetadata map[string]any
	var orderedBlocks []conversation.MessageBlock
	for chunk := range stream {
		if chunk.Error != nil {
			return nil, chunk.Error
		}
		contentBuilder.WriteString(chunk.Delta)
		if len(chunk.ToolCalls) > 0 {
			toolCalls = append(toolCalls, chunk.ToolCalls...)
		}
		if chunk.FinishReason != "" {
			finishReason = chunk.FinishReason
		}
		if chunk.Done {
			finalUsage = chunk.Usage
			finalMetadata = chunk.Metadata
			orderedBlocks = chunk.OrderedBlocks
		}
	}
	if finishReason == "" {
		if len(toolCalls) > 0 {
			finishReason = provider.FinishReasonToolCalls
		} else {
			finishReason = provider.FinishReasonStop
		}
	}

	message := &conversation.Message{
		Role:          conversation.RoleAssistant,
		Content:       contentBuilder.String(),
		ToolCalls:     toolCalls,
		OrderedBlocks: orderedBlocks,
		Metadata:      finalMetadata,
	}

	return &provider.ChatResponse{
		Message:      message,
		FinishReason: finishReason,
		Usage:        finalUsage,
		RawResponse:  nil,
	}, nil
}

// LatestRateLimits returns the usage snapshot captured from the most recent
// response's headers, or nil when no request has completed yet.
func (p *Provider) LatestRateLimits() *RateLimitSnapshot {
	p.limitsMu.Lock()
	defer p.limitsMu.Unlock()
	if p.latestRateLimits == nil {
		return nil
	}
	cp := *p.latestRateLimits
	return &cp
}

// statusError converts a non-200 codex response into an error. Rate-limit
// responses (HTTP 429, or a usage_limit_reached/usage_not_included detail)
// become typed sdkerr rate-limit errors so the orchestrator's retry/rotation
// and the TUI's account rotation can recognize them.
func (p *Provider) statusError(resp *http.Response, body []byte) error {
	detail := strings.TrimSpace(string(body))
	var parsed struct {
		Detail string `json:"detail"`
	}
	if json.Unmarshal(body, &parsed) == nil && strings.TrimSpace(parsed.Detail) != "" {
		detail = strings.TrimSpace(parsed.Detail)
	}

	isLimit := resp.StatusCode == http.StatusTooManyRequests ||
		strings.Contains(detail, "usage_limit_reached") ||
		strings.Contains(detail, "usage_not_included")
	if !isLimit {
		return fmt.Errorf("codex http %d: %s", resp.StatusCode, detail)
	}

	// Retry-after: prefer the standard header, else the primary window's
	// reset. Capped — a weekly window resets in days, and an uncapped value
	// would stall the orchestrator's retry sleep; rotation/fallback paths
	// consult the snapshot store for the true reset time instead.
	const retryAfterCap = 60 * time.Second
	var retryAfter time.Duration
	if ra := headerInt(resp.Header, "retry-after"); ra > 0 {
		retryAfter = time.Duration(ra) * time.Second
	} else if reset := headerInt(resp.Header, "x-codex-primary-reset-after-seconds"); reset > 0 {
		retryAfter = time.Duration(reset) * time.Second
	}
	if retryAfter <= 0 || retryAfter > retryAfterCap {
		retryAfter = retryAfterCap
	}
	if detail == "" {
		detail = "ChatGPT subscription rate limit reached"
	}
	return sdkerr.Transient("codex.rate_limited", detail, sdkerr.WithRetryAfter(retryAfter))
}

// Stream implements provider.Provider.
func (p *Provider) Stream(ctx context.Context, req provider.ChatRequest) (<-chan provider.StreamChunk, error) {
	if p.tracer != nil {
		var span observability.Span
		ctx, span = p.tracer.StartSpan(ctx, "codex.stream")
		defer span.End()
	}

	wire := p.wireOptions(req.Model)
	liteMode := wire.ResponsesLite
	threadID, _ := req.Metadata["conversation_id"].(string)
	// The session id doubles as the prompt cache key, matching codex-rs
	// where both are conversation-scoped.
	sessionID := threadID
	if sessionID == "" {
		sessionID = p.sessionID
	}

	payload, err := buildRequestWithOptions(req, requestOptions{
		responsesLite:     liteMode,
		parallelToolCalls: wire.ParallelToolCalls,
		supportsMaxEffort: wire.SupportsMaxEffort,
		promptCacheKey:    sessionID,
	})
	if err != nil {
		return nil, err
	}

	bodyBytes, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	endpoint, err := url.Parse(p.baseURL + "/responses")
	if err != nil {
		return nil, fmt.Errorf("failed to parse codex url: %w", err)
	}
	if len(p.extraQuery) > 0 {
		query := endpoint.Query()
		for key, value := range p.extraQuery {
			query.Set(key, value)
		}
		endpoint.RawQuery = query.Encode()
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint.String(), bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "text/event-stream")
	httpReq.Header.Set("Authorization", "Bearer "+p.accessToken)
	httpReq.Header.Set("ChatGPT-Account-ID", p.accountID)
	httpReq.Header.Set("originator", "swarmos")
	httpReq.Header.Set("version", codexClientVersion)
	httpReq.Header.Set("User-Agent", codexUserAgent())
	// Stable identifiers (codex-rs parity): session-id, thread-id, and
	// x-client-request-id are conversation-scoped, and session-id matches
	// prompt_cache_key. The backend keys prompt caching and sticky routing
	// on these; the previous per-request random UUID headers defeated both.
	httpReq.Header.Set("session-id", sessionID)
	if threadID != "" {
		httpReq.Header.Set("thread-id", threadID)
		httpReq.Header.Set("x-client-request-id", threadID)
	}
	if liteMode {
		httpReq.Header.Set("x-openai-internal-codex-responses-lite", "true")
	}
	for key, value := range p.extraHeader {
		httpReq.Header.Set(key, value)
	}

	resp, err := p.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("codex request failed: %w", err)
	}

	// Harvest the x-codex-* usage headers on every response (success or
	// failure) — this passive capture is the only usage data source the
	// backend offers. Persistence is advisory telemetry; never fail the send.
	if snap := ParseRateLimitHeaders(resp.Header); snap != nil {
		snap.AccountID = p.accountID
		p.limitsMu.Lock()
		p.latestRateLimits = snap
		p.limitsMu.Unlock()
		_ = SaveRateLimitSnapshot(*snap)
	}

	// Surface non-200s as a synchronous error rather than an in-channel chunk:
	// the orchestrator's retry/rotation machinery only inspects the error
	// returned from Stream itself, so an in-channel 429 was invisible to it.
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		return nil, p.statusError(resp, b)
	}

	ch := make(chan provider.StreamChunk, 8)

	go func() {
		defer close(ch)
		defer resp.Body.Close()

		scanner := bufio.NewScanner(resp.Body)
		scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
		var streamedText strings.Builder
		var streamedThinking strings.Builder
		var streamedToolCalls []conversation.ToolCall
		toolCallIndex := make(map[string]int)
		// orderedBlocks preserves provider-native ordering of content and
		// tool_call blocks across the stream. Codex events arrive in stream
		// order, so we can build the list directly as events flow:
		// text deltas extend the trailing content block, output_item.done
		// tool calls append tool_call blocks, and upserts patch existing
		// tool_call entries in place.
		var orderedBlocks []conversation.MessageBlock
		var finalUsage *conversation.TokenUsage
		// rawOutputItems collects this turn's raw output items (reasoning,
		// function_call, message, ...) in exact stream order so the agent
		// can replay them verbatim on the next turn. The Responses API with
		// store:false requires each reasoning item to be immediately
		// followed by its original successor, so order must be preserved —
		// mirrors codex-rs, which replays recorded history items as-is.
		var rawOutputItems []json.RawMessage
		finalized := false

		appendContent := func(delta string) {
			if delta == "" {
				return
			}
			streamedText.WriteString(delta)
			if n := len(orderedBlocks); n > 0 && orderedBlocks[n-1].Type == conversation.BlockTypeContent {
				orderedBlocks[n-1].Content += delta
				return
			}
			orderedBlocks = append(orderedBlocks, conversation.MessageBlock{
				Type:     conversation.BlockTypeContent,
				Content:  delta,
				Sequence: len(orderedBlocks),
			})
		}
		appendThinking := func(delta string) {
			if delta == "" {
				return
			}
			streamedThinking.WriteString(delta)
			if n := len(orderedBlocks); n > 0 && orderedBlocks[n-1].Type == conversation.BlockTypeThinking {
				orderedBlocks[n-1].Content += delta
				return
			}
			orderedBlocks = append(orderedBlocks, conversation.MessageBlock{
				Type:     conversation.BlockTypeThinking,
				Content:  delta,
				Sequence: len(orderedBlocks),
			})
		}
		addToolCall := func(call conversation.ToolCall) {
			if call.ID == "" || call.Name == "" {
				return
			}
			if idx, exists := toolCallIndex[call.ID]; exists {
				// `output_item.added` often carries placeholder args while
				// `output_item.done` carries finalized args. Upsert by call ID.
				if len(call.Parameters) > 0 {
					streamedToolCalls[idx].Parameters = call.Parameters
					// Keep the matching block's ToolCall pointer in sync so
					// the final OrderedBlocks snapshot carries finalized args.
					for i := range orderedBlocks {
						if orderedBlocks[i].Type != conversation.BlockTypeToolCall {
							continue
						}
						if orderedBlocks[i].ToolCall != nil && orderedBlocks[i].ToolCall.ID == call.ID {
							updated := streamedToolCalls[idx]
							orderedBlocks[i].ToolCall = &updated
							break
						}
					}
				}
				return
			}
			toolCallIndex[call.ID] = len(streamedToolCalls)
			streamedToolCalls = append(streamedToolCalls, call)
			copied := call
			orderedBlocks = append(orderedBlocks, conversation.MessageBlock{
				Type:     conversation.BlockTypeToolCall,
				ToolCall: &copied,
				Sequence: len(orderedBlocks),
			})
		}
		emitMissingText := func(candidate string) {
			if missing := missingSuffix(streamedText.String(), strings.TrimSpace(candidate)); missing != "" {
				appendContent(missing)
				ch <- provider.StreamChunk{Delta: missing}
			}
		}
		emitMissingThinking := func(candidate string) {
			if missing := missingSuffix(streamedThinking.String(), candidate); missing != "" {
				appendThinking(missing)
				ch <- provider.StreamChunk{Thinking: missing}
			}
		}
		finalize := func(reason provider.FinishReason) {
			if finalized {
				return
			}
			finalized = true
			if reason == "" {
				if len(streamedToolCalls) > 0 {
					reason = provider.FinishReasonToolCalls
				} else {
					reason = provider.FinishReasonStop
				}
			}
			snapshot := make([]conversation.MessageBlock, len(orderedBlocks))
			copy(snapshot, orderedBlocks)
			var metadata map[string]any
			if len(rawOutputItems) > 0 {
				metadata = map[string]any{
					codexOutputItemsMetadataKey: rawOutputItems,
				}
			}
			ch <- provider.StreamChunk{
				Done:          true,
				FinishReason:  reason,
				Usage:         finalUsage,
				ToolCalls:     streamedToolCalls,
				OrderedBlocks: snapshot,
				Metadata:      metadata,
			}
		}

		for scanner.Scan() {
			line := scanner.Text()
			// Expect SSE lines starting with "data:"
			if !strings.HasPrefix(line, "data:") {
				continue
			}
			data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
			if data == "" {
				continue
			}
			if data == "[DONE]" {
				finalize("")
				return
			}

			var event codexEvent
			if err := json.Unmarshal([]byte(data), &event); err != nil {
				ch <- provider.StreamChunk{Error: fmt.Errorf("failed to parse codex event: %w", err), Done: true}
				return
			}

			switch event.Type {
			case "response.output_text.delta":
				if event.Delta != "" {
					appendContent(event.Delta)
				}
				ch <- provider.StreamChunk{
					Delta: event.Delta,
				}
			case "response.reasoning_summary_text.delta", "response.reasoning_text.delta":
				if event.Delta != "" {
					appendThinking(event.Delta)
					ch <- provider.StreamChunk{Thinking: event.Delta}
				}
			case "response.reasoning_summary_text.done", "response.reasoning_text.done":
				// Some backends emit the full reasoning text at done without every delta.
				emitMissingThinking(event.Text)
			case "response.reasoning_summary_part.done":
				if event.Part != nil {
					emitMissingThinking(event.Part.Text)
				}
			case "response.output_text.done":
				// Some Codex backends emit full text at done/completed events instead of all deltas.
				emitMissingText(event.Text)
			case "response.content_part.done":
				if event.Part != nil && event.Part.Type == "output_text" {
					emitMissingText(event.Part.Text)
				}
			case "response.output_item.done", "response.output_item.added":
				if event.Item != nil {
					if event.Item.Type == "reasoning" {
						emitMissingThinking(event.Item.reasoningDisplayText())
					}
					emitMissingText(event.Item.outputText())
					if call, ok := event.Item.toToolCall(); ok {
						addToolCall(call)
					}
					if event.Type == "response.output_item.done" {
						switch event.Item.Type {
						case "reasoning", "function_call", "custom_tool_call", "message":
							if raw := extractRawEventItem(data); raw != nil {
								rawOutputItems = append(rawOutputItems, raw)
							}
						}
					}
				}
			case "response.completed", "response.done":
				if event.Response != nil {
					emitMissingThinking(event.Response.reasoningDisplayText())
					emitMissingText(event.Response.outputText())
					for _, call := range event.Response.outputToolCalls() {
						addToolCall(call)
					}
					finalUsage = event.Response.toTokenUsage()
				}
				finalize("")
				return
			case "response.failed", "error":
				msg := "codex error"
				if event.Error != nil && event.Error.Message != "" {
					msg = event.Error.Message
				}
				if event.Response != nil && event.Response.Error != nil && event.Response.Error.Message != "" {
					msg = event.Response.Error.Message
				}
				ch <- provider.StreamChunk{Error: fmt.Errorf("%s", msg), Done: true}
				return
			}
		}

		if err := scanner.Err(); err != nil && ctx.Err() == nil {
			ch <- provider.StreamChunk{Error: fmt.Errorf("codex stream error: %w", err), Done: true}
			return
		}
		if ctx.Err() == nil && !finalized {
			if streamedText.Len() > 0 || streamedThinking.Len() > 0 || len(streamedToolCalls) > 0 || finalUsage != nil {
				finalize("")
				return
			}
			ch <- provider.StreamChunk{Error: fmt.Errorf("codex stream ended without completion event"), Done: true}
		}
	}()

	return ch, nil
}

// codexEvent represents a minimal subset of SSE events.
type codexEvent struct {
	Type     string            `json:"type"`
	Delta    string            `json:"delta,omitempty"`
	Text     string            `json:"text,omitempty"`
	Part     *codexContentPart `json:"part,omitempty"`
	Item     *codexOutputItem  `json:"item,omitempty"`
	Response *codexResponse    `json:"response,omitempty"`
	Error    *codexEventError  `json:"error,omitempty"`
}

type codexEventError struct {
	Message string `json:"message"`
}

type codexResponse struct {
	Usage *struct {
		InputTokens        int `json:"input_tokens"`
		OutputTokens       int `json:"output_tokens"`
		InputTokensDetails *struct {
			CachedTokens int `json:"cached_tokens"`
		} `json:"input_tokens_details,omitempty"`
	} `json:"usage"`
	Output []codexOutputItem `json:"output,omitempty"`
	Error  *codexEventError  `json:"error,omitempty"`
}

type codexOutputItem struct {
	ID        string             `json:"id,omitempty"`
	Type      string             `json:"type,omitempty"`
	Role      string             `json:"role,omitempty"`
	Content   []codexContentPart `json:"content,omitempty"`
	Summary   []codexSummaryPart `json:"summary,omitempty"`
	CallID    string             `json:"call_id,omitempty"`
	Name      string             `json:"name,omitempty"`
	Arguments string             `json:"arguments,omitempty"`
	Input     string             `json:"input,omitempty"`
}

type codexSummaryPart struct {
	Type string `json:"type,omitempty"`
	Text string `json:"text,omitempty"`
}

func (i codexOutputItem) reasoningSummary() string {
	var builder strings.Builder
	for _, part := range i.Summary {
		if part.Type == "" || part.Type == "summary_text" {
			builder.WriteString(part.Text)
		}
	}
	return builder.String()
}

func (r *codexResponse) reasoningSummary() string {
	if r == nil {
		return ""
	}
	var builder strings.Builder
	for _, item := range r.Output {
		if item.Type == "reasoning" {
			builder.WriteString(item.reasoningSummary())
		}
	}
	return builder.String()
}

func (i codexOutputItem) reasoningText() string {
	var builder strings.Builder
	for _, part := range i.Content {
		if part.Type == "reasoning_text" {
			builder.WriteString(part.Text)
		}
	}
	return builder.String()
}

func (i codexOutputItem) reasoningDisplayText() string {
	if text := i.reasoningText(); text != "" {
		return text
	}
	return i.reasoningSummary()
}

func (r *codexResponse) reasoningDisplayText() string {
	if r == nil {
		return ""
	}
	var builder strings.Builder
	for _, item := range r.Output {
		if item.Type == "reasoning" {
			builder.WriteString(item.reasoningDisplayText())
		}
	}
	return builder.String()
}

func (i codexOutputItem) outputText() string {
	var builder strings.Builder
	for _, part := range i.Content {
		if part.Type == "output_text" {
			builder.WriteString(part.Text)
		}
	}
	return builder.String()
}

func (i codexOutputItem) toToolCall() (conversation.ToolCall, bool) {
	if i.Type != "function_call" && i.Type != "custom_tool_call" {
		return conversation.ToolCall{}, false
	}
	callID := strings.TrimSpace(i.CallID)
	if callID == "" {
		callID = strings.TrimSpace(i.ID)
	}
	name := strings.TrimSpace(i.Name)
	if callID == "" || name == "" {
		return conversation.ToolCall{}, false
	}

	rawArgs := strings.TrimSpace(i.Arguments)
	rawInput := strings.TrimSpace(i.Input)

	parameters := parseToolParameters(rawArgs, name)
	// Some backends populate `arguments` as "{}" while carrying usable
	// payload in `input`; prefer `input` when arguments decode to empty.
	if len(parameters) == 0 && rawInput != "" {
		parameters = parseToolParameters(rawInput, name)
	}
	parameters = normalizeToolParameters(name, parameters)

	return conversation.ToolCall{
		ID:         callID,
		Name:       name,
		Parameters: parameters,
	}, true
}

func parseToolParameters(raw string, toolName string) map[string]any {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return map[string]any{}
	}

	var parameters map[string]any
	if err := json.Unmarshal([]byte(raw), &parameters); err == nil && parameters != nil {
		return parameters
	}

	// Some backends send arguments as an encoded JSON string.
	var inner string
	if err := json.Unmarshal([]byte(raw), &inner); err == nil {
		inner = strings.TrimSpace(inner)
		if inner != "" {
			if err := json.Unmarshal([]byte(inner), &parameters); err == nil && parameters != nil {
				return parameters
			}
			raw = inner
		}
	}

	switch strings.ToLower(strings.TrimSpace(toolName)) {
	case "bash", "exec_command", "shell", "shell_command":
		return map[string]any{
			"command": raw,
		}
	case "apply_patch":
		return map[string]any{
			"input": raw,
		}
	default:
		return map[string]any{
			"_raw_arguments": raw,
		}
	}
}

func normalizeToolParameters(toolName string, params map[string]any) map[string]any {
	if params == nil {
		return map[string]any{}
	}

	lowerName := strings.ToLower(strings.TrimSpace(toolName))
	switch lowerName {
	case "bash", "exec_command", "shell", "shell_command":
		if _, ok := params["command"]; !ok {
			if value, ok := stringParam(params, "cmd", "input", "text"); ok {
				params["command"] = value
			}
		}
		if _, ok := params["cwd"]; !ok {
			if value, ok := stringParam(params, "workdir", "working_directory", "workingDir"); ok {
				params["cwd"] = value
			}
		}
	case "write":
		if _, ok := params["file_path"]; !ok {
			if value, ok := stringParam(params, "path", "filepath", "file", "filename"); ok {
				params["file_path"] = value
			}
		}
		if _, ok := params["content"]; !ok {
			if value, ok := stringParam(params, "text", "body", "contents"); ok {
				params["content"] = value
			}
		}
	}

	return params
}

func stringParam(params map[string]any, keys ...string) (string, bool) {
	for _, key := range keys {
		value, ok := params[key]
		if !ok {
			continue
		}
		switch typed := value.(type) {
		case string:
			trimmed := strings.TrimSpace(typed)
			if trimmed != "" {
				return trimmed, true
			}
		}
	}
	return "", false
}

type codexContentPart struct {
	Type string `json:"type,omitempty"`
	Text string `json:"text,omitempty"`
}

func (r codexResponse) outputText() string {
	var builder strings.Builder
	for _, item := range r.Output {
		builder.WriteString(item.outputText())
	}
	return builder.String()
}

func (r codexResponse) outputToolCalls() []conversation.ToolCall {
	if len(r.Output) == 0 {
		return nil
	}
	toolCalls := make([]conversation.ToolCall, 0, len(r.Output))
	for _, item := range r.Output {
		if toolCall, ok := item.toToolCall(); ok {
			toolCalls = append(toolCalls, toolCall)
		}
	}
	return toolCalls
}

func (r codexResponse) toTokenUsage() *conversation.TokenUsage {
	if r.Usage == nil {
		return nil
	}
	// The Responses API reports input_tokens INCLUSIVE of the cached prefix;
	// input_tokens_details.cached_tokens is the portion served from cache.
	// Split into uncached Input vs CacheRead to match the Anthropic-style
	// convention used across the SDK (see openai/translate.go).
	cached := 0
	if r.Usage.InputTokensDetails != nil {
		cached = r.Usage.InputTokensDetails.CachedTokens
	}
	uncached := r.Usage.InputTokens
	if cached > 0 && cached <= r.Usage.InputTokens {
		uncached = r.Usage.InputTokens - cached
	}
	return &conversation.TokenUsage{
		Input:     uncached,
		Output:    r.Usage.OutputTokens,
		CacheRead: cached,
		Total:     r.Usage.InputTokens + r.Usage.OutputTokens,
	}
}

func missingSuffix(current string, candidate string) string {
	if candidate == "" {
		return ""
	}
	if current == "" {
		return candidate
	}
	if strings.HasPrefix(candidate, current) {
		return candidate[len(current):]
	}
	// Fallback: if current diverges, avoid duplicating content in stream updates.
	return ""
}

// codexOutputItemsMetadataKey is where the raw ordered output items of an
// assistant turn (reasoning items with encrypted_content, function calls,
// messages) are stashed on the message for verbatim in-order replay.
const codexOutputItemsMetadataKey = "codex_output_items"

// requestOptions carries per-request Codex conventions resolved by the
// provider (model catalog wire flags, stable cache key).
type requestOptions struct {
	responsesLite     bool
	parallelToolCalls bool
	supportsMaxEffort bool
	promptCacheKey    string
}

// extractRawEventItem returns the raw JSON of an SSE event's "item" field.
func extractRawEventItem(data string) json.RawMessage {
	var shadow struct {
		Item json.RawMessage `json:"item"`
	}
	if err := json.Unmarshal([]byte(data), &shadow); err != nil {
		return nil
	}
	return shadow.Item
}

// codexRawOutputItems returns the ordered replayable output items stored on
// an assistant message. Handles both in-memory ([]json.RawMessage) and
// JSON-round-tripped ([]any of maps) metadata forms. Server-assigned item
// ids and status fields are stripped: codex-rs removes all item ids before
// sending when store:false, and the backend rejects ids of unstored items.
func codexRawOutputItems(msg *conversation.Message) []map[string]any {
	if msg == nil || msg.Metadata == nil {
		return nil
	}
	stored, ok := msg.Metadata[codexOutputItemsMetadataKey]
	if !ok {
		return nil
	}
	appendRaw := func(out []map[string]any, raw []byte) []map[string]any {
		var item map[string]any
		if err := json.Unmarshal(raw, &item); err != nil || item == nil {
			return out
		}
		return append(out, item)
	}
	var out []map[string]any
	switch items := stored.(type) {
	case []json.RawMessage:
		for _, raw := range items {
			out = appendRaw(out, raw)
		}
	case []any:
		for _, entry := range items {
			switch v := entry.(type) {
			case map[string]any:
				out = append(out, v)
			case json.RawMessage:
				out = appendRaw(out, v)
			case string:
				out = appendRaw(out, []byte(v))
			}
		}
	}
	for _, item := range out {
		delete(item, "id")
		delete(item, "status")
	}
	return out
}

// buildRequest converts the canonical request into Codex backend format
// using default per-request options (kept for tests and non-stream callers).
func buildRequest(req provider.ChatRequest) (map[string]any, error) {
	return buildRequestWithOptions(req, requestOptions{})
}

// buildRequestWithOptions converts the canonical request into Codex backend
// format, mirroring codex-rs build_responses_request (core/src/client.rs).
func buildRequestWithOptions(req provider.ChatRequest, opts requestOptions) (map[string]any, error) {
	tools := make([]map[string]any, 0, len(req.Tools))
	for _, t := range req.Tools {
		// Codex models are post-trained on the freeform V4A apply_patch tool.
		// Avoid wrapping the patch in JSON: the named custom-tool shape reduces
		// formatting failures and is the contract used by the Codex harness.
		if strings.EqualFold(strings.TrimSpace(t.Name), "apply_patch") {
			tools = append(tools, map[string]any{
				"type":        "custom",
				"name":        "apply_patch",
				"description": applyPatchToolDescription,
				"format": map[string]any{
					"type":       "grammar",
					"syntax":     "lark",
					"definition": applyPatchLarkGrammar,
				},
			})
			continue
		}
		schema := provider.NormalizeToolParametersSchema(t.Parameters)
		if schema == nil {
			schema = map[string]any{
				"type":                 "object",
				"properties":           map[string]any{},
				"additionalProperties": false,
			}
		}

		tool := map[string]any{
			"type":        "function",
			"name":        t.Name,
			"description": t.Description,
			"strict":      false,
			"parameters":  schema,
		}
		tools = append(tools, tool)
	}

	// Persisted legacy ToolResults may omit Name. Correlate call IDs with the
	// assistant calls so custom apply_patch outputs retain their wire type.
	customCallIDs := make(map[string]bool)
	for _, msg := range req.Messages {
		if msg == nil {
			continue
		}
		for _, call := range msg.ToolCalls {
			if strings.EqualFold(strings.TrimSpace(call.Name), "apply_patch") && strings.TrimSpace(call.ID) != "" {
				customCallIDs[call.ID] = true
			}
		}
		for _, item := range codexRawOutputItems(msg) {
			typeName, _ := item["type"].(string)
			name, _ := item["name"].(string)
			callID, _ := item["call_id"].(string)
			if typeName == "custom_tool_call" && strings.EqualFold(name, "apply_patch") && callID != "" {
				customCallIDs[callID] = true
			}
		}
	}

	inputMessages := make([]map[string]any, 0, len(req.Messages))
	for _, msg := range provider.PrepareMessagesForLLM(req.Messages) {
		if msg == nil {
			continue
		}
		switch msg.Role {
		case conversation.RoleSystem:
			// Codex backend validates system instructions via top-level `instructions`.
			continue

		case conversation.RoleTool:
			if len(msg.ToolResults) > 0 {
				for _, result := range msg.ToolResults {
					if strings.TrimSpace(result.CallID) == "" {
						continue
					}
					output := result.Output
					if strings.TrimSpace(output) == "" && result.Error != nil {
						output = result.Error.Message
					}
					outputType := "function_call_output"
					if strings.EqualFold(strings.TrimSpace(result.Name), "apply_patch") || customCallIDs[result.CallID] {
						outputType = "custom_tool_call_output"
					}
					inputMessages = append(inputMessages, map[string]any{
						"type":    outputType,
						"call_id": result.CallID,
						"output":  output,
					})
				}
				continue
			}

		case conversation.RoleAssistant:
			// When the turn's raw output items were captured, replay them
			// verbatim in original stream order — required with store:false
			// so each encrypted reasoning item stays immediately before its
			// paired item (the backend rejects reordered reasoning).
			if rawItems := codexRawOutputItems(msg); len(rawItems) > 0 {
				for _, item := range rawItems {
					inputMessages = append(inputMessages, item)
				}
				continue
			}
			for _, tc := range msg.ToolCalls {
				callID := strings.TrimSpace(tc.ID)
				name := strings.TrimSpace(tc.Name)
				if callID == "" || name == "" {
					continue
				}
				if strings.EqualFold(name, "apply_patch") {
					input, _ := tc.Parameters["input"].(string)
					inputMessages = append(inputMessages, map[string]any{
						"type":    "custom_tool_call",
						"call_id": callID,
						"name":    "apply_patch",
						"input":   input,
					})
					continue
				}
				argsJSON := "{}"
				if tc.Parameters != nil {
					if marshaled, err := json.Marshal(tc.Parameters); err == nil {
						argsJSON = string(marshaled)
					}
				}
				inputMessages = append(inputMessages, map[string]any{
					"type":      "function_call",
					"call_id":   callID,
					"name":      name,
					"arguments": argsJSON,
				})
			}
		}

		content := any(msg.Content)
		if vision.HasImages(msg.Metadata) {
			images, err := vision.ExtractImagesFromMetadata(msg.Metadata)
			if err != nil {
				return nil, fmt.Errorf("extract Codex message images: %w", err)
			}

			parts := make([]map[string]any, 0, len(images)+1)
			if strings.TrimSpace(msg.Content) != "" {
				parts = append(parts, map[string]any{"type": "input_text", "text": msg.Content})
			}
			for _, image := range images {
				imageURL := image.URL
				if image.Type == "base64" {
					imageURL = "data:" + image.MediaType + ";base64," + image.Data
				}
				parts = append(parts, map[string]any{
					"type":      "input_image",
					"image_url": imageURL,
				})
			}
			content = parts
		}
		if strings.TrimSpace(msg.Content) == "" && !vision.HasImages(msg.Metadata) {
			continue
		}
		inputMessages = append(inputMessages, map[string]any{
			"type":    "message",
			"role":    string(msg.Role),
			"content": content,
		})
	}

	payload := map[string]any{
		"model":       req.Model,
		"input":       inputMessages,
		"tool_choice": "auto",
		"stream":      true,
		"store":       false,
		// include reasoning.encrypted_content so reasoning items can be
		// replayed across turns despite store:false (codex-rs parity).
		"include": []string{"reasoning.encrypted_content"},
		// codex-rs sends supports_parallel_tool_calls && !lite; unknown
		// models default to false there, mirrored via requestOptions.
		"parallel_tool_calls": opts.parallelToolCalls && !opts.responsesLite,
	}

	if opts.responsesLite {
		// Responses-Lite (gpt-5.6 family): instructions stay empty and the
		// tool definitions plus system prompt move to the head of input as
		// an additional_tools item and a developer message.
		prefix := make([]map[string]any, 0, 2)
		prefix = append(prefix, map[string]any{
			"type":  "additional_tools",
			"role":  "developer",
			"tools": tools,
		})
		if strings.TrimSpace(req.SystemPrompt) != "" {
			prefix = append(prefix, map[string]any{
				"type": "message",
				"role": "developer",
				"content": []map[string]any{
					{"type": "input_text", "text": req.SystemPrompt},
				},
			})
		}
		payload["input"] = append(prefix, inputMessages...)
	} else {
		payload["instructions"] = req.SystemPrompt
		payload["tools"] = tools
	}

	reasoning := map[string]any{"summary": "auto"}
	if effort := provider.NormalizeReasoningEffort(req.ReasoningEffort); effort != "" {
		// Clamp the top effort to what the model actually accepts: "max"
		// exists only on catalog models that list it (gpt-5.6 family);
		// sending it elsewhere is a 400.
		if effort == provider.ReasoningEffortMax && !opts.supportsMaxEffort {
			effort = provider.ReasoningEffortXHigh
		}
		reasoning["effort"] = effort
	}
	if opts.responsesLite {
		reasoning["context"] = "all_turns"
	}
	if len(reasoning) > 0 {
		payload["reasoning"] = reasoning
	}

	if opts.promptCacheKey != "" {
		payload["prompt_cache_key"] = opts.promptCacheKey
	}
	if verbosity := strings.TrimSpace(req.Verbosity); verbosity != "" {
		payload["text"] = map[string]any{"verbosity": verbosity}
	}

	return payload, nil
}
