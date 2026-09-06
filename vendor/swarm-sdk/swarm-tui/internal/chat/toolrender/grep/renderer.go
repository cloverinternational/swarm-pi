package grep

import (
	"regexp"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/chat/toolrender"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/chat/toolrender/shared"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/i18n"
	"github.com/charmbracelet/x/ansi"
)

// hashlinePattern matches "lineNum:hash|content" format
var hashlinePattern = regexp.MustCompile(`^(\d+):([0-9a-f]{4})\|(.*)$`)

// GrepRenderer renders grep/search/glob tool results with styled file headers,
// match count summaries, and highlighted line numbers.
type GrepRenderer struct{}

// New creates a new GrepRenderer.
func New() *GrepRenderer {
	return &GrepRenderer{}
}

// CanRender returns true if this renderer handles the given tool context.
// Matches: Grep, grep, Search, search, Glob, glob, RipGrep, and MCP tools
// containing "grep", "search_file", "find_in", "code_search".
// Content fallback: output starts with "Found " and contains " matches".
func (r *GrepRenderer) CanRender(ctx *toolrender.RenderContext) bool {
	name := ctx.ToolName

	// Direct name matches
	switch name {
	case "Grep", "grep", "Search", "search", "Glob", "glob", "RipGrep", "swarm_Grep", "GrepFiles":
		return true
	}

	// MCP tool name patterns
	lower := strings.ToLower(name)
	if strings.Contains(lower, "grep") ||
		strings.Contains(lower, "search_file") ||
		strings.Contains(lower, "find_in") ||
		strings.Contains(lower, "code_search") {
		return true
	}

	// Content-based fallback: output starts with "Found " and contains " matches"
	if ctx.Output != "" &&
		strings.HasPrefix(ctx.Output, "Found ") &&
		strings.Contains(ctx.Output, " matches") {
		return true
	}

	return false
}

// Render returns styled output lines for grep/search results.
// It parses the output for summary lines, file headers, and match lines,
// applying ANSI styling to each.
func (r *GrepRenderer) Render(ctx *toolrender.RenderContext, _ toolrender.CachedResult) []string {
	output := ctx.Output
	width := ctx.Width

	// Create base style for background
	baseStyle := lipgloss.NewStyle()
	if ctx.BgColor != "" {
		baseStyle = baseStyle.Background(lipgloss.Color(ctx.BgColor))
	}

	// Helper to wrap line with background
	wrap := func(s string) string {
		// Every branch below builds a line as gutter + content, where the
		// content width is max(width-8, 30) — a FLOOR, not a cap. On any pane
		// narrower than 38 columns the branches hand themselves more room than
		// exists, and several branches (summary, file header, plain-text
		// fallback) do not truncate at all. Clamping here, at the single point
		// every rendered line passes through, is the only place the gutter and
		// the true left edge are both accounted for. FitLine also expands tabs,
		// which measure one column but draw up to four.
		s = shared.FitLine(s, width)
		if ctx.BgColor == "" {
			return s
		}
		return shared.ReapplyBackground(baseStyle.Render(s), ctx.BgColor)
	}

	if output == "" {
		return []string{wrap("    " + shared.AnsiFgMuted + "⎿" + shared.AnsiReset + " " + shared.AnsiFgDim + i18n.T("toolrender.grep.no_output") + shared.AnsiReset)}
	}

	lines := strings.Split(output, "\n")
	var result []string

	// Styles
	fileHeaderStyle := shared.AnsiFgBlue + shared.AnsiBold
	lineNumStyle := shared.AnsiFgLineNum // Swarm blue for line numbers
	matchCountStyle := shared.AnsiFgGreen

	// State tracking
	var currentFile string
	matchesInFile := 0
	inFileSection := false

	// Calculate content width accounting for indentation and line numbers.
	// Gutter = 8: 4-char indent + 2-char line-ref + 2-char separator.
	codeWidth := max(width-8, 30)

	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}

		// Summary line: "Found N matches for pattern..."
		if strings.HasPrefix(trimmed, "Found ") && strings.Contains(trimmed, " matches") {
			summaryLine := trimmed
			// Highlight the match count in green
			summaryLine = strings.Replace(summaryLine, "Found ", matchCountStyle+"Found ", 1)
			if idx := strings.Index(summaryLine, " matches"); idx != -1 {
				summaryLine = summaryLine[:idx] + shared.AnsiReset + summaryLine[idx:]
			}
			result = append(result, wrap("    "+summaryLine))

			continue
		}

		// "No matches found" line
		if strings.HasPrefix(trimmed, "No matches found") {
			result = append(result, wrap("    "+shared.AnsiFgDim+trimmed+shared.AnsiReset))

			continue
		}

		// Separator line: "---"
		if trimmed == "---" {
			if inFileSection && currentFile != "" {
				inFileSection = false
				currentFile = ""
				matchesInFile = 0
			}
			continue
		}

		// File header: "File: /path/to/file"
		if after, ok := strings.CutPrefix(trimmed, "File: "); ok {
			currentFile = after
			inFileSection = true
			matchesInFile = 0

			fileDisplay := fileHeaderStyle + currentFile + shared.AnsiReset

			// Blank line before file header (except first)
			if i > 1 {
				result = append(result, wrap(""))
			}
			result = append(result, wrap("      "+fileDisplay))

			continue
		}

		// Hashline match: "42:a3|content" — strip hash, show only line number
		if matches := hashlinePattern.FindStringSubmatch(trimmed); matches != nil {
			lineNum := matches[1]
			_ = matches[2] // hash — not displayed
			content := matches[3]

			if ansi.StringWidth(content) > codeWidth {
				content = string(ansi.Truncate(content, codeWidth-1, "")) + "…"
			}

			lineRef := lineNumStyle + "L" + lineNum + shared.AnsiReset

			rendered := "      " + lineRef + "  " + content

			result = append(result, wrap(rendered))

			matchesInFile++
			continue
		}

		// Match line: "L123: content"
		if strings.HasPrefix(trimmed, "L") && strings.Contains(trimmed, ": ") {
			parts := strings.SplitN(trimmed, ": ", 2)
			if len(parts) == 2 {
				lineNum := strings.TrimPrefix(parts[0], "L")
				content := parts[1]

				// Truncate if too long
				if ansi.StringWidth(content) > codeWidth {
					content = string(ansi.Truncate(content, codeWidth-1, "")) + "…"
				}

				rendered := "      " + lineNumStyle + "L" + lineNum + shared.AnsiReset + "  " + content

				result = append(result, wrap(rendered))

				matchesInFile++
				continue
			}
		}

		// Note/Warning lines
		if strings.HasPrefix(trimmed, "Note:") || strings.HasPrefix(trimmed, "Warning:") {
			result = append(result, wrap(""))
			result = append(result, wrap("      "+shared.AnsiFgDim+trimmed+shared.AnsiReset))
			continue
		}

		// Fallback: plain text
		if len(result) == 0 {
			result = append(result, wrap("    "+shared.AnsiFgMuted+"⎿"+shared.AnsiReset+" "+trimmed))
		} else {
			result = append(result, wrap("      "+trimmed))
		}

	}

	// If no results generated, render raw output
	if len(result) == 0 {
		for _, line := range lines {
			if strings.TrimSpace(line) == "" {
				continue
			}
			result = append(result, wrap("      "+line))

		}
	}

	return result
}

// PreProcess returns nil. Grep output is fast enough to render live.
func (r *GrepRenderer) PreProcess(_ *toolrender.RenderContext) toolrender.CachedResult {
	return nil
}
