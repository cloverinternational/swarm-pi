package subagent

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
)

// stripANSIWidth returns the visual width of a line as the terminal sees it.
func visualWidth(s string) int { return lipgloss.Width(s) }

// makeTable builds a table with n active sub-agents for layout testing.
func makeTable(n int) *SubAgentTable {
	tbl := NewSubAgentTable()
	for i := 0; i < n; i++ {
		row := tbl.GetOrCreate(
			"owner-"+string(rune('A'+i)),
			"research-agent-"+strings.Repeat("x", i), // varying-length names
		)
		row.Status = StatusStreaming
		row.IsStreaming = true
		row.CurrentTodo = "Explore the new sub-agent execution changes in swarm-sdk/agent/agent_execute.go and document the flow thoroughly"
		row.Progress = Progress{Completed: i, Total: 5}
	}
	return tbl
}

// TestRenderTable_AllLinesEqualWidth is the regression guard for the broken
// table layout: borders, separators, the title row, the column-header row and
// every data row MUST share the exact same visual width. The old renderer
// produced three different widths (borders=width, separators=width+2,
// data rows=width+6), which is what made the table look broken.
func TestRenderTable_AllLinesEqualWidth(t *testing.T) {
	widths := []int{60, 80, 100, 120, 200}
	for _, w := range widths {
		tbl := makeTable(5) // 3+ → table view
		out := tbl.renderTable(w, "⠋", 1)
		lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
		if len(lines) < 5 {
			t.Fatalf("width=%d: expected at least 5 lines, got %d:\n%s", w, len(lines), out)
		}

		want := visualWidth(lines[0]) // top border defines the canonical width
		for i, ln := range lines {
			if got := visualWidth(ln); got != want {
				t.Errorf("width=%d: line %d visual width = %d, want %d\nline: %q\nfull:\n%s",
					w, i, got, want, ln, out)
			}
		}

		// The table must fit within the requested terminal width.
		if want > w {
			t.Errorf("width=%d: table width %d exceeds terminal width", w, want)
		}
	}
}

// TestRenderTable_BordersWellFormed checks that the box-drawing structure is
// intact: matching corner glyphs and column junctions on the rules.
func TestRenderTable_BordersWellFormed(t *testing.T) {
	tbl := makeTable(4)
	out := tbl.renderTable(100, "⠋", 0)
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")

	first := lines[0]
	last := lines[len(lines)-1]
	if !strings.HasPrefix(first, "┌") || !strings.HasSuffix(first, "┐") {
		t.Errorf("top border malformed: %q", first)
	}
	if !strings.HasPrefix(last, "└") || !strings.HasSuffix(last, "┘") {
		t.Errorf("bottom border malformed: %q", last)
	}
	// The header/body separators carry column junctions.
	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, "┬") || !strings.Contains(joined, "┼") || !strings.Contains(joined, "┴") {
		t.Errorf("expected ┬, ┼ and ┴ junctions in table:\n%s", out)
	}
}

// TestRenderTable_NarrowDoesNotPanic guards the title-padding math that
// previously could compute a negative repeat count and panic.
func TestRenderTable_NarrowDoesNotPanic(t *testing.T) {
	for _, w := range []int{10, 20, 30, 40, 50} {
		tbl := makeTable(3)
		_ = tbl.renderTable(w, "⠋", 0) // must not panic
	}
}
