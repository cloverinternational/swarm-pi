package chat

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// contextGaugeBarCells is the fill width (in terminal cells) of the context
// usage gauge embedded in the chat's bottom separator.
const contextGaugeBarCells = 10

// renderBottomSeparatorWithGauge returns the bottom separator line with an
// embedded live context-usage gauge, e.g.
//
//	─────────────────────── ctx ██████░░░░ 62% · compact @85% ──
//
// Data sources: a.tokenCount / a.modelContextWindow (kept live by
// tokenUpdateMsg) and a.autoCompactThresholdTokens (the SDK's auto-compaction
// threshold). Returns "" when token data is unavailable or the terminal is too
// narrow — the caller then falls back to the plain cached separator.
//
// The rendered string is cached keyed on width|percent|threshold so
// steady-state frames don't re-run lipgloss (the same reason the plain
// separator render is cached — see cachedSeparator).
func (a *App) renderBottomSeparatorWithGauge() string {
	if a.tokenCount <= 0 || a.modelContextWindow <= 0 {
		return ""
	}
	pct := a.tokenCount * 100 / a.modelContextWindow
	if pct > 100 {
		pct = 100
	}
	thrPct := 0
	if a.autoCompactThresholdTokens > 0 && a.autoCompactThresholdTokens < a.modelContextWindow {
		thrPct = a.autoCompactThresholdTokens * 100 / a.modelContextWindow
	}
	key := fmt.Sprintf("%d|%d|%d", a.width, pct, thrPct)
	if a.cachedCtxGauge.key == key && a.cachedCtxGauge.rendered != "" {
		return a.cachedCtxGauge.rendered
	}

	filled := pct * contextGaugeBarCells / 100
	if filled > contextGaugeBarCells {
		filled = contextGaugeBarCells
	}
	// Only 1-cell runes are used below, so visible width == rune count and no
	// grapheme-measuring pass is needed to size the surrounding dashes.
	label := fmt.Sprintf(" ctx %s%s %d%%",
		strings.Repeat("█", filled),
		strings.Repeat("░", contextGaugeBarCells-filled),
		pct,
	)
	if thrPct > 0 {
		label += fmt.Sprintf(" · compact @%d%%", thrPct)
	}
	label += " "
	visible := len([]rune(label))
	const rightStub = 2
	if a.width < visible+rightStub+8 {
		return ""
	}

	th := a.theme
	fillColor := th.Success
	switch {
	case (thrPct > 0 && pct >= thrPct) || pct >= 90:
		fillColor = th.Error
	case (thrPct > 0 && pct >= thrPct*3/4) || pct >= 70:
		fillColor = th.Warning
	}
	sepStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(th.Border))
	gaugeStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(fillColor))
	rendered := sepStyle.Render(strings.Repeat("─", a.width-visible-rightStub)) +
		gaugeStyle.Render(label) +
		sepStyle.Render(strings.Repeat("─", rightStub))

	a.cachedCtxGauge.key = key
	a.cachedCtxGauge.rendered = rendered
	return rendered
}
