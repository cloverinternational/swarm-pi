package diffview

import (
	"charm.land/lipgloss/v2"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/ui/palette"
)

// DiffStyle defines the complete color scheme for diff rendering
type DiffStyle struct {
	// Background colors (darker for line numbers, lighter for code)
	AdditionBGDark string // Dark green for line numbers
	AdditionBG     string // Light green for code
	DeletionBGDark string // Dark red for line numbers
	DeletionBG     string // Light red for code
	ContextBG      string // Subtle background for context lines

	// Line number styles
	AdditionLineNum lipgloss.Style
	DeletionLineNum lipgloss.Style
	ContextLineNum  lipgloss.Style

	// Symbol styles (+/-)
	AdditionSymbol lipgloss.Style
	DeletionSymbol lipgloss.Style
	ContextSymbol  lipgloss.Style

	// Code content styles (background only, foreground from syntax highlighting)
	AdditionCode lipgloss.Style
	DeletionCode lipgloss.Style
	ContextCode  lipgloss.Style

	// Header/separator styles
	HunkHeader lipgloss.Style
	Separator  lipgloss.Style
}

// DefaultDarkStyle returns the default dark theme style for diffs
// Colors inspired by GitHub dark theme and VS Code
func DefaultDarkStyle() DiffStyle {
	// Background colors matching the screenshot
	additionBGDark := "#293229" // Dark green for line numbers
	additionBG := "#1e3a1e"     // Light green for code (more visible)
	deletionBGDark := "#332929" // Dark red for line numbers
	deletionBG := "#3a1e1e"     // Light red for code (more visible)
	contextBG := ""             // No background for context (or subtle: "#1a1a1a")

	return DiffStyle{
		AdditionBGDark: additionBGDark,
		AdditionBG:     additionBG,
		DeletionBGDark: deletionBGDark,
		DeletionBG:     deletionBG,
		ContextBG:      contextBG,

		// Line numbers
		AdditionLineNum: lipgloss.NewStyle().
			Foreground(lipgloss.Color(palette.Success)).
			Background(lipgloss.Color(additionBGDark)),
		DeletionLineNum: lipgloss.NewStyle().
			Foreground(lipgloss.Color(palette.Error)).
			Background(lipgloss.Color(deletionBGDark)),
		ContextLineNum: lipgloss.NewStyle().
			Foreground(lipgloss.Color(palette.TextMuted)).
			Background(lipgloss.Color(palette.Border)),

		// Symbols (+/-)
		AdditionSymbol: lipgloss.NewStyle().
			Foreground(lipgloss.Color(palette.Success)).
			Background(lipgloss.Color(additionBG)).
			Bold(true),
		DeletionSymbol: lipgloss.NewStyle().
			Foreground(lipgloss.Color(palette.Error)).
			Background(lipgloss.Color(deletionBG)).
			Bold(true),
		ContextSymbol: lipgloss.NewStyle().
			Foreground(lipgloss.Color(palette.TextMuted)),

		// Code content (background will extend full width)
		AdditionCode: lipgloss.NewStyle().
			Background(lipgloss.Color(additionBG)),
		DeletionCode: lipgloss.NewStyle().
			Background(lipgloss.Color(deletionBG)),
		ContextCode: lipgloss.NewStyle(),

		// Headers
		HunkHeader: lipgloss.NewStyle().
			Foreground(lipgloss.Color(palette.Info)).
			Background(lipgloss.Color(palette.Border)),
		Separator: lipgloss.NewStyle().
			Foreground(lipgloss.Color(palette.Border)),
	}
}

// GetLineNumStyle returns the appropriate style for a line number
func (s *DiffStyle) GetLineNumStyle(kind DiffLineKind) lipgloss.Style {
	switch kind {
	case DiffLineInsert:
		return s.AdditionLineNum
	case DiffLineDelete:
		return s.DeletionLineNum
	default:
		return s.ContextLineNum
	}
}

// GetSymbolStyle returns the appropriate style for a +/- symbol
func (s *DiffStyle) GetSymbolStyle(kind DiffLineKind) lipgloss.Style {
	switch kind {
	case DiffLineInsert:
		return s.AdditionSymbol
	case DiffLineDelete:
		return s.DeletionSymbol
	default:
		return s.ContextSymbol
	}
}

// GetCodeStyle returns the appropriate style for code content
func (s *DiffStyle) GetCodeStyle(kind DiffLineKind) lipgloss.Style {
	switch kind {
	case DiffLineInsert:
		return s.AdditionCode
	case DiffLineDelete:
		return s.DeletionCode
	default:
		return s.ContextCode
	}
}

// GetBGColor returns the background color string for a line kind
func (s *DiffStyle) GetBGColor(kind DiffLineKind) string {
	switch kind {
	case DiffLineInsert:
		return s.AdditionBG
	case DiffLineDelete:
		return s.DeletionBG
	default:
		return s.ContextBG
	}
}
