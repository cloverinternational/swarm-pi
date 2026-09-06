package settings

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/i18n"
)

// DisplaySettings handles display/rendering settings
type DisplaySettings struct {
	showThinking             bool
	showFullToolOutput       bool
	richAnimations           bool
	copySelectionShortcut    bool
	autoCopySelectionOnMouse bool
	spinnerType              string // "Matrix", "Braille", "Blocks", "Wave", "Pulse", "Bounce", "Aurora", "DNA Helix"
	startupView              string // "simple" or "advanced"
}

// SpinnerTypes available for selection
var SpinnerTypes = []string{
	"Matrix",
	"Braille",
	"Blocks",
	"Wave",
	"Pulse",
	"Bounce",
	"Aurora",
	"DNA Helix",
}

// SpinnerTypeDescriptions maps spinner name to description
var SpinnerTypeDescriptions = map[string]string{
	"Matrix":    "Cyberpunk matrix-style random characters",
	"Braille":   "Classic minimalist braille dots spinner",
	"Blocks":    "Building blocks loading animation",
	"Wave":      "Smooth wave pattern animation",
	"Pulse":     "Pulsing circles with glow effect",
	"Bounce":    "Bouncing ball between walls",
	"Aurora":    "Northern lights color shimmer",
	"DNA Helix": "Double helix DNA strand",
}

// NewDisplaySettings creates a new display settings handler
func NewDisplaySettings(showThinking, showFullToolOutput, richAnimations, copySelectionShortcut, autoCopySelectionOnMouse bool, spinnerType, startupView string) *DisplaySettings {
	if spinnerType == "" {
		spinnerType = "Braille" // Default if not provided (better terminal compat than Matrix)
	}
	if startupView == "" {
		startupView = "simple"
	}
	return &DisplaySettings{
		showThinking:             showThinking,
		showFullToolOutput:       showFullToolOutput,
		richAnimations:           richAnimations,
		copySelectionShortcut:    copySelectionShortcut,
		autoCopySelectionOnMouse: autoCopySelectionOnMouse,
		spinnerType:              spinnerType,
		startupView:              startupView,
	}
}

// GetShowThinking returns current show thinking setting
func (d *DisplaySettings) GetShowThinking() bool {
	return d.showThinking
}

// GetShowFullToolOutput returns current show full tool output setting
func (d *DisplaySettings) GetShowFullToolOutput() bool {
	return d.showFullToolOutput
}

// GetRichAnimations returns whether rich animations are enabled
func (d *DisplaySettings) GetRichAnimations() bool {
	return d.richAnimations
}

// GetCopySelectionShortcut returns whether copy shortcut is enabled
func (d *DisplaySettings) GetCopySelectionShortcut() bool {
	return d.copySelectionShortcut
}

// GetAutoCopySelectionOnMouse returns whether auto copy on selection is enabled
func (d *DisplaySettings) GetAutoCopySelectionOnMouse() bool {
	return d.autoCopySelectionOnMouse
}

// ToggleShowThinking toggles the show thinking setting
func (d *DisplaySettings) ToggleShowThinking() {
	d.showThinking = !d.showThinking
}

// ToggleShowFullToolOutput toggles the show full tool output setting
func (d *DisplaySettings) ToggleShowFullToolOutput() {
	d.showFullToolOutput = !d.showFullToolOutput
}

// ToggleRichAnimations toggles rich animations
func (d *DisplaySettings) ToggleRichAnimations() {
	d.richAnimations = !d.richAnimations
}

// ToggleCopySelectionShortcut toggles copy shortcut
func (d *DisplaySettings) ToggleCopySelectionShortcut() {
	d.copySelectionShortcut = !d.copySelectionShortcut
}

// ToggleAutoCopySelectionOnMouse toggles auto copy on selection
func (d *DisplaySettings) ToggleAutoCopySelectionOnMouse() {
	d.autoCopySelectionOnMouse = !d.autoCopySelectionOnMouse
}

// GetStartupView returns the current startup view preference ("simple" or "advanced")
func (d *DisplaySettings) GetStartupView() string {
	return d.startupView
}

// SetStartupView sets the startup view preference
func (d *DisplaySettings) SetStartupView(view string) {
	d.startupView = view
}

// ToggleStartupView toggles between "simple" and "advanced"
func (d *DisplaySettings) ToggleStartupView() {
	if d.startupView == "simple" {
		d.startupView = "advanced"
	} else {
		d.startupView = "simple"
	}
}

// GetSpinnerType returns current spinner type
func (d *DisplaySettings) GetSpinnerType() string {
	return d.spinnerType
}

// SetSpinnerType sets the spinner type
func (d *DisplaySettings) SetSpinnerType(spinnerType string) {
	d.spinnerType = spinnerType
}

// NextSpinnerType cycles to the next spinner type
func (d *DisplaySettings) NextSpinnerType() {
	for i, t := range SpinnerTypes {
		if t == d.spinnerType {
			d.spinnerType = SpinnerTypes[(i+1)%len(SpinnerTypes)]
			return
		}
	}
	d.spinnerType = SpinnerTypes[0]
}

// PrevSpinnerType cycles to the previous spinner type
func (d *DisplaySettings) PrevSpinnerType() {
	for i, t := range SpinnerTypes {
		if t == d.spinnerType {
			newIdx := i - 1
			if newIdx < 0 {
				newIdx = len(SpinnerTypes) - 1
			}
			d.spinnerType = SpinnerTypes[newIdx]
			return
		}
	}
	d.spinnerType = SpinnerTypes[0]
}

// Render renders the display settings view
// animationFrame is provided by the shared AnimationClock for live previews
func (d *DisplaySettings) Render(width, height int, state *State, theme any, animationFrame int) string {
	th := theme.(Theme)
	bg := th.BG
	var lines []string

	safeWidth := maxInt(20, width-4)

	// Title (centered and styled)
	titleStyle := lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color(th.Primary)).
		Background(lipgloss.Color(bg)).
		Width(safeWidth).
		Align(lipgloss.Center)
	lines = append(lines, titleStyle.Render(i18n.T("settings.display.title")))

	// Subtitle — hide at narrow widths
	if width >= 50 {
		subtitleStyle := lipgloss.NewStyle().
			Foreground(lipgloss.Color(th.TextMuted)).
			Background(lipgloss.Color(bg)).
			Italic(true).
			Width(safeWidth).
			Align(lipgloss.Center)
		lines = append(lines, subtitleStyle.Render(i18n.T("settings.display.subtitle")))
	}
	lines = append(lines, "")

	// Toggle settings (items 0-4)
	toggleSettings := []struct {
		name        string
		enabled     bool
		description string
	}{
		{i18n.T("settings.display.thinking.label"), d.showThinking, i18n.T("settings.display.thinking.description")},
		{i18n.T("settings.display.tool_output.label"), d.showFullToolOutput, i18n.T("settings.display.tool_output.description")},
		{i18n.T("settings.display.animations.label"), d.richAnimations, i18n.T("settings.display.animations.description")},
		{i18n.T("settings.display.copy_shortcut.label"), d.copySelectionShortcut, i18n.T("settings.display.copy_shortcut.description")},
		{i18n.T("settings.display.auto_copy.label"), d.autoCopySelectionOnMouse, i18n.T("settings.display.auto_copy.description")},
		{i18n.T("settings.display.startup.label"), d.startupView == "simple", i18n.T("settings.display.startup.description")},
	}

	for i, setting := range toggleSettings {
		isSelected := i == state.SelectedItem && state.Focus == FocusContent

		// Setting name — reduce padding at narrow widths
		namePadH := 2
		if width < 50 {
			namePadH = 1
		}
		nameStyle := lipgloss.NewStyle().
			Foreground(lipgloss.Color(th.Text)).
			Background(lipgloss.Color(bg)).
			Bold(isSelected).
			Padding(0, namePadH)
		if isSelected {
			nameStyle = nameStyle.Foreground(lipgloss.Color(th.Primary))
		}
		name := nameStyle.Render(fmt.Sprintf("%s %s", map[bool]string{true: "▶", false: " "}[isSelected], setting.name))
		lines = append(lines, name)

		// Description — reduce padding at narrow widths
		descPadH := 4
		if width < 50 {
			descPadH = 2
		}
		descStyle := lipgloss.NewStyle().
			Foreground(lipgloss.Color(th.TextMuted)).
			Background(lipgloss.Color(bg)).
			Padding(0, descPadH)
		desc := descStyle.Render(setting.description)
		lines = append(lines, desc)

		// Toggle control
		var toggleStyle lipgloss.Style
		var toggleText string

		toggleMargin := 2
		if width < 50 {
			toggleMargin = 1
		}
		if setting.enabled {
			toggleStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color(th.Text)).
				Background(lipgloss.Color(th.Success)).
				Bold(true).
				Padding(0, 1).
				Margin(0, toggleMargin)
			toggleText = i18n.T("settings.common.toggle.on")
		} else {
			toggleBg := th.BGLight
			if toggleBg == "" {
				toggleBg = bg
			}
			toggleStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color(th.TextMuted)).
				Background(lipgloss.Color(toggleBg)).
				Padding(0, 1).
				Margin(0, toggleMargin)
			toggleText = i18n.T("settings.common.toggle.off")
		}

		if isSelected {
			toggleStyle = toggleStyle.
				Foreground(lipgloss.Color(th.Primary)).
				Bold(true)
		}

		toggle := toggleStyle.Render(toggleText)
		lines = append(lines, toggle, "")
	}

	// Spinner Type Selector (item 5)
	spinnerIdx := 6
	isSpinnerSelected := spinnerIdx == state.SelectedItem && state.Focus == FocusContent

	// Section divider
	dividerStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(th.Border)).
		Background(lipgloss.Color(bg)).
		Padding(0, 2)
	lines = append(lines, dividerStyle.Render(strings.Repeat("─", maxInt(5, width-8))))

	// Spinner section header
	spinnerHeaderStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(th.Text)).
		Background(lipgloss.Color(bg)).
		Bold(isSpinnerSelected).
		Padding(0, 2)
	if isSpinnerSelected {
		spinnerHeaderStyle = spinnerHeaderStyle.Foreground(lipgloss.Color(th.Primary))
	}
	lines = append(lines, spinnerHeaderStyle.Render(i18n.T("settings.display.spinner.label", map[bool]string{true: "▶", false: " "}[isSpinnerSelected])))

	// Description
	spinnerDescPadH := 4
	if width < 50 {
		spinnerDescPadH = 2
	}
	spinnerDescStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(th.TextMuted)).
		Background(lipgloss.Color(bg)).
		Padding(0, spinnerDescPadH)
	lines = append(lines, spinnerDescStyle.Render(i18n.T("settings.display.spinner.description")))
	lines = append(lines, "")

	// Use animation frame from shared clock for live preview
	frame := animationFrame

	// At narrow widths, show a simplified spinner list (no live preview)
	showPreview := width >= 60

	// Render all spinner types
	for i, spinnerType := range SpinnerTypes {
		isCurrent := spinnerType == d.spinnerType

		// Style based on selection
		rowPadH := 4
		if width < 50 {
			rowPadH = 2
		}
		var rowStyle lipgloss.Style
		if isCurrent {
			rowStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color(th.Primary)).
				Background(lipgloss.Color(bg)).
				Bold(true).
				Padding(0, rowPadH)
		} else {
			rowStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color(th.Text)).
				Background(lipgloss.Color(bg)).
				Padding(0, rowPadH)
		}

		// Selection indicator
		selector := "  "
		if isCurrent {
			selector = "● "
		}

		// Arrow indicators when selected
		arrows := ""
		if isSpinnerSelected && isCurrent {
			arrows = " ◀ ▶"
		}

		// Build the row — include preview only at wider widths
		nameWidth := 12
		name := fmt.Sprintf("%-*s", nameWidth, localizedSpinnerName(spinnerType))

		var row string
		if showPreview {
			preview := d.renderSpinnerPreview(spinnerType, frame)
			desc := localizedSpinnerDescription(spinnerType)
			row = fmt.Sprintf("%s%s  %s  %s%s", selector, preview, rowStyle.Render(name), spinnerDescStyle.Render(desc), arrows)
		} else {
			// Compact: just indicator + name + arrows
			row = fmt.Sprintf("%s%s%s", selector, rowStyle.Render(name), arrows)
		}

		// Highlight current selection when focused
		if isSpinnerSelected && isCurrent {
			highlightBg := th.BGLight
			if highlightBg == "" {
				highlightBg = bg
			}
			highlightStyle := lipgloss.NewStyle().
				Background(lipgloss.Color(highlightBg)).
				Width(maxInt(20, width-8))
			row = highlightStyle.Render(row)
		}

		indent := "    "
		if width < 50 {
			indent = "  "
		}
		lines = append(lines, indent+row)

		// Add spacing between options
		if i < len(SpinnerTypes)-1 {
			lines = append(lines, "")
		}
	}

	// Hint bar with keyboard shortcuts
	lines = append(lines, "")
	content := lipgloss.JoinVertical(lipgloss.Left, lines...)

	// Build hint bar
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

	var hintText string
	if width < 50 {
		hintText = i18n.T("settings.display.hints.compact",
			keyStyle.Render("↑↓"),
			keyStyle.Render("Spc"))
	} else {
		hintText = i18n.T("settings.display.hints.full",
			keyStyle.Render("↑/↓"),
			keyStyle.Render("Space"),
			keyStyle.Render("←/→"))
	}
	hints := hintStyle.Render(hintText)

	// Combine content with hints
	fullContent := lipgloss.JoinVertical(lipgloss.Left,
		content,
		hints,
	)

	// Apply container border with rounded style
	containerStyle := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color(th.Border)).
		Background(lipgloss.Color(bg)).
		Width(maxInt(20, width-2)).
		Padding(0, 1)

	return containerStyle.Render(fullContent)
}

func localizedSpinnerName(spinnerType string) string {
	keys := map[string]string{
		"Matrix":    "settings.display.spinner.matrix",
		"Braille":   "settings.display.spinner.braille",
		"Blocks":    "settings.display.spinner.blocks",
		"Wave":      "settings.display.spinner.wave",
		"Pulse":     "settings.display.spinner.pulse",
		"Bounce":    "settings.display.spinner.bounce",
		"Aurora":    "settings.display.spinner.aurora",
		"DNA Helix": "settings.display.spinner.dna",
	}
	if key, ok := keys[spinnerType]; ok {
		return i18n.T(key)
	}
	return spinnerType
}

func localizedSpinnerDescription(spinnerType string) string {
	keys := map[string]string{
		"Matrix":    "settings.display.spinner.matrix.description",
		"Braille":   "settings.display.spinner.braille.description",
		"Blocks":    "settings.display.spinner.blocks.description",
		"Wave":      "settings.display.spinner.wave.description",
		"Pulse":     "settings.display.spinner.pulse.description",
		"Bounce":    "settings.display.spinner.bounce.description",
		"Aurora":    "settings.display.spinner.aurora.description",
		"DNA Helix": "settings.display.spinner.dna.description",
	}
	if key, ok := keys[spinnerType]; ok {
		return i18n.T(key)
	}
	return SpinnerTypeDescriptions[spinnerType]
}

// renderSpinnerPreview renders a live preview of a spinner type
func (d *DisplaySettings) renderSpinnerPreview(spinnerType string, frame int) string {
	switch spinnerType {
	case "Matrix":
		return d.previewMatrix(frame)
	case "Braille":
		return d.previewBraille(frame)
	case "Blocks":
		return d.previewBlocks(frame)
	case "Wave":
		return d.previewWave(frame)
	case "Pulse":
		return d.previewPulse(frame)
	case "Bounce":
		return d.previewBounce(frame)
	case "Aurora":
		return d.previewAurora(frame)
	case "DNA Helix":
		return d.previewDNA(frame)
	default:
		return d.previewMatrix(frame)
	}
}

func (d *DisplaySettings) previewMatrix(frame int) string {
	chars := []rune("0123456789abcdefABCDEF~!@#$%^&*+=")
	colors := []string{"#FF6B6B", "#FF8E72", "#FFB07A", "#FFD182", "#E8E46E", "#A8E6CF", "#88D8B0", "#4ECDC4"}
	var result strings.Builder
	for i := range 8 {
		charIdx := (frame + i*3) % len(chars)
		colorIdx := i % len(colors)
		style := lipgloss.NewStyle().Foreground(lipgloss.Color(colors[colorIdx])).Bold(true)
		result.WriteString(style.Render(string(chars[charIdx])))
	}
	return result.String()
}

func (d *DisplaySettings) previewBraille(frame int) string {
	braille := []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}
	idx := frame % len(braille)
	style := lipgloss.NewStyle().Foreground(lipgloss.Color("#4ECDC4")).Bold(true)
	return style.Render(braille[idx]) + "       " // Pad to same width
}

func (d *DisplaySettings) previewBlocks(frame int) string {
	blocks := []string{"▏", "▎", "▍", "▌", "▋", "▊", "▉", "█", "▉", "▊", "▋", "▌", "▍", "▎", "▏", " "}
	colors := []string{"#FF6B6B", "#FF8E72", "#FFB07A", "#FFD182", "#E8E46E", "#A8E6CF", "#88D8B0", "#4ECDC4"}
	var result strings.Builder
	for i := range 8 {
		idx := (frame + i*2) % len(blocks)
		colorIdx := i % len(colors)
		style := lipgloss.NewStyle().Foreground(lipgloss.Color(colors[colorIdx]))
		result.WriteString(style.Render(blocks[idx]))
	}
	return result.String()
}

func (d *DisplaySettings) previewWave(frame int) string {
	chars := []string{"▁", "▂", "▃", "▄", "▅", "▆", "▇", "█"}
	colors := []string{"#FF6B6B", "#FF8E72", "#FFB07A", "#FFD182", "#E8E46E", "#A8E6CF", "#88D8B0", "#4ECDC4"}
	var result strings.Builder
	for i := range 8 {
		phase := float64(frame)/5.0 + float64(i)*0.5
		height := (sin(phase) + 1) / 2
		idx := int(height * float64(len(chars)-1))
		colorIdx := i % len(colors)
		style := lipgloss.NewStyle().Foreground(lipgloss.Color(colors[colorIdx]))
		result.WriteString(style.Render(chars[idx]))
	}
	return result.String()
}

func (d *DisplaySettings) previewPulse(frame int) string {
	circles := []string{"○", "◔", "◑", "◕", "●", "◕", "◑", "◔"}
	colors := []string{"#555555", "#666666", "#888888", "#AAAAAA", "#4ECDC4", "#AAAAAA", "#888888", "#666666"}
	var result strings.Builder
	for i := range 5 {
		idx := (frame + i*2) % len(circles)
		style := lipgloss.NewStyle().Foreground(lipgloss.Color(colors[idx]))
		result.WriteString(style.Render(circles[idx]))
	}
	return result.String() + "   " // Pad to same width
}

func (d *DisplaySettings) previewBounce(frame int) string {
	width := 8
	pos := frame % (width * 2)
	if pos >= width {
		pos = width*2 - pos - 1
	}
	var result strings.Builder
	for i := range width {
		if i == pos {
			style := lipgloss.NewStyle().Foreground(lipgloss.Color("#4ECDC4")).Bold(true)
			result.WriteString(style.Render("●"))
		} else if i == pos-1 || i == pos+1 {
			style := lipgloss.NewStyle().Foreground(lipgloss.Color("#2A9D8F"))
			result.WriteString(style.Render("○"))
		} else {
			style := lipgloss.NewStyle().Foreground(lipgloss.Color("#333333"))
			result.WriteString(style.Render("·"))
		}
	}
	return result.String()
}

func (d *DisplaySettings) previewAurora(frame int) string {
	chars := []string{"░", "▒", "▓", "█", "▓", "▒", "░", " "}
	auroraColors := []string{"#00FF87", "#00E676", "#1DE9B6", "#64FFDA", "#00BFA5", "#26A69A", "#4DB6AC", "#80CBC4"}
	var result strings.Builder
	for i := range 8 {
		charIdx := (frame/2 + i) % len(chars)
		colorIdx := (frame + i) % len(auroraColors)
		style := lipgloss.NewStyle().Foreground(lipgloss.Color(auroraColors[colorIdx]))
		result.WriteString(style.Render(chars[charIdx]))
	}
	return result.String()
}

func (d *DisplaySettings) previewDNA(frame int) string {
	width := 8
	var result strings.Builder
	for i := range width {
		phase := float64(frame)/3.0 + float64(i)*0.5
		y1 := sin(phase)
		y2 := sin(phase + 3.14159)

		var char string
		var color string
		if y1 > y2 {
			if y1 > 0.3 {
				char = "◉"
				color = "#FF6B6B"
			} else if y1 > -0.3 {
				char = "─"
				color = "#666666"
			} else {
				char = "◎"
				color = "#4ECDC4"
			}
		} else {
			if y2 > 0.3 {
				char = "◎"
				color = "#4ECDC4"
			} else if y2 > -0.3 {
				char = "─"
				color = "#666666"
			} else {
				char = "◉"
				color = "#FF6B6B"
			}
		}
		style := lipgloss.NewStyle().Foreground(lipgloss.Color(color))
		result.WriteString(style.Render(char))
	}
	return result.String()
}

// sin is a simple sine function wrapper
func sin(x float64) float64 {
	// Simple Taylor series approximation for sin
	x = x - float64(int(x/(2*3.14159)))*2*3.14159
	if x > 3.14159 {
		x -= 2 * 3.14159
	}
	x3 := x * x * x
	x5 := x3 * x * x
	return x - x3/6 + x5/120
}
