package chat

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

func (a *App) handleConversationsKey(key string) (tea.Model, tea.Cmd) {
	selectedBefore := a.selectedIdx
	defer func() {
		if a.selectedIdx != selectedBefore {
			a.loadSelectedHistoryPreview()
		}
	}()

	if a.twoPaneMode && key == "\\" {
		a.sidebarVisible = !a.sidebarVisible
		return a, nil
	}

	// Calculate the visible rows in the upper list pane.
	listHeight := a.height
	if a.twoPaneMode {
		listHeight, _ = historyStackHeights(a.height)
	}
	availableHeight := max(1, listHeight-3) // panel borders + Sessions header
	itemsPerConv := 4                       // Must match renderConversationList

	switch key {
	case "j", "down":
		if a.selectedIdx < len(a.conversations)-1 {
			a.selectedIdx++
			// Reset preview scroll when changing selection
			a.previewScrollOffset = 0
			a.messageScrollOffset = 0 // Also reset message scroll

			// Auto-scroll differently based on view mode
			// In two-pane mode, we always treat navigation as if in compact view (grouped)
			if a.compactConversationView || a.twoPaneMode {
				// For compact/two-pane view, ensure selected line is visible
				// We need to calculate which line the selected conversation is on
				selectedLine := a.getCompactLineForConversation(a.selectedIdx)
				if selectedLine >= a.scrollOffset+availableHeight-1 {
					a.scrollOffset = selectedLine - availableHeight + 2
				}
			} else {
				// Auto-scroll down if selected item goes below visible area
				// Each conversation takes itemsPerConv lines
				selectedEndLine := (a.selectedIdx + 1) * itemsPerConv
				if selectedEndLine > a.scrollOffset+availableHeight {
					a.scrollOffset = selectedEndLine - availableHeight
				}
			}

			// Lazy loading: check if we need to load more conversations
			a.checkAndLoadNextPage()
		}
	case "k", "up":
		if a.selectedIdx > 0 {
			a.selectedIdx--
			// Reset preview scroll when changing selection
			a.previewScrollOffset = 0
			a.messageScrollOffset = 0 // Also reset message scroll

			// Auto-scroll differently based on view mode
			if a.compactConversationView || a.twoPaneMode {
				// For compact/two-pane view, ensure selected line is visible
				selectedLine := a.getCompactLineForConversation(a.selectedIdx)
				if selectedLine < a.scrollOffset {
					a.scrollOffset = selectedLine
				}
			} else {
				// Auto-scroll up if selected item goes above visible area
				selectedStartLine := a.selectedIdx * itemsPerConv
				if selectedStartLine < a.scrollOffset {
					a.scrollOffset = selectedStartLine
				}
			}
		}
	case "g": // Jump to top
		a.selectedIdx = 0
		a.scrollOffset = 0
		a.previewScrollOffset = 0
		a.messageScrollOffset = 0
	case "G": // Jump to bottom (capital G)
		if len(a.conversations) > 0 {
			a.selectedIdx = len(a.conversations) - 1
			a.previewScrollOffset = 0
			a.messageScrollOffset = 0

			if a.compactConversationView || a.twoPaneMode {
				// For compact view, calculate total lines and position at bottom
				selectedLine := a.getCompactLineForConversation(a.selectedIdx)
				if selectedLine >= availableHeight {
					a.scrollOffset = selectedLine - availableHeight + 2
				}
			} else {
				// Scroll to show the last conversation
				selectedEndLine := (a.selectedIdx + 1) * itemsPerConv
				if selectedEndLine > availableHeight {
					a.scrollOffset = selectedEndLine - availableHeight
				}
			}
		}
	case "h", "left": // Scroll preview up
		if !a.twoPaneMode {
			if a.previewScrollOffset > 0 {
				a.previewScrollOffset -= 5 // Scroll by 5 lines
			}
		}
	case "l", "right": // Scroll preview down
		if !a.twoPaneMode {
			a.previewScrollOffset += 5 // Scroll by 5 lines
		}
	case "tab": // Toggle collapse/expand fork group
		if a.selectedIdx >= 0 && a.selectedIdx < len(a.conversations) {
			conv := a.conversations[a.selectedIdx]
			if a.collapsedParents == nil {
				a.collapsedParents = make(map[string]bool)
			}
			// Toggle collapse for this conversation's forks
			a.collapsedParents[conv.ID] = !a.collapsedParents[conv.ID]
		}
	case "enter":
		a.openConversation(a.selectedIdx)
	case "n":
		a.startNewChatDirect()
	case "v": // Toggle view mode
		if !a.twoPaneMode {
			a.compactConversationView = !a.compactConversationView
			// Reset scroll when changing view mode
			a.scrollOffset = 0
		}
	case "1":
		// Filter: All conversations
		a.branchFilter = FilterAll
		return a, a.loadConversationsAsync()
	case "2":
		// Filter: Current branch only
		a.branchFilter = FilterCurrentBranch
		return a, a.loadConversationsAsync()
	case "3":
		// Filter: No branch
		a.branchFilter = FilterNoBranch
		return a, a.loadConversationsAsync()
	}
	return a, nil
}

// handleConversationsScroll handles mouse wheel scrolling in conversations list
func (a *App) handleConversationsScroll(msg tea.MouseWheelMsg) {
	listHeight := a.height
	if a.twoPaneMode {
		listHeight, _ = historyStackHeights(a.height)
	}
	availableHeight := max(1, listHeight-3)

	var totalLines, scrollAmount int

	if a.compactConversationView || a.twoPaneMode {
		// For compact view, calculate total lines including groups
		groups := a.groupConversations(a.conversations)
		totalLines = 0
		for i, group := range groups {
			if i > 0 {
				totalLines++ // Empty line between groups
			}
			totalLines++ // Group header
			totalLines += len(group.Conversations)
		}
		scrollAmount = 3 // Scroll by 3 lines in compact mode
	} else {
		// For detailed view
		itemsPerConv := 4 // Must match renderConversationList
		totalLines = len(a.conversations) * itemsPerConv
		scrollAmount = itemsPerConv
	}

	maxScroll := totalLines - availableHeight
	if maxScroll < 0 {
		maxScroll = 0
	}

	// Scroll based on wheel direction
	direction := msg.String()
	if direction == "wheeldown" {
		a.scrollOffset += scrollAmount
		if a.scrollOffset > maxScroll {
			a.scrollOffset = maxScroll
		}
		// Lazy loading: check if we need to load more conversations when scrolling down
		a.checkAndLoadNextPage()
	} else if direction == "wheelup" {
		a.scrollOffset -= scrollAmount
		if a.scrollOffset < 0 {
			a.scrollOffset = 0
		}
	}
}

// ============================================================================
// CONVERSATIONS SCREEN VIEW
// ============================================================================

func (a *App) viewConversations() string {
	// Render filter tabs at top
	filterTabs := a.renderBranchFilterTabs()

	// Calculate split layout dimensions
	listWidth := 35 // Compact list on the left
	if a.width < 100 {
		listWidth = 25 // Even more compact for smaller screens
	}
	previewWidth := a.width - listWidth // Right side for preview

	// Build conversation list (left side)
	listContent := a.renderConversationList(listWidth)

	// Build preview pane (right side) - renders actual messages
	previewContent := a.renderConversationPreview(previewWidth)

	// Combine side by side
	mainContent := lipgloss.JoinHorizontal(
		lipgloss.Top,
		listContent,
		previewContent,
	)

	// Combine tabs + main content
	combined := lipgloss.JoinVertical(
		lipgloss.Left,
		filterTabs,
		mainContent,
	)

	return combined
}

// renderBranchFilterTabs renders the branch filter tab bar
func (a *App) renderBranchFilterTabs() string {
	th := a.theme

	// Get current branch for display
	currentBranch := ""
	if a.gitHelper != nil && a.gitHelper.IsRepo() {
		currentBranch, _ = a.gitHelper.CurrentBranch()
	}

	// Tab definitions
	type filterTab struct {
		label  string
		filter BranchFilter
	}
	tabs := []filterTab{
		{tr("classic.conversations.filter_all"), FilterAll},
		{tr("classic.conversations.filter_untracked"), FilterNoBranch},
	}

	// Only show current branch tab if we're in a git repo
	if currentBranch != "" {
		// Insert after "All"
		branchLabel := currentBranch
		if len(branchLabel) > 15 {
			branchLabel = branchLabel[:12] + "..."
		}
		tabs = append([]filterTab{tabs[0], {branchLabel, FilterCurrentBranch}}, tabs[1:]...)
	}

	// Render tabs
	var tabsRendered []string
	for i, tab := range tabs {
		tabStyle := lipgloss.NewStyle().
			Padding(0, 2)

		if tab.filter == a.branchFilter {
			tabStyle = tabStyle.
				Foreground(lipgloss.Color(th.Primary)).
				Bold(true).
				Underline(true)
		} else {
			tabStyle = tabStyle.
				Foreground(lipgloss.Color(th.TextMuted))
		}

		// Add shortcut hint
		shortcut := fmt.Sprintf("[%d]", i+1)
		tabText := fmt.Sprintf("%s %s", shortcut, tab.label)
		tabsRendered = append(tabsRendered, tabStyle.Render(tabText))
	}

	// Join tabs horizontally with separators
	separator := lipgloss.NewStyle().
		Foreground(lipgloss.Color(th.Border)).
		Render(" | ")

	// Use strings.Builder to avoid repeated allocations
	var result strings.Builder
	for i, tab := range tabsRendered {
		if i > 0 {
			result.WriteString(separator)
		}
		result.WriteString(tab)
	}

	// Wrap in container
	containerStyle := lipgloss.NewStyle().
		Width(a.width).
		Padding(0, 2).
		BorderBottom(true).
		BorderStyle(lipgloss.NormalBorder()).
		BorderForeground(lipgloss.Color(th.Border))

	return containerStyle.Render(result.String())
}

// unfinishedMarkerGlyph is the compact indicator shown in the conversation
// history menu for sessions that did NOT end in a natural terminal stop (see
// conversation.Conversation.IsUnfinished). It is rendered in the theme's
// warning color and occupies the existing status-icon column, so finished rows
// stay visually unchanged and perfectly aligned.
const unfinishedMarkerGlyph = "◍"

// unfinishedConvCache memoizes the unfinished status of conversations by ID.
// A past conversation's finished/unfinished state is stable while the history
// menu is open, so caching avoids re-resuming conversations from storage on
// every render frame (rendering happens on the UI goroutine).
var (
	unfinishedConvMu    sync.RWMutex
	unfinishedConvCache = map[string]bool{}
)

// conversationUnfinished reports whether the conversation with the given ID did
// NOT reach a natural terminal stop, by delegating to the SDK's
// conversation.Conversation.IsUnfinished(). It is deliberately defensive: any
// missing dependency (nil app, empty ID, nil SDK integration, degraded client,
// or a conversation that cannot be resolved) yields false so the history menu
// never panics or blocks rendering.
//
// NOTE: IsUnfinished is provided by a parallel worker on the SDK side (see
// /tmp/unfin-flow/unfin/CONTRACT.md). Until that method lands, this file will
// not compile solely because of the undefined conv.IsUnfinished reference.
func (a *App) conversationUnfinished(id string) bool {
	if a == nil || id == "" || a.sdk == nil || a.sdk.SDKClient() == nil {
		return false
	}

	unfinishedConvMu.RLock()
	cached, ok := unfinishedConvCache[id]
	unfinishedConvMu.RUnlock()
	if ok {
		return cached
	}

	conv := a.sdk.GetConversation(context.Background(), id)
	if conv == nil {
		return false
	}
	unfinished := conv.IsUnfinished()

	unfinishedConvMu.Lock()
	unfinishedConvCache[id] = unfinished
	unfinishedConvMu.Unlock()
	return unfinished
}

// renderConversationList renders the compact scrollable conversation list
func (a *App) renderConversationList(width int) string {
	th := a.theme

	// Header with count and running indicator - improved styling
	runningCount := 0
	if a.bgManager != nil {
		runningCount = a.bgManager.Count()
	}

	// Add view mode hint to header
	viewModeHint := ""
	if a.compactConversationView {
		viewModeHint = tr("classic.conversations.compact_badge")
	}

	headerText := tr("classic.conversations.header", len(a.conversations), viewModeHint)
	if runningCount > 0 {
		headerText = tr("classic.conversations.header_active", len(a.conversations), viewModeHint, runningCount)
	}
	// Brief legend for the unfinished marker; the header style truncates safely
	// on narrow terminals via its Width constraint.
	headerText += tr("classic.conversations.unfinished", unfinishedMarkerGlyph)

	// Header with subtle background and left accent
	headerStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(th.Primary)).
		Background(lipgloss.Color(th.BGLight)).
		Bold(true).
		Width(width-4).
		Padding(0, 1).
		Align(lipgloss.Left)

	header := headerStyle.Render(headerText)

	// Styled divider with gradient effect
	dividerStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(th.Primary))
	divider := dividerStyle.Render("━" + strings.Repeat("─", width-6) + "━")

	// Calculate visible area
	headerHeight := 3 // title + divider + spacing
	footerHeight := 3 // hints (2 lines)
	availableHeight := a.height - headerHeight - footerHeight

	var items []string

	if a.compactConversationView {
		// COMPACT VIEW: 1 line per conversation with time grouping
		groups := a.groupConversations(a.conversations)

		// Calculate total lines needed for compact view
		totalLines := 0
		for i, group := range groups {
			if i > 0 {
				totalLines++ // Empty line between groups
			}
			totalLines++ // Group header
			totalLines += len(group.Conversations)
		}

		// Find which groups and conversations are visible
		currentLine := 0
		globalIdx := 0

		for groupIdx, group := range groups {
			// Add empty line before group (except first)
			if groupIdx > 0 {
				if currentLine >= a.scrollOffset && currentLine < a.scrollOffset+availableHeight {
					items = append(items, "")
				}
				currentLine++
			}

			// Group header
			if currentLine >= a.scrollOffset && currentLine < a.scrollOffset+availableHeight {
				// Add count to group header like sidecar: "Today (5)"
				groupHeader := tr("classic.conversations.group_count", localizedConversationGroup(group.Label), len(group.Conversations))
				if len(groupHeader) > width-8 {
					groupHeader = groupHeader[:width-8]
				}

				groupHeaderStyle := lipgloss.NewStyle().
					Foreground(lipgloss.Color(th.Primary)).
					Bold(true).
					Padding(0, 1)
				items = append(items, groupHeaderStyle.Render(groupHeader))
			}
			currentLine++

			// Conversations in this group
			for _, conv := range group.Conversations {
				if currentLine >= a.scrollOffset && currentLine < a.scrollOffset+availableHeight {
					selected := globalIdx == a.selectedIdx
					compactRow := a.renderCompactConversation(conv, selected, width-6)
					items = append(items, "  "+compactRow) // Indent under group
				}
				currentLine++
				globalIdx++

			}

			// Stop if we've rendered enough
			if currentLine >= a.scrollOffset+availableHeight {
				break
			}
		}
	} else {
		// DETAILED VIEW: 4 lines per conversation (original implementation)
		itemsPerConv := 4 // Title, preview, metadata, blank line

		// PERFORMANCE: Only render visible conversations (viewport-based rendering)
		// Calculate which conversations are visible based on scroll offset
		visibleStartLine := a.scrollOffset
		visibleEndLine := a.scrollOffset + availableHeight

		// Convert line offsets to conversation indices
		firstVisibleConv := visibleStartLine / itemsPerConv
		lastVisibleConv := (visibleEndLine + itemsPerConv - 1) / itemsPerConv // Round up

		// Clamp to valid range
		if firstVisibleConv < 0 {
			firstVisibleConv = 0
		}
		if lastVisibleConv > len(a.conversations) {
			lastVisibleConv = len(a.conversations)
		}

		// Pre-allocate items slice for visible conversations only
		visibleConvCount := lastVisibleConv - firstVisibleConv
		items = make([]string, 0, visibleConvCount*itemsPerConv)

		// Only render visible conversations (not all of them!)
		for i := firstVisibleConv; i < lastVisibleConv; i++ {
			conv := a.conversations[i]
			selected := i == a.selectedIdx

			// Determine status icon and color
			statusIcon := "○"
			statusColor := th.TextMuted
			isRunningInBg := a.bgManager != nil && a.bgManager.IsRunning(conv.ID)
			if isRunningInBg {
				statusIcon = "●"
				statusColor = th.Warning
			} else if conv.Status == "thinking" || conv.Status == "waiting" {
				statusIcon = "◐"
				statusColor = th.Warning
			} else if conv.IsActive {
				statusIcon = "●"
				statusColor = th.Success
			} else if a.conversationUnfinished(conv.ID) {
				// Idle session that did not reach a natural terminal stop.
				statusIcon = unfinishedMarkerGlyph
				statusColor = th.Warning
			}

			// Improved styling with left accent border and subtle backgrounds
			bgColor := th.BG
			accentBar := "  "
			if selected {
				bgColor = th.BGLight
				accentBar = lipgloss.NewStyle().
					Foreground(lipgloss.Color(th.Primary)).
					Render("┃ ")
			}

			statusStr := lipgloss.NewStyle().
				Foreground(lipgloss.Color(statusColor)).
				Render(statusIcon)

			// Truncate title
			title := conv.Title
			maxTitleLen := width - 10 // Account for accent bar
			if len(title) > maxTitleLen && maxTitleLen > 3 {
				title = title[:maxTitleLen-3] + "..."
			}

			// === Title Line with left accent ===
			var titleLine string
			if selected {
				titleStyle := lipgloss.NewStyle().
					Background(lipgloss.Color(bgColor)).
					Bold(true).
					Width(width - 6)
				titleLine = accentBar + titleStyle.Render(statusStr+" "+title)
			} else {
				titleStyle := lipgloss.NewStyle().
					Foreground(lipgloss.Color(th.TextDim)).
					Width(width - 6)
				titleLine = accentBar + titleStyle.Render(statusStr+" "+title)
			}

			// === Preview Line with consistent accent ===
			preview := conv.Preview
			maxPreviewLen := width - 10
			if len(preview) > maxPreviewLen && maxPreviewLen > 3 {
				preview = preview[:maxPreviewLen-3]
				if lastSpace := strings.LastIndex(preview, " "); lastSpace > maxPreviewLen/2 {
					preview = preview[:lastSpace]
				}
				preview = strings.TrimRight(preview, " .,;:") + "..."
			}
			if preview == "" {
				preview = tr("classic.conversations.no_messages_yet")
			}

			// Use continuation bar for selected items
			contBar := "  "
			if selected {
				contBar = lipgloss.NewStyle().
					Foreground(lipgloss.Color(th.Primary)).
					Render("┃ ")
			}

			previewStyle := lipgloss.NewStyle().
				Foreground(lipgloss.Color(th.TextMuted)).
				Width(width - 6)
			if selected {
				previewStyle = previewStyle.Background(lipgloss.Color(bgColor))
			}
			previewLine := contBar + previewStyle.Render("  "+preview)

			// === Metadata Line with consistent accent ===
			tokenInfo := ""
			if conv.TotalTokens > 0 {
				if conv.TotalTokens >= 1000 {
					tokenInfo = fmt.Sprintf("%.1fk tok", float64(conv.TotalTokens)/1000.0)
				} else {
					tokenInfo = fmt.Sprintf("%d tok", conv.TotalTokens)
				}
			}

			timeStr := timeAgo(conv.LastMessage)
			msgStr := tr("classic.conversations.message_count", conv.MessageCount)

			// Branch badge
			branchBadge := ""
			if conv.Branch != "" {
				branchName := conv.Branch
				runes := []rune(branchName)
				if len(runes) > 12 {
					branchName = string(runes[:9]) + "..."
				}
				branchBadge = lipgloss.NewStyle().
					Foreground(lipgloss.Color(th.Success)).
					Render("[" + branchName + "]")
			}

			var metaContent string
			if tokenInfo != "" {
				if branchBadge != "" {
					metaContent = fmt.Sprintf("  %s %s • %s • %s", branchBadge, tokenInfo, timeStr, msgStr)
				} else {
					metaContent = fmt.Sprintf("  %s • %s • %s", tokenInfo, timeStr, msgStr)
				}
			} else {
				if branchBadge != "" {
					metaContent = fmt.Sprintf("  %s %s • %s", branchBadge, timeStr, msgStr)
				} else {
					metaContent = fmt.Sprintf("  %s • %s", timeStr, msgStr)
				}
			}

			metaStyle := lipgloss.NewStyle().
				Foreground(lipgloss.Color(th.TextMuted)).
				Width(width - 6)
			if selected {
				metaStyle = metaStyle.Background(lipgloss.Color(bgColor))
			}
			metaLine := contBar + metaStyle.Render(metaContent)

			// === Separator Line with subtle divider ===
			var separatorLine string
			if selected {
				// Bottom of selected item - accent bar with background
				sepStyle := lipgloss.NewStyle().
					Foreground(lipgloss.Color(th.Primary)).
					Background(lipgloss.Color(bgColor)).
					Width(width - 6)
				separatorLine = lipgloss.NewStyle().
					Foreground(lipgloss.Color(th.Primary)).
					Render("┗") + sepStyle.Render(strings.Repeat("━", width-7))
			} else {
				// Subtle separator between unselected items
				sepStyle := lipgloss.NewStyle().
					Foreground(lipgloss.Color(th.Border)).
					Width(width - 4)
				separatorLine = sepStyle.Render("  " + strings.Repeat("─", width-8))
			}

			items = append(items, titleLine)
			items = append(items, previewLine)
			items = append(items, metaLine)
			items = append(items, separatorLine)

		}
	} // End of detailed view

	// Handle empty state
	if len(a.conversations) == 0 {
		emptyMsg := lipgloss.NewStyle().
			Foreground(lipgloss.Color(th.TextMuted)).
			Italic(true).
			Width(width - 4).
			Align(lipgloss.Center).
			Render(tr("classic.conversations.empty"))
		items = append(items, emptyMsg)
	}

	// Add "↓ N more" pagination indicator for compact view
	if a.compactConversationView {
		// Calculate total lines in compact view
		groups := a.groupConversations(a.conversations)
		totalCompactLines := 0
		for i, group := range groups {
			if i > 0 {
				totalCompactLines++
			}
			totalCompactLines++
			totalCompactLines += len(group.Conversations)
		}

		// If there are more items below the visible area
		remainingLines := totalCompactLines - (a.scrollOffset + availableHeight)
		if remainingLines > 0 {
			moreIndicator := lipgloss.NewStyle().
				Foreground(lipgloss.Color(th.TextMuted)).
				Italic(true).
				Render(tr("classic.common.more_down", remainingLines))
			items = append(items, moreIndicator)
		}
	}

	// PERFORMANCE: Items already contains only visible conversations
	// For compact view, items are already correctly sliced during rendering
	visibleItems := items

	// Limit to available height
	if len(visibleItems) > availableHeight {
		visibleItems = visibleItems[:availableHeight]
	}

	// Pad to fill height
	for len(visibleItems) < availableHeight {
		visibleItems = append(visibleItems, strings.Repeat(" ", width-4))
	}

	// Use strings.Builder for efficient joining (Crush technique)
	var sb strings.Builder
	sb.Grow(len(visibleItems) * (width + 1)) // Pre-allocate
	for i, item := range visibleItems {
		if i > 0 {
			sb.WriteByte('\n')
		}
		sb.WriteString(item)
	}
	listBody := sb.String()

	// Calculate scrollbar parameters
	var totalLines int
	if a.compactConversationView {
		// For compact view, count all lines including group headers
		groups := a.groupConversations(a.conversations)
		totalLines = 0
		for i, group := range groups {
			if i > 0 {
				totalLines++ // Empty line
			}
			totalLines++ // Group header
			totalLines += len(group.Conversations)
		}
	} else {
		// For detailed view
		totalLines = len(a.conversations) * 4 // 4 lines per conversation
	}

	// Render scrollbar
	scrollbarStr := ""
	if totalLines > availableHeight {
		scrollbarStyle := lipgloss.NewStyle().
			Foreground(lipgloss.Color(th.Border))
		scrollbarContent := renderScrollbar(totalLines, a.scrollOffset, availableHeight, availableHeight)
		scrollbarStr = scrollbarStyle.Render(scrollbarContent)

		// Join list body with scrollbar
		listBodyLines := strings.Split(listBody, "\n")
		scrollbarLines := strings.Split(scrollbarStr, "\n")

		var combined []string
		for i := range listBodyLines {
			line := listBodyLines[i]
			// Ensure line is padded to full width minus scrollbar
			if len(line) < width-6 {
				line = line + strings.Repeat(" ", width-6-len(line))
			}
			if i < len(scrollbarLines) {
				combined = append(combined, line+" "+scrollbarLines[i])
			} else {
				combined = append(combined, line)
			}
		}
		listBody = strings.Join(combined, "\n")
	}

	// Footer hints with improved styling
	hintsText := tr("classic.conversations.hint")
	hintsStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(th.TextMuted)).
		Background(lipgloss.Color(th.BGLight)).
		Width(width-4).
		Padding(0, 1).
		Align(lipgloss.Center)
	hints := hintsStyle.Render(hintsText)

	// Combine sections
	content := lipgloss.JoinVertical(
		lipgloss.Left,
		header,
		divider,
		listBody,
		hints,
	)

	// Rounded border with accent color
	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color(th.Primary)).
		Width(width).
		Height(a.height).
		Render(content)
}

// renderConversationPreview renders actual chat messages from the selected conversation
// Uses Sidecar-style rendering for consistent look across all views.
func (a *App) renderConversationPreview(width int) string {
	th := a.theme

	if a.selectedIdx < 0 || a.selectedIdx >= len(a.conversations) {
		emptyStyle := lipgloss.NewStyle().
			Foreground(lipgloss.Color(th.TextMuted)).
			Italic(true).
			Width(width).
			Height(a.height).
			Align(lipgloss.Center, lipgloss.Center)
		return emptyStyle.Render(tr("classic.conversations.select"))
	}

	// Use Sidecar-style main pane rendering
	contentWidth := width - 4
	if contentWidth < 20 {
		contentWidth = 20
	}
	contentHeight := a.height - 4 // padding
	if contentHeight < 1 {
		contentHeight = 1
	}

	content := a.renderSidecarMainPane(contentWidth, contentHeight)

	// Wrap in box with padding
	return lipgloss.NewStyle().
		Width(width).
		Height(a.height).
		Padding(1, 2).
		Render(content)
}

// ============================================================================
// COMPACT CONVERSATION RENDERING
// ============================================================================

// renderCompactConversation renders a single conversation in 1-line format
// Format: [●] [icon] [branch] Title...                    12m  45k
func (a *App) renderCompactConversation(conv Conversation, selected bool, width int) string {
	th := a.theme

	// Get adapter badge/icon text for width calculations
	badgeText := a.getConversationIcon(conv)

	// Branch badge with rune-safe truncation
	branchBadge := ""
	if conv.Branch != "" {
		branchName := conv.Branch
		runes := []rune(branchName)
		if len(runes) > 12 {
			branchName = string(runes[:9]) + "..."
		}
		branchBadge = "[" + branchName + "]"
	}

	// Format duration - using time ago format
	timeStr := ""
	if !conv.LastMessage.IsZero() {
		timeStr = formatConversationTime(conv.LastMessage)
	}

	// Format token count - only if we have data
	tokenStr := ""
	if conv.TotalTokens > 0 {
		tokenStr = formatCompactTokens(conv.TotalTokens)
	}

	// Calculate right column width (only for columns that have data)
	rightColWidth := 0
	if timeStr != "" {
		rightColWidth += len(timeStr)
	}
	if tokenStr != "" {
		if rightColWidth > 0 {
			rightColWidth += 2 // spaces between columns
		}
		rightColWidth += len(tokenStr)
	}

	// Calculate prefix length for width calculations
	// status(1) + space + badge + space + branch (if present) + space
	prefixLen := 1 + 1 + len(badgeText) + 1
	if branchBadge != "" {
		prefixLen += len(branchBadge) + 1
	}
	// Add right column width plus spacing if present
	if rightColWidth > 0 {
		prefixLen += rightColWidth + 2 // space before stats
	}

	// Title/name
	title := conv.Title
	if title == "" {
		title = tr("classic.conversations.untitled")
	}

	// Calculate available width for title (rune-safe)
	titleWidth := width - prefixLen
	if titleWidth < 5 {
		titleWidth = 5
	}

	// Truncate title to fit (rune-safe for Unicode)
	if runes := []rune(title); len(runes) > titleWidth {
		title = string(runes[:titleWidth-3]) + "..."
	}

	// Calculate padding for right-aligned stats
	visibleLen := 1 + 1 + len(badgeText) + 1 + len(title) // status + space + badge + space + title
	if branchBadge != "" {
		visibleLen += len(branchBadge) + 1
	}
	padding := width - visibleLen - rightColWidth - 1
	if padding < 0 {
		padding = 0
	}

	// Build the row with styling
	var sb strings.Builder

	// Activity indicator with colors
	statusIcon := " "
	statusColor := th.TextMuted
	isRunningInBg := a.bgManager != nil && a.bgManager.IsRunning(conv.ID)
	if isRunningInBg {
		statusIcon = "●"
		statusColor = th.Warning
	} else if conv.Status == "thinking" || conv.Status == "waiting" {
		statusIcon = "◐"
		statusColor = th.Warning
	} else if conv.IsActive {
		statusIcon = "●"
		statusColor = th.Success
	} else if a.conversationUnfinished(conv.ID) {
		// Idle session that did not reach a natural terminal stop.
		statusIcon = unfinishedMarkerGlyph
		statusColor = th.Warning
	}

	sb.WriteString(lipgloss.NewStyle().
		Foreground(lipgloss.Color(statusColor)).
		Render(statusIcon))
	sb.WriteString(" ")

	// Colored adapter icon
	sb.WriteString(a.renderConversationIcon(conv))
	sb.WriteString(" ")

	// Branch badge with color
	if branchBadge != "" {
		sb.WriteString(lipgloss.NewStyle().
			Foreground(lipgloss.Color(th.Success)).
			Render(branchBadge))
		sb.WriteString(" ")
	}

	// Title with appropriate styling
	titleStyle := lipgloss.NewStyle()
	if selected {
		titleStyle = titleStyle.Bold(true)
	} else {
		titleStyle = titleStyle.Foreground(lipgloss.Color(th.TextDim))
	}
	sb.WriteString(titleStyle.Render(title))

	// Padding and right-aligned stats (only if we have data)
	if rightColWidth > 0 && padding > 0 {
		sb.WriteString(strings.Repeat(" ", padding))
		sb.WriteString(" ")

		// Muted style for stats
		statsStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(th.TextMuted))

		if timeStr != "" {
			sb.WriteString(statsStyle.Render(timeStr))
		}
		if tokenStr != "" {
			if timeStr != "" {
				sb.WriteString("  ")
			}
			sb.WriteString(lipgloss.NewStyle().
				Foreground(lipgloss.Color(th.TextMuted)).
				Render(tokenStr))
		}
	}

	row := sb.String()

	// For selected rows, apply background highlight
	if selected {
		return lipgloss.NewStyle().
			Background(lipgloss.Color(th.BGLight)).
			Width(width).
			Render(row)
	}

	return row
}

// getConversationIcon returns a plain text icon/badge for the conversation type based on provider
func (a *App) getConversationIcon(conv Conversation) string {
	// Extract provider from the conversation's model
	if conv.Model != "" {
		provider := extractProviderFromModel(conv.Model)
		icon := getProviderIcon(provider)
		// DEBUG: Log for troubleshooting
		if conv.ID != "" && len(conv.ID) >= 8 {
			logDebug("[getConversationIcon] conv=%s model=%s provider=%s icon=%s", conv.ID[:8], conv.Model, provider, icon)
		}
		return icon
	}
	// Default icon if no model info
	return "◆"
}

// renderConversationIcon returns a colorized icon for the conversation based on provider
func (a *App) renderConversationIcon(conv Conversation) string {
	th := a.theme
	icon := a.getConversationIcon(conv)

	// Get provider-specific color from loaded configs
	var color string
	if conv.Model != "" {
		provider := extractProviderFromModel(conv.Model)
		fgColor := getProviderColor(a.providerConfigs, provider)

		// Use provider color if active, muted if not
		if conv.IsActive {
			color = fgColor
		} else {
			color = th.TextMuted
		}
	} else {
		// No model info - use theme colors
		if conv.IsActive {
			color = th.Primary
		} else {
			color = th.TextMuted
		}
	}

	return lipgloss.NewStyle().
		Foreground(lipgloss.Color(color)).
		Render(icon)
}

// formatConversationTime formats time ago in compact format (12m, 3h, 2d)
func formatConversationTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}

	d := time.Since(t)
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	if d < time.Hour {
		return fmt.Sprintf("%dm", int(d.Minutes()))
	}
	if d < 24*time.Hour {
		h := int(d.Hours())
		return fmt.Sprintf("%dh", h)
	}
	days := int(d.Hours() / 24)
	if days == 1 {
		return "1d"
	}
	if days < 30 {
		return fmt.Sprintf("%dd", days)
	}
	months := days / 30
	if months == 1 {
		return "1mo"
	}
	if months < 12 {
		return fmt.Sprintf("%dmo", months)
	}
	years := months / 12
	return fmt.Sprintf("%dy", years)
}

// formatCompactTokens formats token counts compactly (45k, 1.2M) - delegates to formatUsageTokenCount.
func formatCompactTokens(n int) string {
	return formatUsageTokenCount(int64(n))
}

// getSessionGroup returns the time group label for a given timestamp
func getSessionGroup(t time.Time) string {
	now := time.Now()
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	yesterday := today.AddDate(0, 0, -1)
	weekAgo := today.AddDate(0, 0, -7)

	switch {
	case t.After(today) || t.Equal(today):
		return "Today"
	case t.After(yesterday) || t.Equal(yesterday):
		return "Yesterday"
	case t.After(weekAgo):
		return "This Week"
	default:
		return "Older"
	}
}

func localizedConversationGroup(label string) string {
	switch label {
	case "Today":
		return tr("classic.conversations.today")
	case "Yesterday":
		return tr("classic.conversations.yesterday")
	case "This Week":
		return tr("classic.conversations.this_week")
	case "Older":
		return tr("classic.conversations.older")
	default:
		return label
	}
}

// normalizeBranchName converts a raw branch ref to a display-friendly name
// Handles: empty string -> "Untracked", HEAD -> "HEAD", origin/main -> "origin/main"
func normalizeBranchName(branch string) string {
	if branch == "" {
		return "Untracked"
	}
	return branch
}

// getLocalBranchName extracts the local branch name from a potentially remote-prefixed branch
// e.g., "origin/main" -> "main", "upstream/feature" -> "feature", "main" -> "main"
func getLocalBranchName(branch string) string {
	if branch == "" || branch == "Untracked" || branch == "HEAD" {
		return branch
	}
	// Split on "/" and take the last part for remote branches
	parts := strings.Split(branch, "/")
	if len(parts) > 1 {
		return parts[len(parts)-1]
	}
	return branch
}

// ConversationSubGroup represents a subgroup of conversations (e.g. by branch)
type ConversationSubGroup struct {
	Label         string // Display label (e.g. "main [Current]")
	RawName       string // Raw branch name (e.g. "main")
	Conversations []Conversation
}

// ConversationGroup represents a group of conversations by time period
type ConversationGroup struct {
	Label         string
	Conversations []Conversation         // Flattened list for backward compatibility
	SubGroups     []ConversationSubGroup // Structured list grouped by branch
}

// groupConversations groups conversations by time periods and then by branch
func (a *App) groupConversations(conversations []Conversation) []ConversationGroup {
	// Get current git branch for prioritization and labeling
	currentGitBranch := ""
	if a.gitHelper != nil && a.gitHelper.IsRepo() {
		currentGitBranch, _ = a.gitHelper.CurrentBranch()
	}

	// Group by time period first
	timeGroups := make(map[string][]Conversation)
	for _, conv := range conversations {
		group := getSessionGroup(conv.LastMessage)
		timeGroups[group] = append(timeGroups[group], conv)
	}

	// Order: Today, Yesterday, This Week, Older
	orderedTimeGroups := []string{"Today", "Yesterday", "This Week", "Older"}
	result := []ConversationGroup{}

	for _, timeLabel := range orderedTimeGroups {
		convs, ok := timeGroups[timeLabel]
		if !ok || len(convs) == 0 {
			continue
		}

		// Group by branch within this time group
		branchMap := make(map[string][]Conversation)
		var branches []string

		for _, conv := range convs {
			branch := normalizeBranchName(conv.Branch)
			if _, exists := branchMap[branch]; !exists {
				branches = append(branches, branch)
			}
			branchMap[branch] = append(branchMap[branch], conv)
		}

		// Sort branches: current branch first, then main/master, then remotes (origin/main), then others, No Branch/HEAD last
		sort.Slice(branches, func(i, j int) bool {
			b1, b2 := branches[i], branches[j]

			// Current git branch always first
			if currentGitBranch != "" {
				if b1 == currentGitBranch && b2 != currentGitBranch {
					return true
				}
				if b1 != currentGitBranch && b2 == currentGitBranch {
					return false
				}
			}

			// Handle Untracked last
			if b1 == "Untracked" && b2 != "Untracked" {
				return false
			}
			if b1 != "Untracked" && b2 == "Untracked" {
				return true
			}

			// Handle HEAD second to last
			if b1 == "HEAD" && b2 != "HEAD" && b2 != "Untracked" {
				return false
			}
			if b1 != "HEAD" && b1 != "Untracked" && b2 == "HEAD" {
				return true
			}

			// Handle main/master priority (including origin/main)
			localB1 := getLocalBranchName(b1)
			localB2 := getLocalBranchName(b2)
			isMain1 := localB1 == "main" || localB1 == "master"
			isMain2 := localB2 == "main" || localB2 == "master"

			if isMain1 && !isMain2 {
				return true
			}
			if !isMain1 && isMain2 {
				return false
			}

			// Local branches before remote branches
			isRemote1 := strings.Contains(b1, "/")
			isRemote2 := strings.Contains(b2, "/")
			if !isRemote1 && isRemote2 {
				return true
			}
			if isRemote1 && !isRemote2 {
				return false
			}

			return strings.ToLower(b1) < strings.ToLower(b2)
		})

		var subGroups []ConversationSubGroup
		for _, b := range branches {
			label := b
			if currentGitBranch != "" && b == currentGitBranch {
				label = b + " [Current]"
			}
			subGroups = append(subGroups, ConversationSubGroup{
				Label:         label,
				RawName:       b,
				Conversations: branchMap[b],
			})
		}

		result = append(result, ConversationGroup{
			Label:         timeLabel,
			Conversations: convs, // Flattened list for backward compatibility
			SubGroups:     subGroups,
		})
	}

	return result
}

// getCompactLineForConversation calculates which display line a conversation appears on in compact view
func (a *App) getCompactLineForConversation(convIdx int) int {
	groups := a.groupConversations(a.conversations)
	line := 0
	globalIdx := 0

	for groupIdx, group := range groups {
		// Add empty line before group (except first)
		if groupIdx > 0 {
			line++
		}
		// Group header (Time)
		line++

		// Subgroups (Branches) — only count branch header if multiple branches
		showBranch := len(group.SubGroups) > 1
		for _, subGroup := range group.SubGroups {
			if showBranch {
				line++ // Branch header
			}

			for range subGroup.Conversations {
				if globalIdx == convIdx {
					return line
				}
				line++
				globalIdx++
			}
		}
	}

	return line
}

// renderScrollbar renders a visual scrollbar for the conversation list
func renderScrollbar(totalItems, scrollOffset, visibleItems, height int) string {
	if totalItems <= visibleItems {
		return strings.Repeat(" ", height) // No scrollbar needed
	}

	// Calculate thumb size and position
	thumbHeight := int(float64(visibleItems) / float64(totalItems) * float64(height))
	if thumbHeight < 1 {
		thumbHeight = 1
	}

	thumbPos := int(float64(scrollOffset) / float64(totalItems-visibleItems) * float64(height-thumbHeight))
	if thumbPos < 0 {
		thumbPos = 0
	}
	if thumbPos > height-thumbHeight {
		thumbPos = height - thumbHeight
	}

	// Build scrollbar
	var sb strings.Builder
	for i := range height {
		if i >= thumbPos && i < thumbPos+thumbHeight {
			sb.WriteString("█") // Thumb
		} else {
			sb.WriteString("│") // Track
		}
		if i < height-1 {
			sb.WriteString("\n")
		}
	}

	return sb.String()
}
