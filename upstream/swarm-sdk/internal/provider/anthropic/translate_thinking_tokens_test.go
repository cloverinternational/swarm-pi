package anthropic

import (
	"context"
	"math"
	"strings"
	"testing"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/conversation"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/observability"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/provider"
)

func TestTranslateRequest_ManualThinkingTokenCoherence(t *testing.T) {
	tests := []struct {
		name             string
		model            string
		thinkingEnabled  bool
		thinkingBudget   int
		maxTokens        int
		maxTokensSet     bool
		wantMaxTokens    int
		wantThinkingType string
		wantBudget       int
		wantError        string
	}{
		{
			name:             "default max grows above 10k budget",
			model:            "claude-sonnet-4-5-20250929",
			thinkingEnabled:  true,
			thinkingBudget:   10000,
			wantMaxTokens:    10001,
			wantThinkingType: "enabled",
			wantBudget:       10000,
		},
		{
			name:            "explicit max below budget is rejected",
			model:           "claude-sonnet-4-5-20250929",
			thinkingEnabled: true,
			thinkingBudget:  10000,
			maxTokens:       9999,
			maxTokensSet:    true,
			wantError:       "thinking budget_tokens (10000) must be less than explicit max_tokens (9999)",
		},
		{
			name:            "explicit max equal to budget is rejected",
			model:           "claude-sonnet-4-5-20250929",
			thinkingEnabled: true,
			thinkingBudget:  10000,
			maxTokens:       10000,
			maxTokensSet:    true,
			wantError:       "thinking budget_tokens (10000) must be less than explicit max_tokens (10000)",
		},
		{
			name:             "explicit max one above budget is preserved",
			model:            "claude-sonnet-4-5-20250929",
			thinkingEnabled:  true,
			thinkingBudget:   10000,
			maxTokens:        10001,
			maxTokensSet:     true,
			wantMaxTokens:    10001,
			wantThinkingType: "enabled",
			wantBudget:       10000,
		},
		{
			name:             "minimum manual budget keeps provider default",
			model:            "claude-sonnet-4-5-20250929",
			thinkingEnabled:  true,
			thinkingBudget:   1024,
			wantMaxTokens:    4096,
			wantThinkingType: "enabled",
			wantBudget:       1024,
		},
		{
			name:             "minimum valid explicit pair is preserved",
			model:            "claude-sonnet-4-5-20250929",
			thinkingEnabled:  true,
			thinkingBudget:   1024,
			maxTokens:        1025,
			maxTokensSet:     true,
			wantMaxTokens:    1025,
			wantThinkingType: "enabled",
			wantBudget:       1024,
		},
		{
			name:            "default max cannot grow past integer boundary",
			model:           "claude-sonnet-4-5-20250929",
			thinkingEnabled: true,
			thinkingBudget:  math.MaxInt,
			wantError:       "thinking budget_tokens is too large to derive max_tokens",
		},
		{
			name:             "thinking disabled leaves default max unchanged",
			model:            "claude-sonnet-4-5-20250929",
			thinkingEnabled:  false,
			thinkingBudget:   10000,
			wantMaxTokens:    4096,
			wantThinkingType: "",
		},
		{
			name:             "adaptive thinking ignores manual budget with default max",
			model:            "claude-opus-4-6",
			thinkingEnabled:  true,
			thinkingBudget:   10000,
			wantMaxTokens:    4096,
			wantThinkingType: "adaptive",
		},
		{
			name:             "adaptive thinking preserves explicit max below metadata budget",
			model:            "claude-opus-4-6",
			thinkingEnabled:  true,
			thinkingBudget:   10000,
			maxTokens:        1024,
			maxTokensSet:     true,
			wantMaxTokens:    1024,
			wantThinkingType: "adaptive",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := provider.ChatRequest{
				Model: test.model,
				Messages: []*conversation.Message{
					{Role: "user", Content: "Test message"},
				},
				Metadata: map[string]any{
					"thinking_enabled": test.thinkingEnabled,
					"thinking_budget":  test.thinkingBudget,
				},
			}
			if test.maxTokensSet {
				maxTokens := test.maxTokens
				request.MaxTokens = &maxTokens
			}

			translated, providerJSON, err := translateRequest(
				context.Background(), request, false, "", observability.NewNopLogger(), nil,
			)
			if test.wantError != "" {
				if err == nil {
					t.Fatalf("translateRequest() error = nil, want error containing %q", test.wantError)
				}
				if !strings.Contains(err.Error(), test.wantError) {
					t.Fatalf("translateRequest() error = %q, want error containing %q", err, test.wantError)
				}
				if translated != nil || providerJSON != nil {
					t.Fatalf("translateRequest() returned request shape on error: request=%+v providerJSON=%+v", translated, providerJSON)
				}
				return
			}
			if err != nil {
				t.Fatalf("translateRequest() unexpected error: %v", err)
			}

			if translated.MaxTokens != test.wantMaxTokens {
				t.Errorf("MaxTokens = %d, want %d", translated.MaxTokens, test.wantMaxTokens)
			}
			if got, ok := providerJSON["max_tokens"].(int); !ok || got != test.wantMaxTokens {
				t.Errorf("providerJSON[max_tokens] = %#v, want %d", providerJSON["max_tokens"], test.wantMaxTokens)
			}

			if test.wantThinkingType == "" {
				if translated.Thinking != nil {
					t.Fatalf("Thinking = %+v, want nil", translated.Thinking)
				}
				if _, ok := providerJSON["thinking"]; ok {
					t.Fatalf("providerJSON unexpectedly contains thinking: %#v", providerJSON["thinking"])
				}
				return
			}

			if translated.Thinking == nil {
				t.Fatal("Thinking = nil, want config")
			}
			if translated.Thinking.Type != test.wantThinkingType {
				t.Errorf("Thinking.Type = %q, want %q", translated.Thinking.Type, test.wantThinkingType)
			}
			if translated.Thinking.BudgetTokens != test.wantBudget {
				t.Errorf("Thinking.BudgetTokens = %d, want %d", translated.Thinking.BudgetTokens, test.wantBudget)
			}
			if got, ok := providerJSON["thinking"].(*ThinkingConfig); !ok || got != translated.Thinking {
				t.Errorf("providerJSON[thinking] = %#v, want translated thinking config", providerJSON["thinking"])
			}
		})
	}
}
