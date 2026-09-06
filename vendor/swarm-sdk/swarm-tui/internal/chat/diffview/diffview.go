// Package diffview provides a diff visualization component for the TUI
package diffview

// DiffView is the main component that orchestrates diff rendering
// It combines diff computation, layout decisions, styling, syntax highlighting,
// and final rendering into a cohesive component.
type DiffView struct {
	// Input data
	BeforePath string // Path to original file (empty for new files)
	AfterPath  string // Path to new/modified file
	Before     string // Original content
	After      string // New content

	// Sub-components
	computer    *DiffComputer
	layout      *DiffLayout
	styler      *DiffStyler
	highlighter *SyntaxHighlighter
	renderer    *DiffRenderer

	// State
	diff          *ComputedDiff
	width         int
	maxLines      int           // 0 = unlimited
	collapseLevel CollapseLevel // Integration with existing collapse system
}

// New creates a new DiffView for comparing two versions of content
func New(beforePath, afterPath, before, after string) *DiffView {
	// Determine file path for syntax highlighting (prefer afterPath)
	filePath := afterPath
	if filePath == "" {
		filePath = beforePath
	}

	dv := &DiffView{
		BeforePath:    beforePath,
		AfterPath:     afterPath,
		Before:        before,
		After:         after,
		computer:      NewDiffComputer(),
		layout:        NewDiffLayout(),
		styler:        NewDiffStyler(),
		highlighter:   NewSyntaxHighlighter(filePath),
		renderer:      NewDiffRenderer(80),
		width:         80,
		maxLines:      0,
		collapseLevel: CollapseLevelFull,
	}

	// Compute diff immediately
	dv.computeDiff()

	return dv
}

// NewForNewFile creates a DiffView for a newly created file (no before content)
func NewForNewFile(path, content string) *DiffView {
	return New("", path, "", content)
}

// NewForEdit creates a DiffView for an edit operation
func NewForEdit(path, oldContent, newContent string) *DiffView {
	return New(path, path, oldContent, newContent)
}

// computeDiff computes the diff between before and after content
func (dv *DiffView) computeDiff() {
	dv.diff = dv.computer.Compute(dv.BeforePath, dv.AfterPath, dv.Before, dv.After)

	// Update renderer's line number width based on diff
	if dv.diff != nil {
		numWidth := CalculateNumWidth(dv.diff)
		dv.renderer.SetNumWidth(numWidth)
		dv.styler.SetNumWidth(numWidth)
	}
}

// SetWidth sets the available width for rendering
func (dv *DiffView) SetWidth(w int) *DiffView {
	dv.width = w
	dv.renderer.Width = w
	return dv
}

// SetMaxLines sets the maximum number of lines to render (0 = unlimited)
func (dv *DiffView) SetMaxLines(n int) *DiffView {
	dv.maxLines = n
	return dv
}

// SetCollapseLevel sets the collapse level for rendering
func (dv *DiffView) SetCollapseLevel(level CollapseLevel) *DiffView {
	dv.collapseLevel = level
	return dv
}

// SetLayoutType sets the layout type (unified, split, or auto)
func (dv *DiffView) SetLayoutType(layoutType LayoutType) *DiffView {
	dv.layout.Type = layoutType
	return dv
}

// EnableSyntaxHighlighting enables or disables syntax highlighting
func (dv *DiffView) EnableSyntaxHighlighting(enabled bool) *DiffView {
	dv.highlighter.SetEnabled(enabled)
	return dv
}

// SetContextLines sets the number of context lines around changes
func (dv *DiffView) SetContextLines(n int) *DiffView {
	dv.computer.ContextLines = n
	dv.computeDiff() // Recompute with new context
	return dv
}

// Render renders the diff based on current settings
func (dv *DiffView) Render() []string {
	if dv.diff == nil || dv.diff.IsEmpty() {
		return []string{}
	}

	// Handle collapse levels
	switch dv.collapseLevel {
	case CollapseLevelCollapsed:
		// Just return summary (header will be added by caller)
		return []string{}

	case CollapseLevelCompact:
		// Show first few lines
		previewLines := 3
		if dv.maxLines > 0 && dv.maxLines < previewLines {
			previewLines = dv.maxLines
		}
		return dv.renderer.RenderCompact(dv.diff, dv.styler, dv.highlighter, previewLines)

	case CollapseLevelFull:
		// Determine layout based on width
		maxCodeWidth := dv.getMaxCodeWidth()
		useSplit := dv.layout.ShouldUseSplit(dv.width, maxCodeWidth)

		var lines []string
		if useSplit {
			lines = dv.renderer.RenderSplit(dv.diff, dv.styler, dv.highlighter)
		} else {
			lines = dv.renderer.RenderUnified(dv.diff, dv.styler, dv.highlighter)
		}

		// Apply maxLines if set
		if dv.maxLines > 0 && len(lines) > dv.maxLines {
			lines = lines[:dv.maxLines]
		}

		return lines
	}

	return []string{}
}

// RenderWithHeader renders the diff with a header line showing file and stats
func (dv *DiffView) RenderWithHeader() []string {
	header := dv.GetHeader()
	lines := dv.Render()

	if len(lines) == 0 && dv.collapseLevel == CollapseLevelCollapsed {
		// Collapsed: just the header
		return []string{header}
	}

	result := make([]string, 0, len(lines)+1)
	result = append(result, header)
	result = append(result, lines...)
	return result
}

// GetHeader returns the header line for the diff
func (dv *DiffView) GetHeader() string {
	if dv.diff == nil {
		return dv.AfterPath
	}
	return dv.renderer.RenderSummary(dv.diff)
}

// GetStats returns the number of additions and deletions
func (dv *DiffView) GetStats() (additions, deletions int) {
	if dv.diff == nil {
		return 0, 0
	}
	return dv.diff.Additions, dv.diff.Deletions
}

// GetTotalLines returns the total number of lines in the diff
func (dv *DiffView) GetTotalLines() int {
	if dv.diff == nil {
		return 0
	}
	return dv.diff.TotalLines
}

// IsEmpty returns true if there are no changes
func (dv *DiffView) IsEmpty() bool {
	return dv.diff == nil || dv.diff.IsEmpty()
}

// getMaxCodeWidth returns the maximum code line width in the diff
func (dv *DiffView) getMaxCodeWidth() int {
	if dv.diff == nil {
		return 40
	}

	maxWidth := 0
	for _, hunk := range dv.diff.Hunks {
		for _, line := range hunk.Lines {
			if len(line.Content) > maxWidth {
				maxWidth = len(line.Content)
			}
		}
	}

	if maxWidth < 40 {
		maxWidth = 40 // Minimum readable width
	}

	return maxWidth
}

// GetDiff returns the computed diff (for testing/inspection)
func (dv *DiffView) GetDiff() *ComputedDiff {
	return dv.diff
}
