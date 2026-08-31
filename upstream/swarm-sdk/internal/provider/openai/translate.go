package openai

import (
	"encoding/json"
	"fmt"
	"maps"
	"strings"
	"time"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/conversation"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/provider"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/vision"
)

// requiresMaxCompletionTokens returns true if the model uses max_completion_tokens
// instead of max_tokens. This applies to o1, o3, and gpt-5+ models.
func requiresMaxCompletionTokens(model string) bool {
	modelLower := strings.ToLower(model)
	return strings.HasPrefix(modelLower, "o1") ||
		strings.HasPrefix(modelLower, "o3") ||
		strings.HasPrefix(modelLower, "gpt-5")
}

// isGLMModel checks if a model is a GLM (ZhipuAI) model.
// GLM models support the reasoning_content field in both requests and responses.
func isGLMModel(model string) bool {
	modelLower := strings.ToLower(model)
	return strings.Contains(modelLower, "glm") ||
		strings.Contains(modelLower, "zhipu")
}

// IsMessageValid checks if a message has valid content for the OpenAI API.
// A message is valid if it has:
// - Non-empty content, OR
// - At least one tool call (for assistant messages), OR
// - At least one tool result (for tool messages)
//
// This function can be used to filter out invalid messages before translation.
func IsMessageValid(msg *conversation.Message) bool {
	if msg == nil {
		return false
	}

	// Check for non-empty content
	if strings.TrimSpace(msg.Content) != "" {
		return true
	}

	// Check for tool calls (assistant requesting tool execution)
	if len(msg.ToolCalls) > 0 {
		return true
	}

	// Check for tool results (tool execution results)
	if len(msg.ToolResults) > 0 {
		return true
	}

	// Check for thinking content in metadata (can substitute for content)
	if msg.Metadata != nil {
		if thinking, ok := msg.Metadata["thinking"].(string); ok && strings.TrimSpace(thinking) != "" {
			return true
		}
		if reasoning, ok := msg.Metadata["reasoning_content"].(string); ok && strings.TrimSpace(reasoning) != "" {
			return true
		}
	}

	// Check for thinking in dedicated field
	if strings.TrimSpace(msg.Thinking) != "" {
		return true
	}

	return false
}

// SanitizeMessage attempts to repair an invalid message to make it valid.
// If the message cannot be repaired, it returns nil.
// This is useful for handling edge cases like truncated responses.
//
// Repair strategies:
// 1. If message has thinking/reasoning content but no main content, use thinking as content
// 2. If message is completely empty (from finish_reason=length), add a placeholder
// 3. For assistant messages with no content, add a minimal placeholder to maintain conversation flow
func SanitizeMessage(msg *conversation.Message) *conversation.Message {
	if msg == nil {
		return nil
	}

	// Already valid, no changes needed
	if IsMessageValid(msg) {
		return msg
	}

	// Create a copy to avoid modifying the original
	sanitized := &conversation.Message{
		ID:          msg.ID,
		Timestamp:   msg.Timestamp,
		Role:        msg.Role,
		Provider:    msg.Provider,
		Model:       msg.Model,
		ToolCalls:   msg.ToolCalls,
		ToolResults: msg.ToolResults,
		Tokens:      msg.Tokens,
		Thinking:    msg.Thinking,
	}

	// Copy metadata
	if msg.Metadata != nil {
		sanitized.Metadata = make(map[string]any)
		maps.Copy(sanitized.Metadata, msg.Metadata)
	}

	// Strategy 1: Use thinking/reasoning content as main content
	if sanitized.Thinking != "" {
		sanitized.Content = sanitized.Thinking
		return sanitized
	}

	if sanitized.Metadata != nil {
		if thinking, ok := sanitized.Metadata["thinking"].(string); ok && thinking != "" {
			sanitized.Content = thinking
			return sanitized
		}
		if reasoning, ok := sanitized.Metadata["reasoning_content"].(string); ok && reasoning != "" {
			sanitized.Content = reasoning
			return sanitized
		}
	}

	// Strategy 2: For empty assistant messages (likely from finish_reason=length),
	// add a placeholder that indicates the response was truncated
	if sanitized.Role == conversation.RoleAssistant {
		sanitized.Content = "[Response truncated due to token limit]"
		if sanitized.Metadata == nil {
			sanitized.Metadata = make(map[string]any)
		}
		sanitized.Metadata["truncated"] = true
		sanitized.Metadata["sanitized"] = true
		return sanitized
	}

	// Strategy 3: For other roles with no content and no tools, we cannot repair
	// Return nil to indicate this message should be skipped
	return nil
}

// SanitizeMessages filters and repairs messages to ensure they are all valid for the API.
// It returns a new slice with only valid messages.
// Invalid messages that cannot be repaired are logged and skipped.
func SanitizeMessages(messages []*conversation.Message) []*conversation.Message {
	result := make([]*conversation.Message, 0, len(messages))

	for _, msg := range messages {
		if msg == nil {
			continue
		}

		// Check if already valid
		if IsMessageValid(msg) {
			result = append(result, msg)
			continue
		}

		// Try to sanitize
		sanitized := SanitizeMessage(msg)
		if sanitized != nil {
			result = append(result, sanitized)
		}
		// If sanitization returns nil, the message is skipped
	}

	return result
}

// repairOrphanedToolCalls ensures every assistant message with tool_calls
// has matching tool result messages immediately after.
// This is critical for cross-provider compatibility since providers like Gemini
// allow tool call interruptions but OpenAI-compatible providers may require strict pairing.
func repairOrphanedToolCalls(messages []OpenAIMessage) []OpenAIMessage {
	if len(messages) == 0 {
		return messages
	}

	result := make([]OpenAIMessage, 0, len(messages))

	for i := range messages {
		msg := messages[i]
		result = append(result, msg)

		// Only check assistant messages for tool_calls
		if msg.Role != "assistant" || len(msg.ToolCalls) == 0 {
			continue
		}

		// Extract tool call IDs and names from this message
		toolCallIDs := make(map[string]string) // id -> name
		for _, tc := range msg.ToolCalls {
			toolCallIDs[tc.ID] = tc.Function.Name
		}

		// Look ahead for tool result messages
		j := i + 1
		for j < len(messages) && messages[j].Role == "tool" {
			if messages[j].ToolCallID != nil {
				delete(toolCallIDs, *messages[j].ToolCallID)
			}
			j++
		}

		// Synthesize missing tool results
		for id, name := range toolCallIDs {
			idCopy := id // Create copy for pointer
			syntheticMsg := OpenAIMessage{
				Role:       "tool",
				ToolCallID: &idCopy,
				Content:    fmt.Sprintf("[Tool execution interrupted - %s was cancelled before completion]", name),
			}
			result = append(result, syntheticMsg)
		}
	}

	return result
}

// TranslateRequest converts canonical format to OpenAI native format.
func TranslateRequest(req provider.ChatRequest) (*ChatCompletionRequest, error) {
	openaiReq := &ChatCompletionRequest{
		Model:       req.Model,
		Messages:    make([]OpenAIMessage, 0, len(req.Messages)),
		Temperature: req.Temperature,
		TopP:        req.TopP,
		Stream:      false,
	}

	if req.TopP != nil && (*req.TopP < 0.0 || *req.TopP > 1.0) {
		return nil, fmt.Errorf("top_p must be between 0.0 and 1.0, got %.2f", *req.TopP)
	}

	// Handle max_tokens vs max_completion_tokens based on model
	if req.MaxTokens != nil {
		if requiresMaxCompletionTokens(req.Model) {
			openaiReq.MaxCompletionTokens = req.MaxTokens
		} else {
			openaiReq.MaxTokens = req.MaxTokens
		}
	}

	// GPT-5+ Verbosity parameter
	if req.Verbosity != "" {
		if openaiReq.Text == nil {
			openaiReq.Text = &TextOptions{}
		}
		openaiReq.Text.Verbosity = req.Verbosity
	}

	// Reasoning effort aliases are normalized and "auto" is omitted so only
	// provider-valid values reach the wire. GPT-5 uses nested reasoning.effort;
	// OpenAI-compatible models use the legacy top-level reasoning_effort field.
	if effort := provider.NormalizeReasoningEffort(req.ReasoningEffort); effort != "" {
		modelLower := strings.ToLower(req.Model)
		if strings.HasPrefix(modelLower, "gpt-5") {
			// "max" exists only on the gpt-5.6 family; clamp it elsewhere so a
			// stored max/ultra preference doesn't 400 on older models.
			if effort == provider.ReasoningEffortMax && !strings.HasPrefix(modelLower, "gpt-5.6") {
				effort = provider.ReasoningEffortXHigh
			}
			openaiReq.Reasoning = &ReasoningOptions{Effort: effort}
		} else {
			openaiReq.ReasoningEffort = &effort
		}
	}

	// Cross-provider thinking support (from metadata)
	if req.Metadata != nil {
		if thinkingEnabled, ok := req.Metadata["thinking_enabled"].(bool); ok && thinkingEnabled {
			// Get thinking budget (default 10000)
			budget := 10000
			if thinkingBudget, ok := req.Metadata["thinking_budget"].(int); ok {
				budget = thinkingBudget
			}

			// Translate to OpenAI reasoning based on model
			modelLower := strings.ToLower(req.Model)

			// Map budget to effort level
			// GPT-5 uses "minimal", "medium", "high"
			// Cerebras uses "low", "medium", "high"
			var effort string
			if budget >= 4096 {
				effort = "high"
			} else if budget < 2048 {
				effort = "low" // "minimal" for GPT-5, "low" for Cerebras
			} else {
				effort = "medium"
			}

			// For GPT-5+: Use reasoning.effort parameter
			if strings.HasPrefix(modelLower, "gpt-5") {
				if effort == "low" {
					effort = "minimal"
				}
				if openaiReq.Reasoning == nil {
					openaiReq.Reasoning = &ReasoningOptions{Effort: effort}
				}
			} else if openaiReq.ReasoningEffort == nil {
				// For other models (Cerebras, etc.) that support thinking,
				// derive the top-level effort only when no explicit value exists.
				openaiReq.ReasoningEffort = &effort
			}
			// For o1/o3: Reasoning is automatic, captured in response
			// No request parameters needed - just note in metadata
		}
	}

	// Add stop sequences if present
	if len(req.StopSequences) > 0 {
		openaiReq.Stop = req.StopSequences
	}

	// IMPORTANT: Sanitize messages before translation to handle edge cases
	// This ensures we don't fail on empty messages from truncated responses
	sanitizedMessages := SanitizeMessages(provider.PrepareMessagesForLLM(req.Messages))

	// Translate messages
	for _, msg := range sanitizedMessages {
		if msg == nil {
			continue
		}

		// Handle multiple tool results in a single message
		// OpenAI requires each tool result to be a separate message
		if msg.Role == conversation.RoleTool && len(msg.ToolResults) > 1 {
			for _, result := range msg.ToolResults {
				// Create a temporary message for each result
				// We create a new message with just this single result
				tempMsg := &conversation.Message{
					Role:        conversation.RoleTool,
					ToolResults: []conversation.ToolResult{result},
					// Copy other fields if necessary, though for tool results usually only Role/Results matter
					Content:   msg.Content,
					Timestamp: msg.Timestamp,
					Provider:  msg.Provider,
					Model:     msg.Model,
				}

				openaiMsg, err := TranslateMessage(tempMsg, req.Model)
				if err != nil {
					return nil, fmt.Errorf("failed to translate tool result message: %w", err)
				}
				openaiReq.Messages = append(openaiReq.Messages, openaiMsg)
			}
			continue
		}

		openaiMsg, err := TranslateMessage(msg, req.Model)
		if err != nil {
			return nil, fmt.Errorf("failed to translate message: %w", err)
		}
		openaiReq.Messages = append(openaiReq.Messages, openaiMsg)
	}

	// Add system prompt as first message if present
	if req.SystemPrompt != "" {
		systemMsg := OpenAIMessage{
			Role:    "system",
			Content: req.SystemPrompt,
		}
		// Insert at the beginning
		openaiReq.Messages = append([]OpenAIMessage{systemMsg}, openaiReq.Messages...)
	}

	// CRITICAL: Repair orphaned tool calls before sending to API
	// This ensures every tool_call has a matching tool result, which is required
	// for cross-provider compatibility (e.g., conversations from Gemini/Anthropic)
	openaiReq.Messages = repairOrphanedToolCalls(openaiReq.Messages)

	// Translate tools if present
	if len(req.Tools) > 0 {
		openaiReq.Tools = make([]Tool, len(req.Tools))
		hasCustomTools := false
		for i, tool := range req.Tools {
			openaiReq.Tools[i] = TranslateTool(tool)
			if tool.Type == "custom" {
				hasCustomTools = true
			}
		}

		// Disable parallel tool calls for custom tools (GPT-5 requirement)
		if hasCustomTools {
			parallel := false
			openaiReq.ParallelToolCalls = &parallel
		}
	}

	return openaiReq, nil
}

// TranslateMessage converts canonical message to OpenAI message.
// The message should be validated/sanitized before calling this function.
// TranslateMessage converts a canonical message to OpenAI format.
// The model parameter is used to determine provider-specific field inclusion.
func TranslateMessage(msg *conversation.Message, model string) (OpenAIMessage, error) {
	openaiMsg := OpenAIMessage{
		Role: string(msg.Role),
	}

	// Handle content - check multiple sources
	content := strings.TrimSpace(msg.Content)

	// Check for images in metadata
	hasImages := vision.HasImages(msg.Metadata)

	// If no direct content, try thinking/reasoning as fallback
	if content == "" {
		// Check dedicated Thinking field
		if msg.Thinking != "" {
			content = msg.Thinking
		}
		// Check metadata for thinking/reasoning
		if content == "" && msg.Metadata != nil {
			if thinking, ok := msg.Metadata["thinking"].(string); ok && thinking != "" {
				content = thinking
			} else if reasoning, ok := msg.Metadata["reasoning_content"].(string); ok && reasoning != "" {
				content = reasoning
			}
		}
	}

	// Handle multimodal content (text + images)
	if hasImages {
		// Extract images from metadata
		images, err := vision.ExtractImagesFromMetadata(msg.Metadata)
		if err != nil {
			return openaiMsg, fmt.Errorf("failed to extract images from metadata: %w", err)
		}

		// Build content parts array
		contentParts := make([]ContentPart, 0)

		// Add text part if present
		if content != "" {
			contentParts = append(contentParts, ContentPart{
				Type: "text",
				Text: content,
			})
		}

		// Add image parts
		for _, img := range images {
			if img.Type == "base64" {
				// Convert to data URL format: data:image/png;base64,{data}
				dataURL := fmt.Sprintf("data:%s;base64,%s", img.MediaType, img.Data)
				contentParts = append(contentParts, ContentPart{
					Type: "image_url",
					ImageURL: &ImageURL{
						URL: dataURL,
					},
				})
			} else if img.Type == "url" {
				// Use URL directly
				contentParts = append(contentParts, ContentPart{
					Type: "image_url",
					ImageURL: &ImageURL{
						URL: img.URL,
					},
				})
			}
		}

		// Set content as array of parts
		openaiMsg.Content = contentParts
	} else if content != "" {
		// Simple string content (no images)
		openaiMsg.Content = content
	} else if len(msg.ToolCalls) == 0 && len(msg.ToolResults) == 0 {
		// No content and no tool calls/results - this is an invalid message
		// Instead of returning an error, add a placeholder for robustness
		// This handles edge cases from various models and finish reasons
		if msg.Role == conversation.RoleAssistant {
			openaiMsg.Content = "[Response truncated]"
		} else {
			// For other roles, we still need to return an error as we can't safely add a placeholder
			return openaiMsg, fmt.Errorf("message has no content and no tool calls/results")
		}
	}

	// Handle tool calls (assistant requesting tool execution)
	if len(msg.ToolCalls) > 0 {
		openaiMsg.ToolCalls = make([]ToolCall, len(msg.ToolCalls))
		for i, tc := range msg.ToolCalls {
			openaiMsg.ToolCalls[i] = TranslateToolCall(tc)
		}
	}

	// Include reasoning/thinking fields for specific providers only
	// This preserves thinking context across conversation turns
	// CRITICAL: Only include these fields for providers that support them to avoid API rejection
	if msg.Role == conversation.RoleAssistant {
		// GLM models use reasoning_content field
		if isGLMModel(model) {
			if msg.Thinking != "" {
				openaiMsg.ReasoningContent = msg.Thinking
			} else if msg.Metadata != nil {
				if reasoning, ok := msg.Metadata["reasoning_content"].(string); ok && reasoning != "" {
					openaiMsg.ReasoningContent = reasoning
				} else if thinking, ok := msg.Metadata["thinking"].(string); ok && thinking != "" {
					openaiMsg.ReasoningContent = thinking
				}
			}
		}
		// Cerebras models use reasoning field
		// Note: Currently not setting this in messages, only in response parsing
	}

	// Handle tool results (tool execution results)
	if len(msg.ToolResults) > 0 {
		// OpenAI expects tool results as separate messages with role "tool"
		// This should be handled at a higher level, but we'll note it here
		if msg.Role == conversation.RoleTool && len(msg.ToolResults) > 0 {
			// Use the first tool result
			result := msg.ToolResults[0]
			openaiMsg.ToolCallID = &result.CallID
			openaiMsg.Content = result.Output
		}
	}

	return openaiMsg, nil
}

// TranslateToolCall converts canonical tool call to OpenAI format.
func TranslateToolCall(tc conversation.ToolCall) ToolCall {
	// Marshal parameters to JSON string
	argsJSON, _ := json.Marshal(tc.Parameters)

	return ToolCall{
		ID:   tc.ID,
		Type: "function",
		Function: FunctionCall{
			Name:      tc.Name,
			Arguments: string(argsJSON),
		},
	}
}

// TranslateTool converts canonical tool to OpenAI format.
func TranslateTool(tool provider.Tool) Tool {
	// Handle custom tools (GPT-5+ freeform function calling)
	if tool.Type == "custom" {
		openaiTool := Tool{
			Type:        "custom",
			Name:        tool.Name,
			Description: tool.Description,
		}

		// Add format constraints if specified
		if tool.Format != nil {
			openaiTool.Format = &ToolFormat{
				Type:       tool.Format.Type,
				Syntax:     tool.Format.Syntax,
				Definition: tool.Format.Definition,
			}
		}

		return openaiTool
	}

	// Handle traditional function calling (structured JSON)
	params, ok := provider.NormalizeToolParametersSchema(tool.Parameters).(map[string]any)
	if !ok || params == nil {
		params = map[string]any{
			"type":       "object",
			"properties": map[string]any{},
		}
	}

	return Tool{
		Type: "function",
		Function: &Function{
			Name:        tool.Name,
			Description: tool.Description,
			Parameters:  params,
		},
	}
}

// TranslateResponse converts OpenAI response to canonical format.
func TranslateResponse(resp *ChatCompletionResponse) (*provider.ChatResponse, error) {
	if len(resp.Choices) == 0 {
		return nil, fmt.Errorf("no choices in response")
	}

	// Take first choice (TODO: handle N > 1)
	choice := resp.Choices[0]

	// Create canonical message
	canonicalMsg := &conversation.Message{
		ID:        resp.ID,
		Timestamp: time.Unix(resp.Created, 0),
		Role:      conversation.Role(choice.Message.Role),
		Provider:  "openai",
		Model:     resp.Model,
	}

	// Extract content
	var textParts []string
	var reasoningParts []string

	switch content := choice.Message.Content.(type) {
	case string:
		if strings.TrimSpace(content) != "" {
			textParts = append(textParts, content)
		}
	case []any:
		for _, rawPart := range content {
			partMap, ok := rawPart.(map[string]any)
			if !ok {
				continue
			}

			partType, _ := partMap["type"].(string)
			partType = strings.ToLower(partType)
			textVal, _ := partMap["text"].(string)

			switch partType {
			case "reasoning", "reasoning_content", "analysis", "thinking", "chain_of_thought":
				if textVal != "" {
					reasoningParts = append(reasoningParts, textVal)
				} else if reason, ok := partMap["content"].(string); ok && reason != "" {
					reasoningParts = append(reasoningParts, reason)
				}
			default:
				if textVal != "" {
					textParts = append(textParts, textVal)
				} else if contentVal, ok := partMap["content"].(string); ok && contentVal != "" {
					textParts = append(textParts, contentVal)
				}

				// If an unknown type still clearly looks like reasoning, capture it
				if (partType == "" || strings.Contains(partType, "reason")) && textVal != "" {
					reasoningParts = append(reasoningParts, textVal)
				}
			}
		}
	}

	canonicalMsg.Content = strings.TrimSpace(strings.Join(textParts, "\n\n"))

	// Extract reasoning content (o1/o3/GLM models)
	// Store in metadata similar to Anthropic's thinking
	if choice.Message.Reasoning != "" {
		reasoningParts = append(reasoningParts, choice.Message.Reasoning)
	}
	if choice.Message.ReasoningContent != "" {
		reasoningParts = append(reasoningParts, choice.Message.ReasoningContent)
	}

	if len(reasoningParts) > 0 {
		reasoningText := strings.TrimSpace(strings.Join(reasoningParts, "\n\n"))
		if reasoningText != "" {
			if canonicalMsg.Metadata == nil {
				canonicalMsg.Metadata = make(map[string]any)
			}
			canonicalMsg.Thinking = reasoningText // Store in first-class field
			canonicalMsg.Metadata["thinking"] = reasoningText
			canonicalMsg.Metadata["reasoning_content"] = reasoningText

			// If content is empty but we have reasoning, use reasoning as content
			// so reasoning-only responses don't come back empty — EXCEPT when
			// generation was truncated (finish_reason "length"): then the
			// reasoning is an unfinished chain-of-thought that stopped before
			// the answer was emitted, and substituting it hands raw CoT to
			// consumers as if it were the reply (observed: a 4096-token cap
			// truncated a kimi reasoning model mid-thought and the monologue
			// was committed verbatim as a commit message downstream).
			if canonicalMsg.Content == "" && choice.FinishReason != "length" {
				canonicalMsg.Content = reasoningText
			}
		}
	}

	// Handle tool calls
	if len(choice.Message.ToolCalls) > 0 {
		canonicalMsg.ToolCalls = make([]conversation.ToolCall, len(choice.Message.ToolCalls))
		for i, tc := range choice.Message.ToolCalls {
			canonicalMsg.ToolCalls[i] = TranslateToolCallFromOpenAI(tc)
		}
	}

	// Create token usage - supports both OpenAI and Anthropic-style formats.
	// When prompt_tokens_details.cached_tokens is reported, split the prompt
	// budget into cached vs uncached to match the Anthropic convention used
	// elsewhere in the SDK (Input = uncached delta, CacheRead = cached portion).
	var tokenUsage *conversation.TokenUsage
	inputTokens := resp.Usage.InputCount()
	outputTokens := resp.Usage.OutputCount()
	totalTokens := resp.Usage.TotalCount()
	cachedTokens := resp.Usage.CachedTokens()

	if totalTokens > 0 || inputTokens > 0 || outputTokens > 0 {
		uncached := inputTokens
		if cachedTokens > 0 && cachedTokens <= inputTokens {
			uncached = inputTokens - cachedTokens
		}
		tokenUsage = &conversation.TokenUsage{
			Input:     uncached,
			Output:    outputTokens,
			Total:     totalTokens,
			CacheRead: cachedTokens,
		}
	}
	canonicalMsg.Tokens = tokenUsage

	// Map finish reason
	finishReason := MapFinishReason(choice.FinishReason)

	// DEFENSIVE: Override finish reason if there are tool calls
	// Some providers report "stop" even when they want to call tools
	// This matches Gemini provider behavior for cross-provider consistency
	if len(canonicalMsg.ToolCalls) > 0 && finishReason != provider.FinishReasonToolCalls {
		finishReason = provider.FinishReasonToolCalls
	}

	// IMPORTANT: Handle truncated responses (finish_reason=length)
	// If the response was truncated and we have no content, mark it appropriately
	if finishReason == provider.FinishReasonLength && canonicalMsg.Content == "" && len(canonicalMsg.ToolCalls) == 0 {
		canonicalMsg.Content = "[Response truncated due to token limit]"
		if canonicalMsg.Metadata == nil {
			canonicalMsg.Metadata = make(map[string]any)
		}
		canonicalMsg.Metadata["truncated"] = true
		canonicalMsg.Metadata["finish_reason"] = "length"
	}

	return &provider.ChatResponse{
		Message:      canonicalMsg,
		FinishReason: finishReason,
		Usage:        tokenUsage,
		RawResponse:  resp,
	}, nil
}

// TranslateToolCallFromOpenAI converts OpenAI tool call to canonical format.
func TranslateToolCallFromOpenAI(tc ToolCall) conversation.ToolCall {
	// Parse arguments JSON string to map
	var params map[string]any
	if tc.Function.Arguments != "" {
		// Best-effort parse; nil params handled below
		_ = json.Unmarshal([]byte(tc.Function.Arguments), &params)
	}
	if params == nil {
		params = make(map[string]any)
	}

	return conversation.ToolCall{
		ID:         tc.ID,
		Name:       tc.Function.Name,
		Parameters: params,
	}
}

// MapFinishReason maps OpenAI finish reason to canonical format.
func MapFinishReason(reason string) provider.FinishReason {
	switch reason {
	case "stop":
		return provider.FinishReasonStop
	case "length":
		return provider.FinishReasonLength
	case "tool_calls", "function_call":
		return provider.FinishReasonToolCalls
	case "content_filter":
		return provider.FinishReasonContentFilter
	default:
		return provider.FinishReasonStop
	}
}
