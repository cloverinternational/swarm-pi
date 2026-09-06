package read

import (
	"fmt"
	"strings"
	"testing"

	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/chat/toolrender"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/chat/toolrender/fuzzcorpus"
)

// renderRead exercises BOTH paths the read renderer has: the live path
// (Render with no cache) and the pre-processed path (PreProcess then
// GetRenderedLines). They are separate implementations of the same layout, so
// a bug fixed in one can survive in the other.
func renderRead(t *testing.T, filePath, output string, width int) {
	t.Helper()
	r := New()
	ctx := &toolrender.RenderContext{
		ToolName: "Read",
		Params:   map[string]any{"file_path": filePath},
		Output:   output,
		Width:    width,
	}
	fuzzcorpus.CheckLines(t, "live", r.Render(ctx, nil), width)
	cached := r.PreProcess(ctx)
	if cached != nil {
		fuzzcorpus.CheckLines(t, "cached", cached.GetRenderedLines(), width)
	}
}

// readShapes wraps a payload in each line format the renderer parses: cat-n
// with an arrow, cat-n with a tab, hashline, and unnumbered raw content.
func readShapes(sample string) []string {
	return []string{
		sample,
		"     1→" + sample,
		"     1\t" + sample,
		"1:c0de|" + sample,
		"     1→" + sample + "\n     2→" + sample + "\n999999→" + sample,
		// Ragged: numbers with no content, content with no numbers, mixed.
		"     1→\n" + sample + "\n7:beef|\n     →x\n" + sample,
	}
}

// readPaths drives language detection, which selects the syntax highlighter.
// A different highlighter tokenizes the same bytes differently, so each one is
// its own code path over the payload.
var readPaths = []string{"", "a.go", "a.py", "a.ts", "a.json", "a.md", "a" + strings.Repeat("x", 300) + ".go"}

// TestReadNastyInput renders every corpus payload in every line format, for
// every detected language, at every width.
func TestReadNastyInput(t *testing.T) {
	for i, sample := range fuzzcorpus.Samples {
		for _, width := range fuzzcorpus.Widths {
			t.Run(fmt.Sprintf("sample_%d/width_%d", i, width), func(t *testing.T) {
				for _, shape := range readShapes(sample) {
					for _, p := range readPaths {
						renderRead(t, p, shape, width)
					}
				}
			})
		}
	}
}

// TestReadWholeCorpusAsFile renders the entire corpus as one file body, so the
// line-number gutter grows past its assumed 4-column padding.
func TestReadWholeCorpusAsFile(t *testing.T) {
	var sb strings.Builder
	for i, s := range fuzzcorpus.Samples {
		fmt.Fprintf(&sb, "%6d→%s\n", i*100000, s)
	}
	for _, width := range fuzzcorpus.Widths {
		t.Run(fmt.Sprintf("width_%d", width), func(t *testing.T) {
			renderRead(t, "big.go", sb.String(), width)
		})
	}
}

// FuzzReadRender is the native fuzz target. Run it with:
//
//	go test ./internal/chat/toolrender/read/ -run XXX -fuzz FuzzReadRender -fuzztime 30s
func FuzzReadRender(f *testing.F) {
	f.Add("a.go", "     1→package main\n     2→func main() {}", 80)
	f.Add("a.py", "     1→# 日本語のコメント", 20)
	f.Add("a.ts", "     1→\tconst x = 1;\t// tab", 5)
	f.Add("", "1:beef|\x1b[31mred\x1b[0m", 13)
	f.Add("a.json", "🎉👨‍👩‍👧‍👦", 2)

	f.Fuzz(func(t *testing.T, filePath, output string, width int) {
		if !fuzzcorpus.ValidWidth(width) ||
			!fuzzcorpus.ValidInput(filePath) || !fuzzcorpus.ValidInput(output) {
			t.Skip()
		}
		renderRead(t, filePath, output, width)
	})
}
