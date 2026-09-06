// Package diffview provides diff visualization with syntax highlighting
package diffview

import "github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/i18n"

// DiffLineKind represents the type of diff line
type DiffLineKind int

const (
	// DiffLineEqual represents an unchanged line (context)
	DiffLineEqual DiffLineKind = iota
	// DiffLineInsert represents an added line
	DiffLineInsert
	// DiffLineDelete represents a removed line
	DiffLineDelete
)

// String returns the symbol for this line kind
func (k DiffLineKind) String() string {
	switch k {
	case DiffLineInsert:
		return "+"
	case DiffLineDelete:
		return "-"
	default:
		return " "
	}
}

// DiffLine represents a single line in the diff
type DiffLine struct {
	Kind      DiffLineKind
	BeforeNum int    // Line number in original (0 if insert-only)
	AfterNum  int    // Line number in new (0 if delete-only)
	Content   string // The actual line content (without +/- prefix)
}

// DiffHunk represents a group of changes with surrounding context
type DiffHunk struct {
	BeforeStart int        // Starting line number in original
	AfterStart  int        // Starting line number in new
	BeforeCount int        // Number of lines from original
	AfterCount  int        // Number of lines from new
	Lines       []DiffLine // Lines in this hunk
}

// ComputedDiff holds the complete diff result
type ComputedDiff struct {
	BeforePath string     // Path to original file (empty for new files)
	AfterPath  string     // Path to new file
	Hunks      []DiffHunk // Grouped change hunks
	Additions  int        // Total lines added
	Deletions  int        // Total lines deleted
	TotalLines int        // Total lines in the diff
}

// IsEmpty returns true if there are no changes
func (d *ComputedDiff) IsEmpty() bool {
	return d.Additions == 0 && d.Deletions == 0
}

// Summary returns a human-readable summary like "+3, -2"
func (d *ComputedDiff) Summary() string {
	if d.IsEmpty() {
		return i18n.T("misc.diff.no_changes")
	}
	return "+" + itoa(d.Additions) + ", -" + itoa(d.Deletions)
}

// itoa is a simple int to string conversion
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	neg := n < 0
	if neg {
		n = -n
	}
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}

// CollapseLevel defines the display level for tool output
// Matches the definition in tool_state.go
type CollapseLevel int

const (
	// CollapseLevelCollapsed shows only tool name + "N lines"
	CollapseLevelCollapsed CollapseLevel = iota
	// CollapseLevelCompact shows tool name + first N lines + "N more"
	CollapseLevelCompact
	// CollapseLevelFull shows everything
	CollapseLevelFull
)

// LayoutType determines how the diff is displayed
type LayoutType int

const (
	// LayoutUnified shows a single-column diff with +/- prefixes
	LayoutUnified LayoutType = iota
	// LayoutSplit shows side-by-side comparison
	LayoutSplit
	// LayoutAuto automatically switches based on terminal width
	LayoutAuto
)

// StyledLine represents a fully styled line ready for rendering
type StyledLine struct {
	BeforeNum string // Styled "before" line number
	AfterNum  string // Styled "after" line number
	Symbol    string // Styled +/- symbol
	Code      string // Styled code content
}
