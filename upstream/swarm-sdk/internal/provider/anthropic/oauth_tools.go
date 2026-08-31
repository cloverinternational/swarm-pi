package anthropic

import (
	"strings"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/conversation"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/provider"
)

// OAuth tool naming constants
const (
	// OAuthToolPrefix is prepended to tool names for OAuth requests.
	// This is required to avoid conflicts with Claude Code's built-in tools.
	OAuthToolPrefix = "swarm_"

	// OAuthUserAgent is the User-Agent header for OAuth requests.
	// Must match the current Claude Code CLI version to avoid detection.
	// Reference: @anthropic-ai/claude-code version 2.1.45
	OAuthUserAgent = "claude-cli/2.1.45 (external, cli)"

	// OAuthInterleavedThinkingBeta is the beta header for interleaved thinking.
	// Only used when extended thinking is enabled with OAuth requests.
	OAuthInterleavedThinkingBeta = "interleaved-thinking-2025-05-14"
)

// ApplyOAuthToolPrefix adds the OAuth prefix to a tool name if not already present.
// This is required for OAuth requests to properly route tool calls without
// conflicting with Claude Code's built-in tools.
func ApplyOAuthToolPrefix(toolName string) string {
	if strings.HasPrefix(toolName, OAuthToolPrefix) {
		return toolName // Already prefixed (idempotent)
	}
	return OAuthToolPrefix + toolName
}

// RemoveOAuthToolPrefix removes the OAuth prefix from a tool name if present.
// Used when translating API responses back to canonical format.
func RemoveOAuthToolPrefix(toolName string) string {
	return strings.TrimPrefix(toolName, OAuthToolPrefix)
}

// TransformToolsForOAuth applies OAuth prefixing to a list of provider tools.
// Returns a new slice with transformed tool names, leaving the original unchanged.
// NOTE: Beta tools (computer use, text editor, bash, web search) are NOT prefixed
// because the Anthropic API requires their names to be exactly as specified.
func TransformToolsForOAuth(tools []provider.Tool) []provider.Tool {
	if len(tools) == 0 {
		return tools
	}

	transformed := make([]provider.Tool, len(tools))
	for i, tool := range tools {
		transformedTool := tool
		// Beta tools have fixed names required by the API - don't prefix them
		if !isBetaToolType(tool.Type) {
			transformedTool.Name = ApplyOAuthToolPrefix(tool.Name)
		}
		transformed[i] = transformedTool
	}

	return transformed
}

// isBetaToolType checks if a tool type is a beta tool (computer use, text editor, bash, web search).
// Beta tools have fixed names required by the Anthropic API and should not be prefixed.
func isBetaToolType(toolType string) bool {
	return toolType == ToolTypeComputerUse ||
		toolType == ToolTypeTextEditor ||
		toolType == ToolTypeBash ||
		toolType == ToolTypeWebSearch
}

// RemoveOAuthPrefixFromToolCalls removes OAuth prefixes from response tool calls.
// Used to restore canonical tool names before returning to SDK consumers.
func RemoveOAuthPrefixFromToolCalls(toolCalls []conversation.ToolCall) {
	for i := range toolCalls {
		toolCalls[i].Name = RemoveOAuthToolPrefix(toolCalls[i].Name)
	}
}
