# Crush Scrolling, Selection & Highlighting Architecture

## Deep Technical Analysis + Custom Cell Buffer Design

This document provides an exhaustive analysis of how Crush achieves fast, efficient scrolling, text selection, and highlighting. It includes a complete design for our own cell buffer system to replace the external ultraviolet dependency.

---

## Table of Contents

1. [Core Data Structures](#1-core-data-structures)
2. [The Rendering Pipeline](#2-the-rendering-pipeline)
3. [Line Offset System (O(1) Access)](#3-line-offset-system-o1-access)
4. [View Caching Mechanics](#4-view-caching-mechanics)
5. [Scrolling Engine](#5-scrolling-engine)
6. [Mouse Coordinate Translation](#6-mouse-coordinate-translation)
7. [Selection State Machine](#7-selection-state-machine)
8. [Custom Cell Buffer System Design](#8-custom-cell-buffer-system-design)
9. [Word & Paragraph Detection](#9-word--paragraph-detection)
10. [Performance Optimizations Summary](#10-performance-optimizations-summary)
11. [Implementation Plan](#11-implementation-plan)

---

## 1. Core Data Structures

### 1.1 The `renderedItem` Struct

```go
// Location: example/crush/internal/tui/exp/list/list.go:91-96
type renderedItem struct {
    view   string  // The rendered string output of this item
    height int     // Height in terminal lines
    start  int     // First line index in the COMPLETE rendered content
    end    int     // Last line index in the COMPLETE rendered content
}
```

**Purpose**: Tracks where each item lives in the complete rendered output.
- `start` and `end` are **absolute line positions** in the full content
- Used for scroll-to-selection and viewport visibility checks
- Cached per item - only invalidated when item content changes

### 1.2 The `list` Struct (Core State)

```go
// Location: example/crush/internal/tui/exp/list/list.go:111-136
type list[T Item] struct {
    *confOptions

    // === SCROLL STATE ===
    offset int  // Current scroll offset (lines from top OR bottom based on direction)

    // === ITEM TRACKING ===
    indexMap      map[string]int           // O(1) lookup: item ID → array index
    items         []T                       // The actual items
    renderedItems map[string]renderedItem  // Cache: item ID → rendered output + position

    // === COMPLETE RENDERED OUTPUT ===
    rendered       string   // The ENTIRE rendered content as one string
    renderedHeight int      // Total height of rendered content
    lineOffsets    []int    // Byte offsets for each line (for O(1) slicing)

    // === VIEW CACHING ===
    cachedView       string  // Cached viewport output
    cachedViewOffset int     // Offset when view was cached
    cachedViewDirty  bool    // True = cache is invalid

    // === SELECTION STATE ===
    selectionStartCol   int
    selectionStartLine  int
    selectionEndCol     int
    selectionEndLine    int
    selectionActive     bool  // True = user is actively dragging

    // === ITEM NAVIGATION ===
    movingByItem        bool  // True when navigating item-by-item (vs scrolling)
    prevSelectedItemIdx int   // Previously selected item (for blur/focus)
}
```

**Key Insight**: The `rendered` string contains ALL items concatenated together. The `lineOffsets` array enables O(1) access to any line range without string splitting.

---

## 2. The Rendering Pipeline

### 2.1 High-Level Flow

```
┌─────────────────────────────────────────────────────────────────┐
│                        render()                                  │
│  Location: list.go:652-692                                       │
├─────────────────────────────────────────────────────────────────┤
│                                                                  │
│  1. setDefaultSelected()  ─────► Ensure something is selected    │
│                                                                  │
│  2. focusSelectedItem()   ─────► Blur old, focus new item        │
│                                                                  │
│  3. renderIterator()      ─────► Build complete content          │
│      │                                                           │
│      ├── First pass (if first render): limitHeight=true         │
│      │   • Render items until viewport is full                   │
│      │   • Returns finishIndex                                   │
│      │                                                           │
│      └── Second pass: limitHeight=false                          │
│          • Render remaining items                                │
│          • Builds complete content                               │
│                                                                  │
│  4. setRendered()         ─────► Store content + build offsets   │
│                                                                  │
│  5. recalculateItemPositions() ─► Update start/end for each item│
│                                                                  │
│  6. scrollToSelection()   ─────► Scroll to keep selection visible│
│                                                                  │
└─────────────────────────────────────────────────────────────────┘
```

### 2.2 The `renderIterator()` Function (The Heart)

**Two-phase approach**:

**Phase 1: Collect Fragments** (lines 972-1008)
- Iterates through items
- Checks cache for each item
- If not cached, renders item and stores position
- Creates `renderFragment{view, gap}` for each item
- Stops when height limit reached (if `limitHeight=true`)

**Phase 2: Build String Efficiently** (lines 1010-1054)
- Pre-sizes StringBuilder with `b.Grow(estimatedSize)` - **CRITICAL**
- For forward direction: writes existing content first, then fragments
- For backward direction: writes fragments in reverse, then existing
- Uses pre-allocated `newlineBuffer` for gaps

**Critical Optimizations**:
1. **Two-pass approach**: Collect fragments first, build string once
2. **Pre-allocated StringBuilder**: `b.Grow(estimatedSize)` prevents reallocations
3. **Pre-allocated newline buffer**: `newlineBuffer = strings.Repeat("\n", 100)`
4. **Item cache**: `renderedItems` map prevents re-rendering unchanged items

---

## 3. Line Offset System (O(1) Access)

### 3.1 Building the Offset Table

```go
// Location: list.go:546-567
func (l *list[T]) setRendered(rendered string) {
    l.rendered = rendered
    l.renderedHeight = lipgloss.Height(rendered)
    l.cachedViewDirty = true

    if len(rendered) > 0 {
        // Pre-allocate with expected capacity
        l.lineOffsets = make([]int, 0, l.renderedHeight)
        l.lineOffsets = append(l.lineOffsets, 0)  // First line starts at byte 0

        offset := 0
        for {
            idx := strings.IndexByte(rendered[offset:], '\n')
            if idx == -1 { break }
            offset += idx + 1
            l.lineOffsets = append(l.lineOffsets, offset)
        }
    }
}
```

**Memory Layout Example**:
```
rendered = "Line 0 content\nLine 1 content\nLine 2 content"
            ^              ^               ^
            byte 0         byte 15         byte 30

lineOffsets = [0, 15, 30]
```

### 3.2 O(1) Line Access

```go
func (l *list[T]) getLines(start, end int) string {
    // Get byte offsets directly - NO ITERATION!
    startOffset := l.lineOffsets[start]
    endOffset := l.lineOffsets[end+1] - 1  // -1 to exclude newline

    // Direct string slice - O(1)!
    return l.rendered[startOffset:endOffset]
}
```

**Why This Is Fast**:
- **NO `strings.Split()`**: Splitting allocates a new slice + new strings for every line
- **Direct slicing**: `rendered[start:end]` is O(1) - just pointer + length
- **Zero allocation**: The returned string shares memory with `rendered`

---

## 4. View Caching Mechanics

```go
func (l *list[T]) View() string {
    // === FAST PATH: Return cached view ===
    if !l.cachedViewDirty &&           // Cache is valid
       l.cachedViewOffset == l.offset && // Same scroll position
       !l.hasSelection() &&             // No selection active
       l.cachedView != "" {             // Cache exists
        return l.cachedView             // O(1) return!
    }

    // === SLOW PATH: Regenerate view ===
    view := l.getLines(viewStart, viewEnd)
    view = t.S().Base.Height(l.height).Width(l.width).Render(view)

    // Cache if no selection
    if !l.hasSelection() {
        l.cachedView = view
        l.cachedViewOffset = l.offset
        l.cachedViewDirty = false
    }

    // Selection active - apply highlighting
    return l.selectionView(view, false)
}
```

---

## 5. Scrolling Engine

### 5.1 Direction-Aware View Position

```go
func (l *list[T]) viewPosition() (int, int) {
    renderedLines := l.renderedHeight - 1

    if l.direction == DirectionForward {
        // Traditional: offset = lines scrolled from top
        start = max(0, l.offset)
        end = min(l.offset + l.height - 1, renderedLines)
    } else {
        // DirectionBackward: Content anchored at BOTTOM (chat-style)
        start = max(0, renderedLines - l.offset - l.height + 1)
        end = max(0, renderedLines - l.offset)
    }
    return start, end
}
```

### 5.2 Selection-Synchronized Scrolling

```go
func (l *list[T]) MoveDown(n int) tea.Cmd {
    oldOffset := l.offset
    l.incrementOffset(n)

    if oldOffset == l.offset { return nil }

    // === CRITICAL: MOVE SELECTION WITH SCROLL ===
    if l.hasSelection() && !l.selectionActive {
        l.selectionStartLine -= n
        l.selectionEndLine -= n
    }
    if l.selectionActive {
        // Only move anchor point
        if l.selectionStartLine < l.selectionEndLine {
            l.selectionStartLine -= n
        } else {
            l.selectionEndLine -= n
        }
    }
    return l.changeSelectionWhenScrolling()
}
```

---

## 6. Mouse Coordinate Translation

### 6.1 Viewport to Content Line Translation

```go
func (l *list[T]) findWordBoundaries(col, line int) (startCol, endCol int) {
    numLines := l.lineCount()

    // === TRANSLATE VIEWPORT LINE TO CONTENT LINE ===

    // For backward direction, viewport is "upside down"
    if l.direction == DirectionBackward && numLines > l.height {
        line = ((numLines - 1) - l.height) + line + 1
    }

    // Apply scroll offset
    if l.offset > 0 {
        if l.direction == DirectionBackward {
            line -= l.offset
        } else {
            line += l.offset
        }
    }

    // Now 'line' is the actual content line index
    currentLine := ansi.Strip(l.getLine(line))
    // ... word boundary detection
}
```

---

## 7. Selection State Machine

```
┌─────────────────────────────────────────────────────────────────┐
│                    SELECTION STATE MACHINE                       │
├─────────────────────────────────────────────────────────────────┤
│                                                                  │
│  IDLE ──[MouseClick]──► SELECTION_ACTIVE (selectionActive=true) │
│                         Start = End = click position             │
│                                                                  │
│  ──[MouseMotion]──► SELECTING (start ≠ end)                      │
│                     End follows mouse                            │
│                                                                  │
│  ──[MouseRelease]──► SELECTION_COMPLETE (selectionActive=false)  │
│                      Selection is "frozen"                       │
│                      Scroll adjusts both start AND end           │
│                                                                  │
│  ──[New Click]──► IDLE (or new selection)                        │
│                                                                  │
└─────────────────────────────────────────────────────────────────┘
```

---

## 8. Custom Cell Buffer System Design

Instead of using ultraviolet, we'll build a minimal but efficient cell buffer system.

### 8.1 Core Types

```go
// internal/tui/cellbuf/cell.go
package cellbuf

import (
    "image/color"
)

// Cell represents a single terminal cell
type Cell struct {
    Content string      // Grapheme cluster (usually 1 rune)
    Width   int         // Display width (1 or 2 for wide chars)
    Fg      color.Color // Foreground color (nil = default)
    Bg      color.Color // Background color (nil = default)
    Bold    bool
    Italic  bool
    Strike  bool
    Under   bool
}

// EmptyCell is a zero-value cell
var EmptyCell = Cell{Content: " ", Width: 1}

// Clone creates a copy of the cell
func (c *Cell) Clone() Cell {
    return Cell{
        Content: c.Content,
        Width:   c.Width,
        Fg:      c.Fg,
        Bg:      c.Bg,
        Bold:    c.Bold,
        Italic:  c.Italic,
        Strike:  c.Strike,
        Under:   c.Under,
    }
}

// IsBlank returns true if cell has no visible content
func (c *Cell) IsBlank() bool {
    return c.Content == "" || c.Content == " " || c.Content == "\x00"
}
```

### 8.2 Buffer Implementation

```go
// internal/tui/cellbuf/buffer.go
package cellbuf

// Buffer is a 2D grid of cells
type Buffer struct {
    cells  [][]Cell
    width  int
    height int
}

// NewBuffer creates a buffer with given dimensions
func NewBuffer(width, height int) *Buffer {
    cells := make([][]Cell, height)
    for y := range cells {
        cells[y] = make([]Cell, width)
        for x := range cells[y] {
            cells[y][x] = EmptyCell
        }
    }
    return &Buffer{
        cells:  cells,
        width:  width,
        height: height,
    }
}

// Width returns buffer width
func (b *Buffer) Width() int { return b.width }

// Height returns buffer height
func (b *Buffer) Height() int { return b.height }

// CellAt returns pointer to cell at x,y (nil if out of bounds)
func (b *Buffer) CellAt(x, y int) *Cell {
    if x < 0 || x >= b.width || y < 0 || y >= b.height {
        return nil
    }
    return &b.cells[y][x]
}

// SetCell sets cell at x,y
func (b *Buffer) SetCell(x, y int, c Cell) {
    if x < 0 || x >= b.width || y < 0 || y >= b.height {
        return
    }
    b.cells[y][x] = c
}

// Clear resets all cells to empty
func (b *Buffer) Clear() {
    for y := range b.cells {
        for x := range b.cells[y] {
            b.cells[y][x] = EmptyCell
        }
    }
}

// Line returns a row of cells
func (b *Buffer) Line(y int) []Cell {
    if y < 0 || y >= b.height {
        return nil
    }
    return b.cells[y]
}
```

### 8.3 ANSI Parser

```go
// internal/tui/cellbuf/parser.go
package cellbuf

import (
    "github.com/charmbracelet/x/ansi"
    "github.com/rivo/uniseg"
)

// ParseANSI parses an ANSI-styled string into a buffer
func ParseANSI(s string, buf *Buffer) {
    // Current style state
    var style Cell
    style.Width = 1

    x, y := 0, 0

    // Use ansi.Parser to extract style sequences
    p := ansi.NewParser(ansi.MaxParamsSize, 0)

    var seq []byte
    for i := 0; i < len(s); {
        seq, _, i, _ = ansi.DecodeSequence(s, i, p)

        if len(seq) == 0 {
            continue
        }

        // Check if it's a control sequence
        if seq[0] == '\x1b' {
            // Parse SGR (style) parameters
            parseStyleSequence(seq, &style)
            continue
        }

        // Check for newline
        if seq[0] == '\n' {
            y++
            x = 0
            continue
        }

        // Regular character - iterate grapheme clusters
        gr := uniseg.NewGraphemes(string(seq))
        for gr.Next() {
            cluster := gr.Str()
            width := gr.Width()

            if x < buf.width && y < buf.height {
                cell := style.Clone()
                cell.Content = cluster
                cell.Width = width
                buf.SetCell(x, y, cell)
            }

            x += width
        }
    }
}

// parseStyleSequence parses SGR sequence and updates style
func parseStyleSequence(seq []byte, style *Cell) {
    // Parse CSI parameters
    params := ansi.Params(seq)

    for i := 0; i < len(params); i++ {
        p := params[i]
        switch p {
        case 0: // Reset
            *style = Cell{Width: 1}
        case 1: // Bold
            style.Bold = true
        case 3: // Italic
            style.Italic = true
        case 4: // Underline
            style.Under = true
        case 9: // Strikethrough
            style.Strike = true
        case 22: // Not bold
            style.Bold = false
        case 23: // Not italic
            style.Italic = false
        case 24: // Not underline
            style.Under = false
        case 29: // Not strikethrough
            style.Strike = false
        case 30, 31, 32, 33, 34, 35, 36, 37: // Standard FG colors
            style.Fg = ansiColor(p - 30)
        case 38: // Extended FG
            style.Fg, i = parseExtendedColor(params, i)
        case 39: // Default FG
            style.Fg = nil
        case 40, 41, 42, 43, 44, 45, 46, 47: // Standard BG colors
            style.Bg = ansiColor(p - 40)
        case 48: // Extended BG
            style.Bg, i = parseExtendedColor(params, i)
        case 49: // Default BG
            style.Bg = nil
        case 90, 91, 92, 93, 94, 95, 96, 97: // Bright FG
            style.Fg = ansiBrightColor(p - 90)
        case 100, 101, 102, 103, 104, 105, 106, 107: // Bright BG
            style.Bg = ansiBrightColor(p - 100)
        }
    }
}
```

### 8.4 Buffer Renderer

```go
// internal/tui/cellbuf/render.go
package cellbuf

import (
    "fmt"
    "strings"
)

// Render converts buffer back to ANSI string
func (b *Buffer) Render() string {
    var sb strings.Builder
    sb.Grow(b.width * b.height * 4) // Estimate

    var lastCell *Cell

    for y := 0; y < b.height; y++ {
        for x := 0; x < b.width; {
            cell := b.CellAt(x, y)
            if cell == nil {
                x++
                continue
            }

            // Only emit style changes
            if lastCell == nil || !sameStyle(lastCell, cell) {
                sb.WriteString(styleToANSI(cell))
            }

            sb.WriteString(cell.Content)
            lastCell = cell
            x += max(1, cell.Width)
        }

        // Reset at end of line and newline
        if y < b.height-1 {
            sb.WriteString("\x1b[0m\n")
            lastCell = nil
        }
    }

    sb.WriteString("\x1b[0m") // Final reset
    return sb.String()
}

func sameStyle(a, b *Cell) bool {
    return a.Fg == b.Fg && a.Bg == b.Bg &&
           a.Bold == b.Bold && a.Italic == b.Italic &&
           a.Strike == b.Strike && a.Under == b.Under
}

func styleToANSI(c *Cell) string {
    var codes []string

    if c.Bold { codes = append(codes, "1") }
    if c.Italic { codes = append(codes, "3") }
    if c.Under { codes = append(codes, "4") }
    if c.Strike { codes = append(codes, "9") }

    if c.Fg != nil {
        codes = append(codes, fgColorCode(c.Fg))
    }
    if c.Bg != nil {
        codes = append(codes, bgColorCode(c.Bg))
    }

    if len(codes) == 0 {
        return ""
    }

    return fmt.Sprintf("\x1b[%sm", strings.Join(codes, ";"))
}
```

### 8.5 Selection Highlighter

```go
// internal/tui/cellbuf/selection.go
package cellbuf

import "image/color"

// Rectangle represents a selection area
type Rectangle struct {
    MinX, MinY int
    MaxX, MaxY int // Exclusive
}

// Normalize ensures Min < Max
func (r *Rectangle) Normalize() Rectangle {
    nr := *r
    if nr.MinX > nr.MaxX {
        nr.MinX, nr.MaxX = nr.MaxX, nr.MinX
    }
    if nr.MinY > nr.MaxY {
        nr.MinY, nr.MaxY = nr.MaxY, nr.MinY
    }
    return nr
}

// ApplySelection highlights cells within the selection area
func (b *Buffer) ApplySelection(sel Rectangle, fg, bg color.Color) {
    sel = sel.Normalize()

    for y := sel.MinY; y < sel.MaxY && y < b.height; y++ {
        // Calculate X range for this line
        startX := 0
        endX := b.width

        if sel.MinY == sel.MaxY-1 {
            // Single line selection
            startX = sel.MinX
            endX = sel.MaxX
        } else if y == sel.MinY {
            // First line of multi-line
            startX = sel.MinX
            endX = b.width
        } else if y == sel.MaxY-1 {
            // Last line of multi-line
            startX = 0
            endX = sel.MaxX
        }
        // Middle lines: full width (defaults)

        // Apply selection style to each cell
        for x := startX; x < endX && x < b.width; x++ {
            cell := b.CellAt(x, y)
            if cell == nil || cell.IsBlank() {
                continue
            }
            cell.Fg = fg
            cell.Bg = bg
        }
    }
}

// GetText extracts text from selection area
func (b *Buffer) GetText(sel Rectangle, ignoreChars map[string]struct{}) string {
    sel = sel.Normalize()

    var sb strings.Builder

    for y := sel.MinY; y < sel.MaxY && y < b.height; y++ {
        startX := 0
        endX := b.width

        if sel.MinY == sel.MaxY-1 {
            startX = sel.MinX
            endX = sel.MaxX
        } else if y == sel.MinY {
            startX = sel.MinX
        } else if y == sel.MaxY-1 {
            endX = sel.MaxX
        }

        for x := startX; x < endX && x < b.width; x++ {
            cell := b.CellAt(x, y)
            if cell == nil {
                continue
            }
            // Skip ignored characters (icons)
            if ignoreChars != nil {
                if _, skip := ignoreChars[cell.Content]; skip {
                    continue
                }
            }
            sb.WriteString(cell.Content)
        }

        if y < sel.MaxY-1 {
            sb.WriteByte('\n')
        }
    }

    return strings.TrimRight(sb.String(), " \n")
}
```

### 8.6 Usage Example

```go
// In messagelist.go selection rendering
func (m *MessageList) selectionView(view string) string {
    // Create buffer
    buf := cellbuf.NewBuffer(m.Width, m.Height)

    // Parse ANSI into cells
    cellbuf.ParseANSI(view, buf)

    // Apply selection highlighting
    sel := cellbuf.Rectangle{
        MinX: m.Selection.StartCol,
        MinY: m.Selection.StartLine,
        MaxX: m.Selection.EndCol,
        MaxY: m.Selection.EndLine + 1, // Exclusive
    }

    fg := lipgloss.Color("#FFFFFF")
    bg := lipgloss.Color("#4444AA")
    buf.ApplySelection(sel, fg, bg)

    // Render back to ANSI
    return buf.Render()
}
```

---

## 9. Word & Paragraph Detection

### 9.1 Word Boundary Detection

```go
func (m *MessageList) findWordBoundaries(col, line int) (startCol, endCol int) {
    // Translate viewport to content coordinates
    contentLine := m.viewportToContentLine(line)

    if contentLine < 0 || contentLine >= len(m.lines) {
        return 0, 0
    }

    currentLine := ansi.Strip(m.lines[contentLine])
    gr := uniseg.NewGraphemes(currentLine)

    startCol = -1
    upTo := col

    for gr.Next() {
        if gr.IsWordBoundary() && upTo > 0 {
            startCol = col - upTo + 1
        } else if gr.IsWordBoundary() && upTo < 0 {
            endCol = col - upTo + 1
            break
        }
        if upTo == 0 && gr.Str() == " " {
            return 0, 0  // Clicked on whitespace
        }
        upTo--
    }

    if startCol == -1 {
        return 0, 0
    }
    return startCol, endCol
}
```

### 9.2 Paragraph Detection

```go
func (m *MessageList) findParagraphBoundaries(line int) (startLine, endLine int, found bool) {
    contentLine := m.viewportToContentLine(line)

    if strings.TrimSpace(m.getCleanLine(contentLine)) == "" {
        return 0, 0, false
    }

    // Search backwards for paragraph start
    startLine = contentLine
    for startLine > 0 && strings.TrimSpace(m.getCleanLine(startLine-1)) != "" {
        startLine--
    }

    // Search forwards for paragraph end
    endLine = contentLine
    for endLine < len(m.lines)-1 && strings.TrimSpace(m.getCleanLine(endLine+1)) != "" {
        endLine++
    }

    // Convert back to viewport coordinates
    return m.contentToViewportLine(startLine), m.contentToViewportLine(endLine), true
}
```

---

## 10. Performance Optimizations Summary

### Memory Optimizations

| Technique | Impact |
|-----------|--------|
| Pre-allocated newline buffer | Eliminates per-gap allocations |
| StringBuilder pre-sizing | Single allocation for render |
| Line offset table | No strings.Split() needed |
| View caching | Skip re-render when unchanged |
| Item render cache | Skip re-render unchanged items |
| Cell buffer reuse | Cell modification in-place |

### CPU Optimizations

| Technique | Impact |
|-----------|--------|
| O(1) line access | Direct string slice |
| Cache dirty flag | Skip unnecessary work |
| Two-pass rendering | Collect then build once |
| Early exit on scroll | Skip if no change |
| Selection bounds pre-compute | One pass for bounds |

---

## 11. Implementation Plan

### Phase 1: Line Offset System - COMPLETED
- [x] Add `lineOffsets []int` to MessageList
- [x] Implement `setContent()` that builds line offset table
- [x] Implement `getLines(start, end)` using offsets
- [x] Implement `getLine(index)` for single line access

### Phase 2: View Caching - COMPLETED
- [x] Add `cachedView`, `cachedViewOffset`, `cachedViewDirty`
- [x] Implement cache invalidation triggers
- [x] Add fast path in View() for cached return

### Phase 3: Cell Buffer System - COMPLETED
- [x] Create `internal/tui/cellbuf/` package
- [x] Implement `Cell` struct with Style support
- [x] Implement `Buffer` with `CellAt`, `SetCell`
- [x] Implement `StyledString.Draw()` for string→buffer (ANSI parsing)
- [x] Implement `Buffer.Render()` for buffer→string
- [x] Implement `Buffer.ApplySelection()` for highlighting
- [x] Implement `Rectangle` geometry types

### Phase 4: Selection Enhancement - COMPLETED
- [x] Add multi-click detection (double=word, triple=paragraph)
- [x] Add `lastClickTime`, `lastClickX`, `lastClickY`, `clickCount` tracking
- [x] Implement `SelectWord()` and `SelectParagraph()` methods
- [x] Implement `findWordBoundaries()` with uniseg
- [x] Implement `findParagraphBoundaries()`

### Phase 5: Integration - COMPLETED
- [x] Integrate cell buffer into MessageList via `renderWithCellbufSelection()`
- [x] Update View() to use cellbuf for selection rendering
- [x] Add special character filtering (SelectionIgnoreIcons with sync.Once cache)
- [x] Implement selection-scroll synchronization (scroll before updating selection end)
- [x] Update GetSelectedText() to use cellbuf with icon filtering
- [ ] Performance testing and optimization

---

## Implementation Summary

### Files Created
- `internal/tui/cellbuf/cell.go` - Cell type with Style
- `internal/tui/cellbuf/buffer.go` - Buffer with CellAt/SetCell
- `internal/tui/cellbuf/rectangle.go` - Geometry types
- `internal/tui/cellbuf/parser.go` - ANSI to cellbuf parser
- `internal/tui/cellbuf/render.go` - Buffer to ANSI renderer
- `internal/tui/cellbuf/selection.go` - Selection highlighting

### Files Modified
- `internal/chat/messagelist.go` - Added:
  - Multi-click detection (doubleClickThreshold, clickTolerance)
  - SelectWord(), SelectParagraph() methods
  - findWordBoundaries(), findParagraphBoundaries() helpers
  - renderWithCellbufSelection() for cell-level highlighting
  - SelectionIgnoreIcons list with sync.Once cached map
  - getSelectionIgnoreMap() for O(1) icon lookup
  - Updated GetSelectedText() to use cellbuf with icon filtering
  - Fixed handleMouseMotion() to scroll before updating selection (Crush technique)
