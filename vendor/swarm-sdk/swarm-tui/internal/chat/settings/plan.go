package settings

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/chat/commands"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/i18n"
)

// PlanSettings manages the Plan Mode configuration panel.
// Plan Mode registers enter_plan_mode and exit_plan_mode tools for the agent,
// enabling a structured PLAN → ACT workflow before implementation.
type PlanSettings struct {
	configManager *commands.ConfigManager

	enabled      bool
	autoClear    bool
	planFileName string

	hasChanges bool
}

// NewPlanSettings creates a new PlanSettings loaded from persisted config.
func NewPlanSettings(configManagers ...*commands.ConfigManager) *PlanSettings {
	var cm *commands.ConfigManager
	if len(configManagers) > 0 {
		cm = configManagers[0]
	} else {
		cm, _ = commands.NewConfigManager()
	}

	var config *commands.SwarmOSConfig
	if cm != nil {
		if cfg, err := cm.LoadConfig(); err == nil {
			config = cfg
		}
	}
	if config == nil {
		config = &commands.SwarmOSConfig{}
	}

	fileName := config.PlanModeFileName
	if fileName == "" {
		fileName = "PLAN.md"
	}

	// Default to enabled if not explicitly set in config
	enabled := config.PlanModeEnabled
	// If config is empty (no saved settings), default to enabled
	if !config.PlanModeEnabled && config.PlanModeFileName == "" && !config.PlanModeAutoClear {
		enabled = true // Default ON for new installations
	}

	return &PlanSettings{
		configManager: cm,
		enabled:       enabled,
		autoClear:     config.PlanModeAutoClear,
		planFileName:  fileName,
	}
}

// IsEnabled returns true if plan mode tools are registered.
func (p *PlanSettings) IsEnabled() bool {
	if p == nil {
		return true // enabled by default
	}
	return p.enabled
}

// GetAutoClear returns whether context is auto-compacted after plan approval.
func (p *PlanSettings) GetAutoClear() bool {
	if p == nil {
		return false
	}
	return p.autoClear
}

// GetPlanFileName returns the configured plan file name.
func (p *PlanSettings) GetPlanFileName() string {
	if p == nil || p.planFileName == "" {
		return "PLAN.md"
	}
	return p.planFileName
}

// ReloadFromConfig reloads settings from the configuration file
func (p *PlanSettings) ReloadFromConfig() {
	if p.configManager == nil {
		return
	}

	if cfg, err := p.configManager.LoadConfig(); err == nil && cfg != nil {
		p.enabled = cfg.PlanModeEnabled
		p.autoClear = cfg.PlanModeAutoClear

		fileName := cfg.PlanModeFileName
		if fileName == "" {
			fileName = "PLAN.md"
		}
		p.planFileName = fileName

		p.hasChanges = false
	}
}

// Save persists the current configuration to disk (public API, returns error).
func (p *PlanSettings) Save() error {
	if p == nil || p.configManager == nil {
		return nil
	}
	cfg, err := p.configManager.LoadConfig()
	if err != nil {
		cfg = &commands.SwarmOSConfig{}
	}
	cfg.PlanModeEnabled = p.enabled
	cfg.PlanModeAutoClear = p.autoClear
	cfg.PlanModeFileName = p.planFileName
	if err := p.configManager.SaveConfig(cfg); err != nil {
		return fmt.Errorf("plan settings: save failed: %w", err)
	}
	p.hasChanges = false
	return nil
}

// ─── Key handling ─────────────────────────────────────────────────────────────

// HandleKey processes keyboard input for plan settings.
// Returns true if the key was consumed.
//
// Item layout:
//
//	0 = Enable toggle
//	1 = AutoClear toggle
func (p *PlanSettings) HandleKey(key string, state *State) bool {
	if p == nil || state.Focus != FocusContent {
		return false
	}

	const totalItems = 2

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
	case " ", "space", "enter":
		switch state.SelectedItem {
		case 0:
			p.enabled = !p.enabled
			p.save()
			return true
		case 1:
			p.autoClear = !p.autoClear
			p.save()
			return true
		}
	}

	return false
}

// save persists on the hot path (no error return).
func (p *PlanSettings) save() {
	if err := p.Save(); err != nil {
		logDebug("[PlanSettings] save failed: %v", err)
	}
}

// ─── Rendering ────────────────────────────────────────────────────────────────

// Render returns the rendered settings panel content for Plan Mode.
func (p *PlanSettings) Render(width, height int, state *State, theme any) string {
	_ = height

	th := theme.(Theme)

	teal := lipgloss.Color("#00BCD4")

	headerStyle := lipgloss.NewStyle().Foreground(teal).Bold(true)
	dimStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(th.TextMuted))

	innerWidth := maxInt(20, width-4)
	pad := 2
	detailPad := 4
	if width < 50 {
		pad = 1
		detailPad = 2
	}

	var sb strings.Builder

	// ── Header ────────────────────────────────────────────────────────────────
	sb.WriteString(headerStyle.Render(i18n.T("settings.plan.title")))
	sb.WriteString("\n")
	sb.WriteString(dimStyle.Render(i18n.T("settings.plan.description.line1")))
	sb.WriteString("\n")
	sb.WriteString(dimStyle.Render(i18n.T("settings.plan.description.line2")))
	sb.WriteString("\n\n")

	// ── Item 0: Enable toggle ─────────────────────────────────────────────────
	isSelected0 := state != nil && state.SelectedItem == 0 && state.Focus == FocusContent
	sb.WriteString(p.renderToggleItem(
		i18n.T("settings.plan.enable.label"),
		i18n.T("settings.plan.enable.description"),
		p.enabled,
		isSelected0,
		pad, detailPad, innerWidth, th,
	))
	sb.WriteString("\n\n")

	// ── Item 1: AutoClear toggle ──────────────────────────────────────────────
	isSelected1 := state != nil && state.SelectedItem == 1 && state.Focus == FocusContent
	sb.WriteString(p.renderToggleItem(
		i18n.T("settings.plan.auto_compact.label"),
		i18n.T("settings.plan.auto_compact.description"),
		p.autoClear,
		isSelected1,
		pad, detailPad, innerWidth, th,
	))
	sb.WriteString("\n\n")

	// ── Plan file name (read-only display) ────────────────────────────────────
	labelStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(th.Text)).Padding(0, pad)
	sb.WriteString(labelStyle.Render(i18n.T("settings.plan.file.label")))
	sb.WriteString("\n")
	sb.WriteString(strings.Repeat(" ", detailPad))
	sb.WriteString(lipgloss.NewStyle().Foreground(teal).Render(p.planFileName))
	sb.WriteString("\n")
	sb.WriteString(dimStyle.Render(strings.Repeat(" ", detailPad) + i18n.T("settings.plan.file.description")))
	sb.WriteString("\n\n")

	// ── How it works ──────────────────────────────────────────────────────────
	sb.WriteString(dimStyle.Render(i18n.T("settings.plan.how_it_works")))
	sb.WriteString("\n")
	steps := []string{
		i18n.T("settings.plan.step.1"),
		i18n.T("settings.plan.step.2"),
		i18n.T("settings.plan.step.3"),
		i18n.T("settings.plan.step.4"),
		i18n.T("settings.plan.step.5"),
		i18n.T("settings.plan.step.6"),
	}
	for _, s := range steps {
		sb.WriteString(dimStyle.Render(strings.Repeat(" ", detailPad) + s))
		sb.WriteString("\n")
	}
	sb.WriteString("\n")

	// ── Hint bar ──────────────────────────────────────────────────────────────
	sb.WriteString(p.renderHintBar(innerWidth, th))

	return sb.String()
}

// renderToggleItem renders a labelled boolean toggle row with selection highlight.
// Matches the exact pattern from DreamSettings.renderToggleItem.
func (p *PlanSettings) renderToggleItem(label, description string, value, isSelected bool, pad, detailPad, width int, th Theme) string {
	checkmark := "[ ]"
	toggleStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(th.Text)).
		Padding(0, pad)

	if value {
		checkmark = "[✓]"
		toggleStyle = toggleStyle.
			Background(lipgloss.Color(th.Success)).
			Bold(true)
	}
	if isSelected {
		toggleStyle = toggleStyle.
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color(th.Primary))
	}

	row := fmt.Sprintf("%s %s", checkmark, label)
	lines := []string{toggleStyle.Render(row)}
	if width >= 50 {
		lines = append(lines, lipgloss.NewStyle().
			Foreground(lipgloss.Color(th.TextMuted)).
			Padding(0, detailPad).
			Render(description))
	}
	return lipgloss.JoinVertical(lipgloss.Left, lines...)
}

// renderHintBar renders keyboard navigation hints at the bottom.
func (p *PlanSettings) renderHintBar(width int, th Theme) string {
	if width < 45 {
		return ""
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

	hints := i18n.T("settings.plan.hints",
		keyStyle.Render("↑/↓"),
		keyStyle.Render("Space"),
		keyStyle.Render("tab"),
		keyStyle.Render("esc"),
	)

	return hintStyle.Render(hints)
}
