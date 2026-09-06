package patch

import (
	"fmt"
	"strings"
	"testing"

	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/chat/toolrender"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/chat/toolrender/fuzzcorpus"
)

// renderPatch exercises both the live path (renderPatchOutput) and the cached
// path (PreProcess + GetRenderedLines), which duplicate the same layout logic.
func renderPatch(t *testing.T, output string, width int) {
	t.Helper()
	r := New()
	ctx := &toolrender.RenderContext{ToolName: "apply_patch", Output: output, Width: width}
	fuzzcorpus.CheckLines(t, "live", r.Render(ctx, nil), width)
	if cached := r.PreProcess(ctx); cached != nil {
		fuzzcorpus.CheckLines(t, "cached", cached.GetRenderedLines(), width)
	}
}

// patchShapes places a payload in each V4A position the parser branches on:
// as a file path in a header, as an addition, as a deletion, as a context line,
// and as an unrecognised line. Each branch has its own truncation arithmetic.
func patchShapes(sample string) []string {
	flat := strings.ReplaceAll(sample, "\n", " ")
	return []string{
		sample,
		"*** Begin Patch\n*** Update File: " + flat + "\n+" + flat + "\n-" + flat + "\n " + flat + "\n*** End Patch",
		"*** Add File: " + flat + "\n+" + flat,
		"*** Delete File: " + flat,
		"+" + sample,
		"-" + sample,
		" " + sample,
		// Ragged: markers with no bodies, bodies with no markers, truncated
		// patch, and a header buried mid-stream.
		"*** Begin Patch\n*** End Patch\n+\n-\n \n*** Update File:\n" + sample + "\n*** Begin Patch",
	}
}

// TestPatchNastyInput renders every corpus payload in every V4A position at
// every width.
func TestPatchNastyInput(t *testing.T) {
	for i, sample := range fuzzcorpus.Samples {
		for _, width := range fuzzcorpus.Widths {
			t.Run(fmt.Sprintf("sample_%d/width_%d", i, width), func(t *testing.T) {
				for j, shape := range patchShapes(sample) {
					t.Run(fmt.Sprintf("shape_%d", j), func(t *testing.T) {
						renderPatch(t, shape, width)
					})
				}
			})
		}
	}
}

// TestPatchDeeplyNested builds a multi-file patch out of the whole corpus so
// section handling and the first-line connector are exercised across many
// transitions in one render.
func TestPatchDeeplyNested(t *testing.T) {
	var sb strings.Builder
	sb.WriteString("*** Begin Patch\n")
	for _, s := range fuzzcorpus.Samples {
		flat := strings.ReplaceAll(s, "\n", " ")
		sb.WriteString("*** Update File: " + flat + "\n")
		sb.WriteString("@@ " + flat + "\n")
		sb.WriteString(" " + flat + "\n-" + flat + "\n+" + flat + "\n")
	}
	sb.WriteString("*** End Patch\n")
	for _, width := range fuzzcorpus.Widths {
		t.Run(fmt.Sprintf("width_%d", width), func(t *testing.T) {
			renderPatch(t, sb.String(), width)
		})
	}
}

// FuzzPatchRender is the native fuzz target. Run it with:
//
//	go test ./internal/chat/toolrender/patch/ -run XXX -fuzz FuzzPatchRender -fuzztime 30s
func FuzzPatchRender(f *testing.F) {
	f.Add("*** Begin Patch\n*** Update File: a.go\n-old\n+new\n*** End Patch", 80)
	f.Add("*** Add File: 日本語.go\n+中文字符测试内容比较长一些", 20)
	f.Add("+a\tb\tc\n- \tx", 5)
	f.Add("*** Update File: \x1b[31mred\x1b[0m\n+\x1b[2J", 13)
	f.Add("+🎉👨‍👩‍👧‍👦", 2)

	f.Fuzz(func(t *testing.T, output string, width int) {
		if !fuzzcorpus.ValidWidth(width) || !fuzzcorpus.ValidInput(output) {
			t.Skip()
		}
		renderPatch(t, output, width)
	})
}
