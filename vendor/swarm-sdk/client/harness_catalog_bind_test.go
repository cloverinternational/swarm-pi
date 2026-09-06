// Harness Phase 10b — D1/D2 regression guard.
//
// Phase 10a's audit (.swarm-p/harness-anywhere/phase10a/PARITY_HARNESS.md)
// found the exact bug this test exists to make impossible to repeat
// silently: harnessToolBindings covered only 7 of the 62 catalog ids, and
// nothing enforced that the remaining 55 were even NOTICED, let alone
// accounted for. TestHarnessCatalogBindConformance re-derives, from the live
// catalog and the live binder/host-binding maps, that EVERY catalog id lands
// in EXACTLY ONE of three buckets:
//
//	(a) bound             — harnessToolBindings
//	(b) host-binding-required — harnessToolHostBindingReasons
//	(c) DEFER-DISCOVER    — hard-rejected upstream (harness/plan.go); never
//	                        reaches this file, so accounted for by Class alone.
//
// Any FUTURE catalog id that lands in NONE of the three (the exact Phase 10a
// failure mode) fails this test loudly, by name. The counts are also
// hard-floored so the test cannot pass vacuously if the catalog or the maps
// were ever emptied by accident.
package client

import (
	"testing"

	"github.com/Swarm-Code/mono/swarm-sdk/harness"
)

func TestHarnessCatalogBindConformance(t *testing.T) {
	catalog := harness.Catalog()

	// Anti-vacuous guard: the whole test is meaningless if the catalog, or
	// either bucket, is empty. Floors were lowered when the grep family
	// (forge.grep, forge.unified_grep, builtin.grep, forge.semantic_grep) and
	// forge.semantic_rename were removed in favour of shell-based search.
	// These floors match the known-good Phase 10b
	// state (62 catalog ids: 19 bound, 16 host-binding-required, 27
	// DEFER-DISCOVER) with headroom for future additions, never subtractions.
	const (
		minCatalogSize          = 55
		minBound                = 13
		minHostBindingRequired  = 10
		minDeferDiscoverAccount = 20
	)
	if len(catalog) < minCatalogSize {
		t.Fatalf("harness.Catalog() has %d entries, want >= %d (anti-vacuous floor)", len(catalog), minCatalogSize)
	}
	if len(harnessToolBindings) < minBound {
		t.Fatalf("harnessToolBindings has %d entries, want >= %d (anti-vacuous floor)", len(harnessToolBindings), minBound)
	}
	if len(harnessToolHostBindingReasons) < minHostBindingRequired {
		t.Fatalf("harnessToolHostBindingReasons has %d entries, want >= %d (anti-vacuous floor)",
			len(harnessToolHostBindingReasons), minHostBindingRequired)
	}

	var (
		unaccounted     []string
		doubleAccounted []string
		deferButBound   []string
		deferCount      int
	)
	for _, c := range catalog {
		_, bound := harnessToolBindings[c.ID]
		_, hostBinding := harnessToolHostBindingReasons[c.ID]
		isDefer := c.Class == harness.PolicyDeferDiscover

		switch {
		case bound && hostBinding:
			doubleAccounted = append(doubleAccounted, c.ID)
		case isDefer && (bound || hostBinding):
			// DEFER-DISCOVER ids are hard-rejected before ever reaching this
			// file (harness/plan.go); a binder entry for one would be dead
			// code at best and a silent posture-widening bug at worst if the
			// upstream gate were ever loosened.
			deferButBound = append(deferButBound, c.ID)
		case isDefer:
			deferCount++
		case bound || hostBinding:
			// accounted for, exactly once — the good case.
		default:
			unaccounted = append(unaccounted, c.ID)
		}
	}

	if len(unaccounted) > 0 {
		t.Errorf("catalog id(s) accounted for in NEITHER bucket (the exact Phase 10a failure mode): %v", unaccounted)
	}
	if len(doubleAccounted) > 0 {
		t.Errorf("catalog id(s) present in BOTH harnessToolBindings and harnessToolHostBindingReasons "+
			"(ambiguous — pick exactly one route): %v", doubleAccounted)
	}
	if len(deferButBound) > 0 {
		t.Errorf("DEFER-DISCOVER catalog id(s) present in a binder/host-binding map "+
			"(dead code today, a posture-widening trap if the upstream gate ever loosens): %v", deferButBound)
	}
	if deferCount < minDeferDiscoverAccount {
		t.Errorf("only %d DEFER-DISCOVER ids observed, want >= %d (anti-vacuous floor on the third bucket)",
			deferCount, minDeferDiscoverAccount)
	}

	// Every bound id must actually be constructible end-to-end through
	// buildHarnessTools, and every host-binding-required id must fail with
	// the PRECISE reason (never the generic "not yet bound" fallback, which
	// exists only for a truly-unaccounted-for future id).
	ws := t.TempDir()
	for id := range harnessToolBindings {
		if _, err := buildHarnessTools([]string{id}, ws); err != nil {
			t.Errorf("bound id %q failed to construct: %v", id, err)
		}
	}
	for id, reason := range harnessToolHostBindingReasons {
		_, err := buildHarnessTools([]string{id}, ws)
		if err == nil {
			t.Errorf("host-binding-required id %q constructed successfully; either bind it for real "+
				"or keep it refused", id)
			continue
		}
		if reason.Requirement == "" || reason.Reason == "" {
			t.Errorf("host-binding-required id %q has an empty Requirement/Reason (D1 requires a PRECISE, "+
				"actionable reason, never a placeholder)", id)
		}
	}
}

// TestHarnessCatalogBindNeverWidensPosture is the D1 HARD INVARIANT proof:
// binding a capability must never let it become reachable somewhere the
// class gate (harness/plan.go, untouched by this slice) still forbids it.
func TestHarnessCatalogBindNeverWidensPosture(t *testing.T) {
	// A DEFER-DISCOVER id stays hard-rejected at PLAN COMPILE time — before it
	// could ever reach harnessToolBindings/harnessToolHostBindingReasons —
	// regardless of whether this slice touched either map.
	const deferManifest = `apiVersion: swarm.ai/v1alpha1
kind: Harness
metadata:
  name: defer-discover-still-rejected
provider:
  id: anthropic
  model: claude-x
  credential:
    inline: test-key
agent:
  systemPrompt:
    inline: "d"
  tools:
    - steering.ask_user
permissions:
  approvalMode: interactive
`
	if _, err := harness.CompileBytes([]byte(deferManifest), t.TempDir()+"/harness.yaml"); err == nil {
		t.Fatal("DEFER-DISCOVER id compiled; the Phase 8d class gate must still hard-reject it")
	}

	// A NEVER-DEFAULT id newly bound in this slice (forge.write) still
	// requires permissions.acknowledgeNeverDefault before it compiles at all.
	const unacknowledgedNeverDefault = `apiVersion: swarm.ai/v1alpha1
kind: Harness
metadata:
  name: never-default-still-gated
provider:
  id: anthropic
  model: claude-x
  credential:
    inline: test-key
agent:
  systemPrompt:
    inline: "n"
  tools:
    - forge.write
permissions:
  approvalMode: interactive
`
	if _, err := harness.CompileBytes([]byte(unacknowledgedNeverDefault), t.TempDir()+"/harness.yaml"); err == nil {
		t.Fatal("unacknowledged NEVER-DEFAULT id (now bound) compiled without acknowledgeNeverDefault; " +
			"binding must never widen posture")
	}

	// Zero selection stays byte-for-byte a zero-tool sentinel, regardless of
	// how many ids are now bound.
	c := mustBuild(t, `apiVersion: swarm.ai/v1alpha1
kind: Harness
metadata:
  name: zero-selection-still-empty
provider:
  id: anthropic
  model: claude-x
  credential:
    inline: test-key
agent:
  systemPrompt:
    inline: "z"
  tools: []
permissions:
  approvalMode: interactive
`)
	if got := exposedSorted(c); len(got) != 0 {
		t.Fatalf("zero tool selection exposed %v; binding more ids must never widen the default/empty case", got)
	}
}
