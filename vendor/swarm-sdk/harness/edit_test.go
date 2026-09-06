package harness

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const editableManifest = `# harness head
apiVersion: swarm.ai/v1alpha1
kind: Harness
metadata:
  name: editable # keep name comment
  description: "untouched description"
runtime:
  workspace: ./work
  storage: ./state # unrelated runtime comment
  watch: false
provider:
  id: anthropic
  model: "old-model" # edited model comment
agent:
  systemPrompt:
    inline: "secret raw prompt"
  tools:
    - forge.read
permissions:
  approvalMode: interactive
interfaces:
  default: print
`

// editableManifestObjectSkills is editableManifest's twin but declares the
// `skills:` section in its OBJECT form (`{entries: [...], searchRoots: [...]}`)
// instead of the bare sequence form, to exercise SetSkillsExact against the
// shape that must preserve the `searchRoots` sibling key untouched.
const editableManifestObjectSkills = `apiVersion: swarm.ai/v1alpha1
kind: Harness
metadata:
  name: editable-object-skills
runtime:
  workspace: ./work
  storage: ./state
provider:
  id: anthropic
  model: "old-model"
agent:
  systemPrompt:
    inline: "secret raw prompt"
  tools:
    - forge.read
permissions:
  approvalMode: interactive
interfaces:
  default: print
skills:
  entries:
    - id: c
  searchRoots:
    - skills
`

func TestEditPreservesCommentsOrderAndUntouchedNodes(t *testing.T) {
	path, original := writeEditableManifest(t, editableManifest)
	session := openValidEditSession(t, path)
	if err := session.SetProviderModel("new-model"); err != nil {
		t.Fatal(err)
	}
	result, err := session.Save(SaveOptions{})
	if err != nil {
		t.Fatal(err)
	}
	written := readFile(t, path)

	if !bytes.Contains(written, []byte("# unrelated runtime comment")) {
		t.Fatalf("unrelated comment was lost:\n%s", written)
	}
	if !bytes.Contains(written, []byte(`model: "new-model" # edited model comment`)) {
		t.Fatalf("edited node style/comment was not retained:\n%s", written)
	}
	assertOrdered(t, string(written),
		"apiVersion:", "kind:", "metadata:", "runtime:", "provider:", "agent:", "permissions:", "interfaces:")
	for _, stable := range []string{
		`description: "untouched description"`,
		"storage: ./state # unrelated runtime comment",
		"inline: \"secret raw prompt\"",
		"    - forge.read",
	} {
		if !bytes.Contains(written, []byte(stable)) {
			t.Errorf("untouched content %q changed or disappeared:\n%s", stable, written)
		}
	}
	if got := readFile(t, result.BackupPath); !bytes.Equal(got, original) {
		t.Fatalf("backup differs from original\nbackup:\n%s\noriginal:\n%s", got, original)
	}
}

func TestEditBackupCanRestoreOriginal(t *testing.T) {
	path, original := writeEditableManifest(t, editableManifest)
	session := openValidEditSession(t, path)
	if err := session.SetWatchEnabled(true); err != nil {
		t.Fatal(err)
	}
	result, err := session.Save(SaveOptions{})
	if err != nil {
		t.Fatal(err)
	}
	backup := readFile(t, result.BackupPath)
	if !bytes.Equal(backup, original) {
		t.Fatal("backup does not contain immediate previous content")
	}
	if err := os.WriteFile(path, backup, 0o600); err != nil {
		t.Fatal(err)
	}
	if restored := readFile(t, path); !bytes.Equal(restored, original) {
		t.Fatal("restoring backup did not reproduce original")
	}
}

func TestEditValidateBeforeSaveLeavesDiskAndDirectoryUnchanged(t *testing.T) {
	path, original := writeEditableManifest(t, editableManifest)
	session := openValidEditSession(t, path)
	if err := session.SetScalar([]string{"apiVersion"}, "swarm.ai/v999"); err != nil {
		t.Fatal(err)
	}
	_, err := session.Save(SaveOptions{})
	var validationErr *ValidationError
	if !errors.As(err, &validationErr) {
		t.Fatalf("Save error = %T %v, want ValidationError", err, err)
	}
	if got := readFile(t, path); !bytes.Equal(got, original) {
		t.Fatal("invalid candidate changed the target")
	}
	if _, err := os.Stat(path + ".bak"); !os.IsNotExist(err) {
		t.Fatalf("invalid candidate created backup: %v", err)
	}
	assertNoEditTemps(t, filepath.Dir(path))
}

func TestEditAtomicReplaceMatchesCandidateAndLeavesNoTemp(t *testing.T) {
	path, _ := writeEditableManifest(t, editableManifest)
	session := openValidEditSession(t, path)
	if err := session.SetMaxOutputTokens(4096); err != nil {
		t.Fatal(err)
	}
	if err := session.SetMaxTurns(8); err != nil {
		t.Fatal(err)
	}
	if err := session.SetTimeout(90); err != nil {
		t.Fatal(err)
	}
	candidate, err := session.Render()
	if err != nil {
		t.Fatal(err)
	}
	result, err := session.Save(SaveOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, path); !bytes.Equal(got, candidate) {
		t.Fatalf("target does not equal rendered candidate\ngot:\n%s\nwant:\n%s", got, candidate)
	}
	if result.ContentHash != hashBytes(candidate) {
		t.Fatalf("content hash = %q, want %q", result.ContentHash, hashBytes(candidate))
	}
	assertNoEditTemps(t, filepath.Dir(path))
}

func TestEditExternalWriteConflictDoesNotClobber(t *testing.T) {
	path, _ := writeEditableManifest(t, editableManifest)
	session := openValidEditSession(t, path)
	if err := session.SetProviderModel("candidate-model"); err != nil {
		t.Fatal(err)
	}
	external := []byte(strings.Replace(editableManifest, "old-model", "external-model", 1))
	if err := os.WriteFile(path, external, 0o600); err != nil {
		t.Fatal(err)
	}

	_, err := session.Save(SaveOptions{})
	var conflict *ConflictError
	if !errors.As(err, &conflict) {
		t.Fatalf("Save error = %T %v, want ConflictError", err, err)
	}
	if got := readFile(t, path); !bytes.Equal(got, external) {
		t.Fatal("conflict save clobbered external content")
	}
	if _, err := os.Stat(path + ".bak"); !os.IsNotExist(err) {
		t.Fatalf("conflict created backup: %v", err)
	}
	assertNoEditTemps(t, filepath.Dir(path))
}

func TestEditLateExternalWriteConflictDoesNotClobber(t *testing.T) {
	path, _ := writeEditableManifest(t, editableManifest)
	session := openValidEditSession(t, path)
	if err := session.SetProviderModel("candidate-model"); err != nil {
		t.Fatal(err)
	}
	external := []byte(strings.Replace(editableManifest, "old-model", "late-external-model", 1))
	hookCalled := false
	saveHookAfterValidate = func() {
		hookCalled = true
		if err := os.WriteFile(path, external, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() { saveHookAfterValidate = nil })

	_, err := session.Save(SaveOptions{})
	var conflict *ConflictError
	if !errors.As(err, &conflict) {
		t.Fatalf("Save error = %T %v, want ConflictError", err, err)
	}
	if !hookCalled {
		t.Fatal("post-validation save hook was not called")
	}
	if conflict.CurrentHash != hashBytes(external) {
		t.Fatalf("conflict current hash = %q, want late external hash %q",
			conflict.CurrentHash, hashBytes(external))
	}
	if got := readFile(t, path); !bytes.Equal(got, external) {
		t.Fatal("late conflict save clobbered external content")
	}
	if _, err := os.Stat(path + ".bak"); !os.IsNotExist(err) {
		t.Fatalf("late conflict created backup: %v", err)
	}
	assertNoEditTemps(t, filepath.Dir(path))
}

func TestEditLateExternalWriteOverridesUseCurrentVersion(t *testing.T) {
	tests := []struct {
		name    string
		options func([]byte) SaveOptions
	}{
		{
			name: "force",
			options: func([]byte) SaveOptions {
				return SaveOptions{Force: true}
			},
		},
		{
			name: "expected hash",
			options: func(external []byte) SaveOptions {
				return SaveOptions{ExpectHash: hashBytes(external)}
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			path, _ := writeEditableManifest(t, editableManifest)
			session := openValidEditSession(t, path)
			if err := session.SetProviderModel("candidate-model"); err != nil {
				t.Fatal(err)
			}
			candidate, err := session.Render()
			if err != nil {
				t.Fatal(err)
			}
			external := []byte(strings.Replace(editableManifest, "old-model", "late-external-model", 1))
			saveHookAfterValidate = func() {
				if err := os.WriteFile(path, external, 0o600); err != nil {
					t.Fatal(err)
				}
			}
			t.Cleanup(func() { saveHookAfterValidate = nil })

			result, err := session.Save(test.options(external))
			if err != nil {
				t.Fatal(err)
			}
			if got := readFile(t, result.BackupPath); !bytes.Equal(got, external) {
				t.Fatal("late override backup does not contain the external version replaced")
			}
			if got := readFile(t, path); !bytes.Equal(got, candidate) {
				t.Fatalf("late override target differs from candidate\ngot:\n%s\nwant:\n%s", got, candidate)
			}
			assertNoEditTemps(t, filepath.Dir(path))
		})
	}
}

func TestEditExpectedHashAllowsKnownExternalVersion(t *testing.T) {
	path, _ := writeEditableManifest(t, editableManifest)
	session := openValidEditSession(t, path)
	if err := session.SetProviderModel("candidate-model"); err != nil {
		t.Fatal(err)
	}
	external := []byte(strings.Replace(editableManifest, "old-model", "external-model", 1))
	if err := os.WriteFile(path, external, 0o600); err != nil {
		t.Fatal(err)
	}
	result, err := session.Save(SaveOptions{ExpectHash: hashBytes(external)})
	if err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, result.BackupPath); !bytes.Equal(got, external) {
		t.Fatal("expected-hash save did not back up known external version")
	}
}

func TestEditPromptHelpersEnforceOneOfAndToolsExact(t *testing.T) {
	path, _ := writeEditableManifest(t, editableManifest)
	session := openValidEditSession(t, path)
	if err := session.SetSystemPromptFile("prompt.txt"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(filepath.Dir(path), "prompt.txt"), []byte("file prompt"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := session.SetToolsExact([]string{}); err != nil {
		t.Fatal(err)
	}
	rendered, err := session.Render()
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(rendered, []byte("inline:")) || !bytes.Contains(rendered, []byte("file: prompt.txt")) {
		t.Fatalf("file helper did not enforce prompt one-of:\n%s", rendered)
	}
	if plan, ds, err := session.Validate(); err != nil || ds.HasErrors() || len(plan.Tools()) != 0 {
		t.Fatalf("Validate = plan %#v, diagnostics %v, error %v", plan, ds, err)
	}
}

func TestEditDiffRedactsSecretAndRawPrompts(t *testing.T) {
	const envName = "HARNESS_EDIT_TEST_SECRET"
	const secret = "never-print-this-credential-value"
	t.Setenv(envName, secret)
	manifest := strings.Replace(editableManifest,
		"  model: \"old-model\" # edited model comment",
		"  model: \"old-model\" # edited model comment\n  credential:\n    env: "+envName, 1)
	path, _ := writeEditableManifest(t, manifest)
	session := openValidEditSession(t, path)
	const newPrompt = "new raw prompt that must not appear"
	if err := session.SetSystemPromptInline(newPrompt); err != nil {
		t.Fatal(err)
	}
	if err := session.SetApprovalMode("always"); err != nil {
		t.Fatal(err)
	}
	diff, err := session.Diff()
	if err != nil {
		t.Fatal(err)
	}
	serialized, err := json.Marshal(diff)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{secret, "secret raw prompt", newPrompt} {
		if bytes.Contains(serialized, []byte(forbidden)) {
			t.Fatalf("redacted diff contains forbidden value %q: %s", forbidden, serialized)
		}
	}
	if diff.Before.PromptHash == diff.After.PromptHash {
		t.Fatal("prompt hash did not reflect prompt edit")
	}
	if !diff.Changed {
		t.Fatal("semantic diff did not report a change")
	}
}

func TestOpenEditSessionAllowsSchemaRepair(t *testing.T) {
	invalid := strings.Replace(editableManifest, "swarm.ai/v1alpha1", "swarm.ai/v999", 1)
	path, _ := writeEditableManifest(t, invalid)
	session, ds, err := OpenEditSession(path)
	if err != nil {
		t.Fatal(err)
	}
	if session == nil || !ds.HasErrors() {
		t.Fatalf("OpenEditSession = session %#v, diagnostics %v; want editable invalid session", session, ds)
	}
	if err := session.SetScalar([]string{"apiVersion"}, "swarm.ai/v1alpha1"); err != nil {
		t.Fatal(err)
	}
	if _, err := session.Save(SaveOptions{}); err != nil {
		t.Fatal(err)
	}
}

// TestEditSetSkillsExactAddsRendersAndCompiles: adding id-only + path-backed
// skill entries to a manifest with no prior `skills:` key renders a `skills:`
// sequence that Render->CompileBytes carries through to Plan.Skills() — the
// same parity path `-p` compiles.
func TestEditSetSkillsExactAddsRendersAndCompiles(t *testing.T) {
	path, _ := writeEditableManifest(t, editableManifest)
	dir := filepath.Dir(path)
	writeEditSkillFile(t, dir, "skills/demo-skill", "demo-skill body")

	session := openValidEditSession(t, path)
	entries := []SkillEntry{
		{ID: "demo-skill", Path: "skills/demo-skill"},
		{ID: "id-only-skill"},
	}
	if err := session.SetSkillsExact(entries); err != nil {
		t.Fatal(err)
	}

	rendered, err := session.Render()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(rendered, []byte("demo-skill")) || !bytes.Contains(rendered, []byte("id-only-skill")) {
		t.Fatalf("rendered manifest missing skill ids:\n%s", rendered)
	}

	plan, ds, err := session.Validate()
	if err != nil {
		t.Fatal(err)
	}
	if ds.HasErrors() {
		t.Fatalf("unexpected validation diagnostics: %v", ds)
	}
	got := plan.Skills()
	if len(got) != 2 {
		t.Fatalf("plan.Skills() = %#v, want 2 entries", got)
	}
	if got[0].ID != "demo-skill" || got[0].Path != filepath.Clean("skills/demo-skill") || got[0].ContentHash == "" || got[0].Source != "file" {
		t.Fatalf("path-backed skill spec = %#v", got[0])
	}
	if got[1].ID != "id-only-skill" || got[1].Path != "" || got[1].ContentHash != "" || got[1].Source != "manifest" {
		t.Fatalf("id-only skill spec = %#v", got[1])
	}

	// Parity: compiling the rendered bytes directly (the same bytes a `-p`
	// worker would compile from disk) must produce the identical skill list.
	direct, err := CompileBytes(rendered, path)
	if err != nil {
		t.Fatal(err)
	}
	if diff := cmpSkillSpecs(plan.Skills(), direct.Skills()); diff != "" {
		t.Fatalf("edited-session plan.Skills() differs from direct CompileBytes: %s", diff)
	}
}

// TestEditSetSkillsExactReordersAndRemoves: re-calling SetSkillsExact with a
// different order/subset replaces the sequence exactly (no leftover entries).
func TestEditSetSkillsExactReordersAndRemoves(t *testing.T) {
	path, _ := writeEditableManifest(t, editableManifest)
	session := openValidEditSession(t, path)
	if err := session.SetSkillsExact([]SkillEntry{{ID: "a"}, {ID: "b"}, {ID: "c"}}); err != nil {
		t.Fatal(err)
	}
	if err := session.SetSkillsExact([]SkillEntry{{ID: "c"}, {ID: "a"}}); err != nil {
		t.Fatal(err)
	}
	plan, ds, err := session.Validate()
	if err != nil {
		t.Fatal(err)
	}
	if ds.HasErrors() {
		t.Fatalf("unexpected diagnostics: %v", ds)
	}
	got := plan.Skills()
	if len(got) != 2 || got[0].ID != "c" || got[1].ID != "a" {
		t.Fatalf("plan.Skills() = %#v, want [c a] in order", got)
	}
}

// TestEditSetSkillsExactEmptyClearsSelection: an empty slice is a valid
// zero-skills selection.
func TestEditSetSkillsExactEmptyClearsSelection(t *testing.T) {
	path, _ := writeEditableManifest(t, editableManifest)
	session := openValidEditSession(t, path)
	if err := session.SetSkillsExact([]SkillEntry{{ID: "a"}}); err != nil {
		t.Fatal(err)
	}
	if err := session.SetSkillsExact(nil); err != nil {
		t.Fatal(err)
	}
	plan, ds, err := session.Validate()
	if err != nil {
		t.Fatal(err)
	}
	if ds.HasErrors() {
		t.Fatalf("unexpected diagnostics: %v", ds)
	}
	if got := plan.Skills(); len(got) != 0 {
		t.Fatalf("plan.Skills() = %#v, want empty after clearing", got)
	}
}

// TestEditSetSkillsExactRejectsEmptyAndDuplicateIDs: invalid entries are
// rejected BEFORE mutation and leave the session's rendered document unchanged.
func TestEditSetSkillsExactRejectsEmptyAndDuplicateIDs(t *testing.T) {
	path, _ := writeEditableManifest(t, editableManifest)
	session := openValidEditSession(t, path)
	before, err := session.Render()
	if err != nil {
		t.Fatal(err)
	}

	if err := session.SetSkillsExact([]SkillEntry{{ID: ""}}); err == nil {
		t.Fatal("expected error for empty skill id")
	}
	if err := session.SetSkillsExact([]SkillEntry{{ID: "dup"}, {ID: "dup"}}); err == nil {
		t.Fatal("expected error for duplicate skill id")
	}

	after, err := session.Render()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatalf("rejected SetSkillsExact call mutated the document\nbefore:\n%s\nafter:\n%s", before, after)
	}
}

// TestEditSetSkillsExactPreservesObjectFormSearchRoots: SetSkillsExact against
// the object-form `skills: {entries: [...], searchRoots: [...]}` on-disk shape
// must rewrite ONLY the `entries` child; `searchRoots` (and any other sibling
// key) must survive byte-for-byte. Regression for the Phase 5d independent
// review MAJOR finding: searchRoots was silently destroyed on any edit.
func TestEditSetSkillsExactPreservesObjectFormSearchRoots(t *testing.T) {
	path, _ := writeEditableManifest(t, editableManifestObjectSkills)
	dir := filepath.Dir(path)
	if err := os.MkdirAll(filepath.Join(dir, "skills"), 0o755); err != nil {
		t.Fatal(err)
	}

	session := openValidEditSession(t, path)
	if err := session.SetSkillsExact([]SkillEntry{{ID: "b"}, {ID: "a"}}); err != nil {
		t.Fatal(err)
	}

	rendered, err := session.Render()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(rendered, []byte("searchRoots")) {
		t.Fatalf("SetSkillsExact dropped the object-form searchRoots sibling key:\n%s", rendered)
	}

	plan, ds, err := session.Validate()
	if err != nil {
		t.Fatal(err)
	}
	if ds.HasErrors() {
		t.Fatalf("unexpected validation diagnostics: %v", ds)
	}
	if got := plan.SkillSearchRoots(); len(got) != 1 || got[0] != "skills" {
		t.Fatalf("plan.SkillSearchRoots() = %#v, want [\"skills\"] preserved from the object form", got)
	}
	gotSkills := plan.Skills()
	if len(gotSkills) != 2 || gotSkills[0].ID != "b" || gotSkills[1].ID != "a" {
		t.Fatalf("plan.Skills() = %#v, want [b a] in the new order", gotSkills)
	}

	// Parity: compiling the rendered bytes directly (the same bytes a `-p`
	// worker would compile from disk) must produce the identical result,
	// including the preserved search roots.
	direct, err := CompileBytes(rendered, path)
	if err != nil {
		t.Fatal(err)
	}
	if diff := cmpSkillSpecs(plan.Skills(), direct.Skills()); diff != "" {
		t.Fatalf("edited-session plan.Skills() differs from direct CompileBytes: %s", diff)
	}
	if len(direct.SkillSearchRoots()) != 1 || direct.SkillSearchRoots()[0] != "skills" {
		t.Fatalf("direct.SkillSearchRoots() = %#v, want [\"skills\"]", direct.SkillSearchRoots())
	}
}

// TestEditDiffShowsSkillsChange: the redacted diff surface reflects a skills
// edit without ever including a SKILL.md body or an absolute host path.
func TestEditDiffShowsSkillsChange(t *testing.T) {
	path, _ := writeEditableManifest(t, editableManifest)
	dir := filepath.Dir(path)
	abs := writeEditSkillFile(t, dir, "skills/demo-skill", "SKILL-BODY-MUST-NOT-LEAK")

	session := openValidEditSession(t, path)
	if err := session.SetSkillsExact([]SkillEntry{{ID: "demo-skill", Path: "skills/demo-skill"}}); err != nil {
		t.Fatal(err)
	}
	diff, err := session.Diff()
	if err != nil {
		t.Fatal(err)
	}
	if !diff.Changed {
		t.Fatal("expected the skills edit to register as a semantic change")
	}
	if len(diff.Before.Skills) != 0 {
		t.Fatalf("before.Skills = %v, want empty", diff.Before.Skills)
	}
	if len(diff.After.Skills) != 1 || !strings.HasPrefix(diff.After.Skills[0], "demo-skill:") || !strings.HasSuffix(diff.After.Skills[0], "*") {
		t.Fatalf("after.Skills = %v, want one redacted token like demo-skill:<hash>*", diff.After.Skills)
	}
	serialized, err := json.Marshal(diff)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(serialized, []byte("SKILL-BODY-MUST-NOT-LEAK")) || bytes.Contains(serialized, []byte(abs)) {
		t.Fatalf("redacted diff leaked skill content or absolute path: %s", serialized)
	}
}

// writeEditSkillFile writes <dir>/<rel>/SKILL.md with minimal content and
// returns the absolute skill directory (for negative "must not leak" checks).
func writeEditSkillFile(t *testing.T, dir, rel, body string) string {
	t.Helper()
	sd := filepath.Join(dir, rel)
	if err := os.MkdirAll(sd, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sd, "SKILL.md"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return sd
}

// cmpSkillSpecs returns a non-empty description when the two SkillSpec slices
// differ (order-sensitive, mirroring declaration order).
func cmpSkillSpecs(a, b []SkillSpec) string {
	if len(a) != len(b) {
		return fmt.Sprintf("length %d != %d", len(a), len(b))
	}
	for i := range a {
		if a[i] != b[i] {
			return fmt.Sprintf("index %d: %#v != %#v", i, a[i], b[i])
		}
	}
	return ""
}

func writeEditableManifest(t *testing.T, manifest string) (string, []byte) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "harness.yaml")
	raw := []byte(manifest)
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	return path, raw
}

func openValidEditSession(t *testing.T, path string) *EditSession {
	t.Helper()
	session, ds, err := OpenEditSession(path)
	if err != nil {
		t.Fatal(err)
	}
	if ds.HasErrors() {
		t.Fatalf("unexpected open diagnostics: %v", ds)
	}
	return session
}

func readFile(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func assertOrdered(t *testing.T, text string, markers ...string) {
	t.Helper()
	last := -1
	for _, marker := range markers {
		index := strings.Index(text, marker)
		if index < 0 {
			t.Fatalf("marker %q missing from:\n%s", marker, text)
		}
		if index <= last {
			t.Fatalf("marker %q is out of order in:\n%s", marker, text)
		}
		last = index
	}
}

func assertNoEditTemps(t *testing.T, dir string) {
	t.Helper()
	matches, err := filepath.Glob(filepath.Join(dir, "*.tmp"))
	if err != nil {
		t.Fatal(err)
	}
	hidden, err := filepath.Glob(filepath.Join(dir, ".*.tmp"))
	if err != nil {
		t.Fatal(err)
	}
	matches = append(matches, hidden...)
	if len(matches) != 0 {
		t.Fatalf("leftover temp files: %v", matches)
	}
}
