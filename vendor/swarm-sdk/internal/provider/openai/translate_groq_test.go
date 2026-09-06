package openai

import (
	"strings"
	"testing"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/conversation"
)

// TestTranslateMessage_GroqNoReasoningContent verifies that Groq models
// do NOT receive reasoning_content field in messages, which they don't support.
// This test addresses the bug: Groq API rejection with HTTP 400 for unsupported
// 'reasoning_content' property in assistant messages.
func TestTranslateMessage_GroqNoReasoningContent(t *testing.T) {
	tests := []struct {
		name            string
		model           string
		message         *conversation.Message
		expectReasoning bool
	}{
		{
			name:  "Groq gpt-oss model should NOT include reasoning_content",
			model: "openai/gpt-oss-20b",
			message: &conversation.Message{
				Role:     conversation.RoleAssistant,
				Content:  "The answer is 42",
				Thinking: "Let me think about this...",
			},
			expectReasoning: false,
		},
		{
			name:  "Groq model with metadata reasoning_content should NOT include it",
			model: "gpt-oss-20b",
			message: &conversation.Message{
				Role:    conversation.RoleAssistant,
				Content: "Response",
				Metadata: map[string]any{
					"reasoning_content": "My reasoning process",
				},
			},
			expectReasoning: false,
		},
		{
			name:  "GLM model SHOULD include reasoning_content",
			model: "glm-4-plus",
			message: &conversation.Message{
				Role:     conversation.RoleAssistant,
				Content:  "The answer is 42",
				Thinking: "Let me think about this...",
			},
			expectReasoning: true,
		},
		{
			name:  "GLM model with metadata SHOULD include reasoning_content",
			model: "GLM-4.6",
			message: &conversation.Message{
				Role:    conversation.RoleAssistant,
				Content: "Response",
				Metadata: map[string]any{
					"reasoning_content": "My reasoning process",
				},
			},
			expectReasoning: true,
		},
		{
			name:  "Non-GLM model without reasoning should have empty reasoning_content",
			model: "gpt-4",
			message: &conversation.Message{
				Role:    conversation.RoleAssistant,
				Content: "Simple response",
			},
			expectReasoning: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			openaiMsg, err := TranslateMessage(tt.message, tt.model)
			if err != nil {
				t.Fatalf("TranslateMessage failed: %v", err)
			}

			// Check if ReasoningContent was set
			hasReasoning := strings.TrimSpace(openaiMsg.ReasoningContent) != ""

			if hasReasoning != tt.expectReasoning {
				t.Errorf("ReasoningContent presence mismatch:\n"+
					"  Model: %s\n"+
					"  Expected reasoning_content: %v\n"+
					"  Got reasoning_content: %v\n"+
					"  ReasoningContent value: %q",
					tt.model, tt.expectReasoning, hasReasoning, openaiMsg.ReasoningContent)
			}

			// Additional check: Ensure content is properly set
			if tt.message.Content != "" {
				if openaiMsg.Content == nil || openaiMsg.Content == "" {
					t.Errorf("Content was not properly set:\n"+
						"  Expected: %q\n"+
						"  Got: %v",
						tt.message.Content, openaiMsg.Content)
				}
			}
		})
	}
}

// TestIsGLMModel verifies the GLM model detection logic
func TestIsGLMModel(t *testing.T) {
	tests := []struct {
		model string
		isGLM bool
	}{
		{"glm-4-plus", true},
		{"GLM-4.6", true},
		{"glm-4-reasoning", true},
		{"zhipu-ai-model", true},
		{"ZHIPU-GLM-4", true},
		{"gpt-4", false},
		{"openai/gpt-oss-20b", false},
		{"gpt-oss-20b", false},
		{"qwen/qwen3-32b", false},
		{"claude-3-opus", false},
		{"mixtral-8x7b", false},
		{"", false},
	}

	for _, tt := range tests {
		t.Run(tt.model, func(t *testing.T) {
			result := isGLMModel(tt.model)
			if result != tt.isGLM {
				t.Errorf("isGLMModel(%q) = %v, want %v", tt.model, result, tt.isGLM)
			}
		})
	}
}

// TestTranslateMessage_GroqRealWorldScenario simulates the exact error scenario
// from the bug report: gpt-oss-20b model with reasoning_content in metadata
func TestTranslateMessage_GroqRealWorldScenario(t *testing.T) {
	// Simulate a conversation with multiple messages
	messages := []*conversation.Message{
		{
			Role:    conversation.RoleUser,
			Content: "What is 2+2?",
		},
		{
			Role:    conversation.RoleAssistant,
			Content: "Let me solve that for you",
			Metadata: map[string]any{
				"reasoning_content": "The user asked a simple arithmetic question",
			},
		},
		{
			Role:    conversation.RoleUser,
			Content: "Show your work",
		},
		{
			Role:     conversation.RoleAssistant,
			Content:  "2 + 2 = 4",
			Thinking: "Simple addition: 2 plus 2 equals 4",
		},
	}

	model := "openai/gpt-oss-20b"

	for i, msg := range messages {
		openaiMsg, err := TranslateMessage(msg, model)
		if err != nil {
			t.Fatalf("Message %d translation failed: %v", i, err)
		}

		// CRITICAL: Groq models should NEVER have reasoning_content set
		if msg.Role == conversation.RoleAssistant && openaiMsg.ReasoningContent != "" {
			t.Errorf("Message %d (role=%s) has reasoning_content=%q for Groq model %s\n"+
				"This will cause HTTP 400 error: 'property reasoning_content is unsupported'",
				i, msg.Role, openaiMsg.ReasoningContent, model)
		}

		// Verify content is preserved
		if msg.Content != "" && (openaiMsg.Content == nil || openaiMsg.Content == "") {
			t.Errorf("Message %d lost content during translation", i)
		}
	}
}
