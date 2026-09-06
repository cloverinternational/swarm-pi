// Package openai provides OpenAI-compatible provider factory.
package openai

import (
	"fmt"
	"strings"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/observability"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/provider/openai/profiles"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/provider/openai/quirks"
)

// Factory creates OpenAI-compatible providers from configuration.
type Factory struct {
	profileRegistry *profiles.Registry
	quirkRegistry   *quirks.Registry
}

// NewFactory creates a new provider factory.
func NewFactory() (*Factory, error) {
	profileReg, err := profiles.LoadBuiltinProfiles()
	if err != nil {
		return nil, fmt.Errorf("failed to load built-in profiles: %w", err)
	}

	quirkReg := quirks.NewRegistry()

	return &Factory{
		profileRegistry: profileReg,
		quirkRegistry:   quirkReg,
	}, nil
}

// FactoryConfig represents user configuration for creating a provider.
type FactoryConfig struct {
	// Profile name (e.g., "openai", "glm", "openrouter")
	Profile string

	// API key (required)
	APIKey string

	// Base URL (optional, overrides profile default)
	BaseURL string

	// Organization ID (OpenAI specific)
	OrganizationID string
	HTTPMaxRetries *int

	// Provider-specific extras
	AppName      string            // OpenRouter
	AppURL       string            // OpenRouter
	ResourceName string            // Azure
	APIVersion   string            // Azure
	Deployments  map[string]string // Azure

	// Observability (required)
	Logger observability.Logger
	Tracer observability.Tracer
}

// CreateFromProfile creates a provider using a built-in profile.
func (f *Factory) CreateFromProfile(config FactoryConfig) (*Provider, error) {
	if config.APIKey == "" {
		return nil, fmt.Errorf("API key is required")
	}

	if config.Logger == nil {
		return nil, fmt.Errorf("logger is required")
	}

	if config.Tracer == nil {
		return nil, fmt.Errorf("tracer is required")
	}

	// Load profile
	profile, ok := f.profileRegistry.Get(config.Profile)
	if !ok {
		return nil, fmt.Errorf("unknown profile: %s", config.Profile)
	}

	// Merge with user overrides
	userConfig := profiles.UserConfig{
		ProfileName:  config.Profile,
		APIKey:       config.APIKey,
		BaseURL:      config.BaseURL,
		AppName:      config.AppName,
		AppURL:       config.AppURL,
		ResourceName: config.ResourceName,
		APIVersion:   config.APIVersion,
		Deployments:  config.Deployments,
	}
	profile = profile.Merge(userConfig)

	// Get quirk adapter
	adapter := f.quirkRegistry.Lookup(profile.QuirkAdapter)

	// Create provider
	return f.createProvider(profile, config, adapter)
}

// CreateCustom creates a provider from custom configuration (no profile).
func (f *Factory) CreateCustom(config FactoryConfig, baseURL string) (*Provider, error) {
	if config.APIKey == "" {
		return nil, fmt.Errorf("API key is required")
	}

	if baseURL == "" {
		return nil, fmt.Errorf("base URL is required for custom provider")
	}

	// Create custom profile
	profile := profiles.FromUserConfig(profiles.UserConfig{
		ProfileName: "custom",
		APIKey:      config.APIKey,
		BaseURL:     baseURL,
	})

	// Use passthrough adapter
	adapter := f.quirkRegistry.Lookup("")

	return f.createProvider(profile, config, adapter)
}

// createProvider builds the actual provider instance.
func (f *Factory) createProvider(
	profile profiles.Profile,
	config FactoryConfig,
	adapter quirks.Adapter,
) (*Provider, error) {
	// Use profile base URL or user override
	baseURL := profile.BaseURL
	if config.BaseURL != "" {
		baseURL = config.BaseURL
	}

	// Replace template variables in base URL
	baseURL = f.replaceTemplateVars(baseURL, map[string]string{
		"resource_name": config.ResourceName,
	})

	// Create HTTP client
	httpClient, err := NewHTTPClient(HTTPClientConfig{
		BaseURL:        baseURL,
		Logger:         config.Logger,
		Tracer:         config.Tracer,
		HTTPMaxRetries: config.HTTPMaxRetries,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create HTTP client: %w", err)
	}

	// Set authentication headers
	authHeader := f.replaceTemplateVars(profile.Auth.HeaderFormat, map[string]string{
		"api_key": config.APIKey,
	})

	if profile.Auth.HeaderName != "" && authHeader != "" {
		httpClient.SetDefaultHeader(profile.Auth.HeaderName, authHeader)
	}

	// Set organization ID if provided (OpenAI-specific)
	if config.OrganizationID != "" {
		httpClient.SetDefaultHeader("OpenAI-Organization", config.OrganizationID)
	}

	// Add extra headers from profile
	for _, header := range profile.Auth.ExtraHeaders {
		// Skip headers with empty names
		if header.Name == "" {
			continue
		}

		value := f.replaceTemplateVars(header.Value, map[string]string{
			"app_name": config.AppName,
			"app_url":  config.AppURL,
		})
		if value != "" || !header.Required {
			httpClient.SetDefaultHeader(header.Name, value)
		}
	}

	// Create provider
	provider := &Provider{
		client:  httpClient,
		apiKey:  config.APIKey,
		baseURL: baseURL,
		orgID:   config.OrganizationID,
		logger:  config.Logger,
		tracer:  config.Tracer,
		profile: &profile,
		quirks:  adapter,
	}

	return provider, nil
}

// replaceTemplateVars replaces {var} placeholders in strings.
func (f *Factory) replaceTemplateVars(template string, vars map[string]string) string {
	result := template
	for key, value := range vars {
		placeholder := "{" + key + "}"
		result = strings.ReplaceAll(result, placeholder, value)
	}
	return result
}

// ListProfiles returns all available profile names.
func (f *Factory) ListProfiles() []string {
	return f.profileRegistry.List()
}

// GetProfile retrieves a profile by name.
func (f *Factory) Profile(name string) (profiles.Profile, bool) {
	return f.profileRegistry.Get(name)
}
