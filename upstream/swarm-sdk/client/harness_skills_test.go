package client

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Swarm-Code/mono/swarm-sdk/harness"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/observability/noop"
)

// writeClientSkill writes dir/skills/<id>/SKILL.md with valid frontmatter (name
// == id so the loaded registry resolves it by id) and the given body. It returns
// the absolute skill directory.
func writeClientSkill(t *testing.T, dir, id, body string) string {
	t.Helper()
	sd := filepath.Join(dir, "skills", id)
	if err := os.MkdirAll(sd, 0o755); err != nil {
		t.Fatalf("mkdir skill %q: %v", id, err)
	}
	content := "---\nname: " + id + "\ndescription: harness closed-path test skill " + id + "\n---\n" + body
	if err := os.WriteFile(filepath.Join(sd, "SKILL.md"), []byte(content), 0o644); err != nil {
		t.Fatalf("write SKILL.md %q: %v", id, err)
	}
	return sd
}

// writeSkillAt writes <root>/<id>/SKILL.md under an arbitrary root directory.
func writeSkillAt(t *testing.T, root, id, body string) {
	t.Helper()
	sd := filepath.Join(root, id)
	if err := os.MkdirAll(sd, 0o755); err != nil {
		t.Fatalf("mkdir skill %q: %v", id, err)
	}
	content := "---\nname: " + id + "\ndescription: harness closed-path test skill " + id + "\n---\n" + body
	if err := os.WriteFile(filepath.Join(sd, "SKILL.md"), []byte(content), 0o644); err != nil {
		t.Fatalf("write SKILL.md %q: %v", id, err)
	}
}

// compilePlanInDir compiles an in-memory manifest anchored at dir (so
// manifest-relative skills resolve against dir and RevealSkillBaseDir()==dir).
func compilePlanInDir(t *testing.T, dir, manifest string) *harness.Plan {
	t.Helper()
	src := filepath.Join(dir, "harness.yaml")
	plan, err := harness.CompileBytes([]byte(manifest), src)
	if err != nil {
		t.Fatalf("CompileBytes: %v", err)
	}
	return plan
}

func containsStr(ss []string, want string) bool {
	for _, s := range ss {
		if s == want {
			return true
		}
	}
	return false
}

const pathSkillManifest = `apiVersion: swarm.ai/v1alpha1
kind: Harness
metadata:
  name: skillclient
provider:
  id: anthropic
  model: claude-x
  credential:
    inline: test-key
agent:
  systemPrompt:
    inline: "hi"
  tools: []
skills:
  - id: demo-skill
    path: skills/demo-skill
permissions:
  approvalMode: readonly
`

// TestHarnessSkillToolRegisteredForSelectedSkill: a path-backed selected skill
// registers EXACTLY the Skill tool (visible via hints/provider tools) and loads
// into a registry resolvable by id.
func TestHarnessSkillToolRegisteredForSelectedSkill(t *testing.T) {
	dir := t.TempDir()
	writeClientSkill(t, dir, "demo-skill", "DEMO_BODY\n")
	plan := compilePlanInDir(t, dir, pathSkillManifest)

	c, err := New(WithHarnessPlan(plan))
	if err != nil {
		t.Fatalf("New(harness): %v", err)
	}
	t.Cleanup(func() { _ = c.Close() })

	if !containsStr(c.agentDef.ToolHints, "Skill") {
		t.Errorf("ToolHints %v missing \"Skill\"", c.agentDef.ToolHints)
	}
	if names := providerToolNames(c); !containsStr(names, "Skill") {
		t.Errorf("provider tools %v missing \"Skill\"", names)
	}
	if c.skillRegistry == nil {
		t.Fatalf("skillRegistry is nil for a plan that selected a skill")
	}
	if _, ok := c.skillRegistry.Get("demo-skill"); !ok {
		t.Errorf("loaded registry does not resolve id \"demo-skill\"")
	}
}

// TestHarnessSkillContentHashMismatchFailsClosed: mutating the skill file after
// compile causes construction to fail closed with NO client.
func TestHarnessSkillContentHashMismatchFailsClosed(t *testing.T) {
	dir := t.TempDir()
	writeClientSkill(t, dir, "demo-skill", "ORIGINAL_BODY\n")
	plan := compilePlanInDir(t, dir, pathSkillManifest)

	// Tamper with the on-disk SKILL.md so its hash no longer matches the plan.
	writeClientSkill(t, dir, "demo-skill", "TAMPERED_BODY_DIFFERENT_LENGTH\n")

	c, err := New(WithHarnessPlan(plan))
	if err == nil {
		if c != nil {
			_ = c.Close()
		}
		t.Fatalf("expected fail-closed hash-mismatch error, got a client")
	}
	if c != nil {
		t.Fatalf("client must be nil on fail-closed construction")
	}
	if !strings.Contains(err.Error(), "hash mismatch") {
		t.Errorf("error %q does not mention hash mismatch", err.Error())
	}
}

// TestHarnessNoSkillsUnchanged: a plan with zero skills registers no Skill tool,
// leaves hints free of "Skill", and keeps skillRegistry nil.
func TestHarnessNoSkillsUnchanged(t *testing.T) {
	c := mustBuild(t, minimalManifest)
	if containsStr(c.agentDef.ToolHints, "Skill") {
		t.Errorf("ToolHints unexpectedly contains \"Skill\": %v", c.agentDef.ToolHints)
	}
	if names := providerToolNames(c); containsStr(names, "Skill") {
		t.Errorf("provider tools unexpectedly contain \"Skill\": %v", names)
	}
	if c.skillRegistry != nil {
		t.Errorf("skillRegistry must be nil when no skills are selected")
	}
}

// TestBuildHarnessSkillToolZeroSkills: the helper returns the no-op tuple for a
// zero-skill plan.
func TestBuildHarnessSkillToolZeroSkills(t *testing.T) {
	plan := compilePlan(t, minimalManifest)
	tool, reg, has, err := buildHarnessSkillTool(plan, noop.NewLogger(), noop.NewTracer(), func() string { return "" })
	if err != nil {
		t.Fatalf("buildHarnessSkillTool: %v", err)
	}
	if has || tool != nil || reg != nil {
		t.Fatalf("zero-skill plan must return (nil,nil,false,nil); got tool=%v reg=%v has=%v", tool, reg, has)
	}
}

const idOnlySkillManifest = `apiVersion: swarm.ai/v1alpha1
kind: Harness
metadata:
  name: skillclient
provider:
  id: anthropic
  model: claude-x
  credential:
    inline: test-key
agent:
  systemPrompt:
    inline: "hi"
  tools: []
skills:
  entries:
    - id: demo-skill
  searchRoots:
    - skills
permissions:
  approvalMode: readonly
`

// TestHarnessIdOnlySkillResolvesViaSearchRoot: an id-only entry with exactly one
// matching search root loads and is resolvable by id.
func TestHarnessIdOnlySkillResolvesViaSearchRoot(t *testing.T) {
	dir := t.TempDir()
	writeClientSkill(t, dir, "demo-skill", "IDONLY_BODY\n")
	plan := compilePlanInDir(t, dir, idOnlySkillManifest)

	c, err := New(WithHarnessPlan(plan))
	if err != nil {
		t.Fatalf("New(harness): %v", err)
	}
	t.Cleanup(func() { _ = c.Close() })

	if !containsStr(c.agentDef.ToolHints, "Skill") {
		t.Errorf("ToolHints %v missing \"Skill\"", c.agentDef.ToolHints)
	}
	if c.skillRegistry == nil {
		t.Fatalf("skillRegistry nil for id-only selection")
	}
	if _, ok := c.skillRegistry.Get("demo-skill"); !ok {
		t.Errorf("id-only registry does not resolve \"demo-skill\"")
	}
}

const idOnlyAmbiguousManifest = `apiVersion: swarm.ai/v1alpha1
kind: Harness
metadata:
  name: skillclient
provider:
  id: anthropic
  model: claude-x
  credential:
    inline: test-key
agent:
  systemPrompt:
    inline: "hi"
  tools: []
skills:
  entries:
    - id: demo-skill
  searchRoots:
    - skillsA
    - skillsB
permissions:
  approvalMode: readonly
`

// TestHarnessIdOnlyAmbiguousFailsClosed: an id-only entry matched under more than
// one search root fails closed (ambiguous) with NO client.
func TestHarnessIdOnlyAmbiguousFailsClosed(t *testing.T) {
	dir := t.TempDir()
	writeSkillAt(t, filepath.Join(dir, "skillsA"), "demo-skill", "A_BODY\n")
	writeSkillAt(t, filepath.Join(dir, "skillsB"), "demo-skill", "B_BODY\n")
	plan := compilePlanInDir(t, dir, idOnlyAmbiguousManifest)

	c, err := New(WithHarnessPlan(plan))
	if err == nil {
		if c != nil {
			_ = c.Close()
		}
		t.Fatalf("expected ambiguous-id fail-closed error, got a client")
	}
	if c != nil {
		t.Fatalf("client must be nil on ambiguous id-only resolution")
	}
	if !strings.Contains(err.Error(), "ambiguous") {
		t.Errorf("error %q does not mention ambiguity", err.Error())
	}
}

// TestResolveHarnessSkillPathEscapeRejected: a spec whose path escapes the base
// directory is rejected as a containment error (this case cannot be produced by
// a real compile, so the resolver is exercised directly).
func TestResolveHarnessSkillPathEscapeRejected(t *testing.T) {
	base := t.TempDir()
	realBase, err := filepath.EvalSymlinks(base)
	if err != nil {
		realBase = filepath.Clean(base)
	}
	spec := harness.SkillSpec{ID: "evil", Path: "../evil", Source: "file"}
	_, _, rerr := resolveHarnessSkillTarget(base, realBase, nil, spec)
	if rerr == nil {
		t.Fatalf("expected containment error for path escaping base")
	}
	if !isSkillContainmentError(rerr) {
		t.Errorf("error %q is not classified as a containment error", rerr.Error())
	}
}

// TestResolveHarnessSkillIdOnlyZeroMatch: an id-only spec with no matching root
// fails closed with a not-found error.
func TestResolveHarnessSkillIdOnlyZeroMatch(t *testing.T) {
	base := t.TempDir()
	writeClientSkill(t, base, "present-skill", "X\n") // under base/skills, not searched here
	realBase, err := filepath.EvalSymlinks(base)
	if err != nil {
		realBase = filepath.Clean(base)
	}
	spec := harness.SkillSpec{ID: "absent-skill", Source: "manifest"}
	_, _, rerr := resolveHarnessSkillTarget(base, realBase, []string{"skills"}, spec)
	if rerr == nil {
		t.Fatalf("expected not-found error for id-only with zero matches")
	}
	if isSkillContainmentError(rerr) {
		t.Errorf("zero-match should not be a containment error: %v", rerr)
	}
}
