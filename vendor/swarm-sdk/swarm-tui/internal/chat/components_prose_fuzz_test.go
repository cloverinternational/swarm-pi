package chat

import (
	"fmt"
	"math/rand"
	"strings"
	"testing"
	"unicode/utf8"
)

// This file is the blast-radius check for the prose wrap change.
//
// The table work removed a hidden floor in the prose wrapper: it used to clamp
// the available width up to 20 columns, which meant every pane narrower than 20
// overflowed. Removing the floor is correct, but it changes behaviour for ALL
// assistant output, not just tables — so prose needs the same invariant
// coverage the tables got.
//
// The invariants are deliberately identical to the table ones (see
// checkRenderInvariants): valid UTF-8, never wider than the viewport, bounded
// line count, no panics.

// proseBlocks are non-table markdown constructs. Each is a complete block that
// the renderer handles on a different code path: headings, lists, quotes,
// fences, rules and inline styling.
var proseBlocks = []string{
	"# Heading one",
	"## Heading two with a considerably longer trailing phrase",
	"### Heading three",
	"#### Heading four",
	"##### Heading five",
	"###### Heading six",
	"#NoSpaceAfterHash",
	"####### Seven hashes is not a heading",
	"Plain prose that is long enough to need wrapping at any sensible terminal width at all.",
	"- simple bullet",
	"- bullet with a long tail that must wrap onto continuation lines and stay aligned",
	"* star bullet",
	"+ plus bullet",
	"1. ordered item",
	"27. ordered item with a large marker",
	"- top\n  - nested\n    - deeper\n      - deeper still\n        - absurdly deep nesting level",
	"1. one\n   1. one point one\n      1. one point one point one",
	"> a block quote",
	"> quoted line one\n> quoted line two that is long enough to wrap somewhere",
	">>> nested quote markers",
	"```\nplain fenced code\n```",
	"```go\nfunc main() { fmt.Println(\"hello\") }\n```",
	"```python\ndef f(x):\n    return x * 2\n```",
	"```\nunterminated fence",
	"~~~\ntilde fence\n~~~",
	"    four space indented code block",
	"\tliteral tab indented code",
	"---",
	"***",
	"___",
	"**bold** and _italic_ and `code` and ~~strike~~ all in one line",
	"**unclosed bold and `unclosed code",
	"[markdown link](https://example.com/some/deep/path?query=1&other=2)",
	"bare https://example.com/" + strings.Repeat("segment/", 40) + " trailing",
	"see internal/chat/components.go:2172 for the layout",
	"`" + strings.Repeat("x", 200) + "`",
	strings.Repeat("unbreakable", 40),
	strings.Repeat("日", 200),
	"mixed 日本語 and english text repeated " + strings.Repeat("more ", 40),
	"\x1b[31mpre-colored prose\x1b[0m continues here",
	"line with trailing spaces   \nnext line",
	"",
	" ",
	"\n\n\n",
}

// narrowWidths covers the region the removed floor used to hide, plus the
// degenerate widths that guard the arithmetic.
var narrowWidths = []int{0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 12, 14, 16, 18, 19, 20, 21, 24}

// checkProseInvariants renders markdown in both compact and verbose mode. Ctrl+O
// (verbose) changes indentation, so a layout that fits in one mode can overflow
// in the other — that asymmetry is exactly what hid the blank-image bug.
func checkProseInvariants(t *testing.T, md string, width int) {
	t.Helper()
	for _, verbose := range []bool{false, true} {
		lines := renderMarkdownWithWrappingVerbose(md, width, makeTestTheme(), verbose)
		for i, line := range lines {
			if !utf8.ValidString(line) {
				t.Fatalf("verbose=%v width %d line %d is not valid UTF-8: %q",
					verbose, width, i, line)
			}
			// width <= 0 means "unconstrained"; width 1 cannot hold a
			// two-column grapheme, so both are exempt from the width bound.
			if width > 1 {
				if got := PrintableWidth(line); got > width {
					t.Fatalf("verbose=%v width %d line %d overflows by %d: %q",
						verbose, width, i, got-width, StripANSI(line))
				}
			}
		}
		if limit := 20000; len(lines) > limit {
			t.Fatalf("verbose=%v width %d produced %d lines (limit %d) — possible non-terminating wrap",
				verbose, width, len(lines), limit)
		}
	}
}

// TestProseNarrowWidths is the direct regression test for the removed width
// floor: every prose construct, at every width below and around the old
// clamp of 20.
func TestProseNarrowWidths(t *testing.T) {
	for i, block := range proseBlocks {
		for _, width := range narrowWidths {
			t.Run(fmt.Sprintf("block_%d/width_%d", i, width), func(t *testing.T) {
				checkProseInvariants(t, block, width)
			})
		}
	}
}

// TestProseNastyCharacters embeds the hostile-character corpus into every prose
// construct, not just table cells.
func TestProseNastyCharacters(t *testing.T) {
	carriers := []string{
		"%s",
		"# %s",
		"- %s",
		"1. %s",
		"  - nested %s",
		"> %s",
		"```\n%s\n```",
		"```go\n%s\n```",
		"**%s**",
		"`%s`",
		"[label](%s)",
		"prefix %s suffix",
	}
	for i, sample := range nastyCellSamples {
		for c, carrier := range carriers {
			for _, width := range fuzzWidths {
				t.Run(fmt.Sprintf("sample_%d/carrier_%d/width_%d", i, c, width), func(t *testing.T) {
					checkProseInvariants(t, fmt.Sprintf(carrier, sample), width)
				})
			}
		}
	}
}

// TestProseRandomizedDocuments assembles multi-block documents from the corpus.
// Blocks interact: a fence can swallow a following heading, a list can absorb a
// quote. Deterministic seed, so failures reproduce.
func TestProseRandomizedDocuments(t *testing.T) {
	rng := rand.New(rand.NewSource(0xC0FFEE))

	for iter := 0; iter < 600; iter++ {
		n := 1 + rng.Intn(8)
		var sb strings.Builder
		for b := 0; b < n; b++ {
			sb.WriteString(proseBlocks[rng.Intn(len(proseBlocks))])
			sb.WriteString("\n")
			if rng.Intn(3) == 0 {
				sb.WriteString("\n")
			}
		}
		width := narrowWidths[rng.Intn(len(narrowWidths))]
		if rng.Intn(2) == 0 {
			width = fuzzWidths[rng.Intn(len(fuzzWidths))]
		}
		md := sb.String()
		t.Run(fmt.Sprintf("iter_%d", iter), func(t *testing.T) {
			checkProseInvariants(t, md, width)
		})
	}
}

// TestProseMixedWithTables covers documents where prose and tables share a
// message, since the table renderer and the prose wrapper split the same width
// budget.
func TestProseMixedWithTables(t *testing.T) {
	docs := []string{
		"Intro paragraph.\n\n| a | b |\n| --- | --- |\n| 1 | 2 |\n\nClosing paragraph.",
		"# Title\n\n- bullet before\n\n| col |\n| --- |\n| " + strings.Repeat("v", 80) + " |\n\n> quote after",
		"```\n| not | a | table |\n```\n\n| real | table |\n| --- | --- |\n| x | y |",
		"| a | b |\n| --- | --- |\n| 1 | 2 |\n| unterminated",
	}
	for i, md := range docs {
		for _, width := range append(append([]int{}, narrowWidths...), fuzzWidths...) {
			t.Run(fmt.Sprintf("doc_%d/width_%d", i, width), func(t *testing.T) {
				checkProseInvariants(t, md, width)
			})
		}
	}
}

// FuzzRenderMarkdownProse is the native fuzz target for non-table rendering.
// Run it with:
//
//	go test ./internal/chat/ -run XXX -fuzz FuzzRenderMarkdownProse -fuzztime 60s
func FuzzRenderMarkdownProse(f *testing.F) {
	f.Add("# Heading\n\nSome prose that wraps.", 80)
	f.Add("- a\n  - b\n    - c", 8)
	f.Add("```go\nfunc main() {}\n```", 5)
	f.Add("> quoted "+strings.Repeat("word ", 30), 3)
	f.Add("**bold** `code` [x](https://example.com)", 12)
	f.Add(strings.Repeat("日", 50), 2)
	f.Add("\x1b[31mred\x1b[0m", 1)

	f.Fuzz(func(t *testing.T, md string, width int) {
		if width < 0 || width > 400 {
			t.Skip()
		}
		if len(md) > 4096 {
			t.Skip()
		}
		// Prose keeps invalid bytes verbatim and their display width is
		// undefined, so the width bound cannot be asserted for them.
		if !utf8.ValidString(md) {
			t.Skip()
		}
		checkProseInvariants(t, md, width)
	})
}
