package chat

import (
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/chat/settings"
)

// AgentSwitcherItem represents one agent choice
type AgentSwitcherItem struct {
	ID          string
	Name        string
	Description string
	Icon        string
	Provider    string
	Model       string
	IsCurrent   bool
	IsBuiltin   bool
}

// AgentSwitcher is a quick-access overlay for switching agents
type AgentSwitcher struct {
	visible     bool
	items       []AgentSwitcherItem
	filtered    []AgentSwitcherItem
	selected    int
	searchQuery string
}

// NewAgentSwitcher creates a new agent switcher
func NewAgentSwitcher() *AgentSwitcher {
	return &AgentSwitcher{}
}

// Show opens the agent switcher
func (as *AgentSwitcher) Show(agents []settings.CustomAgentEntry, currentAgentID string) {
	as.visible = true
	as.searchQuery = ""
	as.selected = 0
	as.items = nil

	currentIdx := 0
	for _, agent := range agents {
		isCurrent := agent.ID == currentAgentID
		if isCurrent {
			currentIdx = len(as.items)
		}
		desc := agent.Description
		if len(desc) > 60 {
			desc = desc[:57] + "..."
		}
		icon := agent.Icon
		if icon == "" {
			icon = "●"
		}
		as.items = append(as.items, AgentSwitcherItem{
			ID:          agent.ID,
			Name:        agent.Name,
			Description: desc,
			Icon:        icon,
			Provider:    agent.Provider,
			Model:       agent.Model,
			IsCurrent:   isCurrent,
			IsBuiltin:   agent.Builtin,
		})
	}
	as.filtered = as.items
	as.selected = currentIdx
}

// Hide closes the switcher
func (as *AgentSwitcher) Hide() {
	as.visible = false
}

// IsVisible returns true if open
func (as *AgentSwitcher) IsVisible() bool {
	return as.visible
}

// Update handles key input
func (as *AgentSwitcher) Update(key string) {
	switch key {
	case "down", "ctrl+n":
		if as.selected < len(as.filtered)-1 {
			as.selected++
		}
	case "up", "ctrl+p":
		if as.selected > 0 {
			as.selected--
		}
	case "esc":
		as.Hide()
	case "backspace":
		if len(as.searchQuery) > 0 {
			as.searchQuery = as.searchQuery[:len(as.searchQuery)-1]
			as.filter()
		}
	default:
		if len(key) == 1 && key >= " " {
			as.searchQuery += key
			as.filter()
		}
	}
}

func (as *AgentSwitcher) filter() {
	if as.searchQuery == "" {
		as.filtered = as.items
		as.selected = 0
		return
	}
	query := strings.ToLower(as.searchQuery)
	as.filtered = nil
	for _, item := range as.items {
		if strings.Contains(strings.ToLower(item.Name), query) ||
			strings.Contains(strings.ToLower(item.Description), query) ||
			strings.Contains(strings.ToLower(item.ID), query) {
			as.filtered = append(as.filtered, item)
		}
	}
	if as.selected >= len(as.filtered) {
		as.selected = max(0, len(as.filtered)-1)
	}
}

// SelectedAgent returns the ID of the selected agent
func (as *AgentSwitcher) SelectedAgent() (id string, ok bool) {
	if as.selected >= 0 && as.selected < len(as.filtered) {
		return as.filtered[as.selected].ID, true
	}
	return "", false
}

// Render renders the modal overlay
func (as *AgentSwitcher) Render(width, height int, theme Theme) string {
	modalWidth := 60
	if modalWidth > width-6 {
		modalWidth = width - 6
	}
	if modalWidth < 30 {
		modalWidth = 30
	}

	// Title
	titleStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(theme.Primary)).
		Bold(true)
	title := titleStyle.Render(tr("classic.agent_switcher.title"))

	// Search
	searchStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(theme.Primary))
	cursor := lipgloss.NewStyle().Foreground(lipgloss.Color(theme.Primary)).Render("█")
	search := searchStyle.Render("❯ ") +
		lipgloss.NewStyle().Foreground(lipgloss.Color(theme.Text)).Render(as.searchQuery+cursor)

	// Count
	countText := ""
	if as.searchQuery != "" {
		countStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(theme.TextMuted))
		countText = countStyle.Render(tr("classic.agent_switcher.count", len(as.filtered), len(as.items)))
	}

	// Items
	maxVisible := 10
	var itemViews []string

	scrollOff := 0
	if as.selected >= maxVisible {
		scrollOff = as.selected - maxVisible + 1
	}
	if scrollOff > len(as.filtered)-maxVisible {
		scrollOff = len(as.filtered) - maxVisible
	}
	if scrollOff < 0 {
		scrollOff = 0
	}

	if scrollOff > 0 {
		moreStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(theme.TextMuted))
		itemViews = append(itemViews, moreStyle.Render(tr("classic.common.more_up", scrollOff)))
	}

	for i := scrollOff; i < scrollOff+maxVisible && i < len(as.filtered); i++ {
		item := as.filtered[i]
		isSelected := i == as.selected

		prefix := "  "
		if isSelected {
			prefix = lipgloss.NewStyle().Foreground(lipgloss.Color(theme.Primary)).Render("› ")
		}

		// Icon
		icon := lipgloss.NewStyle().Foreground(lipgloss.Color(theme.Accent)).Render(item.Icon)

		// Name
		var nameStyle lipgloss.Style
		if item.IsCurrent {
			nameStyle = lipgloss.NewStyle().Foreground(lipgloss.Color(theme.Success)).Bold(true)
		} else if isSelected {
			nameStyle = lipgloss.NewStyle().Foreground(lipgloss.Color(theme.Primary)).Bold(true)
		} else {
			nameStyle = lipgloss.NewStyle().Foreground(lipgloss.Color(theme.Text))
		}
		name := nameStyle.Render(item.Name)

		// Current indicator
		currentMark := ""
		if item.IsCurrent {
			currentMark = lipgloss.NewStyle().Foreground(lipgloss.Color(theme.Success)).Render(" ✓")
		}

		// Badge
		badge := ""
		if item.IsBuiltin {
			badge = lipgloss.NewStyle().Foreground(lipgloss.Color(theme.TextMuted)).Render(tr("classic.agent.builtin_badge"))
		}

		// Description
		desc := ""
		if item.Description != "" {
			desc = lipgloss.NewStyle().Foreground(lipgloss.Color(theme.TextMuted)).Render(" " + item.Description)
		}

		line := prefix + icon + " " + name + currentMark + badge + desc
		itemViews = append(itemViews, line)
	}

	remaining := len(as.filtered) - (scrollOff + maxVisible)
	if remaining > 0 {
		moreStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(theme.TextMuted))
		itemViews = append(itemViews, moreStyle.Render(tr("classic.common.more_down", remaining)))
	}

	if len(as.filtered) == 0 {
		noMatch := lipgloss.NewStyle().Foreground(lipgloss.Color(theme.TextMuted)).Italic(true)
		itemViews = append(itemViews, noMatch.Render(tr("classic.agent_switcher.empty")))
	}

	// Hints
	hintStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(theme.TextMuted))
	hints := hintStyle.Render(tr("classic.common.select_hint"))

	// Compose
	var sections []string
	sections = append(sections, title)
	sections = append(sections, search)
	if countText != "" {
		sections = append(sections, countText)
	}
	sections = append(sections, "")
	sections = append(sections, strings.Join(itemViews, "\n"))
	sections = append(sections, "")
	sections = append(sections, hints)

	content := strings.Join(sections, "\n")

	boxStyle := lipgloss.NewStyle().
		Width(modalWidth).
		Padding(1, 2).
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color(theme.Primary)).
		Background(lipgloss.Color(theme.BG))

	return boxStyle.Render(content)
}
