package diffview

import (
	"strings"

	"charm.land/lipgloss/v2"
)

// DiffStyler applies styles to diff lines
type DiffStyler struct {
	Style      DiffStyle
	NumWidth   int // Width of line number column (including padding)
	CodeWidth  int // Width of code column
	ShowSymbol bool
}

// NewDiffStyler creates a new styler with default settings
func NewDiffStyler() *DiffStyler {
	return &DiffStyler{
		Style:      DefaultDarkStyle(),
		NumWidth:   5, // " 123 " format
		CodeWidth:  80,
		ShowSymbol: true,
	}
}

// SetNumWidth sets the width for line number columns
func (ds *DiffStyler) SetNumWidth(width int) *DiffStyler {
	ds.NumWidth = width
	return ds
}

// SetCodeWidth sets the width for code content
func (ds *DiffStyler) SetCodeWidth(width int) *DiffStyler {
	ds.CodeWidth = width
	return ds
}

// StyleLine styles a complete diff line
func (ds *DiffStyler) StyleLine(line DiffLine) StyledLine {
	return StyledLine{
		BeforeNum: ds.StyleLineNum(line.BeforeNum, line.Kind),
		AfterNum:  ds.StyleLineNum(line.AfterNum, line.Kind),
		Symbol:    ds.StyleSymbol(line.Kind),
		Code:      ds.StyleCode(line.Content, line.Kind),
	}
}

// StyleLineNum formats and styles a line number
func (ds *DiffStyler) StyleLineNum(num int, kind DiffLineKind) string {
	style := ds.Style.GetLineNumStyle(kind)

	var content string
	if num == 0 {
		// No line number - show blank with same width
		content = strings.Repeat(" ", ds.NumWidth)
	} else {
		// Format number right-aligned with padding
		numStr := itoa(num)
		padding := max(
			// -1 for trailing space
			ds.NumWidth-len(numStr)-1, 1)
		content = strings.Repeat(" ", padding) + numStr + " "
	}

	return style.Render(content)
}

// StyleSymbol styles the +/- symbol
func (ds *DiffStyler) StyleSymbol(kind DiffLineKind) string {
	if !ds.ShowSymbol {
		return ""
	}

	style := ds.Style.GetSymbolStyle(kind)
	symbol := kind.String()
	return style.Render(" " + symbol + " ")
}

// StyleCode styles the code content (without syntax highlighting)
// Returns code with diff background but no foreground color (for syntax highlighting)
func (ds *DiffStyler) StyleCode(content string, kind DiffLineKind) string {
	style := ds.Style.GetCodeStyle(kind)

	// Pad or truncate to width
	displayContent := padOrTruncate(content, ds.CodeWidth)

	// Apply background color, extending to full width
	return style.Width(ds.CodeWidth).Render(displayContent)
}

// StyleCodeWithHighlighting applies diff background to already-highlighted code
func (ds *DiffStyler) StyleCodeWithHighlighting(highlightedContent string, kind DiffLineKind) string {
	bgColor := ds.Style.GetBGColor(kind)
	if bgColor == "" {
		// No background for context lines
		return padToWidth(highlightedContent, ds.CodeWidth)
	}

	// Wrap the highlighted content in a background style
	bgStyle := lipgloss.NewStyle().Background(lipgloss.Color(bgColor))
	return bgStyle.Width(ds.CodeWidth).Render(highlightedContent)
}

// StyleHunkHeader styles a hunk header line (@@...@@)
func (ds *DiffStyler) StyleHunkHeader(beforeStart, beforeCount, afterStart, afterCount int) string {
	// Format: @@ -1,3 +1,4 @@
	header := "@@ -" + itoa(beforeStart)
	if beforeCount != 1 {
		header += "," + itoa(beforeCount)
	}
	header += " +" + itoa(afterStart)
	if afterCount != 1 {
		header += "," + itoa(afterCount)
	}
	header += " @@"

	return ds.Style.HunkHeader.Render(header)
}

// padOrTruncate pads or truncates a string to the specified width
func padOrTruncate(s string, width int) string {
	// Get visual width (accounting for potential ANSI codes)
	visualWidth := lipgloss.Width(s)

	if visualWidth >= width {
		// Need to truncate - this is tricky with ANSI codes
		// For now, just return as-is (lipgloss.Width will handle display)
		return s
	}

	// Pad with spaces
	return s + strings.Repeat(" ", max(0, width-visualWidth))
}

// padToWidth pads a string to the specified width
func padToWidth(s string, width int) string {
	visualWidth := lipgloss.Width(s)
	if visualWidth >= width {
		return s
	}
	return s + strings.Repeat(" ", max(0, width-visualWidth))
}

// maxLineNumWidth calculates the width needed for line numbers
// given the maximum line number that will be displayed
func maxLineNumWidth(maxNum int) int {
	digits := 1
	n := maxNum
	for n >= 10 {
		digits++
		n /= 10
	}
	// Add padding: " " + digits + " "
	return digits + 2
}

// CalculateNumWidth determines the appropriate line number width for a diff
func CalculateNumWidth(diff *ComputedDiff) int {
	maxBefore := 0
	maxAfter := 0

	for _, hunk := range diff.Hunks {
		for _, line := range hunk.Lines {
			if line.BeforeNum > maxBefore {
				maxBefore = line.BeforeNum
			}
			if line.AfterNum > maxAfter {
				maxAfter = line.AfterNum
			}
		}
	}

	// Use the larger of the two
	maxNum := max(maxAfter, maxBefore)

	return maxLineNumWidth(maxNum)
}
