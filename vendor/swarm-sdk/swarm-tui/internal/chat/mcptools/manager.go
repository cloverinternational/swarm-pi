package mcptools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/observability"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/plugins"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/tools"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/tools/mcp"

	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/chat/commands"
)

// sanitizeToolName sanitizes a tool name to match Anthropic's requirements
// Pattern: ^[a-zA-Z0-9_-]{1,128}$
func sanitizeToolName(name string) string {
	// Replace invalid characters with underscores
	re := regexp.MustCompile(`[^a-zA-Z0-9_-]`)
	sanitized := re.ReplaceAllString(name, "_")

	// Ensure length <= 128
	if len(sanitized) > 128 {
		sanitized = sanitized[:128]
	}

	// Ensure not empty
	if sanitized == "" {
		sanitized = "tool"
	}

	return sanitized
}

// parseScopes converts a space-separated scope string into a slice
func parseScopes(scopes string) []string {
	if scopes == "" {
		return nil
	}
	parts := regexp.MustCompile(`\s+`).Split(scopes, -1)
	result := make([]string, 0, len(parts))
	for _, part := range parts {
		if part != "" {
			result = append(result, part)
		}
	}
	return result
}

// MCPManager manages MCP server connections and tools
type MCPManager struct {
	servers      map[string]*commands.MCPServerState
	configMgr    *commands.MCPConfigManager
	toolRegistry tools.Registry
	logger       observability.Logger
	tracer       observability.Tracer
	projectDir   string // workspace root for project-level MCP discovery
	mu           sync.RWMutex
}

// NewMCPManager creates a new MCP manager.
// projectDir is the workspace root used to discover project-level MCP configs
// (mcp.json, .mcp.json, .swarm/mcp.json, .swarm/.mcp.json). Pass "" to skip discovery.
func NewMCPManager(toolRegistry tools.Registry, logger observability.Logger, tracer observability.Tracer, projectDir string) (*MCPManager, error) {
	configMgr, err := commands.NewMCPConfigManager()
	if err != nil {
		return nil, fmt.Errorf("failed to create config manager: %w", err)
	}

	return &MCPManager{
		servers:      make(map[string]*commands.MCPServerState),
		configMgr:    configMgr,
		toolRegistry: toolRegistry,
		logger:       logger,
		tracer:       tracer,
		projectDir:   projectDir,
	}, nil
}

// GetServerNames returns a list of all server names (for cache invalidation)
func (m *MCPManager) GetServerNames() []string {
	m.mu.RLock()
	defer m.mu.RUnlock()

	names := make([]string, 0, len(m.servers))
	for name := range m.servers {
		names = append(names, name)
	}
	return names
}

// LoadAndConnect loads server configs and connects to enabled servers.
// It first loads the user's global config from ~/.swarmos/mcp_servers.json,
// then discovers and merges any project-level MCP configs found in the workspace.
func (m *MCPManager) LoadAndConnect(ctx context.Context) error {
	configs, err := m.configMgr.LoadServers()
	if err != nil {
		return fmt.Errorf("failed to load server configs: %w", err)
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	// Create server states from global config
	for _, config := range configs {
		state := &commands.MCPServerState{
			Config:    config,
			Connected: false,
			Tools:     []*mcp.MCPTool{},
		}
		m.servers[config.Name] = state

		if config.Enabled && config.ShouldAutoConnect() {
			go m.connectServer(ctx, state)
		}
	}

	// Discover and merge project-level MCP configs (read-only, not persisted)
	if m.projectDir != "" {
		projectConfigs, warnings := m.discoverProjectMCPs(ctx)
		for _, w := range warnings {
			m.logger.Warn(ctx, "project MCP discovery", observability.F("detail", w))
		}
		for _, config := range projectConfigs {
			if _, exists := m.servers[config.Name]; exists {
				m.logger.Debug(ctx, "project MCP skipped (name already configured)",
					observability.F("name", config.Name))
				continue
			}
			state := &commands.MCPServerState{
				Config:    config,
				Connected: false,
				Tools:     []*mcp.MCPTool{},
			}
			m.servers[config.Name] = state
			m.logger.Info(ctx, "project MCP server added",
				observability.F("name", config.Name),
				observability.F("type", string(config.GetType())))
			if config.Enabled && config.ShouldAutoConnect() {
				go m.connectServer(ctx, state)
			}
		}
	}

	return nil
}

// projectMCPCandidates lists the file paths searched for project-level MCP configs,
// in order of precedence (earlier entries win on name conflicts).
var projectMCPCandidates = []string{
	"mcp.json",
	".mcp.json",
	".swarm/mcp.json",
	".swarm/.mcp.json",
}

// discoverProjectMCPs searches the project directory for MCP config files and
// returns the merged set of server configs (deduplicated, first-found wins).
// Warnings describe parse errors for files that were found but could not be read.
func (m *MCPManager) discoverProjectMCPs(ctx context.Context) ([]*commands.MCPServerConfig, []string) {
	seen := make(map[string]struct{})
	var result []*commands.MCPServerConfig
	var warnings []string

	for _, rel := range projectMCPCandidates {
		path := filepath.Join(m.projectDir, filepath.FromSlash(rel))
		data, err := os.ReadFile(path)
		if err != nil {
			if !os.IsNotExist(err) {
				warnings = append(warnings, fmt.Sprintf("%s: read error: %v", rel, err))
			}
			continue
		}

		m.logger.Info(ctx, "found project MCP config", observability.F("file", rel))

		parsed, err := parseProjectMCPFile(data)
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("%s: parse error: %v", rel, err))
			continue
		}

		for _, cfg := range parsed {
			if _, dup := seen[cfg.Name]; dup {
				continue
			}
			seen[cfg.Name] = struct{}{}
			result = append(result, cfg)
		}
	}

	return result, warnings
}

// projectMCPEntry is the common shape shared by flat-map and mcpServers formats.
type projectMCPEntry struct {
	Type    string            `json:"type"`
	Command string            `json:"command"`
	Args    []string          `json:"args"`
	URL     string            `json:"url"`
	Env     map[string]string `json:"env"`
	Headers map[string]string `json:"headers"`
	Timeout int               `json:"timeout"`
}

// canonicalMCPServer is the shape for the Swarm canonical servers array.
type canonicalMCPServer struct {
	Name       string            `json:"name"`
	Type       string            `json:"type"`
	Command    string            `json:"command"`
	Args       []string          `json:"args"`
	URL        string            `json:"url"`
	Env        map[string]string `json:"env"`
	Headers    map[string]string `json:"headers"`
	WorkingDir string            `json:"working_dir"`
	TimeoutSec int               `json:"timeout_sec"`
	Enabled    bool              `json:"enabled"`
}

// parseProjectMCPFile parses an MCP config file, auto-detecting the format:
//
//   - Swarm canonical:   {"schema_version": 1, "servers": [...]}
//   - Claude Desktop:    {"mcpServers": {"name": {...}}}
//   - Flat map / Context7: {"name": {"command": "...", "args": [...]}}
//
// All formats result in []*commands.MCPServerConfig with Enabled=true.
func parseProjectMCPFile(data []byte) ([]*commands.MCPServerConfig, error) {
	var probe map[string]json.RawMessage
	if err := json.Unmarshal(data, &probe); err != nil {
		return nil, fmt.Errorf("invalid JSON: %w", err)
	}

	// ── Swarm canonical format ──────────────────────────────────────────────
	if _, hasSchema := probe["schema_version"]; hasSchema {
		return parseCanonicalMCPFormat(probe)
	}
	if _, hasServers := probe["servers"]; hasServers {
		return parseCanonicalMCPFormat(probe)
	}

	// ── Claude Desktop format ───────────────────────────────────────────────
	if raw, hasMCPServers := probe["mcpServers"]; hasMCPServers {
		var inner map[string]projectMCPEntry
		if err := json.Unmarshal(raw, &inner); err != nil {
			return nil, fmt.Errorf("mcpServers: %w", err)
		}
		return projectMCPEntriesToConfigs(inner), nil
	}

	// ── Flat map / Context7 format ──────────────────────────────────────────
	// Use the plugins package parser which already handles this format well.
	servers, err := plugins.ParseMCPConfigBytes(data)
	if err != nil {
		return nil, fmt.Errorf("flat-map parse: %w", err)
	}
	return pluginMCPServersToConfigs(servers), nil
}

// parseCanonicalMCPFormat handles {"schema_version": 1, "servers": [...]}.
func parseCanonicalMCPFormat(probe map[string]json.RawMessage) ([]*commands.MCPServerConfig, error) {
	raw, ok := probe["servers"]
	if !ok {
		return nil, nil
	}
	var entries []canonicalMCPServer
	if err := json.Unmarshal(raw, &entries); err != nil {
		return nil, fmt.Errorf("servers: %w", err)
	}
	result := make([]*commands.MCPServerConfig, 0, len(entries))
	for _, e := range entries {
		if e.Name == "" {
			continue
		}
		cfg := &commands.MCPServerConfig{
			Name:       e.Name,
			Type:       resolveProjectMCPType(e.Type, e.Command, e.URL),
			Command:    e.Command,
			Args:       e.Args,
			URL:        e.URL,
			Env:        e.Env,
			Headers:    e.Headers,
			WorkingDir: e.WorkingDir,
			Timeout:    e.TimeoutSec,
			Enabled:    e.Enabled,
		}
		result = append(result, cfg)
	}
	return result, nil
}

// projectMCPEntriesToConfigs converts a flat/mcpServers-style map to configs.
func projectMCPEntriesToConfigs(m map[string]projectMCPEntry) []*commands.MCPServerConfig {
	result := make([]*commands.MCPServerConfig, 0, len(m))
	for name, e := range m {
		result = append(result, &commands.MCPServerConfig{
			Name:    name,
			Type:    resolveProjectMCPType(e.Type, e.Command, e.URL),
			Command: e.Command,
			Args:    e.Args,
			URL:     e.URL,
			Env:     e.Env,
			Headers: e.Headers,
			Timeout: e.Timeout,
			Enabled: true,
		})
	}
	return result
}

// pluginMCPServersToConfigs converts plugins.MCPServer slice (from ParseMCPConfigBytes) to configs.
func pluginMCPServersToConfigs(servers []plugins.MCPServer) []*commands.MCPServerConfig {
	result := make([]*commands.MCPServerConfig, 0, len(servers))
	for _, s := range servers {
		result = append(result, &commands.MCPServerConfig{
			Name:    s.Name,
			Type:    resolveProjectMCPType(s.Type, s.Command, s.URL),
			Command: s.Command,
			Args:    s.Args,
			URL:     s.URL,
			Env:     s.Environment,
			Enabled: true,
		})
	}
	return result
}

// resolveProjectMCPType maps a raw type string (or infers it) to MCPServerType.
func resolveProjectMCPType(rawType, command, url string) commands.MCPServerType {
	switch strings.ToLower(strings.TrimSpace(rawType)) {
	case "sse":
		return commands.MCPTypeSSE
	case "http", "streamable-http":
		return commands.MCPTypeHTTP
	case "oauth":
		return commands.MCPTypeOAuth
	case "stdio":
		return commands.MCPTypeStdio
	}
	// Infer from fields when type is empty
	if url != "" {
		return commands.MCPTypeHTTP
	}
	if command != "" {
		return commands.MCPTypeStdio
	}
	return commands.MCPTypeStdio
}

// connectServer connects to an MCP server
func (m *MCPManager) connectServer(ctx context.Context, state *commands.MCPServerState) {
	// Track connection attempt
	m.mu.Lock()
	state.ConnectionAttempts++
	state.LastAttempt = time.Now()
	m.mu.Unlock()

	m.logger.Info(ctx, "Connecting to MCP server",
		observability.F("name", state.Config.Name),
		observability.F("type", string(state.Config.GetType())),
		observability.F("command", state.Config.Command),
		observability.F("url", state.Config.URL),
		observability.F("attempt", state.ConnectionAttempts))

	// Create transport config
	transportConfig := &mcp.TransportConfig{
		Command:    state.Config.Command,
		Args:       state.Config.Args,
		Env:        state.Config.Env,
		WorkingDir: state.Config.WorkingDir,
		URL:        state.Config.URL,
		Headers:    state.Config.Headers,
	}

	// Set timeout if specified
	if state.Config.Timeout > 0 {
		transportConfig.Timeout = time.Duration(state.Config.Timeout) * time.Second
	}

	// Create appropriate transport based on server type
	var transport mcp.Transport
	var err error

	switch state.Config.GetType() {
	case commands.MCPTypeStdio:
		transportConfig.Type = mcp.TransportStdio
		transport = mcp.NewStdioTransport(transportConfig, m.logger)
	case commands.MCPTypeHTTP, commands.MCPTypeSSE:
		transportConfig.Type = mcp.TransportHTTPStream
		// Set OAuth config if available (for auto-discovery)
		if state.Config.OAuth != nil {
			transportConfig.OAuthConfig = &mcp.OAuthConfig{
				ClientID:     state.Config.OAuth.ClientID,
				ClientSecret: state.Config.OAuth.ClientSecret,
				Scopes:       parseScopes(state.Config.OAuth.Scopes),
			}
		}
		// Use Auto HTTP transport which handles OAuth auto-discovery
		transport = mcp.NewAutoHTTPTransport(transportConfig, m.logger, m.tracer)
	case commands.MCPTypeOAuth:
		if state.Config.OAuth == nil {
			m.mu.Lock()
			state.Error = "OAuth configuration missing"
			state.Connected = false
			m.mu.Unlock()
			m.logger.Error(ctx, "OAuth configuration missing for server",
				observability.F("name", state.Config.Name))
			return
		}
		transportConfig.Type = mcp.TransportOAuthHTTP
		transportConfig.OAuthConfig = &mcp.OAuthConfig{
			ClientID:     state.Config.OAuth.ClientID,
			ClientSecret: state.Config.OAuth.ClientSecret,
			Scopes:       parseScopes(state.Config.OAuth.Scopes),
		}
		transport, err = mcp.NewOAuthHTTPTransport(transportConfig, m.logger, m.tracer)
		if err != nil {
			m.mu.Lock()
			state.Error = fmt.Sprintf("failed to create OAuth transport: %v", err)
			state.LastError = err
			state.ErrorTimestamp = time.Now()
			state.Connected = false
			m.mu.Unlock()
			m.logger.Error(ctx, "Failed to create OAuth transport",
				observability.F("name", state.Config.Name),
				observability.F("error", err.Error()))
			return
		}
	default:
		m.mu.Lock()
		state.Error = fmt.Sprintf("unsupported transport type: %s", state.Config.GetType())
		state.Connected = false
		m.mu.Unlock()
		m.logger.Error(ctx, "Unsupported transport type",
			observability.F("name", state.Config.Name),
			observability.F("type", string(state.Config.GetType())))
		return
	}

	// Create client
	client := mcp.NewClient(transport, m.logger, m.tracer)

	// Connect
	if err := client.Connect(ctx); err != nil {
		m.mu.Lock()
		state.Error = err.Error()
		state.LastError = err // Store full error with context
		state.ErrorTimestamp = time.Now()
		state.Connected = false
		m.mu.Unlock()

		m.logger.Error(ctx, "Failed to connect to MCP server",
			observability.F("name", state.Config.Name),
			observability.F("error", err.Error()),
			observability.F("attempt", state.ConnectionAttempts))
		return
	}

	// Update state
	m.mu.Lock()
	state.Client = client
	state.Connected = true
	state.Tools = client.ListTools()
	state.Error = ""
	state.LastError = nil
	m.mu.Unlock()

	m.logger.Info(ctx, "Connected to MCP server",
		observability.F("name", state.Config.Name),
		observability.F("tools", len(state.Tools)))

	// Register tools with registry
	m.registerServerTools(ctx, state)
}

// registerServerTools registers MCP tools with the tool registry
func (m *MCPManager) registerServerTools(ctx context.Context, state *commands.MCPServerState) {
	if state.Client == nil {
		return
	}

	// Wrap MCP tools as SDK tools
	wrappedTools := mcp.WrapMCPTools(state.Client)

	for _, tool := range wrappedTools {
		toolName := tool.Name()

		// Check if tool is disabled
		if state.Config.DisabledTools != nil {
			if _, disabled := state.Config.DisabledTools[toolName]; disabled {
				m.logger.Debug(ctx, "Skipping disabled MCP tool",
					observability.F("server", state.Config.Name),
					observability.F("tool", toolName))
				continue
			}
		}

		// Register with unique name: mcp_server_tool (use underscores for Anthropic compatibility)
		// Anthropic requires tool names to match ^[a-zA-Z0-9_-]{1,128}$ (no colons allowed)
		sanitizedServer := sanitizeToolName(state.Config.Name)
		sanitizedTool := sanitizeToolName(toolName)
		uniqueName := fmt.Sprintf("mcp_%s_%s", sanitizedServer, sanitizedTool)

		// Create a wrapper that uses the unique name
		wrappedTool := &mcpToolWrapper{
			Tool:       tool,
			serverName: state.Config.Name,
			uniqueName: uniqueName,
		}

		if err := m.toolRegistry.Register(wrappedTool); err != nil {
			m.logger.Warn(ctx, "Failed to register MCP tool",
				observability.F("server", state.Config.Name),
				observability.F("tool", toolName),
				observability.F("error", err.Error()))
		} else {
			m.logger.Debug(ctx, "Registered MCP tool",
				observability.F("server", state.Config.Name),
				observability.F("tool", toolName),
				observability.F("unique_name", uniqueName))
		}
	}
}

// GetServers returns all server states
func (m *MCPManager) GetServers() []*commands.MCPServerState {
	m.mu.RLock()
	defer m.mu.RUnlock()

	servers := make([]*commands.MCPServerState, 0, len(m.servers))
	for _, state := range m.servers {
		servers = append(servers, state)
	}
	return servers
}

// GetConfigJSON returns the MCP config JSON for global and project
func (m *MCPManager) GetConfigJSON() (global []byte, project []byte, err error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	// Load global config
	globalServers, err := m.configMgr.LoadServers()
	if err != nil {
		return nil, nil, fmt.Errorf("failed to load global config: %w", err)
	}

	globalJSON, err := json.MarshalIndent(globalServers, "", "  ")
	if err != nil {
		return nil, nil, fmt.Errorf("failed to marshal global config: %w", err)
	}

	// Project config (if exists)
	projectConfigPath := "./.swarmos/mcp_servers.json"
	projectJSON := []byte("{}")
	if _, err := os.Stat(projectConfigPath); err == nil {
		data, err := os.ReadFile(projectConfigPath)
		if err == nil {
			projectJSON = data
		}
	}

	return globalJSON, projectJSON, nil
}

// mcpToolWrapper wraps an MCP tool with a unique name
type mcpToolWrapper struct {
	tools.Tool
	serverName string
	uniqueName string
}

func (w *mcpToolWrapper) Name() string {
	return w.uniqueName
}

// AddServer adds a new MCP server
func (m *MCPManager) AddServer(ctx context.Context, config *commands.MCPServerConfig) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Check if server already exists
	if _, exists := m.servers[config.Name]; exists {
		return fmt.Errorf("server %s already exists", config.Name)
	}

	// Create server state
	state := &commands.MCPServerState{
		Config:    config,
		Connected: false,
		Tools:     []*mcp.MCPTool{},
	}
	m.servers[config.Name] = state

	// Save to config
	configs := m.getAllConfigs()
	if err := m.configMgr.SaveServers(configs); err != nil {
		return fmt.Errorf("failed to save server config: %w", err)
	}

	// Connect if enabled
	if config.Enabled {
		go m.connectServer(ctx, state)
	}

	return nil
}

// ToggleServer enables/disables a server
func (m *MCPManager) ToggleServer(ctx context.Context, name string, enabled bool) error {
	m.mu.Lock()
	state, exists := m.servers[name]
	if !exists {
		m.mu.Unlock()
		return fmt.Errorf("server %s not found", name)
	}

	state.Config.Enabled = enabled
	m.mu.Unlock()

	// Save config
	configs := m.getAllConfigs()
	if err := m.configMgr.SaveServers(configs); err != nil {
		return fmt.Errorf("failed to save server config: %w", err)
	}

	// Connect or disconnect
	if enabled {
		go m.connectServer(ctx, state)
	} else {
		m.disconnectServer(state)
	}

	return nil
}

// ToggleTool enables/disables a specific tool
func (m *MCPManager) ToggleTool(ctx context.Context, serverName, toolName string, enabled bool) error {
	m.mu.Lock()
	state, exists := m.servers[serverName]
	if !exists {
		m.mu.Unlock()
		return fmt.Errorf("server %s not found", serverName)
	}

	// Initialize maps if needed
	if state.Config.DisabledTools == nil {
		state.Config.DisabledTools = make(map[string]bool)
	}

	// FIX: Find the tool so we can get its unique registry name for immediate unregister
	var uniqueToolName string
	var foundTool *mcp.MCPTool
	for _, tool := range state.Tools {
		if tool.Name == toolName {
			foundTool = tool
			// Build unique name same way as registerServerTools does
			sanitizedServer := sanitizeToolName(state.Config.Name)
			sanitizedTool := sanitizeToolName(toolName)
			uniqueToolName = fmt.Sprintf("mcp_%s_%s", sanitizedServer, sanitizedTool)
			break
		}
	}

	// Update disabled tools map
	if enabled {
		delete(state.Config.DisabledTools, toolName)
	} else {
		state.Config.DisabledTools[toolName] = true
	}
	m.mu.Unlock()

	// Save config
	configs := m.getAllConfigs()
	if err := m.configMgr.SaveServers(configs); err != nil {
		return fmt.Errorf("failed to save server config: %w", err)
	}

	// FIX: Immediately register/unregister tool from registry (no reconnect needed!)
	if uniqueToolName != "" && foundTool != nil {
		if enabled {
			// Re-enable: wrap and re-register the tool
			wrappedTools := mcp.WrapMCPTools(state.Client)
			for _, wrappedTool := range wrappedTools {
				if wrappedTool.Name() == toolName {
					wrappedToolWithName := &mcpToolWrapper{
						Tool:       wrappedTool,
						serverName: state.Config.Name,
						uniqueName: uniqueToolName,
					}
					m.toolRegistry.Register(wrappedToolWithName)
					m.logger.Debug(ctx, "Re-registered MCP tool",
						observability.F("server", state.Config.Name),
						observability.F("tool", toolName),
						observability.F("unique_name", uniqueToolName))
					break
				}
			}
		} else {
			// Disable: unregister from registry
			if err := m.toolRegistry.Unregister(uniqueToolName); err != nil {
				m.logger.Warn(ctx, "Failed to unregister MCP tool",
					observability.F("server", serverName),
					observability.F("tool", toolName),
					observability.F("unique_name", uniqueToolName),
					observability.F("error", err.Error()))
			} else {
				m.logger.Debug(ctx, "Unregistered MCP tool",
					observability.F("server", state.Config.Name),
					observability.F("tool", toolName),
					observability.F("unique_name", uniqueToolName))
			}
		}
	}

	return nil
}

// disconnectServer disconnects from an MCP server
func (m *MCPManager) disconnectServer(state *commands.MCPServerState) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if state.Client != nil {
		state.Client.Close()
		state.Client = nil
	}
	state.Connected = false
	state.Tools = []*mcp.MCPTool{}
	state.Error = ""
	state.LastError = nil
}

// GetServerStates returns all server states
func (m *MCPManager) GetServerStates() []*commands.MCPServerState {
	m.mu.RLock()
	defer m.mu.RUnlock()

	states := make([]*commands.MCPServerState, 0, len(m.servers))
	for _, state := range m.servers {
		states = append(states, state)
	}
	return states
}

// getAllConfigs returns all server configs
func (m *MCPManager) getAllConfigs() []*commands.MCPServerConfig {
	configs := make([]*commands.MCPServerConfig, 0, len(m.servers))
	for _, state := range m.servers {
		configs = append(configs, state.Config)
	}
	return configs
}

// RefreshAll reconnects all enabled servers
func (m *MCPManager) RefreshAll(ctx context.Context) {
	m.mu.RLock()
	servers := make([]*commands.MCPServerState, 0, len(m.servers))
	for _, state := range m.servers {
		if state.Config.Enabled {
			servers = append(servers, state)
		}
	}
	m.mu.RUnlock()

	// Reconnect each enabled server
	for _, state := range servers {
		m.disconnectServer(state)
		go m.connectServer(ctx, state)
	}
}

// GetServerPrompts returns all prompts from a specific MCP server
func (m *MCPManager) GetServerPrompts(serverName string) []*mcp.MCPPrompt {
	m.mu.RLock()
	defer m.mu.RUnlock()

	state, exists := m.servers[serverName]
	if !exists || state.Client == nil {
		return nil
	}

	return state.Client.Prompts()
}

// GetServerResources returns all resources from a specific MCP server
func (m *MCPManager) GetServerResources(serverName string) []*mcp.MCPResource {
	m.mu.RLock()
	defer m.mu.RUnlock()

	state, exists := m.servers[serverName]
	if !exists || state.Client == nil {
		return nil
	}

	return state.Client.Resources()
}

// GetAllPrompts returns all prompts from all connected MCP servers
func (m *MCPManager) GetAllPrompts() map[string][]*mcp.MCPPrompt {
	m.mu.RLock()
	defer m.mu.RUnlock()

	result := make(map[string][]*mcp.MCPPrompt)
	for name, state := range m.servers {
		if state.Client != nil && state.Connected {
			prompts := state.Client.Prompts()
			if len(prompts) > 0 {
				result[name] = prompts
			}
		}
	}
	return result
}

// GetAllResources returns all resources from all connected MCP servers
func (m *MCPManager) GetAllResources() map[string][]*mcp.MCPResource {
	m.mu.RLock()
	defer m.mu.RUnlock()

	result := make(map[string][]*mcp.MCPResource)
	for name, state := range m.servers {
		if state.Client != nil && state.Connected {
			resources := state.Client.Resources()
			if len(resources) > 0 {
				result[name] = resources
			}
		}
	}
	return result
}

// Close disconnects all servers
func (m *MCPManager) Close() {
	m.mu.Lock()
	defer m.mu.Unlock()

	for _, state := range m.servers {
		if state.Client != nil {
			state.Client.Close()
		}
	}
}

// IsServerConnected returns true if a server is connected
func (m *MCPManager) IsServerConnected(serverName string) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()

	state, exists := m.servers[serverName]
	if !exists {
		return false
	}
	return state.Connected && state.Client != nil
}

// ReadResource reads a resource from a specific MCP server
func (m *MCPManager) ReadResource(ctx context.Context, serverName, uri string) (*mcp.ResourceContents, error) {
	m.mu.RLock()
	state, exists := m.servers[serverName]
	m.mu.RUnlock()

	if !exists {
		return nil, fmt.Errorf("server %s not found", serverName)
	}

	if state.Client == nil || !state.Connected {
		return nil, fmt.Errorf("server %s not connected", serverName)
	}

	return state.Client.ReadResource(ctx, uri)
}

// GetPrompt executes a prompt from a specific MCP server with the given arguments
func (m *MCPManager) GetPrompt(ctx context.Context, serverName, promptName string, args map[string]string) (*mcp.PromptResult, error) {
	m.mu.RLock()
	state, exists := m.servers[serverName]
	m.mu.RUnlock()

	if !exists {
		return nil, fmt.Errorf("server %s not found", serverName)
	}

	if state.Client == nil || !state.Connected {
		return nil, fmt.Errorf("server %s not connected", serverName)
	}

	return state.Client.PromptContent(ctx, promptName, args)
}

// GetServer returns a single server by name
func (m *MCPManager) GetServer(name string) *commands.MCPServerState {
	m.mu.RLock()
	defer m.mu.RUnlock()

	return m.servers[name]
}

// EnableServer enables a server (wrapper around ToggleServer)
func (m *MCPManager) EnableServer(name string) error {
	return m.ToggleServer(context.Background(), name, true)
}

// DisableServer disables a server (wrapper around ToggleServer)
func (m *MCPManager) DisableServer(name string) error {
	return m.ToggleServer(context.Background(), name, false)
}

// ConfigureTool enables/disables a specific tool (wrapper around ToggleTool)
func (m *MCPManager) ConfigureTool(serverName, toolName string, enabled bool) error {
	return m.ToggleTool(context.Background(), serverName, toolName, enabled)
}

// DeleteServer removes a server configuration
func (m *MCPManager) DeleteServer(name string) error {
	m.mu.Lock()
	state, exists := m.servers[name]
	if !exists {
		m.mu.Unlock()
		return fmt.Errorf("server %s not found", name)
	}
	m.mu.Unlock()

	// Disconnect the server
	m.disconnectServer(state)

	m.mu.Lock()
	// Remove from map
	delete(m.servers, name)
	m.mu.Unlock()

	// Unregister all tools from this server from the tool registry
	if m.toolRegistry != nil {
		toolNames := m.toolRegistry.List()
		for _, toolName := range toolNames {
			// Check if this tool is from the deleted server
			if strings.HasPrefix(toolName, fmt.Sprintf("mcp_%s_", sanitizeToolName(name))) {
				m.toolRegistry.Unregister(toolName)
			}
		}
	}

	// Save updated config
	configs := m.getAllConfigs()
	if err := m.configMgr.SaveServers(configs); err != nil {
		return fmt.Errorf("failed to save server config: %w", err)
	}

	return nil
}

// ReconnectServer reconnects a server
func (m *MCPManager) ReconnectServer(ctx context.Context, name string) error {
	m.mu.Lock()
	state, exists := m.servers[name]
	if !exists {
		m.mu.Unlock()
		return fmt.Errorf("server %s not found", name)
	}
	m.mu.Unlock()

	// Disconnect first
	m.disconnectServer(state)

	// Reconnect if enabled
	if state.Config.Enabled {
		// For OAuth servers, connect synchronously so user can complete the flow
		// For stdio servers, connect in background
		if state.Config.GetType() == commands.MCPTypeHTTP ||
			state.Config.GetType() == commands.MCPTypeSSE ||
			state.Config.GetType() == commands.MCPTypeOAuth {
			// Synchronous connection with extended timeout for OAuth
			connCtx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
			defer cancel()
			m.connectServer(connCtx, state)
		} else {
			// Async for stdio
			go m.connectServer(ctx, state)
		}
	}

	return nil
}

// ReconnectAllServers reconnects all enabled servers that are not currently connected.
// This is useful at startup to retry connections that may have failed or timed out.
func (m *MCPManager) ReconnectAllServers(ctx context.Context) {
	m.mu.RLock()
	var toReconnect []*commands.MCPServerState
	for _, state := range m.servers {
		if state.Config.Enabled && !state.Connected {
			toReconnect = append(toReconnect, state)
		}
	}
	m.mu.RUnlock()

	m.logger.Info(ctx, "mcp.reconnect_all",
		observability.F("count", len(toReconnect)))

	for _, state := range toReconnect {
		m.logger.Info(ctx, "mcp.reconnecting",
			observability.F("name", state.Config.Name))
		go m.connectServer(ctx, state)
	}
}
