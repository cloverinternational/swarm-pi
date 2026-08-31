// Harness Phase 6b tests — client-side hook EXECUTION wiring on the closed
// path. Two tiers:
//
//  1. Direct unit tests against buildHarnessHooksManager / the returned
//     agent.HooksManager, calling EmitToolBeforeExecute/EmitToolAfterExecute
//     itself (no LLM/provider round trip needed) to prove hooks actually run,
//     the env allowlist is enforced, script resolution/containment is
//     re-verified, disabled hooks never fire, http fails closed, and scope
//     mapping (global/interface/agent/tool) is correct.
//  2. A couple of full end-to-end wiring tests via New(WithHarnessPlan(...))
//     (mirroring harness_plan_test.go/harness_skills_test.go) proving
//     initHarnessAgent's atomic wiring: a bad hooks plan aborts construction
//     entirely (no observable client), a good one builds cleanly.
package client

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Swarm-Code/mono/swarm-sdk/harness"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/observability/noop"
)

// writeHookScript writes dir/hooks/<name> with the given body (0o755, so it
// is directly executable via "sh -c <abs-path>") and returns the
// manifest-relative path to it.
func writeHookScript(t *testing.T, dir, name, body string) string {
	t.Helper()
	hd := filepath.Join(dir, "hooks")
	if err := os.MkdirAll(hd, 0o755); err != nil {
		t.Fatalf("mkdir hooks dir: %v", err)
	}
	p := filepath.Join(hd, name)
	if err := os.WriteFile(p, []byte(body), 0o755); err != nil {
		t.Fatalf("write hook script %q: %v", name, err)
	}
	return filepath.ToSlash(filepath.Join("hooks", name))
}

// baseHookClientManifest returns a minimal valid manifest (matching
// harness/hooks_test.go's baseHookManifest shape) with the given raw hooks
// block (already including the `hooks:` key) and metadata.name spliced in, so
// scope: agent tests can control plan.Name().
func baseHookClientManifest(name, hooksBlock string) string {
	return "apiVersion: swarm.ai/v1alpha1\n" +
		"kind: Harness\n" +
		"metadata:\n  name: " + name + "\n" +
		"provider:\n  id: anthropic\n  model: claude-x\n  credential:\n    inline: test-key\n" +
		"agent:\n  systemPrompt:\n    inline: \"hi\"\n  tools: []\n" +
		hooksBlock
}

func compileHookPlan(t *testing.T, dir, manifest string) *harness.Plan {
	t.Helper()
	src := filepath.Join(dir, "harness.yaml")
	plan, err := harness.CompileBytes([]byte(manifest), src)
	if err != nil {
		t.Fatalf("CompileBytes: %v", err)
	}
	return plan
}

// --- Tier 1: buildHarnessHooksManager direct unit tests --------------------

// TestHarnessHooksZeroHooksNoManager: a hookless plan returns (nil, false, nil).
func TestHarnessHooksZeroHooksNoManager(t *testing.T) {
	dir := t.TempDir()
	plan := compileHookPlan(t, dir, baseHookClientManifest("zero", ""))

	hm, hasHooks, err := buildHarnessHooksManager(plan, noop.NewLogger(), "sdk")
	if err != nil {
		t.Fatalf("buildHarnessHooksManager: %v", err)
	}
	if hasHooks {
		t.Errorf("hasHooks = true for a hookless plan")
	}
	if hm != nil {
		t.Errorf("manager = %v, want nil for a hookless plan", hm)
	}
}

// TestHarnessHooksCommandRunsAndEnforcesAllowlist: a type: command hook with a
// one-name environment allowlist actually executes, an allowlisted var is
// visible to the child process, and a non-allowlisted ambient var is NOT.
func TestHarnessHooksCommandRunsAndEnforcesAllowlist(t *testing.T) {
	t.Setenv("HARNESS_HOOK_ALLOWED", "allowed-value")
	t.Setenv("HARNESS_HOOK_DISALLOWED", "disallowed-value")

	dir := t.TempDir()
	marker := filepath.Join(dir, "marker.txt")
	cmd := fmt.Sprintf("printf 'A=%%s|D=%%s' ${HARNESS_HOOK_ALLOWED:-MISSING} ${HARNESS_HOOK_DISALLOWED:-MISSING} > %s", marker)

	manifest := baseHookClientManifest("envtest", ""+
		"hooks:\n"+
		"  - id: envhook\n"+
		"    event: tool.before_execute\n"+
		"    scope: global\n"+
		"    type: command\n"+
		"    command: \""+cmd+"\"\n"+
		"    environment:\n"+
		"      - HARNESS_HOOK_ALLOWED\n")
	plan := compileHookPlan(t, dir, manifest)

	hm, hasHooks, err := buildHarnessHooksManager(plan, noop.NewLogger(), "sdk")
	if err != nil {
		t.Fatalf("buildHarnessHooksManager: %v", err)
	}
	if !hasHooks || hm == nil {
		t.Fatalf("expected an active manager, got hasHooks=%v hm=%v", hasHooks, hm)
	}

	if _, err := hm.EmitToolBeforeExecute(context.Background(), "Bash", map[string]any{"command": "ls"}); err != nil {
		t.Fatalf("EmitToolBeforeExecute: %v", err)
	}

	out, rerr := os.ReadFile(marker)
	if rerr != nil {
		t.Fatalf("hook did not run (marker file missing): %v", rerr)
	}
	got := string(out)
	if !strings.Contains(got, "A=allowed-value") {
		t.Errorf("marker %q: allowlisted var did not reach the hook process", got)
	}
	if !strings.Contains(got, "D=MISSING") {
		t.Errorf("marker %q: non-allowlisted var LEAKED into the hook process (ENV ALLOWLIST DISCIPLINE violated)", got)
	}
}

// TestHarnessHooksScriptResolvesAndRuns: a type: script hook resolves under
// plan.RevealSkillBaseDir() and executes successfully.
func TestHarnessHooksScriptResolvesAndRuns(t *testing.T) {
	dir := t.TempDir()
	marker := filepath.Join(dir, "script-marker.txt")
	rel := writeHookScript(t, dir, "run.sh", "#!/bin/sh\nprintf 'script-ran' > "+marker+"\n")

	manifest := baseHookClientManifest("scripttest", ""+
		"hooks:\n"+
		"  - id: scripthook\n"+
		"    event: tool.before_execute\n"+
		"    scope: global\n"+
		"    type: script\n"+
		"    path: "+rel+"\n")
	plan := compileHookPlan(t, dir, manifest)

	hm, hasHooks, err := buildHarnessHooksManager(plan, noop.NewLogger(), "sdk")
	if err != nil {
		t.Fatalf("buildHarnessHooksManager: %v", err)
	}
	if !hasHooks || hm == nil {
		t.Fatalf("expected an active manager, got hasHooks=%v hm=%v", hasHooks, hm)
	}
	if _, err := hm.EmitToolBeforeExecute(context.Background(), "Bash", nil); err != nil {
		t.Fatalf("EmitToolBeforeExecute: %v", err)
	}
	if _, rerr := os.ReadFile(marker); rerr != nil {
		t.Fatalf("script hook did not run (marker file missing): %v", rerr)
	}
}

// TestHarnessHooksScriptEscapeRejected: resolveHarnessHookScript (the exact
// re-verification buildHarnessHooksManager relies on) rejects a "../" escape
// directly. harness's own compile-time resolveContainedFile (Phase 6a) also
// rejects such a manifest earlier in the pipeline (TestHooksScriptPathResolves
// et al in harness/hooks_test.go cover that layer), so this test targets the
// CLIENT's OWN independent re-verification (defense in depth), not
// reachability via a normal compile.
func TestHarnessHooksScriptEscapeRejected(t *testing.T) {
	dir := t.TempDir()
	base := filepath.Join(dir, "manifest-root")
	if err := os.MkdirAll(base, 0o755); err != nil {
		t.Fatalf("mkdir base: %v", err)
	}
	// A file that genuinely exists just outside base, so a naive
	// existence-only check would not catch the escape.
	if err := os.WriteFile(filepath.Join(dir, "evil.sh"), []byte("#!/bin/sh\necho no\n"), 0o755); err != nil {
		t.Fatalf("write evil.sh: %v", err)
	}

	if _, err := resolveHarnessHookScript(base, base, "../evil.sh", "escape-hook"); err == nil {
		t.Fatalf("resolveHarnessHookScript: expected containment error, got nil")
	} else if !strings.Contains(err.Error(), "escapes the manifest directory") {
		t.Errorf("error = %q, want a containment-escape message", err.Error())
	}
}

// TestHarnessHooksScriptMissingAfterCompileFailsClosed: a script that existed
// (and hashed cleanly) at compile time but is deleted before
// buildHarnessHooksManager runs (a TOCTOU-style race) fails closed with no
// partial manager.
func TestHarnessHooksScriptMissingAfterCompileFailsClosed(t *testing.T) {
	dir := t.TempDir()
	rel := writeHookScript(t, dir, "vanish.sh", "#!/bin/sh\necho hi\n")
	manifest := baseHookClientManifest("vanishtest", ""+
		"hooks:\n"+
		"  - id: vanish\n"+
		"    event: tool.before_execute\n"+
		"    scope: global\n"+
		"    type: script\n"+
		"    path: "+rel+"\n")
	plan := compileHookPlan(t, dir, manifest)

	// Simulate the file disappearing between compile and load.
	if err := os.Remove(filepath.Join(dir, rel)); err != nil {
		t.Fatalf("remove script: %v", err)
	}

	hm, hasHooks, err := buildHarnessHooksManager(plan, noop.NewLogger(), "sdk")
	if err == nil {
		t.Fatalf("expected fail-closed error for a missing script, got hasHooks=%v hm=%v", hasHooks, hm)
	}
	if hm != nil || hasHooks {
		t.Errorf("expected no partial manager on error, got hasHooks=%v hm=%v", hasHooks, hm)
	}
}

// TestHarnessHooksDisabledNeverFires: a hook with enabled: false is present in
// plan.Hooks() but the plan has no active hooks (hasHooks == false) and
// nothing fires.
func TestHarnessHooksDisabledNeverFires(t *testing.T) {
	dir := t.TempDir()
	marker := filepath.Join(dir, "should-not-exist.txt")
	manifest := baseHookClientManifest("disabledtest", ""+
		"hooks:\n"+
		"  - id: disabledhook\n"+
		"    event: tool.before_execute\n"+
		"    scope: global\n"+
		"    type: command\n"+
		"    command: \"touch "+marker+"\"\n"+
		"    enabled: false\n")
	plan := compileHookPlan(t, dir, manifest)

	if len(plan.Hooks()) != 1 {
		t.Fatalf("plan.Hooks() len = %d, want 1 (disabled hook must still be resolved+present)", len(plan.Hooks()))
	}
	if plan.Hooks()[0].Enabled {
		t.Fatalf("plan.Hooks()[0].Enabled = true, want false")
	}

	hm, hasHooks, err := buildHarnessHooksManager(plan, noop.NewLogger(), "sdk")
	if err != nil {
		t.Fatalf("buildHarnessHooksManager: %v", err)
	}
	if hasHooks || hm != nil {
		t.Fatalf("an all-disabled plan must behave like a hookless one, got hasHooks=%v hm=%v", hasHooks, hm)
	}
	if _, rerr := os.Stat(marker); rerr == nil {
		t.Fatalf("disabled hook fired (marker file exists)")
	}
}

// TestHarnessHooksHTTPFailsClosed: a type: http hook (no executor exists in
// internal/hooks) makes buildHarnessHooksManager return a clear error rather
// than silently registering fewer hooks than declared.
func TestHarnessHooksHTTPFailsClosed(t *testing.T) {
	dir := t.TempDir()
	manifest := baseHookClientManifest("httptest", ""+
		"hooks:\n"+
		"  - id: webhook\n"+
		"    event: tool.after_execute\n"+
		"    scope: global\n"+
		"    type: http\n"+
		"    url: https://example.com/hook\n")
	plan := compileHookPlan(t, dir, manifest)

	hm, hasHooks, err := buildHarnessHooksManager(plan, noop.NewLogger(), "sdk")
	if err == nil {
		t.Fatalf("expected a fail-closed error for type=http, got hasHooks=%v hm=%v", hasHooks, hm)
	}
	if !strings.Contains(err.Error(), "type=http is not yet supported") {
		t.Errorf("error = %q, want the explicit type=http-unsupported message", err.Error())
	}
	if hm != nil || hasHooks {
		t.Errorf("expected no partial manager on error, got hasHooks=%v hm=%v", hasHooks, hm)
	}
}

// TestHarnessHooksScopeMapping: one sub-test per scope (global/interface/
// agent/tool) confirming the hook only fires for a matching scope/matcher,
// and a global-scope hook fires unconditionally.
func TestHarnessHooksScopeMapping(t *testing.T) {
	newManifestAndMarker := func(t *testing.T, dir, name, scopeBlock string) (*harness.Plan, string) {
		marker := filepath.Join(dir, "marker-"+name+".txt")
		manifest := baseHookClientManifest(name, ""+
			"hooks:\n"+
			"  - id: "+name+"hook\n"+
			"    event: tool.before_execute\n"+
			scopeBlock+
			"    type: command\n"+
			"    command: \"touch "+marker+"\"\n")
		return compileHookPlan(t, dir, manifest), marker
	}
	fired := func(t *testing.T, hm hooksManagerIface, marker string) bool {
		t.Helper()
		_ = os.Remove(marker)
		if _, err := hm.EmitToolBeforeExecute(context.Background(), "Bash", nil); err != nil {
			t.Fatalf("EmitToolBeforeExecute: %v", err)
		}
		_, statErr := os.Stat(marker)
		return statErr == nil
	}

	t.Run("global fires unconditionally", func(t *testing.T) {
		dir := t.TempDir()
		plan, marker := newManifestAndMarker(t, dir, "global", "    scope: global\n")
		hm, hasHooks, err := buildHarnessHooksManager(plan, noop.NewLogger(), "some-random-interface")
		if err != nil || !hasHooks {
			t.Fatalf("buildHarnessHooksManager: hasHooks=%v err=%v", hasHooks, err)
		}
		if !fired(t, hm, marker) {
			t.Errorf("global-scope hook did not fire")
		}
	})

	t.Run("interface scope matches only the constructed interface", func(t *testing.T) {
		dir := t.TempDir()
		plan, marker := newManifestAndMarker(t, dir, "iface", "    scope: interface\n    matcher: tui\n")

		hmMatch, hasHooks, err := buildHarnessHooksManager(plan, noop.NewLogger(), "tui")
		if err != nil || !hasHooks {
			t.Fatalf("buildHarnessHooksManager(tui): hasHooks=%v err=%v", hasHooks, err)
		}
		if !fired(t, hmMatch, marker) {
			t.Errorf("interface-scope hook did not fire for the matching interfaceID")
		}

		hmMismatch, hasHooks2, err2 := buildHarnessHooksManager(plan, noop.NewLogger(), "headless")
		// A non-matching interfaceID means the hook is excluded at BUILD time
		// (harnessHookApplies), not merely inert: it is never registered at
		// all, so the manager is nil (mirrors an all-disabled/hookless plan).
		if err2 != nil || hasHooks2 || hmMismatch != nil {
			t.Fatalf("buildHarnessHooksManager(headless): expected (nil, false, nil) for a non-matching interfaceID, got hasHooks=%v hm=%v err=%v", hasHooks2, hmMismatch, err2)
		}
		_ = marker
	})

	t.Run("agent scope matches only the plan's own declared name", func(t *testing.T) {
		dir := t.TempDir()
		// metadata.name == "agentmatch" AND matcher == "agentmatch": must fire.
		planMatch, markerMatch := newManifestAndMarker(t, dir, "agentmatch", "    scope: agent\n    matcher: agentmatch\n")
		hmMatch, hasHooks, err := buildHarnessHooksManager(planMatch, noop.NewLogger(), "sdk")
		if err != nil || !hasHooks {
			t.Fatalf("buildHarnessHooksManager: hasHooks=%v err=%v", hasHooks, err)
		}
		if !fired(t, hmMatch, markerMatch) {
			t.Errorf("agent-scope hook did not fire when matcher == plan.Name()")
		}

		// metadata.name == "agentmismatch" but matcher references a different
		// agent identity: must never fire.
		planMismatch, markerMismatch := newManifestAndMarker(t, dir, "agentmismatch", "    scope: agent\n    matcher: some-other-agent\n")
		hmMismatch, hasHooks2, err2 := buildHarnessHooksManager(planMismatch, noop.NewLogger(), "sdk")
		// A matcher that does not equal plan.Name() means the hook is
		// excluded at BUILD time (harnessHookApplies): never registered.
		if err2 != nil || hasHooks2 || hmMismatch != nil {
			t.Fatalf("buildHarnessHooksManager: expected (nil, false, nil) for a non-matching agent matcher, got hasHooks=%v hm=%v err=%v", hasHooks2, hmMismatch, err2)
		}
		_ = markerMismatch
	})

	t.Run("tool scope matches only the declared tool name", func(t *testing.T) {
		dir := t.TempDir()
		plan, marker := newManifestAndMarker(t, dir, "tool", "    scope: tool\n    matcher: Bash\n")
		hm, hasHooks, err := buildHarnessHooksManager(plan, noop.NewLogger(), "sdk")
		if err != nil || !hasHooks {
			t.Fatalf("buildHarnessHooksManager: hasHooks=%v err=%v", hasHooks, err)
		}

		_ = os.Remove(marker)
		if _, err := hm.EmitToolBeforeExecute(context.Background(), "Edit", nil); err != nil {
			t.Fatalf("EmitToolBeforeExecute(Edit): %v", err)
		}
		if _, statErr := os.Stat(marker); statErr == nil {
			t.Errorf("tool-scope hook fired for a NON-matching tool name")
		}

		if !fired(t, hm, marker) {
			t.Errorf("tool-scope hook did not fire for the matching tool name (Bash)")
		}
	})
}

// --- Tier 2: full initHarnessAgent wiring (end-to-end) ----------------------

// TestHarnessHooksWiringZeroHooksBuildsUnchanged: a hookless plan builds a
// client exactly as before Phase 6b (no wiring error, no behavior change).
func TestHarnessHooksWiringZeroHooksBuildsUnchanged(t *testing.T) {
	c := mustBuild(t, minimalManifest)
	if c.agent == nil {
		t.Fatalf("client has no agent")
	}
}

// TestHarnessHooksWiringValidHooksBuildCleanly: a plan with an active,
// resolvable command hook builds successfully end-to-end through
// initHarnessAgent (proving SetHooksManager is reachable without erroring).
func TestHarnessHooksWiringValidHooksBuildCleanly(t *testing.T) {
	dir := t.TempDir()
	manifest := baseHookClientManifest("wireok", ""+
		"hooks:\n"+
		"  - id: wireok-hook\n"+
		"    event: tool.before_execute\n"+
		"    scope: global\n"+
		"    type: command\n"+
		"    command: \"true\"\n")
	plan := compileHookPlan(t, dir, manifest)

	c, err := New(WithHarnessPlan(plan))
	if err != nil {
		t.Fatalf("New(harness): %v", err)
	}
	t.Cleanup(func() { _ = c.Close() })
	if c.agent == nil {
		t.Fatalf("client has no agent")
	}
}

// TestHarnessHooksWiringBadHookAbortsAtomically: a plan whose script hook is
// deleted between compile and construction aborts New() ENTIRELY — no
// partial/observable client, mirroring the skill-tool build-error abort.
func TestHarnessHooksWiringBadHookAbortsAtomically(t *testing.T) {
	dir := t.TempDir()
	rel := writeHookScript(t, dir, "wire-vanish.sh", "#!/bin/sh\necho hi\n")
	manifest := baseHookClientManifest("wirebad", ""+
		"hooks:\n"+
		"  - id: wirebad-hook\n"+
		"    event: tool.before_execute\n"+
		"    scope: global\n"+
		"    type: script\n"+
		"    path: "+rel+"\n")
	plan := compileHookPlan(t, dir, manifest)

	if err := os.Remove(filepath.Join(dir, rel)); err != nil {
		t.Fatalf("remove script: %v", err)
	}

	c, err := New(WithHarnessPlan(plan))
	if err == nil {
		if c != nil {
			_ = c.Close()
		}
		t.Fatalf("expected a fail-closed construction error, got a client")
	}
	if c != nil {
		t.Fatalf("client must be nil on fail-closed construction")
	}
}
