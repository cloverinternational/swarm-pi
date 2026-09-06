package gitprotect

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSetProtectionAndLoad(t *testing.T) {
	dir := t.TempDir()

	cfg, err := SetProtection(dir, true, []string{"main", "release"})
	if err != nil {
		t.Fatalf("SetProtection enable: %v", err)
	}
	if !cfg.Protected {
		t.Fatal("expected Protected=true")
	}

	// File must be at repo root, named config.swarm (not under .swarm/).
	if _, err := os.Stat(filepath.Join(dir, "config.swarm")); err != nil {
		t.Fatalf("config.swarm not written at repo root: %v", err)
	}

	loaded, found, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !found {
		t.Fatal("expected config to be found")
	}
	if !loaded.Protected {
		t.Fatal("persisted config should be Protected=true")
	}
	got := loaded.EffectiveProtectedBranches()
	if len(got) != 2 || got[0] != "main" || got[1] != "release" {
		t.Fatalf("unexpected branches: %v", got)
	}
}

func TestLoadMissingIsNotFound(t *testing.T) {
	dir := t.TempDir()
	cfg, found, err := Load(dir)
	if err != nil {
		t.Fatalf("Load missing: %v", err)
	}
	if found {
		t.Fatal("expected found=false for missing file")
	}
	if cfg.Protected {
		t.Fatal("missing config should be zero value")
	}
}

func TestEffectiveProtectedBranchesDefault(t *testing.T) {
	cfg := Config{Protected: true}
	got := cfg.EffectiveProtectedBranches()
	if len(got) != len(DefaultProtectedBranches) {
		t.Fatalf("expected defaults, got %v", got)
	}
}

func TestIsBranchProtected(t *testing.T) {
	cfg := Config{Protected: true, ProtectedBranches: []string{"main"}}
	if !cfg.IsBranchProtected("main") {
		t.Fatal("main should be protected")
	}
	if cfg.IsBranchProtected("feature") {
		t.Fatal("feature should not be protected")
	}
	off := Config{Protected: false, ProtectedBranches: []string{"main"}}
	if off.IsBranchProtected("main") {
		t.Fatal("disabled config should protect nothing")
	}
}

func TestSetProtectionDisableRetainsBranches(t *testing.T) {
	dir := t.TempDir()
	if _, err := SetProtection(dir, true, []string{"main"}); err != nil {
		t.Fatalf("enable: %v", err)
	}
	if _, err := SetProtection(dir, false, nil); err != nil {
		t.Fatalf("disable: %v", err)
	}
	loaded, _, err := Load(dir)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if loaded.Protected {
		t.Fatal("expected Protected=false after disable")
	}
	if len(loaded.ProtectedBranches) != 1 || loaded.ProtectedBranches[0] != "main" {
		t.Fatalf("branch list not retained: %v", loaded.ProtectedBranches)
	}
}
