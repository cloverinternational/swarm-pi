package anthropic

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/conversation"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/provider"
	sdkerr "github.com/Swarm-Code/mono/swarm-sdk/internal/sdkerr"
)

// ============================================================================
// JSON Utilities - Structured Data Extraction and Validation
// ============================================================================

// ExtractJSONFromResponse extracts structured JSON from Claude's response.
// Handles both plain JSON responses and JSON embedded in markdown code blocks.
func ExtractJSONFromResponse(resp *provider.ChatResponse) (map[string]any, error) {
	if resp == nil || resp.Message == nil {
		return nil, sdkerr.Permanent(
			"anthropic.advanced.nil_response",
			"response cannot be nil",
		)
	}

	content := resp.Message.Content
	if content == "" {
		return nil, sdkerr.Permanent(
			"anthropic.advanced.empty_content",
			"response content is empty",
		)
	}

	// Try to extract JSON from markdown code blocks first
	jsonStr := extractJSONFromMarkdown(content)
	if jsonStr == "" {
		// If no markdown block, treat entire content as JSON
		jsonStr = strings.TrimSpace(content)
	}

	// Parse JSON
	var result map[string]any
	if err := json.Unmarshal([]byte(jsonStr), &result); err != nil {
		return nil, sdkerr.Permanent(
			"anthropic.advanced.invalid_json",
			fmt.Sprintf("failed to parse JSON: %v", err),
		)
	}

	return result, nil
}

// extractJSONFromMarkdown extracts JSON from markdown code blocks.
// Supports ```json, ```, and ``` formats.
func extractJSONFromMarkdown(content string) string {
	// Pattern to match JSON code blocks: ```json...``` or ```...```
	patterns := []string{
		"```json\\s*\\n([\\s\\S]*?)\\n```",
		"```\\s*\\n([\\s\\S]*?)\\n```",
		"```([\\s\\S]*?)```",
	}

	for _, pattern := range patterns {
		re := regexp.MustCompile(pattern)
		matches := re.FindStringSubmatch(content)
		if len(matches) > 1 {
			jsonStr := strings.TrimSpace(matches[1])
			// Verify it looks like JSON (starts with { or [)
			if strings.HasPrefix(jsonStr, "{") || strings.HasPrefix(jsonStr, "[") {
				return jsonStr
			}
		}
	}

	return ""
}

// ValidateAndFormatJSON validates JSON string and returns formatted version.
// Useful for cleaning up JSON before further processing.
func ValidateAndFormatJSON(jsonStr string) (string, error) {
	if jsonStr == "" {
		return "", sdkerr.Permanent(
			"anthropic.advanced.empty_json",
			"JSON string is empty",
		)
	}

	// Parse to validate
	var data any
	if err := json.Unmarshal([]byte(jsonStr), &data); err != nil {
		return "", sdkerr.Permanent(
			"anthropic.advanced.invalid_json_format",
			fmt.Sprintf("invalid JSON: %v", err),
		)
	}

	// Format with indentation
	formatted, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return "", sdkerr.Permanent(
			"anthropic.advanced.json_marshal_failed",
			fmt.Sprintf("failed to format JSON: %v", err),
		)
	}

	return string(formatted), nil
}

// ============================================================================
// Token Counting - Accurate Usage Estimation
// ============================================================================

// CountTokens estimates token count for text using Claude's tokenization.
// This is an approximation based on the rough heuristic: 1 token ≈ 4 characters.
// For precise counts, use the official Anthropic token counting API.
func CountTokens(p *Provider, text string) (int, error) {
	if p == nil {
		return 0, sdkerr.Permanent(
			"anthropic.advanced.nil_provider",
			"provider cannot be nil",
		)
	}

	if text == "" {
		return 0, nil
	}

	// Approximate token count using character-based heuristic
	// Claude's tokenizer is similar to GPT: ~4 characters per token
	// This is a rough estimate and may vary by ±20%
	charCount := len(text)
	tokenCount := charCount / 4

	// Account for whitespace and punctuation (typically more efficient)
	whitespaceCount := strings.Count(text, " ") + strings.Count(text, "\n") + strings.Count(text, "\t")
	if whitespaceCount > 0 {
		// Whitespace is usually tokenized efficiently, adjust estimate
		tokenCount = (charCount - whitespaceCount/2) / 4
	}

	// Minimum of 1 token for non-empty text
	if tokenCount == 0 && len(text) > 0 {
		tokenCount = 1
	}

	return tokenCount, nil
}

// CountTokensForMessages estimates token count for a list of messages.
// Includes overhead for message formatting and role indicators.
func CountTokensForMessages(p *Provider, messages []*conversation.Message) (int, error) {
	if p == nil {
		return 0, sdkerr.Permanent(
			"anthropic.advanced.nil_provider",
			"provider cannot be nil",
		)
	}

	if len(messages) == 0 {
		return 0, nil
	}

	totalTokens := 0

	for _, msg := range messages {
		// Count content tokens
		contentTokens, err := CountTokens(p, msg.Content)
		if err != nil {
			return 0, err
		}
		totalTokens += contentTokens

		// Add overhead for message structure (role, formatting)
		// Anthropic adds ~3-5 tokens per message for structure
		totalTokens += 4

		// Add tokens for tool calls if present
		if len(msg.ToolCalls) > 0 {
			for _, toolCall := range msg.ToolCalls {
				// Tool name
				nameTokens, _ := CountTokens(p, toolCall.Name)
				totalTokens += nameTokens

				// Tool parameters (JSON)
				if toolCall.Parameters != nil {
					paramsJSON, err := json.Marshal(toolCall.Parameters)
					if err == nil {
						paramsTokens, _ := CountTokens(p, string(paramsJSON))
						totalTokens += paramsTokens
					}
				}

				// Overhead for tool call structure
				totalTokens += 10
			}
		}

		// Add tokens for tool results if present
		if len(msg.ToolResults) > 0 {
			for _, toolResult := range msg.ToolResults {
				outputTokens, _ := CountTokens(p, toolResult.Output)
				totalTokens += outputTokens

				// Overhead for tool result structure
				totalTokens += 5
			}
		}
	}

	return totalTokens, nil
}

// ============================================================================
// Context Optimization - Intelligent Conversation Trimming
// ============================================================================

// OptimizeMessages reduces messages to fit within token budget.
// Uses a "sliding window" approach that keeps the most recent messages.
// Preserves conversation context while staying within limits.
func OptimizeMessages(p *Provider, messages []*conversation.Message, targetTokens int) ([]*conversation.Message, error) {
	if p == nil {
		return nil, sdkerr.Permanent(
			"anthropic.advanced.nil_provider",
			"provider cannot be nil",
		)
	}

	if len(messages) == 0 {
		return []*conversation.Message{}, nil
	}

	if targetTokens <= 0 {
		return nil, sdkerr.Permanent(
			"anthropic.advanced.invalid_target_tokens",
			fmt.Sprintf("target tokens must be positive, got %d", targetTokens),
		)
	}

	// Count current tokens
	currentTokens, err := CountTokensForMessages(p, messages)
	if err != nil {
		return nil, err
	}

	// If already within budget, return as-is
	if currentTokens <= targetTokens {
		return messages, nil
	}

	// Use sliding window from the end (keep most recent messages)
	optimized := make([]*conversation.Message, 0)
	runningTotal := 0

	// Iterate backwards to keep most recent messages
	for i := len(messages) - 1; i >= 0; i-- {
		msg := messages[i]

		// Estimate tokens for this message
		msgTokens, err := CountTokensForMessages(p, []*conversation.Message{msg})
		if err != nil {
			continue
		}

		// Check if adding this message would exceed budget
		if runningTotal+msgTokens > targetTokens {
			break
		}

		// Add to beginning of optimized list (we're iterating backwards)
		optimized = append([]*conversation.Message{msg}, optimized...)
		runningTotal += msgTokens
	}

	// Ensure we have at least one message
	if len(optimized) == 0 && len(messages) > 0 {
		// Take the last message, truncate if necessary
		lastMsg := messages[len(messages)-1]
		optimized = []*conversation.Message{lastMsg}
	}

	return optimized, nil
}

// ============================================================================
// Tool Orchestration - Multi-Tool Workflow Management
// ============================================================================

// WorkflowType defines the execution pattern for tool workflows.
type WorkflowType string

const (
	// WorkflowTypeSequential executes tools one after another
	WorkflowTypeSequential WorkflowType = "sequential"

	// WorkflowTypeParallel executes tools concurrently
	WorkflowTypeParallel WorkflowType = "parallel"

	// WorkflowTypeConditional executes tools based on conditions
	WorkflowTypeConditional WorkflowType = "conditional"

	// WorkflowTypePipeline chains tool outputs as inputs
	WorkflowTypePipeline WorkflowType = "pipeline"
)

// ToolWorkflow represents a multi-tool execution workflow.
type ToolWorkflow struct {
	Type        WorkflowType    `json:"type"`
	Tools       []provider.Tool `json:"tools"`
	Description string          `json:"description,omitempty"`
	Metadata    map[string]any  `json:"metadata,omitempty"`
}

// CreateToolWorkflow creates a tool orchestration workflow.
// Organizes tools into an execution pattern for complex operations.
func CreateToolWorkflow(tools []provider.Tool, workflowType WorkflowType) (*ToolWorkflow, error) {
	if len(tools) == 0 {
		return nil, sdkerr.Permanent(
			"anthropic.advanced.empty_tools",
			"workflow requires at least one tool",
		)
	}

	// Validate workflow type
	validTypes := map[WorkflowType]bool{
		WorkflowTypeSequential:  true,
		WorkflowTypeParallel:    true,
		WorkflowTypeConditional: true,
		WorkflowTypePipeline:    true,
	}

	if !validTypes[workflowType] {
		return nil, sdkerr.Permanent(
			"anthropic.advanced.invalid_workflow_type",
			fmt.Sprintf("invalid workflow type: %s", workflowType),
		)
	}

	workflow := &ToolWorkflow{
		Type:     workflowType,
		Tools:    tools,
		Metadata: make(map[string]any),
	}

	// Set description based on type
	switch workflowType {
	case WorkflowTypeSequential:
		workflow.Description = fmt.Sprintf("Sequential execution of %d tools", len(tools))
	case WorkflowTypeParallel:
		workflow.Description = fmt.Sprintf("Parallel execution of %d tools", len(tools))
	case WorkflowTypeConditional:
		workflow.Description = fmt.Sprintf("Conditional execution of %d tools", len(tools))
	case WorkflowTypePipeline:
		workflow.Description = fmt.Sprintf("Pipeline execution of %d tools", len(tools))
	}

	return workflow, nil
}

// ============================================================================
// Response Processing - Enhanced Response Handling
// ============================================================================

// ProcessedResponse contains enriched response metadata.
type ProcessedResponse struct {
	Content         string                   `json:"content"`
	HasThinking     bool                     `json:"has_thinking"`
	ThinkingContent string                   `json:"thinking_content,omitempty"`
	HasCitations    bool                     `json:"has_citations"`
	CitationCount   int                      `json:"citation_count"`
	Citations       []Citation               `json:"citations,omitempty"`
	HasToolCalls    bool                     `json:"has_tool_calls"`
	ToolCallCount   int                      `json:"tool_call_count"`
	TokenUsage      *conversation.TokenUsage `json:"token_usage,omitempty"`
	FinishReason    provider.FinishReason    `json:"finish_reason"`
	Metadata        map[string]any           `json:"metadata,omitempty"`
}

// ProcessResponse analyzes and enriches response with metadata.
// Extracts thinking, citations, tool calls, and other metadata.
func ProcessResponse(resp *provider.ChatResponse) *ProcessedResponse {
	if resp == nil || resp.Message == nil {
		return &ProcessedResponse{
			Metadata: make(map[string]any),
		}
	}

	processed := &ProcessedResponse{
		Content:      resp.Message.Content,
		TokenUsage:   resp.Usage,
		FinishReason: resp.FinishReason,
		Metadata:     make(map[string]any),
	}

	// Extract thinking if present
	if resp.Message.Metadata != nil {
		if thinking, ok := resp.Message.Metadata["thinking"].(string); ok && thinking != "" {
			processed.HasThinking = true
			processed.ThinkingContent = thinking
		}

		// Extract citations
		if citationsRaw, ok := resp.Message.Metadata["citations"]; ok {
			switch citations := citationsRaw.(type) {
			case []Citation:
				processed.HasCitations = len(citations) > 0
				processed.CitationCount = len(citations)
				processed.Citations = citations
			case []any:
				processed.HasCitations = len(citations) > 0
				processed.CitationCount = len(citations)
			}
		}

		// Copy other metadata
		for k, v := range resp.Message.Metadata {
			if k != "thinking" && k != "citations" {
				processed.Metadata[k] = v
			}
		}
	}

	// Check for tool calls
	if len(resp.Message.ToolCalls) > 0 {
		processed.HasToolCalls = true
		processed.ToolCallCount = len(resp.Message.ToolCalls)
	}

	return processed
}

// ResponseQuality assesses the quality of a response.
type ResponseQuality struct {
	IsComplete      bool    `json:"is_complete"`
	ContentLength   int     `json:"content_length"`
	HasThinking     bool    `json:"has_thinking"`
	HasCitations    bool    `json:"has_citations"`
	TokenEfficiency float64 `json:"token_efficiency,omitempty"`
	QualityScore    float64 `json:"quality_score"`
}

// ValidateResponseQuality assesses response quality.
// Checks completeness, content length, and other quality indicators.
func ValidateResponseQuality(resp *provider.ChatResponse) *ResponseQuality {
	if resp == nil || resp.Message == nil {
		return &ResponseQuality{
			IsComplete:   false,
			QualityScore: 0.0,
		}
	}

	quality := &ResponseQuality{
		ContentLength: len(resp.Message.Content),
	}

	// Check if response is complete
	quality.IsComplete = resp.FinishReason == provider.FinishReasonStop

	// Check for thinking
	if resp.Message.Metadata != nil {
		if thinking, ok := resp.Message.Metadata["thinking"].(string); ok && thinking != "" {
			quality.HasThinking = true
		}
		if citationsRaw, ok := resp.Message.Metadata["citations"]; ok {
			switch citations := citationsRaw.(type) {
			case []Citation:
				quality.HasCitations = len(citations) > 0
			case []any:
				quality.HasCitations = len(citations) > 0
			}
		}
	}

	// Calculate token efficiency (output tokens / input tokens)
	if resp.Usage != nil && resp.Usage.Input > 0 {
		quality.TokenEfficiency = float64(resp.Usage.Output) / float64(resp.Usage.Input)
	}

	// Calculate quality score (0.0 - 1.0)
	score := 0.0

	// Completeness (40%)
	if quality.IsComplete {
		score += 0.4
	}

	// Content length (30%)
	if quality.ContentLength > 100 {
		score += 0.3
	} else if quality.ContentLength > 20 {
		score += 0.15
	}

	// Thinking present (15%)
	if quality.HasThinking {
		score += 0.15
	}

	// Citations present (15%)
	if quality.HasCitations {
		score += 0.15
	}

	quality.QualityScore = score

	return quality
}

// ============================================================================
// Response Formatting - Display Utilities
// ============================================================================

// DisplayFormat defines output format for responses.
type DisplayFormat string

const (
	// DisplayFormatPlain returns plain text content
	DisplayFormatPlain DisplayFormat = "plain"

	// DisplayFormatMarkdown returns markdown-formatted content
	DisplayFormatMarkdown DisplayFormat = "markdown"

	// DisplayFormatJSON returns JSON-formatted content
	DisplayFormatJSON DisplayFormat = "json"

	// DisplayFormatHTML returns HTML-formatted content
	DisplayFormatHTML DisplayFormat = "html"
)

// FormatResponseForDisplay formats response for display.
// Supports multiple output formats for different contexts.
func FormatResponseForDisplay(resp *provider.ChatResponse, format DisplayFormat) string {
	if resp == nil || resp.Message == nil {
		return ""
	}

	switch format {
	case DisplayFormatPlain:
		return resp.Message.Content

	case DisplayFormatMarkdown:
		return formatAsMarkdown(resp)

	case DisplayFormatJSON:
		return formatAsJSON(resp)

	case DisplayFormatHTML:
		return formatAsHTML(resp)

	default:
		return resp.Message.Content
	}
}

// formatAsMarkdown formats response as markdown.
func formatAsMarkdown(resp *provider.ChatResponse) string {
	var builder strings.Builder

	// Content
	builder.WriteString(resp.Message.Content)
	builder.WriteString("\n\n")

	// Thinking (if present)
	if resp.Message.Metadata != nil {
		if thinking, ok := resp.Message.Metadata["thinking"].(string); ok && thinking != "" {
			builder.WriteString("### Thinking\n\n")
			builder.WriteString(thinking)
			builder.WriteString("\n\n")
		}
	}

	// Usage stats (if present)
	if resp.Usage != nil {
		builder.WriteString("---\n\n")
		builder.WriteString(fmt.Sprintf("**Tokens:** %d input, %d output, %d total\n",
			resp.Usage.Input, resp.Usage.Output, resp.Usage.Total))
	}

	return builder.String()
}

// formatAsJSON formats response as JSON.
func formatAsJSON(resp *provider.ChatResponse) string {
	data := map[string]any{
		"content":       resp.Message.Content,
		"finish_reason": resp.FinishReason,
	}

	if resp.Usage != nil {
		data["usage"] = map[string]int{
			"input":  resp.Usage.Input,
			"output": resp.Usage.Output,
			"total":  resp.Usage.Total,
		}
	}

	if resp.Message.Metadata != nil {
		data["metadata"] = resp.Message.Metadata
	}

	jsonBytes, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return resp.Message.Content
	}

	return string(jsonBytes)
}

// formatAsHTML formats response as HTML.
func formatAsHTML(resp *provider.ChatResponse) string {
	var builder strings.Builder

	builder.WriteString("<div class=\"claude-response\">\n")

	// Content
	builder.WriteString("  <div class=\"content\">\n")
	builder.WriteString("    <p>")
	builder.WriteString(htmlEscape(resp.Message.Content))
	builder.WriteString("</p>\n")
	builder.WriteString("  </div>\n")

	// Thinking (if present)
	if resp.Message.Metadata != nil {
		if thinking, ok := resp.Message.Metadata["thinking"].(string); ok && thinking != "" {
			builder.WriteString("  <div class=\"thinking\">\n")
			builder.WriteString("    <h4>Thinking</h4>\n")
			builder.WriteString("    <p>")
			builder.WriteString(htmlEscape(thinking))
			builder.WriteString("</p>\n")
			builder.WriteString("  </div>\n")
		}
	}

	// Usage stats (if present)
	if resp.Usage != nil {
		builder.WriteString("  <div class=\"usage\">\n")
		builder.WriteString(fmt.Sprintf("    <small>Tokens: %d input, %d output</small>\n",
			resp.Usage.Input, resp.Usage.Output))
		builder.WriteString("  </div>\n")
	}

	builder.WriteString("</div>\n")

	return builder.String()
}

// htmlEscape escapes HTML special characters.
func htmlEscape(s string) string {
	replacer := strings.NewReplacer(
		"&", "&amp;",
		"<", "&lt;",
		">", "&gt;",
		"\"", "&quot;",
		"'", "&#39;",
	)
	return replacer.Replace(s)
}
