package openai

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/observability"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/provider"
	httplib "github.com/Swarm-Code/mono/swarm-sdk/internal/provider/http"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/provider/openai/profiles"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/provider/openai/quirks"
)

const defaultRequestTimeout = 15 * time.Minute

// Provider implements provider.Provider for OpenAI.
type Provider struct {
	client  *httplib.Client
	apiKey  string
	baseURL string
	orgID   string
	name    string // Custom provider name (defaults to "openai")
	logger  observability.Logger
	tracer  observability.Tracer
	profile *profiles.Profile // Optional: provider profile
	quirks  quirks.Adapter    // Optional: quirk adapter
}

// New creates a new OpenAI provider.
func New(config Config) (*Provider, error) {
	if err := config.Validate(); err != nil {
		return nil, err
	}

	baseURL := strings.TrimRight(config.BaseURL, "/")
	if baseURL == "" {
		baseURL = "https://api.openai.com/v1"
	}

	// Create resilient HTTP client based on transport mode
	underlyingClient := createResilientHTTPClientOpenAI(config)

	// Wrap with debug transport if RawDebugWriter is set
	if config.RawDebugWriter != nil {
		base := underlyingClient.Transport
		if base == nil {
			base = http.DefaultTransport
		}
		underlyingClient.Transport = provider.NewDebugTransport(base, config.RawDebugWriter)
	}

	// Create HTTP client wrapper with observability
	httpClient, err := httplib.NewClient(httplib.ClientConfig{
		HTTPClient:      underlyingClient,
		Logger:          config.Logger,
		Tracer:          config.Tracer,
		OperationPrefix: "openai",
		HTTPMaxRetries:  config.HTTPMaxRetries,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create HTTP client: %w", err)
	}

	// Set default headers
	httpClient.SetDefaultHeader("Authorization", "Bearer "+config.APIKey)
	if config.OrganizationID != "" {
		httpClient.SetDefaultHeader("OpenAI-Organization", config.OrganizationID)
	}

	// Default provider name to "openai" if not specified
	providerName := config.Name
	if providerName == "" {
		providerName = "openai"
	}

	return &Provider{
		client:  httpClient,
		apiKey:  config.APIKey,
		baseURL: baseURL,
		orgID:   config.OrganizationID,
		name:    providerName,
		logger:  config.Logger,
		tracer:  config.Tracer,
	}, nil
}

// Name implements provider.Provider.
func (p *Provider) Name() string {
	if p.profile != nil {
		return p.profile.Name
	}
	return p.name
}

// Chat implements provider.Provider.
func (p *Provider) Chat(ctx context.Context, req provider.ChatRequest) (*provider.ChatResponse, error) {
	return p.chat(ctx, req)
}

// Stream implements provider.Provider.
func (p *Provider) Stream(ctx context.Context, req provider.ChatRequest) (<-chan provider.StreamChunk, error) {
	return p.stream(ctx, req)
}

// Capabilities implements provider.Provider.
func (p *Provider) Capabilities() provider.Capabilities {
	caps := p.capabilities()

	// Override with profile features if available
	if p.profile != nil {
		caps.Streaming = p.profile.Features.Streaming
		caps.FunctionCalling = p.profile.Features.FunctionCalling
		caps.Vision = p.profile.Features.Vision
		// Add more feature mappings as needed
	}

	return caps
}

// createResilientHTTPClientOpenAI creates an HTTP client with resilient transport settings.
func createResilientHTTPClientOpenAI(config Config) *http.Client {
	// Default transport mode to resilient
	transportMode := config.TransportMode
	if transportMode == "" {
		transportMode = "resilient"
	}

	// Select transport configuration based on mode
	var transportConfig httplib.TransportConfig
	switch transportMode {
	case "aggressive":
		transportConfig = httplib.AggressiveRetryTransportConfig()
	case "unstable":
		transportConfig = httplib.UnstableNetworkTransportConfig()
	case "fast":
		transportConfig = httplib.FastTransportConfig()
	case "default":
		// Use Go's default transport
		return createBasicHTTPClientOpenAI(config)
	default: // "resilient" or empty
		transportConfig = httplib.DefaultTransportConfig()
	}

	// Override specific settings from config if provided
	if config.DialTimeout > 0 {
		transportConfig.DialTimeout = time.Duration(config.DialTimeout) * time.Second
	}
	if config.TCPKeepAlive > 0 {
		transportConfig.TCPKeepAlive = time.Duration(config.TCPKeepAlive) * time.Second
	}

	// Create transport with our settings
	transport := httplib.NewTransport(transportConfig)

	// Determine client timeout
	timeout := config.Timeout
	if timeout == 0 {
		timeout = int(defaultRequestTimeout / time.Second)
	}

	var clientTimeout time.Duration
	if timeout == -1 {
		clientTimeout = 0 // No timeout
	} else {
		clientTimeout = time.Duration(timeout) * time.Second
	}

	return &http.Client{
		Transport: transport,
		Timeout:   clientTimeout,
	}
}

// createBasicHTTPClientOpenAI creates a basic HTTP client without resilient transport.
func createBasicHTTPClientOpenAI(config Config) *http.Client {
	timeout := config.Timeout
	if timeout == 0 {
		timeout = int(defaultRequestTimeout / time.Second)
	}

	var clientTimeout time.Duration
	if timeout == -1 {
		clientTimeout = 0
	} else {
		clientTimeout = time.Duration(timeout) * time.Second
	}

	return &http.Client{
		Timeout: clientTimeout,
	}
}
