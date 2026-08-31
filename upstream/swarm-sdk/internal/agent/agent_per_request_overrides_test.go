package agent

import (
	"context"
	"testing"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/conversation"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/fallback"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/observability/noop"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/provider"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/tools"
)

// stubTool is a no-op tool used to populate a registry for DisableTools tests.
type stubTool struct{ name string }

func (s *stubTool) Name() string        { return s.name }
func (s *stubTool) Description() string { return "stub tool for tests" }
func (s *stubTool) Parameters() any     { return map[string]any{"type": "object"} }
func (s *stubTool) Execute(context.Context, map[string]any) (*tools.ToolResult, error) {
	return &tools.ToolResult{}, nil
}

// stubHooksManager satisfies HooksManager with no-op emissions.
type stubHooksManager struct{}

func (stubHooksManager) EmitToolBeforeExecute(context.Context, string, map[string]any) ([]HookResult, error) {
	return nil, nil
}
func (stubHooksManager) EmitToolAfterExecute(context.Context, string, map[string]any, any, error) []HookResult {
	return nil
}
func (stubHooksManager) EmitProviderResponse(context.Context, string, string, int, int, int64) {}

// newTestAgent builds a minimal Agent suitable for exercising buildProviderRequest /
// buildProviderTools / effectiveChain directly, without a real provider or loop.
func newOverrideTestAgent(t *testing.T, reg tools.Registry) *Agent {
	t.Helper()
	if reg == nil {
		reg = tools.NewRegistry()
	}
	return &Agent{
		definition: &Definition{
			ID:           "ovr-agent",
			Name:         "ovr",
			Provider:     "stub",
			Model:        "model-a",
			SystemPrompt: "BASE PROMPT",
			Capabilities: &Capabilities{MaxTokens: 1024},
		},
		toolReg: reg,
		logger:  noop.NewLogger(),
		tracer:  noop.NewTracer(),
		auditor: noop.NewAuditor(),
		ctx:     t.Context(),
	}
}

// TestSystemPromptOverride_PerTurn verifies the per-request system prompt
// override replaces the definition prompt for the turn, and that an empty
// override leaves the configured prompt intact (no accidental blanking).
func TestSystemPromptOverride_PerTurn(t *testing.T) {
	msgs := []*conversation.Message{{Role: conversation.RoleUser, Content: "hi"}}

	a := newOverrideTestAgent(t, nil)
	// No override → base prompt.
	if got := a.buildProviderRequest(msgs).SystemPrompt; got != "BASE PROMPT" {
		t.Fatalf("expected base prompt, got %q", got)
	}

	// With override → replaced.
	a.reqSystemPromptOverride = "ONE-SHOT PROMPT"
	if got := a.buildProviderRequest(msgs).SystemPrompt; got != "ONE-SHOT PROMPT" {
		t.Fatalf("expected override prompt, got %q", got)
	}

	// Cleared override → base prompt restored (definition never mutated).
	a.reqSystemPromptOverride = ""
	if got := a.buildProviderRequest(msgs).SystemPrompt; got != "BASE PROMPT" {
		t.Fatalf("expected base prompt after clear, got %q", got)
	}
}

// TestDisableTools_PerTurn verifies that with reqDisableTools the agent offers
// no tools to the provider even though the registry has tools, and that the
// registry is unaffected (tools return once the flag clears).
func TestDisableTools_PerTurn(t *testing.T) {
	reg := tools.NewRegistry()
	if err := reg.Register(&stubTool{name: "echo"}); err != nil {
		t.Fatalf("register tool: %v", err)
	}
	a := newOverrideTestAgent(t, reg)

	if got := len(a.buildProviderTools()); got == 0 {
		t.Fatalf("precondition: expected tools offered, got 0")
	}

	a.reqDisableTools = true
	if got := len(a.buildProviderTools()); got != 0 {
		t.Fatalf("expected 0 tools with DisableTools, got %d", got)
	}

	a.reqDisableTools = false
	if got := len(a.buildProviderTools()); got == 0 {
		t.Fatalf("expected tools to return after clearing DisableTools, got 0")
	}
}

// TestHooksEnabled_DisableHooks verifies the per-turn hook gate.
func TestHooksEnabled_DisableHooks(t *testing.T) {
	a := newOverrideTestAgent(t, nil)
	// No hooks manager → disabled regardless.
	if a.hooksEnabled() {
		t.Fatalf("expected hooksEnabled false with no manager")
	}
	a.hooksManager = stubHooksManager{}
	if !a.hooksEnabled() {
		t.Fatalf("expected hooksEnabled true with manager and no disable")
	}
	a.reqDisableHooks = true
	if a.hooksEnabled() {
		t.Fatalf("expected hooksEnabled false with DisableHooks set")
	}
}

// TestEffectiveChain_DropsUnregisteredFallbacks verifies that fallbacks whose
// provider isn't registered are pruned, the primary is always preserved, and
// noFallback collapses to the primary only.
func TestEffectiveChain_DropsUnregisteredFallbacks(t *testing.T) {
	reg := provider.NewSimpleRegistry(noop.NewLogger())
	_ = reg.Register("anthropic", func(provider.Config) (provider.Provider, error) { return nil, nil })

	a := newOverrideTestAgent(t, nil)
	a.providerRegistry = reg
	a.chain = &fallback.Chain{
		Primary: fallback.ModelRef{Provider: "anthropic", Model: "claude-x"},
		Fallbacks: []fallback.ModelRef{
			{Provider: "anthropic", Model: "claude-y"}, // registered → kept
			{Provider: "cerebras", Model: "llama-3.3"}, // NOT registered → dropped
		},
	}

	got := a.effectiveChain()
	if got.Primary.Provider != "anthropic" {
		t.Fatalf("primary changed: %+v", got.Primary)
	}
	if len(got.Fallbacks) != 1 {
		t.Fatalf("expected 1 surviving fallback, got %d (%+v)", len(got.Fallbacks), got.Fallbacks)
	}
	if got.Fallbacks[0].Provider != "anthropic" {
		t.Fatalf("wrong fallback kept: %+v", got.Fallbacks[0])
	}

	// noFallback collapses to primary only.
	a.noFallback = true
	got = a.effectiveChain()
	if len(got.Fallbacks) != 0 {
		t.Fatalf("expected no fallbacks with noFallback, got %d", len(got.Fallbacks))
	}
	if got.Primary.Provider != "anthropic" {
		t.Fatalf("primary changed under noFallback: %+v", got.Primary)
	}
}

// TestEffectiveChain_AllRegistered_NoCopy verifies the common case is a no-op
// (returns the same pointer when nothing needs filtering).
func TestEffectiveChain_AllRegistered_NoCopy(t *testing.T) {
	reg := provider.NewSimpleRegistry(noop.NewLogger())
	_ = reg.Register("anthropic", func(provider.Config) (provider.Provider, error) { return nil, nil })

	a := newOverrideTestAgent(t, nil)
	a.providerRegistry = reg
	a.chain = &fallback.Chain{
		Primary:   fallback.ModelRef{Provider: "anthropic", Model: "claude-x"},
		Fallbacks: []fallback.ModelRef{{Provider: "anthropic", Model: "claude-y"}},
	}
	if got := a.effectiveChain(); got != a.chain {
		t.Fatalf("expected same chain pointer when all fallbacks registered")
	}
}
