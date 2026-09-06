package toolrender_test

import (
	"strings"
	"testing"

	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/chat/toolrender"
	bashrender "github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/chat/toolrender/bash"
	editrender "github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/chat/toolrender/edit"
	genericrender "github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/chat/toolrender/generic"
	greprender "github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/chat/toolrender/grep"
	historyrender "github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/chat/toolrender/history"
	patchrender "github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/chat/toolrender/patch"
	readrender "github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/chat/toolrender/read"
	readbgrender "github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/chat/toolrender/readbg"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/chat/toolrender/shared"
	subagentrender "github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/chat/toolrender/subagent"
	todorender "github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/chat/toolrender/todo"
	websearchrender "github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/chat/toolrender/websearch"
	writerender "github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/chat/toolrender/write"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/i18n"
)

func TestToolRendererChromeIsBilingual(t *testing.T) {
	previous := i18n.CurrentLanguage()
	t.Cleanup(func() { i18n.SetLanguage(string(previous)) })

	tests := []struct {
		name    string
		render  func() []string
		english string
		spanish string
	}{
		{"bash status", func() []string {
			return bashrender.New().Render(&toolrender.RenderContext{Params: map[string]any{"command": "printf sentinel"}, IsActive: true, Width: 80}, nil)
		}, "running...", "ejecutando..."},
		{"edit empty", func() []string {
			return editrender.New().Render(&toolrender.RenderContext{Width: 80}, nil)
		}, "(no changes)", "(sin cambios)"},
		{"generic empty", func() []string {
			return genericrender.New().Render(&toolrender.RenderContext{Width: 80}, nil)
		}, "(no output)", "(sin salida)"},
		{"grep empty", func() []string {
			return greprender.New().Render(&toolrender.RenderContext{Width: 80}, nil)
		}, "(no output)", "(sin salida)"},
		{"history empty", func() []string {
			return historyrender.New().Render(&toolrender.RenderContext{ToolName: "HistorySearch", Output: `{"query":"needle","scope":"all","results":[]}`}, nil)
		}, "no matching conversations across all workspaces", "no hay conversaciones coincidentes en ningún espacio de trabajo"},
		{"patch empty", func() []string {
			return patchrender.New().Render(&toolrender.RenderContext{Width: 80}, nil)
		}, "(empty patch)", "(parche vacío)"},
		{"read empty", func() []string {
			return readrender.New().Render(&toolrender.RenderContext{Width: 80}, nil)
		}, "(empty file)", "(archivo vacío)"},
		{"background heartbeat", func() []string {
			return readbgrender.New().Render(&toolrender.RenderContext{Width: 100, Output: `{"task_id":"task-7","status":"running","command":"sleep 9","metadata":{"seconds_since_last_output":2,"bytes_written":64}}`}, nil)
		}, "last output 2s ago", "última salida hace 2s"},
		{"subagent error", func() []string {
			return subagentrender.New().Render(&toolrender.RenderContext{Width: 80, Error: "sentinel failure"}, nil)
		}, "Error: sentinel failure", "Error: sentinel failure"},
		{"task state", func() []string {
			return todorender.New().Render(&toolrender.RenderContext{ToolName: "TaskManage", Output: "not-json", Width: 80}, nil)
		}, "Task state unavailable", "Estado de las tareas no disponible"},
		{"web search", func() []string {
			return websearchrender.New().Render(&toolrender.RenderContext{Width: 100, Output: `{"query":"needle","results":[{"title":"Result","url":"https://example.test","page_age":"2d"}]}`}, nil)
		}, "Search: needle", "Búsqueda: needle"},
		{"write created", func() []string {
			return writerender.New().Render(&toolrender.RenderContext{
				Width: 80, Output: "Successfully created new file",
				Params: map[string]any{"file_path": "/tmp/sentinel.go", "content": "package sentinel"},
			}, nil)
		}, "Created", "Creado"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			i18n.SetLanguage("en")
			if got := plain(test.render()); !strings.Contains(got, test.english) {
				t.Fatalf("English render missing %q:\n%s", test.english, got)
			}
			i18n.SetLanguage("es")
			if got := plain(test.render()); !strings.Contains(got, test.spanish) {
				t.Fatalf("Spanish render missing %q:\n%s", test.spanish, got)
			}
		})
	}
}

func TestToolRendererLeavesMachineAndDynamicContentUntranslated(t *testing.T) {
	previous := i18n.CurrentLanguage()
	t.Cleanup(func() { i18n.SetLanguage(string(previous)) })
	i18n.SetLanguage("es")

	writeOutput := plain(writerender.New().Render(&toolrender.RenderContext{
		Width: 80, Output: "Successfully created new file",
		Params: map[string]any{"file_path": "/tmp/needle.go", "content": "package needle"},
	}, nil))
	for _, unchanged := range []string{"/tmp/needle.go", "package needle"} {
		if !strings.Contains(writeOutput, unchanged) {
			t.Fatalf("write renderer changed dynamic content %q:\n%s", unchanged, writeOutput)
		}
	}

	historyOutput := plain(historyrender.New().Render(&toolrender.RenderContext{
		ToolName: "HistoryGet",
		Output:   `{"conversation_id":"conv-7","workspace_path":"/work/needle","messages":[{"id":"m-1","role":"user","content":"exact tool output"}]}`,
	}, nil))
	for _, unchanged := range []string{"conv-7", "/work/needle", "user", "exact tool output"} {
		if !strings.Contains(historyOutput, unchanged) {
			t.Fatalf("history renderer changed protocol or tool content %q:\n%s", unchanged, historyOutput)
		}
	}
}

func TestCachedRendererInvalidatesWhenLanguageChanges(t *testing.T) {
	previous := i18n.CurrentLanguage()
	t.Cleanup(func() { i18n.SetLanguage(string(previous)) })

	ctx := &toolrender.RenderContext{Width: 80, Output: `{"query":"needle","results":[]}`}
	renderer := websearchrender.New()
	i18n.SetLanguage("en")
	cached := renderer.PreProcess(ctx)
	if cached == nil || cached.NeedsRerender(ctx.Width) {
		t.Fatal("fresh English cache should be reusable")
	}
	i18n.SetLanguage("es")
	if !cached.NeedsRerender(ctx.Width) {
		t.Fatal("language switch must invalidate localized cached output")
	}
}

func plain(lines []string) string {
	return shared.StripANSI(strings.Join(lines, "\n"))
}
