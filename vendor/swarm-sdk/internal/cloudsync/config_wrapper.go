package cloudsync

import (
	"context"

	"github.com/Swarm-Code/mono/swarm-core/core"
)

// ConfigSyncManager wraps a ConfigManager to trigger sync on changes.
type ConfigSyncManager struct {
	base    core.ConfigManager
	manager *Manager
}

// NewConfigSyncManager creates a new wrapper for config sync.
func NewConfigSyncManager(base core.ConfigManager, manager *Manager) *ConfigSyncManager {
	return &ConfigSyncManager{base: base, manager: manager}
}

func (c *ConfigSyncManager) Load(ctx context.Context) error {
	return c.base.Load(ctx)
}

func (c *ConfigSyncManager) Save(ctx context.Context) error {
	err := c.base.Save(ctx)
	if err == nil && c.manager != nil {
		c.manager.MarkSettingsDirty()
		c.manager.MarkProfilesDirty()
		c.manager.QueueSettingsSync()
		c.manager.QueueProfilesSync()
	}
	return err
}

func (c *ConfigSyncManager) GetConfig() *core.Config {
	return c.base.GetConfig()
}

func (c *ConfigSyncManager) SetConfig(cfg *core.Config) error {
	err := c.base.SetConfig(cfg)
	if err == nil && c.manager != nil {
		c.manager.MarkSettingsDirty()
		c.manager.QueueSettingsSync()
	}
	return err
}

func (c *ConfigSyncManager) GetProviders() []core.ProviderConfig {
	return c.base.GetProviders()
}

func (c *ConfigSyncManager) SetProvider(provider core.ProviderConfig) error {
	return c.base.SetProvider(provider)
}

func (c *ConfigSyncManager) DeleteProvider(name string) error {
	return c.base.DeleteProvider(name)
}

func (c *ConfigSyncManager) Credentials() *core.Credentials {
	return c.base.Credentials()
}

func (c *ConfigSyncManager) SetCredentials(creds *core.Credentials) error {
	return c.base.SetCredentials(creds)
}

func (c *ConfigSyncManager) GetHooks() *core.HookConfig {
	return c.base.GetHooks()
}

func (c *ConfigSyncManager) SetHooks(hooks *core.HookConfig) error {
	return c.base.SetHooks(hooks)
}

func (c *ConfigSyncManager) GetPermissionPolicies() *core.PermissionConfig {
	return c.base.GetPermissionPolicies()
}

func (c *ConfigSyncManager) SetPermissionPolicies(policies *core.PermissionConfig) error {
	err := c.base.SetPermissionPolicies(policies)
	if err == nil && c.manager != nil {
		c.manager.MarkSettingsDirty()
		c.manager.QueueSettingsSync()
	}
	return err
}

func (c *ConfigSyncManager) GetAgents() []core.AgentConfig {
	return c.base.GetAgents()
}

func (c *ConfigSyncManager) SetAgent(agent core.AgentConfig) error {
	return c.base.SetAgent(agent)
}

func (c *ConfigSyncManager) DeleteAgent(name string) error {
	return c.base.DeleteAgent(name)
}

func (c *ConfigSyncManager) GetProfiles() []core.AgentProfile {
	return c.base.GetProfiles()
}

func (c *ConfigSyncManager) SetProfile(profile core.AgentProfile) error {
	err := c.base.SetProfile(profile)
	if err == nil && c.manager != nil {
		c.manager.MarkProfilesDirty()
		c.manager.QueueProfilesSync()
	}
	return err
}

func (c *ConfigSyncManager) DeleteProfile(name string) error {
	err := c.base.DeleteProfile(name)
	if err == nil && c.manager != nil {
		c.manager.MarkProfilesDirty()
		c.manager.QueueProfilesSync()
	}
	return err
}

func (c *ConfigSyncManager) GetSystemPrompts() *core.SystemPromptsConfig {
	return c.base.GetSystemPrompts()
}

func (c *ConfigSyncManager) SetSystemPrompts(prompts *core.SystemPromptsConfig) error {
	return c.base.SetSystemPrompts(prompts)
}

func (c *ConfigSyncManager) GetContextSources() *core.ContextSourcesConfig {
	return c.base.GetContextSources()
}

func (c *ConfigSyncManager) SetContextSources(sources *core.ContextSourcesConfig) error {
	return c.base.SetContextSources(sources)
}

func (c *ConfigSyncManager) GetRenderSettings() *core.RenderSettings {
	return c.base.GetRenderSettings()
}

func (c *ConfigSyncManager) SetRenderSettings(settings *core.RenderSettings) error {
	err := c.base.SetRenderSettings(settings)
	if err == nil && c.manager != nil {
		c.manager.MarkSettingsDirty()
		c.manager.QueueSettingsSync()
	}
	return err
}

func (c *ConfigSyncManager) GetEnvironments() *core.EnvironmentsConfig {
	return c.base.GetEnvironments()
}

func (c *ConfigSyncManager) SetEnvironments(cfg *core.EnvironmentsConfig) error {
	err := c.base.SetEnvironments(cfg)
	if err == nil && c.manager != nil {
		c.manager.MarkSettingsDirty()
		c.manager.QueueSettingsSync()
	}
	return err
}

func (c *ConfigSyncManager) ConfigDir() string {
	return c.base.ConfigDir()
}

func (c *ConfigSyncManager) SharedConfigDir() string {
	return c.base.SharedConfigDir()
}
