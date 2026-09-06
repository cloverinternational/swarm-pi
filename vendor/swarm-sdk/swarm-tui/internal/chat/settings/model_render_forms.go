package settings

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/chat/commands"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/i18n"
	"github.com/charmbracelet/x/ansi"
)

func (m *ModelSettings) renderAddProvider(width, height int, th Theme) string {
	var lines []string

	headerStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(th.Primary)).
		Bold(true).
		Padding(1, 2)
	lines = append(lines, headerStyle.Render(i18n.T("settings.residual_final.forms.add_provider")))

	infoStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(th.TextMuted)).
		Italic(true).
		Padding(0, 2).
		MarginBottom(1)
	lines = append(lines, infoStyle.Render(i18n.T("settings.residual_final.forms.provider_description")), "")

	// Field 0: Provider Type (cycle with arrows)
	isSelected := m.formField == 0
	labelStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(th.Text)).
		Bold(isSelected).
		Padding(0, 2)
	if isSelected {
		labelStyle = labelStyle.Foreground(lipgloss.Color(th.Primary))
	}
	lines = append(lines, labelStyle.Render(i18n.T("settings.residual_final.forms.selected_label", map[bool]string{true: "▶", false: " "}[isSelected], i18n.T("settings.residual_final.forms.provider_type"))))

	hintStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(th.TextMuted)).
		Padding(0, 4)
	lines = append(lines, hintStyle.Render(i18n.T("settings.residual_final.forms.provider_type_hint")))

	valueStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(th.Text)).
		Background(lipgloss.Color(th.BGLight)).
		Padding(0, 2).
		Margin(0, 4)
	if isSelected {
		valueStyle = valueStyle.Border(lipgloss.NormalBorder()).BorderForeground(lipgloss.Color(th.Primary))
	}
	lines = append(lines, valueStyle.Render(fmt.Sprintf("< %s >", m.formProviderType)), "")

	// Field 1: Display Name
	isSelected = m.formField == 1
	isEditing := m.formEditing && m.formField == 1
	labelStyle = lipgloss.NewStyle().
		Foreground(lipgloss.Color(th.Text)).
		Bold(isSelected).
		Padding(0, 2)
	if isSelected {
		labelStyle = labelStyle.Foreground(lipgloss.Color(th.Primary))
	}
	lines = append(lines, labelStyle.Render(i18n.T("settings.residual_final.forms.selected_label", map[bool]string{true: "▶", false: " "}[isSelected], i18n.T("settings.residual_final.forms.display_name"))))
	hintText := i18n.T("settings.residual_final.forms.provider_name_hint")
	if isSelected && !isEditing {
		hintText += i18n.T("settings.residual_models.form.press_edit")
	} else if isEditing {
		hintText = i18n.T("settings.residual_models.form.editing_save")
	}
	lines = append(lines, hintStyle.Render(hintText))

	valueStyle = lipgloss.NewStyle().
		Foreground(lipgloss.Color(th.Text)).
		Background(lipgloss.Color(th.BGLight)).
		Padding(0, 2).
		Margin(0, 4).
		Width(width - 12)
	if isSelected {
		valueStyle = valueStyle.Border(lipgloss.NormalBorder()).BorderForeground(lipgloss.Color(th.Primary))
	}
	if isEditing {
		valueStyle = valueStyle.Background(lipgloss.Color(th.BGLighter)).BorderForeground(lipgloss.Color(th.Success))
	}
	displayName := m.formDisplayName
	if displayName == "" {
		displayName = i18n.T("settings.residual_models.form.type_here")
	}
	// Show cursor if editing
	if isEditing && m.formCursorPos <= len(m.formDisplayName) {
		displayName = m.formDisplayName[:m.formCursorPos] + "│" + m.formDisplayName[m.formCursorPos:]
	}
	lines = append(lines, valueStyle.Render(displayName), "")

	// Field 2: API Endpoint (ALWAYS shown and editable)
	isSelected = m.formField == 2
	isEditing = m.formEditing && m.formField == 2
	labelStyle = lipgloss.NewStyle().
		Foreground(lipgloss.Color(th.Text)).
		Bold(isSelected).
		Padding(0, 2)
	if isSelected {
		labelStyle = labelStyle.Foreground(lipgloss.Color(th.Primary))
	}
	lines = append(lines, labelStyle.Render(i18n.T("settings.residual_final.forms.selected_label", map[bool]string{true: "▶", false: " "}[isSelected], i18n.T("settings.residual_final.forms.api_endpoint"))))
	hintText = i18n.T("settings.residual_final.forms.endpoint_hint")
	if isSelected && !isEditing {
		hintText += i18n.T("settings.residual_models.form.press_edit")
	} else if isEditing {
		hintText = i18n.T("settings.residual_models.form.editing_save")
	}
	lines = append(lines, hintStyle.Render(hintText))

	valueStyle = lipgloss.NewStyle().
		Foreground(lipgloss.Color(th.Text)).
		Background(lipgloss.Color(th.BGLight)).
		Padding(0, 2).
		Margin(0, 4).
		Width(width - 12)
	if isSelected {
		valueStyle = valueStyle.Border(lipgloss.NormalBorder()).BorderForeground(lipgloss.Color(th.Primary))
	}
	if isEditing {
		valueStyle = valueStyle.Background(lipgloss.Color(th.BGLighter)).BorderForeground(lipgloss.Color(th.Success))
	}
	endpointDisplay := m.formAPIEndpoint
	if endpointDisplay == "" {
		endpointDisplay = i18n.T("settings.residual_models.form.type_here")
	}
	// Show cursor if editing
	if isEditing && m.formCursorPos <= len(m.formAPIEndpoint) {
		endpointDisplay = m.formAPIEndpoint[:m.formCursorPos] + "│" + m.formAPIEndpoint[m.formCursorPos:]
	}
	lines = append(lines, valueStyle.Render(endpointDisplay), "")

	// Field 3: Auth Method
	isSelected = m.formField == 3
	labelStyle = lipgloss.NewStyle().
		Foreground(lipgloss.Color(th.Text)).
		Bold(isSelected).
		Padding(0, 2)
	if isSelected {
		labelStyle = labelStyle.Foreground(lipgloss.Color(th.Primary))
	}
	lines = append(lines, labelStyle.Render(i18n.T("settings.residual_final.forms.selected_label", map[bool]string{true: "▶", false: " "}[isSelected], i18n.T("settings.residual_final.forms.auth_method"))))
	var normalizedAuthType string = normalizeAuthType(m.formAuthType)
	if supportsOAuth(m.formProviderType) {
		hintText = i18n.T("settings.residual_final.forms.auth_choose")
	} else if normalizedAuthType == "oauth" {
		hintText = i18n.T("settings.residual_final.forms.oauth_fixed")
	} else {
		hintText = i18n.T("settings.residual_final.forms.api_key_fixed")
	}
	lines = append(lines, hintStyle.Render(hintText))

	valueStyle = lipgloss.NewStyle().
		Foreground(lipgloss.Color(th.Text)).
		Background(lipgloss.Color(th.BGLight)).
		Padding(0, 2).
		Margin(0, 4)
	if isSelected {
		valueStyle = valueStyle.Border(lipgloss.NormalBorder()).BorderForeground(lipgloss.Color(th.Primary))
	}

	var authDisplay string = i18n.T("settings.residual_final.forms.api_key")
	if normalizedAuthType == "oauth" {
		authDisplay = "OAuth"
	}
	if !supportsOAuth(m.formProviderType) {
		if normalizedAuthType == "oauth" {
			authDisplay = i18n.T("settings.residual_final.forms.oauth_fixed_value")
		} else {
			authDisplay = i18n.T("settings.residual_final.forms.api_key_fixed_value")
		}
	}
	lines = append(lines, valueStyle.Render(fmt.Sprintf("< %s >", authDisplay)), "")

	// Field 4: API Key
	isSelected = m.formField == 4
	isEditing = m.formEditing && m.formField == 4
	labelStyle = lipgloss.NewStyle().
		Foreground(lipgloss.Color(th.Text)).
		Bold(isSelected).
		Padding(0, 2)
	if isSelected {
		labelStyle = labelStyle.Foreground(lipgloss.Color(th.Primary))
	}
	lines = append(lines, labelStyle.Render(i18n.T("settings.residual_final.forms.selected_label", map[bool]string{true: "▶", false: " "}[isSelected], i18n.T("settings.residual_final.forms.api_key"))))
	hintText = i18n.T("settings.residual_final.forms.api_key_for", m.formProviderType)
	if supportsOAuth(m.formProviderType) && normalizeAuthType(m.formAuthType) == "oauth" {
		hintText = i18n.T("settings.residual_final.forms.api_key_ignored")
	}
	if isSelected && !isEditing {
		hintText += i18n.T("settings.residual_models.form.press_edit")
	} else if isEditing {
		hintText = i18n.T("settings.residual_models.form.editing_save")
	}
	lines = append(lines, hintStyle.Render(hintText))

	valueStyle = lipgloss.NewStyle().
		Foreground(lipgloss.Color(th.Text)).
		Background(lipgloss.Color(th.BGLight)).
		Padding(0, 2).
		Margin(0, 4).
		Width(width - 12)
	if isSelected {
		valueStyle = valueStyle.Border(lipgloss.NormalBorder()).BorderForeground(lipgloss.Color(th.Primary))
	}
	if isEditing {
		valueStyle = valueStyle.Background(lipgloss.Color(th.BGLighter)).BorderForeground(lipgloss.Color(th.Success))
	}

	// Show API key — masked when not actively editing for security.
	// Always show a preview so the user can confirm a key is set.
	apiKeyDisplay := m.formAPIKey
	if apiKeyDisplay == "" {
		apiKeyDisplay = i18n.T("settings.residual_models.form.type_here")
	} else if !isEditing {
		// Mask the key regardless of length:
		//   > 8 chars  	 first4…****…last4
		//   5-8 chars  	 first2…****
		//   ≤ 4 chars  	 all stars (don't reveal a short secret)
		switch {
		case len(apiKeyDisplay) > 8:
			apiKeyDisplay = apiKeyDisplay[:4] + strings.Repeat("*", len(apiKeyDisplay)-8) + apiKeyDisplay[len(apiKeyDisplay)-4:]
		case len(apiKeyDisplay) > 4:
			apiKeyDisplay = apiKeyDisplay[:2] + strings.Repeat("*", len(apiKeyDisplay)-2)
		default:
			apiKeyDisplay = strings.Repeat("*", len(apiKeyDisplay))
		}
	}
	// Show cursor if editing
	if isEditing && m.formCursorPos <= len(m.formAPIKey) {
		apiKeyDisplay = m.formAPIKey[:m.formCursorPos] + "│" + m.formAPIKey[m.formCursorPos:]
	}
	lines = append(lines, valueStyle.Render(apiKeyDisplay), "")

	// Field 5: Color (with preview)
	isSelected = m.formField == 5
	labelStyle = lipgloss.NewStyle().
		Foreground(lipgloss.Color(th.Text)).
		Bold(isSelected).
		Padding(0, 2)
	if isSelected {
		labelStyle = labelStyle.Foreground(lipgloss.Color(th.Primary))
	}
	lines = append(lines, labelStyle.Render(i18n.T("settings.residual_final.forms.selected_label", map[bool]string{true: "▶", false: " "}[isSelected], i18n.T("settings.residual_final.forms.color"))))
	lines = append(lines, hintStyle.Render(i18n.T("settings.residual_final.forms.color_hint")))

	// Color preview
	currentColor := colorPresets[m.formColorIndex]
	previewStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(currentColor.hex)).
		Bold(true).
		Padding(0, 2).
		Margin(0, 4)
	if isSelected {
		previewStyle = previewStyle.Border(lipgloss.NormalBorder()).BorderForeground(lipgloss.Color(th.Primary))
	}
	colorPreview := fmt.Sprintf("< %s ■■■ >", currentColor.name)
	lines = append(lines, previewStyle.Render(colorPreview), "")

	// Save button
	isSelected = m.formField == 6
	btnStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(th.Text)).
		Background(lipgloss.Color(th.Primary)).
		Bold(true).
		Padding(0, 3).
		Margin(1, 4)
	if isSelected {
		btnStyle = btnStyle.Border(lipgloss.NormalBorder()).BorderForeground(lipgloss.Color(th.Success))
	}
	lines = append(lines, btnStyle.Render(i18n.T("settings.residual_final.forms.save_provider")))

	// Bottom hint bar matching conventions of other settings forms
	hintBarStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(th.TextMuted)).
		Padding(0, 2).
		MarginTop(1)
	lines = append(lines, hintBarStyle.Render(i18n.T("settings.residual_final.forms.form_hint")))

	content := lipgloss.JoinVertical(lipgloss.Left, lines...)
	return lipgloss.NewStyle().Width(width).Height(height).Render(content)
}

func (m *ModelSettings) renderEditProvider(width, height int, th Theme) string {
	if m.selectedProviderIsPlexus() {
		return m.renderPlexusProvider(width, height, th)
	}
	// Same as add provider but with "Edit" title
	return strings.Replace(m.renderAddProvider(width, height, th), i18n.T("settings.residual_final.forms.add_provider"), i18n.T("settings.residual_final.forms.edit_provider"), 1)
}

func (m *ModelSettings) renderProviderModels(width, height int, th Theme) string {
	if len(m.providers) == 0 || m.selectedProvider >= len(m.providers) {
		return ""
	}
	provider := m.providers[m.selectedProvider]
	results := m.providerModelResults(m.selectedProvider)
	totalItems := len(results.Indices)

	// Calculate responsive width using breakpoints
	contentWidth := width
	if contentWidth <= 0 {
		contentWidth = 120 // Fallback to standard terminal width
	}

	// Apply responsive breakpoints (matches layout.go CalculateResponsiveWidth)
	w := contentWidth - 4 // Account for padding
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
	if contentWidth > 4 {
		contentWidth -= 4
	}
	if contentWidth < 60 {
		contentWidth = width
	}
	leftWidth := int(float64(contentWidth) * 0.44)
	if leftWidth < 32 {
		leftWidth = 32
	}
	rightWidth := contentWidth - leftWidth - 2
	if rightWidth < 24 {
		rightWidth = contentWidth - leftWidth - 1
	}

	panelInnerWidth := leftWidth - 4
	if panelInnerWidth < 10 {
		panelInnerWidth = leftWidth
	}
	header := m.renderToolbar(contentWidth, th)
	tabs := m.renderTabChips(th)
	filters := m.renderFilterChips(false, th)
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
	contentLineWidth := panelInnerWidth - 2
	if contentLineWidth < 10 {
		contentLineWidth = panelInnerWidth
	}

	var items []string
	if listHeader != "" {
		items = append(items, listHeader)
	}
	titleLine := lipgloss.NewStyle().
		Foreground(lipgloss.Color(th.TextMuted)).
		Bold(true).
		Render(provider.DisplayName + i18n.T("settings.residual_models.browse.models_suffix"))
	items = append(items, titleLine)
	if m.isSelectedRefreshable() && m.refreshStatusText != "" {
		statusColor := th.Success
		if m.refreshStatusErr {
			statusColor = th.Error
		}
		items = append(items, lipgloss.NewStyle().
			Foreground(lipgloss.Color(statusColor)).
			Render(m.refreshStatusText), "")
	}

	if totalItems == 0 {
		items = append(items, lipgloss.NewStyle().
			Foreground(lipgloss.Color(th.TextMuted)).
			Italic(true).
			Render(i18n.T("settings.residual_models.browse.no_model_match")))
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
			items = append(items, renderGroupHeaderLine(groupLabel, panelInnerWidth, indexOfInt(group.Indices, m.selectedModel) >= 0, th))

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
				badges := m.renderModelBadges(provider.Name, model.ID, th)
				maxLabelWidth := contentLineWidth
				if badges != "" {
					maxLabelWidth -= lipgloss.Width(badges) + 1
				}
				trimmedLabel, trimmedHighlight := trimLabelWithHighlights(label, highlight, maxLabelWidth)

				nameStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(th.Text))
				if isSelected {
					nameStyle = nameStyle.Bold(true)
				}
				highlightStyle := lipgloss.NewStyle().
					Foreground(lipgloss.Color(th.Primary)).
					Bold(true)
				renderedLabel := highlightText(trimmedLabel, trimmedHighlight, nameStyle, highlightStyle)
				line1 := joinLeftRight(contentLineWidth, renderedLabel, badges)

				contextLabel := strings.TrimSpace(model.Context)
				if contextLabel == "" && model.ContextWindow > 0 {
					contextLabel = formatContextWindow(model.ContextWindow)
				}
				if contextLabel == "" {
					contextLabel = "n/a"
				}
				metaLeft := i18n.T("settings.residual_models.browse.context") + contextLabel
				tags := commands.InferModelTags(provider.Name, model)
				metaRight := renderTagChips(tags, th)
				line2 := joinLeftRight(contentLineWidth, metaLeft, metaRight)

				card := strings.Join([]string{
					renderCardLine(line1, panelInnerWidth, isSelected, th),
					renderCardLine(line2, panelInnerWidth, isSelected, th),
				}, "\n")
				items = append(items, card)
			}
		}
	}

	if totalItems > m.maxVisible {
		scrollLine := lipgloss.NewStyle().
			Foreground(lipgloss.Color(th.TextMuted)).
			Render(i18n.T("settings.common.showing_range", startIdx+1, endIdx, totalItems))
		items = append(items, scrollLine)
	}

	listContent := lipgloss.JoinVertical(lipgloss.Left, items...)
	leftPanel := renderPanel(listContent, leftWidth, th)

	var detailPanel string
	if len(results.Indices) > 0 {
		m.ensureSelection(results.Indices, &m.selectedModel)
		model := provider.Models[m.selectedModel]
		detailPanel = m.renderModelDetail(provider, model, rightWidth, th)
	} else {
		empty := lipgloss.NewStyle().Foreground(lipgloss.Color(th.TextMuted)).Render(i18n.T("settings.residual_models.browse.model_none"))
		detailPanel = renderPanel(empty, rightWidth, th)
	}

	body := lipgloss.JoinHorizontal(lipgloss.Top, leftPanel, "  ", detailPanel)
	hint := lipgloss.NewStyle().
		Foreground(lipgloss.Color(th.TextMuted)).
		Render(i18n.T("settings.residual_models.form.model_list_hint"))
	if strings.EqualFold(provider.Name, "openrouter") {
		hint = lipgloss.NewStyle().
			Foreground(lipgloss.Color(th.TextMuted)).
			Render(i18n.T("settings.residual_models.form.model_list_refresh_hint"))
	}

	content := lipgloss.JoinVertical(lipgloss.Left, header, "", body, "", hint)
	return lipgloss.NewStyle().Width(contentWidth+4).Padding(1, 2).Render(content)
}

func (m *ModelSettings) renderModelPicker(width, height int, th Theme) string {
	if len(m.providers) == 0 || m.selectedProvider >= len(m.providers) {
		return ""
	}

	provider := m.providers[m.selectedProvider]
	results := m.providerModelResults(m.selectedProvider)
	totalItems := len(results.Indices)

	// Calculate responsive width using breakpoints
	contentWidth := width
	if contentWidth <= 0 {
		contentWidth = 120 // Fallback to standard terminal width
	}

	// Apply responsive breakpoints (matches layout.go CalculateResponsiveWidth)
	w := contentWidth - 4 // Account for padding
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
	if contentWidth > 4 {
		contentWidth -= 4
	}
	if contentWidth < 60 {
		contentWidth = width
	}
	leftWidth := int(float64(contentWidth) * 0.44)
	if leftWidth < 32 {
		leftWidth = 32
	}
	rightWidth := contentWidth - leftWidth - 2
	if rightWidth < 24 {
		rightWidth = contentWidth - leftWidth - 1
	}

	panelInnerWidth := leftWidth - 4
	if panelInnerWidth < 10 {
		panelInnerWidth = leftWidth
	}
	header := m.renderToolbar(contentWidth, th)
	tabs := m.renderTabChips(th)
	filters := m.renderFilterChips(false, th)
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
	contentLineWidth := panelInnerWidth - 2
	if contentLineWidth < 10 {
		contentLineWidth = panelInnerWidth
	}

	var items []string
	if listHeader != "" {
		items = append(items, listHeader)
	}
	titleLine := lipgloss.NewStyle().
		Foreground(lipgloss.Color(th.TextMuted)).
		Bold(true).
		Render(provider.DisplayName + i18n.T("settings.residual_models.browse.models_suffix"))
	items = append(items, titleLine)

	if totalItems == 0 {
		items = append(items, lipgloss.NewStyle().
			Foreground(lipgloss.Color(th.TextMuted)).
			Italic(true).
			Render(i18n.T("settings.residual_models.browse.no_model_match")))
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
			items = append(items, renderGroupHeaderLine(groupLabel, panelInnerWidth, indexOfInt(group.Indices, m.selectedModel) >= 0, th))

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
				badges := m.renderModelBadges(provider.Name, model.ID, th)
				maxLabelWidth := contentLineWidth
				if badges != "" {
					maxLabelWidth -= lipgloss.Width(badges) + 1
				}
				trimmedLabel, trimmedHighlight := trimLabelWithHighlights(label, highlight, maxLabelWidth)

				nameStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(th.Text))
				if isSelected {
					nameStyle = nameStyle.Bold(true)
				}
				highlightStyle := lipgloss.NewStyle().
					Foreground(lipgloss.Color(th.Primary)).
					Bold(true)
				renderedLabel := highlightText(trimmedLabel, trimmedHighlight, nameStyle, highlightStyle)
				line1 := joinLeftRight(contentLineWidth, renderedLabel, badges)

				contextLabel := strings.TrimSpace(model.Context)
				if contextLabel == "" && model.ContextWindow > 0 {
					contextLabel = formatContextWindow(model.ContextWindow)
				}
				if contextLabel == "" {
					contextLabel = "n/a"
				}
				metaLeft := i18n.T("settings.residual_models.browse.context") + contextLabel
				tags := commands.InferModelTags(provider.Name, model)
				metaRight := renderTagChips(tags, th)
				line2 := joinLeftRight(contentLineWidth, metaLeft, metaRight)

				card := strings.Join([]string{
					renderCardLine(line1, panelInnerWidth, isSelected, th),
					renderCardLine(line2, panelInnerWidth, isSelected, th),
				}, "\n")
				items = append(items, card)
			}
		}
	}

	if totalItems > m.maxVisible {
		scrollLine := lipgloss.NewStyle().
			Foreground(lipgloss.Color(th.TextMuted)).
			Render(i18n.T("settings.common.showing_range", startIdx+1, endIdx, totalItems))
		items = append(items, scrollLine)
	}

	listContent := lipgloss.JoinVertical(lipgloss.Left, items...)
	leftPanel := renderPanel(listContent, leftWidth, th)

	var detailPanel string
	if len(results.Indices) > 0 {
		m.ensureSelection(results.Indices, &m.selectedModel)
		model := provider.Models[m.selectedModel]
		detailPanel = m.renderModelDetail(provider, model, rightWidth, th)
	} else {
		empty := lipgloss.NewStyle().Foreground(lipgloss.Color(th.TextMuted)).Render(i18n.T("settings.residual_models.browse.model_none"))
		detailPanel = renderPanel(empty, rightWidth, th)
	}

	body := lipgloss.JoinHorizontal(lipgloss.Top, leftPanel, "  ", detailPanel)
	hintText := i18n.T("settings.residual_models.form.model_list_hint")
	if m.isSelectedRefreshable() {
		hintText = i18n.T("settings.residual_models.form.model_list_refresh_hint")
	}
	hint := lipgloss.NewStyle().
		Foreground(lipgloss.Color(th.TextMuted)).
		Render(hintText)

	content := lipgloss.JoinVertical(lipgloss.Left, header, "", body, "", hint)
	return lipgloss.NewStyle().Width(contentWidth+4).Padding(1, 2).Render(content)
}

func (m *ModelSettings) renderAddModel(width, height int, th Theme) string {
	var lines []string

	headerStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(th.Primary)).
		Bold(true).
		Padding(1, 2)
	lines = append(lines, headerStyle.Render(i18n.T("settings.residual_final.forms.add_model")))

	infoStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(th.TextMuted)).
		Italic(true).
		Padding(0, 2).
		MarginBottom(1)
	lines = append(lines, infoStyle.Render(i18n.T("settings.residual_final.forms.add_model_description")), "")

	hintStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(th.TextMuted)).
		Padding(0, 4)

	// Field 0: Model ID
	isSelected := m.modelFormField == 0
	isEditing := m.formEditing && m.modelFormField == 0
	labelStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(th.Text)).
		Bold(isSelected).
		Padding(0, 2)
	if isSelected {
		labelStyle = labelStyle.Foreground(lipgloss.Color(th.Primary))
	}
	lines = append(lines, labelStyle.Render(i18n.T("settings.residual_final.forms.selected_label", map[bool]string{true: "▶", false: " "}[isSelected], i18n.T("settings.residual_final.forms.model_id"))))
	hintText := i18n.T("settings.residual_final.forms.model_id_hint")
	if isSelected && !isEditing {
		hintText += i18n.T("settings.residual_models.form.press_edit")
	} else if isEditing {
		hintText = i18n.T("settings.residual_models.form.editing_save")
	}
	lines = append(lines, hintStyle.Render(hintText))

	valueStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(th.Text)).
		Background(lipgloss.Color(th.BGLight)).
		Padding(0, 2).
		Margin(0, 4).
		Width(width - 12)
	if isSelected {
		valueStyle = valueStyle.Border(lipgloss.NormalBorder()).BorderForeground(lipgloss.Color(th.Primary))
	}
	if isEditing {
		valueStyle = valueStyle.Background(lipgloss.Color(th.BGLighter)).BorderForeground(lipgloss.Color(th.Success))
	}
	modelID := m.modelFormID
	if modelID == "" {
		modelID = i18n.T("settings.residual_models.form.type_here")
	}
	if isEditing && m.formCursorPos <= len(m.modelFormID) {
		modelID = m.modelFormID[:m.formCursorPos] + "│" + m.modelFormID[m.formCursorPos:]
	}
	lines = append(lines, valueStyle.Render(modelID), "")

	// Field 1: Display Name
	isSelected = m.modelFormField == 1
	isEditing = m.formEditing && m.modelFormField == 1
	labelStyle = lipgloss.NewStyle().
		Foreground(lipgloss.Color(th.Text)).
		Bold(isSelected).
		Padding(0, 2)
	if isSelected {
		labelStyle = labelStyle.Foreground(lipgloss.Color(th.Primary))
	}
	lines = append(lines, labelStyle.Render(i18n.T("settings.residual_final.forms.selected_label", map[bool]string{true: "▶", false: " "}[isSelected], i18n.T("settings.residual_final.forms.display_name"))))
	hintText = i18n.T("settings.residual_final.forms.model_name_hint")
	if isSelected && !isEditing {
		hintText += i18n.T("settings.residual_models.form.press_edit")
	} else if isEditing {
		hintText = i18n.T("settings.residual_models.form.editing_save")
	}
	lines = append(lines, hintStyle.Render(hintText))

	valueStyle = lipgloss.NewStyle().
		Foreground(lipgloss.Color(th.Text)).
		Background(lipgloss.Color(th.BGLight)).
		Padding(0, 2).
		Margin(0, 4).
		Width(width - 12)
	if isSelected {
		valueStyle = valueStyle.Border(lipgloss.NormalBorder()).BorderForeground(lipgloss.Color(th.Primary))
	}
	if isEditing {
		valueStyle = valueStyle.Background(lipgloss.Color(th.BGLighter)).BorderForeground(lipgloss.Color(th.Success))
	}
	displayName := m.modelFormDisplayName
	if displayName == "" {
		displayName = i18n.T("settings.residual_models.form.type_here")
	}
	if isEditing && m.formCursorPos <= len(m.modelFormDisplayName) {
		displayName = m.modelFormDisplayName[:m.formCursorPos] + "│" + m.modelFormDisplayName[m.formCursorPos:]
	}
	lines = append(lines, valueStyle.Render(displayName), "")

	// Field 2: Context
	isSelected = m.modelFormField == 2
	isEditing = m.formEditing && m.modelFormField == 2
	labelStyle = lipgloss.NewStyle().
		Foreground(lipgloss.Color(th.Text)).
		Bold(isSelected).
		Padding(0, 2)
	if isSelected {
		labelStyle = labelStyle.Foreground(lipgloss.Color(th.Primary))
	}
	lines = append(lines, labelStyle.Render(i18n.T("settings.residual_final.forms.selected_label", map[bool]string{true: "▶", false: " "}[isSelected], i18n.T("settings.residual_final.forms.context_window"))))
	hintText = i18n.T("settings.residual_final.forms.context_hint")
	if isSelected && !isEditing {
		hintText += i18n.T("settings.residual_models.form.press_edit")
	} else if isEditing {
		hintText = i18n.T("settings.residual_models.form.editing_save")
	}
	lines = append(lines, hintStyle.Render(hintText))

	valueStyle = lipgloss.NewStyle().
		Foreground(lipgloss.Color(th.Text)).
		Background(lipgloss.Color(th.BGLight)).
		Padding(0, 2).
		Margin(0, 4).
		Width(width - 12)
	if isSelected {
		valueStyle = valueStyle.Border(lipgloss.NormalBorder()).BorderForeground(lipgloss.Color(th.Primary))
	}
	if isEditing {
		valueStyle = valueStyle.Background(lipgloss.Color(th.BGLighter)).BorderForeground(lipgloss.Color(th.Success))
	}
	context := m.modelFormContext
	if context == "" {
		context = i18n.T("settings.residual_models.form.type_here")
	}
	if isEditing && m.formCursorPos <= len(m.modelFormContext) {
		context = m.modelFormContext[:m.formCursorPos] + "│" + m.modelFormContext[m.formCursorPos:]
	}
	lines = append(lines, valueStyle.Render(context), "")

	// ── Thinking configuration (fields 3-5) ──
	// Determine model capabilities from the model ID being edited
	isAdaptive := modelSupportsAdaptiveThinking(m.modelFormID)
	thinkingModeTag := modelThinkingModeLabel(m.modelFormID)

	// Field 3: Thinking Enabled (toggle)
	isSelected = m.modelFormField == 3
	labelStyle = lipgloss.NewStyle().
		Foreground(lipgloss.Color(th.Text)).
		Bold(isSelected).
		Padding(0, 2)
	if isSelected {
		labelStyle = labelStyle.Foreground(lipgloss.Color(th.Primary))
	}
	thinkingLabel := i18n.T("settings.residual_final.forms.extended_thinking_mode",
		map[bool]string{true: "▶", false: " "}[isSelected], thinkingModeTag)
	lines = append(lines, labelStyle.Render(thinkingLabel))
	hintText = i18n.T("settings.residual_final.forms.extended_thinking_hint")
	lines = append(lines, hintStyle.Render(hintText))

	valueStyle = lipgloss.NewStyle().
		Foreground(lipgloss.Color(th.Text)).
		Background(lipgloss.Color(th.BGLight)).
		Padding(0, 2).
		Margin(0, 4).
		Width(width - 12)
	if isSelected {
		valueStyle = valueStyle.Border(lipgloss.NormalBorder()).BorderForeground(lipgloss.Color(th.Primary))
	}
	thinkingStatus := i18n.T("settings.residual_final.common.disabled")
	if m.modelFormThinkingEnabled {
		thinkingStatus = i18n.T("settings.residual_final.common.enabled")
		valueStyle = valueStyle.Foreground(lipgloss.Color(th.Success))
	}
	lines = append(lines, valueStyle.Render(thinkingStatus), "")

	// Fields 4 & 5 only appear when thinking is turned on
	if m.modelFormThinkingEnabled {
		if !isAdaptive {
			// Field 4: Thinking Budget (Only shown for manual/budget models)
			isSelected = m.modelFormField == 4
			isEditing = m.formEditing && m.modelFormField == 4
			labelStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color(th.Text)).
				Bold(isSelected).
				Padding(0, 2)
			if isSelected {
				labelStyle = labelStyle.Foreground(lipgloss.Color(th.Primary))
			}
			budgetLabel := i18n.T("settings.residual_final.forms.thinking_budget")
			lines = append(lines, labelStyle.Render(fmt.Sprintf("%s %s", map[bool]string{true: "▶", false: " "}[isSelected], budgetLabel)))
			hintText = i18n.T("settings.residual_final.forms.thinking_budget_hint")
			if isSelected && !isEditing {
				hintText += i18n.T("settings.residual_final.forms.enter_to_edit")
			} else if isEditing {
				hintText = i18n.T("settings.residual_final.forms.editing_save")
			}
			lines = append(lines, hintStyle.Render(hintText))

			valueStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color(th.Text)).
				Background(lipgloss.Color(th.BGLight)).
				Padding(0, 2).
				Margin(0, 4).
				Width(width - 12)
			if isSelected {
				valueStyle = valueStyle.Border(lipgloss.NormalBorder()).BorderForeground(lipgloss.Color(th.Primary))
			}
			if isEditing {
				valueStyle = valueStyle.Background(lipgloss.Color(th.BGLighter)).BorderForeground(lipgloss.Color(th.Success))
			}
			budget := fmt.Sprintf("%d", m.modelFormThinkingBudget)
			if m.modelFormThinkingBudget == 0 {
				budget = i18n.T("settings.residual_final.forms.default_2048")
			}
			lines = append(lines, valueStyle.Render(budget), "")
		} else {
			// Field 5: Thinking Effort (Only shown for adaptive/level models)
			isSelected = m.modelFormField == 5
			labelStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color(th.Text)).
				Bold(isSelected).
				Padding(0, 2)
			if isSelected {
				labelStyle = labelStyle.Foreground(lipgloss.Color(th.Primary))
			}
			availableEfforts := modelThinkingEfforts(m.modelFormID)
			effortHint := i18n.T("settings.residual_final.forms.effort_hint", strings.Join(availableEfforts[1:], ", "))
			lines = append(lines, labelStyle.Render(i18n.T("settings.residual_final.forms.selected_label", map[bool]string{true: "▶", false: " "}[isSelected], i18n.T("settings.residual_final.forms.thinking_effort"))))
			lines = append(lines, hintStyle.Render(effortHint))

			valueStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color(th.Text)).
				Background(lipgloss.Color(th.BGLight)).
				Padding(0, 2).
				Margin(0, 4).
				Width(width - 12)
			if isSelected {
				valueStyle = valueStyle.Border(lipgloss.NormalBorder()).BorderForeground(lipgloss.Color(th.Primary))
			}
			effort := m.modelFormThinkingEffort
			if effort == "" {
				effort = i18n.T("settings.residual_final.forms.effort_not_set")
			}
			lines = append(lines, valueStyle.Render(effort), "")
		}
	}
	// Field 6: Diffusion model (toggle)
	isSelected = m.modelFormField == 6
	labelStyle = lipgloss.NewStyle().
		Foreground(lipgloss.Color(th.Text)).
		Bold(isSelected).
		Padding(0, 2)
	if isSelected {
		labelStyle = labelStyle.Foreground(lipgloss.Color(th.Primary))
	}
	diffusionLabel := i18n.T("settings.residual_final.forms.diffusion_label", map[bool]string{true: "▶", false: " "}[isSelected])
	lines = append(lines, labelStyle.Render(diffusionLabel))
	hintText = i18n.T("settings.residual_final.forms.diffusion_hint")
	lines = append(lines, hintStyle.Render(hintText))

	valueStyle = lipgloss.NewStyle().
		Foreground(lipgloss.Color(th.Text)).
		Background(lipgloss.Color(th.BGLight)).
		Padding(0, 2).
		Margin(0, 4).
		Width(width - 12)
	if isSelected {
		valueStyle = valueStyle.Border(lipgloss.NormalBorder()).BorderForeground(lipgloss.Color(th.Primary))
	}
	diffusionStatus := i18n.T("settings.residual_final.common.disabled")
	if m.modelFormDiffusion {
		diffusionStatus = i18n.T("settings.residual_final.common.enabled")
		valueStyle = valueStyle.Foreground(lipgloss.Color(th.Success))
	}
	lines = append(lines, valueStyle.Render(diffusionStatus), "")

	lines = append(lines, m.renderGenerationOverrideFields(width-8, th)...)

	// Save button
	isSelected = m.modelFormField == modelFieldSave
	btnStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(th.Text)).
		Background(lipgloss.Color(th.Primary)).
		Bold(true).
		Padding(0, 3).
		Margin(1, 4)
	if isSelected {
		btnStyle = btnStyle.Border(lipgloss.NormalBorder()).BorderForeground(lipgloss.Color(th.Success))
	}
	lines = append(lines, btnStyle.Render(i18n.T("settings.residual_final.forms.save_model")))

	content := lipgloss.JoinVertical(lipgloss.Left, lines...)
	return lipgloss.NewStyle().Width(width).Height(height).Render(content)
}

func (m *ModelSettings) renderEditModel(width, height int, th Theme) string {
	cardWidth := min(76, max(44, width-4))
	innerWidth := cardWidth - 4
	mode, modeColor := m.modelEditorMode(th)
	providerName := m.modelEditorProviderName()

	title := lipgloss.NewStyle().
		Foreground(lipgloss.Color(th.Primary)).
		Bold(true).
		Render(i18n.T("settings.model_editor.title"))
	badge := lipgloss.NewStyle().
		Foreground(lipgloss.Color(th.BG)).
		Background(lipgloss.Color(modeColor)).
		Bold(true).
		Padding(0, 1).
		Render(mode)
	titleLine := title + "  " + badge

	subtitle := providerName
	if strings.TrimSpace(m.modelFormID) != "" {
		subtitle += "  •  " + m.modelFormID
	}
	subtitle = truncateModelEditorText(subtitle, innerWidth)

	lines := []string{
		titleLine,
		lipgloss.NewStyle().Foreground(lipgloss.Color(th.TextMuted)).Render(subtitle),
	}
	lines = append(lines, m.renderCompactModelSection(i18n.T("settings.model_editor.section.identity"), []int{0, 1, 2}, innerWidth, th)...)
	lines = append(lines, m.renderCompactModelSection(i18n.T("settings.model_editor.section.reasoning"), []int{3, 4, 5, 6}, innerWidth, th)...)
	lines = append(lines, m.renderCompactModelSection(i18n.T("settings.model_editor.section.generation"), []int{
		modelFieldTemperature,
		modelFieldMaxTokens,
		modelFieldTopP,
		modelFieldTopK,
	}, innerWidth, th)...)
	lines = append(lines, m.renderCompactModelRow(modelFieldSave, i18n.T("settings.model_editor.save"), "", innerWidth, th))

	help := truncateModelEditorText(m.modelEditorFieldHelp(), innerWidth)
	lines = append(lines, lipgloss.NewStyle().
		Foreground(lipgloss.Color(th.TextMuted)).
		Render(help))
	lines = append(lines, lipgloss.NewStyle().
		Foreground(lipgloss.Color(th.TextMuted)).
		Render(truncateModelEditorText(m.modelEditorFooterHelp(), innerWidth)))

	availableLines := max(10, height-4)
	lines = fitCompactModelEditor(lines, availableLines)
	content := lipgloss.JoinVertical(lipgloss.Left, lines...)
	card := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color(th.Border)).
		Width(cardWidth).
		Padding(0, 2).
		Render(content)
	return lipgloss.NewStyle().Width(width).Align(lipgloss.Center).Render(card)
}

func (m *ModelSettings) modelEditorMode(th Theme) (string, string) {
	if !m.modelFormThinkingEnabled {
		return i18n.T("settings.model_editor.mode.standard"), th.TextMuted
	}
	if modelSupportsAdaptiveThinking(m.modelFormID) {
		return i18n.T("settings.model_editor.mode.adaptive"), th.Success
	}
	return i18n.T("settings.model_editor.mode.manual_thinking"), th.Warning
}

func (m *ModelSettings) modelEditorProviderName() string {
	if m.selectedProvider >= 0 && m.selectedProvider < len(m.providers) {
		if name := strings.TrimSpace(m.providers[m.selectedProvider].Name); name != "" {
			return name
		}
	}
	return i18n.T("settings.model_editor.provider")
}

func (m *ModelSettings) renderCompactModelSection(title string, fields []int, width int, th Theme) []string {
	header := lipgloss.NewStyle().
		Foreground(lipgloss.Color(th.Primary)).
		Bold(true).
		Render(title)
	ruleWidth := max(1, width-lipgloss.Width(header)-1)
	lines := []string{header + " " + lipgloss.NewStyle().
		Foreground(lipgloss.Color(th.Border)).
		Render(strings.Repeat("─", ruleWidth))}
	for _, field := range fields {
		label, value := m.compactModelField(field)
		lines = append(lines, m.renderCompactModelRow(field, label, value, width, th))
	}
	return lines
}

func (m *ModelSettings) renderCompactModelRow(field int, label, value string, width int, th Theme) string {
	selected := m.modelFormField == field
	disabled := m.modelEditorFieldDisabled(field)
	cursor := "  "
	if selected {
		cursor = "▶ "
	}
	labelWidth := min(22, max(20, width*2/5))
	valueWidth := max(8, width-labelWidth-5)
	if field == modelFieldSave {
		button := "  " + label + "  "
		style := lipgloss.NewStyle().
			Foreground(lipgloss.Color(th.BG)).
			Background(lipgloss.Color(th.Primary)).
			Bold(true).
			Padding(0, 1)
		if selected {
			style = style.Background(lipgloss.Color(th.Success))
		}
		return cursor + style.Render(button)
	}

	labelStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(th.Text)).
		Width(labelWidth)
	valueStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(th.Text)).
		Background(lipgloss.Color(th.BGLight)).
		Width(valueWidth).
		Padding(0, 1)
	if selected {
		labelStyle = labelStyle.Foreground(lipgloss.Color(th.Primary)).Bold(true)
		valueStyle = valueStyle.Background(lipgloss.Color(th.BGLighter))
	}
	if disabled {
		labelStyle = labelStyle.Foreground(lipgloss.Color(th.TextMuted))
		valueStyle = valueStyle.Foreground(lipgloss.Color(th.TextMuted))
	}
	value = truncateModelEditorText(value, max(1, valueWidth-2))
	return cursor + labelStyle.Render(label) + " " + valueStyle.Render(value)
}

func (m *ModelSettings) compactModelField(field int) (string, string) {
	auto := i18n.T("settings.model_editor.value.auto")
	caps := m.generationCapabilities()
	switch field {
	case 0:
		return i18n.T("settings.model_editor.field.model_id"), compactModelTextValue(m.modelFormID, m.formEditing && m.modelFormField == field, m.formCursorPos)
	case 1:
		return i18n.T("settings.model_editor.field.display_name"), compactModelTextValue(m.modelFormDisplayName, m.formEditing && m.modelFormField == field, m.formCursorPos)
	case 2:
		value := compactModelTextValue(m.modelFormContext, m.formEditing && m.modelFormField == field, m.formCursorPos)
		if strings.TrimSpace(m.modelFormContext) == "" && !m.formEditing {
			value = auto
		}
		return i18n.T("settings.model_editor.field.context_window"), value
	case 3:
		return i18n.T("settings.model_editor.field.extended_thinking"), compactOnOff(m.modelFormThinkingEnabled)
	case 4:
		if modelSupportsAdaptiveThinking(m.modelFormID) {
			return i18n.T("settings.model_editor.field.thinking_budget"), i18n.T("settings.model_editor.value.managed")
		}
		if !m.modelFormThinkingEnabled {
			return i18n.T("settings.model_editor.field.thinking_budget"), i18n.T("settings.model_editor.value.inactive")
		}
		return i18n.T("settings.model_editor.field.thinking_budget"), i18n.T("settings.model_editor.value.tokens", max(1024, m.modelFormThinkingBudget))
	case 5:
		if !m.modelFormThinkingEnabled {
			return i18n.T("settings.model_editor.field.reasoning_effort"), i18n.T("settings.model_editor.value.inactive")
		}
		effort := strings.TrimSpace(m.modelFormThinkingEffort)
		if effort == "" {
			effort = i18n.T("settings.model_editor.value.high_default")
		}
		return i18n.T("settings.model_editor.field.reasoning_effort"), effort
	case 6:
		return i18n.T("settings.model_editor.field.diffusion"), compactOnOff(m.modelFormDiffusion)
	case modelFieldTemperature:
		if !caps.temperature {
			return i18n.T("settings.model_editor.field.temperature"), i18n.T("settings.model_editor.value.unsupported")
		}
		if m.modelFormTemperature == nil {
			return i18n.T("settings.model_editor.field.temperature"), auto
		}
		return i18n.T("settings.model_editor.field.temperature"), fmt.Sprintf("%.2f", *m.modelFormTemperature)
	case modelFieldMaxTokens:
		if !caps.maxTokens {
			return i18n.T("settings.model_editor.field.max_tokens"), i18n.T("settings.model_editor.value.unsupported")
		}
		if m.modelFormMaxTokens == 0 {
			return i18n.T("settings.model_editor.field.max_tokens"), auto
		}
		return i18n.T("settings.model_editor.field.max_tokens"), fmt.Sprintf("%d", m.modelFormMaxTokens)
	case modelFieldTopP:
		if !caps.topP {
			return i18n.T("settings.model_editor.field.top_p"), i18n.T("settings.model_editor.value.unsupported")
		}
		if m.modelFormTopP == nil {
			return i18n.T("settings.model_editor.field.top_p"), auto
		}
		return i18n.T("settings.model_editor.field.top_p"), fmt.Sprintf("%.2f", *m.modelFormTopP)
	case modelFieldTopK:
		if !caps.topK {
			return i18n.T("settings.model_editor.field.top_k"), i18n.T("settings.model_editor.value.unsupported")
		}
		if m.modelFormTopK == nil {
			return i18n.T("settings.model_editor.field.top_k"), auto
		}
		return i18n.T("settings.model_editor.field.top_k"), fmt.Sprintf("%d", *m.modelFormTopK)
	default:
		return "", ""
	}
}

func (m *ModelSettings) modelEditorFieldDisabled(field int) bool {
	if field == 4 {
		return !m.modelFormThinkingEnabled || modelSupportsAdaptiveThinking(m.modelFormID)
	}
	if field == 5 {
		return !m.modelFormThinkingEnabled
	}
	if field >= modelFieldTemperature && field <= modelFieldTopK {
		return !m.generationCapabilities().supports(field)
	}
	return false
}

func (m *ModelSettings) modelEditorFieldHelp() string {
	switch m.modelFormField {
	case 0:
		return i18n.T("settings.model_editor.help.model_id")
	case 1:
		return i18n.T("settings.model_editor.help.display_name")
	case 2:
		return i18n.T("settings.model_editor.help.context_window")
	case 3:
		return i18n.T("settings.model_editor.help.extended_thinking")
	case 4:
		if modelSupportsAdaptiveThinking(m.modelFormID) {
			return i18n.T("settings.model_editor.help.thinking_managed")
		}
		if !m.modelFormThinkingEnabled {
			return i18n.T("settings.model_editor.help.thinking_disabled")
		}
		return i18n.T("settings.model_editor.help.thinking_budget")
	case 5:
		return i18n.T("settings.model_editor.help.reasoning_effort")
	case 6:
		return i18n.T("settings.model_editor.help.diffusion")
	case modelFieldTemperature:
		return i18n.T("settings.model_editor.help.temperature")
	case modelFieldMaxTokens:
		return i18n.T("settings.model_editor.help.max_tokens")
	case modelFieldTopP:
		return i18n.T("settings.model_editor.help.top_p")
	case modelFieldTopK:
		return i18n.T("settings.model_editor.help.top_k")
	case modelFieldSave:
		return i18n.T("settings.model_editor.help.save")
	default:
		return ""
	}
}

func (m *ModelSettings) modelEditorFooterHelp() string {
	switch m.modelFormField {
	case 0, 1, 2:
		return i18n.T("settings.model_editor.footer.edit")
	case 3, 6:
		return i18n.T("settings.model_editor.footer.toggle")
	case 4:
		if m.modelEditorFieldDisabled(4) {
			return i18n.T("settings.model_editor.footer.enable_thinking")
		}
		return i18n.T("settings.model_editor.footer.edit")
	case 5:
		if m.modelEditorFieldDisabled(5) {
			return i18n.T("settings.model_editor.footer.enable_thinking")
		}
		return i18n.T("settings.model_editor.footer.cycle")
	case modelFieldTemperature, modelFieldMaxTokens, modelFieldTopP, modelFieldTopK:
		if m.modelEditorFieldDisabled(m.modelFormField) {
			return i18n.T("settings.model_editor.footer.unsupported")
		}
		return i18n.T("settings.model_editor.footer.adjust")
	case modelFieldSave:
		return i18n.T("settings.model_editor.footer.save")
	default:
		return i18n.T("settings.model_editor.footer.move")
	}
}

func compactModelTextValue(value string, editing bool, cursor int) string {
	if value == "" {
		if editing {
			return "│"
		}
		return i18n.T("settings.model_editor.value.not_set")
	}
	if editing && cursor >= 0 && cursor <= len(value) {
		return value[:cursor] + "│" + value[cursor:]
	}
	return value
}

func compactOnOff(enabled bool) string {
	if enabled {
		return i18n.T("settings.model_editor.value.on")
	}
	return i18n.T("settings.model_editor.value.off")
}

func truncateModelEditorText(value string, width int) string {
	if width <= 0 {
		return ""
	}
	if ansi.StringWidth(value) <= width {
		return value
	}
	if width == 1 {
		return "…"
	}
	return ansi.Truncate(value, width-1, "") + "…"
}

func fitCompactModelEditor(lines []string, height int) []string {
	if height <= 0 || len(lines) <= height {
		return lines
	}
	focus := 0
	for i, line := range lines {
		if strings.Contains(line, "▶") {
			focus = i
			break
		}
	}
	start := max(0, focus-height/2)
	start = min(start, len(lines)-height)
	return lines[start : start+height]
}

// fitModelEditorPanel keeps the selected row visible when a short terminal
// cannot show an entire editor pane. Renderers mark selected rows with ▶; Save
// is the final left-pane control and requests an end-aligned window.
func fitModelEditorPanel(lines []string, height int, preferEnd bool) []string {
	if height <= 0 || len(lines) <= height {
		return lines
	}
	focus := 0
	if preferEnd {
		focus = len(lines) - 1
	} else {
		for i, line := range lines {
			if strings.Contains(line, "▶") {
				focus = i
				break
			}
		}
	}
	start := focus - height/2
	if start < 0 {
		start = 0
	}
	if maximum := len(lines) - height; start > maximum {
		start = maximum
	}
	return lines[start : start+height]
}

// ── Left panel: Model ID / Display Name / Context ──

func (m *ModelSettings) renderEditLeftPanel(w, h int, th Theme) []string {
	var lines []string

	headerStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(th.Primary)).Bold(true)
	lines = append(lines, headerStyle.Render(i18n.T("settings.residual_final.agents.identity")))
	lines = append(lines, strings.Repeat("─", w-2))

	hintStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(th.TextMuted)).Padding(0, 1)

	fields := []struct {
		index int
		label string
		value *string
		hint  string
	}{
		{0, i18n.T("settings.residual_final.forms.model_id"), &m.modelFormID, i18n.T("settings.residual_final.forms.api_identifier_hint")},
		{1, i18n.T("settings.residual_final.forms.display_name"), &m.modelFormDisplayName, i18n.T("settings.residual_final.forms.friendly_name_hint")},
		{2, i18n.T("settings.residual_final.forms.context_window"), &m.modelFormContext, i18n.T("settings.residual_final.forms.context_examples")},
	}

	for _, f := range fields {
		isSelected := m.modelFormField == f.index
		isEditing := m.formEditing && m.modelFormField == f.index

		lbl := lipgloss.NewStyle().
			Foreground(lipgloss.Color(th.Text)).
			Bold(isSelected).
			Padding(0, 1)
		if isSelected {
			lbl = lbl.Foreground(lipgloss.Color(th.Primary))
		}
		cursor := " "
		if isSelected {
			cursor = "▶"
		}
		lines = append(lines, lbl.Render(cursor+" "+f.label))

		hint := f.hint
		if isSelected && !isEditing {
			hint += i18n.T("settings.residual_final.forms.enter_to_edit")
		} else if isEditing {
			hint = i18n.T("settings.residual_final.forms.editing_finish")
		}
		lines = append(lines, hintStyle.Render(hint))

		val := *f.value
		if val == "" {
			val = "…"
		}
		if isEditing && m.formCursorPos <= len(*f.value) {
			val = (*f.value)[:m.formCursorPos] + "│" + (*f.value)[m.formCursorPos:]
		}
		vs := lipgloss.NewStyle().
			Foreground(lipgloss.Color(th.Text)).
			Background(lipgloss.Color(th.BGLight)).
			Padding(0, 1).
			Margin(0, 2).
			Width(w - 6)
		if isSelected {
			vs = vs.Border(lipgloss.NormalBorder()).BorderForeground(lipgloss.Color(th.Primary))
		}
		if isEditing {
			vs = vs.Background(lipgloss.Color(th.BGLighter)).BorderForeground(lipgloss.Color(th.Success))
		}
		lines = append(lines, vs.Render(val), "")
	}

	// Save button
	isSelected := m.modelFormField == modelFieldSave
	btnStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(th.Text)).
		Background(lipgloss.Color(th.Primary)).
		Bold(true).
		Padding(0, 3).
		Margin(1, 2)
	if isSelected {
		btnStyle = btnStyle.Border(lipgloss.NormalBorder()).BorderForeground(lipgloss.Color(th.Success))
	}
	lines = append(lines, btnStyle.Render(i18n.T("settings.residual_models.form.save_model_spaced")))

	for len(lines) < h {
		lines = append(lines, "")
	}
	return lines
}

// ── Right panel: Thinking / Effort settings ──

func (m *ModelSettings) renderEditRightPanel(w, h int, th Theme) []string {
	var lines []string
	isAdaptive := modelSupportsAdaptiveThinking(m.modelFormID)

	headerStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(th.Primary)).Bold(true)
	lines = append(lines, headerStyle.Render(i18n.T("settings.residual_final.forms.intelligence")))
	lines = append(lines, strings.Repeat("─", w-2))

	metaStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(th.TextMuted))
	labelStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(th.Text))
	hintStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(th.TextMuted)).Padding(0, 1)

	// ── Info line: detected mode ──
	if isAdaptive {
		lines = append(lines, labelStyle.Render(i18n.T("settings.residual_final.forms.mode_label"))+lipgloss.NewStyle().
			Foreground(lipgloss.Color(th.Success)).Bold(true).Render(i18n.T("settings.residual_final.forms.adaptive_thinking")))
		lines = append(lines, metaStyle.Render(i18n.T("settings.residual_final.forms.adaptive_description")))
	} else if modelSupportsExtendedThinking(m.modelFormID) {
		lines = append(lines, labelStyle.Render(i18n.T("settings.residual_final.forms.mode_label"))+lipgloss.NewStyle().
			Foreground(lipgloss.Color(th.Warning)).Bold(true).Render(i18n.T("settings.residual_final.forms.manual_budget")))
		lines = append(lines, metaStyle.Render(i18n.T("settings.residual_final.forms.manual_description")))
	} else {
		lines = append(lines, labelStyle.Render(i18n.T("settings.residual_final.forms.mode_label"))+metaStyle.Render(i18n.T("settings.residual_final.forms.standard_no_thinking")))
	}
	lines = append(lines, "")

	// ── Field 3: Thinking Enabled ──
	{
		isSelected := m.modelFormField == 3
		cursor := " "
		if isSelected {
			cursor = "▶"
		}
		lbl := lipgloss.NewStyle().
			Foreground(lipgloss.Color(th.Text)).Bold(isSelected).Padding(0, 1)
		if isSelected {
			lbl = lbl.Foreground(lipgloss.Color(th.Primary))
		}
		lines = append(lines, lbl.Render(cursor+" "+i18n.T("settings.residual_final.forms.extended_thinking")))
		lines = append(lines, hintStyle.Render(i18n.T("settings.residual_final.forms.toggle_hint")))

		statusText := i18n.T("settings.residual_final.forms.status_unchecked", i18n.T("settings.residual_final.common.disabled"))
		statusColor := th.TextMuted
		if m.modelFormThinkingEnabled {
			statusText = i18n.T("settings.residual_final.forms.status_checked", i18n.T("settings.residual_final.common.enabled"))
			statusColor = th.Success
		}
		vs := lipgloss.NewStyle().
			Foreground(lipgloss.Color(statusColor)).
			Bold(m.modelFormThinkingEnabled).
			Padding(0, 1).Margin(0, 2).Width(w - 6)
		if isSelected {
			vs = vs.Background(lipgloss.Color(th.BGLighter))
		}
		lines = append(lines, vs.Render(statusText), "")
	}

	// ── Field 4: Thinking Budget ──
	{
		isSelected := m.modelFormField == 4
		isEditing := m.formEditing && m.modelFormField == 4
		cursor := " "
		if isSelected {
			cursor = "▶"
		}

		budgetLabel := i18n.T("settings.residual_final.forms.thinking_budget")
		if isAdaptive {
			budgetLabel = i18n.T("settings.residual_final.forms.thinking_budget_ignored")
		}
		dimmed := !m.modelFormThinkingEnabled
		lbl := lipgloss.NewStyle().Foreground(lipgloss.Color(th.Text)).Bold(isSelected).Padding(0, 1)
		if isSelected {
			lbl = lbl.Foreground(lipgloss.Color(th.Primary))
		}
		if dimmed {
			lbl = lbl.Foreground(lipgloss.Color(th.TextMuted))
		}
		lines = append(lines, lbl.Render(cursor+" "+budgetLabel))

		hint := i18n.T("settings.residual_final.forms.budget_range_hint")
		if dimmed {
			hint = i18n.T("settings.residual_final.forms.enable_thinking")
		} else if isSelected && !isEditing {
			hint += i18n.T("settings.residual_final.forms.enter_to_edit")
		} else if isEditing {
			hint = i18n.T("settings.residual_final.forms.editing_finish")
		}
		lines = append(lines, hintStyle.Render(hint))

		val := fmt.Sprintf("%d", m.modelFormThinkingBudget)
		if m.modelFormThinkingBudget == 0 {
			val = "2048"
		}
		vs := lipgloss.NewStyle().
			Foreground(lipgloss.Color(th.Text)).
			Background(lipgloss.Color(th.BGLight)).
			Padding(0, 1).Margin(0, 2).Width(w - 6)
		if dimmed || isAdaptive {
			vs = vs.Foreground(lipgloss.Color(th.TextMuted))
		}
		if isSelected {
			vs = vs.Border(lipgloss.NormalBorder()).BorderForeground(lipgloss.Color(th.Primary))
		}
		if isEditing {
			vs = vs.Background(lipgloss.Color(th.BGLighter)).BorderForeground(lipgloss.Color(th.Success))
		}
		lines = append(lines, vs.Render(val), "")
	}

	// ── Field 5: Thinking Effort ──
	{
		isSelected := m.modelFormField == 5
		cursor := " "
		if isSelected {
			cursor = "▶"
		}
		dimmed := !m.modelFormThinkingEnabled

		lbl := lipgloss.NewStyle().Foreground(lipgloss.Color(th.Text)).Bold(isSelected).Padding(0, 1)
		if isSelected {
			lbl = lbl.Foreground(lipgloss.Color(th.Primary))
		}
		if dimmed {
			lbl = lbl.Foreground(lipgloss.Color(th.TextMuted))
		}
		lines = append(lines, lbl.Render(cursor+" "+i18n.T("settings.residual_final.forms.thinking_effort")))

		availableEfforts := modelThinkingEfforts(m.modelFormID)
		hint := i18n.T("settings.residual_final.forms.effort_cycle", strings.Join(availableEfforts[1:], ", "))
		if dimmed {
			hint = i18n.T("settings.residual_final.forms.enable_thinking")
		}
		lines = append(lines, hintStyle.Render(hint))

		effort := m.modelFormThinkingEffort
		if effort == "" {
			effort = i18n.T("settings.residual_final.forms.default_state", "high")
		}
		vs := lipgloss.NewStyle().
			Foreground(lipgloss.Color(th.Text)).
			Background(lipgloss.Color(th.BGLight)).
			Padding(0, 1).Margin(0, 2).Width(w - 6)
		if dimmed {
			vs = vs.Foreground(lipgloss.Color(th.TextMuted))
		}
		if isSelected {
			vs = vs.Background(lipgloss.Color(th.BGLighter))
		}
		lines = append(lines, vs.Render(effort), "")
	}

	// ── Field 6: Diffusion Model ──
	{
		isSelected := m.modelFormField == 6
		cursor := " "
		if isSelected {
			cursor = "▶"
		}
		lbl := lipgloss.NewStyle().
			Foreground(lipgloss.Color(th.Text)).Bold(isSelected).Padding(0, 1)
		if isSelected {
			lbl = lbl.Foreground(lipgloss.Color(th.Primary))
		}
		lines = append(lines, lbl.Render(cursor+" "+i18n.T("settings.residual_final.forms.diffusion_model")))
		lines = append(lines, hintStyle.Render(i18n.T("settings.residual_final.forms.diffusion_reveal_hint")))

		statusText := i18n.T("settings.residual_final.forms.status_unchecked", i18n.T("settings.residual_final.common.disabled"))
		statusColor := th.TextMuted
		if m.modelFormDiffusion {
			statusText = i18n.T("settings.residual_final.forms.status_checked", i18n.T("settings.residual_final.common.enabled"))
			statusColor = th.Success
		}
		vs := lipgloss.NewStyle().
			Foreground(lipgloss.Color(statusColor)).
			Bold(m.modelFormDiffusion).
			Padding(0, 1).Margin(0, 2).Width(w - 6)
		if isSelected {
			vs = vs.Background(lipgloss.Color(th.BGLighter))
		}
		lines = append(lines, vs.Render(statusText), "")
	}

	lines = append(lines, m.renderGenerationOverrideFields(w, th)...)

	for len(lines) < h {
		lines = append(lines, "")
	}
	return lines
}

// ── Hint bar for edit model ──

func (m *ModelSettings) renderGenerationOverrideFields(w int, th Theme) []string {
	const auto = "Auto / provider default"
	const unsupported = "Unsupported for this provider/model"
	caps := m.generationCapabilities()
	floatValue := func(value *float64) string {
		if value == nil {
			return auto
		}
		return fmt.Sprintf("%.2f", *value)
	}
	intValue := func(value *int) string {
		if value == nil {
			return auto
		}
		return fmt.Sprintf("%d", *value)
	}
	maxTokens := auto
	if m.modelFormMaxTokens != 0 {
		maxTokens = fmt.Sprintf("%d", m.modelFormMaxTokens)
	}
	fields := []struct {
		index     int
		label     string
		value     string
		hint      string
		supported bool
	}{
		{modelFieldTemperature, "Temperature", floatValue(m.modelFormTemperature), fmt.Sprintf("←/→ adjust by 0.10 (max %.1f); left from 0 clears", caps.temperatureMax), caps.temperature},
		{modelFieldMaxTokens, "Max tokens", maxTokens, "←/→ adjust by 1024; 0 inherits", caps.maxTokens},
		{modelFieldTopP, "Top P", floatValue(m.modelFormTopP), "←/→ adjust by 0.05; left from 0 clears", caps.topP},
		{modelFieldTopK, "Top K", intValue(m.modelFormTopK), "←/→ adjust by 1; left from 0 clears", caps.topK},
	}
	var lines []string
	hintStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(th.TextMuted)).Padding(0, 1)
	selectedHint := ""
	for _, field := range fields {
		selected := m.modelFormField == field.index
		cursor := " "
		if selected {
			cursor = "▶"
		}
		label := lipgloss.NewStyle().Foreground(lipgloss.Color(th.Text)).Bold(selected).Padding(0, 1)
		if selected {
			label = label.Foreground(lipgloss.Color(th.Primary))
		}
		if !field.supported {
			field.value = unsupported
			field.hint = "Not sent by the selected provider/model"
			label = label.Foreground(lipgloss.Color(th.TextMuted))
		}
		value := lipgloss.NewStyle().
			Foreground(lipgloss.Color(th.Text)).
			Background(lipgloss.Color(th.BGLight)).
			Padding(0, 1).
			Width(max(16, w-20))
		if selected {
			value = value.Background(lipgloss.Color(th.BGLighter)).
				Border(lipgloss.NormalBorder()).BorderForeground(lipgloss.Color(th.Primary))
			selectedHint = field.hint
		}
		labelCell := label.Width(18).Render(cursor + " " + field.label)
		lines = append(lines, lipgloss.JoinHorizontal(lipgloss.Center, labelCell, value.Render(field.value)))
	}
	if selectedHint != "" {
		lines = append(lines, hintStyle.Render(selectedHint))
	}
	return lines
}

func (m *ModelSettings) renderEditHintBar(width int, th Theme) string {
	keyStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(th.Text)).
		Background(lipgloss.Color(th.BGLighter)).
		Padding(0, 1)
	descStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(th.TextMuted)).
		MarginRight(2)

	hint := func(key, desc string) string {
		return keyStyle.Render(key) + descStyle.Render(" "+desc)
	}

	hints := []string{
		hint("↑↓", i18n.T("settings.residual_models.form.hint.navigate")),
		hint("←→", i18n.T("settings.residual_models.form.hint.toggle")),
		hint("Enter", i18n.T("settings.residual_models.form.hint.edit_save")),
		hint("Esc", i18n.T("settings.residual_models.form.hint.cancel")),
	}

	bar := strings.Join(hints, "  ")
	return lipgloss.NewStyle().Width(width).Align(lipgloss.Center).Render(bar)
}

func (m *ModelSettings) renderConfirmDelete(width, height int, th Theme) string {
	var lines []string

	var isCustomProviderConfirm bool = m.confirmDeleteType == "custom_provider"
	var headerText string = i18n.T("settings.residual_final.forms.confirm_delete")
	var headerColor string = th.Error
	if isCustomProviderConfirm {
		headerText = i18n.T("settings.residual_final.forms.confirm_custom_provider")
		headerColor = th.Warning
	}

	headerStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(headerColor)).
		Bold(true).
		Padding(1, 2)
	lines = append(lines, headerStyle.Render(headerText))

	// Build the message based on what we're deleting
	var itemName string
	var itemType string
	switch m.confirmDeleteType {
	case "model":
		itemType = i18n.T("settings.residual_final.common.model_lower")
		if m.selectedProvider < len(m.providers) && m.confirmDeleteIndex < len(m.providers[m.selectedProvider].Models) {
			model := m.providers[m.selectedProvider].Models[m.confirmDeleteIndex]
			itemName = model.DisplayName
		}
	case "provider":
		itemType = strings.ToLower(i18n.T("settings.residual_final.common.provider"))
		if m.confirmDeleteIndex < len(m.providers) {
			itemName = m.providers[m.confirmDeleteIndex].DisplayName
		}
	case "custom_provider":
		itemType = strings.ToLower(i18n.T("settings.residual_final.common.provider"))
		itemName = m.formDisplayName
	}

	messageStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(th.Text)).
		Padding(0, 2).
		MarginBottom(1)
	if isCustomProviderConfirm {
		lines = append(lines, messageStyle.Render(i18n.T("settings.residual_final.forms.untrusted_url")))
	} else {
		lines = append(lines, messageStyle.Render(i18n.T("settings.residual_final.forms.delete_question", itemType)))
	}

	itemStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(th.Primary)).
		Bold(true).
		Padding(0, 4)
	lines = append(lines, itemStyle.Render(fmt.Sprintf("\"%s\"", itemName)), "")

	warningStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(th.Warning)).
		Italic(true).
		Padding(0, 2)
	if isCustomProviderConfirm {
		var endpoint string = strings.TrimSpace(m.formAPIEndpoint)
		if endpoint != "" {
			lines = append(lines, itemStyle.Render(endpoint))
		}
		lines = append(lines, warningStyle.Render(i18n.T("settings.residual_final.forms.trust_endpoint")), "")
	} else {
		lines = append(lines, warningStyle.Render(i18n.T("settings.residual_final.forms.cannot_undo")), "")
	}

	// Yes/No buttons
	noStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(th.Text)).
		Padding(0, 3).
		MarginRight(2)
	yesStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(th.Error)).
		Padding(0, 3)
	var yesLabel string = i18n.T("settings.residual_final.forms.yes_delete")
	if isCustomProviderConfirm {
		yesStyle = yesStyle.Foreground(lipgloss.Color(th.Warning))
		yesLabel = i18n.T("settings.residual_final.forms.yes_continue")
	}

	if m.confirmSelected == 0 {
		noStyle = noStyle.
			Background(lipgloss.Color(th.Primary)).
			Foreground(lipgloss.Color(th.Text)).
			Bold(true)
	} else {
		// Use Warning color for custom provider confirm (not destructive), Error for actual deletions
		var highlightBG string = th.Error
		if isCustomProviderConfirm {
			highlightBG = th.Warning
		}
		yesStyle = yesStyle.
			Background(lipgloss.Color(highlightBG)).
			Foreground(lipgloss.Color(th.Text)).
			Bold(true)
	}

	buttonRow := lipgloss.JoinHorizontal(
		lipgloss.Center,
		noStyle.Render(i18n.T("settings.residual_final.forms.no")),
		yesStyle.Render(yesLabel),
	)

	buttonContainer := lipgloss.NewStyle().
		Padding(1, 2).
		Render(buttonRow)
	lines = append(lines, buttonContainer)

	hintStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(th.TextMuted)).
		Padding(0, 2)
	lines = append(lines, hintStyle.Render(i18n.T("settings.residual_final.forms.confirm_hint")))

	content := lipgloss.JoinVertical(lipgloss.Left, lines...)
	return lipgloss.NewStyle().Width(width).Height(height).Render(content)
}

// renderConfirmOverride renders the manual override warning
func (m *ModelSettings) renderConfirmOverride(width, height int, th Theme) string {
	var lines []string

	headerStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(th.Warning)).
		Bold(true).
		Padding(1, 2)
	lines = append(lines, headerStyle.Render(i18n.T("settings.residual_final.forms.manual_override")))

	messageStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(th.Text)).
		Padding(0, 2).
		MarginBottom(1)

	lines = append(lines, messageStyle.Render(i18n.T("settings.residual_final.forms.override_warning")))
	lines = append(lines, messageStyle.Render(i18n.T("settings.residual_final.forms.new_model", m.pendingProvider, m.pendingModel)))

	warningStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(th.Warning)).
		Italic(true).
		Padding(0, 2)
	lines = append(lines, warningStyle.Render(i18n.T("settings.residual_final.forms.bypass_profile")), "")

	// Buttons: Cancel (0), Override (1), Save to Profile (2)
	noStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(th.Text)).
		Padding(0, 3).
		MarginRight(2)
	overrideStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(th.Warning)).
		Padding(0, 3).
		MarginRight(2)
	saveStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(th.Primary)).
		Padding(0, 3)

	if m.confirmSelected == 0 {
		noStyle = noStyle.
			Background(lipgloss.Color(th.Primary)).
			Foreground(lipgloss.Color(th.Text)).
			Bold(true)
	} else if m.confirmSelected == 1 {
		overrideStyle = overrideStyle.
			Background(lipgloss.Color(th.Warning)).
			Foreground(lipgloss.Color(th.Text)).
			Bold(true)
	} else {
		saveStyle = saveStyle.
			Background(lipgloss.Color(th.Primary)).
			Foreground(lipgloss.Color(th.Text)).
			Bold(true)
	}

	buttonRow := lipgloss.JoinHorizontal(
		lipgloss.Center,
		noStyle.Render(i18n.T("settings.residual_final.forms.cancel")),
		overrideStyle.Render(i18n.T("settings.residual_final.forms.override")),
		saveStyle.Render(i18n.T("settings.residual_final.forms.save_to_profile")),
	)

	lines = append(lines, lipgloss.NewStyle().Padding(1, 2).Render(buttonRow))

	content := lipgloss.JoinVertical(lipgloss.Left, lines...)
	return lipgloss.NewStyle().Width(width).Height(height).Render(content)
}
