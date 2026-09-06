package skills

import (
	"os"
	"path/filepath"
	"testing"
)

// TestDiscoverSkillsSkipsArchive guards the fix for the curator/loader
// disagreement: the autogenskills curator retires unused skills by moving them
// into <root>/archive/, but discovery used to recurse into that directory and
// re-inject every archived skill into the system prompt — so archiving
// reclaimed zero context. Discovery must skip "archive".
func TestDiscoverSkillsSkipsArchive(t *testing.T) {
	root := t.TempDir()

	write := func(dir, name string) {
		t.Helper()
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		md := "---\nname: " + name + "\ndescription: regression fixture skill for the archive-skip test\n---\n# " + name + "\n"
		if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(md), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	write(filepath.Join(root, "active-skill"), "active-skill")
	write(filepath.Join(root, "archive", "retired-skill"), "retired-skill")
	write(filepath.Join(root, "autogen", "archive", "deep-retired-skill"), "deep-retired-skill")

	got, err := DiscoverSkills(root)
	if err != nil {
		t.Fatal(err)
	}

	names := map[string]bool{}
	for _, s := range got {
		names[s.Metadata.Name] = true
	}

	if !names["active-skill"] {
		t.Errorf("active skill was not discovered: %v", names)
	}
	if names["retired-skill"] || names["deep-retired-skill"] {
		t.Errorf("archived skill(s) leaked into discovery — archive/ must be skipped at any depth: %v", names)
	}
}
