package chat

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/plugins"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/skills"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/chat/commands"
)

// PluginsManager provides a unified interface for managing plugins and skills in the TUI.
type PluginsManager struct {
	mu sync.RWMutex

	// Plugin loader
	pluginLoader *plugins.Loader

	// Skills loader (for integration)
	skillsLoader *skills.Loader

	// Marketplace database
	marketplace *plugins.MarketplaceDatabase

	// Unified searcher
	searcher *plugins.UnifiedSearcher

	// Install directory
	installDir string

	// Initialized flag
	initialized bool

	// Callbacks
	onPluginEnabled  func(name string)
	onPluginDisabled func(name string)
	onPluginChanged  func()
}

// NewPluginsManager creates a new plugins manager.
func NewPluginsManager(skillsLoader *skills.Loader) *PluginsManager {
	installDir := plugins.DefaultPluginsDir()
	cacheDir := filepath.Join(installDir, ".cache")

	pluginLoader := plugins.NewLoader(installDir, skillsLoader)
	marketplace := plugins.NewMarketplaceDatabase(cacheDir)

	return &PluginsManager{
		pluginLoader: pluginLoader,
		skillsLoader: skillsLoader,
		marketplace:  marketplace,
		searcher:     plugins.NewUnifiedSearcher(skillsLoader, pluginLoader, marketplace),
		installDir:   installDir,
	}
}

// Initialize loads plugins and marketplace data.
func (m *PluginsManager) Initialize(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.initialized {
		return nil
	}

	// Add project-level plugin paths BEFORE initialization so discovery covers them
	m.addProjectPaths()

	// Initialize plugin loader (discovers all search paths, including project paths above)
	if err := m.pluginLoader.Initialize(ctx); err != nil {
		return fmt.Errorf("failed to initialize plugin loader: %w", err)
	}

	// Load marketplace cache
	if err := m.marketplace.LoadCache(); err != nil {
		// Log but don't fail
		fmt.Printf("warning: failed to load marketplace cache: %v\n", err)
	}

	m.initialized = true
	return nil
}

// addProjectPaths adds project-level plugin directories.
func (m *PluginsManager) addProjectPaths() {
	// Add .swarmos/plugins if it exists
	swarmosPlugins := plugins.ProjectPluginsDir()
	if info, err := os.Stat(swarmosPlugins); err == nil && info.IsDir() {
		m.pluginLoader.AddSearchPath(swarmosPlugins)
	}

	// Add .claude/plugins for Claude Code compatibility
	claudePlugins := plugins.ClaudePluginsDir()
	if info, err := os.Stat(claudePlugins); err == nil && info.IsDir() {
		m.pluginLoader.AddSearchPath(claudePlugins)
	}
}

// SetCallbacks sets the event callbacks.
func (m *PluginsManager) SetCallbacks(
	onEnabled func(name string),
	onDisabled func(name string),
	onChange func(),
) {
	m.onPluginEnabled = onEnabled
	m.onPluginDisabled = onDisabled
	m.onPluginChanged = onChange
}

// GetPlugins returns all loaded plugins.
func (m *PluginsManager) GetPlugins() []*plugins.Plugin {
	return m.pluginLoader.List()
}

// GetEnabledPlugins returns all enabled plugins.
func (m *PluginsManager) GetEnabledPlugins() []*plugins.Plugin {
	return m.pluginLoader.GetEnabled()
}

// GetPlugin returns a plugin by name.
func (m *PluginsManager) GetPlugin(name string) *plugins.Plugin {
	return m.pluginLoader.Get(name)
}

// EnablePlugin enables a plugin.
func (m *PluginsManager) EnablePlugin(name string) error {
	if err := m.pluginLoader.Enable(name); err != nil {
		return err
	}
	if m.onPluginEnabled != nil {
		m.onPluginEnabled(name)
	}
	if m.onPluginChanged != nil {
		m.onPluginChanged()
	}
	return nil
}

// DisablePlugin disables a plugin.
func (m *PluginsManager) DisablePlugin(name string) error {
	if err := m.pluginLoader.Disable(name); err != nil {
		return err
	}
	if m.onPluginDisabled != nil {
		m.onPluginDisabled(name)
	}
	if m.onPluginChanged != nil {
		m.onPluginChanged()
	}
	return nil
}

// TogglePlugin toggles a plugin's enabled state.
func (m *PluginsManager) TogglePlugin(name string) error {
	plugin := m.pluginLoader.Get(name)
	if plugin == nil {
		return fmt.Errorf("plugin %q not found", name)
	}

	if plugin.Enabled {
		return m.DisablePlugin(name)
	}
	return m.EnablePlugin(name)
}

// InstallPlugin installs a plugin from a source.
func (m *PluginsManager) InstallPlugin(ctx context.Context, source, name string) (*plugins.Plugin, error) {
	plugin, err := m.pluginLoader.Install(ctx, source, name)
	if err != nil {
		return nil, err
	}
	if m.onPluginChanged != nil {
		m.onPluginChanged()
	}
	return plugin, nil
}

// InstallFromMarketplace installs a plugin from the marketplace.
func (m *PluginsManager) InstallFromMarketplace(ctx context.Context, name string) (*plugins.Plugin, error) {
	// Find the plugin in marketplace
	entry := m.marketplace.Get(name)
	if entry == nil {
		return nil, fmt.Errorf("plugin %q not found in marketplace", name)
	}

	// Install from source
	return m.InstallPlugin(ctx, entry.Source, entry.Name)
}

// UninstallPlugin uninstalls a plugin.
func (m *PluginsManager) UninstallPlugin(name string) error {
	if err := m.pluginLoader.Uninstall(name); err != nil {
		return err
	}
	if m.onPluginChanged != nil {
		m.onPluginChanged()
	}
	return nil
}

// Refresh reloads all plugins.
func (m *PluginsManager) Refresh(ctx context.Context) error {
	if err := m.pluginLoader.Refresh(ctx); err != nil {
		return err
	}
	if m.onPluginChanged != nil {
		m.onPluginChanged()
	}
	return nil
}

// UpdateMarketplace updates the marketplace index.
func (m *PluginsManager) UpdateMarketplace(ctx context.Context) error {
	return m.marketplace.Update(ctx)
}

// Search performs a unified search.
func (m *PluginsManager) Search(ctx context.Context, query string) ([]plugins.UnifiedSearchResult, error) {
	return m.searcher.QuickSearch(ctx, query)
}

// SearchWithFilter performs a filtered search.
func (m *PluginsManager) SearchWithFilter(ctx context.Context, filter plugins.SearchFilter) ([]plugins.UnifiedSearchResult, error) {
	return m.searcher.Search(ctx, filter)
}

// SearchPluginsOnly searches only plugins.
func (m *PluginsManager) SearchPluginsOnly(ctx context.Context, query string) ([]plugins.UnifiedSearchResult, error) {
	return m.searcher.SearchPluginsOnly(ctx, query)
}

// SearchSkillsOnly searches only skills.
func (m *PluginsManager) SearchSkillsOnly(ctx context.Context, query string) ([]plugins.UnifiedSearchResult, error) {
	return m.searcher.SearchSkillsOnly(ctx, query)
}

// GetCategories returns all categories.
func (m *PluginsManager) GetCategories() []string {
	return m.searcher.GetCategories()
}

// GetFeatured returns featured items.
func (m *PluginsManager) GetFeatured() []plugins.UnifiedSearchResult {
	return m.searcher.GetFeatured()
}

// GetMarketplaceSources returns all marketplace sources.
func (m *PluginsManager) GetMarketplaceSources() []plugins.MarketplaceSource {
	return m.marketplace.GetSources()
}

// AddMarketplaceSource adds a marketplace source.
func (m *PluginsManager) AddMarketplaceSource(source plugins.MarketplaceSource) {
	m.marketplace.AddSource(source)
}

// AddGitHubMarketplace adds a GitHub marketplace.
func (m *PluginsManager) AddGitHubMarketplace(owner, repo string) {
	m.marketplace.AddGitHubMarketplace(owner, repo)
}

// RemoveMarketplaceSource removes a marketplace source.
func (m *PluginsManager) RemoveMarketplaceSource(name string) {
	m.marketplace.RemoveSource(name)
}

// GetCommand returns a command from any enabled plugin.
func (m *PluginsManager) GetCommand(fullName string) (*plugins.Plugin, *plugins.Command) {
	return m.pluginLoader.GetCommand(fullName)
}

// GetAgent returns an agent from any enabled plugin.
func (m *PluginsManager) GetAgent(fullName string) (*plugins.Plugin, *plugins.Agent) {
	return m.pluginLoader.GetAgent(fullName)
}

// ListCommands returns all available commands.
func (m *PluginsManager) ListCommands() []string {
	return m.pluginLoader.ListCommands()
}

// GetEnabledPluginCommands returns all commands from enabled plugins with metadata.
// This implements the PluginCommandProvider interface for autocomplete.
func (m *PluginsManager) GetEnabledPluginCommands() []commands.PluginCommandMatch {
	enabledPlugins := m.pluginLoader.GetEnabled()
	var cmds []commands.PluginCommandMatch

	for _, plugin := range enabledPlugins {
		for _, cmd := range plugin.Commands {
			cmds = append(cmds, commands.PluginCommandMatch{
				FullName:     plugin.Manifest.Name + ":" + cmd.Name,
				PluginName:   plugin.Manifest.Name,
				CommandName:  cmd.Name,
				Description:  cmd.Description,
				ArgumentHint: cmd.ArgumentHint,
			})
		}
	}

	return cmds
}

// ListAgents returns all available agents.
func (m *PluginsManager) ListAgents() []string {
	return m.pluginLoader.ListAgents()
}

// GetAllMCPServers returns MCP servers from all enabled plugins.
func (m *PluginsManager) GetAllMCPServers() []plugins.MCPServer {
	return m.pluginLoader.Registry.GetAllMCPServers()
}

// GetMCPServerConfigs returns MCP servers in a format compatible with MCPManager.
// This converts plugin MCP servers to commands.MCPServerConfig format.
func (m *PluginsManager) GetMCPServerConfigs() []*commands.MCPServerConfig {
	pluginServers := m.pluginLoader.Registry.GetAllMCPServers()
	configs := make([]*commands.MCPServerConfig, 0, len(pluginServers))

	for _, srv := range pluginServers {
		serverType := commands.MCPTypeStdio // Default to stdio
		switch srv.Type {
		case "sse":
			serverType = commands.MCPTypeSSE
		case "http":
			serverType = commands.MCPTypeHTTP
		case "stdio":
			serverType = commands.MCPTypeStdio
		}

		config := &commands.MCPServerConfig{
			Name:    srv.Name,
			Type:    serverType,
			Command: srv.Command,
			Args:    srv.Args,
			URL:     srv.URL,
			Env:     srv.Environment,
			Enabled: srv.Enabled,
		}
		configs = append(configs, config)
	}

	return configs
}

// GetAllLSPServers returns LSP servers from all enabled plugins.
func (m *PluginsManager) GetAllLSPServers() []plugins.LSPServer {
	return m.pluginLoader.Registry.GetAllLSPServers()
}

// GetAllHooks returns hooks from all enabled plugins.
func (m *PluginsManager) GetAllHooks() []*plugins.PluginHooks {
	return m.pluginLoader.Registry.GetAllHooks()
}

// GetCounts returns counts of skills, plugins, and marketplace items.
func (m *PluginsManager) GetCounts() (skills, pluginsCount, marketplace int) {
	return m.searcher.Count()
}

// GetPluginSource returns the source type for a plugin.
func (m *PluginsManager) GetPluginSource(plugin *plugins.Plugin) string {
	switch plugin.Source {
	case plugins.SourceUser:
		return "USR"
	case plugins.SourceProject:
		return "PRJ"
	case plugins.SourceMarketplace:
		return "MKT"
	case plugins.SourceGit:
		return "GIT"
	default:
		return "LOC"
	}
}

// IsInitialized returns whether the manager is initialized.
func (m *PluginsManager) IsInitialized() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.initialized
}

// GetLoader returns the plugin loader (for advanced usage).
func (m *PluginsManager) GetLoader() *plugins.Loader {
	return m.pluginLoader
}

// GetMarketplace returns the marketplace database (for advanced usage).
func (m *PluginsManager) GetMarketplace() *plugins.MarketplaceDatabase {
	return m.marketplace
}

// GetSearcher returns the unified searcher (for advanced usage).
func (m *PluginsManager) GetSearcher() *plugins.UnifiedSearcher {
	return m.searcher
}
