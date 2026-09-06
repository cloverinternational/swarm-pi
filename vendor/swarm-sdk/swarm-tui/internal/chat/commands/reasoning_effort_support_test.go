package commands

import (
	"testing"

	sdkprovider "github.com/Swarm-Code/mono/swarm-sdk/internal/provider"
)

func boolPtr(value bool) *bool {
	v := value
	return &v
}

func TestResolveReasoningEffortsForModel_UsesExplicitEfforts(t *testing.T) {
	got := ResolveReasoningEffortsForModel(
		"openai",
		"OpenAI",
		"gpt-5.1",
		nil,
		[]string{"high", "x-high", "medium", "invalid", "high"},
	)
	want := []string{sdkprovider.ReasoningEffortMedium, sdkprovider.ReasoningEffortHigh, sdkprovider.ReasoningEffortXHigh}

	if len(got) != len(want) {
		t.Fatalf("expected %d efforts, got %d (%v)", len(want), len(got), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("expected effort[%d]=%q, got %q", i, want[i], got[i])
		}
	}
}

func TestResolveReasoningEffortsForModel_ExplicitUnsupportedWins(t *testing.T) {
	got := ResolveReasoningEffortsForModel(
		"openai",
		"OpenAI",
		"gpt-5.1-codex",
		boolPtr(false),
		nil,
	)
	if len(got) != 0 {
		t.Fatalf("expected no efforts, got %v", got)
	}
}

func TestResolveReasoningEffortsForModel_InferGPT5Codex(t *testing.T) {
	got := ResolveReasoningEffortsForModel(
		"openai",
		"OpenAI",
		"gpt-5.1-codex",
		nil,
		nil,
	)
	if len(got) == 0 {
		t.Fatalf("expected inferred efforts")
	}
	if got[len(got)-1] != sdkprovider.ReasoningEffortXHigh {
		t.Fatalf("expected xhigh support for codex, got %v", got)
	}
}

func TestResolveReasoningEffortsForModel_InferGPT55GetsXHigh(t *testing.T) {
	got := ResolveReasoningEffortsForModel(
		"openai",
		"OpenAI",
		"gpt-5.5",
		nil,
		nil,
	)
	if len(got) == 0 {
		t.Fatalf("expected inferred efforts")
	}
	if got[len(got)-1] != sdkprovider.ReasoningEffortXHigh {
		t.Fatalf("expected xhigh support for gpt-5.5, got %v", got)
	}
	for _, effort := range got {
		if effort == sdkprovider.ReasoningEffortMax {
			t.Fatalf("did not expect max support for gpt-5.5, got %v", got)
		}
	}
}

func TestResolveReasoningEffortsForModel_InferGPT56GetsMax(t *testing.T) {
	got := ResolveReasoningEffortsForModel(
		"openai",
		"OpenAI",
		"gpt-5.6-sol",
		nil,
		nil,
	)
	if len(got) == 0 {
		t.Fatalf("expected inferred efforts")
	}
	if got[len(got)-1] != sdkprovider.ReasoningEffortMax {
		t.Fatalf("expected max support for gpt-5.6-sol, got %v", got)
	}
	foundXHigh := false
	for _, effort := range got {
		if effort == sdkprovider.ReasoningEffortXHigh {
			foundXHigh = true
		}
	}
	if !foundXHigh {
		t.Fatalf("expected xhigh support for gpt-5.6-sol, got %v", got)
	}
}

func TestResolveReasoningEffortsForModel_ExplicitMetadataWinsOverInference(t *testing.T) {
	got := ResolveReasoningEffortsForModel(
		"openai",
		"OpenAI",
		"gpt-5.6-terra",
		nil,
		[]string{"medium", "high"},
	)
	want := []string{sdkprovider.ReasoningEffortMedium, sdkprovider.ReasoningEffortHigh}
	if len(got) != len(want) {
		t.Fatalf("expected explicit efforts %v to win, got %v", want, got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("expected effort[%d]=%q, got %q", i, want[i], got[i])
		}
	}
}

func TestResolveReasoningEffortsForModel_NonOpenAIUnsupported(t *testing.T) {
	got := ResolveReasoningEffortsForModel(
		"anthropic",
		"ClaudeCode",
		"claude-opus-4-20250514",
		nil,
		nil,
	)
	if len(got) != 0 {
		t.Fatalf("expected no efforts, got %v", got)
	}
}

func TestAvailableReasoningEffortSettings_IncludesAuto(t *testing.T) {
	got := AvailableReasoningEffortSettings(
		"openai",
		"OpenAI",
		"gpt-5.1",
		nil,
		nil,
	)
	if len(got) < 2 {
		t.Fatalf("expected auto + inferred efforts, got %v", got)
	}
	if got[0] != sdkprovider.ReasoningEffortAuto {
		t.Fatalf("expected first option auto, got %q", got[0])
	}
}
