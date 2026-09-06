package gemini

import (
	"encoding/json"
	"testing"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/conversation"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/provider"
)

func TestThinkingConfigForGemini3Models(t *testing.T) {
	req := provider.ChatRequest{
		Model:        "gemini-3-flash-preview",
		SystemPrompt: "You are a helpful assistant.",
		Messages: []*conversation.Message{
			{
				Role:    conversation.RoleUser,
				Content: "Hello!",
			},
		},
	}

	geminiReq := TranslateRequest(req, "test-project", "test-session")

	// Verify model is correctly formatted (no "models/" prefix for cloudcode-pa endpoint)
	if geminiReq.Model != "gemini-3-flash-preview" {
		t.Errorf("expected model 'gemini-3-flash-preview', got '%s'", geminiReq.Model)
	}

	// Verify thinkingConfig is present
	if geminiReq.Request.GenerationConfig == nil {
		t.Fatal("expected GenerationConfig to be non-nil")
	}

	if geminiReq.Request.GenerationConfig.ThinkingConfig == nil {
		t.Fatal("expected ThinkingConfig to be non-nil for gemini-3 model")
	}

	tc := geminiReq.Request.GenerationConfig.ThinkingConfig
	if tc.ThinkingLevel != "HIGH" {
		t.Errorf("expected ThinkingLevel 'HIGH', got '%s'", tc.ThinkingLevel)
	}

	if tc.IncludeThoughts == nil || !*tc.IncludeThoughts {
		t.Error("expected IncludeThoughts to be true")
	}

	// Verify JSON output has correct structure
	jsonData, err := json.MarshalIndent(geminiReq, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("Request JSON:\n%s", string(jsonData))
}

func TestThinkingConfigForGemini25Models(t *testing.T) {
	req := provider.ChatRequest{
		Model:        "gemini-2.5-pro",
		SystemPrompt: "You are a helpful assistant.",
		Messages: []*conversation.Message{
			{
				Role:    conversation.RoleUser,
				Content: "Hello!",
			},
		},
	}

	geminiReq := TranslateRequest(req, "test-project", "test-session")

	// Verify thinkingConfig is present with budget
	if geminiReq.Request.GenerationConfig == nil {
		t.Fatal("expected GenerationConfig to be non-nil")
	}

	if geminiReq.Request.GenerationConfig.ThinkingConfig == nil {
		t.Fatal("expected ThinkingConfig to be non-nil for gemini-2.5 model")
	}

	tc := geminiReq.Request.GenerationConfig.ThinkingConfig
	if tc.ThinkingBudget == nil || *tc.ThinkingBudget != 8192 {
		t.Errorf("expected ThinkingBudget 8192 (capped), got %v", tc.ThinkingBudget)
	}

	if tc.IncludeThoughts == nil || !*tc.IncludeThoughts {
		t.Error("expected IncludeThoughts to be true")
	}

	// Verify temperature keeps its existing default while optional sampling controls are omitted.
	gc := geminiReq.Request.GenerationConfig
	if gc.Temperature == nil || *gc.Temperature != 1.0 {
		t.Errorf("expected Temperature 1.0, got %v", gc.Temperature)
	}
	if gc.TopP != nil {
		t.Errorf("expected nil TopP, got %v", gc.TopP)
	}
	if gc.TopK != nil {
		t.Errorf("expected nil TopK, got %v", gc.TopK)
	}

	// Verify JSON output has correct structure
	jsonData, err := json.MarshalIndent(geminiReq, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("Request JSON:\n%s", string(jsonData))
}

func TestThinkingDisabledGemini25(t *testing.T) {
	req := provider.ChatRequest{
		Model:        "gemini-2.5-pro",
		SystemPrompt: "You are a helpful assistant.",
		Messages: []*conversation.Message{
			{
				Role:    conversation.RoleUser,
				Content: "Hello!",
			},
		},
		Metadata: map[string]any{
			"thinking_enabled": false,
		},
	}

	geminiReq := TranslateRequest(req, "test-project", "test-session")

	if geminiReq.Request.GenerationConfig == nil {
		t.Fatal("expected GenerationConfig to be non-nil")
	}
	if geminiReq.Request.GenerationConfig.ThinkingConfig == nil {
		t.Fatal("expected ThinkingConfig to be non-nil (with budget=0)")
	}

	tc := geminiReq.Request.GenerationConfig.ThinkingConfig
	if tc.ThinkingBudget == nil || *tc.ThinkingBudget != 0 {
		t.Errorf("expected ThinkingBudget 0 (disabled), got %v", tc.ThinkingBudget)
	}
	if tc.IncludeThoughts == nil || *tc.IncludeThoughts {
		t.Error("expected IncludeThoughts to be false when thinking disabled")
	}
}

func TestThinkingDisabledGemini3(t *testing.T) {
	req := provider.ChatRequest{
		Model:        "gemini-3-pro-preview",
		SystemPrompt: "You are a helpful assistant.",
		Messages: []*conversation.Message{
			{
				Role:    conversation.RoleUser,
				Content: "Hello!",
			},
		},
		Metadata: map[string]any{
			"thinking_enabled": false,
		},
	}

	geminiReq := TranslateRequest(req, "test-project", "test-session")

	if geminiReq.Request.GenerationConfig == nil {
		t.Fatal("expected GenerationConfig to be non-nil")
	}
	// When thinking is disabled for gemini-3, we return nil to omit thinkingConfig entirely
	// The API rejects "NONE" as a thinking level value
	if geminiReq.Request.GenerationConfig.ThinkingConfig != nil {
		t.Fatal("expected ThinkingConfig to be nil when thinking disabled (API rejects 'NONE' value)")
	}
}

func TestThinkingConfigNotAddedForOlderModels(t *testing.T) {
	req := provider.ChatRequest{
		Model:        "gemini-2.0-flash",
		SystemPrompt: "You are a helpful assistant.",
		Messages: []*conversation.Message{
			{
				Role:    conversation.RoleUser,
				Content: "Hello!",
			},
		},
	}

	geminiReq := TranslateRequest(req, "test-project", "test-session")

	// For older models, thinkingConfig should still be present with includeThoughts only
	if geminiReq.Request.GenerationConfig != nil && geminiReq.Request.GenerationConfig.ThinkingConfig != nil {
		tc := geminiReq.Request.GenerationConfig.ThinkingConfig
		// Should only have includeThoughts, not level or budget
		if tc.ThinkingLevel != "" {
			t.Errorf("expected no ThinkingLevel for gemini-2.0, got '%s'", tc.ThinkingLevel)
		}
		if tc.ThinkingBudget != nil {
			t.Errorf("expected no ThinkingBudget for gemini-2.0, got %v", *tc.ThinkingBudget)
		}
	}
}
