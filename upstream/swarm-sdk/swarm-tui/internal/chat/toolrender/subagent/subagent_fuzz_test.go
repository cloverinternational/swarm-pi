package subagent

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/chat/toolrender"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/chat/toolrender/fuzzcorpus"
)

func renderSub(tool, output, errMsg string, width int) []string {
	r := New()
	ctx := &toolrender.RenderContext{ToolName: tool, Output: output, Error: errMsg, Width: width}
	return r.Render(ctx, r.PreProcess(ctx))
}

// subShapes drives every dispatch branch in Render: the launch stub, the
// TaskOutput NDJSON tree, the structured "agent_id:" header form, the JSON
// pretty-printer, the TOON box-drawing detector, and the plain-text fallback.
// Each branch has its own truncation arithmetic and its own indent.
func subShapes(sample string) []struct{ Tool, Body string } {
	q := func(s string) string { b, _ := json.Marshal(s); return string(b) }
	flat := strings.ReplaceAll(sample, "\n", " ")
	return []struct{ Tool, Body string }{
		{"Subagent", `{"agent_id":` + q(sample) + `,"description":"[TASK]` + flat + `[/TASK]","message":` + q(sample) + `}`},
		{"Subagent", `{"agent_id":"a","message":` + q(sample) + `}`},
		{"TaskOutput", "status: completed\nagent_id: " + flat + "\nduration: " + flat +
			"\n───────\n" + `{"type":"tool_call","name":"Bash","params":{"command":` + q(sample) + `}}` +
			"\n" + `{"type":"content","content":` + q(sample) + `}`},
		{"TaskOutput", `{"agent_id":"a","status":"running"}`},
		{"SubagentOutput", "agent_id: " + flat + "\nstatus: running\n───────\n" + sample},
		{"Task", `{"a":{"b":[1,2,{"c":` + q(sample) + `}]}}`},
		{"Subagent", "│ " + flat + "\n├─ " + flat + "\n└─ " + flat}, // TOON detector
		{"Subagent", sample}, // plain text
	}
}

// TestSubagentNastyInput renders every corpus payload through every dispatch
// branch at every width, plus the error path.
func TestSubagentNastyInput(t *testing.T) {
	for i, sample := range fuzzcorpus.Samples {
		for _, width := range fuzzcorpus.Widths {
			t.Run(fmt.Sprintf("sample_%d/width_%d", i, width), func(t *testing.T) {
				for j, shape := range subShapes(sample) {
					fuzzcorpus.CheckLines(t, fmt.Sprintf("shape_%d", j),
						renderSub(shape.Tool, shape.Body, "", width), width)
				}
				fuzzcorpus.CheckLines(t, "err", renderSub("Subagent", "", sample, width), width)
			})
		}
	}
}

// TestSubagentLongTranscript feeds a TaskOutput transcript containing a tool
// call and a content record per corpus payload, so the action tree's connector
// glyphs and the last-message tail are driven over the whole corpus at once.
func TestSubagentLongTranscript(t *testing.T) {
	q := func(s string) string { b, _ := json.Marshal(s); return string(b) }
	var sb strings.Builder
	sb.WriteString("status: completed\nagent_id: a-1\nduration: 3m\n───────\n")
	for _, s := range fuzzcorpus.Samples {
		fmt.Fprintf(&sb, `{"type":"tool_call","name":"Read","params":{"file_path":%s}}`+"\n", q(s))
		fmt.Fprintf(&sb, `{"type":"content","content":%s}`+"\n", q(s))
	}
	for _, width := range fuzzcorpus.Widths {
		t.Run(fmt.Sprintf("width_%d", width), func(t *testing.T) {
			fuzzcorpus.CheckLines(t, "transcript", renderSub("TaskOutput", sb.String(), "", width), width)
		})
	}
}

// FuzzSubagentRender is the native fuzz target. Run it with:
//
//	go test ./internal/chat/toolrender/subagent/ -run XXX -fuzz FuzzSubagentRender -fuzztime 30s
func FuzzSubagentRender(f *testing.F) {
	f.Add("Subagent", `{"agent_id":"a","description":"[TASK]do it[/TASK]"}`, 80)
	f.Add("TaskOutput", "status: ok\nagent_id: a\n───────\n{\"type\":\"content\",\"content\":\"日本語のテキスト\"}", 20)
	f.Add("SubagentOutput", "agent_id: a\n───────\na\tb\tc", 5)
	f.Add("Task", `{"k":"\u001b[31mred\u001b[0m"}`, 13)
	f.Add("Subagent", "│┌┐└┘├┤─ 🎉", 2)

	f.Fuzz(func(t *testing.T, tool, output string, width int) {
		if !fuzzcorpus.ValidWidth(width) ||
			!fuzzcorpus.ValidInput(tool) || !fuzzcorpus.ValidInput(output) {
			t.Skip()
		}
		for _, name := range []string{"Subagent", "SubagentOutput", "Task", "TaskOutput", "BackgroundTask"} {
			fuzzcorpus.CheckLines(t, "fuzz/"+name, renderSub(name, output, "", width), width)
		}
	})
}
