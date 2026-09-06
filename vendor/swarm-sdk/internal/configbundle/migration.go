// Package configbundle provides a unified configuration system for Swarm.
package configbundle

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/Swarm-Code/mono/swarm-core/core"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/atomicfile"
)

// Migration handles converting legacy config files to the unified ConfigBundle format.
type Migration struct {
	configDir string // legacy config directory
}

// NewMigration creates a new Migration instance.
func NewMigration(configDir string) *Migration {
	return &Migration{configDir: configDir}
}

// MigrateToBundle migrates all legacy config files to a unified ConfigBundle.
func (m *Migration) MigrateToBundle(ctx context.Context, outputPath string) (*ConfigBundle, error) {
	bundle := &ConfigBundle{
		SchemaVersion: SchemaVersion,
		Name:          "Migrated Config",
		Description:   "Migrated from legacy config files",
		CreatedAt:     time.Now(),
		UpdatedAt:     time.Now(),
	}

	// Migrate each config file
	if err := m.migrateMainConfig(bundle); err != nil {
		return nil, fmt.Errorf("failed to migrate main config: %w", err)
	}

	if err := m.migrateProviders(bundle); err != nil {
		return nil, fmt.Errorf("failed to migrate providers: %w", err)
	}

	if err := m.migrateAgentProfiles(bundle); err != nil {
		return nil, fmt.Errorf("failed to migrate agent profiles: %w", err)
	}

	if err := m.migrateHooks(bundle); err != nil {
		return nil, fmt.Errorf("failed to migrate hooks: %w", err)
	}

	if err := m.migrateMCPServers(bundle); err != nil {
		return nil, fmt.Errorf("failed to migrate MCP servers: %w", err)
	}

	if err := m.migrateCredentials(bundle); err != nil {
		return nil, fmt.Errorf("failed to migrate credentials: %w", err)
	}

	if err := m.migrateSystemPrompts(bundle); err != nil {
		return nil, fmt.Errorf("failed to migrate system prompts: %w", err)
	}

	if err := m.migrateContextConfig(bundle); err != nil {
		return nil, fmt.Errorf("failed to migrate context config: %w", err)
	}

	// Save the bundle if output path specified
	if outputPath != "" {
		data, err := json.MarshalIndent(bundle, "", "  ")
		if err != nil {
			return nil, fmt.Errorf("failed to marshal bundle: %w", err)
		}
		if err := atomicfile.Write(outputPath, data); err != nil {
			return nil, fmt.Errorf("failed to write bundle: %w", err)
		}
	}

	return bundle, nil
}

// migrateMainConfig migrates config.json to bundle.System.
func (m *Migration) migrateMainConfig(bundle *ConfigBundle) error {
	configPath := filepath.Join(m.configDir, "config.json")
	data, err := os.ReadFile(configPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil // No config.json, skip
		}
		return err
	}

	var cfg core.Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return fmt.Errorf("failed to parse config.json: %w", err)
	}

	// Map core.Config to SystemConfig
	bundle.System = SystemConfig{
		DefaultProvider:        cfg.DefaultProvider,
		DefaultModel:           cfg.DefaultModel,
		CurrentProvider:        cfg.CurrentProvider,
		CurrentModel:           cfg.CurrentModel,
		DefaultMode:            cfg.DefaultMode,
		DefaultAgent:           cfg.DefaultAgent,
		Theme:                  cfg.Theme,
		ShowThinking:           &cfg.ShowThinking,
		ShowTokenCount:         &cfg.ShowTokenCount,
		ShowToolOutput:         &cfg.ShowToolOutput,
		CompactMode:            &cfg.CompactMode,
		MaxOutputLines:         cfg.MaxOutputLines,
		SyntaxHighlighting:     &cfg.SyntaxHighlighting,
		MaxConcurrentTools:     cfg.MaxConcurrentTools,
		ToolTimeout:            cfg.ToolTimeout,
		EnableSandbox:          &cfg.EnableSandbox,
		SandboxPaths:           cfg.SandboxPaths,
		Editor:                 cfg.Editor,
		EditorArgs:             cfg.EditorArgs,
		ExternalEditorCmd:      cfg.ExternalEditorCmd,
		AutoSaveConversations:  &cfg.AutoSaveConversations,
		ConfirmBeforeExit:      &cfg.ConfirmBeforeExit,
		EnableLogging:          &cfg.EnableLogging,
		LogLevel:               cfg.LogLevel,
		EnableCompaction:       &cfg.EnableCompaction,
		CompactionThreshold:    cfg.CompactionThreshold,
		WarningThreshold:       cfg.WarningThreshold,
		PreserveRecentMessages: cfg.PreserveRecentMessages,
		EnableMicroCompaction:  &cfg.EnableMicroCompaction,
		MicroRetentionCount:    cfg.MicroRetentionCount,
		EnableCache:            &cfg.EnableCache,
		CacheDir:               cfg.CacheDir,
		CacheMaxSizeMB:         cfg.CacheMaxSizeMB,
		CacheExpiryDays:        cfg.CacheExpiryDays,
		SyncConversations:      &cfg.SyncConversations,
		SyncSettings:           &cfg.SyncSettings,
		SyncSettingsScope:      cfg.SyncSettingsScope,
		SyncSettingsTeamID:     cfg.SyncSettingsTeamID,
		SyncProfiles:           &cfg.SyncProfiles,
		EncryptCloudData:       &cfg.EncryptCloudData,
		AnonymousAnalytics:     &cfg.AnonymousAnalytics,
		HybridConfig:           cfg.HybridConfig,
		WebSearch:              cfg.WebSearch,
		SteeringConfig:         cfg.SteeringConfig,
	}

	return nil
}

// migrateProviders migrates providers.json to bundle.Providers.
func (m *Migration) migrateProviders(bundle *ConfigBundle) error {
	providersPath := filepath.Join(m.configDir, "providers.json")
	data, err := os.ReadFile(providersPath)
	if err != nil {
		if os.IsNotExist(err) {
			// Use reference mode if no providers.json
			bundle.Providers = ProvidersConfig{
				Mode:          ProviderModeReference,
				ReferencePath: "providers.json",
			}
			return nil
		}
		return err
	}

	var providerConfigs []core.ProviderConfig
	if err := json.Unmarshal(data, &providerConfigs); err != nil {
		return fmt.Errorf("failed to parse providers.json: %w", err)
	}

	// Convert to ProviderDefinition
	definitions := make([]ProviderDefinition, 0, len(providerConfigs))
	for _, pc := range providerConfigs {
		enabled := pc.Enabled
		def := ProviderDefinition{
			ID:      pc.Name,
			Name:    pc.Name,
			Type:    pc.APIType, // Use APIType as the provider type
			Enabled: &enabled,
			BaseURL: pc.BaseURL,
			Config:  pc.Options,
		}

		// Convert models
		if len(pc.Models) > 0 {
			def.Models = make([]ModelDefinition, 0, len(pc.Models))
			for _, mc := range pc.Models {
				vision := mc.Vision
				def.Models = append(def.Models, ModelDefinition{
					ID:              mc.ID,
					Name:            mc.Name,
					ContextWindow:   mc.ContextWindow,
					MaxOutputTokens: mc.MaxOutput,
					SupportsVision:  &vision,
				})
			}
		}

		definitions = append(definitions, def)
	}

	bundle.Providers = ProvidersConfig{
		Mode:   ProviderModeInline,
		Inline: definitions,
	}

	return nil
}

// migrateAgentProfiles migrates agent_profiles.json to bundle.Profiles.
func (m *Migration) migrateAgentProfiles(bundle *ConfigBundle) error {
	profilesPath := filepath.Join(m.configDir, "agent_profiles.json")
	data, err := os.ReadFile(profilesPath)
	if err != nil {
		if os.IsNotExist(err) {
			// Use reference mode if no agent_profiles.json
			bundle.Profiles = ProfilesConfig{
				Mode:          ProfileModeReference,
				ReferencePath: "agent_profiles.json",
			}
			return nil
		}
		return err
	}

	var profiles []core.AgentProfile
	if err := json.Unmarshal(data, &profiles); err != nil {
		return fmt.Errorf("failed to parse agent_profiles.json: %w", err)
	}

	// Convert to ProfileDefinition
	definitions := make([]ProfileDefinition, 0, len(profiles))
	for _, p := range profiles {
		temp := p.Temperature
		def := ProfileDefinition{
			ID:            p.Name, // Use Name as ID since AgentProfile doesn't have ID
			Name:          p.Name,
			Description:   p.Description,
			SystemPrompt:  p.SystemPrompt,
			Temperature:   &temp,
			MaxTokens:     p.MaxTokens,
			Tools:         p.Tools,
			DisabledTools: p.DisabledTools,
		}
		definitions = append(definitions, def)
	}

	bundle.Profiles = ProfilesConfig{
		Mode:   ProfileModeInline,
		Inline: definitions,
	}

	return nil
}

// migrateHooks migrates hooks.json to bundle.Hooks.
func (m *Migration) migrateHooks(bundle *ConfigBundle) error {
	hooksPath := filepath.Join(m.configDir, "hooks.json")
	data, err := os.ReadFile(hooksPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil // No hooks.json, skip
		}
		return err
	}

	var hookConfig core.HookConfig
	if err := json.Unmarshal(data, &hookConfig); err != nil {
		return fmt.Errorf("failed to parse hooks.json: %w", err)
	}

	// Convert to HookDefinition
	definitions := make([]HookDefinition, 0, len(hookConfig.Hooks))
	for _, h := range hookConfig.Hooks {
		enabled := h.Enabled
		def := HookDefinition{
			ID:      h.Name, // Use Name as ID since Hook doesn't have ID
			Name:    h.Name,
			Event:   h.Event,
			Command: h.Command,
			Timeout: h.Timeout,
			Enabled: &enabled,
		}
		// Convert environment slice to map (use slice values as keys with empty value)
		if len(h.Environment) > 0 {
			def.Environment = make(map[string]string)
			for _, env := range h.Environment {
				def.Environment[env] = ""
			}
		}
		definitions = append(definitions, def)
	}

	bundle.Hooks = HooksConfig{
		Definitions: definitions,
	}

	return nil
}

// migrateMCPServers migrates mcp_servers.json to bundle.MCPServers.
func (m *Migration) migrateMCPServers(bundle *ConfigBundle) error {
	mcpPath := filepath.Join(m.configDir, "mcp_servers.json")
	data, err := os.ReadFile(mcpPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil // No mcp_servers.json, skip
		}
		return err
	}

	// MCP servers have their own format, try to parse it
	var mcpConfig struct {
		Servers []struct {
			Name    string            `json:"name"`
			Type    string            `json:"type"`
			Enabled bool              `json:"enabled"`
			Command string            `json:"command"`
			Args    []string          `json:"args"`
			Env     map[string]string `json:"env"`
			URL     string            `json:"url"`
			Timeout int               `json:"timeout"`
		} `json:"servers"`
	}

	if err := json.Unmarshal(data, &mcpConfig); err != nil {
		return fmt.Errorf("failed to parse mcp_servers.json: %w", err)
	}

	// Convert to MCPServerDefinition
	definitions := make([]MCPServerDefinition, 0, len(mcpConfig.Servers))
	for _, s := range mcpConfig.Servers {
		enabled := s.Enabled
		def := MCPServerDefinition{
			ID:      s.Name,
			Name:    s.Name,
			Type:    s.Type,
			Enabled: &enabled,
			Command: s.Command,
			Args:    s.Args,
			Env:     s.Env,
			URL:     s.URL,
			Timeout: s.Timeout,
		}
		definitions = append(definitions, def)
	}

	bundle.MCPServers = MCPServersConfig{
		Servers: definitions,
	}

	return nil
}

// migrateCredentials migrates credentials.json to bundle.Credentials.
func (m *Migration) migrateCredentials(bundle *ConfigBundle) error {
	credsPath := filepath.Join(m.configDir, "credentials.json")
	data, err := os.ReadFile(credsPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil // No credentials.json, skip
		}
		return err
	}

	var creds core.Credentials
	if err := json.Unmarshal(data, &creds); err != nil {
		return fmt.Errorf("failed to parse credentials.json: %w", err)
	}

	// Convert core.Credentials to CredentialsConfig
	providerKeys := make(map[string]string)
	for providerName, cred := range creds.Providers {
		providerKeys[providerName] = cred.APIKey
	}

	mcpAuth := make(map[string]MCPAuthConfig)
	for serverName, cred := range creds.MCP {
		mcpAuth[serverName] = MCPAuthConfig{
			Type:    "bearer",
			Token:   cred.Token,
			Headers: cred.Headers,
		}
	}

	bundle.Credentials = CredentialsConfig{
		ProviderKeys: providerKeys,
		MCPAuth:      mcpAuth,
		Inherit:      true,
	}

	return nil
}

// migrateSystemPrompts migrates system_prompts.json to bundle.Prompts.
func (m *Migration) migrateSystemPrompts(bundle *ConfigBundle) error {
	promptsPath := filepath.Join(m.configDir, "system_prompts.json")
	data, err := os.ReadFile(promptsPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil // No system_prompts.json, skip
		}
		return err
	}

	var prompts core.SystemPromptsConfig
	if err := json.Unmarshal(data, &prompts); err != nil {
		return fmt.Errorf("failed to parse system_prompts.json: %w", err)
	}

	// Convert SystemPromptEntry to custom prompts map
	customPrompts := make(map[string]string)
	for _, p := range prompts.Prompts {
		customPrompts[p.Name] = p.Content
	}

	bundle.Prompts = PromptsConfig{
		Custom:    customPrompts,
		Overrides: prompts.ModePrompts,
	}

	return nil
}

// migrateContextConfig migrates context_config.json to bundle.ContextSources.
func (m *Migration) migrateContextConfig(bundle *ConfigBundle) error {
	contextPath := filepath.Join(m.configDir, "context_config.json")
	data, err := os.ReadFile(contextPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil // No context_config.json, skip
		}
		return err
	}

	var contextConfig core.ContextSourcesConfig
	if err := json.Unmarshal(data, &contextConfig); err != nil {
		return fmt.Errorf("failed to parse context_config.json: %w", err)
	}

	// Convert to ContextSourceDefinition
	sources := make([]ContextSourceDefinition, 0, len(contextConfig.Sources))
	for _, s := range contextConfig.Sources {
		enabled := s.Enabled
		sources = append(sources, ContextSourceDefinition{
			ID:      s.Name, // Use Name as ID
			Name:    s.Name,
			Type:    s.Type,
			Path:    s.Path,
			URL:     s.URL,
			Enabled: &enabled,
		})
	}

	bundle.ContextSources = ContextSourcesConfig{
		Sources: sources,
	}

	return nil
}

// MigrateProjectConfig creates a project config bundle from an existing
// project-local .swarm directory (legacy loose config files).
func (m *Migration) MigrateProjectConfig(projectDir string) (*ConfigBundle, error) {
	projectConfigDir := filepath.Join(projectDir, ".swarm")
	if _, err := os.Stat(projectConfigDir); os.IsNotExist(err) {
		return nil, ErrConfigNotFound
	}

	bundle := &ConfigBundle{
		SchemaVersion: SchemaVersion,
		Name:          filepath.Base(projectDir),
		Description:   "Migrated from project .swarm directory",
		CreatedAt:     time.Now(),
		UpdatedAt:     time.Now(),
		Source:        SourceProject,
		MergePolicy:   DefaultMergePolicy(),
	}

	// Run migrations on the project .swarm directory
	mig := NewMigration(projectConfigDir)
	if err := mig.migrateAgentProfiles(bundle); err != nil {
		return nil, err
	}
	if err := mig.migrateHooks(bundle); err != nil {
		return nil, err
	}
	if err := mig.migrateMCPServers(bundle); err != nil {
		return nil, err
	}
	if err := mig.migrateSystemPrompts(bundle); err != nil {
		return nil, err
	}
	if err := mig.migrateContextConfig(bundle); err != nil {
		return nil, err
	}

	// Project config should not include credentials by default
	bundle.Credentials = CredentialsConfig{
		Inherit: true,
	}

	return bundle, nil
}

// NeedsMigration checks if the config directory needs migration to bundle format.
func (m *Migration) NeedsMigration() bool {
	// Check for config.json (new format)
	bundlePath := filepath.Join(m.configDir, "config_bundle.json")
	if _, err := os.Stat(bundlePath); err == nil {
		return false // Already has bundle
	}

	// Check for legacy files
	legacyFiles := []string{
		"config.json",
		"providers.json",
		"agent_profiles.json",
		"hooks.json",
		"mcp_servers.json",
		"credentials.json",
	}

	for _, f := range legacyFiles {
		if _, err := os.Stat(filepath.Join(m.configDir, f)); err == nil {
			return true // Has legacy file, needs migration
		}
	}

	return false
}
