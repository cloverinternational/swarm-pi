package openai

import (
	"testing"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/provider"
)

func TestTranslateRequestNormalizesReasoningEffort(t *testing.T) {
	tests := []struct {
		name       string
		model      string
		effort     string
		metadata   map[string]any
		wantEffort string
		wantNil    bool
		topLevel   bool
	}{
		{name: "auto omitted", model: "gpt-5.6-terra", effort: "auto", wantNil: true},
		{name: "alias canonicalized", model: "gpt-5.6-terra", effort: "med", wantEffort: provider.ReasoningEffortMedium},
		{name: "explicit effort beats thinking budget", model: "gpt-5.6-terra", effort: "med", metadata: map[string]any{"thinking_enabled": true, "thinking_budget": 10000}, wantEffort: provider.ReasoningEffortMedium},
		{name: "ultra maps to max on 5.6", model: "gpt-5.6-terra", effort: "ultra", wantEffort: provider.ReasoningEffortMax},
		{name: "max clamps on older model", model: "gpt-5.5", effort: "max", wantEffort: provider.ReasoningEffortXHigh},
		{name: "compatible provider uses top-level effort", model: "qwen-3", effort: "med", wantEffort: provider.ReasoningEffortMedium, topLevel: true},
		{name: "compatible explicit effort beats budget", model: "qwen-3", effort: "med", metadata: map[string]any{"thinking_enabled": true, "thinking_budget": 10000}, wantEffort: provider.ReasoningEffortMedium, topLevel: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req, err := TranslateRequest(provider.ChatRequest{Model: tt.model, ReasoningEffort: tt.effort, Metadata: tt.metadata})
			if err != nil {
				t.Fatalf("TranslateRequest: %v", err)
			}
			if tt.wantNil {
				if req.Reasoning != nil {
					t.Fatalf("Reasoning = %+v, want nil", req.Reasoning)
				}
				return
			}
			if tt.topLevel {
				if req.Reasoning != nil || req.ReasoningEffort == nil || *req.ReasoningEffort != tt.wantEffort {
					t.Fatalf("Reasoning=%+v ReasoningEffort=%v, want top-level %q", req.Reasoning, req.ReasoningEffort, tt.wantEffort)
				}
				return
			}
			if req.Reasoning == nil || req.Reasoning.Effort != tt.wantEffort || req.ReasoningEffort != nil {
				t.Fatalf("Reasoning=%+v ReasoningEffort=%v, want nested effort %q", req.Reasoning, req.ReasoningEffort, tt.wantEffort)
			}
		})
	}
}
