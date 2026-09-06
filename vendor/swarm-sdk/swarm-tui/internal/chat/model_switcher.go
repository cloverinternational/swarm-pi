package chat

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/chat/commands"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/i18n"
)

// ModelSwitcherItem represents one model choice in the switcher
type ModelSwitcherItem struct {
	Provider    string
	Model       string
	DisplayName string
	Color       string
	Context     string
	IsCurrent   bool
}

// ModelSwitcher is a quick-access overlay for switching models
type ModelSwitcher struct {
	visible     bool
	items       []ModelSwitcherItem
	filtered    []ModelSwitcherItem
	selected    int
	searchQuery string
}

// NewModelSwitcher creates a new model switcher
func NewModelSwitcher() *ModelSwitcher {
	return &ModelSwitcher{}
}

// Show opens the model switcher with available providers
func (ms *ModelSwitcher) Show(providers []commands.Provider, currentProvider, currentModel string) {
	ms.visible = true
	ms.searchQuery = ""
	ms.selected = 0

	// Build flat list of provider/model pairs
	ms.items = nil
	currentIdx := 0
	for _, p := range providers {
		for _, m := range p.Models {
			display := m.DisplayName
			if display == "" {
				display = m.ID
			}
			isCurrent := strings.EqualFold(p.Name, currentProvider) && strings.EqualFold(m.ID, currentModel)
			if isCurrent {
				currentIdx = len(ms.items)
			}
			ctx := m.Context
			if ctx == "" && m.ContextWindow > 0 {
				ctx = fmt.Sprintf("%dk", m.ContextWindow/1000)
			}
			ms.items = append(ms.items, ModelSwitcherItem{
				Provider:    p.Name,
				Model:       m.ID,
				DisplayName: display,
				Color:       p.Color,
				Context:     ctx,
				IsCurrent:   isCurrent,
			})
		}
	}
	ms.filtered = ms.items
	// Start selection on current model
	ms.selected = currentIdx
}

// Hide closes the switcher
func (ms *ModelSwitcher) Hide() {
	ms.visible = false
}

// IsVisible returns true if open
func (ms *ModelSwitcher) IsVisible() bool {
	return ms.visible
}

// Update handles key input
func (ms *ModelSwitcher) Update(key string) {
	switch key {
	case "down", "ctrl+n":
		if ms.selected < len(ms.filtered)-1 {
			ms.selected++
		}
	case "up", "ctrl+p":
		if ms.selected > 0 {
			ms.selected--
		}
	case "esc":
		ms.Hide()
	case "backspace":
		if len(ms.searchQuery) > 0 {
			ms.searchQuery = ms.searchQuery[:len(ms.searchQuery)-1]
			ms.filter()
		}
	default:
		if len(key) == 1 && key >= " " {
			ms.searchQuery += key
			ms.filter()
		}
	}
}

func (ms *ModelSwitcher) filter() {
	if ms.searchQuery == "" {
		ms.filtered = ms.items
		ms.selected = 0
		return
	}
	query := strings.ToLower(ms.searchQuery)
	ms.filtered = nil
	for _, item := range ms.items {
		if strings.Contains(strings.ToLower(item.Provider), query) ||
			strings.Contains(strings.ToLower(item.DisplayName), query) ||
			strings.Contains(strings.ToLower(item.Model), query) {
			ms.filtered = append(ms.filtered, item)
		}
	}
	if ms.selected >= len(ms.filtered) {
		ms.selected = max(0, len(ms.filtered)-1)
	}
}

// SelectedModel returns the provider and model of the current selection
func (ms *ModelSwitcher) SelectedModel() (provider, model string, ok bool) {
	if ms.selected >= 0 && ms.selected < len(ms.filtered) {
		item := ms.filtered[ms.selected]
		return item.Provider, item.Model, true
	}
	return "", "", false
}

// Render renders the modal overlay
func (ms *ModelSwitcher) Render(width, height int, theme Theme) string {
	modalWidth := 65
	if modalWidth > width-6 {
		modalWidth = width - 6
	}
	if modalWidth < 30 {
		modalWidth = 30
	}
	contentWidth := modalWidth - 4

	// Title
	titleStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(theme.Primary)).
		Bold(true)
	title := titleStyle.Render(i18n.T("chat_b.model_switcher.title"))

	// Search
	searchStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(theme.Primary))
	cursor := lipgloss.NewStyle().Foreground(lipgloss.Color(theme.Primary)).Render("█")
	search := searchStyle.Render("❯ ") +
		lipgloss.NewStyle().Foreground(lipgloss.Color(theme.Text)).Render(ms.searchQuery+cursor)

	// Count
	countText := ""
	if ms.searchQuery != "" {
		countStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(theme.TextMuted))
		countText = countStyle.Render(i18n.T("chat_b.model_switcher.count", len(ms.filtered), len(ms.items)))
	}

	// Items
	maxVisible := 10
	var itemViews []string

	// Calculate scroll offset
	scrollOff := 0
	if ms.selected >= maxVisible {
		scrollOff = ms.selected - maxVisible + 1
	}
	if scrollOff > len(ms.filtered)-maxVisible {
		scrollOff = len(ms.filtered) - maxVisible
	}
	if scrollOff < 0 {
		scrollOff = 0
	}

	if scrollOff > 0 {
		moreStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(theme.TextMuted))
		itemViews = append(itemViews, moreStyle.Render(i18n.T("chat_b.model_switcher.more_up", scrollOff)))
	}

	for i := scrollOff; i < scrollOff+maxVisible && i < len(ms.filtered); i++ {
		item := ms.filtered[i]
		isSelected := i == ms.selected

		prefix := "  "
		if isSelected {
			prefix = lipgloss.NewStyle().Foreground(lipgloss.Color(theme.Primary)).Render("› ")
		}

		// Provider badge
		provColor := item.Color
		if provColor == "" {
			provColor = theme.Accent
		}
		provBadge := lipgloss.NewStyle().
			Foreground(lipgloss.Color(provColor)).
			Render(item.Provider)

		// Model name
		var nameStyle lipgloss.Style
		if item.IsCurrent {
			nameStyle = lipgloss.NewStyle().Foreground(lipgloss.Color(theme.Success)).Bold(true)
		} else if isSelected {
			nameStyle = lipgloss.NewStyle().Foreground(lipgloss.Color(theme.Primary)).Bold(true)
		} else {
			nameStyle = lipgloss.NewStyle().Foreground(lipgloss.Color(theme.Text))
		}
		name := nameStyle.Render(item.DisplayName)

		// Current indicator
		currentMark := ""
		if item.IsCurrent {
			currentMark = lipgloss.NewStyle().Foreground(lipgloss.Color(theme.Success)).Render(" ✓")
		}

		// Context on the right
		ctxText := ""
		if item.Context != "" {
			ctxText = lipgloss.NewStyle().Foreground(lipgloss.Color(theme.TextMuted)).Render(item.Context)
		}

		// Build line
		left := prefix + provBadge + " · " + name + currentMark
		leftWidth := lipgloss.Width(left)
		rightWidth := lipgloss.Width(ctxText)
		gap := contentWidth - leftWidth - rightWidth
		if gap < 1 {
			gap = 1
		}
		line := left + strings.Repeat(" ", gap) + ctxText

		itemViews = append(itemViews, line)
	}

	remaining := len(ms.filtered) - (scrollOff + maxVisible)
	if remaining > 0 {
		moreStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(theme.TextMuted))
		itemViews = append(itemViews, moreStyle.Render(i18n.T("chat_b.model_switcher.more_down", remaining)))
	}

	if len(ms.filtered) == 0 {
		noMatch := lipgloss.NewStyle().Foreground(lipgloss.Color(theme.TextMuted)).Italic(true)
		itemViews = append(itemViews, noMatch.Render(i18n.T("chat_b.model_switcher.no_matches")))
	}

	// Hints
	hintStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(theme.TextMuted))
	hints := hintStyle.Render(i18n.T("chat_b.model_switcher.hint"))

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

	// Modal box
	boxStyle := lipgloss.NewStyle().
		Width(modalWidth).
		Padding(1, 2).
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color(theme.Primary)).
		Background(lipgloss.Color(theme.BG))

	return boxStyle.Render(content)
}
