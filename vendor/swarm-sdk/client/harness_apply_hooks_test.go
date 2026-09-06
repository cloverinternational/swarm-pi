// Phase 6c tests: hooks-only plan changes must be classified (never silently
// applied, mirroring Phase 5c's skills fix). See harness_apply.go
// (classifyHarnessPlanChange, the hooks comparison, and harnessHooksLabel).
//
// Stale-reference note: unlike c.skillRegistry (Phase 5b/5c), there is NO
// separate *Client field holding a hooks-manager reference. initHarnessAgent
// (client/harness_plan.go) calls buildHarnessHooksManager(o.harness.plan, ...)
// and attaches the result directly onto the freshly-built *agent.Agent via
// a.SetHooksManager(hm) — never onto c itself — and initHarnessAgent always
// constructs a brand-new agent on every hot apply. So there is no analogous
// stale-pointer regression to fix here; this file adds no regression test
// for one (verified by reading client/harness_plan.go's initHarnessAgent and
// client/harness_hooks.go/client/client.go for any "c.<field> = hm"-shaped
// assignment — none exists).
package client

import (
	"context"
	"strings"
	"testing"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/observability/noop"
)

// hooksBaseManifest is a hot-apply baseline carrying one hook, hookA, a
// type: command hook scoped to the Bash tool.
const hooksBaseManifest = `apiVersion: swarm.ai/v1alpha1
kind: Harness
metadata:
  name: apply-hooks
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
  limits:
    maxOutputTokens: 4096
    maxTurns: 12
    timeoutSeconds: 600
hooks:
  - id: hookA
    event: tool.before_execute
    scope: tool
    matcher: Bash
    type: command
    command: "echo HOOKA_SECRET_COMMAND_V1"
permissions:
  approvalMode: interactive
`

// hooksZeroManifest is the same baseline with an EMPTY hooks selection.
const hooksZeroManifest = `apiVersion: swarm.ai/v1alpha1
kind: Harness
metadata:
  name: apply-hooks
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
  limits:
    maxOutputTokens: 4096
    maxTurns: 12
    timeoutSeconds: 600
hooks: []
permissions:
  approvalMode: interactive
`

// hooksTwoManifest declares TWO hooks (adds "hookB" to the baseline
// selection, hookA unchanged).
const hooksTwoManifest = `apiVersion: swarm.ai/v1alpha1
kind: Harness
metadata:
  name: apply-hooks
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
  limits:
    maxOutputTokens: 4096
    maxTurns: 12
    timeoutSeconds: 600
hooks:
  - id: hookA
    event: tool.before_execute
    scope: tool
    matcher: Bash
    type: command
    command: "echo HOOKA_SECRET_COMMAND_V1"
  - id: hookB
    event: tool.after_execute
    scope: global
    type: command
    command: "echo HOOKB_SECRET_COMMAND_V1"
permissions:
  approvalMode: interactive
`

// hooksCommandChangedManifest keeps hookA's id/event/scope/matcher but gives
// it a DIFFERENT inline command (=> different CommandHash only).
const hooksCommandChangedManifest = `apiVersion: swarm.ai/v1alpha1
kind: Harness
metadata:
  name: apply-hooks
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
  limits:
    maxOutputTokens: 4096
    maxTurns: 12
    timeoutSeconds: 600
hooks:
  - id: hookA
    event: tool.before_execute
    scope: tool
    matcher: Bash
    type: command
    command: "echo HOOKA_SECRET_COMMAND_V2_DIFFERENT"
permissions:
  approvalMode: interactive
`

// hooksScopeChangedManifest keeps hookA's id/command but changes ONLY its
// scope+matcher (tool/Bash -> global).
const hooksScopeChangedManifest = `apiVersion: swarm.ai/v1alpha1
kind: Harness
metadata:
  name: apply-hooks
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
  limits:
    maxOutputTokens: 4096
    maxTurns: 12
    timeoutSeconds: 600
hooks:
  - id: hookA
    event: tool.before_execute
    scope: global
    type: command
    command: "echo HOOKA_SECRET_COMMAND_V1"
permissions:
  approvalMode: interactive
`

// hooksPriorityTimeoutEnvChangedManifest keeps hookA's id/event/scope/
// matcher/command but changes priority, timeoutSeconds, enabled, and the
// environment allowlist.
const hooksPriorityTimeoutEnvChangedManifest = `apiVersion: swarm.ai/v1alpha1
kind: Harness
metadata:
  name: apply-hooks
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
  limits:
    maxOutputTokens: 4096
    maxTurns: 12
    timeoutSeconds: 600
hooks:
  - id: hookA
    event: tool.before_execute
    scope: tool
    matcher: Bash
    type: command
    command: "echo HOOKA_SECRET_COMMAND_V1"
    priority: 7
    timeoutSeconds: 45
    enabled: false
    environment:
      - SOME_ALLOWED_VAR
permissions:
  approvalMode: interactive
`

// hooksSwappedManifest selects ONLY "hookB" (the baseline's hookA is
// dropped, a different one is added) — an apply-parity "selection changed"
// scenario distinct from the pure add/zero cases above.
const hooksSwappedManifest = `apiVersion: swarm.ai/v1alpha1
kind: Harness
metadata:
  name: apply-hooks
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
  limits:
    maxOutputTokens: 4096
    maxTurns: 12
    timeoutSeconds: 600
hooks:
  - id: hookB
    event: tool.after_execute
    scope: global
    type: command
    command: "echo HOOKB_SECRET_COMMAND_V1"
permissions:
  approvalMode: interactive
`

// hooksUnchangedOtherFieldManifest declares the EXACT same hookA as
// hooksBaseManifest but bumps an unrelated hot field (maxTurns), to prove
// unchanged hooks produce no diff entry even when the plan differs.
const hooksUnchangedOtherFieldManifest = `apiVersion: swarm.ai/v1alpha1
kind: Harness
metadata:
  name: apply-hooks
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
  limits:
    maxOutputTokens: 4096
    maxTurns: 99
    timeoutSeconds: 600
hooks:
  - id: hookA
    event: tool.before_execute
    scope: tool
    matcher: Bash
    type: command
    command: "echo HOOKA_SECRET_COMMAND_V1"
permissions:
  approvalMode: interactive
`

// TestClassifyHarnessPlanChangeHooksAdded: adding a second hook classifies a
// redacted hooks change as hot (never restart/forbidden, never silent) —
// closing the Phase 6c gate (classifyHarnessPlanChange previously had ZERO
// references to hooks, so this exact scenario used to apply silently).
func TestClassifyHarnessPlanChangeHooksAdded(t *testing.T) {
	dir := t.TempDir()
	oldPlan := compilePlanInDir(t, dir, hooksBaseManifest)
	newPlan := compilePlanInDir(t, dir, hooksTwoManifest)

	diff, hot, restart, forbidden, err := classifyHarnessPlanChange(oldPlan, newPlan, false)
	if err != nil {
		t.Fatalf("classifyHarnessPlanChange: unexpected error: %v", err)
	}
	if containsStr(restart, harnessFieldHooks) || containsStr(forbidden, harnessFieldHooks) {
		t.Fatalf("hooks change must never be restart/forbidden: restart=%v forbidden=%v", restart, forbidden)
	}
	if !containsStr(hot, harnessFieldHooks) {
		t.Fatalf("hot = %v, want to include %q", hot, harnessFieldHooks)
	}
	var found bool
	for _, ch := range diff.Changes {
		if ch.Field != harnessFieldHooks {
			continue
		}
		found = true
		if ch.Class != harnessClassHot {
			t.Errorf("hooks change class = %q, want hot", ch.Class)
		}
		for _, label := range []string{ch.Old, ch.New} {
			if strings.Contains(label, "SECRET_COMMAND") {
				t.Errorf("hooks diff label leaks a raw inline command: %q", label)
			}
		}
		if !strings.Contains(ch.New, "hookB") {
			t.Errorf("new label %q does not mention added hook id \"hookB\"", ch.New)
		}
		if !strings.Contains(ch.Old, "hookA") {
			t.Errorf("old label %q does not mention hook id \"hookA\"", ch.Old)
		}
	}
	if !found {
		t.Fatalf("Diff.Changes = %+v, want a %q entry", diff.Changes, harnessFieldHooks)
	}
}

// TestClassifyHarnessPlanChangeHooksCommandHashOnly: the SAME hook id/event/
// scope/matcher with a different inline command (different resolved
// CommandHash) is still a classified, non-silent hot hooks change, and the
// raw command text never appears in either label.
func TestClassifyHarnessPlanChangeHooksCommandHashOnly(t *testing.T) {
	dir := t.TempDir()
	oldPlan := compilePlanInDir(t, dir, hooksBaseManifest)
	newPlan := compilePlanInDir(t, dir, hooksCommandChangedManifest)

	if oldPlan.Hooks()[0].CommandHash == newPlan.Hooks()[0].CommandHash {
		t.Fatalf("test setup invalid: command hash did not change")
	}

	diff, hot, restart, forbidden, err := classifyHarnessPlanChange(oldPlan, newPlan, false)
	if err != nil {
		t.Fatalf("classifyHarnessPlanChange: unexpected error: %v", err)
	}
	if !containsStr(hot, harnessFieldHooks) {
		t.Fatalf("hot = %v, want to include %q", hot, harnessFieldHooks)
	}
	if containsStr(restart, harnessFieldHooks) || containsStr(forbidden, harnessFieldHooks) {
		t.Fatalf("command-hash-only hooks change must be hot, not restart/forbidden")
	}
	var found bool
	for _, ch := range diff.Changes {
		if ch.Field != harnessFieldHooks {
			continue
		}
		found = true
		if ch.Old == ch.New {
			t.Errorf("hooks labels identical despite command hash change: %q", ch.Old)
		}
		for _, label := range []string{ch.Old, ch.New} {
			if strings.Contains(label, "SECRET_COMMAND") {
				t.Errorf("hooks diff label leaks a raw inline command: %q", label)
			}
			if !strings.Contains(label, "hookA") {
				t.Errorf("label %q does not mention hook id \"hookA\"", label)
			}
		}
	}
	if !found {
		t.Fatalf("Diff.Changes = %+v, want a %q entry", diff.Changes, harnessFieldHooks)
	}
}

// TestClassifyHarnessPlanChangeHooksScopeMatcherOnly: the SAME hook id/
// command with ONLY its scope+matcher changed (tool/Bash -> global) still
// produces a classified hot hooks change.
func TestClassifyHarnessPlanChangeHooksScopeMatcherOnly(t *testing.T) {
	dir := t.TempDir()
	oldPlan := compilePlanInDir(t, dir, hooksBaseManifest)
	newPlan := compilePlanInDir(t, dir, hooksScopeChangedManifest)

	diff, hot, restart, forbidden, err := classifyHarnessPlanChange(oldPlan, newPlan, false)
	if err != nil {
		t.Fatalf("classifyHarnessPlanChange: unexpected error: %v", err)
	}
	if !containsStr(hot, harnessFieldHooks) {
		t.Fatalf("hot = %v, want to include %q", hot, harnessFieldHooks)
	}
	if containsStr(restart, harnessFieldHooks) || containsStr(forbidden, harnessFieldHooks) {
		t.Fatalf("scope/matcher-only hooks change must be hot, not restart/forbidden")
	}
	var found bool
	for _, ch := range diff.Changes {
		if ch.Field != harnessFieldHooks {
			continue
		}
		found = true
		if !strings.Contains(ch.Old, ":tool:Bash:") || !strings.Contains(ch.New, ":global::") {
			t.Errorf("hooks labels do not reflect scope/matcher change: old=%q new=%q", ch.Old, ch.New)
		}
	}
	if !found {
		t.Fatalf("Diff.Changes = %+v, want a %q entry", diff.Changes, harnessFieldHooks)
	}
}

// TestClassifyHarnessPlanChangeHooksPriorityTimeoutEnvOnly: the SAME hook
// id/event/scope/matcher/command with ONLY priority, timeoutSeconds,
// enabled, and environment changed still produces a classified hot change.
func TestClassifyHarnessPlanChangeHooksPriorityTimeoutEnvOnly(t *testing.T) {
	dir := t.TempDir()
	oldPlan := compilePlanInDir(t, dir, hooksBaseManifest)
	newPlan := compilePlanInDir(t, dir, hooksPriorityTimeoutEnvChangedManifest)

	diff, hot, restart, forbidden, err := classifyHarnessPlanChange(oldPlan, newPlan, false)
	if err != nil {
		t.Fatalf("classifyHarnessPlanChange: unexpected error: %v", err)
	}
	if !containsStr(hot, harnessFieldHooks) {
		t.Fatalf("hot = %v, want to include %q", hot, harnessFieldHooks)
	}
	if containsStr(restart, harnessFieldHooks) || containsStr(forbidden, harnessFieldHooks) {
		t.Fatalf("priority/timeout/env-only hooks change must be hot, not restart/forbidden")
	}
	var found bool
	for _, ch := range diff.Changes {
		if ch.Field != harnessFieldHooks {
			continue
		}
		found = true
		if !strings.Contains(ch.New, "SOME_ALLOWED_VAR") {
			t.Errorf("new label %q does not reflect the added environment allowlist entry", ch.New)
		}
		if !strings.Contains(ch.New, ":false:") {
			t.Errorf("new label %q does not reflect enabled=false", ch.New)
		}
	}
	if !found {
		t.Fatalf("Diff.Changes = %+v, want a %q entry", diff.Changes, harnessFieldHooks)
	}
}

// TestClassifyHarnessPlanChangeHooksUnchanged: when hooks are byte-identical
// but SOME other hot field differs, no hooks diff entry is reported (no
// spurious noise).
func TestClassifyHarnessPlanChangeHooksUnchanged(t *testing.T) {
	dir := t.TempDir()
	oldPlan := compilePlanInDir(t, dir, hooksBaseManifest)
	newPlan := compilePlanInDir(t, dir, hooksUnchangedOtherFieldManifest)

	diff, hot, _, _, err := classifyHarnessPlanChange(oldPlan, newPlan, false)
	if err != nil {
		t.Fatalf("classifyHarnessPlanChange: unexpected error: %v", err)
	}
	if !containsStr(hot, harnessFieldMaxTurns) {
		t.Fatalf("test setup invalid: maxTurns not classified hot: %v", hot)
	}
	if containsStr(hot, harnessFieldHooks) {
		t.Errorf("hot = %v, must NOT include %q for an unchanged hook selection", hot, harnessFieldHooks)
	}
	for _, ch := range diff.Changes {
		if ch.Field == harnessFieldHooks {
			t.Errorf("Diff.Changes unexpectedly contains a %q entry for unchanged hooks: %+v", harnessFieldHooks, ch)
		}
	}
}

// TestApplyHarnessPlanHooksOnlyChange: apply parity. A hot re-apply that
// changes ONLY the hook selection is reported via result.Applied, and the
// initHarnessAgent rebuild constructs a genuinely NEW *agent.Agent instance
// (the closed hot-apply path documented at the top of harness_apply.go),
// proving the hooks manager wired onto it reflects buildHarnessHooksManager
// evaluated against the NEW plan — the same function, and the same
// interfaceID (ClientTypeSDK, "sdk", the default when WithHarnessPlan is
// used without an explicit client type), that initHarnessAgent itself calls.
//
// Client exposes no public seam to observe a *live* agent's wired hooks
// manager directly (internal/agent's hooksManager field is unexported and
// Phase 6b's own end-to-end wiring tests, e.g.
// TestHarnessHooksWiringValidHooksBuildCleanly, likewise only assert
// construction succeeds without inspecting hook-firing through Client) —
// so this test combines the black-box result (Applied/err/Digest/agent
// identity change) with the SAME white-box buildHarnessHooksManager firing
// check Tier 1 of harness_hooks_test.go uses, evaluated on oldPlan vs
// newPlan, to show the selection genuinely flips from hookA to hookB.
func TestApplyHarnessPlanHooksOnlyChange(t *testing.T) {
	dir := t.TempDir()
	oldPlan := compilePlanInDir(t, dir, hooksBaseManifest)

	c, err := New(WithHarnessPlan(oldPlan))
	if err != nil {
		t.Fatalf("New(harness): %v", err)
	}
	t.Cleanup(func() { _ = c.Close() })
	if c.agent == nil {
		t.Fatalf("client has no agent before apply")
	}
	agentBefore := c.agent

	newPlan := compilePlanInDir(t, dir, hooksSwappedManifest)
	res, err := c.ApplyHarnessPlan(context.Background(), newPlan, ApplyHarnessOptions{})
	if err != nil {
		t.Fatalf("ApplyHarnessPlan hooks-only: unexpected error: %v", err)
	}
	if len(res.Forbidden) != 0 || len(res.RestartRequired) != 0 {
		t.Fatalf("hooks-only apply misclassified: forbidden=%v restart=%v", res.Forbidden, res.RestartRequired)
	}
	if !containsStr(res.Applied, harnessFieldHooks) {
		t.Fatalf("Applied = %v, want to include %q", res.Applied, harnessFieldHooks)
	}
	if res.Digest != newPlan.Digest() {
		t.Errorf("result digest = %q, want new plan digest %q", res.Digest, newPlan.Digest())
	}
	if c.agent == nil {
		t.Fatalf("client has no agent after apply")
	}
	if c.agent == agentBefore {
		t.Errorf("agent instance unchanged after a hot hooks-only apply; initHarnessAgent must rebuild it fresh")
	}

	// Confirm the new selection (hookB, global/tool.after_execute) is what
	// buildHarnessHooksManager resolves from newPlan, and that the dropped
	// selection (hookA, tool/Bash) is what it resolved from oldPlan — using
	// the exact function and interfaceID initHarnessAgent itself invokes.
	oldHM, oldHasHooks, oldErr := buildHarnessHooksManager(oldPlan, noop.NewLogger(), string(ClientTypeSDK))
	if oldErr != nil || !oldHasHooks || oldHM == nil {
		t.Fatalf("buildHarnessHooksManager(oldPlan): hasHooks=%v err=%v", oldHasHooks, oldErr)
	}
	newHM, newHasHooks, newErr := buildHarnessHooksManager(newPlan, noop.NewLogger(), string(ClientTypeSDK))
	if newErr != nil || !newHasHooks || newHM == nil {
		t.Fatalf("buildHarnessHooksManager(newPlan): hasHooks=%v err=%v", newHasHooks, newErr)
	}

	// oldPlan's hook only fires for tool "Bash"; newPlan's hook is global
	// scope with event tool.after_execute, so before_execute never fires it.
	if _, err := oldHM.EmitToolBeforeExecute(context.Background(), "Bash", nil); err != nil {
		t.Fatalf("oldHM.EmitToolBeforeExecute: %v", err)
	}
	if results := newHM.EmitToolAfterExecute(context.Background(), "AnyTool", nil, nil, nil); len(results) == 0 {
		t.Errorf("newHM (global scope, hookB) did not fire on EmitToolAfterExecute for an arbitrary tool")
	}
}

// TestApplyHarnessPlanHooksToZero: a hot re-apply from a with-hooks plan to
// a ZERO-hooks plan succeeds and is reported via result.Applied. There is no
// c.<field> hooks registry to leave stale (see the file-level doc comment
// above): buildHarnessHooksManager's result is attached only to the freshly
// built *agent.Agent, and initHarnessAgent always rebuilds that agent fresh
// on every hot apply, so a zero-hooks reapply has no stale-pointer surface
// analogous to Phase 5c's c.skillRegistry fix.
func TestApplyHarnessPlanHooksToZero(t *testing.T) {
	dir := t.TempDir()
	oldPlan := compilePlanInDir(t, dir, hooksBaseManifest)

	c, err := New(WithHarnessPlan(oldPlan))
	if err != nil {
		t.Fatalf("New(harness): %v", err)
	}
	t.Cleanup(func() { _ = c.Close() })

	newPlan := compilePlanInDir(t, dir, hooksZeroManifest)
	res, err := c.ApplyHarnessPlan(context.Background(), newPlan, ApplyHarnessOptions{})
	if err != nil {
		t.Fatalf("ApplyHarnessPlan hooks-to-zero: unexpected error: %v", err)
	}
	if len(res.Forbidden) != 0 || len(res.RestartRequired) != 0 {
		t.Fatalf("hooks-to-zero apply misclassified: forbidden=%v restart=%v", res.Forbidden, res.RestartRequired)
	}
	if !containsStr(res.Applied, harnessFieldHooks) {
		t.Fatalf("Applied = %v, want to include %q", res.Applied, harnessFieldHooks)
	}
	if c.agent == nil {
		t.Fatalf("client has no agent after hooks-to-zero apply")
	}

	hm, hasHooks, herr := buildHarnessHooksManager(newPlan, noop.NewLogger(), string(ClientTypeSDK))
	if herr != nil {
		t.Fatalf("buildHarnessHooksManager(newPlan): %v", herr)
	}
	if hasHooks || hm != nil {
		t.Errorf("expected a hookless resolution for the zero-hooks plan, got hasHooks=%v hm=%v", hasHooks, hm)
	}
}
