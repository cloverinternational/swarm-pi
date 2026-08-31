package mcp

import (
	"context"
	"time"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/tools/mcp/oauth"
)

// CredentialTokenStorage persists OAuth tokens into credentials.json.
type CredentialTokenStorage struct {
	Store         CredentialsStore
	CredentialRef string
	ServerURL     string
}

func (s *CredentialTokenStorage) SaveToken(serverURL string, token *oauth.TokenData) error {
	if s.Store == nil {
		return nil
	}
	if token == nil {
		return nil
	}

	// Ensure ServerURL is set
	token.ServerURL = serverURL

	ctx := context.Background()
	creds, err := s.Store.Load(ctx)
	if err != nil {
		return err
	}
	ref := s.CredentialRef
	if ref == "" {
		ref = serverURL
	}
	cred := ensureCredential(creds, ref)
	cred.Token = token.AccessToken
	if token.RefreshToken != "" {
		cred.Headers["oauth.refresh_token"] = token.RefreshToken
	}
	if token.TokenType != "" {
		cred.Headers["oauth.token_type"] = token.TokenType
	}
	if token.Scope != "" {
		cred.Headers["oauth.scope"] = token.Scope
	}
	if !token.ExpiresAt.IsZero() {
		cred.Headers["oauth.expires_at"] = token.ExpiresAt.UTC().Format(time.RFC3339)
	}
	// Store ServerURL
	if token.ServerURL != "" {
		cred.Headers["oauth.server_url"] = token.ServerURL
	}
	creds.MCP[ref] = cred
	return s.Store.Save(ctx, creds)
}

func (s *CredentialTokenStorage) LoadToken(serverURL string) (*oauth.TokenData, error) {
	if s.Store == nil {
		return nil, nil
	}
	ctx := context.Background()
	creds, err := s.Store.Load(ctx)
	if err != nil {
		return nil, err
	}
	ref := s.CredentialRef
	if ref == "" {
		ref = serverURL
	}
	cred := getCredential(creds, ref)
	if cred == nil {
		return nil, nil
	}
	if cred.Token == "" && len(cred.Headers) == 0 {
		return nil, nil
	}
	result := &oauth.TokenData{
		AccessToken:  cred.Token,
		TokenType:    "Bearer",
		RefreshToken: cred.Headers["oauth.refresh_token"],
		Scope:        cred.Headers["oauth.scope"],
		ServerURL:    cred.Headers["oauth.server_url"],
	}
	if tokenType := cred.Headers["oauth.token_type"]; tokenType != "" {
		result.TokenType = tokenType
	}
	if expires := cred.Headers["oauth.expires_at"]; expires != "" {
		if parsed, err := time.Parse(time.RFC3339, expires); err == nil {
			result.ExpiresAt = parsed
		}
	}
	return result, nil
}

func (s *CredentialTokenStorage) DeleteToken(serverURL string) error {
	if s.Store == nil {
		return nil
	}
	ctx := context.Background()
	creds, err := s.Store.Load(ctx)
	if err != nil {
		return err
	}
	ref := s.CredentialRef
	if ref == "" {
		ref = serverURL
	}
	cred := getCredential(creds, ref)
	if cred == nil {
		return nil
	}
	cred.Token = ""
	delete(cred.Headers, "oauth.refresh_token")
	delete(cred.Headers, "oauth.token_type")
	delete(cred.Headers, "oauth.scope")
	delete(cred.Headers, "oauth.expires_at")
	delete(cred.Headers, "oauth.server_url")
	creds.MCP[ref] = *cred
	return s.Store.Save(ctx, creds)
}

func (s *CredentialTokenStorage) ListTokens() ([]string, error) {
	if s.Store == nil {
		return nil, nil
	}
	ctx := context.Background()
	creds, err := s.Store.Load(ctx)
	if err != nil {
		return nil, err
	}
	urls := make([]string, 0, len(creds.MCP))
	for key, cred := range creds.MCP {
		if cred.Token != "" || len(cred.Headers) > 0 {
			urls = append(urls, key)
		}
	}
	return urls, nil
}

// LoadTokenForURL loads a token and validates it matches the server URL.
// For CredentialTokenStorage, this delegates to LoadToken since credentials
// are already keyed by server URL/reference.
func (s *CredentialTokenStorage) LoadTokenForURL(serverURL string) (*oauth.TokenData, error) {
	token, err := s.LoadToken(serverURL)
	if err != nil || token == nil {
		return token, err
	}

	// Validate ServerURL if set
	if token.ServerURL != "" && token.ServerURL != serverURL {
		return nil, nil // URL mismatch - return nil token
	}

	// Set ServerURL if not already set (for backward compatibility)
	if token.ServerURL == "" {
		token.ServerURL = serverURL
	}

	return token, nil
}

// SaveClientInfo saves client registration information.
// Stores client info in credential headers with "oauth.client." prefix.
func (s *CredentialTokenStorage) SaveClientInfo(serverURL string, clientInfo *oauth.ClientInfo) error {
	if s.Store == nil || clientInfo == nil {
		return nil
	}
	ctx := context.Background()
	creds, err := s.Store.Load(ctx)
	if err != nil {
		return err
	}
	ref := s.CredentialRef
	if ref == "" {
		ref = serverURL
	}
	cred := ensureCredential(creds, ref)

	// Store client info in headers
	cred.Headers["oauth.client.id"] = clientInfo.ClientID
	if clientInfo.ClientSecret != "" {
		cred.Headers["oauth.client.secret"] = clientInfo.ClientSecret
	}
	if !clientInfo.ClientIDIssuedAt.IsZero() {
		cred.Headers["oauth.client.id_issued_at"] = clientInfo.ClientIDIssuedAt.UTC().Format(time.RFC3339)
	}
	if !clientInfo.ClientSecretExpiresAt.IsZero() {
		cred.Headers["oauth.client.secret_expires_at"] = clientInfo.ClientSecretExpiresAt.UTC().Format(time.RFC3339)
	}

	creds.MCP[ref] = cred
	return s.Store.Save(ctx, creds)
}

// LoadClientInfo loads client registration information.
func (s *CredentialTokenStorage) LoadClientInfo(serverURL string) (*oauth.ClientInfo, error) {
	if s.Store == nil {
		return nil, nil
	}
	ctx := context.Background()
	creds, err := s.Store.Load(ctx)
	if err != nil {
		return nil, err
	}
	ref := s.CredentialRef
	if ref == "" {
		ref = serverURL
	}
	cred := getCredential(creds, ref)
	if cred == nil {
		return nil, nil
	}

	clientID := cred.Headers["oauth.client.id"]
	if clientID == "" {
		return nil, nil
	}

	result := &oauth.ClientInfo{
		ClientID:     clientID,
		ClientSecret: cred.Headers["oauth.client.secret"],
	}

	if issuedAt := cred.Headers["oauth.client.id_issued_at"]; issuedAt != "" {
		if parsed, err := time.Parse(time.RFC3339, issuedAt); err == nil {
			result.ClientIDIssuedAt = parsed
		}
	}

	if expiresAt := cred.Headers["oauth.client.secret_expires_at"]; expiresAt != "" {
		if parsed, err := time.Parse(time.RFC3339, expiresAt); err == nil {
			result.ClientSecretExpiresAt = parsed
		}
	}

	return result, nil
}

// DeleteClientInfo deletes client registration information.
func (s *CredentialTokenStorage) DeleteClientInfo(serverURL string) error {
	if s.Store == nil {
		return nil
	}
	ctx := context.Background()
	creds, err := s.Store.Load(ctx)
	if err != nil {
		return err
	}
	ref := s.CredentialRef
	if ref == "" {
		ref = serverURL
	}
	cred := getCredential(creds, ref)
	if cred == nil {
		return nil
	}

	// Remove client info headers
	delete(cred.Headers, "oauth.client.id")
	delete(cred.Headers, "oauth.client.secret")
	delete(cred.Headers, "oauth.client.id_issued_at")
	delete(cred.Headers, "oauth.client.secret_expires_at")

	creds.MCP[ref] = *cred
	return s.Store.Save(ctx, creds)
}

func tokenStorageFor(server ServerConfig, store CredentialsStore) oauth.TokenStorage {
	ref := credentialRef(server)
	return &CredentialTokenStorage{
		Store:         store,
		CredentialRef: ref,
		ServerURL:     server.URL,
	}
}

// compile-time check
var _ oauth.TokenStorage = (*CredentialTokenStorage)(nil)
