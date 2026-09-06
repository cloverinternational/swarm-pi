// Phase 5c tests: skills-only changes must be classified (never silently
// applied) and a hot re-apply to a zero-skills plan must leave c.skillRegistry
// nil (not stale). See harness_apply.go (classifyHarnessPlanChange, the skills
// comparison) and harness_plan.go (initHarnessAgent's unconditional
// c.skillRegistry assignment).
package client

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// skillsBaseManifest is a hot-apply baseline carrying one path-backed skill,
// anthropic/inline-cred so no live provider handshake is needed.
const skillsBaseManifest = `apiVersion: swarm.ai/v1alpha1
kind: Harness
metadata:
  name: apply-skills
provider:
  id: anthropic
  model: claude-x
  credential:
    inline: test-key
agent:
  systemPrompt:
    inline: "PROMPT-ONE"
  tools:
    - forge.read
  limits:
    maxOutputTokens: 4096
    maxTurns: 12
    timeoutSeconds: 600
skills:
  - id: demo-skill
    path: skills/demo-skill
permissions:
  approvalMode: interactive
`

// skillsZeroManifest is the same baseline with an EMPTY skills selection.
const skillsZeroManifest = `apiVersion: swarm.ai/v1alpha1
kind: Harness
metadata:
  name: apply-skills
provider:
  id: anthropic
  model: claude-x
  credential:
    inline: test-key
agent:
  systemPrompt:
    inline: "PROMPT-ONE"
  tools:
    - forge.read
  limits:
    maxOutputTokens: 4096
    maxTurns: 12
    timeoutSeconds: 600
skills: []
permissions:
  approvalMode: interactive
`

// twoSkillsManifest declares TWO path-backed skills (adds "demo-skill-2" to
// the baseline selection).
const twoSkillsManifest = `apiVersion: swarm.ai/v1alpha1
kind: Harness
metadata:
  name: apply-skills
provider:
  id: anthropic
  model: claude-x
  credential:
    inline: test-key
agent:
  systemPrompt:
    inline: "PROMPT-ONE"
  tools:
    - forge.read
  limits:
    maxOutputTokens: 4096
    maxTurns: 12
    timeoutSeconds: 600
skills:
  - id: demo-skill
    path: skills/demo-skill
  - id: demo-skill-2
    path: skills/demo-skill-2
permissions:
  approvalMode: interactive
`

// swappedSkillManifest selects ONLY "demo-skill-2" (the baseline's skill is
// dropped, a different one is added) — an apply-parity "selection changed"
// scenario distinct from the pure add/zero cases above.
const swappedSkillManifest = `apiVersion: swarm.ai/v1alpha1
kind: Harness
metadata:
  name: apply-skills
provider:
  id: anthropic
  model: claude-x
  credential:
    inline: test-key
agent:
  systemPrompt:
    inline: "PROMPT-ONE"
  tools:
    - forge.read
  limits:
    maxOutputTokens: 4096
    maxTurns: 12
    timeoutSeconds: 600
skills:
  - id: demo-skill-2
    path: skills/demo-skill-2
permissions:
  approvalMode: interactive
`

// idOnlySkillsRootAManifest / idOnlySkillsRootBManifest are identical except
// for their declared search root, exercising a skills-only change that comes
// purely from a searchRoots edit (id-only entry, no path/hash change at all).
const idOnlySkillsRootAManifest = `apiVersion: swarm.ai/v1alpha1
kind: Harness
metadata:
  name: apply-skills
provider:
  id: anthropic
  model: claude-x
  credential:
    inline: test-key
agent:
  systemPrompt:
    inline: "PROMPT-ONE"
  tools:
    - forge.read
  limits:
    maxOutputTokens: 4096
    maxTurns: 12
    timeoutSeconds: 600
skills:
  entries:
    - id: demo-skill
  searchRoots:
    - skillsA
permissions:
  approvalMode: interactive
`

const idOnlySkillsRootBManifest = `apiVersion: swarm.ai/v1alpha1
kind: Harness
metadata:
  name: apply-skills
provider:
  id: anthropic
  model: claude-x
  credential:
    inline: test-key
agent:
  systemPrompt:
    inline: "PROMPT-ONE"
  tools:
    - forge.read
  limits:
    maxOutputTokens: 4096
    maxTurns: 12
    timeoutSeconds: 600
skills:
  entries:
    - id: demo-skill
  searchRoots:
    - skillsB
permissions:
  approvalMode: interactive
`

// TestClassifyHarnessPlanChangeSkillsAdded: adding a second skill classifies a
// redacted skills change as hot (never restart/forbidden, never silent).
func TestClassifyHarnessPlanChangeSkillsAdded(t *testing.T) {
	dir := t.TempDir()
	writeClientSkill(t, dir, "demo-skill", "DEMO_ONE_BODY_SECRET\n")
	writeClientSkill(t, dir, "demo-skill-2", "DEMO_TWO_BODY_SECRET\n")
	oldPlan := compilePlanInDir(t, dir, skillsBaseManifest)
	newPlan := compilePlanInDir(t, dir, twoSkillsManifest)

	diff, hot, restart, forbidden, err := classifyHarnessPlanChange(oldPlan, newPlan, false)
	if err != nil {
		t.Fatalf("classifyHarnessPlanChange: unexpected error: %v", err)
	}
	if containsStr(restart, harnessFieldSkills) || containsStr(forbidden, harnessFieldSkills) {
		t.Fatalf("skills change must never be restart/forbidden: restart=%v forbidden=%v", restart, forbidden)
	}
	if !containsStr(hot, harnessFieldSkills) {
		t.Fatalf("hot = %v, want to include %q", hot, harnessFieldSkills)
	}
	var found bool
	for _, ch := range diff.Changes {
		if ch.Field != harnessFieldSkills {
			continue
		}
		found = true
		if ch.Class != harnessClassHot {
			t.Errorf("skills change class = %q, want hot", ch.Class)
		}
		for _, label := range []string{ch.Old, ch.New} {
			if strings.Contains(label, "SECRET") {
				t.Errorf("skills diff label leaks a SKILL.md body: %q", label)
			}
			if strings.Contains(label, dir) {
				t.Errorf("skills diff label leaks an absolute host path: %q", label)
			}
		}
		if !strings.Contains(ch.New, "demo-skill-2") {
			t.Errorf("new label %q does not mention added skill id \"demo-skill-2\"", ch.New)
		}
		if !strings.Contains(ch.Old, "demo-skill") {
			t.Errorf("old label %q does not mention skill id \"demo-skill\"", ch.Old)
		}
	}
	if !found {
		t.Fatalf("Diff.Changes = %+v, want a %q entry", diff.Changes, harnessFieldSkills)
	}
}

// TestClassifyHarnessPlanChangeSkillsContentHashOnly: the SAME skill id/path
// with different on-disk content (different resolved ContentHash) is still a
// classified, non-silent hot skills change.
func TestClassifyHarnessPlanChangeSkillsContentHashOnly(t *testing.T) {
	dir := t.TempDir()
	writeClientSkill(t, dir, "demo-skill", "BODY-VERSION-A\n")
	oldPlan := compilePlanInDir(t, dir, skillsBaseManifest)

	writeClientSkill(t, dir, "demo-skill", "BODY-VERSION-B-DIFFERENT-LENGTH\n")
	newPlan := compilePlanInDir(t, dir, skillsBaseManifest)

	if oldPlan.Skills()[0].ContentHash == newPlan.Skills()[0].ContentHash {
		t.Fatalf("test setup invalid: content hash did not change")
	}

	diff, hot, restart, forbidden, err := classifyHarnessPlanChange(oldPlan, newPlan, false)
	if err != nil {
		t.Fatalf("classifyHarnessPlanChange: unexpected error: %v", err)
	}
	if !containsStr(hot, harnessFieldSkills) {
		t.Fatalf("hot = %v, want to include %q", hot, harnessFieldSkills)
	}
	if containsStr(restart, harnessFieldSkills) || containsStr(forbidden, harnessFieldSkills) {
		t.Fatalf("content-hash-only skills change must be hot, not restart/forbidden")
	}
	for _, ch := range diff.Changes {
		if ch.Field == harnessFieldSkills && ch.Old == ch.New {
			t.Errorf("skills labels identical despite content hash change: %q", ch.Old)
		}
	}
}

// TestClassifyHarnessPlanChangeSkillsSearchRootsOnly: an id-only skill entry
// whose ONLY change is the declared search root still produces a classified
// hot skills change (roots are part of the label).
func TestClassifyHarnessPlanChangeSkillsSearchRootsOnly(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "skillsA"), 0o755); err != nil {
		t.Fatalf("mkdir skillsA: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "skillsB"), 0o755); err != nil {
		t.Fatalf("mkdir skillsB: %v", err)
	}
	oldPlan := compilePlanInDir(t, dir, idOnlySkillsRootAManifest)
	newPlan := compilePlanInDir(t, dir, idOnlySkillsRootBManifest)

	diff, hot, restart, forbidden, err := classifyHarnessPlanChange(oldPlan, newPlan, false)
	if err != nil {
		t.Fatalf("classifyHarnessPlanChange: unexpected error: %v", err)
	}
	if !containsStr(hot, harnessFieldSkills) {
		t.Fatalf("hot = %v, want to include %q", hot, harnessFieldSkills)
	}
	if containsStr(restart, harnessFieldSkills) || containsStr(forbidden, harnessFieldSkills) {
		t.Fatalf("search-roots-only skills change must be hot, not restart/forbidden")
	}
	var found bool
	for _, ch := range diff.Changes {
		if ch.Field != harnessFieldSkills {
			continue
		}
		found = true
		if !strings.Contains(ch.Old, "skillsA") || !strings.Contains(ch.New, "skillsB") {
			t.Errorf("skills labels do not reflect search-root change: old=%q new=%q", ch.Old, ch.New)
		}
		if strings.Contains(ch.Old, dir) || strings.Contains(ch.New, dir) {
			t.Errorf("skills labels leak an absolute host path: old=%q new=%q", ch.Old, ch.New)
		}
	}
	if !found {
		t.Fatalf("Diff.Changes = %+v, want a %q entry", diff.Changes, harnessFieldSkills)
	}
}

// TestClassifyHarnessPlanChangeSkillsUnchanged: when skills (and search roots)
// are byte-identical but SOME other hot field differs, no skills diff entry
// is reported (no spurious noise).
func TestClassifyHarnessPlanChangeSkillsUnchanged(t *testing.T) {
	dir := t.TempDir()
	writeClientSkill(t, dir, "demo-skill", "STABLE_BODY\n")
	oldPlan := compilePlanInDir(t, dir, skillsBaseManifest)

	const changedTurnsManifest = `apiVersion: swarm.ai/v1alpha1
kind: Harness
metadata:
  name: apply-skills
provider:
  id: anthropic
  model: claude-x
  credential:
    inline: test-key
agent:
  systemPrompt:
    inline: "PROMPT-ONE"
  tools:
    - forge.read
  limits:
    maxOutputTokens: 4096
    maxTurns: 99
    timeoutSeconds: 600
skills:
  - id: demo-skill
    path: skills/demo-skill
permissions:
  approvalMode: interactive
`
	newPlan := compilePlanInDir(t, dir, changedTurnsManifest)

	diff, hot, _, _, err := classifyHarnessPlanChange(oldPlan, newPlan, false)
	if err != nil {
		t.Fatalf("classifyHarnessPlanChange: unexpected error: %v", err)
	}
	if !containsStr(hot, harnessFieldMaxTurns) {
		t.Fatalf("test setup invalid: maxTurns not classified hot: %v", hot)
	}
	if containsStr(hot, harnessFieldSkills) {
		t.Errorf("hot = %v, must NOT include %q for an unchanged skill selection", hot, harnessFieldSkills)
	}
	for _, ch := range diff.Changes {
		if ch.Field == harnessFieldSkills {
			t.Errorf("Diff.Changes unexpectedly contains a %q entry for unchanged skills: %+v", harnessFieldSkills, ch)
		}
	}
}

// TestApplyHarnessPlanSkillsOnlyChange: apply parity. A hot re-apply that
// changes ONLY the skill selection is reported via result.Applied, and the
// live client's Skill tool/registry reflects the NEW selection exactly (the
// old skill id no longer resolves; the new one does).
func TestApplyHarnessPlanSkillsOnlyChange(t *testing.T) {
	dir := t.TempDir()
	writeClientSkill(t, dir, "demo-skill", "DEMO_ONE_BODY\n")
	writeClientSkill(t, dir, "demo-skill-2", "DEMO_TWO_BODY\n")
	oldPlan := compilePlanInDir(t, dir, skillsBaseManifest)

	c, err := New(WithHarnessPlan(oldPlan))
	if err != nil {
		t.Fatalf("New(harness): %v", err)
	}
	t.Cleanup(func() { _ = c.Close() })

	if c.skillRegistry == nil {
		t.Fatalf("skillRegistry nil before apply")
	}
	if _, ok := c.skillRegistry.Get("demo-skill"); !ok {
		t.Fatalf("baseline registry does not resolve \"demo-skill\"")
	}

	newPlan := compilePlanInDir(t, dir, swappedSkillManifest)
	res, err := c.ApplyHarnessPlan(context.Background(), newPlan, ApplyHarnessOptions{})
	if err != nil {
		t.Fatalf("ApplyHarnessPlan skills-only: unexpected error: %v", err)
	}
	if len(res.Forbidden) != 0 || len(res.RestartRequired) != 0 {
		t.Fatalf("skills-only apply misclassified: forbidden=%v restart=%v", res.Forbidden, res.RestartRequired)
	}
	if !containsStr(res.Applied, harnessFieldSkills) {
		t.Fatalf("Applied = %v, want to include %q", res.Applied, harnessFieldSkills)
	}
	if res.Digest != newPlan.Digest() {
		t.Errorf("result digest = %q, want new plan digest %q", res.Digest, newPlan.Digest())
	}

	if !containsStr(providerToolNames(c), "Skill") {
		t.Errorf("provider tools %v missing \"Skill\" after skills-only apply", providerToolNames(c))
	}
	if c.skillRegistry == nil {
		t.Fatalf("skillRegistry nil after skills-only apply")
	}
	if _, ok := c.skillRegistry.Get("demo-skill-2"); !ok {
		t.Errorf("post-apply registry does not resolve new selection \"demo-skill-2\"")
	}
	if _, ok := c.skillRegistry.Get("demo-skill"); ok {
		t.Errorf("post-apply registry still resolves dropped selection \"demo-skill\"")
	}
}

// TestApplyHarnessPlanSkillsToZeroClearsRegistry: the MINOR fix. A hot
// re-apply from a with-skills plan to a ZERO-skills plan succeeds, drops the
// Skill tool, and leaves c.skillRegistry == nil (via LoadedSkills(), the
// existing public surface, rather than exporting internals just for the test).
func TestApplyHarnessPlanSkillsToZeroClearsRegistry(t *testing.T) {
	dir := t.TempDir()
	writeClientSkill(t, dir, "demo-skill", "DEMO_ONE_BODY\n")
	oldPlan := compilePlanInDir(t, dir, skillsBaseManifest)

	c, err := New(WithHarnessPlan(oldPlan))
	if err != nil {
		t.Fatalf("New(harness): %v", err)
	}
	t.Cleanup(func() { _ = c.Close() })

	if c.skillRegistry == nil {
		t.Fatalf("skillRegistry nil before apply")
	}
	if len(c.LoadedSkills()) == 0 {
		t.Fatalf("LoadedSkills() empty before apply, want the baseline skill")
	}

	newPlan := compilePlanInDir(t, dir, skillsZeroManifest)
	res, err := c.ApplyHarnessPlan(context.Background(), newPlan, ApplyHarnessOptions{})
	if err != nil {
		t.Fatalf("ApplyHarnessPlan skills-to-zero: unexpected error: %v", err)
	}
	if len(res.Forbidden) != 0 || len(res.RestartRequired) != 0 {
		t.Fatalf("skills-to-zero apply misclassified: forbidden=%v restart=%v", res.Forbidden, res.RestartRequired)
	}
	if !containsStr(res.Applied, harnessFieldSkills) {
		t.Fatalf("Applied = %v, want to include %q", res.Applied, harnessFieldSkills)
	}

	if containsStr(providerToolNames(c), "Skill") {
		t.Errorf("provider tools %v still contain \"Skill\" after skills-to-zero apply", providerToolNames(c))
	}
	if got := c.LoadedSkills(); len(got) != 0 {
		t.Errorf("LoadedSkills() = %v, want empty after skills-to-zero apply (c.skillRegistry must be nil)", got)
	}
	if c.skillRegistry != nil {
		t.Errorf("c.skillRegistry must be nil after a hot apply to a zero-skills plan (stale-pointer regression)")
	}
}
