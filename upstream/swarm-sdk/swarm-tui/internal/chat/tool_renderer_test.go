package chat

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

// TestAggregatedGrepSummaryNeverEmpty guards the "empty blue Grep line" bug:
// grep tool calls without a "pattern" param (AST/symbol searches) must still
// render a visible detail instead of only the bare label.
func TestAggregatedGrepSummaryNeverEmpty(t *testing.T) {
	colors := DefaultToolColors()

	tests := []struct {
		name string
		agg  *AggregatedToolCall
		want string
	}{
		{
			name: "text pattern keeps existing format",
			agg:  &AggregatedToolCall{ToolCategory: "grep", Count: 1, Pattern: "needle"},
			want: `pattern "needle"`,
		},
		{
			name: "pattern with include keeps existing format",
			agg:  &AggregatedToolCall{ToolCategory: "grep", Count: 1, Pattern: "needle", IncludePattern: "*.go"},
			want: `pattern "needle" in *.go`,
		},
		{
			name: "no pattern single call shows generic detail",
			agg:  &AggregatedToolCall{ToolCategory: "grep", Count: 1},
			want: "1 search",
		},
		{
			name: "no pattern multiple calls shows count",
			agg:  &AggregatedToolCall{ToolCategory: "grep", Count: 3},
			want: "3 searches",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			line := ansi.Strip(RenderAggregatedToolSummary(tt.agg, 120, colors, false))
			if !strings.Contains(line, "Grep") {
				t.Fatalf("summary lost Grep label: %q", line)
			}
			if !strings.Contains(line, tt.want) {
				t.Fatalf("summary %q missing detail %q", line, tt.want)
			}
		})
	}
}
