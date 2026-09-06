package readbg

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/chat/toolrender"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/chat/toolrender/fuzzcorpus"
)

func renderBG(output, errMsg string, width int) []string {
	r := New()
	ctx := &toolrender.RenderContext{
		ToolName: "ReadBackgroundCommand",
		Output:   output,
		Error:    errMsg,
		Width:    width,
	}
	return r.Render(ctx, r.PreProcess(ctx))
}

// bgShapes places a payload in each field the renderer reads across both
// response shapes (status and list) plus the error and non-JSON fallbacks.
// The .Output[].Content field matters most: it is verbatim process stdout, the
// most attacker-adjacent string this renderer ever handles.
func bgShapes(sample string) []string {
	q := func(s string) string { b, _ := json.Marshal(s); return string(b) }
	return []string{
		sample,
		`{"task_id":` + q(sample) + `,"status":"running","command":` + q(sample) +
			`,"duration_seconds":1.5,"output":[{"stream":"stdout","content":` + q(sample) +
			`},{"stream":"stderr","content":` + q(sample) + `}],"metadata":{"total_lines":9,` +
			`"output_truncated":true,"output_file":` + q(sample) + `,"seconds_since_last_output":3,"bytes_written":42}}`,
		`{"task_id":"t","status":"failed","command":` + q(sample) + `,"exit_code":1,"output":[]}`,
		`{"count":2,"processes":[{"task_id":` + q(sample) + `,"command":` + q(sample) +
			`,"status":"cancelled","duration":` + q(sample) + `}]}`,
		`{"processes":` + q(sample) + `}`, // list shape with a wrong-typed field
		`{`, `{"broken":`,                 // malformed JSON — exercises the raw fallback
	}
}

// TestReadBGNastyInput renders every corpus payload in every field at every
// width, through both the output and the error path.
func TestReadBGNastyInput(t *testing.T) {
	for i, sample := range fuzzcorpus.Samples {
		for _, width := range fuzzcorpus.Widths {
			t.Run(fmt.Sprintf("sample_%d/width_%d", i, width), func(t *testing.T) {
				for j, shape := range bgShapes(sample) {
					fuzzcorpus.CheckLines(t, fmt.Sprintf("shape_%d", j), renderBG(shape, "", width), width)
				}
				fuzzcorpus.CheckLines(t, "err", renderBG("", sample, width), width)
			})
		}
	}
}

// TestReadBGManyOutputLines feeds more output lines than maxOutputLines so the
// tail selection and the "earlier lines" hint are exercised, with every line a
// different hostile payload.
func TestReadBGManyOutputLines(t *testing.T) {
	q := func(s string) string { b, _ := json.Marshal(s); return string(b) }
	var outs []string
	for i, s := range fuzzcorpus.Samples {
		stream := "stdout"
		if i%2 == 1 {
			stream = "stderr"
		}
		outs = append(outs, `{"stream":"`+stream+`","content":`+q(s)+`}`)
	}
	body := `{"task_id":"t","status":"running","command":"sleep 1","output":[` +
		strings.Join(outs, ",") + `],"metadata":{"output_file":"/tmp/x.log","bytes_written":1}}`
	for _, width := range fuzzcorpus.Widths {
		t.Run(fmt.Sprintf("width_%d", width), func(t *testing.T) {
			fuzzcorpus.CheckLines(t, "many", renderBG(body, "", width), width)
		})
	}
}

// FuzzReadBGRender is the native fuzz target. Run it with:
//
//	go test ./internal/chat/toolrender/readbg/ -run XXX -fuzz FuzzReadBGRender -fuzztime 30s
func FuzzReadBGRender(f *testing.F) {
	f.Add(`{"task_id":"t","status":"running","command":"make","output":[{"stream":"stdout","content":"building"}]}`, "", 80)
	f.Add(`{"task_id":"t","status":"completed","command":"echo 日本語","output":[{"stream":"stdout","content":"日本語のテキスト"}]}`, "", 20)
	f.Add(`{"task_id":"t","status":"running","output":[{"stream":"stdout","content":"a\tb\tc"}]}`, "", 5)
	f.Add(`{"count":1,"processes":[{"task_id":"t","command":"\u001b[2J","status":"failed","duration":"1s"}]}`, "", 13)
	f.Add("Process t has been cancelled", "task not found: 🎉", 2)

	f.Fuzz(func(t *testing.T, output, errMsg string, width int) {
		if !fuzzcorpus.ValidWidth(width) ||
			!fuzzcorpus.ValidInput(output) || !fuzzcorpus.ValidInput(errMsg) {
			t.Skip()
		}
		fuzzcorpus.CheckLines(t, "fuzz", renderBG(output, errMsg, width), width)
	})
}
