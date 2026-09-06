package components

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/chatui/theme"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/i18n"
)

// SidePanel renders the side panel with tools, settings, and status.
type SidePanel struct {
	theme  theme.Theme
	width  int
	height int

	// State
	tokenCount      int
	contextWindow   int
	tools           []ToolInfo
	showThinking    bool
	showVerbose     bool
	cachingOn       bool
	operatingMode   string
	permissionLevel string
}

// ToolInfo represents a tool in the side panel.
type ToolInfo struct {
	Name    string
	Enabled bool
	Count   int // Number of times used
}

// NewSidePanel creates a new side panel component.
func NewSidePanel(th theme.Theme) *SidePanel {
	return &SidePanel{
		theme:  th,
		width:  32,
		height: 20,
	}
}

// SetSize updates the side panel dimensions.
func (s *SidePanel) SetSize(width, height int) {
	s.width = width
	s.height = height
}

// SetTokenInfo sets the token count and context window.
func (s *SidePanel) SetTokenInfo(count, window int) {
	s.tokenCount = count
	s.contextWindow = window
}

// SetTools sets the list of tools.
func (s *SidePanel) SetTools(tools []ToolInfo) {
	s.tools = tools
}

// SetToggles sets the toggle states.
func (s *SidePanel) SetToggles(showThinking, showVerbose, cachingOn bool) {
	s.showThinking = showThinking
	s.showVerbose = showVerbose
	s.cachingOn = cachingOn
}

// SetPermissionLevel sets the current permission level.
func (s *SidePanel) SetPermissionLevel(level string) {
	s.permissionLevel = level
}

// SetOperatingMode sets the current operating mode.
func (s *SidePanel) SetOperatingMode(mode string) {
	s.operatingMode = mode
}

// View renders the side panel.
func (s *SidePanel) View() string {
	th := s.theme
	w := s.width - 2 // Padding

	// Styles
	panelStyle := lipgloss.NewStyle().
		Width(s.width).
		Height(s.height).
		Background(lipgloss.Color(th.BackgroundLightColor())).
		Padding(0, 1)

	headerStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(th.TextColor())).
		Bold(true)

	labelStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(th.TextDimColor()))

	valueStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(th.TextColor()))

	dimStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(th.TextMutedColor()))

	successStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(th.SuccessColor()))

	var lines []string

	// Token meter section
	lines = append(lines, headerStyle.Render(i18n.T("chatui.side.context")))
	lines = append(lines, s.renderTokenMeter(w, th))
	lines = append(lines, "")

	// Permissions section
	if s.permissionLevel != "" {
		lines = append(lines, headerStyle.Render(i18n.T("chatui.side.permissions")))
		permDisplay := formatPermissionLevel(s.permissionLevel)
		lines = append(lines, labelStyle.Render("  ")+valueStyle.Render(permDisplay))
		lines = append(lines, labelStyle.Render(i18n.T("chatui.side.permission_cycle")))
		lines = append(lines, "")
	}

	// Mode section
	if s.operatingMode != "" {
		lines = append(lines, headerStyle.Render(i18n.T("chatui.side.mode")))
		modeDisplay := s.operatingMode
		if modeDisplay == "off" {
			modeDisplay = i18n.T("chatui.side.mode_off")
		}
		lines = append(lines, labelStyle.Render("  ")+valueStyle.Render(modeDisplay))
		lines = append(lines, "")
	}

	// Toggles section
	lines = append(lines, headerStyle.Render(i18n.T("chatui.side.options")))
	lines = append(lines, s.renderToggle(i18n.T("chatui.side.thinking"), s.showThinking, th))
	lines = append(lines, s.renderToggle(i18n.T("chatui.side.verbose"), s.showVerbose, th))
	lines = append(lines, s.renderToggle(i18n.T("chatui.side.caching"), s.cachingOn, th))
	lines = append(lines, "")

	// Tools section (if any)
	if len(s.tools) > 0 {
		lines = append(lines, headerStyle.Render(i18n.T("chatui.side.tools")))
		maxTools := max(
			// Leave room
			s.height-len(lines)-2, 3)
		for i, tool := range s.tools {
			if i >= maxTools {
				remaining := len(s.tools) - maxTools
				lines = append(lines, dimStyle.Render(i18n.T("chatui.side.more", remaining)))
				break
			}
			icon := "○"
			style := dimStyle
			if tool.Enabled {
				icon = "●"
				style = successStyle
			}
			toolLine := fmt.Sprintf("  %s %s", icon, tool.Name)
			if tool.Count > 0 {
				toolLine += dimStyle.Render(fmt.Sprintf(" (%d)", tool.Count))
			}
			lines = append(lines, style.Render(toolLine))
		}
	}

	// Pad to fill height
	for len(lines) < s.height {
		lines = append(lines, "")
	}

	content := strings.Join(lines[:s.height], "\n")
	return panelStyle.Render(content)
}

// renderTokenMeter renders a visual token usage meter.
func (s *SidePanel) renderTokenMeter(width int, th theme.Theme) string {
	if s.contextWindow == 0 {
		return i18n.T("chatui.side.no_context")
	}

	// Calculate percentage
	pct := float64(s.tokenCount) / float64(s.contextWindow)
	if pct > 1.0 {
		pct = 1.0
	}

	// Create bar
	barWidth := max(width-4, 10)
	filled := min(int(float64(barWidth)*pct), barWidth)

	// Choose color based on usage
	var barColor string
	switch {
	case pct > 0.9:
		barColor = th.ErrorColor()
	case pct > 0.7:
		barColor = th.WarningColor()
	default:
		barColor = th.SuccessColor()
	}

	filledStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(barColor))
	emptyStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(th.BorderColor()))

	bar := filledStyle.Render(strings.Repeat("█", filled)) +
		emptyStyle.Render(strings.Repeat("░", barWidth-filled))

	// Format numbers
	tokenStr := formatNumber(s.tokenCount)
	windowStr := formatNumber(s.contextWindow)
	label := fmt.Sprintf("  %s / %s", tokenStr, windowStr)

	return "  " + bar + "\n" + lipgloss.NewStyle().Foreground(lipgloss.Color(th.TextDimColor())).Render(label)
}

// renderToggle renders a toggle option.
func (s *SidePanel) renderToggle(name string, on bool, th theme.Theme) string {
	onStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(th.SuccessColor()))
	offStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(th.TextMutedColor()))
	labelStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(th.TextDimColor()))

	var toggle string
	if on {
		toggle = onStyle.Render(i18n.T("chatui.side.toggle_on"))
	} else {
		toggle = offStyle.Render(i18n.T("chatui.side.toggle_off"))
	}

	return "  " + toggle + " " + labelStyle.Render(name)
}

// formatNumber formats a number with K/M suffix.
func formatNumber(n int) string {
	if n >= 1000000 {
		return fmt.Sprintf("%.1fM", float64(n)/1000000)
	}
	if n >= 1000 {
		return fmt.Sprintf("%.1fK", float64(n)/1000)
	}
	return fmt.Sprintf("%d", n)
}

func formatPermissionLevel(level string) string {
	switch level {
	case "always_ask":
		return i18n.T("chatui.side.permission_always_ask")
	case "permissive":
		return i18n.T("chatui.side.permission_permissive")
	default:
		return i18n.T("chatui.side.permission_balanced")
	}
}
