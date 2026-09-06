package chat

import (
	"fmt"
	"math/rand"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/ansi/kitty"

	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/termimage"
)

// wrapFuzzWidths spans a phone-width pane through a wide monitor plus the
// degenerate widths that guard the arithmetic.
var wrapFuzzWidths = []int{1, 2, 3, 5, 8, 13, 20, 40, 61, 80, 120, 200}

// checkWrapInvariants asserts the properties the viewport depends on for every
// wrapped line. Violating any of them corrupts YOffset arithmetic, the raw ->
// wrapped mapping, scroll anchoring, hit-testing, or image placement.
func checkWrapInvariants(t *testing.T, line string, width int) []string {
	t.Helper()

	wrapped := wrapLineToWidth(line, width)

	if len(wrapped) == 0 {
		t.Fatalf("width %d: wrapping produced no lines for %q", width, line)
	}
	if limit := 20000; len(wrapped) > limit {
		t.Fatalf("width %d: wrapping produced %d lines (limit %d) — possible non-terminating wrap",
			width, len(wrapped), limit)
	}

	for i, out := range wrapped {
		if !utf8.ValidString(out) && utf8.ValidString(line) {
			t.Fatalf("width %d line %d: valid input produced invalid UTF-8", width, i)
		}
		// Width 1 is exempt: a single grapheme can be two columns wide, so no
		// wrapping can make 日 fit into one column.
		if width > 1 {
			if got := ansi.StringWidth(out); got > width {
				t.Errorf("width %d line %d: wrapped to %d columns\n%q",
					width, i, got, ansi.Strip(out))
			}
		}
		// A placeholder cell with no image-ID foreground in front of it is an
		// orphan: the terminal reserves the columns and paints nothing.
		if idx := strings.IndexRune(out, kitty.Placeholder); idx >= 0 {
			if !strings.Contains(out[:idx], "\x1b[38;") {
				t.Errorf("width %d line %d: placeholder cells with no image ID (blank image)\n%q",
					width, i, out)
			}
		}
		// A hyperlink left open at the end of a line claims every cell the
		// viewport paints afterwards; one closed but never reopened silently
		// stops the rest of the link being clickable. Every emitted line must
		// therefore balance its own link markup.
		if opens, closes := countLinkSegments(out); opens != closes {
			t.Errorf("width %d line %d: unbalanced hyperlink (%d opens, %d closes)\n%q",
				width, i, opens, closes, out)
		}
	}
	return wrapped
}

// TestWrapPlaceholderRunsAcrossSizes sweeps image geometry against viewport
// width. Any combination must keep every placeholder cell bound to its image
// and every wrapped line inside the viewport.
func TestWrapPlaceholderRunsAcrossSizes(t *testing.T) {
	for _, columns := range []int{1, 2, 7, 20, 40, 80, 200} {
		for _, indent := range []int{0, 2, 8, 20} {
			for _, width := range wrapFuzzWidths {
				t.Run(fmt.Sprintf("cols_%d/indent_%d/width_%d", columns, indent, width), func(t *testing.T) {
					placement := termimage.Placement{
						Source:      termimage.Source{Key: "k", PNG: []byte{1}, Width: 10, Height: 10},
						Occurrence:  "occ",
						Columns:     columns,
						Rows:        2,
						ImageID:     0x01020304,
						PlacementID: 9,
					}
					for _, raw := range termimage.PlaceholderLines(placement) {
						line := strings.Repeat(" ", indent) + raw
						checkWrapInvariants(t, line, width)
					}
				})
			}
		}
	}
}

// wrapCorpus collects line shapes that have historically broken the wrapper:
// styled and unstyled oversized tokens, wide runes, and control content.
var wrapCorpus = []string{
	"",
	" ",
	"short line",
	strings.Repeat("word ", 80),
	strings.Repeat("x", 500), // unstyled oversized token
	"\x1b[31m" + strings.Repeat("y", 500) + "\x1b[0m", // styled oversized token
	"\x1b[48;2;10;10;10m" + strings.Repeat("z", 200),  // background-carrying token
	strings.Repeat("日", 200),                          // oversized wide-rune token
	strings.Repeat("🎉", 100),                          // oversized emoji token
	"  indented " + strings.Repeat("q", 300),
	"- bullet " + strings.Repeat("b", 300),
	`{"status":"succeeded","results":[` + strings.Repeat(`{"k":"v"},`, 60) + "]}",
	"https://example.com/" + strings.Repeat("segment/", 50),
	"\x1b[38;2;1;2;3mfg\x1b[58;2;4;5;6mul\x1b[39m\x1b[59m",
	"mixed \x1b[1mbold\x1b[0m and " + strings.Repeat("t", 200),
	"\u200b\u200b\u200bzero width",
	"a\tb\tc",
}

// linkCorpus adds hyperlink shapes to the wrap corpus: a link long enough to
// wrap several times, a link beside plain text, adjacent links, and a link
// whose target is far longer than its label.
var linkCorpus = []string{
	Hyperlink("short", "https://example.com"),
	Hyperlink(strings.Repeat("clickable ", 15), "https://example.com/docs"),
	"before " + Hyperlink("middle", "https://example.com") + " after",
	Hyperlink("a", "https://example.com/1") + Hyperlink("b", "https://example.com/2"),
	Hyperlink("tiny", "https://example.com/"+strings.Repeat("segment/", 40)),
	Hyperlink(strings.Repeat("日本語", 20), "https://example.com/wide"),
	"  indented " + Hyperlink(strings.Repeat("link ", 20), "file://host/tmp/x.go#12"),
}

// TestWrapLinkCorpus renders every hyperlink shape at every width, enforcing
// the same invariants as the rest of the wrapper — including link balance.
func TestWrapLinkCorpus(t *testing.T) {
	for i, line := range linkCorpus {
		for _, width := range wrapFuzzWidths {
			t.Run(fmt.Sprintf("link_%d/width_%d", i, width), func(t *testing.T) {
				checkWrapInvariants(t, line, width)
			})
		}
	}
}

// TestWrapCorpus renders every corpus entry at every width.
func TestWrapCorpus(t *testing.T) {
	for i, line := range wrapCorpus {
		for _, width := range wrapFuzzWidths {
			t.Run(fmt.Sprintf("corpus_%d/width_%d", i, width), func(t *testing.T) {
				checkWrapInvariants(t, line, width)
			})
		}
	}
}

// TestWrapRandomizedLines is a seeded property test that splices corpus
// fragments, placeholder runs and styling into random lines.
func TestWrapRandomizedLines(t *testing.T) {
	rng := rand.New(rand.NewSource(0xC0FFEE))
	placement := termimage.Placement{
		Source:      termimage.Source{Key: "k", PNG: []byte{1}, Width: 10, Height: 10},
		Occurrence:  "occ",
		Columns:     12,
		Rows:        1,
		ImageID:     0x00ABCDEF,
		PlacementID: 3,
	}
	placeholderRun := termimage.PlaceholderLines(placement)[0]

	fragments := append([]string{placeholderRun}, wrapCorpus...)

	for iter := 0; iter < 600; iter++ {
		var sb strings.Builder
		for parts := rng.Intn(4) + 1; parts > 0; parts-- {
			sb.WriteString(fragments[rng.Intn(len(fragments))])
			if rng.Intn(2) == 0 {
				sb.WriteString(" ")
			}
		}
		width := wrapFuzzWidths[rng.Intn(len(wrapFuzzWidths))]
		t.Run(fmt.Sprintf("iter_%d", iter), func(t *testing.T) {
			checkWrapInvariants(t, sb.String(), width)
		})
	}
}

// FuzzWrapLineToWidth is the native fuzz target for the viewport wrapper:
//
//	go test ./internal/chat/ -run XXX -fuzz FuzzWrapLineToWidth -fuzztime 60s
func FuzzWrapLineToWidth(f *testing.F) {
	f.Add("hello world", 80)
	f.Add(strings.Repeat("x", 300), 20)
	f.Add("\x1b[31m"+strings.Repeat("y", 300)+"\x1b[0m", 13)
	f.Add(strings.Repeat("日", 100), 7)
	f.Add("  indented "+strings.Repeat("q", 100), 5)

	f.Fuzz(func(t *testing.T, line string, width int) {
		if width < 1 || width > 400 {
			t.Skip()
		}
		if len(line) > 4096 {
			t.Skip()
		}
		if !utf8.ValidString(line) {
			t.Skip()
		}
		checkWrapInvariants(t, line, width)
	})
}
