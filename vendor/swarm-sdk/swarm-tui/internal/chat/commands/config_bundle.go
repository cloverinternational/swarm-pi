// Package commands handles the command interface for the chat TUI.
// This file provides integration between the existing ConfigManager and the new
// configbundle system for unified global/project config management.
package commands

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"

	"github.com/Swarm-Code/mono/swarm-core/core"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/configbundle"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/i18n"
)

// ConfigBundleIntegration bridges the existing ConfigManager with the new ConfigBundle system.
// It provides project config detection, switching, and synchronization.
type ConfigBundleIntegration struct {
	mu sync.RWMutex

	// legacyManager is the existing TUI ConfigManager
	legacyManager *ConfigManager

	// bundleManager is the new unified config bundle manager
	bundleManager *configbundle.Manager

	// detector handles project config detection
	detector *configbundle.Detector

	// callbacks for config source changes
	onSourceChange []func(source string, name string)
}

// ConfigBundleIntegrationOptions contains options for creating the integration.
type ConfigBundleIntegrationOptions struct {
	WorkDir string
}

// NewConfigBundleIntegration creates a new integration between legacy and bundle configs.
func NewConfigBundleIntegration(ctx context.Context, opts ConfigBundleIntegrationOptions) (*ConfigBundleIntegration, error) {
	// Create legacy manager
	legacyMgr, err := NewConfigManager()
	if err != nil {
		return nil, fmt.Errorf("failed to create legacy config manager: %w", err)
	}

	// Create bundle manager.
	//
	// IMPORTANT: the bundle MUST NOT share the legacy global config file.
	// configbundle's default global path is ~/.swarmos/config.json — the exact
	// file the legacy ConfigManager (and every Settings section, the startup
	// model loader, and the compaction engine) reads and writes. The two use
	// incompatible JSON schemas (flat snake_case SwarmOSConfig vs. nested
	// camelCase ConfigBundle), so a single bundle save used to rewrite
	// config.json in bundle format and silently reset model / compaction /
	// auto-compaction / reasoning / recents back to defaults. Point the bundle
	// at its own file so the legacy config.json has exactly one writer/format.
	bundleMgr, err := configbundle.NewManager(ctx, configbundle.Options{
		WorkDir:    opts.WorkDir,
		GlobalPath: legacyMgr.GetConfigPath("bundle.json"),
		AutoSwitch: true, // Auto-switch to project config if found
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create bundle manager: %w", err)
	}

	// Create detector for project config changes
	detector := configbundle.NewDetector(configbundle.DetectorOptions{})

	integration := &ConfigBundleIntegration{
		legacyManager:  legacyMgr,
		bundleManager:  bundleMgr,
		detector:       detector,
		onSourceChange: make([]func(string, string), 0),
	}

	// Set up detector callbacks
	detector.OnDetect(func(info *configbundle.ProjectConfigInfo) {
		integration.handleProjectDetected(info)
	})
	detector.OnLost(func() {
		integration.handleProjectLost()
	})

	// Start monitoring if work directory provided
	if opts.WorkDir != "" {
		detector.Start(ctx, opts.WorkDir)
	}

	return integration, nil
}

// Legacy returns the legacy ConfigManager for backward compatibility.
// Deprecated: Use GetConfig/UpdateConfig instead.
func (i *ConfigBundleIntegration) Legacy() *ConfigManager {
	return i.legacyManager
}

// Bundle returns the ConfigBundle manager.
func (i *ConfigBundleIntegration) Bundle() *configbundle.Manager {
	return i.bundleManager
}

// GetConfig returns the current active config as core.Config.
// This is the preferred way to access config - it respects global/project switching.
func (i *ConfigBundleIntegration) GetConfig() *core.Config {
	i.mu.RLock()
	defer i.mu.RUnlock()

	// Get the active bundle
	active := i.bundleManager.Active()
	var cfg *core.Config
	if active == nil {
		cfg = core.DefaultConfig()
	} else {
		// Convert ConfigBundle's SystemConfig to core.Config
		cfg = systemConfigToCoreConfig(&active.System)
	}

	// The legacy config.json is authoritative for the fields it shares with the
	// Settings UI / compaction engine; overlay them so readers (sidepanel,
	// handlers) see the real persisted values rather than bundle defaults.
	i.overlayLegacySharedFields(cfg)
	return cfg
}

// UpdateConfig loads the config, applies updates, and saves it back.
// This is the preferred way to modify config - it respects global/project switching.
func (i *ConfigBundleIntegration) UpdateConfig(update func(*core.Config)) error {
	i.mu.Lock()
	defer i.mu.Unlock()

	// Get current config (inline logic from GetConfig to avoid reentrant lock)
	var cfg *core.Config
	active := i.bundleManager.Active()
	if active == nil {
		cfg = core.DefaultConfig()
	} else {
		cfg = systemConfigToCoreConfig(&active.System)
	}

	// Overlay the legacy-authoritative shared fields BEFORE applying the update,
	// so the caller mutates current values and we never round-trip a stale
	// bundle snapshot back over the user's real settings.
	i.overlayLegacySharedFields(cfg)

	// Apply updates
	update(cfg)

	// Convert back and save the bundle (to its own file, not config.json).
	if active != nil {
		coreConfigToSystemConfig(cfg, &active.System)
	}
	if err := i.bundleManager.Save(context.Background()); err != nil {
		return err
	}

	// Mirror the shared fields into the legacy config.json so the startup model
	// loader and the compaction engine — which read the flat config — observe
	// the change. Without this, e.g. toggling code mode would write only the
	// bundle and the engine would never see it.
	return i.applySharedFieldsToLegacy(cfg)
}

// overlayLegacySharedFields copies the fields that exist in BOTH the flat
// legacy SwarmOSConfig and core.Config — and which the Settings UI, startup
// model loader, and compaction engine read from the legacy config.json — from
// the legacy config onto cfg. The legacy file is the single source of truth for
// these; the bundle only mirrors them so its own readers stay consistent.
func (i *ConfigBundleIntegration) overlayLegacySharedFields(cfg *core.Config) {
	if i == nil || i.legacyManager == nil || cfg == nil {
		return
	}
	lc, err := i.legacyManager.LoadConfig()
	if err != nil || lc == nil {
		return
	}
	if lc.CurrentProvider != "" {
		cfg.CurrentProvider = lc.CurrentProvider
	}
	if lc.CurrentModel != "" {
		cfg.CurrentModel = lc.CurrentModel
	}
	if lc.MemoryBackend != "" {
		cfg.MemoryBackend = lc.MemoryBackend
	}
	if lc.MicroRetentionCount > 0 {
		cfg.MicroRetentionCount = lc.MicroRetentionCount
	}
	// Bools cannot encode "unset"; legacy is authoritative, so copy directly.
	cfg.EnableMicroCompaction = lc.EnableMicroCompaction
	cfg.EnableCodeMode = lc.EnableCodeMode
	cfg.CompletionConfirm = lc.CompletionConfirm
	cfg.CompletionConfirmMax = lc.CompletionConfirmMax
	cfg.ProactiveSummarizeThreshold = lc.ProactiveSummarizeThreshold
}

// applySharedFieldsToLegacy persists the shared fields from cfg back into the
// legacy config.json via a load-modify-save (which preserves every other legacy
// field). This keeps config.json — read by startup and the compaction engine —
// in sync with bundle-routed mutations (model switch, code-mode, micro-compaction,
// memory backend).
func (i *ConfigBundleIntegration) applySharedFieldsToLegacy(cfg *core.Config) error {
	if i == nil || i.legacyManager == nil || cfg == nil {
		return nil
	}
	_, err := i.legacyManager.UpdateConfig(func(lc *SwarmOSConfig) {
		if cfg.CurrentProvider != "" {
			lc.CurrentProvider = cfg.CurrentProvider
		}
		if cfg.CurrentModel != "" {
			lc.CurrentModel = cfg.CurrentModel
		}
		if cfg.MemoryBackend != "" {
			lc.MemoryBackend = cfg.MemoryBackend
		}
		if cfg.MicroRetentionCount > 0 {
			lc.MicroRetentionCount = cfg.MicroRetentionCount
		}
		lc.EnableMicroCompaction = cfg.EnableMicroCompaction
		lc.EnableCodeMode = cfg.EnableCodeMode
		lc.CompletionConfirm = cfg.CompletionConfirm
		lc.CompletionConfirmMax = cfg.CompletionConfirmMax
		lc.ProactiveSummarizeThreshold = cfg.ProactiveSummarizeThreshold
	})
	return err
}

// systemConfigToCoreConfig converts a configbundle.SystemConfig to core.Config.
func systemConfigToCoreConfig(sys *configbundle.SystemConfig) *core.Config {
	cfg := core.DefaultConfig()

	if sys.DefaultProvider != "" {
		cfg.DefaultProvider = sys.DefaultProvider
	}
	if sys.DefaultModel != "" {
		cfg.DefaultModel = sys.DefaultModel
	}
	if sys.CurrentProvider != "" {
		cfg.CurrentProvider = sys.CurrentProvider
	}
	if sys.CurrentModel != "" {
		cfg.CurrentModel = sys.CurrentModel
	}
	if sys.DefaultMode != "" {
		cfg.DefaultMode = sys.DefaultMode
	}
	if sys.Theme != "" {
		cfg.Theme = sys.Theme
	}
	if sys.ShowThinking != nil {
		cfg.ShowThinking = *sys.ShowThinking
	}
	if sys.ShowTokenCount != nil {
		cfg.ShowTokenCount = *sys.ShowTokenCount
	}
	if sys.ShowToolOutput != nil {
		cfg.ShowToolOutput = *sys.ShowToolOutput
	}
	if sys.CompactMode != nil {
		cfg.CompactMode = *sys.CompactMode
	}
	if sys.MaxOutputLines > 0 {
		cfg.MaxOutputLines = sys.MaxOutputLines
	}
	if sys.SyntaxHighlighting != nil {
		cfg.SyntaxHighlighting = *sys.SyntaxHighlighting
	}
	if sys.MaxConcurrentTools > 0 {
		cfg.MaxConcurrentTools = sys.MaxConcurrentTools
	}
	if sys.ToolTimeout > 0 {
		cfg.ToolTimeout = sys.ToolTimeout
	}
	if sys.EnableSandbox != nil {
		cfg.EnableSandbox = *sys.EnableSandbox
	}
	if len(sys.SandboxPaths) > 0 {
		cfg.SandboxPaths = sys.SandboxPaths
	}
	if sys.Editor != "" {
		cfg.Editor = sys.Editor
	}
	if sys.EditorArgs != "" {
		cfg.EditorArgs = sys.EditorArgs
	}
	if sys.AutoSaveConversations != nil {
		cfg.AutoSaveConversations = *sys.AutoSaveConversations
	}
	if sys.ConfirmBeforeExit != nil {
		cfg.ConfirmBeforeExit = *sys.ConfirmBeforeExit
	}
	if sys.EnableLogging != nil {
		cfg.EnableLogging = *sys.EnableLogging
	}
	if sys.LogLevel != "" {
		cfg.LogLevel = sys.LogLevel
	}
	if sys.EnableCompaction != nil {
		cfg.EnableCompaction = *sys.EnableCompaction
	}
	if sys.CompactionThreshold > 0 {
		cfg.CompactionThreshold = sys.CompactionThreshold
	}
	if sys.WarningThreshold > 0 {
		cfg.WarningThreshold = sys.WarningThreshold
	}
	if sys.PreserveRecentMessages > 0 {
		cfg.PreserveRecentMessages = sys.PreserveRecentMessages
	}
	if sys.EnableMicroCompaction != nil {
		cfg.EnableMicroCompaction = *sys.EnableMicroCompaction
	}
	if sys.MicroRetentionCount > 0 {
		cfg.MicroRetentionCount = sys.MicroRetentionCount
	}
	if sys.EnableCodeMode != nil {
		cfg.EnableCodeMode = *sys.EnableCodeMode
	}
	if sys.CompletionConfirm != nil {
		cfg.CompletionConfirm = *sys.CompletionConfirm
	}
	if sys.CompletionConfirmMax != nil {
		cfg.CompletionConfirmMax = *sys.CompletionConfirmMax
	}
	if sys.ProactiveSummarizeThreshold != nil {
		cfg.ProactiveSummarizeThreshold = *sys.ProactiveSummarizeThreshold
	}
	if sys.MemoryBackend != "" {
		cfg.MemoryBackend = sys.MemoryBackend
	}
	if sys.EnableCache != nil {
		cfg.EnableCache = *sys.EnableCache
	}
	if sys.CacheDir != "" {
		cfg.CacheDir = sys.CacheDir
	}
	if sys.CacheMaxSizeMB > 0 {
		cfg.CacheMaxSizeMB = sys.CacheMaxSizeMB
	}
	if sys.CacheExpiryDays > 0 {
		cfg.CacheExpiryDays = sys.CacheExpiryDays
	}
	if sys.SyncConversations != nil {
		cfg.SyncConversations = *sys.SyncConversations
	}
	if sys.SyncSettings != nil {
		cfg.SyncSettings = *sys.SyncSettings
	}
	if sys.SyncSettingsScope != "" {
		cfg.SyncSettingsScope = sys.SyncSettingsScope
	}
	if sys.SyncSettingsTeamID != "" {
		cfg.SyncSettingsTeamID = sys.SyncSettingsTeamID
	}
	if sys.SyncProfiles != nil {
		cfg.SyncProfiles = *sys.SyncProfiles
	}
	if sys.EncryptCloudData != nil {
		cfg.EncryptCloudData = *sys.EncryptCloudData
	}
	if sys.AnonymousAnalytics != nil {
		cfg.AnonymousAnalytics = *sys.AnonymousAnalytics
	}
	// Note: HybridConfig, WebSearch, and SteeringConfig are handled directly
	// via configbundle using swarm-core/core types. They are not copied here
	// to avoid type conflicts between packages.

	return cfg
}

// coreConfigToSystemConfig converts a core.Config to configbundle.SystemConfig.
func coreConfigToSystemConfig(cfg *core.Config, sys *configbundle.SystemConfig) {
	if cfg == nil || sys == nil {
		return
	}

	sys.DefaultProvider = cfg.DefaultProvider
	sys.DefaultModel = cfg.DefaultModel
	sys.CurrentProvider = cfg.CurrentProvider
	sys.CurrentModel = cfg.CurrentModel
	sys.DefaultMode = cfg.DefaultMode
	sys.Theme = cfg.Theme
	sys.ShowThinking = &cfg.ShowThinking
	sys.ShowTokenCount = &cfg.ShowTokenCount
	sys.ShowToolOutput = &cfg.ShowToolOutput
	sys.CompactMode = &cfg.CompactMode
	sys.MaxOutputLines = cfg.MaxOutputLines
	sys.SyntaxHighlighting = &cfg.SyntaxHighlighting
	sys.MaxConcurrentTools = cfg.MaxConcurrentTools
	sys.ToolTimeout = cfg.ToolTimeout
	sys.EnableSandbox = &cfg.EnableSandbox
	sys.SandboxPaths = cfg.SandboxPaths
	sys.Editor = cfg.Editor
	sys.EditorArgs = cfg.EditorArgs
	sys.AutoSaveConversations = &cfg.AutoSaveConversations
	sys.ConfirmBeforeExit = &cfg.ConfirmBeforeExit
	sys.EnableLogging = &cfg.EnableLogging
	sys.LogLevel = cfg.LogLevel
	sys.EnableCompaction = &cfg.EnableCompaction
	sys.CompactionThreshold = cfg.CompactionThreshold
	sys.WarningThreshold = cfg.WarningThreshold
	sys.PreserveRecentMessages = cfg.PreserveRecentMessages
	sys.EnableMicroCompaction = &cfg.EnableMicroCompaction
	sys.MicroRetentionCount = cfg.MicroRetentionCount
	sys.EnableCodeMode = &cfg.EnableCodeMode
	sys.CompletionConfirm = &cfg.CompletionConfirm
	sys.CompletionConfirmMax = &cfg.CompletionConfirmMax
	sys.ProactiveSummarizeThreshold = &cfg.ProactiveSummarizeThreshold
	sys.MemoryBackend = cfg.MemoryBackend
	sys.EnableCache = &cfg.EnableCache
	sys.CacheDir = cfg.CacheDir
	sys.CacheMaxSizeMB = cfg.CacheMaxSizeMB
	sys.CacheExpiryDays = cfg.CacheExpiryDays
	sys.SyncConversations = &cfg.SyncConversations
	sys.SyncSettings = &cfg.SyncSettings
	sys.SyncSettingsScope = cfg.SyncSettingsScope
	sys.SyncSettingsTeamID = cfg.SyncSettingsTeamID
	sys.SyncProfiles = &cfg.SyncProfiles
	sys.EncryptCloudData = &cfg.EncryptCloudData
	sys.AnonymousAnalytics = &cfg.AnonymousAnalytics
	// Note: HybridConfig, WebSearch, and SteeringConfig are handled directly
	// via configbundle using swarm-core/core types.
}

// IsUsingProject returns true if currently using project config.
func (i *ConfigBundleIntegration) IsUsingProject() bool {
	i.mu.RLock()
	defer i.mu.RUnlock()
	return i.bundleManager.IsUsingProject()
}

// HasProjectConfig returns true if a project config exists.
func (i *ConfigBundleIntegration) HasProjectConfig() bool {
	i.mu.RLock()
	defer i.mu.RUnlock()
	return i.bundleManager.HasProjectConfig()
}

// ProjectName returns the name of the current project config.
func (i *ConfigBundleIntegration) ProjectName() string {
	i.mu.RLock()
	defer i.mu.RUnlock()
	return i.bundleManager.ProjectName()
}

// ProjectPath returns the path to the project config.
func (i *ConfigBundleIntegration) ProjectPath() string {
	i.mu.RLock()
	defer i.mu.RUnlock()
	return i.bundleManager.ProjectPath()
}

// GlobalPath returns the path to the global config.
func (i *ConfigBundleIntegration) GlobalPath() string {
	i.mu.RLock()
	defer i.mu.RUnlock()
	return i.bundleManager.GlobalPath()
}

// Source returns the current config source ("global" or "project").
func (i *ConfigBundleIntegration) Source() string {
	i.mu.RLock()
	defer i.mu.RUnlock()
	if i.bundleManager.IsUsingProject() {
		return "project"
	}
	return "global"
}

// SourceDisplayName returns a human-readable name for the current config source.
func (i *ConfigBundleIntegration) SourceDisplayName() string {
	i.mu.RLock()
	defer i.mu.RUnlock()
	if i.bundleManager.IsUsingProject() {
		name := i.bundleManager.ProjectName()
		if name == "" {
			name = i18n.T("commands.config_bundle.project")
		}
		return "📦 " + name
	}
	return i18n.T("commands.config_bundle.global_display")
}

// UseProject switches to using project config.
func (i *ConfigBundleIntegration) UseProject() error {
	i.mu.Lock()
	defer i.mu.Unlock()

	if err := i.bundleManager.UseProject(); err != nil {
		return err
	}

	// Invalidate legacy cache so subsequent LoadConfig reads from project config
	InvalidateConfigCache()

	// Notify listeners
	name := i.bundleManager.ProjectName()
	for _, cb := range i.onSourceChange {
		cb("project", name)
	}

	return nil
}

// UseGlobal switches to using global config.
func (i *ConfigBundleIntegration) UseGlobal() {
	i.mu.Lock()
	defer i.mu.Unlock()

	i.bundleManager.UseGlobal()

	// Invalidate legacy cache so subsequent LoadConfig reads from global config
	InvalidateConfigCache()

	// Notify listeners
	for _, cb := range i.onSourceChange {
		cb("global", "Global")
	}
}

// Toggle switches between global and project config.
// Returns the new source ("project" or "global").
func (i *ConfigBundleIntegration) Toggle() string {
	i.mu.Lock()
	defer i.mu.Unlock()

	i.bundleManager.Toggle()

	// Invalidate legacy cache so subsequent LoadConfig reads from new source
	InvalidateConfigCache()

	var source, name string
	if i.bundleManager.IsUsingProject() {
		source = "project"
		name = i.bundleManager.ProjectName()
	} else {
		source = "global"
		name = i18n.T("commands.config_bundle.global")
	}

	// Notify listeners
	for _, cb := range i.onSourceChange {
		cb(source, name)
	}

	return source
}

// CreateProjectConfig creates a new project config bundle in the current directory.
func (i *ConfigBundleIntegration) CreateProjectConfig(workDir string) error {
	i.mu.Lock()
	defer i.mu.Unlock()

	bundle, err := i.bundleManager.CreateProject("")
	if err != nil {
		return err
	}

	// Invalidate legacy cache so subsequent LoadConfig reads from new project config
	InvalidateConfigCache()

	// Update detector
	if workDir != "" {
		i.detector.SetWorkDir(workDir)
	}

	// Notify listeners
	for _, cb := range i.onSourceChange {
		cb("project", bundle.Name)
	}

	return nil
}

// SetWorkDir updates the working directory and checks for project config.
func (i *ConfigBundleIntegration) SetWorkDir(ctx context.Context, workDir string) error {
	i.mu.Lock()
	defer i.mu.Unlock()

	// Track if config source might change
	wasUsingProject := i.bundleManager.IsUsingProject()

	if err := i.bundleManager.SetWorkDir(ctx, workDir); err != nil {
		return err
	}

	// Invalidate cache if project config detection might have changed the source
	if wasUsingProject != i.bundleManager.IsUsingProject() {
		InvalidateConfigCache()
	}

	i.detector.SetWorkDir(workDir)
	return nil
}

// OnSourceChange registers a callback for when the config source changes.
func (i *ConfigBundleIntegration) OnSourceChange(callback func(source string, name string)) {
	i.mu.Lock()
	defer i.mu.Unlock()
	i.onSourceChange = append(i.onSourceChange, callback)
}

// handleProjectDetected handles when a project config is detected.
func (i *ConfigBundleIntegration) handleProjectDetected(info *configbundle.ProjectConfigInfo) {
	i.mu.Lock()
	defer i.mu.Unlock()

	// Auto-switch to project config if not already using it
	if !i.bundleManager.IsUsingProject() {
		_ = i.bundleManager.UseProject()

		// Invalidate legacy cache so subsequent LoadConfig reads from project config
		InvalidateConfigCache()

		// Notify listeners
		for _, cb := range i.onSourceChange {
			cb("project", info.Name)
		}
	}
}

// handleProjectLost handles when a project config is lost.
func (i *ConfigBundleIntegration) handleProjectLost() {
	i.mu.Lock()
	defer i.mu.Unlock()

	// Switch back to global config
	i.bundleManager.UseGlobal()

	// Invalidate legacy cache so subsequent LoadConfig reads from global config
	InvalidateConfigCache()

	// Notify listeners
	for _, cb := range i.onSourceChange {
		cb("global", "Global")
	}
}

// Save saves the current active config.
func (i *ConfigBundleIntegration) Save(ctx context.Context) error {
	i.mu.Lock()
	defer i.mu.Unlock()
	return i.bundleManager.Save(ctx)
}

// Refresh reloads configs from disk.
func (i *ConfigBundleIntegration) Refresh(ctx context.Context) error {
	i.mu.Lock()
	defer i.mu.Unlock()

	err := i.bundleManager.Refresh(ctx)
	if err == nil {
		// Invalidate legacy cache so subsequent LoadConfig reads fresh data
		InvalidateConfigCache()
	}
	return err
}

// Stop stops the detector.
func (i *ConfigBundleIntegration) Stop() {
	i.mu.Lock()
	defer i.mu.Unlock()
	i.detector.Stop()
}

// GetActiveConfigLite returns a summary of the active config for display.
func (i *ConfigBundleIntegration) GetActiveConfigLite() *configbundle.ConfigBundleLite {
	i.mu.RLock()
	defer i.mu.RUnlock()

	active := i.bundleManager.Active()
	if active == nil {
		return nil
	}

	lite := &configbundle.ConfigBundleLite{
		Name:        active.Name,
		Description: active.Description,
		Source:      string(active.Source),
		Path:        active.Path,
	}

	// System summary
	lite.System = configbundle.ConfigSectionSummary{
		HasData: active.System.Theme != "" || active.System.DefaultProvider != "",
		Preview: i18n.T("commands.config_bundle.system_preview", active.System.Theme, active.System.DefaultProvider),
	}

	// Agents summary
	lite.Agents = configbundle.ConfigSectionSummary{
		HasData: len(active.Agents.Definitions) > 0,
		Count:   len(active.Agents.Definitions),
	}
	if len(active.Agents.Definitions) > 0 {
		lite.Agents.Preview = active.Agents.Definitions[0].Name
	}

	// Profiles summary
	lite.Profiles = configbundle.ConfigSectionSummary{
		HasData: len(active.Profiles.Inline) > 0 || active.Profiles.ReferencePath != "",
		Count:   len(active.Profiles.Inline),
	}
	if len(active.Profiles.Inline) > 0 {
		lite.Profiles.Preview = active.Profiles.Inline[0].Name
	} else if active.Profiles.ReferencePath != "" {
		lite.Profiles.Preview = i18n.T("commands.config_bundle.reference", active.Profiles.ReferencePath)
	}

	// Prompts summary
	lite.Prompts = configbundle.ConfigSectionSummary{
		HasData: len(active.Prompts.Custom) > 0 || len(active.Prompts.Overrides) > 0,
		Count:   len(active.Prompts.Custom) + len(active.Prompts.Overrides),
	}
	if len(active.Prompts.Custom) > 0 {
		for k := range active.Prompts.Custom {
			lite.Prompts.Preview = k
			break
		}
	}

	// Tools summary
	lite.Tools = configbundle.ConfigSectionSummary{
		HasData: len(active.Tools.Enabled) > 0 || len(active.Tools.Disabled) > 0 || len(active.Tools.Custom) > 0,
		Count:   len(active.Tools.Enabled) + len(active.Tools.Custom),
	}
	if len(active.Tools.Enabled) > 0 {
		maxPreview := 3
		if len(active.Tools.Enabled) < maxPreview {
			maxPreview = len(active.Tools.Enabled)
		}
		lite.Tools.Preview = strings.Join(active.Tools.Enabled[:maxPreview], ", ")
	}

	// Hooks summary
	lite.Hooks = configbundle.ConfigSectionSummary{
		HasData: len(active.Hooks.Definitions) > 0,
		Count:   len(active.Hooks.Definitions),
	}
	if len(active.Hooks.Definitions) > 0 {
		lite.Hooks.Preview = active.Hooks.Definitions[0].Name
	}

	// Skills summary
	lite.Skills = configbundle.ConfigSectionSummary{
		HasData: len(active.Skills.Installed) > 0,
		Count:   len(active.Skills.Installed),
	}
	if len(active.Skills.Installed) > 0 {
		lite.Skills.Preview = active.Skills.Installed[0].Name
	}

	// Providers summary
	lite.Providers = configbundle.ConfigSectionSummary{
		HasData: len(active.Providers.Inline) > 0 || active.Providers.ReferencePath != "",
		Count:   len(active.Providers.Inline),
	}
	if len(active.Providers.Inline) > 0 {
		lite.Providers.Preview = active.Providers.Inline[0].Name
	} else if active.Providers.ReferencePath != "" {
		lite.Providers.Preview = i18n.T("commands.config_bundle.reference", active.Providers.ReferencePath)
	}

	// MCPServers summary
	lite.MCPServers = configbundle.ConfigSectionSummary{
		HasData: len(active.MCPServers.Servers) > 0,
		Count:   len(active.MCPServers.Servers),
	}
	if len(active.MCPServers.Servers) > 0 {
		lite.MCPServers.Preview = active.MCPServers.Servers[0].Name
	}

	return lite
}

// OpenInEditor opens the active config file in the user's editor.
func (i *ConfigBundleIntegration) OpenInEditor() error {
	i.mu.RLock()
	defer i.mu.RUnlock()

	path := i.bundleManager.Active().Path
	if path == "" {
		return fmt.Errorf("no config file path available")
	}

	// Use EDITOR environment variable or fallback to common editors
	editor := os.Getenv("EDITOR")
	if editor == "" {
		editor = os.Getenv("VISUAL")
	}
	if editor == "" {
		// Try common editors
		for _, e := range []string{"vim", "nano", "code", "subl"} {
			if _, err := exec.LookPath(e); err == nil {
				editor = e
				break
			}
		}
	}
	if editor == "" {
		return fmt.Errorf("no editor found (set EDITOR environment variable)")
	}

	cmd := exec.Command(editor, path)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	return cmd.Run()
}
