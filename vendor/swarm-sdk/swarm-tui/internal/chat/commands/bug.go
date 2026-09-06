package commands

import (
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/i18n"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/ui/palette"
)

// BugReportMsg is sent when the user confirms the bug report submission.
// The App handles this message by collecting full client state and pushing it to Sentry.
type BugReportMsg struct {
	// Description is the optional text the user typed in the preview panel
	Description string
}

// BugCancelMsg is sent when the user cancels the bug report.
type BugCancelMsg struct{}

// BugCommand shows an interactive preview panel before sending a bug report to Sentry.
type BugCommand struct {
	visible     bool
	description strings.Builder // text typed by user in the preview
	width       int
	height      int
}

// NewBugCommand creates a new /bug command.
func NewBugCommand() *BugCommand {
	return &BugCommand{}
}

func (c *BugCommand) Name() string {
	return "bug"
}

func (c *BugCommand) Description() string {
	return i18n.T("commands.bug.description")
}

func (c *BugCommand) Aliases() []string {
	return []string{"report", "feedback"}
}

// Execute opens the interactive preview panel.
// Any args passed inline are pre-filled as the description.
func (c *BugCommand) Execute(args []string) tea.Cmd {
	c.visible = true
	c.description.Reset()
	if len(args) > 0 {
		c.description.WriteString(strings.Join(args, " "))
	}
	return nil
}

// Update handles keyboard input for the preview panel.
func (c *BugCommand) Update(msg tea.Msg) (Command, tea.Cmd) {
	if !c.visible {
		return c, nil
	}

	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "enter":
			desc := strings.TrimSpace(c.description.String())
			c.visible = false
			c.description.Reset()
			return c, func() tea.Msg {
				return BugReportMsg{Description: desc}
			}

		case "esc", "ctrl+c":
			c.visible = false
			c.description.Reset()
			return c, func() tea.Msg {
				return BugCancelMsg{}
			}

		case "backspace", "ctrl+h":
			s := c.description.String()
			if len(s) > 0 {
				// Remove last rune
				runes := []rune(s)
				c.description.Reset()
				c.description.WriteString(string(runes[:len(runes)-1]))
			}

		case "ctrl+u":
			// Clear description line
			c.description.Reset()

		default:
			// In bubbletea v2, Key.Text holds printable characters typed by the user.
			// It is empty for special keys (enter, esc, backspace, ctrl+x, etc.)
			if text := msg.Key().Text; text != "" {
				c.description.WriteString(text)
			}
		}

	case tea.WindowSizeMsg:
		c.width = msg.Width
		c.height = msg.Height
	}

	return c, nil
}

// View renders the bug report confirmation panel.
func (c *BugCommand) View() string {
	if !c.visible {
		return ""
	}

	panelW := max(c.width-4, 50)
	if panelW > 80 {
		panelW = 80
	}

	accent := lipgloss.Color(palette.Accent)
	dim := lipgloss.Color(palette.TextDim)
	warn := lipgloss.Color(palette.Warning)
	err_ := lipgloss.Color(palette.Error) // debug icon color

	titleStyle := lipgloss.NewStyle().
		Foreground(err_).
		Bold(true)

	sectionStyle := lipgloss.NewStyle().
		Foreground(accent).
		Bold(true)

	dimStyle := lipgloss.NewStyle().Foreground(dim)
	textStyle := lipgloss.NewStyle()
	warnStyle := lipgloss.NewStyle().Foreground(warn)

	borderStyle := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(err_).
		Padding(1, 2).
		Width(panelW)

	var b strings.Builder

	b.WriteString(titleStyle.Render(i18n.T("commands.bug.title")) + "\n\n")

	b.WriteString(sectionStyle.Render(i18n.T("commands.bug.collected.title")) + "\n")
	items := []string{
		i18n.T("commands.bug.collected.logs"),
		i18n.T("commands.bug.collected.api_history"),
		i18n.T("commands.bug.collected.metadata"),
		i18n.T("commands.bug.collected.config"),
		i18n.T("commands.bug.collected.runtime"),
		i18n.T("commands.bug.collected.version"),
	}
	for _, item := range items {
		b.WriteString(dimStyle.Render(item) + "\n")
	}

	b.WriteString("\n")
	b.WriteString(sectionStyle.Render(i18n.T("commands.bug.description_label")) + "\n")

	desc := c.description.String()
	inputLine := desc + "▌"
	b.WriteString(textStyle.Render("  "+inputLine) + "\n")

	b.WriteString("\n")
	b.WriteString(warnStyle.Render(i18n.T("commands.bug.privacy")) + "\n")

	b.WriteString("\n")
	b.WriteString(
		dimStyle.Render("  ") +
			lipgloss.NewStyle().Foreground(lipgloss.Color(palette.Success)).Bold(true).Render("Enter") +
			dimStyle.Render(i18n.T("commands.bug.hint.send")) +
			lipgloss.NewStyle().Foreground(dim).Bold(true).Render("Esc") +
			dimStyle.Render(i18n.T("commands.bug.hint.cancel")),
	)

	return borderStyle.Render(b.String())
}

// IsInteractive returns true while the preview panel is open.
func (c *BugCommand) IsInteractive() bool {
	return c.visible
}
