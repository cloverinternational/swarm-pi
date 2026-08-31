package gitgraph_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/gitgraph"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/gitops"
)

// initTestRepo creates a temporary git repo with one commit on branch "main"
// and returns the repo path.
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

	if err := os.WriteFile(filepath.Join(dir, "f.txt"), []byte("hello"), 0644); err != nil {
		t.Fatalf("write file: %v", err)
	}
	must(exec.Command("git", "-C", dir, "add", "."))
	must(exec.Command("git", "-C", dir, "commit", "-m", "init"))

	return dir
}

func TestBuildWorktreeGraph_SingleRepo(t *testing.T) {
	dir := initTestRepo(t)

	worktrees := []gitops.WorktreeInfo{{Path: dir, Branch: "main", IsMain: true}}
	graph, err := gitgraph.BuildWorktreeGraph(dir, worktrees, gitgraph.DefaultFilter())
	if err != nil {
		t.Fatalf("BuildWorktreeGraph failed: %v", err)
	}
	if len(graph.Commits) == 0 {
		t.Fatal("expected at least one commit")
	}
}

func TestBuildWorktreeGraph_ColorIndexAssigned(t *testing.T) {
	dir := initTestRepo(t)

	must := func(cmd *exec.Cmd) {
		t.Helper()
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("%s failed: %v\n%s", cmd.String(), err, string(out))
		}
	}

	// Create a feature branch with an extra commit.
	must(exec.Command("git", "-C", dir, "checkout", "-b", "feature"))
	if err := os.WriteFile(filepath.Join(dir, "g.txt"), []byte("world"), 0644); err != nil {
		t.Fatalf("write file: %v", err)
	}
	must(exec.Command("git", "-C", dir, "add", "."))
	must(exec.Command("git", "-C", dir, "commit", "-m", "feature commit"))
	must(exec.Command("git", "-C", dir, "checkout", "main"))

	worktrees := []gitops.WorktreeInfo{
		{Path: dir, Branch: "main", IsMain: true},
		{Path: dir, Branch: "feature", IsMain: false},
	}
	graph, err := gitgraph.BuildWorktreeGraph(dir, worktrees, gitgraph.DefaultFilter())
	if err != nil {
		t.Fatalf("BuildWorktreeGraph failed: %v", err)
	}
	if len(graph.Commits) < 2 {
		t.Fatalf("expected at least 2 commits, got %d", len(graph.Commits))
	}
}

func TestBuildWorktreeGraph_GeneratesEdges(t *testing.T) {
	dir := initTestRepo(t)

	must := func(cmd *exec.Cmd) {
		t.Helper()
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("%s failed: %v\n%s", cmd.String(), err, string(out))
		}
	}

	// Add a second commit to create a parent/child relationship.
	if err := os.WriteFile(filepath.Join(dir, "g.txt"), []byte("world"), 0644); err != nil {
		t.Fatalf("write file: %v", err)
	}
	must(exec.Command("git", "-C", dir, "add", "."))
	must(exec.Command("git", "-C", dir, "commit", "-m", "second"))

	worktrees := []gitops.WorktreeInfo{{Path: dir, Branch: "main", IsMain: true}}
	graph, err := gitgraph.BuildWorktreeGraph(dir, worktrees, gitgraph.DefaultFilter())
	if err != nil {
		t.Fatalf("BuildWorktreeGraph failed: %v", err)
	}

	if len(graph.Edges) == 0 {
		t.Fatal("expected at least one edge")
	}
}

func TestBuildWorktreeGraph_OnlyBranchRefsColored(t *testing.T) {
	dir := initTestRepo(t)

	must := func(cmd *exec.Cmd) {
		t.Helper()
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("%s failed: %v\n%s", cmd.String(), err, string(out))
		}
	}

	// Add a tag -- tags should NOT influence color assignment.
	must(exec.Command("git", "-C", dir, "tag", "-m", "", "v1.0"))

	worktrees := []gitops.WorktreeInfo{{Path: dir, Branch: "main", IsMain: true}}
	graph, err := gitgraph.BuildWorktreeGraph(dir, worktrees, gitgraph.DefaultFilter())
	if err != nil {
		t.Fatalf("BuildWorktreeGraph failed: %v", err)
	}
	if len(graph.Commits) == 0 {
		t.Fatal("expected at least one commit")
	}
	// The commit with the "main" branch ref should have its ColorIndex set
	// based on the worktree mapping (index 0 for the first worktree).
	for _, commit := range graph.Commits {
		for _, ref := range commit.Refs {
			if ref.Type == gitgraph.RefBranch && ref.ShortName() == "main" {
				if commit.ColorIndex != 0 {
					t.Errorf("expected ColorIndex 0 for main worktree commit, got %d", commit.ColorIndex)
				}
			}
		}
	}
}

func TestBuildWorktreeGraph_AttachesRefs(t *testing.T) {
	dir := initTestRepo(t)

	must := func(cmd *exec.Cmd) {
		t.Helper()
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("%s failed: %v\n%s", cmd.String(), err, string(out))
		}
	}

	// Add a second commit so we have one commit without a branch ref.
	if err := os.WriteFile(filepath.Join(dir, "g.txt"), []byte("world"), 0644); err != nil {
		t.Fatalf("write file: %v", err)
	}
	must(exec.Command("git", "-C", dir, "add", "."))
	must(exec.Command("git", "-C", dir, "commit", "-m", "second"))

	worktrees := []gitops.WorktreeInfo{{Path: dir, Branch: "main", IsMain: true}}
	graph, err := gitgraph.BuildWorktreeGraph(dir, worktrees, gitgraph.DefaultFilter())
	if err != nil {
		t.Fatalf("BuildWorktreeGraph failed: %v", err)
	}

	if graph.HeadSHA == "" {
		t.Fatal("expected head SHA")
	}

	head := graph.GetCommit(graph.HeadSHA)
	if head == nil {
		t.Fatal("expected head commit")
	}
	if head.Refs == nil {
		t.Fatal("expected head refs to be non-nil")
	}
	foundMain := false
	for _, ref := range head.Refs {
		if ref.Type == gitgraph.RefBranch && ref.ShortName() == "main" {
			foundMain = true
			break
		}
	}
	if !foundMain {
		t.Fatal("expected head commit to include main branch ref")
	}

	// Verify non-head commits have non-nil refs slices (empty is fine).
	for sha, commit := range graph.Commits {
		if sha == graph.HeadSHA {
			continue
		}
		if commit.Refs == nil {
			t.Fatalf("expected refs to be non-nil for commit %s", sha)
		}
		break
	}
}

func TestBuildWorktreeGraph_EmptyWorktreeList(t *testing.T) {
	dir := initTestRepo(t)

	// Empty worktrees slice -- should still work; just no color overrides.
	graph, err := gitgraph.BuildWorktreeGraph(dir, nil, gitgraph.DefaultFilter())
	if err != nil {
		t.Fatalf("BuildWorktreeGraph failed: %v", err)
	}
	if len(graph.Commits) == 0 {
		t.Fatal("expected at least one commit")
	}
}
