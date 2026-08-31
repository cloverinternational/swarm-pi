// cmd/hooks-probe — offline shape probe for the hooks loader.
//
// Layer 1 (no LLM, no network). Stages temporary fixtures, calls
// [loader.LoadDefault], and verifies which sources are actually read.
//
// Known bugs this probe exposes:
//
//   - BUG-1: .claude/settings.local.json is never read by LoadDefault.
//     Claude Code reads it as the highest-priority project override;
//     Swarm silently ignores it.
//
// Usage:
//
//	cd swarm-sdk && go run ./cmd/hooks-probe
//
// Exit 0 = all checks passed (bugs are fixed).
// Non-zero = one or more checks failed (bugs still present).
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/hooks"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/hooks/loader"
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

func writeJSON(path string, v any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.Marshal(v)
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

func claudeHook(command string) map[string]any {
	return map[string]any{
		"hooks": map[string]any{
			"PreToolUse": []map[string]any{{
				"matcher": "Bash",
				"hooks": []map[string]any{{
					"type":    "command",
					"command": command,
				}},
			}},
		},
	}
}

// loadHooks calls the real production loader for a given projectDir.
// It returns the flat slice of ShellHookConfig the loader assembled.
func loadHooks(projectDir string) ([]*hooks.ShellHookConfig, error) {
	cfg, err := loader.LoadDefault(projectDir)
	if err != nil {
		return nil, err
	}
	if cfg == nil {
		return nil, nil
	}
	return cfg.Hooks, nil
}

// findCommand returns the first hook whose Command field matches cmd, or nil.
func findCommand(cfgs []*hooks.ShellHookConfig, cmd string) *hooks.ShellHookConfig {
	for _, h := range cfgs {
		if h.Command == cmd {
			return h
		}
	}
	return nil
}

// ─── main ─────────────────────────────────────────────────────────────────────

func main() {
	// Isolated fixture tree so we don't touch the real ~/.claude.
	baseDir, err := os.MkdirTemp("", "hooks-probe-*")
	if err != nil {
		fmt.Fprintf(os.Stderr, "setup: %v\n", err)
		os.Exit(1)
	}
	defer os.RemoveAll(baseDir)

	homeDir := filepath.Join(baseDir, "home")
	projectDir := filepath.Join(baseDir, "project")

	// Override HOME so loader.LoadDefault picks up our fixture ~/.claude.
	origHome := os.Getenv("HOME")
	os.Setenv("HOME", homeDir)
	defer os.Setenv("HOME", origHome)

	fmt.Println("=== Hooks Probe (Layer 1 – offline) ===")
	fmt.Printf("   home:    %s\n", homeDir)
	fmt.Printf("   project: %s\n", projectDir)
	fmt.Println()

	// ── Section 1: user-level ~/.claude/settings.json ────────────────────────
	fmt.Println("── Section 1: user-level ~/.claude/settings.json ──")

	if err := writeJSON(filepath.Join(homeDir, ".claude", "settings.json"),
		claudeHook("echo user-hook")); err != nil {
		fmt.Fprintf(os.Stderr, "write user settings: %v\n", err)
		os.Exit(1)
	}

	cfg1, loadErr := loadHooks(projectDir)
	if loadErr != nil {
		fmt.Fprintf(os.Stderr, "LoadDefault: %v\n", loadErr)
		os.Exit(1)
	}
	{
		found := findCommand(cfg1, "echo user-hook")
		check("user hook is loaded", found != nil,
			"echo user-hook not found in LoadDefault output")
		if found != nil {
			check("user hook is enabled", found.Enabled,
				fmt.Sprintf("Enabled=%v – should default to true for Claude Code hooks", found.Enabled))
		}
	}

	// ── Section 2: project-level .claude/settings.json ───────────────────────
	fmt.Println()
	fmt.Println("── Section 2: project-level .claude/settings.json ──")

	if err := writeJSON(filepath.Join(projectDir, ".claude", "settings.json"),
		claudeHook("echo project-hook")); err != nil {
		fmt.Fprintf(os.Stderr, "write project settings: %v\n", err)
		os.Exit(1)
	}

	cfg2, loadErr := loadHooks(projectDir)
	if loadErr != nil {
		fmt.Fprintf(os.Stderr, "LoadDefault: %v\n", loadErr)
		os.Exit(1)
	}
	{
		found := findCommand(cfg2, "echo project-hook")
		check("project hook is loaded", found != nil,
			"echo project-hook not found — project .claude/settings.json ignored")
		if found != nil {
			check("project hook is enabled", found.Enabled,
				fmt.Sprintf("Enabled=%v – should default to true for Claude Code hooks", found.Enabled))
		}
	}

	// ── Section 3: BUG-1 – .claude/settings.local.json is NOT read ───────────
	fmt.Println()
	fmt.Println("── Section 3: .claude/settings.local.json (BUG-1 – expected to FAIL) ──")
	fmt.Println("   Claude Code reads this as the highest-priority project override.")
	fmt.Println("   Swarm's loader.LoadDefault does not read this file at all.")

	// Write a local override with a distinct PostToolUse hook.
	localSettings := map[string]any{
		"hooks": map[string]any{
			"PostToolUse": []map[string]any{{
				"hooks": []map[string]any{{
					"type":    "command",
					"command": "echo local-override-hook",
				}},
			}},
		},
	}
	if err := writeJSON(filepath.Join(projectDir, ".claude", "settings.local.json"),
		localSettings); err != nil {
		fmt.Fprintf(os.Stderr, "write local settings: %v\n", err)
		os.Exit(1)
	}

	cfg3, loadErr := loadHooks(projectDir)
	if loadErr != nil {
		fmt.Fprintf(os.Stderr, "LoadDefault: %v\n", loadErr)
		os.Exit(1)
	}

	fmt.Printf("   Total hooks from LoadDefault: %d\n", len(cfg3))
	for _, h := range cfg3 {
		fmt.Printf("   %-40s enabled=%-5v cmd=%s\n", h.Name, h.Enabled, h.Command)
	}
	fmt.Println()

	found3 := findCommand(cfg3, "echo local-override-hook")
	check("settings.local.json hook IS loaded [BUG-1: currently NOT loaded]",
		found3 != nil,
		"echo local-override-hook not in output — LoadDefault skips settings.local.json")

	// ── Section 4: all hooks must be enabled ─────────────────────────────────
	fmt.Println()
	fmt.Println("── Section 4: all loaded hooks must be enabled ──")

	allEnabled := true
	for _, h := range cfg3 {
		if !h.Enabled {
			allEnabled = false
			fmt.Fprintf(os.Stderr, "   hook %q has Enabled=false\n", h.Name)
		}
	}
	check("all loaded Claude Code hooks have Enabled=true", allEnabled,
		"one or more hooks are Enabled=false — they will never fire")

	// ── Summary ───────────────────────────────────────────────────────────────
	fmt.Println()
	if len(failures) == 0 {
		fmt.Println("All checks passed.")
		os.Exit(0)
	}
	fmt.Fprintf(os.Stderr, "\n%d check(s) FAILED (bugs still present):\n", len(failures))
	for _, f := range failures {
		fmt.Fprintf(os.Stderr, "  - %s\n", f)
	}
	os.Exit(1)
}
