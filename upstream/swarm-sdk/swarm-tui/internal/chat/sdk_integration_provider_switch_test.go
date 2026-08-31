package chat

import (
	"errors"
	"testing"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/provider"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/tools"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/chat/commands"
	tuiobs "github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/chat/observability"
)

// TestReloadProvider_RegistersSwitchedToProviderInSharedRegistry is the TUI-level
// regression test for the sub-agent "provider not registered" bug (SWA-5).
//
// The shared providerRegistry is built once at startup. When the user switches
// to a provider that was NOT present at startup, sub-agents (which resolve via
// the factory's providerRegistry.IsRegistered gate) fail. ReloadProvider must
// register the switched-to provider into the SAME shared registry so sub-agents
// keep working after the switch.
//
// This test starts with a registry that lacks "cerebras", switches to it via
// ReloadProvider, and asserts the registry now resolves it.
func TestReloadProvider_RegistersSwitchedToProviderInSharedRegistry(t *testing.T) {
	t.Setenv("SWARM_HOME", t.TempDir())

	logger := tuiobs.NewTUILogger()
	tracer := tuiobs.NewTUITracer(logger)

	// Startup registry: only the launch provider present.
	reg := provider.NewSimpleRegistry(logger)
	if err := reg.Register("openai", func(cfg provider.Config) (provider.Provider, error) {
		return &testChatProvider{name: "openai"}, nil
	}); err != nil {
		t.Fatalf("seed registry: %v", err)
	}

	initial := &testChatProvider{name: "openai"}
	sdk := &SDKIntegration{
		provider:         initial,
		providerName:     "openai",
		currentModel:     "gpt-5.1",
		logger:           logger,
		tracer:           tracer,
		agent:            newTestAgent(t, initial, "openai", "gpt-5.1"),
		providerRegistry: reg,
		providerBuilder: func(providerName, model string, _ providerBuildDeps) (provider.Provider, string, string, bool, error) {
			return &testChatProvider{name: provider.NormalizeProviderName(providerName)}, "system", "token", false, nil
		},
		reliabilityConfigLoader: func() (*commands.SwarmOSConfig, error) {
			return &commands.SwarmOSConfig{
				RetrySettings: &commands.RetrySettingsConfig{
					Enabled:               false,
					MaxRetriesPerProvider: 3,
					RetryAfterFallbackMs:  1000,
				},
			}, nil
		},
	}

	// Sanity: the switched-to provider is absent before the switch (bug precondition).
	if reg.IsRegistered("cerebras") {
		t.Fatal("precondition failed: cerebras should NOT be registered at startup")
	}

	// Switch to cerebras (a provider not present at startup).
	sdk.providerName = "cerebras"
	sdk.currentModel = "llama-3.3-70b"
	if err := sdk.ReloadProvider(); err != nil {
		t.Fatalf("ReloadProvider failed: %v", err)
	}

	// After the switch, sub-agents resolve providers via this shared registry.
	// It MUST now know about cerebras, or Task-tool sub-agents would fail with
	// "factory.provider_not_registered".
	if !reg.IsRegistered("cerebras") {
		t.Fatal("ReloadProvider did not register the switched-to provider 'cerebras' in the shared registry")
	}

	// The factory creates providers via registry.Create(); ensure that path works.
	p, err := reg.Create(provider.Config{Name: "cerebras", Model: "llama-3.3-70b"})
	if err != nil {
		t.Fatalf("registry.Create(cerebras) failed after ReloadProvider: %v", err)
	}
	if p == nil {
		t.Fatal("registry.Create(cerebras) returned nil provider")
	}
}

// TestReloadProvider_NormalizesProviderNameForFactoryLookup is the regression
// guard for the exact live failure: "provider 'claudecode' (normalized:
// 'anthropic') is not registered". The agent factory looks providers up by
// provider.NormalizeProviderName(def.Provider), so switching to a display-name
// variant like "claudecode" must make the registry resolve the NORMALIZED name
// "anthropic" (and the raw name too, via alias).
func TestReloadProvider_NormalizesProviderNameForFactoryLookup(t *testing.T) {
	t.Setenv("SWARM_HOME", t.TempDir())

	logger := tuiobs.NewTUILogger()
	tracer := tuiobs.NewTUITracer(logger)

	// Empty registry — nothing registered at startup.
	reg := provider.NewSimpleRegistry(logger)

	initial := &testChatProvider{name: "openai"}
	sdk := &SDKIntegration{
		provider:         initial,
		providerName:     "openai",
		currentModel:     "gpt-5.1",
		logger:           logger,
		tracer:           tracer,
		agent:            newTestAgent(t, initial, "openai", "gpt-5.1"),
		providerRegistry: reg,
		providerBuilder: func(providerName, model string, _ providerBuildDeps) (provider.Provider, string, string, bool, error) {
			return &testChatProvider{name: provider.NormalizeProviderName(providerName)}, "system", "token", false, nil
		},
		reliabilityConfigLoader: func() (*commands.SwarmOSConfig, error) {
			return &commands.SwarmOSConfig{RetrySettings: &commands.RetrySettingsConfig{Enabled: false}}, nil
		},
	}

	// Switch to the display-name variant "claudecode".
	sdk.providerName = "claudecode"
	sdk.currentModel = "claude-sonnet-4-20250514"
	if err := sdk.ReloadProvider(); err != nil {
		t.Fatalf("ReloadProvider failed: %v", err)
	}

	// The factory looks up NormalizeProviderName("claudecode") == "anthropic".
	if !reg.IsRegistered("anthropic") {
		t.Fatal("registry must resolve normalized name 'anthropic' after switching to 'claudecode'")
	}
	// The raw name must also resolve (via alias).
	if !reg.IsRegistered("claudecode") {
		t.Fatal("registry must also resolve the raw name 'claudecode' via alias")
	}
	// factory creation path by the normalized name must succeed.
	if _, err := reg.Create(provider.Config{
		Name: "anthropic", Model: "claude-sonnet-4-20250514", APIKey: "test-key",
	}); err != nil {
		t.Fatalf("registry.Create(anthropic) failed after switch to claudecode: %v", err)
	}
}

func TestCredentialChangedRebuildsInitializedSDKInPlace(t *testing.T) {
	logger := tuiobs.NewTUILogger()
	tracer := tuiobs.NewTUITracer(logger)
	stale := &testChatProvider{name: "codex"}
	agt := newTestAgent(t, stale, "codex", "gpt-5.6-terra")
	toolRegistry := tools.NewSimpleRegistry(nil, nil)
	fresh := &testChatProvider{name: "codex"}
	sdk := &SDKIntegration{
		provider:     stale,
		providerName: "codex",
		currentModel: "gpt-5.6-terra",
		logger:       logger,
		tracer:       tracer,
		agent:        agt,
		toolRegistry: toolRegistry,
		providerBuilder: func(providerName, model string, _ providerBuildDeps) (provider.Provider, string, string, bool, error) {
			return fresh, "system", "fresh-token", true, nil
		},
		reliabilityConfigLoader: func() (*commands.SwarmOSConfig, error) {
			return &commands.SwarmOSConfig{RetrySettings: &commands.RetrySettingsConfig{Enabled: false}}, nil
		},
	}

	if err := sdk.CredentialChanged("codex", "gpt-5.6-terra"); err != nil {
		t.Fatal(err)
	}
	if sdk.agent != agt {
		t.Fatal("credential rebuild replaced the agent/conversation owner")
	}
	if sdk.toolRegistry != toolRegistry {
		t.Fatal("credential rebuild replaced the tool registry")
	}
	if sdk.provider == stale || sdk.provider.Name() != "codex" {
		t.Fatal("provider was not rebuilt with fresh credentials")
	}
	if sdk.authToken != "fresh-token" {
		t.Fatalf("authToken = %q", sdk.authToken)
	}
}

func TestSwitchProviderFailurePreservesRunningState(t *testing.T) {
	logger := tuiobs.NewTUILogger()
	tracer := tuiobs.NewTUITracer(logger)
	oldProvider := &testChatProvider{name: "openai"}
	agt := newTestAgent(t, oldProvider, "openai", "old-model")
	sdk := &SDKIntegration{
		provider:        oldProvider,
		providerName:    "openai",
		currentModel:    "old-model",
		authToken:       "old-token",
		logger:          logger,
		tracer:          tracer,
		agent:           agt,
		thinkingEnabled: true,
		thinkingBudget:  4096,
		thinkingEffort:  "high",
		diffusionModel:  true,
		providerBuilder: func(string, string, providerBuildDeps) (provider.Provider, string, string, bool, error) {
			return nil, "", "", false, errors.New("invalid replacement credentials")
		},
	}

	err := sdk.SwitchProvider("codex", "new-model")
	if err == nil {
		t.Fatal("expected replacement builder failure")
	}
	if sdk.provider != oldProvider || sdk.providerName != "openai" || sdk.currentModel != "old-model" {
		t.Fatalf("provider selection mutated on failure: provider=%p name=%q model=%q",
			sdk.provider, sdk.providerName, sdk.currentModel)
	}
	if sdk.agent != agt || sdk.authToken != "old-token" {
		t.Fatal("agent or auth token mutated on failed replacement")
	}
	if !sdk.thinkingEnabled || sdk.thinkingBudget != 4096 ||
		sdk.thinkingEffort != "high" || !sdk.diffusionModel {
		t.Fatal("effective model configuration mutated on failed replacement")
	}
}
