package generic

import (
	"strings"

	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/chat/toolrender"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/chat/toolrender/shared"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/i18n"
)

// GenericRenderer is the fallback renderer for any tool that no other
// renderer handles. It renders errors in red, plain output with connector
// prefixes, and truncates long output with a "more lines" hint.
type GenericRenderer struct{}

// New creates a new GenericRenderer.
func New() *GenericRenderer {
	return &GenericRenderer{}
}

// CanRender always returns true. This is the fallback renderer.
func (r *GenericRenderer) CanRender(_ *toolrender.RenderContext) bool {
	return true
}

// Render returns styled output lines for any tool result.
//   - Errors are rendered in red with a connector prefix.
//   - Output is rendered as plain text with a connector prefix on the first line.
//   - Long output is truncated to 10 lines with a "... (N more lines)" hint.
func (r *GenericRenderer) Render(ctx *toolrender.RenderContext, _ toolrender.CachedResult) []string {
	width := ctx.Width
	if width <= 0 {
		width = 80
	}

	// Error rendering
	if ctx.Error != "" {
		return r.renderError(ctx.Error, width)
	}

	// Output rendering
	if ctx.Output != "" {
		return r.renderOutput(ctx.Output, width)
	}

	// No output and no error
	return shared.FitLines([]string{"    " + shared.AnsiFgMuted + "\u23bf" + shared.AnsiReset + " " + shared.AnsiFgDim + i18n.T("toolrender.generic.no_output") + shared.AnsiReset}, width)
}

// PreProcess returns nil. Generic rendering is always done live.
func (r *GenericRenderer) PreProcess(_ *toolrender.RenderContext) toolrender.CachedResult {
	return nil
}

// renderError renders an error message in red with text wrapping.
func (r *GenericRenderer) renderError(errMsg string, width int) []string {
	var result []string

	// Calculate available width for error text
	// Prefix is "    ⎿ Error: " which is ~14 characters
	contentWidth := max(width-14, 20)

	// Wrap the error text
	wrapped := shared.WrapText(errMsg, contentWidth)

	for i, line := range wrapped {
		if i == 0 {
			result = append(result, "    "+shared.AnsiFgMuted+"\u23bf"+shared.AnsiReset+" "+shared.AnsiFgRed+i18n.T("toolrender.generic.error_prefix")+line+shared.AnsiReset)
		} else {
			result = append(result, "      "+shared.AnsiFgRed+line+shared.AnsiReset)
		}
	}

	// max(..., 20) above is a FLOOR, not a cap: on a pane narrower than 34
	// columns it hands the wrapper more room than exists, and the six-column
	// gutter overflows on top of that. Clamp the finished lines — that is the
	// only place the true left edge and the gutter are both visible. Tabs are
	// also expanded here: they measure one column but draw up to four.
	return shared.FitLines(result, width)
}

// renderOutput renders plain output text, truncated to 10 lines.
func (r *GenericRenderer) renderOutput(output string, width int) []string {
	lines := strings.Split(output, "\n")

	// Filter out empty trailing lines
	for len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) == "" {
		lines = lines[:len(lines)-1]
	}

	// Calculate available width
	contentWidth := max(width-8, 20)

	const maxLines = 10
	totalLines := len(lines)
	displayLines := lines
	truncated := false

	if totalLines > maxLines {
		displayLines = lines[:maxLines]
		truncated = true
	}

	var result []string
	for i, line := range displayLines {
		// Truncate long lines. This MUST be width-aware, not byte-aware:
		// len() counts bytes, so `line[:n]` both mismeasures CJK/emoji (which
		// are two columns per rune but three or four bytes) and can slice a
		// UTF-8 sequence in half, emitting invalid UTF-8 into the frame.
		displayLine := shared.ExpandTabsANSI(line)
		if shared.PrintableWidth(displayLine) > contentWidth {
			displayLine = shared.TruncateANSI(displayLine, contentWidth, "...")
		}

		if i == 0 {
			result = append(result, "    "+shared.AnsiFgMuted+"\u23bf"+shared.AnsiReset+" "+displayLine)
		} else {
			result = append(result, "      "+displayLine)
		}
	}

	// Add truncation hint
	if truncated {
		remaining := totalLines - maxLines
		hint := i18n.T("toolrender.generic.more_lines", remaining)
		result = append(result, "      "+shared.AnsiFgDim+hint+shared.AnsiReset)
	}

	// See renderError: contentWidth's minimum is a floor, so the assembled
	// line (gutter + content) can still exceed a narrow pane. Clamp it.
	return shared.FitLines(result, width)
}
