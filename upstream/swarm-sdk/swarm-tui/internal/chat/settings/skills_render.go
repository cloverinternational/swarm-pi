package settings

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/skills"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/i18n"
)

func (s *SkillsSettings) Render(width, height int, state *State, th Theme) string {
	if width < 40 || height < 15 {
		return s.renderMinimal(width, height, th)
	}

	switch state.SkillsState {
	case "detail":
		return s.renderDetail(width, height, state, th)
	case "search":
		return s.renderSearch(width, height, state, th)
	case "add_source":
		return s.renderAddSource(width, height, state, th)
	default:
		return s.renderList(width, height, state, th)
	}
}

// renderMinimal renders a minimal view for small terminals
func (s *SkillsSettings) renderMinimal(width, height int, th Theme) string {
	msg := i18n.T("settings.extensions.common.terminal_too_small")
	return lipgloss.NewStyle().
		Width(width).
		Height(height).
		Foreground(lipgloss.Color(th.TextMuted)).
		Align(lipgloss.Center, lipgloss.Center).
		Render(msg)
}

// renderList renders the main skills list view with split-panel layout
func (s *SkillsSettings) renderList(width, height int, state *State, th Theme) string {
	var sections []string

	// Title with styled badges
	title := s.renderTitle(width, th)
	sections = append(sections, title)
	sections = append(sections, "")

	// Calculate content area height
	contentHeight := maxInt(5, height-10)

	// Error/success message area
	var msgArea string
	if s.errorMessage != "" {
		errStyle := lipgloss.NewStyle().
			Foreground(lipgloss.Color(th.Error)).
			Width(maxInt(10, width-6)).
			Align(lipgloss.Center)
		msgArea = errStyle.Render(s.errorMessage)
		contentHeight = maxInt(5, contentHeight-1)
	} else if s.successMsg != "" {
		successStyle := lipgloss.NewStyle().
			Foreground(lipgloss.Color(th.Success)).
			Width(maxInt(10, width-6)).
			Align(lipgloss.Center)
		msgArea = successStyle.Render(s.successMsg)
		contentHeight = maxInt(5, contentHeight-1)
	}

	// Render split panel (list + detail)
	panelContent := s.renderListAndDetail(maxInt(20, width-6), contentHeight, state, th)
	sections = append(sections, panelContent)

	// Add message if present
	if msgArea != "" {
		sections = append(sections, msgArea)
	}

	// Hint bar
	sections = append(sections, "")
	sections = append(sections, s.renderHintBar(width, state, th))

	content := lipgloss.JoinVertical(lipgloss.Left, sections...)

	// Container with rounded border
	containerStyle := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color(th.Border)).
		Width(maxInt(20, width-2)).
		Padding(1, minInt(2, width/10))

	return containerStyle.Render(content)
}

// renderTitle renders the section title with view mode tabs
func (s *SkillsSettings) renderTitle(width int, th Theme) string {
	allSkills := s.GetSkills()
	activeSkills := s.GetActiveSkills()
	marketplaceCount := len(s.marketplaceEntries)
	sourcesCount := len(s.marketplaceSources)

	titleStyle := lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color(th.Primary))

	title := titleStyle.Render(i18n.T("settings.extensions.skills.title"))

	// View mode tabs - 3 tabs now
	installedTabStyle := lipgloss.NewStyle().Padding(0, 1)
	availableTabStyle := lipgloss.NewStyle().Padding(0, 1)
	sourcesTabStyle := lipgloss.NewStyle().Padding(0, 1)

	switch s.viewMode {
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

	installedTab := installedTabStyle.Render(i18n.T("settings.extensions.common.installed_count", len(allSkills)))
	availableTab := availableTabStyle.Render(i18n.T("settings.extensions.common.available_count", marketplaceCount))
	sourcesTab := sourcesTabStyle.Render(i18n.T("settings.extensions.common.sources_count", sourcesCount))

	// Loading indicator
	loadingIndicator := ""
	if s.marketplaceLoading {
		loadingIndicator = lipgloss.NewStyle().
			Foreground(lipgloss.Color(th.Warning)).
			Render(" ⟳")
	}

	tabs := installedTab + " " + availableTab + " " + sourcesTab + loadingIndicator

	// Stats line
	var statsLine string
	switch s.viewMode {
	case "installed":
		activeBadge := lipgloss.NewStyle().
			Foreground(lipgloss.Color(th.Success)).
			Render(i18n.T("settings.extensions.skills.active_count", len(activeSkills)))
		statsLine = activeBadge
	case "available":
		featuredCount := 0
		for _, e := range s.marketplaceEntries {
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
		for _, src := range s.marketplaceSources {
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

// renderSkillLine renders a single skill entry with source badge
func (s *SkillsSettings) renderSkillLine(skill *skills.Skill, isSelected, isActive bool, width int, th Theme) string {
	var parts []string

	// Source badge (SYS/USR/PRJ/POL)
	source := s.getSkillSource(skill)
	var srcColor string
	switch source {
	case "USR":
		srcColor = th.Success
	case "PRJ":
		srcColor = th.Warning
	case "POL":
		srcColor = th.Error // Policy/managed skills use error color to indicate enterprise control
	default:
		srcColor = th.Primary
	}
	srcBadge := lipgloss.NewStyle().
		Foreground(lipgloss.Color(th.BGLight)).
		Background(lipgloss.Color(srcColor)).
		Padding(0, 1).
		Render(source)
	parts = append(parts, srcBadge)

	// Status indicator
	if isActive {
		activeStyle := lipgloss.NewStyle().
			Foreground(lipgloss.Color(th.Success)).
			Bold(true)
		parts = append(parts, activeStyle.Render("[*]"))
	} else {
		parts = append(parts, lipgloss.NewStyle().
			Foreground(lipgloss.Color(th.TextMuted)).
			Render("[ ]"))
	}

	// Name with icon
	nameColor := th.Text
	if isSelected {
		nameColor = th.Primary
	}
	nameStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(nameColor)).
		Bold(isSelected)

	name := skill.Metadata.Name
	if skill.Metadata.Icon != "" {
		name = skill.Metadata.Icon + " " + name
	}
	parts = append(parts, nameStyle.Render(name))

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

// renderListAndDetail renders the split panel layout with skills list and detail
func (s *SkillsSettings) renderListAndDetail(width, height int, state *State, th Theme) string {
	listHeight := maxInt(3, height-2)

	// Collapse to single-pane at narrow widths
	if width < 60 {
		listWidth := maxInt(15, width-2)
		var listPanel string
		switch s.viewMode {
		case "available":
			listPanel = s.renderMarketplaceList(listWidth, listHeight, state, th)
		case "sources":
			listPanel = s.renderSourcesList(listWidth, listHeight, state, th)
		default:
			allSkills := s.GetSkills()
			activeSkills := s.GetActiveSkills()
			activeMap := make(map[string]bool)
			for _, sk := range activeSkills {
				activeMap[sk.Metadata.Name] = true
			}
			var listLines []string
			listHeaderStyle := lipgloss.NewStyle().
				Foreground(lipgloss.Color(th.Primary)).
				Bold(true)
			listLines = append(listLines, listHeaderStyle.Render(i18n.T("settings.extensions.common.installed")))
			listLines = append(listLines, strings.Repeat("─", maxInt(1, listWidth-2)))
			if len(allSkills) == 0 {
				emptyStyle := lipgloss.NewStyle().
					Foreground(lipgloss.Color(th.TextMuted)).
					Italic(true)
				listLines = append(listLines, emptyStyle.Render(i18n.T("settings.extensions.skills.no_installed")))
			} else {
				visibleEnd := minInt(state.SkillsScrollOffset+listHeight-3, len(allSkills))
				for i := state.SkillsScrollOffset; i < visibleEnd; i++ {
					skill := allSkills[i]
					isSelected := i == state.SkillsSelected
					isActive := activeMap[skill.Metadata.Name]
					listLines = append(listLines, s.renderSkillLine(skill, isSelected, isActive, maxInt(8, listWidth-2), th))
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

	switch s.viewMode {
	case "available":
		// Show marketplace entries
		listPanel = s.renderMarketplaceList(listWidth, listHeight, state, th)
		detailPanel = s.renderMarketplaceDetails(state, detailWidth, listHeight, th)
	case "sources":
		// Show marketplace sources
		listPanel = s.renderSourcesList(listWidth, listHeight, state, th)
		detailPanel = s.renderSourcesDetails(state, detailWidth, listHeight, th)
	default:
		// Show installed skills
		allSkills := s.GetSkills()
		activeSkills := s.GetActiveSkills()

		// Build active skills map
		activeMap := make(map[string]bool)
		for _, sk := range activeSkills {
			activeMap[sk.Metadata.Name] = true
		}

		// Render skills list
		var listLines []string
		listHeaderStyle := lipgloss.NewStyle().
			Foreground(lipgloss.Color(th.Primary)).
			Bold(true)
		listLines = append(listLines, listHeaderStyle.Render(i18n.T("settings.extensions.common.installed")))
		listLines = append(listLines, strings.Repeat("─", maxInt(1, listWidth-2)))

		if len(allSkills) == 0 {
			emptyStyle := lipgloss.NewStyle().
				Foreground(lipgloss.Color(th.TextMuted)).
				Italic(true)
			listLines = append(listLines, emptyStyle.Render(i18n.T("settings.extensions.skills.no_installed")))
			listLines = append(listLines, "")
			listLines = append(listLines, emptyStyle.Render(i18n.T("settings.extensions.common.press_available")))
			listLines = append(listLines, emptyStyle.Render(i18n.T("settings.extensions.common.press_search")))
		} else {
			visibleEnd := minInt(state.SkillsScrollOffset+listHeight-3, len(allSkills))
			for i := state.SkillsScrollOffset; i < visibleEnd; i++ {
				skill := allSkills[i]
				isSelected := i == state.SkillsSelected
				isActive := activeMap[skill.Metadata.Name]
				listLines = append(listLines, s.renderSkillLine(skill, isSelected, isActive, listWidth-2, th))
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
		detailPanel = s.renderSkillDetails(allSkills, state, detailWidth, listHeight, th)
	}

	// Join panels horizontally
	separator := lipgloss.NewStyle().
		Foreground(lipgloss.Color(th.Border)).
		Render(strings.Repeat("│\n", listHeight))

	return lipgloss.JoinHorizontal(lipgloss.Top, listPanel, " ", separator, " ", detailPanel)
}

// renderMarketplaceList renders the marketplace entries list panel
func (s *SkillsSettings) renderMarketplaceList(listWidth, listHeight int, state *State, th Theme) string {
	entries := s.marketplaceEntries

	var listLines []string
	listHeaderStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(th.Primary)).
		Bold(true)
	listLines = append(listLines, listHeaderStyle.Render(i18n.T("settings.extensions.common.available")))
	listLines = append(listLines, strings.Repeat("─", maxInt(1, listWidth-2)))

	if s.marketplaceLoading {
		loadingStyle := lipgloss.NewStyle().
			Foreground(lipgloss.Color(th.Warning)).
			Italic(true)
		listLines = append(listLines, loadingStyle.Render(i18n.T("settings.extensions.common.loading")))
	} else if len(entries) == 0 {
		emptyStyle := lipgloss.NewStyle().
			Foreground(lipgloss.Color(th.TextMuted)).
			Italic(true)
		listLines = append(listLines, emptyStyle.Render(i18n.T("settings.extensions.skills.no_found")))
		listLines = append(listLines, "")
		listLines = append(listLines, emptyStyle.Render(i18n.T("settings.extensions.common.press_refresh")))
	} else {
		visibleEnd := minInt(state.SkillsScrollOffset+listHeight-3, len(entries))
		for i := state.SkillsScrollOffset; i < visibleEnd; i++ {
			entry := entries[i]
			isSelected := i == state.SkillsSelected
			listLines = append(listLines, s.renderMarketplaceEntryLine(entry, isSelected, listWidth-2, th))
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
func (s *SkillsSettings) renderMarketplaceEntryLine(entry skills.PluginEntry, isSelected bool, width int, th Theme) string {
	var parts []string

	// Source badge
	srcBadge := lipgloss.NewStyle().
		Foreground(lipgloss.Color(th.BGLight)).
		Background(lipgloss.Color(th.Primary)).
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

	// Check if already installed
	installed := false
	if s.loader != nil {
		installed = s.loader.Database.IsInstalled(entry.Name)
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
func (s *SkillsSettings) renderMarketplaceDetails(state *State, width, height int, th Theme) string {
	entries := s.marketplaceEntries
	var lines []string

	headerStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(th.Primary)).
		Bold(true)
	lines = append(lines, headerStyle.Render(i18n.T("settings.extensions.common.details")))
	lines = append(lines, strings.Repeat("─", maxInt(1, width-2)))

	if len(entries) == 0 || state.SkillsSelected >= len(entries) {
		emptyStyle := lipgloss.NewStyle().
			Foreground(lipgloss.Color(th.TextMuted)).
			Italic(true)
		lines = append(lines, "")
		if s.marketplaceLoading {
			lines = append(lines, emptyStyle.Render(i18n.T("settings.extensions.common.loading_marketplace")))
		} else {
			lines = append(lines, emptyStyle.Render(i18n.T("settings.extensions.skills.select_details")))
		}
		for len(lines) < height {
			lines = append(lines, "")
		}
		return lipgloss.NewStyle().
			Width(width).
			Height(height).
			Render(lipgloss.JoinVertical(lipgloss.Left, lines...))
	}

	entry := entries[state.SkillsSelected]

	// Skill name
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
	installed := false
	if s.loader != nil {
		installed = s.loader.Database.IsInstalled(entry.Name)
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
	if entry.Repository != "" {
		lines = append(lines, labelStyle.Render(i18n.T("settings.extensions.common.label_install_from")))
		srcStyle := lipgloss.NewStyle().
			Foreground(lipgloss.Color(th.TextMuted)).
			Width(dw)
		src := entry.Repository
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
func (s *SkillsSettings) renderSourcesList(listWidth, listHeight int, state *State, th Theme) string {
	sources := s.marketplaceSources

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
		visibleEnd := minInt(state.SkillsScrollOffset+listHeight-3, len(sources))
		for i := state.SkillsScrollOffset; i < visibleEnd; i++ {
			source := sources[i]
			isSelected := i == state.SkillsSelected
			listLines = append(listLines, s.renderSourceLine(source, isSelected, listWidth-2, th))
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
func (s *SkillsSettings) renderSourceLine(source skills.MarketplaceSource, isSelected bool, width int, th Theme) string {
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
	case "github-search":
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
func (s *SkillsSettings) renderSourcesDetails(state *State, width, height int, th Theme) string {
	sources := s.marketplaceSources
	var lines []string

	headerStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(th.Primary)).
		Bold(true)
	lines = append(lines, headerStyle.Render(i18n.T("settings.extensions.common.source_details")))
	lines = append(lines, strings.Repeat("─", maxInt(1, width-2)))

	if len(sources) == 0 || state.SkillsSelected >= len(sources) {
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

	source := sources[state.SkillsSelected]

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
func (s *SkillsSettings) renderAddSource(width, height int, state *State, th Theme) string {
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
	if s.addSourceState == "name" {
		lines = append(lines, labelStyle.Render(nameLabel)+activeStyle.Render(s.newSourceName+"_"))
	} else {
		lines = append(lines, labelStyle.Render(nameLabel)+valueStyle.Render(s.newSourceName))
	}

	// URL field
	urlLabel := i18n.T("settings.extensions.common.label_url_padded")
	if s.addSourceState == "url" {
		lines = append(lines, labelStyle.Render(urlLabel)+activeStyle.Render(s.newSourceURL+"_"))
	} else {
		lines = append(lines, labelStyle.Render(urlLabel)+valueStyle.Render(s.newSourceURL))
	}

	// Type field
	typeLabel := i18n.T("settings.extensions.common.label_type")
	typeOptions := []string{"http", "github-search", "awesome-list"}
	var typeDisplay strings.Builder
	for i, opt := range typeOptions {
		if s.addSourceState == "type" && opt == s.newSourceType {
			typeDisplay.WriteString(activeStyle.Render("["+opt+"]") + " ")
		} else if opt == s.newSourceType {
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
	lines = append(lines, valueStyle.Render(i18n.T("settings.extensions.skills.http_description")))
	lines = append(lines, valueStyle.Render(i18n.T("settings.extensions.skills.github_description")))
	lines = append(lines, valueStyle.Render(i18n.T("settings.extensions.skills.awesome_description")))

	// Error message
	if s.errorMessage != "" {
		lines = append(lines, "")
		errStyle := lipgloss.NewStyle().
			Foreground(lipgloss.Color(th.Error))
		lines = append(lines, errStyle.Render(s.errorMessage))
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

// renderSkillDetails renders the detail panel for the selected skill
func (s *SkillsSettings) renderSkillDetails(allSkills []*skills.Skill, state *State, width, height int, th Theme) string {
	var lines []string

	headerStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(th.Primary)).
		Bold(true)
	lines = append(lines, headerStyle.Render(i18n.T("settings.extensions.common.details")))
	lines = append(lines, strings.Repeat("─", maxInt(1, width-2)))

	if len(allSkills) == 0 || state.SkillsSelected >= len(allSkills) {
		emptyStyle := lipgloss.NewStyle().
			Foreground(lipgloss.Color(th.TextMuted)).
			Italic(true)
		lines = append(lines, "")
		lines = append(lines, emptyStyle.Render(i18n.T("settings.extensions.skills.select_details")))
		for len(lines) < height {
			lines = append(lines, "")
		}
		return lipgloss.NewStyle().
			Width(width).
			Height(height).
			Render(lipgloss.JoinVertical(lipgloss.Left, lines...))
	}

	skill := allSkills[state.SkillsSelected]

	// Active skills map
	activeSkills := s.GetActiveSkills()
	isActive := false
	for _, sk := range activeSkills {
		if sk.Metadata.Name == skill.Metadata.Name {
			isActive = true
			break
		}
	}

	// Skill name with icon
	nameStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(th.Primary)).
		Bold(true)
	name := skill.Metadata.Name
	if skill.Metadata.Icon != "" {
		name = skill.Metadata.Icon + " " + name
	}
	lines = append(lines, nameStyle.Render(name))

	// Status badge
	if isActive {
		activeBadge := lipgloss.NewStyle().
			Foreground(lipgloss.Color(th.Text)).
			Background(lipgloss.Color(th.Success)).
			Padding(0, 1).
			Render(i18n.T("settings.extensions.skills.badge_active"))
		lines = append(lines, activeBadge)
	} else {
		inactiveBadge := lipgloss.NewStyle().
			Foreground(lipgloss.Color(th.TextMuted)).
			Render(i18n.T("settings.extensions.skills.inactive"))
		lines = append(lines, inactiveBadge)
	}
	lines = append(lines, "")

	// Metadata
	metaStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(th.TextMuted))
	labelStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(th.Text))

	if skill.Metadata.Version != "" {
		lines = append(lines, labelStyle.Render(i18n.T("settings.extensions.common.label_version"))+metaStyle.Render(skill.Metadata.Version))
	}
	if skill.Metadata.Author != "" {
		lines = append(lines, labelStyle.Render(i18n.T("settings.extensions.common.label_author"))+metaStyle.Render(skill.Metadata.Author))
	}
	if skill.Metadata.Category != "" {
		catBadge := lipgloss.NewStyle().
			Foreground(lipgloss.Color(th.Warning)).
			Render("[" + skill.Metadata.Category + "]")
		lines = append(lines, labelStyle.Render(i18n.T("settings.extensions.common.label_category"))+catBadge)
	}
	if len(skill.Metadata.Tags) > 0 {
		lines = append(lines, labelStyle.Render(i18n.T("settings.extensions.common.label_tags"))+metaStyle.Render(strings.Join(skill.Metadata.Tags, ", ")))
	}

	// Source
	source := s.getSkillSource(skill)
	var srcLabel string
	switch source {
	case "USR":
		srcLabel = i18n.T("settings.extensions.common.source_user")
	case "PRJ":
		srcLabel = i18n.T("settings.extensions.common.source_project")
	case "POL":
		srcLabel = i18n.T("settings.extensions.skills.source_policy")
	default:
		srcLabel = i18n.T("settings.extensions.skills.source_system")
	}
	lines = append(lines, labelStyle.Render(i18n.T("settings.extensions.common.label_source"))+metaStyle.Render(srcLabel))
	lines = append(lines, "")

	// Description
	if skill.Metadata.Description != "" {
		lines = append(lines, labelStyle.Render(i18n.T("settings.extensions.common.label_description")))
		skdw := maxInt(4, width-4)
		descStyle := lipgloss.NewStyle().
			Foreground(lipgloss.Color(th.TextMuted)).
			Width(skdw)
		desc := skill.Metadata.Description
		if len(desc) > skdw*3 {
			desc = desc[:skdw*3-3] + "..."
		}
		lines = append(lines, descStyle.Render(desc))
		lines = append(lines, "")
	}

	// Scripts/References counts
	if len(skill.Scripts) > 0 || len(skill.References) > 0 {
		countStyle := lipgloss.NewStyle().
			Foreground(lipgloss.Color(th.TextMuted))
		if len(skill.Scripts) > 0 {
			lines = append(lines, countStyle.Render(i18n.T("settings.extensions.skills.scripts_count", len(skill.Scripts))))
		}
		if len(skill.References) > 0 {
			lines = append(lines, countStyle.Render(i18n.T("settings.extensions.skills.references_count", len(skill.References))))
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

// renderDetail renders the skill detail view
func (s *SkillsSettings) renderDetail(width, height int, state *State, th Theme) string {
	var lines []string

	allSkills := s.GetSkills()
	if state.SkillsSelected >= len(allSkills) || state.SkillsSelected < 0 {
		return s.renderList(width, height, state, th)
	}

	skill := allSkills[state.SkillsSelected]

	// Header with back hint
	headerStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(th.Text)).
		Background(lipgloss.Color(th.Primary)).
		Bold(true).
		Width(maxInt(10, width-4)).
		Padding(0, 2)
	lines = append(lines, headerStyle.Render(i18n.T("settings.extensions.skills.detail_title")))
	lines = append(lines, "")

	// Skill name and icon
	titleStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(th.Primary)).
		Bold(true)
	title := skill.Metadata.Name
	if skill.Metadata.Icon != "" {
		title = skill.Metadata.Icon + " " + title
	}
	lines = append(lines, titleStyle.Render(title))

	// Metadata
	metaStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(th.TextMuted))

	if skill.Metadata.Version != "" {
		lines = append(lines, metaStyle.Render(i18n.T("settings.extensions.common.label_version")+skill.Metadata.Version))
	}
	if skill.Metadata.Author != "" {
		lines = append(lines, metaStyle.Render(i18n.T("settings.extensions.common.label_author")+skill.Metadata.Author))
	}
	if skill.Metadata.Category != "" {
		lines = append(lines, metaStyle.Render(i18n.T("settings.extensions.common.label_category")+skill.Metadata.Category))
	}
	if len(skill.Metadata.Tags) > 0 {
		lines = append(lines, metaStyle.Render(i18n.T("settings.extensions.common.label_tags")+strings.Join(skill.Metadata.Tags, ", ")))
	}
	lines = append(lines, "")

	// Description
	detDw := maxInt(4, width-10)
	if skill.Metadata.Description != "" {
		descHeaderStyle := lipgloss.NewStyle().
			Foreground(lipgloss.Color(th.Primary)).
			Bold(true)
		lines = append(lines, descHeaderStyle.Render(i18n.T("settings.extensions.common.label_description")))

		descStyle := lipgloss.NewStyle().
			Foreground(lipgloss.Color(th.Text)).
			Width(detDw)
		lines = append(lines, descStyle.Render(skill.Metadata.Description))
		lines = append(lines, "")
	}

	// Tab bar: Info | Scripts | References
	tabNames := []string{
		i18n.T("settings.extensions.common.tab_info"),
		i18n.T("settings.extensions.skills.tab_scripts"),
		i18n.T("settings.extensions.skills.tab_references"),
	}
	var tabParts []string
	for i, name := range tabNames {
		tabStyle := lipgloss.NewStyle().
			Padding(0, 2)
		if i == state.SkillsDetailTab {
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

	switch state.SkillsDetailTab {
	case 0: // Info tab - show instructions
		if skill.Instructions != "" {
			instrStyle := lipgloss.NewStyle().
				Foreground(lipgloss.Color(th.Text)).
				Width(detDw)
			instr := skill.Instructions
			if detDw*contentHeight > 3 && len(instr) > detDw*contentHeight {
				instr = instr[:detDw*contentHeight-3] + "..."
			}
			lines = append(lines, instrStyle.Render(instr))
		} else {
			lines = append(lines, metaStyle.Render(i18n.T("settings.extensions.skills.no_instructions")))
		}

	case 1: // Scripts tab
		if len(skill.Scripts) > 0 {
			for _, script := range skill.Scripts {
				scriptStyle := lipgloss.NewStyle().
					Foreground(lipgloss.Color(th.Primary))
				lines = append(lines, scriptStyle.Render("* "+script.Name))
				if script.Description != "" {
					lines = append(lines, metaStyle.Render("  "+script.Description))
				}
				if script.Language != "" {
					lines = append(lines, metaStyle.Render(i18n.T("settings.extensions.skills.label_language")+script.Language))
				}
			}
		} else {
			lines = append(lines, metaStyle.Render(i18n.T("settings.extensions.skills.no_scripts")))
		}

	case 2: // References tab
		if len(skill.References) > 0 {
			for _, ref := range skill.References {
				refStyle := lipgloss.NewStyle().
					Foreground(lipgloss.Color(th.Primary))
				lines = append(lines, refStyle.Render("* "+ref.Name))
				if ref.Description != "" {
					lines = append(lines, metaStyle.Render("  "+ref.Description))
				}
				if ref.Format != "" {
					lines = append(lines, metaStyle.Render(i18n.T("settings.extensions.skills.label_format")+ref.Format))
				}
			}
		} else {
			lines = append(lines, metaStyle.Render(i18n.T("settings.extensions.skills.no_references")))
		}
	}

	// Styled hint bar
	lines = append(lines, "")
	lines = append(lines, s.renderHintBar(width, state, th))

	content := lipgloss.JoinVertical(lipgloss.Left, lines...)

	containerStyle := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color(th.Border)).
		Width(maxInt(10, width-2)).
		Padding(1, 2)

	return containerStyle.Render(content)
}

// renderSearch renders the search view
func (s *SkillsSettings) renderSearch(width, height int, state *State, th Theme) string {
	var lines []string

	// Header
	headerStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(th.Text)).
		Background(lipgloss.Color(th.Primary)).
		Bold(true).
		Width(maxInt(10, width-4)).
		Padding(0, 2)
	lines = append(lines, headerStyle.Render(i18n.T("settings.extensions.skills.search_title")))
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

	inputValue := state.SkillsSearchQuery
	if state.SkillsCursorPos <= len(inputValue) {
		inputValue = inputValue[:state.SkillsCursorPos] + "|" + inputValue[state.SkillsCursorPos:]
	}
	lines = append(lines, searchInputStyle.Render(inputValue))
	lines = append(lines, "")

	// Results
	resultsHeaderStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(th.Primary)).
		Bold(true)
	lines = append(lines, resultsHeaderStyle.Render(i18n.T("settings.extensions.common.results_count", len(s.searchResults))))
	lines = append(lines, "")

	if len(s.searchResults) == 0 {
		if state.SkillsSearchQuery == "" {
			emptyStyle := lipgloss.NewStyle().
				Foreground(lipgloss.Color(th.TextMuted)).
				Italic(true)
			lines = append(lines, emptyStyle.Render(i18n.T("settings.extensions.skills.search_prompt")))
		} else {
			emptyStyle := lipgloss.NewStyle().
				Foreground(lipgloss.Color(th.TextMuted)).
				Italic(true)
			lines = append(lines, emptyStyle.Render(i18n.T("settings.extensions.common.no_results")))
		}
	} else {
		// Calculate visible area
		listHeight := maxInt(3, height-14)

		visibleEnd := minInt(state.SkillsSearchOffset+listHeight, len(s.searchResults))
		for i := state.SkillsSearchOffset; i < visibleEnd; i++ {
			result := s.searchResults[i]
			isSelected := i == state.SkillsSearchSelected
			lines = append(lines, s.renderSearchResult(result, isSelected, maxInt(8, width-8), th))
		}
	}

	// Error message
	if s.errorMessage != "" {
		errStyle := lipgloss.NewStyle().
			Foreground(lipgloss.Color(th.Error)).
			Width(maxInt(4, width-4)).
			Align(lipgloss.Center)
		lines = append(lines, errStyle.Render(s.errorMessage))
	}

	// Styled hint bar
	lines = append(lines, "")
	lines = append(lines, s.renderHintBar(width, state, th))

	content := lipgloss.JoinVertical(lipgloss.Left, lines...)

	containerStyle := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color(th.Border)).
		Width(maxInt(10, width-2)).
		Padding(1, 2)

	return containerStyle.Render(content)
}

// renderSearchResult renders a single search result
func (s *SkillsSettings) renderSearchResult(result skills.SkillSearchResult, isSelected bool, width int, th Theme) string {
	// Build the line
	var parts []string

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

// renderKeyHint renders a single styled key hint
func (s *SkillsSettings) renderKeyHint(key, action string, th Theme) string {
	keyStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(th.Text)).
		Background(lipgloss.Color(th.BGLighter)).
		Padding(0, 1)

	actionStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(th.TextMuted)).
		MarginRight(2)

	return keyStyle.Render(key) + actionStyle.Render(" "+action)
}

// renderHintBar renders the styled keyboard hints bar
func (s *SkillsSettings) renderHintBar(width int, state *State, th Theme) string {
	var hints []string

	switch state.SkillsState {
	case "search":
		hints = []string{
			s.renderKeyHint("Enter", i18n.T("settings.extensions.action.search"), th),
			s.renderKeyHint("Esc", i18n.T("settings.extensions.action.back"), th),
		}
	case "detail":
		hints = []string{
			s.renderKeyHint("Tab", i18n.T("settings.extensions.action.switch_tabs"), th),
			s.renderKeyHint("Space", i18n.T("settings.extensions.action.toggle"), th),
			s.renderKeyHint("u", i18n.T("settings.extensions.action.uninstall"), th),
			s.renderKeyHint("Esc", i18n.T("settings.extensions.action.back"), th),
		}
	case "add_source":
		hints = []string{
			s.renderKeyHint("Tab", i18n.T("settings.extensions.action.next_field"), th),
			s.renderKeyHint("Enter", i18n.T("settings.extensions.action.save"), th),
			s.renderKeyHint("Esc", i18n.T("settings.extensions.action.cancel"), th),
		}
	default:
		switch s.viewMode {
		case "available":
			hints = []string{
				s.renderKeyHint("1", i18n.T("settings.extensions.common.installed"), th),
				s.renderKeyHint("2", i18n.T("settings.extensions.common.available"), th),
				s.renderKeyHint("3", i18n.T("settings.extensions.common.sources"), th),
				s.renderKeyHint("Enter", i18n.T("settings.extensions.action.install"), th),
				s.renderKeyHint("/", i18n.T("settings.extensions.action.search"), th),
				s.renderKeyHint("r", i18n.T("settings.extensions.action.refresh"), th),
			}
		case "sources":
			hints = []string{
				s.renderKeyHint("1", i18n.T("settings.extensions.common.installed"), th),
				s.renderKeyHint("2", i18n.T("settings.extensions.common.available"), th),
				s.renderKeyHint("3", i18n.T("settings.extensions.common.sources"), th),
				s.renderKeyHint("Enter", i18n.T("settings.extensions.action.toggle"), th),
				s.renderKeyHint("a", i18n.T("settings.extensions.action.add"), th),
				s.renderKeyHint("d", i18n.T("settings.extensions.action.delete"), th),
				s.renderKeyHint("r", i18n.T("settings.extensions.action.refresh"), th),
			}
		default:
			hints = []string{
				s.renderKeyHint("1", i18n.T("settings.extensions.common.installed"), th),
				s.renderKeyHint("2", i18n.T("settings.extensions.common.available"), th),
				s.renderKeyHint("3", i18n.T("settings.extensions.common.sources"), th),
				s.renderKeyHint("Space", i18n.T("settings.extensions.action.toggle"), th),
				s.renderKeyHint("d", i18n.T("settings.extensions.common.details"), th),
				s.renderKeyHint("r", i18n.T("settings.extensions.action.refresh"), th),
			}
		}
	}

	hintLine := strings.Join(hints, "  ")
	return lipgloss.NewStyle().
		Width(maxInt(4, width-4)).
		Align(lipgloss.Center).
		Render(hintLine)
}

// HandleKey handles keyboard input for skills settings
