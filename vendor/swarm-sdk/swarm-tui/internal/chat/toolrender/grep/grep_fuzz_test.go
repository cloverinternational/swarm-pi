package grep

import (
	"fmt"
	"strings"
	"testing"

	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/chat/toolrender"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/chat/toolrender/fuzzcorpus"
)

func renderGrep(output string, width int) []string {
	r := New()
	ctx := &toolrender.RenderContext{ToolName: "Grep", Output: output, Width: width}
	return r.Render(ctx, r.PreProcess(ctx))
}

// grepShapes wraps a payload in each structural position the grep parser
// recognises. The parser is a line-prefix state machine, so a hostile payload
// behaves completely differently depending on which branch it lands in — a
// bare payload takes the plain-text fallback, the same payload after "L12: "
// takes the match branch with its own truncation arithmetic.
func grepShapes(sample string) []string {
	return []string{
		sample,
		"Found 3 matches for " + sample,
		"File: " + sample + "\nL12: hit\n---",
		"File: /tmp/f.go\nL12: " + sample,
		"File: /tmp/f.go\n42:a3f1|" + sample,
		"Note: " + sample,
		"Warning: " + sample,
		"No matches found for " + sample,
		// Ragged: headers with no body, bodies with no header, stray separators.
		"File: " + sample + "\n---\n---\nL: \n" + sample + "\nFile:\n",
	}
}

// TestGrepNastyInput renders every corpus payload in every structural position
// at every width.
func TestGrepNastyInput(t *testing.T) {
	for i, sample := range fuzzcorpus.Samples {
		for _, width := range fuzzcorpus.Widths {
			t.Run(fmt.Sprintf("sample_%d/width_%d", i, width), func(t *testing.T) {
				for j, shape := range grepShapes(sample) {
					fuzzcorpus.CheckLines(t, fmt.Sprintf("shape_%d", j), renderGrep(shape, width), width)
				}
			})
		}
	}
}

// TestGrepDeeplyRaggedResults builds a large result set mixing every corpus
// payload across every branch, so the per-line state machine is driven through
// many transitions in a single render.
func TestGrepDeeplyRaggedResults(t *testing.T) {
	var sb strings.Builder
	sb.WriteString("Found 999 matches for " + fuzzcorpus.Samples[3] + "\n")
	for i, s := range fuzzcorpus.Samples {
		sb.WriteString("File: " + s + "\n")
		sb.WriteString(fmt.Sprintf("L%d: %s\n", i, s))
		sb.WriteString(fmt.Sprintf("%d:beef|%s\n", i, s))
		sb.WriteString("---\n")
	}
	for _, width := range fuzzcorpus.Widths {
		t.Run(fmt.Sprintf("width_%d", width), func(t *testing.T) {
			fuzzcorpus.CheckLines(t, "ragged", renderGrep(sb.String(), width), width)
		})
	}
}

// FuzzGrepRender is the native fuzz target. Run it with:
//
//	go test ./internal/chat/toolrender/grep/ -run XXX -fuzz FuzzGrepRender -fuzztime 30s
func FuzzGrepRender(f *testing.F) {
	f.Add("Found 2 matches\nFile: /a/b.go\nL10: func main()", 80)
	f.Add("File: 日本語.go\nL1: 中文字符测试内容比较长一些", 20)
	f.Add("L1: a\tb\tc", 5)
	f.Add("42:dead|\x1b[31mred\x1b[0m", 13)
	f.Add("File:\n---\nL:", 2)

	f.Fuzz(func(t *testing.T, output string, width int) {
		if !fuzzcorpus.ValidWidth(width) || !fuzzcorpus.ValidInput(output) {
			t.Skip()
		}
		fuzzcorpus.CheckLines(t, "fuzz", renderGrep(output, width), width)
	})
}
