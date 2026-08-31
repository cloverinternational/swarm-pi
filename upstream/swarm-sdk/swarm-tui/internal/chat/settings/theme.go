package settings

import (
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/i18n"
)

// BackgroundModes lists available background rendering modes in display order.
var BackgroundModes = []struct {
	Key         string
	Label       string
	Description string
}{
	{"solid", "Solid Background", "Fill every pixel with the theme background colour — cleanest look"},
	{"none", "No Background", "Transparent — let your terminal background show through"},
}

// ThemeSettings manages theme/appearance settings.
type ThemeSettings struct {
	themeName      string // active palette name, e.g. "SwarmCode"
	backgroundMode string // "solid" or "none"
}

// NewThemeSettings creates a ThemeSettings handler initialised to the given values.
func NewThemeSettings(themeName, backgroundMode string) *ThemeSettings {
	if themeName == "" {
		themeName = "SwarmCode"
	}
	if backgroundMode == "" {
		backgroundMode = "solid"
	}
	return &ThemeSettings{
		themeName:      themeName,
		backgroundMode: backgroundMode,
	}
}

// --- Accessors -----------------------------------------------------------------

func (t *ThemeSettings) GetThemeName() string      { return t.themeName }
func (t *ThemeSettings) GetBackgroundMode() string { return t.backgroundMode }

// --- Mutators ------------------------------------------------------------------

func (t *ThemeSettings) SetThemeName(name string)      { t.themeName = name }
func (t *ThemeSettings) SetBackgroundMode(mode string) { t.backgroundMode = mode }

// NextTheme cycles forward through the preset list.
func (t *ThemeSettings) NextTheme(presets []ThemePresetInfo) {
	for i, p := range presets {
		if p.Name == t.themeName {
			t.themeName = presets[(i+1)%len(presets)].Name
			return
		}
	}
	if len(presets) > 0 {
		t.themeName = presets[0].Name
	}
}

// PrevTheme cycles backward through the preset list.
func (t *ThemeSettings) PrevTheme(presets []ThemePresetInfo) {
	for i, p := range presets {
		if p.Name == t.themeName {
			idx := i - 1
			if idx < 0 {
				idx = len(presets) - 1
			}
			t.themeName = presets[idx].Name
			return
		}
	}
	if len(presets) > 0 {
		t.themeName = presets[len(presets)-1].Name
	}
}

// NextBackgroundMode cycles forward through background modes.
func (t *ThemeSettings) NextBackgroundMode() {
	for i, m := range BackgroundModes {
		if m.Key == t.backgroundMode {
			t.backgroundMode = BackgroundModes[(i+1)%len(BackgroundModes)].Key
			return
		}
	}
	t.backgroundMode = BackgroundModes[0].Key
}

// PrevBackgroundMode cycles backward through background modes.
func (t *ThemeSettings) PrevBackgroundMode() {
	for i, m := range BackgroundModes {
		if m.Key == t.backgroundMode {
			idx := i - 1
			if idx < 0 {
				idx = len(BackgroundModes) - 1
			}
			t.backgroundMode = BackgroundModes[idx].Key
			return
		}
	}
	t.backgroundMode = BackgroundModes[len(BackgroundModes)-1].Key
}

// ThemePresetInfo is a lightweight summary used by the render function.
// The full Theme struct lives in the chat package; here we only need name + description.
type ThemePresetInfo struct {
	Name        string
	Description string
	// Swatch colours used to draw the preview strip
	Primary string
	Accent  string
	BG      string
	BGLight string
	Text    string
}

// SelectedThemeIdx returns the index of the active theme in the preset list (-1 if unknown).
func (t *ThemeSettings) SelectedThemeIdx(presets []ThemePresetInfo) int {
	for i, p := range presets {
		if p.Name == t.themeName {
			return i
		}
	}
	return 0
}

// SelectedBGModeIdx returns the index of the active background mode (-1 if unknown).
func (t *ThemeSettings) SelectedBGModeIdx() int {
	for i, m := range BackgroundModes {
		if m.Key == t.backgroundMode {
			return i
		}
	}
	return 0
}

// --- Rendering -----------------------------------------------------------------

// ThemeSettingsState is the subset of State fields relevant to this section.
// We read from the shared State via the ThemeSelectedItem / ThemeSelectedPanel fields.

const (
	ThemePanelPalette    = 0
	ThemePanelBackground = 1
)

// Render draws the Theme & Background settings screen.
//
// state.SelectedItem encodes a two-dimensional cursor:
//
//	0..N-1  → palette panel rows (one per theme preset)
//	N       → background mode panel
//
// Within the background panel, state.ThemeSelectedBGIdx picks which background mode row is active.
func (t *ThemeSettings) Render(width, height int, state *State, thm any, presets []ThemePresetInfo) string {
	th := thm.(Theme)
	bg := th.BG

	// For hover/selected highlight we need something visible even in no-bg mode
	selBg := th.BGLighter
	if selBg == "" {
		selBg = th.BGLight
	}

	totalItems := len(presets) + len(BackgroundModes)
	_ = totalItems

	var lines []string

	safeWidth := maxInt(20, width-4)

	// ── Title ──────────────────────────────────────────────────────────────────
	titleStyle := lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color(th.Primary)).
		Background(lipgloss.Color(bg)).
		Width(safeWidth).
		Align(lipgloss.Center)
	lines = append(lines, titleStyle.Render(i18n.T("settings.theme.title")))

	if width >= 50 {
		subtitleStyle := lipgloss.NewStyle().
			Foreground(lipgloss.Color(th.TextMuted)).
			Background(lipgloss.Color(bg)).
			Italic(true).
			Width(safeWidth).
			Align(lipgloss.Center)
		lines = append(lines, subtitleStyle.Render(i18n.T("settings.theme.subtitle")))
	}
	lines = append(lines, "")

	divStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(th.Border)).
		Background(lipgloss.Color(bg)).
		Padding(0, 2)

	// ── Colour Palette Section ─────────────────────────────────────────────────
	sectionHeaderStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(th.Accent)).
		Background(lipgloss.Color(bg)).
		Bold(true).
		Padding(0, 2)
	lines = append(lines, sectionHeaderStyle.Render(i18n.T("settings.theme.palette")))
	lines = append(lines, divStyle.Render(strings.Repeat("─", maxInt(5, width-6))))

	for i, preset := range presets {
		isSelected := i == state.SelectedItem && state.Focus == FocusContent
		isCurrent := preset.Name == t.themeName

		// Row background — highlighted when selected, else transparent
		rowBg := bg
		if isSelected {
			rowBg = selBg
		}

		// Selection indicator
		indicator := "  "
		if isCurrent {
			indicator = "● "
		}
		if isSelected {
			indicator = "▶ "
		}

		// Colour swatch
		swatch := t.renderSwatch(preset)

		// Name style — at narrow widths reduce fixed name width
		nameW := 18
		if width < 50 {
			nameW = 12
		}
		var nameStyle lipgloss.Style
		if isSelected {
			nameStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color(th.Primary)).
				Background(lipgloss.Color(rowBg)).
				Bold(true).
				Width(nameW)
		} else if isCurrent {
			nameStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color(th.Accent)).
				Background(lipgloss.Color(rowBg)).
				Bold(true).
				Width(nameW)
		} else {
			nameStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color(th.Text)).
				Background(lipgloss.Color(rowBg)).
				Width(nameW)
		}

		descStyle := lipgloss.NewStyle().
			Foreground(lipgloss.Color(th.TextMuted)).
			Background(lipgloss.Color(rowBg))

		name := nameStyle.Render(preset.Name)

		currentTag := ""
		if isCurrent {
			currentTag = lipgloss.NewStyle().
				Foreground(lipgloss.Color(th.Success)).
				Background(lipgloss.Color(rowBg)).
				Render(" ✓")
		}

		indStyle := lipgloss.NewStyle().
			Foreground(lipgloss.Color(th.TextMuted)).
			Background(lipgloss.Color(rowBg))
		if isSelected {
			indStyle = indStyle.Foreground(lipgloss.Color(th.Primary))
		} else if isCurrent {
			indStyle = indStyle.Foreground(lipgloss.Color(th.Accent))
		}

		// At narrow widths hide description; at medium hide swatch description
		var rowContent string
		if width < 50 {
			rowContent = indStyle.Render(indicator) + name + currentTag
		} else {
			desc := descStyle.Render(localizedThemeDescription(preset))
			rowContent = indStyle.Render(indicator) + swatch + "  " + name + "  " + desc + currentTag
		}
		if isSelected {
			rowStyle := lipgloss.NewStyle().
				Background(lipgloss.Color(rowBg)).
				Width(maxInt(20, width-4))
			rowContent = rowStyle.Render(rowContent)
		}

		lines = append(lines, "  "+rowContent)
	}

	lines = append(lines, "")

	// ── Background Mode Section ────────────────────────────────────────────────
	lines = append(lines, sectionHeaderStyle.Render(i18n.T("settings.theme.background")))
	lines = append(lines, divStyle.Render(strings.Repeat("─", maxInt(5, width-6))))

	for i, mode := range BackgroundModes {
		itemIdx := len(presets) + i
		isSelected := itemIdx == state.SelectedItem && state.Focus == FocusContent
		isCurrent := mode.Key == t.backgroundMode

		rowBg := bg
		if isSelected {
			rowBg = selBg
		}

		indicator := "  "
		if isCurrent {
			indicator = "● "
		}
		if isSelected {
			indicator = "▶ "
		}

		modeIcon := t.bgModeIcon(mode.Key)

		bgNameW := 22
		if width < 50 {
			bgNameW = 16
		}
		var nameStyle lipgloss.Style
		if isSelected {
			nameStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color(th.Primary)).
				Background(lipgloss.Color(rowBg)).
				Bold(true).
				Width(bgNameW)
		} else if isCurrent {
			nameStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color(th.Accent)).
				Background(lipgloss.Color(rowBg)).
				Bold(true).
				Width(bgNameW)
		} else {
			nameStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color(th.Text)).
				Background(lipgloss.Color(rowBg)).
				Width(bgNameW)
		}

		descStyle := lipgloss.NewStyle().
			Foreground(lipgloss.Color(th.TextMuted)).
			Background(lipgloss.Color(rowBg))

		currentTag := ""
		if isCurrent {
			currentTag = lipgloss.NewStyle().
				Foreground(lipgloss.Color(th.Success)).
				Background(lipgloss.Color(rowBg)).
				Render(i18n.T("settings.theme.active"))
		}

		name := nameStyle.Render(modeIcon + " " + localizedBackgroundModeLabel(mode.Key))

		indStyle := lipgloss.NewStyle().
			Foreground(lipgloss.Color(th.TextMuted)).
			Background(lipgloss.Color(rowBg))
		if isSelected {
			indStyle = indStyle.Foreground(lipgloss.Color(th.Primary))
		} else if isCurrent {
			indStyle = indStyle.Foreground(lipgloss.Color(th.Accent))
		}

		var rowContent string
		if width < 50 {
			rowContent = indStyle.Render(indicator) + name + currentTag
		} else {
			desc := descStyle.Render(localizedBackgroundModeDescription(mode.Key))
			rowContent = indStyle.Render(indicator) + name + "  " + desc + currentTag
		}
		if isSelected {
			rowStyle := lipgloss.NewStyle().
				Background(lipgloss.Color(rowBg)).
				Width(maxInt(20, width-4))
			rowContent = rowStyle.Render(rowContent)
		}

		lines = append(lines, "  "+rowContent)
		lines = append(lines, "")
	}

	// ── Hints ──────────────────────────────────────────────────────────────────
	hintStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(th.TextMuted)).
		Background(lipgloss.Color(bg)).
		Width(safeWidth).
		Align(lipgloss.Center)
	keyBg := th.BGLight
	if keyBg == "" {
		keyBg = th.BGLighter
	}
	keyStyle := lipgloss.NewStyle().
		Background(lipgloss.Color(keyBg)).
		Foreground(lipgloss.Color(th.Text)).
		Padding(0, 1).
		Bold(true)

	hintText := i18n.T("settings.theme.hints",
		keyStyle.Render("↑/↓"),
		keyStyle.Render("Enter/Space"))
	hints := hintStyle.Render(hintText)

	content := lipgloss.JoinVertical(lipgloss.Left, lines...)
	fullContent := lipgloss.JoinVertical(lipgloss.Left, content, hints)

	containerStyle := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color(th.Border)).
		Background(lipgloss.Color(bg)).
		Width(maxInt(20, width-2)).
		Padding(0, 1)

	return containerStyle.Render(fullContent)
}

func localizedThemeDescription(preset ThemePresetInfo) string {
	keys := map[string]string{
		"SwarmCode":   "settings.theme.swarmcode.description",
		"Midnight":    "settings.theme.midnight.description",
		"Nord":        "settings.theme.nord.description",
		"Gruvbox":     "settings.theme.gruvbox.description",
		"Dracula":     "settings.theme.dracula.description",
		"Monokai":     "settings.theme.monokai.description",
		"Catppuccin":  "settings.theme.catppuccin.description",
		"Tokyo Night": "settings.theme.tokyo_night.description",
		"Solarized":   "settings.theme.solarized.description",
		"Horizon":     "settings.theme.horizon.description",
	}
	if key, ok := keys[preset.Name]; ok {
		return i18n.T(key)
	}
	return preset.Description
}

func localizedBackgroundModeLabel(key string) string {
	switch key {
	case "solid":
		return i18n.T("settings.theme.background.solid.label")
	case "none":
		return i18n.T("settings.theme.background.none.label")
	default:
		return key
	}
}

func localizedBackgroundModeDescription(key string) string {
	switch key {
	case "solid":
		return i18n.T("settings.theme.background.solid.description")
	case "none":
		return i18n.T("settings.theme.background.none.description")
	default:
		return ""
	}
}

// renderSwatch draws a small 5-block colour strip for a palette.
func (t *ThemeSettings) renderSwatch(p ThemePresetInfo) string {
	colors := []string{p.BG, p.BGLight, p.Primary, p.Accent, p.Text}
	var b strings.Builder
	for _, c := range colors {
		if c == "" {
			b.WriteString(" ")
			continue
		}
		b.WriteString(lipgloss.NewStyle().
			Background(lipgloss.Color(c)).
			Foreground(lipgloss.Color(c)).
			Render("█"))
	}
	return b.String()
}

func (t *ThemeSettings) bgModeIcon(key string) string {
	switch key {
	case "solid":
		return "█"
	case "none":
		return "░"
	default:
		return "·"
	}
}
