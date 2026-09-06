package minimax

import (
	"testing"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/conversation"
)

// TestTranslateUserMessage tests translation of user messages.
func TestTranslateUserMessage(t *testing.T) {
	tests := []struct {
		name   string
		input  *conversation.Message
		verify func(t *testing.T, msg Message)
	}{
		{
			name: "simple text message",
			input: &conversation.Message{
				Role:    conversation.RoleUser,
				Content: "Hello, MiniMax!",
			},
			verify: func(t *testing.T, msg Message) {
				if msg.Role != "user" {
					t.Errorf("expected role 'user', got %s", msg.Role)
				}
				if msg.Content != "Hello, MiniMax!" {
					t.Errorf("expected 'Hello, MiniMax!', got %s", msg.Content)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := translateUserMessage(tt.input)
			tt.verify(t, result)
		})
	}
}

// TestTranslateAssistantMessage tests translation of assistant messages.
func TestTranslateAssistantMessage(t *testing.T) {
	tests := []struct {
		name   string
		input  *conversation.Message
		verify func(t *testing.T, msg Message)
	}{
		{
			name: "simple text response",
			input: &conversation.Message{
				Role:    conversation.RoleAssistant,
				Content: "Hello from MiniMax!",
			},
			verify: func(t *testing.T, msg Message) {
				if msg.Role != "assistant" {
					t.Errorf("expected role 'assistant', got %s", msg.Role)
				}
				blocks := msg.Content.([]ContentBlock)
				if len(blocks) != 1 {
					t.Errorf("expected 1 block, got %d", len(blocks))
				}
				if blocks[0].Type != "text" {
					t.Errorf("expected text block, got %s", blocks[0].Type)
				}
				if blocks[0].Text != "Hello from MiniMax!" {
					t.Errorf("expected 'Hello from MiniMax!', got %s", blocks[0].Text)
				}
			},
		},
		{
			name: "response with thinking",
			input: &conversation.Message{
				Role:     conversation.RoleAssistant,
				Content:  "The answer is 42",
				Thinking: "Let me reason through this",
			},
			verify: func(t *testing.T, msg Message) {
				blocks := msg.Content.([]ContentBlock)
				if len(blocks) < 2 {
					t.Errorf("expected at least 2 blocks, got %d", len(blocks))
					return
				}
				if blocks[0].Type != "thinking" {
					t.Errorf("expected thinking block first, got %s", blocks[0].Type)
				}
				if blocks[1].Type != "text" {
					t.Errorf("expected text block second, got %s", blocks[1].Type)
				}
			},
		},
		{
			name: "tool call",
			input: &conversation.Message{
				Role: conversation.RoleAssistant,
				ToolCalls: []conversation.ToolCall{
					{
						ID:         "call_123",
						Name:       "calculator",
						Parameters: map[string]any{"operation": "add", "x": 2, "y": 3},
					},
				},
			},
			verify: func(t *testing.T, msg Message) {
				blocks := msg.Content.([]ContentBlock)
				found := false
				for _, block := range blocks {
					if block.Type == "tool_use" {
						found = true
						if block.ID != "call_123" {
							t.Errorf("expected id 'call_123', got %s", block.ID)
						}
						if block.Name != "calculator" {
							t.Errorf("expected name 'calculator', got %s", block.Name)
						}
					}
				}
				if !found {
					t.Error("expected to find tool_use block")
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := translateAssistantMessage(tt.input)
			tt.verify(t, result)
		})
	}
}

// TestTranslateToolResult tests translation of tool results.
func TestTranslateToolResult(t *testing.T) {
	tests := []struct {
		name   string
		input  *conversation.Message
		verify func(t *testing.T, blocks []ContentBlock)
	}{
		{
			name: "simple tool result",
			input: &conversation.Message{
				Role: conversation.RoleUser,
				ToolResults: []conversation.ToolResult{
					{
						CallID: "call_123",
						Output: "Result of tool execution",
					},
				},
			},
			verify: func(t *testing.T, blocks []ContentBlock) {
				if len(blocks) != 1 {
					t.Errorf("expected 1 block, got %d", len(blocks))
					return
				}
				if blocks[0].Type != "tool_result" {
					t.Errorf("expected tool_result type, got %s", blocks[0].Type)
				}
				if blocks[0].ToolUseID != "call_123" {
					t.Errorf("expected ToolUseID 'call_123', got %s", blocks[0].ToolUseID)
				}
				if blocks[0].Content != "Result of tool execution" {
					t.Errorf("expected content, got %s", blocks[0].Content)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := translateToolResult(tt.input)
			tt.verify(t, result)
		})
	}
}

// TestTranslateMessages tests the complete message translation pipeline.
func TestTranslateMessages(t *testing.T) {
	messages := []*conversation.Message{
		{
			Role:    conversation.RoleUser,
			Content: "What is 2+3?",
		},
		{
			Role:     conversation.RoleAssistant,
			Content:  "The answer is 5",
			Thinking: "Simple arithmetic",
			ToolCalls: []conversation.ToolCall{
				{
					ID:         "call_1",
					Name:       "calculator",
					Parameters: map[string]any{"x": 2, "y": 3},
				},
			},
		},
		{
			Role: conversation.RoleUser,
			ToolResults: []conversation.ToolResult{
				{
					CallID: "call_1",
					Output: "5",
				},
			},
		},
	}

	result, err := translateMessages(messages)
	if err != nil {
		t.Fatalf("failed to translate messages: %v", err)
	}

	if len(result) != 3 {
		t.Errorf("expected 3 messages, got %d", len(result))
	}

	// Verify first message (user)
	if result[0].Role != "user" {
		t.Errorf("expected user role for first message, got %s", result[0].Role)
	}

	// Verify second message (assistant with thinking and tool_use)
	if result[1].Role != "assistant" {
		t.Errorf("expected assistant role for second message, got %s", result[1].Role)
	}
	blocks := result[1].Content.([]ContentBlock)
	if len(blocks) < 2 {
		t.Errorf("expected at least 2 blocks in second message, got %d", len(blocks))
	}

	// Verify third message (user with tool_result)
	if result[2].Role != "user" {
		t.Errorf("expected user role for third message, got %s", result[2].Role)
	}
}

// TestTranslateResponse tests translation of MiniMax response to canonical format.
func TestTranslateResponse(t *testing.T) {
	response := &MessageResponse{
		ID:    "msg_123",
		Role:  "assistant",
		Model: "MiniMax-M2.5",
		Content: []ContentBlock{
			{
				Type:     "thinking",
				Thinking: "Let me think",
			},
			{
				Type: "text",
				Text: "Here's my answer",
			},
		},
		StopReason: "end_turn",
		Usage: Usage{
			InputTokens:  10,
			OutputTokens: 20,
		},
	}

	result, err := translateResponse(response)
	if err != nil {
		t.Fatalf("failed to translate response: %v", err)
	}

	if result.Message.Role != conversation.RoleAssistant {
		t.Errorf("expected assistant role, got %s", result.Message.Role)
	}

	if result.Message.Thinking != "Let me think" {
		t.Errorf("expected thinking content, got %s", result.Message.Thinking)
	}

	if result.Message.Content != "Here's my answer" {
		t.Errorf("expected text content, got %s", result.Message.Content)
	}

	if result.Usage.Input != 10 {
		t.Errorf("expected 10 input tokens, got %d", result.Usage.Input)
	}

	if result.Usage.Output != 20 {
		t.Errorf("expected 20 output tokens, got %d", result.Usage.Output)
	}
}
