// Package capture provides frame capture and ANSI parsing for headless TUI automation.
package capture

import (
	"sync"
	"time"
)

// Frame represents a captured terminal frame.
type Frame struct {
	Width      int
	Height     int
	Cells      [][]Cell
	Content    string // Raw content without ANSI parsing
	Lines      []string
	Timestamp  time.Time
	RenderTime time.Duration
}

// Cell represents a single terminal cell.
type Cell struct {
	Rune  rune
	Style Style
}

// Style represents cell styling.
type Style struct {
	Foreground Color
	Background Color
	Bold       bool
	Dim        bool
	Italic     bool
	Underline  bool
	Reverse    bool
}

// Color represents a terminal color.
type Color struct {
	R, G, B uint8
	IsSet   bool
	Is256   bool
	Index   uint8 // For 256-color mode
}

// DefaultStyle returns the default unstyled style.
func DefaultStyle() Style {
	return Style{}
}

// NewFrame creates a new frame with the given dimensions.
func NewFrame(width, height int) *Frame {
	cells := make([][]Cell, height)
	for i := range cells {
		cells[i] = make([]Cell, width)
		for j := range cells[i] {
			cells[i][j] = Cell{Rune: ' ', Style: DefaultStyle()}
		}
	}

	return &Frame{
		Width:     width,
		Height:    height,
		Cells:     cells,
		Lines:     make([]string, height),
		Timestamp: time.Now(),
	}
}

// GetCell returns the cell at the given position.
func (f *Frame) GetCell(x, y int) *Cell {
	if y < 0 || y >= len(f.Cells) {
		return nil
	}
	if x < 0 || x >= len(f.Cells[y]) {
		return nil
	}
	return &f.Cells[y][x]
}

// SetCell sets the cell at the given position.
func (f *Frame) SetCell(x, y int, c Cell) {
	if y >= 0 && y < len(f.Cells) && x >= 0 && x < len(f.Cells[y]) {
		f.Cells[y][x] = c
	}
}

// GetText extracts text from a rectangular region.
func (f *Frame) GetText(x, y, width, height int) string {
	var result []byte

	for row := y; row < y+height && row < f.Height; row++ {
		if row < 0 {
			continue
		}
		for col := x; col < x+width && col < f.Width; col++ {
			if col < 0 {
				continue
			}
			cell := f.GetCell(col, row)
			if cell != nil && cell.Rune != 0 {
				result = append(result, string(cell.Rune)...)
			}
		}
		if row < y+height-1 {
			result = append(result, '\n')
		}
	}

	return string(result)
}

// GetLine returns a specific line as text.
func (f *Frame) GetLine(y int) string {
	if y < 0 || y >= len(f.Lines) {
		return ""
	}
	return f.Lines[y]
}

// GetAllText returns all text content.
func (f *Frame) GetAllText() string {
	return f.Content
}

// Clone creates a deep copy of the frame.
func (f *Frame) Clone() *Frame {
	clone := NewFrame(f.Width, f.Height)
	clone.Content = f.Content
	clone.Timestamp = f.Timestamp
	clone.RenderTime = f.RenderTime

	for y := range f.Cells {
		copy(clone.Cells[y], f.Cells[y])
	}
	copy(clone.Lines, f.Lines)

	return clone
}

// FrameBuffer manages frame capture with thread-safe access.
type FrameBuffer struct {
	mu          sync.RWMutex
	current     *Frame
	width       int
	height      int
	frameCount  int
	subscribers []chan *Frame
}

// NewFrameBuffer creates a new frame buffer.
func NewFrameBuffer(width, height int) *FrameBuffer {
	return &FrameBuffer{
		width:   width,
		height:  height,
		current: NewFrame(width, height),
	}
}

// Resize updates the buffer dimensions.
func (fb *FrameBuffer) Resize(width, height int) {
	fb.mu.Lock()
	defer fb.mu.Unlock()

	fb.width = width
	fb.height = height
	fb.current = NewFrame(width, height)
}

// SetContent sets the current frame content from raw output.
func (fb *FrameBuffer) SetContent(content string) {
	fb.mu.Lock()
	defer fb.mu.Unlock()

	start := time.Now()

	frame := NewFrame(fb.width, fb.height)
	frame.Content = content

	// Parse content into lines (simple split, ANSI handled separately)
	frame.Lines = splitLines(content, fb.height)

	// Parse into cells (with ANSI stripping for now)
	parseContentToCells(frame, content)

	frame.RenderTime = time.Since(start)
	fb.current = frame
	fb.frameCount++

	// Notify subscribers
	for _, ch := range fb.subscribers {
		select {
		case ch <- frame.Clone():
		default:
			// Non-blocking send
		}
	}
}

// GetFrame returns the current frame.
func (fb *FrameBuffer) GetFrame() *Frame {
	fb.mu.RLock()
	defer fb.mu.RUnlock()
	return fb.current.Clone()
}

// GetFrameCount returns the number of frames captured.
func (fb *FrameBuffer) GetFrameCount() int {
	fb.mu.RLock()
	defer fb.mu.RUnlock()
	return fb.frameCount
}

// Subscribe returns a channel that receives frame updates.
func (fb *FrameBuffer) Subscribe() chan *Frame {
	fb.mu.Lock()
	defer fb.mu.Unlock()

	ch := make(chan *Frame, 10)
	fb.subscribers = append(fb.subscribers, ch)
	return ch
}

// Unsubscribe removes a subscriber channel.
func (fb *FrameBuffer) Unsubscribe(ch chan *Frame) {
	fb.mu.Lock()
	defer fb.mu.Unlock()

	for i, sub := range fb.subscribers {
		if sub == ch {
			fb.subscribers = append(fb.subscribers[:i], fb.subscribers[i+1:]...)
			close(ch)
			break
		}
	}
}

// splitLines splits content into lines, padding/truncating to fit height.
func splitLines(content string, maxLines int) []string {
	lines := make([]string, 0, maxLines)
	start := 0

	for i := 0; i < len(content); i++ {
		if content[i] == '\n' {
			lines = append(lines, content[start:i])
			start = i + 1
			if len(lines) >= maxLines {
				break
			}
		}
	}

	// Add last line if no trailing newline
	if start < len(content) && len(lines) < maxLines {
		lines = append(lines, content[start:])
	}

	// Pad to maxLines
	for len(lines) < maxLines {
		lines = append(lines, "")
	}

	return lines[:maxLines]
}

// parseContentToCells parses content into the frame's cell buffer.
func parseContentToCells(frame *Frame, content string) {
	x, y := 0, 0
	style := DefaultStyle()
	inEscape := false
	escapeSeq := make([]byte, 0, 32)

	for i := 0; i < len(content); i++ {
		c := content[i]

		if inEscape {
			escapeSeq = append(escapeSeq, c)
			if isEscapeTerminator(c) {
				// Parse and apply escape sequence
				style = parseEscapeSequence(escapeSeq, style)
				inEscape = false
				escapeSeq = escapeSeq[:0]
			}
			continue
		}

		if c == '\x1b' {
			inEscape = true
			escapeSeq = append(escapeSeq[:0], c)
			continue
		}

		if c == '\n' {
			y++
			x = 0
			continue
		}

		if c == '\r' {
			x = 0
			continue
		}

		if y < frame.Height && x < frame.Width {
			frame.Cells[y][x] = Cell{Rune: rune(c), Style: style}
			x++
		}
	}
}

// isEscapeTerminator returns true if the byte terminates an escape sequence.
func isEscapeTerminator(c byte) bool {
	return (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') || c == '~'
}

// parseEscapeSequence parses an ANSI escape sequence and returns updated style.
func parseEscapeSequence(seq []byte, current Style) Style {
	// Basic SGR (Select Graphic Rendition) parsing
	// Format: ESC [ <params> m
	if len(seq) < 3 || seq[1] != '[' {
		return current
	}

	// Find 'm' terminator for SGR
	if seq[len(seq)-1] != 'm' {
		return current
	}

	// Parse parameters
	params := seq[2 : len(seq)-1]
	codes := parseCSIParams(params)

	for i := 0; i < len(codes); i++ {
		code := codes[i]
		switch code {
		case 0:
			current = DefaultStyle()
		case 1:
			current.Bold = true
		case 2:
			current.Dim = true
		case 3:
			current.Italic = true
		case 4:
			current.Underline = true
		case 7:
			current.Reverse = true
		case 22:
			current.Bold = false
			current.Dim = false
		case 23:
			current.Italic = false
		case 24:
			current.Underline = false
		case 27:
			current.Reverse = false
		case 30, 31, 32, 33, 34, 35, 36, 37:
			current.Foreground = basicColor(code - 30)
		case 38:
			// Extended foreground color
			if i+2 < len(codes) && codes[i+1] == 5 {
				current.Foreground = color256(codes[i+2])
				i += 2
			} else if i+4 < len(codes) && codes[i+1] == 2 {
				current.Foreground = colorRGB(codes[i+2], codes[i+3], codes[i+4])
				i += 4
			}
		case 39:
			current.Foreground = Color{}
		case 40, 41, 42, 43, 44, 45, 46, 47:
			current.Background = basicColor(code - 40)
		case 48:
			// Extended background color
			if i+2 < len(codes) && codes[i+1] == 5 {
				current.Background = color256(codes[i+2])
				i += 2
			} else if i+4 < len(codes) && codes[i+1] == 2 {
				current.Background = colorRGB(codes[i+2], codes[i+3], codes[i+4])
				i += 4
			}
		case 49:
			current.Background = Color{}
		case 90, 91, 92, 93, 94, 95, 96, 97:
			current.Foreground = brightColor(code - 90)
		case 100, 101, 102, 103, 104, 105, 106, 107:
			current.Background = brightColor(code - 100)
		}
	}

	return current
}

// parseCSIParams parses semicolon-separated CSI parameters.
func parseCSIParams(params []byte) []int {
	if len(params) == 0 {
		return []int{0}
	}

	codes := make([]int, 0, 8)
	current := 0

	for _, b := range params {
		if b >= '0' && b <= '9' {
			current = current*10 + int(b-'0')
		} else if b == ';' {
			codes = append(codes, current)
			current = 0
		}
	}
	codes = append(codes, current)

	return codes
}

// basicColor returns a basic ANSI color (0-7).
func basicColor(index int) Color {
	return Color{IsSet: true, Is256: true, Index: uint8(index)}
}

// brightColor returns a bright ANSI color (8-15).
func brightColor(index int) Color {
	return Color{IsSet: true, Is256: true, Index: uint8(index + 8)}
}

// color256 returns a 256-color palette color.
func color256(index int) Color {
	return Color{IsSet: true, Is256: true, Index: uint8(index)}
}

// colorRGB returns a true color RGB color.
func colorRGB(r, g, b int) Color {
	return Color{IsSet: true, R: uint8(r), G: uint8(g), B: uint8(b)}
}
