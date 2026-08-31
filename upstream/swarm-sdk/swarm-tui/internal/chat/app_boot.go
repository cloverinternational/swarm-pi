package chat

import (
	"math/rand"
	"strings"

	"charm.land/lipgloss/v2"
)

// ============================================================================
// NAVIGATION ACTIONS
// ============================================================================
// Note: startNewChat and openConversation are now in chat_state.go

// ============================================================================
// BOOT ANIMATION - SCANLINES + VHS NOISE
// ============================================================================

func (a *App) viewBoot() string {
	th := a.theme

	midY := a.height / 2
	// Smooth progress - divide by 2 for half-pixel movement
	progress := a.bootFrame / 2

	// Scanlines converge from top and bottom to middle
	topScan := progress
	botScan := a.height - 1 - progress

	logDebug("viewBoot: width=%d height=%d frame=%d progress=%d midY=%d",
		a.width, a.height, a.bootFrame, progress, midY)

	if progress >= midY {
		// Animation complete - show final screen
		return a.renderFinalBoot()
	}

	var lines []string
	for y := 0; y < a.height; y++ {
		var line string

		// Main scanline (bright)
		if y == topScan || y == botScan {
			line = lipgloss.NewStyle().
				Foreground(lipgloss.Color(th.Primary)).
				Render(strings.Repeat("━", a.width))
		} else if y == topScan+1 || y == botScan-1 {
			// Secondary line (dimmer)
			line = lipgloss.NewStyle().
				Foreground(lipgloss.Color(th.PrimaryDim)).
				Render(strings.Repeat("─", a.width))
		} else if y > topScan+1 && y < botScan-1 {
			// Area between scanlines - add VHS noise (contained)
			line = a.renderVHSNoiseLine(0.02, th)
		} else {
			// Just outside scanlines - very minimal noise (1-2 rows only)
			if y == topScan-1 || y == botScan+1 {
				line = a.renderVHSNoiseLine(0.005, th)
			} else {
				// Empty line - no noise escape
				line = strings.Repeat(" ", a.width)
			}
		}

		lines = append(lines, line)
	}

	content := lipgloss.JoinVertical(lipgloss.Left, lines...)

	bgStyle := lipgloss.NewStyle().Background(lipgloss.Color(th.BG))
	return lipgloss.Place(
		a.width, a.height,
		lipgloss.Left, lipgloss.Top,
		content,
		lipgloss.WithWhitespaceStyle(bgStyle),
	)
}

func (a *App) renderFinalBoot() string {
	th := a.theme

	title := lipgloss.NewStyle().
		Foreground(lipgloss.Color(th.Primary)).
		Bold(true).
		Render("swarm")

	content := lipgloss.JoinVertical(lipgloss.Center, title)

	bgStyle := lipgloss.NewStyle().Background(lipgloss.Color(th.BG))
	return lipgloss.Place(
		a.width, a.height,
		lipgloss.Center, lipgloss.Center,
		content,
		lipgloss.WithWhitespaceStyle(bgStyle),
	)
}

func (a *App) renderVHSNoiseLine(density float64, th Theme) string {
	var b strings.Builder
	noiseChars := []rune{'░', '▒', '·', '.', ':'}
	noiseColors := []string{th.Border, th.TextMuted, th.BGLighter}

	// Occasional line shift for VHS effect
	shift := 0
	if rand.Float64() < 0.05 {
		shift = rand.Intn(6) - 3
		if shift > 0 {
			b.WriteString(strings.Repeat(" ", shift))
		}
	}

	lineLen := a.width - abs(shift)
	if lineLen < 0 {
		lineLen = 0
	}

	for x := 0; x < lineLen; x++ {
		if rand.Float64() < density {
			char := noiseChars[rand.Intn(len(noiseChars))]
			color := noiseColors[rand.Intn(len(noiseColors))]
			b.WriteString(lipgloss.NewStyle().
				Foreground(lipgloss.Color(color)).
				Render(string(char)))
		} else {
			b.WriteString(" ")
		}
	}

	return b.String()
}

func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}
