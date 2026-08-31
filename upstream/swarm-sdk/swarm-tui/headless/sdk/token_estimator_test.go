package sdk

import (
	"testing"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/conversation"
)

func TestTokenEstimator(t *testing.T) {
	estimator := NewTokenEstimator()

	tests := []struct {
		name        string
		text        string
		provider    string
		expectedMin int
		expectedMax int
	}{
		{
			name:        "simple text anthropic",
			text:        "Hello, world!",
			provider:    "anthropic",
			expectedMin: 3,
			expectedMax: 5,
		},
		{
			name:        "code block anthropic",
			text:        "```go\nfunc main() {\n    fmt.Println(\"Hello\")\n}\n```",
			provider:    "anthropic",
			expectedMin: 10,
			expectedMax: 20,
		},
		{
			name:        "long text google",
			text:        "This is a comprehensive token analysis system that I built in Go to analyze all my AI conversations and estimate token usage with 97.74% confidence!",
			provider:    "google",
			expectedMin: 35,
			expectedMax: 45,
		},
		{
			name:        "markdown text",
			text:        "# Header\n\n**Bold text** and *italic* with [links](https://example.com)",
			provider:    "anthropic",
			expectedMin: 15,
			expectedMax: 25,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := estimator.estimateTokens(tt.text, tt.provider)

			if result < tt.expectedMin || result > tt.expectedMax {
				t.Errorf("estimateTokens() = %d, want between %d and %d",
					result, tt.expectedMin, tt.expectedMax)
			}
		})
	}
}

func TestEstimateInitialContext(t *testing.T) {
	estimator := NewTokenEstimator()

	systemPrompt := "You are a helpful AI assistant."
	history := []*conversation.Message{
		{
			Role:    "user",
			Content: "Hello!",
		},
		{
			Role:    "assistant",
			Content: "Hi there! How can I help you today?",
		},
	}
	userMessage := "Can you explain what tokens are?"

	result := estimator.EstimateInitialContext(
		systemPrompt,
		history,
		userMessage,
		"anthropic",
	)

	// Verify all components are present
	if result.SystemPromptTokens == 0 {
		t.Error("SystemPromptTokens should be > 0")
	}
	if result.HistoryTokens == 0 {
		t.Error("HistoryTokens should be > 0")
	}
	if result.UserMessageTokens == 0 {
		t.Error("UserMessageTokens should be > 0")
	}
	if result.TotalEstimated == 0 {
		t.Error("TotalEstimated should be > 0")
	}

	// Verify total is sum of components
	expectedTotal := result.SystemPromptTokens + result.HistoryTokens + result.UserMessageTokens
	if result.TotalEstimated != expectedTotal {
		t.Errorf("TotalEstimated = %d, want %d (sum of components)",
			result.TotalEstimated, expectedTotal)
	}

	// Verify provider
	if result.Provider != "anthropic" {
		t.Errorf("Provider = %s, want anthropic", result.Provider)
	}

	// Verify safety margin
	if result.SafetyMargin != 0.02 {
		t.Errorf("SafetyMargin = %f, want 0.02", result.SafetyMargin)
	}
}

func TestGetProviderFromModel(t *testing.T) {
	tests := []struct {
		model    string
		expected string
	}{
		{"claude-sonnet-4-5", "anthropic"},
		{"claude-opus-4-5-20251101", "anthropic"},
		{"claude-haiku-4-5-20251001", "anthropic"},
		{"gpt-4", "openai"},
		{"gpt-3.5-turbo", "openai"},
		{"gemini-3-pro-preview", "google"},
		{"gemini-3-flash-preview", "google"},
		{"glm-4.7", "zhipu"},
		{"zai-glm-4.7", "zhipu"},
		{"llama-3.3-70b", "meta"},
		{"model.gguf", "local"},
		{"unknown-model", "unknown"},
	}

	for _, tt := range tests {
		t.Run(tt.model, func(t *testing.T) {
			result := GetProviderFromModel(tt.model)
			if result != tt.expected {
				t.Errorf("GetProviderFromModel(%s) = %s, want %s",
					tt.model, result, tt.expected)
			}
		})
	}
}

func TestPatternDetection(t *testing.T) {
	tests := []struct {
		name    string
		text    string
		hasCode bool
		hasMD   bool
		hasSpec bool
	}{
		{
			name:    "plain text",
			text:    "This is just plain text.",
			hasCode: false,
			hasMD:   false,
			hasSpec: false,
		},
		{
			name:    "code block",
			text:    "```go\nfunc main() {}\n```",
			hasCode: true,
			hasMD:   false,
			hasSpec: true, // Code blocks contain {} and () special chars
		},
		{
			name:    "inline code",
			text:    "Use `fmt.Println()` to print.",
			hasCode: true,
			hasMD:   false,
			hasSpec: true, // Parentheses and backticks are special
		},
		{
			name:    "markdown",
			text:    "# Header\n\n**Bold** and *italic*",
			hasCode: false,
			hasMD:   true,
			hasSpec: true, // #, *, * are special chars
		},
		{
			name:    "high special chars",
			text:    "!@#$%^&*()_+-=[]{}|;':\",./<>?",
			hasCode: false,
			hasMD:   false,
			hasSpec: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if hasCodeBlocks(tt.text) != tt.hasCode && !hasInlineCode(tt.text) {
				t.Errorf("code detection mismatch for %s", tt.name)
			}
			if hasMarkdown(tt.text) != tt.hasMD {
				t.Errorf("markdown detection = %v, want %v", hasMarkdown(tt.text), tt.hasMD)
			}
			if hasHighSpecialCharDensity(tt.text) != tt.hasSpec {
				t.Errorf("special char detection = %v, want %v",
					hasHighSpecialCharDensity(tt.text), tt.hasSpec)
			}
		})
	}
}

func TestEstimateTokensForText(t *testing.T) {
	tests := []struct {
		text      string
		model     string
		minTokens int
		maxTokens int
	}{
		{
			text:      "Hello, world!",
			model:     "claude-sonnet-4-5",
			minTokens: 3,
			maxTokens: 5,
		},
		{
			text:      "This is a test message.",
			model:     "gpt-4",
			minTokens: 5,
			maxTokens: 8,
		},
	}

	for _, tt := range tests {
		result := EstimateTokensForText(tt.text, tt.model)
		if result < tt.minTokens || result > tt.maxTokens {
			t.Errorf("EstimateTokensForText(%q, %q) = %d, want between %d and %d",
				tt.text, tt.model, result, tt.minTokens, tt.maxTokens)
		}
	}
}

func BenchmarkEstimateTokens(b *testing.B) {
	estimator := NewTokenEstimator()
	text := "This is a sample text for benchmarking token estimation performance."

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		estimator.estimateTokens(text, "anthropic")
	}
}

func BenchmarkEstimateInitialContext(b *testing.B) {
	estimator := NewTokenEstimator()
	systemPrompt := "You are a helpful AI assistant."
	history := []*conversation.Message{
		{Role: "user", Content: "Hello!"},
		{Role: "assistant", Content: "Hi! How can I help?"},
	}
	userMessage := "Can you help me?"

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		estimator.EstimateInitialContext(systemPrompt, history, userMessage, "anthropic")
	}
}

// TestEstimateInitialContextWithToolCalls verifies that tool calls and tool results are counted
func TestEstimateInitialContextWithToolCalls(t *testing.T) {
	estimator := NewTokenEstimator()

	systemPrompt := "You are a helpful assistant with access to tools."

	// Create history with tool calls and tool results
	history := []*conversation.Message{
		{
			Role:    "user",
			Content: "List files in the current directory",
		},
		{
			Role:    "assistant",
			Content: "I'll list the files for you.",
			ToolCalls: []conversation.ToolCall{
				{
					ID:   "call_abc123",
					Name: "list_files",
					Parameters: map[string]any{
						"path": ".",
					},
				},
			},
		},
		{
			Role: "user",
			ToolResults: []conversation.ToolResult{
				{
					CallID: "call_abc123",
					Name:   "list_files",
					Output: "file1.go\nfile2.go\nREADME.md\n",
				},
			},
		},
		{
			Role:    "assistant",
			Content: "I found 3 files: file1.go, file2.go, and README.md",
		},
	}

	userMessage := "What's in file1.go?"

	// Estimate without tool calls (baseline)
	historyWithoutTools := []*conversation.Message{
		{Role: "user", Content: "List files in the current directory"},
		{Role: "assistant", Content: "I'll list the files for you."},
		{Role: "user", Content: ""}, // Empty content for tool result message
		{Role: "assistant", Content: "I found 3 files: file1.go, file2.go, and README.md"},
	}

	resultWithoutTools := estimator.EstimateInitialContext(
		systemPrompt,
		historyWithoutTools,
		userMessage,
		"anthropic",
	)

	// Estimate with tool calls (should be higher)
	resultWithTools := estimator.EstimateInitialContext(
		systemPrompt,
		history,
		userMessage,
		"anthropic",
	)

	t.Logf("Tokens without tools: %d", resultWithoutTools.TotalEstimated)
	t.Logf("Tokens with tools: %d", resultWithTools.TotalEstimated)

	// With tools should have MORE tokens due to tool call/result overhead
	if resultWithTools.TotalEstimated <= resultWithoutTools.TotalEstimated {
		t.Errorf("Expected tool calls to add tokens, got without=%d with=%d",
			resultWithoutTools.TotalEstimated, resultWithTools.TotalEstimated)
	}

	// Tool overhead should be at least 30 tokens (conservative estimate)
	// Tool call: ID (10) + name (5) + params (10) + overhead (10) = ~35
	// Tool result: ID (10) + name (5) + output (15) + overhead (10) = ~40
	// Total: ~75 tokens minimum
	minExpectedDifference := 30
	actualDifference := resultWithTools.TotalEstimated - resultWithoutTools.TotalEstimated

	if actualDifference < minExpectedDifference {
		t.Errorf("Tool call/result tokens too low: got %d, want at least %d",
			actualDifference, minExpectedDifference)
	}

	t.Logf("Tool call/result overhead: %d tokens", actualDifference)
}

// TestEstimateToolCallTokens tests tool call token estimation
func TestEstimateToolCallTokens(t *testing.T) {
	estimator := NewTokenEstimator()

	tests := []struct {
		name      string
		toolCall  conversation.ToolCall
		minTokens int
		maxTokens int
	}{
		{
			name: "simple tool call",
			toolCall: conversation.ToolCall{
				ID:   "call_123",
				Name: "get_weather",
				Parameters: map[string]any{
					"city": "San Francisco",
				},
			},
			minTokens: 15,
			maxTokens: 35,
		},
		{
			name: "complex tool call with large params",
			toolCall: conversation.ToolCall{
				ID:   "call_complex_456",
				Name: "search_files",
				Parameters: map[string]any{
					"query":       "golang implementation of token estimation",
					"path":        "/home/user/projects",
					"recursive":   true,
					"max_results": 100,
				},
			},
			minTokens: 30,
			maxTokens: 80,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tokens := estimator.estimateToolCallTokens(tt.toolCall, "anthropic")

			t.Logf("Tool call %q estimated at %d tokens", tt.name, tokens)

			if tokens < tt.minTokens || tokens > tt.maxTokens {
				t.Errorf("estimateToolCallTokens() = %d, want between %d and %d",
					tokens, tt.minTokens, tt.maxTokens)
			}
		})
	}
}

// TestEstimateToolResultTokens tests tool result token estimation
func TestEstimateToolResultTokens(t *testing.T) {
	estimator := NewTokenEstimator()

	tests := []struct {
		name       string
		toolResult conversation.ToolResult
		minTokens  int
		maxTokens  int
	}{
		{
			name: "simple tool result",
			toolResult: conversation.ToolResult{
				CallID: "call_123",
				Name:   "get_weather",
				Output: "Temperature: 72°F, Sunny",
			},
			minTokens: 15,
			maxTokens: 35,
		},
		{
			name: "large tool result",
			toolResult: conversation.ToolResult{
				CallID: "call_456",
				Name:   "read_file",
				Output: "package main\n\nimport \"fmt\"\n\nfunc main() {\n    fmt.Println(\"Hello, world!\")\n}\n",
			},
			minTokens: 25,
			maxTokens: 60,
		},
		{
			name: "tool result with error",
			toolResult: conversation.ToolResult{
				CallID: "call_789",
				Name:   "execute_command",
				Output: "",
				Error: &conversation.ToolError{
					Type:    "tool.permission_denied",
					Message: "Permission denied: cannot execute this command",
				},
			},
			minTokens: 20,
			maxTokens: 50,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tokens := estimator.estimateToolResultTokens(tt.toolResult, "anthropic")

			t.Logf("Tool result %q estimated at %d tokens", tt.name, tokens)

			if tokens < tt.minTokens || tokens > tt.maxTokens {
				t.Errorf("estimateToolResultTokens() = %d, want between %d and %d",
					tokens, tt.minTokens, tt.maxTokens)
			}
		})
	}
}
