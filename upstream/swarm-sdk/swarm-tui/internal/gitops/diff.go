package gitops

import (
	"bytes"
	"fmt"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
)

// DiffOptions configures diff generation.
type DiffOptions struct {
	ContextLines int  // Number of context lines (default 3)
	IgnoreSpace  bool // Ignore whitespace changes
	WordDiff     bool // Show word-level diff
	ColorOutput  bool // Include ANSI color codes
}

// DefaultDiffOptions returns default diff options.
func DefaultDiffOptions() DiffOptions {
	return DiffOptions{
		ContextLines: 3,
	}
}

// GetFileDiffWithOptions returns the diff for a file with custom options.
func (g *GitRepo) GetFileDiffWithOptions(path string, staged bool, opts DiffOptions) (*FileDiff, error) {
	args := []string{"diff"}

	if staged {
		args = append(args, "--cached")
	}

	args = append(args, fmt.Sprintf("-U%d", opts.ContextLines))

	if opts.IgnoreSpace {
		args = append(args, "-w")
	}

	if opts.ColorOutput {
		args = append(args, "--color=always")
	}

	args = append(args, "--", path)

	cmd := exec.Command("git", args...)
	cmd.Dir = g.Path
	output, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("failed to get diff: %w", err)
	}

	return parseDiffDetailed(string(output), path)
}

// parseDiffDetailed parses a diff with more detailed information.
func parseDiffDetailed(diffText, path string) (*FileDiff, error) {
	diff := &FileDiff{
		Path:  path,
		Hunks: make([]Hunk, 0),
	}

	if strings.Contains(diffText, "Binary files") {
		diff.Binary = true
		return diff, nil
	}

	// Check for rename
	renameRegex := regexp.MustCompile(`rename from (.+)`)
	if matches := renameRegex.FindStringSubmatch(diffText); matches != nil {
		diff.OldPath = matches[1]
	}

	// Parse hunks with detailed line information
	hunkRegex := regexp.MustCompile(`@@ -(\d+)(?:,(\d+))? \+(\d+)(?:,(\d+))? @@(.*)`)
	lines := strings.Split(diffText, "\n")

	var currentHunk *Hunk
	for _, line := range lines {
		if matches := hunkRegex.FindStringSubmatch(line); matches != nil {
			if currentHunk != nil {
				diff.Hunks = append(diff.Hunks, *currentHunk)
			}

			startLine, _ := strconv.Atoi(matches[1])
			startCount := 1
			if matches[2] != "" {
				startCount, _ = strconv.Atoi(matches[2])
			}
			newStart, _ := strconv.Atoi(matches[3])
			newCount := 1
			if matches[4] != "" {
				newCount, _ = strconv.Atoi(matches[4])
			}

			currentHunk = &Hunk{
				Header:    line,
				StartLine: startLine,
				EndLine:   startLine + startCount - 1,
				NewStart:  newStart,
				NewEnd:    newStart + newCount - 1,
			}
		} else if currentHunk != nil {
			// Skip file headers
			if strings.HasPrefix(line, "---") || strings.HasPrefix(line, "+++") ||
				strings.HasPrefix(line, "diff --git") || strings.HasPrefix(line, "index ") ||
				strings.HasPrefix(line, "new file") || strings.HasPrefix(line, "deleted file") ||
				strings.HasPrefix(line, "rename from") || strings.HasPrefix(line, "rename to") {
				continue
			}
			if line != "" || len(currentHunk.Lines) > 0 {
				currentHunk.Lines = append(currentHunk.Lines, line)
			}
		}
	}

	if currentHunk != nil && len(currentHunk.Lines) > 0 {
		diff.Hunks = append(diff.Hunks, *currentHunk)
	}

	return diff, nil
}

// ParseDiffLines parses diff lines into structured DiffLine objects.
func ParseDiffLines(hunk *Hunk) []DiffLine {
	var lines []DiffLine
	oldNum := hunk.StartLine
	newNum := hunk.NewStart

	for i, line := range hunk.Lines {
		if len(line) == 0 {
			lines = append(lines, DiffLine{
				Content: "",
				Type:    DiffContext,
				OldNum:  oldNum,
				NewNum:  newNum,
				HunkIdx: i,
			})
			oldNum++
			newNum++
			continue
		}

		prefix := line[0]
		content := ""
		if len(line) > 1 {
			content = line[1:]
		}

		var dl DiffLine
		switch prefix {
		case '+':
			dl = DiffLine{
				Content: content,
				Type:    DiffAdd,
				NewNum:  newNum,
				HunkIdx: i,
			}
			newNum++
		case '-':
			dl = DiffLine{
				Content: content,
				Type:    DiffDelete,
				OldNum:  oldNum,
				HunkIdx: i,
			}
			oldNum++
		case '@':
			dl = DiffLine{
				Content: line,
				Type:    DiffHeader,
				HunkIdx: i,
			}
		default:
			dl = DiffLine{
				Content: content,
				Type:    DiffContext,
				OldNum:  oldNum,
				NewNum:  newNum,
				HunkIdx: i,
			}
			oldNum++
			newNum++
		}
		lines = append(lines, dl)
	}

	return lines
}

// GetHunkDiff returns the raw diff for a specific hunk.
func (g *GitRepo) GetHunkDiff(path string, staged bool, hunkIndex int) (string, error) {
	diff, err := g.GetFileDiff(path, staged)
	if err != nil {
		return "", err
	}

	if hunkIndex < 0 || hunkIndex >= len(diff.Hunks) {
		return "", fmt.Errorf("hunk index out of range")
	}

	hunk := diff.Hunks[hunkIndex]
	var sb strings.Builder
	sb.WriteString(hunk.Header)
	sb.WriteString("\n")
	for _, line := range hunk.Lines {
		sb.WriteString(line)
		sb.WriteString("\n")
	}

	return sb.String(), nil
}

// StageHunk stages a specific hunk of changes.
func (g *GitRepo) StageHunk(path string, hunkIndex int) error {
	// Get the full diff
	diff, err := g.GetFileDiff(path, false)
	if err != nil {
		return err
	}

	if hunkIndex < 0 || hunkIndex >= len(diff.Hunks) {
		return fmt.Errorf("hunk index out of range")
	}

	// Build a patch for just this hunk
	patch := buildHunkPatch(diff, hunkIndex)

	// Apply the patch
	cmd := exec.Command("git", "apply", "--cached", "--unidiff-zero", "-")
	cmd.Dir = g.Path
	cmd.Stdin = bytes.NewBufferString(patch)

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to stage hunk: %w", err)
	}

	g.InvalidateCache()
	return nil
}

// UnstageHunk unstages a specific hunk of changes.
func (g *GitRepo) UnstageHunk(path string, hunkIndex int) error {
	// Get the staged diff
	diff, err := g.GetFileDiff(path, true)
	if err != nil {
		return err
	}

	if hunkIndex < 0 || hunkIndex >= len(diff.Hunks) {
		return fmt.Errorf("hunk index out of range")
	}

	// Build a reverse patch for just this hunk
	patch := buildReverseHunkPatch(diff, hunkIndex)

	// Apply the reverse patch
	cmd := exec.Command("git", "apply", "--cached", "--unidiff-zero", "--reverse", "-")
	cmd.Dir = g.Path
	cmd.Stdin = bytes.NewBufferString(patch)

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to unstage hunk: %w", err)
	}

	g.InvalidateCache()
	return nil
}

// DiscardHunk discards a specific hunk of changes.
func (g *GitRepo) DiscardHunk(path string, hunkIndex int) error {
	// Get the unstaged diff
	diff, err := g.GetFileDiff(path, false)
	if err != nil {
		return err
	}

	if hunkIndex < 0 || hunkIndex >= len(diff.Hunks) {
		return fmt.Errorf("hunk index out of range")
	}

	// Build a reverse patch for just this hunk
	patch := buildReverseHunkPatch(diff, hunkIndex)

	// Apply the reverse patch to the working tree
	cmd := exec.Command("git", "apply", "--unidiff-zero", "--reverse", "-")
	cmd.Dir = g.Path
	cmd.Stdin = bytes.NewBufferString(patch)

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to discard hunk: %w", err)
	}

	g.InvalidateCache()
	return nil
}

// buildHunkPatch builds a patch for a single hunk.
func buildHunkPatch(diff *FileDiff, hunkIndex int) string {
	hunk := diff.Hunks[hunkIndex]
	var sb strings.Builder

	// Write patch header
	sb.WriteString(fmt.Sprintf("--- a/%s\n", diff.Path))
	sb.WriteString(fmt.Sprintf("+++ b/%s\n", diff.Path))
	sb.WriteString(hunk.Header)
	sb.WriteString("\n")

	for _, line := range hunk.Lines {
		sb.WriteString(line)
		sb.WriteString("\n")
	}

	return sb.String()
}

// buildReverseHunkPatch builds a reverse patch for a single hunk.
func buildReverseHunkPatch(diff *FileDiff, hunkIndex int) string {
	hunk := diff.Hunks[hunkIndex]
	var sb strings.Builder

	// Write patch header (reversed)
	sb.WriteString(fmt.Sprintf("--- a/%s\n", diff.Path))
	sb.WriteString(fmt.Sprintf("+++ b/%s\n", diff.Path))

	// Reverse the hunk header (swap old and new line numbers)
	reverseHeader := reverseHunkHeader(hunk.Header)
	sb.WriteString(reverseHeader)
	sb.WriteString("\n")

	// Reverse the lines (swap + and -)
	for _, line := range hunk.Lines {
		if len(line) > 0 {
			switch line[0] {
			case '+':
				sb.WriteString("-")
				sb.WriteString(line[1:])
			case '-':
				sb.WriteString("+")
				sb.WriteString(line[1:])
			default:
				sb.WriteString(line)
			}
		}
		sb.WriteString("\n")
	}

	return sb.String()
}

// reverseHunkHeader reverses a hunk header (swaps old/new line numbers).
func reverseHunkHeader(header string) string {
	hunkRegex := regexp.MustCompile(`@@ -(\d+)(?:,(\d+))? \+(\d+)(?:,(\d+))? @@(.*)`)
	matches := hunkRegex.FindStringSubmatch(header)
	if matches == nil {
		return header
	}

	oldStart := matches[1]
	oldCount := matches[2]
	newStart := matches[3]
	newCount := matches[4]
	context := matches[5]

	if oldCount == "" {
		oldCount = "1"
	}
	if newCount == "" {
		newCount = "1"
	}

	return fmt.Sprintf("@@ -%s,%s +%s,%s @@%s", newStart, newCount, oldStart, oldCount, context)
}

// DiffStats returns statistics for a diff.
type DiffStats struct {
	FilesChanged int
	Insertions   int
	Deletions    int
}

// GetDiffStats returns diff statistics for the repository.
func (g *GitRepo) GetDiffStats(staged bool) (*DiffStats, error) {
	args := []string{"diff", "--stat", "--numstat"}
	if staged {
		args = append(args, "--cached")
	}

	cmd := exec.Command("git", args...)
	cmd.Dir = g.Path
	output, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("failed to get diff stats: %w", err)
	}

	stats := &DiffStats{}
	lines := strings.SplitSeq(string(output), "\n")

	for line := range lines {
		fields := strings.Fields(line)
		if len(fields) >= 3 {
			if add, err := strconv.Atoi(fields[0]); err == nil {
				stats.Insertions += add
			}
			if del, err := strconv.Atoi(fields[1]); err == nil {
				stats.Deletions += del
			}
			stats.FilesChanged++
		}
	}

	return stats, nil
}

// GetFileDiffBetweenCommits returns the diff for a file between two commits.
func (g *GitRepo) GetFileDiffBetweenCommits(path, fromCommit, toCommit string) (*FileDiff, error) {
	args := []string{"diff", fromCommit, toCommit, "--", path}

	cmd := exec.Command("git", args...)
	cmd.Dir = g.Path
	output, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("failed to get diff: %w", err)
	}

	return parseDiffDetailed(string(output), path)
}

// GetCommitDiff returns the diff for a specific commit.
func (g *GitRepo) GetCommitDiff(commitSHA string) ([]*FileDiff, error) {
	args := []string{"show", "--format=", commitSHA}

	cmd := exec.Command("git", args...)
	cmd.Dir = g.Path
	output, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("failed to get commit diff: %w", err)
	}

	return parseMultiFileDiff(string(output))
}

// parseMultiFileDiff parses a diff containing multiple files.
func parseMultiFileDiff(diffText string) ([]*FileDiff, error) {
	var diffs []*FileDiff

	// Split by "diff --git"
	parts := strings.Split(diffText, "diff --git ")

	for _, part := range parts[1:] {
		part = "diff --git " + part

		// Extract file path
		pathRegex := regexp.MustCompile(`diff --git a/(.+) b/(.+)`)
		matches := pathRegex.FindStringSubmatch(part)
		if matches == nil {
			continue
		}

		path := matches[2]
		diff, err := parseDiffDetailed(part, path)
		if err != nil {
			continue
		}

		if matches[1] != matches[2] {
			diff.OldPath = matches[1]
		}

		diffs = append(diffs, diff)
	}

	return diffs, nil
}
