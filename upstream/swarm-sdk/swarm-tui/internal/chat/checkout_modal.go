package chat

import (
	"charm.land/lipgloss/v2"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/i18n"
)

// CheckoutOption represents the user's choice in the checkout modal
type CheckoutOption int

const (
	CheckoutYes CheckoutOption = iota
	CheckoutStayOnCurrent
	CheckoutCancel
)

// CheckoutModal handles branch checkout confirmations
type CheckoutModal struct {
	currentBranch     string
	targetBranch      string
	conversationTitle string
	hasUncommitted    bool
	selected          int
	onSelect          func(CheckoutOption)
}

// NewCheckoutModal creates a checkout confirmation modal
func NewCheckoutModal(currentBranch, targetBranch, conversationTitle string, hasUncommitted bool, onSelect func(CheckoutOption)) *CheckoutModal {
	return &CheckoutModal{
		currentBranch:     currentBranch,
		targetBranch:      targetBranch,
		conversationTitle: conversationTitle,
		hasUncommitted:    hasUncommitted,
		selected:          0, // Default to "Stay on current"
		onSelect:          onSelect,
	}
}

// Update handles input for the modal
func (m *CheckoutModal) Update(key string) {
	switch key {
	case "j", "down", "right", "tab":
		m.selected = (m.selected + 1) % 3
	case "k", "up", "left", "shift+tab":
		m.selected = (m.selected + 2) % 3
	case "y", "Y":
		if m.onSelect != nil {
			m.onSelect(CheckoutYes)
		}
	case "n", "N":
		if m.onSelect != nil {
			m.onSelect(CheckoutStayOnCurrent)
		}
	case "enter", " ":
		if m.onSelect != nil {
			m.onSelect(CheckoutOption(m.selected))
		}
	case "esc":
		if m.onSelect != nil {
			m.onSelect(CheckoutCancel)
		}
	}
}

// Render renders the checkout modal
func (m *CheckoutModal) Render(width, height int, theme Theme) string {
	modalWidth := 60
	if modalWidth > width-8 {
		modalWidth = width - 8
	}

	// Title
	titleStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(theme.Warning)).
		Bold(true)
	title := titleStyle.Render(i18n.T("classic_chat.checkout.title"))

	// Conversation info
	convStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(theme.Text))
	convTitle := m.conversationTitle
	if len(convTitle) > 40 {
		convTitle = convTitle[:37] + "..."
	}
	convInfo := convStyle.Render(i18n.T("classic_chat.checkout.resume", convTitle))

	// Branch info
	branchStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(theme.TextMuted))
	currentInfo := branchStyle.Render(i18n.T("classic_chat.checkout.current_branch", m.currentBranch))
	targetStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(theme.Info))
	targetInfo := targetStyle.Render(i18n.T("classic_chat.checkout.conversation_branch", m.targetBranch))

	// Warning if uncommitted changes
	var warningLine string
	if m.hasUncommitted {
		warningStyle := lipgloss.NewStyle().
			Foreground(lipgloss.Color(theme.Error)).
			Bold(true)
		warningLine = warningStyle.Render(i18n.T("classic_chat.checkout.dirty_warning"))
	}

	// Options
	options := []string{
		i18n.T("classic_chat.checkout.checkout"),
		i18n.T("classic_chat.checkout.stay"),
		i18n.T("classic_chat.common.cancel"),
	}
	var buttons []string
	for i, opt := range options {
		btnStyle := lipgloss.NewStyle().
			Foreground(lipgloss.Color(theme.TextMuted)).
			Padding(0, 1)

		if i == m.selected {
			btnStyle = btnStyle.
				Foreground(lipgloss.Color(theme.Primary)).
				Bold(true)
			opt = "› " + opt + " ‹"
		}
		buttons = append(buttons, btnStyle.Render(opt))
	}
	buttonRow := lipgloss.JoinHorizontal(lipgloss.Left, buttons...)

	// Hints
	hintStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(theme.TextMuted)).
		Italic(true)
	hints := hintStyle.Render(i18n.T("classic_chat.checkout.hint"))

	// Build content
	var lines []string
	lines = append(lines, "")
	lines = append(lines, title)
	lines = append(lines, "")
	lines = append(lines, convInfo)
	lines = append(lines, "")
	lines = append(lines, currentInfo)
	lines = append(lines, targetInfo)
	if warningLine != "" {
		lines = append(lines, "")
		lines = append(lines, warningLine)
	}
	lines = append(lines, "")
	lines = append(lines, buttonRow)
	lines = append(lines, "")
	lines = append(lines, hints)

	content := lipgloss.JoinVertical(lipgloss.Center, lines...)

	modalStyle := lipgloss.NewStyle().
		Width(modalWidth).
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color(theme.Warning)).
		Padding(1, 2).
		Align(lipgloss.Center)

	return modalStyle.Render(content)
}

// GitInitModal prompts user to initialize git in the workspace
type GitInitModal struct {
	selected int
	onSelect func(bool) // true = init, false = skip
}

// NewGitInitModal creates a git init prompt modal
func NewGitInitModal(onSelect func(bool)) *GitInitModal {
	return &GitInitModal{
		selected: 1, // Default to "No" for safety
		onSelect: onSelect,
	}
}

// Update handles input for the modal
func (m *GitInitModal) Update(key string) {
	switch key {
	case "h", "left", "tab":
		m.selected = (m.selected + 1) % 2
	case "l", "right", "shift+tab":
		m.selected = (m.selected + 1) % 2
	case "y", "Y":
		if m.onSelect != nil {
			m.onSelect(true)
		}
	case "n", "N", "esc":
		if m.onSelect != nil {
			m.onSelect(false)
		}
	case "enter", " ":
		if m.onSelect != nil {
			m.onSelect(m.selected == 0) // 0 = Yes
		}
	}
}

// Render renders the git init modal
func (m *GitInitModal) Render(width, height int, theme Theme) string {
	modalWidth := 50
	if modalWidth > width-8 {
		modalWidth = width - 8
	}

	// Title
	titleStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(theme.Info)).
		Bold(true)
	title := titleStyle.Render(i18n.T("classic_chat.git_init.title"))

	// Message
	msgStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(theme.Text))
	message := msgStyle.Render(i18n.T("classic_chat.git_init.not_repository"))
	message2 := msgStyle.Render(i18n.T("classic_chat.git_init.message"))

	// Options
	options := []string{i18n.T("classic_chat.git_init.yes"), i18n.T("classic_chat.git_init.no")}
	var buttons []string
	for i, opt := range options {
		btnStyle := lipgloss.NewStyle().
			Foreground(lipgloss.Color(theme.TextMuted)).
			Padding(0, 1)

		if i == m.selected {
			btnStyle = btnStyle.
				Foreground(lipgloss.Color(theme.Primary)).
				Bold(true)
			opt = "› " + opt + " ‹"
		}
		buttons = append(buttons, btnStyle.Render(opt))
	}
	buttonRow := lipgloss.JoinHorizontal(lipgloss.Left, buttons...)

	// Hints
	hintStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(theme.TextMuted)).
		Italic(true)
	hints := hintStyle.Render(i18n.T("classic_chat.git_init.hint"))

	// Build content
	var lines []string
	lines = append(lines, "")
	lines = append(lines, title)
	lines = append(lines, "")
	lines = append(lines, message)
	lines = append(lines, message2)
	lines = append(lines, "")
	lines = append(lines, buttonRow)
	lines = append(lines, "")
	lines = append(lines, hints)

	content := lipgloss.JoinVertical(lipgloss.Center, lines...)

	modalStyle := lipgloss.NewStyle().
		Width(modalWidth).
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color(theme.Info)).
		Padding(1, 2).
		Align(lipgloss.Center)

	return modalStyle.Render(content)
}
