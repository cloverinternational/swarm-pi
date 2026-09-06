package chat

import (
	"testing"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/provider"
)

// ---------------------------------------------------------------------------
// resolveAPITypeFromName — unit tests
// ---------------------------------------------------------------------------

func TestResolveAPITypeFromName_FirstClassProviders(t *testing.T) {
	tests := []struct {
		name    string
		want    string
		wantURL string
	}{
		{"openai", "openai", "https://api.openai.com/v1"}, // SDK registry provides the base URL
		{"anthropic", "anthropic", ""},                    // Anthropic not in openai profiles registry, uses legacy fallback
		{"gemini", "gemini", ""},                          // Gemini not in openai profiles registry, uses legacy fallback
	}
	for _, tt := range tests {
		got := resolveAPITypeFromName(tt.name)
		if got.APIType != tt.want {
			t.Errorf("resolveAPITypeFromName(%q).APIType = %q, want %q", tt.name, got.APIType, tt.want)
		}
		if got.DefaultBaseURL != tt.wantURL {
			t.Errorf("resolveAPITypeFromName(%q).DefaultBaseURL = %q, want %q", tt.name, got.DefaultBaseURL, tt.wantURL)
		}
	}
}

func TestResolveAPITypeFromName_OpenAICompatible(t *testing.T) {
	tests := []struct {
		name    string
		wantURL string
	}{
		{"cerebras", "https://api.cerebras.ai/v1"},
		{"fireworks", "https://api.fireworks.ai/inference/v1"},
		{"groq", "https://api.groq.com/openai/v1"},
		{"openrouter", "https://openrouter.ai/api/v1"},
		{"together", "https://api.together.xyz/v1"},
		{"deepseek", "https://api.deepseek.com/v1"},
		{"perplexity", "https://api.perplexity.ai"},
		{"z.ai", "https://api.z.ai/api/coding/paas/v4"},
		{"wafer.ai", "https://pass.wafer.ai/v1"},
		{"mistral", "https://api.mistral.ai/v1"},
		{"moonshot", "https://api.moonshot.cn/v1"},
		{"replicate", "https://api.replicate.com/v1"},
		{"chutes", "https://llm.chutes.ai/v1"},
		{"qwen", "https://dashscope.aliyuncs.com/compatible-mode/v1"},
	}
	for _, tt := range tests {
		got := resolveAPITypeFromName(tt.name)
		if got.APIType != "openai-compatible" {
			t.Errorf("resolveAPITypeFromName(%q).APIType = %q, want openai-compatible", tt.name, got.APIType)
		}
		if got.DefaultBaseURL != tt.wantURL {
			t.Errorf("resolveAPITypeFromName(%q).DefaultBaseURL = %q, want %q", tt.name, got.DefaultBaseURL, tt.wantURL)
		}
	}
}

func TestResolveAPITypeFromName_OpenAICompatibleNoBaseURL(t *testing.T) {
	// These are OpenAI-compatible but have no well-known default base URL.
	// Note: "xai" is now a first-class provider with its own api_type and base URL.
	for _, name := range []string{"kimi", "meta", "zhipu"} {
		got := resolveAPITypeFromName(name)
		if got.APIType != "openai-compatible" {
			t.Errorf("resolveAPITypeFromName(%q).APIType = %q, want openai-compatible", name, got.APIType)
		}
		if got.DefaultBaseURL != "" {
			t.Errorf("resolveAPITypeFromName(%q).DefaultBaseURL = %q, want empty", name, got.DefaultBaseURL)
		}
	}
}

func TestResolveAPITypeFromName_AnthropicCompatible(t *testing.T) {
	got := resolveAPITypeFromName("minimax")
	if got.APIType != "anthropic" {
		t.Errorf("resolveAPITypeFromName(\"minimax\").APIType = %q, want anthropic", got.APIType)
	}
	if got.DefaultBaseURL != "https://api.minimax.io/anthropic" {
		t.Errorf("resolveAPITypeFromName(\"minimax\").DefaultBaseURL = %q, want https://api.minimax.io/anthropic", got.DefaultBaseURL)
	}
}

func TestResolveAPITypeFromName_Local(t *testing.T) {
	got := resolveAPITypeFromName("local")
	if got.APIType != "openai-compatible" {
		t.Errorf("resolveAPITypeFromName(\"local\").APIType = %q, want openai-compatible", got.APIType)
	}
	if got.DefaultBaseURL != "" {
		t.Errorf("resolveAPITypeFromName(\"local\").DefaultBaseURL = %q, want empty (user sets it)", got.DefaultBaseURL)
	}
}

// ---------------------------------------------------------------------------
// Alias resolution — verify that common display names route correctly
// ---------------------------------------------------------------------------

func TestResolveAPITypeFromName_Aliases(t *testing.T) {
	// These all go through NormalizeProviderName first
	tests := []struct {
		input   string
		wantAPI string
	}{
		// Anthropic aliases
		{"ClaudeCode", "anthropic"},
		{"Claude Code", "anthropic"},
		{"claudecode", "anthropic"},
		{"claude", "anthropic"},

		// OpenAI aliases
		{"Codex", "openai"},
		{"codex", "openai"},

		// Gemini aliases
		{"Google", "gemini"},
		{"google", "gemini"},
		{"gemini-code-assist", "gemini"},

		// Z.AI aliases
		{"GLM", "openai-compatible"}, // NormalizeProviderName("glm") → "z.ai"
		{"zai", "openai-compatible"}, // NormalizeProviderName("zai") → "z.ai"

		// Together aliases
		{"Together AI", "openai-compatible"}, // NormalizeProviderName → "together"
		{"together-ai", "openai-compatible"},
		{"togetherai", "openai-compatible"},

		// Local aliases
		{"Ollama", "openai-compatible"}, // NormalizeProviderName("ollama") → "local"

		// Wafer aliases
		{"Wafer", "openai-compatible"}, // NormalizeProviderName("wafer") → "wafer.ai"
		{"wafer.ai", "openai-compatible"},

		// XAI aliases — now first-class with own api_type
		{"Grok", "xai"}, // NormalizeProviderName("grok") → "xai" (first-class provider)

		// Meta aliases
		{"Llama", "openai-compatible"}, // NormalizeProviderName("llama") → "meta"
	}
	for _, tt := range tests {
		got := resolveAPITypeFromName(tt.input)
		if got.APIType != tt.wantAPI {
			t.Errorf("resolveAPITypeFromName(%q).APIType = %q, want %q", tt.input, got.APIType, tt.wantAPI)
		}
	}
}

// ---------------------------------------------------------------------------
// Edge cases
// ---------------------------------------------------------------------------

func TestResolveAPITypeFromName_UnknownProvider(t *testing.T) {
	// Completely unknown provider name — should default to openai-compatible
	got := resolveAPITypeFromName("some-new-llm")
	if got.APIType != "openai-compatible" {
		t.Errorf("resolveAPITypeFromName(\"some-new-llm\").APIType = %q, want openai-compatible", got.APIType)
	}
	if got.DefaultBaseURL != "" {
		t.Errorf("resolveAPITypeFromName(\"some-new-llm\").DefaultBaseURL = %q, want empty", got.DefaultBaseURL)
	}
}

func TestResolveAPITypeFromName_EmptyString(t *testing.T) {
	// Empty string — NormalizeProviderName("") returns "", which hits default
	got := resolveAPITypeFromName("")
	if got.APIType != "openai-compatible" {
		t.Errorf("resolveAPITypeFromName(\"\").APIType = %q, want openai-compatible", got.APIType)
	}
}

func TestResolveAPITypeFromName_CaseInsensitivity(t *testing.T) {
	// NormalizeProviderName lowercases, so all of these should work
	for _, name := range []string{"Fireworks", "FIREWORKS", "FiReWoRkS"} {
		got := resolveAPITypeFromName(name)
		if got.APIType != "openai-compatible" {
			t.Errorf("resolveAPITypeFromName(%q).APIType = %q, want openai-compatible", name, got.APIType)
		}
		if got.DefaultBaseURL != "https://api.fireworks.ai/inference/v1" {
			t.Errorf("resolveAPITypeFromName(%q).DefaultBaseURL = %q, want default", name, got.DefaultBaseURL)
		}
	}
}

func TestResolveAPITypeFromName_Whitespace(t *testing.T) {
	// Leading/trailing whitespace should be trimmed by NormalizeProviderName
	got := resolveAPITypeFromName("  fireworks  ")
	if got.APIType != "openai-compatible" {
		t.Errorf("resolveAPITypeFromName(\"  fireworks  \").APIType = %q, want openai-compatible", got.APIType)
	}
}

// ---------------------------------------------------------------------------
// Consistency checks — make sure resolveAPITypeFromName is consistent
// with the SDK's NormalizeProviderName
// ---------------------------------------------------------------------------

func TestResolveAPITypeFromName_ConsistentWithNormalize(t *testing.T) {
	// Every name that NormalizeProviderName recognizes should be handled
	// by resolveAPITypeFromName without returning a surprising api_type.
	knownProviders := []string{
		"anthropic", "claudecode", "claude", "claude code",
		"openai", "codex",
		"gemini", "google", "gemini-code-assist",
		"cerebras", "openrouter", "fireworks", "groq",
		"together", "together-ai", "togetherai", "together ai",
		"deepseek", "perplexity",
		"z.ai", "zai", "glm",
		"mistral", "moonshot", "replicate", "chutes",
		"qwen", "kimi",
		"xai", "grok",
		"meta", "llama",
		"local", "ollama",
		"zhipu", "minimax",
		"wafer", "wafer.ai",
	}

	for _, name := range knownProviders {
		result := resolveAPITypeFromName(name)
		switch result.APIType {
		case "openai", "anthropic", "gemini", "openai-compatible", "xai":
			// All valid — "xai" is a first-class provider with SuperGrok OAuth support.
		default:
			t.Errorf("resolveAPITypeFromName(%q) returned unexpected api_type %q", name, result.APIType)
		}
	}
}

// ---------------------------------------------------------------------------
// Coverage: ensure every NormalizeProviderName canonical output
// that the SDK's getCompatibilityType maps to "openai" also gets
// "openai-compatible" from our resolver (except "openai" itself).
// ---------------------------------------------------------------------------

func TestResolveAPITypeFromName_SDKCompatibileProvidersGetOpenAICompatible(t *testing.T) {
	// The SDK's getCompatibilityType (compatibility.go) maps these to "openai":
	sdkOpenAICompat := []string{
		"codex", "z.ai", "zai", "glm", "cerebras", "openrouter",
		"fireworks", "groq", "deepseek", "mistral", "together",
		"moonshot", "replicate", "chutes", "qwen", "kimi",
		"wafer", "wafer.ai",
	}

	for _, name := range sdkOpenAICompat {
		result := resolveAPITypeFromName(name)
		if result.APIType != "openai-compatible" && result.APIType != "openai" {
			t.Errorf("provider %q: SDK maps to openai-compatible but resolveAPITypeFromName returned %q", name, result.APIType)
		}
		// Only "openai" and "codex" should resolve to "openai" (the real OpenAI)
		normalized := provider.NormalizeProviderName(name)
		if normalized == "openai" {
			if result.APIType != "openai" {
				t.Errorf("provider %q (normalized=%q) should map to openai, got %q", name, normalized, result.APIType)
			}
		} else {
			// All other OpenAI-compatible providers must NOT resolve to "openai"
			// to avoid them accidentally using OPENAI_API_KEY
			if result.APIType == "openai" {
				t.Errorf("provider %q (normalized=%q) should map to openai-compatible, not openai", name, normalized)
			}
		}
	}
}

// ---------------------------------------------------------------------------
// The exact bug scenario: providers.json entry with empty api_type
// ---------------------------------------------------------------------------

func TestResolveAPITypeFromName_BugScenario_EmptyAPIType(t *testing.T) {
	// Simulates the exact scenario from the bug report:
	// providers.json has "fireworks" entry but no api_type field.
	// resolveAPITypeFromName should still resolve it correctly.
	providersMissingAPIType := []string{
		"fireworks",
		"groq",
		"deepseek",
		"cerebras",
		"openrouter",
		"together",
		"mistral",
		"perplexity",
	}

	for _, name := range providersMissingAPIType {
		result := resolveAPITypeFromName(name)
		if result.APIType == "" {
			t.Errorf("BUG: resolveAPITypeFromName(%q) returned empty api_type — this is the exact bug!", name)
		}
		if result.APIType == "openai" {
			t.Errorf("BUG: resolveAPITypeFromName(%q) returned 'openai' — should be 'openai-compatible' to avoid using OPENAI_API_KEY", name)
		}
	}
}
