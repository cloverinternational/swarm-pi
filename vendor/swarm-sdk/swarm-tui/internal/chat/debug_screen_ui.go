package chat

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/i18n"
)

// ════════════════════════════════════════════════════════════════════════════════
// Debug Screen UI - Clean, Responsive Layout System
// ════════════════════════════════════════════════════════════════════════════════

// Layout constants - adapt based on screen size
const (
	minWidthCompact  = 60
	minWidthStandard = 100
	minWidthWide     = 140
)

// debugLayout calculates responsive layout based on screen width
type debugLayout struct {
	width       int
	height      int
	isCompact   bool
	isWide      bool
	contentW    int
	columnWidth int
}

func (d *DebugScreen) getLayout() debugLayout {
	l := debugLayout{
		width:    d.width,
		height:   d.height,
		contentW: d.width - 4, // Account for border padding
	}
	l.isCompact = d.width < minWidthStandard
	l.isWide = d.width >= minWidthWide

	// Calculate column width for table-like displays
	if l.isCompact {
		l.columnWidth = l.contentW - 4
	} else if l.isWide {
		l.columnWidth = l.contentW - 8
	} else {
		l.columnWidth = l.contentW - 6
	}
	return l
}

// ════════════════════════════════════════════════════════════════════════════════
// Main View - Clean Container
// ════════════════════════════════════════════════════════════════════════════════

func (d *DebugScreen) renderCleanView() string {
	if !d.visible {
		return ""
	}

	th := d.theme
	layout := d.getLayout()

	// ┌─ Header ─────────────────────────────────────────────────────────────────┐
	header := d.renderCleanHeader(layout)

	// ├─ Tab Bar ────────────────────────────────────────────────────────────────┤
	tabBar := d.renderCleanTabBar(layout)

	// ├─ Content ────────────────────────────────────────────────────────────────┤
	contentHeight := d.height - 7 // header(1) + tabs(1) + footer(2) + borders(3)
	if contentHeight < 5 {
		contentHeight = 5
	}
	content := d.renderCleanContent(layout, contentHeight)

	// └─ Footer ─────────────────────────────────────────────────────────────────┘
	footer := d.renderCleanFooter(layout)

	// Assemble screen
	screen := lipgloss.JoinVertical(lipgloss.Left,
		header,
		tabBar,
		content,
		footer,
	)

	// Container with subtle border
	containerStyle := lipgloss.NewStyle().
		Width(d.width).
		Height(d.height).
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color(th.Border))

	return containerStyle.Render(screen)
}

// ════════════════════════════════════════════════════════════════════════════════
// Header - Title + Status Indicators
// ════════════════════════════════════════════════════════════════════════════════

func (d *DebugScreen) renderCleanHeader(layout debugLayout) string {
	th := d.theme

	// Title on left
	titleStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(th.Primary)).
		Bold(true).
		Padding(0, 1)
	title := titleStyle.Render(i18n.T("classic_chat_2.debug.title"))

	// Status indicators on right
	var indicators []string

	// Request count
	reqCount := i18n.T("classic_chat_2.debug.request_count", len(d.requests))
	reqStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(th.TextMuted))
	indicators = append(indicators, reqStyle.Render(reqCount))

	// Auto-scroll indicator
	if d.autoScroll {
		indicators = append(indicators, lipgloss.NewStyle().
			Foreground(lipgloss.Color("#A6E3A1")).
			Render(i18n.T("classic_chat_2.debug.auto")))
	} else {
		indicators = append(indicators, lipgloss.NewStyle().
			Foreground(lipgloss.Color("#F9E2AF")).
			Render(i18n.T("classic_chat_2.debug.manual")))
	}

	statusText := strings.Join(indicators, "  ")

	// Calculate spacing
	titleWidth := lipgloss.Width(title)
	statusWidth := lipgloss.Width(statusText)
	spacing := layout.contentW - titleWidth - statusWidth - 2
	if spacing < 1 {
		spacing = 1
	}

	return title + strings.Repeat(" ", spacing) + statusText
}

// ════════════════════════════════════════════════════════════════════════════════
// Tab Bar - Clean Minimal Tabs
// ════════════════════════════════════════════════════════════════════════════════

func (d *DebugScreen) renderCleanTabBar(layout debugLayout) string {
	th := d.theme

	type tabDef struct {
		key  string
		name string
		tab  DebugTab
	}

	tabs := []tabDef{
		{"1", i18n.T("classic_chat_2.debug.tab.requests"), DebugTabRequests},
		{"2", i18n.T("classic_chat_2.debug.tab.logs"), DebugTabLogs},
		{"3", i18n.T("classic_chat_2.debug.tab.usage"), DebugTabUsage},
		{"4", i18n.T("classic_chat_2.debug.tab.context"), DebugTabContext},
	}

	var tabViews []string
	for _, t := range tabs {
		var label string
		if layout.isCompact {
			label = t.key // Just number in compact mode
		} else {
			label = fmt.Sprintf("%s %s", t.key, t.name)
		}

		var style lipgloss.Style
		if t.tab == d.activeTab {
			style = lipgloss.NewStyle().
				Foreground(lipgloss.Color(th.BG)).
				Background(lipgloss.Color(th.Primary)).
				Bold(true).
				Padding(0, 1)
		} else {
			style = lipgloss.NewStyle().
				Foreground(lipgloss.Color(th.TextMuted)).
				Padding(0, 1)
		}
		tabViews = append(tabViews, style.Render(label))
	}

	// Separator line under tabs
	tabLine := lipgloss.JoinHorizontal(lipgloss.Left, tabViews...)
	separator := lipgloss.NewStyle().
		Foreground(lipgloss.Color(th.Border)).
		Render(strings.Repeat("─", layout.contentW))

	return tabLine + "\n" + separator
}

// ════════════════════════════════════════════════════════════════════════════════
// Content Router
// ════════════════════════════════════════════════════════════════════════════════

func (d *DebugScreen) renderCleanContent(layout debugLayout, height int) string {
	switch d.activeTab {
	case DebugTabRequests:
		return d.renderRequestsInspector(layout, height)
	case DebugTabLogs:
		return d.renderLogsView(height) // Keep existing for now
	case DebugTabUsage:
		return d.renderUsageView(layout, height)
	case DebugTabContext:
		return d.renderContextView(layout, height)
	default:
		return ""
	}
}

// renderRequestsInspector renders the Requests tab as a master-detail view: the
// request list by default, and — once the user drills into a request — a lens
// sub-tab bar plus the selected lens's content. Only lenses that have data for
// the selected request are offered, so no lens ever shows an empty panel.
func (d *DebugScreen) renderRequestsInspector(layout debugLayout, height int) string {
	if !d.detailOpen {
		return d.renderCleanRequestsList(layout, height)
	}

	lensBar := d.renderLensBar(layout)
	detailHeight := height - 1 // lens bar consumes one line
	if detailHeight < 3 {
		detailHeight = 3
	}

	if len(d.requests) == 0 || d.selectedReq >= len(d.requests) {
		body := lipgloss.NewStyle().
			Foreground(lipgloss.Color(d.theme.TextMuted)).
			Padding(2, 2).
			Render(i18n.T("classic_chat_2.debug.no_request_selected"))
		return lipgloss.JoinVertical(lipgloss.Left, lensBar, body)
	}

	req := d.requests[d.selectedReq]
	var body string
	switch d.lens {
	case lensRequest:
		body = d.renderCleanJSONView(layout, detailHeight)
	case lensProvider:
		body = d.renderProviderLens(layout, detailHeight, req)
	case lensEvents:
		body = d.renderEventsLens(layout, detailHeight, req)
	default: // lensOverview
		body = d.renderCleanDetailsView(layout, detailHeight)
	}
	return lipgloss.JoinVertical(lipgloss.Left, lensBar, body)
}

// renderLensBar renders the available detail lenses for the selected request as
// a compact sub-tab row, highlighting the active lens.
func (d *DebugScreen) renderLensBar(layout debugLayout) string {
	th := d.theme
	labels := map[debugLens][2]string{
		lensOverview: {"o", i18n.T("classic_chat_2.debug.lens.overview")},
		lensRequest:  {"r", i18n.T("classic_chat_2.debug.lens.request")},
		lensProvider: {"p", i18n.T("classic_chat_2.debug.lens.provider")},
		lensEvents:   {"e", i18n.T("classic_chat_2.debug.lens.events")},
	}
	var chips []string
	for _, l := range d.availableLenses() {
		meta := labels[l]
		label := fmt.Sprintf("%s %s", meta[0], meta[1])
		var style lipgloss.Style
		if l == d.lens {
			style = lipgloss.NewStyle().
				Foreground(lipgloss.Color(th.BG)).
				Background(lipgloss.Color(th.Accent)).
				Bold(true).
				Padding(0, 1)
		} else {
			style = lipgloss.NewStyle().
				Foreground(lipgloss.Color(th.TextMuted)).
				Padding(0, 1)
		}
		chips = append(chips, style.Render(label))
	}
	chips = append(chips, lipgloss.NewStyle().
		Foreground(lipgloss.Color(th.TextMuted)).
		Padding(0, 1).
		Render(i18n.T("classic_chat_2.debug.lens.back")))
	return lipgloss.JoinHorizontal(lipgloss.Left, chips...)
}

// renderProviderLens shows the provider-format (API) JSON for a request.
func (d *DebugScreen) renderProviderLens(layout debugLayout, height int, req DebugRequest) string {
	th := d.theme
	if req.ProviderJSON == nil {
		return lipgloss.NewStyle().
			Foreground(lipgloss.Color(th.TextMuted)).
			Padding(2, 2).
			Render(i18n.T("classic_chat_2.debug.provider_json_missing"))
	}
	var lines []string
	lines = append(lines, lipgloss.NewStyle().
		Foreground(lipgloss.Color("#A6E3A1")).
		Bold(true).
		Render(i18n.T("classic_chat_2.debug.provider_json_title")))
	lines = append(lines, strings.Repeat("─", min(40, layout.columnWidth)))
	lines = append(lines, formatJSONClean(*req.ProviderJSON, layout.columnWidth-4, d.jsonPretty)...)
	return d.applyScrolling(lines, layout, height)
}

// renderEventsLens shows the raw API events (envelopes) captured for a request.
func (d *DebugScreen) renderEventsLens(layout debugLayout, height int, req DebugRequest) string {
	th := d.theme
	if len(req.Envelopes) == 0 {
		return lipgloss.NewStyle().
			Foreground(lipgloss.Color(th.TextMuted)).
			Padding(2, 2).
			Render(i18n.T("classic_chat_2.debug.events_missing"))
	}
	var lines []string
	lines = append(lines, lipgloss.NewStyle().
		Foreground(lipgloss.Color("#89B4FA")).
		Bold(true).
		Render(i18n.T("classic_chat_2.debug.events_title", len(req.Envelopes))))
	lines = append(lines, strings.Repeat("─", min(40, layout.columnWidth)))
	for i, env := range req.Envelopes {
		lines = append(lines, lipgloss.NewStyle().
			Foreground(lipgloss.Color(th.Accent)).
			Bold(true).
			Render(i18n.T("classic_chat_2.debug.event_number", i)))
		if j := marshalForDebug(env); j != nil {
			lines = append(lines, formatJSONClean(*j, layout.columnWidth-4, d.jsonPretty)...)
		}
		lines = append(lines, "")
	}
	return d.applyScrolling(lines, layout, height)
}

// ════════════════════════════════════════════════════════════════════════════════
// Requests List - Clean Table Layout
// ════════════════════════════════════════════════════════════════════════════════

func (d *DebugScreen) renderCleanRequestsList(layout debugLayout, height int) string {
	th := d.theme

	if len(d.requests) == 0 {
		emptyStyle := lipgloss.NewStyle().
			Foreground(lipgloss.Color(th.TextMuted)).
			Padding(2, 2)
		return emptyStyle.Render(i18n.T("classic_chat_2.debug.requests_empty"))
	}

	var lines []string

	// Column header
	headerStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(th.TextMuted)).
		Bold(true)

	if layout.isCompact {
		lines = append(lines, headerStyle.Render(i18n.T("classic_chat_2.debug.requests_header_compact")))
	} else {
		lines = append(lines, headerStyle.Render(i18n.T("classic_chat_2.debug.requests_header")))
	}
	lines = append(lines, lipgloss.NewStyle().
		Foreground(lipgloss.Color(th.Border)).
		Render(strings.Repeat("─", layout.columnWidth)))

	// Request rows
	for i, req := range d.requests {
		isSelected := i == d.selectedReq

		// Status
		statusIcon := "✓"
		statusColor := "#A6E3A1"
		if req.Error != "" {
			statusIcon = "✗"
			statusColor = "#F38BA8"
		} else if req.ResponseCode >= 400 {
			statusIcon = "!"
			statusColor = "#F9E2AF"
		}

		// Build row based on layout
		var row string
		if layout.isCompact {
			row = fmt.Sprintf(" %2d   %s   %4dms  %5d  %5d  %s",
				i,
				lipgloss.NewStyle().Foreground(lipgloss.Color(statusColor)).Render(statusIcon),
				req.Duration.Milliseconds(),
				req.TokensInput,
				req.TokensOutput,
				truncateModel(req.Model, 12),
			)
		} else {
			row = fmt.Sprintf(" %3d    %s %s    %-4s    %6dms    %7d    %8d   %s",
				i,
				lipgloss.NewStyle().Foreground(lipgloss.Color(statusColor)).Render(statusIcon),
				lipgloss.NewStyle().Foreground(lipgloss.Color(statusColor)).Render(fmt.Sprintf("%-3d", req.ResponseCode)),
				req.Method,
				req.Duration.Milliseconds(),
				req.TokensInput,
				req.TokensOutput,
				truncateModel(req.Model, 25),
			)
		}

		// Apply selection styling
		var rowStyle lipgloss.Style
		if isSelected {
			rowStyle = lipgloss.NewStyle().
				Background(lipgloss.Color(th.BGLight)).
				Foreground(lipgloss.Color(th.Primary)).
				Bold(true).
				Width(layout.columnWidth)
		} else {
			rowStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color(th.Text)).
				Width(layout.columnWidth)
		}

		lines = append(lines, rowStyle.Render(row))

		// Show error details for selected item
		if isSelected && req.Error != "" {
			errStyle := lipgloss.NewStyle().
				Foreground(lipgloss.Color("#F38BA8")).
				Padding(0, 0, 0, 6)
			errText := truncate(req.Error, layout.columnWidth-10)
			lines = append(lines, errStyle.Render("└ "+errText))
		}
	}

	// Apply scrolling
	visibleHeight := height - 3 // Account for header
	if visibleHeight < 3 {
		visibleHeight = 3
	}

	totalLines := len(lines)
	if d.scrollOffset() >= totalLines-2 {
		d.setScrollOffset(totalLines - visibleHeight)
	}
	if d.scrollOffset() < 0 {
		d.resetScrollOffset()
	}

	// Keep selected visible
	selectedLine := d.selectedReq + 2 // +2 for header rows
	if selectedLine >= d.scrollOffset()+visibleHeight {
		d.setScrollOffset(selectedLine - visibleHeight + 1)
	}
	if selectedLine < d.scrollOffset() {
		d.setScrollOffset(selectedLine)
	}

	// Slice visible lines
	endIdx := d.scrollOffset() + visibleHeight
	if endIdx > totalLines {
		endIdx = totalLines
	}
	startIdx := d.scrollOffset()
	if startIdx < 0 {
		startIdx = 0
	}
	if startIdx < totalLines {
		lines = lines[startIdx:endIdx]
	}

	// Scroll indicator
	if totalLines > visibleHeight {
		pct := 0
		if totalLines-visibleHeight > 0 {
			pct = int(float64(d.scrollOffset()) / float64(totalLines-visibleHeight) * 100)
		}
		scrollStyle := lipgloss.NewStyle().
			Foreground(lipgloss.Color(th.TextMuted))
		lines = append(lines, scrollStyle.Render(i18n.T("classic_chat_2.debug.position", d.scrollOffset()+1, totalLines, pct)))
	}

	contentStyle := lipgloss.NewStyle().
		Padding(0, 1).
		Width(layout.contentW)

	return contentStyle.Render(strings.Join(lines, "\n"))
}

// ════════════════════════════════════════════════════════════════════════════════
// Details View - Clean Sections
// ════════════════════════════════════════════════════════════════════════════════

func (d *DebugScreen) renderCleanDetailsView(layout debugLayout, height int) string {
	th := d.theme

	if len(d.requests) == 0 || d.selectedReq >= len(d.requests) {
		return lipgloss.NewStyle().
			Foreground(lipgloss.Color(th.TextMuted)).
			Padding(2, 2).
			Render(i18n.T("classic_chat_2.debug.no_request_selected"))
	}

	req := d.requests[d.selectedReq]
	var lines []string

	// Section helper
	section := func(title string, content [][2]string) {
		titleStyle := lipgloss.NewStyle().
			Foreground(lipgloss.Color(th.Accent)).
			Bold(true)
		lines = append(lines, titleStyle.Render("▸ "+title))

		labelStyle := lipgloss.NewStyle().
			Foreground(lipgloss.Color(th.TextMuted)).
			Width(14)
		valueStyle := lipgloss.NewStyle().
			Foreground(lipgloss.Color(th.Text))

		for _, kv := range content {
			line := labelStyle.Render(kv[0]+":") + " " + valueStyle.Render(kv[1])
			lines = append(lines, "  "+line)
		}
		lines = append(lines, "")
	}

	// Overview section
	section(i18n.T("classic_chat_2.debug.details.overview"), [][2]string{
		{i18n.T("classic_chat_2.debug.details.id"), req.ID},
		{i18n.T("classic_chat_2.debug.details.timestamp"), req.Timestamp.Format("2006-01-02 15:04:05")},
		{i18n.T("classic_chat_2.debug.details.model"), req.Model},
		{i18n.T("classic_chat_2.debug.details.method"), req.Method},
		{i18n.T("classic_chat_2.debug.details.status"), fmt.Sprintf("%d", req.ResponseCode)},
		{i18n.T("classic_chat_2.debug.details.duration"), fmt.Sprintf("%dms", req.Duration.Milliseconds())},
	})

	// Tokens section
	section(i18n.T("classic_chat_2.debug.details.tokens"), [][2]string{
		{i18n.T("classic_chat_2.debug.details.input"), fmt.Sprintf("%d", req.TokensInput)},
		{i18n.T("classic_chat_2.debug.details.output"), fmt.Sprintf("%d", req.TokensOutput)},
		{i18n.T("classic_chat_2.debug.details.total"), fmt.Sprintf("%d", req.TokensInput+req.TokensOutput)},
	})

	// Error section (if present)
	if req.Error != "" {
		errStyle := lipgloss.NewStyle().
			Foreground(lipgloss.Color("#F38BA8"))
		lines = append(lines, errStyle.Render(i18n.T("classic_chat_2.debug.details.error")))
		wrappedErr := wrapText(req.Error, layout.columnWidth-4)
		for _, line := range wrappedErr {
			lines = append(lines, "  "+line)
		}
		lines = append(lines, "")
	}

	// Conversation state
	if req.ConversationState != nil {
		cs := req.ConversationState
		section(i18n.T("classic_chat_2.debug.details.conversation_state"), [][2]string{
			{i18n.T("classic_chat_2.debug.details.messages"), fmt.Sprintf("%d", cs.MessageCount)},
			{i18n.T("classic_chat_2.debug.details.system_prompt"), i18n.T("classic_chat_2.debug.details.chars", cs.SystemPromptLength)},
			{i18n.T("classic_chat_2.debug.details.tools"), fmt.Sprintf("%d", cs.ToolCount)},
			{i18n.T("classic_chat_2.debug.details.content_size"), i18n.T("classic_chat_2.debug.details.chars", cs.TotalContentLength)},
		})
	}

	// Apply scrolling
	visibleHeight := height - 2
	totalLines := len(lines)

	if d.scrollOffset() > totalLines-visibleHeight {
		d.setScrollOffset(totalLines - visibleHeight)
	}
	if d.scrollOffset() < 0 {
		d.resetScrollOffset()
	}

	endIdx := d.scrollOffset() + visibleHeight
	if endIdx > totalLines {
		endIdx = totalLines
	}
	if d.scrollOffset() < totalLines {
		lines = lines[d.scrollOffset():endIdx]
	}

	// Scroll indicator
	if totalLines > visibleHeight {
		pct := 0
		if totalLines-visibleHeight > 0 {
			pct = int(float64(d.scrollOffset()) / float64(totalLines-visibleHeight) * 100)
		}
		scrollStyle := lipgloss.NewStyle().
			Foreground(lipgloss.Color(th.TextMuted))
		lines = append(lines, scrollStyle.Render(i18n.T("classic_chat_2.debug.line_position", d.scrollOffset()+1, totalLines, pct)))
	}

	contentStyle := lipgloss.NewStyle().
		Padding(1, 2).
		Width(layout.contentW)

	return contentStyle.Render(strings.Join(lines, "\n"))
}

// ════════════════════════════════════════════════════════════════════════════════
// Usage View - Provider quota / token consumption (bridged from *App)
// ════════════════════════════════════════════════════════════════════════════════

func (d *DebugScreen) renderUsageView(layout debugLayout, height int) string {
	th := d.theme

	if d.usageRenderer == nil {
		return lipgloss.NewStyle().
			Foreground(lipgloss.Color(th.TextMuted)).
			Padding(2, 2).
			Render(i18n.T("classic_chat_2.debug.usage_unavailable"))
	}

	lines := d.usageRenderer(layout.contentW, height)
	if len(lines) == 0 {
		return lipgloss.NewStyle().
			Foreground(lipgloss.Color(th.TextMuted)).
			Padding(2, 2).
			Render(i18n.T("classic_chat_2.debug.usage_empty"))
	}

	return d.applyScrolling(lines, layout, height)
}

// ════════════════════════════════════════════════════════════════════════════════
// Footer - Clean Contextual Help
// ════════════════════════════════════════════════════════════════════════════════

func (d *DebugScreen) renderCleanFooter(layout debugLayout) string {
	th := d.theme

	// Group shortcuts by category
	var navKeys, actionKeys, globalKeys []string

	switch d.activeTab {
	case DebugTabRequests:
		if d.detailOpen {
			navKeys = []string{i18n.T("classic_chat_2.debug.hint.scroll"), i18n.T("classic_chat_2.debug.hint.page"), i18n.T("classic_chat_2.debug.hint.ends")}
			actionKeys = []string{i18n.T("classic_chat_2.debug.hint.lens"), i18n.T("classic_chat_2.debug.hint.back"), i18n.T("classic_chat_2.debug.hint.pretty"), i18n.T("classic_chat_2.debug.hint.copy")}
		} else {
			navKeys = []string{i18n.T("classic_chat_2.debug.hint.select"), i18n.T("classic_chat_2.debug.hint.ends"), i18n.T("classic_chat_2.debug.hint.open")}
			actionKeys = []string{i18n.T("classic_chat_2.debug.hint.auto_scroll"), i18n.T("classic_chat_2.debug.hint.clear"), i18n.T("classic_chat_2.debug.hint.copy")}
		}
	case DebugTabLogs:
		navKeys = []string{i18n.T("classic_chat_2.debug.hint.scroll"), i18n.T("classic_chat_2.debug.hint.ends")}
		actionKeys = []string{i18n.T("classic_chat_2.debug.hint.filter"), i18n.T("classic_chat_2.debug.hint.errors"), i18n.T("classic_chat_2.debug.hint.reset"), i18n.T("classic_chat_2.debug.hint.copy")}
	case DebugTabUsage:
		navKeys = []string{i18n.T("classic_chat_2.debug.hint.scroll"), i18n.T("classic_chat_2.debug.hint.subtab"), i18n.T("classic_chat_2.debug.hint.ends")}
		actionKeys = []string{i18n.T("classic_chat_2.debug.hint.refresh")}
	case DebugTabContext:
		navKeys = []string{i18n.T("classic_chat_2.debug.hint.scroll"), i18n.T("classic_chat_2.debug.hint.page"), i18n.T("classic_chat_2.debug.hint.ends"), i18n.T("classic_chat_2.debug.hint.turn")}
		actionKeys = []string{i18n.T("classic_chat_2.debug.hint.copy")}
	}
	globalKeys = []string{i18n.T("classic_chat_2.debug.hint.tab"), i18n.T("classic_chat_2.debug.hint.close")}

	// Format based on width
	var parts []string
	if layout.isCompact {
		// Minimal format for narrow screens
		all := append(navKeys, actionKeys...)
		all = append(all, globalKeys...)
		parts = all[:min(len(all), 5)] // Show max 5 shortcuts
	} else {
		// Full format with separators
		parts = append(parts, strings.Join(navKeys, " "))
		parts = append(parts, "│")
		parts = append(parts, strings.Join(actionKeys, " "))
		parts = append(parts, "│")
		parts = append(parts, strings.Join(globalKeys, " "))
	}

	footerStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(th.TextMuted)).
		Width(layout.contentW).
		Padding(0, 1)

	return footerStyle.Render(strings.Join(parts, " "))
}

// ════════════════════════════════════════════════════════════════════════════════
// Helpers
// ════════════════════════════════════════════════════════════════════════════════

func truncateModel(model string, maxLen int) string {
	// Remove common prefixes for cleaner display
	model = strings.TrimPrefix(model, "claude-")
	model = strings.TrimPrefix(model, "gpt-")
	model = strings.TrimPrefix(model, "gemini-")

	if len(model) > maxLen {
		return model[:maxLen-1] + "…"
	}
	return model
}

func wrapTextDebug(text string, width int) []string {
	if width <= 0 {
		return []string{text}
	}

	var lines []string
	for len(text) > width {
		// Find last space before width
		idx := strings.LastIndex(text[:width], " ")
		if idx <= 0 {
			idx = width
		}
		lines = append(lines, text[:idx])
		text = strings.TrimSpace(text[idx:])
	}
	if len(text) > 0 {
		lines = append(lines, text)
	}
	return lines
}

// ════════════════════════════════════════════════════════════════════════════════
// Clean JSON View
// ════════════════════════════════════════════════════════════════════════════════

func (d *DebugScreen) renderCleanJSONView(layout debugLayout, height int) string {
	th := d.theme

	if len(d.requests) == 0 || d.selectedReq >= len(d.requests) {
		return lipgloss.NewStyle().
			Foreground(lipgloss.Color(th.TextMuted)).
			Padding(2, 2).
			Render(i18n.T("classic_chat_2.debug.no_request_selected"))
	}

	req := d.requests[d.selectedReq]
	var lines []string

	// Header with request info
	headerStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(th.Primary)).
		Bold(true)
	lines = append(lines, headerStyle.Render(i18n.T("classic_chat_2.debug.request_header",
		d.selectedReq, truncateModel(req.Model, 20), req.Duration.Milliseconds())))
	lines = append(lines, "")

	// Request section
	reqHeader := lipgloss.NewStyle().
		Foreground(lipgloss.Color("#89B4FA")).
		Bold(true)
	lines = append(lines, reqHeader.Render(i18n.T("classic_chat_2.debug.request")))
	lines = append(lines, strings.Repeat("─", min(40, layout.columnWidth)))

	if req.Body != "" {
		jsonLines := formatJSONClean(req.Body, layout.columnWidth-4, d.jsonPretty)
		lines = append(lines, jsonLines...)
	} else {
		lines = append(lines, lipgloss.NewStyle().
			Foreground(lipgloss.Color(th.TextMuted)).
			Render(i18n.T("classic_chat_2.debug.no_body")))
	}
	lines = append(lines, "")

	// Response section
	respHeader := lipgloss.NewStyle().
		Foreground(lipgloss.Color("#A6E3A1")).
		Bold(true)
	lines = append(lines, respHeader.Render(i18n.T("classic_chat_2.debug.response")))
	lines = append(lines, strings.Repeat("─", min(40, layout.columnWidth)))

	if req.Response != "" {
		jsonLines := formatJSONClean(req.Response, layout.columnWidth-4, d.jsonPretty)
		lines = append(lines, jsonLines...)
	} else if req.Error != "" {
		lines = append(lines, lipgloss.NewStyle().
			Foreground(lipgloss.Color("#F38BA8")).
			Render(i18n.T("classic_chat_2.debug.error", req.Error)))
	} else {
		lines = append(lines, lipgloss.NewStyle().
			Foreground(lipgloss.Color(th.TextMuted)).
			Render(i18n.T("classic_chat_2.debug.pending")))
	}

	// Apply scrolling
	visibleHeight := height - 2
	totalLines := len(lines)

	maxScroll := totalLines - visibleHeight
	if maxScroll < 0 {
		maxScroll = 0
	}
	if d.scrollOffset() > maxScroll {
		d.setScrollOffset(maxScroll)
	}
	if d.scrollOffset() < 0 {
		d.resetScrollOffset()
	}

	endIdx := d.scrollOffset() + visibleHeight
	if endIdx > totalLines {
		endIdx = totalLines
	}
	if d.scrollOffset() < totalLines {
		lines = lines[d.scrollOffset():endIdx]
	}

	// Scroll indicator
	if totalLines > visibleHeight {
		pct := 0
		if maxScroll > 0 {
			pct = int(float64(d.scrollOffset()) / float64(maxScroll) * 100)
		}
		scrollStyle := lipgloss.NewStyle().
			Foreground(lipgloss.Color(th.TextMuted))
		lines = append(lines, scrollStyle.Render(i18n.T("classic_chat_2.debug.lines_position", d.scrollOffset()+1, totalLines, pct)))
	}

	contentStyle := lipgloss.NewStyle().
		Padding(1, 2).
		Width(layout.contentW)

	return contentStyle.Render(strings.Join(lines, "\n"))
}

func formatJSONClean(jsonStr string, maxWidth int, pretty bool) []string {
	if !pretty {
		// Just wrap raw JSON
		return wrapTextDebug(jsonStr, maxWidth)
	}

	// Pretty print with minimal syntax highlighting
	var result []string
	lines := strings.SplitSeq(prettyJSON(jsonStr), "\n")

	for line := range lines {
		if len(line) > maxWidth {
			line = line[:maxWidth-1] + "…"
		}
		result = append(result, line)
	}
	return result
}

func (d *DebugScreen) applyScrolling(lines []string, layout debugLayout, height int) string {
	th := d.theme
	visibleHeight := height - 2
	totalLines := len(lines)

	maxScroll := totalLines - visibleHeight
	if maxScroll < 0 {
		maxScroll = 0
	}
	if d.scrollOffset() > maxScroll {
		d.setScrollOffset(maxScroll)
	}
	if d.scrollOffset() < 0 {
		d.resetScrollOffset()
	}

	endIdx := d.scrollOffset() + visibleHeight
	if endIdx > totalLines {
		endIdx = totalLines
	}
	startIdx := d.scrollOffset()
	if startIdx < 0 {
		startIdx = 0
	}
	if startIdx < totalLines {
		lines = lines[startIdx:endIdx]
	}

	// Scroll indicator
	if totalLines > visibleHeight {
		pct := 0
		if maxScroll > 0 {
			pct = int(float64(d.scrollOffset()) / float64(maxScroll) * 100)
		}
		scrollStyle := lipgloss.NewStyle().
			Foreground(lipgloss.Color(th.TextMuted))
		lines = append(lines, scrollStyle.Render(i18n.T("classic_chat_2.debug.position", d.scrollOffset()+1, totalLines, pct)))
	}

	return lipgloss.NewStyle().
		Padding(1, 2).
		Width(layout.contentW).
		Render(strings.Join(lines, "\n"))
}
