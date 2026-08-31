package settings

import (
	"fmt"
	"image/color"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/i18n"
)

// ============================================================================
// AGENTS RENDER — top-level dispatcher
// ============================================================================

func (s *AgentsSettings) Render(width, height int, state *State, th Theme) string {
	var rendered string
	switch state.AgentsState {
	case "create", "edit":
		rendered = s.renderForm(width, height, state, th)
	case "action_menu":
		rendered = s.renderActionMenu(width, height, state, th)
	case "preview":
		rendered = s.renderPreview(width, height, state, th)
	case "chat":
		rendered = s.renderAgentChat(width, height, state, th)
	default:
		rendered = s.renderList(width, height, state, th)
	}
	return i18n.SettingsResidualModelsText(rendered)
}

// ============================================================================
// LIST VIEW  — profile-first, clean grouped rows
// ============================================================================

func (s *AgentsSettings) renderList(width, height int, state *State, th Theme) string {
	const pad = 1
	iw := maxInt(20, width-4)

	var lines []string

	// ── title ────────────────────────────────────────────────────────────────
	titleStyle := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color(th.Primary)).
		Width(iw).Align(lipgloss.Center)
	lines = append(lines, titleStyle.Render(i18n.T("settings.residual_final.agents.title")))
	lines = append(lines, "")

	// ── profile info banner ───────────────────────────────────────────────────
	activeProfile := ""
	if s.profileManager != nil {
		if p, err := s.profileManager.GetActiveProfile(); err == nil && p != nil {
			activeProfile = p.Name
		}
	}
	if activeProfile != "" {
		bannerPad := 2
		if width < 50 {
			bannerPad = 1
		}
		bannerSuffix := ""
		if width >= 70 {
			bannerSuffix = lipgloss.NewStyle().Foreground(lipgloss.Color(th.TextMuted)).Render(i18n.T("settings.residual_final.agents.active_profile_suffix"))
		}
		profileBanner := lipgloss.NewStyle().
			Background(lipgloss.Color(th.BGLight)).Foreground(lipgloss.Color(th.Text)).
			Width(iw).Padding(0, bannerPad).
			Render(i18n.T("settings.residual_final.agents.active_profile",
				lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color(th.Primary)).Render(activeProfile),
				bannerSuffix))
		lines = append(lines, profileBanner, "")
	}

	// ── new agent button ──────────────────────────────────────────────────────
	newSel := state.AgentsSelected == -1
	newStyle := lipgloss.NewStyle().Width(iw).Padding(0, 2)
	if newSel {
		newStyle = newStyle.Background(lipgloss.Color(th.Primary)).Foreground(lipgloss.Color(th.BG)).Bold(true)
	} else {
		newStyle = newStyle.Foreground(lipgloss.Color(th.Primary)).Background(lipgloss.Color(th.BGLighter))
	}
	lines = append(lines, newStyle.Render(i18n.T("settings.residual_final.agents.new")), "")

	// ── agents — split built-in / custom ─────────────────────────────────────
	var builtins, custom []indexedAgent
	for i, a := range s.config.Agents {
		if a.Builtin {
			builtins = append(builtins, indexedAgent{i, a})
		} else {
			custom = append(custom, indexedAgent{i, a})
		}
	}

	countLabel := func(agents []indexedAgent) string {
		total := len(agents)
		disabled := 0
		for _, ia := range agents {
			if ia.agent.Disabled {
				disabled++
			}
		}
		if disabled > 0 {
			return i18n.T("settings.residual_final.agents.count_with_disabled", total-disabled, disabled)
		}
		return i18n.T("settings.residual_final.agents.count", total)
	}

	sectionLabel := func(label string, agents []indexedAgent) string {
		return lipgloss.NewStyle().Foreground(lipgloss.Color(th.TextMuted)).Bold(true).
			Padding(0, 2).Render(label + "  " + countLabel(agents))
	}

	// Built-in agents
	if len(builtins) > 0 {
		lines = append(lines, sectionLabel(i18n.T("settings.residual_final.agents.builtin"), builtins), "")
		for _, ia := range builtins {
			lines = append(lines, s.renderAgentRow(ia.agent, ia.idx, state, iw, th))
		}
		lines = append(lines, "")
	}

	// Custom agents
	if len(custom) > 0 {
		lines = append(lines, sectionLabel(i18n.T("settings.residual_final.agents.custom"), custom), "")
		for _, ia := range custom {
			lines = append(lines, s.renderAgentRow(ia.agent, ia.idx, state, iw, th))
		}
		lines = append(lines, "")
	}

	// ── hint bar ──────────────────────────────────────────────────────────────
	k := func(key string) string {
		return lipgloss.NewStyle().Background(lipgloss.Color(th.BGLight)).
			Foreground(lipgloss.Color(th.Text)).Padding(0, 1).Bold(true).Render(key)
	}
	var hintText string
	if width < 45 {
		hintText = ""
	} else if width < 65 {
		hintText = i18n.T("settings.residual_models.agent.hints.compact",
			k("↑"), k("↓"), k("Enter"), k("n"), k("Esc"))
	} else {
		hintText = i18n.T("settings.residual_models.agent.hints.list",
			k("↑"), k("↓"), k("Enter"), k("d"), k("n"), k("x"), k("a"), k("Esc"))
	}
	if hintText != "" {
		hints := lipgloss.NewStyle().Foreground(lipgloss.Color(th.TextMuted)).Width(iw).Align(lipgloss.Center).Render(hintText)
		lines = append(lines, hints)
	}

	content := lipgloss.JoinVertical(lipgloss.Left, lines...)
	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).BorderForeground(lipgloss.Color(th.Border)).
		Width(maxInt(20, width-2)).Padding(0, pad).
		Render(content)
}

type indexedAgent struct {
	idx   int
	agent CustomAgentEntry
}

func (s *AgentsSettings) renderAgentRow(agent CustomAgentEntry, idx int, state *State, width int, th Theme) string {
	isDefault := agent.ID == s.config.DefaultAgent
	isSel := idx == state.AgentsSelected
	isDisabled := agent.Disabled

	icon := agent.Icon
	if icon == "" {
		icon = "●"
	}

	// ── Colour palette for this row ───────────────────────────────────────────
	var (
		bgMain  color.Color
		fgMain  color.Color
		fgMuted color.Color
		fgBadge color.Color
	)
	if isSel {
		bgMain = lipgloss.Color(th.Primary)
		fgMain = lipgloss.Color(th.BG)
		fgMuted = lipgloss.Color(th.BG) // slightly dimmer on highlight
		fgBadge = lipgloss.Color(th.BG)
	} else if isDisabled {
		bgMain = lipgloss.Color("")
		fgMain = lipgloss.Color(th.TextMuted)
		fgMuted = lipgloss.Color(th.TextMuted)
		fgBadge = lipgloss.Color(th.TextMuted)
	} else {
		bgMain = lipgloss.Color("")
		fgMain = lipgloss.Color(th.Text)
		fgMuted = lipgloss.Color(th.TextMuted)
		fgBadge = lipgloss.Color(th.Primary)
	}

	// Helper: create a styled, fixed-width line with the row's background
	lineStyle := func() lipgloss.Style {
		st := lipgloss.NewStyle().Width(width).Padding(0, 1)
		if isSel {
			st = st.Background(bgMain)
		}
		return st
	}

	// ── Default mark ──────────────────────────────────────────────────────────
	defaultMark := "  "
	if isDefault {
		if isSel {
			defaultMark = "✓ "
		} else {
			defaultMark = lipgloss.NewStyle().Foreground(lipgloss.Color(th.Success)).Bold(true).Render("✓ ")
		}
	}

	// ── Disabled badge ────────────────────────────────────────────────────────
	disabledTag := ""
	if isDisabled {
		if isSel {
			disabledTag = i18n.T("settings.residual_final.agents.disabled_suffix")
		} else {
			disabledTag = " " + lipgloss.NewStyle().Foreground(lipgloss.Color(th.TextMuted)).Render(i18n.T("settings.residual_final.agents.disabled_badge"))
		}
	}

	// ── Model source badge ────────────────────────────────────────────────────
	modelBadge := ""
	roleAliasBadge := ""

	// Resolve the actual model from the profile + role alias
	var resolvedProvider, resolvedModel string
	if s.profileManager != nil {
		_, _, resolvedProvider, resolvedModel = s.profileManager.GetResolvedModel(
			agent.ProfileID,
			agent.RoleAlias,
		)
	}

	// Show role alias badge
	if agent.RoleAlias != "" {
		roleBadge := i18n.T("settings.residual_final.agents.role_badge", agent.RoleAlias)
		if isSel {
			roleAliasBadge = "  " + roleBadge
		} else {
			roleAliasBadge = "  " + lipgloss.NewStyle().Foreground(fgBadge).Render(roleBadge)
		}
	}

	// Show resolved model badge
	if resolvedProvider != "" && resolvedModel != "" {
		modelShort := shortenModel(resolvedModel)
		badge := "[" + resolvedProvider + "/" + modelShort + "]"
		if isSel {
			modelBadge = "  " + badge
		} else {
			modelBadge = "  " + lipgloss.NewStyle().Foreground(fgBadge).Render(badge)
		}
	} else if agent.ProfileID != "" {
		// Fallback: show profile ID if model couldn't be resolved
		badge := i18n.T("settings.residual_final.agents.profile_badge", agent.ProfileID)
		if isSel {
			modelBadge = "  " + badge
		} else {
			modelBadge = "  " + lipgloss.NewStyle().Foreground(fgBadge).Render(badge)
		}
	} else {
		// Using active profile
		badge := i18n.T("settings.residual_final.agents.active_profile_badge")
		if isSel {
			modelBadge = "  " + badge
		} else {
			modelBadge = "  " + lipgloss.NewStyle().Foreground(fgBadge).Render(badge)
		}
	}

	// ── Build each line independently — NO embedded \n in Render calls ────────
	var rows []string

	// Calculate available width for name
	// width is the total width of the row.
	// lineStyle has Padding(0, 1) so effective width is width - 2.
	availWidth := width - 2
	dmWidth := lipgloss.Width(defaultMark)
	iconWidth := lipgloss.Width(icon)
	mbWidth := lipgloss.Width(modelBadge)
	dtWidth := lipgloss.Width(disabledTag)

	// fixedWidth = dmWidth + iconWidth + 1 (space) + mbWidth + dtWidth
	fixedWidth := dmWidth + iconWidth + 1 + mbWidth + dtWidth
	nameWidth := maxInt(3, availWidth-fixedWidth)

	name := agent.Name
	if lipgloss.Width(name) > nameWidth {
		if nameWidth > 3 {
			runes := []rune(name)
			for i := len(runes); i >= 0; i-- {
				shortened := string(runes[:i]) + "…"
				if lipgloss.Width(shortened) <= nameWidth {
					name = shortened
					break
				}
			}
		} else {
			name = "…"
		}
	}

	// Line 1: main info line
	mainText := defaultMark + icon + " " + name + roleAliasBadge + modelBadge + disabledTag
	mainStyle := lineStyle().Foreground(fgMain)
	if isSel {
		mainStyle = mainStyle.Bold(true)
	}
	if isDisabled && !isSel {
		mainStyle = mainStyle.Strikethrough(false) // keep readable, just muted
	}
	rows = append(rows, mainStyle.Render(mainText))

	// Lines 2+ (only when selected): description + extras
	if isSel {
		if agent.Description != "" {
			desc := agent.Description
			maxLen := maxInt(10, width-8)
			if len(desc) > maxLen {
				desc = desc[:maxLen-3] + "..."
			}
			descStyle := lineStyle().Foreground(fgMuted).Italic(true).PaddingLeft(5)
			rows = append(rows, descStyle.Render(desc))
		}

		extras := ""
		// Show resolved model info
		if resolvedProvider != "" && resolvedModel != "" {
			modelShort := shortenModel(resolvedModel)
			extras += fmt.Sprintf("  model:%s/%s", resolvedProvider, modelShort)
		}
		if agent.RoleAlias != "" {
			extras += "  role:" + agent.RoleAlias
		}
		if agent.ProfileID != "" {
			extras += "  profile:" + agent.ProfileID
		} else {
			extras += "  profile:active"
		}
		if n := len(agent.Tools); n > 0 {
			extras += fmt.Sprintf("  tools:%d", n)
		}
		if extras != "" {
			extrasStyle := lineStyle().Foreground(fgMuted).PaddingLeft(5)
			rows = append(rows, extrasStyle.Render(extras))
		}
	}

	return lipgloss.JoinVertical(lipgloss.Left, rows...)
}

// ============================================================================
// ACTION MENU
// ============================================================================

func (s *AgentsSettings) renderActionMenu(width, height int, state *State, th Theme) string {
	if state.AgentsSelected < 0 || state.AgentsSelected >= len(s.config.Agents) {
		return ""
	}
	agent := s.config.Agents[state.AgentsSelected]

	icon := agent.Icon
	if icon == "" {
		icon = "●"
	}

	var lines []string

	amPad := 2
	amDetailPad := 4
	if width < 50 {
		amPad = 1
		amDetailPad = 2
	}

	hintStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(th.Text)).Background(lipgloss.Color(th.BGLight)).
		Bold(true).Width(maxInt(20, width-4)).Padding(0, amPad)
	lines = append(lines, hintStyle.Render(i18n.T("settings.residual_final.agents.action_hint")))
	lines = append(lines, "")

	titleStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(th.Primary)).Bold(true).Padding(1, 0)
	lines = append(lines, titleStyle.Render(fmt.Sprintf("%s %s", icon, agent.Name)))

	infoStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(th.Text)).Padding(0, amPad)
	mutedStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(th.TextMuted)).Padding(0, amDetailPad)

	// Resolve model from profile
	var resolvedProv, resolvedMod string
	if s.profileManager != nil {
		_, _, resolvedProv, resolvedMod = s.profileManager.GetResolvedModel(
			agent.ProfileID,
			agent.RoleAlias,
		)
	}
	if resolvedProv != "" && resolvedMod != "" {
		modelShort := shortenModel(resolvedMod)
		lines = append(lines, infoStyle.Render(i18n.T("settings.residual_final.agents.model_value", resolvedProv, modelShort)))
	} else if agent.ProfileID != "" {
		lines = append(lines, infoStyle.Render(i18n.T("settings.residual_final.agents.model_via_profile", agent.ProfileID)))
	} else {
		lines = append(lines, infoStyle.Render(i18n.T("settings.residual_final.agents.model_via_active_profile")))
	}
	if agent.RoleAlias != "" {
		lines = append(lines, infoStyle.Render(i18n.T("settings.residual_final.agents.role_value", agent.RoleAlias)))
	}
	lines = append(lines, mutedStyle.Render(i18n.T("settings.residual_final.agents.tools_hooks_count", len(agent.Tools), len(agent.Hooks))))
	lines = append(lines, "")

	actions := []struct{ label, key string }{
		{i18n.T("settings.residual_final.agents.action.edit"), "e"},
		{i18n.T("settings.residual_final.agents.action.default"), "d"},
		{i18n.T("settings.residual_final.agents.action.clone"), "c"},
		{i18n.T("settings.residual_final.common.delete"), "del"},
		{i18n.T("settings.residual_final.agents.action.preview"), "p"},
	}
	if agent.Disabled {
		actions = append(actions, struct{ label, key string }{i18n.T("settings.residual_final.agents.action.enable"), "x"})
	} else {
		actions = append(actions, struct{ label, key string }{i18n.T("settings.residual_final.agents.action.disable"), "x"})
	}
	lines = append(lines, lipgloss.NewStyle().Foreground(lipgloss.Color(th.Primary)).Bold(true).Render(i18n.T("settings.residual_final.agents.actions")))
	lines = append(lines, "")

	actionLabelWidth := 20
	if width < 50 {
		actionLabelWidth = maxInt(10, width-10)
	}
	for i, action := range actions {
		isSel := i == state.AgentsActionChoice
		prefix := "  "
		if isSel {
			prefix = "▶ "
		}
		style := lipgloss.NewStyle().Padding(0, amPad)
		if isSel {
			style = style.Foreground(lipgloss.Color(th.BG)).Background(lipgloss.Color(th.Primary)).Bold(true)
		} else {
			style = style.Foreground(lipgloss.Color(th.Text))
		}
		kStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(th.TextMuted))
		lines = append(lines, style.Render(fmt.Sprintf("%s%-*s %s", prefix, actionLabelWidth, action.label, kStyle.Render(action.key))))
	}

	content := lipgloss.JoinVertical(lipgloss.Left, lines...)
	return lipgloss.NewStyle().Width(maxInt(20, width)).Height(maxInt(5, height)).Padding(1, amPad).Render(content)
}

// ============================================================================
// EDIT / CREATE FORM
// ============================================================================
//
// Tab 0 - Basic: fields 0-9
//   0  ID           text
//   1  Name         text
//   2  Description  text
//   3  Icon         text
//   4  Profile      dropdown
//   5  Role Alias   dropdown
//   6  UseOverride  toggle (Space/Enter)
//   7  Provider     dropdown  (only shown when UseOverride)
//   8  Model        dropdown  (only shown when UseOverride)
//   9  System Prompt text multiline
// Tab 1 - Tools
// Tab 2 - Hooks
// Tab 3 - Capabilities

func (s *AgentsSettings) renderForm(width, height int, state *State, th Theme) string {
	var lines []string

	isEdit := state.AgentsState == "edit"
	formTitle := i18n.T("settings.residual_final.agents.form.create")
	if isEdit {
		formTitle = i18n.T("settings.residual_final.agents.form.edit")
	}

	// ── title bar ─────────────────────────────────────────────────────────────
	titleBar := lipgloss.NewStyle().
		Foreground(lipgloss.Color(th.BG)).Background(lipgloss.Color(th.Primary)).
		Bold(true).Width(width).Padding(0, 2).
		Render(formTitle)
	lines = append(lines, titleBar)

	// ── hints bar ─────────────────────────────────────────────────────────────
	k := func(key string) string {
		return lipgloss.NewStyle().Background(lipgloss.Color(th.BGLight)).
			Foreground(lipgloss.Color(th.Text)).Padding(0, 1).Bold(true).Render(key)
	}
	hintLine := i18n.T("settings.residual_models.agent.hints.form",
		k("↑"), k("↓"), k("←"), k("→"), k("Enter"), k("Ctrl+S"), k("Esc"))
	lines = append(lines, lipgloss.NewStyle().Foreground(lipgloss.Color(th.TextMuted)).
		Width(width).Render(hintLine))
	lines = append(lines, "")

	// ── tab strip ─────────────────────────────────────────────────────────────
	tabs := []string{
		i18n.T("settings.residual_final.agents.tab.basic"),
		i18n.T("settings.residual_final.common.tools"),
		i18n.T("settings.residual_final.common.hooks"),
		i18n.T("settings.residual_final.agents.tab.capabilities"),
	}
	var tabItems []string
	for i, tab := range tabs {
		ts := lipgloss.NewStyle().Padding(0, 2)
		if i == state.AgentsFormTab {
			ts = ts.Foreground(lipgloss.Color(th.BG)).Background(lipgloss.Color(th.Primary)).Bold(true)
		} else {
			ts = ts.Foreground(lipgloss.Color(th.TextMuted)).Background(lipgloss.Color(th.BGLighter))
		}
		tabItems = append(tabItems, ts.Render(tab))
	}
	lines = append(lines, lipgloss.JoinHorizontal(lipgloss.Top, tabItems...), "")

	// ── tab content ───────────────────────────────────────────────────────────
	switch state.AgentsFormTab {
	case 0:
		lines = append(lines, s.renderBasicTab(width, height, state, th)...)
	case 1:
		lines = append(lines, s.renderToolsTab(width, height, state, th)...)
	case 2:
		lines = append(lines, s.renderHooksTab(width, height, state, th)...)
	case 3:
		lines = append(lines, s.renderCapabilitiesTab(width, height, state, th)...)
	}

	// ── validation error ──────────────────────────────────────────────────────
	if state.AgentsFormError != "" {
		lines = append(lines, "")
		errStyle := lipgloss.NewStyle().
			Foreground(lipgloss.Color(th.Error)).Bold(true).Padding(0, 2)
		lines = append(lines, errStyle.Render("⚠  "+state.AgentsFormError))
	}

	// ── save/cancel buttons ───────────────────────────────────────────────────
	lines = append(lines, "")
	saveBtn := lipgloss.NewStyle().
		Background(lipgloss.Color(th.Success)).Foreground(lipgloss.Color(th.BG)).
		Padding(0, 3).Bold(true).Render(i18n.T("settings.residual_final.agents.save_button"))
	cancelBtn := lipgloss.NewStyle().
		Background(lipgloss.Color(th.BGLight)).Foreground(lipgloss.Color(th.Text)).
		Padding(0, 3).Render(i18n.T("settings.residual_final.agents.cancel_button"))
	lines = append(lines, lipgloss.JoinHorizontal(lipgloss.Top, "  ", saveBtn, "  ", cancelBtn))

	return lipgloss.JoinVertical(lipgloss.Left, lines...)
}

// ── Basic Info Tab ──────────────────────────────────────────────────────────

func (s *AgentsSettings) renderBasicTab(width, height int, state *State, th Theme) []string {
	var lines []string
	innerWidth := maxInt(20, width-6)

	renderField := func(idx int, label, value string, isText, isLong bool, hint string) {
		isSel := state.AgentsFormField == idx && state.AgentsFormTab == 0
		isEditing := isSel && state.AgentsFormEditing && isText

		prefix := "  "
		if isSel {
			prefix = "▶ "
		}
		labelColor := th.TextMuted
		if isSel {
			labelColor = th.Primary
		}
		lines = append(lines, lipgloss.NewStyle().Foreground(lipgloss.Color(labelColor)).
			Bold(isSel).Padding(0, 1).Render(prefix+label))

		var fieldStyle lipgloss.Style
		if isSel && isEditing {
			fieldStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color(th.Text)).Background(lipgloss.Color(th.BGLight)).
				Border(lipgloss.RoundedBorder()).BorderForeground(lipgloss.Color("#FFFF00")).
				Width(innerWidth).Padding(0, 1)
		} else if isSel {
			fieldStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color(th.Text)).Background(lipgloss.Color(th.BGLight)).
				Border(lipgloss.RoundedBorder()).BorderForeground(lipgloss.Color(th.Primary)).
				Width(innerWidth).Padding(0, 1)
		} else {
			fieldStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color(th.Text)).Background(lipgloss.Color(th.BGLighter)).
				Border(lipgloss.RoundedBorder()).BorderForeground(lipgloss.Color(th.Border)).
				Width(innerWidth).Padding(0, 1)
		}

		v := value
		if !isText {
			arrowStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(th.Primary)).Bold(true)
			left, right := "  ", "  "
			if isSel {
				left = arrowStyle.Render("◀ ")
				right = arrowStyle.Render(" ▶")
			}
			v = left + v + right
		} else if isSel && !isEditing {
			if v == "" {
				v = lipgloss.NewStyle().Foreground(lipgloss.Color(th.TextMuted)).Italic(true).Render(i18n.T("settings.residual_final.agents.type_placeholder"))
			}
		} else if isEditing && state.AgentsCursorPos <= len(value) {
			v = v[:state.AgentsCursorPos] + "█" + v[state.AgentsCursorPos:]
		}

		if isLong {
			fieldStyle = fieldStyle.Height(5)
			v = wordWrap(v, innerWidth-3)
		}
		lines = append(lines, fieldStyle.Render(v))

		editHint := ""
		if isSel && isText && !isEditing {
			editHint = i18n.T("settings.residual_final.agents.type_hint")
		} else if isSel && isEditing {
			editHint = i18n.T("settings.residual_final.agents.stop_hint")
		}
		lines = append(lines, lipgloss.NewStyle().Foreground(lipgloss.Color(th.TextMuted)).Italic(true).
			Padding(0, 0, 0, 4).Render(hint+editHint), "")
	}

	renderDropdown := func(idx int, label, value, hint string) {
		renderField(idx, label, value, false, false, hint)
	}

	renderToggle := func(idx int, label string, on bool, hint string) {
		isSel := state.AgentsFormField == idx && state.AgentsFormTab == 0
		prefix := "  "
		if isSel {
			prefix = "▶ "
		}
		labelColor := th.TextMuted
		if isSel {
			labelColor = th.Primary
		}
		lines = append(lines, lipgloss.NewStyle().Foreground(lipgloss.Color(labelColor)).
			Bold(isSel).Padding(0, 1).Render(prefix+label))

		checkMark := i18n.T("settings.residual_final.agents.explicit_model_off")
		checkStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(th.Text)).Background(lipgloss.Color(th.BGLighter)).
			Border(lipgloss.RoundedBorder()).BorderForeground(lipgloss.Color(th.Border)).
			Width(innerWidth).Padding(0, 1)
		if on {
			checkMark = i18n.T("settings.residual_final.agents.explicit_model_on")
			checkStyle = checkStyle.Background(lipgloss.Color(th.BGLight)).
				BorderForeground(lipgloss.Color(th.Warning))
		}
		if isSel {
			checkStyle = checkStyle.BorderForeground(lipgloss.Color(th.Primary)).
				Background(lipgloss.Color(th.BGLight))
		}
		lines = append(lines, checkStyle.Render(checkMark))
		lines = append(lines, lipgloss.NewStyle().Foreground(lipgloss.Color(th.TextMuted)).Italic(true).
			Padding(0, 0, 0, 4).Render(hint), "")
	}

	profileIDs := s.GetAvailableProfileIDs()
	roleAliases := GetAvailableRoleAliases()

	profileDisplay := state.AgentsFormProfileID
	if profileDisplay == "" {
		profileDisplay = i18n.T("settings.residual_final.agents.active_profile_parenthesized")
	}
	roleDisplay := state.AgentsFormRoleAlias
	if roleDisplay == "" {
		roleDisplay = i18n.T("settings.residual_final.common.none_parenthesized")
	}

	// ── Section: Identity ────────────────────────────────────────────────────
	secStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(th.TextMuted)).Bold(true).Padding(0, 2)
	lines = append(lines, secStyle.Render(i18n.T("settings.residual_final.agents.identity")), "")

	renderField(0, "ID", state.AgentsFormID, true, false, i18n.T("settings.residual_final.agents.id_hint"))
	renderField(1, i18n.T("settings.residual_final.common.name"), state.AgentsFormName, true, false, i18n.T("settings.residual_final.agents.name_hint"))
	renderField(2, i18n.T("settings.residual_final.common.description"), state.AgentsFormDescription, true, false, i18n.T("settings.residual_final.agents.description_hint"))
	renderField(3, i18n.T("settings.residual_final.agents.icon"), state.AgentsFormIcon, true, false, i18n.T("settings.residual_final.agents.icon_hint"))

	// ── Section: Model Source ────────────────────────────────────────────────
	lines = append(lines, secStyle.Render(i18n.T("settings.residual_final.agents.model_source")), "")

	// Explain the profile-first approach
	infoBox := lipgloss.NewStyle().
		Background(lipgloss.Color(th.BGLighter)).Foreground(lipgloss.Color(th.TextMuted)).
		Border(lipgloss.RoundedBorder()).BorderForeground(lipgloss.Color(th.Border)).
		Width(innerWidth).Padding(0, 1).Italic(true).
		Render(i18n.T("settings.residual_final.agents.model_source_description"))
	lines = append(lines, infoBox, "")

	renderDropdown(4, i18n.T("settings.residual_final.common.profile"), profileDisplay,
		i18n.T("settings.residual_final.agents.profile_hint", len(profileIDs)))
	renderDropdown(5, i18n.T("settings.residual_final.agents.role_alias"), roleDisplay,
		i18n.T("settings.residual_final.agents.role_hint", len(roleAliases)))

	// Pin toggle
	renderToggle(6, i18n.T("settings.residual_final.agents.pin_model"), state.AgentsFormUseOverride,
		i18n.T("settings.residual_final.agents.pin_model_hint"))

	// Provider + Model only when pinned
	if state.AgentsFormUseOverride {
		lines = append(lines, secStyle.Render(i18n.T("settings.residual_final.agents.pinned_model")), "")
		renderDropdown(7, i18n.T("settings.residual_final.common.provider"), state.AgentsFormProvider,
			i18n.T("settings.residual_final.agents.available_hint", len(s.providerNames)))
		renderDropdown(8, i18n.T("settings.residual_final.common.model"), state.AgentsFormModel,
			i18n.T("settings.residual_final.agents.models_for_hint", len(s.availableModels[state.AgentsFormProvider]), state.AgentsFormProvider))
	}

	// ── Section: Instructions ────────────────────────────────────────────────
	lines = append(lines, secStyle.Render(i18n.T("settings.residual_final.agents.instructions")), "")
	renderField(9, i18n.T("settings.residual_final.agents.system_prompt"), state.AgentsFormSystemPrompt, true, true, i18n.T("settings.residual_final.agents.system_prompt_hint"))

	return lines
}

// ── Tools Tab ────────────────────────────────────────────────────────────────

func (s *AgentsSettings) renderToolsTab(width, height int, state *State, th Theme) []string {
	var lines []string
	innerWidth := maxInt(20, width-6)

	filterStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(th.TextMuted))
	if state.AgentsToolsFiltering {
		filterStyle = filterStyle.Foreground(lipgloss.Color(th.Primary)).Bold(true)
	}
	filterDisplay := state.AgentsToolsFilter
	if state.AgentsToolsFiltering {
		filterDisplay += "█"
	}
	lines = append(lines, lipgloss.NewStyle().Foreground(lipgloss.Color(th.Primary)).Bold(true).
		Render(i18n.T("settings.residual_final.agents.tools_heading")))
	lines = append(lines, filterStyle.Render(i18n.T("settings.residual_final.agents.filter_value", filterDisplay)), "")

	allEnabled := len(state.AgentsFormTools) == 1 && state.AgentsFormTools[0] == "*"
	allSel := state.AgentsToolsSelected == 0 && !state.AgentsToolsFiltering
	checkbox := "[ ]"
	if allEnabled {
		checkbox = "[✓]"
	}
	allStyle := lipgloss.NewStyle().Padding(0, 2)
	if allSel {
		allStyle = allStyle.Foreground(lipgloss.Color(th.BG)).Background(lipgloss.Color(th.Primary)).Bold(true)
	} else {
		allStyle = allStyle.Foreground(lipgloss.Color(th.Text))
	}
	lines = append(lines, allStyle.Render(checkbox+i18n.T("settings.residual_final.agents.enable_all_tools")), "")

	orderedTools := s.getOrderedToolList()
	filter := strings.ToLower(state.AgentsToolsFilter)
	var filtered []AgentToolInfo
	for _, t := range orderedTools {
		if filter == "" || strings.Contains(strings.ToLower(t.Name), filter) ||
			strings.Contains(strings.ToLower(t.Description), filter) {
			filtered = append(filtered, t)
		}
	}

	if filter != "" && len(filtered) == 0 {
		lines = append(lines, lipgloss.NewStyle().Foreground(lipgloss.Color(th.TextMuted)).Italic(true).
			Padding(0, 4).Render(i18n.T("settings.residual_final.agents.no_tools_match")))
		return lines
	}

	seen := map[string]bool{}
	var order []string
	groups := map[string][]AgentToolInfo{}
	for _, t := range filtered {
		cat := t.Category
		if cat == "" {
			cat = "other"
		}
		if !seen[cat] {
			seen[cat] = true
			order = append(order, cat)
		}
		groups[cat] = append(groups[cat], t)
	}

	itemIndex := 1
	catLabelStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(th.TextMuted)).Bold(true).Padding(0, 2)

	for _, cat := range order {
		tools := groups[cat]
		catTitle := i18n.T("settings.residual_final.agents.category_tools", strings.ToUpper(cat[:1])+cat[1:])
		lines = append(lines, catLabelStyle.Render(catTitle))

		for _, tool := range tools {
			isSel := state.AgentsToolsSelected == itemIndex && !state.AgentsToolsFiltering
			isEnabled := allEnabled || contains(state.AgentsFormTools, tool.Name)

			cb := "[ ]"
			if isEnabled {
				cb = "[✓]"
			}

			itemStyle := lipgloss.NewStyle().Padding(0, 4).Width(innerWidth)
			if isSel {
				itemStyle = itemStyle.Foreground(lipgloss.Color(th.BG)).Background(lipgloss.Color(th.Primary))
			} else {
				itemStyle = itemStyle.Foreground(lipgloss.Color(th.Text))
			}

			desc := tool.Description
			nameFieldWidth := 22
			if innerWidth < 50 {
				nameFieldWidth = minInt(22, maxInt(8, innerWidth-12))
			}
			maxDescLen := maxInt(5, innerWidth-nameFieldWidth-8)
			if len(desc) > maxDescLen {
				desc = desc[:maxInt(0, maxDescLen-3)] + "..."
			}
			if innerWidth < 40 {
				desc = "" // hide description at very narrow widths
			}

			nameField := fmt.Sprintf("%-*s", nameFieldWidth, tool.Name)
			if desc != "" {
				lines = append(lines, itemStyle.Render(fmt.Sprintf("%s %s  %s", cb, nameField, desc)))
			} else {
				lines = append(lines, itemStyle.Render(fmt.Sprintf("%s %s", cb, nameField)))
			}
			itemIndex++
		}
		lines = append(lines, "")
	}

	return lines
}

// ── Hooks Tab ────────────────────────────────────────────────────────────────

func (s *AgentsSettings) renderHooksTab(width, height int, state *State, th Theme) []string {
	var lines []string
	innerWidth := maxInt(20, width-6)

	lines = append(lines, lipgloss.NewStyle().Foreground(lipgloss.Color(th.Primary)).Bold(true).
		Render(i18n.T("settings.residual_final.agents.hooks_heading")), "")

	orderedHooks := s.getOrderedHookList()
	types := map[string][]AgentHookInfo{}
	var typeOrder []string
	seenType := map[string]bool{}
	for _, h := range orderedHooks {
		t := h.Type
		if t == "" {
			t = "other"
		}
		if !seenType[t] {
			seenType[t] = true
			typeOrder = append(typeOrder, t)
		}
		types[t] = append(types[t], h)
	}

	itemIndex := 0
	catLabelStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(th.TextMuted)).Bold(true).Padding(0, 2)

	for _, t := range typeOrder {
		hooks := types[t]
		catTitle := i18n.T("settings.residual_final.agents.category_hooks", strings.ToUpper(t[:1])+t[1:])
		lines = append(lines, catLabelStyle.Render(catTitle))

		for _, hook := range hooks {
			isSel := state.AgentsHooksSelected == itemIndex
			isEnabled := contains(state.AgentsFormHooks, hook.Name)

			cb := "[ ]"
			if isEnabled {
				cb = "[✓]"
			}

			itemStyle := lipgloss.NewStyle().Padding(0, 4).Width(innerWidth)
			if isSel {
				itemStyle = itemStyle.Foreground(lipgloss.Color(th.BG)).Background(lipgloss.Color(th.Primary))
			} else {
				itemStyle = itemStyle.Foreground(lipgloss.Color(th.Text))
			}

			desc := hook.Description
			hookNameFieldWidth := 26
			if innerWidth < 50 {
				hookNameFieldWidth = minInt(26, maxInt(8, innerWidth-12))
			}
			maxDescLen := maxInt(5, innerWidth-hookNameFieldWidth-8)
			if len(desc) > maxDescLen {
				desc = desc[:maxInt(0, maxDescLen-3)] + "..."
			}
			if innerWidth < 40 {
				desc = ""
			}

			nameField := fmt.Sprintf("%-*s", hookNameFieldWidth, hook.Name)
			if desc != "" {
				lines = append(lines, itemStyle.Render(fmt.Sprintf("%s %s  %s", cb, nameField, desc)))
			} else {
				lines = append(lines, itemStyle.Render(fmt.Sprintf("%s %s", cb, nameField)))
			}
			itemIndex++
		}
		lines = append(lines, "")
	}

	return lines
}

// ── Capabilities Tab ─────────────────────────────────────────────────────────

func (s *AgentsSettings) renderCapabilitiesTab(width, height int, state *State, th Theme) []string {
	var lines []string

	lines = append(lines, lipgloss.NewStyle().Foreground(lipgloss.Color(th.Primary)).Bold(true).
		Render(i18n.T("settings.residual_final.agents.performance_heading")), "")

	type capField struct {
		idx   int
		label string
		value string
		bar   string
		hint  string
	}

	fields := []capField{
		{
			0, i18n.T("settings.residual_final.agents.temperature"),
			fmt.Sprintf("%.1f", state.AgentsFormTemperature),
			renderSlider(int(state.AgentsFormTemperature*100), 0, 200, 30),
			i18n.T("settings.residual_final.agents.temperature_hint"),
		},
		{
			1, i18n.T("settings.residual_final.agents.max_turns"),
			func() string {
				if state.AgentsFormMaxTurns == 0 {
					return i18n.T("settings.residual_final.agents.unlimited_zero")
				}
				return fmt.Sprintf("%d", state.AgentsFormMaxTurns)
			}(),
			renderSlider(state.AgentsFormMaxTurns, 0, 100, 30),
			i18n.T("settings.residual_final.agents.max_turns_hint"),
		},
		{
			2, i18n.T("settings.residual_final.mcp.timeout_label"),
			fmt.Sprintf("%ds (%dm)", state.AgentsFormTimeout, state.AgentsFormTimeout/60),
			renderSlider(state.AgentsFormTimeout, 900, 3600, 30),
			i18n.T("settings.residual_final.agents.timeout_hint"),
		},
	}

	labelStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(th.Primary)).Bold(true)
	valueStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(th.Text)).Bold(true)
	hintStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(th.TextMuted)).Italic(true).Padding(0, 0, 0, 4)
	selStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(th.BG)).Background(lipgloss.Color(th.Primary)).Bold(true)

	for _, f := range fields {
		isSel := state.AgentsFormTab == 3 && state.AgentsFormField == f.idx
		prefix := "  "
		if isSel {
			prefix = "▶ "
		}

		label := prefix + f.label
		if isSel {
			label = selStyle.Render(" " + f.label + " ")
		} else {
			label = labelStyle.Render(label)
		}

		lines = append(lines,
			label,
			"    "+f.bar+"  "+valueStyle.Render(f.value),
			hintStyle.Render(f.hint),
			"",
		)
	}

	return lines
}

// ============================================================================
// PREVIEW
// ============================================================================

func (s *AgentsSettings) renderPreview(width, height int, state *State, th Theme) string {
	if state.AgentsSelected < 0 || state.AgentsSelected >= len(s.config.Agents) {
		return ""
	}
	agent := s.config.Agents[state.AgentsSelected]

	pvPad := 2
	if width < 50 {
		pvPad = 1
	}

	var lines []string
	lines = append(lines,
		lipgloss.NewStyle().Foreground(lipgloss.Color(th.Text)).Background(lipgloss.Color(th.BGLight)).
			Bold(true).Width(maxInt(20, width-4)).Padding(0, pvPad).Render(i18n.T("settings.residual_final.common.esc_back")),
		"")

	icon := agent.Icon
	if icon == "" {
		icon = "●"
	}
	lines = append(lines,
		lipgloss.NewStyle().Foreground(lipgloss.Color(th.Primary)).Bold(true).Padding(1, 0).
			Render(fmt.Sprintf("%s  %s", icon, agent.Name)),
		"",
	)

	lbl := func(s string) string {
		return lipgloss.NewStyle().Foreground(lipgloss.Color(th.Success)).Bold(true).Render(s)
	}
	val := func(s string) string {
		return lipgloss.NewStyle().Foreground(lipgloss.Color(th.Text)).Render(s)
	}

	lines = append(lines, lbl(i18n.T("settings.residual_final.agents.preview.id_label"))+val(agent.ID))
	if agent.Provider != "" && agent.Model != "" {
		lines = append(lines,
			lbl(i18n.T("settings.residual_final.agents.preview.provider_label"))+val(agent.Provider),
			lbl(i18n.T("settings.residual_final.agents.preview.model_label"))+val(agent.Model),
		)
	} else {
		profileLabel := i18n.T("settings.residual_final.agents.active_profile_lower")
		if agent.ProfileID != "" {
			profileLabel = agent.ProfileID
		}
		lines = append(lines, lbl(i18n.T("settings.residual_final.agents.preview.model_label"))+val(i18n.T("settings.residual_final.agents.preview.via_profile", profileLabel)))
	}
	if agent.RoleAlias != "" {
		lines = append(lines, lbl(i18n.T("settings.residual_final.agents.preview.role_alias_label"))+val(agent.RoleAlias))
	}
	lines = append(lines, "")

	if agent.Description != "" {
		lines = append(lines, lbl(i18n.T("settings.residual_final.common.description_label")),
			val(wordWrap(agent.Description, maxInt(20, width-10))), "")
	}

	if len(agent.Tools) > 0 {
		lines = append(lines, lbl(i18n.T("settings.residual_final.common.tools_label")),
			val(wordWrap(strings.Join(agent.Tools, ", "), maxInt(20, width-10))), "")
	}

	if len(agent.Hooks) > 0 {
		lines = append(lines, lbl(i18n.T("settings.residual_final.common.hooks_label")),
			val(strings.Join(agent.Hooks, ", ")), "")
	}

	if agent.Capabilities != nil {
		lines = append(lines, lbl(i18n.T("settings.residual_final.agents.capabilities_label")),
			val(i18n.T("settings.residual_final.agents.temperature_value", agent.Capabilities.Temperature)),
			val(i18n.T("settings.residual_final.agents.max_turns_value", agent.Capabilities.MaxTurns)),
			val(i18n.T("settings.residual_final.agents.timeout_value", agent.Capabilities.Timeout)),
			"")
	}

	if agent.SystemPrompt != "" {
		lines = append(lines, lbl(i18n.T("settings.residual_final.agents.system_prompt_label")),
			lipgloss.NewStyle().Foreground(lipgloss.Color(th.Text)).
				Background(lipgloss.Color(th.BGLight)).Padding(1).Width(maxInt(20, width-10)).
				Render(wordWrap(agent.SystemPrompt, maxInt(20, width-14))),
		)
	}

	content := lipgloss.JoinVertical(lipgloss.Left, lines...)
	return lipgloss.NewStyle().Width(maxInt(20, width)).Height(maxInt(5, height)).Padding(1, pvPad).Render(content)
}

// ============================================================================
// HELPERS (render-only)
// ============================================================================
