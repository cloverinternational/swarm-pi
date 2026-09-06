package quick_test

import (
	"strings"
	"testing"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/quick"
)

func TestNewAgent_UnrecognisedModel(t *testing.T) {
	_, err := quick.NewAgent("llama3-8b")
	if err == nil {
		t.Fatal("expected error for unrecognised model prefix, got nil")
	}
	if !strings.Contains(err.Error(), "unrecognised model prefix") {
		t.Errorf("unexpected error message: %v", err)
	}
}

func TestNewAgent_MissingAnthropicKey(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "")
	_, err := quick.NewAgent("claude-sonnet-4-5")
	if err == nil {
		t.Fatal("expected error when ANTHROPIC_API_KEY is unset, got nil")
	}
	if !strings.Contains(err.Error(), "ANTHROPIC_API_KEY") {
		t.Errorf("error should mention env var, got: %v", err)
	}
}

func TestNewAgent_MissingOpenAIKey(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "")
	_, err := quick.NewAgent("gpt-4o")
	if err == nil {
		t.Fatal("expected error when OPENAI_API_KEY is unset, got nil")
	}
	if !strings.Contains(err.Error(), "OPENAI_API_KEY") {
		t.Errorf("error should mention env var, got: %v", err)
	}
}

func TestNewAgent_MissingGeminiKey(t *testing.T) {
	t.Setenv("GEMINI_API_KEY", "")
	_, err := quick.NewAgent("gemini-2.0-flash")
	if err == nil {
		t.Fatal("expected error when GEMINI_API_KEY is unset, got nil")
	}
	if !strings.Contains(err.Error(), "GEMINI_API_KEY") {
		t.Errorf("error should mention env var, got: %v", err)
	}
}
