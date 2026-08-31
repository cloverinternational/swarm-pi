package anthropic

import (
	"context"
	"testing"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/conversation"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/observability"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/provider"
)

// TestTranslateRequest_Opus47_TemperatureDropped verifies that Opus 4.7 silently
// drops a caller-supplied temperature (Opus 4.7 returns HTTP 400 on any non-default
// sampling param). Older Claude models must still propagate temperature unchanged.
func TestTranslateRequest_Opus47_TemperatureDropped(t *testing.T) {
	ctx := context.Background()
	logger := observability.NewNopLogger()
	temp := 0.7

	tests := []struct {
		name              string
		model             string
		expectTemperature bool
	}{
		{"opus 4.7 drops temperature", "claude-opus-4-7", false},
		{"opus 4.7 with date drops temperature", "claude-opus-4-7-20260416", false},
		{"opus 4.6 keeps temperature", "claude-opus-4-6", true},
		{"opus 4.5 keeps temperature", "claude-opus-4-5-20251101", true},
		{"sonnet 4.5 keeps temperature", "claude-sonnet-4-5-20250929", true},
		{"haiku 4.5 keeps temperature", "claude-haiku-4-5-20251001", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := provider.ChatRequest{
				Model:       tt.model,
				Temperature: &temp,
				Messages: []*conversation.Message{
					{Role: "user", Content: "hi"},
				},
			}
			anthropicReq, _, err := translateRequest(ctx, req, false, "", logger, nil)
			if err != nil {
				t.Fatalf("translateRequest failed: %v", err)
			}
			gotTemp := anthropicReq.Temperature != nil
			if gotTemp != tt.expectTemperature {
				t.Errorf("temperature present=%v, want %v (model=%s)", gotTemp, tt.expectTemperature, tt.model)
			}
		})
	}
}

// TestTranslateRequest_Opus47_AdaptiveOnly confirms Opus 4.7 always lands on
// adaptive thinking (the manual `enabled` mode would return HTTP 400). The
// adaptive routing is shared with Opus 4.6+, so this is a regression guard
// rather than a new code path.
func TestTranslateRequest_Opus47_AdaptiveOnly(t *testing.T) {
	ctx := context.Background()
	logger := observability.NewNopLogger()

	req := provider.ChatRequest{
		Model: "claude-opus-4-7",
		Messages: []*conversation.Message{
			{Role: "user", Content: "hi"},
		},
		Metadata: map[string]any{
			"thinking_enabled": true,
			"thinking_budget":  4096, // would be honored on older models, must be ignored on 4.7
		},
	}
	anthropicReq, _, err := translateRequest(ctx, req, false, "", logger, nil)
	if err != nil {
		t.Fatalf("translateRequest failed: %v", err)
	}
	if anthropicReq.Thinking == nil {
		t.Fatal("expected thinking config")
	}
	if anthropicReq.Thinking.Type != "adaptive" {
		t.Errorf("thinking.type=%q, want adaptive", anthropicReq.Thinking.Type)
	}
	if anthropicReq.Thinking.BudgetTokens != 0 {
		t.Errorf("budget_tokens=%d, want 0 (forbidden on Opus 4.7)", anthropicReq.Thinking.BudgetTokens)
	}
}

// TestTranslateRequest_XHighEffort verifies xhigh is accepted only on Opus 4.7.
func TestTranslateRequest_XHighEffort(t *testing.T) {
	ctx := context.Background()
	logger := observability.NewNopLogger()

	tests := []struct {
		name         string
		model        string
		expectEffort string // "" means OutputConfig should be nil
	}{
		{"opus 4.7 accepts xhigh", "claude-opus-4-7", "xhigh"},
		{"opus 4.6 rejects xhigh", "claude-opus-4-6", ""},
		{"sonnet 4.5 rejects xhigh", "claude-sonnet-4-5-20250929", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := provider.ChatRequest{
				Model: tt.model,
				Messages: []*conversation.Message{
					{Role: "user", Content: "hi"},
				},
				Metadata: map[string]any{"thinking_effort": "xhigh"},
			}
			anthropicReq, _, err := translateRequest(ctx, req, false, "", logger, nil)
			if err != nil {
				t.Fatalf("translateRequest failed: %v", err)
			}
			if tt.expectEffort == "" {
				if anthropicReq.OutputConfig != nil {
					t.Errorf("expected no output_config, got effort=%q", anthropicReq.OutputConfig.Effort)
				}
				return
			}
			if anthropicReq.OutputConfig == nil {
				t.Fatal("expected output_config, got nil")
			}
			if anthropicReq.OutputConfig.Effort != tt.expectEffort {
				t.Errorf("effort=%q, want %q", anthropicReq.OutputConfig.Effort, tt.expectEffort)
			}
		})
	}
}
