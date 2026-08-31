package conversation

// gitsha.go — resolve HEAD without forking git.
//
// The constraint that shapes this file: measurement must never add cost to the
// agent's hot path. Shelling out to `git rev-parse HEAD` costs a fork+exec
// (~5-15ms, and worse under load); doing that per message, or per tool call,
// would be a syscall storm bought for a bookkeeping field.
//
// So HEAD is read straight out of the .git directory — 1 to 3 small file reads —
// and the result is memoised per workspace for the life of the process. In
// practice that is a handful of reads once, at conversation creation, and zero
// thereafter. There is no exec.Command anywhere in this package.
//
// The value is a HINT, not ground truth. A SHA self-invalidates on the first
// rebase, amend or squash, and agent history does get rewritten. It answers
// "where did this run start", nothing stronger.

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// maxRefFileBytes caps every file this package reads. HEAD and a loose ref are
// tens of bytes; packed-refs is the only one that can grow, and a few megabytes
// is already an extreme repository. The cap exists so a corrupt or hostile .git
// cannot make conversation creation allocate without bound.
const maxRefFileBytes = 8 << 20 // 8 MiB

// gitSHACache memoises workspace path -> resolved SHA (possibly ""). A negative
// result is cached too: a workspace that is not a git repository must not be
// re-probed on every conversation creation.
var gitSHACache sync.Map // map[string]string

// GitSHA returns the commit SHA that HEAD points at for the repository
// containing workspacePath, or "" when it cannot be determined cheaply (not a
// repository, unborn branch, unreadable .git, malformed ref).
//
// "" is a normal, expected answer and never an error — a conversation created
// outside a git checkout is perfectly valid and simply carries no git_sha key.
//
// The first call per workspace does the file reads; every later call is a
// sync.Map load.
func GitSHA(workspacePath string) string {
	if workspacePath == "" {
		return ""
	}
	if cached, ok := gitSHACache.Load(workspacePath); ok {
		return cached.(string)
	}
	sha := resolveGitSHA(workspacePath)
	gitSHACache.Store(workspacePath, sha)
	return sha
}

// resolveGitSHA does the uncached work: locate the git directory, read HEAD,
// and follow one level of symbolic ref.
func resolveGitSHA(workspacePath string) string {
	gitDir, commonDir := locateGitDir(workspacePath)
	if gitDir == "" {
		return ""
	}

	head := strings.TrimSpace(readSmallFile(filepath.Join(gitDir, "HEAD")))
	if head == "" {
		return ""
	}

	// Detached HEAD: the file holds the SHA directly.
	if !strings.HasPrefix(head, "ref:") {
		return validSHA(head)
	}

	ref := strings.TrimSpace(strings.TrimPrefix(head, "ref:"))
	if ref == "" || strings.Contains(ref, "..") {
		return ""
	}

	// Loose ref, in this git dir first and then in the common dir. A linked
	// worktree keeps its own HEAD but shares refs/ with the main checkout, so
	// both locations must be tried before falling back to packed-refs.
	for _, dir := range []string{gitDir, commonDir} {
		if dir == "" {
			continue
		}
		if sha := validSHA(strings.TrimSpace(readSmallFile(filepath.Join(dir, filepath.FromSlash(ref))))); sha != "" {
			return sha
		}
	}

	// Packed ref: a repository that has been gc'd keeps branch tips in one
	// packed-refs file instead of individual loose files.
	for _, dir := range []string{gitDir, commonDir} {
		if dir == "" {
			continue
		}
		if sha := lookupPackedRef(filepath.Join(dir, "packed-refs"), ref); sha != "" {
			return sha
		}
	}
	return ""
}

// locateGitDir returns the git directory for workspacePath and, when the
// workspace is a linked worktree, the shared common directory. Both are absolute
// where possible; either may be "".
//
// It walks upward from workspacePath so a conversation created in a
// subdirectory of a checkout still resolves — the same containment rule git
// itself uses. It does NOT cross into a parent repository once a .git is found.
func locateGitDir(workspacePath string) (gitDir, commonDir string) {
	dir := workspacePath
	for {
		candidate := filepath.Join(dir, ".git")
		info, err := os.Stat(candidate)
		switch {
		case err == nil && info.IsDir():
			// Ordinary checkout.
			return candidate, ""
		case err == nil && info.Mode().IsRegular():
			// Linked worktree or submodule: ".git" is a file containing
			// "gitdir: <path>". The path may be relative to the workspace.
			contents := strings.TrimSpace(readSmallFile(candidate))
			pointer := strings.TrimSpace(strings.TrimPrefix(contents, "gitdir:"))
			if pointer == "" || pointer == contents {
				return "", ""
			}
			if !filepath.IsAbs(pointer) {
				pointer = filepath.Join(dir, pointer)
			}
			return pointer, readCommonDir(pointer)
		}

		parent := filepath.Dir(dir)
		if parent == dir {
			// Reached the filesystem root without finding a repository.
			return "", ""
		}
		dir = parent
	}
}

// readCommonDir resolves the "commondir" pointer inside a linked worktree's git
// directory, which is where the shared refs/ live. Returns "" when absent.
func readCommonDir(gitDir string) string {
	pointer := strings.TrimSpace(readSmallFile(filepath.Join(gitDir, "commondir")))
	if pointer == "" {
		return ""
	}
	if !filepath.IsAbs(pointer) {
		pointer = filepath.Join(gitDir, pointer)
	}
	return filepath.Clean(pointer)
}

// lookupPackedRef scans packed-refs for ref and returns its SHA, or "" when the
// file is missing or the ref is not packed. Peeled tag lines ("^<sha>") are
// skipped: they annotate the previous line, they are not entries themselves.
func lookupPackedRef(path, ref string) string {
	contents := readSmallFile(path)
	if contents == "" {
		return ""
	}
	for _, line := range strings.Split(contents, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || line[0] == '#' || line[0] == '^' {
			continue
		}
		sha, name, found := strings.Cut(line, " ")
		if !found || strings.TrimSpace(name) != ref {
			continue
		}
		return validSHA(sha)
	}
	return ""
}

// readSmallFile reads path, returning "" for any failure or for a file larger
// than maxRefFileBytes. Errors are deliberately swallowed: a missing or
// unreadable ref is an expected outcome, not a fault worth propagating into
// conversation creation.
func readSmallFile(path string) string {
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() > maxRefFileBytes {
		return ""
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return string(data)
}

// validSHA returns s when it looks like a git object ID (40 hex for SHA-1, 64
// for SHA-256) and "" otherwise, so a malformed ref never reaches the store.
func validSHA(s string) string {
	s = strings.TrimSpace(s)
	if len(s) != 40 && len(s) != 64 {
		return ""
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') && (c < 'A' || c > 'F') {
			return ""
		}
	}
	return s
}
