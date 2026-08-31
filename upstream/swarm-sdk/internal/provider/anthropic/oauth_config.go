package anthropic

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/atomicfile"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/paths"
)

// OAuthConfig represents stored OAuth configuration
type OAuthConfig struct {
	Token          *OAuthToken         `json:"token,omitempty"`
	DeviceIdentity *DeviceIdentity     `json:"device_identity,omitempty"`
	ModelProfiles  []OAuthModelProfile `json:"model_profiles,omitempty"`
	// AccountIdentities maps account ID → DeviceIdentity for multi-account setups.
	// Each entry is the stable fingerprint for that specific account so requests
	// from different accounts carry distinct device_id / host_id values.
	AccountIdentities map[string]*DeviceIdentity `json:"account_identities,omitempty"`
}

// OAuthModelProfile represents an OAuth-enabled model profile
type OAuthModelProfile struct {
	ModelID  string `json:"model_id"`
	Name     string `json:"name"`
	Provider string `json:"provider"`
	IsActive bool   `json:"is_active"`
	LastUsed int64  `json:"last_used"`
}

// Default OAuth models
var DefaultOAuthModels = []OAuthModelProfile{
	{
		ModelID:  "claude-fable-5",
		Name:     "Claude Fable 5 (OAuth)",
		Provider: "anthropic",
		IsActive: true,
	},
	{
		ModelID:  "claude-opus-4-7",
		Name:     "Claude Opus 4.7 (OAuth)",
		Provider: "anthropic",
		IsActive: true,
	},
	{
		ModelID:  "claude-opus-4-6",
		Name:     "Claude Opus 4.6 (OAuth)",
		Provider: "anthropic",
		IsActive: true,
	},
	{
		ModelID:  "claude-sonnet-4-6",
		Name:     "Claude Sonnet 4.6 (OAuth)",
		Provider: "anthropic",
		IsActive: true,
	},
	{
		ModelID:  "claude-haiku-4-5-20251001",
		Name:     "Claude Haiku 4.5 (OAuth)",
		Provider: "anthropic",
		IsActive: true,
	},
}

// GetOAuthConfigPath returns the path to the OAuth config file
func GetOAuthConfigPath() (string, error) {
	// Canonical per-provider OAuth store: ~/.swarm/config/oauth/anthropic.json.
	// paths.OAuthFile ensures the oauth directory exists at 0700.
	return paths.OAuthFile("anthropic"), nil
}

// LoadOAuthConfig loads OAuth configuration from disk
func LoadOAuthConfig() (*OAuthConfig, error) {
	configPath, err := GetOAuthConfigPath()
	if err != nil {
		return nil, err
	}

	data, err := os.ReadFile(configPath)
	if err != nil {
		if os.IsNotExist(err) {
			return &OAuthConfig{}, nil
		}
		return nil, fmt.Errorf("failed to read OAuth config: %w", err)
	}

	var config OAuthConfig
	if err := json.Unmarshal(data, &config); err != nil {
		return nil, fmt.Errorf("failed to parse OAuth config: %w", err)
	}

	return &config, nil
}

// SaveOAuthConfig saves OAuth configuration to disk
func SaveOAuthConfig(config *OAuthConfig) error {
	configPath, err := GetOAuthConfigPath()
	if err != nil {
		return err
	}

	data, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal OAuth config: %w", err)
	}

	// Atomic, cross-process-locked write with restricted (0600) permissions.
	// The lock guards the load-modify-save sequences (StoreOAuthToken,
	// RefreshAndStoreToken, device-identity updates) against concurrent writers.
	if err := atomicfile.WithLock(configPath, func() error {
		return atomicfile.Write(configPath, data)
	}); err != nil {
		return fmt.Errorf("failed to write OAuth config: %w", err)
	}

	return nil
}

// StoreOAuthToken stores an OAuth token and creates model profiles.
// It also creates a device identity if one doesn't exist.
func StoreOAuthToken(token *OAuthToken) error {
	// Load existing config to preserve device identity if it exists
	existingConfig, err := LoadOAuthConfig()
	if err != nil {
		existingConfig = &OAuthConfig{}
	}

	config := &OAuthConfig{
		Token:          token,
		DeviceIdentity: existingConfig.DeviceIdentity, // Preserve existing identity
		ModelProfiles:  DefaultOAuthModels,
	}

	// Create device identity if not present
	if config.DeviceIdentity == nil {
		identity, err := EnsureDeviceIdentityForToken(token)
		if err != nil {
			// Non-fatal: log but continue
			fmt.Fprintf(os.Stderr, "[WARN] Failed to create device identity: %v\n", err)
		} else {
			config.DeviceIdentity = identity
			// Cache for current session
			SetCachedDeviceIdentity(identity)
		}
	}

	// Update timestamps
	now := os.Getpid() // Use process start time as proxy
	for i := range config.ModelProfiles {
		config.ModelProfiles[i].LastUsed = int64(now)
	}

	return SaveOAuthConfig(config)
}

// GetStoredOAuthToken retrieves the stored OAuth token
func GetStoredOAuthToken() (*OAuthToken, error) {
	config, err := LoadOAuthConfig()
	if err != nil {
		return nil, err
	}

	if config.Token == nil {
		return nil, fmt.Errorf("no OAuth token stored")
	}

	return config.Token, nil
}

// ClearOAuthToken removes the stored OAuth token
func ClearOAuthToken() error {
	configPath, err := GetOAuthConfigPath()
	if err != nil {
		return err
	}

	if err := os.Remove(configPath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("failed to remove OAuth config: %w", err)
	}

	return nil
}

// RefreshAndStoreToken attempts to refresh the OAuth token and save it.
// Preserves the existing device identity.
func RefreshAndStoreToken() (*OAuthToken, error) {
	// Load current config
	config, err := LoadOAuthConfig()
	if err != nil {
		return nil, fmt.Errorf("failed to load OAuth config: %w", err)
	}

	if config.Token == nil {
		return nil, fmt.Errorf("no OAuth token stored")
	}

	if config.Token.RefreshToken == "" {
		return nil, fmt.Errorf("no refresh token available")
	}

	// Attempt to refresh
	newToken, err := RefreshAccessToken(config.Token.RefreshToken)
	if err != nil {
		return nil, fmt.Errorf("failed to refresh token: %w", err)
	}

	// Preserve device identity when storing refreshed token
	config.Token = newToken
	if err := SaveOAuthConfig(config); err != nil {
		return nil, fmt.Errorf("failed to store refreshed token: %w", err)
	}

	return newToken, nil
}
