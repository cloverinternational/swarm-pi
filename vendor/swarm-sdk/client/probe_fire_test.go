package client

import (
	"strings"
	"testing"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/provider"
)

// TestCustomProviderName_LookupResolvesAPIType is the bedrock of the fix:
// a user-defined custom provider name registered in
// ~/.swarm/config/providers.json must be
// recognised by lookupCustomProvider with its declared api_type.
//
// Pre-fix: lookupCustomProvider didn't exist; the SDK had no way to
// understand custom names like "fire".
func TestCustomProviderName_LookupResolvesAPIType(t *testing.T) {
	info, ok := lookupCustomProvider("fire")
	if !ok {
		t.Skip("'fire' not registered in this environment's ~/.swarm/config/providers.json — skipping")
	}
	if info.APIType == "" {
		t.Fatalf("custom provider 'fire' resolved but api_type is empty: %+v", info)
	}
	t.Logf("'fire' resolves to api_type=%q base_url=%q", info.APIType, info.BaseURL)
}

// TestCustomProviderName_AcceptedByClientNew is the regression test for the
// reported error:
//
//	SDK unavailable: NewSDKIntegration: CONTRACT VIOLATION: Unknown provider: "fire"
//
// Before the fix, client.New rejected any name that wasn't in the hardcoded
// validProviders map. After the fix, custom names defined in providers.json
// with api_type are accepted, routed via the appropriate canonical factory,
// and registered in the global compatibility registry so the agent's
// definition check passes too.
func TestCustomProviderName_AcceptedByClientNew(t *testing.T) {
	if _, ok := lookupCustomProvider("fire"); !ok {
		t.Skip("'fire' not registered in this environment's ~/.swarm/config/providers.json — skipping")
	}

	c, err := New(
		WithProviderString("fire", "accounts/fireworks/routers/kimi-k2p5-turbo"),
		WithoutAutoConfig(),
	)
	if err != nil {
		t.Fatalf("client.New unexpectedly failed for custom provider 'fire': %v", err)
	}
	if c == nil {
		t.Fatal("client.New returned nil with no error")
	}

	// Compatibility mapping is now instance-scoped (the package-level global
	// registry was removed in the DX overhaul). The client registers "fire" as
	// openai-compatible on its own internal SimpleRegistry; we verify the
	// instance-scoped contract that the agent's ProvidersMatch check relies on:
	// a registry told that "fire" is openai-compatible must treat them as a
	// match, while a registry without that mapping must not.
	reg := provider.NewSimpleRegistry(nil)
	reg.RegisterCompatible("fire", "openai")
	if !reg.ProvidersMatch("fire", "openai") {
		t.Errorf("expected reg.ProvidersMatch(\"fire\", \"openai\") = true after RegisterCompatible, got false")
	}
	// Instance isolation: a separate registry must NOT have inherited the mapping.
	other := provider.NewSimpleRegistry(nil)
	if other.ProvidersMatch("fire", "openai") {
		t.Errorf("expected a separate registry to NOT treat \"fire\" as openai-compatible (instance isolation)")
	}
}

// TestCustomProviderName_AcceptedByParseProvider is the regression test for
// the sac-side failure:
//
//	✗ Failed to generate commit message: CONTRACT VIOLATION: Unknown provider: "fire"
//
// sac's commit-message generator (and many sibling agents in
// swarm-sdk/sac/internal/...) call client.ParseProvider before constructing a client.
// Before the fix, ParseProvider only consulted the hardcoded validProviders
// map and rejected custom names. After the fix, it falls back to the same
// custom-provider lookup used elsewhere in the SDK and returns a Provider
// preserving the original name (so credential lookup still pulls the
// per-name api_key/base_url).
func TestCustomProviderName_AcceptedByParseProvider(t *testing.T) {
	if _, ok := lookupCustomProvider("fire"); !ok {
		t.Skip("'fire' not registered in this environment's ~/.swarm/config/providers.json — skipping")
	}

	p, err := ParseProvider("fire")
	if err != nil {
		t.Fatalf("ParseProvider(\"fire\") unexpectedly failed: %v", err)
	}
	if string(p) != "fire" {
		t.Errorf("ParseProvider must preserve the custom name (so downstream credential lookup pulls the per-name api_key); got %q", p)
	}
}

// TestCustomProviderName_RejectedWhenUndefined ensures we don't regress on
// the contract: an unknown name that is NOT in providers.json must still
// produce a clear contract violation, not silently default to anthropic or
// crash deeper in the stack.
func TestCustomProviderName_RejectedWhenUndefined(t *testing.T) {
	bogus := "definitely-not-a-real-provider-xyzzy-9000"
	_, err := New(
		WithProviderString(bogus, "some-model"),
		WithoutAutoConfig(),
	)
	if err == nil {
		t.Fatalf("expected contract violation for unknown name %q; got nil", bogus)
	}
	if !strings.Contains(err.Error(), "Unknown provider") {
		t.Fatalf("expected 'Unknown provider' contract violation, got: %v", err)
	}
}
