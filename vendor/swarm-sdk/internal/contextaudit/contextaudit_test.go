package contextaudit

import (
	"bytes"
	"strings"
	"testing"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/provider"
)

func TestAnalyzeSystemPromptSplitsNamedContexts(t *testing.T) {
	system := strings.Join([]string{
		"You are a helpful agent.", // base
		`<context name="claudeMd">` + strings.Repeat("x", 100) + `</context>`,
		`<context name="gitStatus">` + strings.Repeat("y", 40) + `</context>`,
		`<available_skills>` + strings.Repeat("z", 200) + `</available_skills>`,
	}, "\n")

	comps := AnalyzeSystemPrompt(system)

	got := map[string]int{}
	for _, c := range comps {
		got[c.Label] = c.Bytes
	}
	if got["context:claudeMd"] != 100 {
		t.Errorf("claudeMd bytes = %d, want 100", got["context:claudeMd"])
	}
	if got["context:gitStatus"] != 40 {
		t.Errorf("gitStatus bytes = %d, want 40", got["context:gitStatus"])
	}
	if got["available_skills"] != 200 {
		t.Errorf("available_skills bytes = %d, want 200", got["available_skills"])
	}
	if _, ok := got["base_prompt + scaffolding"]; !ok {
		t.Errorf("expected a base_prompt remainder component, got %v", got)
	}

	// Largest-first ordering.
	for i := 1; i < len(comps); i++ {
		if comps[i-1].Bytes < comps[i].Bytes {
			t.Fatalf("components not sorted desc: %+v", comps)
		}
	}
}

func TestSplitSystemPromptPreservesContent(t *testing.T) {
	system := `<available_skills>SKILLBODY</available_skills>You are an agent.
<context name="claudeMd">CLAUDEDATA</context>tail text`

	secs := SplitSystemPrompt(system)
	got := map[string]string{}
	for _, s := range secs {
		got[s.Label] = s.Content
	}
	if got["available_skills"] != "SKILLBODY" {
		t.Errorf("available_skills content = %q", got["available_skills"])
	}
	if got["context:claudeMd"] != "CLAUDEDATA" {
		t.Errorf("claudeMd content = %q", got["context:claudeMd"])
	}
	// Base must contain the non-tagged prose, both before and after tags, and
	// must NOT contain any tagged body — nothing silently dropped or duplicated.
	base := got["base_prompt"]
	if !strings.Contains(base, "You are an agent.") || !strings.Contains(base, "tail text") {
		t.Errorf("base lost prose: %q", base)
	}
	if strings.Contains(base, "SKILLBODY") || strings.Contains(base, "CLAUDEDATA") {
		t.Errorf("base leaked tagged content: %q", base)
	}
}

func TestToolComponentsCountsSchema(t *testing.T) {
	tools := []provider.Tool{
		{Name: "small", Description: "d", Parameters: map[string]any{"type": "object"}},
		{Name: "big", Description: strings.Repeat("D", 50), Parameters: map[string]any{
			"type":       "object",
			"properties": map[string]any{"path": map[string]any{"type": "string"}},
		}},
	}
	comps := ToolComponents(tools)
	if len(comps) != 2 {
		t.Fatalf("got %d components, want 2", len(comps))
	}
	// "big" must sort first (more bytes).
	if comps[0].Label != "big" {
		t.Errorf("largest-first ordering broken: first = %q", comps[0].Label)
	}
	if comps[0].Bytes <= comps[1].Bytes {
		t.Errorf("expected big > small, got %d <= %d", comps[0].Bytes, comps[1].Bytes)
	}
}

func TestFromRequestJSONAnthropic(t *testing.T) {
	body := []byte(`{
		"model": "claude",
		"system": "base prompt <context name=\"indexMd\">INDEXDATA</context>",
		"tools": [{"name":"read","description":"reads","input_schema":{"type":"object"}}],
		"messages": [{"role":"user","content":"hello there"}]
	}`)
	rep, format, err := FromRequestJSON(body)
	if err != nil {
		t.Fatal(err)
	}
	if format != "anthropic" {
		t.Fatalf("format = %q, want anthropic", format)
	}
	if !hasComponent(rep.System, "context:indexMd") {
		t.Errorf("missing indexMd section: %+v", rep.System)
	}
	if len(rep.Tools) != 1 || rep.Tools[0].Label != "read" {
		t.Errorf("tools parsed wrong: %+v", rep.Tools)
	}
	if len(rep.Messages) != 1 {
		t.Errorf("messages = %d, want 1", len(rep.Messages))
	}
}

func TestFromRequestJSONOpenAI(t *testing.T) {
	body := []byte(`{
		"model": "gpt",
		"messages": [
			{"role":"system","content":"sys prompt here"},
			{"role":"user","content":"hi"}
		],
		"tools": [{"type":"function","function":{"name":"grep","description":"search","parameters":{"type":"object"}}}]
	}`)
	rep, format, err := FromRequestJSON(body)
	if err != nil {
		t.Fatal(err)
	}
	if format != "openai" {
		t.Fatalf("format = %q, want openai", format)
	}
	// System message must land in System, not Messages (no double counting).
	if len(rep.Messages) != 1 || !strings.Contains(rep.Messages[0].Label, "user") {
		t.Errorf("openai system message leaked into messages: %+v", rep.Messages)
	}
	total := 0
	for _, c := range rep.System {
		total += c.Bytes
	}
	if total != len("sys prompt here") {
		t.Errorf("system bytes = %d, want %d", total, len("sys prompt here"))
	}
	if len(rep.Tools) != 1 || rep.Tools[0].Label != "grep" {
		t.Errorf("openai tool parsed wrong: %+v", rep.Tools)
	}
}

func TestRenderRunsAndTotals(t *testing.T) {
	rep := Report{
		System:   AnalyzeSystemPrompt(`base<context name="a">` + strings.Repeat("x", 400) + `</context>`),
		Tools:    ToolComponents([]provider.Tool{{Name: "t", Description: "d", Parameters: map[string]any{"type": "object"}}}),
		Messages: MessageComponents([]Msg{{Role: "user", Content: "hi"}}),
	}
	var buf bytes.Buffer
	rep.Render(&buf)
	out := buf.String()
	for _, want := range []string{"SYSTEM PROMPT", "TOOL SCHEMAS", "MESSAGES", "GRAND TOTAL", "context:a"} {
		if !strings.Contains(out, want) {
			t.Errorf("render output missing %q\n%s", want, out)
		}
	}
}

func hasComponent(comps []Component, label string) bool {
	for _, c := range comps {
		if c.Label == label {
			return true
		}
	}
	return false
}
