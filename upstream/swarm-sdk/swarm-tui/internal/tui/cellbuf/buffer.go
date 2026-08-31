package cellbuf

// Line is a row of cells
type Line []Cell

// NewLine creates a new line with the given width
func NewLine(width int) Line {
	line := make(Line, width)
	for i := range line {
		line[i] = EmptyCell
	}
	return line
}

// Buffer represents a 2D grid of cells that contains the contents of a screen.
// This matches ultraviolet's Buffer structure.
type Buffer struct {
	// Lines is a slice of lines that make up the cells of the buffer.
	Lines []Line

	width  int
	height int
}

// NewBuffer creates a new buffer with the given dimensions
func NewBuffer(width, height int) *Buffer {
	if width < 0 {
		width = 0
	}
	if height < 0 {
		height = 0
	}

	lines := make([]Line, height)
	for y := range lines {
		lines[y] = NewLine(width)
	}

	return &Buffer{
		Lines:  lines,
		width:  width,
		height: height,
	}
}

// Width returns buffer width
func (b *Buffer) Width() int {
	return b.width
}

// Height returns buffer height
func (b *Buffer) Height() int {
	return b.height
}

// Bounds returns the buffer bounds as a Rectangle
func (b *Buffer) Bounds() Rectangle {
	return Rect(0, 0, b.width, b.height)
}

// CellAt returns pointer to cell at x,y (nil if out of bounds)
// This matches ultraviolet's CellAt method used in Crush's selectionView
func (b *Buffer) CellAt(x, y int) *Cell {
	if x < 0 || x >= b.width || y < 0 || y >= b.height {
		return nil
	}
	return &b.Lines[y][x]
}

// SetCell sets cell at x,y
// This matches ultraviolet's SetCell method used in Crush's selectionView
func (b *Buffer) SetCell(x, y int, c *Cell) {
	if x < 0 || x >= b.width || y < 0 || y >= b.height {
		return
	}
	if c == nil {
		b.Lines[y][x] = EmptyCell
	} else {
		b.Lines[y][x] = *c
	}
}

// Line returns a row of cells (nil if out of bounds)
func (b *Buffer) Line(y int) Line {
	if y < 0 || y >= b.height {
		return nil
	}
	return b.Lines[y]
}

// Clear resets all cells to empty
func (b *Buffer) Clear() {
	for y := range b.Lines {
		for x := range b.Lines[y] {
			b.Lines[y][x] = EmptyCell
		}
	}
}

// ClearArea clears cells within the given area
func (b *Buffer) ClearArea(area Rectangle) {
	area = area.Intersect(b.Bounds())
	for y := area.Min.Y; y < area.Max.Y; y++ {
		for x := area.Min.X; x < area.Max.X; x++ {
			b.Lines[y][x] = EmptyCell
		}
	}
}

// Clone creates a copy of the buffer
func (b *Buffer) Clone() *Buffer {
	clone := NewBuffer(b.width, b.height)
	for y := range b.Lines {
		copy(clone.Lines[y], b.Lines[y])
	}
	return clone
}

// Fill fills all cells with the given cell
func (b *Buffer) Fill(c *Cell) {
	for y := range b.Lines {
		for x := range b.Lines[y] {
			if c == nil {
				b.Lines[y][x] = EmptyCell
			} else {
				b.Lines[y][x] = *c
			}
		}
	}
}

// FillArea fills cells within the given area with the given cell
func (b *Buffer) FillArea(c *Cell, area Rectangle) {
	area = area.Intersect(b.Bounds())
	for y := area.Min.Y; y < area.Max.Y; y++ {
		for x := area.Min.X; x < area.Max.X; x++ {
			if c == nil {
				b.Lines[y][x] = EmptyCell
			} else {
				b.Lines[y][x] = *c
			}
		}
	}
}

// Resize resizes the buffer to new dimensions
func (b *Buffer) Resize(width, height int) {
	if width < 0 {
		width = 0
	}
	if height < 0 {
		height = 0
	}

	// Create new lines
	newLines := make([]Line, height)
	for y := range newLines {
		newLines[y] = NewLine(width)
		// Copy existing content
		if y < len(b.Lines) {
			copyLen := min(len(b.Lines[y]), width)
			copy(newLines[y][:copyLen], b.Lines[y][:copyLen])
		}
	}

	b.Lines = newLines
	b.width = width
	b.height = height
}
