// Package gitops provides Git operations for the TUI.
// This is a Go port of the octogit/git_status_sidebar.py functionality.
package gitops

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

// GitRepo represents a Git repository and provides operations on it.
// This is equivalent to the Python GitStatusSidebar class.
type GitRepo struct {
	Path     string        // Repository root path
	mu       sync.RWMutex  // Mutex for thread-safe access
	cache    *statusCache  // Cache for expensive operations
	cacheTTL time.Duration // Cache TTL
}

// statusCache holds cached status information.
type statusCache struct {
	status    *RepoStatus
	timestamp time.Time
}

// NewGitRepo creates a new GitRepo instance.
// If path is empty, it searches for a git repository in the current directory.
func NewGitRepo(path string) (*GitRepo, error) {
	if path == "" {
		var err error
		path, err = os.Getwd()
		if err != nil {
			return nil, fmt.Errorf("failed to get current directory: %w", err)
		}
	}

	// Find the repository root
	root, err := findRepoRoot(path)
	if err != nil {
		return nil, err
	}

	return &GitRepo{
		Path:     root,
		cacheTTL: 5 * time.Second,
	}, nil
}

// findRepoRoot finds the root of a git repository.
func findRepoRoot(path string) (string, error) {
	cmd := exec.Command("git", "rev-parse", "--show-toplevel")
	cmd.Dir = path
	output, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("not a git repository: %w", err)
	}
	return strings.TrimSpace(string(output)), nil
}

// IsValid returns true if this is a valid git repository.
func (g *GitRepo) IsValid() bool {
	return g.Path != ""
}

// InvalidateCache clears the status cache.
func (g *GitRepo) InvalidateCache() {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.cache = nil
}

// GetStatus returns the current repository status.
func (g *GitRepo) GetStatus() (*RepoStatus, error) {
	g.mu.RLock()
	if g.cache != nil && time.Since(g.cache.timestamp) < g.cacheTTL {
		status := g.cache.status
		g.mu.RUnlock()
		return status, nil
	}
	g.mu.RUnlock()

	// Need to refresh cache
	status, err := g.fetchStatus()
	if err != nil {
		return nil, err
	}

	g.mu.Lock()
	g.cache = &statusCache{
		status:    status,
		timestamp: time.Now(),
	}
	g.mu.Unlock()

	return status, nil
}

// fetchStatus fetches the current repository status from git.
func (g *GitRepo) fetchStatus() (*RepoStatus, error) {
	status := &RepoStatus{
		Path: g.Path,
	}

	// Get current branch
	branch, isDetached, err := g.getCurrentBranch()
	if err != nil {
		return nil, err
	}
	status.CurrentBranch = branch
	status.IsDetached = isDetached

	// Get HEAD SHA
	headSHA, err := g.getHeadSHA()
	if err != nil {
		return nil, err
	}
	status.HeadSHA = headSHA

	// Get file status
	staged, unstaged, untracked, err := g.getFileStatus()
	if err != nil {
		return nil, err
	}
	status.Staged = staged
	status.Unstaged = unstaged
	status.Untracked = untracked
	status.IsDirty = len(staged) > 0 || len(unstaged) > 0
	status.HasUntracked = len(untracked) > 0

	// Get remote info
	remote, remoteURL, err := g.getRemoteInfo()
	if err == nil {
		status.Remote = remote
		status.RemoteURL = remoteURL
	}

	// Get ahead/behind counts
	ahead, behind, err := g.getAheadBehind()
	if err == nil {
		status.Ahead = ahead
		status.Behind = behind
	}

	// Get branches (best-effort; always assign so JSON serializes as [] not null).
	branches, err := g.getBranches()
	if err != nil {
		branches = []BranchInfo{}
	}
	status.Branches = branches

	return status, nil
}

// getCurrentBranch returns the current branch name and whether HEAD is detached.
func (g *GitRepo) getCurrentBranch() (string, bool, error) {
	cmd := exec.Command("git", "symbolic-ref", "--short", "HEAD")
	cmd.Dir = g.Path
	output, err := cmd.Output()
	if err != nil {
		// HEAD might be detached
		cmd = exec.Command("git", "rev-parse", "--short", "HEAD")
		cmd.Dir = g.Path
		output, err = cmd.Output()
		if err != nil {
			return "", false, fmt.Errorf("failed to get branch: %w", err)
		}
		return strings.TrimSpace(string(output)), true, nil
	}
	return strings.TrimSpace(string(output)), false, nil
}

// getHeadSHA returns the current HEAD SHA.
func (g *GitRepo) getHeadSHA() (string, error) {
	cmd := exec.Command("git", "rev-parse", "HEAD")
	cmd.Dir = g.Path
	output, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("failed to get HEAD SHA: %w", err)
	}
	return strings.TrimSpace(string(output)), nil
}

// getFileStatus returns staged, unstaged, and untracked files.
func (g *GitRepo) getFileStatus() (staged, unstaged, untracked []FileEntry, err error) {
	cmd := exec.Command("git", "status", "--porcelain=v1", "-z")
	cmd.Dir = g.Path
	output, err := cmd.Output()
	if err != nil {
		return nil, nil, nil, fmt.Errorf("failed to get status: %w", err)
	}

	// Split by null character
	entries := bytes.Split(output, []byte{0})

	for i := 0; i < len(entries); i++ {
		entry := entries[i]
		if len(entry) < 3 {
			continue
		}

		indexStatus := entry[0]
		workTreeStatus := entry[1]
		path := string(entry[3:])

		// Handle renames (next entry is the old path)
		var oldPath string
		if indexStatus == 'R' || indexStatus == 'C' {
			i++
			if i < len(entries) {
				oldPath = string(entries[i])
			}
		}

		// Process staged changes
		if indexStatus != ' ' && indexStatus != '?' {
			staged = append(staged, FileEntry{
				Path:     path,
				OldPath:  oldPath,
				Status:   parseStatus(indexStatus),
				IsStaged: true,
			})
		}

		// Process unstaged changes
		if workTreeStatus != ' ' && workTreeStatus != '?' {
			unstaged = append(unstaged, FileEntry{
				Path:     path,
				Status:   parseStatus(workTreeStatus),
				IsStaged: false,
			})
		}

		// Process untracked files
		if indexStatus == '?' && workTreeStatus == '?' {
			untracked = append(untracked, FileEntry{
				Path:     path,
				Status:   FileUntracked,
				IsStaged: false,
			})
		}
	}

	return staged, unstaged, untracked, nil
}

// parseStatus converts a git status character to FileStatus.
func parseStatus(c byte) FileStatus {
	switch c {
	case 'M':
		return FileModified
	case 'A':
		return FileAdded
	case 'D':
		return FileDeleted
	case 'R':
		return FileRenamed
	case 'C':
		return FileCopied
	case 'T':
		return FileTypeChanged
	case 'U':
		return FileConflict
	case '?':
		return FileUntracked
	case '!':
		return FileIgnored
	default:
		return FileUnmodified
	}
}

// getRemoteInfo returns the default remote name and URL.
func (g *GitRepo) getRemoteInfo() (string, string, error) {
	// Get default remote (usually origin)
	cmd := exec.Command("git", "remote")
	cmd.Dir = g.Path
	output, err := cmd.Output()
	if err != nil {
		return "", "", err
	}

	lines := strings.Split(strings.TrimSpace(string(output)), "\n")
	if len(lines) == 0 || lines[0] == "" {
		return "", "", fmt.Errorf("no remote configured")
	}

	remote := lines[0]

	// Get remote URL
	cmd = exec.Command("git", "remote", "get-url", remote)
	cmd.Dir = g.Path
	output, err = cmd.Output()
	if err != nil {
		return remote, "", nil
	}

	return remote, strings.TrimSpace(string(output)), nil
}

// getAheadBehind returns the number of commits ahead and behind upstream.
func (g *GitRepo) getAheadBehind() (int, int, error) {
	cmd := exec.Command("git", "rev-list", "--left-right", "--count", "@{upstream}...HEAD")
	cmd.Dir = g.Path
	output, err := cmd.Output()
	if err != nil {
		return 0, 0, err
	}

	parts := strings.Fields(string(output))
	if len(parts) != 2 {
		return 0, 0, fmt.Errorf("unexpected output format")
	}

	behind, _ := strconv.Atoi(parts[0])
	ahead, _ := strconv.Atoi(parts[1])

	return ahead, behind, nil
}

// getBranches returns all branches in the repository.
func (g *GitRepo) getBranches() ([]BranchInfo, error) {
	var branches []BranchInfo

	// Get local branches
	cmd := exec.Command("git", "branch", "--format=%(refname:short) %(objectname:short) %(upstream:short)")
	cmd.Dir = g.Path
	output, err := cmd.Output()
	if err != nil {
		return nil, err
	}

	currentBranch, _, _ := g.getCurrentBranch()

	for line := range strings.SplitSeq(strings.TrimSpace(string(output)), "\n") {
		if line == "" {
			continue
		}
		parts := strings.Fields(line)
		if len(parts) < 2 {
			continue
		}

		branch := BranchInfo{
			Name:      parts[0],
			CommitSHA: parts[1],
			IsCurrent: parts[0] == currentBranch,
		}
		if len(parts) >= 3 {
			branch.Upstream = parts[2]
		}
		branches = append(branches, branch)
	}

	// Get remote branches
	cmd = exec.Command("git", "branch", "-r", "--format=%(refname:short) %(objectname:short)")
	cmd.Dir = g.Path
	output, err = cmd.Output()
	if err == nil {
		for line := range strings.SplitSeq(strings.TrimSpace(string(output)), "\n") {
			if line == "" || strings.Contains(line, "HEAD") {
				continue
			}
			parts := strings.Fields(line)
			if len(parts) < 2 {
				continue
			}
			branches = append(branches, BranchInfo{
				Name:      parts[0],
				CommitSHA: parts[1],
				IsRemote:  true,
			})
		}
	}

	return branches, nil
}

// GetFileDiff returns the diff for a specific file.
func (g *GitRepo) GetFileDiff(path string, staged bool) (*FileDiff, error) {
	var args []string
	if staged {
		args = []string{"diff", "--cached", "--", path}
	} else {
		args = []string{"diff", "--", path}
	}

	cmd := exec.Command("git", args...)
	cmd.Dir = g.Path
	output, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("failed to get diff: %w", err)
	}

	return parseDiff(string(output), path)
}

// GetAllDiffs returns diffs for all changed files.
func (g *GitRepo) GetAllDiffs(staged bool) ([]*FileDiff, error) {
	status, err := g.GetStatus()
	if err != nil {
		return nil, err
	}

	var files []FileEntry
	if staged {
		files = status.Staged
	} else {
		files = status.Unstaged
	}

	var diffs []*FileDiff
	for _, f := range files {
		diff, err := g.GetFileDiff(f.Path, staged)
		if err != nil {
			continue
		}
		diffs = append(diffs, diff)
	}

	return diffs, nil
}

// parseDiff parses a unified diff string into a FileDiff.
func parseDiff(diffText, path string) (*FileDiff, error) {
	diff := &FileDiff{
		Path:  path,
		Hunks: make([]Hunk, 0),
	}

	if strings.Contains(diffText, "Binary files") {
		diff.Binary = true
		return diff, nil
	}

	// Parse hunks
	hunkRegex := regexp.MustCompile(`@@ -(\d+),?(\d*) \+(\d+),?(\d*) @@(.*)`)
	lines := strings.Split(diffText, "\n")

	var currentHunk *Hunk
	for _, line := range lines {
		if matches := hunkRegex.FindStringSubmatch(line); matches != nil {
			if currentHunk != nil {
				diff.Hunks = append(diff.Hunks, *currentHunk)
			}
			startLine, _ := strconv.Atoi(matches[1])
			newStart, _ := strconv.Atoi(matches[3])
			currentHunk = &Hunk{
				Header:    line,
				StartLine: startLine,
				NewStart:  newStart,
			}
		} else if currentHunk != nil && len(line) > 0 {
			// Skip file headers
			if strings.HasPrefix(line, "---") || strings.HasPrefix(line, "+++") ||
				strings.HasPrefix(line, "diff --git") || strings.HasPrefix(line, "index ") {
				continue
			}
			currentHunk.Lines = append(currentHunk.Lines, line)
		}
	}

	if currentHunk != nil {
		diff.Hunks = append(diff.Hunks, *currentHunk)
	}

	return diff, nil
}

// StageFile stages a file for commit.
func (g *GitRepo) StageFile(path string) error {
	cmd := exec.Command("git", "add", "--", path)
	cmd.Dir = g.Path
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to stage file: %w", err)
	}
	g.InvalidateCache()
	return nil
}

// UnstageFile unstages a file.
func (g *GitRepo) UnstageFile(path string) error {
	cmd := exec.Command("git", "reset", "HEAD", "--", path)
	cmd.Dir = g.Path
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to unstage file: %w", err)
	}
	g.InvalidateCache()
	return nil
}

// StageAll stages all changes.
func (g *GitRepo) StageAll() error {
	cmd := exec.Command("git", "add", "-A")
	cmd.Dir = g.Path
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to stage all: %w", err)
	}
	g.InvalidateCache()
	return nil
}

// UnstageAll unstages all changes.
func (g *GitRepo) UnstageAll() error {
	cmd := exec.Command("git", "reset", "HEAD")
	cmd.Dir = g.Path
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to unstage all: %w", err)
	}
	g.InvalidateCache()
	return nil
}

// DiscardChanges discards changes to a file.
func (g *GitRepo) DiscardChanges(path string) error {
	cmd := exec.Command("git", "checkout", "--", path)
	cmd.Dir = g.Path
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to discard changes: %w", err)
	}
	g.InvalidateCache()
	return nil
}

// Commit creates a new commit with the given message.
func (g *GitRepo) Commit(message string) error {
	cmd := exec.Command("git", "commit", "-m", message)
	cmd.Dir = g.Path
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to commit: %w", err)
	}
	g.InvalidateCache()
	return nil
}

// CommitWithBody creates a new commit with subject and body.
func (g *GitRepo) CommitWithBody(subject, body string) error {
	message := subject
	if body != "" {
		message = subject + "\n\n" + body
	}
	cmd := exec.Command("git", "commit", "-m", message)
	cmd.Dir = g.Path
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to commit: %w", err)
	}
	g.InvalidateCache()
	return nil
}

// Push pushes commits to the remote.
func (g *GitRepo) Push() error {
	cmd := exec.Command("git", "push")
	cmd.Dir = g.Path
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to push: %w", err)
	}
	g.InvalidateCache()
	return nil
}

// Pull pulls changes from the remote.
func (g *GitRepo) Pull() error {
	cmd := exec.Command("git", "pull")
	cmd.Dir = g.Path
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to pull: %w", err)
	}
	g.InvalidateCache()
	return nil
}

// Fetch fetches changes from the remote without merging.
func (g *GitRepo) Fetch() error {
	cmd := exec.Command("git", "fetch", "--all")
	cmd.Dir = g.Path
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to fetch: %w", err)
	}
	g.InvalidateCache()
	return nil
}

// SwitchBranch switches to a different branch.
func (g *GitRepo) SwitchBranch(branch string) error {
	cmd := exec.Command("git", "checkout", branch)
	cmd.Dir = g.Path
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to switch branch: %w", err)
	}
	g.InvalidateCache()
	return nil
}

// CreateBranch creates a new branch.
func (g *GitRepo) CreateBranch(name string) error {
	cmd := exec.Command("git", "checkout", "-b", name)
	cmd.Dir = g.Path
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to create branch: %w", err)
	}
	g.InvalidateCache()
	return nil
}

// Stash stashes the current changes.
func (g *GitRepo) Stash() error {
	cmd := exec.Command("git", "stash")
	cmd.Dir = g.Path
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to stash: %w", err)
	}
	g.InvalidateCache()
	return nil
}

// StashPop pops the most recent stash.
func (g *GitRepo) StashPop() error {
	cmd := exec.Command("git", "stash", "pop")
	cmd.Dir = g.Path
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to pop stash: %w", err)
	}
	g.InvalidateCache()
	return nil
}

// GetCommitHistory returns the commit history.
func (g *GitRepo) GetCommitHistory(maxCount int) ([]CommitInfo, error) {
	format := "%H%n%h%n%s%n%B%n%an%n%ae%n%cn%n%aI%n%P%n%D%n---COMMIT---"
	cmd := exec.Command("git", "log", fmt.Sprintf("-n%d", maxCount), fmt.Sprintf("--format=%s", format))
	cmd.Dir = g.Path
	output, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("failed to get history: %w", err)
	}

	var commits []CommitInfo
	entries := strings.SplitSeq(string(output), "---COMMIT---")

	for entry := range entries {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}

		lines := strings.Split(entry, "\n")
		if len(lines) < 10 {
			continue
		}

		// Parse the body (everything between subject and author name)
		var bodyLines []string
		for i := 3; i < len(lines)-6; i++ {
			bodyLines = append(bodyLines, lines[i])
		}

		dateStr := lines[len(lines)-3]
		date, _ := time.Parse(time.RFC3339, dateStr)

		parentStr := lines[len(lines)-2]
		parents := []string{}
		if parentStr != "" {
			parents = strings.Fields(parentStr)
		}

		refStr := lines[len(lines)-1]
		var refs []string
		if refStr != "" {
			// Split refs by comma and clean up
			rawRefs := strings.SplitSeq(refStr, ",")
			for r := range rawRefs {
				refs = append(refs, strings.TrimSpace(r))
			}
		}

		commits = append(commits, CommitInfo{
			SHA:         lines[0],
			ShortSHA:    lines[1],
			Message:     lines[2],
			FullMessage: strings.Join(bodyLines, "\n"),
			Author:      lines[len(lines)-6],
			AuthorEmail: lines[len(lines)-5],
			Committer:   lines[len(lines)-4],
			Date:        date,
			ParentSHAs:  parents,
			Refs:        refs,
		})
	}

	return commits, nil
}

// GetFileTree returns the file tree for the repository.
func (g *GitRepo) GetFileTree() ([]string, error) {
	cmd := exec.Command("git", "ls-files")
	cmd.Dir = g.Path
	output, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("failed to get file tree: %w", err)
	}

	files := strings.Split(strings.TrimSpace(string(output)), "\n")
	return files, nil
}

// GetAbsolutePath returns the absolute path for a file in the repo.
func (g *GitRepo) GetAbsolutePath(relPath string) string {
	return filepath.Join(g.Path, relPath)
}

// ─── Phase 2 additions ───────────────────────────────────────────────────────

// StashEntry represents a single stash entry.
type StashEntry struct {
	Index   int    `json:"index"`
	Message string `json:"message"`
	SHA     string `json:"sha"`
}

// fieldSep is the ASCII Record Separator used to delimit fields in git
// --format output. Pipe "|" is unsafe because stash messages and reflog
// subjects can contain literal pipes.
const fieldSep = "\x1e"

// GetStashList returns all stash entries for this repo.
func (g *GitRepo) GetStashList() ([]StashEntry, error) {
	cmd := exec.Command("git", "stash", "list", "--format=%gd\x1e%s\x1e%H")
	cmd.Dir = g.Path
	output, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("git stash list: %w", err)
	}

	entries := make([]StashEntry, 0)
	for i, line := range strings.Split(strings.TrimSpace(string(output)), "\n") {
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, fieldSep, 3)
		if len(parts) < 3 {
			continue
		}
		entries = append(entries, StashEntry{
			Index:   i,
			Message: parts[1],
			SHA:     parts[2],
		})
	}
	return entries, nil
}

// StashPopIndex pops stash@{index}.
func (g *GitRepo) StashPopIndex(index int) error {
	ref := fmt.Sprintf("stash@{%d}", index)
	cmd := exec.Command("git", "stash", "pop", ref)
	cmd.Dir = g.Path
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("git stash pop %s: %w\n%s", ref, err, out)
	}
	g.InvalidateCache()
	return nil
}

// ReflogEntry represents a single git reflog entry.
type ReflogEntry struct {
	SHA      string    `json:"sha"`
	ShortSHA string    `json:"shortSHA"`
	Action   string    `json:"action"`
	Message  string    `json:"message"`
	Date     time.Time `json:"date"`
}

// GetReflog returns the most recent reflog entries.
func (g *GitRepo) GetReflog(maxEntries int) ([]ReflogEntry, error) {
	args := []string{"reflog", "--format=%H\x1e%h\x1e%gs\x1e%ci", fmt.Sprintf("-n%d", maxEntries)}
	cmd := exec.Command("git", args...)
	cmd.Dir = g.Path
	output, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("git reflog: %w", err)
	}

	entries := make([]ReflogEntry, 0)
	for line := range strings.SplitSeq(strings.TrimSpace(string(output)), "\n") {
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, fieldSep, 4)
		if len(parts) < 4 {
			continue
		}
		date, _ := time.Parse("2006-01-02 15:04:05 -0700", parts[3])
		action := parts[2]
		if idx := strings.Index(action, ":"); idx > 0 {
			action = action[:idx]
		}
		entries = append(entries, ReflogEntry{
			SHA:      parts[0],
			ShortSHA: parts[1],
			Action:   action,
			Message:  parts[2],
			Date:     date,
		})
	}
	return entries, nil
}

// RemoteInfo represents a git remote.
type RemoteInfo struct {
	Name     string `json:"name"`
	FetchURL string `json:"fetchURL"`
	PushURL  string `json:"pushURL"`
}

// GetRemotes returns all remotes configured for this repo.
func (g *GitRepo) GetRemotes() ([]RemoteInfo, error) {
	cmd := exec.Command("git", "remote", "-v")
	cmd.Dir = g.Path
	output, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("git remote -v: %w", err)
	}

	seen := make(map[string]*RemoteInfo)
	var order []string

	for line := range strings.SplitSeq(strings.TrimSpace(string(output)), "\n") {
		if line == "" {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 3 {
			continue
		}
		name := fields[0]
		url := fields[1]
		kind := strings.Trim(fields[2], "()")

		if _, exists := seen[name]; !exists {
			seen[name] = &RemoteInfo{Name: name}
			order = append(order, name)
		}
		if kind == "fetch" {
			seen[name].FetchURL = url
		} else if kind == "push" {
			seen[name].PushURL = url
		}
	}

	remotes := make([]RemoteInfo, 0, len(order))
	for _, name := range order {
		remotes = append(remotes, *seen[name])
	}
	return remotes, nil
}

// DiscardFile discards changes to path. For tracked files, restores HEAD version.
// For untracked files, removes the file.
func (g *GitRepo) DiscardFile(path string) error {
	// Check if tracked
	lsCmd := exec.Command("git", "ls-files", "--error-unmatch", path)
	lsCmd.Dir = g.Path
	if err := lsCmd.Run(); err != nil {
		// Untracked — clean it
		cleanCmd := exec.Command("git", "clean", "-f", path)
		cleanCmd.Dir = g.Path
		if out, err := cleanCmd.CombinedOutput(); err != nil {
			return fmt.Errorf("git clean -f %s: %w\n%s", path, err, out)
		}
		g.InvalidateCache()
		return nil
	}
	// Tracked — restore
	restoreCmd := exec.Command("git", "checkout", "--", path)
	restoreCmd.Dir = g.Path
	if out, err := restoreCmd.CombinedOutput(); err != nil {
		return fmt.Errorf("git checkout -- %s: %w\n%s", path, err, out)
	}
	g.InvalidateCache()
	return nil
}
