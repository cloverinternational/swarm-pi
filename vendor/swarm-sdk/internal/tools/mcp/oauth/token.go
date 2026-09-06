package oauth

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// TokenClient handles OAuth token operations.
type TokenClient struct {
	httpClient *http.Client
}

// NewTokenClient creates a new token client.
func NewTokenClient() *TokenClient {
	return &TokenClient{
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

// ExchangeCodeConfig holds configuration for exchanging an authorization code.
type ExchangeCodeConfig struct {
	TokenURL     string
	ClientID     string
	ClientSecret string // Optional for public clients
	Code         string
	CodeVerifier string
	RedirectURI  string
}

// ExchangeCode exchanges an authorization code for tokens.
func (c *TokenClient) ExchangeCode(ctx context.Context, config *ExchangeCodeConfig) (*TokenData, error) {
	// Build form data
	data := url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {config.Code},
		"redirect_uri":  {config.RedirectURI},
		"client_id":     {config.ClientID},
		"code_verifier": {config.CodeVerifier},
	}

	req, err := http.NewRequestWithContext(ctx, "POST", config.TokenURL, strings.NewReader(data.Encode()))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")

	// Add client authentication if secret is provided
	if config.ClientSecret != "" {
		req.SetBasicAuth(config.ClientID, config.ClientSecret)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("token request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		// Try to parse error response
		var errResp TokenErrorResponse
		if json.Unmarshal(body, &errResp) == nil && errResp.ErrorCode != "" {
			return nil, fmt.Errorf("token error: %s - %s", errResp.ErrorCode, errResp.ErrorDescription)
		}
		return nil, fmt.Errorf("token request failed with status %d: %s", resp.StatusCode, string(body))
	}

	var tokenResp TokenResponse
	if err := json.Unmarshal(body, &tokenResp); err != nil {
		return nil, fmt.Errorf("failed to parse token response: %w", err)
	}

	// Convert to TokenData with computed expiry
	token := &TokenData{
		AccessToken:  tokenResp.AccessToken,
		RefreshToken: tokenResp.RefreshToken,
		TokenType:    tokenResp.TokenType,
		ExpiresIn:    tokenResp.ExpiresIn,
		Scope:        tokenResp.Scope,
	}

	// Calculate absolute expiry time
	if tokenResp.ExpiresIn > 0 {
		token.ExpiresAt = time.Now().Add(time.Duration(tokenResp.ExpiresIn) * time.Second)
	}

	return token, nil
}

// RefreshConfig holds configuration for refreshing a token.
type RefreshConfig struct {
	TokenURL     string
	ClientID     string
	ClientSecret string // Optional for public clients
	RefreshToken string
	Scope        string // Optional, to request a subset of original scopes
}

// RefreshToken exchanges a refresh token for new tokens.
func (c *TokenClient) RefreshToken(ctx context.Context, config *RefreshConfig) (*TokenData, error) {
	// Build form data
	data := url.Values{
		"grant_type":    {"refresh_token"},
		"refresh_token": {config.RefreshToken},
		"client_id":     {config.ClientID},
	}

	if config.Scope != "" {
		data.Set("scope", config.Scope)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", config.TokenURL, strings.NewReader(data.Encode()))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")

	// Add client authentication if secret is provided
	if config.ClientSecret != "" {
		req.SetBasicAuth(config.ClientID, config.ClientSecret)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("refresh request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		// Try to parse error response
		var errResp TokenErrorResponse
		if json.Unmarshal(body, &errResp) == nil && errResp.ErrorCode != "" {
			return nil, fmt.Errorf("refresh error: %s - %s", errResp.ErrorCode, errResp.ErrorDescription)
		}
		return nil, fmt.Errorf("refresh request failed with status %d: %s", resp.StatusCode, string(body))
	}

	var tokenResp TokenResponse
	if err := json.Unmarshal(body, &tokenResp); err != nil {
		return nil, fmt.Errorf("failed to parse token response: %w", err)
	}

	// Convert to TokenData
	token := &TokenData{
		AccessToken:  tokenResp.AccessToken,
		RefreshToken: tokenResp.RefreshToken,
		TokenType:    tokenResp.TokenType,
		ExpiresIn:    tokenResp.ExpiresIn,
		Scope:        tokenResp.Scope,
	}

	// If no new refresh token was provided, keep the old one
	if token.RefreshToken == "" {
		token.RefreshToken = config.RefreshToken
	}

	// Calculate absolute expiry time
	if tokenResp.ExpiresIn > 0 {
		token.ExpiresAt = time.Now().Add(time.Duration(tokenResp.ExpiresIn) * time.Second)
	}

	return token, nil
}

// RevokeConfig holds configuration for revoking a token.
type RevokeConfig struct {
	RevocationURL string
	ClientID      string
	ClientSecret  string // Optional for public clients
	Token         string
	TokenTypeHint string // "access_token" or "refresh_token"
}

// RevokeToken revokes a token at the authorization server.
func (c *TokenClient) RevokeToken(ctx context.Context, config *RevokeConfig) error {
	if config.RevocationURL == "" {
		return fmt.Errorf("revocation endpoint not available")
	}

	// Build form data
	data := url.Values{
		"token":     {config.Token},
		"client_id": {config.ClientID},
	}

	if config.TokenTypeHint != "" {
		data.Set("token_type_hint", config.TokenTypeHint)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", config.RevocationURL, strings.NewReader(data.Encode()))
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	// Add client authentication if secret is provided
	if config.ClientSecret != "" {
		req.SetBasicAuth(config.ClientID, config.ClientSecret)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("revocation request failed: %w", err)
	}
	defer resp.Body.Close()

	// Per RFC 7009, successful revocation returns 200 OK
	// The server may return 200 even if the token was invalid
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("revocation failed with status %d: %s", resp.StatusCode, string(body))
	}

	return nil
}
