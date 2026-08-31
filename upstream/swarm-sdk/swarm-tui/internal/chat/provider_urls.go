package chat

import "strings"

const defaultCerebrasBaseURL = "https://api.cerebras.ai/v1"
const defaultOpenAIOAuthCodexBaseURL = "https://chatgpt.com/backend-api/codex"

// defaultXAIBaseURL is the canonical xAI API endpoint (OpenAI-compatible).
const defaultXAIBaseURL = "https://api.x.ai/v1"

func normalizeCerebrasBaseURL(raw string) string {
	base := strings.TrimSpace(raw)
	if base == "" {
		return defaultCerebrasBaseURL
	}

	base = strings.TrimRight(base, "/")

	if before, ok := strings.CutSuffix(base, "/chat/completions"); ok {
		base = before
		base = strings.TrimRight(base, "/")
	}

	if !strings.HasSuffix(base, "/v1") {
		base = base + "/v1"
	}

	return base
}

// normalizeOpenAIOAuthBaseURL sanitizes OpenAI OAuth base URLs for Codex-backed calls.
//
// For OAuth, Codex access tokens should target the ChatGPT Codex backend, not
// https://api.openai.com/v1. Returning an empty string means "use the Codex
// provider default" (https://chatgpt.com/backend-api/codex).
func normalizeOpenAIOAuthBaseURL(raw string) string {
	base := strings.TrimSpace(raw)
	if base == "" {
		return ""
	}

	base = strings.TrimRight(base, "/")

	// Handle endpoint-shaped values and normalize back to a base URL.
	for _, suffix := range []string{"/chat/completions", "/responses"} {
		if before, ok := strings.CutSuffix(base, suffix); ok {
			base = before
			base = strings.TrimRight(base, "/")
		}
	}

	lower := strings.ToLower(base)
	if lower == "https://api.openai.com" || lower == "https://api.openai.com/v1" {
		return ""
	}

	return base
}
