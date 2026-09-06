package chat

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/bgprocess"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/chat/toolrender/shared"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/i18n"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/ui/palette"
	"github.com/charmbracelet/x/ansi"
)

// Bash dock sizing.
const (
	bashDockMaxRows  = 5 // max command rows shown when focused
	bashDockTailRows = 5 // live tail rows under the selected command
)

// bashDockEntry is a normalized view of one background bash process for the dock.
type bashDockEntry struct {
	TaskID      string
	Command     string
	State       bgprocess.ProcessState
	Running     bool
	ExitCode    *int
	Elapsed     time.Duration
	StartedAt   time.Time
	CompletedAt time.Time // zero while running

	// IsAgent marks this entry as a background sub-agent rather than a bash
	// command. AgentID is set only for sub-agent entries. Sub-agents reuse the
	// same dock + Enter-to-view takeover as bash commands.
	IsAgent bool
	AgentID string
}

// ID returns the stable identifier used to anchor selection across refreshes.
func (e bashDockEntry) ID() string {
	if e.IsAgent {
		return "agent:" + e.AgentID
	}
	return "bash:" + e.TaskID
}

// dockFinishedLinger is how long a finished command/agent stays in the dock
// after completion before it ages out. Keeps just-finished work visible without
// letting old entries pile up (the process manager itself retains them ~30min).
const dockFinishedLinger = 30 * time.Second

// sortDockEntries orders entries deterministically: running first, then most
// recently started first, with the stable ID as the final tiebreak. This keeps
// the list stable across refreshes regardless of the manager's map iteration
// order (which is otherwise non-deterministic and caused the list to reshuffle).
func sortDockEntries(entries []bashDockEntry) {
	sort.SliceStable(entries, func(i, j int) bool {
		a, b := entries[i], entries[j]
		if a.Running != b.Running {
			return a.Running // running before finished
		}
		if !a.StartedAt.Equal(b.StartedAt) {
			return a.StartedAt.After(b.StartedAt) // newest first
		}
		return a.ID() < b.ID()
	})
}

// collectBashDockEntries turns the manager's ProcessInfo list into dock entries,
// excluding the current foreground command. Finished entries older than
// dockFinishedLinger are dropped so stale commands don't linger. Ordering is
// applied by the caller via sortDockEntries for stability.
func collectBashDockEntries(procs []bgprocess.ProcessInfo, foregroundID string) []bashDockEntry {
	var entries []bashDockEntry
	now := time.Now()
	for _, p := range procs {
		id := p.Handle.ID()
		if id == foregroundID && foregroundID != "" {
			continue
		}
		isRunning := !p.State.IsTerminal()

		var completedAt time.Time
		if p.CompletedAt != nil {
			completedAt = *p.CompletedAt
		}
		// Age out finished commands after a short linger window.
		if !isRunning && !completedAt.IsZero() && now.Sub(completedAt) > dockFinishedLinger {
			continue
		}

		elapsed := p.Duration
		if isRunning && !p.StartedAt.IsZero() {
			elapsed = now.Sub(p.StartedAt)
		}
		entries = append(entries, bashDockEntry{
			TaskID:      id,
			Command:     p.Command,
			State:       p.State,
			Running:     isRunning,
			ExitCode:    p.ExitCode,
			Elapsed:     elapsed,
			StartedAt:   p.StartedAt,
			CompletedAt: completedAt,
		})
	}
	return entries
}

// renderBashDock renders the compact bash dock shown under the chatbox.
//
// Behavior:
//   - No background commands           -> ("", 0)   (fully hidden when idle)
//   - Commands exist, dock NOT focused  -> a single dim hint line
//   - Dock focused                      -> a compact list (glyph status command
//     elapsed) with the selected row highlighted and a short live output tail.
//
// Returns the rendered block and its exact line count (so the caller can reserve
// viewport height). tailFn returns the last n output lines for a task_id.
func renderBashDock(
	entries []bashDockEntry,
	selectedIdx int,
	focused bool,
	width int,
	bgColor string,
	tailFn func(taskID string, n int) []string,
) (string, int) {
	if len(entries) == 0 {
		return "", 0
	}
	if width < 20 {
		width = 20
	}

	runningCount := 0
	for _, e := range entries {
		if e.Running {
			runningCount++
		}
	}

	// ── Unfocused: one compact hint line.
	if !focused {
		label := i18n.T("classic_chat.bash.tasks", len(entries))
		if len(entries) == 1 {
			label = i18n.T("classic_chat.bash.one_task")
		}
		if runningCount > 0 && runningCount != len(entries) {
			label = i18n.T("classic_chat.bash.tasks_running", len(entries), runningCount)
		}
		hint := dockStyle(palette.TextDim, bgColor).Render(label) +
			dockStyle(palette.TextMuted, bgColor).Render(i18n.T("classic_chat.bash.view_hint"))
		return clampWidth(hint, width, bgColor), 1
	}

	// ── Focused: compact list + selected tail.
	if selectedIdx < 0 {
		selectedIdx = 0
	}
	if selectedIdx >= len(entries) {
		selectedIdx = len(entries) - 1
	}

	var lines []string

	header := dockStyle(palette.Accent, bgColor).Bold(true).Render(i18n.T("classic_chat.bash.background_tasks")) +
		dockStyle(palette.TextMuted, bgColor).Render(fmt.Sprintf("  %d/%d", selectedIdx+1, len(entries)))
	lines = append(lines, clampWidth(header, width, bgColor))

	// Window the list around the selection (cap bashDockMaxRows rows).
	start := 0
	if len(entries) > bashDockMaxRows {
		start = selectedIdx - bashDockMaxRows/2
		if start < 0 {
			start = 0
		}
		if start > len(entries)-bashDockMaxRows {
			start = len(entries) - bashDockMaxRows
		}
	}
	end := start + bashDockMaxRows
	if end > len(entries) {
		end = len(entries)
	}

	for i := start; i < end; i++ {
		lines = append(lines, clampWidth(renderDockRow(entries[i], i == selectedIdx, width, bgColor), width, bgColor))
	}

	// ── Live tail for the selected command.
	if tailFn != nil {
		sel := entries[selectedIdx]
		tail := tailFn(sel.TaskID, bashDockTailRows)
		if len(tail) > 0 {
			for _, t := range tail {
				t = shared.SanitizeANSI(t)
				row := "    " + dockStyle(palette.TextDim, bgColor).Render("│ ") + t
				lines = append(lines, clampWidth(row, width, bgColor))
			}
		} else if sel.Running {
			lines = append(lines, clampWidth("    "+dockStyle(palette.TextMuted, bgColor).Italic(true).Render(i18n.T("classic_chat.bash.waiting_output")), width, bgColor))
		}
	}

	// ── Footer hint.
	footer := dockStyle(palette.TextMuted, bgColor).Render(i18n.T("classic_chat.bash.footer"))
	lines = append(lines, clampWidth(footer, width, bgColor))

	return strings.Join(lines, "\n"), len(lines)
}

// renderDockRow renders one command row: "‹glyph› ‹status› ‹command›  ‹elapsed›".
func renderDockRow(e bashDockEntry, selected bool, width int, bgColor string) string {
	glyph, glyphColor := dockGlyph(e)
	statusText := string(e.State)

	elapsed := formatDockElapsed(e.Elapsed)
	if !e.Running && e.ExitCode != nil {
		statusText = i18n.T("classic_chat.bash.exit_code", *e.ExitCode)
	}

	cursor := "  "
	nameColor := palette.TextDim
	if selected {
		cursor = dockStyle(palette.Accent, bgColor).Bold(true).Render("▸ ")
		nameColor = palette.Text
	}

	// Reserve room for cursor(2) + glyph(2) + status + spacing + elapsed.
	right := dockStyle(palette.TextMuted, bgColor).Render(elapsed)
	rightW := ansi.StringWidth(elapsed)
	statusPart := dockStyle(glyphColor, bgColor).Render(statusText)
	left := cursor + dockStyle(glyphColor, bgColor).Render(glyph+" ") + statusPart + " "
	leftW := 2 + 2 + ansi.StringWidth(statusText) + 1

	cmd := flattenDockCommand(e.Command)
	maxCmd := width - leftW - rightW - 2
	if maxCmd < 3 {
		maxCmd = 3
	}
	if ansi.StringWidth(cmd) > maxCmd {
		cmd = string(ansi.Truncate(cmd, maxCmd-1, "")) + "…"
	}
	cmdPart := dockStyle(nameColor, bgColor).Render(cmd)

	// Pad between command and right-aligned elapsed.
	used := leftW + ansi.StringWidth(cmd) + rightW
	pad := width - used
	if pad < 1 {
		pad = 1
	}
	return left + cmdPart + strings.Repeat(" ", pad) + right
}

// dockGlyph returns a status glyph + color for an entry.
func dockGlyph(e bashDockEntry) (string, string) {
	switch {
	case e.Running:
		return rotatingBashGlyph(e.Elapsed), palette.Success
	case e.State == bgprocess.StateFailed:
		return "✗", palette.Error
	case e.State == bgprocess.StateCancelled:
		return "⊘", palette.Warning
	default:
		if e.ExitCode != nil && *e.ExitCode != 0 {
			return "✗", palette.Error
		}
		return "✓", palette.TextMuted
	}
}

func rotatingBashGlyph(elapsed time.Duration) string {
	frames := []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}
	frame := int(elapsed/(120*time.Millisecond)) % len(frames)
	if frame < 0 {
		frame = 0
	}
	return frames[frame]
}

// ── helpers ──────────────────────────────────────────────────────────────

func dockStyle(fg, bg string) lipgloss.Style {
	s := lipgloss.NewStyle().Foreground(lipgloss.Color(fg))
	if bg != "" {
		s = s.Background(lipgloss.Color(bg))
	}
	return s
}

// clampWidth truncates/pads a styled line to exactly width columns and reapplies
// the background so the dock row is visually solid.
func clampWidth(s string, width int, bgColor string) string {
	w := ansi.StringWidth(ansi.Strip(s))
	if w > width {
		s = ansi.Truncate(s, width, "")
		w = ansi.StringWidth(ansi.Strip(s))
	}
	if w < width {
		s += strings.Repeat(" ", width-w)
	}
	return shared.ReapplyBackground(s, bgColor)
}

func flattenDockCommand(cmd string) string {
	cmd = strings.ReplaceAll(cmd, "\n", " ")
	return strings.Join(strings.Fields(cmd), " ")
}

func formatDockElapsed(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	total := int(d.Seconds())
	h := total / 3600
	m := (total % 3600) / 60
	s := total % 60
	if h > 0 {
		return fmt.Sprintf("%d:%02d:%02d", h, m, s)
	}
	return fmt.Sprintf("%02d:%02d", m, s)
}

// bashDockEntryCount returns how many dock entries (bash + sub-agents) exist.
func (a *App) bashDockEntryCount() int {
	if a.sdk == nil {
		return 0
	}
	return len(a.collectDockEntries())
}

// dockSelectedIndex resolves the ID-anchored selection to an index in the given
// entry list. If the selected ID is gone (finished/aged out), it falls back to
// the first entry so the highlight never lands on a stale item. Returns -1 for
// an empty list.
func (a *App) dockSelectedIndex(entries []bashDockEntry) int {
	if len(entries) == 0 {
		return -1
	}
	for i, e := range entries {
		if e.ID() == a.bashDockSelectedID {
			return i
		}
	}
	// Selection lost (item finished/aged out) → anchor to the first entry.
	a.bashDockSelectedID = entries[0].ID()
	return 0
}

// dockMoveSelection moves the selection by delta within the current entry list,
// updating the anchored ID. Returns false if it would move above the top (the
// caller uses that to leave the dock).
func (a *App) dockMoveSelection(delta int) bool {
	entries := a.collectDockEntries()
	if len(entries) == 0 {
		return false
	}
	idx := a.dockSelectedIndex(entries)
	next := idx + delta
	if next < 0 {
		return false
	}
	if next >= len(entries) {
		next = len(entries) - 1
	}
	a.bashDockSelectedID = entries[next].ID()
	return true
}

// dockSelectedEntry returns the currently selected entry, if any.
func (a *App) dockSelectedEntry() (bashDockEntry, bool) {
	entries := a.collectDockEntries()
	idx := a.dockSelectedIndex(entries)
	if idx < 0 {
		return bashDockEntry{}, false
	}
	return entries[idx], true
}

// cancelSelectedBashDockCommand cancels the currently selected running command
// in the dock (no-op if the selection is out of range or already finished).
func (a *App) cancelSelectedBashDockCommand() {
	if a.sdk == nil || a.sdk.bgProcessManager == nil {
		return
	}
	sel, ok := a.dockSelectedEntry()
	if !ok || !sel.Running || sel.IsAgent {
		return
	}
	_ = a.sdk.bgProcessManager.CancelByID(
		context.Background(), sel.TaskID, bgprocess.OwnerInfo{Role: "admin"})
}

// bashDetailMaxLines caps how many output lines the detail takeover pulls.
const bashDetailMaxLines = 2000

// openBashDetail opens the full-screen takeover viewer for a background bash
// command, showing a live header + its full (tailed) output. Esc closes it.
func (a *App) openBashDetail(taskID string) {
	if a.sdk == nil || taskID == "" {
		return
	}

	refresh := func() string {
		view, ok := a.sdk.GetBackgroundProcessOutput(taskID, bashDetailMaxLines)
		if !ok || view == nil {
			return i18n.T("classic_chat.bash.no_longer_available")
		}
		header := []string{
			fmtDetailHeader(i18n.T("classic_chat.bash.command_label"), flattenDockCommand(view.Command)),
			fmtDetailHeader(i18n.T("classic_chat.bash.status_label"), view.State),
		}
		if view.PID > 0 {
			header = append(header, fmtDetailHeader(i18n.T("classic_chat.bash.pid_label"), fmt.Sprintf("%d", view.PID)))
		}
		header = append(header, fmtDetailHeader(i18n.T("classic_chat.bash.elapsed_label"), formatDockElapsed(view.Elapsed)))
		if !view.Running && view.ExitCode != nil {
			header = append(header, fmtDetailHeader(i18n.T("classic_chat.bash.exit_label"), fmt.Sprintf("%d", *view.ExitCode)))
		}
		out := make([]string, 0, len(view.Lines))
		for _, ln := range view.Lines {
			out = append(out, ln.Content)
		}
		if len(out) == 0 {
			out = append(out, i18n.T("classic_chat.bash.no_output"))
		}
		return detailBodyFromLines(header, out)
	}

	status := func() string {
		view, ok := a.sdk.GetBackgroundProcessOutput(taskID, 1)
		if !ok || view == nil {
			return i18n.T("classic_chat.bash.gone")
		}
		if view.Running {
			return i18n.T("classic_chat.bash.running_elapsed", formatDockElapsed(view.Elapsed))
		}
		if view.ExitCode != nil {
			return i18n.T("classic_chat.bash.state_exit", view.State, *view.ExitCode)
		}
		return view.State
	}

	title := "bash"
	if view, ok := a.sdk.GetBackgroundProcessOutput(taskID, 1); ok && view != nil {
		title = flattenDockCommand(view.Command)
	}

	v := NewDetailViewer(title, status, refresh)
	v.SetSize(a.width, a.height)
	a.detailViewer = v
}
