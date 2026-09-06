package chat

import (
	"strings"

	"charm.land/lipgloss/v2"
)

// renderTwoPane renders History as a responsive vertical split: the conversation
// list stays above the selected-conversation preview.
func (a *App) renderTwoPane(width, height int) string {
	if !a.twoPaneMode {
		return a.renderConversationsList(width, height)
	}

	if !a.sidebarVisible {
		return a.renderMessagesPane(width, height)
	}

	listHeight, previewHeight := historyStackHeights(height)
	return lipgloss.JoinVertical(
		lipgloss.Left,
		a.renderSidebarPane(width, listHeight),
		a.renderMessagesPane(width, previewHeight),
	)
}

func historyStackHeights(height int) (listHeight, previewHeight int) {
	if height <= 1 {
		return max(0, height), 0
	}
	if height < 12 {
		previewHeight = max(3, height/3)
	} else if height < 24 {
		previewHeight = 6
	} else {
		previewHeight = height / 2
	}
	if previewHeight >= height {
		previewHeight = height - 1
	}
	listHeight = height - previewHeight
	return listHeight, previewHeight
}

// renderPaneDivider creates a vertical divider between panes
func (a *App) renderPaneDivider(height int) string {
	th := a.theme

	dividerStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(th.Border)).
		Height(height)

	divider := strings.Builder{}
	for i := range height {
		if i == 0 {
			divider.WriteString("┬")
		} else if i == height-1 {
			divider.WriteString("┴")
		} else {
			divider.WriteString("│")
		}
		if i < height-1 {
			divider.WriteString("\n")
		}
	}

	return dividerStyle.Render(divider.String())
}

// renderSidebarPane renders the conversations list in the sidebar
func (a *App) renderSidebarPane(width, height int) string {
	th := a.theme

	panelStyle := lipgloss.NewStyle().
		Width(width).
		Height(height).
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color(th.Border))

	panelStyle = panelStyle.BorderForeground(lipgloss.Color(th.Primary))

	innerHeight := height - 2
	if innerHeight < 1 {
		innerHeight = 1
	}

	content := a.renderSidecarSidebarPane(width-2, innerHeight)

	return panelStyle.Render(content)
}

// renderMessagesPane renders the right-hand panel of the history menu.
// For the consolidated Layout C this shows the conversation SUMMARY + stats
// (not raw messages).
func (a *App) renderMessagesPane(width, height int) string {
	th := a.theme

	panelStyle := lipgloss.NewStyle().
		Width(width).
		Height(height).
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color(th.Border))

	contentWidth := width - 4
	if contentWidth < 20 {
		contentWidth = 20
	}
	innerHeight := height - 2
	if innerHeight < 1 {
		innerHeight = 1
	}

	content := a.renderHistoryPreview(contentWidth, innerHeight)

	return panelStyle.Render(content)
}

// renderConversationsList is a fallback for non-two-pane mode
func (a *App) renderConversationsList(width, height int) string {
	return a.viewConversations()
}
