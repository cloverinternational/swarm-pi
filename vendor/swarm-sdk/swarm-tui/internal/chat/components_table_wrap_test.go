package chat

import (
	"fmt"
	"strings"
	"testing"
)

// probeViewportWidths are the terminal widths the table probe renders at:
// a narrow split pane, a phone-sized SSH session, the classic 80 columns,
// a common laptop window, and a wide monitor.
var probeViewportWidths = []int{40, 60, 80, 100, 120, 160}

// userReportedTable is the table from the bug report: a 3-column Spanish table
// where the first column held short labels and the middle column held long
// sentences. Before the fix every overflowing cell was cut off with "…", so the
// reader lost the end of each sentence and could not tell what the row said.
const userReportedTable = `| Situación | Qué significa | Riesgo del force |
| --- | --- | --- |
| Solo BEHIND | Remote tiene commits que local no tiene, pero local no tiene nada nuevo | Bajo — solo pierdes la historia lineal |
| **Divergido** | Remote Y local tienen commits diferentes entre sí | **ALTO** — el force push destruye los commits del remote |`

// largeProbeTable is a deliberately demanding table: seven columns, mixed
// short/long content, inline markdown styling, and a very long unbroken token.
const largeProbeTable = `| ID | Component | Description | Owner | Status | Latency | Notes |
| --- | --- | --- | --- | --- | --- | --- |
| 1 | Auth Gateway | Handles OAuth 2.0 authorization code flow with PKCE for every first-party client | platform-team | **Complete** | 42ms | Rotate signing keys quarterly |
| 2 | Session Store | Redis-backed session cache with sliding expiry and cross-region replication | infra | In Progress | 7ms | supercalifragilisticexpialidocious_identifier_that_never_breaks |
| 3 | Billing | Usage metering, invoice generation, and dunning emails for delinquent accounts | payments | Planned | 310ms | Blocked on tax vendor |`

// renderProbe renders markdown at a width and returns the visual lines.
func renderProbe(t *testing.T, md string, width int) []string {
	t.Helper()
	return renderMarkdownWithWrapping(md, width, makeTestTheme())
}

func stripAllForProbe(lines []string) []string {
	out := make([]string, len(lines))
	for i, l := range lines {
		out[i] = StripANSI(l)
	}
	return out
}

// TestTableProbeAcrossViewports is the visual probe. Run with -v to eyeball the
// rendering at each viewport width:
//
//	go test ./internal/chat/ -run TestTableProbeAcrossViewports -v
//
// It also asserts the property the fix guarantees at every width: nothing
// overflows the viewport.
func TestTableProbeAcrossViewports(t *testing.T) {
	cases := []struct {
		name  string
		input string
	}{
		{name: "user_reported_spanish_table", input: userReportedTable},
		{name: "large_seven_column_table", input: largeProbeTable},
	}

	for _, tc := range cases {
		for _, width := range probeViewportWidths {
			t.Run(fmt.Sprintf("%s/width_%d", tc.name, width), func(t *testing.T) {
				lines := renderProbe(t, tc.input, width)
				ruler := strings.Repeat("┄", width)
				t.Logf("\n%s @ width=%d\n%s\n%s\n%s",
					tc.name, width, ruler,
					strings.Join(stripAllForProbe(lines), "\n"), ruler)

				if len(lines) == 0 {
					t.Fatalf("no output rendered at width=%d", width)
				}
				for i, line := range lines {
					if got := PrintableWidth(line); got > width {
						t.Errorf("line %d overflows viewport: visual width %d > %d\n%q",
							i, got, width, StripANSI(line))
					}
				}
			})
		}
	}
}

// TestTableCellsWordWrapInsteadOfTruncating is the direct regression test for
// the user's complaint: a cell that does not fit must continue on the next
// physical line, not vanish behind an ellipsis.
func TestTableCellsWordWrapInsteadOfTruncating(t *testing.T) {
	for _, width := range probeViewportWidths {
		t.Run(fmt.Sprintf("width_%d", width), func(t *testing.T) {
			text := strings.Join(stripAllForProbe(renderProbe(t, userReportedTable, width)), "\n")

			if strings.Contains(text, "…") {
				t.Errorf("table at width=%d still truncates content with an ellipsis:\n%s", width, text)
			}

			// Every word of the longest cell must survive somewhere in the output.
			longCell := "Remote tiene commits que local no tiene, pero local no tiene nada nuevo"
			for _, word := range strings.Fields(longCell) {
				if !strings.Contains(text, word) {
					t.Errorf("word %q lost at width=%d; table content was dropped:\n%s", word, width, text)
				}
			}
		})
	}
}

// TestTableWrapKeepsWordsIntact verifies the wrap points land between words
// rather than mid-word (the "word wrapping" half of the request).
func TestTableWrapKeepsWordsIntact(t *testing.T) {
	md := "| Phrase |\n| --- |\n| alpha bravo charlie delta echo foxtrot |"
	// Width 24 forces the cell to wrap across several lines.
	joined := strings.Join(stripAllForProbe(renderProbe(t, md, 24)), "\n")

	for _, word := range []string{"alpha", "bravo", "charlie", "delta", "echo", "foxtrot"} {
		if !strings.Contains(joined, word) {
			t.Fatalf("word %q was split across lines or dropped:\n%s", word, joined)
		}
	}
}

// TestTableNarrowColumnsKeepNaturalWidth verifies the water-filling allocation:
// a narrow column next to a very wide one keeps its full natural width instead
// of being shrunk proportionally alongside the wide column.
func TestTableNarrowColumnsKeepNaturalWidth(t *testing.T) {
	md := "| ID | Description |\n| --- | --- |\n" +
		"| 42 | " + strings.Repeat("long ", 40) + "|"

	lines := stripAllForProbe(renderProbe(t, md, 60))
	if len(lines) < 4 {
		t.Fatalf("expected a multi-line table, got:\n%s", strings.Join(lines, "\n"))
	}

	// The ID column holds at most 2 chars of content ("ID"/"42") and is floored
	// at the 3-char minimum column width, so with one space of padding on each
	// side its cell is 5 wide: the first inner divider must sit 5 runes after
	// the opening border. If the narrow column had been shrunk proportionally
	// alongside the wide Description column, this divider would sit earlier.
	header := []rune(lines[1])
	idx := strings.IndexRune(string(header[1:]), '│')
	if idx != 5 {
		t.Errorf("narrow ID column was shrunk: expected first divider 5 runes in, got %d\n%s",
			idx, strings.Join(lines, "\n"))
	}
}

// TestProseWordWrapping covers the non-table half of the request: assistant
// prose should break between words, not mid-word.
func TestProseWordWrapping(t *testing.T) {
	prose := "The quick brown fox jumps over the lazy dog while the observant developer reviews the rendering pipeline."
	lines := stripAllForProbe(renderProbe(t, prose, 40))

	for i, line := range lines {
		if PrintableWidth(line) > 40 {
			t.Errorf("prose line %d overflows: %q", i, line)
		}
	}
	joined := strings.Join(lines, "\n")
	for _, word := range strings.Fields(prose) {
		if !strings.Contains(joined, word) {
			t.Errorf("prose word %q was split mid-word:\n%s", word, joined)
		}
	}
}
