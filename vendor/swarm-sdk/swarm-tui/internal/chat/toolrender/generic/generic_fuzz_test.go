package generic

import (
	"fmt"
	"testing"

	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/chat/toolrender"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/chat/toolrender/fuzzcorpus"
)

// render exercises the renderer the way the app does: build a context, ask the
// renderer for lines. The generic renderer is the fallback for every unknown
// tool, so it sees the widest variety of hostile payloads of any renderer here.
func render(output, errMsg string, width int) []string {
	r := New()
	ctx := &toolrender.RenderContext{
		ToolName: "SomeUnknownTool",
		Output:   output,
		Error:    errMsg,
		Width:    width,
	}
	return r.Render(ctx, r.PreProcess(ctx))
}

// TestGenericNastyInput drives the whole hostile corpus through both the output
// path and the error path at every width.
func TestGenericNastyInput(t *testing.T) {
	for i, sample := range fuzzcorpus.Samples {
		for _, width := range fuzzcorpus.Widths {
			t.Run(fmt.Sprintf("sample_%d/width_%d", i, width), func(t *testing.T) {
				fuzzcorpus.CheckLines(t, "output", render(sample, "", width), width)
				fuzzcorpus.CheckLines(t, "error", render("", sample, width), width)
				// Both at once: the error path wins, but the combination has
				// its own branch and must not regress.
				fuzzcorpus.CheckLines(t, "both", render(sample, sample, width), width)
			})
		}
	}
}

// TestGenericRaggedOutput covers structurally degenerate output: many short
// lines (exercising the 10-line truncation hint), only newlines, and a single
// line far longer than any pane.
func TestGenericRaggedOutput(t *testing.T) {
	ragged := []string{
		"\n\n\n\n\n\n\n\n\n\n\n\n",
		"a\n\n\tb\n\n日\n\n🎉\n\n\x1b[31mc\x1b[0m\n\n" + fuzzcorpus.Samples[27],
		fuzzcorpus.Samples[27] + "\n" + fuzzcorpus.Samples[28],
	}
	for i, out := range ragged {
		for _, width := range fuzzcorpus.Widths {
			t.Run(fmt.Sprintf("ragged_%d/width_%d", i, width), func(t *testing.T) {
				fuzzcorpus.CheckLines(t, "ragged", render(out, "", width), width)
			})
		}
	}
}

// FuzzGenericRender is the native fuzz target. Run it with:
//
//	go test ./internal/chat/toolrender/generic/ -run XXX -fuzz FuzzGenericRender -fuzztime 30s
func FuzzGenericRender(f *testing.F) {
	f.Add("plain output", "", 80)
	f.Add("日本語\tタブ", "", 20)
	f.Add("", "boom: \x1b[31mfailed\x1b[0m", 13)
	f.Add("a\nb\nc\nd\ne\nf\ng\nh\ni\nj\nk\nl", "", 5)
	f.Add("🎉👨‍👩‍👧‍👦🇯🇵", "🎉", 2)

	f.Fuzz(func(t *testing.T, output, errMsg string, width int) {
		if !fuzzcorpus.ValidWidth(width) ||
			!fuzzcorpus.ValidInput(output) || !fuzzcorpus.ValidInput(errMsg) {
			t.Skip()
		}
		fuzzcorpus.CheckLines(t, "fuzz", render(output, errMsg, width), width)
	})
}
