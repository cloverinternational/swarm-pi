package chat

import (
	"regexp"
	"strings"
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/i18n"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/ui/palette"
	zone "github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/ui/zone"
	"github.com/mattn/go-runewidth"

	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/chat/toolrender/shared"
)

func wordWrapText(text string, width int) []string {
	if width <= 0 {
		return []string{text}
	}

	// Handle explicit newlines first
	paragraphs := strings.Split(text, "\n")
	var allLines []string

	for _, paragraph := range paragraphs {
		if paragraph == "" {
			allLines = append(allLines, "")
			continue
		}

		words := strings.Fields(paragraph)
		if len(words) == 0 {
			allLines = append(allLines, "")
			continue
		}

		var currentLine strings.Builder
		currentLen := 0

		for i, word := range words {
			// Use lipgloss.Width for accurate terminal width (handles wide chars)
			wordLen := lipgloss.Width(word)

			// If word itself is longer than width, break it
			if wordLen > width {
				// Flush current line if not empty
				if currentLen > 0 {
					allLines = append(allLines, currentLine.String())
					currentLine.Reset()
					currentLen = 0
				}
				// Break long word into chunks by rune (best effort for wide chars)
				runes := []rune(word)
				var chunk strings.Builder
				chunkWidth := 0
				for _, r := range runes {
					runeWidth := lipgloss.Width(string(r))
					if chunkWidth+runeWidth > width && chunk.Len() > 0 {
						allLines = append(allLines, chunk.String())
						chunk.Reset()
						chunkWidth = 0
					}
					chunk.WriteRune(r)
					chunkWidth += runeWidth
				}
				if chunk.Len() > 0 {
					currentLine.WriteString(chunk.String())
					currentLen = chunkWidth
				}
				continue
			}

			// Check if word fits on current line
			spaceNeeded := wordLen
			if currentLen > 0 {
				spaceNeeded++ // Account for space before word
			}

			if currentLen+spaceNeeded > width {
				// Word doesn't fit, start new line
				if currentLen > 0 {
					allLines = append(allLines, currentLine.String())
					currentLine.Reset()
					currentLen = 0
				}
			}

			// Add word to current line
			if currentLen > 0 {
				currentLine.WriteString(" ")
				currentLen++
			}
			currentLine.WriteString(word)
			currentLen += wordLen

			// If this is the last word, flush the line
			if i == len(words)-1 && currentLen > 0 {
				allLines = append(allLines, currentLine.String())
			}
		}
	}

	if len(allLines) == 0 {
		return []string{""}
	}

	return allLines
}

// ============================================================================
// THEME SYSTEM
// ============================================================================

// ============================================================================
// THEME SYSTEM
// ============================================================================

// Theme defines all colors in the UI kit
type Theme struct {
	// Primary colors
	Primary    string
	PrimaryDim string
	Secondary  string
	Accent     string

	// Status colors
	Success string
	Warning string
	Error   string
	Info    string

	// Neutral
	BG        string // Main background
	BGLight   string // Elevated surface
	BGLighter string // Hover/active states
	Border    string // Subtle borders
	Text      string // Primary text
	TextDim   string // Secondary text
	TextMuted string // Disabled/hint text

	// User message background — must be clearly distinguishable from BG
	// (~30+ RGB units per channel above BG so the highlight bar is actually visible).
	// Claude Code uses rgb(55,55,55) for dark terminals; each theme needs its own tuned value.
	UserMsgBG        string
	UserMsgBGFocused string
}

var DefaultTheme = Theme{
	Primary:    palette.Accent,
	PrimaryDim: palette.AccentDim,
	Secondary:  palette.AccentSoft,
	Accent:     palette.Teal,

	Success: palette.Success,
	Warning: palette.Warning,
	Error:   palette.Error,
	Info:    palette.Info,

	BG:        palette.Surface,
	BGLight:   palette.Panel,
	BGLighter: palette.PanelAlt,
	Border:    palette.Border,
	Text:      palette.Text,
	TextDim:   palette.TextDim,
	TextMuted: palette.TextMuted,

	UserMsgBG:        "#1F283A", // clearly elevated above BG #0E1118 (+17/+16/+34 per channel)
	UserMsgBGFocused: "#263348",
}

// AutoVacTheme is a retro green terminal aesthetic for the AutoVac Easter egg mode.
// Inspired by classic CRT monitors and Asimov's Multivac computer from "The Last Question".
// Green phosphor on black, with amber accents for a true vintage feel.
var AutoVacTheme = Theme{
	Primary:    "#00FF41", // Phosphor green - classic CRT glow
	PrimaryDim: "#00AA2A", // Dimmer green for secondary elements
	Secondary:  "#00CC33", // Soft green
	Accent:     "#FFB000", // Amber accent - old terminal highlight color

	Success: "#00FF41", // Green for success
	Warning: "#FFB000", // Amber for warnings
	Error:   "#FF3333", // Red for errors (only color that breaks the aesthetic)
	Info:    "#00CC33", // Green for info

	BG:        "#0A0A0A", // Near black - CRT bezel
	BGLight:   "#0D1A0D", // Very dark green tint
	BGLighter: "#0F200F", // Slightly lighter for hover states
	Border:    "#003311", // Dark green border
	Text:      "#00FF41", // Green text
	TextDim:   "#00AA2A", // Dimmer green
	TextMuted: "#004411", // Very dim green for muted text

	UserMsgBG:        "#102010", // dark green surface clearly above #0A0A0A
	UserMsgBGFocused: "#152815",
}

// ============================================================================
// NAMED THEME PRESETS
// ============================================================================

// ThemePreset describes a named colour palette entry
type ThemePreset struct {
	Name        string
	Description string
	Theme       Theme
}

// ThemePresets holds all available named palettes in display order.
var ThemePresets = []ThemePreset{
	{
		Name:        "SwarmCode",
		Description: i18n.T("classic_chat.theme.swarmcode"),
		Theme:       DefaultTheme,
	},
	{
		Name:        "Midnight",
		Description: i18n.T("classic_chat.theme.midnight"),
		Theme: Theme{
			Primary:          "#A78BFA",
			PrimaryDim:       "#6D28D9",
			Secondary:        "#C4B5FD",
			Accent:           "#818CF8",
			Success:          "#34D399",
			Warning:          "#FBBF24",
			Error:            "#F87171",
			Info:             "#60A5FA",
			BG:               "#0D0D1A",
			BGLight:          "#12122A",
			BGLighter:        "#1A1A35",
			Border:           "#2D2B55",
			Text:             "#E2E8F0",
			TextDim:          "#94A3B8",
			TextMuted:        "#4A4A70",
			UserMsgBG:        "#1F1F40",
			UserMsgBGFocused: "#262652",
		},
	},
	{
		Name:        "Nord",
		Description: i18n.T("classic_chat.theme.nord"),
		Theme: Theme{
			Primary:          "#88C0D0",
			PrimaryDim:       "#5E81AC",
			Secondary:        "#81A1C1",
			Accent:           "#8FBCBB",
			Success:          "#A3BE8C",
			Warning:          "#EBCB8B",
			Error:            "#BF616A",
			Info:             "#81A1C1",
			BG:               "#2E3440",
			BGLight:          "#3B4252",
			BGLighter:        "#434C5E",
			Border:           "#4C566A",
			Text:             "#ECEFF4",
			TextDim:          "#D8DEE9",
			TextMuted:        "#616E88",
			UserMsgBG:        "#3E4A5C",
			UserMsgBGFocused: "#475470",
		},
	},
	{
		Name:        "Gruvbox Dark",
		Description: i18n.T("classic_chat.theme.gruvbox"),
		Theme: Theme{
			Primary:          "#FABD2F",
			PrimaryDim:       "#D79921",
			Secondary:        "#FE8019",
			Accent:           "#8EC07C",
			Success:          "#B8BB26",
			Warning:          "#FABD2F",
			Error:            "#FB4934",
			Info:             "#83A598",
			BG:               "#1D2021",
			BGLight:          "#282828",
			BGLighter:        "#3C3836",
			Border:           "#504945",
			Text:             "#EBDBB2",
			TextDim:          "#A89984",
			TextMuted:        "#665C54",
			UserMsgBG:        "#32302F",
			UserMsgBGFocused: "#3C3836",
		},
	},
	{
		Name:        "Dracula",
		Description: i18n.T("classic_chat.theme.dracula"),
		Theme: Theme{
			Primary:          "#BD93F9",
			PrimaryDim:       "#6272A4",
			Secondary:        "#FF79C6",
			Accent:           "#50FA7B",
			Success:          "#50FA7B",
			Warning:          "#FFB86C",
			Error:            "#FF5555",
			Info:             "#8BE9FD",
			BG:               "#282A36",
			BGLight:          "#323443",
			BGLighter:        "#44475A",
			Border:           "#6272A4",
			Text:             "#F8F8F2",
			TextDim:          "#BFBFBF",
			TextMuted:        "#6272A4",
			UserMsgBG:        "#373A4E",
			UserMsgBGFocused: "#414462",
		},
	},
	{
		Name:        "Monokai",
		Description: i18n.T("classic_chat.theme.monokai"),
		Theme: Theme{
			Primary:          "#F92672",
			PrimaryDim:       "#C4265E",
			Secondary:        "#FD971F",
			Accent:           "#A6E22E",
			Success:          "#A6E22E",
			Warning:          "#E6DB74",
			Error:            "#F92672",
			Info:             "#66D9E8",
			BG:               "#272822",
			BGLight:          "#3E3D32",
			BGLighter:        "#49483E",
			Border:           "#75715E",
			Text:             "#F8F8F2",
			TextDim:          "#CFCFC2",
			TextMuted:        "#75715E",
			UserMsgBG:        "#363830",
			UserMsgBGFocused: "#42443C",
		},
	},
	{
		Name:        "Catppuccin",
		Description: i18n.T("classic_chat.theme.catppuccin"),
		Theme: Theme{
			Primary:          "#CBA6F7",
			PrimaryDim:       "#9471BF",
			Secondary:        "#F5C2E7",
			Accent:           "#94E2D5",
			Success:          "#A6E3A1",
			Warning:          "#FAB387",
			Error:            "#F38BA8",
			Info:             "#89B4FA",
			BG:               "#1E1E2E",
			BGLight:          "#27273E",
			BGLighter:        "#313244",
			Border:           "#45475A",
			Text:             "#CDD6F4",
			TextDim:          "#BAC2DE",
			TextMuted:        "#6C7086",
			UserMsgBG:        "#2E2E48",
			UserMsgBGFocused: "#383858",
		},
	},
	{
		Name:        "Tokyo Night",
		Description: i18n.T("classic_chat.theme.tokyo_night"),
		Theme: Theme{
			Primary:          "#7AA2F7",
			PrimaryDim:       "#3D59A1",
			Secondary:        "#BB9AF7",
			Accent:           "#7DCFFF",
			Success:          "#9ECE6A",
			Warning:          "#E0AF68",
			Error:            "#F7768E",
			Info:             "#7AA2F7",
			BG:               "#1A1B26",
			BGLight:          "#24283B",
			BGLighter:        "#2F3549",
			Border:           "#3B4261",
			Text:             "#C0CAF5",
			TextDim:          "#9AA5CE",
			TextMuted:        "#565F89",
			UserMsgBG:        "#252840",
			UserMsgBGFocused: "#2E3254",
		},
	},
	{
		Name:        "Solarized Dark",
		Description: i18n.T("classic_chat.theme.solarized"),
		Theme: Theme{
			Primary:          "#268BD2",
			PrimaryDim:       "#1A5F8F",
			Secondary:        "#2AA198",
			Accent:           "#859900",
			Success:          "#859900",
			Warning:          "#CB4B16",
			Error:            "#DC322F",
			Info:             "#268BD2",
			BG:               "#002B36",
			BGLight:          "#073642",
			BGLighter:        "#0D4352",
			Border:           "#586E75",
			Text:             "#FDF6E3",
			TextDim:          "#93A1A1",
			TextMuted:        "#586E75",
			UserMsgBG:        "#0C3B48",
			UserMsgBGFocused: "#134756",
		},
	},
	{
		Name:        "Horizon",
		Description: i18n.T("classic_chat.theme.horizon"),
		Theme: Theme{
			Primary:          "#E95678",
			PrimaryDim:       "#B33559",
			Secondary:        "#FAB795",
			Accent:           "#25B0BC",
			Success:          "#29D398",
			Warning:          "#FAB795",
			Error:            "#E95678",
			Info:             "#26BBD9",
			BG:               "#1C1E26",
			BGLight:          "#232530",
			BGLighter:        "#2E303E",
			Border:           "#3D3F4E",
			Text:             "#D5D8DA",
			TextDim:          "#ACADB1",
			TextMuted:        "#4A4C59",
			UserMsgBG:        "#2B2E40",
			UserMsgBGFocused: "#363952",
		},
	},
	{
		Name:        "Light",
		Description: i18n.T("classic_chat.theme.light"),
		Theme: Theme{
			Primary:          "#2563EB",
			PrimaryDim:       "#1D4ED8",
			Secondary:        "#7C3AED",
			Accent:           "#0891B2",
			Success:          "#059669",
			Warning:          "#D97706",
			Error:            "#DC2626",
			Info:             "#2563EB",
			BG:               "#FFFFFF",
			BGLight:          "#F3F4F6",
			BGLighter:        "#E5E7EB",
			Border:           "#D1D5DB",
			Text:             "#111827", // near-black — visible on white bg
			TextDim:          "#4B5563",
			TextMuted:        "#9CA3AF",
			UserMsgBG:        "#EFF6FF",
			UserMsgBGFocused: "#DBEAFE",
		},
	},
	{
		Name:        "GitHub Light",
		Description: i18n.T("classic_chat.theme.github_light"),
		Theme: Theme{
			Primary:          "#0969DA",
			PrimaryDim:       "#0550AE",
			Secondary:        "#8250DF",
			Accent:           "#0969DA",
			Success:          "#1A7F37",
			Warning:          "#9A6700",
			Error:            "#CF222E",
			Info:             "#0969DA",
			BG:               "#FFFFFF",
			BGLight:          "#F6F8FA",
			BGLighter:        "#EAEEF2",
			Border:           "#D0D7DE",
			Text:             "#1F2328", // GitHub's primary text colour
			TextDim:          "#57606A",
			TextMuted:        "#8C959F",
			UserMsgBG:        "#DDF4FF",
			UserMsgBGFocused: "#B6E3FF",
		},
	},
}

// ThemeByName returns the Theme for the given preset name.
// Returns DefaultTheme if the name is not recognised.
func ThemeByName(name string) Theme {
	for _, p := range ThemePresets {
		if p.Name == name {
			return p.Theme
		}
	}
	return DefaultTheme
}

// AdaptThemeForTerminal adjusts a theme so its text and neutral-surface colors
// are legible regardless of the terminal's background colour.
//
// When hasDark is true (dark terminal) the theme is returned unchanged.
// When hasDark is false (light/white terminal) only the body-text and neutral
// surface colours are overridden with readable dark values; accent/status
// colours (Primary, Success, Error, Warning, Info…) are kept so every theme
// retains its visual identity on light terminals too.
func AdaptThemeForTerminal(t Theme, hasDark bool) Theme {
	if hasDark {
		return t
	}
	adapted := t
	// Body text: force to near-black so all themes are readable on white/light bg.
	adapted.Text = "#111827"
	adapted.TextDim = "#374151"
	adapted.TextMuted = "#6B7280"
	// Neutral surfaces: replace very-dark panel colours with light equivalents.
	adapted.BG = "#FFFFFF"
	adapted.BGLight = "#F3F4F6"
	adapted.BGLighter = "#E5E7EB"
	adapted.Border = "#D1D5DB"
	// Message backgrounds: light-blue tint keeps user/assistant regions distinct.
	adapted.UserMsgBG = "#EFF6FF"
	adapted.UserMsgBGFocused = "#DBEAFE"
	return adapted
}

// ============================================================================
// LAYOUT PRIMITIVES
// ============================================================================

// Bounds represents a bounding box with constraints
type Bounds struct {
	Width    int
	Height   int
	MinWidth int
	MaxWidth int
}

// Constrain applies min/max constraints
func (b Bounds) Constrain(w int) int {
	if b.MinWidth > 0 && w < b.MinWidth {
		return b.MinWidth
	}
	if b.MaxWidth > 0 && w > b.MaxWidth {
		return b.MaxWidth
	}
	return w
}

// Spacing defines consistent spacing values
type Spacing struct {
	None int
	XS   int
	SM   int
	MD   int
	LG   int
	XL   int
	XXL  int
}

var Space = Spacing{
	None: 0,
	XS:   1,
	SM:   2,
	MD:   3,
	LG:   4,
	XL:   6,
	XXL:  8,
}

// ============================================================================
// BASE COMPONENTS
// ============================================================================

// Box is a container with padding, margin, and borders
type Box struct {
	Content     string
	Width       int
	Height      int
	Padding     [4]int // top, right, bottom, left
	Margin      [4]int
	Background  string
	BorderStyle lipgloss.Border
	BorderColor string
	Align       lipgloss.Position
	VAlign      lipgloss.Position
}

func (b Box) Render() string {
	style := lipgloss.NewStyle()

	if b.Width > 0 {
		style = style.Width(b.Width)
	}
	if b.Height > 0 {
		style = style.Height(b.Height)
	}

	// Padding
	style = style.PaddingTop(b.Padding[0]).
		PaddingRight(b.Padding[1]).
		PaddingBottom(b.Padding[2]).
		PaddingLeft(b.Padding[3])

	// Margin
	style = style.MarginTop(b.Margin[0]).
		MarginRight(b.Margin[1]).
		MarginBottom(b.Margin[2]).
		MarginLeft(b.Margin[3])

	if b.Background != "" {
		style = style.Background(lipgloss.Color(b.Background))
	}

	if b.BorderStyle.Top != "" {
		style = style.Border(b.BorderStyle)
		if b.BorderColor != "" {
			style = style.BorderForeground(lipgloss.Color(b.BorderColor))
		}
	}

	if b.Align != 0 {
		style = style.Align(b.Align)
	}
	if b.VAlign != 0 {
		style = style.AlignVertical(b.VAlign)
	}

	return style.Render(b.Content)
}

// Text creates styled text with consistent API
type Text struct {
	Content string
	Color   string
	Bold    bool
	Italic  bool
	Faint   bool
}

func (t Text) Render() string {
	style := lipgloss.NewStyle()
	if t.Color != "" {
		style = style.Foreground(lipgloss.Color(t.Color))
	}
	if t.Bold {
		style = style.Bold(true)
	}
	if t.Italic {
		style = style.Italic(true)
	}
	if t.Faint {
		style = style.Faint(true)
	}
	return style.Render(t.Content)
}

// T is a shorthand for creating text
func T(content string, color string, opts ...string) string {
	t := Text{Content: content, Color: color}
	for _, opt := range opts {
		switch opt {
		case "bold":
			t.Bold = true
		case "italic":
			t.Italic = true
		case "dim", "faint":
			t.Faint = true
		}
	}
	return t.Render()
}

// ============================================================================
// FLEX LAYOUT
// ============================================================================

// FlexDirection defines layout direction
type FlexDirection int

const (
	FlexRow FlexDirection = iota
	FlexColumn
)

// FlexAlign defines alignment
type FlexAlign int

const (
	AlignStart FlexAlign = iota
	AlignCenter
	AlignEnd
	AlignStretch
)

// Flex is a flexbox-like container
type Flex struct {
	Children  []string
	Direction FlexDirection
	Gap       int
	Align     FlexAlign
	Width     int
	Height    int
}

func (f Flex) Render() string {
	if len(f.Children) == 0 {
		return ""
	}

	// Add gaps between children
	var items []string
	for i, child := range f.Children {
		items = append(items, child)
		if f.Gap > 0 && i < len(f.Children)-1 {
			if f.Direction == FlexRow {
				items = append(items, strings.Repeat(" ", f.Gap))
			}
		}
	}

	var result string
	align := lipgloss.Left
	switch f.Align {
	case AlignCenter:
		align = lipgloss.Center
	case AlignEnd:
		align = lipgloss.Right
	}

	if f.Direction == FlexRow {
		result = lipgloss.JoinHorizontal(align, items...)
	} else {
		if f.Gap > 0 {
			// Re-insert gaps as newlines for column
			items = nil
			for i, child := range f.Children {
				items = append(items, child)
				if i < len(f.Children)-1 {
					items = append(items, strings.Repeat("\n", f.Gap))
				}
			}
		}
		result = lipgloss.JoinVertical(align, items...)
	}

	if f.Width > 0 || f.Height > 0 {
		style := lipgloss.NewStyle()
		if f.Width > 0 {
			style = style.Width(f.Width).Align(align)
		}
		if f.Height > 0 {
			style = style.Height(f.Height)
		}
		result = style.Render(result)
	}

	return result
}

// ============================================================================
// BUTTON COMPONENT
// ============================================================================

// ButtonVariant defines button style variants
type ButtonVariant int

const (
	ButtonDefault ButtonVariant = iota
	ButtonPrimary
	ButtonGhost
)

// Button is a clickable button component
type Button struct {
	ID       string // Unique ID for bubblezone click detection
	Label    string
	Shortcut string
	Selected bool
	Disabled bool
	Variant  ButtonVariant
	Width    int
	Theme    Theme
}

func (b Button) Render() string {
	th := b.Theme

	// Colors based on state
	bgColor := th.BGLight
	borderColor := th.Border
	textColor := th.TextDim

	if b.Selected {
		bgColor = th.BGLighter
		borderColor = th.Primary
		textColor = th.Text
	}
	if b.Disabled {
		bgColor = th.BG
		borderColor = th.Border
		textColor = th.TextMuted
	}

	width := b.Width
	if width == 0 {
		width = 16
	}

	// Enforce minimum width to prevent layout breaking
	if width < 8 {
		width = 8
	}

	// Simple single-style approach
	innerW := width - 4 // account for border + padding
	if innerW < 2 {
		innerW = 2
	}

	// Center label with truncation if needed
	label := b.Label
	if len(label) > innerW {
		if innerW > 1 {
			label = label[:innerW-1] + "…"
		} else {
			label = label[:innerW]
		}
	}
	labelPad := (innerW - len(label)) / 2
	if labelPad < 0 {
		labelPad = 0
	}
	rightPad := innerW - labelPad - len(label)
	if rightPad < 0 {
		rightPad = 0
	}
	labelLine := strings.Repeat(" ", labelPad) + label + strings.Repeat(" ", rightPad)

	// Center shortcut with truncation if needed
	shortcutLine := ""
	if b.Shortcut != "" {
		shortcut := b.Shortcut
		if len(shortcut) > innerW {
			if innerW > 1 {
				shortcut = shortcut[:innerW-1] + "…"
			} else {
				shortcut = shortcut[:innerW]
			}
		}
		sPad := (innerW - len(shortcut)) / 2
		if sPad < 0 {
			sPad = 0
		}
		sRightPad := innerW - sPad - len(shortcut)
		if sRightPad < 0 {
			sRightPad = 0
		}
		shortcutLine = strings.Repeat(" ", sPad) + shortcut + strings.Repeat(" ", sRightPad)
	}

	// Build content block
	content := labelLine + "\n" + shortcutLine

	// Single style for entire button
	style := lipgloss.NewStyle().
		Width(width).
		Foreground(lipgloss.Color(textColor))

	if bgColor != "" {
		style = style.Background(lipgloss.Color(bgColor))
	}

	rendered := style.
		Bold(b.Selected).
		Padding(1, 1).
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color(borderColor)).
		Render(content)

	// Wrap with zone marker if ID is provided
	if b.ID != "" {
		return zone.Mark(b.ID, rendered)
	}
	return rendered
}

// ============================================================================
// CARD COMPONENT
// ============================================================================

// Card is a container with elevation
type Card struct {
	Content    string
	Title      string
	Subtitle   string
	Width      int
	Selected   bool
	Elevated   bool
	ShowBorder bool
	Theme      Theme
}

func (c Card) Render() string {
	th := c.Theme

	bgColor := th.BG
	if c.Elevated || c.Selected {
		bgColor = th.BGLight
	}

	var parts []string

	if c.Title != "" {
		titleColor := th.TextDim
		if c.Selected {
			titleColor = th.Text
		}
		title := lipgloss.NewStyle().
			Foreground(lipgloss.Color(titleColor)).
			Bold(true).
			Render(c.Title)
		parts = append(parts, title)
	}

	if c.Subtitle != "" {
		subtitle := lipgloss.NewStyle().
			Foreground(lipgloss.Color(th.TextMuted)).
			Render(c.Subtitle)
		parts = append(parts, subtitle)
	}

	if c.Content != "" {
		parts = append(parts, c.Content)
	}

	inner := lipgloss.JoinVertical(lipgloss.Left, parts...)

	style := lipgloss.NewStyle().
		Padding(1, 2)

	if bgColor != "" {
		style = style.Background(lipgloss.Color(bgColor))
	}

	if c.Width > 0 {
		style = style.Width(c.Width)
	}

	if c.ShowBorder || c.Selected {
		borderColor := th.Border
		if c.Selected {
			borderColor = th.Primary
		}
		style = style.
			BorderLeft(true).
			BorderStyle(lipgloss.NormalBorder()).
			BorderForeground(lipgloss.Color(borderColor))
	}

	return style.Render(inner)
}

// ============================================================================
// LIST ITEM COMPONENT
// ============================================================================

// ListItem is a single row in a list with optional preview
type ListItem struct {
	ID       string // Unique ID for bubblezone click detection
	Icon     string
	Label    string
	Meta     string
	Preview  string // Optional preview text
	Selected bool
	Width    int
	Theme    Theme
	Padding  int // Padding around item (default 1)
}

func (l ListItem) Render() string {
	th := l.Theme
	w := l.Width
	if w == 0 {
		w = 60
	}

	pad := l.Padding
	if pad == 0 {
		pad = 1
	}

	// Icon
	icon := l.Icon
	if icon == "" {
		icon = "●"
	}
	iconStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(th.TextMuted)).
		Width(2)
	if l.Selected {
		iconStyle = iconStyle.Foreground(lipgloss.Color(th.Primary))
	}
	iconStr := iconStyle.Render(icon)

	// Label
	labelColor := th.TextDim
	if l.Selected {
		labelColor = th.Text
	}
	maxLabelW := w - 20
	label := lipgloss.NewStyle().
		Foreground(lipgloss.Color(labelColor)).
		Bold(l.Selected).
		Width(maxLabelW).
		Render(truncateStr(l.Label, maxLabelW))

	// Meta (right-aligned)
	meta := lipgloss.NewStyle().
		Foreground(lipgloss.Color(th.TextMuted)).
		Render(l.Meta)

	// Calculate spacing
	leftPart := iconStr + " " + label
	leftW := lipgloss.Width(leftPart)
	metaW := lipgloss.Width(meta)
	gap := w - leftW - metaW - 4
	if gap < 1 {
		gap = 1
	}

	row := leftPart + strings.Repeat(" ", gap) + meta

	// Add preview if provided (on second line)
	var fullContent string
	if l.Preview != "" {
		previewColor := th.TextMuted
		previewText := lipgloss.NewStyle().
			Foreground(lipgloss.Color(previewColor)).
			Italic(true).
			Render(truncateStr(l.Preview, w-6))
		fullContent = lipgloss.JoinVertical(lipgloss.Left, row, "  "+previewText)
	} else {
		fullContent = row
	}

	// Container with improved styling
	bgColor := th.BG
	borderColor := th.Border
	if l.Selected {
		bgColor = th.BGLight
		borderColor = th.Primary
	}

	style := lipgloss.NewStyle().
		Width(w).
		Padding(pad, 2)

	if bgColor != "" {
		style = style.Background(lipgloss.Color(bgColor))
	}

	// Add left border for selected items and hover effect
	if l.Selected {
		style = style.
			BorderLeft(true).
			BorderStyle(lipgloss.NormalBorder()).
			BorderForeground(lipgloss.Color(borderColor))
	}

	rendered := style.Render(fullContent)

	// Wrap with zone marker if ID is provided
	if l.ID != "" {
		return zone.Mark(l.ID, rendered)
	}
	return rendered
}

// ============================================================================
// INPUT FIELD COMPONENT
// ============================================================================

// InputField is a text input component
type InputField struct {
	Value       string
	Placeholder string
	Cursor      int
	Focused     bool
	Width       int
	Theme       Theme
}

func (i InputField) Render() string {
	th := i.Theme
	w := i.Width - 4 // Account for border

	// Prompt indicator
	promptColor := th.TextMuted
	if i.Focused {
		promptColor = th.Primary
	}
	prompt := lipgloss.NewStyle().
		Foreground(lipgloss.Color(promptColor)).
		Bold(true).
		Render("›")

	// Content with cursor
	var display string
	if i.Value == "" && !i.Focused {
		display = lipgloss.NewStyle().
			Foreground(lipgloss.Color(th.TextMuted)).
			Italic(true).
			Render(i.Placeholder)
	} else {
		runes := []rune(i.Value)
		cursor := i.Cursor
		if cursor > len(runes) {
			cursor = len(runes)
		}

		before := string(runes[:cursor])

		if i.Focused {
			cursorStyle := lipgloss.NewStyle().
				Background(lipgloss.Color(th.Primary)).
				Foreground(lipgloss.Color(th.BG))

			if cursor < len(runes) {
				cursorChar := string(runes[cursor])
				after := string(runes[cursor+1:])
				display = before + cursorStyle.Render(cursorChar) + after
			} else {
				display = before + cursorStyle.Render(" ")
			}
		} else {
			display = i.Value
		}
	}

	content := prompt + "  " + display

	// Box style
	borderColor := th.Border
	if i.Focused {
		borderColor = th.Primary
	}

	style := lipgloss.NewStyle().
		Width(w).
		Padding(1, 2).
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color(borderColor))

	return style.Render(content)
}

// ============================================================================
// MESSAGE BUBBLE COMPONENT
// ============================================================================

// MessageBubble displays a chat message
// MessageBubble moved to app_conversations_messages.go

// ============================================================================
// KEY HINT COMPONENT
// ============================================================================

// KeyHint displays a keyboard shortcut hint
func KeyHint(key string, action string, th Theme) string {
	k := lipgloss.NewStyle().
		Background(lipgloss.Color(th.BGLighter)).
		Foreground(lipgloss.Color(th.Text)).
		Padding(0, 1).
		Render(key)

	a := lipgloss.NewStyle().
		Foreground(lipgloss.Color(th.TextMuted)).
		Render(" " + action)

	return k + a
}

// ============================================================================
// DIVIDER COMPONENT
// ============================================================================

// Divider creates a horizontal line
type Divider struct {
	Width int
	Theme Theme
}

func (d Divider) Render() string {
	return lipgloss.NewStyle().
		Foreground(lipgloss.Color(d.Theme.Border)).
		Background(lipgloss.Color(d.Theme.BG)).
		Render(strings.Repeat("─", d.Width))
}

// ============================================================================
// SCREEN CONTAINER
// ============================================================================

// ScreenContainer is a full-screen container
type ScreenContainer struct {
	Content string
	Width   int
	Height  int
	Theme   Theme
}

func (s ScreenContainer) Render() string {
	return lipgloss.NewStyle().
		Width(s.Width).
		Height(s.Height).
		Background(lipgloss.Color(s.Theme.BG)).
		Render(s.Content)
}

// ============================================================================
// CENTER LAYOUT
// ============================================================================

// Center centers content within bounds
type Center struct {
	Content string
	Width   int
	Height  int
}

func (c Center) Render() string {
	style := lipgloss.NewStyle()
	if c.Width > 0 {
		style = style.Width(c.Width).Align(lipgloss.Center)
	}
	if c.Height > 0 {
		style = style.Height(c.Height).AlignVertical(lipgloss.Center)
	}
	return style.Render(c.Content)
}

// ============================================================================
// HELPERS
// ============================================================================

func truncateStr(s string, max int) string {
	if max <= 0 {
		return ""
	}
	if len(s) <= max {
		return s
	}
	if max <= 3 {
		return s[:max]
	}
	return s[:max-1] + "…"
}

// truncateToVisualWidth truncates a string to fit within the given visual width,
// properly handling multi-byte Unicode characters. It returns the truncated string.
func truncateToVisualWidth(s string, maxVisualWidth int) string {
	if maxVisualWidth <= 0 {
		return ""
	}

	currentWidth := 0
	for i, r := range s {
		charWidth := runewidth.RuneWidth(r)
		if currentWidth+charWidth > maxVisualWidth {
			return s[:i]
		}
		currentWidth += charWidth
	}
	return s
}

func wrapText(text string, width int) []string {
	if width <= 0 {
		return []string{text}
	}

	// Preserve newlines - split by newline first
	inputLines := strings.Split(text, "\n")
	var result []string

	for _, line := range inputLines {
		// For each line, word wrap it
		if len(line) == 0 {
			result = append(result, "")
			continue
		}

		words := strings.Fields(line)
		if len(words) == 0 {
			result = append(result, "")
			continue
		}

		var current strings.Builder
		currentVisualWidth := 0
		for _, word := range words {
			wordVisualWidth := lipgloss.Width(word)
			// Check if word is too long - handle gracefully by breaking it
			if wordVisualWidth > width {
				// Word is too long for the width
				if current.Len() > 0 {
					// Flush current line first
					result = append(result, current.String())
					current.Reset()
					currentVisualWidth = 0
				}
				// Break long word into chunks using visual width
				remaining := word
				for lipgloss.Width(remaining) > width {
					chunk := truncateToVisualWidth(remaining, width)
					result = append(result, chunk)
					remaining = remaining[len(chunk):]
				}
				if len(remaining) > 0 {
					current.WriteString(remaining)
					currentVisualWidth = lipgloss.Width(remaining)
				}
			} else if current.Len() == 0 {
				// Start of new line
				current.WriteString(word)
				currentVisualWidth = wordVisualWidth
			} else if currentVisualWidth+1+wordVisualWidth <= width {
				// Word fits on current line with space
				current.WriteString(" ")
				current.WriteString(word)
				currentVisualWidth += 1 + wordVisualWidth
			} else {
				// Word doesn't fit - start new line
				result = append(result, current.String())
				current.Reset()
				current.WriteString(word)
				currentVisualWidth = wordVisualWidth
			}
		}

		if current.Len() > 0 {
			result = append(result, current.String())
		}
	}

	if len(result) == 0 {
		return []string{""}
	}

	return result
}

// wrapTextWithIndent wraps text while preserving indentation for continuation lines.
// The indent string is prepended to all lines after the first one.
// STRICT VERSION: Ensures text never wraps around - always respects indent
func wrapTextWithIndent(text string, width int, indent string) []string {
	if width <= 0 {
		return []string{text}
	}

	// Calculate indent length (strip ANSI codes for accurate measurement)
	indentLen := len(stripANSI(indent))

	// First line gets full width
	firstLineWidth := width
	// Continuation lines get reduced width (total width minus indent)
	continuationWidth := width - indentLen
	if continuationWidth < 10 {
		continuationWidth = 10 // Minimum usable width
	}

	// Split text by newlines first to preserve explicit line breaks
	inputLines := strings.Split(text, "\n")
	var result []string

	for lineIdx, line := range inputLines {
		if lineIdx == 0 {
			// First line of entire text - use full width
			wrapped := wrapText(line, firstLineWidth)
			if len(wrapped) > 0 {
				result = append(result, wrapped[0])
				// Remaining wrapped parts get continuation indent
				for i := 1; i < len(wrapped); i++ {
					result = append(result, indent+wrapped[i])
				}
			}
		} else {
			// Subsequent lines - wrap with continuation width and add indent
			wrapped := wrapText(line, continuationWidth)
			for _, w := range wrapped {
				result = append(result, indent+w)
			}
		}
	}

	if len(result) == 0 {
		return []string{""}
	}

	return result
}

// Pad creates horizontal padding
func Pad(n int) string {
	return strings.Repeat(" ", n)
}

// VPad creates vertical padding (newlines)
func VPad(n int) string {
	return strings.Repeat("\n", n)
}

// ============================================================================
// CHAT VIEWPORT COMPONENT (Bubbletea v2 compatible)
// ============================================================================

// ChatViewport is a scrollable viewport for chat messages
type ChatViewport struct {
	width   int
	height  int
	yOffset int
	content string
	lines   []string
}

// NewChatViewport creates a new chat viewport
func NewChatViewport(width, height int) *ChatViewport {
	return &ChatViewport{
		width:   width,
		height:  height,
		yOffset: 0,
		lines:   []string{},
	}
}

// SetContent sets the viewport content and clamps the offset
// to ensure it's valid for the new content
func (v *ChatViewport) SetContent(content string) {
	v.content = content
	v.lines = strings.Split(content, "\n")
	v.clampOffset() // Re-clamp offset with the new line count
}

// SetSize sets the viewport dimensions
// Note: Does NOT call clampOffset() here because the offset will be clamped
// after SetContent() is called. Calling it here then calling it again after
// SetContent() causes the offset to be calculated with stale line counts.
func (v *ChatViewport) SetSize(width, height int) {
	v.width = width
	v.height = height
	// Don't clamp here - let SetContent() handle it via clampOffset() after updating lines
}

// GotoBottom scrolls to the bottom
func (v *ChatViewport) GotoBottom() {
	maxOffset := len(v.lines) - v.height
	if maxOffset < 0 {
		v.yOffset = 0
	} else {
		v.yOffset = maxOffset
	}
}

// ScrollUp scrolls up by n lines
func (v *ChatViewport) ScrollUp(n int) {
	v.yOffset -= n
	v.clampOffset()
}

// ScrollDown scrolls down by n lines
func (v *ChatViewport) ScrollDown(n int) {
	v.yOffset += n
	v.clampOffset()
}

// clampOffset ensures yOffset is within valid bounds
func (v *ChatViewport) clampOffset() {
	maxOffset := len(v.lines) - v.height
	if maxOffset < 0 {
		maxOffset = 0
	}

	if v.yOffset < 0 {
		v.yOffset = 0
	}
	if v.yOffset > maxOffset {
		v.yOffset = maxOffset
	}
}

// Update handles bubbletea messages
func (v *ChatViewport) Update(msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case tea.MouseWheelMsg:
		if msg.Y < 0 {
			// Scroll up
			v.ScrollUp(3)
			logDebug("[ChatViewport] Scroll UP: yOffset=%d totalLines=%d", v.yOffset, len(v.lines))
		} else if msg.Y > 0 {
			// Scroll down
			v.ScrollDown(3)
			logDebug("[ChatViewport] Scroll DOWN: yOffset=%d totalLines=%d", v.yOffset, len(v.lines))
		}
	}
	return nil
}

// View renders the visible portion of content
func (v *ChatViewport) View() string {
	if len(v.lines) == 0 {
		return ""
	}

	start := v.yOffset
	end := v.yOffset + v.height

	if start < 0 {
		start = 0
	}
	if end > len(v.lines) {
		end = len(v.lines)
	}

	visible := v.lines[start:end]

	// Pad to height if needed
	for len(visible) < v.height {
		visible = append(visible, "")
	}

	return strings.Join(visible, "\n")
}

// YOffset returns current scroll offset
func (v *ChatViewport) YOffset() int {
	return v.yOffset
}

// Width returns viewport width
func (v *ChatViewport) Width() int {
	return v.width
}

// ============================================================================
// CLICKABLE BUTTON COMPONENT
// ============================================================================

// ClickableButton is a button that can be clicked with mouse or activated with keyboard
type ClickableButton struct {
	ID       string // Unique ID for bubblezone click detection
	Label    string
	Icon     string
	Hovered  bool
	Active   bool
	Disabled bool
	Width    int
	Theme    Theme
}

func (b ClickableButton) Render() string {
	th := b.Theme

	// Determine colors based on state
	var bgColor, fgColor string
	if b.Disabled {
		bgColor = th.BGLight
		fgColor = th.TextMuted
	} else if b.Active {
		bgColor = th.Primary
		fgColor = th.BG
	} else if b.Hovered {
		bgColor = th.BGLighter
		fgColor = th.Primary
	} else {
		bgColor = th.BGLight
		fgColor = th.Text
	}

	// Build content
	content := b.Label
	if b.Icon != "" {
		content = b.Icon + " " + b.Label
	}

	// Create button style
	style := lipgloss.NewStyle().
		Foreground(lipgloss.Color(fgColor)).
		Background(lipgloss.Color(bgColor)).
		Padding(0, 2).
		MarginRight(1)

	if b.Width > 0 {
		style = style.Width(b.Width).Align(lipgloss.Center)
	}

	rendered := style.Render(content)

	// Wrap with zone marker for mouse clicks
	if b.ID != "" {
		return zone.Mark(b.ID, rendered)
	}
	return rendered
}

// ============================================================================
// SIDEBAR COMPONENT (VSCode-style)
// ============================================================================

// Sidebar represents a vertical sidebar menu
type Sidebar struct {
	Items  []SidebarItem
	Active int
	Width  int
	Height int
	Theme  Theme
}

// SidebarItem represents a clickable sidebar item
type SidebarItem struct {
	ID    string
	Icon  string
	Label string
	Badge string // Optional badge/count
}

func (s Sidebar) Render() string {
	if len(s.Items) == 0 {
		return ""
	}

	th := s.Theme
	var items []string

	for i, item := range s.Items {
		var style lipgloss.Style

		if i == s.Active {
			style = lipgloss.NewStyle().
				Foreground(lipgloss.Color(th.Primary)).
				Background(lipgloss.Color(th.BGLight)).
				Bold(true).
				BorderLeft(true).
				BorderStyle(lipgloss.NormalBorder()).
				BorderForeground(lipgloss.Color(th.Primary))
		} else {
			style = lipgloss.NewStyle().
				Foreground(lipgloss.Color(th.TextMuted)).
				Background(lipgloss.Color(th.BG))
		}

		content := item.Icon + "  " + item.Label
		if item.Badge != "" {
			content += "  " + item.Badge
		}

		rendered := style.
			Width(s.Width).
			Padding(1, 2).
			Render(content)

		// Mark with zone for clicks
		if item.ID != "" {
			rendered = zone.Mark(item.ID, rendered)
		}

		items = append(items, rendered)
	}

	return lipgloss.JoinVertical(lipgloss.Left, items...)
}

// Height returns viewport height
func (v *ChatViewport) Height() int {
	return v.height
}

// ============================================================================
// MARKDOWN RENDERING FOR ASSISTANT MESSAGES
// ============================================================================

// MarkdownLine represents a processed markdown line with metadata
type MarkdownLine struct {
	Content string
	Indent  int    // How much to indent continuation lines
	Type    string // "normal", "bullet", "number", "code", "header"
}

// renderMarkdownWithWrapping converts markdown to styled output and handles wrapping
func renderMarkdownWithWrapping(text string, width int, theme Theme) []string {
	return renderMarkdownWithWrappingVerbose(text, width, theme, false)
}

func renderMarkdownWithWrappingVerbose(text string, width int, theme Theme, verbose bool) []string {
	mdLines := parseMarkdownWithVerbose(text, theme, verbose, width)
	var result []string

	for _, mdLine := range mdLines {
		// Table lines are already laid out to fit the viewport (and word wrapped
		// cell-by-cell). Re-wrapping them here would slice the box-drawing
		// borders in half, so emit them verbatim.
		if mdLine.Type == "table" {
			result = append(result, mdLine.Content)
			continue
		}

		// A non-positive width means "unconstrained" — emit the line as is
		// rather than wrapping it to an arbitrary column.
		if width <= 0 {
			result = append(result, mdLine.Content)
			continue
		}

		// Wrap the line considering available width minus indent.
		//
		// The available width is never allowed to exceed the viewport: a floor
		// wider than the terminal (there used to be a hard floor of 20) makes
		// every line overflow on a narrow pane instead of wrapping. The indent
		// is clamped for the same reason — a list nested deeper than the pane is
		// wide would otherwise emit a continuation prefix longer than the line
		// it is meant to align. Deeply indented content may end up with very
		// little room, but a narrow column is honest, whereas overflow is not.
		//
		// The clamp leaves at least TWO columns, not one: a single grapheme can
		// be two columns wide (CJK, emoji, fullwidth forms) and cannot be split.
		// Leaving one column meant the wrapper emitted an unsplittable
		// two-column chunk that, once the indent was prepended, ran past the
		// viewport — so "- 日" at width 3 overflowed by one column.
		indentWidth := mdLine.Indent
		if indentWidth > width-2 {
			indentWidth = width - 2
		}
		if indentWidth < 0 {
			indentWidth = 0
		}
		availableWidth := width - indentWidth
		if availableWidth < 1 {
			availableWidth = 1
		}

		// Use visual width for wrapping (accounts for ANSI codes) and preserve
		// styling across wrap points.
		//
		// Prose wraps on WORD boundaries so sentences stay readable; code keeps
		// the hard character wrap because breaking source at spaces would
		// misrepresent it (indentation and tokens must land where they are).
		var wrapped []string
		if mdLine.Type == "code" || mdLine.Type == "code_truncated" {
			wrapped = shared.WrapLineWithANSI(mdLine.Content, availableWidth)
		} else {
			wrapped = shared.WordWrapLineWithANSI(mdLine.Content, availableWidth)
		}

		// Assistant prose inherits the terminal's default background — no
		// width-padding, no reapplyBackground wrap. Inline tokens (bold,
		// code, links) carry their own theme colors via applyInlineStyles;
		// the spaces between them stay terminal-default so the chat reads
		// naturally on both light and dark terminals.
		if len(wrapped) > 0 {
			result = append(result, wrapped[0])
		}
		indent := strings.Repeat(" ", indentWidth)
		for i := 1; i < len(wrapped); i++ {
			result = append(result, indent+wrapped[i])
		}
	}

	return result
}

func parseMarkdownWithVerbose(text string, theme Theme, verbose bool, width int) []MarkdownLine {
	var result []MarkdownLine
	lines := strings.Split(text, "\n")

	inCodeBlock := false
	var codeBlockLines []string
	var codeLanguage string
	var tableRows []string

	for _, line := range lines {
		// Handle code blocks
		if after, ok := strings.CutPrefix(line, "```"); ok {
			if !inCodeBlock {
				inCodeBlock = true
				codeBlockLines = []string{}
				// Extract language hint (e.g., ```go, ```javascript)
				codeLanguage = after
				codeLanguage = strings.TrimSpace(codeLanguage)
				continue
			} else {
				inCodeBlock = false

				// Mermaid diagrams always render as ASCII art, regardless of verbose mode.
				if codeLanguage == "mermaid" {
					result = append(result, renderMermaidDiagram(codeBlockLines, theme, width, verbose)...)
				} else if !verbose && len(codeBlockLines) > 25 {
					if len(codeBlockLines) > 0 {
						firstLine := codeBlockLines[0]
						// Truncate long lines to prevent wrapping
						maxLineLen := 120
						if len(firstLine) > maxLineLen {
							firstLine = firstLine[:maxLineLen-3] + "..."
						}
						styledLine := applySyntaxHighlighting(firstLine, codeLanguage, theme)
						result = append(result, MarkdownLine{
							Content: styledLine,
							Indent:  0,
							Type:    "code",
						})

						// Add truncation indicator
						truncStyle := lipgloss.NewStyle().
							Foreground(lipgloss.Color(theme.TextMuted)).
							Background(lipgloss.Color(theme.BGLight)).
							Italic(true)
						truncLine := truncStyle.Render(i18n.T("classic_chat.markdown.more_lines", len(codeBlockLines)-1))
						result = append(result, MarkdownLine{
							Content: truncLine,
							Indent:  0,
							Type:    "code_truncated",
						})
					}
				} else {
					// Render full code block in verbose mode
					for _, codeLine := range codeBlockLines {
						styledLine := applySyntaxHighlighting(codeLine, codeLanguage, theme)
						result = append(result, MarkdownLine{
							Content: styledLine,
							Indent:  0,
							Type:    "code",
						})
					}
				}

				codeBlockLines = []string{}
				codeLanguage = ""
				continue
			}
		}

		if inCodeBlock {
			codeBlockLines = append(codeBlockLines, line)
			continue
		}

		// Table rows — accumulate, then flush as a unit
		if isTableRow(line) {
			tableRows = append(tableRows, line)
			continue
		}
		if len(tableRows) > 0 {
			result = append(result, renderMarkdownTable(tableRows, theme, width)...)
			tableRows = nil
		}

		// Process different line types
		mdLine := processMarkdownLine(line, theme)
		result = append(result, mdLine)
	}

	// Flush any trailing table
	if len(tableRows) > 0 {
		result = append(result, renderMarkdownTable(tableRows, theme, width)...)
	}

	return result
}

// processMarkdownLine processes a single markdown line
// processMarkdownLine processes a single markdown line
func processMarkdownLine(line string, theme Theme) MarkdownLine {
	// Headers - bold white text
	if strings.HasPrefix(line, "### ") {
		// Headers: bold + explicit theme.Text foreground so the line is visible
		// on both light and dark terminals (never inherit terminal default fg).
		headerStyle := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color(theme.Text))
		content := applyInlineStyles(strings.TrimPrefix(line, "### "), theme)
		return MarkdownLine{
			Content: headerStyle.Render(content),
			Indent:  0,
			Type:    "header",
		}
	}
	if strings.HasPrefix(line, "## ") {
		// Headers: bold + explicit theme.Text foreground so the line is visible
		// on both light and dark terminals (never inherit terminal default fg).
		headerStyle := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color(theme.Text))
		content := applyInlineStyles(strings.TrimPrefix(line, "## "), theme)
		return MarkdownLine{
			Content: headerStyle.Render(content),
			Indent:  0,
			Type:    "header",
		}
	}
	if strings.HasPrefix(line, "# ") {
		// Headers: bold + explicit theme.Text foreground so the line is visible
		// on both light and dark terminals (never inherit terminal default fg).
		headerStyle := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color(theme.Text))
		content := applyInlineStyles(strings.TrimPrefix(line, "# "), theme)
		return MarkdownLine{
			Content: headerStyle.Render(content),
			Indent:  0,
			Type:    "header",
		}
	}

	// Bullet lists - accent bullet
	if strings.HasPrefix(line, "- ") || strings.HasPrefix(line, "* ") {
		// Bullet marker keeps its accent fg; the rest of the line inherits
		// terminal default bg so the row reads on both light and dark.
		bulletStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(theme.Accent))
		bullet := bulletStyle.Render("● ")
		bodyStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(theme.Text))
		content := bodyStyle.Render(applyInlineStyles(line[2:], theme))
		return MarkdownLine{
			Content: bullet + content,
			Indent:  2,
			Type:    "bullet",
		}
	}
	// Numbered lists — accent number, default everything else.
	if len(line) > 0 && line[0] >= '0' && line[0] <= '9' {
		if idx := strings.Index(line, ". "); idx > 0 && idx < 4 {
			numberStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(theme.Accent))
			number := numberStyle.Render(line[:idx+1] + " ")
			bodyStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(theme.Text))
			content := bodyStyle.Render(applyInlineStyles(line[idx+2:], theme))
			return MarkdownLine{
				Content: number + content,
				Indent:  idx + 2,
				Type:    "number",
			}
		}
	}
	// Normal text — explicit theme.Text foreground so assistant prose is visible
	// on both light and dark terminals (never inherit terminal default fg).
	// Inline accents (bold, italic, code) still come through via applyInlineStyles.
	bodyStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(theme.Text))
	return MarkdownLine{
		Content: bodyStyle.Render(applyInlineStyles(line, theme)),
		Indent:  0,
		Type:    "normal",
	}
}

// isTableRow returns true if the line looks like a markdown table row (| ... |)
func isTableRow(line string) bool {
	trimmed := strings.TrimSpace(line)
	return len(trimmed) >= 2 && strings.HasPrefix(trimmed, "|") && strings.HasSuffix(trimmed, "|")
}

// isTableSeparator returns true for separator rows like |---|---|
func isTableSeparator(line string) bool {
	trimmed := strings.TrimSpace(line)
	if !strings.HasPrefix(trimmed, "|") {
		return false
	}
	for _, c := range trimmed {
		if c != '|' && c != '-' && c != ':' && c != ' ' {
			return false
		}
	}
	return strings.ContainsRune(trimmed, '-')
}

// parseTableCells splits a table row into trimmed cell strings
func parseTableCells(line string) []string {
	trimmed := strings.TrimSpace(line)
	if len(trimmed) > 0 && trimmed[0] == '|' {
		trimmed = trimmed[1:]
	}
	if len(trimmed) > 0 && trimmed[len(trimmed)-1] == '|' {
		trimmed = trimmed[:len(trimmed)-1]
	}
	parts := strings.Split(trimmed, "|")
	cells := make([]string, len(parts))
	for i, p := range parts {
		cells[i] = strings.TrimSpace(sanitizeTableCellText(p))
	}
	return cells
}

// sanitizeTableCellText makes a cell safe to measure and draw.
//
// A table's alignment depends on the rendered width of a cell matching what the
// terminal actually paints. Two kinds of input break that promise:
//
//   - Tabs, which measure as one column here but expand to the next tab stop on
//     screen, shoving the border right and shearing the whole column.
//   - Control characters such as backspace or carriage return, which move the
//     cursor and can erase the border that was already drawn.
//
// Escape sequences are filtered first so that legitimate SGR colouring in the
// source text survives, then tabs become a single space and any remaining C0
// control (plus DEL), C1 control, or invalid UTF-8 byte is dropped. C1 matters
// because 0x9B is an eight-bit CSI introducer: left in place it lets arbitrary
// text steer the terminal.
func sanitizeTableCellText(s string) string {
	if s == "" {
		return s
	}
	s = shared.SanitizeANSI(s)

	var sb strings.Builder
	sb.Grow(len(s))
	for i := 0; i < len(s); {
		r, size := utf8.DecodeRuneInString(s[i:])
		if r == utf8.RuneError && size <= 1 {
			i++ // not valid UTF-8 — drop the byte
			continue
		}
		i += size

		switch {
		case r == '\t':
			sb.WriteRune(' ')
		case r == 0x1b:
			sb.WriteRune(r) // retained SGR sequence
		case r < 0x20 || r == 0x7f:
			// drop: backspace, BEL, NUL, vertical tab, form feed, DEL…
		case r >= 0x80 && r <= 0x9f:
			// drop: C1 controls, including the eight-bit CSI introducer 0x9B
		default:
			sb.WriteRune(r)
		}
	}
	return sb.String()
}

// tableCellBreakPattern matches the HTML line breaks authors commonly use to
// force a new line inside a markdown table cell.
var tableCellBreakPattern = regexp.MustCompile(`(?i)<br\s*/?>`)

// wrapTableCell word-wraps a single table cell's content to the given column
// width, returning one string per physical line. Content is never truncated:
// long values flow onto additional lines, and words longer than the column are
// broken only as a last resort so the cell can never overflow its border.
//
// Explicit <br> tags and embedded newlines are honoured as hard line breaks.
func wrapTableCell(content string, width int) []string {
	if width <= 0 {
		return []string{""}
	}

	// Normalize author-forced breaks into real newlines.
	content = tableCellBreakPattern.ReplaceAllString(content, "\n")

	var lines []string
	for _, segment := range strings.Split(content, "\n") {
		if strings.TrimSpace(segment) == "" {
			lines = append(lines, "")
			continue
		}
		lines = append(lines, shared.WordWrapLineWithANSI(segment, width)...)
	}
	if len(lines) == 0 {
		return []string{""}
	}
	return lines
}

// minGridColContentWidth is the smallest number of characters a column may hold
// before a bordered grid stops being worth drawing. Below this the borders and
// padding dominate the viewport and every column becomes an unreadable sliver,
// so renderMarkdownTable switches to a stacked record layout instead.
const minGridColContentWidth = 5

// maxComfortableWordWidth caps how much width a single long token may demand
// when deciding whether a grid is viable. One pathological token (a URL, a
// hash, a long identifier) should not by itself banish an otherwise fine table
// to the stacked layout — such a token can be broken with little loss.
const maxComfortableWordWidth = 12

// maxTopUpDonationRatio controls when a short column may reclaim its full
// natural width from the widest column: the donation must be at most 1/ratio of
// the donor's width. At 10, a label needing 4 more columns can take them from a
// 143-wide prose column (cheap) but not from an 18-wide one (expensive).
const maxTopUpDonationRatio = 10

// longestWordWidth returns the visual width of the widest space-delimited token
// in s. ANSI styling has zero visual width, so styled cells measure correctly.
func longestWordWidth(s string) int {
	widest := 0
	for _, word := range strings.Fields(s) {
		if w := lipgloss.Width(word); w > widest {
			widest = w
		}
	}
	return widest
}

// renderTableAsRecords renders a table as one stacked "label: value" block per
// row. It is the fallback for tables that cannot be drawn as a grid — too many
// columns for the viewport, or a viewport too narrow to give each column a
// readable width. Content is word wrapped and never truncated, so a table that
// is impossible to tabulate is still fully readable.
func renderTableAsRecords(headers []string, dataRows [][]string, theme Theme, maxWidth int) []MarkdownLine {
	if maxWidth < 1 {
		maxWidth = 1
	}

	labelStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(theme.Primary)).
		Background(lipgloss.Color(theme.BG)).
		Bold(true)
	valueStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(theme.Text)).
		Background(lipgloss.Color(theme.BG))
	ruleStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(theme.Border)).
		Background(lipgloss.Color(theme.BG))

	labelWidth := 0
	for _, h := range headers {
		if w := lipgloss.Width(h); w > labelWidth {
			labelWidth = w
		}
	}

	// Indentation is a luxury: on a very narrow viewport every column of it is
	// a column not spent on content, so it shrinks away rather than pushing
	// text past the edge.
	margin, valueIndent := " ", "   "
	if maxWidth < 12 {
		margin, valueIndent = "", " "
	}
	if maxWidth < 4 {
		valueIndent = ""
	}
	labelBudget := maxWidth - lipgloss.Width(margin)
	valueBudget := maxWidth - lipgloss.Width(valueIndent)
	if labelBudget < 1 {
		labelBudget = 1
	}
	if valueBudget < 1 {
		valueBudget = 1
	}

	// Keep labels beside their values only while the label column stays a minor
	// part of the viewport and the value still has room to read. Otherwise put
	// the label on its own line and indent the value beneath it.
	maxLabelWidth := maxWidth / 3
	inlineValueWidth := maxWidth - labelWidth - 3
	inlineLabels := labelWidth > 0 && labelWidth <= maxLabelWidth && inlineValueWidth >= 8

	var out []MarkdownLine
	appendLine := func(s string) {
		out = append(out, MarkdownLine{Content: s, Indent: 0, Type: "table"})
	}

	for ri, row := range dataRows {
		if ri > 0 {
			appendLine(ruleStyle.Render(strings.Repeat("─", maxWidth)))
		}
		for c, rawCell := range row {
			if strings.TrimSpace(rawCell) == "" {
				continue // nothing to show for this field
			}
			label := ""
			if c < len(headers) {
				label = strings.TrimSpace(headers[c])
			}
			value := applyInlineStyles(rawCell, theme)

			if inlineLabels {
				labelPad := strings.Repeat(" ", labelWidth-lipgloss.Width(label))
				for li, seg := range wrapTableCell(value, inlineValueWidth) {
					if li == 0 {
						appendLine(" " + labelStyle.Render(label+labelPad) + "  " + valueStyle.Render(seg))
					} else {
						appendLine(" " + strings.Repeat(" ", labelWidth) + "  " + valueStyle.Render(seg))
					}
				}
				continue
			}

			// Stacked: the label gets its own line(s), the value is indented
			// beneath it. Labels wrap too — a header can be longer than the
			// whole viewport.
			if label != "" {
				for _, seg := range wrapTableCell(label, labelBudget) {
					appendLine(margin + labelStyle.Render(seg))
				}
			}
			for _, seg := range wrapTableCell(value, valueBudget) {
				appendLine(valueIndent + valueStyle.Render(seg))
			}
		}
	}
	return out
}

// renderMarkdownTable turns a slice of raw table row strings into styled MarkdownLines
func renderMarkdownTable(rows []string, theme Theme, maxWidth int) []MarkdownLine {
	if len(rows) == 0 {
		return nil
	}

	// Find separator to locate header row (row immediately before separator)
	headerIdx := -1
	separatorIdx := -1
	for i, row := range rows {
		if isTableSeparator(row) {
			separatorIdx = i
			if i > 0 {
				headerIdx = i - 1
			}
			break
		}
	}

	type parsedRow struct {
		cells    []string
		isHeader bool
	}
	var parsedRows []parsedRow
	for i, row := range rows {
		if i == separatorIdx {
			continue
		}
		parsedRows = append(parsedRows, parsedRow{
			cells:    parseTableCells(row),
			isHeader: i == headerIdx,
		})
	}
	if len(parsedRows) == 0 {
		return nil
	}

	// Column count = max cells across all rows
	numCols := 0
	for _, pr := range parsedRows {
		if len(pr.cells) > numCols {
			numCols = len(pr.cells)
		}
	}
	if numCols == 0 {
		return nil
	}

	// Build the display form of every cell once. Data cells carry inline
	// markdown styling (bold, code, links); header cells stay raw because the
	// header style is applied at render time. Caching this matters: a large
	// table would otherwise run the inline-style regexes twice per cell, once
	// to measure and again to render.
	displayRows := make([][]string, len(parsedRows))
	for i, pr := range parsedRows {
		displayRows[i] = make([]string, numCols)
		for c := 0; c < numCols && c < len(pr.cells); c++ {
			if pr.isHeader {
				displayRows[i][c] = pr.cells[c]
			} else {
				displayRows[i][c] = applyInlineStyles(pr.cells[c], theme)
			}
		}
	}

	// Natural column widths from the visual width of the display cells.
	colWidths := make([]int, numCols)
	for _, cells := range displayRows {
		for c, cell := range cells {
			w := lipgloss.Width(cell)
			if w < 1 {
				w = 1
			}
			if w > colWidths[c] {
				colWidths[c] = w
			}
		}
	}
	for c := range colWidths {
		if colWidths[c] < 3 {
			colWidths[c] = 3
		}
	}

	// Decide whether a bordered grid is still viable at this viewport.
	//
	// A grid costs 1 + numCols*3 columns of pure structure. What is left has to
	// hold the text, and a column that cannot fit its own words shreds them
	// mid-token ("alice@e/xample./com"), which is exactly the unreadable result
	// users complain about. So each column asks for enough room to hold its
	// longest word — capped, and never more than its natural width. If the sum
	// of those modest requests does not fit, the grid is hopeless at this width
	// and stacked records are used instead: they fit any viewport and still show
	// every value in full.
	gridViable := true
	if maxWidth > 0 {
		budget := maxWidth - (1 + numCols*3)
		comfortable := 0
		for c := 0; c < numCols; c++ {
			want := minGridColContentWidth
			for _, cells := range displayRows {
				if w := longestWordWidth(cells[c]); w > want {
					want = w
				}
			}
			if want > maxComfortableWordWidth {
				want = maxComfortableWordWidth
			}
			if want > colWidths[c] {
				want = colWidths[c] // never ask for more than the content needs
			}
			comfortable += want
		}
		gridViable = comfortable <= budget
	}

	if !gridViable {
		var headers []string
		var dataRows [][]string
		for i, pr := range parsedRows {
			if pr.isHeader {
				headers = pr.cells
				continue
			}
			dataRows = append(dataRows, parsedRows[i].cells)
		}
		if len(dataRows) == 0 {
			// Header-only table: show the header cells as the record.
			dataRows = append(dataRows, headers)
			headers = nil
		}
		return renderTableAsRecords(headers, dataRows, theme, maxWidth)
	}

	// Natural widths are what each column would like to have; keep a copy so we
	// never grow a column beyond the space its content actually needs.
	naturalWidths := make([]int, numCols)
	copy(naturalWidths, colWidths)

	// Fit the columns into maxWidth.
	//
	// Table width = 1 (opening │) + per-col: (colW + 2 padding + 1 │)
	// So overhead = 1 + numCols*3, and content budget = maxWidth - overhead.
	//
	// Each column first claims a floor: enough room for its longest word (so it
	// is not shredded mid-token), capped, and never more than it actually needs.
	// Whatever budget remains is then shared out in proportion to how much each
	// column still wants. That matters when demand is lopsided: a short label
	// column beside a two-paragraph column should not keep padding it does not
	// need while the prose column is starved to a sliver. Pure min-max fairness
	// (water-filling) does exactly that, so it is used only as the fallback for
	// when even the floors do not fit.
	//
	// Since cells word wrap rather than truncate, a narrowed column costs
	// vertical space, never information.
	//
	// If the table cannot fit even with every column at minColWidth=3, we retry
	// at minColWidth=1. If it still cannot fit (viewport narrower than the
	// structural overhead of borders+padding), we accept the minimum: the table
	// is then as small as physically possible.

	// wordFloors[c] is the width column c needs to hold its longest word.
	wordFloors := make([]int, numCols)
	for c := 0; c < numCols; c++ {
		floor := 1
		for _, cells := range displayRows {
			if w := longestWordWidth(cells[c]); w > floor {
				floor = w
			}
		}
		if floor > maxComfortableWordWidth {
			floor = maxComfortableWordWidth
		}
		if floor > naturalWidths[c] {
			floor = naturalWidths[c]
		}
		wordFloors[c] = floor
	}

	calcTableWidth := func() int {
		tw := 1
		for _, w := range colWidths {
			tw += w + 3
		}
		return tw
	}

	// distributeRemainder hands leftover budget to the columns that are still
	// below their natural width, one cell at a time, so integer division never
	// wastes space.
	distributeRemainder := func(leftover int) {
		for leftover > 0 {
			progressed := false
			for c := range colWidths {
				if leftover == 0 {
					break
				}
				if colWidths[c] < naturalWidths[c] {
					colWidths[c]++
					leftover--
					progressed = true
				}
			}
			if !progressed {
				return
			}
		}
	}

	// waterFill equalizes columns under a cap: the largest T where
	// Σ min(width_i, T) fits the budget. Used when the word floors themselves
	// cannot fit, where fairness beats proportionality.
	waterFill := func(budget, minW int) {
		lo, hi := minW, 0
		for _, w := range colWidths {
			if w > hi {
				hi = w
			}
		}
		for lo < hi {
			mid := (lo + hi + 1) / 2
			sum := 0
			for _, w := range colWidths {
				if w < mid {
					sum += w
				} else {
					sum += mid
				}
			}
			if sum <= budget {
				lo = mid
			} else {
				hi = mid - 1
			}
		}

		used := 0
		for c := range colWidths {
			if colWidths[c] > lo {
				colWidths[c] = lo
			}
			used += colWidths[c]
		}
		distributeRemainder(budget - used)
	}

	// shrinkColumns fits the columns into maxWidth, never letting any column
	// fall below minW. It always starts from the natural widths so repeated
	// calls with a smaller minW behave predictably.
	shrinkColumns := func(minW int) {
		budget := maxWidth - (1 + numCols*3)

		// Start from natural widths, clamped to at least minW.
		total := 0
		for c := range colWidths {
			colWidths[c] = naturalWidths[c]
			if colWidths[c] < minW {
				colWidths[c] = minW
			}
			total += colWidths[c]
		}
		if total <= budget {
			return // already fits
		}
		if budget < numCols*minW {
			// Cannot fit even at the minimum — pin every column to minW so the
			// table is as narrow as it can structurally be.
			for c := range colWidths {
				colWidths[c] = minW
			}
			return
		}

		// Lay the floors first: each column gets room for its longest word.
		floorTotal := 0
		for c := range colWidths {
			colWidths[c] = wordFloors[c]
			if colWidths[c] < minW {
				colWidths[c] = minW
			}
			floorTotal += colWidths[c]
		}

		if floorTotal > budget {
			// Even the floors do not fit — fall back to equalizing, which keeps
			// every column at a comparable (if cramped) width.
			for c := range colWidths {
				colWidths[c] = naturalWidths[c]
				if colWidths[c] < minW {
					colWidths[c] = minW
				}
			}
			waterFill(budget, minW)
			return
		}

		// Share the rest in proportion to unmet demand, so the column with two
		// paragraphs of text gets the slack rather than a short label column.
		totalDemand := 0
		for c := range colWidths {
			if d := naturalWidths[c] - colWidths[c]; d > 0 {
				totalDemand += d
			}
		}
		remaining := budget - floorTotal
		if totalDemand > 0 && remaining > 0 {
			granted := 0
			for c := range colWidths {
				demand := naturalWidths[c] - colWidths[c]
				if demand <= 0 {
					continue
				}
				share := (demand * remaining) / totalDemand
				colWidths[c] += share
				granted += share
			}
			distributeRemainder(remaining - granted)
		}

		// Demand-proportional sharing alone starves a short column whenever a
		// neighbour's appetite is enormous — a two-word label would wrap even on
		// a 200-column terminal, which looks broken next to a column with space
		// to spare. So let a column that is only slightly short of its natural
		// width buy the difference from the widest column, but only while that
		// donation stays a small fraction of the donor's space. When the table
		// is genuinely tight the donation is refused and the prose column keeps
		// the room, which is what matters there.
		for pass := 0; pass < numCols; pass++ {
			widest, widestIdx := 0, -1
			for c, w := range colWidths {
				if w > widest {
					widest, widestIdx = w, c
				}
			}
			if widestIdx < 0 {
				break
			}

			moved := false
			for c := range colWidths {
				if c == widestIdx {
					continue
				}
				need := naturalWidths[c] - colWidths[c]
				if need <= 0 {
					continue
				}
				if need*maxTopUpDonationRatio > colWidths[widestIdx] {
					continue // too expensive for the donor
				}
				colWidths[c] += need
				colWidths[widestIdx] -= need
				moved = true
			}
			if !moved {
				break
			}
		}
	}

	if maxWidth > 0 {
		// First pass: try to fit with comfortable minimum (3 chars per col)
		shrinkColumns(3)
		// Second pass: if still overflowing, shrink harder to absolute minimum (1 char)
		if calcTableWidth() > maxWidth {
			shrinkColumns(1)
		}
	}

	borderStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(theme.Border)).
		Background(lipgloss.Color(theme.BG))
	headerCellStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(theme.Primary)).
		Background(lipgloss.Color(theme.BG)).
		Bold(true)
	dataCellStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(theme.Text)).
		Background(lipgloss.Color(theme.BG))

	bord := func(s string) string { return borderStyle.Render(s) }

	buildHRule := func(left, mid, right, fill string) string {
		var sb strings.Builder
		sb.WriteString(bord(left))
		for c, w := range colWidths {
			sb.WriteString(bord(strings.Repeat(fill, w+2)))
			if c < numCols-1 {
				sb.WriteString(bord(mid))
			}
		}
		sb.WriteString(bord(right))
		return sb.String()
	}

	// buildDataRowLines renders one logical table row as one or more physical
	// lines. Cell content that is wider than its column is WORD WRAPPED onto
	// additional lines instead of being truncated with an ellipsis, so no
	// information is ever hidden from the user. Every physical line of the row
	// has the same number of cells, padded to the column width, so the vertical
	// borders stay aligned.
	buildDataRowLines := func(cells []string, isHeader bool) []string {
		// Wrap each cell into its column width; remember the tallest cell so we
		// know how many physical lines this row occupies.
		wrappedCells := make([][]string, numCols)
		rowHeight := 1
		for c, w := range colWidths {
			var content string
			if c < len(cells) {
				content = cells[c]
			}
			// cells are already in display form (see displayRows): data cells
			// carry inline styling so the wrap math uses real visual width,
			// header cells stay raw for headerCellStyle to style.
			lines := wrapTableCell(content, w)
			wrappedCells[c] = lines
			if len(lines) > rowHeight {
				rowHeight = len(lines)
			}
		}

		out := make([]string, 0, rowHeight)
		for line := 0; line < rowHeight; line++ {
			var sb strings.Builder
			sb.WriteString(bord("│"))
			for c, w := range colWidths {
				segment := ""
				if line < len(wrappedCells[c]) {
					segment = wrappedCells[c][line]
				}
				segVW := lipgloss.Width(segment)
				padLen := w - segVW
				if padLen < 0 {
					padLen = 0
				}
				pad := strings.Repeat(" ", padLen)
				if isHeader {
					sb.WriteString(headerCellStyle.Render(" " + segment + pad + " "))
				} else {
					sb.WriteString(dataCellStyle.Render(" " + segment + pad + " "))
				}
				sb.WriteString(bord("│"))
			}
			out = append(out, sb.String())
		}
		return out
	}

	var result []MarkdownLine
	result = append(result, MarkdownLine{Content: buildHRule("┌", "┬", "┐", "─"), Indent: 0, Type: "table"})

	// Lay every row out first so we know whether any row wrapped. Once rows span
	// multiple lines, adjacent rows visually merge — especially when a leading
	// cell is short or empty — so a tall table gets a rule between rows. Compact
	// tables where every row is a single line stay dense and unruled.
	rowGroups := make([][]string, len(parsedRows))
	anyRowWrapped := false
	for i, pr := range parsedRows {
		rowGroups[i] = buildDataRowLines(displayRows[i], pr.isHeader)
		if !pr.isHeader && len(rowGroups[i]) > 1 {
			anyRowWrapped = true
		}
	}

	prevWasData := false
	for i, pr := range parsedRows {
		if anyRowWrapped && prevWasData {
			result = append(result, MarkdownLine{Content: buildHRule("├", "┼", "┤", "─"), Indent: 0, Type: "table"})
		}
		for _, physical := range rowGroups[i] {
			result = append(result, MarkdownLine{Content: physical, Indent: 0, Type: "table"})
		}
		if pr.isHeader {
			result = append(result, MarkdownLine{Content: buildHRule("├", "┼", "┤", "─"), Indent: 0, Type: "table"})
		}
		prevWasData = !pr.isHeader
	}
	result = append(result, MarkdownLine{Content: buildHRule("└", "┴", "┘", "─"), Indent: 0, Type: "table"})
	return result
}

func applyInlineStyles(text string, theme Theme) string {
	// Apply in order: code first (to protect), then bold, then italic.
	// Pass "" (empty) for the bg arg so the inline tokens keep their fg
	// accent but inherit whatever bg is active around them — important
	// so assistant prose reads correctly on both light and dark terminals.
	// Inline code is the one exception: it keeps an explicit bg (handled
	// inside styleInlineCode) to read as a distinct token like ` foo `.
	//
	// Linkification runs FIRST, while the text is still plain: the detectors
	// must never see an escape sequence, and a markdown link's label has to be
	// resolved before the label itself is styled. Everything after it is then
	// applied only OUTSIDE the emitted OSC payloads, so a URL containing an
	// underscore or a pair of asterisks cannot have style escapes spliced into
	// the middle of its escape sequence.
	linked := Linkify(text, LinkifyOptions{
		AllowLabels:   true,
		WorkspaceRoot: linkWorkspaceRoot(),
	})

	return mapOutsideLinkPayloads(linked, func(styled string) string {
		styled = styleInlineCode(styled, theme)

		// Bold: **text** or __text__ — primary accent fg only.
		styled = styleInlinePattern(styled, `\*\*([^\*]+?)\*\*`, theme.Primary, "", true, false)
		styled = styleInlinePattern(styled, `__(.+?)__`, theme.Primary, "", true, false)

		// Italic: *text* or _text_ — secondary accent fg only.
		styled = styleInlinePattern(styled, `\*([^\*]+?)\*`, theme.Secondary, "", false, true)
		styled = styleInlineUnderscoreItalic(styled, theme)

		return styled
	})
}

// reapplyBackground replaces ANSI resets with reset+background to ensure background continuity
func reapplyBackground(text string, bgColor string) string {
	// Get the ANSI sequence for the theme background
	// We render a space and extract the sequence before the space
	style := lipgloss.NewStyle().Background(lipgloss.Color(bgColor))
	rendered := style.Render(" ")
	// rendered is like: <seq> <space> <reset>
	// We want <seq>
	// Find the space
	before, _, ok := strings.Cut(rendered, " ")
	if !ok {
		return text
	}
	bgSeq := before

	// Replace resets with reset + bgSeq
	// Lipgloss typically uses \x1b[0m
	replaced := strings.ReplaceAll(text, "\x1b[0m", "\x1b[0m"+bgSeq)
	// Just in case it uses the shorter \x1b[m
	replaced = strings.ReplaceAll(replaced, "\x1b[m", "\x1b[m"+bgSeq)

	// CRITICAL: Prepend bgSeq to ensure the very start of the line has the background
	// Without this, any leading characters (spaces, plain text) before the first
	// ANSI code will fall through to the terminal default background
	return bgSeq + replaced
}

// styleInlinePattern applies styling to regex pattern matches
func styleInlinePattern(text, pattern, color, bgColor string, bold, italic bool) string {
	re := regexp.MustCompile(pattern)

	return re.ReplaceAllStringFunc(text, func(match string) string {
		content := re.FindStringSubmatch(match)
		if len(content) < 2 {
			return match
		}

		style := lipgloss.NewStyle().
			Foreground(lipgloss.Color(color)).
			Background(lipgloss.Color(bgColor))

		if bold {
			style = style.Bold(true)
		}
		if italic {
			style = style.Italic(true)
		}

		return style.Render(content[1])
	})
}

// styleInlineCode applies styling to inline code blocks
func styleInlineCode(text string, theme Theme) string {
	re := regexp.MustCompile("`([^`]+)`")

	return re.ReplaceAllStringFunc(text, func(match string) string {
		content := re.FindStringSubmatch(match)
		if len(content) < 2 {
			return match
		}

		codeStyle := lipgloss.NewStyle().
			Foreground(lipgloss.Color(theme.Accent)).
			Background(lipgloss.Color(theme.BGLighter))

		return codeStyle.Render(" " + content[1] + " ")
	})
}

var underscoreItalicRE = regexp.MustCompile(`(^|[^A-Za-z0-9_])_([^_\n]+?)_([^A-Za-z0-9_]|$)`)

// styleInlineUnderscoreItalic renders underscore-based italics with a more Markdown-like boundary rule.
// This prevents accidental italics in common snake_case tokens and IDs (e.g., error_id=..., execution_failed).
func styleInlineUnderscoreItalic(text string, theme Theme) string {
	italic := lipgloss.NewStyle().
		Foreground(lipgloss.Color(theme.Secondary)).
		Italic(true)

	return underscoreItalicRE.ReplaceAllStringFunc(text, func(match string) string {
		parts := underscoreItalicRE.FindStringSubmatch(match)
		if len(parts) < 4 {
			return match
		}
		prefix := parts[1]
		content := parts[2]
		suffix := parts[3]
		return prefix + italic.Render(content) + suffix
	})
}

// applySyntaxHighlighting applies basic syntax highlighting to code
func applySyntaxHighlighting(line string, language string, theme Theme) string {
	// Base style - white text on dark background for readability
	baseStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(theme.Text)).
		Background(lipgloss.Color(theme.BGLight))

	// If line has line numbers already (from file read tool), style the line number separately
	if strings.Contains(line, "│") {
		parts := strings.SplitN(line, "│", 2)
		if len(parts) == 2 {
			// Style line number in muted color
			lineNumStyle := lipgloss.NewStyle().
				Foreground(lipgloss.Color(theme.TextMuted)).
				Background(lipgloss.Color(theme.BGLight))

			// Style code content in base color
			codeStyle := lipgloss.NewStyle().
				Foreground(lipgloss.Color(theme.Text)).
				Background(lipgloss.Color(theme.BGLight))

			return lineNumStyle.Render(parts[0]) + "│" + codeStyle.Render(parts[1])
		}
	}

	// Highlight comments (simple detection for readability)
	commentStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(theme.TextDim)).
		Background(lipgloss.Color(theme.BGLight)).
		Italic(true)

	if strings.Contains(line, "//") || strings.HasPrefix(strings.TrimSpace(line), "#") {
		return commentStyle.Render(line)
	}

	// Return with base white styling for good readability
	// Full syntax highlighting would require a proper lexer
	return baseStyle.Render(line)
}
