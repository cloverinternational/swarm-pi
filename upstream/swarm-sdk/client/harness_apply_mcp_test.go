// Phase 7d tests: mcp-only plan changes must be CLASSIFIED (never silently
// applied), mirroring Phase 5c's skills fix and Phase 6c's hooks fix, plus the
// re-apply MCP runtime LIFECYCLE regression disclosed by the Phase 7c review.
//
// Two concerns live in this file because they are two halves of the same
// apply/reload slice:
//
//  1. Classification. Phase 7a folded the `mcp:` section into the plan digest,
//     so an mcp-only change ALREADY forced the hot rebuild — it just reported
//     nothing. See harness_apply.go (classifyHarnessPlanChange's mcp
//     comparison, and harnessMCPLabel).
//
//  2. Lifecycle. Unlike hooks (whose manager is attached only to the freshly
//     built agent and never stored on *Client — see the note atop
//     harness_apply_hooks_test.go), MCP DOES retain client-level state:
//     c.mcpManager and c.harnessMCPExposure. That makes the stale-pointer
//     regression real here, and TestApplyHarnessPlanMcpReapplyStopsPrevious
//     Manager pins the fix in client/harness_plan.go's initHarnessAgent.
package client

import (
	"context"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/mcp"
)

// mcpApplyManifest returns a hot-apply baseline manifest that is byte-identical
// except for the spliced mcp block, so any classified difference between two of
// these manifests can ONLY be the mcp section. approvalMode is readonly and
// tools are fixed, matching harnessMCPClient's shape.
func mcpApplyManifest(mcpBlock string) string {
	return "apiVersion: swarm.ai/v1alpha1\n" +
		"kind: Harness\n" +
		"metadata:\n  name: apply-mcp\n" +
		"provider:\n  id: anthropic\n  model: claude-x\n  credential:\n    inline: test-key\n" +
		"agent:\n  systemPrompt:\n    inline: \"PROMPT-ONE\"\n  tools:\n    - forge.read\n" +
		"  limits:\n    maxOutputTokens: 4096\n    maxTurns: 12\n    timeoutSeconds: 600\n" +
		"permissions:\n  approvalMode: readonly\n" +
		mcpBlock
}

// The mcp blocks below differ from mcpBlockBase in EXACTLY ONE dimension each,
// so each classification test isolates one field of harnessMCPLabel.
const (
	mcpBlockBase = "mcp:\n" +
		"  - id: alpha\n" +
		"    type: stdio\n" +
		"    command: /usr/bin/true\n" +
		"    args: [\"--mode=v1\"]\n"

	// + a second server (beta).
	mcpBlockTwo = "mcp:\n" +
		"  - id: alpha\n" +
		"    type: stdio\n" +
		"    command: /usr/bin/true\n" +
		"    args: [\"--mode=v1\"]\n" +
		"  - id: beta\n" +
		"    type: stdio\n" +
		"    command: /usr/bin/true\n"

	// same id/command, different ARGS.
	mcpBlockArgsChanged = "mcp:\n" +
		"  - id: alpha\n" +
		"    type: stdio\n" +
		"    command: /usr/bin/true\n" +
		"    args: [\"--mode=v2\"]\n"

	// same everything, ENABLED flipped to false.
	mcpBlockDisabled = "mcp:\n" +
		"  - id: alpha\n" +
		"    type: stdio\n" +
		"    command: /usr/bin/true\n" +
		"    args: [\"--mode=v1\"]\n" +
		"    enabled: false\n"

	// same everything, plus a TOOLS allowlist.
	mcpBlockTools = "mcp:\n" +
		"  - id: alpha\n" +
		"    type: stdio\n" +
		"    command: /usr/bin/true\n" +
		"    args: [\"--mode=v1\"]\n" +
		"    tools: [\"keep_me\"]\n"

	// same everything, but an EXCLUDE list instead of the allowlist (declaring
	// both is a Phase 7a diagnostic, so they are never combined).
	mcpBlockExcludeTools = "mcp:\n" +
		"  - id: alpha\n" +
		"    type: stdio\n" +
		"    command: /usr/bin/true\n" +
		"    args: [\"--mode=v1\"]\n" +
		"    excludeTools: [\"keep_me\"]\n"

	// same everything, plus an env-NAME allowlist.
	mcpBlockEnvNames = "mcp:\n" +
		"  - id: alpha\n" +
		"    type: stdio\n" +
		"    command: /usr/bin/true\n" +
		"    args: [\"--mode=v1\"]\n" +
		"    environment: [\"HARNESS_MCP_7D_SECRET\"]\n"

	// an http server whose header references an ALLOWLISTED ENV VAR NAME —
	// never a literal. Used by the redaction test.
	mcpBlockHTTPHeaders = "mcp:\n" +
		"  - id: alpha\n" +
		"    type: http\n" +
		"    url: https://mcp.example.invalid/v1\n" +
		"    environment: [\"HARNESS_MCP_7D_SECRET\"]\n" +
		"    headers:\n      Authorization: HARNESS_MCP_7D_SECRET\n"
)

// assertMcpHotChange is the shared assertion body: an mcp difference must be a
// hot, non-silent, non-restart, non-forbidden change whose labels mention the
// server id and leak no secret VALUE.
func assertMcpHotChange(t *testing.T, oldManifest, newManifest string) (oldLabel, newLabel string) {
	t.Helper()
	dir := t.TempDir()
	oldPlan := compilePlanInDir(t, dir, mcpApplyManifest(oldManifest))
	newPlan := compilePlanInDir(t, dir, mcpApplyManifest(newManifest))

	diff, hot, restart, forbidden, err := classifyHarnessPlanChange(oldPlan, newPlan, false)
	if err != nil {
		t.Fatalf("classifyHarnessPlanChange: unexpected error: %v", err)
	}
	if containsStr(restart, harnessFieldMcp) || containsStr(forbidden, harnessFieldMcp) {
		t.Fatalf("mcp change must never be restart/forbidden: restart=%v forbidden=%v", restart, forbidden)
	}
	if !containsStr(hot, harnessFieldMcp) {
		t.Fatalf("hot = %v, want to include %q", hot, harnessFieldMcp)
	}
	var found bool
	for _, ch := range diff.Changes {
		if ch.Field != harnessFieldMcp {
			continue
		}
		found = true
		if ch.Class != harnessClassHot {
			t.Errorf("mcp change class = %q, want hot", ch.Class)
		}
		if !strings.Contains(ch.Old, "alpha") || !strings.Contains(ch.New, "alpha") {
			t.Errorf("labels must name the server id \"alpha\": old=%q new=%q", ch.Old, ch.New)
		}
		oldLabel, newLabel = ch.Old, ch.New
	}
	if !found {
		t.Fatalf("Diff.Changes = %+v, want a %q entry", diff.Changes, harnessFieldMcp)
	}
	return oldLabel, newLabel
}

// --- Classification -------------------------------------------------------

// TestClassifyHarnessPlanChangeMcpAdded: adding a server is a classified hot
// change and the new label names the added id.
func TestClassifyHarnessPlanChangeMcpAdded(t *testing.T) {
	_, newLabel := assertMcpHotChange(t, mcpBlockBase, mcpBlockTwo)
	if !strings.Contains(newLabel, "beta") {
		t.Errorf("new label %q does not mention added server id \"beta\"", newLabel)
	}
}

// TestClassifyHarnessPlanChangeMcpArgsOnly: the SAME id and command with
// different ARGS must still be classified. This is the case a naive label
// (id + type + enabled only) would silently miss.
func TestClassifyHarnessPlanChangeMcpArgsOnly(t *testing.T) {
	oldLabel, newLabel := assertMcpHotChange(t, mcpBlockBase, mcpBlockArgsChanged)
	if oldLabel == newLabel {
		t.Errorf("labels identical despite differing args: %q", oldLabel)
	}
}

// TestClassifyHarnessPlanChangeMcpEnabledOnly: flipping enabled is a real
// posture change (it removes the server from the manager AND from the Phase 7c
// exposure's declared set), so it must never be silent.
func TestClassifyHarnessPlanChangeMcpEnabledOnly(t *testing.T) {
	assertMcpHotChange(t, mcpBlockBase, mcpBlockDisabled)
}

// TestClassifyHarnessPlanChangeMcpToolsOnly: adding a tools allowlist changes
// which MCP tools can ever reach the provider — always classified.
func TestClassifyHarnessPlanChangeMcpToolsOnly(t *testing.T) {
	assertMcpHotChange(t, mcpBlockBase, mcpBlockTools)
}

// TestClassifyHarnessPlanChangeMcpToolsVsExcludeTools: an allowlist of X and a
// DENYLIST of X are near-opposite exposure postures over the same name. They
// must not collapse to the same label.
func TestClassifyHarnessPlanChangeMcpToolsVsExcludeTools(t *testing.T) {
	oldLabel, newLabel := assertMcpHotChange(t, mcpBlockTools, mcpBlockExcludeTools)
	if oldLabel == newLabel {
		t.Errorf("tools allowlist and excludeTools denylist collapsed to one label: %q", oldLabel)
	}
}

// TestClassifyHarnessPlanChangeMcpEnvNamesOnly: widening the env-NAME
// allowlist widens what the spawned server process can read, so it is a
// classified change even though no value is involved.
func TestClassifyHarnessPlanChangeMcpEnvNamesOnly(t *testing.T) {
	assertMcpHotChange(t, mcpBlockBase, mcpBlockEnvNames)
}

// TestClassifyHarnessPlanChangeMcpUnchanged: NO false positives. Two plans
// with an identical mcp section produce no mcp entry at all — including the
// map-valued Headers field, whose randomised Go iteration order would make a
// naive label non-deterministic and so report a phantom change. Repeated to
// make a randomised-order flake vanishingly unlikely to pass by chance.
func TestClassifyHarnessPlanChangeMcpUnchanged(t *testing.T) {
	for _, block := range []string{mcpBlockBase, mcpBlockHTTPHeaders, ""} {
		for i := 0; i < 8; i++ {
			dir := t.TempDir()
			oldPlan := compilePlanInDir(t, dir, mcpApplyManifest(block))
			newPlan := compilePlanInDir(t, dir, mcpApplyManifest(block))
			diff, hot, _, _, err := classifyHarnessPlanChange(oldPlan, newPlan, false)
			if err != nil {
				t.Fatalf("classifyHarnessPlanChange: unexpected error: %v", err)
			}
			if containsStr(hot, harnessFieldMcp) {
				t.Fatalf("unchanged mcp reported hot (iteration %d)", i)
			}
			for _, ch := range diff.Changes {
				if ch.Field == harnessFieldMcp {
					t.Fatalf("unchanged mcp produced a diff entry: old=%q new=%q", ch.Old, ch.New)
				}
			}
		}
	}
}

// TestHarnessMCPLabelRedaction: the label must never carry a resolved secret.
// This is structurally guaranteed — harness.McpServerSpec carries env var
// NAMES and header-name -> env-var-NAME references, never values — and this
// test pins that guarantee end to end with a REAL ambient env var set to a
// recognisable value: the NAME appears, the VALUE never does.
func TestHarnessMCPLabelRedaction(t *testing.T) {
	const secretValue = "SUPER-SECRET-VALUE-7D-MUST-NEVER-APPEAR"
	t.Setenv("HARNESS_MCP_7D_SECRET", secretValue)

	dir := t.TempDir()
	for _, block := range []string{mcpBlockEnvNames, mcpBlockHTTPHeaders} {
		plan := compilePlanInDir(t, dir, mcpApplyManifest(block))
		label := harnessMCPLabel(plan)
		if strings.Contains(label, secretValue) {
			t.Fatalf("label leaks a resolved env VALUE: %q", label)
		}
		if !strings.Contains(label, "HARNESS_MCP_7D_SECRET") {
			t.Errorf("label %q should carry the env var NAME (non-secret) for change detection", label)
		}
	}
}

// TestHarnessMCPLabelArgvHashUnambiguous: argv boundaries must be unambiguous.
// ["ab","c"] and ["a","bc"] are different commands and must hash differently;
// a naive concatenation would collide and silently miss the change.
func TestHarnessMCPLabelArgvHashUnambiguous(t *testing.T) {
	if a, b := harnessMCPArgvHash("cmd", []string{"ab", "c"}), harnessMCPArgvHash("cmd", []string{"a", "bc"}); a == b {
		t.Errorf("argv hash collided across different argv boundaries: %q", a)
	}
	if a, b := harnessMCPArgvHash("cmd", []string{"x"}), harnessMCPArgvHash("cmd", []string{"x"}); a != b {
		t.Errorf("argv hash is not deterministic: %q vs %q", a, b)
	}
}

// TestClassifyHarnessPlanChangeMcpDoesNotDisturbOtherFields: an mcp-only
// change classifies mcp and NOTHING else — no regression to provider/model/
// tools/skills/hooks/limits classification.
func TestClassifyHarnessPlanChangeMcpDoesNotDisturbOtherFields(t *testing.T) {
	dir := t.TempDir()
	oldPlan := compilePlanInDir(t, dir, mcpApplyManifest(mcpBlockBase))
	newPlan := compilePlanInDir(t, dir, mcpApplyManifest(mcpBlockArgsChanged))

	diff, hot, restart, forbidden, err := classifyHarnessPlanChange(oldPlan, newPlan, false)
	if err != nil {
		t.Fatalf("classifyHarnessPlanChange: unexpected error: %v", err)
	}
	if len(restart) != 0 || len(forbidden) != 0 {
		t.Fatalf("mcp-only change produced restart=%v forbidden=%v, want both empty", restart, forbidden)
	}
	if len(hot) != 1 || hot[0] != harnessFieldMcp {
		t.Fatalf("hot = %v, want exactly [%s]", hot, harnessFieldMcp)
	}
	if len(diff.Changes) != 1 || diff.Changes[0].Field != harnessFieldMcp {
		t.Fatalf("Diff.Changes = %+v, want exactly one mcp entry", diff.Changes)
	}
}

// --- Apply parity ---------------------------------------------------------

// TestApplyHarnessPlanMcpOnlyChange: a hot re-apply that changes ONLY the mcp
// section reports "mcp" in result.Applied (previously it reported nothing at
// all) and rebuilds the agent.
func TestApplyHarnessPlanMcpOnlyChange(t *testing.T) {
	dir := t.TempDir()
	oldPlan := compilePlanInDir(t, dir, mcpApplyManifest(mcpBlockBase))

	c, err := New(WithHarnessPlan(oldPlan))
	if err != nil {
		t.Fatalf("New(harness): %v", err)
	}
	t.Cleanup(func() { _ = c.Close() })
	agentBefore := c.agent

	newPlan := compilePlanInDir(t, dir, mcpApplyManifest(mcpBlockArgsChanged))
	res, err := c.ApplyHarnessPlan(context.Background(), newPlan, ApplyHarnessOptions{})
	if err != nil {
		t.Fatalf("ApplyHarnessPlan mcp-only: unexpected error: %v", err)
	}
	if len(res.Forbidden) != 0 || len(res.RestartRequired) != 0 {
		t.Fatalf("mcp-only apply misclassified: forbidden=%v restart=%v", res.Forbidden, res.RestartRequired)
	}
	if !containsStr(res.Applied, harnessFieldMcp) {
		t.Fatalf("Applied = %v, want to include %q", res.Applied, harnessFieldMcp)
	}
	if res.Digest != newPlan.Digest() {
		t.Errorf("result digest = %q, want new plan digest %q", res.Digest, newPlan.Digest())
	}
	if c.agent == nil || c.agent == agentBefore {
		t.Errorf("agent instance unchanged after a hot mcp-only apply; initHarnessAgent must rebuild it fresh")
	}
}

// --- Lifecycle regression (Phase 7c review finding) -----------------------

// TestApplyHarnessPlanMcpReapplyStopsPreviousManager: a re-apply must not leak
// the previous *mcp.RuntimeManager, and must not leave a stale
// harnessMCPExposure whose status handler still targets the OLD, already
// Stop()ed agent.
//
// Before the fix in initHarnessAgent, EVERY assertion below failed in a
// different way:
//   - the old manager's background reconnect goroutines were never cancelled,
//     so it kept retrying its (deliberately unreachable) server and kept
//     emitting status forever;
//   - each emission drove the OLD exposure, which called SetToolHints on the
//     OLD agent — an agent that had already been Stop()ed and replaced;
//   - and because the assignment was guarded by `if hasMCP`, re-applying a
//     plan with NO mcp section left c.mcpManager/c.harnessMCPExposure pointing
//     at the previous run's runtime indefinitely, unreferenced by any agent.
func TestApplyHarnessPlanMcpReapplyStopsPreviousManager(t *testing.T) {
	dir := t.TempDir()
	p1 := compilePlanInDir(t, dir, mcpApplyManifest(mcpBlockBase))

	c, err := New(WithHarnessPlan(p1))
	if err != nil {
		t.Fatalf("New(harness): %v", err)
	}
	t.Cleanup(func() { _ = c.Close() })

	oldMgr := c.MCPManager()
	oldExp := c.harnessMCPExposure
	oldAgent := c.agent
	if oldMgr == nil || oldExp == nil || oldAgent == nil {
		t.Fatalf("baseline plan must wire a manager+exposure+agent: mgr=%v exp=%v agent=%v",
			oldMgr != nil, oldExp != nil, oldAgent != nil)
	}

	// (1) Re-apply an mcp-only change. The manager and exposure must both be
	//     replaced by fresh instances, not reused and not left dangling.
	p2 := compilePlanInDir(t, dir, mcpApplyManifest(mcpBlockArgsChanged))
	res, err := c.ApplyHarnessPlan(context.Background(), p2, ApplyHarnessOptions{})
	if err != nil {
		t.Fatalf("ApplyHarnessPlan(p2): %v", err)
	}
	if !containsStr(res.Applied, harnessFieldMcp) {
		t.Fatalf("Applied = %v, want to include %q", res.Applied, harnessFieldMcp)
	}
	if c.MCPManager() == oldMgr {
		t.Fatalf("client still holds the PREVIOUS RuntimeManager after re-apply")
	}
	if c.harnessMCPExposure == oldExp {
		t.Fatalf("client still holds the PREVIOUS harnessMCPExposure after re-apply")
	}

	// (2) The stale exposure must not be able to touch the LIVE agent: its
	//     hint setter is the old, Stop()ed agent, so even if something still
	//     held a reference and invoked it, the new agent is unreachable from
	//     it. Pointer identity, so this is race-free.
	if oldExp.setter == c.agent {
		t.Fatalf("stale exposure's hint setter targets the NEW live agent")
	}
	if oldExp.setter != oldAgent {
		t.Fatalf("stale exposure's hint setter is not the old agent; test assumption broken")
	}

	// (3) Behavioural proof that the old manager is genuinely STOPPED, not
	//     merely dereferenced. Its server (/usr/bin/true) exits immediately, so
	//     a LIVE manager is in a connect -> error -> backoff -> reconnect loop
	//     emitting status on every attempt (reconnectBase is 200ms).
	//
	//     We assert the LOOP IS DEAD, not that emissions are exactly zero.
	//     RuntimeManager.Stop() cancels each server's context but does NOT join
	//     the runServer goroutines (runtime_manager.go:114-127), so a single
	//     emit already in flight when Stop() returns can still land — that is
	//     Stop()'s real contract, and asserting "zero, ever" against it is
	//     flaky (reproduced ~1 run in 3). A bounded straggler is harmless here
	//     precisely because of assertion (2): the stale exposure's setter is the
	//     OLD, stopped agent, so a late emit cannot reach the live one.
	//
	//     A LEAKED manager is distinguishable and is what we actually test for:
	//     its reconnect loop emits forever, so its count keeps GROWING across
	//     successive windows. A stopped manager flatlines after at most the
	//     in-flight straggler.
	var emissions int32
	oldMgr.SetStatusHandler(func(mcp.ServerStatusRecord) { atomic.AddInt32(&emissions, 1) })
	time.Sleep(750 * time.Millisecond)
	settled := atomic.LoadInt32(&emissions)
	time.Sleep(1500 * time.Millisecond) // >= 7 reconnect intervals
	final := atomic.LoadInt32(&emissions)
	if final != settled {
		t.Errorf("stopped manager kept emitting (%d -> %d across a 1.5s window): its reconnect loop is still live",
			settled, final)
	}
	if settled > 1 {
		t.Errorf("stopped manager emitted %d updates after Stop(); expected at most one in-flight straggler", settled)
	}

	// (4) The hardest pre-fix case: re-applying a plan with NO mcp section at
	//     all. The old `if hasMCP` guard only ever OVERWROTE these fields, so
	//     they must now be explicitly CLEARED.
	midMgr := c.MCPManager()
	p3 := compilePlanInDir(t, dir, mcpApplyManifest(""))
	if _, err := c.ApplyHarnessPlan(context.Background(), p3, ApplyHarnessOptions{}); err != nil {
		t.Fatalf("ApplyHarnessPlan(p3, zero mcp): %v", err)
	}
	if c.MCPManager() != nil {
		t.Errorf("re-applying a zero-mcp plan retained a RuntimeManager (same as before: %v)", c.MCPManager() == midMgr)
	}
	if c.harnessMCPExposure != nil {
		t.Errorf("re-applying a zero-mcp plan retained a stale harnessMCPExposure")
	}
}
