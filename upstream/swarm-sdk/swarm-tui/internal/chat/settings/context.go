package settings

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/tools/mcp"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/chat/commands"
	chatcontext "github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/chat/context"
)

// MCPContextProvider supplies MCP data needed for the picker.
type MCPContextProvider interface {
	GetServerStates() []*commands.MCPServerState
	GetAllResources() map[string][]*mcp.MCPResource
	GetAllPrompts() map[string][]*mcp.MCPPrompt
}

// ContextSettings handles context source configuration
type ContextSettings struct {
	loader      *chatcontext.FileLoader
	mcpProvider MCPContextProvider
}

// NewContextSettings creates a new context settings handler
func NewContextSettings() *ContextSettings {
	return &ContextSettings{
		loader: chatcontext.NewFileLoader(),
	}
}

// SetMCPProvider injects the MCP provider used for the picker.
func (c *ContextSettings) SetMCPProvider(provider MCPContextProvider) {
	c.mcpProvider = provider
}

// GetLoader returns the context loader
func (c *ContextSettings) GetLoader() *chatcontext.FileLoader {
	return c.loader
}

// GetConfig returns the current context configuration
func (c *ContextSettings) GetConfig() chatcontext.ContextConfig {
	return c.loader.GetConfig()
}

type contextRow struct {
	Source     chatcontext.ContextSourceConfig
	Global     chatcontext.ContextSourceConfig
	Project    chatcontext.ContextSourceConfig
	HasGlobal  bool
	HasProject bool
}

const (
	contextEditGlobal  = "global"
	contextEditProject = "project"
)

func (c *ContextSettings) getRows() []contextRow {
	merged := c.loader.GetConfig()
	globalCfg := c.loader.GetGlobalConfig()
	projectCfg := c.loader.GetProjectConfig()

	mergedSources := chatcontext.NormalizeSourceConfigs(merged.Sources)
	rows := make([]contextRow, 0, len(mergedSources))
	for _, source := range mergedSources {
		if source.ID == "" {
			continue
		}
		global, _, hasGlobal := chatcontext.FindSourceByID(globalCfg.Sources, source.ID)
		project, _, hasProject := chatcontext.FindSourceByID(projectCfg.Sources, source.ID)
		rows = append(rows, contextRow{
			Source:     source,
			Global:     global,
			Project:    project,
			HasGlobal:  hasGlobal,
			HasProject: hasProject,
		})
	}
	return rows
}

// Render renders the context settings view
func (c *ContextSettings) HandleKey(key string, state *State) bool {
	if state.ContextState == "" {
		state.ContextState = "list"
	}

	switch state.ContextState {
	case "mcp_servers":
		return c.handleMCPServerKey(key, state)
	case "mcp_picker":
		return c.handleMCPPickerKey(key, state)
	case "mcp_prompt_args":
		return c.handleMCPArgsKey(key, state)
	case "detail":
		return c.handleDetailKey(key, state)
	default:
		return c.handleMainContextKey(key, state)
	}
}

func (c *ContextSettings) handleMainContextKey(key string, state *State) bool {
	rows := c.getRows()
	if len(rows) == 0 {
		if key == "a" || key == "A" {
			state.ContextState = "mcp_servers"
			state.ContextMCPFilter = ""
			state.ContextMCPFilterMode = false
			state.ContextMCPServerIndex = 0
			state.ContextMCPSelectedServer = ""
			return true
		}
		return false
	}
	state.ContextSelectedItem = clampInt(state.ContextSelectedItem, 0, len(rows)-1)

	switch key {
	case "up", "k":
		if state.ContextSelectedItem > 0 {
			state.ContextSelectedItem--
		}
		return true
	case "down", "j":
		if state.ContextSelectedItem < len(rows)-1 {
			state.ContextSelectedItem++
		}
		return true
	case "g", "G":
		state.ContextEditTarget = contextEditGlobal
		return true
	case "p", "P":
		state.ContextEditTarget = contextEditProject
		return true
	case "left", "h":
		c.cycleRefreshMode(state, rows, -1)
		return true
	case "right", "l":
		c.cycleRefreshMode(state, rows, 1)
		return true
	case " ", "space":
		c.toggleSource(state, rows)
		return true
	case "c", "C":
		c.toggleCachePolicy(state, rows)
		return true
	case "enter":
		row := rows[state.ContextSelectedItem]
		state.ContextDetailSourceID = row.Source.ID
		state.ContextState = "detail"
		return true
	case "d", "D":
		c.deleteSource(state, rows)
		if state.ContextSelectedItem >= len(rows)-1 && state.ContextSelectedItem > 0 {
			state.ContextSelectedItem--
		}
		return true
	case "a", "A":
		state.ContextState = "mcp_servers"
		state.ContextMCPFilter = ""
		state.ContextMCPFilterMode = false
		state.ContextMCPServerIndex = 0
		state.ContextMCPSelectedServer = ""
		return true
	case "esc":
		state.Focus = FocusSidebar
		return true
	}

	return false
}

func (c *ContextSettings) cycleRefreshMode(state *State, rows []contextRow, delta int) {
	if len(rows) == 0 {
		return
	}
	row := rows[state.ContextSelectedItem]
	if row.Source.Kind == chatcontext.SourceKindInjection {
		return // refresh modes don't apply to injection gates
	}
	next := nextRefreshMode(row.Source.RefreshMode, row.Source.Kind, delta)
	c.updateSource(state, row, func(source *chatcontext.ContextSourceConfig) {
		source.RefreshMode = next
	})
}

func (c *ContextSettings) toggleSource(state *State, rows []contextRow) {
	if len(rows) == 0 {
		return
	}
	row := rows[state.ContextSelectedItem]
	c.updateSource(state, row, func(source *chatcontext.ContextSourceConfig) {
		source.Enabled = !source.Enabled
	})
}

func (c *ContextSettings) toggleCachePolicy(state *State, rows []contextRow) {
	if len(rows) == 0 {
		return
	}
	row := rows[state.ContextSelectedItem]
	if row.Source.Kind == chatcontext.SourceKindInjection {
		return // cache policies don't apply to injection gates
	}
	next := nextCachePolicy(row.Source.CachePolicy)
	c.updateSource(state, row, func(source *chatcontext.ContextSourceConfig) {
		source.CachePolicy = next
	})
}

func (c *ContextSettings) handleDetailKey(key string, state *State) bool {
	switch key {
	case "esc", "enter":
		state.ContextState = "list"
		state.ContextDetailSourceID = ""
		return true
	}
	return false
}

func (c *ContextSettings) updateSource(state *State, row contextRow, update func(*chatcontext.ContextSourceConfig)) {
	// Determine which config actually affects the merged view.
	// If a source has a project override, we must update the project config
	// because project sources replace global sources in mergeSources().
	// Otherwise, update the config based on the edit target (g for global, p for project).
	//
	// This ensures that toggling/enabling/disabling a source always has a visible effect
	// in the merged view, regardless of which config the user thinks they're editing.
	var target string
	if row.HasProject {
		target = contextEditProject
	} else {
		target = normalizeEditTarget(state.ContextEditTarget)
	}

	var cfg chatcontext.ContextConfig
	if target == contextEditProject {
		cfg = c.loader.GetProjectConfig()
	} else {
		cfg = c.loader.GetGlobalConfig()
	}

	source, idx, ok := chatcontext.FindSourceByID(cfg.Sources, row.Source.ID)
	if !ok {
		source = sourceForTarget(row, target)
		cfg.Sources = append(cfg.Sources, source)
		idx = len(cfg.Sources) - 1
	}

	update(&source)
	cfg.Sources[idx] = source

	if target == contextEditProject {
		_ = c.loader.SetProjectConfig(cfg)
	} else {
		_ = c.loader.SetGlobalConfig(cfg)
	}
}

func (c *ContextSettings) deleteSource(state *State, rows []contextRow) {
	if len(rows) == 0 {
		return
	}
	row := rows[state.ContextSelectedItem]
	target := normalizeEditTarget(state.ContextEditTarget)
	if !canDeleteSource(row, target) {
		return
	}

	var cfg chatcontext.ContextConfig
	if target == contextEditProject {
		cfg = c.loader.GetProjectConfig()
	} else {
		cfg = c.loader.GetGlobalConfig()
	}

	filtered := make([]chatcontext.ContextSourceConfig, 0, len(cfg.Sources))
	for _, source := range cfg.Sources {
		if source.ID == row.Source.ID {
			continue
		}
		filtered = append(filtered, source)
	}
	cfg.Sources = filtered

	if target == contextEditProject {
		_ = c.loader.SetProjectConfig(cfg)
	} else {
		_ = c.loader.SetGlobalConfig(cfg)
	}
}

func (c *ContextSettings) addMCPSource(target string, source chatcontext.MCPContextSource) {
	normalized := chatcontext.NormalizeMCPSource(source)
	configSource := chatcontext.ConfigSourceFromMCP(normalized)
	configSource.Enabled = true
	configSource.RefreshMode = chatcontext.RefreshOnChange
	configSource.CachePolicy = chatcontext.CacheCached

	target = normalizeEditTarget(target)
	var cfg chatcontext.ContextConfig
	if target == contextEditProject {
		cfg = c.loader.GetProjectConfig()
	} else {
		cfg = c.loader.GetGlobalConfig()
	}

	if existing, idx, ok := chatcontext.FindSourceByID(cfg.Sources, configSource.ID); ok {
		existing.Enabled = true
		if configSource.Label != "" {
			existing.Label = configSource.Label
		}
		if configSource.Kind == chatcontext.SourceKindMCPPrompt && configSource.PromptArgs != nil {
			existing.PromptArgs = configSource.PromptArgs
		}
		if existing.RefreshMode == "" || existing.RefreshMode == chatcontext.RefreshInherit {
			existing.RefreshMode = configSource.RefreshMode
		}
		if existing.TTLSeconds == nil {
			existing.TTLSeconds = configSource.TTLSeconds
		}
		if existing.CachePolicy == "" || existing.CachePolicy == chatcontext.CacheInherit {
			existing.CachePolicy = configSource.CachePolicy
		}
		cfg.Sources[idx] = existing
	} else {
		cfg.Sources = append(cfg.Sources, configSource)
	}

	if target == contextEditProject {
		_ = c.loader.SetProjectConfig(cfg)
	} else {
		_ = c.loader.SetGlobalConfig(cfg)
	}
}

func normalizeEditTarget(target string) string {
	if target == contextEditProject {
		return contextEditProject
	}
	return contextEditGlobal
}

func sourceForTarget(row contextRow, target string) chatcontext.ContextSourceConfig {
	switch normalizeEditTarget(target) {
	case contextEditProject:
		if row.HasProject {
			return row.Project
		}
		if row.HasGlobal {
			return row.Global
		}
	case contextEditGlobal:
		if row.HasGlobal {
			return row.Global
		}
	}
	return row.Source
}

func canDeleteSource(row contextRow, target string) bool {
	switch normalizeEditTarget(target) {
	case contextEditProject:
		return row.HasProject
	case contextEditGlobal:
		if !row.HasGlobal {
			return false
		}
		return !isBuiltinSource(row.Source.ID)
	default:
		return false
	}
}

func isBuiltinSource(id string) bool {
	_, ok := chatcontext.DefaultSourceConfig(id)
	return ok
}

func (c *ContextSettings) handleMCPServerKey(key string, state *State) bool {
	if state.ContextMCPFilterMode {
		switch key {
		case "esc", "enter":
			state.ContextMCPFilterMode = false
			return true
		case "backspace":
			if len(state.ContextMCPFilter) > 0 {
				state.ContextMCPFilter = state.ContextMCPFilter[:len(state.ContextMCPFilter)-1]
			}
			return true
		default:
			if key != "" && !isControlKey(key) {
				state.ContextMCPFilter += key
				return true
			}
		}
		return false
	}

	switch key {
	case "esc":
		state.ContextState = "list"
		return true
	case "/":
		state.ContextMCPFilterMode = true
		return true
	case "up", "k":
		if state.ContextMCPServerIndex > 0 {
			state.ContextMCPServerIndex--
		}
		return true
	case "down", "j":
		servers := filterServers(c.getSortedServers(), state.ContextMCPFilter)
		if state.ContextMCPServerIndex < len(servers)-1 {
			state.ContextMCPServerIndex++
		}
		return true
	case "enter":
		servers := filterServers(c.getSortedServers(), state.ContextMCPFilter)
		if len(servers) == 0 {
			return true
		}
		idx := clampInt(state.ContextMCPServerIndex, 0, len(servers)-1)
		selected := servers[idx]
		if selected != nil && selected.Config != nil {
			state.ContextMCPSelectedServer = selected.Config.Name
			state.ContextState = "mcp_picker"
			state.ContextMCPTab = 0
			state.ContextMCPResourceIndex = 0
			state.ContextMCPPromptIndex = 0
			state.ContextMCPFilter = ""
			state.ContextMCPFilterMode = false
		}
		return true
	}

	return false
}

func (c *ContextSettings) handleMCPPickerKey(key string, state *State) bool {
	if state.ContextMCPFilterMode {
		switch key {
		case "esc", "enter":
			state.ContextMCPFilterMode = false
			return true
		case "backspace":
			if len(state.ContextMCPFilter) > 0 {
				state.ContextMCPFilter = state.ContextMCPFilter[:len(state.ContextMCPFilter)-1]
			}
			return true
		default:
			if key != "" && !isControlKey(key) {
				state.ContextMCPFilter += key
				return true
			}
		}
		return false
	}

	switch key {
	case "esc":
		state.ContextState = "mcp_servers"
		return true
	case "/":
		state.ContextMCPFilterMode = true
		return true
	case "tab", "shift+tab":
		if state.ContextMCPTab == 0 {
			state.ContextMCPTab = 1
		} else {
			state.ContextMCPTab = 0
		}
		return true
	case "up", "k":
		if state.ContextMCPTab == 0 {
			if state.ContextMCPResourceIndex > 0 {
				state.ContextMCPResourceIndex--
			}
			return true
		}
		if state.ContextMCPPromptIndex > 0 {
			state.ContextMCPPromptIndex--
		}
		return true
	case "down", "j":
		if state.ContextMCPTab == 0 {
			resources := filterResources(c.getResourcesForServer(state.ContextMCPSelectedServer), state.ContextMCPFilter)
			if state.ContextMCPResourceIndex < len(resources)-1 {
				state.ContextMCPResourceIndex++
			}
			return true
		}
		prompts := filterPrompts(c.getPromptsForServer(state.ContextMCPSelectedServer), state.ContextMCPFilter)
		if state.ContextMCPPromptIndex < len(prompts)-1 {
			state.ContextMCPPromptIndex++
		}
		return true
	case "enter":
		if state.ContextMCPTab == 0 {
			resources := filterResources(c.getResourcesForServer(state.ContextMCPSelectedServer), state.ContextMCPFilter)
			if len(resources) == 0 {
				return true
			}
			idx := clampInt(state.ContextMCPResourceIndex, 0, len(resources)-1)
			resource := resources[idx]
			if resource == nil {
				return true
			}
			source := chatcontext.MCPContextSource{
				Enabled:    true,
				Kind:       chatcontext.MCPSourceResource,
				ServerName: state.ContextMCPSelectedServer,
				URI:        resource.URI,
				Label:      resource.Name,
			}
			c.addMCPSource(state.ContextEditTarget, source)
			return true
		}

		prompts := filterPrompts(c.getPromptsForServer(state.ContextMCPSelectedServer), state.ContextMCPFilter)
		if len(prompts) == 0 {
			return true
		}
		idx := clampInt(state.ContextMCPPromptIndex, 0, len(prompts)-1)
		prompt := prompts[idx]
		if prompt == nil {
			return true
		}
		state.ContextState = "mcp_prompt_args"
		state.ContextMCPArgsServer = state.ContextMCPSelectedServer
		state.ContextMCPArgsPromptName = prompt.Name
		state.ContextMCPArgsPromptArgs = prompt.Arguments
		state.ContextMCPArgsValues = make(map[string]string)
		state.ContextMCPArgsSelected = 0
		return true
	}

	return false
}

func (c *ContextSettings) handleMCPArgsKey(key string, state *State) bool {
	args := state.ContextMCPArgsPromptArgs
	maxIndex := len(args)
	state.ContextMCPArgsSelected = clampInt(state.ContextMCPArgsSelected, 0, maxIndex)

	switch key {
	case "esc":
		state.ContextState = "mcp_picker"
		return true
	case "up", "k", "shift+tab":
		if state.ContextMCPArgsSelected > 0 {
			state.ContextMCPArgsSelected--
		}
		return true
	case "down", "j", "tab":
		if state.ContextMCPArgsSelected < maxIndex {
			state.ContextMCPArgsSelected++
		}
		return true
	case "enter":
		if state.ContextMCPArgsSelected == maxIndex {
			values := make(map[string]string)
			for _, arg := range args {
				values[arg.Name] = state.ContextMCPArgsValues[arg.Name]
			}
			source := chatcontext.MCPContextSource{
				Enabled:    true,
				Kind:       chatcontext.MCPSourcePrompt,
				ServerName: state.ContextMCPArgsServer,
				PromptName: state.ContextMCPArgsPromptName,
				PromptArgs: values,
				Label:      state.ContextMCPArgsPromptName,
			}
			c.addMCPSource(state.ContextEditTarget, source)
			state.ContextState = "mcp_picker"
			return true
		}
		return true
	case "backspace":
		if state.ContextMCPArgsSelected < len(args) {
			arg := args[state.ContextMCPArgsSelected]
			val := state.ContextMCPArgsValues[arg.Name]
			if len(val) > 0 {
				state.ContextMCPArgsValues[arg.Name] = val[:len(val)-1]
			}
			return true
		}
		return true
	default:
		if state.ContextMCPArgsSelected < len(args) && key != "" && !isControlKey(key) {
			arg := args[state.ContextMCPArgsSelected]
			state.ContextMCPArgsValues[arg.Name] += key
			return true
		}
	}

	return false
}

func (c *ContextSettings) getSortedServers() []*commands.MCPServerState {
	if c.mcpProvider == nil {
		return nil
	}
	servers := c.mcpProvider.GetServerStates()
	sort.SliceStable(servers, func(i, j int) bool {
		nameI := ""
		nameJ := ""
		if servers[i] != nil && servers[i].Config != nil {
			nameI = servers[i].Config.Name
		}
		if servers[j] != nil && servers[j].Config != nil {
			nameJ = servers[j].Config.Name
		}
		return strings.ToLower(nameI) < strings.ToLower(nameJ)
	})
	return servers
}

func (c *ContextSettings) getResourcesForServer(server string) []*mcp.MCPResource {
	if c.mcpProvider == nil || server == "" {
		return nil
	}
	all := c.mcpProvider.GetAllResources()
	resources := all[server]
	sort.SliceStable(resources, func(i, j int) bool {
		return strings.ToLower(resourceLabel(resources[i])) < strings.ToLower(resourceLabel(resources[j]))
	})
	return resources
}

func (c *ContextSettings) getPromptsForServer(server string) []*mcp.MCPPrompt {
	if c.mcpProvider == nil || server == "" {
		return nil
	}
	all := c.mcpProvider.GetAllPrompts()
	prompts := all[server]
	sort.SliceStable(prompts, func(i, j int) bool {
		nameI := ""
		nameJ := ""
		if prompts[i] != nil {
			nameI = prompts[i].Name
		}
		if prompts[j] != nil {
			nameJ = prompts[j].Name
		}
		return strings.ToLower(nameI) < strings.ToLower(nameJ)
	})
	return prompts
}

func filterServers(servers []*commands.MCPServerState, query string) []*commands.MCPServerState {
	query = strings.ToLower(strings.TrimSpace(query))
	if query == "" {
		return servers
	}
	filtered := make([]*commands.MCPServerState, 0, len(servers))
	for _, server := range servers {
		name := ""
		if server != nil && server.Config != nil {
			name = server.Config.Name
		}
		if strings.Contains(strings.ToLower(name), query) {
			filtered = append(filtered, server)
		}
	}
	return filtered
}

func filterResources(resources []*mcp.MCPResource, query string) []*mcp.MCPResource {
	query = strings.ToLower(strings.TrimSpace(query))
	if query == "" {
		return resources
	}
	filtered := make([]*mcp.MCPResource, 0, len(resources))
	for _, resource := range resources {
		if resource == nil {
			continue
		}
		if strings.Contains(strings.ToLower(resource.Name), query) ||
			strings.Contains(strings.ToLower(resource.URI), query) ||
			strings.Contains(strings.ToLower(resource.Description), query) {
			filtered = append(filtered, resource)
		}
	}
	return filtered
}

func filterPrompts(prompts []*mcp.MCPPrompt, query string) []*mcp.MCPPrompt {
	query = strings.ToLower(strings.TrimSpace(query))
	if query == "" {
		return prompts
	}
	filtered := make([]*mcp.MCPPrompt, 0, len(prompts))
	for _, prompt := range prompts {
		if prompt == nil {
			continue
		}
		if strings.Contains(strings.ToLower(prompt.Name), query) ||
			strings.Contains(strings.ToLower(prompt.Description), query) {
			filtered = append(filtered, prompt)
		}
	}
	return filtered
}

func nextRefreshMode(current chatcontext.RefreshMode, kind chatcontext.ContextSourceKind, delta int) chatcontext.RefreshMode {
	modes := []chatcontext.RefreshMode{
		chatcontext.RefreshInherit,
		chatcontext.RefreshEveryMessage,
		chatcontext.RefreshEveryTurn,
	}
	if kind == chatcontext.SourceKindFile {
		modes = []chatcontext.RefreshMode{
			chatcontext.RefreshInherit,
			chatcontext.RefreshOnChange,
			chatcontext.RefreshEveryMessage,
			chatcontext.RefreshEveryTurn,
		}
	}

	current = normalizeRefreshMode(current)
	index := 0
	for i, mode := range modes {
		if mode == current {
			index = i
			break
		}
	}
	if delta == 0 {
		return current
	}
	steps := len(modes)
	index = (index + delta%steps + steps) % steps
	return modes[index]
}

func normalizeRefreshMode(mode chatcontext.RefreshMode) chatcontext.RefreshMode {
	switch mode {
	case chatcontext.RefreshInherit, chatcontext.RefreshOnChange, chatcontext.RefreshEveryMessage, chatcontext.RefreshEveryTurn:
		return mode
	default:
		return chatcontext.RefreshInherit
	}
}

func nextCachePolicy(current chatcontext.CachePolicy) chatcontext.CachePolicy {
	policies := []chatcontext.CachePolicy{
		chatcontext.CacheInherit,
		chatcontext.CacheCached,
		chatcontext.CacheEphemeral,
	}
	current = normalizeCachePolicy(current)
	index := 0
	for i, policy := range policies {
		if policy == current {
			index = i
			break
		}
	}
	index = (index + 1) % len(policies)
	return policies[index]
}

func normalizeCachePolicy(policy chatcontext.CachePolicy) chatcontext.CachePolicy {
	switch policy {
	case chatcontext.CacheInherit, chatcontext.CacheCached, chatcontext.CacheEphemeral:
		return policy
	default:
		return chatcontext.CacheInherit
	}
}

func refreshLabel(mode chatcontext.RefreshMode) string {
	mode = normalizeRefreshMode(mode)
	if mode == chatcontext.RefreshInherit {
		return "inherit"
	}
	return string(mode)
}

func cacheLabel(policy chatcontext.CachePolicy) string {
	policy = normalizeCachePolicy(policy)
	if policy == chatcontext.CacheInherit {
		return "inherit"
	}
	return string(policy)
}

func kindLabel(kind chatcontext.ContextSourceKind) string {
	switch kind {
	case chatcontext.SourceKindFile:
		return "file"
	case chatcontext.SourceKindDynamic:
		return "dynamic"
	case chatcontext.SourceKindMCPResource:
		return "mcp_res"
	case chatcontext.SourceKindMCPPrompt:
		return "mcp_prompt"
	case chatcontext.SourceKindInjection:
		return "inject"
	default:
		return "unknown"
	}
}

func scopeLabel(row contextRow) string {
	if row.HasProject {
		if row.HasGlobal {
			return "P*"
		}
		return "P"
	}
	if row.HasGlobal {
		return "G"
	}
	return "?"
}

func scopeDescription(row contextRow) string {
	if row.HasProject && row.HasGlobal {
		return "Project (overrides global)"
	}
	if row.HasProject {
		return "Project"
	}
	if row.HasGlobal {
		return "Global"
	}
	return "Unknown"
}

func rowLabel(row contextRow) string {
	if row.Source.Label != "" {
		return row.Source.Label
	}
	return row.Source.ID
}

func boolLabel(value bool) string {
	if value {
		return "enabled"
	}
	return "disabled"
}

func padCell(text string, width int) string {
	return lipgloss.NewStyle().Width(width).Render(text)
}

func contextTruncateString(value string, max int) string {
	if max <= 0 {
		return ""
	}
	runes := []rune(value)
	if len(runes) <= max {
		return value
	}
	if max <= 3 {
		return string(runes[:max])
	}
	return string(runes[:max-3]) + "..."
}

func resolveRefreshMode(source chatcontext.ContextSourceConfig, defaults chatcontext.ContextDefaults) chatcontext.RefreshMode {
	mode := source.RefreshMode
	if mode == "" || mode == chatcontext.RefreshInherit {
		mode = defaults.RefreshMode
	}
	if mode == "" || mode == chatcontext.RefreshInherit {
		mode = chatcontext.RefreshEveryMessage
	}
	if mode == chatcontext.RefreshOnChange && source.Kind != chatcontext.SourceKindFile {
		mode = chatcontext.RefreshEveryMessage
	}
	return mode
}

func resolveCachePolicy(source chatcontext.ContextSourceConfig, defaults chatcontext.ContextDefaults) chatcontext.CachePolicy {
	policy := source.CachePolicy
	if policy == "" || policy == chatcontext.CacheInherit {
		policy = defaults.CachePolicy
	}
	if policy == "" || policy == chatcontext.CacheInherit {
		return chatcontext.CacheCached
	}
	return policy
}

func renderDetailLine(label, value string, th Theme) string {
	keyStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(th.TextMuted))
	valStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(th.Text))
	return fmt.Sprintf("%s %s", keyStyle.Render(label+":"), valStyle.Render(value))
}

func findRowByID(rows []contextRow, id string) (contextRow, bool) {
	if id == "" {
		return contextRow{}, false
	}
	for _, row := range rows {
		if row.Source.ID == id {
			return row, true
		}
	}
	return contextRow{}, false
}

func formatPromptArgs(args map[string]string) string {
	if len(args) == 0 {
		return "none"
	}
	keys := make([]string, 0, len(args))
	for key := range args {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		parts = append(parts, fmt.Sprintf("%s=%s", key, args[key]))
	}
	return strings.Join(parts, ", ")
}

func resolveFilePath(sourceID string) (string, bool, []string) {
	home, _ := os.UserHomeDir()
	workDir, _ := os.Getwd()
	var candidates []string
	switch sourceID {
	case chatcontext.SourceIDGlobalClaudeMd:
		candidates = []string{filepath.Join(home, ".claude", "CLAUDE.md")}
	case chatcontext.SourceIDGlobalSwarmMd:
		candidates = []string{filepath.Join(home, ".swarm", "SWARM.md")}
	case chatcontext.SourceIDProjectClaudeMd:
		candidates = []string{
			filepath.Join(workDir, ".claude", "CLAUDE.md"),
			filepath.Join(workDir, "CLAUDE.md"),
		}
	case chatcontext.SourceIDProjectSwarmMd:
		candidates = []string{
			filepath.Join(workDir, ".swarm", "SWARM.md"),
			filepath.Join(workDir, "SWARM.md"),
		}
	case chatcontext.SourceIDAgentsMd:
		candidates = []string{
			filepath.Join(workDir, ".swarm", "AGENTS.md"),
			filepath.Join(workDir, ".claude", "AGENTS.md"),
			filepath.Join(workDir, "AGENTS.md"),
		}
	case chatcontext.SourceIDIndexMd:
		candidates = []string{
			filepath.Join(workDir, "INDEX.md"),
		}
	default:
		return "", false, nil
	}

	for _, candidate := range candidates {
		if _, err := os.Stat(candidate); err == nil {
			return candidate, true, candidates
		}
	}
	if len(candidates) > 0 {
		return candidates[0], false, candidates
	}
	return "", false, candidates
}

func formatPromptArgsSummary(prompt *mcp.MCPPrompt) string {
	if prompt == nil || len(prompt.Arguments) == 0 {
		return ""
	}

	var required []string
	var optional []string
	for _, arg := range prompt.Arguments {
		if arg.Required {
			required = append(required, arg.Name)
		} else {
			optional = append(optional, arg.Name)
		}
	}

	var parts []string
	if len(required) > 0 {
		sort.Strings(required)
		parts = append(parts, "req: "+strings.Join(required, ", "))
	}
	if len(optional) > 0 {
		sort.Strings(optional)
		parts = append(parts, "opt: "+strings.Join(optional, ", "))
	}
	if len(parts) == 0 {
		return ""
	}
	return " (" + strings.Join(parts, "; ") + ")"
}

func resourceLabel(resource *mcp.MCPResource) string {
	if resource == nil {
		return ""
	}
	if resource.Name != "" {
		return resource.Name
	}
	return resource.URI
}

func clampInt(value, minValue, maxValue int) int {
	if value < minValue {
		return minValue
	}
	if value > maxValue {
		return maxValue
	}
	return value
}

func windowBounds(selected, total, maxVisible int) (int, int) {
	if maxVisible <= 0 || total <= maxVisible {
		return 0, total
	}
	start := selected - maxVisible + 1
	if start < 0 {
		start = 0
	}
	if start+maxVisible > total {
		start = total - maxVisible
	}
	if start < 0 {
		start = 0
	}
	end := start + maxVisible
	if end > total {
		end = total
	}
	return start, end
}
