package openai

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sync"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/atomicfile"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/paths"
)

var oauthConfigMu sync.Mutex

func withOAuthConfigLock(path string, fn func() error) error {
	oauthConfigMu.Lock()
	defer oauthConfigMu.Unlock()
	return atomicfile.WithLock(path, fn)
}

// OAuthConfig holds persisted OpenAI OAuth details.
type OAuthConfig struct {
	Token *OAuthToken `json:"token,omitempty"`
}

// getOAuthConfigPath resolves the config path (~/.swarm/config/oauth/openai.json).
func getOAuthConfigPath() (string, error) {
	return paths.OAuthFile("openai"), nil
}

// LoadOAuthConfig reads the persisted OAuth config.
func LoadOAuthConfig() (*OAuthConfig, error) {
	path, err := getOAuthConfigPath()
	if err != nil {
		return nil, err
	}
	var cfg *OAuthConfig
	err = withOAuthConfigLock(path, func() error {
		var loadErr error
		cfg, loadErr = loadOAuthConfigLocked(path)
		return loadErr
	})
	return cfg, err
}

func loadOAuthConfigLocked(path string) (*OAuthConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return &OAuthConfig{}, nil
		}
		return nil, fmt.Errorf("failed to read OpenAI OAuth config: %w", err)
	}
	var cfg OAuthConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("failed to parse OpenAI OAuth config: %w", err)
	}
	return &cfg, nil
}

// saveOAuthConfigLocked marshals and atomically writes the OAuth config at
// 0600. Callers MUST already hold both the process mutex and advisory lock.
func saveOAuthConfigLocked(path string, cfg *OAuthConfig) error {
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal OpenAI OAuth config: %w", err)
	}
	if err := atomicfile.Write(path, data); err != nil {
		return fmt.Errorf("failed to write OpenAI OAuth config: %w", err)
	}
	return nil
}

// SaveOAuthConfig writes the OAuth config to disk atomically at 0600.
func SaveOAuthConfig(cfg *OAuthConfig) error {
	path, err := getOAuthConfigPath()
	if err != nil {
		return err
	}
	return withOAuthConfigLock(path, func() error {
		return saveOAuthConfigLocked(path, cfg)
	})
}

// StoreOAuthToken persists a token (and minted API key).
func StoreOAuthToken(token *OAuthToken) error {
	return SaveOAuthConfig(&OAuthConfig{Token: token})
}

// GetStoredOAuthToken returns the saved token, if present.
func GetStoredOAuthToken() (*OAuthToken, error) {
	cfg, err := LoadOAuthConfig()
	if err != nil {
		return nil, err
	}
	return cfg.Token, nil
}

// ClearOAuthToken removes the stored OAuth token.
func ClearOAuthToken() error {
	path, err := getOAuthConfigPath()
	if err != nil {
		return err
	}
	return withOAuthConfigLock(path, func() error {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("failed to remove OpenAI OAuth config: %w", err)
		}
		return nil
	})
}

// RefreshAndStoreToken refreshes the token if needed and persists it.
func RefreshAndStoreToken(ctx context.Context) (*OAuthToken, error) {
	path, err := getOAuthConfigPath()
	if err != nil {
		return nil, err
	}

	var result *OAuthToken
	lockErr := withOAuthConfigLock(path, func() error {
		cfg, err := loadOAuthConfigLocked(path)
		if err != nil {
			return err
		}
		if cfg.Token == nil {
			return fmt.Errorf("no OpenAI OAuth token stored")
		}
		if !IsTokenExpired(cfg.Token) {
			if cfg.Token.APIKey != "" || (cfg.Token.AccessToken != "" && cfg.Token.AccountID != "") {
				result = cfg.Token
				return nil
			}
			if cfg.Token.IDToken != "" {
				apiKey, exchangeErr := exchangeIDTokenForAPIKey(ctx, cfg.Token.IDToken)
				if exchangeErr == nil && apiKey != "" {
					cfg.Token.APIKey = apiKey
					if cfg.Token.AccountID == "" {
						cfg.Token.AccountID = extractAccountID(cfg.Token.IDToken)
					}
					if err := saveOAuthConfigLocked(path, cfg); err != nil {
						return err
					}
					result = cfg.Token
					return nil
				}
			}
		}
		newToken, err := RefreshAccessToken(ctx, cfg.Token)
		if err != nil {
			return err
		}
		if err := saveOAuthConfigLocked(path, &OAuthConfig{Token: newToken}); err != nil {
			return err
		}
		result = newToken
		return nil
	})
	if lockErr != nil {
		return nil, lockErr
	}
	return result, nil
}
