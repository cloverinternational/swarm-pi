package chat

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"
)

// renderCompactConversationRow renders a single conversation in the compact sidebar format
// Format: [●] [icon] Title...                    12m  45k
func (a *App) renderCompactConversationRow(conv Conversation, selected bool, maxWidth int) string {
	th := a.theme

	// Get adapter badge/icon text for width calculations
	badgeText := a.getConversationIcon(conv)

	// Activity indicator
	activityIcon := " "
	activityColor := th.TextMuted

	isRunningInBg := a.bgManager != nil && a.bgManager.IsRunning(conv.ID)
	if isRunningInBg {
		activityIcon = "●"
		activityColor = th.Warning
	} else if conv.IsActive {
		activityIcon = "●"
		activityColor = th.Success
	}

	// Format duration using our new helper
	duration := ""
	if !conv.LastMessage.IsZero() {
		duration = formatConversationTime(conv.LastMessage)
	}

	// Format token count using our new helper
	tokens := ""
	if conv.TotalTokens > 0 {
		tokens = formatCompactTokens(conv.TotalTokens)
	}

	// Responsive logic: hide stats if very narrow
	showStats := true
	if maxWidth < 30 {
		showStats = false
	}

	// Calculate right column width
	rightColWidth := 0
	if showStats {
		if duration != "" {
			rightColWidth += len(duration)
		}
		if tokens != "" {
			if rightColWidth > 0 {
				rightColWidth += 2 // spaces between columns
			}
			rightColWidth += len(tokens)
		}
	}

	// Calculate prefix length
	// activity(1) + space + badge + space
	prefixLen := 1 + 1 + len(badgeText) + 1

	if rightColWidth > 0 {
		prefixLen += rightColWidth + 2
	}

	// Calculate available space for title
	titleWidth := maxWidth - prefixLen
	if titleWidth < 5 {
		titleWidth = 5
	}

	// Truncate title if needed (rune-safe)
	title := conv.Title
	if title == "" {
		title = tr("classic.conversations.untitled_short")
	}
	if runes := []rune(title); len(runes) > titleWidth {
		title = string(runes[:titleWidth-1]) + "…"
	}

	// Calculate padding for right-aligned stats
	visibleLen := 1 + 1 + len(badgeText) + 1 + len([]rune(title))
	padding := maxWidth - visibleLen - rightColWidth - 1
	if padding < 0 {
		padding = 0
	}

	// Build the row
	var row strings.Builder

	// Activity indicator
	row.WriteString(lipgloss.NewStyle().
		Foreground(lipgloss.Color(activityColor)).
		Render(activityIcon))
	row.WriteString(" ")

	// Colored adapter icon
	row.WriteString(a.renderConversationIcon(conv))
	row.WriteString(" ")

	// Title
	titleStyle := lipgloss.NewStyle()
	if selected {
		titleStyle = titleStyle.Foreground(lipgloss.Color(th.Text)).Bold(true)
	} else {
		titleStyle = titleStyle.Foreground(lipgloss.Color(th.TextDim))
	}
	row.WriteString(titleStyle.Render(title))

	// Right-aligned stats with padding
	if showStats && rightColWidth > 0 {
		row.WriteString(strings.Repeat(" ", padding))
		row.WriteString(" ")

		statsStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(th.TextMuted))

		if duration != "" {
			row.WriteString(statsStyle.Render(duration))
		}
		if tokens != "" {
			if duration != "" {
				row.WriteString("  ")
			}
			row.WriteString(statsStyle.Render(tokens))
		}
	}

	result := row.String()

	// Apply selection highlighting
	if selected {
		return lipgloss.NewStyle().
			Background(lipgloss.Color(th.BGLight)).
			Width(maxWidth).
			Render(result)
	}

	return result
}

// renderGroupedConversations renders conversations grouped by time and branch
func (a *App) renderGroupedConversations(width, height int) string {
	th := a.theme

	groups := a.groupConversations(a.conversations)

	var lines []string
	globalIdx := 0

	for groupIdx, group := range groups {
		// Add spacing between groups (except first)
		if groupIdx > 0 {
			lines = append(lines, "")
		}

		// Group header (Time Period)
		headerStyle := lipgloss.NewStyle().
			Foreground(lipgloss.Color(th.Primary)).
			Bold(true)

		// Count total conversations in this time group (using flattened list)
		header := tr("classic.conversations.group_count", localizedConversationGroup(group.Label), len(group.Conversations))
		lines = append(lines, headerStyle.Render(header))

		// Render subgroups (Branches)
		for _, subGroup := range group.SubGroups {
			// Branch header
			branchStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(th.Text)).Bold(true)

			icon := "⎇" // Git branch icon
			displayName := subGroup.Label

			// Handle special cases based on raw name
			if subGroup.RawName == "Untracked" {
				branchStyle = lipgloss.NewStyle().Foreground(lipgloss.Color(th.TextMuted)).Italic(true)
				icon = "◌"
				displayName = tr("classic.conversations.filter_untracked")
			} else if subGroup.RawName == "HEAD" {
				// Detached HEAD state
				branchStyle = lipgloss.NewStyle().Foreground(lipgloss.Color(th.Warning)).Bold(true)
				icon = "⚠"
				displayName = strings.Replace(displayName, "HEAD", tr("classic.conversations.detached_head"), 1)
			} else if strings.Contains(subGroup.RawName, "/") {
				// Remote branch like origin/main, upstream/feature
				parts := strings.Split(subGroup.RawName, "/")
				if len(parts) >= 2 {
					remote := parts[0]
					branch := strings.Join(parts[1:], "/")

					// Rebuild display name with remote in muted color
					currentSuffix := ""
					if strings.HasSuffix(subGroup.Label, " [Current]") {
						currentSuffix = tr("classic.conversations.current_suffix")
					}

					displayName = lipgloss.NewStyle().
						Foreground(lipgloss.Color(th.TextMuted)).
						Render(remote+": ") + branch + currentSuffix
					icon = "↗" // Remote icon
				}
			}

			branchHeader := fmt.Sprintf("  %s %s (%d)", icon, displayName, len(subGroup.Conversations))
			lines = append(lines, branchStyle.Render(branchHeader))

			// Conversations in subgroup
			for _, conv := range subGroup.Conversations {
				selected := globalIdx == a.selectedIdx
				// Indent 4 spaces (under branch)
				row := a.renderCompactConversationRow(conv, selected, width-4)
				lines = append(lines, "    "+row)

				globalIdx++
			}
		}
	}

	// Apply scrolling
	visibleLines := lines
	if len(lines) > height {
		start := a.scrollOffset
		end := start + height

		if end > len(lines) {
			end = len(lines)
		}
		if start < 0 {
			start = 0
		}
		if start < len(lines) {
			visibleLines = lines[start:end]
		}
	}

	// Join lines
	return strings.Join(visibleLines, "\n")
}

// renderEnhancedConversationList renders the enhanced conversation list for the sidebar
func (a *App) renderEnhancedConversationList(width int) string {
	th := a.theme

	// System Prompt Indicator (shown at top of sidebar)
	var systemPromptSection string
	if a.settingsManager != nil {
		if sp := a.settingsManager.GetSystemPromptSettings(); sp != nil {
			promptName := sp.GetActivePromptName()
			if promptName != "" {
				promptStyle := lipgloss.NewStyle().
					Foreground(lipgloss.Color(th.TextMuted)).
					Width(width-2).
					Padding(0, 1)
				// Truncate if too long, leave room for icon and padding
				maxLen := width - 6
				if len(promptName) > maxLen && maxLen > 3 {
					promptName = promptName[:maxLen-3] + "..."
				}
				promptText := fmt.Sprintf("◆ %s", promptName)
				systemPromptSection = promptStyle.Render(promptText)
			}
		}
	}

	// Header
	headerStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(th.Primary)).
		Background(lipgloss.Color(th.BGLight)).
		Bold(true).
		Width(width-2).
		Padding(0, 1)

	runningCount := 0
	if a.bgManager != nil {
		runningCount = a.bgManager.Count()
	}

	headerText := tr("classic.conversations.sessions", len(a.conversations))
	if runningCount > 0 {
		headerText = tr("classic.conversations.sessions_active", len(a.conversations), runningCount)
	}

	header := headerStyle.Render(headerText)

	// Calculate available height for content
	// Account for: system prompt (if present) + sessions header + divider + spacing
	headerHeight := 3 // sessions header + divider + spacing
	if systemPromptSection != "" {
		headerHeight += 2 // system prompt line + spacing
	}
	availableHeight := a.height - headerHeight - 2 // -2 for panel borders

	// Render grouped conversations
	content := a.renderGroupedConversations(width, availableHeight)

	// Combine header and content
	divider := strings.Repeat("─", width-2)

	// Build sections array for vertical join
	sections := []string{}
	if systemPromptSection != "" {
		sections = append(sections, systemPromptSection)
	}
	sections = append(sections, header, divider, content)

	return lipgloss.JoinVertical(
		lipgloss.Left,
		sections...,
	)
}
