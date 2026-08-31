package plugins

import (
	"sort"
	"strings"
	"sync"
)

// Registry manages loaded plugins and provides access to their components.
type Registry struct {
	mu sync.RWMutex

	// Loaded plugins by name
	plugins map[string]*Plugin

	// Enabled plugins
	enabled map[string]bool

	// Command index: "namespace:command" -> plugin name
	commandIndex map[string]string

	// Agent index: "namespace:agent" -> plugin name
	agentIndex map[string]string
}

// NewRegistry creates a new plugin registry.
func NewRegistry() *Registry {
	return &Registry{
		plugins:      make(map[string]*Plugin),
		enabled:      make(map[string]bool),
		commandIndex: make(map[string]string),
		agentIndex:   make(map[string]string),
	}
}

// Register adds a plugin to the registry.
func (r *Registry) Register(plugin *Plugin) {
	r.mu.Lock()
	defer r.mu.Unlock()

	name := plugin.Manifest.Name
	r.plugins[name] = plugin
	r.enabled[name] = plugin.Enabled

	// Index commands
	for _, cmd := range plugin.Commands {
		fullName := name + ":" + cmd.Name
		r.commandIndex[fullName] = name
	}

	// Index agents
	for _, agent := range plugin.Agents {
		fullName := name + ":" + agent.Name
		r.agentIndex[fullName] = name
	}
}

// Unregister removes a plugin from the registry.
func (r *Registry) Unregister(name string) {
	r.mu.Lock()
	defer r.mu.Unlock()

	plugin, ok := r.plugins[name]
	if !ok {
		return
	}

	// Remove command index entries
	for _, cmd := range plugin.Commands {
		delete(r.commandIndex, name+":"+cmd.Name)
	}

	// Remove agent index entries
	for _, agent := range plugin.Agents {
		delete(r.agentIndex, name+":"+agent.Name)
	}

	delete(r.plugins, name)
	delete(r.enabled, name)
}

// Get returns a plugin by name.
func (r *Registry) Get(name string) *Plugin {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.plugins[name]
}

// List returns all registered plugins.
func (r *Registry) List() []*Plugin {
	r.mu.RLock()
	defer r.mu.RUnlock()

	plugins := make([]*Plugin, 0, len(r.plugins))
	for _, p := range r.plugins {
		plugins = append(plugins, p)
	}

	// Sort by name
	sort.Slice(plugins, func(i, j int) bool {
		return plugins[i].Manifest.Name < plugins[j].Manifest.Name
	})

	return plugins
}

// GetEnabled returns all enabled plugins.
func (r *Registry) GetEnabled() []*Plugin {
	r.mu.RLock()
	defer r.mu.RUnlock()

	var plugins []*Plugin
	for name, p := range r.plugins {
		if r.enabled[name] {
			plugins = append(plugins, p)
		}
	}

	// Sort by name
	sort.Slice(plugins, func(i, j int) bool {
		return plugins[i].Manifest.Name < plugins[j].Manifest.Name
	})

	return plugins
}

// Enable enables a plugin.
func (r *Registry) Enable(name string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if plugin, ok := r.plugins[name]; ok {
		r.enabled[name] = true
		plugin.Enabled = true
		return nil
	}
	return nil
}

// Disable disables a plugin.
func (r *Registry) Disable(name string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if plugin, ok := r.plugins[name]; ok {
		r.enabled[name] = false
		plugin.Enabled = false
		return nil
	}
	return nil
}

// IsEnabled checks if a plugin is enabled.
func (r *Registry) IsEnabled(name string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.enabled[name]
}

// Clear removes all plugins from the registry.
func (r *Registry) Clear() {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.plugins = make(map[string]*Plugin)
	r.enabled = make(map[string]bool)
	r.commandIndex = make(map[string]string)
	r.agentIndex = make(map[string]string)
}

// GetCommand returns a command and its plugin by full name (namespace:command).
func (r *Registry) GetCommand(fullName string) (*Plugin, *Command) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	pluginName, ok := r.commandIndex[fullName]
	if !ok {
		return nil, nil
	}

	plugin, ok := r.plugins[pluginName]
	if !ok || !r.enabled[pluginName] {
		return nil, nil
	}

	// Extract command name
	parts := strings.SplitN(fullName, ":", 2)
	if len(parts) != 2 {
		return nil, nil
	}

	cmd := plugin.GetCommand(parts[1])
	return plugin, cmd
}

// GetAgent returns an agent and its plugin by full name (namespace:agent).
func (r *Registry) GetAgent(fullName string) (*Plugin, *Agent) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	pluginName, ok := r.agentIndex[fullName]
	if !ok {
		return nil, nil
	}

	plugin, ok := r.plugins[pluginName]
	if !ok || !r.enabled[pluginName] {
		return nil, nil
	}

	// Extract agent name
	parts := strings.SplitN(fullName, ":", 2)
	if len(parts) != 2 {
		return nil, nil
	}

	agent := plugin.GetAgent(parts[1])
	return plugin, agent
}

// ListCommands returns all available command names from enabled plugins.
func (r *Registry) ListCommands() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()

	var commands []string
	for fullName, pluginName := range r.commandIndex {
		if r.enabled[pluginName] {
			commands = append(commands, fullName)
		}
	}

	sort.Strings(commands)
	return commands
}

// ListAgents returns all available agent names from enabled plugins.
func (r *Registry) ListAgents() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()

	var agents []string
	for fullName, pluginName := range r.agentIndex {
		if r.enabled[pluginName] {
			agents = append(agents, fullName)
		}
	}

	sort.Strings(agents)
	return agents
}

// Search searches plugins matching a query.
func (r *Registry) Search(query string) []PluginSearchResult {
	r.mu.RLock()
	defer r.mu.RUnlock()

	query = strings.ToLower(query)
	var results []PluginSearchResult

	for _, plugin := range r.plugins {
		score := r.calculateScore(plugin, query)
		if score > 0 {
			results = append(results, plugin.ToSearchResult())
		}
	}

	// Sort by score (approximated by relevance)
	sort.Slice(results, func(i, j int) bool {
		// Featured first
		if results[i].Featured != results[j].Featured {
			return results[i].Featured
		}
		// Then by name match
		iExact := strings.EqualFold(results[i].Name, query)
		jExact := strings.EqualFold(results[j].Name, query)
		if iExact != jExact {
			return iExact
		}
		// Then alphabetically
		return results[i].Name < results[j].Name
	})

	return results
}

// calculateScore calculates search relevance score.
func (r *Registry) calculateScore(plugin *Plugin, query string) int {
	score := 0

	// Name match
	if strings.Contains(strings.ToLower(plugin.Manifest.Name), query) {
		score += 10
		if strings.EqualFold(plugin.Manifest.Name, query) {
			score += 20
		}
	}

	// Description match
	if strings.Contains(strings.ToLower(plugin.Manifest.Description), query) {
		score += 5
	}

	// Keyword match
	for _, kw := range plugin.Manifest.Keywords {
		if strings.Contains(strings.ToLower(kw), query) {
			score += 3
		}
	}

	// Category match
	if strings.Contains(strings.ToLower(plugin.Manifest.Category), query) {
		score += 2
	}

	// Command name match
	for _, cmd := range plugin.Commands {
		if strings.Contains(strings.ToLower(cmd.Name), query) {
			score += 2
		}
	}

	// Agent name match
	for _, agent := range plugin.Agents {
		if strings.Contains(strings.ToLower(agent.Name), query) {
			score += 2
		}
	}

	return score
}

// Count returns the number of registered plugins.
func (r *Registry) Count() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.plugins)
}

// EnabledCount returns the number of enabled plugins.
func (r *Registry) EnabledCount() int {
	r.mu.RLock()
	defer r.mu.RUnlock()

	count := 0
	for _, enabled := range r.enabled {
		if enabled {
			count++
		}
	}
	return count
}

// GetByCategory returns plugins in a specific category.
func (r *Registry) GetByCategory(category string) []*Plugin {
	r.mu.RLock()
	defer r.mu.RUnlock()

	var plugins []*Plugin
	for _, p := range r.plugins {
		if strings.EqualFold(p.Manifest.Category, category) {
			plugins = append(plugins, p)
		}
	}

	sort.Slice(plugins, func(i, j int) bool {
		return plugins[i].Manifest.Name < plugins[j].Manifest.Name
	})

	return plugins
}

// GetCategories returns all plugin categories.
func (r *Registry) GetCategories() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()

	categories := make(map[string]bool)
	for _, p := range r.plugins {
		if p.Manifest.Category != "" {
			categories[p.Manifest.Category] = true
		}
	}

	result := make([]string, 0, len(categories))
	for cat := range categories {
		result = append(result, cat)
	}
	sort.Strings(result)

	return result
}

// GetPluginSkills returns all skills from a specific plugin.
func (r *Registry) GetPluginSkills(name string) []*Plugin {
	r.mu.RLock()
	defer r.mu.RUnlock()

	plugin, ok := r.plugins[name]
	if !ok {
		return nil
	}

	// Return the plugin if it has skills
	if len(plugin.Skills) > 0 {
		return []*Plugin{plugin}
	}
	return nil
}

// GetAllMCPServers returns all MCP servers from enabled plugins.
func (r *Registry) GetAllMCPServers() []MCPServer {
	r.mu.RLock()
	defer r.mu.RUnlock()

	var servers []MCPServer
	for name, plugin := range r.plugins {
		if r.enabled[name] {
			servers = append(servers, plugin.MCPServers...)
		}
	}
	return servers
}

// GetAllLSPServers returns all LSP servers from enabled plugins.
func (r *Registry) GetAllLSPServers() []LSPServer {
	r.mu.RLock()
	defer r.mu.RUnlock()

	var servers []LSPServer
	for name, plugin := range r.plugins {
		if r.enabled[name] {
			servers = append(servers, plugin.LSPServers...)
		}
	}
	return servers
}

// GetAllHooks returns all hooks from enabled plugins.
func (r *Registry) GetAllHooks() []*PluginHooks {
	r.mu.RLock()
	defer r.mu.RUnlock()

	var hooks []*PluginHooks
	for name, plugin := range r.plugins {
		if r.enabled[name] && plugin.Hooks != nil {
			hooks = append(hooks, plugin.Hooks)
		}
	}
	return hooks
}
