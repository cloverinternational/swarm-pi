package openai

import (
	"io"
	"os"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/observability"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/observability/noop"
)

// Config provides configuration for the OpenAI provider.
type Config struct {
	// APIKey is the OpenAI API key (required).
	APIKey string

	// BaseURL is the API endpoint (defaults to https://api.openai.com/v1).
	BaseURL string

	// OrganizationID is the optional organization ID.
	OrganizationID string

	// Name is the provider name returned by Name() (defaults to "openai").
	// Use this for OpenAI-compatible providers like Cerebras, OpenRouter, etc.
	Name string

	// Logger for structured logging (required).
	Logger observability.Logger

	// Tracer for distributed tracing (required).
	Tracer observability.Tracer

	// TransportMode controls the HTTP transport configuration.
	// Options: "default", "resilient", "aggressive", "unstable", "fast"
	// - "default": Standard Go defaults (may fail on slow networks)
	// - "resilient": Optimized for reliability with longer timeouts (recommended)
	// - "aggressive": Maximum reliability for very unstable networks
	// - "unstable": For the worst network conditions (satellite, poor mobile)
	// - "fast": Tighter timeouts for fast, stable networks
	// Default: "resilient"
	TransportMode string

	// Timeout is the request timeout in seconds.
	// 0 means use default (900s), -1 means no timeout.
	// Default: 900
	Timeout int

	// DialTimeout is the maximum time to establish a TCP connection in seconds.
	// 0 means use transport mode default.
	DialTimeout int

	// TCPKeepAlive is the interval for TCP keepalive probes in seconds.
	// 0 means use transport mode default.
	TCPKeepAlive int
	// HTTPMaxRetries controls retries performed by the shared HTTP client.
	// Nil preserves the default policy, zero disables retries.
	HTTPMaxRetries *int

	// RawDebugWriter, if set, enables raw JSON request/response logging
	// All HTTP request/response bodies will be written to this writer
	RawDebugWriter io.Writer
}

// Validate checks if the configuration is valid.
// It applies noop defaults for Logger and Tracer when they are nil,
// consistent with the other provider implementations.
func (c *Config) Validate() error {
	if c.Logger == nil {
		c.Logger = noop.NewLogger()
	}
	if c.Tracer == nil {
		c.Tracer = noop.NewTracer()
	}
	if c.APIKey == "" {
		return &ConfigError{Field: "APIKey", Message: "API key is required (set OPENAI_API_KEY or provide in config)"}
	}
	return nil
}

// NewFromEnv creates an OpenAI provider from environment variables.
// It reads OPENAI_API_KEY automatically — no config required.
//
//	p, err := openai.NewFromEnv()
func NewFromEnv() (*Provider, error) {
	return New(Config{APIKey: os.Getenv("OPENAI_API_KEY")})
}

// ConfigError represents a configuration validation error.
type ConfigError struct {
	Field   string
	Message string
}

// Error implements the error interface.
func (e *ConfigError) Error() string {
	return "openai config: " + e.Field + ": " + e.Message
}
