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

// StatusView renders the repository status.
type StatusView struct {
	theme  theme.Theme
	width  int
	height int
	status *gitops.RepoStatus
}

// NewStatusView creates a new status view.
func NewStatusView(th theme.Theme) *StatusView {
	return &StatusView{
		theme: th,
	}
}

// SetSize sets the dimensions of the status view.
func (s *StatusView) SetSize(width, height int) {
	s.width = width
	s.height = height
}

// SetStatus sets the repository status to display.
func (s *StatusView) SetStatus(status *gitops.RepoStatus) {
	s.status = status
}

// View renders the status view.
func (s *StatusView) View() string {
	if s.status == nil {
		return s.renderLoading()
	}

	var sections []string

	sections = append(sections, s.renderHeader())
	sections = append(sections, s.renderBranchInfo())
	sections = append(sections, s.renderSyncStatus())
	sections = append(sections, s.renderChangeSummary())
	sections = append(sections, s.renderActions())

	return strings.Join(sections, "\n\n")
}

// renderLoading renders a loading state.
func (s *StatusView) renderLoading() string {
	style := lipgloss.NewStyle().
		Foreground(lipgloss.Color(s.theme.TextDimColor()))

	return style.Render(i18n.T("chatui.git.status.loading"))
}

// renderHeader renders the section header.
func (s *StatusView) renderHeader() string {
	headerStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(s.theme.TextColor())).
		Bold(true)

	return headerStyle.Render(i18n.T("chatui.git.status.title"))
}

// renderBranchInfo renders branch information.
func (s *StatusView) renderBranchInfo() string {
	labelStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(s.theme.TextDimColor()))

	valueStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(s.theme.TextColor()))

	branchStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(s.theme.AccentColor())).
		Bold(true)

	var lines []string

	// Current branch
	branchName := s.status.CurrentBranch
	if s.status.IsDetached {
		branchName = s.status.HeadSHA[:7] + i18n.T("chatui.git.status.detached_head")
	}
	lines = append(lines, labelStyle.Render(i18n.T("chatui.git.status.branch"))+branchStyle.Render(branchName))

	// Remote info
	if s.status.Remote != "" {
		remoteInfo := s.status.Remote
		if s.status.RemoteURL != "" {
			// Shorten long URLs
			url := s.status.RemoteURL
			if len(url) > 40 {
				url = url[:37] + "..."
			}
			remoteInfo = fmt.Sprintf("%s (%s)", s.status.Remote, url)
		}
		lines = append(lines, labelStyle.Render(i18n.T("chatui.git.status.remote"))+valueStyle.Render(remoteInfo))
	}

	// Head SHA
	lines = append(lines, labelStyle.Render(i18n.T("chatui.git.status.head"))+valueStyle.Render(s.status.HeadSHA[:12]))

	return strings.Join(lines, "\n")
}

// renderSyncStatus renders sync status with remote.
func (s *StatusView) renderSyncStatus() string {
	labelStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(s.theme.TextDimColor()))

	successStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(s.theme.SuccessColor()))

	warningStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(s.theme.WarningColor()))

	errorStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(s.theme.ErrorColor()))

	syncStatus := s.status.SyncStatus()
	var statusStyle lipgloss.Style
	var statusIcon string

	switch syncStatus {
	case "up to date":
		statusStyle = successStyle
		statusIcon = "✓"
	case "ahead":
		statusStyle = warningStyle
		statusIcon = "↑"
	case "behind":
		statusStyle = warningStyle
		statusIcon = "↓"
	case "diverged":
		statusStyle = errorStyle
		statusIcon = "⚠"
	default:
		statusStyle = labelStyle
		statusIcon = "?"
	}

	var syncText string
	displayStatus := localizedSyncStatus(syncStatus)
	if s.status.Ahead > 0 && s.status.Behind > 0 {
		syncText = i18n.T("chatui.git.sync.ahead_behind", statusIcon, displayStatus, s.status.Ahead, s.status.Behind)
	} else if s.status.Ahead > 0 {
		syncText = i18n.T("chatui.git.sync.by_commits", statusIcon, displayStatus, s.status.Ahead)
	} else if s.status.Behind > 0 {
		syncText = i18n.T("chatui.git.sync.by_commits", statusIcon, displayStatus, s.status.Behind)
	} else {
		syncText = fmt.Sprintf("%s %s", statusIcon, displayStatus)
	}

	return labelStyle.Render(i18n.T("chatui.git.status.sync")) + statusStyle.Render(syncText)
}

func localizedSyncStatus(status string) string {
	switch status {
	case "up to date":
		return i18n.T("chatui.git.sync.up_to_date")
	case "ahead":
		return i18n.T("chatui.git.sync.ahead")
	case "behind":
		return i18n.T("chatui.git.sync.behind")
	case "diverged":
		return i18n.T("chatui.git.sync.diverged")
	default:
		return status
	}
}

// renderChangeSummary renders a summary of changes.
func (s *StatusView) renderChangeSummary() string {
	headerStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(s.theme.TextColor())).
		Bold(true)

	labelStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(s.theme.TextDimColor()))

	stagedStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(s.theme.SuccessColor()))

	unstagedStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(s.theme.WarningColor()))

	untrackedStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(s.theme.TextMutedColor()))

	var lines []string
	lines = append(lines, headerStyle.Render(i18n.T("chatui.git.status.changes")))

	if !s.status.HasChanges() {
		lines = append(lines, labelStyle.Render(i18n.T("chatui.git.status.no_changes")))
		return strings.Join(lines, "\n")
	}

	// Staged changes
	stagedCount := len(s.status.Staged)
	if stagedCount > 0 {
		stagedText := i18n.T("chatui.git.status.staged", stagedCount)
		lines = append(lines, stagedStyle.Render(stagedText))

		// Show first few staged files
		for i, f := range s.status.Staged {
			if i >= 3 {
				lines = append(lines, stagedStyle.Render(i18n.T("chatui.git.status.more", stagedCount-3)))
				break
			}
			lines = append(lines, stagedStyle.Render(fmt.Sprintf("    %s %s", f.Status.Symbol(), f.Path)))
		}
	}

	// Unstaged changes
	unstagedCount := len(s.status.Unstaged)
	if unstagedCount > 0 {
		unstagedText := i18n.T("chatui.git.status.unstaged", unstagedCount)
		lines = append(lines, unstagedStyle.Render(unstagedText))

		for i, f := range s.status.Unstaged {
			if i >= 3 {
				lines = append(lines, unstagedStyle.Render(i18n.T("chatui.git.status.more", unstagedCount-3)))
				break
			}
			lines = append(lines, unstagedStyle.Render(fmt.Sprintf("    %s %s", f.Status.Symbol(), f.Path)))
		}
	}

	// Untracked files
	untrackedCount := len(s.status.Untracked)
	if untrackedCount > 0 {
		untrackedText := i18n.T("chatui.git.status.untracked", untrackedCount)
		lines = append(lines, untrackedStyle.Render(untrackedText))

		for i, f := range s.status.Untracked {
			if i >= 2 {
				lines = append(lines, untrackedStyle.Render(i18n.T("chatui.git.status.more", untrackedCount-2)))
				break
			}
			lines = append(lines, untrackedStyle.Render(fmt.Sprintf("    ? %s", f.Path)))
		}
	}

	return strings.Join(lines, "\n")
}

// renderActions renders available actions.
func (s *StatusView) renderActions() string {
	headerStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(s.theme.TextColor())).
		Bold(true)

	keyStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(s.theme.AccentColor())).
		Bold(true)

	labelStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(s.theme.TextDimColor()))

	var lines []string
	lines = append(lines, headerStyle.Render(i18n.T("chatui.git.status.quick_actions")))

	// Working Tree
	lines = append(lines, "")
	lines = append(lines, labelStyle.Render(i18n.T("chatui.git.status.working_tree")))
	lines = append(lines, "    "+keyStyle.Render("[a]")+i18n.T("chatui.git.status.stage_unstage_all", keyStyle.Render("[x]")))
	lines = append(lines, "    "+keyStyle.Render("[S]")+i18n.T("chatui.git.status.stash_pop", keyStyle.Render("[P]")))

	// Remote
	lines = append(lines, "")
	lines = append(lines, labelStyle.Render(i18n.T("chatui.git.status.remote_actions")))
	lines = append(lines, "    "+keyStyle.Render("[p]")+i18n.T("chatui.git.status.push_pull", keyStyle.Render("[o]")))
	lines = append(lines, "    "+keyStyle.Render("[f]")+i18n.T("chatui.git.status.fetch"))

	// Commit
	lines = append(lines, "")
	lines = append(lines, labelStyle.Render(i18n.T("chatui.git.status.commit_actions")))
	lines = append(lines, "    "+keyStyle.Render("[c]")+i18n.T("chatui.git.status.commit_generate", keyStyle.Render("[g]")))

	return strings.Join(lines, "\n")
}

// BranchSelector renders a branch selector component.
type BranchSelector struct {
	theme    theme.Theme
	width    int
	height   int
	branches []gitops.BranchInfo
	selected int
	scroll   int
}

// NewBranchSelector creates a new branch selector.
func NewBranchSelector(th theme.Theme) *BranchSelector {
	return &BranchSelector{
		theme: th,
	}
}

// SetSize sets the dimensions.
func (b *BranchSelector) SetSize(width, height int) {
	b.width = width
	b.height = height
}

// SetBranches sets the available branches.
func (b *BranchSelector) SetBranches(branches []gitops.BranchInfo) {
	b.branches = branches
}

// SelectNext selects the next branch.
func (b *BranchSelector) SelectNext() {
	if b.selected < len(b.branches)-1 {
		b.selected++
		// Scroll if needed
		if b.selected >= b.scroll+b.height-2 {
			b.scroll++
		}
	}
}

// SelectPrev selects the previous branch.
func (b *BranchSelector) SelectPrev() {
	if b.selected > 0 {
		b.selected--
		// Scroll if needed
		if b.selected < b.scroll {
			b.scroll--
		}
	}
}

// SelectedBranch returns the currently selected branch.
func (b *BranchSelector) SelectedBranch() *gitops.BranchInfo {
	if b.selected >= 0 && b.selected < len(b.branches) {
		return &b.branches[b.selected]
	}
	return nil
}

// View renders the branch selector.
func (b *BranchSelector) View() string {
	headerStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(b.theme.TextColor())).
		Bold(true)

	selectedStyle := lipgloss.NewStyle().
		Background(lipgloss.Color(b.theme.AccentColor())).
		Foreground(lipgloss.Color(b.theme.TextColor())).
		Bold(true)

	normalStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(b.theme.TextColor()))

	currentStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(b.theme.SuccessColor()))

	remoteStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(b.theme.TextDimColor()))

	var lines []string
	lines = append(lines, headerStyle.Render(i18n.T("chatui.git.branches")))
	lines = append(lines, "")

	visibleCount := max(b.height-3, 1)

	end := min(b.scroll+visibleCount, len(b.branches))

	for i := b.scroll; i < end; i++ {
		branch := b.branches[i]

		icon := "  "
		if branch.IsCurrent {
			icon = "● "
		}

		name := branch.Name
		style := normalStyle
		if branch.IsRemote {
			style = remoteStyle
		}
		if branch.IsCurrent {
			style = currentStyle
		}

		line := icon + name
		if i == b.selected {
			line = selectedStyle.Render("▶ " + name)
		} else {
			line = style.Render(line)
		}

		lines = append(lines, line)
	}

	return strings.Join(lines, "\n")
}

// CommitInput renders a commit message input component.
type CommitInput struct {
	theme   theme.Theme
	width   int
	height  int
	subject string
	body    string
	focused bool
	cursor  int
}

// NewCommitInput creates a new commit input.
func NewCommitInput(th theme.Theme) *CommitInput {
	return &CommitInput{
		theme: th,
	}
}

// SetSize sets the dimensions.
func (c *CommitInput) SetSize(width, height int) {
	c.width = width
	c.height = height
}

// Focus sets the focus state.
func (c *CommitInput) Focus() {
	c.focused = true
}

// Blur removes focus.
func (c *CommitInput) Blur() {
	c.focused = false
}

// IsFocused returns the focus state.
func (c *CommitInput) IsFocused() bool {
	return c.focused
}

// SetSubject sets the commit subject.
func (c *CommitInput) SetSubject(subject string) {
	c.subject = subject
}

// SetBody sets the commit body.
func (c *CommitInput) SetBody(body string) {
	c.body = body
}

// Subject returns the commit subject.
func (c *CommitInput) Subject() string {
	return c.subject
}

// Body returns the commit body.
func (c *CommitInput) Body() string {
	return c.body
}

// Clear clears the input.
func (c *CommitInput) Clear() {
	c.subject = ""
	c.body = ""
	c.cursor = 0
}

// InsertChar inserts a character at the cursor.
func (c *CommitInput) InsertChar(ch rune) {
	c.subject = c.subject[:c.cursor] + string(ch) + c.subject[c.cursor:]
	c.cursor++
}

// DeleteChar deletes the character before the cursor.
func (c *CommitInput) DeleteChar() {
	if c.cursor > 0 {
		c.subject = c.subject[:c.cursor-1] + c.subject[c.cursor:]
		c.cursor--
	}
}

// View renders the commit input.
func (c *CommitInput) View() string {
	headerStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(c.theme.TextColor())).
		Bold(true)

	labelStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(c.theme.TextDimColor()))

	inputStyle := lipgloss.NewStyle().
		Width(c.width-4).
		Background(lipgloss.Color(c.theme.BackgroundLightColor())).
		Foreground(lipgloss.Color(c.theme.TextColor())).
		Padding(0, 1)

	focusedStyle := inputStyle.
		BorderStyle(lipgloss.NormalBorder()).
		BorderForeground(lipgloss.Color(c.theme.AccentColor()))

	var lines []string
	lines = append(lines, headerStyle.Render(i18n.T("chatui.git.commit.title")))
	lines = append(lines, "")

	// Subject line
	lines = append(lines, labelStyle.Render(i18n.T("chatui.git.commit.subject")))
	subjectDisplay := c.subject
	if c.focused {
		subjectDisplay = c.subject[:c.cursor] + "█" + c.subject[c.cursor:]
		lines = append(lines, focusedStyle.Render(subjectDisplay))
	} else {
		if subjectDisplay == "" {
			subjectDisplay = i18n.T("chatui.git.commit.subject_placeholder")
		}
		lines = append(lines, inputStyle.Render(subjectDisplay))
	}

	lines = append(lines, "")

	// Body
	lines = append(lines, labelStyle.Render(i18n.T("chatui.git.commit.body")))
	bodyDisplay := c.body
	if bodyDisplay == "" {
		bodyDisplay = i18n.T("chatui.git.commit.body_placeholder")
	}
	lines = append(lines, inputStyle.Render(bodyDisplay))

	lines = append(lines, "")

	// Help text
	if c.focused {
		lines = append(lines, labelStyle.Render(i18n.T("chatui.git.commit.focused_hint")))
	} else {
		lines = append(lines, labelStyle.Render(i18n.T("chatui.git.commit.start_hint")))
	}

	return strings.Join(lines, "\n")
}
