package chat

import (
	"context"

	"github.com/Swarm-Code/mono/swarm-core/core"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/chat/commands"
)

// ============================================================================
// CONFIG HELPER METHODS
// ============================================================================
//
// These methods provide unified access to config through App.configBundle,
// ensuring all config reads respect global/project switching and cache invalidation.

// GetConfig returns the current active config.
// This is the preferred way to access config from App - it respects global/project switching.
func (a *App) GetConfig() *core.Config {
	if a.configBundle == nil {
		return core.DefaultConfig()
	}
	return a.configBundle.GetConfig()
}

// UpdateConfig loads the config, applies updates, and saves it back.
// This is the preferred way to modify config from App - it respects global/project switching.
func (a *App) UpdateConfig(update func(*core.Config)) error {
	if a.configBundle == nil {
		return nil
	}
	return a.configBundle.UpdateConfig(update)
}

// GetConfigBundle returns the config bundle integration for advanced use cases.
// Most code should use GetConfig/UpdateConfig instead.
func (a *App) GetConfigBundle() *commands.ConfigBundleIntegration {
	return a.configBundle
}

// SaveConfig saves the current config to disk.
func (a *App) SaveConfig(ctx context.Context) error {
	if a.configBundle == nil {
		return nil
	}
	return a.configBundle.Save(ctx)
}

// RefreshConfig reloads config from disk and invalidates cache.
func (a *App) RefreshConfig(ctx context.Context) error {
	if a.configBundle == nil {
		return nil
	}
	return a.configBundle.Refresh(ctx)
}

// IsUsingProjectConfig returns true if currently using project config.
func (a *App) IsUsingProjectConfig() bool {
	if a.configBundle == nil {
		return false
	}
	return a.configBundle.IsUsingProject()
}

// HasProjectConfig returns true if a project config exists.
func (a *App) HasProjectConfig() bool {
	if a.configBundle == nil {
		return false
	}
	return a.configBundle.HasProjectConfig()
}

// ToggleConfigSource switches between global and project config.
// Returns the new source ("project" or "global").
func (a *App) ToggleConfigSource() string {
	if a.configBundle == nil {
		return "global"
	}
	return a.configBundle.Toggle()
}

// GetConfigSource returns the current config source ("project" or "global").
func (a *App) GetConfigSource() string {
	if a.configBundle == nil {
		return "global"
	}
	return a.configBundle.Source()
}
