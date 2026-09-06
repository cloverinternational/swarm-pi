package diffview

import (
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/i18n"
)

// DiffRenderer assembles styled components into final output lines
type DiffRenderer struct {
	Width       int  // Total available width
	LineNumbers bool // Whether to show line numbers
	NumWidth    int  // Width of each line number column
	ShowHunks   bool // Whether to show hunk headers (@@ ... @@)
}

// NewDiffRenderer creates a renderer with default settings
func NewDiffRenderer(width int) *DiffRenderer {
	return &DiffRenderer{
		Width:       width,
		LineNumbers: true,
		NumWidth:    5,
		ShowHunks:   true,
	}
}

// SetLineNumbers enables or disables line number display
func (dr *DiffRenderer) SetLineNumbers(show bool) *DiffRenderer {
	dr.LineNumbers = show
	return dr
}

// SetNumWidth sets the line number column width
func (dr *DiffRenderer) SetNumWidth(width int) *DiffRenderer {
	dr.NumWidth = width
	return dr
}

// RenderUnified renders the diff in unified format (single column with +/-)
func (dr *DiffRenderer) RenderUnified(diff *ComputedDiff, styler *DiffStyler, highlighter *SyntaxHighlighter) []string {
	if diff == nil || len(diff.Hunks) == 0 {
		return []string{}
	}

	var lines []string

	// Calculate code width: total - (2 * numWidth) - symbol width
	symbolWidth := 3 // " + "
	codeWidth := max(dr.Width-(dr.NumWidth*2)-symbolWidth, 20)

	// Update styler with calculated widths
	styler.SetNumWidth(dr.NumWidth).SetCodeWidth(codeWidth)

	for _, hunk := range diff.Hunks {
		// Add hunk header
		if dr.ShowHunks {
			header := styler.StyleHunkHeader(
				hunk.BeforeStart, hunk.BeforeCount,
				hunk.AfterStart, hunk.AfterCount,
			)
			lines = append(lines, header)
		}

		// Render each line in the hunk
		for _, line := range hunk.Lines {
			rendered := dr.renderUnifiedLine(line, styler, highlighter)
			lines = append(lines, rendered)
		}
	}

	return lines
}

// renderUnifiedLine renders a single line in unified format
// Format: [beforeNum] [afterNum] [symbol] [code]
func (dr *DiffRenderer) renderUnifiedLine(line DiffLine, styler *DiffStyler, highlighter *SyntaxHighlighter) string {
	var parts []string

	// Line numbers
	if dr.LineNumbers {
		beforeNum := styler.StyleLineNum(line.BeforeNum, line.Kind)
		afterNum := styler.StyleLineNum(line.AfterNum, line.Kind)
		parts = append(parts, beforeNum, afterNum)
	}

	// Symbol (+/-)
	symbol := styler.StyleSymbol(line.Kind)
	parts = append(parts, symbol)

	// Code content with syntax highlighting and diff background
	var code string
	if highlighter != nil && highlighter.enabled {
		highlighted := highlighter.highlightLine(line.Content)
		code = styler.StyleCodeWithHighlighting(highlighted, line.Kind)
	} else {
		code = styler.StyleCode(line.Content, line.Kind)
	}
	parts = append(parts, code)

	// Join without extra spaces (each part includes its own padding)
	return strings.Join(parts, "")
}

// RenderSplit renders the diff in split format (side-by-side)
func (dr *DiffRenderer) RenderSplit(diff *ComputedDiff, styler *DiffStyler, highlighter *SyntaxHighlighter) []string {
	if diff == nil || len(diff.Hunks) == 0 {
		return []string{}
	}

	var lines []string

	// Calculate column widths for split view
	// Layout: [beforeNum] [code] | [afterNum] [code]
	separatorWidth := 3 // " │ "
	numWidthTotal := dr.NumWidth * 2
	availableForCode := dr.Width - numWidthTotal - separatorWidth
	codeWidth := max(availableForCode/2, 15)

	// Update styler
	styler.SetNumWidth(dr.NumWidth).SetCodeWidth(codeWidth)

	separator := lipgloss.NewStyle().
		Foreground(lipgloss.Color("#3a3a3a")).
		Render(" │ ")

	for _, hunk := range diff.Hunks {
		// Add hunk header (centered across both columns)
		if dr.ShowHunks {
			header := styler.StyleHunkHeader(
				hunk.BeforeStart, hunk.BeforeCount,
				hunk.AfterStart, hunk.AfterCount,
			)
			// Pad to full width
			lines = append(lines, padToWidth(header, dr.Width))
		}

		// Convert hunk to split format (pair up deletes and inserts)
		splitLines := dr.hunkToSplitLines(hunk)

		for _, sl := range splitLines {
			rendered := dr.renderSplitLine(sl, styler, highlighter, codeWidth, separator)
			lines = append(lines, rendered)
		}
	}

	return lines
}

// splitLine represents a line in split view (may have before, after, or both)
type splitLine struct {
	before *DiffLine // Left side (original)
	after  *DiffLine // Right side (new)
}

// hunkToSplitLines converts a hunk to split view format
func (dr *DiffRenderer) hunkToSplitLines(hunk DiffHunk) []splitLine {
	var result []splitLine

	// Collect consecutive deletes and inserts for pairing
	var pendingDeletes []*DiffLine
	var pendingInserts []*DiffLine

	flushPending := func() {
		// Pair up deletes and inserts
		maxPairs := max(len(pendingInserts), len(pendingDeletes))

		for i := range maxPairs {
			sl := splitLine{}
			if i < len(pendingDeletes) {
				sl.before = pendingDeletes[i]
			}
			if i < len(pendingInserts) {
				sl.after = pendingInserts[i]
			}
			result = append(result, sl)
		}

		pendingDeletes = nil
		pendingInserts = nil
	}

	for i := range hunk.Lines {
		line := &hunk.Lines[i]

		switch line.Kind {
		case DiffLineEqual:
			// Flush any pending changes first
			flushPending()
			// Equal lines appear on both sides
			result = append(result, splitLine{before: line, after: line})

		case DiffLineDelete:
			pendingDeletes = append(pendingDeletes, line)

		case DiffLineInsert:
			pendingInserts = append(pendingInserts, line)
		}
	}

	// Flush remaining
	flushPending()

	return result
}

// renderSplitLine renders a single line in split format
func (dr *DiffRenderer) renderSplitLine(sl splitLine, styler *DiffStyler, highlighter *SyntaxHighlighter, codeWidth int, separator string) string {
	// Left side (before)
	leftNum := strings.Repeat(" ", dr.NumWidth)
	leftCode := strings.Repeat(" ", codeWidth)
	if sl.before != nil {
		leftNum = styler.StyleLineNum(sl.before.BeforeNum, sl.before.Kind)
		bgColor := styler.Style.GetBGColor(sl.before.Kind)
		if highlighter != nil && highlighter.enabled {
			highlighted := highlighter.highlightLine(sl.before.Content)
			leftCode = applyBackgroundAndPad(highlighted, bgColor, codeWidth)
		} else {
			leftCode = applyBackgroundAndPad(sl.before.Content, bgColor, codeWidth)
		}
	}

	// Right side (after)
	rightNum := strings.Repeat(" ", dr.NumWidth)
	rightCode := strings.Repeat(" ", codeWidth)
	if sl.after != nil {
		rightNum = styler.StyleLineNum(sl.after.AfterNum, sl.after.Kind)
		bgColor := styler.Style.GetBGColor(sl.after.Kind)
		if highlighter != nil && highlighter.enabled {
			highlighted := highlighter.highlightLine(sl.after.Content)
			rightCode = applyBackgroundAndPad(highlighted, bgColor, codeWidth)
		} else {
			rightCode = applyBackgroundAndPad(sl.after.Content, bgColor, codeWidth)
		}
	}

	return leftNum + leftCode + separator + rightNum + rightCode
}

// applyBackgroundAndPad applies background color and pads to width
func applyBackgroundAndPad(content, bgColor string, width int) string {
	// Pad content first
	padded := padToWidth(content, width)

	if bgColor == "" {
		return padded
	}

	return lipgloss.NewStyle().
		Background(lipgloss.Color(bgColor)).
		Width(width).
		Render(content)
}

// RenderCompact renders a preview of the diff (first N lines)
func (dr *DiffRenderer) RenderCompact(diff *ComputedDiff, styler *DiffStyler, highlighter *SyntaxHighlighter, maxLines int) []string {
	if maxLines <= 0 {
		maxLines = 3
	}

	// Get full unified render
	fullLines := dr.RenderUnified(diff, styler, highlighter)

	if len(fullLines) <= maxLines {
		return fullLines
	}

	// Truncate and add indicator
	result := fullLines[:maxLines]
	remaining := len(fullLines) - maxLines

	moreIndicator := lipgloss.NewStyle().
		Foreground(lipgloss.Color("#666666")).
		Italic(true).
		Render(i18n.T("misc.diff.more_lines", remaining))

	result = append(result, moreIndicator)
	return result
}

// RenderSummary renders a one-line summary of the diff
// Format: "file.go (+3, -2)"
func (dr *DiffRenderer) RenderSummary(diff *ComputedDiff) string {
	if diff == nil {
		return ""
	}

	path := diff.AfterPath
	if path == "" {
		path = diff.BeforePath
	}

	return path + " (" + diff.Summary() + ")"
}
