package edit

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/x/ansi"

	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/chat/diffview"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/chat/toolrender"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/chat/toolrender/shared"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/i18n"
)

// EditRenderer renders Edit/Write tool output as a unified diff with colored
// backgrounds: green for additions and red for deletions. It uses the diffview
// package to compute the actual diff between old and new content.
type EditRenderer struct{}

// New creates a new EditRenderer instance.
func New() *EditRenderer {
	return &EditRenderer{}
}

// CanRender returns true if this renderer can handle the given tool context.
// Matches:
//   - Exact tool names: "Edit", "edit", "Write", "write", "EditFile", "edit_file"
//   - MCP names containing: "write_file", "edit_file", "update_file", "create_file"
//   - Params fallback: presence of both "old_string" and "new_string" keys
func (r *EditRenderer) CanRender(ctx *toolrender.RenderContext) bool {
	name := ctx.ToolName

	// Direct name matches
	switch name {
	case "Edit", "edit", "Write", "write", "EditFile", "edit_file", "EditLegacy":
		return true
	}

	// MCP tool name matching
	lower := strings.ToLower(name)
	if strings.HasPrefix(lower, "mcp_") || strings.HasPrefix(lower, "mcp__") {
		return strings.Contains(lower, "write_file") ||
			strings.Contains(lower, "write-file") ||
			strings.Contains(lower, "edit_file") ||
			strings.Contains(lower, "edit-file") ||
			strings.Contains(lower, "update_file") ||
			strings.Contains(lower, "create_file") ||
			strings.Contains(lower, "modify_file")
	}

	// Params fallback: check for edit-specific parameters
	if ctx.Params != nil {
		_, hasOld := ctx.Params["old_string"]
		_, hasNew := ctx.Params["new_string"]
		if hasOld && hasNew {
			return true
		}
	}

	return false
}

// Render produces styled diff output lines.
// If a cached EditResult is provided and valid, returns its pre-rendered lines.
// Otherwise computes the diff from parameters and renders it.
func (r *EditRenderer) Render(ctx *toolrender.RenderContext, cached toolrender.CachedResult) []string {
	// Try to use cached result
	if cached != nil {
		if er, ok := cached.(*EditResult); ok && !er.NeedsRerender(ctx.Width) {
			return er.GetRenderedLines()
		}
	}

	// Extract parameters — try legacy params first, then metadata (hashline edit)
	filePath := getFilePath(ctx.Params)
	oldContent, newContent := extractOldNew(ctx)

	// If no old/new strings, we can't compute a diff; fall back to raw output
	if oldContent == "" && newContent == "" {
		if ctx.Output != "" {
			// Raw tool output is unmeasured attacker-adjacent text: flatten its
			// newlines (one returned string must be one line), expand its tabs,
			// and clamp it to the pane like every other line here.
			return []string{shared.FitLine("    "+ansiFgMuted+"⎿"+ansiReset+" "+ansiFgDim+strings.ReplaceAll(ctx.Output, "\n", " ")+ansiReset, ctx.Width)}
		}
		return []string{shared.FitLine("    "+ansiFgMuted+"⎿"+ansiReset+" "+ansiFgDim+i18n.T("toolrender.edit.no_changes")+ansiReset, ctx.Width)}
	}

	return renderDiffOutput(filePath, oldContent, newContent, ctx.Width)
}

// PreProcess computes the diff and returns an EditResult that can be cached.
func (r *EditRenderer) PreProcess(ctx *toolrender.RenderContext) toolrender.CachedResult {
	filePath := getFilePath(ctx.Params)
	oldContent, newContent := extractOldNew(ctx)
	width := ctx.Width

	result := &EditResult{
		FilePath:    filePath,
		RenderWidth: width,
		IsNewFile:   oldContent == "",
		Language:    i18n.CurrentLanguage(),
	}

	// Compute the diff
	dv := diffview.NewForEdit(filePath, oldContent, newContent)
	diff := dv.GetDiff()

	if diff == nil || diff.IsEmpty() {
		return result
	}

	// Calculate code width
	codeWidth := max(width-14, 20)

	// Calculate max line number width
	maxLineNum := 0
	for _, hunk := range diff.Hunks {
		for _, line := range hunk.Lines {
			if line.BeforeNum > maxLineNum {
				maxLineNum = line.BeforeNum
			}
			if line.AfterNum > maxLineNum {
				maxLineNum = line.AfterNum
			}
		}
	}
	numWidth := max(len(fmt.Sprintf("%d", maxLineNum)), 3)

	result.Lines = make([]DiffLine, 0)

	firstLine := true
	for _, hunk := range diff.Hunks {
		for _, line := range hunk.Lines {
			dl := processDiffLine(line, numWidth, codeWidth, firstLine)
			result.Lines = append(result.Lines, dl)

			if dl.Kind == DiffLineInsert {
				result.TotalAdded++
			} else if dl.Kind == DiffLineDelete {
				result.TotalRemoved++
			}

			firstLine = false
		}
	}

	return result
}

// renderDiffOutput renders a diff between oldContent and newContent.
// This is the non-cached rendering path.
func renderDiffOutput(filePath, oldContent, newContent string, width int) []string {
	dv := diffview.NewForEdit(filePath, oldContent, newContent)
	diff := dv.GetDiff()

	if diff == nil || diff.IsEmpty() {
		return []string{shared.FitLine("    "+ansiFgMuted+"⎿"+ansiReset+" "+ansiFgDim+i18n.T("toolrender.edit.no_changes")+ansiReset, width)}
	}

	var result []string
	codeWidth := max(width-14, 20)

	// Calculate max line number width
	maxLineNum := 0
	for _, hunk := range diff.Hunks {
		for _, line := range hunk.Lines {
			if line.BeforeNum > maxLineNum {
				maxLineNum = line.BeforeNum
			}
			if line.AfterNum > maxLineNum {
				maxLineNum = line.AfterNum
			}
		}
	}
	numWidth := max(len(fmt.Sprintf("%d", maxLineNum)), 3)

	firstLine := true
	for _, hunk := range diff.Hunks {
		for _, line := range hunk.Lines {
			var rendered string

			// Format line number
			var lineNumStr string
			if line.Kind == diffview.DiffLineDelete {
				lineNumStr = fmt.Sprintf("%*d", numWidth, line.BeforeNum)
			} else if line.Kind == diffview.DiffLineInsert {
				lineNumStr = fmt.Sprintf("%*d", numWidth, line.AfterNum)
			} else {
				lineNumStr = fmt.Sprintf("%*d", numWidth, line.AfterNum)
			}

			// Truncate code if needed
			code := line.Content
			// Expand tabs BEFORE measuring: source code is full of them, and a
			// tab measures one column to ansi.StringWidth but draws up to four
			// on the terminal, shearing the diff's colored background and every
			// column to its right.
			code = shared.ExpandTabsANSI(code)
			if ansi.StringWidth(code) > codeWidth {
				code = ansi.Truncate(code, codeWidth-3, "") + "..."
			}
			// Pad code to full width for consistent background
			code = padToWidth(code, codeWidth)

			switch line.Kind {
			case diffview.DiffLineInsert:
				rendered = ansiBgGreen + ansiFgDim + lineNumStr + " " + ansiFgGreen + "+" + " " + ansiFgWhite + code + ansiReset
			case diffview.DiffLineDelete:
				rendered = ansiBgRed + ansiFgDim + lineNumStr + " " + ansiFgRed + "-" + " " + ansiFgWhite + code + ansiReset
			default:
				rendered = ansiFgDim + lineNumStr + "   " + ansiFgWhite + code + ansiReset
			}

			// Add connector prefix
			if firstLine {
				result = append(result, "    "+ansiFgMuted+"⎿"+ansiReset+" "+rendered)
				firstLine = false
			} else {
				result = append(result, "      "+rendered)
			}
		}
	}

	// codeWidth is max(width-14, 20): the 20 is a FLOOR, not a cap, so on a
	// pane narrower than 34 columns the code is PADDED wider than the viewport
	// and the 6-column gutter plus the line-number column sit on top of that.
	// Clamp the assembled lines — the only place the true left edge is visible.
	return shared.FitLines(result, width)
}

// processDiffLine processes a single diff line from the diffview package
// into a styled DiffLine for caching.
func processDiffLine(line diffview.DiffLine, numWidth, codeWidth int, isFirst bool) DiffLine {
	var kind DiffLineKind
	var lineNumStr string

	switch line.Kind {
	case diffview.DiffLineDelete:
		kind = DiffLineDelete
		lineNumStr = fmt.Sprintf("%*d", numWidth, line.BeforeNum)
	case diffview.DiffLineInsert:
		kind = DiffLineInsert
		lineNumStr = fmt.Sprintf("%*d", numWidth, line.AfterNum)
	default:
		kind = DiffLineContext
		lineNumStr = fmt.Sprintf("%*d", numWidth, line.AfterNum)
	}

	// Process the code content with ANSI-aware truncation
	code := line.Content
	// See renderDiffOutput: tabs must be expanded before they are measured.
	code = shared.ExpandTabsANSI(code)
	if ansi.StringWidth(code) > codeWidth {
		code = ansi.Truncate(code, codeWidth-3, "") + "..."
	}

	// Pad code to full width for consistent background
	code = padToWidth(code, codeWidth)

	// Build styled line with ANSI codes
	var styled string
	switch kind {
	case DiffLineInsert:
		styled = ansiBgGreen + ansiFgDim + lineNumStr + " " + ansiFgGreen + "+" + " " + ansiFgWhite + code + ansiReset
	case DiffLineDelete:
		styled = ansiBgRed + ansiFgDim + lineNumStr + " " + ansiFgRed + "-" + " " + ansiFgWhite + code + ansiReset
	default:
		styled = ansiFgDim + lineNumStr + "   " + ansiFgWhite + code + ansiReset
	}

	// Add connector prefix
	if isFirst {
		styled = "    " + ansiFgMuted + "⎿" + ansiReset + " " + styled
	} else {
		styled = "      " + styled
	}

	return DiffLine{
		Kind:        kind,
		LineNum:     line.AfterNum,
		Content:     line.Content,
		Styled:      styled,
		VisualWidth: int(ansi.StringWidth(styled)),
	}
}

// extractOldNew extracts old/new content for diff computation.
// Checks legacy params (old_string/new_string) first, then metadata
// (old_content/new_content from hashline edit tool).
func extractOldNew(ctx *toolrender.RenderContext) (oldContent, newContent string) {
	// Legacy path: old_string/new_string params (EditLegacy)
	oldContent = getStringParam(ctx.Params, "old_string")
	newContent = getStringParam(ctx.Params, "new_string")
	if oldContent != "" || newContent != "" {
		return oldContent, newContent
	}

	// Hashline edit path: old_content/new_content in metadata
	if ctx.Metadata != nil {
		if old, ok := ctx.Metadata["old_content"].(string); ok {
			oldContent = old
		}
		if nw, ok := ctx.Metadata["new_content"].(string); ok {
			newContent = nw
		}
	}
	return oldContent, newContent
}

// getFilePath extracts a file path from tool parameters.
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

// getStringParam extracts a string parameter from the params map.
func getStringParam(params map[string]any, key string) string {
	if params == nil {
		return ""
	}
	if v, ok := params[key].(string); ok {
		return v
	}
	return ""
}

// padToWidth pads a string with spaces to reach the target visual width.
// ANSI codes in the string are properly accounted for.
func padToWidth(s string, targetWidth int) string {
	currentWidth := ansi.StringWidth(s)
	if currentWidth >= targetWidth {
		return s
	}
	padding := strings.Repeat(" ", targetWidth-currentWidth)
	return s + padding
}
