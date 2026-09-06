package anthropic

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// OAuth configuration constants
const (
	OAuthClientID     = "9d1c250a-e61b-44d9-88ed-5944d1962f5e"
	OAuthAuthorizeURL = "https://claude.ai/oauth/authorize"
	// OAuthTokenURL is the live token/refresh endpoint used by the official
	// Claude Code CLI. The old console.anthropic.com/v1/oauth/token endpoint is
	// dead and silently fails refreshes; platform.claude.com/v1/oauth/token is
	// the current endpoint. KEEP the authorize URL behavior as-is — the device/
	// PKCE add-account flow against claude.ai still works.
	OAuthTokenURL    = "https://platform.claude.com/v1/oauth/token"
	OAuthRedirectURI = "https://console.anthropic.com/oauth/code/callback"
)

// OAuth scopes required for Claude Code
var OAuthScopes = []string{
	"org:create_api_key",
	"user:profile",
	"user:inference",
}

// DeviceCodeResponse represents the device code response
type DeviceCodeResponse struct {
	DeviceCode              string `json:"device_code"`
	UserCode                string `json:"user_code"`
	VerificationURI         string `json:"verification_uri"`
	VerificationURIComplete string `json:"verification_uri_complete"`
	ExpiresIn               int    `json:"expires_in"`
	Interval                int    `json:"interval"`
}

// OAuthToken represents an OAuth access token
type OAuthToken struct {
	AccessToken  string `json:"access_token"`
	TokenType    string `json:"token_type"`
	ExpiresIn    int    `json:"expires_in"`
	RefreshToken string `json:"refresh_token,omitempty"`
	Scope        string `json:"scope,omitempty"`
	Expiry       int64  `json:"expiry"`
}

// DeviceFlow handles OAuth 2.0 Device Flow with PKCE
type DeviceFlow struct {
	codeVerifier  string
	codeChallenge string
	state         string
}

// NewDeviceFlow creates a new device flow handler
func NewDeviceFlow() *DeviceFlow {
	return &DeviceFlow{}
}

// generatePKCE generates PKCE code verifier and challenge using S256 method
func (d *DeviceFlow) generatePKCE() error {
	// Generate random code verifier (43-128 characters, base64url encoded)
	verifierBytes := make([]byte, 32)
	if _, err := rand.Read(verifierBytes); err != nil {
		return fmt.Errorf("failed to generate random verifier: %w", err)
	}

	verifier := base64.RawURLEncoding.EncodeToString(verifierBytes)
	d.codeVerifier = verifier

	// Generate code challenge using S256 (SHA256 hash)
	hash := sha256.Sum256([]byte(verifier))
	challenge := base64.RawURLEncoding.EncodeToString(hash[:])
	d.codeChallenge = challenge

	// Use verifier as state for CSRF protection
	d.state = verifier

	return nil
}

// InitiateDeviceFlow starts the OAuth flow and returns device code response
func (d *DeviceFlow) InitiateDeviceFlow() (*DeviceCodeResponse, error) {
	if err := d.generatePKCE(); err != nil {
		return nil, err
	}

	// Build authorization URL
	params := map[string]string{
		"code":                  "true",
		"client_id":             OAuthClientID,
		"response_type":         "code",
		"redirect_uri":          OAuthRedirectURI,
		"scope":                 strings.Join(OAuthScopes, " "),
		"code_challenge":        d.codeChallenge,
		"code_challenge_method": "S256",
		"state":                 d.state,
	}

	queryParams := []string{}
	for key, value := range params {
		queryParams = append(queryParams, fmt.Sprintf("%s=%s", key, value))
	}

	authURL := fmt.Sprintf("%s?%s", OAuthAuthorizeURL, strings.Join(queryParams, "&"))

	return &DeviceCodeResponse{
		DeviceCode:              d.codeVerifier,
		UserCode:                "ANTHROPIC",
		VerificationURI:         "https://console.anthropic.com/oauth/authorize",
		VerificationURIComplete: authURL,
		ExpiresIn:               1800, // 30 minutes
		Interval:                5,
	}, nil
}

// ExchangeCodeForToken exchanges authorization code for access token
func (d *DeviceFlow) ExchangeCodeForToken(authCodeWithState string) (*OAuthToken, error) {
	if d.codeVerifier == "" {
		return nil, fmt.Errorf("no PKCE code verifier - OAuth flow not initialized")
	}

	// Split code and state - format: code#state
	parts := strings.SplitN(authCodeWithState, "#", 2)
	authCode := parts[0]
	var stateFromResponse string
	if len(parts) > 1 {
		stateFromResponse = parts[1]
	}

	// Use state from response or fallback to stored state
	finalState := stateFromResponse
	if finalState == "" {
		finalState = d.state
	}

	// Build token request
	requestBody := map[string]string{
		"grant_type":    "authorization_code",
		"code":          authCode,
		"state":         finalState,
		"client_id":     OAuthClientID,
		"redirect_uri":  OAuthRedirectURI,
		"code_verifier": d.codeVerifier,
	}

	jsonBody, err := json.Marshal(requestBody)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request body: %w", err)
	}

	// Make token exchange request
	req, err := http.NewRequest("POST", OAuthTokenURL, strings.NewReader(string(jsonBody)))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to exchange authorization code: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("failed to exchange authorization code (status %d): %s", resp.StatusCode, string(body))
	}

	// Parse token response
	var tokenResp struct {
		AccessToken  string `json:"access_token"`
		TokenType    string `json:"token_type"`
		ExpiresIn    int    `json:"expires_in"`
		RefreshToken string `json:"refresh_token"`
		Scope        string `json:"scope"`
	}

	if err := json.Unmarshal(body, &tokenResp); err != nil {
		return nil, fmt.Errorf("failed to parse token response: %w", err)
	}

	return &OAuthToken{
		AccessToken:  tokenResp.AccessToken,
		TokenType:    tokenResp.TokenType,
		ExpiresIn:    tokenResp.ExpiresIn,
		RefreshToken: tokenResp.RefreshToken,
		Scope:        tokenResp.Scope,
		Expiry:       time.Now().Unix() + int64(tokenResp.ExpiresIn),
	}, nil
}

// GetCLISystemPromptPrefix returns the required OAuth system prompt prefix
// This MUST be at the beginning of the system prompt when using OAuth tokens
// CRITICAL: Must use Claude Code identity for Claude Code OAuth tokens
func GetCLISystemPromptPrefix() string {
	return "You are Claude Code, Anthropic's official CLI for Claude."
}

// RefreshAccessToken refreshes an expired OAuth token using the refresh token
func RefreshAccessToken(refreshToken string) (*OAuthToken, error) {
	if refreshToken == "" {
		return nil, fmt.Errorf("no refresh token available")
	}

	// Build token refresh request.
	// CRITICAL: do NOT send `scope` on the refresh_token grant. The Anthropic
	// OAuth token endpoint rejects a refresh request that carries a `scope`
	// parameter with HTTP 400 invalid_scope ("The requested scope is invalid,
	// unknown, or malformed"), which silently breaks token refresh (and thus
	// compaction / any request after the access token expires) even though
	// interactive chat still works on the not-yet-expired token. Per OAuth 2.0
	// (RFC 6749 §6) scope is OPTIONAL on refresh and must not request
	// additional scope, so we omit it entirely and let the original grant's
	// scope carry over.
	requestBody := map[string]string{
		"grant_type":    "refresh_token",
		"refresh_token": refreshToken,
		"client_id":     OAuthClientID,
	}

	jsonBody, err := json.Marshal(requestBody)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal refresh request: %w", err)
	}

	// Make token refresh request
	req, err := http.NewRequest("POST", OAuthTokenURL, strings.NewReader(string(jsonBody)))
	if err != nil {
		return nil, fmt.Errorf("failed to create refresh request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to refresh token: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read refresh response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("failed to refresh token (status %d): %s", resp.StatusCode, string(body))
	}

	// Parse token response
	var tokenResp struct {
		AccessToken  string `json:"access_token"`
		TokenType    string `json:"token_type"`
		ExpiresIn    int    `json:"expires_in"`
		RefreshToken string `json:"refresh_token"`
		Scope        string `json:"scope"`
	}

	if err := json.Unmarshal(body, &tokenResp); err != nil {
		return nil, fmt.Errorf("failed to parse refresh response: %w", err)
	}

	// Preserve the previous refresh token when the response omits a new one.
	// Anthropic frequently returns a refresh response with no refresh_token,
	// in which case the existing refresh token remains valid. Blindly storing
	// the empty value would permanently break future refreshes.
	newRefreshToken := tokenResp.RefreshToken
	if newRefreshToken == "" {
		newRefreshToken = refreshToken
	}

	// Return new token with updated expiry
	return &OAuthToken{
		AccessToken:  tokenResp.AccessToken,
		TokenType:    tokenResp.TokenType,
		ExpiresIn:    tokenResp.ExpiresIn,
		RefreshToken: newRefreshToken,
		Scope:        tokenResp.Scope,
		Expiry:       time.Now().Unix() + int64(tokenResp.ExpiresIn),
	}, nil
}

// IsTokenExpired checks if an OAuth token has expired
func IsTokenExpired(token *OAuthToken) bool {
	if token == nil {
		return true
	}
	// Add 5 minute buffer to avoid edge cases
	return time.Now().Unix() >= (token.Expiry - 300)
}

// IsOAuthToken checks if a token looks like an OAuth token
// OAuth tokens start with "sk-ant-oat" (not just "sk-ant-")
func IsOAuthToken(token string) bool {
	return strings.HasPrefix(token, "sk-ant-oat")
}
