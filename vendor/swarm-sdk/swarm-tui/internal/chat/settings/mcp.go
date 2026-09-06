package settings

import (
	"fmt"
	"sort"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/tools"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/chat/commands"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/i18n"
)

// MCPSettings handles the MCP server management UI
type MCPSettings struct {
	servers              []*commands.MCPServerState
	allTools             []ToolInfo
	enabledTools         []ToolInfo
	disabledTools        []ToolInfo
	disabledBuiltinTools map[string]bool // Track disabled builtin/SDK tools
	scrollOffset         int
	onToolToggle         func(serverName, toolName string, enabled bool) error
	onBuiltinToolToggle  func(toolName string, enabled bool) error   // Callback for builtin tool toggle
	onServerToggle       func(serverName string, enabled bool) error // Callback for entire MCP server toggle
	onServerDelete       func(serverName string) error               // Callback to delete MCP server
	onServerReconnect    func(serverName string) error               // Callback to reconnect server
	toolTester           *ToolTester
	sdkTools             []tools.Tool            // SDK tools for tool tester
	configManager        *commands.ConfigManager // For persisting disabled builtin tools

	// MCP Chat
	chatMessages []AgentChatMessage   // Chat history (reusing AgentChatMessage type)
	onChatSend   func(message string) // Callback to send chat message
}

// ToolInfo represents a tool with its metadata
type ToolInfo struct {
	Name        string
	ServerName  string
	Description string
	InputSchema string
	IsBuiltin   bool
	Source      string // "sdk", "ii", "mcp" - where the tool comes from
	Category    string // "file", "shell", "productivity", "dev", "agent", etc.
}

// ConfigurableToolItem represents a tool that can be enabled/disabled
type ConfigurableToolItem struct {
	Tool       ToolInfo
	IsEnabled  bool
	ServerName string
}

// CategoryItem represents a tool category in the list
type CategoryItem struct {
	Name         string
	Icon         string
	EnabledCount int
	TotalCount   int
	IsExpanded   bool
}

// ServerItem represents an MCP server with its tools (for server grouping view)
type ServerItem struct {
	Name         string
	IsExpanded   bool
	EnabledCount int
	TotalCount   int
	IsBuiltin    bool // True for "SDK Builtin Tools"
}

// ListItem can be a category, server header, or a tool
type ListItem struct {
	IsCategory bool
	IsServer   bool
	Category   *CategoryItem
	Server     *ServerItem
	Tool       *ConfigurableToolItem
	Index      int // Original index in the tools list (for tools only)
}

// ToolCategory defines the structure for tool categorization
type ToolCategory struct {
	Name string
	Icon string
}

// Define tool categories
var toolCategories = []ToolCategory{
	{"📁 File & Write Tools", "📁"},
	{"Shell & Execution", ""},
	{"📝 Memory & Task Management", "📝"},
	{"🤖 Agent & Collaboration", "🤖"},
	{"🌐 Browser & UI", "🌐"},
	{"🔧 Development Tools", "🔧"},
	{"🌍 Web & Search", "🌍"},
	{"🔌 MCP Tools", "🔌"},
	{"🔨 Other Internal Tools", "🔨"},
}

// categorizeToolByName returns the category for a given tool
func categorizeToolByName(toolName string, source string, isBuiltin bool) string {
	// File & Write Tools (includes both SDK builtin and II package)
	fileTools := []string{
		// SDK builtin
		"file_read", "file_write", "file_edit", "apply_patch", "grep", "list_dir", "swarm_Read", "swarm_Write", "swarm_Edit", "swarm_Grep",
		// II package
		"Read", "Write", "Edit", "Grep",
	}
	for _, t := range fileTools {
		if toolName == t || strings.Contains(toolName, t) {
			return "📁 File & Write Tools"
		}
	}

	// Shell & Execution (includes both SDK builtin and II package)
	shellTools := []string{
		// SDK builtin only
		"bash", "swarm_Bash",
	}
	for _, t := range shellTools {
		if toolName == t || strings.Contains(toolName, t) {
			return "Shell & Execution"
		}
	}

	// Memory & Task Management (includes both SDK builtin and II package)
	memoryTools := []string{
		// SDK builtin (legacy)
		"todo_read", "todo_write", "dev_checkpoint", "dev_database", "swarm_TodoRead", "swarm_TodoWrite",
		// II package - canonical task tool
		"TaskManage",
	}
	for _, t := range memoryTools {
		if toolName == t || strings.Contains(toolName, t) {
			return "📝 Memory & Task Management"
		}
	}

	// Agent & Collaboration (includes both SDK builtin and II package)
	agentTools := []string{
		// SDK builtin
		"delegate_task", "spawn_background_agent", "check_background_agent",
		"ask_user_question", "swarm_Task", "swarm_TaskOutput", "swarm_ask_user_question",
		"swarm_BackgroundTask",
		// Actual tool names (not file names)
		"Task", "TaskOutput", "BackgroundTask",
	}
	for _, t := range agentTools {
		if toolName == t || strings.Contains(toolName, t) {
			return "🤖 Agent & Collaboration"
		}
	}

	// Browser & UI (includes both SDK builtin and II package)
	browserTools := []string{
		// II package
		"browser_navigate", "browser_view", "browser_click", "browser_drag",
		"browser_get_select_options", "browser_select_dropdown_option",
		"browser_enter_text", "browser_enter_multi_texts",
		"browser_press_key", "browser_restart", "browser_scroll_down",
		"browser_scroll_up", "browser_wait", "browser_tab",
		"browser_switch_tab", "browser_open_new_tab",
	}
	for _, t := range browserTools {
		if toolName == t || strings.Contains(toolName, t) {
			return "🌐 Browser & UI"
		}
	}

	// Development Tools (includes both SDK builtin and II package)
	devTools := []string{
		// SDK builtin
		"debug_inspect", "dev_init", "dev_server",
		// II package
		"dev_checkpoint", "dev_database", "dev_init", "dev_port",
		"save_checkpoint", "get_database_connection", "register_deployment",
		"fullstack_project_init", "debug_logs",
	}
	for _, t := range devTools {
		if toolName == t || strings.Contains(toolName, t) {
			return "🔧 Development Tools"
		}
	}

	// Web & Search
	webTools := []string{"websearch", "web_fetch", "anthropic_web_search", "swarm_anthropic_web_search", "context7", "swarm_mcp_context7"}
	for _, t := range webTools {
		if toolName == t || strings.Contains(toolName, t) {
			return "🌍 Web & Search"
		}
	}

	// MCP Tools (non-builtin from MCP servers)
	if source == "mcp" && !isBuiltin {
		return "🔌 MCP Tools"
	}

	// Default: Other Internal Tools
	return "🔨 Other Internal Tools"
}

// NewMCPSettings creates a new MCP settings component
func NewMCPSettings() *MCPSettings {
	return &MCPSettings{
		servers:              []*commands.MCPServerState{},
		allTools:             []ToolInfo{},
		enabledTools:         []ToolInfo{},
		disabledTools:        []ToolInfo{},
		disabledBuiltinTools: make(map[string]bool),
		scrollOffset:         0,
		toolTester:           NewToolTester(),
		sdkTools:             []tools.Tool{},
		chatMessages:         []AgentChatMessage{},
	}
}

// SetBuiltinToolToggleCallback registers a handler for builtin tool enable/disable toggles.
func (m *MCPSettings) SetBuiltinToolToggleCallback(handler func(toolName string, enabled bool) error) {
	m.onBuiltinToolToggle = handler
}

// SetDisabledBuiltinTools sets the initial list of disabled builtin tools
func (m *MCPSettings) SetDisabledBuiltinTools(disabled map[string]bool) {
	if disabled == nil {
		m.disabledBuiltinTools = make(map[string]bool)
	} else {
		m.disabledBuiltinTools = disabled
	}
	// Rebuild tool lists to reflect the new disabled state
	m.updateToolLists()
}

// GetDisabledBuiltinTools returns the map of disabled builtin tools
func (m *MCPSettings) GetDisabledBuiltinTools() map[string]bool {
	return m.disabledBuiltinTools
}

// SetConfigManager sets the config manager for persisting settings
func (m *MCPSettings) SetConfigManager(cm *commands.ConfigManager) {
	m.configManager = cm
}

// saveDisabledBuiltinTools persists the disabled builtin tools to config
func (m *MCPSettings) saveDisabledBuiltinTools() error {
	if m.configManager == nil {
		return nil
	}

	config, err := m.configManager.LoadConfig()
	if err != nil {
		// Create new config if load fails
		config = &commands.SwarmOSConfig{
			CurrentProvider: "ClaudeCode",
			CurrentModel:    "claude-opus-4-20250514",
		}
	}

	// Update disabled builtin tools
	config.DisabledBuiltinTools = m.disabledBuiltinTools

	return m.configManager.SaveConfig(config)
}

// SetSDKTools sets the SDK tools available for testing
func (m *MCPSettings) SetSDKTools(toolList []tools.Tool) {
	m.sdkTools = toolList
	if m.toolTester != nil {
		m.toolTester.SetTools(toolList)
	}
}

// GetToolTester returns the tool tester component
func (m *MCPSettings) GetToolTester() *ToolTester {
	return m.toolTester
}

// SetServers updates the server list
func (m *MCPSettings) SetServers(servers []*commands.MCPServerState) {
	m.servers = servers
	m.updateToolLists()
}

// SetAllTools sets all available tools (builtin + MCP)
func (m *MCPSettings) SetAllTools(tools []ToolInfo) {
	m.allTools = tools
	m.updateToolLists()
}

// SetToolToggleCallback registers a handler for tool enable/disable toggles.
func (m *MCPSettings) SetToolToggleCallback(handler func(serverName, toolName string, enabled bool) error) {
	m.onToolToggle = handler
}

// SetChatCallback registers a handler for MCP assistant chat messages
func (m *MCPSettings) SetChatCallback(handler func(message string)) {
	m.onChatSend = handler
}

// SetServerReconnectCallback registers a handler for server reconnection
func (m *MCPSettings) SetServerReconnectCallback(handler func(serverName string) error) {
	m.onServerReconnect = handler
}

// SetServerToggleCallback registers a handler for entire MCP server enable/disable toggles
func (m *MCPSettings) SetServerToggleCallback(handler func(serverName string, enabled bool) error) {
	m.onServerToggle = handler
}

// SetServerDeleteCallback registers a handler for MCP server deletion
func (m *MCPSettings) SetServerDeleteCallback(handler func(serverName string) error) {
	m.onServerDelete = handler
}

// AddChatMessage adds a message to the chat history
func (m *MCPSettings) AddChatMessage(role, content string, toolCalls []ToolCallDisplay, turns int) {
	m.chatMessages = append(m.chatMessages, AgentChatMessage{
		Role:      role,
		Content:   content,
		ToolCalls: toolCalls,
		Turns:     turns,
	})
}

// ResetChat clears the chat history
func (m *MCPSettings) ResetChat() {
	m.chatMessages = []AgentChatMessage{}
}

// buildConfigurableToolsList builds a list of all tools that can be configured
func (m *MCPSettings) buildConfigurableToolsList() []ConfigurableToolItem {
	var items []ConfigurableToolItem

	// Add builtin tools (now toggleable!)
	for _, tool := range m.allTools {
		if tool.IsBuiltin {
			isDisabled := m.disabledBuiltinTools[tool.Name]
			items = append(items, ConfigurableToolItem{
				Tool:       tool,
				IsEnabled:  !isDisabled,
				ServerName: "builtin",
			})
		}
	}

	// Add MCP tools (toggleable)
	for _, server := range m.servers {
		if !server.Connected || server.Client == nil {
			continue
		}
		for _, mcpTool := range server.Tools {
			isDisabled := false
			if server.Config.DisabledTools != nil {
				_, isDisabled = server.Config.DisabledTools[mcpTool.Name]
			}
			items = append(items, ConfigurableToolItem{
				Tool: ToolInfo{
					Name:        mcpTool.Name,
					ServerName:  server.Config.Name,
					Description: mcpTool.Description,
					IsBuiltin:   false,
					Source:      "mcp",
					Category:    "external",
				},
				IsEnabled:  !isDisabled,
				ServerName: server.Config.Name,
			})
		}
	}
	return items
}

// buildToolsGroupedByServer builds a hierarchical list with servers as top-level groups and tools beneath
func (m *MCPSettings) buildToolsGroupedByServer(state *State) []ListItem {
	tools := m.buildConfigurableToolsList()

	// Group tools by server
	serverMap := make(map[string][]ConfigurableToolItem)
	builtinTools := []ConfigurableToolItem{}

	for _, tool := range tools {
		if tool.Tool.IsBuiltin {
			builtinTools = append(builtinTools, tool)
		} else {
			serverMap[tool.ServerName] = append(serverMap[tool.ServerName], tool)
		}
	}

	var items []ListItem

	// Add builtin tools server group first if there are any
	if len(builtinTools) > 0 {
		enabledCount := 0
		for _, tool := range builtinTools {
			if tool.IsEnabled {
				enabledCount++
			}
		}
		isExpanded := state.MCPConfigureToolsExpandedServers["SDK Builtin Tools"]
		items = append(items, ListItem{
			IsServer: true,
			Server: &ServerItem{
				Name:         "SDK Builtin Tools",
				IsExpanded:   isExpanded,
				EnabledCount: enabledCount,
				TotalCount:   len(builtinTools),
				IsBuiltin:    true,
			},
		})
		if isExpanded {
			for i, tool := range builtinTools {
				items = append(items, ListItem{
					IsServer: false,
					Tool:     &tool,
					Index:    i,
				})
			}
		}
	}

	// Add MCP server groups in order
	// First collect server names and sort for consistent ordering
	var serverNames []string
	for serverName := range serverMap {
		serverNames = append(serverNames, serverName)
	}
	sort.Strings(serverNames)

	for _, serverName := range serverNames {
		toolsForServer := serverMap[serverName]
		enabledCount := 0
		for _, tool := range toolsForServer {
			if tool.IsEnabled {
				enabledCount++
			}
		}

		isExpanded := state.MCPConfigureToolsExpandedServers[serverName]
		items = append(items, ListItem{
			IsServer: true,
			Server: &ServerItem{
				Name:         serverName,
				IsExpanded:   isExpanded,
				EnabledCount: enabledCount,
				TotalCount:   len(toolsForServer),
				IsBuiltin:    false,
			},
		})

		if isExpanded {
			for i, tool := range toolsForServer {
				items = append(items, ListItem{
					IsServer: false,
					Tool:     &tool,
					Index:    i,
				})
			}
		}
	}

	return items
}

// buildCategorizedToolsList builds a hierarchical list with categories and tools
func (m *MCPSettings) buildCategorizedToolsList(state *State) []ListItem {
	tools := m.buildConfigurableToolsList()

	// Group tools by category
	categoryMap := make(map[string][]ConfigurableToolItem)
	for _, tool := range tools {
		category := categorizeToolByName(tool.Tool.Name, tool.Tool.Source, tool.Tool.IsBuiltin)
		categoryMap[category] = append(categoryMap[category], tool)
	}

	// Build list items with categories
	var items []ListItem

	// Iterate through predefined category order
	for _, catDef := range toolCategories {
		toolsInCategory := categoryMap[catDef.Name]
		if len(toolsInCategory) == 0 {
			continue // Skip empty categories
		}

		// Count enabled/disabled
		enabledCount := 0
		for _, tool := range toolsInCategory {
			if tool.IsEnabled {
				enabledCount++
			}
		}

		// Check if category is expanded
		isExpanded := state.MCPConfigureToolsExpandedCategories[catDef.Name]

		// Add category header
		items = append(items, ListItem{
			IsCategory: true,
			Category: &CategoryItem{
				Name:         catDef.Name,
				Icon:         catDef.Icon,
				EnabledCount: enabledCount,
				TotalCount:   len(toolsInCategory),
				IsExpanded:   isExpanded,
			},
		})

		// Add tools if expanded
		if isExpanded {
			for i, tool := range toolsInCategory {
				items = append(items, ListItem{
					IsCategory: false,
					Tool:       &tool,
					Index:      i, // Store original index within category
				})
			}
		}
	}

	return items
}

// updateToolLists rebuilds the enabled/disabled tool lists
func (m *MCPSettings) updateToolLists() {
	m.enabledTools = []ToolInfo{}
	m.disabledTools = []ToolInfo{}

	// Add builtin tools - check disabled state
	for _, tool := range m.allTools {
		if tool.IsBuiltin {
			// Check if this builtin tool is disabled
			if m.disabledBuiltinTools != nil {
				if disabled := m.disabledBuiltinTools[tool.Name]; disabled {
					m.disabledTools = append(m.disabledTools, tool)
					continue
				}
			}
			m.enabledTools = append(m.enabledTools, tool)
		}
	}

	// Add MCP tools based on server state
	for _, server := range m.servers {
		if !server.Connected || server.Client == nil {
			continue
		}

		// Get all tools from the server
		for _, tool := range server.Tools {
			toolName := tool.Name

			info := ToolInfo{
				Name:        toolName,
				ServerName:  server.Config.Name,
				Description: tool.Description,
				InputSchema: formatInputSchema(tool.InputSchema),
				IsBuiltin:   false,
				Source:      "mcp",
				Category:    "external",
			}

			// Check if disabled
			isDisabled := false
			if server.Config.DisabledTools != nil {
				if _, disabled := server.Config.DisabledTools[toolName]; disabled {
					isDisabled = true
				}
			}

			if isDisabled {
				m.disabledTools = append(m.disabledTools, info)
			} else {
				m.enabledTools = append(m.enabledTools, info)
			}
		}
	}
}

// formatInputSchema converts input schema to a readable string
func formatInputSchema(schema map[string]any) string {
	if schema == nil {
		return ""
	}

	props, ok := schema["properties"].(map[string]any)
	if !ok {
		return ""
	}

	var params []string
	for name := range props {
		params = append(params, name)
	}

	if len(params) == 0 {
		return "no parameters"
	}

	return strings.Join(params, ", ")
}

// Render renders the MCP settings UI
func (m *MCPSettings) Render(width, height int, state *State, theme any) string {
	th := theme.(Theme)
	var rendered string

	if width < 40 || height < 20 {
		rendered = m.renderMinimal(width, height, th)
		return i18n.SettingsIntegrationsText(rendered)
	}

	// Route to different screens based on state
	switch state.MCPState {
	case "configure_tools":
		rendered = m.renderConfigureToolsScreen(width, height, state, th)
	case "mcp_config":
		rendered = m.renderMCPServersScreen(width, height, state, th)
	case "tool_tester":
		if m.toolTester != nil {
			rendered = m.toolTester.Render(width, height, th)
			break
		}
		rendered = m.renderMainScreen(width, height, state, th)
	case "mcp_chat":
		rendered = m.renderMCPChatScreen(width, height, state, th)
	case "error_detail":
		rendered = m.renderErrorDetailScreen(width, height, state, th)
	default:
		rendered = m.renderMainScreen(width, height, state, th)
	}
	return i18n.SettingsIntegrationsText(rendered)
}

// renderMainScreen renders the main Tools and MCP view with professional styling
func (m *MCPSettings) renderMainScreen(width, height int, state *State, th Theme) string {
	// Calculate layout
	const (
		borderWidth      = 2
		containerPadding = 1
		titleHeight      = 4
		separatorHeight  = 3
		hintHeight       = 2
	)

	innerWidth := maxInt(20, width-(borderWidth*2)-(containerPadding*2))
	innerHeight := maxInt(5, height-(borderWidth*2)-(containerPadding*2)-titleHeight-hintHeight)

	// Split: 25% actions, 75% tools (more space for tools)
	actionsHeight := int(float64(innerHeight) * 0.28)
	if actionsHeight < 6 {
		actionsHeight = 6
	}
	if actionsHeight > 10 {
		actionsHeight = 10
	}
	toolsHeight := innerHeight - actionsHeight - separatorHeight

	// Render sections
	title := m.renderTitle(innerWidth, th)
	actions := m.renderActions(innerWidth, actionsHeight, state, th)
	separator := m.renderSeparator(innerWidth, th)
	tools := m.renderToolsSection(innerWidth, toolsHeight, state, th)
	hints := m.renderHintBar(innerWidth, state, th)

	// Combine sections
	content := lipgloss.JoinVertical(lipgloss.Left,
		title,
		actions,
		separator,
		tools,
		hints,
	)

	// Apply container border
	containerStyle := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color(th.Border)).
		Width(maxInt(20, width-2)).
		Padding(0, containerPadding)

	return containerStyle.Render(content)
}

// renderHintBar renders keyboard navigation hints at the bottom
func (m *MCPSettings) renderHintBar(width int, state *State, th Theme) string {
	hintStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(th.TextMuted)).
		Width(width).
		Align(lipgloss.Center)

	keyStyle := lipgloss.NewStyle().
		Background(lipgloss.Color(th.BGLight)).
		Foreground(lipgloss.Color(th.Text)).
		Padding(0, 1).
		Bold(true)

	// Build context-sensitive hints
	var hints string
	switch state.MCPFocusedPanel {
	case "buttons":
		hints = fmt.Sprintf("%s navigate • %s select • %s panels",
			keyStyle.Render("←/→"),
			keyStyle.Render("Enter"),
			keyStyle.Render("Tab"))
	case "enabled", "disabled":
		hints = fmt.Sprintf("%s scroll • %s panels • %s buttons",
			keyStyle.Render("↑/↓"),
			keyStyle.Render("Tab"),
			keyStyle.Render("Shift+Tab"))
	default:
		hints = fmt.Sprintf("%s navigate • %s select • %s back",
			keyStyle.Render("↑/↓/←/→"),
			keyStyle.Render("Enter"),
			keyStyle.Render("Esc"))
	}

	return hintStyle.Render(hints)
}

// HandleConfigureToolsKey handles key input while in the configure tools screen.
func (m *MCPSettings) HandleConfigureToolsKey(key string, state *State) {
	// Build list based on view mode
	var items []ListItem
	if state.MCPConfigureToolsViewMode == "categorized" {
		items = m.buildCategorizedToolsList(state)
	} else if state.MCPConfigureToolsViewMode == "by_server" {
		// Group by MCP server
		items = m.buildToolsGroupedByServer(state)
	} else {
		// Flat view
		tools := m.buildConfigurableToolsList()
		for i, tool := range tools {
			items = append(items, ListItem{
				IsCategory: false,
				Tool:       &tool,
				Index:      i,
			})
		}
	}

	if len(items) == 0 {
		return
	}

	switch key {
	case "down", "j":
		if state.MCPConfigureToolsSelected < len(items)-1 {
			state.MCPConfigureToolsSelected++
		}
	case "up", "k":
		if state.MCPConfigureToolsSelected > 0 {
			state.MCPConfigureToolsSelected--
		}
	case "pagedown", "ctrl+d":
		state.MCPConfigureToolsSelected += 10
		if state.MCPConfigureToolsSelected >= len(items) {
			state.MCPConfigureToolsSelected = len(items) - 1
		}
	case "pageup", "ctrl+u":
		state.MCPConfigureToolsSelected -= 10
		if state.MCPConfigureToolsSelected < 0 {
			state.MCPConfigureToolsSelected = 0
		}
	case "home", "g":
		state.MCPConfigureToolsSelected = 0
		state.MCPConfigureToolsScrollOffset = 0
	case "end", "G":
		state.MCPConfigureToolsSelected = len(items) - 1
	case "enter", " ":
		m.toggleSelectedItem(state, items)
	case "e", "E":
		// Enable all tools
		m.setAllMCPToolsEnabled(true)
	case "d", "D":
		// Disable all tools
		m.setAllMCPToolsEnabled(false)
	case "t", "T":
		// Cycle through: flat → categorized → by_server → flat
		switch state.MCPConfigureToolsViewMode {
		case "flat":
			state.MCPConfigureToolsViewMode = "categorized"
		case "categorized":
			state.MCPConfigureToolsViewMode = "by_server"
		default:
			state.MCPConfigureToolsViewMode = "flat"
		}
		// Reset selection to top
		state.MCPConfigureToolsSelected = 0
		state.MCPConfigureToolsScrollOffset = 0
	case "x", "X":
		// Expand all (categories or servers depending on view mode)
		if state.MCPConfigureToolsViewMode == "categorized" {
			for _, catDef := range toolCategories {
				state.MCPConfigureToolsExpandedCategories[catDef.Name] = true
			}
		} else if state.MCPConfigureToolsViewMode == "by_server" {
			// Expand all servers
			for _, server := range m.servers {
				state.MCPConfigureToolsExpandedServers[server.Config.Name] = true
			}
			state.MCPConfigureToolsExpandedServers["SDK Builtin Tools"] = true
		}
		state.MCPConfigureToolsSelected = 0
		state.MCPConfigureToolsScrollOffset = 0
	case "c", "C":
		// Collapse all (categories or servers depending on view mode)
		if state.MCPConfigureToolsViewMode == "categorized" {
			state.MCPConfigureToolsExpandedCategories = make(map[string]bool)
		} else if state.MCPConfigureToolsViewMode == "by_server" {
			state.MCPConfigureToolsExpandedServers = make(map[string]bool)
		}
		state.MCPConfigureToolsSelected = 0
		state.MCPConfigureToolsScrollOffset = 0
	}
}

// toggleSelectedItem toggles the currently selected item (category, server, or tool)
func (m *MCPSettings) toggleSelectedItem(state *State, items []ListItem) {
	idx := state.MCPConfigureToolsSelected
	if idx < 0 || idx >= len(items) {
		return
	}

	item := items[idx]

	if item.IsCategory {
		// Toggle category expansion
		categoryName := item.Category.Name
		currentState := state.MCPConfigureToolsExpandedCategories[categoryName]
		state.MCPConfigureToolsExpandedCategories[categoryName] = !currentState
	} else if item.IsServer {
		// Toggle server expansion
		serverName := item.Server.Name
		currentState := state.MCPConfigureToolsExpandedServers[serverName]
		state.MCPConfigureToolsExpandedServers[serverName] = !currentState
	} else {
		// Toggle tool enabled/disabled
		m.toggleTool(item.Tool)
	}
}

// toggleTool toggles a specific tool's enabled/disabled state
func (m *MCPSettings) toggleTool(tool *ConfigurableToolItem) {
	if tool == nil {
		return
	}

	newEnabled := !tool.IsEnabled

	if tool.Tool.IsBuiltin {
		// Toggle builtin tool
		if m.onBuiltinToolToggle != nil {
			if err := m.onBuiltinToolToggle(tool.Tool.Name, newEnabled); err != nil {
				return
			}
		}
		// Update local state
		if newEnabled {
			delete(m.disabledBuiltinTools, tool.Tool.Name)
		} else {
			m.disabledBuiltinTools[tool.Tool.Name] = true
		}
		// Persist to config
		_ = m.saveDisabledBuiltinTools()
	} else {
		// Toggle MCP tool
		if m.onToolToggle != nil {
			if err := m.onToolToggle(tool.ServerName, tool.Tool.Name, newEnabled); err != nil {
				return
			}
		}
		// Update local state - find and update in server config
		for _, server := range m.servers {
			if server.Config.Name == tool.ServerName {
				if server.Config.DisabledTools == nil {
					server.Config.DisabledTools = make(map[string]bool)
				}
				if newEnabled {
					delete(server.Config.DisabledTools, tool.Tool.Name)
				} else {
					server.Config.DisabledTools[tool.Tool.Name] = true
				}
				break
			}
		}
	}
}

// setAllMCPToolsEnabled enables or disables ALL tools (builtin + MCP)
func (m *MCPSettings) setAllMCPToolsEnabled(enabled bool) {
	// Handle builtin tools
	for _, tool := range m.allTools {
		if tool.IsBuiltin {
			if enabled {
				delete(m.disabledBuiltinTools, tool.Name)
			} else {
				m.disabledBuiltinTools[tool.Name] = true
			}
			if m.onBuiltinToolToggle != nil {
				m.onBuiltinToolToggle(tool.Name, enabled)
			}
		}
	}
	// Persist builtin tools changes
	_ = m.saveDisabledBuiltinTools()

	// Handle MCP tools
	for _, server := range m.servers {
		if !server.Connected || server.Client == nil {
			continue
		}
		if server.Config.DisabledTools == nil {
			server.Config.DisabledTools = make(map[string]bool)
		}
		for _, mcpTool := range server.Tools {
			if enabled {
				// Enable: remove from disabled list
				delete(server.Config.DisabledTools, mcpTool.Name)
			} else {
				// Disable: add to disabled list
				server.Config.DisabledTools[mcpTool.Name] = true
			}
			// Notify callback (this also persists MCP config)
			if m.onToolToggle != nil {
				m.onToolToggle(server.Config.Name, mcpTool.Name, enabled)
			}
		}
	}
}
