package readbg

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/chat/toolrender"
)

func strip(lines []string) string {
	var b strings.Builder
	for _, l := range lines {
		b.WriteString(ansi.Strip(l))
		b.WriteString("\n")
	}
	return b.String()
}

func TestCanRender(t *testing.T) {
	r := New()
	cases := []struct {
		name string
		want bool
	}{
		{"ReadBackgroundCommand", true},
		{"mcp__srv__ReadBackgroundCommand", true},
		{"Bash", false},
		{"Read", false},
	}
	for _, c := range cases {
		got := r.CanRender(&toolrender.RenderContext{ToolName: c.name})
		if got != c.want {
			t.Errorf("CanRender(%q) = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestRenderStatus_RunningWithHeartbeat(t *testing.T) {
	out := `{
		"task_id": "bg-1",
		"status": "running",
		"command": "go build ./...",
		"duration_seconds": 12.5,
		"output": [
			{"stream":"stdout","content":"compiling pkg a"},
			{"stream":"stdout","content":"compiling pkg b"}
		],
		"metadata": {
			"total_lines": 2,
			"seconds_since_last_output": 3,
			"bytes_written": 4096
		}
	}`
	ctx := &toolrender.RenderContext{
		ToolName: "ReadBackgroundCommand",
		Output:   out,
		Width:    80,
		BgColor:  "#000000",
	}
	text := strip(New().Render(ctx, nil))
	for _, want := range []string{"bg:running", "task_id: bg-1", "bytes", "compiling pkg b"} {
		if !strings.Contains(text, want) {
			t.Errorf("expected %q in output:\n%s", want, text)
		}
	}
}

func TestRenderStatus_CompletedWithExit(t *testing.T) {
	out := `{
		"task_id": "bg-2",
		"status": "completed",
		"command": "make test",
		"exit_code": 1,
		"output": [{"stream":"stderr","content":"FAIL"}],
		"metadata": {"total_lines": 1, "bytes_written": 10}
	}`
	ctx := &toolrender.RenderContext{
		ToolName: "ReadBackgroundCommand",
		Output:   out,
		Width:    80,
		BgColor:  "#000000",
	}
	text := strip(New().Render(ctx, nil))
	if !strings.Contains(text, "bg:completed") {
		t.Errorf("expected 'bg:completed', got:\n%s", text)
	}
	if !strings.Contains(text, "exit 1") {
		t.Errorf("expected 'exit 1', got:\n%s", text)
	}
}

func TestRenderStatus_TruncationFileHint(t *testing.T) {
	out := `{
		"task_id": "bg-3",
		"status": "running",
		"command": "cat huge",
		"output": [{"stream":"stdout","content":"line"}],
		"metadata": {"output_truncated": true, "output_file": "/tmp/out.txt", "bytes_written": 999999}
	}`
	ctx := &toolrender.RenderContext{
		ToolName: "ReadBackgroundCommand",
		Output:   out,
		Width:    100,
		BgColor:  "#000000",
	}
	text := strip(New().Render(ctx, nil))
	if !strings.Contains(text, "/tmp/out.txt") {
		t.Errorf("expected output_file hint, got:\n%s", text)
	}
}

func TestRenderList(t *testing.T) {
	out := `{
		"count": 2,
		"processes": [
			{"task_id":"a","command":"sleep 100","status":"running","duration":"1m0s"},
			{"task_id":"b","command":"npm test","status":"failed","duration":"5s"}
		]
	}`
	ctx := &toolrender.RenderContext{
		ToolName: "ReadBackgroundCommand",
		Output:   out,
		Width:    80,
		BgColor:  "#000000",
	}
	text := strip(New().Render(ctx, nil))
	for _, want := range []string{"background processes: 2", "running", "failed", "npm test"} {
		if !strings.Contains(text, want) {
			t.Errorf("expected %q in list output:\n%s", want, text)
		}
	}
}

func TestRender_ErrorResult(t *testing.T) {
	ctx := &toolrender.RenderContext{
		ToolName: "ReadBackgroundCommand",
		Output:   "",
		Error:    "task_id \"missing\" not found",
		Width:    80,
		BgColor:  "#000000",
	}
	text := strip(New().Render(ctx, nil))
	if !strings.Contains(text, "not found") {
		t.Errorf("expected error surfaced, got:\n%s", text)
	}
}

func TestRender_CancelPlainText(t *testing.T) {
	ctx := &toolrender.RenderContext{
		ToolName: "ReadBackgroundCommand",
		Output:   "Process bg-9 has been cancelled",
		Width:    80,
		BgColor:  "#000000",
	}
	text := strip(New().Render(ctx, nil))
	if !strings.Contains(text, "cancelled") {
		t.Errorf("expected cancel message, got:\n%s", text)
	}
}

// Output lines beyond the cap should be tailed with an "earlier lines" hint.
func TestRenderStatus_TailsLongOutput(t *testing.T) {
	var sb strings.Builder
	sb.WriteString(`{"task_id":"bg-x","status":"running","command":"seq","output":[`)
	for i := 0; i < 30; i++ {
		if i > 0 {
			sb.WriteString(",")
		}
		sb.WriteString(`{"stream":"stdout","content":"line`)
		sb.WriteString(string(rune('0' + i%10)))
		sb.WriteString(`"}`)
	}
	sb.WriteString(`],"metadata":{"total_lines":30,"bytes_written":100}}`)

	ctx := &toolrender.RenderContext{
		ToolName: "ReadBackgroundCommand",
		Output:   sb.String(),
		Width:    80,
		BgColor:  "#000000",
	}
	text := strip(New().Render(ctx, nil))
	if !strings.Contains(text, "earlier lines") {
		t.Errorf("expected 'earlier lines' hint for >maxOutputLines, got:\n%s", text)
	}
}
