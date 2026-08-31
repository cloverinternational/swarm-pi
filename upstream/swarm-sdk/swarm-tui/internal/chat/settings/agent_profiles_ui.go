package settings

// ============================================================================
// AGENT PROFILES UI — v3 split-panel design
// ============================================================================
//
// STATE MACHINE:
//
//   "list"       — profile list.  ↑/↓ navigate.  e/Enter = edit roles.
//                  d = set default.  n = new.  c = clone.  x = delete.
//                  Esc = back to sidebar.
//
//   "edit_roles" — split-panel role editor.
//                  LEFT  panel: scrollable role list (↑/↓ to navigate).
//                  RIGHT panel: FallbackPicker for the selected role.
//                  Tab / → = move focus to picker.  ← = move focus back.
//                  s / Ctrl+S = save all.  Esc = discard.
//
// KEY DESIGN DECISIONS vs previous versions:
//   • The picker is NEVER embedded inline below role rows.
//     It always lives in the right panel at full available height.
//   • Only ONE picker is alive at a time (lazy allocation per selected role).
//   • ↑/↓ ONLY moves between roles — the picker gets its own focus via Tab/→.
//   • Picker navigation keys (↑/↓/Enter/Esc) are only forwarded when
//     pickerFocused == true.  This eliminates the sticky-navigation bug.
//   • No "Resize to view" — right panel receives full height.

import (
	"fmt"
	"os"
	"strings"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/fallback"
)

// profileLog writes a timestamped line to /tmp/swarm_profile_debug.log for tracing.
func profileLog(format string, args ...any) {
	f, err := os.OpenFile("/tmp/swarm_profile_debug.log", os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return
	}
	defer f.Close()
	msg := fmt.Sprintf(format, args...)
	fmt.Fprintf(f, "[%s] %s\n", time.Now().Format("15:04:05.000"), msg)
}

// ============================================================================
// STRUCT
// ============================================================================

type ProfileSettings struct {
	manager *ProfileManager

	// edit_roles state
	editingProfileID string
	rolesSelected    int  // which role row is highlighted
	pickerFocused    bool // true when ↑/↓/Enter go to picker instead of role list

	// One picker, allocated lazily for rolesSelected.
	// Replaced whenever rolesSelected changes.
	activePicker *FallbackPicker
	activeAlias  ModelAlias // which alias the activePicker belongs to

	// pendingRoles holds chains edited during this session.
	// Committed on Save, discarded on Esc.
	pendingRoles map[ModelAlias]*fallback.Chain

	// lastActivatedProfileID is set when the user presses "d" to set a profile
	// as default. The Manager reads and clears this via TakeActivatedProfileID()
	// so it can trigger model-switch callbacks.
	lastActivatedProfileID string

	// lastSavedProfileID is set when saveAllRoles succeeds. The Manager reads
	// and clears this via TakeSavedProfileID() to trigger a model re-sync when
	// the user saves changes to the currently-active profile.
	lastSavedProfileID string

	// rename mode state
	renameMode  bool   // true when renaming a profile
	renameInput string // current text input for rename
	renameIndex int    // which profile index is being renamed

	// bulk model assignment state
	roleChecked map[ModelAlias]bool // which roles are checked for bulk apply
}

func NewProfileSettings() *ProfileSettings {
	return &ProfileSettings{manager: NewProfileManager()}
}

func (p *ProfileSettings) GetManager() *ProfileManager { return p.manager }

// TakeActivatedProfileID returns the profile ID that was most recently set as
// default via the "d" key, then clears it. Returns "" if no activation occurred.
func (p *ProfileSettings) TakeActivatedProfileID() string {
	id := p.lastActivatedProfileID
	p.lastActivatedProfileID = ""
	profileLog("[TakeActivatedProfileID] returning: %q", id)
	return id
}

// TakeSavedProfileID returns the profile ID whose roles were just successfully
// saved via saveAllRoles, then clears it. Returns "" if no save occurred.
// Used by Manager to trigger a model re-sync when the active profile is edited.
func (p *ProfileSettings) TakeSavedProfileID() string {
	id := p.lastSavedProfileID
	p.lastSavedProfileID = ""
	return id
}

// IsPickerFocused returns true when the fallback-chain picker has keyboard focus.
// Used by renderHints to show chain-editor vs role-list key hints.
func (p *ProfileSettings) IsPickerFocused() bool { return p.pickerFocused }

// ============================================================================
// TOP-LEVEL RENDER
// ============================================================================

func (p *ProfileSettings) Render(width, height int, state *State, th Theme) string {
	switch state.ProfilesState {
	case "edit_roles":
		return p.renderRolesEditor(width, height, state, th)
	default:
		return p.renderList(width, height, state, th)
	}
}

// ============================================================================
// LIST VIEW
// ============================================================================

func (p *ProfileSettings) renderList(width, height int, state *State, th Theme) string {
	const pad = 1
	iw := max(20, width-4)

	var lines []string

	if banner := p.renderMessageBanner(state, width, th); banner != "" {
		lines = append(lines, banner, "")
	}

	// Title + badges
	profiles := p.manager.ListProfiles()
	activeName := ""
	for _, pr := range profiles {
		if pr.IsDefault {
			activeName = pr.Name
			break
		}
	}

	titleStyle := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color(th.Primary)).
		Width(iw).Align(lipgloss.Center)
	lines = append(lines, titleStyle.Render("Profiles"))

	if activeName != "" {
		if width < 50 {
			// Show only active badge at narrow widths
			badge := lipgloss.NewStyle().
				Background(lipgloss.Color(th.Success)).Foreground(lipgloss.Color(th.BG)).
				Padding(0, 1).Bold(true).Render("✓ " + activeName)
			lines = append(lines, lipgloss.NewStyle().Width(iw).Align(lipgloss.Center).Render(badge))
		} else {
			badge := lipgloss.NewStyle().
				Background(lipgloss.Color(th.Success)).Foreground(lipgloss.Color(th.BG)).
				Padding(0, 1).Bold(true).Render("✓ " + activeName)
			countBadge := lipgloss.NewStyle().
				Background(lipgloss.Color(th.BGLight)).Foreground(lipgloss.Color(th.Text)).
				Padding(0, 1).Render(fmt.Sprintf("%d profiles", len(profiles)))
			badgesLine := lipgloss.JoinHorizontal(lipgloss.Center, countBadge, "  ", badge)
			lines = append(lines, lipgloss.NewStyle().Width(iw).Align(lipgloss.Center).Render(badgesLine))
		}
	}
	lines = append(lines, "")

	// Profile rows
	for i, profile := range profiles {
		isSel := i == state.ProfilesSelected
		lines = append(lines, p.renderProfileRow(profile, isSel, iw, th))
		if i < len(profiles)-1 {
			lines = append(lines, "")
		}
	}
	lines = append(lines, "")

	// Hint bar
	k := func(key string) string {
		return lipgloss.NewStyle().Background(lipgloss.Color(th.BGLight)).
			Foreground(lipgloss.Color(th.Text)).Padding(0, 1).Bold(true).Render(key)
	}
	var plHintText string
	if p.renameMode {
		plHintText = fmt.Sprintf("%s save  %s cancel", k("Enter"), k("Esc"))
	} else if width < 45 {
		plHintText = ""
	} else if width < 65 {
		plHintText = fmt.Sprintf("%s/%s  %s edit  %s rename  %s default  %s back",
			k("↑"), k("↓"), k("e"), k("r"), k("d"), k("Esc"))
	} else if width < 85 {
		plHintText = fmt.Sprintf("%s/%s navigate  %s edit  %s rename  %s default  %s new  %s del  %s back",
			k("↑"), k("↓"), k("e"), k("r"), k("d"), k("n"), k("x"), k("Esc"))
	} else {
		plHintText = fmt.Sprintf("%s/%s navigate  %s edit roles  %s rename  %s set default  %s new  %s clone  %s delete  %s back",
			k("↑"), k("↓"), k("e"), k("r"), k("d"), k("n"), k("c"), k("x"), k("Esc"))
	}
	if plHintText != "" {
		lines = append(lines, lipgloss.NewStyle().Foreground(lipgloss.Color(th.TextMuted)).
			Width(iw).Align(lipgloss.Center).Render(plHintText))
	}

	content := lipgloss.JoinVertical(lipgloss.Left, lines...)
	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color(th.Border)).
		Width(max(20, width-2)).Padding(0, pad).
		Render(content)
}

func (p *ProfileSettings) renderProfileRow(profile AgentProfile, isSel bool, width int, th Theme) string {
	icon := profile.Icon
	if icon == "" {
		icon = "●"
	}

	cardWidth := max(20, width-2)
	var cardStyle lipgloss.Style
	if isSel {
		cardStyle = lipgloss.NewStyle().
			Background(lipgloss.Color(th.BGLighter)).
			Border(lipgloss.RoundedBorder()).BorderForeground(lipgloss.Color(th.Primary)).
			Width(cardWidth).Padding(0, 1)
	} else {
		cardStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).BorderForeground(lipgloss.Color(th.Border)).
			Width(cardWidth).Padding(0, 1)
	}

	nameStyle := lipgloss.NewStyle().Bold(true)
	if isSel {
		nameStyle = nameStyle.Foreground(lipgloss.Color(th.Primary))
	}

	defaultMark := ""
	if profile.IsDefault {
		defaultMark = lipgloss.NewStyle().Foreground(lipgloss.Color(th.Success)).Bold(true).Render("  ✓ active")
	}

	// Check if this row is in rename mode
	var nameLine string
	if p.renameMode && isSel {
		// Show inline text input with cursor
		inputStyle := lipgloss.NewStyle().
			Foreground(lipgloss.Color(th.Primary)).
			Background(lipgloss.Color(th.BGLighter)).
			Bold(true)
		cursor := lipgloss.NewStyle().
			Foreground(lipgloss.Color(th.BGLighter)).
			Background(lipgloss.Color(th.Primary)).
			Render(" ")
		nameLine = nameStyle.Render(icon+" ") + inputStyle.Render(p.renameInput) + cursor + defaultMark
	} else {
		nameLine = nameStyle.Render(icon+" "+profile.Name) + defaultMark
	}

	roleCount := len(profile.Roles)
	meta := lipgloss.NewStyle().Foreground(lipgloss.Color(th.TextMuted)).
		Render(fmt.Sprintf("  %d roles", roleCount))
	if profile.Description != "" {
		desc := profile.Description
		maxDescW := max(10, width-6)
		if len(desc) > maxDescW {
			desc = desc[:max(0, maxDescW-3)] + "..."
		}
		meta = lipgloss.NewStyle().Foreground(lipgloss.Color(th.TextMuted)).Italic(true).Render(desc) + meta
	}

	inner := lipgloss.JoinVertical(lipgloss.Left, nameLine, meta)

	// Expanded summary when selected (but not during rename)
	if isSel && !p.renameMode {
		summary := p.buildRoleSummary(profile, max(20, width-6), th)
		if summary != "" {
			inner = lipgloss.JoinVertical(lipgloss.Left, inner, summary)
		}
	}

	return cardStyle.Render(inner)
}

func (p *ProfileSettings) buildRoleSummary(profile AgentProfile, width int, th Theme) string {
	if len(profile.Roles) == 0 {
		return lipgloss.NewStyle().Foreground(lipgloss.Color(th.TextMuted)).Italic(true).
			Render("  no roles — press e to configure")
	}
	aliases := AllAliases()
	var parts []string
	for _, alias := range aliases {
		rc, ok := profile.Roles[alias]
		if !ok || rc.Chain == nil || rc.Chain.IsEmpty() {
			continue
		}
		primary := rc.PrimaryRef()
		model := shortenModel(primary.Model)
		fb := ""
		if len(rc.Chain.Fallbacks) > 0 {
			fb = lipgloss.NewStyle().Foreground(lipgloss.Color(th.Primary)).
				Render(fmt.Sprintf("+%d", len(rc.Chain.Fallbacks)))
		}
		parts = append(parts, lipgloss.NewStyle().Foreground(lipgloss.Color(th.TextMuted)).
			Render(AliasDisplayName(alias)+":")+
			" "+lipgloss.NewStyle().Render(model)+fb)
	}
	if len(parts) == 0 {
		return ""
	}
	line := "  " + strings.Join(parts, "  │  ")
	if len(line) > width {
		line = line[:width-3] + "…"
	}
	return line
}

// shortenModel trims date suffixes: "claude-sonnet-4-20250514" → "claude-sonnet-4"
func shortenModel(model string) string {
	if len(model) <= 18 {
		return model
	}
	// If last segment is 8 digits (date), remove it
	parts := strings.Split(model, "-")
	if len(parts) > 1 {
		last := parts[len(parts)-1]
		allDigits := true
		for _, c := range last {
			if c < '0' || c > '9' {
				allDigits = false
				break
			}
		}
		if allDigits && len(last) >= 6 {
			return strings.Join(parts[:len(parts)-1], "-")
		}
	}
	return model
}

// ============================================================================
// ROLES EDITOR — SPLIT PANEL
// ============================================================================

func (p *ProfileSettings) renderRolesEditor(width, height int, state *State, th Theme) string {
	profile := p.getEditingProfile()
	if profile == nil {
		return "Error: profile not found"
	}

	const (
		headerLines = 3  // title bar + hint bar + blank
		footerLines = 2  // save/cancel + blank
		leftFrac    = 38 // % of width for role list
		sepWidth    = 1
	)

	// Calculate split widths - collapse to stacked at narrow widths
	leftWidth := (width * leftFrac) / 100
	if leftWidth < 20 {
		leftWidth = 20
	}
	rightWidth := width - leftWidth - sepWidth
	if rightWidth < 20 || width < 60 {
		// Too narrow: fall back to stacked layout
		return p.renderRolesEditorStacked(width, height, state, th, profile)
	}

	contentHeight := max(6, height-headerLines-footerLines)

	// ── header ────────────────────────────────────────────────────────────────
	icon := profile.Icon
	if icon == "" {
		icon = "●"
	}
	titleBar := lipgloss.NewStyle().
		Foreground(lipgloss.Color(th.BG)).Background(lipgloss.Color(th.Primary)).
		Bold(true).Width(width).Padding(0, 2).
		Render(fmt.Sprintf("Configure Roles  %s %s", icon, profile.Name))

	k := func(key string) string {
		return lipgloss.NewStyle().Background(lipgloss.Color(th.BGLight)).
			Foreground(lipgloss.Color(th.Text)).Padding(0, 1).Bold(true).Render(key)
	}
	focusHint := ""
	if p.pickerFocused {
		focusHint = "  " + lipgloss.NewStyle().
			Background(lipgloss.Color(th.Primary)).Foreground(lipgloss.Color(th.BG)).
			Padding(0, 1).Bold(true).Render("CHAIN EDITOR") +
			"  " + k("←") + " back to roles"
	} else {
		focusHint = fmt.Sprintf("%s/%s roles  %s toggle  %s bulk apply  %s edit chain  %s save  %s back",
			k("↑"), k("↓"), k("Space"), k("a"), k("→")+"/"+k("Enter"), k("Ctrl+S"), k("Esc"))
	}
	hintBar := lipgloss.NewStyle().Foreground(lipgloss.Color(th.TextMuted)).
		Width(width).Render(focusHint)

	// ── left panel: role list ─────────────────────────────────────────────────
	leftPanel := p.renderRoleList(leftWidth, contentHeight, profile, th)

	// ── separator ─────────────────────────────────────────────────────────────
	sepLines := make([]string, contentHeight)
	for i := range sepLines {
		sepLines[i] = "│"
	}
	sep := lipgloss.NewStyle().Foreground(lipgloss.Color(th.Border)).
		Render(strings.Join(sepLines, "\n"))

	// ── right panel: picker ───────────────────────────────────────────────────
	rightPanel := p.renderPickerPanel(rightWidth, contentHeight, profile, th)

	// ── assemble ─────────────────────────────────────────────────────────────
	body := lipgloss.JoinHorizontal(lipgloss.Top, leftPanel, sep, rightPanel)

	saveBtn := lipgloss.NewStyle().
		Background(lipgloss.Color(th.Success)).Foreground(lipgloss.Color(th.BG)).
		Padding(0, 2).Bold(true).Render("Save  Ctrl+S")
	cancelBtn := lipgloss.NewStyle().
		Background(lipgloss.Color(th.BGLight)).Foreground(lipgloss.Color(th.Text)).
		Padding(0, 2).Render("Discard  Esc")
	footer := "  " + saveBtn + "  " + cancelBtn

	return lipgloss.JoinVertical(lipgloss.Left, titleBar, hintBar, "", body, "", footer)
}

// renderRoleList draws the left panel (scrollable list of roles).
func (p *ProfileSettings) renderRoleList(width, height int, profile *AgentProfile, th Theme) string {
	aliases := AllAliases()

	// Header
	headerStyle := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color(th.TextMuted)).
		Width(width).Padding(0, 1)
	header := headerStyle.Render("Roles")

	var rows []string
	rows = append(rows, header, "")

	for i, alias := range aliases {
		isSel := i == p.rolesSelected
		rc, hasRole := profile.Roles[alias]

		// Read pending edits if available
		if chain, ok := p.pendingRoles[alias]; ok {
			if hasRole {
				rc.Chain = chain
			} else {
				rc = RoleConfig{Chain: chain, Enabled: true}
				hasRole = !chain.IsEmpty()
			}
		}

		rows = append(rows, p.renderRoleListRow(alias, rc, hasRole, isSel, width, th))
	}

	content := lipgloss.JoinVertical(lipgloss.Left, rows...)

	// Clip to height
	contentLines := strings.Split(content, "\n")
	if len(contentLines) > height {
		contentLines = contentLines[:height]
	}
	content = strings.Join(contentLines, "\n")

	return lipgloss.NewStyle().Width(width).Height(height).Render(content)
}

func (p *ProfileSettings) renderRoleListRow(alias ModelAlias, rc RoleConfig, hasRole, isSel bool, width int, th Theme) string {
	// Checkbox for bulk operations
	checkBox := "☐ "
	if p.roleChecked[alias] {
		checkBox = lipgloss.NewStyle().Foreground(lipgloss.Color(th.Success)).Render("☑ ")
	}

	// Status dot — always enabled when chain is configured
	dot := lipgloss.NewStyle().Foreground(lipgloss.Color(th.TextMuted)).Render("○")
	if hasRole && rc.Chain != nil && !rc.Chain.IsEmpty() {
		dot = lipgloss.NewStyle().Foreground(lipgloss.Color(th.Success)).Render("●")
	}

	label := AliasDisplayName(alias)

	// Chain shorthand
	chainStr := ""
	if hasRole && rc.Chain != nil && !rc.Chain.IsEmpty() {
		chainStr = shortenModel(rc.Chain.Primary.Model)
		if len(rc.Chain.Fallbacks) > 0 {
			chainStr += lipgloss.NewStyle().Foreground(lipgloss.Color(th.Primary)).
				Render(fmt.Sprintf("+%d", len(rc.Chain.Fallbacks)))
		}
	}

	var rowStyle lipgloss.Style
	if isSel && !p.pickerFocused {
		rowStyle = lipgloss.NewStyle().
			Background(lipgloss.Color(th.Primary)).Foreground(lipgloss.Color(th.BG)).
			Bold(true).Width(width).Padding(0, 1)
	} else if isSel {
		rowStyle = lipgloss.NewStyle().
			Background(lipgloss.Color(th.BGLighter)).Foreground(lipgloss.Color(th.Text)).
			Width(width).Padding(0, 1)
	} else {
		rowStyle = lipgloss.NewStyle().
			Width(width).Padding(0, 1)
	}

	// Compact single-line row
	prefix := "  "
	if isSel && !p.pickerFocused {
		prefix = "▶ "
	} else if isSel {
		prefix = "› "
	}

	labelW := 12 // Slightly narrower to accommodate checkbox
	labelStr := fmt.Sprintf("%-*s", labelW, label)

	row := checkBox + dot + " " + prefix + labelStr
	if chainStr != "" {
		row += "  " + chainStr
	}

	return rowStyle.Render(row)
}

// renderPickerPanel draws the right panel (FallbackPicker for selected role).
func (p *ProfileSettings) renderPickerPanel(width, height int, profile *AgentProfile, th Theme) string {
	aliases := AllAliases()
	if p.rolesSelected < 0 || p.rolesSelected >= len(aliases) {
		return lipgloss.NewStyle().Width(width).Height(height).Render("")
	}

	alias := aliases[p.rolesSelected]

	// Lazily create picker for this alias if needed
	if p.activePicker == nil || p.activeAlias != alias {
		p.buildPickerForAlias(alias, profile)
	}

	if p.activePicker == nil {
		return lipgloss.NewStyle().Width(width).Height(height).
			Foreground(lipgloss.Color(th.TextMuted)).Align(lipgloss.Center, lipgloss.Center).
			Render("Select a role to configure")
	}

	// Role header inside right panel
	roleLabel := AliasDisplayName(alias)
	roleDesc := roleDescription(string(alias))
	headerLine := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color(th.Primary)).
		Width(max(20, width-2)).Render(roleLabel)
	descLine := lipgloss.NewStyle().Foreground(lipgloss.Color(th.TextMuted)).Italic(true).
		Width(max(20, width-2)).Render(roleDesc)

	pickerHeight := max(5, height-4) // leave room for role header + desc + blank
	// FallbackPicker.Render subtracts 6 (2*border + 2*padding) from height,
	// and renderMinimal triggers when innerHeight < 8.  So we need >= 14.
	if pickerHeight < 14 {
		pickerHeight = 14
	}

	pickerOut := p.activePicker.Render(width, pickerHeight, th)

	return lipgloss.JoinVertical(lipgloss.Left,
		headerLine, descLine, "",
		pickerOut,
	)
}

// buildPickerForAlias creates the active picker for a given alias.
func (p *ProfileSettings) buildPickerForAlias(alias ModelAlias, profile *AgentProfile) {
	if p.pendingRoles == nil {
		p.pendingRoles = make(map[ModelAlias]*fallback.Chain)
	}

	var chain *fallback.Chain

	// Check pending edits first
	if c, ok := p.pendingRoles[alias]; ok {
		chain = c.Clone()
	} else if profile != nil {
		if rc, ok := profile.Roles[alias]; ok && rc.Chain != nil && !rc.Chain.IsEmpty() {
			chain = rc.Chain.Clone()
		}
	}
	if chain == nil {
		chain = fallback.NewChain("", "")
	}

	picker := NewFallbackPicker(chain)
	aliasCopy := alias
	picker.SetOnSave(func(c *fallback.Chain) {
		if p.pendingRoles == nil {
			p.pendingRoles = make(map[ModelAlias]*fallback.Chain)
		}
		p.pendingRoles[aliasCopy] = c
	})

	p.activePicker = picker
	p.activeAlias = alias
}

// ============================================================================
// STACKED FALLBACK (narrow terminals)
// ============================================================================

func (p *ProfileSettings) renderRolesEditorStacked(width, height int, state *State, th Theme, profile *AgentProfile) string {
	icon := profile.Icon
	if icon == "" {
		icon = "●"
	}
	titleBar := lipgloss.NewStyle().
		Foreground(lipgloss.Color(th.BG)).Background(lipgloss.Color(th.Primary)).
		Bold(true).Width(width).Padding(0, 2).
		Render(fmt.Sprintf("Roles — %s %s", icon, profile.Name))

	aliases := AllAliases()
	var rows []string
	for i, alias := range aliases {
		isSel := i == p.rolesSelected
		rc, hasRole := profile.Roles[alias]
		if chain, ok := p.pendingRoles[alias]; ok {
			rc.Chain = chain
			hasRole = !chain.IsEmpty()
		}
		rows = append(rows, p.renderRoleListRow(alias, rc, hasRole, isSel, width, th))
	}

	k := func(key string) string {
		return lipgloss.NewStyle().Background(lipgloss.Color(th.BGLight)).
			Foreground(lipgloss.Color(th.Text)).Padding(0, 1).Bold(true).Render(key)
	}
	hints := lipgloss.NewStyle().Foreground(lipgloss.Color(th.TextMuted)).Width(width).Render(
		fmt.Sprintf("%s/%s  %s toggle  %s bulk  %s edit  %s save  %s back",
			k("↑"), k("↓"), k("Space"), k("a"), k("Enter"), k("Ctrl+S"), k("Esc")))

	return lipgloss.JoinVertical(lipgloss.Left,
		append([]string{titleBar, hints, ""}, append(rows, "", "  Ctrl+S save  ·  Esc discard")...)...)
}

// ============================================================================
// KEYBOARD HANDLERS
// ============================================================================

func (p *ProfileSettings) HandleKey(key string, state *State) bool {
	// Handle rename mode text input first
	if p.renameMode {
		return p.handleRenameKey(key, state)
	}

	switch state.ProfilesState {
	case "edit_roles":
		return p.handleRolesKey(key, state)
	default:
		return p.handleListKey(key, state)
	}
}

// ── list view keys ────────────────────────────────────────────────────────────

func (p *ProfileSettings) handleListKey(key string, state *State) bool {
	profiles := p.manager.ListProfiles()

	switch key {
	case "up", "k":
		if state.ProfilesSelected > 0 {
			state.ProfilesSelected--
		}
		return true

	case "down", "j":
		if state.ProfilesSelected < len(profiles)-1 {
			state.ProfilesSelected++
		}
		return true

	case "enter", "e":
		if state.ProfilesSelected >= 0 && state.ProfilesSelected < len(profiles) {
			p.openRolesEditor(profiles[state.ProfilesSelected].ID, state)
		}
		return true

	case "d":
		profileLog("[ProfileSettings] 'd' pressed, ProfilesSelected=%d, len(profiles)=%d", state.ProfilesSelected, len(profiles))
		if state.ProfilesSelected >= 0 && state.ProfilesSelected < len(profiles) {
			prof := profiles[state.ProfilesSelected]
			profileLog("[ProfileSettings] selected prof: id=%s name=%s isDefault=%v", prof.ID, prof.Name, prof.IsDefault)
			if !prof.IsDefault {
				if err := p.manager.SetDefaultProfile(prof.ID); err != nil {
					profileLog("[ProfileSettings] SetDefaultProfile error: %v", err)
					p.showError(state, "Failed to set default: "+err.Error())
				} else {
					p.showSuccess(state, "✓ '"+prof.Name+"' is now the default profile")
					// Signal Manager so it can switch the main chat model
					p.lastActivatedProfileID = prof.ID
					profileLog("[ProfileSettings] lastActivatedProfileID set to: %s", prof.ID)
				}
			} else {
				profileLog("[ProfileSettings] prof already isDefault, skipping")
			}
		}
		return true

	case "n":
		prof, err := p.manager.CreateProfile("New Profile", "")
		if err != nil {
			p.showError(state, "Failed to create: "+err.Error())
			return true
		}
		all := p.manager.ListProfiles()
		for i, pr := range all {
			if pr.ID == prof.ID {
				state.ProfilesSelected = i
				break
			}
		}
		p.openRolesEditor(prof.ID, state)
		return true

	case "c":
		if state.ProfilesSelected >= 0 && state.ProfilesSelected < len(profiles) {
			prof := profiles[state.ProfilesSelected]
			cloned, err := p.manager.CloneProfile(prof.ID, prof.Name+" (Copy)")
			if err != nil {
				p.showError(state, "Clone failed: "+err.Error())
			} else {
				p.showSuccess(state, "✨ Cloned as '"+cloned.Name+"'")
				all := p.manager.ListProfiles()
				for i, pr := range all {
					if pr.ID == cloned.ID {
						state.ProfilesSelected = i
						break
					}
				}
			}
		}
		return true

	case "x", "delete":
		if state.ProfilesSelected >= 0 && state.ProfilesSelected < len(profiles) {
			prof := profiles[state.ProfilesSelected]
			if prof.IsDefault {
				p.showError(state, "Cannot delete the default profile")
			} else if len(profiles) <= 1 {
				p.showError(state, "Cannot delete the last profile")
			} else {
				if err := p.manager.DeleteProfile(prof.ID); err != nil {
					p.showError(state, "Delete failed: "+err.Error())
				} else {
					p.showSuccess(state, "🗑  Deleted '"+prof.Name+"'")
					if state.ProfilesSelected >= len(profiles)-1 {
						state.ProfilesSelected = len(profiles) - 2
					}
				}
			}
		}
		return true

	case "r":
		// Enter rename mode for selected profile
		if state.ProfilesSelected >= 0 && state.ProfilesSelected < len(profiles) {
			prof := profiles[state.ProfilesSelected]
			p.renameMode = true
			p.renameInput = prof.Name
			p.renameIndex = state.ProfilesSelected
		}
		return true

	default:
		return false
	}
}

// handleRenameKey handles text input when renaming a profile
func (p *ProfileSettings) handleRenameKey(key string, state *State) bool {
	switch key {
	case "enter":
		// Save the rename
		if p.renameInput == "" {
			p.showError(state, "Profile name cannot be empty")
			return true
		}
		profiles := p.manager.ListProfiles()
		if p.renameIndex >= 0 && p.renameIndex < len(profiles) {
			prof := profiles[p.renameIndex]
			if err := p.manager.RenameProfile(prof.ID, p.renameInput); err != nil {
				p.showError(state, "Rename failed: "+err.Error())
			} else {
				p.showSuccess(state, "✓ Renamed to '"+p.renameInput+"'")
			}
		}
		p.renameMode = false
		p.renameInput = ""
		p.renameIndex = -1
		return true

	case "esc":
		// Cancel rename
		p.renameMode = false
		p.renameInput = ""
		p.renameIndex = -1
		return true

	case "backspace":
		// Delete last character
		if len(p.renameInput) > 0 {
			p.renameInput = p.renameInput[:len(p.renameInput)-1]
		}
		return true

	default:
		// Handle character input
		if len(key) == 1 {
			p.renameInput += key
			return true
		}
		// Handle space key
		if key == " " {
			p.renameInput += " "
			return true
		}
		return false
	}
}

// ── roles editor keys ─────────────────────────────────────────────────────────

func (p *ProfileSettings) handleRolesKey(key string, state *State) bool {
	aliases := AllAliases()

	// ctrl+s: force-save immediately regardless of picker focus state.
	// Checked first so it can never be swallowed by the picker's key handler.
	// On macOS, Ctrl+S can be intercepted by the terminal; the lowercase "s"
	// alias (when picker is unfocused) provides a reliable fallback.
	if key == "ctrl+s" {
		p.saveAllRoles(state)
		state.ProfilesState = "list"
		return true
	}

	// When picker has focus, forward keys to it (except focus-leave keys)
	if p.pickerFocused && p.activePicker != nil {
		switch key {
		case "left", "esc":
			// Return focus to role list
			p.pickerFocused = false
			return true
		default:
			handled := p.activePicker.HandleKey(key)
			if handled {
				// Update pending after picker action
				if p.activeAlias != "" {
					p.pendingRoles[p.activeAlias] = p.activePicker.GetChain()
				}
				return true
			}
			// If picker didn't handle it, fall through to role-list keys below
		}
	}

	// Role-list focus
	switch key {
	case "up", "k":
		if p.rolesSelected > 0 {
			p.rolesSelected--
			p.invalidatePicker()
		}
		return true

	case "down", "j":
		if p.rolesSelected < len(aliases)-1 {
			p.rolesSelected++
			p.invalidatePicker()
		}
		return true

	case "right", "enter", "tab":
		// Move focus into picker
		p.pickerFocused = true
		// Ensure picker exists
		if p.activePicker == nil || p.activeAlias != aliases[p.rolesSelected] {
			p.buildPickerForAlias(aliases[p.rolesSelected], p.getEditingProfile())
		}
		return true

	case " ":
		// Toggle checkbox for selected role
		if p.rolesSelected >= 0 && p.rolesSelected < len(aliases) {
			alias := aliases[p.rolesSelected]
			p.roleChecked[alias] = !p.roleChecked[alias]
		}
		return true

	case "a":
		// Bulk apply: apply current model to all checked roles
		p.bulkApplyToChecked(state)
		return true

	case "s", "ctrl+s":
		p.saveAllRoles(state)
		state.ProfilesState = "list"
		return true

	case "esc":
		p.discardEdits()
		state.ProfilesState = "list"
		return true

	default:
		return false
	}
}

// ============================================================================
// ROLES EDITOR HELPERS
// ============================================================================

func (p *ProfileSettings) openRolesEditor(profileID string, state *State) {
	p.editingProfileID = profileID
	p.rolesSelected = 0
	p.pickerFocused = false
	p.activePicker = nil
	p.activeAlias = ""
	p.pendingRoles = make(map[ModelAlias]*fallback.Chain)
	// Initialize role checkboxes - default to checked for bulk operations
	p.roleChecked = make(map[ModelAlias]bool)
	for _, alias := range AllAliases() {
		p.roleChecked[alias] = true
	}
	state.ProfilesState = "edit_roles"
}

func (p *ProfileSettings) invalidatePicker() {
	p.activePicker = nil
	p.activeAlias = ""
	p.pickerFocused = false
}

func (p *ProfileSettings) discardEdits() {
	p.pendingRoles = nil
	p.activePicker = nil
	p.activeAlias = ""
	p.pickerFocused = false
}

func (p *ProfileSettings) saveAllRoles(state *State) {
	profile, err := p.manager.GetProfile(p.editingProfileID)
	if err != nil || profile == nil {
		p.showError(state, "Profile not found")
		return
	}

	// Flush active picker into pending before committing
	if p.activePicker != nil && p.activeAlias != "" {
		p.pendingRoles[p.activeAlias] = p.activePicker.GetChain()
	}

	if profile.Roles == nil {
		profile.Roles = make(map[ModelAlias]RoleConfig)
	}

	for alias, chain := range p.pendingRoles {
		if chain == nil || chain.IsEmpty() {
			delete(profile.Roles, alias)
			continue
		}
		existing, had := profile.Roles[alias]
		if !had {
			profile.Roles[alias] = RoleConfig{Chain: chain, Enabled: true}
		} else {
			existing.Chain = chain
			existing.Enabled = true // roles are always enabled
			profile.Roles[alias] = existing
		}
	}

	if err := p.manager.UpdateProfile(profile.ID, *profile); err != nil {
		p.showError(state, "Save failed: "+err.Error())
		return
	}

	// Signal that this profile was saved so the Manager can re-sync the SDK
	// model when the active profile's roles change.
	p.lastSavedProfileID = profile.ID

	p.discardEdits()
	p.showSuccess(state, "✓ Saved roles for '"+profile.Name+"'")
}

// bulkApplyToChecked applies the current picker's model chain to all checked roles.
// This allows quickly setting the same model for multiple sub-agent types.
func (p *ProfileSettings) bulkApplyToChecked(state *State) {
	// Get the current model chain from the active picker
	if p.activePicker == nil {
		p.showError(state, "No model selected - select a role first")
		return
	}
	chain := p.activePicker.GetChain()
	if chain == nil || chain.IsEmpty() {
		p.showError(state, "No model selected")
		return
	}

	// Count checked roles
	checkedCount := 0
	for _, checked := range p.roleChecked {
		if checked {
			checkedCount++
		}
	}
	if checkedCount == 0 {
		p.showError(state, "No roles checked - use Space to check roles")
		return
	}

	// Apply to all checked roles
	for alias, checked := range p.roleChecked {
		if checked {
			p.pendingRoles[alias] = chain
		}
	}

	p.showSuccess(state, fmt.Sprintf("✓ Applied model to %d checked roles", checkedCount))
}

func (p *ProfileSettings) getEditingProfile() *AgentProfile {
	if p.editingProfileID == "" {
		return nil
	}
	profile, err := p.manager.GetProfile(p.editingProfileID)
	if err != nil {
		return nil
	}
	return profile
}

// ============================================================================
// FEEDBACK MESSAGES
// ============================================================================

func (p *ProfileSettings) showError(state *State, message string) {
	state.ProfilesErrorMessage = message
	state.ProfilesSuccessMessage = ""
	state.ProfilesMessageTimeout = 180
}

func (p *ProfileSettings) showSuccess(state *State, message string) {
	state.ProfilesSuccessMessage = message
	state.ProfilesErrorMessage = ""
	state.ProfilesMessageTimeout = 120
}

func (p *ProfileSettings) renderMessageBanner(state *State, width int, th Theme) string {
	if state.ProfilesMessageTimeout <= 0 {
		state.ProfilesErrorMessage = ""
		state.ProfilesSuccessMessage = ""
		return ""
	}
	state.ProfilesMessageTimeout--

	var msg, bg, fg, icon string
	if state.ProfilesErrorMessage != "" {
		msg, bg, fg, icon = state.ProfilesErrorMessage, th.Error, th.Text, "✗"
	} else if state.ProfilesSuccessMessage != "" {
		msg, bg, fg, icon = state.ProfilesSuccessMessage, th.Success, th.BG, "✓"
	} else {
		return ""
	}

	bannerPad := 2
	if width < 50 {
		bannerPad = 1
	}
	return lipgloss.NewStyle().
		Foreground(lipgloss.Color(fg)).Background(lipgloss.Color(bg)).
		Padding(0, bannerPad).Width(max(20, width-4)).Bold(true).
		Render(icon + " " + msg)
}

// ============================================================================
// ROLE DESCRIPTIONS
// ============================================================================

func roleDescription(alias string) string {
	switch alias {
	case "main":
		return "Primary conversation agent"
	case "steering":
		return "Supervisor — quality control and orchestration"
	case "background":
		return "Long-running async tasks"
	case "sub_agent":
		return "Fast specialised subtasks"
	case "inference":
		return "Quick reasoning, low latency"
	case "long_context":
		return "Large-context specialist (100k+ tokens)"
	case "compaction":
		return "Context summarisation"
	default:
		return ""
	}
}
