// Package gitprotect provides the protected-branch guardrail configuration
// used by the SDK's protected-branch hook and any consumer (TUI, daemons,
// custom harnesses) that wants to configure branch protection for a repo.
//
// Configuration is persisted in a committed, root-level file named
// "config.swarm" (JSON content, ".swarm" extension) so that protection policy
// is shared with the team via version control — unlike the frequently
// gitignored ".swarm/" directory.
package gitprotect

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// ConfigFileName is the repo-root file that stores protection policy. It is
// intentionally NOT inside the ".swarm/" directory (which is commonly
// gitignored) so the policy is committed and shared across the team.
const ConfigFileName = "config.swarm"

// DefaultProtectedBranches are the branches guarded when protection is enabled
// without naming specific branches.
var DefaultProtectedBranches = []string{"main", "master"}

// Config is the protected-branch policy for a single repository.
type Config struct {
	// Protected, when true, enables the guardrail: mutating tool calls (file
	// writes/edits and mutating shell/git commands) are hard-blocked while the
	// repo is checked out on one of the protected branches.
	Protected bool `json:"protected,omitempty"`

	// ProtectedBranches lists the branch names guarded when Protected is true.
	// Empty means the guardrail defaults (DefaultProtectedBranches) apply.
	ProtectedBranches []string `json:"protectedBranches,omitempty"`
}

// EffectiveProtectedBranches returns the configured protected branches, or the
// defaults when none are set.
func (c Config) EffectiveProtectedBranches() []string {
	if len(c.ProtectedBranches) > 0 {
		out := make([]string, len(c.ProtectedBranches))
		copy(out, c.ProtectedBranches)
		return out
	}
	out := make([]string, len(DefaultProtectedBranches))
	copy(out, DefaultProtectedBranches)
	return out
}

// IsBranchProtected reports whether branch is guarded by this config. It
// returns false when protection is disabled.
func (c Config) IsBranchProtected(branch string) bool {
	if !c.Protected || branch == "" {
		return false
	}
	for _, b := range c.EffectiveProtectedBranches() {
		if b == branch {
			return true
		}
	}
	return false
}

// ConfigPath returns the absolute path to the protection config for a repo.
func ConfigPath(repoRoot string) string {
	return filepath.Join(repoRoot, ConfigFileName)
}

// Load reads <repoRoot>/config.swarm. The bool result reports whether the file
// was found and parsed; a missing file is (zero, false, nil) so callers fall
// back to their own defaults. A malformed file returns an error.
func Load(repoRoot string) (Config, bool, error) {
	if repoRoot == "" {
		return Config{}, false, nil
	}
	data, err := os.ReadFile(ConfigPath(repoRoot))
	if err != nil {
		if os.IsNotExist(err) {
			return Config{}, false, nil
		}
		return Config{}, false, fmt.Errorf("read protection config: %w", err)
	}
	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return Config{}, false, fmt.Errorf("parse protection config: %w", err)
	}
	return cfg, true, nil
}

// Save writes the protection config to <repoRoot>/config.swarm.
func Save(repoRoot string, cfg Config) error {
	if repoRoot == "" {
		return fmt.Errorf("save protection config: empty repo root")
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal protection config: %w", err)
	}
	data = append(data, '\n')
	if err := os.WriteFile(ConfigPath(repoRoot), data, 0o644); err != nil {
		return fmt.Errorf("write protection config: %w", err)
	}
	return nil
}

// SetProtection enables or disables branch protection for a repo and persists
// the change to <repoRoot>/config.swarm, preserving existing fields. When
// branches is non-empty it replaces the protected-branch list. Returns the
// updated config.
func SetProtection(repoRoot string, enabled bool, branches []string) (Config, error) {
	cfg, _, err := Load(repoRoot)
	if err != nil {
		return Config{}, err
	}
	cfg.Protected = enabled
	if len(branches) > 0 {
		cfg.ProtectedBranches = branches
	}
	if err := Save(repoRoot, cfg); err != nil {
		return Config{}, err
	}
	return cfg, nil
}
