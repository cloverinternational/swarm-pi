package settings

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/i18n"
)

// renderMinimal renders a minimal view for small terminals
func (m *MCPSettings) renderMinimal(width, height int, th Theme) string {
	msg := i18n.T("settings.residual_final.mcp.terminal_small")
	return lipgloss.NewStyle().
		Width(width).
		Height(height).
		Foreground(lipgloss.Color(th.TextMuted)).
		Align(lipgloss.Center, lipgloss.Center).
		Render(msg)
}

// renderTitle renders the section title with professional header
func (m *MCPSettings) renderTitle(width int, th Theme) string {
	// Main title with icon
	titleStyle := lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color(th.Primary)).
		Width(width).
		Align(lipgloss.Center)

	title := titleStyle.Render(i18n.T("settings.integrations.mcp.dashboard"))

	// Stats badges row
	enabledBadge := lipgloss.NewStyle().
		Background(lipgloss.Color(th.Success)).
		Foreground(lipgloss.Color(th.Text)).
		Padding(0, 1).
		Bold(true).
		Render(i18n.T("settings.residual_final.mcp.enabled_count", len(m.enabledTools)))

	disabledBadge := lipgloss.NewStyle().
		Background(lipgloss.Color(th.Error)).
		Foreground(lipgloss.Color(th.Text)).
		Padding(0, 1).
		Bold(true).
		Render(i18n.T("settings.residual_final.mcp.disabled_count", len(m.disabledTools)))

	// Count connected servers
	connectedServers := 0
	for _, s := range m.servers {
		if s.Connected {
			connectedServers++
		}
	}
	var serverBadge string
	if connectedServers == len(m.servers) && len(m.servers) > 0 {
		serverBadge = lipgloss.NewStyle().
			Background(lipgloss.Color(th.Success)).
			Foreground(lipgloss.Color(th.Text)).
			Padding(0, 1).
			Bold(true).
			Render(fmt.Sprintf("MCP: %d/%d", connectedServers, len(m.servers)))
	} else if len(m.servers) > 0 {
		serverBadge = lipgloss.NewStyle().
			Background(lipgloss.Color(th.Warning)).
			Foreground(lipgloss.Color(th.Text)).
			Padding(0, 1).
			Bold(true).
			Render(fmt.Sprintf("MCP: %d/%d", connectedServers, len(m.servers)))
	} else {
		serverBadge = lipgloss.NewStyle().
			Background(lipgloss.Color(th.TextMuted)).
			Foreground(lipgloss.Color(th.Text)).
			Padding(0, 1).
			Render(i18n.T("settings.residual_final.mcp.none"))
	}

	// Center the badges
	badgesRow := lipgloss.JoinHorizontal(lipgloss.Center, enabledBadge, " ", disabledBadge, " ", serverBadge)
	centeredBadges := lipgloss.NewStyle().Width(width).Align(lipgloss.Center).Render(badgesRow)

	return lipgloss.JoinVertical(lipgloss.Left, title, "", centeredBadges)
}

// renderActions renders the action buttons (top section) with professional styling
func (m *MCPSettings) renderActions(width, height int, state *State, th Theme) string {
	const (
		buttonGap    = 1
		minBtnWidth  = 14
		minBtnHeight = 3
	)

	// Calculate button layout (4 buttons in a row)
	numButtons := 4
	totalGaps := (numButtons - 1) * buttonGap
	availWidth := width - totalGaps
	buttonWidth := availWidth / numButtons

	if buttonWidth < minBtnWidth || width < 80 {
		// Fall back to vertical layout for narrow screens
		return m.renderActionsVertical(width, height, state, th)
	}

	// Calculate tool counts for button subtitles
	totalTools := len(m.allTools)
	for _, server := range m.servers {
		if server.Connected {
			totalTools += len(server.Tools)
		}
	}

	// Create buttons with selection state and icons
	isButtonsFocused := state.MCPFocusedPanel == "buttons"

	// Button 1: Configure Tools with count
	configLabel := fmt.Sprintf("%s%d)", i18n.T("settings.residual_models.mcp.configure"), totalTools)
	configTools := m.renderActionButton(configLabel, i18n.T("settings.residual_final.mcp.configure_description"), buttonWidth, minBtnHeight,
		isButtonsFocused && state.MCPSelectedButton == 0, th)

	// Button 2: MCP Configuration with server count
	mcpLabel := i18n.T("settings.residual_final.mcp.servers_count", len(m.servers))
	mcpConfig := m.renderActionButton(mcpLabel, i18n.T("settings.residual_final.mcp.servers_description"), buttonWidth, minBtnHeight,
		isButtonsFocused && state.MCPSelectedButton == 1, th)

	// Button 3: Tool Tester
	toolTester := m.renderActionButton(i18n.T("settings.residual_final.mcp.tool_tester"), i18n.T("settings.residual_final.mcp.tool_tester_description"), buttonWidth, minBtnHeight,
		isButtonsFocused && state.MCPSelectedButton == 2, th)

	// Button 4: MCP Assistant
	mcpAssistant := m.renderActionButton(i18n.T("settings.residual_final.mcp.ai_assistant"), i18n.T("settings.residual_final.mcp.ai_assistant_description"), buttonWidth, minBtnHeight,
		isButtonsFocused && state.MCPSelectedButton == 3, th)

	// Join horizontally
	buttonRow := lipgloss.JoinHorizontal(lipgloss.Top,
		configTools,
		strings.Repeat(" ", buttonGap),
		mcpConfig,
		strings.Repeat(" ", buttonGap),
		toolTester,
		strings.Repeat(" ", buttonGap),
		mcpAssistant,
	)

	banner := m.renderPermissionsBanner(width, th)
	content := lipgloss.JoinVertical(lipgloss.Left, banner, "", buttonRow)

	// Center in available height
	contentHeight := lipgloss.Height(content)
	topPadding := (height - contentHeight) / 2
	if topPadding < 0 {
		topPadding = 0
	}
	bottomPadding := height - contentHeight - topPadding
	if bottomPadding < 0 {
		bottomPadding = 0
	}

	return lipgloss.JoinVertical(lipgloss.Left,
		strings.Repeat("\n", max(0, topPadding)),
		content,
		strings.Repeat("\n", max(0, bottomPadding)),
	)
}

// renderPermissionsBanner shows a reminder that permissions moved to Security.
func (m *MCPSettings) renderPermissionsBanner(width int, th Theme) string {
	bannerStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(th.TextMuted)).
		Background(lipgloss.Color(th.BGLight)).
		Padding(0, 2).
		Width(maxInt(10, width-2))

	text := i18n.T("settings.residual_final.mcp.permissions_moved")
	if width > 4 && len(text) > width-4 {
		text = truncateString(text, width-4)
	}
	return bannerStyle.Render(text)
}

// renderActionsVertical renders buttons vertically for narrow terminals
func (m *MCPSettings) renderActionsVertical(width, height int, state *State, th Theme) string {
	isButtonsFocused := state.MCPFocusedPanel == "buttons"

	// Simple vertical button list
	var lines []string
	buttons := []struct {
		label    string
		selected bool
	}{
		{i18n.T("settings.residual_final.mcp.button.configure"), isButtonsFocused && state.MCPSelectedButton == 0},
		{i18n.T("settings.residual_final.mcp.button.servers"), isButtonsFocused && state.MCPSelectedButton == 1},
		{i18n.T("settings.residual_final.mcp.button.tester"), isButtonsFocused && state.MCPSelectedButton == 2},
		{i18n.T("settings.residual_final.mcp.button.assistant"), isButtonsFocused && state.MCPSelectedButton == 3},
	}

	for _, btn := range buttons {
		var style lipgloss.Style
		if btn.selected {
			style = lipgloss.NewStyle().
				Background(lipgloss.Color(th.Primary)).
				Foreground(lipgloss.Color(th.Text)).
				Bold(true).
				Width(maxInt(10, width-4)).
				Padding(0, 1)
		} else {
			style = lipgloss.NewStyle().
				Foreground(lipgloss.Color(th.Text)).
				Width(maxInt(10, width-4)).
				Padding(0, 1)
		}
		lines = append(lines, style.Render(btn.label))
	}

	banner := m.renderPermissionsBanner(width, th)
	return lipgloss.JoinVertical(lipgloss.Left, banner, "", lipgloss.JoinVertical(lipgloss.Left, lines...))
}

// renderActionButton renders a professional action button with title and subtitle
func (m *MCPSettings) renderActionButton(title, subtitle string, width, height int, selected bool, th Theme) string {
	bg := th.BGLight
	fg := th.Text
	subtitleFg := th.TextMuted
	borderColor := th.Border

	if selected {
		bg = th.Primary
		fg = th.Text
		subtitleFg = th.TextDim
		borderColor = th.Primary
	}

	// Title style - truncate if too long
	displayTitle := title
	if width > 7 && len(displayTitle) > width-4 {
		displayTitle = displayTitle[:width-7] + "..."
	}

	titleStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(fg)).
		Bold(true)

	// Subtitle style (smaller, muted) - truncate if too long
	displaySubtitle := subtitle
	if width > 7 && len(displaySubtitle) > width-4 {
		displaySubtitle = displaySubtitle[:width-7] + "..."
	}

	subtitleStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(subtitleFg)).
		Italic(true)

	// Combine title and subtitle
	content := lipgloss.JoinVertical(lipgloss.Center,
		titleStyle.Render(displayTitle),
		subtitleStyle.Render(displaySubtitle),
	)

	// Button container - minimal height
	style := lipgloss.NewStyle().
		Background(lipgloss.Color(bg)).
		BorderStyle(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color(borderColor)).
		Width(maxInt(10, width-2)).
		Align(lipgloss.Center, lipgloss.Center).
		Padding(0, 1)

	return style.Render(content)
}

// renderSeparator renders a subtle separator between sections
func (m *MCPSettings) renderSeparator(width int, th Theme) string {
	// Create a gradient-style separator with label
	sideLen := maxInt(1, (width-14)/2)
	leftLine := lipgloss.NewStyle().
		Foreground(lipgloss.Color(th.Border)).
		Render(strings.Repeat("─", sideLen))

	label := lipgloss.NewStyle().
		Foreground(lipgloss.Color(th.TextMuted)).
		Italic(true).
		Render(i18n.T("settings.residual_final.mcp.tool_status"))

	rightLine := lipgloss.NewStyle().
		Foreground(lipgloss.Color(th.Border)).
		Render(strings.Repeat("─", sideLen))

	separator := lipgloss.JoinHorizontal(lipgloss.Center, leftLine, label, rightLine)

	return lipgloss.JoinVertical(lipgloss.Left, "", separator, "")
}

// renderToolsSection renders the enabled/disabled tools section
func (m *MCPSettings) renderToolsSection(width, height int, state *State, th Theme) string {
	const (
		panelGap      = 3
		minPanelWidth = 25
	)

	// Calculate panel widths
	totalGap := panelGap
	availWidth := width - totalGap
	panelWidth := availWidth / 2

	if panelWidth < minPanelWidth {
		// Fall back to vertical layout
		return m.renderToolsVertical(width, height, state, th)
	}

	// Render panels with scrolling support
	enabledPanel := m.renderToolPanel(i18n.T("settings.residual_final.mcp.enabled_tools"), m.enabledTools, panelWidth, height, true, state, th)
	disabledPanel := m.renderToolPanel(i18n.T("settings.residual_final.mcp.disabled_tools"), m.disabledTools, panelWidth, height, false, state, th)

	// Join horizontally
	return lipgloss.JoinHorizontal(lipgloss.Top,
		enabledPanel,
		strings.Repeat(" ", panelGap),
		disabledPanel,
	)
}

// renderToolsVertical renders tool panels vertically for narrow terminals
func (m *MCPSettings) renderToolsVertical(width, height int, state *State, th Theme) string {
	halfHeight := height / 2
	enabledPanel := m.renderToolPanel(i18n.T("settings.residual_final.mcp.enabled_tools"), m.enabledTools, width, halfHeight, true, state, th)
	disabledPanel := m.renderToolPanel(i18n.T("settings.residual_final.mcp.disabled_tools"), m.disabledTools, width, halfHeight, false, state, th)

	return lipgloss.JoinVertical(lipgloss.Left, enabledPanel, "", disabledPanel)
}

// renderToolPanel renders a single tool list panel with scrolling and professional styling
func (m *MCPSettings) renderToolPanel(title string, tools []ToolInfo, width, height int, isEnabled bool, state *State, th Theme) string {
	const (
		borderWidth  = 2
		headerHeight = 4
	)

	// Determine if this panel is focused
	panelType := "enabled"
	if !isEnabled {
		panelType = "disabled"
	}
	isFocused := state.MCPFocusedPanel == panelType

	// Calculate content dimensions
	contentWidth := maxInt(12, width-borderWidth*2-2)
	contentHeight := maxInt(3, height-borderWidth-headerHeight)

	// Status icon for header
	var statusIcon string
	if isEnabled {
		statusIcon = "●"
	} else {
		statusIcon = "○"
	}

	// Render header with icon and count
	headerColor := th.Success
	if !isEnabled {
		headerColor = th.Error
	}
	if isFocused {
		headerColor = th.Primary
	}

	headerStyle := lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color(headerColor)).
		Width(contentWidth).
		Align(lipgloss.Center)

	iconStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(headerColor)).
		Bold(true)

	header := headerStyle.Render(fmt.Sprintf("%s %s (%d)", iconStyle.Render(statusIcon), title, len(tools)))

	// Count tools by source for subtitle
	iiCount, sdkCount, mcpCount := 0, 0, 0
	for _, t := range tools {
		switch t.Source {
		case "ii":
			iiCount++
		case "sdk":
			sdkCount++
		case "mcp":
			mcpCount++
		default:
			if t.IsBuiltin {
				sdkCount++
			} else {
				mcpCount++
			}
		}
	}

	// Source summary line
	var sourceParts []string
	if iiCount > 0 {
		sourceParts = append(sourceParts, fmt.Sprintf("II:%d", iiCount))
	}
	if sdkCount > 0 {
		sourceParts = append(sourceParts, fmt.Sprintf("SDK:%d", sdkCount))
	}
	if mcpCount > 0 {
		sourceParts = append(sourceParts, fmt.Sprintf("MCP:%d", mcpCount))
	}
	sourceInfo := ""
	if len(sourceParts) > 0 {
		sourceInfo = strings.Join(sourceParts, " • ")
	}
	sourceStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(th.TextMuted)).
		Width(contentWidth).
		Align(lipgloss.Center)

	// Render tools with scrolling
	var lines []string

	// Get scroll offset for this panel
	var scrollOffset int
	if isEnabled {
		scrollOffset = state.MCPScrollOffsetEnabled
	} else {
		scrollOffset = state.MCPScrollOffsetDisabled
	}

	if scrollOffset < 0 {
		scrollOffset = 0
	}
	if scrollOffset > len(tools)-contentHeight {
		scrollOffset = max(0, len(tools)-contentHeight)
	}

	// Render visible tools (subtract 1 for padding space applied later)
	lineWidth := contentWidth - 1
	if lineWidth < 12 {
		lineWidth = 12
	}
	visibleEnd := min(scrollOffset+contentHeight, len(tools))
	for i := scrollOffset; i < visibleEnd; i++ {
		tool := tools[i]
		lines = append(lines, m.renderToolLine(tool, lineWidth, isEnabled, th))
	}

	// Build header with source info and scroll indicator
	headerParts := []string{header}
	if sourceInfo != "" {
		headerParts = append(headerParts, sourceStyle.Render(sourceInfo))
	}
	if len(tools) > contentHeight {
		scrollInfo := fmt.Sprintf("↑ %d-%d of %d ↓", scrollOffset+1, visibleEnd, len(tools))
		infoStyle := lipgloss.NewStyle().
			Foreground(lipgloss.Color(th.TextMuted)).
			Italic(true).
			Width(contentWidth).
			Align(lipgloss.Center)
		headerParts = append(headerParts, infoStyle.Render(scrollInfo))
	}
	fullHeader := lipgloss.JoinVertical(lipgloss.Left, headerParts...)

	// Fill remaining space
	for len(lines) < contentHeight {
		lines = append(lines, strings.Repeat(" ", contentWidth))
	}

	content := strings.Join(lines, "\n")

	// Add manual padding (space prefix) instead of lipgloss Padding which can cause wrapping
	paddedLines := strings.Split(content, "\n")
	for i, line := range paddedLines {
		paddedLines[i] = " " + line
	}
	paddedContent := strings.Join(paddedLines, "\n")

	panel := lipgloss.JoinVertical(lipgloss.Left, fullHeader, paddedContent)

	// Apply border with rounded corners
	borderColor := th.Success
	if !isEnabled {
		borderColor = th.Error
	}
	if isFocused {
		borderColor = th.Primary
	}

	style := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color(borderColor)).
		Width(maxInt(20, width-2)).
		Height(maxInt(5, height-2))

	return style.Render(panel)
}

// renderToolLine renders a single tool in the list with source badges - guaranteed single line
func (m *MCPSettings) renderToolLine(tool ToolInfo, width int, isEnabled bool, th Theme) string {
	if width < 8 {
		width = 8
	}

	color := th.Text
	if !isEnabled {
		color = th.TextMuted
	}

	// Build source badge based on tool source (fixed width: 3 chars)
	var sourceBadge string
	var badgeColor string
	switch tool.Source {
	case "ii":
		badgeColor = th.Primary
		sourceBadge = "II "
	case "sdk":
		badgeColor = th.Warning
		sourceBadge = "SDK"
	case "mcp":
		badgeColor = th.Primary
		sourceBadge = "MCP"
	default:
		if tool.IsBuiltin {
			badgeColor = th.Warning
			sourceBadge = "SDK"
		} else {
			badgeColor = th.Primary
			sourceBadge = "MCP"
		}
	}

	// Available width for tool name: total - badge(3) - space(1)
	availableForName := width - 4
	if availableForName < 3 {
		availableForName = 3
	}

	// Truncate tool name to fit
	toolName := tool.Name
	if len(toolName) > availableForName {
		if availableForName > 3 {
			toolName = toolName[:availableForName-1] + "…"
		} else {
			toolName = toolName[:availableForName]
		}
	}

	// Build as single string with fixed positions
	// Pad tool name to fill width
	for len(toolName) < availableForName {
		toolName = toolName + " "
	}

	// Apply styles inline
	badgeStyled := lipgloss.NewStyle().
		Foreground(lipgloss.Color(badgeColor)).
		Bold(true).
		Render(sourceBadge)

	nameStyled := lipgloss.NewStyle().
		Foreground(lipgloss.Color(color)).
		Bold(isEnabled).
		Render(toolName)

	return badgeStyled + " " + nameStyled
}

// renderMCPAddServerForm renders the add server form
func (m *MCPSettings) renderMCPAddServerForm(width, height int, state *State, th Theme) string {
	containerWidth := maxInt(20, width-4)
	if containerWidth > 85 {
		containerWidth = 85
	}
	contentWidth := maxInt(14, containerWidth-6)

	// Title
	titleStyle := lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color(th.Primary))

	// Use different title based on mode
	titleText := ""
	if state.MCPServersEditMode {
		titleText = titleStyle.Render(i18n.T("settings.residual_final.mcp.edit_server"))
	} else {
		titleText = titleStyle.Render(i18n.T("settings.residual_final.mcp.add_server"))
	}
	title := lipgloss.NewStyle().Width(contentWidth).Align(lipgloss.Center).Render(titleText)

	// Type badge
	var typeBadgeColor string
	var typeDesc string
	switch state.AddFormType {
	case "stdio":
		typeBadgeColor = th.Primary
		typeDesc = i18n.T("settings.residual_final.mcp.type.stdio_description")
	case "sse":
		typeBadgeColor = th.Success
		typeDesc = i18n.T("settings.residual_final.mcp.type.sse_description")
	case "http":
		typeBadgeColor = th.Warning
		typeDesc = i18n.T("settings.residual_final.mcp.type.http_description")
	case "oauth":
		typeBadgeColor = th.Error
		typeDesc = i18n.T("settings.residual_final.mcp.type.oauth_description")
	default:
		typeBadgeColor = th.Primary
		typeDesc = i18n.T("settings.residual_final.mcp.type.select")
	}

	typeBadge := lipgloss.NewStyle().
		Background(lipgloss.Color(typeBadgeColor)).
		Foreground(lipgloss.Color(th.Text)).
		Padding(0, 1).
		Bold(true).
		Render(state.AddFormType)

	descText := lipgloss.NewStyle().
		Foreground(lipgloss.Color(th.TextMuted)).
		Render(typeDesc)

	badgeRow := fmt.Sprintf("%s  %s", typeBadge, descText)
	badges := lipgloss.NewStyle().Width(contentWidth).Align(lipgloss.Center).Render(badgeRow)

	// Form fields
	var formItems []string

	// Name field (always shown)
	formItems = append(formItems, m.renderAddFormField(i18n.T("settings.residual_final.common.name"), state.AddFormName, 0, true, state, th))

	// Type selector
	formItems = append(formItems, m.renderAddTypeSelector(state, th))

	// Type-dependent fields
	switch state.AddFormType {
	case "stdio":
		formItems = append(formItems, m.renderAddFormField(i18n.T("settings.residual_final.mcp.command"), state.AddFormCommand, 2, true, state, th))
		formItems = append(formItems, m.renderAddFormField(i18n.T("settings.residual_final.mcp.arguments"), state.AddFormArgs, 3, false, state, th))
		formItems = append(formItems, m.renderAddFormField(i18n.T("settings.residual_final.mcp.working_directory"), state.AddFormWorkDir, 10, false, state, th))
	case "oauth":
		formItems = append(formItems, m.renderAddFormField(i18n.T("settings.residual_models.mcp.url"), state.AddFormURL, 4, true, state, th))
		formItems = append(formItems, m.renderAddFormField(i18n.T("settings.residual_final.mcp.oauth_client_id"), state.AddFormClientID, 7, true, state, th))
		formItems = append(formItems, m.renderAddFormField(i18n.T("settings.residual_final.mcp.scopes"), state.AddFormScopes, 8, false, state, th))
	default: // sse, http
		formItems = append(formItems, m.renderAddFormField("URL", state.AddFormURL, 4, true, state, th))
		formItems = append(formItems, m.renderAddFormField(i18n.T("settings.residual_final.mcp.headers"), state.AddFormHeaders, 5, false, state, th))
	}

	// Common optional fields
	formItems = append(formItems, m.renderAddFormField(i18n.T("settings.residual_final.mcp.environment"), state.AddFormEnv, 9, false, state, th))
	formItems = append(formItems, m.renderAddFormField(i18n.T("settings.residual_final.mcp.timeout"), state.AddFormTimeout, 11, false, state, th))

	form := lipgloss.JoinVertical(lipgloss.Left, formItems...)

	// Hint bar
	hint := m.renderAddFormHintBar(contentWidth, state, th)

	content := lipgloss.JoinVertical(lipgloss.Left, title, "", badges, "", form, "", hint)

	container := lipgloss.NewStyle().
		Width(containerWidth).
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color(th.Border)).
		Padding(2, 3)

	return lipgloss.NewStyle().
		Width(width).
		Height(height).
		Align(lipgloss.Center, lipgloss.Center).
		Render(container.Render(content))
}

// renderAddFormField renders a single form field for the add server form
func (m *MCPSettings) renderAddFormField(label, value string, fieldID int, required bool, state *State, th Theme) string {
	isSelected := state.AddFormField == fieldID

	labelStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(th.Text))
	if required {
		labelStyle = labelStyle.Bold(true)
	}

	labelText := labelStyle.Render(label + ":")

	var valueDisplay string
	if isSelected {
		cursor := "▌"
		valueDisplay = lipgloss.NewStyle().
			Foreground(lipgloss.Color(th.Primary)).
			Render(value + cursor)
	} else {
		if value == "" {
			valueDisplay = lipgloss.NewStyle().
				Foreground(lipgloss.Color(th.TextMuted)).
				Italic(true).
				Render(i18n.T("settings.residual_final.common.empty_parenthesized"))
		} else {
			valueDisplay = lipgloss.NewStyle().
				Foreground(lipgloss.Color(th.Text)).
				Render(value)
		}
	}

	return lipgloss.JoinVertical(lipgloss.Left, labelText, "  "+valueDisplay, "")
}

// renderAddTypeSelector renders the type selection field
func (m *MCPSettings) renderAddTypeSelector(state *State, th Theme) string {
	isSelected := state.AddFormField == 1

	label := lipgloss.NewStyle().
		Foreground(lipgloss.Color(th.Text)).
		Bold(true).
		Render(i18n.T("settings.residual_final.mcp.type_label"))

	types := []struct {
		value string
		label string
		desc  string
	}{
		{"stdio", "stdio", i18n.T("settings.residual_final.mcp.subprocess")},
		{"sse", "sse", i18n.T("settings.residual_final.mcp.sse_endpoint")},
		{"http", "http", i18n.T("settings.residual_final.mcp.http_endpoint")},
		{"oauth", "oauth", "OAuth 2.0"},
	}

	var typeOptions []string
	for _, t := range types {
		isCurrentType := state.AddFormType == t.value

		var optionText string
		if isCurrentType {
			if isSelected {
				// Selected and focused - highlight with arrows
				optionText = lipgloss.NewStyle().
					Foreground(lipgloss.Color(th.Primary)).
					Bold(true).
					Render(fmt.Sprintf("→ %s ←", t.label))
			} else {
				// Current type but not focused
				optionText = lipgloss.NewStyle().
					Foreground(lipgloss.Color(th.Primary)).
					Bold(true).
					Render(t.label)
			}
		} else {
			// Not selected
			optionText = lipgloss.NewStyle().
				Foreground(lipgloss.Color(th.TextMuted)).
				Render(t.label)
		}
		typeOptions = append(typeOptions, optionText)
	}

	options := lipgloss.JoinHorizontal(lipgloss.Left, typeOptions[0], "  ", typeOptions[1], "  ", typeOptions[2], "  ", typeOptions[3])
	hint := ""
	if isSelected {
		hint = lipgloss.NewStyle().
			Foreground(lipgloss.Color(th.TextMuted)).
			Italic(true).
			Render(i18n.T("settings.residual_final.mcp.change_type_hint"))
	}

	result := lipgloss.JoinVertical(lipgloss.Left, label, "  "+options)
	if hint != "" {
		result = lipgloss.JoinVertical(lipgloss.Left, result, "  "+hint, "")
	} else {
		result += "\n"
	}
	return result
}

// renderAddFormHintBar renders the hint bar for the add server form
func (m *MCPSettings) renderAddFormHintBar(width int, state *State, th Theme) string {
	keyStyle := lipgloss.NewStyle().
		Background(lipgloss.Color(th.BGLight)).
		Foreground(lipgloss.Color(th.Text)).
		Padding(0, 1).
		Bold(true)

	var hints string
	if state.AddFormField == 1 { // Type field
		hints = i18n.T("settings.residual_models.mcp.hints.type",
			keyStyle.Render("←/→"),
			keyStyle.Render("Tab"),
			keyStyle.Render("Esc"))
	} else {
		hints = i18n.T("settings.residual_models.mcp.hints.form",
			keyStyle.Render("Tab"),
			keyStyle.Render("Enter"),
			keyStyle.Render("⌫"),
			keyStyle.Render("Esc"))
	}

	return lipgloss.NewStyle().Width(width).Align(lipgloss.Center).Render(hints)
}

// renderMCPChatScreen renders the MCP AI Assistant chat interface
func (m *MCPSettings) renderMCPChatScreen(width, height int, state *State, th Theme) string {
	var lines []string

	// Title bar
	titleStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(th.Text)).
		Background(lipgloss.Color(th.Primary)).
		Bold(true).
		Width(maxInt(10, width-4)).
		Padding(0, minInt(2, width/10))
	lines = append(lines, titleStyle.Render(i18n.T("settings.residual_final.mcp.assistant.title")))
	lines = append(lines, "")

	// Instructions
	instructPad := minInt(2, width/10)
	instructStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(th.TextMuted)).
		Italic(true).
		Padding(0, instructPad)
	if width >= 60 {
		lines = append(lines, instructStyle.Render(i18n.T("settings.residual_final.mcp.assistant.description")))
		lines = append(lines, instructStyle.Render(i18n.T("settings.residual_final.mcp.assistant.example")))
	} else {
		lines = append(lines, instructStyle.Render(i18n.T("settings.residual_final.mcp.assistant.description_compact")))
	}
	lines = append(lines, "")

	// Chat history area
	chatAreaHeight := maxInt(5, height-14)

	chatBgStyle := lipgloss.NewStyle().
		Background(lipgloss.Color(th.BG)).
		Width(maxInt(10, width-6)).
		Height(chatAreaHeight).
		Padding(1).
		Margin(0, minInt(2, width/10)).
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color(th.Border))

	var chatLines []string
	if len(m.chatMessages) == 0 {
		emptyStyle := lipgloss.NewStyle().
			Foreground(lipgloss.Color(th.TextMuted)).
			Italic(true)
		chatLines = append(chatLines, emptyStyle.Render(i18n.T("settings.residual_final.mcp.assistant.empty")))
	} else {
		// Styles for different elements
		userStyle := lipgloss.NewStyle().
			Foreground(lipgloss.Color(th.Primary)).
			Bold(true)
		assistantHeaderStyle := lipgloss.NewStyle().
			Foreground(lipgloss.Color(th.Success)).
			Bold(true)
		assistantTextStyle := lipgloss.NewStyle().
			Foreground(lipgloss.Color(th.Text))
		toolNameStyle := lipgloss.NewStyle().
			Foreground(lipgloss.Color("#C792EA")). // Purple for tool names
			Bold(true)
		toolSymbolStyle := lipgloss.NewStyle().
			Foreground(lipgloss.Color("#89DDFF")) // Cyan for symbols
		toolInputStyle := lipgloss.NewStyle().
			Foreground(lipgloss.Color("#82AAFF")) // Blue for input JSON
		toolResultStyle := lipgloss.NewStyle().
			Foreground(lipgloss.Color("#C3E88D")) // Green for results
		toolErrorStyle := lipgloss.NewStyle().
			Foreground(lipgloss.Color(th.Error))
		turnsStyle := lipgloss.NewStyle().
			Foreground(lipgloss.Color(th.TextMuted)).
			Italic(true)

		for _, msg := range m.chatMessages {
			switch msg.Role {
			case "user":
				chatLines = append(chatLines, userStyle.Render(i18n.T("settings.residual_final.mcp.assistant.you")))
				wrapped := wordWrapText(msg.Content, width-16)
				for line := range strings.SplitSeq(wrapped, "\n") {
					chatLines = append(chatLines, "  "+line)
				}
				chatLines = append(chatLines, "")

			case "assistant":
				// Show turn count if > 1 (indicates multi-turn conversation)
				if msg.Turns > 1 {
					chatLines = append(chatLines, assistantHeaderStyle.Render("◆ Assistant")+turnsStyle.Render(fmt.Sprintf(" (%d%s)", msg.Turns, i18n.T("settings.residual_models.mcp.turns"))))
				} else {
					chatLines = append(chatLines, assistantHeaderStyle.Render(i18n.T("settings.residual_final.mcp.assistant.assistant")))
				}

				// Show tool calls (if any)
				if len(msg.ToolCalls) > 0 {
					for _, tc := range msg.ToolCalls {
						// Tool call header
						chatLines = append(chatLines, "  "+toolSymbolStyle.Render("⎿ ")+toolNameStyle.Render(tc.Name))

						// Tool input (pretty-printed JSON, truncated if long)
						if tc.InputJSON != "" {
							inputLines := strings.Split(tc.InputJSON, "\n")
							maxInputLines := 6
							if len(inputLines) > maxInputLines {
								for i := 0; i < maxInputLines-1; i++ {
									line := inputLines[i]
									if width > 23 && len(line) > width-20 {
										line = line[:width-23] + "..."
									}
									chatLines = append(chatLines, "    "+toolInputStyle.Render(line))
								}
								chatLines = append(chatLines, "    "+toolInputStyle.Render("    ..."))
							} else {
								for _, line := range inputLines {
									if width > 23 && len(line) > width-20 {
										line = line[:width-23] + "..."
									}
									chatLines = append(chatLines, "    "+toolInputStyle.Render(line))
								}
							}
						}

						// Tool result
						if tc.Result != "" {
							resultStyle := toolResultStyle
							resultPrefix := "✓ "
							if !tc.Success {
								resultStyle = toolErrorStyle
								resultPrefix = "✗ "
							}
							resultLines := strings.Split(tc.Result, "\n")
							maxResultLines := 8
							if len(resultLines) > maxResultLines {
								for i := 0; i < maxResultLines-1; i++ {
									line := resultPrefix + resultLines[i]
									if width > 23 && len(line) > width-20 {
										line = line[:width-23] + "..."
									}
									chatLines = append(chatLines, "    "+resultStyle.Render(line))
								}
								chatLines = append(chatLines, "    "+resultStyle.Render("    ..."))
							} else {
								for _, line := range resultLines {
									displayLine := resultPrefix + line
									resultPrefix = "  " // Only show checkmark on first line
									if width > 23 && len(displayLine) > width-20 {
										displayLine = displayLine[:width-23] + "..."
									}
									chatLines = append(chatLines, "    "+resultStyle.Render(displayLine))
								}
							}
						}
						chatLines = append(chatLines, "")
					}
				}

				// Assistant's final text response
				if msg.Content != "" {
					wrapped := wordWrapText(msg.Content, width-16)
					for line := range strings.SplitSeq(wrapped, "\n") {
						chatLines = append(chatLines, "  "+assistantTextStyle.Render(line))
					}
					chatLines = append(chatLines, "")
				}
			}
		}
	}

	// Scroll handling
	totalChatLines := len(chatLines)
	visibleLines := chatAreaHeight - 2 // Account for padding
	scrollOffset := state.MCPChatScrollOffset
	if scrollOffset > maxInt(0, totalChatLines-visibleLines) {
		scrollOffset = maxInt(0, totalChatLines-visibleLines)
	}
	if scrollOffset < 0 {
		scrollOffset = 0
	}

	// Extract visible portion
	visibleChatLines := chatLines
	if totalChatLines > visibleLines {
		end := minInt(scrollOffset+visibleLines, totalChatLines)
		visibleChatLines = chatLines[scrollOffset:end]
	}

	chatContent := strings.Join(visibleChatLines, "\n")
	lines = append(lines, chatBgStyle.Render(chatContent))
	lines = append(lines, "")

	// Input area
	inputBoxStyle := lipgloss.NewStyle().
		Background(lipgloss.Color(th.BGLight)).
		Foreground(lipgloss.Color(th.Text)).
		Width(maxInt(10, width-8)).
		Padding(0, 1).
		Margin(0, minInt(2, width/10)).
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color(th.Border))

	inputPrompt := i18n.T("settings.residual_final.mcp.assistant.message")
	inputText := state.MCPChatInput
	if state.MCPChatWaiting {
		inputText = i18n.T("settings.residual_final.mcp.assistant.waiting")
	}
	inputLine := inputBoxStyle.Render(inputPrompt + " " + inputText + "▌")
	lines = append(lines, inputLine)
	lines = append(lines, "")

	// Hint bar
	hintStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(th.TextMuted)).
		Padding(0, 2)
	hints := i18n.T("settings.residual_final.mcp.assistant.hints")
	lines = append(lines, hintStyle.Render(hints))

	return strings.Join(lines, "\n")
}
