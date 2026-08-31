package gitops

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

const DefaultConfigFileName = ".swarm/worktree.json"

type WorktreeConfig struct {
	SymlinkDirectories []string `json:"symlinkDirectories"`
}

func DefaultWorktreeConfig() WorktreeConfig {
	return WorktreeConfig{
		SymlinkDirectories: []string{"node_modules", ".cache"},
	}
}

func LoadWorktreeConfig(repoRoot string) (WorktreeConfig, error) {
	p := filepath.Join(repoRoot, DefaultConfigFileName)
	data, err := os.ReadFile(p)
	if err != nil {
		if os.IsNotExist(err) {
			return WorktreeConfig{}, nil
		}
		return WorktreeConfig{}, fmt.Errorf("read worktree config: %w", err)
	}
	var cfg WorktreeConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return WorktreeConfig{}, fmt.Errorf("parse worktree config: %w", err)
	}
	return cfg, nil
}

func SaveWorktreeConfig(repoRoot string, cfg WorktreeConfig) error {
	dir := filepath.Join(repoRoot, ".swarm")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create config dir: %w", err)
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal worktree config: %w", err)
	}
	data = append(data, '\n')
	p := filepath.Join(repoRoot, DefaultConfigFileName)
	if err := os.WriteFile(p, data, 0o644); err != nil {
		return fmt.Errorf("write worktree config: %w", err)
	}
	return nil
}
