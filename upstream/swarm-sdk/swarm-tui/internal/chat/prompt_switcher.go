package chat

import (
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/chat/settings"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/i18n"
)

// PromptSwitcherItem represents one system prompt choice
type PromptSwitcherItem struct {
	Name      string
	Preview   string
	IsBuiltin bool
	IsCurrent bool
}

// PromptSwitcher is a quick-access overlay for switching system prompts
type PromptSwitcher struct {
	visible     bool
	items       []PromptSwitcherItem
	filtered    []PromptSwitcherItem
	selected    int
	searchQuery string
}

// NewPromptSwitcher creates a new prompt switcher
func NewPromptSwitcher() *PromptSwitcher {
	return &PromptSwitcher{}
}

// Show opens the prompt switcher
func (ps *PromptSwitcher) Show(prompts []settings.SystemPromptEntry, currentPromptName string) {
	ps.visible = true
	ps.searchQuery = ""
	ps.selected = 0
	ps.items = nil

	currentIdx := 0
	for _, p := range prompts {
		isCurrent := p.Name == currentPromptName
		if isCurrent {
			currentIdx = len(ps.items)
		}
		preview := p.Content
		if len(preview) > 80 {
			preview = preview[:77] + "..."
		}
		// Remove newlines for clean display
		preview = strings.ReplaceAll(preview, "\n", " ")

		ps.items = append(ps.items, PromptSwitcherItem{
			Name:      p.Name,
			Preview:   preview,
			IsBuiltin: p.Builtin,
			IsCurrent: isCurrent,
		})
	}
	ps.filtered = ps.items
	ps.selected = currentIdx
}

// Hide closes the switcher
func (ps *PromptSwitcher) Hide() {
	ps.visible = false
}

// IsVisible returns true if open
func (ps *PromptSwitcher) IsVisible() bool {
	return ps.visible
}

// Update handles key input
func (ps *PromptSwitcher) Update(key string) {
	switch key {
	case "down", "ctrl+n":
		if ps.selected < len(ps.filtered)-1 {
			ps.selected++
		}
	case "up", "ctrl+p":
		if ps.selected > 0 {
			ps.selected--
		}
	case "esc":
		ps.Hide()
	case "backspace":
		if len(ps.searchQuery) > 0 {
			ps.searchQuery = ps.searchQuery[:len(ps.searchQuery)-1]
			ps.filter()
		}
	default:
		if len(key) == 1 && key >= " " {
			ps.searchQuery += key
			ps.filter()
		}
	}
}

func (ps *PromptSwitcher) filter() {
	if ps.searchQuery == "" {
		ps.filtered = ps.items
		ps.selected = 0
		return
	}
	query := strings.ToLower(ps.searchQuery)
	ps.filtered = nil
	for _, item := range ps.items {
		if strings.Contains(strings.ToLower(item.Name), query) ||
			strings.Contains(strings.ToLower(item.Preview), query) {
			ps.filtered = append(ps.filtered, item)
		}
	}
	if ps.selected >= len(ps.filtered) {
		ps.selected = max(0, len(ps.filtered)-1)
	}
}

// SelectedPrompt returns the name of the selected prompt
func (ps *PromptSwitcher) SelectedPrompt() (name string, ok bool) {
	if ps.selected >= 0 && ps.selected < len(ps.filtered) {
		return ps.filtered[ps.selected].Name, true
	}
	return "", false
}

// Render renders the modal overlay
func (ps *PromptSwitcher) Render(width, height int, theme Theme) string {
	modalWidth := 65
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
	title := titleStyle.Render(i18n.T("classic_chat_3.prompt.title"))

	// Search
	searchStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(theme.Primary))
	cursor := lipgloss.NewStyle().Foreground(lipgloss.Color(theme.Primary)).Render("█")
	search := searchStyle.Render("❯ ") +
		lipgloss.NewStyle().Foreground(lipgloss.Color(theme.Text)).Render(ps.searchQuery+cursor)

	// Count
	countText := ""
	if ps.searchQuery != "" {
		countStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(theme.TextMuted))
		countText = countStyle.Render(i18n.T("classic_chat_3.prompt.count", len(ps.filtered), len(ps.items)))
	}

	// Items
	maxVisible := 10
	var itemViews []string

	scrollOff := 0
	if ps.selected >= maxVisible {
		scrollOff = ps.selected - maxVisible + 1
	}
	if scrollOff > len(ps.filtered)-maxVisible {
		scrollOff = len(ps.filtered) - maxVisible
	}
	if scrollOff < 0 {
		scrollOff = 0
	}

	if scrollOff > 0 {
		moreStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(theme.TextMuted))
		itemViews = append(itemViews, moreStyle.Render(i18n.T("classic_chat_3.common.more_up", scrollOff)))
	}

	for i := scrollOff; i < scrollOff+maxVisible && i < len(ps.filtered); i++ {
		item := ps.filtered[i]
		isSelected := i == ps.selected

		prefix := "  "
		if isSelected {
			prefix = lipgloss.NewStyle().Foreground(lipgloss.Color(theme.Primary)).Render("› ")
		}

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
			badge = lipgloss.NewStyle().Foreground(lipgloss.Color(theme.TextMuted)).
				Render(i18n.T("classic_chat_3.prompt.builtin"))
		}

		line1 := prefix + name + currentMark + badge

		// Preview on second line (indented)
		previewStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(theme.TextMuted)).Italic(true)
		line2 := "    " + previewStyle.Render(item.Preview)

		itemViews = append(itemViews, line1)
		itemViews = append(itemViews, line2)
	}

	remaining := len(ps.filtered) - (scrollOff + maxVisible)
	if remaining > 0 {
		moreStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(theme.TextMuted))
		itemViews = append(itemViews, moreStyle.Render(i18n.T("classic_chat_3.common.more_down", remaining)))
	}

	if len(ps.filtered) == 0 {
		noMatch := lipgloss.NewStyle().Foreground(lipgloss.Color(theme.TextMuted)).Italic(true)
		itemViews = append(itemViews, noMatch.Render(i18n.T("classic_chat_3.prompt.no_matches")))
	}

	// Hints
	hintStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(theme.TextMuted))
	hints := hintStyle.Render(i18n.T("classic_chat_3.common.select_hint"))

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
