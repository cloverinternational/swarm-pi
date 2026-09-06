package cellbuf

import (
	"image/color"
	"strings"
)

// SelectionStyle defines the colors for text selection
type SelectionStyle struct {
	Fg color.Color
	Bg color.Color
}

// selectionBounds tracks selection range for a line
type selectionBounds struct {
	startX, endX int
	inSelection  bool
}

// lineBounds tracks where actual text content is on a line
type lineBounds struct {
	start, end int
}

// ApplySelection applies selection highlighting to the buffer.
// This matches the logic in Crush's selectionView function (list.go:347-490).
//
// Parameters:
//   - sel: The selection rectangle (will be canonicalized)
//   - style: The selection foreground and background colors
//   - ignoreChars: Map of characters to skip (like icons)
func (b *Buffer) ApplySelection(sel Rectangle, style SelectionStyle, ignoreChars map[string]struct{}) {
	// Canonicalize selection (ensure Min < Max)
	sel = sel.Canon()

	// Make max Y exclusive for iteration (matches Crush: selArea.Max.Y++)
	// Already done in caller typically, but ensure it's correct

	// Pre-compute selection bounds for each line (Crush technique)
	lineSelections := make([]selectionBounds, b.height)

	for y := 0; y < b.height; y++ {
		bounds := selectionBounds{startX: -1, endX: -1, inSelection: false}

		if y >= sel.Min.Y && y < sel.Max.Y {
			bounds.inSelection = true

			if sel.Min.Y == sel.Max.Y-1 {
				// Single line selection
				bounds.startX = sel.Min.X
				bounds.endX = sel.Max.X
			} else if y == sel.Min.Y {
				// First line of multi-line selection
				bounds.startX = sel.Min.X
				bounds.endX = b.width
			} else if y == sel.Max.Y-1 {
				// Last line of multi-line selection
				bounds.startX = 0
				bounds.endX = sel.Max.X
			} else {
				// Middle lines - full width
				bounds.startX = 0
				bounds.endX = b.width
			}
		}
		lineSelections[y] = bounds
	}

	// First pass: find text bounds for lines that have selections (Crush technique)
	lineTextBounds := make([]lineBounds, b.height)

	for y := 0; y < b.height; y++ {
		bounds := lineBounds{start: -1, end: -1}

		if lineSelections[y].inSelection {
			for x := 0; x < b.width; x++ {
				cell := b.CellAt(x, y)
				if cell == nil {
					continue
				}

				// Skip empty cells
				if cell.Content == "" {
					continue
				}

				// Skip special characters (icons)
				if ignoreChars != nil {
					if _, isSpecial := ignoreChars[cell.Content]; isSpecial {
						continue
					}
				}

				// Check if it's non-whitespace or has background
				isNonWhitespace := cell.Content != " " && cell.Content != "\t" && cell.Content != "\x00"
				hasBg := cell.Style.Bg != nil

				if isNonWhitespace || hasBg {
					if bounds.start == -1 {
						bounds.start = x
					}
					bounds.end = x + 1 // Position after last character
				}
			}
		}
		lineTextBounds[y] = bounds
	}

	// Second pass: apply selection highlighting (Crush technique)
	for y := 0; y < b.height; y++ {
		selBounds := lineSelections[y]
		if !selBounds.inSelection {
			continue
		}

		textBounds := lineTextBounds[y]
		if textBounds.start < 0 {
			continue // No text on this line
		}

		// Only scan within the intersection of text bounds and selection bounds
		scanStart := max(textBounds.start, selBounds.startX)
		scanEnd := min(textBounds.end, selBounds.endX)

		for x := scanStart; x < scanEnd; x++ {
			cell := b.CellAt(x, y)
			if cell == nil {
				continue
			}

			// Skip empty content
			if cell.Content == "" {
				continue
			}

			// Skip special characters (icons)
			if ignoreChars != nil {
				if _, isSpecial := ignoreChars[cell.Content]; isSpecial {
					continue
				}
			}

			// Apply selection style
			cell.Style.Fg = style.Fg
			cell.Style.Bg = style.Bg
		}
	}
}

// GetSelectedText extracts text from the selection area.
// This matches the logic in Crush's GetSelectedText function.
//
// Parameters:
//   - sel: The selection rectangle
//   - ignoreChars: Map of characters to skip (like icons)
func (b *Buffer) GetSelectedText(sel Rectangle, ignoreChars map[string]struct{}) string {
	sel = sel.Canon()

	if sel.Empty() {
		return ""
	}

	var sb strings.Builder

	for y := sel.Min.Y; y < sel.Max.Y && y < b.height; y++ {
		// Calculate X range for this line
		startX := 0
		endX := b.width

		if sel.Min.Y == sel.Max.Y-1 {
			// Single line selection
			startX = sel.Min.X
			endX = sel.Max.X
		} else if y == sel.Min.Y {
			// First line
			startX = sel.Min.X
			endX = b.width
		} else if y == sel.Max.Y-1 {
			// Last line
			startX = 0
			endX = sel.Max.X
		}
		// Middle lines use full width (defaults)

		// Track pending whitespace (don't include trailing whitespace)
		var pending strings.Builder

		for x := startX; x < endX && x < b.width; x++ {
			cell := b.CellAt(x, y)
			if cell == nil || cell.IsZero() {
				continue
			}

			// Skip placeholder cells
			if cell.Width == 0 {
				continue
			}

			// Skip special characters
			if ignoreChars != nil {
				if _, isSpecial := ignoreChars[cell.Content]; isSpecial {
					continue
				}
			}

			// Handle whitespace - buffer it
			if cell.Content == " " {
				pending.WriteString(cell.Content)
				continue
			}

			// Non-whitespace: flush pending and add content
			sb.WriteString(pending.String())
			pending.Reset()
			sb.WriteString(cell.Content)
		}

		// Add newline between lines (not after last line)
		if y < sel.Max.Y-1 {
			sb.WriteByte('\n')
		}
	}

	return strings.TrimSpace(sb.String())
}

// NewScreenBuffer creates a new buffer suitable for screen operations.
// This matches ultraviolet's NewScreenBuffer function.
func NewScreenBuffer(width, height int) *Buffer {
	return NewBuffer(width, height)
}
