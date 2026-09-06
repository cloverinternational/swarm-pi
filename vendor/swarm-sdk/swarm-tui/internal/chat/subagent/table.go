package subagent

import (
	"fmt"
	"strings"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/chat/uitypes"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/ui/palette"
	"github.com/mattn/go-runewidth"
)

// AgentStatus represents the current state of a sub-agent
type AgentStatus string

const (
	StatusWorking   AgentStatus = "working"
	StatusStreaming AgentStatus = "streaming"
	StatusComplete  AgentStatus = "complete"
	StatusError     AgentStatus = "error"
)

// Progress holds todo completion progress for a sub-agent
type Progress struct {
	Completed int
	Total     int
}

// SubAgentRow represents one sub-agent in the table
type SubAgentRow struct {
	OwnerID     string      // Matches TodoManager owner ID
	AgentName   string      // Human-readable name
	Status      AgentStatus // Current status
	TaskSummary string      // Truncated task description
	CurrentTodo string      // Content of in-progress todo
	Progress    Progress    // Todo completion progress
	IsStreaming bool        // Whether actively streaming
	Error       string      // Error message if any
}

// SubAgentTable maintains state for multiple sub-agents and renders them compactly
type SubAgentTable struct {
	rows       []*SubAgentRow
	allDone    bool
	lastUpdate time.Time
}

// NewSubAgentTable creates a new sub-agent table
func NewSubAgentTable() *SubAgentTable {
	return &SubAgentTable{
		rows: make([]*SubAgentRow, 0),
	}
}

// AddRow adds a sub-agent row to the table
func (t *SubAgentTable) AddRow(row *SubAgentRow) {
	t.rows = append(t.rows, row)
	t.lastUpdate = time.Now()
}

// GetOrCreate finds or creates a row for the given owner
func (t *SubAgentTable) GetOrCreate(ownerID, agentName string) *SubAgentRow {
	for _, row := range t.rows {
		if row.OwnerID == ownerID {
			return row
		}
	}
	row := &SubAgentRow{
		OwnerID:   ownerID,
		AgentName: agentName,
		Status:    StatusWorking,
	}
	t.AddRow(row)
	return row
}

// UpdateProgress updates progress for a specific owner
func (t *SubAgentTable) UpdateProgress(ownerID string, completed, total int, currentTodo string) {
	for _, row := range t.rows {
		if row.OwnerID == ownerID {
			row.Progress.Completed = completed
			row.Progress.Total = total
			row.CurrentTodo = currentTodo
			t.lastUpdate = time.Now()
			break
		}
	}
}

// UpdateStatus updates the status for a specific owner
func (t *SubAgentTable) UpdateStatus(ownerID string, status AgentStatus) {
	for _, row := range t.rows {
		if row.OwnerID == ownerID {
			row.Status = status
			t.lastUpdate = time.Now()
			break
		}
	}
}

// SetStreaming sets the streaming state for a specific owner
func (t *SubAgentTable) SetStreaming(ownerID string, isStreaming bool) {
	for _, row := range t.rows {
		if row.OwnerID == ownerID {
			row.IsStreaming = isStreaming
			if isStreaming && row.Status == StatusWorking {
				row.Status = StatusStreaming
			}
			t.lastUpdate = time.Now()
			break
		}
	}
}

// SetError sets an error for a specific owner
func (t *SubAgentTable) SetError(ownerID string, errMsg string) {
	for _, row := range t.rows {
		if row.OwnerID == ownerID {
			row.Status = StatusError
			row.Error = errMsg
			t.lastUpdate = time.Now()
			break
		}
	}
}

// SetTaskSummary sets the task summary for a specific owner
func (t *SubAgentTable) SetTaskSummary(ownerID string, summary string) {
	for _, row := range t.rows {
		if row.OwnerID == ownerID {
			row.TaskSummary = summary
			t.lastUpdate = time.Now()
			break
		}
	}
}

// RemoveRow removes a row by owner ID
func (t *SubAgentTable) RemoveRow(ownerID string) {
	for i, row := range t.rows {
		if row.OwnerID == ownerID {
			t.rows = append(t.rows[:i], t.rows[i+1:]...)
			t.lastUpdate = time.Now()
			break
		}
	}
}

// GetRows returns all rows
func (t *SubAgentTable) GetRows() []*SubAgentRow {
	return t.rows
}

// Count returns the number of rows
func (t *SubAgentTable) Count() int {
	return len(t.rows)
}

// ActiveCount returns the number of non-complete rows
func (t *SubAgentTable) ActiveCount() int {
	count := 0
	for _, row := range t.rows {
		if row.Status != StatusComplete && row.Status != StatusError {
			count++
		}
	}
	return count
}

// IsAllDone returns true if all sub-agents are complete or errored
func (t *SubAgentTable) IsAllDone() bool {
	if len(t.rows) == 0 {
		return true
	}
	for _, row := range t.rows {
		if row.Status != StatusComplete && row.Status != StatusError {
			return false
		}
	}
	return true
}

// Clear removes all rows
func (t *SubAgentTable) Clear() {
	t.rows = make([]*SubAgentRow, 0)
	t.lastUpdate = time.Now()
}

// LastUpdate returns the time of the last update
func (t *SubAgentTable) LastUpdate() time.Time {
	return t.lastUpdate
}

// UpdateFromSubAgentDisplay updates the table from a SubAgentDisplay block
func (t *SubAgentTable) UpdateFromSubAgentDisplay(sa *uitypes.SubAgentDisplay, isStreaming bool) {
	if sa == nil {
		return
	}

	row := t.GetOrCreate(sa.AgentID, sa.AgentName)
	row.TaskSummary = sa.TaskInstruction
	row.IsStreaming = isStreaming

	// Determine status
	if isStreaming {
		row.Status = StatusStreaming
	} else {
		// Check if there's an error in the blocks
		hasError := false
		for _, block := range sa.Blocks {
			if block.Type == "tool_result" && block.ToolResult != nil && block.ToolResult.Error != "" {
				hasError = true
				row.Error = block.ToolResult.Error
				break
			}
		}
		if hasError {
			row.Status = StatusError
		} else {
			row.Status = StatusComplete
		}
	}
}

// Render renders the table at the given width
func (t *SubAgentTable) Render(width int, spinner string, animFrame int) string {
	if len(t.rows) == 0 {
		return ""
	}

	// Check if all done
	t.allDone = t.IsAllDone()

	// Collapsed view when all done
	if t.allDone {
		return t.renderCollapsed(width)
	}

	// Table view for multiple agents (3+)
	if len(t.rows) >= 3 {
		return t.renderTable(width, spinner, animFrame)
	}

	// Individual boxes for 1-2 agents
	return t.renderIndividual(width, spinner, animFrame)
}

// renderCollapsed renders a minimal summary when all agents are done
func (t *SubAgentTable) renderCollapsed(width int) string {
	names := make([]string, 0, len(t.rows))
	errorCount := 0
	for _, row := range t.rows {
		if row.Status == StatusError {
			errorCount++
		}
		names = append(names, row.AgentName)
	}

	var header string
	if errorCount > 0 {
		headerStyle := lipgloss.NewStyle().
			Foreground(lipgloss.Color(palette.Error)).
			Bold(true)
		header = headerStyle.Render(fmt.Sprintf("✗ %d sub-agents (%d errors)", len(t.rows), errorCount))
	} else {
		headerStyle := lipgloss.NewStyle().
			Foreground(lipgloss.Color(palette.Success)).
			Bold(true)
		header = headerStyle.Render(fmt.Sprintf("✓ %d sub-agents complete", len(t.rows)))
	}

	// Join names with dots
	body := strings.Join(names, " · ")

	// Truncate if too long
	maxBodyLen := width - 8
	if len(body) > maxBodyLen && maxBodyLen > 20 {
		body = body[:maxBodyLen-3] + "..."
	}

	content := header + "\n\n" + lipgloss.NewStyle().
		Foreground(lipgloss.Color(palette.TextMuted)).
		Render(body)

	boxStyle := lipgloss.NewStyle().
		Width(width-4).
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color(palette.Border)).
		Padding(0, 1)

	return boxStyle.Render(content)
}

// renderTable renders multiple agents in a compact table format.
//
// Every line in the table is derived from a single, consistent geometry so the
// borders, separators, column-header row and data rows all line up exactly. A
// data row produced by RenderTableRow has the shape
//
//	│ <agentCol> │ <statusCol> │ <taskCol> │ <progressCol> │
//
// whose visual width is sumCols + 13 (two outer "│", and " │ " separators add
// up to 13 fixed cells). The horizontal rules use one dash-segment of (col+2)
// per column joined by the box-drawing junctions, which yields the exact same
// width. taskCol is sized so the whole table fits inside `width`.
func (t *SubAgentTable) renderTable(width int, spinner string, animFrame int) string {
	const fixedCells = 13 // "│ " + 3×" │ " + "│" around the four columns

	// Column widths. taskCol flexes to fill the remaining space.
	agentCol := 14
	statusCol := 10
	progressCol := 10
	taskCol := width - agentCol - statusCol - progressCol - fixedCells

	if taskCol < 20 {
		// Not enough room at the preferred sizes — shrink the fixed columns.
		taskCol = 20
		availableForOthers := width - taskCol - fixedCells
		if availableForOthers >= 24 {
			agentCol = availableForOthers / 4
			statusCol = availableForOthers / 4
			progressCol = availableForOthers - agentCol - statusCol
		} else {
			// Extremely narrow: clamp to small but valid widths.
			agentCol = 8
			statusCol = 7
			progressCol = 7
		}
	}

	// Total visual width of every line in the table.
	totalWidth := agentCol + statusCol + progressCol + taskCol + fixedCells
	// Inner width between the two outer "│" characters.
	innerWidth := totalWidth - 2
	// Dash segments per column (one padding space on each side of the cell).
	segs := []int{agentCol + 2, statusCol + 2, taskCol + 2, progressCol + 2}

	var sb strings.Builder

	dimStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(palette.TextMuted))
	headerStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(palette.Accent)).
		Bold(true)

	// Top border (single span — the title row has no internal columns).
	sb.WriteString("┌")
	sb.WriteString(strings.Repeat("─", innerWidth))
	sb.WriteString("┐\n")

	// Title row: "Sub-Agents" on the left, "<n> active" on the right.
	title := "Sub-Agents"
	active := fmt.Sprintf("%d active", t.ActiveCount())
	// Usable space inside the row, minus the leading and trailing pad space.
	usable := innerWidth - 2
	gap := usable - lipgloss.Width(title) - lipgloss.Width(active)
	if gap < 1 {
		// Title section too narrow for both labels — drop the active count.
		active = ""
		gap = usable - lipgloss.Width(title)
		if gap < 0 {
			gap = 0
		}
	}
	sb.WriteString("│ ")
	sb.WriteString(headerStyle.Render(title))
	sb.WriteString(strings.Repeat(" ", gap))
	if active != "" {
		sb.WriteString(dimStyle.Render(active))
	}
	sb.WriteString(" │\n")

	// Separator below the title, introducing the column junctions.
	sb.WriteString(tableRule("├", "┬", "┤", segs))
	sb.WriteString("\n")

	// Column header labels.
	sb.WriteString(fmt.Sprintf("│ %s │ %s │ %s │ %s │\n",
		dimStyle.Render(padRight("Agent", agentCol)),
		dimStyle.Render(padRight("Status", statusCol)),
		dimStyle.Render(padRight("Current", taskCol)),
		dimStyle.Render(padRight("Todos", progressCol)),
	))

	// Separator between header and body.
	sb.WriteString(tableRule("├", "┼", "┤", segs))
	sb.WriteString("\n")

	// Data rows.
	for _, row := range t.rows {
		sb.WriteString(row.RenderTableRow(agentCol, statusCol, taskCol, progressCol, spinner, animFrame))
	}

	// Bottom border.
	sb.WriteString(tableRule("└", "┴", "┘", segs))

	return sb.String()
}

// tableRule builds one horizontal rule from per-column dash segments joined by
// the given left, middle (junction) and right box-drawing characters. The
// resulting visual width matches a data row exactly: sum(segs) + len(segs) + 1.
func tableRule(left, mid, right string, segs []int) string {
	var b strings.Builder
	b.WriteString(left)
	for i, s := range segs {
		if i > 0 {
			b.WriteString(mid)
		}
		if s < 0 {
			s = 0
		}
		b.WriteString(strings.Repeat("─", s))
	}
	b.WriteString(right)
	return b.String()
}

// renderIndividual renders 1-2 agents as individual boxes
func (t *SubAgentTable) renderIndividual(width int, spinner string, animFrame int) string {
	var boxes []string
	for _, row := range t.rows {
		boxes = append(boxes, row.RenderBox(width, spinner, animFrame))
	}
	return strings.Join(boxes, "\n\n")
}

// Helper function to pad string to fixed width (using visual width)
func padRight(s string, width int) string {
	visualWidth := lipgloss.Width(s)
	if visualWidth >= width {
		// Need to truncate to visual width
		return truncateToVisualWidth(s, width)
	}
	return s + strings.Repeat(" ", width-visualWidth)
}

// truncateToVisualWidth truncates a string to fit within the given visual width
func truncateToVisualWidth(s string, maxVisualWidth int) string {
	if maxVisualWidth <= 0 {
		return ""
	}

	currentWidth := 0
	for i, r := range s {
		charWidth := runewidth.RuneWidth(r)
		if currentWidth+charWidth > maxVisualWidth {
			return s[:i]
		}
		currentWidth += charWidth
	}
	return s
}
