package chatui_test

import (
	"testing"
	"time"

	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/chatui"
)

func TestNewPanel(t *testing.T) {
	panel := chatui.NewPanel(80, 24)

	if panel == nil {
		t.Fatal("NewPanel returned nil")
	}

	state := panel.State()
	if state == nil {
		t.Fatal("State returned nil")
	}

	if state.Viewport.Width != 80 {
		t.Errorf("Expected width 80, got %d", state.Viewport.Width)
	}

	if state.Viewport.Height != 24 {
		t.Errorf("Expected height 24, got %d", state.Viewport.Height)
	}
}

func TestPanelWithOptions(t *testing.T) {
	panel := chatui.NewPanel(80, 24,
		chatui.WithShowThinking(true),
		chatui.WithShowFullToolOutput(true),
	)

	state := panel.State()

	if !state.ShowThinking {
		t.Error("ShowThinking should be true")
	}

	if !state.ShowFullToolOutput {
		t.Error("ShowFullToolOutput should be true")
	}
}

func TestMessageTypes(t *testing.T) {
	// Test that types are properly exported
	msg := chatui.Message{
		Role:      "assistant",
		Content:   "Hello, world!",
		Timestamp: time.Now(),
		OrderedBlocks: []chatui.MessageBlock{
			{
				Type:    chatui.BlockContent,
				Content: "Test content",
			},
		},
	}

	if msg.Role != "assistant" {
		t.Error("Message role not set correctly")
	}

	if len(msg.OrderedBlocks) != 1 {
		t.Error("OrderedBlocks not set correctly")
	}

	if msg.OrderedBlocks[0].Type != chatui.BlockContent {
		t.Error("Block type not set correctly")
	}
}

func TestBlockTypes(t *testing.T) {
	// Verify block type constants are exported
	tests := []struct {
		blockType chatui.BlockType
		want      int
	}{
		{chatui.BlockThinking, 0},
		{chatui.BlockContent, 1},
		{chatui.BlockToolCall, 2},
		{chatui.BlockToolResult, 3},
		{chatui.BlockHook, 4},
		{chatui.BlockSubAgent, 5},
	}

	for _, tt := range tests {
		if int(tt.blockType) != tt.want {
			t.Errorf("BlockType %v = %d, want %d", tt.blockType, tt.blockType, tt.want)
		}
	}
}

func TestMessageCount(t *testing.T) {
	panel := chatui.NewPanel(80, 24)

	if panel.MessageCount() != 0 {
		t.Error("New panel should have 0 messages")
	}

	// Add a message directly via SetMessages
	panel.SetMessages([]chatui.Message{
		{Role: "user", Content: "Test"},
	})

	if panel.MessageCount() != 1 {
		t.Error("Panel should have 1 message")
	}
}

func TestPanelView(t *testing.T) {
	panel := chatui.NewPanel(80, 24)

	// View should not panic with no messages
	view := panel.ViewString()

	if view == "" {
		// Empty is acceptable for no messages
	}
}

func TestIsStreaming(t *testing.T) {
	panel := chatui.NewPanel(80, 24)

	if panel.IsStreaming() {
		t.Error("New panel should not be streaming")
	}
}

func TestConversationID(t *testing.T) {
	panel := chatui.NewPanel(80, 24)

	panel.SetConversationID("test-123")

	if panel.State().ConversationID != "test-123" {
		t.Error("ConversationID not set correctly")
	}
}
