package gitops_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/gitprotect"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/gitops"
)

func TestResolveWorkspaceBindingNonGit(t *testing.T) {
	dir := t.TempDir()
	got, err := gitops.ResolveWorkspaceBinding(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got.IsGit || got.ProjectRoot != dir || got.ExecutionRoot != dir {
		t.Fatalf("unexpected binding: %+v", got)
	}
}

func TestResolveWorkspaceBindingCanonicalProjectRoot(t *testing.T) {
	dir := initManageTestRepo(t)
	if _, err := gitprotect.SetProtection(dir, true, []string{"main"}); err != nil {
		t.Fatal(err)
	}
	wtPath := filepath.Join(t.TempDir(), "feature-wt")
	if err := gitops.AddWorktree(gitops.AddWorktreeOpts{
		RepoPath: dir,
		Path:     wtPath,
		Branch:   "feature-test",
	}, gitops.WorktreeConfig{}); err != nil {
		t.Fatal(err)
	}

	mainBinding, err := gitops.ResolveWorkspaceBinding(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !mainBinding.IsProtected || mainBinding.ProjectRoot != dir || mainBinding.ExecutionRoot != dir {
		t.Fatalf("unexpected main binding: %+v", mainBinding)
	}

	wtBinding, err := gitops.ResolveWorkspaceBinding(wtPath)
	if err != nil {
		t.Fatal(err)
	}
	if wtBinding.ProjectRoot != dir || wtBinding.ExecutionRoot != wtPath || wtBinding.Branch != "feature-test" || wtBinding.IsProtected {
		t.Fatalf("unexpected worktree binding: %+v", wtBinding)
	}
}

func TestFindWorktreeByBranchPathAndBasename(t *testing.T) {
	dir := initManageTestRepo(t)
	wtPath := filepath.Join(t.TempDir(), "feature-wt")
	if err := gitops.AddWorktree(gitops.AddWorktreeOpts{RepoPath: dir, Path: wtPath, Branch: "feature-test"}, gitops.WorktreeConfig{}); err != nil {
		t.Fatal(err)
	}
	for _, selector := range []string{"feature-test", wtPath, filepath.Base(wtPath)} {
		wt, err := gitops.FindWorktree(dir, selector)
		if err != nil {
			t.Fatalf("selector %q: %v", selector, err)
		}
		if wt.Branch != "feature-test" {
			t.Fatalf("selector %q resolved %+v", selector, wt)
		}
	}
}

func TestCreateWorkspaceWorktreeAndCollisions(t *testing.T) {
	dir := initManageTestRepo(t)
	wt, err := gitops.CreateWorkspaceWorktree(dir, "swarm/test-change")
	if err != nil {
		t.Fatal(err)
	}
	wantPath := filepath.Join(dir, ".worktrees", "swarm-test-change")
	if wt.Path != wantPath || wt.Branch != "swarm/test-change" || wt.IsMain {
		t.Fatalf("unexpected worktree: %+v", wt)
	}
	if _, err := gitops.CreateWorkspaceWorktree(dir, "swarm/test-change"); err == nil {
		t.Fatal("expected duplicate branch/path error")
	}
}

func TestRemoveWorkspaceWorktreeRefusesDirty(t *testing.T) {
	dir := initManageTestRepo(t)
	wt, err := gitops.CreateWorkspaceWorktree(dir, "swarm/dirty")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(wt.Path, "dirty.txt"), []byte("dirty"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := gitops.RemoveWorkspaceWorktree(dir, wt.Branch); err == nil {
		t.Fatal("expected dirty worktree removal to be refused")
	}
}

// TestResolveWorkspaceBinding_IgnoresHomeGit verifies the guard against an
// accidental `git init` in $HOME: launching from a subdirectory of a home repo
// must NOT bind to that repo (which would make the whole home dir the
// workspace and scan the entire tree). Launching from $HOME itself, or with
// SWARM_ALLOW_HOME_GIT=1, still binds.
func TestResolveWorkspaceBinding_IgnoresHomeGit(t *testing.T) {
	// Make a temp directory the "home" and turn it into a git repo.
	home := initManageTestRepo(t)
	t.Setenv("HOME", home)
	t.Setenv("SWARM_ALLOW_HOME_GIT", "")

	// A subdirectory inside the home repo.
	sub := filepath.Join(home, "projects", "app")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}

	// Launched from a subdir of the home repo => ignore the home git binding.
	got, err := gitops.ResolveWorkspaceBinding(sub)
	if err != nil {
		t.Fatal(err)
	}
	if got.IsGit {
		t.Errorf("expected non-git binding for subdir of $HOME repo, got IsGit=true: %+v", got)
	}
	if !got.IgnoredHomeGit {
		t.Errorf("expected IgnoredHomeGit=true, got %+v", got)
	}
	if got.ExecutionRoot != sub || got.ProjectRoot != sub {
		t.Errorf("expected roots to be the launch dir %q, got %+v", sub, got)
	}

	// Launched from $HOME itself => bind normally (user is explicitly there).
	atHome, err := gitops.ResolveWorkspaceBinding(home)
	if err != nil {
		t.Fatal(err)
	}
	if !atHome.IsGit || atHome.IgnoredHomeGit {
		t.Errorf("expected normal git binding when launched from $HOME, got %+v", atHome)
	}

	// Opt-in override restores binding even from a subdir.
	t.Setenv("SWARM_ALLOW_HOME_GIT", "1")
	override, err := gitops.ResolveWorkspaceBinding(sub)
	if err != nil {
		t.Fatal(err)
	}
	if !override.IsGit || override.IgnoredHomeGit {
		t.Errorf("expected SWARM_ALLOW_HOME_GIT=1 to bind, got %+v", override)
	}
}
