package gitops_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/gitops"
)

func initManageTestRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	must := func(cmd *exec.Cmd) {
		t.Helper()
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("%s failed: %v\n%s", cmd.String(), err, string(out))
		}
	}
	must(exec.Command("git", "init", "-b", "main", dir))
	must(exec.Command("git", "-C", dir, "config", "user.email", "test@test.com"))
	must(exec.Command("git", "-C", dir, "config", "user.name", "Test"))
	if err := os.WriteFile(filepath.Join(dir, "readme.txt"), []byte("hi"), 0644); err != nil {
		t.Fatalf("write file: %v", err)
	}
	must(exec.Command("git", "-C", dir, "add", "."))
	must(exec.Command("git", "-C", dir, "commit", "-m", "init"))
	return dir
}

func TestAddWorktree_Basic(t *testing.T) {
	dir := initManageTestRepo(t)
	wtPath := filepath.Join(t.TempDir(), "feature-wt")

	err := gitops.AddWorktree(gitops.AddWorktreeOpts{
		RepoPath: dir,
		Path:     wtPath,
		Branch:   "feature-test",
	}, gitops.WorktreeConfig{})
	if err != nil {
		t.Fatalf("AddWorktree failed: %v", err)
	}

	if _, err := os.Stat(wtPath); os.IsNotExist(err) {
		t.Error("worktree directory was not created")
	}

	worktrees, err := gitops.GetWorktrees(dir)
	if err != nil {
		t.Fatalf("GetWorktrees: %v", err)
	}
	if len(worktrees) != 2 {
		t.Fatalf("expected 2 worktrees, got %d", len(worktrees))
	}
	if worktrees[1].Branch != "feature-test" {
		t.Errorf("expected branch feature-test, got %q", worktrees[1].Branch)
	}
}

func TestAddWorktree_WithSymlinks(t *testing.T) {
	dir := initManageTestRepo(t)

	nmDir := filepath.Join(dir, "node_modules")
	if err := os.MkdirAll(filepath.Join(nmDir, "some-pkg"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(nmDir, "some-pkg", "index.js"), []byte("module.exports=1"), 0644); err != nil {
		t.Fatal(err)
	}

	wtPath := filepath.Join(t.TempDir(), "symlink-wt")
	cfg := gitops.WorktreeConfig{SymlinkDirectories: []string{"node_modules"}}

	err := gitops.AddWorktree(gitops.AddWorktreeOpts{
		RepoPath: dir,
		Path:     wtPath,
		Branch:   "feat-symlink",
	}, cfg)
	if err != nil {
		t.Fatalf("AddWorktree with symlinks failed: %v", err)
	}

	linkPath := filepath.Join(wtPath, "node_modules")
	fi, err := os.Lstat(linkPath)
	if err != nil {
		t.Fatalf("Lstat symlink: %v", err)
	}
	if fi.Mode()&os.ModeSymlink == 0 {
		t.Error("expected node_modules to be a symlink")
	}

	target, err := os.Readlink(linkPath)
	if err != nil {
		t.Fatalf("Readlink: %v", err)
	}
	resolved := filepath.Join(wtPath, target)
	abs1, _ := filepath.Abs(resolved)
	abs2, _ := filepath.Abs(nmDir)
	if abs1 != abs2 {
		t.Errorf("symlink target mismatch: resolved to %q, expected %q", abs1, abs2)
	}
}

func TestRemoveWorktree(t *testing.T) {
	dir := initManageTestRepo(t)
	wtPath := filepath.Join(t.TempDir(), "remove-wt")

	err := gitops.AddWorktree(gitops.AddWorktreeOpts{
		RepoPath: dir,
		Path:     wtPath,
		Branch:   "to-remove",
	}, gitops.WorktreeConfig{})
	if err != nil {
		t.Fatalf("AddWorktree: %v", err)
	}

	if err := gitops.RemoveWorktree(dir, wtPath); err != nil {
		t.Fatalf("RemoveWorktree failed: %v", err)
	}

	worktrees, err := gitops.GetWorktrees(dir)
	if err != nil {
		t.Fatalf("GetWorktrees: %v", err)
	}
	if len(worktrees) != 1 {
		t.Errorf("expected 1 worktree after removal, got %d", len(worktrees))
	}
}

func TestRemoveWorktree_RefusesMain(t *testing.T) {
	dir := initManageTestRepo(t)
	err := gitops.RemoveWorktree(dir, dir)
	if err == nil {
		t.Error("expected error when removing main worktree")
	}
}

func TestCreateSymlinks(t *testing.T) {
	mainDir := t.TempDir()
	wtDir := t.TempDir()

	cacheDir := filepath.Join(mainDir, ".cache")
	if err := os.MkdirAll(cacheDir, 0755); err != nil {
		t.Fatal(err)
	}

	err := gitops.CreateSymlinks(mainDir, wtDir, []string{".cache", "nonexistent"})
	if err != nil {
		t.Fatalf("CreateSymlinks failed: %v", err)
	}

	linkPath := filepath.Join(wtDir, ".cache")
	fi, err := os.Lstat(linkPath)
	if err != nil {
		t.Fatalf("Lstat: %v", err)
	}
	if fi.Mode()&os.ModeSymlink == 0 {
		t.Error("expected .cache to be a symlink")
	}

	nonexistent := filepath.Join(wtDir, "nonexistent")
	if _, err := os.Lstat(nonexistent); !os.IsNotExist(err) {
		t.Error("nonexistent dir should not be symlinked")
	}
}

func TestDirDiskUsage(t *testing.T) {
	dir := t.TempDir()
	data := make([]byte, 1024)
	for i := range data {
		data[i] = 'x'
	}
	if err := os.WriteFile(filepath.Join(dir, "file.bin"), data, 0644); err != nil {
		t.Fatal(err)
	}

	usage := gitops.DirDiskUsage(dir)
	if usage < 1024 {
		t.Errorf("expected at least 1024 bytes, got %d", usage)
	}
}
