package provider

import "fmt"

// Provider Resolution Pipeline (five-stage contract)
//
// Stage 1 — Name canonicalisation
//   Input:  raw alias string (e.g. "ClaudeCode", "MyFireworks")
//   Func:   NormalizeProviderName(string) string
//   Output: canonical lower-case name ("anthropic", "myfireworks")
//
// Stage 2 — Registry entry creation
//   Input:  canonical name + ProviderFactory
//   Func:   (*SimpleRegistry).Register(string, ProviderFactory) error
//   Output: factory stored under canonical key
//
// Stage 3 — Alias registration (optional)
//   Input:  display-name → canonical-name pairs from StandardAliases
//   Func:   (*SimpleRegistry).SetupStandardAliases()
//   Output: alias table populated in registry
//
// Stage 4 — Custom provider setup (combines stages 1–3)
//   Input:  custom display name + factory
//   Func:   (*SimpleRegistry).RegisterProviderFromConfig(string, ProviderFactory) error
//   Output: alias + factory registration in one call
//
// Stage 5 — Runtime resolution
//   Input:  provider.Config{Name: <any alias>}
//   Func:   (*SimpleRegistry).Create(Config) (Provider, error)
//   Output: instantiated provider with canonical name in config
//
// Invariants:
//   • Empty name → error (RegisterProviderFromConfig)
//   • Nil factory → error (RegisterProviderFromConfig)
//   • Unknown alias → lowercased fallback (NormalizeProviderName)
//   • Duplicate registration → error (Register)
//   • Alias resolution is case-insensitive (RegisterAlias lowercases keys)

// RegisterStandardAliases is a package-level convenience that registers
// all known provider aliases on the supplied registry.
func RegisterStandardAliases(reg *SimpleRegistry) {
	reg.SetupStandardAliases()
}

// RegisterProviderFromConfig registers a provider factory from a user-defined
// configuration descriptor.
//
// If the supplied name differs from its canonical form (per
// NormalizeProviderName), an alias is registered so that both the
// custom display name and the canonical name resolve to the same
// factory.
//
// Example:
//
//	reg := provider.NewSimpleRegistry(logger)
//	err := reg.RegisterProviderFromConfig("MyFireworks", openai.NewOpenAIProvider)
//	// Now both "MyFireworks" and "fireworks" resolve to the same factory.
func (r *SimpleRegistry) RegisterProviderFromConfig(name string, factory ProviderFactory) error {
	if name == "" {
		return fmt.Errorf("provider name cannot be empty")
	}
	if factory == nil {
		return fmt.Errorf("provider factory cannot be nil")
	}

	canonical := NormalizeProviderName(name)

	// Register an alias when the user chose a custom display name.
	if canonical != name && canonical != "" {
		r.RegisterAlias(name, canonical)
	}

	return r.Register(canonical, factory)
}
