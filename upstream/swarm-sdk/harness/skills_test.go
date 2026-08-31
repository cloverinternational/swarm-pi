package harness

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// baseSkillManifest returns a minimal valid manifest with the given raw skills
// block spliced in verbatim (the block already includes the `skills:` key).
func baseSkillManifest(skillsBlock string) string {
	return "apiVersion: swarm.ai/v1alpha1\n" +
		"kind: Harness\n" +
		"metadata:\n  name: skilltest\n" +
		"provider:\n  id: anthropic\n  model: claude-x\n" +
		"agent:\n  systemPrompt:\n    inline: \"hi\"\n  tools: []\n" +
		skillsBlock
}

// writeSkillFixture writes dir/skills/<id>/SKILL.md with a unique body and
// returns the manifest-relative path for that skill dir.
func writeSkillFixture(t *testing.T, dir, id, body string) string {
	t.Helper()
	sd := filepath.Join(dir, "skills", id)
	if err := os.MkdirAll(sd, 0o755); err != nil {
		t.Fatalf("mkdir skill %q: %v", id, err)
	}
	if err := os.WriteFile(filepath.Join(sd, "SKILL.md"), []byte(body), 0o644); err != nil {
		t.Fatalf("write SKILL.md %q: %v", id, err)
	}
	return filepath.ToSlash(filepath.Join("skills", id))
}

func writeManifest(t *testing.T, dir, content string) string {
	t.Helper()
	path := filepath.Join(dir, "harness.yaml")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
	return path
}

// TestSkillsTwoPathEntriesResolve: two path-backed entries parse; Plan.Skills()
// has both with content hashes; Explain lists them (no bodies); digest present.
func TestSkillsTwoPathEntriesResolve(t *testing.T) {
	dir := t.TempDir()
	writeSkillFixture(t, dir, "git-triage", "TRIAGE_BODY_SENTINEL\n")
	writeSkillFixture(t, dir, "pdf-report", "PDF_BODY_SENTINEL\n")

	manifest := baseSkillManifest(
		"skills:\n" +
			"  - id: git-triage\n    path: skills/git-triage\n" +
			"  - id: pdf-report\n    path: skills/pdf-report\n")
	path := writeManifest(t, dir, manifest)

	p, err := Compile(path)
	if err != nil {
		t.Fatalf("compile failed: %v", err)
	}
	sk := p.Skills()
	if len(sk) != 2 {
		t.Fatalf("Skills() len = %d, want 2 (%+v)", len(sk), sk)
	}
	if sk[0].ID != "git-triage" || sk[1].ID != "pdf-report" {
		t.Errorf("ids = %q,%q", sk[0].ID, sk[1].ID)
	}
	for _, s := range sk {
		if !strings.HasPrefix(s.ContentHash, "sha256:") {
			t.Errorf("skill %q contentHash = %q, want sha256: prefix", s.ID, s.ContentHash)
		}
		if s.Source != "file" {
			t.Errorf("skill %q source = %q, want file", s.ID, s.Source)
		}
		if filepath.IsAbs(s.Path) {
			t.Errorf("skill %q path %q is absolute; want manifest-relative", s.ID, s.Path)
		}
	}
	if sk[0].ContentHash == sk[1].ContentHash {
		t.Errorf("distinct skill bodies produced identical hashes")
	}
	if p.Digest() == "" {
		t.Errorf("digest empty")
	}

	// Explain contains the hashes but NEVER the SKILL.md bodies.
	j, err := p.ExplainJSON()
	if err != nil {
		t.Fatalf("ExplainJSON: %v", err)
	}
	js := string(j)
	for _, s := range sk {
		if !strings.Contains(js, s.ContentHash) {
			t.Errorf("Explain JSON missing hash %q", s.ContentHash)
		}
	}
	if strings.Contains(js, "TRIAGE_BODY_SENTINEL") || strings.Contains(js, "PDF_BODY_SENTINEL") {
		t.Errorf("Explain JSON leaked skill file body:\n%s", js)
	}
}

// TestSkillsOmittedAndEmptyValid: omitted skills and empty `skills: []` are both
// valid and yield no skills.
func TestSkillsOmittedAndEmptyValid(t *testing.T) {
	omitted := baseSkillManifest("")
	p, err := CompileBytes([]byte(omitted), syntheticYAML)
	if err != nil {
		t.Fatalf("omitted skills should compile: %v", err)
	}
	if len(p.Skills()) != 0 {
		t.Errorf("omitted: Skills() = %v, want empty", p.Skills())
	}
	digestNoSkills := p.Digest()

	empty := baseSkillManifest("skills: []\n")
	p2, err := CompileBytes([]byte(empty), syntheticYAML)
	if err != nil {
		t.Fatalf("empty skills should compile: %v", err)
	}
	if len(p2.Skills()) != 0 {
		t.Errorf("empty: Skills() = %v, want empty", p2.Skills())
	}
	// `skills: []` and omitted are semantically identical (no skills) => same digest.
	if p2.Digest() != digestNoSkills {
		t.Errorf("empty vs omitted digests differ: %q vs %q", p2.Digest(), digestNoSkills)
	}
}

// TestSkillsDuplicateID: duplicate ids are rejected.
func TestSkillsDuplicateID(t *testing.T) {
	manifest := baseSkillManifest(
		"skills:\n  - id: dup\n  - id: dup\n")
	ds := compileErr(t, manifest, syntheticYAML)
	if !hasCode(ds, "harness.skills.id.duplicate") {
		t.Fatalf("expected duplicate id diagnostic, got: %v", ds)
	}
}

// TestSkillsMissingPathFailsClosed: a path-backed entry whose skill does not
// exist fails compilation with a source-cited diagnostic.
func TestSkillsMissingPathFailsClosed(t *testing.T) {
	dir := t.TempDir()
	manifest := baseSkillManifest(
		"skills:\n  - id: ghost\n    path: skills/does-not-exist\n")
	path := writeManifest(t, dir, manifest)

	_, err := Compile(path)
	if err == nil {
		t.Fatalf("expected compile to fail closed on missing skill path")
	}
	ds, ok := AsDiagnostics(err)
	if !ok {
		t.Fatalf("expected Diagnostics, got %T: %v", err, err)
	}
	if !hasCode(ds, "harness.path.missing") {
		t.Fatalf("expected harness.path.missing, got: %v", ds)
	}
	// Diagnostic must cite the source manifest.
	cited := false
	for _, d := range ds {
		if d.Code == "harness.path.missing" && d.SourcePath == path {
			cited = true
		}
	}
	if !cited {
		t.Errorf("missing-skill diagnostic did not cite source path %q: %v", path, ds)
	}
}

// TestSkillsManifestRelativeUnderChangedCWD: resolution is manifest-relative even
// when the process CWD is elsewhere.
func TestSkillsManifestRelativeUnderChangedCWD(t *testing.T) {
	dir := t.TempDir()
	writeSkillFixture(t, dir, "git-triage", "BODY_A\n")
	manifest := baseSkillManifest(
		"skills:\n  - id: git-triage\n    path: skills/git-triage\n")
	path := writeManifest(t, dir, manifest)

	// Change CWD to an unrelated directory.
	other := t.TempDir()
	orig, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	if err := os.Chdir(other); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(orig) })

	p, err := Compile(path)
	if err != nil {
		t.Fatalf("compile under changed cwd failed: %v", err)
	}
	sk := p.Skills()
	if len(sk) != 1 || sk[0].ContentHash == "" {
		t.Fatalf("skills not resolved manifest-relative under changed cwd: %+v", sk)
	}
}

// TestSkillsUnknownFieldRejected: an unknown key under a skills entry is rejected.
func TestSkillsUnknownFieldRejected(t *testing.T) {
	manifest := baseSkillManifest(
		"skills:\n  - id: x\n    bogus: y\n")
	ds := compileErr(t, manifest, syntheticYAML)
	if !hasCode(ds, "harness.decode.unknownField") {
		t.Fatalf("expected unknown-field rejection, got: %v", ds)
	}
}

// TestSkillsContentChangesDigest: changing a selected skill's content changes the
// plan digest.
func TestSkillsContentChangesDigest(t *testing.T) {
	dir := t.TempDir()
	skPath := filepath.Join(dir, "skills", "git-triage", "SKILL.md")
	writeSkillFixture(t, dir, "git-triage", "ORIGINAL_CONTENT\n")
	manifest := baseSkillManifest(
		"skills:\n  - id: git-triage\n    path: skills/git-triage\n")
	path := writeManifest(t, dir, manifest)

	p1, err := Compile(path)
	if err != nil {
		t.Fatalf("first compile: %v", err)
	}
	d1 := p1.Digest()
	h1 := p1.Skills()[0].ContentHash

	if err := os.WriteFile(skPath, []byte("CHANGED_CONTENT\n"), 0o644); err != nil {
		t.Fatalf("rewrite skill: %v", err)
	}
	p2, err := Compile(path)
	if err != nil {
		t.Fatalf("second compile: %v", err)
	}
	if p2.Skills()[0].ContentHash == h1 {
		t.Errorf("content hash unchanged after edit: %q", h1)
	}
	if p2.Digest() == d1 {
		t.Errorf("digest unchanged after skill content edit: %q", d1)
	}
}

// TestSkillsFilePathBacked: a path pointing directly at a SKILL.md file (not a
// directory) is hashed as that file.
func TestSkillsFilePathBacked(t *testing.T) {
	dir := t.TempDir()
	writeSkillFixture(t, dir, "git-triage", "FILE_BODY\n")
	manifest := baseSkillManifest(
		"skills:\n  - id: git-triage\n    path: skills/git-triage/SKILL.md\n")
	path := writeManifest(t, dir, manifest)

	p, err := Compile(path)
	if err != nil {
		t.Fatalf("compile file-backed skill: %v", err)
	}
	sk := p.Skills()
	if len(sk) != 1 || !strings.HasPrefix(sk[0].ContentHash, "sha256:") {
		t.Fatalf("file-backed skill not hashed: %+v", sk)
	}
}

// TestSkillsIDOnlyEntryValid: an id-only entry (no path) is declared without a
// content hash and does not fail closed.
func TestSkillsIDOnlyEntryValid(t *testing.T) {
	manifest := baseSkillManifest("skills:\n  - id: later\n")
	p, err := CompileBytes([]byte(manifest), syntheticYAML)
	if err != nil {
		t.Fatalf("id-only entry should compile: %v", err)
	}
	sk := p.Skills()
	if len(sk) != 1 || sk[0].ID != "later" {
		t.Fatalf("id-only skill not carried: %+v", sk)
	}
	if sk[0].ContentHash != "" || sk[0].Source != "manifest" {
		t.Errorf("id-only skill = %+v, want empty hash + manifest source", sk[0])
	}
}

// TestSkillsMissingIDRejected: an entry without an id is rejected.
func TestSkillsMissingIDRejected(t *testing.T) {
	manifest := baseSkillManifest("skills:\n  - path: skills/x\n")
	ds := compileErr(t, manifest, syntheticYAML)
	if !hasCode(ds, "harness.skills.id.missing") {
		t.Fatalf("expected missing-id diagnostic, got: %v", ds)
	}
}

// TestSkillsSearchRootsObjectForm: the object form carries manifest-relative
// search roots (which must exist) alongside entries.
func TestSkillsSearchRootsObjectForm(t *testing.T) {
	dir := t.TempDir()
	writeSkillFixture(t, dir, "git-triage", "BODY\n")
	manifest := baseSkillManifest(
		"skills:\n" +
			"  entries:\n    - id: git-triage\n      path: skills/git-triage\n" +
			"  searchRoots:\n    - skills\n")
	path := writeManifest(t, dir, manifest)

	p, err := Compile(path)
	if err != nil {
		t.Fatalf("object-form skills compile: %v", err)
	}
	if len(p.Skills()) != 1 {
		t.Fatalf("entries not carried: %+v", p.Skills())
	}
	roots := p.SkillSearchRoots()
	if len(roots) != 1 || roots[0] != "skills" {
		t.Fatalf("search roots = %v, want [skills]", roots)
	}
}

// TestSkillsSearchRootMissingFailsClosed: a non-existent search root fails closed.
func TestSkillsSearchRootMissingFailsClosed(t *testing.T) {
	dir := t.TempDir()
	manifest := baseSkillManifest(
		"skills:\n  entries: []\n  searchRoots:\n    - nope\n")
	path := writeManifest(t, dir, manifest)
	_, err := Compile(path)
	if err == nil {
		t.Fatalf("expected missing search root to fail closed")
	}
}
