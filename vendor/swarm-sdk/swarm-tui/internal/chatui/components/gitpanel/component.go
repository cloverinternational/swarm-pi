// Package gitpanel provides Bubbletea TUI components for Git operations.
// This is a Go port of the octogit functionality integrated into the Swarm TUI.
package gitpanel

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/chatui/theme"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/gitgraph"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/gitops"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/i18n"
)

// Tab represents a tab in the git panel.
type Tab int

const (
	TabStatus Tab = iota
	TabFiles
	TabUnstaged
	TabStaged
	TabCommit
	TabGraph
	TabHistory
	TabWorktrees
)

// String returns the display name of the tab.
func (t Tab) String() string {
	switch t {
	case TabStatus:
		return i18n.T("chatui.git.tab.status")
	case TabFiles:
		return i18n.T("chatui.git.tab.files")
	case TabUnstaged:
		return i18n.T("chatui.git.tab.unstaged")
	case TabStaged:
		return i18n.T("chatui.git.tab.staged")
	case TabCommit:
		return i18n.T("chatui.git.tab.commit")
	case TabGraph:
		return i18n.T("chatui.git.tab.graph")
	case TabHistory:
		return i18n.T("chatui.git.tab.history")
	case TabWorktrees:
		return i18n.T("chatui.git.tab.worktrees")
	default:
		return i18n.T("chatui.git.tab.unknown")
	}
}

// Model is the Bubbletea model for the git panel.
type Model struct {
	// Theme and dimensions
	theme  theme.Theme
	width  int
	height int

	// Git repository
	repo   *gitops.GitRepo
	status *gitops.RepoStatus

	// Views
	statusView   *StatusView
	diffViewer   *DiffViewer
	graphView    *GraphView
	historyView  *HistoryView
	worktreeView *WorktreeView

	// Git Data
	graph     *gitgraph.CommitGraph
	history   []gitops.CommitInfo
	worktrees []gitops.WorktreeInfo

	// UI State
	activeTab     Tab
	selectedIndex int
	scrollOffset  int
	viewingDiff   bool // Track if we're viewing a diff

	// Tab-specific state
	commitMessage string
	commitBody    string
	inputFocused  bool

	// Error state
	lastError error

	// Loading state
	loading bool
}

// New creates a new git panel model.
func New(th theme.Theme, repoPath string) Model {
	repo, err := gitops.NewGitRepo(repoPath)
	if err != nil {
		return Model{
			theme:     th,
			lastError: err,
		}
	}

	m := Model{
		theme:        th,
		repo:         repo,
		activeTab:    TabStatus,
		statusView:   NewStatusView(th),
		diffViewer:   NewDiffViewer(th),
		graphView:    NewGraphView(th),
		historyView:  NewHistoryView(th),
		worktreeView: NewWorktreeView(th),
	}

	return m
}

// Init initializes the model.
func (m Model) Init() tea.Cmd {
	return m.refreshStatus()
}

// StatusRefreshedMsg is sent when the status has been refreshed.
type StatusRefreshedMsg struct {
	Status    *gitops.RepoStatus
	Graph     *gitgraph.CommitGraph
	History   []gitops.CommitInfo
	Worktrees []gitops.WorktreeInfo
	Error     error
}

// refreshStatus returns a command to refresh the repository status.
func (m Model) refreshStatus() tea.Cmd {
	return func() tea.Msg {
		if m.repo == nil {
			return StatusRefreshedMsg{Error: fmt.Errorf("%s", i18n.T("chatui.git.no_repository_error"))}
		}

		status, err := m.repo.GetStatus()
		if err != nil {
			return StatusRefreshedMsg{Error: err}
		}

		// Fetch history
		history, _ := m.repo.GetCommitHistory(100)

		// Build graph
		graph, _ := gitgraph.BuildGraphFromRepo(m.repo.Path, gitgraph.DefaultFilter())

		// Fetch worktrees
		worktrees, _ := gitops.GetWorktrees(m.repo.Path)

		return StatusRefreshedMsg{
			Status:    status,
			Graph:     graph,
			History:   history,
			Worktrees: worktrees,
		}
	}
}

// OperationCompleteMsg is sent when a git operation completes.
type OperationCompleteMsg struct {
	Operation string
	Error     error
}

// Update handles messages and updates the model.
func (m Model) Update(msg tea.Msg) (Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		return m.handleKeyPress(msg)

	case StatusRefreshedMsg:
		m.loading = false
		if msg.Error != nil {
			m.lastError = msg.Error
		} else {
			m.status = msg.Status
			m.graph = msg.Graph
			m.history = msg.History
			m.lastError = nil

			// Update views
			m.statusView.SetStatus(m.status)
			m.graphView.SetGraph(m.graph)
			m.historyView.SetCommits(m.history)
			m.worktrees = msg.Worktrees
			m.worktreeView.SetWorktrees(m.worktrees)
		}
		return m, nil

	case OperationCompleteMsg:
		m.loading = false
		if msg.Error != nil {
			m.lastError = msg.Error
			return m, nil
		} else {
			m.lastError = nil
			return m, m.refreshStatus()
		}

	case DiffLoadedMsg:
		m.loading = false
		m.diffViewer.SetDiff(msg.Path, msg.Diff)
		m.activeTab = TabUnstaged // We stay on the same tab for now, just show the diff
		// Or we could have a dedicated Diff tab
		return m, nil

	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height

		// Update view sizes
		m.statusView.SetSize(m.width, m.height-4)
		m.diffViewer.SetSize(m.width, m.height-4)
		m.graphView.SetSize(m.width, m.height-4)
		m.historyView.SetSize(m.width, m.height-4)
		m.worktreeView.SetSize(m.width, m.height-4)

		return m, nil
	}

	return m, nil
}

// handleKeyPress handles key press events.
func (m Model) handleKeyPress(msg tea.KeyMsg) (Model, tea.Cmd) {
	// Input mode handling
	if m.inputFocused {
		return m.handleInputKey(msg)
	}

	switch msg.String() {
	// Tab switching
	case "1":
		m.activeTab = TabStatus
		m.selectedIndex = 0
		m.scrollOffset = 0
		m.viewingDiff = false
	case "2":
		m.activeTab = TabFiles
		m.selectedIndex = 0
		m.scrollOffset = 0
		m.viewingDiff = false
	case "3":
		m.activeTab = TabUnstaged
		m.selectedIndex = 0
		m.scrollOffset = 0
		m.viewingDiff = false
	case "4":
		m.activeTab = TabStaged
		m.selectedIndex = 0
		m.scrollOffset = 0
		m.viewingDiff = false
	case "5":
		m.activeTab = TabCommit
		m.selectedIndex = 0
		m.scrollOffset = 0
		m.viewingDiff = false
	case "6":
		m.activeTab = TabGraph
		m.selectedIndex = 0
		m.scrollOffset = 0
		m.viewingDiff = false
	case "7":
		m.activeTab = TabHistory
		m.selectedIndex = 0
		m.scrollOffset = 0
		m.viewingDiff = false
	case "8":
		m.activeTab = TabWorktrees
		m.selectedIndex = 0
		m.scrollOffset = 0
		m.viewingDiff = false

	// Navigation
	case "j", "down":
		if m.viewingDiff && (m.activeTab == TabUnstaged || m.activeTab == TabStaged) {
			// Scroll diff viewer
			m.diffViewer.ScrollDown(1)
		} else if m.activeTab == TabGraph {
			m.graphView.SelectNext()
		} else if m.activeTab == TabHistory {
			m.historyView.SelectNext()
		} else if m.activeTab == TabWorktrees {
			m.worktreeView.SelectNext()
		} else {
			m.selectedIndex++
			m = m.clampSelection()
			m = m.ensureVisible()
		}
	case "k", "up":
		if m.viewingDiff && (m.activeTab == TabUnstaged || m.activeTab == TabStaged) {
			// Scroll diff viewer
			m.diffViewer.ScrollUp(1)
		} else if m.activeTab == TabGraph {
			m.graphView.SelectPrev()
		} else if m.activeTab == TabHistory {
			m.historyView.SelectPrev()
		} else if m.activeTab == TabWorktrees {
			m.worktreeView.SelectPrev()
		} else {
			m.selectedIndex--
			m = m.clampSelection()
			m = m.ensureVisible()
		}
	// Page up/down for faster scrolling
	case "ctrl+d", "pgdown":
		pageSize := (m.height - 4) / 2
		if m.viewingDiff && (m.activeTab == TabUnstaged || m.activeTab == TabStaged) {
			m.diffViewer.ScrollDown(pageSize)
		} else if m.activeTab == TabGraph {
			for i := 0; i < pageSize && m.graphView.selectedIndex < len(m.graphView.commits)-1; i++ {
				m.graphView.SelectNext()
			}
		} else if m.activeTab == TabHistory {
			for i := 0; i < pageSize && m.historyView.selectedIndex < len(m.historyView.commits)-1; i++ {
				m.historyView.SelectNext()
			}
		} else {
			m.selectedIndex += pageSize
			m = m.clampSelection()
			m = m.ensureVisible()
		}

	case "ctrl+u", "pgup":
		pageSize := (m.height - 4) / 2
		if m.viewingDiff && (m.activeTab == TabUnstaged || m.activeTab == TabStaged) {
			m.diffViewer.ScrollUp(pageSize)
		} else if m.activeTab == TabGraph {
			for i := 0; i < pageSize && m.graphView.selectedIndex > 0; i++ {
				m.graphView.SelectPrev()
			}
		} else if m.activeTab == TabHistory {
			for i := 0; i < pageSize && m.historyView.selectedIndex > 0; i++ {
				m.historyView.SelectPrev()
			}
		} else {
			m.selectedIndex -= pageSize
			m = m.clampSelection()
			m = m.ensureVisible()
		}

	case "g", "home":
		if m.viewingDiff && (m.activeTab == TabUnstaged || m.activeTab == TabStaged) {
			m.diffViewer.scrollOffset = 0
		} else if m.activeTab == TabGraph {
			m.graphView.SelectFirst()
		} else if m.activeTab == TabHistory {
			m.historyView.selectedIndex = 0
			m.historyView.scrollOffset = 0
		} else {
			m.selectedIndex = 0
			m.scrollOffset = 0
		}

	case "G", "end":
		if m.viewingDiff && (m.activeTab == TabUnstaged || m.activeTab == TabStaged) {
			maxScroll := max(m.diffViewer.totalLines()-m.height+4, 0)
			m.diffViewer.scrollOffset = maxScroll
		} else if m.activeTab == TabGraph {
			m.graphView.SelectLast()
		} else if m.activeTab == TabHistory {
			m.historyView.selectedIndex = len(m.historyView.commits) - 1
			m.historyView.ensureVisible()
		} else {
			m.selectedIndex = m.maxSelection()
			m = m.ensureVisible()
		}

	// Actions
	case "r":
		m.loading = true
		return m, m.refreshStatus()
	case "s":
		return m.stageSelected()
	case "u":
		return m.unstageSelected()
	case "d":
		return m.viewDiffSelected()
	case "q", "esc":
		// Exit diff view if currently viewing one
		if m.viewingDiff {
			m.viewingDiff = false
			m.diffViewer.SetDiff("", nil)
		}
	case "a":
		return m.stageAll()
	case "x":
		return m.unstageAll()
	case "c":
		m.activeTab = TabCommit
		m.inputFocused = true
	case "enter":
		if m.activeTab == TabCommit && m.commitMessage != "" {
			return m.commit()
		} else if (m.activeTab == TabUnstaged || m.activeTab == TabStaged) && !m.viewingDiff {
			// Enter to view diff
			return m.viewDiffSelected()
		}
	case "p":
		return m.push()
	case "o":
		return m.pull()
	case "f":
		return m.fetch()
	}

	return m, nil
}

func (m Model) viewDiffSelected() (Model, tea.Cmd) {
	if m.repo == nil || m.status == nil {
		return m, nil
	}

	var path string
	var staged bool

	switch m.activeTab {
	case TabUnstaged:
		files := append(m.status.Unstaged, m.status.Untracked...)
		if m.selectedIndex < len(files) {
			path = files[m.selectedIndex].Path
			staged = false
		}
	case TabStaged:
		if m.selectedIndex < len(m.status.Staged) {
			path = m.status.Staged[m.selectedIndex].Path
			staged = true
		}
	}

	if path == "" {
		return m, nil
	}

	m.loading = true
	m.viewingDiff = true
	return m, func() tea.Msg {
		diff, err := m.repo.GetFileDiff(path, staged)
		if err != nil {
			return OperationCompleteMsg{Operation: "diff", Error: err}
		}

		// We'll use a custom message to update the diff viewer
		return DiffLoadedMsg{Path: path, Diff: diff}
	}
}

type DiffLoadedMsg struct {
	Path string
	Diff *gitops.FileDiff
}

// handleInputKey handles key presses in input mode.
func (m Model) handleInputKey(msg tea.KeyMsg) (Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.inputFocused = false
	case "enter":
		if m.commitMessage != "" {
			m.inputFocused = false
			return m.commit()
		}
	case "backspace":
		if len(m.commitMessage) > 0 {
			m.commitMessage = m.commitMessage[:len(m.commitMessage)-1]
		}
	default:
		if len(msg.String()) == 1 {
			m.commitMessage += msg.String()
		}
	}
	return m, nil
}

// clampSelection ensures the selection is within valid bounds.
func (m Model) clampSelection() Model {
	max := m.maxSelection()
	if m.selectedIndex < 0 {
		m.selectedIndex = 0
	}
	if m.selectedIndex > max {
		m.selectedIndex = max
	}
	return m
}

// ensureVisible ensures the selected item is visible by adjusting scroll offset.
func (m Model) ensureVisible() Model {
	// Calculate visible area (subtract header and help text)
	visibleLines := max(m.height-6, 1)

	// Scroll down if selection is below visible area
	if m.selectedIndex >= m.scrollOffset+visibleLines {
		m.scrollOffset = m.selectedIndex - visibleLines + 1
	}

	// Scroll up if selection is above visible area
	if m.selectedIndex < m.scrollOffset {
		m.scrollOffset = m.selectedIndex
	}

	// Ensure scroll offset is valid
	if m.scrollOffset < 0 {
		m.scrollOffset = 0
	}

	return m
}

// maxSelection returns the maximum valid selection index for the current tab.
func (m Model) maxSelection() int {
	if m.status == nil {
		return 0
	}

	switch m.activeTab {
	case TabUnstaged:
		return max(0, len(m.status.Unstaged)+len(m.status.Untracked)-1)
	case TabStaged:
		return max(0, len(m.status.Staged)-1)
	case TabFiles:
		if m.repo != nil {
			files, err := m.repo.GetFileTree()
			if err == nil {
				return max(0, len(files)-1)
			}
		}
		return 0
	default:
		return 0
	}
}

// Git operations

func (m Model) stageSelected() (Model, tea.Cmd) {
	if m.repo == nil || m.status == nil {
		return m, nil
	}

	var path string
	switch m.activeTab {
	case TabUnstaged:
		files := append(m.status.Unstaged, m.status.Untracked...)
		if m.selectedIndex < len(files) {
			path = files[m.selectedIndex].Path
		}
	}

	if path == "" {
		return m, nil
	}

	m.loading = true
	return m, func() tea.Msg {
		err := m.repo.StageFile(path)
		return OperationCompleteMsg{Operation: "stage", Error: err}
	}
}

func (m Model) unstageSelected() (Model, tea.Cmd) {
	if m.repo == nil || m.status == nil {
		return m, nil
	}

	var path string
	if m.activeTab == TabStaged && m.selectedIndex < len(m.status.Staged) {
		path = m.status.Staged[m.selectedIndex].Path
	}

	if path == "" {
		return m, nil
	}

	m.loading = true
	return m, func() tea.Msg {
		err := m.repo.UnstageFile(path)
		return OperationCompleteMsg{Operation: "unstage", Error: err}
	}
}

func (m Model) stageAll() (Model, tea.Cmd) {
	if m.repo == nil {
		return m, nil
	}

	m.loading = true
	return m, func() tea.Msg {
		err := m.repo.StageAll()
		return OperationCompleteMsg{Operation: "stage all", Error: err}
	}
}

func (m Model) unstageAll() (Model, tea.Cmd) {
	if m.repo == nil {
		return m, nil
	}

	m.loading = true
	return m, func() tea.Msg {
		err := m.repo.UnstageAll()
		return OperationCompleteMsg{Operation: "unstage all", Error: err}
	}
}

func (m Model) commit() (Model, tea.Cmd) {
	if m.repo == nil || m.commitMessage == "" {
		return m, nil
	}

	message := m.commitMessage
	body := m.commitBody
	m.commitMessage = ""
	m.commitBody = ""

	m.loading = true
	return m, func() tea.Msg {
		var err error
		if body != "" {
			err = m.repo.CommitWithBody(message, body)
		} else {
			err = m.repo.Commit(message)
		}
		return OperationCompleteMsg{Operation: "commit", Error: err}
	}
}

func (m Model) push() (Model, tea.Cmd) {
	if m.repo == nil {
		return m, nil
	}

	m.loading = true
	return m, func() tea.Msg {
		err := m.repo.Push()
		return OperationCompleteMsg{Operation: "push", Error: err}
	}
}

func (m Model) pull() (Model, tea.Cmd) {
	if m.repo == nil {
		return m, nil
	}

	m.loading = true
	return m, func() tea.Msg {
		err := m.repo.Pull()
		return OperationCompleteMsg{Operation: "pull", Error: err}
	}
}

func (m Model) fetch() (Model, tea.Cmd) {
	if m.repo == nil {
		return m, nil
	}

	m.loading = true
	return m, func() tea.Msg {
		err := m.repo.Fetch()
		return OperationCompleteMsg{Operation: "fetch", Error: err}
	}
}

// View renders the git panel.
func (m Model) View() string {
	bg := m.theme.BackgroundColor()

	if m.repo == nil {
		return m.renderError(i18n.T("chatui.git.not_repository"))
	}

	var content string
	switch m.activeTab {
	case TabStatus:
		content = m.renderStatusTab()
	case TabFiles:
		content = m.renderFilesTab()
	case TabUnstaged:
		if m.viewingDiff && m.diffViewer.filePath != "" {
			content = m.diffViewer.View()
		} else {
			content = m.renderUnstagedTab()
		}
	case TabStaged:
		if m.viewingDiff && m.diffViewer.filePath != "" {
			content = m.diffViewer.View()
		} else {
			content = m.renderStagedTab()
		}
	case TabCommit:
		content = m.renderCommitTab()
	case TabGraph:
		content = m.graphView.View()
	case TabHistory:
		content = m.historyView.View()
	case TabWorktrees:
		content = m.worktreeView.View()
	default:
		content = i18n.T("chatui.git.unknown_tab")
	}

	// Combine tabs bar, content, and status bar
	tabs := m.renderTabs()
	statusBar := m.renderStatusBar()

	// Pad content to fill available height between tabs and status bar
	// tabs = 2 lines (tab line + rule), statusBar = 1 line
	contentH := max(m.height-3, 1)
	contentLines := strings.Count(content, "\n") + 1
	if contentLines < contentH {
		emptyLine := lipgloss.NewStyle().
			Background(lipgloss.Color(bg)).
			Width(m.width).
			Render("")
		for i := contentLines; i < contentH; i++ {
			content += "\n" + emptyLine
		}
	}

	assembled := lipgloss.JoinVertical(lipgloss.Left, tabs, content, statusBar)

	return lipgloss.NewStyle().
		Width(m.width).
		Height(m.height).
		Background(lipgloss.Color(bg)).
		Render(assembled)
}

// renderTabs renders the sub-tab bar for the git panel.
// Uses the same visual language as the main TUI's top bar: muted labels with
// an accented active indicator, separated by a thin rule.
func (m Model) renderTabs() string {
	bg := m.theme.BackgroundColor()
	tabs := []Tab{TabStatus, TabFiles, TabUnstaged, TabStaged, TabCommit, TabGraph, TabHistory, TabWorktrees}

	activeStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(m.theme.PrimaryColor())).
		Background(lipgloss.Color(bg)).
		Bold(true).
		Padding(0, 1)

	inactiveStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(m.theme.TextDimColor())).
		Background(lipgloss.Color(bg)).
		Padding(0, 1)

	var parts []string
	for i, tab := range tabs {
		label := fmt.Sprintf("%d %s", i+1, tab.String())
		if tab == m.activeTab {
			parts = append(parts, activeStyle.Render(label))
		} else {
			parts = append(parts, inactiveStyle.Render(label))
		}
	}

	tabLine := lipgloss.JoinHorizontal(lipgloss.Top, parts...)

	// Thin rule below the tabs (matches main TUI divider style)
	ruleStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(m.theme.BorderColor())).
		Background(lipgloss.Color(bg))
	rule := ruleStyle.Render(strings.Repeat("─", m.width))

	return tabLine + "\n" + rule
}

// renderStatusBar renders the bottom status bar with key-badge hints.
func (m Model) renderStatusBar() string {
	bg := m.theme.BackgroundColor()

	if m.loading {
		style := lipgloss.NewStyle().
			Foreground(lipgloss.Color(m.theme.TextDimColor())).
			Background(lipgloss.Color(bg)).
			Width(m.width)
		return style.Render(i18n.T("chatui.git.loading"))
	}

	if m.lastError != nil {
		errStyle := lipgloss.NewStyle().
			Foreground(lipgloss.Color(m.theme.ErrorColor())).
			Background(lipgloss.Color(bg)).
			Width(m.width)
		return errStyle.Render("  " + i18n.T("chatui.git.error", m.lastError))
	}

	// Key badge helpers (match main TUI keyBadge/hintLabel pattern)
	keyBadge := func(k string) string {
		return lipgloss.NewStyle().
			Foreground(lipgloss.Color(m.theme.BackgroundColor())).
			Background(lipgloss.Color(m.theme.PrimaryColor())).
			Bold(true).
			Padding(0, 1).
			Render(k)
	}
	hintLabel := func(l string) string {
		return lipgloss.NewStyle().
			Foreground(lipgloss.Color(m.theme.TextDimColor())).
			Background(lipgloss.Color(bg)).
			Render(" " + l)
	}

	var hints string
	if m.viewingDiff {
		hints = keyBadge("j/k") + hintLabel(i18n.T("chatui.git.hint.scroll")) + "  " +
			keyBadge("ctrl+d/u") + hintLabel(i18n.T("chatui.git.hint.page")) + "  " +
			keyBadge("g/G") + hintLabel(i18n.T("chatui.git.hint.top_bottom")) + "  " +
			keyBadge("esc") + hintLabel(i18n.T("chatui.git.hint.exit_diff"))
	} else {
		switch m.activeTab {
		case TabGraph, TabHistory:
			hints = keyBadge("j/k") + hintLabel(i18n.T("chatui.git.hint.navigate")) + "  " +
				keyBadge("ctrl+d/u") + hintLabel(i18n.T("chatui.git.hint.page")) + "  " +
				keyBadge("r") + hintLabel(i18n.T("chatui.git.hint.refresh"))
		case TabWorktrees:
			hints = keyBadge("j/k") + hintLabel(i18n.T("chatui.git.hint.navigate")) + "  " +
				keyBadge("r") + hintLabel(i18n.T("chatui.git.hint.refresh"))
		case TabUnstaged, TabStaged:
			hints = keyBadge("j/k") + hintLabel(i18n.T("chatui.git.hint.navigate")) + "  " +
				keyBadge("s") + hintLabel(i18n.T("chatui.git.hint.stage")) + "  " +
				keyBadge("u") + hintLabel(i18n.T("chatui.git.hint.unstage")) + "  " +
				keyBadge("d") + hintLabel(i18n.T("chatui.git.hint.diff")) + "  " +
				keyBadge("c") + hintLabel(i18n.T("chatui.git.hint.commit"))
		case TabCommit:
			hints = keyBadge("enter") + hintLabel(i18n.T("chatui.git.hint.commit")) + "  " +
				keyBadge("esc") + hintLabel(i18n.T("chatui.git.hint.cancel"))
		default:
			hints = keyBadge("r") + hintLabel(i18n.T("chatui.git.hint.refresh")) + "  " +
				keyBadge("s") + hintLabel(i18n.T("chatui.git.hint.stage")) + "  " +
				keyBadge("a") + hintLabel(i18n.T("chatui.git.hint.stage_all")) + "  " +
				keyBadge("c") + hintLabel(i18n.T("chatui.git.hint.commit")) + "  " +
				keyBadge("p") + hintLabel(i18n.T("chatui.git.hint.push")) + "  " +
				keyBadge("o") + hintLabel(i18n.T("chatui.git.hint.pull"))
		}
	}

	return " " + hints
}

// renderStatusTab renders the Status tab content.
func (m Model) renderStatusTab() string {
	bg := m.theme.BackgroundColor()

	if m.status == nil {
		return lipgloss.NewStyle().
			Foreground(lipgloss.Color(m.theme.TextDimColor())).
			Background(lipgloss.Color(bg)).
			Render(i18n.T("chatui.git.loading"))
	}

	// Shared styles with explicit backgrounds (match main TUI pattern)
	sectionHeader := func(title string, color string) string {
		accent := lipgloss.NewStyle().
			Foreground(lipgloss.Color(bg)).
			Background(lipgloss.Color(color)).
			Render("▌")
		label := lipgloss.NewStyle().
			Foreground(lipgloss.Color(color)).
			Background(lipgloss.Color(bg)).
			Bold(true).
			Render(" " + title)
		return accent + label
	}

	labelStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(m.theme.TextDimColor())).
		Background(lipgloss.Color(bg))

	valueStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(m.theme.TextColor())).
		Background(lipgloss.Color(bg)).
		Bold(true)

	successStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(m.theme.SuccessColor())).
		Background(lipgloss.Color(bg))

	warningStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(m.theme.WarningColor())).
		Background(lipgloss.Color(bg))

	var lines []string

	// ── REPOSITORY ──
	lines = append(lines, sectionHeader(i18n.T("chatui.git.repository"), m.theme.PrimaryColor()))

	// Branch
	branchValue := m.status.CurrentBranch
	if m.status.IsDetached {
		branchValue = m.status.HeadSHA[:7] + i18n.T("chatui.git.detached")
	}
	lines = append(lines, labelStyle.Render(i18n.T("chatui.git.branch_lower"))+valueStyle.Render(branchValue))

	// Remote
	if m.status.RemoteURL != "" {
		remote := m.status.RemoteURL
		maxW := m.width - 12
		if maxW > 0 && len(remote) > maxW {
			remote = "..." + remote[len(remote)-maxW+3:]
		}
		lines = append(lines, labelStyle.Render(i18n.T("chatui.git.remote_lower"))+
			lipgloss.NewStyle().Foreground(lipgloss.Color(m.theme.TextDimColor())).Background(lipgloss.Color(bg)).Render(remote))
	}

	// Sync status
	syncStatus := m.status.SyncStatus()
	syncStyle := successStyle
	if syncStatus == "diverged" {
		syncStyle = warningStyle
	}
	syncText := localizedSyncStatus(syncStatus)
	if m.status.Ahead > 0 || m.status.Behind > 0 {
		syncText = fmt.Sprintf("%s (↑%d ↓%d)", syncText, m.status.Ahead, m.status.Behind)
	}
	lines = append(lines, labelStyle.Render(i18n.T("chatui.git.sync_lower"))+syncStyle.Render(syncText))
	lines = append(lines, "")

	// ── CHANGES ──
	lines = append(lines, sectionHeader(i18n.T("chatui.git.changes"), m.theme.AccentColor()))

	stagedCount := len(m.status.Staged)
	unstagedCount := len(m.status.Unstaged)
	untrackedCount := len(m.status.Untracked)

	if stagedCount > 0 {
		lines = append(lines, successStyle.Render(i18n.T("chatui.git.count_staged", stagedCount)))
	}
	if unstagedCount > 0 {
		lines = append(lines, warningStyle.Render(i18n.T("chatui.git.count_modified", unstagedCount)))
	}
	if untrackedCount > 0 {
		lines = append(lines, labelStyle.Render(i18n.T("chatui.git.count_untracked", untrackedCount)))
	}
	if stagedCount == 0 && unstagedCount == 0 && untrackedCount == 0 {
		lines = append(lines, successStyle.Render(i18n.T("chatui.git.working_tree_clean")))
	}
	lines = append(lines, "")

	// ── QUICK ACTIONS ──
	lines = append(lines, sectionHeader(i18n.T("chatui.git.actions"), m.theme.InfoColor()))

	actionKey := func(k, label string) string {
		return lipgloss.NewStyle().
			Foreground(lipgloss.Color(m.theme.PrimaryColor())).
			Background(lipgloss.Color(bg)).
			Bold(true).
			Render(k) +
			labelStyle.Render(" "+label)
	}

	lines = append(lines, "  "+actionKey("a", i18n.T("chatui.git.action_stage_all"))+"    "+actionKey("x", i18n.T("chatui.git.action_unstage_all")))
	lines = append(lines, "  "+actionKey("p", i18n.T("chatui.git.action_push"))+"         "+actionKey("o", i18n.T("chatui.git.action_pull")))
	lines = append(lines, "  "+actionKey("f", i18n.T("chatui.git.action_fetch"))+"        "+actionKey("c", i18n.T("chatui.git.action_commit")))

	return strings.Join(lines, "\n")
}

// renderFilesTab renders the Files tab content.
func (m Model) renderFilesTab() string {
	bg := m.theme.BackgroundColor()

	if m.repo == nil {
		return lipgloss.NewStyle().
			Foreground(lipgloss.Color(m.theme.TextDimColor())).
			Background(lipgloss.Color(bg)).
			Render(i18n.T("chatui.git.no_repository"))
	}

	files, err := m.repo.GetFileTree()
	if err != nil {
		return i18n.T("chatui.git.error", err)
	}

	var lines []string
	headerStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(m.theme.PrimaryColor())).
		Background(lipgloss.Color(bg)).
		Bold(true)

	dimStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(m.theme.TextDimColor())).
		Background(lipgloss.Color(bg))

	countStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(m.theme.TextMutedColor())).
		Background(lipgloss.Color(bg))

	lines = append(lines, headerStyle.Render(i18n.T("chatui.git.files_header"))+countStyle.Render(i18n.T("chatui.git.tracked_count", len(files))))
	lines = append(lines, "")

	// Use content height - account for header (2 lines) and help text
	visibleLines := max(m.height-6, 1)

	start := m.scrollOffset
	end := min(start+visibleLines, len(files))

	// Show scroll indicator at top
	if m.scrollOffset > 0 {
		lines = append(lines, dimStyle.Render(i18n.T("chatui.git.more_above_count", m.scrollOffset)))
	}

	// Render visible files
	for i := start; i < end; i++ {
		lines = append(lines, "  "+files[i])
	}

	// Show scroll indicator at bottom
	if end < len(files) {
		lines = append(lines, dimStyle.Render(i18n.T("chatui.git.more_below_count", len(files)-end)))
	}

	return strings.Join(lines, "\n")
}

// renderUnstagedTab renders the Unstaged tab content.
func (m Model) renderUnstagedTab() string {
	bg := m.theme.BackgroundColor()

	if m.status == nil {
		return lipgloss.NewStyle().
			Foreground(lipgloss.Color(m.theme.TextDimColor())).
			Background(lipgloss.Color(bg)).
			Render("  Loading...")
	}

	var lines []string
	headerStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(m.theme.PrimaryColor())).
		Background(lipgloss.Color(bg)).
		Bold(true)

	selectedStyle := lipgloss.NewStyle().
		Background(lipgloss.Color(m.theme.BackgroundLighterColor())).
		Foreground(lipgloss.Color(m.theme.TextColor()))

	normalStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(m.theme.TextColor())).
		Background(lipgloss.Color(bg))

	modifiedStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(m.theme.WarningColor())).
		Background(lipgloss.Color(bg))

	addedStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(m.theme.SuccessColor())).
		Background(lipgloss.Color(bg))

	dimStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(m.theme.TextDimColor())).
		Background(lipgloss.Color(bg))

	countStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(m.theme.TextMutedColor())).
		Background(lipgloss.Color(bg))

	files := append(m.status.Unstaged, m.status.Untracked...)

	lines = append(lines, headerStyle.Render(i18n.T("chatui.git.unstaged_header"))+countStyle.Render(i18n.T("chatui.git.changes_count", len(files))))
	lines = append(lines, "")

	if len(files) == 0 {
		lines = append(lines, dimStyle.Render(i18n.T("chatui.git.no_unstaged_changes")))
		return strings.Join(lines, "\n")
	}

	// Calculate visible range
	visibleLines := max(m.height-6, 1)

	start := m.scrollOffset
	end := min(start+visibleLines, len(files))

	// Show scroll indicator at top
	if m.scrollOffset > 0 {
		lines = append(lines, dimStyle.Render(fmt.Sprintf("  ↑ %d more above", m.scrollOffset)))
	}

	// Render visible files
	for i := start; i < end; i++ {
		file := files[i]
		symbol := file.Status.Symbol()
		style := normalStyle
		switch file.Status {
		case gitops.FileModified:
			style = modifiedStyle
		case gitops.FileAdded, gitops.FileUntracked:
			style = addedStyle
		}

		prefix := "  "
		if i == m.selectedIndex {
			prefix = lipgloss.NewStyle().
				Foreground(lipgloss.Color(m.theme.PrimaryColor())).
				Background(lipgloss.Color(bg)).
				Bold(true).
				Render("→ ")
			lines = append(lines, selectedStyle.Render(fmt.Sprintf("%s%s %s", prefix, symbol, file.Path)))
		} else {
			lines = append(lines, style.Render(fmt.Sprintf("%s%s %s", prefix, symbol, file.Path)))
		}
	}

	// Show scroll indicator at bottom
	if end < len(files) {
		lines = append(lines, dimStyle.Render(fmt.Sprintf("  ↓ %d more below", len(files)-end)))
	}

	return strings.Join(lines, "\n")
}

// renderStagedTab renders the Staged tab content.
func (m Model) renderStagedTab() string {
	bg := m.theme.BackgroundColor()

	if m.status == nil {
		return lipgloss.NewStyle().
			Foreground(lipgloss.Color(m.theme.TextDimColor())).
			Background(lipgloss.Color(bg)).
			Render("  Loading...")
	}

	var lines []string
	headerStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(m.theme.PrimaryColor())).
		Background(lipgloss.Color(bg)).
		Bold(true)

	selectedStyle := lipgloss.NewStyle().
		Background(lipgloss.Color(m.theme.BackgroundLighterColor())).
		Foreground(lipgloss.Color(m.theme.TextColor()))

	stagedStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(m.theme.SuccessColor())).
		Background(lipgloss.Color(bg))

	dimStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(m.theme.TextDimColor())).
		Background(lipgloss.Color(bg))

	countStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(m.theme.TextMutedColor())).
		Background(lipgloss.Color(bg))

	lines = append(lines, headerStyle.Render(i18n.T("chatui.git.staged_header"))+countStyle.Render(i18n.T("chatui.git.ready_to_commit_count", len(m.status.Staged))))
	lines = append(lines, "")

	if len(m.status.Staged) == 0 {
		lines = append(lines, dimStyle.Render(i18n.T("chatui.git.no_staged_changes")))
		return strings.Join(lines, "\n")
	}

	// Calculate visible range
	visibleLines := max(m.height-6, 1)

	start := m.scrollOffset
	end := min(start+visibleLines, len(m.status.Staged))

	// Show scroll indicator at top
	if m.scrollOffset > 0 {
		lines = append(lines, dimStyle.Render(fmt.Sprintf("  ↑ %d more above", m.scrollOffset)))
	}

	// Render visible files
	for i := start; i < end; i++ {
		file := m.status.Staged[i]
		symbol := file.Status.Symbol()
		if i == m.selectedIndex {
			prefix := lipgloss.NewStyle().
				Foreground(lipgloss.Color(m.theme.PrimaryColor())).
				Background(lipgloss.Color(bg)).
				Bold(true).
				Render("→ ")
			lines = append(lines, selectedStyle.Render(fmt.Sprintf("%s%s %s", prefix, symbol, file.Path)))
		} else {
			lines = append(lines, stagedStyle.Render(fmt.Sprintf("  %s %s", symbol, file.Path)))
		}
	}

	// Show scroll indicator at bottom
	if end < len(m.status.Staged) {
		lines = append(lines, dimStyle.Render(fmt.Sprintf("  ↓ %d more below", len(m.status.Staged)-end)))
	}

	return strings.Join(lines, "\n")
}

// renderCommitTab renders the Commit tab content.
func (m Model) renderCommitTab() string {
	bg := m.theme.BackgroundColor()

	var lines []string
	headerStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(m.theme.PrimaryColor())).
		Background(lipgloss.Color(bg)).
		Bold(true)

	labelStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(m.theme.TextDimColor())).
		Background(lipgloss.Color(bg))

	inputStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(m.theme.TextColor())).
		Background(lipgloss.Color(m.theme.BackgroundLightColor())).
		Width(m.width-4).
		Padding(0, 1)

	lines = append(lines, headerStyle.Render("  COMMIT"))
	lines = append(lines, "")
	lines = append(lines, labelStyle.Render("  Commit Message:"))

	msgDisplay := m.commitMessage
	if m.inputFocused {
		msgDisplay += "█"
	}
	if msgDisplay == "" {
		msgDisplay = "(enter message...)"
	}
	lines = append(lines, inputStyle.Render(msgDisplay))

	lines = append(lines, "")
	lines = append(lines, labelStyle.Render("  Press [Enter] to commit, [Esc] to cancel"))

	// Show staged files count
	if m.status != nil {
		lines = append(lines, "")
		lines = append(lines, labelStyle.Render(fmt.Sprintf("  %d file(s) staged for commit", len(m.status.Staged))))
	}

	return strings.Join(lines, "\n")
}

// renderError renders an error message.
func (m Model) renderError(msg string) string {
	style := lipgloss.NewStyle().
		Foreground(lipgloss.Color(m.theme.ErrorColor())).
		Background(lipgloss.Color(m.theme.BackgroundColor())).
		Padding(1)

	return style.Render("Error: " + msg)
}

// SetSize sets the dimensions of the panel and propagates to subviews.
func (m *Model) SetSize(width, height int) {
	m.width = width
	m.height = height

	// Calculate content height (subtract space for tabs bar, rule, and status bar)
	// tabs = 1 line, rule = 1 line, statusBar = 1 line = 3 lines total
	contentHeight := max(height-3, 1)

	// Propagate size to all subviews
	if m.statusView != nil {
		m.statusView.SetSize(width, contentHeight)
	}
	if m.diffViewer != nil {
		m.diffViewer.SetSize(width, contentHeight)
	}
	if m.graphView != nil {
		m.graphView.SetSize(width, contentHeight)
	}
	if m.historyView != nil {
		m.historyView.SetSize(width, contentHeight)
	}
	if m.worktreeView != nil {
		m.worktreeView.SetSize(width, contentHeight)
	}
}

// GetActiveTab returns the currently active tab.
func (m Model) GetActiveTab() Tab {
	return m.activeTab
}

// SetActiveTab sets the active tab.
func (m *Model) SetActiveTab(tab Tab) {
	m.activeTab = tab
	m.selectedIndex = 0
}
