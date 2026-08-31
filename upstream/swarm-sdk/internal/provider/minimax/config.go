// Package minimax implements the provider.Provider interface for MiniMax's API.
// MiniMax provides an Anthropic-compatible endpoint at https://api.minimax.io/anthropic
package minimax

import (
	"fmt"
	"io"
	"os"
)

const (
	// DefaultBaseURL is the default API endpoint for international users.
	DefaultBaseURL = "https://api.minimax.io/anthropic"

	// ChinaBaseURL is the API endpoint for users in China.
	ChinaBaseURL = "https://api.minimaxi.com/anthropic"

	// DefaultModel is the default model to use when none is specified.
	DefaultModel = "MiniMax-M2.5"
)

// Config holds configuration for the MiniMax provider.
type Config struct {
	// APIKey is the MiniMax API key (required).
	// Can also be set via MINIMAX_API_KEY environment variable.
	APIKey string

	// BaseURL is the API endpoint URL.
	// Defaults to DefaultBaseURL (https://api.minimax.io/anthropic).
	// Use ChinaBaseURL for users in China.
	BaseURL string

	// DefaultModel is the model to use when none is specified in requests.
	// Defaults to "MiniMax-M2.5".
	DefaultModel string

	// Timeout is the HTTP client timeout in seconds.
	// Set to -1 for no timeout, 0 for default timeout.
	Timeout int

	// MaxTokens is the default maximum tokens for responses.
	// Can be overridden per-request.
	MaxTokens int

	// UseChinaEndpoint forces the use of the China endpoint.
	UseChinaEndpoint bool

	// RawDebugWriter, if set, enables raw JSON request/response logging
	// All HTTP request/response bodies will be written to this writer
	RawDebugWriter io.Writer
}

// Validate validates the configuration and sets defaults.
func (c *Config) Validate() error {
	// Load API key from environment if not set
	if c.APIKey == "" {
		c.APIKey = os.Getenv("MINIMAX_API_KEY")
	}

	if c.APIKey == "" {
		return fmt.Errorf("minimax API key is required (set APIKey or MINIMAX_API_KEY env var)")
	}

	// Set default base URL
	if c.BaseURL == "" {
		if c.UseChinaEndpoint {
			c.BaseURL = ChinaBaseURL
		} else {
			c.BaseURL = DefaultBaseURL
		}
	}

	// Set default model
	if c.DefaultModel == "" {
		c.DefaultModel = DefaultModel
	}

	// Set default max tokens
	if c.MaxTokens == 0 {
		c.MaxTokens = 64000 // MiniMax-M2.5 supports up to 64K
	}

	return nil
}
