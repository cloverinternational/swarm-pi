// Package anthropic provides advanced tool definitions for web search.
package anthropic

import (
	"encoding/json"
	"fmt"

	sdkerr "github.com/Swarm-Code/mono/swarm-sdk/internal/sdkerr"
)

// WebSearchTool represents the web search tool configuration.
// Type: web_search_20250305
// Beta header: web-search-2025-03-05
type WebSearchTool struct {
	Name           string        `json:"name"`                      // Must be "web_search"
	Type           string        `json:"type"`                      // Must be "web_search_20250305"
	AllowedDomains []string      `json:"allowed_domains,omitempty"` // Domains to include (mutually exclusive with BlockedDomains)
	BlockedDomains []string      `json:"blocked_domains,omitempty"` // Domains to exclude (mutually exclusive with AllowedDomains)
	CacheControl   *CacheControl `json:"cache_control,omitempty"`   // Ephemeral cache control
	MaxUses        *int          `json:"max_uses,omitempty"`        // Maximum number of uses
	UserLocation   *UserLocation `json:"user_location,omitempty"`   // User location for localized results
}

// UserLocation specifies the user's location for localized search results.
type UserLocation struct {
	Type     string  `json:"type"`               // Must be "approximate"
	City     *string `json:"city,omitempty"`     // City name
	Country  *string `json:"country,omitempty"`  // Two-letter ISO country code (e.g., "US", "GB")
	Region   *string `json:"region,omitempty"`   // Region/state
	Timezone *string `json:"timezone,omitempty"` // IANA timezone (e.g., "America/New_York")
}

// WebSearchResult represents a single search result item.
type WebSearchResult struct {
	Type             string  `json:"type"`               // Must be "web_search_result"
	Title            string  `json:"title"`              // Title of the search result page
	URL              string  `json:"url"`                // URL of the search result
	EncryptedContent string  `json:"encrypted_content"`  // Encrypted content snippet
	PageAge          *string `json:"page_age,omitempty"` // Age of the page (e.g., "1d", "2h")
}

// WebSearchToolResult represents the result of a web search tool execution.
type WebSearchToolResult struct {
	Type         string            `json:"type"`                    // Must be "web_search_tool_result"
	ToolUseID    string            `json:"tool_use_id"`             // ID of the tool use this result corresponds to
	Content      []WebSearchResult `json:"content"`                 // Array of search results
	CacheControl *CacheControl     `json:"cache_control,omitempty"` // Cache control settings
}

// WebSearchToolError represents an error during web search execution.
type WebSearchToolError struct {
	Type      string `json:"type"`        // Must be "web_search_tool_result_error"
	ToolUseID string `json:"tool_use_id"` // ID of the tool use this error corresponds to
	ErrorCode string `json:"error_code"`  // Error code: "invalid_tool_input", "unavailable", "max_uses_exceeded", "too_many_requests", "query_too_long"
}

// WebSearchToolConfig holds configuration options for creating a web search tool.
type WebSearchToolConfig struct {
	AllowedDomains []string      // Optional: Domains to include in results
	BlockedDomains []string      // Optional: Domains to exclude from results
	CacheTTL       string        // Optional: Cache TTL ("5m" or "1h")
	MaxUses        *int          // Optional: Maximum number of uses
	UserLocation   *UserLocation // Optional: User location for localized results
}

// CreateWebSearchTool creates a web search tool definition with the specified configuration.
// Returns an Anthropic-specific WebSearchTool that should be marshaled to JSON and passed
// in the tools array of the message request.
func CreateWebSearchTool(config WebSearchToolConfig) (*WebSearchTool, error) {
	// Validate mutually exclusive domain filters
	if len(config.AllowedDomains) > 0 && len(config.BlockedDomains) > 0 {
		return nil, sdkerr.Permanent(
			"anthropic.web_search.invalid_config",
			"allowed_domains and blocked_domains are mutually exclusive",
		)
	}

	tool := &WebSearchTool{
		Name:           "web_search",
		Type:           "web_search_20250305",
		AllowedDomains: config.AllowedDomains,
		BlockedDomains: config.BlockedDomains,
		MaxUses:        config.MaxUses,
	}

	// Add cache control if specified
	if config.CacheTTL != "" {
		if config.CacheTTL != "5m" && config.CacheTTL != "1h" {
			return nil, sdkerr.Permanent(
				"anthropic.web_search.invalid_cache_ttl",
				fmt.Sprintf("cache TTL must be '5m' or '1h', got '%s'", config.CacheTTL),
			)
		}
		tool.CacheControl = &CacheControl{
			Type: "ephemeral",
			TTL:  config.CacheTTL,
		}
	}

	// Add user location if specified
	if config.UserLocation != nil {
		if err := validateUserLocation(config.UserLocation); err != nil {
			return nil, err
		}
		tool.UserLocation = config.UserLocation
	}

	return tool, nil
}

// ParseWebSearchResult parses a web search tool result from the API response.
func ParseWebSearchResult(contentBlock ContentBlock) (*WebSearchToolResult, error) {
	if contentBlock.Type != "web_search_tool_result" {
		return nil, sdkerr.Permanent(
			"anthropic.web_search.invalid_type",
			fmt.Sprintf("expected type 'web_search_tool_result', got '%s'", contentBlock.Type),
		)
	}

	// Parse content as JSON
	var results []WebSearchResult
	if contentBlock.Content != nil {
		contentBytes, err := json.Marshal(contentBlock.Content)
		if err != nil {
			return nil, sdkerr.Permanent(
				"anthropic.web_search.parse_error",
				fmt.Sprintf("failed to marshal content: %v", err),
			)
		}

		if err := json.Unmarshal(contentBytes, &results); err != nil {
			return nil, sdkerr.Permanent(
				"anthropic.web_search.parse_error",
				fmt.Sprintf("failed to unmarshal search results: %v", err),
			)
		}
	}

	return &WebSearchToolResult{
		Type:         "web_search_tool_result",
		ToolUseID:    contentBlock.ToolUseID,
		Content:      results,
		CacheControl: contentBlock.CacheControl,
	}, nil
}

// ParseWebSearchError parses a web search tool error from the API response.
func ParseWebSearchError(contentBlock ContentBlock) (*WebSearchToolError, error) {
	if contentBlock.Type != "web_search_tool_result_error" {
		return nil, sdkerr.Permanent(
			"anthropic.web_search.invalid_error_type",
			fmt.Sprintf("expected type 'web_search_tool_result_error', got '%s'", contentBlock.Type),
		)
	}

	// Extract error code from content
	errorCode := ""
	if contentBlock.Content != nil {
		if contentMap, ok := contentBlock.Content.(map[string]any); ok {
			if code, exists := contentMap["error_code"]; exists {
				errorCode = fmt.Sprintf("%v", code)
			}
		}
	}

	return &WebSearchToolError{
		Type:      "web_search_tool_result_error",
		ToolUseID: contentBlock.ToolUseID,
		ErrorCode: errorCode,
	}, nil
}

// validateUserLocation validates user location configuration.
func validateUserLocation(loc *UserLocation) error {
	if loc == nil {
		return nil
	}

	// Type must be "approximate"
	if loc.Type != "approximate" {
		return sdkerr.Permanent(
			"anthropic.web_search.invalid_location_type",
			fmt.Sprintf("user location type must be 'approximate', got '%s'", loc.Type),
		)
	}

	// Country must be 2-letter ISO code if specified
	if loc.Country != nil && len(*loc.Country) != 2 {
		return sdkerr.Permanent(
			"anthropic.web_search.invalid_country_code",
			fmt.Sprintf("country must be 2-letter ISO code, got '%s'", *loc.Country),
		)
	}

	return nil
}

// ExtractCitationsFromWebSearch extracts citations from web search results.
// Web search results automatically produce citations with type "web_search_result_location".
func ExtractCitationsFromWebSearch(results []WebSearchResult) []Citation {
	var citations []Citation

	for i, result := range results {
		citation := Citation{
			Type:              "web_search_result_location",
			URL:               result.URL,
			Title:             result.Title,
			SearchResultIndex: &i,
		}

		// Add encrypted index if available
		if result.EncryptedContent != "" {
			citation.EncryptedIndex = result.EncryptedContent
		}

		citations = append(citations, citation)
	}

	return citations
}
