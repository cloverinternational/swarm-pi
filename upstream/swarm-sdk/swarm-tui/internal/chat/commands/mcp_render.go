package commands

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/i18n"
)

func (m *MCPCommand) View() string {
	if !m.interactive {
		return ""
	}

	var content string
	switch m.state {
	case "menu":
		content = m.renderMenu()
	case "list":
		content = m.renderServerList()
	case "add":
		content = m.renderAddServer()
	case "configure":
		content = m.renderServerConfig()
	case "tool_detail":
		content = m.renderToolDetail()
	case "import_claude":
		content = m.renderImportClaude()
	case "assistant_info":
		content = m.renderAssistantInfo()
	}

	// Center on screen
	if m.height > 0 {
		lines := strings.Count(content, "\n") + 1
		topPad := (m.height - lines) / 2
		if topPad > 0 {
			content = strings.Repeat("\n", topPad) + content
		}
	}

	return content
}

func (m *MCPCommand) renderMenu() string {
	containerWidth := 75
	if m.width > 0 && m.width < containerWidth+4 {
		containerWidth = m.width - 4
	}
	contentWidth := containerWidth - 6 // Account for padding

	// Centered title (use PlaceHorizontal to avoid background extension)
	titleText := lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color(ColorCyan)).
		Render(i18n.T("commands_b.mcp.title"))

	title := lipgloss.PlaceHorizontal(contentWidth, lipgloss.Center, titleText)

	// Count servers
	connectedCount := 0
	enabledCount := 0
	for _, s := range m.servers {
		if s.Config.Enabled {
			enabledCount++
			if s.Connected {
				connectedCount++
			}
		}
	}

	// Styled count badges
	connectedBadge := lipgloss.NewStyle().
		Background(lipgloss.Color(ColorSuccess)).
		Foreground(lipgloss.Color(ColorWhite)).
		Padding(0, 1).
		Bold(true).
		Render(i18n.T("commands_b.mcp.badge_connected", connectedCount))

	enabledBadge := lipgloss.NewStyle().
		Background(lipgloss.Color(ColorCyan)).
		Foreground(lipgloss.Color(ColorWhite)).
		Padding(0, 1).
		Render(i18n.T("commands_b.mcp.badge_enabled", enabledCount))

	totalBadge := lipgloss.NewStyle().
		Background(lipgloss.Color(ColorMuted)).
		Foreground(lipgloss.Color(ColorWhite)).
		Padding(0, 1).
		Render(i18n.T("commands_b.mcp.badge_total", len(m.servers)))

	badgeRow := fmt.Sprintf("%s  %s  %s", connectedBadge, enabledBadge, totalBadge)
	badges := lipgloss.PlaceHorizontal(contentWidth, lipgloss.Center, badgeRow)

	// Menu options with icons
	menuIcons := []string{"⚙", "+", "↓", "↻", "🤖"}
	menuDescs := []string{
		i18n.T("commands_b.mcp.menu.manage_desc"),
		i18n.T("commands_b.mcp.menu.add_desc"),
		i18n.T("commands_b.mcp.menu.import_desc"),
		i18n.T("commands_b.mcp.menu.refresh_desc"),
		i18n.T("commands_b.mcp.menu.assistant_desc"),
	}

	var items []string
	for i, option := range mcpMenuOptions() {
		isSelected := i == m.selectedMenu
		icon := menuIcons[i]
		desc := menuDescs[i]

		var itemText string
		if isSelected {
			// Selected item with highlight
			optionLine := lipgloss.NewStyle().
				Foreground(lipgloss.Color(ColorCyan)).
				Bold(true).
				Render(fmt.Sprintf("  ▶ %s  %s", icon, option))

			descLine := lipgloss.NewStyle().
				Foreground(lipgloss.Color(ColorGray)).
				Render(fmt.Sprintf("      %s", desc))

			itemText = lipgloss.JoinVertical(lipgloss.Left, optionLine, descLine, "")
		} else {
			itemText = lipgloss.NewStyle().
				Foreground(lipgloss.Color(ColorWhite)).
				Render(fmt.Sprintf("    %s  %s", icon, option))
		}

		items = append(items, itemText)
	}

	list := lipgloss.JoinVertical(lipgloss.Left, items...)

	// Styled hint bar
	hint := m.renderMenuHintBar(contentWidth)

	content := lipgloss.JoinVertical(lipgloss.Left,
		title,
		"",
		badges,
		"",
		list,
		"",
		hint,
	)

	container := lipgloss.NewStyle().
		Width(containerWidth).
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color(ColorDarkGray)).
		Padding(2, 3)

	return container.Render(content)
}

// renderMenuHintBar renders the styled hint bar for the menu
func (m *MCPCommand) renderMenuHintBar(width int) string {
	keyStyle := lipgloss.NewStyle().
		Background(lipgloss.Color(ColorPanel)).
		Foreground(lipgloss.Color(ColorWhite)).
		Padding(0, 1).
		Bold(true)

	hints := i18n.T("commands_b.mcp.hint_menu",
		keyStyle.Render("↑/↓"),
		keyStyle.Render("Enter"),
		keyStyle.Render("Esc"))

	return lipgloss.PlaceHorizontal(width, lipgloss.Center, hints)
}

func (m *MCPCommand) renderServerList() string {
	containerWidth := 90
	if m.width > 0 && m.width < containerWidth+4 {
		containerWidth = m.width - 4
	}
	contentWidth := containerWidth - 6

	// Centered title (use PlaceHorizontal to avoid background extension)
	titleText := lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color(ColorCyan)).
		Render(i18n.T("commands_b.mcp.servers_title"))

	title := lipgloss.PlaceHorizontal(contentWidth, lipgloss.Center, titleText)

	// Count by status
	connectedCount, enabledCount, errorCount := 0, 0, 0
	for _, s := range m.servers {
		if s.Config.Enabled {
			enabledCount++
			if s.Connected {
				connectedCount++
			} else if s.Error != "" {
				errorCount++
			}
		}
	}

	// Styled badges
	connectedBadge := lipgloss.NewStyle().
		Background(lipgloss.Color(ColorSuccess)).
		Foreground(lipgloss.Color(ColorWhite)).
		Padding(0, 1).
		Bold(true).
		Render(i18n.T("commands_b.mcp.badge_connected", connectedCount))

	enabledBadge := lipgloss.NewStyle().
		Background(lipgloss.Color(ColorCyan)).
		Foreground(lipgloss.Color(ColorWhite)).
		Padding(0, 1).
		Render(i18n.T("commands_b.mcp.badge_enabled", enabledCount))

	var badgeRowContent string
	if errorCount > 0 {
		errorBadge := lipgloss.NewStyle().
			Background(lipgloss.Color(ColorRed)).
			Foreground(lipgloss.Color(ColorWhite)).
			Padding(0, 1).
			Render(i18n.T("commands_b.mcp.badge_error", errorCount))
		badgeRowContent = fmt.Sprintf("%s  %s  %s", connectedBadge, enabledBadge, errorBadge)
	} else {
		badgeRowContent = fmt.Sprintf("%s  %s", connectedBadge, enabledBadge)
	}

	badges := lipgloss.PlaceHorizontal(contentWidth, lipgloss.Center, badgeRowContent)

	if len(m.servers) == 0 {
		emptyMsgText := lipgloss.NewStyle().
			Foreground(lipgloss.Color(ColorYellow)).
			Italic(true).
			Render(i18n.T("commands_b.mcp.no_servers"))

		emptyMsg := lipgloss.PlaceHorizontal(contentWidth, lipgloss.Center, emptyMsgText)

		hint := m.renderServerListHintBar(contentWidth)

		content := lipgloss.JoinVertical(lipgloss.Left, title, "", badges, "", emptyMsg, "", hint)
		container := lipgloss.NewStyle().
			Width(containerWidth).
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color(ColorDarkGray)).
			Padding(2, 3)
		return container.Render(content)
	}

	var items []string
	totalItems := len(m.servers)
	endIdx := m.scrollOffset + m.maxVisible
	if endIdx > totalItems {
		endIdx = totalItems
	}

	for i := m.scrollOffset; i < endIdx; i++ {
		server := m.servers[i]
		isSelected := i == m.selectedItem

		statusIcon := "○"
		statusColor := ColorRed
		statusText := i18n.T("commands_b.mcp.status_disabled")

		if server.Config.Enabled {
			if server.Connected {
				statusIcon = "●"
				statusColor = ColorGreen
				statusText = i18n.T("commands_b.mcp.status_connected")
			} else if server.Error != "" {
				statusIcon = "✗"
				statusColor = ColorOrange
				statusText = i18n.T("commands_b.mcp.status_error")
			} else {
				statusIcon = "◐"
				statusColor = ColorYellow
				statusText = i18n.T("commands_b.mcp.status_connecting")
			}
		}

		toolCount := i18n.T("commands_b.mcp.tool_count", len(server.Tools))

		// Get type badge
		typeLabel := "stdio"
		switch server.Config.GetType() {
		case MCPTypeSSE:
			typeLabel = "sse"
		case MCPTypeHTTP:
			typeLabel = "http"
		case MCPTypeOAuth:
			typeLabel = "oauth"
		}

		typeBadge := lipgloss.NewStyle().
			Background(lipgloss.Color(ColorPanel)).
			Foreground(lipgloss.Color(ColorWhite)).
			Padding(0, 1).
			Render(typeLabel)

		var itemText string
		if isSelected {
			// Get connection info based on type
			connInfo := server.Config.Command
			if server.Config.GetType() != MCPTypeStdio {
				connInfo = server.Config.URL
			}

			nameLine := lipgloss.NewStyle().
				Foreground(lipgloss.Color(ColorCyan)).
				Bold(true).
				Render(fmt.Sprintf("  ▶ %s  %s", server.Config.Name, typeBadge))

			statusLine := lipgloss.NewStyle().
				Foreground(lipgloss.Color(statusColor)).
				Render(fmt.Sprintf("      %s %s  •  %s  •  %s", statusIcon, statusText, truncate(connInfo, 35), toolCount))

			if server.Error != "" {
				errorLine := lipgloss.NewStyle().
					Foreground(lipgloss.Color(ColorRed)).
					Render(i18n.T("commands_b.mcp.error_line", truncate(server.Error, 55)))
				itemText = lipgloss.JoinVertical(lipgloss.Left, nameLine, statusLine, errorLine, "")
			} else {
				itemText = lipgloss.JoinVertical(lipgloss.Left, nameLine, statusLine, "")
			}
		} else {
			statusIconStyled := lipgloss.NewStyle().
				Foreground(lipgloss.Color(statusColor)).
				Render(statusIcon)

			itemText = lipgloss.NewStyle().
				Foreground(lipgloss.Color(ColorWhite)).
				Render(fmt.Sprintf("    %s %s  %s  (%s)", statusIconStyled, server.Config.Name, typeBadge, toolCount))
		}

		items = append(items, itemText)
	}

	list := lipgloss.JoinVertical(lipgloss.Left, items...)

	hint := m.renderServerListHintBar(contentWidth)

	content := lipgloss.JoinVertical(lipgloss.Left, title, "", badges, "", list, "", hint)

	container := lipgloss.NewStyle().
		Width(containerWidth).
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color(ColorDarkGray)).
		Padding(2, 3)

	return container.Render(content)
}

// renderServerListHintBar renders the styled hint bar for the server list
func (m *MCPCommand) renderServerListHintBar(width int) string {
	keyStyle := lipgloss.NewStyle().
		Background(lipgloss.Color(ColorPanel)).
		Foreground(lipgloss.Color(ColorWhite)).
		Padding(0, 1).
		Bold(true)

	hints := i18n.T("commands_b.mcp.hint_servers",
		keyStyle.Render("↑/↓"),
		keyStyle.Render("Enter"),
		keyStyle.Render("Space"),
		keyStyle.Render("⌫"))

	return lipgloss.PlaceHorizontal(width, lipgloss.Center, hints)
}

func (m *MCPCommand) renderAddServer() string {
	containerWidth := 85
	if m.width > 0 && m.width < containerWidth+4 {
		containerWidth = m.width - 4
	}
	contentWidth := containerWidth - 6

	// Centered title (use PlaceHorizontal to avoid background extension)
	titleText := lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color(ColorCyan)).
		Render(i18n.T("commands_b.mcp.add_title"))

	title := lipgloss.PlaceHorizontal(contentWidth, lipgloss.Center, titleText)

	// Type badge showing current selection
	var typeBadgeColor string
	var typeDesc string
	switch m.formType {
	case MCPTypeStdio:
		typeBadgeColor = ColorCyan
		typeDesc = i18n.T("commands_b.mcp.type_stdio_desc")
	case MCPTypeSSE:
		typeBadgeColor = ColorSuccess
		typeDesc = i18n.T("commands_b.mcp.type_sse_desc")
	case MCPTypeHTTP:
		typeBadgeColor = ColorPurple
		typeDesc = i18n.T("commands_b.mcp.type_http_desc")
	case MCPTypeOAuth:
		typeBadgeColor = ColorOrange
		typeDesc = i18n.T("commands_b.mcp.type_oauth_desc")
	}

	typeBadge := lipgloss.NewStyle().
		Background(lipgloss.Color(typeBadgeColor)).
		Foreground(lipgloss.Color(ColorWhite)).
		Padding(0, 1).
		Bold(true).
		Render(string(m.formType))

	descText := lipgloss.NewStyle().
		Foreground(lipgloss.Color(ColorGray)).
		Render(typeDesc)

	badgeRow := fmt.Sprintf("%s  %s", typeBadge, descText)
	badges := lipgloss.PlaceHorizontal(contentWidth, lipgloss.Center, badgeRow)

	var formItems []string

	// Name field (always shown)
	formItems = append(formItems, m.renderFormField(i18n.T("commands_b.mcp.field_name"), m.formName, FormFieldName, true))

	// Type selector
	formItems = append(formItems, m.renderTypeSelector())

	// Type-dependent fields
	switch m.formType {
	case MCPTypeStdio:
		// stdio: Command, Args, WorkDir
		formItems = append(formItems, m.renderFormField(i18n.T("commands_b.mcp.field_command"), m.formCommand, FormFieldCommand, true))
		formItems = append(formItems, m.renderFormField(i18n.T("commands_b.mcp.field_arguments"), m.formArgs, FormFieldArgs, false))
		formItems = append(formItems, m.renderFormField(i18n.T("commands_b.mcp.field_working_directory"), m.formWorkDir, FormFieldWorkDir, false))
	case MCPTypeOAuth:
		// OAuth: URL, ClientID, Scopes
		formItems = append(formItems, m.renderFormField(i18n.T("commands_b.mcp.field_server_url"), m.formURL, FormFieldURL, true))
		formItems = append(formItems, m.renderFormField(i18n.T("commands_b.mcp.field_client_id"), m.formClientID, FormFieldClientID, true))
		formItems = append(formItems, m.renderFormField(i18n.T("commands_b.mcp.field_scopes"), m.formScopes, FormFieldScopes, false))
	default:
		// SSE/HTTP: URL, Headers
		formItems = append(formItems, m.renderFormField(i18n.T("commands_b.mcp.field_url"), m.formURL, FormFieldURL, true))
		formItems = append(formItems, m.renderFormField(i18n.T("commands_b.mcp.field_headers"), m.formHeaders, FormFieldHeaders, false))
	}

	// Common optional fields
	formItems = append(formItems, m.renderFormField(i18n.T("commands_b.mcp.field_environment"), m.formEnv, FormFieldEnv, false))
	formItems = append(formItems, m.renderFormField(i18n.T("commands_b.mcp.field_timeout"), m.formTimeout, FormFieldTimeout, false))

	form := lipgloss.JoinVertical(lipgloss.Left, formItems...)

	// Styled hint bar
	hint := m.renderAddServerHintBar(contentWidth)

	content := lipgloss.JoinVertical(lipgloss.Left, title, "", badges, "", form, "", hint)

	container := lipgloss.NewStyle().
		Width(containerWidth).
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color(ColorDarkGray)).
		Padding(2, 3)

	return container.Render(content)
}

// renderAddServerHintBar renders the styled hint bar for the add server form
func (m *MCPCommand) renderAddServerHintBar(width int) string {
	keyStyle := lipgloss.NewStyle().
		Background(lipgloss.Color(ColorPanel)).
		Foreground(lipgloss.Color(ColorWhite)).
		Padding(0, 1).
		Bold(true)

	var hints string
	if m.formField == FormFieldType {
		hints = i18n.T("commands_b.mcp.hint_type",
			keyStyle.Render("←/→"),
			keyStyle.Render("Tab"),
			keyStyle.Render("Esc"))
	} else {
		hints = i18n.T("commands_b.mcp.hint_form",
			keyStyle.Render("Tab"),
			keyStyle.Render("Enter"),
			keyStyle.Render("⌫"),
			keyStyle.Render("Esc"))
	}

	return lipgloss.PlaceHorizontal(width, lipgloss.Center, hints)
}

// renderFormField renders a single form field
func (m *MCPCommand) renderFormField(label, value string, fieldID int, required bool) string {
	isSelected := m.formField == fieldID

	labelStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(ColorWhite))
	if required {
		labelStyle = labelStyle.Bold(true)
	}

	labelText := labelStyle.Render(label + ":")

	var valueDisplay string
	if isSelected {
		cursor := "▌"
		valueDisplay = lipgloss.NewStyle().
			Foreground(lipgloss.Color(ColorCyan)).
			Render(value + cursor)
	} else {
		if value == "" {
			valueDisplay = lipgloss.NewStyle().
				Foreground(lipgloss.Color(ColorGray)).
				Italic(true).
				Render(i18n.T("commands_b.mcp.empty"))
		} else {
			valueDisplay = lipgloss.NewStyle().
				Foreground(lipgloss.Color(ColorWhite)).
				Render(value)
		}
	}

	return lipgloss.JoinVertical(lipgloss.Left, labelText, "  "+valueDisplay, "")
}

// renderTypeSelector renders the type selection field
func (m *MCPCommand) renderTypeSelector() string {
	isSelected := m.formField == FormFieldType

	label := lipgloss.NewStyle().
		Foreground(lipgloss.Color(ColorWhite)).
		Bold(true).
		Render(i18n.T("commands_b.mcp.field_type") + ":")

	types := []struct {
		value MCPServerType
		label string
		desc  string
	}{
		{MCPTypeStdio, "stdio", "subprocess"},
		{MCPTypeSSE, "sse", "SSE endpoint"},
		{MCPTypeHTTP, "http", "HTTP endpoint"},
		{MCPTypeOAuth, "oauth", "OAuth 2.0"},
	}

	var typeOptions []string
	for _, t := range types {
		isCurrentType := m.formType == t.value

		var optionText string
		if isCurrentType {
			if isSelected {
				// Selected and focused - highlight with arrows
				optionText = lipgloss.NewStyle().
					Foreground(lipgloss.Color(ColorCyan)).
					Bold(true).
					Render(fmt.Sprintf("◀ %s ▶", t.label))
			} else {
				// Selected but not focused
				optionText = lipgloss.NewStyle().
					Foreground(lipgloss.Color(ColorGreen)).
					Bold(true).
					Render(fmt.Sprintf("[%s]", t.label))
			}
		} else {
			// Not selected
			optionText = lipgloss.NewStyle().
				Foreground(lipgloss.Color(ColorGray)).
				Render(t.label)
		}
		typeOptions = append(typeOptions, optionText)
	}

	optionsLine := "  " + strings.Join(typeOptions, "  ")

	return lipgloss.JoinVertical(lipgloss.Left, label, optionsLine, "")
}

func (m *MCPCommand) renderServerConfig() string {
	if m.currentServer == nil {
		return i18n.T("commands_b.mcp.no_server_selected")
	}

	title := lipgloss.NewStyle().
		Foreground(lipgloss.Color(ColorWhite)).
		Bold(true).
		Render(i18n.T("commands_b.mcp.server_tools_title", m.currentServer.Config.Name))

	statusIcon := "○"
	statusColor := ColorRed
	statusText := i18n.T("commands_b.mcp.status_disabled")

	if m.currentServer.Config.Enabled {
		if m.currentServer.Connected {
			statusIcon = "●"
			statusColor = ColorGreen
			statusText = i18n.T("commands_b.mcp.status_connected")
		} else if m.currentServer.Error != "" {
			statusIcon = "⚠"
			statusColor = ColorOrange
			statusText = i18n.T("commands_b.mcp.status_error_detail", truncate(m.currentServer.Error, 40))
		} else {
			statusIcon = "○"
			statusColor = ColorYellow
			statusText = i18n.T("commands_b.mcp.status_connecting_ellipsis")
		}
	}

	status := lipgloss.NewStyle().
		Foreground(lipgloss.Color(statusColor)).
		Render(fmt.Sprintf("%s %s", statusIcon, statusText))

	subtitle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(ColorGray)).
		Italic(true).
		MarginBottom(1).
		Render(i18n.T("commands_b.mcp.tools_available", len(m.currentServer.Tools)))

	if len(m.currentServer.Tools) == 0 {
		emptyMsg := lipgloss.NewStyle().
			Foreground(lipgloss.Color(ColorYellow)).
			Italic(true).
			Align(lipgloss.Center).
			Render(i18n.T("commands_b.mcp.no_tools"))

		hint := lipgloss.NewStyle().
			Foreground(lipgloss.Color(ColorGray)).
			MarginTop(1).
			Render(i18n.T("commands_b.mcp.hint_back_cancel"))

		content := lipgloss.JoinVertical(lipgloss.Left, title, status, "", subtitle, "", emptyMsg, "", hint)
		container := lipgloss.NewStyle().
			Width(80).
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color(ColorDarkGray)).
			Padding(2, 3)
		return container.Render(content)
	}

	var items []string
	totalItems := len(m.currentServer.Tools)
	endIdx := m.scrollOffset + m.maxVisible
	if endIdx > totalItems {
		endIdx = totalItems
	}

	for i := m.scrollOffset; i < endIdx; i++ {
		tool := m.currentServer.Tools[i]
		isSelected := i == m.selectedItem

		// Check if tool is enabled
		enabled := true
		if m.currentServer.Config.DisabledTools != nil {
			if _, disabled := m.currentServer.Config.DisabledTools[tool.Name]; disabled {
				enabled = false
			}
		}

		checkbox := "☐"
		checkColor := ColorGray
		if enabled {
			checkbox = "☑"
			checkColor = ColorGreen
		}

		var itemText string
		if isSelected {
			nameLine := lipgloss.NewStyle().
				Foreground(lipgloss.Color(ColorCyan)).
				Bold(true).
				Render(fmt.Sprintf("▶ %s %s", checkbox, tool.Name))

			descLine := lipgloss.NewStyle().
				Foreground(lipgloss.Color(ColorGray)).
				Render(fmt.Sprintf("  %s", truncate(tool.Description, 70)))

			itemText = lipgloss.JoinVertical(lipgloss.Left, nameLine, descLine, "")
		} else {
			checkboxStyled := lipgloss.NewStyle().
				Foreground(lipgloss.Color(checkColor)).
				Render(checkbox)

			itemText = lipgloss.NewStyle().
				Foreground(lipgloss.Color(ColorWhite)).
				Render(fmt.Sprintf("  %s %s", checkboxStyled, tool.Name))
		}

		items = append(items, itemText)
	}

	list := lipgloss.JoinVertical(lipgloss.Left, items...)

	hint := lipgloss.NewStyle().
		Foreground(lipgloss.Color(ColorGray)).
		MarginTop(1).
		Render(i18n.T("commands_b.mcp.hint_tools"))

	content := lipgloss.JoinVertical(lipgloss.Left, title, status, "", subtitle, "", list, "", hint)

	containerWidth := 85
	if m.width > 0 && m.width < containerWidth+4 {
		containerWidth = m.width - 4
	}

	container := lipgloss.NewStyle().
		Width(containerWidth).
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color(ColorDarkGray)).
		Padding(2, 3)

	return container.Render(content)
}

func (m *MCPCommand) renderToolDetail() string {
	if m.currentTool == nil {
		return i18n.T("commands_b.mcp.no_tool_selected")
	}

	title := lipgloss.NewStyle().
		Foreground(lipgloss.Color(ColorWhite)).
		Bold(true).
		Render(m.currentTool.Name)

	desc := lipgloss.NewStyle().
		Foreground(lipgloss.Color(ColorGray)).
		Italic(true).
		Render(m.currentTool.Description)

	schemaTitle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(ColorCyan)).
		Bold(true).
		MarginTop(1).
		Render(i18n.T("commands_b.mcp.input_schema"))

	// Pretty print schema
	schemaJSON, _ := prettyJSON(m.currentTool.InputSchema)
	schema := lipgloss.NewStyle().
		Foreground(lipgloss.Color(ColorWhite)).
		Render(schemaJSON)

	hint := lipgloss.NewStyle().
		Foreground(lipgloss.Color(ColorGray)).
		MarginTop(1).
		Render(i18n.T("commands_b.mcp.hint_return_cancel"))

	content := lipgloss.JoinVertical(lipgloss.Left, title, "", desc, "", schemaTitle, schema, "", hint)

	containerWidth := 85
	if m.width > 0 && m.width < containerWidth+4 {
		containerWidth = m.width - 4
	}

	container := lipgloss.NewStyle().
		Width(containerWidth).
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color(ColorDarkGray)).
		Padding(2, 3)

	return container.Render(content)
}

func (m *MCPCommand) renderImportClaude() string {
	title := lipgloss.NewStyle().
		Foreground(lipgloss.Color(ColorWhite)).
		Bold(true).
		Render(i18n.T("commands_b.mcp.import_title"))

	subtitle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(ColorGray)).
		Italic(true).
		MarginBottom(1).
		Render(i18n.T("commands_b.mcp.import_subtitle"))

	var items []string

	if len(m.claudeServers) == 0 {
		emptyMsg := lipgloss.NewStyle().
			Foreground(lipgloss.Color(ColorYellow)).
			Render(i18n.T("commands_b.mcp.import_empty"))
		items = append(items, "", emptyMsg)
	} else {
		// Calculate visible range
		visibleStart := m.scrollOffset
		visibleEnd := m.scrollOffset + m.maxVisible
		if visibleEnd > len(m.claudeServers) {
			visibleEnd = len(m.claudeServers)
		}

		// Add scroll indicator at top if needed
		if m.scrollOffset > 0 {
			indicator := lipgloss.NewStyle().
				Foreground(lipgloss.Color(ColorGray)).
				Render(i18n.T("commands_b.mcp.more_above", m.scrollOffset))
			items = append(items, indicator, "")
		}

		// Render visible servers
		for i := visibleStart; i < visibleEnd; i++ {
			server := m.claudeServers[i]
			isSelected := i == m.selectedItem

			checkbox := "[ ]"
			if isSelected {
				checkbox = lipgloss.NewStyle().
					Foreground(lipgloss.Color(ColorCyan)).
					Render("[•]")
			}

			nameStyle := lipgloss.NewStyle()
			if isSelected {
				nameStyle = nameStyle.Foreground(lipgloss.Color(ColorCyan)).Bold(true)
			} else {
				nameStyle = nameStyle.Foreground(lipgloss.Color(ColorWhite))
			}

			// Show connection info based on type
			var connPreview string
			var typeLabel string
			switch server.GetType() {
			case MCPTypeSSE:
				connPreview = server.URL
				typeLabel = "sse"
			case MCPTypeHTTP:
				connPreview = server.URL
				typeLabel = "http"
			default:
				connPreview = server.Command
				if len(server.Args) > 0 {
					connPreview += " " + strings.Join(server.Args, " ")
				}
				typeLabel = "stdio"
			}
			if len(connPreview) > 55 {
				connPreview = connPreview[:52] + "..."
			}

			detailStyle := lipgloss.NewStyle().
				Foreground(lipgloss.Color(ColorGray))

			typeBadge := lipgloss.NewStyle().
				Foreground(lipgloss.Color(ColorGray)).
				Render(fmt.Sprintf("[%s]", typeLabel))

			line := fmt.Sprintf("%s %s %s", checkbox, nameStyle.Render(server.Name), typeBadge)
			detail := fmt.Sprintf("      %s", detailStyle.Render(connPreview))

			items = append(items, line, detail, "")
		}

		// Add scroll indicator at bottom if needed
		if visibleEnd < len(m.claudeServers) {
			indicator := lipgloss.NewStyle().
				Foreground(lipgloss.Color(ColorGray)).
				Render(i18n.T("commands_b.mcp.more_below", len(m.claudeServers)-visibleEnd))
			items = append(items, indicator)
		}
	}

	hint := lipgloss.NewStyle().
		Foreground(lipgloss.Color(ColorGray)).
		MarginTop(1).
		Render(i18n.T("commands_b.mcp.hint_import"))

	content := lipgloss.JoinVertical(lipgloss.Left, title, subtitle)
	if len(items) > 0 {
		itemsContent := lipgloss.JoinVertical(lipgloss.Left, items...)
		content = lipgloss.JoinVertical(lipgloss.Left, content, "", itemsContent)
	}
	content = lipgloss.JoinVertical(lipgloss.Left, content, "", hint)

	containerWidth := 85
	if m.width > 0 && m.width < containerWidth+4 {
		containerWidth = m.width - 4
	}

	container := lipgloss.NewStyle().
		Width(containerWidth).
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color(ColorDarkGray)).
		Padding(2, 3)

	return container.Render(content)
}

func (m *MCPCommand) renderAssistantInfo() string {
	containerWidth := 85
	if m.width > 0 && m.width < containerWidth+4 {
		containerWidth = m.width - 4
	}
	contentWidth := containerWidth - 6

	// Title
	title := lipgloss.NewStyle().
		Foreground(lipgloss.Color(ColorCyan)).
		Bold(true).
		Render(i18n.T("commands_b.mcp.assistant_title"))

	// Description
	description := lipgloss.NewStyle().
		Foreground(lipgloss.Color(ColorWhite)).
		MarginTop(1).
		Render(i18n.T("commands_b.mcp.assistant_description"))

	// Features list
	featuresTitle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(ColorWhite)).
		Bold(true).
		MarginTop(2).
		Render(i18n.T("commands_b.mcp.assistant_features_title"))

	features := []string{
		i18n.T("commands_b.mcp.assistant_feature_add"),
		i18n.T("commands_b.mcp.assistant_feature_toggle"),
		i18n.T("commands_b.mcp.assistant_feature_status"),
		i18n.T("commands_b.mcp.assistant_feature_test"),
		i18n.T("commands_b.mcp.assistant_feature_tools"),
		i18n.T("commands_b.mcp.assistant_feature_recommend"),
		i18n.T("commands_b.mcp.assistant_feature_troubleshoot"),
	}

	featuresContent := lipgloss.NewStyle().
		Foreground(lipgloss.Color(ColorGray)).
		MarginTop(1).
		Render(strings.Join(features, "\n"))

	// Usage instructions
	usageTitle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(ColorWhite)).
		Bold(true).
		MarginTop(2).
		Render(i18n.T("commands_b.mcp.assistant_access_title"))

	usage := lipgloss.NewStyle().
		Foreground(lipgloss.Color(ColorGray)).
		MarginTop(1).
		Render(i18n.T("commands_b.mcp.assistant_access"))

	// Examples
	examplesTitle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(ColorWhite)).
		Bold(true).
		MarginTop(2).
		Render(i18n.T("commands_b.mcp.assistant_examples_title"))

	examples := []string{
		i18n.T("commands_b.mcp.assistant_example_add"),
		i18n.T("commands_b.mcp.assistant_example_enable"),
		i18n.T("commands_b.mcp.assistant_example_show"),
		i18n.T("commands_b.mcp.assistant_example_test"),
		i18n.T("commands_b.mcp.assistant_example_diagnose"),
	}

	examplesContent := lipgloss.NewStyle().
		Foreground(lipgloss.Color(ColorGray)).
		MarginTop(1).
		Render(strings.Join(examples, "\n"))

	// Hint
	hint := lipgloss.NewStyle().
		Foreground(lipgloss.Color(ColorGray)).
		MarginTop(2).
		Render(i18n.T("commands_b.mcp.assistant_return_hint"))

	// Combine all sections
	content := lipgloss.JoinVertical(
		lipgloss.Left,
		lipgloss.PlaceHorizontal(contentWidth, lipgloss.Center, title),
		description,
		featuresTitle,
		featuresContent,
		usageTitle,
		usage,
		examplesTitle,
		examplesContent,
		hint,
	)

	container := lipgloss.NewStyle().
		Width(containerWidth).
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color(ColorDarkGray)).
		Padding(2, 3)

	return container.Render(content)
}
