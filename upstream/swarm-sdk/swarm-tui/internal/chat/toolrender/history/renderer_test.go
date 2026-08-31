package history

import (
	"strings"
	"testing"

	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/chat/toolrender"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/chat/toolrender/shared"
	websearchrender "github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/chat/toolrender/websearch"
)

const searchOutput = `{
  "workspace_path": "/work/a",
  "query": "autonomy",
  "results": [
    {"id": "20260716-1", "title": "Autonomy design", "preview": "capability manifest", "message_count": 12, "updated_at": "2026-07-16T01:00:00Z", "workspace_path": "/work/a"}
  ],
  "truncated": true
}`

func TestHistoryRendererClaimsHistoryToolsBeforeWebSearch(t *testing.T) {
	ctx := &toolrender.RenderContext{ToolName: "HistorySearch", Output: searchOutput, Width: 120}
	if !New().CanRender(ctx) {
		t.Fatal("history renderer must claim HistorySearch")
	}
	if !websearchrender.New().CanRender(ctx) {
		t.Fatal("precondition lost: websearch fallback no longer claims this output; test needs updating")
	}
}

func TestHistoryRendererShowsResultsAndProvenance(t *testing.T) {
	lines := New().Render(&toolrender.RenderContext{ToolName: "HistorySearch", Output: searchOutput, Width: 120}, nil)
	joined := shared.StripANSI(strings.Join(lines, "\n"))
	for _, want := range []string{"History: \"autonomy\"", "1 conversation", "Autonomy design", "20260716-1", "12 msgs", "list truncated"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("missing %q in rendered output:\n%s", want, joined)
		}
	}
	if strings.Contains(joined, "(no results)") {
		t.Fatalf("regression: non-empty history rendered as no results:\n%s", joined)
	}
}

func TestHistoryRendererRendersGetExcerpt(t *testing.T) {
	output := `{"conversation_id":"20260716-1","title":"Autonomy design","workspace_path":"/work/a","truncated":true,"messages":[{"id":"m1","role":"user","content":"prove the manifest works"}]}`
	lines := New().Render(&toolrender.RenderContext{ToolName: "HistoryGet", Output: output, Width: 120}, nil)
	joined := shared.StripANSI(strings.Join(lines, "\n"))
	for _, want := range []string{"Conversation 20260716-1", "user", "prove the manifest works", "excerpt truncated"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("missing %q in rendered output:\n%s", want, joined)
		}
	}
}

func TestHistoryRendererEmptyResults(t *testing.T) {
	output := `{"workspace_path":"/work/a","query":"nothing","results":[],"truncated":false}`
	lines := New().Render(&toolrender.RenderContext{ToolName: "HistorySearch", Output: output, Width: 120}, nil)
	joined := shared.StripANSI(strings.Join(lines, "\n"))
	if !strings.Contains(joined, "no matching conversations") {
		t.Fatalf("empty search should say no matching conversations:\n%s", joined)
	}
}

func TestHistoryRendererShowsWorkspacePerAllScopeResult(t *testing.T) {
	output := `{"scope":"all","workspace_path":"","query":"","results":[{"id":"a","title":"First","workspace_path":"/work/a"},{"id":"b","title":"Second","workspace_path":"/work/b"}],"truncated":false}`
	lines := New().Render(&toolrender.RenderContext{ToolName: "HistorySearch", Output: output, Width: 120}, nil)
	joined := shared.StripANSI(strings.Join(lines, "\n"))
	for _, want := range []string{"all workspaces", "/work/a", "/work/b"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("all-workspace result missing %q:\n%s", want, joined)
		}
	}
}

func TestHistoryRendererAllScopeEmptyResults(t *testing.T) {
	output := `{"scope":"all","workspace_path":"","query":"nothing","results":[],"truncated":false}`
	lines := New().Render(&toolrender.RenderContext{ToolName: "HistorySearch", Output: output, Width: 120}, nil)
	joined := shared.StripANSI(strings.Join(lines, "\n"))
	if !strings.Contains(joined, "no matching conversations across all workspaces") {
		t.Fatalf("all-workspace empty search has incorrect provenance:\n%s", joined)
	}
}
