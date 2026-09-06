// Package gitops provides Git operations for the TUI.
// This is a Go port of the octogit/git_status_sidebar.py functionality.
package gitops

import (
	"time"
)

// FileStatus represents the status of a file in the git repository.
type FileStatus int

const (
	FileUnmodified FileStatus = iota
	FileModified
	FileAdded
	FileDeleted
	FileRenamed
	FileCopied
	FileUntracked
	FileIgnored
	FileTypeChanged
	FileConflict
)

// String returns a string representation of the FileStatus.
func (s FileStatus) String() string {
	switch s {
	case FileUnmodified:
		return "unmodified"
	case FileModified:
		return "modified"
	case FileAdded:
		return "added"
	case FileDeleted:
		return "deleted"
	case FileRenamed:
		return "renamed"
	case FileCopied:
		return "copied"
	case FileUntracked:
		return "untracked"
	case FileIgnored:
		return "ignored"
	case FileTypeChanged:
		return "typechange"
	case FileConflict:
		return "conflict"
	default:
		return "unknown"
	}
}

// Symbol returns a short symbol for the FileStatus.
func (s FileStatus) Symbol() string {
	switch s {
	case FileUnmodified:
		return " "
	case FileModified:
		return "M"
	case FileAdded:
		return "A"
	case FileDeleted:
		return "D"
	case FileRenamed:
		return "R"
	case FileCopied:
		return "C"
	case FileUntracked:
		return "?"
	case FileIgnored:
		return "!"
	case FileTypeChanged:
		return "T"
	case FileConflict:
		return "U"
	default:
		return " "
	}
}

// FileEntry represents a file in the repository with its status.
type FileEntry struct {
	Path       string     `json:"path"`       // File path relative to repo root
	OldPath    string     `json:"oldPath"`    // Previous path (for renames)
	Status     FileStatus `json:"status"`     // Current status
	IsStaged   bool       `json:"isStaged"`   // Whether the change is staged
	IsBinary   bool       `json:"isBinary"`   // Whether the file is binary
	Insertions int        `json:"insertions"` // Number of lines added
	Deletions  int        `json:"deletions"`  // Number of lines deleted
}

// Hunk represents a diff hunk with header and line information.
// Equivalent to the Python Hunk dataclass.
type Hunk struct {
	Header    string   `json:"header"`    // Hunk header (e.g., @@ -1,5 +1,6 @@)
	Lines     []string `json:"lines"`     // Lines in the hunk
	StartLine int      `json:"startLine"` // Starting line number in original file
	EndLine   int      `json:"endLine"`   // Ending line number in original file
	NewStart  int      `json:"newStart"`  // Starting line number in new file
	NewEnd    int      `json:"newEnd"`    // Ending line number in new file
}

// DiffLine represents a single line in a diff.
type DiffLine struct {
	Content string       `json:"content"` // Line content (without prefix)
	Type    DiffLineType `json:"type"`    // Type of the line
	OldNum  int          `json:"oldNum"`  // Line number in old file (0 if not applicable)
	NewNum  int          `json:"newNum"`  // Line number in new file (0 if not applicable)
	HunkIdx int          `json:"hunkIdx"` // Index of the hunk this line belongs to
}

// DiffLineType represents the type of a diff line.
type DiffLineType int

const (
	DiffContext DiffLineType = iota // Context line (unchanged)
	DiffAdd                         // Added line
	DiffDelete                      // Deleted line
	DiffHeader                      // Hunk header
)

// String returns a string representation of the DiffLineType.
func (t DiffLineType) String() string {
	switch t {
	case DiffContext:
		return "context"
	case DiffAdd:
		return "add"
	case DiffDelete:
		return "delete"
	case DiffHeader:
		return "header"
	default:
		return "unknown"
	}
}

// Prefix returns the diff prefix character for the line type.
func (t DiffLineType) Prefix() string {
	switch t {
	case DiffContext:
		return " "
	case DiffAdd:
		return "+"
	case DiffDelete:
		return "-"
	case DiffHeader:
		return "@"
	default:
		return " "
	}
}

// FileDiff represents the complete diff for a file.
type FileDiff struct {
	Path    string `json:"path"`    // File path
	OldPath string `json:"oldPath"` // Old path (for renames)
	Hunks   []Hunk `json:"hunks"`   // List of hunks in the diff
	Binary  bool   `json:"binary"`  // Whether file is binary
}

// CommitInfo represents commit information for history display.
// Equivalent to the Python CommitInfo dataclass.
type CommitInfo struct {
	SHA         string    `json:"sha"`         // Full commit SHA
	ShortSHA    string    `json:"shortSHA"`    // Abbreviated SHA (7-8 chars)
	Message     string    `json:"message"`     // Commit message (first line)
	FullMessage string    `json:"fullMessage"` // Full commit message
	Author      string    `json:"author"`      // Author name
	AuthorEmail string    `json:"authorEmail"` // Author email
	Committer   string    `json:"committer"`   // Committer name
	Date        time.Time `json:"date"`        // Commit date
	ParentSHAs  []string  `json:"parentSHAs"`  // Parent commit SHAs
	Refs        []string  `json:"refs"`        // Associated refs (branches, tags)
}

// ShortMessage returns a truncated commit message.
func (c *CommitInfo) ShortMessage(maxLen int) string {
	if len(c.Message) <= maxLen {
		return c.Message
	}
	if maxLen <= 3 {
		return c.Message[:maxLen]
	}
	return c.Message[:maxLen-3] + "..."
}

// IsMerge returns true if this is a merge commit.
func (c *CommitInfo) IsMerge() bool {
	return len(c.ParentSHAs) > 1
}

// IsInitial returns true if this is an initial commit (no parents).
func (c *CommitInfo) IsInitial() bool {
	return len(c.ParentSHAs) == 0
}

// BranchInfo represents information about a git branch.
type BranchInfo struct {
	Name      string `json:"name"`      // Branch name
	IsRemote  bool   `json:"isRemote"`  // Whether this is a remote branch
	IsCurrent bool   `json:"isCurrent"` // Whether this is the current branch
	CommitSHA string `json:"commitSHA"` // SHA of the branch head
	Upstream  string `json:"upstream"`  // Upstream branch name (if tracking)
	Ahead     int    `json:"ahead"`     // Commits ahead of upstream
	Behind    int    `json:"behind"`    // Commits behind upstream
}

// FullName returns the full reference name.
func (b *BranchInfo) FullName() string {
	if b.IsRemote {
		return "refs/remotes/" + b.Name
	}
	return "refs/heads/" + b.Name
}

// RepoStatus represents the overall status of a repository.
type RepoStatus struct {
	Path          string       `json:"path"`          // Repository path
	CurrentBranch string       `json:"currentBranch"` // Current branch name
	HeadSHA       string       `json:"headSHA"`       // Current HEAD SHA
	IsDetached    bool         `json:"isDetached"`    // Whether HEAD is detached
	IsBare        bool         `json:"isBare"`        // Whether repo is bare
	IsDirty       bool         `json:"isDirty"`       // Whether there are uncommitted changes
	HasUntracked  bool         `json:"hasUntracked"`  // Whether there are untracked files
	Remote        string       `json:"remote"`        // Default remote name
	RemoteURL     string       `json:"remoteURL"`     // Default remote URL
	Ahead         int          `json:"ahead"`         // Commits ahead of upstream
	Behind        int          `json:"behind"`        // Commits behind upstream
	Staged        []FileEntry  `json:"staged"`        // Staged files
	Unstaged      []FileEntry  `json:"unstaged"`      // Unstaged modified files
	Untracked     []FileEntry  `json:"untracked"`     // Untracked files
	Branches      []BranchInfo `json:"branches"`      // All branches
}

// TotalChanges returns the total number of changed files.
func (r *RepoStatus) TotalChanges() int {
	return len(r.Staged) + len(r.Unstaged) + len(r.Untracked)
}

// HasChanges returns true if there are any changes.
func (r *RepoStatus) HasChanges() bool {
	return r.TotalChanges() > 0
}

// SyncStatus returns a human-readable sync status string.
func (r *RepoStatus) SyncStatus() string {
	if r.Ahead == 0 && r.Behind == 0 {
		return "up to date"
	}
	if r.Ahead > 0 && r.Behind > 0 {
		return "diverged"
	}
	if r.Ahead > 0 {
		return "ahead"
	}
	return "behind"
}

// StageAction represents an action to perform on staging.
type StageAction int

const (
	StageFile StageAction = iota
	UnstageFile
	StageHunk
	UnstageHunk
	DiscardChanges
)
