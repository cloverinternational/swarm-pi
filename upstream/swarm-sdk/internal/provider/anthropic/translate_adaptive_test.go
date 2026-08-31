package anthropic

import (
	"context"
	"testing"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/conversation"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/observability"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/provider"
)

func TestTranslateRequest_AdaptiveThinking(t *testing.T) {
	ctx := context.Background()
	logger := observability.NewNopLogger()

	tests := []struct {
		name           string
		model          string
		metadata       map[string]any
		expectType     string
		expectBudget   int
		expectDisplay  string
		expectNoConfig bool
	}{
		{
			name:  "opus 4.6 uses adaptive thinking",
			model: "claude-opus-4-6",
			metadata: map[string]any{
				"thinking_enabled": true,
			},
			expectType:     "adaptive",
			expectNoConfig: false,
		},
		{
			name:  "opus 4.6 with budget still uses adaptive",
			model: "claude-opus-4-6",
			metadata: map[string]any{
				"thinking_enabled": true,
				"thinking_budget":  5000,
			},
			expectType:     "adaptive",
			expectNoConfig: false,
		},
		{
			name:  "fable 5 requests summarized adaptive thinking",
			model: "claude-fable-5",
			metadata: map[string]any{
				"thinking_enabled": true,
			},
			expectType:    "adaptive",
			expectDisplay: "summarized",
		},
		{
			name:  "opus 4.7 requests summarized adaptive thinking",
			model: "claude-opus-4-7",
			metadata: map[string]any{
				"thinking_enabled": true,
			},
			expectType:    "adaptive",
			expectDisplay: "summarized",
		},
		{
			name:  "sonnet 4.6 uses adaptive thinking",
			model: "claude-sonnet-4-6",
			metadata: map[string]any{
				"thinking_enabled": true,
			},
			expectType: "adaptive",
		},
		{
			name:  "sonnet 5 requests summarized adaptive thinking",
			model: "claude-sonnet-5-20260301",
			metadata: map[string]any{
				"thinking_enabled": true,
			},
			expectType:    "adaptive",
			expectDisplay: "summarized",
		},
		{
			name:  "opus 4.5 uses manual mode with budget",
			model: "claude-opus-4-5-20251101",
			metadata: map[string]any{
				"thinking_enabled": true,
				"thinking_budget":  3000,
			},
			expectType:   "enabled",
			expectBudget: 3000,
		},
		{
			name:  "sonnet 4.5 uses manual mode with default budget",
			model: "claude-sonnet-4-5-20250929",
			metadata: map[string]any{
				"thinking_enabled": true,
			},
			expectType:   "enabled",
			expectBudget: 2048,
		},
		{
			name:  "haiku uses manual mode",
			model: "claude-haiku-4-5-20251001",
			metadata: map[string]any{
				"thinking_enabled": true,
				"thinking_budget":  1500,
			},
			expectType:   "enabled",
			expectBudget: 1500,
		},
		{
			name:  "thinking disabled",
			model: "claude-opus-4-6",
			metadata: map[string]any{
				"thinking_enabled": false,
			},
			expectNoConfig: true,
		},
		{
			name:           "no thinking metadata",
			model:          "claude-opus-4-6",
			metadata:       map[string]any{},
			expectNoConfig: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := provider.ChatRequest{
				Model: tt.model,
				Messages: []*conversation.Message{
					{
						Role:    "user",
						Content: "Test message",
					},
				},
				SystemPrompt: "Test system",
				Metadata:     tt.metadata,
			}

			anthropicReq, _, err := translateRequest(ctx, req, false, "", logger, nil)
			if err != nil {
				t.Fatalf("translateRequest failed: %v", err)
			}

			if tt.expectNoConfig {
				if anthropicReq.Thinking != nil {
					t.Errorf("Expected no thinking config, got %+v", anthropicReq.Thinking)
				}
				return
			}

			if anthropicReq.Thinking == nil {
				t.Fatal("Expected thinking config, got nil")
			}

			if anthropicReq.Thinking.Type != tt.expectType {
				t.Errorf("Thinking type = %v, want %v", anthropicReq.Thinking.Type, tt.expectType)
			}

			if tt.expectType == "enabled" {
				if anthropicReq.Thinking.BudgetTokens != tt.expectBudget {
					t.Errorf("BudgetTokens = %v, want %v", anthropicReq.Thinking.BudgetTokens, tt.expectBudget)
				}
			} else if tt.expectType == "adaptive" {
				// Adaptive mode should not have budget_tokens set
				if anthropicReq.Thinking.BudgetTokens != 0 {
					t.Errorf("Adaptive mode should not have BudgetTokens, got %v", anthropicReq.Thinking.BudgetTokens)
				}
				if anthropicReq.Thinking.Display != tt.expectDisplay {
					t.Errorf("Thinking display = %q, want %q", anthropicReq.Thinking.Display, tt.expectDisplay)
				}
			}
		})
	}
}

func TestTranslateRequest_EffortParameter(t *testing.T) {
	ctx := context.Background()
	logger := observability.NewNopLogger()

	tests := []struct {
		name           string
		model          string
		metadata       map[string]any
		expectEffort   string
		expectNoConfig bool
	}{
		{
			name:  "opus 4.6 with high effort",
			model: "claude-opus-4-6",
			metadata: map[string]any{
				"thinking_effort": "high",
			},
			expectEffort: "high",
		},
		{
			name:  "opus 4.6 with max effort",
			model: "claude-opus-4-6",
			metadata: map[string]any{
				"thinking_effort": "max",
			},
			expectEffort: "max",
		},
		{
			name:  "opus 4.6 with medium effort",
			model: "claude-opus-4-6",
			metadata: map[string]any{
				"thinking_effort": "medium",
			},
			expectEffort: "medium",
		},
		{
			name:  "opus 4.6 with low effort",
			model: "claude-opus-4-6",
			metadata: map[string]any{
				"thinking_effort": "low",
			},
			expectEffort: "low",
		},
		{
			name:  "opus 4.5 with max effort should be ignored (not supported)",
			model: "claude-opus-4-5-20251101",
			metadata: map[string]any{
				"thinking_effort": "max",
			},
			expectNoConfig: true,
		},
		{
			name:  "opus 4.5 with high effort is valid",
			model: "claude-opus-4-5-20251101",
			metadata: map[string]any{
				"thinking_effort": "high",
			},
			expectEffort: "high",
		},
		{
			name:  "sonnet with medium effort",
			model: "claude-sonnet-4-5-20250929",
			metadata: map[string]any{
				"thinking_effort": "medium",
			},
			expectEffort: "medium",
		},
		{
			name:  "no effort parameter",
			model: "claude-opus-4-6",
			metadata: map[string]any{
				"thinking_enabled": true,
			},
			expectNoConfig: true,
		},
		{
			name:  "effort normalization - uppercase",
			model: "claude-opus-4-6",
			metadata: map[string]any{
				"thinking_effort": "HIGH",
			},
			expectEffort: "high",
		},
		{
			name:  "effort normalization - with spaces",
			model: "claude-opus-4-6",
			metadata: map[string]any{
				"thinking_effort": "  medium  ",
			},
			expectEffort: "medium",
		},
		{
			name:  "invalid effort level",
			model: "claude-opus-4-6",
			metadata: map[string]any{
				"thinking_effort": "ultra",
			},
			expectNoConfig: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := provider.ChatRequest{
				Model: tt.model,
				Messages: []*conversation.Message{
					{
						Role:    "user",
						Content: "Test message",
					},
				},
				SystemPrompt: "Test system",
				Metadata:     tt.metadata,
			}

			anthropicReq, _, err := translateRequest(ctx, req, false, "", logger, nil)
			if err != nil {
				t.Fatalf("translateRequest failed: %v", err)
			}

			if tt.expectNoConfig {
				if anthropicReq.OutputConfig != nil {
					t.Errorf("Expected no output config, got %+v", anthropicReq.OutputConfig)
				}
				return
			}

			if anthropicReq.OutputConfig == nil {
				t.Fatal("Expected output config, got nil")
			}

			if anthropicReq.OutputConfig.Effort != tt.expectEffort {
				t.Errorf("Effort = %v, want %v", anthropicReq.OutputConfig.Effort, tt.expectEffort)
			}
		})
	}
}

func TestTranslateRequest_AdaptiveThinkingWithEffort(t *testing.T) {
	ctx := context.Background()
	logger := observability.NewNopLogger()

	// Test that adaptive thinking and effort work together
	req := provider.ChatRequest{
		Model: "claude-opus-4-6",
		Messages: []*conversation.Message{
			{
				Role:    "user",
				Content: "Explain quantum computing",
			},
		},
		SystemPrompt: "You are a helpful assistant",
		Metadata: map[string]any{
			"thinking_enabled": true,
			"thinking_effort":  "medium",
		},
	}

	anthropicReq, providerJSON, err := translateRequest(ctx, req, false, "", logger, nil)
	if err != nil {
		t.Fatalf("translateRequest failed: %v", err)
	}

	// Verify thinking config
	if anthropicReq.Thinking == nil {
		t.Fatal("Expected thinking config, got nil")
	}
	if anthropicReq.Thinking.Type != "adaptive" {
		t.Errorf("Thinking type = %v, want adaptive", anthropicReq.Thinking.Type)
	}

	// Verify output config
	if anthropicReq.OutputConfig == nil {
		t.Fatal("Expected output config, got nil")
	}
	if anthropicReq.OutputConfig.Effort != "medium" {
		t.Errorf("Effort = %v, want medium", anthropicReq.OutputConfig.Effort)
	}

	// Verify provider JSON includes both
	if providerJSON["thinking"] == nil {
		t.Error("Provider JSON missing thinking config")
	}
	if providerJSON["output_config"] == nil {
		t.Error("Provider JSON missing output_config")
	}
}
