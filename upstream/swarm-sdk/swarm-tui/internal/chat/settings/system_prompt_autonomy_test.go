package settings

import (
	"strings"
	"testing"
)

func TestSwarmForgeIncludesHarnessEngineerControlLoop(t *testing.T) {
	for _, required := range []string{
		"<effective_capabilities>",
		"Recall relevant prior evidence",
		"Load relevant skills",
		"Completion is a state, not a phrase",
	} {
		if !strings.Contains(swarmForgeSystemPrompt, required) {
			t.Fatalf("SwarmForge missing %q", required)
		}
	}
}
