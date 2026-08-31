package provider

// StandardAliases maps provider aliases to their canonical provider names.
//
// It is GENERATED from providerCanonical (identity.go), the single source of
// truth — it is no longer hand-maintained. Every alias whose name differs from
// its canonical form is included, so SetupStandardAliases registers the full
// known alias set on a registry.
//
// Kept as an exported package-level var because provider/shim.go re-exports it
// as public API. Keys are lower-case (RegisterAlias lower-cases them anyway);
// values are canonical names that must match a registered factory.
var StandardAliases = buildStandardAliases()

// buildStandardAliases derives the alias→canonical map from providerCanonical.
// An entry is emitted whenever the lookup key is not already its own canonical
// name (i.e. it is a genuine alias).
func buildStandardAliases() map[string]string {
	m := make(map[string]string, len(providerCanonical))
	for alias, canonical := range providerCanonical {
		if alias != canonical {
			m[alias] = canonical
		}
	}
	return m
}

// SetupStandardAliases registers all entries from StandardAliases on the
// receiver registry.  Safe to call multiple times — RegisterAlias is
// idempotent (last write wins).
func (r *SimpleRegistry) SetupStandardAliases() {
	for alias, target := range StandardAliases {
		r.RegisterAlias(alias, target)
	}
}
