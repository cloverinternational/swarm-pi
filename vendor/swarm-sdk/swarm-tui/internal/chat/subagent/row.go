package subagent

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/chat/uitypes"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/i18n"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/ui/palette"
)

// RenderTableRow renders a single row in table format
func (r *SubAgentRow) RenderTableRow(agentW, statusW, taskW, progressW int, spinner string, animFrame int) string {
	// Agent name (truncated)
	name := r.truncateName(r.AgentName, agentW-2)

	// Status icon and color
	statusIcon, statusText := r.getStatusDisplay(spinner)

	// Current task or todo
	task := r.getTaskDisplay(taskW - 2)

	// Progress bar or text
	progress := r.getProgressDisplay(progressW-2, animFrame)

	// Build the row
	return fmt.Sprintf("│ %s │ %s │ %s │ %s │\n",
		padRight(statusIcon+" "+name, agentW),
		padRight(statusText, statusW),
		padRight(task, taskW),
		padRight(progress, progressW),
	)
}

// RenderBox renders a single row as a boxed display (for 1-2 agents)
func (r *SubAgentRow) RenderBox(width int, spinner string, animFrame int) string {
	contentWidth := max(
		// borders + padding
		width-6, 30)

	var content []string

	// Task summary
	if r.TaskSummary != "" {
		taskStyle := lipgloss.NewStyle().
			Foreground(lipgloss.Color(palette.TextMuted)).
			Italic(true)
		taskText := r.truncateText(r.TaskSummary, contentWidth-4)
		content = append(content, taskStyle.Render(taskText))
		content = append(content, "")
	}

	// Progress section
	if r.Progress.Total > 0 {
		progressBar := r.renderProgressBar(contentWidth-10, animFrame)
		content = append(content, i18n.T("chat_b.subagent.progress", progressBar))

		if r.CurrentTodo != "" {
			currentStyle := lipgloss.NewStyle().
				Foreground(lipgloss.Color(palette.TextDim))
			currentText := r.truncateText(r.CurrentTodo, contentWidth-12)
			content = append(content, currentStyle.Render(i18n.T("chat_b.subagent.current", currentText)))
		}
	}

	// Status indicator
	switch r.Status {
	case StatusComplete:
		doneStyle := lipgloss.NewStyle().
			Foreground(lipgloss.Color(palette.Success))
		content = append(content, "")
		content = append(content, doneStyle.Render(i18n.T("chat_b.subagent.complete")))
	case StatusError:
		errorStyle := lipgloss.NewStyle().
			Foreground(lipgloss.Color(palette.Error))
		content = append(content, "")
		errText := r.Error
		if len(errText) > contentWidth-10 {
			errText = errText[:contentWidth-13] + "..."
		}
		content = append(content, errorStyle.Render(i18n.T("chat_b.subagent.error", errText)))
	default:
		// Streaming indicator
		if r.IsStreaming {
			statusText := r.getStreamingStatus()
			pulseConfig := uitypes.PulseTextConfig{
				BaseColor:    ColorSubAgentIdentity,
				DimColor:     palette.TextMuted,
				PulseSpeed:   0.15,
				MinIntensity: 0.3,
				MaxIntensity: 1.0,
			}
			indicator := uitypes.RenderPulseTextWithDots(statusText, animFrame, pulseConfig)
			content = append(content, "")
			content = append(content, indicator)
		}
	}

	// Build header
	agentStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(ColorSubAgentIdentity)).
		Bold(true)

	var headerParts []string
	headerParts = append(headerParts, agentStyle.Render(r.AgentName))

	if r.IsStreaming {
		spinnerStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(ColorSubAgentIdentity))
		headerParts = append(headerParts, spinnerStyle.Render(spinner))
	}

	if r.Progress.Total > 0 {
		todoStyle := lipgloss.NewStyle().
			Foreground(lipgloss.Color(palette.TextMuted)).
			Faint(true)
		headerParts = append(headerParts, todoStyle.Render(i18n.T("chat_b.subagent.todos", r.Progress.Completed, r.Progress.Total)))
	}

	header := strings.Join(headerParts, " ")

	// Determine border color
	borderColor := palette.Border
	if r.IsStreaming {
		borderColor = ColorSubAgentIdentity
	} else if r.Status == StatusError {
		borderColor = palette.Error
	} else if r.Status == StatusComplete {
		borderColor = palette.Success
	}

	// Build box
	boxStyle := lipgloss.NewStyle().
		Width(contentWidth).
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color(borderColor)).
		Padding(0, 1)

	fullContent := header
	if len(content) > 0 {
		fullContent += "\n" + strings.Join(content, "\n")
	}

	return boxStyle.Render(fullContent)
}

// getStatusDisplay returns the icon and text for the status
func (r *SubAgentRow) getStatusDisplay(spinner string) (icon string, text string) {
	switch r.Status {
	case StatusWorking:
		if r.IsStreaming {
			return spinner, "working"
		}
		return "○", "working"
	case StatusStreaming:
		return spinner, "stream"
	case StatusComplete:
		return "✓", "done"
	case StatusError:
		return "✗", "error"
	default:
		return "○", "pending"
	}
}

// getTaskDisplay returns the current task or todo for display
func (r *SubAgentRow) getTaskDisplay(maxLen int) string {
	// Prefer current todo if available
	if r.CurrentTodo != "" {
		return r.truncateText(r.CurrentTodo, maxLen)
	}
	// Fall back to task summary
	if r.TaskSummary != "" {
		return r.truncateText(r.TaskSummary, maxLen)
	}
	return ""
}

// getProgressDisplay returns the progress as a string
func (r *SubAgentRow) getProgressDisplay(width int, animFrame int) string {
	if r.Progress.Total == 0 {
		return ""
	}

	completed := r.Progress.Completed
	total := r.Progress.Total

	// Show completion text
	if completed >= total {
		successStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(palette.Success))
		return successStyle.Render(fmt.Sprintf("%d/%d ✓", completed, total))
	}

	// Show progress with animated dots when working
	progressText := fmt.Sprintf("%d/%d", completed, total)
	if r.IsStreaming || r.Status == StatusWorking {
		// Add animated dots
		numDots := (animFrame % 3) + 1
		progressText += strings.Repeat(".", numDots)
	}

	accStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(palette.Warning))
	return accStyle.Render(progressText)
}

// renderProgressBar renders a visual progress bar
func (r *SubAgentRow) renderProgressBar(width int, animFrame int) string {
	if r.Progress.Total == 0 {
		return ""
	}

	completed := r.Progress.Completed
	total := r.Progress.Total

	// Calculate filled portion
	barWidth := max(
		// Reserve space for count
		width-8, 5)

	if completed >= total {
		// Complete
		filledBar := strings.Repeat("█", barWidth)
		successStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(palette.Success))
		return successStyle.Render(filledBar) + " ✓"
	}

	filled := min((barWidth*completed)/total, barWidth)

	// Animated fill when working
	var bar string
	if r.IsStreaming || r.Status == StatusWorking {
		// Pulse effect on the last filled block
		filledChar := "█"
		emptyChar := "░"
		if animFrame%2 == 0 {
			emptyChar = "░"
		}

		bar = strings.Repeat(filledChar, filled)
		if filled < barWidth {
			bar += emptyChar
			bar += strings.Repeat("░", barWidth-filled-1)
		}
	} else {
		bar = strings.Repeat("█", filled) + strings.Repeat("░", barWidth-filled)
	}

	accStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(palette.Warning))
	return accStyle.Render(bar) + fmt.Sprintf(" %d/%d", completed, total)
}

// getStreamingStatus returns a status text for streaming indicator
func (r *SubAgentRow) getStreamingStatus() string {
	if r.CurrentTodo != "" {
		return "working"
	}
	if r.Progress.Total > 0 && r.Progress.Completed < r.Progress.Total {
		return "working"
	}
	return "thinking"
}

// truncateName truncates an agent name for display
func (r *SubAgentRow) truncateName(name string, maxLen int) string {
	if len(name) <= maxLen {
		return name
	}
	// Try to preserve meaningful parts
	if maxLen < 5 {
		return name[:maxLen]
	}
	// Truncate with tilde
	return name[:maxLen-1] + "~"
}

// truncateText truncates text with ellipsis
func (r *SubAgentRow) truncateText(text string, maxLen int) string {
	if len(text) <= maxLen {
		return text
	}
	if maxLen <= 3 {
		return text[:maxLen]
	}
	// Try to break at word boundary
	truncated := text[:maxLen-3]
	lastSpace := strings.LastIndex(truncated, " ")
	if lastSpace > maxLen/2 {
		truncated = truncated[:lastSpace]
	}
	return truncated + "..."
}
