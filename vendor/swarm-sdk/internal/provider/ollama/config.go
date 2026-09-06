package ollama

import (
	"io"
	"time"
)

const (
	// DefaultCloudBaseURL is the default URL for Ollama Cloud
	DefaultCloudBaseURL = "https://ollama.com"

	// DefaultLocalBaseURL is the default URL for local Ollama
	DefaultLocalBaseURL = "http://localhost:11434"

	// DefaultTimeout is the default request timeout
	DefaultTimeout = 15 * time.Minute
)

// Config represents the configuration for the Ollama provider
type Config struct {
	// BaseURL is the API endpoint URL
	// Cloud: https://ollama.com
	// Local: http://localhost:11434
	BaseURL string `json:"baseUrl"`

	// APIKey is the authentication key for Ollama Cloud
	// Required for cloud mode, ignored for local mode
	APIKey string `json:"apiKey,omitempty"`

	// Model is the default model to use
	Model string `json:"model,omitempty"`

	// Timeout for API requests
	Timeout time.Duration `json:"timeout,omitempty"`

	// Model options
	Options ModelOptions `json:"options"`

	// RawDebugWriter, if set, enables raw JSON request/response logging
	// All HTTP request/response bodies will be written to this writer
	RawDebugWriter io.Writer `json:"-"`
}

// DefaultCloudConfig returns the default configuration for Ollama Cloud
func DefaultCloudConfig() *Config {
	return &Config{
		BaseURL: DefaultCloudBaseURL,
		Timeout: DefaultTimeout,
	}
}

// DefaultLocalConfig returns the default configuration for local Ollama
func DefaultLocalConfig() *Config {
	return &Config{
		BaseURL: DefaultLocalBaseURL,
		Timeout: DefaultTimeout,
	}
}

// IsCloud returns true if using Ollama Cloud
func (c *Config) IsCloud() bool {
	return c.BaseURL == DefaultCloudBaseURL || c.APIKey != ""
}

// HasAuth returns true if authentication is configured
func (c *Config) HasAuth() bool {
	return c.APIKey != ""
}

// Validate validates the configuration
func (c *Config) ApplyDefaults() {
	if c.BaseURL == "" {
		c.BaseURL = DefaultLocalBaseURL
	}
	if c.Timeout == 0 {
		c.Timeout = DefaultTimeout
	}
}

func (c *Config) Validate() error {
	c.ApplyDefaults()
	return nil
}
