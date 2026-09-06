package todo

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/chat/toolrender"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/chat/toolrender/fuzzcorpus"
)

func renderTodo(output string, width int) []string {
	r := New()
	ctx := &toolrender.RenderContext{ToolName: "TaskManage", Output: output, Width: width}
	return r.Render(ctx, r.PreProcess(ctx))
}

// todoShapes places a payload in every structural slot the renderer parses:
// the TaskManage batch envelope, an error message, a legacy todo array, a
// single todo object, a "todos" wrapper, and the plain-text fallback. Each has
// its own truncation arithmetic, and the JSON paths only run when the envelope
// parses, so the payload must be JSON-encoded rather than concatenated.
func todoShapes(sample string) []string {
	q := func(s string) string { b, _ := json.Marshal(s); return string(b) }
	return []string{
		sample,
		`{"status":"ok","results":[{"key":"k","op":"create","status":"succeeded","data":{"task":{"id":` +
			q(sample) + `,"content":` + q(sample) + `,"status":"in_progress","priority":"high"}}}]}`,
		`{"status":"ok","results":[{"key":"k","op":"create","status":"failed","error":{"code":"E","message":` +
			q(sample) + `}}]}`,
		`[{"id":"1","content":` + q(sample) + `,"status":"completed","priority":"low","depends_on":["2"]},` +
			`{"id":"2","content":` + q(sample) + `,"status":"pending","priority":` + q(sample) + `}]`,
		`{"id":` + q(sample) + `,"content":` + q(sample) + `,"status":` + q(sample) + `}`,
		`{"todos":[{"id":"1","content":` + q(sample) + `,"status":"pending"}]}`,
		"Updated: " + strings.ReplaceAll(sample, "\n", " ") + "\nSummary: x\n" + sample,
	}
}

// TestTodoNastyInput renders every corpus payload in every structural slot at
// every width.
func TestTodoNastyInput(t *testing.T) {
	for i, sample := range fuzzcorpus.Samples {
		for _, width := range fuzzcorpus.Widths {
			t.Run(fmt.Sprintf("sample_%d/width_%d", i, width), func(t *testing.T) {
				for j, shape := range todoShapes(sample) {
					fuzzcorpus.CheckLines(t, fmt.Sprintf("shape_%d", j), renderTodo(shape, width), width)
				}
			})
		}
	}
}

// TestTodoDeeplyNestedBatch builds one batch containing an operation per corpus
// payload, plus deliberately ragged entries (missing data, unknown status,
// duplicate ids, empty ids) that exercise the collect/order bookkeeping.
func TestTodoDeeplyNestedBatch(t *testing.T) {
	q := func(s string) string { b, _ := json.Marshal(s); return string(b) }
	var ops []string
	for i, s := range fuzzcorpus.Samples {
		ops = append(ops, fmt.Sprintf(
			`{"key":"k%d","op":"update","status":"succeeded","data":{"tasks":[{"id":%s,"content":%s,"status":%s,"priority":%s}]}}`,
			i, q(fmt.Sprintf("%d", i%3)), q(s), q([]string{"pending", "in_progress", "completed", "bogus"}[i%4]), q(s)))
	}
	ops = append(ops,
		`{"key":"x","op":"get","status":"succeeded"}`,
		`{"key":"y","op":"get","status":"failed","error":{"code":"E"}}`,
		`{"key":"z","op":"get","status":"succeeded","data":{"task":{"content":""}}}`)
	body := `{"status":"partial","results":[` + strings.Join(ops, ",") + `]}`

	for _, width := range fuzzcorpus.Widths {
		t.Run(fmt.Sprintf("width_%d", width), func(t *testing.T) {
			fuzzcorpus.CheckLines(t, "batch", renderTodo(body, width), width)
		})
	}
}

// FuzzTodoRender is the native fuzz target. Run it with:
//
//	go test ./internal/chat/toolrender/todo/ -run XXX -fuzz FuzzTodoRender -fuzztime 30s
func FuzzTodoRender(f *testing.F) {
	f.Add(`{"status":"ok","results":[{"key":"a","op":"create","status":"succeeded","data":{"task":{"id":"1","content":"do it","status":"pending"}}}]}`, 80)
	f.Add(`[{"id":"1","content":"日本語のテキスト","status":"completed"}]`, 20)
	f.Add(`{"id":"1","content":"a\tb\tc","status":"in_progress"}`, 5)
	f.Add(`{"todos":[{"id":"1","content":"\u001b[31mred\u001b[0m","status":"x"}]}`, 13)
	f.Add("not json at all\n🎉", 2)

	f.Fuzz(func(t *testing.T, output string, width int) {
		if !fuzzcorpus.ValidWidth(width) || !fuzzcorpus.ValidInput(output) {
			t.Skip()
		}
		fuzzcorpus.CheckLines(t, "fuzz", renderTodo(output, width), width)
	})
}
