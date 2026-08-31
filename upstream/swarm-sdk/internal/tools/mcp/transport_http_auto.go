package mcp

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/observability"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/tools/mcp/oauth"
)

// AutoHTTPTransport automatically detects OAuth requirements and handles authentication.
// It tries plain HTTP first, and if it receives a 401, it initiates OAuth auto-discovery.
type AutoHTTPTransport struct {
	config          *TransportConfig
	logger          observability.Logger
	tracer          observability.Tracer
	inner           Transport
	oauthFlow       *oauth.Flow
	discoveryClient *oauth.DiscoveryClient
}

// NewAutoHTTPTransport creates an HTTP transport with automatic OAuth detection.
func NewAutoHTTPTransport(config *TransportConfig, logger observability.Logger, tracer observability.Tracer) *AutoHTTPTransport {
	var flowOpts []oauth.FlowOption
	if config.OAuthConfig != nil && config.OAuthConfig.TokenStorage != nil {
		flowOpts = append(flowOpts, oauth.WithStorage(config.OAuthConfig.TokenStorage))
	}

	oauthFlow, _ := oauth.NewFlow(flowOpts...)

	return &AutoHTTPTransport{
		config:          config,
		logger:          logger,
		tracer:          tracer,
		oauthFlow:       oauthFlow,
		discoveryClient: oauth.NewDiscoveryClient(),
	}
}

// Connect attempts to connect, handling OAuth if required.
func (t *AutoHTTPTransport) Connect(ctx context.Context) error {
	ctx, span := t.tracer.StartSpan(ctx, "mcp.auto_http.connect")
	defer span.End()

	t.logger.Error(ctx, "=== AUTO HTTP TRANSPORT STARTING ===")
	t.logger.Error(ctx, "Auto HTTP transport connecting",
		observability.F("url", t.config.URL))

	// Step 1: Try plain HTTP connection first (with short timeout)
	t.logger.Error(ctx, "Step 1: Trying plain HTTP connection")
	plainTransport := NewHTTPStreamTransport(t.config, t.logger, t.tracer)

	// Use a short timeout for the initial probe
	probeCtx, cancelProbe := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancelProbe()

	t.logger.Info(ctx, "Calling plainTransport.Connect...")
	err := plainTransport.Connect(probeCtx)

	if err == nil {
		// Success! No OAuth needed
		t.inner = plainTransport
		t.logger.Info(ctx, "Connected without OAuth - SUCCESS")
		return nil
	}

	t.logger.Info(ctx, "Plain HTTP failed",
		observability.F("error", err.Error()))

	// Step 2: Check if error indicates OAuth is needed
	errStr := err.Error()
	if !strings.Contains(errStr, "401") && !strings.Contains(errStr, "Unauthorized") {
		// Some other error, not OAuth-related
		t.logger.Info(ctx, "Error is not 401/OAuth-related, giving up")
		return err
	}

	t.logger.Info(ctx, "=== OAUTH FLOW STARTING ===")
	t.logger.Info(ctx, "Received 401, attempting OAuth auto-discovery")

	// Step 3: Try to extract WWW-Authenticate header from the error
	// We need to make a test request to get the header
	challengeCtx, cancelChallenge := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancelChallenge()

	wwwAuth, resourceURL, err := t.getAuthChallenge(challengeCtx)
	if err != nil {
		return fmt.Errorf("failed to get auth challenge: %w", err)
	}

	t.logger.Info(ctx, "Got OAuth challenge",
		observability.F("resource_metadata", resourceURL))

	// Step 4: Discover OAuth configuration (use fresh context)
	discoveryCtx, cancelDiscovery := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancelDiscovery()

	var resourceMeta *oauth.ProtectedResourceMetadata
	if resourceURL != "" {
		// Parse WWW-Authenticate header
		resourceMeta, err = t.discoveryClient.DiscoverFromWWWAuthenticate(discoveryCtx, wwwAuth)
		if err != nil {
			return fmt.Errorf("failed to discover from WWW-Authenticate: %w", err)
		}
	} else {
		// Fallback: try well-known URL
		resourceMeta, err = t.discoveryClient.DiscoverProtectedResource(discoveryCtx, t.config.URL)
		if err != nil {
			return fmt.Errorf("failed to discover protected resource: %w", err)
		}
	}

	if len(resourceMeta.AuthorizationServers) == 0 {
		return fmt.Errorf("no authorization servers found")
	}

	// Step 5: Get authorization server metadata
	authServer := resourceMeta.AuthorizationServers[0]
	serverMeta, err := t.discoveryClient.DiscoverAuthorizationServer(discoveryCtx, authServer)
	if err != nil {
		return fmt.Errorf("failed to discover authorization server: %w", err)
	}

	t.logger.Info(ctx, "Discovered OAuth endpoints",
		observability.F("auth_endpoint", serverMeta.AuthorizationEndpoint),
		observability.F("token_endpoint", serverMeta.TokenEndpoint))

	// Step 6: Determine client ID
	// For MCP, we use Client ID Metadata Documents if supported
	// Otherwise, we need a pre-configured client ID
	clientID := t.getClientID(serverMeta)
	if clientID == "" {
		return fmt.Errorf("no client ID available - please configure OAuth clientID for this server")
	}

	t.logger.Info(ctx, "Starting OAuth authorization flow",
		observability.F("client_id", clientID))

	// Step 7: Determine scopes
	scopes := t.getScopes(resourceMeta, wwwAuth)

	t.logger.Info(ctx, "Requesting OAuth scopes",
		observability.F("scopes", scopes))

	t.logger.Info(ctx, "*** OPENING BROWSER FOR AUTHORIZATION ***")
	t.logger.Info(ctx, "Please complete the authorization in your browser")
	t.logger.Info(ctx, "Waiting for OAuth callback (this may take a few minutes)...")

	// Step 8: Run OAuth flow with extended timeout
	// Create a new context with a longer timeout for OAuth
	authCtx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	token, err := t.oauthFlow.Authenticate(authCtx, &oauth.AuthenticateConfig{
		ServerURL:    t.config.URL,
		ClientID:     clientID,
		ClientSecret: "", // Public client
		Scopes:       scopes,
		AuthURL:      serverMeta.AuthorizationEndpoint,
		TokenURL:     serverMeta.TokenEndpoint,
	})
	if err != nil {
		return fmt.Errorf("OAuth authentication failed: %w", err)
	}

	t.logger.Info(ctx, "OAuth authentication successful")

	// Step 9: Create authenticated HTTP transport
	authedConfig := *t.config
	if authedConfig.Headers == nil {
		authedConfig.Headers = make(map[string]string)
	}
	authedConfig.Headers["Authorization"] = "Bearer " + token.AccessToken

	authedTransport := NewHTTPStreamTransport(&authedConfig, t.logger, t.tracer)

	// Use a fresh context for the authenticated connection attempt
	connectCtx, cancelConnect := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancelConnect()

	if err := authedTransport.Connect(connectCtx); err != nil {
		return fmt.Errorf("failed to connect with OAuth token: %w", err)
	}

	t.inner = authedTransport
	return nil
}

// getAuthChallenge makes a test request to get the WWW-Authenticate header.
func (t *AutoHTTPTransport) getAuthChallenge(ctx context.Context) (string, string, error) {
	req, err := http.NewRequestWithContext(ctx, "POST", t.config.URL, nil)
	if err != nil {
		return "", "", err
	}
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: t.config.Timeout}
	resp, err := client.Do(req)
	if err != nil {
		return "", "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != 401 {
		return "", "", fmt.Errorf("expected 401, got %d", resp.StatusCode)
	}

	wwwAuth := resp.Header.Get("WWW-Authenticate")
	if wwwAuth == "" {
		return "", "", fmt.Errorf("no WWW-Authenticate header in 401 response")
	}

	// Extract resource_metadata URL if present
	resourceURL := extractResourceMetadataURL(wwwAuth)

	return wwwAuth, resourceURL, nil
}

// extractResourceMetadataURL extracts the resource_metadata URL from WWW-Authenticate header.
func extractResourceMetadataURL(header string) string {
	for param := range strings.SplitSeq(header, ",") {
		param = strings.TrimSpace(param)
		if after, ok := strings.CutPrefix(param, "resource_metadata="); ok {
			value := after
			return strings.Trim(value, "\"")
		}
	}
	return ""
}

// extractScopeFromChallenge extracts scopes from WWW-Authenticate header.
func extractScopeFromChallenge(header string) []string {
	for param := range strings.SplitSeq(header, ",") {
		param = strings.TrimSpace(param)
		if after, ok := strings.CutPrefix(param, "scope="); ok {
			value := after
			value = strings.Trim(value, "\"")
			return strings.Fields(value)
		}
	}
	return nil
}

// getClientID determines the client ID to use.
func (t *AutoHTTPTransport) getClientID(serverMeta *oauth.ServerMetadata) string {
	// If configured, use that
	if t.config.OAuthConfig != nil && t.config.OAuthConfig.ClientID != "" {
		return t.config.OAuthConfig.ClientID
	}

	// For MCP spec compliance, we should use Client ID Metadata Documents
	// This would be a hosted URL like: https://app.example.com/mcp/client-metadata.json
	// For now, return empty to indicate configuration is needed
	return ""
}

// getScopes determines which scopes to request.
func (t *AutoHTTPTransport) getScopes(resourceMeta *oauth.ProtectedResourceMetadata, wwwAuth string) []string {
	// Priority 1: Scopes from WWW-Authenticate challenge
	challengeScopes := extractScopeFromChallenge(wwwAuth)
	if len(challengeScopes) > 0 {
		return challengeScopes
	}

	// Priority 2: Scopes from protected resource metadata
	if len(resourceMeta.ScopesSupported) > 0 {
		return resourceMeta.ScopesSupported
	}

	// Priority 3: Configured scopes
	if t.config.OAuthConfig != nil && len(t.config.OAuthConfig.Scopes) > 0 {
		return t.config.OAuthConfig.Scopes
	}

	// Default: request all available scopes
	return nil
}

// Send delegates to inner transport.
func (t *AutoHTTPTransport) Send(ctx context.Context, message any) error {
	if t.inner == nil {
		return fmt.Errorf("not connected")
	}
	return t.inner.Send(ctx, message)
}

// Receive delegates to inner transport.
func (t *AutoHTTPTransport) Receive(ctx context.Context) (any, error) {
	if t.inner == nil {
		return nil, fmt.Errorf("not connected")
	}
	return t.inner.Receive(ctx)
}

// Close delegates to inner transport.
func (t *AutoHTTPTransport) Close() error {
	if t.inner != nil {
		return t.inner.Close()
	}
	return nil
}

// IsConnected checks connection status.
func (t *AutoHTTPTransport) IsConnected() bool {
	if t.inner != nil {
		return t.inner.IsConnected()
	}
	return false
}

// TransportType returns the transport type.
func (t *AutoHTTPTransport) TransportType() TransportType {
	return TransportHTTPStream
}
