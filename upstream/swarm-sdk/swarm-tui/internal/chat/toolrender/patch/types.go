// Package patch provides a renderer for V4A patch format output.
// It parses the V4A patch format (with markers like "*** Begin Patch",
// "*** Update File:", "*** Add File:", "*** Delete File:") and renders
// diffs with colored backgrounds for additions and deletions.
package patch

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
	ansiFgDim   = "\x1b[38;2;140;140;140m" // Dim gray for context lines
	ansiFgWhite = "\x1b[38;2;255;255;255m" // Bright white for code text
	ansiFgMuted = "\x1b[38;2;147;153;178m" // Muted for connectors
	ansiFgType  = "\x1b[38;2;137;180;250m" // Blue for file headers
	ansiBold    = "\x1b[1m"                // Bold text
)

// DiffLineKind indicates the type of diff line.
// This is defined locally to avoid importing the edit package.
type DiffLineKind int

const (
	// DiffLineContext is an unchanged context line.
	DiffLineContext DiffLineKind = iota
	// DiffLineInsert is an added line (shown with green background).
	DiffLineInsert
	// DiffLineDelete is a removed line (shown with red background).
	DiffLineDelete
)

// PatchLine represents a single line in a V4A patch output.
type PatchLine struct {
	Kind        DiffLineKind // Type: context, insert, or delete
	Content     string       // Raw content
	Styled      string       // Styled content with ANSI codes
	VisualWidth int          // Pre-computed visual width
	IsHeader    bool         // True if this is a file header line (*** Update/Add/Delete File:)
}

// PatchSection represents a section of a patch corresponding to one file.
type PatchSection struct {
	FilePath string      // Path to the file being patched
	Lines    []PatchLine // Patch lines for this section
}

// PatchResult stores pre-processed V4A Patch tool output.
// It implements toolrender.CachedResult so it can be cached on the message
// for efficient re-rendering on each frame.
type PatchResult struct {
	Sections    []PatchSection // Patch sections (one per file)
	RenderWidth int            // Width the patch was rendered for
	RawInput    string         // Original patch input (for re-rendering)
	Language    i18n.Language
}

// Ensure PatchResult implements CachedResult at compile time.
var _ toolrender.CachedResult = (*PatchResult)(nil)

// GetRenderedLines returns the pre-styled patch lines for rendering.
// This is the fast path - returns cached styled strings with no recomputation.
func (p *PatchResult) GetRenderedLines() []string {
	if p == nil || len(p.Sections) == 0 {
		width := 0
		if p != nil {
			width = p.RenderWidth
		}
		return []string{shared.FitLine("    "+ansiFgMuted+"⎿"+ansiReset+" "+ansiFgDim+i18n.T("toolrender.patch.empty")+ansiReset, width)}
	}

	var result []string
	for _, section := range p.Sections {
		for _, line := range section.Lines {
			result = append(result, line.Styled)
		}
	}
	// The cached path duplicates renderPatchOutput's layout, so it inherits the
	// same defect: codeWidth's minimum is a floor, not a cap, and the gutter is
	// added on top of it. Clamp here too.
	return shared.FitLines(result, p.RenderWidth)
}

// NeedsRerender checks if the PatchResult needs to be re-rendered at a new width.
func (p *PatchResult) NeedsRerender(width int) bool {
	if p == nil {
		return true
	}
	return p.RenderWidth != width || p.Language != i18n.CurrentLanguage()
}
