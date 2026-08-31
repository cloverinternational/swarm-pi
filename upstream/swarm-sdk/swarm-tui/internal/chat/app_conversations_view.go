package chat

// app_conversations_view.go
// High-quality conversation history rendering matching Sidecar reference quality.
// Renders: sidebar session list + main pane message preview.

import (
	"context"
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/conversation/manager"
)

// ============================================================================
// MAIN PANE — conversation detail with header + unified message renderer
// ============================================================================

func (a *App) renderSidecarMainPane(contentWidth, height int) string {
	th := a.theme

	// While conversations are loading, show a loading indicator instead of
	// attempting to render stale data (which would trigger synchronous
	// message loading via getOrLoadPreviewMessages and block the UI).
	if a.conversationsLoading {
		spinFrames := []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}
		frame := 0
		if a.animationClock != nil {
			frame = a.animationClock.Frame() % len(spinFrames)
		}
		loadingStyle := lipgloss.NewStyle().
			Width(contentWidth).
			Padding(2, 2)
		return loadingStyle.Render(
			lipgloss.NewStyle().Foreground(lipgloss.Color(th.Primary)).Render(spinFrames[frame]) +
				" " +
				lipgloss.NewStyle().Foreground(lipgloss.Color(th.TextMuted)).Render(tr("classic.conversations.loading")))
	}

	if a.selectedIdx < 0 || a.selectedIdx >= len(a.conversations) {
		emptyStyle := lipgloss.NewStyle().
			Foreground(lipgloss.Color(th.TextMuted)).
			Width(contentWidth).
			Padding(2, 2)
		return emptyStyle.Render(tr("classic.conversations.none_selected_horizontal"))
	}

	conv := a.conversations[a.selectedIdx]
	var sb strings.Builder

	// REMOVED: Title line and header section for minimal UI
	// ── Line 1: icon + title + model badges (all on one line) ──
	// titleLine := a.renderMainPaneTitleLine(conv, contentWidth)
	// sb.WriteString(titleLine)
	// sb.WriteString("\n")

	// ── Line 2: stats bar — msgs │ in:Xk out:Xk │ $cost │ tools │ date ──
	// statsLine := a.renderMainPaneStatsLine(conv, contentWidth)
	// if statsLine != "" {
	//	sb.WriteString(statsLine)
	//	sb.WriteString("\n")
	// }

	// ── Line 3 (conditional): lineage — fork/compaction origin ──
	// lineageLine := a.renderMainPaneLineage(conv, contentWidth)
	// if lineageLine != "" {
	//	sb.WriteString(lineageLine)
	//	sb.WriteString("\n")
	// }

	// ── Thin separator ──
	// sepW := contentWidth
	// if sepW > 80 {
	//	sepW = 80
	// }
	// sb.WriteString(lipgloss.NewStyle().
	//	Foreground(lipgloss.Color(th.Border)).
	//	Render(strings.Repeat("─", sepW)))
	// sb.WriteString("\n")

	// Calculate remaining height for messages
	headerLines := 0 // No header lines since we removed the title/stats/separator
	contentHeight := height - headerLines
	if contentHeight < 1 {
		contentHeight = 1
	}

	// Load messages (cached)
	messages := a.getOrLoadPreviewMessages(conv.ID)

	if a.messagesLoading {
		spinFrames := []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}
		frame := 0
		if a.animationClock != nil {
			frame = a.animationClock.Frame() % len(spinFrames)
		}
		sb.WriteString(lipgloss.NewStyle().Foreground(lipgloss.Color(th.Primary)).Render(spinFrames[frame]))
		sb.WriteString(" ")
		sb.WriteString(lipgloss.NewStyle().Foreground(lipgloss.Color(th.TextMuted)).Render(tr("classic.conversations.loading_messages")))
		return sb.String()
	}

	if len(messages) == 0 {
		sb.WriteString(lipgloss.NewStyle().
			Foreground(lipgloss.Color(th.TextMuted)).
			Render(tr("classic.conversations.no_messages_available")))
		return sb.String()
	}

	// Use unified message renderer for consistent tool call rendering
	previewCtx := a.NewPreviewMessageContext(contentWidth)
	msgLines := a.renderMessageListWithContext(messages, previewCtx)

	// Scroll logic
	totalLines := len(msgLines)
	maxScroll := totalLines - contentHeight
	if maxScroll < 0 {
		maxScroll = 0
	}
	if a.messageScrollOffset > maxScroll {
		a.messageScrollOffset = maxScroll
	}
	if a.messageScrollOffset < 0 {
		a.messageScrollOffset = 0
	}

	start := a.messageScrollOffset
	end := start + contentHeight
	if end > totalLines {
		end = totalLines
	}
	if start >= totalLines {
		return sb.String()
	}

	for _, line := range msgLines[start:end] {
		sb.WriteString(line)
		sb.WriteString("\n")
	}

	// Scroll position indicator (bottom-right)
	if totalLines > contentHeight {
		pct := 0
		if maxScroll > 0 {
			pct = a.messageScrollOffset * 100 / maxScroll
		}
		scrollInfo := fmt.Sprintf("─── %d%% (%d/%d) ───", pct, end, totalLines)
		sb.WriteString(lipgloss.NewStyle().
			Foreground(lipgloss.Color(th.TextMuted)).
			Render(scrollInfo))
	}

	return sb.String()
}

// renderConversationSummaryPane renders the right-hand panel of the consolidated
// history menu (Layout C): the LLM-generated recap of the selected conversation
// plus a compact stats block. It intentionally does NOT render raw messages —
// the goal is "why was this chat", answerable at a glance.
func (a *App) renderConversationSummaryPane(contentWidth, height int) string {
	th := a.theme

	// Every style sets an explicit background so the theme color fills the pane
	// (see AGENTS.md "Background Color Rendering").
	base := lipgloss.NewStyle().Background(lipgloss.Color(th.BG))

	if a.conversationsLoading {
		return base.Foreground(lipgloss.Color(th.TextMuted)).
			Width(contentWidth).Padding(1, 2).Render(tr("classic.conversations.loading"))
	}
	if a.selectedIdx < 0 || a.selectedIdx >= len(a.conversations) {
		return base.Foreground(lipgloss.Color(th.TextMuted)).
			Width(contentWidth).Padding(1, 2).Render(tr("classic.conversations.none_selected_vertical"))
	}

	conv := a.conversations[a.selectedIdx]
	var sb strings.Builder

	titleStyle := base.Foreground(lipgloss.Color(th.Primary)).Bold(true).Width(contentWidth)
	labelStyle := base.Foreground(lipgloss.Color(th.TextMuted))
	bodyStyle := base.Foreground(lipgloss.Color(th.Text)).Width(contentWidth)

	title := conv.Title
	if title == "" {
		title = tr("classic.conversations.untitled")
	}
	sb.WriteString(reapplyBackground(titleStyle.Render(truncateString(title, contentWidth)), th.BG))
	sb.WriteString("\n\n")

	// ── Summary body ──
	sb.WriteString(reapplyBackground(labelStyle.Render(tr("classic.conversations.summary")), th.BG))
	sb.WriteString("\n")
	recap := strings.TrimSpace(conv.Recap)
	if recap == "" {
		// Fallback to the heuristic preview until a recap is generated.
		if p := strings.TrimSpace(conv.Preview); p != "" {
			sb.WriteString(reapplyBackground(bodyStyle.Foreground(lipgloss.Color(th.TextMuted)).
				Render(p), th.BG))
		} else {
			sb.WriteString(reapplyBackground(bodyStyle.Foreground(lipgloss.Color(th.TextMuted)).
				Render(tr("classic.conversations.no_summary")), th.BG))
		}
	} else {
		for _, line := range wordWrapText(recap, contentWidth) {
			sb.WriteString(reapplyBackground(bodyStyle.Render(line), th.BG))
			sb.WriteString("\n")
		}
	}
	sb.WriteString("\n")

	// ── Stats block ──
	sb.WriteString(reapplyBackground(labelStyle.Render(tr("classic.conversations.details")), th.BG))
	sb.WriteString("\n")

	stat := func(label, value string) {
		if value == "" {
			return
		}
		line := labelStyle.Render(label+": ") + bodyStyle.Render(value)
		sb.WriteString(reapplyBackground(line, th.BG))
		sb.WriteString("\n")
	}

	stat(tr("classic.conversations.messages"), fmt.Sprintf("%d", conv.MessageCount))
	if conv.TotalTokens > 0 {
		stat(tr("classic.conversations.tokens"), formatTokenCount(conv.TotalTokens))
	}
	if len(conv.ModelsUsed) > 0 {
		stat(tr("classic.conversations.models"), strings.Join(conv.ModelsUsed, ", "))
	} else if conv.Model != "" {
		stat(tr("classic.conversations.model"), conv.Model)
	}
	if conv.Branch != "" {
		stat(tr("classic.conversations.branch"), conv.Branch)
	}
	if conv.ForkedFrom != "" {
		stat(tr("classic.conversations.forked_from"), conv.ForkedFrom)
	}
	if conv.CompactionCount > 0 {
		stat(tr("classic.conversations.compacted"), fmt.Sprintf("%d×", conv.CompactionCount))
	}
	if !conv.LastMessage.IsZero() {
		stat(tr("classic.conversations.updated"), timeAgo(conv.LastMessage))
	}

	return sb.String()
}

func (a *App) renderSidecarSidebarPane(width, height int) string {
	th := a.theme
	var sb strings.Builder

	sessions := a.conversations

	contentWidth := width - 4
	if contentWidth < 15 {
		contentWidth = 15
	}

	// ── Header: Sessions N  (M active) ──
	countStr := fmt.Sprintf("%d", len(sessions))
	runningCount := 0
	if a.bgManager != nil {
		runningCount = a.bgManager.Count()
	}
	if runningCount > 0 {
		countStr = tr("classic.conversations.count_active", len(sessions), runningCount)
	}
	if a.conversationsLoading {
		spinFrames := []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}
		frame := 0
		if a.animationClock != nil {
			frame = a.animationClock.Frame() % len(spinFrames)
		}
		countStr = spinFrames[frame] + tr("classic.conversations.loading_suffix")
	}

	sb.WriteString(lipgloss.NewStyle().Bold(true).Render(tr("classic.conversations.title")))
	sb.WriteString(" ")
	sb.WriteString(lipgloss.NewStyle().Foreground(lipgloss.Color(th.TextMuted)).Render(countStr))
	sb.WriteString("\n")

	if len(sessions) == 0 {
		sb.WriteString(lipgloss.NewStyle().Foreground(lipgloss.Color(th.TextMuted)).Render(tr("classic.conversations.no_sessions")))
		return sb.String()
	}

	contentHeight := height - 1 // header took 1 line
	if contentHeight < 1 {
		contentHeight = 1
	}

	// Reserve 1 col for scrollbar
	sessionWidth := contentWidth - 1
	if sessionWidth < 15 {
		sessionWidth = 15
	}

	var sessionSB strings.Builder
	groups := a.groupConversations(sessions)
	a.renderSidecarGroupedSessions(&sessionSB, groups, contentHeight, sessionWidth)

	// Count total lines for scrollbar
	totalLines := 0
	for i, group := range groups {
		if i > 0 {
			totalLines++
		}
		totalLines++ // time group header
		if len(group.SubGroups) > 1 {
			totalLines += len(group.SubGroups) // branch headers
		}
		totalLines += len(group.Conversations)
	}

	// "↓ N more" indicator
	remaining := totalLines - (a.scrollOffset + contentHeight)
	if remaining > 0 {
		sessionSB.WriteString(lipgloss.NewStyle().
			Foreground(lipgloss.Color(th.TextMuted)).
			Italic(true).
			Render(tr("classic.common.more_down", remaining)))
		sessionSB.WriteString("\n")
	}

	sessionContent := strings.TrimRight(sessionSB.String(), "\n")

	// Scrollbar
	scrollbarContent := renderScrollbar(totalLines, a.scrollOffset, contentHeight, contentHeight)
	scrollbar := lipgloss.NewStyle().
		Foreground(lipgloss.Color(th.Border)).
		Render(scrollbarContent)

	sb.WriteString(lipgloss.JoinHorizontal(lipgloss.Top, sessionContent, scrollbar))

	return sb.String()
}

// renderSidecarGroupedSessions renders sessions grouped by time period and branch.
// Hierarchy: Time -> Branch -> Conversations (with fork/compaction children)
func (a *App) renderSidecarGroupedSessions(sb *strings.Builder, groups []ConversationGroup, contentHeight, contentWidth int) {
	th := a.theme

	if a.collapsedParents == nil {
		a.collapsedParents = make(map[string]bool)
	}

	// Build parent→children map for fork/compaction relationships
	childrenOf := make(map[string][]int) // parent conv ID -> global indices
	isChild := make(map[int]bool)        // global index -> is it a child?
	parentConvID := make(map[int]string) // global index -> parent conv ID

	// First pass: flatten all conversations with branch info and assign global indices
	type flatEntry struct {
		conv      Conversation
		timeGroup string
		branch    string
		idx       int
	}
	var allEntries []flatEntry
	globalIdx := 0

	for _, group := range groups {
		for _, subGroup := range group.SubGroups {
			for _, conv := range subGroup.Conversations {
				allEntries = append(allEntries, flatEntry{
					conv:      conv,
					timeGroup: group.Label,
					branch:    subGroup.Label,
					idx:       globalIdx,
				})
				globalIdx++
			}
		}
	}

	// Build convID→globalIdx lookup
	convIDToIdx := make(map[string]int)
	for _, e := range allEntries {
		convIDToIdx[e.conv.ID] = e.idx
	}

	// Mark children (forks/compactions)
	for _, e := range allEntries {
		parentID := e.conv.ForkedFrom
		if parentID == "" {
			parentID = e.conv.CompactedFrom
		}
		if parentID == "" {
			continue
		}
		if _, parentExists := convIDToIdx[parentID]; parentExists {
			childrenOf[parentID] = append(childrenOf[parentID], e.idx)
			isChild[e.idx] = true
			parentConvID[e.idx] = parentID
		}
	}

	// Build visible entries with branch awareness
	type visEntry struct {
		entry         flatEntry
		indent        int    // 0 = branch level, 1 = child (fork/compaction)
		treeChar      string // "├─" or "└─" for fork children
		isBranch      bool   // true = this is a branch header line
		branchLabel   string // Display label (e.g. "main [Current]")
		branchRawName string // Raw branch name (e.g. "main")
	}
	var visible []visEntry

	// Track which branch we're in within each time group
	for _, group := range groups {
		// Only show branch headers when the time group has multiple branches
		showBranchHeaders := len(group.SubGroups) > 1

		for _, subGroup := range group.SubGroups {
			if showBranchHeaders {
				// Add branch header entry only when multiple branches exist
				visible = append(visible, visEntry{
					isBranch:      true,
					branchLabel:   subGroup.Label,
					branchRawName: subGroup.RawName,
					entry:         flatEntry{timeGroup: group.Label},
				})
			}

			// Add conversations in this branch
			for _, conv := range subGroup.Conversations {
				// Find the flatEntry for this conv
				var entry flatEntry
				for _, e := range allEntries {
					if e.conv.ID == conv.ID {
						entry = e
						break
					}
				}

				if isChild[entry.idx] {
					continue // Children rendered after parent
				}

				// Top-level conversation in this branch
				visible = append(visible, visEntry{entry: entry, indent: 0})

				// Add fork/compaction children if not collapsed
				children := childrenOf[conv.ID]
				if len(children) > 0 && !a.collapsedParents[conv.ID] {
					for ci, childIdx := range children {
						childEntry := allEntries[childIdx]
						tc := "├─"
						if ci == len(children)-1 {
							tc = "└─"
						}
						visible = append(visible, visEntry{entry: childEntry, indent: 1, treeChar: tc})
					}
				}
			}
		}
	}

	// Build a set of time groups that show branch headers
	branchHeaderGroups := make(map[string]bool)
	for _, group := range groups {
		if len(group.SubGroups) > 1 {
			branchHeaderGroups[group.Label] = true
		}
	}

	// Render with scroll offset
	lineCount := 0
	currentTimeGroup := ""

	for i := a.scrollOffset; i < len(visible) && lineCount < contentHeight; i++ {
		v := visible[i]

		// Time group header on change
		if v.entry.timeGroup != currentTimeGroup {
			if currentTimeGroup != "" {
				sb.WriteString("\n")
				lineCount++
				if lineCount >= contentHeight {
					break
				}
			}
			currentTimeGroup = v.entry.timeGroup

			// Count total in this time group
			groupCount := 0
			for _, g := range groups {
				if g.Label == currentTimeGroup {
					groupCount = len(g.Conversations)
					break
				}
			}

			header := tr("classic.conversations.group_count", localizedConversationGroup(currentTimeGroup), groupCount)
			if len(header) > contentWidth {
				header = header[:contentWidth]
			}
			sb.WriteString(lipgloss.NewStyle().
				Foreground(lipgloss.Color(th.Warning)).
				Bold(true).
				Render(header))
			sb.WriteString("\n")
			lineCount++
			if lineCount >= contentHeight {
				break
			}
		}

		// Branch header
		if v.isBranch {
			branchStyle := lipgloss.NewStyle().
				Bold(true)

			icon := "⎇" // Git branch icon
			displayName := v.branchLabel

			// Handle special cases based on raw name
			if v.branchRawName == "Untracked" {
				branchStyle = lipgloss.NewStyle().
					Foreground(lipgloss.Color(th.TextMuted)).
					Italic(true)
				icon = "◌"
				displayName = tr("classic.conversations.filter_untracked")
			} else if v.branchRawName == "HEAD" {
				// Detached HEAD state
				branchStyle = lipgloss.NewStyle().
					Foreground(lipgloss.Color(th.Warning)).
					Bold(true)
				icon = "⚠"
				displayName = strings.Replace(displayName, "HEAD", tr("classic.conversations.detached_head"), 1)
			} else if strings.Contains(v.branchRawName, "/") {
				// Remote branch like origin/main, upstream/feature
				parts := strings.Split(v.branchRawName, "/")
				if len(parts) >= 2 {
					remote := parts[0]
					branch := strings.Join(parts[1:], "/")

					// Rebuild display name with remote in muted color
					currentSuffix := ""
					if strings.HasSuffix(v.branchLabel, " [Current]") {
						currentSuffix = tr("classic.conversations.current_suffix")
					}

					displayName = lipgloss.NewStyle().
						Foreground(lipgloss.Color(th.TextMuted)).
						Render(remote+": ") + branch + currentSuffix
					icon = "↗" // Remote icon
				}
			}

			// Count conversations in this branch using raw name
			branchCount := 0
			for _, g := range groups {
				if g.Label == currentTimeGroup {
					for _, sg := range g.SubGroups {
						if sg.RawName == v.branchRawName {
							branchCount = len(sg.Conversations)
							break
						}
					}
					break
				}
			}

			branchHeader := fmt.Sprintf("  %s %s (%d)", icon, displayName, branchCount)
			if lipgloss.Width(branchHeader) > contentWidth {
				// Truncate display name if needed
				branchHeader = fmt.Sprintf("  %s %s (%d)", icon, v.branchLabel, branchCount)
				if len(branchHeader) > contentWidth {
					branchHeader = branchHeader[:contentWidth-3] + "..."
				}
			}
			sb.WriteString(branchStyle.Render(branchHeader))
			sb.WriteString("\n")
			lineCount++
			continue
		}

		// Conversation row
		e := v.entry
		selected := e.idx == a.selectedIdx

		if v.indent == 0 {
			// Top-level conversation — deeper indent when under branch headers
			hasBranch := branchHeaderGroups[currentTimeGroup]
			indent := "  "
			indentW := 2
			if hasBranch {
				indent = "    "
				indentW = 4
			}

			children := childrenOf[e.conv.ID]
			forkCount := len(children)
			collapsed := a.collapsedParents[e.conv.ID]

			row := a.renderSidecarSessionRow(e.conv, selected, contentWidth-indentW)

			// Append fork count indicator
			if forkCount > 0 {
				var indicator string
				if collapsed {
					indicator = fmt.Sprintf(" ▶%d", forkCount)
				} else {
					indicator = fmt.Sprintf(" ▼%d", forkCount)
				}
				if lipgloss.Width(row)+len(indicator) <= contentWidth-indentW {
					if selected {
						row = row + indicator
					} else {
						row = row + lipgloss.NewStyle().
							Foreground(lipgloss.Color(th.TextMuted)).
							Render(indicator)
					}
				}
			}

			sb.WriteString(indent)
			sb.WriteString(row)
		} else {
			// Child row (fork/compaction) — extra indent with tree character
			treePrefix := lipgloss.NewStyle().
				Foreground(lipgloss.Color(th.Border)).
				Render("      " + v.treeChar + " ")
			treePrefixW := 9 // "      ├─ " = 9 chars

			childRow := a.renderSidecarSessionRow(e.conv, selected, contentWidth-treePrefixW)
			sb.WriteString(treePrefix + childRow)
		}
		sb.WriteString("\n")

		lineCount++
	}
}

// ============================================================================
// SESSION ROW — single compact line per session
// Format: [●] [◆] [↳⟲] [⎇branch] Title...              opus  12m  45k
// ============================================================================

func (a *App) renderSidecarSessionRow(conv Conversation, selected bool, maxWidth int) string {
	th := a.theme

	// Build parts with their visible widths
	type part struct {
		text  string
		width int
	}
	var leftParts []part
	var rightParts []part

	// Activity indicator (1 char)
	isRunning := a.bgManager != nil && a.bgManager.IsRunning(conv.ID)
	if isRunning {
		leftParts = append(leftParts, part{
			text:  lipgloss.NewStyle().Foreground(lipgloss.Color(th.Warning)).Render("●"),
			width: 1,
		})
	} else if conv.IsActive {
		leftParts = append(leftParts, part{
			text:  lipgloss.NewStyle().Foreground(lipgloss.Color(th.Success)).Render("●"),
			width: 1,
		})
	} else {
		leftParts = append(leftParts, part{text: " ", width: 1})
	}

	// Lineage indicator (1 char) — fork or compaction
	if conv.ForkedFrom != "" {
		leftParts = append(leftParts, part{
			text:  lipgloss.NewStyle().Foreground(lipgloss.Color(th.Info)).Render("↳"),
			width: 1,
		})
	} else if conv.CompactedFrom != "" {
		leftParts = append(leftParts, part{
			text:  lipgloss.NewStyle().Foreground(lipgloss.Color("#C084FC")).Render("⟲"),
			width: 1,
		})
	} else {
		leftParts = append(leftParts, part{
			text:  lipgloss.NewStyle().Foreground(lipgloss.Color(th.Warning)).Render("◆"),
			width: 1,
		})
	}
	leftParts = append(leftParts, part{text: " ", width: 1})

	// Right side: model badge │ time │ tokens
	if conv.Model != "" {
		short := getModelShortName(conv.Model)
		if short != "" {
			rightParts = append(rightParts, part{text: " ", width: 1})
			if selected {
				rightParts = append(rightParts, part{text: short, width: len(short)})
			} else {
				badge := a.sidecarRenderModelBadgeCompact(conv.Model)
				rightParts = append(rightParts, part{text: badge, width: len(short)})
			}
		}
	}
	if !conv.LastMessage.IsZero() {
		t := formatConversationTime(conv.LastMessage)
		rightParts = append(rightParts, part{text: " ", width: 1})
		if selected {
			rightParts = append(rightParts, part{text: t, width: len(t)})
		} else {
			rightParts = append(rightParts, part{
				text:  lipgloss.NewStyle().Foreground(lipgloss.Color(th.TextMuted)).Render(t),
				width: len(t),
			})
		}
	}
	if conv.TotalTokens > 0 {
		tok := sidecarFormatK(conv.TotalTokens)
		rightParts = append(rightParts, part{text: " ", width: 1})
		if selected {
			rightParts = append(rightParts, part{text: tok, width: len(tok)})
		} else {
			rightParts = append(rightParts, part{
				text:  lipgloss.NewStyle().Foreground(lipgloss.Color(th.TextMuted)).Render(tok),
				width: len(tok),
			})
		}
	}

	// Calculate widths
	leftW := 0
	for _, p := range leftParts {
		leftW += p.width
	}
	rightW := 0
	for _, p := range rightParts {
		rightW += p.width
	}

	// Session name
	name := conv.Title
	if name == "" {
		name = shortSessionID(conv.ID)
	}
	nameMaxW := maxWidth - leftW - rightW - 1
	if nameMaxW < 5 {
		nameMaxW = 5
	}
	if runes := []rune(name); len(runes) > nameMaxW {
		name = string(runes[:nameMaxW-3]) + "..."
	}
	nameW := len([]rune(name))

	// Padding between name and right parts
	pad := maxWidth - leftW - nameW - rightW
	if pad < 0 {
		pad = 0
	}

	if selected {
		// Selected: plain text with background highlight
		var plain strings.Builder
		for _, p := range leftParts {
			plain.WriteString(stripANSI(p.text))
		}
		plain.WriteString(name)
		if pad > 0 {
			plain.WriteString(strings.Repeat(" ", pad))
		}
		for _, p := range rightParts {
			plain.WriteString(stripANSI(p.text))
		}
		row := plain.String()
		if runeLen := len([]rune(row)); runeLen < maxWidth {
			row += strings.Repeat(" ", maxWidth-runeLen)
		}
		return lipgloss.NewStyle().
			Foreground(lipgloss.Color(th.Text)).
			Background(lipgloss.Color(th.BGLight)).
			Render(row)
	}

	// Normal row with colors
	var sb strings.Builder
	for _, p := range leftParts {
		sb.WriteString(p.text)
	}
	sb.WriteString(lipgloss.NewStyle().Foreground(lipgloss.Color(th.TextDim)).Render(name))
	if pad > 0 {
		sb.WriteString(strings.Repeat(" ", pad))
	}
	for _, p := range rightParts {
		sb.WriteString(p.text)
	}
	return sb.String()
}

// ============================================================================
// UTILITY — model badges, formatting, message loading
// ============================================================================

// sidecarFormatK formats a number with K/M suffix.
func sidecarFormatK(n int) string {
	if n >= 1000000 {
		return fmt.Sprintf("%.1fM", float64(n)/1000000)
	}
	if n >= 1000 {
		return fmt.Sprintf("%.1fk", float64(n)/1000)
	}
	return fmt.Sprintf("%d", n)
}

// sidecarRenderModelBadgeCompact renders a colored badge without background (for lists) using provider configs
func (a *App) sidecarRenderModelBadgeCompact(model string) string {
	short := getModelShortName(model)
	if short == "" {
		return ""
	}

	// Get provider-based colors from loaded configs
	provider := extractProviderFromModel(model)
	fgColor := getProviderColor(a.providerConfigs, provider)

	// Special case: Claude model variants
	if provider == "anthropic" {
		lowerModel := strings.ToLower(model)
		if strings.Contains(lowerModel, "opus") {
			fgColor = "#C084FC"
		} else if strings.Contains(lowerModel, "sonnet") {
			fgColor = "#86EFAC"
		} else if strings.Contains(lowerModel, "haiku") {
			fgColor = "#93C5FD"
		}
	}

	return lipgloss.NewStyle().Foreground(lipgloss.Color(fgColor)).Render(short)
}

func shortSessionID(id string) string {
	if len(id) > 12 {
		return id[:12] + "..."
	}
	return id
}

// ============================================================================
// MESSAGE LOADING — lazy load + cache
// ============================================================================

func (a *App) getOrLoadPreviewMessages(convID string) []Message {
	if a.cachedPreviewID == convID && a.cachedPreviewMsgs != nil {
		return a.cachedPreviewMsgs
	}
	messages := a.loadSidecarMessages(convID)
	a.cachedPreviewID = convID
	a.cachedPreviewMsgs = messages
	return messages
}

func (a *App) loadSidecarMessages(convID string) []Message {
	if a.sdk == nil {
		return nil
	}

	ctx := context.Background()

	// Load before pagination so generated compaction context can be removed
	// without displacing real turns from the ten-message preview.
	sdkMsgs, err := a.sdk.GetMessagesWithOptions(ctx, convID, manager.GetMessagesOptions{})
	if err != nil || len(sdkMsgs) == 0 {
		return nil
	}
	visible := sdkMsgs[:0]
	for _, sdkMsg := range sdkMsgs {
		if !isGeneratedCompactionMessage(sdkMsg) {
			visible = append(visible, sdkMsg)
		}
	}
	sdkMsgs = visible

	// Messages are in chronological order; take the last 10
	startIdx := 0
	if len(sdkMsgs) > 10 {
		startIdx = len(sdkMsgs) - 10
	}
	sdkMsgs = sdkMsgs[startIdx:]

	var messages []Message
	for _, sdkMsg := range sdkMsgs {
		msg := Message{
			Role:      string(sdkMsg.Role),
			Content:   sdkMsg.Content,
			Timestamp: sdkMsg.Timestamp,
			Model:     sdkMsg.Model,
		}

		if sdkMsg.Tokens != nil {
			msg.InputTokens = sdkMsg.Tokens.Input
			msg.OutputTokens = sdkMsg.Tokens.Output
		}

		if sdkMsg.Thinking != "" {
			msg.Thinking = sdkMsg.Thinking
		} else if sdkMsg.Metadata != nil {
			if thinking, ok := sdkMsg.Metadata["thinking"].(string); ok {
				msg.Thinking = thinking
			}
		}

		if len(sdkMsg.ToolCalls) > 0 {
			msg.ToolCalls = make([]ToolCallDisplay, len(sdkMsg.ToolCalls))
			for i, tc := range sdkMsg.ToolCalls {
				msg.ToolCalls[i] = ToolCallDisplay{
					ID:         tc.ID,
					Name:       tc.Name,
					Parameters: tc.Parameters,
				}
			}
		}

		if len(sdkMsg.ToolResults) > 0 {
			msg.ToolResults = make([]ToolResultDisplay, len(sdkMsg.ToolResults))
			for i, tr := range sdkMsg.ToolResults {
				errStr := ""
				if tr.Error != nil {
					errStr = tr.Error.Message
				}
				msg.ToolResults[i] = ToolResultDisplay{
					CallID: tr.CallID,
					Output: tr.Output,
					Error:  errStr,
				}
			}
		}

		messages = append(messages, msg)
	}

	return messages
}
