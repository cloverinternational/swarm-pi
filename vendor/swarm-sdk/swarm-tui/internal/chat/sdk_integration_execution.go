package chat

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/agent"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/cache/profiling"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/conversation"
	sdkhooks "github.com/Swarm-Code/mono/swarm-sdk/internal/hooks"
	hooksbuiltin "github.com/Swarm-Code/mono/swarm-sdk/internal/hooks/builtin"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/mode"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/observability"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/paths"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/provider"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/provider/prompttrace"
	chatcontext "github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/chat/context"
	"github.com/getsentry/sentry-go"
)

// SetMessageInjector sets a callback on the underlying agent that is called
// between execution turns.  The callback should return pending user messages
// to inject into the conversation (and drain them).  Pass nil to clear.
func (sdk *SDKIntegration) SetMessageInjector(injector agent.MessageInjector) {
	if sdk == nil {
		return
	}
	if ag := sdk.activeAgent(); ag != nil {
		ag.SetMessageInjector(injector)
	}
}

// SetRichMessageInjector is the system-role counterpart to SetMessageInjector.
// It delivers full conversation.Message values between turns, which lets the
// TUI inject non-user notifications (background task done, async system events)
// without them being attributed to the user in the model's view.
func (sdk *SDKIntegration) SetRichMessageInjector(injector agent.RichMessageInjector) {
	if sdk == nil {
		return
	}
	if ag := sdk.activeAgent(); ag != nil {
		ag.SetRichMessageInjector(injector)
	}
}

// activeAgent returns the agent to use for execution: the mandatory SDK
// client's agent. The sdk.agent field is the same instance (handed to the
// client via WithAgentInstance) and is used only as the construction-time /
// degraded fallback before the client is wired.
func (sdk *SDKIntegration) activeAgent() *agent.Agent {
	if sdk.sdkClient != nil {
		if ag := sdk.sdkClient.Agent(); ag != nil {
			return ag
		}
	}
	return sdk.agent
}

// executeRequest routes through sdkClient.Execute so the SDK owns mode filter
// injection and lifecycle events. Falls back to the raw agent when sdkClient
// is unavailable (degraded mode).
func (sdk *SDKIntegration) executeRequest(ctx context.Context, req agent.ExecuteRequest) (*agent.ExecuteResponse, error) {
	if sdk.sdkClient != nil {
		return sdk.sdkClient.Execute(ctx, req)
	}
	// ExecuteWhenIdle queues the request behind any running sub-agents rather
	// than immediately returning agent.busy when the agent is mid-execution.
	return sdk.activeAgent().ExecuteWhenIdle(ctx, req)
}

func (sdk *SDKIntegration) applyRequestedModel(model string) {
	if sdk == nil || sdk.harnessGoverned() {
		return
	}
	if active := sdk.activeAgent(); active != nil {
		active.SetModel(model)
	}
}

func (sdk *SDKIntegration) ExecuteMessage(ctx context.Context, convID string, userMessage string, model string, updateChan chan<- agent.IntermediateUpdate, attachments []Attachment) (string, error) {
	return sdk.ExecuteMessageWithMaxTurns(ctx, convID, userMessage, model, updateChan, attachments, 0)
}

// ExecuteMessageWithMaxTurns executes a message with an optional per-execution
// turn budget. A value <= 0 preserves the agent's configured default.
func (sdk *SDKIntegration) ExecuteMessageWithMaxTurns(ctx context.Context, convID string, userMessage string, model string, updateChan chan<- agent.IntermediateUpdate, attachments []Attachment, maxTurns int) (string, error) {
	if maxTurns < 0 {
		maxTurns = 0
	}
	startTime := time.Now()
	sdk.lastReqStart = startTime

	// Add Sentry breadcrumb for execution start
	AddSentryBreadcrumb("agent.execution", fmt.Sprintf("Starting agent execution (model: %s)", model), sentry.LevelInfo, map[string]any{
		"conversation_id": convID,
		"model":           model,
		"provider":        sdk.GetProviderName(),
		"message_length":  len(userMessage),
	})

	// Non-harness sessions retain the legacy per-request model override. A
	// harness plan owns its model, and hot apply owns generation publication, so
	// never mutate the raw active agent on the governed path.
	sdk.applyRequestedModel(model)

	// Get full conversation history (CRITICAL: This provides context to the agent)
	var conversationMessages []*conversation.Message
	var cacheProfiler profiling.CacheProfiler // Declare at function scope for later use

	if conv, err := sdk.resumeConv(ctx, convID); err == nil {
		conversationMessages = conv.ActiveMessages()

		// CRITICAL: Ensure conversation starts with a user or system message.
		// After context trimming, we may have orphaned assistant/tool messages at the start.
		// System messages (like compaction summaries) must be preserved.
		for len(conversationMessages) > 0 &&
			conversationMessages[0].Role != conversation.RoleUser &&
			conversationMessages[0].Role != conversation.RoleSystem {
			sdk.logger.Warn(ctx, "removing_orphaned_assistant_message_at_start",
				observability.F("role", string(conversationMessages[0].Role)),
				observability.F("conversation_id", convID))
			conversationMessages = conversationMessages[1:]
		}

		// CRITICAL: Repair orphaned tool calls (tool_use without tool_result)
		// This can happen if the connection drops or tool execution is interrupted.
		// Anthropic API requires every tool_use to have a corresponding tool_result.

		// ─────────────────────────────────────────────────────────────
		// CACHE PROFILING: Track TUI-specific message mutations
		// ─────────────────────────────────────────────────────────────
		// Initialize profiler for this conversation to track mutations
		// during repair and injection phases
		cacheProfiler = profiling.GetProfiler(convID)

		// Record message state BEFORE repairs (so we can detect if they mutate)
		preRepairHash, _ := cacheProfiler.BeforeTranslation(ctx, conversationMessages)
		_ = preRepairHash // Currently informational; used for cache break detection

		repairedMessages := sdk.repairOrphanedToolCalls(ctx, conversationMessages)
		if len(repairedMessages) > len(conversationMessages) {
			// If repairs were made, persist them back to the database so they aren't orphaned forever.
			// This prevents the same orphaned tool calls from being repaired on every turn.
			conv.ReplaceActiveMessages(repairedMessages)
			if err := sdk.saveConv(ctx, conv); err != nil {
				sdk.logger.Warn(ctx, "failed_to_save_repaired_conversation", observability.F("error", err.Error()))
			} else {
				sdk.logger.Info(ctx, "saved_repaired_conversation", observability.F("conversation_id", convID))
			}
		}
		conversationMessages = repairedMessages

		sdk.logger.Info(ctx, "loaded_conversation_history",
			observability.F("conversation_id", convID),
			observability.F("message_count", len(conversationMessages)))
	} else {
		sdk.logger.Warn(ctx, "failed_to_load_conversation_history",
			observability.F("conversation_id", convID),
			observability.F("error", err.Error()))
	}

	// debugReqIndex tracks the index of the DebugRequest captured for THIS call
	// so the response/provider JSON can be written back to the exact same entry
	// later, even if nested calls append other requests in between. -1 = none.
	debugReqIndex := -1

	// Capture debug request with full conversation
	if sdk.debugScreen != nil {
		// Build complete request body with all messages
		// IMPORTANT: Capture ALL message fields including metadata, tokens, thinking
		messages := make([]map[string]any, 0)
		totalHistoryTokens := 0
		for i, msg := range conversationMessages {
			msgMap := map[string]any{
				"_index":         i,
				"role":           string(msg.Role),
				"content":        msg.Content,
				"content_length": len(msg.Content),
			}

			// Include token info if available
			if msg.Tokens != nil {
				msgMap["tokens"] = map[string]any{
					"input":  msg.Tokens.Input,
					"output": msg.Tokens.Output,
					"total":  msg.Tokens.Total,
				}
				totalHistoryTokens += msg.Tokens.Total
			}

			// Include thinking content if present (important for understanding token growth)
			if msg.Thinking != "" {
				msgMap["thinking"] = msg.Thinking
				msgMap["thinking_length"] = len(msg.Thinking)
			}

			// Include metadata (contains thinking, signatures, cache info)
			if len(msg.Metadata) > 0 {
				// Capture specific metadata fields
				metaMap := make(map[string]any)
				if thinking, ok := msg.Metadata["thinking"].(string); ok {
					metaMap["thinking_in_metadata"] = true
					metaMap["thinking_length"] = len(thinking)
				}
				if sig, ok := msg.Metadata["thinking_signature"].(string); ok {
					metaMap["has_signature"] = true
					metaMap["signature_preview"] = sig[:min(20, len(sig))] + "..."
				}
				if cache, ok := msg.Metadata["cache_metrics"]; ok {
					metaMap["cache_metrics"] = cache
				}
				if len(metaMap) > 0 {
					msgMap["metadata_summary"] = metaMap
				}
			}

			// Include tool calls (for assistant messages that make tool calls)
			if len(msg.ToolCalls) > 0 {
				toolCalls := make([]map[string]any, 0, len(msg.ToolCalls))
				for _, tc := range msg.ToolCalls {
					tcMap := map[string]any{
						"id":   tc.ID,
						"name": tc.Name,
					}
					// Truncate large parameters
					if tc.Parameters != nil {
						paramJSON, _ := json.Marshal(tc.Parameters)
						if len(paramJSON) > 500 {
							tcMap["parameters_preview"] = string(paramJSON[:500]) + "...(truncated)"
							tcMap["parameters_length"] = len(paramJSON)
						} else {
							tcMap["parameters"] = tc.Parameters
						}
					}
					toolCalls = append(toolCalls, tcMap)
				}
				msgMap["tool_calls"] = toolCalls
			}

			// Include tool results (for tool result messages)
			if len(msg.ToolResults) > 0 {
				toolResults := make([]map[string]any, 0, len(msg.ToolResults))
				for _, tr := range msg.ToolResults {
					result := map[string]any{
						"call_id":       tr.CallID,
						"output_length": len(tr.Output),
					}
					// Truncate large outputs for readability
					if len(tr.Output) > 1000 {
						result["output_preview"] = tr.Output[:1000] + "...(truncated)"
					} else {
						result["output"] = tr.Output
					}
					if tr.Error != nil {
						result["error"] = tr.Error.Message
					}
					toolResults = append(toolResults, result)
				}
				msgMap["tool_results"] = toolResults
			}
			messages = append(messages, msgMap)
		}

		// Add current user message
		messages = append(messages, map[string]any{
			"_index":         len(conversationMessages),
			"role":           "user",
			"content":        userMessage,
			"content_length": len(userMessage),
			"_current":       true,
		})

		// Get tools list with size info
		toolList := sdk.getToolsForRequest()
		toolsJSON := make([]map[string]any, 0)
		totalToolsSize := 0
		for _, tool := range toolList {
			toolJSON := map[string]any{
				"name":        tool.Name,
				"description": tool.Description,
			}
			// Calculate approximate size
			schemaJSON, _ := json.Marshal(tool.Parameters)
			toolJSON["schema_size"] = len(schemaJSON)
			totalToolsSize += len(tool.Description) + len(schemaJSON)
			toolsJSON = append(toolsJSON, toolJSON)
		}

		// Get system prompt info
		systemPrompt := sdk.activeAgent().SystemPrompt()
		systemPromptPreview := systemPrompt
		if len(systemPromptPreview) > 500 {
			systemPromptPreview = systemPromptPreview[:500] + "...(truncated)"
		}

		// Build request body with summary info
		reqBody := map[string]any{
			"_summary": map[string]any{
				"message_count":        len(messages),
				"history_tokens_sum":   totalHistoryTokens,
				"tool_count":           len(toolsJSON),
				"tools_approx_chars":   totalToolsSize,
				"system_prompt_length": len(systemPrompt),
				"thinking_enabled":     sdk.thinkingEnabled,
				"thinking_budget":      sdk.thinkingBudget,
			},
			"model":         model,
			"max_tokens":    sdk.GetMaxTokens(),
			"system":        systemPromptPreview,
			"system_length": len(systemPrompt),
			"messages":      messages,
		}
		if len(toolsJSON) > 0 {
			reqBody["tools"] = toolsJSON
		}

		reqBodyJSON, _ := json.MarshalIndent(reqBody, "", "  ")

		// Build conversation snapshot for tracking changes between requests
		snapshot := &DebugConversationState{
			MessageCount:       len(conversationMessages) + 1, // +1 for current user message
			SystemPromptLength: len(systemPrompt),
			ToolCount:          len(toolsJSON),
			Messages:           make([]DebugMessageState, 0, len(conversationMessages)+1),
		}

		// Track each message in the history
		messageHashes := make([]string, 0, len(conversationMessages)+1)
		currentMessageIDs := make(map[string]bool)
		totalContentLen := 0

		for i, msg := range conversationMessages {
			contentHash := hashContent(msg.Content)
			messageHashes = append(messageHashes, contentHash)
			currentMessageIDs[msg.ID] = true
			totalContentLen += len(msg.Content)

			tokensTotal := 0
			if msg.Tokens != nil {
				tokensTotal = msg.Tokens.Total
			}

			snapshot.Messages = append(snapshot.Messages, DebugMessageState{
				Index:          i,
				ID:             msg.ID,
				Role:           string(msg.Role),
				ContentLength:  len(msg.Content),
				ContentHash:    contentHash,
				HasToolCalls:   len(msg.ToolCalls) > 0,
				HasToolResults: len(msg.ToolResults) > 0,
				HasThinking:    msg.Thinking != "" || (msg.Metadata != nil && msg.Metadata["thinking"] != nil),
				TokensTotal:    tokensTotal,
			})
		}

		// Add current user message to snapshot
		userMsgHash := hashContent(userMessage)
		messageHashes = append(messageHashes, userMsgHash)
		totalContentLen += len(userMessage)
		snapshot.Messages = append(snapshot.Messages, DebugMessageState{
			Index:         len(conversationMessages),
			ID:            "_current_user_msg",
			Role:          "user",
			ContentLength: len(userMessage),
			ContentHash:   userMsgHash,
		})
		snapshot.TotalContentLength = totalContentLen

		// Detect messages added/removed by comparing with previous request
		var messagesAdded, messagesRemoved []string
		var prevVersion int
		if len(sdk.debugScreen.requests) > 0 {
			prevReq := sdk.debugScreen.requests[len(sdk.debugScreen.requests)-1]
			prevVersion = prevReq.ConversationVersion

			// Find messages that were in previous but not in current (removed/trimmed)
			if prevReq.ConversationState != nil {
				for _, prevMsg := range prevReq.ConversationState.Messages {
					if prevMsg.ID != "_current_user_msg" && !currentMessageIDs[prevMsg.ID] {
						messagesRemoved = append(messagesRemoved, prevMsg.ID)
					}
				}
			}
		}

		// Current user message is always "added"
		messagesAdded = append(messagesAdded, "_current_user_msg")

		// Get actual provider URL and headers
		debugURL, debugHeaders := sdk.getProviderDebugInfo()

		// Capture canonical conversation JSON (internal format before provider translation)
		canonicalConv := map[string]any{
			"conversation_id": convID,
			"message_count":   len(conversationMessages) + 1,
			"messages":        conversationMessages,
			"current_user_message": map[string]any{
				"role":    "user",
				"content": userMessage,
			},
			"system_prompt_length": len(sdk.activeAgent().SystemPrompt()),
			"tools_count":          len(sdk.getToolsForRequest()),
		}
		canonicalJSON := marshalForDebug(canonicalConv)

		debugReq := DebugRequest{
			MessageIndex:  len(conversationMessages),
			Timestamp:     startTime,
			Method:        "POST",
			URL:           debugURL,
			Headers:       debugHeaders,
			Body:          string(reqBodyJSON),
			CanonicalJSON: canonicalJSON,
			Model:         model,
			Metadata: map[string]any{
				"thinking_enabled": sdk.thinkingEnabled,
				"thinking_budget":  sdk.thinkingBudget,
				"history_messages": len(conversationMessages),
				"provider":         sdk.GetProviderName(),
			},
			// Conversation state tracking
			ConversationVersion: prevVersion + 1,
			MessageCount:        len(conversationMessages) + 1,
			MessageHashes:       messageHashes,
			MessagesAdded:       messagesAdded,
			MessagesRemoved:     messagesRemoved,
			ConversationState:   snapshot,
		}
		debugReqIndex = sdk.debugScreen.AddRequest(debugReq)
	}

	// First, add the user message to conversation
	userMsg := &conversation.Message{
		ID:        fmt.Sprintf("msg_%d", time.Now().UnixNano()),
		Timestamp: time.Now(),
		Role:      conversation.RoleUser,
		Content:   userMessage,
	}
	// Convert attachments to metadata["images"] format for vision support
	if len(attachments) > 0 {
		userMsg.Metadata = make(map[string]any)
		images := make([]map[string]string, len(attachments))
		for i, att := range attachments {
			images[i] = map[string]string{
				"type":       "base64",
				"media_type": att.MimeType,
				"data":       base64.StdEncoding.EncodeToString(att.Content),
			}
		}
		userMsg.Metadata["images"] = images
		logDebug("Added %d image(s) to user message metadata", len(attachments))
	}
	// CRITICAL: Add the userMsg with images to conversation history
	// so the agent receives it in the current request.
	// NOTE: We do NOT persist to the conversation store here — we wait until after the
	// UserPromptSubmit hook fires so that any hook-injected context (e.g. task nudge)
	// is included in the stored message content.
	conversationMessages = append(conversationMessages, userMsg)

	// Set hooks manager for tool execution events
	if sdk.hooksManager != nil {
		sdk.activeAgent().SetHooksManager(sdk.hooksManager)
		logDebug("[SDK] HooksManager set on agent - hooks will fire during tool execution")
		// Log registered hooks
		registeredHooks := sdk.hooksManager.ListHooks()
		logDebug("[SDK] Registered hooks count: %d", len(registeredHooks))
		for _, h := range registeredHooks {
			logDebug("[SDK]   Hook: %s (scope=%s, enabled=%v)", h.Hook.Name(), h.Scope, h.Enabled)
		}
	} else {
		logDebug("[SDK] HooksManager is nil - hooks will NOT fire")
	}

	sdk.setLocalUpdateChannel(updateChan)
	defer sdk.clearLocalUpdateChannel(updateChan)
	ctx = withA2AExecutionContext(ctx, a2aExecutionSourceLocal, convID, "")
	sdk.SetActiveConversation(convID)

	// Execute using agent (handles tools automatically)
	// CRITICAL: Pass conversation history so agent maintains context
	runID := fmt.Sprintf("run_%d", time.Now().UnixNano())
	defer sdk.flushProviderContextCaptures(runID)
	// Keep the agent's lazy task-store fallback on the same canonical root as
	// transcript, task and journal persistence.
	storagePath := paths.ConversationsDir()
	req := agent.ExecuteRequest{
		Message:             userMessage,
		ConversationID:      convID,
		ConversationHistory: conversationMessages, // This provides conversation context!
		MaxTurns:            maxTurns,
		Timeout:             0,           // No timeout - let agent take as long as needed
		StoragePath:         storagePath, // Ensure TaskStore path matches SetupTaskPersistence
	}
	var codexPromptIntegrity bool = sdk.IsCodexBacked()
	if req.Context == nil {
		req.Context = make(map[string]any)
	}
	req.Context["context_run_id"] = runID
	if sdk.shouldIncludeReasoningEffortInRequest() {
		req.Context["reasoning_effort"] = sdk.GetReasoningEffort()
	}

	if codexPromptIntegrity {
		if prompt, changed := sdk.EnsureCodexPromptIntegrity(ctx, false); prompt != "" {
			sdk.logger.Info(ctx, "codex.prompt.integrity_enforced",
				observability.F("changed", changed),
				observability.F("prompt_length", len(prompt)),
			)
		}
	}

	// CRITICAL: Add extended thinking configuration if enabled
	// The context is stored in agent.requestContext and will be added to Metadata
	// in agent.buildProviderRequest(), which the Anthropic provider reads from
	// Extended-thinking configuration is written into req.Context by the SDK
	// client (ApplyThinking) so the TUI doesn't encode provider translate-layer
	// keys; the disabled branch still explicitly sets thinking_enabled=false
	// (needed for Gemini 2.5/3). The "how" of thinking belongs to the client.
	if sdk.thinkingEnabled {
		sdk.sdkClient.ApplyThinking(&req, true, sdk.thinkingBudget, sdk.thinkingEffort)

		sdk.logger.Info(ctx, "agent.thinking.enabled",
			observability.F("budget", sdk.thinkingBudget),
			observability.F("effort", sdk.thinkingEffort),
			observability.F("thinking_enabled", sdk.thinkingEnabled),
		)
	} else {
		sdk.sdkClient.ApplyThinking(&req, false, 0, "")

		sdk.logger.Info(ctx, "agent.thinking.disabled",
			observability.F("thinking_enabled", sdk.thinkingEnabled),
		)
	}

	// Enable Anthropic prefix-based prompt caching for this request. The
	// cache-control marker construction now lives in the SDK client
	// (ApplyPromptCaching) so the TUI doesn't encode provider translate-layer
	// keys — the "how" of caching belongs to the client.
	if sdk.cachingEnabled {
		sdk.sdkClient.ApplyPromptCaching(&req, sdk.cacheTTL)

		sdk.logger.Info(ctx, "agent.caching.enabled",
			observability.F("ttl", sdk.cacheTTL),
			observability.F("system_cache", true),
			observability.F("tool_cache", true),
			observability.F("message_cache", true),
		)
	}

	// Auto-activate skills based on message context
	// This must happen before prompt building so active skills can inject instructions.
	// For Codex-integrity requests, skills guidance is carried on the user turn.
	// Gated by the "skills" injection source in the Context settings screen.
	skillsInjectionEnabled := !sdk.harnessGoverned() && sdk.injectionEnabled(chatcontext.SourceIDSkills)
	if !skillsInjectionEnabled {
		sdk.logger.Info(ctx, "agent.skills.injection_disabled_by_context_settings")
	}
	var originalPromptBeforeSkills string
	if skillsInjectionEnabled && sdk.skillsManager != nil && sdk.skillsManager.IsInitialized() {
		// Store original prompt for restoration after execution
		originalPromptBeforeSkills = sdk.activeAgent().SystemPrompt()

		// Auto-activate skills based on context (file patterns, keywords, project type)
		sdk.AutoActivateSkills(userMessage)

		var skillsContext string = sdk.GetSkillsPromptContextForQuery(userMessage)
		if codexPromptIntegrity {
			if strings.TrimSpace(skillsContext) != "" {
				req.Message = upsertSwarmRuntimeGuidanceSection(req.Message, "skills", skillsContext)
				sdk.logger.Info(ctx, "agent.skills.runtime_guidance_applied",
					observability.F("active_skills", len(sdk.skillsManager.GetActiveSkills())),
				)
			}
		} else if injected := sdk.InjectSkillsContext(); injected {
			sdk.logger.Info(ctx, "agent.skills.injected",
				observability.F("active_skills", len(sdk.skillsManager.GetActiveSkills())),
			)

			// Restore original prompt after execution (in defer)
			defer func() {
				if originalPromptBeforeSkills != "" {
					sdk.activeAgent().SetSystemPrompt(originalPromptBeforeSkills)
				}
			}()
		}
	}

	// Add operating mode configuration for tool filtering
	// This passes the mode's tool filtering rules to the agent via Context
	// OFF mode and ACT mode have no filtering (all tools allowed)
	currentModeID := sdk.GetOperatingMode()
	operatingModeDef := sdk.GetOperatingModeDefinition()
	if currentModeID != "" {
		if req.Context == nil {
			req.Context = make(map[string]any)
		}
		req.Context["mode"] = currentModeID
	}
	if !sdk.harnessGoverned() && operatingModeDef != nil && currentModeID != "off" && currentModeID != mode.ModeAct {
		// Only add mode_filter for modes with restrictions (PLAN, AUTO have filtering).
		// The agent-contract map shape lives in the SDK client (ApplyModeFilter) so
		// the TUI doesn't encode the mode.ModeFilterConfig<->agent bridge keys.
		sdk.sdkClient.ApplyModeFilter(&req, operatingModeDef.Name, operatingModeDef.AllowedTools, operatingModeDef.BlockedTools, operatingModeDef.HideBlockedTools)

		// Inject mode-specific instructions.
		// Codex requests carry this in the user turn runtime guidance.
		if operatingModeDef.SystemInstruction != "" {
			if codexPromptIntegrity {
				req.Message = upsertSwarmRuntimeGuidanceSection(req.Message, "mode", operatingModeDef.SystemInstruction)
			} else {
				currentPrompt := sdk.activeAgent().SystemPrompt()
				enhancedPrompt := currentPrompt + "\n\n" + operatingModeDef.SystemInstruction
				sdk.activeAgent().SetSystemPrompt(enhancedPrompt)

				// Restore original prompt after execution (in defer)
				defer sdk.activeAgent().SetSystemPrompt(currentPrompt)
			}
		}

		sdk.logger.Info(ctx, "agent.operating_mode.applied",
			observability.F("mode", currentModeID),
			observability.F("allowed_tools", len(operatingModeDef.AllowedTools)),
			observability.F("blocked_tools", len(operatingModeDef.BlockedTools)),
			observability.F("hide_blocked", operatingModeDef.HideBlockedTools),
		)
	}

	effectiveTools := sdk.activeAgent().ProviderToolsForContext(req.Context, req.DisableTools)
	// The manifest is EPHEMERAL on both provider paths: the durable store keeps
	// only the user's clean text (see the persist block below), so the block is
	// never re-sent from history and re-injecting an identical copy every turn
	// teaches the model nothing while costing input tokens on every provider.
	// Emit it when it actually says something new — first turn of a
	// conversation, or a change in the effective tool set / operating mode —
	// mirroring the "advertise once" treatment the swarm-flow guidance below
	// already gets.
	manifestSignature := capabilityManifestSignature(currentModeID, effectiveTools)
	if sdk.shouldEmitCapabilityManifest(convID, manifestSignature) {
		capabilityManifest := buildCapabilityManifest(currentModeID, effectiveTools, defaultCapabilityManifestToolLimit)
		if codexPromptIntegrity {
			req.Message = upsertSwarmRuntimeGuidanceSection(req.Message, "capabilities", capabilityManifest)
		} else {
			req.Message = req.Message + "\n\n" + capabilityManifest
		}
		prompttrace.From(ctx).Append("effective_capabilities", capabilityManifest)
		sdk.logger.Info(ctx, "agent.capability_manifest.applied",
			observability.F("mode", currentModeID),
			observability.F("tool_count", len(effectiveTools)),
			observability.F("manifest_length", len(capabilityManifest)),
		)
	} else {
		sdk.logger.Info(ctx, "agent.capability_manifest.skipped",
			observability.F("mode", currentModeID),
			observability.F("tool_count", len(effectiveTools)),
			observability.F("reason", "unchanged_since_previous_turn"),
		)
	}

	if guidance := sdk.buildA2ARuntimeGuidance(ctx, userMessage); guidance != "" {
		if codexPromptIntegrity {
			req.Message = upsertSwarmRuntimeGuidanceSection(req.Message, "a2a", guidance)
		} else {
			req.Message = req.Message + "\n\n" + guidance
		}
	}

	// Codex locks its system prompt to the backend-validated canonical instructions
	// (EnsureCodexPromptIntegrity resets it every request), so the static swarm-flow
	// capability cannot live in the system prompt like it does for other providers
	// (see LoadAndInjectContext). Advertise it ONCE — on the FIRST turn of a new
	// conversation only — never every turn. Interactive TUI + installed CLI only.
	if codexPromptIntegrity && !sdk.activeAgent().IsHeadless() && swarmFlowAvailable() &&
		!conversationHasAssistantTurn(conversationMessages) {
		req.Message = upsertSwarmRuntimeGuidanceSection(req.Message, "swarmflow", buildSwarmFlowGuidance())
	}

	sdk.logger.Info(ctx, "agent.execute.start",
		observability.F("conversation_id", convID),
		observability.F("model", model),
		observability.F("operating_mode", currentModeID),
	)

	// Fire UserPromptSubmit hook — equivalent to Claude Code's UserPromptSubmit / BeforeAgent.
	if sdk.hooksManager != nil {
		injectedCtx, hookResults, hookErr := sdk.hooksManager.EmitUserPromptSubmit(ctx, userMessage, convID)
		if hookErr != nil {
			return "", fmt.Errorf("prompt blocked by hook: %w", hookErr)
		}
		// Send hook results to TUI as BlockHook entries
		for _, hr := range hookResults {
			if updateChan != nil {
				select {
				case updateChan <- agent.HookExecutionUpdate{
					HookName: hr.HookName,
					ToolName: "user.prompt_submit",
					Phase:    "before",
					Success:  hr.Success,
					Output:   hr.Output,
					Blocked:  hr.Blocked,
					Error:    hr.Error,
				}:
				default:
				}
			}
		}

		// The "hook_context" injection source in the Context settings gates the
		// hook-injected context message (the hooks themselves still run — only
		// their prompt contribution is dropped).
		if injectedCtx != "" && !sdk.injectionEnabled(chatcontext.SourceIDHookContext) {
			sdk.logger.Info(ctx, "hooks.context_injection_disabled_by_context_settings",
				observability.F("dropped_length", len(injectedCtx)))
			injectedCtx = ""
		}
		if injectedCtx != "" {
			// Wrap in <system-reminder> so the model can distinguish hook-injected
			// context from the user's own words. FormatHookContext is a no-op if
			// the content is already wrapped (e.g. hooks that emit their own block).
			injectedCtx = sdkhooks.FormatHookContext("user_prompt_submit", injectedCtx)
			if codexPromptIntegrity {
				// Codex carries all context (skills, mode, hook) in req.Message so the
				// dedup check in agent_execute prevents a double-append.
				req.Message = req.Message + "\n\n" + injectedCtx
			} else {
				// Standard path: persist hook context as a standalone conversation message
				// so it is a first-class history entry — visible to the model, stored in
				// the DB, and clearly separate from the user's own words.
				hookMsg := &conversation.Message{
					ID:        fmt.Sprintf("hook_%d", time.Now().UnixNano()),
					Role:      conversation.RoleUser,
					Content:   injectedCtx,
					Timestamp: time.Now(),
					Metadata: map[string]any{
						"type":            "hook_context",
						"is_hook_context": true,
					},
				}
				conversationMessages = append(conversationMessages, hookMsg)
				// CRITICAL: req.ConversationHistory was set before the hook ran from the
				// old slice header. Any append that reallocates the backing array means
				// req.ConversationHistory no longer points to the current slice — sync it
				// back so the agent sees both the user message and the hook message.
				req.ConversationHistory = conversationMessages
				if saveErr := sdk.AddMessage(ctx, convID, hookMsg); saveErr != nil {
					logDebug("[SDK] Gate A: failed to persist hook context message: %v", saveErr)
				}
				// req.Message = "" tells agent_execute.go to skip re-appending a user
				// message; ConversationHistory already contains both the user message and
				// the hook-context message in the correct order.
				req.Message = ""
			}
		}
	}

	// Persist the user message.
	//
	// For Codex, the REQUEST-history entry (userMsg, already inside
	// conversationMessages == req.ConversationHistory) must carry the full injected
	// runtime guidance (skills + mode + capabilities + a2a + hook) so the provider
	// receives it this turn AND agent_execute's dedup skips a double-append. That is
	// EPHEMERAL to this request. The DURABLE store, however, must keep ONLY the
	// user's own words: baking req.Message into the persisted message made every
	// turn store its own ~5–32KB guidance copy, which conversation history then
	// re-sent on every subsequent turn (observed: 166KB across one 428-message
	// conversation). The next turn reloads clean history from disk, so guidance is
	// injected fresh each turn and never accumulates.
	if codexPromptIntegrity {
		userMsg.Content = req.Message // ephemeral: request history only
		persistMsg := userMsg.Clone()
		persistMsg.Content = userMessage // durable: clean user text (+ any attachments metadata)
		if err := sdk.AddMessage(ctx, convID, persistMsg); err != nil {
			return "", fmt.Errorf("failed to add user message: %w", err)
		}
	} else {
		userMsg.Content = userMessage
		if err := sdk.AddMessage(ctx, convID, userMsg); err != nil {
			return "", fmt.Errorf("failed to add user message: %w", err)
		}
	}

	// REMOVED: Token estimation code
	// Reason: All providers return REAL token counts in their responses.
	// Estimation caused bugs with 200x+ inflation for large tool results.
	// We now wait for accurate counts from API responses.
	// See: TOKEN_COUNT_FIX_PLAN.md

	resp, err := sdk.executeRequest(ctx, req)
	duration := time.Since(startTime)
	var retryOriginalErr error
	var retryErr error
	var retried bool
	if err != nil && codexPromptIntegrity && isCodexInstructionsInvalid(err) {
		retried = true
		retryOriginalErr = err

		prompt, changed := sdk.EnsureCodexPromptIntegrity(ctx, true)
		if prompt != "" {
			sdk.logger.Info(ctx, "codex.prompt.refreshed",
				observability.F("changed", changed),
			)
			resp, retryErr = sdk.executeRequest(ctx, req)
			duration = time.Since(startTime)
			if retryErr == nil {
				err = nil
				sdk.logger.Info(ctx, "codex.instructions_retry.succeeded")
			} else {
				err = retryErr
			}
		} else {
			sdk.logger.Warn(ctx, "codex.prompt.refresh_failed")
		}
	}

	sdk.activeAgent().SetMessageInjector(nil)

	// ─────────────────────────────────────────────────────────────
	// CACHE PROFILING: Log cache status after execution
	// ─────────────────────────────────────────────────────────────
	// Retrieve cache metrics from profiler to understand cache behavior
	// This helps identify if TUI-level repairs or injections affected cache
	if cacheProfiler != nil {
		cacheMetrics := cacheProfiler.Metrics(convID)
		if cacheMetrics != nil {
			sdk.logger.Info(ctx, "tui.cache_profiler.metrics",
				observability.F("conversation_id", convID),
				observability.F("cache_breaks", cacheMetrics.CacheBreaks),
				observability.F("total_turns", cacheMetrics.TotalTurns),
				observability.F("cache_hits", cacheMetrics.CacheHits),
				observability.F("hit_percent", fmt.Sprintf("%.1f%%", cacheMetrics.CacheHitPercent)),
			)
		}
	}

	if err != nil {
		sdk.logger.Error(ctx, "agent.execute.failed",
			observability.F("error", err.Error()),
		)
		if retried {
			sdk.logger.Error(ctx, "codex.instructions_retry.failed",
				observability.F("original_error", retryOriginalErr.Error()),
				observability.F("retry_error", err.Error()),
			)
		}

		// ── CRITICAL: Capture invalid_request errors with full conversation
		// structure so tool_use/tool_result pairing mismatches can be diagnosed
		// from Sentry alone. This fires for any Anthropic 400 with
		// "invalid_request_error" including "unexpected tool_use_id".
		errStr := err.Error()
		if strings.Contains(errStr, "invalid_request") ||
			strings.Contains(errStr, "unexpected tool_use_id") ||
			strings.Contains(errStr, "tool_result") {
			CaptureInvalidRequestError(ctx, err, sdk.GetProviderName(), model, convID, conversationMessages)
		}

		// Capture error in debug screen
		if sdk.debugScreen != nil && len(sdk.debugScreen.requests) > 0 {
			lastReq := &sdk.debugScreen.requests[len(sdk.debugScreen.requests)-1]
			if retried {
				lastReq.Error = fmt.Sprintf("codex invalid instructions retry failed; original=%s; retry=%s", retryOriginalErr.Error(), err.Error())
			} else {
				lastReq.Error = err.Error()
			}
			lastReq.Duration = duration
			lastReq.ResponseCode = 500
		}

		// Add error breadcrumb
		AddSentryBreadcrumb("agent.execution", "Agent execution failed", sentry.LevelError, map[string]any{
			"conversation_id": convID,
			"model":           model,
			"provider":        sdk.GetProviderName(),
			"error":           err.Error(),
			"retried":         retried,
		})

		// Create structured error for better Sentry categorization
		if retried {
			structuredErr := NewAgentExecutionErrorWithRetry(
				ctx,
				"codex_retry_failed",
				"Agent execution failed after Codex instructions retry",
				sdk.GetProviderName(),
				model,
				convID,
				retryOriginalErr,
				err,
			)
			return "", structuredErr
		}

		// Classify the error to determine the appropriate structured type
		structuredErr := ClassifyError(ctx, err, sdk.GetProviderName(), model, convID)
		if _, ok := structuredErr.(*AgentExecutionError); !ok {
			// Error was classified as something specific, return it
			return "", structuredErr
		}

		// Create generic agent execution error
		return "", NewAgentExecutionError(
			ctx,
			"execution_failed",
			err.Error(),
			sdk.GetProviderName(),
			model,
			convID,
			err,
		)
	}

	sdk.logger.Info(ctx, "agent.execute.completed",
		observability.F("conversation_id", convID),
		observability.F("turns", resp.TurnCount),
		observability.F("tokens", resp.TokensUsed),
		observability.F("duration_ms", resp.Duration.Milliseconds()),
	)

	// Add success breadcrumb
	AddSentryBreadcrumb("agent.execution", "Agent execution completed successfully", sentry.LevelInfo, map[string]any{
		"conversation_id": convID,
		"model":           model,
		"provider":        sdk.GetProviderName(),
		"turns":           resp.TurnCount,
		"tokens":          resp.TokensUsed,
		"duration_ms":     resp.Duration.Milliseconds(),
	})

	// Extract and track cache metrics from response
	if resp.Metadata != nil {
		if cacheMetrics, ok := resp.Metadata["cache_metrics"].(map[string]int); ok {
			sdk.lastCacheMetrics = cacheMetrics

			// Calculate totals (supports both legacy and new format)
			cacheCreation := cacheMetrics["cache_creation_tokens"]
			cacheCreation5m := cacheMetrics["cache_creation_5m_tokens"]
			cacheCreation1h := cacheMetrics["cache_creation_1h_tokens"]
			cacheRead := cacheMetrics["cache_read_tokens"]

			totalCreation := cacheCreation + cacheCreation5m + cacheCreation1h

			// Update session totals
			sdk.totalCacheCreation += totalCreation
			sdk.totalCacheRead += cacheRead

			// Calculate session hit rate
			totalCacheActivity := sdk.totalCacheCreation + sdk.totalCacheRead
			if totalCacheActivity > 0 {
				sdk.cacheHitRate = float64(sdk.totalCacheRead) / float64(totalCacheActivity) * 100
			}

			// Update global stats in cache manager
			if sdk.cacheManager != nil {
				if stats, ok := sdk.cacheManager.GetStats().(*CacheStats); ok {
					stats.TotalRequests++
					stats.TotalCreationTokens += int64(totalCreation)
					stats.TotalReadTokens += int64(cacheRead)

					// Calculate global hit rate
					totalGlobalActivity := stats.TotalCreationTokens + stats.TotalReadTokens
					if totalGlobalActivity > 0 {
						stats.TotalHitRate = float64(stats.TotalReadTokens) / float64(totalGlobalActivity) * 100
					}

					// Persist stats
					if err := sdk.cacheManager.SaveStats(); err != nil {
						logDebug("[CACHE] Failed to save stats: %v", err)
					}
				}
			}

			sdk.logger.Info(ctx, "agent.cache.metrics",
				observability.F("cache_creation", totalCreation),
				observability.F("cache_read", cacheRead),
				observability.F("hit_rate", fmt.Sprintf("%.1f%%", sdk.cacheHitRate)),
			)
		}
	}

	// Capture successful response in debug screen.
	// Write back to the EXACT request captured for this call (debugReqIndex)
	// rather than the last request, since nested calls may have appended other
	// requests in between — otherwise the JSON/Diff tabs show no response for
	// the request the user actually selected. Fall back to the last request if
	// the index was never set (e.g. capture path skipped).
	if sdk.debugScreen != nil {
		lastReq := sdk.debugScreen.requestPtr(debugReqIndex)
		if lastReq == nil && len(sdk.debugScreen.requests) > 0 {
			lastReq = &sdk.debugScreen.requests[len(sdk.debugScreen.requests)-1]
		}
		if lastReq != nil {

			// Capture provider JSON if available (shows exact API payload after translation)
			if debugProvider, ok := sdk.provider.(provider.DebugProvider); ok {
				if providerJSON := debugProvider.LastProviderJSON(); providerJSON != nil {
					lastReq.ProviderJSON = marshalForDebug(providerJSON)
				}
			}

			// Truncate response content for readability if very long
			responseContent := resp.Message
			contentTruncated := false
			if len(responseContent) > 5000 {
				responseContent = responseContent[:5000] + "\n...(response truncated for display, full content: " + fmt.Sprintf("%d chars", len(resp.Message)) + ")"
				contentTruncated = true
			}

			// Build detailed response with usage breakdown
			respBody := map[string]any{
				"id":   fmt.Sprintf("msg_%d", time.Now().UnixNano()),
				"type": "message",
				"role": "assistant",
				"_response_summary": map[string]any{
					"content_length":    len(resp.Message),
					"content_truncated": contentTruncated,
					"turns_taken":       resp.TurnCount,
					"duration_ms":       duration.Milliseconds(),
					"finish_reason":     string(resp.FinishReason),
				},
				"model":   model,
				"content": responseContent,
				"usage": map[string]any{
					"input_tokens":            resp.InputTokens,
					"output_tokens":           resp.OutputTokens,
					"total_tokens":            resp.TokensUsed,
					"_context_size_explainer": "input_tokens = full context sent to model (system prompt + tools + all messages)",
					"_output_explainer":       "output_tokens = tokens generated by this response only",
				},
			}

			// Add cache metrics if available
			if len(sdk.lastCacheMetrics) > 0 {
				respBody["cache_metrics"] = sdk.lastCacheMetrics
			}

			// Add cost info
			if resp.CostUSD > 0 {
				respBody["cost_usd"] = resp.CostUSD
			}

			respJSON, _ := json.MarshalIndent(respBody, "", "  ")
			lastReq.Response = string(respJSON)
			lastReq.ResponseCode = 200
			lastReq.Duration = duration
			lastReq.TokensInput = resp.InputTokens
			lastReq.TokensOutput = resp.OutputTokens

			// Update metadata with final stats
			lastReq.Metadata["final_input_tokens"] = resp.InputTokens
			lastReq.Metadata["final_output_tokens"] = resp.OutputTokens
			lastReq.Metadata["turns_taken"] = resp.TurnCount
		}
	}

	// CRITICAL: Explicitly update the conversation's CurrentContextSize
	// The AddMessage callback may not always set this correctly (e.g., if message has no tokens)
	// This ensures the conversation has the latest input_tokens for accurate side panel display
	logDebug("[SDK-TOKENS] Agent response: InputTokens=%d OutputTokens=%d TotalTokens=%d",
		resp.InputTokens, resp.OutputTokens, resp.TokensUsed)

	if strings.TrimSpace(resp.Message) != "" {
		if err := sdk.persistAssistantResponse(ctx, convID, model, resp); err != nil {
			sdk.logger.Warn(ctx, "assistant.message.persist_failed",
				observability.F("conversation_id", convID),
				observability.F("error", err.Error()))
		}
	}
	// The prebuilt TUI agent owns its permanent message callback, so the
	// unified Client.Execute boundary can run before this explicit persistence
	// fallback. Refresh once more after the final assistant message is guaranteed
	// on disk; the client method is version-idempotent when the callback already
	// generated metadata.
	if err := sdk.RefreshConversationMetadata(ctx, convID); err != nil {
		sdk.logger.Warn(ctx, "conversation.metadata_refresh_failed",
			observability.F("conversation_id", convID),
			observability.F("error", err.Error()))
	}

	if resp.InputTokens > 0 {
		if conv, err := sdk.resumeConv(ctx, convID); err == nil {
			prevContextSize := conv.CurrentContextSize
			logDebug("[SDK-TOKENS] Updating CurrentContextSize: prev=%d new=%d (from API InputTokens)", prevContextSize, resp.InputTokens)
			conv.UpdateContextSize(resp.InputTokens)
			if err := sdk.saveConv(ctx, conv); err != nil {
				logDebug("[SDK-TOKENS] Failed to save updated context size: %v", err)
			} else {
				logDebug("[SDK-TOKENS] Successfully saved conversation with CurrentContextSize=%d", conv.CurrentContextSize)
			}
		} else {
			logDebug("[SDK-TOKENS] Failed to resume conversation for context update: %v", err)
		}
	} else {
		logDebug("[SDK-TOKENS] WARNING: resp.InputTokens is 0, not updating CurrentContextSize")
	}

	// Emit agent stopped event to fire goal hook and other stop-related hooks.
	// Include the persisted turn trace so the goal evaluator can observe tool
	// calls and their results instead of seeing only prompt + final prose.
	if sdk.hooksManager != nil {
		messages, err := sdk.GetMessages(ctx, convID)
		if err != nil {
			sdk.logger.Warn(ctx, "goal.transcript_load_failed",
				observability.F("conversation_id", convID),
				observability.F("error", err.Error()))
		}
		transcript := hooksbuiltin.BuildGoalEvaluationTranscript(messages, userMessage, resp.Message)
		hookResults := sdk.hooksManager.EmitAgentStopped(ctx, convID, string(resp.FinishReason), userMessage, resp.Message, transcript)
		if len(hookResults) > 0 {
			sdk.logger.Info(ctx, "agent.stopped_hooks_fired",
				observability.F("count", len(hookResults)))
		}
		// /goal loop driver: the GoalHook above only EVALUATES the condition —
		// nothing re-prompts the agent when it is unmet, so without this the
		// goal "loop" ran exactly one turn and went silent.
		sdk.maybeContinueGoal(ctx)
	}

	return resp.Message, nil
}

// goalMaxIterations caps the /goal continuation loop as a runaway backstop.
// The evaluator decides when the goal is met; this only stops a goal that
// never converges. The user can always /goal clear.
const goalMaxIterations = 50

// scheduledTasksSummary returns a compact, human-readable listing of the
// harness's other active cron/scheduled work (CronCreate + ScheduleWakeup),
// or "" when there is none. maybeContinueGoal folds this into the /goal
// continuation prompt so the loop is aware other prompts may fire and
// interleave with its own turns — without it the goal loop had no idea the
// scheduler existed at all (cronScheduler was a constructor-local variable,
// never reachable from here) and could blindly re-drive work a cron job was
// already covering, or leave the agent confused when a [SCHEDULED] message
// arrived mid-loop.
func (sdk *SDKIntegration) scheduledTasksSummary() (string, int) {
	if sdk == nil || sdk.cronScheduler == nil {
		return "", 0
	}
	tasks := sdk.cronScheduler.ListTasks()
	if len(tasks) == 0 {
		return "", 0
	}
	var b strings.Builder
	count := 0
	for _, t := range tasks {
		if t == nil {
			continue
		}
		count++
		kind := "one-shot"
		if t.Recurring {
			kind = "recurring"
		}
		p := t.Prompt
		if len(p) > 80 {
			p = p[:77] + "..."
		}
		fmt.Fprintf(&b, "- [%s] %s (cron=%q, next fire %s): %q\n",
			t.ID, kind, t.Cron, t.NextFireAt.Format(time.RFC3339), p)
	}
	return b.String(), count
}

// maybeContinueGoal drives the /goal loop: when the active goal was evaluated
// as NOT met for the turn that just finished, re-prompt the main session via
// the same delivery path as fired cron prompts so the agent keeps working.
//
// Gated on GoalStateActive: an evaluator error leaves the state untouched
// (not_yet_evaluated / previous state), which stops the loop instead of
// spinning on broken evaluations.
func (sdk *SDKIntegration) maybeContinueGoal(ctx context.Context) {
	if sdk.hooksManager == nil || sdk.cronPromptSink == nil {
		return
	}
	gh := sdk.hooksManager.GetGoalHook()
	if gh == nil {
		return
	}
	g := gh.GetGoal()
	if g == nil || g.State != hooksbuiltin.GoalStateActive {
		return
	}
	if g.Iterations >= goalMaxIterations {
		sdk.logger.Warn(ctx, "goal.iteration_cap_reached",
			observability.F("condition", g.Condition),
			observability.F("iterations", g.Iterations))
		return
	}

	reason := g.LastReason
	if reason == "" {
		reason = "condition not yet satisfied"
	}

	// Fold in a summary of any other cron/scheduled tasks the harness has
	// registered so the loop's re-prompt does not pretend it is the only
	// thing running — the agent should avoid duplicating scheduled work and
	// should expect [SCHEDULED] messages to interleave with this loop.
	scheduledNote := ""
	scheduledCount := 0
	if summary, count := sdk.scheduledTasksSummary(); summary != "" {
		scheduledCount = count
		scheduledNote = fmt.Sprintf("\n\nNote: the harness also has %d other scheduled/cron task(s) active in this session. They may fire independently and interleave as [SCHEDULED] messages — do not duplicate their work:\n%s",
			scheduledCount, summary)
	}

	prompt := fmt.Sprintf(`Goal check after the last turn: NOT yet met.
Goal: %s
Evaluator: %s
%s
Continue working toward this goal now. This loop repeats after every turn until the evaluator confirms the goal is met.`,
		g.Condition, reason, scheduledNote)

	if err := sdk.cronPromptSink.EnqueuePrompt(ctx, prompt); err != nil {
		sdk.logger.Warn(ctx, "goal.continuation_enqueue_failed",
			observability.F("error", err.Error()))
		return
	}
	sdk.logger.Info(ctx, "goal.continuation_enqueued",
		observability.F("condition", g.Condition),
		observability.F("iteration", g.Iterations),
		observability.F("active_scheduled_tasks", scheduledCount))
}

func (sdk *SDKIntegration) persistAssistantResponse(ctx context.Context, convID, model string, resp *agent.ExecuteResponse) error {
	if sdk == nil || resp == nil || strings.TrimSpace(resp.Message) == "" {
		return nil
	}
	if conv, err := sdk.resumeConv(ctx, convID); err == nil {
		for i := len(conv.Messages) - 1; i >= 0; i-- {
			msg := conv.Messages[i]
			if msg == nil {
				continue
			}
			if msg.Role == conversation.RoleAssistant && msg.Content == resp.Message {
				return nil
			}
		}
	}

	usage := &conversation.TokenUsage{
		Input:  resp.InputTokens,
		Output: resp.OutputTokens,
		Total:  resp.TokensUsed,
	}
	assistantMsg := &conversation.Message{
		ID:        fmt.Sprintf("msg_%d", time.Now().UnixNano()),
		Timestamp: time.Now(),
		Role:      conversation.RoleAssistant,
		Content:   resp.Message,
		Provider:  sdk.GetProviderName(),
		Model:     model,
		Tokens:    usage,
		Metadata: map[string]any{
			"finish_reason": string(resp.FinishReason),
			"turn_count":    resp.TurnCount,
			"duration_ms":   time.Since(sdk.lastReqStart).Milliseconds(),
		},
	}
	return sdk.AddMessage(ctx, convID, assistantMsg)
}

// StreamResponse handles streaming chat and returns a channel of chunks (DEPRECATED - use ExecuteMessage)
func (sdk *SDKIntegration) StreamResponse(ctx context.Context, convID string, userMessage string, model string, messageIndex int) (<-chan provider.StreamChunk, error) {
	startTime := time.Now()
	sdk.lastReqStart = startTime

	// Add user message to conversation
	userMsg := &conversation.Message{
		ID:        fmt.Sprintf("msg_%d", time.Now().UnixNano()),
		Timestamp: time.Now(),
		Role:      conversation.RoleUser,
		Content:   userMessage,
	}

	err := sdk.AddMessage(ctx, convID, userMsg)
	if err != nil {
		return nil, fmt.Errorf("failed to add user message: %w", err)
	}

	// Get all messages for context
	messages, err := sdk.GetMessages(ctx, convID)
	if err != nil {
		return nil, fmt.Errorf("failed to get conversation history: %w", err)
	}

	// Get all registered tools and convert to provider.Tool format
	toolList := sdk.getToolsForRequest()

	// Create chat request
	req := provider.ChatRequest{
		Messages:  messages,
		Model:     model,
		MaxTokens: intPtr(sdk.GetMaxTokens()),
		Tools:     toolList,
	}

	// Capture request for debugging
	if sdk.debugScreen != nil {
		debugReq := sdk.captureRequest(req, model, messageIndex, startTime)
		sdk.debugScreen.AddRequest(debugReq)
	}

	// Start streaming from provider
	logDebug("StreamResponse: About to call provider.Stream with model=%s, messages=%d", model, len(messages))
	chunkChan, err := sdk.provider.Stream(ctx, req)
	if err != nil {
		logDebug("StreamResponse: provider.Stream returned error: %v", err)
		// Capture error in debug screen
		if sdk.debugScreen != nil {
			sdk.captureError(err, messageIndex, time.Since(startTime))
		}
		return nil, fmt.Errorf("failed to start stream: %w", err)
	}

	logDebug("StreamResponse: provider.Stream successful, returning channel")
	sdk.logger.Info(ctx, "stream.started",
		observability.F("conversation_id", convID),
		observability.F("model", model),
		observability.F("message_count", len(messages)),
	)

	return chunkChan, nil
}

// evaluateGoalWithLLM asks a separate provider call whether the requested goal
// state is supported by observable evidence from the final turn.
func evaluateGoalWithLLM(ctx context.Context, prov provider.Provider, model, condition, transcript string) (hooksbuiltin.GoalResult, error) {
	userMsg := fmt.Sprintf("Goal condition: %s\n\nTranscript:\n%s", condition, transcript)

	msg := &conversation.Message{Role: conversation.RoleUser, Content: userMsg}
	resp, err := prov.Chat(ctx, provider.ChatRequest{
		SystemPrompt: goalEvaluatorSystemPrompt(),
		Messages:     []*conversation.Message{msg},
		Model:        model,
	})
	if err != nil {
		return hooksbuiltin.GoalResult{Ok: false, Reason: "evaluator error: " + err.Error()}, nil
	}
	if resp == nil || resp.Message == nil {
		return hooksbuiltin.GoalResult{Ok: false, Reason: "evaluator returned empty response"}, nil
	}
	return parseGoalVerdict(resp.Message.Content), nil
}

func goalEvaluatorSystemPrompt() string {
	return `You are an independent goal-completion verifier. Decide whether the requested goal state is proven by observable evidence in the most recent transcript.

Assistant claims are not evidence. Saying "done", describing an intended change, or presenting a plausible plan/diff does not establish the state. Text appearing only in help, usage, error, or log output is not proof that a command succeeded or a program is actually running. A failed command is never completion.

Return MET only when the transcript contains direct evidence of the observable requested state, such as passing targeted tests, successful execution output, or captured final UI/process state. When evidence is absent or ambiguous, return NOT_MET and state the missing verification.

Reply with EXACTLY one of:
  MET: <one-sentence evidence-based reason>
  NOT_MET: <one-sentence missing-evidence reason>
  IMPOSSIBLE: <one-sentence reason>

No other text.`
}

func parseGoalVerdict(reply string) hooksbuiltin.GoalResult {
	text := strings.TrimSpace(reply)
	up := strings.ToUpper(text)
	switch {
	case strings.Contains(up, "IMPOSSIBLE"):
		return hooksbuiltin.GoalResult{Ok: false, Impossible: true, Reason: goalVerdictReason(text, "IMPOSSIBLE")}
	case strings.Contains(up, "NOT_MET"), strings.Contains(up, "NOT MET"), strings.Contains(up, "UNMET"):
		return hooksbuiltin.GoalResult{Ok: false, Reason: goalVerdictReason(text, "NOT_MET")}
	case strings.Contains(up, "MET"):
		return hooksbuiltin.GoalResult{Ok: true, Reason: goalVerdictReason(text, "MET")}
	default:
		return hooksbuiltin.GoalResult{Ok: false, Reason: "unparseable evaluator reply: " + truncateForLog(text, 120)}
	}
}

func goalVerdictReason(text, verdict string) string {
	up := strings.ToUpper(text)
	if idx := strings.Index(up, verdict+":"); idx >= 0 {
		return strings.TrimSpace(text[idx+len(verdict)+1:])
	}
	return strings.TrimSpace(text)
}
