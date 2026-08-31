// Package websearch provides a unified web search tool with Anthropic and Exa backends.
//
// # Usage
//
// Register the tool with a registry:
//
//	registry.RegisterTool(websearch.New())
//
// Or get all tools (may include future variants):
//
//	for _, t := range websearch.GetWebSearchTools() {
//	    registry.RegisterTool(t)
//	}
//
// # Authentication
//
// Anthropic backend: requires Claude Code OAuth (run 'claude login').
// Exa backend:       requires EXA_API_KEY environment variable or explicit config.
//
// Check availability before registering:
//
//	if websearch.IsAnthropicAuthConfigured() {
//	    registry.RegisterTool(websearch.New())
//	}
package websearch

import (
	"os"
	"time"

	"github.com/Swarm-Code/mono/swarm-core/core"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/provider/anthropic"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/tools"
)

// NewFromCoreConfig builds a websearch tool driven by the core config.
// It respects PreferredBackend, ExaSearchType, MaxResults, and TimeoutSeconds.
// exaAPIKey is the API key from providers.json (empty string if not configured).
func NewFromCoreConfig(cfg *core.WebSearchConfig, exaAPIKey string) *Tool {
	if cfg == nil {
		cfg = core.DefaultWebSearchConfig()
	}

	// Resolve the effective backend.
	backend := BackendAnthropic
	switch cfg.PreferredBackend {
	case "exa":
		backend = BackendExa
	case "anthropic":
		backend = BackendAnthropic
	default: // "auto" or empty
		if exaAPIKey != "" || os.Getenv("EXA_API_KEY") != "" {
			backend = BackendExa
		}
	}

	// Resolve Exa API key.
	if backend == BackendExa && exaAPIKey == "" {
		exaAPIKey = os.Getenv("EXA_API_KEY")
	}

	// Apply overrides with sensible defaults.
	maxResults := cfg.MaxResults
	if maxResults <= 0 {
		maxResults = 10
	}
	timeoutSecs := cfg.TimeoutSeconds
	if timeoutSecs <= 0 {
		timeoutSecs = 30
	}
	searchType := cfg.ExaSearchType
	if searchType == "" {
		searchType = "auto"
	}

	wsCfg := WebSearchConfig{
		Backend:            backend,
		ExaAPIKey:          exaAPIKey,
		ExaBaseURL:         DefaultExaBaseURL,
		ExaSearchType:      searchType,
		Enabled:            true,
		MaxResultsPerQuery: maxResults,
		Timeout:            time.Duration(timeoutSecs) * time.Second,
		MaxPerSession:      100,
		MaxPerMinute:       10,
	}
	return &Tool{config: wsCfg}
}

// GetWebSearchTools returns all websearch tool instances.
// The returned slice contains a single tool configured via auto-detection:
// Exa backend when EXA_API_KEY is set, Anthropic OAuth backend otherwise.
func GetWebSearchTools() []tools.Tool {
	return []tools.Tool{New()}
}

// GetWebSearchToolsWithConfig returns websearch tools with custom configuration.
func GetWebSearchToolsWithConfig(config WebSearchConfig) []tools.Tool {
	return []tools.Tool{NewWithConfig(config)}
}

// IsAnthropicAuthConfigured checks if Claude Code OAuth is set up.
func IsAnthropicAuthConfigured() bool {
	token, err := anthropic.GetStoredOAuthToken()
	if err != nil || token == nil || token.AccessToken == "" {
		return false
	}
	return true
}

// IsExaAuthConfigured checks if the Exa API key is available.
func IsExaAuthConfigured() bool {
	return os.Getenv("EXA_API_KEY") != ""
}

// IsAuthConfigured returns true when at least one backend is ready to use.
func IsAuthConfigured() bool {
	return IsAnthropicAuthConfigured() || IsExaAuthConfigured()
}

// RefreshAuthIfNeeded refreshes the Anthropic OAuth token if it is expired.
func RefreshAuthIfNeeded() error {
	token, err := anthropic.GetStoredOAuthToken()
	if err != nil || token == nil || token.AccessToken == "" {
		return nil
	}
	if anthropic.IsTokenExpired(token) {
		_, err = anthropic.RefreshAndStoreToken()
		return err
	}
	return nil
}
