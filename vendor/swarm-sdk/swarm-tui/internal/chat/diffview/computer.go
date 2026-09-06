package diffview

import (
	"strings"

	"github.com/aymanbagabas/go-udiff"
)

// DiffComputer computes diffs between two text contents
type DiffComputer struct {
	ContextLines int // Number of context lines around changes (default: 3)
}

// NewDiffComputer creates a new diff computer with default settings
func NewDiffComputer() *DiffComputer {
	return &DiffComputer{
		ContextLines: 3,
	}
}

// Compute calculates the diff between before and after content
func (dc *DiffComputer) Compute(beforePath, afterPath, before, after string) *ComputedDiff {
	result := &ComputedDiff{
		BeforePath: beforePath,
		AfterPath:  afterPath,
	}

	// Handle special cases
	if before == after {
		return result // No changes
	}

	// Get edits using udiff's Myers algorithm
	edits := udiff.Strings(before, after)
	if len(edits) == 0 {
		return result // No changes
	}

	// Convert edits to our hunk-based format
	result.Hunks = dc.editsToHunks(before, after, edits)

	// Count additions and deletions
	for _, hunk := range result.Hunks {
		for _, line := range hunk.Lines {
			result.TotalLines++
			switch line.Kind {
			case DiffLineInsert:
				result.Additions++
			case DiffLineDelete:
				result.Deletions++
			}
		}
	}

	return result
}

// editsToHunks converts udiff edits to our DiffHunk format with context
func (dc *DiffComputer) editsToHunks(before, after string, edits []udiff.Edit) []DiffHunk {
	beforeLines := strings.Split(before, "\n")
	afterLines := strings.Split(after, "\n")

	// Convert character-based edits to line-based changes
	changes := dc.editsToLineChanges(before, after, edits)

	if len(changes) == 0 {
		// No line-level changes, but content differs (whitespace only?)
		return nil
	}

	// Group changes into hunks with context
	hunks := []DiffHunk{}
	var currentHunk *DiffHunk

	for _, change := range changes {
		// Calculate context boundaries
		contextStart := max(change.beforeStart-dc.ContextLines, 0)
		contextEnd := min(change.beforeEnd+dc.ContextLines, len(beforeLines))

		// Start new hunk or extend existing one
		if currentHunk == nil || contextStart > currentHunk.BeforeStart+currentHunk.BeforeCount {
			// Start new hunk
			if currentHunk != nil {
				hunks = append(hunks, *currentHunk)
			}
			currentHunk = &DiffHunk{
				BeforeStart: contextStart + 1, // 1-indexed
				AfterStart:  change.afterStart - (change.beforeStart - contextStart) + 1,
				Lines:       []DiffLine{},
			}
			// Add leading context
			for i := contextStart; i < change.beforeStart; i++ {
				if i < len(beforeLines) {
					currentHunk.Lines = append(currentHunk.Lines, DiffLine{
						Kind:      DiffLineEqual,
						BeforeNum: i + 1,
						AfterNum:  currentHunk.AfterStart + len(currentHunk.Lines),
						Content:   beforeLines[i],
					})
				}
			}
		}

		// Add deleted lines
		for i := change.beforeStart; i < change.beforeEnd; i++ {
			if i < len(beforeLines) {
				currentHunk.Lines = append(currentHunk.Lines, DiffLine{
					Kind:      DiffLineDelete,
					BeforeNum: i + 1,
					AfterNum:  0,
					Content:   beforeLines[i],
				})
			}
		}

		// Add inserted lines
		for i := change.afterStart; i < change.afterEnd; i++ {
			if i < len(afterLines) {
				afterLineNum := currentHunk.AfterStart
				// Count non-delete lines to get correct after line number
				for _, l := range currentHunk.Lines {
					if l.Kind != DiffLineDelete {
						afterLineNum++
					}
				}
				currentHunk.Lines = append(currentHunk.Lines, DiffLine{
					Kind:      DiffLineInsert,
					BeforeNum: 0,
					AfterNum:  i + 1,
					Content:   afterLines[i],
				})
			}
		}

		// Add trailing context
		trailingStart := change.beforeEnd
		trailingEnd := contextEnd
		afterOffset := change.afterEnd - change.beforeEnd
		for i := trailingStart; i < trailingEnd; i++ {
			if i < len(beforeLines) {
				beforeNum := i + 1
				// Calculate the after line number accounting for changes
				afterNum := beforeNum + afterOffset
				if afterNum > 0 && afterNum <= len(afterLines) {
					currentHunk.Lines = append(currentHunk.Lines, DiffLine{
						Kind:      DiffLineEqual,
						BeforeNum: beforeNum,
						AfterNum:  afterNum,
						Content:   beforeLines[i],
					})
				}
			}
		}
	}

	// Don't forget the last hunk
	if currentHunk != nil && len(currentHunk.Lines) > 0 {
		// Calculate counts
		for _, line := range currentHunk.Lines {
			if line.Kind != DiffLineInsert {
				currentHunk.BeforeCount++
			}
			if line.Kind != DiffLineDelete {
				currentHunk.AfterCount++
			}
		}
		hunks = append(hunks, *currentHunk)
	}

	return hunks
}

// lineChange represents a change at the line level
type lineChange struct {
	beforeStart int // First line affected in before (0-indexed)
	beforeEnd   int // Last line + 1 in before
	afterStart  int // First line in after (0-indexed)
	afterEnd    int // Last line + 1 in after
}

// editsToLineChanges converts character-based edits to line-based changes
func (dc *DiffComputer) editsToLineChanges(before, after string, edits []udiff.Edit) []lineChange {
	beforeLines := strings.Split(before, "\n")
	afterLines := strings.Split(after, "\n")

	// Simple approach: do a line-level diff directly
	// This is more accurate than trying to map character offsets to lines
	changes := []lineChange{}

	// Use a simple LCS-based approach for line-level diff
	beforeLen := len(beforeLines)
	afterLen := len(afterLines)

	// Build a map of line content to occurrences
	i, j := 0, 0
	for i < beforeLen || j < afterLen {
		if i >= beforeLen {
			// Rest of after is insertions
			changes = append(changes, lineChange{
				beforeStart: i,
				beforeEnd:   i,
				afterStart:  j,
				afterEnd:    afterLen,
			})
			break
		}
		if j >= afterLen {
			// Rest of before is deletions
			changes = append(changes, lineChange{
				beforeStart: i,
				beforeEnd:   beforeLen,
				afterStart:  j,
				afterEnd:    j,
			})
			break
		}

		if beforeLines[i] == afterLines[j] {
			// Equal - advance both
			i++
			j++
			continue
		}

		// Find the extent of the change
		changeStart := lineChange{
			beforeStart: i,
			afterStart:  j,
		}

		// Look ahead to find where they sync up again
		syncFound := false
		for lookAhead := 1; lookAhead < 100 && !syncFound; lookAhead++ {
			// Check if after[j+lookAhead] matches before[i]
			if j+lookAhead < afterLen && i < beforeLen && afterLines[j+lookAhead] == beforeLines[i] {
				// Insert only
				changeStart.beforeEnd = i
				changeStart.afterEnd = j + lookAhead
				syncFound = true
			}
			// Check if before[i+lookAhead] matches after[j]
			if i+lookAhead < beforeLen && j < afterLen && beforeLines[i+lookAhead] == afterLines[j] {
				// Delete only
				changeStart.beforeEnd = i + lookAhead
				changeStart.afterEnd = j
				syncFound = true
			}
			// Check diagonal
			if i+lookAhead < beforeLen && j+lookAhead < afterLen && beforeLines[i+lookAhead] == afterLines[j+lookAhead] {
				// Replace
				changeStart.beforeEnd = i + lookAhead
				changeStart.afterEnd = j + lookAhead
				syncFound = true
			}
		}

		if !syncFound {
			// Couldn't sync, treat rest as changed
			changeStart.beforeEnd = beforeLen
			changeStart.afterEnd = afterLen
		}

		changes = append(changes, changeStart)
		i = changeStart.beforeEnd
		j = changeStart.afterEnd
	}

	return changes
}

// ComputeSimple is a simpler version that shows all old content as deleted
// and all new content as inserted (useful when content is small)
func (dc *DiffComputer) ComputeSimple(beforePath, afterPath, before, after string) *ComputedDiff {
	result := &ComputedDiff{
		BeforePath: beforePath,
		AfterPath:  afterPath,
	}

	if before == after {
		return result
	}

	beforeLines := strings.Split(before, "\n")
	afterLines := strings.Split(after, "\n")

	hunk := DiffHunk{
		BeforeStart: 1,
		AfterStart:  1,
		BeforeCount: len(beforeLines),
		AfterCount:  len(afterLines),
		Lines:       []DiffLine{},
	}

	// Add all before lines as deletions
	for i, line := range beforeLines {
		if before == "" && i == 0 && line == "" {
			continue // Skip empty before
		}
		hunk.Lines = append(hunk.Lines, DiffLine{
			Kind:      DiffLineDelete,
			BeforeNum: i + 1,
			AfterNum:  0,
			Content:   line,
		})
		result.Deletions++
		result.TotalLines++
	}

	// Add all after lines as insertions
	for i, line := range afterLines {
		if after == "" && i == 0 && line == "" {
			continue // Skip empty after
		}
		hunk.Lines = append(hunk.Lines, DiffLine{
			Kind:      DiffLineInsert,
			BeforeNum: 0,
			AfterNum:  i + 1,
			Content:   line,
		})
		result.Additions++
		result.TotalLines++
	}

	if len(hunk.Lines) > 0 {
		result.Hunks = []DiffHunk{hunk}
	}

	return result
}
