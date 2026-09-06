package settings

import (
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/i18n"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/ui/palette"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/update"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/version"
)

// UpdateSettingsMsg is sent when update settings change
type UpdateSettingsMsg struct {
	Mode     update.UpdateMode
	Channel  update.ReleaseChannel
	Interval time.Duration
}

// renderUpdates renders the Updates settings panel
func (m *Manager) renderUpdates() string {
	var b []string

	accent := lipgloss.Color(palette.Accent)
	dim := lipgloss.Color(palette.TextDim)
	success := lipgloss.Color(palette.Success)

	titleStyle := lipgloss.NewStyle().Foreground(accent).Bold(true)
	textStyle := lipgloss.NewStyle()
	dimStyle := lipgloss.NewStyle().Foreground(dim)
	successStyle := lipgloss.NewStyle().Foreground(success)

	// Header
	b = append(b, titleStyle.Render(i18n.T("settings.updates.title")))
	b = append(b, "")

	// Current version info
	lastCheck := m.state.UpdatesLastCheck
	if lastCheck == "" || lastCheck == "Never" {
		lastCheck = i18n.T("settings.updates.never")
	}
	b = append(b, dimStyle.Render(i18n.T("settings.updates.current_version"))+textStyle.Render(version.Version))
	b = append(b, dimStyle.Render(i18n.T("settings.updates.last_check"))+textStyle.Render(lastCheck))
	b = append(b, "")

	// Update available indicator
	if m.state.UpdatesAvailable {
		b = append(b, successStyle.Render(i18n.T("settings.updates.available", m.state.UpdatesLatestVersion)))
		b = append(b, "")
	} else if m.state.UpdatesLatestVersion != "" {
		b = append(b, dimStyle.Render(i18n.T("settings.updates.latest")))
		b = append(b, "")
	}

	// Mode selection
	b = append(b, textStyle.Render(i18n.T("settings.updates.mode")))
	modes := []string{
		i18n.T("settings.updates.mode.prompt"),
		i18n.T("settings.updates.mode.automatic"),
		i18n.T("settings.updates.mode.manual"),
		i18n.T("settings.updates.mode.disabled"),
	}
	for i, mode := range modes {
		prefix := "  "
		if i == m.state.UpdatesModeSelected {
			prefix = "▶ "
		}
		style := dimStyle
		if i == m.state.UpdatesModeSelected {
			style = textStyle
		}
		b = append(b, style.Render(prefix+mode))
	}
	b = append(b, "")

	// Channel selection
	b = append(b, textStyle.Render(i18n.T("settings.updates.channel")))
	channels := []string{
		i18n.T("settings.updates.channel.stable"),
		i18n.T("settings.updates.channel.beta"),
		i18n.T("settings.updates.channel.nightly"),
	}
	for i, ch := range channels {
		prefix := "  "
		if i == m.state.UpdatesChannelSelected {
			prefix = "▶ "
		}
		style := dimStyle
		if i == m.state.UpdatesChannelSelected {
			style = textStyle
		}
		b = append(b, style.Render(prefix+ch))
	}
	b = append(b, "")

	// Check interval selection
	b = append(b, textStyle.Render(i18n.T("settings.updates.interval")))
	intervals := []string{
		i18n.T("settings.updates.interval.hour"),
		i18n.T("settings.updates.interval.six_hours"),
		i18n.T("settings.updates.interval.day"),
		i18n.T("settings.updates.interval.manual"),
	}
	for i, interval := range intervals {
		prefix := "  "
		if i == m.state.UpdatesIntervalSelected {
			prefix = "▶ "
		}
		style := dimStyle
		if i == m.state.UpdatesIntervalSelected {
			style = textStyle
		}
		b = append(b, style.Render(prefix+interval))
	}
	b = append(b, "")

	// Check now button
	if m.state.UpdatesChecking {
		b = append(b, dimStyle.Render(i18n.T("settings.updates.checking")))
	} else {
		b = append(b, accentStyle().Render(i18n.T("settings.updates.check_now")))
	}
	b = append(b, "")

	// Help
	b = append(b, dimStyle.Render(i18n.T("settings.updates.help.navigation")))
	b = append(b, dimStyle.Render(i18n.T("settings.updates.help.sections")))

	return lipgloss.JoinVertical(lipgloss.Left, b...)
}

// handleUpdatesKey handles key events for the Updates settings panel
func (m *Manager) handleUpdatesKey(key string) tea.Cmd {
	switch key {
	case "up", "k":
		return m.updatesNavigateUp()

	case "down", "j":
		return m.updatesNavigateDown()

	case "enter", " ":
		return m.updatesSelect()

	case "tab":
		m.state.UpdatesModeSelected = 0
		m.state.UpdatesChannelSelected = 0
		m.state.UpdatesIntervalSelected = 0
		return nil

	default:
		return nil
	}
}

// updatesNavigateUp handles up navigation
func (m *Manager) updatesNavigateUp() tea.Cmd {
	// Simple navigation: cycle through mode, channel, interval sections
	// For simplicity, we'll just move the selected item up within the current section
	// A more sophisticated approach would track which section we're in
	if m.state.UpdatesModeSelected > 0 {
		m.state.UpdatesModeSelected--
	} else if m.state.UpdatesChannelSelected > 0 {
		m.state.UpdatesChannelSelected--
	} else if m.state.UpdatesIntervalSelected > 0 {
		m.state.UpdatesIntervalSelected--
	}
	return nil
}

// updatesNavigateDown handles down navigation
func (m *Manager) updatesNavigateDown() tea.Cmd {
	// Move selection down within sections
	if m.state.UpdatesModeSelected < 3 {
		m.state.UpdatesModeSelected++
	} else if m.state.UpdatesChannelSelected < 2 {
		m.state.UpdatesChannelSelected++
	} else if m.state.UpdatesIntervalSelected < 3 {
		m.state.UpdatesIntervalSelected++
	}
	return nil
}

// updatesSelect handles selection
func (m *Manager) updatesSelect() tea.Cmd {
	// Determine which section we're in based on current focus
	// For now, cycle through modes when Enter is pressed
	m.state.UpdatesModeSelected = (m.state.UpdatesModeSelected + 1) % 4

	// Update the actual settings
	var mode update.UpdateMode
	switch m.state.UpdatesModeSelected {
	case 0:
		mode = update.ModePrompt
	case 1:
		mode = update.ModeAutomatic
	case 2:
		mode = update.ModeManual
	case 3:
		mode = update.ModeDisabled
	}

	return func() tea.Msg {
		return UpdateSettingsMsg{
			Mode: mode,
		}
	}
}

// SetUpdateStatus sets the update status from the app
func (m *Manager) SetUpdateStatus(checkResult *update.CheckResult) {
	if checkResult == nil {
		m.state.UpdatesAvailable = false
		m.state.UpdatesLatestVersion = ""
		return
	}

	m.state.UpdatesAvailable = checkResult.UpdateAvailable
	m.state.UpdatesLatestVersion = checkResult.LatestVersion

	// Update last check time
	m.state.UpdatesLastCheck = time.Now().Format("2006-01-02 15:04")
}

// SetUpdateConfig sets the current update configuration
func (m *Manager) SetUpdateConfig(config *update.Config) {
	if config == nil {
		return
	}

	// Set mode
	switch config.Mode {
	case update.ModePrompt:
		m.state.UpdatesModeSelected = 0
	case update.ModeAutomatic:
		m.state.UpdatesModeSelected = 1
	case update.ModeManual:
		m.state.UpdatesModeSelected = 2
	case update.ModeDisabled:
		m.state.UpdatesModeSelected = 3
	}

	// Set channel
	switch config.Channel {
	case update.ChannelStable:
		m.state.UpdatesChannelSelected = 0
	case update.ChannelBeta:
		m.state.UpdatesChannelSelected = 1
	case update.ChannelNightly:
		m.state.UpdatesChannelSelected = 2
	}

	// Set interval
	switch config.CheckInterval {
	case time.Hour:
		m.state.UpdatesIntervalSelected = 0
	case 6 * time.Hour:
		m.state.UpdatesIntervalSelected = 1
	case 24 * time.Hour:
		m.state.UpdatesIntervalSelected = 2
	default:
		m.state.UpdatesIntervalSelected = 3
	}

	// Set last check
	if !config.LastCheck.IsZero() {
		m.state.UpdatesLastCheck = config.LastCheck.Format("2006-01-02 15:04")
	}

	// Set current version
	m.state.UpdatesCurrentVersion = version.Version
}

// accentStyle returns an accent-colored style
func accentStyle() lipgloss.Style {
	return lipgloss.NewStyle().Foreground(lipgloss.Color(palette.Accent))
}
