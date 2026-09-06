package harness

import (
	"strings"
	"testing"
)

// baseBudgetsManifest returns a minimal valid manifest with the given raw block
// (typically a `budgets:` section) spliced in verbatim at document level.
func baseBudgetsManifest(block string) string {
	return "apiVersion: swarm.ai/v1alpha1\n" +
		"kind: Harness\n" +
		"metadata:\n  name: budgetstest\n" +
		"provider:\n  id: anthropic\n  model: claude-x\n" +
		"agent:\n  systemPrompt:\n    inline: \"hi\"\n  tools: []\n" +
		block
}

// budgetsManifestWithLimits splices an `agent.limits:` block (which must be
// indented for nesting under `agent:`) plus a document-level block.
func budgetsManifestWithLimits(limits, block string) string {
	return "apiVersion: swarm.ai/v1alpha1\n" +
		"kind: Harness\n" +
		"metadata:\n  name: budgetstest\n" +
		"provider:\n  id: anthropic\n  model: claude-x\n" +
		"agent:\n  systemPrompt:\n    inline: \"hi\"\n  tools: []\n" +
		limits +
		block
}

// --- omitted / empty / digest backward compatibility -----------------------

// TestBudgetsOmittedAndEmptyValid: an omitted section and an explicitly empty
// `budgets: {}` are both valid, carry zero budgets, produce the SAME digest, and
// add no key to the explained report. This is the D5 "omitting changes nothing"
// guarantee and the backward-compatibility guarantee in one.
func TestBudgetsOmittedAndEmptyValid(t *testing.T) {
	p := compileOK(t, baseBudgetsManifest(""))
	if len(p.Budgets()) != 0 {
		t.Errorf("omitted: want zero budgets, got %v", p.Budgets())
	}
	if _, ok := p.Budget(BudgetKindTurn); ok {
		t.Errorf("omitted: Budget(turn) should report ok=false")
	}
	digestOmitted := p.Digest()

	p2 := compileOK(t, baseBudgetsManifest("budgets: {}\n"))
	if len(p2.Budgets()) != 0 {
		t.Errorf("empty: want zero budgets, got %v", p2.Budgets())
	}
	if p2.Digest() != digestOmitted {
		t.Errorf("`budgets: {}` vs omitted digests differ: %q vs %q", p2.Digest(), digestOmitted)
	}

	js, err := p2.ExplainJSON()
	if err != nil {
		t.Fatalf("ExplainJSON: %v", err)
	}
	if strings.Contains(string(js), "\"budgets\"") {
		t.Errorf("an unused budgets section must be omitted from ExplainJSON entirely")
	}
	// No provenance entry may appear either: an unused section contributes
	// nothing at all to the redacted surface.
	for _, fs := range p2.Provenance() {
		if strings.HasPrefix(fs.Field, "budgets") {
			t.Errorf("unused budgets section leaked provenance %+v", fs)
		}
	}
}

// TestBudgetsDigestBackwardCompatiblePre9a pins EXACT plan digests captured from
// a throwaway `git worktree` at HEAD (12bd1f9c, pre-9a) for manifests that
// declare no budgets. They must be BYTE IDENTICAL after this slice: the Document
// field and the ExplainReport field are both `omitempty`, so an absent section
// contributes nothing to the canonical JSON the digest hashes.
func TestBudgetsDigestBackwardCompatiblePre9a(t *testing.T) {
	const (
		pre9aBase   = "sha256:b10ba787e05ae18cfccb2c9efb911017ef40c2b85215173275f0e69aab4c237a"
		pre9aLimits = "sha256:7619d8a0230ccd20803b55dccba7dfbe2959f2ef0563c316a93ac47824318454"
	)
	if got := compileOK(t, baseBudgetsManifest("")).Digest(); got != pre9aBase {
		t.Fatalf("digest changed for a manifest without a budgets section:\n got  %s\n want %s", got, pre9aBase)
	}
	// A manifest that uses agent.limits (the section budgets overlaps with) is
	// likewise untouched.
	got := compileOK(t, budgetsManifestWithLimits("  limits:\n    maxTurns: 12\n", "")).Digest()
	if got != pre9aLimits {
		t.Fatalf("digest changed for a limits-only manifest:\n got  %s\n want %s", got, pre9aLimits)
	}
}

// --- the kind -> enforcement class table -----------------------------------

// TestEnforcementClassTable pins the classification of every one of the seven
// kinds. If a runtime change makes a kind enforceable, this test is the place
// that forces the table (and the reasoning around it) to be updated with it.
func TestEnforcementClassTable(t *testing.T) {
	want := map[BudgetKind]EnforcementClass{
		BudgetKindRun:         BudgetUnavailable,
		BudgetKindTurn:        BudgetEnforced,
		BudgetKindOutputToken: BudgetObserved,
		BudgetKindTotalToken:  BudgetObserved,
		BudgetKindToolCall:    BudgetObserved,
		BudgetKindCost:        BudgetUnavailable,
		BudgetKindWallClock:   BudgetEnforced,
	}
	if len(budgetFacts) != len(want) || len(budgetKindOrder) != len(want) {
		t.Fatalf("kind count drift: facts=%d order=%d want=%d", len(budgetFacts), len(budgetKindOrder), len(want))
	}
	for kind, wantClass := range want {
		got, ok := EnforcementClassOf(kind)
		if !ok {
			t.Errorf("%s: not classified", kind)
			continue
		}
		if got != wantClass {
			t.Errorf("%s: class = %s, want %s", kind, got, wantClass)
		}
	}
	if _, ok := EnforcementClassOf("nonsense"); ok {
		t.Errorf("an unknown kind must not be classified")
	}
	// Every kind must carry a unit and its file:line evidence, and every
	// unavailable kind must carry its own rejection reason.
	for _, kind := range budgetKindOrder {
		f := budgetFacts[kind]
		if f.unit == "" || f.evidence == "" {
			t.Errorf("%s: missing unit/evidence", kind)
		}
		if f.class == BudgetUnavailable && f.unavailableReason == "" {
			t.Errorf("%s: unavailable kinds must explain why", kind)
		}
	}
}

// --- per-class declaration gates -------------------------------------------

// TestBudgetsEnforcedKindsAccepted: the two genuinely enforced kinds are
// declarable normally, with no acknowledgement, and are reported as enforced.
func TestBudgetsEnforcedKindsAccepted(t *testing.T) {
	p := compileOK(t, baseBudgetsManifest(
		"budgets:\n"+
			"  turn:\n    maxTurns: 40\n    onExhaustion: graceful\n"+
			"  wallClock:\n    maxSeconds: 600\n    onExhaustion: fail\n"))

	turn, ok := p.Budget(BudgetKindTurn)
	if !ok {
		t.Fatalf("turn budget missing")
	}
	if turn.Enforcement != BudgetEnforced || turn.Limit != 40 || turn.Unit != "turns" ||
		turn.OnExhaustion != BudgetOnExhaustionGraceful || turn.Acknowledged {
		t.Errorf("turn spec wrong: %+v", turn)
	}
	wall, ok := p.Budget(BudgetKindWallClock)
	if !ok {
		t.Fatalf("wallClock budget missing")
	}
	if wall.Enforcement != BudgetEnforced || wall.Limit != 600 || wall.Unit != "seconds" ||
		wall.OnExhaustion != BudgetOnExhaustionFail {
		t.Errorf("wallClock spec wrong: %+v", wall)
	}

	// Canonical order, not declaration order.
	all := p.Budgets()
	if len(all) != 2 || all[0].Kind != BudgetKindTurn || all[1].Kind != BudgetKindWallClock {
		t.Errorf("budgets not in canonical kind order: %+v", all)
	}
}

// TestBudgetsObservedRequireAcknowledgement: each observed kind is REJECTED
// without its per-kind acknowledgement and ACCEPTED with it — and is still
// reported as observed, never as enforced.
func TestBudgetsObservedRequireAcknowledgement(t *testing.T) {
	cases := []struct {
		kind  BudgetKind
		block string
		unit  string
		limit int
	}{
		{BudgetKindOutputToken, "  outputToken:\n    maxCumulativeOutputTokens: 50000\n    onExhaustion: graceful\n", "tokens", 50000},
		{BudgetKindTotalToken, "  totalToken:\n    maxTotalTokens: 200000\n    onExhaustion: graceful\n", "tokens", 200000},
		{BudgetKindToolCall, "  toolCall:\n    maxToolCalls: 250\n    onExhaustion: fail\n", "calls", 250},
	}
	for _, tc := range cases {
		t.Run(string(tc.kind), func(t *testing.T) {
			raw := baseBudgetsManifest("budgets:\n" + tc.block)
			ds := compileErr(t, raw, syntheticYAML)
			code := "harness.budgets." + string(tc.kind) + ".unacknowledged"
			if !hasCode(ds, code) {
				t.Fatalf("expected %s, got: %v", code, ds)
			}
			if msg := diagMessage(ds, code); !strings.Contains(msg, "OBSERVED") ||
				!strings.Contains(msg, "budgets.acknowledgeObserved") {
				t.Errorf("diagnostic must say the bound is observed-only and name the remedy: %q", msg)
			}

			withAck := baseBudgetsManifest("budgets:\n" + tc.block +
				"  acknowledgeObserved:\n    - " + string(tc.kind) + "\n")
			p := compileOK(t, withAck)
			spec, ok := p.Budget(tc.kind)
			if !ok {
				t.Fatalf("%s budget missing after acknowledgement", tc.kind)
			}
			if spec.Enforcement != BudgetObserved {
				t.Errorf("acknowledging must NOT promote the kind to enforced: %+v", spec)
			}
			if !spec.Acknowledged || spec.Limit != tc.limit || spec.Unit != tc.unit {
				t.Errorf("%s spec wrong: %+v", tc.kind, spec)
			}
		})
	}
}

// TestBudgetsCostRejectedNoOverride: cost is UNAVAILABLE — rejected outright,
// with a diagnostic that says it is not merely unenforced but never computed,
// and with no expressible override (acknowledging it is itself an error).
func TestBudgetsCostRejectedNoOverride(t *testing.T) {
	raw := baseBudgetsManifest("budgets:\n  cost:\n    maxCostMicroUSD: 5000000\n    onExhaustion: fail\n")
	ds := compileErr(t, raw, syntheticYAML)
	if !hasCode(ds, "harness.budgets.cost.unavailable") {
		t.Fatalf("expected harness.budgets.cost.unavailable, got: %v", ds)
	}
	msg := diagMessage(ds, "harness.budgets.cost.unavailable")
	for _, want := range []string{"never COMPUTES cost", "agent_execute.go:632", "no acknowledgement, posture, or override"} {
		if !strings.Contains(msg, want) {
			t.Errorf("cost diagnostic must contain %q; got %q", want, msg)
		}
	}

	// The acknowledgement escape hatch must not exist for it: the ack list
	// rejects a non-observed kind, so cost stays rejected either way.
	acked := baseBudgetsManifest("budgets:\n  cost:\n    maxCostMicroUSD: 5000000\n    onExhaustion: fail\n" +
		"  acknowledgeObserved:\n    - cost\n")
	ds2 := compileErr(t, acked, syntheticYAML)
	if !hasCode(ds2, "harness.budgets.cost.unavailable") ||
		!hasCode(ds2, "harness.budgets.acknowledgeObserved.notObserved") {
		t.Fatalf("acknowledging cost must not enable it, and must itself be rejected; got: %v", ds2)
	}
}

// TestBudgetsRunRejected: run is UNAVAILABLE, and its diagnostic explains that
// it is a schedule-level concept deferred to Phase 9b rather than reusing the
// cost reasoning.
func TestBudgetsRunRejected(t *testing.T) {
	raw := baseBudgetsManifest("budgets:\n  run:\n    maxRuns: 3\n    onExhaustion: fail\n")
	ds := compileErr(t, raw, syntheticYAML)
	if !hasCode(ds, "harness.budgets.run.unavailable") {
		t.Fatalf("expected harness.budgets.run.unavailable, got: %v", ds)
	}
	msg := diagMessage(ds, "harness.budgets.run.unavailable")
	for _, want := range []string{"SCHEDULE-level", "one compiled plan describes one run", "Phase 9b"} {
		if !strings.Contains(msg, want) {
			t.Errorf("run diagnostic must contain %q; got %q", want, msg)
		}
	}
}

// --- acknowledgement list rules --------------------------------------------

// TestBudgetsAckDangling: acknowledging a kind that is not declared is an error
// (a dangling acknowledgement would pre-authorize a future edit).
func TestBudgetsAckDangling(t *testing.T) {
	raw := baseBudgetsManifest("budgets:\n  acknowledgeObserved:\n    - toolCall\n")
	ds := compileErr(t, raw, syntheticYAML)
	if !hasCode(ds, "harness.budgets.acknowledgeObserved.unused") {
		t.Fatalf("expected acknowledgeObserved.unused, got: %v", ds)
	}

	// Declaring one observed kind does not license acknowledging another.
	mixed := baseBudgetsManifest("budgets:\n" +
		"  toolCall:\n    maxToolCalls: 10\n    onExhaustion: fail\n" +
		"  acknowledgeObserved:\n    - toolCall\n    - totalToken\n")
	ds2 := compileErr(t, mixed, syntheticYAML)
	if !hasCode(ds2, "harness.budgets.acknowledgeObserved.unused") {
		t.Fatalf("expected acknowledgeObserved.unused for the undeclared kind, got: %v", ds2)
	}
}

// TestBudgetsAckOfEnforcedKindRejected: the field accepts observed kinds ONLY,
// so it can never become a junk drawer of unrelated kinds.
func TestBudgetsAckOfEnforcedKindRejected(t *testing.T) {
	raw := baseBudgetsManifest("budgets:\n" +
		"  turn:\n    maxTurns: 5\n    onExhaustion: graceful\n" +
		"  acknowledgeObserved:\n    - turn\n")
	ds := compileErr(t, raw, syntheticYAML)
	if !hasCode(ds, "harness.budgets.acknowledgeObserved.notObserved") {
		t.Fatalf("expected acknowledgeObserved.notObserved, got: %v", ds)
	}
}

// TestBudgetsAckListMalformed: empty entry, duplicate entry, and unknown kind
// are each fail-closed.
func TestBudgetsAckListMalformed(t *testing.T) {
	cases := []struct{ name, block, code string }{
		{"empty", "  acknowledgeObserved:\n    - \"\"\n", "harness.budgets.acknowledgeObserved.empty"},
		{"duplicate", "  acknowledgeObserved:\n    - toolCall\n    - toolCall\n", "harness.budgets.acknowledgeObserved.duplicate"},
		{"unknown", "  acknowledgeObserved:\n    - bogusKind\n", "harness.budgets.acknowledgeObserved.unknown"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			raw := baseBudgetsManifest("budgets:\n" +
				"  toolCall:\n    maxToolCalls: 10\n    onExhaustion: fail\n" + tc.block)
			ds := compileErr(t, raw, syntheticYAML)
			if !hasCode(ds, tc.code) {
				t.Fatalf("expected %s, got: %v", tc.code, ds)
			}
		})
	}

	// There is no wildcard: "all"/"*" are simply unknown kinds, so blanket
	// acknowledgement is unexpressible by construction.
	for _, wildcard := range []string{"*", "all"} {
		raw := baseBudgetsManifest("budgets:\n" +
			"  toolCall:\n    maxToolCalls: 10\n    onExhaustion: fail\n" +
			"  acknowledgeObserved:\n    - \"" + wildcard + "\"\n")
		ds := compileErr(t, raw, syntheticYAML)
		if !hasCode(ds, "harness.budgets.acknowledgeObserved.unknown") {
			t.Fatalf("wildcard %q must not be acceptable, got: %v", wildcard, ds)
		}
	}
}

// --- onExhaustion (D4) ------------------------------------------------------

// TestBudgetsOnExhaustionRequired: omitting onExhaustion is an error that NAMES
// the budget, for every declarable kind. There is no default.
func TestBudgetsOnExhaustionRequired(t *testing.T) {
	cases := []struct {
		kind  BudgetKind
		block string
	}{
		{BudgetKindTurn, "  turn:\n    maxTurns: 5\n"},
		{BudgetKindWallClock, "  wallClock:\n    maxSeconds: 30\n"},
		{BudgetKindOutputToken, "  outputToken:\n    maxCumulativeOutputTokens: 100\n  acknowledgeObserved:\n    - outputToken\n"},
		{BudgetKindTotalToken, "  totalToken:\n    maxTotalTokens: 100\n  acknowledgeObserved:\n    - totalToken\n"},
		{BudgetKindToolCall, "  toolCall:\n    maxToolCalls: 100\n  acknowledgeObserved:\n    - toolCall\n"},
	}
	for _, tc := range cases {
		t.Run(string(tc.kind), func(t *testing.T) {
			ds := compileErr(t, baseBudgetsManifest("budgets:\n"+tc.block), syntheticYAML)
			code := "harness.budgets." + string(tc.kind) + ".onExhaustion.missing"
			if !hasCode(ds, code) {
				t.Fatalf("expected %s, got: %v", code, ds)
			}
			if msg := diagMessage(ds, code); !strings.Contains(msg, string(tc.kind)) {
				t.Errorf("diagnostic must name the budget: %q", msg)
			}
		})
	}
}

// TestBudgetsOnExhaustionUnknownRejected: the action enum is closed. An unknown
// action is never silently ignored or coerced to a default.
func TestBudgetsOnExhaustionUnknownRejected(t *testing.T) {
	for _, bad := range []string{"degrade", "retry", "Graceful", "abort"} {
		raw := baseBudgetsManifest("budgets:\n  turn:\n    maxTurns: 5\n    onExhaustion: " + bad + "\n")
		ds := compileErr(t, raw, syntheticYAML)
		if !hasCode(ds, "harness.budgets.turn.onExhaustion.unknown") {
			t.Fatalf("action %q must be rejected, got: %v", bad, ds)
		}
	}

	// Both valid variants are accepted and round-trip exactly.
	for _, good := range []BudgetAction{BudgetOnExhaustionGraceful, BudgetOnExhaustionFail} {
		p := compileOK(t, baseBudgetsManifest("budgets:\n  turn:\n    maxTurns: 5\n    onExhaustion: "+string(good)+"\n"))
		spec, _ := p.Budget(BudgetKindTurn)
		if spec.OnExhaustion != good {
			t.Errorf("onExhaustion %q did not round-trip: %+v", good, spec)
		}
	}
}

// --- numerics ---------------------------------------------------------------

// TestBudgetsNumericValidity: zero and negative bounds are rejected. Zero is
// invalid rather than "unset" because the pointer already expresses
// declared-ness, so a zero bound would be a budget exhausted before any work.
func TestBudgetsNumericValidity(t *testing.T) {
	cases := []struct {
		name  string
		kind  BudgetKind
		block string
	}{
		{"turn-zero", BudgetKindTurn, "  turn:\n    maxTurns: 0\n    onExhaustion: fail\n"},
		{"turn-negative", BudgetKindTurn, "  turn:\n    maxTurns: -1\n    onExhaustion: fail\n"},
		{"wallClock-zero", BudgetKindWallClock, "  wallClock:\n    maxSeconds: 0\n    onExhaustion: fail\n"},
		{"wallClock-negative", BudgetKindWallClock, "  wallClock:\n    maxSeconds: -30\n    onExhaustion: fail\n"},
		{"toolCall-negative", BudgetKindToolCall, "  toolCall:\n    maxToolCalls: -5\n    onExhaustion: fail\n  acknowledgeObserved:\n    - toolCall\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ds := compileErr(t, baseBudgetsManifest("budgets:\n"+tc.block), syntheticYAML)
			code := "harness.budgets." + string(tc.kind) + ".invalid"
			if !hasCode(ds, code) {
				t.Fatalf("expected %s, got: %v", code, ds)
			}
		})
	}
}

// --- unknown keys (fail-closed decode) --------------------------------------

// TestBudgetsUnknownKeysRejected: an unknown budget kind and an unknown field
// inside a budget are both rejected at decode. This is what makes the seven
// kinds a genuinely CLOSED set rather than a free-form map.
func TestBudgetsUnknownKeysRejected(t *testing.T) {
	cases := []string{
		"budgets:\n  bogus:\n    maxTurns: 5\n    onExhaustion: fail\n",
		"budgets:\n  turn:\n    maxTurns: 5\n    onExhaustion: fail\n    extra: 1\n",
	}
	for _, block := range cases {
		ds := compileErr(t, baseBudgetsManifest(block), syntheticYAML)
		if !hasCode(ds, "harness.decode.unknownField") {
			t.Fatalf("expected unknownField rejection for %q, got: %v", block, ds)
		}
	}
}

// TestBudgetsKeyCaseLenienceCannotSmuggleAKind documents a second KNOWN DECODER
// GAP honestly rather than asserting a guarantee that does not exist: key
// matching is case-INSENSITIVE for the whole schema (encoding/json behaviour,
// shared by every field of the Document, not introduced by budgets), so
// `Turn:` is accepted as `turn:`. Fixing that belongs in the decode layer.
//
// What matters for THIS slice, and what is pinned here, is that the leniency is
// only about spelling: a case-variant key still binds to the SAME typed field,
// so it resolves to the same classified kind and cannot introduce an eighth,
// unclassified budget that bypasses the enforcement-class gate.
func TestBudgetsKeyCaseLenienceCannotSmuggleAKind(t *testing.T) {
	p, err := CompileBytes([]byte(baseBudgetsManifest(
		"budgets:\n  Turn:\n    maxTurns: 5\n    onExhaustion: fail\n")), syntheticYAML)
	if err != nil {
		// Also acceptable (and strictly better): the decoder rejects it.
		return
	}
	all := p.Budgets()
	if len(all) != 1 || all[0].Kind != BudgetKindTurn || all[0].Enforcement != BudgetEnforced || all[0].Limit != 5 {
		t.Fatalf("a case-variant key must resolve to the same classified kind, got %+v", all)
	}
	// And the class gate still applies to a case-variant unavailable kind.
	ds := compileErr(t, baseBudgetsManifest(
		"budgets:\n  Cost:\n    maxCostMicroUSD: 10\n    onExhaustion: fail\n"), syntheticYAML)
	if !hasCode(ds, "harness.budgets.cost.unavailable") {
		t.Fatalf("case leniency must not bypass the class gate, got: %v", ds)
	}
}

// TestBudgetsAtMostOneSpecPerKind: a budget kind is a typed field, not a map
// entry, so a kind can never resolve to two competing budgets.
//
// KNOWN DECODER GAP, stated rather than hidden: a duplicated YAML mapping key is
// currently accepted last-wins by the shared decoder for EVERY section of the
// schema (metadata, provider, agent, ...), not just budgets; fixing it belongs
// in the decode layer, not here. What this test pins is the property budgets can
// guarantee on its own — whatever the decoder does with a duplicate key, at most
// one spec per kind ever reaches the Plan, so a duplicate can never produce two
// conflicting bounds for the same quantity.
func TestBudgetsAtMostOneSpecPerKind(t *testing.T) {
	raw := baseBudgetsManifest("budgets:\n" +
		"  turn:\n    maxTurns: 5\n    onExhaustion: graceful\n" +
		"  turn:\n    maxTurns: 9\n    onExhaustion: fail\n")
	p, err := CompileBytes([]byte(raw), syntheticYAML)
	if err != nil {
		// Also acceptable (and strictly better): the decoder rejects it.
		return
	}
	seen := map[BudgetKind]int{}
	for _, s := range p.Budgets() {
		seen[s.Kind]++
	}
	if seen[BudgetKindTurn] != 1 {
		t.Fatalf("a kind must yield exactly one spec, got %d: %+v", seen[BudgetKindTurn], p.Budgets())
	}
}

// --- D3: outputToken must never be conflatable with the per-request cap ------

// TestBudgetsOutputTokenNotConflatable pins the D3 distinction three ways:
// the budget's field name is not writable as `maxOutputTokens`; the two fields
// are genuinely different quantities and may therefore coexist; and a cumulative
// budget below the per-request cap — the signature of an author who thought they
// were the same field — is rejected with an explanation.
func TestBudgetsOutputTokenNotConflatable(t *testing.T) {
	// 1. `maxOutputTokens` is not a key of the budget at all: writing the
	//    per-request name inside the cumulative budget is a decode error.
	ds := compileErr(t, baseBudgetsManifest(
		"budgets:\n  outputToken:\n    maxOutputTokens: 1000\n    onExhaustion: fail\n"), syntheticYAML)
	if !hasCode(ds, "harness.decode.unknownField") {
		t.Fatalf("the per-request field name must be unwritable inside the cumulative budget, got: %v", ds)
	}

	// 2. They are different quantities, so declaring BOTH is legal (unlike the
	//    same-quantity overlaps, which are rejected outright).
	p := compileOK(t, budgetsManifestWithLimits(
		"  limits:\n    maxOutputTokens: 4096\n",
		"budgets:\n  outputToken:\n    maxCumulativeOutputTokens: 500000\n    onExhaustion: graceful\n"+
			"  acknowledgeObserved:\n    - outputToken\n"))
	spec, ok := p.Budget(BudgetKindOutputToken)
	if !ok {
		t.Fatalf("cumulative output-token budget missing")
	}
	if spec.Limit != 500000 {
		t.Errorf("cumulative budget must keep its own value, got %+v", spec)
	}
	if p.Limits().MaxOutputTokens != 4096 {
		t.Errorf("the per-request cap must be untouched by the budget, got %d", p.Limits().MaxOutputTokens)
	}

	// 3. A cumulative budget below the per-request cap is incoherent and is
	//    rejected with a diagnostic that spells out the unit difference.
	ds3 := compileErr(t, budgetsManifestWithLimits(
		"  limits:\n    maxOutputTokens: 4096\n",
		"budgets:\n  outputToken:\n    maxCumulativeOutputTokens: 1000\n    onExhaustion: graceful\n"+
			"  acknowledgeObserved:\n    - outputToken\n"), syntheticYAML)
	if !hasCode(ds3, "harness.budgets.outputToken.belowPerRequestCap") {
		t.Fatalf("expected belowPerRequestCap, got: %v", ds3)
	}
	msg := diagMessage(ds3, "harness.budgets.outputToken.belowPerRequestCap")
	if !strings.Contains(msg, "SINGLE provider response") || !strings.Contains(msg, "SUM over the whole run") {
		t.Errorf("diagnostic must explain the unit-of-account difference: %q", msg)
	}
}

// --- D5: declaring a budget must not weaken anything ------------------------

// TestBudgetsOverlapWithLimitsRejected pins the D5 rule for the two genuine
// same-quantity overlaps: co-declaration is rejected so the manifest keeps a
// single source of truth, and a bound can never be silently relaxed by a second
// declaration elsewhere.
func TestBudgetsOverlapWithLimitsRejected(t *testing.T) {
	cases := []struct {
		name    string
		kind    BudgetKind
		limits  string
		budgets string
	}{
		{"turn-looser-budget", BudgetKindTurn, "  limits:\n    maxTurns: 10\n",
			"budgets:\n  turn:\n    maxTurns: 99\n    onExhaustion: graceful\n"},
		{"turn-stricter-budget", BudgetKindTurn, "  limits:\n    maxTurns: 99\n",
			"budgets:\n  turn:\n    maxTurns: 10\n    onExhaustion: graceful\n"},
		{"turn-equal", BudgetKindTurn, "  limits:\n    maxTurns: 10\n",
			"budgets:\n  turn:\n    maxTurns: 10\n    onExhaustion: graceful\n"},
		{"wallClock-looser-budget", BudgetKindWallClock, "  limits:\n    timeoutSeconds: 60\n",
			"budgets:\n  wallClock:\n    maxSeconds: 600\n    onExhaustion: fail\n"},
		{"wallClock-equal", BudgetKindWallClock, "  limits:\n    timeoutSeconds: 60\n",
			"budgets:\n  wallClock:\n    maxSeconds: 60\n    onExhaustion: fail\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ds := compileErr(t, budgetsManifestWithLimits(tc.limits, tc.budgets), syntheticYAML)
			code := "harness.budgets." + string(tc.kind) + ".overlapsLimits"
			if !hasCode(ds, code) {
				t.Fatalf("expected %s, got: %v", code, ds)
			}
			if msg := diagMessage(ds, code); !strings.Contains(msg, "single source of truth") {
				t.Errorf("diagnostic must state the rule: %q", msg)
			}
		})
	}

	// Either one alone is fine: the budget ADDS a bound where limits declares
	// none, and a limits-only manifest is completely unaffected by this slice.
	budgetOnly := compileOK(t, baseBudgetsManifest(
		"budgets:\n  turn:\n    maxTurns: 7\n    onExhaustion: graceful\n"))
	if spec, ok := budgetOnly.Budget(BudgetKindTurn); !ok || spec.Limit != 7 {
		t.Errorf("budget-only declaration must be accepted: %+v", budgetOnly.Budgets())
	}
	if budgetOnly.Limits().MaxTurns != 0 {
		t.Errorf("a budget must not write itself into agent.limits (that is Phase 9d's job)")
	}
	limitsOnly := compileOK(t, budgetsManifestWithLimits("  limits:\n    maxTurns: 12\n", ""))
	if limitsOnly.Limits().MaxTurns != 12 || len(limitsOnly.Budgets()) != 0 {
		t.Errorf("a limits-only manifest must be unchanged by this slice")
	}

	// A non-overlapping budget coexists with limits without complaint.
	mixed := compileOK(t, budgetsManifestWithLimits("  limits:\n    maxTurns: 12\n",
		"budgets:\n  wallClock:\n    maxSeconds: 30\n    onExhaustion: fail\n"))
	if _, ok := mixed.Budget(BudgetKindWallClock); !ok {
		t.Errorf("non-overlapping budget must be accepted alongside limits")
	}
}

// --- explain / redaction / accessors ----------------------------------------

// TestBudgetsExplainShowsEnforcementClass: the redacted report states the
// enforcement class for EVERY declared budget, so an operator reading it cannot
// mistake an observed bound for one that stops the run.
func TestBudgetsExplainShowsEnforcementClass(t *testing.T) {
	p := compileOK(t, baseBudgetsManifest("budgets:\n"+
		"  turn:\n    maxTurns: 40\n    onExhaustion: graceful\n"+
		"  toolCall:\n    maxToolCalls: 100\n    onExhaustion: fail\n"+
		"  acknowledgeObserved:\n    - toolCall\n"))

	rep := p.Explain()
	if len(rep.Budgets) != 2 {
		t.Fatalf("explain must report every declared budget, got %+v", rep.Budgets)
	}
	byKind := map[BudgetKind]BudgetSpec{}
	for _, b := range rep.Budgets {
		byKind[b.Kind] = b
	}
	if byKind[BudgetKindTurn].Enforcement != BudgetEnforced {
		t.Errorf("turn must be reported enforced: %+v", byKind[BudgetKindTurn])
	}
	if byKind[BudgetKindToolCall].Enforcement != BudgetObserved {
		t.Errorf("toolCall must be reported observed-not-enforced: %+v", byKind[BudgetKindToolCall])
	}

	js, err := p.ExplainJSON()
	if err != nil {
		t.Fatalf("ExplainJSON: %v", err)
	}
	for _, want := range []string{"\"budgets\"", "\"enforcement\": \"enforced\"", "\"enforcement\": \"observed\"", "\"unit\": \"calls\""} {
		if !strings.Contains(string(js), want) {
			t.Errorf("ExplainJSON missing %s", want)
		}
	}

	// Redaction: budgets contribute only kinds, numbers, classes, actions.
	// Every declared budget is also provenanced, and the provenance carries no
	// value (Ref must stay empty).
	found := 0
	for _, fs := range p.Provenance() {
		if strings.HasPrefix(fs.Field, "budgets.") {
			found++
			if fs.Ref != "" {
				t.Errorf("budget provenance must carry no ref: %+v", fs)
			}
		}
	}
	if found != 2 {
		t.Errorf("want provenance for each declared budget, got %d", found)
	}
}

// TestBudgetsDigestChangesPerFieldCategory: every budget field category affects
// the digest, so a budget can never be changed invisibly.
func TestBudgetsDigestChangesPerFieldCategory(t *testing.T) {
	const base = "budgets:\n  turn:\n    maxTurns: 40\n    onExhaustion: graceful\n"
	baseDigest := compileOK(t, baseBudgetsManifest(base)).Digest()
	if again := compileOK(t, baseBudgetsManifest(base)).Digest(); again != baseDigest {
		t.Fatalf("digest not stable across identical compiles: %q vs %q", again, baseDigest)
	}
	if baseDigest == compileOK(t, baseBudgetsManifest("")).Digest() {
		t.Fatalf("declaring a budget must change the digest")
	}

	variants := map[string]string{
		"different-limit":  "budgets:\n  turn:\n    maxTurns: 41\n    onExhaustion: graceful\n",
		"different-action": "budgets:\n  turn:\n    maxTurns: 40\n    onExhaustion: fail\n",
		"different-kind":   "budgets:\n  wallClock:\n    maxSeconds: 40\n    onExhaustion: graceful\n",
		"extra-kind": "budgets:\n  turn:\n    maxTurns: 40\n    onExhaustion: graceful\n" +
			"  toolCall:\n    maxToolCalls: 5\n    onExhaustion: fail\n  acknowledgeObserved:\n    - toolCall\n",
	}
	for name, block := range variants {
		if got := compileOK(t, baseBudgetsManifest(block)).Digest(); got == baseDigest {
			t.Errorf("%s: digest unchanged; every budget field category must affect it", name)
		}
	}
}

// TestBudgetsAccessorsDefensiveCopy: mutating a returned slice or spec must not
// affect the plan, nor a later Explain/digest.
func TestBudgetsAccessorsDefensiveCopy(t *testing.T) {
	p := compileOK(t, baseBudgetsManifest("budgets:\n  turn:\n    maxTurns: 40\n    onExhaustion: graceful\n"))
	before := p.Digest()

	got := p.Budgets()
	got[0].Limit = 99999
	got[0].Kind = "tampered"
	got[0].Enforcement = BudgetEnforced

	spec, _ := p.Budget(BudgetKindTurn)
	spec.Limit = 12345

	again := p.Budgets()
	if len(again) != 1 || again[0].Kind != BudgetKindTurn || again[0].Limit != 40 {
		t.Fatalf("plan budgets were mutated through an accessor: %+v", again)
	}
	if fresh, ok := p.Budget(BudgetKindTurn); !ok || fresh.Limit != 40 {
		t.Fatalf("Budget() returned a mutable view: %+v", fresh)
	}
	if p.Digest() != before {
		t.Fatalf("digest changed after accessor mutation: %q -> %q", before, p.Digest())
	}
	if rep := p.Explain(); len(rep.Budgets) != 1 || rep.Budgets[0].Limit != 40 {
		t.Fatalf("explain reflects mutated state: %+v", rep.Budgets)
	}
}
