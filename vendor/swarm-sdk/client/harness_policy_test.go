// Harness Phase 8d — action #9 verification: tool schemas, provider visibility,
// approvals, and recovery paths, each independently inspectable.
//
// Scope note (stated rather than fabricated): properties 1, 2 and 4 are fully
// assertable from this package because the closed harness path constructs the
// real tool instances, the real provider-visible tool list, and the real
// re-apply/rollback path here. Property 3 is asserted as the EXPOSURE-vs-
// PERMISSION separation this package actually owns; see
// TestHarnessApprovalsAndAcknowledgementAreOrthogonal for exactly what is and
// is not proven here.
package client

import (
	"context"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/Swarm-Code/mono/swarm-sdk/harness"
)

// --- Property 1: tool schemas ---------------------------------------------

// TestHarnessBoundSchemaIsTheCatalogsSchema generalizes property 1 across every
// Phase-2-bound capability: for each bound id, the provider-visible tool name is
// exactly the name the catalog entry claims for it.
func TestHarnessBoundSchemaIsTheCatalogsSchema(t *testing.T) {
	ws := t.TempDir()
	for _, id := range boundHarnessCatalogIDs() {
		c, ok := harness.LookupCapability(id)
		if !ok {
			t.Fatalf("bound id %q is not in the catalog", id)
		}
		built, err := buildHarnessTools([]string{id}, ws)
		if err != nil {
			t.Fatalf("%s: buildHarnessTools: %v", id, err)
		}
		claimed := false
		for _, a := range c.RuntimeAliases {
			if a == built[0].runtimeName {
				claimed = true
			}
		}
		if !claimed {
			t.Errorf("%s: bound runtime name %q is not among the catalog's claimed aliases %v",
				id, built[0].runtimeName, c.RuntimeAliases)
		}
	}
}

// --- Property 2: provider visibility --------------------------------------

// TestHarnessProviderVisibilityEqualsActiveAllowlist proves the provider-visible
// tool set is EXACTLY the plan's active allowlist mapped through the binder —
// nothing ambient, nothing added by discovery.
func TestHarnessProviderVisibilityEqualsActiveAllowlist(t *testing.T) {
	const manifest = `apiVersion: swarm.ai/v1alpha1
kind: Harness
metadata:
  name: visibility
provider:
  id: anthropic
  model: claude-x
  credential:
    inline: test-key
agent:
  systemPrompt:
    inline: "v"
  tools:
    - forge.read
    - forge.apply_patch
    - forge.undo
permissions:
  approvalMode: interactive
`
	c := mustBuild(t, manifest)
	plan := c.opts.harness.plan

	// Derive the expected visible names from the PLAN, through the binder.
	built, err := buildHarnessTools(plan.Tools(), plan.Workspace())
	if err != nil {
		t.Fatalf("buildHarnessTools: %v", err)
	}
	want := make([]string, 0, len(built))
	for _, b := range built {
		want = append(want, b.runtimeName)
	}
	sort.Strings(want)

	got := exposedSorted(c)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("provider-visible set != plan allowlist:\n got  %v\n want %v", got, want)
	}
	// One visible tool per selected capability: no promotion, no collapsing.
	if len(got) != len(plan.Tools()) {
		t.Fatalf("visible=%d selected=%d; exposure must be 1:1 with the allowlist", len(got), len(plan.Tools()))
	}
	// And the snapshot's own record of the effective set agrees.
	snap := c.HarnessSnapshot()
	gotSnap := append([]string(nil), snap.ExposedTools...)
	sort.Strings(gotSnap)
	if !reflect.DeepEqual(gotSnap, want) {
		t.Fatalf("snapshot ExposedTools = %v, want %v", gotSnap, want)
	}
	gotIDs := append([]string(nil), snap.SelectedCatalogIDs...)
	sort.Strings(gotIDs)
	wantIDs := plan.Tools()
	sort.Strings(wantIDs)
	if !reflect.DeepEqual(gotIDs, wantIDs) {
		t.Fatalf("snapshot SelectedCatalogIDs = %v, want %v", gotIDs, wantIDs)
	}
}

// TestHarnessDiscoveryAndCodeModeUnreachableOnThisPath records, rather than
// fabricates, the state of the two special visibility behaviors named in action
// #9: `code.run_code` (collapses provider visibility to a single tool) and
// `meta.tool_search` (must never promote a capability outside the active
// allowlist). Both are catalogued OPT-IN capabilities that COMPILE, but
// neither can be constructed by this client package alone — Phase 10b routes
// both through the D1(b) host-binding-required bucket
// (harnessToolHostBindingReasons) with a PRECISE reason instead of the old
// generic "not yet bound in Phase 2" message, and buildHarnessTools still
// fails closed. That fail-closed fact is what is assertable here; the
// collapse/promotion semantics themselves are NOT, and would have to be
// asserted wherever those tools are actually implemented.
func TestHarnessDiscoveryAndCodeModeUnreachableOnThisPath(t *testing.T) {
	for _, id := range []string{"code.run_code", "meta.tool_search"} {
		if _, ok := harness.LookupCapability(id); !ok {
			t.Fatalf("%s must remain in the catalog", id)
		}
		if _, err := buildHarnessTools([]string{id}, t.TempDir()); err == nil {
			t.Fatalf("%s became bindable; the collapse/promotion semantics of this "+
				"capability are now reachable and MUST be asserted directly instead "+
				"of relying on this fail-closed placeholder", id)
		} else if !strings.Contains(err.Error(), "requires a host runtime binding") {
			t.Errorf("%s: want a fail-closed host-binding-required error, got %v", id, err)
		}
	}
}

// TestHarnessGatedCapabilityCannotReachTheClient is the Phase 8d end-to-end
// statement: a DEFER-DISCOVER or unacknowledged NEVER-DEFAULT capability is
// rejected at COMPILE time, so it can never reach the binder, the registry, or
// the provider at all. Before 8d these manifests compiled cleanly and were
// stopped (if at all) only by the coincidental absence of a constructor.
func TestHarnessGatedCapabilityCannotReachTheClient(t *testing.T) {
	mk := func(tools, perms string) string {
		return `apiVersion: swarm.ai/v1alpha1
kind: Harness
metadata:
  name: gated
provider:
  id: anthropic
  model: claude-x
  credential:
    inline: test-key
agent:
  systemPrompt:
    inline: "g"
  tools:
` + tools + perms
	}
	for _, tc := range []struct{ name, tools, perms string }{
		{"defer-discover", "    - steering.ask_user\n", ""},
		{"never-default unacknowledged", "    - forge.write\n", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := harness.CompileBytes([]byte(mk(tc.tools, tc.perms)), t.TempDir()+"/harness.yaml"); err == nil {
				t.Fatal("expected a compile-time rejection")
			}
		})
	}
	// Acknowledged NEVER-DEFAULT compiles, and is then stopped by the binder's
	// own fail-closed rule — proving the two gates are independent layers.
	//
	// forge.write is bound directly as of Phase 10b (it needs only the
	// workspace, like every other forge.* mutation tool), so this uses
	// computeruse.computer instead — a NEVER-DEFAULT id that is still
	// class-legal-but-unbound (routed through the D1(b) host-binding-required
	// bucket: it would need a real OS input executor this client refuses to
	// create implicitly).
	plan, err := harness.CompileBytes([]byte(mk("    - computeruse.computer\n",
		"permissions:\n  approvalMode: interactive\n  acknowledgeNeverDefault:\n    - computeruse.computer\n")),
		t.TempDir()+"/harness.yaml")
	if err != nil {
		t.Fatalf("acknowledged NEVER-DEFAULT must compile: %v", err)
	}
	if _, err := buildHarnessTools(plan.Tools(), plan.Workspace()); err == nil {
		t.Fatal("computeruse.computer has no Phase 2 constructor; the binder must still fail closed")
	}
}

// --- Property 3: approvals ------------------------------------------------

// TestHarnessApprovalsAndAcknowledgementAreOrthogonal proves the two gates do
// not substitute for one another.
//
// WHAT IS PROVEN HERE: (a) exposure and permission are separate stages — a
// mutation capability is exposed by selection while the permission posture is
// applied independently from approvalMode; (b) an acknowledged NEVER-DEFAULT
// capability does not relax the approval posture: `yolo` still requires the
// explicit WithHarnessAllowYolo posture even when the manifest acknowledges a
// NEVER-DEFAULT capability, and the acknowledgement never sets allowMutation.
//
// WHAT IS NOT PROVEN HERE: there is no compile-time rule tying a mutation
// capability to permissions.allowMutation — the catalog's side-effect note says
// "requires explicit mutation permission" but that requirement is enforced at
// tool-execution time by the PermissionChecker, not by the harness compiler.
// D4 forbids tightening KEEP, so this slice does NOT add such a rule, and this
// test does not pretend one exists.
func TestHarnessApprovalsAndAcknowledgementAreOrthogonal(t *testing.T) {
	// A mutation capability is exposed by selection, with the approval posture
	// applied separately.
	const mutating = `apiVersion: swarm.ai/v1alpha1
kind: Harness
metadata:
  name: mutate
provider:
  id: anthropic
  model: claude-x
  credential:
    inline: test-key
agent:
  systemPrompt:
    inline: "m"
  tools:
    - forge.apply_patch
permissions:
  approvalMode: interactive
`
	c := mustBuild(t, mutating)
	if got := exposedSorted(c); len(got) != 1 || got[0] != "apply_patch" {
		t.Fatalf("exposed = %v, want [apply_patch]", got)
	}
	if c.opts.approvalMode != "interactive" {
		t.Errorf("approvalMode = %q, want interactive (the permission stage is "+
			"independent of exposure)", c.opts.approvalMode)
	}
	if c.HarnessSnapshot().AllowMutation {
		t.Error("allowMutation must stay false unless the manifest declares it")
	}

	// An acknowledgement does NOT unlock a permissive approval posture.
	const ackYolo = `apiVersion: swarm.ai/v1alpha1
kind: Harness
metadata:
  name: ackyolo
provider:
  id: anthropic
  model: claude-x
  credential:
    inline: test-key
agent:
  systemPrompt:
    inline: "a"
  tools:
    - forge.read
    - forge.write
permissions:
  approvalMode: yolo
  acknowledgeNeverDefault:
    - forge.write
`
	plan := compilePlan(t, ackYolo)
	if _, err := validateHarnessApprovalMode(plan.Permissions().ApprovalMode, false); err == nil {
		t.Fatal("yolo must still require WithHarnessAllowYolo even when a NEVER-DEFAULT " +
			"capability is acknowledged; the acknowledgement is not an approval")
	}
	if plan.Permissions().AllowMutation {
		t.Error("the acknowledgement must not imply allowMutation")
	}
	// Conversely, the yolo posture does not acknowledge anything: dropping the
	// acknowledgement makes the same manifest fail to compile.
	noAck := strings.Replace(ackYolo,
		"  acknowledgeNeverDefault:\n    - forge.write\n", "", 1)
	if _, err := harness.CompileBytes([]byte(noAck), t.TempDir()+"/harness.yaml"); err == nil {
		t.Fatal("approvalMode: yolo must not substitute for the per-id acknowledgement")
	}
}

// --- Property 4: recovery paths -------------------------------------------

// TestHarnessApplyRollbackRestoresCapabilitySet asserts the recovery path at the
// granularity action #9 asks for: when an apply fails, the client is left on the
// PRIOR plan with the PRIOR effective capability set — both the provider-visible
// tool names and the selected catalog IDs — with no partially-applied exposure.
//
// WHICH RECOVERY STAGE THIS REACHES (stated, not glossed): ApplyHarnessPlan has
// three fail-closed stages — (4) classify/forbidden, (6) preflight construction,
// and (7) mid-apply rollback via a re-initHarnessAgent of the prior plan. A
// manifest can deterministically reach stages 4 and 6; stage 7 requires
// initHarnessAgent to fail AFTER a clean preflight, which no manifest input can
// force without fault injection. The case below is pinned to the stage it
// actually reaches (asserted explicitly), and the effective-capability-set
// assertions hold identically at every stage.
func TestHarnessApplyRollbackRestoresCapabilitySet(t *testing.T) {
	dir := t.TempDir()
	oldPlan := compilePlanIn(t, dir, baseApplyManifest)
	c, err := New(WithHarnessPlan(oldPlan))
	if err != nil {
		t.Fatalf("New(harness): %v", err)
	}
	t.Cleanup(func() { _ = c.Close() })

	beforeSnap := c.HarnessSnapshot()
	beforeExposed := exposedSorted(c)
	beforeIDs := append([]string(nil), beforeSnap.SelectedCatalogIDs...)

	// A plan that COMPILES (so it reaches apply) but cannot be bound: it adds a
	// second, valid capability alongside an unbound one, so a non-atomic apply
	// would leave the new tool partially exposed.
	const partialManifest = `apiVersion: swarm.ai/v1alpha1
kind: Harness
metadata:
  name: apply-base
provider:
  id: anthropic
  model: claude-x
  credential:
    inline: test-key
agent:
  systemPrompt:
    inline: "PROMPT-ONE"
  tools:
    - forge.read
    - forge.undo
    - interactive.ask_user_question
  limits:
    maxOutputTokens: 4096
    maxTurns: 12
    timeoutSeconds: 600
permissions:
  approvalMode: interactive
`
	newPlan := compilePlanIn(t, dir, partialManifest)
	res0, err := c.ApplyHarnessPlan(context.Background(), newPlan, ApplyHarnessOptions{})
	if err == nil {
		t.Fatal("expected the apply to fail (interactive.ask_user_question requires a host runtime binding)")
	}
	// Pin the stage: the unbindable capability is caught during classification,
	// i.e. BEFORE any live resource is touched.
	sawTools := false
	for _, f := range res0.Forbidden {
		if f == harnessFieldTools {
			sawTools = true
		}
	}
	if !sawTools {
		t.Fatalf("expected the tools change to be classified forbidden, got Forbidden=%v", res0.Forbidden)
	}
	if len(res0.Applied) != 0 {
		t.Fatalf("a failed apply must apply nothing, got Applied=%v", res0.Applied)
	}

	afterSnap := c.HarnessSnapshot()
	if afterSnap.PlanDigest != beforeSnap.PlanDigest {
		t.Errorf("plan digest changed on a failed apply: %s -> %s", beforeSnap.PlanDigest, afterSnap.PlanDigest)
	}
	if !snapEqual(beforeSnap, afterSnap) {
		t.Errorf("snapshot changed on a failed apply:\n before %+v\n after  %+v", beforeSnap, afterSnap)
	}
	if got := exposedSorted(c); !reflect.DeepEqual(got, beforeExposed) {
		t.Errorf("provider-visible tools changed on a failed apply: %v -> %v", beforeExposed, got)
	}
	afterIDs := append([]string(nil), afterSnap.SelectedCatalogIDs...)
	if !reflect.DeepEqual(afterIDs, beforeIDs) {
		t.Errorf("effective capability set changed on a failed apply: %v -> %v", beforeIDs, afterIDs)
	}
	// Specifically: NOTHING from the rejected plan leaked into exposure.
	for _, n := range exposedSorted(c) {
		if n == "semantic_grep" || n == "ask_user_question" {
			t.Errorf("partially-applied tool exposure: %q leaked from the rejected plan", n)
		}
	}
	// And the client is still usable on the prior plan (re-applying it is a no-op).
	res, err := c.ApplyHarnessPlan(context.Background(), oldPlan, ApplyHarnessOptions{})
	if err != nil {
		t.Fatalf("client unusable after rollback: %v", err)
	}
	if len(res.Applied) != 0 || res.Digest != beforeSnap.PlanDigest {
		t.Errorf("re-applying the prior plan should be a no-op, got %+v", res)
	}
}
