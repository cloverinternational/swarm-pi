package commands

import (
	"fmt"
	"path/filepath"
	"strings"
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/gitops"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/i18n"
)

type WorkspaceEntry struct {
	Worktree   gitops.WorktreeInfo
	ActiveTUIs int
	Current    bool
}

type WorkspaceHandoffMsg struct {
	ProjectRoot string
	TargetPath  string
	Branch      string
	Created     bool
	Shared      bool
}

type WorkspaceErrorMsg struct {
	Err error
}

type workspaceLoadedMsg struct {
	binding gitops.WorkspaceBinding
	entries []WorkspaceEntry
	err     error
}

type workspaceCreatedMsg struct {
	binding  gitops.WorkspaceBinding
	worktree gitops.WorktreeInfo
	err      error
}

type WorkspaceCommand struct {
	interactive bool
	loading     bool
	creating    bool
	selected    int
	width       int
	branchInput string
	errorText   string
	binding     gitops.WorkspaceBinding
	entries     []WorkspaceEntry

	resolve     func(string) (gitops.WorkspaceBinding, error)
	list        func(string) ([]gitops.WorktreeInfo, error)
	find        func(string, string) (gitops.WorktreeInfo, error)
	create      func(string, string) (gitops.WorktreeInfo, error)
	activeCount func(string) int
}

func NewWorkspaceCommand() *WorkspaceCommand {
	return &WorkspaceCommand{
		width:       80,
		resolve:     gitops.ResolveWorkspaceBinding,
		list:        gitops.GetWorktrees,
		find:        gitops.FindWorktree,
		create:      gitops.CreateWorkspaceWorktree,
		activeCount: gitops.CountActiveWorkspaceSessions,
	}
}

func (c *WorkspaceCommand) SetActiveCountFunc(fn func(string) int) {
	if fn != nil {
		c.activeCount = fn
	}
}

func (c *WorkspaceCommand) Name() string { return "workspace" }
func (c *WorkspaceCommand) Description() string {
	return i18n.T("commands.workspace.description")
}
func (c *WorkspaceCommand) Aliases() []string { return []string{"worktree", "ws"} }

func (c *WorkspaceCommand) Execute(args []string) tea.Cmd {
	c.errorText = ""
	if len(args) == 0 {
		return c.openPickerCmd()
	}
	switch strings.ToLower(args[0]) {
	case "create", "new":
		if len(args) < 2 {
			c.interactive = true
			c.creating = true
			c.branchInput = ""
			return nil
		}
		return c.createCmd(strings.Join(args[1:], "-"))
	case "join", "use":
		if len(args) < 2 {
			return c.openPickerCmd()
		}
		return c.joinCmd(strings.Join(args[1:], " "))
	default:
		return c.joinCmd(strings.Join(args, " "))
	}
}

func (c *WorkspaceCommand) openPickerCmd() tea.Cmd {
	c.interactive = true
	c.loading = true
	c.creating = false
	c.selected = 0
	c.entries = nil
	return func() tea.Msg {
		binding, err := c.resolve("")
		if err != nil {
			return workspaceLoadedMsg{err: err}
		}
		if !binding.IsGit {
			return workspaceLoadedMsg{binding: binding, err: fmt.Errorf("%s", i18n.T("commands.workspace.not_git_repository", binding.ExecutionRoot))}
		}
		worktrees, err := c.list(binding.ProjectRoot)
		if err != nil {
			return workspaceLoadedMsg{binding: binding, err: err}
		}
		entries := make([]WorkspaceEntry, 0, len(worktrees))
		for _, wt := range worktrees {
			entries = append(entries, WorkspaceEntry{
				Worktree:   wt,
				ActiveTUIs: c.activeCount(wt.Path),
				Current:    samePath(wt.Path, binding.ExecutionRoot),
			})
		}
		return workspaceLoadedMsg{binding: binding, entries: entries}
	}
}

func (c *WorkspaceCommand) joinCmd(selector string) tea.Cmd {
	return func() tea.Msg {
		binding, err := c.resolve("")
		if err != nil {
			return WorkspaceErrorMsg{Err: err}
		}
		wt, err := c.find(binding.ProjectRoot, selector)
		if err != nil {
			return WorkspaceErrorMsg{Err: err}
		}
		return WorkspaceHandoffMsg{
			ProjectRoot: binding.ProjectRoot,
			TargetPath:  wt.Path,
			Branch:      wt.Branch,
			Shared:      c.activeCount(wt.Path) > 0 && !samePath(wt.Path, binding.ExecutionRoot),
		}
	}
}

func (c *WorkspaceCommand) createCmd(branch string) tea.Cmd {
	c.interactive = true
	c.loading = true
	c.errorText = ""
	return func() tea.Msg {
		binding, err := c.resolve("")
		if err != nil {
			return workspaceCreatedMsg{err: err}
		}
		wt, err := c.create(binding.ProjectRoot, branch)
		return workspaceCreatedMsg{binding: binding, worktree: wt, err: err}
	}
}

func (c *WorkspaceCommand) Update(msg tea.Msg) (Command, tea.Cmd) {
	switch msg := msg.(type) {
	case workspaceLoadedMsg:
		c.loading = false
		c.binding = msg.binding
		c.entries = msg.entries
		if msg.err != nil {
			c.errorText = msg.err.Error()
		}
	case workspaceCreatedMsg:
		c.loading = false
		if msg.err != nil {
			c.errorText = msg.err.Error()
			return c, nil
		}
		c.interactive = false
		return c, func() tea.Msg {
			return WorkspaceHandoffMsg{ProjectRoot: msg.binding.ProjectRoot, TargetPath: msg.worktree.Path, Branch: msg.worktree.Branch, Created: true}
		}
	case tea.WindowSizeMsg:
		c.width = msg.Width
	case tea.KeyMsg:
		if !c.interactive || c.loading {
			return c, nil
		}
		if c.creating {
			return c.updateCreate(msg)
		}
		return c.updateList(msg)
	}
	return c, nil
}

func (c *WorkspaceCommand) updateList(msg tea.KeyMsg) (Command, tea.Cmd) {
	last := len(c.entries)
	switch msg.String() {
	case "up", "k":
		if c.selected > 0 {
			c.selected--
		}
	case "down", "j":
		if c.selected < last {
			c.selected++
		}
	case "enter", " ":
		if c.selected == last {
			c.creating = true
			c.branchInput = ""
			c.errorText = ""
			return c, nil
		}
		if c.selected >= 0 && c.selected < len(c.entries) {
			entry := c.entries[c.selected]
			c.interactive = false
			return c, func() tea.Msg {
				return WorkspaceHandoffMsg{
					ProjectRoot: c.binding.ProjectRoot,
					TargetPath:  entry.Worktree.Path,
					Branch:      entry.Worktree.Branch,
					Shared:      entry.ActiveTUIs > 0 && !entry.Current,
				}
			}
		}
	case "esc", "q":
		c.interactive = false
	}
	return c, nil
}

func (c *WorkspaceCommand) updateCreate(msg tea.KeyMsg) (Command, tea.Cmd) {
	switch msg.String() {
	case "esc":
		c.interactive = false
	case "backspace", "ctrl+h":
		if c.branchInput == "" {
			c.creating = false
			return c, nil
		}
		_, size := utf8.DecodeLastRuneInString(c.branchInput)
		c.branchInput = c.branchInput[:len(c.branchInput)-size]
	case "enter":
		branch := strings.TrimSpace(c.branchInput)
		if branch == "" {
			c.errorText = i18n.T("commands.workspace.enter_branch_name")
			return c, nil
		}
		return c, c.createCmd(branch)
	default:
		if text := msg.Key().Text; text != "" {
			c.branchInput += text
		}
	}
	return c, nil
}

func (c *WorkspaceCommand) View() string {
	if !c.interactive {
		return ""
	}
	title := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#6E64E8")).Render(i18n.T("commands.workspace.title"))
	dim := lipgloss.NewStyle().Foreground(lipgloss.Color("#94A3B8"))
	errStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#F87171"))
	lines := []string{title, ""}
	if c.loading {
		lines = append(lines, dim.Render(i18n.T("commands.workspace.loading")))
	} else if c.creating {
		lines = append(lines,
			i18n.T("commands.workspace.create_branch_worktree"), "",
			lipgloss.NewStyle().Foreground(lipgloss.Color("#39D2C0")).Render("> "+c.branchInput+"█"), "",
			dim.Render(i18n.T("commands.workspace.create_hint")),
		)
	} else {
		for i, entry := range c.entries {
			indicator := "  "
			if i == c.selected {
				indicator = "▶ "
			}
			branch := entry.Worktree.Branch
			if branch == "" {
				branch = i18n.T("commands.workspace.detached")
			}
			flags := []string{i18n.T("commands.workspace.clean")}
			if entry.Worktree.IsDirty {
				flags[0] = i18n.T("commands.workspace.dirty")
			}
			if entry.Current {
				flags = append([]string{i18n.T("commands.workspace.current")}, flags...)
			}
			flags = append(flags, i18n.T("commands.workspace.active_tuis", entry.ActiveTUIs))
			row := fmt.Sprintf("%s%-24s  %-22s  %s", indicator, branch, strings.Join(flags, " · "), entry.Worktree.Path)
			if i == c.selected {
				row = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#39D2C0")).Render(row)
			}
			lines = append(lines, row)
		}
		indicator := "  "
		if c.selected == len(c.entries) {
			indicator = "▶ "
		}
		lines = append(lines, indicator+i18n.T("commands.workspace.create_new"), "", dim.Render(i18n.T("commands.workspace.list_hint")))
	}
	if c.errorText != "" {
		lines = append(lines, "", errStyle.Render(c.errorText))
	}
	maxWidth := c.width - 8
	if maxWidth > 110 {
		maxWidth = 110
	}
	if maxWidth < 40 {
		maxWidth = 40
	}
	return lipgloss.NewStyle().Width(maxWidth).Padding(1, 2).Border(lipgloss.RoundedBorder()).BorderForeground(lipgloss.Color("#6E64E8")).Render(strings.Join(lines, "\n"))
}

func (c *WorkspaceCommand) IsInteractive() bool { return c.interactive }

func samePath(a, b string) bool {
	absA, errA := filepath.Abs(a)
	absB, errB := filepath.Abs(b)
	return errA == nil && errB == nil && filepath.Clean(absA) == filepath.Clean(absB)
}
