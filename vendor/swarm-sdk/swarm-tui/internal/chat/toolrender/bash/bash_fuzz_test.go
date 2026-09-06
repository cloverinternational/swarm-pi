package bash

import (
	"fmt"
	"testing"

	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/chat/toolrender"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/chat/toolrender/fuzzcorpus"
)

// renderBash drives the renderer the way the app does. The bash renderer is the
// most exposed of all of them: its Output is raw process stdout, which is where
// tabs, ANSI, and control bytes actually come from in practice.
func renderBash(command, output, errMsg string, width int, active, showFull bool) []string {
	r := New()
	ctx := &toolrender.RenderContext{
		ToolName: "Bash",
		Params:   map[string]any{"command": command},
		Output:   output,
		Error:    errMsg,
		Width:    width,
		IsActive: active,
		ShowFull: showFull,
	}
	return r.Render(ctx, r.PreProcess(ctx))
}

// TestBashNastyInput sweeps the hostile corpus through the command line, the
// output body and the error body, in each of the renderer's display modes
// (completed / streaming / verbose), at every width.
func TestBashNastyInput(t *testing.T) {
	for i, sample := range fuzzcorpus.Samples {
		for _, width := range fuzzcorpus.Widths {
			t.Run(fmt.Sprintf("sample_%d/width_%d", i, width), func(t *testing.T) {
				fuzzcorpus.CheckLines(t, "cmd", renderBash(sample, "ok", "", width, false, false), width)
				fuzzcorpus.CheckLines(t, "out", renderBash("ls", sample, "", width, false, false), width)
				fuzzcorpus.CheckLines(t, "err", renderBash("ls", "", sample, width, false, false), width)
				fuzzcorpus.CheckLines(t, "streaming", renderBash(sample, sample, "", width, true, false), width)
				fuzzcorpus.CheckLines(t, "showfull", renderBash(sample, sample, sample, width, false, true), width)
			})
		}
	}
}

// TestBashBackgroundedStatus covers the separate backgrounded-status path,
// which builds its own status line from a JSON body and metadata and therefore
// does not share the main terminal layout's arithmetic.
func TestBashBackgroundedStatus(t *testing.T) {
	for i, sample := range fuzzcorpus.Samples {
		for _, width := range fuzzcorpus.Widths {
			t.Run(fmt.Sprintf("bg_%d/width_%d", i, width), func(t *testing.T) {
				r := New()
				ctx := &toolrender.RenderContext{
					ToolName: "Bash",
					Params:   map[string]any{"command": sample},
					Output:   `{"backgrounded":true,"task_id":"` + "t-1" + `"}`,
					Metadata: map[string]any{"background": true, "task_id": sample, "background_reason": "idle"},
					Width:    width,
				}
				fuzzcorpus.CheckLines(t, "bg", r.Render(ctx, nil), width)
			})
		}
	}
}

// TestBashManyLines exercises the head/tail truncation hints, which append
// their own text after the output block and so have their own overflow risk.
func TestBashManyLines(t *testing.T) {
	var out string
	for i := 0; i < 200; i++ {
		out += fuzzcorpus.Samples[i%len(fuzzcorpus.Samples)] + "\n"
	}
	for _, width := range fuzzcorpus.Widths {
		t.Run(fmt.Sprintf("width_%d", width), func(t *testing.T) {
			fuzzcorpus.CheckLines(t, "head", renderBash("ls", out, "", width, false, false), width)
			fuzzcorpus.CheckLines(t, "tail", renderBash("ls", out, "", width, true, false), width)
			fuzzcorpus.CheckLines(t, "full", renderBash("ls", out, "", width, false, true), width)
		})
	}
}

// FuzzBashRender is the native fuzz target. Run it with:
//
//	go test ./internal/chat/toolrender/bash/ -run XXX -fuzz FuzzBashRender -fuzztime 30s
func FuzzBashRender(f *testing.F) {
	f.Add("ls -la", "total 4\ndrwx  .\n", "", 80)
	f.Add("echo 日本語", "日本語のテキスト", "", 8)
	f.Add("cat f", "a\tb\tc\n\tdeep", "", 5)
	f.Add("x", "\x1b[31mred\x1b[0m\x1b[2J", "", 13)
	f.Add("", "", "command not found: \U0001F4A9", 2)

	f.Fuzz(func(t *testing.T, command, output, errMsg string, width int) {
		if !fuzzcorpus.ValidWidth(width) || !fuzzcorpus.ValidInput(command) ||
			!fuzzcorpus.ValidInput(output) || !fuzzcorpus.ValidInput(errMsg) {
			t.Skip()
		}
		fuzzcorpus.CheckLines(t, "fuzz", renderBash(command, output, errMsg, width, false, false), width)
		fuzzcorpus.CheckLines(t, "fuzz/stream", renderBash(command, output, errMsg, width, true, false), width)
	})
}
