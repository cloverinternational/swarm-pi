package cloud

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/atomicfile"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/paths"
)

const (
	defaultRegion         string = "us-east-2"
	defaultAuthIssuerURL  string = "https://cognito-idp.us-east-2.amazonaws.com/us-east-2_ujLOU4j5x"
	defaultHostedUIURL    string = "https://auth.swarmcode.ai"
	defaultPublicClientID string = "4tltqh814mgcq22c21nvdocb1h"
	defaultAPIBaseURL     string = "https://api.swarmcode.ai"
	defaultAPIFallbackURL string = "https://exuhtyu4vl.execute-api.us-east-2.amazonaws.com"

	legacyAuthIssuerURL  string = "https://cognito-idp.us-east-2.amazonaws.com/us-east-2_6TLn51FRx"
	legacyHostedUIURL    string = "https://auth-tailnet.swarmcode.ai"
	legacyPublicClientID string = "3tu9vpsfkpkut79fqp1f34qvve"
)

// CloudConfig stores Swarm Cloud auth and API settings.
type CloudConfig struct {
	Region         string `json:"region"`
	AuthIssuerURL  string `json:"auth_issuer_url"`
	HostedUIURL    string `json:"hosted_ui_url"`
	PublicClientID string `json:"public_client_id"`
	APIBaseURL     string `json:"api_base_url"`
	APIFallbackURL string `json:"api_fallback_url"`
	DeviceID       string `json:"device_id,omitempty"`
}

// ConfigManager manages cloud config persisted under ~/.swarm.
type ConfigManager struct {
	configDir string
}

// NewConfigManager creates a new cloud config manager.
func NewConfigManager() (*ConfigManager, error) {
	// Canonical swarm root (~/.swarm). cloud.json remains a root-level file to
	// preserve the historical layout, just under the unified root.
	var configDir string = paths.Root()
	if err := paths.EnsureDir(configDir); err != nil {
		return nil, fmt.Errorf("failed to create config directory: %w", err)
	}

	return &ConfigManager{configDir: configDir}, nil
}

// GetConfigPath returns the path to the cloud config file.
func (cm *ConfigManager) GetConfigPath() string {
	return filepath.Join(cm.configDir, "cloud.json")
}

// LoadConfig loads cloud config, creating a default config if missing.
func (cm *ConfigManager) LoadConfig() (*CloudConfig, error) {
	var path string = cm.GetConfigPath()

	if _, err := os.Stat(path); os.IsNotExist(err) {
		var config CloudConfig = DefaultConfig()
		if _, err = ensureDeviceID(&config); err != nil {
			return nil, err
		}
		if err = cm.SaveConfig(&config); err != nil {
			return nil, err
		}
		ApplyEnvOverrides(&config)
		return &config, nil
	}

	var data []byte
	var err error
	data, err = os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read cloud.json: %w", err)
	}

	var config CloudConfig
	if err = json.Unmarshal(data, &config); err != nil {
		return nil, fmt.Errorf("failed to parse cloud.json: %w", err)
	}

	var updated bool
	updated, err = ensureDeviceID(&config)
	if err != nil {
		return nil, err
	}
	var hostedUpdated bool
	hostedUpdated, err = ensureHostedUIConfig(&config)
	if err != nil {
		return nil, err
	}
	if updated || hostedUpdated {
		if err = cm.SaveConfig(&config); err != nil {
			return nil, err
		}
	}

	ApplyEnvOverrides(&config)
	return &config, nil
}

// SaveConfig writes cloud config to disk.
func (cm *ConfigManager) SaveConfig(config *CloudConfig) error {
	var path string = cm.GetConfigPath()
	var data []byte
	var err error

	data, err = json.MarshalIndent(config, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal cloud config: %w", err)
	}

	// Atomic, cross-process-locked write. cloud.json is non-secret config
	// (region/URLs/device id) so 0644 is preserved; the lock guards the
	// load-modify-save sequence in LoadConfig (device id / hosted-ui backfill).
	if err = atomicfile.WithLock(path, func() error {
		return atomicfile.Write(path, data, atomicfile.WithPerm(0o644))
	}); err != nil {
		return fmt.Errorf("failed to write cloud.json: %w", err)
	}

	return nil
}

// DefaultConfig returns the default cloud configuration.
func DefaultConfig() CloudConfig {
	var config CloudConfig = CloudConfig{
		Region:         getEnvOrDefault("SWARMOS_CLOUD_REGION", defaultRegion),
		AuthIssuerURL:  getEnvOrDefault("SWARMOS_CLOUD_ISSUER_URL", defaultAuthIssuerURL),
		HostedUIURL:    getEnvOrDefault("SWARMOS_CLOUD_HOSTED_UI_URL", defaultHostedUIURL),
		PublicClientID: getEnvOrDefault("SWARMOS_CLOUD_PUBLIC_CLIENT_ID", defaultPublicClientID),
		APIBaseURL:     getEnvOrDefault("SWARMOS_CLOUD_API_BASE_URL", defaultAPIBaseURL),
		APIFallbackURL: getEnvOrDefault("SWARMOS_CLOUD_API_FALLBACK_URL", defaultAPIFallbackURL),
	}

	return config
}

// ApplyEnvOverrides overrides config values if env vars are present.
func ApplyEnvOverrides(config *CloudConfig) {
	var value string

	value = os.Getenv("SWARMOS_CLOUD_REGION")
	if value != "" {
		config.Region = value
	}

	value = os.Getenv("SWARMOS_CLOUD_ISSUER_URL")
	if value != "" {
		config.AuthIssuerURL = value
	}

	value = os.Getenv("SWARMOS_CLOUD_HOSTED_UI_URL")
	if value != "" {
		config.HostedUIURL = value
	}

	value = os.Getenv("SWARMOS_CLOUD_PUBLIC_CLIENT_ID")
	if value != "" {
		config.PublicClientID = value
	}

	value = os.Getenv("SWARMOS_CLOUD_API_BASE_URL")
	if value != "" {
		config.APIBaseURL = value
	}

	value = os.Getenv("SWARMOS_CLOUD_API_FALLBACK_URL")
	if value != "" {
		config.APIFallbackURL = value
	}
}

func getEnvOrDefault(key string, fallback string) string {
	var value string = os.Getenv(key)
	if value == "" {
		return fallback
	}
	return value
}

func ensureDeviceID(config *CloudConfig) (bool, error) {
	if config == nil {
		return false, fmt.Errorf("cloud config is nil")
	}
	if config.DeviceID != "" {
		return false, nil
	}

	var id string
	var err error
	id, err = NewDeviceID()
	if err != nil {
		return false, err
	}
	config.DeviceID = id
	return true, nil
}

func ensureHostedUIConfig(config *CloudConfig) (bool, error) {
	if config == nil {
		return false, fmt.Errorf("cloud config is nil")
	}
	updated := false

	if config.HostedUIURL == "" {
		config.HostedUIURL = defaultHostedUIURL
		updated = true
	}
	if config.AuthIssuerURL == "" {
		config.AuthIssuerURL = defaultAuthIssuerURL
		updated = true
	}
	if config.PublicClientID == "" {
		config.PublicClientID = defaultPublicClientID
		updated = true
	}

	if config.HostedUIURL == legacyHostedUIURL &&
		config.PublicClientID == legacyPublicClientID &&
		config.AuthIssuerURL == legacyAuthIssuerURL {
		config.HostedUIURL = defaultHostedUIURL
		config.PublicClientID = defaultPublicClientID
		config.AuthIssuerURL = defaultAuthIssuerURL
		return true, nil
	}

	if config.HostedUIURL == defaultHostedUIURL && config.PublicClientID == legacyPublicClientID {
		config.PublicClientID = defaultPublicClientID
		updated = true
	}
	if config.HostedUIURL == defaultHostedUIURL && config.AuthIssuerURL == legacyAuthIssuerURL {
		config.AuthIssuerURL = defaultAuthIssuerURL
		updated = true
	}

	return updated, nil
}
