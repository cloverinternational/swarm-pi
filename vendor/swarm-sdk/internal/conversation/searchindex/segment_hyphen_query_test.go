package searchindex

import (
	"context"
	"strings"
	"testing"
)

// TestSearchSegmentsPlainHyphenatedQueryDoesNotErrorAsColumnExpression proves
// issue #279: SearchSegments passed query.Text straight through as the raw
// FTS5 MATCH argument (unlike Search(), which already quotes each token via
// literalFTSQuery), so a plain hyphenated identifier like "claude-code" was
// parsed as FTS5's own query syntax instead of literal text and failed with
// a SQL error such as "no such column: claude" rather than matching or
// returning zero results.
func TestSearchSegmentsPlainHyphenatedQueryDoesNotErrorAsColumnExpression(t *testing.T) {
	engine := openSegmentTestSQLite(t)
	ctx := context.Background()
	segments := []Segment{
		{
			ConversationID: "conversation", WorkspacePath: "/work", Ordinal: 0,
			MessageID: "m1", Role: "tool", Kind: SegmentToolResult,
			ToolName: "Bash", CallID: "call-1", Failed: true,
			Text: "using claude-code to run the build",
		},
	}
	if err := engine.ReplaceSegments(ctx, "conversation", segments); err != nil {
		t.Fatal(err)
	}

	queries := []string{
		"claude-code",
		"chrome mcp", // near-miss: plain multi-word query must keep working
	}
	for _, q := range queries {
		t.Run(q, func(t *testing.T) {
			hits, err := engine.SearchSegments(ctx, SegmentQuery{Text: q, Limit: 10})
			if err != nil {
				t.Fatalf("SearchSegments(%q) returned an error instead of results/empty: %v", q, err)
			}
			if strings.Contains(q, "claude-code") {
				if len(hits) != 1 {
					t.Fatalf("SearchSegments(%q) = %d hits, want 1 (the hyphenated term should match literally)", q, len(hits))
				}
			}
		})
	}
}
