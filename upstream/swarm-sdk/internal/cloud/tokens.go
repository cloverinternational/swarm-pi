package cloud

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/atomicfile"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/paths"
)

const (
	cloudTokenFileName string = "cloud_tokens.json"
)

var defaultTokenSkew time.Duration = 60 * time.Second

// TokenSet stores Cognito tokens for Swarm Cloud.
type TokenSet struct {
	AccessToken  string `json:"access_token"`
	IDToken      string `json:"id_token"`
	RefreshToken string `json:"refresh_token"`
	TokenType    string `json:"token_type"`
	Scope        string `json:"scope"`
	ExpiresAt    int64  `json:"expires_at"`
}

// IsExpired returns true if the token is expired or nearing expiry.
func (t *TokenSet) IsExpired() bool {
	return t.IsExpiredWithSkew(defaultTokenSkew)
}

// IsExpiredWithSkew checks expiry with the provided skew.
func (t *TokenSet) IsExpiredWithSkew(skew time.Duration) bool {
	if t == nil {
		return true
	}
	if t.ExpiresAt == 0 {
		return false
	}
	var expiry time.Time = time.Unix(t.ExpiresAt, 0)
	var now time.Time = time.Now()
	return now.After(expiry.Add(-skew))
}

// TokenManager manages cloud token storage.
type TokenManager struct {
	configDir string
}

// NewTokenManager creates a new cloud token manager.
func NewTokenManager() (*TokenManager, error) {
	// Canonical swarm root (~/.swarm). cloud_tokens.json remains a root-level
	// file to preserve the historical layout, just under the unified root.
	var configDir string = paths.Root()
	if err := paths.EnsureDir(configDir); err != nil {
		return nil, fmt.Errorf("failed to create config directory: %w", err)
	}

	return &TokenManager{configDir: configDir}, nil
}

// GetTokenPath returns the path to the cloud token file.
func (tm *TokenManager) GetTokenPath() string {
	return filepath.Join(tm.configDir, cloudTokenFileName)
}

// LoadTokens loads stored cloud tokens if present.
func (tm *TokenManager) LoadTokens() (*TokenSet, error) {
	var path string = tm.GetTokenPath()

	var statErr error
	_, statErr = os.Stat(path)
	if os.IsNotExist(statErr) {
		return nil, nil
	}
	if statErr != nil {
		return nil, fmt.Errorf("failed to stat cloud tokens: %w", statErr)
	}

	var data []byte
	var err error
	data, err = os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read cloud tokens: %w", err)
	}

	var tokens TokenSet
	if err = json.Unmarshal(data, &tokens); err != nil {
		return nil, fmt.Errorf("failed to parse cloud tokens: %w", err)
	}

	return &tokens, nil
}

// SaveTokens persists cloud tokens to disk.
func (tm *TokenManager) SaveTokens(tokens *TokenSet) error {
	if tokens == nil {
		return fmt.Errorf("cannot save nil tokens")
	}

	var path string = tm.GetTokenPath()
	var data []byte
	var err error
	data, err = json.MarshalIndent(tokens, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal cloud tokens: %w", err)
	}

	// Cloud tokens are secrets: atomic, cross-process-locked write at 0600.
	if err = atomicfile.WithLock(path, func() error {
		return atomicfile.Write(path, data)
	}); err != nil {
		return fmt.Errorf("failed to write cloud tokens: %w", err)
	}

	return nil
}

// ClearTokens removes stored cloud tokens.
func (tm *TokenManager) ClearTokens() error {
	var path string = tm.GetTokenPath()
	var err error
	if err = os.Remove(path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("failed to delete cloud tokens: %w", err)
	}
	return nil
}
