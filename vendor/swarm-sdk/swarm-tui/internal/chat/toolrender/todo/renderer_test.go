package todo

import (
	"strings"
	"testing"

	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/chat/toolrender"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/chat/toolrender/shared"
)

func TestCanRenderTaskManageButNotDelegationTask(t *testing.T) {
	renderer := New()
	if !renderer.CanRender(&toolrender.RenderContext{ToolName: "TaskManage"}) {
		t.Fatal("TaskManage should use the task renderer")
	}
	if renderer.CanRender(&toolrender.RenderContext{ToolName: "Task"}) {
		t.Fatal("subagent delegation Task must not use the task-management renderer")
	}
}

func TestRenderTaskBatchShowsTaskStateWithoutOperationLogs(t *testing.T) {
	output := `{"status":"partial","results":[` +
		`{"key":"create-api","op":"create","status":"succeeded","data":{"task":{"id":"17","content":"Build API","status":"pending"}}},` +
		`{"key":"list-work","op":"list","status":"succeeded","data":{"tasks":[{"id":"17","content":"Build API","status":"pending"},{"id":"18","content":"Test API","status":"in_progress"},{"id":"19","content":"Document API","status":"completed"}]}},` +
		`{"key":"bad-update","op":"update","status":"failed","error":{"code":"not_found","message":"task 99 not found","retryable":false}},` +
		`{"key":"after-failure","op":"get","status":"skipped"}` +
		`]}`

	lines := New().Render(&toolrender.RenderContext{ToolName: "TaskManage", Output: output, Width: 160}, nil)
	if len(lines) != 4 {
		t.Fatalf("expected three unique task rows and one concise error, got %d: %#v", len(lines), lines)
	}
	plain := make([]string, len(lines))
	for i, line := range lines {
		plain[i] = shared.StripANSI(line)
	}
	want := []string{
		"○ #17 Build API",
		"◉ #18 Test API",
		"✓ #19 Document API",
		"✗ task 99 not found",
	}
	for i, fragment := range want {
		if !strings.Contains(plain[i], fragment) {
			t.Errorf("line %d = %q, want fragment %q", i, plain[i], fragment)
		}
	}
	all := strings.Join(plain, "\n")
	for _, internal := range []string{"create-api", "list-work", "bad-update", "after-failure", "Task batch", "not_found"} {
		if strings.Contains(all, internal) {
			t.Errorf("rendered internal operation detail %q:\n%s", internal, all)
		}
	}
}

func TestRenderTaskBatchSupportsTerseAcknowledgementSubject(t *testing.T) {
	output := `{"status":"succeeded","results":[{"key":"create","op":"create","status":"succeeded","data":{"task":{"id":"17","subject":"Build API","status":"pending","active":false,"parent_id":""}}}]}`
	lines := New().Render(&toolrender.RenderContext{ToolName: "TaskManage", Output: output, Width: 100}, nil)
	if len(lines) != 1 || !strings.Contains(shared.StripANSI(lines[0]), "○ #17 Build API") {
		t.Fatalf("terse acknowledgement was not rendered: %#v", lines)
	}
}

func TestRenderTaskManageNeverFallsBackToRawJSON(t *testing.T) {
	output := `{"status":"succeeded","operations":[{"key":"secret-action","op":"update"}]}`
	lines := New().Render(&toolrender.RenderContext{ToolName: "TaskManage", Output: output, Width: 100}, nil)
	plain := shared.StripANSI(strings.Join(lines, "\n"))
	if !strings.Contains(plain, "Task state unavailable") {
		t.Fatalf("expected sanitized fallback, got %q", plain)
	}
	for _, raw := range []string{"secret-action", `"op"`, output} {
		if strings.Contains(plain, raw) {
			t.Fatalf("sanitized fallback leaked %q: %q", raw, plain)
		}
	}
}

func TestRenderLongStyledTaskEndsWithReset(t *testing.T) {
	output := `{"status":"succeeded","results":[{"key":"update","op":"update","status":"succeeded","data":{"task":{"id":"1","content":"A deliberately long completed task summary that must be truncated safely","status":"completed"}}}]}`
	lines := New().Render(&toolrender.RenderContext{ToolName: "TaskManage", Output: output, Width: 42}, nil)
	if len(lines) != 1 {
		t.Fatalf("expected one task row, got %#v", lines)
	}
	if !strings.HasSuffix(lines[0], shared.AnsiReset) {
		t.Fatalf("styled task row does not end in ANSI reset: %q", lines[0])
	}
	if got := shared.PrintableWidth(lines[0]); got > 40 {
		t.Fatalf("task row width = %d, want <= 40: %q", got, shared.StripANSI(lines[0]))
	}
}

func TestRenderTaskBatchHonorsCompactWidth(t *testing.T) {
	output := `{"status":"succeeded","results":[{"key":"create","op":"create","status":"succeeded","data":{"task":{"id":"1","content":"A deliberately long task summary that must be truncated","status":"pending"}}}]}`
	lines := New().Render(&toolrender.RenderContext{ToolName: "TaskManage", Output: output, Width: 48}, nil)
	if got := shared.PrintableWidth(lines[0]); got > 46 {
		t.Fatalf("task row width = %d, want <= 46: %q", got, shared.StripANSI(lines[0]))
	}
	if !strings.Contains(shared.StripANSI(lines[0]), "…") {
		t.Fatalf("expected truncated row, got %q", shared.StripANSI(lines[0]))
	}
}

func TestRenderKeepsLegacyTodoShapeFallback(t *testing.T) {
	output := `[{"id":"1","content":"Old conversation task","status":"completed","priority":"high"}]`
	lines := New().Render(&toolrender.RenderContext{ToolName: "TaskList", Output: output, Width: 100}, nil)
	if len(lines) != 1 || !strings.Contains(shared.StripANSI(lines[0]), "Old conversation task") {
		t.Fatalf("legacy todo output was not rendered: %#v", lines)
	}
}
