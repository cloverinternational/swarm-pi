package anthropic

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/cache/profiling"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/conversation"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/observability"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/provider"
	sdkerr "github.com/Swarm-Code/mono/swarm-sdk/internal/sdkerr"
)

// chat implements the synchronous chat completion.
func (p *Provider) chat(ctx context.Context, req provider.ChatRequest) (*provider.ChatResponse, error) {
	// Start observability span
	ctx, span := p.tracer.StartSpan(ctx, "anthropic.chat")
	defer span.End()

	// Add span attributes
	span.SetAttribute("provider", "anthropic")
	span.SetAttribute("model", req.Model)
	span.SetAttribute("message_count", len(req.Messages))

	// Log request
	historySummary := conversation.SummarizeMessages(req.Messages)
	p.logger.Info(ctx, "anthropic.chat.request",
		observability.F("model", req.Model),
		observability.F("message_count", historySummary.MessageCount),
		observability.F("nil_message_count", historySummary.NilCount),
		observability.F("role_histogram", historySummary.Roles),
		observability.F("tool_call_count", historySummary.ToolCalls),
		observability.F("tool_result_count", historySummary.ToolResults),
		observability.F("message_id_sequence_hash", historySummary.IDSequenceHash),
		observability.F("has_tools", len(req.Tools) > 0),
		observability.F("has_system_prompt", req.SystemPrompt != ""),
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
			p.logger.Debug(ctx, "anthropic.chat.oauth_prefix_added",
				observability.F("is_oauth", true),
				observability.F("prefix", oauthPrefix),
				observability.F("full_prompt_length", len(req.SystemPrompt)),
			)
		} else {
			p.logger.Debug(ctx, "anthropic.chat.oauth_prefix_already_present",
				observability.F("is_oauth", true),
				observability.F("prompt_length", len(req.SystemPrompt)),
			)
		}
	}

	// Translate request to Anthropic format (with translation cache for incremental builds)
	anthropicReq, providerJSON, err := translateRequest(ctx, req, p.config.IsOAuth, p.config.AccountID, p.logger, p.translationCache)
	if err != nil {
		err = sdkerr.Wrap(
			err,
			"provider.anthropic.request_translation_failed",
			sdkerr.WithOperation("anthropic.chat"),
			sdkerr.WithComponent("provider.anthropic"),
			sdkerr.WithTraceFromContext(ctx),
		)
		p.logger.Error(ctx, "anthropic.chat.translation_failed",
			observability.F("error", err.Error()),
		)
		return nil, err
	}

	// Store provider JSON for debugging (SDK can retrieve via GetLastProviderJSON)
	if rawJSON, err := json.Marshal(providerJSON); err == nil {
		p.lastProviderJSON = rawJSON
	}
	// IMPORTANT: For OAuth, we need to explicitly set headers here because
	// the local headers map will OVERRIDE defaultHeaders for matching keys
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
		// Must be explicitly set here to override defaultHeaders
		headers["x-app"] = "cli"

		// Set User-Agent for OAuth requests (required for proper OAuth routing)
		headers["User-Agent"] = OAuthUserAgent
	} else if len(p.config.BetaHeaders) > 0 {
		// Non-OAuth with beta headers from config
		headers["anthropic-beta"] = strings.Join(p.config.BetaHeaders, ",")
	}

	// Auto-enable beta headers based on request features
	// Note: Extended thinking uses request parameters, not beta headers
	// Note: Citations is now generally available (GA) and does NOT require a beta header
	// if anthropicReq.Citations != nil {
	//	if headers["anthropic-beta"] == "" {
	//		headers["anthropic-beta"] = BetaCitations
	//	} else {
	//		headers["anthropic-beta"] += "," + BetaCitations
	//	}
	// }

	// Log request details for OAuth debugging
	if p.config.IsOAuth {
		systemPromptPreview := req.SystemPrompt
		if len(systemPromptPreview) > 100 {
			systemPromptPreview = systemPromptPreview[:100]
		}
		p.logger.Info(ctx, "anthropic.chat.oauth_request_details",
			observability.F("url", p.config.BaseURL+"/v1/messages"),
			observability.F("model", anthropicReq.Model),
			observability.F("system_prompt_length", len(req.SystemPrompt)),
			observability.F("system_prompt_preview", systemPromptPreview),
			observability.F("has_tools", len(anthropicReq.Tools) > 0),
			observability.F("beta_headers", headers["anthropic-beta"]),
			observability.F("x_app_header", headers["x-app"]),
		)
	}

	// Managed mode: reload the centrally-rotated OAuth token from disk before
	// sending (cheap; mtime-cached). No-op unless SWARMOS_OAUTH_MANAGED is set.
	p.reloadManagedToken(ctx, false)

	// Make API request using the fluent API
	reqBuilder := p.client.BuildRequest(ctx).Method("POST").URL(p.config.BaseURL + "/v1/messages").Body(anthropicReq)

	// Add custom headers
	for k, v := range headers {
		reqBuilder = reqBuilder.Header(k, v)
	}

	// Log what headers we're adding for OAuth debugging
	if p.config.IsOAuth {
		p.logger.Info(ctx, "anthropic.chat.headers_being_added",
			observability.F("header_count", len(headers)),
			observability.F("headers", fmt.Sprintf("%+v", headers)),
		)
	}

	// Capture complete raw HTTP request for debugging (before sending)
	if rawJSON, err := json.Marshal(providerJSON); err == nil {
		p.storeRawRequest(
			"POST",
			p.config.BaseURL+"/v1/messages",
			headers,
			rawJSON,
		)
	}

	resp, err := p.client.Do(ctx, reqBuilder)
	if err != nil {
		err = sdkerr.Wrap(
			err,
			"provider.anthropic.request_failed",
			sdkerr.WithOperation("anthropic.chat"),
			sdkerr.WithComponent("provider.anthropic"),
			sdkerr.WithTraceFromContext(ctx),
		)
		p.logger.Error(ctx, "anthropic.chat.request_failed",
			observability.F("error", err.Error()),
		)
		return nil, err
	}

	// Managed mode 401 handling: do NOT refresh. Reload the token from disk once
	// (the central daemon owns refreshing) and retry. If it still 401s, return
	// the error — the turn fails and the worker requeues.
	if resp.StatusCode() == 401 && p.isManaged() {
		p.logger.Warn(ctx, "anthropic.chat.managed.token_unauthorized",
			observability.F("status_code", 401),
			observability.F("action", "reload_from_disk"),
		)
		p.reloadManagedToken(ctx, true)

		reqBuilder = p.client.BuildRequest(ctx).Method("POST").URL(p.config.BaseURL + "/v1/messages").Body(anthropicReq)
		for k, v := range headers {
			reqBuilder = reqBuilder.Header(k, v)
		}
		resp, err = p.client.Do(ctx, reqBuilder)
		if err != nil {
			err = sdkerr.Wrap(
				err,
				"provider.anthropic.retry_request_failed",
				sdkerr.WithOperation("anthropic.chat"),
				sdkerr.WithComponent("provider.anthropic"),
				sdkerr.WithTraceFromContext(ctx),
			)
			p.logger.Error(ctx, "anthropic.chat.managed.retry_failed",
				observability.F("error", err.Error()),
			)
			return nil, err
		}
		if resp.StatusCode() == 401 {
			p.logger.Error(ctx, "anthropic.chat.managed.retry_still_unauthorized",
				observability.F("status_code", 401),
			)
			// Fall through to error parsing below; central daemon will refresh
			// before the next requeued attempt.
		} else {
			p.logger.Info(ctx, "anthropic.chat.managed.retry_successful",
				observability.F("status_code", resp.StatusCode()),
			)
		}
	} else if resp.StatusCode() == 401 && p.config.IsOAuth {
		// Default (self-refresh) path — unchanged.
		p.logger.Warn(ctx, "anthropic.chat.oauth_token_expired",
			observability.F("status_code", 401),
			observability.F("attempting_refresh", true),
		)

		// Attempt to refresh the token
		if refreshErr := p.refreshOAuthToken(ctx); refreshErr != nil {
			p.logger.Error(ctx, "anthropic.chat.token_refresh_failed",
				observability.F("error", refreshErr.Error()),
			)
			// Continue to parse error - let it fail with proper error message
		} else {
			// Retry the request with the new token
			p.logger.Info(ctx, "anthropic.chat.retrying_with_new_token")

			// Rebuild the request with same parameters
			reqBuilder = p.client.BuildRequest(ctx).Method("POST").URL(p.config.BaseURL + "/v1/messages").Body(anthropicReq)
			for k, v := range headers {
				reqBuilder = reqBuilder.Header(k, v)
			}

			resp, err = p.client.Do(ctx, reqBuilder)
			if err != nil {
				err = sdkerr.Wrap(
					err,
					"provider.anthropic.retry_request_failed",
					sdkerr.WithOperation("anthropic.chat"),
					sdkerr.WithComponent("provider.anthropic"),
					sdkerr.WithTraceFromContext(ctx),
				)
				p.logger.Error(ctx, "anthropic.chat.retry_failed",
					observability.F("error", err.Error()),
				)
				return nil, err
			}

			// Check if retry also failed with 401
			if resp.StatusCode() == 401 {
				p.logger.Error(ctx, "anthropic.chat.retry_still_unauthorized",
					observability.F("status_code", 401),
				)
				// Let it parse the error response below
			} else {
				p.logger.Info(ctx, "anthropic.chat.retry_successful",
					observability.F("status_code", resp.StatusCode()),
				)
			}
		}
	}

	// Check for HTTP error status codes before attempting to parse JSON.
	// A 4xx/5xx response body is an error object, not a MessageResponse.
	if resp.IsError() || resp.StatusCode() >= 400 {
		rawResp := resp.RawResponse()
		if rawResp != nil {
			categorizedErr := categorizeError(nil, rawResp)

			// Attempt auto-retry when input + max_tokens exceeds context limit.
			if retryReq, ok := p.tryClampMaxTokens(ctx, categorizedErr, anthropicReq); ok {
				p.logger.Warn(ctx, "anthropic.chat.context_limit_retry",
					observability.F("original_max_tokens", retryReq.originalMaxTokens),
					observability.F("clamped_max_tokens", retryReq.clampedMaxTokens),
					observability.F("input_length", retryReq.inputLength),
					observability.F("context_size", retryReq.contextSize),
				)

				// Rebuild and retry with clamped max_tokens
				reqBuilder = p.client.BuildRequest(ctx).Method("POST").URL(p.config.BaseURL + "/v1/messages").Body(anthropicReq)
				for k, v := range headers {
					reqBuilder = reqBuilder.Header(k, v)
				}
				resp, err = p.client.Do(ctx, reqBuilder)
				if err != nil {
					err = sdkerr.Wrap(
						err,
						"provider.anthropic.retry_request_failed",
						sdkerr.WithOperation("anthropic.chat"),
						sdkerr.WithComponent("provider.anthropic"),
						sdkerr.WithTraceFromContext(ctx),
					)
					p.logger.Error(ctx, "anthropic.chat.retry_failed",
						observability.F("error", err.Error()),
					)
					return nil, err
				}
				// If retry also returned an error, return the original categorized error
				if resp.IsError() || resp.StatusCode() >= 400 {
					err = sdkerr.Wrap(
						categorizedErr,
						"provider.anthropic.request_failed",
						sdkerr.WithOperation("anthropic.chat"),
						sdkerr.WithComponent("provider.anthropic"),
						sdkerr.WithTraceFromContext(ctx),
					)
					p.logger.Error(ctx, "anthropic.chat.context_limit_retry_failed",
						observability.F("status_code", resp.StatusCode()),
					)
					return nil, err
				}
				// Retry succeeded — fall through to parse the response
				p.logger.Info(ctx, "anthropic.chat.context_limit_retry_success",
					observability.F("status_code", resp.StatusCode()),
				)
			} else {
				err = sdkerr.Wrap(
					categorizedErr,
					"provider.anthropic.request_failed",
					sdkerr.WithOperation("anthropic.chat"),
					sdkerr.WithComponent("provider.anthropic"),
					sdkerr.WithTraceFromContext(ctx),
				)
				p.logger.Error(ctx, "anthropic.chat.request_failed",
					observability.F("status_code", resp.StatusCode()),
					observability.F("error", err.Error()),
				)
				return nil, err
			}
		} else {
			err = sdkerr.Permanent(
				"anthropic.request_failed",
				fmt.Sprintf("HTTP %d error without response body", resp.StatusCode()),
				sdkerr.WithOperation("anthropic.chat"),
				sdkerr.WithComponent("provider.anthropic"),
				sdkerr.WithTraceFromContext(ctx),
			)
			p.logger.Error(ctx, "anthropic.chat.request_failed",
				observability.F("status_code", resp.StatusCode()),
			)
			return nil, err
		}
	}

	// Parse response
	var anthropicResp MessageResponse
	if err := resp.JSON(&anthropicResp); err != nil {
		err = sdkerr.Wrap(
			err,
			"provider.anthropic.response_parse_failed",
			sdkerr.WithOperation("anthropic.chat"),
			sdkerr.WithComponent("provider.anthropic"),
			sdkerr.WithTraceFromContext(ctx),
		)
		p.logger.Error(ctx, "anthropic.chat.parse_failed",
			observability.F("error", err.Error()),
		)
		return nil, err
	}

	// Capture complete raw HTTP response for debugging
	if rawRespBody, err := resp.Body(); err == nil {
		p.storeRawResponse(
			resp.StatusCode(),
			map[string]string{}, // Headers not directly accessible from httplib.Response
			rawRespBody,         // Complete raw response body
		)
	}

	// Emit one bounded response-content event.
	p.logger.Info(ctx, "anthropic.chat.response_content_blocks",
		observability.F("block_count", len(anthropicResp.Content)),
	)

	// Log token parsing from JSON
	p.logger.Debug(ctx, "anthropic.chat.parsing_tokens",
		observability.F("input_tokens", anthropicResp.Usage.InputTokens),
		observability.F("output_tokens", anthropicResp.Usage.OutputTokens),
	)

	// Translate response to canonical format
	chatResp, err := translateResponse(&anthropicResp, p.config.IsOAuth)
	if err != nil {
		err = sdkerr.Wrap(
			err,
			"provider.anthropic.response_translation_failed",
			sdkerr.WithOperation("anthropic.chat"),
			sdkerr.WithComponent("provider.anthropic"),
			sdkerr.WithTraceFromContext(ctx),
		)
		p.logger.Error(ctx, "anthropic.chat.response_translation_failed",
			observability.F("error", err.Error()),
		)
		return nil, err
	}

	// Log success
	p.logger.Info(ctx, "anthropic.chat.success",
		observability.F("model", anthropicResp.Model),
		observability.F("finish_reason", anthropicResp.StopReason),
		observability.F("input_tokens", anthropicResp.Usage.InputTokens),
		observability.F("output_tokens", anthropicResp.Usage.OutputTokens),
		observability.F("has_tool_calls", len(chatResp.Message.ToolCalls) > 0),
	)

	// Record cache metrics with profiler for cache break analysis
	conversationID := ""
	if ctxConvID, ok := ctx.Value("conversation_id").(string); ok {
		conversationID = ctxConvID
	}
	profiler := profiling.GetProfiler(conversationID)

	cacheCreationTokens := int64(0)
	if anthropicResp.Usage.CacheCreationInputTokens != nil {
		cacheCreationTokens = int64(*anthropicResp.Usage.CacheCreationInputTokens)
	}

	cacheReadTokens := int64(0)
	cacheHitDetected := false
	if anthropicResp.Usage.CacheReadInputTokens != nil {
		cacheReadTokens = int64(*anthropicResp.Usage.CacheReadInputTokens)
		cacheHitDetected = cacheReadTokens > 0
	}

	cacheMetrics := &profiling.APIMetrics{
		Timestamp:           time.Now(),
		Provider:            "anthropic",
		Model:               anthropicResp.Model,
		RequestTokens:       int64(anthropicResp.Usage.InputTokens),
		OutputTokens:        int64(anthropicResp.Usage.OutputTokens),
		CacheCreationTokens: cacheCreationTokens,
		CacheReadTokens:     cacheReadTokens,
		CacheHitDetected:    cacheHitDetected,
		MessageCount:        len(req.Messages),
		SystemPromptLength:  len(req.SystemPrompt),
		ResponseTimeMS:      0, // TODO: track from request start
	}
	_ = profiler.RecordAPIResponse(ctx, cacheMetrics)

	return chatResp, nil
}

// categorizeError converts HTTP errors to categorized SDK errors.
func categorizeError(err error, resp *http.Response) error {
	if resp == nil {
		// Network error (no response)
		msg := "network error"
		if err != nil {
			msg = err.Error()
		}
		return sdkerr.Transient("anthropic.network_error", msg,
			sdkerr.WithRetryAfter(5*time.Second),
		)
	}

	// Parse error response if available
	var apiErr APIError
	if resp.Body != nil {
		decoder := json.NewDecoder(resp.Body)
		if decodeErr := decoder.Decode(&apiErr); decodeErr == nil {
			return categorizeAPIError(&apiErr, resp.StatusCode)
		}
	}

	// Categorize by status code
	switch resp.StatusCode {
	case http.StatusTooManyRequests: // 429
		errMsg := "rate limit exceeded"
		if err != nil {
			errMsg = err.Error()
		}
		return sdkerr.Transient("anthropic.rate_limited", errMsg,
			sdkerr.WithRetryAfter(60*time.Second),
		)

	case http.StatusUnauthorized: // 401
		return sdkerr.Permanent(
			"anthropic.authentication_failed",
			"invalid API key",
		)

	case http.StatusForbidden: // 403
		return sdkerr.Permanent(
			"anthropic.forbidden",
			"access denied (check API key permissions)",
		)

	case http.StatusBadRequest: // 400
		errMsg := "bad request"
		if err != nil {
			errMsg = err.Error()
		}
		return sdkerr.Permanent(
			"anthropic.invalid_request",
			errMsg,
		)

	case http.StatusInternalServerError, // 500
		http.StatusBadGateway,         // 502
		http.StatusServiceUnavailable, // 503
		http.StatusGatewayTimeout:     // 504
		errMsg := "server error"
		if err != nil {
			errMsg = err.Error()
		}
		return sdkerr.Transient("anthropic.server_error", errMsg,
			sdkerr.WithRetryAfter(30*time.Second),
		)

	default:
		return sdkerr.Permanent(
			"anthropic.unknown_error",
			fmt.Sprintf("unexpected status code: %d", resp.StatusCode),
		)
	}
}

// categorizeAPIError categorizes Anthropic API errors.
func categorizeAPIError(apiErr *APIError, statusCode int) error {
	// Log the actual API error for debugging
	// Note: This is logged at the error categorization layer, not via observability logger
	// because we don't have access to ctx/logger here. The error message will be in the returned error.

	switch apiErr.Error.Type {
	case "invalid_request_error":
		return sdkerr.Permanent(
			"anthropic.invalid_request",
			fmt.Sprintf("%s (type: %s)", apiErr.Error.Message, apiErr.Error.Type),
		)

	case "authentication_error":
		return sdkerr.Permanent(
			"anthropic.authentication_failed",
			withErrorType(apiErr),
		)

	case "permission_error":
		return sdkerr.Permanent(
			"anthropic.permission_denied",
			withErrorType(apiErr),
		)

	case "not_found_error":
		return sdkerr.Permanent(
			"anthropic.not_found",
			withErrorType(apiErr),
		)

	case "rate_limit_error":
		return sdkerr.Transient("anthropic.rate_limited", withErrorType(apiErr),
			sdkerr.WithRetryAfter(60*time.Second),
		)

	case "api_error":
		return sdkerr.Transient("anthropic.api_error", withErrorType(apiErr),
			sdkerr.WithRetryAfter(30*time.Second),
		)

	case "overloaded_error":
		return sdkerr.Transient("anthropic.overloaded", withErrorType(apiErr),
			sdkerr.WithRetryAfter(60*time.Second),
		)

	default:
		return sdkerr.Permanent(
			"anthropic.unknown_error",
			withErrorType(apiErr),
		)
	}
}

// withErrorType formats an Anthropic API error message with its type appended,
// e.g. `model: claude-opus-4-x (type: not_found_error)`. Anthropic returns bare
// messages like "model: <name>" for a 404; without the type these are hard to
// diagnose (they look like a truncated log line rather than a not-found error).
// Mirrors the invalid_request_error case, which already includes the type.
func withErrorType(apiErr *APIError) string {
	if apiErr.Error.Type == "" {
		return apiErr.Error.Message
	}
	return fmt.Sprintf("%s (type: %s)", apiErr.Error.Message, apiErr.Error.Type)
}
