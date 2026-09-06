package client

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/Swarm-Code/mono/swarm-sdk/harness"
)

// harnessAgentsBaseManifest is a minimal, valid manifest with NO profiles/
// fallback/agents sections declared — the zero-selection baseline every test
// below splices additional sections onto. It intentionally mirrors
// minimalManifest (harness_plan_test.go) so a full mustBuild() stays
// comparable across both test files.
const harnessAgentsBaseManifest = `apiVersion: swarm.ai/v1alpha1
kind: Harness
metadata:
  name: agentsphase8b
provider:
  id: anthropic
  model: claude-x
  credential:
    inline: main-test-key
agent:
  systemPrompt:
    inline: "MAIN-PROMPT"
  tools:
    - forge.read
permissions:
  approvalMode: interactive
`

// harnessTwoProfilesBlock declares two sealed, env-referenced-credential
// profiles reusable across several tests below. Profile credentials must be
// an indirect REFERENCE (harness/agents.go resolveProfiles rejects an inline
// literal outright), so every test that splices this block in must first
// t.Setenv the two referenced names.
const (
	harnessTestPrimaryKeyEnv   = "HARNESS_TEST_PRIMARY_PROFILE_KEY"
	harnessTestSecondaryKeyEnv = "HARNESS_TEST_SECONDARY_PROFILE_KEY"
)

const harnessTwoProfilesBlock = `profiles:
  - id: primary
    provider: anthropic
    model: claude-primary
    credential:
      env: HARNESS_TEST_PRIMARY_PROFILE_KEY
  - id: secondary
    provider: openai
    model: gpt-secondary
    credential:
      env: HARNESS_TEST_SECONDARY_PROFILE_KEY
`

// ─── zero-selection: byte-for-byte unchanged (HARD INVARIANT) ─────────────

func TestHarnessAgentsZeroSelectionNoOp(t *testing.T) {
	plan := compilePlan(t, harnessAgentsBaseManifest)

	profileSpecs, profileCfgs, err := buildHarnessProfiles(plan)
	if err != nil || profileSpecs != nil || profileCfgs != nil {
		t.Fatalf("zero profiles: want nil,nil,nil got %v,%v,%v", profileSpecs, profileCfgs, err)
	}

	chain, noFallback, err := harnessFallbackDecision(plan, profileSpecs, profileCfgs)
	if err != nil || chain != nil || !noFallback {
		t.Fatalf("zero fallback: want nil,true,nil got %v,%v,%v", chain, noFallback, err)
	}

	binding, err := buildHarnessSubagentBinding(plan, profileSpecs, profileCfgs, "anthropic", "claude-x")
	if err != nil || binding != nil {
		t.Fatalf("zero agents: want nil,nil got %v,%v", binding, err)
	}

	// Full construction stays byte-for-byte unchanged too: no new manager, no
	// new goroutine, no new file read — the plan simply never reaches any of
	// the three new builders' non-trivial branches.
	c := mustBuild(t, harnessAgentsBaseManifest)
	if c.agentDef.Provider != "anthropic" || c.agentDef.Model != "claude-x" {
		t.Fatalf("unexpected agent def: %+v", c.agentDef)
	}
}

// ─── profiles -> provider.Config ───────────────────────────────────────────

func TestHarnessProfilesResolveProviderConfig(t *testing.T) {
	t.Setenv("HARNESS_TEST_PROFILE_KEY", "super-secret-profile-value")
	manifest := harnessAgentsBaseManifest + `profiles:
  - id: fast
    provider: anthropic
    model: claude-haiku
    credential:
      env: HARNESS_TEST_PROFILE_KEY
`
	plan := compilePlan(t, manifest)
	specs, cfgs, err := buildHarnessProfiles(plan)
	if err != nil {
		t.Fatalf("buildHarnessProfiles: %v", err)
	}
	if _, ok := specs["fast"]; !ok {
		t.Fatalf("missing profile spec for %q", "fast")
	}
	cfg, ok := cfgs["fast"]
	if !ok {
		t.Fatalf("missing resolved provider config for %q", "fast")
	}
	if cfg.Name != "anthropic" || cfg.Model != "claude-haiku" {
		t.Fatalf("unexpected config: %+v", cfg)
	}
	if cfg.APIKey != "super-secret-profile-value" {
		t.Fatalf("credential not resolved into the provider config")
	}
	if !cfg.NoAmbientEnv {
		t.Fatalf("NoAmbientEnv must stay true (closed posture)")
	}
}

// TestHarnessProfileCredentialNeverLeaksInErrors: a profile referencing an
// UNSEALED provider (gemini is deliberately excluded from
// harnessSealedProviders — see harness_plan.go) is rejected fail-closed, and
// the resolved credential value never appears in the returned error.
func TestHarnessProfileCredentialNeverLeaksInErrors(t *testing.T) {
	t.Setenv("HARNESS_TEST_PROFILE_KEY2", "another-super-secret-value")
	manifest := harnessAgentsBaseManifest + `profiles:
  - id: bad
    provider: gemini
    model: gemini-x
    credential:
      env: HARNESS_TEST_PROFILE_KEY2
`
	plan := compilePlan(t, manifest)
	_, _, err := buildHarnessProfiles(plan)
	if err == nil {
		t.Fatalf("expected the gemini profile to be rejected (unsealed provider)")
	}
	if strings.Contains(err.Error(), "another-super-secret-value") {
		t.Fatalf("credential value leaked into the error: %v", err)
	}
}

// TestHarnessProfileCredentialNeverLeaksInLabel: the SAME redaction
// discipline applies to the apply/reload diff label — harnessProfilesLabel
// must never render the raw credential value, only a source+hash token.
func TestHarnessProfileCredentialNeverLeaksInLabel(t *testing.T) {
	t.Setenv("HARNESS_TEST_LABEL_KEY", "super-secret-env-value")
	manifest := harnessAgentsBaseManifest + `profiles:
  - id: fast
    provider: anthropic
    model: claude-haiku
    credential:
      env: HARNESS_TEST_LABEL_KEY
`
	plan := compilePlan(t, manifest)
	label := harnessProfilesLabel(plan)
	if strings.Contains(label, "super-secret-env-value") {
		t.Fatalf("credential value leaked into the diff label: %q", label)
	}
}

// ─── fallback -> *fallback.Chain (D3: three states) ────────────────────────

func TestHarnessFallbackDecisionThreeStates(t *testing.T) {
	t.Setenv(harnessTestPrimaryKeyEnv, "primary-secret-value")
	t.Setenv(harnessTestSecondaryKeyEnv, "secondary-secret-value")

	// 1. ABSENT: no `fallback:` section at all.
	planAbsent := compilePlan(t, harnessAgentsBaseManifest)
	chain, noFallback, err := harnessFallbackDecision(planAbsent, nil, nil)
	if err != nil || chain != nil || !noFallback {
		t.Fatalf("absent fallback: want nil,true,nil got %v,%v,%v", chain, noFallback, err)
	}

	// 2. DECLARED BUT DISABLED: must behave identically to absent.
	planDisabled := compilePlan(t, harnessAgentsBaseManifest+harnessTwoProfilesBlock+`fallback:
  enabled: false
  chain: [primary, secondary]
  retryOn: [timeout]
`)
	specs, cfgs, err := buildHarnessProfiles(planDisabled)
	if err != nil {
		t.Fatalf("buildHarnessProfiles(disabled): %v", err)
	}
	chain, noFallback, err = harnessFallbackDecision(planDisabled, specs, cfgs)
	if err != nil || chain != nil || !noFallback {
		t.Fatalf("disabled fallback: want nil,true,nil got %v,%v,%v", chain, noFallback, err)
	}

	// 3. DECLARED AND ENABLED: chain built in declared order, noFallback false.
	planEnabled := compilePlan(t, harnessAgentsBaseManifest+harnessTwoProfilesBlock+`fallback:
  enabled: true
  chain: [primary, secondary]
  retryOn: [timeout]
`)
	specs, cfgs, err = buildHarnessProfiles(planEnabled)
	if err != nil {
		t.Fatalf("buildHarnessProfiles(enabled): %v", err)
	}
	chain, noFallback, err = harnessFallbackDecision(planEnabled, specs, cfgs)
	if err != nil {
		t.Fatalf("enabled fallback: unexpected error: %v", err)
	}
	if noFallback {
		t.Fatalf("enabled fallback: noFallback must be false")
	}
	if chain == nil {
		t.Fatalf("enabled fallback: expected a non-nil chain")
	}
	if chain.Primary.Provider != "anthropic" || chain.Primary.Model != "claude-primary" {
		t.Fatalf("unexpected primary: %+v", chain.Primary)
	}
	if len(chain.Fallbacks) != 1 || chain.Fallbacks[0].Provider != "openai" || chain.Fallbacks[0].Model != "gpt-secondary" {
		t.Fatalf("unexpected fallbacks: %+v", chain.Fallbacks)
	}
}

// TestHarnessFallbackEnabledClientConstructs: an explicitly enabled fallback
// wires through the FULL closed construction path (initHarnessAgent) without
// error — the live-wiring half of D3, not just the pure decision function.
func TestHarnessFallbackEnabledClientConstructs(t *testing.T) {
	t.Setenv("HARNESS_TEST_FALLBACK_OK_KEY", "primary-secret-value")
	manifest := harnessAgentsBaseManifest + `profiles:
  - id: primary
    provider: anthropic
    model: claude-primary
    credential:
      env: HARNESS_TEST_FALLBACK_OK_KEY
fallback:
  enabled: true
  chain: [primary]
  retryOn: [timeout]
`
	c := mustBuild(t, manifest)
	if c.agent == nil {
		t.Fatalf("expected the agent to be constructed")
	}
}

// TestHarnessFallbackEnabledSealsEveryChainAttempt is the B1 (Phase 10b)
// end-to-end wiring proof: initHarnessAgent's fallback wiring (harness_plan.
// go) must install a sealed provider.Config, via Agent.SetChainProviderConfigs,
// for EVERY declared chain entry — not just the primary — so
// agent_execute_chain.go's execFn never falls back to an unsealed attempt.
// This does not re-prove the ambient-credential-unreachable property itself
// (that is internal/agent's TestExecuteWithChainSealedFallbackNeverReachesAmbientCredential,
// scoped to the file B1 actually changed); it proves the CLIENT wires real
// sealed values through to the agent for the exact chain it just built,
// using only a presence check (Agent.HasChainProviderConfig) that can never
// leak the credential value itself.
func TestHarnessFallbackEnabledSealsEveryChainAttempt(t *testing.T) {
	t.Setenv(harnessTestPrimaryKeyEnv, "primary-secret-value")
	t.Setenv(harnessTestSecondaryKeyEnv, "secondary-secret-value")

	manifest := harnessAgentsBaseManifest + harnessTwoProfilesBlock + `fallback:
  enabled: true
  chain: [primary, secondary]
  retryOn: [timeout]
`
	c := mustBuild(t, manifest)
	if c.agent == nil {
		t.Fatalf("expected the agent to be constructed")
	}
	if !c.agent.HasChainProviderConfig("anthropic", "claude-primary") {
		t.Error("primary chain entry (anthropic/claude-primary) has no sealed provider config installed")
	}
	if !c.agent.HasChainProviderConfig("openai", "gpt-secondary") {
		t.Error("fallback chain entry (openai/gpt-secondary) has no sealed provider config installed")
	}
	// An entry that is not part of THIS chain must not spuriously report a
	// sealed config (proves the key derivation is exact, not a wildcard).
	if c.agent.HasChainProviderConfig("anthropic", "some-other-model") {
		t.Error("an unrelated provider/model reported a sealed config; key derivation must be exact")
	}
}

// TestHarnessFallbackUnsealedProfileFailsClientConstruction: an enabled
// fallback chain referencing an unsealed provider's profile must fail closed
// at CONSTRUCTION time, not degrade silently.
func TestHarnessFallbackUnsealedProfileFailsClientConstruction(t *testing.T) {
	t.Setenv("HARNESS_TEST_FALLBACK_BAD_KEY", "whatever-secret-value")
	manifest := harnessAgentsBaseManifest + `profiles:
  - id: geminiprofile
    provider: gemini
    model: gemini-x
    credential:
      env: HARNESS_TEST_FALLBACK_BAD_KEY
fallback:
  enabled: true
  chain: [geminiprofile]
  retryOn: [timeout]
`
	if _, err := buildHarnessClient(t, manifest); err == nil {
		t.Fatalf("expected construction to fail closed on an unsealed fallback profile")
	}
}

// ─── subagents -> agent.Definition roster ──────────────────────────────────

func TestHarnessSubagentsInjectedRoster(t *testing.T) {
	t.Setenv("HARNESS_TEST_ROSTER_KEY", "fast-secret-value")
	manifest := harnessAgentsBaseManifest + `profiles:
  - id: fast
    provider: anthropic
    model: claude-haiku
    credential:
      env: HARNESS_TEST_ROSTER_KEY
agents:
  - id: researcher
    profile: fast
    systemPrompt:
      inline: "RESEARCH-PROMPT"
    tools:
      - forge.read
    limits:
      maxTurns: 4
      timeoutSeconds: 30
  - id: writer
    systemPrompt:
      inline: "WRITER-PROMPT"
`
	plan := compilePlan(t, manifest)
	specs, cfgs, err := buildHarnessProfiles(plan)
	if err != nil {
		t.Fatalf("buildHarnessProfiles: %v", err)
	}
	binding, err := buildHarnessSubagentBinding(plan, specs, cfgs, "anthropic", "claude-x")
	if err != nil {
		t.Fatalf("buildHarnessSubagentBinding: %v", err)
	}
	if binding == nil {
		t.Fatalf("expected a non-nil binding")
	}
	defs := binding.Definitions()
	if len(defs) != 2 {
		t.Fatalf("want 2 definitions, got %d: %+v", len(defs), defs)
	}

	researcher, ok := defs["researcher"]
	if !ok {
		t.Fatalf("missing researcher definition")
	}
	if researcher.Provider != "anthropic" || researcher.Model != "claude-haiku" {
		t.Fatalf("researcher provider/model resolved from its profile is wrong: %+v", researcher)
	}
	if !strings.Contains(researcher.SystemPrompt, "RESEARCH-PROMPT") {
		t.Fatalf("researcher prompt missing: %q", researcher.SystemPrompt)
	}
	if len(researcher.ToolHints) != 1 || researcher.ToolHints[0] != "Read" {
		t.Fatalf("researcher tool hints wrong: %v", researcher.ToolHints)
	}
	if researcher.Capabilities == nil || researcher.Capabilities.MaxTurns != 4 {
		t.Fatalf("researcher limits wrong: %+v", researcher.Capabilities)
	}

	writer, ok := defs["writer"]
	if !ok {
		t.Fatalf("missing writer definition")
	}
	if writer.Provider != "anthropic" || writer.Model != "claude-x" {
		t.Fatalf("writer (no profile override) must inherit the document provider/model, got %+v", writer)
	}
	if len(writer.ToolHints) != 1 || writer.ToolHints[0] != harnessNoToolsSentinel {
		t.Fatalf("writer (no tools declared) must default to the zero-tools sentinel, got %v", writer.ToolHints)
	}
}

// TestHarnessSubagentsD4Isolation proves plan-declared subagents are the
// ONLY subagents ever reachable from buildHarnessSubagentBinding: a
// plausible ambient definition sitting exactly where the STANDARD (non-
// harness) client's SubagentTool would discover one (~/.swarmos) has zero
// effect on the result, because the function takes no ambient input at all.
func TestHarnessSubagentsD4Isolation(t *testing.T) {
	// Zero agents declared -> nil binding, never a default/ambient roster.
	plan := compilePlan(t, harnessAgentsBaseManifest)
	binding, err := buildHarnessSubagentBinding(plan, nil, nil, "anthropic", "claude-x")
	if err != nil || binding != nil {
		t.Fatalf("zero agents: want nil,nil got %v,%v", binding, err)
	}

	fakeHome := t.TempDir()
	t.Setenv("HOME", fakeHome)
	ambientDir := filepath.Join(fakeHome, ".swarmos", "agents")
	if err := os.MkdirAll(ambientDir, 0o755); err != nil {
		t.Fatalf("mkdir ambient: %v", err)
	}
	if err := os.WriteFile(filepath.Join(ambientDir, "ambient-agent.json"),
		[]byte(`{"id":"ambient-agent","name":"Ambient","systemPrompt":"I should never appear"}`), 0o644); err != nil {
		t.Fatalf("write ambient definition: %v", err)
	}

	manifest := harnessAgentsBaseManifest + "agents:\n  - id: researcher\n    systemPrompt:\n      inline: \"RESEARCH-PROMPT\"\n"
	plan2 := compilePlan(t, manifest)
	binding2, err := buildHarnessSubagentBinding(plan2, nil, nil, "anthropic", "claude-x")
	if err != nil {
		t.Fatalf("buildHarnessSubagentBinding: %v", err)
	}
	defs := binding2.Definitions()
	if len(defs) != 1 {
		t.Fatalf("want exactly 1 declared subagent, got %d: %+v", len(defs), defs)
	}
	if _, ok := defs["ambient-agent"]; ok {
		t.Fatalf("ambient agent definition leaked into the closed roster")
	}
	if _, ok := defs["researcher"]; !ok {
		t.Fatalf("declared subagent missing from the roster")
	}
}

// ─── D2: delegation allowlist, enforced at the binding layer ──────────────

func TestHarnessSubagentsDelegationAllowlist(t *testing.T) {
	manifest := harnessAgentsBaseManifest + "agents:\n  - id: a\n    delegates: [b]\n  - id: b\n  - id: c\n"
	plan := compilePlan(t, manifest)
	binding, err := buildHarnessSubagentBinding(plan, nil, nil, "anthropic", "claude-x")
	if err != nil {
		t.Fatalf("buildHarnessSubagentBinding: %v", err)
	}

	if !binding.DelegationAllowed("a", "b") {
		t.Fatalf("a -> b should be allowed (declared delegate)")
	}
	if binding.DelegationAllowed("a", "c") {
		t.Fatalf("a -> c must be refused (c is not in a's declared delegates)")
	}
	if binding.DelegationAllowed("b", "a") {
		t.Fatalf("b -> a must be refused (b declares no delegates at all)")
	}
	if binding.DelegationAllowed("c", "a") {
		t.Fatalf("c -> a must be refused (c declares no delegates at all)")
	}
	var nilBinding *harnessSubagentBinding
	if nilBinding.DelegationAllowed("a", "b") {
		t.Fatalf("a nil binding must refuse every delegation")
	}
}

// ─── D1: unbindable fields fail closed, by name ────────────────────────────

func TestHarnessSubagentsD1RejectsSkills(t *testing.T) {
	manifest := harnessAgentsBaseManifest + "agents:\n  - id: researcher\n    skills:\n      - id: reviewing\n"
	plan := compilePlan(t, manifest)
	_, err := buildHarnessSubagentBinding(plan, nil, nil, "anthropic", "claude-x")
	if err == nil {
		t.Fatalf("expected rejection for a per-subagent skills declaration")
	}
	if !strings.Contains(err.Error(), "agents[researcher].skills") {
		t.Fatalf("error does not name the agent+field: %v", err)
	}
}

func TestHarnessSubagentsD1RejectsHooks(t *testing.T) {
	manifest := harnessAgentsBaseManifest + "agents:\n  - id: researcher\n    hooks:\n      - id: pre\n        event: tool.pre\n        scope: global\n        type: command\n        command: echo hi\n"
	plan := compilePlan(t, manifest)
	_, err := buildHarnessSubagentBinding(plan, nil, nil, "anthropic", "claude-x")
	if err == nil {
		t.Fatalf("expected rejection for a per-subagent hooks declaration")
	}
	if !strings.Contains(err.Error(), "agents[researcher].hooks") {
		t.Fatalf("error does not name the agent+field: %v", err)
	}
}

// TestHarnessSubagentsFullClientConstructionRejectsD1: the SAME D1 rejection
// aborts FULL client construction atomically (initHarnessAgent wiring), not
// just the standalone builder call.
func TestHarnessSubagentsFullClientConstructionRejectsD1(t *testing.T) {
	manifest := harnessAgentsBaseManifest + "agents:\n  - id: researcher\n    hooks:\n      - id: pre\n        event: tool.pre\n        scope: global\n        type: command\n        command: echo hi\n"
	_, err := buildHarnessClient(t, manifest)
	if err == nil {
		t.Fatalf("expected full client construction to fail closed on a D1-rejected field")
	}
	if !strings.Contains(err.Error(), "agents[researcher].hooks") {
		t.Fatalf("error does not name the agent+field: %v", err)
	}
}

// ─── apply/reload classification (Phase 7d pattern) ────────────────────────

func TestClassifyHarnessPlanChangeProfilesIsRestart(t *testing.T) {
	t.Setenv("HARNESS_TEST_CLASSIFY_PROFILES_KEY", "fast-secret-value")
	oldPlan := compilePlan(t, harnessAgentsBaseManifest)
	newPlan := compilePlan(t, harnessAgentsBaseManifest+`profiles:
  - id: fast
    provider: anthropic
    model: claude-haiku
    credential:
      env: HARNESS_TEST_CLASSIFY_PROFILES_KEY
`)
	_, hot, restart, forbidden, err := classifyHarnessPlanChange(oldPlan, newPlan, false)
	if err != nil {
		t.Fatalf("classify: %v", err)
	}
	if len(forbidden) != 0 {
		t.Fatalf("unexpected forbidden set: %v", forbidden)
	}
	if !slices.Contains(restart, harnessFieldProfiles) {
		t.Fatalf("expected %q in restart set, got %v", harnessFieldProfiles, restart)
	}
	if slices.Contains(hot, harnessFieldProfiles) {
		t.Fatalf("profiles must never be classified hot")
	}
}

func TestClassifyHarnessPlanChangeFallbackIsHot(t *testing.T) {
	t.Setenv("HARNESS_TEST_CLASSIFY_FALLBACK_KEY", "fast-secret-value")
	base := harnessAgentsBaseManifest + `profiles:
  - id: fast
    provider: anthropic
    model: claude-haiku
    credential:
      env: HARNESS_TEST_CLASSIFY_FALLBACK_KEY
`
	oldPlan := compilePlan(t, base)
	newPlan := compilePlan(t, base+`fallback:
  enabled: true
  chain: [fast]
  retryOn: [timeout]
`)
	_, hot, restart, forbidden, err := classifyHarnessPlanChange(oldPlan, newPlan, false)
	if err != nil {
		t.Fatalf("classify: %v", err)
	}
	if len(forbidden) != 0 {
		t.Fatalf("unexpected forbidden set: %v", forbidden)
	}
	if !slices.Contains(hot, harnessFieldFallback) {
		t.Fatalf("expected %q in hot set, got %v", harnessFieldFallback, hot)
	}
	if slices.Contains(restart, harnessFieldFallback) {
		t.Fatalf("fallback must never be classified restart")
	}
}

func TestClassifyHarnessPlanChangeAgentsIsHot(t *testing.T) {
	oldPlan := compilePlan(t, harnessAgentsBaseManifest)
	newPlan := compilePlan(t, harnessAgentsBaseManifest+"agents:\n  - id: researcher\n    systemPrompt:\n      inline: hi\n")
	_, hot, restart, forbidden, err := classifyHarnessPlanChange(oldPlan, newPlan, false)
	if err != nil {
		t.Fatalf("classify: %v", err)
	}
	if len(forbidden) != 0 {
		t.Fatalf("unexpected forbidden set: %v", forbidden)
	}
	if !slices.Contains(hot, harnessFieldAgents) {
		t.Fatalf("expected %q in hot set, got %v", harnessFieldAgents, hot)
	}
	if slices.Contains(restart, harnessFieldAgents) {
		t.Fatalf("agents must never be classified restart")
	}
}

// TestClassifyHarnessPlanChangeZeroSectionsNoDiff: two plans that agree on
// having NO profiles/fallback/agents sections must never report a spurious
// diff for any of the three new fields, even when an unrelated field (model)
// differs.
func TestClassifyHarnessPlanChangeZeroSectionsNoDiff(t *testing.T) {
	oldPlan := compilePlan(t, harnessAgentsBaseManifest)
	newManifest := strings.Replace(harnessAgentsBaseManifest, "model: claude-x", "model: claude-y", 1)
	newPlan := compilePlan(t, newManifest)

	diff, hot, restart, forbidden, err := classifyHarnessPlanChange(oldPlan, newPlan, false)
	if err != nil {
		t.Fatalf("classify: %v", err)
	}
	if len(forbidden) != 0 {
		t.Fatalf("unexpected forbidden set: %v", forbidden)
	}
	for _, f := range []string{harnessFieldProfiles, harnessFieldFallback, harnessFieldAgents} {
		if slices.Contains(hot, f) || slices.Contains(restart, f) {
			t.Fatalf("spurious %q diff when neither plan declares the section", f)
		}
	}
	for _, c := range diff.Changes {
		if c.Field == harnessFieldProfiles || c.Field == harnessFieldFallback || c.Field == harnessFieldAgents {
			t.Fatalf("spurious diff entry for %q: %+v", c.Field, c)
		}
	}
}

// sanity: keep the harness import used even if a future edit trims a
// harness.* reference from the tests above (avoids an unused-import churn).
var _ = harness.FallbackRetryTimeout
