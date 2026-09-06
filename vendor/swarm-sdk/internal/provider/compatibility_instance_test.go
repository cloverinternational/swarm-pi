package provider

import "testing"

// TestCompatibilityIsInstanceScoped verifies that compatibility mappings
// registered on one SimpleRegistry do not leak into another. This is the core
// guarantee of the global-state elimination (US2 / FR4): multi-tenant embedders
// must be able to hold isolated registries without shared mutable state.
func TestCompatibilityIsInstanceScoped(t *testing.T) {
	regA := NewSimpleRegistry(nil)
	regB := NewSimpleRegistry(nil)

	// Register a custom compatibility mapping on A only.
	regA.RegisterOpenAICompatible("local")

	// A sees the mapping.
	if !regA.ProvidersMatch("local", "openai") {
		t.Error("regA.ProvidersMatch(local, openai) = false, want true after RegisterOpenAICompatible")
	}

	// B must NOT see A's mapping — no shared global state.
	if regB.ProvidersMatch("local", "openai") {
		t.Error("regB.ProvidersMatch(local, openai) = true, want false (instance isolation broken)")
	}

	// The package-level (static-only) helper must also not see the dynamic
	// mapping, since it consults built-in families only.
	if ProvidersMatch("local", "openai") {
		t.Error("package-level ProvidersMatch(local, openai) = true, want false (no dynamic state)")
	}
}

// TestCompatibilityBuiltinFamiliesPerInstance verifies the built-in (static)
// compatibility families resolve identically on every instance and via the
// package-level helper, independent of any dynamic registration.
func TestCompatibilityBuiltinFamiliesPerInstance(t *testing.T) {
	reg := NewSimpleRegistry(nil)

	cases := []struct {
		a, b string
		want bool
	}{
		{"codex", "openai", true},
		{"z.ai", "openai", true},
		{"claudecode", "anthropic", true},
		{"gemini-code-assist", "gemini", true},
		{"anthropic", "openai", false},
		{"openai", "gemini", false},
	}
	for _, tc := range cases {
		if got := reg.ProvidersMatch(tc.a, tc.b); got != tc.want {
			t.Errorf("reg.ProvidersMatch(%q,%q) = %v, want %v", tc.a, tc.b, got, tc.want)
		}
		if got := ProvidersMatch(tc.a, tc.b); got != tc.want {
			t.Errorf("ProvidersMatch(%q,%q) = %v, want %v", tc.a, tc.b, got, tc.want)
		}
	}
}

// TestRegisterCompatibleHelpers verifies the convenience instance methods route
// to the correct base type.
func TestRegisterCompatibleHelpers(t *testing.T) {
	reg := NewSimpleRegistry(nil)
	reg.RegisterOpenAICompatible("custom-oai")
	reg.RegisterAnthropicCompatible("custom-anthropic")
	reg.RegisterGeminiCompatible("custom-gemini")

	if !reg.ProvidersMatch("custom-oai", "openai") {
		t.Error("RegisterOpenAICompatible did not make custom-oai openai-compatible")
	}
	if !reg.ProvidersMatch("custom-anthropic", "anthropic") {
		t.Error("RegisterAnthropicCompatible did not make custom-anthropic anthropic-compatible")
	}
	if !reg.ProvidersMatch("custom-gemini", "gemini") {
		t.Error("RegisterGeminiCompatible did not make custom-gemini gemini-compatible")
	}
	// Cross-base must not match.
	if reg.ProvidersMatch("custom-oai", "anthropic") {
		t.Error("custom-oai must not be anthropic-compatible")
	}
}
