package minimax

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/conversation"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/observability"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/provider"
)

// MessageRequest represents a request to the MiniMax Messages API.
// MiniMax uses Anthropic-compatible format.
type MessageRequest struct {
	Model       string          `json:"model"`
	Messages    []Message       `json:"messages"`
	MaxTokens   int             `json:"max_tokens"`
	System      any             `json:"system,omitempty"`      // string or []SystemBlock
	Temperature *float64        `json:"temperature,omitempty"` // 0.0-1.0
	TopP        *float64        `json:"top_p,omitempty"`       // 0.0-1.0
	Stream      bool            `json:"stream,omitempty"`
	Tools       []Tool          `json:"tools,omitempty"`
	ToolChoice  any             `json:"tool_choice,omitempty"` // auto, any, tool, or nil
	Thinking    *ThinkingConfig `json:"thinking,omitempty"`    // Extended/Interleaved Thinking
}

// Message represents a single message in the conversation.
type Message struct {
	Role    string `json:"role"`    // "user" or "assistant"
	Content any    `json:"content"` // string or []ContentBlock
}

// ContentBlock represents a content block in a message (multimodal support).
type ContentBlock struct {
	Type      string         `json:"type"` // "text", "image", "tool_use", "tool_result", "thinking"
	Text      string         `json:"text,omitempty"`
	Source    *ContentSource `json:"source,omitempty"`      // For images
	ID        string         `json:"id,omitempty"`          // For tool_use/tool_result
	Name      string         `json:"name,omitempty"`        // For tool_use
	Input     any            `json:"input,omitempty"`       // For tool_use
	ToolUseID string         `json:"tool_use_id,omitempty"` // For tool_result
	Content   any            `json:"content,omitempty"`     // For tool_result (string or []ContentBlock)
	IsError   bool           `json:"is_error,omitempty"`    // For tool_result
	Thinking  string         `json:"thinking,omitempty"`    // For thinking blocks
	Signature string         `json:"signature,omitempty"`   // Thinking signature (required for history)
}

// MarshalJSON implements custom JSON marshaling for ContentBlock to ensure
// tool_result blocks always include the content field (even if empty).
func (c ContentBlock) MarshalJSON() ([]byte, error) {
	// For tool_result blocks, we need to ensure content is always present
	if c.Type == "tool_result" {
		type Alias ContentBlock
		return json.Marshal(&struct {
			Content any `json:"content"` // Always include, no omitempty
			*Alias
		}{
			Content: c.Content,
			Alias:   (*Alias)(&c),
		})
	}

	// For all other block types, use default marshaling
	type Alias ContentBlock
	return json.Marshal((*Alias)(&c))
}

// ContentSource represents the source of an image.
type ContentSource struct {
	Type      string  `json:"type"`                 // "base64", "url"
	MediaType *string `json:"media_type,omitempty"` // "image/jpeg", etc.
	Data      string  `json:"data,omitempty"`       // Base64 data
	URL       string  `json:"url,omitempty"`        // URL
}

// SystemBlock represents a system prompt block.
type SystemBlock struct {
	Type string `json:"type"` // "text"
	Text string `json:"text"`
}

// ThinkingConfig enables Interleaved Thinking.
type ThinkingConfig struct {
	Type         string `json:"type"`                    // "enabled", "disabled", or "interleaved"
	BudgetTokens int    `json:"budget_tokens,omitempty"` // Token budget for thinking
}

// Tool represents a tool definition for function calling.
type Tool struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Type        string `json:"type,omitempty"`         // Default: "function"
	InputSchema any    `json:"input_schema,omitempty"` // JSON Schema
}

// MessageResponse represents a response from the MiniMax Messages API.
type MessageResponse struct {
	ID           string         `json:"id"`
	Type         string         `json:"type"` // "message"
	Role         string         `json:"role"` // "assistant"
	Content      []ContentBlock `json:"content"`
	Model        string         `json:"model"`
	StopReason   string         `json:"stop_reason"` // "end_turn", "max_tokens", "stop_sequence", "tool_use"
	StopSequence *string        `json:"stop_sequence,omitempty"`
	Usage        Usage          `json:"usage"`
}

// Usage tracks token consumption for a request.
type Usage struct {
	InputTokens   int  `json:"input_tokens"`
	OutputTokens  int  `json:"output_tokens"`
	CacheCreation *int `json:"cache_creation_input_tokens,omitempty"`
	CacheRead     *int `json:"cache_read_input_tokens,omitempty"`
}

// translateRequest translates a canonical ChatRequest to MiniMax format.
func translateRequest(
	req provider.ChatRequest,
	logger observability.Logger,
) (*MessageRequest, map[string]any, error) {
	// Build MiniMax request
	minimaxReq := &MessageRequest{
		Model:     req.Model,
		MaxTokens: 64000, // Default
	}

	// Use model from request or default
	if minimaxReq.Model == "" {
		minimaxReq.Model = DefaultModel
	}

	// Set max tokens based on model capabilities
	if modelInfo, ok := ModelCapabilities[minimaxReq.Model]; ok {
		minimaxReq.MaxTokens = modelInfo.MaxTokens
	}

	// Override with request-specific max tokens
	if req.MaxTokens != nil && *req.MaxTokens > 0 {
		minimaxReq.MaxTokens = *req.MaxTokens
	}

	if req.Temperature != nil {
		temperature := *req.Temperature
		if temperature < 0.0 || temperature > 1.0 {
			return nil, nil, fmt.Errorf("temperature must be between 0.0 and 1.0, got %.2f", temperature)
		}
		minimaxReq.Temperature = &temperature
	}

	if req.TopP != nil {
		topP := *req.TopP
		if topP < 0.0 || topP > 1.0 {
			return nil, nil, fmt.Errorf("top_p must be between 0.0 and 1.0, got %.2f", topP)
		}
		minimaxReq.TopP = &topP
	}

	// Set system prompt
	if req.SystemPrompt != "" {
		minimaxReq.System = req.SystemPrompt
	}

	// Translate messages
	messages, err := translateMessages(provider.PrepareMessagesForLLM(req.Messages))
	if err != nil {
		return nil, nil, fmt.Errorf("failed to translate messages: %w", err)
	}
	minimaxReq.Messages = messages

	// Translate tools
	if len(req.Tools) > 0 {
		minimaxReq.Tools = translateTools(req.Tools)
	}

	// Enable thinking if requested
	if req.Metadata != nil {
		if thinkingEnabled, ok := req.Metadata["thinking_enabled"].(bool); ok && thinkingEnabled {
			minimaxReq.Thinking = &ThinkingConfig{
				Type: "enabled",
			}
		}
	}

	// Create provider JSON for debugging (best-effort, non-fatal if serialization fails)
	var providerJSON map[string]any
	if jsonBytes, err := json.Marshal(minimaxReq); err == nil {
		_ = json.Unmarshal(jsonBytes, &providerJSON)
	}

	if logger != nil {
		logger.Debug(nil, "minimax.translate.request",
			observability.F("model", minimaxReq.Model),
			observability.F("message_count", len(minimaxReq.Messages)),
			observability.F("has_tools", len(minimaxReq.Tools) > 0),
		)
	}

	return minimaxReq, providerJSON, nil
}

// translateMessages translates canonical messages to MiniMax format.
func translateMessages(messages []*conversation.Message) ([]Message, error) {
	result := make([]Message, 0, len(messages))

	for _, msg := range messages {
		switch msg.Role {
		case conversation.RoleUser:
			result = append(result, translateUserMessage(msg))
		case conversation.RoleAssistant:
			result = append(result, translateAssistantMessage(msg))
		case conversation.RoleTool:
			// Tool results are wrapped in a user message
			result = append(result, Message{
				Role:    "user",
				Content: translateToolResult(msg),
			})
		}
	}

	return result, nil
}

// translateUserMessage translates a user message.
func translateUserMessage(msg *conversation.Message) Message {
	// For now, just send text content
	// MiniMax doesn't support images in the canonical format we're using
	return Message{
		Role:    "user",
		Content: msg.Content,
	}
}

// translateAssistantMessage translates an assistant message.
// CRITICAL: Must preserve thinking blocks for Interleaved Thinking context.
func translateAssistantMessage(msg *conversation.Message) Message {
	blocks := []ContentBlock{}

	// Add thinking block if present (required for Interleaved Thinking history)
	if msg.Thinking != "" {
		block := ContentBlock{
			Type:     "thinking",
			Thinking: msg.Thinking,
		}
		// Include signature if available (required for MiniMax)
		if sig, ok := msg.Metadata["thinking_signature"].(string); ok {
			block.Signature = sig
		}
		blocks = append(blocks, block)
	}

	// Check for reasoning_content in metadata (alternative thinking format)
	if reasoning, ok := msg.Metadata["reasoning_content"].(string); ok && reasoning != "" && msg.Thinking == "" {
		blocks = append(blocks, ContentBlock{
			Type:     "thinking",
			Thinking: reasoning,
		})
	}

	// Add text content
	if msg.Content != "" {
		blocks = append(blocks, ContentBlock{
			Type: "text",
			Text: msg.Content,
		})
	}

	// Add tool calls
	for _, tc := range msg.ToolCalls {
		params := tc.Parameters
		if params == nil {
			params = make(map[string]any)
		}
		blocks = append(blocks, ContentBlock{
			Type:  "tool_use",
			ID:    tc.ID,
			Name:  tc.Name,
			Input: params,
		})
	}

	return Message{
		Role:    "assistant",
		Content: blocks,
	}
}

// translateToolResult translates a tool result message.
func translateToolResult(msg *conversation.Message) []ContentBlock {
	blocks := []ContentBlock{}

	// Tool results are wrapped in a user message with tool_result blocks
	for _, result := range msg.ToolResults {
		block := ContentBlock{
			Type:      "tool_result",
			ToolUseID: result.CallID,
			Content:   result.Output,
		}
		// Check if error is indicated
		if result.Error != nil {
			block.IsError = true
		}
		blocks = append(blocks, block)
	}

	return blocks
}

// translateTools translates canonical tools to MiniMax format.
func translateTools(tools []provider.Tool) []Tool {
	result := make([]Tool, 0, len(tools))
	for _, t := range tools {
		result = append(result, Tool{
			Name:        t.Name,
			Description: t.Description,
			InputSchema: t.Parameters,
		})
	}
	return result
}

// translateResponse translates a MiniMax response to canonical format.
func translateResponse(resp *MessageResponse) (*provider.ChatResponse, error) {
	result := &provider.ChatResponse{
		FinishReason: translateStopReason(resp.StopReason),
		RawResponse:  resp,
	}

	// Build canonical message from response
	msg := &conversation.Message{
		ID:       resp.ID,
		Provider: "minimax",
		Model:    resp.Model,
		Role:     conversation.RoleAssistant,
		Metadata: make(map[string]any),
	}

	// Process content blocks
	var textParts []string
	var thinkingParts []string
	var thinkingSignature string

	for _, block := range resp.Content {
		switch block.Type {
		case "text":
			textParts = append(textParts, block.Text)
		case "thinking":
			thinkingParts = append(thinkingParts, block.Thinking)
			if block.Signature != "" {
				thinkingSignature = block.Signature
			}
		case "tool_use":
			msg.ToolCalls = append(msg.ToolCalls, conversation.ToolCall{
				ID:         block.ID,
				Name:       block.Name,
				Parameters: block.Input.(map[string]any),
			})
		}
	}

	// Combine text content
	msg.Content = strings.Join(textParts, "\n")

	// Store thinking content
	if len(thinkingParts) > 0 {
		msg.Thinking = strings.Join(thinkingParts, "\n")
		msg.Metadata["thinking_signature"] = thinkingSignature
	}

	// Set usage
	result.Message = msg
	result.Usage = &conversation.TokenUsage{
		Input:  resp.Usage.InputTokens,
		Output: resp.Usage.OutputTokens,
		Total:  resp.Usage.InputTokens + resp.Usage.OutputTokens,
	}

	return result, nil
}

// translateStopReason converts MiniMax stop reason to canonical format.
func translateStopReason(reason string) provider.FinishReason {
	switch strings.ToLower(reason) {
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
