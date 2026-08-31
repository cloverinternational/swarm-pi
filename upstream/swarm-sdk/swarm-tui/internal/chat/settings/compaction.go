package settings

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/provider"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/chat/commands"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/i18n"
)

// CompactionSettings handles automatic compaction thresholds and behavior.
// Compaction itself always uses the active chat profile's provider and model.
type CompactionSettings struct {
	configManager *commands.ConfigManager
	hasChanges    bool

	// Auto-compaction settings
	enableAutoCompaction            bool
	autoCompactionContinueIfRunning bool
	autoCompactionThresholdPercent  float64
	autoCompactionThreshold         commands.CompactionThreshold
	compactionThresholdOverrides    map[string]commands.CompactionThreshold

	// Current chat model/provider for the per-model override row. Set by the
	// App/Manager before rendering so the screen can show the effective
	// threshold for the active model.
	currentChatProvider string
	currentChatModel    string
}

// NewCompactionSettings creates a new compaction settings handler.
func NewCompactionSettings(cm *commands.ConfigManager) *CompactionSettings {
	var config *commands.SwarmOSConfig
	if cm != nil {
		config, _ = cm.LoadConfig()
		if config == nil {
			// Continue with default config
			config = &commands.SwarmOSConfig{}
		}
	} else {
		config = &commands.SwarmOSConfig{}
	}

	// Load auto-compaction settings from config
	enableAutoCompaction := config.GetEnableAutoCompaction()
	continueIfRunning := config.AutoCompactionContinueIfRunning
	thresholdPercent := config.AutoCompactionThresholdPercent
	if thresholdPercent <= 0 {
		thresholdPercent = 0.85 // Default to 85%
	}
	// Resolve the global threshold (migrates from legacy percent field).
	threshold := config.GetAutoCompactionThreshold()
	if threshold.Mode == "" {
		threshold = commands.CompactionThreshold{
			Mode:  commands.CompactionThresholdPercent,
			Value: thresholdPercent,
		}
	}
	// Copy per-model overrides (defensive copy).
	var overrides map[string]commands.CompactionThreshold
	if len(config.CompactionThresholdOverrides) > 0 {
		overrides = make(map[string]commands.CompactionThreshold, len(config.CompactionThresholdOverrides))
		for k, v := range config.CompactionThresholdOverrides {
			overrides[k] = v
		}
	}

	cs := &CompactionSettings{
		configManager:                   cm,
		enableAutoCompaction:            enableAutoCompaction,
		autoCompactionContinueIfRunning: continueIfRunning,
		autoCompactionThresholdPercent:  thresholdPercent,
		autoCompactionThreshold:         threshold,
		compactionThresholdOverrides:    overrides,
	}

	return cs
}

// Init initializes compaction settings from config.
func (c *CompactionSettings) Init() error {
	return nil
}

// Auto-compaction getters
func (c *CompactionSettings) GetEnableAutoCompaction() bool {
	return c.enableAutoCompaction
}

func (c *CompactionSettings) GetAutoCompactionContinueIfRunning() bool {
	return c.autoCompactionContinueIfRunning
}

func (c *CompactionSettings) GetAutoCompactionThresholdPercent() float64 {
	if c == nil {
		return 0.85
	}
	// Back-compat shim: derive the percent from the resolved threshold.
	if c.autoCompactionThreshold.Mode != "" {
		return c.autoCompactionThreshold.AsPercent(0)
	}
	return c.autoCompactionThresholdPercent
}

// GetAutoCompactionThreshold returns the global compaction threshold descriptor.
func (c *CompactionSettings) GetAutoCompactionThreshold() commands.CompactionThreshold {
	if c == nil || c.autoCompactionThreshold.Mode == "" {
		return commands.DefaultCompactionThreshold()
	}
	return c.autoCompactionThreshold
}

// SetAutoCompactionThreshold sets the global compaction threshold and persists it.
func (c *CompactionSettings) SetAutoCompactionThresholdDescriptor(th commands.CompactionThreshold) {
	if c == nil {
		return
	}
	if th.Mode == "" {
		th = commands.DefaultCompactionThreshold()
	}
	// Validate bounds.
	switch th.Mode {
	case commands.CompactionThresholdFixedTokens:
		if th.Value < 1000 {
			th.Value = 1000
		}
	default: // percent
		if th.Value <= 0 {
			th.Value = 0.85
		}
		if th.Value < 0.5 {
			th.Value = 0.5
		}
		if th.Value > 0.95 {
			th.Value = 0.95
		}
	}
	c.autoCompactionThreshold = th
	// Keep legacy field in sync for back-compat.
	if th.Mode == commands.CompactionThresholdPercent {
		c.autoCompactionThresholdPercent = th.Value
	}
	c.saveAutoCompactionSettings()
}

// GetThresholdOverride returns the per-model override for the given
// provider/model pair, if one is set. The ok flag indicates whether an
// override exists.
func (c *CompactionSettings) GetThresholdOverride(provider, model string) (commands.CompactionThreshold, bool) {
	if c == nil || c.configManager == nil {
		return commands.CompactionThreshold{}, false
	}
	cfg, _ := c.configManager.LoadConfig()
	if cfg == nil {
		return commands.CompactionThreshold{}, false
	}
	return c.lookupOverride(cfg, provider, model)
}

func (c *CompactionSettings) lookupOverride(cfg *commands.SwarmOSConfig, provider, model string) (commands.CompactionThreshold, bool) {
	if cfg == nil {
		return commands.CompactionThreshold{}, false
	}
	if key := commands.CompactionOverrideKey(provider, model); key != "" && len(c.compactionThresholdOverrides) > 0 {
		if ov, ok := c.compactionThresholdOverrides[key]; ok && ov.Mode != "" {
			return ov, true
		}
	}
	return commands.CompactionThreshold{}, false
}

// SetThresholdOverride sets a per-model override and persists it.
func (c *CompactionSettings) SetThresholdOverride(provider, model string, th commands.CompactionThreshold) {
	if c == nil || c.configManager == nil {
		return
	}
	if th.Mode == "" {
		c.ClearThresholdOverride(provider, model)
		return
	}
	// Validate bounds.
	switch th.Mode {
	case commands.CompactionThresholdFixedTokens:
		if th.Value < 1000 {
			th.Value = 1000
		}
	default: // percent
		if th.Value <= 0 {
			th.Value = 0.85
		}
		if th.Value < 0.5 {
			th.Value = 0.5
		}
		if th.Value > 0.95 {
			th.Value = 0.95
		}
	}
	if c.compactionThresholdOverrides == nil {
		c.compactionThresholdOverrides = make(map[string]commands.CompactionThreshold)
	}
	c.compactionThresholdOverrides[commands.CompactionOverrideKey(provider, model)] = th
	c.saveAutoCompactionSettings()
}

// ClearThresholdOverride removes a per-model override and persists the change.
func (c *CompactionSettings) ClearThresholdOverride(provider, model string) {
	if c == nil || c.configManager == nil {
		return
	}
	if c.compactionThresholdOverrides != nil {
		delete(c.compactionThresholdOverrides, commands.CompactionOverrideKey(provider, model))
	}
	c.saveAutoCompactionSettings()
}

// GetCompactionThresholdOverrides returns a copy of all per-model overrides.
func (c *CompactionSettings) GetCompactionThresholdOverrides() map[string]commands.CompactionThreshold {
	if c == nil || len(c.compactionThresholdOverrides) == 0 {
		return nil
	}
	out := make(map[string]commands.CompactionThreshold, len(c.compactionThresholdOverrides))
	for k, v := range c.compactionThresholdOverrides {
		out[k] = v
	}
	return out
}

// ResolveThresholdForModel returns the effective threshold for the given
// provider/model: the per-model override if set, else the global threshold.
func (c *CompactionSettings) ResolveThresholdForModel(provider, model string) commands.CompactionThreshold {
	if c == nil {
		return commands.DefaultCompactionThreshold()
	}
	if cfg, _ := c.configManager.LoadConfig(); cfg != nil {
		if key := commands.CompactionOverrideKey(provider, model); key != "" && len(c.compactionThresholdOverrides) > 0 {
			if ov, ok := c.compactionThresholdOverrides[key]; ok && ov.Mode != "" {
				return ov
			}
		}
	}
	return c.GetAutoCompactionThreshold()
}

// ResolveThresholdTokensForModel returns the effective auto-compaction threshold
// for the given provider/model as an ABSOLUTE input-token count, resolving the
// descriptor (percent OR fixed-tokens mode + per-model override) against the model
// context window. This is the value the SDK agent needs so that a user-configured
// fixed token count actually triggers in-loop auto-compaction. Returns 0 when no
// settings are available (SDK falls back to its default margin threshold).
func (c *CompactionSettings) ResolveThresholdTokensForModel(provider, model string, contextLimit int) int {
	if c == nil {
		return 0
	}
	return c.ResolveThresholdForModel(provider, model).ThresholdTokens(contextLimit)
}

// ReloadFromConfig reloads settings from the configuration file
func (c *CompactionSettings) ReloadFromConfig() {
	if c.configManager == nil {
		return
	}

	if config, err := c.configManager.LoadConfig(); err == nil && config != nil {
		// Reload auto-compaction settings
		c.enableAutoCompaction = config.GetEnableAutoCompaction()
		c.autoCompactionContinueIfRunning = config.AutoCompactionContinueIfRunning
		c.autoCompactionThresholdPercent = config.AutoCompactionThresholdPercent
		if c.autoCompactionThresholdPercent <= 0 {
			c.autoCompactionThresholdPercent = 0.85 // Default to 85%
		}
		// Resolve the global threshold (migrates from legacy percent field).
		th := config.GetAutoCompactionThreshold()
		if th.Mode == "" {
			th = commands.CompactionThreshold{
				Mode:  commands.CompactionThresholdPercent,
				Value: c.autoCompactionThresholdPercent,
			}
		}
		c.autoCompactionThreshold = th
		// Reload per-model overrides (defensive copy).
		c.compactionThresholdOverrides = nil
		if len(config.CompactionThresholdOverrides) > 0 {
			c.compactionThresholdOverrides = make(map[string]commands.CompactionThreshold, len(config.CompactionThresholdOverrides))
			for k, v := range config.CompactionThresholdOverrides {
				c.compactionThresholdOverrides[k] = v
			}
		}

		c.hasChanges = false
	}
}

// Auto-compaction setters
func (c *CompactionSettings) ToggleAutoCompaction() {
	c.enableAutoCompaction = !c.enableAutoCompaction
	c.saveAutoCompactionSettings()
}

func (c *CompactionSettings) ToggleAutoCompactionContinue() {
	c.autoCompactionContinueIfRunning = !c.autoCompactionContinueIfRunning
	c.saveAutoCompactionSettings()
}

func (c *CompactionSettings) SetAutoCompactionThreshold(percent float64) {
	if percent < 0.5 {
		percent = 0.5
	}
	if percent > 0.95 {
		percent = 0.95
	}
	c.autoCompactionThresholdPercent = percent
	// Keep the descriptor in sync (percent mode).
	c.autoCompactionThreshold = commands.CompactionThreshold{
		Mode:  commands.CompactionThresholdPercent,
		Value: percent,
	}
	c.saveAutoCompactionSettings()
}

func (c *CompactionSettings) AdjustAutoCompactionThreshold(delta float64) {
	cur := c.autoCompactionThresholdPercent
	if c.autoCompactionThreshold.Mode == commands.CompactionThresholdPercent && c.autoCompactionThreshold.Value > 0 {
		cur = c.autoCompactionThreshold.Value
	}
	c.SetAutoCompactionThreshold(cur + delta)
}

// ToggleThresholdMode switches the global threshold mode between percent and
// fixed_tokens, preserving a sensible value across the switch.
func (c *CompactionSettings) ToggleThresholdMode(contextLimit int) {
	if c == nil {
		return
	}
	if contextLimit <= 0 {
		contextLimit = provider.DefaultUnknownContextWindow
	}
	th := c.GetAutoCompactionThreshold()
	switch th.Mode {
	case commands.CompactionThresholdFixedTokens:
		// fixed -> percent: derive percent from the fixed value.
		pct := th.Value / float64(contextLimit)
		if pct <= 0 {
			pct = 0.85
		}
		c.SetAutoCompactionThresholdDescriptor(commands.CompactionThreshold{
			Mode:  commands.CompactionThresholdPercent,
			Value: pct,
		})
	default: // percent -> fixed_tokens
		tokens := th.ThresholdTokens(contextLimit)
		c.SetAutoCompactionThresholdDescriptor(commands.CompactionThreshold{
			Mode:  commands.CompactionThresholdFixedTokens,
			Value: float64(tokens),
		})
	}
}

// AdjustThresholdValue adjusts the global threshold value by delta, with the
// step size depending on the current mode (5% for percent, 4000 tokens for
// fixed_tokens).
func (c *CompactionSettings) AdjustThresholdValue(delta float64) {
	if c == nil {
		return
	}
	th := c.GetAutoCompactionThreshold()
	switch th.Mode {
	case commands.CompactionThresholdFixedTokens:
		th.Value += delta * 4000
		c.SetAutoCompactionThresholdDescriptor(th)
	default: // percent
		th.Value += delta * 0.05
		c.SetAutoCompactionThresholdDescriptor(th)
	}
}

// HasThresholdOverrideForModel reports whether a per-model override is set.
func (c *CompactionSettings) HasThresholdOverrideForModel(provider, model string) bool {
	_, ok := c.GetThresholdOverride(provider, model)
	return ok
}

// SetCurrentChatModel records the active chat provider/model so the per-model
// override row can display and toggle the override for the current model.
func (c *CompactionSettings) SetCurrentChatModel(provider, model string) {
	if c == nil {
		return
	}
	c.currentChatProvider = provider
	c.currentChatModel = model
}

// ToggleModelOverride sets or clears a per-model override for the current chat
// model. When setting, it copies the current global threshold so the user can
// then adjust it independently.
func (c *CompactionSettings) ToggleModelOverride(contextLimit int) {
	if c == nil || c.currentChatProvider == "" || c.currentChatModel == "" {
		return
	}
	if c.HasThresholdOverrideForModel(c.currentChatProvider, c.currentChatModel) {
		c.ClearThresholdOverride(c.currentChatProvider, c.currentChatModel)
		return
	}
	// Seed the override from the current global threshold.
	th := c.GetAutoCompactionThreshold()
	c.SetThresholdOverride(c.currentChatProvider, c.currentChatModel, th)
}

// HandleKey handles keyboard input for auto-compaction settings.
func (c *CompactionSettings) HandleKey(key string, state *State) bool {
	if state.Focus != FocusContent {
		return false
	}

	// 0: Auto-Compaction toggle
	// 1: Auto-Continue toggle
	// 2: Threshold Mode toggle (percent / fixed_tokens)
	// 3: Threshold Value adjuster
	// 4: Per-Model override toggle (current model)
	totalItems := 5

	switch key {
	case "up", "k":
		if state.SelectedItem > 0 {
			state.SelectedItem--
			return true
		}
	case "down", "j":
		if state.SelectedItem < totalItems-1 {
			state.SelectedItem++
			return true
		}
	case " ", "space": // Space - toggle
		switch state.SelectedItem {
		case 0: // Auto-Compaction toggle
			c.ToggleAutoCompaction()
			return true
		case 1: // Auto-Continue toggle
			c.ToggleAutoCompactionContinue()
			return true
		case 2: // Threshold Mode toggle (percent <-> fixed_tokens)
			c.ToggleThresholdMode(provider.DefaultUnknownContextWindow)
			return true
		case 4: // Per-Model override toggle
			c.ToggleModelOverride(provider.DefaultUnknownContextWindow)
			return true
		}
	case "left", "h":
		// Adjust threshold value down
		if state.SelectedItem == 3 {
			c.AdjustThresholdValue(-1)
			return true
		}
	case "right", "l":
		// Adjust threshold value up
		if state.SelectedItem == 3 {
			c.AdjustThresholdValue(1)
			return true
		}
	}

	return false
}

// Render renders the threshold-only compaction settings view.
func (c *CompactionSettings) Render(width, height int, state *State, theme any) string {
	th := theme.(Theme)

	if width < 36 || height < 10 {
		return c.renderMinimal(width, height, th)
	}

	const (
		borderWidth      = 2
		containerPadding = 1
		titleHeight      = 4
		hintHeight       = 2
	)

	innerWidth := maxInt(20, width-(borderWidth*2)-(containerPadding*2))
	innerHeight := maxInt(5, height-(borderWidth*2)-(containerPadding*2)-titleHeight-hintHeight)

	// Render sections
	title := c.renderTitle(innerWidth, th)
	content := c.renderSettingsContent(innerWidth, innerHeight, state, th)
	hints := c.renderHintBar(innerWidth, th)

	// Combine sections
	fullContent := lipgloss.JoinVertical(lipgloss.Left,
		title,
		content,
		hints,
	)

	// Apply container border
	containerStyle := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color(th.Border)).
		Width(maxInt(20, width-2)).
		Padding(0, containerPadding)

	return containerStyle.Render(fullContent)
}

// renderMinimal renders a minimal view for small terminals
func (c *CompactionSettings) renderMinimal(width, height int, th Theme) string {
	msg := i18n.T("settings.compaction.terminal_small")
	return lipgloss.NewStyle().
		Width(maxInt(20, width)).
		Height(maxInt(5, height)).
		Foreground(lipgloss.Color(th.TextMuted)).
		Align(lipgloss.Center, lipgloss.Center).
		Render(msg)
}

// renderTitle renders the section title with status badges
func (c *CompactionSettings) renderTitle(width int, th Theme) string {
	titleStyle := lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color(th.Primary)).
		Width(width).
		Align(lipgloss.Center)

	title := titleStyle.Render(i18n.T("settings.compaction.title"))

	// Status badges
	autoCompactStatus := i18n.T("settings.common.off")
	autoCompactColor := th.Error
	if c.enableAutoCompaction {
		autoCompactStatus = i18n.T("settings.common.on")
		autoCompactColor = th.Success
	}

	autoBadge := lipgloss.NewStyle().
		Background(lipgloss.Color(autoCompactColor)).
		Foreground(lipgloss.Color(th.Text)).
		Padding(0, 1).
		Bold(true).
		Render(i18n.T("settings.compaction.badge.auto", autoCompactStatus))

	if width < 50 {
		// Show only one badge at narrow widths
		centeredBadges := lipgloss.NewStyle().Width(width).Align(lipgloss.Center).Render(autoBadge)
		return lipgloss.JoinVertical(lipgloss.Left, title, "", centeredBadges)
	}

	thresholdBadgeStyle := lipgloss.NewStyle().
		Background(lipgloss.Color(th.BGLight)).
		Foreground(lipgloss.Color(th.Text)).
		Padding(0, 1)
	cth := c.GetAutoCompactionThreshold()
	thresholdBadgeText := i18n.T("settings.compaction.badge.threshold_percent", int(cth.AsPercent(0)*100))
	if cth.Mode == commands.CompactionThresholdFixedTokens {
		thresholdBadgeText = i18n.T("settings.compaction.badge.threshold_tokens", formatTokenCount(int(cth.Value)))
	}
	thresholdBadge := thresholdBadgeStyle.Render(thresholdBadgeText)

	badgesRow := lipgloss.JoinHorizontal(lipgloss.Center, autoBadge, " ", thresholdBadge)
	centeredBadges := lipgloss.NewStyle().Width(width).Align(lipgloss.Center).Render(badgesRow)

	return lipgloss.JoinVertical(lipgloss.Left, title, "", centeredBadges)
}

// renderSettingsContent renders the settings content with proper selection state
func (c *CompactionSettings) renderSettingsContent(width, height int, state *State, th Theme) string {
	lines := c.renderAutoCompactionSection(width, th, state)
	content := lipgloss.JoinVertical(lipgloss.Left, lines...)
	return content
}

// renderAutoCompactionSection renders auto-compaction settings with selection state
func (c *CompactionSettings) renderAutoCompactionSection(width int, th Theme, state *State) []string {
	var lines []string

	acPad := 2
	acDetailPad := 4
	if width < 50 {
		acPad = 1
		acDetailPad = 2
	}

	sectionTitle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(th.Primary)).
		Bold(true).
		Render(i18n.T("settings.compaction.auto.title"))

	lines = append(lines, sectionTitle)
	if width >= 50 {
		lines = append(lines, lipgloss.NewStyle().
			Foreground(lipgloss.Color(th.TextMuted)).
			Padding(0, acPad).
			Render(i18n.T("settings.compaction.auto.description")))
	}
	lines = append(lines, "")

	// Item 0: Auto-Compaction Toggle
	isSelected := 0 == state.SelectedItem && state.Focus == FocusContent
	autoToggleStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(th.Text)).
		Padding(0, acPad)

	autoStatus := "[ ] OFF"
	if c.enableAutoCompaction {
		autoToggleStyle = autoToggleStyle.
			Background(lipgloss.Color(th.Success)).
			Bold(true)
		autoStatus = "[✓] ON"
	}
	if isSelected {
		autoToggleStyle = autoToggleStyle.
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color(th.Primary))
	}

	lines = append(lines, autoToggleStyle.Render(autoStatus))
	lines = append(lines, lipgloss.NewStyle().
		Foreground(lipgloss.Color(th.TextMuted)).
		Padding(0, acDetailPad).
		Render(i18n.T("settings.compaction.auto.toggle_hint")))
	lines = append(lines, "")

	// Item 1: Auto-Continue Toggle
	isSelected = 1 == state.SelectedItem && state.Focus == FocusContent
	continueToggleStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(th.Text)).
		Padding(0, acPad)

	continueStatus := "[ ] OFF"
	if c.autoCompactionContinueIfRunning {
		continueToggleStyle = continueToggleStyle.
			Background(lipgloss.Color(th.Success)).
			Bold(true)
		continueStatus = "[✓] ON"
	}
	if isSelected {
		continueToggleStyle = continueToggleStyle.
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color(th.Primary))
	}

	lines = append(lines, continueToggleStyle.Render(continueStatus))
	lines = append(lines, lipgloss.NewStyle().
		Foreground(lipgloss.Color(th.TextMuted)).
		Padding(0, acDetailPad).
		Render(i18n.T("settings.compaction.continue_hint")))
	lines = append(lines, "")

	// Item 2: Threshold Mode toggle (percent <-> fixed_tokens)
	isSelected = 2 == state.SelectedItem && state.Focus == FocusContent
	cth := c.GetAutoCompactionThreshold()
	modeLabel := i18n.T("settings.compaction.mode.percent")
	if cth.Mode == commands.CompactionThresholdFixedTokens {
		modeLabel = i18n.T("settings.compaction.mode.tokens")
	}
	modeStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(th.Text)).
		Padding(0, acPad)
	modeText := i18n.T("settings.compaction.mode.label", modeLabel)
	if isSelected {
		modeStyle = modeStyle.
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color(th.Primary))
	}
	lines = append(lines, modeStyle.Render(modeText))
	lines = append(lines, lipgloss.NewStyle().
		Foreground(lipgloss.Color(th.TextMuted)).
		Padding(0, acDetailPad).
		Render(i18n.T("settings.compaction.mode.hint")))
	lines = append(lines, "")

	// Item 3: Threshold Value adjuster (mode-aware)
	isSelected = 3 == state.SelectedItem && state.Focus == FocusContent
	valueText := c.renderThresholdValue(cth, width)
	valueStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(th.Text)).
		Background(lipgloss.Color(th.BGLight)).
		Padding(0, acPad)
	if isSelected {
		valueStyle = valueStyle.
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color(th.Primary))
	}
	lines = append(lines, valueStyle.Render(valueText))
	lines = append(lines, lipgloss.NewStyle().
		Foreground(lipgloss.Color(th.TextMuted)).
		Padding(0, acDetailPad).
		Render(c.renderThresholdValueHint(cth)))
	lines = append(lines, "")

	// Item 4: Per-Model override toggle (current model)
	isSelected = 4 == state.SelectedItem && state.Focus == FocusContent
	lines = append(lines, c.renderPerModelOverrideRow(width, th, cth, isSelected, acPad, acDetailPad)...)

	return lines
}

// renderThresholdValue renders the threshold value display (mode-aware).
func (c *CompactionSettings) renderThresholdValue(th commands.CompactionThreshold, width int) string {
	switch th.Mode {
	case commands.CompactionThresholdFixedTokens:
		return i18n.T("settings.compaction.tokens", formatTokenCount(int(th.Value)))
	default: // percent
		percent := int(th.AsPercent(0) * 100)
		barWidth := minInt(20, maxInt(8, width-20))
		filledWidth := int(float64(barWidth) * ((th.AsPercent(0) - 0.5) / 0.45))
		if filledWidth < 0 {
			filledWidth = 0
		}
		if filledWidth > barWidth {
			filledWidth = barWidth
		}
		var bar strings.Builder
		for i := range barWidth {
			if i < filledWidth {
				bar.WriteString("█")
			} else {
				bar.WriteString("░")
			}
		}
		return fmt.Sprintf("[%s] %d%%", bar.String(), percent)
	}
}

// renderThresholdValueHint returns the help text for the value adjuster.
func (c *CompactionSettings) renderThresholdValueHint(th commands.CompactionThreshold) string {
	switch th.Mode {
	case commands.CompactionThresholdFixedTokens:
		return i18n.T("settings.compaction.adjust.tokens")
	default:
		return i18n.T("settings.compaction.adjust.percent")
	}
}

// renderPerModelOverrideRow renders the per-model override toggle for the
// current chat model.
func (c *CompactionSettings) renderPerModelOverrideRow(width int, theme Theme, globalTh commands.CompactionThreshold, isSelected bool, acPad, acDetailPad int) []string {
	var lines []string

	titleStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(theme.TextMuted)).
		Bold(true)
	lines = append(lines, titleStyle.Render(i18n.T("settings.compaction.per_model.title")))

	modelLabel := c.currentChatModel
	if c.currentChatProvider != "" {
		modelLabel = fmt.Sprintf("%s/%s", c.currentChatProvider, c.currentChatModel)
	}
	if modelLabel == "" {
		modelLabel = i18n.T("settings.compaction.per_model.none")
	}

	hasOverride := c.currentChatProvider != "" && c.currentChatModel != "" &&
		c.HasThresholdOverrideForModel(c.currentChatProvider, c.currentChatModel)

	status := i18n.T("settings.compaction.per_model.global")
	effectLabel := i18n.T("settings.compaction.per_model.inherits")
	if hasOverride {
		status = i18n.T("settings.compaction.per_model.override")
		if ov, ok := c.GetThresholdOverride(c.currentChatProvider, c.currentChatModel); ok {
			effectLabel = c.renderThresholdValue(ov, width)
		}
	}

	overrideStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(theme.Text)).
		Padding(0, acPad)
	if isSelected {
		overrideStyle = overrideStyle.
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color(theme.Primary))
	}
	lines = append(lines, overrideStyle.Render(fmt.Sprintf("%s  %s", status, modelLabel)))
	lines = append(lines, lipgloss.NewStyle().
		Foreground(lipgloss.Color(theme.TextMuted)).
		Padding(0, acDetailPad).
		Render(i18n.T("settings.compaction.per_model.hint", effectLabel)))

	return lines
}

// formatTokenCount renders an integer with thousands separators.
func formatTokenCount(n int) string {
	if n < 1000 {
		return fmt.Sprintf("%d", n)
	}
	s := fmt.Sprintf("%d", n)
	var b strings.Builder
	for i, ch := range s {
		if i > 0 && (len(s)-i)%3 == 0 {
			b.WriteByte(',')
		}
		b.WriteRune(ch)
	}
	return b.String()
}

// renderHintBar renders keyboard navigation hints at the bottom
func (c *CompactionSettings) renderHintBar(width int, th Theme) string {
	if width < 45 {
		return "" // No room for hints
	}

	hintStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(th.TextMuted)).
		Width(width).
		Align(lipgloss.Center)

	keyStyle := lipgloss.NewStyle().
		Background(lipgloss.Color(th.BGLight)).
		Foreground(lipgloss.Color(th.Text)).
		Padding(0, 1).
		Bold(true)

	var hints string
	if width < 65 {
		hints = i18n.T("settings.compaction.hints.compact",
			keyStyle.Render("↑↓"),
			keyStyle.Render("Space"),
			keyStyle.Render("←→"),
			keyStyle.Render("M"))
	} else {
		hints = i18n.T("settings.compaction.hints",
			keyStyle.Render("↑/↓"),
			keyStyle.Render("Space"),
			keyStyle.Render("←/→"))
	}

	return hintStyle.Render(hints)
}

// saveAutoCompactionSettings saves auto-compaction settings to config.
func (c *CompactionSettings) saveAutoCompactionSettings() {
	if c.configManager == nil {
		logDebug("[CompactionSettings] saveAutoCompactionSettings failed: configManager is nil")
		return
	}

	config, err := c.configManager.LoadConfig()
	if err != nil {
		logDebug("[CompactionSettings] saveAutoCompactionSettings failed: could not load config: %v", err)
		return
	}

	// Update config values
	config.SetEnableAutoCompaction(c.enableAutoCompaction)
	config.AutoCompactionContinueIfRunning = c.autoCompactionContinueIfRunning
	config.AutoCompactionThresholdPercent = c.autoCompactionThresholdPercent
	if c.autoCompactionThreshold.Mode != "" {
		config.AutoCompactionThreshold = c.autoCompactionThreshold
		// Keep legacy percent in sync for back-compat consumers.
		if c.autoCompactionThreshold.Mode == commands.CompactionThresholdPercent {
			config.AutoCompactionThresholdPercent = c.autoCompactionThreshold.Value
		}
	}
	// Persist per-model overrides.
	if len(c.compactionThresholdOverrides) > 0 {
		overrides := make(map[string]commands.CompactionThreshold, len(c.compactionThresholdOverrides))
		for k, v := range c.compactionThresholdOverrides {
			overrides[k] = v
		}
		config.CompactionThresholdOverrides = overrides
	} else {
		config.CompactionThresholdOverrides = nil
	}

	if err := c.configManager.SaveConfig(config); err != nil {
		logDebug("[CompactionSettings] saveAutoCompactionSettings failed: could not save config: %v", err)
		return
	}

	logDebug("[CompactionSettings] auto-compaction settings saved successfully")
	c.hasChanges = false
}

// Save persists all compaction settings to disk.
// Call this after one or more mutations to commit changes.
// This matches the pattern used by ReliabilitySettings and DreamSettings.
func (c *CompactionSettings) Save() error {
	if c == nil || c.configManager == nil {
		return nil
	}

	config, err := c.configManager.LoadConfig()
	if err != nil || config == nil {
		config = &commands.SwarmOSConfig{}
	}

	config.SetEnableAutoCompaction(c.enableAutoCompaction)
	config.AutoCompactionContinueIfRunning = c.autoCompactionContinueIfRunning
	config.AutoCompactionThresholdPercent = c.autoCompactionThresholdPercent
	if c.autoCompactionThreshold.Mode != "" {
		config.AutoCompactionThreshold = c.autoCompactionThreshold
		if c.autoCompactionThreshold.Mode == commands.CompactionThresholdPercent {
			config.AutoCompactionThresholdPercent = c.autoCompactionThreshold.Value
		}
	}
	if len(c.compactionThresholdOverrides) > 0 {
		overrides := make(map[string]commands.CompactionThreshold, len(c.compactionThresholdOverrides))
		for k, v := range c.compactionThresholdOverrides {
			overrides[k] = v
		}
		config.CompactionThresholdOverrides = overrides
	} else {
		config.CompactionThresholdOverrides = nil
	}

	if err := c.configManager.SaveConfig(config); err != nil {
		return fmt.Errorf("compaction settings: save failed: %w", err)
	}

	c.hasChanges = false
	return nil
}
