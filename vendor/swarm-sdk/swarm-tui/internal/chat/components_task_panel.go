// Package chat provides the TUI chat interface components.
// This file implements TaskPanelModel for displaying active tasks above the chat input.
// Based on Claude Code's CoordinatorTaskPanel component.
package chat

import (
	"fmt"
	"strings"
	"time"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/tools/ii"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/i18n"
	"github.com/charmbracelet/lipgloss"
)

// TaskPanelModel renders active tasks above the chat input bar.
// It displays in_progress and pending tasks in a compact format,
// similar to Claude Code's CoordinatorTaskPanel.
type TaskPanelModel struct {
	// tasks is the current list of todo items to display
	tasks []ii.TodoItem

	// width is the available horizontal space
	width int

	// startTime tracks when we started displaying tasks (for elapsed time)
	startTime time.Time

	// hasVisibleTasks tracks if we have any non-completed tasks
	hasVisibleTasks bool

	// totalCount / completedCount track overall progress across all tasks
	// (including completed ones) so the panel can show a "N/M done" header.
	totalCount     int
	completedCount int

	// cached render for performance
	cachedRender string
	cacheKey     string
}

// NewTaskPanelModel creates a new TaskPanelModel instance.
func NewTaskPanelModel() TaskPanelModel {
	return TaskPanelModel{
		startTime: time.Now(),
	}
}

// SetWidth updates the panel width for layout calculations.
func (m *TaskPanelModel) SetWidth(width int) {
	if m.width != width {
		m.width = width
		m.cachedRender = "" // Invalidate cache
	}
}

// SetTasks updates the task list to display.
// Only in_progress and pending tasks are shown.
func (m *TaskPanelModel) SetTasks(tasks []ii.TodoItem) {
	// Filter to only visible tasks (in_progress and pending) while counting
	// overall progress across every task for the header.
	visibleTasks := make([]ii.TodoItem, 0, len(tasks))
	completed := 0
	for _, t := range tasks {
		if t.Status == ii.TodoStatusCompleted {
			completed++
		}
		if t.Status == ii.TodoStatusInProgress || t.Status == ii.TodoStatusPending {
			visibleTasks = append(visibleTasks, t)
		}
	}

	m.totalCount = len(tasks)
	m.completedCount = completed

	// Check if tasks changed
	newKey := m.buildCacheKey(visibleTasks)
	if newKey != m.cacheKey {
		m.tasks = visibleTasks
		m.cacheKey = newKey
		m.cachedRender = "" // Invalidate cache
	}

	m.hasVisibleTasks = len(visibleTasks) > 0
}

// HasVisibleTasks returns true if there are tasks to display.
func (m TaskPanelModel) HasVisibleTasks() bool {
	return m.hasVisibleTasks
}

// Height returns the number of lines the panel will occupy.
// Returns 0 if no tasks are visible.
func (m TaskPanelModel) Height() int {
	if !m.hasVisibleTasks || len(m.tasks) == 0 {
		return 0
	}
	// One header line + one line per task
	return len(m.tasks) + 1
}

// View renders the task panel.
// Returns empty string if no tasks are visible.
func (m *TaskPanelModel) View() string {
	if !m.hasVisibleTasks || len(m.tasks) == 0 {
		return ""
	}

	// Return cached render if available
	if m.cachedRender != "" {
		return m.cachedRender
	}

	// Build the task panel render
	var lines []string

	// Header: "Tasks   N/M done" — gives a sense of overall progress.
	lines = append(lines, m.renderHeader())

	// Order tasks hierarchically: each top-level task is followed by its
	// visible children. Within a sibling group, the active/in-progress task
	// sorts first, then pending.
	ordered := orderTasksHierarchically(m.tasks)

	// Render each task line with a depth-derived indent.
	for _, entry := range ordered {
		line := m.renderTaskLine(entry.task, entry.depth)
		lines = append(lines, line)
	}

	m.cachedRender = strings.Join(lines, "\n")
	return m.cachedRender
}

type taskPanelEntry struct {
	task  ii.TodoItem
	depth int
}

// orderTasksHierarchically arranges visible tasks parent-before-child. Tasks
// whose parent is not visible are treated as roots so nothing is dropped.
func orderTasksHierarchically(tasks []ii.TodoItem) []taskPanelEntry {
	visible := make(map[string]bool, len(tasks))
	for _, t := range tasks {
		visible[t.ID] = true
	}
	childrenByParent := make(map[string][]ii.TodoItem)
	var roots []ii.TodoItem
	for _, t := range tasks {
		if t.ParentID != "" && visible[t.ParentID] {
			childrenByParent[t.ParentID] = append(childrenByParent[t.ParentID], t)
		} else {
			roots = append(roots, t)
		}
	}
	sortSiblings := func(list []ii.TodoItem) {
		for i := range list {
			for j := i + 1; j < len(list); j++ {
				if taskFocusRank(list[j]) < taskFocusRank(list[i]) {
					list[i], list[j] = list[j], list[i]
				}
			}
		}
	}
	sortSiblings(roots)
	var out []taskPanelEntry
	var walk func(task ii.TodoItem, depth int)
	walk = func(task ii.TodoItem, depth int) {
		out = append(out, taskPanelEntry{task: task, depth: depth})
		kids := childrenByParent[task.ID]
		sortSiblings(kids)
		for _, kid := range kids {
			walk(kid, depth+1)
		}
	}
	for _, root := range roots {
		walk(root, 0)
	}
	return out
}

// taskFocusRank orders active focus first, then in-progress, then pending.
func taskFocusRank(t ii.TodoItem) int {
	switch {
	case t.Active:
		return 0
	case t.Status == ii.TodoStatusInProgress:
		return 1
	default:
		return 2
	}
}

// renderHeader renders the compact progress header, Claude-style.
// Format: "Tasks   N/M done" with a muted label and accent count.
func (m TaskPanelModel) renderHeader() string {
	labelStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(DefaultTheme.TextMuted)).
		Bold(true)

	header := labelStyle.Render(i18n.T("classic_chat.tasks.title"))

	if m.totalCount > 0 {
		countStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(DefaultTheme.TextDim))
		header += countStyle.Render(i18n.T("classic_chat.tasks.done", m.completedCount, m.totalCount))
	}

	return header
}

// renderTaskLine renders a single task line, Claude-style.
// Glyphs: ● active focus (accent, bold), ◐ in-progress but not focused
// (e.g. a parent whose subtask holds focus), ○ pending (muted),
// ⧗ pending-with-deps (warning). Subtasks are indented by depth.
func (m TaskPanelModel) renderTaskLine(task ii.TodoItem, depth int) string {
	var glyph, glyphColor, textColor string
	bold := false

	switch {
	case task.Active:
		glyph = "●"
		glyphColor = DefaultTheme.Accent
		textColor = DefaultTheme.Text
		bold = true
	case task.Status == ii.TodoStatusInProgress:
		glyph = "◐"
		glyphColor = DefaultTheme.Accent
		textColor = DefaultTheme.Text
	default: // pending
		if len(task.DependsOn) > 0 {
			glyph = "⧗" // blocked: waiting on a dependency
			glyphColor = DefaultTheme.Warning
		} else {
			glyph = "○"
			glyphColor = DefaultTheme.TextMuted
		}
		textColor = DefaultTheme.TextMuted
	}

	glyphStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(glyphColor)).Bold(true)

	// Category badge, e.g. [A]
	categoryLetter := getCategoryLetter(task.Category)
	badgeStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(DefaultTheme.TextDim))
	badge := badgeStyle.Render("[" + categoryLetter + "]")

	// Two-space base indent + two spaces per hierarchy level.
	indent := "  " + strings.Repeat("  ", depth)

	// Reserve space for indent + glyph (1) + space (1) + badge (3) + space (1).
	maxContentWidth := m.width - len(indent) - 6
	if maxContentWidth < 10 {
		maxContentWidth = 10
	}

	content := truncateTaskContent(task.Content, maxContentWidth)
	contentStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(textColor)).Bold(bold)

	return indent + glyphStyle.Render(glyph) + " " + badge + " " + contentStyle.Render(content)
}

// getCategoryLetter returns the single-letter abbreviation for a task category.
func getCategoryLetter(category ii.TaskCategory) string {
	switch category {
	case ii.TaskCategoryResearching:
		return "R"
	case ii.TaskCategoryPlanning:
		return "P"
	case ii.TaskCategoryActing:
		return "A"
	case ii.TaskCategoryVerifying:
		return "V"
	case ii.TaskCategoryDebugging:
		return "D"
	case ii.TaskCategoryDocumenting:
		return "X" // X for documenting (eXplain)
	default:
		return "A" // Default to acting
	}
}

// buildCacheKey creates a unique key for the current task state.
func (m TaskPanelModel) buildCacheKey(tasks []ii.TodoItem) string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("%d|%d|", m.width, len(tasks)))
	for _, t := range tasks {
		sb.WriteString(t.ID)
		sb.WriteString("|")
		sb.WriteString(string(t.Status))
		sb.WriteString("|")
		if t.Active {
			sb.WriteString("A")
		}
		sb.WriteString("|")
		sb.WriteString(t.ParentID)
		sb.WriteString("|")
		sb.WriteString(t.Content)
		sb.WriteString("|")
		sb.WriteString(string(t.Category))
		sb.WriteString("|")
	}
	return sb.String()
}

// truncateTaskContent truncates task content to fit within maxChars.
// Uses rune-safe truncation to avoid breaking unicode characters.
func truncateTaskContent(content string, maxChars int) string {
	runes := []rune(content)
	if len(runes) <= maxChars {
		return content
	}
	// Truncate and add ellipsis
	if maxChars > 1 {
		return string(runes[:maxChars-1]) + "…"
	}
	return string(runes[:maxChars])
}
