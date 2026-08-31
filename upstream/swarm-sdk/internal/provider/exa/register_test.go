package exa

import (
	"testing"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/provider"
)

// TestRegisterIntoGivenRegistryOnly verifies that exa.Register adds the "exa"
// factory to exactly the registry it is given and to no other registry. This is
// the core guarantee after removing the init() global side-effect (FR4): there
// is no shared global registry, so a provider registered into one registry must
// not appear in another.
func TestRegisterIntoGivenRegistryOnly(t *testing.T) {
	target := provider.NewSimpleRegistry(nil)
	other := provider.NewSimpleRegistry(nil)

	if err := Register(target); err != nil {
		t.Fatalf("Register(target) error: %v", err)
	}

	if !target.IsRegistered("exa") {
		t.Error("expected 'exa' registered in target registry")
	}
	if other.IsRegistered("exa") {
		t.Error("expected 'exa' NOT registered in a separate registry (no global side-effect)")
	}
}

// TestRegisterIsExplicit verifies that constructing a fresh registry does not
// implicitly register exa — confirming there is no init() side-effect.
func TestRegisterIsExplicit(t *testing.T) {
	reg := provider.NewSimpleRegistry(nil)
	if reg.IsRegistered("exa") {
		t.Error("fresh registry must not have 'exa' pre-registered (init() side-effect must be gone)")
	}
}

// TestRegisterExaInterfaceForm verifies the retained interface-based helper
// still wires the provider into the given registry.
func TestRegisterExaInterfaceForm(t *testing.T) {
	reg := provider.NewSimpleRegistry(nil)
	if err := RegisterExa(reg); err != nil {
		t.Fatalf("RegisterExa(reg) error: %v", err)
	}
	if !reg.IsRegistered("exa") {
		t.Error("expected 'exa' registered via RegisterExa")
	}
}
