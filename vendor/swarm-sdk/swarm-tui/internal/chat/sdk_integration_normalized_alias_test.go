package chat

import (
	"testing"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/provider"
	tuiobs "github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/chat/observability"
)

// TestEnsureNormalizedAlias_FreshOAuthSession is the regression test for the
// STARTUP variant of the sub-agent "provider not registered" bug.
//
// Bug: registerAllProviders (and the current-provider fallback) register the
// provider factory under the RAW lowercased name, e.g. "claudecode". The agent
// factory (sub-agents / Task tool) looks the provider up by
// provider.NormalizeProviderName(def.Provider) == "anthropic". The registry's
// resolve() only follows an alias when the alias TARGET is a real factory, so a
// "ClaudeCode"->"anthropic" display alias does NOT help (no factory under
// "anthropic"). Result: a fresh OAuth session that never switches providers has
// IsRegistered("anthropic") == false and EVERY sub-agent spawn fails with
// "provider 'claudecode' (normalized: 'anthropic') is not registered".
//
// ensureNormalizedAlias must alias normalized("anthropic") -> raw("claudecode")
// so the factory's normalized lookup resolves.
func TestEnsureNormalizedAlias_FreshOAuthSession(t *testing.T) {
	logger := tuiobs.NewTUILogger()
	reg := provider.NewSimpleRegistry(logger)

	// Simulate startup: only the raw OAuth name is registered as a factory.
	if err := reg.Register("claudecode", func(cfg provider.Config) (provider.Provider, error) {
		return &testChatProvider{name: "claudecode"}, nil
	}); err != nil {
		t.Fatalf("seed registry: %v", err)
	}

	// Bug precondition: the factory's normalized lookup is unresolved.
	if reg.IsRegistered("anthropic") {
		t.Fatal("precondition failed: 'anthropic' should NOT resolve before the alias")
	}

	// Apply the fix.
	ensureNormalizedAlias(reg, "claudecode")

	// The factory's normalized lookup must now resolve (this is the exact gate at
	// swarm-sdk/internal/agent/factory.go: IsRegistered(NormalizeProviderName(...))).
	if !reg.IsRegistered("anthropic") {
		t.Fatal("after ensureNormalizedAlias, IsRegistered(\"anthropic\") must be true " +
			"(MUTATION CHECK: removing RegisterAlias(normalized, raw) breaks this)")
	}

	// And Create under the normalized name must build the provider (the factory's
	// next step after the IsRegistered gate).
	p, err := reg.Create(provider.Config{Name: "anthropic"})
	if err != nil {
		t.Fatalf("Create({Name:anthropic}) must succeed after alias: %v", err)
	}
	if p == nil {
		t.Fatal("Create returned nil provider")
	}

	// The raw spelling must keep working too.
	if !reg.IsRegistered("claudecode") {
		t.Fatal("raw name 'claudecode' must still resolve")
	}
}

// TestEnsureNormalizedAlias_NoOpCases verifies the helper does not shadow a
// legitimately-registered canonical provider and is a no-op when not needed.
func TestEnsureNormalizedAlias_NoOpCases(t *testing.T) {
	logger := tuiobs.NewTUILogger()

	// Case 1: normalized == raw (e.g. provider already named "anthropic") -> no-op,
	// no panic, still resolves via its own factory.
	reg := provider.NewSimpleRegistry(logger)
	if err := reg.Register("anthropic", func(cfg provider.Config) (provider.Provider, error) {
		return &testChatProvider{name: "anthropic"}, nil
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	ensureNormalizedAlias(reg, "anthropic")
	if !reg.IsRegistered("anthropic") {
		t.Fatal("anthropic must still resolve")
	}

	// Case 2: a custom provider whose name does not normalize to anything else
	// must not gain a spurious alias / must not error.
	reg2 := provider.NewSimpleRegistry(logger)
	if err := reg2.Register("wafer.ai", func(cfg provider.Config) (provider.Provider, error) {
		return &testChatProvider{name: "wafer.ai"}, nil
	}); err != nil {
		t.Fatalf("seed wafer: %v", err)
	}
	ensureNormalizedAlias(reg2, "wafer.ai")
	if !reg2.IsRegistered("wafer.ai") {
		t.Fatal("wafer.ai must still resolve")
	}

	// Case 3: nil registry / empty name must not panic.
	ensureNormalizedAlias(nil, "claudecode")
	ensureNormalizedAlias(reg, "")
}
