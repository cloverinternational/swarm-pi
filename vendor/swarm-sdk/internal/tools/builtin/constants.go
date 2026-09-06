package builtin

import "time"

// Shared constants for tool output limits
// Token estimation: 1 token ≈ 4 characters
const (
	// CharsPerToken is the approximate character to token ratio
	CharsPerToken = 4

	// MaxOutputTokens is the maximum tokens allowed per tool result
	MaxOutputTokens = 25000

	// MaxOutputSize is calculated from token limit (25k tokens × 4 chars = 100k chars)
	// This prevents sending too much data to the AI in a single tool call
	MaxOutputSize = MaxOutputTokens * CharsPerToken // 100,000 chars

	// GrepCommandTimeout is the default timeout for grep/ripgrep commands
	GrepCommandTimeout = 30 * time.Second
)
