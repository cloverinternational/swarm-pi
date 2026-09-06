package gemini

import (
	"os"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/provider"
)

// Register registers the Gemini provider with the given registry.
func Register(registry provider.Registry) error {
	// Create default config
	config := Config{
		AuthMode: AuthModeOAuth,
		Model:    "gemini-2.5-pro",
	}

	// Override with environment variables if set
	if apiKey := os.Getenv("GEMINI_API_KEY"); apiKey != "" {
		config.AuthMode = AuthModeAPIKey
		config.APIKey = apiKey
	}

	if projectID := os.Getenv("GOOGLE_CLOUD_PROJECT"); projectID != "" {
		config.ProjectID = projectID
	} else if projectID := os.Getenv("GOOGLE_CLOUD_PROJECT_ID"); projectID != "" {
		config.ProjectID = projectID
	}

	if noBrowser := os.Getenv("NO_BROWSER"); noBrowser == "true" || noBrowser == "1" {
		config.NoBrowser = true
	}

	// Create provider factory
	factory := func(cfg provider.Config) (provider.Provider, error) {
		geminiConfig := Config{
			AuthMode:  config.AuthMode,
			APIKey:    cfg.APIKey,
			ProjectID: config.ProjectID,
			Model:     cfg.Model,
			Timeout:   cfg.Timeout,
			BaseURL:   cfg.BaseURL,
			NoBrowser: config.NoBrowser,
		}
		if geminiConfig.APIKey == "" {
			geminiConfig.APIKey = config.APIKey
		}
		if geminiConfig.Model == "" {
			geminiConfig.Model = config.Model
		}
		return New(geminiConfig)
	}

	// Register with registry
	return registry.Register("gemini", factory)
}

// RegisterWithConfig registers the Gemini provider with custom configuration.
func RegisterWithConfig(registry provider.Registry, config Config) error {
	factory := func(cfg provider.Config) (provider.Provider, error) {
		// Merge configs
		if cfg.APIKey != "" {
			config.APIKey = cfg.APIKey
		}
		if cfg.Model != "" {
			config.Model = cfg.Model
		}
		if cfg.BaseURL != "" {
			config.BaseURL = cfg.BaseURL
		}
		if cfg.Timeout != 0 {
			config.Timeout = cfg.Timeout
		}
		return New(config)
	}
	return registry.Register("gemini", factory)
}

// MustRegister registers the Gemini provider or panics on error.
func MustRegister(registry provider.Registry) {
	if err := Register(registry); err != nil {
		panic("failed to register Gemini provider: " + err.Error())
	}
}

// NewDefault creates a Gemini provider with default configuration.
// Uses OAuth by default, or API key if GEMINI_API_KEY is set.
func NewDefault() (*Provider, error) {
	config := Config{
		AuthMode: AuthModeOAuth,
		Model:    "gemini-2.5-pro",
	}

	// Override with environment variables
	if apiKey := os.Getenv("GEMINI_API_KEY"); apiKey != "" {
		config.AuthMode = AuthModeAPIKey
		config.APIKey = apiKey
	}

	if projectID := os.Getenv("GOOGLE_CLOUD_PROJECT"); projectID != "" {
		config.ProjectID = projectID
	} else if projectID := os.Getenv("GOOGLE_CLOUD_PROJECT_ID"); projectID != "" {
		config.ProjectID = projectID
	}

	if noBrowser := os.Getenv("NO_BROWSER"); noBrowser == "true" || noBrowser == "1" {
		config.NoBrowser = true
	}

	return New(config)
}

// NewWithOAuth creates a Gemini provider using OAuth authentication.
func NewWithOAuth(options ...func(*Config)) (*Provider, error) {
	config := Config{
		AuthMode: AuthModeOAuth,
		Model:    "gemini-2.5-pro",
	}

	for _, opt := range options {
		opt(&config)
	}

	return New(config)
}

// NewWithAPIKey creates a Gemini provider using API key authentication.
func NewWithAPIKey(apiKey string, options ...func(*Config)) (*Provider, error) {
	config := Config{
		AuthMode: AuthModeAPIKey,
		APIKey:   apiKey,
		Model:    "gemini-2.5-pro",
	}

	for _, opt := range options {
		opt(&config)
	}

	return New(config)
}

// Configuration options

// WithModel sets the default model.
func WithModel(model string) func(*Config) {
	return func(c *Config) {
		c.Model = model
	}
}

// WithProjectID sets the GCP project ID.
func WithProjectID(projectID string) func(*Config) {
	return func(c *Config) {
		c.ProjectID = projectID
	}
}

// WithNoBrowser disables browser-based OAuth.
func WithNoBrowser() func(*Config) {
	return func(c *Config) {
		c.NoBrowser = true
	}
}

// WithTimeout sets the request timeout in seconds.
func WithTimeout(seconds int) func(*Config) {
	return func(c *Config) {
		c.Timeout = seconds
	}
}

// WithTokenPath sets the OAuth token storage path.
func WithTokenPath(path string) func(*Config) {
	return func(c *Config) {
		c.TokenPath = path
	}
}

// WithBaseURL sets a custom API base URL.
func WithBaseURL(url string) func(*Config) {
	return func(c *Config) {
		c.BaseURL = url
	}
}
