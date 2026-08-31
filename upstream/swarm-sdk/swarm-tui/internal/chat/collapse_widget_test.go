package chat

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

func TestRenderToolHeaderCollapsedShowsPrimaryArgumentOnly(t *testing.T) {
	widget := NewDefaultCollapseWidget()
	state := &ToolCallState{ToolName: "Bash", CollapseLevel: CollapseLevelCollapsed}

	rendered := ansi.Strip(widget.RenderToolHeader(state, "•", map[string]any{
		"command":         "go test ./internal/chat/...",
		"timeout_seconds": 120,
		"description":     "Run chat tests",
	}, false, false))

	if !strings.Contains(rendered, "• Bash go test ./internal/chat/...") {
		t.Fatalf("collapsed header should show status, tool, and command: %q", rendered)
	}
	if strings.Contains(rendered, "timeout_seconds") || strings.Contains(rendered, "description") {
		t.Fatalf("collapsed header should hide secondary parameters: %q", rendered)
	}
}

func TestRenderToolHeaderCompactShowsSecondaryArguments(t *testing.T) {
	widget := NewDefaultCollapseWidget()
	state := &ToolCallState{ToolName: "Bash", CollapseLevel: CollapseLevelCompact}

	rendered := ansi.Strip(widget.RenderToolHeader(state, "•", map[string]any{
		"command":         "go test ./internal/chat/...",
		"timeout_seconds": 120,
	}, false, false))

	if !strings.Contains(rendered, "timeout_seconds=120") {
		t.Fatalf("compact header should disclose secondary parameters: %q", rendered)
	}
}

func TestRenderToolHeaderCollapsedShowsGenericPrimaryArgument(t *testing.T) {
	widget := NewDefaultCollapseWidget()
	state := &ToolCallState{ToolName: "Read", CollapseLevel: CollapseLevelCollapsed}

	rendered := ansi.Strip(widget.RenderToolHeader(state, "•", map[string]any{
		"file_path": "/tmp/example.go",
		"offset":    20,
	}, false, false))

	if !strings.Contains(rendered, "file_path=/tmp/example.go") {
		t.Fatalf("collapsed generic header should show its primary argument: %q", rendered)
	}
	if strings.Contains(rendered, "offset") {
		t.Fatalf("collapsed generic header should hide secondary arguments: %q", rendered)
	}
}

func TestToolResultFailedRecognizesNonZeroExitCode(t *testing.T) {
	if !toolResultFailed(&ToolResultDisplay{
		Metadata: map[string]any{"exit_code": float64(7)},
	}) {
		t.Fatal("non-zero Bash exit code should mark the tool header as failed")
	}
	if toolResultFailed(&ToolResultDisplay{
		Metadata: map[string]any{"exit_code": float64(0)},
	}) {
		t.Fatal("zero Bash exit code should remain successful")
	}
}

func TestProductionToolRenderingKeepsHeaderLeftAndCommandVisible(t *testing.T) {
	app := NewApp()
	msg := Message{
		Role: "assistant",
		OrderedBlocks: []MessageBlock{
			{
				Type: "tool_call",
				ToolCall: &ToolCallDisplay{
					ID:   "call-bash",
					Name: "Bash",
					Parameters: map[string]any{
						"command":         "printf 'first\\nlast\\n'",
						"timeout_seconds": 20,
					},
				},
			},
			{
				Type: "tool_result",
				ToolResult: &ToolResultDisplay{
					CallID:   "call-bash",
					ToolName: "Bash",
					Output:   "first\nlast",
					Metadata: map[string]any{"exit_code": float64(0)},
				},
			},
		},
	}

	lines := app.renderMessageListWithContext(
		[]Message{msg},
		app.NewSingleMessageContext(0, 80, false),
	)
	var header string
	for _, line := range lines {
		stripped := ansi.Strip(line)
		if strings.Contains(stripped, "Bash") {
			header = stripped
			break
		}
	}
	if header == "" {
		t.Fatal("expected a Bash tool header")
	}
	if len(header)-len(strings.TrimLeft(header, " ")) > 4 {
		t.Fatalf("tool header should be left-aligned with transcript content: %q", header)
	}
	if !strings.Contains(header, "printf 'first\\nlast\\n'") {
		t.Fatalf("collapsed production header should keep the command visible: %q", header)
	}
	if strings.Contains(header, "timeout_seconds") {
		t.Fatalf("collapsed production header should hide secondary parameters: %q", header)
	}
}
