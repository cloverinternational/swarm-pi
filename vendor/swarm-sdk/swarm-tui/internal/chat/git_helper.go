package chat

import (
	"fmt"
	"os/exec"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// GitBranch represents a git branch with metadata
type GitBranch struct {
	Name           string
	LastCommitTime time.Time
	CommitMsg      string
	IsCurrent      bool
}

// GitHelper provides git operations for branch management
type GitHelper struct {
	workspaceRoot string
}

// NewGitHelper creates a new GitHelper for the given workspace
func NewGitHelper(root string) *GitHelper {
	return &GitHelper{
		workspaceRoot: root,
	}
}

// SetWorkspaceRoot updates the workspace root
func (h *GitHelper) SetWorkspaceRoot(root string) {
	h.workspaceRoot = root
}

// IsRepo checks if the workspace is a git repository
func (h *GitHelper) IsRepo() bool {
	if h.workspaceRoot == "" {
		return false
	}

	cmd := exec.Command("git", "rev-parse", "--is-inside-work-tree")
	cmd.Dir = h.workspaceRoot
	output, err := cmd.Output()
	if err != nil {
		return false
	}
	return strings.TrimSpace(string(output)) == "true"
}

// CurrentBranch returns the current branch name
func (h *GitHelper) CurrentBranch() (string, error) {
	if h.workspaceRoot == "" {
		return "", nil
	}

	cmd := exec.Command("git", "branch", "--show-current")
	cmd.Dir = h.workspaceRoot
	output, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(output)), nil
}

// RecentBranches returns the most recent branches sorted by commit date
func (h *GitHelper) RecentBranches(limit int) ([]GitBranch, error) {
	if h.workspaceRoot == "" || limit <= 0 {
		return nil, nil
	}

	// Get current branch for marking
	currentBranch, _ := h.CurrentBranch()

	// Get branches sorted by commit date with format:
	// refname:short | committerdate:unix | subject
	cmd := exec.Command("git", "for-each-ref",
		"--count", strconv.Itoa(limit),
		"--sort=-committerdate",
		"refs/heads/",
		"--format=%(refname:short)|%(committerdate:unix)|%(subject)",
	)
	cmd.Dir = h.workspaceRoot
	output, err := cmd.Output()
	if err != nil {
		return nil, err
	}

	lines := strings.Split(strings.TrimSpace(string(output)), "\n")
	branches := make([]GitBranch, 0, len(lines))

	for _, line := range lines {
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, "|", 3)
		if len(parts) < 3 {
			continue
		}

		name := parts[0]
		timestamp, _ := strconv.ParseInt(parts[1], 10, 64)
		commitMsg := parts[2]

		// Truncate long commit messages
		if len(commitMsg) > 50 {
			commitMsg = commitMsg[:47] + "..."
		}

		branches = append(branches, GitBranch{
			Name:           name,
			LastCommitTime: time.Unix(timestamp, 0),
			CommitMsg:      commitMsg,
			IsCurrent:      name == currentBranch,
		})
	}

	return branches, nil
}

// BranchExists checks if a branch with the given name exists
func (h *GitHelper) BranchExists(name string) bool {
	if h.workspaceRoot == "" || name == "" {
		return false
	}

	cmd := exec.Command("git", "show-ref", "--verify", "--quiet", "refs/heads/"+name)
	cmd.Dir = h.workspaceRoot
	err := cmd.Run()
	return err == nil
}

// CreateBranch creates a new branch (does not checkout)
func (h *GitHelper) CreateBranch(name string) error {
	if h.workspaceRoot == "" {
		return nil
	}

	// Sanitize branch name
	name = sanitizeBranchName(name)

	cmd := exec.Command("git", "branch", name)
	cmd.Dir = h.workspaceRoot
	return cmd.Run()
}

// CheckoutBranch switches to the specified branch
func (h *GitHelper) CheckoutBranch(name string) error {
	if h.workspaceRoot == "" {
		return nil
	}

	cmd := exec.Command("git", "checkout", name)
	cmd.Dir = h.workspaceRoot
	return cmd.Run()
}

// CreateAndCheckout creates a new branch and switches to it
func (h *GitHelper) CreateAndCheckout(name string) error {
	if h.workspaceRoot == "" {
		return nil
	}

	// Sanitize branch name
	name = sanitizeBranchName(name)

	cmd := exec.Command("git", "checkout", "-b", name)
	cmd.Dir = h.workspaceRoot
	return cmd.Run()
}

// HasUncommittedChanges checks if there are uncommitted changes in the working tree
func (h *GitHelper) HasUncommittedChanges() (bool, error) {
	if h.workspaceRoot == "" {
		return false, nil
	}

	cmd := exec.Command("git", "status", "--porcelain")
	cmd.Dir = h.workspaceRoot
	output, err := cmd.Output()
	if err != nil {
		return false, err
	}
	return len(strings.TrimSpace(string(output))) > 0, nil
}

// InitRepo initializes a new git repository
func (h *GitHelper) InitRepo() error {
	if h.workspaceRoot == "" {
		return nil
	}

	cmd := exec.Command("git", "init")
	cmd.Dir = h.workspaceRoot
	return cmd.Run()
}

// GetWorkspaceRoot returns the current workspace root
func (h *GitHelper) GetWorkspaceRoot() string {
	return h.workspaceRoot
}

// FormatBranchAge formats a time as a human-readable age string
func (h *GitHelper) FormatBranchAge(t time.Time) string {
	if t.IsZero() {
		return "unknown"
	}

	diff := time.Since(t)

	switch {
	case diff < time.Minute:
		return "just now"
	case diff < time.Hour:
		mins := int(diff.Minutes())
		if mins == 1 {
			return "1 minute ago"
		}
		return strconv.Itoa(mins) + " minutes ago"
	case diff < 24*time.Hour:
		hours := int(diff.Hours())
		if hours == 1 {
			return "1 hour ago"
		}
		return strconv.Itoa(hours) + " hours ago"
	case diff < 7*24*time.Hour:
		days := int(diff.Hours() / 24)
		if days == 1 {
			return "1 day ago"
		}
		return strconv.Itoa(days) + " days ago"
	case diff < 30*24*time.Hour:
		weeks := int(diff.Hours() / 24 / 7)
		if weeks == 1 {
			return "1 week ago"
		}
		return strconv.Itoa(weeks) + " weeks ago"
	default:
		months := int(diff.Hours() / 24 / 30)
		if months == 1 {
			return "1 month ago"
		}
		return strconv.Itoa(months) + " months ago"
	}
}

// sanitizeBranchName cleans up a string to be a valid git branch name
func sanitizeBranchName(name string) string {
	// Convert to lowercase
	name = strings.ToLower(name)

	// Replace spaces and invalid chars with hyphens
	invalidChars := regexp.MustCompile(`[^a-z0-9/_-]`)
	name = invalidChars.ReplaceAllString(name, "-")

	// Remove consecutive hyphens
	multiHyphen := regexp.MustCompile(`-+`)
	name = multiHyphen.ReplaceAllString(name, "-")

	// Remove leading/trailing hyphens
	name = strings.Trim(name, "-")

	// Limit length
	if len(name) > 50 {
		name = name[:50]
	}

	return name
}

// SuggestBranchName generates a branch name from a prompt/description
func SuggestBranchName(prompt string) string {
	// Common prefixes based on keywords
	lowerPrompt := strings.ToLower(prompt)

	var prefix string
	switch {
	case strings.Contains(lowerPrompt, "fix") || strings.Contains(lowerPrompt, "bug"):
		prefix = "fix/"
	case strings.Contains(lowerPrompt, "add") || strings.Contains(lowerPrompt, "implement") || strings.Contains(lowerPrompt, "create"):
		prefix = "feature/"
	case strings.Contains(lowerPrompt, "refactor") || strings.Contains(lowerPrompt, "clean"):
		prefix = "refactor/"
	case strings.Contains(lowerPrompt, "test"):
		prefix = "test/"
	case strings.Contains(lowerPrompt, "doc"):
		prefix = "docs/"
	case strings.Contains(lowerPrompt, "update") || strings.Contains(lowerPrompt, "upgrade"):
		prefix = "update/"
	default:
		prefix = "feature/"
	}

	// Extract meaningful words from prompt
	words := extractBranchKeywords(prompt)
	if len(words) == 0 {
		return prefix + "task"
	}

	// Take first 3-4 meaningful words
	maxWords := 4
	if len(words) < maxWords {
		maxWords = len(words)
	}

	branchSuffix := strings.Join(words[:maxWords], "-")
	return prefix + sanitizeBranchName(branchSuffix)
}

// extractBranchKeywords pulls meaningful words from a prompt for branch naming
func extractBranchKeywords(prompt string) []string {
	// Remove common stop words
	stopWords := map[string]bool{
		"the": true, "a": true, "an": true, "and": true, "or": true, "but": true,
		"in": true, "on": true, "at": true, "to": true, "for": true, "of": true,
		"is": true, "it": true, "i": true, "we": true, "you": true, "they": true,
		"this": true, "that": true, "with": true, "as": true, "be": true, "are": true,
		"was": true, "were": true, "been": true, "being": true, "have": true, "has": true,
		"do": true, "does": true, "did": true, "will": true, "would": true, "could": true,
		"should": true, "may": true, "might": true, "can": true, "need": true, "want": true,
		"please": true, "help": true, "me": true, "my": true, "our": true, "your": true,
	}

	// Tokenize and filter
	wordRegex := regexp.MustCompile(`[a-zA-Z]+`)
	allWords := wordRegex.FindAllString(strings.ToLower(prompt), -1)

	var keywords []string
	seen := make(map[string]bool)

	for _, word := range allWords {
		if len(word) < 3 {
			continue
		}
		if stopWords[word] {
			continue
		}
		if seen[word] {
			continue
		}
		seen[word] = true
		keywords = append(keywords, word)
	}

	return keywords
}

// FindMatchingBranch finds existing branches that might match the given prompt
func (h *GitHelper) FindMatchingBranch(prompt string, branches []GitBranch) *GitBranch {
	if len(branches) == 0 || prompt == "" {
		return nil
	}

	keywords := extractBranchKeywords(prompt)
	if len(keywords) == 0 {
		return nil
	}

	type branchScore struct {
		branch *GitBranch
		score  int
	}

	var scores []branchScore

	for i := range branches {
		branch := &branches[i]
		branchLower := strings.ToLower(branch.Name)
		msgLower := strings.ToLower(branch.CommitMsg)

		score := 0
		for _, kw := range keywords {
			if strings.Contains(branchLower, kw) {
				score += 3 // Higher weight for branch name match
			}
			if strings.Contains(msgLower, kw) {
				score += 1 // Lower weight for commit message match
			}
		}

		if score > 0 {
			scores = append(scores, branchScore{branch: branch, score: score})
		}
	}

	if len(scores) == 0 {
		return nil
	}

	// Sort by score descending
	sort.Slice(scores, func(i, j int) bool {
		return scores[i].score > scores[j].score
	})

	// Only return if we have a reasonable match (at least 2 points)
	if scores[0].score >= 2 {
		return scores[0].branch
	}

	return nil
}

// FormatBranchAge returns a human-readable age string for a branch
func FormatBranchAge(t time.Time) string {
	now := time.Now()
	diff := now.Sub(t)

	switch {
	case diff < time.Minute:
		return "just now"
	case diff < time.Hour:
		mins := int(diff.Minutes())
		if mins == 1 {
			return "1m ago"
		}
		return strconv.Itoa(mins) + "m ago"
	case diff < 24*time.Hour:
		hours := int(diff.Hours())
		if hours == 1 {
			return "1h ago"
		}
		return strconv.Itoa(hours) + "h ago"
	case diff < 7*24*time.Hour:
		days := int(diff.Hours() / 24)
		if days == 1 {
			return "1d ago"
		}
		return strconv.Itoa(days) + "d ago"
	case diff < 30*24*time.Hour:
		weeks := int(diff.Hours() / 24 / 7)
		if weeks == 1 {
			return "1w ago"
		}
		return strconv.Itoa(weeks) + "w ago"
	default:
		months := int(diff.Hours() / 24 / 30)
		if months == 1 {
			return "1mo ago"
		}
		return strconv.Itoa(months) + "mo ago"
	}
}

// GetModifiedFiles returns recently modified files in the git repo
func (g *GitHelper) GetModifiedFiles(limit int) ([]string, error) {
	if !g.IsRepo() {
		return nil, fmt.Errorf("not a git repository")
	}

	// Get files modified in recent commits
	cmd := exec.Command("git", "diff", "--name-only", "HEAD~10..HEAD")
	cmd.Dir = g.workspaceRoot
	output, err := cmd.Output()
	if err != nil {
		return nil, err
	}

	lines := strings.Split(strings.TrimSpace(string(output)), "\n")
	var files []string
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line != "" {
			files = append(files, line)
			if len(files) >= limit {
				break
			}
		}
	}

	return files, nil
}
