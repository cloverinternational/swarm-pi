package skills

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/paths"
)

// Loader handles dynamic skill loading and context integration.
type Loader struct {
	// Registry for loaded skills
	Registry *Registry

	// Database for plugin discovery
	Database *PluginDatabase

	// Default install directory
	InstallDir string

	// Search paths for discovery
	SearchPaths []string
}

// NewLoader creates a new skill loader.
func NewLoader(installDir string) *Loader {
	cacheDir := filepath.Join(installDir, ".cache")
	if err := os.MkdirAll(cacheDir, 0755); err != nil {
		// Log warning but continue - cache operations may fail gracefully
		fmt.Printf("warning: failed to create skills cache directory %s: %v\n", cacheDir, err)
	}

	searchPaths := []string{
		installDir,
		filepath.Join(installDir, "skills"),
	}

	// Managed/policy skills: highest-priority directory controlled by enterprise.
	// If SWARM_MANAGED_SKILLS_DIR is set and the directory exists, it is added
	// as the FIRST search path so managed skills take precedence over all others.
	// Can be disabled with SWARM_DISABLE_POLICY_SKILLS=1.
	// Mirrors Claude Code's managedSkillsDir (src/skills/loadSkillsDir.ts:641-642).
	if os.Getenv("SWARM_DISABLE_POLICY_SKILLS") != "1" {
		if managedDir := os.Getenv("SWARM_MANAGED_SKILLS_DIR"); managedDir != "" {
			if info, err := os.Stat(managedDir); err == nil && info.IsDir() {
				// Prepend as highest-priority path
				searchPaths = append([]string{managedDir}, searchPaths...)
			}
		}
	}

	// Also search ~/.claude/skills/ — the user-level Claude Code skills directory.
	// This mirrors Claude Code's discovery convention so skills installed there
	// are visible to the Swarm agent without any extra configuration.
	if home, err := os.UserHomeDir(); err == nil {
		claudeSkills := filepath.Join(home, ".claude", "skills")
		claudeCommands := filepath.Join(home, ".claude", "commands") // legacy
		searchPaths = append(searchPaths, claudeSkills, claudeCommands)
	}
	// Also search the canonical user-level Swarm skills directory
	// (~/.swarm/skills == paths.SkillsDir()). The legacy dual-search of the old
	// SwarmOS skills directory is now collapsed to this single canonical location.
	searchPaths = append(searchPaths, paths.SkillsDir())

	loader := &Loader{
		Registry:    NewRegistry(),
		Database:    NewPluginDatabase(cacheDir),
		InstallDir:  installDir,
		SearchPaths: searchPaths,
	}

	// Add search paths to registry
	for _, path := range loader.SearchPaths {
		loader.Registry.AddSearchPath(path)
	}

	return loader
}

// Initialize loads installed plugins and discovers available skills.
// It always seeds the registry with built-in default skills first so they are
// available even when the install directory is empty or inaccessible.
// User-installed skills loaded afterward may shadow built-in skills with the
// same name because RegisterDefaultSkills uses overwrite=false.
func (l *Loader) Initialize(ctx context.Context) error {
	// Seed registry with embedded built-in skills (non-overwriting).
	// Failures here are non-fatal; we continue so the rest of the system works.
	if err := RegisterDefaultSkills(l.Registry); err != nil {
		fmt.Printf("warning: failed to register built-in skills: %v\n", err)
	}

	// Load installed plugins state
	if err := l.Database.LoadInstalled(); err != nil {
		return fmt.Errorf("failed to load installed state: %w", err)
	}

	// Discover user-installed skills from all search paths.
	// These are registered with overwrite=true inside DiscoverAll, so a
	// user-installed skill with the same name as a built-in takes precedence.
	if _, err := l.Registry.DiscoverAll(); err != nil {
		return fmt.Errorf("failed to discover skills: %w", err)
	}

	return nil
}

// Search finds skills matching a query (local and remote).
func (l *Loader) Search(ctx context.Context, query string) ([]SkillSearchResult, error) {
	// Search local registry
	localResults := l.Registry.Search(query)

	// Search plugin database
	dbResults := l.Database.Search(query)

	// Convert and merge results
	var results []SkillSearchResult

	// Add local results
	for _, r := range localResults {
		results = append(results, r)
	}

	// Add database results (mark installed status)
	for _, plugin := range dbResults {
		result := SkillSearchResult{
			Name:        plugin.Name,
			Description: plugin.Description,
			Version:     plugin.Version,
			Author:      plugin.Author,
			Category:    plugin.Category,
			Tags:        plugin.Tags,
			Source:      "marketplace",
			Downloads:   plugin.Downloads,
			Rating:      plugin.Rating,
			Installed:   l.Database.IsInstalled(plugin.Name),
		}

		// Avoid duplicates
		found := false
		for _, r := range results {
			if strings.EqualFold(r.Name, result.Name) {
				found = true
				break
			}
		}
		if !found {
			results = append(results, result)
		}
	}

	return results, nil
}

// Install installs a skill from the marketplace.
func (l *Loader) Install(ctx context.Context, name string) (*Skill, error) {
	// Install via database
	installed, err := l.Database.Install(ctx, name, l.InstallDir)
	if err != nil {
		return nil, err
	}

	// Load the skill
	skill, err := l.Registry.LoadFromPath(installed.InstallPath)
	if err != nil {
		return nil, fmt.Errorf("installed but failed to load: %w", err)
	}

	return skill, nil
}

// Uninstall removes an installed skill.
func (l *Loader) Uninstall(name string) error {
	// Deactivate first
	l.Registry.Deactivate(name)

	// Remove from registry
	l.Registry.Unload(name)

	// Remove from database
	return l.Database.Uninstall(name)
}

// Activate is a no-op in the Claude-style skill model.
//
// Deprecated: Skills are invoked on-demand via the Skill tool. This method
// is preserved for backward compatibility and will be removed in a future release.
func (l *Loader) Activate(name string) error {
	return l.Registry.Activate(name)
}

// Deactivate is a no-op in the Claude-style skill model.
//
// Deprecated: See Activate.
func (l *Loader) Deactivate(name string) error {
	return l.Registry.Deactivate(name)
}

// GetActiveInstructions always returns "" in the Claude-style skill model.
//
// Deprecated: See Activate.
func (l *Loader) GetActiveInstructions() string {
	return ""
}

// GetActiveSkills always returns nil in the Claude-style skill model.
//
// Deprecated: See Activate.
func (l *Loader) GetActiveSkills() []*Skill {
	return nil
}

// ActivationContext provides context for automatic skill activation.
//
// Deprecated: AutoActivate is a no-op in the Claude-style skill model.
// This type is preserved for backward compatibility with the TUI.
type ActivationContext struct {
	// CurrentFile being edited
	CurrentFile string
	// CurrentTool being used
	CurrentTool string
	// CurrentMode of the agent
	CurrentMode string
	// Keywords in the current message
	Keywords []string
	// Message is the full normalized (lowercase) user message text
	Message string
	// ProjectType detected
	ProjectType string
}

// AutoActivate is a no-op in the Claude-style skill model.
//
// Deprecated: Triggers do not auto-activate skills in the on-demand invocation
// model. This method is preserved for backward compatibility with the TUI.
func (l *Loader) AutoActivate(ctx ActivationContext) []*Skill {
	return nil
}

// Update refreshes the plugin database.
func (l *Loader) Update(ctx context.Context) error {
	return l.Database.Update(ctx)
}

// List returns all available skills.
func (l *Loader) List() []*Skill {
	return l.Registry.List()
}

// ListInstalled returns installed plugins.
func (l *Loader) ListInstalled() []*InstalledPlugin {
	return l.Database.GetInstalled()
}

// ListCategories returns available plugin categories.
func (l *Loader) ListCategories() []string {
	return l.Database.GetCategories()
}

// GetFeatured returns featured plugins.
func (l *Loader) GetFeatured() []PluginEntry {
	return l.Database.GetFeatured()
}
