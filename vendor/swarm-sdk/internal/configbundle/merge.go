// Package configbundle provides a unified configuration system for Swarm.
package configbundle

import (
	"maps"
	"time"
)

// mergeConfigs merges a project config bundle on top of a global config bundle.
// The result is a new bundle that combines both according to the merge policy.
func (m *Manager) mergeConfigs(global, project *ConfigBundle) *ConfigBundle {
	if project == nil {
		return global
	}
	if global == nil {
		return project
	}

	// Get merge policy (use default if not specified)
	policy := project.MergePolicy
	if policy == nil {
		policy = DefaultMergePolicy()
	}

	// Start with a copy of global
	result := &ConfigBundle{
		SchemaVersion: global.SchemaVersion,
		Name:          global.Name,
		Description:   global.Description,
		CreatedAt:     global.CreatedAt,
		UpdatedAt:     time.Now(),
		Source:        SourceProject, // Merged config is considered project-level
		Path:          project.Path,
	}

	// Merge each section according to policy
	result.System = mergeSystemConfig(global.System, project.System, policy.System)
	result.Agents = mergeAgentsConfig(global.Agents, project.Agents, policy.Agents)
	result.Profiles = mergeProfilesConfig(global.Profiles, project.Profiles, policy.Profiles)
	result.Prompts = mergePromptsConfig(global.Prompts, project.Prompts, policy.Prompts)
	result.Tools = mergeToolsConfig(global.Tools, project.Tools, policy.Tools)
	result.Hooks = mergeHooksConfig(global.Hooks, project.Hooks, policy.Hooks)
	result.Skills = mergeSkillsConfig(global.Skills, project.Skills, policy.Skills)
	result.Providers = mergeProvidersConfig(global.Providers, project.Providers, policy.Providers)
	result.Credentials = mergeCredentialsConfig(global.Credentials, project.Credentials) // Always append
	result.MCPServers = mergeMCPServersConfig(global.MCPServers, project.MCPServers, policy.MCPServers)
	result.ContextSources = mergeContextSourcesConfig(global.ContextSources, project.ContextSources, policy.ContextSources)
	result.Environments = mergeEnvironmentsConfig(global.Environments, project.Environments, policy.Environments)

	return result
}

// mergeSystemConfig merges system settings.
func mergeSystemConfig(global, project SystemConfig, mode MergeMode) SystemConfig {
	if mode == MergeModeOverride {
		return project
	}

	// Merge mode: Override individual fields
	result := global

	// Provider/Model selection
	if project.DefaultProvider != "" {
		result.DefaultProvider = project.DefaultProvider
	}
	if project.DefaultModel != "" {
		result.DefaultModel = project.DefaultModel
	}
	if project.CurrentProvider != "" {
		result.CurrentProvider = project.CurrentProvider
	}
	if project.CurrentModel != "" {
		result.CurrentModel = project.CurrentModel
	}
	if project.DefaultMode != "" {
		result.DefaultMode = project.DefaultMode
	}
	if project.DefaultAgent != "" {
		result.DefaultAgent = project.DefaultAgent
	}

	// Display settings
	if project.Theme != "" {
		result.Theme = project.Theme
	}
	if project.ShowThinking != nil {
		result.ShowThinking = project.ShowThinking
	}
	if project.ShowTokenCount != nil {
		result.ShowTokenCount = project.ShowTokenCount
	}
	if project.ShowToolOutput != nil {
		result.ShowToolOutput = project.ShowToolOutput
	}
	if project.CompactMode != nil {
		result.CompactMode = project.CompactMode
	}
	if project.MaxOutputLines != 0 {
		result.MaxOutputLines = project.MaxOutputLines
	}
	if project.SyntaxHighlighting != nil {
		result.SyntaxHighlighting = project.SyntaxHighlighting
	}
	if project.MaxConcurrentTools != 0 {
		result.MaxConcurrentTools = project.MaxConcurrentTools
	}
	if project.ToolTimeout != 0 {
		result.ToolTimeout = project.ToolTimeout
	}
	if project.EnableSandbox != nil {
		result.EnableSandbox = project.EnableSandbox
	}
	if len(project.SandboxPaths) > 0 {
		result.SandboxPaths = append(result.SandboxPaths, project.SandboxPaths...)
	}

	// Editor settings
	if project.Editor != "" {
		result.Editor = project.Editor
	}
	if project.EditorArgs != "" {
		result.EditorArgs = project.EditorArgs
	}
	if project.ExternalEditorCmd != "" {
		result.ExternalEditorCmd = project.ExternalEditorCmd
	}

	// Behavior settings
	if project.AutoSaveConversations != nil {
		result.AutoSaveConversations = project.AutoSaveConversations
	}
	if project.ConfirmBeforeExit != nil {
		result.ConfirmBeforeExit = project.ConfirmBeforeExit
	}
	if project.EnableLogging != nil {
		result.EnableLogging = project.EnableLogging
	}
	if project.LogLevel != "" {
		result.LogLevel = project.LogLevel
	}

	// Compaction settings
	if project.EnableCompaction != nil {
		result.EnableCompaction = project.EnableCompaction
	}
	if project.CompactionThreshold != 0 {
		result.CompactionThreshold = project.CompactionThreshold
	}
	if project.WarningThreshold != 0 {
		result.WarningThreshold = project.WarningThreshold
	}
	if project.PreserveRecentMessages != 0 {
		result.PreserveRecentMessages = project.PreserveRecentMessages
	}

	// Micro-compaction settings
	if project.EnableMicroCompaction != nil {
		result.EnableMicroCompaction = project.EnableMicroCompaction
	}
	if project.MicroRetentionCount != 0 {
		result.MicroRetentionCount = project.MicroRetentionCount
	}

	// Cache settings
	if project.EnableCache != nil {
		result.EnableCache = project.EnableCache
	}
	if project.CacheDir != "" {
		result.CacheDir = project.CacheDir
	}
	if project.CacheMaxSizeMB != 0 {
		result.CacheMaxSizeMB = project.CacheMaxSizeMB
	}
	if project.CacheExpiryDays != 0 {
		result.CacheExpiryDays = project.CacheExpiryDays
	}

	// Cloud sync settings
	if project.SyncConversations != nil {
		result.SyncConversations = project.SyncConversations
	}
	if project.SyncSettings != nil {
		result.SyncSettings = project.SyncSettings
	}
	if project.SyncSettingsScope != "" {
		result.SyncSettingsScope = project.SyncSettingsScope
	}
	if project.SyncSettingsTeamID != "" {
		result.SyncSettingsTeamID = project.SyncSettingsTeamID
	}
	if project.SyncProfiles != nil {
		result.SyncProfiles = project.SyncProfiles
	}
	if project.EncryptCloudData != nil {
		result.EncryptCloudData = project.EncryptCloudData
	}
	if project.AnonymousAnalytics != nil {
		result.AnonymousAnalytics = project.AnonymousAnalytics
	}

	// Advanced features
	if project.HybridConfig != nil {
		result.HybridConfig = project.HybridConfig
	}
	if project.WebSearch != nil {
		result.WebSearch = project.WebSearch
	}
	if project.SteeringConfig != nil {
		result.SteeringConfig = project.SteeringConfig
	}

	// Voice settings
	if project.EnableVoice != nil {
		result.EnableVoice = project.EnableVoice
	}
	if project.VoiceProvider != "" {
		result.VoiceProvider = project.VoiceProvider
	}

	// Custom settings
	if len(project.Custom) > 0 {
		if result.Custom == nil {
			result.Custom = make(map[string]any)
		}
		maps.Copy(result.Custom, project.Custom)
	}

	return result
}

// mergeAgentsConfig merges agent configurations.
func mergeAgentsConfig(global, project AgentsConfig, mode MergeMode) AgentsConfig {
	if mode == MergeModeOverride {
		return project
	}

	// Merge mode: Combine definitions, project overrides global with same ID
	result := AgentsConfig{
		Definitions: make([]AgentDefinition, 0),
	}

	// Build map of global agents
	globalAgents := make(map[string]AgentDefinition)
	for _, a := range global.Definitions {
		globalAgents[a.ID] = a
	}

	// Add global agents first
	result.Definitions = append(result.Definitions, global.Definitions...)

	// Add/override with project agents
	for _, a := range project.Definitions {
		if _, exists := globalAgents[a.ID]; exists {
			// Override: find and replace
			for i, ga := range result.Definitions {
				if ga.ID == a.ID {
					result.Definitions[i] = a
					break
				}
			}
		} else {
			// New agent
			result.Definitions = append(result.Definitions, a)
		}
	}

	// Default agent from project takes precedence
	if project.DefaultAgent != "" {
		result.DefaultAgent = project.DefaultAgent
	} else {
		result.DefaultAgent = global.DefaultAgent
	}

	result.Mode = mode
	return result
}

// mergeProfilesConfig merges profile configurations.
func mergeProfilesConfig(global, project ProfilesConfig, mode MergeMode) ProfilesConfig {
	if mode == MergeModeOverride {
		return project
	}

	// Handle different modes
	switch project.Mode {
	case ProfileModeInline:
		// Project uses inline profiles, override global
		return project
	case ProfileModeReference:
		// Project uses external reference, just use project's config
		return project
	case ProfileModeMerge:
		// Explicit merge: combine inline profiles with global
		result := global
		if result.Mode == ProfileModeReference || result.Mode == "" {
			result.Mode = ProfileModeMerge
		}
		result.Inline = make([]ProfileDefinition, 0)

		// Build map of global profiles
		globalProfiles := make(map[string]ProfileDefinition)
		for _, p := range global.Inline {
			globalProfiles[p.ID] = p
		}

		// Add all global profiles
		result.Inline = append(result.Inline, global.Inline...)

		// Add/override with project profiles
		for _, p := range project.Inline {
			if _, exists := globalProfiles[p.ID]; exists {
				// Override
				for i, gp := range result.Inline {
					if gp.ID == p.ID {
						result.Inline[i] = p
						break
					}
				}
			} else {
				result.Inline = append(result.Inline, p)
			}
		}

		return result
	default:
		// Default merge behavior
		if len(project.Inline) > 0 {
			return mergeProfilesConfig(global, project, MergeModeMerge)
		}
		return global
	}
}

// mergePromptsConfig merges prompt configurations.
func mergePromptsConfig(global, project PromptsConfig, mode MergeMode) PromptsConfig {
	if mode == MergeModeOverride {
		return project
	}

	result := PromptsConfig{
		Custom:    make(map[string]string),
		Overrides: make(map[string]string),
	}

	// Copy global prompts
	maps.Copy(result.Custom, global.Custom)
	maps.Copy(result.Overrides, global.Overrides)

	// Override/add with project prompts
	maps.Copy(result.Custom, project.Custom)
	maps.Copy(result.Overrides, project.Overrides)

	result.Mode = mode
	return result
}

// mergeToolsConfig merges tool configurations.
func mergeToolsConfig(global, project ToolsConfig, mode MergeMode) ToolsConfig {
	if mode == MergeModeOverride {
		return project
	}

	result := ToolsConfig{
		Enabled:   make([]string, 0),
		Disabled:  make([]string, 0),
		Custom:    make([]ToolDefinition, 0),
		Overrides: make(map[string]ToolOverride),
	}

	// Merge enabled/disabled lists
	enabledSet := make(map[string]bool)
	disabledSet := make(map[string]bool)

	// Add global enabled
	for _, t := range global.Enabled {
		enabledSet[t] = true
	}
	// Add project enabled
	for _, t := range project.Enabled {
		enabledSet[t] = true
	}

	// Add global disabled
	for _, t := range global.Disabled {
		disabledSet[t] = true
	}
	// Add project disabled
	for _, t := range project.Disabled {
		disabledSet[t] = true
	}

	// Convert back to slices
	for t := range enabledSet {
		if !disabledSet[t] {
			result.Enabled = append(result.Enabled, t)
		}
	}
	for t := range disabledSet {
		result.Disabled = append(result.Disabled, t)
	}

	// Merge custom tools
	customTools := make(map[string]ToolDefinition)
	for _, t := range global.Custom {
		customTools[t.ID] = t
	}
	for _, t := range project.Custom {
		customTools[t.ID] = t // Override
	}
	for _, t := range customTools {
		result.Custom = append(result.Custom, t)
	}

	// Merge overrides
	maps.Copy(result.Overrides, global.Overrides)
	// Override
	maps.Copy(result.Overrides, project.Overrides)

	// Permission policy: project overrides global
	if project.PermissionPolicy != nil {
		result.PermissionPolicy = project.PermissionPolicy
	} else {
		result.PermissionPolicy = global.PermissionPolicy
	}

	result.Mode = mode
	return result
}

// mergeHooksConfig merges hook configurations.
func mergeHooksConfig(global, project HooksConfig, mode MergeMode) HooksConfig {
	if mode == MergeModeOverride {
		return project
	}

	// Default to append mode for hooks
	result := HooksConfig{
		Definitions: make([]HookDefinition, 0),
		Disabled:    make([]string, 0),
	}

	// Add all global hooks
	result.Definitions = append(result.Definitions, global.Definitions...)
	result.Disabled = append(result.Disabled, global.Disabled...)

	// Add all project hooks (append)
	result.Definitions = append(result.Definitions, project.Definitions...)
	result.Disabled = append(result.Disabled, project.Disabled...)

	result.Mode = mode
	return result
}

// mergeSkillsConfig merges skill configurations.
func mergeSkillsConfig(global, project SkillsConfig, mode MergeMode) SkillsConfig {
	if mode == MergeModeOverride {
		return project
	}

	// Default to append mode for skills
	result := SkillsConfig{
		Installed:  make([]SkillReference, 0),
		SearchPath: make([]string, 0),
	}

	// Add global skills
	result.Installed = append(result.Installed, global.Installed...)
	result.SearchPath = append(result.SearchPath, global.SearchPath...)

	// Add project skills
	result.Installed = append(result.Installed, project.Installed...)
	result.SearchPath = append(result.SearchPath, project.SearchPath...)

	result.Mode = mode
	return result
}

// mergeProvidersConfig merges provider configurations.
func mergeProvidersConfig(global, project ProvidersConfig, mode MergeMode) ProvidersConfig {
	if mode == MergeModeOverride {
		return project
	}

	// Handle different modes
	switch project.Mode {
	case ProviderModeInline:
		return project
	case ProviderModeReference:
		return project
	case ProviderModeMerge:
		result := global
		if result.Mode == ProviderModeReference || result.Mode == "" {
			result.Mode = ProviderModeMerge
		}
		result.Inline = make([]ProviderDefinition, 0)

		// Build map of global providers
		globalProviders := make(map[string]ProviderDefinition)
		for _, p := range global.Inline {
			globalProviders[p.ID] = p
		}

		// Add all global providers
		result.Inline = append(result.Inline, global.Inline...)

		// Add/override with project providers
		for _, p := range project.Inline {
			if _, exists := globalProviders[p.ID]; exists {
				// Override
				for i, gp := range result.Inline {
					if gp.ID == p.ID {
						result.Inline[i] = p
						break
					}
				}
			} else {
				result.Inline = append(result.Inline, p)
			}
		}

		return result
	default:
		if len(project.Inline) > 0 {
			return mergeProvidersConfig(global, project, MergeModeMerge)
		}
		return global
	}
}

// mergeCredentialsConfig merges credentials.
// SECURITY: Project credentials always APPEND to global, never replace.
func mergeCredentialsConfig(global, project CredentialsConfig) CredentialsConfig {
	result := CredentialsConfig{
		ProviderKeys: make(map[string]string),
		OAuthTokens:  make(map[string]string),
		MCPAuth:      make(map[string]MCPAuthConfig),
		Inherit:      true, // Always inherit
	}

	// Copy global credentials
	maps.Copy(result.ProviderKeys, global.ProviderKeys)
	maps.Copy(result.OAuthTokens, global.OAuthTokens)
	maps.Copy(result.MCPAuth, global.MCPAuth)

	// Add project credentials (cannot override global - this is security feature)
	for k, v := range project.ProviderKeys {
		// Only add if not already in global
		if _, exists := global.ProviderKeys[k]; !exists {
			result.ProviderKeys[k] = v
		}
		// If key exists in global, project cannot override it
	}
	for k, v := range project.OAuthTokens {
		if _, exists := global.OAuthTokens[k]; !exists {
			result.OAuthTokens[k] = v
		}
	}
	for k, v := range project.MCPAuth {
		if _, exists := global.MCPAuth[k]; !exists {
			result.MCPAuth[k] = v
		}
	}

	return result
}

// mergeMCPServersConfig merges MCP server configurations.
func mergeMCPServersConfig(global, project MCPServersConfig, mode MergeMode) MCPServersConfig {
	if mode == MergeModeOverride {
		return project
	}

	// Default to append mode for MCP servers
	result := MCPServersConfig{
		Servers:  make([]MCPServerDefinition, 0),
		Disabled: make([]string, 0),
	}

	// Add global servers
	result.Servers = append(result.Servers, global.Servers...)
	result.Disabled = append(result.Disabled, global.Disabled...)

	// Add project servers
	result.Servers = append(result.Servers, project.Servers...)
	result.Disabled = append(result.Disabled, project.Disabled...)

	result.Mode = mode
	return result
}

// mergeContextSourcesConfig merges context source configurations.
func mergeContextSourcesConfig(global, project ContextSourcesConfig, mode MergeMode) ContextSourcesConfig {
	if mode == MergeModeOverride {
		return project
	}

	result := ContextSourcesConfig{
		Sources: make([]ContextSourceDefinition, 0),
	}

	// Add global sources
	result.Sources = append(result.Sources, global.Sources...)

	// Add project sources
	result.Sources = append(result.Sources, project.Sources...)

	result.Mode = mode
	return result
}

// mergeEnvironmentsConfig merges environment configurations.
func mergeEnvironmentsConfig(global, project EnvironmentsConfig, mode MergeMode) EnvironmentsConfig {
	if mode == MergeModeOverride {
		return project
	}

	result := EnvironmentsConfig{
		Definitions: make([]EnvironmentDefinition, 0),
	}

	// Build map of global environments
	globalEnvs := make(map[string]EnvironmentDefinition)
	for _, e := range global.Definitions {
		globalEnvs[e.ID] = e
	}

	// Add global environments
	result.Definitions = append(result.Definitions, global.Definitions...)

	// Add/override with project environments
	for _, e := range project.Definitions {
		if _, exists := globalEnvs[e.ID]; exists {
			// Override
			for i, ge := range result.Definitions {
				if ge.ID == e.ID {
					result.Definitions[i] = e
					break
				}
			}
		} else {
			result.Definitions = append(result.Definitions, e)
		}
	}

	// Current environment from project takes precedence
	if project.Current != "" {
		result.Current = project.Current
	} else {
		result.Current = global.Current
	}

	return result
}
