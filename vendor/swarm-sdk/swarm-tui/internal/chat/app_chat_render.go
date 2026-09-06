package chat

import (
	"expvar"
	"fmt"
	"sort"
	"strings"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/conversation"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/tools/ii"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/chat/toolrender"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/i18n"
)

// Expvar counters — visible at http://localhost:6060/debug/vars
var (
	renderChatContentCalls    = expvar.NewInt("render_chat_content_calls")
	renderChatContentCacheHit = expvar.NewInt("render_chat_content_cache_hits") // header+divider+viewport cache hits
	renderViewportCalls       = expvar.NewInt("render_viewport_calls")
	renderViewportCacheHit    = expvar.NewInt("render_viewport_cache_hits")

	// Viewport cache miss breakdown — tells us WHY the lipgloss re-render runs
	renderViewportMissContent = expvar.NewInt("render_viewport_miss_content") // new scroll position or new streaming tokens
	renderViewportMissSize    = expvar.NewInt("render_viewport_miss_size")    // terminal resize changed width/height

	// Rapid-invalidation detector: counts frames where viewport re-rendered
	// within 32ms of the previous re-render (potential flicker source).
	renderViewportRapidSeq = expvar.NewInt("render_viewport_rapid_sequential")

	// SidePanel flicker tracking
	sidePanelRenderCalls    = expvar.NewInt("sidepanel_render_calls")
	sidePanelCacheHits      = expvar.NewInt("sidepanel_cache_hits")
	sidePanelCacheMissCount = expvar.NewInt("sidepanel_cache_misses")        // track misses to find flicker source
	sidePanelLastMissReason = expvar.NewString("sidepanel_last_miss_reason") // what invalidated the cache
)

// ============================================================================
// CHAT SCREEN VIEW
// ============================================================================

func (a *App) viewChat() string {
	// If a detail viewer is active (Enter-to-view on a bash command or
	// sub-agent), it takes over the chat area until dismissed with Esc.
	if a.detailViewer != nil {
		return a.detailViewer.View()
	}
	// If plan viewer is active (plan submitted for approval), show it instead of chat
	if a.planViewer != nil && a.planQuestionModal != nil {
		return a.planViewer.View()
	}
	return a.renderChatContent()
}

func shouldShowTaskPanel(modalActive, sidePanelToggled bool) bool {
	return !modalActive && !sidePanelToggled
}

func writeTaskPanelAboveInput(builder *strings.Builder, taskPanel, topSeparator string) {
	if taskPanel != "" {
		builder.WriteByte('\n')
		builder.WriteString(taskPanel)
	}
	builder.WriteByte('\n')
	builder.WriteString(topSeparator)
}

func (a *App) renderChatContent() string {
	languageKey := string(i18n.CurrentLanguage()) + "|"
	if a.cachedHeaderKey != "" && !strings.HasPrefix(a.cachedHeaderKey, languageKey) {
		a.cachedHeaderKey = languageKey
		a.cachedHeaderRendered = ""
		a.cachedViewportContent = ""
		a.cachedViewportRendered = ""
		for i := range a.messages {
			a.messages[i].MarkDirty()
		}
	} else if a.cachedHeaderKey == "" {
		a.cachedHeaderKey = languageKey
	}
	renderChatContentCalls.Add(1)
	th := a.theme

	if a.activeConv == nil {
		return tr("classic.chat.no_conversation")
	}

	// Use full width for chat view (no breakpoint constraints)
	// Chat view should maximize available space for code, diffs, and messages
	contentW := a.width

	// Clean header - just title, no clutter
	// Sanitize title: remove newlines and truncate to fit on one line
	titleContent := strings.ReplaceAll(a.activeConv.Title, "\n", " ")
	titleContent = strings.ReplaceAll(titleContent, "\r", " ")
	// Use visual-width truncation to account for padding (2 on each side = 4 total)
	maxTitleWidth := contentW - 6
	if lipgloss.Width(titleContent) > maxTitleWidth {
		// Truncate by runes to avoid cutting multi-byte characters
		runes := []rune(titleContent)
		for len(runes) > 0 && lipgloss.Width(string(runes)) > maxTitleWidth-1 {
			runes = runes[:len(runes)-1]
		}
		titleContent = string(runes) + "…"
	}
	titleText := Text{
		Content: titleContent,
		Color:   "",
		Bold:    true,
	}.Render()

	// Top header/divider is intentionally disabled: pressing Tab toggles only
	// the right-side info panel (side menu). The top menu (conversation title
	// bar + divider) is no longer shown so Tab affects the side panel alone.
	var showHeaderDivider bool = false

	var header, divider string
	if showHeaderDivider {
		header = lipgloss.NewStyle().
			Width(contentW).
			Padding(0, 2).
			Render(titleText + a.attachBanner())
		divider = Divider{Width: contentW, Theme: th}.Render()
	}

	// =========================================================================
	// DIMENSION CALCULATIONS - Must happen BEFORE any View() calls
	// =========================================================================

	// Dynamic input height - grows from minInputHeight up to an adaptive cap.
	// The cap scales with the terminal height (up to ~40% of the screen) so
	// large pastes and long multi-line messages are far more visible, while
	// always leaving the message viewport usable. A hard floor of 6 keeps the
	// old behaviour on short terminals.
	const minInputHeight = 1
	const absoluteMaxInputHeight = 20
	maxInputHeight := a.height * 2 / 5 // ~40% of the screen
	if maxInputHeight < 6 {
		maxInputHeight = 6
	}
	if maxInputHeight > absoluteMaxInputHeight {
		maxInputHeight = absoluteMaxInputHeight
	}

	// Check if the global status row will be shown (for height calculation).
	// Keep this predicate in sync with the render predicate below. The status
	// spinner is rendered outside the viewport cache, so it stays stable while
	// the message viewport is being rebuilt for tool/content updates.
	globalStatusActive := (a.streamingMessage || a.isCompacting) && (a.spinner.IsActive() || a.loadingIndicator.IsActive())
	spinnerHeight := 0
	if globalStatusActive {
		spinnerHeight = 2 // Spinner has Padding(1, 4, 0, 4) = 1 top padding + 1 content = 2 lines
	}

	// Set input width first to calculate wrapped line count. a.width is already
	// narrowed to the effective chat width during ScreenChat rendering.
	inputWidth := a.width - chatInputHorizontalPadding
	if inputWidth < 1 {
		inputWidth = 1
	}
	a.textInput.SetWidth(inputWidth)

	// Calculate actual input height based on content (dynamic sizing)
	// Hide input when a modal bar is active (approval or question)
	modalActive := a.approvalModal != nil || a.questionModal != nil || a.planQuestionModal != nil
	inputContentHeight := 0
	if !modalActive {
		inputContentHeight = a.textInput.GetContentHeight()
		if inputContentHeight < minInputHeight {
			inputContentHeight = minInputHeight
		}
		if inputContentHeight > maxInputHeight {
			inputContentHeight = maxInputHeight
		}
	}
	a.textInput.SetHeight(inputContentHeight)

	countLines := func(s string) int {
		if s == "" {
			return 0
		}
		return strings.Count(s, "\n") + 1
	}

	approvalBarRendered := ""
	approvalBarHeight := 0
	if a.approvalModal != nil {
		approvalBarRendered = a.approvalModal.RenderBar(a.width, a.theme)
		approvalBarHeight = countLines(approvalBarRendered)
	}

	// Count actual header and divider lines for accurate space calculation
	// Only count if they will be rendered (when sidebar is visible)
	headerHeight := 0
	dividerHeight := 0
	if showHeaderDivider {
		headerHeight = countLines(header)
		dividerHeight = countLines(divider)
	}

	questionBarRendered := ""
	questionBarHeight := 0
	if a.questionModal != nil && a.approvalModal == nil {
		questionBarRendered = a.questionModal.RenderBar(a.width, a.height, a.theme)
		questionBarHeight = countLines(questionBarRendered)
	}

	// Plan approval bar (planQuestionModal) – supersedes regular questionModal at bottom
	planQuestionBarRendered := ""
	planQuestionBarHeight := 0
	if a.planQuestionModal != nil && a.approvalModal == nil {
		planQuestionBarRendered = a.planQuestionModal.RenderBar(a.width, a.height, a.theme)
		planQuestionBarHeight = countLines(planQuestionBarRendered)
		// When the plan bar is active, suppress the regular question bar
		questionBarRendered = ""
		questionBarHeight = 0
	}

	workflowParamBarRendered := ""
	workflowParamBarHeight := 0

	// Edit mode hint bar height
	editHintHeight := 0
	if a.editMessageMode {
		editHintHeight = 1
	}

	// Queued/pending messages indicator — rendered NOW so its true line count is
	// known before we compute reservedLines and availableViewportLines.
	// This ensures the viewport shrinks to make room rather than overflowing.
	var queuedMsgRendered string
	queuedMsgHeight := 0
	a.pendingMsgMu.Lock()
	pendingSnapshot := make([]string, len(a.pendingUserMessages))
	copy(pendingSnapshot, a.pendingUserMessages)
	a.pendingMsgMu.Unlock()
	if len(pendingSnapshot) > 0 {
		// Style tokens — from dim→accent so the count pops and previews recede.
		accentBold := lipgloss.NewStyle().
			Foreground(lipgloss.Color(th.Accent)).
			Bold(true)
		mutedStyle := lipgloss.NewStyle().
			Foreground(lipgloss.Color(a.theme.TextMuted))
		bulletStyle := lipgloss.NewStyle().
			Foreground(lipgloss.Color(th.Border))
		dimStyle := lipgloss.NewStyle().
			Foreground(lipgloss.Color(th.TextDim))

		// Effective content width inside Padding(0,2) wrapper
		innerW := a.width - 4
		if innerW < 10 {
			innerW = 10
		}
		previewW := innerW - 4 // "  › " prefix occupies 4 chars

		noun := tr("classic.chat.message")
		if len(pendingSnapshot) != 1 {
			noun = tr("classic.chat.messages")
		}
		// Header: "⟳ 2 messages queued"
		header := accentBold.Render(fmt.Sprintf("⟳ %d", len(pendingSnapshot))) +
			" " + mutedStyle.Render(tr("classic.chat.queued", noun))

		rows := make([]string, 0, 1+len(pendingSnapshot))
		rows = append(rows, header)
		for _, msg := range pendingSnapshot {
			preview := strings.Join(strings.Fields(msg), " ")
			// Trim to available width (rune-safe)
			if lipgloss.Width(preview) > previewW {
				runes := []rune(preview)
				for len(runes) > 0 && lipgloss.Width(string(runes)) > previewW-1 {
					runes = runes[:len(runes)-1]
				}
				preview = string(runes) + "…"
			}
			rows = append(rows, bulletStyle.Render("  ›")+" "+dimStyle.Render(preview))
		}

		queuedMsgRendered = lipgloss.NewStyle().
			Width(a.width).
			Padding(0, 2).
			Render(strings.Join(rows, "\n"))
		queuedMsgHeight = countLines(queuedMsgRendered)
	}

	// Bash dock (compact list of background bash commands and agents, docked
	// BELOW the input). Rendered BEFORE
	// reservedLines so its height shrinks the viewport instead of overflowing
	// the screen, and so the height change can be traced before it's ever
	// applied to layout.
	prevBashDockHeight := a.lastBashDockHeight
	bashDockRendered := ""
	bashDockHeight := 0
	if a.sdk != nil {
		entries := a.collectDockEntries()
		// Auto-unfocus when nothing is left to show.
		if len(entries) == 0 {
			a.bashDockFocused = false
		}
		// Resolve the ID-anchored selection to an index (stable across the
		// non-deterministic manager ordering; falls back to the first entry
		// when the selected item finished / aged out).
		selIdx := a.dockSelectedIndex(entries)
		tailFn := func(taskID string, n int) []string {
			view, ok := a.sdk.GetBackgroundProcessOutput(taskID, n)
			if !ok || view == nil {
				return nil
			}
			out := make([]string, 0, len(view.Lines))
			for _, ln := range view.Lines {
				out = append(out, ln.Content)
			}
			return out
		}
		bashDockRendered, bashDockHeight = renderBashDock(
			entries, selIdx, a.bashDockFocused, a.width, th.BG, tailFn)
	}
	if bashDockHeight != prevBashDockHeight {
		a.lastBashDockHeight = bashDockHeight
	}

	// Task panel — active tasks (in_progress + pending) shown directly above the
	// input box, Claude-style. Rendered NOW (BEFORE reservedLines) so its true
	// line count shrinks the viewport instead of pushing the input box off the
	// bottom of the screen. Only shown when there are visible tasks, no bottom
	// modal bar is taking over the input area, and the side panel is toggled off.
	prevTaskPanelHeight := a.lastTaskPanelHeight
	var taskPanelRendered string
	taskPanelHeight := 0
	if shouldShowTaskPanel(modalActive, a.showSidePanel) {
		a.taskPanel.SetWidth(a.width - 4) // account for Padding(0, 2)
		if todoManager := ii.GetTodoManager(); todoManager != nil {
			a.taskPanel.SetTasks(todoManager.Todos())
		}
		if a.taskPanel.HasVisibleTasks() {
			taskPanelRendered = lipgloss.NewStyle().
				Width(a.width).
				Padding(0, 2).
				Render(a.taskPanel.View())
			taskPanelHeight = countLines(taskPanelRendered)
		}
	}
	if taskPanelHeight != prevTaskPanelHeight {
		a.lastTaskPanelHeight = taskPanelHeight
	}

	// Calculate available space for viewport.
	// Reserve: header + divider + spinner(0-2) + topSep(1) + editHint(0-1) + modals + queued-msgs + task-panel(0-N) + bash-dock(0-N) + input(dynamic) + bottomSep(1)
	// NOTE: bash-dock is grouped with task-panel/queued-msgs in the "leader" set
	// (absorbed entirely by the elastic message viewport above) — it must NOT be
	// counted in a trailing/"below input" position, or its height changes shift
	// the input's screen row every time a background command starts/finishes.
	reservedLines := headerHeight + dividerHeight + spinnerHeight + 1 + editHintHeight + approvalBarHeight + questionBarHeight + planQuestionBarHeight + workflowParamBarHeight + queuedMsgHeight + taskPanelHeight + bashDockHeight + inputContentHeight + 1
	// When sidebar is visible, add 2 extra lines to push input up
	if showHeaderDivider {
		reservedLines += 2
	}
	availableViewportLines := a.height - reservedLines
	if availableViewportLines < 5 {
		availableViewportLines = 5 // Minimum viewport height
	}
	if availableViewportLines < 1 {
		availableViewportLines = 1
	}

	// Calculate viewport width. a.width is already narrowed to the effective
	// chat width during ScreenChat rendering, so never subtract SidePanelWidth here.
	viewportWidth := a.width - chatViewportHorizontalPadding
	if viewportWidth < 1 {
		viewportWidth = 1
	}

	// Capture pre-resize state for sticky-bottom on layout changes.
	oldWidth := a.msgViewport.Width
	oldHeight := a.msgViewport.Height
	wasAtBottom := a.msgViewport.AtBottom()
	// CRITICAL: Set viewport size BEFORE calling View()
	a.msgViewport.SetSize(viewportWidth, availableViewportLines)
	// Set origin for mouse coordinate mapping (screen → content)
	originY := 0
	if showHeaderDivider {
		// header has Padding(1,2) = +2 vertical lines, divider has no padding
		originY = headerHeight + dividerHeight + 2
	}
	a.msgViewport.SetOrigin(2, originY)
	if viewportWidth != oldWidth {
		a.viewportContentDirty = true
	}

	// =========================================================================
	// RENDER - Only re-render content if something changed (dirty flag).
	// Pure scroll events skip this — content is already in the viewport.
	// updateViewportContent handles its own sticky-bottom (GotoBottom) internally.
	// =========================================================================
	//
	// ANIMATION FAST PATH: when the TTE intro is running and the message list is
	// empty, render the frame directly into viewportContent — bypassing the
	// scrollable viewport entirely.  The viewport's YOffset state and the
	// lipgloss Height() truncation are the sources of the up/down jitter; going
	// direct eliminates both.
	var viewportContent string
	if !a.introComplete && len(a.messages) == 0 {
		viewportContent = a.renderChatIntroContent(viewportWidth, availableViewportLines)
	} else {
		if a.viewportContentDirty {
			a.updateViewportContent()
		} else if availableViewportLines != oldHeight && wasAtBottom && !a.userScrolledAway {
			// Height-only change (spinner appeared/disappeared, queued-msg bar grew/shrank):
			// content didn't change so updateViewportContent wasn't called, but maxYOffset
			// shifted. Re-pin to bottom so the user doesn't drift away from the end.
			a.msgViewport.GotoBottom()
		}
		viewportContent = a.msgViewport.View()
	}

	// Use dynamic height for layout calculation
	actualInputHeight := inputContentHeight

	// Feed active-skill keyword triggers into the input so matching words
	// are highlighted in Warning colour while the user types.
	if a.sdk != nil {
		a.textInput.SetHighlightTerms(getActiveSkillKeywords(a.sdk.skillsManager))
	}

	// Get the rendered input text (now correctly sized)
	inputText := a.textInput.View()

	// Prompt symbol
	var prompt string
	if a.voiceRecording {
		dot := "●"
		if a.voicePulseTick {
			dot = "○"
		}
		prompt = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#FF4444")).
			Bold(true).
			Render(dot)
	} else {
		prompt = lipgloss.NewStyle().
			Foreground(lipgloss.Color(th.Primary)).
			Render(">")
	}

	// Handle multi-line input with proper indentation
	// Input text may contain newlines - we need to add prefix only to first line
	inputTextLines := strings.Split(inputText, "\n")
	logDebug("[viewMainChat] inputTextLines count: %d, inputText len: %d", len(inputTextLines), len(inputText))

	var formattedInput strings.Builder
	if len(inputTextLines) > 0 {
		// First line gets prefix: "> text"
		formattedInput.WriteString(prompt)
		formattedInput.WriteString(" ")
		formattedInput.WriteString(inputTextLines[0])
		// Continuation lines get 2 spaces to align with text after "> "
		for i := 1; i < len(inputTextLines); i++ {
			formattedInput.WriteString("\n  ")
			formattedInput.WriteString(inputTextLines[i])
		}
	} else {
		formattedInput.WriteString(prompt)
		formattedInput.WriteString(" ")
	}

	// Show current attachments if any
	var attachmentsLine string
	if len(a.currentAttachments) > 0 {
		attachmentStyle := lipgloss.NewStyle().
			Foreground(lipgloss.Color(a.theme.TextMuted)).
			Background(lipgloss.Color(th.BGLight))

		var attParts []string
		for i, att := range a.currentAttachments {
			sizeKB := att.Size / 1024
			icon := "📎"
			if strings.HasPrefix(att.MimeType, "image/") {
				icon = "🖼️"
			}
			attText := fmt.Sprintf(" %d: %s %s (%d KB) ", i, icon, att.FileName, sizeKB)
			attParts = append(attParts, attachmentStyle.Render(attText))
		}
		attachmentsLine = strings.Join(attParts, " ") + "\n"
	}

	inputLine := attachmentsLine + formattedInput.String()

	// Enforce max height for input area (truncate if needed)
	inputLines := strings.Split(inputLine, "\n")
	if len(inputLines) > actualInputHeight {
		// Too many lines - keep the LAST N lines to show newest text with cursor
		inputLines = inputLines[len(inputLines)-actualInputHeight:]
		inputLine = strings.Join(inputLines, "\n")
	}

	// Light blue separator lines above and below input — fully cached including render.
	// Only rebuilt when width changes; reusing the rendered string avoids lipgloss allocs.
	if a.cachedSeparator.width != a.width || a.cachedSeparator.rendered == "" {
		if a.cachedSeparator.width != a.width || a.cachedSeparator.str == "" {
			a.cachedSeparator.str = strings.Repeat("─", a.width)
		}
		a.cachedSeparator.rendered = lipgloss.NewStyle().
			Foreground(lipgloss.Color(th.Border)).
			Width(a.width).
			Render(a.cachedSeparator.str)
		a.cachedSeparator.width = a.width
	}
	topSeparator := a.cachedSeparator.rendered
	bottomSeparator := topSeparator // Same string, reuse

	// LAYOUT: Everything stacked vertically with proper containment

	// 1. Header with title (fixed, compact) — cached by title+workflow+width key
	var headerRendered string
	if showHeaderDivider {
		headerKey := fmt.Sprintf("%s%s|%d", languageKey, header, a.width)
		if a.cachedHeaderKey != headerKey || a.cachedHeaderRendered == "" {
			a.cachedHeaderRendered = lipgloss.NewStyle().
				Width(a.width).
				Padding(1, 2).
				Render(header)
			a.cachedHeaderKey = headerKey
		} else {
			renderChatContentCacheHit.Add(1)
		}
		headerRendered = a.cachedHeaderRendered
	}

	// Notification banners (optional)
	// 2. Divider (fixed) — cached by width
	var dividerRendered string
	if showHeaderDivider {
		if a.cachedDividerWidth != a.width || a.cachedDividerRendered == "" {
			a.cachedDividerRendered = lipgloss.NewStyle().
				Width(a.width).
				Render(divider)
			a.cachedDividerWidth = a.width
		}
		dividerRendered = a.cachedDividerRendered
	}

	// 3. Message viewport (takes remaining space, properly bounded) — cached by content+dims
	renderViewportCalls.Add(1)
	var viewportRendered string
	sizeChanged := a.cachedViewportWidth != a.width || a.cachedViewportHeight != availableViewportLines
	contentChanged := a.cachedViewportContent != viewportContent
	if !sizeChanged && !contentChanged && a.cachedViewportRendered != "" {
		renderViewportCacheHit.Add(1)
		viewportRendered = a.cachedViewportRendered
	} else {
		// Track why we missed — helps diagnose flicker sources at /debug/vars
		if sizeChanged {
			renderViewportMissSize.Add(1)
		}
		if contentChanged {
			renderViewportMissContent.Add(1)
		}
		// Detect rapid sequential re-renders (< 32ms apart = potential flicker source)
		now := time.Now()
		if !a.cachedViewportLastMissAt.IsZero() && now.Sub(a.cachedViewportLastMissAt) < 32*time.Millisecond {
			renderViewportRapidSeq.Add(1)
		}
		a.cachedViewportLastMissAt = now

		// This used to be a lipgloss Style with Width/Height/Padding, which
		// measured EVERY line of the whole viewport with ansi.StringWidth ->
		// FirstGraphemeCluster (34% of total CPU while scrolling). All three
		// were redundant:
		//   Width(a.width)  -> normalizeFrameForTerminal (app_view.go:413)
		//                      already pads every frame line to a.width.
		//   Height(n)       -> a plain line-count pad, no measuring needed.
		//   Padding(0, 2)   -> two literal spaces.
		// Only the left pad is real work, and it needs no width computation.
		//
		// ponytail: assumes viewportContent lines already fit viewportWidth
		// (a.width-4). They do — content arrives pre-wrapped. lipgloss would
		// have hard-wrapped an over-long line; this does not.
		vpLines := strings.Split(viewportContent, "\n")
		padTo := availableViewportLines
		if len(vpLines) > padTo {
			padTo = len(vpLines)
		}
		var vb strings.Builder
		vb.Grow(len(viewportContent) + 3*padTo)
		for i := 0; i < padTo; i++ {
			if i > 0 {
				vb.WriteByte('\n')
			}
			vb.WriteString("  ")
			if i < len(vpLines) {
				vb.WriteString(vpLines[i])
			}
		}
		viewportRendered = vb.String()
		a.cachedViewportRendered = viewportRendered
		a.cachedViewportContent = viewportContent
		a.cachedViewportWidth = a.width
		a.cachedViewportHeight = availableViewportLines
	}

	// 3b. Spinner line - rendered OUTSIDE viewport cache for independent animation
	// This allows the spinner to animate smoothly without expensive viewport rebuilds
	//
	// Chrome bars (spinner box, separators, input box) used to paint th.BG
	// across their full width. On a light terminal that draws a black blob
	// behind the agent-status / input / separator rows. Drop the Background
	// here so these rows inherit the terminal's default bg like the rest of
	// the chat surface; the surrounding padding still keeps them visually
	// separated from the message content above.
	var spinnerRendered string
	// The global status row is the single animated spinner for all active phases,
	// including tool_use. Tool headers below use a static running marker; keeping
	// the animation here avoids a cached inline spinner that disappears/reappears
	// whenever viewport content is rebuilt.
	showGlobalSpinner := (a.streamingMessage || a.isCompacting) && a.spinner.IsActive()
	if showGlobalSpinner {
		spinnerView := a.spinner.View(a.animationClock)
		loadingStyle := lipgloss.NewStyle().
			Width(a.width).
			Padding(1, 4, 0, 4)
		spinnerRendered = loadingStyle.Render(spinnerView)
	} else if (a.streamingMessage || a.isCompacting) && a.loadingIndicator.IsActive() {
		loadingText := a.loadingIndicator.ViewWithTime(a.animationClock)
		loadingStyle := lipgloss.NewStyle().
			Width(a.width).
			Padding(1, 4, 0, 4).
			Foreground(lipgloss.Color(th.Accent))
		spinnerRendered = loadingStyle.Render(loadingText)
	}

	// 4. Top separator (light blue line). The task panel is stacked immediately
	// before this separator so it sits above, rather than inside, the input box.
	//
	// topSeparator is a.cachedSeparator.rendered, which was ALREADY produced by
	// a lipgloss Style with Width(a.width) (see above) and is cached per width.
	// Re-Rendering it here made lipgloss re-measure the whole string with
	// ansi.StringWidth -> grapheme clustering on every frame: 2.92s of a 35s
	// scroll profile (8.3% of all CPU) to reproduce a string it already had.
	topSeparatorRendered := topSeparator

	// 4a. Task panel is rendered earlier (before reservedLines) so its height
	// shrinks the message viewport instead of pushing the input box off-screen.
	// taskPanelRendered / taskPanelHeight are already computed above.

	// 4b. Edit mode hint bar
	var editHintRendered string
	if a.editMessageMode {
		// Calculate position and impact
		current := 0
		total := len(a.editMessageUserIdxs)
		for i, idx := range a.editMessageUserIdxs {
			if idx == a.editMessageIdx {
				current = i + 1
				break
			}
		}
		toRemove := len(a.messages) - a.editMessageIdx

		// Build enhanced hint text
		var hintText string
		if toRemove > 1 {
			hintText = tr("classic.chat.edit_mode_remove", current, total, toRemove-1)
		} else {
			hintText = tr("classic.chat.edit_mode_last", current, total)
		}

		hintStyle := lipgloss.NewStyle().
			Width(a.width).
			Padding(0, 2).
			Foreground(lipgloss.Color(th.Warning)).
			Bold(true)
		editHintRendered = hintStyle.Render(hintText)
	}

	// 5. Input line (dynamic height based on content). No bg paint — terminal default.
	inputLineRendered := lipgloss.NewStyle().
		Width(a.width).
		Render(inputLine)

	// 6. Bottom separator (light blue line) - last element, pinned to bottom. No bg paint.
	// Same cached, already-Width(a.width)-rendered string as the top separator.
	// The redundant re-Render cost 2.73s (7.8%) of a 35s scroll profile.
	bottomSeparatorRendered := bottomSeparator
	// When live token data is available, embed the context-usage gauge into
	// the bottom separator (same row — no extra layout height). Cached by
	// width/percent/threshold, so steady-state frames stay cheap.
	if gauge := a.renderBottomSeparatorWithGauge(); gauge != "" {
		bottomSeparatorRendered = gauge
	}

	// inputOverlayY is the 0-indexed line where the input starts in the rendered output.
	// The \n separators between elements terminate lines but do not add extra lines to
	// the count — each element simply occupies countLines(element) rows starting at the
	// current accumulated offset.
	a.inputOverlayY = 0
	if showHeaderDivider {
		a.inputOverlayY += countLines(headerRendered)
		a.inputOverlayY += countLines(dividerRendered)
	}
	a.inputOverlayY += countLines(viewportRendered)
	if spinnerRendered != "" {
		a.inputOverlayY += countLines(spinnerRendered)
	}
	if taskPanelRendered != "" {
		a.inputOverlayY += countLines(taskPanelRendered)
	}
	a.inputOverlayY += countLines(topSeparatorRendered)
	if editHintRendered != "" {
		a.inputOverlayY += countLines(editHintRendered)
	}
	if approvalBarRendered != "" {
		a.inputOverlayY += countLines(approvalBarRendered)
	}
	if questionBarRendered != "" {
		a.inputOverlayY += countLines(questionBarRendered)
	}
	if planQuestionBarRendered != "" {
		a.inputOverlayY += countLines(planQuestionBarRendered)
	}
	if workflowParamBarRendered != "" {
		a.inputOverlayY += countLines(workflowParamBarRendered)
	}
	if queuedMsgRendered != "" {
		a.inputOverlayY += countLines(queuedMsgRendered)
	}
	// Do NOT add inputLineRendered — inputOverlayY is the START of the input row.

	// Sync the input box's screen origin so mouse clicks/drags can be mapped to
	// byte offsets for text selection. The first text column sits after the
	// "> " prompt (2 cells); the first text row is inputOverlayY. Continuation
	// rows are indented by 2 spaces, which matches the same originX.
	a.textInput.SetInputOrigin(2, a.inputOverlayY)

	// Stack everything vertically using strings.Builder for better performance
	var contentBuilder strings.Builder
	estimatedSize := len(headerRendered) + len(dividerRendered) + len(viewportRendered) +
		len(spinnerRendered) + len(topSeparatorRendered) + len(editHintRendered) + len(approvalBarRendered) + len(questionBarRendered) + len(planQuestionBarRendered) + len(workflowParamBarRendered) + len(inputLineRendered) +
		len(bottomSeparatorRendered) + 10
	contentBuilder.Grow(estimatedSize)

	// Header and divider are only rendered when sidebar is visible
	if showHeaderDivider {
		contentBuilder.WriteString(headerRendered)
		contentBuilder.WriteByte('\n')
		contentBuilder.WriteString(dividerRendered)
		contentBuilder.WriteByte('\n')
	}
	contentBuilder.WriteString(viewportRendered)
	// Add spinner line only when active (between viewport and separator)
	if spinnerRendered != "" {
		contentBuilder.WriteByte('\n')
		contentBuilder.WriteString(spinnerRendered)
	}
	writeTaskPanelAboveInput(&contentBuilder, taskPanelRendered, topSeparatorRendered)
	if editHintRendered != "" {
		contentBuilder.WriteByte('\n')
		contentBuilder.WriteString(editHintRendered)
	}
	if approvalBarRendered != "" {
		contentBuilder.WriteByte('\n')
		contentBuilder.WriteString(approvalBarRendered)
	}
	if questionBarRendered != "" {
		contentBuilder.WriteByte('\n')
		contentBuilder.WriteString(questionBarRendered)
	}
	if planQuestionBarRendered != "" {
		contentBuilder.WriteByte('\n')
		contentBuilder.WriteString(planQuestionBarRendered)
	}
	if workflowParamBarRendered != "" {
		contentBuilder.WriteByte('\n')
		contentBuilder.WriteString(workflowParamBarRendered)
	}
	if queuedMsgRendered != "" {
		contentBuilder.WriteByte('\n')
		contentBuilder.WriteString(queuedMsgRendered)
	}
	if !modalActive {
		contentBuilder.WriteByte('\n')
		contentBuilder.WriteString(inputLineRendered)
	}
	// Keep the dock below the input. Its height is reserved above, so changes
	// in the number of rows shrink the message viewport instead of overflowing
	// the terminal or covering the input.
	if bashDockRendered != "" {
		contentBuilder.WriteByte('\n')
		contentBuilder.WriteString(bashDockRendered)
	}
	contentBuilder.WriteByte('\n')
	contentBuilder.WriteString(bottomSeparatorRendered)

	content := contentBuilder.String()

	// Pad to exact height with newlines — avoids the expensive lipgloss.Render pass
	// that would process the entire ~200-line screen through grapheme clustering.
	padded := content
	if lc := strings.Count(content, "\n") + 1; lc < a.height {
		padded += strings.Repeat("\n", a.height-lc)
	}

	// Overlay notifications without shifting layout
	// DISABLED: Notifications are hidden for minimal UI
	// if notif, _ := a.renderNotifications(a.width); notif != "" {
	//	// Slight padding from top/left to avoid touching header border
	//	padded = overlayAt(padded, notif, a.width, a.height, 2, 1, "")
	// }

	// Wrap with bubblezone for complete click detection
	return padded
}

// renderMessageListWithContext renders messages using the provided render context.
func (a *App) renderMessageListWithContext(messages []Message, ctx MessageRenderContext) []string {
	th := a.theme
	width := ctx.Width
	var msgLines []string
	a.lastRenderedImageAnchors = nil
	appendImageAnchors := func(images []nativeImageAnchor, lineBase int) {
		for _, anchor := range images {
			anchor.Line += lineBase
			a.lastRenderedImageAnchors = append(a.lastRenderedImageAnchors, anchor)
		}
	}
	var previewToolStates map[string]*ToolCallState
	if ctx.IsPreview {
		previewToolStates = make(map[string]*ToolCallState)
	}
	defaultCollapseLevel := CollapseLevelCollapsed
	if a.collapseManager != nil {
		defaultCollapseLevel = a.collapseManager.GetDefaultLevel()
	}

	ensurePreviewState := func(msgIdx int, callID, friendlyName, apiName string, sequence int) *ToolCallState {
		if !ctx.IsPreview {
			return nil
		}
		key := makeKey(msgIdx, callID)
		if state, ok := previewToolStates[key]; ok {
			return state
		}
		state := &ToolCallState{
			CallID:        callID,
			ToolName:      friendlyName,
			ToolAPIName:   apiName,
			CollapseLevel: defaultCollapseLevel,
			BlockSequence: sequence,
			MessageIndex:  msgIdx,
		}
		previewToolStates[key] = state
		return state
	}

	for i, msg := range messages {
		actualIdx := ctx.ActualMessageIndex(i)
		if i > 0 {
			msgLines = append(msgLines, reapplyBackground("", th.BG)) // Spacing between messages
		}

		// === CACHE CHECK: Use pre-rendered lines if message hasn't changed ===
		if !msg.IsDirty() {
			if cached := msg.GetPreRenderLines(); cached != nil && len(cached) > 0 {
				lineBase := len(msgLines)
				msgLines = append(msgLines, cached...)
				appendImageAnchors(msg.GetPreRenderImages(), lineBase)
				continue // Skip rendering entirely for clean messages
			}
		}
		// === END CACHE CHECK ===

		// Track where this message's content starts for caching
		msgStartIdx := len(msgLines)

		// Check if this message is focused in navigation mode
		// (only applies to main chat view, preview passes -1 for focusedIdx)
		isFocused := ctx.IsFocused(i)
		isEditTarget := ctx.IsEditTarget(i)
		isDimmed := ctx.IsDimmed(i)

		// User messages: delegated to renderUserMessage (app_chat_render_user.go)
		// which applies the "You" badge highlight and elevated background.
		if msg.Role == "user" {
			block := a.renderUserMessage(msg, ctx, actualIdx, msgStartIdx, isFocused, isEditTarget, isDimmed, len(messages))
			appendImageAnchors(block.Images, len(msgLines))
			msgLines = append(msgLines, block.Lines...)
		} else if msg.Role == "assistant" || msg.Role == string(conversation.RolePeer) {
			// Assistant messages: left-aligned with prominent border
			isPeerMessage := msg.Role == string(conversation.RolePeer)
			isAssistantError := msg.ErrorLineage != nil || strings.HasPrefix(strings.TrimSpace(msg.GetContent()), "Error:")
			borderColor := th.Success
			if isPeerMessage {
				borderColor = th.Primary
			}
			if isAssistantError {
				borderColor = th.Error
			}
			borderStyle := lipgloss.NewStyle().
				Foreground(lipgloss.Color(borderColor)).
				Background(lipgloss.Color(th.BG))

			// Dim assistant messages after the edit point
			if isDimmed {
				borderStyle = lipgloss.NewStyle().
					Foreground(lipgloss.Color(a.theme.TextMuted)).
					Background(lipgloss.Color(th.BG)).
					Strikethrough(true)
			}

			if isPeerMessage {
				peerHandle := "peer"
				peerStatus := ""
				peerTask := ""
				if msg.A2A != nil {
					if strings.TrimSpace(msg.A2A.RemoteAgentHandle) != "" {
						peerHandle = strings.TrimSpace(msg.A2A.RemoteAgentHandle)
					}
					if strings.TrimSpace(msg.A2A.RemoteAgentStatus) != "" {
						peerStatus = strings.TrimSpace(msg.A2A.RemoteAgentStatus)
					}
					if strings.TrimSpace(msg.A2A.RemoteAgentTask) != "" {
						peerTask = strings.TrimSpace(msg.A2A.RemoteAgentTask)
					}
				}
				labelStyle := lipgloss.NewStyle().
					Foreground(lipgloss.Color(th.Primary)).
					Background(lipgloss.Color(th.BG)).
					Bold(true)
				badgeStyle := lipgloss.NewStyle().
					Foreground(lipgloss.Color(th.BG)).
					Background(lipgloss.Color(th.Primary)).
					Padding(0, 1)
				statusStyle := lipgloss.NewStyle().
					Foreground(lipgloss.Color(a.theme.TextMuted)).
					Background(lipgloss.Color(th.BG))

				// Build header: handle + status badge + task (if any)
				headerParts := []string{borderStyle.Render("▌"), " ", labelStyle.Render(peerHandle)}
				if peerStatus != "" {
					statusBadge := lipgloss.NewStyle().Foreground(lipgloss.Color(th.BG)).Background(lipgloss.Color(th.Accent)).Padding(0, 1).Render(peerStatus)
					headerParts = append(headerParts, " ", statusBadge)
				}
				if peerTask != "" {
					taskDisplay := peerTask
					if len(taskDisplay) > 40 {
						taskDisplay = taskDisplay[:37] + "..."
					}
					headerParts = append(headerParts, " ", statusStyle.Render("• "+taskDisplay))
				}
				headerParts = append(headerParts, " ", badgeStyle.Render("peer"))
				msgLines = append(msgLines, reapplyBackground(strings.Join(headerParts, ""), th.BG))
			}

			renderAssistantContent := func(content string, blockSeq int) []string {
				trimmed := strings.TrimSpace(content)
				if isAssistantError && strings.HasPrefix(trimmed, "Error:") {
					wrapped := rawWrap(content, width-2)
					if len(wrapped) == 0 {
						wrapped = []string{""}
					}

					// Assistant prose: keep semantic error color but let the body
					// inherit terminal default fg+bg so the chat renders cleanly on
					// both light and dark terminals (matches Claude Code's approach
					// of not surfacing assistant prose with a painted backdrop).
					errorLabelStyle := lipgloss.NewStyle().
						Foreground(lipgloss.Color(th.Error)).
						Bold(true)
					bodyStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(th.Error))

					out := make([]string, 0, len(wrapped))
					for i, line := range wrapped {
						if i == 0 {
							var leadingSpaces strings.Builder
							for len(line) > 0 && line[0] == ' ' {
								leadingSpaces.WriteString(" ")
								line = line[1:]
							}

							if after, ok := strings.CutPrefix(line, "Error:"); ok {
								rest := strings.TrimSpace(after)
								styled := bodyStyle.Render(leadingSpaces.String()) + errorLabelStyle.Render("Error:")
								if rest != "" {
									styled += " " + bodyStyle.Render(rest)
								}
								out = append(out, styled)
								continue
							}
						}

						out = append(out, bodyStyle.Render(line))
					}
					return out
				}

				// Diffusion denoising reveal: while active for this block,
				// render this frame's partially-resolved noise instead of the
				// final text (the final frame is the real content verbatim).
				content = a.diffusionRevealTransform(actualIdx, blockSeq, content)

				return renderMarkdownWithWrappingVerbose(content, width-2, th, a.showFullToolOutput)
			}

			// Check if this is a bash result — route through the tool registry
			if msg.BashResult != nil {
				output := msg.BashResult.Output
				if msg.BashResult.Error != "" {
					if output != "" {
						output += "\n"
					}
					output += msg.BashResult.Error
				}
				// Build metadata from BashResult fields
				bashMeta := msg.BashResult.Metadata
				if bashMeta == nil {
					bashMeta = make(map[string]any)
				}
				// Ensure exit_code and duration_ms are populated from BashResult fields
				if _, ok := bashMeta["exit_code"]; !ok {
					bashMeta["exit_code"] = msg.BashResult.ExitCode
				}
				if _, ok := bashMeta["duration_ms"]; !ok && msg.BashResult.DurationMs > 0 {
					bashMeta["duration_ms"] = msg.BashResult.DurationMs
				}
				ctx := &toolrender.RenderContext{
					ToolName: "Bash",
					Output:   output,
					Error:    msg.BashResult.Error,
					Params:   map[string]any{"command": msg.BashResult.Command},
					Metadata: bashMeta,
					Width:    width,
					BgColor:  th.BG,
					IsActive: msg.IsLoading,
					ShowFull: a.showFullToolOutput,
				}
				bashLines := a.toolRegistry.Render(ctx, nil)
				// CRITICAL: Ensure all tool output has background color
				for i, line := range bashLines {
					bashLines[i] = reapplyBackground(line, th.BG)
				}
				msgLines = append(msgLines, bashLines...)
				continue
			}

			// Use OrderedBlocks if available (proper interleaved rendering)
			if len(msg.OrderedBlocks) > 0 {
				blocks := make([]MessageBlock, len(msg.OrderedBlocks))
				copy(blocks, msg.OrderedBlocks)
				// Sort by sequence first (arrival order)
				sort.Slice(blocks, func(i, j int) bool {
					return blocks[i].Sequence < blocks[j].Sequence
				})

				// Pair tool_results with their tool_calls so each result renders
				// immediately after its call. Without this, all calls render first
				// (seq 1,2,3) then all results (seq 4,5,6) because that's arrival order.
				blocks = reorderBlocksForDisplay(blocks)

				// Create dimmed style if needed
				var dimStyle *lipgloss.Style
				if isDimmed {
					s := lipgloss.NewStyle().
						Foreground(lipgloss.Color(a.theme.TextMuted)).
						Faint(true)
					dimStyle = &s
				}

				// Pre-compute which tool_calls have FINAL results to avoid O(n^2) scanning.
				// Streaming results (IsStreaming=true) are partial output from a still-running
				// tool and should NOT count as "has result" for spinner/running state.
				toolResultMap := make(map[string]bool, len(blocks)/2)
				toolErrorMap := make(map[string]bool, len(blocks)/2)
				for _, b := range blocks {
					if b.Type == "tool_result" && b.ToolResult != nil && !b.ToolResult.IsStreaming {
						toolResultMap[b.ToolResult.CallID] = true
						toolErrorMap[b.ToolResult.CallID] = toolResultFailed(b.ToolResult)
					}
				}

				// Pre-compute tool aggregation for non-verbose mode.
				// Aggregates consecutive Read/ReadLegacy, Grep, and Edit/EditLegacy tool calls
				// into a single summary line like "Read 3 files" or "Grep pattern 'foo'".
				type aggregationGroup struct {
					category       string
					count          int
					filePaths      []string
					pattern        string
					includePattern string
					hasRunning     bool
					leaderID       string
					memberIDs      map[string]bool
				}
				aggregationMap := make(map[string]*aggregationGroup) // tool_call ID -> group
				aggregatedResultsRendered := make(map[string]bool)   // which results have been hidden
				var currentGroup *aggregationGroup

				if !a.showFullToolOutput {
					for i := 0; i < len(blocks); i++ {
						b := blocks[i]
						if b.Type != "tool_call" || b.ToolCall == nil {
							// Non-tool-call block breaks the chain
							currentGroup = nil
							continue
						}

						tc := b.ToolCall
						category := GetToolCategory(tc.Name)

						// Only aggregate read, grep, edit tools
						if category == "" {
							currentGroup = nil
							continue
						}

						hasResult := toolResultMap[tc.ID]
						isRunning := ctx.IsActiveMessage && !hasResult

						// Check if tool has error (look ahead for result)
						hasError := false
						for j := i + 1; j < len(blocks); j++ {
							if blocks[j].Type == "tool_result" && blocks[j].ToolResult != nil {
								if blocks[j].ToolResult.CallID == tc.ID && blocks[j].ToolResult.Error != "" {
									hasError = true
									break
								}
							}
						}

						// Tools with errors should not be aggregated
						if hasError {
							currentGroup = nil
							continue
						}

						// Start new group if category changed or no current group
						if currentGroup == nil || currentGroup.category != category {
							currentGroup = &aggregationGroup{
								category:  category,
								leaderID:  tc.ID,
								memberIDs: make(map[string]bool),
							}
							aggregationMap[tc.ID] = currentGroup
						}

						currentGroup.memberIDs[tc.ID] = true
						currentGroup.count++

						if isRunning {
							currentGroup.hasRunning = true
						}

						// Extract parameters based on tool type
						switch category {
						case "read", "edit":
							if fp := ExtractFileParam(tc.Parameters); fp != "" {
								currentGroup.filePaths = append(currentGroup.filePaths, fp)
							}
						case "grep":
							pattern, include := ExtractGrepParams(tc.Parameters)
							if pattern != "" {
								currentGroup.pattern = pattern
							}
							if include != "" {
								currentGroup.includePattern = include
							}
						}
					}
				}

				// Keep sub-agent progress lines in transcript order. The old table
				// view is intentionally not used, but compact progress remains useful.
				var subAgentActivities []*SubAgentDisplay
				for _, b := range blocks {
					if b.Type == "sub_agent_activity" && b.SubAgentActivity != nil {
						subAgentActivities = append(subAgentActivities, b.SubAgentActivity)
					}
				}

				// Track the previous hook block so we can insert a visual separator between
				// hook groups (e.g. between a previous tool's post-hooks and the next tool's
				// pre-hooks). A "hook group" is defined by (Phase, ToolCallID).
				var prevHookPhase string
				var prevHookToolCallID string
				// Diffusion block holdback: while this message's text is still
				// denoising, blocks that arrived after the animating content
				// block (tool_call / tool_result / later content) must not
				// render yet — otherwise a tool call pops in below text that is
				// still resolving. Hold back everything past the animating
				// block's sequence; the tick handler marks the message dirty on
				// reveal expiry, so suppressed blocks appear the moment it ends.
				diffusionHoldbackActive := a.diffusionRevealActiveFor(actualIdx)
				diffusionHoldbackSeq := a.diffusionReveal.blockSeq
				for _, block := range blocks {
					// Suppress blocks sequenced after the still-denoising content
					// block. The animating block itself (and anything before it)
					// renders normally.
					if diffusionHoldbackActive && block.Sequence > diffusionHoldbackSeq {
						continue
					}
					// Reset hook tracking when we hit a non-hook block — the separator
					// only applies between two consecutive hook blocks across different groups.
					if block.Type != "hook_execution" {
						prevHookPhase = ""
						prevHookToolCallID = ""
					}
					switch block.Type {
					case "thinking":
						if a.showThinking {
							// Thinking is the lowest-emphasis content — let it inherit
							// the terminal's default bg so it blends in cleanly on both
							// light and dark terminals (matches Claude Code's behavior
							// where assistant prose isn't painted with a surface color).
							// Only the muted fg + italic carries the style.
							thinkingStyle := lipgloss.NewStyle().
								Foreground(lipgloss.Color(a.theme.TextMuted)).
								Italic(true)
							if isDimmed {
								thinkingStyle = thinkingStyle.Faint(true)
							}
							// Only add a blank separator before ◆ Thinking when there is
							// already content above it — avoids the message starting with
							// an empty line when thinking is the very first block.
							if len(msgLines) > 0 {
								msgLines = append(msgLines, "")
							}
							msgLines = append(msgLines, thinkingStyle.Render(tr("classic.chat.thinking")))
							thinkingWrapped := rawWrap(block.GetContent(), width-4)
							for _, line := range thinkingWrapped {
								msgLines = append(msgLines, "    "+thinkingStyle.Render(line))
							}
						}

					case "content":
						renderedLines := renderAssistantContent(block.GetContent(), block.Sequence)
						// Strip any leading empty lines that the markdown renderer may inject,
						// preventing a double blank gap after a thinking block.
						for len(renderedLines) > 0 && strings.TrimSpace(strings.ReplaceAll(renderedLines[0], "\x1b[0m", "")) == "" {
							renderedLines = renderedLines[1:]
						}

						// Apply background highlight if focused — focus is the only
						// state that surfaces assistant content with a painted bg.
						if isFocused {
							highlightStyle := lipgloss.NewStyle().Background(lipgloss.Color(th.BGLight))
							for i, line := range renderedLines {
								renderedLines[i] = highlightStyle.Render(line)
							}
						}

						// Apply dimming if needed
						if isDimmed && dimStyle != nil {
							for i, line := range renderedLines {
								renderedLines[i] = dimStyle.Render(line)
							}
						}

						// Unfocused/undimmed assistant prose: just left-pad. No bg
						// repaint so terminal default shows through. When focused,
						// preserve the highlight bg via reapplyBackground.
						for _, line := range renderedLines {
							if isFocused {
								msgLines = append(msgLines, reapplyBackground("  "+line, th.BGLight))
							} else {
								msgLines = append(msgLines, "  "+line)
							}
						}

					case "sub_agent_activity":
						if block.SubAgentActivity == nil || a.subAgentRenderer == nil {
							continue
						}
						sa := block.SubAgentActivity
						saIdx := -1
						for i, activity := range subAgentActivities {
							if activity == sa {
								saIdx = i
								break
							}
						}
						resultMap := make(map[string]bool, len(sa.Blocks)/2)
						for _, subBlock := range sa.Blocks {
							if subBlock.Type == "tool_result" && subBlock.ToolResult != nil {
								resultMap[subBlock.ToolResult.CallID] = true
							}
						}
						hasPendingTools := false
						for _, subBlock := range sa.Blocks {
							if subBlock.Type == "tool_call" && subBlock.ToolCall != nil &&
								!resultMap[subBlock.ToolCall.ID] {
								hasPendingTools = true
								break
							}
						}
						config := a.subAgentRenderer.GetConfig()
						config.ShowThinking = a.showThinking
						a.subAgentRenderer.SetConfig(config)
						a.subAgentRenderer.SetWidth(width)
						saLines := a.subAgentRenderer.RenderSubAgentWithPosition(
							sa,
							ctx.IsActiveMessage && !sa.IsComplete,
							hasPendingTools,
							a.showFullToolOutput,
							"●",
							a.animationClock.Frame(),
							saIdx < 0 || saIdx == len(subAgentActivities)-1,
						)
						for i := range saLines {
							saLines[i] = reapplyBackground(saLines[i], th.BG)
						}
						msgLines = append(msgLines, saLines...)

					case "tool_call":
						if block.ToolCall != nil {
							tc := block.ToolCall
							dimToolStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(th.TextDim)).Background(lipgloss.Color(th.BG))

							// Get friendly tool name (resolves mcp_server_tool to server:tool)
							friendlyName := a.toolNameResolver.Resolve(tc.Name)

							// Check if this tool call is part of an aggregation
							aggGroup, inAggregation := aggregationMap[tc.ID]
							isAggregationLeader := inAggregation && aggGroup.leaderID == tc.ID

							// If part of aggregation but not the leader, skip rendering this tool call
							if inAggregation && !isAggregationLeader {
								continue
							}

							// Register tool call with collapse manager if not already registered
							var state *ToolCallState
							if ctx.IsPreview {
								state = ensurePreviewState(actualIdx, tc.ID, friendlyName, tc.Name, block.Sequence)
							} else if a.collapseManager != nil {
								a.collapseManager.RegisterToolCall(actualIdx, tc.ID, friendlyName, tc.Name, block.Sequence)
								state = a.collapseManager.GetState(actualIdx, tc.ID)
							}
							if state != nil {
								state.HasError = toolErrorMap[tc.ID]
							}

							hasResult := toolResultMap[tc.ID]

							// Only show spinner for the LAST tool_call without a result,
							// since tools execute sequentially. Earlier tool_calls without
							// results have completed but their result block hasn't arrived yet.
							isLastToolWithoutResult := false
							if !hasResult && ctx.IsActiveMessage {
								isLastToolWithoutResult = true
								foundSelf := false
								for _, scanBlock := range blocks {
									if scanBlock.Type == "tool_call" && scanBlock.ToolCall != nil {
										if scanBlock.ToolCall.ID == tc.ID {
											foundSelf = true
											continue
										}
										if foundSelf && !toolResultMap[scanBlock.ToolCall.ID] {
											// A later tool also has no result - we're not the last
											isLastToolWithoutResult = false
											break
										}
									}
								}
							}

							// Use consistent bullet - spinner only for actively running tools
							var bullet string
							isRunning := ctx.IsActiveMessage && !hasResult && isLastToolWithoutResult
							if isRunning {
								// Static marker only: the animated spinner lives in the global
								// status row outside the viewport cache.
								bullet = "●"
							} else {
								// Show completion status for finished tools
								bullet = "•"
							}

							// Render tool header - either aggregated or individual
							var toolLine string
							if inAggregation {
								// Render aggregated summary (works for single or multiple tools)
								colors := DefaultToolColors()
								agg := &AggregatedToolCall{
									ToolCategory:   aggGroup.category,
									Count:          aggGroup.count,
									FilePaths:      aggGroup.filePaths,
									Pattern:        aggGroup.pattern,
									IncludePattern: aggGroup.includePattern,
									HasRunningTool: aggGroup.hasRunning,
								}
								toolLine = bullet + " " + RenderAggregatedToolSummary(agg, width, colors, aggGroup.hasRunning)
							} else {
								// Render individual tool header (non-aggregatable tools like Bash)
								toolLine = a.collapseWidget.RenderToolHeader(state, bullet, tc.Parameters, isRunning, a.showFullToolOutput)
							}
							// Add blank separator before tool call if previous line is non-blank
							if n := len(msgLines); n > 0 {
								last := strings.TrimSpace(strings.ReplaceAll(msgLines[n-1], "\x1b[0m", ""))
								if last != "" {
									msgLines = append(msgLines, reapplyBackground("", th.BG))
								}
							}
							indentStyle := lipgloss.NewStyle().Background(lipgloss.Color(th.BG))
							// Tool activity reads in transcript order from the left.
							// Right alignment disconnected calls from their output.
							fullLine := "  " + toolLine
							msgLines = append(msgLines, reapplyBackground(fullLine, th.BG))

							// Show "Ctrl+B to background" hint for running Bash commands
							// (Live output is rendered by the bash tool renderer in the tool_result block)
							if isBashToolName(tc.Name) && isRunning {
								hintLine := a.collapseWidget.RenderBashBackgroundHint()
								hintIndent := indentStyle.Render("    ")
								msgLines = append(msgLines, reapplyBackground(hintIndent+hintLine, th.BG))
							}

							// Show full parameters if verbose mode and level is full
							if a.showFullToolOutput && len(tc.Parameters) > 1 && (state == nil || state.CollapseLevel == CollapseLevelFull) {
								for key, val := range tc.Parameters {
									paramLine := fmt.Sprintf("%s → %v", key, val)
									paramIndent := indentStyle.Render("    ")
									fullParamLine := paramIndent + dimToolStyle.Render("  "+paramLine)
									msgLines = append(msgLines, reapplyBackground(fullParamLine, th.BG))
								}
							}
						}

					case "tool_result":
						if block.ToolResult != nil {
							tr := block.ToolResult

							// Skip result if it's part of an aggregation
							// (all results are hidden, the summary line replaces them)
							if _, inAggregation := aggregationMap[tr.CallID]; inAggregation {
								aggregatedResultsRendered[tr.CallID] = true
								continue
							}
							// Use cached ToolName if available (set during streaming or history load),
							// fall back to OrderedBlocks scan only if not set
							toolName := tr.ToolName
							if toolName == "" {
								for _, prevBlock := range msg.OrderedBlocks {
									if prevBlock.Type == "tool_call" && prevBlock.ToolCall != nil {
										if prevBlock.ToolCall.ID == tr.CallID {
											toolName = prevBlock.ToolCall.Name
											tr.ToolName = toolName // Cache for future renders
											break
										}
									}
								}
							}

							friendlyName := ""
							if toolName != "" {
								friendlyName = a.toolNameResolver.Resolve(toolName)
							}

							// Get collapse state for this tool
							var state *ToolCallState
							if ctx.IsPreview {
								state = ensurePreviewState(actualIdx, tr.CallID, friendlyName, toolName, block.Sequence)
							} else if a.collapseManager != nil {
								state = a.collapseManager.GetState(actualIdx, tr.CallID)
							}

							// Update collapse manager with result metadata
							// Use strings.Count to avoid allocation from Split
							trOutput := tr.GetOutput()
							outputLines := strings.Count(trOutput, "\n") + 1
							hasError := tr.Error != ""
							if ctx.IsPreview {
								if state != nil {
									state.OutputLines = outputLines
									state.HasError = hasError
								}
							} else if a.collapseManager != nil {
								a.collapseManager.UpdateToolResult(actualIdx, tr.CallID, outputLines, hasError, time.Time{}, time.Time{})
							}

							// Check for sub-agent activity (for Task tool compact rendering)
							hasSubAgentActivity := false
							for _, b := range msg.OrderedBlocks {
								if b.Type == "sub_agent_activity" {
									hasSubAgentActivity = true
									break
								}
							}

							// Look up tool parameters from the tool_call block
							var toolParams map[string]any
							for _, prevBlock := range msg.OrderedBlocks {
								if prevBlock.Type == "tool_call" && prevBlock.ToolCall != nil {
									if prevBlock.ToolCall.ID == tr.CallID {
										toolParams = prevBlock.ToolCall.Parameters
										break
									}
								}
							}

							// Unified rendering via tool registry
							// NOTE: isActive is always false for tool_result blocks because the result
							// has already arrived. "running..." should only show for tool_call blocks
							// that are waiting for their result.
							resultBlock := a.renderToolResultUnified(toolName, tr, toolParams, &msg, width, false, hasSubAgentActivity)
							resultLines := resultBlock.Lines
							// CRITICAL: Ensure all tool output has background color to prevent "grey gaps"
							for i, line := range resultLines {
								resultLines[i] = "  " + reapplyBackground(line, th.BG)
							}
							appendImageAnchors(resultBlock.Images, len(msgLines))
							msgLines = append(msgLines, resultLines...)
							// Add blank separator after tool result if there was content
							if len(resultLines) > 0 {
								msgLines = append(msgLines, reapplyBackground("", th.BG))
							}
						}

					case "hook_execution":
						// Skip hook rendering if ShowHooks is disabled
						if !a.renderSettings.ShowHooks {
							continue
						}
						if block.HookExecution != nil {
							he := block.HookExecution
							// Insert a blank separator line when entering a new hook group
							// (different ToolCallID or different Phase), including the first
							// group after non-hook content. This visually separates one
							// tool's hooks from the next tool's hooks. De-dupe against an
							// existing trailing blank line to avoid double blanks.
							isNewGroup := prevHookToolCallID != he.ToolCallID || prevHookPhase != he.Phase
							if isNewGroup {
								if n := len(msgLines); n > 0 {
									last := strings.TrimSpace(strings.ReplaceAll(msgLines[n-1], "\x1b[0m", ""))
									if last != "" {
										msgLines = append(msgLines, reapplyBackground("", th.BG))
									}
								}
							}
							prevHookPhase = he.Phase
							prevHookToolCallID = he.ToolCallID
							hookStyle := lipgloss.NewStyle().
								Foreground(lipgloss.Color(a.theme.TextMuted)).
								Background(lipgloss.Color(th.BG))
							successStyle := lipgloss.NewStyle().
								Foreground(lipgloss.Color(a.theme.TextMuted)).
								Background(lipgloss.Color(th.BG))
							errorHookStyle := lipgloss.NewStyle().
								Foreground(lipgloss.Color(th.Error)).
								Background(lipgloss.Color(th.BG))

							icon := "✓"
							if !he.Success || he.Blocked {
								icon = "✗"
							}

							phaseStr := "pre"
							if he.Phase == "after" {
								phaseStr = "post"
							}

							hookLine := fmt.Sprintf("%s [%s-hook] %s", icon, phaseStr, he.HookName)

							if he.Blocked {
								msgLines = append(msgLines, reapplyBackground("  "+errorHookStyle.Render(hookLine+tr("classic.hook.blocked_suffix")), th.BG))
								if he.Error != "" {
									msgLines = append(msgLines, reapplyBackground("    "+errorHookStyle.Render("  "+he.Error), th.BG))
								}
							} else if !he.Success {
								msgLines = append(msgLines, reapplyBackground("  "+errorHookStyle.Render(hookLine), th.BG))
								if he.Error != "" {
									msgLines = append(msgLines, reapplyBackground("    "+errorHookStyle.Render("  "+he.Error), th.BG))
								}
							} else {
								msgLines = append(msgLines, reapplyBackground("  "+hookStyle.Render(hookLine), th.BG))
								if he.Output != "" {
									msgLines = append(msgLines, reapplyBackground("    "+successStyle.Render("  "+he.Output), th.BG))
								}
							}
						}
					}
				}
			} else if msg.IsPreRendered {
				// Pre-rendered workflow content: output directly without re-processing
				// The content already contains lipgloss-styled boxes and ANSI codes
				preLines := strings.Split(msg.Content, "\n")
				// Apply background to pre-rendered content
				for i, line := range preLines {
					preLines[i] = reapplyBackground(line, th.BG)
				}
				msgLines = append(msgLines, preLines...)
			} else {
				// Fallback: old rendering for messages without OrderedBlocks
				if a.showThinking && msg.GetThinking() != "" {
					// Thinking inherits terminal default bg — see the OrderedBlocks
					// branch above for rationale. Muted fg + italic only.
					thinkingStyle := lipgloss.NewStyle().
						Foreground(lipgloss.Color(a.theme.TextMuted)).
						Italic(true)
					if len(msgLines) > 0 {
						msgLines = append(msgLines, "")
					}
					msgLines = append(msgLines, thinkingStyle.Render(tr("classic.chat.thinking")))
					thinkingWrapped := rawWrap(msg.GetThinking(), width-4)
					for _, line := range thinkingWrapped {
						msgLines = append(msgLines, "    "+thinkingStyle.Render(line))
					}
				}

				renderedLines := renderAssistantContent(msg.GetContent(), diffusionRevealAnyBlock)
				// Strip any leading empty lines that the markdown renderer may inject.
				for len(renderedLines) > 0 && strings.TrimSpace(strings.ReplaceAll(renderedLines[0], "\x1b[0m", "")) == "" {
					renderedLines = renderedLines[1:]
				}

				// Apply background highlight if focused
				if isFocused {
					highlightStyle := lipgloss.NewStyle().Background(lipgloss.Color(th.BGLight))
					for i, line := range renderedLines {
						renderedLines[i] = highlightStyle.Render(line)
					}
				}

				for idx, line := range renderedLines {
					if idx == 0 {
						msgLines = append(msgLines, reapplyBackground("  "+line, th.BGLight))
					} else {
						msgLines = append(msgLines, reapplyBackground("  "+line, th.BGLight))
					}
				}

				for _, tc := range msg.ToolCalls {
					level := CollapseLevelCollapsed
					if a.showFullToolOutput {
						level = CollapseLevelFull
					}
					state := &ToolCallState{
						CallID:        tc.ID,
						ToolName:      a.toolNameResolver.Resolve(tc.Name),
						ToolAPIName:   tc.Name,
						CollapseLevel: level,
					}
					for i := range msg.ToolResults {
						if msg.ToolResults[i].CallID == tc.ID {
							state.HasError = toolResultFailed(&msg.ToolResults[i])
							break
						}
					}
					toolLine := a.collapseWidget.RenderToolHeader(
						state, "•", tc.Parameters, false, a.showFullToolOutput,
					)
					msgLines = append(msgLines, reapplyBackground("", th.BG))
					msgLines = append(msgLines, reapplyBackground("  "+toolLine, th.BG))
				}

				for _, tr := range msg.ToolResults {
					// Look up tool name and params from ToolCalls
					var toolName string
					var toolParams map[string]any
					for _, tc := range msg.ToolCalls {
						if tc.ID == tr.CallID {
							toolName = tc.Name
							toolParams = tc.Parameters
							break
						}
					}

					// Unified rendering via tool registry
					resultBlock := a.renderToolResultUnified(toolName, &tr, toolParams, &msg, width, false, false)
					resultLines := resultBlock.Lines
					// CRITICAL: Ensure all tool output has background color
					for i, line := range resultLines {
						resultLines[i] = "  " + reapplyBackground(line, th.BG)
					}
					appendImageAnchors(resultBlock.Images, len(msgLines))
					msgLines = append(msgLines, resultLines...)
				}
			}

			if a.wordPlayback.Active && a.wordPlayback.MessageIdx == actualIdx {
				if playbackLine := a.wordPlaybackLine(width); playbackLine != "" {
					msgLines = append(msgLines, playbackLine)
				}
			}

			// Render lineage diagnostics panel for failed assistant responses.
			if lineageLines := a.renderErrorLineagePanel(&msg, width); len(lineageLines) > 0 {
				for i, line := range lineageLines {
					lineageLines[i] = reapplyBackground(line, th.BG)
				}
				msgLines = append(msgLines, lineageLines...)
			}
		} else if msg.Role == "system" {
			// NEW: Section-based system message rendering
			if len(msg.Sections) > 0 {
				// Each section renders with its own colour + character count;
				// content is shown raw (see renderSystemSectionLines).
				for _, section := range msg.Sections {
					msgLines = append(msgLines, a.renderSystemSectionLines(section, width)...)
				}
			} else if msg.IsPreRendered {
				// Pre-rendered workflow content (mode switches, workflow headers, etc.)
				// Collapsed to a single line by default; expand with Ctrl+O (verbose mode)
				preLines := strings.Split(msg.Content, "\n")
				// Count non-empty lines to decide whether to collapse
				nonEmptyCount := 0
				for _, pl := range preLines {
					if strings.TrimSpace(pl) != "" {
						nonEmptyCount++
					}
				}
				separatorStyle := lipgloss.NewStyle().
					Foreground(lipgloss.Color(th.Border)).
					Background(lipgloss.Color(th.BG))
				separator := strings.Repeat("─", width-4)
				if !a.showFullToolOutput && nonEmptyCount > 3 {
					// Collapsed: show a compact one-liner summary
					collapsedStyle := lipgloss.NewStyle().
						Foreground(lipgloss.Color(a.theme.TextMuted)).
						Background(lipgloss.Color(th.BG)).
						Italic(true)
					hintStyle := lipgloss.NewStyle().
						Foreground(lipgloss.Color(th.Border)).
						Background(lipgloss.Color(th.BG))
					borderSt := lipgloss.NewStyle().
						Foreground(lipgloss.Color(th.Border)).
						Background(lipgloss.Color(th.BG))
					collapsedLine := borderSt.Render("  ") + " " +
						collapsedStyle.Render(tr("classic.chat.system_context", nonEmptyCount)) + "  " +
						hintStyle.Render(tr("classic.common.expand_hint"))
					msgLines = append(msgLines, reapplyBackground(collapsedLine, th.BG))
				} else {
					// Expanded: output pre-rendered content directly
					for i, line := range preLines {
						preLines[i] = reapplyBackground(line, th.BG)
					}
					msgLines = append(msgLines, preLines...)
					// Separator after content
					msgLines = append(msgLines, reapplyBackground("", th.BG))
					msgLines = append(msgLines, reapplyBackground("  "+separatorStyle.Render(separator), th.BG))
					msgLines = append(msgLines, reapplyBackground("", th.BG))
				}
			} else {
				// Plain system messages: collapsed by default, expanded in verbose mode
				systemStyle := lipgloss.NewStyle().
					Foreground(lipgloss.Color(a.theme.TextMuted)).
					Background(lipgloss.Color(th.BG)).
					Italic(true)

				borderStyle := lipgloss.NewStyle().
					Foreground(lipgloss.Color(th.Border)).
					Background(lipgloss.Color(th.BG))

				wrapped := rawWrap(msg.Content, width-8)
				separatorStyle := lipgloss.NewStyle().
					Foreground(lipgloss.Color(th.Border)).
					Background(lipgloss.Color(th.BG))
				separator := strings.Repeat("─", width-4)
				if !a.showFullToolOutput && len(wrapped) > 3 {
					// Collapsed: single summary line with expand hint
					hintStyle := lipgloss.NewStyle().
						Foreground(lipgloss.Color(th.Border)).
						Background(lipgloss.Color(th.BG))
					collapsedLine := borderStyle.Render("  ") + " " +
						systemStyle.Render(tr("classic.chat.system_prompt", len(wrapped))) + "  " +
						hintStyle.Render(tr("classic.common.expand_hint"))
					msgLines = append(msgLines, reapplyBackground(collapsedLine, th.BG))
				} else {
					// Expanded (verbose mode or short message ≤3 lines): show full content
					for idx, line := range wrapped {
						if idx == 0 {
							firstLine := borderStyle.Render("  ") + " " + systemStyle.Render(line)
							msgLines = append(msgLines, reapplyBackground(firstLine, th.BG))
						} else {
							restLine := "    " + systemStyle.Render(line)
							msgLines = append(msgLines, reapplyBackground(restLine, th.BG))
						}
					}
					// Separator after full content
					msgLines = append(msgLines, reapplyBackground("", th.BG))
					sepLine := "  " + separatorStyle.Render(separator)
					msgLines = append(msgLines, reapplyBackground(sepLine, th.BG))
					msgLines = append(msgLines, reapplyBackground("", th.BG))
				}
			}
		} else if msg.Role == "tool" {
			// Tool result messages — unified rendering via registry
			for _, tr := range msg.ToolResults {
				// Look up tool name and params from previous assistant message
				var toolName string
				var toolParams map[string]any
				if i > 0 {
					for j := i - 1; j >= 0; j-- {
						if messages[j].Role == "assistant" && len(messages[j].ToolCalls) > 0 {
							for _, tc := range messages[j].ToolCalls {
								if tc.ID == tr.CallID {
									toolName = tc.Name
									toolParams = tc.Parameters
									break
								}
							}
							if toolName == "" && len(messages[j].ToolCalls) > 0 {
								toolName = messages[j].ToolCalls[0].Name
								toolParams = messages[j].ToolCalls[0].Parameters
							}
							break
						}
					}
				}

				// Unified rendering via tool registry
				resultBlock := a.renderToolResultUnified(toolName, &tr, toolParams, &msg, width, false, false)
				resultLines := resultBlock.Lines
				// CRITICAL: Ensure all tool output has background color
				for i, line := range resultLines {
					resultLines[i] = "  " + reapplyBackground(line, th.BG)
				}
				appendImageAnchors(resultBlock.Images, len(msgLines))
				msgLines = append(msgLines, resultLines...)
			}

			// === CACHE STORAGE: Store rendered lines and clear dirty flag ===
			if msgStartIdx < len(msgLines) {
				msg.SetPreRenderLines(msgLines[msgStartIdx:])
			}
			msg.ClearDirty()
			// === END CACHE STORAGE ===
		}
	}

	return msgLines
}
func reorderBlocksForDisplay(blocks []MessageBlock) []MessageBlock {

	// Phase 1: Local thinking hoist.
	//
	// Context:
	//   - In STREAMING mode the SDK emits ThinkingUpdate BEFORE ContentUpdate for each
	//     turn, so thinking blocks naturally have a lower sequence number than their
	//     associated content. The input (already sorted by sequence by the caller) puts
	//     thinking in the correct position — no reordering needed.
	//   - In NON-STREAMING mode (chain fallback, non-streaming providers) the SDK emits
	//     ContentUpdate BEFORE ThinkingUpdate (executeLoop calls content callback first).
	//     Sorting by sequence would place content before thinking, which is wrong.
	//
	// Algorithm: scan through the sequence-sorted blocks. When a thinking block is found
	// immediately after one or more content blocks (with no tool_call/tool_result between
	// them), insert the thinking block BEFORE those content blocks.
	//
	// This is a LOCAL hoist (per-turn), not a GLOBAL hoist (all thinking to front).
	// Global hoisting breaks multi-turn execution where each turn has its own thinking
	// block interleaved with that turn's tools and content.
	phase1 := make([]MessageBlock, 0, len(blocks))
	for _, b := range blocks {
		if b.Type != "thinking" {
			phase1 = append(phase1, b)
			continue
		}
		// Thinking block: find how many consecutive content blocks trail phase1.
		// If any exist (no tool_call/tool_result separating them), hoist thinking
		// to before those content blocks.
		insertAt := len(phase1)
		for insertAt > 0 && phase1[insertAt-1].Type == "content" {
			insertAt--
		}
		// Insert thinking at insertAt (before any trailing content blocks).
		phase1 = append(phase1, MessageBlock{}) // grow by one slot
		copy(phase1[insertAt+1:], phase1[insertAt:])
		phase1[insertAt] = b
	}

	// Phase 2 removed: tool_result pairing no longer needed since sequences
	// are correctly assigned end-to-end (pre-hooks → tool_call → tool_result → post-hooks).
	// Pairing was placing tool_result before pre-hooks, breaking the visual order.
	// Phase 3 also removed for the same reason.
	return phase1
}
