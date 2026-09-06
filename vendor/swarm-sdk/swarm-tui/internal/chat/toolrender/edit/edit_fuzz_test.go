package edit

import (
	"fmt"
	"strings"
	"testing"

	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/chat/toolrender"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/chat/toolrender/fuzzcorpus"
)

// renderEdit exercises both the live diff path and the pre-processed cache
// path. They are separate implementations of the same layout (renderDiffOutput
// vs processDiffLine), so a fix in one can silently miss the other.
func renderEdit(t *testing.T, filePath, oldC, newC string, width int) {
	t.Helper()
	r := New()
	ctx := &toolrender.RenderContext{
		ToolName: "Edit",
		Params:   map[string]any{"file_path": filePath, "old_string": oldC, "new_string": newC},
		Width:    width,
	}
	fuzzcorpus.CheckLines(t, "live", r.Render(ctx, nil), width)
	if cached := r.PreProcess(ctx); cached != nil {
		fuzzcorpus.CheckLines(t, "cached", cached.GetRenderedLines(), width)
	}

	// Hashline path: old/new arrive via metadata rather than params.
	mctx := &toolrender.RenderContext{
		ToolName: "Edit",
		Metadata: map[string]any{"old_content": oldC, "new_content": newC},
		Width:    width,
	}
	fuzzcorpus.CheckLines(t, "meta", r.Render(mctx, nil), width)
}

// TestEditNastyInput diffs every corpus payload against ordinary text and
// against itself-plus-a-change, at every width. Both sides of the diff matter:
// the differ's line splitting and the renderer's per-line truncation are
// independent sources of overflow.
func TestEditNastyInput(t *testing.T) {
	for i, sample := range fuzzcorpus.Samples {
		for _, width := range fuzzcorpus.Widths {
			t.Run(fmt.Sprintf("sample_%d/width_%d", i, width), func(t *testing.T) {
				renderEdit(t, "a.go", "ordinary line\n", sample, width)
				renderEdit(t, "a.go", sample, "ordinary line\n", width)
				renderEdit(t, sample, sample, sample+"\nchanged", width)
				renderEdit(t, "", "", sample, width)
			})
		}
	}
}

// TestEditManyHunks builds a diff whose every line is a different hostile
// payload, so line-number width grows and each branch (insert/delete/context)
// is driven over the whole corpus in one render.
func TestEditManyHunks(t *testing.T) {
	var oldSB, newSB strings.Builder
	for i, s := range fuzzcorpus.Samples {
		oldSB.WriteString(strings.ReplaceAll(s, "\n", " ") + "\n")
		if i%2 == 0 {
			newSB.WriteString(strings.ReplaceAll(s, "\n", " ") + " CHANGED\n")
		} else {
			newSB.WriteString(strings.ReplaceAll(s, "\n", " ") + "\n")
		}
	}
	for _, width := range fuzzcorpus.Widths {
		t.Run(fmt.Sprintf("width_%d", width), func(t *testing.T) {
			renderEdit(t, "big.go", oldSB.String(), newSB.String(), width)
		})
	}
}

// FuzzEditRender is the native fuzz target. Run it with:
//
//	go test ./internal/chat/toolrender/edit/ -run XXX -fuzz FuzzEditRender -fuzztime 30s
func FuzzEditRender(f *testing.F) {
	f.Add("a.go", "func a() {}", "func b() {}", 80)
	f.Add("a.py", "日本語のテキスト", "中文字符测试内容比较长一些", 20)
	f.Add("a.ts", "a\tb\tc", "a\t\tb", 5)
	f.Add("", "\x1b[31mred\x1b[0m", "\x1b[2J", 13)
	f.Add("x", "", "🎉👨‍👩‍👧‍👦", 2)

	f.Fuzz(func(t *testing.T, filePath, oldC, newC string, width int) {
		if !fuzzcorpus.ValidWidth(width) || !fuzzcorpus.ValidInput(filePath) ||
			!fuzzcorpus.ValidInput(oldC) || !fuzzcorpus.ValidInput(newC) {
			t.Skip()
		}
		renderEdit(t, filePath, oldC, newC, width)
	})
}
