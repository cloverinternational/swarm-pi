// Package cellbuf provides a terminal cell buffer for efficient text rendering
// and selection highlighting. This is our own implementation inspired by
// charmbracelet/ultraviolet but tailored for our needs.
package cellbuf

import (
	"image/color"
)

// Style represents the visual attributes of a cell
type Style struct {
	Fg      color.Color // Foreground color (nil = default)
	Bg      color.Color // Background color (nil = default)
	Bold    bool
	Italic  bool
	Strike  bool
	Under   bool
	Reverse bool
	Dim     bool
}

// Cell represents a single terminal cell.
// This matches ultraviolet's Cell structure for compatibility with Crush patterns.
type Cell struct {
	// Content is the cell's content, which consists of a single grapheme
	// cluster. Most of the time, this will be a single rune, but it
	// can also be a combination of runes that form a grapheme cluster.
	Content string

	// Style is the visual style of the cell.
	Style Style

	// Width is the mono-spaced width of the grapheme cluster (1 or 2 for wide chars).
	Width int
}

// EmptyCell is a zero-value cell representing an empty space
var EmptyCell = Cell{Content: " ", Width: 1}

// Clone creates a copy of the cell
func (c *Cell) Clone() *Cell {
	return &Cell{
		Content: c.Content,
		Width:   c.Width,
		Style:   c.Style,
	}
}

// IsZero returns true if the cell is a zero value
func (c *Cell) IsZero() bool {
	return c.Content == "" && c.Width == 0
}

// IsBlank returns true if cell has no visible content
func (c *Cell) IsBlank() bool {
	return c.Content == "" || c.Content == " " || c.Content == "\x00"
}

// String returns the cell content
func (c *Cell) String() string {
	return c.Content
}

// Empty resets the cell to empty state
func (c *Cell) Empty() {
	c.Content = " "
	c.Width = 1
	c.Style = Style{}
}

// Equal checks if two cells are equal
func (c *Cell) Equal(o *Cell) bool {
	if c == nil || o == nil {
		return c == o
	}
	return c.Content == o.Content &&
		c.Width == o.Width &&
		c.Style == o.Style
}
