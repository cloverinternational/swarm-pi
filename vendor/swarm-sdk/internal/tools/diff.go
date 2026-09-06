// Package tools provides diff generation utilities for comparing file contents.
// This enables showing the LLM what changes were made to a file.
package tools

import (
	"fmt"
	"strings"
)

// DiffResult contains the result of comparing two strings.
type DiffResult struct {
	// Diff is the unified diff string
	Diff string
	// Additions is the count of added lines
	Additions int
	// Removals is the count of removed lines
	Removals int
	// Changed indicates if there were any differences
	Changed bool
}

// GenerateDiff creates a unified diff between old and new content.
// Returns the diff string, addition count, and removal count.
// The filePath is used for the diff header.
func GenerateDiff(oldContent, newContent, filePath string) (diff string, additions int, removals int) {
	result := GenerateDiffResult(oldContent, newContent, filePath)
	return result.Diff, result.Additions, result.Removals
}

// GenerateDiffResult creates a detailed diff result between old and new content.
func GenerateDiffResult(oldContent, newContent, filePath string) *DiffResult {
	if oldContent == newContent {
		return &DiffResult{
			Diff:      "",
			Additions: 0,
			Removals:  0,
			Changed:   false,
		}
	}

	oldLines := splitLines(oldContent)
	newLines := splitLines(newContent)

	// Compute LCS-based diff
	hunks := computeHunks(oldLines, newLines)

	var diffBuilder strings.Builder

	// Write unified diff header
	if filePath != "" {
		diffBuilder.WriteString(fmt.Sprintf("--- a/%s\n", filePath))
		diffBuilder.WriteString(fmt.Sprintf("+++ b/%s\n", filePath))
	} else {
		diffBuilder.WriteString("--- a/file\n")
		diffBuilder.WriteString("+++ b/file\n")
	}

	additions := 0
	removals := 0

	for _, hunk := range hunks {
		// Write hunk header
		diffBuilder.WriteString(fmt.Sprintf("@@ -%d,%d +%d,%d @@\n",
			hunk.OldStart+1, hunk.OldCount,
			hunk.NewStart+1, hunk.NewCount))

		for _, line := range hunk.Lines {
			diffBuilder.WriteString(line.Prefix)
			diffBuilder.WriteString(line.Content)
			diffBuilder.WriteString("\n")

			switch line.Prefix {
			case "+":
				additions++
			case "-":
				removals++
			}
		}
	}

	return &DiffResult{
		Diff:      diffBuilder.String(),
		Additions: additions,
		Removals:  removals,
		Changed:   true,
	}
}

// DiffHunk represents a continuous section of changes
type DiffHunk struct {
	OldStart int
	OldCount int
	NewStart int
	NewCount int
	Lines    []DiffLine
}

// DiffLine represents a single line in a diff
type DiffLine struct {
	Prefix  string // " ", "+", or "-"
	Content string
}

// splitLines splits content into lines, handling empty content
func splitLines(content string) []string {
	if content == "" {
		return []string{}
	}
	lines := strings.Split(content, "\n")
	// Remove trailing empty line if content ends with newline
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}

// computeHunks computes the diff hunks between old and new lines
func computeHunks(oldLines, newLines []string) []DiffHunk {
	// Use Myers diff algorithm simplified
	lcs := computeLCS(oldLines, newLines)

	var hunks []DiffHunk
	var currentHunk *DiffHunk

	oldIdx := 0
	newIdx := 0
	lcsIdx := 0

	contextLines := 3 // Number of context lines around changes

	for oldIdx < len(oldLines) || newIdx < len(newLines) {
		// Check if current lines match LCS
		if lcsIdx < len(lcs) &&
			oldIdx < len(oldLines) &&
			newIdx < len(newLines) &&
			oldLines[oldIdx] == lcs[lcsIdx] &&
			newLines[newIdx] == lcs[lcsIdx] {
			// Context line (unchanged)
			if currentHunk != nil {
				currentHunk.Lines = append(currentHunk.Lines, DiffLine{" ", oldLines[oldIdx]})
				currentHunk.OldCount++
				currentHunk.NewCount++
			}
			oldIdx++
			newIdx++
			lcsIdx++
		} else {
			// Start new hunk if needed
			if currentHunk == nil {
				start := max(0, oldIdx-contextLines)
				newStart := max(0, newIdx-contextLines)

				currentHunk = &DiffHunk{
					OldStart: start,
					NewStart: newStart,
					Lines:    []DiffLine{},
				}

				// Add leading context
				for i := start; i < oldIdx; i++ {
					currentHunk.Lines = append(currentHunk.Lines, DiffLine{" ", oldLines[i]})
					currentHunk.OldCount++
					currentHunk.NewCount++
				}
			}

			// Removal from old
			if oldIdx < len(oldLines) && (lcsIdx >= len(lcs) || oldLines[oldIdx] != lcs[lcsIdx]) {
				currentHunk.Lines = append(currentHunk.Lines, DiffLine{"-", oldLines[oldIdx]})
				currentHunk.OldCount++
				oldIdx++
			} else if newIdx < len(newLines) && (lcsIdx >= len(lcs) || newLines[newIdx] != lcs[lcsIdx]) {
				// Addition to new
				currentHunk.Lines = append(currentHunk.Lines, DiffLine{"+", newLines[newIdx]})
				currentHunk.NewCount++
				newIdx++
			}
		}

		// Check if we should close the current hunk
		if currentHunk != nil && lcsIdx < len(lcs) {
			// Look ahead to see if we have enough context lines coming
			contextCount := 0
			for i := 0; i < contextLines*2 && oldIdx+i < len(oldLines) && newIdx+i < len(newLines); i++ {
				if lcsIdx+i < len(lcs) &&
					oldLines[oldIdx+i] == lcs[lcsIdx+i] &&
					newLines[newIdx+i] == lcs[lcsIdx+i] {
					contextCount++
				} else {
					break
				}
			}

			// If we have enough unchanged lines, close the hunk
			if contextCount >= contextLines*2 {
				// Add trailing context
				for i := 0; i < contextLines && oldIdx < len(oldLines); i++ {
					if lcsIdx < len(lcs) && oldLines[oldIdx] == lcs[lcsIdx] && newLines[newIdx] == lcs[lcsIdx] {
						currentHunk.Lines = append(currentHunk.Lines, DiffLine{" ", oldLines[oldIdx]})
						currentHunk.OldCount++
						currentHunk.NewCount++
						oldIdx++
						newIdx++
						lcsIdx++
					}
				}
				hunks = append(hunks, *currentHunk)
				currentHunk = nil
			}
		}
	}

	// Close final hunk if open
	if currentHunk != nil {
		hunks = append(hunks, *currentHunk)
	}

	// Handle case where entire file is new or deleted
	if len(hunks) == 0 {
		if len(newLines) > 0 && len(oldLines) == 0 {
			// Entire file is new
			hunk := DiffHunk{
				OldStart: 0,
				OldCount: 0,
				NewStart: 0,
				NewCount: len(newLines),
				Lines:    make([]DiffLine, len(newLines)),
			}
			for i, line := range newLines {
				hunk.Lines[i] = DiffLine{"+", line}
			}
			hunks = append(hunks, hunk)
		} else if len(oldLines) > 0 && len(newLines) == 0 {
			// Entire file deleted
			hunk := DiffHunk{
				OldStart: 0,
				OldCount: len(oldLines),
				NewStart: 0,
				NewCount: 0,
				Lines:    make([]DiffLine, len(oldLines)),
			}
			for i, line := range oldLines {
				hunk.Lines[i] = DiffLine{"-", line}
			}
			hunks = append(hunks, hunk)
		}
	}

	return hunks
}

// computeLCS computes the Longest Common Subsequence of two string slices
func computeLCS(a, b []string) []string {
	m, n := len(a), len(b)
	if m == 0 || n == 0 {
		return []string{}
	}

	// DP table
	dp := make([][]int, m+1)
	for i := range dp {
		dp[i] = make([]int, n+1)
	}

	// Fill DP table
	for i := 1; i <= m; i++ {
		for j := 1; j <= n; j++ {
			if a[i-1] == b[j-1] {
				dp[i][j] = dp[i-1][j-1] + 1
			} else {
				dp[i][j] = max(dp[i-1][j], dp[i][j-1])
			}
		}
	}

	// Backtrack to find LCS
	lcs := make([]string, 0, dp[m][n])
	i, j := m, n
	for i > 0 && j > 0 {
		if a[i-1] == b[j-1] {
			lcs = append([]string{a[i-1]}, lcs...)
			i--
			j--
		} else if dp[i-1][j] > dp[i][j-1] {
			i--
		} else {
			j--
		}
	}

	return lcs
}

// SimpleDiff generates a simple line-by-line diff without context.
// Useful for quick comparisons where full unified diff is overkill.
func SimpleDiff(oldContent, newContent string) (additions []string, removals []string) {
	oldLines := splitLines(oldContent)
	newLines := splitLines(newContent)

	oldSet := make(map[string]int)
	newSet := make(map[string]int)

	for _, line := range oldLines {
		oldSet[line]++
	}
	for _, line := range newLines {
		newSet[line]++
	}

	for _, line := range oldLines {
		if newSet[line] == 0 {
			removals = append(removals, line)
		} else {
			newSet[line]--
		}
	}

	for _, line := range newLines {
		if oldSet[line] == 0 {
			additions = append(additions, line)
		} else {
			oldSet[line]--
		}
	}

	return additions, removals
}
