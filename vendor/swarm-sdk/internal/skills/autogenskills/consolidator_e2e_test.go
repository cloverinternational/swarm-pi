package autogenskills_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/skills"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/skills/autogenskills"
)

// TestEndToEnd_ArchiveShrinksRenderedPrompt is the in-practice proof for the
// daemon skill consolidator: it exercises the REAL prompt-injection path
// (DiscoverSkills -> GenerateAvailableSkillsXML) and verifies that running the
// curator's Archive step actually removes a skill from the rendered
// <available_skills> block. Before this work, archiving reclaimed zero context
// because nothing triggered the curator under the daemon.
func TestEndToEnd_ArchiveShrinksRenderedPrompt(t *testing.T) {
	autogenDir := t.TempDir()

	writeSkill := func(name string) {
		t.Helper()
		dir := filepath.Join(autogenDir, name)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		md := "---\nname: " + name + "\ndescription: e2e fixture skill for consolidator proof\n---\n# " + name + "\n"
		if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(md), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	// Seed several autogen skills.
	for _, n := range []string{"alpha-skill", "beta-skill", "gamma-skill"} {
		writeSkill(n)
	}

	// Render the prompt the way the system actually does it.
	before, err := skills.DiscoverSkills(autogenDir)
	if err != nil {
		t.Fatalf("DiscoverSkills(before): %v", err)
	}
	xmlBefore := skills.GenerateAvailableSkillsXML(before)
	if !strings.Contains(xmlBefore, "beta-skill") {
		t.Fatalf("expected beta-skill in rendered prompt before archive:\n%s", xmlBefore)
	}
	countBefore := strings.Count(xmlBefore, "<skill>")
	if countBefore != 3 {
		t.Fatalf("expected 3 skills rendered before archive, got %d:\n%s", countBefore, xmlBefore)
	}

	// Run the curator's archive step on an unused skill (this is the action the
	// daemon now triggers via RunCuratorIfDue -> runCurator -> rule-based pass).
	curator := autogenskills.NewCurator(
		autogenskills.CuratorConfig{}.WithDefaults(),
		nil,
		filepath.Join(autogenDir, ".curator_state"),
	)
	if _, err := curator.Archive(autogenDir, "beta-skill"); err != nil {
		t.Fatalf("Archive(beta-skill): %v", err)
	}

	// Re-render: the archived skill must be gone from the prompt.
	after, err := skills.DiscoverSkills(autogenDir)
	if err != nil {
		t.Fatalf("DiscoverSkills(after): %v", err)
	}
	xmlAfter := skills.GenerateAvailableSkillsXML(after)
	if strings.Contains(xmlAfter, "beta-skill") {
		t.Fatalf("beta-skill leaked into prompt after archive — consolidation did not reclaim context:\n%s", xmlAfter)
	}
	countAfter := strings.Count(xmlAfter, "<skill>")
	if countAfter != 2 {
		t.Fatalf("expected 2 skills rendered after archive, got %d:\n%s", countAfter, xmlAfter)
	}

	t.Logf("PROOF: rendered skills shrank %d -> %d after curator archive", countBefore, countAfter)
}
