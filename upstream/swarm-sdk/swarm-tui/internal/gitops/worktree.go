package gitops

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// WorktreeInfo represents a single git worktree.
type WorktreeInfo struct {
	Path           string   `json:"path"`
	HeadSHA        string   `json:"headSHA"`
	Branch         string   `json:"branch"`
	IsMain         bool     `json:"isMain"`
	IsDirty        bool     `json:"isDirty"`
	SymlinkedDirs  []string `json:"symlinkDirs,omitempty"`
	DiskUsageBytes int64    `json:"diskUsageBytes,omitempty"`
}

// ListWorktrees returns all git worktrees for the repository at repoPath with
// only the cheap metadata that `git worktree list --porcelain` provides (path,
// HEAD, branch, IsMain). It performs NO per-worktree filesystem scans — no
// dirty-status check, no symlink detection, and crucially no recursive
// disk-usage walk. This makes it safe on the startup-critical path even when
// the resolved git top-level is an enormous tree (e.g. a stray `git init` in
// $HOME, where the working tree can contain hundreds of thousands of files).
//
// Callers that only need worktree identity/paths (workspace binding, worktree
// lookup/removal) must use this. Only UI that actually displays disk usage
// should pay for the enriched GetWorktrees.
func ListWorktrees(repoPath string) ([]WorktreeInfo, error) {
	cmd := exec.Command("git", "worktree", "list", "--porcelain")
	cmd.Dir = repoPath
	output, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("git worktree list: %w", err)
	}

	var worktrees []WorktreeInfo
	var current WorktreeInfo
	isFirst := true

	for raw := range strings.SplitSeq(string(output), "\n") {
		line := strings.TrimRight(raw, "\r")
		switch {
		case strings.HasPrefix(line, "worktree "):
			if current.Path != "" {
				worktrees = append(worktrees, current)
			}
			current = WorktreeInfo{
				Path:   strings.TrimPrefix(line, "worktree "),
				IsMain: isFirst,
			}
			isFirst = false
		case strings.HasPrefix(line, "HEAD "):
			current.HeadSHA = strings.TrimPrefix(line, "HEAD ")
		case strings.HasPrefix(line, "branch "):
			ref := strings.TrimPrefix(line, "branch ")
			current.Branch = strings.TrimPrefix(ref, "refs/heads/")
		}
	}
	if current.Path != "" {
		worktrees = append(worktrees, current)
	}

	return worktrees, nil
}

// GetWorktrees returns all git worktrees for the repository at repoPath,
// enriched with dirty status, symlinked-directory detection, and disk usage.
//
// WARNING: the disk-usage pass performs a full recursive filepath.Walk of every
// worktree tree, so this is O(total files) and can take tens of seconds on a
// large repository. Use it ONLY from UI that displays this data. For everything
// on the startup or control path use ListWorktrees instead.
func GetWorktrees(repoPath string) ([]WorktreeInfo, error) {
	worktrees, err := ListWorktrees(repoPath)
	if err != nil {
		return nil, err
	}

	// Check dirty status per worktree (best-effort).
	for i := range worktrees {
		worktrees[i].IsDirty = isWorktreeDirty(worktrees[i].Path)
	}

	// Detect symlinked directories and compute disk usage per worktree in a single pass.
	for i := range worktrees {
		worktrees[i].SymlinkedDirs = detectSymlinkedDirs(worktrees[i].Path)
		worktrees[i].DiskUsageBytes = DirDiskUsage(worktrees[i].Path)
	}

	return worktrees, nil
}

// isWorktreeDirty returns true if the worktree at path has uncommitted changes.
// Returns false if the dirty state cannot be determined (e.g., path unavailable).
func isWorktreeDirty(path string) bool {
	repo, err := NewGitRepo(path)
	if err != nil {
		return false
	}
	status, err := repo.GetStatus()
	if err != nil {
		return false
	}
	return status.IsDirty
}

// detectSymlinkedDirs returns a list of top-level directory names in the worktree
// that are symlinks (e.g., node_modules, .cache).
func detectSymlinkedDirs(worktreePath string) []string {
	entries, err := os.ReadDir(worktreePath)
	if err != nil {
		return nil
	}
	var symlinked []string
	for _, e := range entries {
		if e.Type()&os.ModeSymlink != 0 {
			// Follow the symlink and check if it points to a directory
			fullPath := filepath.Join(worktreePath, e.Name())
			if fi, err := os.Stat(fullPath); err == nil && fi.IsDir() {
				symlinked = append(symlinked, e.Name())
			}
		}
	}
	return symlinked
}
