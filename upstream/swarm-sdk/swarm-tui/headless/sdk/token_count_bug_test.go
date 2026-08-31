package sdk

import (
	"fmt"
	"testing"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/conversation"
)

// TestTokenCountBug_ReproduceMillions attempts to reproduce the bug where
// token counts can spike to millions before settling to the correct value
func TestTokenCountBug_ReproduceMillions(t *testing.T) {
	estimator := NewTokenEstimator()

	// Test 1: Large binary content in tool results (images, PDFs)
	t.Run("large_binary_content", func(t *testing.T) {
		// Simulate a 5MB image in tool result
		largeImageData := make([]byte, 5*1024*1024) // 5MB

		history := []*conversation.Message{
			{
				Role:    "assistant",
				Content: "Let me read that image for you.",
				ToolCalls: []conversation.ToolCall{
					{
						ID:   "call_123",
						Name: "read_image",
						Parameters: map[string]any{
							"path": "/path/to/image.png",
						},
					},
				},
			},
			{
				Role:    "user",
				Content: "",
				ToolResults: []conversation.ToolResult{
					{
						CallID: "call_123",
						Name:   "read_image",
						Output: "Image processed successfully",
						Content: []conversation.ContentBlock{
							{
								Type: "image",
								Data: largeImageData,
								Name: "screenshot.png",
							},
						},
					},
				},
			},
		}

		result := estimator.EstimateInitialContext(
			"You are a helpful assistant.",
			history,
			"What's in this image?",
			"anthropic",
		)

		fmt.Printf("Test: large_binary_content\n")
		fmt.Printf("  SystemPromptTokens: %d\n", result.SystemPromptTokens)
		fmt.Printf("  HistoryTokens: %d\n", result.HistoryTokens)
		fmt.Printf("  UserMessageTokens: %d\n", result.UserMessageTokens)
		fmt.Printf("  TotalEstimated: %d\n", result.TotalEstimated)

		// Check if we're getting millions
		if result.TotalEstimated > 100000 {
			t.Logf("⚠️  BUG REPRODUCED: TotalEstimated = %s tokens (%.2fM)",
				formatNumber(result.TotalEstimated),
				float64(result.TotalEstimated)/1000000)
		}
	})

	// Test 2: Multiple large files in conversation history
	t.Run("multiple_large_files", func(t *testing.T) {
		history := []*conversation.Message{}

		// Simulate reading 10 large files (1MB each)
		for i := range 10 {
			history = append(history,
				&conversation.Message{
					Role:    "assistant",
					Content: fmt.Sprintf("Reading file %d...", i),
					ToolCalls: []conversation.ToolCall{
						{
							ID:   fmt.Sprintf("call_%d", i),
							Name: "read_file",
							Parameters: map[string]any{
								"path": fmt.Sprintf("/large/file_%d.bin", i),
							},
						},
					},
				},
				&conversation.Message{
					Role:    "user",
					Content: "",
					ToolResults: []conversation.ToolResult{
						{
							CallID: fmt.Sprintf("call_%d", i),
							Name:   "read_file",
							Output: "File read successfully",
							Content: []conversation.ContentBlock{
								{
									Type: "file",
									Data: make([]byte, 1*1024*1024), // 1MB each
									Name: fmt.Sprintf("file_%d.bin", i),
								},
							},
						},
					},
				},
			)
		}

		result := estimator.EstimateInitialContext(
			"You are a helpful assistant.",
			history,
			"Analyze all these files",
			"anthropic",
		)

		fmt.Printf("\nTest: multiple_large_files\n")
		fmt.Printf("  SystemPromptTokens: %d\n", result.SystemPromptTokens)
		fmt.Printf("  HistoryTokens: %d\n", result.HistoryTokens)
		fmt.Printf("  UserMessageTokens: %d\n", result.UserMessageTokens)
		fmt.Printf("  TotalEstimated: %d\n", result.TotalEstimated)

		if result.TotalEstimated > 100000 {
			t.Logf("⚠️  BUG REPRODUCED: TotalEstimated = %s tokens (%.2fM)",
				formatNumber(result.TotalEstimated),
				float64(result.TotalEstimated)/1000000)
		}
	})

	// Test 3: Very long text content in tool results
	t.Run("very_long_text_content", func(t *testing.T) {
		// Simulate a tool result with extremely long output (e.g., reading a huge log file)
		veryLongText := make([]byte, 10*1024*1024) // 10MB of text
		for i := range veryLongText {
			veryLongText[i] = 'A'
		}

		history := []*conversation.Message{
			{
				Role:    "assistant",
				Content: "Let me read that log file.",
				ToolCalls: []conversation.ToolCall{
					{
						ID:         "call_log",
						Name:       "read_file",
						Parameters: map[string]any{"path": "/var/log/huge.log"},
					},
				},
			},
			{
				Role:    "user",
				Content: "",
				ToolResults: []conversation.ToolResult{
					{
						CallID: "call_log",
						Name:   "read_file",
						Output: string(veryLongText),
					},
				},
			},
		}

		result := estimator.EstimateInitialContext(
			"You are a helpful assistant.",
			history,
			"Summarize this log",
			"anthropic",
		)

		fmt.Printf("\nTest: very_long_text_content\n")
		fmt.Printf("  SystemPromptTokens: %d\n", result.SystemPromptTokens)
		fmt.Printf("  HistoryTokens: %d\n", result.HistoryTokens)
		fmt.Printf("  UserMessageTokens: %d\n", result.UserMessageTokens)
		fmt.Printf("  TotalEstimated: %d\n", result.TotalEstimated)

		if result.TotalEstimated > 1000000 {
			t.Logf("⚠️  BUG REPRODUCED: TotalEstimated = %s tokens (%.2fM)",
				formatNumber(result.TotalEstimated),
				float64(result.TotalEstimated)/1000000)
		}
	})

	// Test 4: Duplicated conversation history (possible bug in history accumulation)
	t.Run("duplicated_history", func(t *testing.T) {
		baseHistory := []*conversation.Message{
			{Role: "user", Content: "Hello"},
			{Role: "assistant", Content: "Hi there!"},
		}

		// Simulate accidentally duplicating history 100 times
		duplicatedHistory := []*conversation.Message{}
		for range 100 {
			duplicatedHistory = append(duplicatedHistory, baseHistory...)
		}

		result := estimator.EstimateInitialContext(
			"You are a helpful assistant.",
			duplicatedHistory,
			"What's the weather?",
			"anthropic",
		)

		fmt.Printf("\nTest: duplicated_history (100x duplication)\n")
		fmt.Printf("  SystemPromptTokens: %d\n", result.SystemPromptTokens)
		fmt.Printf("  HistoryTokens: %d\n", result.HistoryTokens)
		fmt.Printf("  UserMessageTokens: %d\n", result.UserMessageTokens)
		fmt.Printf("  TotalEstimated: %d\n", result.TotalEstimated)

		if result.TotalEstimated > 10000 {
			t.Logf("⚠️  POSSIBLE BUG: TotalEstimated = %s tokens with duplicated history",
				formatNumber(result.TotalEstimated))
		}
	})
}

// TestTokenCountBug_TraceFlow traces the complete flow of token counting
// to identify where the multiplication/accumulation happens
func TestTokenCountBug_TraceFlow(t *testing.T) {
	estimator := NewTokenEstimator()

	// Simple conversation
	systemPrompt := "You are a helpful assistant."
	history := []*conversation.Message{
		{Role: "user", Content: "Hello!"},
		{Role: "assistant", Content: "Hi! How can I help?"},
	}
	userMessage := "Tell me about Go"

	// Estimate
	result := estimator.EstimateInitialContext(systemPrompt, history, userMessage, "anthropic")

	fmt.Printf("\n=== Token Count Flow Trace ===\n")
	fmt.Printf("System Prompt: %q (%d chars) → %d tokens\n",
		truncate(systemPrompt, 50), len(systemPrompt), result.SystemPromptTokens)
	fmt.Printf("History Messages: %d\n", len(history))
	for i, msg := range history {
		msgTokens := estimator.estimateTokens(msg.Content, "anthropic")
		fmt.Printf("  [%d] %s: %q (%d chars) → %d tokens\n",
			i, msg.Role, truncate(msg.Content, 30), len(msg.Content), msgTokens)
	}
	fmt.Printf("History Total: %d tokens\n", result.HistoryTokens)
	fmt.Printf("User Message: %q (%d chars) → %d tokens\n",
		truncate(userMessage, 50), len(userMessage), result.UserMessageTokens)
	fmt.Printf("\nFINAL TOTAL: %d tokens\n", result.TotalEstimated)
	fmt.Printf("Expected: %d + %d + %d = %d\n",
		result.SystemPromptTokens, result.HistoryTokens, result.UserMessageTokens,
		result.SystemPromptTokens+result.HistoryTokens+result.UserMessageTokens)

	// Verify no unexpected multiplication
	expected := result.SystemPromptTokens + result.HistoryTokens + result.UserMessageTokens
	if result.TotalEstimated != expected {
		t.Errorf("Token count mismatch! Got %d, expected %d", result.TotalEstimated, expected)
	}
}

// Helper functions
func formatNumber(n int) string {
	if n >= 1000000 {
		return fmt.Sprintf("%.2fM", float64(n)/1000000)
	}
	if n >= 1000 {
		return fmt.Sprintf("%.1fK", float64(n)/1000)
	}
	return fmt.Sprintf("%d", n)
}

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}
