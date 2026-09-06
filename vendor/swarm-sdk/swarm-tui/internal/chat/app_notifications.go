package chat

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

// ============================================================================
// HELPERS
// ============================================================================

func clamp(v, min, max int) int {
	if v < min {
		return min
	}
	if v > max {
		return max
	}
	return v
}

// extractTaskResult extracts a preview of the result from Task tool JSON output.
// Returns empty string if parsing fails or result is empty.
func extractTaskResult(output string) string {
	if output == "" {
		return ""
	}

	// Try to parse as JSON
	var data map[string]any
	if err := json.Unmarshal([]byte(output), &data); err != nil {
		// Not JSON, return truncated output
		if len(output) > 60 {
			return output[:57] + "..."
		}
		return output
	}

	// Extract result field
	result, ok := data["result"].(string)
	if !ok || result == "" {
		return ""
	}

	// Clean up and truncate
	result = strings.TrimSpace(result)
	result = strings.ReplaceAll(result, "\n", " ")
	if len(result) > 80 {
		result = result[:77] + "..."
	}
	return result
}

func applyBackground(content string, bgColor string) string {
	if content == "" || bgColor == "" {
		return content
	}

	var bgStyle ansi.Style = ansi.Style{}.BackgroundColor(ansi.HexColor(bgColor))
	var bgSeq string = bgStyle.String()
	if bgSeq == ansi.ResetStyle {
		return content
	}

	// Re-apply the base background after any reset so whitespace stays colored.
	// Handle both short (\x1b[m) and long (\x1b[0m) reset forms.
	// Lipgloss uses \x1b[0m while charmbracelet/x/ansi uses \x1b[m.
	// Replace long form first to avoid double-replacement.
	content = strings.ReplaceAll(content, "\x1b[0m", "\x1b[0m"+bgSeq)
	// Now replace short form, but only instances that weren't already handled
	// (i.e., \x1b[m that aren't part of \x1b[0m which we already replaced)
	// Since \x1b[0m was replaced to \x1b[0m+bgSeq, any remaining \x1b[m
	// are genuine short resets
	content = strings.ReplaceAll(content, "\x1b[m", "\x1b[m"+bgSeq)
	// Fix any double-bgSeq from \x1b[0m containing \x1b[m as a substring
	content = strings.ReplaceAll(content, bgSeq+bgSeq, bgSeq)
	return bgSeq + content + ansi.ResetStyle
}

func timeAgo(t time.Time) string {
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return "now"
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd", int(d.Hours()/24))
	}
}

// addNotification adds a user-facing banner message with deduplication and a small cap.
func (a *App) addNotification(kind, text string) {
	if text == "" {
		return
	}

	a.pruneNotifications()

	for _, n := range a.notifications {
		if n.Kind == kind && n.Text == text {
			return
		}
	}
	if len(a.notifications) >= 3 {
		a.notifications = a.notifications[1:]
	}
	a.notifications = append(a.notifications, Notification{
		Kind:      kind,
		Text:      text,
		CreatedAt: time.Now(),
	})
}

// clearNotifications removes banners. If kind is empty, clears all.
func (a *App) clearNotifications(kind string) {
	if kind == "" {
		a.notifications = nil
		return
	}
	filtered := a.notifications[:0]
	for _, n := range a.notifications {
		if n.Kind != kind {
			filtered = append(filtered, n)
		}
	}
	a.notifications = filtered
}

func (a *App) pruneNotifications() {
	if len(a.notifications) == 0 {
		return
	}

	cutoff := time.Now().Add(-notificationTTL)
	filtered := a.notifications[:0]
	for _, n := range a.notifications {
		if n.CreatedAt.IsZero() {
			n.CreatedAt = time.Now()
		}
		if n.CreatedAt.After(cutoff) {
			filtered = append(filtered, n)
		}
	}
	a.notifications = filtered
}

func stampNotifications(ns []Notification) []Notification {
	if len(ns) == 0 {
		return ns
	}
	now := time.Now()
	for i := range ns {
		if ns[i].CreatedAt.IsZero() {
			ns[i].CreatedAt = now
		}
	}
	return ns
}

// renderNotifications renders stacked notification banners and returns content + line count.
// DEFENSIVE: Ensures output never exceeds terminal width to prevent layout breakage.
func (a *App) renderNotifications(width int) (string, int) {
	a.pruneNotifications()

	if len(a.notifications) == 0 {
		return "", 0
	}

	// DEFENSIVE: Calculate safe content width
	// Border adds 2 chars (left + right), padding adds 2 chars (left + right)
	// Add extra margin of 2 for safety
	const borderWidth = 2
	const paddingWidth = 2
	const safetyMargin = 2
	contentWidth := width - borderWidth - paddingWidth - safetyMargin

	// Clamp to reasonable bounds
	const maxBannerWidth = 90
	const minBannerWidth = 30
	if contentWidth > maxBannerWidth {
		contentWidth = maxBannerWidth
	}
	if contentWidth < minBannerWidth {
		contentWidth = minBannerWidth
	}

	th := a.theme
	var blocks []string

	for _, n := range a.notifications {
		color := th.Info
		label := "INFO"
		switch n.Kind {
		case "error":
			color = th.Error
			label = "ERROR"
		case "warning":
			color = th.Warning
			label = "WARNING"
		case "success":
			color = th.Success
			label = "OK"
		}

		labelStyle := lipgloss.NewStyle().
			Foreground(lipgloss.Color(color)).
			Bold(true)

		// Calculate text wrap width (content minus label and spacing)
		labelLen := lipgloss.Width(label)
		textWidth := contentWidth - labelLen - 4 // 4 = "  " before + "  " after label
		if textWidth < 20 {
			textWidth = 20
		}

		// Wrap the notification text
		wrappedLines := wrapText(n.Text, textWidth)

		// Build the content with proper indentation for wrapped lines
		var contentBuilder strings.Builder
		indent := strings.Repeat(" ", labelLen+2)
		for i, line := range wrappedLines {
			if i == 0 {
				contentBuilder.WriteString(labelStyle.Render(label))
				contentBuilder.WriteString("  ")
				contentBuilder.WriteString(line)
			} else {
				contentBuilder.WriteString("\n")
				contentBuilder.WriteString(indent)
				contentBuilder.WriteString(line)
			}
		}

		// Render banner with controlled width
		banner := lipgloss.NewStyle().
			Width(contentWidth).
			BorderStyle(lipgloss.NormalBorder()).
			BorderForeground(lipgloss.Color(color)).
			Padding(0, 1).
			Background(lipgloss.Color(th.BGLighter)).
			Render(contentBuilder.String())

		blocks = append(blocks, banner)
	}

	joined := lipgloss.JoinVertical(lipgloss.Left, blocks...)

	// DEFENSIVE: Final safety check - truncate any line that exceeds terminal width
	// This prevents layout breakage even if calculations above have edge cases
	// Use ansi.Truncate to preserve ANSI escape sequences
	lines := strings.Split(joined, "\n")
	for i, line := range lines {
		if ansi.StringWidth(line) > width {
			// Truncate to width - 1 to leave room for potential edge cases
			// ansi.Truncate preserves ANSI codes unlike rune-based truncation
			lines[i] = ansi.Truncate(line, width-1, "")
		}
	}
	joined = strings.Join(lines, "\n")

	return joined, strings.Count(joined, "\n") + 1
}

// ============================================================================
// WORKSPACE LAYOUT CALCULATION
// ============================================================================
