package read

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/charmbracelet/x/ansi"

	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/chat/toolrender"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/chat/toolrender/shared"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/i18n"
)

// catNPattern matches cat -n format: "     1→content" or "     1\tcontent"
var catNPattern = regexp.MustCompile(`^(\s*\d+)([→\t])(.*)$`)

// hashlinePattern matches hashline format: "lineNum:hash|content" (e.g. "42:a3|  return x;")
var hashlinePattern = regexp.MustCompile(`^(\d+):([0-9a-f]{4})\|(.*)$`)

// ReadRenderer renders Read/file-view tool output with syntax highlighting,
// line numbers, and connector symbols. It parses cat-n formatted output (the
// standard format returned by Read tools), detects language from the file path,
// and applies token-level syntax highlighting.
type ReadRenderer struct{}

// New creates a new ReadRenderer instance.
func New() *ReadRenderer {
	return &ReadRenderer{}
}

// CanRender returns true if this renderer can handle the given tool context.
// Matches:
//   - Exact tool names: "Read", "read", "ReadFile", "read_file"
//   - MCP names containing: "read_file", "get_file", "file_content", "view_file"
//   - Content fallback: output lines match cat-n format (^\s*\d+[→\t])
func (r *ReadRenderer) CanRender(ctx *toolrender.RenderContext) bool {
	name := ctx.ToolName

	// Direct name matches
	switch name {
	case "Read", "read", "ReadFile", "read_file", "ReadLegacy":
		return true
	}

	// MCP tool name matching
	lower := strings.ToLower(name)
	if strings.HasPrefix(lower, "mcp_") || strings.HasPrefix(lower, "mcp__") {
		return strings.Contains(lower, "read_file") ||
			strings.Contains(lower, "read-file") ||
			strings.Contains(lower, "get_file") ||
			strings.Contains(lower, "file_content") ||
			strings.Contains(lower, "get_content") ||
			strings.Contains(lower, "view_file")
	}

	// Content fallback: check if output lines match cat-n or hashline format
	if ctx.Output != "" {
		lines := strings.SplitN(ctx.Output, "\n", 5)
		matchCount := 0
		checkCount := min(len(lines), 4)
		for i := range checkCount {
			if catNPattern.MatchString(lines[i]) || hashlinePattern.MatchString(lines[i]) {
				matchCount++
			}
		}
		// If at least half the checked lines match, it's likely cat-n or hashline format
		if matchCount > 0 && matchCount >= checkCount/2 {
			return true
		}
	}

	return false
}

// Render produces styled output lines for file content display.
// If a cached ReadResult is provided and valid, returns its pre-rendered lines.
// Otherwise renders from scratch using the context's output and parameters.
func (r *ReadRenderer) Render(ctx *toolrender.RenderContext, cached toolrender.CachedResult) []string {
	// Try to use cached result
	if cached != nil {
		if rr, ok := cached.(*ReadResult); ok && !rr.NeedsRerender(ctx.Width) {
			return rr.GetRenderedLines()
		}
	}

	// Extract file path from params
	filePath := getFilePath(ctx.Params)

	// Render from scratch
	return renderReadOutput(filePath, ctx.Output, ctx.Width, ctx.BgColor)
}

// PreProcess computes a ReadResult with syntax-highlighted lines that can be
// cached on the message for efficient re-rendering. Parses cat-n lines, detects
// language from filePath, and applies token-level syntax highlighting.
func (r *ReadRenderer) PreProcess(ctx *toolrender.RenderContext) toolrender.CachedResult {
	filePath := getFilePath(ctx.Params)
	content := ctx.Output
	width := ctx.Width

	if content == "" {
		return &ReadResult{
			FilePath:       filePath,
			IsEmpty:        true,
			RenderWidth:    width,
			RenderLanguage: i18n.CurrentLanguage(),
		}
	}

	// Detect language for syntax highlighting
	lang := detectLanguageFromPath(filePath)

	lines := strings.Split(content, "\n")
	result := &ReadResult{
		FilePath:       filePath,
		Language:       lang,
		TotalLines:     len(lines),
		RenderWidth:    width,
		RawContent:     content,
		Lines:          make([]HighlightedLine, 0, len(lines)),
		RenderLanguage: i18n.CurrentLanguage(),
	}

	// Calculate code width (leaving room for line numbers and connectors)
	// Prefix is: "    ⎿ " (6) + lineNum (4-5) + " " (1) = 11-12 chars
	// Use 14 to be safe for large line numbers
	codeWidth := max(width-14, 20)

	for i, line := range lines {
		hl := processReadLine(line, i, lang, codeWidth, width, ctx.BgColor)
		result.Lines = append(result.Lines, hl)
	}

	return result
}

// processReadLine processes a single line of read output, extracting line numbers
// from cat-n format and applying syntax highlighting.
func processReadLine(line string, index int, lang string, codeWidth int, fullWidth int, bgColor string) HighlightedLine {
	var lineNumStr string
	var codeContent string

	// Check if line has cat -n format line numbers
	if matches := catNPattern.FindStringSubmatch(line); matches != nil {
		lineNumStr = strings.TrimSpace(matches[1])
		codeContent = matches[3]
	} else if matches := hashlinePattern.FindStringSubmatch(line); matches != nil {
		// Hashline format: "lineNum:hash|content" — strip hash, show only line number
		lineNumStr = matches[1]
		codeContent = matches[3]
	} else {
		lineNumStr = fmt.Sprintf("%d", index+1)
		codeContent = line
	}

	// Pad line number for alignment
	if len(lineNumStr) < 4 {
		lineNumStr = fmt.Sprintf("%4s", lineNumStr)
	}

	// Check if this is a comment line
	isComment := isCommentLine(strings.TrimSpace(codeContent), lang)

	// Apply syntax highlighting
	highlightedCode := highlightLineANSI(codeContent, lang, codeWidth, bgColor)

	// Build the complete styled line
	var styled string
	if index == 0 {
		styled = "    " + ansiFgMuted + "⎿" + ansiReset + " " + ansiFgLineNum + lineNumStr + ansiReset + " " + highlightedCode
		styled = shared.ReapplyBackground(styled, bgColor)
	} else {
		styled = "      " + ansiFgLineNum + lineNumStr + ansiReset + " " + highlightedCode
		styled = shared.ReapplyBackground(styled, bgColor)
	}

	// Pad to full width with background
	styled = shared.PadToWidth(styled, fullWidth)
	styled = shared.ReapplyBackground(styled, bgColor)

	// Final clamp. codeWidth above is max(width-14, 20): the 20 is a FLOOR, so
	// on a pane narrower than 34 columns the highlighter is handed more room
	// than exists, and the 6-column gutter plus the 4-column line number sit on
	// top of that. The assembled line is the only place the true left edge is
	// visible, so it is the only place the bound can be enforced.
	styled = shared.FitLine(styled, fullWidth)

	return HighlightedLine{
		LineNum:     index + 1,
		Content:     codeContent,
		Styled:      styled,
		VisualWidth: int(ansi.StringWidth(styled)),
		IsComment:   isComment,
	}
}

// renderReadOutput renders file content with syntax highlighting for Read tool.
// Uses hardcoded ANSI colors. This is the non-cached rendering path.
func renderReadOutput(filePath, content string, width int, bgColor string) []string {
	if content == "" {
		// Even the fixed placeholder is wider than a narrow pane, so it needs
		// the same clamp as real content.
		return []string{shared.FitLine("    "+ansiFgMuted+"⎿"+ansiReset+" "+ansiFgDim+i18n.T("toolrender.read.empty_file")+ansiReset, width)}
	}

	lines := strings.Split(content, "\n")
	var result []string

	// Calculate widths
	codeWidth := max(width-14, 20)

	// Detect language from file path
	lang := detectLanguageFromPath(filePath)

	for i, line := range lines {
		var lineNumStr, codeContent string

		// Check if line has cat -n format or hashline format line numbers
		if matches := catNPattern.FindStringSubmatch(line); matches != nil {
			lineNumStr = strings.TrimSpace(matches[1])
			codeContent = matches[3]
		} else if matches := hashlinePattern.FindStringSubmatch(line); matches != nil {
			// Hashline format: "lineNum:hash|content" — strip hash, show only line number
			lineNumStr = matches[1]
			codeContent = matches[3]
		} else {
			lineNumStr = fmt.Sprintf("%d", i+1)
			codeContent = line
		}

		// Pad line number for alignment
		if len(lineNumStr) < 4 {
			lineNumStr = fmt.Sprintf("%4s", lineNumStr)
		}

		// Apply syntax highlighting to the code content
		highlightedCode := highlightLineANSI(codeContent, lang, codeWidth, bgColor)

		// Build the line: [connector] [linenum] [code]
		var rendered string
		if i == 0 {
			rendered = "    " + ansiFgMuted + "⎿" + ansiReset + " " + ansiFgLineNum + lineNumStr + ansiReset + " " + highlightedCode
			rendered = shared.ReapplyBackground(rendered, bgColor)
		} else {
			rendered = "      " + ansiFgLineNum + lineNumStr + ansiReset + " " + highlightedCode
			rendered = shared.ReapplyBackground(rendered, bgColor)
		}

		// Pad to full width with background
		rendered = shared.PadToWidth(rendered, width)
		rendered = shared.ReapplyBackground(rendered, bgColor)

		// See processReadLine: codeWidth's minimum is a floor, not a cap, so
		// the assembled line (gutter + line number + code) must still be
		// clamped to the real pane width.
		rendered = shared.FitLine(rendered, width)

		result = append(result, rendered)
	}

	return result
}

// getFilePath extracts a file path from tool parameters, checking both
// "file_path" and "path" keys.
func getFilePath(params map[string]any) string {
	if params == nil {
		return ""
	}
	if fp, ok := params["file_path"].(string); ok {
		return fp
	}
	if fp, ok := params["path"].(string); ok {
		return fp
	}
	return ""
}
