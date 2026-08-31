package appshell

import (
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/i18n"
)

// Tab represents a single tab in the tab bar.
type Tab struct {
	ID    string // Unique identifier (e.g., "prompt", "settings")
	Label string // Display label (e.g., "PROMPT", "SETTINGS")
}

// TabBarTheme holds the color values needed to render the tab bar.
// This avoids importing the chat package (which would create a cycle).
type TabBarTheme struct {
	Primary    string
	PrimaryDim string
	Text       string
	TextMuted  string
	BG         string
}

// TabBar is a reusable top navigation component.
type TabBar struct {
	Brand        string
	Tabs         []Tab
	ActiveIdx    int
	Theme        TabBarTheme
	ConfigSource string // Optional: "global" or "project:Name"
}

// Render produces the 3-line tab bar: spacer + brand+tabs+configSource + border.
func (tb *TabBar) Render(width int) string {
	bg := tb.Theme.BG

	// Brand on the left
	brandStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(tb.Theme.Primary)).
		Background(lipgloss.Color(bg)).
		Bold(true).
		Padding(0, 2)
	brand := brandStyle.Render(tb.Brand)
	brandW := lipgloss.Width(brand)

	// Config source on the right (if set)
	var configSource string
	var configSourceW int
	if tb.ConfigSource != "" {
		configStyle := lipgloss.NewStyle().
			Foreground(lipgloss.Color(tb.Theme.TextMuted)).
			Background(lipgloss.Color(bg)).
			Padding(0, 1)
		configSource = configStyle.Render(localizedConfigSource(tb.ConfigSource))
		configSourceW = lipgloss.Width(configSource)
	}

	// Tab styles
	activeStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(tb.Theme.Text)).
		Background(lipgloss.Color(tb.Theme.PrimaryDim)).
		Bold(true).
		Padding(0, 2)

	inactiveStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(tb.Theme.TextMuted)).
		Background(lipgloss.Color(bg)).
		Padding(0, 2)

	// Render each tab
	var parts []string
	for i, tab := range tb.Tabs {
		label := localizedTabLabel(tab)
		if i == tb.ActiveIdx {
			parts = append(parts, activeStyle.Render(label))
		} else {
			parts = append(parts, inactiveStyle.Render(label))
		}
	}

	tabContent := lipgloss.JoinHorizontal(lipgloss.Bottom, parts...)
	tabW := lipgloss.Width(tabContent)

	// Center tabs after brand, with config source on right
	// Use inline max() for zero overhead bounds checking
	// This prevents negative values during terminal resize events
	availW := max(0, width-brandW-configSourceW)
	leftPad := max(max(0, availW-tabW)/2, 1)
	// rightPad = width - brandW - leftPad - tabW - configSourceW, safely calculated
	rightPad := max(max(0, width-brandW-leftPad-tabW-configSourceW), 0)

	padStyle := lipgloss.NewStyle().Background(lipgloss.Color(bg))

	tabRow := brand +
		padStyle.Render(strings.Repeat(" ", leftPad)) +
		tabContent +
		padStyle.Render(strings.Repeat(" ", rightPad)) +
		configSource
	tabRow = reapplyBackground(tabRow, bg)

	// Border line
	borderStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(tb.Theme.PrimaryDim)).
		Background(lipgloss.Color(bg))
	border := borderStyle.Render(strings.Repeat("─", width))

	// Spacer line above tabs — full-width background-colored line
	spacerStyle := lipgloss.NewStyle().
		Background(lipgloss.Color(bg)).
		Width(width)
	spacer := spacerStyle.Render("")

	result := spacer + "\n" + tabRow + "\n" + border
	return reapplyBackground(result, bg)
}

// SetActive sets the active tab by index.
func (tb *TabBar) SetActive(idx int) {
	if idx >= 0 && idx < len(tb.Tabs) {
		tb.ActiveIdx = idx
	}
}

// SetActiveByID sets the active tab by ID.
func (tb *TabBar) SetActiveByID(id string) {
	for i, tab := range tb.Tabs {
		if tab.ID == id {
			tb.ActiveIdx = i
			return
		}
	}
}

// CycleNext moves to the next tab, wrapping around.
func (tb *TabBar) CycleNext() int {
	tb.ActiveIdx = (tb.ActiveIdx + 1) % len(tb.Tabs)
	return tb.ActiveIdx
}

// CyclePrev moves to the previous tab, wrapping around.
func (tb *TabBar) CyclePrev() int {
	tb.ActiveIdx = (tb.ActiveIdx - 1 + len(tb.Tabs)) % len(tb.Tabs)
	return tb.ActiveIdx
}

// ActiveTabID returns the ID of the currently active tab.
func (tb *TabBar) ActiveTabID() string {
	if tb.ActiveIdx >= 0 && tb.ActiveIdx < len(tb.Tabs) {
		return tb.Tabs[tb.ActiveIdx].ID
	}
	return ""
}

// SetConfigSource sets the config source display string.
// Use "global" for global config or "project:Name" for project config.
func (tb *TabBar) SetConfigSource(source string) {
	tb.ConfigSource = source
}

func localizedTabLabel(tab Tab) string {
	var messageID string
	switch tab.ID {
	case "prompt":
		messageID = "misc.tabbar.prompt"
	case "history":
		messageID = "misc.tabbar.history"
	case "usage":
		messageID = "misc.tabbar.usage"
	case "settings":
		messageID = "misc.tabbar.settings"
	default:
		return tab.Label
	}
	return i18n.T(messageID)
}

func localizedConfigSource(source string) string {
	switch source {
	case "global":
		return i18n.T("misc.tabbar.global")
	case "🌍 Global":
		return "🌍 " + i18n.T("misc.tabbar.global")
	default:
		return source
	}
}

// Height returns the fixed height of the tab bar (spacer + tabs + border).
const TabBarHeight = 3

// reapplyBackground replaces ANSI resets with reset+background to ensure
// background continuity across styled segments.
func reapplyBackground(text string, bgColor string) string {
	style := lipgloss.NewStyle().Background(lipgloss.Color(bgColor))
	rendered := style.Render(" ")
	before, _, ok := strings.Cut(rendered, " ")
	if !ok {
		return text
	}
	bgSeq := before

	replaced := strings.ReplaceAll(text, "\x1b[0m", "\x1b[0m"+bgSeq)
	replaced = strings.ReplaceAll(replaced, "\x1b[m", "\x1b[m"+bgSeq)
	return bgSeq + replaced
}
