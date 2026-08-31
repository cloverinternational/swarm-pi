// Package settings provides modular settings components for the chat TUI.
package settings

import (
	"fmt"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/configbundle"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/i18n"
)

// ConfigSourceState defines the state machine constants.
type ConfigSourceState string

const (
	// CSStateList displays the list of config sources (global/project)
	CSStateList ConfigSourceState = "list"
	// CSStateCreateConfirm shows confirmation for creating new project config
	CSStateCreateConfirm ConfigSourceState = "create_confirm"
	// CSStateSwitchConfirm shows confirmation for switching config source
	CSStateSwitchConfirm ConfigSourceState = "switch_confirm"
)

// ConfigSourceSettings handles config source (global/project) switching UI.
// This is a minimal view - just shows current source and allows switching.
// Actual config editing happens in the normal settings panels (General, Reliability, etc.).
type ConfigSourceSettings struct {
	// --- State Machine ---
	state ConfigSourceState

	// --- Config Data ---
	configBundle configbundle.ConfigSourceProvider

	// --- State ---
	isUsingProject bool
	projectName    string
	projectPath    string
	hasProject     bool
	globalPath     string

	// --- Navigation ---
	selectedIndex int // 0 = global, 1 = project

	// --- UI State ---
	width  int
	height int

	// --- Callbacks ---
	onToggleConfig  func() string
	onCreateProject func() error
}

// NewConfigSourceSettings creates a new config source settings handler.
func NewConfigSourceSettings(provider configbundle.ConfigSourceProvider) *ConfigSourceSettings {
	s := &ConfigSourceSettings{
		state:         CSStateList,
		configBundle:  provider,
		selectedIndex: 0,
	}

	// Initialize state
	if provider != nil {
		s.isUsingProject = provider.IsUsingProject()
		s.hasProject = provider.HasProjectConfig()
		s.projectName = provider.ProjectName()
		s.projectPath = provider.ProjectPath()
		s.globalPath = provider.GlobalPath()
		// Set initial selection based on current source
		if s.isUsingProject {
			s.selectedIndex = 1
		}
	}

	return s
}

// SetOnToggleConfig sets the callback for toggling config source.
func (s *ConfigSourceSettings) SetOnToggleConfig(cb func() string) {
	s.onToggleConfig = cb
}

// SetOnCreateProject sets the callback for creating project config.
func (s *ConfigSourceSettings) SetOnCreateProject(cb func() error) {
	s.onCreateProject = cb
}

// SetDimensions sets the width and height for rendering.
func (s *ConfigSourceSettings) SetDimensions(width, height int) {
	s.width = width
	s.height = height
}

// Refresh refreshes the state from the config bundle.
func (s *ConfigSourceSettings) Refresh() {
	if s.configBundle != nil {
		s.isUsingProject = s.configBundle.IsUsingProject()
		s.hasProject = s.configBundle.HasProjectConfig()
		s.projectName = s.configBundle.ProjectName()
		s.projectPath = s.configBundle.ProjectPath()
		s.globalPath = s.configBundle.GlobalPath()
	}
}

// State returns the current state.
func (s *ConfigSourceSettings) State() ConfigSourceState {
	return s.state
}

// HandleKey handles key input for config source settings.
func (s *ConfigSourceSettings) HandleKey(key string, sharedState *State) tea.Cmd {
	switch s.state {
	case CSStateList:
		return s.handleListKey(key, sharedState)
	case CSStateCreateConfirm:
		return s.handleCreateConfirmKey(key, sharedState)
	case CSStateSwitchConfirm:
		return s.handleSwitchConfirmKey(key, sharedState)
	}
	return nil
}

func (s *ConfigSourceSettings) handleListKey(key string, sharedState *State) tea.Cmd {
	switch key {
	case "up", "k":
		if s.selectedIndex > 0 {
			s.selectedIndex--
		}
	case "down", "j":
		if s.hasProject && s.selectedIndex < 1 {
			s.selectedIndex++
		}
	case "s", "S":
		if s.hasProject {
			s.state = CSStateSwitchConfirm
		}
	case "c", "C":
		if !s.hasProject {
			s.state = CSStateCreateConfirm
		}
	case "e", "E":
		// Open current config in external editor
		if s.configBundle != nil {
			_ = s.configBundle.OpenInEditor()
			s.Refresh()
		}
	case "r", "R":
		// Refresh config from disk
		s.Refresh()
		// NOTE: esc is NOT handled here - it propagates to manager to return to sidebar
	}
	return nil
}

func (s *ConfigSourceSettings) handleCreateConfirmKey(key string, sharedState *State) tea.Cmd {
	switch key {
	case "enter":
		if s.onCreateProject != nil {
			if err := s.onCreateProject(); err == nil {
				s.hasProject = true
				s.isUsingProject = true
				s.selectedIndex = 1
				s.state = CSStateList
			}
		}
	case "esc":
		s.state = CSStateList
	}
	return nil
}

func (s *ConfigSourceSettings) handleSwitchConfirmKey(key string, sharedState *State) tea.Cmd {
	switch key {
	case "enter":
		if s.onToggleConfig != nil {
			s.onToggleConfig()
			s.isUsingProject = !s.isUsingProject
		}
		s.state = CSStateList
	case "esc":
		s.state = CSStateList
	}
	return nil
}

// View renders the config source settings.
func (s *ConfigSourceSettings) View(width int, theme Theme) string {
	switch s.state {
	case CSStateList:
		return s.renderListView(width, theme)
	case CSStateCreateConfirm:
		return s.renderCreateConfirm(width, theme)
	case CSStateSwitchConfirm:
		return s.renderSwitchConfirm(width, theme)
	}
	return ""
}

// renderListView renders the main list view.
func (s *ConfigSourceSettings) renderListView(width int, theme Theme) string {
	iw := max(20, width-4)
	pad := 2
	if width < 60 {
		pad = 1
	}

	var lines []string

	// Title
	titleStyle := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color(theme.Primary)).
		Width(iw).Align(lipgloss.Center)
	lines = append(lines, titleStyle.Render(i18n.T("settings.config_source.title")))
	lines = append(lines, "")

	// Status badge
	if s.isUsingProject {
		badge := lipgloss.NewStyle().
			Background(lipgloss.Color(theme.Success)).Foreground(lipgloss.Color(theme.BG)).
			Padding(0, 1).Bold(true).Render("✓ " + s.projectName)
		lines = append(lines, lipgloss.NewStyle().Width(iw).Align(lipgloss.Center).Render(badge))
	} else {
		badge := lipgloss.NewStyle().
			Background(lipgloss.Color(theme.Success)).Foreground(lipgloss.Color(theme.BG)).
			Padding(0, 1).Bold(true).Render("✓ " + i18n.T("settings.config_source.global"))
		lines = append(lines, lipgloss.NewStyle().Width(iw).Align(lipgloss.Center).Render(badge))
	}
	lines = append(lines, "")

	// Global config card
	lines = append(lines, s.renderConfigCard("global", s.selectedIndex == 0, iw, theme))
	lines = append(lines, "")

	// Project config card (if exists)
	if s.hasProject {
		lines = append(lines, s.renderConfigCard("project", s.selectedIndex == 1, iw, theme))
		lines = append(lines, "")
	} else {
		// No project config hint
		noConfigStyle := lipgloss.NewStyle().
			Foreground(lipgloss.Color(theme.TextMuted)).
			Italic(true).
			Width(iw).Align(lipgloss.Center)
		lines = append(lines, noConfigStyle.Render(i18n.T("settings.config_source.no_project")))
		lines = append(lines, "")
	}

	// Hint bar
	lines = append(lines, s.renderListHints(width, theme))

	content := lipgloss.JoinVertical(lipgloss.Left, lines...)
	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color(theme.Border)).
		Width(max(20, width-2)).Padding(0, pad).
		Render(content)
}

// renderConfigCard renders a config source card.
func (s *ConfigSourceSettings) renderConfigCard(source string, isSel bool, width int, theme Theme) string {
	cardWidth := max(20, width-2)
	var cardStyle lipgloss.Style
	if isSel {
		cardStyle = lipgloss.NewStyle().
			Background(lipgloss.Color(theme.BGLighter)).
			Border(lipgloss.RoundedBorder()).BorderForeground(lipgloss.Color(theme.Primary)).
			Width(cardWidth).Padding(0, 1)
	} else {
		cardStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).BorderForeground(lipgloss.Color(theme.Border)).
			Width(cardWidth).Padding(0, 1)
	}

	nameStyle := lipgloss.NewStyle().Bold(true)
	if isSel {
		nameStyle = nameStyle.Foreground(lipgloss.Color(theme.Primary))
	} else {
		nameStyle = nameStyle.Foreground(lipgloss.Color(theme.Text))
	}

	var icon, name, desc string
	var isActive bool

	if source == "global" {
		icon = "🌍"
		name = i18n.T("settings.config_source.global")
		desc = i18n.T("settings.config_source.global.description")
		isActive = !s.isUsingProject
	} else {
		icon = "📦"
		name = i18n.T("settings.config_source.project.name", s.projectName)
		desc = i18n.T("settings.config_source.project.description")
		isActive = s.isUsingProject
	}

	// Active indicator
	activeMark := ""
	if isActive {
		activeMark = lipgloss.NewStyle().Foreground(lipgloss.Color(theme.Success)).Bold(true).Render(i18n.T("settings.config_source.active"))
	}

	// Name line
	nameLine := nameStyle.Render(icon+" "+name) + activeMark

	// Description
	descStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(theme.TextMuted)).Italic(true)
	descLine := descStyle.Render(desc)

	inner := lipgloss.JoinVertical(lipgloss.Left, nameLine, descLine)

	return cardStyle.Render(inner)
}

// renderListHints renders the keyboard hints for the list view.
func (s *ConfigSourceSettings) renderListHints(width int, theme Theme) string {
	k := func(key string) string {
		return lipgloss.NewStyle().Background(lipgloss.Color(theme.BGLight)).
			Foreground(lipgloss.Color(theme.Text)).Padding(0, 1).Bold(true).Render(key)
	}

	var hints string
	if width < 50 {
		if s.hasProject {
			hints = i18n.T("settings.config_source.hints.switch.compact", k("s"), k("e"), k("esc"))
		} else {
			hints = i18n.T("settings.config_source.hints.create.compact", k("c"), k("e"), k("esc"))
		}
	} else if width < 80 {
		if s.hasProject {
			hints = i18n.T("settings.config_source.hints.switch.medium",
				k("↑"), k("↓"), k("s"), k("e"), k("esc"))
		} else {
			hints = i18n.T("settings.config_source.hints.create.medium",
				k("↑"), k("↓"), k("c"), k("e"), k("esc"))
		}
	} else {
		if s.hasProject {
			hints = i18n.T("settings.config_source.hints.switch.full",
				k("↑"), k("↓"), k("s"), k("e"), k("esc"))
		} else {
			hints = i18n.T("settings.config_source.hints.create.full",
				k("↑"), k("↓"), k("c"), k("e"), k("esc"))
		}
	}

	return lipgloss.NewStyle().Foreground(lipgloss.Color(theme.TextMuted)).
		Width(width - 4).Align(lipgloss.Center).Render(hints)
}

// renderCreateConfirm renders the create project config confirmation.
func (s *ConfigSourceSettings) renderCreateConfirm(width int, theme Theme) string {
	iw := max(20, width-4)

	var lines []string

	// Title
	titleStyle := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color(theme.Primary)).
		Width(iw).Align(lipgloss.Center)
	lines = append(lines, titleStyle.Render(i18n.T("settings.config_source.create.title")))
	lines = append(lines, "")

	// Description
	descStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(theme.Text))
	lines = append(lines, descStyle.Render(i18n.T("settings.config_source.create.missing")))
	lines = append(lines, "")
	lines = append(lines, descStyle.Render(i18n.T("settings.config_source.create.benefits")))

	bulletStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(theme.TextMuted))
	lines = append(lines, bulletStyle.Render(i18n.T("settings.config_source.create.override")))
	lines = append(lines, bulletStyle.Render(i18n.T("settings.config_source.create.tools")))
	lines = append(lines, "")

	// Location
	pathStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(theme.TextMuted)).Italic(true)
	lines = append(lines, pathStyle.Render(i18n.T("settings.config_source.create.location")))
	lines = append(lines, "")

	// Buttons
	k := func(key string) string {
		return lipgloss.NewStyle().Background(lipgloss.Color(theme.BGLight)).
			Foreground(lipgloss.Color(theme.Text)).Padding(0, 1).Bold(true).Render(key)
	}

	createBtn := lipgloss.NewStyle().
		Background(lipgloss.Color(theme.Success)).Foreground(lipgloss.Color(theme.BG)).
		Padding(0, 2).Bold(true).Render(i18n.T("settings.config_source.create.button"))
	cancelBtn := lipgloss.NewStyle().
		Background(lipgloss.Color(theme.BGLight)).Foreground(lipgloss.Color(theme.Text)).
		Padding(0, 2).Render(i18n.T("settings.config_source.cancel.button"))

	buttons := fmt.Sprintf("  %s %s  %s %s", k("enter"), createBtn, k("esc"), cancelBtn)
	lines = append(lines, lipgloss.NewStyle().Width(iw).Align(lipgloss.Center).Render(buttons))

	content := lipgloss.JoinVertical(lipgloss.Left, lines...)
	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color(theme.Border)).
		Width(max(20, width-2)).Padding(0, 2).
		Render(content)
}

// renderSwitchConfirm renders the switch config confirmation.
func (s *ConfigSourceSettings) renderSwitchConfirm(width int, theme Theme) string {
	iw := max(20, width-4)

	var lines []string

	// Title
	titleStyle := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color(theme.Primary)).
		Width(iw).Align(lipgloss.Center)
	lines = append(lines, titleStyle.Render(i18n.T("settings.config_source.switch.title")))
	lines = append(lines, "")

	// Question
	questionStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(theme.Text)).Bold(true)
	if s.isUsingProject {
		lines = append(lines, questionStyle.Render(i18n.T("settings.config_source.switch.to_global")))
	} else {
		lines = append(lines, questionStyle.Render(i18n.T("settings.config_source.switch.to_project", s.projectName)))
	}
	lines = append(lines, "")

	// Comparison hint
	hintStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(theme.TextMuted)).Italic(true)
	if s.isUsingProject {
		lines = append(lines, hintStyle.Render(i18n.T("settings.config_source.switch.global_hint")))
	} else {
		lines = append(lines, hintStyle.Render(i18n.T("settings.config_source.switch.project_hint")))
	}
	lines = append(lines, "")

	// Buttons
	k := func(key string) string {
		return lipgloss.NewStyle().Background(lipgloss.Color(theme.BGLight)).
			Foreground(lipgloss.Color(theme.Text)).Padding(0, 1).Bold(true).Render(key)
	}

	switchBtn := lipgloss.NewStyle().
		Background(lipgloss.Color(theme.Primary)).Foreground(lipgloss.Color(theme.BG)).
		Padding(0, 2).Bold(true).Render(i18n.T("settings.config_source.switch.button"))
	cancelBtn := lipgloss.NewStyle().
		Background(lipgloss.Color(theme.BGLight)).Foreground(lipgloss.Color(theme.Text)).
		Padding(0, 2).Render(i18n.T("settings.config_source.cancel.button"))

	buttons := fmt.Sprintf("  %s %s  %s %s", k("enter"), switchBtn, k("esc"), cancelBtn)
	lines = append(lines, lipgloss.NewStyle().Width(iw).Align(lipgloss.Center).Render(buttons))

	content := lipgloss.JoinVertical(lipgloss.Left, lines...)
	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color(theme.Border)).
		Width(max(20, width-2)).Padding(0, 2).
		Render(content)
}

// IsUsingProject returns whether project config is active.
func (s *ConfigSourceSettings) IsUsingProject() bool {
	return s.isUsingProject
}

// HasProject returns whether a project config exists.
func (s *ConfigSourceSettings) HasProject() bool {
	return s.hasProject
}

// ProjectName returns the project config name.
func (s *ConfigSourceSettings) ProjectName() string {
	return s.projectName
}

// SourceDisplayName returns a human-readable source name.
func (s *ConfigSourceSettings) SourceDisplayName() string {
	if s.isUsingProject {
		if s.projectName != "" {
			return "📦 " + s.projectName
		}
		return "📦 " + i18n.T("settings.config_source.project")
	}
	return "🌍 " + i18n.T("settings.config_source.global")
}
