// Package anthropic_web_search provides Anthropic's server-side web search tool integration.
// This tool leverages Claude's built-in web search capability via the web-search beta flag.
//
// The tool uses the stored OAuth token from Claude Code's authentication flow,
// allowing users to perform web searches using their existing Claude Code login.
//
// Key features:
// - Server-side web search powered by Anthropic's infrastructure
// - Uses Claude Code OAuth token for authentication
// - Rate limiting and usage tracking
// - Automatic token refresh on expiry
//
// # Usage
//
// To create a new web search tool:
//
//	tool := anthropic_web_search.New()
//	// or with custom config:
//	tool := anthropic_web_search.NewWithConfig(anthropic_web_search.WebSearchConfig{
//	    Enabled:            true,
//	    MaxResultsPerQuery: 10,
//	    Timeout:            30 * time.Second,
//	    MaxPerSession:      100,
//	    MaxPerMinute:       10,
//	})
//
// To register the tool with a registry:
//
//	registry.RegisterTool(anthropic_web_search.New())
//
// # Authentication
//
// This tool requires the user to be logged in with Claude Code. The OAuth token
// is stored at ~/.swarm/config/oauth/anthropic.json (paths.OAuthFile) and is
// automatically used by the tool.
// If the token is expired, the tool will attempt to refresh it.
//
// To check if authentication is available:
//
//	if anthropic_web_search.IsAuthConfigured() {
//	    // Web search is available
//	}
//
// # Rate Limiting
//
// The tool implements rate limiting to prevent abuse:
// - MaxPerSession: Maximum searches per client instance
// - MaxPerMinute: Maximum searches per minute
//
// Use the GetUsageStats method to check current usage.
package anthropic_web_search

import (
	"github.com/Swarm-Code/mono/swarm-sdk/internal/provider/anthropic"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/tools"
)

// GetWebSearchTools returns all web search related tools.
// Currently this returns only the main web search tool, but may include
// additional tools in the future (e.g., specialized searches, results filtering).
func GetWebSearchTools() []tools.Tool {
	return []tools.Tool{
		New(),
	}
}

// GetWebSearchToolsWithConfig returns web search tools with custom configuration.
func GetWebSearchToolsWithConfig(config WebSearchConfig) []tools.Tool {
	return []tools.Tool{
		NewWithConfig(config),
	}
}

// IsAuthConfigured checks if OAuth authentication is configured.
// Returns true if a valid OAuth token exists, false otherwise.
// Use this to check if web search functionality is available before
// registering the tool or attempting to use it.
func IsAuthConfigured() bool {
	token, err := anthropic.GetStoredOAuthToken()
	if err != nil {
		return false
	}
	if token == nil || token.AccessToken == "" {
		return false
	}
	// Token exists - it might be expired but that's handled during execution
	return true
}

// RefreshAuthIfNeeded attempts to refresh the OAuth token if it's expired.
// Returns nil if the token is valid or was successfully refreshed.
// Returns an error if the token couldn't be refreshed.
func RefreshAuthIfNeeded() error {
	token, err := anthropic.GetStoredOAuthToken()
	if err != nil {
		return err
	}

	if token == nil || token.AccessToken == "" {
		return nil // No token to refresh
	}

	if anthropic.IsTokenExpired(token) {
		_, err := anthropic.RefreshAndStoreToken()
		return err
	}

	return nil
}
