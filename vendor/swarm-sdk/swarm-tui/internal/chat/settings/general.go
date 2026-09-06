package settings

import (
	"fmt"

	"charm.land/lipgloss/v2"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/chat/commands"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/i18n"
)

// GeneralSettings handles general application settings
type GeneralSettings struct {
	sidePanelEnabled       bool
	compactMode            bool
	debugMode              bool
	maxHistory             int
	microCompactionEnabled bool
	microRetentionCount    int
	telemetryEnabled       bool
	telemetryConsentAck    bool
	language               i18n.Language

	configManager *commands.ConfigManager
}

// NewGeneralSettings creates a new general settings handler with persistence.
func NewGeneralSettings(cm *commands.ConfigManager) *GeneralSettings {
	g := &GeneralSettings{
		sidePanelEnabled:       true,
		compactMode:            false,
		debugMode:              false,
		maxHistory:             100,
		microCompactionEnabled: true,
		microRetentionCount:    3,
		telemetryEnabled:       false, // telemetry is opt-in / off by default
		telemetryConsentAck:    false,
		language:               i18n.LanguageEnglish,
		configManager:          cm,
	}

	// Restore persisted values from config.json.
	if cm != nil {
		if cfg, err := cm.LoadConfig(); err == nil {
			// SidePanelEnabled uses a pointer so we can distinguish "never written"
			// (nil 	 keep default true) from "user disabled it" (false).
			if cfg.SidePanelEnabled != nil {
				g.sidePanelEnabled = *cfg.SidePanelEnabled
			}
			g.compactMode = cfg.CompactMode
			g.debugMode = cfg.DebugMode
			if cfg.MaxHistory > 0 {
				g.maxHistory = cfg.MaxHistory
			}
			g.microCompactionEnabled = cfg.EnableMicroCompaction
			if cfg.MicroRetentionCount > 0 {
				g.microRetentionCount = cfg.MicroRetentionCount
			}
			g.telemetryEnabled = cfg.TelemetryEnabled
			g.telemetryConsentAck = cfg.TelemetryConsentAcknowledged
			g.language = i18n.SetLanguage(cfg.Language)
		}
	}

	return g
}

// save writes current general settings to config.json immediately.
func (g *GeneralSettings) save() {
	if g.configManager == nil {
		logDebug("[GeneralSettings] save failed: configManager is nil")
		return
	}
	cfg, err := g.configManager.LoadConfig()
	if err != nil || cfg == nil {
		logDebug("[GeneralSettings] save failed: could not load config: %v", err)
		return
	}
	boolPtr := func(b bool) *bool { return &b }
	cfg.SidePanelEnabled = boolPtr(g.sidePanelEnabled)
	cfg.CompactMode = g.compactMode
	cfg.DebugMode = g.debugMode
	cfg.MaxHistory = g.maxHistory
	cfg.EnableMicroCompaction = g.microCompactionEnabled
	cfg.MicroRetentionCount = g.microRetentionCount
	cfg.TelemetryEnabled = g.telemetryEnabled
	cfg.TelemetryConsentAcknowledged = g.telemetryConsentAck
	cfg.Language = string(g.language)
	if err := g.configManager.SaveConfig(cfg); err != nil {
		logDebug("[GeneralSettings] save failed: could not save config: %v", err)
		return
	}
	logDebug("[GeneralSettings] settings saved successfully")
}

// ReloadFromConfig reloads settings from the configuration file
func (g *GeneralSettings) ReloadFromConfig() {
	if g.configManager == nil {
		return
	}

	if cfg, err := g.configManager.LoadConfig(); err == nil && cfg != nil {
		// SidePanelEnabled uses a pointer so we can distinguish "never written"
		// (nil 	 keep default true) from "user disabled it" (false).
		if cfg.SidePanelEnabled != nil {
			g.sidePanelEnabled = *cfg.SidePanelEnabled
		}
		g.compactMode = cfg.CompactMode
		g.debugMode = cfg.DebugMode
		if cfg.MaxHistory > 0 {
			g.maxHistory = cfg.MaxHistory
		}
		g.microCompactionEnabled = cfg.EnableMicroCompaction
		if cfg.MicroRetentionCount > 0 {
			g.microRetentionCount = cfg.MicroRetentionCount
		}
		g.telemetryEnabled = cfg.TelemetryEnabled
		g.telemetryConsentAck = cfg.TelemetryConsentAcknowledged
		g.language = i18n.SetLanguage(cfg.Language)
	}
}

// GetLanguage returns the canonical active UI language.
func (g *GeneralSettings) GetLanguage() i18n.Language {
	return g.language
}

// SetLanguage switches the UI immediately and persists the canonical locale.
func (g *GeneralSettings) SetLanguage(value string) {
	g.language = i18n.SetLanguage(value)
	g.save()
}

// CycleLanguage moves between the two supported UI languages.
func (g *GeneralSettings) CycleLanguage(delta int) {
	if delta == 0 {
		return
	}
	if g.language == i18n.LanguageSpanish {
		g.SetLanguage(string(i18n.LanguageEnglish))
		return
	}
	g.SetLanguage(string(i18n.LanguageSpanish))
}

// GetSidePanelEnabled returns side panel state
func (g *GeneralSettings) GetSidePanelEnabled() bool {
	return g.sidePanelEnabled
}

// GetCompactMode returns compact mode state
func (g *GeneralSettings) GetCompactMode() bool {
	return g.compactMode
}

// GetDebugMode returns debug mode state
func (g *GeneralSettings) GetDebugMode() bool {
	return g.debugMode
}

// GetMaxHistory returns max history value
func (g *GeneralSettings) GetMaxHistory() int {
	return g.maxHistory
}

// ToggleSidePanel toggles side panel
func (g *GeneralSettings) ToggleSidePanel() {
	g.SetSidePanelEnabled(!g.sidePanelEnabled)
}

// SetSidePanelEnabled sets the side panel preference and persists it.
func (g *GeneralSettings) SetSidePanelEnabled(enabled bool) {
	g.sidePanelEnabled = enabled
	g.save()
}

// ToggleCompactMode toggles compact mode
func (g *GeneralSettings) ToggleCompactMode() {
	g.compactMode = !g.compactMode
	g.save()
}

// ToggleDebugMode toggles debug mode
func (g *GeneralSettings) ToggleDebugMode() {
	g.debugMode = !g.debugMode
	g.save()
}

// GetTelemetryEnabled returns whether telemetry opt-in is enabled.
func (g *GeneralSettings) GetTelemetryEnabled() bool {
	return g.telemetryEnabled
}

// TelemetryConsentAcknowledged reports whether the one-time consent modal has
// already been shown and answered.
func (g *GeneralSettings) TelemetryConsentAcknowledged() bool {
	return g.telemetryConsentAck
}

// SetTelemetryEnabled sets the telemetry opt-in state, marks consent as
// acknowledged, and persists immediately. Called from the consent modal's
// Accept/Decline callback.
func (g *GeneralSettings) SetTelemetryEnabled(enabled bool) {
	g.telemetryEnabled = enabled
	g.telemetryConsentAck = true
	g.save()
}

// ToggleTelemetry flips the telemetry opt-in state and persists. The settings
// key handler is responsible for showing the consent modal before enabling.
func (g *GeneralSettings) ToggleTelemetry() {
	g.telemetryEnabled = !g.telemetryEnabled
	g.telemetryConsentAck = true
	g.save()
}

// SetMaxHistory sets max history
func (g *GeneralSettings) SetMaxHistory(value int) {
	if value < 10 {
		value = 10
	}
	if value > 1000 {
		value = 1000
	}
	g.maxHistory = value
	g.save()
}

// AdjustMaxHistory adjusts max history by delta
func (g *GeneralSettings) AdjustMaxHistory(delta int) {
	g.SetMaxHistory(g.maxHistory + delta)
}

// GetMicroCompactionEnabled returns micro-compaction enabled state
func (g *GeneralSettings) GetMicroCompactionEnabled() bool {
	return g.microCompactionEnabled
}

// GetMicroRetentionCount returns micro-compaction retention count
func (g *GeneralSettings) GetMicroRetentionCount() int {
	return g.microRetentionCount
}

// ToggleMicroCompaction toggles micro-compaction
func (g *GeneralSettings) ToggleMicroCompaction() {
	g.microCompactionEnabled = !g.microCompactionEnabled
	g.save()
}

// SetMicroRetentionCount sets micro-compaction retention count
func (g *GeneralSettings) SetMicroRetentionCount(value int) {
	if value < 1 {
		value = 1
	}
	if value > 10 {
		value = 10
	}
	g.microRetentionCount = value
	g.save()
}

// AdjustMicroRetentionCount adjusts micro-compaction retention count by delta
func (g *GeneralSettings) AdjustMicroRetentionCount(delta int) {
	g.SetMicroRetentionCount(g.microRetentionCount + delta)
}

func (g *GeneralSettings) Render(width, height int, state *State, theme any) string {
	th := theme.(Theme)

	const (
		borderWidth      = 2
		containerPadding = 1
		titleHeight      = 4
		hintHeight       = 2
	)

	innerWidth := maxInt(20, width-(borderWidth*2)-(containerPadding*2))
	innerHeight := maxInt(5, height-(borderWidth*2)-(containerPadding*2)-titleHeight-hintHeight)

	// Render sections
	title := g.renderTitle(innerWidth, th)
	content := g.renderSettingsContent(innerWidth, innerHeight, state, th)
	hints := g.renderHintBar(innerWidth, th)

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
		Background(lipgloss.Color(th.BG)).
		Width(maxInt(20, width-2)).
		Padding(0, containerPadding)

	return containerStyle.Render(fullContent)
}

// renderTitle renders the section title with status badges
func (g *GeneralSettings) renderTitle(width int, th Theme) string {
	titleStyle := lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color(th.Primary)).
		Width(width).
		Align(lipgloss.Center)

	title := titleStyle.Render(i18n.T("settings.general.title"))

	// Count enabled settings
	enabledCount := 0
	if g.sidePanelEnabled {
		enabledCount++
	}
	if g.compactMode {
		enabledCount++
	}
	if g.debugMode {
		enabledCount++
	}
	if g.microCompactionEnabled {
		enabledCount++
	}

	// Build status badges
	statusBadge := lipgloss.NewStyle().
		Background(lipgloss.Color(th.Success)).
		Foreground(lipgloss.Color(th.Text)).
		Padding(0, 1).
		Bold(true).
		Render(i18n.T("settings.general.badge.active", enabledCount))

	totalBadge := lipgloss.NewStyle().
		Background(lipgloss.Color(th.TextMuted)).
		Foreground(lipgloss.Color(th.Text)).
		Padding(0, 1).
		Render(i18n.T("settings.general.badge.toggles", 5))

	historyBadgeBg := th.BGLight
	if historyBadgeBg == "" {
		historyBadgeBg = th.BG
	}
	historyBadge := lipgloss.NewStyle().
		Background(lipgloss.Color(historyBadgeBg)).
		Foreground(lipgloss.Color(th.Text)).
		Padding(0, 1).
		Render(i18n.T("settings.general.badge.history", g.maxHistory))

	// At narrow widths, hide badges to prevent overflow
	if width < 50 {
		return title
	}

	var badgesRow string
	if width < 70 {
		// Show only the status badge at medium widths
		badgesRow = statusBadge
	} else {
		badgesRow = lipgloss.JoinHorizontal(lipgloss.Center, statusBadge, " ", totalBadge, " ", historyBadge)
	}
	centeredBadges := lipgloss.NewStyle().Width(width).Align(lipgloss.Center).Render(badgesRow)

	return lipgloss.JoinVertical(lipgloss.Left, title, "", centeredBadges)
}

// renderSettingsContent renders the settings items with proper scrolling
func (g *GeneralSettings) renderSettingsContent(width, height int, state *State, th Theme) string {
	var lines []string

	// Settings items
	type settingItem struct {
		label       string
		description string
		itemType    string // "toggle", "number", "language"
		value       any
	}

	items := []settingItem{
		{i18n.T("settings.general.side_panel.label"), i18n.T("settings.general.side_panel.description"), "toggle", g.sidePanelEnabled},
		{i18n.T("settings.general.compact.label"), i18n.T("settings.general.compact.description"), "toggle", g.compactMode},
		{i18n.T("settings.general.debug.label"), i18n.T("settings.general.debug.description"), "toggle", g.debugMode},
		{i18n.T("settings.general.telemetry.label"), i18n.T("settings.general.telemetry.description"), "toggle", g.telemetryEnabled},
		{i18n.T("settings.general.micro_compaction.label"), i18n.T("settings.general.micro_compaction.description"), "toggle", g.microCompactionEnabled},
		{i18n.T("settings.general.retention.label"), i18n.T("settings.general.retention.description"), "number", g.microRetentionCount},
		{i18n.T("settings.general.max_history.label"), i18n.T("settings.general.max_history.description"), "number", g.maxHistory},
		{i18n.T("settings.general.language.label"), i18n.T("settings.general.language.description"), "language", g.language},
	}

	// Calculate visible range
	maxContentHeight := height / 4 // Each item ~4 lines
	state.MaxVisible = maxContentHeight
	if state.MaxVisible < 1 {
		state.MaxVisible = 1
	}

	visibleStart := state.ScrollOffset
	visibleEnd := state.ScrollOffset + state.MaxVisible
	if visibleEnd > len(items) {
		visibleEnd = len(items)
	}

	for i := visibleStart; i < visibleEnd; i++ {
		item := items[i]
		isSelected := i == state.SelectedItem && state.Focus == FocusContent

		// Label — reduce padding at narrow widths
		labelPadH := 2
		if width < 50 {
			labelPadH = 1
		}
		labelStyle := lipgloss.NewStyle().
			Foreground(lipgloss.Color(th.Text)).
			Bold(isSelected).
			Padding(0, labelPadH)
		if isSelected {
			labelStyle = labelStyle.Foreground(lipgloss.Color(th.Primary))
		}
		label := labelStyle.Render(fmt.Sprintf("%s %s", map[bool]string{true: "▶", false: " "}[isSelected], item.label))
		lines = append(lines, label)

		// Description — reduce padding at narrow widths
		descPadH := 4
		if width < 50 {
			descPadH = 2
		}
		descStyle := lipgloss.NewStyle().
			Foreground(lipgloss.Color(th.TextMuted)).
			Padding(0, descPadH)
		desc := descStyle.Render(item.description)
		lines = append(lines, desc)

		// Control
		var control string
		switch item.itemType {
		case "toggle":
			control = renderToggle(item.value.(bool), isSelected, th)
		case "number":
			control = renderNumber(item.value.(int), isSelected, th)
		case "language":
			control = renderLanguage(item.value.(i18n.Language), isSelected, th)
		}
		lines = append(lines, control, "")
	}

	// Scroll indicator
	if len(items) > state.MaxVisible {
		scrollInfo := i18n.T("settings.common.showing_range", visibleStart+1, visibleEnd, len(items))
		scrollStyle := lipgloss.NewStyle().
			Foreground(lipgloss.Color(th.TextMuted)).
			Italic(true).
			Padding(0, 2)
		lines = append(lines, scrollStyle.Render(scrollInfo))
	}

	content := lipgloss.JoinVertical(lipgloss.Left, lines...)
	return content
}

// renderHintBar renders keyboard navigation hints at the bottom
func (g *GeneralSettings) renderHintBar(width int, th Theme) string {
	hintStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(th.TextMuted)).
		Width(width).
		Align(lipgloss.Center)

	keyBg := th.BGLight
	if keyBg == "" {
		keyBg = th.BG
	}
	keyStyle := lipgloss.NewStyle().
		Background(lipgloss.Color(keyBg)).
		Foreground(lipgloss.Color(th.Text)).
		Padding(0, 1).
		Bold(true)

	hints := i18n.T("settings.general.hints",
		keyStyle.Render("↑/↓"),
		keyStyle.Render("Space"),
		keyStyle.Render("←/→"))

	return hintStyle.Render(hints)
}

func renderToggle(enabled bool, isSelected bool, th Theme) string {
	var toggleStyle lipgloss.Style
	var toggleText string

	if enabled {
		toggleStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color(th.Text)).
			Background(lipgloss.Color(th.Success)).
			Bold(true).
			Padding(0, 1).
			Margin(0, 2)
		toggleText = i18n.T("settings.common.toggle.on")
	} else {
		offBg := th.BGLight
		if offBg == "" {
			offBg = th.BG
		}
		toggleStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color(th.TextMuted)).
			Background(lipgloss.Color(offBg)).
			Padding(0, 1).
			Margin(0, 2)
		toggleText = i18n.T("settings.common.toggle.off")
	}

	if isSelected {
		toggleStyle = toggleStyle.
			Foreground(lipgloss.Color(th.Primary)).
			Bold(true)
	}

	return toggleStyle.Render(toggleText)
}

func renderNumber(value int, isSelected bool, th Theme) string {
	numBg := th.BGLight
	if numBg == "" {
		numBg = th.BG
	}
	numberStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(th.Text)).
		Background(lipgloss.Color(numBg)).
		Padding(0, 1).
		Margin(0, 2)

	if isSelected {
		numberStyle = numberStyle.
			Foreground(lipgloss.Color(th.Primary)).
			Bold(true)
	}

	return numberStyle.Render(fmt.Sprintf("[ %d ]  < - / + >", value))
}

func renderLanguage(language i18n.Language, isSelected bool, th Theme) string {
	bg := th.BGLight
	if bg == "" {
		bg = th.BG
	}
	style := lipgloss.NewStyle().
		Foreground(lipgloss.Color(th.Text)).
		Background(lipgloss.Color(bg)).
		Padding(0, 1).
		Margin(0, 2)
	if isSelected {
		style = style.Foreground(lipgloss.Color(th.Primary)).Bold(true)
	}

	label := i18n.T("settings.general.language.english")
	if language == i18n.LanguageSpanish {
		label = i18n.T("settings.general.language.spanish")
	}
	return style.Render(i18n.T("settings.general.language.control", label))
}

// Theme interface for rendering
type Theme struct {
	Primary   string
	Accent    string
	Text      string
	TextDim   string
	TextMuted string
	BG        string
	BGLight   string
	BGLighter string
	Border    string
	Success   string
	Warning   string
	Error     string
}
