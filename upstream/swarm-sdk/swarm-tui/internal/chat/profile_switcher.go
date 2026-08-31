package chat

import (
	"strings"

	"charm.land/lipgloss/v2"
	sdkprofiles "github.com/Swarm-Code/mono/swarm-sdk/internal/profiles"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/i18n"
)

// ProfileSwitcherItem represents one profile choice
type ProfileSwitcherItem struct {
	ID          string
	Name        string
	Description string
	Roles       string // Summary of role configuration
	IsCurrent   bool
	IsDefault   bool
}

// ProfileSwitcher is a quick-access overlay for switching agent profiles
type ProfileSwitcher struct {
	visible     bool
	items       []ProfileSwitcherItem
	filtered    []ProfileSwitcherItem
	selected    int
	searchQuery string
	// banner is an optional one-line message shown below the title,
	// e.g. when the switcher is opened due to provider exhaustion.
	banner string
}

// NewProfileSwitcher creates a new profile switcher
func NewProfileSwitcher() *ProfileSwitcher {
	return &ProfileSwitcher{}
}

// Show opens the profile switcher
func (ps *ProfileSwitcher) Show(profilesList []sdkprofiles.AgentProfile, currentProfileID, defaultProfileID string) {
	ps.ShowWithBanner(profilesList, currentProfileID, defaultProfileID, "")
}

// ShowWithBanner opens the profile switcher with an optional banner line
// shown below the title (e.g. "⚠ Provider exhausted — select a fallback profile").
func (ps *ProfileSwitcher) ShowWithBanner(profilesList []sdkprofiles.AgentProfile, currentProfileID, defaultProfileID, banner string) {
	ps.visible = true
	ps.searchQuery = ""
	ps.selected = 0
	ps.items = nil

	currentIdx := 0
	for i, profile := range profilesList {
		isCurrent := profile.ID == currentProfileID
		isDefault := profile.ID == defaultProfileID
		if isCurrent {
			currentIdx = i
		}

		// Build roles summary
		roleCount := len(profile.Roles)
		if roleCount == 0 {
			roleCount = len(profile.Pointers) // legacy fallback
		}
		var rolesSummary string
		if roleCount == 0 {
			rolesSummary = i18n.T("classic_chat_3.profile.no_roles")
		} else if roleCount == 1 {
			rolesSummary = i18n.T("classic_chat_3.profile.one_role")
		} else {
			rolesSummary = i18n.T("classic_chat_3.profile.roles", roleCount)
		}

		ps.items = append(ps.items, ProfileSwitcherItem{
			ID:          profile.ID,
			Name:        profile.Name,
			Description: profile.Description,
			Roles:       rolesSummary,
			IsCurrent:   isCurrent,
			IsDefault:   isDefault,
		})
	}
	ps.filtered = ps.items
	ps.selected = currentIdx
	ps.banner = banner
}

// Hide closes the switcher
func (ps *ProfileSwitcher) Hide() {
	ps.visible = false
	ps.banner = ""
}

// IsVisible returns true if open
func (ps *ProfileSwitcher) IsVisible() bool {
	return ps.visible
}

// Update handles key input
func (ps *ProfileSwitcher) Update(key string) {
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

func (ps *ProfileSwitcher) filter() {
	if ps.searchQuery == "" {
		ps.filtered = ps.items
		ps.selected = 0
		return
	}
	query := strings.ToLower(ps.searchQuery)
	ps.filtered = nil
	for _, item := range ps.items {
		if strings.Contains(strings.ToLower(item.Name), query) ||
			strings.Contains(strings.ToLower(item.Description), query) ||
			strings.Contains(strings.ToLower(item.ID), query) {
			ps.filtered = append(ps.filtered, item)
		}
	}
	if ps.selected >= len(ps.filtered) {
		ps.selected = max(0, len(ps.filtered)-1)
	}
}

// SelectedProfile returns the ID of the selected profile
func (ps *ProfileSwitcher) SelectedProfile() (id string, ok bool) {
	if ps.selected >= 0 && ps.selected < len(ps.filtered) {
		return ps.filtered[ps.selected].ID, true
	}
	return "", false
}

// Render renders the modal overlay
func (ps *ProfileSwitcher) Render(width, height int, theme Theme) string {
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
	title := titleStyle.Render(i18n.T("classic_chat_3.profile.title"))

	// Search
	searchStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(theme.Primary))
	cursor := lipgloss.NewStyle().Foreground(lipgloss.Color(theme.Primary)).Render("█")
	search := searchStyle.Render("❯ ") +
		lipgloss.NewStyle().Foreground(lipgloss.Color(theme.Text)).Render(ps.searchQuery+cursor)

	// Count
	countText := ""
	if ps.searchQuery != "" {
		countStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(theme.TextMuted))
		countText = countStyle.Render(i18n.T("classic_chat_3.profile.count", len(ps.filtered), len(ps.items)))
	}

	// Items
	maxVisible := 10
	var itemViews []string

	// Calculate scroll offset
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

		// Name with status indicators
		var nameStyle lipgloss.Style
		if item.IsCurrent {
			nameStyle = lipgloss.NewStyle().Foreground(lipgloss.Color(theme.Success)).Bold(true)
		} else if isSelected {
			nameStyle = lipgloss.NewStyle().Foreground(lipgloss.Color(theme.Primary)).Bold(true)
		} else {
			nameStyle = lipgloss.NewStyle().Foreground(lipgloss.Color(theme.Text))
		}
		name := nameStyle.Render(item.Name)

		// Status indicators
		var indicators []string
		if item.IsCurrent {
			indicators = append(indicators, lipgloss.NewStyle().Foreground(lipgloss.Color(theme.Success)).Render("✓"))
		}
		if item.IsDefault {
			indicators = append(indicators, lipgloss.NewStyle().Foreground(lipgloss.Color(theme.Accent)).Render("★"))
		}
		indicatorText := ""
		if len(indicators) > 0 {
			indicatorText = " " + strings.Join(indicators, " ")
		}

		// Build item - compact view with just name and indicators
		line := prefix + name + indicatorText
		itemViews = append(itemViews, line)
	}

	remaining := len(ps.filtered) - (scrollOff + maxVisible)
	if remaining > 0 {
		moreStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(theme.TextMuted))
		itemViews = append(itemViews, moreStyle.Render(i18n.T("classic_chat_3.common.more_down", remaining)))
	}

	if len(ps.filtered) == 0 {
		noMatch := lipgloss.NewStyle().Foreground(lipgloss.Color(theme.TextMuted)).Italic(true)
		itemViews = append(itemViews, noMatch.Render(i18n.T("classic_chat_3.profile.no_matches")))
	}

	// Hints
	hintStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(theme.TextMuted))
	hints := hintStyle.Render(i18n.T("classic_chat_3.common.select_hint"))
	legend := hintStyle.Render(i18n.T("classic_chat_3.profile.legend"))

	// Compose
	var sections []string
	sections = append(sections, title)
	// Optional exhaustion/context banner — shown in warning colour when set.
	if ps.banner != "" {
		bannerStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(theme.Warning)).Italic(true)
		sections = append(sections, bannerStyle.Render(ps.banner))
	}
	sections = append(sections, search)
	if countText != "" {
		sections = append(sections, countText)
	}
	sections = append(sections, "")
	sections = append(sections, strings.Join(itemViews, "\n"))
	sections = append(sections, "")
	sections = append(sections, hints)
	sections = append(sections, legend)

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
