package client

// End-to-end coverage for the client → provider-registry compatibility wiring
// (review finding W4, specs/001-sdk-dx-overhaul/REVIEW.md).
//
// Commit 85760d6a moved provider compatibility from a package-level global to
// per-SimpleRegistry instance state. client.initAgent now calls
// reg.RegisterCompatible(...) on the SAME registry instance it passes to
// agent.New as agent.Config.ProviderRegistry (client.go: the xai-family loop,
// the "claudecode"→"anthropic" mapping, and the providers.json custom-provider
// branch, all feeding the registry handed to the agent config).
//
// The registry instance itself is intentionally NOT exported (no accessor on
// Client or Agent), so these tests assert the wiring through the only
// exported observable: agent.New's provider-mismatch gate
// (internal/agent/agent.go: provReg.ProvidersMatch(def.Provider, prov.Name())
// → sdkerr "agent.provider_mismatch" → client.New error). The gate runs on
// every chainless client.New, so:
//
//   - client.New SUCCEEDING for a definition/factory name pair that is NOT in
//     the built-in static families (builtinCompatibilityType) proves the
//     dynamic RegisterCompatible call landed on the exact registry instance
//     the agent validates against. A regression — e.g. a future second
//     registry, or registering compat after the agent is built — makes these
//     constructions fail.
//   - client.New FAILING for a pair in no family proves the gate is live,
//     i.e. the success assertions above are not vacuous.
//
// Fixture pattern (temp SWARM_HOME + ~/.swarm/config/providers.json legacy array format
// + t.Setenv key isolation) mirrors client_env_test.go
// (TestResolveCredentialsFromProvidersJSON) and provider_type_case_test.go.
// No network calls: client.New only constructs the provider (HTTP client +
// headers); nothing dials out.

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/paths"
)

// writeProvidersJSON writes a legacy array-format providers.json to the
// canonical ~/.swarm/config/providers.json location (paths.ProvidersFile()),
// the same shape the TUI settings UI persists and the same fixture shape
// client_env_test.go uses. Callers must isolate the root via
// t.Setenv("SWARM_HOME", ...) first.
func writeProvidersJSON(t *testing.T, home string, providers []map[string]any) {
	t.Helper()
	_ = home // retained for call-site compatibility; path derives from SWARM_HOME
	data, err := json.Marshal(providers)
	if err != nil {
		t.Fatalf("marshal providers.json: %v", err)
	}
	if err := os.WriteFile(paths.ProvidersFile(), data, 0o600); err != nil {
		t.Fatalf("write providers.json: %v", err)
	}
}

// TestRegistryWiring_CustomProviderCompatEndToEnd asserts the providers.json
// branch of the wiring end-to-end: a custom provider name ("fire") declared
// with api_type=openai-compatible must survive the agent's provider-mismatch
// gate after client.New.
//
// Why this is conclusive: the agent definition keeps Provider="fire" (custom
// names are never collapsed to the canonical type — asserted below via
// AgentInfo), while the factory-built provider reports Name()=="openai"
// (SimpleRegistry.Create rewrites config.Name to the alias target, and the
// openai provider echoes it). "fire" appears in NO built-in static family, so
// ProvidersMatch("fire","openai") can only be true if initAgent called
// RegisterCompatible("fire","openai") on the registry instance it gave the
// agent. This restores, hermetically, the public-API assertion the old
// probe_fire_test made against the (now deleted) global registry.
func TestRegistryWiring_CustomProviderCompatEndToEnd(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("SWARM_HOME", home)
	// Deterministic credential resolution: force the providers.json fallback
	// by blanking the convention env var resolveCredentials tries first.
	t.Setenv("FIRE_API_KEY", "")

	writeProvidersJSON(t, home, []map[string]any{
		{
			"name":     "fire",
			"type":     "api_key",
			"api_type": "openai-compatible",
			"api_key":  "test-key-fire-not-used",
			"base_url": "https://api.fireworks.ai/inference/v1",
		},
	})

	c, err := New(
		WithProviderString("fire", "accounts/fireworks/models/test-model"),
		WithWorkspace(home),
		WithoutAutoConfig(),
		WithoutIndexMd(),
	)
	if err != nil {
		t.Fatalf("client.New for custom provider 'fire' failed — the compat mapping "+
			"did not reach the registry instance the agent validates against: %v", err)
	}

	// Guard against vacuity: the definition must still carry the CUSTOM name.
	// If a refactor collapsed it to "openai", the gate above would pass by
	// direct match and this test would stop covering the compat wiring.
	def := c.AgentInfo()
	if def == nil {
		t.Fatal("AgentInfo() returned nil after successful New")
	}
	if def.Provider != "fire" {
		t.Fatalf("agent definition provider = %q, want %q — the custom name must be "+
			"preserved on the definition so the mismatch gate exercises the compat mapping",
			def.Provider, "fire")
	}
	if got := c.ProviderName(); got != "fire" {
		t.Errorf("ProviderName() = %q, want %q (custom name preserved for credential lookup)", got, "fire")
	}
}

// TestRegistryWiring_XAIFamilyCompatEndToEnd asserts the built-in xai-alias
// loop of the wiring end-to-end on the same registry instance the client
// uses. The xai family ("xai", "grok", "x.ai", "supergrok", ...) is routed to
// the openai factory by alias but is deliberately ABSENT from the static
// builtinCompatibilityType families — its openai-compatibility exists only as
// instance-scoped RegisterCompatible calls made by initAgent. So a successful
// client.New with definition Provider "grok"/"xai" and factory name "openai"
// is observable proof the loop ran against the agent's registry.
func TestRegistryWiring_XAIFamilyCompatEndToEnd(t *testing.T) {
	for _, name := range []string{"grok", "xai"} {
		t.Run(name, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HOME", home)
			t.Setenv("SWARM_HOME", home)
			// Both names resolve credentials via XAI_API_KEY; never dialed.
			t.Setenv("XAI_API_KEY", "test-key-xai-not-used")

			c, err := New(
				WithProviderString(name, "grok-3"),
				WithWorkspace(home),
				WithoutAutoConfig(),
				WithoutIndexMd(),
			)
			if err != nil {
				t.Fatalf("client.New for built-in family alias %q failed — the xai-family "+
					"RegisterCompatible loop did not reach the registry instance the agent "+
					"validates against: %v", name, err)
			}
			def := c.AgentInfo()
			if def == nil {
				t.Fatal("AgentInfo() returned nil after successful New")
			}
			// Vacuity guard: the alias must survive onto the definition
			// (normalizeProviderName keeps xai-family names as-is), otherwise
			// the gate passed by direct match and proved nothing.
			if def.Provider != name {
				t.Fatalf("agent definition provider = %q, want %q — xai-family aliases must "+
					"reach the definition un-collapsed for this test to exercise the compat path",
					def.Provider, name)
			}
		})
	}
}

// TestRegistryWiring_ClaudeCodeFamilyEndToEnd locks the anthropic-family
// behavior end-to-end: client.New(WithProviderString("claudecode", ...)) must
// construct successfully against the same registry.
//
// Note the weaker discrimination here, documented deliberately: client.New
// normalizes "claudecode" → "anthropic" BEFORE building the definition, so
// the gate passes by direct match and additionally "claudecode" is in the
// static built-in anthropic family. This test therefore pins the end-to-end
// family behavior (alias accepted, normalized onto the definition, agent
// builds), not the instance-scoped RegisterCompatible("claudecode",
// "anthropic") call specifically — the discriminating instance-scoped
// assertions are the fire/xai tests above.
func TestRegistryWiring_ClaudeCodeFamilyEndToEnd(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("SWARM_HOME", home)
	t.Setenv("ANTHROPIC_API_KEY", "test-key-anthropic-not-used")

	c, err := New(
		WithProviderString("claudecode", "claude-sonnet-4-5"),
		WithWorkspace(home),
		WithoutAutoConfig(),
		WithoutIndexMd(),
	)
	if err != nil {
		t.Fatalf("client.New for 'claudecode' failed: %v", err)
	}
	def := c.AgentInfo()
	if def == nil {
		t.Fatal("AgentInfo() returned nil after successful New")
	}
	if def.Provider != "anthropic" {
		t.Errorf("agent definition provider = %q, want %q (claudecode normalizes to anthropic)",
			def.Provider, "anthropic")
	}
}

// TestRegistryWiring_MismatchGateIsLive is the negative control that keeps
// the positive tests above honest: it proves the agent really does consult
// ProvidersMatch on the client's registry and rejects non-family pairs. If a
// regression ever made agent.New skip provider validation (in which case the
// success-path tests would pass vacuously regardless of registry wiring),
// this test fails.
//
// It injects a pre-built openai provider (exported NewProvider +
// WithProviderInstance) under a definition that demands "anthropic": no
// direct match, no shared family (built-in or dynamic), so client.New must
// surface agent.New's provider-mismatch error.
func TestRegistryWiring_MismatchGateIsLive(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("SWARM_HOME", home)

	prov, err := NewProvider("openai", "gpt-4o", WithProviderAPIKey("test-key-not-used"))
	if err != nil {
		t.Fatalf("NewProvider(openai): %v", err)
	}

	_, err = New(
		WithProviderString("anthropic", "claude-sonnet-4-5"),
		WithProviderInstance(prov),
		WithWorkspace(home),
		WithoutAutoConfig(),
		WithoutIndexMd(),
	)
	if err == nil {
		t.Fatal("client.New succeeded with definition provider 'anthropic' and injected " +
			"'openai' provider — the agent's ProvidersMatch gate is not running, so the " +
			"registry-wiring success tests in this file are vacuous")
	}
	if !strings.Contains(err.Error(), "definition requires provider") {
		t.Fatalf("expected the agent provider-mismatch error (\"definition requires provider\"), got: %v", err)
	}
}
