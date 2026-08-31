package chat

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/chat/appshell"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/chat/settings"
)

// ============================================================================
// HOME SCREEN VIEW — Dense terminal layout with Swarm theme colors
// All styles use explicit .Background() per AGENTS.md background rendering rules
// ============================================================================

const homeSidebarCacheTTL = 30 * time.Second

// refreshHomeSidebarCache populates all git data used by the sidebar.
// Called once on first render and then every 30s. All git shell-outs happen here,
// NEVER in the render path.
func (a *App) refreshHomeSidebarCache() {
	c := &a.homeSidebarCache
	c.currentBranch = a.getGitBranch()
	c.commits = a.getRecentCommits(6)
	c.headFiles, c.headIns, c.headDel = a.getHeadDiffStat()
	c.modifiedFiles = a.getModifiedFiles(20)

	// Branch parent for current branch only
	c.branchParents = make(map[string]string)
	if c.currentBranch != "" {
		c.branchParents[c.currentBranch] = a.getBranchParent(c.currentBranch)
	}

	c.lastRefresh = time.Now()
	c.valid = true
}

// ensureHomeSidebarCache checks if cache needs refresh and does so if stale.
func (a *App) ensureHomeSidebarCache() {
	if !a.homeSidebarCache.valid || time.Since(a.homeSidebarCache.lastRefresh) > homeSidebarCacheTTL {
		a.refreshHomeSidebarCache()
	}
}

// highlightInputSyntax applies syntax highlighting to @mentions and /commands
// highlightInputSyntax applies syntax highlighting to @mentions and /commands
// When cursor is inside a mention, shows raw format. Otherwise shows display name.
func highlightInputSyntax(text string, cursorPos int, agents []settings.CustomAgentEntry, theme Theme, bgColor string) string {
	if text == "" {
		return text
	}

	// Build agent ID -> Name lookup map
	agentNames := make(map[string]string)
	for _, agent := range agents {
		agentNames[agent.ID] = agent.Name
	}

	// Style for @mentions
	mentionStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(theme.Primary)).
		Background(lipgloss.Color(bgColor)).
		Bold(true)

	// Style for slash commands
	commandStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(theme.Success)).
		Background(lipgloss.Color(bgColor)).
		Bold(true)

	// Base style for regular text
	baseStyle := lipgloss.NewStyle().Background(lipgloss.Color(bgColor))

	// Process line by line to handle newlines properly
	lines := strings.Split(text, "\n")
	lineOffset := 0

	for lineIdx, line := range lines {
		plainLine := stripANSI(line)

		// Highlight slash commands at start of line
		trimmed := strings.TrimLeft(plainLine, " \t")
		if strings.HasPrefix(trimmed, "/") {
			cmdEnd := 1
			for cmdEnd < len(trimmed) && trimmed[cmdEnd] != ' ' {
				cmdEnd++
			}

			if cmdEnd > 1 {
				cmd := trimmed[:cmdEnd]
				rest := trimmed[cmdEnd:]

				styled := commandStyle.Render(cmd)
				if rest != "" {
					styled += baseStyle.Render(rest)
				}

				leadingSpace := plainLine[:len(plainLine)-len(trimmed)]
				if leadingSpace != "" {
					lines[lineIdx] = baseStyle.Render(leadingSpace) + styled
				} else {
					lines[lineIdx] = styled
				}
				lineOffset += len(plainLine) + 1 // +1 for newline
				continue
			}
		}

		// Highlight @mentions
		var result strings.Builder
		remaining := plainLine
		charPos := lineOffset

		for {
			idx := strings.Index(remaining, "@")
			if idx == -1 {
				if remaining != "" {
					result.WriteString(baseStyle.Render(remaining))
				}
				break
			}

			// Add text before the @
			if idx > 0 {
				result.WriteString(baseStyle.Render(remaining[:idx]))
				charPos += idx
			}

			// Find the end of the mention
			endIdx := idx + 1
			for endIdx < len(remaining) && remaining[endIdx] != ' ' && remaining[endIdx] != '\n' {
				endIdx++
			}

			if endIdx > idx+1 {
				mention := remaining[idx:endIdx]
				mentionStart := charPos
				mentionEnd := charPos + len(mention)

				// Check if cursor is inside this mention
				cursorInside := cursorPos >= mentionStart && cursorPos <= mentionEnd

				// Parse mention: @type:value
				parts := strings.SplitN(mention[1:], ":", 2)
				if len(parts) == 2 {
					mentionType := parts[0]
					mentionValue := parts[1]

					if mentionType == "agent" {
						// Check for namespace format: agent:namespace:name
						agentParts := strings.SplitN(mentionValue, ":", 2)
						agentID := ""
						if len(agentParts) == 2 {
							// Has namespace
							agentID = agentParts[1]
						} else {
							// No namespace
							agentID = mentionValue
						}

						if displayName, ok := agentNames[agentID]; ok && !cursorInside {
							// Show pretty format: @Display Name (remove agent: prefix completely)
							// Apply styling to the entire name including spaces
							prettyMention := "@" + displayName
							result.WriteString(mentionStyle.Render(prettyMention))
						} else {
							// Show raw format (cursor inside or no display name found)
							result.WriteString(mentionStyle.Render(mention))
						}
					} else if mentionType == "file" {
						// File/folder mentions: show as @file:path
						if !cursorInside {
							// Pretty format: @📄 filename or @📁 foldername
							baseName := filepath.Base(mentionValue)
							// Check if it's a directory (ends with /)
							if strings.HasSuffix(mentionValue, "/") {
								// Directory
								baseName = strings.TrimSuffix(baseName, "/")
								if baseName == "" {
									// Root directory case
									baseName = mentionValue
								}
								result.WriteString(mentionStyle.Render("@📁 " + baseName))
							} else {
								// File
								result.WriteString(mentionStyle.Render("@📄 " + baseName))
							}
						} else {
							// Raw format (cursor inside)
							result.WriteString(mentionStyle.Render(mention))
						}
					} else {
						// Other mention types - always show raw
						result.WriteString(mentionStyle.Render(mention))
					}

					charPos = mentionEnd
					remaining = remaining[endIdx:]
					continue
				}
			}

			// Not a valid mention, just add the @ and continue
			result.WriteString(baseStyle.Render("@"))
			charPos++
			remaining = remaining[idx+1:]
		}

		if result.String() != "" {
			lines[lineIdx] = result.String()
		}

		lineOffset += len(plainLine) + 1 // +1 for newline
	}

	return strings.Join(lines, "\n")
}

func (a *App) viewHome(_ any) string {
	th := a.theme
	bg := th.BG

	// ── Full-screen animation ─────────────────────────────────────────
	// While the TTE animation is running, return its frame directly — the entire
	// screen is the animation canvas.
	// Use reapplyBackground to ensure seamless transition (handles ANSI resets).
	if !a.homeTitleDone && a.homeTitleFrame != "" {
		animFrame := lipgloss.NewStyle().
			Width(a.homeBurnW).
			Height(a.homeBurnH).
			Background(lipgloss.Color(bg)).
			Render(a.homeTitleFrame)
		return reapplyBackground(animFrame, bg)
	}

	progress := a.menuProgress()
	w := a.width
	h := a.height

	// Ensure sidebar git data is cached (never shell out in render path)
	a.ensureHomeSidebarCache()

	// ── Sync shared TabBar with current state ────────────────────────────
	a.syncTabBar()

	// ── Tab bar ─────────────────────────────────────────────────────────
	tabOpacity := a.fade(progress, 0.0, 0.1)
	tabBarView := a.tabBar.Render(w)
	tabBarView = a.applyOpacity(tabBarView, tabOpacity)

	// ── Available height for content area ────────────────────────────────
	tabH := appshell.TabBarHeight
	contentH := h - tabH
	if contentH < 10 {
		contentH = 10
	}

	// ── Settings tab: custom layout (settings sidebar + settings content) ─
	isSettingsTab := !a.homeInputFocused && a.homeButton == ButtonSettings
	if isSettingsTab {
		settingsView := a.renderSettingsInline(w, contentH)
		settingsView = a.applyOpacity(settingsView, a.fade(progress, 0.05, 0.2))

		content := lipgloss.JoinVertical(lipgloss.Left, tabBarView, settingsView)
		return lipgloss.NewStyle().
			Width(w).
			Height(h).
			Background(lipgloss.Color(bg)).
			Render(content)
	}

	// ── Non-settings tabs: git sidebar + main content ────────────────────
	fullSidebarW := 36
	showSidebar := !a.homeSidebarCollapsed && w >= 96
	sidebarW := 0
	if showSidebar {
		sidebarW = fullSidebarW
	}

	mainW := w - sidebarW

	sidebarContent := ""
	if showSidebar {
		sidebarOpacity := a.fade(progress, 0.05, 0.15)
		sidebarContent = a.renderTerminalSidebar(sidebarW, contentH)
		sidebarContent = a.applyOpacity(sidebarContent, sidebarOpacity)
	}

	mainOpacity := a.fade(progress, 0.05, 0.2)
	mainContent := a.renderTerminalMain(mainW, contentH, !showSidebar)
	mainContent = a.applyOpacity(mainContent, mainOpacity)

	var middleSection string
	if showSidebar {
		middleSection = lipgloss.JoinHorizontal(lipgloss.Top, sidebarContent, mainContent)
	} else {
		middleSection = mainContent
	}

	// ── Assemble ────────────────────────────────────────────────────────
	content := lipgloss.JoinVertical(lipgloss.Left,
		tabBarView,
		middleSection,
	)

	// Adjust overlay coordinates to absolute position in final composed content
	a.homeInputOverlayY += tabH         // tab bar rows above main content
	a.homeInputOverlayX += sidebarW + 3 // sidebar width + main content Padding(0,3)

	return lipgloss.NewStyle().
		Width(w).
		Height(h).
		Background(lipgloss.Color(bg)).
		Render(content)
}

// syncTabBar updates the shared TabBar's active index AND theme colours on every render.
func (a *App) syncTabBar() {
	if a.tabBar == nil {
		return
	}
	// Always keep theme in sync so tab bar immediately reflects any theme change
	a.tabBar.Theme.BG = a.theme.BG
	a.tabBar.Theme.Primary = a.theme.Primary
	a.tabBar.Theme.PrimaryDim = a.theme.PrimaryDim
	a.tabBar.Theme.Text = a.theme.Text
	a.tabBar.Theme.TextMuted = a.theme.TextMuted

	if a.homeInputFocused {
		a.tabBar.SetActiveByID("prompt")
	} else {
		switch a.homeButton {
		case ButtonNewChat:
			a.tabBar.SetActiveByID("prompt")
		case ButtonConversations:
			a.tabBar.SetActiveByID("history")
		case ButtonUsage:
			a.tabBar.SetActiveByID("usage")
		case ButtonSettings:
			a.tabBar.SetActiveByID("settings")
		default:
			a.tabBar.SetActiveByID("prompt")
		}
	}
}

// propagateTheme pushes new theme colours to every component that caches
// a local copy of bg/fg/border values.  Call this whenever a.theme changes.
func (a *App) propagateTheme(th Theme) {
	// Tab bar — sync all theme fields so the tab bar re-renders with new colours
	if a.tabBar != nil {
		a.tabBar.Theme.BG = th.BG
		a.tabBar.Theme.Primary = th.Primary
		a.tabBar.Theme.PrimaryDim = th.PrimaryDim
		a.tabBar.Theme.Text = th.Text
		a.tabBar.Theme.TextMuted = th.TextMuted
	}

	// Collapse widget — BackgroundColor was hardcoded to palette.Surface at init
	if a.collapseWidget != nil {
		cfg := a.collapseWidget.GetConfig()
		cfg.BackgroundColor = th.BG
		cfg.HintBarColor = th.Warning // Use theme warning color for Ctrl+B bar
		a.collapseWidget.SetConfig(cfg)
	}

	// Mention autocomplete dropdown
	if a.mentionAutocomplete != nil {
		mentionBg := th.BGLight
		if mentionBg == "" {
			mentionBg = th.BG
		}
		a.mentionAutocomplete.SetTheme(
			mentionBg,
			th.Border,
			th.Accent,
			th.PrimaryDim,
			th.Text,
			th.TextDim,
		)
	}

	// Command autocomplete dropdown
	if a.cmdAutocomplete != nil {
		cmdBg := th.BGLight
		if cmdBg == "" {
			cmdBg = th.BG
		}
		a.cmdAutocomplete.SetTheme(
			cmdBg,
			th.Border,
			th.Accent,
			th.PrimaryDim,
			th.Text,
			th.TextDim,
		)
	}

	// Loading indicator caches lipgloss styles derived from its theme.
	if a.loadingIndicator != nil {
		a.loadingIndicator.Theme = th
		a.loadingIndicator.stylesInited = false
		a.loadingIndicator.initStyles()
	}

	// Side panel — invalidate cache so it re-renders with new theme colours
	a.sidePanelCache.valid = false
}

// applyTerminalBackground reapplies the selected base theme after Bubble Tea's
// asynchronous OSC 11 response arrives. It runs only from Update, keeping all
// model mutation on the Bubble Tea event loop.
func (a *App) applyTerminalBackground(hasDark bool) {
	if a.hasDarkTerminal == hasDark {
		return
	}

	a.hasDarkTerminal = hasDark
	base := DefaultTheme
	if a.renderSettings != nil && a.renderSettings.ThemeName != "" {
		base = ThemeByName(a.renderSettings.ThemeName)
	}
	if a.autovacMode {
		base = AutoVacTheme
	}

	adapted := AdaptThemeForTerminal(base, hasDark)
	if a.renderSettings != nil && a.renderSettings.BackgroundMode == "none" {
		adapted.BG = ""
		adapted.BGLight = ""
		adapted.BGLighter = ""
	}

	a.theme = adapted
	a.propagateTheme(adapted)
	if a.messageCache != nil {
		a.messageCache.Clear()
	}
	a.invalidateViewportCache()
	a.viewNeedsRefresh = true
	logDebug("[THEME] terminal hasDarkBackground=%v", hasDark)
}

// renderSettingsInline renders the settings view inline within the home screen layout.
// Uses the settings manager's extracted sidebar + content + hints methods.
func (a *App) renderSettingsInline(width, height int) string {
	if a.settingsManager == nil {
		return lipgloss.NewStyle().
			Width(width).
			Height(height).
			Align(lipgloss.Center, lipgloss.Center).
			Foreground(lipgloss.Color(a.theme.Error)).
			Background(lipgloss.Color(a.theme.BG)).
			Render(tr("classic.home.settings_unavailable"))
	}

	animationFrame := 0
	if a.animationClock != nil {
		animationFrame = a.animationClock.Frame()
	}

	hintsH := 2

	if width >= 80 {
		// Two-pane: sidebar + content
		sidebarWidth := width / 4
		if sidebarWidth < 20 {
			sidebarWidth = 20
		}
		if sidebarWidth > 40 {
			sidebarWidth = 40
		}
		contentWidth := width - sidebarWidth - 1
		if contentWidth < 30 {
			contentWidth = 30
		}
		contentHeight := height - hintsH
		if contentHeight < 5 {
			contentHeight = 5
		}

		sidebar := a.settingsManager.RenderSidebar(sidebarWidth, contentHeight, a.theme)
		content := a.settingsManager.RenderContent(contentWidth, contentHeight, a.theme, animationFrame)
		hints := a.settingsManager.RenderHints(width, a.theme)

		combined := lipgloss.JoinHorizontal(lipgloss.Top, sidebar, content)
		combined = reapplyBackground(combined, a.theme.BG)

		// Overlay exit modal if shown
		state := a.settingsManager.GetState()
		if state.ShowExitModal {
			combined = a.renderSettingsExitModal(combined, width, contentHeight)
		}

		result := lipgloss.JoinVertical(lipgloss.Left, combined, hints)
		return reapplyBackground(result, a.theme.BG)
	}

	// Single-pane: delegate to Manager.Render which handles narrow layout
	result := a.settingsManager.Render(width, height, a.theme, animationFrame)

	// Overlay exit modal if shown
	state := a.settingsManager.GetState()
	if state.ShowExitModal {
		contentHeight := height - hintsH
		if contentHeight < 5 {
			contentHeight = 5
		}
		result = a.renderSettingsExitModal(result, width, contentHeight)
	}

	return reapplyBackground(result, a.theme.BG)
}

// renderSettingsExitModal renders the exit modal overlaid on settings content.
func (a *App) renderSettingsExitModal(baseView string, width, height int) string {
	modalWidth := 60
	modalHeight := 10

	options := []string{
		tr("classic.home.save_exit"),
		tr("classic.home.exit_without_saving"),
		tr("classic.common.cancel"),
	}

	state := a.settingsManager.GetState()

	var lines []string

	modalBG := a.theme.BGLight

	titleStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(a.theme.Primary)).
		Background(lipgloss.Color(modalBG)).
		Bold(true).
		Align(lipgloss.Center).
		Width(modalWidth - 4)
	lines = append(lines, titleStyle.Render(tr("classic.home.exit_settings")))
	lines = append(lines, reapplyBackground("", modalBG))

	msgStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(a.theme.Text)).
		Background(lipgloss.Color(modalBG)).
		Align(lipgloss.Center).
		Width(modalWidth - 4)
	lines = append(lines, msgStyle.Render(tr("classic.home.save_changes")))
	lines = append(lines, reapplyBackground("", modalBG))

	for i, option := range options {
		isSelected := i == state.ExitModalChoice
		optionStyle := lipgloss.NewStyle().
			Foreground(lipgloss.Color(a.theme.Text)).
			Background(lipgloss.Color(modalBG)).
			Width(modalWidth - 4).
			Align(lipgloss.Center)
		if isSelected {
			optionStyle = optionStyle.
				Foreground(lipgloss.Color(a.theme.Primary)).
				Bold(true)
			lines = append(lines, optionStyle.Render("▶ "+option))
		} else {
			lines = append(lines, optionStyle.Render("  "+option))
		}
	}

	hintStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(a.theme.TextMuted)).
		Background(lipgloss.Color(modalBG)).
		Align(lipgloss.Center).
		Width(modalWidth - 4)
	lines = append(lines, reapplyBackground("", modalBG))
	lines = append(lines, hintStyle.Render(tr("classic.home.exit_hint")))

	modalContent := lipgloss.JoinVertical(lipgloss.Left, lines...)

	modalStyle := lipgloss.NewStyle().
		Width(modalWidth).
		Height(modalHeight+2).
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color(a.theme.Primary)).
		Background(lipgloss.Color(a.theme.BGLight)).
		Padding(1, 2)

	modal := modalStyle.Render(modalContent)

	bgStyle := lipgloss.NewStyle().Background(lipgloss.Color(a.theme.BG))
	return lipgloss.Place(
		width,
		height,
		lipgloss.Center,
		lipgloss.Center,
		modal,
		lipgloss.WithWhitespaceChars(" "),
		lipgloss.WithWhitespaceStyle(bgStyle),
	)
}

// ── Tab bar ─────────────────────────────────────────────────────────────
// ── Sidebar ─────────────────────────────────────────────────────────────
func (a *App) renderTerminalSidebar(width, height int) string {
	th := a.theme
	sbBg := th.BGLight
	innerW := width - 4

	// Section header with colored left accent
	sectionHeader := func(title string, color string) string {
		accent := lipgloss.NewStyle().
			Foreground(lipgloss.Color(sbBg)).
			Background(lipgloss.Color(color)).
			Render("▌")
		label := lipgloss.NewStyle().
			Foreground(lipgloss.Color(color)).
			Background(lipgloss.Color(sbBg)).
			Bold(true).
			Render(" " + title)
		return reapplyBackground(accent+label, sbBg)
	}

	labelW := 5
	labelStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(th.TextMuted)).
		Background(lipgloss.Color(sbBg)).
		Width(labelW)
	valueStyle := lipgloss.NewStyle().
		Background(lipgloss.Color(sbBg)).
		Bold(true)
	dimValStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(th.TextDim)).
		Background(lipgloss.Color(sbBg))
	accentValStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(th.Accent)).
		Background(lipgloss.Color(sbBg))

	emptyLine := lipgloss.NewStyle().
		Background(lipgloss.Color(sbBg)).
		Width(innerW).
		Render("")

	row := func(label, value string, style lipgloss.Style) string {
		r := labelStyle.Render(label) + style.Render(value)
		return reapplyBackground(r, sbBg)
	}

	maxValW := innerW - labelW
	if maxValW < 5 {
		maxValW = 5
	}

	var lines []string

	// ── SYSTEM — what tool you're using ──
	lines = append(lines, sectionHeader(tr("classic.home.system"), th.Primary))
	modelName := a.currentModelDisplay
	if modelName == "" {
		modelName = a.currentModel
	}
	if len(modelName) > maxValW {
		modelName = modelName[:maxValW-3] + "…"
	}
	lines = append(lines, row("mod", strings.ToUpper(modelName), valueStyle))
	providerName := a.currentProviderDisplay
	if providerName == "" {
		providerName = a.currentProvider
	}
	if len(providerName) > maxValW {
		providerName = providerName[:maxValW-3] + "…"
	}
	lines = append(lines, row("api", providerName, accentValStyle))
	dir := a.getWorkingDir()
	if len(dir) > maxValW {
		dir = "…" + dir[len(dir)-maxValW+1:]
	}
	lines = append(lines, row("dir", dir, dimValStyle))

	// Branch as system state
	currentBranch := a.homeSidebarCache.currentBranch
	if currentBranch != "" {
		branchIcon := lipgloss.NewStyle().
			Foreground(lipgloss.Color(th.Success)).
			Background(lipgloss.Color(sbBg)).
			Width(labelW).
			Render(" ⎇")
		branchName := currentBranch
		parent := a.homeSidebarCache.branchParents[currentBranch]
		suffix := ""
		if parent != "" {
			suffix = " ← " + parent
		}
		maxBranchW := maxValW - len(suffix)
		if maxBranchW < 8 {
			maxBranchW = maxValW
			suffix = ""
		}
		if len(branchName) > maxBranchW {
			branchName = branchName[:maxBranchW-1] + "…"
		}
		branchStyle := lipgloss.NewStyle().
			Background(lipgloss.Color(sbBg)).
			Bold(true)
		fromStyle := lipgloss.NewStyle().
			Foreground(lipgloss.Color(th.TextMuted)).
			Background(lipgloss.Color(sbBg)).
			Italic(true)
		bLine := branchIcon + branchStyle.Render(branchName)
		if suffix != "" {
			bLine += fromStyle.Render(suffix)
		}
		lines = append(lines, reapplyBackground(bLine, sbBg))
	}
	lines = append(lines, emptyLine)

	// ── VOICE — recording/transcribing indicator ──
	if a.voiceRecording || a.voiceTranscribing {
		lines = append(lines, sectionHeader(tr("classic.home.voice"), "#FF4444"))
		if a.voiceRecording {
			recDot := "●"
			if a.voicePulseTick {
				recDot = "○"
			}
			recStyle := lipgloss.NewStyle().
				Foreground(lipgloss.Color("#FF4444")).
				Background(lipgloss.Color(sbBg)).
				Bold(true)
			recLine := recStyle.Render(recDot + tr("classic.home.recording_short"))
			if a.voiceAutoRecord {
				autoStyle := lipgloss.NewStyle().
					Foreground(lipgloss.Color(th.TextMuted)).
					Background(lipgloss.Color(sbBg))
				recLine += autoStyle.Render(tr("classic.home.auto_badge"))
			}
			lines = append(lines, reapplyBackground(recLine, sbBg))
		} else if a.voiceTranscribing {
			transStyle := lipgloss.NewStyle().
				Foreground(lipgloss.Color("#FFD93D")).
				Background(lipgloss.Color(sbBg)).
				Bold(true)
			lines = append(lines, reapplyBackground(transStyle.Render(tr("classic.home.transcribing_short")), sbBg))
		}
		lines = append(lines, emptyLine)
	} else if a.voiceAutoRecord {
		// Show auto-record indicator even when idle
		lines = append(lines, sectionHeader(tr("classic.home.voice"), "#FF4444"))
		autoStyle := lipgloss.NewStyle().
			Foreground(lipgloss.Color(th.TextMuted)).
			Background(lipgloss.Color(sbBg))
		lines = append(lines, reapplyBackground(autoStyle.Render(tr("classic.home.auto_standby")), sbBg))
		lines = append(lines, emptyLine)
	}

	// ── RECENT — just commits, clean ──
	commits := a.homeSidebarCache.commits
	if len(commits) > 0 {
		lines = append(lines, sectionHeader(tr("classic.home.recent"), th.Accent))

		msgStyle := lipgloss.NewStyle().
			Background(lipgloss.Color(sbBg)).
			Italic(true)

		tagColors := map[string]string{
			"fix": th.Success, "feat": th.Primary, "chore": th.TextMuted,
			"docs": th.Info, "test": th.Warning, "ci": th.TextMuted,
			"style": th.Secondary, "perf": th.Accent, "refactor": th.Secondary,
		}
		const tagVisualW = 7 // enough for "chore" + padding

		for i, commit := range commits {
			if i >= 6 {
				break
			}
			cParts := strings.SplitN(commit, " ", 2)
			msg := commit
			if len(cParts) >= 2 {
				msg = cParts[1]
			}

			var tagStr string
			remaining := msg
			colonIdx := strings.Index(msg, ":")
			if colonIdx > 0 && colonIdx <= 20 {
				prefix := strings.TrimSpace(msg[:colonIdx])
				if parenIdx := strings.Index(prefix, "("); parenIdx > 0 {
					prefix = prefix[:parenIdx]
				}
				prefix = strings.ToLower(prefix)
				tagColor := ""
				for key, color := range tagColors {
					if prefix == key {
						tagColor = color
						break
					}
				}
				if tagColor != "" {
					tagStyle := lipgloss.NewStyle().
						Foreground(lipgloss.Color(th.BG)).
						Background(lipgloss.Color(tagColor)).
						Bold(true).
						Width(tagVisualW).
						Align(lipgloss.Center)
					tagStr = tagStyle.Render(prefix)
					remaining = strings.TrimSpace(msg[colonIdx+1:])
				}
			}

			maxMsg := innerW - 1
			if tagStr != "" {
				maxMsg = innerW - tagVisualW - 2
			}
			if maxMsg < 8 {
				maxMsg = 8
			}
			if len(remaining) > maxMsg {
				remaining = remaining[:maxMsg-1] + "…"
			}

			var cLine string
			if tagStr != "" {
				cLine = tagStr + " " + msgStyle.Render(remaining)
			} else {
				cLine = msgStyle.Render(" " + remaining)
			}
			lines = append(lines, reapplyBackground(cLine, sbBg))
		}
	}
	lines = append(lines, emptyLine)

	// ── CHANGES — sorted by churn, ● staged / ○ unstaged ──
	modFiles := a.homeSidebarCache.modifiedFiles
	if len(modFiles) > 0 {
		currentLines := len(lines)
		availableLines := height - 4 - currentLines
		if availableLines >= 4 {
			// Sort by churn (total additions + deletions, descending)
			sorted := make([]modifiedFileEntry, len(modFiles))
			copy(sorted, modFiles)
			sort.Slice(sorted, func(i, j int) bool {
				return (sorted[i].ins + sorted[i].del) > (sorted[j].ins + sorted[j].del)
			})

			// Max files to show
			maxFiles := availableLines - 2 // header + overflow line
			if maxFiles > 10 {
				maxFiles = 10
			}
			if maxFiles > len(sorted) {
				maxFiles = len(sorted)
			}
			displayed := sorted[:maxFiles]

			// Header: ▌ CHANGES  20 files  +1950 -725
			changesHeader := sectionHeader(tr("classic.home.changes"), th.Warning)
			countStyle := lipgloss.NewStyle().
				Foreground(lipgloss.Color(th.TextMuted)).
				Background(lipgloss.Color(sbBg))
			hdrInsStyle := lipgloss.NewStyle().
				Foreground(lipgloss.Color(th.Success)).
				Background(lipgloss.Color(sbBg)).
				Bold(true)
			hdrDelStyle := lipgloss.NewStyle().
				Foreground(lipgloss.Color(th.Error)).
				Background(lipgloss.Color(sbBg)).
				Bold(true)
			changesHeader += countStyle.Render(tr("classic.home.file_count", len(modFiles)))
			headIns := a.homeSidebarCache.headIns
			headDel := a.homeSidebarCache.headDel
			if headIns != "" {
				changesHeader += " " + hdrInsStyle.Render(headIns)
			}
			if headDel != "" {
				changesHeader += " " + hdrDelStyle.Render(headDel)
			}
			lines = append(lines, reapplyBackground(changesHeader, sbBg))

			// Styles
			fileStyle := lipgloss.NewStyle().
				Background(lipgloss.Color(sbBg))
			fileBoldStyle := lipgloss.NewStyle().
				Background(lipgloss.Color(sbBg)).
				Bold(true)
			insStyle := lipgloss.NewStyle().
				Foreground(lipgloss.Color(th.Success)).
				Background(lipgloss.Color(sbBg))
			insBoldStyle := lipgloss.NewStyle().
				Foreground(lipgloss.Color(th.Success)).
				Background(lipgloss.Color(sbBg)).
				Bold(true)
			delStyle := lipgloss.NewStyle().
				Foreground(lipgloss.Color(th.Error)).
				Background(lipgloss.Color(sbBg))
			delBoldStyle := lipgloss.NewStyle().
				Foreground(lipgloss.Color(th.Error)).
				Background(lipgloss.Color(sbBg)).
				Bold(true)
			stagedDot := lipgloss.NewStyle().
				Foreground(lipgloss.Color(th.Success)).
				Background(lipgloss.Color(sbBg))
			unstagedDot := lipgloss.NewStyle().
				Foreground(lipgloss.Color(th.TextMuted)).
				Background(lipgloss.Color(sbBg))

			// Pre-compute max stat widths for right-alignment
			maxInsW := 0
			maxDelW := 0
			for _, f := range displayed {
				if f.ins > 0 {
					w := len(fmt.Sprintf("+%d", f.ins))
					if w > maxInsW {
						maxInsW = w
					}
				}
				if f.del > 0 {
					w := len(fmt.Sprintf("-%d", f.del))
					if w > maxDelW {
						maxDelW = w
					}
				}
			}

			statColW := 0
			if maxInsW > 0 {
				statColW += maxInsW + 1
			}
			if maxDelW > 0 {
				statColW += maxDelW + 1
			}

			for _, f := range displayed {
				// ● or ○
				var dot string
				if f.staged {
					dot = stagedDot.Render("●")
				} else {
					dot = unstagedDot.Render("○")
				}

				displayPath := filepath.Base(f.path)
				churn := f.ins + f.del
				isBold := churn >= 100

				// Pick style based on churn threshold
				fStyle := fileStyle
				iStyle := insStyle
				dStyle := delStyle
				if isBold {
					fStyle = fileBoldStyle
					iStyle = insBoldStyle
					dStyle = delBoldStyle
				}

				// Stats
				insStr := ""
				delStr := ""
				if f.ins > 0 {
					insStr = fmt.Sprintf("+%d", f.ins)
				}
				if f.del > 0 {
					delStr = fmt.Sprintf("-%d", f.del)
				}

				paddedIns := ""
				if maxInsW > 0 {
					if insStr != "" {
						paddedIns = fmt.Sprintf("%*s", maxInsW, insStr)
					} else {
						paddedIns = strings.Repeat(" ", maxInsW)
					}
				}
				paddedDel := ""
				if maxDelW > 0 {
					if delStr != "" {
						paddedDel = fmt.Sprintf("%*s", maxDelW, delStr)
					} else {
						paddedDel = strings.Repeat(" ", maxDelW)
					}
				}

				// " ● filename     +NNN -NNN"
				// " ● " = 3 chars, then filename, then stats
				maxPathW := innerW - 3 - statColW - 1
				if maxPathW < 6 {
					maxPathW = 6
				}
				if len(displayPath) > maxPathW {
					displayPath = displayPath[:maxPathW-1] + "…"
				}
				padCount := maxPathW - len(displayPath)
				if padCount < 0 {
					padCount = 0
				}
				padded := displayPath + strings.Repeat(" ", padCount)

				fLine := " " + dot + " " + fStyle.Render(padded)
				if paddedIns != "" {
					fLine += " " + iStyle.Render(paddedIns)
				}
				if paddedDel != "" {
					fLine += " " + dStyle.Render(paddedDel)
				}
				lines = append(lines, reapplyBackground(fLine, sbBg))
			}

			// Overflow indicator
			if len(sorted) > maxFiles {
				moreStyle := lipgloss.NewStyle().
					Foreground(lipgloss.Color(th.TextMuted)).
					Background(lipgloss.Color(sbBg)).
					Italic(true)
				moreStr := tr("classic.common.more_down_compact", len(sorted)-maxFiles)
				// Right-align the overflow indicator
				pad := innerW - len(moreStr) - 2
				if pad < 0 {
					pad = 0
				}
				lines = append(lines, reapplyBackground(
					strings.Repeat(" ", pad)+moreStyle.Render(moreStr), sbBg))
			}
		}
	}

	// Pad remaining height
	content := strings.Join(lines, "\n")
	cH := strings.Count(content, "\n") + 1
	for cH < height-2 {
		content += "\n" + emptyLine
		cH++
	}

	return lipgloss.NewStyle().
		Width(width).
		Height(height).
		Background(lipgloss.Color(sbBg)).
		BorderRight(true).
		BorderStyle(lipgloss.NormalBorder()).
		BorderForeground(lipgloss.Color(th.Border)).
		Padding(1, 1).
		Render(content)
}

// ── Collapsed sidebar (thin icon strip) ─────────────────────────────────
// ── Main content ────────────────────────────────────────────────────────
func (a *App) renderTerminalMain(width, height int, showInfoStrip bool) string {

	// Route to appropriate tab content
	// Note: ButtonSettings is handled by renderSettingsInline() in viewHome() before this is called.
	switch a.homeButton {
	case ButtonConversations:
		// Consolidated history menu: render the SAME single Layout-C menu
		// (compact list + summary panel) that ScreenChats uses, so there is
		// only ONE history surface regardless of how the user reaches it.
		result := a.renderTwoPane(width, height)
		return result
	case ButtonUsage:
		result := a.renderUsageTab(width, height)
		return result
	default: // ButtonNewChat (and fallback)
		return a.renderPromptTab(width, height, showInfoStrip)
	}
}

// renderPromptTab renders the default PROMPT tab (input box + resume)
func (a *App) renderPromptTab(width, height int, showInfoStrip bool) string {
	th := a.theme
	bg := th.BG
	innerW := width - 6

	bgLine := func(s string) string {
		return reapplyBackground(s, bg)
	}
	emptyLine := lipgloss.NewStyle().
		Background(lipgloss.Color(bg)).
		Width(innerW).
		Render("")

	// Title — lowercase like original
	title := lipgloss.NewStyle().
		Foreground(lipgloss.Color(th.Primary)).
		Background(lipgloss.Color(bg)).
		Bold(true).
		Render("swarm")

	// Subtitle — lowercase
	subtitle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(th.TextDim)).
		Background(lipgloss.Color(bg)).
		Render(tr("classic.home.tagline"))

	// ── Session resumption ──
	var resumeLine string
	if len(a.conversations) > 0 {
		last := a.conversations[0] // most recent
		elapsed := time.Since(last.LastMessage)
		if elapsed < 24*time.Hour && last.Title != "" {
			// Format time ago
			var ago string
			switch {
			case elapsed < time.Minute:
				ago = tr("classic.time.just_now")
			case elapsed < time.Hour:
				ago = tr("classic.time.minutes_ago", int(elapsed.Minutes()))
			default:
				ago = tr("classic.time.hours_ago", int(elapsed.Hours()))
			}

			badgeStyle := lipgloss.NewStyle().
				Foreground(lipgloss.Color(th.BG)).
				Background(lipgloss.Color(th.Accent)).
				Bold(true).
				Padding(0, 1)
			arrowStyle := lipgloss.NewStyle().
				Foreground(lipgloss.Color(th.Accent)).
				Background(lipgloss.Color(bg)).
				Bold(true)
			titleStyle := lipgloss.NewStyle()
			metaStyle := lipgloss.NewStyle().
				Foreground(lipgloss.Color(th.TextMuted)).
				Background(lipgloss.Color(bg))

			convTitle := last.Title
			maxTitleW := innerW - 40
			if maxTitleW < 15 {
				maxTitleW = 15
			}
			if len(convTitle) > maxTitleW {
				convTitle = convTitle[:maxTitleW-1] + "…"
			}

			resumeLine = bgLine(
				badgeStyle.Render("ctrl+↩") + " " +
					arrowStyle.Render(tr("classic.home.continue")) +
					titleStyle.Render("\""+convTitle+"\"") +
					metaStyle.Render(tr("classic.home.continue_meta", ago, last.MessageCount)))
		}
	}

	// ── Input (dynamic height, supports multi-line wrapping) ──
	// inputW is the content width inside the box (lipgloss Width).
	// The border adds 2 visual chars total, so cap at innerW-2 to avoid overflow.
	inputW := innerW - 2
	if inputW > 76 {
		inputW = 76
	}
	if inputW < 30 {
		inputW = 30
	}

	// SimpleInput wraps at (width - 6).  The bordered box uses Width(inputW)
	// which in lipgloss v2 *includes* border (2) + padding (4) = 6 overhead.
	// Inner content area = inputW - 6.  We need wrap + prefix ≤ inner:
	//   (inputW-2) - 6 + 2 = inputW - 6  ✓
	if a.homeInput != nil {
		a.homeInput.SetWidth(inputW - 2)
	}

	// Size autocomplete dropdowns to match input box width
	a.cmdAutocomplete.SetSize(inputW, 8)
	a.mentionAutocomplete.SetSize(inputW, 8)

	// Dynamic input height — grows from 1 to 6 based on content
	const homeInputMinH = 3
	const homeInputMaxH = 6
	inputContentHeight := homeInputMinH
	if a.homeInput != nil {
		inputContentHeight = a.homeInput.GetContentHeight()
		if inputContentHeight < homeInputMinH {
			inputContentHeight = homeInputMinH
		}
		if inputContentHeight > homeInputMaxH {
			inputContentHeight = homeInputMaxH
		}
		a.homeInput.SetHeight(inputContentHeight)
	}

	inputBg := th.BG

	// Check if voice is active - show voice prompt instead of input
	inputView := ""
	if a.homeInput != nil {
		// Feed active-skill keyword triggers so matching words glow Warning colour.
		if a.sdk != nil {
			a.homeInput.SetHighlightTerms(getActiveSkillKeywords(a.sdk.skillsManager))
		}
		inputView = a.homeInput.View()
	}

	// Fix background on input content — reapply black background across all ANSI resets
	inputView = reapplyBackground(inputView, inputBg)

	// Apply syntax highlighting for @mentions and /commands
	cursorPos := 0
	if a.homeInput != nil {
		cursorPos = a.homeInput.GetCursor()
	}
	agents := []settings.CustomAgentEntry{}
	if a.settingsManager != nil && a.settingsManager.GetAgentsSettings() != nil {
		agents = a.settingsManager.GetAgentsSettings().GetAgents()
	}
	inputView = highlightInputSyntax(inputView, cursorPos, agents, th, inputBg)

	// Multi-line: add prompt prefix to first line, indent continuation lines
	inputTextLines := strings.Split(inputView, "\n")
	var promptStr string
	if a.voiceRecording {
		dot := "● "
		if a.voicePulseTick {
			dot = "○ "
		}
		promptStr = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#FF4444")).
			Background(lipgloss.Color(inputBg)).
			Bold(true).
			Render(dot)
	} else {
		promptStr = lipgloss.NewStyle().
			Foreground(lipgloss.Color(th.Primary)).
			Background(lipgloss.Color(inputBg)).
			Bold(true).
			Render("❯ ")
	}

	// Create styled indent for continuation lines (2 spaces with background)
	indentStr := lipgloss.NewStyle().
		Background(lipgloss.Color(inputBg)).
		Render("  ")

	var inputWithPrompt string

	// Check if voice is active - show voice prompt instead of input
	if voicePrompt := a.renderVoicePrompt(); voicePrompt != "" {
		inputWithPrompt = voicePrompt
	} else {
		if len(inputTextLines) > 0 {
			var b strings.Builder
			b.WriteString(promptStr)
			b.WriteString(inputTextLines[0])
			for i := 1; i < len(inputTextLines); i++ {
				b.WriteString("\n")
				b.WriteString(indentStr)
				b.WriteString(inputTextLines[i])
			}
			inputWithPrompt = reapplyBackground(b.String(), inputBg)
		} else {
			inputWithPrompt = reapplyBackground(promptStr, inputBg)
		}

		// Input box: grey bg, white text, rounded border, focus-dependent border color
	}
	borderColor := th.Border
	if a.homeInputFocused {
		borderColor = th.Primary
	}
	inputBox := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color(borderColor)).
		BorderBackground(lipgloss.Color(bg)).
		Width(inputW).
		Height(inputContentHeight).
		Padding(0, 2).
		Render(inputWithPrompt)

	// Key hints with badge style
	keyBadge := func(k string) string {
		return lipgloss.NewStyle().
			Foreground(lipgloss.Color(th.BG)).
			Background(lipgloss.Color(th.Primary)).
			Bold(true).
			Padding(0, 1).
			Render(k)
	}
	hintLabel := func(l string) string {
		return lipgloss.NewStyle().
			Foreground(lipgloss.Color(th.TextDim)).
			Background(lipgloss.Color(bg)).
			Render(" " + l)
	}

	hints := bgLine(
		keyBadge("tab") + hintLabel(tr("classic.home.history")) + "  " +
			keyBadge("ctrl+u") + hintLabel(tr("classic.home.usage")) + "  " +
			keyBadge("ctrl+s") + hintLabel(tr("classic.home.settings")) + "  " +
			keyBadge("ctrl+`") + hintLabel(tr("classic.home.palette")))

	// ── Vertical centering (tight gaps) ──
	var mainLines []string

	titleH := 1
	subH := 1
	resumeH := 0
	if resumeLine != "" {
		resumeH = 2 // resume line + gap
	}
	// Use the actual content height instead of counting newlines in rendered output
	inputH := inputContentHeight
	hintsH := 1
	infoStripH := 0
	if showInfoStrip {
		infoStripH = 4 // empty + info + empty + hint
	}
	totalContentH := titleH + subH + 1 + resumeH + inputH + 1 + hintsH + infoStripH
	topPad := (height - totalContentH) / 2
	if topPad < 1 {
		topPad = 1
	}

	for i := 0; i < topPad; i++ {
		mainLines = append(mainLines, emptyLine)
	}

	// Title / animation area — always static (full-screen burn handled in viewHome).
	mainLines = append(mainLines, lipgloss.PlaceHorizontal(innerW, lipgloss.Center, title))
	mainLines = append(mainLines, lipgloss.PlaceHorizontal(innerW, lipgloss.Center, subtitle))
	mainLines = append(mainLines, emptyLine)

	// Session resumption prompt
	if resumeLine != "" {
		mainLines = append(mainLines, lipgloss.PlaceHorizontal(innerW, lipgloss.Center, resumeLine))
		mainLines = append(mainLines, emptyLine)
	}

	// Track input Y position for autocomplete overlay positioning
	// This is relative to renderTerminalMain — viewHome() adds tabH and sidebarW offsets
	a.homeInputOverlayY = len(mainLines)
	// X = center offset of input box within innerW (border adds 2 to inputW)
	inputBoxVisualW := inputW + 2
	a.homeInputOverlayX = (innerW - inputBoxVisualW) / 2
	if a.homeInputOverlayX < 0 {
		a.homeInputOverlayX = 0
	}

	mainLines = append(mainLines, lipgloss.PlaceHorizontal(innerW, lipgloss.Center, inputBox))
	mainLines = append(mainLines, emptyLine)
	mainLines = append(mainLines, lipgloss.PlaceHorizontal(innerW, lipgloss.Center, hints))

	// ── Info strip when sidebar is collapsed or hidden ──
	if showInfoStrip {
		mainLines = append(mainLines, emptyLine)

		mutedStyle := lipgloss.NewStyle().
			Foreground(lipgloss.Color(th.TextMuted)).
			Background(lipgloss.Color(bg))
		brightStyle := lipgloss.NewStyle()
		accentStyle := lipgloss.NewStyle().
			Foreground(lipgloss.Color(th.Accent)).
			Background(lipgloss.Color(bg))
		branchColor := lipgloss.NewStyle().
			Foreground(lipgloss.Color(th.Success)).
			Background(lipgloss.Color(bg))
		insColor := lipgloss.NewStyle().
			Foreground(lipgloss.Color(th.Success)).
			Background(lipgloss.Color(bg))
		delColor := lipgloss.NewStyle().
			Foreground(lipgloss.Color(th.Error)).
			Background(lipgloss.Color(bg))

		// Model · Branch ← parent · 20 files +1950 -725
		var parts []string

		// Model
		model := a.currentModelDisplay
		if model == "" {
			model = a.currentModel
		}
		if model != "" {
			parts = append(parts, brightStyle.Render(strings.ToUpper(model)))
		}

		// Branch
		branch := a.homeSidebarCache.currentBranch
		if branch != "" {
			bStr := branchColor.Render("⎇ " + branch)
			if parent := a.homeSidebarCache.branchParents[branch]; parent != "" {
				bStr += mutedStyle.Render(" ← " + parent)
			}
			parts = append(parts, bStr)
		}

		// Changes summary
		modFiles := a.homeSidebarCache.modifiedFiles
		if len(modFiles) > 0 {
			chgStr := accentStyle.Render(tr("classic.home.file_count_plain", len(modFiles)))
			if ins := a.homeSidebarCache.headIns; ins != "" {
				chgStr += " " + insColor.Render(ins)
			}
			if del := a.homeSidebarCache.headDel; del != "" {
				chgStr += " " + delColor.Render(del)
			}
			parts = append(parts, chgStr)
		}

		sep := mutedStyle.Render("  ·  ")
		infoLine := bgLine(strings.Join(parts, sep))
		mainLines = append(mainLines, lipgloss.PlaceHorizontal(innerW, lipgloss.Center, infoLine))

		// Hint to expand sidebar (extra blank line for spacing)
		mainLines = append(mainLines, emptyLine)
		expandHint := keyBadge("shift+tab") + hintLabel(tr("classic.home.details"))
		mainLines = append(mainLines, lipgloss.PlaceHorizontal(innerW, lipgloss.Center, bgLine(expandHint)))
	}

	content := strings.Join(mainLines, "\n")

	lineCount := strings.Count(content, "\n") + 1
	for lineCount < height {
		content += "\n" + emptyLine
		lineCount++
	}

	return lipgloss.NewStyle().
		Width(width).
		Height(height).
		Background(lipgloss.Color(bg)).
		Padding(0, 3).
		Render(content)
}

// focusHomeInput resets home screen to input-focused mode and re-starts the
// logo animation if the boot intro has already completed. Returns a tea.Cmd
// that must be returned from the caller's Update so the tick loop fires.
func (a *App) focusHomeInput() tea.Cmd {
	a.homeInputFocused = true
	if a.homeInput != nil {
		a.homeInput.Focus()
	}
	if a.animationClock != nil && !a.animationClock.IsActive() {
		a.pendingAnimCmd = a.animationClock.Subscribe()
	}
	// Replay the title burn animation each time the user returns to the home screen.
	return a.startHomeTitleAnim()
}

// getWorkingDir returns a shortened working directory path.
func (a *App) getWorkingDir() string {
	wd, err := os.Getwd()
	if err != nil {
		return ""
	}
	home, _ := os.UserHomeDir()
	if home != "" && strings.HasPrefix(wd, home) {
		return "~" + wd[len(home):]
	}
	return filepath.Base(wd)
}

// getRecentCommits fetches recent git commits from the current directory
func (a *App) getRecentCommits(limit int) []string {
	cmdStr := fmt.Sprintf("git log --oneline -n %d 2>/dev/null", limit)
	cmd := exec.Command("sh", "-c", cmdStr)
	out, err := cmd.Output()
	if err != nil {
		return nil
	}
	rawLines := strings.Split(strings.TrimSpace(string(out)), "\n")
	var commits []string
	for _, line := range rawLines {
		if line = strings.TrimSpace(line); line != "" {
			commits = append(commits, line)
		}
	}
	return commits
}

// getHeadDiffStat returns a compact summary like "3 files +45 -12"
// getHeadDiffStat returns (files, insertions, deletions) as separate strings.
func (a *App) getHeadDiffStat() (string, string, string) {
	cmd := exec.Command("sh", "-c", "git diff --shortstat 2>/dev/null")
	out, err := cmd.Output()
	if err != nil {
		return "", "", ""
	}
	raw := strings.TrimSpace(string(out))
	if raw == "" {
		return "", "", ""
	}
	// Parse "3 files changed, 45 insertions(+), 12 deletions(-)"
	var files, ins, del string
	parts := strings.SplitSeq(raw, ",")
	for p := range parts {
		p = strings.TrimSpace(p)
		fields := strings.Fields(p)
		if len(fields) >= 2 {
			if strings.Contains(p, "file") {
				files = fields[0]
			} else if strings.Contains(p, "insertion") {
				ins = "+" + fields[0]
			} else if strings.Contains(p, "deletion") {
				del = "-" + fields[0]
			}
		}
	}
	return files, ins, del
}

// getModifiedFiles returns a list of modified files with their status and diff stats.
func (a *App) getModifiedFiles(limit int) []modifiedFileEntry {
	// Get porcelain status for staged/unstaged info
	statusCmd := exec.Command("sh", "-c", "git status --porcelain 2>/dev/null")
	statusOut, err := statusCmd.Output()
	if err != nil {
		return nil
	}

	// Get numstat for unstaged changes
	numstatCmd := exec.Command("sh", "-c", "git diff --numstat 2>/dev/null")
	numstatOut, _ := numstatCmd.Output()

	// Get numstat for staged changes
	stagedCmd := exec.Command("sh", "-c", "git diff --cached --numstat 2>/dev/null")
	stagedOut, _ := stagedCmd.Output()

	// Build numstat lookup: path → (ins, del)
	type diffStat struct{ ins, del int }
	numstatMap := make(map[string]diffStat)
	for line := range strings.SplitSeq(strings.TrimSpace(string(numstatOut)), "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 3 {
			ins := 0
			del := 0
			if fields[0] != "-" {
				fmt.Sscanf(fields[0], "%d", &ins)
			}
			if fields[1] != "-" {
				fmt.Sscanf(fields[1], "%d", &del)
			}
			numstatMap[fields[2]] = diffStat{ins, del}
		}
	}
	// Merge staged numstat
	for line := range strings.SplitSeq(strings.TrimSpace(string(stagedOut)), "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 3 {
			ins := 0
			del := 0
			if fields[0] != "-" {
				fmt.Sscanf(fields[0], "%d", &ins)
			}
			if fields[1] != "-" {
				fmt.Sscanf(fields[1], "%d", &del)
			}
			existing := numstatMap[fields[2]]
			numstatMap[fields[2]] = diffStat{existing.ins + ins, existing.del + del}
		}
	}

	var entries []modifiedFileEntry
	for line := range strings.SplitSeq(strings.TrimSpace(string(statusOut)), "\n") {
		if len(line) < 4 {
			continue
		}
		indexStatus := line[0] // staged status
		workStatus := line[1]  // unstaged status
		filePath := strings.TrimSpace(line[3:])

		var status string
		staged := false

		switch {
		case indexStatus == '?' && workStatus == '?':
			status = "?"
		case indexStatus == 'A':
			status = "A"
			staged = true
		case indexStatus == 'D' || workStatus == 'D':
			status = "D"
			staged = indexStatus == 'D'
		case indexStatus == 'R':
			status = "R"
			staged = true
		case indexStatus == 'M' && workStatus == 'M':
			status = "M"
			staged = true // partially staged
		case indexStatus == 'M':
			status = "M"
			staged = true
		case workStatus == 'M':
			status = "M"
			staged = false
		default:
			status = "M"
		}

		stat := numstatMap[filePath]
		entries = append(entries, modifiedFileEntry{
			path:   filePath,
			status: status,
			staged: staged,
			ins:    stat.ins,
			del:    stat.del,
		})

		if len(entries) >= limit {
			break
		}
	}
	return entries
}

// getBranchParent tries to find the parent branch of a given branch
func (a *App) getBranchParent(branch string) string {
	// Use git log to find the branch point — check which well-known branch this diverged from
	bases := []string{"main", "master", "development", "develop", "staging"}
	for _, base := range bases {
		if branch == base {
			continue
		}
		// Check if base exists
		checkCmd := fmt.Sprintf("git rev-parse --verify %s 2>/dev/null", base)
		if err := exec.Command("sh", "-c", checkCmd).Run(); err != nil {
			continue
		}
		// Check if branch has commits ahead of base
		cmdStr := fmt.Sprintf("git log %s..%s --oneline 2>/dev/null | wc -l", base, branch)
		out, err := exec.Command("sh", "-c", cmdStr).Output()
		if err != nil {
			continue
		}
		count := strings.TrimSpace(string(out))
		if count != "" && count != "0" {
			return base
		}
	}
	return ""
}

// menuProgress returns 0.0-1.0 with ease-out curve
func (a *App) menuProgress() float64 {
	if a.menuReady {
		return 1.0
	}
	t := float64(a.menuFrame) / 30.0
	if t > 1.0 {
		t = 1.0
	}
	return 1.0 - (1.0-t)*(1.0-t)*(1.0-t)
}

// fade returns opacity 0.0-1.0 for a time window
func (a *App) fade(progress, startT, duration float64) float64 {
	if progress >= 1.0 {
		return 1.0
	}
	if duration <= 0 {
		if progress >= startT {
			return 1.0
		}
		return 0.0
	}
	if progress >= startT+duration {
		return 1.0
	}
	if progress < startT {
		return 0.0
	}
	return (progress - startT) / duration
}

// applyOpacity hides characters based on opacity (0=hidden, 1=visible)
func (a *App) applyOpacity(text string, opacity float64) string {
	if opacity >= 0.95 {
		return text
	}
	if opacity <= 0.05 {
		var b strings.Builder
		for _, r := range text {
			if r == '\n' {
				b.WriteRune('\n')
			} else {
				b.WriteRune(' ')
			}
		}
		return b.String()
	}
	lines := strings.Split(text, "\n")
	numLines := len(lines)
	revealLines := int(float64(numLines) * opacity)
	if revealLines < 1 && opacity > 0 {
		revealLines = 1
	}
	var result []string
	for i, line := range lines {
		if i < revealLines {
			result = append(result, line)
		} else {
			result = append(result, strings.Repeat(" ", lipgloss.Width(line)))
		}
	}
	return strings.Join(result, "\n")
}
