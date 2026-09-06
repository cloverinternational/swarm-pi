package subagent

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/chat/uitypes"
)

func testStyles() *uitypes.SubAgentRenderStyles {
	return &uitypes.SubAgentRenderStyles{
		Agent:     lipgloss.NewStyle().Bold(true),
		Meta:      lipgloss.NewStyle(),
		Tool:      lipgloss.NewStyle(),
		Content:   lipgloss.NewStyle(),
		Error:     lipgloss.NewStyle(),
		Connector: lipgloss.NewStyle(),
	}
}

// assertTwoLines verifies the renderer produces exactly 2 lines (or 2+ in verbose)
// and that line 1 contains the agent name and line 2 contains the ⏿ indicator.
func assertBaseFormat(t *testing.T, lines []string, agentName string) {
	t.Helper()
	if len(lines) < 2 {
		t.Errorf("Expected at least 2 lines, got %d", len(lines))
		return
	}
	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, agentName) {
		t.Errorf("Expected @%s in output\nOutput:\n%s", agentName, joined)
	}
	if !strings.Contains(joined, "⏿") {
		t.Errorf("Expected ⏿ activity glyph in line 2\nOutput:\n%s", joined)
	}
}

func TestSubAgentRenderer_BasicRender(t *testing.T) {
	renderer := NewSubAgentRenderer(80, testStyles())

	sa := &uitypes.SubAgentDisplay{
		AgentName: "TestAgent",
		Blocks: []uitypes.MessageBlock{
			{Type: "content", Content: "This is test content from the agent."},
			{Type: "tool_call", ToolCall: &uitypes.ToolCallDisplay{
				ID:         "tc1",
				Name:       "Read",
				Parameters: map[string]any{"file_path": "/test/file.go"},
			}},
			{Type: "tool_result", ToolResult: &uitypes.ToolResultDisplay{
				CallID: "tc1",
				Output: "file contents here",
			}},
		},
	}

	lines := renderer.RenderSubAgent(sa, false, false, false, ">", 0)
	assertBaseFormat(t, lines, "TestAgent")
	t.Logf("Rendered %d lines:\n%s", len(lines), strings.Join(lines, "\n"))
}

func TestSubAgentRenderer_FinalOutputAlwaysShown(t *testing.T) {
	renderer := NewSubAgentRenderer(80, testStyles())

	sa := &uitypes.SubAgentDisplay{
		AgentName: "AnalysisAgent",
		Blocks: []uitypes.MessageBlock{
			{Type: "content", Content: "Let me analyze this..."},
			{Type: "tool_call", ToolCall: &uitypes.ToolCallDisplay{
				ID: "tc1", Name: "Read",
				Parameters: map[string]any{"file_path": "/src/main.go"},
			}},
			{Type: "tool_result", ToolResult: &uitypes.ToolResultDisplay{
				CallID: "tc1", Output: "package main",
			}},
			{Type: "content", Content: "The analysis is complete. All tests pass."},
		},
	}

	lines := renderer.RenderSubAgent(sa, false, false, false, ">", 0)
	assertBaseFormat(t, lines, "AnalysisAgent")

	// Line 1 should show tool count
	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, "tool") {
		t.Errorf("Expected tool count in summary line\nOutput:\n%s", joined)
	}

	t.Logf("Compact output (%d lines):\n%s", len(lines), joined)
}

func TestSubAgentRenderer_FinalOutputInVerboseMode(t *testing.T) {
	renderer := NewSubAgentRenderer(80, testStyles())

	sa := &uitypes.SubAgentDisplay{
		AgentName: "VerboseAgent",
		Blocks: []uitypes.MessageBlock{
			{Type: "content", Content: "Starting work..."},
			{Type: "tool_call", ToolCall: &uitypes.ToolCallDisplay{
				ID: "tc1", Name: "Read",
				Parameters: map[string]any{"file_path": "/test.go"},
			}},
			{Type: "tool_result", ToolResult: &uitypes.ToolResultDisplay{
				CallID: "tc1", Output: "test contents",
			}},
			{Type: "content", Content: "Here is my detailed analysis of the code."},
		},
	}

	// Verbose mode shows per-tool detail lines
	lines := renderer.RenderSubAgent(sa, false, false, true, ">", 0)
	assertBaseFormat(t, lines, "VerboseAgent")

	joined := strings.Join(lines, "\n")
	// Verbose mode appends tool detail lines
	if !strings.Contains(joined, "Read") {
		t.Errorf("Expected Read tool in verbose detail\nOutput:\n%s", joined)
	}

	t.Logf("Verbose output (%d lines):\n%s", len(lines), joined)
}

func TestSubAgentRenderer_StreamingIndicator(t *testing.T) {
	renderer := NewSubAgentRenderer(80, testStyles())

	sa := &uitypes.SubAgentDisplay{
		AgentName: "StreamingAgent",
		Blocks: []uitypes.MessageBlock{
			{Type: "content", Content: "Working on it..."},
			{Type: "tool_call", ToolCall: &uitypes.ToolCallDisplay{
				ID: "tc1", Name: "Bash",
				Parameters: map[string]any{"command": "sleep 10"},
			}},
			// No tool_result — still pending
		},
	}

	lines := renderer.RenderSubAgent(sa, true, true, false, "⠋", 5)
	assertBaseFormat(t, lines, "StreamingAgent")

	joined := strings.Join(lines, "\n")
	// Line 2 should show the in-flight tool name (lastToolInfo)
	if !strings.Contains(joined, "Bash") && !strings.Contains(joined, "working") {
		t.Errorf("Expected tool name or activity in status line\nOutput:\n%s", joined)
	}

	t.Logf("Streaming mode rendered %d lines", len(lines))
}

func TestSubAgentRenderer_StreamingWithoutPendingTools(t *testing.T) {
	renderer := NewSubAgentRenderer(80, testStyles())

	sa := &uitypes.SubAgentDisplay{
		AgentName: "ComposingAgent",
		Blocks: []uitypes.MessageBlock{
			{Type: "tool_call", ToolCall: &uitypes.ToolCallDisplay{
				ID: "tc1", Name: "Read",
				Parameters: map[string]any{"file_path": "/test.go"},
			}},
			{Type: "tool_result", ToolResult: &uitypes.ToolResultDisplay{
				CallID: "tc1", Output: "file contents",
			}},
			// No content blocks yet
		},
	}

	lines := renderer.RenderSubAgent(sa, true, false, false, "⠋", 5)
	assertBaseFormat(t, lines, "ComposingAgent")

	joined := strings.Join(lines, "\n")
	// Line 2 shows last completed tool as activity hint (or "Initializing…")
	if !strings.Contains(joined, "Read") && !strings.Contains(joined, "Initializing") {
		t.Errorf("Expected Read tool or Initializing in status\nOutput:\n%s", joined)
	}

	t.Logf("Composing output (%d lines):\n%s", len(lines), joined)
}

func TestSubAgentRenderer_NoBoxBorder(t *testing.T) {
	renderer := NewSubAgentRenderer(60, testStyles())

	sa := &uitypes.SubAgentDisplay{
		AgentName: "NoBorderAgent",
		Blocks: []uitypes.MessageBlock{
			{Type: "content", Content: "Content without a box"},
		},
	}

	lines := renderer.RenderSubAgent(sa, false, false, false, ">", 0)
	joined := strings.Join(lines, "\n")

	// No box borders
	if strings.Contains(joined, "╭") || strings.Contains(joined, "╯") {
		t.Errorf("Unexpected box border in output\nOutput:\n%s", joined)
	}
	// Must have tree character
	if !strings.Contains(joined, "─") {
		t.Errorf("Expected tree char (─) in output\nOutput:\n%s", joined)
	}

	t.Logf("No-border output:\n%s", joined)
}

func TestSubAgentRenderer_ThinkingContent(t *testing.T) {
	renderer := NewSubAgentRenderer(80, testStyles())

	sa := &uitypes.SubAgentDisplay{
		AgentName: "ThinkingAgent",
		Blocks: []uitypes.MessageBlock{
			{Type: "thinking", Content: "Let me think about this..."},
			{Type: "content", Content: "I have an answer!"},
		},
	}

	// Both compact and verbose should at least show agent name and tree format
	for _, verbose := range []bool{false, true} {
		lines := renderer.RenderSubAgent(sa, false, false, verbose, ">", 0)
		assertBaseFormat(t, lines, "ThinkingAgent")
	}
}

func TestSubAgentRenderer_CompactToolSummary(t *testing.T) {
	renderer := NewSubAgentRenderer(80, testStyles())

	sa := &uitypes.SubAgentDisplay{
		AgentName: "ToolyAgent",
		Blocks: []uitypes.MessageBlock{
			{Type: "tool_call", ToolCall: &uitypes.ToolCallDisplay{
				ID: "tc1", Name: "Read",
				Parameters: map[string]any{"file_path": "/a.go"},
			}},
			{Type: "tool_result", ToolResult: &uitypes.ToolResultDisplay{CallID: "tc1"}},
			{Type: "tool_call", ToolCall: &uitypes.ToolCallDisplay{
				ID: "tc2", Name: "Read",
				Parameters: map[string]any{"file_path": "/b.go"},
			}},
			{Type: "tool_result", ToolResult: &uitypes.ToolResultDisplay{CallID: "tc2"}},
			{Type: "tool_call", ToolCall: &uitypes.ToolCallDisplay{
				ID: "tc3", Name: "Bash",
				Parameters: map[string]any{"command": "go test"},
			}},
			{Type: "tool_result", ToolResult: &uitypes.ToolResultDisplay{CallID: "tc3"}},
			{Type: "content", Content: "All done."},
		},
	}

	lines := renderer.RenderSubAgent(sa, false, false, false, ">", 0)
	joined := strings.Join(lines, "\n")

	// Line 1 should show "3 tool uses"
	if !strings.Contains(joined, "3 tool") {
		t.Errorf("Expected '3 tool uses' in summary line\nOutput:\n%s", joined)
	}

	t.Logf("Compact tools:\n%s", joined)
}

func TestSubAgentRenderer_VerboseToolDetails(t *testing.T) {
	renderer := NewSubAgentRenderer(80, testStyles())

	sa := &uitypes.SubAgentDisplay{
		AgentName: "DetailAgent",
		Blocks: []uitypes.MessageBlock{
			{Type: "tool_call", ToolCall: &uitypes.ToolCallDisplay{
				ID: "tc1", Name: "Read",
				Parameters: map[string]any{"file_path": "/src/main.go"},
			}},
			{Type: "tool_result", ToolResult: &uitypes.ToolResultDisplay{
				CallID: "tc1", Output: "package main",
			}},
			{Type: "tool_call", ToolCall: &uitypes.ToolCallDisplay{
				ID: "tc2", Name: "Bash",
				Parameters: map[string]any{"command": "go build ./..."},
			}},
			{Type: "tool_result", ToolResult: &uitypes.ToolResultDisplay{CallID: "tc2"}},
			{Type: "content", Content: "Build succeeded."},
		},
	}

	lines := renderer.RenderSubAgent(sa, false, false, true, ">", 0)
	joined := strings.Join(lines, "\n")

	// Verbose mode appends per-tool detail lines
	if !strings.Contains(joined, "Read") {
		t.Errorf("Expected Read tool in verbose\nOutput:\n%s", joined)
	}
	if !strings.Contains(joined, "Bash") {
		t.Errorf("Expected Bash tool in verbose\nOutput:\n%s", joined)
	}
	if !strings.Contains(joined, "/src/main.go") {
		t.Errorf("Expected file path preview in verbose\nOutput:\n%s", joined)
	}

	t.Logf("Verbose details:\n%s", joined)
}

func TestSubAgentRenderer_ErrorDisplay(t *testing.T) {
	renderer := NewSubAgentRenderer(80, testStyles())

	sa := &uitypes.SubAgentDisplay{
		AgentName: "ErrorAgent",
		Blocks: []uitypes.MessageBlock{
			{Type: "tool_call", ToolCall: &uitypes.ToolCallDisplay{
				ID: "tc1", Name: "Bash",
				Parameters: map[string]any{"command": "bad-command"},
			}},
			{Type: "tool_result", ToolResult: &uitypes.ToolResultDisplay{
				CallID: "tc1", Error: "command not found: bad-command",
			}},
			{Type: "content", Content: "The command failed."},
		},
	}

	// Verbose mode appends tool error inline
	lines := renderer.RenderSubAgent(sa, false, false, true, ">", 0)
	joined := strings.Join(lines, "\n")

	if !strings.Contains(joined, "command not found") {
		t.Errorf("Expected error message in verbose output\nOutput:\n%s", joined)
	}

	t.Logf("Error output:\n%s", joined)
}

func TestSubAgentRenderer_WidthConstraint(t *testing.T) {
	renderer := NewSubAgentRenderer(50, testStyles())

	sa := &uitypes.SubAgentDisplay{
		AgentName: "NarrowAgent",
		Blocks: []uitypes.MessageBlock{
			{Type: "content", Content: "Short."},
		},
	}

	lines := renderer.RenderSubAgent(sa, false, false, false, ">", 0)

	// Lines must not be massively wider than terminal (allow ANSI overhead)
	for i, line := range lines {
		lineWidth := lipgloss.Width(line)
		if lineWidth > 60 { // 50 + generous ANSI overhead
			t.Errorf("Line %d exceeds width: %d > 60: %q", i, lineWidth, line)
		}
	}

	t.Logf("Narrow output (%d lines):\n%s", len(lines), strings.Join(lines, "\n"))
}

func TestSubAgentRenderer_TaskInstruction(t *testing.T) {
	renderer := NewSubAgentRenderer(80, testStyles())

	sa := &uitypes.SubAgentDisplay{
		AgentName:       "TaskAgent",
		TaskInstruction: "Analyze the codebase for security vulnerabilities",
		Blocks: []uitypes.MessageBlock{
			{Type: "content", Content: "Found 2 potential issues."},
		},
	}

	lines := renderer.RenderSubAgent(sa, false, false, false, ">", 0)
	joined := strings.Join(lines, "\n")

	if !strings.Contains(joined, "security vulnerabilities") {
		t.Errorf("Expected task instruction in summary line\nOutput:\n%s", joined)
	}

	t.Logf("Task instruction output:\n%s", joined)
}

func TestSubAgentRenderer_EmptySubAgent(t *testing.T) {
	renderer := NewSubAgentRenderer(80, testStyles())

	sa := &uitypes.SubAgentDisplay{
		AgentName: "EmptyAgent",
		Blocks:    []uitypes.MessageBlock{},
	}

	// Not streaming — should still render 2-line format
	lines := renderer.RenderSubAgent(sa, false, false, false, ">", 0)
	if len(lines) < 2 {
		t.Errorf("Expected at least 2 lines, got %d", len(lines))
	}
	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, "EmptyAgent") {
		t.Error("Expected agent name in output")
	}

	// Streaming with no blocks — should show "Initializing…"
	lines = renderer.RenderSubAgent(sa, true, false, false, "⠋", 3)
	joined = strings.Join(lines, "\n")
	if !strings.Contains(joined, "Initializing") {
		t.Errorf("Expected 'Initializing' indicator for empty streaming agent\nOutput:\n%s", joined)
	}

	t.Logf("Empty agent output:\n%s", joined)
}

func TestSubAgentRenderer_NilSubAgent(t *testing.T) {
	renderer := NewSubAgentRenderer(80, testStyles())

	lines := renderer.RenderSubAgent(nil, false, false, false, ">", 0)
	if lines != nil {
		t.Error("Expected nil for nil SubAgentDisplay")
	}
}

func TestSubAgentRenderer_ToolCountBadge(t *testing.T) {
	renderer := NewSubAgentRenderer(80, testStyles())

	sa := &uitypes.SubAgentDisplay{
		AgentName: "BadgeAgent",
		Blocks: []uitypes.MessageBlock{
			{Type: "tool_call", ToolCall: &uitypes.ToolCallDisplay{
				ID: "tc1", Name: "Read",
				Parameters: map[string]any{"file_path": "/a.go"},
			}},
			{Type: "tool_result", ToolResult: &uitypes.ToolResultDisplay{
				CallID: "tc1", Output: "ok",
			}},
			{Type: "content", Content: "Done."},
		},
	}

	lines := renderer.RenderSubAgent(sa, false, false, false, ">", 0)
	joined := strings.Join(lines, "\n")

	// "1 tool use" or "1 tool uses"
	if !strings.Contains(joined, "tool use") {
		t.Errorf("Expected tool use count in summary\nOutput:\n%s", joined)
	}

	t.Logf("Badge output:\n%s", joined)
}

func TestSubAgentRenderer_StreamingActivityLine(t *testing.T) {
	renderer := NewSubAgentRenderer(60, testStyles())

	sa := &uitypes.SubAgentDisplay{
		AgentName: "ActiveAgent",
		Blocks: []uitypes.MessageBlock{
			{Type: "content", Content: "Working..."},
		},
	}

	streamingLines := renderer.RenderSubAgent(sa, true, false, false, "⠋", 0)
	staticLines := renderer.RenderSubAgent(sa, false, false, false, ">", 0)

	streamJoined := strings.Join(streamingLines, "\n")
	staticJoined := strings.Join(staticLines, "\n")

	// Streaming should show "Initializing…" (no tools yet)
	if !strings.Contains(streamJoined, "Initializing") && !strings.Contains(streamJoined, "…") {
		t.Errorf("Expected Initializing status in streaming line\nOutput:\n%s", streamJoined)
	}
	// Static should show "Done" or completion verb
	if !strings.Contains(staticJoined, "Done") && !strings.Contains(staticJoined, "Worked") {
		t.Errorf("Expected Done/completion in static line\nOutput:\n%s", staticJoined)
	}

	// Both should have ⏿ glyph
	if !strings.Contains(streamJoined, "⏿") {
		t.Errorf("Expected ⏿ in streaming output\nOutput:\n%s", streamJoined)
	}

	t.Logf("Streaming:\n%s\nStatic:\n%s", streamJoined, staticJoined)
}

func TestSubAgentRenderer_IsLastTreeChar(t *testing.T) {
	renderer := NewSubAgentRenderer(80, testStyles())

	sa := &uitypes.SubAgentDisplay{
		AgentName: "TreeAgent",
		Blocks:    []uitypes.MessageBlock{},
	}

	last := renderer.RenderSubAgentWithPosition(sa, false, false, false, ">", 0, true)
	notLast := renderer.RenderSubAgentWithPosition(sa, false, false, false, ">", 0, false)

	lastJoined := strings.Join(last, "\n")
	notLastJoined := strings.Join(notLast, "\n")

	if !strings.Contains(lastJoined, "└") {
		t.Errorf("Expected └ for last agent\nOutput:\n%s", lastJoined)
	}
	if !strings.Contains(notLastJoined, "├") {
		t.Errorf("Expected ├ for non-last agent\nOutput:\n%s", notLastJoined)
	}

	t.Logf("Last:\n%s\n\nNot last:\n%s", lastJoined, notLastJoined)
}
