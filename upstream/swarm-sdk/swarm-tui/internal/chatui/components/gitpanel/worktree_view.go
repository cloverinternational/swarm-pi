package gitpanel

import (
	"fmt"
	"path/filepath"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/chatui/theme"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/gitops"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/i18n"
)

// WorktreeView renders worktree list with status, disk usage, and symlink info.
type WorktreeView struct {
	theme  theme.Theme
	width  int
	height int

	worktrees []gitops.WorktreeInfo
	config    gitops.WorktreeConfig

	selectedIndex int
	scrollOffset  int
}

// NewWorktreeView creates a new worktree view.
func NewWorktreeView(th theme.Theme) *WorktreeView {
	return &WorktreeView{theme: th}
}

// SetSize sets the dimensions.
func (w *WorktreeView) SetSize(width, height int) {
	w.width = width
	w.height = height
}

// SetWorktrees sets the worktree data.
func (w *WorktreeView) SetWorktrees(worktrees []gitops.WorktreeInfo) {
	w.worktrees = worktrees
	w.selectedIndex = 0
	w.scrollOffset = 0
}

// SetConfig sets the worktree config.
func (w *WorktreeView) SetConfig(cfg gitops.WorktreeConfig) {
	w.config = cfg
}

// SelectNext selects the next worktree.
func (w *WorktreeView) SelectNext() {
	if w.selectedIndex < len(w.worktrees)-1 {
		w.selectedIndex++
		w.ensureVisible()
	}
}

// SelectPrev selects the previous worktree.
func (w *WorktreeView) SelectPrev() {
	if w.selectedIndex > 0 {
		w.selectedIndex--
		w.ensureVisible()
	}
}

// SelectedWorktree returns the currently selected worktree.
func (w *WorktreeView) SelectedWorktree() *gitops.WorktreeInfo {
	if w.selectedIndex >= 0 && w.selectedIndex < len(w.worktrees) {
		return &w.worktrees[w.selectedIndex]
	}
	return nil
}

func (w *WorktreeView) ensureVisible() {
	visibleLines := max(w.height-8, 1)
	if w.selectedIndex < w.scrollOffset {
		w.scrollOffset = w.selectedIndex
	}
	if w.selectedIndex >= w.scrollOffset+visibleLines {
		w.scrollOffset = w.selectedIndex - visibleLines + 1
	}
}

// View renders the worktree view.
func (w *WorktreeView) View() string {
	bg := w.theme.BackgroundColor()

	if len(w.worktrees) == 0 {
		return w.renderEmpty()
	}

	var lines []string

	headerStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(w.theme.PrimaryColor())).
		Background(lipgloss.Color(bg)).
		Bold(true)

	countStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(w.theme.TextMutedColor())).
		Background(lipgloss.Color(bg))

	dimStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(w.theme.TextDimColor())).
		Background(lipgloss.Color(bg))

	// Header with count and total disk usage
	var totalDisk int64
	for _, wt := range w.worktrees {
		totalDisk += wt.DiskUsageBytes
	}
	diskStr := ""
	if totalDisk > 0 {
		diskStr = countStyle.Render(i18n.T("chatui.git.worktrees.total", formatBytes(totalDisk)))
	}
	lines = append(lines, headerStyle.Render(i18n.T("chatui.git.worktrees.header", len(w.worktrees)))+diskStr)
	lines = append(lines, "")

	// Worktree list
	visibleLines := max(w.height-8, 1)

	start := w.scrollOffset
	end := min(start+visibleLines, len(w.worktrees))

	if w.scrollOffset > 0 {
		lines = append(lines, dimStyle.Render(i18n.T("chatui.git.more_above_count", w.scrollOffset)))
	}

	for i := start; i < end; i++ {
		wt := w.worktrees[i]
		isSelected := i == w.selectedIndex
		lines = append(lines, w.renderWorktreeLine(wt, isSelected))
	}

	if end < len(w.worktrees) {
		lines = append(lines, dimStyle.Render(i18n.T("chatui.git.more_below_count", len(w.worktrees)-end)))
	}

	// Config section
	lines = append(lines, "")
	lines = append(lines, w.renderConfigSection())

	// Actions
	lines = append(lines, "")
	lines = append(lines, w.renderActions())

	return strings.Join(lines, "\n")
}

func (w *WorktreeView) renderWorktreeLine(wt gitops.WorktreeInfo, selected bool) string {
	bg := w.theme.BackgroundColor()

	selectedStyle := lipgloss.NewStyle().
		Background(lipgloss.Color(w.theme.BackgroundLighterColor())).
		Foreground(lipgloss.Color(w.theme.TextColor()))

	branchStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(w.theme.AccentColor())).
		Background(lipgloss.Color(bg)).
		Bold(true)

	successStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(w.theme.SuccessColor())).
		Background(lipgloss.Color(bg))

	warningStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(w.theme.WarningColor())).
		Background(lipgloss.Color(bg))

	dimStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(w.theme.TextDimColor())).
		Background(lipgloss.Color(bg))

	mutedStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(w.theme.TextMutedColor())).
		Background(lipgloss.Color(bg))

	// Cursor indicator
	prefix := "  "
	if selected {
		prefix = lipgloss.NewStyle().
			Foreground(lipgloss.Color(w.theme.PrimaryColor())).
			Background(lipgloss.Color(bg)).
			Bold(true).
			Render("→ ")
	}

	// Branch
	branch := wt.Branch
	if branch == "" {
		// Only use filepath.Base if path is non-empty to avoid returning "."
		if strings.TrimSpace(wt.Path) != "" {
			branch = filepath.Base(wt.Path)
		}
	}

	// Main indicator
	mainLabel := ""
	if wt.IsMain {
		mainLabel = mutedStyle.Render(i18n.T("chatui.git.worktrees.main"))
	}

	// Dirty status
	var status string
	if wt.IsDirty {
		status = warningStyle.Render(i18n.T("chatui.git.worktrees.dirty"))
	} else {
		status = successStyle.Render(i18n.T("chatui.git.worktrees.clean"))
	}

	// Disk usage
	diskStr := dimStyle.Render(formatBytes(wt.DiskUsageBytes))

	// Truncate path for display
	path := wt.Path
	maxPathLen := 40
	if len(path) > maxPathLen {
		path = "..." + path[len(path)-maxPathLen+3:]
	}

	line := fmt.Sprintf("%s%s%s  %s  %s",
		prefix,
		branchStyle.Render(branch),
		mainLabel,
		status,
		diskStr,
	)

	// Path on same line if room, otherwise hint
	if w.width > 80 {
		line += "  " + dimStyle.Render(path)
	}

	if selected {
		return selectedStyle.Render(line)
	}
	return line
}

func (w *WorktreeView) renderConfigSection() string {
	bg := w.theme.BackgroundColor()

	// Section header matching the sidebar style
	accent := lipgloss.NewStyle().
		Foreground(lipgloss.Color(bg)).
		Background(lipgloss.Color(w.theme.InfoColor())).
		Render("▌")
	label := lipgloss.NewStyle().
		Foreground(lipgloss.Color(w.theme.InfoColor())).
		Background(lipgloss.Color(bg)).
		Bold(true).
		Render(i18n.T("chatui.git.worktrees.config"))

	dimStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(w.theme.TextDimColor())).
		Background(lipgloss.Color(bg))

	valueStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(w.theme.AccentColor())).
		Background(lipgloss.Color(bg))

	var lines []string
	lines = append(lines, accent+label)

	if len(w.config.SymlinkDirectories) == 0 {
		lines = append(lines, dimStyle.Render(i18n.T("chatui.git.worktrees.no_symlinks")))
	} else {
		lines = append(lines, dimStyle.Render(i18n.T("chatui.git.worktrees.symlink"))+
			valueStyle.Render(strings.Join(w.config.SymlinkDirectories, ", ")))
	}

	return strings.Join(lines, "\n")
}

func (w *WorktreeView) renderActions() string {
	bg := w.theme.BackgroundColor()

	keyBadge := func(k string) string {
		return lipgloss.NewStyle().
			Foreground(lipgloss.Color(w.theme.BackgroundColor())).
			Background(lipgloss.Color(w.theme.PrimaryColor())).
			Bold(true).
			Padding(0, 1).
			Render(k)
	}
	hintLabel := func(l string) string {
		return lipgloss.NewStyle().
			Foreground(lipgloss.Color(w.theme.TextDimColor())).
			Background(lipgloss.Color(bg)).
			Render(" " + l)
	}

	return "  " + keyBadge("w") + hintLabel(i18n.T("chatui.git.hint.new")) + "  " +
		keyBadge("W") + hintLabel(i18n.T("chatui.git.hint.remove")) + "  " +
		keyBadge("j/k") + hintLabel(i18n.T("chatui.git.hint.navigate")) + "  " +
		keyBadge("r") + hintLabel(i18n.T("chatui.git.hint.refresh"))
}

func (w *WorktreeView) renderEmpty() string {
	bg := w.theme.BackgroundColor()
	style := lipgloss.NewStyle().
		Foreground(lipgloss.Color(w.theme.TextDimColor())).
		Background(lipgloss.Color(bg))
	return style.Render(i18n.T("chatui.git.worktrees.empty"))
}

// formatBytes formats bytes into human-readable string.
func formatBytes(b int64) string {
	const (
		KB = 1024
		MB = KB * 1024
		GB = MB * 1024
	)
	switch {
	case b >= GB:
		return fmt.Sprintf("%.1f GB", float64(b)/float64(GB))
	case b >= MB:
		return fmt.Sprintf("%.1f MB", float64(b)/float64(MB))
	case b >= KB:
		return fmt.Sprintf("%.1f KB", float64(b)/float64(KB))
	default:
		return fmt.Sprintf("%d B", b)
	}
}
