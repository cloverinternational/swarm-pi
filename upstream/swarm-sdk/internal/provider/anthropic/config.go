// Package anthropic provides an implementation of the provider.Provider interface
// for Anthropic's Claude API with full beta feature support.
package anthropic

import (
	"fmt"
	"io"
	"os"
	"slices"
	"strings"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/observability"
	sdkerr "github.com/Swarm-Code/mono/swarm-sdk/internal/sdkerr"
)

const defaultRequestTimeoutSeconds = 15 * 60

// Config holds configuration for the Anthropic provider.
type Config struct {
	// APIKey is the Anthropic API key (required).
	// Format: "sk-ant-..."
	APIKey string

	// BaseURL is the API endpoint (optional, defaults to Anthropic's production URL).
	BaseURL string

	// DefaultModel is the model to use when not specified in requests.
	DefaultModel string

	// MaxRetries is the number of times to retry failed requests.
	MaxRetries int

	// Timeout is the request timeout in seconds.
	Timeout int

	// BetaHeaders are beta feature flags to enable.
	// Examples: "prompt-caching-2024-07-31", "computer-use-2024-10-22"
	BetaHeaders []string

	// IsOAuth indicates if this provider uses an OAuth token.
	// When true, the required OAuth system prompt prefix is automatically added.
	IsOAuth bool

	// AccountID is the unique account identifier for this credential, used to
	// look up the per-account DeviceIdentity when building request metadata.
	// Set when creating a provider from a multi-account CredentialStore entry.
	AccountID string

	// TransportMode controls the HTTP transport configuration.
	// Options: "default", "resilient", "aggressive", "fast"
	// - "default": Standard Go defaults (may fail on slow networks)
	// - "resilient": Optimized for reliability with longer timeouts (recommended)
	// - "aggressive": Maximum reliability for very unstable networks
	// - "fast": Tighter timeouts for fast, stable networks
	TransportMode string

	// ConnectionRetries is the number of times to retry on connection failures
	// (dial timeout, TLS handshake, DNS errors). Only used when TransportMode != "default".
	// Default: 3
	ConnectionRetries int

	// DialTimeout is the maximum time to establish a TCP connection in seconds.
	// Default: 60 for resilient, 120 for aggressive, 10 for fast
	DialTimeout int

	// TCPKeepAlive is the interval for TCP keepalive probes in seconds.
	// Helps detect broken connections on unstable networks.
	// Default: 30
	TCPKeepAlive int

	// Logger is the structured logger for this provider.
	// Defaults to a noop logger when nil.
	Logger observability.Logger

	// Tracer is the distributed tracer for this provider.
	// Defaults to a noop tracer when nil.
	Tracer observability.Tracer

	// RawDebugWriter, if set, enables raw JSON request/response logging
	// All HTTP request/response bodies will be written to this writer
	RawDebugWriter io.Writer
}

// Validate checks if the configuration is valid.
// ApplyDefaults fills in zero-value fields with sensible defaults.
// Called automatically by Validate; can also be called independently.
func (c *Config) ApplyDefaults() {
	if IsOAuthToken(c.APIKey) {
		c.IsOAuth = true
	}
	if c.BaseURL == "" {
		c.BaseURL = "https://api.anthropic.com"
	}
	if c.DefaultModel == "" {
		c.DefaultModel = "claude-sonnet-4-6"
	}
	if c.MaxRetries == 0 {
		c.MaxRetries = 2
	}
	if c.Timeout == 0 {
		c.Timeout = defaultRequestTimeoutSeconds
	}
	if c.TransportMode == "" {
		c.TransportMode = "resilient"
	}
	if c.ConnectionRetries == 0 {
		c.ConnectionRetries = 3
	}
	if c.DialTimeout == 0 {
		switch c.TransportMode {
		case "unstable":
			c.DialTimeout = 600
		case "aggressive":
			c.DialTimeout = 300
		case "fast":
			c.DialTimeout = 10
		default:
			c.DialTimeout = 120
		}
	}
	if c.TCPKeepAlive == 0 {
		switch c.TransportMode {
		case "unstable":
			c.TCPKeepAlive = 3
		case "aggressive":
			c.TCPKeepAlive = 5
		case "fast":
			c.TCPKeepAlive = 60
		default:
			c.TCPKeepAlive = 10
		}
	}
}

// Validate applies defaults and checks that the configuration is valid.
func (c *Config) Validate() error {
	c.ApplyDefaults()

	if c.APIKey == "" || strings.TrimSpace(c.APIKey) == "" {
		return sdkerr.Permanent(
			"anthropic.config.missing_api_key",
			"API key is required (set ANTHROPIC_API_KEY or provide in config)",
		)
	}
	if c.Timeout != -1 && c.Timeout < 1 {
		return sdkerr.Permanent(
			"anthropic.config.invalid_timeout",
			fmt.Sprintf("timeout must be -1 (no timeout) or a positive number of seconds, got %d", c.Timeout),
		)
	}
	validModes := map[string]bool{"default": true, "resilient": true, "aggressive": true, "unstable": true, "fast": true}
	if !validModes[c.TransportMode] {
		return sdkerr.Permanent(
			"anthropic.config.invalid_transport_mode",
			fmt.Sprintf("transport mode must be one of: default, resilient, aggressive, unstable, fast; got %s", c.TransportMode),
		)
	}
	return nil
}

// WithDefaults returns a new config with default values applied.
func WithDefaults() Config {
	return Config{
		BaseURL:      "https://api.anthropic.com",
		DefaultModel: "claude-sonnet-4-6",
		MaxRetries:   2,
		Timeout:      defaultRequestTimeoutSeconds,
		BetaHeaders:  []string{},
	}
}

// NewFromEnv creates an Anthropic provider from environment variables.
// It reads ANTHROPIC_API_KEY automatically — no config required.
//
// This is the zero-config entry point for the Anthropic provider:
//
//	p, err := anthropic.NewFromEnv()
//	resp, err := p.Chat(ctx, provider.ChatRequest{Messages: msgs})
func NewFromEnv() (*Provider, error) {
	cfg := WithDefaults()
	cfg.APIKey = os.Getenv("ANTHROPIC_API_KEY")
	return New(cfg) // Logger/Tracer nil → noop
}

// WithBeta adds a beta feature flag to the configuration.
func (c *Config) WithBeta(feature string) *Config {
	c.BetaHeaders = append(c.BetaHeaders, feature)
	return c
}

// HasBeta checks if a beta feature is enabled.
func (c *Config) HasBeta(feature string) bool {
	return slices.Contains(c.BetaHeaders, feature)
}

// Common beta feature constants
const (
	BetaPromptCaching  = "prompt-caching-2024-07-31"
	BetaComputerUse    = "computer-use-2025-01-24"
	BetaWebSearch      = "web-search-20250305"
	BetaCodeExecution  = "code-execution-2025-05-22"
	BetaMessageBatches = "message-batches-2024-09-24"

	// DEPRECATED: Text editor and bash tools are part of computer-use beta
	// Use BetaComputerUse instead
	BetaTextEditor = "computer-use-2025-01-24"
	BetaBash       = "computer-use-2025-01-24"
)

// IMPORTANT: Citations is now generally available (GA)
// Citations are automatically returned in ContentBlock.Citations when documents are provided in the request.
// They are NOT configured via a request parameter - they come back in the API response.
// No special configuration or beta header is needed to use citations.
