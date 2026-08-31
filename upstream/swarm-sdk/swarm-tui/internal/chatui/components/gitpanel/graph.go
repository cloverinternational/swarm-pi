// Package gitpanel provides Bubbletea TUI components for Git operations.
package gitpanel

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/chatui/theme"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/gitgraph"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/gitops"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/i18n"
)

// GraphView renders a visual commit graph.
type GraphView struct {
	theme  theme.Theme
	width  int
	height int

	// Graph data
	graph   *gitgraph.CommitGraph
	commits []*gitgraph.CommitNode

	// Selection state
	selectedIndex int
	scrollOffset  int

	// Display options
	showRefs    bool
	showDates   bool
	showAuthors bool
}

// NewGraphView creates a new graph view.
func NewGraphView(th theme.Theme) *GraphView {
	return &GraphView{
		theme:    th,
		showRefs: true,
	}
}

// SetSize sets the dimensions.
func (g *GraphView) SetSize(width, height int) {
	g.width = width
	g.height = height
}

// SetGraph sets the commit graph to display.
func (g *GraphView) SetGraph(graph *gitgraph.CommitGraph) {
	g.graph = graph
	if graph != nil {
		g.commits = graph.GetCommitsInOrder()
	} else {
		g.commits = nil
	}
	g.selectedIndex = 0
	g.scrollOffset = 0
}

// SetShowRefs toggles ref display.
func (g *GraphView) SetShowRefs(show bool) {
	g.showRefs = show
}

// SetShowDates toggles date display.
func (g *GraphView) SetShowDates(show bool) {
	g.showDates = show
}

// SetShowAuthors toggles author display.
func (g *GraphView) SetShowAuthors(show bool) {
	g.showAuthors = show
}

// SelectNext selects the next commit.
func (g *GraphView) SelectNext() {
	if g.selectedIndex < len(g.commits)-1 {
		g.selectedIndex++
		g.ensureVisible()
	}
}

// SelectPrev selects the previous commit.
func (g *GraphView) SelectPrev() {
	if g.selectedIndex > 0 {
		g.selectedIndex--
		g.ensureVisible()
	}
}

// SelectFirst selects the first commit.
func (g *GraphView) SelectFirst() {
	g.selectedIndex = 0
	g.scrollOffset = 0
}

// SelectLast selects the last commit.
func (g *GraphView) SelectLast() {
	g.selectedIndex = len(g.commits) - 1
	g.ensureVisible()
}

// ensureVisible ensures the selected commit is visible.
func (g *GraphView) ensureVisible() {
	visibleLines := max(
		// Account for header lines
		g.height-2, 1)

	if g.selectedIndex < g.scrollOffset {
		g.scrollOffset = g.selectedIndex
	}
	if g.selectedIndex >= g.scrollOffset+visibleLines {
		g.scrollOffset = g.selectedIndex - visibleLines + 1
	}
}

// SelectedCommit returns the currently selected commit.
func (g *GraphView) SelectedCommit() *gitgraph.CommitNode {
	if g.selectedIndex >= 0 && g.selectedIndex < len(g.commits) {
		return g.commits[g.selectedIndex]
	}
	return nil
}

// View renders the graph view.
func (g *GraphView) View() string {
	if g.graph == nil || len(g.commits) == 0 {
		return g.renderEmpty()
	}

	return g.renderGraph()
}

// renderEmpty renders an empty state.
func (g *GraphView) renderEmpty() string {
	style := lipgloss.NewStyle().
		Width(g.width).
		Height(g.height).
		Foreground(lipgloss.Color(g.theme.TextDimColor())).
		Align(lipgloss.Center, lipgloss.Center)

	return style.Render(i18n.T("chatui.git.graph.empty"))
}

// renderGraph renders the commit graph.
func (g *GraphView) renderGraph() string {
	var lines []string

	headerStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(g.theme.TextColor())).
		Bold(true)

	lines = append(lines, headerStyle.Render(i18n.T("chatui.git.graph.title")))
	lines = append(lines, "")

	// Calculate visible range - use full height since we receive content height
	visibleLines := max(
		// Just subtract header lines
		g.height-2, 1)

	start := g.scrollOffset
	end := min(start+visibleLines, len(g.commits))

	// Render visible commits
	for i := start; i < end; i++ {
		commit := g.commits[i]
		isSelected := i == g.selectedIndex
		line := g.renderCommitLine(commit, isSelected)
		lines = append(lines, line)
	}

	// Scroll indicators
	dimStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(g.theme.TextDimColor()))

	if g.scrollOffset > 0 {
		lines = append([]string{dimStyle.Render(i18n.T("chatui.git.graph.scroll_up"))}, lines[1:]...)
	}
	if end < len(g.commits) {
		lines = append(lines, dimStyle.Render(i18n.T("chatui.git.graph.scroll_down")))
	}

	return strings.Join(lines, "\n")
}

// renderCommitLine renders a single commit line with graph.
func (g *GraphView) renderCommitLine(commit *gitgraph.CommitNode, selected bool) string {
	var parts []string

	// Graph visualization
	graphPart := g.renderGraphPart(commit)
	parts = append(parts, graphPart)

	// Commit info
	infoPart := g.renderCommitInfo(commit, selected)
	parts = append(parts, infoPart)

	line := strings.Join(parts, " ")

	if selected {
		selectedStyle := lipgloss.NewStyle().
			Background(lipgloss.Color(g.theme.BackgroundLightColor())).
			Foreground(lipgloss.Color(g.theme.TextColor()))
		return selectedStyle.Render(line)
	}

	return line
}

// renderGraphPart renders the graph visualization part.
func (g *GraphView) renderGraphPart(commit *gitgraph.CommitNode) string {
	if g.graph == nil {
		return ""
	}

	maxLanes := max(g.graph.MaxLanes, 1)
	if maxLanes > 12 {
		maxLanes = 12 // Allow more lanes for complex trees
	}

	var sb strings.Builder

	for lane := 0; lane < maxLanes; lane++ {
		color := g.graph.GetColorForLane(lane)
		style := lipgloss.NewStyle().Foreground(lipgloss.Color(color))

		if lane == commit.Lane {
			// This is the commit's lane
			symbol := "●"
			if commit.IsMerge() {
				symbol = "◉"
			} else if commit.IsInitial() {
				symbol = "◆"
			}

			// Highlight current HEAD differently
			if commit.SHA == g.graph.HeadSHA {
				symbol = "⦿"
			}

			sb.WriteString(style.Render(symbol))
		} else if commit.ActiveLanes[lane] {
			// Active lane passing through
			sb.WriteString(style.Render("│"))
		} else {
			// Check for horizontal merge lines
			isMergeLine := false
			for _, sourceLane := range commit.MergeSourceLanes {
				if (sourceLane > commit.Lane && lane > commit.Lane && lane < sourceLane) ||
					(sourceLane < commit.Lane && lane < commit.Lane && lane > sourceLane) {
					isMergeLine = true
					break
				}
			}

			if isMergeLine {
				sb.WriteString(style.Render("─"))
			} else {
				// Handle merge corners
				isCorner := false
				for _, sourceLane := range commit.MergeSourceLanes {
					if lane == sourceLane {
						if sourceLane > commit.Lane {
							sb.WriteString(style.Render("╮"))
						} else {
							sb.WriteString(style.Render("╭"))
						}
						isCorner = true
						break
					}
				}
				if !isCorner {
					sb.WriteString(" ")
				}
			}
		}
		sb.WriteString(" ")
	}

	return sb.String()
}

// renderCommitInfo renders the commit information part.
func (g *GraphView) renderCommitInfo(commit *gitgraph.CommitNode, selected bool) string {
	shaStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(g.theme.AccentColor()))

	msgStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(g.theme.TextColor()))

	branchStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(g.theme.SuccessColor())).
		Bold(true)

	tagStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(g.theme.WarningColor())).
		Bold(true)

	remoteStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(g.theme.TextDimColor()))

	dateStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(g.theme.TextMutedColor()))

	authorStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(g.theme.PrimaryColor()))

	var parts []string

	// SHA
	parts = append(parts, shaStyle.Render(commit.ShortSHA))

	// Refs (Branches/Tags)
	if g.showRefs && len(commit.Refs) > 0 {
		var refParts []string
		for _, ref := range commit.Refs {
			display := ref.DisplayName()
			switch ref.Type {
			case gitgraph.RefTag:
				refParts = append(refParts, tagStyle.Render(display))
			case gitgraph.RefBranch:
				refParts = append(refParts, branchStyle.Render(display))
			case gitgraph.RefRemoteBranch:
				refParts = append(refParts, remoteStyle.Render(display))
			case gitgraph.RefHead:
				refParts = append(refParts, branchStyle.Underline(true).Render(display))
			}
		}
		if len(refParts) > 0 {
			parts = append(parts, "("+strings.Join(refParts, ", ")+")")
		}
	}

	// Message
	parts = append(parts, msgStyle.Render(commit.ShortMessage(60)))

	// Author (Compact)
	if g.showAuthors {
		author := commit.Author
		if idx := strings.Index(author, "<"); idx > 0 {
			author = strings.TrimSpace(author[:idx])
		}
		parts = append(parts, authorStyle.Render("["+author+"]"))
	}

	// Date
	if g.showDates {
		dateStr := commit.Date.Format("2006-01-02")
		parts = append(parts, dateStyle.Render(dateStr))
	}

	return strings.Join(parts, " ")
}

// HistoryView renders a simple commit history list.
type HistoryView struct {
	theme   theme.Theme
	width   int
	height  int
	commits []gitops.CommitInfo

	selectedIndex int
	scrollOffset  int
}

// NewHistoryView creates a new history view.
func NewHistoryView(th theme.Theme) *HistoryView {
	return &HistoryView{
		theme: th,
	}
}

// SetSize sets the dimensions.
func (h *HistoryView) SetSize(width, height int) {
	h.width = width
	h.height = height
}

// SetCommits sets the commits to display.
func (h *HistoryView) SetCommits(commits []gitops.CommitInfo) {
	h.commits = commits
	h.selectedIndex = 0
	h.scrollOffset = 0
}

// SelectNext selects the next commit.
func (h *HistoryView) SelectNext() {
	if h.selectedIndex < len(h.commits)-1 {
		h.selectedIndex++
		h.ensureVisible()
	}
}

// SelectPrev selects the previous commit.
func (h *HistoryView) SelectPrev() {
	if h.selectedIndex > 0 {
		h.selectedIndex--
		h.ensureVisible()
	}
}

// ensureVisible ensures the selected commit is visible.
func (h *HistoryView) ensureVisible() {
	visibleLines := max(
		// Account for header lines
		h.height-2, 1)

	if h.selectedIndex < h.scrollOffset {
		h.scrollOffset = h.selectedIndex
	}
	if h.selectedIndex >= h.scrollOffset+visibleLines {
		h.scrollOffset = h.selectedIndex - visibleLines + 1
	}
}

// SelectedCommit returns the selected commit.
func (h *HistoryView) SelectedCommit() *gitops.CommitInfo {
	if h.selectedIndex >= 0 && h.selectedIndex < len(h.commits) {
		return &h.commits[h.selectedIndex]
	}
	return nil
}

// View renders the history view.
func (h *HistoryView) View() string {
	if len(h.commits) == 0 {
		return h.renderEmpty()
	}

	var lines []string

	headerStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(h.theme.TextColor())).
		Bold(true)

	lines = append(lines, headerStyle.Render(i18n.T("chatui.git.history.title")))
	lines = append(lines, "")

	// Calculate visible range - use full height since we receive content height
	visibleLines := max(
		// Just subtract header lines
		h.height-2, 1)

	start := h.scrollOffset
	end := min(start+visibleLines, len(h.commits))

	shaStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(h.theme.AccentColor()))

	dateStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(h.theme.TextMutedColor()))

	authorStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(h.theme.PrimaryColor()))

	refStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(h.theme.SuccessColor())).
		Bold(true)

	msgStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(h.theme.TextColor()))

	selectedStyle := lipgloss.NewStyle().
		Background(lipgloss.Color(h.theme.BackgroundLightColor())).
		Foreground(lipgloss.Color(h.theme.TextColor()))

	mergeStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(h.theme.WarningColor()))

	for i := start; i < end; i++ {
		commit := h.commits[i]

		symbol := "○"
		if commit.IsMerge() {
			symbol = mergeStyle.Render("◎")
		}

		dateStr := commit.Date.Format("2006-01-02 15:04")
		sha := shaStyle.Render(commit.ShortSHA)
		date := dateStyle.Render(dateStr)

		// Clean author name
		authorName := commit.Author
		if idx := strings.Index(authorName, "<"); idx > 0 {
			authorName = strings.TrimSpace(authorName[:idx])
		}
		author := authorStyle.Render(authorName)

		msg := msgStyle.Render(commit.ShortMessage(50))

		// Render refs if any
		var refsStr string
		if len(commit.Refs) > 0 {
			refsStr = refStyle.Render(" (" + strings.Join(commit.Refs, ", ") + ")")
		}

		line := fmt.Sprintf("  %s %s %s %s%s %s", symbol, sha, date, author, refsStr, msg)

		if i == h.selectedIndex {
			lines = append(lines, selectedStyle.Render(line))
		} else {
			lines = append(lines, line)
		}
	}

	// Scroll indicators
	dimStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(h.theme.TextDimColor()))

	if h.scrollOffset > 0 {
		lines = append([]string{lines[0], dimStyle.Render(i18n.T("chatui.git.history.more_above"))}, lines[2:]...)
	}
	if end < len(h.commits) {
		lines = append(lines, dimStyle.Render(i18n.T("chatui.git.history.more_commits", len(h.commits)-end)))
	}

	return strings.Join(lines, "\n")
}

// renderEmpty renders an empty state.
func (h *HistoryView) renderEmpty() string {
	style := lipgloss.NewStyle().
		Foreground(lipgloss.Color(h.theme.TextDimColor()))

	return style.Render(i18n.T("chatui.git.graph.empty"))
}
