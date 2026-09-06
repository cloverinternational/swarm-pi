// Package chat provides the TUI application for SwarmOS.
//
// SkillPicker is a quick-access overlay for toggling skills on/off, modeled
// after ProfileSwitcher (Ctrl+P). It can be opened via:
//   - Ctrl+S keyboard shortcut
//   - /skill or /skills slash command
//
// Each skill shows its source badge (SYS/USR/PRJ/POL) and active status.
// Use space/enter to toggle, ↑/↓ to navigate, type to filter, esc to close.

package chat

import (
	"os"
	"path/filepath"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/skills"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/i18n"
)

// SkillPickerItem represents one skill in the picker
type SkillPickerItem struct {
	ID          string
	Name        string
	Description string
	Source      string // SYS/USR/PRJ/POL
	IsActive    bool
}

// SkillPicker is a quick-access overlay for toggling skills on/off
// Similar to ProfileSwitcher but for skill activation
type SkillPicker struct {
	visible     bool
	items       []SkillPickerItem
	filtered    []SkillPickerItem
	selected    int
	searchQuery string
	// loader is the skills loader for activation/deactivation
	loader *skills.Loader
}

// NewSkillPicker creates a new skill picker
func NewSkillPicker() *SkillPicker {
	return &SkillPicker{}
}

// SetLoader sets the skill loader for activation/deactivation
func (sp *SkillPicker) SetLoader(loader *skills.Loader) {
	sp.loader = loader
}

// Show opens the skill picker with the given skills
func (sp *SkillPicker) Show(allSkills []*skills.Skill, activeSkills []*skills.Skill) {
	sp.visible = true
	sp.searchQuery = ""
	sp.selected = 0
	sp.items = nil

	// Build active map
	activeMap := make(map[string]bool)
	for _, s := range activeSkills {
		if s != nil {
			activeMap[s.Metadata.Name] = true
		}
	}

	// Get source for each skill
	getSource := func(skill *skills.Skill) string {
		if skill == nil || skill.Path == "" {
			return "SYS"
		}
		path := skill.Path
		homeDir, _ := os.UserHomeDir()

		// User-level skills directories
		userPatterns := []string{
			filepath.Join(homeDir, ".swarmos", "skills"),
			filepath.Join(homeDir, ".claude", "skills"),
			filepath.Join(homeDir, ".claude", "commands"),
			filepath.Join(homeDir, ".swarm", "skills"),
		}
		for _, pattern := range userPatterns {
			if strings.HasPrefix(path, pattern) {
				return "USR"
			}
		}

		// Project-local skills directories
		projectPatterns := []string{
			".claude" + string(filepath.Separator) + "skills",
			".claude" + string(filepath.Separator) + "commands",
			".swarm" + string(filepath.Separator) + "skills",
			".swarmos" + string(filepath.Separator) + "skills",
		}
		for _, pattern := range projectPatterns {
			if strings.Contains(path, pattern) {
				return "PRJ"
			}
		}

		// Managed/policy skills
		if managedDir := os.Getenv("SWARM_MANAGED_SKILLS_DIR"); managedDir != "" {
			if strings.HasPrefix(path, managedDir) {
				return "POL"
			}
		}

		return "SYS"
	}

	for _, skill := range allSkills {
		if skill == nil {
			continue
		}
		sp.items = append(sp.items, SkillPickerItem{
			ID:          skill.Metadata.Name,
			Name:        skill.Metadata.Name,
			Description: skill.Metadata.Description,
			Source:      getSource(skill),
			IsActive:    activeMap[skill.Metadata.Name],
		})
	}

	sp.filtered = sp.items
}

// Hide closes the picker
func (sp *SkillPicker) Hide() {
	sp.visible = false
}

// IsVisible returns true if open
func (sp *SkillPicker) IsVisible() bool {
	return sp.visible
}

// ToggleSelected toggles the active state of the currently selected skill
// Returns the skill ID, new state (true=active), and ok status
func (sp *SkillPicker) ToggleSelected() (skillID string, newState bool, ok bool) {
	if sp.selected < 0 || sp.selected >= len(sp.filtered) {
		return "", false, false
	}

	item := &sp.filtered[sp.selected]
	item.IsActive = !item.IsActive

	// Actually activate/deactivate via loader if available
	if sp.loader != nil {
		if item.IsActive {
			if err := sp.loader.Activate(item.ID); err != nil {
				// Revert on error
				item.IsActive = false
				return item.ID, false, false
			}
		} else {
			if err := sp.loader.Deactivate(item.ID); err != nil {
				// Revert on error
				item.IsActive = true
				return item.ID, true, false
			}
		}
	}

	return item.ID, item.IsActive, true
}

// Update handles key input
func (sp *SkillPicker) Update(key string) {
	switch key {
	case "down", "ctrl+n":
		if sp.selected < len(sp.filtered)-1 {
			sp.selected++
		}
	case "up", "ctrl+p":
		if sp.selected > 0 {
			sp.selected--
		}
	case "esc":
		sp.Hide()
	case " ", "enter":
		// Toggle the selected skill
		sp.ToggleSelected()
	case "backspace":
		if len(sp.searchQuery) > 0 {
			sp.searchQuery = sp.searchQuery[:len(sp.searchQuery)-1]
			sp.filter()
		}
	default:
		if len(key) == 1 && key >= " " {
			sp.searchQuery += key
			sp.filter()
		}
	}
}

func (sp *SkillPicker) filter() {
	if sp.searchQuery == "" {
		sp.filtered = sp.items
		sp.selected = 0
		return
	}
	query := strings.ToLower(sp.searchQuery)
	sp.filtered = nil
	for _, item := range sp.items {
		if strings.Contains(strings.ToLower(item.Name), query) ||
			strings.Contains(strings.ToLower(item.Description), query) ||
			strings.Contains(strings.ToLower(item.Source), query) {
			sp.filtered = append(sp.filtered, item)
		}
	}
	if sp.selected >= len(sp.filtered) {
		sp.selected = max(0, len(sp.filtered)-1)
	}
}

// SelectedSkill returns the ID of the selected skill
func (sp *SkillPicker) SelectedSkill() (id string, ok bool) {
	if sp.selected >= 0 && sp.selected < len(sp.filtered) {
		return sp.filtered[sp.selected].ID, true
	}
	return "", false
}

// Render renders the modal overlay
func (sp *SkillPicker) Render(width, height int, theme Theme) string {
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
	title := titleStyle.Render(i18n.T("classic_chat_3.skills.title"))

	// Search
	searchStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(theme.Primary))
	cursor := lipgloss.NewStyle().Foreground(lipgloss.Color(theme.Primary)).Render("█")
	search := searchStyle.Render("❯ ") +
		lipgloss.NewStyle().Foreground(lipgloss.Color(theme.Text)).Render(sp.searchQuery+cursor)

	// Count
	countText := ""
	if sp.searchQuery != "" {
		countStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(theme.TextMuted))
		countText = countStyle.Render(i18n.T("classic_chat_3.skills.count", len(sp.filtered), len(sp.items)))
	}

	// Items
	maxVisible := 10
	var itemViews []string

	// Calculate scroll offset
	scrollOff := 0
	if sp.selected >= maxVisible {
		scrollOff = sp.selected - maxVisible + 1
	}
	if scrollOff > len(sp.filtered)-maxVisible {
		scrollOff = len(sp.filtered) - maxVisible
	}
	if scrollOff < 0 {
		scrollOff = 0
	}

	if scrollOff > 0 {
		moreStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(theme.TextMuted))
		itemViews = append(itemViews, moreStyle.Render(i18n.T("classic_chat_3.common.more_up", scrollOff)))
	}

	for i := scrollOff; i < scrollOff+maxVisible && i < len(sp.filtered); i++ {
		item := sp.filtered[i]
		isSelected := i == sp.selected

		prefix := "  "
		if isSelected {
			prefix = lipgloss.NewStyle().Foreground(lipgloss.Color(theme.Primary)).Render("› ")
		}

		// Source badge color
		var srcColor string
		switch item.Source {
		case "USR":
			srcColor = theme.Success
		case "PRJ":
			srcColor = theme.Warning
		case "POL":
			srcColor = theme.Error
		default:
			srcColor = theme.Primary
		}
		srcBadge := lipgloss.NewStyle().
			Foreground(lipgloss.Color(theme.BGLight)).
			Background(lipgloss.Color(srcColor)).
			Padding(0, 1).
			Render(item.Source)

		// Name with status
		var nameStyle lipgloss.Style
		if item.IsActive {
			nameStyle = lipgloss.NewStyle().Foreground(lipgloss.Color(theme.Success)).Bold(true)
		} else if isSelected {
			nameStyle = lipgloss.NewStyle().Foreground(lipgloss.Color(theme.Primary)).Bold(true)
		} else {
			nameStyle = lipgloss.NewStyle().Foreground(lipgloss.Color(theme.Text))
		}
		name := nameStyle.Render(item.Name)

		// Status indicator
		status := ""
		if item.IsActive {
			status = lipgloss.NewStyle().Foreground(lipgloss.Color(theme.Success)).
				Render(i18n.T("classic_chat_3.skills.on"))
		} else {
			status = lipgloss.NewStyle().Foreground(lipgloss.Color(theme.TextMuted)).
				Render(i18n.T("classic_chat_3.skills.off"))
		}

		// Build item line: prefix + badge + name + status
		line := prefix + srcBadge + " " + name + "  " + status
		itemViews = append(itemViews, line)
	}

	remaining := len(sp.filtered) - (scrollOff + maxVisible)
	if remaining > 0 {
		moreStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(theme.TextMuted))
		itemViews = append(itemViews, moreStyle.Render(i18n.T("classic_chat_3.common.more_down", remaining)))
	}

	if len(sp.filtered) == 0 {
		noMatch := lipgloss.NewStyle().Foreground(lipgloss.Color(theme.TextMuted)).Italic(true)
		itemViews = append(itemViews, noMatch.Render(i18n.T("classic_chat_3.skills.no_matches")))
	}

	// Hints
	hintStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(theme.TextMuted))
	hints := hintStyle.Render(i18n.T("classic_chat_3.skills.hint"))
	legend := hintStyle.Render(i18n.T("classic_chat_3.skills.legend"))

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
