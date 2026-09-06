package chat

import (
	"strings"
	"testing"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/agent"
	chatui "github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/chatui"
)

func TestRenderMessageListUsesBuilderBackedContentBlocks(t *testing.T) {
	app := NewApp()

	msg := Message{
		Role: "assistant",
		OrderedBlocks: []MessageBlock{
			{
				Type:    "content",
				Content: "In",
			},
		},
	}
	msg.OrderedBlocks[0].AppendContent(" a town that never put names on its streets.")

	lines := app.renderMessageListWithContext([]Message{msg}, app.NewMessageRenderContext(220))
	rendered := stripANSI(strings.Join(lines, "\n"))

	if !strings.Contains(rendered, "In a town that never put names on its streets.") {
		t.Fatalf("expected rendered content to include appended text, got: %q", rendered)
	}
}

func TestRenderMessageListUsesBuilderBackedFallbackContent(t *testing.T) {
	app := NewApp()

	msg := Message{
		Role:    "assistant",
		Content: "In",
	}
	msg.AppendContent(" a town that never put names on its streets.")

	lines := app.renderMessageListWithContext([]Message{msg}, app.NewMessageRenderContext(220))
	rendered := stripANSI(strings.Join(lines, "\n"))

	if !strings.Contains(rendered, "In a town that never put names on its streets.") {
		t.Fatalf("expected rendered fallback content to include appended text, got: %q", rendered)
	}
}

func TestSyncMessagesToChatPanelUsesBuilderBackedFields(t *testing.T) {
	app := NewApp()
	app.chatPanel = chatui.NewPanel(120, 40)

	contentBlock := MessageBlock{
		Type:    "content",
		Content: "first",
	}
	contentBlock.AppendContent(" second")

	toolResult := ToolResultDisplay{
		CallID: "call-1",
		Output: "output",
	}
	toolResult.AppendOutput(" tail")

	msg := Message{
		Role:     "assistant",
		Content:  "base",
		Thinking: "think",
		OrderedBlocks: []MessageBlock{
			contentBlock,
			{
				Type:       "tool_result",
				ToolResult: &toolResult,
				Sequence:   2,
			},
		},
		ToolResults: []ToolResultDisplay{toolResult},
	}
	msg.AppendContent(" extended")
	msg.AppendThinking(" deeper")

	app.messages = []Message{msg}
	app.syncMessagesToChatPanel()

	panelState := app.chatPanel.State()
	if len(panelState.Messages) != 1 {
		t.Fatalf("expected 1 chat panel message, got %d", len(panelState.Messages))
	}

	got := panelState.Messages[0]
	if got.Content != "base extended" {
		t.Fatalf("expected content %q, got %q", "base extended", got.Content)
	}
	if got.Thinking != "think deeper" {
		t.Fatalf("expected thinking %q, got %q", "think deeper", got.Thinking)
	}
	if len(got.OrderedBlocks) != 2 {
		t.Fatalf("expected 2 ordered blocks, got %d", len(got.OrderedBlocks))
	}
	if got.OrderedBlocks[0].Content != "first second" {
		t.Fatalf("expected first block content %q, got %q", "first second", got.OrderedBlocks[0].Content)
	}
	if got.OrderedBlocks[1].ToolResult == nil {
		t.Fatal("expected second block tool result to be present")
	}
	if got.OrderedBlocks[1].ToolResult.Output != "output tail" {
		t.Fatalf("expected ordered block tool output %q, got %q", "output tail", got.OrderedBlocks[1].ToolResult.Output)
	}
	if len(got.ToolResults) != 1 {
		t.Fatalf("expected 1 top-level tool result, got %d", len(got.ToolResults))
	}
	if got.ToolResults[0].Output != "output tail" {
		t.Fatalf("expected top-level tool output %q, got %q", "output tail", got.ToolResults[0].Output)
	}
}

func TestQueuedFinalToolResultReplacesStreamingBuilderOutput(t *testing.T) {
	app := NewApp()

	orderedResult := &ToolResultDisplay{
		CallID:      "call-1",
		Output:      "stream",
		IsStreaming: true,
	}
	orderedResult.AppendOutput(" tail")

	topLevelResult := ToolResultDisplay{
		CallID:      "call-1",
		Output:      "stream",
		IsStreaming: true,
	}
	topLevelResult.AppendOutput(" tail")

	app.messages = []Message{
		{
			Role: "assistant",
			OrderedBlocks: []MessageBlock{
				{
					Type:       "tool_result",
					ToolResult: orderedResult,
					Sequence:   1,
				},
			},
			ToolResults: []ToolResultDisplay{topLevelResult},
		},
	}

	app.updateQueue <- agentToolResultMsg{
		callID: "call-1",
		output: "final cleaned output",
	}

	app.Update(menuTickMsg{})

	gotBlock := app.messages[0].OrderedBlocks[0].ToolResult
	if gotBlock == nil {
		t.Fatal("expected ordered block tool result to be present")
	}
	if got := gotBlock.GetOutput(); got != "final cleaned output" {
		t.Fatalf("expected ordered block output %q, got %q", "final cleaned output", got)
	}
	gotBlock.AppendOutput(" +ordered")
	if got := gotBlock.GetOutput(); got != "final cleaned output +ordered" {
		t.Fatalf("expected ordered block builder to restart from final output, got %q", got)
	}
	if gotBlock.IsStreaming {
		t.Fatal("expected ordered block tool result to stop streaming after final result")
	}

	if len(app.messages[0].ToolResults) != 1 {
		t.Fatalf("expected 1 top-level tool result, got %d", len(app.messages[0].ToolResults))
	}
	gotTopLevel := &app.messages[0].ToolResults[0]
	if got := gotTopLevel.GetOutput(); got != "final cleaned output" {
		t.Fatalf("expected top-level tool result output %q, got %q", "final cleaned output", got)
	}
	gotTopLevel.AppendOutput(" +top")
	if got := gotTopLevel.GetOutput(); got != "final cleaned output +top" {
		t.Fatalf("expected top-level builder to restart from final output, got %q", got)
	}
	if gotTopLevel.IsStreaming {
		t.Fatal("expected top-level tool result to stop streaming after final result")
	}
}

func TestQueuedFinalContentReplaceResetsContentBlockBuilder(t *testing.T) {
	app := NewApp()

	contentBlock := MessageBlock{
		Type:    "content",
		Content: "stream",
	}
	contentBlock.AppendContent(" tail")

	app.messages = []Message{
		{
			Role: "assistant",
			OrderedBlocks: []MessageBlock{
				contentBlock,
			},
		},
	}

	app.updateQueue <- agentContentUpdateMsg{
		content:  "final cleaned output",
		append:   false,
		sequence: 1,
	}

	app.Update(menuTickMsg{})

	gotBlock := &app.messages[0].OrderedBlocks[0]
	if got := gotBlock.GetContent(); got != "final cleaned output" {
		t.Fatalf("expected content block %q, got %q", "final cleaned output", got)
	}

	gotBlock.AppendContent(" +tail")
	if got := gotBlock.GetContent(); got != "final cleaned output +tail" {
		t.Fatalf("expected builder to restart from final output, got %q", got)
	}
}

func TestQueuedFinalContentReplaceTargetsMatchingSequence(t *testing.T) {
	app := NewApp()

	lateBlock := MessageBlock{
		Type:     "content",
		Content:  "late",
		Sequence: 3,
	}
	lateBlock.AppendContent(" partial")

	app.messages = []Message{
		{
			Role: "assistant",
			OrderedBlocks: []MessageBlock{
				{
					Type:     "content",
					Content:  "intro",
					Sequence: 1,
				},
				{
					Type:     "tool_call",
					Sequence: 2,
					ToolCall: &ToolCallDisplay{
						ID:   "call-1",
						Name: "bash",
					},
				},
				lateBlock,
			},
		},
	}

	app.updateQueue <- agentContentUpdateMsg{
		content:  "final follow-up",
		append:   false,
		sequence: 3,
	}

	app.Update(menuTickMsg{})

	if got := app.messages[0].OrderedBlocks[0].GetContent(); got != "intro" {
		t.Fatalf("expected first content block to remain unchanged, got %q", got)
	}

	gotBlock := &app.messages[0].OrderedBlocks[2]
	if got := gotBlock.GetContent(); got != "final follow-up" {
		t.Fatalf("expected matching content block %q, got %q", "final follow-up", got)
	}
}

func TestSubAgentFinalToolResultReusesStreamingBlock(t *testing.T) {
	app := NewApp()
	app.messages = []Message{{Role: "assistant"}}

	app.updateQueue <- agentSubAgentUpdateMsg{
		agentName: "sub-agent",
		sequence:  1,
		update: agent.ToolOutputChunk{
			ID:    "call-1",
			Chunk: "stream",
		},
	}
	app.Update(menuTickMsg{})

	app.updateQueue <- agentSubAgentUpdateMsg{
		agentName: "sub-agent",
		sequence:  1,
		update: agent.ToolResultUpdate{
			ID:     "call-1",
			Output: "final output",
		},
	}
	app.Update(menuTickMsg{})

	if len(app.messages[0].OrderedBlocks) != 1 {
		t.Fatalf("expected one sub-agent block, got %d", len(app.messages[0].OrderedBlocks))
	}
	subAgent := app.messages[0].OrderedBlocks[0].SubAgentActivity
	if subAgent == nil {
		t.Fatal("expected sub-agent activity block to be present")
	}
	if len(subAgent.Blocks) != 1 {
		t.Fatalf("expected final tool result to reuse the existing block, got %d blocks", len(subAgent.Blocks))
	}
	if subAgent.Blocks[0].ToolResult == nil {
		t.Fatal("expected sub-agent tool result block to be present")
	}
	if got := subAgent.Blocks[0].ToolResult.GetOutput(); got != "final output" {
		t.Fatalf("expected sub-agent tool output %q, got %q", "final output", got)
	}
	if subAgent.Blocks[0].ToolResult.IsStreaming {
		t.Fatal("expected sub-agent tool result to stop streaming after final update")
	}
}

func TestUpdateLimitsQueuedBatchPerInvocation(t *testing.T) {
	app := NewApp()

	for range updateQueueDrainBatchLimit + 1 {
		app.updateQueue <- notificationMsg{
			level:   "info",
			message: "queued",
		}
	}

	_, cmd := app.Update(notificationMsg{level: "info", message: "incoming"})

	if cmd == nil {
		t.Fatal("expected deferred drain command when queue batch limit is reached")
	}
	if got := len(app.updateQueue); got != 1 {
		t.Fatalf("expected one queued message to remain after capped drain, got %d", got)
	}
}

func TestSubAgentRendererUsesBuilderBackedContentInCompactSummary(t *testing.T) {
	styles := newSubAgentRenderStyles(DefaultTheme)
	renderer := NewSubAgentRenderer(120, styles)

	sa := &SubAgentDisplay{
		AgentName: "test-sub-agent",
		Blocks: []MessageBlock{
			{
				Type:    "content",
				Content: "In",
			},
		},
	}
	sa.Blocks[0].AppendContent(" a town that never put names on its streets.")

	lines := renderer.RenderSubAgent(sa, false, false, false, ">", 0)
	rendered := stripANSI(strings.Join(lines, "\n"))

	// Compact sub-agent rendering intentionally shows only the summary + status lines.
	// Builder-backed content should not corrupt that summary rendering path.
	if !strings.Contains(rendered, "@test-sub-agent") {
		t.Fatalf("expected compact sub-agent render to include agent name, got: %q", rendered)
	}
	if !strings.Contains(rendered, "Done") && !strings.Contains(rendered, "Worked") {
		t.Fatalf("expected compact sub-agent render to include completion status, got: %q", rendered)
	}
}
