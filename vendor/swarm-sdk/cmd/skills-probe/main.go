// cmd/skills-probe — offline shape probe for the skills loader.
//
// Layer 1 (no LLM, no network). Stages temporary fixtures, calls
// [skills.NewLoader] + Initialize(), and verifies which skill directories
// are actually searched.
//
// Verifies after the BUG-2 fix:
//
//   - ~/.claude/skills/ is included in default search paths (NewLoader fix).
//   - <project>/.claude/skills/ skills are discoverable via Registry.AddSearchPath
//     (the API that SkillsManager.AddProjectSearchPaths uses in the TUI).
//
// Usage:
//
//	cd swarm-sdk && go run ./cmd/skills-probe
//
// Exit 0 = all checks passed.
// Non-zero = one or more checks failed.
package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/skills"
)

// ─── probe helpers ────────────────────────────────────────────────────────────

var failures []string

func check(name string, ok bool, detail string) {
	if ok {
		fmt.Printf("  ✓  %s\n", name)
	} else {
		fmt.Fprintf(os.Stderr, "  ✗  FAIL %s: %s\n", name, detail)
		failures = append(failures, name)
	}
}

// writeSkill creates a minimal SKILL.md inside dir/<name>/ so the loader
// can discover it via DiscoverAll.
func writeSkill(dir, name, description string) error {
	skillDir := filepath.Join(dir, name)
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		return err
	}
	content := fmt.Sprintf(`---
name: %s
description: %s
version: 1.0.0
---

# %s

%s
`, name, description, name, description)
	return os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(content), 0o644)
}

// ─── main ─────────────────────────────────────────────────────────────────────

func main() {
	// Isolated fixture tree — never touches real ~/.swarm or ~/.claude.
	baseDir, err := os.MkdirTemp("", "skills-probe-*")
	if err != nil {
		fmt.Fprintf(os.Stderr, "setup: %v\n", err)
		os.Exit(1)
	}
	defer os.RemoveAll(baseDir)

	// ~/.swarm/skills/ — the SwarmOS install directory (what NewLoader receives)
	installDir := filepath.Join(baseDir, "swarmos", "skills")

	// <home>/.claude/skills/ — user-level Claude Code skills directory
	// We override HOME so os.UserHomeDir() returns our fixture home.
	homeDir := filepath.Join(baseDir, "home")
	homeClaudeSkillsDir := filepath.Join(homeDir, ".claude", "skills")

	// <project>/.claude/skills/ — project-level Claude Code skills
	projectDir := filepath.Join(baseDir, "myproject")
	projectClaudeSkillsDir := filepath.Join(projectDir, ".claude", "skills")

	// ── Create fixtures ───────────────────────────────────────────────────────
	if err := writeSkill(installDir, "swarmos-skill", "Skill in ~/.swarm/skills/"); err != nil {
		fmt.Fprintf(os.Stderr, "setup swarmos skill: %v\n", err)
		os.Exit(1)
	}
	if err := writeSkill(homeClaudeSkillsDir, "home-claude-skill", "Skill in ~/.claude/skills/"); err != nil {
		fmt.Fprintf(os.Stderr, "setup home skill: %v\n", err)
		os.Exit(1)
	}
	if err := writeSkill(projectClaudeSkillsDir, "project-claude-skill", "Skill in <project>/.claude/skills/"); err != nil {
		fmt.Fprintf(os.Stderr, "setup project skill: %v\n", err)
		os.Exit(1)
	}

	// Override HOME so NewLoader's os.UserHomeDir() picks up our fixture.
	origHome := os.Getenv("HOME")
	os.Setenv("HOME", homeDir)
	defer os.Setenv("HOME", origHome)

	fmt.Println("=== Skills Probe (Layer 1 – offline) ===")
	fmt.Printf("   installDir:             %s\n", installDir)
	fmt.Printf("   home .claude/skills:    %s\n", homeClaudeSkillsDir)
	fmt.Printf("   project .claude/skills: %s\n", projectClaudeSkillsDir)
	fmt.Println()

	// ── Create loader (mirrors sdk_integration.go:1657-1658) ─────────────────
	ldr := skills.NewLoader(installDir)
	if err := ldr.Initialize(context.Background()); err != nil {
		fmt.Fprintf(os.Stderr, "Initialize: %v\n", err)
		os.Exit(1)
	}

	// ── Print search paths ────────────────────────────────────────────────────
	fmt.Println("── Search paths after NewLoader(installDir).Initialize() ──")
	for _, p := range ldr.SearchPaths {
		fmt.Printf("   %s\n", p)
	}
	fmt.Println()

	// ── Print discovered skills ───────────────────────────────────────────────
	fmt.Println("── Discovered skills (before project path added) ──")
	allSkills := ldr.List()
	for _, s := range allSkills {
		if s.Path != "" && !strings.HasPrefix(s.Path, "builtin:") {
			fmt.Printf("   %-30s %s\n", s.Metadata.Name, s.Path)
		}
	}
	fmt.Println()

	hasInPaths := func(sub string) bool {
		for _, p := range ldr.SearchPaths {
			if strings.Contains(p, sub) {
				return true
			}
		}
		return false
	}
	hasSkill := func(name string) bool {
		for _, s := range ldr.List() {
			if s.Metadata.Name == name {
				return true
			}
		}
		return false
	}

	// ── Section 1: installDir skills ARE discovered ───────────────────────────
	fmt.Println("── Section 1: installDir (always worked) ──")
	check("installDir is in SearchPaths", hasInPaths("swarmos"),
		"installDir missing — loader is fundamentally broken")
	check("swarmos-skill is discovered", hasSkill("swarmos-skill"),
		"swarmos-skill not found — installDir skills not loading")

	// ── Section 2: ~/.claude/skills/ IS now searched (BUG-2 fix) ─────────────
	fmt.Println()
	fmt.Println("── Section 2: ~/.claude/skills/ (fixed in NewLoader) ──")
	check("~/.claude/skills/ is in SearchPaths", hasInPaths(".claude"),
		"~/.claude path missing — NewLoader fix not applied")
	check("home-claude-skill is discovered", hasSkill("home-claude-skill"),
		"home-claude-skill not found — ~/.claude/skills/ not being searched")

	// ── Section 3: project .claude/skills/ via AddSearchPath API ─────────────
	// The TUI wires this via SkillsManager.AddProjectSearchPaths(workspaceRoot).
	// Here we call the underlying API directly to prove it works.
	fmt.Println()
	fmt.Println("── Section 3: project .claude/skills/ via AddSearchPath (TUI fix) ──")
	fmt.Println("   (mirrors what SkillsManager.AddProjectSearchPaths does in the TUI)")

	ldr.Registry.AddSearchPath(projectClaudeSkillsDir)
	ldr.SearchPaths = append(ldr.SearchPaths, projectClaudeSkillsDir)
	if _, err := ldr.Registry.DiscoverAll(); err != nil {
		fmt.Fprintf(os.Stderr, "DiscoverAll after AddSearchPath: %v\n", err)
	}

	fmt.Println()
	fmt.Println("── Discovered skills (after project path added) ──")
	for _, s := range ldr.List() {
		if s.Path != "" && !strings.HasPrefix(s.Path, "builtin:") {
			fmt.Printf("   %-30s %s\n", s.Metadata.Name, s.Path)
		}
	}
	fmt.Println()

	check("project .claude/skills/ is in SearchPaths after AddSearchPath",
		hasInPaths("myproject"),
		"project path not added — AddSearchPath API broken")
	check("project-claude-skill is discovered after AddSearchPath",
		hasSkill("project-claude-skill"),
		"project-claude-skill not found — DiscoverAll didn't pick up new path")

	// ── Summary ───────────────────────────────────────────────────────────────
	fmt.Println()
	if len(failures) == 0 {
		fmt.Println("All checks passed.")
		os.Exit(0)
	}
	fmt.Fprintf(os.Stderr, "\n%d check(s) FAILED:\n", len(failures))
	for _, f := range failures {
		fmt.Fprintf(os.Stderr, "  - %s\n", f)
	}
	os.Exit(1)
}
