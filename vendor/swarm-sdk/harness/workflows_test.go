package harness

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// --- fixtures ---------------------------------------------------------------

// wfManifest builds a minimal valid harness manifest. tools defaults to "[]";
// extra is spliced in verbatim at document level.
func wfManifest(tools, extra string) string {
	if tools == "" {
		tools = "[]"
	}
	return "apiVersion: swarm.ai/v1alpha1\n" +
		"kind: Harness\n" +
		"metadata:\n  name: workflowstest\n" +
		"provider:\n  id: anthropic\n  model: claude-x\n" +
		"agent:\n  systemPrompt:\n    inline: \"hi\"\n  tools: " + tools + "\n" +
		extra
}

// wfHooksBlock declares the one hook a workflow's entry_hooks/exit_hooks may name.
const wfHooksBlock = "hooks:\n  - id: notify\n    event: tool.before_execute\n    scope: global\n    type: command\n    command: echo hi\n"

// wfKitchenTools is the selection the kitchen-sink workflow needs: forge.read for
// its agent's `Read` tool and interactive.ask_user_question for its
// config.allow_human_intervention.
const wfKitchenTools = "[forge.read, interactive.ask_user_question]"

// wfRef builds a `workflows:` block with one entry.
func wfRef(id, file, version string) string {
	return "workflows:\n  - id: " + id + "\n    file: " + file + "\n    expectedVersion: " + version + "\n"
}

// writeWorkflowDir writes every named file into a fresh temp dir and returns the
// harness manifest path inside it. Manifest-relative resolution is therefore
// exercised for real, without depending on any path in the repository.
func writeWorkflowDir(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, body := range files {
		full := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(full), 0o750); err != nil {
			t.Fatalf("mkdir %s: %v", filepath.Dir(full), err)
		}
		if err := os.WriteFile(full, []byte(body), 0o600); err != nil {
			t.Fatalf("write %s: %v", full, err)
		}
	}
	return filepath.Join(dir, "harness.yaml")
}

// compileWF writes manifest + flow.yaml into a temp dir and compiles.
func compileWF(t *testing.T, manifest, flow string) (*Plan, error) {
	t.Helper()
	src := writeWorkflowDir(t, map[string]string{"harness.yaml": manifest, "flow.yaml": flow})
	raw, err := os.ReadFile(src)
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	return CompileBytes(raw, src)
}

func compileWFOK(t *testing.T, manifest, flow string) *Plan {
	t.Helper()
	p, err := compileWF(t, manifest, flow)
	if err != nil {
		t.Fatalf("expected a clean compile, got: %v", err)
	}
	return p
}

func compileWFErr(t *testing.T, manifest, flow string) Diagnostics {
	t.Helper()
	p, err := compileWF(t, manifest, flow)
	if err == nil {
		t.Fatalf("expected a compile error, got plan with digest %s", p.Digest())
	}
	ds, ok := err.(Diagnostics)
	if !ok {
		t.Fatalf("expected Diagnostics, got %T: %v", err, err)
	}
	return ds
}

// wfMinimalFlow is the smallest valid workflow file.
const wfMinimalFlow = "id: flow-1\n" +
	"name: Flow One\n" +
	"version: 1.0.0\n" +
	"groups:\n" +
	"  - id: g1\n" +
	"    name: Group One\n" +
	"    execution: sequential\n" +
	"    agents:\n" +
	"      - name: Agent One\n" +
	"        provider: anthropic\n" +
	"        model: claude-x\n"

// wfKitchenFlow sets EVERY field of every mirrored DTO, so the mapping test can
// assert the whole snake_case -> camelCase table on one artifact. The SECRET*
// markers are the redaction probes.
const wfKitchenFlow = `id: flow-1
name: Flow One
description: rootprose
version: 1.0.0
config:
  max_duration: 2h
  allow_human_intervention: true
  fail_on_steering_block: false
  max_retries: 2
  timeout_behavior: partial
  custom:
    ckey: SECRETCUSTOM
groups:
  - id: g1
    name: Group One
    description: groupprose
    execution: parallel
    output_strategy: synthesize
    timeout: 10m
    metadata:
      gmk: SECRETGROUPMETA
    completion:
      type: majority
      threshold: 0.75
      min_agents: 2
      max_failures: 1
      require_output: true
    agents:
      - id: a1
        name: Agent One
        description: agentprose
        provider: anthropic
        model: claude-x
        tools:
          - Read
        system_prompt: SECRETPROMPTBODY
        system_prompt_template: SECRETTEMPLATE
        prompt_variables:
          pvk: SECRETPROMPTVAR
        provider_config:
          thinking_enabled: true
          thinking_budget: 2048
          system_cache_control:
            type: SECRETSYSCACHE
          tool_cache_control:
            type: SECRETTOOLCACHE
          message_cache_control:
            type: SECRETMSGCACHE
          custom:
            pck: SECRETPROVIDERCUSTOM
        context_sources:
          - repo-index
        capabilities:
          max_tokens: 8000
          temperature: 0.8
          max_turns: 3
          timeout_seconds: 900
          streaming: true
          cache_prompts: false
          parallel_tools: true
    coordinator:
      id: coord
      name: Coordinator
      provider: anthropic
      model: claude-x
    steering:
      synthesis_strategy: llm-synthesis
      conflict_resolution: weighted
      validate_plan:
        type: rules
        rules:
          - plan-must-list-files
        min_confidence: 0.5
  - id: g2
    name: Group Two
    execution: sequential
    depends_on:
      - g1
    agents:
      - name: Agent Two
        provider: anthropic
        model: claude-x
transitions:
  - from: g1
    to: g2
    condition: confidence > 0.8
    max_retries: 2
    priority: 5
    metadata:
      tmk: SECRETTRANSMETA
entry_hooks:
  - notify
exit_hooks:
  - notify
steering:
  type: rule-based
  rules:
    - id: r1
      condition: group.g1.failed
      action: retry_with_more_context
      priority: 10
      parameters:
        message: SECRETSTEERINGMESSAGE
parameters:
  - id: p1
    question: SECRETQUESTION
    description: paramprose
    type: choice
    required: true
    default: a
    choices:
      - a
      - b
    validation:
      min_length: 1
      max_length: 10
      pattern: ^[a-z]+$
      min: 1
      max: 5
      step: 0.5
    metadata:
      pmk: SECRETPARAMMETA
metadata:
  mk: SECRETROOTMETA
`

// --- omitted / empty / digest backward compatibility ------------------------

// TestWorkflowsOmittedAndEmptyValid: an omitted section and an explicitly empty
// `workflows: []` are both valid, carry nothing, produce the SAME digest, and add
// no key to the explained report.
func TestWorkflowsOmittedAndEmptyValid(t *testing.T) {
	p := compileOK(t, wfManifest("", ""))
	if len(p.Workflows()) != 0 {
		t.Errorf("omitted: want zero workflows, got %v", p.Workflows())
	}
	if _, ok := p.Workflow("flow-1"); ok {
		t.Errorf("omitted: Workflow() should report ok=false")
	}
	if len(p.RequiredRuntimeBindings()) != 0 {
		t.Errorf("omitted: want zero runtime bindings, got %v", p.RequiredRuntimeBindings())
	}
	digestOmitted := p.Digest()

	p2 := compileOK(t, wfManifest("", "workflows: []\n"))
	if len(p2.Workflows()) != 0 {
		t.Errorf("empty: want zero workflows, got %v", p2.Workflows())
	}
	if p2.Digest() != digestOmitted {
		t.Errorf("`workflows: []` vs omitted digests differ: %q vs %q", p2.Digest(), digestOmitted)
	}
	js, err := p2.ExplainJSON()
	if err != nil {
		t.Fatalf("ExplainJSON: %v", err)
	}
	if strings.Contains(string(js), "\"workflows\"") {
		t.Errorf("an unused workflows section must not add a \"workflows\" key to ExplainJSON")
	}
	for _, fs := range p2.Provenance() {
		if strings.HasPrefix(fs.Field, "workflows") {
			t.Errorf("unused workflows section leaked provenance %+v", fs)
		}
	}
}

// TestWorkflowsDigestBackwardCompatiblePre9c pins EXACT plan digests captured from
// a throwaway detached `git worktree` at HEAD (ee8aab80, pre-9c) for manifests that
// declare no workflows. They must be BYTE IDENTICAL after this slice: the Document
// field and the ExplainReport field are `omitempty`, and mergeWorkflowBindings is a
// strict no-op for zero uses, so an absent section contributes nothing to the
// canonical JSON the digest hashes.
func TestWorkflowsDigestBackwardCompatiblePre9c(t *testing.T) {
	const (
		pre9cBase   = "sha256:23a5efe1f9369a7ef4969a032c8a8b964cb4b98b91b372fbdc8e79176057bdd8"
		pre9cTooled = "sha256:3c81fd7c6aaaa9512906cccbf588532ddd5f1f843462ac7a912281b4baec0608"
	)
	if got := compileOK(t, wfManifest("", "")).Digest(); got != pre9cBase {
		t.Fatalf("digest changed for a manifest without a workflows section:\n got  %s\n want %s", got, pre9cBase)
	}
	if got := compileOK(t, wfManifest(wfKitchenTools, wfHooksBlock)).Digest(); got != pre9cTooled {
		t.Fatalf("digest changed for a tools+hooks manifest:\n got  %s\n want %s", got, pre9cTooled)
	}
}

// --- ids and the reference ---------------------------------------------------

func TestWorkflowIDMissingAndDuplicate(t *testing.T) {
	missing := wfManifest("", "workflows:\n  - file: ./flow.yaml\n    expectedVersion: 1.0.0\n")
	if ds := compileWFErr(t, missing, wfMinimalFlow); !hasCode(ds, "harness.workflows.id.missing") {
		t.Errorf("missing id not rejected: %v", ds)
	}
	empty := wfManifest("", "workflows:\n  - id: \"\"\n    file: ./flow.yaml\n    expectedVersion: 1.0.0\n")
	if ds := compileWFErr(t, empty, wfMinimalFlow); !hasCode(ds, "harness.workflows.id.missing") {
		t.Errorf("empty id not rejected: %v", ds)
	}
	dup := wfManifest("", wfRef("flow-1", "./flow.yaml", "1.0.0")+
		"  - id: flow-1\n    file: ./flow.yaml\n    expectedVersion: 1.0.0\n")
	if ds := compileWFErr(t, dup, wfMinimalFlow); !hasCode(ds, "harness.workflows.id.duplicate") {
		t.Errorf("duplicate id not rejected: %v", ds)
	}
}

// TestWorkflowReferenceFailsClosed covers the Phase 5a contained-file contract: an
// empty reference, a missing file, and a path that escapes the manifest directory.
func TestWorkflowReferenceFailsClosed(t *testing.T) {
	cases := []struct {
		name  string
		block string
		code  string
	}{
		{"empty file", "workflows:\n  - id: flow-1\n    file: \"\"\n    expectedVersion: 1.0.0\n", "harness.workflows.file.missing"},
		{"missing file", wfRef("flow-1", "./nope.yaml", "1.0.0"), "harness.path.missing"},
		{"traversal", wfRef("flow-1", "../escape.yaml", "1.0.0"), "harness.path.traversal"},
	}
	for _, tc := range cases {
		ds := compileWFErr(t, wfManifest("", tc.block), wfMinimalFlow)
		if !hasCode(ds, tc.code) {
			t.Errorf("%s: want %s, got %v", tc.name, tc.code, ds)
		}
	}
}

// TestWorkflowMalformedFileRejected: a file that is not valid YAML, one that holds
// two documents, and one whose shape does not fit the schema are all rejected.
func TestWorkflowMalformedFileRejected(t *testing.T) {
	manifest := wfManifest("", wfRef("flow-1", "./flow.yaml", "1.0.0"))
	cases := map[string]string{
		"harness.workflows.file.syntax":            "name: [unclosed\n",
		"harness.workflows.file.multipleDocuments": wfMinimalFlow + "---\n" + wfMinimalFlow,
		"harness.workflows.file.invalid":           "id: 12\nname: x\nversion: 1.0.0\ngroups: notalist\n",
	}
	for code, flow := range cases {
		ds := compileWFErr(t, manifest, flow)
		if !hasCode(ds, code) {
			t.Errorf("want %s, got %v", code, ds)
		}
	}
}

// TestWorkflowUnknownFileKeyRejected pins the D2 decision: an unknown key in the
// REFERENCED file is a hard error naming the key, not a silent pass and not a
// warning. The fixture uses a key that really exists in a live manifest
// (swarm-tui/workflows/multi_model_debate.yaml declares `gates:`, which
// internal/mode's own YAML DTOs do not).
func TestWorkflowUnknownFileKeyRejected(t *testing.T) {
	flow := wfMinimalFlow + "gates:\n  - id: quality\n"
	ds := compileWFErr(t, wfManifest("", wfRef("flow-1", "./flow.yaml", "1.0.0")), flow)
	msg := diagMessage(ds, "harness.workflows.file.unknownField")
	if msg == "" {
		t.Fatalf("unknown workflow-file key not rejected: %v", ds)
	}
	if !strings.Contains(msg, "gates") {
		t.Errorf("the diagnostic must name the offending key, got %q", msg)
	}
}

// TestWorkflowVersionPin pins the D3 decision: expectedVersion is REQUIRED, a file
// with no version cannot satisfy it, and a mismatch is an error that names both
// sides.
func TestWorkflowVersionPin(t *testing.T) {
	// Required.
	noPin := wfManifest("", "workflows:\n  - id: flow-1\n    file: ./flow.yaml\n")
	if ds := compileWFErr(t, noPin, wfMinimalFlow); !hasCode(ds, "harness.workflows.expectedVersion.missing") {
		t.Errorf("a missing expectedVersion must be rejected: %v", ds)
	}
	// Unverifiable: the file declares no version.
	unversioned := strings.Replace(wfMinimalFlow, "version: 1.0.0\n", "", 1)
	if ds := compileWFErr(t, wfManifest("", wfRef("flow-1", "./flow.yaml", "1.0.0")), unversioned); !hasCode(ds, "harness.workflows.version.missing") {
		t.Errorf("an unverifiable pin must be rejected: %v", ds)
	}
	// Mismatch.
	ds := compileWFErr(t, wfManifest("", wfRef("flow-1", "./flow.yaml", "2.0.0")), wfMinimalFlow)
	msg := diagMessage(ds, "harness.workflows.version.mismatch")
	if msg == "" {
		t.Fatalf("a version mismatch must be rejected: %v", ds)
	}
	for _, want := range []string{"2.0.0", "1.0.0"} {
		if !strings.Contains(msg, want) {
			t.Errorf("the mismatch diagnostic must name both versions, missing %q in %q", want, msg)
		}
	}
	// Match: compiles, and the carried version is the verified one.
	p := compileWFOK(t, wfManifest("", wfRef("flow-1", "./flow.yaml", "1.0.0")), wfMinimalFlow)
	w, ok := p.Workflow("flow-1")
	if !ok {
		t.Fatalf("workflow not carried")
	}
	if w.Version != "1.0.0" {
		t.Errorf("carried version = %q, want 1.0.0", w.Version)
	}
	if !strings.HasPrefix(w.ContentHash, "sha256:") {
		t.Errorf("content hash = %q, want a sha256: value", w.ContentHash)
	}
	if w.File != "flow.yaml" {
		t.Errorf("file label = %q, want the cleaned manifest-relative path", w.File)
	}
	if !p.hasProvenance("workflows.flow-1") {
		t.Errorf("a declared workflow must record provenance")
	}
}

// TestWorkflowContentHashChangesDigest is the D3 guarantee: swapping the
// referenced file changes the plan's identity even though the harness manifest is
// byte-identical.
func TestWorkflowContentHashChangesDigest(t *testing.T) {
	manifest := wfManifest("", wfRef("flow-1", "./flow.yaml", "1.0.0"))
	a := compileWFOK(t, manifest, wfMinimalFlow)
	b := compileWFOK(t, manifest, strings.Replace(wfMinimalFlow, "name: Group One", "name: Group Uno", 1))
	wa, _ := a.Workflow("flow-1")
	wb, _ := b.Workflow("flow-1")
	if wa.ContentHash == wb.ContentHash {
		t.Fatalf("a changed workflow file must change the content hash")
	}
	if a.Digest() == b.Digest() {
		t.Errorf("a changed workflow file must change the plan digest (%s)", a.Digest())
	}
	// And an unchanged file is stable.
	c := compileWFOK(t, manifest, wfMinimalFlow)
	wc, _ := c.Workflow("flow-1")
	if wa.ContentHash != wc.ContentHash {
		t.Errorf("the content hash must be deterministic")
	}
}

// --- REAL manifests ---------------------------------------------------------

// readRealWorkflow loads one of the in-repo workflow manifests. It is COPIED into
// the caller's temp dir by compileWF, so nothing is compiled at the live path and
// no test depends on the repository layout for resolution — only for the fixture
// bytes. A read failure is fatal rather than skipped, so this test cannot silently
// stop exercising real data.
func readRealWorkflow(t *testing.T, name string) string {
	t.Helper()
	path := filepath.Join("..", "swarm-tui", "workflows", name)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("real workflow fixture %s could not be read (%v); this test must exercise a real manifest", path, err)
	}
	return string(data)
}

// TestWorkflowRealManifestAdapts adapts a REAL in-repo workflow manifest
// (adversarial_review.yaml: three groups, rule-based steering, no agent tools)
// end to end and pins what the plan then carries.
func TestWorkflowRealManifestAdapts(t *testing.T) {
	flow := readRealWorkflow(t, "adversarial_review.yaml")
	// The real file declares config.allow_human_intervention: true, so the plan
	// must genuinely select the interactive capability — the D5 seam applies to
	// real data exactly as it does to fixtures.
	p := compileWFOK(t, wfManifest("[interactive.ask_user_question]", wfRef("adversarial", "./flow.yaml", "1.0.0")), flow)
	w, ok := p.Workflow("adversarial")
	if !ok {
		t.Fatalf("real workflow not carried")
	}
	if w.Definition.Name != "Adversarial Review" {
		t.Errorf("adapted name = %q", w.Definition.Name)
	}
	gotGroups := make([]string, 0, len(w.Definition.Groups))
	for _, g := range w.Definition.Groups {
		gotGroups = append(gotGroups, g.ID)
	}
	wantGroups := []string{"creation", "critique", "refinement"}
	if !reflect.DeepEqual(gotGroups, wantGroups) {
		t.Errorf("adapted group ids = %v, want %v", gotGroups, wantGroups)
	}
	if w.Definition.Steering == nil || w.Definition.Steering.Type != "rule-based" {
		t.Fatalf("adapted steering = %+v, want type rule-based", w.Definition.Steering)
	}
	if len(w.Definition.Steering.Rules) != 2 {
		t.Errorf("want the file's 2 steering rules, got %d", len(w.Definition.Steering.Rules))
	}
	// Every real group agent carries a prompt HASH and no body.
	for _, g := range w.Definition.Groups {
		for _, a := range g.Agents {
			if a.SystemPromptHash == "" || a.SystemPromptBytes == 0 {
				t.Errorf("group %s agent %s: prompt must be described by hash+bytes, got %q/%d", g.ID, a.Name, a.SystemPromptHash, a.SystemPromptBytes)
			}
		}
	}
	// D5 route (b): rule-based steering + human intervention are recorded as
	// required bindings rather than silently accepted.
	wantBindings := map[RuntimeBindingKind]bool{
		BindingWorkflowEngine:                  true,
		BindingWorkflowSteeringController:      true,
		BindingWorkflowHumanInterventionBroker: true,
	}
	got := map[RuntimeBindingKind]bool{}
	for _, b := range w.Bindings {
		got[b] = true
	}
	for b := range wantBindings {
		if !got[b] {
			t.Errorf("real manifest must require binding %s, got %v", b, w.Bindings)
		}
	}
	for _, r := range p.RequiredRuntimeBindings() {
		if r.Reason == "" {
			t.Errorf("binding %s reported with an empty reason", r.Binding)
		}
	}
}

// TestWorkflowRealManifestSteeringRejected is the D5 seam on REAL data: a shipped
// manifest that steers with an LLM meta-agent (gated_code_review.yaml, steering
// type "hybrid" + llm_meta_agent) cannot be adapted as active, and the refusal
// names the capabilities and their class.
func TestWorkflowRealManifestSteeringRejected(t *testing.T) {
	flow := readRealWorkflow(t, "gated_code_review.yaml")
	ds := compileWFErr(t, wfManifest(wfKitchenTools, wfHooksBlock+wfRef("gated", "./flow.yaml", "1.0.0")), flow)
	msg := diagMessage(ds, "harness.workflows.steering.llmMetaAgent.forbidden")
	if msg == "" {
		t.Fatalf("an llm_meta_agent in a real manifest must be refused: %v", ds)
	}
	for _, want := range []string{"steering.halt_peer_loop", string(PolicyDeferDiscover)} {
		if !strings.Contains(msg, want) {
			t.Errorf("the refusal must name %q; got %q", want, msg)
		}
	}
	if !hasCode(ds, "harness.workflows.steering.llmSteeringForbidden") {
		t.Errorf("steering type hybrid must also be refused on its own: %v", ds)
	}
}

// TestWorkflowRealManifestToolsCannotWiden is the other half of the D5 seam on real
// data: git_staging_workflow.yaml's agents declare `Task` and `BackgroundTask`,
// which resolve to DEFER-DISCOVER capabilities, plus names that are not in the
// catalog at all. None of them can be granted through a workflow file.
func TestWorkflowRealManifestToolsCannotWiden(t *testing.T) {
	flow := readRealWorkflow(t, "git_staging_workflow.yaml")
	ds := compileWFErr(t, wfManifest(wfKitchenTools, wfRef("staging", "./flow.yaml", "1.0.0")), flow)
	msg := diagMessage(ds, "harness.workflows.agents.tools.deferDiscover")
	if msg == "" {
		t.Fatalf("a DEFER-DISCOVER tool in a real manifest must be refused: %v", ds)
	}
	if !strings.Contains(msg, string(PolicyDeferDiscover)) {
		t.Errorf("the refusal must name the policy class, got %q", msg)
	}
	if !hasCode(ds, "harness.workflows.agents.tools.unknown") {
		t.Errorf("a tool name absent from the catalog must be refused too: %v", ds)
	}
}

// --- the FULL snake_case -> camelCase mapping -------------------------------

// TestWorkflowFieldMappingIsAsserted walks the kitchen-sink workflow and ASSERTS
// every mapped field's adapted value, rather than assuming the adapter wired them.
// Each case names the snake_case source and the camelCase destination.
func TestWorkflowFieldMappingIsAsserted(t *testing.T) {
	p := compileWFOK(t, wfManifest(wfKitchenTools, wfHooksBlock+wfRef("flow-1", "./flow.yaml", "1.0.0")), wfKitchenFlow)
	w, ok := p.Workflow("flow-1")
	if !ok {
		t.Fatalf("workflow not carried")
	}
	d := w.Definition

	// root
	eq(t, "id -> definition.id", d.ID, "flow-1")
	eq(t, "name -> definition.name", d.Name, "Flow One")
	eq(t, "version -> version", w.Version, "1.0.0")
	eqSlice(t, "metadata -> metadataKeys", d.MetadataKeys, []string{"mk"})
	eqSlice(t, "entry_hooks -> entryHooks", d.EntryHooks, []string{"notify"})
	eqSlice(t, "exit_hooks -> exitHooks", d.ExitHooks, []string{"notify"})

	// config
	eq(t, "config.max_duration -> config.maxDuration", d.Config.MaxDuration, "2h")
	eqBoolPtr(t, "config.allow_human_intervention -> config.allowHumanIntervention", d.Config.AllowHumanIntervention, true)
	eqBoolPtr(t, "config.fail_on_steering_block -> config.failOnSteeringBlock", d.Config.FailOnSteeringBlock, false)
	eqIntPtr(t, "config.max_retries -> config.maxRetries", d.Config.MaxRetries, 2)
	eq(t, "config.timeout_behavior -> config.timeoutBehavior", d.Config.TimeoutBehavior, "partial")
	eqSlice(t, "config.custom -> config.customKeys", d.Config.CustomKeys, []string{"ckey"})

	if len(d.Groups) != 2 {
		t.Fatalf("want 2 groups, got %d", len(d.Groups))
	}
	g := d.Groups[0]
	eq(t, "groups[].id -> groups[].id", g.ID, "g1")
	eq(t, "groups[].name -> groups[].name", g.Name, "Group One")
	eq(t, "groups[].execution -> groups[].execution", g.Execution, "parallel")
	eq(t, "groups[].output_strategy -> groups[].outputStrategy", g.OutputStrategy, "synthesize")
	eq(t, "groups[].timeout -> groups[].timeout", g.Timeout, "10m")
	eqSlice(t, "groups[].metadata -> groups[].metadataKeys", g.MetadataKeys, []string{"gmk"})
	eq(t, "groups[].completion.type -> completion.type", g.Completion.Type, "majority")
	eqFloatPtr(t, "groups[].completion.threshold -> completion.threshold", g.Completion.Threshold, 0.75)
	eqIntPtr(t, "groups[].completion.min_agents -> completion.minAgents", g.Completion.MinAgents, 2)
	eqIntPtr(t, "groups[].completion.max_failures -> completion.maxFailures", g.Completion.MaxFailures, 1)
	eqBoolPtr(t, "groups[].completion.require_output -> completion.requireOutput", g.Completion.RequireOutput, true)
	eqSlice(t, "groups[].depends_on -> groups[].dependsOn", d.Groups[1].DependsOn, []string{"g1"})

	if len(g.Agents) != 1 {
		t.Fatalf("want 1 agent in g1, got %d", len(g.Agents))
	}
	a := g.Agents[0]
	eq(t, "agents[].id -> agents[].id", a.ID, "a1")
	eq(t, "agents[].name -> agents[].name", a.Name, "Agent One")
	eq(t, "agents[].provider -> agents[].provider", a.Provider, "anthropic")
	eq(t, "agents[].model -> agents[].model", a.Model, "claude-x")
	eqSlice(t, "agents[].tools -> agents[].tools", a.Tools, []string{"Read"})
	eqSlice(t, "agents[].tools -> agents[].toolCapabilities", a.ToolCapabilities, []string{"forge.read"})
	eq(t, "agents[].system_prompt -> agents[].systemPromptHash", a.SystemPromptHash, hashString("SECRETPROMPTBODY"))
	eq(t, "agents[].system_prompt -> agents[].systemPromptBytes", a.SystemPromptBytes, len("SECRETPROMPTBODY"))
	eq(t, "agents[].system_prompt_template -> agents[].systemPromptTemplateHash", a.SystemPromptTemplateHash, hashString("SECRETTEMPLATE"))
	eq(t, "agents[].system_prompt_template -> agents[].systemPromptTemplateBytes", a.SystemPromptTemplateBytes, len("SECRETTEMPLATE"))
	eqSlice(t, "agents[].prompt_variables -> agents[].promptVariableKeys", a.PromptVariableKeys, []string{"pvk"})
	eqSlice(t, "agents[].context_sources -> agents[].contextSources", a.ContextSources, []string{"repo-index"})
	if a.ProviderConfig == nil {
		t.Fatalf("agents[].provider_config -> agents[].providerConfig was dropped entirely")
	}
	eq(t, "provider_config.thinking_enabled -> providerConfig.thinkingEnabled", a.ProviderConfig.ThinkingEnabled, true)
	eq(t, "provider_config.thinking_budget -> providerConfig.thinkingBudget", a.ProviderConfig.ThinkingBudget, 2048)
	eqSlice(t, "provider_config.system_cache_control -> providerConfig.systemCacheControlKeys", a.ProviderConfig.SystemCacheControlKeys, []string{"type"})
	eqSlice(t, "provider_config.tool_cache_control -> providerConfig.toolCacheControlKeys", a.ProviderConfig.ToolCacheControlKeys, []string{"type"})
	eqSlice(t, "provider_config.message_cache_control -> providerConfig.messageCacheControlKeys", a.ProviderConfig.MessageCacheControlKeys, []string{"type"})
	eqSlice(t, "provider_config.custom -> providerConfig.customKeys", a.ProviderConfig.CustomKeys, []string{"pck"})
	if a.Capabilities == nil {
		t.Fatalf("agents[].capabilities -> agents[].capabilities was dropped entirely")
	}
	eqIntPtr(t, "capabilities.max_tokens -> capabilities.maxTokens", a.Capabilities.MaxTokens, 8000)
	eqFloatPtr(t, "capabilities.temperature -> capabilities.temperature", a.Capabilities.Temperature, 0.8)
	eqIntPtr(t, "capabilities.max_turns -> capabilities.maxTurns", a.Capabilities.MaxTurns, 3)
	eqIntPtr(t, "capabilities.timeout_seconds -> capabilities.timeoutSeconds", a.Capabilities.TimeoutSeconds, 900)
	eqBoolPtr(t, "capabilities.streaming -> capabilities.streaming", a.Capabilities.Streaming, true)
	eqBoolPtr(t, "capabilities.cache_prompts -> capabilities.cachePrompts", a.Capabilities.CachePrompts, false)
	eqBoolPtr(t, "capabilities.parallel_tools -> capabilities.parallelTools", a.Capabilities.ParallelTools, true)

	if g.Coordinator == nil {
		t.Fatalf("groups[].coordinator -> groups[].coordinator was dropped")
	}
	eq(t, "coordinator.id -> coordinator.id", g.Coordinator.ID, "coord")

	if g.Steering == nil {
		t.Fatalf("groups[].steering -> groups[].steering was dropped")
	}
	eq(t, "groups[].steering.synthesis_strategy -> steering.synthesisStrategy", g.Steering.SynthesisStrategy, "llm-synthesis")
	eq(t, "groups[].steering.conflict_resolution -> steering.conflictResolution", g.Steering.ConflictResolution, "weighted")
	if g.Steering.ValidatePlan == nil {
		t.Fatalf("groups[].steering.validate_plan -> steering.validatePlan was dropped")
	}
	eq(t, "validate_plan.type -> validatePlan.type", g.Steering.ValidatePlan.Type, "rules")
	eqSlice(t, "validate_plan.rules -> validatePlan.rules", g.Steering.ValidatePlan.Rules, []string{"plan-must-list-files"})
	eq(t, "validate_plan.min_confidence -> validatePlan.minConfidence", g.Steering.ValidatePlan.MinConfidence, 0.5)

	if len(d.Transitions) != 1 {
		t.Fatalf("want 1 transition, got %d", len(d.Transitions))
	}
	tr := d.Transitions[0]
	eq(t, "transitions[].from -> transitions[].from", tr.From, "g1")
	eq(t, "transitions[].to -> transitions[].to", tr.To, "g2")
	eq(t, "transitions[].condition -> transitions[].condition", tr.Condition, "confidence > 0.8")
	eq(t, "transitions[].max_retries -> transitions[].maxRetries", tr.MaxRetries, 2)
	eq(t, "transitions[].priority -> transitions[].priority", tr.Priority, 5)
	eqSlice(t, "transitions[].metadata -> transitions[].metadataKeys", tr.MetadataKeys, []string{"tmk"})

	if d.Steering == nil || len(d.Steering.Rules) != 1 {
		t.Fatalf("steering -> definition.steering not adapted: %+v", d.Steering)
	}
	eq(t, "steering.type -> steering.type", d.Steering.Type, "rule-based")
	r := d.Steering.Rules[0]
	eq(t, "steering.rules[].id -> rules[].id", r.ID, "r1")
	eq(t, "steering.rules[].condition -> rules[].condition", r.Condition, "group.g1.failed")
	eq(t, "steering.rules[].action -> rules[].action", r.Action, "retry_with_more_context")
	eq(t, "steering.rules[].priority -> rules[].priority", r.Priority, 10)
	eqSlice(t, "steering.rules[].parameters -> rules[].parameterKeys", r.ParameterKeys, []string{"message"})

	if len(d.Parameters) != 1 {
		t.Fatalf("want 1 parameter, got %d", len(d.Parameters))
	}
	prm := d.Parameters[0]
	eq(t, "parameters[].id -> parameters[].id", prm.ID, "p1")
	eq(t, "parameters[].type -> parameters[].type", prm.Type, "choice")
	eq(t, "parameters[].required -> parameters[].required", prm.Required, true)
	eq(t, "parameters[].default -> parameters[].hasDefault", prm.HasDefault, true)
	eqSlice(t, "parameters[].choices -> parameters[].choices", prm.Choices, []string{"a", "b"})
	eq(t, "parameters[].question -> parameters[].questionHash", prm.QuestionHash, hashString("SECRETQUESTION"))
	eqSlice(t, "parameters[].metadata -> parameters[].metadataKeys", prm.MetadataKeys, []string{"pmk"})
	if prm.Validation == nil {
		t.Fatalf("parameters[].validation -> parameters[].validation was dropped")
	}
	eq(t, "validation.min_length -> validation.minLength", prm.Validation.MinLength, 1)
	eq(t, "validation.max_length -> validation.maxLength", prm.Validation.MaxLength, 10)
	eq(t, "validation.pattern -> validation.pattern", prm.Validation.Pattern, "^[a-z]+$")
	eq(t, "validation.min -> validation.min", prm.Validation.Min, float64(1))
	eq(t, "validation.max -> validation.max", prm.Validation.Max, float64(5))
	eq(t, "validation.step -> validation.step", prm.Validation.Step, 0.5)

	// Every one of this slice's six bindings is exercised by the kitchen sink.
	got := map[RuntimeBindingKind]bool{}
	for _, b := range w.Bindings {
		got[b] = true
	}
	for _, want := range workflowBindingOrder {
		if !got[want] {
			t.Errorf("kitchen-sink workflow must require binding %s, got %v", want, w.Bindings)
		}
	}
}

// TestWorkflowDeliberateDropsAreReported: every field this adapter does not carry
// is REPORTED with a reason, and every reported drop has a registered reason (an
// undocumented drop is marked as such and would fail here).
func TestWorkflowDeliberateDropsAreReported(t *testing.T) {
	p := compileWFOK(t, wfManifest(wfKitchenTools, wfHooksBlock+wfRef("flow-1", "./flow.yaml", "1.0.0")), wfKitchenFlow)
	w, _ := p.Workflow("flow-1")
	got := map[string]string{}
	for _, d := range w.DroppedFields {
		got[d.Field] = d.Reason
	}
	for _, want := range []string{
		"description",
		"groups[].description",
		"groups[].agents[].description",
		"parameters[].description",
		"parameters[].question",
		"parameters[].default",
		"metadata{}",
		"config.custom{}",
		"groups[].metadata{}",
		"groups[].agents[].prompt_variables{}",
		"groups[].agents[].provider_config{}",
		"transitions[].metadata{}",
		"steering.rules[].parameters{}",
		"parameters[].metadata{}",
		"groups[].agents[].system_prompt",
		"groups[].agents[].system_prompt_template",
	} {
		reason, ok := got[want]
		if !ok {
			t.Errorf("drop of %q must be reported; reported: %v", want, w.DroppedFields)
			continue
		}
		if reason == "" || strings.Contains(reason, "UNDOCUMENTED") {
			t.Errorf("drop of %q has no registered reason: %q", want, reason)
		}
	}
	// A workflow that drops nothing reports nothing.
	p2 := compileWFOK(t, wfManifest("", wfRef("flow-1", "./flow.yaml", "1.0.0")), wfMinimalFlow)
	w2, _ := p2.Workflow("flow-1")
	if len(w2.DroppedFields) != 0 {
		t.Errorf("a workflow with no droppable fields must report no drops, got %v", w2.DroppedFields)
	}
}

// TestWorkflowExplainNeverLeaksBodies: no prompt body, template, metadata value,
// prompt variable value, provider payload value, steering parameter value, or
// question text may appear anywhere in the redacted report.
func TestWorkflowExplainNeverLeaksBodies(t *testing.T) {
	p := compileWFOK(t, wfManifest(wfKitchenTools, wfHooksBlock+wfRef("flow-1", "./flow.yaml", "1.0.0")), wfKitchenFlow)
	js, err := p.ExplainJSON()
	if err != nil {
		t.Fatalf("ExplainJSON: %v", err)
	}
	for _, secret := range []string{
		"SECRETPROMPTBODY", "SECRETTEMPLATE", "SECRETPROMPTVAR", "SECRETSYSCACHE",
		"SECRETTOOLCACHE", "SECRETMSGCACHE", "SECRETPROVIDERCUSTOM", "SECRETCUSTOM",
		"SECRETGROUPMETA", "SECRETTRANSMETA", "SECRETSTEERINGMESSAGE", "SECRETQUESTION",
		"SECRETPARAMMETA", "SECRETROOTMETA",
		"rootprose", "groupprose", "agentprose", "paramprose",
	} {
		if strings.Contains(string(js), secret) {
			t.Errorf("the redacted report leaked %q", secret)
		}
	}
	// What it DOES expose: the identity and posture facts.
	for _, want := range []string{"\"workflows\"", "\"contentHash\"", "\"version\"", "\"systemPromptHash\"", "\"droppedFields\""} {
		if !strings.Contains(string(js), want) {
			t.Errorf("the redacted report must expose %s", want)
		}
	}
}

// --- tiny assertion helpers -------------------------------------------------

func eq[T comparable](t *testing.T, what string, got, want T) {
	t.Helper()
	if got != want {
		t.Errorf("%s: got %v, want %v", what, got, want)
	}
}

func eqSlice(t *testing.T, what string, got, want []string) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Errorf("%s: got %v, want %v", what, got, want)
	}
}

func eqIntPtr(t *testing.T, what string, got *int, want int) {
	t.Helper()
	if got == nil {
		t.Errorf("%s: got nil, want %d", what, want)
		return
	}
	if *got != want {
		t.Errorf("%s: got %d, want %d", what, *got, want)
	}
}

func eqBoolPtr(t *testing.T, what string, got *bool, want bool) {
	t.Helper()
	if got == nil {
		t.Errorf("%s: got nil, want %v", what, want)
		return
	}
	if *got != want {
		t.Errorf("%s: got %v, want %v", what, *got, want)
	}
}

func eqFloatPtr(t *testing.T, what string, got *float64, want float64) {
	t.Helper()
	if got == nil {
		t.Errorf("%s: got nil, want %v", what, want)
		return
	}
	if *got != want {
		t.Errorf("%s: got %v, want %v", what, *got, want)
	}
}

// --- D5: no widening, per feature -------------------------------------------

// flowWith splices extra top-level YAML onto the minimal flow.
func flowWith(extra string) string { return wfMinimalFlow + extra }

// flowWithAgentTools rebuilds the minimal flow with an agent tool list.
func flowWithAgentTools(tools string) string {
	return strings.Replace(wfMinimalFlow,
		"        model: claude-x\n",
		"        model: claude-x\n        tools: "+tools+"\n", 1)
}

// TestWorkflowToolsCannotWidenCapabilities is the core D5 assertion: a workflow
// agent's tools are held to exactly the plan's audited selection.
func TestWorkflowToolsCannotWidenCapabilities(t *testing.T) {
	ref := wfRef("flow-1", "./flow.yaml", "1.0.0")

	// (1) A capability the plan selects: accepted, and reported as the resolved id.
	p := compileWFOK(t, wfManifest("[forge.read]", ref), flowWithAgentTools("[Read]"))
	w, _ := p.Workflow("flow-1")
	eqSlice(t, "selected alias resolves", w.Definition.Groups[0].Agents[0].ToolCapabilities, []string{"forge.read"})

	// (2) The SAME workflow against a plan that selects nothing: widening, refused.
	ds := compileWFErr(t, wfManifest("", ref), flowWithAgentTools("[Read]"))
	msg := diagMessage(ds, "harness.workflows.agents.tools.widensCapabilities")
	if msg == "" {
		t.Fatalf("an unselected capability must be refused: %v", ds)
	}
	if !strings.Contains(msg, "forge.read") {
		t.Errorf("the refusal must name the capability, got %q", msg)
	}

	// (3) A DEFER-DISCOVER steering tool, by runtime alias AND by stable id: both
	// refused with the class named, with or without the capability being selected
	// (it CANNOT be selected — that is the point of the class).
	for _, tools := range []string{"[block_next_tool]", "[steering.block_next_tool]", "[halt_peer_loop]"} {
		ds := compileWFErr(t, wfManifest("[forge.read]", ref), flowWithAgentTools(tools))
		m := diagMessage(ds, "harness.workflows.agents.tools.deferDiscover")
		if m == "" {
			t.Fatalf("%s must be refused as DEFER-DISCOVER: %v", tools, ds)
		}
		if !strings.Contains(m, string(PolicyDeferDiscover)) || !strings.Contains(m, "steering.") {
			t.Errorf("%s: refusal must name the capability and class, got %q", tools, m)
		}
	}

	// (4) A name the catalog does not know at all.
	if ds := compileWFErr(t, wfManifest("[forge.read]", ref), flowWithAgentTools("[NoSuchTool]")); !hasCode(ds, "harness.workflows.agents.tools.unknown") {
		t.Errorf("an uncatalogued tool name must be refused: %v", ds)
	}

	// (5) A subagent's selection counts as part of the effective set, so the two
	// gates cannot disagree about what the document is allowed to do.
	agents := "agents:\n  - id: helper\n    systemPrompt:\n      inline: \"h\"\n    tools: [forge.read]\n"
	if _, err := compileWF(t, wfManifest("", agents+ref), flowWithAgentTools("[Read]")); err != nil {
		t.Errorf("a capability selected by a subagent must satisfy the gate: %v", err)
	}
}

// TestWorkflowSteeringRoutesPerFeature pins the D5 route chosen for EVERY steering
// feature: which are rejected (route a) and which compile while recording a
// required runtime binding (route b).
func TestWorkflowSteeringRoutesPerFeature(t *testing.T) {
	ref := wfRef("flow-1", "./flow.yaml", "1.0.0")
	manifest := wfManifest(wfKitchenTools, wfHooksBlock+ref)

	// ROUTE (a): rejected features.
	rejected := map[string]struct {
		extra string
		code  string
	}{
		"llm-based steering": {"steering:\n  type: llm-based\n", "harness.workflows.steering.llmSteeringForbidden"},
		"hybrid steering":    {"steering:\n  type: hybrid\n", "harness.workflows.steering.llmSteeringForbidden"},
		"llm meta agent": {"steering:\n  type: rule-based\n  llm_meta_agent:\n    name: Overseer\n    provider: anthropic\n    model: claude-x\n",
			"harness.workflows.steering.llmMetaAgent.forbidden"},
		"unknown steering type": {"steering:\n  type: telepathy\n", "harness.workflows.steering.type.unknown"},
		"workflow steering custom": {"steering:\n  type: rule-based\n  custom:\n    k: v\n",
			"harness.workflows.steering.custom.unclassifiable"},
	}
	for name, tc := range rejected {
		ds := compileWFErr(t, manifest, flowWith(tc.extra))
		if !hasCode(ds, tc.code) {
			t.Errorf("%s: want %s, got %v", name, tc.code, ds)
		}
	}

	// ROUTE (a) at group level.
	groupRejected := map[string]struct {
		block string
		code  string
	}{
		"validate_plan with a prompt": {"    steering:\n      validate_plan:\n        type: llm\n        prompt: JUDGE THIS\n",
			"harness.workflows.groups.steering.validatePlan.prompt.forbidden"},
		"group steering custom": {"    steering:\n      custom:\n        k: v\n",
			"harness.workflows.groups.steering.custom.unclassifiable"},
		"unknown synthesis strategy": {"    steering:\n      synthesis_strategy: telepathy\n",
			"harness.workflows.groups.steering.synthesisStrategy.unknown"},
		"unknown conflict resolution": {"    steering:\n      conflict_resolution: coinflip\n",
			"harness.workflows.groups.steering.conflictResolution.unknown"},
		"llm-synthesis without a coordinator": {"    steering:\n      synthesis_strategy: llm-synthesis\n",
			"harness.workflows.groups.steering.llmSynthesisWithoutCoordinator"},
		"llm-decide without a coordinator": {"    steering:\n      conflict_resolution: llm-decide\n",
			"harness.workflows.groups.steering.llmDecideWithoutCoordinator"},
	}
	for name, tc := range groupRejected {
		flow := wfMinimalFlow + tc.block
		ds := compileWFErr(t, manifest, flow)
		if !hasCode(ds, tc.code) {
			t.Errorf("%s: want %s, got %v", name, tc.code, ds)
		}
	}

	// ROUTE (b): accepted, but ONLY with the binding recorded.
	accepted := map[string]struct {
		flow    string
		binding RuntimeBindingKind
	}{
		"rule-based steering": {flowWith("steering:\n  type: rule-based\n"), BindingWorkflowSteeringController},
		"prompt-free validate_plan": {wfMinimalFlow + "    steering:\n      validate_plan:\n        type: rules\n        rules: [r]\n        min_confidence: 0.5\n",
			BindingWorkflowPlanValidator},
		"deterministic synthesis": {wfMinimalFlow + "    steering:\n      synthesis_strategy: voting\n      conflict_resolution: majority\n",
			BindingWorkflowOutputSynthesizer},
		"parameters":         {flowWith("parameters:\n  - id: p\n    question: q\n    type: text\n"), BindingWorkflowParameterProvider},
		"human intervention": {flowWith("config:\n  allow_human_intervention: true\n"), BindingWorkflowHumanInterventionBroker},
	}
	for name, tc := range accepted {
		p := compileWFOK(t, manifest, tc.flow)
		w, ok := p.Workflow("flow-1")
		if !ok {
			t.Fatalf("%s: workflow not carried", name)
		}
		found := false
		for _, b := range w.Bindings {
			if b == tc.binding {
				found = true
			}
		}
		if !found {
			t.Errorf("%s: must record binding %s, got %v", name, tc.binding, w.Bindings)
		}
		// Route (b) is never "silently accepted": the requirement is enumerable on
		// the plan with a reason preflight can quote.
		var reason string
		for _, r := range p.RequiredRuntimeBindings() {
			if r.Binding == tc.binding {
				reason = r.Reason
			}
		}
		if reason == "" {
			t.Errorf("%s: binding %s must be enumerable with a reason", name, tc.binding)
		}
		// An unenforced steering feature must never appear as an enforced one.
		if _, ok := ScheduleEnforcementOf(ScheduleDimTarget, "workflow"); ok {
			t.Errorf("scheduleFacts must NOT claim a (target, workflow) row; the class is stated in resolveScheduleWorkflowTarget")
		}
	}

	// A steering ACTION that names a capability is the sharpest back door.
	ds := compileWFErr(t, manifest, flowWith("steering:\n  type: rule-based\n  rules:\n    - id: r\n      condition: c\n      action: halt_peer_loop\n"))
	if !hasCode(ds, "harness.workflows.steering.rules.action.deferDiscover") {
		t.Errorf("a steering action naming a DEFER-DISCOVER capability must be refused: %v", ds)
	}
	ds = compileWFErr(t, wfManifest("", ref), flowWith("steering:\n  type: rule-based\n  rules:\n    - id: r\n      condition: c\n      action: Read\n"))
	if !hasCode(ds, "harness.workflows.steering.rules.action.widensCapabilities") {
		t.Errorf("a steering action naming an unselected capability must be refused: %v", ds)
	}
}

// TestWorkflowHumanInterventionCannotWiden: the flag cannot grant an interactive
// capability the plan never selected.
func TestWorkflowHumanInterventionCannotWiden(t *testing.T) {
	flow := flowWith("config:\n  allow_human_intervention: true\n")
	ds := compileWFErr(t, wfManifest("", wfRef("flow-1", "./flow.yaml", "1.0.0")), flow)
	msg := diagMessage(ds, "harness.workflows.config.allowHumanIntervention.widensCapabilities")
	if msg == "" {
		t.Fatalf("allow_human_intervention without the interactive capability must be refused: %v", ds)
	}
	if !strings.Contains(msg, "interactive.ask_user_question") {
		t.Errorf("the refusal must name the capability, got %q", msg)
	}
}

// TestWorkflowHooksCannotWiden: a workflow may not name a hook the plan's audited
// hooks: section never declared.
func TestWorkflowHooksCannotWiden(t *testing.T) {
	ref := wfRef("flow-1", "./flow.yaml", "1.0.0")
	if _, err := compileWF(t, wfManifest("", wfHooksBlock+ref), flowWith("entry_hooks:\n  - notify\n")); err != nil {
		t.Errorf("a declared hook must satisfy the gate: %v", err)
	}
	ds := compileWFErr(t, wfManifest("", wfHooksBlock+ref), flowWith("exit_hooks:\n  - rogue\n"))
	if !hasCode(ds, "harness.workflows.exitHooks.undeclared") {
		t.Errorf("an undeclared workflow hook must be refused: %v", ds)
	}
}

// --- structural fail-closed rules -------------------------------------------

func TestWorkflowStructuralRulesFailClosed(t *testing.T) {
	manifest := wfManifest("", wfRef("flow-1", "./flow.yaml", "1.0.0"))
	base := "id: flow-1\nname: Flow One\nversion: 1.0.0\n"
	agent := "    agents:\n      - name: A\n        provider: anthropic\n        model: claude-x\n"
	cases := map[string]struct {
		flow string
		code string
	}{
		"no name":                  {"id: f\nversion: 1.0.0\ngroups:\n  - id: g\n    execution: sequential\n" + agent, "harness.workflows.name.missing"},
		"no groups":                {base, "harness.workflows.groups.empty"},
		"group without id":         {base + "groups:\n  - name: G\n    execution: sequential\n" + agent, "harness.workflows.groups.id.missing"},
		"duplicate group id":       {base + "groups:\n  - id: g\n    execution: sequential\n" + agent + "  - id: g\n    execution: sequential\n" + agent, "harness.workflows.groups.id.duplicate"},
		"unknown execution":        {base + "groups:\n  - id: g\n    execution: telepathic\n" + agent, "harness.workflows.groups.execution.unknown"},
		"no agents":                {base + "groups:\n  - id: g\n    execution: sequential\n", "harness.workflows.groups.agents.empty"},
		"unknown outputStrategy":   {base + "groups:\n  - id: g\n    execution: sequential\n    output_strategy: telepathy\n" + agent, "harness.workflows.groups.outputStrategy.unknown"},
		"unknown completion":       {base + "groups:\n  - id: g\n    execution: sequential\n    completion:\n      type: vibes\n" + agent, "harness.workflows.groups.completion.type.unknown"},
		"bad group timeout":        {base + "groups:\n  - id: g\n    execution: sequential\n    timeout: soon\n" + agent, "harness.workflows.groups.timeout.invalid"},
		"agent without provider":   {base + "groups:\n  - id: g\n    execution: sequential\n    agents:\n      - name: A\n        model: m\n", "harness.workflows.agents.provider.missing"},
		"agent without model":      {base + "groups:\n  - id: g\n    execution: sequential\n    agents:\n      - name: A\n        provider: p\n", "harness.workflows.agents.model.missing"},
		"unknown dependsOn":        {base + "groups:\n  - id: g\n    execution: sequential\n    depends_on: [ghost]\n" + agent, "harness.workflows.groups.dependsOn.unknown"},
		"self dependsOn":           {base + "groups:\n  - id: g\n    execution: sequential\n    depends_on: [g]\n" + agent, "harness.workflows.groups.dependsOn.self"},
		"bad max_duration":         {base + "config:\n  max_duration: soon\n" + "groups:\n  - id: g\n    execution: sequential\n" + agent, "harness.workflows.config.maxDuration.invalid"},
		"unknown timeout_behavior": {base + "config:\n  timeout_behavior: shrug\n" + "groups:\n  - id: g\n    execution: sequential\n" + agent, "harness.workflows.config.timeoutBehavior.unknown"},
		"unknown transition endpoint": {base + "groups:\n  - id: g\n    execution: sequential\n" + agent +
			"transitions:\n  - from: g\n    to: ghost\n    condition: x\n", "harness.workflows.transitions.to.unknown"},
		"transition without condition": {base + "groups:\n  - id: g\n    execution: sequential\n" + agent +
			"transitions:\n  - from: g\n    to: g\n", "harness.workflows.transitions.condition.missing"},
		"unknown parameter type": {base + "groups:\n  - id: g\n    execution: sequential\n" + agent +
			"parameters:\n  - id: p\n    type: telepathy\n", "harness.workflows.parameters.type.unknown"},
		"choice without choices": {base + "groups:\n  - id: g\n    execution: sequential\n" + agent +
			"parameters:\n  - id: p\n    type: choice\n", "harness.workflows.parameters.choices.missing"},
	}
	for name, tc := range cases {
		ds := compileWFErr(t, manifest, tc.flow)
		if !hasCode(ds, tc.code) {
			t.Errorf("%s: want %s, got %v", name, tc.code, ds)
		}
	}
}

// TestWorkflowGroupCycleRejectedWithPath: a depends_on cycle is refused and the
// diagnostic REPORTS THE PATH, using the one shared three-colour DFS detector.
func TestWorkflowGroupCycleRejectedWithPath(t *testing.T) {
	agent := "    agents:\n      - name: A\n        provider: anthropic\n        model: claude-x\n"
	flow := "id: f\nname: F\nversion: 1.0.0\ngroups:\n" +
		"  - id: a\n    execution: sequential\n    depends_on: [c]\n" + agent +
		"  - id: b\n    execution: sequential\n    depends_on: [a]\n" + agent +
		"  - id: c\n    execution: sequential\n    depends_on: [b]\n" + agent
	ds := compileWFErr(t, wfManifest("", wfRef("flow-1", "./flow.yaml", "1.0.0")), flow)
	msg := diagMessage(ds, "harness.workflows.groups.dependsOn.cycle")
	if msg == "" {
		t.Fatalf("a group dependency cycle must be refused: %v", ds)
	}
	if !strings.Contains(msg, "->") || !strings.Contains(msg, "a") {
		t.Errorf("the cycle diagnostic must report the path, got %q", msg)
	}
	if strings.Contains(msg, "delegation") {
		t.Errorf("the group cycle diagnostic must not describe itself as agent delegation, got %q", msg)
	}
	// A deep acyclic chain is NOT rejected (a proper traversal, not a depth cap).
	acyclic := "id: f\nname: F\nversion: 1.0.0\ngroups:\n" +
		"  - id: a\n    execution: sequential\n" + agent +
		"  - id: b\n    execution: sequential\n    depends_on: [a]\n" + agent +
		"  - id: c\n    execution: sequential\n    depends_on: [b]\n" + agent
	if _, err := compileWF(t, wfManifest("", wfRef("flow-1", "./flow.yaml", "1.0.0")), acyclic); err != nil {
		t.Errorf("an acyclic dependency chain must compile: %v", err)
	}
}

// --- D4: schedules[].target.kind == workflow --------------------------------

// wfScheduleBlock builds a fully-declared schedule whose target is a workflow.
func wfScheduleBlock(workflowID string) string {
	return "schedules:\n  - id: nightly\n    cron: \"0 3 * * *\"\n    timezone: UTC\n" +
		"    overlap: skip\n    misfire: drop\n    retry:\n      policy: none\n" +
		"    concurrency:\n      policy: unlimited\n" +
		"    target:\n      kind: workflow\n      id: " + workflowID + "\n" +
		"    approvalPosture: deny\n    resultSink:\n      kind: discard\n" +
		"    acknowledgeUnenforced:\n      - overlap\n"
}

// TestScheduleWorkflowTargetResolves closes Phase 9b's forward reference: a
// workflow target now RESOLVES against the declared workflows[] ids, carries an
// honest enforcement class and bindings, and an unknown id is a compile error that
// mirrors unknownAgent/unknownProfile.
func TestScheduleWorkflowTargetResolves(t *testing.T) {
	ref := wfRef("flow-1", "./flow.yaml", "1.0.0")
	p := compileWFOK(t, wfManifest("", ref+wfScheduleBlock("flow-1")), wfMinimalFlow)
	s, ok := p.Schedule("nightly")
	if !ok {
		t.Fatalf("schedule not carried")
	}
	tg, ok := s.Dimension(ScheduleDimTarget)
	if !ok {
		t.Fatalf("target dimension not reported")
	}
	if tg.Value != string(ScheduleTargetWorkflow) || tg.Ref != "flow-1" {
		t.Errorf("target = %q/%q, want workflow/flow-1", tg.Value, tg.Ref)
	}
	if tg.Enforcement != EnforcementBindingRequired {
		t.Errorf("a workflow target must report %s, got %q (an empty class would be a plan that claims nothing)",
			EnforcementBindingRequired, tg.Enforcement)
	}
	// Both bindings are honest and both are enumerable on the plan.
	wantOn := map[RuntimeBindingKind]bool{BindingSchedulerTargetDispatcher: false, BindingWorkflowEngine: false}
	for _, b := range tg.Bindings {
		if _, ok := wantOn[b]; ok {
			wantOn[b] = true
		}
	}
	for b, seen := range wantOn {
		if !seen {
			t.Errorf("a workflow target must require %s, got %v", b, tg.Bindings)
		}
	}
	var engineRequiredBy []string
	for _, r := range p.RequiredRuntimeBindings() {
		if r.Binding == BindingWorkflowEngine {
			engineRequiredBy = r.RequiredBy
		}
	}
	if len(engineRequiredBy) == 0 {
		t.Fatalf("workflow.engine must be enumerable on the plan: %+v", p.RequiredRuntimeBindings())
	}
	joined := strings.Join(engineRequiredBy, ",")
	for _, want := range []string{"workflows[0]", "schedules[0].target"} {
		if !strings.Contains(joined, want) {
			t.Errorf("workflow.engine requiredBy must include %q, got %v", want, engineRequiredBy)
		}
	}
	// The Phase 9b scheduler bindings survive the merge unchanged.
	var sawSingleOwner bool
	for _, r := range p.RequiredRuntimeBindings() {
		if r.Binding == BindingSchedulerSingleOwner {
			sawSingleOwner = true
		}
	}
	if !sawSingleOwner {
		t.Errorf("merging workflow bindings must not drop the Phase 9b scheduler bindings: %+v", p.RequiredRuntimeBindings())
	}

	// Unknown id: refused.
	ds := compileWFErr(t, wfManifest("", ref+wfScheduleBlock("ghost")), wfMinimalFlow)
	msg := diagMessage(ds, "harness.schedules.target.unknownWorkflow")
	if msg == "" {
		t.Fatalf("an unknown workflow target must be refused: %v", ds)
	}
	if !strings.Contains(msg, "ghost") || !strings.Contains(msg, "workflows[]") {
		t.Errorf("the refusal must name the id and the section, got %q", msg)
	}

	// An empty target id stays refused with the existing code.
	empty := strings.Replace(wfScheduleBlock("x"), "      id: x\n", "", 1)
	if ds := compileWFErr(t, wfManifest("", ref+empty), wfMinimalFlow); !hasCode(ds, "harness.schedules.target.id.missing") {
		t.Errorf("a workflow target with no id must be refused: %v", ds)
	}
}

// TestWorkflowAccessorsReturnDefensiveCopies: mutating anything a Plan hands out
// must not change the Plan.
func TestWorkflowAccessorsReturnDefensiveCopies(t *testing.T) {
	p := compileWFOK(t, wfManifest(wfKitchenTools, wfHooksBlock+wfRef("flow-1", "./flow.yaml", "1.0.0")), wfKitchenFlow)
	list := p.Workflows()
	if len(list) != 1 {
		t.Fatalf("want 1 workflow, got %d", len(list))
	}
	list[0].ID = "mutated"
	list[0].Bindings[0] = "mutated"
	list[0].Definition.Groups[0].ID = "mutated"
	list[0].Definition.Groups[0].DependsOn = nil
	list[0].Definition.Groups[0].Agents[0].Tools[0] = "mutated"
	list[0].Definition.Groups[0].Agents[0].Capabilities.MaxTokens = nil
	list[0].Definition.Groups[0].Coordinator.Name = "mutated"
	list[0].Definition.Groups[0].Steering.ValidatePlan.Rules[0] = "mutated"
	list[0].Definition.Steering.Rules[0].ID = "mutated"
	list[0].Definition.Parameters[0].Choices[0] = "mutated"
	list[0].Definition.Config.CustomKeys[0] = "mutated"
	list[0].DroppedFields[0].Field = "mutated"
	*list[0].Definition.Config.MaxRetries = 999

	again, ok := p.Workflow("flow-1")
	if !ok {
		t.Fatalf("workflow disappeared")
	}
	eq(t, "id survived mutation", again.ID, "flow-1")
	eq(t, "group id survived", again.Definition.Groups[0].ID, "g1")
	eq(t, "agent tool survived", again.Definition.Groups[0].Agents[0].Tools[0], "Read")
	eq(t, "coordinator survived", again.Definition.Groups[0].Coordinator.Name, "Coordinator")
	eq(t, "validate rule survived", again.Definition.Groups[0].Steering.ValidatePlan.Rules[0], "plan-must-list-files")
	eq(t, "steering rule survived", again.Definition.Steering.Rules[0].ID, "r1")
	eq(t, "choice survived", again.Definition.Parameters[0].Choices[0], "a")
	eq(t, "custom key survived", again.Definition.Config.CustomKeys[0], "ckey")
	eq(t, "dropped field survived", again.DroppedFields[0].Field != "mutated", true)
	eqIntPtr(t, "maxRetries survived", again.Definition.Config.MaxRetries, 2)
	eqSlice(t, "dependsOn survived", again.Definition.Groups[1].DependsOn, []string{"g1"})
	if again.Bindings[0] == "mutated" {
		t.Errorf("bindings must be copied")
	}
}

// --- the DRIFT GUARD --------------------------------------------------------
//
// The adapter's whole value is that it maps a KNOWN, CLOSED schema. If
// internal/mode/yaml.go gains a key and this mirror does not, the harness would
// start REJECTING valid workflow files (fail-closed, but silently stale) — so the
// guard must fail loudly here instead.
//
// It parses the real source rather than reflecting over the types, because
// internal/mode's YAML DTOs are UNEXPORTED: `reflect.TypeOf(mode.yamlModeDefinition{})`
// cannot compile from another package. Parsing the file is exact (it reads the
// actual yaml tags) AND it means this test file does not import internal/mode at
// all, so the harness purity property holds for the test binary too.

// modeYAMLMirrors pairs each internal/mode YAML DTO with the harness mirror type
// that must cover it.
var modeYAMLMirrors = map[string]reflect.Type{
	"yamlModeDefinition":      reflect.TypeOf(wfDefinitionFile{}),
	"yamlModeConfig":          reflect.TypeOf(wfConfig{}),
	"yamlAgentGroup":          reflect.TypeOf(wfGroup{}),
	"yamlAgentDefinition":     reflect.TypeOf(wfAgent{}),
	"yamlCapabilities":        reflect.TypeOf(wfCapabilities{}),
	"yamlCompletionCriteria":  reflect.TypeOf(wfCompletion{}),
	"yamlGroupSteeringConfig": reflect.TypeOf(wfGroupSteering{}),
	"yamlValidationConfig":    reflect.TypeOf(wfValidation{}),
	"yamlTransition":          reflect.TypeOf(wfTransition{}),
	"yamlSteeringConfig":      reflect.TypeOf(wfSteering{}),
	"yamlSteeringRule":        reflect.TypeOf(wfSteeringRule{}),
	"yamlWorkflowParameter":   reflect.TypeOf(wfParameter{}),
	"yamlParameterValidation": reflect.TypeOf(wfParameterValidation{}),
}

// wireKeys returns the wire keys of one struct type in a parsed file: the value of
// the named tag, or the lower-cased field name when the field carries no tag
// (which is what a YAML/JSON decoder would use).
func structWireKeys(t *testing.T, path, tagName string, want map[string]bool) map[string][]string {
	t.Helper()
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, path, nil, 0)
	if err != nil {
		t.Fatalf("the drift guard could not parse %s (%v); it must never pass by failing to read the source", path, err)
	}
	out := map[string][]string{}
	for _, decl := range f.Decls {
		gd, ok := decl.(*ast.GenDecl)
		if !ok || gd.Tok != token.TYPE {
			continue
		}
		for _, spec := range gd.Specs {
			ts, ok := spec.(*ast.TypeSpec)
			if !ok {
				continue
			}
			st, ok := ts.Type.(*ast.StructType)
			if !ok {
				continue
			}
			if want != nil && !want[ts.Name.Name] {
				continue
			}
			keys := []string{}
			for _, fld := range st.Fields.List {
				name := ""
				if len(fld.Names) > 0 {
					name = fld.Names[0].Name
				}
				key := strings.ToLower(name)
				if fld.Tag != nil {
					tag := reflect.StructTag(strings.Trim(fld.Tag.Value, "`"))
					if v := tag.Get(tagName); v != "" {
						key = strings.Split(v, ",")[0]
					}
				}
				if key == "" || key == "-" {
					continue
				}
				keys = append(keys, key)
			}
			out[ts.Name.Name] = keys
		}
	}
	return out
}

// jsonKeysOf returns the json wire keys a mirror type declares.
func jsonKeysOf(rt reflect.Type) map[string]struct{} {
	out := map[string]struct{}{}
	for i := 0; i < rt.NumField(); i++ {
		tag := rt.Field(i).Tag.Get("json")
		if tag == "" || tag == "-" {
			continue
		}
		out[strings.Split(tag, ",")[0]] = struct{}{}
	}
	return out
}

// missingMirrorFields is the guard's PURE core, extracted so its failure behaviour
// can itself be tested (see TestWorkflowDriftGuardFailsOnNewField). It returns one
// human-readable finding per uncovered key, and one per mirror it could not find.
func missingMirrorFields(sources map[string][]string, mirrors map[string]reflect.Type) []string {
	var findings []string
	for name, mirror := range mirrors {
		keys, ok := sources[name]
		if !ok {
			findings = append(findings, name+": struct not found in the parsed source (renamed, moved, or the guard is reading the wrong file)")
			continue
		}
		if len(keys) == 0 {
			findings = append(findings, name+": parsed with ZERO fields, which would make this guard vacuous")
			continue
		}
		have := jsonKeysOf(mirror)
		for _, k := range keys {
			if _, ok := have[k]; !ok {
				findings = append(findings, name+": key "+k+" is not declared by mirror "+mirror.Name())
			}
		}
	}
	for name := range sources {
		if _, ok := mirrors[name]; !ok {
			findings = append(findings, name+": a YAML DTO with no registered harness mirror (a new workflow schema struct?)")
		}
	}
	sortStrings(findings)
	return findings
}

func sortStrings(in []string) {
	for i := 1; i < len(in); i++ {
		for j := i; j > 0 && in[j] < in[j-1]; j-- {
			in[j], in[j-1] = in[j-1], in[j]
		}
	}
}

// TestWorkflowMirrorCoversModeYAMLDTOs is the drift guard proper.
func TestWorkflowMirrorCoversModeYAMLDTOs(t *testing.T) {
	const yamlPath = "../internal/mode/yaml.go"
	// Only the yaml* DTO structs are in scope; the file's other declarations are
	// not decode targets. Passing nil for `want` collects EVERY struct, which is
	// what lets an unregistered new DTO be reported.
	all := structWireKeys(t, yamlPath, "yaml", nil)
	dtos := map[string][]string{}
	for name, keys := range all {
		if strings.HasPrefix(name, "yaml") {
			dtos[name] = keys
		}
	}

	// Anti-silent-pass guards (mirroring the Phase 8c conformance test): the guard
	// must not pass because it found nothing.
	const minStructs, minKeys = 13, 70
	if len(dtos) < minStructs {
		t.Fatalf("the drift guard found only %d yaml DTO structs in %s (minimum %d); it must not pass by reading nothing",
			len(dtos), yamlPath, minStructs)
	}
	total := 0
	for _, keys := range dtos {
		total += len(keys)
	}
	if total < minKeys {
		t.Fatalf("the drift guard found only %d yaml keys (minimum %d); it must not pass by reading nothing", total, minKeys)
	}

	if findings := missingMirrorFields(dtos, modeYAMLMirrors); len(findings) > 0 {
		t.Fatalf("the snake_case mirror has drifted from %s:\n  %s", yamlPath, strings.Join(findings, "\n  "))
	}

	// yamlAgentDefinition embeds internal/agent.ProviderHints by type, so its keys
	// are mirrored by wfProviderHints and are covered separately.
	const hintsPath = "../internal/agent/definition.go"
	hints := structWireKeys(t, hintsPath, "yaml", map[string]bool{"ProviderHints": true})
	if len(hints["ProviderHints"]) < 6 {
		t.Fatalf("the drift guard found only %d ProviderHints keys in %s; it must not pass by reading nothing",
			len(hints["ProviderHints"]), hintsPath)
	}
	if findings := missingMirrorFields(hints, map[string]reflect.Type{"ProviderHints": reflect.TypeOf(wfProviderHints{})}); len(findings) > 0 {
		t.Fatalf("wfProviderHints has drifted from %s:\n  %s", hintsPath, strings.Join(findings, "\n  "))
	}
}

// TestWorkflowDriftGuardFailsOnNewField proves the guard FAILS LOUDLY rather than
// passing vacuously: a phantom new key, a missing struct, a zero-field struct, and
// an unregistered DTO must each produce a finding.
func TestWorkflowDriftGuardFailsOnNewField(t *testing.T) {
	mirrors := map[string]reflect.Type{"yamlSteeringRule": reflect.TypeOf(wfSteeringRule{})}

	// Sanity: the honest input produces NO findings, so a finding below means
	// something real.
	ok := map[string][]string{"yamlSteeringRule": {"id", "condition", "action", "priority", "parameters"}}
	if f := missingMirrorFields(ok, mirrors); len(f) != 0 {
		t.Fatalf("the guard reported a finding for a covered struct: %v", f)
	}

	cases := map[string]struct {
		sources map[string][]string
		mirrors map[string]reflect.Type
		want    string
	}{
		"a new field": {
			map[string][]string{"yamlSteeringRule": {"id", "condition", "action", "priority", "parameters", "phantom_new_field"}},
			mirrors, "phantom_new_field",
		},
		"a renamed struct": {
			map[string][]string{"yamlSteeringRuleRenamed": {"id"}}, mirrors, "struct not found",
		},
		"a zero-field parse": {
			map[string][]string{"yamlSteeringRule": {}}, mirrors, "ZERO fields",
		},
		"an unregistered DTO": {
			map[string][]string{
				"yamlSteeringRule": {"id", "condition", "action", "priority", "parameters"},
				"yamlBrandNewDTO":  {"whatever"},
			},
			mirrors, "no registered harness mirror",
		},
	}
	for name, tc := range cases {
		findings := missingMirrorFields(tc.sources, tc.mirrors)
		if len(findings) == 0 {
			t.Errorf("%s: the guard must report a finding", name)
			continue
		}
		if !strings.Contains(strings.Join(findings, "\n"), tc.want) {
			t.Errorf("%s: findings %v must mention %q", name, findings, tc.want)
		}
	}
}

// TestWorkflowEnforcementClassHasOneDeclaration is the D6 assertion: the class
// Phase 9b introduced still exists, with its original value, now beside its
// siblings in budgets.go.
func TestWorkflowEnforcementClassHasOneDeclaration(t *testing.T) {
	eq(t, "EnforcementBindingRequired value", string(EnforcementBindingRequired), "bindingRequired")
	for _, sibling := range []EnforcementClass{BudgetEnforced, BudgetObserved, BudgetUnavailable} {
		if sibling == EnforcementBindingRequired {
			t.Errorf("EnforcementBindingRequired must stay distinct from %q", sibling)
		}
	}
}
