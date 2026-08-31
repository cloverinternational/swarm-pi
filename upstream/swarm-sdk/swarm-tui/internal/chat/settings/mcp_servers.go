package settings

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/chat/commands"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/i18n"
)

// renderConfigureToolsScreen renders the tool configuration screen with two-panel layout
func (m *MCPSettings) renderConfigureToolsScreen(width, height int, state *State, th Theme) string {
	// Choose view mode (categorized or flat)
	var listItems []ListItem
	var flatTools []ConfigurableToolItem

	if state.MCPConfigureToolsViewMode == "categorized" {
		listItems = m.buildCategorizedToolsList(state)
		if len(listItems) == 0 {
			return m.renderConfigureToolsEmpty(width, height, th)
		}
	} else {
		// Flat view - build list items from tools
		flatTools = m.buildConfigurableToolsList()
		if len(flatTools) == 0 {
			return m.renderConfigureToolsEmpty(width, height, th)
		}
		// Convert to list items for unified handling
		for i, tool := range flatTools {
			listItems = append(listItems, ListItem{
				IsCategory: false,
				Tool:       &tool,
				Index:      i,
			})
		}
	}

	// Ensure selection is valid
	if state.MCPConfigureToolsSelected < 0 {
		state.MCPConfigureToolsSelected = 0
	}
	if state.MCPConfigureToolsSelected >= len(listItems) {
		state.MCPConfigureToolsSelected = len(listItems) - 1
	}

	// Count enabled/disabled from list items (skip categories and servers)
	enabledCount := 0
	disabledCount := 0
	for _, item := range listItems {
		if !item.IsCategory && item.Tool != nil {
			if item.Tool.IsEnabled {
				enabledCount++
			} else {
				disabledCount++
			}
		}
	}

	contentHeight := maxInt(5, height-4)
	sepHeight := maxInt(1, height-6)

	// Two-pane vs single-pane based on width
	var combined string
	if width < 60 {
		// Single-pane: list only
		listPanel := m.renderConfigureToolsList(maxInt(20, width-4), contentHeight, state, listItems, enabledCount, disabledCount, th)
		combined = listPanel
	} else {
		// Two-panel layout: list (55%) + details (45%)
		listWidth := (width * 55) / 100
		detailsWidth := maxInt(15, width-listWidth-3)

		listPanel := m.renderConfigureToolsList(listWidth, contentHeight, state, listItems, enabledCount, disabledCount, th)
		detailsPanel := m.renderConfigureToolsDetails(detailsWidth, contentHeight, state, listItems, th)

		separator := lipgloss.NewStyle().
			Foreground(lipgloss.Color(th.Border)).
			Render(strings.Repeat("│\n", sepHeight))

		combined = lipgloss.JoinHorizontal(lipgloss.Top, listPanel, separator, detailsPanel)
	}

	// Hint bar with new shortcuts
	hintStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(th.TextMuted)).
		Italic(true).
		Width(maxInt(10, width-4)).
		Align(lipgloss.Center)

	var hint string
	if width < 65 {
		hint = hintStyle.Render(i18n.T("settings.residual.mcp.tools_hint_compact"))
	} else if state.MCPConfigureToolsViewMode == "by_server" || state.MCPConfigureToolsViewMode == "categorized" {
		hint = hintStyle.Render(i18n.T("settings.residual.mcp.tools_hint_grouped"))
	} else {
		hint = hintStyle.Render(i18n.T("settings.residual.mcp.tools_hint_flat"))
	}

	content := lipgloss.JoinVertical(lipgloss.Left, combined, "", hint)

	containerStyle := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color(th.Primary)).
		Width(maxInt(20, width-2)).
		Height(maxInt(5, height-2)).
		Padding(1, 1)

	return containerStyle.Render(content)
}

// renderConfigureToolsEmpty renders empty state
func (m *MCPSettings) renderConfigureToolsEmpty(width, height int, th Theme) string {
	iw := maxInt(10, width-4)
	titleStyle := lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color(th.Primary)).
		Width(iw).
		Align(lipgloss.Center)

	msgStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(th.TextMuted)).
		Width(iw).
		Align(lipgloss.Center)

	content := lipgloss.JoinVertical(lipgloss.Center,
		"",
		titleStyle.Render(i18n.T("settings.residual.mcp.configure_tools")),
		"",
		msgStyle.Render(i18n.T("settings.residual.mcp.no_tools")),
		msgStyle.Render(i18n.T("settings.residual.mcp.connect_for_tools")),
	)

	containerStyle := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color(th.Primary)).
		Width(maxInt(20, width-2)).
		Height(maxInt(5, height-2)).
		Padding(1, minInt(2, width/10))

	return containerStyle.Render(content)
}

// renderConfigureToolsList renders the left panel with tool list
// renderConfigureToolsList renders the left panel with tool list (now supports categories)
func (m *MCPSettings) renderConfigureToolsList(width, height int, state *State, items []ListItem, enabledCount, disabledCount int, th Theme) string {
	iw := maxInt(10, width-2)
	titleStyle := lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color(th.Primary)).
		Width(iw)

	// Stats line
	statsStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(th.TextMuted)).
		Width(iw)

	enabledStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(th.Success)).Bold(true)
	disabledStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(th.Error)).Bold(true)

	statsText := i18n.T("settings.residual.mcp.tool_stats",
		enabledStyle.Render(fmt.Sprintf("%d", enabledCount)),
		disabledStyle.Render(fmt.Sprintf("%d", disabledCount)))

	// Count total tools (not categories)
	totalTools := 0
	for _, item := range items {
		if !item.IsCategory {
			totalTools++
		}
	}

	title := titleStyle.Render(i18n.T("settings.residual.mcp.tools_count", enabledCount+disabledCount))

	// Calculate visible area
	visibleHeight := maxInt(3, height-6)

	// Adjust scroll
	if state.MCPConfigureToolsScrollOffset > state.MCPConfigureToolsSelected {
		state.MCPConfigureToolsScrollOffset = state.MCPConfigureToolsSelected
	}
	if state.MCPConfigureToolsSelected >= state.MCPConfigureToolsScrollOffset+visibleHeight {
		state.MCPConfigureToolsScrollOffset = state.MCPConfigureToolsSelected - visibleHeight + 1
	}

	var lines []string
	lines = append(lines, title, statsStyle.Render(statsText), "")

	startIdx := state.MCPConfigureToolsScrollOffset
	endIdx := startIdx + visibleHeight
	if endIdx > len(items) {
		endIdx = len(items)
	}

	for i := startIdx; i < endIdx; i++ {
		item := items[i]
		isSelected := i == state.MCPConfigureToolsSelected

		if item.IsCategory {
			// Render category header
			expandIcon := "▶"
			if item.Category.IsExpanded {
				expandIcon = "▼"
			}

			categoryText := fmt.Sprintf("%s %s (%d/%d)",
				expandIcon,
				item.Category.Name,
				item.Category.EnabledCount,
				item.Category.TotalCount,
			)

			var lineStyle lipgloss.Style
			if isSelected {
				lineStyle = lipgloss.NewStyle().
					Background(lipgloss.Color(th.Primary)).
					Foreground(lipgloss.Color(th.Text)).
					Bold(true).
					Width(iw)
			} else {
				lineStyle = lipgloss.NewStyle().
					Foreground(lipgloss.Color(th.Primary)).
					Bold(true).
					Width(iw)
			}

			lines = append(lines, lineStyle.Render(categoryText))
		} else if item.IsServer {
			// Render server header
			expandIcon := "▶"
			if item.Server.IsExpanded {
				expandIcon = "▼"
			}

			serverText := fmt.Sprintf("%s %s (%d/%d)",
				expandIcon,
				item.Server.Name,
				item.Server.EnabledCount,
				item.Server.TotalCount,
			)

			var lineStyle lipgloss.Style
			if isSelected {
				lineStyle = lipgloss.NewStyle().
					Background(lipgloss.Color(th.Primary)).
					Foreground(lipgloss.Color(th.Text)).
					Bold(true).
					Width(iw)
			} else {
				lineStyle = lipgloss.NewStyle().
					Foreground(lipgloss.Color(th.Primary)).
					Bold(true).
					Width(iw)
			}

			lines = append(lines, lineStyle.Render(serverText))
		} else {
			// Render tool item
			tool := item.Tool

			// Build status indicator with checkbox style
			var statusIcon string
			var statusColor string
			if tool.IsEnabled {
				statusIcon = "●" // Filled circle for enabled
				statusColor = th.Success
			} else {
				statusIcon = "○" // Empty circle for disabled
				statusColor = th.Error
			}

			// Build source badge
			var sourceBadge string
			switch tool.Tool.Source {
			case "ii":
				sourceBadge = "[II]"
			case "sdk":
				sourceBadge = "[SDK]"
			case "mcp":
				sourceBadge = "[MCP]"
			default:
				if tool.Tool.IsBuiltin {
					sourceBadge = "[SDK]"
				}
			}

			// Build line with indentation for categorized view
			indent := ""
			if state.MCPConfigureToolsViewMode == "categorized" {
				indent = "  " // Indent tools under categories
			}
			lineText := fmt.Sprintf("%s %s %s %s", indent, statusIcon, sourceBadge, tool.Tool.Name)

			var lineStyle lipgloss.Style
			if isSelected {
				// Selection color based on enabled/disabled status
				var selBg string
				if tool.IsEnabled {
					selBg = th.Success // Green for enabled
				} else {
					selBg = th.Error // Red for disabled
				}
				lineStyle = lipgloss.NewStyle().
					Background(lipgloss.Color(selBg)).
					Foreground(lipgloss.Color(th.Text)).
					Bold(true).
					Width(iw)
			} else {
				lineStyle = lipgloss.NewStyle().
					Foreground(lipgloss.Color(statusColor)).
					Width(iw)
			}

			lines = append(lines, lineStyle.Render(lineText))
		}
	}

	// Scroll indicator
	if len(items) > visibleHeight {
		scrollInfo := i18n.T("settings.residual.common.range_of_total", startIdx+1, endIdx, len(items))
		scrollStyle := lipgloss.NewStyle().
			Foreground(lipgloss.Color(th.TextMuted)).
			Width(iw).
			Align(lipgloss.Center)
		lines = append(lines, "", scrollStyle.Render(scrollInfo))
	}

	return lipgloss.JoinVertical(lipgloss.Left, lines...)
}

// renderConfigureToolsDetails renders the right panel with selected tool details
func (m *MCPSettings) renderConfigureToolsDetails(width, height int, state *State, items []ListItem, th Theme) string {
	if state.MCPConfigureToolsSelected >= len(items) || state.MCPConfigureToolsSelected < 0 {
		return ""
	}

	item := items[state.MCPConfigureToolsSelected]

	// If a category is selected, show category info
	if item.IsCategory {
		return m.renderCategoryDetails(width, height, item.Category, th)
	}

	// Otherwise show tool details
	tool := item.Tool
	dw := maxInt(10, width-2)

	titleStyle := lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color(th.Primary)).
		Width(dw)

	labelStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(th.TextMuted)).
		Width(dw)

	valueStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(th.Text)).
		Width(dw)

	var lines []string

	// Tool name
	lines = append(lines, titleStyle.Render(tool.Tool.Name))
	lines = append(lines, "")

	// Status badge - show ENABLED/DISABLED prominently
	var statusBadge string
	if tool.IsEnabled {
		badgeStyle := lipgloss.NewStyle().
			Background(lipgloss.Color(th.Success)).
			Foreground(lipgloss.Color(th.Text)).
			Padding(0, 1).
			Bold(true)
		statusBadge = badgeStyle.Render(i18n.T("settings.residual.common.enabled_badge"))
	} else {
		badgeStyle := lipgloss.NewStyle().
			Background(lipgloss.Color(th.Error)).
			Foreground(lipgloss.Color(th.Text)).
			Padding(0, 1).
			Bold(true)
		statusBadge = badgeStyle.Render(i18n.T("settings.residual.common.disabled_badge"))
	}

	// Source badge
	var sourceBadge string
	switch tool.Tool.Source {
	case "ii":
		badgeStyle := lipgloss.NewStyle().
			Background(lipgloss.Color(th.Primary)).
			Foreground(lipgloss.Color(th.Text)).
			Padding(0, 1).
			Bold(true)
		sourceBadge = badgeStyle.Render("II")
	case "sdk":
		badgeStyle := lipgloss.NewStyle().
			Background(lipgloss.Color(th.Warning)).
			Foreground(lipgloss.Color(th.Text)).
			Padding(0, 1).
			Bold(true)
		sourceBadge = badgeStyle.Render("SDK")
	case "mcp":
		badgeStyle := lipgloss.NewStyle().
			Background(lipgloss.Color(th.Primary)).
			Foreground(lipgloss.Color(th.Text)).
			Padding(0, 1).
			Bold(true)
		sourceBadge = badgeStyle.Render("MCP")
	default:
		if tool.Tool.IsBuiltin {
			badgeStyle := lipgloss.NewStyle().
				Background(lipgloss.Color(th.Warning)).
				Foreground(lipgloss.Color(th.Text)).
				Padding(0, 1).
				Bold(true)
			sourceBadge = badgeStyle.Render("SDK")
		}
	}

	// Show badges on same line
	lines = append(lines, lipgloss.JoinHorizontal(lipgloss.Left, statusBadge, " ", sourceBadge))
	lines = append(lines, "")

	// Source details
	lines = append(lines, labelStyle.Render(i18n.T("settings.residual.common.source_label")))
	switch tool.Tool.Source {
	case "ii":
		lines = append(lines, valueStyle.Render(i18n.T("settings.residual.mcp.source_ii")))
	case "sdk":
		lines = append(lines, valueStyle.Render(i18n.T("settings.residual.mcp.source_sdk_path")))
	case "mcp":
		lines = append(lines, valueStyle.Render(i18n.T("settings.residual.mcp.source_server", tool.ServerName)))
	default:
		if tool.Tool.IsBuiltin {
			lines = append(lines, valueStyle.Render(i18n.T("settings.residual.mcp.source_sdk")))
		} else {
			lines = append(lines, valueStyle.Render(i18n.T("settings.residual.mcp.source_mcp", tool.ServerName)))
		}
	}

	// Category
	if tool.Tool.Category != "" {
		lines = append(lines, "")
		lines = append(lines, labelStyle.Render(i18n.T("settings.residual.common.category_label")))
		lines = append(lines, valueStyle.Render("  "+tool.Tool.Category))
	}
	lines = append(lines, "")

	// Description - show more of it, dynamically based on available height
	if tool.Tool.Description != "" {
		lines = append(lines, labelStyle.Render(i18n.T("settings.residual.common.description_label")))
		desc := tool.Tool.Description

		// Calculate available lines for description
		usedLines := len(lines) + 4 // +4 for hint and spacing
		availableLines := height - usedLines
		if availableLines < 3 {
			availableLines = 3
		}

		// Wrap to available width
		wrapped := wrapTextForWidth(desc, width-6)

		// Show as many lines as we can fit
		descStyle := lipgloss.NewStyle().
			Foreground(lipgloss.Color(th.Text)).
			Width(dw)

		for i, line := range wrapped {
			if i >= availableLines {
				lines = append(lines, descStyle.Render("  ..."))
				break
			}
			lines = append(lines, descStyle.Render("  "+line))
		}
	}

	// Toggle hint - all tools can be toggled now
	hintStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(th.TextMuted)).
		Italic(true).
		Width(dw)
	if tool.IsEnabled {
		lines = append(lines, "", hintStyle.Render(i18n.T("settings.residual.mcp.disable_hint")))
	} else {
		lines = append(lines, "", hintStyle.Render(i18n.T("settings.residual.mcp.enable_hint")))
	}

	return lipgloss.JoinVertical(lipgloss.Left, lines...)
}

// wrapTextForWidth wraps text to specified width
func wrapTextForWidth(text string, width int) []string {
	if width <= 0 {
		return []string{text}
	}

	var lines []string
	words := strings.Fields(text)
	if len(words) == 0 {
		return lines
	}

	currentLine := words[0]
	for _, word := range words[1:] {
		if len(currentLine)+1+len(word) <= width {
			currentLine += " " + word
		} else {
			lines = append(lines, currentLine)
			currentLine = word
		}
	}
	if currentLine != "" {
		lines = append(lines, currentLine)
	}

	return lines
}

// renderCategoryDetails renders details for a selected category
func (m *MCPSettings) renderCategoryDetails(width, height int, category *CategoryItem, th Theme) string {
	cdw := maxInt(10, width-2)
	titleStyle := lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color(th.Primary)).
		Width(cdw)

	labelStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(th.TextMuted)).
		Width(cdw)

	valueStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(th.Text)).
		Width(cdw)

	var lines []string

	// Category name
	lines = append(lines, titleStyle.Render(category.Name))
	lines = append(lines, "")

	// Expansion status
	var statusBadge string
	if category.IsExpanded {
		badgeStyle := lipgloss.NewStyle().
			Background(lipgloss.Color(th.Success)).
			Foreground(lipgloss.Color(th.Text)).
			Padding(0, 1).
			Bold(true)
		statusBadge = badgeStyle.Render(i18n.T("settings.residual.mcp.expanded_badge"))
	} else {
		badgeStyle := lipgloss.NewStyle().
			Background(lipgloss.Color(th.Warning)).
			Foreground(lipgloss.Color(th.Text)).
			Padding(0, 1).
			Bold(true)
		statusBadge = badgeStyle.Render(i18n.T("settings.residual.mcp.collapsed_badge"))
	}
	lines = append(lines, statusBadge)
	lines = append(lines, "")

	// Statistics
	lines = append(lines, labelStyle.Render(i18n.T("settings.residual.mcp.statistics")))
	lines = append(lines, valueStyle.Render(i18n.T("settings.residual.mcp.total_tools", category.TotalCount)))

	enabledStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(th.Success))
	disabledStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(th.Error))

	lines = append(lines, valueStyle.Render(i18n.T("settings.residual.mcp.enabled_value", enabledStyle.Render(fmt.Sprintf("%d", category.EnabledCount)))))
	lines = append(lines, valueStyle.Render(i18n.T("settings.residual.mcp.disabled_value", disabledStyle.Render(fmt.Sprintf("%d", category.TotalCount-category.EnabledCount)))))
	lines = append(lines, "")

	// Hint
	hintStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(th.TextMuted)).
		Italic(true).
		Width(cdw)
	if category.IsExpanded {
		lines = append(lines, "", hintStyle.Render(i18n.T("settings.residual.mcp.collapse_hint")))
	} else {
		lines = append(lines, "", hintStyle.Render(i18n.T("settings.residual.mcp.expand_hint")))
	}

	return lipgloss.JoinVertical(lipgloss.Left, lines...)
}

// renderMCPServersScreen renders the MCP servers management screen
func (m *MCPSettings) renderMCPServersScreen(width, height int, state *State, th Theme) string {
	// Check if in add or edit server mode
	if state.MCPServersAddMode || state.MCPServersEditMode {
		return m.renderMCPAddServerForm(width, height, state, th)
	}

	var sections []string

	// Title with styled badges
	title := m.renderMCPServersTitle(width, th)
	sections = append(sections, title)
	sections = append(sections, "")

	// Calculate content area height
	contentHeight := maxInt(5, height-10) // Title, badges, hints, borders, padding

	// Render split panel (list + detail)
	panelContent := m.renderMCPServersListAndDetail(maxInt(20, width-6), contentHeight, state, th)
	sections = append(sections, panelContent)

	// Hint bar
	sections = append(sections, "")
	sections = append(sections, m.renderMCPServersHintBar(width, th))

	content := lipgloss.JoinVertical(lipgloss.Left, sections...)

	// Container with rounded border
	padH := minInt(2, width/10)
	containerStyle := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color(th.Border)).
		Width(maxInt(20, width-2)).
		Padding(1, padH)

	return containerStyle.Render(content)
}

// renderMCPServersTitle renders the title with status badges
func (m *MCPSettings) renderMCPServersTitle(width int, th Theme) string {
	titleStyle := lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color(th.Primary))

	// Count connected vs total
	connectedCount := 0
	enabledCount := 0
	for _, server := range m.servers {
		if server.Connected {
			connectedCount++
		}
		if server.Config.Enabled {
			enabledCount++
		}
	}

	// Styled count badges
	var connBadgeColor string
	if connectedCount == len(m.servers) && len(m.servers) > 0 {
		connBadgeColor = th.Success
	} else if connectedCount > 0 {
		connBadgeColor = th.Warning
	} else {
		connBadgeColor = th.Error
	}

	connectedBadge := lipgloss.NewStyle().
		Foreground(lipgloss.Color(th.Text)).
		Background(lipgloss.Color(connBadgeColor)).
		Padding(0, 1).
		Render(i18n.T("settings.residual.mcp.connected_count", connectedCount, len(m.servers)))

	enabledBadge := lipgloss.NewStyle().
		Foreground(lipgloss.Color(th.Text)).
		Background(lipgloss.Color(th.Primary)).
		Padding(0, 1).
		Render(i18n.T("settings.residual.mcp.enabled_plain_count", enabledCount))

	title := titleStyle.Render(i18n.T("settings.residual.mcp.servers_title"))
	badges := connectedBadge + "  " + enabledBadge

	// Center the title and badges
	tw := maxInt(10, width-4)
	titleLine := lipgloss.NewStyle().
		Width(tw).
		Align(lipgloss.Center).
		Render(title)

	badgeLine := lipgloss.NewStyle().
		Width(tw).
		Align(lipgloss.Center).
		Render(badges)

	return lipgloss.JoinVertical(lipgloss.Left, titleLine, badgeLine)
}

// renderMCPServersListAndDetail renders the split panel layout
func (m *MCPSettings) renderMCPServersListAndDetail(width, height int, state *State, th Theme) string {
	listHeight := maxInt(3, height-2)

	// Collapse to single-pane at narrow widths
	if width < 60 {
		// Single-pane: list only
		listWidth := maxInt(15, width-2)

		selectedIdx := 0
		if state != nil {
			selectedIdx = state.MCPServersSelected
		}
		if selectedIdx >= len(m.servers) {
			selectedIdx = len(m.servers) - 1
		}
		if selectedIdx < 0 {
			selectedIdx = 0
		}

		var listLines []string
		listHeaderStyle := lipgloss.NewStyle().
			Foreground(lipgloss.Color(th.Primary)).
			Bold(true)
		listLines = append(listLines, listHeaderStyle.Render(i18n.T("settings.residual.mcp.servers")))
		listLines = append(listLines, strings.Repeat("─", maxInt(1, listWidth-2)))

		if len(m.servers) == 0 {
			emptyStyle := lipgloss.NewStyle().
				Foreground(lipgloss.Color(th.TextMuted)).
				Italic(true)
			listLines = append(listLines, emptyStyle.Render(i18n.T("settings.residual.mcp.no_servers")))
			listLines = append(listLines, emptyStyle.Render(i18n.T("settings.residual.mcp.press_add")))
		} else {
			scrollOffset := 0
			if state != nil {
				scrollOffset = state.MCPServersScrollOffset
			}
			visibleEnd := minInt(scrollOffset+listHeight-3, len(m.servers))
			for i := scrollOffset; i < visibleEnd; i++ {
				server := m.servers[i]
				isSelected := i == selectedIdx
				listLines = append(listLines, m.renderMCPServerLine(server, isSelected, listWidth-2, th))
			}
		}

		for len(listLines) < listHeight {
			listLines = append(listLines, "")
		}

		return lipgloss.NewStyle().
			Width(listWidth).
			Height(listHeight).
			Render(lipgloss.JoinVertical(lipgloss.Left, listLines...))
	}

	// Calculate panel widths (40/60 split)
	listWidth := (width - 3) * 40 / 100
	detailWidth := maxInt(15, (width-3)-listWidth-1)

	// Get selected index
	selectedIdx := 0
	if state != nil {
		selectedIdx = state.MCPServersSelected
	}

	// Clamp selection
	if selectedIdx >= len(m.servers) {
		selectedIdx = len(m.servers) - 1
	}
	if selectedIdx < 0 {
		selectedIdx = 0
	}

	// Render servers list
	var listLines []string
	listHeaderStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(th.Primary)).
		Bold(true)
	listLines = append(listLines, listHeaderStyle.Render(i18n.T("settings.residual.mcp.servers")))
	listLines = append(listLines, strings.Repeat("─", maxInt(1, listWidth-2)))

	if len(m.servers) == 0 {
		emptyStyle := lipgloss.NewStyle().
			Foreground(lipgloss.Color(th.TextMuted)).
			Italic(true)
		listLines = append(listLines, "")
		listLines = append(listLines, emptyStyle.Render(i18n.T("settings.residual.mcp.no_servers")))
		listLines = append(listLines, "")
		listLines = append(listLines, emptyStyle.Render(i18n.T("settings.residual.mcp.press_add")))
	} else {
		scrollOffset := 0
		if state != nil {
			scrollOffset = state.MCPServersScrollOffset
		}
		visibleEnd := minInt(scrollOffset+listHeight-3, len(m.servers))
		for i := scrollOffset; i < visibleEnd; i++ {
			server := m.servers[i]
			isSelected := i == selectedIdx
			listLines = append(listLines, m.renderMCPServerLine(server, isSelected, maxInt(8, listWidth-2), th))
		}
	}

	// Pad list
	for len(listLines) < listHeight {
		listLines = append(listLines, "")
	}

	listPanel := lipgloss.NewStyle().
		Width(listWidth).
		Height(listHeight).
		Render(lipgloss.JoinVertical(lipgloss.Left, listLines...))

	// Render detail panel
	detailPanel := m.renderMCPServerDetails(selectedIdx, detailWidth, listHeight, th)

	// Join panels horizontally
	separator := lipgloss.NewStyle().
		Foreground(lipgloss.Color(th.Border)).
		Render(strings.Repeat("│\n", maxInt(1, listHeight)))

	return lipgloss.JoinHorizontal(lipgloss.Top, listPanel, " ", separator, " ", detailPanel)
}

// renderMCPServerLine renders a single server entry with status indicators
func (m *MCPSettings) renderMCPServerLine(server *commands.MCPServerState, isSelected bool, width int, th Theme) string {
	var parts []string

	// Status icon (like Crush)
	var statusIcon string
	var statusColor string
	if !server.Config.Enabled {
		statusIcon = "○" // Disabled
		statusColor = th.TextMuted
	} else if server.Error != "" {
		statusIcon = "✗" // Error
		statusColor = th.Error
	} else if server.Connected {
		statusIcon = "●" // Connected
		statusColor = th.Success
	} else {
		statusIcon = "◐" // Connecting/offline
		statusColor = th.Warning
	}

	iconStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(statusColor)).
		Bold(true)
	parts = append(parts, iconStyle.Render(statusIcon))

	// Server name
	nameColor := th.Text
	if isSelected {
		nameColor = th.Primary
	}
	if !server.Config.Enabled {
		nameColor = th.TextMuted
	}
	nameStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(nameColor)).
		Bold(isSelected)
	parts = append(parts, nameStyle.Render(server.Config.Name))

	// Tool count if connected
	if server.Connected && len(server.Tools) > 0 {
		countStyle := lipgloss.NewStyle().
			Foreground(lipgloss.Color(th.TextMuted))
		parts = append(parts, countStyle.Render(fmt.Sprintf("(%d)", len(server.Tools))))
	}

	line := strings.Join(parts, " ")

	// Truncate if too long
	if lipgloss.Width(line) > width {
		line = truncateString(line, width-3) + "..."
	}

	// Highlight selected line
	if isSelected {
		return lipgloss.NewStyle().
			Background(lipgloss.Color(th.BGLighter)).
			Width(width).
			Render(line)
	}

	return line
}

// renderMCPServerDetails renders the detail panel for the selected server
func (m *MCPSettings) renderMCPServerDetails(selectedIdx, width, height int, th Theme) string {
	var lines []string

	headerStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(th.Primary)).
		Bold(true)
	lines = append(lines, headerStyle.Render(i18n.T("settings.residual.common.details")))
	lines = append(lines, strings.Repeat("─", maxInt(1, width-2)))

	if len(m.servers) == 0 || selectedIdx < 0 || selectedIdx >= len(m.servers) {
		emptyStyle := lipgloss.NewStyle().
			Foreground(lipgloss.Color(th.TextMuted)).
			Italic(true)
		lines = append(lines, "")
		lines = append(lines, emptyStyle.Render(i18n.T("settings.residual.mcp.select_server")))
		lines = append(lines, emptyStyle.Render(i18n.T("settings.residual.mcp.view_details")))
		for len(lines) < height {
			lines = append(lines, "")
		}
		return lipgloss.NewStyle().
			Width(width).
			Height(height).
			Render(lipgloss.JoinVertical(lipgloss.Left, lines...))
	}

	server := m.servers[selectedIdx]

	// Server name
	nameStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(th.Primary)).
		Bold(true)
	lines = append(lines, nameStyle.Render(server.Config.Name))

	// Status badge
	var statusBadge string
	if !server.Config.Enabled {
		statusBadge = lipgloss.NewStyle().
			Foreground(lipgloss.Color(th.Text)).
			Background(lipgloss.Color(th.TextMuted)).
			Padding(0, 1).
			Render(i18n.T("settings.residual.common.disabled_upper"))
	} else if server.Error != "" {
		statusBadge = lipgloss.NewStyle().
			Foreground(lipgloss.Color(th.Text)).
			Background(lipgloss.Color(th.Error)).
			Padding(0, 1).
			Render(i18n.T("settings.residual.common.error_upper"))
	} else if server.Connected {
		statusBadge = lipgloss.NewStyle().
			Foreground(lipgloss.Color(th.Text)).
			Background(lipgloss.Color(th.Success)).
			Padding(0, 1).
			Render(i18n.T("settings.residual.common.connected_upper"))
	} else {
		statusBadge = lipgloss.NewStyle().
			Foreground(lipgloss.Color(th.Text)).
			Background(lipgloss.Color(th.Warning)).
			Padding(0, 1).
			Render(i18n.T("settings.residual.common.offline_upper"))
	}
	lines = append(lines, statusBadge)
	lines = append(lines, "")

	// Metadata
	labelStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(th.Text))
	valueStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(th.TextMuted))

	lines = append(lines, labelStyle.Render(i18n.T("settings.residual.common.command_label")))
	cmd := server.Config.Command
	if len(server.Config.Args) > 0 {
		cmd += " " + strings.Join(server.Config.Args, " ")
	}
	if width > 7 && len(cmd) > width-4 {
		cmd = cmd[:width-7] + "..."
	}
	lines = append(lines, valueStyle.Render("  "+cmd))
	lines = append(lines, "")

	// Tool count
	if server.Connected {
		lines = append(lines, labelStyle.Render(i18n.T("settings.residual.mcp.tools_label")))
		lines = append(lines, valueStyle.Render(i18n.T("settings.residual.mcp.available_count", len(server.Tools))))

		// Show first few tools
		maxTools := 5
		if len(server.Tools) > 0 {
			for i, tool := range server.Tools {
				if i >= maxTools {
					lines = append(lines, valueStyle.Render(i18n.T("settings.residual.common.and_more", len(server.Tools)-maxTools)))
					break
				}
				toolName := tool.Name
				if width > 9 && len(toolName) > width-6 {
					toolName = toolName[:width-9] + "..."
				}
				lines = append(lines, valueStyle.Render("  • "+toolName))
			}
		}
		lines = append(lines, "")
	}

	// Error message if any
	if server.Error != "" {
		errStyle := lipgloss.NewStyle().
			Foreground(lipgloss.Color(th.Error))
		lines = append(lines, errStyle.Render(i18n.T("settings.residual.common.error_label")))

		// Show truncated error
		errMsg := server.Error
		if width > 7 && len(errMsg) > width-4 {
			errMsg = errMsg[:width-7] + "..."
		}
		lines = append(lines, errStyle.Render("  "+errMsg))

		// Show error timestamp and attempt count if available
		if !server.ErrorTimestamp.IsZero() {
			timeStr := server.ErrorTimestamp.Format("15:04:05")
			lines = append(lines, valueStyle.Render(i18n.T("settings.residual.mcp.at_attempt", timeStr, server.ConnectionAttempts)))
		}

		// Show hint to view full details
		hintStyle := lipgloss.NewStyle().
			Foreground(lipgloss.Color(th.TextMuted)).
			Italic(true)
		lines = append(lines, "")
		lines = append(lines, hintStyle.Render(i18n.T("settings.residual.mcp.full_error_hint")))
		lines = append(lines, "")
	}

	// Environment variables if set
	if len(server.Config.Env) > 0 {
		lines = append(lines, labelStyle.Render(i18n.T("settings.residual.mcp.environment_label")))
		envCount := 0
		for k := range server.Config.Env {
			if envCount >= 3 {
				lines = append(lines, valueStyle.Render(i18n.T("settings.residual.common.and_more", len(server.Config.Env)-3)))
				break
			}
			lines = append(lines, valueStyle.Render("  "+k+"=***"))
			envCount++
		}
	}

	// Pad to height
	for len(lines) < height {
		lines = append(lines, "")
	}

	return lipgloss.NewStyle().
		Width(width).
		Height(height).
		Render(lipgloss.JoinVertical(lipgloss.Left, lines...))
}

// renderMCPServersHintBar renders the styled keyboard hints bar
func (m *MCPSettings) renderMCPServersHintBar(width int, th Theme) string {
	keyStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(th.Text)).
		Background(lipgloss.Color(th.BGLighter)).
		Padding(0, 1)

	actionStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(th.TextMuted)).
		MarginRight(2)

	hints := []string{
		keyStyle.Render("Space") + actionStyle.Render(i18n.T("settings.residual_final.mcp.action.toggle")),
		keyStyle.Render("a") + actionStyle.Render(i18n.T("settings.residual_final.mcp.action.add")),
		keyStyle.Render("e") + actionStyle.Render(i18n.T("settings.residual_final.mcp.action.edit")),
		keyStyle.Render("d") + actionStyle.Render(i18n.T("settings.residual_final.mcp.action.delete")),
		keyStyle.Render("r") + actionStyle.Render(i18n.T("settings.residual_final.mcp.action.reconnect")),
		keyStyle.Render("Esc") + actionStyle.Render(i18n.T("settings.residual_final.mcp.action.back")),
	}

	hintLine := strings.Join(hints, "  ")
	return lipgloss.NewStyle().
		Width(maxInt(10, width-4)).
		Align(lipgloss.Center).
		Render(hintLine)
}

// renderErrorDetailScreen renders a detailed error view with full stack trace and retry option
func (m *MCPSettings) renderErrorDetailScreen(width, height int, state *State, th Theme) string {
	if state.MCPCurrentServer == nil {
		// No server selected, return to main
		state.MCPState = "mcp_config"
		return ""
	}

	server := state.MCPCurrentServer
	containerWidth := maxInt(20, width-4)
	contentWidth := maxInt(12, containerWidth-4)

	var sections []string

	// Title
	titleStyle := lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color(th.Error)).
		Width(contentWidth).
		Align(lipgloss.Center)
	sections = append(sections, titleStyle.Render(i18n.T("settings.residual.mcp.error_details_title")))
	sections = append(sections, "")

	// Server name badge
	nameBadge := lipgloss.NewStyle().
		Background(lipgloss.Color(th.Primary)).
		Foreground(lipgloss.Color(th.Text)).
		Padding(0, 1).
		Bold(true).
		Render(server.Config.Name)
	sections = append(sections, lipgloss.NewStyle().Width(contentWidth).Align(lipgloss.Center).Render(nameBadge))
	sections = append(sections, "")

	// Error metadata
	labelStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(th.Text)).
		Bold(true)
	valueStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(th.TextMuted))

	// Connection attempts
	sections = append(sections, labelStyle.Render(i18n.T("settings.residual.mcp.connection_attempts")))
	sections = append(sections, valueStyle.Render(fmt.Sprintf("  %d", server.ConnectionAttempts)))
	sections = append(sections, "")

	// Last attempt time
	if !server.LastAttempt.IsZero() {
		sections = append(sections, labelStyle.Render(i18n.T("settings.residual.mcp.last_attempt")))
		sections = append(sections, valueStyle.Render(fmt.Sprintf("  %s", server.LastAttempt.Format("2006-01-02 15:04:05"))))
		sections = append(sections, "")
	}

	// Error timestamp
	if !server.ErrorTimestamp.IsZero() {
		sections = append(sections, labelStyle.Render(i18n.T("settings.residual.mcp.error_occurred")))
		sections = append(sections, valueStyle.Render(fmt.Sprintf("  %s", server.ErrorTimestamp.Format("2006-01-02 15:04:05"))))
		sections = append(sections, "")
	}

	// Configuration
	sections = append(sections, labelStyle.Render(i18n.T("settings.residual.mcp.configuration")))
	sections = append(sections, valueStyle.Render(i18n.T("settings.residual.mcp.type_value", server.Config.GetType())))
	if server.Config.Command != "" {
		sections = append(sections, valueStyle.Render(i18n.T("settings.residual.mcp.command_value", server.Config.Command)))
		if len(server.Config.Args) > 0 {
			sections = append(sections, valueStyle.Render(i18n.T("settings.residual.mcp.args_value", strings.Join(server.Config.Args, " "))))
		}
	}
	if server.Config.URL != "" {
		sections = append(sections, valueStyle.Render(i18n.T("settings.residual.mcp.url_value", server.Config.URL)))
	}
	if server.Config.WorkingDir != "" {
		sections = append(sections, valueStyle.Render(i18n.T("settings.residual.mcp.working_dir_value", server.Config.WorkingDir)))
	}
	sections = append(sections, "")

	// Full error message
	errorStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(th.Error)).
		Bold(true)
	sections = append(sections, errorStyle.Render(i18n.T("settings.residual.mcp.error_message")))
	sections = append(sections, "")

	// Word-wrap error message
	errorMsg := server.Error
	if server.LastError != nil {
		errorMsg = fmt.Sprintf("%+v", server.LastError) // Use %+v to get stack trace if available
	}

	errorLines := wordWrapLines(errorMsg, contentWidth-2)
	errorTextStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(th.Error))

	// Calculate scrollable area for error
	maxErrorLines := height - len(sections) - 8 // Reserve space for hints and padding
	if maxErrorLines < 5 {
		maxErrorLines = 5
	}

	// Apply scroll offset
	scrollOffset := state.MCPErrorDetailScroll
	if scrollOffset > len(errorLines)-maxErrorLines && len(errorLines) > maxErrorLines {
		scrollOffset = len(errorLines) - maxErrorLines
	}
	if scrollOffset < 0 {
		scrollOffset = 0
	}
	state.MCPErrorDetailScroll = scrollOffset

	// Show visible portion of error
	endIdx := scrollOffset + maxErrorLines
	if endIdx > len(errorLines) {
		endIdx = len(errorLines)
	}

	for i := scrollOffset; i < endIdx; i++ {
		sections = append(sections, errorTextStyle.Render("  "+errorLines[i]))
	}

	// Show scroll indicator if needed
	if len(errorLines) > maxErrorLines {
		scrollInfo := i18n.T("settings.residual.mcp.showing_lines", scrollOffset+1, endIdx, len(errorLines))
		sections = append(sections, lipgloss.NewStyle().
			Foreground(lipgloss.Color(th.TextMuted)).
			Italic(true).
			Render(scrollInfo))
	}

	sections = append(sections, "")

	// Troubleshooting hints
	hintStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(th.TextMuted)).
		Italic(true)
	sections = append(sections, labelStyle.Render(i18n.T("settings.residual.mcp.troubleshooting")))
	sections = append(sections, hintStyle.Render(i18n.T("settings.residual.mcp.troubleshoot.executable")))
	sections = append(sections, hintStyle.Render(i18n.T("settings.residual.mcp.troubleshoot.path")))
	sections = append(sections, hintStyle.Render(i18n.T("settings.residual.mcp.troubleshoot.arguments")))
	sections = append(sections, hintStyle.Render(i18n.T("settings.residual.mcp.troubleshoot.installed")))
	sections = append(sections, hintStyle.Render(i18n.T("settings.residual.mcp.troubleshoot.environment")))
	sections = append(sections, "")

	// Hint bar
	keyStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(th.Text)).
		Background(lipgloss.Color(th.BGLighter)).
		Padding(0, 1)
	actionStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(th.TextMuted)).
		MarginRight(2)

	hints := []string{
		keyStyle.Render("r") + actionStyle.Render(i18n.T("settings.residual_final.mcp.action.retry")),
		keyStyle.Render("↑/↓") + actionStyle.Render(i18n.T("settings.residual_final.mcp.action.scroll")),
		keyStyle.Render("e") + actionStyle.Render(i18n.T("settings.residual_final.mcp.action.edit_config")),
		keyStyle.Render("Esc") + actionStyle.Render(i18n.T("settings.residual_final.mcp.action.back")),
	}
	hintLine := strings.Join(hints, "  ")
	sections = append(sections, lipgloss.NewStyle().
		Width(contentWidth).
		Align(lipgloss.Center).
		Render(hintLine))

	content := lipgloss.JoinVertical(lipgloss.Left, sections...)

	// Container with border
	containerStyle := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color(th.Error)).
		Width(containerWidth).
		Padding(1, 2)

	return containerStyle.Render(content)
}

// wordWrapLines wraps text to fit within the specified width and returns lines
func wordWrapLines(text string, width int) []string {
	if width <= 0 {
		return []string{text}
	}

	var lines []string
	currentLine := ""

	words := strings.FieldsSeq(text)
	for word := range words {
		if len(currentLine)+len(word)+1 <= width {
			if currentLine != "" {
				currentLine += " "
			}
			currentLine += word
		} else {
			if currentLine != "" {
				lines = append(lines, currentLine)
			}
			// Handle words longer than width
			if len(word) > width {
				for len(word) > width {
					lines = append(lines, word[:width])
					word = word[width:]
				}
				currentLine = word
			} else {
				currentLine = word
			}
		}
	}
	if currentLine != "" {
		lines = append(lines, currentLine)
	}

	return lines
}

// HandleMCPServersKey handles keyboard input for the MCP servers screen
func (m *MCPSettings) HandleMCPServersKey(key string, state *State) bool {
	// Handle add/edit form mode separately
	if state.MCPServersAddMode || state.MCPServersEditMode {
		return m.handleAddFormKey(key, state)
	}

	if len(m.servers) == 0 {
		// Only handle 'a' to add when empty
		if key == "a" || key == "A" {
			state.MCPServersAddMode = true
			// Reset form fields
			state.AddFormField = 0
			state.AddFormName = ""
			state.AddFormType = "stdio"
			state.AddFormCommand = ""
			state.AddFormArgs = ""
			state.AddFormURL = ""
			state.AddFormHeaders = ""
			state.AddFormClientID = ""
			state.AddFormScopes = ""
			state.AddFormEnv = ""
			state.AddFormWorkDir = ""
			state.AddFormTimeout = ""
			return true
		}
		return false
	}

	// Clamp selection
	if state.MCPServersSelected >= len(m.servers) {
		state.MCPServersSelected = len(m.servers) - 1
	}
	if state.MCPServersSelected < 0 {
		state.MCPServersSelected = 0
	}

	switch key {
	case "up", "k":
		if state.MCPServersSelected > 0 {
			state.MCPServersSelected--
			// Adjust scroll
			if state.MCPServersSelected < state.MCPServersScrollOffset {
				state.MCPServersScrollOffset = state.MCPServersSelected
			}
		}
		return true

	case "down", "j":
		if state.MCPServersSelected < len(m.servers)-1 {
			state.MCPServersSelected++
			// Adjust scroll (assuming ~10 visible items)
			if state.MCPServersSelected >= state.MCPServersScrollOffset+10 {
				state.MCPServersScrollOffset = state.MCPServersSelected - 9
			}
		}
		return true

	case "space", "enter":
		// Toggle enable/disable
		if state.MCPServersSelected >= 0 && state.MCPServersSelected < len(m.servers) {
			server := m.servers[state.MCPServersSelected]
			newEnabled := !server.Config.Enabled

			// Call persistence callback
			if m.onServerToggle != nil {
				if err := m.onServerToggle(server.Config.Name, newEnabled); err != nil {
					logDebug("Failed to toggle MCP server: %v", err)
					return true
				}
			}

			// Update in-memory state
			server.Config.Enabled = newEnabled
		}
		return true

	case "a", "A":
		// Add new server (would trigger form mode)
		state.MCPServersAddMode = true
		// Reset form fields
		state.AddFormField = 0
		state.AddFormName = ""
		state.AddFormType = "stdio"
		state.AddFormCommand = ""
		state.AddFormArgs = ""
		state.AddFormURL = ""
		state.AddFormHeaders = ""
		state.AddFormClientID = ""
		state.AddFormScopes = ""
		state.AddFormEnv = ""
		state.AddFormWorkDir = ""
		state.AddFormTimeout = ""
		return true

	case "e", "E":
		// Edit selected server
		if state.MCPServersSelected >= 0 && state.MCPServersSelected < len(m.servers) {
			server := m.servers[state.MCPServersSelected]
			state.MCPServersEditMode = true
			state.AddFormField = 0

			// Pre-populate form fields with server config
			state.AddFormName = server.Config.Name
			state.AddFormType = string(server.Config.GetType())
			state.AddFormCommand = server.Config.Command

			// Convert Args array to space-separated string
			state.AddFormArgs = strings.Join(server.Config.Args, " ")

			state.AddFormURL = server.Config.URL

			// Convert Headers map to KEY=VALUE,KEY2=VALUE2 format
			if len(server.Config.Headers) > 0 {
				var headerPairs []string
				for k, v := range server.Config.Headers {
					headerPairs = append(headerPairs, fmt.Sprintf("%s=%s", k, v))
				}
				state.AddFormHeaders = strings.Join(headerPairs, ",")
			} else {
				state.AddFormHeaders = ""
			}

			// Populate OAuth fields if available
			if server.Config.OAuth != nil {
				state.AddFormClientID = server.Config.OAuth.ClientID
				state.AddFormScopes = server.Config.OAuth.Scopes
			} else {
				state.AddFormClientID = ""
				state.AddFormScopes = ""
			}

			// Convert Env map to KEY=VALUE,KEY2=VALUE2 format
			if len(server.Config.Env) > 0 {
				var envPairs []string
				for k, v := range server.Config.Env {
					envPairs = append(envPairs, fmt.Sprintf("%s=%s", k, v))
				}
				state.AddFormEnv = strings.Join(envPairs, ",")
			} else {
				state.AddFormEnv = ""
			}

			state.AddFormWorkDir = server.Config.WorkingDir

			// Convert timeout to string
			if server.Config.Timeout > 0 {
				state.AddFormTimeout = fmt.Sprintf("%d", server.Config.Timeout)
			} else {
				state.AddFormTimeout = ""
			}
		}
		return true

	case "d", "D":
		// Delete server (would need confirmation)
		// For now just mark as TODO
		return true

	case "r", "R":
		// Reconnect selected server
		if state.MCPServersSelected >= 0 && state.MCPServersSelected < len(m.servers) {
			server := m.servers[state.MCPServersSelected]
			// Trigger reconnection via callback
			if m.onServerReconnect != nil {
				m.onServerReconnect(server.Config.Name)
			}
		}
		return true

	case "v", "V":
		// View full error details
		if state.MCPServersSelected >= 0 && state.MCPServersSelected < len(m.servers) {
			server := m.servers[state.MCPServersSelected]
			if server.Error != "" {
				// Enter error detail state
				state.MCPState = "error_detail"
				state.MCPCurrentServer = server
				state.MCPErrorDetailScroll = 0
			}
		}
		return true

	case "pageup", "ctrl+u":
		state.MCPServersScrollOffset = maxInt(0, state.MCPServersScrollOffset-5)
		if state.MCPServersSelected > state.MCPServersScrollOffset+9 {
			state.MCPServersSelected = state.MCPServersScrollOffset + 9
		}
		return true

	case "pagedown", "ctrl+d":
		maxOffset := maxInt(0, len(m.servers)-10)
		state.MCPServersScrollOffset = minInt(state.MCPServersScrollOffset+5, maxOffset)
		if state.MCPServersSelected < state.MCPServersScrollOffset {
			state.MCPServersSelected = state.MCPServersScrollOffset
		}
		return true
	}

	return false
}

// handleAddFormKey handles keyboard input when in add/edit server form mode
func (m *MCPSettings) handleAddFormKey(key string, state *State) bool {
	switch key {
	case "esc":
		// Cancel form and return to list
		state.MCPServersAddMode = false
		state.MCPServersEditMode = false
		return true

	case "tab":
		// Move to next field
		state.AddFormField = m.nextAddFormField(state)
		return true

	case "shift+tab":
		// Move to previous field
		state.AddFormField = m.prevAddFormField(state)
		return true

	case "enter":
		// Submit form if valid
		return m.submitAddForm(state)

	case "backspace":
		// Delete character from current field
		m.handleAddFormBackspace(state)
		return true

	case "left":
		// Change type if on type field
		if state.AddFormField == 1 {
			m.cycleAddFormType(state, -1)
		}
		return true

	case "right":
		// Change type if on type field
		if state.AddFormField == 1 {
			m.cycleAddFormType(state, 1)
		}
		return true

	case "h":
		// Use as left-arrow alias only on the type selector field;
		// otherwise fall through to text input so 'h' can be typed.
		if state.AddFormField == 1 {
			m.cycleAddFormType(state, -1)
			return true
		}
		// Fall through to default text input
		m.handleAddFormInput(key, state)
		return true

	case "l":
		// Use as right-arrow alias only on the type selector field;
		// otherwise fall through to text input so 'l' can be typed.
		if state.AddFormField == 1 {
			m.cycleAddFormType(state, 1)
			return true
		}
		// Fall through to default text input
		m.handleAddFormInput(key, state)
		return true

	case "space":
		// Allow space in all fields
		m.handleAddFormInput(" ", state)
		return true
	default:
		// Handle text input
		if len(key) == 1 {
			m.handleAddFormInput(key, state)
		}
		return true
	}
}

// handleAddFormInput handles single character input for the add form
func (m *MCPSettings) handleAddFormInput(key string, state *State) {
	// Skip if on type selector field
	if state.AddFormField == 1 {
		return
	}

	switch state.AddFormField {
	case 0: // Name
		state.AddFormName += key
	case 2: // Command
		state.AddFormCommand += key
	case 3: // Args
		state.AddFormArgs += key
	case 4: // URL
		state.AddFormURL += key
	case 5: // Headers
		state.AddFormHeaders += key
	case 7: // ClientID
		state.AddFormClientID += key
	case 8: // Scopes
		state.AddFormScopes += key
	case 9: // Env
		state.AddFormEnv += key
	case 10: // WorkDir
		state.AddFormWorkDir += key
	case 11: // Timeout
		// Only allow digits for timeout
		if key >= "0" && key <= "9" {
			state.AddFormTimeout += key
		}
	}
}

// handleAddFormBackspace handles backspace in the add form
func (m *MCPSettings) handleAddFormBackspace(state *State) {
	// Skip if on type selector field
	if state.AddFormField == 1 {
		return
	}

	switch state.AddFormField {
	case 0: // Name
		if len(state.AddFormName) > 0 {
			state.AddFormName = state.AddFormName[:len(state.AddFormName)-1]
		}
	case 2: // Command
		if len(state.AddFormCommand) > 0 {
			state.AddFormCommand = state.AddFormCommand[:len(state.AddFormCommand)-1]
		}
	case 3: // Args
		if len(state.AddFormArgs) > 0 {
			state.AddFormArgs = state.AddFormArgs[:len(state.AddFormArgs)-1]
		}
	case 4: // URL
		if len(state.AddFormURL) > 0 {
			state.AddFormURL = state.AddFormURL[:len(state.AddFormURL)-1]
		}
	case 5: // Headers
		if len(state.AddFormHeaders) > 0 {
			state.AddFormHeaders = state.AddFormHeaders[:len(state.AddFormHeaders)-1]
		}
	case 7: // ClientID
		if len(state.AddFormClientID) > 0 {
			state.AddFormClientID = state.AddFormClientID[:len(state.AddFormClientID)-1]
		}
	case 8: // Scopes
		if len(state.AddFormScopes) > 0 {
			state.AddFormScopes = state.AddFormScopes[:len(state.AddFormScopes)-1]
		}
	case 9: // Env
		if len(state.AddFormEnv) > 0 {
			state.AddFormEnv = state.AddFormEnv[:len(state.AddFormEnv)-1]
		}
	case 10: // WorkDir
		if len(state.AddFormWorkDir) > 0 {
			state.AddFormWorkDir = state.AddFormWorkDir[:len(state.AddFormWorkDir)-1]
		}
	case 11: // Timeout
		if len(state.AddFormTimeout) > 0 {
			state.AddFormTimeout = state.AddFormTimeout[:len(state.AddFormTimeout)-1]
		}
	}
}

// cycleAddFormType cycles through server types
func (m *MCPSettings) cycleAddFormType(state *State, direction int) {
	types := []string{"stdio", "sse", "http", "oauth"}
	currentIdx := 0
	for i, t := range types {
		if t == state.AddFormType {
			currentIdx = i
			break
		}
	}
	newIdx := (currentIdx + direction + len(types)) % len(types)
	state.AddFormType = types[newIdx]
}

// nextAddFormField returns the next form field based on server type
func (m *MCPSettings) nextAddFormField(state *State) int {
	fields := m.getAddFormFields(state)
	currentIdx := 0
	for i, f := range fields {
		if f == state.AddFormField {
			currentIdx = i
			break
		}
	}
	nextIdx := (currentIdx + 1) % len(fields)
	return fields[nextIdx]
}

// prevAddFormField returns the previous form field based on server type
func (m *MCPSettings) prevAddFormField(state *State) int {
	fields := m.getAddFormFields(state)
	currentIdx := 0
	for i, f := range fields {
		if f == state.AddFormField {
			currentIdx = i
			break
		}
	}
	prevIdx := (currentIdx - 1 + len(fields)) % len(fields)
	return fields[prevIdx]
}

// getAddFormFields returns the list of valid field IDs for the current server type
func (m *MCPSettings) getAddFormFields(state *State) []int {
	baseFields := []int{0, 1} // Name, Type

	switch state.AddFormType {
	case "stdio":
		baseFields = append(baseFields, 2, 3, 10) // Command, Args, WorkDir
	case "oauth":
		baseFields = append(baseFields, 4, 7, 8) // URL, ClientID, Scopes
	default: // sse, http
		baseFields = append(baseFields, 4, 5) // URL, Headers
	}

	// Common optional fields
	baseFields = append(baseFields, 9, 11) // Env, Timeout

	return baseFields
}

// submitAddForm validates and submits the add server form
func (m *MCPSettings) submitAddForm(state *State) bool {
	// Validate based on server type
	valid := state.AddFormName != ""

	switch state.AddFormType {
	case "stdio":
		valid = valid && state.AddFormCommand != ""
	case "oauth":
		valid = valid && state.AddFormURL != "" && state.AddFormClientID != ""
	default: // sse, http
		valid = valid && state.AddFormURL != ""
	}

	if !valid {
		// TODO: Show validation error message
		return true
	}

	// Handle edit mode - update existing server
	if state.MCPServersEditMode {
		if state.MCPServersSelected >= 0 && state.MCPServersSelected < len(m.servers) {
			server := m.servers[state.MCPServersSelected]

			// Update server config with form values
			server.Config.Name = state.AddFormName
			server.Config.Type = commands.MCPServerType(state.AddFormType)
			server.Config.Command = state.AddFormCommand

			// Parse Args from space-separated string
			if state.AddFormArgs != "" {
				server.Config.Args = strings.Fields(state.AddFormArgs)
			} else {
				server.Config.Args = []string{}
			}

			server.Config.URL = state.AddFormURL

			// Parse Headers from KEY=VALUE,KEY2=VALUE2 format
			if state.AddFormHeaders != "" {
				headers := make(map[string]string)
				pairs := strings.SplitSeq(state.AddFormHeaders, ",")
				for pair := range pairs {
					kv := strings.SplitN(strings.TrimSpace(pair), "=", 2)
					if len(kv) == 2 {
						headers[kv[0]] = kv[1]
					}
				}
				server.Config.Headers = headers
			} else {
				server.Config.Headers = nil
			}

			// Update OAuth config if needed
			if state.AddFormType == "oauth" {
				if server.Config.OAuth == nil {
					server.Config.OAuth = &commands.MCPOAuthConfig{}
				}
				server.Config.OAuth.ClientID = state.AddFormClientID
				server.Config.OAuth.Scopes = state.AddFormScopes
			} else {
				server.Config.OAuth = nil
			}

			// Parse Env from KEY=VALUE,KEY2=VALUE2 format
			if state.AddFormEnv != "" {
				env := make(map[string]string)
				pairs := strings.SplitSeq(state.AddFormEnv, ",")
				for pair := range pairs {
					kv := strings.SplitN(strings.TrimSpace(pair), "=", 2)
					if len(kv) == 2 {
						env[kv[0]] = kv[1]
					}
				}
				server.Config.Env = env
			} else {
				server.Config.Env = nil
			}

			server.Config.WorkingDir = state.AddFormWorkDir

			// Parse timeout
			if state.AddFormTimeout != "" {
				timeout := 0
				fmt.Sscanf(state.AddFormTimeout, "%d", &timeout)
				server.Config.Timeout = timeout
			} else {
				server.Config.Timeout = 0
			}

			// TODO: Trigger a callback to save the config to disk and reconnect if needed
		}
	} else {
		// TODO: Actually create the server config and add it
		// For now, just exit form mode
	}

	// Exit form mode (both add and edit)
	state.MCPServersAddMode = false
	state.MCPServersEditMode = false

	// Reset form fields
	state.AddFormField = 0
	state.AddFormName = ""
	state.AddFormType = "stdio"
	state.AddFormCommand = ""
	state.AddFormArgs = ""
	state.AddFormURL = ""
	state.AddFormHeaders = ""
	state.AddFormClientID = ""
	state.AddFormScopes = ""
	state.AddFormEnv = ""
	state.AddFormWorkDir = ""
	state.AddFormTimeout = ""

	return true
}
