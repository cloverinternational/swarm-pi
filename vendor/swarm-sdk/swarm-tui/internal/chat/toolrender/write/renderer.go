package write

import (
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/chat/toolrender"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/chat/toolrender/shared"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/i18n"
)

// WriteRenderer renders Write/WriteLegacy tool results with constrained output.
// Shows file path, status, line count, and a preview (first 5 + last 5 lines).
type WriteRenderer struct{}

// New creates a new WriteRenderer.
func New() *WriteRenderer {
	return &WriteRenderer{}
}

// CanRender returns true if this renderer handles the given tool context.
// Matches: Write, WriteLegacy, write, file_write, and MCP write tools.
func (r *WriteRenderer) CanRender(ctx *toolrender.RenderContext) bool {
	name := ctx.ToolName

	// Direct name matches
	switch name {
	case "Write", "write", "WriteLegacy", "file_write", "FileWrite":
		return true
	}

	// MCP tool name patterns
	lower := strings.ToLower(name)
	if strings.Contains(lower, "write_file") ||
		strings.Contains(lower, "create_file") ||
		strings.Contains(lower, "save_file") {
		return true
	}

	return false
}

// Render returns styled output lines for write operations.
// Shows file path, status, line count, preview.
func (r *WriteRenderer) Render(ctx *toolrender.RenderContext, _ toolrender.CachedResult) []string {
	output := ctx.Output
	params := ctx.Params

	// Create base style for background
	baseStyle := lipgloss.NewStyle()
	if ctx.BgColor != "" {
		baseStyle = baseStyle.Background(lipgloss.Color(ctx.BgColor))
	}

	// Helper to wrap line with background
	wrap := func(s string) string {
		// WHY THE CLAMP HERE: nothing in this renderer measures the viewport at
		// all. formatPreviewLine truncates at a fixed 80 BYTES — unrelated to
		// the pane, and a byte slice that can cut a UTF-8 sequence in half — and
		// the status line concatenates a two-column 📄 emoji, an i18n label and
		// the whole file path with no bound. Every rendered line passes through
		// this helper, so it is the single point where the pane width and the
		// six-column gutter are both visible. FitLine also expands tabs (one
		// column measured, up to four drawn) in previewed file content.
		s = shared.FitLine(s, ctx.Width)
		if ctx.BgColor == "" {
			return s
		}
		return shared.ReapplyBackground(baseStyle.Render(s), ctx.BgColor)
	}

	if output == "" && params == nil {
		return []string{wrap("    " + shared.AnsiFgMuted + "⎿" + shared.AnsiReset + " " + shared.AnsiFgDim + i18n.T("toolrender.write.no_output") + shared.AnsiReset)}
	}

	// Extract file path and content from params
	var filePath string
	var content string
	var isNewFile bool

	if params != nil {
		if fp, ok := params["file_path"].(string); ok {
			filePath = fp
		}
		if c, ok := params["content"].(string); ok {
			content = c
		}
	}

	// Determine if new file from params (overwrite=false/absent means new file)
	if params != nil {
		if overwrite, ok := params["overwrite"].(bool); ok && !overwrite {
			isNewFile = true
		}
	}
	// Also detect from output message patterns
	if strings.Contains(output, "new file") || strings.Contains(output, "Successfully created") {
		isNewFile = true
	}

	// Build result
	var result []string

	// Status line
	statusIcon := shared.AnsiFgGreen + "✓" + shared.AnsiReset
	var statusMsg string
	if isNewFile {
		statusMsg = shared.AnsiFgGreen + i18n.T("toolrender.write.created") + shared.AnsiReset
	} else {
		statusMsg = shared.AnsiFgBlue + i18n.T("toolrender.write.updated") + shared.AnsiReset
	}

	if filePath != "" {
		fileDisplay := shared.AnsiFgBlue + "📄 " + filePath + shared.AnsiReset
		statusLine := "    " + shared.AnsiFgMuted + "⎿" + shared.AnsiReset + " " + statusIcon + " " + statusMsg + " " + fileDisplay
		result = append(result, wrap(statusLine))
	} else {
		// Fallback if no file path in params
		result = append(result, wrap("    "+shared.AnsiFgMuted+"⎿"+shared.AnsiReset+" "+strings.ReplaceAll(output, "\n", " ")))
		return result
	}

	// Show constrained preview if we have content
	if content != "" {
		lines := strings.Split(content, "\n")
		lineCount := len(lines)

		// Line count
		// Line count
		countLine := "      " + shared.AnsiFgDim + i18n.T("toolrender.write.lines") + shared.AnsiReset + shared.AnsiFgWhite + formatLineCount(lineCount) + shared.AnsiReset
		result = append(result, wrap(countLine))

		// Preview (first 5 + last 5 lines)
		const previewLines = 5
		if lineCount <= previewLines*2 {
			// Show all lines if small enough
			for i, line := range lines {
				lineNum := i + 1
				preview := formatPreviewLine(lineNum, line)
				result = append(result, wrap(preview))
			}
		} else {
			// Show first 5 lines
			for i := range previewLines {
				lineNum := i + 1
				preview := formatPreviewLine(lineNum, lines[i])
				result = append(result, wrap(preview))
			}

			// Ellipsis
			omitted := lineCount - (previewLines * 2)
			ellipsisLine := "      " + shared.AnsiFgDim + i18n.T("toolrender.write.more_lines", formatLineCount(omitted)) + shared.AnsiReset
			result = append(result, wrap(ellipsisLine))

			// Show last 5 lines
			for i := lineCount - previewLines; i < lineCount; i++ {
				lineNum := i + 1
				preview := formatPreviewLine(lineNum, lines[i])
				result = append(result, wrap(preview))
			}
		}

	}

	return result
}

// PreProcess returns nil - Write output is simple enough to render live.
func (r *WriteRenderer) PreProcess(_ *toolrender.RenderContext) toolrender.CachedResult {
	return nil
}

// formatPreviewLine formats a single preview line with line number.
func formatPreviewLine(lineNum int, content string) string {
	const maxLen = 80
	// Width-aware, not byte-aware: `content[:n]` sliced bytes, so a CJK or
	// emoji preview line emitted invalid UTF-8 into the frame and a wide rune
	// consumed up to four "columns" of the budget. The caller's wrap() applies
	// the real viewport bound; this stays only as a work bound.
	content = shared.ExpandTabsANSI(content)
	if shared.PrintableWidth(content) > maxLen {
		content = shared.TruncateANSI(content, maxLen, "...")
	}

	lineNumStr := shared.AnsiFgDim + formatLineNum(lineNum) + shared.AnsiReset
	return "      " + lineNumStr + "  " + content
}

// formatLineNum formats a line number with padding.
func formatLineNum(n int) string {
	if n < 10 {
		return "  " + string(rune('0'+n))
	} else if n < 100 {
		s := string(rune('0'+(n/10))) + string(rune('0'+(n%10)))
		return " " + s
	}
	// For larger numbers, just convert normally
	return strings.Repeat(" ", max(0, 3-len(formatInt(n)))) + formatInt(n)
}

// formatInt converts int to string.
func formatInt(n int) string {
	if n == 0 {
		return "0"
	}
	negative := n < 0
	if negative {
		n = -n
	}
	var digits []rune
	for n > 0 {
		digits = append([]rune{rune('0' + (n % 10))}, digits...)
		n /= 10
	}
	s := string(digits)
	if negative {
		s = "-" + s
	}
	return s
}

// formatLineCount formats a line count as a string.
func formatLineCount(n int) string {
	return formatInt(n)
}
