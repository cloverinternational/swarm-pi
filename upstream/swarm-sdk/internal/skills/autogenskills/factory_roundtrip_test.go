package autogenskills

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/skills"
)

// TestCreateSkillRoundTripsThroughStrictLoader is the regression test for the
// disk-vs-registry split-brain: the factory wrote frontmatter with unquoted
// scalars, so any description containing ": " (or other YAML-special syntax)
// produced a SKILL.md that skills.LoadSkill could not parse. The curator (raw
// disk walk) still saw those skills while SkillManage (registry) could not —
// 57 of 224 real autogen skills were silently invisible to the consolidator.
func TestCreateSkillRoundTripsThroughStrictLoader(t *testing.T) {
	hostile := []struct {
		name string
		opts CreateOptions
	}{
		{
			name: "colon-space in description",
			opts: CreateOptions{
				Name:          "colon-desc",
				Description:   "CRITICAL: addNotification() is invisible — always use LoopInjectPromptMsg",
				Instructions:  "body",
				TriggerReason: TriggerLLMNudge,
			},
		},
		{
			name: "leading bracket and quotes",
			opts: CreateOptions{
				Name:          "bracket-desc",
				Description:   `[edge] "quoted" #comment-looking {flow} value: with colon`,
				Instructions:  "body",
				TriggerReason: TriggerLLMNudge,
			},
		},
		{
			name: "hostile tags and category",
			opts: CreateOptions{
				Name:          "hostile-tags",
				Description:   "plain",
				Category:      "a: b",
				Tags:          []string{"ok", "needs: quoting", "wei\"rd"},
				Instructions:  "body",
				TriggerReason: TriggerLLMNudge,
			},
		},
	}

	for _, tc := range hostile {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			cfg := Config{Mode: ModeAuto, AutogenDir: dir}
			if err := cfg.Validate(); err != nil {
				t.Fatalf("cfg: %v", err)
			}
			reg := skills.NewRegistry()
			factory, err := NewSkillFactory(cfg, reg, nil)
			if err != nil {
				t.Fatalf("factory: %v", err)
			}

			res := factory.Create(tc.opts)
			if res.Error != nil {
				t.Fatalf("create: %v", res.Error)
			}

			// The strict loader (what the TUI registry uses) must be able to
			// read back what the factory wrote.
			loaded, verrs, err := skills.LoadSkillWithValidation(filepath.Dir(res.Path))
			if err != nil {
				data, _ := os.ReadFile(res.Path)
				t.Fatalf("strict loader rejected factory output: %v\nSKILL.md:\n%s", err, data)
			}
			for _, ve := range verrs {
				if ve.Fatal {
					t.Fatalf("fatal validation error: %s", ve.Error())
				}
			}
			if loaded.Metadata.Name != tc.opts.Name {
				t.Errorf("name mismatch: got %q want %q", loaded.Metadata.Name, tc.opts.Name)
			}
			if loaded.Metadata.Description != tc.opts.Description {
				t.Errorf("description mismatch:\n got %q\nwant %q", loaded.Metadata.Description, tc.opts.Description)
			}
			if len(tc.opts.Tags) > 0 && len(loaded.Metadata.Tags) != len(tc.opts.Tags) {
				t.Errorf("tags mismatch: got %v want %v", loaded.Metadata.Tags, tc.opts.Tags)
			}
		})
	}
}

// TestPatchSkillRoundTripsThroughStrictLoader ensures Patch (which rewrites the
// whole SKILL.md via buildSkillMarkdownFromFrontmatter) also emits valid YAML,
// including when the pre-existing file used the legacy unquoted format.
func TestPatchSkillRoundTripsThroughStrictLoader(t *testing.T) {
	dir := t.TempDir()
	cfg := Config{Mode: ModeAuto, AutogenDir: dir}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("cfg: %v", err)
	}
	reg := skills.NewRegistry()
	factory, err := NewSkillFactory(cfg, reg, nil)
	if err != nil {
		t.Fatalf("factory: %v", err)
	}

	// Simulate a legacy broken file written by the old factory: unquoted
	// description containing ": ".
	skillDir := filepath.Join(dir, "legacy-skill")
	if err := os.MkdirAll(skillDir, 0755); err != nil {
		t.Fatal(err)
	}
	legacy := "---\nname: legacy-skill\ndescription: Wire a command. CRITICAL: quoting breaks\nversion: 1.0.0\nauthor: swarm-autogen\ntags: [tui, slash-command]\ntrigger_reason: LLM nudge response\n---\n\nold body\n"
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(legacy), 0644); err != nil {
		t.Fatal(err)
	}

	res := factory.Patch(PatchOptions{Name: "legacy-skill", Instructions: "new body"})
	if res.Error != nil {
		t.Fatalf("patch: %v", res.Error)
	}

	loaded, _, err := skills.LoadSkillWithValidation(skillDir)
	if err != nil {
		data, _ := os.ReadFile(filepath.Join(skillDir, "SKILL.md"))
		t.Fatalf("strict loader rejected patched output: %v\nSKILL.md:\n%s", err, data)
	}
	if !strings.Contains(loaded.Metadata.Description, "CRITICAL: quoting breaks") {
		t.Errorf("description lost in patch: %q", loaded.Metadata.Description)
	}
	if loaded.Metadata.Version != "1.0.1" {
		t.Errorf("version not bumped: %q", loaded.Metadata.Version)
	}
}
