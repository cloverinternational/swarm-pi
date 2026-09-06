package gitops_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/gitops"
)

// initTestRepo creates a temporary git repo with one commit and returns the path.
func initTestRepo(t *testing.T) string {
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

func TestGetWorktrees_ReturnsMainWorktree(t *testing.T) {
	dir := initTestRepo(t)

	worktrees, err := gitops.GetWorktrees(dir)
	if err != nil {
		t.Fatalf("GetWorktrees failed: %v", err)
	}
	if len(worktrees) < 1 {
		t.Fatal("expected at least one worktree (main)")
	}
	if !worktrees[0].IsMain {
		t.Error("first worktree should be main")
	}
	if worktrees[0].Path == "" {
		t.Error("worktree path should not be empty")
	}
	if worktrees[0].Branch != "main" {
		t.Errorf("expected branch 'main', got %q", worktrees[0].Branch)
	}
	if worktrees[0].HeadSHA == "" {
		t.Error("worktree HeadSHA should not be empty")
	}
}

func TestGetWorktrees_MultipleWorktrees(t *testing.T) {
	dir := initTestRepo(t)

	// Create a second worktree
	wtPath := filepath.Join(t.TempDir(), "feature-wt")
	must := func(cmd *exec.Cmd) {
		t.Helper()
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("%s failed: %v\n%s", cmd.String(), err, string(out))
		}
	}
	must(exec.Command("git", "-C", dir, "worktree", "add", wtPath, "-b", "feature-branch"))

	worktrees, err := gitops.GetWorktrees(dir)
	if err != nil {
		t.Fatalf("GetWorktrees failed: %v", err)
	}
	if len(worktrees) != 2 {
		t.Fatalf("expected 2 worktrees, got %d", len(worktrees))
	}

	// First should be main
	if !worktrees[0].IsMain {
		t.Error("first worktree should be main")
	}
	if worktrees[0].Branch != "main" {
		t.Errorf("expected main branch, got %q", worktrees[0].Branch)
	}

	// Second should be the feature worktree
	if worktrees[1].IsMain {
		t.Error("second worktree should not be main")
	}
	if worktrees[1].Branch != "feature-branch" {
		t.Errorf("expected branch 'feature-branch', got %q", worktrees[1].Branch)
	}
	// Resolve symlinks for comparison (macOS /var -> /private/var).
	resolvedWtPath, err := filepath.EvalSymlinks(wtPath)
	if err != nil {
		t.Fatalf("EvalSymlinks: %v", err)
	}
	if worktrees[1].Path != resolvedWtPath {
		t.Errorf("expected path %q, got %q", resolvedWtPath, worktrees[1].Path)
	}
}

func TestGetWorktrees_DirtyStatus(t *testing.T) {
	dir := initTestRepo(t)

	// Initially clean
	worktrees, err := gitops.GetWorktrees(dir)
	if err != nil {
		t.Fatalf("GetWorktrees failed: %v", err)
	}
	if worktrees[0].IsDirty {
		t.Error("expected clean worktree initially")
	}

	// Make it dirty
	if err := os.WriteFile(filepath.Join(dir, "dirty.txt"), []byte("dirty"), 0644); err != nil {
		t.Fatalf("write file: %v", err)
	}
	out, err := exec.Command("git", "-C", dir, "add", "dirty.txt").CombinedOutput()
	if err != nil {
		t.Fatalf("git add failed: %v\n%s", err, string(out))
	}

	worktrees, err = gitops.GetWorktrees(dir)
	if err != nil {
		t.Fatalf("GetWorktrees failed: %v", err)
	}
	if !worktrees[0].IsDirty {
		t.Error("expected dirty worktree after staging a file")
	}
}

func TestGetWorktrees_BranchStripsRefsHeads(t *testing.T) {
	dir := initTestRepo(t)

	worktrees, err := gitops.GetWorktrees(dir)
	if err != nil {
		t.Fatalf("GetWorktrees failed: %v", err)
	}
	// Branch should be "main", not "refs/heads/main"
	if worktrees[0].Branch == "refs/heads/main" {
		t.Error("branch should have refs/heads/ prefix stripped")
	}
	if worktrees[0].Branch != "main" {
		t.Errorf("expected 'main', got %q", worktrees[0].Branch)
	}
}

func TestGetWorktrees_EmptyOutput(t *testing.T) {
	// Test with a non-existent directory -- should return error, not panic
	_, err := gitops.GetWorktrees("/nonexistent/path/to/repo")
	if err == nil {
		t.Error("expected error for non-existent path")
	}
}

// TestListWorktrees_NoDiskUsageWalk locks in the startup-performance fix: the
// lightweight ListWorktrees must return worktree identity (path/branch/IsMain)
// WITHOUT performing the recursive disk-usage walk that GetWorktrees does.
// Walking the whole tree on the startup-critical path is what made launching
// the TUI inside a large git top-level (e.g. a stray `git init` in $HOME) take
// over a minute. DiskUsageBytes must therefore stay zero for the light path.
func TestListWorktrees_NoDiskUsageWalk(t *testing.T) {
	dir := initTestRepo(t)

	worktrees, err := gitops.ListWorktrees(dir)
	if err != nil {
		t.Fatalf("ListWorktrees failed: %v", err)
	}
	if len(worktrees) < 1 {
		t.Fatal("expected at least one worktree (main)")
	}
	wt := worktrees[0]
	if !wt.IsMain {
		t.Error("first worktree should be main")
	}
	if wt.Path == "" {
		t.Error("worktree path should not be empty")
	}
	if wt.Branch != "main" {
		t.Errorf("expected branch 'main', got %q", wt.Branch)
	}
	if wt.HeadSHA == "" {
		t.Error("worktree HeadSHA should not be empty")
	}
	// The whole point of the light path: no disk-usage walk was performed.
	if wt.DiskUsageBytes != 0 {
		t.Errorf("ListWorktrees must not compute disk usage; got %d bytes", wt.DiskUsageBytes)
	}
	if len(wt.SymlinkedDirs) != 0 {
		t.Errorf("ListWorktrees must not detect symlinked dirs; got %v", wt.SymlinkedDirs)
	}
}

// TestGetWorktrees_ComputesDiskUsage confirms the enriched path still populates
// disk usage for the UI that displays it.
func TestGetWorktrees_ComputesDiskUsage(t *testing.T) {
	dir := initTestRepo(t)

	worktrees, err := gitops.GetWorktrees(dir)
	if err != nil {
		t.Fatalf("GetWorktrees failed: %v", err)
	}
	if len(worktrees) < 1 {
		t.Fatal("expected at least one worktree (main)")
	}
	// The repo contains a committed readme.txt, so disk usage must be > 0.
	if worktrees[0].DiskUsageBytes <= 0 {
		t.Errorf("GetWorktrees should compute positive disk usage; got %d", worktrees[0].DiskUsageBytes)
	}
}
