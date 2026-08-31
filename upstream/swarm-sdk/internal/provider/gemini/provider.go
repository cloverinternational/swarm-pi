package gemini

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"runtime"
	"sync"
	"time"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/observability"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/provider"
	"github.com/google/uuid"
)

// cliVersion is the spoofed Gemini CLI version for User-Agent headers.
const cliVersion = "1.0.6"

// Provider implements provider.Provider for Gemini with OAuth support.
type Provider struct {
	config     *Config
	httpClient *http.Client
	sseClient  *SSEClient
	oauth      *OAuthManager
	logger     observability.Logger
	tracer     observability.Tracer

	// Session management
	sessionID string
	sessionMu sync.RWMutex

	// User setup - populated by loadCodeAssist
	projectID string
	isSetup   bool
	setupMu   sync.RWMutex

	// For debugging
	lastRequest  map[string]any
	lastResponse map[string]any
	debugMu      sync.RWMutex
}

// New creates a new Gemini provider.
func New(config Config) (*Provider, error) {
	if err := config.Validate(); err != nil {
		return nil, fmt.Errorf("invalid config: %w", err)
	}

	// Create HTTP client
	timeout := time.Duration(config.Timeout) * time.Second
	var transport http.RoundTripper = http.DefaultTransport

	// Wrap with debug transport if RawDebugWriter is set
	if config.RawDebugWriter != nil {
		transport = provider.NewDebugTransport(transport, config.RawDebugWriter)
	}

	httpClient := &http.Client{
		Timeout:   timeout,
		Transport: transport,
	}

	// Create OAuth manager for OAuth auth mode
	var oauth *OAuthManager
	if config.AuthMode == AuthModeOAuth || config.AuthMode == "" {
		oauth = NewOAuthManager(&config)
	}

	// Generate session ID
	sessionID := uuid.New().String()

	return &Provider{
		config:     &config,
		httpClient: httpClient,
		sseClient:  NewSSEClient(httpClient),
		oauth:      oauth,
		logger:     config.Logger,
		tracer:     config.Tracer,
		sessionID:  sessionID,
	}, nil
}

// Name implements provider.Provider.
func (p *Provider) Name() string {
	return p.config.Name
}

// Chat implements provider.Provider.
func (p *Provider) Chat(ctx context.Context, req provider.ChatRequest) (*provider.ChatResponse, error) {
	// Get access token
	accessToken, err := p.getAccessToken(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get access token: %w", err)
	}

	// Ensure user is set up (calls loadCodeAssist if needed)
	if err := p.ensureSetup(ctx, accessToken); err != nil {
		return nil, fmt.Errorf("failed to setup user: %w", err)
	}

	// Determine model
	model := req.Model
	if model == "" {
		model = p.config.Model
	}
	req.Model = model

	// Translate request - use project ID from setup
	geminiReq := TranslateRequest(req, p.getProjectID(), p.getSessionID())

	// Marshal request
	body, err := json.Marshal(geminiReq)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	// Store for debugging
	p.storeDebugRequest(geminiReq)

	// DEBUG: Log request JSON
	if p.logger != nil {
		p.logger.Info(ctx, fmt.Sprintf("[Gemini] Request JSON: %s", string(body)))
	}

	// Build URL
	url := p.config.MethodURL("generateContent")

	// Create HTTP request
	httpReq, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	setGeminiHeaders(httpReq, accessToken, model)

	// Send request
	resp, err := p.httpClient.Do(httpReq)
	if err != nil {
		if rl := provider.RawLogFromCtx(ctx); rl != nil {
			rl.LogHTTPError("Gemini", "generateContent", 0, nil, err)
		}
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	// Read response
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		apiErr := ParseGeminiError(resp.StatusCode, respBody)
		if rl := provider.RawLogFromCtx(ctx); rl != nil {
			rl.LogHTTPError("Gemini", "generateContent", resp.StatusCode, respBody, apiErr)
		}
		return nil, apiErr
	}

	// Parse response
	var geminiResp GeminiResponse
	if err := json.Unmarshal(respBody, &geminiResp); err != nil {
		if rl := provider.RawLogFromCtx(ctx); rl != nil {
			rl.LogParseError("Gemini", respBody, err)
		}
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	// Store for debugging
	p.storeDebugResponse(&geminiResp)

	// DEBUG: Log response JSON
	if p.logger != nil {
		p.logger.Info(ctx, fmt.Sprintf("[Gemini] Response JSON: %s", string(respBody)))
	}

	// Translate response
	chatResp := TranslateResponse(&geminiResp)
	if chatResp == nil {
		return nil, fmt.Errorf("empty response from API")
	}

	return chatResp, nil
}

// Stream implements provider.Provider.
func (p *Provider) Stream(ctx context.Context, req provider.ChatRequest) (<-chan provider.StreamChunk, error) {
	// Get access token
	accessToken, err := p.getAccessToken(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get access token: %w", err)
	}

	// Ensure user is set up (calls loadCodeAssist if needed)
	if err := p.ensureSetup(ctx, accessToken); err != nil {
		return nil, fmt.Errorf("failed to setup user: %w", err)
	}

	// Determine model
	model := req.Model
	if model == "" {
		model = p.config.Model
	}
	req.Model = model

	// Translate request - use project ID from setup
	geminiReq := TranslateRequest(req, p.getProjectID(), p.getSessionID())

	// Marshal request
	body, err := json.Marshal(geminiReq)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	// Store for debugging
	p.storeDebugRequest(geminiReq)

	// DEBUG: Log request JSON
	if p.logger != nil {
		p.logger.Info(ctx, fmt.Sprintf("[Gemini] Request JSON: %s", string(body)))
	}

	// Build URL
	url := p.config.MethodURL("streamGenerateContent")

	// Use SSE client for streaming
	return p.sseClient.Stream(ctx, url, accessToken, body, model)
}

// Capabilities implements provider.Provider.
func (p *Provider) Capabilities() provider.Capabilities {
	return provider.Capabilities{
		Streaming:       true,
		FunctionCalling: true,
		Vision:          true,
		// Gemini 2.5/3 publish 1M-token windows; capped at the SDK's 300K
		// default policy (provider.DefaultContextWindowCap). User config
		// overrides this in the agent.
		MaxContextWindow:     provider.DefaultContextWindowCap,
		MaxOutputTokens:      65536, // 64K output tokens
		SupportsSystemPrompt: true,
		SupportsTemperature:  true,
		SupportedModels: []string{
			"gemini-3-pro-preview",
			"gemini-3-flash-preview",
			"gemini-2.5-pro",
			"gemini-2.5-flash",
			"gemini-2.5-flash-lite",
			"gemini-2.0-flash",
			"gemini-1.5-pro",
		},
		PromptCaching: true, // Gemini supports context caching via cachedContent
		SupportsJSON:  true,
	}
}

// LastProviderJSON implements provider.DebugProvider.
func (p *Provider) LastProviderJSON() json.RawMessage {
	p.debugMu.RLock()
	defer p.debugMu.RUnlock()
	result := map[string]any{}
	if p.lastRequest != nil {
		result["request"] = p.lastRequest
	}
	if p.lastResponse != nil {
		result["response"] = p.lastResponse
	}
	raw, _ := json.Marshal(result)
	return raw
}

// getAccessToken gets a valid access token based on auth mode.
func (p *Provider) getAccessToken(ctx context.Context) (string, error) {
	switch p.config.AuthMode {
	case AuthModeAPIKey:
		return p.config.APIKey, nil
	case AuthModeOAuth, "":
		if p.oauth == nil {
			return "", fmt.Errorf("OAuth manager not initialized")
		}
		return p.oauth.AccessToken(ctx)
	case AuthModeADC:
		// TODO: Implement ADC support
		return "", fmt.Errorf("ADC auth mode not yet implemented")
	default:
		return "", fmt.Errorf("unknown auth mode: %s", p.config.AuthMode)
	}
}

// getSessionID returns the current session ID.
func (p *Provider) getSessionID() string {
	p.sessionMu.RLock()
	defer p.sessionMu.RUnlock()
	return p.sessionID
}

// NewSession creates a new session ID.
func (p *Provider) NewSession() {
	p.sessionMu.Lock()
	defer p.sessionMu.Unlock()
	p.sessionID = uuid.New().String()
}

// ClearAuth clears cached OAuth tokens.
func (p *Provider) ClearAuth() error {
	if p.oauth != nil {
		return p.oauth.ClearTokens()
	}
	return nil
}

// ensureSetup ensures the user is set up with the Gemini API.
// This calls loadCodeAssist to get the project ID if not already done.
func (p *Provider) ensureSetup(ctx context.Context, accessToken string) error {
	p.setupMu.RLock()
	if p.isSetup {
		p.setupMu.RUnlock()
		return nil
	}
	p.setupMu.RUnlock()

	p.setupMu.Lock()
	defer p.setupMu.Unlock()

	// Double-check after acquiring write lock
	if p.isSetup {
		return nil
	}

	// Call loadCodeAssist to initialize user session
	projectID, err := p.loadCodeAssist(ctx, accessToken)
	if err != nil {
		return fmt.Errorf("failed to setup user: %w", err)
	}

	p.projectID = projectID
	p.isSetup = true
	return nil
}

// loadCodeAssistRequest represents the request to loadCodeAssist endpoint.
type loadCodeAssistRequest struct {
	CloudAICompanionProject string         `json:"cloudaicompanionProject,omitempty"`
	Metadata                map[string]any `json:"metadata"`
}

// loadCodeAssistResponse represents the response from loadCodeAssist endpoint.
type loadCodeAssistResponse struct {
	CurrentTier             *userTier `json:"currentTier,omitempty"`
	CloudAICompanionProject string    `json:"cloudaicompanionProject,omitempty"`
}

type userTier struct {
	ID   string `json:"id"`
	Name string `json:"name,omitempty"`
}

// loadCodeAssist calls the loadCodeAssist endpoint to initialize user session.
func (p *Provider) loadCodeAssist(ctx context.Context, accessToken string) (string, error) {
	// Check for environment variable project ID first
	projectID := p.config.ProjectID
	if projectID == "" {
		// Check environment variables as fallback
		for _, envVar := range []string{"GOOGLE_CLOUD_PROJECT", "GOOGLE_CLOUD_PROJECT_ID"} {
			if val := getenv(envVar); val != "" {
				projectID = val
				break
			}
		}
	}

	req := loadCodeAssistRequest{
		CloudAICompanionProject: projectID,
		Metadata: map[string]any{
			"ideType":     "GEMINI_CLI",
			"ideName":     "IDE_UNSPECIFIED",
			"pluginType":  "GEMINI",
			"ideVersion":  cliVersion,
			"platform":    geminiPlatform(),
			"duetProject": projectID,
		},
	}

	body, err := json.Marshal(req)
	if err != nil {
		return "", fmt.Errorf("failed to marshal request: %w", err)
	}

	url := p.config.MethodURL("loadCodeAssist")
	httpReq, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("failed to create request: %w", err)
	}

	setGeminiHeaders(httpReq, accessToken, p.config.Model)

	resp, err := p.httpClient.Do(httpReq)
	if err != nil {
		return "", fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return "", ParseGeminiError(resp.StatusCode, respBody)
	}

	var loadResp loadCodeAssistResponse
	if err := json.Unmarshal(respBody, &loadResp); err != nil {
		return "", fmt.Errorf("failed to parse response: %w", err)
	}

	// Use returned project ID if available, otherwise use configured one
	if loadResp.CloudAICompanionProject != "" {
		return loadResp.CloudAICompanionProject, nil
	}
	return projectID, nil
}

// getenv is a helper to get environment variables (allows for testing)
var getenv = func(key string) string {
	return os.Getenv(key)
}

// getProjectID returns the project ID, ensuring setup is done first
func (p *Provider) getProjectID() string {
	p.setupMu.RLock()
	defer p.setupMu.RUnlock()
	return p.projectID
}

// storeDebugRequest stores the request for debugging.
func (p *Provider) storeDebugRequest(req *GeminiRequest) {
	p.debugMu.Lock()
	defer p.debugMu.Unlock()

	// Convert to map for storage
	data, _ := json.Marshal(req)
	var m map[string]any
	json.Unmarshal(data, &m)
	p.lastRequest = m
}

// storeDebugResponse stores the response for debugging.
func (p *Provider) storeDebugResponse(resp *GeminiResponse) {
	p.debugMu.Lock()
	defer p.debugMu.Unlock()

	// Convert to map for storage
	data, _ := json.Marshal(resp)
	var m map[string]any
	json.Unmarshal(data, &m)
	p.lastResponse = m
}

// geminiUserAgent returns a User-Agent string that matches the official Gemini CLI format:
// GeminiCLI/{version}/{model} ({platform}; {arch})
func geminiUserAgent(model string) string {
	platform := runtime.GOOS
	arch := runtime.GOARCH
	// Match Node.js platform names used by official CLI
	switch platform {
	case "darwin":
		// keep as-is
	case "windows":
		platform = "win32"
	}
	return fmt.Sprintf("GeminiCLI/%s/%s (%s; %s)", cliVersion, model, platform, arch)
}

// geminiPlatform returns the ClientMetadata platform string matching official Gemini CLI.
func geminiPlatform() string {
	switch runtime.GOOS {
	case "darwin":
		if runtime.GOARCH == "arm64" {
			return "DARWIN_ARM64"
		}
		return "DARWIN_AMD64"
	case "linux":
		if runtime.GOARCH == "arm64" {
			return "LINUX_ARM64"
		}
		return "LINUX_AMD64"
	case "windows":
		return "WINDOWS_AMD64"
	default:
		return "PLATFORM_UNSPECIFIED"
	}
}

// setGeminiHeaders sets HTTP headers to match the official Gemini CLI.
func setGeminiHeaders(req *http.Request, accessToken, model string) {
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("User-Agent", geminiUserAgent(model))
}

// Ensure Provider implements the interfaces
var _ provider.Provider = (*Provider)(nil)
var _ provider.DebugProvider = (*Provider)(nil)
