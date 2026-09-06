package history

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/chat/toolrender"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/chat/toolrender/fuzzcorpus"
)

func renderHistory(tool, output string, width int) []string {
	r := New()
	ctx := &toolrender.RenderContext{ToolName: tool, Output: output, Width: width}
	return r.Render(ctx, r.PreProcess(ctx))
}

// historyShapes places a payload in each JSON field the renderer reads, plus
// the non-JSON fallback. Every string here is JSON-encoded, because the
// structured branches only run when the envelope parses — a concatenated
// payload would silently be tested only against the fallback.
func historyShapes(sample string) []struct{ Tool, Body string } {
	q := func(s string) string { b, _ := json.Marshal(s); return string(b) }
	return []struct{ Tool, Body string }{
		{"HistorySearch", sample},
		{"HistorySearch", `{"scope":"all","workspace_path":` + q(sample) + `,"query":` + q(sample) +
			`,"truncated":true,"results":[{"id":` + q(sample) + `,"title":` + q(sample) +
			`,"preview":` + q(sample) + `,"message_count":9,"updated_at":` + q(sample) +
			`,"workspace_path":` + q(sample) + `}]}`},
		{"HistorySearch", `{"scope":"current","query":"","results":[]}`},
		{"HistoryGet", sample},
		{"HistoryGet", `{"conversation_id":` + q(sample) + `,"title":` + q(sample) +
			`,"workspace_path":` + q(sample) + `,"truncated":true,"messages":[{"id":` + q(sample) +
			`,"role":` + q(sample) + `,"content":` + q(sample) + `}]}`},
		{"HistoryGet", `{"conversation_id":"c","messages":[]}`},
	}
}

// TestHistoryNastyInput renders every corpus payload in every field at every
// width.
func TestHistoryNastyInput(t *testing.T) {
	for i, sample := range fuzzcorpus.Samples {
		for _, width := range fuzzcorpus.Widths {
			t.Run(fmt.Sprintf("sample_%d/width_%d", i, width), func(t *testing.T) {
				for j, shape := range historyShapes(sample) {
					fuzzcorpus.CheckLines(t, fmt.Sprintf("shape_%d", j),
						renderHistory(shape.Tool, shape.Body, width), width)
				}
			})
		}
	}
}

// TestHistoryManyResults builds one search listing containing a result per
// corpus payload, so the per-result card layout is driven over the whole
// corpus in a single render.
func TestHistoryManyResults(t *testing.T) {
	q := func(s string) string { b, _ := json.Marshal(s); return string(b) }
	var results []string
	for i, s := range fuzzcorpus.Samples {
		results = append(results, fmt.Sprintf(
			`{"id":%s,"title":%s,"preview":%s,"message_count":%d,"updated_at":%s,"workspace_path":%s}`,
			q(s), q(s), q(s), i, q(s), q(s)))
	}
	body := `{"scope":"all","query":"q","truncated":true,"results":[` + strings.Join(results, ",") + `]}`
	for _, width := range fuzzcorpus.Widths {
		t.Run(fmt.Sprintf("width_%d", width), func(t *testing.T) {
			fuzzcorpus.CheckLines(t, "many", renderHistory("HistorySearch", body, width), width)
		})
	}
}

// FuzzHistoryRender is the native fuzz target. Run it with:
//
//	go test ./internal/chat/toolrender/history/ -run XXX -fuzz FuzzHistoryRender -fuzztime 30s
func FuzzHistoryRender(f *testing.F) {
	f.Add("HistorySearch", `{"scope":"current","query":"x","results":[{"id":"1","title":"T","preview":"p","message_count":3}]}`, 80)
	f.Add("HistorySearch", `{"query":"日本語","results":[{"id":"1","title":"中文字符测试内容比较长一些"}]}`, 20)
	f.Add("HistoryGet", `{"conversation_id":"c","messages":[{"id":"m","role":"user","content":"a\tb\tc"}]}`, 5)
	f.Add("HistoryGet", "not json \x1b[31mred\x1b[0m", 13)
	f.Add("HistorySearch", "🎉👨‍👩‍👧‍👦", 2)

	f.Fuzz(func(t *testing.T, tool, output string, width int) {
		if !fuzzcorpus.ValidWidth(width) ||
			!fuzzcorpus.ValidInput(tool) || !fuzzcorpus.ValidInput(output) {
			t.Skip()
		}
		fuzzcorpus.CheckLines(t, "fuzz", renderHistory("HistorySearch", output, width), width)
		fuzzcorpus.CheckLines(t, "fuzz/get", renderHistory("HistoryGet", output, width), width)
	})
}
