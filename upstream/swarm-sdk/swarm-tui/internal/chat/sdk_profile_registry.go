package chat

import (
	"strings"
	"sync"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/provider/openai/profiles"
)

// sdkProfileRegistry caches the SDK's builtin provider profiles.
// The SDK's profiles.toml is the single source of truth for provider routing.
var sdkProfileRegistry struct {
	once     sync.Once
	registry *profiles.Registry
	err      error
}

// getSDKProfileRegistry returns the cached SDK profile registry.
// Returns nil if the registry failed to load.
func getSDKProfileRegistry() *profiles.Registry {
	sdkProfileRegistry.once.Do(func() {
		sdkProfileRegistry.registry, sdkProfileRegistry.err = profiles.LoadBuiltinProfiles()
		if sdkProfileRegistry.err != nil {
			logDebug("[ProfileRegistry] Failed to load SDK builtin profiles: %v", sdkProfileRegistry.err)
		} else {
			logDebug("[ProfileRegistry] Loaded %d builtin profiles from SDK", len(sdkProfileRegistry.registry.List()))
		}
	})
	return sdkProfileRegistry.registry
}

// profileRegistryLookupResult contains the resolved provider info from the SDK registry.
type profileRegistryLookupResult struct {
	Found       bool
	BaseURL     string
	APIType     string // "openai", "openai-compatible", "gemini", "anthropic"
	AuthType    string
	DisplayName string
}

// lookupProviderInSDKRegistry looks up a provider in the SDK's builtin profile registry.
// This is the authoritative source for provider routing configuration.
// Returns (result, found) where found indicates if the provider was in the registry.
func lookupProviderInSDKRegistry(providerName string) profileRegistryLookupResult {
	registry := getSDKProfileRegistry()
	if registry == nil {
		return profileRegistryLookupResult{}
	}

	// Normalize provider name for lookup (handle aliases)
	normalized := strings.ToLower(strings.TrimSpace(providerName))

	// Try common aliases
	aliases := []string{normalized}
	switch normalized {
	case "wafer.ai", "firepass":
		aliases = append(aliases, "wafer")
	case "z.ai", "zai":
		aliases = append(aliases, "glm")
	case "togetherai", "together-ai":
		aliases = append(aliases, "together")
	}

	for _, alias := range aliases {
		if p, ok := registry.Get(alias); ok {
			// Determine API type from CompatibleWith
			apiType := apiTypeFromCompatibleWith(p.CompatibleWith, providerName)

			return profileRegistryLookupResult{
				Found:       true,
				BaseURL:     p.BaseURL,
				APIType:     apiType,
				AuthType:    p.Auth.Type,
				DisplayName: p.DisplayName,
			}
		}
	}

	return profileRegistryLookupResult{}
}

// apiTypeFromCompatibleWith maps the SDK's CompatibleWith field to TUI's apiType.
// The SDK uses "openai" for all OpenAI-compatible providers, but TUI distinguishes
// between "openai" (official OpenAI) and "openai-compatible" (third-party providers).
func apiTypeFromCompatibleWith(compatibleWith, providerName string) string {
	normalized := strings.ToLower(strings.TrimSpace(providerName))

	switch compatibleWith {
	case "openai":
		// Distinguish between official OpenAI and third-party OpenAI-compatible
		if normalized == "openai" || normalized == "codex" {
			return "openai"
		}
		return "openai-compatible"
	case "gemini":
		return "gemini"
	case "anthropic":
		return "anthropic"
	default:
		// Default to openai-compatible for unknown compatible_with values
		if compatibleWith != "" {
			return "openai-compatible"
		}
		return ""
	}
}

// backfillProviderConfigFromRegistry updates a legacy ProviderConfig with information
// from the SDK registry. This enables migration of old configs that lack api_type.
//
// Returns true if the config was updated, false otherwise.
func backfillProviderConfigFromRegistry(cfg *ProviderConfig, providerName string) bool {
	if cfg == nil {
		return false
	}

	// If api_type is already set and valid, no backfill needed
	if cfg.APIType != "" && (cfg.APIType == "openai" || cfg.APIType == "openai-compatible" ||
		cfg.APIType == "anthropic" || cfg.APIType == "gemini") {
		return false
	}

	// Look up in SDK registry
	result := lookupProviderInSDKRegistry(providerName)
	if !result.Found {
		return false
	}

	// Backfill missing fields
	updated := false

	if cfg.APIType == "" && result.APIType != "" {
		cfg.APIType = result.APIType
		logDebug("[Backfill] Set api_type=%s for provider %s from SDK registry", cfg.APIType, providerName)
		updated = true
	}

	if cfg.BaseURL == "" && result.BaseURL != "" {
		cfg.BaseURL = result.BaseURL
		logDebug("[Backfill] Set base_url=%s for provider %s from SDK registry", cfg.BaseURL, providerName)
		updated = true
	}

	if cfg.Type == "" && result.AuthType != "" {
		cfg.Type = result.AuthType
		logDebug("[Backfill] Set auth type=%s for provider %s from SDK registry", cfg.Type, providerName)
		updated = true
	}

	if cfg.Name == "" {
		cfg.Name = providerName
		updated = true
	}

	return updated
}

// BackfillProvidersConfig upgrades a legacy providers.json by adding api_type
// from the SDK registry for any providers that are missing it.
// This is called automatically when loading providers.json.
//
// Returns true if any providers were updated and the config should be saved.
func BackfillProvidersConfig(providers []ProviderConfig) (bool, error) {
	if len(providers) == 0 {
		return false, nil
	}

	anyUpdated := false
	for i := range providers {
		cfg := &providers[i]
		if cfg.Name == "" {
			continue
		}

		// Check if this provider needs backfill
		if backfillProviderConfigFromRegistry(cfg, cfg.Name) {
			anyUpdated = true
		}
	}

	return anyUpdated, nil
}

// BackfillProvidersConfigMap upgrades a legacy providers.json (map format) by adding
// api_type from the SDK registry for any providers that are missing it.
//
// Returns true if any providers were updated and the config should be saved.
func BackfillProvidersConfigMap(providers map[string]ProviderConfig) (bool, error) {
	if len(providers) == 0 {
		return false, nil
	}

	anyUpdated := false
	for name, cfg := range providers {
		if name == "" {
			continue
		}

		// Backfill into a copy
		cfgCopy := cfg
		if backfillProviderConfigFromRegistry(&cfgCopy, name) {
			providers[name] = cfgCopy
			anyUpdated = true
		}
	}

	return anyUpdated, nil
}
