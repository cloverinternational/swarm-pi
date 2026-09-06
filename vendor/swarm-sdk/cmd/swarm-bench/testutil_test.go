package main

import (
	"bytes"
	"os"
	"os/exec"
	"strings"
	"testing"
)

// captureStdout redirects os.Stdout for the duration of fn and returns
// everything written to it. Used to assert on runLS/runSurvive's human and
// -json output without spawning a subprocess for every test.
func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	os.Stdout = w
	done := make(chan struct{})
	var buf bytes.Buffer
	go func() {
		_, _ = buf.ReadFrom(r)
		close(done)
	}()
	fn()
	_ = w.Close()
	os.Stdout = old
	<-done
	return buf.String()
}

// mustRunGit runs a real git command in dir and fails the test on error —
// this is the fixture builder for survive's git-truth tests: the repo
// really exists on disk, git really committed to it, nothing is mocked.
func mustRunGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	if err := cmd.Run(); err != nil {
		t.Fatalf("git %v (dir=%s): %v\n%s", args, dir, err, out.String())
	}
	return strings.TrimSpace(out.String())
}

// newGitFixture creates a real throwaway git repository with exactly one
// commit containing committed.txt, and returns the repo dir plus the git
// blob hash of that file's content (computed independently via `git
// hash-object`, not via this tool's own blobHash — so the fixture does not
// silently validate this tool against itself).
func newGitFixture(t *testing.T) (repoDir, committedBlob, headSHA string) {
	t.Helper()
	dir := t.TempDir()
	mustRunGit(t, dir, "init", "-q")
	mustRunGit(t, dir, "config", "user.email", "swarm-bench-test@example.com")
	mustRunGit(t, dir, "config", "user.name", "swarm-bench test")
	mustRunGit(t, dir, "config", "commit.gpgsign", "false")

	if err := os.WriteFile(dir+"/committed.txt", []byte("this content really got committed\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	mustRunGit(t, dir, "add", "committed.txt")
	mustRunGit(t, dir, "commit", "-q", "-m", "add committed.txt")

	committedBlob = mustRunGit(t, dir, "hash-object", "committed.txt")
	headSHA = mustRunGit(t, dir, "rev-parse", "HEAD")
	return dir, committedBlob, headSHA
}

// gitHashObject computes the real git blob hash of content without ever
// writing it into repoDir's working tree or index — the "this content was
// produced somewhere but never committed" fixture case.
func gitHashObject(t *testing.T, repoDir string, content []byte) string {
	t.Helper()
	cmd := exec.Command("git", "hash-object", "--stdin")
	cmd.Dir = repoDir
	cmd.Stdin = bytes.NewReader(content)
	var out bytes.Buffer
	cmd.Stdout = &out
	if err := cmd.Run(); err != nil {
		t.Fatalf("git hash-object --stdin: %v", err)
	}
	return strings.TrimSpace(out.String())
}
