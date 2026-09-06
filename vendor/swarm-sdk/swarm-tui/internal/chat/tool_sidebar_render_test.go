package chat

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/ui/palette"
	"github.com/charmbracelet/x/ansi"
)

func TestToolRenderingAtMeasuredSidebarWidth(t *testing.T) {
	layout := computeChatAreaLayout(188, 60, true)
	if layout.ChatWidth != 150 || layout.ViewportWidth != 146 {
		t.Fatalf(
			"188-column sidebar layout = chat %d, viewport %d; want 150, 146",
			layout.ChatWidth,
			layout.ViewportWidth,
		)
	}

	app := NewApp()
	message := Message{
		Role: "assistant",
		OrderedBlocks: []MessageBlock{
			{
				Type: "tool_call",
				ToolCall: &ToolCallDisplay{
					ID:   "success",
					Name: "Bash",
					Parameters: map[string]any{
						"command": "printf 'first line\\nsecond line\\nfinal outcome\\n'",
					},
				},
			},
			{
				Type: "tool_result",
				ToolResult: &ToolResultDisplay{
					CallID:   "success",
					ToolName: "Bash",
					Output:   "first line\nsecond line\nfinal outcome",
					Metadata: map[string]any{"exit_code": float64(0)},
				},
			},
			{
				Type: "tool_call",
				ToolCall: &ToolCallDisplay{
					ID:         "failure",
					Name:       "Bash",
					Parameters: map[string]any{"command": "false"},
				},
			},
			{
				Type: "tool_result",
				ToolResult: &ToolResultDisplay{
					CallID:   "failure",
					ToolName: "Bash",
					Error:    "Process exited with code 1",
					Metadata: map[string]any{"exit_code": float64(1)},
				},
			},
		},
	}

	lines := app.renderMessageListWithContext(
		[]Message{message},
		app.NewSingleMessageContext(0, layout.ViewportWidth, false),
	)
	for i, line := range lines {
		if width := ansi.StringWidth(line); width > layout.ViewportWidth {
			t.Fatalf(
				"rendered line %d width %d crosses %d-cell sidebar boundary: %q",
				i,
				width,
				layout.ViewportWidth,
				ansi.Strip(line),
			)
		}
	}

	rendered := strings.Join(lines, "\n")
	successBullet := lipgloss.NewStyle().
		Foreground(lipgloss.Color(palette.Success)).
		Bold(true).
		Render("•")
	errorBullet := lipgloss.NewStyle().
		Foreground(lipgloss.Color(palette.Error)).
		Bold(true).
		Render("•")
	mutedCommand := lipgloss.NewStyle().
		Foreground(lipgloss.Color(palette.TextMuted)).
		Render("false")

	if !strings.Contains(rendered, successBullet) {
		t.Fatal("successful tool header should use the semantic success color")
	}
	if !strings.Contains(rendered, errorBullet) {
		t.Fatal("failed tool header should use the semantic error color")
	}
	if !strings.Contains(rendered, mutedCommand) {
		t.Fatal("primary tool argument should use the readable muted color")
	}
}

func TestSidePanelToggleRewrapsCachedMessages(t *testing.T) {
	app := NewApp()
	app.width = 188
	app.height = 60
	app.showSidePanel = true
	app.messages = []Message{{Role: "assistant", Content: "cached"}}
	app.messages[0].SetPreRenderLines([]string{"cached at the old width"})

	withPanel := app.applyChatAreaLayout(true)
	if app.msgViewport.Width != 146 || withPanel.ViewportWidth != 146 {
		t.Fatalf("panel-open viewport width = %d, want 146", app.msgViewport.Width)
	}
	if !app.messages[0].IsDirty() {
		t.Fatal("opening the panel at a new width should invalidate cached message wrapping")
	}

	app.messages[0].SetPreRenderLines([]string{"cached with panel"})
	app.showSidePanel = false
	withoutPanel := app.applyChatAreaLayout(true)
	if app.msgViewport.Width != 184 || withoutPanel.ViewportWidth != 184 {
		t.Fatalf("panel-closed viewport width = %d, want 184", app.msgViewport.Width)
	}
	if !app.messages[0].IsDirty() {
		t.Fatal("closing the panel should invalidate cached message wrapping")
	}
}
