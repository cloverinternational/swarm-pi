package plugins

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/paths"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/skills"
)

// Loader handles plugin discovery, loading, and lifecycle management.
type Loader struct {
	// Registry for loaded plugins
	Registry *Registry

	// SkillLoader for loading skills within plugins
	SkillLoader *skills.Loader

	// InstallDir is the default installation directory
	InstallDir string

	// SearchPaths for plugin discovery
	SearchPaths []string

	// Initialized indicates if the loader has been initialized
	Initialized bool
}

// NewLoader creates a new plugin loader.
func NewLoader(installDir string, skillLoader *skills.Loader) *Loader {
	loader := &Loader{
		Registry:    NewRegistry(),
		SkillLoader: skillLoader,
		InstallDir:  installDir,
		SearchPaths: []string{
			installDir,
			filepath.Join(installDir, "plugins"),
		},
	}

	return loader
}

// Initialize loads installed plugins and discovers available plugins.
func (l *Loader) Initialize(ctx context.Context) error {
	// Ensure install directory exists
	if err := os.MkdirAll(l.InstallDir, 0755); err != nil {
		return fmt.Errorf("failed to create plugin install directory: %w", err)
	}

	// Discover and load plugins from all search paths
	for _, searchPath := range l.SearchPaths {
		if err := l.discoverPath(searchPath); err != nil {
			// Log but don't fail on individual path errors
			continue
		}
	}

	l.Initialized = true
	return nil
}

// AddSearchPath adds a directory to search for plugins.
// If the loader is already initialized, the path is discovered immediately
// so callers don't have to call Refresh just to pick up a newly-registered path.
func (l *Loader) AddSearchPath(path string) {
	l.SearchPaths = append(l.SearchPaths, path)
	if l.Initialized {
		_ = l.discoverPath(path)
	}
}

// discoverPath discovers and loads plugins from a single path.
func (l *Loader) discoverPath(path string) error {
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return nil // Path doesn't exist, skip silently
	}

	pluginDirs, err := FindPluginDirectories(path)
	if err != nil {
		return fmt.Errorf("failed to find plugins in %s: %w", path, err)
	}

	for _, pluginDir := range pluginDirs {
		plugin, err := l.LoadPlugin(pluginDir)
		if err != nil {
			// Log but continue loading other plugins
			continue
		}

		// Determine source based on path
		plugin.Source = determineSource(pluginDir, l.InstallDir)

		// Register the plugin
		l.Registry.Register(plugin)
	}

	return nil
}

// determineSource determines the plugin source based on its path.
func determineSource(pluginPath, installDir string) PluginSource {
	userPluginsDir := paths.In("plugins")

	if hasPrefix(pluginPath, userPluginsDir) {
		return SourceUser
	}
	if hasPrefix(pluginPath, installDir) {
		return SourceMarketplace
	}
	if hasPrefix(pluginPath, ".swarm") || hasPrefix(pluginPath, ".claude") {
		return SourceProject
	}
	return SourceLocal
}

// hasPrefix checks if path starts with prefix (handles symlinks).
func hasPrefix(path, prefix string) bool {
	absPath, _ := filepath.Abs(path)
	absPrefix, _ := filepath.Abs(prefix)
	return len(absPath) >= len(absPrefix) && absPath[:len(absPrefix)] == absPrefix
}

// LoadPlugin loads a plugin from a directory.
func (l *Loader) LoadPlugin(path string) (*Plugin, error) {
	// Verify it's a plugin directory
	if !IsPluginDirectory(path) {
		return nil, fmt.Errorf("not a valid plugin directory: %s", path)
	}

	// Parse manifest
	manifestPath := filepath.Join(path, PluginDir, ManifestFile)
	manifest, err := ParseManifest(manifestPath)
	if err != nil {
		return nil, fmt.Errorf("failed to parse manifest: %w", err)
	}

	plugin := &Plugin{
		Manifest: *manifest,
		Path:     path,
		Enabled:  true,
		LoadedAt: time.Now(),
	}

	// Load commands
	commandsDir := filepath.Join(path, "commands")
	if commands, err := ParseCommandsDir(commandsDir); err == nil {
		plugin.Commands = commands
	}

	// Load agents
	agentsDir := filepath.Join(path, "agents")
	if agents, err := ParseAgentsDir(agentsDir); err == nil {
		plugin.Agents = agents
	}

	// Load skills using the skills loader
	skillsDir := filepath.Join(path, "skills")
	if _, err := os.Stat(skillsDir); err == nil {
		// Discover skills in the plugin's skills directory
		if pluginSkills, err := skills.DiscoverSkills(skillsDir); err == nil {
			plugin.Skills = pluginSkills
		}
	}

	// Load hooks
	hooksPath := filepath.Join(path, "hooks", "hooks.json")
	if hooks, err := ParseHooks(hooksPath); err == nil {
		plugin.Hooks = hooks
	}

	// Load MCP servers
	mcpPath := filepath.Join(path, ".mcp.json")
	if servers, err := ParseMCPConfig(mcpPath); err == nil {
		plugin.MCPServers = servers
	}

	// Load LSP servers
	lspPath := filepath.Join(path, ".lsp.json")
	if servers, err := ParseLSPConfig(lspPath); err == nil {
		plugin.LSPServers = servers
	}

	return plugin, nil
}

// Install installs a plugin from a source path or URL.
func (l *Loader) Install(ctx context.Context, source string, name string) (*Plugin, error) {
	// Determine destination directory
	destDir := filepath.Join(l.InstallDir, name)

	// Check if already installed
	if _, err := os.Stat(destDir); err == nil {
		return nil, fmt.Errorf("plugin %q already installed at %s", name, destDir)
	}

	// Handle different source types
	if strings.HasPrefix(source, "http://") || strings.HasPrefix(source, "https://") || strings.HasPrefix(source, "git@") || strings.HasSuffix(source, ".git") || strings.Contains(source, ".git#") {
		// Git source - needs cloning
		return l.installFromGit(ctx, source, name, destDir)
	}

	// Local directory - copy files
	if err := copyDir(source, destDir); err != nil {
		return nil, fmt.Errorf("failed to copy plugin: %w", err)
	}

	// Load the installed plugin
	plugin, err := l.LoadPlugin(destDir)
	if err != nil {
		// Clean up failed installation
		os.RemoveAll(destDir)
		return nil, fmt.Errorf("failed to load installed plugin: %w", err)
	}

	plugin.Source = SourceMarketplace
	plugin.InstalledAt = time.Now()

	// Register the plugin
	l.Registry.Register(plugin)

	return plugin, nil
}

// installFromGit clones a plugin from a Git repository.
func (l *Loader) installFromGit(ctx context.Context, source, name, destDir string) (*Plugin, error) {
	// Parse Git URL and subdirectory
	// Format: https://github.com/owner/repo.git#subdirectory
	gitURL := source
	subdirectory := ""

	if before, after, ok := strings.Cut(source, "#"); ok {
		gitURL = before
		subdirectory = after
	}

	// Create temporary directory for cloning
	tmpDir := filepath.Join(os.TempDir(), "swarmos-plugin-install-"+name)
	defer os.RemoveAll(tmpDir)

	// Clone the repository
	cmd := exec.CommandContext(ctx, "git", "clone", "--depth", "1", gitURL, tmpDir)
	if output, err := cmd.CombinedOutput(); err != nil {
		return nil, fmt.Errorf("failed to clone repository: %w\nOutput: %s", err, output)
	}

	// Determine source directory
	sourceDir := tmpDir
	if subdirectory != "" {
		sourceDir = filepath.Join(tmpDir, subdirectory)
		if _, err := os.Stat(sourceDir); err != nil {
			return nil, fmt.Errorf("subdirectory %q not found in repository", subdirectory)
		}
	}

	// Copy to destination
	if err := copyDir(sourceDir, destDir); err != nil {
		return nil, fmt.Errorf("failed to copy plugin files: %w", err)
	}

	// Load the installed plugin
	plugin, err := l.LoadPlugin(destDir)
	if err != nil {
		// Clean up failed installation
		os.RemoveAll(destDir)
		return nil, fmt.Errorf("failed to load installed plugin: %w", err)
	}

	plugin.Source = SourceGit
	plugin.InstalledAt = time.Now()

	// Register the plugin
	l.Registry.Register(plugin)

	return plugin, nil
}

// Uninstall removes an installed plugin.
func (l *Loader) Uninstall(name string) error {
	plugin := l.Registry.Get(name)
	if plugin == nil {
		return fmt.Errorf("plugin %q not found", name)
	}

	// Disable first
	l.Registry.Disable(name)

	// Unregister
	l.Registry.Unregister(name)

	// Remove files if installed from marketplace
	if plugin.Source == SourceMarketplace {
		if err := os.RemoveAll(plugin.Path); err != nil {
			return fmt.Errorf("failed to remove plugin files: %w", err)
		}
	}

	return nil
}

// Enable enables a plugin.
func (l *Loader) Enable(name string) error {
	return l.Registry.Enable(name)
}

// Disable disables a plugin.
func (l *Loader) Disable(name string) error {
	return l.Registry.Disable(name)
}

// List returns all loaded plugins.
func (l *Loader) List() []*Plugin {
	return l.Registry.List()
}

// Get returns a plugin by name.
func (l *Loader) Get(name string) *Plugin {
	return l.Registry.Get(name)
}

// GetEnabled returns all enabled plugins.
func (l *Loader) GetEnabled() []*Plugin {
	return l.Registry.GetEnabled()
}

// GetCommand returns a command from any enabled plugin.
func (l *Loader) GetCommand(fullName string) (*Plugin, *Command) {
	return l.Registry.GetCommand(fullName)
}

// GetAgent returns an agent from any enabled plugin.
func (l *Loader) GetAgent(fullName string) (*Plugin, *Agent) {
	return l.Registry.GetAgent(fullName)
}

// ListCommands returns all available commands from enabled plugins.
func (l *Loader) ListCommands() []string {
	return l.Registry.ListCommands()
}

// ListAgents returns all available agents from enabled plugins.
func (l *Loader) ListAgents() []string {
	return l.Registry.ListAgents()
}

// Refresh reloads all plugins from search paths.
func (l *Loader) Refresh(ctx context.Context) error {
	// Clear existing plugins
	l.Registry.Clear()

	// Reinitialize
	l.Initialized = false
	return l.Initialize(ctx)
}

// Search searches for plugins matching a query.
func (l *Loader) Search(query string) []PluginSearchResult {
	return l.Registry.Search(query)
}

// copyDir copies a directory recursively.
func copyDir(src, dst string) error {
	return filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		// Calculate destination path
		relPath, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		dstPath := filepath.Join(dst, relPath)

		if info.IsDir() {
			return os.MkdirAll(dstPath, info.Mode())
		}

		// Copy file
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}

		return os.WriteFile(dstPath, data, info.Mode())
	})
}

// DefaultPluginsDir returns the default plugins installation directory.
func DefaultPluginsDir() string {
	return paths.In("plugins")
}

// ProjectPluginsDir returns the project-level plugins directory.
func ProjectPluginsDir() string {
	return filepath.Join(".swarm", "plugins")
}

// ClaudePluginsDir returns the Claude-style project plugins directory.
func ClaudePluginsDir() string {
	return ".claude/plugins"
}
