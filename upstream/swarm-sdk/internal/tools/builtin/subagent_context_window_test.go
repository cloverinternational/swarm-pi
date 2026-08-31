package builtin

import (
	"testing"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/provider"
)

func testContextWindowResolver(_ string, model string) int {
	switch model {
	case "known-small":
		return 128_000
	case "known-large":
		return provider.DefaultContextWindowCap
	default:
		return 0
	}
}

func TestSubagentProviderConfigResolvesFinalExplicitModelWindow(t *testing.T) {
	tool := &SubagentTool{
		providerConfig: provider.Config{
			Name:          "test",
			Model:         "known-small",
			ContextWindow: 128_000,
		},
		contextWindowResolver: testContextWindowResolver,
	}

	got, _ := tool.getProviderConfig("", "known-large", "")
	if got.Model != "known-large" {
		t.Fatalf("model = %q, want known-large", got.Model)
	}
	if got.ContextWindow != provider.DefaultContextWindowCap {
		t.Fatalf("context window = %d, want %d", got.ContextWindow, provider.DefaultContextWindowCap)
	}
}

func TestSubagentProviderConfigResolvesRoleModelWindow(t *testing.T) {
	tool := &SubagentTool{
		providerConfig: provider.Config{
			Name:          "test",
			Model:         "known-small",
			ContextWindow: 128_000,
		},
		contextWindowResolver: testContextWindowResolver,
		roleModelSelector: func(RoleType) *RoleModelConfig {
			return &RoleModelConfig{Provider: "test", Model: "known-large"}
		},
	}

	got, _ := tool.getProviderConfig("", "", RoleImplementer)
	if got.Model != "known-large" {
		t.Fatalf("model = %q, want known-large", got.Model)
	}
	if got.ContextWindow != provider.DefaultContextWindowCap {
		t.Fatalf("context window = %d, want %d", got.ContextWindow, provider.DefaultContextWindowCap)
	}
}

func TestResolvedContextWindowClearsStaleValueWhenUnknown(t *testing.T) {
	base := provider.Config{Name: "test", Model: "known-large", ContextWindow: provider.DefaultContextWindowCap}
	got := withResolvedContextWindow(provider.Config{
		Name:          "test",
		Model:         "unknown",
		ContextWindow: provider.DefaultContextWindowCap,
	}, base, testContextWindowResolver)
	if got.ContextWindow != 0 {
		t.Fatalf("stale context window retained: got %d, want 0", got.ContextWindow)
	}
}

func TestDefinitionModelOverrideReResolvesContextWindow(t *testing.T) {
	base := provider.Config{Name: "test", Model: "known-large", ContextWindow: provider.DefaultContextWindowCap}
	selected := withResolvedContextWindow(base, base, testContextWindowResolver)
	selected.Model = "known-small" // mirrors a custom/builtin definition override
	selected = withResolvedContextWindow(selected, base, testContextWindowResolver)
	if selected.ContextWindow != 128_000 {
		t.Fatalf("definition model context = %d, want 128000", selected.ContextWindow)
	}
}

func TestModelOverrideWithoutResolverClearsStaleContext(t *testing.T) {
	base := provider.Config{Name: "test", Model: "known-large", ContextWindow: provider.DefaultContextWindowCap}
	selected := base
	selected.Model = "unknown"
	selected = withResolvedContextWindow(selected, base, nil)
	if selected.ContextWindow != 0 {
		t.Fatalf("stale context retained without resolver: got %d, want 0", selected.ContextWindow)
	}
}
