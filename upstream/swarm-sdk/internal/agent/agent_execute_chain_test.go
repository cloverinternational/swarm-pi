package agent

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/conversation"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/fallback"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/observability/noop"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/provider"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/tools"
)

// b1AmbientEnvVar is the ambient environment variable the fake provider
// factory below reads — mirroring EXACTLY the shape of the real leak:
// internal/provider/anthropic/register.go reads ANTHROPIC_API_KEY from the
// process environment whenever `config.APIKey == "" && !config.NoAmbientEnv`.
const b1AmbientEnvVar = "SWARM_TEST_B1_AMBIENT_CREDENTIAL"

// b1FakeProvider is a minimal provider.Provider that records which key it
// was actually constructed with, so a test can assert on it without any
// network access.
type b1FakeProvider struct {
	name        string
	resolvedKey string
	failModel   string // Chat() fails for this model, to force the chain onward
}

func (p *b1FakeProvider) Name() string { return p.name }

func (p *b1FakeProvider) Chat(_ context.Context, req provider.ChatRequest) (*provider.ChatResponse, error) {
	if p.failModel != "" && req.Model == p.failModel {
		return nil, errors.New("b1FakeProvider: forced failure to drive the chain to its next entry")
	}
	return &provider.ChatResponse{
		Message:  &conversation.Message{Role: conversation.RoleAssistant, Content: "ok"},
		Metadata: map[string]any{"resolved_key": p.resolvedKey},
	}, nil
}

func (p *b1FakeProvider) Stream(_ context.Context, _ provider.ChatRequest) (<-chan provider.StreamChunk, error) {
	ch := make(chan provider.StreamChunk)
	close(ch)
	return ch, nil
}

func (p *b1FakeProvider) Capabilities() provider.Capabilities { return provider.Capabilities{} }

// b1FakeFactory mirrors the EXACT ambient-fallback shape of the real
// anthropic/openai provider factories this bug affected: when the supplied
// config carries no explicit APIKey and does not declare NoAmbientEnv, it
// falls back to reading an ambient environment credential.
func b1FakeFactory(failModel string) provider.ProviderFactory {
	return func(cfg provider.Config) (provider.Provider, error) {
		key := cfg.APIKey
		if key == "" && !cfg.NoAmbientEnv {
			key = os.Getenv(b1AmbientEnvVar)
		}
		return &b1FakeProvider{name: cfg.Name, resolvedKey: key, failModel: failModel}, nil
	}
}

func b1NewTestAgent(t *testing.T) (*Agent, *provider.SimpleRegistry) {
	t.Helper()
	reg := provider.NewSimpleRegistry(noop.NewLogger())
	if err := reg.Register("anthropic", b1FakeFactory("primary-model")); err != nil {
		t.Fatalf("register fake factory: %v", err)
	}

	def := &Definition{
		ID:       "b1-test-agent",
		Name:     "b1-test-agent",
		Provider: "anthropic",
		Model:    "primary-model",
		Chain: &fallback.Chain{
			Primary:   fallback.ModelRef{Provider: "anthropic", Model: "primary-model"},
			Fallbacks: []fallback.ModelRef{{Provider: "anthropic", Model: "fallback-model"}},
		},
		Capabilities: &Capabilities{SupportsTools: true},
	}
	a, err := New(Config{
		Definition:       def,
		Provider:         &b1FakeProvider{name: "anthropic"},
		ProviderRegistry: reg,
		ToolRegistry:     tools.NewRegistry(),
	})
	if err != nil {
		t.Fatalf("agent.New: %v", err)
	}
	return a, reg
}

func b1TestRequest() provider.ChatRequest {
	return provider.ChatRequest{
		Model:    "fallback-model",
		Messages: []*conversation.Message{{Role: conversation.RoleUser, Content: "hi"}},
	}
}

// TestExecuteWithChainSealedFallbackNeverReachesAmbientCredential is the B1
// regression test: with a sealed provider.Config installed via
// SetChainProviderConfigs (mirroring client/harness_plan.go's Phase 10b
// wiring) for the FALLBACK entry, a fallback attempt must resolve to the
// SEALED key — and never to an ambient environment credential — even though
// that ambient credential IS present and WOULD be read absent the fix.
func TestExecuteWithChainSealedFallbackNeverReachesAmbientCredential(t *testing.T) {
	t.Setenv(b1AmbientEnvVar, "AMBIENT-CREDENTIAL-MUST-NEVER-BE-USED")

	a, _ := b1NewTestAgent(t)
	a.SetChainProviderConfigs(map[string]provider.Config{
		(fallback.ModelRef{Provider: "anthropic", Model: "fallback-model"}).Key(): {
			Name:         "anthropic",
			APIKey:       "SEALED-FALLBACK-KEY",
			NoAmbientEnv: true,
		},
	})

	resp, err := a.executeWithChain(context.Background(), b1TestRequest())
	if err != nil {
		t.Fatalf("executeWithChain: %v", err)
	}
	got, _ := resp.Metadata["resolved_key"].(string)
	if got != "SEALED-FALLBACK-KEY" {
		t.Fatalf("fallback attempt resolved key = %q, want the sealed key", got)
	}
	if got == "AMBIENT-CREDENTIAL-MUST-NEVER-BE-USED" {
		t.Fatal("fallback attempt reached the ambient environment credential — B1 regression")
	}
}

// TestExecuteWithChainUnsealedFallbackPreservesPriorBehavior proves the
// converse and guards against overreach: when NO sealed config is installed
// (SetChainProviderConfigs is never called — the state of every non-harness
// caller, unconditionally, both before and after this fix), a fallback
// attempt behaves EXACTLY as it did before the fix: a bare
// provider.Config{Name,Model}, which for a real factory means reading
// whatever ambient credential source it has always read. This is not new
// behavior; this test exists so a future change cannot silently start
// sealing attempts that were never asked to be sealed.
func TestExecuteWithChainUnsealedFallbackPreservesPriorBehavior(t *testing.T) {
	t.Setenv(b1AmbientEnvVar, "AMBIENT-CREDENTIAL-EXPECTED-HERE")

	a, _ := b1NewTestAgent(t)
	// Deliberately no call to SetChainProviderConfigs.

	resp, err := a.executeWithChain(context.Background(), b1TestRequest())
	if err != nil {
		t.Fatalf("executeWithChain: %v", err)
	}
	got, _ := resp.Metadata["resolved_key"].(string)
	if got != "AMBIENT-CREDENTIAL-EXPECTED-HERE" {
		t.Fatalf("unsealed fallback attempt resolved key = %q, want the ambient value "+
			"(pre-existing, unchanged behavior for every non-harness caller)", got)
	}
}

// TestSetChainProviderConfigsClearOnEmpty proves the documented "empty map
// clears" contract, and that a nil Agent.definition never panics.
func TestSetChainProviderConfigsClearOnEmpty(t *testing.T) {
	a, _ := b1NewTestAgent(t)
	key := (fallback.ModelRef{Provider: "anthropic", Model: "fallback-model"}).Key()
	a.SetChainProviderConfigs(map[string]provider.Config{key: {Name: "anthropic", APIKey: "x"}})
	if _, ok := a.chainProviderConfig("anthropic", "fallback-model"); !ok {
		t.Fatal("expected the installed config to be readable back")
	}
	a.SetChainProviderConfigs(nil)
	if _, ok := a.chainProviderConfig("anthropic", "fallback-model"); ok {
		t.Fatal("SetChainProviderConfigs(nil) must clear the previously installed set")
	}
}
