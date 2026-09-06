package harness

import (
	"path/filepath"
	"strings"
	"testing"
)

// skillBaseManifest renders a minimal valid manifest that selects one
// path-backed skill and pins runtime.workspace/storage to a caller-supplied
// absolute directory (so the manifest dir itself does NOT leak into the digest
// via the default workspace/storage resolution, which would otherwise equal the
// manifest dir).
func skillBaseManifest(workspace, storage string) string {
	return "apiVersion: swarm.ai/v1alpha1\n" +
		"kind: Harness\n" +
		"metadata:\n  name: skillbase\n" +
		"runtime:\n  workspace: " + workspace + "\n  storage: " + storage + "\n" +
		"provider:\n  id: anthropic\n  model: claude-x\n" +
		"agent:\n  systemPrompt:\n    inline: \"hi\"\n  tools: []\n" +
		"skills:\n  - id: git-triage\n    path: skills/git-triage\n"
}

// TestRevealSkillBaseDirIsManifestDir: the tainted base dir accessor returns the
// absolute manifest directory and is never surfaced through the redacted plan
// surface (Explain, ExplainJSON, Digest).
func TestRevealSkillBaseDirIsManifestDir(t *testing.T) {
	dir := t.TempDir()
	shared := t.TempDir()
	writeSkillFixture(t, dir, "git-triage", "TRIAGE_BODY_SENTINEL\n")
	path := writeManifest(t, dir, skillBaseManifest(shared, shared))

	p, err := Compile(path)
	if err != nil {
		t.Fatalf("compile failed: %v", err)
	}

	// (1) The accessor returns the absolute manifest directory.
	base := p.RevealSkillBaseDir()
	if base != dir {
		t.Fatalf("RevealSkillBaseDir() = %q, want %q", base, dir)
	}
	if !filepath.IsAbs(base) {
		t.Fatalf("RevealSkillBaseDir() = %q, want absolute", base)
	}

	// (2) The base dir must NOT appear in the redacted Explain surface. Guard
	//     against a false negative first: the sentinel path must be absent from
	//     the workspace/storage the report DOES carry.
	rep := p.Explain()
	if rep.Runtime.Workspace == base || rep.Runtime.Storage == base {
		t.Fatalf("test setup leaked base into workspace/storage; pick distinct paths")
	}
	if strings.Contains(rep.Runtime.Workspace, base) || strings.Contains(rep.Runtime.Storage, base) {
		t.Fatalf("base dir leaked into runtime paths: ws=%q store=%q base=%q",
			rep.Runtime.Workspace, rep.Runtime.Storage, base)
	}

	js, err := p.ExplainJSON()
	if err != nil {
		t.Fatalf("ExplainJSON: %v", err)
	}
	if strings.Contains(string(js), base) {
		t.Errorf("ExplainJSON leaked the tainted skill base dir %q:\n%s", base, string(js))
	}

	// (3) The base dir must NOT appear in the digest.
	if strings.Contains(p.Digest(), base) {
		t.Errorf("Digest leaked the tainted skill base dir %q: %s", base, p.Digest())
	}
}

// TestSkillBaseDirDoesNotAffectDigest: two manifests that are byte-identical in
// every DIGEST-AFFECTING field but compiled from DIFFERENT manifest directories
// (hence different RevealSkillBaseDir) produce the SAME digest — proving the new
// skillBaseDir field is excluded from the digest computation.
func TestSkillBaseDirDoesNotAffectDigest(t *testing.T) {
	shared := t.TempDir() // identical absolute workspace/storage for both plans
	manifest := skillBaseManifest(shared, shared)

	dirA := t.TempDir()
	writeSkillFixture(t, dirA, "git-triage", "IDENTICAL_BODY\n")
	pA, err := Compile(writeManifest(t, dirA, manifest))
	if err != nil {
		t.Fatalf("compile A: %v", err)
	}

	dirB := t.TempDir()
	writeSkillFixture(t, dirB, "git-triage", "IDENTICAL_BODY\n")
	pB, err := Compile(writeManifest(t, dirB, manifest))
	if err != nil {
		t.Fatalf("compile B: %v", err)
	}

	if pA.RevealSkillBaseDir() == pB.RevealSkillBaseDir() {
		t.Fatalf("test invalid: both plans share a base dir %q", pA.RevealSkillBaseDir())
	}
	if pA.Digest() != pB.Digest() {
		t.Errorf("digest differs across manifest dirs, so skillBaseDir leaked into it:\n A(%s)=%s\n B(%s)=%s",
			pA.RevealSkillBaseDir(), pA.Digest(), pB.RevealSkillBaseDir(), pB.Digest())
	}
}
