// Package edit provides a renderer for Edit/Write tool output as unified diffs.
// It uses the diffview package to compute diffs between old and new content,
// then renders them with colored backgrounds (green for additions, red for deletions).
package edit

import (
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/chat/toolrender"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/chat/toolrender/shared"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/i18n"
)

// ANSI color constants - hardcoded true-color escapes matching the parent chat package.
const (
	ansiReset   = "\x1b[0m"
	ansiBgGreen = "\x1b[48;2;30;60;30m"    // Dark green background for additions
	ansiBgRed   = "\x1b[48;2;75;30;30m"    // Dark red background for deletions
	ansiFgGreen = "\x1b[38;2;166;227;161m" // Light green for + symbol
	ansiFgRed   = "\x1b[38;2;243;139;168m" // Light red for - symbol
	ansiFgDim   = "\x1b[38;2;140;140;140m" // Dim gray for line numbers
	ansiFgWhite = "\x1b[38;2;255;255;255m" // Bright white for code text
	ansiFgMuted = "\x1b[38;2;147;153;178m" // Muted for connectors
)

// DiffLineKind indicates the type of diff line.
type DiffLineKind int

const (
	// DiffLineContext is an unchanged context line.
	DiffLineContext DiffLineKind = iota
	// DiffLineInsert is an added line (shown with green background).
	DiffLineInsert
	// DiffLineDelete is a removed line (shown with red background).
	DiffLineDelete
)

// DiffLine represents a single line in the rendered diff output.
type DiffLine struct {
	Kind        DiffLineKind // Type: context, insert, or delete
	LineNum     int          // Line number (before for delete, after for insert/context)
	Content     string       // Raw content
	Styled      string       // Styled content with ANSI background/foreground
	VisualWidth int          // Pre-computed visual width
}

// EditResult stores pre-processed Edit/Write tool output as a diff.
// It implements toolrender.CachedResult so it can be cached on the message
// for efficient re-rendering on each frame.
type EditResult struct {
	FilePath     string     // Path to the file that was edited
	Lines        []DiffLine // Pre-styled diff lines
	TotalAdded   int        // Total lines added
	TotalRemoved int        // Total lines removed
	RenderWidth  int        // Width the diff was rendered for
	IsNewFile    bool       // True if this was a Write creating a new file
	Language     i18n.Language
}

// Ensure EditResult implements CachedResult at compile time.
var _ toolrender.CachedResult = (*EditResult)(nil)

// GetRenderedLines returns the pre-styled diff lines for rendering.
// This is the fast path - returns cached styled strings with no recomputation.
func (e *EditResult) GetRenderedLines() []string {
	if e == nil || len(e.Lines) == 0 {
		width := 0
		if e != nil {
			width = e.RenderWidth
		}
		return []string{shared.FitLine("    "+ansiFgMuted+"⎿"+ansiReset+" "+ansiFgDim+i18n.T("toolrender.edit.no_changes")+ansiReset, width)}
	}

	result := make([]string, len(e.Lines))
	for i, line := range e.Lines {
		result[i] = line.Styled
	}
	// The cached path duplicates renderDiffOutput's layout, so it inherits the
	// same defect: codeWidth's minimum is a floor, not a cap, and the gutter is
	// added on top of it. Clamp here too — RenderWidth is the width these lines
	// were computed for.
	return shared.FitLines(result, e.RenderWidth)
}

// NeedsRerender checks if the EditResult needs to be re-rendered at a new width.
func (e *EditResult) NeedsRerender(width int) bool {
	if e == nil {
		return true
	}
	return e.RenderWidth != width || e.Language != i18n.CurrentLanguage()
}
