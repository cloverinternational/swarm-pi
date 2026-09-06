package harness

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// baseAgentsManifest returns a minimal valid manifest with the given raw block
// (profiles/fallback/agents) spliced in verbatim.
func baseAgentsManifest(block string) string {
	return "apiVersion: swarm.ai/v1alpha1\n" +
		"kind: Harness\n" +
		"metadata:\n  name: agentstest\n" +
		"provider:\n  id: anthropic\n  model: claude-x\n" +
		"agent:\n  systemPrompt:\n    inline: \"hi\"\n  tools: []\n" +
		block
}

// twoProfiles is a reusable, valid profiles block.
const twoProfiles = "profiles:\n" +
	"  - id: fast\n    provider: anthropic\n    model: claude-haiku\n" +
	"  - id: slow\n    provider: anthropic\n    model: claude-opus\n"

func compileOK(t *testing.T, raw string) *Plan {
	t.Helper()
	p, err := CompileBytes([]byte(raw), syntheticYAML)
	if err != nil {
		t.Fatalf("expected compile to succeed: %v", err)
	}
	return p
}

// diagMessage returns the message of the first diagnostic with the given code.
func diagMessage(ds Diagnostics, code string) string {
	for _, d := range ds {
		if d.Code == code {
			return d.Message
		}
	}
	return ""
}

// TestAgentsOmittedAndEmptyValid: omitted profiles/agents and explicitly empty
// sequences are all valid, yield zero specs, and preserve the SAME digest (the
// Phase 8a sections contribute nothing to the redacted surface when unused —
// backward compatibility with a pre-8a compile of the same manifest).
func TestAgentsOmittedAndEmptyValid(t *testing.T) {
	p := compileOK(t, baseAgentsManifest(""))
	if len(p.Profiles()) != 0 || len(p.Subagents()) != 0 {
		t.Errorf("omitted: want zero specs, got profiles=%v agents=%v", p.Profiles(), p.Subagents())
	}
	if _, ok := p.Fallback(); ok {
		t.Errorf("omitted: Fallback() should report ok=false")
	}
	digestOmitted := p.Digest()

	p2 := compileOK(t, baseAgentsManifest("profiles: []\nagents: []\n"))
	if len(p2.Profiles()) != 0 || len(p2.Subagents()) != 0 {
		t.Errorf("empty: want zero specs, got profiles=%v agents=%v", p2.Profiles(), p2.Subagents())
	}
	if p2.Digest() != digestOmitted {
		t.Errorf("empty vs omitted digests differ: %q vs %q", p2.Digest(), digestOmitted)
	}

	js, err := p2.ExplainJSON()
	if err != nil {
		t.Fatalf("ExplainJSON: %v", err)
	}
	for _, key := range []string{"\"profiles\"", "\"fallback\"", "\"agents\""} {
		if strings.Contains(string(js), key) {
			t.Errorf("empty sections must be omitted from ExplainJSON, found %s", key)
		}
	}
}

// TestProfilesIDRules: an empty id and a duplicate id are both rejected.

// TestAgentsDigestBackwardCompatible pins the EXACT digest a pre-8a compiler
// (HEAD before this slice) produced for a manifest that declares none of the
// Phase 8a sections. Adding profiles/fallback/agents must not perturb the
// redacted surface of an existing manifest.
func TestAgentsDigestBackwardCompatible(t *testing.T) {
	const pre8aDigest = "sha256:9c23f5b329f1cd3dd244db4ac69cbf7c9b7f6711f52100a142f0439dd7d3eb1f"
	p := compileOK(t, baseAgentsManifest(""))
	if p.Digest() != pre8aDigest {
		t.Fatalf("digest changed for a manifest without 8a sections:\n got  %s\n want %s", p.Digest(), pre8aDigest)
	}
}

func TestProfilesIDRules(t *testing.T) {
	missing := baseAgentsManifest("profiles:\n  - provider: anthropic\n    model: m\n")
	if ds := compileErr(t, missing, syntheticYAML); !hasCode(ds, "harness.profiles.id.missing") {
		t.Fatalf("expected profiles.id.missing, got: %v", ds)
	}

	dup := baseAgentsManifest("profiles:\n" +
		"  - id: fast\n    provider: anthropic\n    model: a\n" +
		"  - id: fast\n    provider: anthropic\n    model: b\n")
	if ds := compileErr(t, dup, syntheticYAML); !hasCode(ds, "harness.profiles.id.duplicate") {
		t.Fatalf("expected profiles.id.duplicate, got: %v", ds)
	}
}

// TestAgentsIDRules: an empty id and a duplicate id are both rejected.
func TestAgentsIDRules(t *testing.T) {
	missing := baseAgentsManifest("agents:\n  - profile: fast\n")
	if ds := compileErr(t, missing, syntheticYAML); !hasCode(ds, "harness.agents.id.missing") {
		t.Fatalf("expected agents.id.missing, got: %v", ds)
	}

	dup := baseAgentsManifest("agents:\n  - id: rev\n  - id: rev\n")
	if ds := compileErr(t, dup, syntheticYAML); !hasCode(ds, "harness.agents.id.duplicate") {
		t.Fatalf("expected agents.id.duplicate, got: %v", ds)
	}
}

// TestAgentsUnknownFieldRejected: unknown keys under a profile, an agent, and
// the fallback block are all rejected by strict decode.
func TestAgentsUnknownFieldRejected(t *testing.T) {
	cases := []string{
		"profiles:\n  - id: fast\n    provider: anthropic\n    model: m\n    bogus: x\n",
		"agents:\n  - id: rev\n    bogus: x\n",
		"fallback:\n  enabled: false\n  bogus: x\n",
	}
	for _, block := range cases {
		ds := compileErr(t, baseAgentsManifest(block), syntheticYAML)
		if !hasCode(ds, "harness.decode.unknownField") {
			t.Fatalf("expected unknown-field rejection for %q, got: %v", block, ds)
		}
	}
}

// TestProfilesRequireProviderAndModel: both fields are required.
func TestProfilesRequireProviderAndModel(t *testing.T) {
	ds := compileErr(t, baseAgentsManifest("profiles:\n  - id: fast\n"), syntheticYAML)
	if !hasCode(ds, "harness.profiles.provider.missing") || !hasCode(ds, "harness.profiles.model.missing") {
		t.Fatalf("expected provider+model missing diagnostics, got: %v", ds)
	}
}

// TestAgentUnknownProfileReference: agents[].profile must resolve to a declared
// profiles[].id (reference integrity; fails before any execution).
func TestAgentUnknownProfileReference(t *testing.T) {
	ds := compileErr(t, baseAgentsManifest(twoProfiles+"agents:\n  - id: rev\n    profile: nope\n"), syntheticYAML)
	if !hasCode(ds, "harness.agents.profile.unknown") {
		t.Fatalf("expected agents.profile.unknown, got: %v", ds)
	}
	if msg := diagMessage(ds, "harness.agents.profile.unknown"); !strings.Contains(msg, "\"nope\"") {
		t.Errorf("diagnostic should name the offending id, got %q", msg)
	}

	// The same reference resolves cleanly when the profile is declared.
	p := compileOK(t, baseAgentsManifest(twoProfiles+"agents:\n  - id: rev\n    profile: fast\n"))
	if got := p.Subagents(); len(got) != 1 || got[0].Profile != "fast" {
		t.Fatalf("expected resolved profile reference, got %+v", got)
	}
}

// TestFallbackChainReferenceIntegrity: an unknown chain id is rejected at both
// document level and per-agent level.
func TestFallbackChainReferenceIntegrity(t *testing.T) {
	doc := baseAgentsManifest(twoProfiles + "fallback:\n  chain: [fast, ghost]\n")
	ds := compileErr(t, doc, syntheticYAML)
	if !hasCode(ds, "harness.fallback.chain.unknownProfile") {
		t.Fatalf("expected fallback.chain.unknownProfile, got: %v", ds)
	}

	perAgent := baseAgentsManifest(twoProfiles +
		"agents:\n  - id: rev\n    fallback:\n      chain: [ghost]\n")
	ds2 := compileErr(t, perAgent, syntheticYAML)
	if !hasCode(ds2, "harness.agents.fallback.chain.unknownProfile") {
		t.Fatalf("expected agents.fallback.chain.unknownProfile, got: %v", ds2)
	}
}

// TestFallbackChainDuplicateIsCycle: repeating an id inside one chain is a
// degenerate cycle and is rejected with the path reported.
func TestFallbackChainDuplicateIsCycle(t *testing.T) {
	ds := compileErr(t, baseAgentsManifest(twoProfiles+"fallback:\n  chain: [fast, slow, fast]\n"), syntheticYAML)
	if !hasCode(ds, "harness.fallback.chain.cycle") {
		t.Fatalf("expected fallback.chain.cycle, got: %v", ds)
	}
	if msg := diagMessage(ds, "harness.fallback.chain.cycle"); !strings.Contains(msg, "fast -> slow -> fast") {
		t.Errorf("cycle diagnostic should report the path, got %q", msg)
	}
}

// TestAgentDelegateCycleMultiHop: a TRUE multi-hop cycle a -> b -> c -> a is
// rejected and the diagnostic reports the full path.
func TestAgentDelegateCycleMultiHop(t *testing.T) {
	block := "agents:\n" +
		"  - id: a\n    delegates: [b]\n" +
		"  - id: b\n    delegates: [c]\n" +
		"  - id: c\n    delegates: [a]\n"
	ds := compileErr(t, baseAgentsManifest(block), syntheticYAML)
	if !hasCode(ds, "harness.agents.delegates.cycle") {
		t.Fatalf("expected agents.delegates.cycle, got: %v", ds)
	}
	msg := diagMessage(ds, "harness.agents.delegates.cycle")
	if !strings.Contains(msg, "a -> b -> c -> a") {
		t.Errorf("cycle diagnostic should report the path a -> b -> c -> a, got %q", msg)
	}
}

// TestAgentDelegateSelfCycle: a self reference is caught by the same traversal.
func TestAgentDelegateSelfCycle(t *testing.T) {
	ds := compileErr(t, baseAgentsManifest("agents:\n  - id: a\n    delegates: [a]\n"), syntheticYAML)
	if !hasCode(ds, "harness.agents.delegates.cycle") {
		t.Fatalf("expected agents.delegates.cycle, got: %v", ds)
	}
	if msg := diagMessage(ds, "harness.agents.delegates.cycle"); !strings.Contains(msg, "a -> a") {
		t.Errorf("self-cycle path should be reported, got %q", msg)
	}
}

// TestAgentDelegateAcyclicDeepGraphAccepted: the detector is a real traversal,
// not a depth cap — a deep but acyclic diamond graph compiles.
func TestAgentDelegateAcyclicDeepGraphAccepted(t *testing.T) {
	block := "agents:\n" +
		"  - id: a\n    delegates: [b, c]\n" +
		"  - id: b\n    delegates: [d]\n" +
		"  - id: c\n    delegates: [d]\n" +
		"  - id: d\n    delegates: [e]\n" +
		"  - id: e\n"
	p := compileOK(t, baseAgentsManifest(block))
	if len(p.Subagents()) != 5 {
		t.Fatalf("expected 5 subagents, got %d", len(p.Subagents()))
	}
}

// TestAgentDelegateUnknownAndDuplicate: unknown/duplicate delegate ids fail.
func TestAgentDelegateUnknownAndDuplicate(t *testing.T) {
	unknown := baseAgentsManifest("agents:\n  - id: a\n    delegates: [ghost]\n")
	if ds := compileErr(t, unknown, syntheticYAML); !hasCode(ds, "harness.agents.delegates.unknown") {
		t.Fatalf("expected agents.delegates.unknown, got: %v", ds)
	}

	dup := baseAgentsManifest("agents:\n  - id: a\n    delegates: [b, b]\n  - id: b\n")
	if ds := compileErr(t, dup, syntheticYAML); !hasCode(ds, "harness.agents.delegates.duplicate") {
		t.Fatalf("expected agents.delegates.duplicate, got: %v", ds)
	}
}

// TestFallbackEnumsValidated: retryOn and onExhaustion are closed enums; an
// unknown value is rejected, never silently ignored.
func TestFallbackEnumsValidated(t *testing.T) {
	retry := baseAgentsManifest(twoProfiles + "fallback:\n  chain: [fast]\n  retryOn: [timeout, teapot]\n")
	if ds := compileErr(t, retry, syntheticYAML); !hasCode(ds, "harness.fallback.retryOn.invalid") {
		t.Fatalf("expected fallback.retryOn.invalid, got: %v", ds)
	}

	dupRetry := baseAgentsManifest(twoProfiles + "fallback:\n  chain: [fast]\n  retryOn: [timeout, timeout]\n")
	if ds := compileErr(t, dupRetry, syntheticYAML); !hasCode(ds, "harness.fallback.retryOn.duplicate") {
		t.Fatalf("expected fallback.retryOn.duplicate, got: %v", ds)
	}

	exh := baseAgentsManifest(twoProfiles + "fallback:\n  chain: [fast]\n  onExhaustion: explode\n")
	if ds := compileErr(t, exh, syntheticYAML); !hasCode(ds, "harness.fallback.onExhaustion.invalid") {
		t.Fatalf("expected fallback.onExhaustion.invalid, got: %v", ds)
	}
}

// TestNegativeNumericsRejected: fallback numerics and both profile and agent
// limits reject negative values.
func TestNegativeNumericsRejected(t *testing.T) {
	cooldown := baseAgentsManifest(twoProfiles + "fallback:\n  chain: [fast]\n  cooldownSeconds: -1\n")
	if ds := compileErr(t, cooldown, syntheticYAML); !hasCode(ds, "harness.fallback.cooldownSeconds.invalid") {
		t.Fatalf("expected cooldownSeconds.invalid, got: %v", ds)
	}

	attempts := baseAgentsManifest(twoProfiles + "fallback:\n  chain: [fast]\n  maxAttempts: -2\n")
	if ds := compileErr(t, attempts, syntheticYAML); !hasCode(ds, "harness.fallback.maxAttempts.invalid") {
		t.Fatalf("expected maxAttempts.invalid, got: %v", ds)
	}

	profLimits := baseAgentsManifest("profiles:\n  - id: fast\n    provider: anthropic\n    model: m\n    limits:\n      maxTurns: -1\n")
	if ds := compileErr(t, profLimits, syntheticYAML); !hasCode(ds, "harness.profiles.limits.maxTurns.invalid") {
		t.Fatalf("expected profiles.limits.maxTurns.invalid, got: %v", ds)
	}

	agentLimits := baseAgentsManifest("agents:\n  - id: rev\n    limits:\n      timeoutSeconds: -5\n")
	if ds := compileErr(t, agentLimits, syntheticYAML); !hasCode(ds, "harness.agents.limits.timeoutSeconds.invalid") {
		t.Fatalf("expected agents.limits.timeoutSeconds.invalid, got: %v", ds)
	}
}

// TestFallbackClosedPostureOptIn: fallback defaults to DISABLED, and an
// explicit enable must be fully specified (non-empty chain + retryOn), so
// declaring a fallback section can never by itself widen the closed posture.
func TestFallbackClosedPostureOptIn(t *testing.T) {
	p := compileOK(t, baseAgentsManifest(twoProfiles+"fallback:\n  chain: [fast, slow]\n"))
	fb, ok := p.Fallback()
	if !ok {
		t.Fatalf("declared fallback should be carried on the plan")
	}
	if fb.Enabled {
		t.Errorf("fallback must default to DISABLED, got enabled=true")
	}
	if fb.OnExhaustion != FallbackOnExhaustionFail {
		t.Errorf("onExhaustion default = %q, want %q", fb.OnExhaustion, FallbackOnExhaustionFail)
	}
	if fb.MaxAttempts != 2 {
		t.Errorf("maxAttempts default = %d, want len(chain)=2", fb.MaxAttempts)
	}

	noChain := baseAgentsManifest(twoProfiles + "fallback:\n  enabled: true\n  retryOn: [timeout]\n")
	if ds := compileErr(t, noChain, syntheticYAML); !hasCode(ds, "harness.fallback.chain.required") {
		t.Fatalf("expected fallback.chain.required, got: %v", ds)
	}

	noRetry := baseAgentsManifest(twoProfiles + "fallback:\n  enabled: true\n  chain: [fast]\n")
	if ds := compileErr(t, noRetry, syntheticYAML); !hasCode(ds, "harness.fallback.retryOn.required") {
		t.Fatalf("expected fallback.retryOn.required, got: %v", ds)
	}

	enabled := compileOK(t, baseAgentsManifest(twoProfiles+
		"fallback:\n  enabled: true\n  chain: [fast, slow]\n  retryOn: [timeout, rateLimit]\n  onExhaustion: degrade\n"))
	efb, _ := enabled.Fallback()
	if !efb.Enabled || efb.OnExhaustion != FallbackOnExhaustionDegrade {
		t.Fatalf("explicit opt-in not carried: %+v", efb)
	}
}

// TestAgentToolsValidatedAgainstCatalog: per-agent tools use the SAME catalog
// as agent.tools; an unknown id is rejected and the tri-state is preserved.
func TestAgentToolsValidatedAgainstCatalog(t *testing.T) {
	bad := baseAgentsManifest("agents:\n  - id: rev\n    tools: [not.a.tool]\n")
	ds := compileErr(t, bad, syntheticYAML)
	if !hasCode(ds, "harness.agents.tools.unknown") {
		t.Fatalf("expected agents.tools.unknown, got: %v", ds)
	}

	good := compileOK(t, baseAgentsManifest("agents:\n  - id: rev\n    tools: [forge.read]\n"))
	sub := good.Subagents()[0]
	if !sub.ToolsSpecified || len(sub.Tools) != 1 || sub.Tools[0] != "forge.read" {
		t.Fatalf("expected resolved tool selection, got %+v", sub)
	}

	empty := compileOK(t, baseAgentsManifest("agents:\n  - id: rev\n    tools: []\n"))
	esub := empty.Subagents()[0]
	if !esub.ToolsSpecified || len(esub.Tools) != 0 {
		t.Fatalf("explicit empty selection must stay specified-with-zero-tools, got %+v", esub)
	}

	omitted := compileOK(t, baseAgentsManifest("agents:\n  - id: rev\n"))
	osub := omitted.Subagents()[0]
	if osub.ToolsSpecified {
		t.Fatalf("omitted tools must be unspecified, got %+v", osub)
	}
}

// TestProfileCredentialReferenceAndRedaction: a profile credential is an
// indirect reference, is redacted everywhere on the plan surface, and an inline
// literal is rejected outright.
func TestProfileCredentialReferenceAndRedaction(t *testing.T) {
	t.Setenv("HARNESS_8A_TEST_KEY", "super-secret-value")

	p := compileOK(t, baseAgentsManifest(
		"profiles:\n  - id: fast\n    provider: anthropic\n    model: m\n    credential:\n      env: HARNESS_8A_TEST_KEY\n"))

	specs := p.Profiles()
	if len(specs) != 1 || specs[0].CredentialRef != "env:HARNESS_8A_TEST_KEY" || specs[0].CredentialState != "present" {
		t.Fatalf("unexpected profile spec: %+v", specs)
	}

	js, err := p.ExplainJSON()
	if err != nil {
		t.Fatalf("ExplainJSON: %v", err)
	}
	if strings.Contains(string(js), "super-secret-value") {
		t.Fatalf("credential value leaked into ExplainJSON")
	}
	if strings.Contains(p.Digest(), "super-secret-value") {
		t.Fatalf("credential value leaked into digest")
	}

	sec, ok := p.ProfileCredential("fast")
	if !ok {
		t.Fatalf("ProfileCredential(fast) not found")
	}
	if v, present := sec.Reveal(); !present || v != "super-secret-value" {
		t.Fatalf("Reveal() = %q,%v; want the resolved value", v, present)
	}
	if sec.String() != redactedPlaceholder {
		t.Errorf("Secret.String() = %q, want redacted", sec.String())
	}

	inline := baseAgentsManifest(
		"profiles:\n  - id: fast\n    provider: anthropic\n    model: m\n    credential:\n      inline: oops\n")
	if ds := compileErr(t, inline, syntheticYAML); !hasCode(ds, "harness.profiles.credential.inlineForbidden") {
		t.Fatalf("expected inline credential rejection, got: %v", ds)
	}
}

// TestAgentSystemPromptFileManifestRelative: a subagent prompt file resolves
// against the MANIFEST directory, its body never appears on the redacted
// surface, and it is reachable only through the Reveal-style accessor.
func TestAgentSystemPromptFileManifestRelative(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "prompts"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	body := "REVIEWER-PROMPT-BODY-8A"
	writeFileT(t, filepath.Join(dir, "prompts", "reviewer.md"), body)

	manifest := baseAgentsManifest("agents:\n  - id: rev\n    systemPrompt:\n      file: prompts/reviewer.md\n")
	src := filepath.Join(dir, "harness.yaml")
	writeFileT(t, src, manifest)

	p, err := CompileBytes([]byte(manifest), src)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}

	sub := p.Subagents()[0]
	if sub.SystemPromptSource != "file" || sub.SystemPromptBytes != len(body) {
		t.Fatalf("unexpected prompt metadata: %+v", sub)
	}
	if sub.SystemPromptHash != hashString(body) {
		t.Errorf("prompt hash = %q, want %q", sub.SystemPromptHash, hashString(body))
	}

	js, err := p.ExplainJSON()
	if err != nil {
		t.Fatalf("ExplainJSON: %v", err)
	}
	if strings.Contains(string(js), body) {
		t.Fatalf("subagent prompt body leaked into ExplainJSON")
	}

	got, ok := p.RevealAgentSystemPrompt("rev")
	if !ok || got != body {
		t.Fatalf("RevealAgentSystemPrompt = %q,%v; want the file body", got, ok)
	}
	if _, ok := p.RevealAgentSystemPrompt("nope"); ok {
		t.Errorf("RevealAgentSystemPrompt should report ok=false for an unknown id")
	}

	// A prompt file outside the manifest subtree is rejected (containment).
	escape := baseAgentsManifest("agents:\n  - id: rev\n    systemPrompt:\n      file: ../escape.md\n")
	if ds := compileErr(t, escape, src); !hasCode(ds, "harness.path.traversal") {
		t.Fatalf("expected path traversal rejection, got: %v", ds)
	}

	// The one-of is enforced through the SAME resolver as the primary agent.
	both := baseAgentsManifest("agents:\n  - id: rev\n    systemPrompt:\n      inline: a\n      file: b\n")
	if ds := compileErr(t, both, syntheticYAML); !hasCode(ds, "harness.agents.systemPrompt.exclusive") {
		t.Fatalf("expected agents.systemPrompt.exclusive, got: %v", ds)
	}
}

// TestAgentSkillsAndHooksReuse: the 5a skills and 6a hooks shapes are reused
// verbatim under an agents[] entry, their diagnostics are re-scoped to the
// agent's field path, and an inline hook command stays tainted.
func TestAgentSkillsAndHooksReuse(t *testing.T) {
	block := "agents:\n" +
		"  - id: rev\n" +
		"    skills:\n      - id: reviewing\n" +
		"    hooks:\n" +
		"      - id: pre\n        event: tool.pre\n        scope: global\n        type: command\n        command: echo SUBAGENT-HOOK-SECRET\n"
	p := compileOK(t, baseAgentsManifest(block))

	sub := p.Subagents()[0]
	if len(sub.Skills) != 1 || sub.Skills[0].ID != "reviewing" {
		t.Fatalf("expected reused skill spec, got %+v", sub.Skills)
	}
	if len(sub.Hooks) != 1 || sub.Hooks[0].ID != "pre" || sub.Hooks[0].CommandHash != hashString("echo SUBAGENT-HOOK-SECRET") {
		t.Fatalf("expected reused hook spec with hashed command, got %+v", sub.Hooks)
	}

	js, err := p.ExplainJSON()
	if err != nil {
		t.Fatalf("ExplainJSON: %v", err)
	}
	if strings.Contains(string(js), "SUBAGENT-HOOK-SECRET") {
		t.Fatalf("subagent inline hook command leaked into ExplainJSON")
	}
	cmd, ok := p.RevealAgentHookCommand("rev", "pre")
	if !ok || cmd != "echo SUBAGENT-HOOK-SECRET" {
		t.Fatalf("RevealAgentHookCommand = %q,%v; want the inline command", cmd, ok)
	}
	if _, ok := p.RevealAgentHookCommand("rev", "nope"); ok {
		t.Errorf("unknown hook id should report ok=false")
	}

	// Re-scoped diagnostics: a duplicate skill id under an agent reports the
	// agent-scoped code and field path, not the top-level skills one.
	dupSkills := baseAgentsManifest("agents:\n  - id: rev\n    skills:\n      - id: s\n      - id: s\n")
	ds := compileErr(t, dupSkills, syntheticYAML)
	if !hasCode(ds, "harness.agents.skills.id.duplicate") {
		t.Fatalf("expected agents.skills.id.duplicate, got: %v", ds)
	}
	for _, d := range ds {
		if d.Code == "harness.agents.skills.id.duplicate" && !strings.HasPrefix(d.FieldPath, "agents[0].skills") {
			t.Errorf("field path = %q, want an agents[0].skills prefix", d.FieldPath)
		}
	}

	dupHooks := baseAgentsManifest("agents:\n  - id: rev\n    hooks:\n" +
		"      - id: h\n        event: e\n        scope: global\n        type: command\n        command: cmd-one\n" +
		"      - id: h\n        event: e\n        scope: global\n        type: command\n        command: cmd-two\n")
	if ds := compileErr(t, dupHooks, syntheticYAML); !hasCode(ds, "harness.agents.hooks.id.duplicate") {
		t.Fatalf("expected agents.hooks.id.duplicate, got: %v", ds)
	}
}

// TestAgentsDigestChangesPerFieldCategory: any change in any Phase 8a section
// changes the digest; an unchanged manifest keeps it stable.
func TestAgentsDigestChangesPerFieldCategory(t *testing.T) {
	baseBlock := twoProfiles +
		"fallback:\n  chain: [fast]\n  retryOn: [timeout]\n" +
		"agents:\n  - id: rev\n    profile: fast\n    tools: [forge.read]\n    delegates: [helper]\n  - id: helper\n"
	base := compileOK(t, baseAgentsManifest(baseBlock)).Digest()

	if again := compileOK(t, baseAgentsManifest(baseBlock)).Digest(); again != base {
		t.Fatalf("digest is not stable across identical compiles: %q vs %q", again, base)
	}

	variants := map[string]string{
		"profile model": strings.Replace(baseBlock, "model: claude-haiku", "model: claude-haiku-4.5", 1),
		"profile baseURL": strings.Replace(baseBlock, "  - id: fast\n    provider: anthropic\n    model: claude-haiku\n",
			"  - id: fast\n    provider: anthropic\n    model: claude-haiku\n    baseURL: https://example.com\n", 1),
		"fallback chain":        strings.Replace(baseBlock, "chain: [fast]", "chain: [slow]", 1),
		"fallback retryOn":      strings.Replace(baseBlock, "retryOn: [timeout]", "retryOn: [rateLimit]", 1),
		"fallback enabled":      strings.Replace(baseBlock, "fallback:\n  chain: [fast]", "fallback:\n  enabled: true\n  chain: [fast]", 1),
		"fallback onExhaustion": strings.Replace(baseBlock, "retryOn: [timeout]\n", "retryOn: [timeout]\n  onExhaustion: degrade\n", 1),
		"agent tools":           strings.Replace(baseBlock, "tools: [forge.read]", "tools: [forge.undo]", 1),
		"agent profile":         strings.Replace(baseBlock, "profile: fast", "profile: slow", 1),
		"agent delegates":       strings.Replace(baseBlock, "    delegates: [helper]\n", "", 1),
		"agent limits":          strings.Replace(baseBlock, "  - id: helper\n", "  - id: helper\n    limits:\n      maxTurns: 3\n", 1),
	}
	for name, block := range variants {
		if block == baseBlock {
			t.Fatalf("%s: variant did not modify the manifest", name)
		}
		got := compileOK(t, baseAgentsManifest(block)).Digest()
		if got == base {
			t.Errorf("%s: digest unchanged (%q); every 8a field category must affect the digest", name, got)
		}
	}
}

// TestAgentsAccessorsDefensiveCopies: mutating an accessor's result must not
// affect the immutable plan.
func TestAgentsAccessorsDefensiveCopies(t *testing.T) {
	block := twoProfiles +
		"fallback:\n  chain: [fast, slow]\n  retryOn: [timeout]\n" +
		"agents:\n  - id: rev\n    profile: fast\n    tools: [forge.read]\n    delegates: [helper]\n" +
		"    skills:\n      - id: s1\n" +
		"    hooks:\n      - id: h\n        event: e\n        scope: global\n        type: command\n        command: x\n        environment: [PATH]\n" +
		"    fallback:\n      chain: [slow]\n" +
		"  - id: helper\n"
	p := compileOK(t, baseAgentsManifest(block))

	profiles := p.Profiles()
	profiles[0].ID = "mutated"
	if p.Profiles()[0].ID == "mutated" {
		t.Errorf("Profiles() must return a defensive copy")
	}

	fb, _ := p.Fallback()
	fb.Chain[0] = "mutated"
	fb.RetryOn[0] = "mutated"
	if got, _ := p.Fallback(); got.Chain[0] == "mutated" || got.RetryOn[0] == "mutated" {
		t.Errorf("Fallback() must return defensive copies of its slices")
	}

	subs := p.Subagents()
	subs[0].ID = "mutated"
	subs[0].Tools[0] = "mutated"
	subs[0].Delegates[0] = "mutated"
	subs[0].Skills[0].ID = "mutated"
	subs[0].Hooks[0].Environment[0] = "mutated"
	subs[0].Fallback.Chain[0] = "mutated"

	fresh := p.Subagents()
	if fresh[0].ID == "mutated" || fresh[0].Tools[0] == "mutated" || fresh[0].Delegates[0] == "mutated" ||
		fresh[0].Skills[0].ID == "mutated" || fresh[0].Hooks[0].Environment[0] == "mutated" ||
		fresh[0].Fallback.Chain[0] == "mutated" {
		t.Errorf("Subagents() must return a deep defensive copy, got %+v", fresh[0])
	}
}

// TestAgentsExplainRedactedShape: Explain exposes ids/references/enums/limits/
// hashes/tool ids only.
func TestAgentsExplainRedactedShape(t *testing.T) {
	t.Setenv("HARNESS_8A_EXPLAIN_KEY", "leak-me-not")
	block := "profiles:\n  - id: fast\n    provider: anthropic\n    model: m\n    credential:\n      env: HARNESS_8A_EXPLAIN_KEY\n" +
		"fallback:\n  chain: [fast]\n  retryOn: [timeout]\n" +
		"agents:\n  - id: rev\n    profile: fast\n    systemPrompt:\n      inline: INLINE-SUBAGENT-PROMPT\n"
	p := compileOK(t, baseAgentsManifest(block))

	rep := p.Explain()
	if len(rep.Profiles) != 1 || rep.Fallback == nil || len(rep.Agents) != 1 {
		t.Fatalf("explain report missing 8a sections: %+v", rep)
	}
	js, err := p.ExplainJSON()
	if err != nil {
		t.Fatalf("ExplainJSON: %v", err)
	}
	for _, forbidden := range []string{"leak-me-not", "INLINE-SUBAGENT-PROMPT"} {
		if strings.Contains(string(js), forbidden) {
			t.Errorf("ExplainJSON leaked %q", forbidden)
		}
	}
	if rep.Agents[0].SystemPromptHash != hashString("INLINE-SUBAGENT-PROMPT") {
		t.Errorf("subagent prompt must be described by hash, got %+v", rep.Agents[0])
	}
}
