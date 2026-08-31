package settings

import (
	"fmt"
	"sort"
	"strings"
	"unicode/utf8"

	"charm.land/lipgloss/v2"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/tools"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/i18n"
)

// RuleDisplayItem represents a permission rule for display in the list.
type RuleDisplayItem struct {
	Label     string                 // e.g. "Bash(curl:*)"
	Policy    tools.PermissionPolicy // allow, ask, deny
	Source    string                 // "rule", "override", "project"
	RuleIndex int                    // index in source rules array, -1 for overrides
	ToolName  string                 // tool name (for override items)
}

// SecuritySettings handles security and permission configuration UI state.
type SecuritySettings struct {
	// Data
	level          tools.PermissionLevel
	globalConfig   tools.PermissionConfig
	projectConfig  tools.PermissionConfig
	availableTools []string // All tool names (builtin + MCP)

	// Callbacks
	onPermissionChange    func(permission tools.Permission, policy tools.PermissionPolicy) error
	onLevelChange         func(level tools.PermissionLevel) error
	onRuleAdd             func(rule tools.PermissionRule, workspace bool) error
	onRuleDelete          func(ruleIndex int, workspace bool) error
	onOverrideDelete      func(tool string) error
	onGlobalConfigRefresh func() // called after any change that affects the live config
}

// NewSecuritySettings creates a new security settings component.
func NewSecuritySettings() *SecuritySettings {
	return &SecuritySettings{
		level: tools.LevelBalanced,
	}
}

// SetLevel sets the current permission level.
func (s *SecuritySettings) SetLevel(level tools.PermissionLevel) {
	s.level = level
}

// GetLevel returns the current permission level.
func (s *SecuritySettings) GetLevel() tools.PermissionLevel {
	return s.level
}

// SetGlobalConfig sets the global permission config (for rules + overrides).
func (s *SecuritySettings) SetGlobalConfig(config tools.PermissionConfig) {
	s.globalConfig = config
}

// SetProjectConfig sets the project-level permission config.
func (s *SecuritySettings) SetProjectConfig(config tools.PermissionConfig) {
	s.projectConfig = config
}

// SetOnPermissionChange registers a handler for permission updates.
func (s *SecuritySettings) SetOnPermissionChange(handler func(permission tools.Permission, policy tools.PermissionPolicy) error) {
	s.onPermissionChange = handler
}

// SetOnLevelChange registers a handler for level updates.
func (s *SecuritySettings) SetOnLevelChange(handler func(level tools.PermissionLevel) error) {
	s.onLevelChange = handler
}

// SetOnRuleAdd registers a handler for adding a new permission rule.
func (s *SecuritySettings) SetOnRuleAdd(handler func(rule tools.PermissionRule, workspace bool) error) {
	s.onRuleAdd = handler
}

// SetOnRuleDelete registers a handler for deleting a permission rule.
func (s *SecuritySettings) SetOnRuleDelete(handler func(ruleIndex int, workspace bool) error) {
	s.onRuleDelete = handler
}

// SetOnOverrideDelete registers a handler for removing a tool override.
func (s *SecuritySettings) SetOnOverrideDelete(handler func(tool string) error) {
	s.onOverrideDelete = handler
}

// SetOnGlobalConfigRefresh registers a callback invoked after any operation that
// mutates the live permission config (level change, rule add/delete, override delete).
// The callback should re-push the updated config into this SecuritySettings instance.
func (s *SecuritySettings) SetOnGlobalConfigRefresh(fn func()) {
	s.onGlobalConfigRefresh = fn
}

// SetAvailableTools sets the list of all available tool names (builtin + MCP).
func (s *SecuritySettings) SetAvailableTools(toolNames []string) {
	s.availableTools = toolNames
}

// defaultTools is the fallback tool list when no dynamic tools are provided.
var defaultTools = []string{
	"Bash", "Read", "Write", "Edit", "Glob", "Grep",
	"WebFetch", "WebSearch", "Task", "NotebookEdit",
}

// getTools returns the available tool list (dynamic if set, otherwise defaults).
func (s *SecuritySettings) getTools() []string {
	if len(s.availableTools) > 0 {
		return s.availableTools
	}
	return defaultTools
}

func localizedSecurityTabs() ([]string, []string) {
	return []string{
			i18n.T("settings.security.tab.allow"),
			i18n.T("settings.security.tab.ask"),
			i18n.T("settings.security.tab.deny"),
			i18n.T("settings.security.tab.workspace"),
		}, []string{
			i18n.T("settings.security.tab.allow.description"),
			i18n.T("settings.security.tab.ask.description"),
			i18n.T("settings.security.tab.deny.description"),
			i18n.T("settings.security.tab.workspace.description"),
		}
}

func localizedSecurityLevelDescription(level tools.PermissionLevel) string {
	keys := map[tools.PermissionLevel]string{
		tools.LevelAlwaysAsk:  "settings.security.level.always_ask.description",
		tools.LevelBalanced:   "settings.security.level.balanced.description",
		tools.LevelPermissive: "settings.security.level.permissive.description",
		tools.LevelYOLO:       "settings.security.level.yolo.description",
	}
	if key, ok := keys[level]; ok {
		return i18n.T(key)
	}
	return ""
}

func localizedSecurityPolicy(policy string) string {
	switch policy {
	case "allow":
		return i18n.T("settings.security.policy.allow")
	case "ask":
		return i18n.T("settings.security.policy.ask")
	case "deny":
		return i18n.T("settings.security.policy.deny")
	default:
		return policy
	}
}

// buildDisplayItems builds the sorted rule list for the current tab and search.
// Fix 3: overrides are collected into a sorted slice instead of ranging the map directly.
func (s *SecuritySettings) buildDisplayItems(tab int, search string) []RuleDisplayItem {
	var items []RuleDisplayItem
	searchLower := strings.ToLower(strings.TrimSpace(search))

	if tab == 3 {
		// Workspace tab: show project rules only.
		for i, rule := range s.projectConfig.Rules {
			label := ruleDisplayLabel(rule)
			if searchLower != "" && !strings.Contains(strings.ToLower(label), searchLower) {
				continue
			}
			items = append(items, RuleDisplayItem{
				Label:     label,
				Policy:    rule.Then.Policy,
				Source:    "project",
				RuleIndex: i,
			})
		}
		return items
	}

	// Map tab to policy.
	var tabPolicy tools.PermissionPolicy
	switch tab {
	case 0:
		tabPolicy = tools.PolicyAllow
	case 1:
		tabPolicy = tools.PolicyAsk
	case 2:
		tabPolicy = tools.PolicyDeny
	}

	// Global rules matching the tab's policy.
	for i, rule := range s.globalConfig.Rules {
		if rule.Then.Policy != tabPolicy {
			continue
		}
		label := ruleDisplayLabel(rule)
		if searchLower != "" && !strings.Contains(strings.ToLower(label), searchLower) {
			continue
		}
		items = append(items, RuleDisplayItem{
			Label:     label,
			Policy:    rule.Then.Policy,
			Source:    "rule",
			RuleIndex: i,
		})
	}

	// Fix 3: collect tool overrides into a sorted slice to get deterministic order.
	type overrideEntry struct {
		tool   string
		policy tools.PermissionPolicy
	}
	var overrides []overrideEntry
	for tool, override := range s.globalConfig.Overrides.Tools {
		var op tools.PermissionPolicy
		switch override {
		case tools.OverrideAlwaysAllow:
			op = tools.PolicyAllow
		case tools.OverrideAlwaysAsk:
			op = tools.PolicyAsk
		case tools.OverrideAlwaysDeny:
			op = tools.PolicyDeny
		default:
			continue
		}
		if op == tabPolicy {
			overrides = append(overrides, overrideEntry{tool: tool, policy: op})
		}
	}
	sort.Slice(overrides, func(i, j int) bool { return overrides[i].tool < overrides[j].tool })

	for _, ov := range overrides {
		label := fmt.Sprintf("%s(*)", ov.tool)
		if searchLower != "" && !strings.Contains(strings.ToLower(label), searchLower) {
			continue
		}
		items = append(items, RuleDisplayItem{
			Label:     label,
			Policy:    ov.policy,
			Source:    "override",
			RuleIndex: -1,
			ToolName:  ov.tool,
		})
	}

	return items
}

// ruleDisplayLabel formats a PermissionRule for display.
func ruleDisplayLabel(rule tools.PermissionRule) string {
	tool := i18n.T("settings.security.unknown_tool")
	if len(rule.When.Tools) > 0 {
		tool = rule.When.Tools[0]
	}

	pattern := "*"
	if rule.When.Commands != nil && len(rule.When.Commands.Glob) > 0 {
		glob := rule.When.Commands.Glob[0]
		pattern = strings.Replace(glob, " *", ":*", 1)
		if !strings.Contains(pattern, ":") {
			pattern = strings.ReplaceAll(pattern, " ", ":")
		}
	} else if rule.When.Paths != nil && len(rule.When.Paths.Glob) > 0 {
		pattern = rule.When.Paths.Glob[0]
	} else if rule.When.URLs != nil && len(rule.When.URLs.Glob) > 0 {
		pattern = rule.When.URLs.Glob[0]
	}

	return fmt.Sprintf("%s(%s)", tool, pattern)
}

// tabItemCount returns the number of items in the given tab for the badge.
func (s *SecuritySettings) tabItemCount(tab int) int {
	return len(s.buildDisplayItems(tab, ""))
}

// Render renders the security & permissions settings view.
func (s *SecuritySettings) Render(width, height int, state *State, theme any) string {
	th := theme.(Theme)

	contentWidth := maxInt(20, width-8)
	if width < 50 {
		contentWidth = maxInt(20, width-4)
	}

	pad := 2
	if width < 50 {
		pad = 1
	}

	titleStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(th.Primary)).
		Bold(true).
		Padding(1, pad).
		Width(maxInt(20, width-4))

	mutedStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(th.TextMuted)).
		Padding(0, pad)

	accentStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(th.Primary)).
		Bold(true)

	dimStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(th.TextDim))

	var lines []string
	lines = append(lines, titleStyle.Render(i18n.T("settings.security.title")))

	// ── Level row ────────────────────────────────────────────────────────────
	// Fix 1: The level row is its own navigable item (SecuritySelected == -2).
	// Left/right only work when it is selected; a ▶ prefix shows the selection.
	levelLabels := []string{
		i18n.T("settings.security.level.always_ask"),
		i18n.T("settings.security.level.balanced"),
		i18n.T("settings.security.level.permissive"),
		i18n.T("settings.security.level.yolo"),
	}
	levelIdx := levelIndex(s.level)
	if levelIdx < 0 {
		levelIdx = 1
	}
	state.SecurityLevelSelected = SecurityLevel(levelIdx)

	levelSelected := (state.SecurityRulesState == "list" || state.SecurityRulesState == "") &&
		state.SecuritySelected == -2

	levelPrefix := "  "
	if levelSelected {
		levelPrefix = lipgloss.NewStyle().
			Foreground(lipgloss.Color(th.Primary)).
			Bold(true).
			Render("▶ ")
	}

	yoloStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(th.Error)).Bold(true)

	var levelRow strings.Builder
	levelRow.WriteString(i18n.T("settings.security.level"))
	for i, label := range levelLabels {
		isActive := i == levelIdx
		var rendered string
		if width < 50 {
			if isActive {
				if i == 3 {
					rendered = yoloStyle.Render(fmt.Sprintf("[ %s ]", label))
				} else {
					rendered = accentStyle.Render(fmt.Sprintf("[ %s ]", label))
				}
				levelRow.WriteString(rendered)
			}
		} else {
			if isActive {
				if i == 3 {
					rendered = yoloStyle.Render(fmt.Sprintf("[ %s ]", label))
				} else {
					rendered = accentStyle.Render(fmt.Sprintf("[ %s ]", label))
				}
			} else {
				rendered = dimStyle.Render(fmt.Sprintf("  %s  ", label))
			}
			levelRow.WriteString(rendered)
		}
	}

	lines = append(lines, mutedStyle.Render(levelPrefix+levelRow.String()))

	// Fix: show a one-line description of the active level below it.
	if desc := localizedSecurityLevelDescription(s.level); desc != "" {
		descStyle := lipgloss.NewStyle().
			Foreground(lipgloss.Color(th.TextDim)).
			Italic(true).
			Padding(0, pad)
		if levelSelected {
			descStyle = descStyle.Foreground(lipgloss.Color(th.Primary))
		}
		lines = append(lines, descStyle.Render("  "+desc))
		if levelSelected {
			lines = append(lines, mutedStyle.Render(i18n.T("settings.security.level.change")))
		}
	}
	lines = append(lines, "")

	// ── Divider ──────────────────────────────────────────────────────────────
	divStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(th.Border))
	lines = append(lines, "  "+divStyle.Render(strings.Repeat("─", maxInt(5, contentWidth-4))))
	lines = append(lines, "")

	// ── Tab bar with item counts ──────────────────────────────────────────────
	tab := state.SecurityTab
	if tab < 0 || tab > 3 {
		tab = 0
	}

	var tabRow strings.Builder
	tabLabels, tabDescriptions := localizedSecurityTabs()
	tabRow.WriteString(i18n.T("settings.security.permissions"))
	for i, label := range tabLabels {
		count := s.tabItemCount(i)
		badge := fmt.Sprintf("%s (%d)", label, count)
		if i == tab {
			tabRow.WriteString(accentStyle.Render(badge))
		} else {
			tabRow.WriteString(dimStyle.Render(badge))
		}
		if i < len(tabLabels)-1 {
			tabRow.WriteString("  ")
		}
	}
	if width >= 80 {
		tabRow.WriteString(dimStyle.Render(i18n.T("settings.security.tab.cycle")))
	}
	lines = append(lines, mutedStyle.Render(tabRow.String()))
	lines = append(lines, "")
	lines = append(lines, mutedStyle.Render(tabDescriptions[tab]))
	lines = append(lines, "")

	// ── Search box ────────────────────────────────────────────────────────────
	// Fix 9: show a cursor and active border when search is focused.
	searchQuery := state.SecuritySearchQuery
	searchActive := state.SecuritySearchActive

	var searchContent string
	if searchQuery == "" && !searchActive {
		searchContent = lipgloss.NewStyle().
			Foreground(lipgloss.Color(th.TextMuted)).
			Italic(true).
			Render(i18n.T("settings.security.search"))
	} else if searchActive {
		// Cursor at end while typing.
		searchContent = lipgloss.NewStyle().
			Foreground(lipgloss.Color(th.Text)).
			Render(searchQuery + "▌")
	} else {
		searchContent = lipgloss.NewStyle().
			Foreground(lipgloss.Color(th.Text)).
			Render(searchQuery)
	}

	searchIcon := lipgloss.NewStyle().Foreground(lipgloss.Color(th.TextMuted)).Render(" ⌕ ")

	borderColor := th.Border
	if searchActive {
		borderColor = th.Primary
	}
	searchBoxStyle := lipgloss.NewStyle().
		Width(maxInt(20, contentWidth-6)).
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color(borderColor)).
		Padding(0, 1)

	lines = append(lines, "  "+searchBoxStyle.Render(searchIcon+searchContent))
	lines = append(lines, "")

	// ── Main content ─────────────────────────────────────────────────────────
	rulesState := state.SecurityRulesState
	if rulesState == "" {
		rulesState = "list"
	}

	if rulesState == "add_form" {
		lines = append(lines, s.renderAddForm(contentWidth, state, th))
	} else {
		items := s.buildDisplayItems(tab, searchQuery)

		// Calculate visible area.
		headerLines := len(lines) + 4
		visibleHeight := height - headerLines
		if visibleHeight < 3 {
			visibleHeight = 3
		}

		// Selection model:
		//   -2 = level row
		//   -1 = "Add new rule…"
		//   0..len(items)-1 = rule items
		selected := state.SecuritySelected
		maxSelected := len(items) - 1

		if selected < -2 {
			selected = -2
		}
		if selected > maxSelected {
			selected = maxSelected
		}
		state.SecuritySelected = selected

		// Display index for scroll purposes: -2→0, -1→1, 0→2, …
		displayIdx := selected + 2
		scrollOffset := state.SecurityScrollOffset
		totalDisplayItems := len(items) + 2 // level row + add row + rules

		if displayIdx < scrollOffset {
			scrollOffset = displayIdx
		}
		if displayIdx >= scrollOffset+visibleHeight {
			scrollOffset = displayIdx - visibleHeight + 1
		}
		if scrollOffset < 0 {
			scrollOffset = 0
		}
		state.SecurityScrollOffset = scrollOffset

		end := scrollOffset + visibleHeight
		if end > totalDisplayItems {
			end = totalDisplayItems
		}

		selectedItemStyle := lipgloss.NewStyle().
			Foreground(lipgloss.Color(th.Primary)).
			Bold(true)
		normalItemStyle := lipgloss.NewStyle().
			Foreground(lipgloss.Color(th.Text))
		dimItemStyle := lipgloss.NewStyle().
			Foreground(lipgloss.Color(th.TextMuted)).
			Italic(true)

		policyAllowStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(th.Success)).Bold(true)
		policyAskStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(th.Warning)).Bold(true)
		policyDenyStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(th.Error)).Bold(true)

		for i := scrollOffset; i < end; i++ {
			// i==0 is the level row, i==1 is "Add new rule…", i>=2 are rules.
			switch {
			case i == 0:
				// Level row is already rendered above the divider; skip here.
				continue

			case i == 1:
				isSelected := selected == -1
				prefix := "  "
				num := "1. "
				label := i18n.T("settings.security.add_rule")
				if isSelected {
					prefix = lipgloss.NewStyle().Foreground(lipgloss.Color(th.Primary)).Bold(true).Render("▶ ")
					lines = append(lines, "  "+prefix+selectedItemStyle.Render(num+label))
				} else {
					lines = append(lines, "  "+prefix+dimItemStyle.Render(num+label))
				}

			default:
				itemIdx := i - 2
				if itemIdx >= len(items) {
					continue
				}
				isSelected := itemIdx == selected
				item := items[itemIdx]

				prefix := "  "
				if isSelected {
					prefix = lipgloss.NewStyle().Foreground(lipgloss.Color(th.Primary)).Bold(true).Render("▶ ")
				}
				num := fmt.Sprintf("%d. ", i)

				// Policy badge.
				var badge string
				switch item.Policy {
				case tools.PolicyAllow:
					badge = policyAllowStyle.Render(i18n.T("settings.security.badge.allow"))
				case tools.PolicyAsk:
					badge = policyAskStyle.Render(i18n.T("settings.security.badge.ask"))
				case tools.PolicyDeny:
					badge = policyDenyStyle.Render(i18n.T("settings.security.badge.deny"))
				default:
					badge = dimStyle.Render(fmt.Sprintf(" %-5s ", strings.ToUpper(string(item.Policy))))
				}

				var labelRendered string
				if isSelected {
					labelRendered = prefix + selectedItemStyle.Render(num+item.Label) + "  " + badge
				} else {
					labelRendered = prefix + normalItemStyle.Render(num+item.Label) + "  " + badge
				}
				lines = append(lines, "  "+labelRendered)
			}
		}

		// Empty state.
		if len(items) == 0 {
			empty := lipgloss.NewStyle().Foreground(lipgloss.Color(th.TextMuted)).Italic(true)
			lines = append(lines, "")
			lines = append(lines, "  "+empty.Render(i18n.T("settings.security.empty")))
		}

		// Fix 7: action menu as a simple inline confirmation, not a fake multi-item menu.
		if rulesState == "action_menu" && selected >= 0 && selected < len(items) {
			lines = append(lines, "")
			lines = append(lines, s.renderDeleteConfirm(items[selected], th))
		}
	}

	content := lipgloss.JoinVertical(lipgloss.Left, lines...)

	containerPad := 2
	if width < 50 {
		containerPad = 1
	}
	containerStyle := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color(th.Primary)).
		Width(maxInt(20, width-2)).
		Height(maxInt(5, height-2)).
		Padding(1, containerPad)

	return containerStyle.Render(content)
}

// renderAddForm renders the inline form for adding a new rule.
// Fix 5: placeholder updated to "Leave blank or * for all".
// Fix 6: adds a Policy picker field (field 2) so the user can choose in-form.
// Fix 8: workspace tab defaults policy+workspace correctly; user can still override.
func (s *SecuritySettings) renderAddForm(width int, state *State, th Theme) string {
	titleStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(th.Warning)).Bold(true)
	labelStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(th.TextMuted))
	valueStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(th.Text))
	activeStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(th.Primary)).Bold(true)
	dimStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(th.TextMuted)).Italic(true)

	lines := []string{"  " + titleStyle.Render(i18n.T("settings.security.form.title"))}
	lines = append(lines, "")

	toolField := state.SecurityAddFormTool
	if toolField == "" {
		toolField = "Bash"
	}
	patternField := state.SecurityAddFormPattern
	policyField := state.SecurityAddFormPolicy
	if policyField == "" {
		policyField = "allow"
	}

	// ── Field 0: Tool ────────────────────────────────────────────────────────
	isField0 := state.SecurityAddFormField == 0
	field0Prefix, field0Label := fieldPrefixLabel(isField0, i18n.T("settings.security.form.tool"), labelStyle, activeStyle, th)
	toolHint := labelStyle.Render(i18n.T("settings.security.form.change"))
	lines = append(lines, "  "+field0Prefix+field0Label+valueStyle.Render(fmt.Sprintf("[ %s ]", toolField))+toolHint)

	// ── Field 1: Pattern ─────────────────────────────────────────────────────
	isField1 := state.SecurityAddFormField == 1
	field1Prefix, field1Label := fieldPrefixLabel(isField1, i18n.T("settings.security.form.pattern"), labelStyle, activeStyle, th)
	var patternDisplay string
	if isField1 {
		cursor := state.SecurityAddCursorPos
		runes := []rune(patternField)
		if cursor > len(runes) {
			cursor = len(runes)
		}
		before := string(runes[:cursor])
		after := string(runes[cursor:])
		patternDisplay = valueStyle.Render(before) + "█" + valueStyle.Render(after)
	} else if patternField == "" {
		patternDisplay = dimStyle.Render(i18n.T("settings.security.form.pattern_placeholder"))
	} else {
		patternDisplay = valueStyle.Render(patternField)
	}
	lines = append(lines, "  "+field1Prefix+field1Label+patternDisplay)

	// ── Field 2: Policy ───────────────────────────────────────────────────────
	// Fix 6: user picks allow/ask/deny directly in the form.
	isField2 := state.SecurityAddFormField == 2
	field2Prefix, field2Label := fieldPrefixLabel(isField2, i18n.T("settings.security.form.policy"), labelStyle, activeStyle, th)

	policyOpts := []string{"allow", "ask", "deny"}
	var policyRow strings.Builder
	for _, opt := range policyOpts {
		if opt == policyField {
			var ps lipgloss.Style
			switch opt {
			case "allow":
				ps = lipgloss.NewStyle().Foreground(lipgloss.Color(th.Success)).Bold(true)
			case "ask":
				ps = lipgloss.NewStyle().Foreground(lipgloss.Color(th.Warning)).Bold(true)
			case "deny":
				ps = lipgloss.NewStyle().Foreground(lipgloss.Color(th.Error)).Bold(true)
			}
			policyRow.WriteString(ps.Render(fmt.Sprintf("[ %s ]", strings.ToUpper(localizedSecurityPolicy(opt)))) + " ")
		} else {
			policyRow.WriteString(dimStyle.Render(fmt.Sprintf("  %s  ", localizedSecurityPolicy(opt))) + " ")
		}
	}
	if isField2 {
		policyRow.WriteString(labelStyle.Render("← →"))
	}
	lines = append(lines, "  "+field2Prefix+field2Label+policyRow.String())

	// ── Field 3: Scope (workspace toggle) ────────────────────────────────────
	isField3 := state.SecurityAddFormField == 3
	field3Prefix, field3Label := fieldPrefixLabel(isField3, i18n.T("settings.security.form.scope"), labelStyle, activeStyle, th)
	var scopeDisplay string
	if state.SecurityAddFormWorkspace {
		scopeDisplay = lipgloss.NewStyle().Foreground(lipgloss.Color(th.Primary)).Bold(true).Render(i18n.T("settings.security.scope.workspace.selected")) +
			dimStyle.Render(i18n.T("settings.security.scope.global"))
	} else {
		scopeDisplay = dimStyle.Render(i18n.T("settings.security.scope.workspace")) +
			lipgloss.NewStyle().Foreground(lipgloss.Color(th.Primary)).Bold(true).Render(i18n.T("settings.security.scope.global.selected"))
	}
	if isField3 {
		scopeDisplay += "  " + labelStyle.Render(i18n.T("settings.security.form.toggle"))
	}
	lines = append(lines, "  "+field3Prefix+field3Label+scopeDisplay)

	lines = append(lines, "")
	hintStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(th.TextDim)).Italic(true)
	lines = append(lines, "  "+hintStyle.Render(i18n.T("settings.security.form.help")))

	return lipgloss.JoinVertical(lipgloss.Left, lines...)
}

// fieldPrefixLabel returns the selection prefix and label style for an add-form field.
func fieldPrefixLabel(active bool, label string, labelStyle, activeStyle lipgloss.Style, th Theme) (string, string) {
	if active {
		prefix := lipgloss.NewStyle().Foreground(lipgloss.Color(th.Primary)).Bold(true).Render("▶ ")
		return prefix, activeStyle.Render(label)
	}
	return "  ", labelStyle.Render(label)
}

// renderDeleteConfirm renders a simple inline delete confirmation.
// Fix 7: replaces the fake multi-item action menu with a plain confirm prompt.
func (s *SecuritySettings) renderDeleteConfirm(item RuleDisplayItem, th Theme) string {
	warnStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(th.Error)).Bold(true)
	dimStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(th.TextMuted)).Italic(true)
	labelStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(th.Text))

	lines := []string{
		"  " + warnStyle.Render(i18n.T("settings.security.delete")) + labelStyle.Render(item.Label),
		"  " + dimStyle.Render(i18n.T("settings.security.delete.help")),
	}
	return lipgloss.JoinVertical(lipgloss.Left, lines...)
}

// HandleKey handles keyboard input for security settings.
func (s *SecuritySettings) HandleKey(key string, state *State) bool {
	rulesState := state.SecurityRulesState
	if rulesState == "" {
		rulesState = "list"
	}

	switch rulesState {
	case "add_form":
		return s.handleAddFormKey(key, state)
	case "action_menu":
		return s.handleDeleteConfirmKey(key, state)
	default:
		return s.handleListKey(key, state)
	}
}

func (s *SecuritySettings) handleListKey(key string, state *State) bool {
	// Fix 9: when search is active, route typing to search.
	if state.SecuritySearchActive {
		switch key {
		case "esc", "enter":
			state.SecuritySearchActive = false
			state.SecuritySelected = -1
			state.SecurityScrollOffset = 0
			return true
		case "backspace":
			if len(state.SecuritySearchQuery) > 0 {
				runes := []rune(state.SecuritySearchQuery)
				state.SecuritySearchQuery = string(runes[:len(runes)-1])
				state.SecuritySelected = -1
				state.SecurityScrollOffset = 0
			}
			return true
		default:
			if utf8.RuneCountInString(key) == 1 {
				r := []rune(key)[0]
				if r >= 32 {
					state.SecuritySearchQuery += string(r)
					state.SecuritySelected = -1
					state.SecurityScrollOffset = 0
					return true
				}
			}
			return true
		}
	}

	tab := state.SecurityTab
	searchQuery := state.SecuritySearchQuery
	items := s.buildDisplayItems(tab, searchQuery)
	maxSelected := len(items) - 1 // items; -2=level, -1=add, 0..n-1=rules

	switch key {
	case "down", "j":
		if state.SecuritySelected < maxSelected {
			state.SecuritySelected++
		}
		return true

	case "up", "k":
		if state.SecuritySelected > -2 {
			state.SecuritySelected--
		}
		return true

	case "tab":
		state.SecurityTab = (state.SecurityTab + 1) % 4
		state.SecuritySelected = -1
		state.SecurityScrollOffset = 0
		return true

	case "shift+tab":
		state.SecurityTab = (state.SecurityTab + 3) % 4
		state.SecuritySelected = -1
		state.SecurityScrollOffset = 0
		return true

	// Fix 1: left/right only change level when the level row (-2) is selected.
	case "left", "h":
		if state.SecuritySelected == -2 {
			return s.cycleLevel(-1, state)
		}
		return false

	case "right", "l":
		if state.SecuritySelected == -2 {
			return s.cycleLevel(1, state)
		}
		return false

	case "enter", " ":
		if state.SecuritySelected == -1 {
			// Open add-form, defaulting policy + workspace from current tab.
			state.SecurityRulesState = "add_form"
			state.SecurityAddFormField = 0
			state.SecurityAddFormTool = "Bash"
			state.SecurityAddFormPattern = ""
			state.SecurityAddCursorPos = 0
			// Fix 8: default policy from tab, workspace from tab==3.
			switch state.SecurityTab {
			case 0:
				state.SecurityAddFormPolicy = "allow"
				state.SecurityAddFormWorkspace = false
			case 1:
				state.SecurityAddFormPolicy = "ask"
				state.SecurityAddFormWorkspace = false
			case 2:
				state.SecurityAddFormPolicy = "deny"
				state.SecurityAddFormWorkspace = false
			case 3:
				state.SecurityAddFormPolicy = "allow"
				state.SecurityAddFormWorkspace = true
			}
			return true
		}
		if state.SecuritySelected >= 0 && state.SecuritySelected < len(items) {
			state.SecurityRulesState = "action_menu"
			state.SecurityRuleActionChoice = 0
			return true
		}
		return true

	case "d", "D":
		if state.SecuritySelected >= 0 && state.SecuritySelected < len(items) {
			s.deleteItem(items[state.SecuritySelected], state)
			return true
		}
		return false

	case "/":
		// Fix 9: enter search mode explicitly.
		state.SecuritySearchActive = true
		return true

	case "esc":
		if state.SecuritySearchActive {
			state.SecuritySearchActive = false
			return true
		}
		if state.SecuritySearchQuery != "" {
			state.SecuritySearchQuery = ""
			state.SecuritySelected = -1
			state.SecurityScrollOffset = 0
			return true
		}
		return false // let manager switch to sidebar

	case "backspace":
		if len(state.SecuritySearchQuery) > 0 {
			runes := []rune(state.SecuritySearchQuery)
			state.SecuritySearchQuery = string(runes[:len(runes)-1])
			state.SecuritySelected = -1
			state.SecurityScrollOffset = 0
			return true
		}
		return false

	case "pagedown", "ctrl+d":
		state.SecuritySelected += 5
		if state.SecuritySelected > maxSelected {
			state.SecuritySelected = maxSelected
		}
		return true

	case "pageup", "ctrl+u":
		state.SecuritySelected -= 5
		if state.SecuritySelected < -2 {
			state.SecuritySelected = -2
		}
		return true

	case "home", "g":
		state.SecuritySelected = -2
		state.SecurityScrollOffset = 0
		return true

	case "end", "G":
		state.SecuritySelected = maxSelected
		return true
	}
	return false
}

func (s *SecuritySettings) handleAddFormKey(key string, state *State) bool {
	// 4 fields: 0=tool, 1=pattern, 2=policy, 3=scope.
	maxField := 3

	switch key {
	case "esc":
		state.SecurityRulesState = "list"
		return true

	case "tab", "down":
		if state.SecurityAddFormField < maxField {
			state.SecurityAddFormField++
			if state.SecurityAddFormField == 1 {
				state.SecurityAddCursorPos = len([]rune(state.SecurityAddFormPattern))
			}
		}
		return true

	case "shift+tab", "up":
		if state.SecurityAddFormField > 0 {
			state.SecurityAddFormField--
		}
		return true

	case "left":
		switch state.SecurityAddFormField {
		case 0:
			idx := s.toolIndex(state.SecurityAddFormTool)
			tools := s.getTools()
			state.SecurityAddFormTool = tools[(idx-1+len(tools))%len(tools)]
		case 1:
			if state.SecurityAddCursorPos > 0 {
				state.SecurityAddCursorPos--
			}
		case 2:
			state.SecurityAddFormPolicy = prevPolicy(state.SecurityAddFormPolicy)
		case 3:
			state.SecurityAddFormWorkspace = !state.SecurityAddFormWorkspace
		}
		return true

	case "right":
		switch state.SecurityAddFormField {
		case 0:
			idx := s.toolIndex(state.SecurityAddFormTool)
			tools := s.getTools()
			state.SecurityAddFormTool = tools[(idx+1)%len(tools)]
		case 1:
			runes := []rune(state.SecurityAddFormPattern)
			if state.SecurityAddCursorPos < len(runes) {
				state.SecurityAddCursorPos++
			}
		case 2:
			state.SecurityAddFormPolicy = nextPolicy(state.SecurityAddFormPolicy)
		case 3:
			state.SecurityAddFormWorkspace = !state.SecurityAddFormWorkspace
		}
		return true

	case "enter":
		s.saveAddForm(state)
		state.SecurityRulesState = "list"
		return true

	case "backspace":
		if state.SecurityAddFormField == 1 && state.SecurityAddCursorPos > 0 {
			runes := []rune(state.SecurityAddFormPattern)
			pos := state.SecurityAddCursorPos
			state.SecurityAddFormPattern = string(append(runes[:pos-1], runes[pos:]...))
			state.SecurityAddCursorPos--
		}
		return true

	case "home", "ctrl+a":
		if state.SecurityAddFormField == 1 {
			state.SecurityAddCursorPos = 0
		}
		return true

	case "end", "ctrl+e":
		if state.SecurityAddFormField == 1 {
			state.SecurityAddCursorPos = len([]rune(state.SecurityAddFormPattern))
		}
		return true

	case "ctrl+u":
		if state.SecurityAddFormField == 1 {
			state.SecurityAddFormPattern = ""
			state.SecurityAddCursorPos = 0
		}
		return true

	default:
		if state.SecurityAddFormField == 1 && utf8.RuneCountInString(key) == 1 {
			r := []rune(key)[0]
			if r >= 32 {
				runes := []rune(state.SecurityAddFormPattern)
				pos := state.SecurityAddCursorPos
				newRunes := make([]rune, 0, len(runes)+1)
				newRunes = append(newRunes, runes[:pos]...)
				newRunes = append(newRunes, r)
				newRunes = append(newRunes, runes[pos:]...)
				state.SecurityAddFormPattern = string(newRunes)
				state.SecurityAddCursorPos++
				return true
			}
		}
		return true // consume all keys in add form
	}
}

// Fix 7: handleDeleteConfirmKey replaces the fake multi-item action menu.
func (s *SecuritySettings) handleDeleteConfirmKey(key string, state *State) bool {
	switch key {
	case "esc":
		state.SecurityRulesState = "list"
		return true
	case "enter":
		tab := state.SecurityTab
		search := state.SecuritySearchQuery
		items := s.buildDisplayItems(tab, search)
		selected := state.SecuritySelected
		if selected >= 0 && selected < len(items) {
			s.deleteItem(items[selected], state)
		}
		state.SecurityRulesState = "list"
		return true
	}
	return true // consume all other keys
}

// saveAddForm persists the new rule using the policy and workspace chosen in the form.
// Fix 6 & 8: policy and workspace come from form state, not hardcoded from tab index.
func (s *SecuritySettings) saveAddForm(state *State) {
	tool := strings.TrimSpace(state.SecurityAddFormTool)
	pattern := strings.TrimSpace(state.SecurityAddFormPattern)

	if tool == "" {
		return
	}

	// Resolve policy from the form field.
	var policy tools.PermissionPolicy
	switch strings.ToLower(state.SecurityAddFormPolicy) {
	case "ask":
		policy = tools.PolicyAsk
	case "deny":
		policy = tools.PolicyDeny
	default:
		policy = tools.PolicyAllow
	}
	workspace := state.SecurityAddFormWorkspace

	rule := tools.PermissionRule{
		Description: fmt.Sprintf("Manual rule: %s(%s)", tool, pattern),
		Priority:    100,
		When: tools.PermissionRuleMatch{
			Tools: []string{tool},
		},
		Then: tools.PermissionRuleAction{
			Policy: policy,
		},
	}

	// Fix 5: treat "" and "*" identically — both mean "match all" (no pattern added).
	if pattern != "" && pattern != "*" {
		switch strings.ToLower(tool) {
		case "bash":
			rule.When.Commands = &tools.PermissionPatternMatch{Glob: []string{pattern}}
		case "read", "write", "edit", "glob", "grep", "notebookedit":
			rule.When.Paths = &tools.PermissionPatternMatch{Glob: []string{pattern}}
		case "webfetch", "websearch":
			rule.When.URLs = &tools.PermissionPatternMatch{Glob: []string{pattern}}
		default:
			rule.When.Commands = &tools.PermissionPatternMatch{Glob: []string{pattern}}
		}
	}

	if s.onRuleAdd != nil {
		if err := s.onRuleAdd(rule, workspace); err != nil {
			logDebug("Failed to add permission rule: %v", err)
		}
	}
}

func (s *SecuritySettings) deleteItem(item RuleDisplayItem, state *State) {
	switch item.Source {
	case "rule":
		if s.onRuleDelete != nil {
			if err := s.onRuleDelete(item.RuleIndex, false); err != nil {
				logDebug("Failed to delete rule: %v", err)
			}
		}
	case "project":
		if s.onRuleDelete != nil {
			if err := s.onRuleDelete(item.RuleIndex, true); err != nil {
				logDebug("Failed to delete project rule: %v", err)
			}
		}
	case "override":
		if s.onOverrideDelete != nil {
			if err := s.onOverrideDelete(item.ToolName); err != nil {
				logDebug("Failed to delete override: %v", err)
			}
		}
	}

	state.SecuritySelected = -1
	state.SecurityScrollOffset = 0
}

// Fix 1+2: cycleLevel now only fires when the level row is selected.
// onLevelChange errors are propagated; the level is not updated locally on error.
func (s *SecuritySettings) cycleLevel(delta int, state *State) bool {
	levels := []tools.PermissionLevel{
		tools.LevelAlwaysAsk,
		tools.LevelBalanced,
		tools.LevelPermissive,
		tools.LevelYOLO,
	}
	idx := levelIndex(s.level)
	if idx < 0 {
		idx = 1
	}
	idx = (idx + delta + len(levels)) % len(levels)
	next := levels[idx]

	if s.onLevelChange != nil {
		if err := s.onLevelChange(next); err != nil {
			logDebug("Failed to change permission level: %v", err)
			// Do not update local state on error so the display stays consistent.
			return true
		}
	}

	s.level = next
	state.SecurityLevelSelected = SecurityLevel(idx)

	// Fix 11: refresh the global config so the level is reflected in displayed rules.
	if s.onGlobalConfigRefresh != nil {
		s.onGlobalConfigRefresh()
	}
	return true
}

func (s *SecuritySettings) toolIndex(name string) int {
	for i, t := range s.getTools() {
		if t == name {
			return i
		}
	}
	return 0
}

// levelIndex returns the 0-based index of the given level, or -1 if unknown.
func levelIndex(level tools.PermissionLevel) int {
	switch level {
	case tools.LevelAlwaysAsk:
		return 0
	case tools.LevelBalanced:
		return 1
	case tools.LevelPermissive:
		return 2
	case tools.LevelYOLO:
		return 3
	default:
		return -1
	}
}

// Fix 6: nextPolicy / prevPolicy cycle among the three form-selectable policies.
// These replace the old dead-code nextPolicy that cycled Allow→Ask→Deny→Allow.
func nextPolicy(current string) string {
	switch current {
	case "allow":
		return "ask"
	case "ask":
		return "deny"
	default:
		return "allow"
	}
}

func prevPolicy(current string) string {
	switch current {
	case "deny":
		return "ask"
	case "ask":
		return "allow"
	default:
		return "deny"
	}
}
