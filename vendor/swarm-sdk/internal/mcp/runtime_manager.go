package mcp

import (
	"context"
	"fmt"
	"math/rand"
	"slices"
	"sync"
	"time"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/observability"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/tools"
	mcpsdk "github.com/Swarm-Code/mono/swarm-sdk/internal/tools/mcp"
)

type RuntimeManager struct {
	configManager *ConfigManager
	registry      tools.Registry
	credentials   CredentialsStore
	logger        observability.Logger
	tracer        observability.Tracer

	mu      sync.RWMutex
	servers map[string]*ServerState

	reconnectBase time.Duration
	reconnectMax  time.Duration

	statusMu      sync.RWMutex
	statusHandler func(ServerStatusRecord)

	connectOnceFunc func(ctx context.Context, state *ServerState) (*mcpsdk.Client, error)
	sleepFn         func(ctx context.Context, base, max time.Duration, attempt int) bool
}

type ServerState struct {
	Config  ServerConfig
	Origin  ResolvedServer
	Client  *mcpsdk.Client
	Status  ServerStatus
	Tools   map[string]*ToolState
	toolMap map[string]string
	cancel  context.CancelFunc
	mu      sync.RWMutex
}

type ToolState struct {
	Name         string
	RegistryName string
	Enabled      bool
}

type ServerStatus struct {
	State         string
	ToolCount     int
	ToolsEnabled  int
	ToolsDisabled int
	LastError     string
	LastErrorAt   time.Time
}

func NewRuntimeManager(configManager *ConfigManager, registry tools.Registry, credentials CredentialsStore, logger observability.Logger, tracer observability.Tracer) *RuntimeManager {
	if credentials == nil {
		credentials = &noopCredentialsStore{}
	}
	return &RuntimeManager{
		configManager: configManager,
		registry:      registry,
		credentials:   credentials,
		logger:        logger,
		tracer:        tracer,
		servers:       make(map[string]*ServerState),
		reconnectBase: 200 * time.Millisecond,
		reconnectMax:  30 * time.Second,
	}
}

func (m *RuntimeManager) Start(ctx context.Context) error {
	resolved, err := m.configManager.Resolve(ctx)
	if err != nil {
		return err
	}
	m.mu.Lock()
	m.servers = make(map[string]*ServerState)
	m.mu.Unlock()

	for _, server := range resolved.Servers {
		if len(server.ShadowedBy) > 0 {
			continue
		}
		state := &ServerState{
			Config:  server.Config,
			Origin:  server,
			Tools:   make(map[string]*ToolState),
			toolMap: make(map[string]string),
			Status: ServerStatus{
				State: "disconnected",
			},
		}
		m.mu.Lock()
		m.servers[state.Config.Name] = state
		m.mu.Unlock()

		if state.Config.Enabled {
			ctx, cancel := context.WithCancel(ctx)
			state.cancel = cancel
			go m.runServer(ctx, state)
		}
		m.emitStatus(state)
	}
	return nil
}

func (m *RuntimeManager) Stop() {
	ctx := context.Background()
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, state := range m.servers {
		if state.cancel != nil {
			state.cancel()
		}
		if state.Client != nil {
			state.Client.Close()
		}
		m.unregisterTools(ctx, state)
	}
}

func (m *RuntimeManager) GetServerStates() []ServerStatusRecord {
	m.mu.RLock()
	defer m.mu.RUnlock()
	states := make([]ServerStatusRecord, 0, len(m.servers))
	for _, state := range m.servers {
		state.mu.RLock()
		status := state.Status
		config := state.Config
		state.mu.RUnlock()
		states = append(states, ServerStatusRecord{Name: config.Name, Status: status})
	}
	return states
}

// ListResources returns resources for a connected MCP server.
func (m *RuntimeManager) ListResources(ctx context.Context, serverName, cursor string, limit int) ([]*mcpsdk.MCPResource, string, error) {
	m.mu.RLock()
	state, ok := m.servers[serverName]
	m.mu.RUnlock()
	if !ok {
		return nil, "", fmt.Errorf("server not found: %s", serverName)
	}

	state.mu.RLock()
	client := state.Client
	state.mu.RUnlock()
	if client == nil || !client.IsConnected() {
		return nil, "", fmt.Errorf("server not connected: %s", serverName)
	}

	return client.ListResourcesPaged(ctx, cursor, limit)
}

// ReadResource returns resource contents for a connected MCP server.
func (m *RuntimeManager) ReadResource(ctx context.Context, serverName, uri string) (*mcpsdk.ResourceContents, error) {
	m.mu.RLock()
	state, ok := m.servers[serverName]
	m.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("server not found: %s", serverName)
	}

	state.mu.RLock()
	client := state.Client
	state.mu.RUnlock()
	if client == nil || !client.IsConnected() {
		return nil, fmt.Errorf("server not connected: %s", serverName)
	}

	return client.ReadResource(ctx, uri)
}

// GetPrompt returns prompt contents for a connected MCP server.
func (m *RuntimeManager) GetPrompt(ctx context.Context, serverName, promptName string, args map[string]string) (*mcpsdk.PromptResult, error) {
	m.mu.RLock()
	state, ok := m.servers[serverName]
	m.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("server not found: %s", serverName)
	}

	state.mu.RLock()
	client := state.Client
	state.mu.RUnlock()
	if client == nil || !client.IsConnected() {
		return nil, fmt.Errorf("server not connected: %s", serverName)
	}

	return client.PromptContent(ctx, promptName, args)
}

// SetStatusHandler registers a callback for MCP status updates.
func (m *RuntimeManager) SetStatusHandler(handler func(ServerStatusRecord)) {
	m.statusMu.Lock()
	defer m.statusMu.Unlock()
	m.statusHandler = handler
}

func (m *RuntimeManager) emitStatus(state *ServerState) {
	if state == nil {
		return
	}
	m.statusMu.RLock()
	handler := m.statusHandler
	m.statusMu.RUnlock()
	if handler == nil {
		return
	}
	state.mu.RLock()
	status := state.Status
	name := state.Config.Name
	state.mu.RUnlock()
	handler(ServerStatusRecord{Name: name, Status: status})
}

// SetToolEnabled enables or disables a tool without reconnecting.
func (m *RuntimeManager) SetToolEnabled(serverName, toolName string, enabled bool) error {
	m.mu.RLock()
	state, ok := m.servers[serverName]
	m.mu.RUnlock()
	if !ok {
		return fmt.Errorf("server not found: %s", serverName)
	}
	toggler, ok := m.registry.(toolToggler)
	if !ok {
		return fmt.Errorf("registry does not support toggles")
	}
	state.mu.Lock()
	toolState, ok := state.Tools[toolName]
	if !ok {
		state.mu.Unlock()
		return fmt.Errorf("tool not found: %s", toolName)
	}
	state.mu.Unlock()

	if enabled {
		if err := toggler.EnableTool(toolState.RegistryName); err != nil {
			return err
		}
	} else {
		if err := toggler.DisableTool(toolState.RegistryName); err != nil {
			return err
		}
	}

	state.mu.Lock()
	toolState.Enabled = enabled
	state.Status = summarizeStatus(state)
	state.mu.Unlock()
	m.emitStatus(state)

	// CRITICAL FIX: Persist tool state to config file
	// Update the server config's Tools.Disabled list and save to disk
	if err := m.persistToolState(context.Background(), serverName, toolName, enabled); err != nil {
		m.logger.Warn(context.Background(), "Failed to persist tool state",
			observability.F("server", serverName),
			observability.F("tool", toolName),
			observability.F("error", err.Error()))
		// Don't fail the operation, but warn the user
	}

	return nil
}

// persistToolState saves the tool enabled/disabled state to the config file
func (m *RuntimeManager) persistToolState(ctx context.Context, serverName, toolName string, enabled bool) error {
	m.mu.RLock()
	state, ok := m.servers[serverName]
	m.mu.RUnlock()
	if !ok {
		return fmt.Errorf("server not found: %s", serverName)
	}

	// Load current config layer
	cfg, err := m.configManager.loadConfigFile(ctx, m.configManager.pathForScope(state.Origin.Scope))
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}

	// Find the server in the config
	for i := range cfg.Servers {
		if cfg.Servers[i].Name == serverName {
			// Initialize Tools config if not present
			if cfg.Servers[i].Tools == nil {
				cfg.Servers[i].Tools = &ToolsConfig{
					Mode:     "explicit",
					Enabled:  []string{},
					Disabled: []string{},
				}
			}

			// Update disabled list
			if enabled {
				// Remove from disabled list
				newDisabled := make([]string, 0, len(cfg.Servers[i].Tools.Disabled))
				for _, name := range cfg.Servers[i].Tools.Disabled {
					if name != toolName {
						newDisabled = append(newDisabled, name)
					}
				}
				cfg.Servers[i].Tools.Disabled = newDisabled
			} else {
				// Add to disabled list if not already present
				alreadyDisabled := slices.Contains(cfg.Servers[i].Tools.Disabled, toolName)
				if !alreadyDisabled {
					cfg.Servers[i].Tools.Disabled = append(cfg.Servers[i].Tools.Disabled, toolName)
				}
			}

			// Save the updated config
			return m.configManager.SaveLayer(ctx, state.Origin.Scope, cfg)
		}
	}

	return fmt.Errorf("server %s not found in config", serverName)
}

type ServerStatusRecord struct {
	Name   string
	Status ServerStatus
}

// RefreshServer reconnects a single MCP server by name.
func (m *RuntimeManager) RefreshServer(ctx context.Context, name string) error {
	m.mu.RLock()
	state, ok := m.servers[name]
	m.mu.RUnlock()
	if !ok {
		return fmt.Errorf("server not found: %s", name)
	}

	state.mu.Lock()
	if state.cancel != nil {
		state.cancel()
		state.cancel = nil
	}
	client := state.Client
	state.Client = nil
	state.mu.Unlock()

	if client != nil {
		client.Close()
	}

	m.unregisterTools(ctx, state)
	m.setStatus(state, "disconnected", "")

	state.mu.RLock()
	enabled := state.Config.Enabled
	state.mu.RUnlock()
	if enabled {
		ctx, cancel := context.WithCancel(ctx)
		state.mu.Lock()
		state.cancel = cancel
		state.mu.Unlock()
		go m.runServer(ctx, state)
	}
	return nil
}

// RefreshAll reconnects all enabled MCP servers.
func (m *RuntimeManager) RefreshAll(ctx context.Context) {
	m.mu.RLock()
	names := make([]string, 0, len(m.servers))
	for _, state := range m.servers {
		state.mu.RLock()
		enabled := state.Config.Enabled
		name := state.Config.Name
		state.mu.RUnlock()
		if enabled {
			names = append(names, name)
		}
	}
	m.mu.RUnlock()

	for _, name := range names {
		_ = m.RefreshServer(ctx, name)
	}
}

func (m *RuntimeManager) runServer(ctx context.Context, state *ServerState) {
	attempt := 0
	connectOnce := m.connectOnceFunc
	if connectOnce == nil {
		connectOnce = m.connectOnce
	}
	sleepWith := m.sleepFn
	if sleepWith == nil {
		sleepWith = sleepWithBackoff
	}
	for {
		if ctx.Err() != nil {
			return
		}
		m.setStatus(state, "connecting", "")

		client, err := connectOnce(ctx, state)
		if err != nil {
			m.setStatus(state, "error", err.Error())
			if isCredentialError(err) {
				return
			}
			attempt++
			if !sleepWith(ctx, m.reconnectBase, m.reconnectMax, attempt) {
				return
			}
			continue
		}

		attempt = 0
		state.mu.Lock()
		state.Client = client
		state.mu.Unlock()

		m.registerTools(ctx, state)
		m.setStatus(state, "connected", "")

		if !m.monitorConnection(ctx, state) {
			return
		}
	}
}

func (m *RuntimeManager) connectOnce(ctx context.Context, state *ServerState) (*mcpsdk.Client, error) {
	runtime, err := m.configManager.ResolveRuntimeServer(ctx, state.Config)
	if err != nil {
		return nil, err
	}
	transport, err := m.buildTransport(runtime)
	if err != nil {
		return nil, err
	}
	client := mcpsdk.NewClient(transport, m.logger, m.tracer)
	if err := client.Connect(ctx); err != nil {
		return nil, err
	}
	return client, nil
}

func (m *RuntimeManager) buildTransport(runtime *RuntimeServer) (mcpsdk.Transport, error) {
	// DEBUG: Log BEFORE any config manipulation
	ctx := context.Background()
	m.logger.Info(ctx, "[DEBUG] buildTransport ENTRY",
		observability.F("server_name", runtime.Config.Name),
		observability.F("config_type", runtime.Config.Type),
		observability.F("command", runtime.Config.Command),
		observability.F("url", runtime.Config.URL),
		observability.F("resolved_url", runtime.ResolvedURL))

	config := mcpsdk.DefaultTransportConfig()

	m.logger.Info(ctx, "[DEBUG] After DefaultTransportConfig",
		observability.F("default_type", string(config.Type)))

	config.Timeout = time.Duration(runtime.Config.TimeoutSec) * time.Second
	config.Retries = runtime.Config.Retries
	config.Env = copyStringMap(runtime.Env)
	config.Headers = copyStringMap(runtime.Headers)
	config.Command = runtime.Config.Command
	config.Args = copyStringSlice(runtime.Config.Args)
	config.WorkingDir = runtime.Config.WorkingDir
	config.URL = runtime.ResolvedURL

	m.logger.Info(ctx, "[DEBUG] BEFORE SWITCH",
		observability.F("runtime_config_type", runtime.Config.Type),
		observability.F("sdk_config_type", string(config.Type)))

	switch runtime.Config.Type {
	case "stdio":
		config.Type = mcpsdk.TransportStdio
		return mcpsdk.NewStdioTransport(config, m.logger), nil
	case "http", "sse":
		config.Type = mcpsdk.TransportHTTPStream
		// Set OAuth config if available
		if runtime.Config.OAuth != nil {
			config.OAuthConfig = &mcpsdk.OAuthConfig{
				ClientID:     runtime.Config.OAuth.ClientID,
				ClientSecret: runtime.OAuthSecret,
				Scopes:       runtime.Config.OAuth.Scopes,
				TokenStorage: tokenStorageFor(runtime.Config, m.credentials),
			}
		}
		// Use AutoHTTPTransport for automatic OAuth discovery
		return mcpsdk.NewAutoHTTPTransport(config, m.logger, m.tracer), nil
	case "oauth":
		if runtime.Config.OAuth == nil {
			return nil, fmt.Errorf("oauth config missing for %s", runtime.Config.Name)
		}
		config.Type = mcpsdk.TransportOAuthHTTP
		config.OAuthConfig = &mcpsdk.OAuthConfig{
			ClientID:     runtime.Config.OAuth.ClientID,
			ClientSecret: runtime.OAuthSecret,
			Scopes:       runtime.Config.OAuth.Scopes,
			AuthURL:      runtime.Config.OAuth.AuthURL,
			TokenURL:     runtime.Config.OAuth.TokenURL,
			TokenStorage: tokenStorageFor(runtime.Config, m.credentials),
		}
		return mcpsdk.NewOAuthHTTPTransport(config, m.logger, m.tracer)
	default:
		return nil, fmt.Errorf("unsupported transport: %s", runtime.Config.Type)
	}
}

func (m *RuntimeManager) registerTools(ctx context.Context, state *ServerState) {
	if m.registry == nil {
		return
	}
	state.mu.Lock()
	client := state.Client
	state.mu.Unlock()
	if client == nil {
		return
	}

	wrapped := mcpsdk.WrapMCPTools(client)
	toolConfig := normalizeToolsConfig(state.Config.Tools)
	for _, tool := range wrapped {
		uniqueName := uniqueToolName(state.Config.Name, tool.Name())
		wrapper := &mcpToolWrapper{
			inner:      tool,
			uniqueName: uniqueName,
			serverName: state.Config.Name,
		}
		if err := m.registry.Register(wrapper); err != nil {
			if m.logger != nil {
				m.logger.Warn(ctx, "mcp.tool_register_failed",
					observability.F("tool", uniqueName),
					observability.F("error", err.Error()))
			}
			continue
		}
		state.mu.Lock()
		state.Tools[tool.Name()] = &ToolState{Name: tool.Name(), RegistryName: uniqueName, Enabled: true}
		state.toolMap[tool.Name()] = uniqueName
		state.mu.Unlock()
	}
	m.registerResourceTools(ctx, state)
	m.applyToolConfig(state, toolConfig)
}

func (m *RuntimeManager) applyToolConfig(state *ServerState, tools *ToolsConfig) {
	if tools == nil {
		return
	}
	toggler, ok := m.registry.(toolToggler)
	if !ok {
		return
	}

	state.mu.Lock()

	for name, toolState := range state.Tools {
		shouldEnable := shouldEnableTool(name, tools)
		if shouldEnable {
			_ = toggler.EnableTool(toolState.RegistryName)
			toolState.Enabled = true
		} else {
			_ = toggler.DisableTool(toolState.RegistryName)
			toolState.Enabled = false
		}
	}
	state.Status = summarizeStatus(state)
	state.mu.Unlock()
	m.emitStatus(state)
}

func shouldEnableTool(name string, tools *ToolsConfig) bool {
	mode := tools.Mode
	if mode == "" {
		mode = "all"
	}
	switch mode {
	case "allowlist":
		return contains(tools.Enabled, name)
	case "blocklist":
		return !contains(tools.Disabled, name)
	default:
		return true
	}
}

func (m *RuntimeManager) monitorConnection(ctx context.Context, state *ServerState) bool {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return false
		case <-ticker.C:
			state.mu.RLock()
			client := state.Client
			state.mu.RUnlock()
			if client == nil {
				return true
			}
			if !client.IsConnected() {
				m.unregisterTools(ctx, state)
				m.setStatus(state, "error", "connection lost")
				_ = client.Close()
				state.mu.Lock()
				state.Client = nil
				state.mu.Unlock()
				return true
			}
		}
	}
}

func (m *RuntimeManager) setStatus(state *ServerState, status string, errMsg string) {
	state.mu.Lock()
	state.Status.State = status
	state.Status.LastError = errMsg
	if errMsg != "" {
		state.Status.LastErrorAt = time.Now()
	} else {
		state.Status.LastErrorAt = time.Time{}
	}
	state.mu.Unlock()
	m.emitStatus(state)
}

func (m *RuntimeManager) unregisterTools(ctx context.Context, state *ServerState) {
	if m.registry == nil {
		state.mu.Lock()
		state.Tools = make(map[string]*ToolState)
		state.toolMap = make(map[string]string)
		state.Status = summarizeStatus(state)
		state.mu.Unlock()
		m.emitStatus(state)
		return
	}

	state.mu.Lock()
	tools := make([]*ToolState, 0, len(state.Tools))
	for _, toolState := range state.Tools {
		tools = append(tools, toolState)
	}
	state.Tools = make(map[string]*ToolState)
	state.toolMap = make(map[string]string)
	state.Status = summarizeStatus(state)
	state.mu.Unlock()
	m.emitStatus(state)

	for _, toolState := range tools {
		_ = m.registry.Unregister(toolState.RegistryName)
	}
}

func summarizeStatus(state *ServerState) ServerStatus {
	status := state.Status
	status.ToolCount = len(state.Tools)
	enabled := 0
	disabled := 0
	for _, toolState := range state.Tools {
		if toolState.Enabled {
			enabled++
		} else {
			disabled++
		}
	}
	status.ToolsEnabled = enabled
	status.ToolsDisabled = disabled
	return status
}

func uniqueToolName(serverName, toolName string) string {
	sanitizedServer := sanitizeToolName(serverName)
	sanitizedTool := sanitizeToolName(toolName)
	return fmt.Sprintf("mcp_%s_%s", sanitizedServer, sanitizedTool)
}

type toolToggler interface {
	EnableTool(name string) error
	DisableTool(name string) error
}

func sleepWithBackoff(ctx context.Context, base, max time.Duration, attempt int) bool {
	delay := min(base*time.Duration(1<<minInt(attempt, 8)), max)
	jitter := time.Duration(rand.Int63n(int64(delay / 2)))
	select {
	case <-ctx.Done():
		return false
	case <-time.After(delay + jitter):
		return true
	}
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func contains(values []string, target string) bool {
	return slices.Contains(values, target)
}
