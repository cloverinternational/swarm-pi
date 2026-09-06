package gitops_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/gitops"
)

func TestLoadWorktreeConfig_MissingFile(t *testing.T) {
	dir := t.TempDir()
	cfg, err := gitops.LoadWorktreeConfig(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(cfg.SymlinkDirectories) != 0 {
		t.Errorf("expected empty SymlinkDirectories, got %v", cfg.SymlinkDirectories)
	}
}

func TestSaveAndLoadWorktreeConfig_Roundtrip(t *testing.T) {
	dir := t.TempDir()
	original := gitops.WorktreeConfig{
		SymlinkDirectories: []string{"node_modules", ".cache", "vendor"},
	}
	if err := gitops.SaveWorktreeConfig(dir, original); err != nil {
		t.Fatalf("save failed: %v", err)
	}
	loaded, err := gitops.LoadWorktreeConfig(dir)
	if err != nil {
		t.Fatalf("load failed: %v", err)
	}
	if len(loaded.SymlinkDirectories) != len(original.SymlinkDirectories) {
		t.Fatalf("expected %d dirs, got %d", len(original.SymlinkDirectories), len(loaded.SymlinkDirectories))
	}
	for i, d := range loaded.SymlinkDirectories {
		if d != original.SymlinkDirectories[i] {
			t.Errorf("dir[%d]: expected %q, got %q", i, original.SymlinkDirectories[i], d)
		}
	}
}

func TestDefaultWorktreeConfig(t *testing.T) {
	cfg := gitops.DefaultWorktreeConfig()
	if len(cfg.SymlinkDirectories) < 2 {
		t.Fatal("expected at least 2 default symlink directories")
	}
	found := false
	for _, d := range cfg.SymlinkDirectories {
		if d == "node_modules" {
			found = true
		}
	}
	if !found {
		t.Error("expected node_modules in default config")
	}
}

func TestSaveWorktreeConfig_CreatesDir(t *testing.T) {
	dir := t.TempDir()
	cfg := gitops.WorktreeConfig{SymlinkDirectories: []string{"vendor"}}
	if err := gitops.SaveWorktreeConfig(dir, cfg); err != nil {
		t.Fatalf("save failed: %v", err)
	}
	swarmDir := filepath.Join(dir, ".swarm")
	if _, err := os.Stat(swarmDir); os.IsNotExist(err) {
		t.Error(".swarm directory was not created")
	}
	configFile := filepath.Join(dir, ".swarm", "worktree.json")
	if _, err := os.Stat(configFile); os.IsNotExist(err) {
		t.Error("worktree.json was not created")
	}
}
