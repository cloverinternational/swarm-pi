package harness

import (
	"encoding/json"
	"strconv"
	"strings"
	"testing"
)

// Phase 8d — PolicyClass enforcement.
//
// THE HOLE THIS FILE CLOSES. Before this slice, resolveTools validated a
// selected agent.tools id with ONLY KnownCapabilityID(id) and never branched on
// Capability.Class, so the catalog's four policy classes were documentation. A
// probe run against the pre-8d tree recorded, verbatim:
//
//	HOLE OPEN: vault.exec (class NEVER-DEFAULT) compiled with ZERO diagnostics;
//	  tools=[vault.exec] digest=sha256:3df175a860e34004ac676642d6fc72e7...
//	HOLE OPEN: steering.ask_user (class DEFER-DISCOVER) compiled with ZERO
//	  diagnostics; tools=[steering.ask_user] digest=sha256:71ccc1c86ac098e5...
//
// TestPolicyClassEnforcedRegression is that same probe, INVERTED, kept as the
// permanent regression.

// policyManifest builds a minimal manifest selecting the given ids, with an
// optional permissions block appended verbatim.
func policyManifest(tools []string, extraPermissions string) string {
	var b strings.Builder
	b.WriteString("apiVersion: swarm.ai/v1alpha1\n")
	b.WriteString("kind: Harness\n")
	b.WriteString("metadata:\n  name: policy\n")
	b.WriteString("provider:\n  id: anthropic\n  model: claude-x\n")
	b.WriteString("agent:\n  systemPrompt:\n    inline: \"p\"\n  tools:\n")
	for _, id := range tools {
		b.WriteString("    - " + id + "\n")
	}
	if extraPermissions != "" {
		b.WriteString(extraPermissions)
	}
	return b.String()
}

// ackPermissions renders a permissions block acknowledging the given ids.
func ackPermissions(ids ...string) string {
	var b strings.Builder
	b.WriteString("permissions:\n  acknowledgeNeverDefault:\n")
	for _, id := range ids {
		// Quoted so YAML-significant probe values ("*", "**") stay scalars
		// rather than becoming alias markers.
		b.WriteString("    - " + strconv.Quote(id) + "\n")
	}
	return b.String()
}

// --- The inverted hole demonstration (permanent regression) ----------------

func TestPolicyClassEnforcedRegression(t *testing.T) {
	cases := []struct {
		id       string
		class    PolicyClass
		wantCode string
	}{
		{"vault.exec", PolicyNeverDefault, "harness.agent.tools.neverDefault.unacknowledged"},
		{"steering.ask_user", PolicyDeferDiscover, "harness.agent.tools.deferDiscover"},
	}
	for _, tc := range cases {
		c, ok := LookupCapability(tc.id)
		if !ok || c.Class != tc.class {
			t.Fatalf("precondition: %s class = %q (ok=%v), want %q", tc.id, c.Class, ok, tc.class)
		}
		ds := compileErr(t, policyManifest([]string{tc.id}, ""), syntheticYAML)
		if !hasCode(ds, tc.wantCode) {
			t.Errorf("%s (%s): want %s, got %v", tc.id, tc.class, tc.wantCode, ds)
		}
		// The diagnostic must NAME the id and its class so the author knows
		// exactly what was rejected and why.
		var msg string
		for _, d := range ds {
			if d.Code == tc.wantCode {
				msg = d.Message
			}
		}
		if !strings.Contains(msg, tc.id) || !strings.Contains(msg, string(tc.class)) {
			t.Errorf("%s: diagnostic must name the id and its class, got %q", tc.id, msg)
		}
	}
}

// --- D1: DEFER-DISCOVER is a hard reject with NO escape hatch --------------

func TestDeferDiscoverAlwaysRejected(t *testing.T) {
	n := 0
	for _, c := range Catalog() {
		if c.Class != PolicyDeferDiscover {
			continue
		}
		n++
		ds := compileErr(t, policyManifest([]string{c.ID}, ""), syntheticYAML)
		if !hasCode(ds, "harness.agent.tools.deferDiscover") {
			t.Errorf("%s: DEFER-DISCOVER must be rejected, got %v", c.ID, ds)
		}
	}
	if n == 0 {
		t.Fatal("no DEFER-DISCOVER capabilities in the catalog; this test proves nothing")
	}
}

// TestDeferDiscoverHasNoEscapeHatch proves the acknowledgement field cannot be
// repurposed as an override for a DEFER-DISCOVER capability: acknowledging one
// is ITSELF an error (the field accepts NEVER-DEFAULT ids only), and the
// selection stays rejected.
func TestDeferDiscoverHasNoEscapeHatch(t *testing.T) {
	const id = "steering.ask_user"
	raw := policyManifest([]string{id}, ackPermissions(id))
	ds := compileErr(t, raw, syntheticYAML)
	if !hasCode(ds, "harness.permissions.acknowledgeNeverDefault.notNeverDefault") {
		t.Errorf("acknowledging a DEFER-DISCOVER id must itself be rejected, got %v", ds)
	}
	if _, err := CompileBytes([]byte(raw), syntheticYAML); err == nil {
		t.Fatal("DEFER-DISCOVER became selectable via the acknowledgement field")
	}
}

// --- D2: NEVER-DEFAULT requires an explicit, per-id, named posture ---------

func TestNeverDefaultRequiresAcknowledgement(t *testing.T) {
	n := 0
	for _, c := range Catalog() {
		if c.Class != PolicyNeverDefault {
			continue
		}
		n++
		// Without the acknowledgement: rejected, naming the exact field.
		ds := compileErr(t, policyManifest([]string{c.ID}, ""), syntheticYAML)
		if !hasCode(ds, "harness.agent.tools.neverDefault.unacknowledged") {
			t.Errorf("%s: NEVER-DEFAULT must be rejected without acknowledgement, got %v", c.ID, ds)
			continue
		}
		for _, d := range ds {
			if d.Code == "harness.agent.tools.neverDefault.unacknowledged" &&
				!strings.Contains(d.Message, "permissions.acknowledgeNeverDefault") {
				t.Errorf("%s: diagnostic must name the exact field the author must add, got %q", c.ID, d.Message)
			}
		}

		// With the acknowledgement: accepted, UNLESS a separate, orthogonal
		// prerequisite (credential binding) also applies.
		perms := ackPermissions(c.ID)
		p, err := CompileBytes([]byte(policyManifest([]string{c.ID}, perms)), syntheticYAML)
		if c.CredentialBound == QYes {
			if err == nil {
				t.Errorf("%s: credential-bound capability must still fail without a credential", c.ID)
			}
			continue
		}
		if err != nil {
			t.Errorf("%s: acknowledged NEVER-DEFAULT must compile, got %v", c.ID, err)
			continue
		}
		if got := p.Tools(); len(got) != 1 || got[0] != c.ID {
			t.Errorf("%s: Tools() = %v", c.ID, got)
		}
	}
	if n == 0 {
		t.Fatal("no NEVER-DEFAULT capabilities in the catalog; this test proves nothing")
	}
}

// TestAcknowledgementIsPerIDNeverBlanket proves the posture cannot be satisfied
// wholesale: acknowledging one NEVER-DEFAULT id does NOT authorize a second
// one, and there is no wildcard/blanket form.
func TestAcknowledgementIsPerIDNeverBlanket(t *testing.T) {
	// forge.write acknowledged; forge.edit selected but NOT acknowledged.
	raw := policyManifest([]string{"forge.write", "forge.edit"}, ackPermissions("forge.write"))
	ds := compileErr(t, raw, syntheticYAML)
	if !hasCode(ds, "harness.agent.tools.neverDefault.unacknowledged") {
		t.Errorf("acknowledging one id must not authorize a sibling, got %v", ds)
	}
	// Both acknowledged: accepted.
	if _, err := CompileBytes([]byte(policyManifest(
		[]string{"forge.write", "forge.edit"},
		ackPermissions("forge.write", "forge.edit"))), syntheticYAML); err != nil {
		t.Fatalf("per-id acknowledgement of both must compile: %v", err)
	}
	// A wildcard is not a capability id and is rejected as unknown; there is no
	// blanket boolean in the schema at all (strict decode rejects unknown keys).
	for _, blanket := range []string{"*", "**", "all", "NEVER-DEFAULT"} {
		ds := compileErr(t, policyManifest([]string{"forge.write"}, ackPermissions(blanket)), syntheticYAML)
		if !hasCode(ds, "harness.permissions.acknowledgeNeverDefault.unknown") {
			t.Errorf("wildcard %q must be rejected as an unknown capability id, got %v", blanket, ds)
		}
	}
	blanketBool := policyManifest([]string{"forge.write"}, "permissions:\n  allowNeverDefault: true\n")
	if _, err := CompileBytes([]byte(blanketBool), syntheticYAML); err == nil {
		t.Fatal("a blanket allow-all key must not exist in the schema")
	}
}

// TestAcknowledgementFieldIsHonest covers the three rules that stop the field
// becoming a junk drawer: no dangling entries, no non-NEVER-DEFAULT entries, no
// unknown/duplicate/empty entries.
func TestAcknowledgementFieldIsHonest(t *testing.T) {
	cases := []struct {
		name     string
		tools    []string
		perms    string
		wantCode string
	}{
		{
			name: "dangling acknowledgement (not selected anywhere)", tools: []string{"forge.read"},
			perms: ackPermissions("vault.exec"), wantCode: "harness.permissions.acknowledgeNeverDefault.unused",
		},
		{
			name: "acknowledged id is KEEP, not NEVER-DEFAULT", tools: []string{"forge.read"},
			perms: ackPermissions("forge.read"), wantCode: "harness.permissions.acknowledgeNeverDefault.notNeverDefault",
		},
		{
			name: "acknowledged id is OPT-IN, not NEVER-DEFAULT", tools: []string{"builtin.bash"},
			perms: ackPermissions("builtin.bash"), wantCode: "harness.permissions.acknowledgeNeverDefault.notNeverDefault",
		},
		{
			name: "acknowledged id is not in the catalog", tools: []string{"forge.read"},
			perms: ackPermissions("nope.nope"), wantCode: "harness.permissions.acknowledgeNeverDefault.unknown",
		},
		{
			name: "duplicate acknowledgement", tools: []string{"forge.write"},
			perms: ackPermissions("forge.write", "forge.write"), wantCode: "harness.permissions.acknowledgeNeverDefault.duplicate",
		},
		{
			name: "empty acknowledgement entry", tools: []string{"forge.write"},
			perms: "permissions:\n  acknowledgeNeverDefault:\n    - \"\"\n", wantCode: "harness.permissions.acknowledgeNeverDefault.empty",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ds := compileErr(t, policyManifest(tc.tools, tc.perms), syntheticYAML)
			if !hasCode(ds, tc.wantCode) {
				t.Errorf("want %s, got %v", tc.wantCode, ds)
			}
		})
	}
}

// TestAcknowledgementSatisfiedBySubagentSelection: an acknowledgement is "used"
// when ANY agent selects it, so a document-scoped acknowledgement covering only
// a subagent's selection is valid (and is NOT reported as dangling).
func TestAcknowledgementSatisfiedBySubagentSelection(t *testing.T) {
	raw := "apiVersion: swarm.ai/v1alpha1\n" +
		"kind: Harness\n" +
		"metadata:\n  name: policy\n" +
		"provider:\n  id: anthropic\n  model: claude-x\n" +
		"agent:\n  systemPrompt:\n    inline: \"p\"\n  tools:\n    - forge.read\n" +
		ackPermissions("forge.write") +
		"agents:\n" +
		"  - id: legacy\n" +
		"    systemPrompt:\n      inline: \"w\"\n" +
		"    tools:\n      - forge.write\n"
	p, err := CompileBytes([]byte(raw), syntheticYAML)
	if err != nil {
		t.Fatalf("subagent selection must satisfy the acknowledgement: %v", err)
	}
	subs := p.Subagents()
	if len(subs) != 1 || len(subs[0].Tools) != 1 || subs[0].Tools[0] != "forge.write" {
		t.Fatalf("subagent tools = %+v", subs)
	}
}

// --- Subagents are gated IDENTICALLY to the primary agent ------------------

func TestSubagentToolsGatedIdenticallyToPrimary(t *testing.T) {
	mk := func(id string) string {
		return "apiVersion: swarm.ai/v1alpha1\n" +
			"kind: Harness\n" +
			"metadata:\n  name: policy\n" +
			"provider:\n  id: anthropic\n  model: claude-x\n" +
			"agent:\n  systemPrompt:\n    inline: \"p\"\n  tools:\n    - forge.read\n" +
			"agents:\n" +
			"  - id: sub\n" +
			"    systemPrompt:\n      inline: \"s\"\n" +
			"    tools:\n      - " + id + "\n"
	}
	cases := []struct{ id, wantCode string }{
		{"steering.ask_user", "harness.agents.tools.deferDiscover"},
		{"vault.exec", "harness.agents.tools.neverDefault.unacknowledged"},
		{"forge.write", "harness.agents.tools.neverDefault.unacknowledged"},
	}
	for _, tc := range cases {
		ds := compileErr(t, mk(tc.id), syntheticYAML)
		if !hasCode(ds, tc.wantCode) {
			t.Errorf("subagent selecting %q: want %s, got %v", tc.id, tc.wantCode, ds)
		}
		// The field path must point at the subagent, not at agent.tools.
		for _, d := range ds {
			if d.Code == tc.wantCode && !strings.HasPrefix(d.FieldPath, "agents[0].tools") {
				t.Errorf("subagent diagnostic field path = %q, want agents[0].tools...", d.FieldPath)
			}
		}
	}
}

// --- D3: OPT-IN keeps working; stated prerequisites are checked ------------

func TestOptInExplicitSelectionStillWorks(t *testing.T) {
	// OPT-IN capabilities with NO unconditional prerequisite compile exactly as
	// before, with no new ceremony.
	ids := []string{"builtin.bash", "ii.task_manage", "skills.skill", "code.run_code",
		"meta.tool_search", "debug.debug_logs", "interactive.ask_user_question",
		"compaction.todo_update"}
	p, err := CompileBytes([]byte(policyManifest(ids, "")), syntheticYAML)
	if err != nil {
		t.Fatalf("OPT-IN explicit selection must still compile: %v", err)
	}
	if len(p.Tools()) != len(ids) {
		t.Fatalf("Tools() = %v, want %v", p.Tools(), ids)
	}
}

// TestOptInCredentialPrerequisite pins the ONE prerequisite that v1alpha1 can
// actually express: a capability the baseline marks unconditionally
// credential-bound (CredentialBound == "yes") cannot be selected by a manifest
// that declares no credential reference at all. This is a NECESSARY-condition
// check and is documented as such — see documentDeclaresCredential.
func TestOptInCredentialPrerequisite(t *testing.T) {
	const id = "web.websearch"
	c, _ := LookupCapability(id)
	if c.Class != PolicyOptIn || c.CredentialBound != QYes {
		t.Fatalf("precondition: %s is %s/cred=%s", id, c.Class, c.CredentialBound)
	}
	ds := compileErr(t, policyManifest([]string{id}, ""), syntheticYAML)
	if !hasCode(ds, "harness.agent.tools.credentialUnbound") {
		t.Fatalf("want credentialUnbound, got %v", ds)
	}

	// provider.credential satisfies it.
	withProviderCred := "apiVersion: swarm.ai/v1alpha1\n" +
		"kind: Harness\n" +
		"metadata:\n  name: policy\n" +
		"provider:\n  id: anthropic\n  model: claude-x\n  credential:\n    inline: k\n" +
		"agent:\n  systemPrompt:\n    inline: \"p\"\n  tools:\n    - " + id + "\n"
	if _, err := CompileBytes([]byte(withProviderCred), syntheticYAML); err != nil {
		t.Fatalf("provider.credential must satisfy the prerequisite: %v", err)
	}

	// A profiles[].credential satisfies it too.
	t.Setenv("HARNESS_8D_TEST_KEY", "value")
	withProfileCred := "apiVersion: swarm.ai/v1alpha1\n" +
		"kind: Harness\n" +
		"metadata:\n  name: policy\n" +
		"provider:\n  id: anthropic\n  model: claude-x\n" +
		"agent:\n  systemPrompt:\n    inline: \"p\"\n  tools:\n    - " + id + "\n" +
		"profiles:\n  - id: alt\n    provider: anthropic\n    model: claude-y\n" +
		"    credential:\n      env: HARNESS_8D_TEST_KEY\n"
	if _, err := CompileBytes([]byte(withProfileCred), syntheticYAML); err != nil {
		t.Fatalf("profiles[].credential must satisfy the prerequisite: %v", err)
	}
}

// TestConditionalQualifiersAreNotTreatedAsPrerequisites: "often"/"maybe"/
// "partly" are deliberately CONDITIONAL cells in PARITY_BASELINE. Rejecting on
// them would assert a requirement the baseline does not make, so they must not
// gate a selection.
func TestConditionalQualifiersAreNotTreatedAsPrerequisites(t *testing.T) {
	for _, id := range []string{"ii.task_manage", "mcp.prompts", "debug.debug_logs"} {
		c, ok := LookupCapability(id)
		if !ok {
			t.Fatalf("missing %s", id)
		}
		if c.CredentialBound == QYes || c.CredentialBound == QNo {
			// mcp.prompts is "often"; the others are "no"/"often". Only the
			// conditional ones are interesting, but a "no" must also pass.
			_ = c
		}
		if _, err := CompileBytes([]byte(policyManifest([]string{id}, "")), syntheticYAML); err != nil {
			t.Errorf("%s (cred=%s) must not be gated by a conditional qualifier: %v", id, c.CredentialBound, err)
		}
	}
}

// TestHostBoundIsNotFakeChecked documents the D3 gap explicitly: v1alpha1 has
// NO field in which a manifest declares a host binding, so HostBound is NOT
// compile-time checkable and is deliberately NOT checked. This test PINS that
// deliberate non-check so a later slice cannot quietly add a fake one that
// always passes (or always fails) for unrelated reasons.
func TestHostBoundIsNotFakeChecked(t *testing.T) {
	const id = "builtin.bash" // OPT-IN, HostBound == "yes", CredentialBound == "no"
	c, _ := LookupCapability(id)
	if c.HostBound != QYes || c.CredentialBound != QNo {
		t.Fatalf("precondition: %s host=%s cred=%s", id, c.HostBound, c.CredentialBound)
	}
	if _, err := CompileBytes([]byte(policyManifest([]string{id}, "")), syntheticYAML); err != nil {
		t.Fatalf("host-bound prerequisite is a documented RUNTIME concern and must not be "+
			"rejected at compile time by a check the manifest cannot satisfy: %v", err)
	}
}

// --- D4: KEEP is unchanged -------------------------------------------------

func TestKeepCapabilitiesUnchanged(t *testing.T) {
	var keep []string
	for _, c := range Catalog() {
		if c.Class == PolicyKeep {
			keep = append(keep, c.ID)
		}
	}
	if len(keep) == 0 {
		t.Fatal("no KEEP capabilities; this test proves nothing")
	}
	// Every KEEP id, selected together, with NO new ceremony at all.
	p, err := CompileBytes([]byte(policyManifest(keep, "")), syntheticYAML)
	if err != nil {
		t.Fatalf("KEEP selection must be unchanged (no new ceremony): %v", err)
	}
	if len(p.Tools()) != len(keep) {
		t.Fatalf("Tools() = %v, want %v", p.Tools(), keep)
	}
	// Mutation KEEP capabilities specifically: still selectable with the
	// default permissions posture, exactly as before this slice. Phase 8d did
	// not tighten KEEP.
	for _, id := range []string{"forge.apply_patch", "forge.undo"} {
		if _, err := CompileBytes([]byte(policyManifest([]string{id}, "")), syntheticYAML); err != nil {
			t.Errorf("KEEP mutation capability %s must remain selectable unchanged: %v", id, err)
		}
	}
}

// --- Approvals and the acknowledgement are ORTHOGONAL gates ----------------

// TestAcknowledgementDoesNotSubstituteForPermissionPosture: acknowledging a
// NEVER-DEFAULT capability grants EXPOSURE only. It must not imply, set, or
// relax any permission posture (approvalMode / allowMutation / workspace
// boundary) — those remain exactly what the manifest declares.
func TestAcknowledgementDoesNotSubstituteForPermissionPosture(t *testing.T) {
	// forge.write is NEVER-DEFAULT *and* a mutation adapter.
	raw := policyManifest([]string{"forge.write"}, ackPermissions("forge.write"))
	p, err := CompileBytes([]byte(raw), syntheticYAML)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	perms := p.Permissions()
	if perms.AllowMutation {
		t.Error("acknowledging a NEVER-DEFAULT mutation capability must NOT set allowMutation")
	}
	if perms.ApprovalMode != "interactive" {
		t.Errorf("approvalMode = %q; the acknowledgement must not relax it (want the "+
			"unchanged non-yolo default)", perms.ApprovalMode)
	}
	if perms.WorkspaceBoundary != nil {
		t.Error("acknowledging a capability must not set workspaceBoundary")
	}

	// And conversely: declaring the permission posture does NOT acknowledge
	// anything. allowMutation:true + approvalMode:yolo still cannot select an
	// unacknowledged NEVER-DEFAULT capability.
	permissive := policyManifest([]string{"forge.write"},
		"permissions:\n  approvalMode: yolo\n  allowMutation: true\n")
	ds := compileErr(t, permissive, syntheticYAML)
	if !hasCode(ds, "harness.agent.tools.neverDefault.unacknowledged") {
		t.Errorf("a permissive approval posture must not substitute for the per-id "+
			"acknowledgement, got %v", ds)
	}
}

// --- Digest backward compatibility ----------------------------------------

// TestDigestBackwardCompatiblePre8d pins EXACT plan digests captured from a
// throwaway `git worktree` at HEAD (a792099c, pre-8d) for manifests that select
// no NEVER-DEFAULT capability and omit the new field. They must be BYTE
// IDENTICAL after this slice: permissions.acknowledgeNeverDefault is
// `omitempty`, so an absent field contributes nothing to the canonical JSON the
// digest hashes.
// NOTE on the two re-pinned digests below (optin-keep-mix-with-credential,
// subagents-8a): their MANIFEST TEXT had to change because it named
// forge.unified_grep / forge.grep / forge.semantic_grep, capabilities that were
// REMOVED when code search moved to the shell. Their digests were therefore
// re-baselined, not historically preserved — they no longer prove pre-8d
// backward compatibility. The "minimal-keep" and "zero-tools" cases are
// UNCHANGED and still carry their original pre-8d pins, so the digest
// ALGORITHM remains covered by this test.
func TestDigestBackwardCompatiblePre8d(t *testing.T) {
	const pinSrc = "/synthetic/dir/harness.yaml"
	cases := []struct{ name, want, raw string }{
		{"minimal-keep", "sha256:87471c6903ffb896e5dba2de7a0a2274dee6ecca295bf7eff577455bd52bc64c", `apiVersion: swarm.ai/v1alpha1
kind: Harness
metadata:
  name: pin-minimal
provider:
  id: anthropic
  model: claude-x
agent:
  systemPrompt:
    inline: "pinned prompt"
  tools:
    - forge.read
    - forge.apply_patch
`},
		{"zero-tools", "sha256:a4456e7f44951fe3c7b58e4930d457adf66c8f370ad9accdb2b219d3f203ffed", `apiVersion: swarm.ai/v1alpha1
kind: Harness
metadata:
  name: pin-zero
provider:
  id: anthropic
  model: claude-x
agent:
  systemPrompt:
    inline: "pinned prompt"
  tools: []
`},
		{"optin-keep-mix-with-credential", "sha256:106960afaaed1c0df0d683d7020044ff1225256a6efd5f93e1110d65c2b703f8", `apiVersion: swarm.ai/v1alpha1
kind: Harness
metadata:
  name: pin-optin
provider:
  id: anthropic
  model: claude-x
  credential:
    inline: pinned-secret
agent:
  systemPrompt:
    inline: "pinned prompt"
  tools:
    - forge.read
    - forge.apply_patch
    - builtin.bash
    - web.websearch
    - code.run_code
    - meta.tool_search
permissions:
  approvalMode: readonly
  allowMutation: true
`},
		{"subagents-8a", "sha256:b005e739d2900e6b417cce29adbc5a77811d7d4b08c9e81b8e9561cd2cecc4f7", `apiVersion: swarm.ai/v1alpha1
kind: Harness
metadata:
  name: pin-agents
provider:
  id: anthropic
  model: claude-x
agent:
  systemPrompt:
    inline: "pinned prompt"
  tools:
    - forge.read
agents:
  - id: worker
    systemPrompt:
      inline: "worker prompt"
    tools:
      - forge.apply_patch
      - forge.undo
`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p, err := CompileBytes([]byte(tc.raw), pinSrc)
			if err != nil {
				t.Fatalf("compile: %v", err)
			}
			if p.Digest() != tc.want {
				t.Fatalf("digest changed vs pre-8d:\n got  %s\n want %s", p.Digest(), tc.want)
			}
		})
	}
}

// TestExplainRedactionUnchanged: the new field is a list of non-secret catalog
// ids (exactly the category `agent.tools` already is). It must be ABSENT from
// the report when unset, and never carry anything else.
func TestExplainRedactionUnchanged(t *testing.T) {
	// Absent when unset.
	p, err := CompileBytes([]byte(policyManifest([]string{"forge.read"}, "")), syntheticYAML)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	b, err := p.ExplainJSON()
	if err != nil {
		t.Fatalf("ExplainJSON: %v", err)
	}
	if strings.Contains(string(b), "acknowledgeNeverDefault") {
		t.Errorf("unset acknowledgement must not appear in the report:\n%s", b)
	}
	if len(p.Permissions().AcknowledgeNeverDefault) != 0 {
		t.Error("unset acknowledgement must resolve to an empty list")
	}

	// Present and exact when set — ids only, no prompt/secret material.
	p2, err := CompileBytes([]byte(policyManifest([]string{"forge.write"}, ackPermissions("forge.write"))), syntheticYAML)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	b2, err := p2.ExplainJSON()
	if err != nil {
		t.Fatalf("ExplainJSON: %v", err)
	}
	var rep map[string]any
	if err := json.Unmarshal(b2, &rep); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	perms, _ := rep["permissions"].(map[string]any)
	got, _ := perms["acknowledgeNeverDefault"].([]any)
	if len(got) != 1 || got[0] != "forge.write" {
		t.Fatalf("acknowledgeNeverDefault in report = %v, want [forge.write]", got)
	}
	if strings.Contains(string(b2), "\"p\"") && strings.Contains(string(b2), "systemPromptHash") {
		// The inline prompt text is the single char "p"; assert the report
		// carries only its hash, never the text under a prompt key.
		if strings.Contains(string(b2), "\"systemPrompt\":") {
			t.Error("prompt text leaked into the redacted report")
		}
	}
}

// TestPlanPermissionsAreCopied: Plan is immutable, so mutating the slice a
// caller received must not affect the plan (nor a later Explain/digest).
func TestPlanPermissionsAreCopied(t *testing.T) {
	p, err := CompileBytes([]byte(policyManifest([]string{"forge.write"}, ackPermissions("forge.write"))), syntheticYAML)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	got := p.Permissions().AcknowledgeNeverDefault
	if len(got) != 1 {
		t.Fatalf("acknowledgement = %v", got)
	}
	got[0] = "MUTATED"
	if again := p.Permissions().AcknowledgeNeverDefault; again[0] != "forge.write" {
		t.Fatalf("Plan permissions are not defensively copied: %v", again)
	}
}
