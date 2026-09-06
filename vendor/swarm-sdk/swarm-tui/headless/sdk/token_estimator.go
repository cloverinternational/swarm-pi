package sdk

import (
	"encoding/json"
	"regexp"
	"strings"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/conversation"
)

// TokenEstimator estimates token counts before API calls
type TokenEstimator struct {
	// Provider-specific char/token ratios with +2% safety margin
	// Based on analysis of 514 conversations, 26,122 messages
	providerRatios map[string]float64

	// Pattern adjustments for different content types
	patternAdjustments map[string]float64
}

// EstimatedTokens represents an initial token count estimate
type EstimatedTokens struct {
	SystemPromptTokens int     `json:"systemPromptTokens"`
	HistoryTokens      int     `json:"historyTokens"`
	UserMessageTokens  int     `json:"userMessageTokens"`
	TotalEstimated     int     `json:"totalEstimated"`
	Provider           string  `json:"provider"`
	SafetyMargin       float64 `json:"safetyMargin"`
}

// NewTokenEstimator creates a new token estimator with default ratios
func NewTokenEstimator() *TokenEstimator {
	return &TokenEstimator{
		// Ratios from token-counting-experiment analysis
		// Confidence: 97.74%, Average Error: 2.26%
		// Adding +2% safety margin to avoid context window issues
		providerRatios: map[string]float64{
			"anthropic": 3.70 * 1.02, // Claude models
			"google":    3.80 * 1.02, // Gemini models
			"openai":    4.00 * 1.02, // GPT models
			"zhipu":     3.50 * 1.02, // GLM models
			"meta":      3.60 * 1.02, // Llama models
			"local":     3.60 * 1.02, // Local models
			"unknown":   3.80 * 1.02, // Conservative fallback
		},
		patternAdjustments: map[string]float64{
			"code_block":   0.85, // Code blocks: fewer tokens per char
			"inline_code":  0.95,
			"plain_text":   1.05,
			"markdown":     1.00,
			"high_special": 0.90,
		},
	}
}

// EstimateInitialContext estimates total token count for the initial context
func (e *TokenEstimator) EstimateInitialContext(
	systemPrompt string,
	conversationHistory []*conversation.Message,
	userMessage string,
	provider string,
) EstimatedTokens {
	estimated := EstimatedTokens{
		Provider:     provider,
		SafetyMargin: 0.02, // 2% safety margin
	}

	// Estimate system prompt tokens
	estimated.SystemPromptTokens = e.estimateTokens(systemPrompt, provider)

	// Estimate conversation history tokens (including tool calls and results)
	for _, msg := range conversationHistory {
		// Count message content
		estimated.HistoryTokens += e.estimateTokens(msg.Content, provider)

		// Count tool calls (assistant messages requesting tools)
		for _, tc := range msg.ToolCalls {
			estimated.HistoryTokens += e.estimateToolCallTokens(tc, provider)
		}

		// Count tool results (user messages with tool execution results)
		for _, tr := range msg.ToolResults {
			estimated.HistoryTokens += e.estimateToolResultTokens(tr, provider)
		}

		// Count thinking content (extended thinking from models like Claude)
		if msg.Thinking != "" {
			estimated.HistoryTokens += e.estimateTokens(msg.Thinking, provider)
		}
	}

	// Estimate user message tokens
	estimated.UserMessageTokens = e.estimateTokens(userMessage, provider)

	// Calculate total with safety margin already included in ratios
	estimated.TotalEstimated = estimated.SystemPromptTokens +
		estimated.HistoryTokens +
		estimated.UserMessageTokens

	return estimated
}

// estimateTokens estimates token count for a single text string
func (e *TokenEstimator) estimateTokens(text string, provider string) int {
	if text == "" {
		return 0
	}

	// Get base chars/token ratio for provider
	charsPerToken := e.getCharsPerToken(provider)

	// Apply pattern adjustments
	adjustmentFactor := e.calculateAdjustmentFactor(text)
	adjustedCharsPerToken := charsPerToken * adjustmentFactor

	// Calculate estimate
	charCount := len(text)
	estimatedTokens := float64(charCount) / adjustedCharsPerToken

	// Round up to be conservative
	return int(estimatedTokens + 0.5)
}

// estimateToolCallTokens estimates token count for a tool call
func (e *TokenEstimator) estimateToolCallTokens(tc conversation.ToolCall, provider string) int {
	tokens := 0

	// Tool call ID: typically 10-20 tokens
	tokens += e.estimateTokens(tc.ID, provider)

	// Tool name: estimate from string length
	tokens += e.estimateTokens(tc.Name, provider)

	// Parameters: marshal to JSON and estimate
	if len(tc.Parameters) > 0 {
		if paramJSON, err := json.Marshal(tc.Parameters); err == nil {
			tokens += e.estimateTokens(string(paramJSON), provider)
		}
	}

	// ThoughtSignature (Gemini thinking models)
	if tc.ThoughtSignature != "" {
		tokens += e.estimateTokens(tc.ThoughtSignature, provider)
	}

	// Add overhead for tool call structure (XML tags, JSON formatting, etc.)
	// Providers wrap tool calls in special formats (e.g., <tool_use>, function_call)
	tokens += 10

	return tokens
}

// estimateToolResultTokens estimates token count for a tool result
func (e *TokenEstimator) estimateToolResultTokens(tr conversation.ToolResult, provider string) int {
	tokens := 0

	// Result ID linking to tool call
	tokens += e.estimateTokens(tr.CallID, provider)

	// Tool name (required by some providers like Gemini)
	if tr.Name != "" {
		tokens += e.estimateTokens(tr.Name, provider)
	}

	// Output text
	tokens += e.estimateTokens(tr.Output, provider)

	// Content blocks (images, audio, PDF, etc.)
	for _, content := range tr.Content {
		// Text content
		if content.Text != "" {
			tokens += e.estimateTokens(content.Text, provider)
		}

		// Binary content - rough estimate based on data size
		// Images/PDF/audio are typically tokenized differently
		if len(content.Data) > 0 {
			// Very rough estimate: binary content uses more tokens per byte
			// This is conservative - actual token count depends on content type
			tokens += len(content.Data) / 100 // ~100 bytes per token for binary
		}

		// URI/Name/Description
		if content.URI != "" {
			tokens += e.estimateTokens(content.URI, provider)
		}
		if content.Name != "" {
			tokens += e.estimateTokens(content.Name, provider)
		}
		if content.Description != "" {
			tokens += e.estimateTokens(content.Description, provider)
		}
	}

	// Error information
	if tr.Error != nil {
		tokens += e.estimateTokens(tr.Error.Type, provider)
		tokens += e.estimateTokens(tr.Error.Message, provider)
	}

	// Add overhead for tool result structure
	tokens += 10

	return tokens
}

// getCharsPerToken returns the chars/token ratio for a provider
func (e *TokenEstimator) getCharsPerToken(provider string) float64 {
	if ratio, ok := e.providerRatios[provider]; ok {
		return ratio
	}
	// Fallback to conservative estimate
	return e.providerRatios["unknown"]
}

// calculateAdjustmentFactor determines pattern-based adjustments
func (e *TokenEstimator) calculateAdjustmentFactor(text string) float64 {
	factors := []float64{}

	// Check for code blocks
	if hasCodeBlocks(text) {
		factors = append(factors, e.patternAdjustments["code_block"])
	} else if hasInlineCode(text) {
		factors = append(factors, e.patternAdjustments["inline_code"])
	}

	// Check for markdown
	if hasMarkdown(text) {
		factors = append(factors, e.patternAdjustments["markdown"])
	}

	// Check for high special character density
	if hasHighSpecialCharDensity(text) {
		factors = append(factors, e.patternAdjustments["high_special"])
	}

	// If no patterns detected, use plain text
	if len(factors) == 0 {
		return e.patternAdjustments["plain_text"]
	}

	// Average all applicable factors
	sum := 0.0
	for _, f := range factors {
		sum += f
	}
	return sum / float64(len(factors))
}

// GetProviderFromModel extracts provider name from model string
// Supports multiple formats:
// 1. "provider/model-name" (e.g., "anthropic/claude-3-opus")
// 2. "provider-model-name" (e.g., "openai-gpt-4")
// 3. Keyword-based detection for models that don't follow these formats
func GetProviderFromModel(model string) string {
	if model == "" {
		return "unknown"
	}

	model = strings.TrimSpace(model)
	// Strip common prefixes that aren't provider names
	model = strings.TrimPrefix(model, "models/")
	lowerModel := strings.ToLower(model)

	// Format 1: provider/model-name
	if strings.Contains(model, "/") {
		parts := strings.SplitN(model, "/", 2)
		if len(parts) == 2 && parts[0] != "" {
			provider := strings.ToLower(strings.TrimSpace(parts[0]))
			return normalizeProviderNameForDisplay(provider)
		}
	}

	// Format 2 & 3: Check for provider keywords/patterns
	// Order matters: check most specific patterns first

	// Local models (GGUF format) - check FIRST before llama detection
	if strings.HasSuffix(lowerModel, ".gguf") {
		return "local"
	}

	// Anthropic/Claude models
	if strings.Contains(lowerModel, "claude") ||
		strings.Contains(lowerModel, "sonnet") ||
		strings.Contains(lowerModel, "opus") ||
		strings.Contains(lowerModel, "haiku") {
		return "anthropic"
	}

	// OpenAI models
	if strings.Contains(lowerModel, "gpt") ||
		strings.Contains(lowerModel, "o1") ||
		strings.Contains(lowerModel, "o3") ||
		strings.HasPrefix(lowerModel, "openai") {
		return "openai"
	}

	// Google/Gemini models
	if strings.Contains(lowerModel, "gemini") {
		return "google"
	}

	// Grok (xAI)
	if strings.Contains(lowerModel, "grok") {
		return "xai"
	}

	// Deepseek models
	if strings.Contains(lowerModel, "deepseek") {
		return "deepseek"
	}

	// Meta/Llama models
	if strings.Contains(lowerModel, "llama") {
		return "meta"
	}

	// Qwen models - separate provider
	if strings.Contains(lowerModel, "qwen") {
		return "qwen"
	}

	// Zhipu models
	if strings.Contains(lowerModel, "glm") ||
		strings.Contains(lowerModel, "zhipu") ||
		strings.Contains(lowerModel, "zai") {
		return "zhipu"
	}

	// Mistral models
	if strings.Contains(lowerModel, "mistral") ||
		strings.Contains(lowerModel, "mixtral") {
		return "mistral"
	}

	// Cohere models
	if strings.Contains(lowerModel, "command") ||
		strings.Contains(lowerModel, "cohere") {
		return "cohere"
	}

	return "unknown"
}

// normalizeProviderName normalizes provider name variations
func normalizeProviderNameForDisplay(provider string) string {
	provider = strings.ToLower(strings.TrimSpace(provider))

	// Handle common aliases
	switch provider {
	case "gemini", "vertex", "vertex-ai":
		return "google"
	case "xai", "x.ai", "x-ai":
		return "xai"
	case "meta", "facebook", "fb":
		return "meta"
	case "alibaba":
		return "qwen"
	case "together.ai", "togetherai":
		return "together"
	case "fireworks.ai", "fireworksai":
		return "fireworks"
	default:
		return provider
	}
}

// Pattern detection helpers

func hasCodeBlocks(text string) bool {
	// Check for ``` or ~~~ code blocks
	return strings.Contains(text, "```") || strings.Contains(text, "~~~")
}

func hasInlineCode(text string) bool {
	// Check for single backticks (inline code)
	pattern := regexp.MustCompile("`[^`]+`")
	return pattern.MatchString(text)
}

func hasMarkdown(text string) bool {
	patterns := []string{
		`\*\*.*\*\*`,    // Bold
		`\*.*\*`,        // Italic
		`^#{1,6}\s`,     // Headers
		`^\s*[-*+]\s`,   // Lists
		`^\s*\d+\.\s`,   // Numbered lists
		`\[.*\]\(.*\)`,  // Links
		`!\[.*\]\(.*\)`, // Images
	}

	for _, pattern := range patterns {
		matched, _ := regexp.MatchString(pattern, text)
		if matched {
			return true
		}
	}

	return false
}

func hasHighSpecialCharDensity(text string) bool {
	if len(text) == 0 {
		return false
	}

	specialCount := 0
	for _, r := range text {
		if !((r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') ||
			(r >= '0' && r <= '9') || r == ' ' || r == '\n' || r == '\t') {
			specialCount++
		}
	}

	density := float64(specialCount) / float64(len(text))
	return density > 0.15 // More than 15% special characters
}

// EstimateTokensForText is a convenience function for quick estimates
func EstimateTokensForText(text, model string) int {
	estimator := NewTokenEstimator()
	provider := GetProviderFromModel(model)
	return estimator.estimateTokens(text, provider)
}
