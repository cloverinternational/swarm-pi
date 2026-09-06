package anthropic

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/conversation"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/observability"
)

// TestTranslateMessages_EmptyToolResult verifies that empty tool results
// are properly handled to avoid Anthropic API validation errors.
//
// Background: When a tool like swarm_Read returns an empty file, the SDK
// creates a ToolResult with Output="" and Content=[]. This was causing
// Anthropic API to reject the request with:
// "messages.X.content.Y.tool_result.content.0.text.text: Field required"
//
// The fix wraps empty strings in a proper text content block.
func TestTranslateMessages_EmptyToolResult(t *testing.T) {
	ctx := context.Background()
	logger := observability.NewNopLogger()

	tests := []struct {
		name           string
		toolResult     conversation.ToolResult
		wantContentStr bool // if true, expect string content
		wantContentArr bool // if true, expect array content
		wantTextValue  string
	}{
		{
			name: "empty_output_string",
			toolResult: conversation.ToolResult{
				CallID: "call-123",
				Name:   "swarm_Read",
				Output: "", // Empty file case
			},
			wantContentArr: true,
			wantTextValue:  "(empty file)",
		},
		{
			name: "non_empty_output_string",
			toolResult: conversation.ToolResult{
				CallID: "call-456",
				Name:   "swarm_Read",
				Output: "file contents here",
			},
			wantContentStr: true,
		},
		{
			name: "empty_output_with_rich_content",
			toolResult: conversation.ToolResult{
				CallID: "call-789",
				Name:   "some_tool",
				Output: "",
				Content: []conversation.ContentBlock{
					{
						Type: "text",
						Text: "actual content",
					},
				},
			},
			wantContentArr: true,
			wantTextValue:  "actual content",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// A tool_result user message MUST be preceded by an assistant message
			// with matching tool_use blocks — this is enforced by Anthropic's API and
			// now by our stripOrphanedToolResults. Use a realistic message pair.
			assistantMsg := &conversation.Message{
				ID:   "asst-1",
				Role: conversation.RoleAssistant,
				ToolCalls: []conversation.ToolCall{
					{
						ID:   tt.toolResult.CallID,
						Name: tt.toolResult.Name,
					},
				},
			}
			msg := &conversation.Message{
				ID:          "msg-1",
				Role:        conversation.RoleUser,
				Content:     "Use the tool",
				ToolResults: []conversation.ToolResult{tt.toolResult},
			}
			// Prepend a user seed so translateMessages first-message invariant holds.
			seed := &conversation.Message{ID: "seed-0", Role: conversation.RoleUser, Content: "go"}
			messages := []*conversation.Message{seed, assistantMsg, msg}

			result, err := translateMessages(ctx, messages, false, false, logger)
			if err != nil {
				t.Fatalf("translateMessages() error: %v", err)
			}

			// We expect 3 translated messages: seed user, assistant, tool_result user.
			if len(result) != 3 {
				t.Fatalf("expected 3 messages (seed, assistant, tool_result), got %d", len(result))
			}

			anthropicMsg := result[2] // The tool_result user message
			contentBlocks, ok := anthropicMsg.Content.([]ContentBlock)
			if !ok {
				t.Fatalf("expected content to be []ContentBlock, got %T", anthropicMsg.Content)
			}

			if len(contentBlocks) == 0 {
				t.Fatal("expected at least one content block")
			}

			// Find the tool_result block
			var toolResultBlock *ContentBlock
			for i := range contentBlocks {
				if contentBlocks[i].Type == "tool_result" {
					toolResultBlock = &contentBlocks[i]
					break
				}
			}

			if toolResultBlock == nil {
				t.Fatal("expected to find tool_result block")
			}

			if tt.wantContentStr {
				// Content should be a string
				contentStr, ok := toolResultBlock.Content.(string)
				if !ok {
					t.Fatalf("expected content to be string, got %T", toolResultBlock.Content)
				}
				if contentStr == "" {
					t.Error("content string should not be empty (violates Anthropic API schema)")
				}
			}

			if tt.wantContentArr {
				// Content should be an array of ContentBlocks
				contentBlocks, ok := toolResultBlock.Content.([]ContentBlock)
				if !ok {
					t.Fatalf("expected content to be []ContentBlock, got %T", toolResultBlock.Content)
				}
				if len(contentBlocks) == 0 {
					t.Fatal("expected at least one content block in array")
				}

				// First block should be text type
				if contentBlocks[0].Type != "text" {
					t.Errorf("expected first block to be text type, got %s", contentBlocks[0].Type)
				}

				if tt.wantTextValue != "" {
					if contentBlocks[0].Text != tt.wantTextValue {
						t.Errorf("expected text value %q, got %q", tt.wantTextValue, contentBlocks[0].Text)
					}
				}
			}

			// CRITICAL: Verify the JSON marshaling produces valid Anthropic API format
			jsonBytes, err := json.Marshal(anthropicMsg)
			if err != nil {
				t.Fatalf("failed to marshal message: %v", err)
			}

			// Parse back to verify structure
			var parsedMsg map[string]any
			if err := json.Unmarshal(jsonBytes, &parsedMsg); err != nil {
				t.Fatalf("failed to unmarshal message: %v", err)
			}

			// Verify content structure is valid for Anthropic API
			content, ok := parsedMsg["content"].([]any)
			if !ok {
				t.Fatalf("expected content to be array, got %T", parsedMsg["content"])
			}

			for i, block := range content {
				blockMap, ok := block.(map[string]any)
				if !ok {
					t.Fatalf("expected content block to be map, got %T", block)
				}

				if blockMap["type"] == "tool_result" {
					// Verify tool_result has valid content field
					resultContent := blockMap["content"]
					if resultContent == nil {
						t.Error("tool_result block missing content field")
					}

					// If content is empty string, that would violate Anthropic API
					if strContent, ok := resultContent.(string); ok && strContent == "" {
						t.Error("tool_result.content is empty string - violates Anthropic API schema")
					}

					// If content is array, verify it has at least one text block
					if arrContent, ok := resultContent.([]any); ok {
						if len(arrContent) == 0 {
							t.Error("tool_result.content array is empty")
						}
						for j, contentBlock := range arrContent {
							cbMap, ok := contentBlock.(map[string]any)
							if !ok {
								t.Errorf("content block %d is not a map: %T", j, contentBlock)
								continue
							}
							if cbMap["type"] == "text" {
								// Text blocks must have "text" field
								if _, hasText := cbMap["text"]; !hasText {
									t.Errorf("text content block %d missing 'text' field", j)
								}
							}
						}
					}

					t.Logf("Block %d tool_result content: %+v", i, resultContent)
				}
			}
		})
	}
}

// TestTranslateMessages_EmptyToolResultJSONSchema verifies the exact JSON
// structure that would be sent to the Anthropic API for empty tool results.
func TestTranslateMessages_EmptyToolResultJSONSchema(t *testing.T) {
	ctx := context.Background()
	logger := observability.NewNopLogger()

	// Tool_result messages must follow an assistant with matching tool_use IDs.
	seed := &conversation.Message{ID: "seed-0", Role: conversation.RoleUser, Content: "go"}
	assistantMsg := &conversation.Message{
		ID:   "asst-1",
		Role: conversation.RoleAssistant,
		ToolCalls: []conversation.ToolCall{
			{ID: "toolu_abc123", Name: "swarm_Read"},
		},
	}
	msg := &conversation.Message{
		ID:      "msg-1",
		Role:    conversation.RoleTool,
		Content: "",
		ToolResults: []conversation.ToolResult{
			{
				CallID: "toolu_abc123",
				Name:   "swarm_Read",
				Output: "", // Empty file
			},
		},
	}
	messages := []*conversation.Message{seed, assistantMsg, msg}

	result, err := translateMessages(ctx, messages, false, false, logger)
	if err != nil {
		t.Fatalf("translateMessages() error: %v", err)
	}

	if len(result) != 3 {
		t.Fatalf("expected 3 messages (seed, assistant, tool_result), got %d", len(result))
	}

	// Marshal to JSON and verify schema for the tool_result message
	jsonBytes, err := json.MarshalIndent(result[2], "", "  ")
	if err != nil {
		t.Fatalf("failed to marshal: %v", err)
	}

	t.Logf("Generated JSON:\n%s", string(jsonBytes))

	// Parse and validate structure
	var msgMap map[string]any
	if err := json.Unmarshal(jsonBytes, &msgMap); err != nil {
		t.Fatalf("failed to parse JSON: %v", err)
	}

	// Verify role is "user" (tool results are sent as user messages)
	if msgMap["role"] != "user" {
		t.Errorf("expected role=user, got %v", msgMap["role"])
	}

	// Verify content structure
	content, ok := msgMap["content"].([]any)
	if !ok || len(content) == 0 {
		t.Fatalf("expected content array with at least one block")
	}

	// Find tool_result block
	var toolResultBlock map[string]any
	for _, block := range content {
		blockMap := block.(map[string]any)
		if blockMap["type"] == "tool_result" {
			toolResultBlock = blockMap
			break
		}
	}

	if toolResultBlock == nil {
		t.Fatal("expected tool_result block in content")
	}

	// CRITICAL CHECK: Verify content field structure
	resultContent := toolResultBlock["content"]
	if resultContent == nil {
		t.Fatal("tool_result.content is missing (required by API)")
	}

	// Must NOT be empty string
	if strContent, ok := resultContent.(string); ok && strContent == "" {
		t.Fatal("tool_result.content is empty string - THIS IS THE BUG WE FIXED")
	}

	// Should be array of content blocks
	arrContent, ok := resultContent.([]any)
	if !ok {
		t.Fatalf("expected tool_result.content to be array, got %T", resultContent)
	}

	if len(arrContent) == 0 {
		t.Fatal("tool_result.content array is empty")
	}

	// First block should be text type with non-empty text
	firstBlock := arrContent[0].(map[string]any)
	if firstBlock["type"] != "text" {
		t.Errorf("expected first block type=text, got %v", firstBlock["type"])
	}

	textValue, ok := firstBlock["text"].(string)
	if !ok || textValue == "" {
		t.Errorf("expected non-empty text field, got %v", firstBlock["text"])
	}

	t.Logf("✓ Empty tool result properly formatted with text: %q", textValue)
}
