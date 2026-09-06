package chat

import (
	"strings"
	"testing"
)

// firstHighlightedRow returns the index of the first rendered row that contains
// the selection highlight background SGR (48;2;0;100;200), or -1 if none.
// This matches the SelectionStyle Bg used by applySelectionHighlight /
// applyWrappedSelectionHighlight (RGBA{0,100,200}).
func firstHighlightedRow(rendered string) int {
	rows := strings.Split(rendered, "\n")
	for i, r := range rows {
		if strings.Contains(r, "48;2;0;100;200") {
			return i
		}
	}
	return -1
}

// TestSelectionHighlight_BottomAnchoredDrop is the regression test for the
// reported bug: "mouse selection is not finding the right point on screen."
//
// When the viewport is pinned to the bottom (AtBottom) and the wrapped content
// exceeds the viewport height, the FALLBACK render path
// (wrapAndPadContentAnchored) drops `drop = len(wrapped) - height` wrapped rows
// from the TOP and keeps the last `height` rows. The hit-test path
// (visibleToRaw) correctly adds `drop` back, so a click on screen row Y maps to
// the right raw line. But applySelectionHighlight historically painted the
// highlight at mapping[i]+seg WITHOUT subtracting `drop`, so the highlight
// landed `drop` rows away from the clicked row.
//
// This test forces the fallback path (no pre-wrap built), starts a selection at
// a screen row via the SAME coordinate mapping the mouse handler uses
// (ScreenToContentClamped -> StartSelection), renders, and asserts the
// highlight appears on the clicked screen row.
func TestSelectionHighlight_BottomAnchoredDrop(t *testing.T) {
	const width = 20
	const height = 6
	m := NewMessageList(width, height)
	m.SetOrigin(0, 0)
	m.SetSize(width, height)

	// Build content whose wrapped height exceeds `height` so the bottom-anchor
	// drop is non-zero. Several long (wrapping) lines guarantee overflow.
	long := "aa bb cc dd ee ff gg hh ii jj kk ll" // wraps to 3 rows at width 20
	content := strings.Join([]string{
		long,        // raw 0 -> 3 wrapped rows
		long,        // raw 1 -> 3 wrapped rows
		long,        // raw 2 -> 3 wrapped rows
		"LAST line", // raw 3 -> 1 row (the newest, visible at the bottom)
	}, "\n")
	m.SetContent(content)
	m.GotoBottom()

	// Deliberately DO NOT build the pre-wrap cache: exercise the synchronous
	// fallback (wrapAndPadContentAnchored + applySelectionHighlight), which is
	// what renders during streaming before the async pre-wrap arrives.
	if m.prewrapActive(width) {
		t.Fatalf("pre-wrap unexpectedly active; test must exercise the fallback path")
	}
	if !m.AtBottom() {
		t.Fatalf("viewport should be bottom-anchored for this test")
	}

	// Click the LAST visible screen row (bottom of the viewport), which shows
	// "LAST line". Use the same mapping the mouse handler uses.
	clickRow := height - 1
	rawLine, rawCol, ok := m.ScreenToContentClamped(0, clickRow)
	if !ok {
		t.Fatalf("ScreenToContentClamped ok=false for bottom row")
	}
	m.StartSelection(rawLine, rawCol)
	m.UpdateSelectionEnd(rawLine, m.safeLineLen(rawLine))
	m.EndSelection()

	rendered := m.View()
	got := firstHighlightedRow(rendered)
	if got == -1 {
		t.Fatalf("no highlight rendered for a selection on the visible bottom row\n---\n%s", rendered)
	}
	// The highlight must appear on (or spanning) the clicked screen row, not
	// `drop` rows away from it.
	if got != clickRow {
		t.Fatalf("highlight rendered on screen row %d, want clicked row %d (bottom-anchor drop not applied in highlight)\n---\n%s",
			got, clickRow, rendered)
	}
}
