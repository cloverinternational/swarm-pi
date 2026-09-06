package chat

import (
	"fmt"
	"math/rand"
	"strings"
	"testing"
	"unicode/utf8"
)

// nastyCellSamples is a corpus of cell contents that historically break
// terminal layout code: width is not character count, some runes are invisible,
// some are two columns wide, some carry their own escape sequences, and some
// are the very box-drawing characters the table is built from.
var nastyCellSamples = []string{
	"",              // empty
	" ",             // whitespace only
	"a",             // trivial
	"日本語のテキストです",    // CJK — two columns per rune
	"한국어 텍스트 예시입니다", // Hangul
	"中文字符测试内容比较长一些",                // CJK, no spaces to break on
	"🎉🎊🥳🎈🎁",                        // emoji, two columns each
	"👨‍👩‍👧‍👦 family",               // ZWJ sequence — one grapheme, many runes
	"🇯🇵🇺🇸🇩🇪",                       // regional indicator pairs
	"e\u0301\u0328\u0327combining", // stacked combining marks
	"\u200bzero\u200bwidth\u200b",  // zero-width spaces
	"\u00ad\u00adsoft hyphens",     // soft hyphens
	"مرحبا بالعالم",                // RTL Arabic
	"שלום עולם",                    // RTL Hebrew
	"a\tb\tc",                      // tabs
	"line\nbreak",                  // raw newline inside a cell
	"\x1b[31mpre-colored\x1b[0m",   // caller-supplied ANSI
	"\x1b[1;38;5;196mtruecolor\x1b[m",
	"\x07\x08\x00control",        // control characters
	"│┌┐└┘├┤┬┴┼─",                // the table's own box-drawing glyphs
	"**bold** _italic_ `code`",   // inline markdown
	"**unclosed bold",            // malformed markdown
	"`unclosed code",             // malformed inline code
	"a|b|c",                      // pipes inside a cell
	strings.Repeat("x", 300),     // one enormous unbreakable token
	strings.Repeat("日", 150),     // enormous wide-rune token
	strings.Repeat("word ", 100), // long but breakable
	"https://example.com/" + strings.Repeat("p/", 60),
	"\U0001F4A9\uFE0F variation selector",
	"ｆｕｌｌｗｉｄｔｈ　ａｓｃｉｉ", // fullwidth forms + ideographic space
	"<br>only<br/>breaks<br>",
	"…ellipsis… — em dash — ‑ non-breaking hyphen",
	"\u2028line sep\u2029para sep",
}

// fuzzWidths spans a phone-width pane through a very wide monitor, plus the
// degenerate widths that guard the arithmetic.
var fuzzWidths = []int{0, 1, 2, 3, 5, 8, 13, 20, 40, 61, 80, 120, 200}

// checkRenderInvariants asserts the properties that must hold for ANY input at
// ANY width: it must not panic, it must not exceed the viewport, it must
// terminate with a bounded number of lines, and it must emit valid UTF-8.
func checkRenderInvariants(t *testing.T, md string, width int) []string {
	t.Helper()

	lines := renderMarkdownWithWrapping(md, width, makeTestTheme())

	for i, line := range lines {
		if !utf8.ValidString(line) {
			t.Fatalf("width %d line %d is not valid UTF-8: %q", width, i, line)
		}
		// A width of 0 or less means "unconstrained" to the renderer.
		//
		// A width of 1 is exempt because a single grapheme can be two columns
		// wide (CJK, emoji): no layout can fit 日 into one column, and no real
		// terminal is one column wide.
		if width > 1 {
			if got := PrintableWidth(line); got > width {
				t.Fatalf("width %d line %d overflows by %d: %q",
					width, i, got-width, StripANSI(line))
			}
		}
	}

	// Guard against a wrap loop that makes no progress: even the most hostile
	// input should not explode into an unbounded number of lines.
	if limit := 20000; len(lines) > limit {
		t.Fatalf("width %d produced %d lines (limit %d) — possible non-terminating wrap",
			width, len(lines), limit)
	}
	return lines
}

// TestTableNastyCharacters renders every nasty sample in every structural
// position (single cell, beside a normal cell, as a header) at every width.
func TestTableNastyCharacters(t *testing.T) {
	for i, sample := range nastyCellSamples {
		for _, width := range fuzzWidths {
			t.Run(fmt.Sprintf("sample_%d/width_%d", i, width), func(t *testing.T) {
				// As the only cell.
				checkRenderInvariants(t, "| H |\n| --- |\n| "+sample+" |", width)
				// Beside ordinary content.
				checkRenderInvariants(t,
					"| Term | Detail |\n| --- | --- |\n| label | "+sample+" |\n| plain | ordinary text |",
					width)
				// As a header.
				checkRenderInvariants(t,
					"| "+sample+" | B |\n| --- | --- |\n| value | other |",
					width)
				// Mixed with another nasty sample.
				other := nastyCellSamples[(i+7)%len(nastyCellSamples)]
				checkRenderInvariants(t,
					"| A | B |\n| --- | --- |\n| "+sample+" | "+other+" |",
					width)
			})
		}
	}
}

// TestTableMalformedStructure covers tables whose shape is broken: ragged rows,
// missing separators, empty cells, stray pipes.
func TestTableMalformedStructure(t *testing.T) {
	malformed := []string{
		"| |",
		"||",
		"|||||",
		"| a |\n| --- |",
		"| --- |",
		"| --- | --- |\n| only | separator |",
		"| a | b |\n| --- |\n| 1 | 2 | 3 | 4 |",
		"| a |\n| --- | --- | --- |\n| 1 |",
		"| a | b |\n| :-: | ---: |\n| 1 | 2 |",
		"|\n|\n|",
		"| a |\n\n| b |",
		strings.Repeat("|", 200),
		"| " + strings.Repeat("a | ", 200) + "|",
	}

	for i, md := range malformed {
		for _, width := range fuzzWidths {
			t.Run(fmt.Sprintf("malformed_%d/width_%d", i, width), func(t *testing.T) {
				checkRenderInvariants(t, md, width)
			})
		}
	}
}

// TestTableRandomizedCells is a randomized property test: it assembles tables
// from the nasty corpus with random shapes and checks the same invariants. It
// is deterministic (fixed seed) so a failure is reproducible.
func TestTableRandomizedCells(t *testing.T) {
	rng := rand.New(rand.NewSource(0x5EED))

	for iter := 0; iter < 400; iter++ {
		cols := 1 + rng.Intn(8)
		rows := 1 + rng.Intn(6)

		var sb strings.Builder
		for c := 0; c < cols; c++ {
			sb.WriteString("| " + nastyCellSamples[rng.Intn(len(nastyCellSamples))] + " ")
		}
		sb.WriteString("|\n")
		for c := 0; c < cols; c++ {
			sb.WriteString("| --- ")
		}
		sb.WriteString("|\n")
		for r := 0; r < rows; r++ {
			// Ragged rows on purpose: a row may have fewer or more cells.
			n := 1 + rng.Intn(cols+2)
			for c := 0; c < n; c++ {
				sb.WriteString("| " + nastyCellSamples[rng.Intn(len(nastyCellSamples))] + " ")
			}
			sb.WriteString("|\n")
		}

		md := sb.String()
		width := fuzzWidths[rng.Intn(len(fuzzWidths))]
		t.Run(fmt.Sprintf("iter_%d", iter), func(t *testing.T) {
			checkRenderInvariants(t, md, width)
		})
	}
}

// FuzzRenderMarkdownTable is the native fuzz target. Run it with:
//
//	go test ./internal/chat/ -run XXX -fuzz FuzzRenderMarkdownTable -fuzztime 60s
func FuzzRenderMarkdownTable(f *testing.F) {
	f.Add("| a | b |\n| --- | --- |\n| 1 | 2 |", 80)
	f.Add("| 日本語 | x |\n| --- | --- |\n| 🎉 | y |", 20)
	f.Add("|", 1)
	f.Add("| "+strings.Repeat("x", 100)+" |", 10)
	f.Add("| a |\n| --- |\n| \x1b[31mred\x1b[0m |", 13)

	f.Fuzz(func(t *testing.T, md string, width int) {
		// Keep the search space in the range a terminal can actually be.
		if width < 0 || width > 400 {
			t.Skip()
		}
		if len(md) > 4096 {
			t.Skip()
		}
		// Model and tool output is UTF-8; arbitrary invalid byte sequences are
		// out of scope here (they are dropped from table cells, but prose keeps
		// them verbatim, and widths are undefined for them).
		if !utf8.ValidString(md) {
			t.Skip()
		}
		checkRenderInvariants(t, md, width)
	})
}
