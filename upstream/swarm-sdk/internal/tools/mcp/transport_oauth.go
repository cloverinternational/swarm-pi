package mcp

import (
	"context"
	"fmt"
	"net/http"
	"sync"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/observability"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/tools/mcp/oauth"
)

// OAuthHTTPTransport wraps HTTPStreamTransport with OAuth authentication.
// It handles token management, automatic refresh, and 401 re-authentication.
type OAuthHTTPTransport struct {
	config     *TransportConfig
	oauthFlow  *oauth.Flow
	token      *oauth.TokenData
	httpClient *http.Client
	inner      *HTTPStreamTransport

	mu     sync.RWMutex
	logger observability.Logger
	tracer observability.Tracer
}

// NewOAuthHTTPTransport creates a new OAuth-authenticated HTTP transport.
func NewOAuthHTTPTransport(
	config *TransportConfig,
	logger observability.Logger,
	tracer observability.Tracer,
) (*OAuthHTTPTransport, error) {
	// Create OAuth flow
	var flowOpts []oauth.FlowOption
	if config != nil && config.OAuthConfig != nil && config.OAuthConfig.TokenStorage != nil {
		flowOpts = append(flowOpts, oauth.WithStorage(config.OAuthConfig.TokenStorage))
	}
	oauthFlow, err := oauth.NewFlow(flowOpts...)
	if err != nil {
		return nil, fmt.Errorf("failed to create OAuth flow: %w", err)
	}

	return &OAuthHTTPTransport{
		config:    config,
		oauthFlow: oauthFlow,
		httpClient: &http.Client{
			Timeout: config.Timeout,
		},
		logger: logger,
		tracer: tracer,
	}, nil
}

// Connect establishes connection with OAuth authentication.
func (t *OAuthHTTPTransport) Connect(ctx context.Context) error {
	ctx, span := t.tracer.StartSpan(ctx, "mcp.oauth_http.connect")
	defer span.End()

	// Get OAuth token
	token, err := t.authenticate(ctx)
	if err != nil {
		return fmt.Errorf("OAuth authentication failed: %w", err)
	}

	t.mu.Lock()
	t.token = token
	t.mu.Unlock()

	// Create inner transport with auth header
	innerConfig := *t.config
	if innerConfig.Headers == nil {
		innerConfig.Headers = make(map[string]string)
	}
	innerConfig.Headers["Authorization"] = "Bearer " + token.AccessToken

	t.inner = NewHTTPStreamTransport(&innerConfig, t.logger, t.tracer)

	// Connect inner transport
	if err := t.inner.Connect(ctx); err != nil {
		// Check if 401 - need to re-authenticate
		if isUnauthorizedError(err) {
			t.logger.Warn(ctx, "Connection returned 401, re-authenticating",
				observability.F("url", t.config.URL))

			// Force re-authentication
			token, err = t.forceReauthenticate(ctx)
			if err != nil {
				return fmt.Errorf("re-authentication failed: %w", err)
			}

			// Update token in inner config
			innerConfig.Headers["Authorization"] = "Bearer " + token.AccessToken
			t.inner = NewHTTPStreamTransport(&innerConfig, t.logger, t.tracer)

			// Retry connection
			if err := t.inner.Connect(ctx); err != nil {
				return err
			}
		} else {
			return err
		}
	}

	t.logger.Info(ctx, "OAuth HTTP transport connected",
		observability.F("url", t.config.URL))

	return nil
}

// authenticate gets or refreshes OAuth token.
func (t *OAuthHTTPTransport) authenticate(ctx context.Context) (*oauth.TokenData, error) {
	if t.config.OAuthConfig == nil {
		return nil, fmt.Errorf("OAuth configuration not provided")
	}

	return t.oauthFlow.Authenticate(ctx, &oauth.AuthenticateConfig{
		ServerURL:    t.config.URL,
		ClientID:     t.config.OAuthConfig.ClientID,
		ClientSecret: t.config.OAuthConfig.ClientSecret,
		Scopes:       t.config.OAuthConfig.Scopes,
		AuthURL:      t.config.OAuthConfig.AuthURL,
		TokenURL:     t.config.OAuthConfig.TokenURL,
	})
}

// forceReauthenticate clears cached token and runs full auth flow.
func (t *OAuthHTTPTransport) forceReauthenticate(ctx context.Context) (*oauth.TokenData, error) {
	// Clear cached token by running full auth flow
	// The oauth.Flow will handle this by detecting expired/invalid tokens

	t.mu.Lock()
	t.token = nil
	t.mu.Unlock()

	// Re-authenticate
	return t.authenticate(ctx)
}

// Send sends a message with OAuth token.
func (t *OAuthHTTPTransport) Send(ctx context.Context, message any) error {
	ctx, span := t.tracer.StartSpan(ctx, "mcp.oauth_http.send")
	defer span.End()

	t.mu.RLock()
	token := t.token
	inner := t.inner
	t.mu.RUnlock()

	if inner == nil {
		return fmt.Errorf("not connected")
	}

	// Check token validity before sending
	if token != nil && token.IsExpired() {
		t.logger.Debug(ctx, "Token expired, refreshing before send")
		newToken, err := t.authenticate(ctx)
		if err != nil {
			return fmt.Errorf("token refresh failed: %w", err)
		}

		// Update inner transport with new token
		if err := t.updateInnerToken(ctx, newToken); err != nil {
			return err
		}
	}

	// Send via inner transport
	err := inner.Send(ctx, message)
	if err != nil && isUnauthorizedError(err) {
		t.logger.Warn(ctx, "Send returned 401, re-authenticating")

		// Re-authenticate
		newToken, authErr := t.forceReauthenticate(ctx)
		if authErr != nil {
			return fmt.Errorf("re-authentication failed: %w", authErr)
		}

		// Update and retry
		if updateErr := t.updateInnerToken(ctx, newToken); updateErr != nil {
			return updateErr
		}

		return inner.Send(ctx, message)
	}

	return err
}

// updateInnerToken updates the Authorization header in the inner transport.
func (t *OAuthHTTPTransport) updateInnerToken(ctx context.Context, token *oauth.TokenData) error {
	t.mu.Lock()
	t.token = token
	t.mu.Unlock()

	// Recreate inner transport with new token
	innerConfig := *t.config
	if innerConfig.Headers == nil {
		innerConfig.Headers = make(map[string]string)
	}
	innerConfig.Headers["Authorization"] = "Bearer " + token.AccessToken

	newInner := NewHTTPStreamTransport(&innerConfig, t.logger, t.tracer)
	if err := newInner.Connect(ctx); err != nil {
		return err
	}

	t.mu.Lock()
	oldInner := t.inner
	t.inner = newInner
	t.mu.Unlock()

	// Close old inner transport
	if oldInner != nil {
		oldInner.Close()
	}

	return nil
}

// Receive receives a message.
func (t *OAuthHTTPTransport) Receive(ctx context.Context) (any, error) {
	t.mu.RLock()
	inner := t.inner
	t.mu.RUnlock()

	if inner == nil {
		return nil, fmt.Errorf("not connected")
	}

	return inner.Receive(ctx)
}

// Close closes the transport.
func (t *OAuthHTTPTransport) Close() error {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.inner != nil {
		return t.inner.Close()
	}
	return nil
}

// IsConnected checks connection status.
func (t *OAuthHTTPTransport) IsConnected() bool {
	t.mu.RLock()
	defer t.mu.RUnlock()

	if t.inner != nil {
		return t.inner.IsConnected()
	}
	return false
}

// TransportType returns the transport type.
func (t *OAuthHTTPTransport) TransportType() TransportType {
	return TransportOAuthHTTP
}

// GetToken returns the current OAuth token (for debugging/status).
func (t *OAuthHTTPTransport) Token() *oauth.TokenData {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.token
}

// isUnauthorizedError checks if error is a 401 Unauthorized.
func isUnauthorizedError(err error) bool {
	if err == nil {
		return false
	}
	errStr := err.Error()
	return contains(errStr, "401") || contains(errStr, "Unauthorized")
}

// contains is a simple string containment check.
func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsImpl(s, substr))
}

func containsImpl(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
