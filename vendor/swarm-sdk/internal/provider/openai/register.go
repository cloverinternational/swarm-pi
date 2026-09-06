package openai

import (
	"fmt"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/observability/noop"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/provider"
)

// profileAlias maps provider names to their OpenAI profile names.
// This enables a single registered "openai" factory to power all
// OpenAI-compatible providers (groq, cerebras, openrouter, etc.) by
// selecting the appropriate profile from the embedded profiles.yaml.
var profileAlias = map[string]string{
	"openai":     "openai",
	"z.ai":       "glm",
	"zai":        "glm",
	"glm":        "glm",
	"openrouter": "openrouter",
	"cerebras":   "cerebras",
	"groq":       "groq",
	"codex":      "openai",
	"wafer.ai":   "wafer",
	"wafer":      "wafer",
}

// ProviderFactory creates an OpenAI (or OpenAI-compatible) provider from
// configuration.  It uses the embedded profile system when a matching profile
// exists and falls back to the simple constructor otherwise.
func ProviderFactory(config provider.Config) (provider.Provider, error) {
	// Resolve observability — typed fields first, noop fallback.
	logger := config.Logger
	if logger == nil {
		logger = noop.NewLogger()
	}
	tracer := config.Tracer
	if tracer == nil {
		tracer = noop.NewTracer()
	}

	// Determine the profile name for this provider.
	profileName := "openai" // safe default
	if alias, ok := profileAlias[config.Name]; ok {
		profileName = alias
	}

	// Try profile-based construction first; it carries per-provider base-URL
	// defaults and other tunables from profiles.yaml.
	factory, err := NewFactory()
	if err == nil {
		if _, ok := factory.Profile(profileName); ok {
			factoryConfig := FactoryConfig{
				Profile:        profileName,
				APIKey:         config.APIKey,
				BaseURL:        config.BaseURL,
				OrganizationID: extractString(config.Custom, "organization_id"),
				Logger:         logger,
				Tracer:         tracer,
				HTTPMaxRetries: config.HTTPMaxRetries,
			}
			if p, err := factory.CreateFromProfile(factoryConfig); err == nil {
				return p, nil
			}
		}
	}

	// Fallback: create directly without profile.
	openaiConfig := Config{
		APIKey:         config.APIKey,
		BaseURL:        config.BaseURL,
		OrganizationID: extractString(config.Custom, "organization_id"),
		Logger:         logger,
		Tracer:         tracer,
		HTTPMaxRetries: config.HTTPMaxRetries,
	}
	p, err := New(openaiConfig)
	if err != nil {
		return nil, fmt.Errorf("openai: create provider: %w", err)
	}
	return p, nil
}

// extractString safely extracts a string from the custom config map.
func extractString(custom map[string]any, key string) string {
	if custom == nil {
		return ""
	}
	if val, ok := custom[key]; ok {
		if str, ok := val.(string); ok {
			return str
		}
	}
	return ""
}

// Register registers the OpenAI provider factory with the given registry.
// This allows applications to control when and where registration happens.
func Register(registry *provider.SimpleRegistry) error {
	return registry.Register("openai", ProviderFactory)
}
