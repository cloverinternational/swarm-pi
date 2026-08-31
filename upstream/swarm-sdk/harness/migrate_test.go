package harness

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

// ── D1: the registry is real but honestly empty ─────────────────────────────

// TestMigrationRegistryIsEmpty asserts, rather than leaves implicit, that the
// shipped migrationRegistry has zero entries: swarm.ai/v1alpha1 is the only
// apiVersion that has ever shipped, so there is nothing to migrate FROM yet
// (D1). This test is meant to FAIL the moment someone adds a fabricated
// entry to make "something" migrate — the correct way to add the first real
// migration is a one-line registry append plus a fixture pair, at which
// point this assertion is updated deliberately, not incidentally.
func TestMigrationRegistryIsEmpty(t *testing.T) {
	if len(migrationRegistry) != 0 {
		t.Fatalf("migrationRegistry has %d entries, want 0 (swarm.ai/v1alpha1 is the only schema version that has ever shipped)", len(migrationRegistry))
	}
}

// ── D2: total, ordered, acyclic — proven on the empty set too ───────────────

// TestMigrationRegistryInvariants runs the four D2 invariants against the
// REAL shipped migrationRegistry. On today's empty registry every invariant
// is vacuously true, but the test still runs (it is not skipped or
// conditioned on len(migrationRegistry) > 0) — exactly the point: the proof
// obligation holds "for whatever the registry contains", including nothing.
func TestMigrationRegistryInvariants(t *testing.T) {
	if problems := validateMigrationRegistry(migrationRegistry); len(problems) != 0 {
		t.Fatalf("migrationRegistry violates its invariants: %v", problems)
	}
}

// TestValidateMigrationRegistryCatchesViolations proves validateMigrationRegistry
// is not vacuously "always passing" — each sub-test is a deliberately broken
// synthetic registry (never installed into the package-level
// migrationRegistry) that must be REJECTED.
func TestValidateMigrationRegistryCatchesViolations(t *testing.T) {
	noop := func(d rawDoc) (rawDoc, Diagnostics) { return d, nil }

	t.Run("duplicate from (ambiguous edge)", func(t *testing.T) {
		reg := []migrationTransform{
			{From: "v1", To: "v2", Name: "a", Apply: noop},
			{From: "v1", To: "v3", Name: "b", Apply: noop},
			{From: "v2", To: APIVersionV1Alpha1, Name: "c", Apply: noop},
			{From: "v3", To: APIVersionV1Alpha1, Name: "d", Apply: noop},
		}
		if problems := validateMigrationRegistry(reg); len(problems) == 0 {
			t.Fatal("expected a violation for duplicate From, got none")
		}
	})

	t.Run("to equals from (no-op masquerading as a migration)", func(t *testing.T) {
		reg := []migrationTransform{
			{From: "v1", To: "v1", Name: "a", Apply: noop},
		}
		if problems := validateMigrationRegistry(reg); len(problems) == 0 {
			t.Fatal("expected a violation for To == From, got none")
		}
	})

	t.Run("cycle (never reaches current)", func(t *testing.T) {
		reg := []migrationTransform{
			{From: "v1", To: "v2", Name: "a", Apply: noop},
			{From: "v2", To: "v1", Name: "b", Apply: noop},
		}
		if problems := validateMigrationRegistry(reg); len(problems) == 0 {
			t.Fatal("expected a violation for a cycle, got none")
		}
	})

	t.Run("dead end (no transform for an intermediate version)", func(t *testing.T) {
		reg := []migrationTransform{
			{From: "v1", To: "v2", Name: "a", Apply: noop},
			// nothing registered FROM v2, so v1's chain dead-ends short of
			// APIVersionV1Alpha1.
		}
		if problems := validateMigrationRegistry(reg); len(problems) == 0 {
			t.Fatal("expected a violation for a dead-end chain, got none")
		}
	})

	t.Run("valid two-step chain passes cleanly", func(t *testing.T) {
		reg := []migrationTransform{
			{From: "v1", To: "v2", Name: "a", Apply: noop},
			{From: "v2", To: APIVersionV1Alpha1, Name: "b", Apply: noop},
		}
		if problems := validateMigrationRegistry(reg); len(problems) != 0 {
			t.Fatalf("valid registry reported violations: %v", problems)
		}
	})
}

// ── Chain executor mechanics (migrate / migrateWith) ─────────────────────────

// TestMigrateAlreadyCurrentIsNoop proves migrate is a true no-op when the
// document is already at APIVersionV1Alpha1: unchanged doc, zero applied
// transforms, no diagnostics — exercised against the REAL (empty) registry,
// so this is also a second, independent confirmation that migrate() behaves
// correctly with nothing registered.
func TestMigrateAlreadyCurrentIsNoop(t *testing.T) {
	doc := loadMigrateFixtureDoc(t, "already_current", "input.json")
	meta := loadMigrateFixtureMeta(t, "already_current")

	got, appliedList, ds := migrate(cloneRawDoc(doc), meta.From)
	if ds.HasErrors() {
		t.Fatalf("unexpected diagnostics: %v", ds)
	}
	if len(appliedList) != len(meta.WantAppliedNames) {
		t.Fatalf("applied = %v, want %v", appliedList, meta.WantAppliedNames)
	}
	if !reflect.DeepEqual(got, doc) {
		t.Fatalf("doc mutated on already-current migrate: got %v, want %v", got, doc)
	}
}

// TestMigrateNoPathOnRealRegistry proves that an apiVersion the (empty) real
// registry has no transform for produces the documented
// "harness.migrate.noPath" diagnostic rather than silently succeeding or
// panicking.
func TestMigrateNoPathOnRealRegistry(t *testing.T) {
	doc := loadMigrateFixtureDoc(t, "no_path", "input.json")
	meta := loadMigrateFixtureMeta(t, "no_path")

	_, appliedList, ds := migrate(cloneRawDoc(doc), meta.From)
	if !ds.HasErrors() {
		t.Fatalf("expected diagnostics for unmigratable version %q, got none", meta.From)
	}
	if len(appliedList) != 0 {
		t.Fatalf("applied = %v, want empty on failure", appliedList)
	}
	if ds[0].Code != meta.WantErrorCode {
		t.Fatalf("diagnostic code = %q, want %q", ds[0].Code, meta.WantErrorCode)
	}
}

// TestMigrateSyntheticChain drives migrateWith (the SAME executor migrate
// uses) against a throwaway two-step synthetic registry that is never
// installed into the package-level migrationRegistry, proving the mechanism
// itself works end-to-end: source fixture -> two named transforms applied in
// order -> expected fixture. This is the "fixture harness" the task requires
// even though the shipped registry (D1) has nothing to exercise it with.
func TestMigrateSyntheticChain(t *testing.T) {
	doc := loadMigrateFixtureDoc(t, "synthetic_two_step", "input.json")
	want := loadMigrateFixtureDoc(t, "synthetic_two_step", "expected.json")
	meta := loadMigrateFixtureMeta(t, "synthetic_two_step")

	const vIntermediate = "swarm.ai/v0balpha-fixture-only"

	reg := []migrationTransform{
		{
			From: meta.From,
			To:   vIntermediate,
			Name: "fixture-v0-to-v0b",
			Apply: func(d rawDoc) (rawDoc, Diagnostics) {
				out := cloneRawDoc(d)
				out["apiVersion"] = vIntermediate
				return out, nil
			},
		},
		{
			From: vIntermediate,
			To:   APIVersionV1Alpha1,
			Name: "fixture-v0b-to-v1alpha1",
			Apply: func(d rawDoc) (rawDoc, Diagnostics) {
				out := cloneRawDoc(d)
				out["apiVersion"] = APIVersionV1Alpha1
				if v, ok := out["legacyField"]; ok {
					out["renamedField"] = v
					delete(out, "legacyField")
				}
				return out, nil
			},
		},
	}

	// The synthetic registry itself must satisfy the same D2 invariants any
	// real registry would have to.
	if problems := validateMigrationRegistry(reg); len(problems) != 0 {
		t.Fatalf("synthetic fixture registry is invalid: %v", problems)
	}

	got, appliedList, ds := migrateWith(reg, cloneRawDoc(doc), meta.From)
	if ds.HasErrors() {
		t.Fatalf("unexpected diagnostics: %v", ds)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("migrated doc = %v, want %v", got, want)
	}
	gotNames := make([]string, 0, len(appliedList))
	for _, a := range appliedList {
		gotNames = append(gotNames, a.Name)
	}
	if !reflect.DeepEqual(gotNames, meta.WantAppliedNames) {
		t.Fatalf("applied names = %v, want %v", gotNames, meta.WantAppliedNames)
	}
	if len(appliedList) > 0 {
		first := appliedList[0]
		if first.From != meta.From || first.To != vIntermediate {
			t.Errorf("first applied step = %+v, want From=%q To=%q", first, meta.From, vIntermediate)
		}
	}
}

// TestMigrateStepCapStopsACycle proves the executor's own defensive step cap
// (maxMigrationSteps) — independent of validateMigrationRegistry — turns a
// registry that (incorrectly) contains a cycle into a bounded diagnostic
// return instead of an infinite loop. validateMigrationRegistry would also
// reject this registry (see TestValidateMigrationRegistryCatchesViolations),
// but this test calls migrateWith directly to prove the RUNTIME executor is
// independently safe even if an invalid registry somehow shipped.
func TestMigrateStepCapStopsACycle(t *testing.T) {
	noop := func(d rawDoc) (rawDoc, Diagnostics) { return d, nil }
	reg := []migrationTransform{
		{From: "cycleA", To: "cycleB", Name: "a-to-b", Apply: noop},
		{From: "cycleB", To: "cycleA", Name: "b-to-a", Apply: noop},
	}

	done := make(chan struct{})
	var ds Diagnostics
	go func() {
		_, _, ds = migrateWith(reg, rawDoc{"apiVersion": "cycleA"}, "cycleA")
		close(done)
	}()

	select {
	case <-done:
	case <-timeoutChan(t):
		t.Fatal("migrateWith on a cyclic registry did not return — step cap failed to bound it")
	}

	if !ds.HasErrors() {
		t.Fatal("expected a step-limit diagnostic for a cyclic registry, got none")
	}
	if ds[0].Code != "harness.migrate.stepLimitExceeded" {
		t.Fatalf("diagnostic code = %q, want harness.migrate.stepLimitExceeded", ds[0].Code)
	}
}

// ── fixture loading helpers ───────────────────────────────────────────────

type migrateFixtureMeta struct {
	From             string   `json:"from"`
	WantAppliedNames []string `json:"wantAppliedNames"`
	WantErrorCode    string   `json:"wantErrorCode"`
}

func loadMigrateFixtureDoc(t *testing.T, dir, file string) rawDoc {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", "migrate", dir, file))
	if err != nil {
		t.Fatalf("reading fixture %s/%s: %v", dir, file, err)
	}
	var doc rawDoc
	if err := json.Unmarshal(b, &doc); err != nil {
		t.Fatalf("decoding fixture %s/%s: %v", dir, file, err)
	}
	return doc
}

func loadMigrateFixtureMeta(t *testing.T, dir string) migrateFixtureMeta {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", "migrate", dir, "meta.json"))
	if err != nil {
		t.Fatalf("reading fixture %s/meta.json: %v", dir, err)
	}
	var meta migrateFixtureMeta
	if err := json.Unmarshal(b, &meta); err != nil {
		t.Fatalf("decoding fixture %s/meta.json: %v", dir, err)
	}
	return meta
}

func cloneRawDoc(d rawDoc) rawDoc {
	out := make(rawDoc, len(d))
	for k, v := range d {
		out[k] = v
	}
	return out
}

func timeoutChan(t *testing.T) <-chan struct{} {
	t.Helper()
	ch := make(chan struct{})
	go func() {
		<-time.After(5 * time.Second)
		close(ch)
	}()
	return ch
}
