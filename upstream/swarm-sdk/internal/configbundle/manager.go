// Package configbundle provides a unified configuration system for Swarm.
package configbundle

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/Swarm-Code/mono/swarm-core/core"
	"github.com/Swarm-Code/mono/swarm-sdk/configformat"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/atomicfile"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/paths"
)

// Manager handles config bundle loading, saving, and switching between
// global and project-level configurations.
type Manager struct {
	mu sync.RWMutex

	// global is the global config bundle (always loaded from ~/.swarm/config/)
	global *ConfigBundle

	// project is the project-level config bundle (may be nil if no project config)
	project *ConfigBundle

	// active is the currently active config (points to either global or merged)
	active *ConfigBundle

	// workDir is the current working directory (used to find project config)
	workDir string

	// globalPath is the path to the global config bundle
	globalPath string

	// projectPath is the path to the project config bundle (empty if none found)
	projectPath string

	// useProject indicates whether to use project config (merged with global)
	useProject bool

	// coreConfigManager is the underlying core config manager (for compatibility)
	coreConfigManager core.ConfigManager

	// callbacks for config changes
	onConfigChange []func(old, new *ConfigBundle)
}

// Options contains options for creating a Manager.
type Options struct {
	// WorkDir is the working directory to search for project config.
	// If empty, uses current working directory.
	WorkDir string

	// GlobalPath is the path to the global config bundle.
	// If empty, uses ~/.swarm/config/config.yaml.
	GlobalPath string

	// CoreConfigManager is an existing core.ConfigManager to use for
	// loading legacy config files. If nil, a new one is created.
	CoreConfigManager core.ConfigManager

	// AutoSwitch determines whether to automatically switch to project
	// config when one is found. Default is true.
	AutoSwitch bool
}

// NewManager creates a new config bundle manager.
func NewManager(ctx context.Context, opts Options) (*Manager, error) {
	m := &Manager{
		workDir:           opts.WorkDir,
		coreConfigManager: opts.CoreConfigManager,
		onConfigChange:    make([]func(old, new *ConfigBundle), 0),
	}

	// Set default global path
	if opts.GlobalPath != "" {
		m.globalPath = opts.GlobalPath
	} else {
		m.globalPath = paths.ConfigFile()
	}

	// Set default work directory
	if m.workDir == "" {
		wd, err := os.Getwd()
		if err != nil {
			return nil, fmt.Errorf("failed to get working directory: %w", err)
		}
		m.workDir = wd
	}

	// Load global config
	global, err := m.loadBundle(m.globalPath)
	if err != nil {
		// Create default global config if it doesn't exist
		global = m.defaultGlobalBundle()
	}
	global.Source = SourceGlobal
	global.Path = m.globalPath
	m.global = global
	m.active = m.global

	// Search for project config
	m.projectPath = FindProjectConfig(m.workDir)
	if m.projectPath != "" {
		project, err := m.loadBundle(m.projectPath)
		if err == nil {
			project.Source = SourceProject
			project.Path = m.projectPath
			m.project = project

			// Auto-switch if requested
			if opts.AutoSwitch {
				m.useProject = true
				m.active = m.mergeConfigs(m.global, m.project)
			}
		}
	}

	return m, nil
}

// Global returns the global config bundle.
func (m *Manager) Global() *ConfigBundle {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.global
}

// Project returns the project config bundle, or nil if none exists.
func (m *Manager) Project() *ConfigBundle {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.project
}

// Active returns the currently active config bundle.
// This is either the global config or the merged config (global + project).
func (m *Manager) Active() *ConfigBundle {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.active
}

// IsUsingProject returns true if the manager is currently using project config.
func (m *Manager) IsUsingProject() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.useProject && m.project != nil
}

// HasProjectConfig returns true if a project config was found.
func (m *Manager) HasProjectConfig() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.project != nil
}

// ProjectName returns the name of the project config, or empty string if none.
func (m *Manager) ProjectName() string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.project == nil {
		return ""
	}
	if m.project.Name != "" {
		return m.project.Name
	}
	// Use directory name as fallback
	return filepath.Base(filepath.Dir(m.projectPath))
}

// ProjectPath returns the path to the project config, or empty string if none.
func (m *Manager) ProjectPath() string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.projectPath
}

// GlobalPath returns the path to the global config.
func (m *Manager) GlobalPath() string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.globalPath
}

// UseProject switches to using project config (merged with global).
// Returns ErrNoProjectConfig if no project config exists.
func (m *Manager) UseProject() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.project == nil {
		return ErrNoProjectConfig
	}

	old := m.active
	m.useProject = true
	m.active = m.mergeConfigs(m.global, m.project)

	m.notifyChange(old, m.active)
	return nil
}

// UseGlobal switches to using global config only.
func (m *Manager) UseGlobal() {
	m.mu.Lock()
	defer m.mu.Unlock()

	if !m.useProject {
		return // Already using global
	}

	old := m.active
	m.useProject = false
	m.active = m.global

	m.notifyChange(old, m.active)
}

// Toggle switches between global and project config.
// Returns the new state (true = using project, false = using global).
func (m *Manager) Toggle() bool {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.project == nil {
		return false // Can't toggle without project config
	}

	old := m.active
	m.useProject = !m.useProject

	if m.useProject {
		m.active = m.mergeConfigs(m.global, m.project)
	} else {
		m.active = m.global
	}

	m.notifyChange(old, m.active)
	return m.useProject
}

// Save saves the active config to the appropriate location.
// If using project config, saves to project path; otherwise saves to global path.
func (m *Manager) Save(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	var bundle *ConfigBundle
	var path string

	if m.useProject && m.project != nil {
		bundle = m.project
		path = m.projectPath
	} else {
		bundle = m.global
		path = m.globalPath
	}

	bundle.UpdatedAt = time.Now()
	return m.saveBundle(path, bundle)
}

// SaveGlobal saves the global config.
func (m *Manager) SaveGlobal(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.global.UpdatedAt = time.Now()
	return m.saveBundle(m.globalPath, m.global)
}

// SaveProject saves the project config.
// Returns ErrNoProjectConfig if no project config exists.
func (m *Manager) SaveProject(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.project == nil {
		return ErrNoProjectConfig
	}

	m.project.UpdatedAt = time.Now()
	return m.saveBundle(m.projectPath, m.project)
}

// CreateProject creates a new project config bundle at the specified path.
// If path is empty, uses .swarm/config.yaml in the current work directory.
func (m *Manager) CreateProject(path string) (*ConfigBundle, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if path == "" {
		path = filepath.Join(m.workDir, ".swarm", "config")
	}
	// Canonical on-disk target is YAML (migrate-on-save default). Normalize any
	// caller-supplied .json path to the .yaml write target so manager state and
	// the file actually written stay consistent.
	path = configformat.WritePath(filepath.Dir(path), configformat.BaseName(filepath.Base(path)))

	// Ensure directory exists
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create config directory: %w", err)
	}

	// Create default project config
	project := m.defaultProjectBundle()
	project.Source = SourceProject
	project.Path = path

	// Save it
	if err := m.saveBundle(path, project); err != nil {
		return nil, err
	}

	// Update manager state
	m.project = project
	m.projectPath = path

	return project, nil
}

// UpdateActive updates the active config with the provided modifications.
// The modify function receives a pointer to the active config and can modify it.
func (m *Manager) UpdateActive(modify func(*ConfigBundle)) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Create a copy for the callback
	oldCopy := *m.active

	// Apply modifications
	modify(m.active)

	// Update timestamp
	m.active.UpdatedAt = time.Now()

	// If using project, also update project bundle
	if m.useProject && m.project != nil {
		// Changes to active (merged) config go to project config
		m.project.UpdatedAt = time.Now()
	}

	m.notifyChange(&oldCopy, m.active)
	return nil
}

// OnConfigChange registers a callback to be called when the active config changes.
func (m *Manager) OnConfigChange(callback func(old, new *ConfigBundle)) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.onConfigChange = append(m.onConfigChange, callback)
}

// Refresh reloads configs from disk and re-merges if using project config.
func (m *Manager) Refresh(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	old := m.active

	// Reload global
	global, err := m.loadBundle(m.globalPath)
	if err != nil {
		return fmt.Errorf("failed to reload global config: %w", err)
	}
	global.Source = SourceGlobal
	global.Path = m.globalPath
	m.global = global

	// Reload project if exists
	if m.projectPath != "" {
		project, err := m.loadBundle(m.projectPath)
		if err == nil {
			project.Source = SourceProject
			project.Path = m.projectPath
			m.project = project
		}
	}

	// Update active
	if m.useProject && m.project != nil {
		m.active = m.mergeConfigs(m.global, m.project)
	} else {
		m.active = m.global
	}

	m.notifyChange(old, m.active)
	return nil
}

// SetWorkDir changes the working directory and searches for a new project config.
func (m *Manager) SetWorkDir(ctx context.Context, workDir string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	old := m.active
	m.workDir = workDir

	// Search for project config in new directory
	newProjectPath := FindProjectConfig(workDir)

	// If project path changed, reload
	if newProjectPath != m.projectPath {
		if newProjectPath == "" {
			m.project = nil
			m.projectPath = ""
			m.useProject = false
			m.active = m.global
		} else {
			project, err := m.loadBundle(newProjectPath)
			if err == nil {
				project.Source = SourceProject
				project.Path = newProjectPath
				m.project = project
				m.projectPath = newProjectPath
				// Keep current useProject setting
				if m.useProject {
					m.active = m.mergeConfigs(m.global, m.project)
				}
			}
		}
	}

	m.notifyChange(old, m.active)
	return nil
}

// GetCoreConfig converts the active ConfigBundle to core.Config.
// This provides compatibility with existing code that expects core.Config.
func (m *Manager) GetCoreConfig() *core.Config {
	m.mu.RLock()
	defer m.mu.RUnlock()

	cfg := &core.Config{
		SchemaVersion: core.CurrentConfigSchemaVersion,
	}

	// Map system settings to core.Config
	s := m.active.System
	cfg.DefaultProvider = s.DefaultProvider
	cfg.DefaultModel = s.DefaultModel
	cfg.CurrentProvider = s.CurrentProvider
	cfg.CurrentModel = s.CurrentModel
	cfg.DefaultMode = s.DefaultMode
	cfg.DefaultAgent = s.DefaultAgent
	cfg.Theme = s.Theme

	// Display settings
	if s.ShowThinking != nil {
		cfg.ShowThinking = *s.ShowThinking
	}
	if s.ShowTokenCount != nil {
		cfg.ShowTokenCount = *s.ShowTokenCount
	}
	if s.ShowToolOutput != nil {
		cfg.ShowToolOutput = *s.ShowToolOutput
	}
	if s.CompactMode != nil {
		cfg.CompactMode = *s.CompactMode
	}
	cfg.MaxOutputLines = s.MaxOutputLines
	if s.SyntaxHighlighting != nil {
		cfg.SyntaxHighlighting = *s.SyntaxHighlighting
	}
	cfg.MaxConcurrentTools = s.MaxConcurrentTools
	cfg.ToolTimeout = s.ToolTimeout
	if s.EnableSandbox != nil {
		cfg.EnableSandbox = *s.EnableSandbox
	}
	cfg.SandboxPaths = s.SandboxPaths

	// Editor settings
	cfg.Editor = s.Editor
	cfg.EditorArgs = s.EditorArgs
	cfg.ExternalEditorCmd = s.ExternalEditorCmd

	// Behavior settings
	if s.AutoSaveConversations != nil {
		cfg.AutoSaveConversations = *s.AutoSaveConversations
	}
	if s.ConfirmBeforeExit != nil {
		cfg.ConfirmBeforeExit = *s.ConfirmBeforeExit
	}
	if s.EnableLogging != nil {
		cfg.EnableLogging = *s.EnableLogging
	}
	cfg.LogLevel = s.LogLevel

	// Compaction settings
	if s.EnableCompaction != nil {
		cfg.EnableCompaction = *s.EnableCompaction
	}
	cfg.CompactionThreshold = s.CompactionThreshold
	cfg.WarningThreshold = s.WarningThreshold
	cfg.PreserveRecentMessages = s.PreserveRecentMessages

	// Micro-compaction settings
	if s.EnableMicroCompaction != nil {
		cfg.EnableMicroCompaction = *s.EnableMicroCompaction
	}
	cfg.MicroRetentionCount = s.MicroRetentionCount

	// Cache settings
	if s.EnableCache != nil {
		cfg.EnableCache = *s.EnableCache
	}
	cfg.CacheDir = s.CacheDir
	cfg.CacheMaxSizeMB = s.CacheMaxSizeMB
	cfg.CacheExpiryDays = s.CacheExpiryDays

	// Cloud sync settings
	if s.SyncConversations != nil {
		cfg.SyncConversations = *s.SyncConversations
	}
	if s.SyncSettings != nil {
		cfg.SyncSettings = *s.SyncSettings
	}
	cfg.SyncSettingsScope = s.SyncSettingsScope
	cfg.SyncSettingsTeamID = s.SyncSettingsTeamID
	if s.SyncProfiles != nil {
		cfg.SyncProfiles = *s.SyncProfiles
	}
	if s.EncryptCloudData != nil {
		cfg.EncryptCloudData = *s.EncryptCloudData
	}
	if s.AnonymousAnalytics != nil {
		cfg.AnonymousAnalytics = *s.AnonymousAnalytics
	}

	// Advanced features
	cfg.HybridConfig = s.HybridConfig
	cfg.WebSearch = s.WebSearch
	cfg.SteeringConfig = s.SteeringConfig

	return cfg
}

// notifyChange calls all registered change callbacks.
// Must be called with lock held.
func (m *Manager) notifyChange(old, new *ConfigBundle) {
	for _, cb := range m.onConfigChange {
		cb(old, new)
	}
}

// loadBundle loads a config bundle from the specified path.
func (m *Manager) loadBundle(path string) (*ConfigBundle, error) {
	// Resolve <base>.yaml -> .yml -> .json so an existing legacy JSON bundle is
	// still read while new installs prefer YAML. Callers historically passed a
	// .json path; we key off its extension-less base name.
	dir := filepath.Dir(path)
	base := configformat.BaseName(filepath.Base(path))
	resolved, found := configformat.ResolvePath(dir, base)
	if !found {
		return nil, fmt.Errorf("config file not found: %s", resolved)
	}

	var bundle ConfigBundle
	if err := configformat.LoadFile(resolved, &bundle); err != nil {
		return nil, err
	}

	// Set defaults
	if bundle.SchemaVersion == 0 {
		bundle.SchemaVersion = SchemaVersion
	}

	return &bundle, nil
}

// saveBundle saves a config bundle to the specified path.
func (m *Manager) saveBundle(path string, bundle *ConfigBundle) error {
	// Write <base>.yaml by default (migrate-on-save); a pre-existing <base>.json
	// is left in place and shadowed by read precedence. The .yaml write target is
	// derived from the extension-less base of the requested path.
	dir := filepath.Dir(path)
	base := configformat.BaseName(filepath.Base(path))
	target := configformat.WritePath(dir, base)
	// Encode with configformat's codec (YAML/JSON selected by target extension)
	// but persist through atomicfile for crash-safe, cross-process-locked writes.
	data, err := configformat.Marshal(bundle, configformat.FormatForPath(target))
	if err != nil {
		return fmt.Errorf("failed to marshal config: %w", err)
	}
	if err := atomicfile.WithLock(target, func() error {
		return atomicfile.Write(target, data)
	}); err != nil {
		return fmt.Errorf("failed to write config file: %w", err)
	}
	return nil
}

// defaultGlobalBundle creates a default global config bundle.
func (m *Manager) defaultGlobalBundle() *ConfigBundle {
	now := time.Now()
	return &ConfigBundle{
		SchemaVersion: SchemaVersion,
		Name:          "Global Config",
		Description:   "Global Swarm configuration",
		CreatedAt:     now,
		UpdatedAt:     now,
		Source:        SourceGlobal,
		System: SystemConfig{
			Theme:                  "dark",
			ShowThinking:           new(true),
			ShowTokenCount:         new(true),
			ShowToolOutput:         new(true),
			CompactMode:            new(false),
			SyntaxHighlighting:     new(true),
			AutoSaveConversations:  new(true),
			ConfirmBeforeExit:      new(true),
			EnableLogging:          new(false),
			EnableCompaction:       new(true),
			CompactionThreshold:    100000,
			WarningThreshold:       80000,
			PreserveRecentMessages: 10,
			EnableMicroCompaction:  new(true),
			MicroRetentionCount:    5,
			EnableCache:            new(true),
			CacheExpiryDays:        7,
			SyncConversations:      new(false),
			SyncSettings:           new(false),
			SyncProfiles:           new(false),
			EncryptCloudData:       new(true),
			AnonymousAnalytics:     new(false),
			EnableSandbox:          new(false),
			MaxConcurrentTools:     5,
			ToolTimeout:            300,
		},
		Profiles: ProfilesConfig{
			Mode:          ProfileModeReference,
			ReferencePath: "agent_profiles.json",
		},
		Providers: ProvidersConfig{
			Mode:          ProviderModeReference,
			ReferencePath: "providers.json",
		},
		Credentials: CredentialsConfig{
			Inherit: true,
		},
	}
}

// defaultProjectBundle creates a default project config bundle.
func (m *Manager) defaultProjectBundle() *ConfigBundle {
	now := time.Now()
	return &ConfigBundle{
		SchemaVersion: SchemaVersion,
		Name:          "Project Config",
		Description:   "Project-specific Swarm configuration",
		CreatedAt:     now,
		UpdatedAt:     now,
		Source:        SourceProject,
		MergePolicy:   DefaultMergePolicy(),
		Credentials: CredentialsConfig{
			Inherit: true,
		},
	}
}

// FindProjectConfig searches upward from startDir for .swarm/config.{yaml,yml,json}.
// Returns the resolved path if found, or empty string if not found.
func FindProjectConfig(startDir string) string {
	dir := startDir
	for {
		if p, found := configformat.ResolvePath(filepath.Join(dir, ".swarm"), "config"); found {
			return p
		}

		parent := filepath.Dir(dir)
		if parent == dir {
			break // reached root
		}
		dir = parent
	}
	return ""
}
