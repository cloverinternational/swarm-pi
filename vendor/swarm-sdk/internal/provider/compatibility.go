// Package provider defines the interface for LLM providers.
package provider

import (
	"strings"
)

// RegisterOpenAICompatible marks a provider as OpenAI-compatible on this registry.
// This should be called when creating custom providers that use the OpenAI API format.
// For example: reg.RegisterOpenAICompatible("local") for a local llama.cpp server.
func (r *SimpleRegistry) RegisterOpenAICompatible(providerName string) {
	r.RegisterCompatible(providerName, "openai")
}

// RegisterAnthropicCompatible marks a provider as Anthropic-compatible on this registry.
func (r *SimpleRegistry) RegisterAnthropicCompatible(providerName string) {
	r.RegisterCompatible(providerName, "anthropic")
}

// RegisterGeminiCompatible marks a provider as Gemini-compatible on this registry.
func (r *SimpleRegistry) RegisterGeminiCompatible(providerName string) {
	r.RegisterCompatible(providerName, "gemini")
}

// RegisterCompatible marks a provider as compatible with a base provider type
// on this registry instance. The baseType should be one of: "openai",
// "anthropic", "gemini". The mapping is scoped to this registry only — separate
// registries never share compatibility state.
func (r *SimpleRegistry) RegisterCompatible(providerName, baseType string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.compatibleWith == nil {
		r.compatibleWith = make(map[string]string)
	}
	r.compatibleWith[strings.ToLower(providerName)] = strings.ToLower(baseType)
}

// builtinCompatibilityType returns the built-in (static) base type a provider is
// compatible with, or the provider name itself if there is no built-in mapping.
// This carries no per-instance state and is the fallback used by both the
// instance method and the package-level ProvidersMatch helper.
//
// It is a thin wrapper over apiTypeOf (identity.go), the single source of truth
// for provider→wire-family mappings.
func builtinCompatibilityType(name string) string {
	return apiTypeOf(name)
}

// getCompatibilityType returns the base type a provider is compatible with,
// consulting this registry's dynamic mappings first and the built-in mappings
// as a fallback. Returns the provider name itself if not registered anywhere.
func (r *SimpleRegistry) getCompatibilityType(providerName string) string {
	// Strip orchestrator endpoint decoration ("provider/model[index]") so a
	// decorated custom provider still matches its RegisterCompatible entry.
	name := strings.ToLower(stripProviderDecoration(providerName))

	// Check this instance's dynamic registry first.
	r.mu.RLock()
	if baseType, ok := r.compatibleWith[name]; ok {
		r.mu.RUnlock()
		return baseType
	}
	r.mu.RUnlock()

	// Fall back to built-in compatibility mappings.
	return builtinCompatibilityType(name)
}

// NormalizeProviderName converts provider aliases to their canonical names.
// This ensures consistent provider naming throughout the SDK.
//
// It merges the canonical mappings from swarm-tui/internal/chat/credentials.go
// so that downstream consumers (CLI, headless, custom agents) resolve the same
// names without copying TUI-specific logic.
//
// Examples:
//
//	"ClaudeCode"  → "anthropic"
//	"Google"      → "gemini"
//	"Together AI" → "together"
//	"MyFireworks" → "myfireworks"  (unknown → lowercased)
//
// This is a thin wrapper over the providerCanonical map (identity.go), the
// single source of truth for provider→canonical-name mappings. Unknown names
// fall through to their lower-cased form (and "" stays "").
func NormalizeProviderName(providerName string) string {
	p := strings.ToLower(strings.TrimSpace(providerName))
	if canonical, ok := providerCanonical[p]; ok {
		return canonical
	}
	return p
}

// ProvidersMatch returns true when two provider names can be treated as
// equivalent according to this registry. Two providers match if:
// 1. They have the same name (case-insensitive), or
// 2. They are both compatible with the same base provider type
//
// This allows custom providers (like "local") to work with agents that expect
// standard providers (like "openai") as long as the custom provider has been
// registered as compatible via reg.RegisterOpenAICompatible() on this registry.
func (r *SimpleRegistry) ProvidersMatch(expected string, actual string) bool {
	expectedLower := strings.ToLower(strings.TrimSpace(expected))
	actualLower := strings.ToLower(strings.TrimSpace(actual))

	// Direct match
	if expectedLower == actualLower {
		return true
	}

	// Check if both providers are compatible with the same base type,
	// consulting this instance's dynamic mappings plus the built-in fallback.
	return r.getCompatibilityType(expectedLower) == r.getCompatibilityType(actualLower)
}

// ProvidersMatch returns true when two provider names can be treated as
// equivalent according to the built-in (static) compatibility mappings only.
//
// It does NOT consult any dynamically registered compatibility (custom
// providers registered at runtime via SimpleRegistry.RegisterCompatible). For
// runtime-registered compatibility, use the instance method
// (*SimpleRegistry).ProvidersMatch. This package-level helper exists for
// callers that have no registry in scope and only need the built-in families.
func ProvidersMatch(expected string, actual string) bool {
	expectedLower := strings.ToLower(strings.TrimSpace(expected))
	actualLower := strings.ToLower(strings.TrimSpace(actual))

	// Direct match
	if expectedLower == actualLower {
		return true
	}

	// Built-in compatibility families only.
	return builtinCompatibilityType(expectedLower) == builtinCompatibilityType(actualLower)
}
