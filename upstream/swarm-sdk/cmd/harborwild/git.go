package main

import (
	"bytes"
	"fmt"
	"os/exec"
	"strings"
)

// runGit runs git in dir and returns trimmed stdout, or an error including
// stderr on failure. Every git invocation in this tool is read-only against
// the existing repository (log/diff/archive/rev-parse) -- this tool never
// commits, checks out, resets, or otherwise mutates the caller's working
// tree or refs.
func runGit(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("git %s: %w\n%s", strings.Join(args, " "), err, stderr.String())
	}
	return strings.TrimRight(stdout.String(), "\n"), nil
}

// runGitBytes is runGit but returns raw stdout bytes (for binary output like
// `git archive`), not trimmed/decoded as text.
func runGitBytes(dir string, args ...string) ([]byte, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("git %s: %w\n%s", strings.Join(args, " "), err, stderr.String())
	}
	return stdout.Bytes(), nil
}

func gitShowToplevel(dir string) (string, error) {
	return runGit(dir, "rev-parse", "--show-toplevel")
}

func gitParent(repo, sha string) (string, error) {
	return runGit(repo, "rev-parse", sha+"~1")
}

func gitResolve(repo, sha string) (string, error) {
	return runGit(repo, "rev-parse", sha)
}

// gitChangedFiles returns every path git diff reports as touched between
// parent and sha (added, modified, or deleted), relative to repo root.
func gitChangedFiles(repo, parent, sha string) ([]string, error) {
	out, err := runGit(repo, "diff", "--name-only", parent, sha)
	if err != nil {
		return nil, err
	}
	if out == "" {
		return nil, nil
	}
	return strings.Split(out, "\n"), nil
}

// gitDiffPaths returns `git diff parent sha -- <paths...>` as raw patch
// text, suitable for `patch -p1` or `git apply` against a tree checked out
// at parent. Empty paths list means "no diff" (returns "").
//
// Deliberately uses runGitBytes, NOT runGit: runGit trims ALL trailing
// newlines from stdout, which for a unified diff silently eats the newline
// terminating the final hunk line -- POSIX `patch` then fails with
// "patch unexpectedly ends in middle of line" on the last hunk of the last
// file in the diff. A patch file must end in exactly one trailing newline
// (or an explicit "\ No newline at end of file" marker, which git already
// emits correctly on its own); this function restores that invariant
// explicitly rather than relying on a general-purpose trimmed-string helper.
func gitDiffPaths(repo, parent, sha string, paths []string) (string, error) {
	if len(paths) == 0 {
		return "", nil
	}
	args := []string{"diff", "--no-color", "--src-prefix=a/", "--dst-prefix=b/", parent, sha, "--"}
	args = append(args, paths...)
	out, err := runGitBytes(repo, args...)
	if err != nil {
		return "", err
	}
	s := string(out)
	for len(s) > 0 && s[len(s)-1] == '\n' {
		s = s[:len(s)-1]
	}
	if s == "" {
		return "", nil
	}
	return s + "\n", nil
}

// gitArchiveTarGz archives the full repo tree at commit into a .tar.gz byte
// stream. Used so the resulting Harbor task is self-contained: it carries its
// own snapshot of the repo and never depends on this sandbox's live working
// copy or a network git remote at task-build time.
func gitArchiveTarGz(repo, commit string) ([]byte, error) {
	return runGitBytes(repo, "archive", "--format=tar.gz", commit)
}
