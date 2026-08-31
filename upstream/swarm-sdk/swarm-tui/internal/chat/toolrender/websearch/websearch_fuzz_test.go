package websearch

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/chat/toolrender"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/chat/toolrender/fuzzcorpus"
)

// renderWS exercises both the live path and the cached path, which share
// renderFromOutput but wrap it differently.
func renderWS(t *testing.T, output string, width int) {
	t.Helper()
	r := New()
	ctx := &toolrender.RenderContext{ToolName: "WebSearch", Output: output, Width: width}
	fuzzcorpus.CheckLines(t, "live", r.Render(ctx, nil), width)
	if cached := r.PreProcess(ctx); cached != nil {
		fuzzcorpus.CheckLines(t, "cached", cached.GetRenderedLines(), width)
	}
}

// wsShapes places a payload in every field the renderer reads. Web-search
// results are third-party HTML-derived text — the single most attacker-
// controlled input any of these renderers receives.
func wsShapes(sample string) []string {
	q := func(s string) string { b, _ := json.Marshal(s); return string(b) }
	return []string{
		sample,
		`{"query":` + q(sample) + `,"results":[{"title":` + q(sample) + `,"url":` + q(sample) +
			`,"encrypted_content":` + q(sample) + `,"page_age":` + q(sample) + `}]}`,
		`{"query":"q","results":[{"url":` + q(sample) + `}]}`,
		`{"query":"q","response":` + q(sample) + `}`,
		`{"query":"q","results":[]}`,
		`{"query":"q","results":` + q(sample) + `}`, // wrong-typed results field
		`{"results":[` + q(sample) + `]}`,           // result entries that are not objects
	}
}

// TestWebSearchNastyInput renders every corpus payload in every field at every
// width.
func TestWebSearchNastyInput(t *testing.T) {
	for i, sample := range fuzzcorpus.Samples {
		for _, width := range fuzzcorpus.Widths {
			t.Run(fmt.Sprintf("sample_%d/width_%d", i, width), func(t *testing.T) {
				for _, shape := range wsShapes(sample) {
					renderWS(t, shape, width)
				}
			})
		}
	}
}

// TestWebSearchManyResults builds one response containing a card per corpus
// payload, with multi-line snippets so the ├/└ connector selection runs over
// every payload.
func TestWebSearchManyResults(t *testing.T) {
	q := func(s string) string { b, _ := json.Marshal(s); return string(b) }
	var results []string
	for _, s := range fuzzcorpus.Samples {
		results = append(results, `{"title":`+q(s)+`,"url":`+q("https://x/"+s)+
			`,"encrypted_content":`+q(s+"\n"+s+"\n"+s)+`,"page_age":`+q(s)+`}`)
	}
	body := `{"query":"q","results":[` + strings.Join(results, ",") + `]}`
	for _, width := range fuzzcorpus.Widths {
		t.Run(fmt.Sprintf("width_%d", width), func(t *testing.T) {
			renderWS(t, body, width)
		})
	}
}

// FuzzWebSearchRender is the native fuzz target. Run it with:
//
//	go test ./internal/chat/toolrender/websearch/ -run XXX -fuzz FuzzWebSearchRender -fuzztime 30s
func FuzzWebSearchRender(f *testing.F) {
	f.Add(`{"query":"go generics","results":[{"title":"T","url":"https://x","encrypted_content":"snippet"}]}`, 80)
	f.Add(`{"query":"日本語","results":[{"title":"中文字符测试内容比较长一些","url":"https://例え.jp"}]}`, 20)
	f.Add(`{"query":"q","results":[{"title":"a\tb\tc"}]}`, 5)
	f.Add(`{"query":"\u001b[2J","response":"\u001b[31mred\u001b[0m"}`, 13)
	f.Add("not json 🎉👨‍👩‍👧‍👦", 2)

	f.Fuzz(func(t *testing.T, output string, width int) {
		if !fuzzcorpus.ValidWidth(width) || !fuzzcorpus.ValidInput(output) {
			t.Skip()
		}
		renderWS(t, output, width)
	})
}
