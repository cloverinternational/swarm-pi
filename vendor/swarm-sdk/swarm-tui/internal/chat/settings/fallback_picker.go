package settings

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/fallback"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/chat/commands"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/i18n"
)

// FallbackPicker handles cascading model selection with fallbacks.
// It shows all fallback levels at once and allows editing each level.
type FallbackPicker struct {
	chain         *fallback.Chain
	providers     []commands.ProviderConfig
	configManager *commands.ConfigManager

	// UI State
	state         string // "list", "select_provider", "select_model"
	selectedIndex int    // Index in list view (0=primary, 1+=fallbacks, last=add button)
	editingIndex  int    // Which level is being edited (-1 for none, 0=primary, 1+=fallback)

	// Provider/model selection state (reuses pattern from compaction.go)
	selectedProviderIdx  int
	selectedModelIdx     int
	providerScrollOffset int
	modelScrollOffset    int
	maxVisible           int

	// Callbacks
	onSave func(*fallback.Chain)
}

// NewFallbackPicker creates a cascading fallback picker.
func NewFallbackPicker(chain *fallback.Chain) *FallbackPicker {
	cm, _ := commands.NewConfigManager()
	providers, _ := cm.LoadProviders()

	// Clone chain to allow modifications without affecting original until save
	chainCopy := chain.Clone()
	if chainCopy == nil {
		chainCopy = fallback.NewChainWithDefaults()
	}

	return &FallbackPicker{
		chain:         chainCopy,
		providers:     providers,
		configManager: cm,
		state:         "list",
		editingIndex:  -1,
		maxVisible:    8,
	}
}

// SetOnSave sets the callback for when the chain is saved.
func (f *FallbackPicker) SetOnSave(callback func(*fallback.Chain)) {
	f.onSave = callback
}

// GetChain returns the current chain.
func (f *FallbackPicker) GetChain() *fallback.Chain {
	return f.chain
}

// ReloadProviders refreshes the provider list.
func (f *FallbackPicker) ReloadProviders() {
	if f.configManager == nil {
		return
	}
	providers, err := f.configManager.LoadProviders()
	if err == nil {
		f.providers = providers
	}
}

// HandleKey processes keyboard input.
func (f *FallbackPicker) HandleKey(key string) bool {
	switch f.state {
	case "list":
		return f.handleListKey(key)
	case "select_provider":
		return f.handleProviderKey(key)
	case "select_model":
		return f.handleModelKey(key)
	}
	return false
}

func (f *FallbackPicker) handleListKey(key string) bool {
	maxIdx := f.chain.Len() // 0 to Len()-1 for models, Len() for add button

	switch key {
	case "up", "k":
		if f.selectedIndex > 0 {
			f.selectedIndex--
			return true
		}
		// Allow navigation out of this section
		return false
	case "down", "j":
		if f.selectedIndex < maxIdx {
			f.selectedIndex++
			return true
		}
		// Allow navigation out of this section
		return false
	case "enter", " ", "space":
		if f.selectedIndex < f.chain.Len() {
			// Edit existing model
			f.startEditing(f.selectedIndex)
		} else {
			// Add new fallback
			f.startAddingFallback()
		}
		return true
	case "d", "delete", "backspace":
		// Delete fallback (not primary)
		if f.selectedIndex > 0 && f.selectedIndex < f.chain.Len() {
			fallbackIdx := f.selectedIndex - 1
			_ = f.chain.RemoveFallback(fallbackIdx)
			// Adjust selection if needed
			if f.selectedIndex >= f.chain.Len() {
				f.selectedIndex = f.chain.Len()
			}
			f.save()
		}
		return true
	case "ctrl+up", "K":
		// Move fallback up
		if f.selectedIndex > 1 && f.selectedIndex < f.chain.Len() {
			fallbackIdx := f.selectedIndex - 1
			if err := f.chain.MoveFallback(fallbackIdx, -1); err == nil {
				f.selectedIndex--
				f.save()
			}
		}
		return true
	case "ctrl+down", "J":
		// Move fallback down
		if f.selectedIndex > 0 && f.selectedIndex < f.chain.Len()-1 {
			fallbackIdx := f.selectedIndex - 1
			if err := f.chain.MoveFallback(fallbackIdx, 1); err == nil {
				f.selectedIndex++
				f.save()
			}
		}
		return true
	}
	return false
}

func (f *FallbackPicker) handleProviderKey(key string) bool {
	availableProviders := f.getAvailableProviders()
	if len(availableProviders) == 0 {
		return false
	}

	switch key {
	case "up", "k":
		if f.selectedProviderIdx > 0 {
			f.selectedProviderIdx--
			f.adjustProviderScroll(len(availableProviders))
		}
		return true
	case "down", "j":
		if f.selectedProviderIdx < len(availableProviders)-1 {
			f.selectedProviderIdx++
			f.adjustProviderScroll(len(availableProviders))
		}
		return true
	case "enter", " ", "space":
		// Select provider and move to model selection
		provider := availableProviders[f.selectedProviderIdx]
		f.state = "select_model"
		f.selectedModelIdx = 0
		f.modelScrollOffset = 0
		// Default to the first known chat-capable model rather than raw
		// index 0: provider catalogs are frequently unfiltered listings
		// (e.g. OpenAI's /v1/models) that include legacy completion, image,
		// audio, embedding, and moderation models. Landing on one of those
		// as the "default" selection lets a user accidentally save it into
		// a fallback chain, where it will always fail at request time and
		// can abort an entire run if it's the last entry tried
		// (Swarm-Code/mono#66).
		for i, m := range provider.Models {
			if !fallback.IsKnownNonChatModel(m.ID) {
				f.selectedModelIdx = i
				break
			}
		}
		// Prefer a recommended lightweight model (haiku, flash, mini, lite)
		// when one exists among the chat-capable models.
		for i, m := range provider.Models {
			lowerID := strings.ToLower(m.ID)
			if fallback.IsKnownNonChatModel(m.ID) {
				continue
			}
			if strings.Contains(lowerID, "haiku") || strings.Contains(lowerID, "flash") ||
				strings.Contains(lowerID, "mini") || strings.Contains(lowerID, "lite") {
				f.selectedModelIdx = i
				break
			}
		}
		return true
	case "esc":
		f.cancelEditing()
		return true
	}
	return false
}

func (f *FallbackPicker) handleModelKey(key string) bool {
	provider := f.getCurrentEditProvider()
	if provider == nil || len(provider.Models) == 0 {
		return false
	}

	switch key {
	case "up", "k":
		if f.selectedModelIdx > 0 {
			f.selectedModelIdx--
			f.adjustModelScroll(len(provider.Models))
		}
		return true
	case "down", "j":
		if f.selectedModelIdx < len(provider.Models)-1 {
			f.selectedModelIdx++
			f.adjustModelScroll(len(provider.Models))
		}
		return true
	case "enter", " ", "space":
		// Select model and save
		model := provider.Models[f.selectedModelIdx]
		f.finishEditing(provider.Name, model.ID)
		return true
	case "esc":
		// Go back to provider selection
		f.state = "select_provider"
		return true
	}
	return false
}

func (f *FallbackPicker) startEditing(index int) {
	f.editingIndex = index
	f.state = "select_provider"
	f.selectedProviderIdx = 0
	f.providerScrollOffset = 0

	// Pre-select current provider if editing existing
	var currentProvider string
	if index == 0 {
		currentProvider = f.chain.Primary.Provider
	} else if index-1 < len(f.chain.Fallbacks) {
		currentProvider = f.chain.Fallbacks[index-1].Provider
	}

	if currentProvider != "" {
		availableProviders := f.getAvailableProviders()
		for i, p := range availableProviders {
			if p.Name == currentProvider {
				f.selectedProviderIdx = i
				break
			}
		}
	}
}

func (f *FallbackPicker) startAddingFallback() {
	f.editingIndex = f.chain.Len() // New fallback will be at end
	f.state = "select_provider"
	f.selectedProviderIdx = 0
	f.providerScrollOffset = 0
}

func (f *FallbackPicker) finishEditing(provider, model string) {
	if f.editingIndex == 0 {
		// Update primary
		f.chain.SetPrimary(provider, model)
	} else if f.editingIndex-1 < len(f.chain.Fallbacks) {
		// Update existing fallback
		_ = f.chain.UpdateFallback(f.editingIndex-1, provider, model)
	} else {
		// Add new fallback
		f.chain.AddFallback(provider, model)
	}

	f.state = "list"
	f.editingIndex = -1
	f.save()
}

// IsInNestedState returns true if the picker is in a sub-state (selecting provider or model).
func (f *FallbackPicker) IsInNestedState() bool {
	return f.state != "list"
}

func (f *FallbackPicker) cancelEditing() {
	f.state = "list"
	f.editingIndex = -1
}

func (f *FallbackPicker) save() {
	if f.onSave != nil {
		f.onSave(f.chain)
	}
}

func (f *FallbackPicker) getAvailableProviders() []commands.ProviderConfig {
	var available []commands.ProviderConfig
	for _, p := range f.providers {
		if p.Available {
			available = append(available, p)
		}
	}
	// Also include unavailable providers but mark them
	for _, p := range f.providers {
		if !p.Available {
			available = append(available, p)
		}
	}
	return available
}

func (f *FallbackPicker) getCurrentEditProvider() *commands.ProviderConfig {
	availableProviders := f.getAvailableProviders()
	if f.selectedProviderIdx >= 0 && f.selectedProviderIdx < len(availableProviders) {
		return &availableProviders[f.selectedProviderIdx]
	}
	return nil
}

func (f *FallbackPicker) adjustProviderScroll(total int) {
	if f.selectedProviderIdx < f.providerScrollOffset {
		f.providerScrollOffset = f.selectedProviderIdx
	} else if f.selectedProviderIdx >= f.providerScrollOffset+f.maxVisible {
		f.providerScrollOffset = f.selectedProviderIdx - f.maxVisible + 1
	}
	if f.providerScrollOffset < 0 {
		f.providerScrollOffset = 0
	}
	maxOffset := total - f.maxVisible
	if maxOffset < 0 {
		maxOffset = 0
	}
	if f.providerScrollOffset > maxOffset {
		f.providerScrollOffset = maxOffset
	}
}

func (f *FallbackPicker) adjustModelScroll(total int) {
	if f.selectedModelIdx < f.modelScrollOffset {
		f.modelScrollOffset = f.selectedModelIdx
	} else if f.selectedModelIdx >= f.modelScrollOffset+f.maxVisible {
		f.modelScrollOffset = f.selectedModelIdx - f.maxVisible + 1
	}
	if f.modelScrollOffset < 0 {
		f.modelScrollOffset = 0
	}
	maxOffset := total - f.maxVisible
	if maxOffset < 0 {
		maxOffset = 0
	}
	if f.modelScrollOffset > maxOffset {
		f.modelScrollOffset = maxOffset
	}
}

// Render draws the fallback picker UI.
func (f *FallbackPicker) Render(width, height int, theme Theme) string {
	const (
		borderWidth      = 2
		containerPadding = 1
	)

	if width <= 0 || height <= 0 {
		return ""
	}

	innerWidth := width - (borderWidth * 2) - (containerPadding * 2)
	innerHeight := height - (borderWidth * 2) - (containerPadding * 2)
	if innerWidth < 0 {
		innerWidth = 0
	}
	if innerHeight < 0 {
		innerHeight = 0
	}

	var content string
	if innerWidth < 20 || innerHeight < 8 {
		content = f.renderMinimal(innerWidth, innerHeight, theme)
	} else {
		switch f.state {
		case "list":
			content = f.renderList(innerWidth, innerHeight, theme)
		case "select_provider":
			content = f.renderProviderSelection(innerWidth, innerHeight, theme)
		case "select_model":
			content = f.renderModelSelection(innerWidth, innerHeight, theme)
		}
	}

	// Apply container border
	containerWidth := width - 2
	if containerWidth < 0 {
		containerWidth = 0
	}
	containerStyle := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color(theme.Border)).
		Width(containerWidth).
		Padding(0, containerPadding)

	return i18n.SettingsResidualModelsText(containerStyle.Render(content))
}

func (f *FallbackPicker) renderList(width, height int, th Theme) string {
	const (
		titleHeight = 4
		hintHeight  = 2
	)

	// Build provider validator
	providerMap := make(map[string]*commands.ProviderConfig)
	for i := range f.providers {
		providerMap[f.providers[i].Name] = &f.providers[i]
	}
	getProvider := func(name string) *fallback.ProviderInfo {
		p, ok := providerMap[name]
		if !ok {
			return nil
		}
		models := make([]fallback.ModelInfo, len(p.Models))
		for i, m := range p.Models {
			models[i] = fallback.ModelInfo{
				ID:          m.ID,
				DisplayName: m.DisplayName,
				Context:     m.Context,
			}
		}
		return &fallback.ProviderInfo{
			Name:        p.Name,
			DisplayName: p.DisplayName,
			Available:   p.Available,
			Models:      models,
		}
	}

	validations := f.chain.Validate(getProvider)

	// Render title with badges
	title := f.renderTitle(width, validations, th)

	// Render list + detail panels
	contentHeight := height - titleHeight - hintHeight
	if contentHeight < 3 {
		return f.renderMinimal(width, height, th)
	}
	content := f.renderListAndDetail(width, contentHeight, validations, th)

	// Render hint bar
	hints := f.renderHintBar(width, th)

	return lipgloss.JoinVertical(lipgloss.Left, title, content, hints)
}

func (f *FallbackPicker) renderMinimal(width, height int, th Theme) string {
	if width < 0 {
		width = 0
	}
	if height < 0 {
		height = 0
	}
	if width == 0 || height == 0 {
		return ""
	}
	msg := i18n.T("settings.residual_final.fallback.resize")
	return lipgloss.NewStyle().
		Width(width).
		Height(height).
		Foreground(lipgloss.Color(th.TextMuted)).
		Align(lipgloss.Center, lipgloss.Center).
		Render(msg)
}

// renderTitle renders the section title with status badges
func (f *FallbackPicker) renderTitle(width int, validations []fallback.ValidationResult, th Theme) string {
	titleStyle := lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color(th.Primary)).
		Width(width).
		Align(lipgloss.Center)

	title := titleStyle.Render(i18n.T("settings.residual_final.fallback.title"))

	// Count status
	totalCount := len(validations)
	availableCount := 0
	for _, v := range validations {
		if v.Available && v.Valid {
			availableCount++
		}
	}

	// Build status badges
	var chainBadge string
	if availableCount == totalCount && totalCount > 0 {
		chainBadge = lipgloss.NewStyle().
			Background(lipgloss.Color(th.Success)).
			Foreground(lipgloss.Color(th.Text)).
			Padding(0, 1).
			Bold(true).
			Render(i18n.T("settings.residual_final.fallback.ready_count", availableCount, totalCount))
	} else if availableCount > 0 {
		chainBadge = lipgloss.NewStyle().
			Background(lipgloss.Color(th.Warning)).
			Foreground(lipgloss.Color(th.Text)).
			Padding(0, 1).
			Bold(true).
			Render(i18n.T("settings.residual_final.fallback.warning_count", availableCount, totalCount))
	} else {
		chainBadge = lipgloss.NewStyle().
			Background(lipgloss.Color(th.Error)).
			Foreground(lipgloss.Color(th.Text)).
			Padding(0, 1).
			Bold(true).
			Render(i18n.T("settings.residual_final.fallback.none_ready"))
	}

	// Primary model badge
	var primaryBadge string
	if len(validations) > 0 && validations[0].Valid {
		primaryBadge = lipgloss.NewStyle().
			Background(lipgloss.Color(th.BGLight)).
			Foreground(lipgloss.Color(th.Text)).
			Padding(0, 1).
			Render(i18n.T("settings.residual_final.fallback.primary_value", validations[0].DisplayName))
	}

	badgesRow := lipgloss.JoinHorizontal(lipgloss.Center, chainBadge, " ", primaryBadge)
	centeredBadges := lipgloss.NewStyle().Width(width).Align(lipgloss.Center).Render(badgesRow)

	return lipgloss.JoinVertical(lipgloss.Left, title, "", centeredBadges)
}

// renderListAndDetail renders the two-panel layout
func (f *FallbackPicker) renderListAndDetail(width, height int, validations []fallback.ValidationResult, th Theme) string {
	if width < 20 || height < 3 {
		return f.renderMinimal(width, height, th)
	}

	// Two-panel layout: list (45%) + details (55%)
	listWidth := (width * 45) / 100
	detailsWidth := width - listWidth - 3 // -3 for separator

	// Render list panel
	listPanel := f.renderChainList(listWidth, height, validations, th)

	// Render detail panel
	detailsPanel := f.renderModelDetails(detailsWidth, height, validations, th)

	// Separator
	separatorLines := make([]string, height-2)
	for i := range separatorLines {
		separatorLines[i] = "│"
	}
	separator := lipgloss.NewStyle().
		Foreground(lipgloss.Color(th.Border)).
		Render(strings.Join(separatorLines, "\n"))

	return lipgloss.JoinHorizontal(lipgloss.Top, listPanel, separator, detailsPanel)
}

// renderChainList renders the left panel with model list
func (f *FallbackPicker) renderChainList(width, height int, validations []fallback.ValidationResult, th Theme) string {
	titleStyle := lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color(th.Primary)).
		Width(width - 2)

	title := titleStyle.Render(i18n.T("settings.residual_final.fallback.chain_count", len(validations)))

	var lines []string
	lines = append(lines, title, "")

	// Render each model in chain
	for i, v := range validations {
		isSelected := f.selectedIndex == i
		lines = append(lines, f.renderChainItem(i, v, width-4, isSelected, th))
	}

	// Add fallback button
	addIdx := len(validations)
	isAddSelected := f.selectedIndex == addIdx
	addIcon := "+"
	addText := i18n.T("settings.residual_final.fallback.add")
	addColor := th.Success
	if isAddSelected {
		addColor = th.Primary
	}
	addLine := fmt.Sprintf(" %s %s", addIcon, addText)
	addStyled := lipgloss.NewStyle().
		Foreground(lipgloss.Color(addColor)).
		Bold(isAddSelected).
		Width(width - 4)
	if isAddSelected {
		addStyled = addStyled.Background(lipgloss.Color(th.BGLighter))
	}
	lines = append(lines, "", addStyled.Render(addLine))

	return lipgloss.JoinVertical(lipgloss.Left, lines...)
}

// renderChainItem renders a single model in the chain list
func (f *FallbackPicker) renderChainItem(index int, v fallback.ValidationResult, width int, isSelected bool, th Theme) string {
	// Status indicator
	var statusIcon string
	var statusColor string
	if !v.Valid {
		statusIcon = "✗"
		statusColor = th.Error
	} else if !v.Available {
		statusIcon = "⚠"
		statusColor = th.Warning
	} else {
		statusIcon = "●"
		statusColor = th.Success
	}

	// Role badge
	var roleBadge string
	if v.IsPrimary {
		roleBadge = i18n.T("settings.residual_final.fallback.primary_short")
	} else {
		roleBadge = i18n.T("settings.residual_final.fallback.fallback_short", index)
	}

	// Model name
	displayName := v.DisplayName
	if displayName == "" {
		displayName = v.Ref.String()
	}

	// Truncate if needed
	availableForName := width - 12
	if availableForName < 8 {
		availableForName = 8
	}
	if len(displayName) > availableForName {
		displayName = displayName[:availableForName-1] + "…"
	}

	// Build line
	statusStyled := lipgloss.NewStyle().
		Foreground(lipgloss.Color(statusColor)).
		Bold(true).
		Render(statusIcon)

	roleColor := th.Warning
	if v.IsPrimary {
		roleColor = th.Primary
	}
	roleStyled := lipgloss.NewStyle().
		Foreground(lipgloss.Color(roleColor)).
		Bold(true).
		Render(roleBadge)

	nameColor := th.Text
	if isSelected {
		nameColor = th.Primary
	}
	nameStyled := lipgloss.NewStyle().
		Foreground(lipgloss.Color(nameColor)).
		Bold(isSelected).
		Render(displayName)

	line := fmt.Sprintf(" %s %s %s", statusStyled, roleStyled, nameStyled)

	if isSelected {
		return lipgloss.NewStyle().
			Background(lipgloss.Color(th.BGLighter)).
			Width(width).
			Render(line)
	}
	return line
}

// renderModelDetails renders the right panel with selected model details
func (f *FallbackPicker) renderModelDetails(width, height int, validations []fallback.ValidationResult, th Theme) string {
	// Check if add button is selected
	if f.selectedIndex >= len(validations) {
		return f.renderAddDetails(width, th)
	}

	if len(validations) == 0 {
		emptyStyle := lipgloss.NewStyle().
			Foreground(lipgloss.Color(th.TextMuted)).
			Italic(true).
			Width(width - 2).
			Align(lipgloss.Center)
		return lipgloss.JoinVertical(lipgloss.Left,
			"",
			emptyStyle.Render(i18n.T("settings.residual_final.fallback.empty")),
			"",
			emptyStyle.Render(i18n.T("settings.residual_final.fallback.empty_hint")),
		)
	}

	v := validations[f.selectedIndex]

	titleStyle := lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color(th.Primary)).
		Width(width - 2)

	labelStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(th.TextMuted)).
		Width(width - 2)

	valueStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(th.Text)).
		Width(width - 2)

	var lines []string

	// Role title
	var roleTitle string
	if v.IsPrimary {
		roleTitle = i18n.T("settings.residual_final.fallback.primary_model")
	} else {
		roleTitle = i18n.T("settings.residual_final.fallback.numbered", f.selectedIndex)
	}
	lines = append(lines, titleStyle.Render(roleTitle))
	lines = append(lines, "")

	// Status badge
	var statusBadge string
	if !v.Valid {
		badgeStyle := lipgloss.NewStyle().
			Background(lipgloss.Color(th.Error)).
			Foreground(lipgloss.Color(th.Text)).
			Padding(0, 1).
			Bold(true)
		statusBadge = badgeStyle.Render(i18n.T("settings.residual_final.fallback.invalid_upper"))
	} else if !v.Available {
		badgeStyle := lipgloss.NewStyle().
			Background(lipgloss.Color(th.Warning)).
			Foreground(lipgloss.Color(th.Text)).
			Padding(0, 1).
			Bold(true)
		statusBadge = badgeStyle.Render(i18n.T("settings.residual_final.fallback.no_credentials_upper"))
	} else {
		badgeStyle := lipgloss.NewStyle().
			Background(lipgloss.Color(th.Success)).
			Foreground(lipgloss.Color(th.Text)).
			Padding(0, 1).
			Bold(true)
		statusBadge = badgeStyle.Render(i18n.T("settings.residual_final.fallback.ready_upper"))
	}

	// Role badge
	var roleBadge string
	if v.IsPrimary {
		badgeStyle := lipgloss.NewStyle().
			Background(lipgloss.Color(th.Primary)).
			Foreground(lipgloss.Color(th.Text)).
			Padding(0, 1).
			Bold(true)
		roleBadge = badgeStyle.Render(i18n.T("settings.residual_final.fallback.primary_upper"))
	} else {
		badgeStyle := lipgloss.NewStyle().
			Background(lipgloss.Color(th.BGLight)).
			Foreground(lipgloss.Color(th.Text)).
			Padding(0, 1)
		roleBadge = badgeStyle.Render(i18n.T("settings.residual_final.fallback.fallback_upper"))
	}

	lines = append(lines, lipgloss.JoinHorizontal(lipgloss.Left, statusBadge, " ", roleBadge))
	lines = append(lines, "")

	// Provider
	lines = append(lines, labelStyle.Render(i18n.T("settings.residual_final.common.provider_label")))
	lines = append(lines, valueStyle.Render("  "+v.Ref.Provider))
	lines = append(lines, "")

	// Model
	lines = append(lines, labelStyle.Render(i18n.T("settings.residual_final.common.model_label")))
	lines = append(lines, valueStyle.Render("  "+v.DisplayName))
	lines = append(lines, "")

	// Model ID
	lines = append(lines, labelStyle.Render(i18n.T("settings.residual_final.model.model_id_label")))
	modelID := v.Ref.Model
	if len(modelID) > width-6 {
		modelID = modelID[:width-9] + "..."
	}
	modelStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(th.TextMuted)).
		Italic(true).
		Width(width - 4)
	lines = append(lines, modelStyle.Render("  "+modelID))
	lines = append(lines, "")

	// Error if any
	if v.Error != nil {
		lines = append(lines, labelStyle.Render(i18n.T("settings.residual_final.common.error_label")))
		errStyle := lipgloss.NewStyle().
			Foreground(lipgloss.Color(th.Error)).
			Width(width - 4)
		lines = append(lines, errStyle.Render("  "+v.Error.Error()))
		lines = append(lines, "")
	}

	// Execution order
	lines = append(lines, labelStyle.Render(i18n.T("settings.residual_final.fallback.execution_order")))
	orderText := i18n.T("settings.residual_final.fallback.order_in_chain", f.selectedIndex+1)
	if v.IsPrimary {
		orderText += i18n.T("settings.residual_final.fallback.tried_first")
	}
	lines = append(lines, valueStyle.Render(orderText))
	lines = append(lines, "")

	// Actions hint
	hintStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(th.TextMuted)).
		Italic(true).
		Width(width - 2)
	if v.IsPrimary {
		lines = append(lines, hintStyle.Render(i18n.T("settings.residual_final.fallback.primary_hint")))
	} else {
		lines = append(lines, hintStyle.Render(i18n.T("settings.residual_final.fallback.item_hint")))
	}

	return lipgloss.JoinVertical(lipgloss.Left, lines...)
}

// renderAddDetails renders details for the add fallback option
func (f *FallbackPicker) renderAddDetails(width int, th Theme) string {
	titleStyle := lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color(th.Primary)).
		Width(width - 2)

	descStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(th.Text)).
		Width(width - 4)

	tipStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(th.TextMuted)).
		Italic(true).
		Width(width - 4)

	var lines []string
	lines = append(lines, titleStyle.Render(i18n.T("settings.residual_final.fallback.add_model")))
	lines = append(lines, "")
	lines = append(lines, descStyle.Render(i18n.T("settings.residual_final.fallback.add_description")))
	lines = append(lines, "")
	lines = append(lines, descStyle.Render(i18n.T("settings.residual_final.fallback.order_description")))
	lines = append(lines, "")
	lines = append(lines, tipStyle.Render(i18n.T("settings.residual_final.fallback.tip")))
	lines = append(lines, "")
	lines = append(lines, tipStyle.Render(i18n.T("settings.residual_final.fallback.add_hint")))

	return lipgloss.JoinVertical(lipgloss.Left, lines...)
}

// renderHintBar renders keyboard navigation hints
func (f *FallbackPicker) renderHintBar(width int, th Theme) string {
	hintStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(th.TextMuted)).
		Width(width).
		Align(lipgloss.Center)

	keyStyle := lipgloss.NewStyle().
		Background(lipgloss.Color(th.BGLight)).
		Foreground(lipgloss.Color(th.Text)).
		Padding(0, 1).
		Bold(true)

	hints := i18n.T("settings.residual_models.fallback.hints.chain",
		keyStyle.Render("↑/↓"),
		keyStyle.Render("Enter"),
		keyStyle.Render("d"),
		keyStyle.Render("K"),
		keyStyle.Render("J"))

	return hintStyle.Render(hints)
}

func (f *FallbackPicker) renderProviderSelection(width, height int, th Theme) string {
	const (
		titleHeight = 4
		hintHeight  = 2
	)

	// Title
	titleStyle := lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color(th.Primary)).
		Width(width).
		Align(lipgloss.Center)

	var headerText string
	if f.editingIndex == 0 {
		headerText = i18n.T("settings.residual_final.fallback.edit_primary")
	} else if f.editingIndex-1 < len(f.chain.Fallbacks) {
		headerText = i18n.T("settings.residual_final.fallback.edit_numbered", f.editingIndex)
	} else {
		headerText = i18n.T("settings.residual_final.fallback.add_model")
	}
	title := titleStyle.Render(headerText)

	// Step indicator
	stepBadge := lipgloss.NewStyle().
		Background(lipgloss.Color(th.Primary)).
		Foreground(lipgloss.Color(th.Text)).
		Padding(0, 1).
		Bold(true).
		Render(i18n.T("settings.residual_final.fallback.step_provider"))

	centeredStep := lipgloss.NewStyle().Width(width).Align(lipgloss.Center).Render(stepBadge)
	titleSection := lipgloss.JoinVertical(lipgloss.Left, title, "", centeredStep)

	availableProviders := f.getAvailableProviders()
	if len(availableProviders) == 0 {
		noProviderStyle := lipgloss.NewStyle().
			Foreground(lipgloss.Color(th.Warning)).
			Width(width).
			Align(lipgloss.Center)
		content := lipgloss.JoinVertical(lipgloss.Left,
			titleSection,
			"",
			noProviderStyle.Render(i18n.T("settings.residual_final.fallback.no_providers")),
			noProviderStyle.Render(i18n.T("settings.residual_final.fallback.configure_providers")),
		)
		return content
	}

	// Provider list
	listHeight := height - titleHeight - hintHeight
	var listLines []string

	// Calculate visible range
	visibleStart := f.providerScrollOffset
	visibleEnd := f.providerScrollOffset + f.maxVisible
	if visibleEnd > len(availableProviders) {
		visibleEnd = len(availableProviders)
	}

	// Scroll indicator (up)
	if visibleStart > 0 {
		scrollStyle := lipgloss.NewStyle().
			Foreground(lipgloss.Color(th.TextMuted)).
			Width(width).
			Align(lipgloss.Center)
		listLines = append(listLines, scrollStyle.Render(i18n.T("settings.residual_final.common.more_above")))
	}

	// Render visible providers
	for i := visibleStart; i < visibleEnd; i++ {
		provider := availableProviders[i]
		isSelected := i == f.selectedProviderIdx

		// Status indicator
		var statusIcon string
		var statusColor string
		if provider.Available {
			statusIcon = "●"
			statusColor = th.Success
		} else {
			statusIcon = "○"
			statusColor = th.Warning
		}

		// Auth type badge
		authBadge := ""
		if provider.Type == "oauth" {
			authBadge = "[OAuth]"
		} else if provider.Type == "api_key" {
			authBadge = "[API]"
		}

		statusStyled := lipgloss.NewStyle().
			Foreground(lipgloss.Color(statusColor)).
			Bold(true).
			Render(statusIcon)

		nameColor := th.Text
		if isSelected {
			nameColor = th.Primary
		}
		nameStyled := lipgloss.NewStyle().
			Foreground(lipgloss.Color(nameColor)).
			Bold(isSelected).
			Render(provider.DisplayName)

		authStyled := lipgloss.NewStyle().
			Foreground(lipgloss.Color(th.TextMuted)).
			Render(authBadge)

		line := fmt.Sprintf("  %s %s %s", statusStyled, nameStyled, authStyled)

		if isSelected {
			line = lipgloss.NewStyle().
				Background(lipgloss.Color(th.BGLighter)).
				Width(width - 4).
				Render(line)
		}
		listLines = append(listLines, line)
	}

	// Scroll indicator (down)
	if visibleEnd < len(availableProviders) {
		scrollStyle := lipgloss.NewStyle().
			Foreground(lipgloss.Color(th.TextMuted)).
			Width(width).
			Align(lipgloss.Center)
		listLines = append(listLines, scrollStyle.Render(i18n.T("settings.residual_final.common.more_below")))
	}

	// Fill remaining height
	for len(listLines) < listHeight {
		listLines = append(listLines, "")
	}

	listSection := lipgloss.JoinVertical(lipgloss.Left, listLines...)

	// Hint bar
	hintStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(th.TextMuted)).
		Width(width).
		Align(lipgloss.Center)
	keyStyle := lipgloss.NewStyle().
		Background(lipgloss.Color(th.BGLight)).
		Foreground(lipgloss.Color(th.Text)).
		Padding(0, 1).
		Bold(true)
	hints := i18n.T("settings.residual_models.fallback.hints.provider",
		keyStyle.Render("↑/↓"),
		keyStyle.Render("Enter"),
		keyStyle.Render("Esc"))
	hintSection := hintStyle.Render(hints)

	return lipgloss.JoinVertical(lipgloss.Left, titleSection, listSection, hintSection)
}

func (f *FallbackPicker) renderModelSelection(width, height int, th Theme) string {
	const (
		titleHeight = 5
		hintHeight  = 2
	)

	provider := f.getCurrentEditProvider()
	if provider == nil {
		errorStyle := lipgloss.NewStyle().
			Foreground(lipgloss.Color(th.Error)).
			Width(width).
			Align(lipgloss.Center)
		return errorStyle.Render(i18n.T("settings.residual_final.fallback.provider_not_found"))
	}

	// Title
	titleStyle := lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color(th.Primary)).
		Width(width).
		Align(lipgloss.Center)

	var headerText string
	if f.editingIndex == 0 {
		headerText = i18n.T("settings.residual_final.fallback.edit_primary")
	} else if f.editingIndex-1 < len(f.chain.Fallbacks) {
		headerText = i18n.T("settings.residual_final.fallback.edit_numbered", f.editingIndex)
	} else {
		headerText = i18n.T("settings.residual_final.fallback.add_model")
	}
	title := titleStyle.Render(headerText)

	// Step indicator + provider badge
	stepBadge := lipgloss.NewStyle().
		Background(lipgloss.Color(th.Primary)).
		Foreground(lipgloss.Color(th.Text)).
		Padding(0, 1).
		Bold(true).
		Render(i18n.T("settings.residual_final.fallback.step_model"))

	providerBadge := lipgloss.NewStyle().
		Background(lipgloss.Color(th.BGLight)).
		Foreground(lipgloss.Color(th.Text)).
		Padding(0, 1).
		Render(provider.DisplayName)

	badgesRow := lipgloss.JoinHorizontal(lipgloss.Center, stepBadge, " ", providerBadge)
	centeredBadges := lipgloss.NewStyle().Width(width).Align(lipgloss.Center).Render(badgesRow)

	// Tip
	tipStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(th.TextMuted)).
		Italic(true).
		Width(width).
		Align(lipgloss.Center)
	tip := tipStyle.Render(i18n.T("settings.residual_final.fallback.recommended"))

	titleSection := lipgloss.JoinVertical(lipgloss.Left, title, "", centeredBadges, tip)

	if len(provider.Models) == 0 {
		noModelStyle := lipgloss.NewStyle().
			Foreground(lipgloss.Color(th.Warning)).
			Width(width).
			Align(lipgloss.Center)
		content := lipgloss.JoinVertical(lipgloss.Left,
			titleSection,
			"",
			noModelStyle.Render(i18n.T("settings.residual_final.fallback.no_models")),
		)
		return content
	}

	// Model list
	listHeight := height - titleHeight - hintHeight
	var listLines []string

	// Calculate visible range
	visibleStart := f.modelScrollOffset
	visibleEnd := f.modelScrollOffset + f.maxVisible
	if visibleEnd > len(provider.Models) {
		visibleEnd = len(provider.Models)
	}

	// Scroll indicator (up)
	if visibleStart > 0 {
		scrollStyle := lipgloss.NewStyle().
			Foreground(lipgloss.Color(th.TextMuted)).
			Width(width).
			Align(lipgloss.Center)
		listLines = append(listLines, scrollStyle.Render("↑ More above"))
	}

	// Render visible models
	for i := visibleStart; i < visibleEnd; i++ {
		model := provider.Models[i]
		isSelected := i == f.selectedModelIdx

		// Recommended marker
		lowerID := strings.ToLower(model.ID)
		isRecommended := strings.Contains(lowerID, "haiku") || strings.Contains(lowerID, "flash") ||
			strings.Contains(lowerID, "mini") || strings.Contains(lowerID, "lite")

		var statusIcon string
		var statusColor string
		if isRecommended {
			statusIcon = "⭐"
			statusColor = th.Warning
		} else {
			statusIcon = "○"
			statusColor = th.TextMuted
		}

		statusStyled := lipgloss.NewStyle().
			Foreground(lipgloss.Color(statusColor)).
			Render(statusIcon)

		nameColor := th.Text
		if isSelected {
			nameColor = th.Primary
		}
		nameStyled := lipgloss.NewStyle().
			Foreground(lipgloss.Color(nameColor)).
			Bold(isSelected).
			Render(model.DisplayName)

		// Context info
		contextStyled := ""
		if model.Context != "" {
			contextStyled = lipgloss.NewStyle().
				Foreground(lipgloss.Color(th.TextMuted)).
				Render(fmt.Sprintf(" [%s]", model.Context))
		}

		line := fmt.Sprintf("  %s %s%s", statusStyled, nameStyled, contextStyled)

		if isSelected {
			line = lipgloss.NewStyle().
				Background(lipgloss.Color(th.BGLighter)).
				Width(width - 4).
				Render(line)
		}
		listLines = append(listLines, line)
	}

	// Scroll indicator (down)
	if visibleEnd < len(provider.Models) {
		scrollStyle := lipgloss.NewStyle().
			Foreground(lipgloss.Color(th.TextMuted)).
			Width(width).
			Align(lipgloss.Center)
		listLines = append(listLines, scrollStyle.Render("↓ More below"))
	}

	// Fill remaining height
	for len(listLines) < listHeight {
		listLines = append(listLines, "")
	}

	listSection := lipgloss.JoinVertical(lipgloss.Left, listLines...)

	// Hint bar
	hintStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(th.TextMuted)).
		Width(width).
		Align(lipgloss.Center)
	keyStyle := lipgloss.NewStyle().
		Background(lipgloss.Color(th.BGLight)).
		Foreground(lipgloss.Color(th.Text)).
		Padding(0, 1).
		Bold(true)
	hints := i18n.T("settings.residual_models.fallback.hints.model",
		keyStyle.Render("↑/↓"),
		keyStyle.Render("Enter"),
		keyStyle.Render("Esc"))
	hintSection := hintStyle.Render(hints)

	return lipgloss.JoinVertical(lipgloss.Left, titleSection, listSection, hintSection)
}
