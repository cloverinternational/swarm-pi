package chat

import (
	"fmt"
	"strings"
	"testing"
)

// buildPrewrap mirrors what the background prewrapViewportCmd + SetPreWrappedLines
// do, so ScreenToContent exercises the real pre-wrapped hit-testing path.
func buildPrewrap(m *MessageList, width int) {
	lines, hash := m.LinesForPrewrap()
	wrapped, mapping := wrapLinesToWidthWithMapping(lines, width)
	// SetPreWrappedLines only stores when hash matches lastContentHash.
	m.SetPreWrappedLines(wrapped, mapping, width, hash)
}

// TestScreenToContent_WrappedAboveFold is the regression test for the reported
// bug: with a long (wrapping) line above the click target, the mouse mapped to
// the wrong raw line because YOffset was interpreted as a raw index while the
// rendered frame indexed wrapped lines. After the fix, a click on the visual row
// showing raw line 2 must return raw line 2.
func TestScreenToContent_WrappedAboveFold(t *testing.T) {
	const width = 20
	const height = 10

	m := NewMessageList(width, height)
	m.SetOrigin(0, 0)
	m.SetSize(width, height)

	// Raw line 0 is short. Raw line 1 wraps into 3 visual rows at width 20.
	// Raw line 2 is the target. Without the fix, the extra wrapped rows from
	// line 1 shift the mapping and the click lands on the wrong raw line.
	// Word-aware wrapping needs spaces, so use spaced words (not one long word).
	longLine := "aa bb cc dd ee ff gg hh ii jj kk ll" // wraps to 3 rows at width 20
	content := strings.Join([]string{
		"line zero",
		longLine,
		"TARGET line two",
		"line three",
	}, "\n")
	m.SetContent(content)
	m.SetYOffset(0)
	buildPrewrap(m, width)

	if !m.prewrapActive(width) {
		t.Fatalf("pre-wrap path not active; test would not exercise the fix")
	}

	// Confirm the premise: line 1 must actually wrap to >1 visual rows.
	wl, mp := wrapLinesToWidthWithMapping(m.lines, width)
	segs := mp[2] - mp[1]
	if segs < 2 {
		t.Fatalf("test premise broken: line 1 wrapped to %d rows (need >=2); wrapped=%d", segs, len(wl))
	}
	targetRow := mp[2] // first visual row of raw line 2

	// Visual layout at width 20 (YOffset 0):
	//   row 0            -> raw 0 "line zero"
	//   rows 1..segs     -> raw 1 (wrapped)
	//   row targetRow    -> raw 2 "TARGET line two"
	rawLine, _, ok := m.ScreenToContent(0, targetRow)
	if !ok {
		t.Fatalf("ScreenToContent returned ok=false for a visible row")
	}
	if rawLine != 2 {
		t.Fatalf("click on visual row %d mapped to raw line %d, want 2 (wrapped line above fold not accounted for)", targetRow, rawLine)
	}

	// A click on the first wrapped segment of the long line (row 1) -> raw 1 seg 0.
	rawLine, rawCol, ok := m.ScreenToContent(3, 1)
	if !ok || rawLine != 1 {
		t.Fatalf("row 1 mapped to raw %d ok=%v, want raw 1", rawLine, ok)
	}
	if rawCol != 3 {
		t.Fatalf("row1 col: got %d want 3 (segment 0, contentX=3)", rawCol)
	}

	// Second wrapped segment (row 2) -> raw 1 seg 1: col should be contentX + 1*width.
	_, rawCol, ok = m.ScreenToContent(2, 2)
	if !ok {
		t.Fatalf("row 2 not ok")
	}
	if rawCol != 2+width {
		t.Fatalf("row2 col: got %d want %d (segment 1 offset)", rawCol, 2+width)
	}
}

// TestScreenToContent_ScrolledWithOrigin verifies the fix under a non-zero
// YOffset and a non-zero screen origin (header offset), which is the real
// runtime configuration (SetOrigin(2, headerRows)).
func TestScreenToContent_ScrolledWithOrigin(t *testing.T) {
	const width = 16
	const height = 6
	const originX = 2
	const originY = 5

	m := NewMessageList(width, height)
	m.SetOrigin(originX, originY)
	m.SetSize(width, height)

	var b strings.Builder
	for i := 0; i < 30; i++ {
		if i > 0 {
			b.WriteByte('\n')
		}
		b.WriteString("row")
		b.WriteByte(byte('0' + i%10))
	}
	m.SetContent(b.String())
	buildPrewrap(m, width)

	// No wrapping here (short lines), so wrapped index == raw index. Scroll down.
	m.SetYOffset(8)
	buildPrewrap(m, width) // width unchanged; refresh mapping/hash coherency

	// Click screen (originX+1, originY+2): contentY=2 -> wrapped/raw index 8+2=10.
	rawLine, _, ok := m.ScreenToContent(originX+1, originY+2)
	if !ok {
		t.Fatalf("ScreenToContent ok=false")
	}
	if rawLine != 10 {
		t.Fatalf("scrolled click mapped to raw %d, want 10", rawLine)
	}

	// Out-of-bounds clicks (above origin / left of origin) must be rejected.
	if _, _, ok := m.ScreenToContent(originX-1, originY+2); ok {
		t.Fatalf("click left of origin should be rejected")
	}
	if _, _, ok := m.ScreenToContent(originX+1, originY-1); ok {
		t.Fatalf("click above origin should be rejected")
	}
}

// TestScreenToContentClamped_DragPastBottom is the regression test for
// "can't highlight the last 4-5 lines": dragging the mouse BELOW the viewport
// (into the separator / input area) must clamp the selection end to the LAST
// visible content line instead of returning ok=false and freezing short.
func TestScreenToContentClamped_DragPastBottom(t *testing.T) {
	const width = 24
	const height = 8
	const originX = 2
	const originY = 0

	m := NewMessageList(width, height)
	m.SetOrigin(originX, originY)
	m.SetSize(width, height)

	var b strings.Builder
	for i := 0; i < 40; i++ {
		if i > 0 {
			b.WriteByte('\n')
		}
		fmt.Fprintf(&b, "line %02d", i)
	}
	m.SetContent(b.String())
	buildPrewrap(m, width)
	m.GotoBottom()
	buildPrewrap(m, width)

	lastRaw := len(m.lines) - 1

	// Strict mapping rejects a click well below the viewport bottom...
	if _, _, ok := m.ScreenToContent(originX+1, originY+height+5); ok {
		t.Fatalf("strict ScreenToContent should reject a point below the viewport")
	}

	// ...but the clamped drag mapping must snap to the last visible content line.
	rawLine, _, ok := m.ScreenToContentClamped(originX+1, originY+height+5)
	if !ok {
		t.Fatalf("clamped mapping returned ok=false; drag past bottom should clamp")
	}
	if rawLine != lastRaw {
		t.Fatalf("drag past bottom clamped to raw line %d, want last line %d", rawLine, lastRaw)
	}

	// A drag far to the right of the last row should also clamp to that row.
	rawLine, _, ok = m.ScreenToContentClamped(originX+width+50, originY+height+2)
	if !ok || rawLine != lastRaw {
		t.Fatalf("drag past bottom-right clamped to raw %d ok=%v, want %d", rawLine, ok, lastRaw)
	}
}

// TestScreenToContentClamped_ShortConversation ensures that when the transcript
// is shorter than the viewport (padded empty rows at the bottom), a drag into
// the empty padding clamps to the last REAL content line, not an empty row.
func TestScreenToContentClamped_ShortConversation(t *testing.T) {
	const width = 24
	const height = 12
	m := NewMessageList(width, height)
	m.SetOrigin(0, 0)
	m.SetSize(width, height)

	m.SetContent("alpha\nbravo\ncharlie") // 3 lines, viewport height 12
	buildPrewrap(m, width)
	m.SetYOffset(0)

	lastRaw := len(m.lines) - 1 // 2 (charlie)

	// Drag into the empty padded region (row 8, well past charlie at row 2).
	rawLine, _, ok := m.ScreenToContentClamped(3, 8)
	if !ok {
		t.Fatalf("clamped mapping ok=false on short conversation")
	}
	if rawLine != lastRaw {
		t.Fatalf("drag into padding clamped to raw %d, want last real line %d", rawLine, lastRaw)
	}
}
func TestRawFromWrappedIndex(t *testing.T) {
	// 3 raw lines producing 1, 3, 2 wrapped rows -> mapping [0,1,4,6].
	mapping := []int{0, 1, 4, 6}
	cases := []struct {
		wrapped int
		wantRaw int
		wantSeg int
		wantOk  bool
	}{
		{0, 0, 0, true},
		{1, 1, 0, true},
		{2, 1, 1, true},
		{3, 1, 2, true},
		{4, 2, 0, true},
		{5, 2, 1, true},
		{6, 0, 0, false}, // out of range (== total)
		{-1, 0, 0, false},
	}
	for _, c := range cases {
		raw, seg, ok := rawFromWrappedIndex(mapping, c.wrapped)
		if ok != c.wantOk || (ok && (raw != c.wantRaw || seg != c.wantSeg)) {
			t.Fatalf("wrapped=%d -> raw=%d seg=%d ok=%v, want raw=%d seg=%d ok=%v",
				c.wrapped, raw, seg, ok, c.wantRaw, c.wantSeg, c.wantOk)
		}
	}
}
