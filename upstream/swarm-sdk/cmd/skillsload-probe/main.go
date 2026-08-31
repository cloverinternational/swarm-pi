// Command skillsload-probe replicates the TUI's skills loader initialization
// and reports whether specific autogen skills made it into the registry.
// Temporary diagnostic for the curator disk-vs-registry split-brain.
package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/paths"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/skills"
)

func main() {
	installDir := paths.SkillsDir()

	loader := skills.NewLoader(installDir)
	if err := loader.Initialize(context.Background()); err != nil {
		fmt.Println("init error:", err)
		os.Exit(1)
	}

	all := loader.List()
	autogenCount := 0
	for _, sk := range all {
		if sk.Source == "autogen" {
			autogenCount++
		}
	}
	fmt.Printf("total skills in registry: %d (autogen: %d)\n", len(all), autogenCount)

	ghosts := []string{
		"explore_project",
		"find_version_definition",
		"tui-slash-command-as-prompt",
		"clinear-transcript-to-issues",
		"remove-reverse-engineering-vocabulary",
		"api-key-auth", // control: agent could view this one
	}
	for _, name := range ghosts {
		sk, ok := loader.Registry.Get(name)
		if ok {
			fmt.Printf("  %-42s FOUND  source=%s path=%s\n", name, sk.Source, sk.Path)
		} else {
			fmt.Printf("  %-42s MISSING\n", name)
		}
	}

	// Count how many autogen dirs on disk are NOT in the registry.
	autogenDir := paths.AutogenSkillsDir()
	entries, _ := os.ReadDir(autogenDir)
	missing := 0
	for _, e := range entries {
		if !e.IsDir() || e.Name() == "archive" {
			continue
		}
		if _, err := os.Stat(filepath.Join(autogenDir, e.Name(), "SKILL.md")); err != nil {
			continue
		}
		if _, ok := loader.Registry.Get(e.Name()); !ok {
			missing++
			_, _, loadErr := skills.LoadSkillWithValidation(filepath.Join(autogenDir, e.Name()))
			fmt.Printf("  DROPPED %-50s err=%v\n", e.Name(), loadErr)
		}
	}
	fmt.Printf("autogen dirs on disk missing from registry: %d\n", missing)
}
