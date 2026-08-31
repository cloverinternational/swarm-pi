package update

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"time"
)

// ConfigManager handles loading and saving update configuration
type ConfigManager struct {
	configPath string
	config     *Config
	mu         sync.RWMutex
}

// NewConfigManager creates a new config manager
func NewConfigManager() (*ConfigManager, error) {
	configDir, err := os.UserConfigDir()
	if err != nil {
		return nil, err
	}

	swarmConfigDir := filepath.Join(configDir, "swarmos")
	if err := os.MkdirAll(swarmConfigDir, 0755); err != nil {
		return nil, err
	}

	cm := &ConfigManager{
		configPath: filepath.Join(swarmConfigDir, "update.json"),
		config:     DefaultConfig(),
	}

	// Load existing config if available
	if err := cm.load(); err != nil && !os.IsNotExist(err) {
		// Log warning but continue with defaults
		// In production, this would use proper logging
	}

	return cm, nil
}

// load reads the config from disk
func (cm *ConfigManager) load() error {
	data, err := os.ReadFile(cm.configPath)
	if err != nil {
		return err
	}

	cm.mu.Lock()
	defer cm.mu.Unlock()

	return json.Unmarshal(data, &cm.config)
}

// save writes the config to disk
func (cm *ConfigManager) save() error {
	cm.mu.RLock()
	data, err := json.MarshalIndent(cm.config, "", "  ")
	cm.mu.RUnlock()

	if err != nil {
		return err
	}

	return os.WriteFile(cm.configPath, data, 0644)
}

// Get returns the current configuration
func (cm *ConfigManager) Get() *Config {
	cm.mu.RLock()
	defer cm.mu.RUnlock()

	// Return a copy to prevent external modification
	config := *cm.config
	return &config
}

// SetMode updates the update mode
func (cm *ConfigManager) SetMode(mode UpdateMode) error {
	cm.mu.Lock()
	cm.config.Mode = mode
	cm.mu.Unlock()

	return cm.save()
}

// SetChannel updates the release channel
func (cm *ConfigManager) SetChannel(channel ReleaseChannel) error {
	cm.mu.Lock()
	cm.config.Channel = channel
	cm.mu.Unlock()

	return cm.save()
}

// SetCheckInterval updates the check interval
func (cm *ConfigManager) SetCheckInterval(interval time.Duration) error {
	cm.mu.Lock()
	cm.config.CheckInterval = interval
	cm.mu.Unlock()

	return cm.save()
}

// UpdateLastCheck records the last check time
func (cm *ConfigManager) UpdateLastCheck() error {
	cm.mu.Lock()
	cm.config.LastCheck = time.Now()
	cm.mu.Unlock()

	return cm.save()
}

// ShouldSkipVersion checks if a version should be skipped
func (cm *ConfigManager) ShouldSkipVersion(version string) bool {
	cm.mu.RLock()
	defer cm.mu.RUnlock()

	return slices.Contains(cm.config.SkipVersions, version)
}

// SkipVersion adds a version to the skip list
func (cm *ConfigManager) SkipVersion(version string) error {
	cm.mu.Lock()
	cm.config.SkipVersions = append(cm.config.SkipVersions, version)
	cm.mu.Unlock()

	return cm.save()
}

// SetLastNotified records which version we last notified about
func (cm *ConfigManager) SetLastNotified(version string) error {
	cm.mu.Lock()
	cm.config.LastNotified = version
	cm.mu.Unlock()

	return cm.save()
}

// ShouldNotify checks if we should notify about this version
// (returns true if we haven't notified about this version yet)
func (cm *ConfigManager) ShouldNotify(version string) bool {
	cm.mu.RLock()
	defer cm.mu.RUnlock()

	return cm.config.LastNotified != version
}

// ShouldCheck returns true if enough time has passed since last check
func (cm *ConfigManager) ShouldCheck() bool {
	cm.mu.RLock()
	defer cm.mu.RUnlock()

	if cm.config.Mode == ModeDisabled || cm.config.Mode == ModeManual {
		return false
	}

	if cm.config.LastCheck.IsZero() {
		return true
	}

	return time.Since(cm.config.LastCheck) >= cm.config.CheckInterval
}

// TimeUntilNextCheck returns the duration until the next check should be performed
func (cm *ConfigManager) TimeUntilNextCheck() time.Duration {
	cm.mu.RLock()
	defer cm.mu.RUnlock()

	if cm.config.LastCheck.IsZero() {
		return 0
	}

	elapsed := time.Since(cm.config.LastCheck)
	if elapsed >= cm.config.CheckInterval {
		return 0
	}

	return cm.config.CheckInterval - elapsed
}
