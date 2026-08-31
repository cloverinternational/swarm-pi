package provider

import "strings"

// Provider naming — single source of truth.
//
// There is exactly ONE fact that matters for routing and matching: a provider's
// API TYPE (the wire family). Is this provider Anthropic-wire, OpenAI-compatible,
// or Gemini-wire? That decides which translator/factory speaks to it, and two
// providers are interchangeable for fallback iff they share an API type.
//
// Everything else a provider needs (base URL, env var, OAuth vs key) is per-
// provider CONFIG, not a routing table, and lives in providers.json / the login
// catalog (catalog.go) — not here.
//
// This file replaces three previously hand-maintained, drift-prone tables:
//   - StandardAliases          (aliases.go)
//   - NormalizeProviderName     (was a big switch)
//   - builtinCompatibilityType  (was a second big switch)
//
// Two small name-keyed maps now carry all of it:
//   - providerAPIType : name → wire family   (the matcher + router; the point)
//   - providerCanonical : name → canonical name
//
// canonical exists only because the factory resolves a registered provider by
// its canonical name (agent/factory.go: NormalizeProviderName). It is deliberately
// boring; APIType is the spine.

// providerAPIType maps every known provider name/alias (lower-case) to its wire
// family. The family is one of "anthropic", "openai", "gemini", or — for
// providers that are their own family and not cross-compatible with the big
// three — the provider's own canonical name.
//
// This reproduces the legacy builtinCompatibilityType switch EXACTLY (the
// equivalence test pins every input). Note the deliberate edges: "google"
// normalizes to gemini for naming but was NOT in the gemini compat family, so it
// maps to its own name here; likewise "together-ai" vs "together".
var providerAPIType = map[string]string{
	// Anthropic wire
	"anthropic":  "anthropic",
	"claudecode": "anthropic",
	"claude":     "anthropic",

	// OpenAI-compatible wire
	"openai":     "openai",
	"codex":      "openai",
	"z.ai":       "openai",
	"zai":        "openai",
	"glm":        "openai",
	"cerebras":   "openai",
	"openrouter": "openai",
	"fireworks":  "openai",
	"groq":       "openai",
	"deepseek":   "openai",
	"mistral":    "openai",
	"together":   "openai",
	"moonshot":   "openai",
	"replicate":  "openai",
	"chutes":     "openai",
	"qwen":       "openai",
	"kimi":       "openai",
	"wafer":      "openai",
	"wafer.ai":   "openai",

	// Cursor (Anysphere) — own wire family (Connect-RPC, not OpenAI-compatible)
	"cursor": "cursor",

	// Gemini wire
	"gemini":             "gemini",
	"gemini-code-assist": "gemini",
}

// providerCanonical maps every known provider name/alias (lower-case) to its
// canonical provider name. Used only by NormalizeProviderName (which the factory
// needs to find a registered provider). Names absent here normalize to their own
// lower-cased form.
//
// Reproduces the legacy NormalizeProviderName switch EXACTLY.
var providerCanonical = map[string]string{
	// Anthropic
	"claudecode":  "anthropic",
	"anthropic":   "anthropic",
	"claude":      "anthropic",
	"claude code": "anthropic",

	// OpenAI
	"openai": "openai",
	"codex":  "openai",

	// Gemini / Google
	"gemini":             "gemini",
	"google":             "gemini",
	"gemini-code-assist": "gemini",

	// Single-name providers (alias → same canonical)
	"cerebras":   "cerebras",
	"openrouter": "openrouter",
	"fireworks":  "fireworks",
	"groq":       "groq",
	"deepseek":   "deepseek",
	"perplexity": "perplexity",
	"mistral":    "mistral",
	"moonshot":   "moonshot",
	"replicate":  "replicate",
	"chutes":     "chutes",
	"qwen":       "qwen",
	"kimi":       "kimi",
	"zhipu":      "zhipu",
	"minimax":    "minimax",

	// Z.AI / GLM
	"z.ai": "z.ai",
	"zai":  "z.ai",
	"glm":  "z.ai",

	// Together AI
	"together":    "together",
	"together-ai": "together",
	"togetherai":  "together",
	"together ai": "together",

	// XAI / Grok
	"xai":            "xai",
	"grok":           "xai",
	"x-ai":           "xai",
	"supergrok":      "xai",
	"xai-oauth":      "xai",
	"grok-oauth":     "xai",
	"x.ai":           "xai",
	"xai-grok-oauth": "xai",

	// Meta / Llama
	"meta":  "meta",
	"llama": "meta",

	// Cursor (Anysphere)
	"cursor": "cursor",

	// Local / Ollama
	"local":  "local",
	"ollama": "local",

	// Wafer
	"wafer":    "wafer.ai",
	"wafer.ai": "wafer.ai",
}

// stripProviderDecoration removes orchestrator/fallback-chain endpoint
// decoration from a provider identity, leaving the bare provider name.
//
// The reliability/fallback orchestrator names its entries "provider/model[index]"
// (e.g. "anthropic/claude-opus-4-8[0]") for telemetry, and Orchestrator.Name()
// returns that decorated label. When such a live provider is matched against a
// definition's bare provider ("claudecode"), the decoration must be stripped or
// the wire-family comparison wrongly fails. A real provider name never contains
// '/' or '['; keep only the segment before the first of either. Idempotent for
// already-bare names.
func stripProviderDecoration(name string) string {
	n := strings.TrimSpace(name)
	if i := strings.IndexAny(n, "/["); i >= 0 {
		n = n[:i]
	}
	return strings.TrimSpace(n)
}

// apiTypeOf returns the wire family for a provider name. For the big-three
// families it returns "anthropic"/"openai"/"gemini"; for every other name it
// returns the name unchanged (its own family). This mirrors the legacy
// builtinCompatibilityType switch EXACTLY, including the deliberate fact that
// own-family aliases (e.g. "grok" vs "xai") do NOT match via the built-in table
// — they only match when a registry instance has RegisterCompatible'd them.
// It is the single primitive behind builtinCompatibilityType / ProvidersMatch.
//
// Orchestrator endpoint decoration ("provider/model[index]") is stripped first
// so a live fallback-stacked provider still matches its definition's bare name.
func apiTypeOf(name string) string {
	n := strings.ToLower(stripProviderDecoration(name))
	if t, ok := providerAPIType[n]; ok {
		return t
	}
	return n
}
