// Package gitpanel provides Bubbletea TUI components for Git operations.
package gitpanel

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/chatui/theme"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/gitops"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/i18n"
)

// DiffViewer renders git diffs with syntax highlighting.
type DiffViewer struct {
	theme  theme.Theme
	width  int
	height int

	// Diff data
	filePath string
	diff     *gitops.FileDiff
	hunks    []gitops.Hunk

	// Scroll state
	scrollOffset int
	selectedHunk int
	showLineNums bool
}

// NewDiffViewer creates a new diff viewer.
func NewDiffViewer(th theme.Theme) *DiffViewer {
	return &DiffViewer{
		theme:        th,
		showLineNums: true,
	}
}

// SetSize sets the dimensions of the diff viewer.
func (d *DiffViewer) SetSize(width, height int) {
	d.width = width
	d.height = height
}

// SetDiff sets the diff to display.
func (d *DiffViewer) SetDiff(path string, diff *gitops.FileDiff) {
	d.filePath = path
	d.diff = diff
	if diff != nil {
		d.hunks = diff.Hunks
	} else {
		d.hunks = nil
	}
	d.scrollOffset = 0
	d.selectedHunk = 0
}

// SetHunks sets the hunks to display.
func (d *DiffViewer) SetHunks(path string, hunks []gitops.Hunk) {
	d.filePath = path
	d.hunks = hunks
	d.scrollOffset = 0
	d.selectedHunk = 0
}

// ScrollUp scrolls the view up.
func (d *DiffViewer) ScrollUp(lines int) {
	d.scrollOffset -= lines
	if d.scrollOffset < 0 {
		d.scrollOffset = 0
	}
}

// ScrollDown scrolls the view down.
func (d *DiffViewer) ScrollDown(lines int) {
	d.scrollOffset += lines
	maxScroll := max(d.totalLines()-d.height+4, 0)
	if d.scrollOffset > maxScroll {
		d.scrollOffset = maxScroll
	}
}

// NextHunk moves to the next hunk.
func (d *DiffViewer) NextHunk() {
	if d.selectedHunk < len(d.hunks)-1 {
		d.selectedHunk++
		d.scrollToHunk(d.selectedHunk)
	}
}

// PrevHunk moves to the previous hunk.
func (d *DiffViewer) PrevHunk() {
	if d.selectedHunk > 0 {
		d.selectedHunk--
		d.scrollToHunk(d.selectedHunk)
	}
}

// scrollToHunk scrolls to show the specified hunk.
func (d *DiffViewer) scrollToHunk(hunkIdx int) {
	if hunkIdx < 0 || hunkIdx >= len(d.hunks) {
		return
	}

	// Calculate line offset for this hunk
	lineOffset := 0
	for i := range hunkIdx {
		lineOffset += len(d.hunks[i].Lines) + 2 // +2 for header and spacing
	}

	d.scrollOffset = lineOffset
}

// totalLines returns the total number of lines in the diff.
func (d *DiffViewer) totalLines() int {
	total := 0
	for _, hunk := range d.hunks {
		total += len(hunk.Lines) + 2 // +2 for header and spacing
	}
	return total
}

// SelectedHunk returns the currently selected hunk index.
func (d *DiffViewer) SelectedHunk() int {
	return d.selectedHunk
}

// View renders the diff viewer.
func (d *DiffViewer) View() string {
	if d.diff == nil && len(d.hunks) == 0 {
		return d.renderEmpty()
	}

	if d.diff != nil && d.diff.Binary {
		return d.renderBinary()
	}

	return d.renderDiff()
}

// renderEmpty renders an empty state.
func (d *DiffViewer) renderEmpty() string {
	style := lipgloss.NewStyle().
		Width(d.width).
		Height(d.height).
		Foreground(lipgloss.Color(d.theme.TextDimColor())).
		Align(lipgloss.Center, lipgloss.Center)

	return style.Render(i18n.T("chatui.git.diff.empty"))
}

// renderBinary renders a binary file indicator.
func (d *DiffViewer) renderBinary() string {
	style := lipgloss.NewStyle().
		Width(d.width).
		Height(d.height).
		Foreground(lipgloss.Color(d.theme.WarningColor())).
		Align(lipgloss.Center, lipgloss.Center)

	return style.Render(i18n.T("chatui.git.diff.binary"))
}

// renderDiff renders the diff content.
func (d *DiffViewer) renderDiff() string {
	var lines []string

	// Header
	headerStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(d.theme.TextColor())).
		Bold(true)

	if d.filePath != "" {
		lines = append(lines, headerStyle.Render(d.filePath))
		lines = append(lines, "")
	}

	// Styles for diff lines
	addStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(d.theme.SuccessColor())).
		Background(lipgloss.Color("#1a2f1a"))

	deleteStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(d.theme.ErrorColor())).
		Background(lipgloss.Color("#2f1a1a"))

	contextStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(d.theme.TextDimColor()))

	hunkHeaderStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(d.theme.AccentColor())).
		Bold(true)

	lineNumStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(d.theme.TextMutedColor())).
		Width(4).
		Align(lipgloss.Right)

	// Render hunks
	for hunkIdx, hunk := range d.hunks {
		// Hunk header
		header := hunk.Header
		if hunkIdx == d.selectedHunk {
			header = "▶ " + header
		} else {
			header = "  " + header
		}
		lines = append(lines, hunkHeaderStyle.Render(header))

		// Parse and render lines
		oldNum := hunk.StartLine
		newNum := hunk.NewStart

		for _, line := range hunk.Lines {
			if len(line) == 0 {
				lines = append(lines, "")
				oldNum++
				newNum++
				continue
			}

			prefix := line[0]
			content := ""
			if len(line) > 1 {
				content = line[1:]
			}

			var renderedLine string
			switch prefix {
			case '+':
				lineNum := ""
				if d.showLineNums {
					lineNum = lineNumStyle.Render(fmt.Sprintf("+%d", newNum))
				}
				renderedLine = lineNum + addStyle.Render("+ "+content)
				newNum++
			case '-':
				lineNum := ""
				if d.showLineNums {
					lineNum = lineNumStyle.Render(fmt.Sprintf("-%d", oldNum))
				}
				renderedLine = lineNum + deleteStyle.Render("- "+content)
				oldNum++
			default:
				lineNum := ""
				if d.showLineNums {
					lineNum = lineNumStyle.Render(fmt.Sprintf(" %d", oldNum))
				}
				renderedLine = lineNum + contextStyle.Render("  "+content)
				oldNum++
				newNum++
			}

			lines = append(lines, renderedLine)
		}

		lines = append(lines, "") // Spacing between hunks
	}

	// Apply scrolling
	visibleLines := max(d.height-2, 1)

	start := d.scrollOffset
	if start >= len(lines) {
		start = len(lines) - 1
	}
	if start < 0 {
		start = 0
	}

	end := min(start+visibleLines, len(lines))

	visible := lines[start:end]

	// Add scroll indicator if needed
	if d.scrollOffset > 0 {
		visible = append([]string{contextStyle.Render(i18n.T("chatui.git.diff.more_above"))}, visible...)
	}
	if end < len(lines) {
		visible = append(visible, contextStyle.Render(i18n.T("chatui.git.diff.more_below")))
	}

	return strings.Join(visible, "\n")
}

// DiffLine represents a parsed diff line for rendering.
type DiffLine struct {
	Type       DiffLineType
	Content    string
	OldLineNum int
	NewLineNum int
}

// DiffLineType represents the type of a diff line.
type DiffLineType int

const (
	DiffLineContext DiffLineType = iota
	DiffLineAdd
	DiffLineDelete
	DiffLineHunkHeader
)

// ParseDiffLines parses diff lines into structured format.
func ParseDiffLines(hunk *gitops.Hunk) []DiffLine {
	var lines []DiffLine
	oldNum := hunk.StartLine
	newNum := hunk.NewStart

	for _, line := range hunk.Lines {
		if len(line) == 0 {
			lines = append(lines, DiffLine{
				Type:       DiffLineContext,
				Content:    "",
				OldLineNum: oldNum,
				NewLineNum: newNum,
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

		switch prefix {
		case '+':
			lines = append(lines, DiffLine{
				Type:       DiffLineAdd,
				Content:    content,
				NewLineNum: newNum,
			})
			newNum++
		case '-':
			lines = append(lines, DiffLine{
				Type:       DiffLineDelete,
				Content:    content,
				OldLineNum: oldNum,
			})
			oldNum++
		default:
			lines = append(lines, DiffLine{
				Type:       DiffLineContext,
				Content:    content,
				OldLineNum: oldNum,
				NewLineNum: newNum,
			})
			oldNum++
			newNum++
		}
	}

	return lines
}

// SideBySideDiffViewer renders diffs in side-by-side format.
type SideBySideDiffViewer struct {
	theme  theme.Theme
	width  int
	height int
	diff   *gitops.FileDiff
}

// NewSideBySideDiffViewer creates a new side-by-side diff viewer.
func NewSideBySideDiffViewer(th theme.Theme) *SideBySideDiffViewer {
	return &SideBySideDiffViewer{
		theme: th,
	}
}

// SetSize sets the dimensions of the viewer.
func (s *SideBySideDiffViewer) SetSize(width, height int) {
	s.width = width
	s.height = height
}

// SetDiff sets the diff to display.
func (s *SideBySideDiffViewer) SetDiff(diff *gitops.FileDiff) {
	s.diff = diff
}

// View renders the side-by-side diff.
func (s *SideBySideDiffViewer) View() string {
	if s.diff == nil || len(s.diff.Hunks) == 0 {
		return ""
	}

	// Split width in half for side-by-side view
	halfWidth := s.width / 2

	deleteStyle := lipgloss.NewStyle().
		Width(halfWidth).
		Foreground(lipgloss.Color(s.theme.ErrorColor())).
		Background(lipgloss.Color("#2f1a1a"))

	addStyle := lipgloss.NewStyle().
		Width(halfWidth).
		Foreground(lipgloss.Color(s.theme.SuccessColor())).
		Background(lipgloss.Color("#1a2f1a"))

	contextStyle := lipgloss.NewStyle().
		Width(halfWidth).
		Foreground(lipgloss.Color(s.theme.TextDimColor()))

	var lines []string

	for _, hunk := range s.diff.Hunks {
		parsed := ParseDiffLines(&hunk)

		// Group consecutive adds/deletes for side-by-side display
		var leftLines []DiffLine
		var rightLines []DiffLine

		for _, line := range parsed {
			switch line.Type {
			case DiffLineDelete:
				leftLines = append(leftLines, line)
			case DiffLineAdd:
				rightLines = append(rightLines, line)
			case DiffLineContext:
				// Flush any pending add/delete pairs
				maxPairs := max(len(leftLines), len(rightLines))
				for i := range maxPairs {
					var left, right string
					if i < len(leftLines) {
						left = deleteStyle.Render(leftLines[i].Content)
					} else {
						left = contextStyle.Render("")
					}
					if i < len(rightLines) {
						right = addStyle.Render(rightLines[i].Content)
					} else {
						right = contextStyle.Render("")
					}
					lines = append(lines, left+right)
				}
				leftLines = nil
				rightLines = nil

				// Add context line
				left := contextStyle.Render(line.Content)
				right := contextStyle.Render(line.Content)
				lines = append(lines, left+right)
			}
		}

		// Flush any remaining pairs
		maxPairs := max(len(leftLines), len(rightLines))
		for i := range maxPairs {
			var left, right string
			if i < len(leftLines) {
				left = deleteStyle.Render(leftLines[i].Content)
			} else {
				left = contextStyle.Render("")
			}
			if i < len(rightLines) {
				right = addStyle.Render(rightLines[i].Content)
			} else {
				right = contextStyle.Render("")
			}
			lines = append(lines, left+right)
		}
	}

	return strings.Join(lines, "\n")
}
