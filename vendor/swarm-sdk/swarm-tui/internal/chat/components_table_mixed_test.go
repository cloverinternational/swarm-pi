package chat

import (
	"fmt"
	"strings"
	"testing"
)

// The classic asymmetric table: a two-word label beside multiple paragraphs of
// explanation. This is the shape that exposes bad column allocation, because
// the label column's needs are trivial and the prose column's are unbounded.
const (
	mixedParaOne = "The force push rewrites the remote branch history so that it exactly matches your local branch. Any commits that exist only on the remote are discarded permanently, and collaborators who have already fetched the old history will find their clones diverged the next time they pull."
	mixedParaTwo = "Prefer a merge or a rebase whenever the remote contains work you did not author. If you must force, use --force-with-lease so the push is rejected when someone else has pushed since your last fetch, which turns a silent data loss into a visible error."
)

func mixedShortAndHugeTable(separator string) string {
	return "| Term | Explanation |\n| --- | --- |\n" +
		"| Force push | " + mixedParaOne + separator + mixedParaTwo + " |\n" +
		"| Safe | Use a merge instead. |"
}

// TestShortCellBesideHugeCell checks the asymmetric case at every viewport: a
// tiny cell next to two paragraphs. Nothing may overflow, and both paragraphs
// must survive in full whether the table renders as a grid or as records.
func TestShortCellBesideHugeCell(t *testing.T) {
	for _, sep := range []struct {
		name string
		text string
	}{
		{"run_on", " "},
		{"paragraph_breaks", "<br><br>"},
	} {
		md := mixedShortAndHugeTable(sep.text)
		for _, width := range probeViewportWidths {
			t.Run(fmt.Sprintf("%s/width_%d", sep.name, width), func(t *testing.T) {
				lines := renderProbe(t, md, width)
				t.Logf("\n%s @ width=%d\n%s", sep.name, width,
					strings.Join(stripAllForProbe(lines), "\n"))

				if got := maxVisualWidth(lines); got > width {
					t.Errorf("overflow at width %d: widest line %d", width, got)
				}

				// Compare within the explanation column only: when the label
				// column itself wraps, its continuation words sit between the
				// prose fragments and would break a whole-output comparison.
				compact := compactLayout(lastColumnText(stripAllForProbe(lines)))
				for i, para := range []string{mixedParaOne, mixedParaTwo} {
					if !strings.Contains(compact, compactLayout(para)) {
						t.Errorf("paragraph %d was not rendered intact at width %d", i+1, width)
					}
				}
			})
		}
	}
}

// lastColumnText extracts the final column of a rendered grid, joined in order.
// For stacked (non-grid) output the text is returned unchanged.
func lastColumnText(lines []string) string {
	var sb strings.Builder
	for _, l := range lines {
		if !strings.HasPrefix(l, "│") {
			if !strings.HasPrefix(l, "┌") && !strings.HasPrefix(l, "├") && !strings.HasPrefix(l, "└") {
				sb.WriteString(l) // stacked record line
				sb.WriteString("\n")
			}
			continue
		}
		cells := strings.Split(strings.Trim(l, "│"), "│")
		sb.WriteString(cells[len(cells)-1])
		sb.WriteString("\n")
	}
	return sb.String()
}

// TestExplicitParagraphBreaksSurvive verifies that <br><br> becomes a real
// blank line inside the cell rather than being swallowed or printed literally.
func TestExplicitParagraphBreaksSurvive(t *testing.T) {
	lines := stripAllForProbe(renderProbe(t, mixedShortAndHugeTable("<br><br>"), 80))
	joined := strings.Join(lines, "\n")

	if strings.Contains(joined, "<br>") {
		t.Errorf("<br> leaked into the rendered output:\n%s", joined)
	}

	// A blank explanation cell must appear between the two paragraphs: find the
	// line ending paragraph one, and require an empty cell right after it.
	endOfFirst := -1
	for i, l := range lines {
		if strings.Contains(l, "next time they pull.") {
			endOfFirst = i
			break
		}
	}
	if endOfFirst < 0 || endOfFirst+1 >= len(lines) {
		t.Fatalf("could not locate the end of paragraph one:\n%s", joined)
	}
	gap := lines[endOfFirst+1]
	if strings.TrimSpace(strings.ReplaceAll(gap, "│", "")) != "" {
		t.Errorf("expected a blank line between paragraphs, got %q", gap)
	}
}

// TestWidthGoesToTheColumnThatNeedsIt is the allocation regression test. With a
// short label column beside a huge prose column, the prose column must receive
// the overwhelming majority of the width — a label column keeping padding it
// does not need while prose is squeezed into a sliver is the bug this guards.
func TestWidthGoesToTheColumnThatNeedsIt(t *testing.T) {
	md := mixedShortAndHugeTable(" ")

	for _, width := range []int{30, 40, 60, 80, 120} {
		t.Run(fmt.Sprintf("width_%d", width), func(t *testing.T) {
			lines := stripAllForProbe(renderProbe(t, md, width))
			if len(lines) == 0 || !strings.HasPrefix(lines[0], "┌") {
				t.Skip("rendered as stacked records at this width")
			}

			// Measure the two columns from the top border: ┌────┬─────────┐
			border := []rune(lines[0])
			divider := -1
			for i, r := range border {
				if r == '┬' {
					divider = i
					break
				}
			}
			if divider < 0 {
				t.Fatalf("no column divider found in %q", lines[0])
			}
			labelCol := divider - 1
			proseCol := len(border) - divider - 2

			if proseCol <= labelCol {
				t.Errorf("width %d: prose column (%d) should be wider than the label column (%d)\n%s",
					width, proseCol, labelCol, strings.Join(lines, "\n"))
			}
			// The label column never needs more than its longest line
			// ("Force push" = 10) plus padding.
			if labelCol > 12 {
				t.Errorf("width %d: label column took %d columns for 10 characters of content",
					width, labelCol)
			}
		})
	}
}
