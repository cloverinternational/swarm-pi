package gemini

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"maps"
	"strings"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/conversation"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/provider"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/vision"
	"github.com/google/uuid"
)

// SyntheticThoughtSignature is used when the original thought signature is not available.
// This matches the gemini-cli behavior which uses this special value to bypass validation.
// See: gemini-cli/packages/core/src/core/geminiChat.ts:85
const SyntheticThoughtSignature = "skip_thought_signature_validator"

// Gemini API request/response types based on the Gemini CLI implementation

// GeminiRequest represents a request to the Gemini API.
type GeminiRequest struct {
	Model        string         `json:"model"`
	Project      string         `json:"project,omitempty"`
	UserPromptID string         `json:"user_prompt_id,omitempty"`
	Request      *VertexRequest `json:"request"`
}

// VertexRequest is the inner request structure.
type VertexRequest struct {
	Contents          []*Content        `json:"contents"`
	SystemInstruction *Content          `json:"systemInstruction,omitempty"`
	CachedContent     string            `json:"cachedContent,omitempty"` // Reference to cached content
	Tools             []*Tool           `json:"tools,omitempty"`
	ToolConfig        *ToolConfig       `json:"toolConfig,omitempty"`
	SafetySettings    []*SafetySetting  `json:"safetySettings,omitempty"`
	GenerationConfig  *GenerationConfig `json:"generationConfig,omitempty"`
	SessionID         string            `json:"session_id,omitempty"`
}

// Content represents a message in the conversation.
type Content struct {
	Role  string  `json:"role,omitempty"`
	Parts []*Part `json:"parts"`
}

// Part represents a part of a message (text, function call, etc.).
type Part struct {
	Text             string            `json:"text,omitempty"`
	FunctionCall     *FunctionCall     `json:"functionCall,omitempty"`
	FunctionResponse *FunctionResponse `json:"functionResponse,omitempty"`
	InlineData       *InlineData       `json:"inline_data,omitempty"` // Changed from "inlineData" to match official Gemini API format
	Thought          any               `json:"thought,omitempty"`
	ThoughtSignature string            `json:"thoughtSignature,omitempty"`
}

// FunctionCall represents a tool/function call.
type FunctionCall struct {
	Name string         `json:"name"`
	Args map[string]any `json:"args"`
}

// FunctionResponse represents a tool/function response.
type FunctionResponse struct {
	ID       string `json:"id,omitempty"` // The call ID from the function call
	Name     string `json:"name"`
	Response any    `json:"response"`
}

// InlineData represents inline binary data (e.g., images).
type InlineData struct {
	MimeType string `json:"mime_type"` // Changed from "mimeType" to match official Gemini API format
	Data     string `json:"data"`      // base64 encoded
}

// Tool represents a tool definition.
type Tool struct {
	FunctionDeclarations []*FunctionDeclaration `json:"functionDeclarations,omitempty"`
}

// FunctionDeclaration defines a function/tool.
type FunctionDeclaration struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Parameters  any    `json:"parameters,omitempty"` // JSON Schema
}

// ToolConfig configures tool behavior.
type ToolConfig struct {
	FunctionCallingConfig *FunctionCallingConfig `json:"functionCallingConfig,omitempty"`
}

// FunctionCallingConfig configures function calling behavior.
type FunctionCallingConfig struct {
	Mode             string   `json:"mode,omitempty"` // "AUTO", "ANY", "NONE"
	AllowedFunctions []string `json:"allowedFunctionNames,omitempty"`
}

// SafetySetting configures content safety.
type SafetySetting struct {
	Category  string `json:"category"`
	Threshold string `json:"threshold"`
}

// ThinkingConfig configures thinking/reasoning for supported models.
type ThinkingConfig struct {
	// ThinkingLevel: "NONE", "LOW", "MEDIUM", "HIGH" - for gemini-3 models
	ThinkingLevel string `json:"thinking_level,omitempty"`
	// ThinkingBudget: token budget for thinking - for gemini-2.5 models
	ThinkingBudget *int `json:"thinking_budget,omitempty"`
	// IncludeThoughts: whether to include thoughts in response
	IncludeThoughts *bool `json:"include_thoughts,omitempty"`
}

// GenerationConfig configures generation parameters.
type GenerationConfig struct {
	Temperature      *float64        `json:"temperature,omitempty"`
	TopP             *float64        `json:"topP,omitempty"`
	TopK             *int            `json:"topK,omitempty"`
	CandidateCount   *int            `json:"candidateCount,omitempty"`
	MaxOutputTokens  *int            `json:"maxOutputTokens,omitempty"`
	StopSequences    []string        `json:"stopSequences,omitempty"`
	ResponseMimeType string          `json:"responseMimeType,omitempty"`
	ResponseLogprobs *bool           `json:"responseLogprobs,omitempty"`
	PresencePenalty  *float64        `json:"presencePenalty,omitempty"`
	FrequencyPenalty *float64        `json:"frequencyPenalty,omitempty"`
	Seed             *int            `json:"seed,omitempty"`
	ThinkingConfig   *ThinkingConfig `json:"thinkingConfig,omitempty"`
}

// GeminiResponse represents a response from the Gemini API.
type GeminiResponse struct {
	Response *VertexResponse `json:"response"`
	TraceID  string          `json:"traceId,omitempty"`
}

// VertexResponse is the inner response structure.
type VertexResponse struct {
	Candidates    []*Candidate   `json:"candidates"`
	UsageMetadata *UsageMetadata `json:"usageMetadata,omitempty"`
	ModelVersion  string         `json:"modelVersion,omitempty"`
}

// Candidate represents a response candidate.
type Candidate struct {
	Content       *Content        `json:"content"`
	FinishReason  string          `json:"finishReason,omitempty"`
	SafetyRatings []*SafetyRating `json:"safetyRatings,omitempty"`
	Index         int             `json:"index"`
}

// SafetyRating represents a safety rating.
type SafetyRating struct {
	Category    string `json:"category"`
	Probability string `json:"probability"`
}

// UsageMetadata tracks token usage.
type UsageMetadata struct {
	PromptTokenCount     int `json:"promptTokenCount,omitempty"`
	CandidatesTokenCount int `json:"candidatesTokenCount,omitempty"`
	TotalTokenCount      int `json:"totalTokenCount,omitempty"`
}

// IsMessageValid checks if a message has valid content for the Gemini API.
// A message is valid if it has:
// - Non-empty content, OR
// - At least one tool call (for assistant messages), OR
// - At least one tool result (for tool messages)
// - Thinking content in metadata
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

	// Check for thinking content in metadata
	if msg.Metadata != nil {
		if thinking, ok := msg.Metadata["thinking"].(string); ok && strings.TrimSpace(thinking) != "" {
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

	// Strategy 1: Use thinking content as main content
	if sanitized.Thinking != "" {
		sanitized.Content = sanitized.Thinking
		return sanitized
	}

	if sanitized.Metadata != nil {
		if thinking, ok := sanitized.Metadata["thinking"].(string); ok && thinking != "" {
			sanitized.Content = thinking
			return sanitized
		}
	}

	// Strategy 2: For empty assistant messages, add a placeholder
	if sanitized.Role == conversation.RoleAssistant {
		sanitized.Content = "[Response truncated due to token limit]"
		if sanitized.Metadata == nil {
			sanitized.Metadata = make(map[string]any)
		}
		sanitized.Metadata["truncated"] = true
		sanitized.Metadata["sanitized"] = true
		return sanitized
	}

	// Cannot repair other roles
	return nil
}

// SanitizeMessages filters and repairs messages to ensure they are all valid.
func SanitizeMessages(messages []*conversation.Message) []*conversation.Message {
	result := make([]*conversation.Message, 0, len(messages))

	for _, msg := range messages {
		if msg == nil {
			continue
		}

		if IsMessageValid(msg) {
			result = append(result, msg)
			continue
		}

		sanitized := SanitizeMessage(msg)
		if sanitized != nil {
			result = append(result, sanitized)
		}
	}

	return result
}

// repairOrphanedFunctionCalls ensures every model message with functionCall
// has a matching functionResponse in the next user message.
// This provides cross-provider compatibility for conversations from other providers
// like Anthropic that may have stricter tool pairing requirements.
func repairOrphanedFunctionCalls(contents []*Content) []*Content {
	if len(contents) == 0 {
		return contents
	}

	result := make([]*Content, 0, len(contents))

	for i := range contents {
		content := contents[i]
		result = append(result, content)

		// Skip nil content or non-model messages
		if content == nil || content.Role != "model" || content.Parts == nil {
			continue
		}

		// Extract function call names from this message
		functionCalls := make(map[string]bool) // name -> exists
		for _, part := range content.Parts {
			if part != nil && part.FunctionCall != nil {
				functionCalls[part.FunctionCall.Name] = true
			}
		}

		if len(functionCalls) == 0 {
			continue
		}

		// Check if next message is user with matching functionResponses
		if i+1 < len(contents) && contents[i+1] != nil && contents[i+1].Role == "user" {
			nextContent := contents[i+1]
			if nextContent.Parts != nil {
				for _, part := range nextContent.Parts {
					if part != nil && part.FunctionResponse != nil {
						delete(functionCalls, part.FunctionResponse.Name)
					}
				}
			}
		}

		// Synthesize missing function responses
		if len(functionCalls) > 0 {
			syntheticParts := make([]*Part, 0, len(functionCalls))
			for name := range functionCalls {
				syntheticParts = append(syntheticParts, &Part{
					FunctionResponse: &FunctionResponse{
						Name:     name,
						Response: map[string]any{"error": "[Tool execution interrupted - " + name + " was cancelled before completion]"},
					},
				})
			}
			syntheticContent := &Content{
				Role:  "user",
				Parts: syntheticParts,
			}
			result = append(result, syntheticContent)
		}
	}

	return result
}

// generateStablePromptID produces a deterministic user_prompt_id from the
// session ID and message count. This ensures the same conversation state
// always produces the same ID, which is important for Gemini's caching.
// Falls back to uuid.New() when sessionID is empty.
func generateStablePromptID(sessionID string, messageCount int) string {
	if sessionID == "" {
		return strings.ReplaceAll(uuid.New().String(), "-", "")
	}
	h := sha256.Sum256(fmt.Appendf(nil, "%s:%d", sessionID, messageCount))
	return hex.EncodeToString(h[:])[:32]
}

// TranslateRequest converts a provider.ChatRequest to a GeminiRequest.
func TranslateRequest(req provider.ChatRequest, projectID, sessionID string) *GeminiRequest {
	// Sanitize messages before translation
	sanitizedMessages := SanitizeMessages(provider.PrepareMessagesForLLM(req.Messages))

	// Generate a stable user prompt ID based on session and message count
	userPromptID := generateStablePromptID(sessionID, len(sanitizedMessages))

	// Translate messages and repair orphaned function calls
	contents := translateMessages(sanitizedMessages)
	contents = repairOrphanedFunctionCalls(contents)

	geminiReq := &GeminiRequest{
		Model:        mapModel(req.Model),
		Project:      projectID,
		UserPromptID: userPromptID,
		Request: &VertexRequest{
			Contents:         contents,
			Tools:            translateTools(req.Tools),
			GenerationConfig: translateGenerationConfig(req),
			SessionID:        sessionID,
		},
	}

	// Add ToolConfig with AUTO mode when tools are present
	// This explicitly tells Gemini to automatically decide when to call functions
	if len(req.Tools) > 0 {
		geminiReq.Request.ToolConfig = &ToolConfig{
			FunctionCallingConfig: &FunctionCallingConfig{
				Mode: "AUTO",
			},
		}
	}

	// Handle system prompt
	if req.SystemPrompt != "" {
		geminiReq.Request.SystemInstruction = &Content{
			// For system instruction, role is often omitted
			Parts: []*Part{
				{Text: req.SystemPrompt},
			},
		}
	}

	return geminiReq
}

// mapModel normalizes model names to the format expected by cloudcode-pa endpoint.
// Note: The cloudcode-pa endpoint does NOT use the "models/" prefix, unlike the
// standard Vertex AI endpoint. This matches the official gemini-cli behavior.
func mapModel(model string) string {
	// Strip "models/" prefix if present - cloudcode-pa doesn't use it
	if len(model) > 7 && model[:7] == "models/" {
		return model[7:]
	}

	// Map common aliases to their canonical names
	switch model {
	case "gemini-pro":
		return "gemini-1.0-pro"
	case "gemini-flash":
		return "gemini-2.0-flash"
	case "gemini-pro-experimental":
		return "gemini-2.5-pro"
	default:
		return model
	}
}

// translateMessages converts conversation messages to Gemini format.
func translateMessages(messages []*conversation.Message) []*Content {
	contents := make([]*Content, 0, len(messages))

	for _, msg := range messages {
		if msg == nil {
			continue
		}

		content := &Content{
			Role:  translateRole(msg.Role),
			Parts: make([]*Part, 0),
		}

		// Get content - check multiple sources
		textContent := strings.TrimSpace(msg.Content)

		// If no direct content, try thinking as fallback
		if textContent == "" {
			if msg.Thinking != "" {
				textContent = msg.Thinking
			} else if msg.Metadata != nil {
				if thinking, ok := msg.Metadata["thinking"].(string); ok && thinking != "" {
					textContent = thinking
				}
			}
		}

		// Add text content if present
		if textContent != "" {
			content.Parts = append(content.Parts, &Part{Text: textContent})
		}

		// Add tool calls (function calls from assistant)
		for i, tc := range msg.ToolCalls {
			// Use stored signature, or synthetic for first call if missing
			// Gemini requires thoughtSignature on the first function call in each model turn
			sig := tc.ThoughtSignature
			if sig == "" && i == 0 {
				sig = SyntheticThoughtSignature
			}
			content.Parts = append(content.Parts, &Part{
				FunctionCall: &FunctionCall{
					Name: tc.Name,
					Args: tc.Parameters,
				},
				ThoughtSignature: sig,
			})
		}

		// Add tool results (function responses)
		for _, tr := range msg.ToolResults {
			content.Parts = append(content.Parts, &Part{
				FunctionResponse: &FunctionResponse{
					ID:       tr.CallID,
					Name:     tr.Name,
					Response: map[string]any{"output": tr.Output},
				},
			})
		}

		// Handle images from metadata using vision package
		if msg.Metadata != nil {
			images, err := vision.ExtractImagesFromMetadata(msg.Metadata)
			if err != nil {
				// Log error and continue with message processing
				// The vision package returns detailed error if format is invalid
				continue
			}

			for _, img := range images {
				if img.Type == "base64" {
					content.Parts = append(content.Parts, &Part{
						InlineData: &InlineData{
							MimeType: img.MediaType,
							Data:     img.Data,
						},
					})
				}
				// Note: URL-based images are not supported by Gemini's inline_data format
				// They would need to be handled differently if needed
			}
		}

		// Only add content if it has parts (skip completely empty messages)
		if len(content.Parts) > 0 {
			contents = append(contents, content)
		} else if msg.Role == conversation.RoleAssistant {
			// For assistant messages with no parts, add a placeholder
			content.Parts = append(content.Parts, &Part{Text: "[Response truncated]"})
			contents = append(contents, content)
		}
	}

	return contents
}

// translateRole maps conversation roles to Gemini roles.
func translateRole(role conversation.Role) string {
	switch role {
	case conversation.RoleUser:
		return "user"
	case conversation.RoleAssistant:
		return "model"
	case conversation.RoleSystem:
		return "user" // System messages are handled separately
	case conversation.RoleTool:
		return "user" // Tool results come from user role
	default:
		return "user"
	}
}

// translateTools converts provider tools to Gemini format.
func translateTools(tools []provider.Tool) []*Tool {
	if len(tools) == 0 {
		return nil
	}

	// Deduplicate tools by name (case-insensitive)
	seen := make(map[string]bool)
	declarations := make([]*FunctionDeclaration, 0, len(tools))
	for _, t := range tools {
		nameLower := strings.ToLower(t.Name)
		if seen[nameLower] {
			continue
		}
		seen[nameLower] = true

		declarations = append(declarations, &FunctionDeclaration{
			Name:        t.Name,
			Description: t.Description,
			Parameters:  cleanSchema(t.Parameters),
		})
	}

	return []*Tool{
		{FunctionDeclarations: declarations},
	}
}

// translateGenerationConfig creates generation config from request.
// This matches the default config from the official gemini-cli for chat models.
func translateGenerationConfig(req provider.ChatRequest) *GenerationConfig {
	config := &GenerationConfig{}

	// Set temperature from request or use chat default (1.0)
	if req.Temperature != nil {
		config.Temperature = req.Temperature
	} else {
		temp := 1.0
		config.Temperature = &temp
	}

	config.TopP = req.TopP
	config.TopK = req.TopK

	if req.MaxTokens != nil {
		config.MaxOutputTokens = req.MaxTokens
	}

	if len(req.StopSequences) > 0 {
		config.StopSequences = req.StopSequences
	}

	// Add thinking config based on model and TUI settings.
	// When the TUI sets thinking_enabled=false, we must explicitly disable thinking.
	// When thinking_enabled=true, we use the provided budget.
	// When thinking_enabled is absent (standalone SDK usage), use defaults.
	thinkingBudget := 0
	thinkingLevel := ""
	thinkingDisabled := false
	if req.Metadata != nil {
		if budget, ok := req.Metadata["thinking_budget"].(int); ok {
			thinkingBudget = budget
		}
		if level, ok := req.Metadata["thinking_level"].(string); ok && level != "" {
			thinkingLevel = level
		} else if effort, ok := req.Metadata["thinking_effort"].(string); ok && effort != "" {
			thinkingLevel = effort
		}
		if enabled, ok := req.Metadata["thinking_enabled"].(bool); ok && !enabled {
			thinkingDisabled = true
		}
	}
	thinkingConfig := getThinkingConfig(req.Model, thinkingBudget, thinkingLevel, thinkingDisabled)
	if thinkingConfig != nil {
		config.ThinkingConfig = thinkingConfig
	}

	return config
}

// getThinkingConfig returns the appropriate thinking config for the model.
// This matches the official gemini-cli default thinking settings.
// Only gemini-2.5+ and gemini-3+ models support thinking.
//
// When thinkingDisabled is true (user ran /thinking off), thinking is explicitly
// turned off by returning nil (omitting thinkingConfig) rather than sending an
// invalid "NONE" value that the API rejects.
func getThinkingConfig(model string, thinkingBudget int, thinkingLevel string, thinkingDisabled bool) *ThinkingConfig {
	includeThoughts := true

	// Gemini 3 models use thinkingLevel
	if strings.Contains(model, "gemini-3") {
		// When thinking is disabled, return nil to omit thinkingConfig entirely
		// The API rejects "NONE" as a thinking level - it expects an enum or valid string value
		if thinkingDisabled {
			return nil
		}
		level := "HIGH"
		if thinkingLevel != "" {
			level = strings.ToUpper(thinkingLevel)
		}
		return &ThinkingConfig{
			ThinkingLevel:   level,
			IncludeThoughts: &includeThoughts,
		}
	}

	// Gemini 2.5 models use thinkingBudget.
	// Official Gemini CLI default: DEFAULT_THINKING_MODE = 8192.
	if strings.Contains(model, "gemini-2.5") {
		if thinkingDisabled {
			budget := 0
			includeThoughts = false
			return &ThinkingConfig{
				ThinkingBudget:  &budget,
				IncludeThoughts: &includeThoughts,
			}
		}
		budget := thinkingBudget
		if budget <= 0 {
			budget = 8192 // Capped thinking budget (matches official gemini-cli DEFAULT_THINKING_MODE)
		}
		return &ThinkingConfig{
			ThinkingBudget:  &budget,
			IncludeThoughts: &includeThoughts,
		}
	}

	// Older models don't support thinking - return nil to omit thinkingConfig
	return nil
}

// TranslateResponse converts a GeminiResponse to a provider.ChatResponse.
func TranslateResponse(resp *GeminiResponse) *provider.ChatResponse {
	if resp == nil || resp.Response == nil || len(resp.Response.Candidates) == 0 {
		return nil
	}

	candidate := resp.Response.Candidates[0]
	msg := &conversation.Message{
		Role: conversation.RoleAssistant,
	}

	// Extract content from parts, separating thinking from content
	if candidate.Content != nil {
		for _, part := range candidate.Content.Parts {
			if part.Text != "" {
				if isThought(part.Thought) {
					if msg.Thinking != "" {
						msg.Thinking += "\n\n"
					}
					msg.Thinking += part.Text
				} else {
					msg.Content += part.Text
				}
			}
			if part.FunctionCall != nil {
				// Generate a unique toolu_-prefixed ID for each tool call.
				// Gemini API does not return per-call IDs in its FunctionCall struct,
				// so we previously used the function name as the ID. This caused
				// "tool_use ids must be unique" (anthropic.invalid_request) errors whenever
				// the same tool was invoked more than once in a single assistant turn
				// (e.g. two Bash calls in one response). The generated UUID guarantees
				// uniqueness across every tool call in the entire conversation history.
				toolCallID := "toolu_" + strings.ReplaceAll(uuid.New().String(), "-", "")[:24]
				msg.ToolCalls = append(msg.ToolCalls, conversation.ToolCall{
					ID:               toolCallID,
					Name:             part.FunctionCall.Name,
					Parameters:       part.FunctionCall.Args,
					ThoughtSignature: part.ThoughtSignature, // Preserve for Gemini thinking models
				})
			}
		}
	}

	finishReason := translateFinishReason(candidate.FinishReason)

	// CRITICAL FIX: Override finish reason if there are tool calls.
	// Gemini API often returns "STOP" as the finish reason even when it wants to call tools.
	// This is different from OpenAI ("tool_calls") and Anthropic ("tool_use") which have
	// explicit finish reasons for tool calls. Without this fix, the agent loop sees
	// FinishReasonStop and terminates instead of executing the requested tools.
	if len(msg.ToolCalls) > 0 {
		finishReason = provider.FinishReasonToolCalls
	}

	// Handle truncated responses (MAX_TOKENS)
	if finishReason == provider.FinishReasonLength && msg.Content == "" && len(msg.ToolCalls) == 0 {
		msg.Content = "[Response truncated due to token limit]"
		if msg.Metadata == nil {
			msg.Metadata = make(map[string]any)
		}
		msg.Metadata["truncated"] = true
		msg.Metadata["finish_reason"] = "MAX_TOKENS"
	}

	result := &provider.ChatResponse{
		Message:      msg,
		FinishReason: finishReason,
		RawResponse:  resp,
	}

	// Add usage if available
	if resp.Response.UsageMetadata != nil {
		result.Usage = &conversation.TokenUsage{
			Input:  resp.Response.UsageMetadata.PromptTokenCount,
			Output: resp.Response.UsageMetadata.CandidatesTokenCount,
			Total:  resp.Response.UsageMetadata.TotalTokenCount,
		}
	}

	return result
}

// translateFinishReason maps Gemini finish reasons to provider finish reasons.
func translateFinishReason(reason string) provider.FinishReason {
	switch reason {
	case "STOP":
		return provider.FinishReasonStop
	case "MAX_TOKENS":
		return provider.FinishReasonLength
	case "SAFETY":
		return provider.FinishReasonContentFilter
	case "RECITATION":
		return provider.FinishReasonContentFilter
	case "MALFORMED_FUNCTION_CALL":
		return provider.FinishReasonToolCalls
	default:
		if reason != "" {
			return provider.FinishReason(reason)
		}
		return provider.FinishReasonStop
	}
}

// cleanSchema removes fields that are not supported by the Gemini API from the JSON schema.
func cleanSchema(v any) any {
	switch val := v.(type) {
	case map[string]any:
		clean := make(map[string]any, len(val))
		for k, v := range val {
			// Skip fields that Gemini API doesn't support
			if k == "$schema" || k == "examples" || k == "show_whitespace" {
				continue
			}
			clean[k] = cleanSchema(v)
		}
		return clean
	case []any:
		newArr := make([]any, len(val))
		for i, item := range val {
			newArr[i] = cleanSchema(item)
		}
		return newArr
	default:
		return val
	}
}
