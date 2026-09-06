package agent

import (
	"testing"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/tools"
)

func TestProviderToolsForContextMatchesRequestToolFiltering(t *testing.T) {
	reg := tools.NewRegistry()
	for _, name := range []string{"Bash", "Read", "Write"} {
		if err := reg.Register(&stubTool{name: name}); err != nil {
			t.Fatalf("register %s: %v", name, err)
		}
	}
	a := newOverrideTestAgent(t, reg)
	ctx := map[string]any{
		"mode_filter": map[string]any{
			"mode_name":          "PLAN",
			"allowed_tools":      []string{"Read"},
			"blocked_tools":      []string{"Write"},
			"hide_blocked_tools": true,
		},
	}

	got := a.ProviderToolsForContext(ctx, false)
	if len(got) != 1 || got[0].Name != "Read" {
		t.Fatalf("ProviderToolsForContext = %#v, want only Read", got)
	}

	a.requestContext = ctx
	requestTools := a.buildProviderRequest(nil).Tools
	if len(requestTools) != len(got) || requestTools[0].Name != got[0].Name {
		t.Fatalf("request tools = %#v, manifest tools = %#v", requestTools, got)
	}
}

func TestProviderToolsForContextHonorsDisableToolsWithoutMutatingAgent(t *testing.T) {
	reg := tools.NewRegistry()
	if err := reg.Register(&stubTool{name: "Read"}); err != nil {
		t.Fatal(err)
	}
	a := newOverrideTestAgent(t, reg)

	if got := a.ProviderToolsForContext(nil, true); len(got) != 0 {
		t.Fatalf("disabled tools = %#v, want none", got)
	}
	if got := a.ProviderToolsForContext(nil, false); len(got) != 1 {
		t.Fatalf("enabled tools = %#v, want Read", got)
	}
	if a.reqDisableTools {
		t.Fatal("ProviderToolsForContext mutated reqDisableTools")
	}
}
