package gitops_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/gitops"
)

// makeRepo creates a temp git repo with one commit and returns its path and repo.
func makeRepo(t *testing.T) (string, *gitops.GitRepo) {
	t.Helper()
	dir := t.TempDir()
	exec.Command("git", "init", dir).Run()
	exec.Command("git", "-C", dir, "config", "user.email", "t@t.com").Run()
	exec.Command("git", "-C", dir, "config", "user.name", "T").Run()
	os.WriteFile(filepath.Join(dir, "f.txt"), []byte("hello"), 0644)
	exec.Command("git", "-C", dir, "add", ".").Run()
	exec.Command("git", "-C", dir, "commit", "-m", "init").Run()
	repo, err := gitops.NewGitRepo(dir)
	if err != nil {
		t.Fatalf("NewGitRepo: %v", err)
	}
	return dir, repo
}

func TestGetStashList_EmptyRepo(t *testing.T) {
	_, repo := makeRepo(t)
	entries, err := repo.GetStashList()
	if err != nil {
		t.Fatalf("GetStashList: %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("expected 0 stash entries, got %d", len(entries))
	}
}

func TestGetStashList_AfterStash(t *testing.T) {
	dir, repo := makeRepo(t)
	os.WriteFile(filepath.Join(dir, "f.txt"), []byte("modified"), 0644)
	if err := repo.Stash(); err != nil {
		t.Fatalf("Stash: %v", err)
	}
	entries, err := repo.GetStashList()
	if err != nil {
		t.Fatalf("GetStashList: %v", err)
	}
	if len(entries) != 1 {
		t.Errorf("expected 1 stash entry, got %d", len(entries))
	}
	if entries[0].Index != 0 {
		t.Errorf("expected index 0, got %d", entries[0].Index)
	}
}

func TestGetReflog_HasEntries(t *testing.T) {
	_, repo := makeRepo(t)
	entries, err := repo.GetReflog(10)
	if err != nil {
		t.Fatalf("GetReflog: %v", err)
	}
	if len(entries) == 0 {
		t.Fatal("expected at least one reflog entry")
	}
}

func TestGetRemotes_NoRemote(t *testing.T) {
	_, repo := makeRepo(t)
	remotes, err := repo.GetRemotes()
	if err != nil {
		t.Fatalf("GetRemotes: %v", err)
	}
	if len(remotes) != 0 {
		t.Errorf("expected 0 remotes, got %d", len(remotes))
	}
}

func TestDiscardFile_UntrackedFile(t *testing.T) {
	dir, repo := makeRepo(t)
	os.WriteFile(filepath.Join(dir, "new.txt"), []byte("untracked"), 0644)
	if err := repo.DiscardFile("new.txt"); err != nil {
		t.Fatalf("DiscardFile untracked: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "new.txt")); !os.IsNotExist(err) {
		t.Error("expected untracked file to be removed")
	}
}

func TestDiscardFile_ModifiedFile(t *testing.T) {
	dir, repo := makeRepo(t)
	os.WriteFile(filepath.Join(dir, "f.txt"), []byte("changed"), 0644)
	if err := repo.DiscardFile("f.txt"); err != nil {
		t.Fatalf("DiscardFile modified: %v", err)
	}
	content, _ := os.ReadFile(filepath.Join(dir, "f.txt"))
	if string(content) != "hello" {
		t.Errorf("expected file restored to 'hello', got %q", string(content))
	}
}

func TestStashPopByIndex(t *testing.T) {
	dir, repo := makeRepo(t)
	os.WriteFile(filepath.Join(dir, "f.txt"), []byte("v1"), 0644)
	repo.Stash()
	os.WriteFile(filepath.Join(dir, "f.txt"), []byte("v2"), 0644)
	repo.Stash()
	if err := repo.StashPopIndex(0); err != nil {
		t.Fatalf("StashPopIndex: %v", err)
	}
	entries, _ := repo.GetStashList()
	if len(entries) != 1 {
		t.Errorf("expected 1 stash entry after pop, got %d", len(entries))
	}
}
