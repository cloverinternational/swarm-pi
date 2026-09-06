// Package xai provides OAuth support for xAI / Grok (SuperGrok subscription).
//
// Auth flow: PKCE loopback on 127.0.0.1:56121 (registered with xAI) using
// OIDC discovery to obtain live endpoints.  Mirrors Hermes hermes_cli/auth.py.
//
// Tokens are persisted to ~/.swarm/config/oauth/xai.json.
package xai

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/atomicfile"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/paths"
)

// OAuth constants — matching Hermes hermes_cli/auth.py exactly.
const (
	// OAuthClientID is the public xAI desktop / CLI OAuth client.
	// Source: Hermes XAI_OAUTH_CLIENT_ID
	OAuthClientID = "b1a00492-073a-47ea-816f-4c329264a828"

	// OAuthIssuer is the xAI OAuth / OIDC authority.
	OAuthIssuer = "https://auth.x.ai"

	// OAuthDiscoveryURL is the OIDC discovery document.
	// Auth + token endpoints are fetched from here at runtime — not hardcoded —
	// so xAI endpoint rotations are handled automatically.
	OAuthDiscoveryURL = OAuthIssuer + "/.well-known/openid-configuration"

	// OAuthRedirectHost is the loopback address for the callback server.
	OAuthRedirectHost = "127.0.0.1"

	// OAuthRedirectPort is the FIXED port registered with xAI's OAuth server.
	// Using a random ephemeral port causes redirect_uri_mismatch errors.
	// Falls back to port 0 (ephemeral) only when 56121 is already occupied.
	OAuthRedirectPort = 56121

	// OAuthRedirectPath is the callback path registered with xAI.
	// Source: Hermes XAI_OAUTH_REDIRECT_PATH
	OAuthRedirectPath = "/callback"

	// OAuthScope mirrors Hermes XAI_OAUTH_SCOPE exactly.
	OAuthScope = "openid profile email offline_access grok-cli:access api:access"

	// DefaultBaseURL is the xAI API base URL (OpenAI-compatible).
	DefaultBaseURL = "https://api.x.ai/v1"

	// GrokCLIAuthKeyPrefix is the key prefix inside ~/.grok/auth.json.
	// Full key: "https://auth.x.ai::CLIENT_ID"
	GrokCLIAuthKeyPrefix = "https://auth.x.ai::"
)

// DefaultGrokCLIAuthPath returns the default Grok CLI auth.json path.
func DefaultGrokCLIAuthPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".grok", "auth.json")
}

// OAuthToken represents a stored xAI OAuth token.
type OAuthToken struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token,omitempty"`
	TokenType    string `json:"token_type,omitempty"`
	ExpiresAt    int64  `json:"expires_at,omitempty"` // unix seconds
	Scope        string `json:"scope,omitempty"`
}

// IsExpired returns true if the token has expired (with a 60-second buffer).
func (t *OAuthToken) IsExpired() bool {
	if t.ExpiresAt == 0 {
		return false // no expiry recorded — assume valid
	}
	return time.Now().Unix() > t.ExpiresAt-60
}

// OAuthConfig is the persisted credential file shape.
type OAuthConfig struct {
	Token *OAuthToken `json:"token,omitempty"`
}

// getOAuthConfigPath returns the path to ~/.swarm/config/oauth/xai.json.
func getOAuthConfigPath() (string, error) {
	return paths.OAuthFile("xai"), nil
}

// LoadOAuthConfig reads the persisted xAI OAuth config.
// Returns an empty config (not an error) when the file does not exist.
func LoadOAuthConfig() (*OAuthConfig, error) {
	path, err := getOAuthConfigPath()
	if err != nil {
		return nil, err
	}

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return &OAuthConfig{}, nil
		}
		return nil, fmt.Errorf("failed to read xAI OAuth config: %w", err)
	}

	var cfg OAuthConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("failed to parse xAI OAuth config: %w", err)
	}
	return &cfg, nil
}

// SaveOAuthConfig writes the xAI OAuth config to disk atomically at 0600,
// serialized across processes with an advisory lock.
func SaveOAuthConfig(cfg *OAuthConfig) error {
	path, err := getOAuthConfigPath()
	if err != nil {
		return err
	}

	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal xAI OAuth config: %w", err)
	}

	return atomicfile.WithLock(path, func() error {
		if err := atomicfile.Write(path, data); err != nil {
			return fmt.Errorf("failed to write xAI OAuth config: %w", err)
		}
		return nil
	})
}

// StoreOAuthToken persists the given token.
func StoreOAuthToken(token *OAuthToken) error {
	return SaveOAuthConfig(&OAuthConfig{Token: token})
}

// GetStoredOAuthToken returns the saved token if present.
func GetStoredOAuthToken() (*OAuthToken, error) {
	cfg, err := LoadOAuthConfig()
	if err != nil {
		return nil, err
	}
	if cfg.Token == nil {
		return nil, fmt.Errorf("no xAI OAuth token stored")
	}
	return cfg.Token, nil
}

// ClearOAuthToken removes the stored xAI OAuth token.
func ClearOAuthToken() error {
	path, err := getOAuthConfigPath()
	if err != nil {
		return err
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("failed to remove xAI OAuth config: %w", err)
	}
	return nil
}
