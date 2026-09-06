// Package harness — Phase 11b schema migration mechanism (plan.md Phase 11
// action #4: "schema migrations with sequential version transforms +
// fixtures").
//
// # D1 — the mechanism is real, the migration SET is honestly empty
//
// swarm.ai/v1alpha1 is the ONLY schema version that has ever shipped (see
// harness/version.go). There is therefore nothing to migrate FROM today, and
// this file does not invent a fictional legacy version to pretend otherwise —
// see migrationRegistry below, and TestMigrationRegistryIsEmpty in
// migrate_test.go, which asserts that fact rather than leaving it implicit.
//
// What IS real is the mechanism: a small, generic (schema-agnostic) chain
// executor that repeatedly looks up a registered transform whose declared
// source version equals a document's current apiVersion, applies it, and
// advances, until the document reaches APIVersionV1Alpha1 or no further
// transform exists. Adding the first REAL migration is meant to be exactly a
// one-line append to migrationRegistry plus a transform function plus a
// fixture pair (see migrate_test.go's synthetic-registry tests, which drive
// this SAME executor against a throwaway two-step chain to prove the
// mechanism itself — not the empty production registry — actually works).
//
// # D2 — total, ordered, acyclic, and proven so by a test on an empty set
//
// validateMigrationRegistry checks four invariants over whatever the
// registry contains:
//
//	(i)   every transform's From is unique (one outgoing edge per version);
//	(ii)  following the chain from EVERY registered From reaches
//	      APIVersionV1Alpha1;
//	(iii) it does so in at most len(registry) steps (the cycle proof);
//	(iv)  no transform's To equals its From.
//
// On the empty production registry these are vacuously true, and
// migrate_test.go's TestMigrationRegistryInvariants runs (and passes) that
// exact assertion — it is not skipped just because the registry is empty.
//
// migrate itself additionally enforces a hard step cap (maxMigrationSteps)
// independent of validateMigrationRegistry, so a future bad/cyclic registry
// that somehow passed review cannot hang a process at runtime: the executor
// degrades to a diagnostic, never a loop.
package harness

import "fmt"

// rawDoc is a generic, schema-agnostic JSON object used ONLY by the
// migration chain. It deliberately does NOT use the strict, single-version
// Document type (strict_decode.go): a migration transform's whole job is to
// reshape a document that predates (or otherwise does not match) the current
// schema, so it cannot assume the current Document shape holds yet. A
// transform receives and returns a rawDoc; only once a document's apiVersion
// equals APIVersionV1Alpha1 does the ordinary strictDecode path apply.
type rawDoc = map[string]any

// migrationTransform is one edge in the migration chain: a single, named,
// total transformation from exactly one source apiVersion to exactly one
// target apiVersion.
type migrationTransform struct {
	// From is the exact apiVersion this transform accepts as input.
	From string
	// To is the exact apiVersion this transform produces as output. Must
	// differ from From (D2.iv) — a transform that does not change the
	// version is not a migration.
	To string
	// Name is a stable, human-readable identifier for this transform, used
	// in diagnostics and in the []applied list returned by migrate so a
	// caller/log can name exactly which transform ran.
	Name string
	// Apply performs the actual reshape. It returns diagnostics instead of
	// an error so it composes with the rest of the harness package's
	// diagnostic-first error handling (diagnostics.go).
	Apply func(rawDoc) (rawDoc, Diagnostics)
}

// migrationRegistry is the SHIPPED set of known schema migrations.
//
// It is, and MUST remain, empty until a second real apiVersion exists to
// migrate from (D1). Do not add a synthetic/fictional entry here to
// exercise the mechanism — migrate_test.go exercises the mechanism against
// its OWN throwaway synthetic registry instead, precisely so this slice can
// stay honestly empty.
var migrationRegistry = []migrationTransform{}

// maxMigrationSteps bounds the chain executor so a future bad (e.g.
// accidentally cyclic) registry degrades to a diagnostic rather than an
// infinite loop. It is generous relative to any plausible registry size —
// nothing close to this many schema versions is expected to ever exist —
// while still being a real, finite bound.
const maxMigrationSteps = 64

// applied records one migration transform that actually ran, in the order it
// ran, so a caller (or an audit trail) can name exactly which path a
// document took to reach the current schema.
type applied struct {
	From string
	To   string
	Name string
}

// migrate runs doc through the shipped migrationRegistry starting at
// apiVersion from, per the file doc's D2 semantics. See migrateWith for the
// actual chain-walking logic (factored out so tests can drive the identical
// executor against a synthetic registry without mutating the production
// one).
func migrate(doc rawDoc, from string) (rawDoc, []applied, Diagnostics) {
	return migrateWith(migrationRegistry, doc, from)
}

// migrateWith is the chain executor itself: repeatedly look up the transform
// registered for the document's current version, apply it, and advance,
// until the version is target (APIVersionV1Alpha1) or no transform is
// registered for the current version (a clear diagnostic, not a silent
// stop). A registry is passed explicitly (rather than always reading the
// package-level migrationRegistry) so migrate_test.go can prove the
// mechanism against synthetic chains while the shipped registry stays empty.
func migrateWith(reg []migrationTransform, doc rawDoc, from string) (rawDoc, []applied, Diagnostics) {
	cur := from
	var out []applied

	for cur != APIVersionV1Alpha1 {
		if len(out) >= maxMigrationSteps {
			return doc, out, Diagnostics{newDiag("harness.migrate.stepLimitExceeded", "apiVersion",
				fmt.Sprintf("migration chain from %s exceeded the %d-step limit without reaching %s; the registry likely contains a cycle", from, maxMigrationSteps, APIVersionV1Alpha1), "")}
		}

		t, ok := lookupMigrationTransform(reg, cur)
		if !ok {
			return doc, out, Diagnostics{newDiag("harness.migrate.noPath", "apiVersion",
				fmt.Sprintf("no migration path from %s to %s (stopped at %s)", from, APIVersionV1Alpha1, cur), "")}
		}

		next, ds := t.Apply(doc)
		if ds.HasErrors() {
			return doc, out, ds
		}
		doc = next
		out = append(out, applied{From: t.From, To: t.To, Name: t.Name})
		cur = t.To
	}

	return doc, out, nil
}

// lookupMigrationTransform returns the transform registered for source
// version from, if any. Callers rely on validateMigrationRegistry (D2.i)
// having already established that at most one such transform exists; this
// function simply returns the first match, which is unambiguous given that
// invariant.
func lookupMigrationTransform(reg []migrationTransform, from string) (migrationTransform, bool) {
	for _, t := range reg {
		if t.From == from {
			return t, true
		}
	}
	return migrationTransform{}, false
}

// validateMigrationRegistry checks the four D2 invariants over reg and
// returns a nil-safe list of human-readable violation descriptions (empty
// when reg is valid). It is a pure structural check — it never runs an
// Apply function — so it terminates immediately even for a registry that
// (incorrectly) contains a cycle.
func validateMigrationRegistry(reg []migrationTransform) []string {
	var problems []string

	// (i) every From is unique — one outgoing edge per version.
	seenFrom := make(map[string]int, len(reg))
	for _, t := range reg {
		seenFrom[t.From]++
	}
	for from, n := range seenFrom {
		if n > 1 {
			problems = append(problems, fmt.Sprintf("apiVersion %s has %d registered transforms; must have exactly one", from, n))
		}
	}

	// (iv) no transform's To equals its From.
	for _, t := range reg {
		if t.To == t.From {
			problems = append(problems, fmt.Sprintf("transform %s: From and To are both %s; a migration must change the version", t.Name, t.From))
		}
	}

	// (ii) + (iii): following the chain from EVERY registered From reaches
	// APIVersionV1Alpha1 in at most len(reg) steps. This is the cycle proof:
	// a chain that has not reached the target within len(reg) hops, over a
	// registry whose From values are unique (checked above), must be
	// revisiting a version — i.e. it is cyclic.
	for _, start := range reg {
		cur := start.From
		steps := 0
		reached := cur == APIVersionV1Alpha1
		for !reached && steps <= len(reg) {
			t, ok := lookupMigrationTransform(reg, cur)
			if !ok {
				problems = append(problems, fmt.Sprintf("no path from %s to %s (stopped at %s after %d step(s))", start.From, APIVersionV1Alpha1, cur, steps))
				break
			}
			cur = t.To
			steps++
			if cur == APIVersionV1Alpha1 {
				reached = true
			}
		}
		if !reached && steps > len(reg) {
			problems = append(problems, fmt.Sprintf("chain from %s did not reach %s within %d step(s); registry likely contains a cycle", start.From, APIVersionV1Alpha1, len(reg)))
		}
	}

	return problems
}
