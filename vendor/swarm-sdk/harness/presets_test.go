package harness

import (
	"sort"
	"strings"
	"testing"
)

// compatManifest builds a minimal manifest for preset tests.
//
//   - compatibility: the `compatibility:` value, or "" to omit the key.
//   - withCredential: when true, declares `provider.credential.env: <envVar>`
//     (the caller must t.Setenv envVar before compiling).
//   - toolsLine: raw, already-indented YAML to splice in place of `agent.tools`
//     ("" omits the key entirely — ToolSelection.Specified=false).
//   - extra: raw YAML appended after the `agent:` block (e.g. a `permissions:`
//     block from ackPermissions, reused from policy_test.go).
func compatManifest(compatibility string, withCredential bool, envVar, toolsLine, extra string) string {
	var b strings.Builder
	b.WriteString("apiVersion: swarm.ai/v1alpha1\n")
	b.WriteString("kind: Harness\n")
	b.WriteString("metadata:\n  name: presets\n")
	b.WriteString("provider:\n  id: anthropic\n  model: claude-x\n")
	if withCredential {
		b.WriteString("  credential:\n    env: " + envVar + "\n")
	}
	if compatibility != "" {
		b.WriteString("compatibility: " + compatibility + "\n")
	}
	b.WriteString("agent:\n  systemPrompt:\n    inline: \"p\"\n")
	b.WriteString(toolsLine)
	b.WriteString(extra)
	return b.String()
}

// tuiV1CredEnvVar names the env var used to satisfy tui-v1's credential-bound
// gate (D1: web.websearch and every vault.* entry are CredentialBound=yes).
const tuiV1CredEnvVar = "HARNESS_PRESETS_TEST_TUI_V1_CRED"

func tuiV1FullPostureManifest(t *testing.T) string {
	t.Helper()
	t.Setenv(tuiV1CredEnvVar, "irrelevant-test-value")
	return compatManifest("tui-v1", true, tuiV1CredEnvVar, "", ackPermissions("vault.exec", "vault.add"))
}

// --- D4: omitted compatibility is byte-identical to pre-10c -----------------

// pre10cDigest was captured by compiling the manifest below with harness
// package code at commit 4f66969d (Phase 10b, HEAD immediately before this
// slice), in a detached `git worktree add --detach 4f66969d`, compiled with
// sourcePath EXACTLY syntheticYAML below (workspace/storage default from
// manifestDir(sourcePath), so the pinned digest is sourcePath-sensitive — see
// the Phase 10c SUMMARY for the exact probe command). Worktree removed
// immediately after capture.
const pre10cDigest = "sha256:6a0dc307359896ecc9b12e279d745f37db998c275b150aefd655b0329fab7ca6"

const pre10cPinnedManifest = "apiVersion: swarm.ai/v1alpha1\n" +
	"kind: Harness\n" +
	"metadata:\n  name: presetstest\n" +
	"provider:\n  id: anthropic\n  model: claude-x\n" +
	"agent:\n  systemPrompt:\n    inline: \"hi\"\n  tools: []\n"

func TestCompatibilityOmittedDigestBackwardCompatible(t *testing.T) {
	p := compileOK(t, pre10cPinnedManifest)
	if p.Digest() != pre10cDigest {
		t.Fatalf("digest changed for a manifest without compatibility:\n got  %s\n want %s", p.Digest(), pre10cDigest)
	}
	if p.Compatibility() != "" {
		t.Errorf("Compatibility() = %q, want empty", p.Compatibility())
	}
	if got := p.CompatibilityShortfall(); got != nil {
		t.Errorf("CompatibilityShortfall() = %v, want nil", got)
	}
}

func TestCompatibilityOmittedExplainHasNoNewKeys(t *testing.T) {
	p := compileOK(t, pre10cPinnedManifest)
	js, err := p.ExplainJSON()
	if err != nil {
		t.Fatalf("ExplainJSON: %v", err)
	}
	for _, key := range []string{"\"compatibility\"", "\"compatibilityShortfall\""} {
		if strings.Contains(string(js), key) {
			t.Errorf("unused compatibility must be omitted from ExplainJSON, found %s in %s", key, js)
		}
	}
}

// --- unknown preset name is rejected ----------------------------------------

func TestUnknownCompatibilityRejected(t *testing.T) {
	raw := compatManifest("bogus-preset", false, "", "", "")
	ds := compileErr(t, raw, syntheticYAML)
	if !hasCode(ds, "harness.compatibility.unknown") {
		t.Fatalf("expected harness.compatibility.unknown, got %v", ds)
	}
	msg := diagMessage(ds, "harness.compatibility.unknown")
	if !strings.Contains(msg, "minimal") || !strings.Contains(msg, "tui-v1") {
		t.Errorf("diagnostic must name known presets, got %q", msg)
	}
}

// --- D5: preset + explicit agent.tools is rejected outright ----------------

func TestCompatibilityConflictsWithExplicitToolsEmpty(t *testing.T) {
	raw := compatManifest("tui-v1", false, "", "  tools: []\n", "")
	ds := compileErr(t, raw, syntheticYAML)
	if !hasCode(ds, "harness.compatibility.conflictsWithTools") {
		t.Fatalf("expected harness.compatibility.conflictsWithTools for explicit empty tools, got %v", ds)
	}
}

func TestCompatibilityConflictsWithExplicitToolsNonEmpty(t *testing.T) {
	raw := compatManifest("minimal", false, "", "  tools:\n    - forge.read\n", "")
	ds := compileErr(t, raw, syntheticYAML)
	if !hasCode(ds, "harness.compatibility.conflictsWithTools") {
		t.Fatalf("expected harness.compatibility.conflictsWithTools for explicit non-empty tools, got %v", ds)
	}
}

// --- minimal preset --------------------------------------------------------

func TestMinimalPresetExpands(t *testing.T) {
	raw := compatManifest("minimal", false, "", "", "")
	p := compileOK(t, raw)
	if p.Compatibility() != "minimal" {
		t.Fatalf("Compatibility() = %q, want minimal", p.Compatibility())
	}
	got := p.Tools()
	want := []string{"forge.read", "forge.apply_patch", "forge.undo"}
	if !stringSlicesEqual(got, want) {
		t.Fatalf("minimal tools = %v, want %v", got, want)
	}
	if sf := p.CompatibilityShortfall(); sf != nil {
		t.Errorf("minimal must have no shortfall, got %v", sf)
	}
}

func TestMinimalPresetRequiresNoPosture(t *testing.T) {
	// minimal must compile with NO credential and NO acknowledgement — every
	// entry is KEEP-class with all-no qualifiers.
	raw := compatManifest("minimal", false, "", "", "")
	if _, err := CompileBytes([]byte(raw), syntheticYAML); err != nil {
		t.Fatalf("minimal should require no extra posture, got error: %v", err)
	}
}

// --- tui-v1 preset -----------------------------------------------------------

func TestTuiV1PresetExpandsWithFullPosture(t *testing.T) {
	raw := tuiV1FullPostureManifest(t)
	p := compileOK(t, raw)
	if p.Compatibility() != "tui-v1" {
		t.Fatalf("Compatibility() = %q, want tui-v1", p.Compatibility())
	}
	got := p.Tools()
	want := []string{
		"forge.read", "forge.apply_patch", "forge.undo",
		"builtin.bash", "ii.task_manage",
		"vault.exec", "vault.add", "vault.list", "vault.two_person_status", "vault.approve",
	}
	if !stringSlicesEqual(got, want) {
		t.Fatalf("tui-v1 tools = %v, want %v", got, want)
	}
	sf := p.CompatibilityShortfall()
	if len(sf) != len(tuiV1Shortfall) {
		t.Fatalf("shortfall len = %d, want %d", len(sf), len(tuiV1Shortfall))
	}
}

func TestTuiV1RequiresCredentialDeclaration(t *testing.T) {
	// ack present, but NO credential declared anywhere in the document: every
	// credential-bound entry (web.websearch, all five vault.*) must be
	// rejected — the preset gets NO exemption from the same D1 gate a manual
	// selection would hit.
	raw := compatManifest("tui-v1", false, "", "", ackPermissions("vault.exec", "vault.add"))
	ds := compileErr(t, raw, syntheticYAML)
	if !hasCode(ds, "harness.agent.tools.credentialUnbound") {
		t.Fatalf("expected credentialUnbound, got %v", ds)
	}
}

func TestTuiV1RequiresNeverDefaultAcknowledgement(t *testing.T) {
	t.Setenv(tuiV1CredEnvVar, "irrelevant-test-value")
	raw := compatManifest("tui-v1", true, tuiV1CredEnvVar, "", "")
	ds := compileErr(t, raw, syntheticYAML)
	if !hasCode(ds, "harness.agent.tools.neverDefault.unacknowledged") {
		t.Fatalf("expected neverDefault.unacknowledged, got %v", ds)
	}
	// Both vault.exec and vault.add must be named — the preset does not
	// silently include either without demanding the same acknowledgement a
	// manual selection would (D1's "must not silently include it").
	var names []string
	for _, d := range ds {
		if d.Code == "harness.agent.tools.neverDefault.unacknowledged" {
			names = append(names, d.Message)
		}
	}
	joined := strings.Join(names, "\n")
	if !strings.Contains(joined, "vault.exec") || !strings.Contains(joined, "vault.add") {
		t.Errorf("expected both vault.exec and vault.add to require acknowledgement, got:\n%s", joined)
	}
}

func TestTuiV1DigestDeterministic(t *testing.T) {
	raw := tuiV1FullPostureManifest(t)
	p1 := compileOK(t, raw)
	p2 := compileOK(t, raw)
	if p1.Digest() != p2.Digest() {
		t.Errorf("nondeterministic digest: %s vs %s", p1.Digest(), p2.Digest())
	}
}

// --- class-gate invariants over the preset data itself ----------------------

// TestPresetsNeverYieldDeferDiscover proves, at the DATA level (no compile
// needed), that neither preset's static ID list contains a DEFER-DISCOVER
// capability — the class gate a preset can never bypass (D1).
func TestPresetsNeverYieldDeferDiscover(t *testing.T) {
	for _, name := range KnownPresetNames() {
		spec, ok := LookupPreset(name)
		if !ok {
			t.Fatalf("KnownPresetNames returned %q but LookupPreset failed", name)
		}
		for _, id := range spec.IDs {
			c, known := LookupCapability(id)
			if !known {
				t.Errorf("preset %s: id %q is not a real catalog entry", name, id)
				continue
			}
			if c.Class == PolicyDeferDiscover {
				t.Errorf("preset %s: id %q is DEFER-DISCOVER; a preset must never select it", name, id)
			}
		}
	}
}

// TestMinimalIsStrictSubsetOfTuiV1 asserts the documented D4 relation.
func TestMinimalIsStrictSubsetOfTuiV1(t *testing.T) {
	minimal, _ := LookupPreset("minimal")
	tuiV1, _ := LookupPreset("tui-v1")
	tuiSet := make(map[string]struct{}, len(tuiV1.IDs))
	for _, id := range tuiV1.IDs {
		tuiSet[id] = struct{}{}
	}
	for _, id := range minimal.IDs {
		if _, ok := tuiSet[id]; !ok {
			t.Errorf("minimal id %q is not in tui-v1; minimal must be a strict subset", id)
		}
	}
	if len(minimal.IDs) >= len(tuiV1.IDs) {
		t.Errorf("minimal (%d ids) must be strictly smaller than tui-v1 (%d ids)", len(minimal.IDs), len(tuiV1.IDs))
	}
}

// TestShortfallEntriesAreConsistentWithCatalogAndSelection cross-checks every
// tui-v1 shortfall entry against the catalog and tui-v1's own selected set, so
// the three D3 groups cannot silently drift from the data they describe.
func TestShortfallEntriesAreConsistentWithCatalogAndSelection(t *testing.T) {
	tuiV1, _ := LookupPreset("tui-v1")
	selected := make(map[string]struct{}, len(tuiV1.IDs))
	for _, id := range tuiV1.IDs {
		selected[id] = struct{}{}
	}
	seen := make(map[string]struct{})
	for _, e := range tuiV1.Shortfall {
		if e.Capability == "" {
			t.Errorf("shortfall entry has empty Capability: %+v", e)
		}
		if e.Reason == "" {
			t.Errorf("shortfall entry %q has empty Reason", e.Capability)
		}
		key := string(e.Cause) + ":" + e.Capability
		if _, dup := seen[key]; dup {
			t.Errorf("duplicate shortfall entry for %s (%s)", e.Capability, e.Cause)
		}
		seen[key] = struct{}{}

		if _, isSelected := selected[e.Capability]; isSelected {
			t.Errorf("shortfall entry %q is ALSO in tui-v1's selected set; a capability cannot be both included and a shortfall", e.Capability)
		}

		c, known := LookupCapability(e.Capability)
		switch e.Cause {
		case ShortfallDeferDiscover:
			if !known {
				t.Errorf("defer-discover shortfall %q is not a real catalog id", e.Capability)
			} else if c.Class != PolicyDeferDiscover {
				t.Errorf("defer-discover shortfall %q has catalog class %s, want %s", e.Capability, c.Class, PolicyDeferDiscover)
			}
		case ShortfallHostBindingRequired:
			if !known {
				t.Errorf("host-binding-required shortfall %q is not a real catalog id", e.Capability)
			} else if c.Class == PolicyDeferDiscover {
				t.Errorf("host-binding-required shortfall %q is DEFER-DISCOVER; belongs in that group instead", e.Capability)
			}
		case ShortfallNoManifestSection:
			if known {
				t.Errorf("no-manifest-section shortfall %q IS a real catalog id (%s); it belongs in a different group", e.Capability, c.Class)
			}
		default:
			t.Errorf("shortfall entry %q has unknown cause %q", e.Capability, e.Cause)
		}
	}
}

// --- D2: Explain is inspectable, never opaque -------------------------------

func TestExplainShowsPresetNameAndFullExpansionAndShortfall(t *testing.T) {
	raw := tuiV1FullPostureManifest(t)
	p := compileOK(t, raw)
	rep := p.Explain()
	if rep.Compatibility != "tui-v1" {
		t.Fatalf("Explain().Compatibility = %q, want tui-v1", rep.Compatibility)
	}
	if len(rep.CompatibilityShortfall) != len(tuiV1Shortfall) {
		t.Fatalf("Explain().CompatibilityShortfall len = %d, want %d", len(rep.CompatibilityShortfall), len(tuiV1Shortfall))
	}
	tuiV1, _ := LookupPreset("tui-v1")
	if !stringSlicesEqual(rep.Agent.Tools, tuiV1.IDs) {
		t.Errorf("Explain().Agent.Tools = %v, want the full tui-v1 expansion %v", rep.Agent.Tools, tuiV1.IDs)
	}
	// A reader must be able to see every capability's class from the report
	// alone (D2), not just its id.
	classes := make(map[string]PolicyClass, len(rep.Capabilities))
	for _, c := range rep.Capabilities {
		classes[c.ID] = c.Class
	}
	if classes["vault.exec"] != PolicyNeverDefault {
		t.Errorf("Explain().Capabilities must show vault.exec as NEVER-DEFAULT, got %v", classes)
	}

	js, err := p.ExplainJSON()
	if err != nil {
		t.Fatalf("ExplainJSON: %v", err)
	}
	for _, key := range []string{"\"compatibility\"", "\"compatibilityShortfall\"", "\"cause\"", "\"reason\""} {
		if !strings.Contains(string(js), key) {
			t.Errorf("ExplainJSON missing key %s", key)
		}
	}
}

// --- defensive copies --------------------------------------------------------

func TestPresetAccessorsReturnDefensiveCopies(t *testing.T) {
	spec1, ok := LookupPreset("tui-v1")
	if !ok {
		t.Fatal("LookupPreset(tui-v1) failed")
	}
	if len(spec1.IDs) > 0 {
		spec1.IDs[0] = "MUTATED"
	}
	if len(spec1.Shortfall) > 0 {
		spec1.Shortfall[0].Capability = "MUTATED"
	}
	spec2, _ := LookupPreset("tui-v1")
	if spec2.IDs[0] == "MUTATED" {
		t.Errorf("LookupPreset returned a mutable reference to IDs")
	}
	if spec2.Shortfall[0].Capability == "MUTATED" {
		t.Errorf("LookupPreset returned a mutable reference to Shortfall")
	}

	names := KnownPresetNames()
	if len(names) > 0 {
		names[0] = "MUTATED"
	}
	names2 := KnownPresetNames()
	if len(names2) > 0 && names2[0] == "MUTATED" {
		t.Errorf("KnownPresetNames returned a mutable reference")
	}

	raw := tuiV1FullPostureManifest(t)
	p := compileOK(t, raw)
	sf := p.CompatibilityShortfall()
	if len(sf) > 0 {
		sf[0].Capability = "MUTATED"
	}
	sf2 := p.CompatibilityShortfall()
	if len(sf2) > 0 && sf2[0].Capability == "MUTATED" {
		t.Errorf("CompatibilityShortfall returned a mutable reference")
	}

	tools := p.Tools()
	if len(tools) > 0 {
		tools[0] = "MUTATED"
	}
	if p.Tools()[0] == "MUTATED" {
		t.Errorf("Tools() returned a mutable reference (preset expansion aliased the plan's backing array)")
	}
}

// TestKnownPresetNamesSorted keeps the diagnostic text and this test in sync
// as presetCatalog grows.
func TestKnownPresetNamesSorted(t *testing.T) {
	names := KnownPresetNames()
	if !sort.StringsAreSorted(names) {
		t.Errorf("KnownPresetNames() = %v, want sorted", names)
	}
	if len(names) != len(presetCatalog) {
		t.Errorf("KnownPresetNames() len = %d, want %d (len(presetCatalog))", len(names), len(presetCatalog))
	}
}

// stringSlicesEqual is a small local helper (avoids importing reflect/slices
// just for equality checks across this file's many order-sensitive asserts).
func stringSlicesEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
