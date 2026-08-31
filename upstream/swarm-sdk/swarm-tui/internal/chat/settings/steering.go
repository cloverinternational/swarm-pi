package settings

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/Swarm-Code/mono/swarm-core/core"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/i18n"
)

// SteeringSettings handles steering configuration UI state.
type SteeringSettings struct {
	// Data (mirrors core.SteeringConfig)
	enabled           bool
	runtimeEnabled    bool
	workflowEnabled   bool
	showDecisions     bool
	logDecisions      bool
	autoApproveSimple bool
	enableEfficiency  bool
	maxRetries        int
	maxFileReads      int

	// Scope selection
	scope string // "global" or "project"

	// Callbacks
	onConfigChange func(scope string, config *core.SteeringConfig) tea.Cmd
	loadConfig     func(scope string) (*core.SteeringConfig, error)
}

// NewSteeringSettings creates a new steering settings component.
func NewSteeringSettings() *SteeringSettings {
	return &SteeringSettings{
		scope: "global",
	}
}

// SetOnConfigChange registers a handler for config updates.
// The handler receives the scope and config, persists via IPC, and returns a cmd.
func (s *SteeringSettings) SetOnConfigChange(handler func(scope string, config *core.SteeringConfig) tea.Cmd) {
	s.onConfigChange = handler
}

// SetLoadConfig sets the function to load config via IPC.
func (s *SteeringSettings) SetLoadConfig(loader func(scope string) (*core.SteeringConfig, error)) {
	s.loadConfig = loader
}

// Load loads the steering config from the IPC layer.
func (s *SteeringSettings) Load() error {
	if s.loadConfig == nil {
		return nil
	}
	cfg, err := s.loadConfig(s.scope)
	if err != nil {
		return err
	}
	if cfg == nil {
		cfg = &core.SteeringConfig{}
	}
	s.syncFromConfig(cfg)
	return nil
}

// syncFromConfig copies config values to UI state.
func (s *SteeringSettings) syncFromConfig(cfg *core.SteeringConfig) {
	s.enabled = cfg.Enabled
	s.runtimeEnabled = cfg.RuntimeEnabled
	s.workflowEnabled = cfg.WorkflowEnabled
	s.showDecisions = cfg.ShowDecisionsInChat
	s.logDecisions = cfg.LogDecisions
	s.autoApproveSimple = cfg.AutoApproveSimple
	s.enableEfficiency = cfg.EnableEfficiency
	s.maxRetries = cfg.MaxRetries
	if s.maxRetries == 0 {
		s.maxRetries = 3
	}
	s.maxFileReads = cfg.MaxFileReads
	if s.maxFileReads == 0 {
		s.maxFileReads = 10
	}
}

// toConfig builds a core config from UI state.
func (s *SteeringSettings) toConfig() *core.SteeringConfig {
	return &core.SteeringConfig{
		Enabled:             s.enabled,
		RuntimeEnabled:      s.runtimeEnabled,
		WorkflowEnabled:     s.workflowEnabled,
		ShowDecisionsInChat: s.showDecisions,
		LogDecisions:        s.logDecisions,
		AutoApproveSimple:   s.autoApproveSimple,
		EnableEfficiency:    s.enableEfficiency,
		MaxRetries:          s.maxRetries,
		MaxFileReads:        s.maxFileReads,
		// Default intervention points for runtime steering
		InterventionPoints: []string{"before_tool", "after_tool"},
		// Default steering type for workflow
		SteeringType: "rule-based",
	}
}

// save persists the current state via the callback.
func (s *SteeringSettings) save() tea.Cmd {
	if s.onConfigChange == nil {
		return nil
	}
	cfg := s.toConfig()
	return s.onConfigChange(s.scope, cfg)
}

// ToggleEnabled toggles the master steering switch.
func (s *SteeringSettings) ToggleEnabled() tea.Cmd {
	s.enabled = !s.enabled
	return s.save()
}

// ToggleRuntimeEnabled toggles runtime steering (tool-call interception).
func (s *SteeringSettings) ToggleRuntimeEnabled() tea.Cmd {
	s.runtimeEnabled = !s.runtimeEnabled
	return s.save()
}

// ToggleWorkflowEnabled toggles workflow steering (mode-based rules).
func (s *SteeringSettings) ToggleWorkflowEnabled() tea.Cmd {
	s.workflowEnabled = !s.workflowEnabled
	return s.save()
}

// ToggleShowDecisions toggles showing decisions in chat.
func (s *SteeringSettings) ToggleShowDecisions() tea.Cmd {
	s.showDecisions = !s.showDecisions
	return s.save()
}

// ToggleLogDecisions toggles logging decisions.
func (s *SteeringSettings) ToggleLogDecisions() tea.Cmd {
	s.logDecisions = !s.logDecisions
	return s.save()
}

// ToggleAutoApproveSimple toggles auto-approving simple operations.
func (s *SteeringSettings) ToggleAutoApproveSimple() tea.Cmd {
	s.autoApproveSimple = !s.autoApproveSimple
	return s.save()
}

// ToggleEnableEfficiency toggles efficiency analysis.
func (s *SteeringSettings) ToggleEnableEfficiency() tea.Cmd {
	s.enableEfficiency = !s.enableEfficiency
	return s.save()
}

// AdjustMaxRetries changes max retries by delta, clamped to [0, 10].
func (s *SteeringSettings) AdjustMaxRetries(delta int) tea.Cmd {
	s.maxRetries += delta
	if s.maxRetries < 0 {
		s.maxRetries = 0
	}
	if s.maxRetries > 10 {
		s.maxRetries = 10
	}
	return s.save()
}

// AdjustMaxFileReads changes max file reads by delta, clamped to [0, 50].
func (s *SteeringSettings) AdjustMaxFileReads(delta int) tea.Cmd {
	s.maxFileReads += delta
	if s.maxFileReads < 0 {
		s.maxFileReads = 0
	}
	if s.maxFileReads > 50 {
		s.maxFileReads = 50
	}
	return s.save()
}

// HandleKey processes keyboard input for steering settings.
// Returns a tea.Cmd if the key was consumed, nil otherwise.
//
// Item layout:
//
//	0 = Master Enable toggle
//	1 = Runtime Steering toggle
//	2 = Workflow Steering toggle
//	3 = Show Decisions in Chat toggle
//	4 = Log Decisions toggle
//	5 = Auto-approve Simple toggle
//	6 = Efficiency Analysis toggle
//	7 = Max Retries slider (if runtime enabled)
//	8 = Max File Reads slider (if efficiency enabled)
func (s *SteeringSettings) HandleKey(key string, state *State) tea.Cmd {
	if state.Focus != FocusContent {
		return nil
	}

	totalItems := 7
	if s.runtimeEnabled {
		totalItems = 8 // Include Max Retries
	}
	if s.enableEfficiency {
		totalItems = 9 // Also include Max File Reads
	}

	switch key {
	case "up", "k":
		if state.SelectedItem > 0 {
			state.SelectedItem--
			return nil
		}
	case "down", "j":
		if state.SelectedItem < totalItems-1 {
			state.SelectedItem++
			return nil
		}
	case " ", "space", "enter":
		switch state.SelectedItem {
		case 0:
			return s.ToggleEnabled()
		case 1:
			return s.ToggleRuntimeEnabled()
		case 2:
			return s.ToggleWorkflowEnabled()
		case 3:
			return s.ToggleShowDecisions()
		case 4:
			return s.ToggleLogDecisions()
		case 5:
			return s.ToggleAutoApproveSimple()
		case 6:
			return s.ToggleEnableEfficiency()
		case 7:
			if s.runtimeEnabled {
				return s.AdjustMaxRetries(0) // Just save current value
			}
		case 8:
			if s.enableEfficiency {
				return s.AdjustMaxFileReads(0) // Just save current value
			}
		}
	case "left", "h":
		switch state.SelectedItem {
		case 0:
			return s.ToggleEnabled()
		case 1:
			return s.ToggleRuntimeEnabled()
		case 2:
			return s.ToggleWorkflowEnabled()
		case 3:
			return s.ToggleShowDecisions()
		case 4:
			return s.ToggleLogDecisions()
		case 5:
			return s.ToggleAutoApproveSimple()
		case 6:
			return s.ToggleEnableEfficiency()
		case 7:
			if s.runtimeEnabled {
				return s.AdjustMaxRetries(-1)
			}
		case 8:
			if s.enableEfficiency && s.runtimeEnabled {
				return s.AdjustMaxFileReads(-1)
			} else if s.enableEfficiency && !s.runtimeEnabled {
				// This is item 7 when runtime is disabled
				return s.AdjustMaxFileReads(-1)
			}
		}
	case "right", "l":
		switch state.SelectedItem {
		case 0:
			return s.ToggleEnabled()
		case 1:
			return s.ToggleRuntimeEnabled()
		case 2:
			return s.ToggleWorkflowEnabled()
		case 3:
			return s.ToggleShowDecisions()
		case 4:
			return s.ToggleLogDecisions()
		case 5:
			return s.ToggleAutoApproveSimple()
		case 6:
			return s.ToggleEnableEfficiency()
		case 7:
			if s.runtimeEnabled {
				return s.AdjustMaxRetries(1)
			}
		case 8:
			if s.enableEfficiency && s.runtimeEnabled {
				return s.AdjustMaxFileReads(1)
			} else if s.enableEfficiency && !s.runtimeEnabled {
				return s.AdjustMaxFileReads(1)
			}
		}
	}

	return nil
}

// Render renders the steering settings UI.
func (s *SteeringSettings) Render(width, height int, state *State, th Theme) string {
	if width < 40 || height < 10 {
		return i18n.T("settings.integrations.steering.terminal_too_small")
	}

	var content strings.Builder

	// Header
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

	content.WriteString(headerStyle.Render("Steering Configuration"))
	content.WriteString("\n")
	content.WriteString(dimStyle.Render("Tool-call interception and meta-cognitive control layer."))
	content.WriteString("\n\n")

	// Item 0: Master Enable toggle
	isSelected0 := state != nil && state.SelectedItem == 0 && state.Focus == FocusContent
	content.WriteString(s.renderToggleItem(
		"Enable Steering",
		"Master switch for all steering functionality",
		s.enabled,
		isSelected0,
		pad, detailPad, innerWidth, th,
	))
	content.WriteString("\n\n")

	// Runtime Steering section header
	content.WriteString(dimStyle.Render("--- Runtime Steering (Tool Interception) ---"))
	content.WriteString("\n")

	// Item 1: Runtime Enabled toggle
	isSelected1 := state != nil && state.SelectedItem == 1 && state.Focus == FocusContent
	content.WriteString(s.renderToggleItem(
		"Runtime Steering",
		"Intercept and evaluate tool calls before execution",
		s.runtimeEnabled,
		isSelected1,
		pad, detailPad, innerWidth, th,
	))
	content.WriteString("\n")

	// Item 5: Auto-approve Simple
	isSelected5 := state != nil && state.SelectedItem == 5 && state.Focus == FocusContent
	content.WriteString(s.renderToggleItem(
		"Auto-approve Simple Operations",
		"Automatically approve read-only tools like file reads",
		s.autoApproveSimple,
		isSelected5,
		pad, detailPad, innerWidth, th,
	))
	content.WriteString("\n")

	// Item 6: Efficiency Analysis
	isSelected6 := state != nil && state.SelectedItem == 6 && state.Focus == FocusContent
	content.WriteString(s.renderToggleItem(
		"Efficiency Analysis",
		"Track and limit excessive file reads",
		s.enableEfficiency,
		isSelected6,
		pad, detailPad, innerWidth, th,
	))
	content.WriteString("\n")

	// Conditionally show sliders
	itemIdx := 7

	if s.runtimeEnabled {
		// Item 7: Max Retries slider
		isSelected := state != nil && state.SelectedItem == itemIdx && state.Focus == FocusContent
		content.WriteString(s.renderSliderItem(
			"Max Retries",
			"Maximum retry attempts for blocked/retry decisions",
			fmt.Sprintf("%d", s.maxRetries),
			float64(s.maxRetries), 0, 10,
			isSelected,
			pad, detailPad, innerWidth, th,
		))
		content.WriteString("\n")
		itemIdx++
	}

	if s.enableEfficiency {
		// Max File Reads slider
		isSelected := state != nil && state.SelectedItem == itemIdx && state.Focus == FocusContent
		content.WriteString(s.renderSliderItem(
			"Max File Reads",
			"Maximum file reads before efficiency warning",
			fmt.Sprintf("%d", s.maxFileReads),
			float64(s.maxFileReads), 0, 50,
			isSelected,
			pad, detailPad, innerWidth, th,
		))
		content.WriteString("\n")
	}

	content.WriteString("\n")

	// Workflow Steering section header
	content.WriteString(dimStyle.Render("--- Workflow Steering (Mode-Based Rules) ---"))
	content.WriteString("\n")

	// Item 2: Workflow Enabled toggle
	isSelected2 := state != nil && state.SelectedItem == 2 && state.Focus == FocusContent
	content.WriteString(s.renderToggleItem(
		"Workflow Steering",
		"Apply rules-based steering during workflow execution",
		s.workflowEnabled,
		isSelected2,
		pad, detailPad, innerWidth, th,
	))
	content.WriteString("\n\n")

	// Display Settings section header
	content.WriteString(dimStyle.Render("--- Display Settings ---"))
	content.WriteString("\n")

	// Item 3: Show Decisions in Chat
	isSelected3 := state != nil && state.SelectedItem == 3 && state.Focus == FocusContent
	content.WriteString(s.renderToggleItem(
		"Show Decisions in Chat",
		"Display steering decisions in the conversation",
		s.showDecisions,
		isSelected3,
		pad, detailPad, innerWidth, th,
	))
	content.WriteString("\n")

	// Item 4: Log Decisions
	isSelected4 := state != nil && state.SelectedItem == 4 && state.Focus == FocusContent
	content.WriteString(s.renderToggleItem(
		"Log Decisions",
		"Record steering decisions to the log file",
		s.logDecisions,
		isSelected4,
		pad, detailPad, innerWidth, th,
	))
	content.WriteString("\n\n")

	// Hint bar
	content.WriteString(s.renderHintBar(innerWidth, th))

	return i18n.SettingsIntegrationsText(content.String())
}

// renderToggleItem renders a labelled boolean toggle row with selection highlight.
func (s *SteeringSettings) renderToggleItem(label, description string, value, isSelected bool, pad, detailPad, width int, th Theme) string {
	checkmark := "[ ]"
	toggleStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(th.Text)).
		Padding(0, pad)

	if value {
		checkmark = "[x]"
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

// renderSliderItem renders a labelled slider row with a progress bar.
func (s *SteeringSettings) renderSliderItem(label, description, valueLabel string, value, min, max float64, isSelected bool, pad, detailPad, width int, th Theme) string {
	barWidth := minInt(20, maxInt(8, width-28))
	fraction := (value - min) / (max - min)
	if fraction < 0 {
		fraction = 0
	}
	if fraction > 1 {
		fraction = 1
	}
	filled := int(float64(barWidth) * fraction)

	var bar strings.Builder
	for i := range barWidth {
		if i < filled {
			bar.WriteString("█")
		} else {
			bar.WriteString("░")
		}
	}

	sliderStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(th.Text)).
		Background(lipgloss.Color(th.BGLight)).
		Padding(0, pad)
	if isSelected {
		sliderStyle = sliderStyle.
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color(th.Primary))
	}

	sectionLabel := lipgloss.NewStyle().
		Foreground(lipgloss.Color(th.Primary)).
		Bold(isSelected).
		Padding(0, pad).
		Render(label)

	sliderRow := sliderStyle.Render(fmt.Sprintf("[%s] %s", bar.String(), valueLabel))

	lines := []string{sectionLabel, sliderRow}
	if width >= 50 {
		lines = append([]string{lines[0]}, lipgloss.NewStyle().
			Foreground(lipgloss.Color(th.TextMuted)).
			Padding(0, detailPad).
			Render(description))
		lines = append(lines, sliderRow)
	}

	return lipgloss.JoinVertical(lipgloss.Left, lines...)
}

// renderHintBar renders keyboard navigation hints at the bottom.
func (s *SteeringSettings) renderHintBar(width int, th Theme) string {
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

	var hints string
	if width < 65 {
		hints = fmt.Sprintf("%s nav %s toggle %s adj",
			keyStyle.Render("up/down"),
			keyStyle.Render("Space"),
			keyStyle.Render("left/right"))
	} else {
		hints = fmt.Sprintf("%s navigate - %s toggle - %s / %s adjust",
			keyStyle.Render("up/down"),
			keyStyle.Render("Space"),
			keyStyle.Render("left/right"),
			keyStyle.Render("H/L"))
	}

	return hintStyle.Render(hints)
}

// Getters for external access

func (s *SteeringSettings) IsEnabled() bool         { return s.enabled }
func (s *SteeringSettings) IsRuntimeEnabled() bool  { return s.runtimeEnabled }
func (s *SteeringSettings) IsWorkflowEnabled() bool { return s.workflowEnabled }
