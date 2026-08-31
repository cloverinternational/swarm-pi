package chat

import (
	"strings"
	"testing"
)

func TestBuildSwarmFlowGuidance(t *testing.T) {
	g := buildSwarmFlowGuidance()
	if g == "" {
		t.Fatal("guidance is empty")
	}
	// Wrapped so it is a recognizable, strippable runtime-guidance section.
	if !strings.HasPrefix(g, "<swarm_flow_capability>") || !strings.HasSuffix(g, "</swarm_flow_capability>") {
		t.Fatalf("guidance not wrapped in <swarm_flow_capability>: %.60q…", g)
	}
	// Must actually teach the tool: name, ownership header, disjointness, run cmd.
	for _, must := range []string{"swarm-flow", "# FILES:", "DISJOINT", "swarm-flow run", "swarm-flow init"} {
		if !strings.Contains(g, must) {
			t.Errorf("guidance missing %q", must)
		}
	}
	// Must warn against recursion / shared files.
	if !strings.Contains(g, "never nest") {
		t.Error("guidance should warn against nesting swarm-flow inside a worker")
	}
}
