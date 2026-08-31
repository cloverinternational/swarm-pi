package chat

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/bgprocess"
)

// bashActivityDefaultCap bounds the scrolling window of BASH rows in the side
// panel. Variable so tests can tune it.
var bashActivityDefaultCap = 5

// bashTailLinesPerRow is how many trailing output lines are shown beneath each
// running command when live tails are supplied.
const bashTailLinesPerRow = 2

// bashRow is a fully-resolved, render-ready view of one Bash command.
type bashRow struct {
	id        string
	command   string
	state     bgprocess.ProcessState
	startedAt time.Time
	elapsed   time.Duration
	exitCode  *int
	tail      []string
}

// bashTheme carries only the color roles the BASH section needs.
type bashTheme struct {
	Success  string
	Warning  string
	Error    string
	Accent   string
	Text     string
	TextDim  string
	TextMute string
	BG       string
}

// bashActivityInput is the side-effect-free input to renderBashActivity.
type bashActivityInput struct {
	rows        []bashRow
	width       int
	cap         int
	focused     bool
	selectedIdx int
	theme       bashTheme
}

// buildBashRows converts process infos into render-ready rows, pulling a live
// output tail for running commands via tailFn (may be nil). Running rows first.
func buildBashRows(procs []bgprocess.ProcessInfo, tailFn func(id string, n int) []string, now time.Time) []bashRow {
	rows := make([]bashRow, 0, len(procs))
	for _, p := range procs {
		r := bashRow{
			id:        p.Handle.ID(),
			command:   p.Command,
			state:     p.State,
			startedAt: p.StartedAt,
			exitCode:  p.ExitCode,
		}
		if p.State == bgprocess.StateRunning {
			r.elapsed = now.Sub(p.StartedAt)
			if tailFn != nil {
				r.tail = tailFn(r.id, bashTailLinesPerRow)
			}
		} else if p.CompletedAt != nil {
			r.elapsed = p.CompletedAt.Sub(p.StartedAt)
		}
		rows = append(rows, r)
	}
	sort.Slice(rows, func(i, j int) bool {
		ri, rj := rows[i].state == bgprocess.StateRunning, rows[j].state == bgprocess.StateRunning
		if ri != rj {
			return ri
		}
		if !rows[i].startedAt.Equal(rows[j].startedAt) {
			return rows[i].startedAt.After(rows[j].startedAt)
		}
		return rows[i].id < rows[j].id
	})
	return rows
}

// renderBashActivity renders the BASH section body. Element 0 is the plain count
// string so it plugs into the existing count-merge logic in SidePanel.Render.
func renderBashActivity(in bashActivityInput) []string {
	if len(in.rows) == 0 {
		return nil
	}
	th := in.theme
	width := in.width
	if width < 8 {
		width = 8
	}
	limit := in.cap
	if limit <= 0 {
		limit = bashActivityDefaultCap
	}

	style := func(text, color string) string {
		return lipgloss.NewStyle().
			Foreground(lipgloss.Color(color)).
			Background(lipgloss.Color(th.BG)).
			Render(text)
	}
	wrap := func(line string) string { return reapplyBackground(line, th.BG) }

	body := make([]string, 0, len(in.rows)*2+2)
	// Header: a self-describing summary line (never a bare number, so the side
	// panel digit-merge heuristic leaves it as its own styled row).
	body = append(body, wrap(bashSummaryLine(in.rows, th, style)))

	shown, windowStart := bashVisibleWindow(in.rows, in.selectedIdx, limit)
	hiddenAbove := windowStart
	hiddenBelow := len(in.rows) - windowStart - len(shown)
	if hiddenAbove > 0 {
		body = append(body, wrap(style(fmt.Sprintf("  ↑ %d more", hiddenAbove), th.TextDim)))
	}

	prevGroupRunning := true
	firstRow := true
	for i, r := range shown {
		absoluteIdx := windowStart + i
		running := r.state == bgprocess.StateRunning
		if !firstRow && running != prevGroupRunning {
			body = append(body, wrap(style(strings.Repeat("─", width), th.TextDim)))
		}
		prevGroupRunning = running
		firstRow = false

		icon, iconColor := bashGlyph(r.state, r.elapsed, th)

		metric := formatElapsed(r.elapsed)
		metricColor := th.TextDim
		if !running {
			if r.exitCode != nil && *r.exitCode != 0 {
				metric = fmt.Sprintf("%s ✘%d", metric, *r.exitCode)
				metricColor = th.Error
			} else if r.exitCode != nil {
				metric = metric + " ✔"
				metricColor = th.Success
			}
		}

		selected := in.focused && absoluteIdx == in.selectedIdx
		row := bashComposeRow(icon, iconColor, r.command, metric, metricColor, width, selected, th, style)
		body = append(body, wrap(row))

		if running && len(r.tail) > 0 {
			for _, tl := range r.tail {
				tl = strings.TrimRight(tl, "\r\n")
				if tl == "" {
					continue
				}
				prefix := "  │ "
				avail := width - len([]rune(prefix))
				if avail < 4 {
					avail = 4
				}
				tl = truncateStr(tl, avail)
				body = append(body, wrap(style(prefix+tl, th.TextDim)))
			}
		}
	}

	if hiddenBelow > 0 {
		body = append(body, wrap(style(fmt.Sprintf("  ↓ %d more", hiddenBelow), th.TextDim)))
	}
	if in.focused {
		body = append(body, wrap(style("  ↑↓ move · enter view   k kill · esc back", th.Accent)))
	} else {
		body = append(body, wrap(style("  alt+b focus", th.TextDim)))
	}
	return body
}

// bashGlyph returns the status glyph and its color for a process state.
func bashGlyph(state bgprocess.ProcessState, elapsed time.Duration, th bashTheme) (string, string) {
	switch state {
	case bgprocess.StateRunning:
		return rotatingBashGlyph(elapsed), th.Warning
	case bgprocess.StateCompleted:
		return "✔", th.Success
	case bgprocess.StateFailed:
		return "✘", th.Error
	case bgprocess.StateCancelled:
		return "⊘", th.TextMute
	default:
		return "○", th.TextMute
	}
}

// bashComposeRow lays out icon + command + right-aligned metric within width.
func bashComposeRow(icon, iconColor, command, metric, metricColor string, width int, selected bool, th bashTheme, style func(text, color string) string) string {
	pointer := "  "
	if selected {
		pointer = style("❯ ", th.Accent)
	}
	pointerW := 2

	iconPart := style(icon+" ", iconColor)
	iconW := 2

	metricW := 0
	metricPart := ""
	if metric != "" {
		metricPart = style(metric, metricColor)
		metricW = len([]rune(metric))
	}

	cmdWidth := width - pointerW - iconW - metricW - 1
	if cmdWidth < 4 {
		cmdWidth = 4
	}
	cmd := truncateStr(command, cmdWidth)
	cmdColor := th.TextMute
	if selected {
		cmdColor = th.Text
	}
	cmdPart := style(cmd, cmdColor)

	used := pointerW + iconW + len([]rune(cmd)) + metricW
	gap := width - used
	if gap < 1 {
		gap = 1
	}
	return pointer + iconPart + cmdPart + strings.Repeat(" ", gap) + metricPart
}

// bashSummaryLine builds a compact, styled status summary such as
// "2 running · 1 done · 1 failed" for the section header.
func bashSummaryLine(rows []bashRow, th bashTheme, style func(text, color string) string) string {
	var running, done, failed, other int
	for _, r := range rows {
		switch r.state {
		case bgprocess.StateRunning:
			running++
		case bgprocess.StateCompleted:
			done++
		case bgprocess.StateFailed:
			failed++
		default:
			other++
		}
	}
	sep := style(" · ", th.TextDim)
	parts := make([]string, 0, 4)
	if running > 0 {
		parts = append(parts, style(fmt.Sprintf("%d running", running), th.Warning))
	}
	if done > 0 {
		parts = append(parts, style(fmt.Sprintf("%d done", done), th.Success))
	}
	if failed > 0 {
		parts = append(parts, style(fmt.Sprintf("%d failed", failed), th.Error))
	}
	if other > 0 {
		parts = append(parts, style(fmt.Sprintf("%d other", other), th.TextMute))
	}
	if len(parts) == 0 {
		return style("idle", th.TextDim)
	}
	out := parts[0]
	for _, prt := range parts[1:] {
		out += sep + prt
	}
	return "  " + out
}
