package write

import (
	"fmt"
	"strings"
	"testing"

	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/chat/toolrender"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/chat/toolrender/fuzzcorpus"
)

func renderWrite(filePath, content, output string, width int, overwrite bool) []string {
	r := New()
	ctx := &toolrender.RenderContext{
		ToolName: "Write",
		Params:   map[string]any{"file_path": filePath, "content": content, "overwrite": overwrite},
		Output:   output,
		Width:    width,
	}
	return r.Render(ctx, r.PreProcess(ctx))
}

// TestWriteNastyInput drives the corpus through the file path (which is echoed
// into the status line beside a two-column emoji), the file content (which is
// echoed into the numbered preview), and the raw output fallback.
func TestWriteNastyInput(t *testing.T) {
	for i, sample := range fuzzcorpus.Samples {
		for _, width := range fuzzcorpus.Widths {
			t.Run(fmt.Sprintf("sample_%d/width_%d", i, width), func(t *testing.T) {
				fuzzcorpus.CheckLines(t, "path", renderWrite(sample, "body\n", "", width, true), width)
				fuzzcorpus.CheckLines(t, "content", renderWrite("a.go", sample, "", width, false), width)
				fuzzcorpus.CheckLines(t, "nopath", renderWrite("", "", sample, width, true), width)
				fuzzcorpus.CheckLines(t, "both", renderWrite(sample, sample, sample, width, false), width)
			})
		}
	}
}

// TestWritePreviewElision writes a body longer than the 5+5 preview window, so
// the head slice, the "N more lines" hint, and the tail slice all run — with
// every previewed line a different hostile payload.
func TestWritePreviewElision(t *testing.T) {
	var sb strings.Builder
	for i := 0; i < 300; i++ {
		sb.WriteString(strings.ReplaceAll(fuzzcorpus.Samples[i%len(fuzzcorpus.Samples)], "\n", " ") + "\n")
	}
	for _, width := range fuzzcorpus.Widths {
		t.Run(fmt.Sprintf("width_%d", width), func(t *testing.T) {
			fuzzcorpus.CheckLines(t, "elided", renderWrite("big.go", sb.String(), "", width, true), width)
		})
	}
}

// FuzzWriteRender is the native fuzz target. Run it with:
//
//	go test ./internal/chat/toolrender/write/ -run XXX -fuzz FuzzWriteRender -fuzztime 30s
func FuzzWriteRender(f *testing.F) {
	f.Add("a.go", "package main\n", "Successfully created", 80)
	f.Add("日本語.go", "中文字符测试内容比较长一些\n", "", 20)
	f.Add("a.ts", "\tconst x = 1;\ta\tb\n", "", 5)
	f.Add("", "", "\x1b[31mfailed\x1b[0m", 13)
	f.Add("🎉", "👨‍👩‍👧‍👦", "", 2)

	f.Fuzz(func(t *testing.T, filePath, content, output string, width int) {
		if !fuzzcorpus.ValidWidth(width) || !fuzzcorpus.ValidInput(filePath) ||
			!fuzzcorpus.ValidInput(content) || !fuzzcorpus.ValidInput(output) {
			t.Skip()
		}
		fuzzcorpus.CheckLines(t, "fuzz", renderWrite(filePath, content, output, width, true), width)
		fuzzcorpus.CheckLines(t, "fuzz/new", renderWrite(filePath, content, output, width, false), width)
	})
}
