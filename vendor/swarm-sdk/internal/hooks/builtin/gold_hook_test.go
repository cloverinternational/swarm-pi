package builtin

import (
	"testing"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/hooks"
)

func TestGoldHookFiltersSilverTreeBuiltAndAgentStopped(t *testing.T) {
	hook := NewGoldHook(GoldConfig{Enabled: true}, nil)

	if !hook.Filter(hooks.Event{Type: hooks.EventSilverTreeBuilt}) {
		t.Fatal("GoldHook should trigger from silver.tree_built")
	}
	if !hook.Filter(hooks.Event{Type: hooks.EventAgentStopped}) {
		t.Fatal("GoldHook should keep agent.stopped fallback")
	}
	if hook.Filter(hooks.Event{Type: hooks.EventToolAfterExecute}) {
		t.Fatal("GoldHook should not trigger from unrelated events")
	}
}

func TestClassifyGoldRunOutcome(t *testing.T) {
	tests := []struct {
		name              string
		treesAnalyzed     int
		insightsGenerated int
		want              string
	}{
		{name: "no trees", treesAnalyzed: 0, insightsGenerated: 0, want: "no_trees"},
		{name: "no patterns", treesAnalyzed: 1, insightsGenerated: 0, want: "no_patterns"},
		{name: "insights", treesAnalyzed: 1, insightsGenerated: 2, want: "insights_generated"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := classifyGoldRunOutcome(tt.treesAnalyzed, tt.insightsGenerated); got != tt.want {
				t.Fatalf("classifyGoldRunOutcome: got %q, want %q", got, tt.want)
			}
		})
	}
}
