package settings

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/tools/mcp"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/chat/commands"
	chatcontext "github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/chat/context"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/i18n"
)

func (c *ContextSettings) Render(width, height int, state *State, theme any) string {
	th := theme.(Theme)
	const (
		borderWidth      = 2
		containerPadding = 1
	)

	innerWidth := maxInt(20, width-(borderWidth*2)-(containerPadding*2))
	innerHeight := maxInt(5, height-(borderWidth*2)-(containerPadding*2))

	if state.ContextState == "" {
		state.ContextState = "list"
	}
	if state.ContextEditTarget == "" {
		state.ContextEditTarget = contextEditGlobal
	}

	var title, content, hints string
	switch state.ContextState {
	case "mcp_servers":
		title = c.renderMCPServerTitle(innerWidth, th)
		hints = c.renderHintBar(innerWidth, state, th)
		contentHeight := innerHeight - lipgloss.Height(title) - lipgloss.Height(hints)
		content = c.renderMCPServerList(innerWidth, maxInt(1, contentHeight), state, th)
	case "mcp_picker":
		title = c.renderMCPPickerTitle(innerWidth, state, th)
		hints = c.renderHintBar(innerWidth, state, th)
		contentHeight := innerHeight - lipgloss.Height(title) - lipgloss.Height(hints)
		content = c.renderMCPPickerList(innerWidth, maxInt(1, contentHeight), state, th)
	case "mcp_prompt_args":
		title = c.renderMCPArgsTitle(innerWidth, state, th)
		hints = c.renderHintBar(innerWidth, state, th)
		contentHeight := innerHeight - lipgloss.Height(title) - lipgloss.Height(hints)
		content = c.renderMCPArgsEditor(innerWidth, maxInt(1, contentHeight), state, th)
	case "detail":
		title = c.renderTitle(innerWidth, state, th)
		hints = c.renderHintBar(innerWidth, state, th)
		contentHeight := innerHeight - lipgloss.Height(title) - lipgloss.Height(hints)
		content = c.renderContextDetail(innerWidth, maxInt(1, contentHeight), state, th)
	default:
		title = c.renderTitle(innerWidth, state, th)
		hints = c.renderHintBar(innerWidth, state, th)
		contentHeight := innerHeight - lipgloss.Height(title) - lipgloss.Height(hints)
		content = c.renderContextList(innerWidth, maxInt(1, contentHeight), state, th)
	}

	fullContent := lipgloss.JoinVertical(lipgloss.Left,
		title,
		content,
		hints,
	)

	// Constrain content to innerWidth to prevent container expansion
	fullContent = lipgloss.NewStyle().
		Width(innerWidth).
		Render(fullContent)

	containerStyle := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color(th.Border)).
		Width(maxInt(20, width-2)).
		Padding(0, containerPadding)

	return i18n.SettingsIntegrationsText(containerStyle.Render(fullContent))
}

// renderTitle renders the section title with stats badges
func (c *ContextSettings) renderTitle(width int, state *State, th Theme) string {
	titleStyle := lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color(th.Primary)).
		Width(width).
		Align(lipgloss.Center)

	title := titleStyle.Render(i18n.T("settings.residual.context.sources_title"))

	enabledCount := 0
	rows := c.getRows()
	for _, row := range rows {
		if row.Source.Enabled {
			enabledCount++
		}
	}

	totalCount := len(rows)

	var sections []string
	sections = append(sections, title, "")

	if width >= 40 {
		enabledBadge := lipgloss.NewStyle().
			Background(lipgloss.Color(th.Success)).
			Foreground(lipgloss.Color(th.Text)).
			Padding(0, 1).
			Bold(true).
			Render(i18n.T("settings.residual.common.enabled_count", enabledCount))

		if width >= 55 {
			totalBadge := lipgloss.NewStyle().
				Background(lipgloss.Color(th.TextMuted)).
				Foreground(lipgloss.Color(th.Text)).
				Padding(0, 1).
				Render(i18n.T("settings.residual.common.total_count", totalCount))
			badgesRow := lipgloss.JoinHorizontal(lipgloss.Center, enabledBadge, " ", totalBadge)
			sections = append(sections, lipgloss.NewStyle().Width(width).Align(lipgloss.Center).Render(badgesRow), "")
		} else {
			sections = append(sections, lipgloss.NewStyle().Width(width).Align(lipgloss.Center).Render(enabledBadge), "")
		}
	}

	if width >= 45 {
		subtitleStyle := lipgloss.NewStyle().
			Foreground(lipgloss.Color(th.TextMuted)).
			Italic(true).
			Width(width).
			Align(lipgloss.Center)
		subText := i18n.T("settings.residual.context.controls")
		if width < 45 {
			subText = i18n.T("settings.residual.context.controls_compact")
		}
		sections = append(sections, subtitleStyle.Render(subText), "")
	}

	editTarget := normalizeEditTarget(state.ContextEditTarget)
	globalStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(th.TextMuted))
	projectStyle := globalStyle
	if editTarget == contextEditGlobal {
		globalStyle = globalStyle.Bold(true).Foreground(lipgloss.Color(th.Primary))
	} else {
		projectStyle = projectStyle.Bold(true).Foreground(lipgloss.Color(th.Primary))
	}
	editLine := lipgloss.NewStyle().Width(width).Align(lipgloss.Center).Render(
		i18n.T("settings.residual.context.editing") + " " +
			globalStyle.Render(i18n.T("settings.residual.context.global")) + "  " +
			projectStyle.Render(i18n.T("settings.residual.context.project")),
	)
	sections = append(sections, editLine)

	return lipgloss.JoinVertical(lipgloss.Left, sections...)
}

// renderContextList renders the context sources list with categories
func (c *ContextSettings) renderContextList(width, height int, state *State, th Theme) string {
	rows := c.getRows()
	if len(rows) == 0 {
		muted := lipgloss.NewStyle().
			Foreground(lipgloss.Color(th.TextMuted)).
			Padding(1, 2)
		return muted.Render(i18n.T("settings.residual.context.no_sources"))
	}

	state.ContextSelectedItem = clampInt(state.ContextSelectedItem, 0, len(rows)-1)

	layout := computeContextTableLayout(width)
	header := renderContextHeader(layout, th)

	lines := []string{header}
	listHeight := maxInt(1, height-1)
	start, end := windowBounds(state.ContextSelectedItem, len(rows), listHeight)

	for i := start; i < end; i++ {
		row := rows[i]
		line := renderContextRow(row, layout, i == state.ContextSelectedItem, th)
		lines = append(lines, line)
	}

	return lipgloss.JoinVertical(lipgloss.Left, lines...)
}

type contextTableLayout struct {
	scopeW   int
	enabledW int
	labelW   int
	kindW    int
	refreshW int
	ttlW     int
	cacheW   int
}

func computeContextTableLayout(width int) contextTableLayout {
	// At narrow widths, hide some columns
	if width < 50 {
		layout := contextTableLayout{
			scopeW:   0,
			enabledW: 2,
			kindW:    0,
			refreshW: 0,
			ttlW:     0,
			cacheW:   0,
		}
		layout.labelW = maxInt(12, width-3)
		return layout
	}
	layout := contextTableLayout{
		scopeW:   4,
		enabledW: 2,
		kindW:    10,
		refreshW: 12,
		ttlW:     0,
		cacheW:   9,
	}
	separatorCount := 5
	minLabel := 12
	fixed := layout.scopeW + layout.enabledW + layout.kindW + layout.refreshW + layout.cacheW + separatorCount
	layout.labelW = maxInt(minLabel, width-fixed)
	return layout
}

func renderContextHeader(layout contextTableLayout, th Theme) string {
	headerStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(th.TextMuted)).
		Bold(true)

	cells := []string{
		padCell(i18n.T("settings.residual.context.scope"), layout.scopeW),
		padCell(i18n.T("settings.residual.context.on"), layout.enabledW),
		padCell(i18n.T("settings.residual.context.source"), layout.labelW),
		padCell(i18n.T("settings.residual.context.kind"), layout.kindW),
		padCell(i18n.T("settings.residual.context.refresh"), layout.refreshW),
		padCell(i18n.T("settings.residual.context.cache"), layout.cacheW),
	}
	return headerStyle.Render(strings.Join(cells, " "))
}

func renderContextRow(row contextRow, layout contextTableLayout, selected bool, th Theme) string {
	scope := scopeLabel(row)
	enabled := "○"
	enabledColor := th.TextMuted
	if row.Source.Enabled {
		enabled = "●"
		enabledColor = th.Success
	}

	label := row.Source.Label
	if label == "" {
		label = row.Source.ID
	}
	kind := kindLabel(row.Source.Kind)
	refresh := refreshLabel(row.Source.RefreshMode)
	cache := cacheLabel(row.Source.CachePolicy)
	if row.Source.Kind == chatcontext.SourceKindInjection {
		// Refresh/cache semantics don't apply to injection gates.
		refresh = "—"
		cache = "—"
	}

	scopeCell := padCell(scope, layout.scopeW)
	enabledCell := padCell(lipgloss.NewStyle().Foreground(lipgloss.Color(enabledColor)).Render(enabled), layout.enabledW)

	labelStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(th.Text))
	if !row.Source.Enabled {
		labelStyle = labelStyle.Foreground(lipgloss.Color(th.TextMuted))
	}

	cells := []string{
		scopeCell,
		enabledCell,
		padCell(labelStyle.Render(contextTruncateString(label, layout.labelW)), layout.labelW),
		padCell(contextTruncateString(kind, layout.kindW), layout.kindW),
		padCell(contextTruncateString(refresh, layout.refreshW), layout.refreshW),
		padCell(contextTruncateString(cache, layout.cacheW), layout.cacheW),
	}

	line := strings.Join(cells, " ")
	if selected {
		return lipgloss.NewStyle().
			Background(lipgloss.Color(th.BGLighter)).
			Bold(true).
			Render(line)
	}
	return line
}

func (c *ContextSettings) renderContextDetail(width, height int, state *State, th Theme) string {
	rows := c.getRows()
	if len(rows) == 0 {
		return ""
	}
	row, ok := findRowByID(rows, state.ContextDetailSourceID)
	if !ok {
		row = rows[clampInt(state.ContextSelectedItem, 0, len(rows)-1)]
	}

	defaults := c.loader.GetConfig().Defaults
	lines := []string{}

	header := lipgloss.NewStyle().
		Foreground(lipgloss.Color(th.Primary)).
		Bold(true).
		Render(rowLabel(row))
	lines = append(lines, header, "")

	lines = append(lines, renderDetailLine(i18n.T("settings.residual.context.id"), row.Source.ID, th))
	lines = append(lines, renderDetailLine(i18n.T("settings.residual.context.kind"), kindLabel(row.Source.Kind), th))
	lines = append(lines, renderDetailLine(i18n.T("settings.residual.context.enabled"), boolLabel(row.Source.Enabled), th))
	lines = append(lines, renderDetailLine(i18n.T("settings.residual.context.defined_in"), scopeDescription(row), th))

	if row.Source.Kind == chatcontext.SourceKindInjection {
		lines = append(lines, renderDetailLine(i18n.T("settings.residual.context.refresh"), i18n.T("settings.residual.context.not_applicable"), th))
		lines = append(lines, renderDetailLine(i18n.T("settings.residual.context.cache"), i18n.T("settings.residual.context.not_applicable"), th))
		if desc := injectionSourceDescription(row.Source.ID); desc != "" {
			descStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(th.TextMuted)).Width(width - 2)
			lines = append(lines, "", descStyle.Render(desc))
		}
	} else {
		explicitRefresh := refreshLabel(row.Source.RefreshMode)
		effectiveRefresh := refreshLabel(resolveRefreshMode(row.Source, defaults))
		refreshValue := explicitRefresh
		if explicitRefresh != effectiveRefresh {
			refreshValue = i18n.T("settings.residual.context.effective_value", refreshValue, effectiveRefresh)
		}
		lines = append(lines, renderDetailLine(i18n.T("settings.residual.context.refresh"), refreshValue, th))

		explicitCache := cacheLabel(row.Source.CachePolicy)
		effectiveCache := cacheLabel(resolveCachePolicy(row.Source, defaults))
		cacheValue := explicitCache
		if explicitCache != effectiveCache {
			cacheValue = i18n.T("settings.residual.context.effective_value", cacheValue, effectiveCache)
		}
		lines = append(lines, renderDetailLine(i18n.T("settings.residual.context.cache"), cacheValue, th))
	}

	if row.Source.Kind == chatcontext.SourceKindFile {
		path, exists, candidates := resolveFilePath(row.Source.ID)
		if exists {
			lines = append(lines, renderDetailLine(i18n.T("settings.residual.context.resolved_path"), path, th))
		} else if path != "" {
			lines = append(lines, renderDetailLine(i18n.T("settings.residual.context.resolved_path"), i18n.T("settings.residual.context.missing_value", path), th))
		} else if len(candidates) > 0 {
			lines = append(lines, renderDetailLine(i18n.T("settings.residual.context.resolved_path"), i18n.T("settings.residual.context.missing_value", candidates[0]), th))
		} else {
			lines = append(lines, renderDetailLine(i18n.T("settings.residual.context.resolved_path"), i18n.T("settings.residual.common.not_available"), th))
		}
	}

	if chatcontext.IsMCPSourceConfig(row.Source) {
		lines = append(lines, renderDetailLine(i18n.T("settings.residual.context.mcp_server"), row.Source.ServerName, th))
		if row.Source.Kind == chatcontext.SourceKindMCPResource {
			lines = append(lines, renderDetailLine(i18n.T("settings.residual.context.resource_uri"), row.Source.URI, th))
		} else {
			lines = append(lines, renderDetailLine(i18n.T("settings.residual.context.prompt"), row.Source.PromptName, th))
			lines = append(lines, renderDetailLine(i18n.T("settings.residual.context.args"), formatPromptArgs(row.Source.PromptArgs), th))
		}
	}

	if height > 0 && len(lines) > height {
		lines = lines[:height]
	}
	return lipgloss.JoinVertical(lipgloss.Left, lines...)
}

// injectionSourceDescription explains what each injection gate controls and
// when toggling it takes effect.
func injectionSourceDescription(id string) string {
	switch id {
	case chatcontext.SourceIDSkills:
		return i18n.T("settings.residual.context.skills_description")
	case chatcontext.SourceIDWorkspaceEnv:
		return i18n.T("settings.residual.context.workspace_description")
	case chatcontext.SourceIDHookContext:
		return i18n.T("settings.residual.context.hooks_description")
	}
	return ""
}

func (c *ContextSettings) renderMCPServerTitle(width int, th Theme) string {
	titleStyle := lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color(th.Primary)).
		Width(width).
		Align(lipgloss.Center)

	subtitleStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(th.TextMuted)).
		Italic(true).
		Width(width).
		Align(lipgloss.Center)

	title := titleStyle.Render(i18n.T("settings.residual.context.mcp_sources"))
	subtitle := subtitleStyle.Render(i18n.T("settings.residual.context.browse"))

	return lipgloss.JoinVertical(lipgloss.Left, title, "", subtitle)
}

func (c *ContextSettings) renderMCPPickerTitle(width int, state *State, th Theme) string {
	titleStyle := lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color(th.Primary)).
		Width(width).
		Align(lipgloss.Center)

	subtitleStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(th.TextMuted)).
		Italic(true).
		Width(width).
		Align(lipgloss.Center)

	title := titleStyle.Render(i18n.T("settings.residual.context.mcp_sources"))
	serverLine := i18n.T("settings.residual.context.server_value", state.ContextMCPSelectedServer)
	if state.ContextMCPSelectedServer == "" {
		serverLine = i18n.T("settings.residual.context.server_none")
	}
	subtitle := subtitleStyle.Render(serverLine)

	tabs := c.renderMCPTabs(width, state, th)
	return lipgloss.JoinVertical(lipgloss.Left, title, "", subtitle, "", tabs)
}

func (c *ContextSettings) renderMCPArgsTitle(width int, state *State, th Theme) string {
	titleStyle := lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color(th.Primary)).
		Width(width).
		Align(lipgloss.Center)

	subtitleStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(th.TextMuted)).
		Italic(true).
		Width(width).
		Align(lipgloss.Center)

	title := titleStyle.Render(i18n.T("settings.residual.context.prompt_args_title"))
	subtitle := subtitleStyle.Render(fmt.Sprintf("%s:%s", state.ContextMCPArgsServer, state.ContextMCPArgsPromptName))

	return lipgloss.JoinVertical(lipgloss.Left, title, "", subtitle)
}

func (c *ContextSettings) renderMCPTabs(width int, state *State, th Theme) string {
	activeStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(th.Primary)).
		Bold(true)
	inactiveStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(th.TextMuted))

	resources := i18n.T("settings.residual.context.resources")
	prompts := i18n.T("settings.residual.context.prompts")
	if state.ContextMCPTab == 0 {
		resources = activeStyle.Render(resources)
		prompts = inactiveStyle.Render(prompts)
	} else {
		resources = inactiveStyle.Render(resources)
		prompts = activeStyle.Render(prompts)
	}

	row := lipgloss.JoinHorizontal(lipgloss.Center, resources, "   ", prompts)
	return lipgloss.NewStyle().Width(width).Align(lipgloss.Center).Render(row)
}

func (c *ContextSettings) renderMCPServerList(width, height int, state *State, th Theme) string {
	var lines []string

	filterLine := c.renderFilterLine(width, state.ContextMCPFilter, state.ContextMCPFilterMode, th)
	lines = append(lines, filterLine)

	if c.mcpProvider == nil {
		muted := lipgloss.NewStyle().
			Foreground(lipgloss.Color(th.TextMuted)).
			Padding(1, 2)
		lines = append(lines, muted.Render(i18n.T("settings.residual.context.manager_unavailable")))
		return lipgloss.JoinVertical(lipgloss.Left, lines...)
	}

	servers := c.getSortedServers()
	servers = filterServers(servers, state.ContextMCPFilter)

	if len(servers) == 0 {
		muted := lipgloss.NewStyle().
			Foreground(lipgloss.Color(th.TextMuted)).
			Padding(1, 2)
		lines = append(lines, muted.Render(i18n.T("settings.residual.context.no_servers")))
		return lipgloss.JoinVertical(lipgloss.Left, lines...)
	}

	selected := clampInt(state.ContextMCPServerIndex, 0, len(servers)-1)
	listHeight := maxInt(1, height-lipgloss.Height(filterLine))
	start, end := windowBounds(selected, len(servers), listHeight)

	for i := start; i < end; i++ {
		server := servers[i]
		isSelected := i == selected

		line := c.renderMCPServerLine(server, isSelected, width, th)
		lines = append(lines, line)

		if isSelected {
			detail := c.renderMCPServerDetail(server, width, th)
			lines = append(lines, detail)
		}
	}

	return lipgloss.JoinVertical(lipgloss.Left, lines...)
}

func (c *ContextSettings) renderMCPPickerList(width, height int, state *State, th Theme) string {
	var lines []string

	filterLine := c.renderFilterLine(width, state.ContextMCPFilter, state.ContextMCPFilterMode, th)
	lines = append(lines, filterLine)

	if c.mcpProvider == nil {
		muted := lipgloss.NewStyle().
			Foreground(lipgloss.Color(th.TextMuted)).
			Padding(1, 2)
		lines = append(lines, muted.Render(i18n.T("settings.residual.context.manager_unavailable")))
		return lipgloss.JoinVertical(lipgloss.Left, lines...)
	}

	listHeight := maxInt(1, height-lipgloss.Height(filterLine))

	if state.ContextMCPTab == 0 {
		resources := c.getResourcesForServer(state.ContextMCPSelectedServer)
		resources = filterResources(resources, state.ContextMCPFilter)
		if len(resources) == 0 {
			muted := lipgloss.NewStyle().
				Foreground(lipgloss.Color(th.TextMuted)).
				Padding(1, 2)
			lines = append(lines, muted.Render(i18n.T("settings.residual.context.no_resources")))
			return lipgloss.JoinVertical(lipgloss.Left, lines...)
		}

		selected := clampInt(state.ContextMCPResourceIndex, 0, len(resources)-1)
		start, end := windowBounds(selected, len(resources), listHeight)
		for i := start; i < end; i++ {
			resource := resources[i]
			isSelected := i == selected
			line := c.renderMCPResourceLine(resource, isSelected, width, th)
			lines = append(lines, line)
			if isSelected {
				detail := c.renderMCPResourceDetail(resource, width, th)
				lines = append(lines, detail)
			}
		}
		return lipgloss.JoinVertical(lipgloss.Left, lines...)
	}

	prompts := c.getPromptsForServer(state.ContextMCPSelectedServer)
	prompts = filterPrompts(prompts, state.ContextMCPFilter)
	if len(prompts) == 0 {
		muted := lipgloss.NewStyle().
			Foreground(lipgloss.Color(th.TextMuted)).
			Padding(1, 2)
		lines = append(lines, muted.Render(i18n.T("settings.residual.context.no_prompts")))
		return lipgloss.JoinVertical(lipgloss.Left, lines...)
	}

	selected := clampInt(state.ContextMCPPromptIndex, 0, len(prompts)-1)
	start, end := windowBounds(selected, len(prompts), listHeight)
	for i := start; i < end; i++ {
		prompt := prompts[i]
		isSelected := i == selected
		line := c.renderMCPPromptLine(prompt, isSelected, width, th)
		lines = append(lines, line)
		if isSelected {
			detail := c.renderMCPPromptDetail(prompt, width, th)
			lines = append(lines, detail)
		}
	}

	return lipgloss.JoinVertical(lipgloss.Left, lines...)
}

func (c *ContextSettings) renderMCPArgsEditor(width, height int, state *State, th Theme) string {
	args := state.ContextMCPArgsPromptArgs
	selected := clampInt(state.ContextMCPArgsSelected, 0, len(args))

	var lines []string

	if len(args) == 0 {
		muted := lipgloss.NewStyle().
			Foreground(lipgloss.Color(th.TextMuted)).
			Padding(1, 2)
		lines = append(lines, muted.Render(i18n.T("settings.residual.context.no_arguments")))
	} else {
		for i, arg := range args {
			value := ""
			if state.ContextMCPArgsValues != nil {
				value = state.ContextMCPArgsValues[arg.Name]
			}
			line := c.renderMCPArgLine(arg, value, i == selected, width, th)
			lines = append(lines, line)
		}
	}

	addSelected := selected == len(args)
	addStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(th.Primary)).
		Padding(1, 2)
	if addSelected {
		addStyle = addStyle.Bold(true).Background(lipgloss.Color(th.BGLighter))
	}
	lines = append(lines, addStyle.Render(i18n.T("settings.residual.context.add_prompt_source")))

	if height > 0 && len(lines) > height {
		lines = lines[:height]
	}

	return lipgloss.JoinVertical(lipgloss.Left, lines...)
}

func (c *ContextSettings) renderMCPServerLine(server *commands.MCPServerState, selected bool, width int, th Theme) string {
	statusIcon := "○"
	statusColor := th.TextMuted
	if server != nil && server.Connected {
		statusIcon = "●"
		statusColor = th.Success
	}

	label := i18n.T("settings.residual.common.unknown")
	if server != nil && server.Config != nil {
		label = server.Config.Name
	}

	var style lipgloss.Style
	if selected {
		style = lipgloss.NewStyle().
			Foreground(lipgloss.Color(th.Primary)).
			Background(lipgloss.Color(th.BGLighter)).
			Bold(true).
			Padding(0, 2)
	} else {
		style = lipgloss.NewStyle().
			Foreground(lipgloss.Color(th.Text)).
			Padding(0, 2)
	}

	iconStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(statusColor))
	line := iconStyle.Render(statusIcon) + " " + label
	return style.Render(line)
}

func (c *ContextSettings) renderMCPServerDetail(server *commands.MCPServerState, width int, th Theme) string {
	status := i18n.T("settings.residual.common.disconnected")
	if server != nil && server.Connected {
		status = i18n.T("settings.residual.common.connected")
	}

	lastError := i18n.T("settings.residual.common.none")
	if server != nil {
		if server.Error != "" {
			lastError = server.Error
		} else if server.LastError != nil {
			lastError = server.LastError.Error()
		}
	}
	lastError = strings.TrimSpace(lastError)
	if len(lastError) > 120 {
		lastError = lastError[:120] + "..."
	}

	detail := i18n.T("settings.residual.context.status_last_error", status, lastError)
	style := lipgloss.NewStyle().
		Foreground(lipgloss.Color(th.TextMuted)).
		Italic(true).
		Padding(0, 6)
	return style.Render(detail)
}

func (c *ContextSettings) renderMCPResourceLine(resource *mcp.MCPResource, selected bool, width int, th Theme) string {
	label := i18n.T("settings.residual.context.resource_fallback")
	if resource != nil {
		if resource.Name != "" {
			label = resource.Name
		} else if resource.URI != "" {
			label = resource.URI
		}
	}

	var style lipgloss.Style
	if selected {
		style = lipgloss.NewStyle().
			Foreground(lipgloss.Color(th.Primary)).
			Background(lipgloss.Color(th.BGLighter)).
			Bold(true).
			Padding(0, 2)
	} else {
		style = lipgloss.NewStyle().
			Foreground(lipgloss.Color(th.Text)).
			Padding(0, 2)
	}
	return style.Render(label)
}

func (c *ContextSettings) renderMCPResourceDetail(resource *mcp.MCPResource, width int, th Theme) string {
	description := ""
	if resource != nil {
		description = resource.Description
		if description == "" {
			description = resource.URI
		}
	}
	if description == "" {
		description = i18n.T("settings.residual.common.no_description")
	}
	if len(description) > 140 {
		description = description[:140] + "..."
	}

	style := lipgloss.NewStyle().
		Foreground(lipgloss.Color(th.TextMuted)).
		Italic(true).
		Padding(0, 6)
	return style.Render(description)
}

func (c *ContextSettings) renderMCPPromptLine(prompt *mcp.MCPPrompt, selected bool, width int, th Theme) string {
	label := i18n.T("settings.residual.context.prompt_fallback")
	if prompt != nil && prompt.Name != "" {
		label = prompt.Name
	}
	label += formatPromptArgsSummary(prompt)

	var style lipgloss.Style
	if selected {
		style = lipgloss.NewStyle().
			Foreground(lipgloss.Color(th.Primary)).
			Background(lipgloss.Color(th.BGLighter)).
			Bold(true).
			Padding(0, 2)
	} else {
		style = lipgloss.NewStyle().
			Foreground(lipgloss.Color(th.Text)).
			Padding(0, 2)
	}
	return style.Render(label)
}

func (c *ContextSettings) renderMCPPromptDetail(prompt *mcp.MCPPrompt, width int, th Theme) string {
	description := ""
	if prompt != nil {
		description = prompt.Description
	}
	if description == "" {
		description = i18n.T("settings.residual.common.no_description")
	}
	if len(description) > 140 {
		description = description[:140] + "..."
	}
	style := lipgloss.NewStyle().
		Foreground(lipgloss.Color(th.TextMuted)).
		Italic(true).
		Padding(0, 6)
	return style.Render(description)
}

func (c *ContextSettings) renderMCPArgLine(arg mcp.PromptArgument, value string, selected bool, width int, th Theme) string {
	label := arg.Name
	if arg.Required {
		label += " " + i18n.T("settings.residual.common.required")
	}
	content := fmt.Sprintf("%s: %s", label, value)
	var style lipgloss.Style
	if selected {
		style = lipgloss.NewStyle().
			Foreground(lipgloss.Color(th.Primary)).
			Background(lipgloss.Color(th.BGLighter)).
			Bold(true).
			Padding(0, 2)
	} else {
		style = lipgloss.NewStyle().
			Foreground(lipgloss.Color(th.Text)).
			Padding(0, 2)
	}
	return style.Render(content)
}

func (c *ContextSettings) renderFilterLine(width int, query string, active bool, th Theme) string {
	label := i18n.T("settings.residual.context.filter_value", query)
	if !active {
		label += " " + i18n.T("settings.residual.context.press_filter")
	}
	style := lipgloss.NewStyle().
		Foreground(lipgloss.Color(th.TextMuted)).
		Padding(0, 2)
	return style.Render(label)
}

// renderHintBar renders keyboard navigation hints at the bottom
func (c *ContextSettings) renderHintBar(width int, state *State, th Theme) string {
	hintStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(th.TextMuted)).
		Width(width).
		Align(lipgloss.Center)

	keyStyle := lipgloss.NewStyle().
		Background(lipgloss.Color(th.BGLight)).
		Foreground(lipgloss.Color(th.Text)).
		Padding(0, 1).
		Bold(true)

	if width < 45 {
		return "" // Too narrow for hints
	}

	var hints string
	switch state.ContextState {
	case "mcp_servers":
		hints = i18n.T("settings.residual.context.hint_servers",
			keyStyle.Render("↑/↓"),
			keyStyle.Render("Enter"),
			keyStyle.Render("/"),
			keyStyle.Render("Esc"))
	case "mcp_picker":
		hints = i18n.T("settings.residual.context.hint_picker",
			keyStyle.Render("↑/↓"),
			keyStyle.Render("Enter"),
			keyStyle.Render("←/→"),
			keyStyle.Render("Esc"))
	case "mcp_prompt_args":
		hints = i18n.T("settings.residual.context.hint_args",
			keyStyle.Render("↑/↓"),
			keyStyle.Render("Type"),
			keyStyle.Render("Enter"),
			keyStyle.Render("Esc"))
	case "detail":
		hints = i18n.T("settings.residual.context.hint_back", keyStyle.Render("Esc"))
	default:
		if width < 65 {
			hints = i18n.T("settings.residual.context.hint_compact",
				keyStyle.Render("↑/↓"),
				keyStyle.Render("Space"),
				keyStyle.Render("Enter"),
				keyStyle.Render("Esc"))
		} else {
			hints = i18n.T("settings.residual.context.hint_full",
				keyStyle.Render("↑/↓"),
				keyStyle.Render("Space"),
				keyStyle.Render("←/→"),
				keyStyle.Render("C"),
				keyStyle.Render("Enter"),
				keyStyle.Render("G/P"),
				keyStyle.Render("Esc"))
		}
	}

	return hintStyle.Render(hints)
}

// HandleKey handles keyboard input for context settings
