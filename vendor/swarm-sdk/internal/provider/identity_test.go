package provider

import "testing"

// TestProviderNamingSourceOfTruth pins the single-source-of-truth invariants
// for provider naming after the four legacy tables (StandardAliases,
// NormalizeProviderName switch, builtinCompatibilityType switch) were collapsed
// into the providerAPIType + providerCanonical maps in identity.go.
//
// If someone re-introduces a divergent hand-maintained table, these assertions
// fail loudly.

// TestNormalizeProviderName_KnownInputs pins the exact canonicalization the
// legacy switch produced for every known alias, plus the unknown/empty edges.
func TestNormalizeProviderName_KnownInputs(t *testing.T) {
	cases := map[string]string{
		// Anthropic
		"claudecode": "anthropic", "anthropic": "anthropic", "claude": "anthropic",
		"claude code": "anthropic", "ClaudeCode": "anthropic", "  Anthropic  ": "anthropic",
		// OpenAI
		"openai": "openai", "codex": "openai",
		// Gemini / Google
		"gemini": "gemini", "google": "gemini", "gemini-code-assist": "gemini",
		// Single-name
		"cerebras": "cerebras", "openrouter": "openrouter", "fireworks": "fireworks",
		"groq": "groq", "deepseek": "deepseek", "perplexity": "perplexity",
		"mistral": "mistral", "moonshot": "moonshot", "replicate": "replicate",
		"chutes": "chutes", "qwen": "qwen", "kimi": "kimi", "zhipu": "zhipu",
		"minimax": "minimax",
		// Z.AI / GLM
		"z.ai": "z.ai", "zai": "z.ai", "glm": "z.ai",
		// Together
		"together": "together", "together-ai": "together", "togetherai": "together",
		"together ai": "together",
		// XAI / Grok
		"xai": "xai", "grok": "xai", "x-ai": "xai", "supergrok": "xai",
		"xai-oauth": "xai", "grok-oauth": "xai", "x.ai": "xai", "xai-grok-oauth": "xai",
		// Meta / Local
		"meta": "meta", "llama": "meta", "local": "local", "ollama": "local",
		// Wafer
		"wafer": "wafer.ai", "wafer.ai": "wafer.ai",
		// Edges
		"": "", "MyFireworks": "myfireworks", "unknown-thing": "unknown-thing",
	}
	for in, want := range cases {
		if got := NormalizeProviderName(in); got != want {
			t.Errorf("NormalizeProviderName(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestBuiltinCompatibilityType_KnownInputs pins the exact wire-family mapping
// the legacy switch produced, including the deliberate self-family edges
// ("google" -> "google", "together-ai" -> "together-ai", "grok" -> "grok").
func TestBuiltinCompatibilityType_KnownInputs(t *testing.T) {
	openai := []string{
		"openai", "codex", "z.ai", "zai", "cerebras", "openrouter", "fireworks",
		"groq", "deepseek", "mistral", "together", "moonshot", "replicate",
		"chutes", "qwen", "kimi", "glm", "wafer", "wafer.ai",
	}
	for _, n := range openai {
		if got := builtinCompatibilityType(n); got != "openai" {
			t.Errorf("builtinCompatibilityType(%q) = %q, want openai", n, got)
		}
	}
	for _, n := range []string{"anthropic", "claudecode", "claude"} {
		if got := builtinCompatibilityType(n); got != "anthropic" {
			t.Errorf("builtinCompatibilityType(%q) = %q, want anthropic", n, got)
		}
	}
	for _, n := range []string{"gemini", "gemini-code-assist"} {
		if got := builtinCompatibilityType(n); got != "gemini" {
			t.Errorf("builtinCompatibilityType(%q) = %q, want gemini", n, got)
		}
	}
	// Self-family / unknown edges: returns the name unchanged (NOT normalized).
	selfFamily := map[string]string{
		"google": "google", "together-ai": "together-ai", "grok": "grok",
		"xai": "xai", "perplexity": "perplexity", "meta": "meta",
		"local": "local", "zhipu": "zhipu", "minimax": "minimax",
		"unknown-thing": "unknown-thing",
	}
	for in, want := range selfFamily {
		if got := builtinCompatibilityType(in); got != want {
			t.Errorf("builtinCompatibilityType(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestStandardAliasesConsistency asserts StandardAliases is consistent with
// NormalizeProviderName (it is generated from the same source) and contains the
// historically-required display aliases.
func TestStandardAliasesConsistency(t *testing.T) {
	for alias, canonical := range StandardAliases {
		if NormalizeProviderName(alias) != canonical {
			t.Errorf("StandardAliases[%q] = %q but NormalizeProviderName(%q) = %q",
				alias, canonical, alias, NormalizeProviderName(alias))
		}
		if alias == canonical {
			t.Errorf("StandardAliases contains identity entry %q (should only hold true aliases)", alias)
		}
	}
	// Every legacy display alias must still resolve correctly.
	mustResolve := map[string]string{
		"claudecode": "anthropic", "claude code": "anthropic", "codex": "openai",
		"google": "gemini", "together-ai": "together", "glm": "z.ai",
		"zai": "z.ai", "grok": "xai", "llama": "meta", "ollama": "local",
	}
	for alias, want := range mustResolve {
		if got, ok := StandardAliases[alias]; !ok || got != want {
			t.Errorf("StandardAliases[%q] = (%q, ok=%v), want %q", alias, got, ok, want)
		}
	}
}

// TestProvidersMatch_OrchestratorDecoration pins the sub-agent dispatch fix:
// a live fallback-stacked provider reports a decorated endpoint name
// ("provider/model[index]"), and that must still match the definition's bare
// provider name. Reproduces the live failure:
//
//	"definition requires provider 'claudecode' but got 'anthropic/claude-opus-4-8[0]'"
func TestProvidersMatch_OrchestratorDecoration(t *testing.T) {
	cases := []struct {
		expected string
		actual   string
		want     bool
	}{
		// The exact live-log failure: claudecode vs decorated anthropic endpoint.
		{"claudecode", "anthropic/claude-opus-4-8[0]", true},
		{"anthropic", "anthropic/claude-opus-4-8[0]", true},
		{"ClaudeCode", "anthropic/claude-sonnet-4-6[1]", true},
		// Provider-only decoration (no model segment).
		{"anthropic", "anthropic[0]", true},
		// OpenAI-family decoration.
		{"openai", "openai/gpt-4o[0]", true},
		{"z.ai", "z.ai/glm-4.6[2]", true},
		// Cross-family must STILL not match.
		{"anthropic", "openai/gpt-4o[0]", false},
		{"claudecode", "gemini/gemini-2.5[0]", false},
		// Bare names unaffected (idempotent).
		{"anthropic", "anthropic", true},
		{"claudecode", "anthropic", true},
	}
	for _, c := range cases {
		if got := ProvidersMatch(c.expected, c.actual); got != c.want {
			t.Errorf("ProvidersMatch(%q, %q) = %v, want %v", c.expected, c.actual, got, c.want)
		}
	}
}

// TestStripProviderDecoration pins the decoration stripper directly.
func TestStripProviderDecoration(t *testing.T) {
	cases := map[string]string{
		"anthropic/claude-opus-4-8[0]": "anthropic",
		"anthropic[0]":                 "anthropic",
		"openai/gpt-4o[12]":            "openai",
		"z.ai/glm-4.6[2]":              "z.ai",
		"anthropic":                    "anthropic",
		"  anthropic  ":                "anthropic",
		"":                             "",
	}
	for in, want := range cases {
		if got := stripProviderDecoration(in); got != want {
			t.Errorf("stripProviderDecoration(%q) = %q, want %q", in, got, want)
		}
	}
}

// name listed in providerAPIType must also be resolvable by providerCanonical
// (or be a big-three family token), so routing and normalization can't diverge.
func TestProviderAPITypeCanonicalConsistency(t *testing.T) {
	for name := range providerAPIType {
		if _, ok := providerCanonical[name]; !ok {
			t.Errorf("providerAPIType has %q but providerCanonical does not — maps diverged", name)
		}
	}
}
