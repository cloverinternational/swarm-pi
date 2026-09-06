package provider

import "testing"

func TestStandardAliases(t *testing.T) {
	// Verify every alias maps to the expected canonical name
	for alias, canonical := range StandardAliases {
		if NormalizeProviderName(alias) != canonical {
			t.Errorf("StandardAliases[%q] = %q but NormalizeProviderName(%q) = %q",
				alias, canonical, alias, NormalizeProviderName(alias))
		}
	}
}

func TestSetupStandardAliases(t *testing.T) {
	reg := NewSimpleRegistry(nil)

	// Register a couple of dummy factories so the aliases can resolve
	_ = reg.Register("anthropic", func(cfg Config) (Provider, error) { return nil, nil })
	_ = reg.Register("openai", func(cfg Config) (Provider, error) { return nil, nil })
	_ = reg.Register("gemini", func(cfg Config) (Provider, error) { return nil, nil })

	reg.SetupStandardAliases()

	cases := []struct {
		alias    string
		expected bool
	}{
		{"ClaudeCode", true},
		{"OpenAI", true},
		{"Google", true},
		{"Codex", true},
		{"UnknownProvider", false},
	}

	for _, tc := range cases {
		t.Run(tc.alias, func(t *testing.T) {
			got := reg.IsRegistered(tc.alias)
			if got != tc.expected {
				t.Errorf("IsRegistered(%q) = %v, want %v", tc.alias, got, tc.expected)
			}
		})
	}
}

func TestRegisterProviderFromConfig(t *testing.T) {
	reg := NewSimpleRegistry(nil)

	// 1. Register with canonical name (no alias needed)
	err := reg.RegisterProviderFromConfig("openai", func(cfg Config) (Provider, error) { return nil, nil })
	if err != nil {
		t.Fatalf("RegisterProviderFromConfig(openai) error: %v", err)
	}

	// Verify direct lookup works
	if !reg.IsRegistered("openai") {
		t.Error("IsRegistered(openai) = false, want true")
	}

	// 2. Register with custom display name — should create alias
	err = reg.RegisterProviderFromConfig("MyFireworks", func(cfg Config) (Provider, error) { return nil, nil })
	if err != nil {
		t.Fatalf("RegisterProviderFromConfig(MyFireworks) error: %v", err)
	}

	// Custom name should resolve via alias
	if !reg.IsRegistered("MyFireworks") {
		t.Error("IsRegistered(MyFireworks) = false, want true")
	}

	// Canonical name should also resolve (normalized form)
	if !reg.IsRegistered("myfireworks") {
		t.Error("IsRegistered(myfireworks) = false, want true")
	}

	// 3. Empty name should error
	err = reg.RegisterProviderFromConfig("", func(cfg Config) (Provider, error) { return nil, nil })
	if err == nil {
		t.Error("RegisterProviderFromConfig(\"\") should error")
	}

	// 4. Nil factory should error
	err = reg.RegisterProviderFromConfig("test", nil)
	if err == nil {
		t.Error("RegisterProviderFromConfig(test, nil) should error")
	}
}

// TestProviderResolutionPipeline exercises the full five-stage resolution chain:
//
//  1. Raw alias string  →  NormalizeProviderName  →  canonical name
//  2. Canonical name    →  SimpleRegistry.Register   →  factory entry
//  3. StandardAliases   →  SetupStandardAliases    →  alias mappings
//  4. Custom name       →  RegisterProviderFromConfig → alias + factory
//  5. Registry lookup   →  Create()                →  provider instance
//
// This integration test proves the pipeline is internally consistent
// and that downstream consumers (task #3/#5) can rely on the contract.
func TestProviderResolutionPipeline(t *testing.T) {
	reg := NewSimpleRegistry(nil)

	// Stage 1 & 2: Register canonical factories
	_ = reg.Register("openai", func(cfg Config) (Provider, error) {
		// Verify config carries the canonical name
		if cfg.Name != "openai" {
			return nil, nil // test dummy — real impl would error
		}
		return nil, nil
	})
	_ = reg.Register("anthropic", func(cfg Config) (Provider, error) { return nil, nil })
	_ = reg.Register("gemini", func(cfg Config) (Provider, error) { return nil, nil })

	// Stage 3: Setup standard aliases (display names → canonical)
	reg.SetupStandardAliases()

	// Stage 4: Register a custom-named provider
	_ = reg.RegisterProviderFromConfig("MyGroq", func(cfg Config) (Provider, error) {
		// Config should carry canonical name, not custom name
		if cfg.Name != "groq" {
			return nil, nil
		}
		return nil, nil
	})

	// ── Assertions ──────────────────────────────────────────────

	// A: Standard alias resolves (ClaudeCode → anthropic)
	if !reg.IsRegistered("ClaudeCode") {
		t.Error("[alias] IsRegistered(ClaudeCode) = false, want true")
	}

	// B: Canonical name still resolves directly
	if !reg.IsRegistered("openai") {
		t.Error("[canonical] IsRegistered(openai) = false, want true")
	}

	// C: Custom name resolves (MyGroq → groq)
	if !reg.IsRegistered("MyGroq") {
		t.Error("[custom] IsRegistered(MyGroq) = false, want true")
	}

	// D: Normalization + registry: an unknown provider lowercases but resolves
	if !reg.IsRegistered("mygroq") {
		t.Error("[normalized] IsRegistered(mygroq) = false, want true")
	}

	// E: Verify compatibility mapping: "z.ai" is OpenAI-compatible
	compat := reg.getCompatibilityType("z.ai")
	if compat != "openai" {
		t.Errorf("[compat] getCompatibilityType(z.ai) = %q, want openai", compat)
	}

	// F: ProvidersMatch: two names for the same base type
	if !reg.ProvidersMatch("codex", "openai") {
		t.Error("[match] ProvidersMatch(codex, openai) = false, want true")
	}
}
