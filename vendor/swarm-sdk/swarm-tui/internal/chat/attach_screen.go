package chat

import (
	"fmt"
	"sort"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/chat/attach"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/headless/automation/client"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/headless/automation/state"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/ui/palette"
)

type liveSession interface {
	Frames() <-chan *client.Frame
	Errors() <-chan error
	Snapshot() attach.StateSnapshot
	SendKey(string) error
	SendText(string) error
	Detach() error
}

type subagentInterjector interface{ Interject(id, text string) error }

type AttachScreen struct {
	targetsFn    func() ([]attach.Target, error)
	attachPeerFn func(string) (liveSession, error)
	interjector  subagentInterjector

	targets   []attach.Target
	folders   []*attach.FolderNode
	rows      []attach.FolderRow
	selected  int
	sortMode  attach.SortMode
	collapsed map[string]bool
	session   liveSession
	target    *attach.Target
	viewport  *MessageList
	rawView   bool
	width     int
	height    int
	err       error
	snapshot  attach.StateSnapshot
}

func NewAttachScreen(targetsFn func() ([]attach.Target, error), attachPeerFn func(string) (liveSession, error), interjector subagentInterjector) *AttachScreen {
	s := &AttachScreen{
		targetsFn:    targetsFn,
		attachPeerFn: attachPeerFn,
		interjector:  interjector,
		viewport:     NewMessageList(80, 20),
		sortMode:     attach.SortLineage,
		collapsed:    make(map[string]bool),
	}
	s.refreshTargets()
	return s
}

func (s *AttachScreen) refreshTargets() {
	if s.targetsFn == nil {
		return
	}
	t, err := s.targetsFn()
	s.err = err
	if err != nil {
		return
	}
	sort.SliceStable(t, func(i, j int) bool {
		if t[i].Kind != t[j].Kind {
			return t[i].Kind == attach.TargetKindSubAgent
		}
		if t[i].Machine != t[j].Machine {
			return t[i].Machine < t[j].Machine
		}
		if t[i].Name != t[j].Name {
			return t[i].Name < t[j].Name
		}
		return t[i].ID < t[j].ID
	})
	selectedKey := ""
	if s.selected >= 0 && s.selected < len(s.rows) {
		selectedKey = attachRowSelectionKey(s.rows[s.selected])
	}
	s.targets = t
	s.folders = attach.BuildFolders(t, s.collapsed, s.sortMode)
	s.rows = attach.FlattenFolders(s.folders)
	s.selected = 0
	if selectedKey != "" {
		for i := range s.rows {
			if attachRowSelectionKey(s.rows[i]) == selectedKey {
				s.selected = i
				break
			}
		}
	}
}

func (s *AttachScreen) SetSize(width, height int) {
	bottom, percent := true, 1.0
	if s.viewport != nil {
		bottom = s.viewport.AtBottom()
		percent = s.viewport.ScrollPercent()
	}
	s.width, s.height = width, height
	rightWidth := width - s.treeWidth() - 3
	if rightWidth < 20 {
		rightWidth = width
	}
	viewportHeight := height - 5
	if viewportHeight < 0 {
		viewportHeight = 0
	}
	s.viewport.SetSize(rightWidth, viewportHeight)
	if bottom {
		s.viewport.GotoBottom()
	} else {
		s.viewport.SetScrollPercent(percent)
	}
}

func attachRowSelectionKey(row attach.FolderRow) string {
	if row.Target != nil {
		return "target:" + row.Target.ID
	}
	if row.Folder != nil {
		return "folder:" + row.Folder.Key
	}
	return ""
}

func (s *AttachScreen) treeWidth() int {
	if s.width < 70 {
		return s.width
	}
	w := s.width / 3
	if w < 28 {
		return 28
	}
	if w > 42 {
		return 42
	}
	return w
}

func (s *AttachScreen) SetOrigin(x, y int) { s.viewport.SetOrigin(x, y) }
func (s *AttachScreen) Attached() bool     { return s.session != nil }
func (s *AttachScreen) Selected() int      { return s.selected }

func (s *AttachScreen) detach() {
	if s.session != nil {
		_ = s.session.Detach()
		s.session = nil
	}
	s.target = nil
	s.snapshot = attach.StateSnapshot{}
	s.rawView = false
	s.viewport.SetContent("")
}

// Update handles navigation and attach/steer keys.
func (s *AttachScreen) Update(msg tea.KeyMsg) (bool, tea.Cmd) {
	key := msg.String()
	if s.target != nil {
		switch key {
		case "esc", "q":
			s.detach()
			return true, nil
		case "enter":
			return true, nil
		case "tab", "n":
			s.openRelativeTarget(1)
			return true, nil
		case "shift+tab", "p":
			s.openRelativeTarget(-1)
			return true, nil
		case "o":
			if s.session != nil {
				s.rawView = true
				s.readSession()
			}
			return true, nil
		case "up":
			s.selectRelative(-1)
		case "down":
			s.selectRelative(1)
		case "k":
			s.viewport.ScrollUp(1)
		case "j":
			s.viewport.ScrollDown(1)
		case "pgup":
			s.viewport.PageUp()
		case "pgdown":
			s.viewport.PageDown()
		case "home":
			s.viewport.GotoTop()
		case "end":
			s.viewport.GotoBottom()
		default:
			if s.target.Steerable && s.target.Kind == attach.TargetKindSubAgent && s.interjector != nil {
				_ = s.interjector.Interject(s.target.ID, key)
			} else if s.target.Steerable && s.session != nil {
				_ = s.session.SendKey(key)
			}
		}
		return true, nil
	}
	if key == "esc" || key == "q" {
		return false, nil
	}
	if len(s.rows) == 0 {
		s.refreshTargets()
	}
	switch key {
	case "s":
		s.sortMode = nextSortMode(s.sortMode)
		s.refreshTargets()
	case "r":
		s.refreshTargets()
	case " ":
		if s.selected < len(s.rows) {
			row := s.rows[s.selected]
			if row.Folder != nil && row.Target == nil {
				s.collapsed[row.Folder.Key] = row.Folder.Expanded
				s.refreshTargets()
			}
		}
	case "up", "k":
		s.selectRelative(-1)
	case "down", "j":
		s.selectRelative(1)
	case "home":
		s.setSelected(0)
	case "end":
		if len(s.rows) > 0 {
			s.setSelected(len(s.rows) - 1)
		}
	case "tab", "n":
		s.selectRelativeTarget(1)
	case "shift+tab", "p":
		s.selectRelativeTarget(-1)
	case "right":
		if s.selected < len(s.rows) && s.rows[s.selected].Folder != nil {
			s.rows[s.selected].Folder.Expanded = true
			s.collapsed[s.rows[s.selected].Folder.Key] = false
			s.refreshTargets()
		}
	case "left":
		if s.selected < len(s.rows) && s.rows[s.selected].Folder != nil {
			s.rows[s.selected].Folder.Expanded = false
			s.collapsed[s.rows[s.selected].Folder.Key] = true
			s.refreshTargets()
		}
	case "enter":
		if s.selected >= len(s.rows) {
			return true, nil
		}
		row := s.rows[s.selected]
		if row.Target == nil {
			if !row.Folder.Expanded {
				row.Folder.Expanded = true
				s.collapsed[row.Folder.Key] = true
				s.refreshTargets()
				return true, nil
			}
			if len(row.Folder.Targets) == 0 {
				return true, nil
			}
			// Folder headers are navigation anchors rather than attachable
			// sessions. Enter on an expanded folder opens its first session;
			// Space remains the explicit collapse/expand control.
			t := row.Folder.Targets[0]
			s.setSelected(s.findRowByID(t.ID))
			return true, nil
		}
		// Enter is also the explicit retry/open action. When the cursor is
		// already on a target, setSelected intentionally returns early to
		// avoid reopening it during navigation; calling it here would make
		// Enter a no-op after a failed attach and leave the detail pane
		// apparently blank. Open the selected target directly.
		s.previewSelected()
	}
	return true, nil
}

func (s *AttachScreen) findRowByID(id string) int {
	for i, row := range s.rows {
		if row.Target != nil && row.Target.ID == id {
			return i
		}
	}
	return s.selected
}

func (s *AttachScreen) selectRelativeTarget(delta int) {
	if len(s.rows) == 0 {
		return
	}
	start := s.selected
	for n := 0; n < len(s.rows); n++ {
		start += delta
		if start < 0 {
			start = len(s.rows) - 1
		}
		if start >= len(s.rows) {
			start = 0
		}
		if s.rows[start].Target != nil {
			s.setSelected(start)
			return
		}
	}
}

func (s *AttachScreen) selectRelative(delta int) {
	next := s.selected + delta
	if next < 0 {
		next = 0
	}
	if next >= len(s.rows) {
		next = len(s.rows) - 1
	}
	s.setSelected(next)
}

func (s *AttachScreen) setSelected(index int) {
	if len(s.rows) == 0 {
		s.selected = 0
		return
	}
	if index < 0 {
		index = 0
	}
	if index >= len(s.rows) {
		index = len(s.rows) - 1
	}
	if index == s.selected && s.target != nil {
		return
	}
	s.selected = index
	s.previewSelected()
}

func (s *AttachScreen) previewSelected() {
	if s.selected < 0 || s.selected >= len(s.rows) {
		return
	}
	row := s.rows[s.selected]
	if row.Target == nil {
		if s.target != nil {
			s.detach()
		}
		return
	}
	if s.target != nil && s.target.ID == row.Target.ID {
		return
	}
	if s.target != nil {
		s.detach()
	}
	s.openTarget(*row.Target)
}

func (s *AttachScreen) openRelativeTarget(delta int) {
	s.detach()
	s.selectRelativeTarget(delta)
}

func (s *AttachScreen) openTarget(t attach.Target) {
	s.err = nil
	if t.Kind == attach.TargetKindPeer && s.attachPeerFn != nil {
		session, err := s.attachPeerFn(t.ControlSocket)
		if err != nil {
			s.err = err
			return
		}
		s.session, s.target = session, &t
		s.snapshot = session.Snapshot()
		s.updateStructuredViewport()
		s.readSession()
	} else if t.Kind == attach.TargetKindSubAgent {
		s.target = &t
		s.snapshot = t.Snapshot
	}
}

func nextSortMode(mode attach.SortMode) attach.SortMode {
	switch mode {
	case attach.SortLineage:
		return attach.SortAttention
	case attach.SortAttention:
		return attach.SortActivity
	case attach.SortActivity:
		return attach.SortStatus
	default:
		return attach.SortLineage
	}
}

func (s *AttachScreen) Refresh() {
	if s.session != nil {
		s.readSession()
		if s.session != nil {
			s.snapshot = s.session.Snapshot()
			s.updateStructuredViewport()
		}
		return
	}
	s.refreshTargets()
	if s.target != nil {
		for i := range s.rows {
			if s.rows[i].Target != nil && s.rows[i].Target.ID == s.target.ID {
				target := *s.rows[i].Target
				s.target = &target
				s.snapshot = target.Snapshot
				break
			}
		}
	}
}

// updateStructuredViewport renders the inspected conversation state into the
// attach pane. Terminal frames remain an explicit debug/presentation path;
// transcript content comes from the canonical automation state.
func (s *AttachScreen) updateStructuredViewport() {
	if s.rawView {
		return
	}
	bottom, percent := s.viewport.AtBottom(), s.viewport.ScrollPercent()
	s.viewport.SetContent(renderAttachTranscript(s.snapshot.Messages, s.snapshot))
	if bottom {
		s.viewport.GotoBottom()
	} else {
		s.viewport.SetScrollPercent(percent)
	}
}

func (s *AttachScreen) readSession() {
	if s.session == nil {
		return
	}
	for {
		select {
		case f, ok := <-s.session.Frames():
			if !ok {
				return
			}
			if f != nil {
				bottom, percent := s.viewport.AtBottom(), s.viewport.ScrollPercent()
				s.viewport.SetContent(f.Content)
				if bottom {
					s.viewport.GotoBottom()
				} else {
					s.viewport.SetScrollPercent(percent)
				}
			}
		case err, ok := <-s.session.Errors():
			if !ok {
				return
			}
			if err != nil {
				s.err = err
				// A peer can accept the socket and then fail during the first
				// frame/state request (stale PID, protocol mismatch, or the
				// server's connection cap). Release that connection
				// immediately. Keeping it attached leaks one socket per
				// navigation attempt until the peer reaches its hard limit.
				failed := s.session
				s.session = nil
				_ = failed.Detach()
				s.updateStructuredViewport()
				return
			}
		default:
			return
		}
	}
}

func (s *AttachScreen) View() string {
	w := s.width
	if w < 20 {
		w = 80
	}
	b := strings.Builder{}
	title := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color(palette.Accent)).Render("ATTACH")
	subtitle := lipgloss.NewStyle().Foreground(lipgloss.Color(palette.TextDim)).Render("live sessions")
	b.WriteString(title + "  " + subtitle + "\n")
	b.WriteString(s.renderCounters() + "\n\n")

	if len(s.rows) == 0 {
		b.WriteString(s.renderEmptyState())
		if s.err != nil {
			b.WriteString("\n" + statusStyle(attach.StateBadgeFailed).Render("× "+s.err.Error()) + "\n")
		}
		b.WriteString("\n" + helpStyle().Render("↑/↓ navigate  Enter open  r refresh  Esc close"))
		return lipgloss.NewStyle().Width(w).Render(b.String())
	}

	leftWidth := s.treeWidth()
	if leftWidth >= w-20 {
		return lipgloss.NewStyle().Width(w).Render(s.renderCompact(b.String()))
	}
	rightWidth := w - leftWidth - 3
	s.viewport.SetSize(rightWidth, attachMaxInt(s.height-7, 1))

	left := lipgloss.NewStyle().Width(leftWidth).Render(s.renderTree())
	right := lipgloss.NewStyle().Width(rightWidth).Render(s.renderDetail(rightWidth))
	b.WriteString(lipgloss.JoinHorizontal(lipgloss.Top, left, " │ ", right))
	b.WriteString("\n" + lipgloss.NewStyle().Foreground(lipgloss.Color(palette.Border)).Render(strings.Repeat("─", attachMaxInt(w, 1))) + "\n")
	b.WriteString(helpStyle().Render("↑/↓ hover  n/p sessions  j/k scroll  Space collapse  s sort  r refresh  Esc back"))
	return lipgloss.NewStyle().Width(w).Render(b.String())
}

func (s *AttachScreen) renderCounters() string {
	running, questions, approvals, finished, failed, disconnected := 0, 0, 0, 0, 0, 0
	for _, target := range s.targets {
		badge := target.Snapshot.Badge(target.Status)
		switch badge {
		case attach.StateBadgeQuestion:
			questions++
		case attach.StateBadgeApproval, attach.StateBadgePlan:
			approvals++
		case attach.StateBadgeDone:
			finished++
		case attach.StateBadgeFailed:
			failed++
		case attach.StateBadgeStale:
			disconnected++
		case attach.StateBadgeRunning, attach.StateBadgePlanning:
			running++
		}
	}
	parts := []string{
		statusStyle(attach.StateBadgeRunning).Render(fmt.Sprintf("● %d active", running)),
		statusStyle(attach.StateBadgeQuestion).Render(fmt.Sprintf("? %d waiting", questions)),
		statusStyle(attach.StateBadgeApproval).Render(fmt.Sprintf("! %d approval", approvals)),
		statusStyle(attach.StateBadgeDone).Render(fmt.Sprintf("✓ %d done", finished)),
	}
	if failed > 0 {
		parts = append(parts, statusStyle(attach.StateBadgeFailed).Render(fmt.Sprintf("× %d failed", failed)))
	}
	if disconnected > 0 {
		parts = append(parts, statusStyle(attach.StateBadgeStale).Render(fmt.Sprintf("○ %d offline", disconnected)))
	}
	return strings.Join(parts, "  ")
}

func (s *AttachScreen) renderTree() string {
	var b strings.Builder
	b.WriteString(lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color(palette.Text)).Render("WORKSPACES") + "  ")
	b.WriteString(lipgloss.NewStyle().Foreground(lipgloss.Color(palette.TextDim)).Render("sort: "+string(s.sortMode)) + "\n\n")
	for i, row := range s.rows {
		if row.Target == nil {
			folder := row.Folder
			cursor := "  "
			if i == s.selected {
				cursor = "› "
			}
			arrow := "▾"
			if !folder.Expanded {
				arrow = "▸"
			}
			badge := statusStyle(folder.Badge).Render(folderGlyph(folder.Badge))
			label := folder.Name
			if folder.Machine != "" && folder.Machine != "local" {
				label += " · " + folder.Machine
			}
			counts := fmt.Sprintf("%d", folder.Counts.Total)
			line := cursor + arrow + " " + badge + " " + compactTargetName(label, attachMaxInt(s.treeWidth()-12, 12)) +
				" " + lipgloss.NewStyle().Foreground(lipgloss.Color(palette.TextDim)).Render(counts)
			b.WriteString(selectLine(line, i == s.selected) + "\n")
			continue
		}
		target := *row.Target
		cursor := "    "
		if i == s.selected {
			cursor = "  › "
		}
		badge := target.Snapshot.Badge(target.Status)
		label := compactTargetLabel(target)
		label = compactTargetName(label, attachMaxInt(s.treeWidth()-18, 12))
		line := cursor + statusStyle(badge).Render(attachGlyph(target)) + " " + label
		b.WriteString(selectLine(line, i == s.selected) + "\n")
	}
	return b.String()
}

func folderGlyph(badge attach.StateBadge) string {
	switch badge {
	case attach.StateBadgeQuestion:
		return "?"
	case attach.StateBadgeApproval, attach.StateBadgePlan:
		return "!"
	case attach.StateBadgeFailed:
		return "×"
	case attach.StateBadgeRunning, attach.StateBadgePlanning:
		return "●"
	case attach.StateBadgeStale:
		return "○"
	case attach.StateBadgeDone:
		return "✓"
	default:
		return "·"
	}
}

func attachGlyph(target attach.Target) string {
	switch target.Snapshot.Badge(target.Status) {
	case attach.StateBadgeQuestion:
		return "? QUESTION"
	case attach.StateBadgeApproval, attach.StateBadgePlan:
		return "! APPROVAL"
	case attach.StateBadgeDone:
		return "✓ FINISHED"
	case attach.StateBadgeFailed:
		return "× FAILED"
	case attach.StateBadgeStale:
		return "○ DISCONNECTED"
	case attach.StateBadgeRunning, attach.StateBadgePlanning:
		return "● RUNNING"
	default:
		return "· IDLE"
	}
}

func (s *AttachScreen) renderDetail(width int) string {
	if s.selected >= len(s.rows) {
		return s.renderEmptyState()
	}
	row := s.rows[s.selected]
	if row.Target == nil {
		return s.renderFolderDetail(*row.Folder)
	}
	target := *row.Target
	if s.target != nil && s.target.ID == target.ID {
		target = *s.target
	}
	badge := target.Snapshot.Badge(target.Status)
	if s.target != nil && s.target.ID == target.ID {
		badge = s.snapshot.Badge(target.Status)
		target.Snapshot = s.snapshot
	}
	var b strings.Builder
	b.WriteString(lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color(palette.Text)).Render(target.Name) + "\n")
	b.WriteString(statusStyle(badge).Render(" "+strings.ToUpper(string(badge))+" ") + "\n\n")
	b.WriteString(lipgloss.NewStyle().Foreground(lipgloss.Color(palette.TextDim)).Render("SESSION") + "\n")
	if target.Machine != "" {
		b.WriteString("machine  " + target.Machine + "\n")
	}
	if target.ParentID != "" {
		b.WriteString("parent   " + target.ParentID + "\n")
	}
	if target.Snapshot.Screen != "" {
		b.WriteString("screen   " + target.Snapshot.Screen + "\n")
	}
	if target.Snapshot.OperatingMode != "" {
		b.WriteString("mode     " + target.Snapshot.OperatingMode + "\n")
	}
	workspace := target.Workspace
	if workspace == "" {
		workspace = target.Snapshot.Workspace
	}
	if workspace != "" {
		b.WriteString("folder   " + workspace + "\n")
	}
	branch := target.Branch
	if branch == "" {
		branch = target.Snapshot.Branch
	}
	if branch != "" {
		b.WriteString("branch   " + branch + "\n")
	}
	// Keep the selected target for error reporting after a failed poll, but do
	// not call that state "attached". readSession deliberately releases the
	// session on broken pipes/protocol errors; using target != nil here made
	// the pane continue to advertise detach/raw-terminal controls and hide the
	// "SESSION UNAVAILABLE" diagnostic.
	attached := s.session != nil && s.target != nil && s.target.ID == target.ID
	b.WriteString("\n" + s.renderActivityPreview(width, target, target.Snapshot, attached))
	if !attached {
		if s.err != nil {
			b.WriteString("\n" + statusStyle(attach.StateBadgeFailed).Render("× SESSION UNAVAILABLE") + "\n")
			b.WriteString(lipgloss.NewStyle().Foreground(lipgloss.Color(palette.TextDim)).Render(s.err.Error()))
			b.WriteString("\n\n" + helpStyle().Render("r retry attach  Tab next session"))
			return b.String()
		}
		b.WriteString("\n" + helpStyle().Render("Enter to open live session"))
		return b.String()
	}
	if !s.rawView {
		b.WriteString("\n\n" + helpStyle().Render("↑/↓ scroll  o raw terminal  Esc detach"))
		return b.String()
	}
	b.WriteString("\n")
	b.WriteString(s.viewport.View())
	return lipgloss.NewStyle().Width(width).Render(b.String())
}

func (s *AttachScreen) renderActivityPreview(width int, target attach.Target, snapshot attach.StateSnapshot, attached bool) string {
	var b strings.Builder
	b.WriteString(lipgloss.NewStyle().Foreground(lipgloss.Color(palette.TextDim)).Render("ACTIVITY") + "\n")

	badge := snapshot.Badge(target.Status)
	if attached {
		badge = s.snapshot.Badge(target.Status)
		snapshot = s.snapshot
	}
	phase := strings.ToUpper(string(badge))
	if snapshot.ActivityLabel != "" {
		phase = snapshot.ActivityLabel
	} else if snapshot.ActivityActive || snapshot.Streaming {
		phase = "WORKING NOW"
	}
	b.WriteString(statusStyle(badge).Render("● " + phase))
	if snapshot.OperatingMode != "" {
		b.WriteString("  " + lipgloss.NewStyle().Foreground(lipgloss.Color(palette.TextDim)).Render(snapshot.OperatingMode+" mode"))
	}
	b.WriteString("\n")

	summary := target.Summary
	if snapshot.Summary != "" {
		summary = snapshot.Summary
	}
	if snapshot.Preview != "" {
		b.WriteString("\n" + lipgloss.NewStyle().Foreground(lipgloss.Color(palette.TextDim)).Render("CURRENT") + "\n")
		b.WriteString(snapshot.Preview + "\n")
	}
	if summary != "" && summary != snapshot.Preview {
		b.WriteString("\n" + lipgloss.NewStyle().Foreground(lipgloss.Color(palette.TextDim)).Render("LATEST") + "\n")
		b.WriteString(summary + "\n")
	}
	if target.LastTool != "" {
		b.WriteString("\n" + lipgloss.NewStyle().Foreground(lipgloss.Color(palette.TextDim)).Render("TOOL ACTIVITY") + "\n")
		b.WriteString(fmt.Sprintf("⚙ %s", target.LastTool))
		if target.ToolCount > 0 {
			b.WriteString(fmt.Sprintf("  ·  %d calls", target.ToolCount))
		}
		b.WriteString("\n")
	}
	if target.Progress > 0 {
		b.WriteString(fmt.Sprintf("\nprogress  %d%%\n", target.Progress))
	}

	if len(snapshot.Messages) > 0 {
		b.WriteString("\n" + lipgloss.NewStyle().Foreground(lipgloss.Color(palette.TextDim)).Render("RECENT TRANSCRIPT") + "\n")
		if attached {
			b.WriteString(s.viewport.View())
		} else {
			b.WriteString(renderRecentAttachTranscript(snapshot.Messages, width))
		}
	} else if snapshot.Preview == "" && summary == "" && target.LastTool == "" && snapshot.ActivityLabel == "" {
		b.WriteString("\n" + lipgloss.NewStyle().Foreground(lipgloss.Color(palette.TextDim)).Render("Waiting for the agent to publish activity…") + "\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

func renderRecentAttachTranscript(messages []state.MessageState, width int) string {
	start := len(messages) - 4
	if start < 0 {
		start = 0
	}
	recent := renderAttachTranscript(messages[start:], attach.StateSnapshot{})
	return compactTargetName(recent, attachMaxInt(width*8, 240))
}

func (s *AttachScreen) renderFolderDetail(folder attach.FolderNode) string {
	var b strings.Builder
	b.WriteString(lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color(palette.Text)).Render(folder.Name) + "\n")
	if folder.Path != "" {
		b.WriteString(lipgloss.NewStyle().Foreground(lipgloss.Color(palette.TextDim)).Render(folder.Path) + "\n")
	}
	if folder.Machine != "" {
		b.WriteString("machine  " + folder.Machine + "\n")
	}
	if s.err != nil {
		b.WriteString("\n" + statusStyle(attach.StateBadgeFailed).Render("× SESSION UNAVAILABLE") + "\n")
		b.WriteString(lipgloss.NewStyle().Foreground(lipgloss.Color(palette.TextDim)).Render(compactTargetName(s.err.Error(), widthForDetail(s.width))) + "\n")
	}
	b.WriteString("\n" + statusStyle(folder.Badge).Render(strings.ToUpper(string(folder.Badge))) + "\n\n")
	b.WriteString(lipgloss.NewStyle().Foreground(lipgloss.Color(palette.TextDim)).Render("SESSIONS") + "\n")
	for _, target := range folder.Targets {
		badge := target.Snapshot.Badge(target.Status)
		label := compactTargetName(target.Name, 24)
		summary := attach.TargetSummary(target)
		if summary == "" || summary == "No conversation summary yet" {
			summary = "waiting for activity"
		}
		b.WriteString(statusStyle(badge).Render(folderGlyph(badge)) + " " + label + "\n")
		b.WriteString("  " + lipgloss.NewStyle().Foreground(lipgloss.Color(palette.TextDim)).Render(compactTargetName(summary, attachMaxInt(widthForDetail(s.width)-4, 18))) + "\n")
	}
	b.WriteString("\n" + helpStyle().Render("↑/↓ hover sessions  Space expand/collapse"))
	return b.String()
}

func (s *AttachScreen) renderEmptyState() string {
	return lipgloss.JoinVertical(lipgloss.Left,
		lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color(palette.Text)).Render("No live sessions"),
		lipgloss.NewStyle().Foreground(lipgloss.Color(palette.TextDim)).Render("Start another Swarm session or connect a peer."),
		"",
		helpStyle().Render("r refresh discovery"),
	)
}

func widthForDetail(screenWidth int) int {
	if screenWidth < 40 {
		return screenWidth
	}
	return screenWidth - 4
}

func selectLine(line string, selected bool) string {
	if !selected {
		return line
	}
	return lipgloss.NewStyle().Foreground(lipgloss.Color(palette.AccentSoft)).Bold(true).Render(line)
}

func helpStyle() lipgloss.Style {
	return lipgloss.NewStyle().Foreground(lipgloss.Color(palette.TextDim))
}

func statusStyle(badge attach.StateBadge) lipgloss.Style {
	color := palette.TextDim
	switch badge {
	case attach.StateBadgeRunning, attach.StateBadgePlanning:
		color = palette.Teal
	case attach.StateBadgeQuestion, attach.StateBadgeApproval, attach.StateBadgePlan:
		color = palette.Warning
	case attach.StateBadgeDone:
		color = palette.Success
	case attach.StateBadgeFailed:
		color = palette.Error
	case attach.StateBadgeStale:
		color = palette.TextMuted
	}
	return lipgloss.NewStyle().Foreground(lipgloss.Color(color))
}

// renderAttachTranscript deliberately mirrors the conversational rhythm of the
// main TUI: role headers, readable multiline content, and visible tool activity.
// It consumes MessageState rather than terminal cells, so it remains correct
// when the peer and viewer have different dimensions or render backends.
func renderAttachTranscript(messages []state.MessageState, snapshot attach.StateSnapshot) string {
	var b strings.Builder
	for _, msg := range messages {
		if strings.EqualFold(msg.Role, "system") {
			continue
		}
		role := strings.ToLower(strings.TrimSpace(msg.Role))
		label := role
		switch role {
		case "user":
			label = "you"
		case "assistant":
			label = "agent"
		case "":
			label = "event"
		}
		b.WriteString("┌─ " + label + "\n")
		rendered := false
		if content := strings.TrimSpace(msg.Content); content != "" {
			b.WriteString("│ " + strings.ReplaceAll(content, "\n", "\n│ ") + "\n")
			rendered = true
		}
		for _, block := range msg.Blocks {
			content := strings.TrimSpace(block.Content)
			if content == "" {
				continue
			}
			rendered = true
			prefix := "│ "
			switch strings.ToLower(block.Type) {
			case "thinking":
				prefix = "│ thinking  "
			case "tool_call":
				prefix = "│ ⚙ "
			case "tool_result":
				prefix = "│ ↳ "
			}
			b.WriteString(prefix + strings.ReplaceAll(content, "\n", "\n│ ") + "\n")
		}
		if !rendered && strings.TrimSpace(msg.Content) != "" {
			b.WriteString("│ " + strings.ReplaceAll(strings.TrimSpace(msg.Content), "\n", "\n│ ") + "\n")
		}
		if !rendered && strings.TrimSpace(msg.Content) == "" {
			b.WriteString("│ …\n")
		}
		b.WriteString("└\n\n")
	}
	if snapshot.Streaming {
		b.WriteString("… agent is still working\n")
	}
	if snapshot.ModalOpen && snapshot.ModalID != "" {
		b.WriteString("\n[" + snapshot.ModalID + " pending]\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

func (s *AttachScreen) renderCompact(prefix string) string {
	var b strings.Builder
	b.WriteString(prefix)
	b.WriteString(s.renderTree())
	if s.selected < len(s.rows) && s.rows[s.selected].Target != nil {
		b.WriteString("\n\n" + s.renderDetail(s.width))
	}
	return b.String()
}

func compactTargetName(name string, limits ...int) string {
	name = strings.TrimSpace(name)
	limit := 22
	if len(limits) > 0 && limits[0] > 0 {
		limit = limits[0]
	}
	runes := []rune(name)
	if len(runes) > limit {
		if limit < 2 {
			return string(runes[:limit])
		}
		return string(runes[:limit-1]) + "…"
	}
	return name
}

func compactTargetLabel(target attach.Target) string {
	conversation := strings.TrimSpace(target.ConversationID)
	if conversation == "" {
		conversation = strings.TrimSpace(target.Snapshot.ConversationID)
	}
	if conversation == "" {
		conversation = strings.TrimSpace(target.Name)
	}
	if conversation == "" {
		conversation = strings.TrimSpace(target.ID)
	}
	task := strings.TrimSpace(target.Snapshot.ActivityLabel)
	if task == "" {
		task = attach.TargetSummary(target)
	}
	if task == "" || task == "No conversation summary yet" {
		return compactTargetName(shortConversationID(conversation))
	}
	return compactTargetName(shortConversationID(conversation), 12) + " · " + compactTargetName(task, 32)
}

func shortConversationID(id string) string {
	id = strings.TrimSpace(id)
	runes := []rune(id)
	if len(runes) <= 12 {
		return id
	}
	return string(runes[:6]) + "…" + string(runes[len(runes)-4:])
}

func attachMaxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
