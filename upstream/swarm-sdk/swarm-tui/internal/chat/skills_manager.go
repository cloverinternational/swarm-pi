package chat

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/hooks"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/skills"
)

// SkillsManager wraps sdk/skills for TUI integration.
// It provides a thread-safe interface for managing skills from the chat application,
// including loading, activation, search, and installation of skills.
type SkillsManager struct {
	loader      *skills.Loader
	installDir  string
	initialized bool
	mu          sync.RWMutex

	// File watcher for hot-reload of skill directories
	watcher *skills.Watcher

	// HooksManager for registering skill hooks on activation
	hooksManager *HooksManager

	// Callbacks for state changes
	onSkillEnabled  func(name string)
	onSkillDisabled func(name string)
	onSkillsChanged func()
}

// NewSkillsManager creates a new skills manager with the specified install directory.
func NewSkillsManager(installDir string) *SkillsManager {
	return &SkillsManager{
		installDir:  installDir,
		initialized: false,
	}
}

// AddProjectSearchPaths registers <projectDir>/.claude/skills/,
// <projectDir>/.claude/commands/, and <projectDir>/.swarm/skills/ as additional
// skill search paths, then re-runs discovery so any skills found there are
// immediately available. Must be called after Initialize. No-ops when the
// manager is not yet initialized or when neither directory exists.
func (m *SkillsManager) AddProjectSearchPaths(projectDir string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.loader == nil || projectDir == "" {
		return nil
	}
	candidates := []string{
		filepath.Join(projectDir, ".claude", "skills"),
		filepath.Join(projectDir, ".claude", "commands"), // legacy Claude Code location
		filepath.Join(projectDir, ".swarm", "skills"),    // Swarm project-scoped skills
	}
	changed := false
	for _, p := range candidates {
		if _, err := os.Stat(p); err == nil {
			m.loader.Registry.AddSearchPath(p)
			m.loader.SearchPaths = append(m.loader.SearchPaths, p)
			changed = true
		}
	}
	if changed {
		if _, err := m.loader.Registry.DiscoverAll(); err != nil {
			return fmt.Errorf("discover project skills: %w", err)
		}
	}
	return nil
}

// Initialize loads the skill registry and discovers available skills.
// This should be called once during application startup.
// Returns nil if already initialized.
func (m *SkillsManager) Initialize(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.initialized {
		return nil
	}

	// Ensure install directory exists
	if err := os.MkdirAll(m.installDir, 0755); err != nil {
		return err
	}

	// Create the skill loader
	m.loader = skills.NewLoader(m.installDir)

	// Initialize the loader (loads installed plugins and discovers skills)
	if err := m.loader.Initialize(ctx); err != nil {
		return err
	}

	m.initialized = true

	// Emit skill_loaded telemetry events for each discovered skill.
	// Mirrors Claude Code's logSkillsLoaded (src/utils/telemetry/skillLoadedEvent.ts).
	for _, skill := range m.loader.List() {
		source := skill.LoadedFrom
		if source == "" {
			source = "project"
		}
		skills.EmitSkillLoaded(skill.Metadata.Name, source, skill.LoadedFrom)
	}

	// Start the file watcher for hot-reload of skill directories.
	// When a SKILL.md changes, the watcher debounces (300ms), clears
	// the registry, re-discovers all skills, and fires onSkillsChanged.
	if watcher, err := skills.NewWatcher(m.loader, func() {
		// Capture callback before releasing lock
		m.mu.RLock()
		onChanged := m.onSkillsChanged
		m.mu.RUnlock()
		if onChanged != nil {
			onChanged()
		}
	}); err == nil {
		if startErr := watcher.Start(); startErr != nil {
			// Non-fatal: skills still work, just no hot-reload
			_ = startErr
		} else {
			m.watcher = watcher
		}
	}

	return nil
}

// GetLoader returns the underlying skill loader.
// Returns nil if not initialized.
func (m *SkillsManager) GetLoader() *skills.Loader {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.loader
}

// GetActiveInstructions returns combined instructions from all active skills.
// Returns empty string if not initialized or no active skills.
func (m *SkillsManager) GetActiveInstructions() string {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if m.loader == nil {
		return ""
	}
	return m.loader.GetActiveInstructions()
}

// GetActiveSkills returns currently active skills.
// Returns empty slice if not initialized.
func (m *SkillsManager) GetActiveSkills() []*skills.Skill {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if m.loader == nil {
		return []*skills.Skill{}
	}
	return m.loader.GetActiveSkills()
}

// GetAllSkills returns all available skills (active and inactive).
// Returns empty slice if not initialized.
func (m *SkillsManager) GetAllSkills() []*skills.Skill {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if m.loader == nil {
		return []*skills.Skill{}
	}
	return m.loader.List()
}

// EnableSkill activates a skill by name.
// Calls onSkillEnabled callback if set.
func (m *SkillsManager) EnableSkill(name string) error {
	m.mu.Lock()
	if m.loader == nil {
		m.mu.Unlock()
		return nil
	}

	if err := m.loader.Activate(name); err != nil {
		m.mu.Unlock()
		return err
	}

	// Register skill hooks with the HooksManager if the skill has hooks.
	// This converts SkillHookConfig entries to ShellHook objects and registers
	// them as session-scoped hooks. Mirrors Claude Code's registerSkillHooks.
	if m.hooksManager != nil {
		if skill, ok := m.loader.Registry.Get(name); ok && skill.Hooks != nil {
			m.registerSkillHooks(skill)
		}
	}

	// Capture callbacks before releasing lock to avoid deadlock
	onEnabled := m.onSkillEnabled
	onChanged := m.onSkillsChanged
	m.mu.Unlock()

	// Fire callbacks outside of lock
	if onEnabled != nil {
		onEnabled(name)
	}
	if onChanged != nil {
		onChanged()
	}

	return nil
}

// DisableSkill deactivates a skill by name.
// Calls onSkillDisabled callback if set.
func (m *SkillsManager) DisableSkill(name string) error {
	m.mu.Lock()
	if m.loader == nil {
		m.mu.Unlock()
		return nil
	}

	if err := m.loader.Deactivate(name); err != nil {
		m.mu.Unlock()
		return err
	}

	// Capture callbacks before releasing lock to avoid deadlock
	onDisabled := m.onSkillDisabled
	onChanged := m.onSkillsChanged
	m.mu.Unlock()

	// Fire callbacks outside of lock
	if onDisabled != nil {
		onDisabled(name)
	}
	if onChanged != nil {
		onChanged()
	}

	return nil
}

// IsSkillActive returns true if the named skill is currently active.
func (m *SkillsManager) IsSkillActive(name string) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if m.loader == nil {
		return false
	}
	return m.loader.Registry.IsActive(name)
}

// SearchSkills searches for skills in local registry and marketplace.
func (m *SkillsManager) SearchSkills(ctx context.Context, query string) ([]skills.SkillSearchResult, error) {
	m.mu.RLock()
	loader := m.loader
	m.mu.RUnlock()

	if loader == nil {
		return []skills.SkillSearchResult{}, nil
	}
	return loader.Search(ctx, query)
}

// InstallSkill installs a skill from the marketplace.
// Calls onSkillsChanged callback if successful.
func (m *SkillsManager) InstallSkill(ctx context.Context, name string) (*skills.Skill, error) {
	m.mu.Lock()
	if m.loader == nil {
		m.mu.Unlock()
		return nil, nil
	}

	skill, err := m.loader.Install(ctx, name)
	if err != nil {
		m.mu.Unlock()
		return nil, err
	}

	// Capture callback before releasing lock to avoid deadlock
	onChanged := m.onSkillsChanged
	m.mu.Unlock()

	// Fire callback outside of lock
	if onChanged != nil {
		onChanged()
	}

	return skill, nil
}

// UninstallSkill removes an installed skill.
// Calls onSkillsChanged callback if successful.
func (m *SkillsManager) UninstallSkill(name string) error {
	m.mu.Lock()
	if m.loader == nil {
		m.mu.Unlock()
		return nil
	}

	if err := m.loader.Uninstall(name); err != nil {
		m.mu.Unlock()
		return err
	}

	// Capture callback before releasing lock to avoid deadlock
	onChanged := m.onSkillsChanged
	m.mu.Unlock()

	// Fire callback outside of lock
	if onChanged != nil {
		onChanged()
	}

	return nil
}

// RefreshSkills reloads all skills from disk.
// Calls onSkillsChanged callback if successful.
func (m *SkillsManager) RefreshSkills() error {
	m.mu.Lock()
	if m.loader == nil {
		m.mu.Unlock()
		return nil
	}

	ctx := context.Background()
	if err := m.loader.Initialize(ctx); err != nil {
		m.mu.Unlock()
		return err
	}

	// Capture callback before releasing lock to avoid deadlock
	onChanged := m.onSkillsChanged
	m.mu.Unlock()

	// Fire callback outside of lock
	if onChanged != nil {
		onChanged()
	}

	return nil
}

// AutoActivate activates skills based on context triggers.
// Returns the list of skills that were activated.
func (m *SkillsManager) AutoActivate(ctx skills.ActivationContext) []*skills.Skill {
	m.mu.Lock()
	if m.loader == nil {
		m.mu.Unlock()
		return []*skills.Skill{}
	}

	activated := m.loader.AutoActivate(ctx)

	// Capture callbacks before releasing lock to avoid deadlock
	onChanged := m.onSkillsChanged
	onEnabled := m.onSkillEnabled
	m.mu.Unlock()

	// Fire callbacks outside of lock
	if len(activated) > 0 && onChanged != nil {
		onChanged()
	}
	for _, skill := range activated {
		if onEnabled != nil {
			onEnabled(skill.Metadata.Name)
		}
	}

	return activated
}

// GetAvailableSkillsXML generates XML for all available skills.
// This is used for injecting skill information into system prompts.
func (m *SkillsManager) GetAvailableSkillsXML() string {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if m.loader == nil {
		return ""
	}
	return skills.GenerateAvailableSkillsXML(m.loader.List())
}

// GetActiveSkillsXML generates XML for currently active skills.
// This is used for injecting active skill information into system prompts.
func (m *SkillsManager) GetActiveSkillsXML() string {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if m.loader == nil {
		return ""
	}
	return skills.GenerateActiveSkillsXML(m.loader.GetActiveSkills())
}

// GetSkillInstructionsXML generates XML containing combined instructions
// from all active skills for system prompt injection.
func (m *SkillsManager) GetSkillInstructionsXML() string {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if m.loader == nil {
		return ""
	}
	return skills.GenerateCombinedInstructionsXML(m.loader.GetActiveSkills())
}

// GetPromptContext generates all skill-related XML blocks for system prompts.
func (m *SkillsManager) GetPromptContext() *skills.SkillPromptContext {
	return m.GetPromptContextForQuery("")
}

// GetPromptContextForQuery ranks available skills for the current request before
// the prompt-size cap is applied. Active skills always remain first.
func (m *SkillsManager) GetPromptContextForQuery(query string) *skills.SkillPromptContext {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if m.loader == nil {
		return &skills.SkillPromptContext{}
	}
	active := m.loader.GetActiveSkills()
	available := skills.RankForContext(m.loader.List(), active, query)
	return skills.GeneratePromptContext(available, active)
}

// SetOnSkillEnabled sets the callback for when a skill is enabled.
func (m *SkillsManager) SetOnSkillEnabled(fn func(string)) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.onSkillEnabled = fn
}

// SetOnSkillDisabled sets the callback for when a skill is disabled.
func (m *SkillsManager) SetOnSkillDisabled(fn func(string)) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.onSkillDisabled = fn
}

// SetOnSkillsChanged sets the callback for when skills list changes.
func (m *SkillsManager) SetOnSkillsChanged(fn func()) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.onSkillsChanged = fn
}

// IsInitialized returns true if the manager has been initialized.
func (m *SkillsManager) IsInitialized() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.initialized
}

// GetInstallDir returns the skill installation directory.
func (m *SkillsManager) GetInstallDir() string {
	return m.installDir
}

// SetHooksManager sets the hooks manager for skill hooks registration.
// When a skill with hooks is enabled, its hooks are automatically registered
// with the hooks manager. Mirrors Claude Code's registerSkillHooks.
func (m *SkillsManager) SetHooksManager(hm *HooksManager) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.hooksManager = hm
}

// registerSkillHooks converts skill hook configs to ShellHook objects and
// registers them with the HooksManager. Called inside EnableSkill while
// holding the lock.
func (m *SkillsManager) registerSkillHooks(skill *skills.Skill) {
	if skill.Hooks == nil || m.hooksManager == nil {
		return
	}

	// Map skill hook event types to event patterns
	registerEvent := func(eventType string, configs []skills.SkillHookConfig) {
		for _, cfg := range configs {
			if cfg.Command == "" && cfg.Prompt == "" {
				continue // Skip hooks with no action
			}

			hookName := fmt.Sprintf("skill:%s:%s:%s", skill.Metadata.Name, eventType, cfg.Matcher)

			shellHook := &hooks.ShellHook{
				HookName:      hookName,
				Description:   fmt.Sprintf("Skill %s hook (%s)", skill.Metadata.Name, eventType),
				EventPatterns: []string{eventType},
				ToolMatcher:   cfg.Matcher,
				Command:       cfg.Command,
				Timeout:       time.Duration(cfg.Timeout) * time.Second,
				HookAction:    "block_exit2", // Claude Code semantics
			}

			if cfg.Timeout > 0 {
				shellHook.Timeout = time.Duration(cfg.Timeout) * time.Second
			}

			if err := m.hooksManager.RegisterCustomHook(shellHook); err != nil {
				// Non-fatal: hook registration failure shouldn't prevent skill activation
				_ = err
			}
		}
	}

	registerEvent("PreToolUse", skill.Hooks.PreToolUse)
	registerEvent("PostToolUse", skill.Hooks.PostToolUse)
	registerEvent("Stop", skill.Hooks.Stop)
	registerEvent("SessionStart", skill.Hooks.SessionStart)
}

// DefaultSkillsDir returns the default skills installation directory.
func DefaultSkillsDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(".", ".swarmos", "skills")
	}
	return filepath.Join(home, ".swarmos", "skills")
}

// Close stops the file watcher and releases resources.
// It should be called when the application shuts down.
func (m *SkillsManager) Close() {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.watcher != nil {
		m.watcher.Stop()
		m.watcher = nil
	}
}
