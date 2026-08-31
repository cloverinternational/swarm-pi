package forge

import (
	"os"
	"path/filepath"
	"strings"
)

// ─── Git worktree authorization ──────────────────────────────────────────────
//
// A conversation is pinned to one workspace directory, and apply_patch refuses
// to write outside it. That is the right default, but it made a common and
// explicitly authorized workflow impossible: a supervisor assigns work in a
// sibling git worktree of the SAME repository, the shell and read tools follow
// it via their cwd parameter, and apply_patch — which had no cwd at all —
// rejected every path in that worktree as "outside the workspace". The task
// could be read and tested but not implemented. See issues #289, #290, #292,
// #295.
//
// Linked worktrees of one repository are not arbitrary filesystem locations;
// they are alternate checkouts of the same project, sharing a single .git
// directory. Recognizing that relationship restores the workflow without
// widening access to unrelated paths: a directory qualifies only if it
// resolves to the same git common directory as the workspace.

// gitCommonDir returns the canonical common .git directory governing dir, or
// "" when dir is not inside a git repository.
//
// It handles both layouts:
//   - a normal checkout, where <root>/.git is a directory; and
//   - a linked worktree, where <root>/.git is a FILE containing
//     "gitdir: /abs/path/to/mainrepo/.git/worktrees/<name>", whose common
//     directory is the enclosing .git.
func gitCommonDir(dir string) string {
	current := filepath.Clean(dir)
	for {
		gitPath := filepath.Join(current, ".git")
		info, err := os.Lstat(gitPath)
		switch {
		case err == nil && info.IsDir():
			return canonicalOrClean(gitPath)
		case err == nil && info.Mode().IsRegular():
			if common := commonDirFromGitFile(gitPath); common != "" {
				return common
			}
		}
		parent := filepath.Dir(current)
		if parent == current {
			return ""
		}
		current = parent
	}
}

// commonDirFromGitFile reads a linked worktree's .git file and returns the
// repository's common .git directory.
func commonDirFromGitFile(gitFile string) string {
	raw, err := os.ReadFile(gitFile)
	if err != nil {
		return ""
	}
	line := strings.TrimSpace(string(raw))
	const prefix = "gitdir:"
	if !strings.HasPrefix(line, prefix) {
		return ""
	}
	gitDir := strings.TrimSpace(strings.TrimPrefix(line, prefix))
	if gitDir == "" {
		return ""
	}
	if !filepath.IsAbs(gitDir) {
		gitDir = filepath.Join(filepath.Dir(gitFile), gitDir)
	}
	gitDir = filepath.Clean(gitDir)
	// .../\.git/worktrees/<name> -> .../\.git
	if parent := filepath.Dir(gitDir); filepath.Base(parent) == "worktrees" {
		return canonicalOrClean(filepath.Dir(parent))
	}
	return canonicalOrClean(gitDir)
}

func canonicalOrClean(path string) string {
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		return filepath.Clean(resolved)
	}
	return filepath.Clean(path)
}

// sameGitRepository reports whether two directories are checkouts of one
// repository — the same checkout, or two linked worktrees of it.
//
// It is deliberately conservative: if either side is not in a git repository,
// the answer is false. Absence of evidence never grants access.
func sameGitRepository(a, b string) bool {
	if a == "" || b == "" {
		return false
	}
	commonA := gitCommonDir(a)
	if commonA == "" {
		return false
	}
	return commonA == gitCommonDir(b)
}
