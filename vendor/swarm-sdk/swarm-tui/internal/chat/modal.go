package chat

import (
	"charm.land/lipgloss/v2"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/i18n"
)

// ModalType represents different modal dialogs
type ModalType int

const (
	ModalNone ModalType = iota
	ModalExitChat
	ModalStopAgent
	ModalNewChat          // New chat input modal
	ModalGitCheckout      // Branch checkout confirmation modal
	ModalGitInit          // Git init prompt modal
	ModalEscapeMenu       // Escape menu with edit/leave options
	ModalQuitConfirm      // Press-again-to-quit confirmation
	ModalTelemetryConsent // Telemetry opt-in consent dialog
)

// Modal represents a dialog overlay
type Modal struct {
	Type     ModalType
	Title    string
	Message  string
	Options  []string
	Selected int
	OnSelect func(option int)
}

// NewTelemetryConsentModal creates the one-time consent dialog shown when a
// user enables telemetry from settings. onSelect receives 1 for Accept, 0 for
// Decline (esc also declines). Telemetry is opt-in and off by default; even
// after accepting, the user must supply their own collector URL + token via
// SWARM_ANALYTICS_COLLECTOR_URL / SWARM_ANALYTICS_AUTH_TOKEN (no endpoint or
// credential ships in the open-source build).
func NewTelemetryConsentModal(onSelect func(int)) *Modal {
	return &Modal{
		Type:     ModalTelemetryConsent,
		Title:    i18n.T("classic_chat_2.modal.telemetry.title"),
		Message:  i18n.T("classic_chat_2.modal.telemetry.message"),
		Options:  []string{i18n.T("classic_chat_2.modal.decline"), i18n.T("classic_chat_2.modal.accept")},
		Selected: 0, // default to Decline for safety
		OnSelect: onSelect,
	}
}

// NewExitChatModal creates a modal for confirming chat exit
func NewExitChatModal(isActive bool, onSelect func(int)) *Modal {
	message := i18n.T("classic_chat_2.modal.exit.message")
	options := []string{i18n.T("classic_chat_2.modal.cancel"), i18n.T("classic_chat_2.modal.leave")}

	if isActive {
		message = i18n.T("classic_chat_2.modal.exit.active_message")
		options = []string{
			i18n.T("classic_chat_2.modal.cancel"),
			i18n.T("classic_chat_2.modal.leave_stop"),
			i18n.T("classic_chat_2.modal.leave_background"),
		}
	}

	return &Modal{
		Type:     ModalExitChat,
		Title:    i18n.T("classic_chat_2.modal.exit.title"),
		Message:  message,
		Options:  options,
		Selected: 0,
		OnSelect: onSelect,
	}
}

// NewStopAgentModal creates a modal for confirming agent stop
func NewStopAgentModal(onSelect func(int)) *Modal {
	return &Modal{
		Type:     ModalStopAgent,
		Title:    i18n.T("classic_chat_2.modal.stop.title"),
		Message:  i18n.T("classic_chat_2.modal.stop.message"),
		Options:  []string{i18n.T("classic_chat_2.modal.cancel"), i18n.T("classic_chat_2.modal.stop")},
		Selected: 1, // Default to Stop for quick confirmation
		OnSelect: onSelect,
	}
}

// NewQuitConfirmModal creates a modal for the two-press quit flow.
// First press shows this modal; second press (or selecting "Quit Now") exits.
func NewQuitConfirmModal(onSelect func(int)) *Modal {
	return &Modal{
		Type:     ModalQuitConfirm,
		Title:    i18n.T("classic_chat_2.modal.quit.title"),
		Message:  i18n.T("classic_chat_2.modal.quit.message"),
		Options:  []string{i18n.T("classic_chat_2.modal.cancel"), i18n.T("classic_chat_2.modal.quit_now")},
		Selected: 0,
		OnSelect: onSelect,
	}
}

// Update handles modal input
func (m *Modal) Update(key string) {
	switch key {
	case "h", "left":
		m.Selected = (m.Selected + len(m.Options) - 1) % len(m.Options)
	case "l", "right", "tab":
		m.Selected = (m.Selected + 1) % len(m.Options)
	case "enter", " ":
		if m.OnSelect != nil {
			m.OnSelect(m.Selected)
		}
	case "esc":
		// Cancel (first option)
		if m.OnSelect != nil {
			m.OnSelect(0)
		}
	}
}

// Render renders the modal above the input area (like slash commands)
func (m *Modal) Render(width, height int, theme Theme) string {
	// Modal dimensions - match slash command style
	modalWidth := 70
	if modalWidth > width-8 {
		modalWidth = width - 8
	}

	// Title
	titleStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(theme.Primary)).
		Bold(true)

	title := titleStyle.Render(m.Title)

	// Message
	messageStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(theme.Text))

	message := messageStyle.Render(m.Message)

	// Options (inline buttons with spacing)
	var buttons []string
	for i, opt := range m.Options {
		var btnStyle lipgloss.Style
		if i == m.Selected {
			btnStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color(theme.Primary)).
				Bold(true).
				Padding(0, 2)
		} else {
			btnStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color(theme.TextMuted)).
				Padding(0, 2)
		}

		// Add bracket indicator for selected
		btnText := opt
		if i == m.Selected {
			btnText = "› " + opt + " ‹"
		}

		buttons = append(buttons, btnStyle.Render(btnText))
	}

	buttonRow := lipgloss.JoinHorizontal(lipgloss.Left, buttons...)

	// Hints
	hintStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(theme.TextMuted)).
		Italic(true)

	hints := hintStyle.Render(i18n.T("classic_chat_2.modal.hint"))

	// Combine content (centered)
	var lines []string
	lines = append(lines, "")
	lines = append(lines, title)
	lines = append(lines, message)
	lines = append(lines, "")
	lines = append(lines, buttonRow)
	lines = append(lines, "")
	lines = append(lines, hints)

	content := lipgloss.JoinVertical(lipgloss.Center, lines...)

	// Modal box with border (no background)
	modalStyle := lipgloss.NewStyle().
		Width(modalWidth).
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color(theme.Primary)).
		Padding(1, 2).
		Align(lipgloss.Center)

	return modalStyle.Render(content)
}
