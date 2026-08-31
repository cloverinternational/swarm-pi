// Source: terminaltexteffects/engine/terminal.py
// Covers: Canvas, Terminal, TerminalConfig
//
// Terminal parses raw input text into EffectCharacters and owns the Canvas.
// The Canvas knows the geometry of the render area and provides spatial helpers.
// Terminal.GetFormattedOutputString() assembles each frame as a plain string
// that bubbletea/lipgloss can display inside a View().
package tteengine

import (
	"math/rand"
	"strings"
)

// ---------------------------------------------------------------------------
// TerminalConfig
// Source: terminal.py — TerminalConfig
// ---------------------------------------------------------------------------

// TerminalConfig carries rendering options forwarded to new Terminals.
type TerminalConfig struct {
	// TabWidth is the number of spaces a tab character expands to.
	TabWidth int
	// NoColor disables all ANSI colour output.
	NoColor bool
	// CanvasWidth/CanvasHeight: 0 means derive from input text.
	CanvasWidth  int
	CanvasHeight int
	// TextStartCol sets the column (1-indexed) at which the first character
	// of each input line is placed.  0 or 1 means the default left edge.
	// Use this to horizontally center text on a wider canvas without adding
	// space characters to the input (spaces become EffectCharacters and get
	// animated, which distorts effects like Rain and Scatter).
	TextStartCol int
	// IncludeTrailingSpaces disables the default trailing-whitespace strip so
	// that every cell in every line (up to CanvasWidth) becomes an
	// EffectCharacter.  Required for full-screen effects that must animate
	// background cells as well as text cells.
	IncludeTrailingSpaces bool
}

// DefaultTerminalConfig returns sensible defaults.
func DefaultTerminalConfig() TerminalConfig {
	return TerminalConfig{TabWidth: 4}
}

// ---------------------------------------------------------------------------
// Canvas
// Source: terminal.py — Canvas
// ---------------------------------------------------------------------------

// Canvas represents the rectangular render area.
// In the Python engine rows are 1-indexed with row 1 at the *bottom*.
// We keep that convention here for direct compatibility with all effects.
type Canvas struct {
	Top    int // highest row number (top of screen)
	Right  int // rightmost column number
	Bottom int // lowest row number (always 1)
	Left   int // leftmost column number (always 1)

	// Text extents — set after characters are laid out
	TextTop    int
	TextBottom int
	TextLeft   int
	TextRight  int

	// Derived centres
	CenterRow    int
	CenterColumn int
	Center       Coord

	TextCenterRow    int
	TextCenterColumn int
	TextCenter       Coord
}

// newCanvas builds a Canvas from pixel dimensions (1-indexed).
func newCanvas(width, height int) Canvas {
	c := Canvas{
		Top:    height,
		Right:  width,
		Bottom: 1,
		Left:   1,
	}
	c.CenterColumn = max(width/2, 1)
	c.CenterRow = max(height/2, 1)
	c.Center = Coord{Column: c.CenterColumn, Row: c.CenterRow}
	return c
}

// setTextBounds calculates text extents from a list of characters.
func (c *Canvas) setTextBounds(chars []*EffectCharacter) {
	if len(chars) == 0 {
		return
	}
	c.TextLeft = chars[0].inputCoord.Column
	c.TextRight = chars[0].inputCoord.Column
	c.TextBottom = chars[0].inputCoord.Row
	c.TextTop = chars[0].inputCoord.Row
	for _, ch := range chars[1:] {
		if ch.inputCoord.Column < c.TextLeft {
			c.TextLeft = ch.inputCoord.Column
		}
		if ch.inputCoord.Column > c.TextRight {
			c.TextRight = ch.inputCoord.Column
		}
		if ch.inputCoord.Row < c.TextBottom {
			c.TextBottom = ch.inputCoord.Row
		}
		if ch.inputCoord.Row > c.TextTop {
			c.TextTop = ch.inputCoord.Row
		}
	}
	c.TextCenterColumn = c.TextLeft + (c.TextRight-c.TextLeft)/2
	c.TextCenterRow = c.TextBottom + (c.TextTop-c.TextBottom)/2
	c.TextCenter = Coord{Column: c.TextCenterColumn, Row: c.TextCenterRow}
}

// CoordIsInCanvas reports whether coord falls inside the canvas bounds.
func (c Canvas) CoordIsInCanvas(coord Coord) bool {
	return coord.Column >= c.Left && coord.Column <= c.Right &&
		coord.Row >= c.Bottom && coord.Row <= c.Top
}

// CoordIsInText reports whether coord falls inside the text extents.
func (c Canvas) CoordIsInText(coord Coord) bool {
	return coord.Column >= c.TextLeft && coord.Column <= c.TextRight &&
		coord.Row >= c.TextBottom && coord.Row <= c.TextTop
}

// RandomCoord returns a random Coord inside the canvas.
// If outsideScope is true, a Coord just outside one of the four edges is returned.
// If withinTextBoundary is true (and outsideScope is false), the coord is
// limited to the text extents.
// Source: Canvas.random_coord
func (c Canvas) RandomCoord(outsideScope bool, withinTextBoundary bool) Coord {
	if outsideScope {
		above := Coord{Column: c.Left + rand.Intn(c.Right-c.Left+1), Row: c.Top + 1}
		below := Coord{Column: c.Left + rand.Intn(c.Right-c.Left+1), Row: c.Bottom - 1}
		left := Coord{Column: c.Left - 1, Row: c.Bottom + rand.Intn(c.Top-c.Bottom+1)}
		right := Coord{Column: c.Right + 1, Row: c.Bottom + rand.Intn(c.Top-c.Bottom+1)}
		choices := []Coord{above, below, left, right}
		return choices[rand.Intn(len(choices))]
	}
	if withinTextBoundary && c.TextRight > c.TextLeft {
		return Coord{
			Column: c.TextLeft + rand.Intn(c.TextRight-c.TextLeft+1),
			Row:    c.TextBottom + rand.Intn(c.TextTop-c.TextBottom+1),
		}
	}
	return Coord{
		Column: c.Left + rand.Intn(c.Right-c.Left+1),
		Row:    c.Bottom + rand.Intn(c.Top-c.Bottom+1),
	}
}

// ---------------------------------------------------------------------------
// Terminal
// Source: terminal.py — Terminal
// ---------------------------------------------------------------------------

// Terminal owns the character list and canvas; it is the root object that
// effects interact with.
type Terminal struct {
	Canvas TerminalCanvas
	Config TerminalConfig

	inputCharacters   []*EffectCharacter
	characterByCoord  map[Coord]*EffectCharacter
	visibleCharacters map[int]bool // keyed by character id

	// cellSymbols is a reusable scratch map for GetFormattedOutputString,
	// holding only occupied cells (key = rowIdx*cols + colIdx). Kept on the
	// struct so its backing store is reused across frames instead of being
	// reallocated every render.
	cellSymbols map[int]string
}

// TerminalCanvas embeds Canvas and is the public surface exposed to effects.
type TerminalCanvas struct {
	Canvas
}

// NewTerminal parses inputData into EffectCharacters and builds the Canvas.
// inputData should be plain UTF-8 text; ANSI sequences are stripped.
// Source: Terminal.__init__
func NewTerminal(inputData string, cfg TerminalConfig) *Terminal {
	if cfg.TabWidth == 0 {
		cfg.TabWidth = 4
	}
	inputData = strings.ReplaceAll(inputData, "\t", strings.Repeat(" ", cfg.TabWidth))

	lines := strings.Split(inputData, "\n")
	// strip trailing whitespace on each line (matches Python behaviour)
	// unless IncludeTrailingSpaces is set (full-screen burn mode).
	if !cfg.IncludeTrailingSpaces {
		for i, l := range lines {
			lines[i] = strings.TrimRight(l, " \t")
		}
	}

	// Determine canvas dimensions
	width := cfg.CanvasWidth
	height := cfg.CanvasHeight
	if width == 0 {
		for _, l := range lines {
			if len([]rune(l)) > width {
				width = len([]rune(l))
			}
		}
	}
	if height == 0 {
		height = len(lines)
	}
	if width < 1 {
		width = 1
	}
	if height < 1 {
		height = 1
	}

	canvas := newCanvas(width, height)

	// Parse characters.
	// Row numbering: line 0 (first line) → Row=height, last line → Row=1.
	// This matches the Python engine where row 1 is the bottom.
	// Horizontal start column: cfg.TextStartCol (default 1 = left edge).
	startCol := max(cfg.TextStartCol, 1)
	// Parse characters.
	// Row numbering: line 0 (first line) → Row=height, last line → Row=1.
	// This matches the Python engine where row 1 is the bottom.
	chars := make([]*EffectCharacter, 0)
	charID := 0
	for lineIdx, line := range lines {
		row := height - lineIdx // row 1 at the bottom
		col := startCol
		for _, r := range line {
			symbol := string(r)
			ch := newEffectCharacter(charID, symbol, col, row)
			chars = append(chars, ch)
			charID++
			col++
		}
	}

	canvas.setTextBounds(chars)

	byCoord := make(map[Coord]*EffectCharacter, len(chars))
	for _, ch := range chars {
		byCoord[ch.inputCoord] = ch
	}

	return &Terminal{
		Canvas:            TerminalCanvas{canvas},
		Config:            cfg,
		inputCharacters:   chars,
		characterByCoord:  byCoord,
		visibleCharacters: make(map[int]bool),
	}
}

// GetCharacters returns all input characters.  No sort is applied; effects
// can sort the returned slice as needed.
// Source: Terminal.get_characters
func (t *Terminal) GetCharacters() []*EffectCharacter {
	out := make([]*EffectCharacter, len(t.inputCharacters))
	copy(out, t.inputCharacters)
	return out
}

// GetCharacterByInputCoord returns the character at the given input coordinate,
// or nil if none exists at that position.
func (t *Terminal) GetCharacterByInputCoord(coord Coord) *EffectCharacter {
	return t.characterByCoord[coord]
}

// SetCharacterVisibility shows or hides a character.
// Source: Terminal.set_character_visibility
func (t *Terminal) SetCharacterVisibility(ch *EffectCharacter, visible bool) {
	ch.IsVisible = visible
	if visible {
		t.visibleCharacters[ch.id] = true
	} else {
		delete(t.visibleCharacters, ch.id)
	}
}

// GetFormattedOutputString assembles a complete frame string.
// Each row is rendered left-to-right; rows are separated by '\n'.
// Invisible characters are rendered as spaces.
// Source: Terminal.get_formatted_output_string
//
// Performance: previously this built a dense rows×cols [][]string grid filled
// with spaces every frame — the single largest source of allocation churn in
// the render path (~28% of cumulative alloc_space under stress profiling,
// dominated by the per-row make([]string) and the space-fill). Visible
// characters are sparse, so we instead record only occupied cells in a map and
// emit spaces inline, pre-sizing the builder to the exact output length. The
// produced string is byte-for-byte identical to the old implementation.
func (t *Terminal) GetFormattedOutputString() string {
	canvas := t.Canvas.Canvas

	rows := canvas.Top
	cols := canvas.Right
	// Match the original loop bounds exactly: with rows<=0 the row loop never
	// runs and the result is "". (cols<=0 is handled naturally below — the
	// column loop is skipped but row separators are still emitted.)
	if rows <= 0 {
		return ""
	}

	// Record only the visible (occupied) cells. Key = rowIdx*cols + colIdx.
	// Reuse a persistent map across frames to avoid re-allocating its backing
	// store every render; clearing keeps capacity.
	if t.cellSymbols == nil {
		t.cellSymbols = make(map[int]string)
	} else {
		clear(t.cellSymbols)
	}
	for _, ch := range t.inputCharacters {
		if !ch.IsVisible {
			continue
		}
		pos := ch.Motion.CurrentCoord.ToCoord()
		// Convert from bottom-indexed row to top-indexed array index.
		rowIdx := canvas.Top - pos.Row
		colIdx := pos.Column - 1
		if rowIdx < 0 || rowIdx >= rows || colIdx < 0 || colIdx >= cols {
			continue
		}
		t.cellSymbols[rowIdx*cols+colIdx] = ch.FormattedSymbol()
	}

	var sb strings.Builder
	// Lower bound: one byte per cell + row separators. Symbols may carry ANSI
	// escapes (wider), but Grow only needs a reasonable hint to cut regrowth.
	if hint := rows*cols + rows; hint > 0 {
		sb.Grow(hint)
	}
	for r := 0; r < rows; r++ {
		if r > 0 {
			sb.WriteByte('\n')
		}
		base := r * cols
		for c := 0; c < cols; c++ {
			if sym, ok := t.cellSymbols[base+c]; ok {
				sb.WriteString(sym)
			} else {
				sb.WriteByte(' ')
			}
		}
	}
	return sb.String()
}

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------
