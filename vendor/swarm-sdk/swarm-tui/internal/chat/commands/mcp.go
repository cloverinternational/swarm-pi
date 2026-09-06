package commands

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/tools/mcp"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/i18n"
)

// MCPServerType represents the type of MCP server connection
type MCPServerType string

const (
	MCPTypeStdio MCPServerType = "stdio" // Command-based stdio transport
	MCPTypeSSE   MCPServerType = "sse"   // Server-Sent Events transport
	MCPTypeHTTP  MCPServerType = "http"  // HTTP/Streamable HTTP transport
	MCPTypeOAuth MCPServerType = "oauth" // OAuth-authenticated HTTP transport
)

// MCPServerConfig represents an MCP server configuration
type MCPServerConfig struct {
	Name       string            `json:"name"`
	Type       MCPServerType     `json:"type,omitempty"` // Server type: stdio, sse, http, oauth (default: stdio)
	Command    string            `json:"command,omitempty"`
	Args       []string          `json:"args,omitempty"`
	Env        map[string]string `json:"env,omitempty"`
	WorkingDir string            `json:"working_dir,omitempty"`
	URL        string            `json:"url,omitempty"`     // For SSE/HTTP/OAuth servers
	Headers    map[string]string `json:"headers,omitempty"` // For SSE/HTTP servers
	Timeout    int               `json:"timeout,omitempty"` // Connection timeout in seconds
	Enabled    bool              `json:"enabled"`
	// AutoConnect controls whether the server process/connection is started at swarm startup.
	// nil means "use the default": auto-connect if Enabled is true, or use transport defaults.
	//   - If server is Enabled: auto-connect (user wants to use it)
	//   - stdio + not enabled: default false (spawning subprocess is expensive)
	//   - http/sse/oauth + not enabled: default true (no subprocess cost)
	// Set explicitly to false in mcp_servers.json to prevent auto-connect even when enabled.
	AutoConnect *bool `json:"auto_connect,omitempty"`
	// OAuth configuration (for MCPTypeOAuth)
	OAuth *MCPOAuthConfig `json:"oauth,omitempty"`
	// Tool-specific settings
	EnabledTools  map[string]bool `json:"enabled_tools,omitempty"`  // tool name -> enabled
	DisabledTools map[string]bool `json:"disabled_tools,omitempty"` // tool name -> disabled
}

// ShouldAutoConnect returns true when this server should connect at startup.
// Priority: explicit AutoConnect > Enabled flag > transport-based defaults.
// If a server is explicitly enabled, it should auto-connect (user intent).
func (c *MCPServerConfig) ShouldAutoConnect() bool {
	// Explicit AutoConnect setting takes precedence
	if c.AutoConnect != nil {
		return *c.AutoConnect
	}
	// If server is explicitly enabled, auto-connect it
	// This respects user intent: enabled = I want to use this server
	if c.Enabled {
		return true
	}
	// Default: stdio doesn't auto-connect; remote transports do.
	return c.GetType() != MCPTypeStdio
}

// MCPOAuthConfig holds OAuth configuration for MCP servers
type MCPOAuthConfig struct {
	ClientID     string `json:"client_id"`
	ClientSecret string `json:"client_secret,omitempty"` // Optional for public clients
	Scopes       string `json:"scopes,omitempty"`        // Space-separated scopes
}

// GetType returns the server type with default fallback to stdio
func (c *MCPServerConfig) GetType() MCPServerType {
	if c.Type == "" {
		return MCPTypeStdio
	}
	return c.Type
}

// MCPServerState tracks runtime state of a server
type MCPServerState struct {
	Config    *MCPServerConfig
	Client    *mcp.Client
	Connected bool
	Tools     []*mcp.MCPTool
	Error     string
	// Detailed error tracking for debugging
	LastError          error     // Full error with context
	ErrorTimestamp     time.Time // When the error occurred
	ConnectionAttempts int       // Number of connection attempts
	LastAttempt        time.Time // Last connection attempt time
}

// MCPCommand implements the /mcp command for MCP server management
type MCPCommand struct {
	interactive  bool
	state        string // "menu", "list", "add", "configure", "tool_detail", "import_claude"
	selectedMenu int
	selectedItem int
	scrollOffset int
	maxVisible   int
	width        int
	height       int

	// Server management
	servers       []*MCPServerState
	currentServer *MCPServerState
	currentTool   *mcp.MCPTool

	// Import from Claude
	claudeServers []*MCPServerConfig

	// Add server form
	formField    int // Which field is being edited
	formName     string
	formType     MCPServerType // stdio, sse, http, oauth
	formCommand  string
	formArgs     string
	formURL      string
	formHeaders  string
	formClientID string // For OAuth
	formScopes   string // For OAuth
	formEnv      string
	formWorkDir  string
	formTimeout  string

	// Callbacks
	onServerAdded    func(*MCPServerConfig)
	onServerToggled  func(string, bool)
	onToolToggled    func(serverName, toolName string, enabled bool)
	onRefreshServers func() []*MCPServerState
}

// Menu options
const (
	MCPMenuListServers  = 0
	MCPMenuAddServer    = 1
	MCPMenuImportClaude = 2
	MCPMenuRefresh      = 3
	MCPMenuAssistant    = 4
)

func mcpMenuOptions() []string {
	return []string{
		i18n.T("commands_b.mcp.menu.manage"),
		i18n.T("commands_b.mcp.menu.add"),
		i18n.T("commands_b.mcp.menu.import"),
		i18n.T("commands_b.mcp.menu.refresh"),
		i18n.T("commands_b.mcp.menu.assistant"),
	}
}

// Form fields
const (
	FormFieldName = iota
	FormFieldType
	FormFieldCommand  // For stdio
	FormFieldArgs     // For stdio
	FormFieldURL      // For sse/http/oauth
	FormFieldHeaders  // For sse/http
	FormFieldClientID // For oauth
	FormFieldScopes   // For oauth
	FormFieldEnv
	FormFieldWorkDir
	FormFieldTimeout
)

// NewMCPCommand creates a new /mcp command
func NewMCPCommand() *MCPCommand {
	return &MCPCommand{
		interactive:  false,
		state:        "menu",
		selectedMenu: 0,
		selectedItem: 0,
		scrollOffset: 0,
		maxVisible:   10,
		width:        80,
		height:       24,
		servers:      []*MCPServerState{},
		formField:    FormFieldName,
		formType:     MCPTypeStdio,
	}
}

func (m *MCPCommand) Name() string {
	return "mcp"
}

func (m *MCPCommand) Description() string {
	return i18n.T("commands_b.mcp.description")
}

func (m *MCPCommand) Aliases() []string {
	return []string{"mcps", "servers"}
}

func (m *MCPCommand) Execute(args []string) tea.Cmd {
	m.interactive = true
	m.state = "menu"
	m.selectedMenu = 0
	// Load servers if refresh callback is set
	if m.onRefreshServers != nil {
		m.servers = m.onRefreshServers()
	}
	return nil
}

func (m *MCPCommand) IsInteractive() bool {
	return m.interactive
}

// SetOnServerAdded sets callback when server is added
func (m *MCPCommand) SetOnServerAdded(fn func(*MCPServerConfig)) {
	m.onServerAdded = fn
}

// SetOnServerToggled sets callback when server is enabled/disabled
func (m *MCPCommand) SetOnServerToggled(fn func(string, bool)) {
	m.onServerToggled = fn
}

// SetOnToolToggled sets callback when tool is enabled/disabled
func (m *MCPCommand) SetOnToolToggled(fn func(serverName, toolName string, enabled bool)) {
	m.onToolToggled = fn
}

// SetOnRefreshServers sets callback to refresh server list
func (m *MCPCommand) SetOnRefreshServers(fn func() []*MCPServerState) {
	m.onRefreshServers = fn
}

func (m *MCPCommand) Update(msg tea.Msg) (Command, tea.Cmd) {
	if !m.interactive {
		return m, nil
	}

	switch msg := msg.(type) {
	case tea.KeyMsg:
		return m.handleKey(msg)
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.maxVisible = max((msg.Height-12)/2, 5)
	}

	return m, nil
}

func (m *MCPCommand) handleKey(msg tea.KeyMsg) (Command, tea.Cmd) {
	switch msg.String() {
	case "esc":
		if m.state == "menu" {
			m.interactive = false
		} else {
			m.state = "menu"
		}
		return m, nil

	case "backspace":
		if m.state == "add" && m.handleFormBackspace() {
			return m, nil
		}
		if m.state != "menu" {
			m.state = "menu"
		}
		return m, nil

	case "down", "j":
		m.handleDown()
	case "up", "k":
		m.handleUp()
	case "enter":
		return m.handleEnter()
	case "tab":
		if m.state == "add" {
			m.formField = m.nextFormField()
		}
	case "shift+tab":
		if m.state == "add" {
			m.formField = m.prevFormField()
		}
	case " ", "space":
		// Allow space as text input in add form
		if m.state == "add" {
			m.handleFormInput(" ")
		} else if m.state == "list" && len(m.servers) > 0 {
			// Toggle server enabled/disabled
			server := m.servers[m.selectedItem]
			newEnabled := !server.Config.Enabled
			server.Config.Enabled = newEnabled
			if m.onServerToggled != nil {
				m.onServerToggled(server.Config.Name, newEnabled)
			}
		} else if m.state == "configure" && m.currentServer != nil {
			// Toggle tool enabled/disabled
			if len(m.currentServer.Tools) > 0 && m.selectedItem < len(m.currentServer.Tools) {
				tool := m.currentServer.Tools[m.selectedItem]
				// Check current state
				enabled := true
				if m.currentServer.Config.DisabledTools != nil {
					if _, disabled := m.currentServer.Config.DisabledTools[tool.Name]; disabled {
						enabled = false
					}
				}
				newEnabled := !enabled

				// Update config
				if m.currentServer.Config.DisabledTools == nil {
					m.currentServer.Config.DisabledTools = make(map[string]bool)
				}
				if newEnabled {
					delete(m.currentServer.Config.DisabledTools, tool.Name)
				} else {
					m.currentServer.Config.DisabledTools[tool.Name] = true
				}

				if m.onToolToggled != nil {
					m.onToolToggled(m.currentServer.Config.Name, tool.Name, newEnabled)
				}
			}
		}
		// Explicitly ignore space in menu and other states
		return m, nil
	default:
		// Text input for add form
		if m.state == "add" {
			m.handleFormInput(msg.String())
		}
	}

	return m, nil
}

func (m *MCPCommand) handleDown() {
	switch m.state {
	case "menu":
		if m.selectedMenu < len(mcpMenuOptions())-1 {
			m.selectedMenu++
		}
	case "list":
		if m.selectedItem < len(m.servers)-1 {
			m.selectedItem++
			if m.selectedItem >= m.scrollOffset+m.maxVisible {
				m.scrollOffset = m.selectedItem - m.maxVisible + 1
			}
		}
	case "import_claude":
		if m.selectedItem < len(m.claudeServers)-1 {
			m.selectedItem++
			if m.selectedItem >= m.scrollOffset+m.maxVisible {
				m.scrollOffset = m.selectedItem - m.maxVisible + 1
			}
		}
	case "configure":
		if m.currentServer != nil && m.selectedItem < len(m.currentServer.Tools)-1 {
			m.selectedItem++
			if m.selectedItem >= m.scrollOffset+m.maxVisible {
				m.scrollOffset = m.selectedItem - m.maxVisible + 1
			}
		}
	}
}

func (m *MCPCommand) handleUp() {
	switch m.state {
	case "menu":
		if m.selectedMenu > 0 {
			m.selectedMenu--
		}
	case "list", "configure", "import_claude":
		if m.selectedItem > 0 {
			m.selectedItem--
			if m.selectedItem < m.scrollOffset {
				m.scrollOffset = m.selectedItem
			}
		}
	}
}

func (m *MCPCommand) handleEnter() (Command, tea.Cmd) {
	switch m.state {
	case "menu":
		switch m.selectedMenu {
		case MCPMenuListServers:
			m.state = "list"
			m.selectedItem = 0
			m.scrollOffset = 0
		case MCPMenuAddServer:
			m.state = "add"
			m.formField = FormFieldName
			m.formName = ""
			m.formType = MCPTypeStdio
			m.formCommand = ""
			m.formArgs = ""
			m.formURL = ""
			m.formHeaders = ""
			m.formClientID = ""
			m.formScopes = ""
			m.formEnv = ""
			m.formWorkDir = ""
			m.formTimeout = ""
		case MCPMenuImportClaude:
			m.state = "import_claude"
			m.selectedItem = 0
			m.scrollOffset = 0
			m.claudeServers = m.discoverClaudeServers()
		case MCPMenuRefresh:
			if m.onRefreshServers != nil {
				m.servers = m.onRefreshServers()
			}
		case MCPMenuAssistant:
			// Note: MCP Assistant is available in Settings > MCP
			// For now, just show a message
			m.state = "assistant_info"
		}
	case "list":
		if len(m.servers) > 0 && m.selectedItem < len(m.servers) {
			m.currentServer = m.servers[m.selectedItem]
			m.state = "configure"
			m.selectedItem = 0
			m.scrollOffset = 0
		}
	case "add":
		// Submit form - validate based on server type
		valid := m.formName != ""
		switch m.formType {
		case MCPTypeStdio:
			valid = valid && m.formCommand != ""
		case MCPTypeOAuth:
			// OAuth requires URL and ClientID
			valid = valid && m.formURL != "" && m.formClientID != ""
		default:
			// SSE or HTTP require URL
			valid = valid && m.formURL != ""
		}

		if valid {
			config := &MCPServerConfig{
				Name:          m.formName,
				Type:          m.formType,
				Enabled:       true,
				EnabledTools:  make(map[string]bool),
				DisabledTools: make(map[string]bool),
			}

			// Set type-specific fields
			switch m.formType {
			case MCPTypeStdio:
				config.Command = m.formCommand
				config.Args = parseArgs(m.formArgs)
				config.WorkingDir = m.formWorkDir
			case MCPTypeOAuth:
				config.URL = m.formURL
				config.OAuth = &MCPOAuthConfig{
					ClientID: m.formClientID,
					Scopes:   m.formScopes,
				}
			default:
				// SSE and HTTP
				config.URL = m.formURL
				config.Headers = parseEnv(m.formHeaders) // Same format as env
			}

			// Common optional fields
			config.Env = parseEnv(m.formEnv)
			if m.formTimeout != "" {
				if timeout, err := strconv.Atoi(m.formTimeout); err == nil {
					config.Timeout = timeout
				}
			}

			if m.onServerAdded != nil {
				m.onServerAdded(config)
			}
			// Refresh server list to show the newly added server
			if m.onRefreshServers != nil {
				m.servers = m.onRefreshServers()
			}
			m.state = "menu"
		}
	case "configure":
		if m.currentServer != nil && len(m.currentServer.Tools) > 0 && m.selectedItem < len(m.currentServer.Tools) {
			m.currentTool = m.currentServer.Tools[m.selectedItem]
			m.state = "tool_detail"
		}
	case "import_claude":
		// Import the selected server
		if len(m.claudeServers) > 0 && m.selectedItem < len(m.claudeServers) {
			selectedServer := m.claudeServers[m.selectedItem]
			if m.onServerAdded != nil {
				m.onServerAdded(selectedServer)
			}
			// Refresh server list to show the newly imported server
			if m.onRefreshServers != nil {
				m.servers = m.onRefreshServers()
			}
			m.state = "menu"
		}
	}

	return m, nil
}

func (m *MCPCommand) handleFormInput(key string) {
	// Handle type selection with left/right arrows
	if m.formField == FormFieldType {
		switch key {
		case "left", "h":
			m.cycleFormType(-1)
		case "right", "l":
			m.cycleFormType(1)
		}
		return
	}

	// Handle basic text input
	if len(key) == 1 {
		switch m.formField {
		case FormFieldName:
			m.formName += key
		case FormFieldCommand:
			m.formCommand += key
		case FormFieldArgs:
			m.formArgs += key
		case FormFieldURL:
			m.formURL += key
		case FormFieldHeaders:
			m.formHeaders += key
		case FormFieldClientID:
			m.formClientID += key
		case FormFieldScopes:
			m.formScopes += key
		case FormFieldEnv:
			m.formEnv += key
		case FormFieldWorkDir:
			m.formWorkDir += key
		case FormFieldTimeout:
			// Only allow digits
			if key >= "0" && key <= "9" {
				m.formTimeout += key
			}
		}
	}
}

// cycleFormType cycles through available server types
func (m *MCPCommand) cycleFormType(direction int) {
	types := []MCPServerType{MCPTypeStdio, MCPTypeSSE, MCPTypeHTTP, MCPTypeOAuth}
	currentIdx := 0
	for i, t := range types {
		if t == m.formType {
			currentIdx = i
			break
		}
	}
	newIdx := (currentIdx + direction + len(types)) % len(types)
	m.formType = types[newIdx]
}

// nextFormField returns the next form field index based on server type
func (m *MCPCommand) nextFormField() int {
	fields := m.getFormFieldsForType()
	currentIdx := 0
	for i, f := range fields {
		if f == m.formField {
			currentIdx = i
			break
		}
	}
	nextIdx := (currentIdx + 1) % len(fields)
	return fields[nextIdx]
}

// prevFormField returns the previous form field index based on server type
func (m *MCPCommand) prevFormField() int {
	fields := m.getFormFieldsForType()
	currentIdx := 0
	for i, f := range fields {
		if f == m.formField {
			currentIdx = i
			break
		}
	}
	prevIdx := (currentIdx - 1 + len(fields)) % len(fields)
	return fields[prevIdx]
}

// getFormFieldsForType returns the applicable form fields based on server type
func (m *MCPCommand) getFormFieldsForType() []int {
	switch m.formType {
	case MCPTypeStdio:
		return []int{FormFieldName, FormFieldType, FormFieldCommand, FormFieldArgs, FormFieldEnv, FormFieldWorkDir, FormFieldTimeout}
	case MCPTypeOAuth:
		// OAuth requires URL, ClientID, and optional Scopes
		return []int{FormFieldName, FormFieldType, FormFieldURL, FormFieldClientID, FormFieldScopes, FormFieldEnv, FormFieldTimeout}
	default:
		// SSE and HTTP use URL-based configuration
		return []int{FormFieldName, FormFieldType, FormFieldURL, FormFieldHeaders, FormFieldEnv, FormFieldTimeout}
	}
}

func (m *MCPCommand) handleFormBackspace() bool {
	// Can't backspace on type selector
	if m.formField == FormFieldType {
		return false
	}

	var target *string
	switch m.formField {
	case FormFieldName:
		target = &m.formName
	case FormFieldCommand:
		target = &m.formCommand
	case FormFieldArgs:
		target = &m.formArgs
	case FormFieldURL:
		target = &m.formURL
	case FormFieldHeaders:
		target = &m.formHeaders
	case FormFieldClientID:
		target = &m.formClientID
	case FormFieldScopes:
		target = &m.formScopes
	case FormFieldEnv:
		target = &m.formEnv
	case FormFieldWorkDir:
		target = &m.formWorkDir
	case FormFieldTimeout:
		target = &m.formTimeout
	}

	if target != nil && len(*target) > 0 {
		*target = (*target)[:len(*target)-1]
		return true
	}
	return false
}

// Helper functions

func parseArgs(argsStr string) []string {
	if argsStr == "" {
		return []string{}
	}
	// Simple space-separated parsing
	return strings.Fields(argsStr)
}

func parseEnv(envStr string) map[string]string {
	env := make(map[string]string)
	if envStr == "" {
		return env
	}
	// Parse KEY=VALUE,KEY2=VALUE2 format
	pairs := strings.SplitSeq(envStr, ",")
	for pair := range pairs {
		parts := strings.SplitN(strings.TrimSpace(pair), "=", 2)
		if len(parts) == 2 {
			env[parts[0]] = parts[1]
		}
	}
	return env
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max-3] + "..."
}

func prettyJSON(v any) (string, error) {
	// Simple JSON formatting for display
	if m, ok := v.(map[string]any); ok {
		var lines []string
		for k, val := range m {
			lines = append(lines, fmt.Sprintf("  %s: %v", k, val))
		}
		return strings.Join(lines, "\n"), nil
	}
	return fmt.Sprintf("%v", v), nil
}

// discoverClaudeServers finds MCP servers configured in Claude's directories
func (m *MCPCommand) discoverClaudeServers() []*MCPServerConfig {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return nil
	}

	var discovered []*MCPServerConfig

	// Check for .mcp.json in the root .claude directory
	rootMCPPath := filepath.Join(homeDir, ".claude", ".mcp.json")
	if configs := m.parseClaudeMCPFile(rootMCPPath); configs != nil {
		discovered = append(discovered, configs...)
	}

	// Also check for claude_desktop_config.json (Claude Desktop format)
	desktopConfigPath := filepath.Join(homeDir, "Library", "Application Support", "Claude", "claude_desktop_config.json")
	if configs := m.parseClaudeDesktopConfig(desktopConfigPath); configs != nil {
		discovered = append(discovered, configs...)
	}

	// Walk through plugin directories looking for .mcp.json files
	claudePluginsDir := filepath.Join(homeDir, ".claude", "plugins")
	err = filepath.Walk(claudePluginsDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil // Skip errors
		}

		if !info.IsDir() && info.Name() == ".mcp.json" {
			if configs := m.parseClaudeMCPFile(path); configs != nil {
				discovered = append(discovered, configs...)
			}
		}

		return nil
	})

	if err != nil {
		return discovered
	}

	return discovered
}

// parseClaudeMCPFile parses a .mcp.json file in Claude's format
func (m *MCPCommand) parseClaudeMCPFile(path string) []*MCPServerConfig {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}

	// Claude's .mcp.json format is a map of server configs
	var claudeServers map[string]map[string]any
	if err := json.Unmarshal(data, &claudeServers); err != nil {
		return nil
	}

	var configs []*MCPServerConfig

	// Convert each server to our format
	for serverName, serverConfig := range claudeServers {
		if config := m.convertClaudeServerConfig(serverName, serverConfig); config != nil {
			configs = append(configs, config)
		}
	}

	return configs
}

// parseClaudeDesktopConfig parses Claude Desktop's claude_desktop_config.json
func (m *MCPCommand) parseClaudeDesktopConfig(path string) []*MCPServerConfig {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}

	var desktopConfig struct {
		MCPServers map[string]map[string]any `json:"mcpServers"`
	}

	if err := json.Unmarshal(data, &desktopConfig); err != nil {
		return nil
	}

	var configs []*MCPServerConfig

	// Convert each server to our format
	for serverName, serverConfig := range desktopConfig.MCPServers {
		if config := m.convertClaudeServerConfig(serverName, serverConfig); config != nil {
			configs = append(configs, config)
		}
	}

	return configs
}

// convertClaudeServerConfig converts Claude's server config format to ours
func (m *MCPCommand) convertClaudeServerConfig(serverName string, serverConfig map[string]any) *MCPServerConfig {
	config := &MCPServerConfig{
		Name:          serverName,
		Enabled:       false, // Disabled by default until user enables
		EnabledTools:  make(map[string]bool),
		DisabledTools: make(map[string]bool),
	}

	// Parse server type and configuration
	serverType := ""
	if t, ok := serverConfig["type"].(string); ok {
		serverType = t
	}

	// Parse common env field
	if env, ok := serverConfig["env"].(map[string]any); ok {
		config.Env = make(map[string]string)
		for k, v := range env {
			if vStr, ok := v.(string); ok {
				config.Env[k] = vStr
			}
		}
	}

	switch serverType {
	case "http", "streamable-http":
		// HTTP/Streamable HTTP server - extract URL and headers
		config.Type = MCPTypeHTTP
		if url, ok := serverConfig["url"].(string); ok {
			config.URL = url
		}
		if headers, ok := serverConfig["headers"].(map[string]any); ok {
			config.Headers = make(map[string]string)
			for k, v := range headers {
				if vStr, ok := v.(string); ok {
					config.Headers[k] = vStr
				}
			}
		}
		// Need URL for HTTP servers
		if config.URL == "" {
			return nil
		}

	case "sse":
		// SSE server - extract URL and headers
		config.Type = MCPTypeSSE
		if url, ok := serverConfig["url"].(string); ok {
			config.URL = url
		}
		if headers, ok := serverConfig["headers"].(map[string]any); ok {
			config.Headers = make(map[string]string)
			for k, v := range headers {
				if vStr, ok := v.(string); ok {
					config.Headers[k] = vStr
				}
			}
		}
		// Need URL for SSE servers
		if config.URL == "" {
			return nil
		}

	case "stdio", "":
		// stdio server - extract command and args
		config.Type = MCPTypeStdio
		if command, ok := serverConfig["command"].(string); ok {
			config.Command = command
		}
		if args, ok := serverConfig["args"].([]any); ok {
			for _, arg := range args {
				if argStr, ok := arg.(string); ok {
					config.Args = append(config.Args, argStr)
				}
			}
		}
		// Need command for stdio servers
		if config.Command == "" {
			return nil
		}

	default:
		// Unknown type, try to parse as stdio
		config.Type = MCPTypeStdio
		if command, ok := serverConfig["command"].(string); ok {
			config.Command = command
		}
		if config.Command == "" {
			return nil
		}
	}

	return config
}
