// Phase 5d — client.HarnessSnapshot Skills/SkillSearchRoots view tests.
//
// These assert the redacted skills view is populated from plan.Skills() /
// plan.SkillSearchRoots() and NEVER carries a SKILL.md body or an absolute
// host path (only the harness package's own manifest-relative Path label and
// content hash, mirroring harness.SkillSpec exactly).
package client

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestHarnessSnapshotSkillsPopulatedAndRedacted: the snapshot's Skills view
// mirrors plan.Skills() field-for-field, SkillSearchRoots mirrors
// plan.SkillSearchRoots(), and neither the SKILL.md body nor the absolute
// temp-dir path ever appears in the serialized snapshot.
func TestHarnessSnapshotSkillsPopulatedAndRedacted(t *testing.T) {
	const bodySentinel = "SKILL-BODY-MUST-NEVER-APPEAR-IN-SNAPSHOT"
	dir := t.TempDir()
	writeClientSkill(t, dir, "demo-skill", bodySentinel)
	writeClientSkill(t, dir, "id-only-skill", "id-only body\n")

	manifest := `apiVersion: swarm.ai/v1alpha1
kind: Harness
metadata:
  name: skillsnapshot
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
      path: skills/demo-skill
    - id: id-only-skill
  searchRoots:
    - skills
permissions:
  approvalMode: readonly
`
	plan := compilePlanInDir(t, dir, manifest)

	c, err := New(WithHarnessPlan(plan))
	if err != nil {
		t.Fatalf("New(harness): %v", err)
	}
	t.Cleanup(func() { _ = c.Close() })

	snap := c.HarnessSnapshot()
	if !snap.Harness {
		t.Fatal("snapshot.Harness = false for a harness-built client")
	}
	if len(snap.Skills) != 2 {
		t.Fatalf("snapshot.Skills = %#v, want 2 entries", snap.Skills)
	}

	want := plan.Skills()
	for i, sp := range want {
		got := snap.Skills[i]
		if got.ID != sp.ID || got.Path != sp.Path || got.ContentHash != sp.ContentHash || got.Source != sp.Source {
			t.Fatalf("snapshot.Skills[%d] = %#v, want mirror of plan spec %#v", i, got, sp)
		}
	}
	if snap.Skills[0].ID != "demo-skill" || snap.Skills[0].Source != "file" || snap.Skills[0].ContentHash == "" {
		t.Fatalf("path-backed skill view = %#v", snap.Skills[0])
	}
	if snap.Skills[1].ID != "id-only-skill" || snap.Skills[1].Source != "manifest" || snap.Skills[1].Path != "" || snap.Skills[1].ContentHash != "" {
		t.Fatalf("id-only skill view = %#v", snap.Skills[1])
	}

	if len(plan.SkillSearchRoots()) != len(snap.SkillSearchRoots) {
		t.Fatalf("snapshot.SkillSearchRoots = %v, want mirror of plan.SkillSearchRoots() = %v",
			snap.SkillSearchRoots, plan.SkillSearchRoots())
	}

	serialized, err := json.Marshal(snap)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(serialized), bodySentinel) {
		t.Fatalf("serialized snapshot leaked the SKILL.md body: %s", serialized)
	}
	if strings.Contains(string(serialized), dir) {
		t.Fatalf("serialized snapshot leaked an absolute host path (%q): %s", dir, serialized)
	}
	if snap.Skills[0].Path != "skills/demo-skill" {
		t.Fatalf("path label should stay manifest-relative, got %q", snap.Skills[0].Path)
	}
}

// TestHarnessSnapshotZeroSkillsUnchanged: a plan selecting zero skills yields
// an empty (nil) Skills view — no behavior change for the common case.
func TestHarnessSnapshotZeroSkillsUnchanged(t *testing.T) {
	dir := t.TempDir()
	manifest := `apiVersion: swarm.ai/v1alpha1
kind: Harness
metadata:
  name: skillsnapshot-zero
provider:
  id: anthropic
  model: claude-x
  credential:
    inline: test-key
agent:
  systemPrompt:
    inline: "hi"
  tools: []
permissions:
  approvalMode: readonly
`
	plan := compilePlanInDir(t, dir, manifest)
	c, err := New(WithHarnessPlan(plan))
	if err != nil {
		t.Fatalf("New(harness): %v", err)
	}
	t.Cleanup(func() { _ = c.Close() })

	snap := c.HarnessSnapshot()
	if len(snap.Skills) != 0 {
		t.Fatalf("snapshot.Skills = %#v, want empty for a zero-skills plan", snap.Skills)
	}
}
