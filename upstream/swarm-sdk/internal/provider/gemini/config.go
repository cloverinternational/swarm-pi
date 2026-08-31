// Package gemini provides a Gemini API provider with OAuth support.
// This implementation is based on the official Gemini CLI source code.
package gemini

import (
	"errors"
	"io"
	"os"
	"path/filepath"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/observability"
)

// Default OAuth client credentials from Gemini CLI
// These are safe to embed as they are "installed application" credentials
// See: https://developers.google.com/identity/protocols/oauth2#installed
const (
	DefaultClientID     = "681255809395-oo8ft2oprdrnp9e3aqf6av3hmdib135j.apps.googleusercontent.com"
	DefaultClientSecret = "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" // Public client secret from official Gemini CLI
	DefaultBaseURL      = "https://cloudcode-pa.googleapis.com"
	DefaultAPIVersion   = "v1internal"
	DefaultTimeout      = 15 * 60
)

// OAuth scopes required for Gemini API access
var DefaultScopes = []string{
	"https://www.googleapis.com/auth/cloud-platform",
	"https://www.googleapis.com/auth/userinfo.email",
	"https://www.googleapis.com/auth/userinfo.profile",
}

// AuthMode specifies the authentication method to use
type AuthMode string

const (
	// AuthModeOAuth uses Google OAuth 2.0 for authentication (free tier)
	AuthModeOAuth AuthMode = "oauth"

	// AuthModeAPIKey uses a Gemini API key for authentication
	AuthModeAPIKey AuthMode = "api_key"

	// AuthModeADC uses Application Default Credentials (for GCP environments)
	AuthModeADC AuthMode = "adc"
)

// Config holds configuration for the Gemini provider.
type Config struct {
	// AuthMode specifies the authentication method
	// Default: AuthModeOAuth
	AuthMode AuthMode

	// OAuth credentials (uses defaults if empty)
	ClientID     string
	ClientSecret string

	// API key for AuthModeAPIKey
	APIKey string

	// GCP Project ID (required for some tiers, optional for free tier)
	ProjectID string

	// Token storage path for caching OAuth tokens
	// Default: ~/.config/gemini-cli/oauth_credentials.json
	TokenPath string

	// API endpoint (for testing or alternative endpoints)
	// Default: https://cloudcode-pa.googleapis.com
	BaseURL string

	// API version
	// Default: v1internal
	APIVersion string

	// Default model to use
	// Example: "gemini-2.5-pro", "gemini-2.0-flash"
	Model string

	// HTTP settings
	Timeout       int    // Request timeout in seconds (default: 900)
	Proxy         string // HTTP proxy URL
	NoBrowser     bool   // Don't open browser for OAuth (use device code flow)
	TransportMode string // "resilient", "aggressive", "fast", "default"

	// Observability
	Logger observability.Logger
	Tracer observability.Tracer

	// Provider name override (default: "gemini")
	Name string

	// RawDebugWriter, if set, enables raw JSON request/response logging
	// All HTTP request/response bodies will be written to this writer
	RawDebugWriter io.Writer
}

// Validate checks the configuration for errors.
func (c *Config) Validate() error {
	switch c.AuthMode {
	case AuthModeOAuth, "":
		// OAuth mode uses default credentials if not specified
		if c.ClientID == "" {
			c.ClientID = DefaultClientID
		}
		if c.ClientSecret == "" {
			c.ClientSecret = os.Getenv("GEMINI_OAUTH_CLIENT_SECRET")
		}
		if c.ClientSecret == "" {
			c.ClientSecret = DefaultClientSecret // Use embedded public client secret
		}
	case AuthModeAPIKey:
		if c.APIKey == "" {
			// Check environment variable
			c.APIKey = os.Getenv("GEMINI_API_KEY")
			if c.APIKey == "" {
				return errors.New("API key required for api_key auth mode")
			}
		}
	case AuthModeADC:
		// ADC doesn't require additional configuration
	default:
		return errors.New("invalid auth mode: must be 'oauth', 'api_key', or 'adc'")
	}

	// Set defaults
	if c.BaseURL == "" {
		c.BaseURL = DefaultBaseURL
	}
	if c.APIVersion == "" {
		c.APIVersion = DefaultAPIVersion
	}
	if c.TokenPath == "" {
		c.TokenPath = defaultTokenPath()
	}
	if c.Timeout == 0 {
		c.Timeout = DefaultTimeout
	}
	if c.Model == "" {
		c.Model = "gemini-2.5-pro"
	}
	if c.Name == "" {
		c.Name = "gemini"
	}

	return nil
}

// defaultTokenPath returns the default path for storing OAuth tokens.
func defaultTokenPath() string {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return ".gemini_oauth_credentials.json"
	}
	return filepath.Join(homeDir, ".config", "gemini-cli", "oauth_credentials.json")
}

// GetMethodURL returns the full URL for an API method.
func (c *Config) MethodURL(method string) string {
	return c.BaseURL + "/" + c.APIVersion + ":" + method
}
