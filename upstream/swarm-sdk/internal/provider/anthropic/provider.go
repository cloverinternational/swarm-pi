// Package anthropic implements the provider.Provider interface for Anthropic's Claude API.
package anthropic

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/conversation"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/observability"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/observability/noop"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/provider"
	httplib "github.com/Swarm-Code/mono/swarm-sdk/internal/provider/http"
)

const defaultRawDumpPath = "/tmp/sac-raw.log"

func rawDumpPathFromEnv(value string) string {
	value = strings.TrimSpace(value)
	switch strings.ToLower(value) {
	case "", "0", "false", "off":
		return ""
	case "1", "true", "yes", "on":
		return defaultRawDumpPath
	default:
		return value
	}
}

func openRawDumpWriterFromEnv() *os.File {
	path := rawDumpPathFromEnv(os.Getenv("SAC_RAW_DUMP"))
	return openRawDumpWriter(path)
}

func openRawDumpWriter(path string) *os.File {
	if path == "" {
		return nil
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return nil
	}
	return f
}

// Provider implements the provider.Provider interface for Anthropic's Claude API.
type Provider struct {
	config           Config
	client           *httplib.Client
	logger           observability.Logger
	tracer           observability.Tracer
	noAmbientEnv     bool
	lastProviderJSON json.RawMessage           // Last translated request for debugging
	rawEventCallback provider.RawEventCallback // Callback for raw API event logging
	translationCache *TranslationCache         // Caches provider-format messages to avoid O(N²) rebuild

	// Raw HTTP capture for debugging - stores complete untruncated request/response
	lastRawRequest  *provider.RawHTTPRequestCapture
	lastRawResponse *provider.RawHTTPResponseCapture
	rawCaptureMu    sync.RWMutex

	// Managed-mode state (SWARMOS_OAUTH_MANAGED). When managed mode is active and
	// this is an OAuth provider, the access token is reloaded from oauth.json before
	// each request (mtime-cached) and the provider NEVER self-refreshes. See
	// oauth_managed.go. These fields are unused in the default self-refresh path.
	managedMu          sync.Mutex
	managedTokenMtime  int64  // mtime (UnixNano) of oauth.json at last successful reload
	managedAccessToken string // last access token loaded from disk (parse-skip guard)
}

// New creates a new Anthropic provider instance.
// Logger and Tracer are read from config.Logger and config.Tracer.
// Both default to noop implementations when nil.
func New(config Config) (*Provider, error) {
	return newProvider(config, false)
}

func newProvider(config Config, noAmbientEnv bool) (*Provider, error) {
	// Validate configuration
	if err := config.Validate(); err != nil {
		return nil, err
	}
	if noAmbientEnv && config.IsOAuth {
		return nil, fmt.Errorf("anthropic: OAuth configuration is not supported with NoAmbientEnv")
	}

	logger := config.Logger
	if logger == nil {
		logger = noop.NewLogger()
	}
	tracer := config.Tracer
	if tracer == nil {
		tracer = noop.NewTracer()
	}

	// Create HTTP client with resilient transport based on config
	httpClient := createResilientHTTPClient(config)

	// Honour SAC_RAW_DUMP on every construction path (register.go only wires
	// it for one factory; the daemon/client paths construct here directly).
	// Boolean values use the legacy default path; an explicit path lets callers
	// isolate sensitive captures per run.
	if config.RawDebugWriter == nil && !noAmbientEnv {
		config.RawDebugWriter = openRawDumpWriterFromEnv()
	}

	// Wrap with debug transport if RawDebugWriter is set
	if config.RawDebugWriter != nil {
		base := httpClient.Transport
		if base == nil {
			base = http.DefaultTransport
		}
		httpClient.Transport = provider.NewDebugTransport(base, config.RawDebugWriter)
	}

	// OAuth tokens use Authorization: Bearer header and require beta header
	// Regular API keys use x-api-key header
	headers := map[string]string{
		"anthropic-version": "2023-06-01", // Latest stable API version
		"content-type":      "application/json",
	}

	if config.IsOAuth {
		headers["Authorization"] = "Bearer " + config.APIKey
		headers["anthropic-beta"] = "oauth-2025-04-20" // Required for OAuth support
		headers["x-app"] = "cli"                       // Required for OAuth authentication
	} else {
		headers["x-api-key"] = config.APIKey
	}

	client, err := httplib.NewClient(httplib.ClientConfig{
		HTTPClient:     httpClient,
		DefaultHeaders: headers,
		Logger:         logger,
		Tracer:         tracer,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create HTTP client: %w", err)
	}

	return &Provider{
		config:           config,
		client:           client,
		logger:           logger,
		tracer:           tracer,
		noAmbientEnv:     noAmbientEnv,
		translationCache: NewTranslationCache(),
	}, nil
}

// NewProvider creates a new Anthropic provider.
//
// Deprecated: NewProvider is now identical to New — use New(config) directly.
func NewProvider(config Config) (*Provider, error) {
	return New(config)
}

// Name returns the unique identifier for this provider.
func (p *Provider) Name() string {
	return "anthropic"
}

// Chat sends a synchronous chat request and returns the complete response.
func (p *Provider) Chat(ctx context.Context, req provider.ChatRequest) (*provider.ChatResponse, error) {
	// Implementation in chat.go
	return p.chat(ctx, req)
}

// Stream sends a chat request and streams the response in chunks.
func (p *Provider) Stream(ctx context.Context, req provider.ChatRequest) (<-chan provider.StreamChunk, error) {
	// Implementation in stream.go
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

// SetRawEventCallback sets the callback for raw API event logging
func (p *Provider) SetRawEventCallback(callback provider.RawEventCallback) {
	p.rawEventCallback = callback
}

// refreshOAuthToken attempts to refresh the OAuth token and update the client headers
func (p *Provider) refreshOAuthToken(ctx context.Context) error {
	if !p.config.IsOAuth {
		return fmt.Errorf("not an OAuth provider")
	}

	p.logger.Info(ctx, "anthropic.oauth.refreshing_token")

	// Attempt to refresh and store the new token
	newToken, err := RefreshAndStoreToken()
	if err != nil {
		p.logger.Error(ctx, "anthropic.oauth.refresh_failed",
			observability.F("error", err.Error()),
		)
		return err
	}

	p.logger.Info(ctx, "anthropic.oauth.refresh_success",
		observability.F("new_expiry", newToken.Expiry),
	)

	// Update the provider config + HTTP client with the new token.
	return p.rebuildClientWithToken(newToken.AccessToken)
}

// rebuildClientWithToken updates p.config.APIKey and recreates the HTTP client
// so its default Authorization header carries the supplied OAuth access token.
// It preserves the resilient transport and any debug transport wrapper.
//
// Extracted from refreshOAuthToken so the managed (no-self-refresh) mode can
// swap in a token reloaded from disk without going through a refresh.
func (p *Provider) rebuildClientWithToken(accessToken string) error {
	p.config.APIKey = accessToken

	headers := map[string]string{
		"Authorization":     "Bearer " + accessToken,
		"anthropic-version": "2023-06-01",
		"anthropic-beta":    "oauth-2025-04-20",
		"x-app":             "cli",
		"content-type":      "application/json",
	}

	// Recreate the HTTP client with new headers using resilient transport
	httpClient := createResilientHTTPClient(p.config)

	// Wrap with debug transport if RawDebugWriter is set
	if p.config.RawDebugWriter != nil {
		base := httpClient.Transport
		if base == nil {
			base = http.DefaultTransport
		}
		httpClient.Transport = provider.NewDebugTransport(base, p.config.RawDebugWriter)
	}

	client, err := httplib.NewClient(httplib.ClientConfig{
		HTTPClient:     httpClient,
		DefaultHeaders: headers,
		Logger:         p.logger,
		Tracer:         p.tracer,
	})
	if err != nil {
		return fmt.Errorf("failed to recreate HTTP client: %w", err)
	}

	p.client = client

	return nil
}

// Close cleans up resources used by the provider.
func (p *Provider) Close() error {
	// HTTP client doesn't require explicit cleanup
	return nil
}

// ============================================================================
// Advanced Features - Utility Methods
// ============================================================================

// ExtractJSON extracts structured JSON from a response.
func (p *Provider) ExtractJSON(resp *provider.ChatResponse) (map[string]any, error) {
	return ExtractJSONFromResponse(resp)
}

// EstimateTokens estimates token count for text.
func (p *Provider) EstimateTokens(text string) (int, error) {
	return CountTokens(p, text)
}

// EstimateTokensForMessages estimates token count for messages.
func (p *Provider) EstimateTokensForMessages(messages []*conversation.Message) (int, error) {
	return CountTokensForMessages(p, messages)
}

// OptimizeContext reduces messages to fit within token budget.
func (p *Provider) OptimizeContext(messages []*conversation.Message, targetTokens int) ([]*conversation.Message, error) {
	return OptimizeMessages(p, messages, targetTokens)
}

// CreateWorkflow creates a tool orchestration workflow.
func (p *Provider) CreateWorkflow(tools []provider.Tool, workflowType WorkflowType) (*ToolWorkflow, error) {
	return CreateToolWorkflow(tools, workflowType)
}

// ProcessResponseMetadata analyzes and enriches response with metadata.
func (p *Provider) ProcessResponseMetadata(resp *provider.ChatResponse) *ProcessedResponse {
	return ProcessResponse(resp)
}

// ValidateQuality assesses response quality.
func (p *Provider) ValidateQuality(resp *provider.ChatResponse) *ResponseQuality {
	return ValidateResponseQuality(resp)
}

// FormatForDisplay formats response for display.
func (p *Provider) FormatForDisplay(resp *provider.ChatResponse, format DisplayFormat) string {
	return FormatResponseForDisplay(resp, format)
}

// createResilientHTTPClient creates an HTTP client with resilient transport settings.
func createResilientHTTPClient(config Config) *http.Client {
	// Select transport configuration based on mode
	var transportConfig httplib.TransportConfig
	switch config.TransportMode {
	case "aggressive":
		transportConfig = httplib.AggressiveRetryTransportConfig()
	case "unstable":
		transportConfig = httplib.UnstableNetworkTransportConfig()
	case "fast":
		transportConfig = httplib.FastTransportConfig()
	case "default":
		// Use Go's default transport (not recommended)
		return createBasicHTTPClient(config)
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

// createBasicHTTPClient creates a basic HTTP client without resilient transport.
// Only used when TransportMode is explicitly "default".
func createBasicHTTPClient(config Config) *http.Client {
	var clientTimeout time.Duration
	if config.Timeout == -1 {
		clientTimeout = 0 // No timeout
	} else if config.Timeout > 0 {
		clientTimeout = time.Duration(config.Timeout) * time.Second
	}
	return &http.Client{
		Timeout: clientTimeout,
	}
}

// LastRawHTTPRequest implements provider.HTTPDebugProvider.
// Returns the complete raw HTTP request that was sent (zero truncation).
func (p *Provider) LastRawHTTPRequest() *provider.RawHTTPRequestCapture {
	p.rawCaptureMu.RLock()
	defer p.rawCaptureMu.RUnlock()
	return p.lastRawRequest
}

// LastRawHTTPResponse implements provider.HTTPDebugProvider.
// Returns the complete raw HTTP response that was received (zero truncation).
func (p *Provider) LastRawHTTPResponse() *provider.RawHTTPResponseCapture {
	p.rawCaptureMu.RLock()
	defer p.rawCaptureMu.RUnlock()
	return p.lastRawResponse
}

// storeRawRequest stores the raw HTTP request for debugging.
func (p *Provider) storeRawRequest(method, url string, headers map[string]string, body []byte) {
	p.rawCaptureMu.Lock()
	defer p.rawCaptureMu.Unlock()
	p.lastRawRequest = &provider.RawHTTPRequestCapture{
		Timestamp: time.Now(),
		Method:    method,
		URL:       url,
		Headers:   headers,
		Body:      append([]byte(nil), body...), // Deep copy
		BodySize:  len(body),
	}
}

// storeRawResponse stores the raw HTTP response for debugging.
func (p *Provider) storeRawResponse(statusCode int, headers map[string]string, body []byte) {
	p.rawCaptureMu.Lock()
	defer p.rawCaptureMu.Unlock()
	p.lastRawResponse = &provider.RawHTTPResponseCapture{
		Timestamp:  time.Now(),
		StatusCode: statusCode,
		Headers:    headers,
		Body:       append([]byte(nil), body...), // Deep copy
		BodySize:   len(body),
	}
}
