// Package client provides an ergonomic top-level interface to the Swarm SDK.
//
// The Provider type enforces compile-time type safety for provider selection.
// Use the predefined constants instead of raw strings:
//
//	c, err := client.New(
//	    client.WithProvider(client.ProviderAnthropic, "claude-sonnet-4-5"),
//	)
//
// For dynamic provider selection, use ParseProvider:
//
//	p, err := client.ParseProvider(userInput)
//	if err != nil {
//	    // Handle invalid provider name
//	}
package client

import (
	"fmt"
	"strings"
)

// Provider is a type-safe identifier for an LLM provider.
// Use the predefined constants (ProviderAnthropic, ProviderOpenAI, ProviderGemini)
// instead of raw strings to get compile-time type safety.
//
// Example:
//
//	// Type-safe: compiler catches typos
//	c, err := client.New(client.WithProvider(client.ProviderAnthropic, "claude-sonnet-4-5"))
type Provider string

// Predefined provider constants provide compile-time type safety.
// Using these constants instead of raw strings prevents typos and
// enables IDE autocomplete.
const (
	// ProviderAnthropic represents the Anthropic Claude API provider.
	// Models: claude-sonnet-4-5, claude-opus-4-7, claude-haiku-4-5, etc.
	ProviderAnthropic Provider = "anthropic"

	// ProviderOpenAI represents the OpenAI GPT API provider.
	// Models: gpt-4o, gpt-4o-mini, gpt-5.2-codex, etc.
	ProviderOpenAI Provider = "openai"

	// ProviderGemini represents the Google Gemini API provider.
	// Models: gemini-2.0-flash, gemini-2.5-pro, etc.
	ProviderGemini Provider = "gemini"

	// ProviderXAI represents the xAI Grok API provider.
	// Supports both API key (XAI_API_KEY) and SuperGrok OAuth.
	// Models: grok-4.3, grok-4.3-mini, grok-4-1, etc.
	// Base URL: https://api.x.ai/v1 (OpenAI-compatible).
	ProviderXAI Provider = "xai"
)

// validProviders maps known provider names to their canonical form.
// This includes all providers defined in the SDK's builtin profile registry
// (provider/openai/profiles/profiles.toml) plus first-class providers.
// When adding a new provider to profiles.toml, add its aliases here too.
var validProviders = map[string]Provider{
	// First-class providers with dedicated implementations.
	"anthropic": ProviderAnthropic,
	"openai":    ProviderOpenAI,
	"gemini":    ProviderGemini,
	"xai":       ProviderXAI,

	// Common aliases (normalized to canonical form).
	"google":             ProviderGemini,
	"claudecode":         ProviderAnthropic,
	"codex":              ProviderOpenAI,
	"gemini-code-assist": ProviderGemini,

	// xAI / Grok aliases — all resolve to ProviderXAI.
	// Supports both XAI_API_KEY and SuperGrok OAuth credential paths.
	"grok":           ProviderXAI,
	"x-ai":           ProviderXAI,
	"x.ai":           ProviderXAI,
	"supergrok":      ProviderXAI,
	"xai-oauth":      ProviderXAI,
	"grok-oauth":     ProviderXAI,
	"xai-grok-oauth": ProviderXAI,

	// OpenAI-compatible providers (from profiles.toml).
	// These are accepted by ParseProvider and routed to the
	// openai-compatible provider at construction time.
	"wafer":        ProviderOpenAI,
	"wafer.ai":     ProviderOpenAI,
	"firepass":     ProviderOpenAI,
	"cerebras":     ProviderOpenAI,
	"fireworks":    ProviderOpenAI,
	"groq":         ProviderOpenAI,
	"openrouter":   ProviderOpenAI,
	"together":     ProviderOpenAI,
	"together-ai":  ProviderOpenAI,
	"togetherai":   ProviderOpenAI,
	"deepseek":     ProviderOpenAI,
	"perplexity":   ProviderOpenAI,
	"glm":          ProviderOpenAI,
	"glm-vision":   ProviderOpenAI,
	"z.ai":         ProviderOpenAI,
	"zai":          ProviderOpenAI,
	"azure":        ProviderOpenAI,
	"azure-openai": ProviderOpenAI,
	"mistral":      ProviderOpenAI,
	"moonshot":     ProviderOpenAI,
	"replicate":    ProviderOpenAI,
	"chutes":       ProviderOpenAI,
	"qwen":         ProviderOpenAI,
	"kimi":         ProviderOpenAI,
	"meta":         ProviderOpenAI,
	"zhipu":        ProviderOpenAI,
	"local":        ProviderOpenAI,
	"minimax":      ProviderAnthropic,
}

// String returns the canonical provider name.
func (p Provider) String() string {
	return string(p)
}

// IsValid returns true if the provider is a known, valid provider.
//
// Recognises:
//   - canonical first-class names (anthropic, openai, gemini)
//   - curated aliases (fireworks, groq, openrouter, …)
//   - user-defined custom names registered in ~/.swarm/config/providers.json (or
//     the SDK's ~/.swarm/config/providers.json) with a valid api_type.
//     The api_type tells us which underlying factory to route to; the
//     custom name is preserved for credential lookup.
//
// Matching is case-insensitive, matching ParseProvider. Any name that
// ParseProvider accepts must also pass IsValid — callers (notably the
// client.New validation gate at the bottom of New) rely on that contract
// when round-tripping a parsed name back through validation. Otherwise a
// canonically-cased name like "ClaudeCode", happily accepted by
// ParseProvider, would still be rejected at construction time.
func (p Provider) IsValid() bool {
	name := strings.ToLower(strings.TrimSpace(string(p)))
	if _, ok := validProviders[name]; ok {
		return true
	}
	_, ok := lookupCustomProvider(name)
	return ok
}

// ParseProvider converts a string to a Provider type.
// Returns a ContractViolation error if the provider name is unknown.
//
// This function is useful for dynamic provider selection from configuration
// files or user input. For static provider selection, prefer using the
// predefined constants directly:
//
//	// Static (preferred): use constants
//	client.WithProvider(client.ProviderAnthropic, "claude-sonnet-4-5")
//
//	// Dynamic: parse from string
//	p, err := client.ParseProvider(config.Provider)
//	if err != nil {
//	    return err
//	}
//	client.WithProvider(p, config.Model)
func ParseProvider(name string) (Provider, error) {
	name = strings.ToLower(strings.TrimSpace(name))
	if name == "" {
		return "", &ContractViolation{
			Violation: "Provider name is empty",
			Required:  "Provider must be a known provider name (anthropic, openai, gemini, xai, or an OpenAI-compatible provider)",
			Hint:      "Use client.ProviderAnthropic, client.ProviderOpenAI, client.ProviderGemini, client.ProviderXAI, or client.ParseProvider for compatible providers",
		}
	}

	if p, ok := validProviders[name]; ok {
		return p, nil
	}

	// User-defined custom provider name (e.g. "fire" → fireworks via
	// api_type=openai-compatible) registered in ~/.swarm/config/providers.json.
	// Return the ORIGINAL custom name (not the canonical underlying type)
	// so downstream credential lookup pulls the per-name api_key/base_url
	// stored under the custom alias. client.New will recognise the custom
	// name via lookupCustomProvider and route it to the right factory.
	if _, ok := lookupCustomProvider(name); ok {
		return Provider(name), nil
	}

	return "", &ContractViolation{
		Violation: fmt.Sprintf("Unknown provider: %q", name),
		Required:  "Provider must be a known provider name (anthropic, openai, gemini, xai, or an OpenAI-compatible provider)",
		Hint:      "Use client.ProviderAnthropic, client.ProviderOpenAI, client.ProviderGemini, client.ProviderXAI, or client.ParseProvider for compatible providers",
	}
}

// MustParseProvider is like ParseProvider but panics on error.
// Use this only in initialization code where errors are fatal.
func MustParseProvider(name string) Provider {
	p, err := ParseProvider(name)
	if err != nil {
		panic(err)
	}
	return p
}

// WithProvider sets the provider and model for the client.
// The provider parameter must be a valid Provider constant.
//
// Example:
//
//	// Type-safe: compiler catches typos
//	c, err := client.New(
//	    client.WithProvider(client.ProviderAnthropic, "claude-sonnet-4-5"),
//	)
//
//	// Alternative: dynamic provider selection
//	p, err := client.ParseProvider(userInput)
//	if err != nil {
//	    return err
//	}
//	c, err := client.New(client.WithProvider(p, "model-name"))
//
// CONTRACT: The provider must be one of the predefined constants.
// Unknown providers will cause a ContractViolation error at client
// construction time.
//
// When using ParseProvider to resolve a dynamic name (e.g., "wafer", "groq"),
// the original name is lost because ParseProvider collapses it to a canonical
// Provider type (e.g., ProviderOpenAI). To preserve the original name for
// credential lookup, use WithOriginalProviderName after WithProvider, or
// use the convenience function ProviderOptions which handles both:
//
//	opts, _ := client.ProviderOptions("wafer", "GLM-5.1")
//	c, _ := client.New(opts...)
func WithProvider(p Provider, model string) Option {
	return func(o *options) {
		o.providerName = string(p)
		// Set originalProviderName only if not already set. This is correct
		// for direct constant usage (WithProvider(ProviderAnthropic, ...))
		// where the canonical type IS the original name. For dynamic names
		// resolved via ParseProvider (e.g., "wafer" → ProviderOpenAI), the
		// caller should use WithOriginalProviderName or ProviderOptions to
		// preserve the original name for credential lookup.
		if o.originalProviderName == "" {
			o.originalProviderName = string(p)
		}
		o.model = model
	}
}

// WithOriginalProviderName preserves the caller's original provider name for
// credential lookup. This is essential when the provider name differs from
// the canonical Provider type — which happens for all OpenAI-compatible
// providers resolved via ParseProvider.
//
// ParseProvider collapses names like "wafer", "groq", or "cerebras" into
// ProviderOpenAI, but each has its own credentials stored under the original
// name in providers.json or environment variables. Without this option, the
// SDK would look up credentials for "openai" instead of the actual provider.
//
// Example:
//
//	p, _ := client.ParseProvider("wafer")
//	c, _ := client.New(
//	    client.WithProvider(p, "GLM-5.1"),
//	    client.WithOriginalProviderName("wafer"),
//	)
//
// For convenience, use ProviderOptions which combines both calls.
func WithOriginalProviderName(name string) Option {
	return func(o *options) {
		o.originalProviderName = name
	}
}

// ProviderOptions returns the standard option pair for a provider: the
// canonical Provider type for factory routing and the original name for
// credential lookup. Use this when dynamically selecting a provider from
// configuration or user input — it correctly handles OpenAI-compatible
// providers whose names differ from the canonical ProviderOpenAI type.
//
// Example:
//
//	opts, _ := client.ProviderOptions("wafer", "GLM-5.1")
//	c, _ := client.New(opts...)
//
// CONTRACT: The provider name must be recognized by ParseProvider.
func ProviderOptions(name, model string) ([]Option, error) {
	p, err := ParseProvider(name)
	if err != nil {
		return nil, err
	}
	return []Option{WithProvider(p, model), WithOriginalProviderName(name)}, nil
}

// WithProviderString is a legacy compatibility function.
// It correctly preserves the original provider name for credential lookup,
// making it suitable for dynamic provider selection from config or user input.
// For new code, prefer ProviderOptions or the WithProvider + WithOriginalProviderName
// pair.
//
// Migration:
//
//	// Option A: Use ProviderOptions (recommended for dynamic names)
//	opts, _ := client.ProviderOptions("wafer", "GLM-5.1")
//	c, _ := client.New(opts...)
//
//	// Option B: Use WithProvider + WithOriginalProviderName
//	p, _ := client.ParseProvider("wafer")
//	c, _ := client.New(
//	    client.WithProvider(p, "GLM-5.1"),
//	    client.WithOriginalProviderName("wafer"),
//	)
//
//	// Option C: Use WithProviderString (still works, not deprecated)
//	c, _ := client.New(client.WithProviderString("wafer", "GLM-5.1"))
func WithProviderString(name, model string) Option {
	// Note: We intentionally do NOT validate here for backward compatibility.
	// Validation happens at client construction time.
	return func(o *options) {
		o.providerName = name
		o.originalProviderName = name
		o.model = model
	}
}
