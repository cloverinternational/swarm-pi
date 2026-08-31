package chat

import (
	"fmt"
	"strings"
	"testing"
)

// buildTable assembles a markdown table from a header and rows.
func buildTable(headers []string, rows ...[]string) string {
	var sb strings.Builder
	sb.WriteString("| " + strings.Join(headers, " | ") + " |\n")
	sb.WriteString("|" + strings.Repeat(" --- |", len(headers)) + "\n")
	for _, r := range rows {
		sb.WriteString("| " + strings.Join(r, " | ") + " |\n")
	}
	return sb.String()
}

// maxVisualWidth returns the widest rendered line.
func maxVisualWidth(lines []string) int {
	widest := 0
	for _, l := range lines {
		if w := PrintableWidth(l); w > widest {
			widest = w
		}
	}
	return widest
}

// TestWideTablesNeverOverflow covers tables with far more columns than the
// viewport can host. A grid physically cannot fit these (30 columns needs at
// least 121 columns of borders and padding alone), so they must degrade to the
// stacked record layout rather than spilling past the viewport edge.
func TestWideTablesNeverOverflow(t *testing.T) {
	for _, numCols := range []int{9, 15, 30} {
		headers := make([]string, numCols)
		values := make([]string, numCols)
		for c := 0; c < numCols; c++ {
			headers[c] = fmt.Sprintf("Column%d", c)
			values[c] = fmt.Sprintf("value-%d", c)
		}
		md := buildTable(headers, values)

		for _, width := range probeViewportWidths {
			t.Run(fmt.Sprintf("cols_%d/width_%d", numCols, width), func(t *testing.T) {
				lines := renderProbe(t, md, width)
				if got := maxVisualWidth(lines); got > width {
					t.Errorf("%d-column table overflows at width %d: widest line %d\n%s",
						numCols, width, got, strings.Join(stripAllForProbe(lines), "\n"))
				}
				// Content must survive the layout change.
				text := strings.Join(stripAllForProbe(lines), "\n")
				for c := 0; c < numCols; c++ {
					if !strings.Contains(text, fmt.Sprintf("value-%d", c)) {
						t.Fatalf("value-%d missing from %d-column table at width %d", c, numCols, width)
					}
				}
			})
		}
	}
}

// TestVeryLongCellContentIsFullyRendered checks that paragraph-length cells and
// long unbroken tokens are wrapped rather than dropped, at every viewport.
func TestVeryLongCellContentIsFullyRendered(t *testing.T) {
	paragraph := strings.TrimSpace(strings.Repeat("lorem ipsum dolor sit amet consectetur ", 25))
	url := "https://example.com/" + strings.Repeat("segment/", 20) + "final?query=1"
	md := buildTable(
		[]string{"Topic", "Detail"},
		[]string{"Overview", paragraph},
		[]string{"Link", url},
	)

	for _, width := range probeViewportWidths {
		t.Run(fmt.Sprintf("width_%d", width), func(t *testing.T) {
			lines := renderProbe(t, md, width)
			if got := maxVisualWidth(lines); got > width {
				t.Errorf("long-content table overflows at width %d: widest line %d", width, got)
			}

			text := strings.Join(stripAllForProbe(lines), "\n")
			if strings.Contains(text, "…") {
				t.Errorf("long content was truncated at width %d", width)
			}
			// The paragraph repeats, so count occurrences of a distinctive word
			// to prove every repetition survived the wrap.
			if got, want := strings.Count(text, "consectetur"), 25; got != want {
				t.Errorf("paragraph cell lost content at width %d: %d/%d occurrences of 'consectetur'",
					width, got, want)
			}
			// The URL is one unbreakable token, so it is split across lines and
			// a break may land mid-segment. Strip the layout (borders, padding,
			// line ends) and the original token must reappear intact.
			if !strings.Contains(compactLayout(text), url) {
				t.Errorf("URL was not preserved intact at width %d:\n%s", width, text)
			}
		})
	}
}

// compactLayout removes box-drawing characters and all whitespace so a value
// split across several physical lines can be compared against its original.
func compactLayout(s string) string {
	var sb strings.Builder
	for _, r := range s {
		switch {
		case r == '\n' || r == ' ' || r == '\t':
			continue
		case strings.ContainsRune("┌┐└┘├┤┬┴┼─│", r):
			continue
		default:
			sb.WriteRune(r)
		}
	}
	return sb.String()
}

// TestTallTableSeparatesWrappedRows verifies that once rows wrap, a rule is
// drawn between them. Without it, a long description bleeds into the next row
// and the reader cannot tell where one record ends.
func TestTallTableSeparatesWrappedRows(t *testing.T) {
	md := buildTable(
		[]string{"Case", "Meaning"},
		[]string{"First", strings.Repeat("alpha ", 12)},
		[]string{"Second", strings.Repeat("beta ", 12)},
	)
	lines := stripAllForProbe(renderProbe(t, md, 50))

	dividers := 0
	for _, l := range lines {
		if strings.HasPrefix(l, "├") {
			dividers++
		}
	}
	// One below the header, one between the two wrapped data rows.
	if dividers != 2 {
		t.Errorf("expected 2 dividers in a wrapped table, got %d:\n%s", dividers, strings.Join(lines, "\n"))
	}
}

// TestCompactTableStaysDense guards the other direction: a table whose rows all
// fit on one line must NOT gain per-row rules, which would make short reference
// tables twice as tall for no benefit.
func TestCompactTableStaysDense(t *testing.T) {
	md := buildTable(
		[]string{"Name", "Age"},
		[]string{"Alice", "30"},
		[]string{"Bob", "25"},
	)
	lines := stripAllForProbe(renderProbe(t, md, 80))

	dividers := 0
	for _, l := range lines {
		if strings.HasPrefix(l, "├") {
			dividers++
		}
	}
	if dividers != 1 {
		t.Errorf("compact table should have only the header divider, got %d:\n%s",
			dividers, strings.Join(lines, "\n"))
	}
}

// TestHugeTableRenders exercises a table with thousands of rows. Messages are
// pre-render cached, so this cost is paid once per width — but it still must
// complete promptly and preserve every row.
func TestHugeTableRenders(t *testing.T) {
	const rowCount = 2000
	var sb strings.Builder
	sb.WriteString("| ID | Name | Description | Status |\n| --- | --- | --- | --- |\n")
	for r := 0; r < rowCount; r++ {
		sb.WriteString(fmt.Sprintf("| %d | item-%d | %s | active |\n", r, r,
			"a reasonably long description that needs to wrap at typical widths"))
	}

	lines := renderProbe(t, sb.String(), 100)
	if got := maxVisualWidth(lines); got > 100 {
		t.Errorf("huge table overflows: widest line %d", got)
	}

	text := strings.Join(stripAllForProbe(lines), "\n")
	for _, r := range []int{0, rowCount / 2, rowCount - 1} {
		if !strings.Contains(text, fmt.Sprintf("item-%d ", r)) {
			t.Errorf("row item-%d missing from a %d-row table", r, rowCount)
		}
	}
}

func BenchmarkRenderHugeTable(b *testing.B) {
	var sb strings.Builder
	sb.WriteString("| ID | Name | Description | Status |\n| --- | --- | --- | --- |\n")
	for r := 0; r < 1000; r++ {
		sb.WriteString(fmt.Sprintf("| %d | item-%d | %s | active |\n", r, r,
			"a reasonably long description that needs to wrap at typical widths"))
	}
	md := sb.String()
	theme := makeTestTheme()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		renderMarkdownWithWrapping(md, 100, theme)
	}
}
