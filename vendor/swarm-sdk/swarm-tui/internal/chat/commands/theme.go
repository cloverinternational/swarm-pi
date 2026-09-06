package commands

import (
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/i18n"
)

// ThemePresetInfo represents a theme available for selection
type ThemePresetInfo struct {
	Name        string
	Description string
	Primary     string
	Accent      string
	BG          string
	BGLight     string
	Text        string
}

// ThemeCommand opens an interactive menu to select and switch themes
type ThemeCommand struct {
	interactive bool
	selectedIdx int
	width       int
	height      int
	lastResult  string
	lastError   string

	// Callback to apply theme changes
	onThemeSelect func(themeName string) tea.Cmd

	// Available themes - populated from app's theme presets
	themes []ThemePresetInfo
}

// NewThemeCommand creates a new /theme command
func NewThemeCommand() *ThemeCommand {
	return &ThemeCommand{
		interactive: false,
		selectedIdx: 0,
		width:       80,
		height:      24,
		themes:      defaultThemes(),
	}
}

// SetOnThemeSelect sets the callback for when a theme is selected
func (c *ThemeCommand) SetOnThemeSelect(callback func(themeName string) tea.Cmd) {
	c.onThemeSelect = callback
}

// SetThemes allows setting the available themes from the app
func (c *ThemeCommand) SetThemes(themes []ThemePresetInfo) {
	c.themes = themes
}

func (c *ThemeCommand) Name() string {
	return "theme"
}

func (c *ThemeCommand) Description() string {
	return i18n.T("commands.theme.description")
}

func (c *ThemeCommand) Aliases() []string {
	return []string{"t", "colors", "palette"}
}

func (c *ThemeCommand) Execute(args []string) tea.Cmd {
	// Handle direct theme name argument
	if len(args) > 0 {
		themeName := strings.Join(args, " ")
		// Find theme by name (case-insensitive)
		for _, t := range c.themes {
			if strings.EqualFold(t.Name, themeName) {
				return func() tea.Msg {
					return ThemeSelectedMsg{ThemeName: t.Name}
				}
			}
		}
		// Theme not found
		c.lastError = i18n.T("commands.theme.not_found", themeName)
		c.interactive = true
		return func() tea.Msg {
			return ThemeErrorMsg{Error: c.lastError}
		}
	}

	// No args - open interactive picker
	c.interactive = true
	c.selectedIdx = 0
	c.lastResult = ""
	c.lastError = ""
	return func() tea.Msg {
		return ThemeMenuOpenMsg{}
	}
}

func (c *ThemeCommand) Update(msg tea.Msg) (Command, tea.Cmd) {
	// Only handle messages when interactive
	if !c.interactive {
		return c, nil
	}

	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "up", "k":
			if c.selectedIdx > 0 {
				c.selectedIdx--
			}
			return c, nil
		case "down", "j":
			if c.selectedIdx < len(c.themes)-1 {
				c.selectedIdx++
			}
			return c, nil
		case "enter", " ":
			// Select the current theme
			if c.selectedIdx >= 0 && c.selectedIdx < len(c.themes) {
				selectedTheme := c.themes[c.selectedIdx].Name
				c.interactive = false // Close the menu
				c.lastResult = i18n.T("commands.theme.changed", selectedTheme)
				return c, func() tea.Msg {
					return ThemeSelectedMsg{ThemeName: selectedTheme}
				}
			}
		case "esc", "q":
			// Cancel and close
			c.interactive = false
			return c, func() tea.Msg {
				return ThemeCancelMsg{}
			}
		case "1", "2", "3", "4", "5", "6", "7", "8", "9":
			// Direct number selection
			idx := int(msg.String()[0] - '1')
			if idx >= 0 && idx < len(c.themes) {
				c.selectedIdx = idx
				selectedTheme := c.themes[c.selectedIdx].Name
				c.interactive = false
				c.lastResult = i18n.T("commands.theme.changed", selectedTheme)
				return c, func() tea.Msg {
					return ThemeSelectedMsg{ThemeName: selectedTheme}
				}
			}
		}

	case tea.WindowSizeMsg:
		c.width = msg.Width
		c.height = msg.Height
	}

	return c, nil
}

func (c *ThemeCommand) View() string {
	if !c.interactive {
		if c.lastError != "" {
			return lipgloss.NewStyle().Foreground(lipgloss.Color("#F87171")).Render(i18n.T("commands.common.error_prefix", c.lastError))
		}
		if c.lastResult != "" {
			return lipgloss.NewStyle().Foreground(lipgloss.Color("#34D399")).Render(c.lastResult)
		}
		return ""
	}

	return c.renderMenu()
}

func (c *ThemeCommand) IsInteractive() bool {
	return c.interactive
}

// renderMenu renders the theme selection menu
func (c *ThemeCommand) renderMenu() string {
	var sb strings.Builder

	titleStyle := lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color("#6E64E8")).
		MarginBottom(1)

	_ = lipgloss.NewStyle().
		Foreground(lipgloss.Color("#39D2C0")).
		Bold(true)

	hintStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color("#94A3B8")).
		Italic(true)

	sb.WriteString(titleStyle.Render(i18n.T("commands.theme.selector_title")))
	sb.WriteString("\n\n")

	for i, theme := range c.themes {
		row := c.renderThemeRow(i, theme)
		sb.WriteString(row)
		sb.WriteString("\n")
	}

	sb.WriteString("\n")
	sb.WriteString(hintStyle.Render(i18n.T("commands.theme.selector_hint")))

	return sb.String()
}

func (c *ThemeCommand) renderThemeRow(idx int, theme ThemePresetInfo) string {
	isSelected := idx == c.selectedIdx

	// Selection indicator
	indicator := "  "
	if isSelected {
		indicator = "▶ "
	}

	// Color swatch
	swatch := c.renderSwatch(theme)

	// Theme name
	nameStyle := lipgloss.NewStyle()
	if isSelected {
		nameStyle = nameStyle.
			Foreground(lipgloss.Color(theme.Primary)).
			Bold(true)
	} else {
		nameStyle = nameStyle.
			Foreground(lipgloss.Color("#E2E8F0"))
	}

	// Description
	descStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color("#94A3B8"))

	row := indicator + swatch + "  " + nameStyle.Render(theme.Name)
	if len(theme.Description) > 0 {
		row += "  " + descStyle.Render(theme.Description)
	}

	// Highlight entire row if selected
	if isSelected {
		rowStyle := lipgloss.NewStyle().
			Background(lipgloss.Color("#151B24")).
			Width(c.width - 2)
		row = rowStyle.Render(row)
	}

	return row
}

func (c *ThemeCommand) renderSwatch(theme ThemePresetInfo) string {
	colors := []string{theme.BG, theme.BGLight, theme.Primary, theme.Accent, theme.Text}
	var b strings.Builder
	for _, color := range colors {
		if color == "" {
			b.WriteString(" ")
			continue
		}
		b.WriteString(lipgloss.NewStyle().
			Background(lipgloss.Color(color)).
			Foreground(lipgloss.Color(color)).
			Render("█"))
	}
	return b.String()
}

// defaultThemes returns the built-in theme list
func defaultThemes() []ThemePresetInfo {
	return []ThemePresetInfo{
		{Name: "SwarmCode", Description: i18n.T("commands.residual_final.theme.swarmcode_description"), Primary: "#6E64E8", Accent: "#39D2C0", BG: "#0E1118", BGLight: "#151B24", Text: "#FFFFFF"},
		{Name: "Midnight", Description: i18n.T("commands.residual_final.theme.midnight_description"), Primary: "#A78BFA", Accent: "#818CF8", BG: "#0D0D1A", BGLight: "#12122A", Text: "#E2E8F0"},
		{Name: "Nord", Description: i18n.T("commands.residual_final.theme.nord_description"), Primary: "#88C0D0", Accent: "#8FBCBB", BG: "#2E3440", BGLight: "#3B4252", Text: "#ECEFF4"},
		{Name: "Gruvbox Dark", Description: i18n.T("commands.residual_final.theme.gruvbox_description"), Primary: "#FABD2F", Accent: "#8EC07C", BG: "#1D2021", BGLight: "#282828", Text: "#EBDBB2"},
		{Name: "Dracula", Description: i18n.T("commands.residual_final.theme.dracula_description"), Primary: "#BD93F9", Accent: "#50FA7B", BG: "#282A36", BGLight: "#323443", Text: "#F8F8F2"},
		{Name: "Monokai", Description: i18n.T("commands.residual_final.theme.monokai_description"), Primary: "#F92672", Accent: "#A6E22E", BG: "#272822", BGLight: "#383830", Text: "#F8F8F2"},
		{Name: "Solarized Dark", Description: i18n.T("commands.residual_final.theme.solarized_description"), Primary: "#268BD2", Accent: "#2AA198", BG: "#002B36", BGLight: "#073642", Text: "#EEE8D5"},
		{Name: "Catppuccin Mocha", Description: i18n.T("commands.residual_final.theme.catppuccin_description"), Primary: "#F5C2E7", Accent: "#94E2D5", BG: "#1E1E2E", BGLight: "#313244", Text: "#CDD6F4"},
		{Name: "Light", Description: i18n.T("commands.residual_final.theme.light_description"), Primary: "#2563EB", Accent: "#0891B2", BG: "#FFFFFF", BGLight: "#F3F4F6", Text: "#111827"},
		{Name: "GitHub Light", Description: i18n.T("commands.residual_final.theme.github_light_description"), Primary: "#0969DA", Accent: "#0969DA", BG: "#FFFFFF", BGLight: "#F6F8FA", Text: "#1F2328"},
	}
}

// Message types for theme command

// ThemeSelectedMsg is sent when a theme is selected
type ThemeSelectedMsg struct {
	ThemeName string
}

// ThemeMenuOpenMsg is sent when the theme menu opens
type ThemeMenuOpenMsg struct{}

// ThemeCancelMsg is sent when the user cancels theme selection
type ThemeCancelMsg struct{}

// ThemeErrorMsg carries error messages
type ThemeErrorMsg struct {
	Error string
}
