// Package oauth implements OAuth 2.0 authentication for MCP servers.
// It follows the MCP specification's authorization flow using PKCE.
package oauth

import (
	"slices"
	"time"

	"golang.org/x/oauth2"
)

// TokenData holds OAuth token information.
type TokenData struct {
	AccessToken  string    `json:"access_token"`
	RefreshToken string    `json:"refresh_token,omitempty"`
	TokenType    string    `json:"token_type"`
	ExpiresIn    int       `json:"expires_in,omitempty"`
	ExpiresAt    time.Time `json:"expires_at"`
	Scope        string    `json:"scope,omitempty"`

	// ServerURL tracks which server URL this token is for.
	// This prevents token reuse when server URLs change (e.g., http -> https).
	// Inspired by OpenCode's URL validation pattern.
	ServerURL string `json:"server_url,omitempty"`
}

// IsExpired checks if the token has expired.
func (t *TokenData) IsExpired() bool {
	if t.ExpiresAt.IsZero() {
		return false // No expiry set
	}
	// Consider expired 30 seconds before actual expiry for safety margin
	return time.Now().Add(30 * time.Second).After(t.ExpiresAt)
}

// IsValid checks if the token is valid and not expired.
func (t *TokenData) IsValid() bool {
	return t.AccessToken != "" && !t.IsExpired()
}

// CanRefresh checks if the token can be refreshed.
func (t *TokenData) CanRefresh() bool {
	return t.RefreshToken != ""
}

// ToOAuth2Token converts TokenData to oauth2.Token
func (t *TokenData) ToOAuth2Token() *oauth2.Token {
	return &oauth2.Token{
		AccessToken:  t.AccessToken,
		TokenType:    t.TokenType,
		RefreshToken: t.RefreshToken,
		Expiry:       t.ExpiresAt,
	}
}

// FromOAuth2Token converts oauth2.Token to TokenData
func FromOAuth2Token(token *oauth2.Token) *TokenData {
	td := &TokenData{
		AccessToken:  token.AccessToken,
		TokenType:    token.TokenType,
		RefreshToken: token.RefreshToken,
		ExpiresAt:    token.Expiry,
	}
	if !token.Expiry.IsZero() {
		td.ExpiresIn = int(time.Until(token.Expiry).Seconds())
	}
	return td
}

// ServerMetadata represents OAuth 2.0 Authorization Server Metadata (RFC 8414).
type ServerMetadata struct {
	Issuer                            string   `json:"issuer"`
	AuthorizationEndpoint             string   `json:"authorization_endpoint"`
	TokenEndpoint                     string   `json:"token_endpoint"`
	RegistrationEndpoint              string   `json:"registration_endpoint,omitempty"`
	RevocationEndpoint                string   `json:"revocation_endpoint,omitempty"`
	JwksURI                           string   `json:"jwks_uri,omitempty"`
	ScopesSupported                   []string `json:"scopes_supported,omitempty"`
	ResponseTypesSupported            []string `json:"response_types_supported,omitempty"`
	GrantTypesSupported               []string `json:"grant_types_supported,omitempty"`
	CodeChallengeMethodsSupported     []string `json:"code_challenge_methods_supported,omitempty"`
	TokenEndpointAuthMethodsSupported []string `json:"token_endpoint_auth_methods_supported,omitempty"`
}

// SupportsS256 checks if the server supports S256 code challenge method.
func (m *ServerMetadata) SupportsS256() bool {
	if len(m.CodeChallengeMethodsSupported) == 0 {
		// If not specified, assume S256 is supported (MUST per spec)
		return true
	}
	return slices.Contains(m.CodeChallengeMethodsSupported, "S256")
}

// ProtectedResourceMetadata represents OAuth 2.0 Protected Resource Metadata.
type ProtectedResourceMetadata struct {
	Resource               string   `json:"resource"`
	AuthorizationServers   []string `json:"authorization_servers"`
	ScopesSupported        []string `json:"scopes_supported,omitempty"`
	BearerMethodsSupported []string `json:"bearer_methods_supported,omitempty"`
}

// FlowState holds the state of an ongoing OAuth authorization flow.
type FlowState struct {
	CodeVerifier  string
	CodeChallenge string
	State         string
	RedirectURI   string
	ServerURL     string
	AuthURL       string
	TokenURL      string
	ClientID      string
	Scopes        []string
}

// Config holds OAuth configuration for an MCP server.
type Config struct {
	ClientID     string   `json:"client_id"`
	ClientSecret string   `json:"client_secret,omitempty"` // Optional for public clients
	Scopes       []string `json:"scopes,omitempty"`

	// Manual override (discovered automatically if not set)
	AuthURL  string `json:"auth_url,omitempty"`
	TokenURL string `json:"token_url,omitempty"`
}

// TokenResponse represents the response from the token endpoint.
type TokenResponse struct {
	AccessToken  string `json:"access_token"`
	TokenType    string `json:"token_type"`
	ExpiresIn    int    `json:"expires_in,omitempty"`
	RefreshToken string `json:"refresh_token,omitempty"`
	Scope        string `json:"scope,omitempty"`
}

// TokenErrorResponse represents an error response from the token endpoint.
type TokenErrorResponse struct {
	ErrorCode        string `json:"error"`
	ErrorDescription string `json:"error_description,omitempty"`
	ErrorURI         string `json:"error_uri,omitempty"`
}

// Error implements the error interface for TokenErrorResponse.
func (e *TokenErrorResponse) Error() string {
	if e.ErrorDescription != "" {
		return e.ErrorCode + ": " + e.ErrorDescription
	}
	return e.ErrorCode
}

// CallbackResult holds the result of an OAuth callback.
type CallbackResult struct {
	Code  string
	State string
	Error string
}

// ClientInfo holds OAuth client registration information.
// This is used for dynamic client registration or tracking
// pre-registered client credentials with expiry information.
type ClientInfo struct {
	ClientID              string    `json:"client_id"`
	ClientSecret          string    `json:"client_secret,omitempty"`
	ClientIDIssuedAt      time.Time `json:"client_id_issued_at"`
	ClientSecretExpiresAt time.Time `json:"client_secret_expires_at"`
}

// IsExpired checks if the client secret has expired.
func (c *ClientInfo) IsExpired() bool {
	if c.ClientSecretExpiresAt.IsZero() {
		return false // No expiry set
	}
	return time.Now().After(c.ClientSecretExpiresAt)
}

// IsValid checks if client info is valid and not expired.
func (c *ClientInfo) IsValid() bool {
	return c.ClientID != "" && !c.IsExpired()
}
