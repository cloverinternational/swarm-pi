package settings

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/chat/commands"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/i18n"
	zone "github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/ui/zone"
)

// Render renders the model settings view
func (m *ModelSettings) Render(width, height int, state *State, theme any) string {
	th := theme.(Theme)
	m.readmeLinkTargets = nil
	var rendered string

	if m.confirmOverride {
		rendered = m.renderConfirmOverride(width, height, th)
		return i18n.SettingsResidualModelsText(rendered)
	}

	switch m.state {
	case "menu":
		rendered = m.renderMenu(width, height, state, th)
	case "browse_models":
		rendered = m.renderBrowseProviders(width, height, th)
	case "alias_variants":
		rendered = m.renderAliasVariants(width, height, th)
	case "manage_providers":
		rendered = m.renderManageProviders(width, height, th)
	case "add_provider":
		rendered = m.renderAddProvider(width, height, th)
	case "edit_provider":
		rendered = m.renderEditProvider(width, height, th)
	case "provider_models":
		rendered = m.renderProviderModels(width, height, th)
	case "models":
		rendered = m.renderModelPicker(width, height, th)
	case "add_model":
		rendered = m.renderAddModel(width, height, th)
	case "edit_model":
		rendered = m.renderEditModel(width, height, th)
	case "confirm_delete":
		rendered = m.renderConfirmDelete(width, height, th)
	}

	return i18n.SettingsResidualModelsText(rendered)
}

func (m *ModelSettings) HandleMouseClick(msg tea.Mouse) (tea.Cmd, bool) {
	if len(m.readmeLinkTargets) == 0 {
		return nil, false
	}
	if msg.Button != tea.MouseLeft {
		return nil, false
	}
	for id, target := range m.readmeLinkTargets {
		info := zone.Get(id)
		if info != nil && info.InBounds(msg) {
			return commands.OpenReadmeLinkCmd(target), true
		}
	}
	return nil, false
}

func (m *ModelSettings) renderMenu(width, height int, state *State, th Theme) string {
	var lines []string

	hPad := 2
	if width < 50 {
		hPad = 1
	}

	// Header
	headerStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(th.Primary)).
		Bold(true).
		Padding(1, hPad)
	lines = append(lines, headerStyle.Render(i18n.T("settings.residual_final.model.menu.title")))

	// Current config
	currentStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(th.TextMuted)).
		Padding(0, hPad)
	currentLine := i18n.T("settings.residual_final.model.menu.active", m.currentProvider, m.currentModel)
	lines = append(lines, currentStyle.Render(currentLine))
	if m.selectionSupportsReasoningEffort() {
		reasoningLine := i18n.T("settings.residual_final.model.menu.reasoning_effort_value", m.GetReasoningEffort())
		lines = append(lines, currentStyle.Render(reasoningLine))
	}
	// Compaction threshold readout: show the effective threshold (global or
	// per-model override) for the currently active model.
	if compLine := m.renderCompactionThresholdReadout(th); compLine != "" {
		lines = append(lines, currentStyle.Render(compLine))
	}
	lines = append(lines, "")

	// Menu options
	menuIDs := m.menuOptionIDs()
	descPad := 4
	if width < 50 {
		descPad = 2
	}
	for pos, id := range menuIDs {
		opt := modelMenuOptions[id]
		isSelected := pos == m.selectedMenuItem && state.Focus == FocusContent

		nameStyle := lipgloss.NewStyle().
			Foreground(lipgloss.Color(th.Text)).
			Bold(isSelected).
			Padding(0, hPad)
		if isSelected {
			nameStyle = nameStyle.Foreground(lipgloss.Color(th.Primary))
		}
		optionName, optionDesc := localizedModelMenuOption(id, opt.name, opt.desc)
		if id == MenuReasoningEffort {
			optionName = i18n.T("settings.residual_final.model.menu.option_with_value", optionName, m.GetReasoningEffort())
			optionDesc = i18n.T("settings.residual_final.model.menu.cycle_effort", strings.Join(m.availableReasoningEfforts(), ", "))
		}
		name := nameStyle.Render(fmt.Sprintf("%s %s", map[bool]string{true: "▶", false: " "}[isSelected], optionName))
		lines = append(lines, name)

		descStyle := lipgloss.NewStyle().
			Foreground(lipgloss.Color(th.TextMuted)).
			Padding(0, descPad)
		desc := descStyle.Render(optionDesc)
		lines = append(lines, desc, "")
	}

	content := lipgloss.JoinVertical(lipgloss.Left, lines...)
	return lipgloss.NewStyle().Width(maxInt(20, width)).Height(maxInt(5, height)).Render(content)
}

// renderCompactionThresholdReadout returns a single-line summary of the
// effective auto-compaction threshold for the current model (global value, or
// a per-model override if one is set). Returns "" if config is unavailable.
func (m *ModelSettings) renderCompactionThresholdReadout(th Theme) string {
	if m == nil || m.configManager == nil {
		return ""
	}
	cfg, err := m.configManager.LoadConfig()
	if err != nil || cfg == nil {
		return ""
	}
	cth := cfg.GetCompactionThresholdForModel(m.currentProvider, m.currentModel)
	hasOverride := false
	if key := commands.CompactionOverrideKey(m.currentProvider, m.currentModel); key != "" {
		if _, ok := cfg.CompactionThresholdOverrides[key]; ok {
			hasOverride = true
		}
	}
	var label string
	switch cth.Mode {
	case commands.CompactionThresholdFixedTokens:
		label = i18n.T("settings.residual_final.model.tokens", formatTokenCount(int(cth.Value)))
	default:
		label = fmt.Sprintf("%d%%", int(cth.AsPercent(0)*100))
	}
	source := i18n.T("settings.residual_final.model.threshold.global")
	if hasOverride {
		source = i18n.T("settings.residual_final.model.threshold.override")
	}
	return i18n.T("settings.residual_final.model.threshold.value", label, source)
}

func localizedModelMenuOption(id int, fallbackName, fallbackDesc string) (string, string) {
	switch id {
	case MenuBrowseModels:
		return i18n.T("settings.residual_final.model.menu.browse"), i18n.T("settings.residual_final.model.menu.browse_description")
	case MenuManageProviders:
		return i18n.T("settings.residual_final.model.menu.manage"), i18n.T("settings.residual_final.model.menu.manage_description")
	case MenuAddProvider:
		return i18n.T("settings.residual_final.model.menu.add_provider"), i18n.T("settings.residual_final.model.menu.add_provider_description")
	case MenuReasoningEffort:
		return i18n.T("settings.residual_final.model.menu.reasoning_effort"), i18n.T("settings.residual_final.model.menu.reasoning_effort_description")
	default:
		return fallbackName, fallbackDesc
	}
}

func (m *ModelSettings) renderBrowseProviders(width, height int, th Theme) string {
	if m.usesAliases() {
		return m.renderBrowseAliases(width, height, th)
	}

	var lines []string

	bpPad := 2
	bpStatusPad := 4
	if width < 50 {
		bpPad = 1
		bpStatusPad = 2
	}

	headerStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(th.Primary)).
		Bold(true).
		Padding(1, bpPad)
	lines = append(lines, headerStyle.Render(i18n.T("settings.residual_final.model.select_provider")))

	subtitleStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(th.TextMuted)).
		Italic(true).
		Padding(0, bpPad).
		MarginBottom(1)
	if width >= 50 {
		lines = append(lines, subtitleStyle.Render(i18n.T("settings.residual_final.model.select_provider_description")))
	}

	lines = append(lines, m.renderSearchLine(th))

	var results listResults = m.providerResults()
	var totalItems int = len(results.Indices)
	var startIdx int = m.scrollOffset
	if startIdx > totalItems {
		startIdx = totalItems
	}
	var endIdx int = startIdx + m.maxVisible
	if endIdx > totalItems {
		endIdx = totalItems
	}

	for pos := startIdx; pos < endIdx; pos++ {
		var idx int = results.Indices[pos]
		var prov commands.Provider = m.providers[idx]
		var isSelected bool = idx == m.selectedProvider
		var highlightStyle lipgloss.Style = lipgloss.NewStyle().
			Foreground(lipgloss.Color(th.Warning)).
			Bold(true)
		var nameStyle lipgloss.Style = lipgloss.NewStyle().
			Foreground(lipgloss.Color(th.Text))
		if isSelected {
			nameStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color(th.Primary)).
				Bold(true)
		}
		var label string = prov.DisplayName
		if label == "" {
			label = prov.Name
		}
		var renderedLabel string = highlightText(label, results.Highlights[idx], nameStyle, highlightStyle)

		if isSelected {
			var provLine string = nameStyle.Render("▶ ") + renderedLabel

			statusColor := th.Success
			statusText := i18n.T("settings.residual_final.common.ready_upper")
			if !prov.Available {
				statusColor = th.Error
				statusText = i18n.T("settings.residual_final.common.not_configured_upper")
			}

			statusLine := lipgloss.NewStyle().
				Foreground(lipgloss.Color(statusColor)).
				Padding(0, bpStatusPad).
				Render(fmt.Sprintf("%s%s  •  %d%s", i18n.T("settings.residual_models.browse.status"), statusText, len(prov.Models), i18n.T("settings.residual_models.browse.models_suffix")))

			lines = append(lines, provLine, statusLine, "")
		} else {
			var provLine string = nameStyle.Render("  ") + renderedLabel + nameStyle.Render(fmt.Sprintf(" (%d%s)", len(prov.Models), i18n.T("settings.residual_models.browse.models_suffix")))
			lines = append(lines, provLine)
		}
	}

	if totalItems == 0 {
		noResults := lipgloss.NewStyle().
			Foreground(lipgloss.Color(th.TextMuted)).
			Italic(true).
			Padding(0, bpPad).
			Render(i18n.T("settings.residual_final.model.no_providers_match"))
		lines = append(lines, noResults)
	}

	if totalItems > m.maxVisible {
		scrollInfo := i18n.T("settings.residual_final.common.showing_range", startIdx+1, endIdx, totalItems)
		lines = append(lines, "",
			lipgloss.NewStyle().
				Foreground(lipgloss.Color(th.TextMuted)).
				Italic(true).
				Padding(0, bpPad).
				Render(scrollInfo))
	}

	content := lipgloss.JoinVertical(lipgloss.Left, lines...)
	return lipgloss.NewStyle().Width(maxInt(20, width)).Height(maxInt(5, height)).Render(content)
}

func (m *ModelSettings) renderBrowseAliases(width, height int, th Theme) string {
	aliasEntries := m.aliasEntries
	results := m.aliasResults()
	totalItems := len(results.Indices)

	// Calculate responsive width using breakpoints
	contentWidth := width
	if contentWidth <= 0 {
		contentWidth = 120 // Fallback to standard terminal width
	}

	// Apply responsive breakpoints (matches layout.go CalculateResponsiveWidth)
	w := maxInt(20, contentWidth-4) // Account for padding
	switch {
	case w < 40:
		contentWidth = maxInt(20, w)
	case w <= 200:
		contentWidth = w
	case w <= 280:
		contentWidth = 240
	case w <= 360:
		contentWidth = 280
	default:
		contentWidth = 320
	}
	if contentWidth > 4 {
		contentWidth -= 4
	}

	// At narrow widths, collapse to single-pane
	showTwoPane := contentWidth >= 60
	if !showTwoPane {
		contentWidth = maxInt(20, width)
	}

	outerPad := 2
	if width < 50 {
		outerPad = 1
	}

	var leftWidth, rightWidth, panelInnerWidth int
	if showTwoPane {
		leftWidth = int(float64(contentWidth) * 0.44)
		if leftWidth < 20 {
			leftWidth = 20
		}
		rightWidth = maxInt(20, contentWidth-leftWidth-2)
		panelInnerWidth = maxInt(10, leftWidth-4)
	} else {
		leftWidth = maxInt(20, contentWidth-2)
		rightWidth = 0
		panelInnerWidth = maxInt(10, leftWidth-4)
	}

	header := m.renderToolbar(maxInt(20, contentWidth), th)
	tabs := m.renderTabChips(th)
	filters := m.renderFilterChips(true, th)
	listHeader := renderHeaderRow(panelInnerWidth, tabs, filters)

	startIdx := m.scrollOffset
	if startIdx > totalItems {
		startIdx = totalItems
	}
	endIdx := startIdx + m.maxVisible
	if endIdx > totalItems {
		endIdx = totalItems
	}
	contentLineWidth := maxInt(10, panelInnerWidth-2)

	var items []string
	if listHeader != "" {
		items = append(items, listHeader)
	}
	titleLine := lipgloss.NewStyle().
		Foreground(lipgloss.Color(th.TextMuted)).
		Bold(true).
		Render("Model families")
	items = append(items, titleLine)

	if totalItems == 0 {
		items = append(items, lipgloss.NewStyle().
			Foreground(lipgloss.Color(th.TextMuted)).
			Italic(true).
			Render("No model families match your search"))
	} else {
		for pos := startIdx; pos < endIdx; pos++ {
			idx := results.Indices[pos]
			alias := aliasEntries[idx]
			isSelected := idx == m.selectedAlias

			label := alias.DisplayName
			if label == "" {
				label = alias.Name
			}
			highlight := results.Highlights[idx]
			trimmedLabel, trimmedHighlight := trimLabelWithHighlights(label, highlight, contentLineWidth)

			nameStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(th.Text))
			if isSelected {
				nameStyle = nameStyle.Bold(true)
			}
			highlightStyle := lipgloss.NewStyle().
				Foreground(lipgloss.Color(th.Primary)).
				Bold(true)
			renderedLabel := highlightText(trimmedLabel, trimmedHighlight, nameStyle, highlightStyle)

			providerCount := len(alias.Variants)
			contextLabel := commands.AliasContextLabel(alias.Variants)
			if contextLabel == "" {
				contextLabel = "n/a"
			}
			metaLeft := lipgloss.NewStyle().
				Foreground(lipgloss.Color(th.TextMuted)).
				Render(fmt.Sprintf("%d providers • Context %s", providerCount, contextLabel))
			tags := aliasVariantTags(alias.Variants)
			metaRight := renderTagChips(tags, th)
			line1 := renderedLabel
			line2 := joinLeftRight(contentLineWidth, metaLeft, metaRight)

			card := strings.Join([]string{
				renderCardLine(line1, panelInnerWidth, isSelected, th),
				renderCardLine(line2, panelInnerWidth, isSelected, th),
			}, "\n")
			items = append(items, card)
		}
	}

	if totalItems > m.maxVisible {
		scrollLine := lipgloss.NewStyle().
			Foreground(lipgloss.Color(th.TextMuted)).
			Render(fmt.Sprintf("Showing %d-%d of %d", startIdx+1, endIdx, totalItems))
		items = append(items, scrollLine)
	}

	listContent := lipgloss.JoinVertical(lipgloss.Left, items...)
	leftPanel := renderPanel(listContent, leftWidth, th)

	var body string
	if showTwoPane {
		var detailPanel string
		if len(results.Indices) > 0 {
			m.ensureSelection(results.Indices, &m.selectedAlias)
			alias := aliasEntries[m.selectedAlias]
			detailPanel = m.renderAliasDetail(alias, rightWidth, th)
		} else {
			empty := lipgloss.NewStyle().Foreground(lipgloss.Color(th.TextMuted)).Render("No model family selected.")
			detailPanel = renderPanel(empty, rightWidth, th)
		}
		body = lipgloss.JoinHorizontal(lipgloss.Top, leftPanel, "  ", detailPanel)
	} else {
		body = leftPanel
	}

	hintText := "↑/↓ navigate • Enter select • / search • Esc back"
	if contentWidth >= 60 {
		hintText = "↑/↓ navigate • Enter select • / search • p/c/s/K/v/t filters • Esc back"
	}
	hint := lipgloss.NewStyle().
		Foreground(lipgloss.Color(th.TextMuted)).
		Render(hintText)

	content := lipgloss.JoinVertical(lipgloss.Left, header, "", body, "", hint)
	return lipgloss.NewStyle().Width(maxInt(20, contentWidth+4)).Padding(1, outerPad).Render(content)
}

func (m *ModelSettings) renderAliasVariants(width, height int, th Theme) string {
	if len(m.aliasEntries) == 0 || m.selectedAlias >= len(m.aliasEntries) {
		return m.renderBrowseAliases(width, height, th)
	}

	alias := m.aliasEntries[m.selectedAlias]
	results := m.aliasVariantResults(m.selectedAlias)
	totalItems := len(results.Indices)

	// Calculate responsive width using breakpoints
	contentWidth := width
	if contentWidth <= 0 {
		contentWidth = 120 // Fallback to standard terminal width
	}

	// Apply responsive breakpoints (matches layout.go CalculateResponsiveWidth)
	avW := maxInt(20, contentWidth-4) // Account for padding
	switch {
	case avW < 40:
		contentWidth = maxInt(20, avW)
	case avW <= 200:
		contentWidth = avW
	case avW <= 280:
		contentWidth = 240
	case avW <= 360:
		contentWidth = 280
	default:
		contentWidth = 320
	}
	if contentWidth > 4 {
		contentWidth -= 4
	}

	// At narrow widths, collapse to single-pane
	avShowTwoPane := contentWidth >= 60
	if !avShowTwoPane {
		contentWidth = maxInt(20, width)
	}

	avOuterPad := 2
	if width < 50 {
		avOuterPad = 1
	}

	var avLeftWidth, avRightWidth, avPanelInnerWidth int
	if avShowTwoPane {
		avLeftWidth = int(float64(contentWidth) * 0.44)
		if avLeftWidth < 20 {
			avLeftWidth = 20
		}
		avRightWidth = maxInt(20, contentWidth-avLeftWidth-2)
		avPanelInnerWidth = maxInt(10, avLeftWidth-4)
	} else {
		avLeftWidth = maxInt(20, contentWidth-2)
		avRightWidth = 0
		avPanelInnerWidth = maxInt(10, avLeftWidth-4)
	}

	header := m.renderToolbar(maxInt(20, contentWidth), th)
	tabs := m.renderTabChips(th)
	filters := m.renderFilterChips(true, th)
	listHeader := renderHeaderRow(avPanelInnerWidth, tabs, filters)

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
	contentLineWidth := maxInt(10, avPanelInnerWidth-2)

	var items []string
	if listHeader != "" {
		items = append(items, listHeader)
	}
	titleLine := lipgloss.NewStyle().
		Foreground(lipgloss.Color(th.TextMuted)).
		Bold(true).
		Render(fmt.Sprintf("Providers for %s", alias.DisplayName))
	items = append(items, titleLine)

	if totalItems == 0 {
		items = append(items, lipgloss.NewStyle().
			Foreground(lipgloss.Color(th.TextMuted)).
			Italic(true).
			Render("No providers match your search"))
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
			items = append(items, renderGroupHeaderLine(groupLabel, avPanelInnerWidth, indexOfInt(group.Indices, m.selectedVariant) >= 0, th))

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
				badges := m.renderModelBadges(variant.ProviderName, variant.Model.ID, th)
				maxLabelWidth := contentLineWidth
				if badges != "" {
					maxLabelWidth -= lipgloss.Width(badges) + 1
				}
				if maxLabelWidth < 10 {
					maxLabelWidth = 10
				}
				trimmedLabel, trimmedHighlight := trimLabelWithHighlights(providerLabel, highlight, maxLabelWidth)

				nameStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(th.Text))
				if isSelected {
					nameStyle = nameStyle.Bold(true)
				}
				highlightStyle := lipgloss.NewStyle().
					Foreground(lipgloss.Color(th.Primary)).
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
					contextLabel = "n/a"
				}
				metaLeft := fmt.Sprintf("%s • %s", modelLabel, contextLabel)
				tags := commands.InferModelTags(variant.ProviderName, variant.Model)
				metaRight := renderTagChips(tags, th)
				line2 := joinLeftRight(contentLineWidth, metaLeft, metaRight)

				card := strings.Join([]string{
					renderCardLine(line1, avPanelInnerWidth, isSelected, th),
					renderCardLine(line2, avPanelInnerWidth, isSelected, th),
				}, "\n")
				items = append(items, card)
			}
		}
	}

	if totalItems > m.maxVisible {
		scrollLine := lipgloss.NewStyle().
			Foreground(lipgloss.Color(th.TextMuted)).
			Render(fmt.Sprintf("Showing %d-%d of %d", startIdx+1, endIdx, totalItems))
		items = append(items, scrollLine)
	}

	listContent := lipgloss.JoinVertical(lipgloss.Left, items...)
	leftPanel := renderPanel(listContent, avLeftWidth, th)

	var avBody string
	if avShowTwoPane {
		var detailPanel string
		if len(results.Indices) > 0 {
			m.ensureSelection(results.Indices, &m.selectedVariant)
			variant := alias.Variants[m.selectedVariant]
			provider := commands.Provider{Name: variant.ProviderName, DisplayName: variant.ProviderDisplayName, Color: variant.ProviderColor, Available: variant.ProviderAvailable}
			if found := m.providerByName(variant.ProviderName); found != nil {
				provider.APIType = found.APIType
				provider.BaseURL = found.BaseURL
				provider.Available = found.Available
				if provider.DisplayName == "" {
					provider.DisplayName = found.DisplayName
				}
			}
			detailPanel = m.renderModelDetail(provider, variant.Model, avRightWidth, th)
		} else {
			empty := lipgloss.NewStyle().Foreground(lipgloss.Color(th.TextMuted)).Render("No provider selected.")
			detailPanel = renderPanel(empty, avRightWidth, th)
		}
		avBody = lipgloss.JoinHorizontal(lipgloss.Top, leftPanel, "  ", detailPanel)
	} else {
		avBody = leftPanel
	}

	avHintText := "↑/↓ navigate • Enter select • / search • Esc back"
	if contentWidth >= 60 {
		avHintText = "↑/↓ navigate • Enter select • / search • f favorite • p/c/s/K/v/t filters • g/G/E groups • Esc back"
	}
	hint := lipgloss.NewStyle().
		Foreground(lipgloss.Color(th.TextMuted)).
		Render(avHintText)

	content := lipgloss.JoinVertical(lipgloss.Left, header, "", avBody, "", hint)
	return lipgloss.NewStyle().Width(maxInt(20, contentWidth+4)).Padding(1, avOuterPad).Render(content)
}

func (m *ModelSettings) renderManageProviders(width, height int, th Theme) string {
	var lines []string

	mpPad := 2
	if width < 50 {
		mpPad = 1
	}

	headerStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(th.Primary)).
		Bold(true).
		Padding(1, mpPad)
	lines = append(lines, headerStyle.Render("MANAGE PROVIDERS"))

	subtitleStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(th.TextMuted)).
		Italic(true).
		Padding(0, mpPad).
		MarginBottom(1)
	lines = append(lines, subtitleStyle.Render(fmt.Sprintf("%d%s", len(m.providers), i18n.T("settings.residual_models.manage.configured"))))
	if m.isSelectedRefreshable() && m.refreshStatusText != "" {
		var statusColor string = th.Success
		if m.refreshStatusErr {
			statusColor = th.Error
		}
		var statusLine string = lipgloss.NewStyle().
			Foreground(lipgloss.Color(statusColor)).
			Padding(0, mpPad).
			Render(m.refreshStatusText)
		lines = append(lines, statusLine, "")
	}

	lines = append(lines, m.renderSearchLine(th))

	mpDetailPad := 4
	if width < 50 {
		mpDetailPad = 2
	}

	var results listResults = m.providerResults()
	var totalItems int = len(results.Indices)
	var startIdx int = m.scrollOffset
	if startIdx > totalItems {
		startIdx = totalItems
	}
	var endIdx int = startIdx + m.maxVisible
	if endIdx > totalItems {
		endIdx = totalItems
	}

	for pos := startIdx; pos < endIdx; pos++ {
		var idx int = results.Indices[pos]
		var prov commands.Provider = m.providers[idx]
		var isSelected bool = idx == m.selectedProvider
		var nameStyle lipgloss.Style = lipgloss.NewStyle().
			Foreground(lipgloss.Color(th.Text))
		if isSelected {
			nameStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color(th.Primary)).
				Bold(true)
		}
		var highlightStyle lipgloss.Style = lipgloss.NewStyle().
			Foreground(lipgloss.Color(th.Warning)).
			Bold(true)
		var label string = prov.DisplayName
		if label == "" {
			label = prov.Name
		}
		var renderedLabel string = highlightText(label, results.Highlights[idx], nameStyle, highlightStyle)

		if isSelected {
			var provLine string = nameStyle.Render("▶ ") + renderedLabel

			var detailsText string = fmt.Sprintf("%d%s  •  e=edit  d=delete  Enter=view models", len(prov.Models), i18n.T("settings.residual_models.browse.models_suffix"))
			if width < 50 {
				detailsText = fmt.Sprintf("%d%s • e/d/Enter", len(prov.Models), i18n.T("settings.residual_models.browse.models_suffix"))
			}
			if strings.EqualFold(prov.Name, "openrouter") {
				detailsText = fmt.Sprintf("%d%s  •  r=refresh  •  e=edit  d=delete  Enter=view models", len(prov.Models), i18n.T("settings.residual_models.browse.models_suffix"))
				if width < 50 {
					detailsText = fmt.Sprintf("%d%s • r/e/d/Enter", len(prov.Models), i18n.T("settings.residual_models.browse.models_suffix"))
				}
			}
			detailsLine := lipgloss.NewStyle().
				Foreground(lipgloss.Color(th.TextMuted)).
				Padding(0, mpDetailPad).
				Render(detailsText)

			lines = append(lines, provLine, detailsLine, "")
		} else {
			var provLine string = nameStyle.Render("  ") + renderedLabel
			lines = append(lines, provLine)
		}
	}

	if totalItems == 0 {
		noResults := lipgloss.NewStyle().
			Foreground(lipgloss.Color(th.TextMuted)).
			Italic(true).
			Padding(0, mpPad).
			Render("No providers match your search")
		lines = append(lines, noResults)
	}

	if totalItems > m.maxVisible {
		scrollInfo := fmt.Sprintf("Showing %d-%d of %d", startIdx+1, endIdx, totalItems)
		lines = append(lines, "",
			lipgloss.NewStyle().
				Foreground(lipgloss.Color(th.TextMuted)).
				Italic(true).
				Padding(0, mpPad).
				Render(scrollInfo))
	}

	// Add keyboard hints
	hintStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(th.TextMuted)).
		Padding(1, mpPad)
	var hintText string
	if width < 50 {
		hintText = "↑/↓ e d Enter / Esc"
	} else {
		hintText = "↑/↓ navigate • e edit • d delete • Enter view models • / search • tab autocomplete • Esc back"
	}
	if m.isSelectedRefreshable() && width >= 50 {
		hintText = "↑/↓ navigate • r refresh • e edit • d delete • Enter view models • / search • tab autocomplete • Esc back"
	}
	lines = append(lines, "", hintStyle.Render(hintText))

	content := lipgloss.JoinVertical(lipgloss.Left, lines...)
	return lipgloss.NewStyle().Width(maxInt(20, width)).Height(maxInt(5, height)).Render(content)
}
