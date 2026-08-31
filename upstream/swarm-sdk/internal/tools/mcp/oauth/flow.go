package oauth

import (
	"context"
	"fmt"
	"net/url"
	"strings"

	"github.com/Swarm-Code/mono/swarm-core/osutil"
	"time"
)

// Flow orchestrates the OAuth authentication flow for MCP servers.
type Flow struct {
	storage       TokenStorage
	discovery     *DiscoveryClient
	tokenClient   *TokenClient
	browserOpener func(url string) error
	callbackPort  int // 0 = auto-select for per-flow callback
	authTimeout   time.Duration

	// Singleton callback server options
	useSingletonCallback  bool
	singletonCallbackPort int
}

// FlowOption configures the OAuth flow.
type FlowOption func(*Flow)

// WithStorage sets the token storage implementation.
func WithStorage(storage TokenStorage) FlowOption {
	return func(f *Flow) {
		f.storage = storage
	}
}

// WithBrowserOpener sets a custom browser opener function.
func WithBrowserOpener(opener func(url string) error) FlowOption {
	return func(f *Flow) {
		f.browserOpener = opener
	}
}

// WithCallbackPort sets a specific port for the callback server.
func WithCallbackPort(port int) FlowOption {
	return func(f *Flow) {
		f.callbackPort = port
	}
}

// WithAuthTimeout sets the timeout for waiting for user authorization.
func WithAuthTimeout(timeout time.Duration) FlowOption {
	return func(f *Flow) {
		f.authTimeout = timeout
	}
}

// WithSingletonCallback configures the flow to use a shared singleton callback server.
// This is useful for simpler deployments where port conflicts aren't a concern.
// The singleton server uses state-based routing to handle multiple concurrent flows.
// If port is 0, uses DefaultCallbackPort (19876, matching OpenCode).
func WithSingletonCallback(port int) FlowOption {
	return func(f *Flow) {
		f.useSingletonCallback = true
		f.singletonCallbackPort = port
	}
}

// NewFlow creates a new OAuth flow coordinator.
func NewFlow(opts ...FlowOption) (*Flow, error) {
	f := &Flow{
		discovery:     NewDiscoveryClient(),
		tokenClient:   NewTokenClient(),
		browserOpener: openBrowser,
		authTimeout:   5 * time.Minute,
	}

	for _, opt := range opts {
		opt(f)
	}

	// Default to file storage if not set
	if f.storage == nil {
		storage, err := NewFileTokenStorage("")
		if err != nil {
			return nil, fmt.Errorf("failed to create token storage: %w", err)
		}
		f.storage = storage
	}

	return f, nil
}

// AuthenticateConfig holds configuration for authentication.
type AuthenticateConfig struct {
	ServerURL    string   // MCP server URL
	ClientID     string   // OAuth client ID
	ClientSecret string   // OAuth client secret (optional for public clients)
	Scopes       []string // Requested scopes

	// Optional manual overrides (discovered automatically if not set)
	AuthURL  string
	TokenURL string
}

// Authenticate gets a valid token for the MCP server.
// It checks for cached tokens, refreshes if needed, or starts a new auth flow.
func (f *Flow) Authenticate(ctx context.Context, config *AuthenticateConfig) (*TokenData, error) {
	// Step 1: Check for existing valid token WITH URL validation
	token, err := f.storage.LoadTokenForURL(config.ServerURL)
	if err != nil {
		// URL mismatch or other validation error
		// Log and continue with new auth flow (don't fail hard)
		// This allows graceful handling when server URLs change
		token = nil
	}

	if token != nil && token.IsValid() {
		return token, nil
	}

	// Step 2: Try to refresh if we have a refresh token
	if token != nil && token.CanRefresh() {
		refreshed, err := f.refreshToken(ctx, config, token)
		if err == nil {
			return refreshed, nil
		}
		// Refresh failed, fall through to full auth flow
	}

	// Step 3: Start full authorization flow
	return f.startAuthorizationFlow(ctx, config)
}

// refreshToken attempts to refresh an expired token.
func (f *Flow) refreshToken(ctx context.Context, config *AuthenticateConfig, token *TokenData) (*TokenData, error) {
	// Get token endpoint
	tokenURL := config.TokenURL
	if tokenURL == "" {
		// Discover it
		serverMeta, err := f.discovery.Discover(ctx, config.ServerURL)
		if err != nil {
			return nil, fmt.Errorf("failed to discover endpoints: %w", err)
		}
		tokenURL = serverMeta.TokenEndpoint
	}

	// Refresh the token
	newToken, err := f.tokenClient.RefreshToken(ctx, &RefreshConfig{
		TokenURL:     tokenURL,
		ClientID:     config.ClientID,
		ClientSecret: config.ClientSecret,
		RefreshToken: token.RefreshToken,
	})
	if err != nil {
		return nil, err
	}

	// Save the refreshed token
	if err := f.storage.SaveToken(config.ServerURL, newToken); err != nil {
		return nil, fmt.Errorf("failed to save refreshed token: %w", err)
	}

	return newToken, nil
}

// startAuthorizationFlow runs the full OAuth authorization code flow.
func (f *Flow) startAuthorizationFlow(ctx context.Context, config *AuthenticateConfig) (*TokenData, error) {
	// Step 1: Discover OAuth endpoints if not provided
	var authURL, tokenURL string
	if config.AuthURL != "" && config.TokenURL != "" {
		authURL = config.AuthURL
		tokenURL = config.TokenURL
	} else {
		serverMeta, err := f.discovery.Discover(ctx, config.ServerURL)
		if err != nil {
			return nil, fmt.Errorf("failed to discover OAuth endpoints: %w", err)
		}
		authURL = serverMeta.AuthorizationEndpoint
		tokenURL = serverMeta.TokenEndpoint
	}

	// Step 2: Generate PKCE and state
	flowState, err := GenerateFlowState()
	if err != nil {
		return nil, fmt.Errorf("failed to generate flow state: %w", err)
	}

	// Step 3: Start callback server (singleton or per-flow)
	var redirectURI string
	var waitForCallback func(context.Context) (*CallbackResult, error)
	var cleanup func()

	if f.useSingletonCallback {
		// Use singleton callback server (shared across flows)
		singleton, err := GetSingletonCallbackServer(f.singletonCallbackPort)
		if err != nil {
			return nil, fmt.Errorf("failed to get singleton callback server: %w", err)
		}

		redirectURI = singleton.RedirectURI()
		resultChan := singleton.RegisterPendingAuth(flowState.State)

		waitForCallback = func(ctx context.Context) (*CallbackResult, error) {
			select {
			case result := <-resultChan:
				if result == nil {
					return nil, fmt.Errorf("callback channel closed")
				}
				if result.Error != "" {
					return nil, fmt.Errorf("authorization failed: %s", result.Error)
				}
				return result, nil
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}

		cleanup = func() {
			singleton.UnregisterPendingAuth(flowState.State)
		}
	} else {
		// Use per-flow callback server (default)
		callbackServer, err := NewCallbackServer(f.callbackPort)
		if err != nil {
			return nil, fmt.Errorf("failed to start callback server: %w", err)
		}

		if err := callbackServer.Start(); err != nil {
			return nil, fmt.Errorf("failed to start callback server: %w", err)
		}

		redirectURI = callbackServer.RedirectURI()

		waitForCallback = func(ctx context.Context) (*CallbackResult, error) {
			return callbackServer.WaitForCallback(ctx)
		}

		cleanup = func() {
			callbackServer.Stop()
		}
	}

	defer cleanup()

	// Step 4: Build authorization URL
	authParams := url.Values{
		"client_id":             {config.ClientID},
		"redirect_uri":          {redirectURI},
		"response_type":         {"code"},
		"code_challenge":        {flowState.CodeChallenge},
		"code_challenge_method": {"S256"},
		"state":                 {flowState.State},
	}

	if len(config.Scopes) > 0 {
		authParams.Set("scope", strings.Join(config.Scopes, " "))
	}

	authorizationURL := authURL + "?" + authParams.Encode()

	// Step 5: Open browser for user authorization
	if err := f.browserOpener(authorizationURL); err != nil {
		return nil, fmt.Errorf("failed to open browser: %w\nPlease open this URL manually: %s", err, authorizationURL)
	}

	// Step 6: Wait for callback
	waitCtx, cancel := context.WithTimeout(ctx, f.authTimeout)
	defer cancel()

	callbackResult, err := waitForCallback(waitCtx)
	if err != nil {
		return nil, fmt.Errorf("authorization failed: %w", err)
	}

	// Step 7: Verify state (CSRF protection)
	if callbackResult.State != flowState.State {
		return nil, fmt.Errorf("state mismatch: possible CSRF attack")
	}

	// Step 8: Exchange code for tokens
	token, err := f.tokenClient.ExchangeCode(ctx, &ExchangeCodeConfig{
		TokenURL:     tokenURL,
		ClientID:     config.ClientID,
		ClientSecret: config.ClientSecret,
		Code:         callbackResult.Code,
		CodeVerifier: flowState.CodeVerifier,
		RedirectURI:  redirectURI,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to exchange code for token: %w", err)
	}

	// Step 9: Save token
	if err := f.storage.SaveToken(config.ServerURL, token); err != nil {
		return nil, fmt.Errorf("failed to save token: %w", err)
	}

	return token, nil
}

// Logout revokes tokens and removes them from storage.
func (f *Flow) Logout(ctx context.Context, serverURL string, config *AuthenticateConfig) error {
	token, err := f.storage.LoadToken(serverURL)
	if err != nil {
		return err
	}

	if token == nil {
		return nil // Already logged out
	}

	// Try to discover revocation endpoint
	serverMeta, err := f.discovery.Discover(ctx, serverURL)
	if err == nil && serverMeta.RevocationEndpoint != "" {
		// Revoke refresh token first (if exists)
		if token.RefreshToken != "" {
			_ = f.tokenClient.RevokeToken(ctx, &RevokeConfig{
				RevocationURL: serverMeta.RevocationEndpoint,
				ClientID:      config.ClientID,
				ClientSecret:  config.ClientSecret,
				Token:         token.RefreshToken,
				TokenTypeHint: "refresh_token",
			})
		}

		// Revoke access token
		_ = f.tokenClient.RevokeToken(ctx, &RevokeConfig{
			RevocationURL: serverMeta.RevocationEndpoint,
			ClientID:      config.ClientID,
			ClientSecret:  config.ClientSecret,
			Token:         token.AccessToken,
			TokenTypeHint: "access_token",
		})
	}

	// Remove from storage
	return f.storage.DeleteToken(serverURL)
}

// GetToken returns the current token for a server (without authenticating).
func (f *Flow) Token(serverURL string) (*TokenData, error) {
	return f.storage.LoadToken(serverURL)
}

// GetClientInfo retrieves client info from storage or config.
// Priority: 1) Stored client info, 2) Configured client ID/secret
func (f *Flow) ClientInfo(ctx context.Context, config *AuthenticateConfig) (*ClientInfo, error) {
	// Priority 1: Check storage for saved client info
	clientInfo, err := f.storage.LoadClientInfo(config.ServerURL)
	if err != nil {
		// Expired or error - fall through to config
	} else if clientInfo != nil && clientInfo.IsValid() {
		return clientInfo, nil
	}

	// Priority 2: Use configured client ID/secret
	if config.ClientID != "" {
		return &ClientInfo{
			ClientID:     config.ClientID,
			ClientSecret: config.ClientSecret,
		}, nil
	}

	return nil, fmt.Errorf("no client credentials available for %s", config.ServerURL)
}

// SaveClientInfo saves client registration information.
// This is useful for dynamic client registration scenarios.
func (f *Flow) SaveClientInfo(serverURL string, clientInfo *ClientInfo) error {
	return f.storage.SaveClientInfo(serverURL, clientInfo)
}

// openBrowser opens a URL in the default browser.
func openBrowser(url string) error {
	return osutil.OpenURL(url)
}
