package components

import (
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/chatui/theme"
)

// TopBar renders the top bar of the chat UI.
type TopBar struct {
	theme theme.Theme
	width int

	// Content
	title         string
	model         string
	provider      string
	status        string
	tokenCount    int
	contextWindow int
}

// NewTopBar creates a new top bar component.
func NewTopBar(th theme.Theme) *TopBar {
	return &TopBar{
		theme: th,
		width: 80,
		title: "SwarmOS",
	}
}

// SetWidth updates the top bar width.
func (t *TopBar) SetWidth(width int) {
	t.width = width
}

// SetTitle sets the title text.
func (t *TopBar) SetTitle(title string) {
	t.title = title
}

// SetModel sets the current model name.
func (t *TopBar) SetModel(model string) {
	t.model = model
}

// SetProvider sets the current provider name.
func (t *TopBar) SetProvider(provider string) {
	t.provider = provider
}

// SetStatus sets the status indicator.
func (t *TopBar) SetStatus(status string) {
	t.status = status
}

// SetTokenInfo sets the token count and context window.
func (t *TopBar) SetTokenInfo(count, window int) {
	t.tokenCount = count
	t.contextWindow = window
}

// View renders the top bar.
func (t *TopBar) View() string {
	th := t.theme

	// Styles
	bgStyle := lipgloss.NewStyle().
		Background(lipgloss.Color(th.BackgroundLightColor())).
		Width(t.width)

	titleStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(th.TextColor())).
		Bold(true).
		Padding(0, 1)

	modelStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(th.PrimaryColor())).
		Padding(0, 1)

	statusStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(th.TextDimColor())).
		Padding(0, 1)

	// Build left side: title
	left := titleStyle.Render(t.title)

	// Build right side: model @ provider | status
	var rightParts []string
	if t.model != "" {
		modelText := t.model
		if t.provider != "" {
			modelText += " @ " + t.provider
		}
		rightParts = append(rightParts, modelStyle.Render(modelText))
	}
	if t.status != "" {
		rightParts = append(rightParts, statusStyle.Render(t.status))
	}
	right := strings.Join(rightParts, " ")

	// Calculate spacing
	leftWidth := lipgloss.Width(left)
	rightWidth := lipgloss.Width(right)
	spacerWidth := max(t.width-leftWidth-rightWidth, 0)

	// Compose the bar
	spacer := strings.Repeat(" ", spacerWidth)
	content := left + spacer + right

	return bgStyle.Render(content)
}
