package anthropic

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math"
	"regexp"
	"slices"
	"strings"

	"github.com/google/uuid"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/cache/profiling"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/conversation"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/envelope"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/observability"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/provider"
	sdkerr "github.com/Swarm-Code/mono/swarm-sdk/internal/sdkerr"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/systemprompt"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/tools"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/vision"
)

// validToolUseID matches the Anthropic API constraint for tool_use block IDs.
// IDs must contain only alphanumeric characters, underscores, and hyphens.
var validToolUseID = regexp.MustCompile(`^[a-zA-Z0-9_-]+$`)

const defaultMaxTokens = 4096

// normalizeManualThinkingTokens enforces Anthropic's requirement that a manual
// thinking budget be strictly less than max_tokens. An omitted max_tokens may
// grow to accommodate the requested budget; an explicit limit remains a hard
// caller constraint and is rejected when incompatible.
func normalizeManualThinkingTokens(maxTokens int, maxTokensExplicit bool, thinkingBudget int) (int, error) {
	if maxTokens > thinkingBudget {
		return maxTokens, nil
	}
	if maxTokensExplicit {
		return 0, sdkerr.Permanent(
			"anthropic.invalid_thinking_budget",
			fmt.Sprintf("thinking budget_tokens (%d) must be less than explicit max_tokens (%d)", thinkingBudget, maxTokens),
		)
	}
	if thinkingBudget == math.MaxInt {
		return 0, sdkerr.Permanent(
			"anthropic.invalid_thinking_budget",
			"thinking budget_tokens is too large to derive max_tokens",
		)
	}
	return thinkingBudget + 1, nil
}

// Helper function for debug logging
func getMetadataKeys(metadata map[string]any) []string {
	keys := make([]string, 0, len(metadata))
	for k := range metadata {
		keys = append(keys, k)
	}
	return keys
}

func extractSystemCacheControl(ctx context.Context, metadata map[string]any, logger observability.Logger) *CacheControl {
	if metadata == nil {
		return nil
	}

	if logger != nil {
		logger.Info(ctx, "anthropic.translate.metadata_check",
			observability.F("metadata_keys", getMetadataKeys(metadata)),
			observability.F("has_system_cache_control", metadata["system_cache_control"] != nil),
		)
	}

	if cc, ok := metadata["system_cache_control"].(map[string]string); ok {
		cacheControl := &CacheControl{
			Type: cc["type"],
			TTL:  cc["ttl"],
		}
		if logger != nil {
			logger.Info(ctx, "anthropic.translate.cache_control_applied",
				observability.F("type", cc["type"]),
				observability.F("ttl", cc["ttl"]),
			)
		}
		return cacheControl
	}

	return nil
}

func appendSystemBlocks(blocks []SystemBlock, basePrompt, cachedContext, dynamicContext string, cacheControl *CacheControl) []SystemBlock {
	if strings.TrimSpace(basePrompt) != "" {
		baseBlock := SystemBlock{
			Type: "text",
			Text: basePrompt,
		}
		if cacheControl != nil {
			baseBlock.CacheControl = cacheControl
		}
		blocks = append(blocks, baseBlock)
	}

	if strings.TrimSpace(cachedContext) != "" {
		cachedBlock := SystemBlock{
			Type: "text",
			Text: cachedContext,
		}
		if cacheControl != nil {
			cachedBlock.CacheControl = cacheControl
		}
		blocks = append(blocks, cachedBlock)
	}

	if strings.TrimSpace(dynamicContext) != "" {
		blocks = append(blocks, SystemBlock{
			Type: "text",
			Text: dynamicContext,
		})
	}

	return blocks
}

// translateRequest converts a canonical ChatRequest to an Anthropic MessageRequest.
// isOAuth indicates if this is an OAuth request (for tool name prefixing).
// cache is an optional TranslationCache for incremental message translation.
// Returns the struct, a JSON map representation for debugging, and any error.
func translateRequest(ctx context.Context, req provider.ChatRequest, isOAuth bool, accountID string, logger observability.Logger, cache *TranslationCache) (*MessageRequest, map[string]any, error) {
	if logger == nil {
		logger = observability.NewNopLogger()
	}

	// Get cache profiler for this conversation (from context or use global registry)
	var conversationID string
	if ctxConvID, ok := ctx.Value("conversation_id").(string); ok {
		conversationID = ctxConvID
	}
	profiler := profiling.GetProfiler(conversationID)

	preparedMessages := provider.PrepareMessagesForLLM(req.Messages)

	// Record message state before translation for cache validation
	preTranslationHash, _ := profiler.BeforeTranslation(ctx, preparedMessages)

	anthropicReq := &MessageRequest{
		Model:     req.Model,
		MaxTokens: defaultMaxTokens,
	}

	// Set max tokens if specified
	if req.MaxTokens != nil {
		anthropicReq.MaxTokens = *req.MaxTokens
	}

	// Set temperature if specified (Anthropic range: 0.0-1.0)
	// Opus 4.7 rejects any non-default sampling param with HTTP 400 — silently drop and warn.
	// Older Claude models and other providers are unaffected.
	if req.Temperature != nil {
		temp := *req.Temperature
		if temp < 0.0 || temp > 1.0 {
			return nil, nil, sdkerr.Permanent(
				"anthropic.invalid_temperature",
				fmt.Sprintf("temperature must be between 0.0 and 1.0, got %.2f", temp),
			)
		}
		if RejectsSamplingParams(req.Model) {
			logger.Warn(ctx, "anthropic.sampling.dropped",
				observability.F("model", req.Model),
				observability.F("param", "temperature"),
				observability.F("value", temp),
				observability.F("reason", "model rejects non-default sampling params"),
			)
		} else {
			anthropicReq.Temperature = &temp
		}
	}

	if req.TopP != nil {
		topP := *req.TopP
		if topP < 0.0 || topP > 1.0 {
			return nil, nil, sdkerr.Permanent(
				"anthropic.invalid_top_p",
				fmt.Sprintf("top_p must be between 0.0 and 1.0, got %.2f", topP),
			)
		}
		if RejectsSamplingParams(req.Model) {
			logger.Warn(ctx, "anthropic.sampling.dropped",
				observability.F("model", req.Model),
				observability.F("param", "top_p"),
				observability.F("value", topP),
				observability.F("reason", "model rejects non-default sampling params"),
			)
		} else {
			anthropicReq.TopP = &topP
		}
	}

	if req.TopK != nil {
		topK := *req.TopK
		if topK < 0 {
			return nil, nil, sdkerr.Permanent(
				"anthropic.invalid_top_k",
				fmt.Sprintf("top_k must be nonnegative, got %d", topK),
			)
		}
		if RejectsSamplingParams(req.Model) {
			logger.Warn(ctx, "anthropic.sampling.dropped",
				observability.F("model", req.Model),
				observability.F("param", "top_k"),
				observability.F("value", topK),
				observability.F("reason", "model rejects non-default sampling params"),
			)
		} else {
			anthropicReq.TopK = &topK
		}
	}

	// Set stop sequences
	if len(req.StopSequences) > 0 {
		anthropicReq.StopSequences = req.StopSequences
	}

	// Translate system prompt with optional cache control
	// CRITICAL OAUTH INVARIANT: The Claude OAuth gateway rejects (with a
	// misleading HTTP 429) any request whose system prompt's FIRST text block is
	// not EXACTLY GetCLISystemPromptPrefix(). The upstream chat.go/stream.go
	// concatenate "<prefix>\n\n<rest>" into req.SystemPrompt purely as a
	// transport convention; here we MUST split that back apart so the identity
	// prefix lands in its OWN standalone first block and never gets concatenated
	// with any other system text. This matches the Claude Code approach:
	// Block 1: "You are Claude Code, Anthropic's official CLI for Claude." (exact, standalone)
	// Block 2+: Everything else (base/cached/dynamic, never merged into block 1)
	// See TestTranslateRequest_OAuthIdentityBlockStandaloneExact for the guard.
	if req.SystemPrompt != "" {
		// OAuth prefix that must be in its own block for OAuth tokens
		oauthPrefix := GetCLISystemPromptPrefix()
		cacheControl := extractSystemCacheControl(ctx, req.Metadata, logger)

		// Check if system prompt starts with OAuth prefix (added by chat.go for OAuth requests)
		if after, ok := strings.CutPrefix(req.SystemPrompt, oauthPrefix); ok {
			// Split into 2 blocks like SwarmCode does
			// Block 1: Just the OAuth prefix
			// Block 2: Everything after the prefix
			restOfPrompt := after
			restOfPrompt = strings.TrimPrefix(restOfPrompt, "\n\n") // Remove the separator added in chat.go
			restOfPrompt = strings.TrimSpace(restOfPrompt)

			systemBlocks := []SystemBlock{
				{
					Type: "text",
					Text: oauthPrefix,
				},
			}

			// Only add additional blocks if there's content after the prefix
			if restOfPrompt != "" {
				basePrompt, cachedContext, dynamicContext := systemprompt.SplitSystemPrompt(restOfPrompt)
				systemBlocks = appendSystemBlocks(systemBlocks, basePrompt, cachedContext, dynamicContext, cacheControl)
			}
			anthropicReq.System = systemBlocks
		} else {
			// Non-OAuth path: base + cached + dynamic in separate blocks; cache control on base + cached.
			basePrompt, cachedContext, dynamicContext := systemprompt.SplitSystemPrompt(req.SystemPrompt)
			systemBlocks := appendSystemBlocks(nil, basePrompt, cachedContext, dynamicContext, cacheControl)
			if len(systemBlocks) > 0 {
				anthropicReq.System = systemBlocks
			}
		}
	}

	// Check if thinking is enabled
	var thinkingEnabled bool
	if req.Metadata != nil {
		if enabled, ok := req.Metadata["thinking_enabled"].(bool); ok {
			thinkingEnabled = enabled
		}
	}

	// Translate messages — use incremental translation when cache is available
	var messages []Message
	var err error
	if cache != nil {
		messages, err = translateMessagesIncremental(ctx, preparedMessages, thinkingEnabled, isOAuth, logger, cache)
	} else {
		messages, err = translateMessages(ctx, preparedMessages, thinkingEnabled, isOAuth, logger)
	}
	if err != nil {
		return nil, nil, err
	}

	// Apply message-level cache control to the last user message if requested.
	// CRITICAL: Apply to a COPY of the messages/blocks so the cached originals
	// are never mutated. This prevents cache_control markers from appearing in
	// the cached prefix, which would cause byte instability across turns.
	if req.Metadata != nil {
		if cacheControl, ok := req.Metadata["message_cache_control"].(map[string]string); ok && len(messages) > 0 {
			// Shallow copy the message slice so mutations don't affect the cache
			messagesCopy := make([]Message, len(messages))
			copy(messagesCopy, messages)
			messages = messagesCopy

			// Find the last user message
			for i := len(messages) - 1; i >= 0; i-- {
				if messages[i].Role == "user" {
					cc := &CacheControl{
						Type: cacheControl["type"], // "ephemeral"
						TTL:  cacheControl["ttl"],  // "5m" or "1h"
					}

					// Content is always []ContentBlock after translateMessages()
					if blocks, ok := messages[i].Content.([]ContentBlock); ok && len(blocks) > 0 {
						// Copy the blocks slice to avoid mutating the cached version
						blocksCopy := make([]ContentBlock, len(blocks))
						copy(blocksCopy, blocks)
						blocksCopy[len(blocksCopy)-1].CacheControl = cc
						messages[i].Content = blocksCopy
					}
					break
				}
			}
		}
	}

	anthropicReq.Messages = messages

	// Translate tools
	if len(req.Tools) > 0 {
		// Check for tool cache control in request metadata
		var toolCacheControl *CacheControl
		if req.Metadata != nil {
			if cacheControl, ok := req.Metadata["tool_cache_control"].(map[string]string); ok {
				toolCacheControl = &CacheControl{
					Type: cacheControl["type"], // "ephemeral"
					TTL:  cacheControl["ttl"],  // "5m" or "1h" (optional)
				}
			}
		}

		// Apply OAuth tool prefixing if needed
		toolsToTranslate := req.Tools
		if isOAuth {
			toolsToTranslate = TransformToolsForOAuth(req.Tools)
			logger.Debug(ctx, "anthropic.translate.oauth_tools_prefixed",
				observability.F("tool_count", len(req.Tools)),
			)
		}

		tools, err := translateTools(toolsToTranslate, toolCacheControl)
		if err != nil {
			return nil, nil, err
		}
		anthropicReq.Tools = tools
	}

	// Extended thinking configuration
	if req.Metadata != nil {
		if thinkingEnabled, ok := req.Metadata["thinking_enabled"].(bool); ok && thinkingEnabled {
			// Intelligently select thinking mode based on model version
			// - Opus 4.6+: Use adaptive thinking (recommended)
			// - Older models: Use manual mode with budget_tokens
			if SupportsAdaptiveThinking(req.Model) {
				// Adaptive thinking mode for Opus 4.6+
				anthropicReq.Thinking = &ThinkingConfig{
					Type: "adaptive",
				}
				if RequiresSummarizedThinking(req.Model) {
					anthropicReq.Thinking.Display = "summarized"
				}

				logger.Info(ctx, "anthropic.thinking.adaptive_mode",
					observability.F("model", req.Model),
					observability.F("mode", "adaptive"),
				)
			} else {
				// Manual mode with budget for older models
				thinkingBudget := 2048 // Default budget
				if budget, ok := req.Metadata["thinking_budget"].(int); ok && budget >= 1024 {
					thinkingBudget = budget
				}
				maxTokens, err := normalizeManualThinkingTokens(anthropicReq.MaxTokens, req.MaxTokens != nil, thinkingBudget)
				if err != nil {
					return nil, nil, err
				}
				anthropicReq.MaxTokens = maxTokens

				anthropicReq.Thinking = &ThinkingConfig{
					Type:         "enabled",
					BudgetTokens: thinkingBudget,
				}

				logger.Info(ctx, "anthropic.thinking.manual_mode",
					observability.F("model", req.Model),
					observability.F("budget", thinkingBudget),
				)
			}
		} else {
			// Debug logging
			logger.Info(ctx, "anthropic.thinking.not_enabled",
				observability.F("metadata_keys", getMetadataKeys(req.Metadata)),
			)
		}

		// Effort parameter (for Opus 4.6+ with adaptive thinking)
		// Valid values: "low", "medium", "high" (default), "max" (Opus 4.6+ only)
		if effortStr, ok := req.Metadata["thinking_effort"].(string); ok && effortStr != "" {
			// Normalize effort value
			effort := strings.ToLower(strings.TrimSpace(effortStr))

			// Validate effort level
			validEfforts := map[string]bool{
				"low":    true,
				"medium": true,
				"high":   true,
				"max":    SupportsMaxEffort(req.Model),   // Only Opus 4.6+ supports "max"
				"xhigh":  SupportsXHighEffort(req.Model), // Only Opus 4.7+ supports "xhigh"
			}

			if validEfforts[effort] {
				anthropicReq.OutputConfig = &OutputConfig{
					Effort: effort,
				}

				logger.Info(ctx, "anthropic.effort.configured",
					observability.F("effort", effort),
					observability.F("model", req.Model),
				)
			} else {
				// Invalid effort level - log warning but don't fail
				logger.Warn(ctx, "anthropic.effort.invalid",
					observability.F("effort", effortStr),
					observability.F("model", req.Model),
					observability.F("supported_max", SupportsMaxEffort(req.Model)),
				)
			}
		}

		// Prompt caching
		if cachePoints, ok := req.Metadata["cache_control"].([]any); ok && len(cachePoints) > 0 {
			// Handle cache control (implemented in system prompt or messages)
			// This is applied during message translation
		}

		// Citations configuration
		if citationsEnabled, ok := req.Metadata["citations_enabled"].(bool); ok && citationsEnabled {
			anthropicReq.Citations = &CitationsConfig{
				Enabled: true,
			}
		}
	} else {
		logger.Info(ctx, "anthropic.thinking.no_metadata")
	}

	// Add metadata with device identity for OAuth requests.
	// Claude Code sends: user_<device_id>_account_<account_uuid>_session_<session_id>
	// This field is present in EVERY OAuth request — its absence is detectable.
	// When multiple accounts are active, each uses its own stable identity so requests
	// from different accounts are not correlated via a shared device_id.
	if isOAuth {
		var deviceIdentity *DeviceIdentity
		var err error

		if accountID != "" {
			// Multi-account path: use the identity bound to this specific account.
			deviceIdentity, err = GetOrCreateDeviceIdentityForAccount(accountID)
		} else {
			// Single-account path: use the global identity, auto-creating if needed.
			deviceIdentity, err = GetCurrentDeviceIdentity()
			if err != nil || deviceIdentity == nil {
				deviceIdentity, err = GetOrCreateDeviceIdentity("")
			}
		}

		if err == nil && deviceIdentity != nil {
			userID := deviceIdentity.FormatUserID(GetOrCreateSessionID())
			anthropicReq.Metadata = &Metadata{
				UserID: userID,
			}
			logger.Debug(ctx, "anthropic.oauth.device_identity",
				observability.F("device_id", deviceIdentity.DeviceID[:16]+"..."),
				observability.F("has_account_id", deviceIdentity.AccountID != ""),
				observability.F("user_id_length", len(userID)),
			)
		} else {
			logger.Warn(ctx, "anthropic.oauth.no_device_identity",
				observability.F("error", err),
			)
		}
	}

	// Build provider JSON map for debugging (reflects exact API payload structure)
	providerJSON := map[string]any{
		"model":      anthropicReq.Model,
		"max_tokens": anthropicReq.MaxTokens,
		"messages":   anthropicReq.Messages,
	}
	if anthropicReq.Temperature != nil {
		providerJSON["temperature"] = *anthropicReq.Temperature
	}
	if anthropicReq.TopP != nil {
		providerJSON["top_p"] = *anthropicReq.TopP
	}
	if anthropicReq.TopK != nil {
		providerJSON["top_k"] = *anthropicReq.TopK
	}
	if anthropicReq.System != nil {
		providerJSON["system"] = anthropicReq.System
	}
	if len(anthropicReq.Tools) > 0 {
		providerJSON["tools"] = anthropicReq.Tools
	}
	if anthropicReq.Thinking != nil {
		providerJSON["thinking"] = anthropicReq.Thinking
	}
	if anthropicReq.OutputConfig != nil {
		providerJSON["output_config"] = anthropicReq.OutputConfig
	}
	if anthropicReq.StopSequences != nil {
		providerJSON["stop_sequences"] = anthropicReq.StopSequences
	}
	if anthropicReq.Citations != nil {
		providerJSON["citations"] = anthropicReq.Citations
	}
	if anthropicReq.Metadata != nil {
		providerJSON["metadata"] = anthropicReq.Metadata
	}

	// Validate translation stability with cache profiler
	// This detects if the translated JSON has changed, which would break cache
	_ = profiler.AfterTranslation(ctx, preTranslationHash, providerJSON)

	return anthropicReq, providerJSON, nil
}

// deduplicateToolUseIDs scans a slice of messages for duplicate tool_use IDs and
// IDs that violate Anthropic's required pattern (^[a-zA-Z0-9_-]+$), then returns
// a shallow-cloned slice where every tool_use block has a globally unique, valid ID.
//
// This is required for cross-provider conversations: Gemini does not assign per-call
// IDs, so it historically used the tool name as the ID. When a Gemini-produced turn
// is later replayed to the Anthropic API (e.g. after a rate-limit fallback back to
// Anthropic), any tool called more than once in the same assistant turn will have
// duplicate IDs, triggering "tool_use ids must be unique" (HTTP 400). Additionally,
// tool names used as IDs may contain characters outside [a-zA-Z0-9_-], triggering
// "String should match pattern" validation errors.
//
// The function works on copies of the ToolCalls/ToolResults slices so the original
// conversation.Message objects are never mutated.
func deduplicateToolUseIDs(ctx context.Context, messages []*conversation.Message, logger observability.Logger) []*conversation.Message {
	// First pass: detect whether any IDs need remapping (duplicates or invalid chars).
	seenIDs := make(map[string]bool)
	needsRemap := false
	for _, msg := range messages {
		for _, tc := range msg.ToolCalls {
			if seenIDs[tc.ID] || !validToolUseID.MatchString(tc.ID) {
				needsRemap = true
				break
			}
			seenIDs[tc.ID] = true
		}
		if needsRemap {
			break
		}
	}
	if !needsRemap {
		return messages
	}

	logger.Warn(ctx, "translate.messages.dedup_tool_use_ids",
		observability.F("reason", "duplicate or invalid tool_use IDs detected; remapping to unique valid IDs"),
	)

	// Second pass: rebuild messages with remapped IDs where needed.
	// idRemap maps old_id -> new_id for IDs that were duplicates/invalid and got reassigned.
	idRemap := make(map[string]string)
	seenIDs = make(map[string]bool)

	result := make([]*conversation.Message, len(messages))
	for i, msg := range messages {
		if len(msg.ToolCalls) == 0 && len(msg.ToolResults) == 0 {
			result[i] = msg
			continue
		}

		// Clone ToolCalls with deduped/sanitized IDs.
		var newToolCalls []conversation.ToolCall
		if len(msg.ToolCalls) > 0 {
			newToolCalls = make([]conversation.ToolCall, len(msg.ToolCalls))
			copy(newToolCalls, msg.ToolCalls)
			for j, tc := range newToolCalls {
				if seenIDs[tc.ID] || !validToolUseID.MatchString(tc.ID) {
					// Duplicate or invalid: generate a fresh unique ID and record
					// the remap so the paired tool_result can be fixed too.
					newID := "toolu_" + strings.ReplaceAll(uuid.New().String(), "-", "")[:24]
					logger.Warn(ctx, "translate.messages.remap_tool_use_id",
						observability.F("msg_index", i),
						observability.F("tool_name", tc.Name),
						observability.F("old_id", tc.ID),
						observability.F("new_id", newID),
					)
					idRemap[tc.ID] = newID
					newToolCalls[j].ID = newID
					seenIDs[newID] = true
				} else {
					seenIDs[tc.ID] = true
				}
			}
		}

		// Clone ToolResults, fixing CallIDs that reference a remapped tool_use ID.
		var newToolResults []conversation.ToolResult
		if len(msg.ToolResults) > 0 {
			newToolResults = make([]conversation.ToolResult, len(msg.ToolResults))
			copy(newToolResults, msg.ToolResults)
			for j, tr := range newToolResults {
				if newID, remapped := idRemap[tr.CallID]; remapped {
					newToolResults[j].CallID = newID
				}
			}
		}

		// Shallow-clone the message, replacing only the tool slices.
		cloned := *msg
		if newToolCalls != nil {
			cloned.ToolCalls = newToolCalls
		}
		if newToolResults != nil {
			cloned.ToolResults = newToolResults
		}
		result[i] = &cloned
	}

	return result
}

// translateMessages converts canonical messages to Anthropic message format.
// When thinkingEnabled is true, the last assistant message must include its thinking block.
// When isOAuth is true, tool names in message history are prefixed for OAuth routing.
// The first translated message must be a user message (enforced by Anthropic's API).
func translateMessages(ctx context.Context, messages []*conversation.Message, thinkingEnabled bool, isOAuth bool, logger observability.Logger) ([]Message, error) {
	return translateMessagesCore(ctx, messages, thinkingEnabled, isOAuth, logger, true)
}

// translateMessagesCore is the internal implementation of translateMessages.
// requireUserFirst controls whether the Anthropic "first message must be from user"
// invariant is enforced. Pass false when translating a mid-conversation sub-slice
// (e.g. the incremental-translation path) where the caller is responsible for
// ensuring the full combined message list is valid.
func translateMessagesCore(ctx context.Context, messages []*conversation.Message, thinkingEnabled bool, isOAuth bool, logger observability.Logger, requireUserFirst bool) ([]Message, error) {
	if logger == nil {
		logger = observability.NewNopLogger()
	}

	historySummary := conversation.SummarizeMessages(messages)

	// CROSS-PROVIDER SAFETY: Deduplicate tool_use IDs before translation.
	// When the fallback chain switches providers (e.g. Anthropic → Gemini → back to
	// Anthropic), Gemini-generated turns in the history may have tool_use IDs that
	// equal the tool name (e.g. "Bash", "Bash") because the Gemini API does not
	// provide per-call IDs.  Anthropic rejects any request where tool_use IDs are
	// not unique across the entire message array.  This call remaps duplicates to
	// fresh UUID-based IDs and fixes the paired tool_result.call_id references so
	// the history remains internally consistent.
	messages = deduplicateToolUseIDs(ctx, messages, logger)

	if len(messages) == 0 {
		return nil, sdkerr.Permanent(
			"anthropic.empty_messages",
			"at least one message is required",
		)
	}

	anthropicMessages := make([]Message, 0, len(messages))

	for i, msg := range messages {
		// Skip system messages (handled separately)
		if msg.Role == conversation.RoleSystem {
			logger.Debug(ctx, "translate.messages.skip_system", observability.F("index", i))
			continue
		}

		// Translate role - convert RoleTool to RoleUser for Anthropic
		role := string(msg.Role)
		if msg.Role == conversation.RoleTool {
			role = "user" // Tool results are sent as user messages in Anthropic API
			logger.Debug(ctx, "translate.messages.tool_to_user", observability.F("index", i))
		}

		if role != "user" && role != "assistant" {
			logger.Error(ctx, "translate.messages.invalid_role",
				observability.F("index", i),
				observability.F("role", role),
			)
			return nil, sdkerr.Permanent(
				"anthropic.invalid_role",
				fmt.Sprintf("invalid message role: %s (must be 'user' or 'assistant')", role),
			)
		}

		// Use effective content to handle modifications (like folding thinking into text)
		effectiveContent := msg.Content

		// Check if this message has cache control
		// NOTE: We ignore pre-existing cache control metadata from the SDK message
		// to avoid TTL conflicts (e.g. 5m vs 1h) and to stay within Anthropic's
		// 4-marker limit. Request-level cache control is applied in translateRequest.
		var messageCacheControl *CacheControl

		// Process image metadata
		imageBlocks, err := ProcessImageMetadata(msg.Metadata)
		if err != nil {
			return nil, err
		}

		// Process document metadata (PDFs)
		documentBlocks, err := ProcessDocumentMetadata(msg.Metadata)
		if err != nil {
			return nil, err
		}

		// Check if this message has thinking content
		// IMPORTANT: When thinking is enabled, ALL assistant messages that originally had
		// thinking blocks should keep them in the conversation history.
		// The API requires this to maintain proper thinking context across turns.
		// Check BOTH the dedicated Thinking field AND metadata for backward compatibility.
		var hasThinking bool
		var thinkingContent string
		var thinkingSignature string
		if thinkingEnabled && role == "assistant" {
			// First check the dedicated Thinking field
			if msg.Thinking != "" {
				hasThinking = true
				thinkingContent = msg.Thinking
			}
			// Also check metadata (backward compatibility with older messages)
			if !hasThinking && msg.Metadata != nil {
				if thinking, ok := msg.Metadata["thinking"].(string); ok && thinking != "" {
					hasThinking = true
					thinkingContent = thinking
				}
			}

			// Get signature if thinking was found
			if hasThinking && msg.Metadata != nil {
				// Try to get the original signature from when we received this thinking block
				if sig, ok := msg.Metadata["thinking_signature"].(string); ok && sig != "" {
					thinkingSignature = sig
				}
			}

			// Handle cross-provider thinking (thinking present but no valid signature)
			// Anthropic requires a valid signature for thinking blocks. If we don't have one
			// (e.g. from Cerebras/GLM), we cannot send a thinking block.
			// Fallback: Prepend thinking to text content so context is preserved.
			if hasThinking && thinkingSignature == "" {
				prefix := "[Thinking Process]\n"
				if effectiveContent != "" {
					effectiveContent = fmt.Sprintf("%s%s\n\n%s", prefix, thinkingContent, effectiveContent)
				} else {
					effectiveContent = fmt.Sprintf("%s%s", prefix, thinkingContent)
				}
				// Disable hasThinking so we don't try to create a thinking block
				hasThinking = false
			}
		}

		// CRITICAL: Trim trailing whitespace from content to satisfy Anthropic API validation
		// The API rejects messages where assistant content ends with trailing whitespace
		// This applies to both simple string content and text blocks in complex content
		if effectiveContent != "" {
			effectiveContent = strings.TrimRight(effectiveContent, " \t\n\r")
		}

		// Simple text content (no tools, no images, no documents, no thinking)
		if len(msg.ToolCalls) == 0 && len(msg.ToolResults) == 0 && len(imageBlocks) == 0 && len(documentBlocks) == 0 && !hasThinking {
			// Skip entirely empty messages (empty content with no tools/images/documents)
			// Anthropic API requires messages to have content
			if effectiveContent == "" {
				logger.Debug(ctx, "translate.messages.skip_empty",
					observability.F("index", i),
					observability.F("role", role),
				)
				continue
			}

			// ALWAYS use []ContentBlock format for consistency.
			// This prevents cache breaks when cache_control markers move between turns,
			// which previously caused Content to flip between string and []ContentBlock.
			textBlock := ContentBlock{
				Type: "text",
				Text: effectiveContent,
			}
			if messageCacheControl != nil {
				textBlock.CacheControl = messageCacheControl
			}
			anthropicMessages = append(anthropicMessages, Message{
				Role:    role,
				Content: []ContentBlock{textBlock},
			})
			continue
		}

		// Complex content with tool calls, results, images, documents, or thinking
		contentBlocks := make([]ContentBlock, 0)

		// CRITICAL: When thinking is enabled, the LAST assistant message must start with a thinking block
		// The API requires the ORIGINAL signature from when the thinking block was generated
		if hasThinking && role == "assistant" {
			contentBlocks = append(contentBlocks, ContentBlock{
				Type:      "thinking",
				Thinking:  thinkingContent,
				Signature: thinkingSignature, // Must use the original signature from the API response
			})
		}

		// Add text content if present
		if effectiveContent != "" {
			textBlock := ContentBlock{
				Type: "text",
				Text: effectiveContent,
			}
			// Apply cache control to text block if specified
			if messageCacheControl != nil {
				textBlock.CacheControl = messageCacheControl
			}
			contentBlocks = append(contentBlocks, textBlock)
		}

		// Add image blocks
		for _, imgBlock := range imageBlocks {
			if imgBlock != nil {
				contentBlocks = append(contentBlocks, *imgBlock)
			}
		}

		// Add document blocks (PDFs)
		for _, docBlock := range documentBlocks {
			if docBlock != nil {
				contentBlocks = append(contentBlocks, *docBlock)
			}
		}

		// Add tool calls (for assistant messages)
		for _, toolCall := range msg.ToolCalls {
			// Apply OAuth prefix to tool names in message history
			toolName := toolCall.Name
			if isOAuth {
				toolName = ApplyOAuthToolPrefix(toolName)
			}
			contentBlocks = append(contentBlocks, ContentBlock{
				Type:  "tool_use",
				ID:    toolCall.ID,
				Name:  toolName,
				Input: toolCall.Parameters,
			})
		}

		// Add tool results (for user messages)
		for i, toolResult := range msg.ToolResults {
			isError := toolResult.Error != nil

			var content any = toolResult.Output

			// Handle rich content (images) if present
			if len(toolResult.Content) > 0 {
				var blocks []ContentBlock
				for _, block := range toolResult.Content {
					switch block.Type {
					case string(tools.ContentTypeText):
						// Anthropic requires non-empty "text" field on text blocks.
						// Skip empty text blocks to prevent API validation errors.
						text := block.Text
						if text == "" {
							text = "(empty)"
						}
						blocks = append(blocks, ContentBlock{
							Type: "text",
							Text: text,
						})
					case string(tools.ContentTypeImage):
						// Handle image content (either raw bytes or base64 string)
						var data string
						if block.Text != "" {
							// stored in Text field by ImageContentBase64 (MCP path)
							data = block.Text
						} else {
							// stored in Data field as raw bytes
							data = base64.StdEncoding.EncodeToString(block.Data)
						}
						// Copy MimeType out of the loop variable before taking its address.
						// &block.MimeType would capture the loop variable, causing all
						// ContentSource pointers to share the same memory (last-iter wins).
						//
						// Also reconcile the declared MimeType against the actual bytes:
						// upstream tools/MCP servers frequently mislabel image payloads
						// (e.g. declaring "image/png" for data that is really JPEG, as
						// seen from browser-automation screenshot bridges). Anthropic
						// independently sniffs the bytes and hard-rejects the whole
						// request with "all providers exhausted" when the declared
						// media_type disagrees with the content, so this is the last
						// line of defense regardless of where the block originated.
						mimeTypeCopy := vision.ReconcileMediaType(data, block.MimeType)

						blocks = append(blocks, ContentBlock{
							Type: "image",
							Source: &ContentSource{
								Type:      "base64",
								MediaType: &mimeTypeCopy,
								Data:      data,
							},
						})
					}
				}

				// If we successfully converted blocks, use them
				if len(blocks) > 0 {
					content = blocks
				}
			}

			// CRITICAL FIX: Anthropic API requires tool_result.content to be either:
			// 1. A non-empty string, OR
			// 2. An array of content blocks with at least one text block
			// Empty string "" violates the schema and causes:
			// "messages.X.content.Y.tool_result.content.0.text.text: Field required"
			//
			// When toolResult.Output is empty AND no rich content blocks exist,
			// wrap it in a text block to satisfy the API schema.
			if outputStr, ok := content.(string); ok && outputStr == "" {
				content = []ContentBlock{
					{
						Type: "text",
						Text: "(empty file)",
					},
				}
			}

			resultBlock := ContentBlock{
				Type:      "tool_result",
				ToolUseID: toolResult.CallID,
				Content:   content,
				IsError:   isError,
			}

			// Apply cache control to last tool result if message has cache control
			// (typically you cache the final tool result in a sequence)
			if messageCacheControl != nil && i == len(msg.ToolResults)-1 {
				resultBlock.CacheControl = messageCacheControl
			}

			contentBlocks = append(contentBlocks, resultBlock)
		}

		// Skip messages with no content blocks (completely empty)
		// This can happen when a message has no text, tools, images, documents, or thinking
		if len(contentBlocks) == 0 {
			logger.Debug(ctx, "translate.messages.skip_empty_blocks",
				observability.F("index", i),
				observability.F("role", role),
			)
			continue
		}

		// CRITICAL: Anthropic API requires the final block in an assistant message
		// cannot be 'thinking'. If we have only thinking block(s), add a placeholder text.
		// This can happen when:
		// 1. Model produces only thinking with no visible output
		// 2. Conversation was compacted and text content was lost
		// 3. Cross-provider conversation had thinking but text was stripped
		if role == "assistant" && len(contentBlocks) > 0 {
			hasThinkingBlock := false
			hasNonThinkingBlock := false
			for _, block := range contentBlocks {
				if block.Type == "thinking" {
					hasThinkingBlock = true
				} else {
					hasNonThinkingBlock = true
				}
			}

			if hasThinkingBlock && !hasNonThinkingBlock {
				// Add placeholder text since API requires final block to not be 'thinking'
				contentBlocks = append(contentBlocks, ContentBlock{
					Type: "text",
					Text: "[Continued]",
				})
				logger.Warn(ctx, "translate.messages.thinking_only_assistant_repaired",
					observability.F("index", i),
					observability.F("blocks_before", len(contentBlocks)-1),
				)
			}
		}

		// CRITICAL FIX: Anthropic API requires tool_result blocks to ONLY appear in
		// "user" messages. If a canonical assistant message somehow has both ToolCalls
		// AND ToolResults (e.g. from cross-provider fallback, compaction corruption,
		// or conversation reconstruction), we MUST split it into two API messages:
		//   1. assistant message with tool_use + text + thinking blocks
		//   2. user message with tool_result blocks
		// Without this split, the API returns:
		//   "messages.N: `tool_result` blocks can only be in `user` messages"
		// See: https://platform.claude.com/docs/en/api — tool_result must be in user messages
		if role == "assistant" {
			var assistantBlocks []ContentBlock
			var toolResultBlocks []ContentBlock
			for _, block := range contentBlocks {
				if block.Type == "tool_result" {
					toolResultBlocks = append(toolResultBlocks, block)
				} else {
					assistantBlocks = append(assistantBlocks, block)
				}
			}
			if len(toolResultBlocks) > 0 {
				// We have tool_result blocks in an assistant message — split them out
				logger.Warn(ctx, "translate.messages.split_tool_results_from_assistant",
					observability.F("index", i),
					observability.F("assistant_blocks", len(assistantBlocks)),
					observability.F("tool_result_blocks", len(toolResultBlocks)),
				)
				if len(assistantBlocks) > 0 {
					anthropicMessages = append(anthropicMessages, Message{
						Role:    "assistant",
						Content: assistantBlocks,
					})
				}
				anthropicMessages = append(anthropicMessages, Message{
					Role:    "user",
					Content: toolResultBlocks,
				})
			} else {
				anthropicMessages = append(anthropicMessages, Message{
					Role:    role,
					Content: contentBlocks,
				})
			}
		} else {
			anthropicMessages = append(anthropicMessages, Message{
				Role:    role,
				Content: contentBlocks,
			})
		}
	}

	// Anthropic requires messages to alternate user/assistant.
	// First message must be from user — only enforce this for full-conversation
	// translations; the incremental path translates a sub-slice that legitimately
	// starts with an assistant message, so it passes requireUserFirst=false.
	if requireUserFirst && len(anthropicMessages) > 0 && anthropicMessages[0].Role != "user" {
		return nil, sdkerr.Permanent(
			"anthropic.invalid_first_message",
			"first message must be from user",
		)
	}

	// Emit one bounded history event, then validate tool_use/tool_result pairing.
	logger.Info(ctx, "translate.messages.summary",
		observability.F("input_message_count", historySummary.MessageCount),
		observability.F("output_message_count", len(anthropicMessages)),
		observability.F("nil_message_count", historySummary.NilCount),
		observability.F("role_histogram", historySummary.Roles),
		observability.F("tool_call_count", historySummary.ToolCalls),
		observability.F("tool_result_count", historySummary.ToolResults),
		observability.F("message_id_sequence_hash", historySummary.IDSequenceHash),
		observability.F("thinking_enabled", thinkingEnabled),
	)

	// Validate tool_use/tool_result pairing.
	var lastToolUseIDs []string
	for i, msg := range anthropicMessages {
		var toolUseIDs []string
		var toolResultIDs []string

		// Extract content block info
		if blocks, ok := msg.Content.([]ContentBlock); ok {
			for _, block := range blocks {
				if block.Type == "tool_use" {
					toolUseIDs = append(toolUseIDs, block.ID)
				}
				if block.Type == "tool_result" {
					toolResultIDs = append(toolResultIDs, block.ToolUseID)
				}
			}
		}

		// Validate tool_result messages have matching tool_use in previous assistant message
		if len(toolResultIDs) > 0 {
			for _, resultID := range toolResultIDs {
				found := slices.Contains(lastToolUseIDs, resultID)
				if !found {
					logger.Error(ctx, "translate.messages.orphan_tool_result",
						observability.F("index", i),
						observability.F("tool_result_id", resultID),
						observability.F("expected_tool_use_ids", lastToolUseIDs),
					)
				}
			}
		}

		// Track tool_use IDs from assistant messages
		if msg.Role == "assistant" && len(toolUseIDs) > 0 {
			lastToolUseIDs = toolUseIDs
		} else if msg.Role == "user" || msg.Role == "tool" {
			// Reset after tool results are processed
			if len(toolResultIDs) > 0 {
				lastToolUseIDs = nil
			}
		}
	}

	// CRITICAL: Merge consecutive user messages before any pairing validation.
	// Consecutive user messages arise when:
	// 1. A hook_context (RoleUser) message is injected between turns and both it
	//    and the original user message end up adjacent in the history.
	// 2. Gate A hook injection saves the hook message to the DB first, then the
	//    real user message — both appear as user-role when the next session loads.
	// Anthropic requires strict user	assistant alternation. Without merging,
	// repairOrphanedToolUse can incorrectly insert synthetic tool_results into
	// the hook_context message (because it stops looking at messages[i+1] even
	// when that message has no tool_results), leaving the REAL tool_result
	// orphaned in a subsequent user message.
	// Mirrors Claude Code's normalizeMessagesForAPI consecutive-user merge.
	anthropicMessages = mergeConsecutiveUserMessages(ctx, anthropicMessages, logger)

	// CRITICAL: Repair orphaned tool_use blocks before returning
	// Anthropic requires that every tool_use has a corresponding tool_result immediately after
	// This can happen when:
	// 1. A conversation was started with a different provider (e.g., Gemini) that allows interruptions
	// 2. The user interrupted a tool call before it completed
	// 3. The conversation was compacted and tool results were lost
	anthropicMessages = repairOrphanedToolUse(ctx, anthropicMessages, logger)

	// CRITICAL: Strip orphaned tool_result blocks.
	// Anthropic requires every tool_result.tool_use_id to reference a tool_use block
	// in the immediately preceding assistant message. Orphaned tool_results cause:
	//   "unexpected tool_use_id found in tool_result blocks: <id>. Each tool_result block
	//    must have a corresponding tool_use block in the previous message."
	// This can happen when:
	// 1. Compaction removed the assistant tool_use message but kept the user tool_result message
	// 2. A message was skipped (empty content blocks) stripping the paired tool_use
	// 3. Cross-provider fallback altered assistant message structure
	anthropicMessages = stripOrphanedToolResults(ctx, anthropicMessages, logger)

	return anthropicMessages, nil
}

// translateMessagesIncremental translates only new canonical messages by reusing
// previously-cached translations. This avoids the O(N²) cost of re-translating
// the entire conversation history on every API call.
//
// If the canonical message count decreased (e.g. conversation was compacted),
// the cache is invalidated and a full translation is performed.
// messagesMatchCache returns true when the canonical input messages produced
// the cached translated messages. Cheap check on the first user message's
// text content — sufficient to distinguish independent requests that happen
// to have the same message count.
func messagesMatchCache(canonical []*conversation.Message, cachedTranslated []Message) bool {
	if len(canonical) == 0 || len(cachedTranslated) == 0 {
		return false
	}
	// First canonical user message text vs first cached user message text.
	var canonHead string
	for _, m := range canonical {
		if m == nil {
			continue
		}
		if m.Role == conversation.RoleUser {
			canonHead = m.Content
			break
		}
	}
	if canonHead == "" {
		return false
	}
	for _, m := range cachedTranslated {
		if m.Role != "user" {
			continue
		}
		switch c := m.Content.(type) {
		case string:
			return strings.HasPrefix(canonHead, c) || strings.HasPrefix(c, canonHead)
		case []ContentBlock:
			for _, b := range c {
				if b.Type == "text" && b.Text != "" {
					return strings.HasPrefix(canonHead, b.Text) || strings.HasPrefix(b.Text, canonHead)
				}
			}
		}
		break
	}
	return false
}

func translateMessagesIncremental(ctx context.Context, messages []*conversation.Message, thinkingEnabled bool, isOAuth bool, logger observability.Logger, cache *TranslationCache) ([]Message, error) {
	cached, cachedCount := cache.Get()

	// If message count decreased, invalidate and do a full translation
	if len(messages) < cachedCount {
		logger.Info(ctx, "translate.messages.cache_invalidated",
			observability.F("cached_count", cachedCount),
			observability.F("current_count", len(messages)),
			observability.F("reason", "message_count_decreased"),
		)
		cache.Reset()
		result, err := translateMessages(ctx, messages, thinkingEnabled, isOAuth, logger)
		if err != nil {
			return nil, err
		}
		cache.UpdateWithCanonical(result, messages)
		return result, nil
	}

	// If message count is the same as cached, the cache is only valid when
	// the FIRST canonical message's text content matches what produced the
	// cache. Callers that send N independent single-message requests
	// (e.g. sac's indexer) would otherwise get the first request's
	// translated message returned for every subsequent call.
	if len(messages) == cachedCount && cached != nil {
		if messagesMatchCache(messages, cached) {
			logger.Debug(ctx, "translate.messages.cache_hit",
				observability.F("cached_count", cachedCount),
			)
			return cached, nil
		}
		logger.Info(ctx, "translate.messages.cache_invalidated",
			observability.F("cached_count", cachedCount),
			observability.F("current_count", len(messages)),
			observability.F("reason", "content_differs"),
		)
		cache.Reset()
		result, err := translateMessages(ctx, messages, thinkingEnabled, isOAuth, logger)
		if err != nil {
			return nil, err
		}
		cache.UpdateWithCanonical(result, messages)
		return result, nil
	}

	// If cache is empty, do a full translation
	if cached == nil || cachedCount == 0 {
		result, err := translateMessages(ctx, messages, thinkingEnabled, isOAuth, logger)
		if err != nil {
			return nil, err
		}
		cache.UpdateWithCanonical(result, messages)
		return result, nil
	}

	// Prefix-safety guard: the incremental slice below assumes messages[:cachedCount]
	// are ALREADY represented by the cache. That holds only when the current
	// canonical prefix is identical to the one the cache was built from. When a
	// canonical message doesn't survive translation 1:1 (e.g. an inline system
	// message that gets stripped), cachedCount over-counts the translated prefix
	// and messages[cachedCount:] skips a real message — silently dropping an
	// assistant reply and merging two user turns. Verify the prefix; on any
	// mismatch, do a full (correct) translation instead of the fast path.
	if !cache.PrefixMatches(messages) {
		logger.Info(ctx, "translate.messages.cache_invalidated",
			observability.F("cached_count", cachedCount),
			observability.F("current_count", len(messages)),
			observability.F("reason", "prefix_mismatch"),
		)
		cache.Reset()
		result, err := translateMessages(ctx, messages, thinkingEnabled, isOAuth, logger)
		if err != nil {
			return nil, err
		}
		cache.UpdateWithCanonical(result, messages)
		return result, nil
	}

	// Incremental: translate only the new messages
	newMessages := messages[cachedCount:]
	logger.Info(ctx, "translate.messages.incremental",
		observability.F("cached_count", cachedCount),
		observability.F("new_count", len(newMessages)),
		observability.F("total_count", len(messages)),
	)

	// Use requireUserFirst=false because newMessages is a mid-conversation sub-slice:
	// it may legitimately start with an assistant message (e.g. the cache boundary
	// fell right after the preceding user message). The full combined result is
	// already validated to start with a user message from the cached prefix.
	newTranslated, err := translateMessagesCore(ctx, newMessages, thinkingEnabled, isOAuth, logger, false)
	if err != nil {
		// On error, fall back to full translation
		logger.Warn(ctx, "translate.messages.incremental_failed_fallback",
			observability.F("error", err.Error()),
		)
		cache.Reset()
		result, err := translateMessages(ctx, messages, thinkingEnabled, isOAuth, logger)
		if err != nil {
			return nil, err
		}
		cache.UpdateWithCanonical(result, messages)
		return result, nil
	}

	// Combine cached + new, then run merge/repair/strip passes on the full set
	combined := make([]Message, 0, len(cached)+len(newTranslated))
	combined = append(combined, cached...)
	combined = append(combined, newTranslated...)
	combined = mergeConsecutiveUserMessages(ctx, combined, logger)
	combined = repairOrphanedToolUse(ctx, combined, logger)
	combined = stripOrphanedToolResults(ctx, combined, logger)

	cache.UpdateWithCanonical(combined, messages)
	return combined, nil
}

// mergeConsecutiveUserMessages merges adjacent "user" role messages into a single
// message. The Anthropic API requires strict user	assistant alternation. Consecutive
// user messages arise in Swarm when:
//
//  1. Gate A injects a hook_context (RoleUser) message before the real user message
//     and saves both to the DB — on the next session load they appear adjacent.
//  2. Tool results (RoleTool 	 "user" after translation) are immediately followed
//     by a hook_context user message from Gate B.
//
// Merge rules (mirrors Claude Code's normalizeMessagesForAPI):
//   - All tool_result content blocks come first in the merged content (Anthropic
//     requires tool_result blocks to precede text siblings in the same user message).
//   - Adjacent text blocks are joined with a newline separator.
//   - Empty merged messages are dropped.
func mergeConsecutiveUserMessages(ctx context.Context, messages []Message, logger observability.Logger) []Message {
	if len(messages) == 0 {
		return messages
	}

	result := make([]Message, 0, len(messages))
	for i := 0; i < len(messages); i++ {
		msg := messages[i]
		if msg.Role != "user" || i+1 >= len(messages) || messages[i+1].Role != "user" {
			result = append(result, msg)
			continue
		}

		// Consecutive user messages detected — collect and merge them all.
		var toolResultBlocks []ContentBlock
		var otherBlocks []ContentBlock

		addBlocks := func(m Message) {
			if blocks, ok := m.Content.([]ContentBlock); ok {
				for _, b := range blocks {
					if b.Type == "tool_result" {
						toolResultBlocks = append(toolResultBlocks, b)
					} else {
						otherBlocks = append(otherBlocks, b)
					}
				}
			} else if s, ok := m.Content.(string); ok && strings.TrimSpace(s) != "" {
				otherBlocks = append(otherBlocks, ContentBlock{Type: "text", Text: s})
			}
		}

		addBlocks(msg)
		mergedCount := 1
		for i+mergedCount < len(messages) && messages[i+mergedCount].Role == "user" {
			addBlocks(messages[i+mergedCount])
			mergedCount++
		}

		// Skip past all merged messages.
		i += mergedCount - 1

		// tool_result blocks MUST precede text siblings per Anthropic API constraints.
		merged := append(toolResultBlocks, otherBlocks...)
		if len(merged) == 0 {
			logger.Warn(ctx, "translate.messages.merge_empty_user_messages",
				observability.F("merged_count", mergedCount),
			)
			continue
		}

		logger.Info(ctx, "translate.messages.merge_consecutive_user",
			observability.F("merged_count", mergedCount),
			observability.F("tool_result_blocks", len(toolResultBlocks)),
			observability.F("other_blocks", len(otherBlocks)),
		)
		result = append(result, Message{Role: "user", Content: merged})
	}

	return result
}

// repairOrphanedToolUse ensures every tool_use block has a matching tool_result.
// This is critical for cross-provider compatibility since providers like Gemini
// allow tool call interruptions but Anthropic requires strict tool_use/tool_result pairing.
func repairOrphanedToolUse(ctx context.Context, messages []Message, logger observability.Logger) []Message {
	if len(messages) == 0 {
		return messages
	}

	result := make([]Message, 0, len(messages))

	for i := range messages {
		msg := messages[i]
		result = append(result, msg)

		// Only check assistant messages for tool_use blocks
		if msg.Role != "assistant" {
			continue
		}

		// Extract tool_use IDs from this message
		var toolUseIDs []string
		var toolUseNames []string
		if blocks, ok := msg.Content.([]ContentBlock); ok {
			for _, block := range blocks {
				if block.Type == "tool_use" && block.ID != "" {
					toolUseIDs = append(toolUseIDs, block.ID)
					toolUseNames = append(toolUseNames, block.Name)
				}
			}
		}

		// No tool_use blocks, nothing to repair
		if len(toolUseIDs) == 0 {
			continue
		}

		// Check which tool_use IDs are covered by the next user message (if any).
		// We only synthesize results for the UNMATCHED IDs — synthesizing for IDs
		// that already have real results would create a duplicate, and (worse) if we
		// inserted a new synthetic user message the real user message would end up
		// following a user message instead of an assistant message, making its own
		// tool_results orphaned and triggering the exact error we are trying to prevent.
		var unmatchedIDs []string
		var unmatchedNames []string
		nextUserMsgIdx := -1
		if i+1 < len(messages) && messages[i+1].Role == "user" {
			nextUserMsgIdx = i + 1
			toolResultIDs := make(map[string]bool)
			if blocks, ok := messages[i+1].Content.([]ContentBlock); ok {
				for _, block := range blocks {
					if block.Type == "tool_result" && block.ToolUseID != "" {
						toolResultIDs[block.ToolUseID] = true
					}
				}
			}
			for j, useID := range toolUseIDs {
				if !toolResultIDs[useID] {
					unmatchedIDs = append(unmatchedIDs, useID)
					unmatchedNames = append(unmatchedNames, toolUseNames[j])
				}
			}
		} else {
			// No next user message at all — every tool_use ID is unmatched.
			unmatchedIDs = toolUseIDs
			unmatchedNames = toolUseNames
		}

		if len(unmatchedIDs) == 0 {
			// All tool_use IDs are covered — nothing to synthesize.
			continue
		}

		logger.Warn(ctx, "translate.messages.repair_orphaned_tool_use",
			observability.F("message_index", i),
			observability.F("unmatched_tool_use_ids", unmatchedIDs),
			observability.F("unmatched_tool_names", unmatchedNames),
		)

		// Build synthetic tool_result blocks only for the unmatched IDs.
		syntheticBlocks := make([]ContentBlock, 0, len(unmatchedIDs))
		for j, toolUseID := range unmatchedIDs {
			syntheticBlocks = append(syntheticBlocks, ContentBlock{
				Type:      "tool_result",
				ToolUseID: toolUseID,
				Content:   fmt.Sprintf("[Tool execution interrupted - %s was cancelled before completion]", unmatchedNames[j]),
				IsError:   true,
			})
		}

		if nextUserMsgIdx >= 0 {
			// A next user message already exists. Inject the synthetic blocks INTO
			// that message (prepended before its existing content) rather than
			// inserting a wholly new user message. This preserves the correct
			// assistant→user pairing for both the synthetic and real tool_results.
			existingMsg := messages[nextUserMsgIdx]
			var existingBlocks []ContentBlock
			if blocks, ok := existingMsg.Content.([]ContentBlock); ok {
				existingBlocks = blocks
			}
			mergedBlocks := append(syntheticBlocks, existingBlocks...)
			messages[nextUserMsgIdx] = Message{
				Role:    "user",
				Content: mergedBlocks,
			}
			logger.Info(ctx, "translate.messages.synthesized_tool_results_merged",
				observability.F("unmatched_ids", unmatchedIDs),
				observability.F("synthesized_count", len(syntheticBlocks)),
				observability.F("merged_into_message_index", nextUserMsgIdx),
			)
		} else {
			// No next user message exists — insert a brand-new synthetic one.
			syntheticMsg := Message{
				Role:    "user",
				Content: syntheticBlocks,
			}
			result = append(result, syntheticMsg)
			logger.Info(ctx, "translate.messages.synthesized_tool_results",
				observability.F("unmatched_ids", unmatchedIDs),
				observability.F("synthesized_count", len(syntheticBlocks)),
			)
		}
	}

	return result
}

// stripOrphanedToolResults removes tool_result blocks whose tool_use_id does not
// correspond to any tool_use block in the immediately preceding assistant message.
//
// Anthropic's API enforces: "Each tool_result block must have a corresponding
// tool_use block in the previous message." When this invariant is violated the API
// returns HTTP 400 with code "invalid_request_error".
//
// Orphaned tool_results can arise from:
//  1. Compaction that dropped the assistant message containing the tool_use but
//     kept the following user message with its tool_results.
//  2. A message skip (empty content blocks) that removed the assistant turn.
//  3. Cross-provider fallbacks that restructured assistant messages.
//
// The function walks through the translated message slice using the OUTPUT (result)
// slice to find the immediately preceding message. This is critical: it checks the
// last message in the already-built result, not the previous INPUT index. That way,
// any messages already dropped by earlier logic are correctly excluded from the
// "preceding" check.
//
// If a user message's tool_result blocks reference IDs not in the last assistant
// message's tool_use blocks, those tool_result blocks are stripped. If all blocks
// are stripped the message is dropped entirely. Non-tool_result content (text,
// images, documents) is always preserved.
func stripOrphanedToolResults(ctx context.Context, messages []Message, logger observability.Logger) []Message {
	if len(messages) == 0 {
		return messages
	}

	result := make([]Message, 0, len(messages))
	for _, msg := range messages {
		if msg.Role != "user" {
			result = append(result, msg)
			continue
		}

		// Find the preceding message in the OUTPUT (result) slice, which correctly
		// accounts for any messages already dropped by this pass.
		// Walk backward through result to find the last assistant message.
		// Any intervening user messages mean there's no directly paired tool_use.
		prevAssistantIdx := -1
		for j := len(result) - 1; j >= 0; j-- {
			if result[j].Role == "assistant" {
				prevAssistantIdx = j
				break
			}
			if result[j].Role == "user" {
				// A user message before this one (with no assistant in between)
				// means this user message cannot have valid tool_results — no
				// corresponding tool_use exists in the immediately preceding message.
				break
			}
		}

		if prevAssistantIdx < 0 {
			// No immediately preceding assistant message — any tool_result blocks
			// in this user message are orphaned. Strip them; keep other content.
			blocks, ok := msg.Content.([]ContentBlock)
			if !ok {
				result = append(result, msg)
				continue
			}
			var kept []ContentBlock
			var stripped []string
			for _, block := range blocks {
				if block.Type == "tool_result" {
					stripped = append(stripped, block.ToolUseID)
				} else {
					kept = append(kept, block)
				}
			}
			if len(stripped) > 0 {
				logger.Warn(ctx, "translate.messages.strip_orphaned_tool_results_no_preceding_assistant",
					observability.F("stripped_tool_use_ids", stripped),
					observability.F("kept_blocks", len(kept)),
					observability.F("reason", "no preceding assistant message in output — tool_result blocks orphaned"),
				)
			}
			if len(kept) > 0 {
				msg.Content = kept
				result = append(result, msg)
			} else if len(stripped) > 0 {
				logger.Warn(ctx, "translate.messages.drop_empty_user_message_after_strip",
					observability.F("reason", "all blocks were orphaned tool_results (no preceding assistant)"),
				)
			} else {
				result = append(result, msg)
			}
			continue
		}

		// Build set of tool_use IDs from the preceding assistant message.
		precedingToolUseIDs := make(map[string]bool)
		if blocks, ok := result[prevAssistantIdx].Content.([]ContentBlock); ok {
			for _, block := range blocks {
				if block.Type == "tool_use" && block.ID != "" {
					precedingToolUseIDs[block.ID] = true
				}
			}
		}

		blocks, ok := msg.Content.([]ContentBlock)
		if !ok {
			result = append(result, msg)
			continue
		}

		// Separate tool_result blocks from other content.
		var keptBlocks []ContentBlock
		var strippedIDs []string
		for _, block := range blocks {
			if block.Type == "tool_result" {
				if precedingToolUseIDs[block.ToolUseID] {
					keptBlocks = append(keptBlocks, block)
				} else {
					strippedIDs = append(strippedIDs, block.ToolUseID)
				}
			} else {
				keptBlocks = append(keptBlocks, block)
			}
		}

		if len(strippedIDs) > 0 {
			logger.Warn(ctx, "translate.messages.strip_orphaned_tool_results",
				observability.F("stripped_tool_use_ids", strippedIDs),
				observability.F("kept_blocks", len(keptBlocks)),
				observability.F("reason", "tool_result references tool_use_id not present in previous assistant message"),
			)
		}

		if len(keptBlocks) == 0 {
			// Entire user message was orphaned tool_results — drop it.
			logger.Warn(ctx, "translate.messages.drop_empty_user_message_after_strip",
				observability.F("reason", "all blocks were orphaned tool_results"),
			)
			continue
		}

		if len(strippedIDs) > 0 {
			// Rebuild message with only the kept blocks.
			msg.Content = keptBlocks
		}
		result = append(result, msg)
	}

	return result
}

// translateTools converts canonical tools to Anthropic tool format.
// cacheControl is applied to the last tool in the list (typical caching pattern).
func translateTools(tools []provider.Tool, cacheControl *CacheControl) ([]Tool, error) {
	anthropicTools := make([]Tool, 0, len(tools))

	for i, tool := range tools {
		// Check if this is a beta tool (computer use, text editor, bash, web search)
		isBetaTool := tool.Type == ToolTypeComputerUse ||
			tool.Type == ToolTypeTextEditor ||
			tool.Type == ToolTypeBash ||
			tool.Type == ToolTypeWebSearch

		// Anthropic supports:
		// 1. Function-type tools (standard tool calling)
		// 2. Beta tools (computer_20250124, text_editor_20250728, bash_20250124, web_search_20250305)
		if tool.Type != "" && tool.Type != "function" && !isBetaTool {
			continue // Skip non-function and non-beta tools
		}

		var anthropicTool Tool

		// Beta tools have different structure than function tools
		if isBetaTool {
			// Beta tools ONLY include: name, type, and type-specific fields
			// DO NOT include description or input_schema (API will reject them)
			anthropicTool = Tool{
				Name: tool.Name,
				Type: tool.Type,
			}

			// Computer use tool needs display dimensions
			if tool.Type == ToolTypeComputerUse && tool.Metadata != nil {
				if width, ok := tool.Metadata["display_width_px"].(int); ok {
					anthropicTool.DisplayWidthPx = width
				}
				if height, ok := tool.Metadata["display_height_px"].(int); ok {
					anthropicTool.DisplayHeightPx = height
				}
				if displayNum, ok := tool.Metadata["display_number"].(int); ok {
					anthropicTool.DisplayNumber = displayNum
				}
			}

			// Web search tool needs max_uses and domain filters
			if tool.Type == ToolTypeWebSearch && tool.Metadata != nil {
				// Handle max_uses - can be int or float64 from JSON
				if maxUses, ok := tool.Metadata["max_uses"].(int); ok {
					anthropicTool.MaxUses = &maxUses
				} else if maxUses, ok := tool.Metadata["max_uses"].(float64); ok {
					mu := int(maxUses)
					anthropicTool.MaxUses = &mu
				}
				// Handle allowed_domains
				if domains, ok := tool.Metadata["allowed_domains"].([]string); ok {
					anthropicTool.AllowedDomains = domains
				}
				// Handle blocked_domains
				if domains, ok := tool.Metadata["blocked_domains"].([]string); ok {
					anthropicTool.BlockedDomains = domains
				}
				// Handle user_location
				if loc, ok := tool.Metadata["user_location"].(*UserLocation); ok {
					anthropicTool.UserLocation = loc
				}
			}

			// Check for tool-specific cache control in metadata
			if tool.Metadata != nil {
				if toolCacheControl, ok := tool.Metadata["cache_control"].(*CacheControl); ok {
					anthropicTool.CacheControl = toolCacheControl
				}
			}
		} else {
			// Standard function tool includes all fields.
			// Anthropic requires input_schema to always have "type": "object".
			// Normalize defensively so MCP tools or no-param tools don't fail.
			schema := tool.Parameters
			if schema == nil {
				schema = map[string]any{"type": "object", "properties": map[string]any{}}
			} else if m, ok := schema.(map[string]any); ok {
				if _, hasType := m["type"]; !hasType {
					m["type"] = "object"
				}
			}
			anthropicTool = Tool{
				Name:        tool.Name,
				Description: tool.Description,
				InputSchema: schema,
			}
		}

		// Apply cache control to the last tool (Anthropic best practice)
		// Only if not already set by tool-specific cache control
		if cacheControl != nil && i == len(tools)-1 && anthropicTool.CacheControl == nil {
			anthropicTool.CacheControl = cacheControl
		}

		anthropicTools = append(anthropicTools, anthropicTool)
	}

	return anthropicTools, nil
}

// translateResponse converts an Anthropic MessageResponse to a canonical ChatResponse.
// isOAuth indicates if this is an OAuth request (for tool name unprefixing).
func translateResponse(anthropicResp *MessageResponse, isOAuth bool) (*provider.ChatResponse, error) {
	// Create canonical message
	msg := &conversation.Message{
		Role:    conversation.RoleAssistant,
		Content: "",
	}

	// Extract content blocks
	var textParts []string
	var toolCalls []conversation.ToolCall
	var thinkingBlocks []string
	var thinkingSignature string // Preserve the signature from the thinking block
	var citations []Citation

	for _, block := range anthropicResp.Content {
		// Debug: log block type for thinking debugging
		if block.Type == "thinking" || block.Thinking != "" {
			// Found thinking block - either explicit type or thinking field populated
			if block.Type == "thinking" {
				thinkingBlocks = append(thinkingBlocks, block.Thinking)
				// CRITICAL: Preserve the signature so we can send it back in future requests
				if block.Signature != "" {
					thinkingSignature = block.Signature
				}
			} else if block.Thinking != "" {
				// Thinking field populated on text block
				thinkingBlocks = append(thinkingBlocks, block.Thinking)
			}
		}

		switch block.Type {
		case "text":
			textParts = append(textParts, block.Text)

		case "tool_use":
			// Convert input to map if needed
			var params map[string]any
			if block.Input != nil {
				if inputMap, ok := block.Input.(map[string]any); ok {
					params = inputMap
				}
			}

			toolCalls = append(toolCalls, conversation.ToolCall{
				ID:         block.ID,
				Name:       block.Name,
				Parameters: params,
			})

		case "thinking":
			// Already handled above

		case "redacted_thinking":
			// Redacted thinking (budget exceeded)
			thinkingBlocks = append(thinkingBlocks, "[REDACTED: thinking budget exceeded]")
		}

		// Extract citations if present
		if len(block.Citations) > 0 {
			citations = append(citations, block.Citations...)
		}
	}

	// Combine text parts and filter system reminders
	combinedText := strings.Join(textParts, "\n")
	msg.Content = filterSystemReminders(combinedText)

	// Remove OAuth prefixes from tool calls if this was an OAuth request
	if isOAuth && len(toolCalls) > 0 {
		RemoveOAuthPrefixFromToolCalls(toolCalls)
	}
	msg.ToolCalls = toolCalls

	// Translate finish reason
	finishReason := translateFinishReason(anthropicResp.StopReason)

	// Build token usage for the message
	// Use Universal Envelope system for cache metrics extraction (Side Table transformation)
	// This ensures consistent transformation logic across streaming and non-streaming
	rawJSON, _ := json.Marshal(anthropicResp)
	env := envelope.NewEnvelope(envelope.ProviderAnthropic, envelope.EventTypeMessage, rawJSON)
	registry := envelope.NewTransformRegistry()
	env.Transform(registry)

	var cacheCreation, cacheRead int
	// Per-TTL split of cacheCreation. Carried on TokenUsage (not only in the
	// cache_metrics metadata map) because that metadata does not survive
	// persistence and cache-break attribution needs the TTL after the fact.
	var cacheCreation5m, cacheCreation1h int
	var cacheMetrics map[string]int

	if env.Canonical != nil && env.Canonical.Usage != nil {
		// Use canonical values from envelope (Side Table did the transformation).
		canonical := env.Canonical.Usage
		cacheCreation = canonical.CacheCreationTokens
		cacheRead = canonical.CacheReadTokens
		cacheCreation5m = canonical.CacheCreation5mTokens
		cacheCreation1h = canonical.CacheCreation1hTokens

		if cacheCreation > 0 || cacheRead > 0 {
			cacheMetrics = map[string]int{
				"cache_creation_tokens":    cacheCreation,
				"cache_read_tokens":        cacheRead,
				"cache_creation_1h_tokens": canonical.CacheCreation1hTokens,
				"cache_creation_5m_tokens": canonical.CacheCreation5mTokens,
			}
		}
	} else {
		// Fallback: direct extraction if envelope transformation failed.
		if anthropicResp.Usage.CacheCreationInputTokens != nil {
			cacheCreation = *anthropicResp.Usage.CacheCreationInputTokens
		}
		if anthropicResp.Usage.CacheReadInputTokens != nil {
			cacheRead = *anthropicResp.Usage.CacheReadInputTokens
		}
		if cc := anthropicResp.Usage.CacheCreation; cc != nil {
			cacheCreation5m = cc.Ephemeral5mInputTokens
			cacheCreation1h = cc.Ephemeral1hInputTokens
		}
	}

	usage := &conversation.TokenUsage{
		Input:           anthropicResp.Usage.InputTokens, // pure input_tokens, excluding cache
		Output:          anthropicResp.Usage.OutputTokens,
		CacheCreation:   cacheCreation,
		CacheRead:       cacheRead,
		CacheCreation5m: cacheCreation5m,
		CacheCreation1h: cacheCreation1h,
	}
	usage.Total = usage.Input + usage.CacheCreation + usage.CacheRead + usage.Output

	// Attach token usage to the message
	msg.Tokens = usage

	// Store cache metrics in message metadata (from envelope canonical)
	if cacheMetrics != nil {
		if msg.Metadata == nil {
			msg.Metadata = make(map[string]any)
		}
		msg.Metadata["cache_metrics"] = cacheMetrics
	}

	// Store extended thinking in BOTH locations for consistency:
	// 1. msg.Thinking field (for direct access and Clone() preservation)
	// 2. msg.Metadata["thinking"] (for translateMessages to read when building requests)
	if len(thinkingBlocks) > 0 {
		thinkingContent := strings.Join(thinkingBlocks, "\n\n")

		// Store in the dedicated field
		msg.Thinking = thinkingContent

		// Also store in metadata for backward compatibility with translateMessages
		if msg.Metadata == nil {
			msg.Metadata = make(map[string]any)
		}
		msg.Metadata["thinking"] = thinkingContent

		// CRITICAL: Store the signature so we can send it back when thinking is enabled
		if thinkingSignature != "" {
			msg.Metadata["thinking_signature"] = thinkingSignature
		}
	}

	// Store citations in metadata
	if len(citations) > 0 {
		if msg.Metadata == nil {
			msg.Metadata = make(map[string]any)
		}
		msg.Metadata["citations"] = citations
	}

	return &provider.ChatResponse{
		Message:      msg,
		FinishReason: finishReason,
		Usage:        usage,
		RawResponse:  anthropicResp,
	}, nil
}

// filterSystemReminders removes system reminder tags from content.
// These are injected by Claude Code and similar environments and should not be shown to users.
func filterSystemReminders(content string) string {
	// Pattern: <system-reminder>...</system-reminder>
	// This regex handles multi-line reminders
	result := content

	// Simple approach: find and remove all <system-reminder>...</system-reminder> blocks
	for {
		start := strings.Index(result, "<system-reminder>")
		if start == -1 {
			break
		}

		end := strings.Index(result[start:], "</system-reminder>")
		if end == -1 {
			break
		}

		// Remove the entire block (including tags)
		result = result[:start] + result[start+end+len("</system-reminder>"):]
	}

	// Trim extra whitespace that may be left
	return strings.TrimSpace(result)
}

// translateFinishReason converts Anthropic stop reasons to canonical format.
func translateFinishReason(stopReason string) provider.FinishReason {
	switch stopReason {
	case "end_turn":
		return provider.FinishReasonStop
	case "max_tokens":
		return provider.FinishReasonLength
	case "stop_sequence":
		return provider.FinishReasonStop
	case "tool_use":
		return provider.FinishReasonToolCalls
	default:
		return provider.FinishReasonStop
	}
}

// Helper functions for content encoding

// ProcessDocumentMetadata extracts document data from message metadata.
// Documents (PDFs) are stored in metadata["documents"] as []map[string]any with keys:
// - "type": "base64" or "url"
// - "media_type": "application/pdf"
// - "data": base64 encoded PDF data (for base64 type)
// - "url": document URL (for url type)
func ProcessDocumentMetadata(metadata map[string]any) ([]*ContentBlock, error) {
	if metadata == nil {
		return nil, nil
	}

	// Check for documents in metadata
	documentsRaw, ok := metadata["documents"]
	if !ok {
		return nil, nil
	}

	// Handle both []map[string]any and []any cases
	var documentsList []map[string]any

	switch docs := documentsRaw.(type) {
	case []map[string]any:
		documentsList = docs
	case []any:
		// Convert []any to []map[string]any
		documentsList = make([]map[string]any, 0, len(docs))
		for _, doc := range docs {
			if docMap, ok := doc.(map[string]any); ok {
				documentsList = append(documentsList, docMap)
			}
		}
	default:
		return nil, sdkerr.Permanent(
			"anthropic.pdf.invalid_documents_format",
			"documents metadata must be []map[string]any or []any",
		)
	}

	if len(documentsList) == 0 {
		return nil, nil
	}

	contentBlocks := make([]*ContentBlock, 0, len(documentsList))

	for i, docData := range documentsList {
		// Extract docType, converting any to string
		docTypeRaw, ok := docData["type"]
		if !ok {
			return nil, sdkerr.Permanent(
				"anthropic.pdf.missing_type",
				fmt.Sprintf("document %d missing 'type' field", i),
			)
		}

		docType, ok := docTypeRaw.(string)
		if !ok {
			return nil, sdkerr.Permanent(
				"anthropic.pdf.invalid_type_format",
				fmt.Sprintf("document %d 'type' field must be a string, got %T", i, docTypeRaw),
			)
		}

		var contentBlock *ContentBlock

		if docType == "base64" {
			// Extract media type
			mediaTypeRaw, ok := docData["media_type"]
			if !ok {
				return nil, sdkerr.Permanent(
					"anthropic.pdf.missing_media_type",
					fmt.Sprintf("base64 document %d missing 'media_type' field", i),
				)
			}

			mediaType, ok := mediaTypeRaw.(string)
			if !ok {
				return nil, sdkerr.Permanent(
					"anthropic.pdf.invalid_media_type_format",
					fmt.Sprintf("base64 document %d 'media_type' must be a string, got %T", i, mediaTypeRaw),
				)
			}

			// Extract data
			dataRaw, ok := docData["data"]
			if !ok {
				return nil, sdkerr.Permanent(
					"anthropic.pdf.missing_data",
					fmt.Sprintf("base64 document %d missing 'data' field", i),
				)
			}

			data, ok := dataRaw.(string)
			if !ok {
				return nil, sdkerr.Permanent(
					"anthropic.pdf.invalid_data_format",
					fmt.Sprintf("base64 document %d 'data' must be a string, got %T", i, dataRaw),
				)
			}

			contentBlock = &ContentBlock{
				Type: "document",
				Source: &ContentSource{
					Type:      "base64",
					MediaType: &mediaType,
					Data:      data,
				},
			}
		} else if docType == "url" {
			// Extract URL
			urlRaw, ok := docData["url"]
			if !ok {
				return nil, sdkerr.Permanent(
					"anthropic.pdf.missing_url",
					fmt.Sprintf("URL document %d missing 'url' field", i),
				)
			}

			url, ok := urlRaw.(string)
			if !ok {
				return nil, sdkerr.Permanent(
					"anthropic.pdf.invalid_url_format",
					fmt.Sprintf("URL document %d 'url' must be a string, got %T", i, urlRaw),
				)
			}

			contentBlock = &ContentBlock{
				Type: "document",
				Source: &ContentSource{
					Type: "url",
					URL:  url,
				},
			}
		} else {
			return nil, sdkerr.Permanent(
				"anthropic.pdf.invalid_type_value",
				fmt.Sprintf("document %d has invalid type: %s (must be 'base64' or 'url')", i, docType),
			)
		}

		contentBlocks = append(contentBlocks, contentBlock)
	}

	return contentBlocks, nil
}
