package client

import (
	"encoding/json"
	"os"
	"strings"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/paths"
)

// customProviderInfo describes a user-defined provider name that points at one
// of the canonical underlying APIs (anthropic / openai / gemini /
// openai-compatible). Users add these via the TUI settings UI; they live in
// ~/.swarm/config/providers.json (legacy array format) or ~/.swarm/config/providers.json
// (SDK map format).
type customProviderInfo struct {
	APIType        string // "anthropic" | "openai" | "openai-compatible" | "gemini"
	BaseURL        string
	HTTPMaxRetries *int
}

// canonicalProviderForAPIType maps the api_type field stored in providers.json
// to the canonical provider name registered in the SDK's provider registry.
// Returns "" for unknown api_types.
func canonicalProviderForAPIType(apiType string) string {
	switch strings.ToLower(strings.TrimSpace(apiType)) {
	case "anthropic":
		return "anthropic"
	case "gemini", "google":
		return "gemini"
	case "openai":
		return "openai"
	case "openai-compatible", "openai_compatible":
		// Treated as "openai" for factory routing — the openai factory
		// is what speaks the OpenAI-compatible HTTP protocol. The custom
		// base URL and credentials come from providers.json/credentials.json.
		return "openai"
	}
	return ""
}

// lookupCustomProvider returns the api_type and base_url for a user-defined
// provider name found in either ~/.swarm/config/providers.json (map format)
// or ~/.swarm/config/providers.json (legacy array format).
//
// The match is case-insensitive on the provider name. If the exact name is not
// found, the function falls back to the SDK-normalized form (e.g., "wafer" ->
// "wafer.ai") to handle providers whose canonical name includes a domain suffix.
//
// If the name is unknown in both files, returns (zero, false).
func lookupCustomProvider(name string) (customProviderInfo, bool) {
	norm := strings.ToLower(strings.TrimSpace(name))
	if norm == "" {
		return customProviderInfo{}, false
	}

	// Compute the SDK-normalized form for fallback matching.
	normalized := normalizeProviderName(norm)

	// 1. SDK config map format: ~/.swarm/config/providers.json
	if data, err := os.ReadFile(paths.ProvidersFile()); err == nil {
		var asMap map[string]struct {
			Name           string `json:"name"`
			APIType        string `json:"api_type"`
			BaseURL        string `json:"base_url"`
			HTTPMaxRetries *int   `json:"http_max_retries"`
		}
		if json.Unmarshal(data, &asMap) == nil {
			for key, p := range asMap {
				lcKey := strings.ToLower(strings.TrimSpace(key))
				lcName := strings.ToLower(strings.TrimSpace(p.Name))
				if p.APIType == "" {
					continue
				}
				if lcKey == norm || lcName == norm {
					return customProviderInfo{APIType: p.APIType, BaseURL: p.BaseURL, HTTPMaxRetries: p.HTTPMaxRetries}, true
				}
				// Fallback: try normalized form (e.g., "wafer" -> "wafer.ai").
				if normalized != norm && (lcKey == normalized || lcName == normalized) {
					return customProviderInfo{APIType: p.APIType, BaseURL: p.BaseURL, HTTPMaxRetries: p.HTTPMaxRetries}, true
				}
			}
		}
	}

	// 2. Legacy array format: same canonical ~/.swarm/config/providers.json
	if data, err := os.ReadFile(paths.ProvidersFile()); err == nil {
		var asArr []struct {
			Name           string `json:"name"`
			DisplayName    string `json:"display_name"`
			APIType        string `json:"api_type"`
			BaseURL        string `json:"base_url"`
			HTTPMaxRetries *int   `json:"http_max_retries"`
		}
		if json.Unmarshal(data, &asArr) == nil {
			for _, p := range asArr {
				if p.APIType == "" {
					continue
				}
				pName := strings.ToLower(strings.TrimSpace(p.Name))
				pDisplay := strings.ToLower(strings.TrimSpace(p.DisplayName))
				if pName == norm || pDisplay == norm {
					return customProviderInfo{APIType: p.APIType, BaseURL: p.BaseURL, HTTPMaxRetries: p.HTTPMaxRetries}, true
				}
				// Fallback: try normalized form (e.g., "wafer" -> "wafer.ai").
				if normalized != norm && (pName == normalized || pDisplay == normalized) {
					return customProviderInfo{APIType: p.APIType, BaseURL: p.BaseURL, HTTPMaxRetries: p.HTTPMaxRetries}, true
				}
			}
		}
	}

	return customProviderInfo{}, false
}
