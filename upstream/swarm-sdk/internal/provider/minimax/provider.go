// Package minimax implements the provider.Provider interface for MiniMax's API.
// MiniMax provides an Anthropic-compatible endpoint at https://api.minimax.io/anthropic
package minimax

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/conversation"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/observability"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/provider"
	httplib "github.com/Swarm-Code/mono/swarm-sdk/internal/provider/http"
)

// Provider implements the provider.Provider interface for MiniMax's API.
type Provider struct {
	config           Config
	client           *httplib.Client
	logger           observability.Logger
	tracer           observability.Tracer
	lastProviderJSON json.RawMessage           // Last translated request for debugging
	rawEventCallback provider.RawEventCallback // Callback for raw API event logging
}

// New creates a new MiniMax provider instance.
func New(config Config, logger observability.Logger, tracer observability.Tracer) (*Provider, error) {
	// Validate configuration
	if err := config.Validate(); err != nil {
		return nil, err
	}

	// Create HTTP client with resilient transport
	httpClient := createResilientHTTPClient(config)

	// Wrap with debug transport if RawDebugWriter is set
	if config.RawDebugWriter != nil {
		base := httpClient.Transport
		if base == nil {
			base = http.DefaultTransport
		}
		httpClient.Transport = provider.NewDebugTransport(base, config.RawDebugWriter)
	}

	// MiniMax uses Bearer token authentication (like Anthropic OAuth)
	headers := map[string]string{
		"Authorization":     "Bearer " + config.APIKey,
		"anthropic-version": "2023-06-01", // Required for Anthropic compatibility
		"content-type":      "application/json",
	}

	client, err := httplib.NewClient(httplib.ClientConfig{
		HTTPClient:      httpClient,
		DefaultHeaders:  headers,
		Logger:          logger,
		Tracer:          tracer,
		OperationPrefix: "minimax",
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create HTTP client: %w", err)
	}

	return &Provider{
		config: config,
		client: client,
		logger: logger,
		tracer: tracer,
	}, nil
}

// NewProvider creates a new MiniMax provider with minimal configuration.
// This is a convenience function for testing and simple use cases.
func NewProvider(config Config) (*Provider, error) {
	return New(config, nil, nil)
}

// Name returns the unique identifier for this provider.
func (p *Provider) Name() string {
	return "minimax"
}

// Chat sends a synchronous chat request and returns the complete response.
func (p *Provider) Chat(ctx context.Context, req provider.ChatRequest) (*provider.ChatResponse, error) {
	return p.chat(ctx, req)
}

// Stream sends a chat request and streams the response in chunks.
func (p *Provider) Stream(ctx context.Context, req provider.ChatRequest) (<-chan provider.StreamChunk, error) {
	return p.stream(ctx, req)
}

// Capabilities returns the capabilities and limits of this provider.
func (p *Provider) Capabilities() provider.Capabilities {
	return getCapabilities()
}

// LastProviderJSON returns the JSON representation of the last translated request.
// This is useful for debugging to see exactly what was sent to the API.
func (p *Provider) LastProviderJSON() json.RawMessage {
	return p.lastProviderJSON
}

// SetRawEventCallback sets the callback for raw API event logging.
func (p *Provider) SetRawEventCallback(callback provider.RawEventCallback) {
	p.rawEventCallback = callback
}

// Close cleans up resources used by the provider.
func (p *Provider) Close() error {
	// HTTP client doesn't require explicit cleanup
	return nil
}

// ============================================================================
// Utility Methods
// ============================================================================

// EstimateTokens estimates token count for text.
func (p *Provider) EstimateTokens(text string) (int, error) {
	// Use a simple approximation: ~4 characters per token
	return len(text) / 4, nil
}

// EstimateTokensForMessages estimates token count for messages.
func (p *Provider) EstimateTokensForMessages(messages []*conversation.Message) (int, error) {
	total := 0
	for _, msg := range messages {
		total += len(msg.Content) / 4
		if msg.Thinking != "" {
			total += len(msg.Thinking) / 4
		}
	}
	return total, nil
}

// createResilientHTTPClient creates an HTTP client with resilient transport settings.
func createResilientHTTPClient(config Config) *http.Client {
	transportConfig := httplib.DefaultTransportConfig()

	// Override specific settings from config if provided
	if config.Timeout > 0 {
		transportConfig.DialTimeout = time.Duration(config.Timeout) * time.Second
	}

	// Create transport with our settings
	transport := httplib.NewTransport(transportConfig)

	// Determine client timeout
	var clientTimeout time.Duration
	if config.Timeout == -1 {
		clientTimeout = 0 // No timeout
	} else if config.Timeout > 0 {
		clientTimeout = time.Duration(config.Timeout) * time.Second
	}

	return &http.Client{
		Transport: transport,
		Timeout:   clientTimeout,
	}
}
