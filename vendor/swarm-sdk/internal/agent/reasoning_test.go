package agent

import (
	"testing"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/provider"
)

func TestResolveReasoningEffortFromMetadata(t *testing.T) {
	tests := []struct {
		name     string
		metadata map[string]any
		want     string
	}{
		{
			name: "disable_reasoning wins",
			metadata: map[string]any{
				"reasoning_effort":  "high",
				"disable_reasoning": true,
			},
			want: provider.ReasoningEffortNone,
		},
		{
			name: "reasoning_effort alias med",
			metadata: map[string]any{
				"reasoning_effort": "med",
			},
			want: provider.ReasoningEffortMedium,
		},
		{
			name: "legacy reasoning_level",
			metadata: map[string]any{
				"reasoning_level": "x-high",
			},
			want: provider.ReasoningEffortXHigh,
		},
		{
			name: "auto omitted",
			metadata: map[string]any{
				"reasoning_effort": "auto",
			},
			want: "",
		},
		{
			name:     "missing",
			metadata: map[string]any{},
			want:     "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := resolveReasoningEffortFromMetadata(tt.metadata)
			if got != tt.want {
				t.Fatalf("resolveReasoningEffortFromMetadata()=%q want %q", got, tt.want)
			}
		})
	}
}
