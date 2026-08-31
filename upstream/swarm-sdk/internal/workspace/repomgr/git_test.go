package repomgr

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func initLocalRepo(t *testing.T) (repoDir string, repoURL string, branch string) {
	t.Helper()

	repoDir = filepath.Join(t.TempDir(), "repo")
	branch = "main"

	must := func(cmd *exec.Cmd) {
		t.Helper()
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("%s failed: %v\n%s", cmd.String(), err, string(out))
		}
	}

	must(exec.Command("git", "init", "-b", branch, repoDir))
	must(exec.Command("git", "-C", repoDir, "config", "user.name", "Test User"))
	must(exec.Command("git", "-C", repoDir, "config", "user.email", "test@example.com"))

	readme := filepath.Join(repoDir, "README.md")
	if err := os.WriteFile(readme, []byte("hello\n"), 0644); err != nil {
		t.Fatalf("write README: %v", err)
	}
	must(exec.Command("git", "-C", repoDir, "add", "README.md"))
	must(exec.Command("git", "-C", repoDir, "commit", "-m", "initial"))

	// Use file:// so `--depth` works consistently (git ignores depth on local-path clones).
	repoURL = "file://" + repoDir
	return repoDir, repoURL, branch
}

func TestCloneRepo_Success(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	tmpDir := t.TempDir()
	targetDir := filepath.Join(tmpDir, "cloned-repo")

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	_, repoURL, branch := initLocalRepo(t)

	// Clone a small local repo (no external network required)
	err := CloneRepo(ctx, CloneParams{
		URL:       repoURL,
		Branch:    branch,
		TargetDir: targetDir,
	})
	if err != nil {
		t.Fatalf("Clone failed: %v", err)
	}

	// Verify .git exists
	gitDir := filepath.Join(targetDir, ".git")
	if _, err := os.Stat(gitDir); os.IsNotExist(err) {
		t.Error("Expected .git directory to exist")
	}
}

func TestCloneRepo_InvalidURL(t *testing.T) {
	tmpDir := t.TempDir()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	err := CloneRepo(ctx, CloneParams{
		URL:       "invalid-url",
		TargetDir: filepath.Join(tmpDir, "test"),
	})
	if err == nil {
		t.Error("Expected error for invalid repo")
	}
}

func TestCloneRepo_Timeout(t *testing.T) {
	tmpDir := t.TempDir()

	_, repoURL, branch := initLocalRepo(t)

	// Deterministic cancellation: ensure ctx is already done before running git.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := CloneRepo(ctx, CloneParams{
		URL:       repoURL,
		Branch:    branch,
		TargetDir: filepath.Join(tmpDir, "test"),
	})
	if err == nil {
		t.Error("Expected cancellation/timeout error")
	}
	if err != nil && ctx.Err() != nil && !strings.Contains(err.Error(), "clone cancelled") {
		t.Fatalf("Expected clone cancelled error, got: %v", err)
	}
}

func TestCloneRepo_SSHFormat(t *testing.T) {
	// Test URL parsing for SSH format
	if !isValidGitURL("git@github.com:user/repo.git") {
		t.Error("Should accept SSH URL format")
	}
}

func TestIsValidGitURL(t *testing.T) {
	tests := []struct {
		url   string
		valid bool
	}{
		{"https://github.com/user/repo.git", true},
		{"http://github.com/user/repo.git", true},
		{"file:///tmp/repo.git", true},
		{"/tmp/repo.git", true},
		{"./repo.git", true},
		{"../repo.git", true},
		{"git@github.com:user/repo.git", true},
		{"user@host:path/to/repo.git", true},
		{"invalid-url", false},
		{"ftp://example.com/repo", false},
		{"", false},
	}

	for _, tt := range tests {
		t.Run(tt.url, func(t *testing.T) {
			got := isValidGitURL(tt.url)
			if got != tt.valid {
				t.Errorf("isValidGitURL(%q) = %v, want %v", tt.url, got, tt.valid)
			}
		})
	}
}
