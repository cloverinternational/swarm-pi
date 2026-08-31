package forge

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// newRepoWithWorktree builds a real git repository plus a real linked
// worktree, mirroring the exact layout the issues describe: a conversation
// pinned to one checkout while the assigned work lives in a sibling worktree.
// It returns (mainCheckout, siblingWorktree).
func newRepoWithWorktree(t *testing.T) (string, string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	base := t.TempDir()
	main := filepath.Join(base, "main")
	if err := os.MkdirAll(main, 0o755); err != nil {
		t.Fatal(err)
	}
	run := func(dir string, args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@e",
			"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@e",
		)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
		}
	}
	run(main, "init", "-q", "-b", "main")
	if err := os.WriteFile(filepath.Join(main, "seed.txt"), []byte("seed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run(main, "add", ".")
	run(main, "commit", "-qm", "seed")

	sibling := filepath.Join(base, "sibling")
	run(main, "worktree", "add", "-q", "-b", "feature", sibling)
	return main, sibling
}

// TestApplyPatchEditsSiblingWorktreeViaCwd is the regression for #289, #290,
// #292 and #295: work explicitly assigned to a linked worktree could be read
// and tested there but never written, because apply_patch had no cwd and
// refused the path as outside the workspace.
func TestApplyPatchEditsSiblingWorktreeViaCwd(t *testing.T) {
	main, sibling := newRepoWithWorktree(t)

	target := filepath.Join(sibling, "app.go")
	if err := os.WriteFile(target, []byte("package main\n\nfunc main() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	tool := NewApplyPatchTool(main) // conversation pinned to the OTHER checkout
	patch := "*** Begin Patch\n*** Update File: app.go\n@@\n-func main() {}\n+func main() { println(\"ok\") }\n*** End Patch"

	if _, err := tool.Execute(context.Background(), map[string]any{
		"input": patch,
		"cwd":   sibling,
	}); err != nil {
		t.Fatalf("patch against an authorized sibling worktree must apply: %v", err)
	}

	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(got), `println("ok")`) {
		t.Fatalf("file not modified: %q", got)
	}
}

// TestApplyPatchAcceptsAbsoluteWorktreePath covers the other reported shape:
// an absolute path inside the assigned worktree.
func TestApplyPatchAcceptsAbsoluteWorktreePath(t *testing.T) {
	main, sibling := newRepoWithWorktree(t)
	target := filepath.Join(sibling, "abs.txt")
	if err := os.WriteFile(target, []byte("before\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	tool := NewApplyPatchTool(main)
	patch := "*** Begin Patch\n*** Update File: " + target + "\n@@\n-before\n+after\n*** End Patch"

	if _, err := tool.Execute(context.Background(), map[string]any{
		"input": patch,
		"cwd":   sibling,
	}); err != nil {
		t.Fatalf("absolute path inside the authorized worktree must apply: %v", err)
	}
	got, _ := os.ReadFile(target)
	if strings.TrimSpace(string(got)) != "after" {
		t.Fatalf("got %q", got)
	}
}

// TestApplyPatchRefusesUnrelatedDirectoryViaCwd is the near-miss that keeps
// cwd from becoming a way to write anywhere on the filesystem. A directory
// that is not part of the same repository must still be refused.
func TestApplyPatchRefusesUnrelatedDirectoryViaCwd(t *testing.T) {
	main, _ := newRepoWithWorktree(t)

	unrelated := t.TempDir() // not a git repo, not related to main
	target := filepath.Join(unrelated, "secret.txt")
	if err := os.WriteFile(target, []byte("original\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	tool := NewApplyPatchTool(main)
	patch := "*** Begin Patch\n*** Update File: secret.txt\n@@\n-original\n+tampered\n*** End Patch"

	_, err := tool.Execute(context.Background(), map[string]any{
		"input": patch,
		"cwd":   unrelated,
	})
	if err == nil {
		t.Fatal("cwd must not authorize writes to an unrelated directory")
	}

	got, _ := os.ReadFile(target)
	if strings.TrimSpace(string(got)) != "original" {
		t.Fatalf("file was modified despite the refusal: %q", got)
	}
}

// TestApplyPatchWithoutCwdStillPinnedToWorkspace proves the default is
// unchanged: with no cwd, a sibling worktree remains off limits.
func TestApplyPatchWithoutCwdStillPinnedToWorkspace(t *testing.T) {
	main, sibling := newRepoWithWorktree(t)
	target := filepath.Join(sibling, "app.go")
	if err := os.WriteFile(target, []byte("before\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	tool := NewApplyPatchTool(main)
	patch := "*** Begin Patch\n*** Update File: " + target + "\n@@\n-before\n+after\n*** End Patch"

	if _, err := tool.Execute(context.Background(), map[string]any{"input": patch}); err == nil {
		t.Fatal("without cwd, a path outside the workspace must still be refused")
	}
	got, _ := os.ReadFile(target)
	if strings.TrimSpace(string(got)) != "before" {
		t.Fatalf("file was modified: %q", got)
	}
}

// TestApplyPatchRejectsUnusableCwd checks the parameter is validated rather
// than silently ignored.
func TestApplyPatchRejectsUnusableCwd(t *testing.T) {
	main, _ := newRepoWithWorktree(t)
	tool := NewApplyPatchTool(main)
	patch := "*** Begin Patch\n*** Update File: x.txt\n@@\n-a\n+b\n*** End Patch"

	if err := tool.Validate(map[string]any{"input": patch, "cwd": filepath.Join(main, "does-not-exist")}); err == nil {
		t.Error("a nonexistent cwd must be rejected")
	}
	if err := tool.Validate(map[string]any{"input": patch, "cwd": filepath.Join(main, "seed.txt")}); err == nil {
		t.Error("a cwd that is a file must be rejected")
	}
}

// TestSameGitRepository covers the authorization predicate directly.
func TestSameGitRepository(t *testing.T) {
	main, sibling := newRepoWithWorktree(t)
	unrelated := t.TempDir()

	if !sameGitRepository(main, sibling) {
		t.Error("a linked worktree must be recognized as the same repository")
	}
	if !sameGitRepository(main, main) {
		t.Error("a checkout must be recognized as itself")
	}
	if sameGitRepository(main, unrelated) {
		t.Error("an unrelated directory must not be recognized as the same repository")
	}
	if sameGitRepository(unrelated, unrelated) {
		t.Error("a non-repository must never authorize itself")
	}
}
