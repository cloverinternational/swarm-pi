package commands

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/i18n"
	zone "github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/ui/zone"
)

// View renders the model picker UI
func (m *ModelCommand) View() string {
	if !m.interactive {
		return ""
	}

	m.linkTargets = nil
	var content string

	switch m.state {
	case "menu":
		content = m.renderMainMenu()
	case "provider":
		content = m.renderProviderPicker()
	case "provider_models":
		content = m.renderProviderModelPicker()
	case "model":
		content = m.renderModelPicker()
	case "alias_variants":
		content = m.renderAliasVariantPicker()
	case "agent":
		content = m.renderAgentConfig()
	}

	if m.height > 0 && (m.state == "provider_models" || m.state == "model" || m.state == "alias_variants") {
		content = padContentToHeight(content, m.height)
	}

	// Center on screen if we have height (keep full-screen layout for model browser)
	if m.height > 0 && (m.state == "menu" || m.state == "provider" || m.state == "agent") {
		lines := strings.Count(content, "\n") + 1
		topPad := (m.height - lines) / 2
		if topPad > 0 {
			content = strings.Repeat("\n", topPad) + content
		}
	}

	return content
}

func (m *ModelCommand) HandleMouseClick(msg tea.Mouse) (tea.Cmd, bool) {
	if len(m.linkTargets) == 0 {
		return nil, false
	}
	if msg.Button != tea.MouseLeft {
		return nil, false
	}
	for id, target := range m.linkTargets {
		info := zone.Get(id)
		if info != nil && info.InBounds(msg) {
			return OpenReadmeLinkCmd(target), true
		}
	}
	return nil, false
}

func padContentToHeight(content string, height int) string {
	if height <= 0 {
		return content
	}
	var currentHeight int = lipgloss.Height(content)
	if currentHeight >= height {
		return content
	}
	var padLines int = max(0, height-currentHeight)
	return content + strings.Repeat("\n", padLines)
}

func (m *ModelCommand) renderMainMenu() string {
	title := lipgloss.NewStyle().
		Foreground(lipgloss.Color(ColorWhite)).
		Bold(true).
		Render(i18n.T("commands_b.model.title"))

	// Show current active model
	var currentModelDisplay string
	if m.currentProvider != "" && m.currentModel != "" {
		// Find the display name for current model
		var modelDisplayName string
		var providerDisplayName string
		for _, prov := range m.providers {
			if strings.EqualFold(prov.Name, m.currentProvider) {
				providerDisplayName = prov.DisplayName
				for _, model := range prov.Models {
					if model.ID == m.currentModel {
						modelDisplayName = model.DisplayName
						break
					}
				}
				break
			}
		}
		if modelDisplayName != "" {
			currentModelDisplay = lipgloss.NewStyle().
				Foreground(lipgloss.Color(ColorCyan)).
				Bold(true).
				Render(i18n.T("commands_b.model.currently_active", modelDisplayName, providerDisplayName))
		}
	}

	subtitle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(ColorGray)).
		Italic(true).
		MarginBottom(1).
		Render(i18n.T("commands_b.model.subtitle"))

	var items []string
	for i, option := range menuOptions() {
		isSelected := i == m.selectedMenu

		var desc string
		switch i {
		case MenuChooseModel:
			if m.modelSelectionUsesAliases() {
				desc = i18n.T("commands_b.model.menu.choose_model_alias_desc")
			} else {
				desc = i18n.T("commands_b.model.menu.choose_model_desc")
			}
		case MenuChooseProvider:
			desc = i18n.T("commands_b.model.menu.choose_provider_desc")
		case MenuAgentConfig:
			desc = i18n.T("commands_b.model.menu.agent_config_desc")
		}

		var itemText string
		if isSelected {
			// Selected menu item - cyan with description
			optionLine := lipgloss.NewStyle().
				Foreground(lipgloss.Color(ColorCyan)).
				Bold(true).
				Render(fmt.Sprintf("▶ %s", option))

			descLine := lipgloss.NewStyle().
				Foreground(lipgloss.Color(ColorGray)).
				Render(fmt.Sprintf("  %s", desc))

			itemText = lipgloss.JoinVertical(lipgloss.Left, optionLine, descLine, "")
		} else {
			// Unselected menu item - white
			itemText = lipgloss.NewStyle().
				Foreground(lipgloss.Color(ColorWhite)).
				Render(fmt.Sprintf("  %s", option))
		}

		items = append(items, itemText)
	}

	list := lipgloss.JoinVertical(lipgloss.Left, items...)

	hint := lipgloss.NewStyle().
		Foreground(lipgloss.Color(ColorGray)).
		MarginTop(1).
		Render(i18n.T("commands_b.model.hint_basic"))

	// Build content with optional current model display
	contentParts := []string{title}
	if currentModelDisplay != "" {
		contentParts = append(contentParts, currentModelDisplay, "")
	}
	contentParts = append(contentParts, subtitle, "", list, "", hint)

	content := lipgloss.JoinVertical(lipgloss.Left, contentParts...)

	containerWidth := 70
	if m.width > 0 && m.width < containerWidth+4 {
		containerWidth = m.width - 4
	}

	container := lipgloss.NewStyle().
		Width(containerWidth).
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color(ColorBorder)).
		Padding(2, 3)

	return container.Render(content)
}

func (m *ModelCommand) renderProviderPicker() string {
	title := lipgloss.NewStyle().
		Foreground(lipgloss.Color(ColorWhite)).
		Bold(true).
		Render(i18n.T("commands_b.model.select_provider"))

	subtitle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(ColorGray)).
		Italic(true).
		MarginBottom(1).
		Render(i18n.T("commands_b.model.select_provider_subtitle"))

	searchLine := m.renderSearchLine()

	var items []string
	var results listResults = m.providerResults()
	var totalItems int = len(results.Indices)
	startIdx := m.scrollOffset
	if startIdx > totalItems {
		startIdx = totalItems
	}
	endIdx := startIdx + m.maxVisible
	if endIdx > totalItems {
		endIdx = totalItems
	}

	for pos := startIdx; pos < endIdx; pos++ {
		var idx int = results.Indices[pos]
		var prov Provider = m.providers[idx]
		var isSelected bool = idx == m.selectedProvider

		var statusText string
		var statusColor string
		if prov.Available {
			statusText = i18n.T("commands_b.model.status_ready")
			statusColor = ColorGreen
		} else {
			statusText = i18n.T("commands_b.model.status_not_configured")
			statusColor = ColorRed
		}

		modelCount := i18n.T("commands_b.model.model_count", len(prov.Models))

		var itemText string
		if isSelected {
			// Selected provider - cyan with details
			var providerStyle lipgloss.Style = lipgloss.NewStyle().
				Foreground(lipgloss.Color(ColorCyan)).
				Bold(true)
			var highlightStyle lipgloss.Style = lipgloss.NewStyle().
				Foreground(lipgloss.Color(ColorYellow)).
				Bold(true)
			var label string = prov.DisplayName
			if label == "" {
				label = prov.Name
			}
			var highlight []int = results.Highlights[idx]
			var renderedLabel string = highlightText(label, highlight, providerStyle, highlightStyle)
			var providerLine string = providerStyle.Render("▶ ") + renderedLabel

			statusLine := lipgloss.NewStyle().
				Foreground(lipgloss.Color(statusColor)).
				Render(i18n.T("commands_b.model.provider_status", statusText, modelCount))

			itemText = lipgloss.JoinVertical(lipgloss.Left, providerLine, statusLine, "")
		} else {
			// Unselected provider - white compact line
			var providerStyle lipgloss.Style = lipgloss.NewStyle().
				Foreground(lipgloss.Color(ColorWhite))
			var highlightStyle lipgloss.Style = lipgloss.NewStyle().
				Foreground(lipgloss.Color(ColorYellow)).
				Bold(true)
			var label string = prov.DisplayName
			if label == "" {
				label = prov.Name
			}
			var highlight []int = results.Highlights[idx]
			var renderedLabel string = highlightText(label, highlight, providerStyle, highlightStyle)
			itemText = providerStyle.Render("  ") + renderedLabel + providerStyle.Render(i18n.T("commands_b.model.model_count_parenthetical", len(prov.Models)))
		}

		items = append(items, itemText)
	}

	if totalItems == 0 {
		noResults := lipgloss.NewStyle().
			Foreground(lipgloss.Color(ColorGray)).
			Italic(true).
			Render(i18n.T("commands_b.model.no_providers_match"))
		items = append(items, noResults)
	}

	// Add scroll indicator
	if totalItems > m.maxVisible {
		scrollInfo := lipgloss.NewStyle().
			Foreground(lipgloss.Color(ColorYellow)).
			Bold(true).
			Align(lipgloss.Center).
			Render(i18n.T("commands_b.model.showing_scroll", startIdx+1, endIdx, totalItems))
		items = append(items, "", scrollInfo)
	}

	list := lipgloss.JoinVertical(lipgloss.Left, items...)

	hint := lipgloss.NewStyle().
		Foreground(lipgloss.Color(ColorGray)).
		MarginTop(1).
		Render(i18n.T("commands_b.model.hint_provider"))

	content := lipgloss.JoinVertical(
		lipgloss.Left,
		title,
		subtitle,
		searchLine,
		"",
		list,
		"",
		hint,
	)

	containerWidth := 70
	if m.width > 0 && m.width < containerWidth+4 {
		containerWidth = m.width - 4
	}

	container := lipgloss.NewStyle().
		Width(containerWidth).
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color(ColorBorder)).
		Padding(2, 3)

	return container.Render(content)
}

func (m *ModelCommand) renderProviderModelPicker() string {
	if m.selectedProvider < 0 || m.selectedProvider >= len(m.providers) {
		return ""
	}
	provider := m.providers[m.selectedProvider]

	// Calculate responsive width using breakpoints
	contentWidth := m.width
	if contentWidth <= 0 {
		contentWidth = 120 // Fallback to standard terminal width
	}

	// Apply responsive breakpoints (matches layout.go CalculateResponsiveWidth)
	w := max(36, contentWidth-4)
	switch {
	case w < 40:
		contentWidth = 40
	case w <= 200:
		contentWidth = w
	case w <= 280:
		contentWidth = 240
	case w <= 360:
		contentWidth = 280
	default:
		contentWidth = 320
	}
	if contentWidth < 80 {
		contentWidth = 80
	}
	contentWidth -= 4
	if contentWidth < 60 {
		contentWidth = 60
	}
	leftWidth := max(int(float64(contentWidth)*0.44), 36)
	rightWidth := max(30, contentWidth-leftWidth-2)
	if rightWidth < 30 {
		rightWidth = max(30, contentWidth-leftWidth-1)
	}

	panelInnerWidth := max(10, leftWidth-4)
	if panelInnerWidth < 10 {
		panelInnerWidth = leftWidth
	}
	contentLineWidth := panelInnerWidth - 2
	if contentLineWidth < 10 {
		contentLineWidth = panelInnerWidth
	}

	header := m.renderToolbar(contentWidth)
	tabs := m.renderTabChips()
	filters := m.renderFilterChips(false)
	listHeader := renderHeaderRow(panelInnerWidth, tabs, filters)

	results := m.providerModelResults(m.selectedProvider)
	totalItems := len(results.Indices)
	startIdx := m.scrollOffset
	if startIdx > totalItems {
		startIdx = totalItems
	}
	endIdx := startIdx + m.maxVisible
	if endIdx > totalItems {
		endIdx = totalItems
	}
	visibleIndices := make([]int, 0, endIdx-startIdx)
	if startIdx < endIdx {
		visibleIndices = append(visibleIndices, results.Indices[startIdx:endIdx]...)
	}
	visibleSet := make(map[int]bool, len(visibleIndices))
	for _, idx := range visibleIndices {
		visibleSet[idx] = true
	}

	var items []string
	if listHeader != "" {
		items = append(items, listHeader)
	}
	titleLine := lipgloss.NewStyle().
		Foreground(lipgloss.Color(ColorMuted)).
		Bold(true).
		Render(i18n.T("commands_b.model.provider_models_title", m.providerDisplayName(provider.Name)))
	items = append(items, titleLine)

	if totalItems == 0 {
		items = append(items, lipgloss.NewStyle().
			Foreground(lipgloss.Color(ColorMuted)).
			Italic(true).
			Render(i18n.T("commands_b.model.no_models_match")))
	} else {
		for _, group := range results.Groups {
			var groupVisible bool
			for _, idx := range group.Indices {
				if visibleSet[idx] {
					groupVisible = true
					break
				}
			}
			if !groupVisible {
				continue
			}
			indicator := "v"
			if group.Collapsed {
				indicator = ">"
			}
			groupLabel := fmt.Sprintf("%s %s (%d)", indicator, group.Title, len(group.Indices))
			items = append(items, renderGroupHeaderLine(groupLabel, panelInnerWidth, indexOfInt(group.Indices, m.selectedModel) >= 0))

			for _, idx := range group.Indices {
				if !visibleSet[idx] {
					continue
				}
				model := provider.Models[idx]
				isSelected := idx == m.selectedModel

				label := model.DisplayName
				if label == "" {
					label = model.ID
				}
				highlight := results.Highlights[idx]
				badges := m.renderModelBadges(provider.Name, model.ID)
				maxLabelWidth := contentLineWidth
				if badges != "" {
					badgeWidth := lipgloss.Width(badges) + 1
					// Ensure maxLabelWidth doesn't go negative or too small
					if badgeWidth < maxLabelWidth {
						maxLabelWidth -= badgeWidth
					} else {
						// Not enough space for both label and badge, just show label
						maxLabelWidth = contentLineWidth
						badges = ""
					}
				}
				trimmedLabel, trimmedHighlight := trimLabelWithHighlights(label, highlight, maxLabelWidth)

				nameStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(ColorWhite))
				if isSelected {
					nameStyle = nameStyle.Bold(true)
				}
				highlightStyle := lipgloss.NewStyle().
					Foreground(lipgloss.Color(ColorAccent)).
					Bold(true)
				renderedLabel := highlightText(trimmedLabel, trimmedHighlight, nameStyle, highlightStyle)
				line1 := joinLeftRight(contentLineWidth, renderedLabel, badges)

				contextLabel := strings.TrimSpace(model.Context)
				if contextLabel == "" && model.ContextWindow > 0 {
					contextLabel = formatContextWindow(model.ContextWindow)
				}
				if contextLabel == "" {
					contextLabel = i18n.T("commands_b.model.not_available")
				}
				metaLeft := i18n.T("commands_b.model.context_value", contextLabel)
				tags := InferModelTags(provider.Name, model)
				metaRight := renderTagChips(tags)
				line2 := joinLeftRight(contentLineWidth, metaLeft, metaRight)

				card := strings.Join([]string{
					renderCardLine(line1, panelInnerWidth, isSelected),
					renderCardLine(line2, panelInnerWidth, isSelected),
				}, "\n")
				items = append(items, card)
			}
		}
	}

	if totalItems > m.maxVisible {
		scrollLine := lipgloss.NewStyle().
			Foreground(lipgloss.Color(ColorMuted)).
			Render(i18n.T("commands_b.model.showing", startIdx+1, endIdx, totalItems))
		items = append(items, scrollLine)
	}

	listContent := lipgloss.JoinVertical(lipgloss.Left, items...)
	leftPanel := renderPanel(listContent, leftWidth)

	var detailPanel string
	if len(results.Indices) > 0 {
		m.ensureSelection(results.Indices, &m.selectedModel)
		model := provider.Models[m.selectedModel]
		detailPanel = m.renderModelDetail(provider, model, rightWidth)
	} else {
		empty := lipgloss.NewStyle().Foreground(lipgloss.Color(ColorMuted)).Render(i18n.T("commands_b.model.no_model_selected"))
		detailPanel = renderPanel(empty, rightWidth)
	}

	body := lipgloss.JoinHorizontal(lipgloss.Top, leftPanel, "  ", detailPanel)
	hint := lipgloss.NewStyle().
		Foreground(lipgloss.Color(ColorMuted)).
		Render(i18n.T("commands_b.model.hint_browser"))

	content := lipgloss.JoinVertical(lipgloss.Left, header, "", body, "", hint)
	return lipgloss.NewStyle().
		Width(contentWidth+4).
		Padding(1, 2).
		Background(lipgloss.Color(ColorSurface)).
		Render(content)
}

func (m *ModelCommand) renderModelPicker() string {
	if m.modelSelectionUsesAliases() {
		return m.renderAliasPicker()
	}

	allModels := m.getAllModels()
	results := m.allModelResults(allModels)
	totalItems := len(results.Indices)

	// Calculate responsive width using breakpoints
	contentWidth := m.width
	if contentWidth <= 0 {
		contentWidth = 120 // Fallback to standard terminal width
	}

	// Apply responsive breakpoints (matches layout.go CalculateResponsiveWidth)
	w := max(36, contentWidth-4)
	switch {
	case w < 40:
		contentWidth = 40
	case w <= 200:
		contentWidth = w
	case w <= 280:
		contentWidth = 240
	case w <= 360:
		contentWidth = 280
	default:
		contentWidth = 320
	}
	if contentWidth < 80 {
		contentWidth = 80
	}
	contentWidth -= 4
	if contentWidth < 60 {
		contentWidth = 60
	}
	leftWidth := max(int(float64(contentWidth)*0.44), 36)
	rightWidth := max(30, contentWidth-leftWidth-2)
	if rightWidth < 30 {
		rightWidth = max(30, contentWidth-leftWidth-1)
	}

	panelInnerWidth := max(10, leftWidth-4)
	if panelInnerWidth < 10 {
		panelInnerWidth = leftWidth
	}
	contentLineWidth := panelInnerWidth - 2
	if contentLineWidth < 10 {
		contentLineWidth = panelInnerWidth
	}

	header := m.renderToolbar(contentWidth)
	tabs := m.renderTabChips()
	filters := m.renderFilterChips(true)
	listHeader := renderHeaderRow(panelInnerWidth, tabs, filters)

	startIdx := m.scrollOffset
	if startIdx > totalItems {
		startIdx = totalItems
	}
	endIdx := startIdx + m.maxVisible
	if endIdx > totalItems {
		endIdx = totalItems
	}
	visibleIndices := make([]int, 0, endIdx-startIdx)
	if startIdx < endIdx {
		visibleIndices = append(visibleIndices, results.Indices[startIdx:endIdx]...)
	}
	visibleSet := make(map[int]bool, len(visibleIndices))
	for _, idx := range visibleIndices {
		visibleSet[idx] = true
	}

	var items []string
	if listHeader != "" {
		items = append(items, listHeader)
	}
	titleLine := lipgloss.NewStyle().
		Foreground(lipgloss.Color(ColorMuted)).
		Bold(true).
		Render(i18n.T("commands_b.model.all_models"))
	items = append(items, titleLine)

	if totalItems == 0 {
		items = append(items, lipgloss.NewStyle().
			Foreground(lipgloss.Color(ColorMuted)).
			Italic(true).
			Render(i18n.T("commands_b.model.no_models_match")))
	} else {
		for _, group := range results.Groups {
			var groupVisible bool
			for _, idx := range group.Indices {
				if visibleSet[idx] {
					groupVisible = true
					break
				}
			}
			if !groupVisible {
				continue
			}
			indicator := "v"
			if group.Collapsed {
				indicator = ">"
			}
			groupLabel := fmt.Sprintf("%s %s (%d)", indicator, group.Title, len(group.Indices))
			items = append(items, renderGroupHeaderLine(groupLabel, panelInnerWidth, indexOfInt(group.Indices, m.selectedModel) >= 0))

			for _, idx := range group.Indices {
				if !visibleSet[idx] {
					continue
				}
				entry := allModels[idx]
				isSelected := idx == m.selectedModel
				label := entry.Model.DisplayName
				if label == "" {
					label = entry.Model.ID
				}
				highlight := results.Highlights[idx]
				badges := m.renderModelBadges(entry.ProviderName, entry.Model.ID)
				maxLabelWidth := contentLineWidth
				if badges != "" {
					badgeWidth := lipgloss.Width(badges) + 1
					// Ensure maxLabelWidth doesn't go negative or too small
					if badgeWidth < maxLabelWidth {
						maxLabelWidth -= badgeWidth
					} else {
						// Not enough space for both label and badge, just show label
						maxLabelWidth = contentLineWidth
						badges = ""
					}
				}
				trimmedLabel, trimmedHighlight := trimLabelWithHighlights(label, highlight, maxLabelWidth)

				nameStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(ColorWhite))
				if isSelected {
					nameStyle = nameStyle.Bold(true)
				}
				highlightStyle := lipgloss.NewStyle().
					Foreground(lipgloss.Color(ColorAccent)).
					Bold(true)
				renderedLabel := highlightText(trimmedLabel, trimmedHighlight, nameStyle, highlightStyle)
				line1 := joinLeftRight(contentLineWidth, renderedLabel, badges)

				contextLabel := strings.TrimSpace(entry.Model.Context)
				if contextLabel == "" && entry.Model.ContextWindow > 0 {
					contextLabel = formatContextWindow(entry.Model.ContextWindow)
				}
				if contextLabel == "" {
					contextLabel = i18n.T("commands_b.model.not_available")
				}
				providerLabel := m.providerDisplayName(entry.ProviderName)
				metaLeft := fmt.Sprintf("%s • %s", providerLabel, contextLabel)
				tags := InferModelTags(entry.ProviderName, entry.Model)
				metaRight := renderTagChips(tags)
				line2 := joinLeftRight(contentLineWidth, metaLeft, metaRight)

				card := strings.Join([]string{
					renderCardLine(line1, panelInnerWidth, isSelected),
					renderCardLine(line2, panelInnerWidth, isSelected),
				}, "\n")
				items = append(items, card)
			}
		}
	}

	if totalItems > m.maxVisible {
		scrollLine := lipgloss.NewStyle().
			Foreground(lipgloss.Color(ColorMuted)).
			Render(i18n.T("commands_b.model.showing", startIdx+1, endIdx, totalItems))
		items = append(items, scrollLine)
	}

	listContent := lipgloss.JoinVertical(lipgloss.Left, items...)
	leftPanel := renderPanel(listContent, leftWidth)

	var detailPanel string
	if len(results.Indices) > 0 {
		m.ensureSelection(results.Indices, &m.selectedModel)
		entry := allModels[m.selectedModel]
		detailPanel = m.renderModelDetail(entry.Provider, entry.Model, rightWidth)
	} else {
		empty := lipgloss.NewStyle().Foreground(lipgloss.Color(ColorMuted)).Render(i18n.T("commands_b.model.no_model_selected"))
		detailPanel = renderPanel(empty, rightWidth)
	}

	body := lipgloss.JoinHorizontal(lipgloss.Top, leftPanel, "  ", detailPanel)
	hint := lipgloss.NewStyle().
		Foreground(lipgloss.Color(ColorMuted)).
		Render(i18n.T("commands_b.model.hint_browser"))

	content := lipgloss.JoinVertical(lipgloss.Left, header, "", body, "", hint)
	return lipgloss.NewStyle().
		Width(contentWidth+4).
		Padding(1, 2).
		Background(lipgloss.Color(ColorSurface)).
		Render(content)
}

func (m *ModelCommand) renderAliasPicker() string {
	aliasEntries := m.aliasEntries
	results := m.aliasResults()
	totalItems := len(results.Indices)

	// Calculate responsive width using breakpoints
	contentWidth := m.width
	if contentWidth <= 0 {
		contentWidth = 120 // Fallback to standard terminal width
	}

	// Apply responsive breakpoints (matches layout.go CalculateResponsiveWidth)
	w := max(36, contentWidth-4)
	switch {
	case w < 40:
		contentWidth = 40
	case w <= 200:
		contentWidth = w
	case w <= 280:
		contentWidth = 240
	case w <= 360:
		contentWidth = 280
	default:
		contentWidth = 320
	}
	if contentWidth < 80 {
		contentWidth = 80
	}
	contentWidth -= 4
	if contentWidth < 60 {
		contentWidth = 60
	}
	leftWidth := max(int(float64(contentWidth)*0.44), 36)
	rightWidth := max(30, contentWidth-leftWidth-2)
	if rightWidth < 30 {
		rightWidth = max(30, contentWidth-leftWidth-1)
	}

	panelInnerWidth := max(10, leftWidth-4)
	if panelInnerWidth < 10 {
		panelInnerWidth = leftWidth
	}
	contentLineWidth := panelInnerWidth - 2
	if contentLineWidth < 10 {
		contentLineWidth = panelInnerWidth
	}

	header := m.renderToolbar(contentWidth)
	tabs := m.renderTabChips()
	filters := m.renderFilterChips(true)
	listHeader := renderHeaderRow(panelInnerWidth, tabs, filters)

	startIdx := m.scrollOffset
	if startIdx > totalItems {
		startIdx = totalItems
	}
	endIdx := startIdx + m.maxVisible
	if endIdx > totalItems {
		endIdx = totalItems
	}

	var items []string
	if listHeader != "" {
		items = append(items, listHeader)
	}
	titleLine := lipgloss.NewStyle().
		Foreground(lipgloss.Color(ColorMuted)).
		Bold(true).
		Render(i18n.T("commands_b.model.model_families"))
	items = append(items, titleLine)

	if totalItems == 0 {
		items = append(items, lipgloss.NewStyle().
			Foreground(lipgloss.Color(ColorMuted)).
			Italic(true).
			Render(i18n.T("commands_b.model.no_model_families_match")))
	} else {
		for pos := startIdx; pos < endIdx; pos++ {
			idx := results.Indices[pos]
			alias := aliasEntries[idx]
			isSelected := idx == m.selectedModel

			label := alias.DisplayName
			if label == "" {
				label = alias.Name
			}
			highlight := results.Highlights[idx]
			trimmedLabel, trimmedHighlight := trimLabelWithHighlights(label, highlight, contentLineWidth)

			nameStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(ColorWhite))
			if isSelected {
				nameStyle = nameStyle.Bold(true)
			}
			highlightStyle := lipgloss.NewStyle().
				Foreground(lipgloss.Color(ColorAccent)).
				Bold(true)
			renderedLabel := highlightText(trimmedLabel, trimmedHighlight, nameStyle, highlightStyle)

			providerCount := len(alias.Variants)
			contextLabel := AliasContextLabel(alias.Variants)
			if contextLabel == "" {
				contextLabel = i18n.T("commands_b.model.not_available")
			}
			metaLeft := lipgloss.NewStyle().
				Foreground(lipgloss.Color(ColorMuted)).
				Render(i18n.T("commands_b.model.providers_context", providerCount, contextLabel))
			tags := aliasEntryTags(alias.Variants)
			metaRight := renderTagChips(tags)
			line1 := renderedLabel
			line2 := joinLeftRight(contentLineWidth, metaLeft, metaRight)

			card := strings.Join([]string{
				renderCardLine(line1, panelInnerWidth, isSelected),
				renderCardLine(line2, panelInnerWidth, isSelected),
			}, "\n")
			items = append(items, card)
		}
	}

	if totalItems > m.maxVisible {
		scrollLine := lipgloss.NewStyle().
			Foreground(lipgloss.Color(ColorMuted)).
			Render(i18n.T("commands_b.model.showing", startIdx+1, endIdx, totalItems))
		items = append(items, scrollLine)
	}

	listContent := lipgloss.JoinVertical(lipgloss.Left, items...)
	leftPanel := renderPanel(listContent, leftWidth)

	var detailPanel string
	if len(results.Indices) > 0 {
		m.ensureSelection(results.Indices, &m.selectedModel)
		alias := aliasEntries[m.selectedModel]
		detailPanel = m.renderAliasDetail(alias, rightWidth)
	} else {
		empty := lipgloss.NewStyle().Foreground(lipgloss.Color(ColorMuted)).Render(i18n.T("commands_b.model.no_model_family_selected"))
		detailPanel = renderPanel(empty, rightWidth)
	}

	body := lipgloss.JoinHorizontal(lipgloss.Top, leftPanel, "  ", detailPanel)
	hint := lipgloss.NewStyle().
		Foreground(lipgloss.Color(ColorMuted)).
		Render(i18n.T("commands_b.model.hint_aliases"))

	content := lipgloss.JoinVertical(lipgloss.Left, header, "", body, "", hint)
	return lipgloss.NewStyle().
		Width(contentWidth+4).
		Padding(1, 2).
		Background(lipgloss.Color(ColorSurface)).
		Render(content)
}

func (m *ModelCommand) renderAliasVariantPicker() string {
	if m.selectedAlias >= len(m.aliasEntries) {
		return m.renderAliasPicker()
	}
	alias := m.aliasEntries[m.selectedAlias]
	results := m.aliasVariantResults(m.selectedAlias)
	totalItems := len(results.Indices)

	// Calculate responsive width using breakpoints
	contentWidth := m.width
	if contentWidth <= 0 {
		contentWidth = 120 // Fallback to standard terminal width
	}

	// Apply responsive breakpoints (matches layout.go CalculateResponsiveWidth)
	w := max(36, contentWidth-4)
	switch {
	case w < 40:
		contentWidth = 40
	case w <= 200:
		contentWidth = w
	case w <= 280:
		contentWidth = 240
	case w <= 360:
		contentWidth = 280
	default:
		contentWidth = 320
	}
	if contentWidth < 80 {
		contentWidth = 80
	}
	contentWidth -= 4
	if contentWidth < 60 {
		contentWidth = 60
	}
	leftWidth := max(int(float64(contentWidth)*0.44), 36)
	rightWidth := max(30, contentWidth-leftWidth-2)
	if rightWidth < 30 {
		rightWidth = max(30, contentWidth-leftWidth-1)
	}

	panelInnerWidth := max(10, leftWidth-4)
	if panelInnerWidth < 10 {
		panelInnerWidth = leftWidth
	}
	contentLineWidth := panelInnerWidth - 2
	if contentLineWidth < 10 {
		contentLineWidth = panelInnerWidth
	}

	header := m.renderToolbar(contentWidth)
	tabs := m.renderTabChips()
	filters := m.renderFilterChips(true)
	listHeader := renderHeaderRow(panelInnerWidth, tabs, filters)

	startIdx := m.scrollOffset
	if startIdx > totalItems {
		startIdx = totalItems
	}
	endIdx := startIdx + m.maxVisible
	if endIdx > totalItems {
		endIdx = totalItems
	}
	visibleIndices := make([]int, 0, endIdx-startIdx)
	if startIdx < endIdx {
		visibleIndices = append(visibleIndices, results.Indices[startIdx:endIdx]...)
	}
	visibleSet := make(map[int]bool, len(visibleIndices))
	for _, idx := range visibleIndices {
		visibleSet[idx] = true
	}

	var items []string
	if listHeader != "" {
		items = append(items, listHeader)
	}
	titleLine := lipgloss.NewStyle().
		Foreground(lipgloss.Color(ColorMuted)).
		Bold(true).
		Render(i18n.T("commands_b.model.providers_for", alias.DisplayName))
	items = append(items, titleLine)

	if totalItems == 0 {
		items = append(items, lipgloss.NewStyle().
			Foreground(lipgloss.Color(ColorMuted)).
			Italic(true).
			Render(i18n.T("commands_b.model.no_providers_match")))
	} else {
		for _, group := range results.Groups {
			var groupVisible bool
			for _, idx := range group.Indices {
				if visibleSet[idx] {
					groupVisible = true
					break
				}
			}
			if !groupVisible {
				continue
			}
			indicator := "v"
			if group.Collapsed {
				indicator = ">"
			}
			groupLabel := fmt.Sprintf("%s %s (%d)", indicator, group.Title, len(group.Indices))
			items = append(items, renderGroupHeaderLine(groupLabel, panelInnerWidth, indexOfInt(group.Indices, m.selectedVariant) >= 0))

			for _, idx := range group.Indices {
				if !visibleSet[idx] {
					continue
				}
				variant := alias.Variants[idx]
				isSelected := idx == m.selectedVariant
				providerLabel := variant.ProviderDisplayName
				if providerLabel == "" {
					providerLabel = variant.ProviderName
				}

				highlight := results.Highlights[idx]
				badges := m.renderModelBadges(variant.ProviderName, variant.Model.ID)
				maxLabelWidth := contentLineWidth
				if badges != "" {
					badgeWidth := lipgloss.Width(badges) + 1
					// Ensure maxLabelWidth doesn't go negative or too small
					if badgeWidth < maxLabelWidth {
						maxLabelWidth -= badgeWidth
					} else {
						// Not enough space for both label and badge, just show label
						maxLabelWidth = contentLineWidth
						badges = ""
					}
				}
				trimmedLabel, trimmedHighlight := trimLabelWithHighlights(providerLabel, highlight, maxLabelWidth)

				nameStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(ColorWhite))
				if isSelected {
					nameStyle = nameStyle.Bold(true)
				}
				highlightStyle := lipgloss.NewStyle().
					Foreground(lipgloss.Color(ColorAccent)).
					Bold(true)
				renderedLabel := highlightText(trimmedLabel, trimmedHighlight, nameStyle, highlightStyle)
				line1 := joinLeftRight(contentLineWidth, renderedLabel, badges)

				modelLabel := variant.Model.DisplayName
				if modelLabel == "" {
					modelLabel = variant.Model.ID
				}
				contextLabel := strings.TrimSpace(variant.Model.Context)
				if contextLabel == "" && variant.Model.ContextWindow > 0 {
					contextLabel = formatContextWindow(variant.Model.ContextWindow)
				}
				if contextLabel == "" {
					contextLabel = i18n.T("commands_b.model.not_available")
				}
				metaLeft := fmt.Sprintf("%s • %s", modelLabel, contextLabel)
				tags := InferModelTags(variant.ProviderName, variant.Model)
				metaRight := renderTagChips(tags)
				line2 := joinLeftRight(contentLineWidth, metaLeft, metaRight)

				card := strings.Join([]string{
					renderCardLine(line1, panelInnerWidth, isSelected),
					renderCardLine(line2, panelInnerWidth, isSelected),
				}, "\n")
				items = append(items, card)
			}
		}
	}

	if totalItems > m.maxVisible {
		scrollLine := lipgloss.NewStyle().
			Foreground(lipgloss.Color(ColorMuted)).
			Render(i18n.T("commands_b.model.showing", startIdx+1, endIdx, totalItems))
		items = append(items, scrollLine)
	}

	listContent := lipgloss.JoinVertical(lipgloss.Left, items...)
	leftPanel := renderPanel(listContent, leftWidth)

	var detailPanel string
	if len(results.Indices) > 0 {
		m.ensureSelection(results.Indices, &m.selectedVariant)
		variant := alias.Variants[m.selectedVariant]
		provider := Provider{Name: variant.ProviderName, DisplayName: variant.ProviderDisplayName, Color: variant.ProviderColor, Available: variant.ProviderAvailable}
		if found := m.providerByName(variant.ProviderName); found != nil {
			provider.APIType = found.APIType
			provider.BaseURL = found.BaseURL
			provider.Available = found.Available
			if provider.DisplayName == "" {
				provider.DisplayName = found.DisplayName
			}
		}
		detailPanel = m.renderModelDetail(provider, variant.Model, rightWidth)
	} else {
		empty := lipgloss.NewStyle().Foreground(lipgloss.Color(ColorMuted)).Render(i18n.T("commands_b.model.no_provider_selected"))
		detailPanel = renderPanel(empty, rightWidth)
	}

	body := lipgloss.JoinHorizontal(lipgloss.Top, leftPanel, "  ", detailPanel)
	hint := lipgloss.NewStyle().
		Foreground(lipgloss.Color(ColorMuted)).
		Render(i18n.T("commands_b.model.hint_browser"))

	content := lipgloss.JoinVertical(lipgloss.Left, header, "", body, "", hint)
	return lipgloss.NewStyle().
		Width(contentWidth+4).
		Padding(1, 2).
		Background(lipgloss.Color(ColorSurface)).
		Render(content)
}

func (m *ModelCommand) renderAgentConfig() string {
	title := lipgloss.NewStyle().
		Foreground(lipgloss.Color(ColorWhite)).
		Bold(true).
		Render(i18n.T("commands_b.model.agent_config_title"))

	message := lipgloss.NewStyle().
		Foreground(lipgloss.Color(ColorGray)).
		Italic(true).
		Align(lipgloss.Center).
		Render(i18n.T("commands_b.model.agent_config_coming_soon"))

	hint := lipgloss.NewStyle().
		Foreground(lipgloss.Color(ColorGray)).
		MarginTop(1).
		Render(i18n.T("commands_b.model.hint_agent_config"))

	content := lipgloss.JoinVertical(
		lipgloss.Left,
		title,
		"",
		message,
		"",
		hint,
	)

	containerWidth := 70
	if m.width > 0 && m.width < containerWidth+4 {
		containerWidth = m.width - 4
	}

	container := lipgloss.NewStyle().
		Width(containerWidth).
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color(ColorBorder)).
		Padding(2, 3)

	return container.Render(content)
}
