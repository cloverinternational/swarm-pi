package chat

import (
	"image/color"

	"charm.land/lipgloss/v2"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/tui/cellbuf"
)

// borderIgnoreChars are box-drawing / separator glyphs used purely as UI
// chrome (message borders, dividers) rather than actual message content.
// They are skipped both when painting the on-screen selection highlight and
// when extracting plain text for the clipboard, so copied text never
// contains stray border characters. Shared by applySelectionHighlight,
// applyWrappedSelectionHighlight, and GetSelectedText so all three treat
// "what's highlighted" and "what's copied" identically.
var borderIgnoreChars = map[string]struct{}{
	"│": {}, "┌": {}, "┐": {}, "└": {}, "┘": {},
	"├": {}, "┤": {}, "─": {}, "═": {}, "║": {},
}

// ============================================================================
// SELECTION STATE MANAGEMENT (raw content coordinates)
// ============================================================================
//
// Selection tracks positions within the raw content (m.lines array):
// StartLine/EndLine are raw line indices (0-based, into m.lines)
// StartCol/EndCol are byte offsets within those raw lines
//
// This means selection SURVIVES scrolling — the same text stays selected
// regardless of YOffset. The render path maps raw coordinates to visible
// coordinates using the wrapping-aware line mapping.

// StartSelection begins a new text selection at the given raw line and column.
func (m *MessageList) StartSelection(rawLine, rawCol int) {
	m.Selection = Selection{
		StartLine: clamp(rawLine, 0, max(0, len(m.lines)-1)),
		StartCol:  clamp(rawCol, 0, m.safeLineLen(rawLine)),
		EndLine:   clamp(rawLine, 0, max(0, len(m.lines)-1)),
		EndCol:    clamp(rawCol, 0, m.safeLineLen(rawLine)),
		Active:    true,
		Selecting: true,
	}
	m.cachedDirty = true
}

// UpdateSelectionEnd updates the selection end point during a drag.
func (m *MessageList) UpdateSelectionEnd(rawLine, rawCol int) {
	if !m.Selection.Selecting {
		return
	}
	m.Selection.EndLine = clamp(rawLine, 0, max(0, len(m.lines)-1))
	m.Selection.EndCol = clamp(rawCol, 0, m.safeLineLen(m.Selection.EndLine))
	m.cachedDirty = true
}

// ExtendSelection extends the existing selection to a new anchor point.
// Used for shift+click to extend selection from the existing start.
func (m *MessageList) ExtendSelection(rawLine, rawCol int) {
	if !m.Selection.Active {
		// No existing selection — start a new one
		m.StartSelection(rawLine, rawCol)
		m.EndSelection()
		return
	}

	m.Selection.EndLine = clamp(rawLine, 0, max(0, len(m.lines)-1))
	m.Selection.EndCol = clamp(rawCol, 0, m.safeLineLen(m.Selection.EndLine))
	m.Selection.Selecting = false
	m.cachedDirty = true
}

// EndSelection freezes the selection on mouse button release.
func (m *MessageList) EndSelection() {
	if !m.Selection.Selecting {
		return
	}
	m.Selection.Selecting = false
	m.cachedDirty = true
}

// ClearSelection removes the active selection.
func (m *MessageList) ClearSelection() {
	m.Selection = Selection{}
	m.cachedDirty = true
}

// selectionSurvivesContentChange reports whether an active selection still
// points at the same text after the raw content is replaced with newLines.
// Called from SetContent to decide whether an in-progress content update
// (e.g. a streaming response growing at the bottom of the transcript)
// should clear the current selection.
//
// Only the portion of the transcript the selection actually spans needs to
// match between oldLines and newLines — content appended afterward (the
// normal case while a response streams in below an already-selected
// message) does not invalidate anything the selection points at. If any
// line the selection covers moved or changed, the selection no longer means
// what the user selected, so it must be cleared.
func selectionSurvivesContentChange(oldLines, newLines []string, sel Selection) bool {
	if !sel.Active || sel.IsEmpty() {
		return false
	}

	n := sel.Normalize()
	if n.StartLine < 0 || n.EndLine < 0 {
		return false
	}
	if n.EndLine >= len(oldLines) || n.EndLine >= len(newLines) {
		return false
	}

	// Every fully-covered line strictly before the last selected line must be
	// byte-for-byte identical: if that text moved or changed, the selection
	// no longer points at what the user actually selected.
	for i := n.StartLine; i < n.EndLine; i++ {
		if oldLines[i] != newLines[i] {
			return false
		}
	}

	// The line the selection ends on only needs to match through the
	// selection's end column — streaming may still be appending more text
	// after that point on the very line the user selected into.
	oldEndLine := oldLines[n.EndLine]
	newEndLine := newLines[n.EndLine]
	endCol := n.EndCol
	if endCol > len(oldEndLine) || endCol > len(newEndLine) {
		return false
	}
	if oldEndLine[:endCol] != newEndLine[:endCol] {
		return false
	}

	// Single-line selection: also make sure the start column is still in
	// range on both sides (the shared prefix check above already covers the
	// actual selected text since StartCol <= EndCol on the same line).
	if n.StartLine == n.EndLine {
		if n.StartCol > len(oldEndLine) || n.StartCol > len(newEndLine) {
			return false
		}
	}

	return true
}

// IsSelectionActive returns true if there is a non-empty, frozen selection.
func (m *MessageList) IsSelectionActive() bool {
	return m.Selection.Active && !m.Selection.Selecting && !m.Selection.IsEmpty()
}

// IsSelecting returns true if the user is actively dragging the mouse.
func (m *MessageList) IsSelecting() bool {
	return m.Selection.Selecting
}

// safeLineLen returns the byte length of a raw line, safe for out-of-bounds.
func (m *MessageList) safeLineLen(line int) int {
	if line < 0 || line >= len(m.lines) {
		return 0
	}
	return len(m.lines[line])
}

// ============================================================================
// COORDINATE MAPPING
// ============================================================================

// ScreenToContent maps screen (mouse) coordinates to raw content coordinates.
// Returns the raw line index, approximate byte offset, and ok.
func (m *MessageList) ScreenToContent(screenX, screenY int) (rawLine, rawCol int, ok bool) {
	contentX := screenX - m.OriginX
	contentY := screenY - m.OriginY

	w := m.Width - m.Style.GetHorizontalFrameSize()
	if w <= 0 {
		w = m.Width
	}
	h := m.Height - m.Style.GetVerticalFrameSize()
	if h <= 0 {
		return 0, 0, false
	}

	if contentX < 0 || contentX >= w || contentY < 0 || contentY >= h {
		return 0, 0, false
	}

	// Map visible line to raw line using wrapping-aware mapping. This MUST
	// mirror whatever the render path actually put on screen, otherwise clicks
	// land on the wrong line.
	var segment int
	rawLine, segment, ok = m.visibleToRaw(contentY, w, h)
	if !ok {
		return 0, 0, false
	}

	// Approximate byte offset: column + (segment * width), clamped to line length
	rawCol = clamp(contentX+segment*w, 0, m.safeLineLen(rawLine))
	return rawLine, rawCol, true
}

// ScreenToContentClamped is like ScreenToContent but, instead of rejecting
// clicks outside the content rectangle, it CLAMPS the coordinates into the
// nearest on-screen content cell. This is what a drag-to-extend needs: when the
// user drags the mouse DOWN past the last transcript row (into the separator /
// input box) to grab the final lines, we must still extend the selection to the
// last visible line rather than freezing it several rows short (the reported
// "can't highlight the last 4-5 lines" bug). Returns ok=false only when there
// is genuinely nothing to select (empty viewport).
func (m *MessageList) ScreenToContentClamped(screenX, screenY int) (rawLine, rawCol int, ok bool) {
	w := m.Width - m.Style.GetHorizontalFrameSize()
	if w <= 0 {
		w = m.Width
	}
	h := m.Height - m.Style.GetVerticalFrameSize()
	if h <= 0 || len(m.lines) == 0 {
		return 0, 0, false
	}

	contentX := clamp(screenX-m.OriginX, 0, w-1)
	contentY := clamp(screenY-m.OriginY, 0, h-1)

	// Walk up from the clamped row to the last row that actually maps to
	// content. Padded/empty rows below the last content line (short
	// conversations) map to ok=false; clamping to them would select nothing, so
	// we snap to the last real content row instead.
	for y := contentY; y >= 0; y-- {
		if rl, seg, mok := m.visibleToRaw(y, w, h); mok {
			rawCol = clamp(contentX+seg*w, 0, m.safeLineLen(rl))
			return rl, rawCol, true
		}
	}
	return 0, 0, false
}

// prewrapActive reports whether the pre-wrapped render path is the one currently
// producing frames for the given content width. When true, the viewport renders
// preWrappedLines[YOffset : YOffset+h] (renderFromPreWrappedLines), which means
// YOffset and each screen row index refer to WRAPPED (visual) lines, not raw
// m.lines indices. Mouse mapping must use the same space.
func (m *MessageList) prewrapActive(width int) bool {
	return len(m.preWrappedLines) > 0 &&
		m.preWrappedHash == m.lastContentHash &&
		m.preWrappedWidth == width &&
		len(m.preWrappedMapping) == len(m.lines)+1
}

// visibleToRaw maps a visible line index to the corresponding raw line
// and segment index within that raw line.
func (m *MessageList) visibleToRaw(visLine, width, height int) (rawLine, segment int, ok bool) {
	if visLine < 0 || visLine >= height {
		return 0, 0, false
	}

	// Pre-wrapped path: the screen shows preWrappedLines[YOffset : YOffset+h],
	// so the wrapped-line index under the cursor is simply YOffset+visLine.
	// Convert that wrapped index back to a raw line + segment via the mapping
	// captured alongside preWrappedLines. This keeps hit-testing exactly aligned
	// with the rendered frame, which is where the "wrong point" bug came from:
	// the old code treated YOffset as a RAW index into m.lines and re-wrapped
	// locally, diverging from the rendered wrapped frame on any above-fold wrap.
	if m.prewrapActive(width) {
		wrappedIdx := m.YOffset + visLine
		if wrappedIdx < 0 || wrappedIdx >= len(m.preWrappedLines) {
			return 0, 0, false
		}
		raw, seg, found := rawFromWrappedIndex(m.preWrappedMapping, wrappedIdx)
		if !found {
			return 0, 0, false
		}
		return raw, seg, true
	}

	// Fallback (no valid pre-wrap yet): mirror the synchronous render path
	// wrapAndPadContentAnchored(m.lines, YOffset, h, w, AtBottom()). That path
	// wraps m.lines[YOffset:YOffset+h] and, when the wrapped result overflows
	// the viewport height AND the viewport is anchored to the bottom, keeps the
	// LAST h visual lines (dropping wrapped lines from the TOP). We must apply
	// the same drop so screen rows line up with content.
	top := m.YOffset
	if top >= len(m.lines) {
		return 0, 0, false
	}
	bottom := m.YOffset + height
	if bottom > len(m.lines) {
		bottom = len(m.lines)
	}

	visibleLines := m.lines[top:bottom]
	wrapped, mapping := wrapLinesToWidthWithMapping(visibleLines, width)

	// Account for bottom-anchoring: when more wrapped lines were produced than
	// fit on screen and the viewport is pinned to the bottom, the render drops
	// (len(wrapped)-height) lines from the top. The visible row visLine then
	// corresponds to wrapped index visLine+drop.
	drop := 0
	if len(wrapped) > height && m.AtBottom() {
		drop = len(wrapped) - height
	}
	wrappedIdx := visLine + drop
	if wrappedIdx < 0 || wrappedIdx >= len(wrapped) {
		return 0, 0, false
	}

	for i := 0; i < len(mapping)-1; i++ {
		if wrappedIdx >= mapping[i] && wrappedIdx < mapping[i+1] {
			return top + i, wrappedIdx - mapping[i], true
		}
	}
	return 0, 0, false
}

// rawFromWrappedIndex converts a global wrapped (visual) line index into the raw
// line index that produced it and the segment (continuation index) within that
// raw line, using a cumulative wrapped-line-count mapping where
// mapping[i] = wrapped lines before raw line i and len(mapping) == rawLines+1.
// Uses binary search since mapping is monotonically non-decreasing.
func rawFromWrappedIndex(mapping []int, wrappedIdx int) (rawLine, segment int, ok bool) {
	n := len(mapping) - 1
	if n <= 0 || wrappedIdx < 0 || wrappedIdx >= mapping[n] {
		return 0, 0, false
	}
	// Find the largest i such that mapping[i] <= wrappedIdx.
	lo, hi := 0, n // answer in [0, n-1]
	for lo < hi {
		mid := (lo + hi + 1) / 2
		if mapping[mid] <= wrappedIdx {
			lo = mid
		} else {
			hi = mid - 1
		}
	}
	return lo, wrappedIdx - mapping[lo], true
}

// wrappedFromRawIndex is the inverse of rawFromWrappedIndex: it converts a raw
// (m.lines) line index plus an in-line segment into the corresponding global
// wrapped (preWrappedLines) index, using the same cumulative mapping where
// mapping[i] = wrapped lines before raw line i and len(mapping) == rawLines+1.
//
// This MUST be used at every site that computes a scroll offset in raw space
// (e.g. from messageLinePositions, which are always raw-space) and then hands
// that offset to MessageList.SetYOffset/YOffset while the pre-wrapped renderer
// is active — SetYOffset always clamps against and is interpreted in whatever
// space prewrapActive() reports (wrapped when active, raw otherwise). Passing
// a raw-space number straight into SetYOffset while prewrap is active is the
// exact bug class that caused the scroll regression: restoreScrollAnchor
// computed newYOffset from raw messageLinePositions but called SetYOffset
// directly without this conversion, while saveScrollAnchor (the read side)
// already converted wrapped->raw via rawFromWrappedIndex. The two must be
// symmetric round-trip conversions or the anchor drifts by however many
// extra visual lines wrapping added, producing the raw=1570 wrapped=1809
// mismatch observed live.
func wrappedFromRawIndex(mapping []int, rawLine, segment int) (wrappedIdx int, ok bool) {
	n := len(mapping) - 1
	if n <= 0 {
		return 0, false
	}
	if rawLine < 0 {
		rawLine = 0
	}
	if rawLine >= n {
		// Clamp to the last raw line's first segment; callers scrolled past
		// the end of content, which SetYOffset will clamp further anyway.
		return mapping[n], true
	}
	segCount := mapping[rawLine+1] - mapping[rawLine]
	if segment < 0 {
		segment = 0
	}
	if segCount > 0 && segment >= segCount {
		segment = segCount - 1
	}
	return mapping[rawLine] + segment, true
}

// ============================================================================
// TEXT EXTRACTION
// ============================================================================

// GetSelectedText returns the plain, unstyled text content of the current
// selection.
//
// m.lines holds the FULLY RENDERED transcript — lipgloss/ANSI styling,
// colors, and box-drawing message borders included — because that's what
// the screen paints from. The previous implementation byte-sliced these raw,
// styled lines directly, which meant every copy leaked ANSI escape sequences
// and border glyphs into the clipboard as garbage characters (reported
// live: "when you copy it adds garbage"). Selection.StartCol/EndCol are
// visible CELL columns — the same coordinate space ScreenToContent produces
// and applySelectionHighlight/applyWrappedSelectionHighlight already consume
// — so extraction goes through the same cellbuf parsing those two render
// paths use: Draw() resolves ANSI escapes into per-cell plain glyphs, then
// cellbuf.Buffer.GetSelectedText walks the selected cell rectangle to
// produce clean text. This keeps "what's highlighted on screen" and "what
// gets copied" identical.
func (m *MessageList) GetSelectedText() string {
	if !m.Selection.Active || m.Selection.IsEmpty() {
		return ""
	}

	sel := m.Selection.Normalize()

	endLine := sel.EndLine
	if endLine >= len(m.lines) {
		endLine = len(m.lines) - 1
	}
	if endLine < sel.StartLine || endLine < 0 {
		return ""
	}
	selectedLines := m.lines[sel.StartLine : endLine+1]

	// Size the scratch buffer to the widest selected raw line so Draw()
	// never clips visible content.
	width := 0
	for _, line := range selectedLines {
		if w := lipgloss.Width(line); w > width {
			width = w
		}
	}
	if width <= 0 {
		return ""
	}

	buf := cellbuf.NewBuffer(width, len(selectedLines))
	for row, line := range selectedLines {
		cellbuf.NewStyledString(line).Draw(buf, cellbuf.Rect(0, row, width, row+1))
	}

	startCol := clamp(sel.StartCol, 0, width)
	endCol := clamp(sel.EndCol, 0, width)
	rect := cellbuf.Rect(startCol, 0, endCol, len(selectedLines))
	return buf.GetSelectedText(rect, borderIgnoreChars)
}

// ============================================================================
// RENDERING — wrapping-aware selection highlighting
// ============================================================================

// applySelectionHighlight applies per-cell selection highlighting using cellbuf.
// It converts raw content coordinates to visible coordinates for the current viewport.
// applySelectionHighlight applies selection styling to a raw-indexed frame.
// It is used only by the synchronous fallback render path (no valid pre-wrap),
// where the frame is produced by wrapAndPadContentAnchored(m.lines, ...).
func applySelectionHighlight(content string, sel Selection, m *MessageList) string {
	if !sel.Active || sel.IsEmpty() {
		return content
	}

	w := m.Width - m.Style.GetHorizontalFrameSize()
	if w <= 0 {
		w = m.Width
	}
	h := m.Height - m.Style.GetVerticalFrameSize()
	if h <= 0 {
		return content
	}

	// Parse styled content into cell buffer
	buf := cellbuf.NewBuffer(w, h)
	ss := cellbuf.NewStyledString(content)
	ss.Draw(buf, cellbuf.Rect(0, 0, w, h))

	// Get visible range and mapping from raw lines → visible lines
	top := m.YOffset
	if top >= len(m.lines) {
		return content
	}
	bottom := m.YOffset + h
	if bottom > len(m.lines) {
		bottom = len(m.lines)
	}
	visibleLines := m.lines[top:bottom]
	wrapped, mapping := wrapLinesToWidthWithMapping(visibleLines, w)

	// Account for bottom-anchoring: wrapAndPadContentAnchored keeps the LAST
	// `h` wrapped rows (dropping `drop` rows from the TOP) when the wrapped
	// window overflows the viewport and the viewport is pinned to the bottom.
	// The render path and the hit-test path (visibleToRaw) both apply this
	// drop, so the highlight MUST apply it too — otherwise the selected text
	// is drawn `drop` rows away from where the user clicked (the reported
	// "selection lands on the wrong point" bug). Wrapped index W therefore maps
	// to screen row W-drop.
	drop := 0
	if len(wrapped) > h && m.AtBottom() {
		drop = len(wrapped) - h
	}

	selNorm := sel.Normalize()

	// For each visible line, determine if it comes from a selected raw line
	// and build highlight rectangles per-segment
	var rects []cellbuf.Rectangle

	for i := 0; i < len(mapping)-1; i++ {
		rawIdx := top + i
		if rawIdx < selNorm.StartLine || rawIdx > selNorm.EndLine {
			continue
		}

		// Screen rows for this raw line after applying the bottom-anchor drop.
		segStart := mapping[i] - drop     // first visible screen row
		segEnd := mapping[i+1] - 1 - drop // last visible screen row

		// Clamp to viewport height; skip rows scrolled above the fold.
		if segEnd < 0 || segStart >= h {
			continue
		}
		if segEnd >= h {
			segEnd = h - 1
		}
		if segStart > segEnd {
			continue
		}

		// Determine column range for this raw line
		startCol, endCol := 0, w
		if rawIdx == selNorm.StartLine {
			startCol = selNorm.StartCol
		}
		if rawIdx == selNorm.EndLine {
			endCol = selNorm.EndCol
		}

		// For wrapped lines, approximate column range per segment.
		// Each segment is roughly `w` chars wide. Compute which part
		// of the column range falls into each visible segment.
		numSegs := mapping[i+1] - mapping[i]
		for seg := range numSegs {
			visLine := mapping[i] + seg - drop
			if visLine < 0 {
				continue
			}
			if visLine >= h {
				break
			}

			// This segment covers approximate byte range [seg*w, (seg+1)*w)
			segByteStart := seg * w

			segColStart := clamp(startCol-segByteStart, 0, w)
			segColEnd := clamp(endCol-segByteStart, 0, w)
			if segColEnd > w {
				segColEnd = w
			}
			if segColStart >= segColEnd {
				continue
			}

			rects = append(rects, cellbuf.Rect(
				segColStart, visLine,
				segColEnd, visLine+1,
			))
		}
	}

	if len(rects) == 0 {
		return content
	}

	// Apply selection style to all rectangles
	selStyle := cellbuf.SelectionStyle{
		Fg: color.White,
		Bg: color.RGBA{R: 0, G: 100, B: 200, A: 255},
	}

	for _, rect := range rects {
		buf.ApplySelection(rect, selStyle, borderIgnoreChars)
	}

	return buf.Render()
}

// applyWrappedSelectionHighlight highlights a selection on a frame that is
// already sliced in WRAPPED-line space (preWrappedLines[top:top+h]). It is the
// wrapped-space counterpart of applySelectionHighlight and shares the exact
// coordinate model used by ScreenToContent, so what the user clicks is what
// gets highlighted.
//
//	content  the joined, height-padded visible wrapped lines
//	sel      the active selection (raw line + byte column)
//	w, h     content width / height in cells
//	top      global wrapped index of the first visible row (== YOffset)
func (m *MessageList) applyWrappedSelectionHighlight(content string, sel Selection, w, h, top int) string {
	if !sel.Active || sel.IsEmpty() {
		return content
	}
	if len(m.preWrappedMapping) != len(m.lines)+1 {
		return content
	}

	buf := cellbuf.NewBuffer(w, h)
	ss := cellbuf.NewStyledString(content)
	ss.Draw(buf, cellbuf.Rect(0, 0, w, h))

	selNorm := sel.Normalize()

	var rects []cellbuf.Rectangle
	for row := 0; row < h; row++ {
		wrappedIdx := top + row
		if wrappedIdx < 0 || wrappedIdx >= len(m.preWrappedLines) {
			break
		}
		rawIdx, seg, ok := rawFromWrappedIndex(m.preWrappedMapping, wrappedIdx)
		if !ok {
			continue
		}
		if rawIdx < selNorm.StartLine || rawIdx > selNorm.EndLine {
			continue
		}
		// Byte-column range selected on this raw line.
		startCol, endCol := 0, w
		if rawIdx == selNorm.StartLine {
			startCol = selNorm.StartCol
		}
		if rawIdx == selNorm.EndLine {
			endCol = selNorm.EndCol
		}
		// Project the raw byte-column range onto this wrapped segment: each
		// segment is ~w cells wide, so segment `seg` covers [seg*w, (seg+1)*w).
		segByteStart := seg * w
		segColStart := clamp(startCol-segByteStart, 0, w)
		segColEnd := clamp(endCol-segByteStart, 0, w)
		if segColStart >= segColEnd {
			continue
		}
		rects = append(rects, cellbuf.Rect(segColStart, row, segColEnd, row+1))
	}
	if len(rects) == 0 {
		return content
	}

	selStyle := cellbuf.SelectionStyle{
		Fg: color.White,
		Bg: color.RGBA{R: 0, G: 100, B: 200, A: 255},
	}
	for _, rect := range rects {
		buf.ApplySelection(rect, selStyle, borderIgnoreChars)
	}
	return buf.Render()
}

// ============================================================================
// HELPER
// ============================================================================
