package settings

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/plugins"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/i18n"
)

func (p *PluginsSettings) Render(width, height int, state *State, th Theme) string {
	if width < 40 || height < 15 {
		return p.renderMinimal(width, height, th)
	}

	switch state.PluginsState {
	case "detail":
		return p.renderDetail(width, height, state, th)
	case "search":
		return p.renderSearch(width, height, state, th)
	case "marketplace":
		return p.renderMarketplace(width, height, state, th)
	case "add_source":
		return p.renderAddSource(width, height, state, th)
	default:
		return p.renderList(width, height, state, th)
	}
}

// renderMinimal renders a minimal view for small terminals
func (p *PluginsSettings) renderMinimal(width, height int, th Theme) string {
	msg := i18n.T("settings.extensions.common.terminal_too_small")
	return lipgloss.NewStyle().
		Width(width).
		Height(height).
		Foreground(lipgloss.Color(th.TextMuted)).
		Align(lipgloss.Center, lipgloss.Center).
		Render(msg)
}

// renderList renders the main plugins list view with split-panel layout
func (p *PluginsSettings) renderList(width, height int, state *State, th Theme) string {
	var sections []string

	// Title with styled badges
	title := p.renderTitle(width, th)
	sections = append(sections, title)
	sections = append(sections, "")

	// Calculate content area height
	contentHeight := maxInt(5, height-10)

	// Error/success message area
	var msgArea string
	if p.errorMessage != "" {
		errStyle := lipgloss.NewStyle().
			Foreground(lipgloss.Color(th.Error)).
			Width(maxInt(10, width-6)).
			Align(lipgloss.Center)
		msgArea = errStyle.Render(p.errorMessage)
		contentHeight -= 1
	} else if p.successMsg != "" {
		successStyle := lipgloss.NewStyle().
			Foreground(lipgloss.Color(th.Success)).
			Width(maxInt(10, width-6)).
			Align(lipgloss.Center)
		msgArea = successStyle.Render(p.successMsg)
		contentHeight -= 1
	}

	// Render split panel (list + detail)
	panelContent := p.renderListAndDetail(maxInt(20, width-6), contentHeight, state, th)
	sections = append(sections, panelContent)

	// Add message if present
	if msgArea != "" {
		sections = append(sections, msgArea)
	}

	// Hint bar
	sections = append(sections, "")
	sections = append(sections, p.renderHintBar(width, state, th))

	content := lipgloss.JoinVertical(lipgloss.Left, sections...)

	// Container with rounded border
	containerStyle := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color(th.Border)).
		Width(maxInt(20, width-2)).
		Padding(1, 2)

	return containerStyle.Render(content)
}

// renderTitle renders the section title with view mode tabs
func (p *PluginsSettings) renderTitle(width int, th Theme) string {
	allPlugins := p.GetPlugins()
	enabledPlugins := p.GetEnabledPlugins()
	marketplaceCount := len(p.marketplaceEntries)
	sourcesCount := len(p.marketplaceSources)

	titleStyle := lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color(th.Primary))

	title := titleStyle.Render(i18n.T("settings.extensions.plugins.title"))

	// View mode tabs - 3 tabs now
	installedTabStyle := lipgloss.NewStyle().Padding(0, 1)
	availableTabStyle := lipgloss.NewStyle().Padding(0, 1)
	sourcesTabStyle := lipgloss.NewStyle().Padding(0, 1)

	switch p.viewMode {
	case "installed":
		installedTabStyle = installedTabStyle.
			Foreground(lipgloss.Color(th.Text)).
			Background(lipgloss.Color(th.Primary)).
			Bold(true)
		availableTabStyle = availableTabStyle.
			Foreground(lipgloss.Color(th.TextMuted))
		sourcesTabStyle = sourcesTabStyle.
			Foreground(lipgloss.Color(th.TextMuted))
	case "available":
		installedTabStyle = installedTabStyle.
			Foreground(lipgloss.Color(th.TextMuted))
		availableTabStyle = availableTabStyle.
			Foreground(lipgloss.Color(th.Text)).
			Background(lipgloss.Color(th.Primary)).
			Bold(true)
		sourcesTabStyle = sourcesTabStyle.
			Foreground(lipgloss.Color(th.TextMuted))
	case "sources":
		installedTabStyle = installedTabStyle.
			Foreground(lipgloss.Color(th.TextMuted))
		availableTabStyle = availableTabStyle.
			Foreground(lipgloss.Color(th.TextMuted))
		sourcesTabStyle = sourcesTabStyle.
			Foreground(lipgloss.Color(th.Text)).
			Background(lipgloss.Color(th.Primary)).
			Bold(true)
	}

	installedTab := installedTabStyle.Render(i18n.T("settings.extensions.common.installed_count", len(allPlugins)))
	availableTab := availableTabStyle.Render(i18n.T("settings.extensions.common.available_count", marketplaceCount))
	sourcesTab := sourcesTabStyle.Render(i18n.T("settings.extensions.common.sources_count", sourcesCount))

	// Loading indicator
	loadingIndicator := ""
	if p.marketplaceLoading {
		loadingIndicator = lipgloss.NewStyle().
			Foreground(lipgloss.Color(th.Warning)).
			Render(" ⟳")
	}

	tabs := installedTab + " " + availableTab + " " + sourcesTab + loadingIndicator

	// Stats line
	var statsLine string
	switch p.viewMode {
	case "installed":
		enabledBadge := lipgloss.NewStyle().
			Foreground(lipgloss.Color(th.Success)).
			Render(i18n.T("settings.extensions.common.enabled_count", len(enabledPlugins)))
		statsLine = enabledBadge
	case "available":
		featuredCount := 0
		for _, e := range p.marketplaceEntries {
			if e.Featured {
				featuredCount++
			}
		}
		if featuredCount > 0 {
			statsLine = lipgloss.NewStyle().
				Foreground(lipgloss.Color(th.Warning)).
				Render(i18n.T("settings.extensions.common.featured_count", featuredCount))
		}
	case "sources":
		enabledCount := 0
		for _, src := range p.marketplaceSources {
			if src.Enabled {
				enabledCount++
			}
		}
		statsLine = lipgloss.NewStyle().
			Foreground(lipgloss.Color(th.Success)).
			Render(i18n.T("settings.extensions.common.enabled_count", enabledCount))
	}

	// Center the title and tabs
	tw := maxInt(10, width-4)
	titleLine := lipgloss.NewStyle().
		Width(tw).
		Align(lipgloss.Center).
		Render(title)

	tabLine := lipgloss.NewStyle().
		Width(tw).
		Align(lipgloss.Center).
		Render(tabs)

	lines := []string{titleLine, tabLine}
	if statsLine != "" {
		statsStyled := lipgloss.NewStyle().
			Width(tw).
			Align(lipgloss.Center).
			Render(statsLine)
		lines = append(lines, statsStyled)
	}

	return lipgloss.JoinVertical(lipgloss.Left, lines...)
}

// renderPluginLine renders a single plugin entry with source badge
func (p *PluginsSettings) renderPluginLine(plugin *plugins.Plugin, isSelected bool, width int, th Theme) string {
	var parts []string

	// Source badge
	source := p.getPluginSource(plugin)
	var srcColor string
	switch source {
	case "USR":
		srcColor = th.Success
	case "PRJ":
		srcColor = th.Warning
	case "MKT":
		srcColor = th.Primary
	default:
		srcColor = th.TextMuted
	}
	srcBadge := lipgloss.NewStyle().
		Foreground(lipgloss.Color(th.BGLight)).
		Background(lipgloss.Color(srcColor)).
		Padding(0, 1).
		Render(source)
	parts = append(parts, srcBadge)

	// Status indicator
	if plugin.Enabled {
		enabledStyle := lipgloss.NewStyle().
			Foreground(lipgloss.Color(th.Success)).
			Bold(true)
		parts = append(parts, enabledStyle.Render("[*]"))
	} else {
		parts = append(parts, lipgloss.NewStyle().
			Foreground(lipgloss.Color(th.TextMuted)).
			Render("[ ]"))
	}

	// Name
	nameColor := th.Text
	if isSelected {
		nameColor = th.Primary
	}
	nameStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(nameColor)).
		Bold(isSelected)
	parts = append(parts, nameStyle.Render(plugin.Manifest.Name))

	// Component counts
	countStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(th.TextMuted))
	if len(plugin.Commands) > 0 {
		parts = append(parts, countStyle.Render(i18n.T("settings.extensions.plugins.command_badge_count", len(plugin.Commands))))
	}
	if len(plugin.Agents) > 0 {
		parts = append(parts, countStyle.Render(i18n.T("settings.extensions.plugins.agent_badge_count", len(plugin.Agents))))
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

// renderListAndDetail renders the split panel layout
func (p *PluginsSettings) renderListAndDetail(width, height int, state *State, th Theme) string {
	listHeight := maxInt(3, height-2)

	// Collapse to single-pane at narrow widths
	if width < 60 {
		listWidth := maxInt(15, width-2)
		var listPanel string
		switch p.viewMode {
		case "available":
			listPanel = p.renderMarketplaceList(listWidth, listHeight, state, th)
		case "sources":
			listPanel = p.renderSourcesList(listWidth, listHeight, state, th)
		default:
			allPlugins := p.GetPlugins()
			var listLines []string
			listHeaderStyle := lipgloss.NewStyle().
				Foreground(lipgloss.Color(th.Primary)).
				Bold(true)
			listLines = append(listLines, listHeaderStyle.Render(i18n.T("settings.extensions.common.installed")))
			listLines = append(listLines, strings.Repeat("─", maxInt(1, listWidth-2)))
			if len(allPlugins) == 0 {
				emptyStyle := lipgloss.NewStyle().
					Foreground(lipgloss.Color(th.TextMuted)).
					Italic(true)
				listLines = append(listLines, emptyStyle.Render(i18n.T("settings.extensions.plugins.no_installed")))
				listLines = append(listLines, "")
				listLines = append(listLines, emptyStyle.Render(i18n.T("settings.extensions.common.press_available")))
				listLines = append(listLines, emptyStyle.Render(i18n.T("settings.extensions.common.press_search")))
			} else {
				visibleEnd := minInt(state.PluginsScrollOffset+listHeight-3, len(allPlugins))
				for i := state.PluginsScrollOffset; i < visibleEnd; i++ {
					plugin := allPlugins[i]
					isSelected := i == state.PluginsSelected
					listLines = append(listLines, p.renderPluginLine(plugin, isSelected, maxInt(8, listWidth-2), th))
				}
			}
			for len(listLines) < listHeight {
				listLines = append(listLines, "")
			}
			listPanel = lipgloss.NewStyle().
				Width(listWidth).
				Height(listHeight).
				Render(lipgloss.JoinVertical(lipgloss.Left, listLines...))
		}
		return listPanel
	}

	// Calculate panel widths (40/60 split)
	listWidth := (width - 6) * 40 / 100
	detailWidth := maxInt(15, (width-6)-listWidth-3)

	var listPanel, detailPanel string

	switch p.viewMode {
	case "available":
		// Show marketplace entries
		listPanel = p.renderMarketplaceList(listWidth, listHeight, state, th)
		detailPanel = p.renderMarketplaceDetails(state, detailWidth, listHeight, th)
	case "sources":
		// Show marketplace sources
		listPanel = p.renderSourcesList(listWidth, listHeight, state, th)
		detailPanel = p.renderSourcesDetails(state, detailWidth, listHeight, th)
	default:
		// Show installed plugins
		allPlugins := p.GetPlugins()

		// Render plugins list
		var listLines []string
		listHeaderStyle := lipgloss.NewStyle().
			Foreground(lipgloss.Color(th.Primary)).
			Bold(true)
		listLines = append(listLines, listHeaderStyle.Render(i18n.T("settings.extensions.common.installed")))
		listLines = append(listLines, strings.Repeat("─", maxInt(1, listWidth-2)))

		if len(allPlugins) == 0 {
			emptyStyle := lipgloss.NewStyle().
				Foreground(lipgloss.Color(th.TextMuted)).
				Italic(true)
			listLines = append(listLines, emptyStyle.Render(i18n.T("settings.extensions.plugins.no_installed")))
			listLines = append(listLines, "")
			listLines = append(listLines, emptyStyle.Render(i18n.T("settings.extensions.common.press_available")))
			listLines = append(listLines, emptyStyle.Render(i18n.T("settings.extensions.common.press_search")))
		} else {
			visibleEnd := minInt(state.PluginsScrollOffset+listHeight-3, len(allPlugins))
			for i := state.PluginsScrollOffset; i < visibleEnd; i++ {
				plugin := allPlugins[i]
				isSelected := i == state.PluginsSelected
				listLines = append(listLines, p.renderPluginLine(plugin, isSelected, maxInt(8, listWidth-2), th))
			}
		}

		// Pad list
		for len(listLines) < listHeight {
			listLines = append(listLines, "")
		}

		listPanel = lipgloss.NewStyle().
			Width(listWidth).
			Height(listHeight).
			Render(lipgloss.JoinVertical(lipgloss.Left, listLines...))

		// Render detail panel
		detailPanel = p.renderPluginDetails(allPlugins, state, detailWidth, listHeight, th)
	}

	// Join panels horizontally
	separator := lipgloss.NewStyle().
		Foreground(lipgloss.Color(th.Border)).
		Render(strings.Repeat("│\n", maxInt(1, listHeight)))

	return lipgloss.JoinHorizontal(lipgloss.Top, listPanel, " ", separator, " ", detailPanel)
}

// renderMarketplaceList renders the marketplace entries list panel
func (p *PluginsSettings) renderMarketplaceList(listWidth, listHeight int, state *State, th Theme) string {
	entries := p.marketplaceEntries

	var listLines []string
	listHeaderStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(th.Primary)).
		Bold(true)
	listLines = append(listLines, listHeaderStyle.Render(i18n.T("settings.extensions.common.available")))
	listLines = append(listLines, strings.Repeat("─", maxInt(1, listWidth-2)))

	if p.marketplaceLoading {
		loadingStyle := lipgloss.NewStyle().
			Foreground(lipgloss.Color(th.Warning)).
			Italic(true)
		listLines = append(listLines, loadingStyle.Render(i18n.T("settings.extensions.common.loading")))
	} else if len(entries) == 0 {
		emptyStyle := lipgloss.NewStyle().
			Foreground(lipgloss.Color(th.TextMuted)).
			Italic(true)
		listLines = append(listLines, emptyStyle.Render(i18n.T("settings.extensions.plugins.no_found")))
		listLines = append(listLines, "")
		listLines = append(listLines, emptyStyle.Render(i18n.T("settings.extensions.common.press_refresh")))
	} else {
		visibleEnd := minInt(state.PluginsScrollOffset+listHeight-3, len(entries))
		for i := state.PluginsScrollOffset; i < visibleEnd; i++ {
			entry := entries[i]
			isSelected := i == state.PluginsSelected
			listLines = append(listLines, p.renderMarketplaceEntryLine(entry, isSelected, listWidth-2, th))
		}
	}

	// Pad list
	for len(listLines) < listHeight {
		listLines = append(listLines, "")
	}

	return lipgloss.NewStyle().
		Width(listWidth).
		Height(listHeight).
		Render(lipgloss.JoinVertical(lipgloss.Left, listLines...))
}

// renderMarketplaceEntryLine renders a single marketplace entry line
func (p *PluginsSettings) renderMarketplaceEntryLine(entry plugins.PluginMarketplaceEntry, isSelected bool, width int, th Theme) string {
	var parts []string

	// Source badge
	var srcColor string
	switch entry.Marketplace {
	case "github-claude-plugins", "github-mcp-servers":
		srcColor = th.Primary
	case "awesome-mcp-servers":
		srcColor = th.Warning
	default:
		srcColor = th.Success
	}

	srcBadge := lipgloss.NewStyle().
		Foreground(lipgloss.Color(th.BGLight)).
		Background(lipgloss.Color(srcColor)).
		Padding(0, 1).
		Render(i18n.T("settings.extensions.common.marketplace_badge"))
	parts = append(parts, srcBadge)

	// Featured indicator
	if entry.Featured {
		parts = append(parts, lipgloss.NewStyle().
			Foreground(lipgloss.Color(th.Warning)).
			Bold(true).
			Render("★"))
	}

	// Verified indicator
	if entry.Verified {
		parts = append(parts, lipgloss.NewStyle().
			Foreground(lipgloss.Color(th.Success)).
			Render("✓"))
	}

	// Check if already installed
	installed := false
	if p.loader != nil {
		installed = p.loader.Get(entry.Name) != nil
	}
	if installed {
		parts = append(parts, lipgloss.NewStyle().
			Foreground(lipgloss.Color(th.Success)).
			Render("[*]"))
	}

	// Name
	nameColor := th.Text
	if isSelected {
		nameColor = th.Primary
	}
	nameStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(nameColor)).
		Bold(isSelected)
	parts = append(parts, nameStyle.Render(entry.Name))

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

// renderMarketplaceDetails renders the detail panel for the selected marketplace entry
func (p *PluginsSettings) renderMarketplaceDetails(state *State, width, height int, th Theme) string {
	entries := p.marketplaceEntries
	var lines []string

	headerStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(th.Primary)).
		Bold(true)
	lines = append(lines, headerStyle.Render(i18n.T("settings.extensions.common.details")))
	lines = append(lines, strings.Repeat("─", maxInt(1, width-2)))

	if len(entries) == 0 || state.PluginsSelected >= len(entries) {
		emptyStyle := lipgloss.NewStyle().
			Foreground(lipgloss.Color(th.TextMuted)).
			Italic(true)
		lines = append(lines, "")
		if p.marketplaceLoading {
			lines = append(lines, emptyStyle.Render(i18n.T("settings.extensions.common.loading_marketplace")))
		} else {
			lines = append(lines, emptyStyle.Render(i18n.T("settings.extensions.plugins.select_details")))
		}
		for len(lines) < height {
			lines = append(lines, "")
		}
		return lipgloss.NewStyle().
			Width(width).
			Height(height).
			Render(lipgloss.JoinVertical(lipgloss.Left, lines...))
	}

	entry := entries[state.PluginsSelected]

	// Plugin name
	nameStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(th.Primary)).
		Bold(true)
	lines = append(lines, nameStyle.Render(entry.Name))

	// Status badges
	var badges []string
	if entry.Featured {
		badges = append(badges, lipgloss.NewStyle().
			Foreground(lipgloss.Color(th.Text)).
			Background(lipgloss.Color(th.Warning)).
			Padding(0, 1).
			Render(i18n.T("settings.extensions.common.badge_featured")))
	}
	if entry.Verified {
		badges = append(badges, lipgloss.NewStyle().
			Foreground(lipgloss.Color(th.Text)).
			Background(lipgloss.Color(th.Success)).
			Padding(0, 1).
			Render(i18n.T("settings.extensions.plugins.badge_verified")))
	}
	installed := false
	if p.loader != nil {
		installed = p.loader.Get(entry.Name) != nil
	}
	if installed {
		badges = append(badges, lipgloss.NewStyle().
			Foreground(lipgloss.Color(th.Text)).
			Background(lipgloss.Color(th.Primary)).
			Padding(0, 1).
			Render(i18n.T("settings.extensions.common.badge_installed")))
	}
	if len(badges) > 0 {
		lines = append(lines, strings.Join(badges, " "))
	}
	lines = append(lines, "")

	// Metadata
	metaStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(th.TextMuted))
	labelStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(th.Text))

	if entry.Version != "" {
		lines = append(lines, labelStyle.Render(i18n.T("settings.extensions.common.label_version"))+metaStyle.Render(entry.Version))
	}
	if entry.Author != "" {
		lines = append(lines, labelStyle.Render(i18n.T("settings.extensions.common.label_author"))+metaStyle.Render(entry.Author))
	}
	if entry.Category != "" {
		catBadge := lipgloss.NewStyle().
			Foreground(lipgloss.Color(th.Warning)).
			Render("[" + entry.Category + "]")
		lines = append(lines, labelStyle.Render(i18n.T("settings.extensions.common.label_category"))+catBadge)
	}
	if len(entry.Tags) > 0 {
		tags := strings.Join(entry.Tags, ", ")
		if width > 18 && len(tags) > width-15 {
			tags = tags[:width-18] + "..."
		}
		lines = append(lines, labelStyle.Render(i18n.T("settings.extensions.common.label_tags"))+metaStyle.Render(tags))
	}
	lines = append(lines, labelStyle.Render(i18n.T("settings.extensions.common.label_source"))+metaStyle.Render(entry.Marketplace))
	lines = append(lines, "")

	// Description
	dw := maxInt(4, width-4)
	if entry.Description != "" {
		lines = append(lines, labelStyle.Render(i18n.T("settings.extensions.common.label_description")))
		descStyle := lipgloss.NewStyle().
			Foreground(lipgloss.Color(th.TextMuted)).
			Width(dw)
		desc := entry.Description
		if len(desc) > dw*4 {
			desc = desc[:dw*4-3] + "..."
		}
		lines = append(lines, descStyle.Render(desc))
		lines = append(lines, "")
	}

	// Install source
	if entry.Source != "" {
		lines = append(lines, labelStyle.Render(i18n.T("settings.extensions.common.label_install_from")))
		srcStyle := lipgloss.NewStyle().
			Foreground(lipgloss.Color(th.TextMuted)).
			Width(dw)
		src := entry.Source
		if width > 7 && len(src) > dw {
			src = src[:dw-3] + "..."
		}
		lines = append(lines, srcStyle.Render(src))
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

// renderSourcesList renders the marketplace sources list panel
func (p *PluginsSettings) renderSourcesList(listWidth, listHeight int, state *State, th Theme) string {
	sources := p.marketplaceSources

	var listLines []string
	listHeaderStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(th.Primary)).
		Bold(true)
	listLines = append(listLines, listHeaderStyle.Render(i18n.T("settings.extensions.common.marketplace_sources")))
	listLines = append(listLines, strings.Repeat("─", maxInt(1, listWidth-2)))

	if len(sources) == 0 {
		emptyStyle := lipgloss.NewStyle().
			Foreground(lipgloss.Color(th.TextMuted)).
			Italic(true)
		listLines = append(listLines, emptyStyle.Render(i18n.T("settings.extensions.common.no_sources")))
		listLines = append(listLines, "")
		listLines = append(listLines, emptyStyle.Render(i18n.T("settings.extensions.common.press_add_source")))
	} else {
		visibleEnd := minInt(state.PluginsScrollOffset+listHeight-3, len(sources))
		for i := state.PluginsScrollOffset; i < visibleEnd; i++ {
			source := sources[i]
			isSelected := i == state.PluginsSelected
			listLines = append(listLines, p.renderSourceLine(source, isSelected, listWidth-2, th))
		}
	}

	// Pad list
	for len(listLines) < listHeight {
		listLines = append(listLines, "")
	}

	return lipgloss.NewStyle().
		Width(listWidth).
		Height(listHeight).
		Render(lipgloss.JoinVertical(lipgloss.Left, listLines...))
}

// renderSourceLine renders a single marketplace source entry
func (p *PluginsSettings) renderSourceLine(source plugins.MarketplaceSource, isSelected bool, width int, th Theme) string {
	var parts []string

	// Enabled indicator
	if source.Enabled {
		enabledStyle := lipgloss.NewStyle().
			Foreground(lipgloss.Color(th.Success))
		parts = append(parts, enabledStyle.Render("●"))
	} else {
		disabledStyle := lipgloss.NewStyle().
			Foreground(lipgloss.Color(th.Error))
		parts = append(parts, disabledStyle.Render("○"))
	}

	// Type badge
	typeColor := th.TextMuted
	switch source.Type {
	case "github":
		typeColor = th.Primary
	case "awesome-list":
		typeColor = th.Warning
	case "http":
		typeColor = th.Success
	}
	typeBadge := lipgloss.NewStyle().
		Foreground(lipgloss.Color(typeColor)).
		Render(fmt.Sprintf("[%s]", source.Type))
	parts = append(parts, typeBadge)

	// Name
	nameWidth := maxInt(5, width-20)
	name := source.Name
	if len(name) > nameWidth {
		name = name[:maxInt(1, nameWidth-3)] + "..."
	}
	parts = append(parts, name)

	line := strings.Join(parts, " ")

	if isSelected {
		return lipgloss.NewStyle().
			Foreground(lipgloss.Color(th.Text)).
			Background(lipgloss.Color(th.Primary)).
			Width(width).
			Render(line)
	}

	return lipgloss.NewStyle().
		Width(width).
		Render(line)
}

// renderSourcesDetails renders the detail panel for the selected marketplace source
func (p *PluginsSettings) renderSourcesDetails(state *State, width, height int, th Theme) string {
	sources := p.marketplaceSources
	var lines []string

	headerStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(th.Primary)).
		Bold(true)
	lines = append(lines, headerStyle.Render(i18n.T("settings.extensions.common.source_details")))
	lines = append(lines, strings.Repeat("─", maxInt(1, width-2)))

	if len(sources) == 0 || state.PluginsSelected >= len(sources) {
		emptyStyle := lipgloss.NewStyle().
			Foreground(lipgloss.Color(th.TextMuted)).
			Italic(true)
		lines = append(lines, "")
		lines = append(lines, emptyStyle.Render(i18n.T("settings.extensions.common.select_source")))
		lines = append(lines, "")
		lines = append(lines, emptyStyle.Render(i18n.T("settings.extensions.common.press_add_new_source")))
		for len(lines) < height {
			lines = append(lines, "")
		}
		return lipgloss.NewStyle().
			Width(width).
			Height(height).
			Render(lipgloss.JoinVertical(lipgloss.Left, lines...))
	}

	source := sources[state.PluginsSelected]

	// Source name
	nameStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(th.Primary)).
		Bold(true)
	lines = append(lines, nameStyle.Render(source.Name))

	// Status badge
	if source.Enabled {
		enabledBadge := lipgloss.NewStyle().
			Foreground(lipgloss.Color(th.Text)).
			Background(lipgloss.Color(th.Success)).
			Padding(0, 1).
			Render(i18n.T("settings.extensions.common.badge_enabled"))
		lines = append(lines, enabledBadge)
	} else {
		disabledBadge := lipgloss.NewStyle().
			Foreground(lipgloss.Color(th.Text)).
			Background(lipgloss.Color(th.Error)).
			Padding(0, 1).
			Render(i18n.T("settings.extensions.common.badge_disabled"))
		lines = append(lines, disabledBadge)
	}
	lines = append(lines, "")

	// Metadata
	metaStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(th.TextMuted))
	labelStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(th.Text))

	lines = append(lines, labelStyle.Render(i18n.T("settings.extensions.common.label_type"))+metaStyle.Render(source.Type))
	lines = append(lines, labelStyle.Render(i18n.T("settings.extensions.common.label_priority"))+metaStyle.Render(fmt.Sprintf("%d", source.Priority)))
	lines = append(lines, "")

	// URL
	lines = append(lines, labelStyle.Render(i18n.T("settings.extensions.common.label_url")))
	uw := maxInt(4, width-4)
	urlStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(th.TextMuted)).
		Width(uw)
	url := source.URL
	// Wrap URL if too long
	for len(url) > uw {
		lines = append(lines, urlStyle.Render(url[:uw]))
		url = url[uw:]
	}
	if len(url) > 0 {
		lines = append(lines, urlStyle.Render(url))
	}
	lines = append(lines, "")

	// Actions hint
	lines = append(lines, "")
	actionsStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(th.TextMuted)).
		Italic(true)
	lines = append(lines, actionsStyle.Render(i18n.T("settings.extensions.common.toggle_source")))
	lines = append(lines, actionsStyle.Render(i18n.T("settings.extensions.common.delete_source")))
	lines = append(lines, actionsStyle.Render(i18n.T("settings.extensions.common.refresh_marketplace")))

	// Pad to height
	for len(lines) < height {
		lines = append(lines, "")
	}

	return lipgloss.NewStyle().
		Width(width).
		Height(height).
		Render(lipgloss.JoinVertical(lipgloss.Left, lines...))
}

// renderAddSource renders the add source form
func (p *PluginsSettings) renderAddSource(width, height int, state *State, th Theme) string {
	var lines []string

	titleStyle := lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color(th.Primary))
	lines = append(lines, titleStyle.Render(i18n.T("settings.extensions.common.add_source_title")))
	lines = append(lines, strings.Repeat("─", maxInt(1, width-4)))
	lines = append(lines, "")

	labelStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(th.Text))
	valueStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(th.TextMuted))
	activeStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(th.Text)).
		Background(lipgloss.Color(th.Primary))

	// Name field
	nameLabel := i18n.T("settings.extensions.common.label_name")
	if p.addSourceState == "name" {
		lines = append(lines, labelStyle.Render(nameLabel)+activeStyle.Render(p.newSourceName+"_"))
	} else {
		lines = append(lines, labelStyle.Render(nameLabel)+valueStyle.Render(p.newSourceName))
	}

	// URL field
	urlLabel := i18n.T("settings.extensions.common.label_url_padded")
	if p.addSourceState == "url" {
		lines = append(lines, labelStyle.Render(urlLabel)+activeStyle.Render(p.newSourceURL+"_"))
	} else {
		lines = append(lines, labelStyle.Render(urlLabel)+valueStyle.Render(p.newSourceURL))
	}

	// Type field
	typeLabel := i18n.T("settings.extensions.common.label_type")
	typeOptions := []string{"http", "github", "awesome-list"}
	var typeDisplay strings.Builder
	for i, opt := range typeOptions {
		if p.addSourceState == "type" && opt == p.newSourceType {
			typeDisplay.WriteString(activeStyle.Render("["+opt+"]") + " ")
		} else if opt == p.newSourceType {
			typeDisplay.WriteString(labelStyle.Render("["+opt+"]") + " ")
		} else {
			typeDisplay.WriteString(valueStyle.Render(opt) + " ")
		}
		if i < len(typeOptions)-1 {
			typeDisplay.WriteString(" ")
		}
	}
	lines = append(lines, labelStyle.Render(typeLabel)+typeDisplay.String())
	lines = append(lines, "")

	// Instructions
	hintStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(th.TextMuted)).
		Italic(true)
	lines = append(lines, hintStyle.Render(i18n.T("settings.extensions.common.form_hint")))
	lines = append(lines, "")

	// Type descriptions
	lines = append(lines, labelStyle.Render(i18n.T("settings.extensions.common.type_descriptions")))
	lines = append(lines, valueStyle.Render(i18n.T("settings.extensions.plugins.http_description")))
	lines = append(lines, valueStyle.Render(i18n.T("settings.extensions.plugins.github_description")))
	lines = append(lines, valueStyle.Render(i18n.T("settings.extensions.plugins.awesome_description")))

	// Error message
	if p.errorMessage != "" {
		lines = append(lines, "")
		errStyle := lipgloss.NewStyle().
			Foreground(lipgloss.Color(th.Error))
		lines = append(lines, errStyle.Render(p.errorMessage))
	}

	// Pad
	for len(lines) < maxInt(1, height-2) {
		lines = append(lines, "")
	}

	content := lipgloss.JoinVertical(lipgloss.Left, lines...)

	containerStyle := lipgloss.NewStyle().
		Width(maxInt(10, width-4)).
		Height(maxInt(3, height-2)).
		Padding(1, 2)

	return containerStyle.Render(content)
}

// renderPluginDetails renders the detail panel for the selected plugin
func (p *PluginsSettings) renderPluginDetails(allPlugins []*plugins.Plugin, state *State, width, height int, th Theme) string {
	var lines []string

	headerStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(th.Primary)).
		Bold(true)
	lines = append(lines, headerStyle.Render(i18n.T("settings.extensions.common.details")))
	lines = append(lines, strings.Repeat("─", maxInt(1, width-2)))

	if len(allPlugins) == 0 || state.PluginsSelected >= len(allPlugins) {
		emptyStyle := lipgloss.NewStyle().
			Foreground(lipgloss.Color(th.TextMuted)).
			Italic(true)
		lines = append(lines, "")
		lines = append(lines, emptyStyle.Render(i18n.T("settings.extensions.plugins.select_details")))
		for len(lines) < height {
			lines = append(lines, "")
		}
		return lipgloss.NewStyle().
			Width(width).
			Height(height).
			Render(lipgloss.JoinVertical(lipgloss.Left, lines...))
	}

	plugin := allPlugins[state.PluginsSelected]

	// Plugin name
	nameStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(th.Primary)).
		Bold(true)
	lines = append(lines, nameStyle.Render(plugin.Manifest.Name))

	// Status badge
	if plugin.Enabled {
		enabledBadge := lipgloss.NewStyle().
			Foreground(lipgloss.Color(th.Text)).
			Background(lipgloss.Color(th.Success)).
			Padding(0, 1).
			Render(i18n.T("settings.extensions.common.badge_enabled"))
		lines = append(lines, enabledBadge)
	} else {
		disabledBadge := lipgloss.NewStyle().
			Foreground(lipgloss.Color(th.TextMuted)).
			Render(i18n.T("settings.extensions.common.disabled"))
		lines = append(lines, disabledBadge)
	}
	lines = append(lines, "")

	// Metadata
	metaStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(th.TextMuted))
	labelStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(th.Text))

	if plugin.Manifest.Version != "" {
		lines = append(lines, labelStyle.Render(i18n.T("settings.extensions.common.label_version"))+metaStyle.Render(plugin.Manifest.Version))
	}
	if plugin.Manifest.Author.Name != "" {
		lines = append(lines, labelStyle.Render(i18n.T("settings.extensions.common.label_author"))+metaStyle.Render(plugin.Manifest.Author.Name))
	}
	if plugin.Manifest.Category != "" {
		catBadge := lipgloss.NewStyle().
			Foreground(lipgloss.Color(th.Warning)).
			Render("[" + plugin.Manifest.Category + "]")
		lines = append(lines, labelStyle.Render(i18n.T("settings.extensions.common.label_category"))+catBadge)
	}
	if len(plugin.Manifest.Keywords) > 0 {
		lines = append(lines, labelStyle.Render(i18n.T("settings.extensions.plugins.label_keywords"))+metaStyle.Render(strings.Join(plugin.Manifest.Keywords, ", ")))
	}

	// Source
	source := p.getPluginSource(plugin)
	var srcLabel string
	switch source {
	case "USR":
		srcLabel = i18n.T("settings.extensions.common.source_user")
	case "PRJ":
		srcLabel = i18n.T("settings.extensions.common.source_project")
	case "MKT":
		srcLabel = i18n.T("settings.extensions.common.marketplace")
	default:
		srcLabel = i18n.T("settings.extensions.plugins.source_local")
	}
	lines = append(lines, labelStyle.Render(i18n.T("settings.extensions.common.label_source"))+metaStyle.Render(srcLabel))
	lines = append(lines, "")

	// Description
	pdw := maxInt(4, width-4)
	if plugin.Manifest.Description != "" {
		lines = append(lines, labelStyle.Render(i18n.T("settings.extensions.common.label_description")))
		descStyle := lipgloss.NewStyle().
			Foreground(lipgloss.Color(th.TextMuted)).
			Width(pdw)
		desc := plugin.Manifest.Description
		if len(desc) > pdw*3 {
			desc = desc[:pdw*3-3] + "..."
		}
		lines = append(lines, descStyle.Render(desc))
		lines = append(lines, "")
	}

	// Component counts
	countStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(th.TextMuted))

	if len(plugin.Commands) > 0 {
		lines = append(lines, countStyle.Render(i18n.T("settings.extensions.plugins.commands_count", len(plugin.Commands))))
		// List first few commands
		for i, cmd := range plugin.Commands {
			if i >= 3 {
				lines = append(lines, metaStyle.Render(i18n.T("settings.extensions.common.and_more", len(plugin.Commands)-3)))
				break
			}
			lines = append(lines, metaStyle.Render(fmt.Sprintf("  /%s:%s", plugin.Manifest.Name, cmd.Name)))
		}
	}
	if len(plugin.Agents) > 0 {
		lines = append(lines, countStyle.Render(i18n.T("settings.extensions.plugins.agents_count", len(plugin.Agents))))
	}
	if len(plugin.Skills) > 0 {
		lines = append(lines, countStyle.Render(i18n.T("settings.extensions.plugins.skills_count", len(plugin.Skills))))
	}
	if len(plugin.MCPServers) > 0 {
		lines = append(lines, countStyle.Render(i18n.T("settings.extensions.plugins.mcp_count", len(plugin.MCPServers))))
	}
	if len(plugin.LSPServers) > 0 {
		lines = append(lines, countStyle.Render(i18n.T("settings.extensions.plugins.lsp_count", len(plugin.LSPServers))))
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

// renderDetail renders the plugin detail view
func (p *PluginsSettings) renderDetail(width, height int, state *State, th Theme) string {
	var lines []string

	allPlugins := p.GetPlugins()
	if state.PluginsSelected >= len(allPlugins) || state.PluginsSelected < 0 {
		return p.renderList(width, height, state, th)
	}

	plugin := allPlugins[state.PluginsSelected]

	// Header with back hint
	headerStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(th.Text)).
		Background(lipgloss.Color(th.Primary)).
		Bold(true).
		Width(maxInt(10, width-4)).
		Padding(0, 2)
	lines = append(lines, headerStyle.Render(i18n.T("settings.extensions.plugins.detail_title")))
	lines = append(lines, "")

	// Plugin name
	titleStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(th.Primary)).
		Bold(true)
	lines = append(lines, titleStyle.Render(plugin.Manifest.Name))

	// Metadata
	metaStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(th.TextMuted))

	if plugin.Manifest.Version != "" {
		lines = append(lines, metaStyle.Render(i18n.T("settings.extensions.common.label_version")+plugin.Manifest.Version))
	}
	if plugin.Manifest.Author.Name != "" {
		lines = append(lines, metaStyle.Render(i18n.T("settings.extensions.common.label_author")+plugin.Manifest.Author.Name))
	}
	if plugin.Manifest.Category != "" {
		lines = append(lines, metaStyle.Render(i18n.T("settings.extensions.common.label_category")+plugin.Manifest.Category))
	}
	lines = append(lines, "")

	// Tab bar
	tabNames := []string{
		i18n.T("settings.extensions.common.tab_info"),
		i18n.T("settings.extensions.plugins.tab_commands"),
		i18n.T("settings.extensions.plugins.tab_agents"),
		i18n.T("settings.extensions.plugins.tab_servers"),
	}
	var tabParts []string
	for i, name := range tabNames {
		tabStyle := lipgloss.NewStyle().
			Padding(0, 2)
		if i == state.PluginsDetailTab {
			tabStyle = tabStyle.
				Foreground(lipgloss.Color(th.Text)).
				Background(lipgloss.Color(th.Primary)).
				Bold(true)
		} else {
			tabStyle = tabStyle.
				Foreground(lipgloss.Color(th.TextMuted))
		}
		tabParts = append(tabParts, tabStyle.Render(name))
	}
	tabBar := lipgloss.JoinHorizontal(lipgloss.Top, tabParts...)
	lines = append(lines, tabBar)
	lines = append(lines, strings.Repeat("-", maxInt(1, width-6)))

	// Tab content
	contentHeight := height - len(lines) - 6
	if contentHeight < 5 {
		contentHeight = 5
	}

	switch state.PluginsDetailTab {
	case 0: // Info tab
		if plugin.Manifest.Description != "" {
			lines = append(lines, metaStyle.Render(plugin.Manifest.Description))
		}
		if len(plugin.Manifest.Keywords) > 0 {
			lines = append(lines, "")
			lines = append(lines, metaStyle.Render(i18n.T("settings.extensions.plugins.label_keywords")+strings.Join(plugin.Manifest.Keywords, ", ")))
		}
		if plugin.Manifest.Homepage != "" {
			lines = append(lines, metaStyle.Render(i18n.T("settings.extensions.plugins.label_homepage")+plugin.Manifest.Homepage))
		}
		if plugin.Manifest.Repository != "" {
			lines = append(lines, metaStyle.Render(i18n.T("settings.extensions.plugins.label_repository")+plugin.Manifest.Repository))
		}

	case 1: // Commands tab
		if len(plugin.Commands) > 0 {
			for _, cmd := range plugin.Commands {
				cmdStyle := lipgloss.NewStyle().
					Foreground(lipgloss.Color(th.Primary))
				lines = append(lines, cmdStyle.Render("/"+plugin.Manifest.Name+":"+cmd.Name))
				if cmd.Description != "" {
					lines = append(lines, metaStyle.Render("  "+cmd.Description))
				}
			}
		} else {
			lines = append(lines, metaStyle.Render(i18n.T("settings.extensions.plugins.no_commands")))
		}

	case 2: // Agents tab
		if len(plugin.Agents) > 0 {
			for _, agent := range plugin.Agents {
				agentStyle := lipgloss.NewStyle().
					Foreground(lipgloss.Color(th.Primary))
				lines = append(lines, agentStyle.Render(agent.Name))
				if agent.Description != "" {
					lines = append(lines, metaStyle.Render("  "+agent.Description))
				}
				if agent.Model != "" {
					lines = append(lines, metaStyle.Render(i18n.T("settings.extensions.plugins.label_model")+agent.Model))
				}
			}
		} else {
			lines = append(lines, metaStyle.Render(i18n.T("settings.extensions.plugins.no_agents")))
		}

	case 3: // MCP/LSP tab
		if len(plugin.MCPServers) > 0 {
			lines = append(lines, i18n.T("settings.extensions.plugins.mcp_servers"))
			for _, server := range plugin.MCPServers {
				lines = append(lines, metaStyle.Render("  "+server.Name+" ("+server.Type+")"))
			}
		}
		if len(plugin.LSPServers) > 0 {
			lines = append(lines, i18n.T("settings.extensions.plugins.lsp_servers"))
			for _, server := range plugin.LSPServers {
				lines = append(lines, metaStyle.Render("  "+server.Language+": "+server.Command))
			}
		}
		if len(plugin.MCPServers) == 0 && len(plugin.LSPServers) == 0 {
			lines = append(lines, metaStyle.Render(i18n.T("settings.extensions.plugins.no_servers")))
		}
	}

	// Styled hint bar
	lines = append(lines, "")
	lines = append(lines, p.renderHintBar(width, state, th))

	content := lipgloss.JoinVertical(lipgloss.Left, lines...)

	containerStyle := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color(th.Border)).
		Width(maxInt(10, width-2)).
		Padding(1, 2)

	return containerStyle.Render(content)
}

// renderSearch renders the search view
func (p *PluginsSettings) renderSearch(width, height int, state *State, th Theme) string {
	var lines []string

	// Header
	headerStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(th.Text)).
		Background(lipgloss.Color(th.Primary)).
		Bold(true).
		Width(maxInt(10, width-4)).
		Padding(0, 2)
	lines = append(lines, headerStyle.Render(i18n.T("settings.extensions.plugins.search_title")))
	lines = append(lines, "")

	// Search input
	searchLabelStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(th.Primary)).
		Bold(true)
	lines = append(lines, searchLabelStyle.Render(i18n.T("settings.extensions.common.search_label")))

	searchInputStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(th.Text)).
		Background(lipgloss.Color(th.BGLight)).
		Padding(0, 1).
		Width(maxInt(4, width-10)).
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color(th.Primary))

	inputValue := state.PluginsSearchQuery
	if state.PluginsCursorPos <= len(inputValue) {
		inputValue = inputValue[:state.PluginsCursorPos] + "|" + inputValue[state.PluginsCursorPos:]
	}
	lines = append(lines, searchInputStyle.Render(inputValue))
	lines = append(lines, "")

	// Results
	resultsHeaderStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(th.Primary)).
		Bold(true)
	lines = append(lines, resultsHeaderStyle.Render(i18n.T("settings.extensions.common.results_count", len(p.searchResults))))
	lines = append(lines, "")

	if len(p.searchResults) == 0 {
		emptyStyle := lipgloss.NewStyle().
			Foreground(lipgloss.Color(th.TextMuted)).
			Italic(true)
		if state.PluginsSearchQuery == "" {
			lines = append(lines, emptyStyle.Render(i18n.T("settings.extensions.plugins.search_prompt")))
		} else {
			lines = append(lines, emptyStyle.Render(i18n.T("settings.extensions.common.no_results")))
		}
	} else {
		listHeight := maxInt(3, height-14)

		visibleEnd := minInt(state.PluginsSearchOffset+listHeight, len(p.searchResults))
		for i := state.PluginsSearchOffset; i < visibleEnd; i++ {
			result := p.searchResults[i]
			isSelected := i == state.PluginsSearchSelected
			lines = append(lines, p.renderSearchResult(result, isSelected, maxInt(8, width-8), th))
		}
	}

	// Error message
	if p.errorMessage != "" {
		errStyle := lipgloss.NewStyle().
			Foreground(lipgloss.Color(th.Error)).
			Width(maxInt(4, width-4)).
			Align(lipgloss.Center)
		lines = append(lines, errStyle.Render(p.errorMessage))
	}

	// Hint bar
	lines = append(lines, "")
	lines = append(lines, p.renderHintBar(width, state, th))

	content := lipgloss.JoinVertical(lipgloss.Left, lines...)

	containerStyle := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color(th.Border)).
		Width(maxInt(10, width-2)).
		Padding(1, 2)

	return containerStyle.Render(content)
}

// renderSearchResult renders a single search result
func (p *PluginsSettings) renderSearchResult(result plugins.UnifiedSearchResult, isSelected bool, width int, th Theme) string {
	var parts []string

	// Type badge
	typeColor := th.Primary
	if result.ResultType == "skill" {
		typeColor = th.Success
	}
	typeBadge := lipgloss.NewStyle().
		Foreground(lipgloss.Color(th.BGLight)).
		Background(lipgloss.Color(typeColor)).
		Padding(0, 1).
		Render(strings.ToUpper(result.ResultType[:3]))
	parts = append(parts, typeBadge)

	// Installed indicator
	if result.Installed {
		instStyle := lipgloss.NewStyle().
			Foreground(lipgloss.Color(th.Success))
		parts = append(parts, instStyle.Render("[*]"))
	} else {
		parts = append(parts, "[ ]")
	}

	// Name
	nameColor := th.Text
	if isSelected {
		nameColor = th.Primary
	}
	nameStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(nameColor)).
		Bold(isSelected)
	parts = append(parts, nameStyle.Render(result.Name))

	// Category
	if result.Category != "" {
		catStyle := lipgloss.NewStyle().
			Foreground(lipgloss.Color(th.Warning)).
			Italic(true)
		parts = append(parts, catStyle.Render("["+result.Category+"]"))
	}

	// Version
	if result.Version != "" {
		verStyle := lipgloss.NewStyle().
			Foreground(lipgloss.Color(th.TextMuted))
		parts = append(parts, verStyle.Render("v"+result.Version))
	}

	line := strings.Join(parts, " ")

	// Description for selected
	if isSelected && result.Description != "" {
		desc := result.Description
		if width > 11 && len(desc) > width-8 {
			desc = desc[:width-11] + "..."
		}
		descStyle := lipgloss.NewStyle().
			Foreground(lipgloss.Color(th.TextMuted)).
			Italic(true).
			MarginLeft(4)
		line = lipgloss.JoinVertical(lipgloss.Left, line, descStyle.Render(desc))
	}

	// Highlight selected
	if isSelected {
		return lipgloss.NewStyle().
			Background(lipgloss.Color(th.BGLighter)).
			Width(width).
			Render(line)
	}

	return line
}

// renderMarketplace renders the marketplace sources view
func (p *PluginsSettings) renderMarketplace(width, height int, state *State, th Theme) string {
	var lines []string

	headerStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(th.Text)).
		Background(lipgloss.Color(th.Primary)).
		Bold(true).
		Width(maxInt(10, width-4)).
		Padding(0, 2)
	lines = append(lines, headerStyle.Render(i18n.T("settings.extensions.common.marketplace_sources")))
	lines = append(lines, "")

	if p.marketplace == nil {
		lines = append(lines, i18n.T("settings.extensions.plugins.marketplace_unavailable"))
	} else {
		sources := p.marketplace.GetSources()
		for i, source := range sources {
			isSelected := i == state.PluginsMarketplaceSelected

			var parts []string

			// Status indicator
			if source.Enabled {
				parts = append(parts, lipgloss.NewStyle().
					Foreground(lipgloss.Color(th.Success)).
					Render("[*]"))
			} else {
				parts = append(parts, lipgloss.NewStyle().
					Foreground(lipgloss.Color(th.TextMuted)).
					Render("[ ]"))
			}

			// Name
			nameColor := th.Text
			if isSelected {
				nameColor = th.Primary
			}
			parts = append(parts, lipgloss.NewStyle().
				Foreground(lipgloss.Color(nameColor)).
				Bold(isSelected).
				Render(source.Name))

			// Type badge
			parts = append(parts, lipgloss.NewStyle().
				Foreground(lipgloss.Color(th.TextMuted)).
				Render("["+source.Type+"]"))

			line := strings.Join(parts, " ")

			if isSelected {
				line = lipgloss.NewStyle().
					Background(lipgloss.Color(th.BGLighter)).
					Width(maxInt(8, width-8)).
					Render(line)
			}

			lines = append(lines, line)

			// Show URL for selected
			if isSelected {
				urlStyle := lipgloss.NewStyle().
					Foreground(lipgloss.Color(th.TextMuted)).
					MarginLeft(4)
				lines = append(lines, urlStyle.Render(source.URL))
			}
		}
	}

	// Hint bar
	lines = append(lines, "")
	lines = append(lines, p.renderHintBar(width, state, th))

	content := lipgloss.JoinVertical(lipgloss.Left, lines...)

	containerStyle := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color(th.Border)).
		Width(maxInt(10, width-2)).
		Padding(1, 2)

	return containerStyle.Render(content)
}

// renderHintBar renders the keyboard hints bar
func (p *PluginsSettings) renderHintBar(width int, state *State, th Theme) string {
	var hints []string

	switch state.PluginsState {
	case "search":
		hints = []string{
			p.renderKeyHint("Enter", i18n.T("settings.extensions.action.search"), th),
			p.renderKeyHint("i", i18n.T("settings.extensions.action.install"), th),
			p.renderKeyHint("Esc", i18n.T("settings.extensions.action.back"), th),
		}
	case "detail":
		hints = []string{
			p.renderKeyHint("Tab", i18n.T("settings.extensions.action.switch_tabs"), th),
			p.renderKeyHint("Space", i18n.T("settings.extensions.action.toggle"), th),
			p.renderKeyHint("u", i18n.T("settings.extensions.action.uninstall"), th),
			p.renderKeyHint("Esc", i18n.T("settings.extensions.action.back"), th),
		}
	case "marketplace":
		hints = []string{
			p.renderKeyHint("Space", i18n.T("settings.extensions.action.toggle"), th),
			p.renderKeyHint("u", i18n.T("settings.extensions.action.update"), th),
			p.renderKeyHint("a", i18n.T("settings.extensions.action.add"), th),
			p.renderKeyHint("Esc", i18n.T("settings.extensions.action.back"), th),
		}
	default:
		if p.viewMode == "available" {
			hints = []string{
				p.renderKeyHint("1", i18n.T("settings.extensions.common.installed"), th),
				p.renderKeyHint("2", i18n.T("settings.extensions.common.available"), th),
				p.renderKeyHint("3", i18n.T("settings.extensions.common.sources"), th),
				p.renderKeyHint("Enter", i18n.T("settings.extensions.action.install"), th),
				p.renderKeyHint("r", i18n.T("settings.extensions.action.refresh"), th),
			}
		} else if p.viewMode == "sources" {
			hints = []string{
				p.renderKeyHint("1", i18n.T("settings.extensions.common.installed"), th),
				p.renderKeyHint("2", i18n.T("settings.extensions.common.available"), th),
				p.renderKeyHint("3", i18n.T("settings.extensions.common.sources"), th),
				p.renderKeyHint("Space", i18n.T("settings.extensions.action.toggle"), th),
				p.renderKeyHint("a", i18n.T("settings.extensions.action.add"), th),
				p.renderKeyHint("d", i18n.T("settings.extensions.action.delete"), th),
			}
		} else {
			hints = []string{
				p.renderKeyHint("1", i18n.T("settings.extensions.common.installed"), th),
				p.renderKeyHint("2", i18n.T("settings.extensions.common.available"), th),
				p.renderKeyHint("3", i18n.T("settings.extensions.common.sources"), th),
				p.renderKeyHint("Space", i18n.T("settings.extensions.action.toggle"), th),
				p.renderKeyHint("d", i18n.T("settings.extensions.common.details"), th),
				p.renderKeyHint("r", i18n.T("settings.extensions.action.refresh"), th),
			}
		}
	}

	hintLine := strings.Join(hints, "  ")
	return lipgloss.NewStyle().
		Width(maxInt(4, width-4)).
		Align(lipgloss.Center).
		Render(hintLine)
}

// renderKeyHint renders a single styled key hint
func (p *PluginsSettings) renderKeyHint(key, action string, th Theme) string {
	keyStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(th.Text)).
		Background(lipgloss.Color(th.BGLighter)).
		Padding(0, 1)

	actionStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(th.TextMuted)).
		MarginRight(2)

	return keyStyle.Render(key) + actionStyle.Render(" "+action)
}

// HandleKey handles keyboard input for plugins settings
