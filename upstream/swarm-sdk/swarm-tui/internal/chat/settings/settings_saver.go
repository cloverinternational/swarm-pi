package settings

import (
	"fmt"
	"sync"

	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/chat/commands"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/i18n"
)

// SettingsSaver is the STRICT interface that ALL settings must implement.
// This ensures consistent save behavior across all settings screens.
type SettingsSaver interface {
	// Save persists the settings to disk. Returns error if save fails.
	// All implementations MUST use the shared ConfigManager from the Manager.
	Save() error

	// SetConfigManager sets the shared ConfigManager instance.
	// Called once during initialization by Manager.
	SetConfigManager(cm *commands.ConfigManager)

	// GetSectionName returns the section identifier for logging.
	GetSectionName() string
}

// SaveCoordinator manages all settings saves to prevent race conditions
// and ensure consistent save patterns.
type SaveCoordinator struct {
	configManager *commands.ConfigManager
	mu            sync.Mutex
}

// NewSaveCoordinator creates a new save coordinator with a shared ConfigManager.
func NewSaveCoordinator() *SaveCoordinator {
	cm, err := commands.NewConfigManager()
	if err != nil {
		// If we can't create a ConfigManager, log the error but continue
		// Settings will fail gracefully when they try to save
		logDebug("[SaveCoordinator] Failed to create ConfigManager: %v", err)
	}
	return &SaveCoordinator{
		configManager: cm,
	}
}

// GetConfigManager returns the shared ConfigManager instance.
// All settings MUST use this instance instead of creating their own.
func (sc *SaveCoordinator) GetConfigManager() *commands.ConfigManager {
	return sc.configManager
}

// ExecuteSave performs a save with proper locking to prevent race conditions.
// This is the ONLY way settings should persist changes.
func (sc *SaveCoordinator) ExecuteSave(sectionName string, updateFn func(*commands.SwarmOSConfig)) error {
	if sc.configManager == nil {
		err := fmt.Errorf("%s", i18n.T("settings.save.config_manager_uninitialized"))
		logDebug("[%s] Save failed: %v", sectionName, err)
		return err
	}

	// Lock to prevent concurrent saves to the same file
	sc.mu.Lock()
	defer sc.mu.Unlock()

	// Load current config
	cfg, err := sc.configManager.LoadConfig()
	if err != nil {
		cfg = &commands.SwarmOSConfig{}
		logDebug("[%s] Created new config (load failed: %v)", sectionName, err)
	}

	// Apply the update function
	updateFn(cfg)

	// Save the config
	if err := sc.configManager.SaveConfig(cfg); err != nil {
		logDebug("[%s] Save failed: %v", sectionName, err)
		return fmt.Errorf("%s: %w", i18n.T("settings.save.failed", sectionName), err)
	}

	logDebug("[%s] Settings saved successfully", sectionName)
	return nil
}

// BaseSettings provides the base implementation that ALL settings MUST embed.
// This enforces the strict save pattern and prevents custom implementations.
type BaseSettings struct {
	configManager *commands.ConfigManager
	sectionName   string
}

// SetConfigManager sets the shared ConfigManager.
// Called by Manager during initialization.
func (bs *BaseSettings) SetConfigManager(cm *commands.ConfigManager) {
	bs.configManager = cm
}

// GetConfigManager returns the ConfigManager (for use by embedded structs).
func (bs *BaseSettings) GetConfigManager() *commands.ConfigManager {
	return bs.configManager
}

// GetSectionName returns the section name.
func (bs *BaseSettings) GetSectionName() string {
	return bs.sectionName
}

// RequireConfigManager returns the ConfigManager or panics if not set.
// Use this in save methods to ensure ConfigManager is always available.
func (bs *BaseSettings) RequireConfigManager() *commands.ConfigManager {
	if bs.configManager == nil {
		panic(fmt.Sprintf("[%s] CRITICAL: ConfigManager not set. Settings must be initialized with SetConfigManager()", bs.sectionName))
	}
	return bs.configManager
}

// StrictSave is the ONLY allowed save pattern for all settings.
// ALL settings MUST use this function - no custom save logic allowed.
func StrictSave(sectionName string, cm *commands.ConfigManager, updateFn func(*commands.SwarmOSConfig)) error {
	if cm == nil {
		err := fmt.Errorf("%s", i18n.T("settings.save.config_manager_nil"))
		logDebug("[%s] Save failed: %v", sectionName, err)
		return err
	}

	// Load current config
	cfg, err := cm.LoadConfig()
	if err != nil {
		cfg = &commands.SwarmOSConfig{}
		logDebug("[%s] Created new config (load failed: %v)", sectionName, err)
	}

	// Apply the update function
	updateFn(cfg)

	// Save the config
	if err := cm.SaveConfig(cfg); err != nil {
		logDebug("[%s] Save failed: %v", sectionName, err)
		return fmt.Errorf("%s: %w", i18n.T("settings.save.failed", sectionName), err)
	}

	logDebug("[%s] Settings saved successfully", sectionName)
	return nil
}

// RegisterSettingsSaver registers a settings instance for centralized management.
// This is called by Manager when creating settings.
func RegisterSettingsSaver(saver SettingsSaver, cm *commands.ConfigManager) {
	if saver == nil {
		logDebug("[SaveCoordinator] Attempted to register nil settings saver")
		return
	}
	saver.SetConfigManager(cm)
	logDebug("[%s] Registered settings saver with ConfigManager", saver.GetSectionName())
}
