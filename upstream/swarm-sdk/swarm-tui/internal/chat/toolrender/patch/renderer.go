package patch

import (
	"strings"

	"github.com/charmbracelet/x/ansi"

	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/chat/toolrender"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/chat/toolrender/shared"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/i18n"
)

// PatchRenderer renders V4A patch format output with colored diff lines.
// V4A patches use markers like "*** Begin Patch", "*** Update File:",
// "*** Add File:", and "*** Delete File:" to delimit file sections,
// with +/- prefixed lines for additions and deletions.
type PatchRenderer struct{}

// New creates a new PatchRenderer instance.
func New() *PatchRenderer {
	return &PatchRenderer{}
}

// CanRender returns true if this renderer can handle the given tool context.
// Matches:
//   - Exact tool names: "Patch", "patch", "ApplyPatch", "apply_patch"
//   - MCP names containing: "apply_patch", "patch_file"
//   - Content fallback: output contains "*** Begin Patch" or "*** Update File:"
func (r *PatchRenderer) CanRender(ctx *toolrender.RenderContext) bool {
	name := ctx.ToolName

	// Direct name matches
	switch name {
	case "Patch", "patch", "ApplyPatch", "apply_patch":
		return true
	}

	// MCP tool name matching
	lower := strings.ToLower(name)
	if strings.HasPrefix(lower, "mcp_") || strings.HasPrefix(lower, "mcp__") {
		return strings.Contains(lower, "apply_patch") ||
			strings.Contains(lower, "patch_file")
	}

	// Content fallback: check if output contains V4A patch markers
	if ctx.Output != "" {
		return strings.Contains(ctx.Output, "*** Begin Patch") ||
			strings.Contains(ctx.Output, "*** Update File:")
	}

	return false
}

// Render produces styled patch output lines.
// If a cached PatchResult is provided and valid, returns its pre-rendered lines.
// Otherwise parses the V4A format and renders it.
func (r *PatchRenderer) Render(ctx *toolrender.RenderContext, cached toolrender.CachedResult) []string {
	// Try to use cached result
	if cached != nil {
		if pr, ok := cached.(*PatchResult); ok && !pr.NeedsRerender(ctx.Width) {
			return pr.GetRenderedLines()
		}
	}

	// Render from scratch
	return renderPatchOutput(ctx.Output, ctx.Width)
}

// PreProcess parses the V4A patch format and returns a PatchResult that can be cached.
func (r *PatchRenderer) PreProcess(ctx *toolrender.RenderContext) toolrender.CachedResult {
	patchInput := ctx.Output
	width := ctx.Width

	if patchInput == "" {
		return &PatchResult{
			RenderWidth: width,
			RawInput:    patchInput,
			Language:    i18n.CurrentLanguage(),
		}
	}

	result := &PatchResult{
		RenderWidth: width,
		RawInput:    patchInput,
		Sections:    make([]PatchSection, 0),
		Language:    i18n.CurrentLanguage(),
	}

	codeWidth := max(width-10, 20)

	lines := strings.Split(patchInput, "\n")
	var currentSection *PatchSection

	firstLine := true
	for _, line := range lines {
		// Skip patch begin/end markers
		if strings.HasPrefix(line, "*** Begin Patch") || strings.HasPrefix(line, "*** End Patch") {
			continue
		}

		pl := processPatchLine(line, codeWidth, firstLine)
		if pl.IsHeader {
			// Extract file path from header
			filePath := strings.TrimPrefix(line, "*** Update File: ")
			filePath = strings.TrimPrefix(filePath, "*** Add File: ")
			filePath = strings.TrimPrefix(filePath, "*** Delete File: ")

			// Save current section and start new one
			if currentSection != nil {
				result.Sections = append(result.Sections, *currentSection)
			}
			currentSection = &PatchSection{
				FilePath: filePath,
				Lines:    make([]PatchLine, 0),
			}
		}

		if currentSection != nil {
			currentSection.Lines = append(currentSection.Lines, pl)
		} else {
			// Lines before any file header
			if len(result.Sections) == 0 {
				result.Sections = append(result.Sections, PatchSection{
					FilePath: "",
					Lines:    make([]PatchLine, 0),
				})
			}
			result.Sections[0].Lines = append(result.Sections[0].Lines, pl)
		}

		firstLine = false
	}

	// Add final section
	if currentSection != nil {
		result.Sections = append(result.Sections, *currentSection)
	}

	return result
}

// renderPatchOutput renders a V4A patch format string.
// This is the non-cached rendering path.
func renderPatchOutput(patchInput string, width int) []string {
	if patchInput == "" {
		return []string{shared.FitLine("    "+ansiFgMuted+"⎿"+ansiReset+" "+ansiFgDim+i18n.T("toolrender.patch.empty")+ansiReset, width)}
	}

	lines := strings.Split(patchInput, "\n")
	var result []string

	codeWidth := max(width-10, 20)

	firstLine := true

	for _, line := range lines {
		var rendered string
		// Expand tabs BEFORE any branch measures or truncates. Patch bodies are
		// source code, so they are full of tabs, and a tab measures one column
		// to ansi.StringWidth but draws up to four on the terminal — shearing
		// the +/- gutter and colored background of every line to its right.
		line = shared.ExpandTabsANSI(line)

		// Detect file headers
		if strings.HasPrefix(line, "*** Begin Patch") {
			continue // Skip begin marker
		}
		if strings.HasPrefix(line, "*** End Patch") {
			continue // Skip end marker
		}
		if strings.HasPrefix(line, "*** Update File:") || strings.HasPrefix(line, "*** Add File:") || strings.HasPrefix(line, "*** Delete File:") {
			// File header - show in blue/bold
			rendered = ansiFgType + ansiBold + line + ansiReset
		} else if after, ok := strings.CutPrefix(line, "+"); ok {
			// Addition line - green background
			code := after
			if ansi.StringWidth(code) > codeWidth {
				code = ansi.Truncate(code, codeWidth-3, "") + "..."
			}
			code = padToWidth(code, codeWidth)
			rendered = ansiBgGreen + ansiFgGreen + "+" + " " + ansiFgWhite + code + ansiReset
		} else if after, ok := strings.CutPrefix(line, "-"); ok {
			// Deletion line - red background
			code := after
			if ansi.StringWidth(code) > codeWidth {
				code = ansi.Truncate(code, codeWidth-3, "") + "..."
			}
			code = padToWidth(code, codeWidth)
			rendered = ansiBgRed + ansiFgRed + "-" + " " + ansiFgWhite + code + ansiReset
		} else if strings.HasPrefix(line, " ") || line == "" {
			// Context line
			code := line
			if ansi.StringWidth(code) > codeWidth {
				code = ansi.Truncate(code, codeWidth-3, "") + "..."
			}
			rendered = ansiFgDim + "  " + code + ansiReset
		} else {
			// Other lines
			rendered = ansiFgDim + line + ansiReset
		}

		// Add connector prefix
		if firstLine {
			result = append(result, "    "+ansiFgMuted+"⎿"+ansiReset+" "+rendered)
			firstLine = false
		} else {
			result = append(result, "      "+rendered)
		}
	}

	// codeWidth is max(width-10, 20): the 20 is a FLOOR, not a cap, so on a
	// pane narrower than 30 columns the code is PADDED wider than the viewport,
	// and the 6-column gutter sits on top of that. The header and "other line"
	// branches above do not truncate at all. Clamp the assembled lines.
	return shared.FitLines(result, width)
}

// processPatchLine processes a single patch line into a styled PatchLine.
func processPatchLine(line string, codeWidth int, isFirst bool) PatchLine {
	var kind DiffLineKind
	var styled string
	isHeader := false

	// See renderPatchOutput: tabs must be expanded before they are measured.
	line = shared.ExpandTabsANSI(line)

	// Detect file headers
	if strings.HasPrefix(line, "*** Update File:") ||
		strings.HasPrefix(line, "*** Add File:") ||
		strings.HasPrefix(line, "*** Delete File:") {
		isHeader = true
		styled = ansiFgType + ansiBold + line + ansiReset
		kind = DiffLineContext
	} else if strings.HasPrefix(line, "+") {
		kind = DiffLineInsert
		code := strings.TrimPrefix(line, "+")
		if ansi.StringWidth(code) > codeWidth {
			code = ansi.Truncate(code, codeWidth-3, "") + "..."
		}
		code = padToWidth(code, codeWidth)
		styled = ansiBgGreen + ansiFgGreen + "+" + " " + ansiFgWhite + code + ansiReset
	} else if strings.HasPrefix(line, "-") {
		kind = DiffLineDelete
		code := strings.TrimPrefix(line, "-")
		if ansi.StringWidth(code) > codeWidth {
			code = ansi.Truncate(code, codeWidth-3, "") + "..."
		}
		code = padToWidth(code, codeWidth)
		styled = ansiBgRed + ansiFgRed + "-" + " " + ansiFgWhite + code + ansiReset
	} else if strings.HasPrefix(line, " ") || line == "" {
		kind = DiffLineContext
		code := line
		if ansi.StringWidth(code) > codeWidth {
			code = ansi.Truncate(code, codeWidth-3, "") + "..."
		}
		styled = ansiFgDim + "  " + code + ansiReset
	} else {
		kind = DiffLineContext
		styled = ansiFgDim + line + ansiReset
	}

	// Add connector prefix
	if isFirst && !isHeader {
		styled = "    " + ansiFgMuted + "⎿" + ansiReset + " " + styled
	} else if !isHeader {
		styled = "      " + styled
	} else {
		// Headers get connector prefix too
		if isFirst {
			styled = "    " + ansiFgMuted + "⎿" + ansiReset + " " + styled
		} else {
			styled = "      " + styled
		}
	}

	return PatchLine{
		Kind:        kind,
		Content:     line,
		Styled:      styled,
		VisualWidth: int(ansi.StringWidth(styled)),
		IsHeader:    isHeader,
	}
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
