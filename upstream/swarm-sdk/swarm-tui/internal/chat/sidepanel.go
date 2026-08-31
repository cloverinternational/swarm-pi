package chat

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"charm.land/lipgloss/v2"
	hooksbuiltin "github.com/Swarm-Code/mono/swarm-sdk/internal/hooks/builtin"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/tools"
	toolbuiltin "github.com/Swarm-Code/mono/swarm-sdk/internal/tools/builtin"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/tools/ii"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/vault"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/bgprocess"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/i18n"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/version"
)

const (
	MinWidthForSidePanel = 80 // Minimum terminal width to show side panel (Inspection frames are 80 cols)
	SidePanelWidth       = 38 // Width of side panel (compact for smaller viewports)

	chatViewportHorizontalPadding = 4 // renderChatContent wraps messages with Padding(0, 2)
	chatInputHorizontalPadding    = 8 // input chrome + prompt overhead
	chatHeaderChromeHeight        = 2 // header + divider baseline used by resize/intro
	chatFooterChromeHeight        = 6 // input area + separators baseline used by resize/intro
	minChatViewportHeight         = 5
)

type chatAreaLayout struct {
	TerminalWidth int
	Height        int

	ShowSidePanel bool
	ChatWidth     int
	OverlayWidth  int

	ViewportWidth  int
	ViewportHeight int
	InputWidth     int
}

func computeChatAreaLayout(width, height int, sidePanelEnabled bool) chatAreaLayout {
	if width <= 0 {
		width = 80
	}
	if height <= 0 {
		height = 24
	}

	layout := chatAreaLayout{
		TerminalWidth: width,
		Height:        height,
		ChatWidth:     width,
		OverlayWidth:  width,
	}
	layout.ShowSidePanel = sidePanelEnabled && width >= MinWidthForSidePanel
	if layout.ShowSidePanel {
		layout.ChatWidth = width - SidePanelWidth
		layout.OverlayWidth = layout.ChatWidth
	}
	if layout.ChatWidth < 1 {
		layout.ChatWidth = 1
	}
	if layout.OverlayWidth < 1 {
		layout.OverlayWidth = 1
	}

	layout.ViewportWidth = layout.ChatWidth - chatViewportHorizontalPadding
	if layout.ViewportWidth < 1 {
		layout.ViewportWidth = 1
	}
	layout.InputWidth = layout.ChatWidth - chatInputHorizontalPadding
	if layout.InputWidth < 1 {
		layout.InputWidth = 1
	}
	layout.ViewportHeight = height - chatHeaderChromeHeight - chatFooterChromeHeight
	if layout.ViewportHeight < minChatViewportHeight {
		layout.ViewportHeight = minChatViewportHeight
	}

	return layout
}

func (a *App) chatAreaLayout() chatAreaLayout {
	return computeChatAreaLayout(a.width, a.height, a.showSidePanel)
}

func (a *App) syncSidePanelPreference() bool {
	if a.settingsManager == nil {
		return false
	}
	general := a.settingsManager.GetGeneralSettings()
	if general == nil {
		return false
	}
	desired := general.GetSidePanelEnabled()
	if a.showSidePanel == desired {
		return false
	}
	a.showSidePanel = desired
	a.sidePanelCache.valid = false
	return true
}

func (a *App) applyChatAreaLayout(invalidate bool) chatAreaLayout {
	layout := a.chatAreaLayout()
	layoutChanged := false

	if a.msgViewport != nil {
		layoutChanged = a.msgViewport.Width != layout.ViewportWidth
		// IMPORTANT: only apply the WIDTH here, not layout.ViewportHeight.
		// layout.ViewportHeight comes from computeChatAreaLayout()'s fixed
		// chatFooterChromeHeight approximation, which does not know the true
		// rendered height of the input box, task panel, bash dock, or any modal
		// bar. renderChatContent() always computes the real, measured height and
		// calls msgViewport.SetSize() with it immediately before View() renders —
		// that call is the single source of truth for Height. Setting an
		// approximate Height here and then eagerly re-rendering content against
		// it (callers of applyChatAreaLayout commonly follow up with
		// updateViewportContent()) previously clamped/restored scroll position
		// using the WRONG maxYOffset on every resize/side-panel-toggle for users
		// who were scrolled away from the bottom — a real, reproducible jump.
		// Width, unlike height, does need to be current here because re-wrapping
		// depends on width and several callers act on wrapped content
		// immediately after this call returns.
		if layoutChanged {
			a.msgViewport.SetSize(layout.ViewportWidth, a.msgViewport.Height)
		}
	}
	if a.textInput != nil {
		a.textInput.SetWidth(layout.InputWidth)
	}
	if a.screen != ScreenHome {
		if a.cmdAutocomplete != nil {
			a.cmdAutocomplete.SetSize(layout.ViewportWidth, layout.Height/3)
		}
		if a.mentionAutocomplete != nil {
			a.mentionAutocomplete.SetSize(layout.ViewportWidth, layout.Height/3)
		}
	}
	if a.subAgentRenderer != nil {
		a.subAgentRenderer.SetWidth(layout.ViewportWidth)
	}
	if a.planViewer != nil {
		planHeight := layout.ViewportHeight
		const barHeight = 3
		if planHeight > barHeight+minChatViewportHeight {
			planHeight -= barHeight
		}
		a.planViewer.SetSize(layout.ViewportWidth, planHeight)
	}
	if a.detailViewer != nil {
		a.detailViewer.SetSize(layout.ChatWidth, layout.Height)
	}

	if invalidate && layoutChanged {
		// Width-aware invalidation: a side-panel toggle flips between exactly
		// two viewport widths, so messages already wrapped at the target width
		// are reused instead of re-rendered.
		a.invalidateViewportCacheForWidth(layout.ViewportWidth)
		a.viewNeedsRefresh = true
	}
	return layout
}

// SidePanel represents the right-side information panel
type SidePanel struct {
	width  int
	height int
}

// NewSidePanel creates a new side panel
func NewSidePanel(width, height int) *SidePanel {
	return &SidePanel{
		width:  width,
		height: height,
	}
}

// ShouldShow returns true if terminal is wide enough for side panel
func (s *SidePanel) ShouldShow(terminalWidth int) bool {
	return terminalWidth >= MinWidthForSidePanel
}

// Render renders the side panel
func (s *SidePanel) Render(app *App) string {
	// === CACHE CHECK ===
	// Get current state for cache validation
	var toolCount int
	var bgAgentCount int
	var bgAgentDigest string
	var bgProcessCount int
	var cachingOn bool
	permissionLevel := tools.LevelBalanced
	if app.sdk != nil {
		toolCount = len(app.sdk.GetToolNames())
		bgAgents := app.sdk.GetBackgroundAgents()
		bgAgentCount = len(bgAgents)
		bgAgentDigest = backgroundAgentsDigest(bgAgents)
		cachingOn = app.sdk.IsCachingEnabled()
	}
	if app.sdk != nil {
		bgProcessCount = app.sdk.GetActiveBackgroundProcessCount()
	}
	if app.permissionConfig != nil {
		permissionLevel = normalizePermissionLevel(app.permissionConfig.Config().Level)
	}
	vaultUnlocked := vault.GetDefaultVaultProvider().IsEnabled()

	// Check if cache is valid
	cache := &app.sidePanelCache

	// Get current micro-compaction state for cache validation
	microCompactionOn := false
	if app.configBundle != nil {
		if cfg := app.configBundle.GetConfig(); cfg != nil {
			microCompactionOn = cfg.EnableMicroCompaction
		}
	}

	// Get current todo count for cache validation
	todoCount := 0
	if todoMgr := ii.GetTodoManager(); todoMgr != nil {
		todoCount = todoMgr.Count()
	}

	// Get auto-mode state for cache validation
	autoModeOn := false
	if app.sdk != nil {
		if hm := app.sdk.GetHooksManager(); hm != nil {
			if h := hm.GetAutoModeHook(); h != nil {
				cfg := h.GetConfig()
				autoModeOn = cfg.SkipAutoPermissionPrompt && h.IsEnabled()
			}
		}
	}

	// Get usage time for cache validation
	var usageTime time.Time
	if app.usageResult != nil {
		usageTime = app.usageResult.FetchedAt
	}

	// Get budget state for cache validation
	var budgetEnabled bool
	var budgetToolCalls int
	var budgetSkilled bool
	var budgetNudgeIgnores int
	if app.sdk != nil {
		if hm := app.sdk.GetHooksManager(); hm != nil {
			snap := hm.GetBudgetSnapshot()
			if snap.OnboardingBudget > 0 || snap.WorkingBudget > 0 {
				budgetEnabled = true
				budgetToolCalls = snap.ToolCalls
				budgetSkilled = snap.Skilled
				budgetNudgeIgnores = snap.NudgeIgnores
			}
		}
	}

	// Get /goal state for cache validation and the GOAL section.
	var goalState, goalCondition, goalReason string
	var goalIterations int
	if app.sdk != nil {
		if hm := app.sdk.GetHooksManager(); hm != nil {
			if gh := hm.GetGoalHook(); gh != nil {
				if g := gh.GetGoal(); g != nil && g.State != hooksbuiltin.GoalStateCleared {
					goalState = g.State
					goalCondition = g.Condition
					goalReason = g.LastReason
					goalIterations = g.Iterations
				}
			}
		}
	}

	// Get TPS state for cache validation. Rounded to whole tok/s so EMA jitter
	// doesn't thrash the render cache every frame.
	var tpsCurRounded, tpsAvgRounded, tpsPeakRounded float64
	var tpsStreaming bool
	var tpsAllTime float64
	if app.metrics != nil && app.metrics.TPS != nil {
		cur, peak, avg := app.metrics.TPS.GetMetrics()
		tpsCurRounded = math.Round(cur)
		tpsAvgRounded = math.Round(avg)
		tpsPeakRounded = math.Round(peak)
		tpsStreaming = app.metrics.TPS.IsStreaming()
		tpsAllTime, _ = app.metrics.GetModelHistoricalTPS(app.currentModel)
	}

	// Check if any background processes are actively running (their elapsed time changes each frame)
	hasRunningBgProcs := false
	if app.sdk != nil {
		for _, proc := range app.sdk.GetBackgroundProcesses() {
			if proc.State == bgprocess.StateRunning {
				foregroundID := app.sdk.GetCurrentForegroundProcessID()
				if proc.Handle.ID() != foregroundID || foregroundID == "" {
					hasRunningBgProcs = true
					break
				}
			}
		}
	}

	sidePanelRenderCalls.Add(1)
	// Build BASH rows once (with live tails for running commands) and a digest so
	// the render cache re-runs whenever a status, exit code, or output tail changes
	// even while processes keep running (the elapsed-only fast path would miss it).
	var bashRows []bashRow
	var bashDigest string
	if app.sdk != nil {
		procs := app.sdk.GetSidePanelBashProcesses()
		if len(procs) > 0 {
			bashRows = buildBashRows(procs, app.sdk.GetBashProcessTail, time.Now())
			bashDigest = bashRowsDigest(bashRows)
		}
	}
	if app.bashPanelFocused {
		app.bashSelectedIdx, app.bashSelectedID = bashResolveSelection(bashRows, app.bashSelectedIdx, app.bashSelectedID)
	}

	if cache.valid && !hasRunningBgProcs && cache.vaultUnlocked == vaultUnlocked &&
		cache.tokenCount == app.tokenCount &&
		cache.mode == app.operatingMode &&
		cache.permissionLevel == string(permissionLevel) &&
		cache.width == s.width &&
		cache.height == s.height &&
		cache.toolCount == toolCount &&
		cache.bgAgentCount == bgAgentCount &&
		cache.bgAgentDigest == bgAgentDigest &&
		cache.bgProcessCount == bgProcessCount &&
		cache.showThinking == app.showThinking &&
		cache.showVerbose == app.showFullToolOutput &&
		cache.cachingOn == cachingOn &&
		cache.microCompactionOn == microCompactionOn &&
		cache.autoModeOn == autoModeOn &&
		cache.currentModel == app.currentModel &&
		cache.currentProvider == app.currentProvider &&
		cache.currentModelDisplay == app.currentModelDisplay &&
		cache.currentProviderDisplay == app.currentProviderDisplay &&
		cache.usageFetchedAt == usageTime &&
		cache.todoCount == todoCount &&
		cache.budgetToolCalls == budgetToolCalls &&
		cache.budgetSkilled == budgetSkilled &&
		cache.budgetNudgeIgnores == budgetNudgeIgnores &&
		cache.budgetEnabled == budgetEnabled &&
		cache.tpsCurrentValue == tpsCurRounded &&
		cache.tpsAvgValue == tpsAvgRounded &&
		cache.tpsPeakValue == tpsPeakRounded &&
		cache.tpsStreaming == tpsStreaming &&
		cache.goalState == goalState &&
		cache.goalIterations == goalIterations &&
		cache.goalReason == goalReason &&
		cache.bashDigest == bashDigest {
		// Cache hit - return cached render
		sidePanelCacheHits.Add(1)
		return cache.rendered
	}

	// When background processes are running, we can return cached content but need to
	// verify it's still valid aside from elapsed time. Only re-render if something structural changed.
	if cache.valid && hasRunningBgProcs && cache.vaultUnlocked == vaultUnlocked &&
		cache.tokenCount == app.tokenCount &&
		cache.mode == app.operatingMode &&
		cache.permissionLevel == string(permissionLevel) &&
		cache.width == s.width &&
		cache.height == s.height &&
		cache.toolCount == toolCount &&
		cache.bgAgentCount == bgAgentCount &&
		cache.bgAgentDigest == bgAgentDigest &&
		cache.bgProcessCount == bgProcessCount &&
		cache.showThinking == app.showThinking &&
		cache.showVerbose == app.showFullToolOutput &&
		cache.cachingOn == cachingOn &&
		cache.microCompactionOn == microCompactionOn &&
		cache.autoModeOn == autoModeOn &&
		cache.currentModel == app.currentModel &&
		cache.currentProvider == app.currentProvider &&
		cache.currentModelDisplay == app.currentModelDisplay &&
		cache.currentProviderDisplay == app.currentProviderDisplay &&
		cache.usageFetchedAt == usageTime &&
		cache.todoCount == todoCount &&
		cache.budgetToolCalls == budgetToolCalls &&
		cache.budgetSkilled == budgetSkilled &&
		cache.budgetNudgeIgnores == budgetNudgeIgnores &&
		cache.budgetEnabled == budgetEnabled &&
		cache.tpsCurrentValue == tpsCurRounded &&
		cache.tpsAvgValue == tpsAvgRounded &&
		cache.tpsPeakValue == tpsPeakRounded &&
		cache.tpsStreaming == tpsStreaming &&
		cache.goalState == goalState &&
		cache.goalIterations == goalIterations &&
		cache.goalReason == goalReason &&
		cache.bashDigest == bashDigest {
		// Cache hit with running processes - elapsed times will be stale, but we avoid full re-render
		sidePanelCacheHits.Add(1)
		return cache.rendered
	}

	// Cache miss - detect which field changed (for flicker diagnostics)
	sidePanelCacheMissCount.Add(1)
	reason := "unknown"
	if !cache.valid {
		reason = "invalid"
	} else if cache.tokenCount != app.tokenCount {
		reason = fmt.Sprintf("token_count(%d→%d)", cache.tokenCount, app.tokenCount)
	} else if cache.mode != app.operatingMode {
		reason = fmt.Sprintf("mode(%s→%s)", cache.mode, app.operatingMode)
	} else if cache.permissionLevel != string(permissionLevel) {
		reason = fmt.Sprintf("permission_level(%s→%s)", cache.permissionLevel, permissionLevel)
	} else if cache.width != s.width {
		reason = fmt.Sprintf("width(%d→%d)", cache.width, s.width)
	} else if cache.height != s.height {
		reason = fmt.Sprintf("height(%d→%d)", cache.height, s.height)
	} else if cache.toolCount != toolCount {
		reason = fmt.Sprintf("tool_count(%d→%d)", cache.toolCount, toolCount)
	} else if cache.bgAgentCount != bgAgentCount {
		reason = fmt.Sprintf("bg_agent_count(%d→%d)", cache.bgAgentCount, bgAgentCount)
	} else if cache.bgAgentDigest != bgAgentDigest {
		reason = "bg_agent_state_changed"
	} else if cache.bgProcessCount != bgProcessCount {
		reason = fmt.Sprintf("bg_process_count(%d→%d)", cache.bgProcessCount, bgProcessCount)
	} else if cache.showThinking != app.showThinking {
		reason = fmt.Sprintf("show_thinking(%v→%v)", cache.showThinking, app.showThinking)
	} else if cache.showVerbose != app.showFullToolOutput {
		reason = fmt.Sprintf("show_verbose(%v→%v)", cache.showVerbose, app.showFullToolOutput)
	} else if cache.cachingOn != cachingOn {
		reason = fmt.Sprintf("caching_on(%v→%v)", cache.cachingOn, cachingOn)
	} else if cache.microCompactionOn != microCompactionOn {
		reason = fmt.Sprintf("micro_compaction(%v→%v)", cache.microCompactionOn, microCompactionOn)
	} else if cache.autoModeOn != autoModeOn {
		reason = fmt.Sprintf("auto_mode_on(%v→%v)", cache.autoModeOn, autoModeOn)
	} else if cache.currentModel != app.currentModel {
		reason = fmt.Sprintf("current_model(%s→%s)", cache.currentModel, app.currentModel)
	} else if cache.currentProvider != app.currentProvider {
		reason = fmt.Sprintf("current_provider(%s→%s)", cache.currentProvider, app.currentProvider)
	} else if cache.currentModelDisplay != app.currentModelDisplay {
		reason = fmt.Sprintf("current_model_display(%s→%s)", cache.currentModelDisplay, app.currentModelDisplay)
	} else if cache.currentProviderDisplay != app.currentProviderDisplay {
		reason = fmt.Sprintf("current_provider_display(%s→%s)", cache.currentProviderDisplay, app.currentProviderDisplay)
	} else if cache.usageFetchedAt != usageTime {
		reason = "usage_fetched_at(miss)"
	} else if cache.todoCount != todoCount {
		reason = fmt.Sprintf("todo_count(%d→%d)", cache.todoCount, todoCount)
	} else if cache.budgetToolCalls != budgetToolCalls {
		reason = fmt.Sprintf("budget_tool_calls(%d→%d)", cache.budgetToolCalls, budgetToolCalls)
	} else if cache.budgetSkilled != budgetSkilled {
		reason = fmt.Sprintf("budget_skilled(%v→%v)", cache.budgetSkilled, budgetSkilled)
	} else if cache.budgetNudgeIgnores != budgetNudgeIgnores {
		reason = fmt.Sprintf("budget_nudge_ignores(%d→%d)", cache.budgetNudgeIgnores, budgetNudgeIgnores)
	} else if cache.budgetEnabled != budgetEnabled {
		reason = fmt.Sprintf("budget_enabled(%v→%v)", cache.budgetEnabled, budgetEnabled)
	}
	sidePanelLastMissReason.Set(reason)
	// Cache miss reason is tracked in sidePanelLastMissReason metric
	// for observability without corrupting TUI output

	// === FULL RENDER (cache miss) ===
	th := app.theme
	innerW := s.width - 4 // usable text width: border(1) + padding(1) each side

	// ── Style helpers (closures avoid repeated style construction) ─────
	dim := func(t string) string {
		return lipgloss.NewStyle().Foreground(lipgloss.Color(th.TextDim)).Render(t)
	}
	muted := func(t string) string {
		return lipgloss.NewStyle().Foreground(lipgloss.Color(th.TextMuted)).Render(t)
	}
	clr := func(t, c string) string {
		return lipgloss.NewStyle().Foreground(lipgloss.Color(c)).Render(t)
	}
	// sectionLbl: all section labels look identical — Gestalt Similarity
	sectionLbl := func(t string) string {
		return lipgloss.NewStyle().Foreground(lipgloss.Color(th.TextDim)).Render(strings.ToUpper(t))
	}

	divLine := lipgloss.NewStyle().
		Foreground(lipgloss.Color(th.Border)).
		Render(strings.Repeat("─", innerW))

	var lines []string
	add := func(ln string) { lines = append(lines, ln) }
	gap := func() { lines = append(lines, "") }
	rule := func() { lines = append(lines, divLine) }

	// ── ZONE 1: IDENTITY ──────────────────────────────────────────────
	// Header: brand + version on one line — no wasted row for just a label
	versionLine := lipgloss.NewStyle().Foreground(lipgloss.Color(th.Primary)).Bold(true).Render("SWARM") +
		dim("  "+version.Version)

	// Add update indicator if available
	if app.updateAvailable != nil && app.updateAvailable.UpdateAvailable {
		updateStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(th.Success))
		versionLine += " " + updateStyle.Render("↻ "+app.updateAvailable.LatestVersion)
	} else if app.updateDownloading {
		updateStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(th.Accent))
		versionLine += " " + updateStyle.Render(i18n.T("chat_b.side.updating"))
	}

	vaultLabel := i18n.T("chat_b.side.vault_locked")
	vaultColor := th.TextMuted
	if vaultUnlocked {
		vaultLabel = i18n.T("chat_b.side.vault_unlocked")
		vaultColor = th.Success
	}
	vaultChip := lipgloss.NewStyle().
		Foreground(lipgloss.Color(vaultColor)).
		Render(vaultLabel)
	app.vaultChipWidth = lipgloss.Width(vaultChip)
	if gapWidth := innerW - lipgloss.Width(versionLine) - app.vaultChipWidth; gapWidth > 0 {
		versionLine += strings.Repeat(" ", gapWidth) + vaultChip
	} else {
		versionLine += " " + vaultChip
	}

	add(versionLine)
	rule()

	// Model — most-glanced datum, no "Config:" preamble
	provider := app.currentProvider
	if provider == "" {
		provider = extractProviderFromModel(app.currentModel)
	}
	providerColor := getProviderColor(app.providerConfigs, provider)
	providerIcon := getProviderIcon(provider)
	modelShort := getModelShortName(app.currentModel)
	if modelShort == "" {
		modelShort = i18n.T("chat_b.side.none")
	}
	provText := app.currentProviderDisplay
	if provText == "" {
		provText = provider
	}
	if provText == "" {
		provText = i18n.T("chat_b.side.no_api")
	}
	maxProvLen := innerW - len(modelShort) - 5
	if maxProvLen < 4 {
		maxProvLen = 4
	}
	provText = truncateStr(provText, maxProvLen)
	add(lipgloss.NewStyle().Foreground(lipgloss.Color(providerColor)).Render(providerIcon) + " " +
		lipgloss.NewStyle().Foreground(lipgloss.Color(th.Primary)).Bold(true).Render(strings.ToUpper(modelShort)) +
		muted("  "+provText))

	// Current directory — shows where the session is operating
	workDir := ""
	if app.sdk != nil {
		workDir = app.sdk.WorkspaceRoot()
	}
	if workDir == "" {
		if cwd, err := os.Getwd(); err == nil {
			workDir = cwd
		}
	}
	if workDir != "" {
		// Show just the last directory name or a shortened path
		displayDir := filepath.Base(workDir)
		if displayDir == "." || displayDir == "/" {
			displayDir = workDir
		}
		displayDir = truncateStr(displayDir, innerW-2)
		add(clr("◈ ", th.Accent) + muted(displayDir))
	}

	// ── ZONE 2: RESOURCES ─────────────────────────────────────────────
	gap()

	inputTokens := app.tokenCount
	maxTokens := app.modelContextWindow

	if maxTokens > 0 {
		pct := float64(inputTokens) / float64(maxTokens) * 100

		var barColor string
		switch {
		case pct > 80:
			barColor = th.Error
		case pct > 60:
			barColor = th.Warning
		case pct > 20:
			barColor = th.Success
		default:
			barColor = th.TextDim
		}

		// Label + inline stats + coloured percentage — one line, Figure/Ground contrast
		tokDisplay := formatTokenCount(inputTokens)
		if app.tokenCountIsEstimate {
			tokDisplay = "~" + tokDisplay
		}
		pctStr := fmt.Sprintf("%.0f%%", pct)
		add(sectionLbl(i18n.T("residual.side.context")) +
			muted("  "+tokDisplay+" / "+formatTokenCount(maxTokens)+"  ") +
			lipgloss.NewStyle().Foreground(lipgloss.Color(barColor)).Render(pctStr))

		// Progress bar — full inner width, reads left→right as context fills (Gestalt Continuation)
		barW := innerW
		filled := int(float64(barW) * pct / 100)
		if filled > barW {
			filled = barW
		}
		if filled < 0 {
			filled = 0
		}
		var bar string
		if filled > 0 {
			bar = lipgloss.NewStyle().Foreground(lipgloss.Color(barColor)).Render(strings.Repeat("█", filled)) +
				lipgloss.NewStyle().Foreground(lipgloss.Color(th.Border)).Render(strings.Repeat("░", barW-filled))
		} else {
			bar = lipgloss.NewStyle().Foreground(lipgloss.Color(th.Border)).Render(strings.Repeat("░", barW))
		}
		add(bar)

		// Compaction warning — only when it actually matters
		if app.configBundle != nil {
			if cfg := app.configBundle.GetConfig(); cfg != nil && cfg.EnableCompaction {
				thPct := float64(cfg.CompactionThreshold) / 100.0
				if thPct <= 0 {
					thPct = 0.8
				}
				thTokens := int(float64(maxTokens) * thPct)
				if inputTokens >= thTokens {
					add(lipgloss.NewStyle().
						Foreground(lipgloss.Color("#FFD700")).
						Bold(true).
						Render(i18n.T("chat_b.side.next_message_compacts")))
				} else if pct/100 >= thPct-0.15 {
					left := formatTokenCount(thTokens - inputTokens)
					add(muted(left + i18n.T("chat_b.side.to_compact", thPct*100)))
				}
			}
		}
	} else {
		// Context window not yet known from provider API
		tokDisplay := formatTokenCount(inputTokens)
		if app.tokenCountIsEstimate {
			tokDisplay = "~" + tokDisplay
		}
		add(sectionLbl(i18n.T("residual.side.context")) + muted("  "+tokDisplay))
		add(dim(i18n.T("residual.side.context_loading")))
	}

	// Speed — tokens/sec. Only while actively streaming (live green readout);
	// the idle summary was low-signal clutter on a resting panel.
	if tpsStreaming {
		if tpsLine := formatTPSLine(tpsCurRounded, tpsAvgRounded, tpsPeakRounded, tpsAllTime, tpsStreaming); tpsLine != "" {
			add(sectionLbl(i18n.T("residual.side.speed")) + "  " + clr(tpsLine, th.Success))
		}
	}

	// Non-default feature flags — one compact line instead of a labelled
	// section per flag (these are modes, not resources). Defaults stay silent.
	var featureFlags []string
	if app.configBundle != nil {
		if cfg := app.configBundle.GetConfig(); cfg != nil {
			if cfg.EnableCodeMode {
				featureFlags = append(featureFlags, i18n.T("residual.side.code_mode"))
			}
			if cfg.MemoryBackend == "palace" {
				featureFlags = append(featureFlags, i18n.T("residual.side.palace_memory"))
			}
		}
	}
	if len(featureFlags) > 0 {
		gap()
		add(clr("▸ ", th.TextMuted) + dim(strings.Join(featureFlags, " · ")))
	}

	// Auto-mode indicator — only surfaced when actually ON; an OFF/disabled
	// row just restates the default and wastes a line.
	if autoModeOn {
		gap()
		add(clr("● ", th.Success) + sectionLbl(i18n.T("residual.side.auto_mode")) + muted("  "+i18n.T("residual.side.on")))
	}

	// Active /goal — condition, loop iteration, and last evaluator verdict.
	if goalState != "" {
		gap()
		var stateLbl, stateColor string
		switch goalState {
		case hooksbuiltin.GoalStateMet:
			stateLbl, stateColor = i18n.T("chat_b.side.goal_met"), th.Success
		case hooksbuiltin.GoalStateImpossible:
			stateLbl, stateColor = i18n.T("chat_b.side.goal_impossible"), th.Error
		case hooksbuiltin.GoalStateFailed:
			stateLbl, stateColor = i18n.T("chat_b.side.goal_failed"), th.Error
		case hooksbuiltin.GoalStateActive:
			stateLbl, stateColor = i18n.T("chat_b.side.goal_iteration", goalIterations), th.Warning
		default: // not_yet_evaluated
			stateLbl, stateColor = i18n.T("chat_b.side.goal_evaluating"), th.TextDim
		}
		add(sectionLbl(i18n.T("residual.side.goal")) + "  " + clr(stateLbl, stateColor))
		add(muted(truncateStr(goalCondition, innerW)))
		if goalReason != "" {
			add(dim(truncateStr("└ "+goalReason, innerW)))
		}
	}

	// ── Skill Budget ──────────────────────────────────────────
	if app.sdk != nil {
		hm := app.sdk.GetHooksManager()
		if hm != nil {
			snap := hm.GetBudgetSnapshot()
			if snap.OnboardingBudget > 0 || snap.WorkingBudget > 0 {
				gap()
				var budgetType string
				var budget uint
				var used int
				if snap.Skilled {
					budgetType = i18n.T("chat_b.side.budget_working")
					budget = snap.WorkingBudget
					used = snap.ToolCalls
				} else {
					budgetType = i18n.T("chat_b.side.budget_onboarding")
					budget = snap.OnboardingBudget
					used = snap.ToolCalls
				}

				pct := 0
				if budget > 0 {
					pct = used * 100 / int(budget)
					if pct > 100 {
						pct = 100
					}
				}

				var barColor string
				switch {
				case pct > 80:
					barColor = th.Error
				case pct > 60:
					barColor = th.Warning
				case pct > 20:
					barColor = th.Success
				default:
					barColor = th.TextDim
				}

				add(sectionLbl(i18n.T("residual.side.skill_budget")) +
					muted(fmt.Sprintf("  %s  %d/%d", budgetType, used, budget)) +
					"  " +
					lipgloss.NewStyle().Foreground(lipgloss.Color(barColor)).Render(fmt.Sprintf("%d%%", pct)))

				// Progress bar — same style as context token bar
				barW := innerW
				filled := pct * barW / 100
				if filled > barW {
					filled = barW
				}
				if filled < 0 {
					filled = 0
				}
				var bar string
				if filled > 0 {
					bar = lipgloss.NewStyle().Foreground(lipgloss.Color(barColor)).Render(strings.Repeat("█", filled)) +
						lipgloss.NewStyle().Foreground(lipgloss.Color(th.Border)).Render(strings.Repeat("░", barW-filled))
				} else {
					bar = lipgloss.NewStyle().Foreground(lipgloss.Color(th.Border)).Render(strings.Repeat("░", barW))
				}
				add(bar)

				if snap.NudgeIgnores > 0 {
					add(lipgloss.NewStyle().
						Foreground(lipgloss.Color("#FFD700")).
						Bold(true).
						Render(i18n.T("chat_b.side.nudges_ignored", snap.NudgeIgnores)))
				}
			}
		}
	}

	// Cache — one compact line. The hit percentage already says everything a
	// second progress bar would; the CONTEXT bar stays the panel's only big bar.
	if app.sdk != nil {
		cachingEnabled := app.sdk.IsCachingEnabled()
		gap()
		if cachingEnabled {
			cacheTTL := app.sdk.GetCacheTTL()
			hitRate := app.sdk.GetCacheHitRate()
			cacheMetrics := app.sdk.GetCacheMetrics()
			totalCreation, totalRead := app.sdk.GetTotalCacheStats()
			hasRead := cacheMetrics["cache_read_tokens"] > 0

			fmtTok := func(t int) string {
				if t >= 1000000 {
					return fmt.Sprintf("%.1fM", float64(t)/1e6)
				}
				if t >= 1000 {
					return fmt.Sprintf("%.0fk", float64(t)/1000)
				}
				return fmt.Sprintf("%d", t)
			}

			switch {
			case hasRead:
				line := i18n.T("residual.side.cache_hits", cacheTTL, hitRate)
				if totalRead > 0 {
					line += i18n.T("residual.side.cache_reused", fmtTok(totalRead))
				}
				add(clr("✓ ", th.Success) + clr(line, th.Success))
			case totalCreation > 0:
				add(clr("↺ ", th.Warning) + clr(i18n.T("residual.side.cache_warming", cacheTTL, fmtTok(totalCreation)), th.Warning))
			default:
				add(clr("◆ ", th.TextMuted) + dim(i18n.T("residual.side.cache_ready", cacheTTL)))
			}
		} else {
			add(clr("◆ ", th.TextMuted) + dim(i18n.T("residual.side.cache_disabled")))
		}
		// Token profiling - show recent changes and bounce detection.
		// Instrumentation, not user info — debug mode only.
		if app.debugMode && len(app.tokenChanges) > 0 {
			add("") // blank line
			// Count bounces
			bounces := 0
			for _, e := range app.tokenChanges {
				if e.IsBounce {
					bounces++
				}
			}
			// Show summary
			last := app.tokenChanges[len(app.tokenChanges)-1]
			direction := "↑"
			dColor := th.Success
			if last.Delta < 0 {
				direction = "↓"
				dColor = th.Warning
			}
			summaryText := i18n.T("residual.side.token_change", direction, absInt(last.Delta), last.Source)
			add(clr(summaryText, dColor))
			if bounces > 0 {
				add(clr(i18n.T("residual.side.bounces_detected", bounces), th.Warning))
			}
			// Show last few changes if there are bounces
			if bounces > 0 && len(app.tokenChanges) >= 2 {
				// Show the bounce event
				for _, e := range app.tokenChanges {
					if e.IsBounce {
						add(dim(fmt.Sprintf("  %d	%d via %s", e.PrevCount, e.NewCount, e.Source)))
						break
					}
				}
			}
		}
	}

	// ── ZONE 3: CONTROLS ──────────────────────────────────────────────
	// Interior zones are separated by a single blank line, not a full-width
	// rule — the UPPERCASE group labels already signal the boundary, so rules
	// are reserved for the identity header and the bottom hint (calmer, fewer
	// horizontal lines).
	gap()

	// Permissions badge — show "AUTO/YOLO" when AUTO mode is managing permissions,
	// so it's obvious the level was set automatically rather than by the user.
	permLbl := strings.ToUpper(permissionLevelLabel(permissionLevel))
	if app.operatingMode == "auto" && permissionLevel == tools.LevelYOLO {
		permLbl = i18n.T("residual.side.auto_yolo")
	}
	permColor := permissionLevelColor(permissionLevel, th)
	permBadge := lipgloss.NewStyle().
		Foreground(lipgloss.Color(th.Text)).
		Background(lipgloss.Color(permColor)).
		Padding(0, 1).
		Bold(true).
		Render(permLbl)

	// Mode badge
	var modeColor, modeLbl string
	switch app.operatingMode {
	case "off", "":
		modeColor = th.TextDim
		modeLbl = i18n.T("residual.side.mode_off")
	case "plan":
		modeColor = th.Warning
		modeLbl = i18n.T("residual.side.mode_plan")
	case "auto":
		modeColor = th.Error
		modeLbl = i18n.T("residual.side.mode_auto")
	case "debug":
		modeColor = th.Secondary
		modeLbl = i18n.T("residual.side.mode_debug")
	case "act":
		modeColor = th.Success
		modeLbl = i18n.T("residual.side.mode_act")
	default:
		modeColor = th.TextDim
		modeLbl = i18n.T("residual.side.mode_off")
	}
	modeBadge := lipgloss.NewStyle().
		Foreground(lipgloss.Color(th.Text)).
		Background(lipgloss.Color(modeColor)).
		Padding(0, 1).
		Bold(true).
		Render(modeLbl)

	// Pack control state and active modifiers into the available width. Keeping
	// each chip intact makes the row denser without sacrificing scanability;
	// uncommon long combinations wrap as whole chips onto the next line.
	controlChips := []string{permBadge, modeBadge}
	if app.showThinking {
		controlChips = append(controlChips, lipgloss.NewStyle().
			Foreground(lipgloss.Color(th.Text)).
			Background(lipgloss.Color(th.Accent)).
			Padding(0, 1).Render(i18n.T("chat_b.side.think")))
	}
	if app.showFullToolOutput {
		controlChips = append(controlChips, lipgloss.NewStyle().
			Foreground(lipgloss.Color(th.Text)).
			Background(lipgloss.Color(th.BGLighter)).
			Padding(0, 1).Render(i18n.T("chat_b.side.verbose")))
	}
	// Plan mode runtime indicators
	if app.planQuestionModal != nil {
		controlChips = append(controlChips, lipgloss.NewStyle().
			Foreground(lipgloss.Color("#000000")).
			Background(lipgloss.Color("#00BCD4")).
			Padding(0, 1).Bold(true).Render(i18n.T("chat_b.side.review_plan")))
	} else if app.inPlanMode {
		controlChips = append(controlChips, lipgloss.NewStyle().
			Foreground(lipgloss.Color("#000000")).
			Background(lipgloss.Color("#00BCD4")).
			Padding(0, 1).Render(i18n.T("chat_b.side.planning")))
	}
	if app.dreamRunning {
		controlChips = append(controlChips, lipgloss.NewStyle().
			Foreground(lipgloss.Color(th.Text)).
			Background(lipgloss.Color(th.Accent)).
			Padding(0, 1).Render(i18n.T("chat_b.side.dreaming")))
	}
	// Steering-hook indicator — reflects live Ctrl+S toggle state. Shown only
	// when steering hooks are actually registered, so users who never wire
	// steering don't see a misleading "OFF" badge.
	if app.sdk != nil && app.sdk.hooksManager != nil {
		steeringTotal, steeringOn := 0, 0
		for _, st := range app.sdk.hooksManager.ListHookStates() {
			if strings.HasPrefix(strings.ToLower(st.Name), "steering") {
				steeringTotal++
				if st.Enabled {
					steeringOn++
				}
			}
		}
		if steeringTotal > 0 {
			label := i18n.T("chat_b.side.steering_off")
			bg := th.BGLighter
			if steeringOn > 0 {
				label = i18n.T("chat_b.side.steering_on")
				bg = th.Accent
			}
			controlChips = append(controlChips, lipgloss.NewStyle().
				Foreground(lipgloss.Color(th.Text)).
				Background(lipgloss.Color(bg)).
				Padding(0, 1).Render(label))
		}
	}
	for _, row := range packSidebarChips(controlChips, innerW) {
		add(row)
	}

	// ── ZONE 4: ACTIVITY ──────────────────────────────────────────────
	modifiedFiles := s.extractModifiedFiles(app)
	usageLines := s.renderSidebarUsage(app)

	type activitySection struct {
		title string
		body  []string
	}
	var actSections []activitySection

	// Audit log — file modifications from tool calls
	if len(modifiedFiles) > 0 {
		var body []string
		maxAudit := 4
		for i, f := range modifiedFiles {
			if i >= maxAudit {
				break
			}
			p := truncateStr(f.path, innerW-4)
			body = append(body, clr(f.icon+" ", th.Success)+muted(p))
		}
		if len(modifiedFiles) > maxAudit {
			body = append(body, dim(i18n.T("chat_b.side.more", len(modifiedFiles)-maxAudit)))
		}
		actSections = append(actSections, activitySection{i18n.T("residual.side.audit"), body})
	}

	// Quota (Claude Code / Gemini usage bars)
	if len(usageLines) > 0 {
		actSections = append(actSections, activitySection{i18n.T("residual.side.quota"), usageLines})
	}

	// Tasks / todos
	if todoMgr := ii.GetTodoManager(); todoMgr != nil && todoMgr.Count() > 0 {
		todos := todoMgr.Todos()
		summary := todoMgr.Summary()
		var body []string
		// Count string: first body item, will be merged onto the label line
		body = append(body, fmt.Sprintf("%d/%d", summary["completed"], summary["total"]))
		displayed := 0
		maxTodos := 10
		for _, todo := range todos {
			if displayed >= maxTodos {
				break
			}
			if todo.Status != ii.TodoStatusInProgress {
				continue
			}
			// Show category letter for in-progress tasks: [R][#1] Task content
			letter := getCategoryLetter(todo.Category)
			catBubble := clr("["+letter+"]", th.Warning)
			taskText := truncateStr(todo.Content, innerW-8) // account for [X] bubble
			body = append(body, catBubble+muted(" ")+clr("◉ ", th.Warning)+muted(taskText))
			displayed++
		}
		for _, todo := range todos {
			if displayed >= maxTodos {
				break
			}
			if todo.Status != ii.TodoStatusPending {
				continue
			}
			// Show category letter for pending tasks: [R][#1] Task content
			letter := getCategoryLetter(todo.Category)
			catBubble := dim("[" + letter + "]")
			taskText := truncateStr(todo.Content, innerW-8) // account for [X] bubble
			body = append(body, catBubble+muted(" ")+dim("○ ")+dim(taskText))
			displayed++
		}
		var footerParts []string
		if c := summary["completed"]; c > 0 {
			footerParts = append(footerParts, clr(fmt.Sprintf("✓ %d", c), th.Success))
		}
		if p := summary["pending"]; p > 0 {
			footerParts = append(footerParts, dim(i18n.T("chat_b.side.pending", p)))
		}
		if len(footerParts) > 0 {
			body = append(body, "  "+strings.Join(footerParts, "  "))
		}
		actSections = append(actSections, activitySection{i18n.T("residual.side.tasks"), body})
	}

	// Background agents
	if app.sdk != nil {
		if bgAgents := app.sdk.GetBackgroundAgents(); len(bgAgents) > 0 {
			var body []string
			body = append(body, fmt.Sprintf("%d", len(bgAgents)))
			for i, agent := range bgAgents {
				if i >= 3 {
					break
				}
				var icon, c string
				switch agent.Status {
				case "running":
					icon = "●"
					c = th.Warning
				case "completed":
					icon = "✓"
					c = th.Success
				case "failed":
					icon = "✗"
					c = th.Error
				default:
					icon = "○"
					c = th.TextMuted
				}
				task := strings.Join(strings.Fields(agent.Task), " ")
				if task == "" {
					task = agent.AgentID
				}
				task = truncateStr(task, innerW-4)
				body = append(body, clr(icon+" ", c)+muted(task))
				if agent.Summary != "" {
					progress := truncateStr(strings.Join(strings.Fields(agent.Summary), " "), innerW-7)
					body = append(body, dim("  └ ")+dim(progress))
				} else if agent.LastTool != "" {
					body = append(body, dim("  └ ")+dim(agent.LastTool))
				}
			}
			actSections = append(actSections, activitySection{i18n.T("residual.side.agents"), body})
		}
	}

	// Bash activity: running commands (with live output tail) and recent status of
	// explicitly backgrounded commands. Rendered by the richer renderBashActivity.
	if len(bashRows) > 0 {
		bt := bashTheme{
			Success: th.Success, Warning: th.Warning, Error: th.Error,
			Accent: th.Accent, Text: th.Text, TextDim: th.TextDim,
			TextMute: th.TextMuted, BG: th.BG,
		}
		body := renderBashActivity(bashActivityInput{
			rows:        bashRows,
			width:       innerW,
			focused:     app.bashPanelFocused,
			selectedIdx: app.bashSelectedIdx,
			theme:       bt,
		})
		if len(body) > 0 {
			actSections = append(actSections, activitySection{i18n.T("residual.side.bash"), body})
		}
	}

	if len(actSections) > 0 {
		// Live / actionable work first, passive telemetry last — the user
		// glances here for "what is happening now", not the audit trail.
		activityRank := map[string]int{
			i18n.T("residual.side.tasks"):  0,
			i18n.T("residual.side.agents"): 1,
			i18n.T("residual.side.bash"):   2,
			i18n.T("residual.side.audit"):  3,
			i18n.T("residual.side.quota"):  4,
		}
		sort.SliceStable(actSections, func(i, j int) bool {
			return activityRank[actSections[i].title] < activityRank[actSections[j].title]
		})
		gap()
		for i, as := range actSections {
			if i > 0 {
				gap()
			}
			// Title line: UPPERCASE label, with count merged on same line when body[0] is digits
			titleLine := sectionLbl(as.title)
			body := as.body
			if len(body) > 0 {
				first := body[0]
				// Detect plain counter like "3" or "2/5" — merge onto label line
				isCount := len(first) > 0
				for _, r := range first {
					if (r < '0' || r > '9') && r != '/' {
						isCount = false
						break
					}
				}
				if isCount {
					titleLine += muted("  " + first)
					body = body[1:]
				}
			}
			add(titleLine)
			for _, bodyLine := range body {
				add(bodyLine)
			}
		}
	}

	// ── ZONE 5: HINT — pinned to the very bottom ───────────────────────
	// Fill unused vertical space so the hint is always the last visible line.
	innerHeight := s.height - 2             // subtract top + bottom padding
	padding := innerHeight - len(lines) - 2 // 2 = rule + hint
	if padding < 0 {
		padding = 0
	}
	for i := 0; i < padding; i++ {
		gap()
	}
	rule()
	add(dim(i18n.T("residual.side.toggle_panel")))

	// ── ASSEMBLE ──────────────────────────────────────────────────────
	var b strings.Builder
	b.Grow(len(lines) * (innerW + 1))
	for i, ln := range lines {
		if i > 0 {
			b.WriteByte('\n')
		}
		b.WriteString(ln)
	}
	content := b.String()

	panelStyle := lipgloss.NewStyle().
		Width(s.width).
		Height(s.height).
		Padding(1, 1).
		Background(lipgloss.Color(th.BG)).
		BorderLeft(true).
		BorderStyle(lipgloss.NormalBorder()).
		BorderForeground(lipgloss.Color(th.Border)).
		BorderBackground(lipgloss.Color(th.BG))

	rendered := panelStyle.Render(content)

	// === UPDATE CACHE ===
	cache.rendered = rendered
	cache.valid = true
	cache.tokenCount = app.tokenCount
	cache.mode = app.operatingMode
	cache.permissionLevel = string(permissionLevel)
	cache.width = s.width
	cache.height = s.height
	cache.toolCount = toolCount
	cache.bgAgentCount = bgAgentCount
	cache.bgAgentDigest = bgAgentDigest
	cache.bgProcessCount = bgProcessCount
	cache.bashDigest = bashDigest
	cache.showThinking = app.showThinking
	cache.showVerbose = app.showFullToolOutput
	cache.cachingOn = cachingOn
	cache.microCompactionOn = microCompactionOn
	cache.autoModeOn = autoModeOn
	cache.currentModel = app.currentModel
	cache.currentProvider = app.currentProvider
	cache.currentModelDisplay = app.currentModelDisplay
	cache.currentProviderDisplay = app.currentProviderDisplay
	cache.usageFetchedAt = usageTime
	cache.todoCount = todoCount
	cache.budgetToolCalls = budgetToolCalls
	cache.budgetSkilled = budgetSkilled
	cache.budgetNudgeIgnores = budgetNudgeIgnores
	cache.budgetEnabled = budgetEnabled
	cache.vaultUnlocked = vaultUnlocked
	cache.tpsCurrentValue = tpsCurRounded
	cache.tpsAvgValue = tpsAvgRounded
	cache.tpsPeakValue = tpsPeakRounded
	cache.tpsStreaming = tpsStreaming
	cache.goalState = goalState
	cache.goalIterations = goalIterations
	cache.goalReason = goalReason

	return rendered
}

// packSidebarChips joins styled chips into width-bounded rows. ANSI styling is
// ignored when measuring visual width, and chips are never split across rows.
func packSidebarChips(chips []string, maxWidth int) []string {
	if len(chips) == 0 {
		return nil
	}

	rows := make([]string, 0, 1)
	current := ""
	currentWidth := 0
	for _, chip := range chips {
		chipWidth := lipgloss.Width(chip)
		if current == "" {
			current = chip
			currentWidth = chipWidth
			continue
		}
		if currentWidth+1+chipWidth <= maxWidth {
			current += " " + chip
			currentWidth += 1 + chipWidth
			continue
		}
		rows = append(rows, current)
		current = chip
		currentWidth = chipWidth
	}
	if current != "" {
		rows = append(rows, current)
	}
	return rows
}

// formatTPSLine renders the body of the side panel's tokens/sec line.
//
//	streaming:  "42 tok/s   avg 38 · peak 51"
//	idle:       "38 tok/s session · 35 all-time"
//
// Returns "" when there is no meaningful data yet (fresh session, nothing
// streamed, no history for this model) so the panel stays uncluttered.
func formatTPSLine(current, avg, peak, allTime float64, streaming bool) string {
	switch {
	case streaming && current > 0:
		s := i18n.T("residual.side.tps_current", current)
		if avg > 0 {
			s += i18n.T("residual.side.tps_avg", avg)
		}
		if peak > 0 && peak != current {
			s += i18n.T("residual.side.tps_peak", peak)
		}
		return s
	case avg > 0 && allTime > 0:
		return i18n.T("residual.side.tps_session_all_time", avg, allTime)
	case avg > 0:
		return i18n.T("residual.side.tps_session", avg)
	case allTime > 0:
		return i18n.T("residual.side.tps_all_time", allTime)
	}
	return ""
}

// formatElapsed formats a duration into a compact human-readable string.
func formatElapsed(d time.Duration) string {
	if d < time.Second {
		return "0s"
	}
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	if d < time.Hour {
		mins := int(d.Minutes())
		secs := int(d.Seconds()) % 60
		return fmt.Sprintf("%dm%ds", mins, secs)
	}
	hours := int(d.Hours())
	mins := int(d.Minutes()) % 60
	return fmt.Sprintf("%dh%dm", hours, mins)
}

// Command palette types
type CommandPaletteItem struct {
	ID          string
	Label       string
	Description string
	Keybind     string
	Category    string
}

type CommandPalette struct {
	visible     bool
	items       []CommandPaletteItem
	filtered    []CommandPaletteItem
	selected    int
	searchQuery string
	width       int
	height      int
}

// NewCommandPalette creates a new command palette
func NewCommandPalette() *CommandPalette {
	items := []CommandPaletteItem{
		// Quick actions
		{ID: "help", Label: i18n.T("residual.palette.help"), Description: i18n.T("residual.palette.help_desc"), Keybind: "?", Category: i18n.T("residual.palette.quick")},
		{ID: "switch_model", Label: i18n.T("residual.palette.switch_model"), Description: i18n.T("residual.palette.switch_model_desc"), Keybind: "Ctrl+M", Category: i18n.T("residual.palette.quick")},
		{ID: "switch_agent", Label: i18n.T("residual.palette.switch_agent"), Description: i18n.T("residual.palette.switch_agent_desc"), Keybind: "Ctrl+J", Category: i18n.T("residual.palette.quick")},
		{ID: "switch_prompt", Label: i18n.T("residual.palette.switch_prompt"), Description: i18n.T("residual.palette.switch_prompt_desc"), Keybind: "Alt+P", Category: i18n.T("residual.palette.quick")},
		{ID: "switch_profile", Label: i18n.T("residual.palette.switch_profile"), Description: i18n.T("residual.palette.switch_profile_desc"), Keybind: "Ctrl+P", Category: i18n.T("residual.palette.quick")},

		// Settings
		{ID: "model", Label: i18n.T("residual.palette.model_settings"), Description: i18n.T("residual.palette.model_settings_desc"), Keybind: "", Category: i18n.T("residual.palette.settings")},
		{ID: "auth", Label: i18n.T("residual.palette.authenticate"), Description: i18n.T("residual.palette.authenticate_desc"), Keybind: "", Category: i18n.T("residual.palette.settings")},
		{ID: "render", Label: i18n.T("residual.palette.display_settings"), Description: i18n.T("residual.palette.display_settings_desc"), Keybind: "", Category: i18n.T("residual.palette.settings")},
		{ID: "mcp", Label: i18n.T("residual.palette.tools_mcp"), Description: i18n.T("residual.palette.tools_mcp_desc"), Keybind: "", Category: i18n.T("residual.palette.settings")},
		{ID: "hooks", Label: i18n.T("residual.palette.hooks"), Description: i18n.T("residual.palette.hooks_desc"), Keybind: "", Category: i18n.T("residual.palette.settings")},

		// Navigation
		{ID: "settings", Label: i18n.T("residual.palette.open_settings"), Description: i18n.T("residual.palette.open_settings_desc"), Keybind: "", Category: i18n.T("residual.palette.navigation")},
		{ID: "new_chat", Label: i18n.T("residual.palette.new_chat"), Description: i18n.T("residual.palette.new_chat_desc"), Keybind: "Ctrl+N", Category: i18n.T("residual.palette.navigation")},
		{ID: "conversations", Label: i18n.T("residual.palette.conversations"), Description: i18n.T("residual.palette.conversations_desc"), Keybind: "", Category: i18n.T("residual.palette.navigation")},
	}

	return &CommandPalette{
		items:    items,
		filtered: items,
	}
}

// Show opens the command palette
func (cp *CommandPalette) Show() {
	cp.visible = true
	cp.searchQuery = ""
	cp.selected = 0
	cp.filtered = cp.items
}

// Hide closes the command palette
func (cp *CommandPalette) Hide() {
	cp.visible = false
}

// IsVisible returns true if palette is open
func (cp *CommandPalette) IsVisible() bool {
	return cp.visible
}

// Update handles input
func (cp *CommandPalette) Update(key string) {
	switch key {
	case "down", "ctrl+j", "j":
		if cp.selected < len(cp.filtered)-1 {
			cp.selected++
		}
	case "up", "ctrl+k", "k":
		if cp.selected > 0 {
			cp.selected--
		}
	case "esc":
		cp.Hide()
	case "backspace":
		if len(cp.searchQuery) > 0 {
			cp.searchQuery = cp.searchQuery[:len(cp.searchQuery)-1]
			cp.filter()
		}
	default:
		// Don't capture j/k if we already handled them above
		if key == "j" || key == "k" {
			return
		}
		// Add to search query if it's a printable character
		if len(key) == 1 {
			cp.searchQuery += key
			cp.filter()
		}
	}
}

// filter updates the filtered list based on search query
func (cp *CommandPalette) filter() {
	if cp.searchQuery == "" {
		cp.filtered = cp.items
		cp.selected = 0
		return
	}

	query := strings.ToLower(cp.searchQuery)
	cp.filtered = nil

	for _, item := range cp.items {
		if strings.Contains(strings.ToLower(item.Label), query) ||
			strings.Contains(strings.ToLower(item.Description), query) ||
			strings.Contains(strings.ToLower(item.Category), query) {
			cp.filtered = append(cp.filtered, item)
		}
	}

	if cp.selected >= len(cp.filtered) {
		cp.selected = len(cp.filtered) - 1
	}
	if cp.selected < 0 {
		cp.selected = 0
	}
}

// SelectedItem returns the currently selected item
func (cp *CommandPalette) SelectedItem() *CommandPaletteItem {
	if cp.selected >= 0 && cp.selected < len(cp.filtered) {
		return &cp.filtered[cp.selected]
	}
	return nil
}

// Render renders the command palette - minimal and sleek
func (cp *CommandPalette) Render(width, height int, theme Theme) string {
	cp.width = width
	cp.height = height

	// Compact width for minimalism
	paletteWidth := 70
	if paletteWidth > width-10 {
		paletteWidth = width - 10
	}

	// Search input - clean and minimal
	searchPrompt := lipgloss.NewStyle().
		Foreground(lipgloss.Color(theme.Primary)).
		Render("❯ ")

	cursorStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(theme.Primary))

	cursor := ""
	if len(cp.searchQuery) == 0 {
		cursor = cursorStyle.Render("█")
	} else {
		cursor = cursorStyle.Render("█")
	}

	searchText := lipgloss.NewStyle().
		Foreground(lipgloss.Color(theme.Text)).
		Render(cp.searchQuery + cursor)

	searchLine := searchPrompt + searchText

	// Items - clean list
	var itemViews []string
	maxItems := 8

	for i, item := range cp.filtered {
		if i >= maxItems {
			break
		}

		// Label with bold for selected
		var labelStyle lipgloss.Style
		if i == cp.selected {
			labelStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color(theme.Primary)).
				Bold(true)
		} else {
			labelStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color(theme.Text))
		}

		// Description - always muted
		descStyle := lipgloss.NewStyle().
			Foreground(lipgloss.Color(theme.TextMuted))

		// Prefix indicator
		prefix := "  "
		if i == cp.selected {
			prefix = "› "
		}

		// Build line: prefix + label + description
		label := labelStyle.Render(item.Label)
		desc := descStyle.Render(item.Description)

		// Calculate spacing to align descriptions
		labelWidth := lipgloss.Width(prefix + item.Label)
		targetWidth := 25 // Align descriptions at column 25
		spacing := targetWidth - labelWidth
		if spacing < 2 {
			spacing = 2
		}

		line := prefix + label + strings.Repeat(" ", spacing) + desc

		// Add keybind if present
		if item.Keybind != "" {
			keybindStyle := lipgloss.NewStyle().
				Foreground(lipgloss.Color(theme.TextMuted))
			line += "  " + keybindStyle.Render(item.Keybind)
		}

		itemViews = append(itemViews, line)
	}

	if len(itemViews) == 0 {
		noResults := lipgloss.NewStyle().
			Foreground(lipgloss.Color(theme.TextMuted)).
			Italic(true).
			Render(i18n.T("chat_b.side.no_commands"))
		itemViews = append(itemViews, noResults)
	}

	// Separator
	separator := lipgloss.NewStyle().
		Foreground(lipgloss.Color(theme.Border)).
		Render(strings.Repeat("─", paletteWidth-4))

	// Build content
	var content strings.Builder
	content.WriteString(searchLine)
	content.WriteString("\n")
	content.WriteString(separator)
	content.WriteString("\n")

	for _, item := range itemViews {
		content.WriteString(item)
		content.WriteString("\n")
	}

	// Hints at bottom
	hintStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(theme.TextMuted)).
		Italic(true)

	hints := hintStyle.Render(i18n.T("chat_b.side.command_hint"))
	content.WriteString(separator)
	content.WriteString("\n")
	content.WriteString(hints)

	// Wrap in minimal border
	paletteStyle := lipgloss.NewStyle().
		Width(paletteWidth).
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color(theme.Primary)).
		Padding(1, 2)

	return paletteStyle.Render(content.String())
}

// extractModifiedFiles parses conversation for file_write tool calls
func (s *SidePanel) extractModifiedFiles(app *App) []struct {
	path string
	icon string
} {
	var files []struct {
		path string
		icon string
	}

	seen := make(map[string]bool)

	// Iterate messages in reverse to get most recent first
	for i := len(app.messages) - 1; i >= 0; i-- {
		msg := app.messages[i]

		// Check tool calls for file_write
		for _, toolCall := range msg.ToolCalls {
			if toolCall.Name == "file_write" {
				if path, ok := toolCall.Parameters["path"].(string); ok && !seen[path] {
					seen[path] = true
					icon := s.getFileIcon(path)
					files = append(files, struct {
						path string
						icon string
					}{path, icon})
				}
			}
		}
	}

	return files
}

// getFileIcon returns an appropriate icon for a file based on extension
func (s *SidePanel) getFileIcon(path string) string {
	// Extract extension
	parts := strings.Split(path, ".")
	if len(parts) < 2 {
		return "󰈙" // Default file icon
	}

	ext := strings.ToLower(parts[len(parts)-1])

	switch ext {
	case "go":
		return "󰟓"
	case "md":
		return ""
	case "js", "ts":
		return ""
	case "json":
		return ""
	case "yml", "yaml":
		return ""
	case "toml":
		return ""
	case "sh":
		return ""
	case "py":
		return ""
	case "txt":
		return ""
	default:
		return "󰈙"
	}
}

// formatTokenCount formats a token count with thousand separators
func formatTokenCount(count int) string {
	if count < 1000 {
		return fmt.Sprintf("%d", count)
	}
	if count < 1000000 {
		return fmt.Sprintf("%d,%03d", count/1000, count%1000)
	}
	return fmt.Sprintf("%d,%03d,%03d", count/1000000, (count/1000)%1000, count%1000)
}

// renderSidebarUsage renders intelligent, numerical quota information for multiple accounts.
func (s *SidePanel) renderSidebarUsage(app *App) []string {
	if app.usageResult == nil || len(app.usageResult.Entries) == 0 {
		return nil
	}

	th := app.theme
	var lines []string

	// Title
	titleStyle := lipgloss.NewStyle().
		Bold(true)
	lines = append(lines, titleStyle.Render(i18n.T("chat_b.side.quota_title")))

	// Styles for status
	okStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(th.Success))
	warnStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(th.Warning))
	critStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(th.Error))
	mutedStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(th.TextMuted))

	for _, entry := range app.usageResult.Entries {
		if entry.AnthropicData == nil || entry.Err != nil {
			continue
		}

		d := entry.AnthropicData

		// Find the "worst" window (highest utilization)
		windows := []struct {
			name string
			q    anthropicUsageQuota
		}{
			{"5h", d.FiveHour},
			{"7d", d.SevenDay},
			{"sn", d.SevenDaySonnet},
		}

		var worst *struct {
			name string
			q    anthropicUsageQuota
		}
		maxUtil := -1.0

		for i := range windows {
			if windows[i].q.Utilization > maxUtil {
				maxUtil = windows[i].q.Utilization
				worst = &windows[i]
			}
		}

		if worst == nil {
			continue
		}

		// Account label (email truncated)
		label := entry.Email
		if label == "" {
			label = i18n.T("chat_b.side.account")
		}
		maxLabelLen := s.width - 18
		if len(label) > maxLabelLen {
			atIdx := strings.Index(label, "@")
			if atIdx > 2 {
				label = label[:2] + "…" + label[atIdx:]
			}
			if len(label) > maxLabelLen {
				label = label[:maxLabelLen-1] + "…"
			}
		}

		// Pick color based on utilization
		valStyle := okStyle
		if maxUtil >= 80 {
			valStyle = critStyle
		} else if maxUtil >= 50 {
			valStyle = warnStyle
		}

		// Format reset time
		resetStr := ""
		if worst.q.ResetsAt != "" {
			if t, err := time.Parse(time.RFC3339, worst.q.ResetsAt); err == nil {
				dur := time.Until(t).Round(time.Minute)
				if dur > 0 {
					resetStr = " " + usageFormatReset(dur)
				}
			}
		}

		// Final line: Email: 85% (5h) 12m
		line := fmt.Sprintf(" %-s: %s (%s)%s",
			label,
			valStyle.Render(fmt.Sprintf("%d%%", int(maxUtil))),
			mutedStyle.Render(worst.name),
			mutedStyle.Render(resetStr))

		lines = append(lines, line)
	}

	if len(lines) == 1 { // Only the title
		return nil
	}

	return lines
}

// bashRowsDigest builds a compact fingerprint of the BASH rows so the side-panel
// cache re-renders when a status, exit code, or live output tail changes.
func bashRowsDigest(rows []bashRow) string {
	if len(rows) == 0 {
		return ""
	}
	var b strings.Builder
	for _, r := range rows {
		b.WriteString(r.id)
		b.WriteByte('|')
		b.WriteString(string(r.state))
		b.WriteByte('|')
		if r.exitCode != nil {
			fmt.Fprintf(&b, "%d", *r.exitCode)
		}
		b.WriteByte('|')
		for _, tl := range r.tail {
			b.WriteString(tl)
			b.WriteByte('\n')
		}
		b.WriteByte(';')
	}
	return b.String()
}

// backgroundAgentsDigest changes whenever visible agent identity, task,
// status, or progress changes, so cached side-panel rows never go stale.
func backgroundAgentsDigest(agents []toolbuiltin.BackgroundAgentInfo) string {
	if len(agents) == 0 {
		return ""
	}
	var b strings.Builder
	for _, agent := range agents {
		fmt.Fprintf(&b, "%s|%s|%s|%s|%s|%d;",
			agent.AgentID, agent.Status, agent.Task, agent.Summary,
			agent.LastTool, agent.ToolCount)
	}
	return b.String()
}
