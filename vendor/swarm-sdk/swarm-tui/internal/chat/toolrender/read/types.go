// Package read provides a renderer for Read/file-view tool output with syntax
// highlighting. It parses cat-n formatted output, detects language from file path,
// applies syntax highlighting, and renders with line numbers and connector symbols.
package read

import (
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/chat/toolrender"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/chat/toolrender/shared"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/i18n"
)

// ANSI color constants - hardcoded true-color escapes matching the parent chat package.
const (
	ansiReset     = "\x1b[0m"
	ansiFgDim     = "\x1b[38;2;140;140;140m" // Dim gray for secondary text
	ansiFgMuted   = "\x1b[38;2;147;153;178m" // Muted for connectors
	ansiFgLineNum = "\x1b[38;2;137;180;250m" // Swarm blue for line numbers
)

// ReadResult stores pre-processed Read tool output with syntax-highlighted lines.
// It implements toolrender.CachedResult so it can be cached on the message for
// efficient re-rendering on each frame.
type ReadResult struct {
	FilePath       string            // Path to the file that was read
	Language       string            // Detected language for syntax highlighting
	Lines          []HighlightedLine // Pre-highlighted lines with ANSI codes
	TotalLines     int               // Total line count
	RenderWidth    int               // Width the content was rendered for
	IsEmpty        bool              // True if file was empty
	RawContent     string            // Original raw content (for re-rendering at different widths)
	RenderLanguage i18n.Language
}

// HighlightedLine represents a single line of syntax-highlighted code.
// The Styled field contains ANSI escape codes for coloring, and VisualWidth
// is pre-computed for efficient rendering without re-parsing.
type HighlightedLine struct {
	LineNum     int    // Original line number in file
	Content     string // Original content without highlighting
	Styled      string // Styled content with ANSI codes
	VisualWidth int    // Pre-computed visual width (excluding ANSI codes)
	IsComment   bool   // True if line is a comment (for styling consistency)
}

// Ensure ReadResult implements CachedResult at compile time.
var _ toolrender.CachedResult = (*ReadResult)(nil)

// GetRenderedLines returns the pre-styled lines for rendering.
// This is the fast path - just returns cached styled strings with no recomputation.
func (r *ReadResult) GetRenderedLines() []string {
	if r == nil || r.IsEmpty {
		// The placeholder is a fixed i18n string wider than a narrow pane, so
		// it needs the same clamp the content lines already get. RenderWidth is
		// the width this result was computed for; a nil receiver has no width
		// and FitLine treats 0 as "unconstrained".
		width := 0
		if r != nil {
			width = r.RenderWidth
		}
		return []string{shared.FitLine("    "+ansiFgMuted+"⎿"+ansiReset+" "+ansiFgDim+i18n.T("toolrender.read.empty_file")+ansiReset, width)}
	}

	result := make([]string, len(r.Lines))
	for i, line := range r.Lines {
		result[i] = line.Styled
	}
	return result
}

// NeedsRerender checks if the ReadResult needs to be re-rendered at a new width.
// Returns true if the cached render width doesn't match the requested width.
func (r *ReadResult) NeedsRerender(width int) bool {
	if r == nil {
		return true
	}
	return r.RenderWidth != width || r.RenderLanguage != i18n.CurrentLanguage()
}
