package chat

import (
	"fmt"
	"strings"

	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/i18n"
	"github.com/charmbracelet/lipgloss"
)

// PlanViewer is a dedicated viewport for reviewing plans in plan mode.
// It takes over the chat message viewport and provides scroll functionality.
type PlanViewer struct {
	viewport *MessageList
	content  string // Raw plan content (markdown)
	width    int
	height   int

	// Styling
	titleStyle  lipgloss.Style
	headerStyle lipgloss.Style
	codeStyle   lipgloss.Style
	textStyle   lipgloss.Style
	borderStyle lipgloss.Style
}

// NewPlanViewer creates a new plan viewer with the given content.
func NewPlanViewer(content string) *PlanViewer {
	v := &PlanViewer{
		viewport: NewMessageList(80, 24), // Default size, will be resized
		content:  content,
	}

	// Set up styles
	v.titleStyle = lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color("#39D2C0")).
		Padding(0, 1)

	v.headerStyle = lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color("#7C7C7C"))

	v.codeStyle = lipgloss.NewStyle().
		Foreground(lipgloss.Color("#B8B8B8")).
		Background(lipgloss.Color("#1E1E1E"))

	v.textStyle = lipgloss.NewStyle()

	v.borderStyle = lipgloss.NewStyle().
		Foreground(lipgloss.Color("#3C3C3C"))

	// Set content
	v.viewport.SetContent(v.renderPlanContent())

	return v
}

// renderPlanContent renders the plan markdown with basic formatting.
// We keep it simple without ANSI styling to allow proper text wrapping.
func (v *PlanViewer) renderPlanContent() string {
	if v.content == "" {
		return i18n.T("classic_chat_3.plan.no_content")
	}

	// Simple markdown rendering - add visual structure but no ANSI styling
	// This allows the MessageList to wrap text properly
	lines := strings.Split(v.content, "\n")
	var rendered []string
	inCodeBlock := false

	for _, line := range lines {
		// Handle code blocks
		if strings.HasPrefix(line, "```") {
			inCodeBlock = !inCodeBlock
			lang := strings.TrimPrefix(line, "```")
			if lang != "" {
				rendered = append(rendered, "┌─ "+lang+" ─")
			} else {
				rendered = append(rendered, "└─────────────")
			}
			continue
		}

		if inCodeBlock {
			rendered = append(rendered, "│ "+line)
			continue
		}

		// Handle headers - add visual emphasis
		if strings.HasPrefix(line, "# ") {
			rendered = append(rendered, "")
			rendered = append(rendered, "━━━ "+strings.TrimPrefix(line, "# ")+" ━━━")
			rendered = append(rendered, "")
			continue
		}

		if strings.HasPrefix(line, "## ") {
			rendered = append(rendered, "")
			rendered = append(rendered, "  ■ "+strings.TrimPrefix(line, "## "))
			continue
		}

		if after, ok := strings.CutPrefix(line, "### "); ok {
			rendered = append(rendered, "  ▸ "+after)
			continue
		}

		// Handle horizontal rules
		if strings.HasPrefix(line, "---") || strings.HasPrefix(line, "***") {
			rendered = append(rendered, "───────────────────────────────────────")
			continue
		}

		// Handle lists (preserve indentation)
		if strings.HasPrefix(line, "- ") || strings.HasPrefix(line, "* ") {
			rendered = append(rendered, "  • "+strings.TrimPrefix(strings.TrimPrefix(line, "- "), "* "))
			continue
		}

		// Numbered lists
		if len(line) > 2 && line[1] == '.' && line[0] >= '0' && line[0] <= '9' {
			rendered = append(rendered, "  "+line)
			continue
		}

		// Regular text
		rendered = append(rendered, line)
	}

	return strings.Join(rendered, "\n")
}

// SetSize sets the viewport dimensions.
func (v *PlanViewer) SetSize(width, height int) {
	// Handle edge cases
	if width < 20 {
		width = 20 // Minimum width for readability
	}
	if height < 5 {
		height = 5 // Minimum height
	}

	v.width = width
	v.height = height

	// Reserve space for header
	headerHeight := 2
	contentHeight := height - headerHeight
	if contentHeight < 1 {
		contentHeight = 1
	}

	v.viewport.SetSize(width, contentHeight)
}

// SetOrigin sets the viewport's top-left screen position for mouse mapping.
func (v *PlanViewer) SetOrigin(x, y int) {
	v.viewport.SetOrigin(x, y)
}

// ScrollDown scrolls the viewport down by n lines.
func (v *PlanViewer) ScrollDown(n int) {
	v.viewport.ScrollDown(n)
}

// ScrollUp scrolls the viewport up by n lines.
func (v *PlanViewer) ScrollUp(n int) {
	v.viewport.ScrollUp(n)
}

// PageDown scrolls the viewport down by one page.
func (v *PlanViewer) PageDown() {
	v.viewport.PageDown()
}

// PageUp scrolls the viewport up by one page.
func (v *PlanViewer) PageUp() {
	v.viewport.PageUp()
}

// HalfPageDown scrolls the viewport down by half a page.
func (v *PlanViewer) HalfPageDown() {
	v.viewport.HalfPageDown()
}

// HalfPageUp scrolls the viewport up by half a page.
func (v *PlanViewer) HalfPageUp() {
	v.viewport.HalfPageUp()
}

// GotoTop scrolls to the top of the plan.
func (v *PlanViewer) GotoTop() {
	v.viewport.GotoTop()
}

// GotoBottom scrolls to the bottom of the plan.
func (v *PlanViewer) GotoBottom() {
	v.viewport.GotoBottom()
}

// TotalLines returns the total number of lines in the plan.
func (v *PlanViewer) TotalLines() int {
	return v.viewport.TotalLineCount()
}

// VisibleLines returns the number of visible lines.
func (v *PlanViewer) VisibleLines() int {
	return v.viewport.VisibleLineCount()
}

// YOffset returns the current scroll offset.
func (v *PlanViewer) YOffset() int {
	return v.viewport.YOffset
}

// View renders the plan viewer.
func (v *PlanViewer) View() string {
	// Render header
	header := v.renderHeader()

	// Render viewport content
	content := v.viewport.View()

	// Combine header and content
	return lipgloss.JoinVertical(lipgloss.Left, header, content)
}

// renderHeader renders the plan viewer header with scroll info.
func (v *PlanViewer) renderHeader() string {
	// Calculate scroll percentage
	total := v.viewport.TotalLineCount()
	visible := v.viewport.VisibleLineCount()
	offset := v.viewport.YOffset

	// Edge case: empty content
	if total == 0 {
		return v.titleStyle.Render(i18n.T("classic_chat_3.plan.title")) +
			v.borderStyle.Render(i18n.T("classic_chat_3.plan.empty"))
	}

	// Calculate percentage through the document
	var percent int
	if visible >= total {
		percent = 100
	} else {
		percent = (offset * 100) / (total - visible)
		if percent > 100 {
			percent = 100
		}
	}

	// Build header with title and scroll indicator
	title := v.titleStyle.Render(i18n.T("classic_chat_3.plan.title"))

	// Scroll indicator
	scrollInfo := ""
	if total > visible {
		scrollInfo = v.borderStyle.Render(" [") +
			v.textStyle.Render(fmt.Sprintf("%d%%", percent)) +
			v.borderStyle.Render(i18n.T("classic_chat_3.plan.scroll_help_full"))
	} else {
		scrollInfo = v.borderStyle.Render(i18n.T("classic_chat_3.plan.scroll_help"))
	}

	// Line info
	lineInfo := v.borderStyle.Render(i18n.T("classic_chat_3.plan.lines",
		offset+1, minInt(offset+visible, total), total))

	// Build the full header line
	headerLine := lipgloss.JoinHorizontal(lipgloss.Top, title, scrollInfo, lineInfo)

	// Add a subtle separator
	separator := v.borderStyle.Render(strings.Repeat("─", v.width))

	return lipgloss.JoinVertical(lipgloss.Left, headerLine, separator)
}

// minInt returns the smaller of two integers.
func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
