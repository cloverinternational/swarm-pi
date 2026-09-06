package shared

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/charmbracelet/x/ansi"
)

// TestExpandTabsANSILeavesNoTabs pins the contract that makes FitLine safe:
// after expansion there is no tab left ANYWHERE in the string, including
// inside malformed escape sequences.
//
// Every case below was found by the renderer fuzz targets. They all share one
// shape: a control byte hidden inside an escape sequence that never terminates.
// If the scanner treats the whole remainder as "an escape sequence" it copies
// the tab through verbatim, the tab then measures one column to every width
// function we have, and the terminal expands it to a tab stop — shearing every
// glyph to its right out of the pane. Per ECMA-48 a control byte terminates the
// sequence it appears in, so the scanner must abort rather than absorb.
func TestExpandTabsANSILeavesNoTabs(t *testing.T) {
	cases := []string{
		"\x1b\t",       // ESC + TAB: not a two-byte escape
		"\x1b\t0",      //
		"\x1b[\t0",     // TAB inside a CSI parameter list
		"\x1b]\t0",     // TAB inside an OSC command string
		"\x1b]\t",      //
		"\x9b\t",       // eight-bit CSI introducer + TAB
		"\x1b",         // bare ESC at EOF
		"\x1b[",        // truncated CSI at EOF
		"\x1b]8;;http", // unterminated OSC hyperlink
		"\x1b]0;t\x07\ta",
		"a\tb\x1b[31m\tc\x1b[0m\t",
		"\x1b[31m\x1b[\t",
		"日\t本",
		"🎉\t",

		// Invalid / truncated UTF-8 beside a tab. The scanner must advance by
		// the bytes it actually CONSUMED: `range` reports utf8.RuneError while
		// advancing one byte, but that rune ENCODES as three bytes, so sizing
		// the step from the decoded rune slices past the end of the remainder
		// and panics ("slice bounds out of range [:3] with length 2").
		// Truncated runes reach this function for real whenever tool output is
		// clipped or chunked mid-character.
		"\t\xe6",          // 3-byte lead byte alone after a tab
		"\t\xe6\x97",      // first two bytes of a 3-byte rune
		"\xe6\x97\t",      // truncated rune BEFORE the tab
		"\t\xff",          // byte that can never start a rune
		"\x80\ta",         // stray continuation byte
		"a\t\xf0\x9f\x8e", // first three bytes of a 4-byte emoji
		"\x1b[31m\t\xe6",  // truncated rune after a tab inside styled output
	}
	for _, in := range cases {
		got := ExpandTabsANSI(in)
		if strings.ContainsRune(got, '\t') {
			t.Errorf("ExpandTabsANSI(%q) still contains a tab: %q", in, got)
		}
		if utf8.ValidString(in) && !utf8.ValidString(got) {
			t.Errorf("ExpandTabsANSI(%q) produced invalid UTF-8: %q", in, got)
		}
	}
}

// FuzzFitLine drives the primitive that every renderer's final clamp depends
// on. If FitLine can be made to emit a tab, exceed the width, or corrupt UTF-8,
// then every renderer is broken at once. Run it with:
//
//	go test ./internal/chat/toolrender/shared/ -run XXX -fuzz FuzzFitLine -fuzztime 30s
func FuzzFitLine(f *testing.F) {
	f.Add("plain text", 20)
	f.Add("a\tb\tc", 5)
	f.Add("\x1b[31mred\x1b[0m", 3)
	f.Add("\x1b]\t0", 8)
	f.Add("日本語のテキスト🎉", 7)
	f.Add("\x9b31m\x1b[\t", 4)

	f.Fuzz(func(t *testing.T, line string, width int) {
		if width < 0 || width > 400 || len(line) > 4096 || !utf8.ValidString(line) {
			t.Skip()
		}
		// A renderer returns one string per screen line; a newline in the input
		// is the caller's bug, not this function's.
		if strings.ContainsAny(line, "\n\r") {
			t.Skip()
		}
		got := FitLine(line, width)

		if strings.ContainsRune(got, '\t') {
			t.Fatalf("FitLine(%q, %d) emitted a raw tab: %q", line, width, got)
		}
		if !utf8.ValidString(got) {
			t.Fatalf("FitLine(%q, %d) emitted invalid UTF-8: %q", line, width, got)
		}
		// Width 0 means unconstrained; width 1 is exempt because a single
		// grapheme can be two columns wide and no cut can make 日 fit in one.
		if width > 1 {
			if w := ansi.StringWidth(got); w > width {
				t.Fatalf("FitLine(%q, %d) is %d columns wide: %q", line, width, w, ansi.Strip(got))
			}
		}
	})
}
