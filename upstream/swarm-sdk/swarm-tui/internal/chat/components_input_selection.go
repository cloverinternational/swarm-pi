package chat

import (
	"strings"
	"unicode/utf8"

	"charm.land/lipgloss/v2"
)

// ============================================================================
// MOUSE TEXT SELECTION for SimpleInput
// ============================================================================
//
// The input box stores selection as a pair of BYTE offsets into i.value:
//
//	selAnchor — the fixed end (where the drag / shift-click started)
//	selFocus  — the moving end (follows the mouse / cursor)
//
// The ordered range [min, max) is the selected text. Storing byte offsets
// (rather than screen coordinates) means the selection is robust to the
// input's own word-wrapping and horizontal layout — exactly like the message
// viewport's raw-coordinate selection model in messagelist_selection.go.
//
// All of this is intentionally isolated in its own file so the very common
// "no selection" render path in View() stays byte-for-byte unchanged: the
// highlight is only applied when HasSelection() reports a non-empty range.

// SetInputOrigin records the top-left screen cell of the input's TEXT area
// (i.e. the first cell AFTER the "> " prompt on the first row). Mouse
// coordinates are mapped relative to this origin in ScreenToIndex.
func (i *SimpleInput) SetInputOrigin(x, y int) {
	i.originX = x
	i.originY = y
}

// HasSelection reports whether there is a non-empty selection to act on.
func (i *SimpleInput) HasSelection() bool {
	return i.selActive && i.selAnchor != i.selFocus
}

// IsSelecting reports whether the user is mid-drag.
func (i *SimpleInput) IsSelecting() bool { return i.selecting }

// SelectionRange returns the ordered [start, end) byte offsets of the current
// selection, clamped to the current value length.
func (i *SimpleInput) SelectionRange() (int, int) {
	a, b := i.selAnchor, i.selFocus
	if a > b {
		a, b = b, a
	}
	a = clamp(a, 0, len(i.value))
	b = clamp(b, 0, len(i.value))
	return a, b
}

// SelectedText returns the currently selected substring (empty if none).
func (i *SimpleInput) SelectedText() string {
	if !i.HasSelection() {
		return ""
	}
	a, b := i.SelectionRange()
	return i.value[a:b]
}

// StartSelection begins a new selection anchored at byte offset pos and moves
// the text cursor there. Used on a left mouse press.
func (i *SimpleInput) StartSelection(pos int) {
	pos = clamp(pos, 0, len(i.value))
	i.selAnchor = pos
	i.selFocus = pos
	i.selActive = true
	i.selecting = true
	i.cursor = pos
}

// UpdateSelectionEnd moves the focus end of the selection to pos (mouse drag).
func (i *SimpleInput) UpdateSelectionEnd(pos int) {
	if !i.selecting {
		return
	}
	pos = clamp(pos, 0, len(i.value))
	i.selFocus = pos
	i.cursor = pos
}

// ExtendSelection extends an existing selection to pos without changing the
// anchor (shift+click). If no selection exists yet, it anchors at the current
// cursor first.
func (i *SimpleInput) ExtendSelection(pos int) {
	pos = clamp(pos, 0, len(i.value))
	if !i.selActive {
		i.selAnchor = i.cursor
	}
	i.selFocus = pos
	i.selActive = true
	i.selecting = false
	i.cursor = pos
}

// EndSelection freezes the selection on mouse release.
func (i *SimpleInput) EndSelection() {
	i.selecting = false
	if i.selAnchor == i.selFocus {
		// A click with no drag is not a selection — just a cursor placement.
		i.selActive = false
	}
}

// ClearSelection removes any active selection. Safe to call unconditionally;
// it is invoked from every text mutation and cursor-movement key.
func (i *SimpleInput) ClearSelection() {
	i.selActive = false
	i.selecting = false
	i.selAnchor = 0
	i.selFocus = 0
}

// SelectAll selects the entire value (Ctrl+A style "select all" when bound).
func (i *SimpleInput) SelectAll() {
	if len(i.value) == 0 {
		return
	}
	i.selAnchor = 0
	i.selFocus = len(i.value)
	i.selActive = true
	i.selecting = false
	i.cursor = len(i.value)
}

// SelectWordAt selects the word surrounding byte offset pos (double-click).
func (i *SimpleInput) SelectWordAt(pos int) {
	pos = clamp(pos, 0, len(i.value))
	if len(i.value) == 0 {
		return
	}
	// wordBoundaryRight from the previous boundary gives the word end; pairing
	// wordBoundaryLeft(end) gives a stable word start. This reuses the exact
	// word semantics already used by Ctrl+Left / Ctrl+Right navigation.
	end := wordBoundaryRight(i.value, pos)
	start := wordBoundaryLeft(i.value, end)
	if start == end {
		// pos landed on trailing separators — fall back to a single char.
		start = clamp(pos, 0, len(i.value))
		end = clamp(pos+1, 0, len(i.value))
	}
	i.selAnchor = start
	i.selFocus = end
	i.selActive = true
	i.selecting = false
	i.cursor = end
}

// SelectLineAt selects the whole logical line (between explicit newlines)
// containing byte offset pos (triple-click).
func (i *SimpleInput) SelectLineAt(pos int) {
	pos = clamp(pos, 0, len(i.value))
	start := strings.LastIndexByte(i.value[:pos], '\n') + 1 // 0 if none
	rel := strings.IndexByte(i.value[pos:], '\n')
	end := len(i.value)
	if rel >= 0 {
		end = pos + rel
	}
	i.selAnchor = start
	i.selFocus = end
	i.selActive = true
	i.selecting = false
	i.cursor = end
}

// DeleteSelection removes the selected text (used before typing over a
// selection, or on Backspace/Delete with an active selection). Returns true
// if anything was deleted.
func (i *SimpleInput) DeleteSelection() bool {
	if !i.HasSelection() {
		return false
	}
	a, b := i.SelectionRange()
	i.pushUndo()
	i.value = i.value[:a] + i.value[b:]
	i.cursor = a
	i.ClearSelection()
	return true
}

// CopySelection copies the current selection to the system clipboard. Returns
// true if there was a selection to copy.
func (i *SimpleInput) CopySelection() bool {
	if !i.HasSelection() {
		return false
	}
	writeClipboard(i.SelectedText())
	return true
}

// ScreenToIndex maps absolute screen coordinates to a byte offset in i.value.
//
// It accounts for:
//   - the input text origin (originX/originY set by the layout),
//   - the "> " prompt on the first row and the 2-space indent on wrapped /
//     continuation rows (which the parent adds in app_chat_render.go),
//   - explicit newlines in the value.
//
// The mapping is exact for the overwhelmingly common case of a single visual
// row and for multi-row values split on explicit '\n'. For values that the
// renderer soft-wraps (word wrap), it resolves to the nearest offset on the
// targeted row, which is good enough for click-to-place and drag-select.
func (i *SimpleInput) ScreenToIndex(screenX, screenY int) (int, bool) {
	row := screenY - i.originY
	if row < 0 {
		return 0, false
	}
	// Column within the text area. The first row's text starts at originX
	// (parent already accounted for the "> " prefix when setting originX).
	col := screenX - i.originX
	if col < 0 {
		col = 0
	}

	// Split the value into the same logical rows the renderer shows. We map on
	// the visible wrapped rows so vertical clicks land on the right line.
	maxWidth := i.width - 6
	if maxWidth < 20 {
		maxWidth = 20
	}
	rows := i.visibleRowsForMapping(maxWidth)
	if len(rows) == 0 {
		return 0, false
	}
	if row >= len(rows) {
		// Clicked below the text — go to end.
		return len(i.value), true
	}

	target := rows[row]
	// Walk runes on the target row until we reach the clicked column.
	//
	// Advance by the bytes each rune OCCUPIES, not by len(string(r)): for
	// invalid UTF-8 the range loop yields utf8.RuneError while consuming a
	// single byte, yet that rune re-encodes to three, so the offset would run
	// past the end of the value.
	off := target.startByte
	consumed := 0
	for rest := target.text; rest != "" && consumed < col; {
		r, size := utf8.DecodeRuneInString(rest)
		consumed += lipgloss.Width(string(r))
		off += size
		rest = rest[size:]
	}
	return clamp(off, 0, len(i.value)), true
}

// inputRow describes one visible row of the input and where it begins in the
// underlying value (byte offset), used only for coordinate mapping.
type inputRow struct {
	text      string
	startByte int
}

// visibleRowsForMapping reproduces the renderer's row breakdown WITHOUT the
// inlined cursor glyph, tracking each row's starting byte offset in i.value.
// It mirrors wordWrapText's behavior (explicit newlines, then width wrap) but
// preserves byte offsets so screen→index mapping is possible.
func (i *SimpleInput) visibleRowsForMapping(width int) []inputRow {
	var rows []inputRow
	if i.value == "" {
		return []inputRow{{text: "", startByte: 0}}
	}
	byteOff := 0
	paragraphs := strings.Split(i.value, "\n")
	for pIdx, para := range paragraphs {
		if para == "" {
			rows = append(rows, inputRow{text: "", startByte: byteOff})
		} else {
			// Width-wrap this paragraph by runes while tracking byte offsets.
			start := byteOff
			lineWidth := 0
			lineStart := byteOff
			cur := byteOff
			// `idx` is the true byte offset of r within para. Deriving the
			// step from len(string(r)) instead would over-advance on invalid
			// UTF-8 (range yields utf8.RuneError after consuming one byte,
			// but that rune encodes as three), pushing cur past the end of
			// i.value and panicking in the slice below — on the View path,
			// which takes the whole TUI down. Pasted text is not guaranteed
			// to be valid UTF-8.
			for idx, r := range para {
				cur = byteOff + idx
				rw := lipgloss.Width(string(r))
				if width > 0 && lineWidth+rw > width && cur > lineStart {
					rows = append(rows, inputRow{text: i.value[lineStart:cur], startByte: lineStart})
					lineStart = cur
					lineWidth = 0
				}
				lineWidth += rw
			}
			cur = byteOff + len(para)
			rows = append(rows, inputRow{text: i.value[lineStart:cur], startByte: lineStart})
			_ = start
		}
		byteOff += len(para)
		// Account for the '\n' separator (present between paragraphs).
		if pIdx < len(paragraphs)-1 {
			byteOff++ // the '\n'
		}
	}
	return rows
}

// renderWithSelection renders the value with the selected byte range styled in
// reverse video. It is only called from View() when HasSelection() is true, so
// it never affects the default path. It returns the full multi-row string
// (with inlined cursor) ready for the same wrapping the parent applies.
func (i *SimpleInput) renderWithSelection() string {
	a, b := i.SelectionRange()
	if a == b {
		return i.value
	}
	// Reverse video makes the selection visible against any theme without
	// needing a specific palette colour, matching common terminal selection UX.
	selStyle := lipgloss.NewStyle().Reverse(true)

	before := i.value[:a]
	mid := i.value[a:b]
	after := i.value[b:]

	// Highlight per-line so an explicit newline inside the selection is not
	// swallowed by the background style (which would paint past line ends).
	var sb strings.Builder
	sb.WriteString(before)
	segs := strings.Split(mid, "\n")
	for idx, seg := range segs {
		if idx > 0 {
			sb.WriteString("\n")
		}
		if seg != "" {
			sb.WriteString(selStyle.Render(seg))
		}
	}
	sb.WriteString(after)
	return sb.String()
}
