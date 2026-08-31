package chat

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// Pasted text is not guaranteed to be valid UTF-8: a bracketed paste of
// latin-1 prose, a clipboard carrying binary, or output copied from a stream
// that was clipped mid-character all arrive as arbitrary bytes.
//
// The row-mapping code walks the value rune by rune while tracking BYTE
// offsets, and those offsets are used to slice i.value. Deriving the step from
// len(string(r)) is wrong for exactly this input: `for _, r := range s` yields
// utf8.RuneError after consuming ONE byte, but that rune re-encodes to THREE,
// so the offset overshoots and the slice panics. Because this runs under
// View(), the panic kills the whole TUI rather than mangling one line.
//
// This is the same defect class as the ExpandTabsANSI crash in
// toolrender/shared/fit.go, found by grepping for len(string( beside a slice.
func TestInputRowMappingSurvivesInvalidUTF8(t *testing.T) {
	values := []string{
		"\xe6",                    // lone 3-byte lead
		"\xe6\x97",                // first two bytes of a 3-byte rune
		"abc\xe6\x97",             // truncated rune at end of line
		"\xff\xfe",                // bytes that can never start a rune
		"\x80abc",                 // stray continuation byte
		"hi \xf0\x9f\x8e",         // first three bytes of a 4-byte emoji
		"a\n\xe6\x97\nb",          // truncated rune on its own paragraph
		strings.Repeat("\xe6", 8), // many, so offsets drift far
		"日本\xe6",                  // valid wide runes then a broken one
		"\ufffd\xe6",              // a LEGITIMATE U+FFFD next to a broken byte
	}

	for _, v := range values {
		if utf8.ValidString(v) && v != "\ufffd\xe6" {
			t.Fatalf("test bug: %q is valid UTF-8, it does not exercise the path", v)
		}

		for _, width := range []int{0, 1, 2, 3, 8, 40, 120} {
			in := NewSimpleInput()
			in.SetValue(v)
			in.SetWidth(width)
			in.SetHeight(3)
			in.SetInputOrigin(2, 0)

			rows := in.visibleRowsForMapping(width)

			// Every row must be a real slice of the value, and the offsets
			// must stay inside it. An over-advanced offset shows up here as
			// a panic; a subtly wrong one shows up as a bad startByte.
			for _, r := range rows {
				if r.startByte < 0 || r.startByte > len(in.value) {
					t.Fatalf("value=%q width=%d: startByte %d out of range [0,%d]",
						v, width, r.startByte, len(in.value))
				}
				if end := r.startByte + len(r.text); end > len(in.value) {
					t.Fatalf("value=%q width=%d: row ends at %d, past value length %d",
						v, width, end, len(in.value))
				}
			}

			// Screen->index mapping must also stay in bounds for every cell
			// the user could plausibly click, including past the end.
			// originX is 2, so screen column 2 is text column 0.
			for col := 0; col <= len(v)+4; col++ {
				off, ok := in.ScreenToIndex(2+col, 0)
				if !ok {
					continue
				}
				if off < 0 || off > len(in.value) {
					t.Fatalf("value=%q width=%d col=%d: offset %d out of range [0,%d]",
						v, width, col, off, len(in.value))
				}
			}
		}
	}
}

// A legitimate U+FFFD occupies three bytes and must still be stepped over as
// three, so a fix that special-cases "RuneError means one byte" is wrong.
func TestInputRowMappingHandlesRealReplacementChar(t *testing.T) {
	const v = "a\ufffdb"
	in := NewSimpleInput()
	in.SetValue(v)
	in.SetWidth(120)
	in.SetHeight(3)
	in.SetInputOrigin(2, 0)

	rows := in.visibleRowsForMapping(120)
	if len(rows) != 1 {
		t.Fatalf("rows = %d, want 1", len(rows))
	}
	if rows[0].text != v {
		t.Fatalf("row text = %q, want %q", rows[0].text, v)
	}
}
