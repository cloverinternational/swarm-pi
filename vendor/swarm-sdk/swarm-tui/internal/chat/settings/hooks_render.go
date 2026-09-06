package settings

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/i18n"
)

func (h *HooksSettings) Render(width, height int, state *State, th Theme) string {
	var rendered string
	if width < 40 || height < 20 {
		rendered = h.renderMinimal(width, height, th)
		return i18n.SettingsIntegrationsText(rendered)
	}

	switch state.HooksState {
	case "templates":
		rendered = h.renderTemplates(width, height, state, th)
	case "chat":
		rendered = h.renderChat(width, height, state, th)
	case "action_menu":
		rendered = h.renderActionMenu(width, height, state, th)
	case "edit":
		rendered = h.renderEditForm(width, height, state, th)
	default:
		rendered = h.renderMainScreen(width, height, state, th)
	}
	return i18n.SettingsIntegrationsText(rendered)
}

// View renders the hooks settings UI (for compatibility with manager.go)
func (h *HooksSettings) View(width, height int) string {
	return ""
}

// renderMinimal renders a minimal view for small terminals
func (h *HooksSettings) renderMinimal(width, height int, th Theme) string {
	msg := i18n.T("settings.integrations.common.terminal_too_small") + "\n" +
		i18n.T("settings.integrations.common.resize_40x20")
	return lipgloss.NewStyle().
		Width(width).
		Height(height).
		Foreground(lipgloss.Color(th.TextMuted)).
		Align(lipgloss.Center, lipgloss.Center).
		Render(msg)
}

// renderMainScreen renders the main hooks view with list + detail panel layout
func (h *HooksSettings) renderMainScreen(width, height int, state *State, th Theme) string {
	const (
		borderWidth      = 2
		containerPadding = 1
		titleHeight      = 4
		hintHeight       = 2
	)

	innerWidth := maxInt(20, width-(borderWidth*2)-(containerPadding*2))
	innerHeight := maxInt(5, height-(borderWidth*2)-(containerPadding*2)-titleHeight-hintHeight)

	// Render sections
	title := h.renderTitle(innerWidth, th)
	content := h.renderHooksListAndDetail(innerWidth, innerHeight, state, th)
	hints := h.renderHintBar(innerWidth, state, th)

	// Combine sections
	fullContent := lipgloss.JoinVertical(lipgloss.Left,
		title,
		content,
		hints,
	)

	// Apply container border
	containerStyle := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color(th.Border)).
		Width(maxInt(20, width-2)).
		Padding(0, containerPadding)

	return containerStyle.Render(fullContent)
}

// renderTitle renders the section title with stats badges
func (h *HooksSettings) renderTitle(width int, th Theme) string {
	titleStyle := lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color(th.Primary)).
		Width(width).
		Align(lipgloss.Center)

	title := titleStyle.Render(i18n.T("settings.integrations.hooks.configuration"))

	// Count hooks by source and status
	systemCount, userCount, projectCount, enabledCount := 0, 0, 0, 0
	for _, hook := range h.allHooks {
		switch hook.Source {
		case "system":
			systemCount++
		case "user":
			userCount++
		case "project":
			projectCount++
		}
		if hook.Enabled {
			enabledCount++
		}
	}

	// Build status badges
	enabledBadge := lipgloss.NewStyle().
		Background(lipgloss.Color(th.Success)).
		Foreground(lipgloss.Color(th.Text)).
		Padding(0, 1).
		Bold(true).
		Render(i18n.T("settings.residual.common.enabled_count", enabledCount))

	disabledBadge := lipgloss.NewStyle().
		Background(lipgloss.Color(th.TextMuted)).
		Foreground(lipgloss.Color(th.Text)).
		Padding(0, 1).
		Render(i18n.T("settings.residual.common.total_count", len(h.allHooks)))

	// Source breakdown badge
	sourceText := i18n.T("settings.residual.hooks.source_counts", systemCount, userCount, projectCount)
	sourceBadge := lipgloss.NewStyle().
		Background(lipgloss.Color(th.BGLight)).
		Foreground(lipgloss.Color(th.Text)).
		Padding(0, 1).
		Render(sourceText)

	badgesRow := lipgloss.JoinHorizontal(lipgloss.Center, enabledBadge, " ", disabledBadge, " ", sourceBadge)
	centeredBadges := lipgloss.NewStyle().Width(width).Align(lipgloss.Center).Render(badgesRow)

	return lipgloss.JoinVertical(lipgloss.Left, title, "", centeredBadges)
}

// renderHooksListAndDetail renders the two-panel layout: list on left, details on right
func (h *HooksSettings) renderHooksListAndDetail(width, height int, state *State, th Theme) string {
	// Ensure selection is valid
	if state.HooksSelected < 0 {
		state.HooksSelected = 0
	}
	if state.HooksSelected >= len(h.allHooks) && len(h.allHooks) > 0 {
		state.HooksSelected = len(h.allHooks) - 1
	}

	// Collapse to single-pane at narrow widths
	if width < 60 {
		return h.renderHooksList(maxInt(15, width), height, state, th)
	}

	// Two-panel layout: list (50%) + details (50%)
	listWidth := (width * 50) / 100
	detailsWidth := maxInt(15, width-listWidth-3) // -3 for separator

	// Render both panels
	listPanel := h.renderHooksList(listWidth, height, state, th)
	detailsPanel := h.renderHookDetails(detailsWidth, height, state, th)

	// Separator
	sepH := maxInt(1, height-2)
	separatorLines := make([]string, sepH)
	for i := range separatorLines {
		separatorLines[i] = "│"
	}
	separator := lipgloss.NewStyle().
		Foreground(lipgloss.Color(th.Border)).
		Render(strings.Join(separatorLines, "\n"))

	return lipgloss.JoinHorizontal(lipgloss.Top, listPanel, separator, detailsPanel)
}

// renderHooksList renders the left panel with hook list
func (h *HooksSettings) renderHooksList(width, height int, state *State, th Theme) string {
	lw := maxInt(8, width-2)
	titleStyle := lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color(th.Primary)).
		Width(lw)

	title := titleStyle.Render(i18n.T("settings.residual.hooks.count", len(h.allHooks)))

	// Calculate visible area
	visibleHeight := maxInt(3, height-4)

	// Adjust scroll
	scrollOffset := state.HooksScrollOffsetGlobal
	if scrollOffset > state.HooksSelected {
		scrollOffset = state.HooksSelected
		state.HooksScrollOffsetGlobal = scrollOffset
	}
	if state.HooksSelected >= scrollOffset+visibleHeight {
		scrollOffset = state.HooksSelected - visibleHeight + 1
		state.HooksScrollOffsetGlobal = scrollOffset
	}

	var lines []string
	lines = append(lines, title, "")

	if len(h.allHooks) == 0 {
		emptyStyle := lipgloss.NewStyle().
			Foreground(lipgloss.Color(th.TextMuted)).
			Italic(true).
			Width(lw)
		lines = append(lines, emptyStyle.Render(i18n.T("settings.integrations.hooks.no_hooks")))
		lines = append(lines, emptyStyle.Render(i18n.T("settings.integrations.hooks.create_hint")))
	} else {
		startIdx := scrollOffset
		endIdx := startIdx + visibleHeight
		if endIdx > len(h.allHooks) {
			endIdx = len(h.allHooks)
		}

		for i := startIdx; i < endIdx; i++ {
			hook := h.allHooks[i]
			isSelected := i == state.HooksSelected
			lines = append(lines, h.renderHookListItem(hook, maxInt(8, width-4), isSelected, th))
		}

		// Scroll indicator
		if len(h.allHooks) > visibleHeight {
			scrollInfo := i18n.T("settings.residual.common.range_of_total", startIdx+1, endIdx, len(h.allHooks))
			scrollStyle := lipgloss.NewStyle().
				Foreground(lipgloss.Color(th.TextMuted)).
				Width(lw).
				Align(lipgloss.Center)
			lines = append(lines, "", scrollStyle.Render(scrollInfo))
		}
	}

	return lipgloss.JoinVertical(lipgloss.Left, lines...)
}

// renderHookListItem renders a single hook in the list
func (h *HooksSettings) renderHookListItem(hook HookEntry, width int, isSelected bool, th Theme) string {
	// Status indicator
	var statusIcon string
	var statusColor string
	if hook.Enabled {
		statusIcon = "●"
		statusColor = th.Success
	} else {
		statusIcon = "○"
		statusColor = th.TextMuted
	}

	// Source badge (3 chars fixed)
	var sourceBadge string
	var badgeColor string
	switch hook.Source {
	case "system":
		sourceBadge = "SYS"
		badgeColor = th.Warning
	case "user":
		sourceBadge = "USR"
		badgeColor = th.Primary
	case "project":
		sourceBadge = "PRJ"
		badgeColor = th.Success
	default:
		sourceBadge = "???"
		badgeColor = th.TextMuted
	}

	// Event type tag
	eventTag := ""
	if len(hook.Events) > 0 {
		eventTag = shortenEvent(hook.Events[0])
	}

	// Available width for name
	availableForName := width - 12 // status(2) + badge(4) + event(6)
	if availableForName < 8 {
		availableForName = 8
	}

	// Truncate name
	name := hook.Name
	if len(name) > availableForName {
		name = name[:availableForName-1] + "…"
	}

	// Build line
	statusStyled := lipgloss.NewStyle().
		Foreground(lipgloss.Color(statusColor)).
		Bold(true).
		Render(statusIcon)

	badgeStyled := lipgloss.NewStyle().
		Foreground(lipgloss.Color(badgeColor)).
		Bold(true).
		Render(sourceBadge)

	nameColor := th.Text
	if isSelected {
		nameColor = th.Primary
	}
	nameStyled := lipgloss.NewStyle().
		Foreground(lipgloss.Color(nameColor)).
		Bold(isSelected).
		Render(name)

	eventStyled := lipgloss.NewStyle().
		Foreground(lipgloss.Color(th.TextMuted)).
		Italic(true).
		Render("[" + eventTag + "]")

	line := fmt.Sprintf(" %s %s %s %s", statusStyled, badgeStyled, nameStyled, eventStyled)

	if isSelected {
		return lipgloss.NewStyle().
			Background(lipgloss.Color(th.BGLighter)).
			Width(width).
			Render(line)
	}
	return line
}

// renderHookDetails renders the right panel with selected hook details
func (h *HooksSettings) renderHookDetails(width, height int, state *State, th Theme) string {
	hdw := maxInt(8, width-2)
	if len(h.allHooks) == 0 || state.HooksSelected >= len(h.allHooks) {
		emptyStyle := lipgloss.NewStyle().
			Foreground(lipgloss.Color(th.TextMuted)).
			Italic(true).
			Width(hdw).
			Align(lipgloss.Center)
		return lipgloss.JoinVertical(lipgloss.Left,
			"",
			emptyStyle.Render(i18n.T("settings.integrations.hooks.select_details")),
			"",
			emptyStyle.Render(i18n.T("settings.integrations.hooks.or_create")),
		)
	}

	hook := h.allHooks[state.HooksSelected]

	titleStyle := lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color(th.Primary)).
		Width(hdw)

	labelStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(th.TextMuted)).
		Width(hdw)

	valueStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(th.Text)).
		Width(hdw)

	var lines []string

	// Hook name
	lines = append(lines, titleStyle.Render(hook.Name))
	lines = append(lines, "")

	// Status badge
	var statusBadge string
	if hook.Enabled {
		badgeStyle := lipgloss.NewStyle().
			Background(lipgloss.Color(th.Success)).
			Foreground(lipgloss.Color(th.Text)).
			Padding(0, 1).
			Bold(true)
		statusBadge = badgeStyle.Render(i18n.T("settings.residual.common.enabled_badge"))
	} else {
		badgeStyle := lipgloss.NewStyle().
			Background(lipgloss.Color(th.TextMuted)).
			Foreground(lipgloss.Color(th.Text)).
			Padding(0, 1)
		statusBadge = badgeStyle.Render(i18n.T("settings.residual.common.disabled_badge"))
	}

	// Source badge
	var sourceBadge string
	switch hook.Source {
	case "system":
		badgeStyle := lipgloss.NewStyle().
			Background(lipgloss.Color(th.Warning)).
			Foreground(lipgloss.Color(th.Text)).
			Padding(0, 1).
			Bold(true)
		sourceBadge = badgeStyle.Render(i18n.T("settings.integrations.hooks.system"))
	case "user":
		badgeStyle := lipgloss.NewStyle().
			Background(lipgloss.Color(th.Primary)).
			Foreground(lipgloss.Color(th.Text)).
			Padding(0, 1).
			Bold(true)
		sourceBadge = badgeStyle.Render(i18n.T("settings.integrations.hooks.user"))
	case "project":
		badgeStyle := lipgloss.NewStyle().
			Background(lipgloss.Color(th.Success)).
			Foreground(lipgloss.Color(th.Text)).
			Padding(0, 1).
			Bold(true)
		sourceBadge = badgeStyle.Render(i18n.T("settings.integrations.hooks.project"))
	}

	lines = append(lines, lipgloss.JoinHorizontal(lipgloss.Left, statusBadge, " ", sourceBadge))
	lines = append(lines, "")

	// Description
	if hook.Description != "" {
		lines = append(lines, labelStyle.Render(i18n.T("settings.integrations.common.description")))
		// Wrap description
		wrapped := wrapTextSimple(hook.Description, width-4)
		for _, line := range wrapped {
			lines = append(lines, valueStyle.Render("  "+line))
		}
		lines = append(lines, "")
	}

	// Events
	lines = append(lines, labelStyle.Render(i18n.T("settings.integrations.hooks.events")))
	for _, event := range hook.Events {
		lines = append(lines, valueStyle.Render("  • "+event))
	}
	lines = append(lines, "")

	// Tool Matcher
	if hook.ToolMatcher != "" {
		lines = append(lines, labelStyle.Render(i18n.T("settings.integrations.hooks.tool_matcher")))
		lines = append(lines, valueStyle.Render("  "+hook.ToolMatcher))
		lines = append(lines, "")
	}

	// Action & Timeout
	lines = append(lines, labelStyle.Render(i18n.T("settings.integrations.hooks.action_timeout")))
	lines = append(lines, valueStyle.Render(fmt.Sprintf("  %s / %s", hook.Action, hook.Timeout)))
	lines = append(lines, "")

	// Priority
	lines = append(lines, labelStyle.Render(i18n.T("settings.integrations.common.priority")))
	priorityDesc := i18n.T("settings.residual.hooks.priority.general")
	if hook.Priority >= 99 {
		priorityDesc = i18n.T("settings.residual.hooks.priority.security")
	} else if hook.Priority >= 90 {
		priorityDesc = i18n.T("settings.residual.hooks.priority.logging")
	} else if hook.Priority >= 70 {
		priorityDesc = i18n.T("settings.residual.hooks.priority.important")
	}
	lines = append(lines, valueStyle.Render(fmt.Sprintf("  %d (%s)", hook.Priority, priorityDesc)))
	lines = append(lines, "")

	// Command (truncated)
	lines = append(lines, labelStyle.Render(i18n.T("settings.integrations.hooks.command")))
	cmd := hook.Command
	maxCmdLen := maxInt(6, width-6)
	if len(cmd) > maxCmdLen {
		cmd = cmd[:maxCmdLen-3] + "..."
	}
	cmdStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(th.Warning)).
		Width(maxInt(8, width-4))
	lines = append(lines, cmdStyle.Render("  "+cmd))

	// Actions hint
	lines = append(lines, "")
	hintStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(th.TextMuted)).
		Italic(true).
		Width(hdw)
	if hook.Builtin {
		lines = append(lines, hintStyle.Render(i18n.T("settings.integrations.hooks.system_hint")))
	} else {
		lines = append(lines, hintStyle.Render(i18n.T("settings.integrations.hooks.user_hint")))
	}

	return lipgloss.JoinVertical(lipgloss.Left, lines...)
}

// renderHintBar renders keyboard navigation hints at the bottom
func (h *HooksSettings) renderHintBar(width int, state *State, th Theme) string {
	hintStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(th.TextMuted)).
		Width(width).
		Align(lipgloss.Center)

	keyStyle := lipgloss.NewStyle().
		Background(lipgloss.Color(th.BGLight)).
		Foreground(lipgloss.Color(th.Text)).
		Padding(0, 1).
		Bold(true)

	hints := i18n.T("settings.residual.hooks.main_hints",
		keyStyle.Render("↑/↓"),
		keyStyle.Render("Space"),
		keyStyle.Render("n"),
		keyStyle.Render("t"),
		keyStyle.Render("a"),
		keyStyle.Render("r"))

	return hintStyle.Render(hints)
}

// wrapTextSimple wraps text to specified width
func wrapTextSimple(text string, width int) []string {
	if width <= 0 {
		return []string{text}
	}
	words := strings.Fields(text)
	if len(words) == 0 {
		return nil
	}
	var lines []string
	var currentLine string
	for _, word := range words {
		if len(currentLine) == 0 {
			currentLine = word
		} else if len(currentLine)+1+len(word) <= width {
			currentLine += " " + word
		} else {
			lines = append(lines, currentLine)
			currentLine = word
		}
	}
	if currentLine != "" {
		lines = append(lines, currentLine)
	}
	return lines
}

// shortenEvent shortens event names for display
func shortenEvent(event string) string {
	switch event {
	// Tool events
	case "tool.before_execute", "PreToolUse", "BeforeTool":
		return "pre"
	case "tool.after_execute", "PostToolUse", "AfterTool":
		return "post"
	// Agent events
	case "user.prompt_submit", "UserPromptSubmit", "BeforeAgent":
		return "prompt"
	case "agent.stop", "Stop", "AfterAgent":
		return "stop"
	case "subagent.stop", "SubagentStop":
		return "sub"
	// Session events
	case "session.start", "SessionStart":
		return "start"
	case "session.end", "SessionEnd":
		return "end"
	// Model events
	case "BeforeModel":
		return "b-mdl"
	case "AfterModel":
		return "a-mdl"
	case "BeforeToolSelection":
		return "b-sel"
	// Other events
	case "compact.before", "PreCompact", "PreCompress":
		return "cmpct"
	case "notification", "Notification":
		return "notif"
	default:
		if len(event) > 8 {
			return event[:6] + ".."
		}
		return event
	}
}

// renderChat renders the AI chat interface
// renderTemplates renders the template picker UI
func (h *HooksSettings) renderTemplates(width, height int, state *State, th Theme) string {
	const (
		borderWidth = 2
		padding     = 2
	)

	innerWidth := maxInt(14, width-borderWidth-(padding*2))
	innerHeight := maxInt(5, height-borderWidth-(padding*2))

	// Title
	titleStyle := lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color(th.Primary)).
		Width(innerWidth).
		Align(lipgloss.Center)
	title := titleStyle.Render(i18n.T("settings.integrations.hooks.templates_title"))

	// Help text
	helpStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(th.TextMuted)).
		Width(innerWidth).
		Align(lipgloss.Center)
	help := helpStyle.Render(i18n.T("settings.integrations.hooks.templates_help"))

	// Get built-in hooks as templates
	templates := builtinHooks

	// Calculate visible area
	listHeight := maxInt(3, innerHeight-6)  // Title + help + hints
	visibleCount := maxInt(1, listHeight/3) // Each template takes 3 lines

	// Calculate scroll offset
	scrollOffset := 0
	if state.HooksTemplateIdx >= visibleCount {
		scrollOffset = state.HooksTemplateIdx - visibleCount + 1
	}

	// Render template list
	var templateLines []string
	for i := scrollOffset; i < len(templates) && i < scrollOffset+visibleCount; i++ {
		tmpl := templates[i]
		isSelected := i == state.HooksTemplateIdx

		nameStyle := lipgloss.NewStyle().
			Bold(isSelected).
			Foreground(lipgloss.Color(th.Text)).
			Width(innerWidth)

		descStyle := lipgloss.NewStyle().
			Foreground(lipgloss.Color(th.TextMuted)).
			Width(maxInt(8, innerWidth-4)).
			MarginLeft(minInt(4, innerWidth/5))

		cursor := "  "
		if isSelected {
			cursor = "▶ "
			nameStyle = nameStyle.Foreground(lipgloss.Color(th.Primary))
		}

		name := nameStyle.Render(cursor + tmpl.Name)
		desc := descStyle.Render(tmpl.Description)

		templateLines = append(templateLines, name)
		templateLines = append(templateLines, desc)
		templateLines = append(templateLines, "") // Spacing
	}

	templateList := lipgloss.JoinVertical(lipgloss.Left, templateLines...)

	// Hints
	hintsStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(th.TextMuted)).
		Width(innerWidth).
		Align(lipgloss.Center)
	hints := hintsStyle.Render(i18n.T("settings.residual.hooks.template_hints"))

	// Combine all sections
	content := lipgloss.JoinVertical(lipgloss.Left,
		title,
		"",
		help,
		"",
		templateList,
		"",
		hints,
	)

	// Apply container
	containerStyle := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color(th.Border)).
		Width(maxInt(20, width-2)).
		Padding(0, minInt(padding, width/10))

	return containerStyle.Render(content)
}

// renderChat renders the AI assistant chat interface
func (h *HooksSettings) renderChat(width, height int, state *State, th Theme) string {
	var lines []string

	// Title bar
	chatPad := minInt(2, width/10)
	titleStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(th.Text)).
		Background(lipgloss.Color(th.Primary)).
		Bold(true).
		Width(maxInt(10, width-4)).
		Padding(0, chatPad)
	lines = append(lines, titleStyle.Render(i18n.T("settings.integrations.hooks.ai_title")))
	lines = append(lines, "")

	// Instructions
	instructStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(th.TextMuted)).
		Italic(true).
		Padding(0, chatPad)
	lines = append(lines, instructStyle.Render(i18n.T("settings.integrations.hooks.ai_description")))
	lines = append(lines, instructStyle.Render(i18n.T("settings.integrations.hooks.ai_example")))
	lines = append(lines, instructStyle.Render("          "+i18n.T("settings.integrations.hooks.ai_example_more")))
	lines = append(lines, "")

	// Chat history area
	chatAreaHeight := maxInt(5, height-14)

	chatBgStyle := lipgloss.NewStyle().
		Background(lipgloss.Color(th.BG)).
		Width(maxInt(10, width-6)).
		Height(chatAreaHeight).
		Padding(1).
		Margin(0, minInt(2, width/10)).
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color(th.Border))

	var chatLines []string
	if len(h.chatMessages) == 0 {
		emptyStyle := lipgloss.NewStyle().
			Foreground(lipgloss.Color(th.TextMuted)).
			Italic(true)
		chatLines = append(chatLines, emptyStyle.Render(i18n.T("settings.integrations.hooks.ai_empty")))
	} else {
		// Styles for different elements
		userStyle := lipgloss.NewStyle().
			Foreground(lipgloss.Color(th.Primary)).
			Bold(true)
		assistantHeaderStyle := lipgloss.NewStyle().
			Foreground(lipgloss.Color(th.Success)).
			Bold(true)
		assistantTextStyle := lipgloss.NewStyle().
			Foreground(lipgloss.Color(th.Text))
		toolNameStyle := lipgloss.NewStyle().
			Foreground(lipgloss.Color("#C792EA")). // Purple for tool names
			Bold(true)
		toolSymbolStyle := lipgloss.NewStyle().
			Foreground(lipgloss.Color("#89DDFF")) // Cyan for symbols
		toolInputStyle := lipgloss.NewStyle().
			Foreground(lipgloss.Color("#82AAFF")) // Blue for input JSON
		toolResultStyle := lipgloss.NewStyle().
			Foreground(lipgloss.Color("#C3E88D")) // Green for results
		toolErrorStyle := lipgloss.NewStyle().
			Foreground(lipgloss.Color(th.Error))
		turnsStyle := lipgloss.NewStyle().
			Foreground(lipgloss.Color(th.TextMuted)).
			Italic(true)
		systemStyle := lipgloss.NewStyle().
			Foreground(lipgloss.Color(th.Warning)).
			Italic(true)

		for _, msg := range h.chatMessages {
			switch msg.Role {
			case "user":
				chatLines = append(chatLines, userStyle.Render(i18n.T("settings.residual.hooks.you")))
				wrapped := wordWrapText(msg.Content, width-16)
				for line := range strings.SplitSeq(wrapped, "\n") {
					chatLines = append(chatLines, "  "+line)
				}
				chatLines = append(chatLines, "")

			case "assistant":
				// Show turn count if > 1 (indicates multi-turn agent conversation)
				if msg.Turns > 1 {
					chatLines = append(chatLines, assistantHeaderStyle.Render(i18n.T("settings.residual.hooks.agent"))+
						turnsStyle.Render(i18n.T("settings.residual.hooks.turns", msg.Turns)))
				} else {
					chatLines = append(chatLines, assistantHeaderStyle.Render(i18n.T("settings.residual.hooks.agent")))
				}

				// Show tool calls with hooks (if any)
				if len(msg.ToolCalls) > 0 {
					// Styles for hook display
					hookSuccessStyle := lipgloss.NewStyle().
						Foreground(lipgloss.Color("#A78BFA"))
					hookErrorStyle := lipgloss.NewStyle().
						Foreground(lipgloss.Color(th.Error))
					hookOutputStyle := lipgloss.NewStyle().
						Foreground(lipgloss.Color(th.TextDim)).
						Italic(true)

					for _, tc := range msg.ToolCalls {
						// Show pre-hooks BEFORE the tool call
						for _, ph := range tc.PreHooks {
							icon := "✓"
							style := hookSuccessStyle
							if !ph.Success || ph.Blocked {
								icon = "✗"
								style = hookErrorStyle
							}
							hookLine := i18n.T("settings.residual.hooks.pre_hook", icon, ph.HookName)
							chatLines = append(chatLines, "  "+style.Render(hookLine))
							// Show hook output if present
							if ph.Output != "" {
								output := ph.Output
								if len(output) > 60 {
									output = output[:57] + "..."
								}
								chatLines = append(chatLines, "    "+hookOutputStyle.Render("⎿ "+output))
							}
							if ph.Error != "" {
								chatLines = append(chatLines, "    "+hookErrorStyle.Render("⎿ "+ph.Error))
							}
						}

						// Tool call header
						chatLines = append(chatLines, "  "+toolSymbolStyle.Render("⎿ ")+toolNameStyle.Render(tc.Name))

						// Tool input (pretty-printed JSON, truncated if long)
						if tc.InputJSON != "" {
							inputLines := strings.Split(tc.InputJSON, "\n")
							maxInputLines := 6
							if len(inputLines) > maxInputLines {
								for i := 0; i < maxInputLines-1; i++ {
									line := inputLines[i]
									if width > 23 && len(line) > width-20 {
										line = line[:width-23] + "..."
									}
									chatLines = append(chatLines, "    "+toolInputStyle.Render(line))
								}
								chatLines = append(chatLines, "    "+toolInputStyle.Render("    ..."))
							} else {
								for _, line := range inputLines {
									if width > 23 && len(line) > width-20 {
										line = line[:width-23] + "..."
									}
									chatLines = append(chatLines, "    "+toolInputStyle.Render(line))
								}
							}
						}

						// Tool result
						if tc.Result != "" {
							resultStyle := toolResultStyle
							resultPrefix := "✓ "
							if !tc.Success {
								resultStyle = toolErrorStyle
								resultPrefix = "✗ "
							}
							// Truncate long results
							result := tc.Result
							if len(result) > 100 {
								result = result[:97] + "..."
							}
							chatLines = append(chatLines, "    "+resultStyle.Render(resultPrefix+result))
						}

						// Show post-hooks AFTER the tool call
						for _, ph := range tc.PostHooks {
							icon := "✓"
							style := hookSuccessStyle
							if !ph.Success || ph.Blocked {
								icon = "✗"
								style = hookErrorStyle
							}
							hookLine := i18n.T("settings.residual.hooks.post_hook", icon, ph.HookName)
							chatLines = append(chatLines, "  "+style.Render(hookLine))
							// Show hook output if present
							if ph.Output != "" {
								output := ph.Output
								if len(output) > 60 {
									output = output[:57] + "..."
								}
								chatLines = append(chatLines, "    "+hookOutputStyle.Render("⎿ "+output))
							}
							if ph.Error != "" {
								chatLines = append(chatLines, "    "+hookErrorStyle.Render("⎿ "+ph.Error))
							}
						}
						chatLines = append(chatLines, "")
					}
				}

				// Show final response with markdown-style rendering
				if msg.Content != "" {
					content := msg.Content
					// Render code blocks with different style
					if strings.Contains(content, "```") {
						parts := strings.Split(content, "```")
						for i, part := range parts {
							if i%2 == 0 {
								// Regular text
								if strings.TrimSpace(part) != "" {
									wrapped := wordWrapText(part, width-16)
									for line := range strings.SplitSeq(wrapped, "\n") {
										chatLines = append(chatLines, "  "+assistantTextStyle.Render(line))
									}
								}
							} else {
								// Code block
								codeStyle := lipgloss.NewStyle().
									Foreground(lipgloss.Color(th.Warning)). // Gold for code
									Background(lipgloss.Color(th.BGLight))
								codeLines := strings.SplitSeq(part, "\n")
								for line := range codeLines {
									if width > 21 && len(line) > width-18 {
										line = line[:width-21] + "..."
									}
									chatLines = append(chatLines, "  "+codeStyle.Render("│ "+line))
								}
							}
						}
					} else {
						// Regular text
						wrapped := wordWrapText(content, width-16)
						for line := range strings.SplitSeq(wrapped, "\n") {
							chatLines = append(chatLines, "  "+assistantTextStyle.Render(line))
						}
					}
				}
				chatLines = append(chatLines, "")

			case "system":
				chatLines = append(chatLines, systemStyle.Render("ℹ "+msg.Content))
				chatLines = append(chatLines, "")
			}
		}
	}

	// Apply scroll offset
	if state.HooksChatScrollOffset > 0 && state.HooksChatScrollOffset < len(chatLines) {
		chatLines = chatLines[state.HooksChatScrollOffset:]
	}

	maxLines := chatAreaHeight - 2
	if len(chatLines) > maxLines {
		chatLines = chatLines[len(chatLines)-maxLines:]
	}

	chatContent := lipgloss.JoinVertical(lipgloss.Left, chatLines...)
	lines = append(lines, chatBgStyle.Render(chatContent))
	lines = append(lines, "")

	// Input field
	inputLabelStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(th.Primary)).
		Bold(true).
		Padding(0, 2)
	lines = append(lines, inputLabelStyle.Render(i18n.T("settings.integrations.mcp.message")))

	inputStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(th.Text)).
		Background(lipgloss.Color(th.BGLight)).
		Padding(0, 1).
		Width(maxInt(10, width-10)).
		Margin(0, minInt(2, width/10)).
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color(th.Primary))

	inputValue := state.HooksChatInput
	if state.HooksCursorPos <= len(inputValue) {
		inputValue = inputValue[:state.HooksCursorPos] + "|" + inputValue[state.HooksCursorPos:]
	}

	if state.HooksChatWaiting {
		inputValue = i18n.T("settings.residual.hooks.waiting")
		inputStyle = inputStyle.Foreground(lipgloss.Color(th.TextMuted)).Italic(true)
	}

	lines = append(lines, inputStyle.Render(inputValue))

	content := lipgloss.JoinVertical(lipgloss.Left, lines...)

	containerStyle := lipgloss.NewStyle().
		Width(width).
		Height(height).
		Padding(1, 2)

	return containerStyle.Render(content)
}

// renderActionMenu renders the action menu for a selected hook
func (h *HooksSettings) renderActionMenu(width, height int, state *State, th Theme) string {
	var lines []string

	// Get the selected hook from unified list
	if state.HooksSelected >= len(h.allHooks) {
		return ""
	}
	hook := h.allHooks[state.HooksSelected]

	// Header
	headerStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(th.Primary)).
		Bold(true).
		Padding(1, 2)
	lines = append(lines, headerStyle.Render(hook.Name))

	// Hook details
	detailStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(th.TextMuted)).
		Padding(0, 2)

	if hook.Description != "" {
		lines = append(lines, detailStyle.Render(hook.Description))
	}

	lines = append(lines, detailStyle.Render(i18n.T("settings.residual.hooks.events_value", strings.Join(hook.Events, ", "))))

	if hook.ToolMatcher != "" {
		lines = append(lines, detailStyle.Render(i18n.T("settings.residual.hooks.tool_matcher_value", hook.ToolMatcher)))
	}
	if len(hook.PathAllowlist) > 0 {
		lines = append(lines, detailStyle.Render(i18n.T("settings.residual.hooks.path_allowlist_value", strings.Join(hook.PathAllowlist, ", "))))
	}
	if len(hook.PathDenylist) > 0 {
		lines = append(lines, detailStyle.Render(i18n.T("settings.residual.hooks.path_denylist_value", strings.Join(hook.PathDenylist, ", "))))
	}

	lines = append(lines, detailStyle.Render(i18n.T("settings.residual.hooks.action_timeout_value", hook.Action, hook.Timeout)))

	statusText := i18n.T("settings.residual.common.disabled")
	if hook.Enabled {
		statusText = i18n.T("settings.residual.common.enabled")
	}
	lines = append(lines, detailStyle.Render(i18n.T("settings.residual.hooks.status_priority", statusText, hook.Priority)))
	permText := strings.ToUpper(normalizeHookPermissionPolicy(hook.PermissionPolicy))
	lines = append(lines, detailStyle.Render(i18n.T("settings.residual.hooks.permission_value", permText)))

	if hook.Command != "" {
		cmd := hook.Command
		if len(cmd) > 50 {
			cmd = cmd[:47] + "..."
		}
		lines = append(lines, detailStyle.Render(i18n.T("settings.residual.hooks.command_value", cmd)))
	}

	lines = append(lines, "")

	// Actions header
	actionsHeaderStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(th.Primary)).
		Bold(true).
		Padding(0, 2)
	lines = append(lines, actionsHeaderStyle.Render(i18n.T("settings.residual.hooks.actions")))
	lines = append(lines, "")

	// Action options
	var actions []string
	if !hook.Builtin {
		actions = []string{
			i18n.T("settings.residual.hooks.toggle_enable"),
			i18n.T("settings.residual.hooks.toggle_permission"),
			i18n.T("settings.residual.hooks.edit"),
			i18n.T("settings.residual.hooks.edit_ai"),
			i18n.T("settings.residual.hooks.delete"),
		}
	} else {
		actions = []string{
			i18n.T("settings.residual.hooks.toggle_enable"),
			i18n.T("settings.residual.hooks.toggle_permission"),
		}
	}

	for i, action := range actions {
		isSelected := i == state.HooksActionChoice

		actionStyle := lipgloss.NewStyle().
			Padding(0, 2)

		if isSelected {
			actionStyle = actionStyle.
				Foreground(lipgloss.Color(th.Text)).
				Background(lipgloss.Color(th.Primary)).
				Bold(true)
		} else {
			actionStyle = actionStyle.
				Foreground(lipgloss.Color(th.Text))
		}

		prefix := "  "
		if isSelected {
			prefix = "> "
		}

		lines = append(lines, actionStyle.Render(prefix+action))
	}

	lines = append(lines, "")

	// Hint
	hintStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(th.TextMuted)).
		Italic(true).
		Padding(0, 2)
	lines = append(lines, hintStyle.Render(i18n.T("settings.residual.hooks.action_hint")))

	content := lipgloss.JoinVertical(lipgloss.Left, lines...)

	containerStyle := lipgloss.NewStyle().
		Width(width).
		Height(height).
		Padding(0, 0)

	return containerStyle.Render(content)
}

// renderEditForm renders the hook editing form
func (h *HooksSettings) renderEditForm(width, height int, state *State, th Theme) string {
	var lines []string

	// Title
	titleStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(th.Primary)).
		Bold(true).
		Padding(1, 2)
	lines = append(lines, titleStyle.Render(i18n.T("settings.integrations.hooks.edit")))
	lines = append(lines, "")

	// Form fields
	fields := []struct {
		label string
		value string
		field int
	}{
		{i18n.T("settings.residual.hooks.form.name"), state.HooksFormName, 0},
		{i18n.T("settings.residual.hooks.form.events"), state.HooksFormEvent, 1},
		{i18n.T("settings.residual.hooks.form.command"), state.HooksFormCommand, 2},
		{i18n.T("settings.residual.hooks.form.tool_matcher"), state.HooksFormToolMatcher, 3},
		{i18n.T("settings.residual.hooks.form.path_allowlist"), state.HooksFormPathAllowlist, 4},
		{i18n.T("settings.residual.hooks.form.path_denylist"), state.HooksFormPathDenylist, 5},
		{i18n.T("settings.residual.hooks.form.action"), state.HooksFormAction, 6},
		{i18n.T("settings.residual.hooks.form.timeout"), state.HooksFormTimeout, 7},
	}

	for _, f := range fields {
		isSelected := state.HooksEditingField == f.field
		value := f.value
		if isSelected && state.HooksCursorPos <= len(value) {
			value = value[:state.HooksCursorPos] + "|" + value[state.HooksCursorPos:]
		}

		labelStyle := lipgloss.NewStyle().
			Foreground(lipgloss.Color(th.TextMuted)).
			Padding(0, 2)

		valueStyle := lipgloss.NewStyle().
			Foreground(lipgloss.Color(th.Text)).
			Background(lipgloss.Color(th.BGLight)).
			Padding(0, 1).
			Width(maxInt(10, width-8))

		if isSelected {
			labelStyle = labelStyle.Foreground(lipgloss.Color(th.Primary)).Bold(true)
			valueStyle = valueStyle.
				BorderStyle(lipgloss.RoundedBorder()).
				BorderForeground(lipgloss.Color(th.Primary))
		}

		lines = append(lines, labelStyle.Render(f.label+":"))
		lines = append(lines, "  "+valueStyle.Render(value))
		lines = append(lines, "")
	}

	// Hints
	hintStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(th.TextMuted)).
		Italic(true).
		Padding(0, 2)
	lines = append(lines, hintStyle.Render(i18n.T("settings.integrations.hooks.form_hint")))

	content := lipgloss.JoinVertical(lipgloss.Left, lines...)

	containerStyle := lipgloss.NewStyle().
		Width(width).
		Height(height).
		Padding(0, 0)

	return containerStyle.Render(content)
}

// HandleKey handles keyboard input for hooks settings
