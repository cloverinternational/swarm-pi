// Package readbg renders output from the ReadBackgroundCommand tool.
//
// Without this renderer the tool's structured JSON (status, tailed output,
// heartbeat metadata) falls through to the generic renderer and is shown to
// the user/agent as a raw JSON blob. This renderer parses that JSON and shows
// a clean status line, tailed output, and a heartbeat hint so the reader can
// tell at a glance whether a long-running background process is healthy.
package readbg

import (
	"encoding/json"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/chat/toolrender"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/chat/toolrender/shared"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/i18n"
	"github.com/charmbracelet/x/ansi"
)

const (
	statusColor     = "#4EC9B0" // teal for status/task_id
	labelColor      = "#FFFFFF" // bold white label
	commandColor    = "#CCCCCC" // command text
	outputTextColor = "#CCCCCC" // output lines
	hintColor       = "#888888" // dim heartbeat/hints
	runningColor    = "#4EC9B0" // green: running/completed ok
	errorColor      = "#F44747" // red: failed/non-zero exit
)

// maxOutputLines caps how many tailed output lines are shown inline.
const maxOutputLines = 12

// Renderer renders ReadBackgroundCommand tool output.
type Renderer struct{}

// New creates a new Renderer.
func New() *Renderer { return &Renderer{} }

// CanRender returns true for the ReadBackgroundCommand tool.
func (r *Renderer) CanRender(ctx *toolrender.RenderContext) bool {
	name := ctx.ToolName
	if name == "ReadBackgroundCommand" {
		return true
	}
	// MCP-wrapped variants (mcp__..._ReadBackgroundCommand).
	lower := strings.ToLower(name)
	return strings.Contains(lower, "readbackgroundcommand")
}

// PreProcess is stateless.
func (r *Renderer) PreProcess(ctx *toolrender.RenderContext) toolrender.CachedResult { return nil }

// bgOutput mirrors the relevant fields of bgprocess.BackgroundBashOutput.
type bgOutput struct {
	TaskID          string  `json:"task_id"`
	Status          string  `json:"status"`
	Command         string  `json:"command"`
	StartedAt       string  `json:"started_at"`
	DurationSeconds float64 `json:"duration_seconds"`
	ExitCode        *int    `json:"exit_code"`
	Output          []struct {
		Stream  string `json:"stream"`
		Content string `json:"content"`
	} `json:"output"`
	Metadata struct {
		TotalLines             int     `json:"total_lines"`
		OutputTruncated        bool    `json:"output_truncated"`
		OutputFile             string  `json:"output_file"`
		SecondsSinceLastOutput float64 `json:"seconds_since_last_output"`
		BytesWritten           int64   `json:"bytes_written"`
	} `json:"metadata"`
}

// bgList mirrors the "list" action response.
type bgList struct {
	Count     int `json:"count"`
	Processes []struct {
		TaskID   string `json:"task_id"`
		Command  string `json:"command"`
		Status   string `json:"status"`
		Duration string `json:"duration"`
	} `json:"processes"`
}

// Render produces styled lines for a ReadBackgroundCommand result.
func (r *Renderer) Render(ctx *toolrender.RenderContext, cached toolrender.CachedResult) []string {
	// WHY THE FINAL CLAMP: the command truncation below uses `width - 24` /
	// `width - 30` and only applies when that is greater than 3, so on a narrow
	// pane it does not run at all; the status labels, task_id line and
	// heartbeat hint are unmeasured fixed strings; and the error/raw fallbacks
	// echo their input verbatim. Clamping the assembled line is the only place
	// the true pane width and the indent are both known.
	//
	// Tabs are expanded by the same call: the .output[].content field is
	// verbatim process stdout, the most tab-dense input this renderer sees, and
	// a tab measures one column but draws up to four.
	return shared.FitLines(r.render(ctx), ctx.Width)
}

// render produces the unclamped lines; Render applies the viewport bound.
func (r *Renderer) render(ctx *toolrender.RenderContext) []string {
	bg := ctx.BgColor
	wrap := func(s string) string { return shared.ReapplyBackground(s, bg) }

	trimmed := strings.TrimSpace(ctx.Output)

	// Error result (e.g. task_id not found): show it plainly.
	if ctx.Error != "" && trimmed == "" {
		errSty := style(errorColor, bg)
		return []string{wrap("  " + errSty.Render(flatten(ctx.Error)))}
	}

	// Non-JSON (e.g. "Process X has been cancelled"): show as a status line.
	if !strings.HasPrefix(trimmed, "{") {
		if trimmed == "" {
			return nil
		}
		return []string{wrap("  " + style(statusColor, bg).Render(flatten(trimmed)))}
	}

	// Try the list shape first (has a "processes" array).
	if strings.Contains(trimmed, `"processes"`) {
		var list bgList
		if err := json.Unmarshal([]byte(trimmed), &list); err == nil {
			return r.renderList(list, ctx.Width, bg)
		}
	}

	var out bgOutput
	if err := json.Unmarshal([]byte(trimmed), &out); err != nil {
		// Unparseable — fall back to showing the raw (sanitized) body.
		return []string{wrap("  " + style(outputTextColor, bg).Render(flatten(shared.SanitizeANSI(trimmed))))}
	}
	return r.renderStatus(out, ctx.Width, bg)
}

// renderStatus renders a single background process's status + tailed output.
func (r *Renderer) renderStatus(out bgOutput, width int, bg string) []string {
	wrap := func(s string) string { return shared.ReapplyBackground(s, bg) }
	var lines []string

	// ── Status line: "bg <status> <command>"
	statusClr := runningColor
	switch out.Status {
	case "failed", "cancelled":
		statusClr = errorColor
	}
	labelSty := style(statusClr, bg).Bold(true)
	cmdSty := style(commandColor, bg)

	cmd := flatten(out.Command)
	// Width-aware, not byte-aware: `cmd[:maxW-3]` sliced BYTES against a COLUMN
	// budget, so a CJK or emoji command was cut in the middle of a UTF-8
	// sequence and the invalid bytes reached the frame.
	if maxW := width - 24; maxW > 3 {
		cmd = shared.TruncateANSI(shared.ExpandTabsANSI(cmd), maxW, "...")
	}
	header := "  " + labelSty.Render("bg:"+out.Status)
	if cmd != "" {
		header += cmdSty.Render(" " + cmd)
	}
	lines = append(lines, wrap(header))

	// ── task_id + exit code
	metaSty := style(statusColor, bg)
	idLine := "    " + metaSty.Render("task_id: "+out.TaskID)
	if out.ExitCode != nil {
		ecClr := runningColor
		if *out.ExitCode != 0 {
			ecClr = errorColor
		}
		idLine += style(hintColor, bg).Render("  ") + style(ecClr, bg).Render(i18n.T("toolrender.read_background.exit_code", *out.ExitCode))
	}
	lines = append(lines, wrap(idLine))

	// ── Heartbeat hint (only meaningful while running)
	if out.Status == "running" {
		hb := i18n.T("toolrender.read_background.heartbeat",
			out.Metadata.SecondsSinceLastOutput, out.Metadata.BytesWritten)
		lines = append(lines, wrap("    "+style(hintColor, bg).Italic(true).Render(hb)))
	}

	// ── Tailed output
	if len(out.Output) > 0 {
		outSty := style(outputTextColor, bg)
		shown := out.Output
		hidden := 0
		if len(shown) > maxOutputLines {
			hidden = len(shown) - maxOutputLines
			shown = shown[len(shown)-maxOutputLines:] // tail: newest lines
		}
		if hidden > 0 {
			lines = append(lines, wrap("    "+style(hintColor, bg).Italic(true).Render(
				i18n.T("toolrender.read_background.earlier_lines", hidden))))
		}
		maxContentW := max(width-4, 1)
		for _, ol := range shown {
			// Expand tabs BEFORE measuring: this is raw process stdout, and a
			// tab measures one column but draws up to four.
			content := shared.ExpandTabsANSI(strings.ReplaceAll(ol.Content, "\n", " "))
			if ansi.StringWidth(content) > maxContentW {
				content = ansi.Truncate(content, maxContentW-1, "") + "…"
			}
			lines = append(lines, wrap("    "+outSty.Render(content)))
		}
	}

	// ── Truncation / full-output-file hint
	if out.Metadata.OutputFile != "" {
		hint := i18n.T("toolrender.read_background.output_truncated", out.Metadata.OutputFile)
		if ansi.StringWidth(hint) > width-4 {
			hint = string(ansi.Truncate(hint, width-5, "")) + "…"
		}
		lines = append(lines, wrap("    "+style(hintColor, bg).Italic(true).Render(hint)))
	}

	return lines
}

// renderList renders the "list" action: a count + one line per process.
func (r *Renderer) renderList(list bgList, width int, bg string) []string {
	wrap := func(s string) string { return shared.ReapplyBackground(s, bg) }
	var lines []string

	lines = append(lines, wrap("  "+style(labelColor, bg).Bold(true).Render(
		i18n.T("toolrender.read_background.process_count", list.Count))))

	for _, p := range list.Processes {
		statusClr := runningColor
		switch p.Status {
		case "failed", "cancelled":
			statusClr = errorColor
		}
		cmd := flatten(p.Command)
		// See renderStatus: this must be width-aware, not byte-aware.
		if maxW := width - 30; maxW > 3 {
			cmd = shared.TruncateANSI(shared.ExpandTabsANSI(cmd), maxW, "...")
		}
		line := "    " + style(statusClr, bg).Render(p.Status) +
			style(hintColor, bg).Render(" "+p.Duration+" ") +
			style(commandColor, bg).Render(cmd)
		lines = append(lines, wrap(line))
	}
	return lines
}

// ── helpers ──────────────────────────────────────────────────────────────

func style(fg, bg string) lipgloss.Style {
	s := lipgloss.NewStyle().Foreground(lipgloss.Color(fg))
	if bg != "" {
		s = s.Background(lipgloss.Color(bg))
	}
	return s
}

func flatten(cmd string) string {
	cmd = strings.ReplaceAll(cmd, "\n", " ")
	return strings.Join(strings.Fields(cmd), " ")
}
