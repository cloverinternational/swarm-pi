// Package fuzzcorpus holds the hostile-input corpus and the layout invariants
// that every tool-output renderer must satisfy.
//
// WHY THIS EXISTS: tool output is attacker-adjacent data. It is command stdout,
// file contents, web-search results and sub-agent transcripts — none of it is
// written by us, and all of it lands in a fixed-width terminal pane. The
// markdown table renderer in internal/chat was fuzzed first and produced three
// bugs, each of which recurs in the tool renderers:
//
//  1. A tab measures as one column (ansi.StringWidth) but expands to the next
//     tab stop on a real terminal, shearing every glyph to its right.
//  2. A layout that clamps its content width UP to a minimum (max(width-8, 30))
//     overflows every pane narrower than that minimum.
//  3. Byte slicing (s[:n]) instead of width-aware truncation both splits UTF-8
//     sequences in half and mismeasures wide runes.
//
// The corpus and CheckLines below are shared by every <pkg>_fuzz_test.go so a
// new renderer inherits the same bar by construction. It is a normal (non-test)
// package only because Go cannot share _test.go code across packages.
package fuzzcorpus

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/charmbracelet/x/ansi"
)

// Samples is the corpus of payloads that historically break terminal layout
// code: width is not byte count nor rune count, some runes are invisible, some
// are two columns wide, some carry their own escape sequences, and some are the
// very box-drawing glyphs the renderers build their gutters from.
var Samples = []string{
	"",              // empty
	"   ",           // whitespace only
	"a",             // trivial
	"日本語のテキスト",      // CJK — two columns per rune
	"한국어 텍스트 예시입니다", // Hangul
	"中文字符测试内容比较长一些", // CJK with no spaces to break on
	"🎉",                            // single emoji, two columns
	"🎉🎊🥳🎈🎁",                        // emoji run
	"👨‍👩‍👧‍👦 family",               // ZWJ sequence — one grapheme, many runes
	"🇯🇵🇺🇸",                         // regional indicator pairs
	"e\u0301\u0328\u0327combining", // stacked combining marks
	"\u200bzero\u200bwidth\u200b",  // zero-width spaces
	"\u00ad\u00adsoft hyphens",     // soft hyphens
	"مرحبا بالعالم",                // RTL Arabic
	"שלום עולם",                    // RTL Hebrew
	"a\tb\tc",                      // tabs — measure 1, expand to a tab stop
	"\t\t\tdeep tabs",              // leading tabs
	"line\nbreak",                  // raw newline inside a payload
	"\x1b[31mred\x1b[0m",           // caller-supplied SGR
	"\x1b[1;38;5;196mtruecolor\x1b[m",
	"\x1b[2J\x1b[H",                                   // non-SGR CSI: erase screen + cursor home
	"\x07\x08\x00control",                             // C0 controls: BEL, BS, NUL
	"\u009b31mC1 CSI",                                 // C1 eight-bit CSI introducer (0x9B)
	"│┌┐└┘├┤─",                                        // the renderers' own box-drawing glyphs
	"**bold** _italic_ `code`",                        // inline markdown
	"| a | b |\n| --- | --- |",                        // markdown-shaped payload
	`{"a":{"b":[1,2,{"c":"d"}]}}`,                     // JSON-shaped payload
	strings.Repeat("x", 300),                          // one enormous unbreakable token
	strings.Repeat("日", 150),                          // enormous wide-rune token
	strings.Repeat("word ", 100),                      // long but breakable
	strings.Repeat("a\tb ", 60),                       // long and full of tabs
	"https://example.com/" + strings.Repeat("p/", 60), // long URL
	"\U0001F4A9\uFE0F variation selector",
	"ｆｕｌｌｗｉｄｔｈ　ａｓｃｉｉ", // fullwidth forms + ideographic space
	"…ellipsis… — em dash — ‑ non-breaking hyphen",
	"\u2028line sep\u2029para sep",
}

// Widths spans a phone-width pane through a very wide monitor, plus the
// degenerate widths that guard the arithmetic.
var Widths = []int{1, 2, 3, 5, 8, 13, 20, 40, 61, 80, 120, 200}

// MaxLines bounds the output of any renderer. A wrap loop that makes no
// progress would otherwise hang the fuzzer instead of failing it.
const MaxLines = 20000

// CheckLines asserts the properties that must hold for ANY renderer on ANY
// input at ANY width.
//
// The label identifies the call site (renderer entry point + sample index) so a
// fuzz failure points straight at the offending path.
//
// Invariants:
//
//   - Valid UTF-8 out when valid UTF-8 went in. Byte-slicing truncation
//     (s[:n]) violates this by cutting a multi-byte rune in half.
//   - No raw tab in the output. A tab is one column to every width function we
//     have but expands to the next tab stop on the terminal, so a line that
//     "fits" shears the pane. Renderers must expand tabs before measuring.
//   - Visual width <= the requested width. EXEMPTION: skipped when width < 2,
//     because a single grapheme can be two columns wide (日, 🎉) and no layout
//     can fit it into one column. No real terminal is one column wide.
//   - Bounded line count, so a non-terminating wrap cannot pass silently.
func CheckLines(t *testing.T, label string, lines []string, width int) {
	t.Helper()

	if len(lines) > MaxLines {
		t.Fatalf("%s: width %d produced %d lines (limit %d) — possible non-terminating wrap",
			label, width, len(lines), MaxLines)
	}

	for i, line := range lines {
		if !utf8.ValidString(line) {
			t.Fatalf("%s: width %d line %d is not valid UTF-8: %q", label, width, i, line)
		}
		if strings.ContainsRune(line, '\t') {
			t.Fatalf("%s: width %d line %d contains a raw tab (measures 1 column, expands to a tab stop): %q",
				label, width, i, ansi.Strip(line))
		}
		if width < 2 {
			continue
		}
		if got := ansi.StringWidth(line); got > width {
			t.Fatalf("%s: width %d line %d overflows by %d: %q",
				label, width, i, got-width, ansi.Strip(line))
		}
	}
}

// ValidInput reports whether a fuzzer-supplied string is in scope. Tool output
// is UTF-8 by contract; arbitrary invalid byte sequences have undefined width
// and are handled (dropped or passed through) by the transport, not the
// renderers. Oversized inputs are skipped so the fuzzer spends its budget on
// structure rather than on length.
func ValidInput(s string) bool {
	return utf8.ValidString(s) && len(s) <= 8192
}

// ValidWidth keeps the fuzzer's width search inside the range a terminal can
// actually be. Negative and absurd widths are the caller's bug, not ours.
func ValidWidth(w int) bool {
	return w >= 0 && w <= 400
}
