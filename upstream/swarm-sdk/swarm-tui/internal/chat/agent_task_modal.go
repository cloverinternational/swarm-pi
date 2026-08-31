package chat

import (
	"fmt"
	"sort"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/tools/ii"
)

// AgentTaskModal shows a dashboard of agent-created tasks from TodoManager.
// Opened with Ctrl+T, closed with Esc or q.
//
// Layout: selectable OPEN and COMPLETED sections with an optional details pane.
type AgentTaskModal struct {
	scroll     int
	maxVis     int
	section    int // 0=open, 1=completed
	cursor     int
	selectedID string
	selections [2]string
	expanded   bool
	visible    []ii.TodoItem
}

// NewAgentTaskModal creates a modal with empty scroll state.
func NewAgentTaskModal() *AgentTaskModal {
	return &AgentTaskModal{}
}

// Update handles key input for the modal.
func (m *AgentTaskModal) Update(key string) {
	switch key {
	case "j", "down":
		if m.cursor+1 < len(m.visible) {
			m.cursor++
		}
	case "k", "up":
		if m.cursor > 0 {
			m.cursor--
		}
	case "ctrl+d":
		m.cursor += max(m.maxVis/2, 1)
		if m.cursor >= len(m.visible) {
			m.cursor = max(len(m.visible)-1, 0)
		}
	case "ctrl+u":
		m.cursor = max(m.cursor-max(m.maxVis/2, 1), 0)
	case "g", "home":
		m.cursor = 0
	case "G", "end":
		m.cursor = max(len(m.visible)-1, 0)
	case "tab":
		if m.cursor >= 0 && m.cursor < len(m.visible) {
			m.selections[m.section] = m.visible[m.cursor].ID
		}
		m.section = (m.section + 1) % 2
		m.scroll = 0
		m.cursor = 0
		m.selectedID = m.selections[m.section]
		m.expanded = false
		return
	case "enter", " ":
		if len(m.visible) > 0 {
			m.expanded = !m.expanded
		}
	}
	if m.cursor >= 0 && m.cursor < len(m.visible) {
		m.selectedID = m.visible[m.cursor].ID
		m.selections[m.section] = m.selectedID
	}
	m.ensureCursorVisible()
}

// Render produces the modal content, reading tasks from TodoManager.
func (m *AgentTaskModal) Render(width, height int, theme Theme, _ []Message) string {
	if width < 16 || height < 9 {
		return renderTinyTaskModal(width, height, theme)
	}
	// Lip Gloss Width includes the border and padding in the final frame.
	modalWidth := min(80, max(width, 1))
	contentWidth := max(modalWidth-6, 1)

	todoManager := ii.GetTodoManager()
	if todoManager == nil {
		return renderEmptyModal(modalWidth, theme)
	}

	todos := todoManager.Todos()
	openCount, completedCount := 0, 0
	selectedID := m.selectedID
	if selectedID == "" && m.cursor >= 0 && m.cursor < len(m.visible) {
		selectedID = m.visible[m.cursor].ID
	}
	m.visible = m.visible[:0]
	for _, task := range todos {
		if task.Status == ii.TodoStatusCompleted {
			completedCount++
			if m.section == 1 {
				m.visible = append(m.visible, task)
			}
		} else {
			openCount++
			if m.section == 0 {
				m.visible = append(m.visible, task)
			}
		}
	}
	sort.SliceStable(m.visible, func(i, j int) bool {
		left, right := m.visible[i], m.visible[j]
		if left.Active != right.Active {
			return left.Active
		}
		if left.Status != right.Status {
			return taskModalStatusRank(left.Status) < taskModalStatusRank(right.Status)
		}
		if left.Sequence != right.Sequence {
			return left.Sequence < right.Sequence
		}
		return left.ID < right.ID
	})
	if selectedID != "" {
		for index := range m.visible {
			if m.visible[index].ID == selectedID {
				m.cursor = index
				break
			}
		}
	}
	if m.cursor >= len(m.visible) {
		m.cursor = max(len(m.visible)-1, 0)
	}
	if len(m.visible) > 0 {
		m.selectedID = m.visible[m.cursor].ID
		m.selections[m.section] = m.selectedID
	} else {
		m.selectedID = ""
		m.expanded = false
	}
	var detailLines []string
	if m.expanded && len(m.visible) > 0 {
		detailLines = renderTaskDetails(theme, m.visible[m.cursor], contentWidth)
	}
	// Vertical padding and borders add four rows. Header, tabs, divider, and
	// footer consume another four, leaving this budget for rows and details.
	dynamicBudget := max(height-8, 1)
	detailCap := max(dynamicBudget-1, 0)
	if len(detailLines) > detailCap {
		detailLines = detailLines[:detailCap]
		if detailCap > 0 {
			detailLines[detailCap-1] = renderTaskDetailOverflow(theme)
		}
	}
	rowBudget := max(dynamicBudget-len(detailLines), 1)
	showScrollIndicator := len(m.visible) > rowBudget && rowBudget > 1
	if showScrollIndicator {
		rowBudget--
	}
	m.maxVis = min(rowBudget, 12)
	m.ensureCursorVisible()

	bg := lipgloss.Color(theme.BG)
	titleStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(theme.Primary)).
		Background(bg).
		Bold(true)
	header := truncateAgentText(tr("classic.tasks.header", openCount, completedCount), contentWidth)
	lines := []string{titleStyle.Render(header)}

	tabStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(theme.TextMuted)).Background(bg)
	activeTabStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(theme.Accent)).
		Background(bg).
		Bold(true).
		Underline(true)
	var todoTab, completedTab string
	if m.section == 0 {
		todoTab = activeTabStyle.Render(tr("classic.tasks.open", openCount))
		completedTab = tabStyle.Render(tr("classic.tasks.done", completedCount))
	} else {
		todoTab = tabStyle.Render(tr("classic.tasks.open", openCount))
		completedTab = activeTabStyle.Render(tr("classic.tasks.done", completedCount))
	}
	lines = append(lines, "  "+todoTab+"   "+completedTab)
	divStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(theme.TextDim)).Background(bg)
	lines = append(lines, divStyle.Render(strings.Repeat("─", contentWidth)))

	if len(m.visible) == 0 {
		emptyStyle := lipgloss.NewStyle().
			Foreground(lipgloss.Color(theme.TextMuted)).
			Background(bg).
			Italic(true)
		if m.section == 0 {
			lines = append(lines, emptyStyle.Render(tr("classic.tasks.no_open")))
		} else {
			lines = append(lines, emptyStyle.Render(tr("classic.tasks.no_done")))
		}
	} else {
		end := min(m.scroll+m.maxVis, len(m.visible))
		for index := m.scroll; index < end; index++ {
			lines = append(lines, renderTaskMenuLine(theme, m.visible[index], contentWidth, index == m.cursor))
		}
	}

	if showScrollIndicator {
		scrollStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(theme.TextDim)).Background(bg)
		lines = append(lines, scrollStyle.Render(fmt.Sprintf("  %d–%d of %d", m.scroll+1, min(m.scroll+m.maxVis, len(m.visible)), len(m.visible))))
	}
	if len(detailLines) > 0 {
		lines = append(lines, detailLines...)
	}

	hintStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(theme.TextMuted)).
		Background(bg).
		Italic(true)
	hint := truncateAgentText(tr("classic.tasks.hint"), contentWidth)
	lines = append(lines, hintStyle.Render(hint))
	for i := range lines {
		lines[i] = reapplyBackground(lines[i], theme.BG)
	}
	content := lipgloss.JoinVertical(lipgloss.Left, lines...)
	modalStyle := lipgloss.NewStyle().
		Width(modalWidth).
		Background(bg).
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color(theme.Primary)).
		Padding(1, 2)
	return reapplyBackground(modalStyle.Render(content), theme.BG)
}

func renderTinyTaskModal(width, height int, theme Theme) string {
	if width <= 0 || height <= 0 {
		return ""
	}
	bg := lipgloss.Color(theme.BG)
	textStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(theme.Primary)).
		Background(bg).
		Bold(true)
	if width < 3 || height < 3 {
		return reapplyBackground(textStyle.Render(truncateAgentText(tr("classic.tasks.title"), width)), theme.BG)
	}
	frame := lipgloss.NewStyle().
		Width(width).
		Background(bg).
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color(theme.Primary)).
		Align(lipgloss.Center)
	text := truncateAgentText(tr("classic.tasks.title"), max(width-2, 1))
	return reapplyBackground(frame.Render(textStyle.Render(text)), theme.BG)
}

func (m *AgentTaskModal) ensureCursorVisible() {
	if m.cursor < m.scroll {
		m.scroll = m.cursor
	}
	if m.maxVis > 0 && m.cursor >= m.scroll+m.maxVis {
		m.scroll = m.cursor - m.maxVis + 1
	}
	maxScroll := max(len(m.visible)-m.maxVis, 0)
	m.scroll = min(max(m.scroll, 0), maxScroll)
}

func taskModalStatusRank(status ii.TodoStatus) int {
	if status == ii.TodoStatusInProgress {
		return 0
	}
	if status == ii.TodoStatusPending {
		return 1
	}
	return 2
}

func renderTaskMenuLine(theme Theme, task ii.TodoItem, width int, selected bool) string {
	bg := lipgloss.Color(theme.BG)
	marker, markerColor := " ", theme.TextDim
	if selected {
		marker, markerColor = "›", theme.Accent
	}
	glyph, glyphColor := "○", theme.TextMuted
	switch task.Status {
	case ii.TodoStatusInProgress:
		glyph, glyphColor = "●", theme.Accent
	case ii.TodoStatusCompleted:
		glyph, glyphColor = "✓", theme.Success
	default:
		if len(task.DependsOn) > 0 {
			glyph, glyphColor = "⧗", theme.Warning
		}
	}
	text := task.Content
	if task.Active && task.ActiveForm != "" {
		text = task.ActiveForm
	}
	text = truncateAgentText(text, max(width-12, 8))
	markerStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(markerColor)).Background(bg).Bold(selected)
	glyphStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(glyphColor)).Background(bg).Bold(true)
	idStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(theme.TextDim)).Background(bg)
	textStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(theme.Text)).Background(bg).Bold(selected || task.Active)
	return reapplyBackground(fmt.Sprintf("%s %s %s %s",
		markerStyle.Render(marker),
		glyphStyle.Render(glyph),
		idStyle.Render("#"+task.ID),
		textStyle.Render(text)), theme.BG)
}

func renderTaskDetails(theme Theme, task ii.TodoItem, width int) []string {
	bg := lipgloss.Color(theme.BG)
	label := lipgloss.NewStyle().Foreground(lipgloss.Color(theme.TextDim)).Background(bg).Bold(true)
	value := lipgloss.NewStyle().Foreground(lipgloss.Color(theme.TextMuted)).Background(bg)
	lines := []string{
		label.Render(tr("classic.tasks.details")),
		value.Render("  " + truncateAgentText(tr("classic.tasks.status", string(task.Status)), max(width-2, 1))),
		value.Render("  " + truncateAgentText(tr("classic.tasks.category", string(task.Category)), max(width-2, 1))),
	}
	if task.Description != "" {
		lines = append(lines, label.Render(tr("classic.tasks.description")))
		for _, line := range wrapText(task.Description, max(width-4, 10)) {
			lines = append(lines, value.Render("  "+line))
		}
	}
	if len(task.DependsOn) > 0 {
		for _, line := range wrapText(tr("classic.tasks.blocked_by", "#"+strings.Join(task.DependsOn, ", #")), max(width-2, 1)) {
			lines = append(lines, value.Render("  "+line))
		}
	}
	if len(task.Blocks) > 0 {
		for _, line := range wrapText(tr("classic.tasks.blocks", "#"+strings.Join(task.Blocks, ", #")), max(width-2, 1)) {
			lines = append(lines, value.Render("  "+line))
		}
	}
	if len(task.Notes) > 0 {
		lines = append(lines, label.Render(tr("classic.tasks.latest_note")))
		for _, line := range wrapText(task.Notes[len(task.Notes)-1], max(width-4, 10)) {
			lines = append(lines, value.Render("  "+line))
		}
	}
	for i := range lines {
		lines[i] = reapplyBackground(lines[i], theme.BG)
	}
	return lines
}

func renderTaskDetailOverflow(theme Theme) string {
	return lipgloss.NewStyle().
		Foreground(lipgloss.Color(theme.TextDim)).
		Background(lipgloss.Color(theme.BG)).
		Render("  …")
}

// buildAgentNowSection creates the pinned top section showing in-progress tasks with tool context.
func buildAgentNowSection(theme Theme, tasks []ii.TodoItem, messages []Message, modalWidth int) []string {
	var lines []string

	if len(tasks) == 0 {
		return lines
	}

	// NOW header
	nowLabel := lipgloss.NewStyle().
		Foreground(lipgloss.Color(theme.Accent)).
		Bold(true).Render(tr("classic.tasks.now"))

	lines = append(lines, fmt.Sprintf("  %s", nowLabel))

	// Show each in-progress task with active tools
	for i, task := range tasks {
		if i >= 5 { // Limit to 5 tasks in NOW section
			moreStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(theme.TextDim))
			lines = append(lines, moreStyle.Render(tr("classic.tasks.more", len(tasks)-5)))
			break
		}

		line := renderTaskLine(theme, task, modalWidth-6, true)
		lines = append(lines, "    "+line)

		// Find active tools for this task (from the current assistant message)
		activeTools := findActiveToolsForTask(task, messages)
		if len(activeTools) > 0 {
			toolStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(theme.Info))
			for _, tool := range activeTools {
				lines = append(lines, toolStyle.Render(fmt.Sprintf("      → %s: %s", tool.toolName, tool.description)))
			}
		}
	}

	return lines
}

// buildTodoSection creates the list of pending tasks.
func buildTodoSection(theme Theme, tasks []ii.TodoItem, modalWidth int) []string {
	var lines []string

	// Group by owner
	tasksByOwner := make(map[string][]ii.TodoItem)
	for _, task := range tasks {
		owner := task.OwnerID
		if owner == "" {
			owner = "main"
		}
		tasksByOwner[owner] = append(tasksByOwner[owner], task)
	}

	// Render each owner's tasks
	for owner, ownerTasks := range tasksByOwner {
		if owner != "main" {
			ownerStyle := lipgloss.NewStyle().
				Foreground(lipgloss.Color(theme.Info)).
				Bold(true)
			lines = append(lines, ownerStyle.Render(fmt.Sprintf("  [%s]", owner)))
		}

		for _, task := range ownerTasks {
			line := renderTaskLine(theme, task, modalWidth-6, false)
			lines = append(lines, "    "+line)

			// Show blockers if any
			if len(task.DependsOn) > 0 {
				blockerStyle := lipgloss.NewStyle().
					Foreground(lipgloss.Color(theme.Warning)).
					Italic(true)
				lines = append(lines, blockerStyle.Render(tr("classic.tasks.row_blocked_by", strings.Join(task.DependsOn, ", "))))
			}

			// Show notes if any
			if len(task.Notes) > 0 {
				noteStyle := lipgloss.NewStyle().
					Foreground(lipgloss.Color(theme.TextDim)).
					Italic(true)
				for _, note := range task.Notes {
					lines = append(lines, noteStyle.Render(fmt.Sprintf("      ▸ %s", truncateAgentText(note, modalWidth-10))))
				}
			}
		}

		lines = append(lines, "") // Blank line between owners
	}

	return lines
}

// buildCompletedSection creates the list of completed tasks with tool summary.
func buildCompletedSection(theme Theme, tasks []ii.TodoItem, messages []Message, modalWidth int) []string {
	var lines []string

	// Sort by completion time, newest first
	// (In real implementation, would sort by CompletedAt)

	for i, task := range tasks {
		if i >= 20 { // Limit completed section
			break
		}

		iconStyle := lipgloss.NewStyle().
			Foreground(lipgloss.Color(theme.Success)).
			Bold(true)
		icon := iconStyle.Render("✓")

		contentStyle := lipgloss.NewStyle().
			Foreground(lipgloss.Color(theme.TextMuted))

		content := truncateAgentText(task.Content, modalWidth-10)

		line := fmt.Sprintf("  %s %s", icon, contentStyle.Render(content))
		lines = append(lines, line)

		// Show tool summary for completed tasks
		toolSummary := getToolSummaryForTask(task, messages)
		if toolSummary != "" {
			summaryStyle := lipgloss.NewStyle().
				Foreground(lipgloss.Color(theme.TextDim)).
				Italic(true)
			lines = append(lines, summaryStyle.Render(tr("classic.tasks.tools", toolSummary)))
		}

		// Show notes if any
		if len(task.Notes) > 0 {
			noteStyle := lipgloss.NewStyle().
				Foreground(lipgloss.Color(theme.TextDim))
			lines = append(lines, noteStyle.Render(tr("classic.tasks.notes", truncateAgentText(task.Notes[len(task.Notes)-1], modalWidth-15))))
		}
	}

	return lines
}

// findActiveToolsForTask finds currently executing tools for an in-progress task
func findActiveToolsForTask(task ii.TodoItem, messages []Message) []toolActivity {
	var activeTools []toolActivity

	// Look at the last assistant message for active tool calls
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role != "assistant" {
			continue
		}

		// Check if this message mentions the task
		msg := &messages[i]
		if !mentionsTask(msg, task) {
			continue
		}

		// Find tool calls without results (still running)
		toolResults := make(map[string]bool)
		for _, block := range msg.OrderedBlocks {
			if block.Type == "tool_result" && block.ToolResult != nil {
				toolResults[block.ToolResult.CallID] = true
			}
		}

		for _, block := range msg.OrderedBlocks {
			if block.Type == "tool_call" && block.ToolCall != nil {
				if !toolResults[block.ToolCall.ID] {
					// This tool is still running
					activeTools = append(activeTools, toolActivity{
						callID:      block.ToolCall.ID,
						toolName:    block.ToolCall.Name,
						description: NewDefaultToolActivityDescriber().Describe(block.ToolCall.Name, block.ToolCall.Parameters),
					})
				}
			}
		}

		break
	}

	return activeTools
}

// getToolSummaryForTask creates a summary of tools used for a completed task
func getToolSummaryForTask(task ii.TodoItem, messages []Message) string {
	toolCounts := make(map[string]int)

	// Find messages that mention this task
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role != "assistant" {
			continue
		}

		msg := &messages[i]
		if !mentionsTask(msg, task) {
			continue
		}

		// Count tool usage
		for _, block := range msg.OrderedBlocks {
			if block.Type == "tool_call" && block.ToolCall != nil {
				toolCounts[block.ToolCall.Name]++
			}
		}
	}

	if len(toolCounts) == 0 {
		return ""
	}

	// Build summary
	var parts []string
	for tool, count := range toolCounts {
		if count > 1 {
			parts = append(parts, fmt.Sprintf("%s (%dx)", tool, count))
		} else {
			parts = append(parts, tool)
		}
	}

	return strings.Join(parts, ", ")
}

// mentionsTask checks if a message mentions a task (by ID or content)
func mentionsTask(msg *Message, task ii.TodoItem) bool {
	content := strings.ToLower(msg.GetContent())
	taskContent := strings.ToLower(task.Content)

	// Check for task ID reference
	if strings.Contains(content, fmt.Sprintf("#%s", task.ID)) ||
		strings.Contains(content, fmt.Sprintf("task %s", task.ID)) {
		return true
	}

	// Check for task content mention (fuzzy match)
	// This is simplified - in reality would use better matching
	keywords := strings.Fields(taskContent)
	matchCount := 0
	for _, keyword := range keywords {
		if len(keyword) > 3 && strings.Contains(content, keyword) {
			matchCount++
		}
	}

	return matchCount >= len(keywords)/2
}

// renderTaskLine renders a single task line.
func renderTaskLine(theme Theme, task ii.TodoItem, maxWidth int, showActive bool) string {
	var icon string
	var iconColor string

	switch task.Status {
	case "in_progress":
		icon = "●"
		iconColor = theme.Accent
	case "pending":
		if len(task.DependsOn) > 0 {
			icon = "⧗"
			iconColor = theme.Warning
		} else {
			icon = "○"
			iconColor = theme.TextMuted
		}
	case "completed":
		icon = "✓"
		iconColor = theme.Success
	default:
		icon = "?"
		iconColor = theme.TextMuted
	}

	iconStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(iconColor)).
		Bold(true)

	// Use Content since TodoItem doesn't have ActiveForm
	content := task.Content

	// Add task ID for reference
	idStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(theme.TextDim))
	taskText := truncateAgentText(content, maxWidth-10)

	// Add category badge if available
	categoryBadge := ""
	if task.Category != "" {
		catStyle := lipgloss.NewStyle().
			Foreground(lipgloss.Color(theme.Info)).
			Background(lipgloss.Color(theme.TextDim)).
			Padding(0, 1)
		categoryBadge = catStyle.Render(string(task.Category))
	}

	if categoryBadge != "" {
		return fmt.Sprintf("%s %s %s %s",
			iconStyle.Render(icon),
			idStyle.Render(fmt.Sprintf("#%s", task.ID)),
			categoryBadge,
			taskText)
	}

	return fmt.Sprintf("%s %s %s",
		iconStyle.Render(icon),
		idStyle.Render(fmt.Sprintf("#%s", task.ID)),
		taskText)
}

// renderEmptyModal shows when TodoManager is not available.
func renderEmptyModal(modalWidth int, theme Theme) string {
	bg := lipgloss.Color(theme.BG)
	titleStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(theme.Primary)).
		Background(bg).
		Bold(true)

	emptyStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(theme.TextMuted)).
		Background(bg).
		Italic(true)

	content := lipgloss.JoinVertical(lipgloss.Center,
		titleStyle.Render(tr("classic.tasks.agent_tasks")),
		"",
		emptyStyle.Render(tr("classic.tasks.unavailable")),
		"",
		emptyStyle.Render(tr("classic.common.esc_close")),
	)

	modalStyle := lipgloss.NewStyle().
		Width(modalWidth).
		Background(bg).
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color(theme.Primary)).
		Padding(1, 2).
		Align(lipgloss.Center)

	return reapplyBackground(modalStyle.Render(content), theme.BG)
}

// truncateAgentText shortens a string to maxLen runes with "...".
func truncateAgentText(s string, maxLen int) string {
	if maxLen <= 0 {
		return s
	}
	runes := []rune(s)
	if len(runes) <= maxLen {
		return s
	}
	if maxLen <= 3 {
		return string(runes[:maxLen])
	}
	return string(runes[:maxLen-3]) + "..."
}
