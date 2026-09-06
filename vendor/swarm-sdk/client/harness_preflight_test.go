// Harness Phase 9d tests — TIER 2/3: the fail-closed preflight itself, adapter
// parity across every execution path, and §7.5 failure injection.
//
// What this file is trying to make impossible:
//
//	D2  a plan that requires a host capability nobody supplied nevertheless RUNS;
//	D2  a manifest that used to COMPILE now fails to compile (the check must be
//	    a run-time refusal, never a compile-time one);
//	D3  an unsupplied binding quietly becoming a no-op default;
//	D4  one execution path (daemon above all) skipping the check the others take;
//	D5  a persistence failure, a restart, or a concurrent Close degrading to
//	    best-effort instead of refusing;
//	--  a credential value, a prompt body, or a schedule payload appearing in a
//	    preflight error.
package client

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Swarm-Code/mono/swarm-sdk/harness"
)

// harness9dCompileIn compiles a manifest AT a fixed source path, so two plans
// can share a workspace/storage identity and a diff between them is HOT rather
// than restart-required. compilePlan (harness_plan_test.go) always uses a fresh
// temp dir, which is the opposite of what an apply test needs.
func harness9dCompileIn(t *testing.T, dir, manifest string) *harness.Plan {
	t.Helper()
	src := filepath.Join(dir, "harness.yaml")
	plan, err := harness.CompileBytes([]byte(manifest), src)
	if err != nil {
		t.Fatalf("CompileBytes: %v", err)
	}
	return plan
}

// ─── HARD INVARIANT: zero required bindings ⇒ exact no-op ──────────────────

// TestHarnessPreflightZeroBindingsNoOp mirrors Phase 8b's
// TestHarnessAgentsZeroSelectionNoOp: a plan that requires NOTHING must be
// byte-for-byte unaffected by this slice — no refusal, no manager, no goroutine,
// no file read, no behaviour delta.
func TestHarnessPreflightZeroBindingsNoOp(t *testing.T) {
	plan := compilePlan(t, harness9dBase)
	if got := plan.RequiredRuntimeBindings(); len(got) != 0 {
		t.Fatalf("the zero-binding baseline requires %v; fixture is wrong", got)
	}

	// Preflight on the zero-requirement plan is a no-op with an EMPTY registry:
	// supplying nothing is fine when nothing is required.
	if err := preflightHarnessBindings(plan, HarnessBindings{}); err != nil {
		t.Fatalf("zero requirements must preflight clean, got: %v", err)
	}
	if err := PreflightHarnessPlan(plan, HarnessBindings{}); err != nil {
		t.Fatalf("exported PreflightHarnessPlan disagreed: %v", err)
	}

	// Full construction is unchanged, and starts no extra goroutine.
	before := runtime.NumGoroutine()
	c := mustBuild(t, harness9dBase)
	if c.agentDef.Provider != "anthropic" || c.agentDef.Model != "claude-x" {
		t.Fatalf("unexpected agent def: %+v", c.agentDef)
	}
	if !strings.Contains(c.agent.SystemPrompt(), "PROMPT-BODY-SENTINEL-9D") {
		t.Errorf("system prompt not applied on the zero-binding path")
	}
	if c.opts.harness == nil || len(c.opts.harness.bindings.Supplied()) != 0 {
		t.Errorf("a client built without WithHarnessBindings must carry an empty registry")
	}
	if err := c.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	harness9dAssertNoGoroutineLeak(t, before)
}

// TestHarnessPreflightNoRegistryNeededForZeroBindingPlans: passing
// WithHarnessBindings on a zero-requirement plan changes nothing observable, so
// hosts can install it unconditionally without altering behaviour.
func TestHarnessPreflightNoRegistryNeededForZeroBindingPlans(t *testing.T) {
	plain := mustBuild(t, harness9dBase)
	withRegistry := mustBuild(t, harness9dBase, WithHarnessBindings(harness9dAllBindings()))

	if plain.agentDef.Provider != withRegistry.agentDef.Provider ||
		plain.agentDef.Model != withRegistry.agentDef.Model ||
		len(plain.agentDef.ToolHints) != len(withRegistry.agentDef.ToolHints) {
		t.Fatalf("supplying bindings altered a zero-requirement build: %+v vs %+v",
			plain.agentDef, withRegistry.agentDef)
	}
}

// ─── D2: compilation still succeeds; only EXECUTION is refused ─────────────

// TestHarnessUnsatisfiedRequirementsStillCompile is the plan.md §3.3 clause
// stated as a test: "Compilation can succeed with unresolved runtime
// requirements". No manifest that compiled before this slice may now fail.
func TestHarnessUnsatisfiedRequirementsStillCompile(t *testing.T) {
	for name, manifest := range map[string]string{
		"one schedule":  harness9dOneSchedule,
		"all eleven":    harness9dAllElevenBindings,
		"zero bindings": harness9dBase,
	} {
		plan := compilePlan(t, manifest) // t.Fatal on any compile error
		if plan.Digest() == "" {
			t.Errorf("%s: compiled plan has an empty digest", name)
		}
	}

	// And the requirements really are unsatisfied — i.e. the fixtures above are
	// not accidentally binding-free.
	plan := compilePlan(t, harness9dAllElevenBindings)
	if got := len(plan.RequiredRuntimeBindings()); got != len(harness9dEveryKind) {
		t.Fatalf("fixture requires %d bindings, want all %d", got, len(harness9dEveryKind))
	}
}

// TestHarnessPreflightRefusesConstruction: the interactive/TUI and print/
// `swarm -p` paths both reach client.New, and client.New must refuse.
func TestHarnessPreflightRefusesConstruction(t *testing.T) {
	before := runtime.NumGoroutine()
	c, err := buildHarnessClient(t, harness9dOneSchedule)
	if err == nil {
		_ = c.Close()
		t.Fatal("client.New built a client for a plan whose requirements nobody supplied")
	}
	if c != nil {
		t.Error("a refused construction must return a nil client, not a partial one")
	}
	if !errors.Is(err, ErrHarnessBindingUnsatisfied) {
		t.Fatalf("error does not wrap ErrHarnessBindingUnsatisfied: %v", err)
	}
	var pf *HarnessPreflightError
	if !errors.As(err, &pf) {
		t.Fatalf("error is not a *HarnessPreflightError: %T", err)
	}
	if len(pf.Unmet) != 5 {
		t.Errorf("want 5 unmet bindings for the lightest schedule, got %d: %+v", len(pf.Unmet), pf.Unmet)
	}
	// A refusal must leave nothing running behind it.
	harness9dAssertNoGoroutineLeak(t, before)
}

type typedNilZonedClock struct{}

func (*typedNilZonedClock) NowInZone(string) (time.Time, error) {
	return time.Time{}, nil
}

func (*typedNilZonedClock) NextAfter(string, string, time.Time) (time.Time, error) {
	return time.Time{}, nil
}

func TestHarnessPreflightRejectsTypedNilBinding(t *testing.T) {
	plan := compilePlan(t, harness9dOneSchedule)
	bindings := harness9dAllBindings()
	var clock *typedNilZonedClock
	bindings.SchedulerZonedClock = clock

	err := preflightHarnessBindings(plan, bindings)
	if !errors.Is(err, ErrHarnessBindingUnsatisfied) {
		t.Fatalf("typed-nil binding passed preflight, err=%v", err)
	}
	var pf *HarnessPreflightError
	if !errors.As(err, &pf) {
		t.Fatalf("error is not HarnessPreflightError: %T", err)
	}
	found := false
	for _, unmet := range pf.Unmet {
		if unmet.Binding == harness.BindingSchedulerZonedClock {
			found = true
		}
	}
	if !found {
		t.Fatalf("typed-nil zoned clock not reported unmet: %+v", pf.Unmet)
	}
}

// TestHarnessPreflightErrorNamesEveryBindingAndFieldPath (D2): an operator must
// be able to fix the manifest OR the host without guessing, so the message names
// every unmet kind AND every requiring field path.
func TestHarnessPreflightErrorNamesEveryBindingAndFieldPath(t *testing.T) {
	plan := compilePlan(t, harness9dAllElevenBindings)
	err := preflightHarnessBindings(plan, HarnessBindings{})
	if err == nil {
		t.Fatal("preflight passed a plan requiring all eleven bindings with an empty registry")
	}
	msg := err.Error()

	for _, kind := range harness9dEveryKind {
		if !strings.Contains(msg, string(kind)) {
			t.Errorf("preflight error omits binding %q:\n%s", kind, msg)
		}
	}
	for _, req := range plan.RequiredRuntimeBindings() {
		for _, field := range req.RequiredBy {
			if !strings.Contains(msg, field) {
				t.Errorf("preflight error omits requiring field path %q:\n%s", field, msg)
			}
		}
	}
	// Both schedules must be represented, not just the first.
	for _, field := range []string{"schedules[0]", "schedules[1]"} {
		if !strings.Contains(msg, field) {
			t.Errorf("preflight error omits %q:\n%s", field, msg)
		}
	}
	// And it must tell the operator what to DO.
	if !strings.Contains(msg, "WithHarnessBindings") {
		t.Errorf("preflight error does not name the remedy:\n%s", msg)
	}

	// The structural form carries the same information without string parsing.
	var pf *HarnessPreflightError
	if !errors.As(err, &pf) {
		t.Fatalf("not a *HarnessPreflightError: %T", err)
	}
	if len(pf.Unmet) != len(harness9dEveryKind) {
		t.Fatalf("structured error lists %d unmet, want %d", len(pf.Unmet), len(harness9dEveryKind))
	}
	for _, u := range pf.Unmet {
		if len(u.RequiredBy) == 0 {
			t.Errorf("unmet binding %q carries no RequiredBy paths", u.Binding)
		}
		if u.Reason == "" {
			t.Errorf("unmet binding %q carries no reason", u.Binding)
		}
		if u.Unsupported {
			t.Errorf("scheduler/approval binding %q reported as unsupported; it has an interface", u.Binding)
		}
	}
}

// TestHarnessPreflightErrorLeaksNoSecrets: the message quotes only kinds, field
// paths, and the compile-time reasons. A credential value, the document prompt,
// and a subagent prompt must never appear.
func TestHarnessPreflightErrorLeaksNoSecrets(t *testing.T) {
	plan := compilePlan(t, harness9dAllElevenBindings)
	_, err := buildHarnessClient(t, harness9dAllElevenBindings)
	if err == nil {
		t.Fatal("expected a refusal")
	}
	direct := preflightHarnessBindings(plan, HarnessBindings{})
	if direct == nil {
		t.Fatal("expected a refusal from preflightHarnessBindings")
	}
	for _, msg := range []string{err.Error(), direct.Error()} {
		for _, secret := range []string{
			"SECRET-CREDENTIAL-VALUE-9D",
			"PROMPT-BODY-SENTINEL-9D",
			"SUBAGENT-PROMPT-SENTINEL-9D",
		} {
			if strings.Contains(msg, secret) {
				t.Errorf("preflight error leaked %q:\n%s", secret, msg)
			}
		}
	}
}

// TestHarnessPreflightSatisfiedBySuppliedBindings: with every requirement
// supplied, the SAME plan that was refused above constructs normally. This is
// exit-gate clause (a) in miniature — the manifest did not change, the host did.
func TestHarnessPreflightSatisfiedBySuppliedBindings(t *testing.T) {
	c, err := buildHarnessClient(t, harness9dAllElevenBindings, WithHarnessBindings(harness9dAllBindings()))
	if err != nil {
		t.Fatalf("a fully supplied host must be allowed to run the plan: %v", err)
	}
	t.Cleanup(func() { _ = c.Close() })
	if c.agentDef.Model != "claude-x" {
		t.Errorf("unexpected agent def after a satisfied preflight: %+v", c.agentDef)
	}
}

// TestHarnessPreflightPartialSupplyStillRefuses (D2/D3 fail-closed): supplying
// SOME bindings never degrades to best-effort. Ten of eleven is a refusal.
func TestHarnessPreflightPartialSupplyStillRefuses(t *testing.T) {
	partial := harness9dAllBindings()
	partial.SchedulerSingleOwner = nil // the one nothing in-tree can supply

	plan := compilePlan(t, harness9dAllElevenBindings)
	err := preflightHarnessBindings(plan, partial)
	if err == nil {
		t.Fatal("preflight passed with a missing binding; it must fail closed")
	}
	var pf *HarnessPreflightError
	if !errors.As(err, &pf) {
		t.Fatalf("not a *HarnessPreflightError: %T", err)
	}
	if len(pf.Unmet) != 1 || pf.Unmet[0].Binding != harness.BindingSchedulerSingleOwner {
		t.Fatalf("want exactly scheduler.singleOwner unmet, got %+v", pf.Unmet)
	}
	if _, err := buildHarnessClient(t, harness9dAllElevenBindings, WithHarnessBindings(partial)); err == nil {
		t.Fatal("construction succeeded with a missing binding")
	}

	partial.SchedulerSingleOwner = NewHostAssertedSingleInstanceScheduleOwner()
	if err := preflightHarnessBindings(plan, partial); err != nil {
		t.Fatalf("explicit host single-instance assertion did not satisfy preflight: %v", err)
	}
}

// TestHarnessPreflightUnknownKindRefused: a requirement whose kind this adapter
// has NO interface for (Phase 9c's workflow bindings, whose host interfaces are
// Phase 10 scope) is refused on sight and marked Unsupported — never ignored as
// an unrecognised string. This is the property that keeps the preflight honest
// as the harness vocabulary grows.
func TestHarnessPreflightUnknownKindRefused(t *testing.T) {
	dir := t.TempDir()
	manifest := harness9dBase + "workflows:\n  - id: flow-1\n    file: ./flow.yaml\n    expectedVersion: 1.0.0\n"
	flow := "id: flow-1\nname: Flow One\nversion: 1.0.0\n" +
		"groups:\n  - id: g1\n    name: Group One\n    execution: sequential\n" +
		"    agents:\n      - name: Agent One\n        provider: anthropic\n        model: claude-x\n"
	if err := os.WriteFile(filepath.Join(dir, "flow.yaml"), []byte(flow), 0o600); err != nil {
		t.Fatalf("write flow: %v", err)
	}
	plan := harness9dCompileIn(t, dir, manifest) // still COMPILES (D2)

	req := plan.RequiredRuntimeBindings()
	if len(req) == 0 {
		t.Fatal("a declared workflow must require at least the workflow engine binding")
	}

	// Even a registry supplying EVERY kind this package knows cannot satisfy it.
	err := preflightHarnessBindings(plan, harness9dAllBindings())
	if err == nil {
		t.Fatal("preflight passed a requirement for which no interface exists; it must fail closed")
	}
	var pf *HarnessPreflightError
	if !errors.As(err, &pf) {
		t.Fatalf("not a *HarnessPreflightError: %T", err)
	}
	for _, u := range pf.Unmet {
		if !u.Unsupported {
			t.Errorf("unknown kind %q not marked Unsupported", u.Binding)
		}
	}
	if !strings.Contains(err.Error(), "no client binding interface is defined") {
		t.Errorf("error does not explain the unknown kind:\n%s", err)
	}
}

// ─── D4: every execution path crosses the SAME preflight ───────────────────

// TestHarnessPreflightNoBypass re-derives the call graph from source rather than
// trusting harnessPreflightChokepoints' prose. It pins that:
//
//	(1) initHarnessAgent — the chokepoint — has exactly the two known call sites;
//	(2) newClientFromHarness has exactly the one known call site;
//	(3) both functions actually invoke preflightHarnessBindings;
//	(4) no bypass identifier exists anywhere in the package.
//
// If a future change adds a third door, (1)/(2) fail and the author is forced to
// route it through preflight deliberately instead of by accident.
func TestHarnessPreflightNoBypass(t *testing.T) {
	sources := harness9dPackageSources(t, ".")

	callSites := func(needle string) []string {
		var out []string
		for path, body := range sources {
			for _, line := range strings.Split(body, "\n") {
				trimmed := strings.TrimSpace(line)
				if !strings.Contains(trimmed, needle) {
					continue
				}
				// Skip the declaration itself and pure comment lines.
				if strings.HasPrefix(trimmed, "//") || strings.HasPrefix(trimmed, "func ") {
					continue
				}
				out = append(out, path+": "+trimmed)
			}
		}
		return out
	}

	// (1) initHarnessAgent: construction on c and hot apply on an off-side
	// candidate. There is deliberately no rollback reconstruction call.
	gotInit := append(callSites("c.initHarnessAgent("), callSites("candidate.initHarnessAgent(")...)
	if len(gotInit) != 2 {
		t.Errorf("initHarnessAgent has %d call sites, want 2 (construct, off-side hot apply):\n%s",
			len(gotInit), strings.Join(gotInit, "\n"))
	}
	// (2) newClientFromHarness: client.go's single branch.
	if got := callSites("newClientFromHarness(o)"); len(got) != 1 {
		t.Errorf("newClientFromHarness has %d call sites, want 1:\n%s", len(got), strings.Join(got, "\n"))
	}

	// (3) both gate functions really call preflight.
	planSrc := sources["harness_plan.go"]
	if strings.Count(planSrc, "preflightHarnessBindings(") != 2 {
		t.Errorf("harness_plan.go must call preflightHarnessBindings exactly twice "+
			"(newClientFromHarness + initHarnessAgent), got %d",
			strings.Count(planSrc, "preflightHarnessBindings("))
	}

	// (4) no bypass identifier anywhere in the package.
	for _, banned := range []string{
		"SkipPreflight", "skipPreflight", "NoPreflight", "noPreflight",
		"DisablePreflight", "disablePreflight", "PreflightOptional",
	} {
		for path, body := range sources {
			if strings.Contains(body, banned) {
				t.Errorf("bypass identifier %q found in %s; preflight must not be skippable", banned, path)
			}
		}
	}
}

// TestHarnessDaemonAdapterParity (D4) pins the daemon story exactly as it is:
//
//   - NO in-tree daemon binary has its own harness-plan path today, so there is
//     no private adapter that could skip preflight (proven by source scan, not
//     asserted);
//   - the ONE gesture a daemon would use — construct a client from a plan, or
//     call the exported PreflightHarnessPlan first — yields the IDENTICAL
//     refusal the interactive and print paths produce, for the same plan.
func TestHarnessDaemonAdapterParity(t *testing.T) {
	// (a) no daemon binary reaches WithHarnessPlan behind the client's back.
	for _, dir := range []string{
		filepath.Join("..", "cmd", "swarm-agent-daemon"),
		filepath.Join("..", "cmd", "headless"),
		filepath.Join("..", "cmd", "swarm-gateway"),
	} {
		if _, err := os.Stat(dir); err != nil {
			t.Fatalf("expected daemon source dir %s: %v", dir, err)
		}
		for path, body := range harness9dPackageSources(t, dir) {
			if strings.Contains(body, "WithHarnessPlan") {
				t.Errorf("%s/%s builds a harness client directly; it must be routed through preflight", dir, path)
			}
		}
	}

	// (b) parity of outcome. The unattended gesture and the interactive gesture
	//     refuse the same plan with the same sentinel and the same unmet set.
	plan := compilePlan(t, harness9dOneSchedule)

	daemonErr := PreflightHarnessPlan(plan, HarnessBindings{}) // daemon: check first, then dispatch
	interactiveClient, interactiveErr := New(WithHarnessPlan(plan))
	if interactiveClient != nil {
		_ = interactiveClient.Close()
	}
	if daemonErr == nil || interactiveErr == nil {
		t.Fatalf("both paths must refuse; daemon=%v interactive=%v", daemonErr, interactiveErr)
	}
	if !errors.Is(daemonErr, ErrHarnessBindingUnsatisfied) || !errors.Is(interactiveErr, ErrHarnessBindingUnsatisfied) {
		t.Fatalf("both paths must wrap the same sentinel; daemon=%v interactive=%v", daemonErr, interactiveErr)
	}
	if daemonErr.Error() != interactiveErr.Error() {
		t.Errorf("daemon and interactive refusals differ:\n--daemon--\n%s\n--interactive--\n%s",
			daemonErr, interactiveErr)
	}

	// (c) and with the bindings supplied, the unattended gesture passes for the
	//     SAME plan — clause (a) of the exit gate, both directions.
	if err := PreflightHarnessPlan(plan, harness9dAllBindings()); err != nil {
		t.Fatalf("a fully supplied unattended host must pass preflight: %v", err)
	}
}

// TestHarnessPreflightGatesTheHotReapplyPath (D4): ApplyHarnessPlan is the third
// execution path (TUI harness editor, WatchHarness, any programmatic re-apply).
// A plan whose requirements are unmet must be refused there too, and the PRIOR
// plan must remain authoritative — a refusal never leaves a half-applied client.
func TestHarnessPreflightGatesTheHotReapplyPath(t *testing.T) {
	dir := t.TempDir()
	basePlan := harness9dCompileIn(t, dir, harness9dBase)
	schedPlan := harness9dCompileIn(t, dir, harness9dOneSchedule)

	c, err := New(WithHarnessPlan(basePlan))
	if err != nil {
		t.Fatalf("build baseline: %v", err)
	}
	t.Cleanup(func() { _ = c.Close() })

	res, err := c.ApplyHarnessPlan(context.Background(), schedPlan, ApplyHarnessOptions{})
	if err == nil {
		t.Fatal("hot re-apply of a plan with unmet requirements succeeded; it must be refused")
	}
	if !errors.Is(err, ErrHarnessBindingUnsatisfied) {
		t.Fatalf("hot apply refusal does not wrap the preflight sentinel: %v", err)
	}
	if len(res.Applied) != 0 {
		t.Errorf("a refused apply reported Applied=%v", res.Applied)
	}
	if c.opts.harness.plan.Digest() != basePlan.Digest() {
		t.Errorf("the prior plan is no longer authoritative after a refused apply")
	}
	// The live client remains driven by the PRIOR plan. Note the agent POINTER
	// is not preserved: preflight rejects before initHarnessAgent's first
	// mutation, but ApplyHarnessPlan's pre-existing rollback
	// (harness_apply.go:212-217) then re-runs initHarnessAgent with the old
	// options, which always rebuilds the agent wholesale. So the guarantee this
	// slice can honestly assert is EQUIVALENCE to the prior plan, not identity
	// of the instance. (Making the rollback conditional would require editing
	// harness_apply.go, which is outside this slice's file scope.)
	if c.agent == nil {
		t.Fatal("a refused apply left the client with no agent")
	}
	if c.agentDef.Provider != basePlan.ProviderID() || c.agentDef.Model != basePlan.Model() {
		t.Errorf("after a refused apply the agent no longer matches the prior plan: %+v", c.agentDef)
	}
	if !strings.Contains(c.agent.SystemPrompt(), "PROMPT-BODY-SENTINEL-9D") {
		t.Errorf("after a refused apply the agent no longer carries the prior plan's prompt")
	}

	// Same client, same proposed plan, but a host that HAS the bindings: the
	// re-apply is allowed. Bindings are a property of the process, so they are
	// read from the construction-time registry, not re-declared per apply.
	supplied, err := New(WithHarnessPlan(basePlan), WithHarnessBindings(harness9dAllBindings()))
	if err != nil {
		t.Fatalf("build supplied baseline: %v", err)
	}
	t.Cleanup(func() { _ = supplied.Close() })
	if _, err := supplied.ApplyHarnessPlan(context.Background(), schedPlan, ApplyHarnessOptions{}); err != nil {
		t.Fatalf("hot re-apply with every binding supplied must succeed: %v", err)
	}
	if supplied.opts.harness.plan.Digest() != schedPlan.Digest() {
		t.Errorf("a satisfied apply did not adopt the new plan")
	}
}

// ─── D5: restart recovery and §7.5 failure injection ───────────────────────

// TestHarnessScheduleSurvivesRestartWithoutDuplicateOrLostOccurrence encodes
// Phase 9b's reasoning literally:
//
//   - the SCHEDULE survives a restart because THE MANIFEST IS THE STATE — the
//     same bytes recompile to the same digest, same schedules, same
//     requirements. Nothing needs to be persisted for that;
//   - what actually needs durability is per-OCCURRENCE history, and the property
//     that matters is that a restart produces neither a DUPLICATE fire nor a
//     LOST one. That is what the durable-store binding is for, and what this
//     test exercises across two store instances over one path.
func TestHarnessScheduleSurvivesRestartWithoutDuplicateOrLostOccurrence(t *testing.T) {
	dir := t.TempDir()

	// (a) manifest-as-state: "restarting" is recompiling the same bytes.
	first := harness9dCompileIn(t, dir, harness9dOneSchedule)
	second := harness9dCompileIn(t, dir, harness9dOneSchedule)
	if first.Digest() != second.Digest() {
		t.Fatalf("the same manifest recompiled to a different digest across a restart: %q vs %q",
			first.Digest(), second.Digest())
	}
	if len(first.Schedules()) != 1 || len(second.Schedules()) != 1 {
		t.Fatalf("schedule count changed across restart: %d vs %d", len(first.Schedules()), len(second.Schedules()))
	}
	if first.Schedules()[0].ID != second.Schedules()[0].ID {
		t.Errorf("schedule identity changed across restart")
	}
	if len(first.RequiredRuntimeBindings()) != len(second.RequiredRuntimeBindings()) {
		t.Errorf("requirements changed across restart")
	}

	// (b) occurrence history: no duplicate, no loss.
	ctx := context.Background()
	due := time.Date(2026, 7, 25, 3, 0, 0, 0, time.UTC)

	// shouldFire is the decision a misfire detector makes from durable state.
	shouldFire := func(store HarnessSchedulerDurableStore, at time.Time) bool {
		t.Helper()
		last, found, err := store.LastOccurrence(ctx, "nightly")
		if err != nil {
			t.Fatalf("LastOccurrence: %v", err)
		}
		return !found || at.After(last.DueAt)
	}

	preRestart := NewFileHarnessOccurrenceStore(dir)
	if !shouldFire(preRestart, due) {
		t.Fatal("first occurrence was suppressed: LOST")
	}
	if err := preRestart.RecordOccurrence(ctx, HarnessOccurrenceRecord{
		ScheduleID: "nightly", OccurrenceID: "occ-1", DueAt: due, FiredAt: due, Outcome: "succeeded",
	}); err != nil {
		t.Fatalf("RecordOccurrence: %v", err)
	}

	// ---- process restart: a brand new store instance over the same path ----
	postRestart := NewFileHarnessOccurrenceStore(dir)
	if shouldFire(postRestart, due) {
		t.Error("the already-fired occurrence would fire again after a restart: DUPLICATE")
	}
	next := due.Add(24 * time.Hour)
	if !shouldFire(postRestart, next) {
		t.Error("the next occurrence would be suppressed after a restart: LOST")
	}
}

// TestHarnessSchedulePersistenceFailureFailsClosed (§7.5): when the durable
// store cannot be read, the decision procedure above must ERROR rather than
// silently choosing. A corrupt store that read as "no history" would re-fire
// every past occurrence; one that read as "already fired" would drop them all.
func TestHarnessSchedulePersistenceFailureFailsClosed(t *testing.T) {
	dir := t.TempDir()
	ctx := context.Background()
	store := NewFileHarnessOccurrenceStore(dir)
	if err := store.RecordOccurrence(ctx, HarnessOccurrenceRecord{
		ScheduleID: "nightly", OccurrenceID: "occ-1",
		DueAt: time.Date(2026, 7, 25, 3, 0, 0, 0, time.UTC),
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := os.WriteFile(store.Path(), []byte("[[[truncated"), 0o600); err != nil {
		t.Fatalf("corrupt: %v", err)
	}
	if _, _, err := NewFileHarnessOccurrenceStore(dir).LastOccurrence(ctx, "nightly"); err == nil {
		t.Fatal("a corrupt store must surface an error so the caller can refuse, not continue")
	}
}

// TestHarnessPreflightClientCloseDuringApply (§7.5): a Close racing a refused
// re-apply must not panic, must not deadlock, and must not leave the client
// claiming a plan it never adopted. Run under -race this also covers the
// lock discipline of the new read in initHarnessAgent.
func TestHarnessPreflightClientCloseDuringApply(t *testing.T) {
	dir := t.TempDir()
	basePlan := harness9dCompileIn(t, dir, harness9dBase)
	schedPlan := harness9dCompileIn(t, dir, harness9dOneSchedule)

	before := runtime.NumGoroutine()
	c, err := New(WithHarnessPlan(basePlan))
	if err != nil {
		t.Fatalf("build: %v", err)
	}

	var wg sync.WaitGroup
	wg.Add(2)
	var applyErr error
	go func() {
		defer wg.Done()
		_, applyErr = c.ApplyHarnessPlan(context.Background(), schedPlan, ApplyHarnessOptions{})
	}()
	go func() {
		defer wg.Done()
		_ = c.Close()
	}()

	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(30 * time.Second):
		t.Fatal("Close racing a refused apply deadlocked")
	}

	if applyErr == nil {
		t.Fatal("the apply must be refused regardless of the concurrent Close")
	}
	if !errors.Is(applyErr, ErrHarnessBindingUnsatisfied) {
		t.Fatalf("unexpected apply error: %v", applyErr)
	}
	if c.opts.harness.plan.Digest() != basePlan.Digest() {
		t.Error("a refused apply adopted the new plan despite the refusal")
	}
	harness9dAssertNoGoroutineLeak(t, before)
}

// TestHarnessPreflightRefusalStartsNothing (§7.5 leak check): repeated refusals
// on BOTH gated paths must not accumulate goroutines, agents, or MCP managers —
// a refusal is a pure comparison, so it costs nothing but the error.
func TestHarnessPreflightRefusalStartsNothing(t *testing.T) {
	dir := t.TempDir()
	basePlan := harness9dCompileIn(t, dir, harness9dBase)
	schedPlan := harness9dCompileIn(t, dir, harness9dOneSchedule)

	live, err := New(WithHarnessPlan(basePlan))
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	t.Cleanup(func() { _ = live.Close() })

	before := runtime.NumGoroutine()
	for i := 0; i < 25; i++ {
		if c, err := New(WithHarnessPlan(schedPlan)); err == nil {
			_ = c.Close()
			t.Fatal("construction succeeded on a refused plan")
		}
		if _, err := live.ApplyHarnessPlan(context.Background(), schedPlan, ApplyHarnessOptions{}); err == nil {
			t.Fatal("apply succeeded on a refused plan")
		}
	}
	harness9dAssertNoGoroutineLeak(t, before)

	// The live client is still fully functional after 25 refusals.
	if live.agent == nil || live.opts.harness.plan.Digest() != basePlan.Digest() {
		t.Error("repeated refusals damaged the live client")
	}
}

// ─── helpers ───────────────────────────────────────────────────────────────

// harness9dPackageSources reads every non-test .go file in dir, keyed by base
// name. It is used by the D4 no-bypass proof, which must inspect SOURCE rather
// than trust a comment.
func harness9dPackageSources(t *testing.T, dir string) map[string]string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir(%s): %v", dir, err)
	}
	out := make(map[string]string, len(entries))
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		body, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatalf("ReadFile(%s): %v", name, err)
		}
		out[name] = string(body)
	}
	if len(out) == 0 {
		t.Fatalf("no source files found in %s", dir)
	}
	return out
}
