package chat

import (
	"context"
	"encoding/base64"
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/conversation"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/tools/ii"
)

// conversationsPageSize is the UI page stride — when the selected row is
// within this many rows of the end of the loaded list, we fetch another page.
const conversationsPageSize = 10

// conversationsChunkSize is the number of metadata-only conversations requested
// from the SDK per page. It's intentionally larger than the UI page size so
// client-side branch filtering rarely produces an empty UI page and forces a
// second fetch. Metadata rows are ~1 KB, so 50 per chunk is cheap.
const conversationsChunkSize = 50

func (a *App) openConversation(idx int) {
	if idx < 0 || idx >= len(a.conversations) {
		logDebug("[openConversation] Invalid index: %d (have %d conversations)", idx, len(a.conversations))
		return
	}

	logDebug("[openConversation] ========== OPENING CONVERSATION ==========")
	logDebug("[openConversation] Opening conversation at index %d", idx)

	// CRITICAL: Save tasks for the CURRENT conversation BEFORE switching
	// The TodoManager is a global singleton. If we don't save now,
	// the previous conversation's tasks will be lost.
	if a.currentConvID != "" && a.sdk != nil {
		if err := a.sdk.SaveTasksForConversation(a.currentConvID); err != nil {
			logDebug("[openConversation] Failed to save tasks for previous conv %s: %v", a.currentConvID, err)
		} else {
			logDebug("[openConversation] Saved tasks for previous conversation %s", a.currentConvID)
		}
	}

	// Save current chat state
	a.saveCurrentChatState()

	// Switch to new conversation
	a.activeConv = &a.conversations[idx]
	a.screen = ScreenChat
	a.setCurrentConversationID(a.activeConv.ID)

	logDebug("[openConversation] Set activeConv=%s, currentConvID=%s", a.activeConv.ID, a.currentConvID)

	// Reset UI state for clean load
	a.streamingMessage = false
	a.streamingInProgress = false
	a.streamBuffer = ""
	a.streamingInputTokens = 0
	a.streamingOutputChars = 0
	a.setUserScrolledAway(false, "load_messages_reset")
	a.isCompacting = false
	// A loaded transcript replaces the empty-chat intro just like sending the
	// first message does. Otherwise the settled logo keeps breathing and
	// marks this entire history viewport dirty on every animation frame.
	a.dismissIntro()
	a.loadingIndicator.Stop(a.animationClock) // Unsubscribe from animation clock
	if a.spinner != nil {
		a.spinner.Stop(a.animationClock)
	}
	a.tokenCount = 0 // Reset token count before loading new conversation

	// CRITICAL: Reset sidebar/preview cache state to prevent stale data from persisting
	// These caches can hold data from the previously opened conversation
	a.cachedPreviewID = ""                     // Clear preview message cache
	a.cachedPreviewMsgs = nil                  // Clear preview messages list
	a.collapsedParents = make(map[string]bool) // Reset fork/compaction collapsed state
	a.scrollOffset = 0                         // Reset sidebar scroll position
	a.selectedIdx = 0                          // Reset sidebar selection index

	// PHASE 3: Estimate system prompt tokens for initial context bar display
	systemPromptTokens := a.getSystemPromptEstimate()
	a.tokenCount = systemPromptTokens
	a.tokenCountIsEstimate = true // Mark as estimate until real tokens arrive
	logDebug("[openConversation] Estimated system prompt: %d tokens", systemPromptTokens)

	// Load messages from SDK storage
	a.loadMessagesFromSDK(a.activeConv.ID)
	logDebug("[openConversation] Loaded %d messages from SDK", len(a.messages))

	// Add system prompt to message history
	// This ensures the system prompt is visible in the chat context
	a.addSystemPromptToHistory()

	// Load token count from conversation's CurrentContextSize (input_tokens from last API call)
	if a.sdk != nil && a.currentConvID != "" {
		ctx := context.Background()
		if conv, err := a.sdk.ResumeConversation(ctx, a.currentConvID); err == nil {
			if conv.CurrentContextSize > 0 {
				prevCount := a.tokenCount
				a.tokenCount = conv.CurrentContextSize
				a.tokenCountIsEstimate = false                 // PHASE 4: Mark as real, not estimate
				a.lastRealTokenCount = conv.CurrentContextSize // PHASE 4: Track real value
				a.recordTokenChange(prevCount, a.tokenCount, "conversation_load", fmt.Sprintf("convID=%s", a.currentConvID[:8]))
				logDebug("[openConversation] Loaded tokenCount=%d from CurrentContextSize (REAL)", a.tokenCount)
			} else {
				logDebug("[openConversation] No CurrentContextSize available, using estimate")
			}
		}

		// Refresh context window from current model config
		a.modelContextWindow = a.sdk.GetModelContextWindow()

		// Log to debug screen
	}

	// Clear stale task state from previous conversation
	if tm := ii.GetTodoManager(); tm != nil {
		tm.ClearTodos()
	}

	// Restore persisted tasks for this conversation and wire auto-persistence
	if a.sdk != nil && a.currentConvID != "" {
		if err := a.sdk.SetupTaskPersistence(a.currentConvID); err != nil {
			logDebug("[openConversation] Task persistence setup failed: %v", err)
		} else {
			taskCount := 0
			if tm := ii.GetTodoManager(); tm != nil {
				taskCount = tm.Count()
			}
			logDebug("[openConversation] Task persistence ready: %d tasks restored for conv=%s", taskCount, a.currentConvID)
		}
	}

	// LOG AUTO-COMPACTION STATE FOR THIS CONVERSATION
	logDebug("[openConversation] ---------- AUTO-COMPACTION STATE ----------")
	logDebug("[openConversation] tokenCount: %d", a.tokenCount)
	logDebug("[openConversation] modelContextWindow: %d", a.modelContextWindow)
	if a.modelContextWindow > 0 {
		logDebug("[openConversation] usage percentage: %.2f%%", float64(a.tokenCount)/float64(a.modelContextWindow)*100)
	}
	if a.settingsManager != nil {
		compactionSettings := a.settingsManager.GetCompactionSettings()
		if compactionSettings != nil {
			enabled := compactionSettings.GetEnableAutoCompaction()
			threshold := compactionSettings.GetAutoCompactionThresholdPercent()
			logDebug("[openConversation] EnableAutoCompaction: %v", enabled)
			logDebug("[openConversation] ThresholdPercent: %.4f (%.1f%%)", threshold, threshold*100)
			if a.modelContextWindow > 0 && threshold > 0 {
				thresholdTokens := int(float64(a.modelContextWindow) * threshold)
				logDebug("[openConversation] thresholdTokens: %d", thresholdTokens)
				logDebug("[openConversation] WILL COMPACT ON NEXT MSG: %v", a.tokenCount >= thresholdTokens)
			}
		} else {
			logDebug("[openConversation] compactionSettings is nil!")
		}
	} else {
		logDebug("[openConversation] settingsManager is nil!")
	}
	logDebug("[openConversation] ============================================")

	// Load the conversation's saved input state
	a.loadChatState(a.activeConv)

	// Check if there's a background agent for this conversation
	if a.bgManager != nil && a.currentConvID != "" {
		isRunning := a.bgManager.IsRunning(a.currentConvID)
		logDebug("[openConversation] bgManager.IsRunning(%s) = %v", a.currentConvID, isRunning)

		if isRunning {
			logDebug("[openConversation] Reconnecting to running agent for %s", a.currentConvID)

			// Restore running state
			a.streamingMessage = true
			a.setActivityPhase(ActivityPhaseThinking, "")
			a.loadingIndicator.Start(a.animationClock) // Subscribe to shared animation clock

			// Subscribe to updates - process only HookExecutionUpdate from buffer
			// to avoid duplicating content updates that are already in SDK storage
			uiChan, buffered := a.bgManager.Subscribe(a.currentConvID, "ui")
			logDebug("[openConversation] Subscribe returned: uiChan=%v, buffered=%d", uiChan != nil, len(buffered))
			// Process buffered hook updates (transient, not stored in SDK)
			for _, update := range buffered {
				if update.UpdateType() == "hook_execution" {
					a.queueAgentIntermediateUpdate(update)
				}
			}

			// Start listening for new updates going forward
			if uiChan != nil {
				go a.listenForAgentUpdates(uiChan)
			}
		} else {
			// Not running - just clean up any stale bgManager entry
			// SDK storage is the source of truth for messages, don't overwrite
			_, _, _, completed := a.bgManager.GetCompletionResult(a.currentConvID)
			if completed {
				logDebug("[openConversation] Cleaning up completed agent record for %s", a.currentConvID)
				a.bgManager.Remove(a.currentConvID)
			}

			// Ensure idle state for non-running conversation
			a.setActivityPhase(ActivityPhaseIdle, "")
			logDebug("[openConversation] Set idle state for non-running conversation")
		}
	} else {
		logDebug("[openConversation] No bgManager or empty convID, setting idle state")
		a.setActivityPhase(ActivityPhaseIdle, "")
	}

	// Update viewport and scroll to bottom
	a.updateViewportContent()
	a.msgViewport.GotoBottom()

	logDebug("[openConversation] Completed opening conversation %s (status=%s, messages=%d)",
		a.currentConvID, a.activeConv.Status, len(a.messages))

	// If in workspace mode, update chat pane
	if a.workspaceMode {
		panes := a.getAllPanes()
		for _, p := range panes {
			if p.Type == PaneChat {
				p.ConvID = a.activeConv.ID
				p.Focused = true
				a.focusedPane = p
				// Unfocus others
				for _, other := range panes {
					if other != p {
						other.Focused = false
					}
				}
				break
			}
		}
	}
}

func (a *App) openConversationByID(convID string) bool {
	convID = strings.TrimSpace(convID)
	if convID == "" {
		return false
	}

	a.loadConversationsFromSDK()
	for idx := range a.conversations {
		if a.conversations[idx].ID != convID {
			continue
		}
		if a.workspaceMode {
			a.openConversationInWorkspace(idx)
		} else {
			a.openConversation(idx)
		}
		return true
	}

	if a.sdk == nil {
		return false
	}

	ctx := context.Background()
	sdkConv, err := a.sdk.ResumeConversation(ctx, convID)
	if err != nil {
		return false
	}

	// Derive a title for this conversation — same priority as extractConversationTitle:
	// 1. persisted Title field, 2. custom_title metadata, 3. first user message.
	title := ""
	if sdkConv.Title != "" && sdkConv.Title != "New Chat" {
		title = decodeJSONHTMLEscapes(sdkConv.Title)
	} else if sdkConv.Metadata.Custom != nil {
		if ct, ok := sdkConv.Metadata.Custom["custom_title"].(string); ok && ct != "" {
			title = ct
		}
	}
	if title == "" {
		for _, msg := range sdkConv.Messages {
			if msg.Role == conversation.RoleUser && msg.Content != "" {
				if extracted := extractCleanTitle(msg.Content); extracted != "" {
					if len(extracted) > 60 {
						extracted = extracted[:57] + "..."
					}
					title = extracted
					break
				}
			}
		}
	}
	if title == "" {
		title = fmt.Sprintf("Chat %s", sdkConv.ID[:8])
	}

	fallback := Conversation{
		ID:           sdkConv.ID,
		Title:        title,
		Preview:      getConversationPreview(sdkConv),
		Recap:        recapFromSummary(sdkConv),
		Status:       "idle",
		LastMessage:  sdkConv.UpdatedAt,
		MessageCount: len(sdkConv.Messages),
		TotalTokens:  contextTokens(sdkConv.CurrentContextSize, sdkConv.TotalTokens),
	}
	a.conversations = append([]Conversation{fallback}, a.conversations...)
	if a.workspaceMode {
		a.openConversationInWorkspace(0)
	} else {
		a.openConversation(0)
	}
	return true
}

// conversationsLoadedMsg fires for the INITIAL page load (offset 0). It resets
// the sidebar state and replaces a.conversations.
type conversationsLoadedMsg struct {
	conversations []*conversation.Conversation // metadata-only
	err           error
}

// conversationsNextPageMsg fires for subsequent pages. It APPENDS to the list.
type conversationsNextPageMsg struct {
	conversations []*conversation.Conversation // metadata-only
	offset        int                          // storage offset this page was fetched from
	err           error
}

// contextTokens returns the best available token count for display.
// currentContextSize (input_tokens from the last API response) is authoritative
// because it reflects the actual context window size. totalTokens is a raw sum
// of per-message token counts and under-counts the real context (it misses the
// system prompt, tool definitions, and other overhead).  This mirrors the logic
// in GetConversationTokens.
func contextTokens(currentContextSize, totalTokens int) int {
	if currentContextSize > 0 {
		return currentContextSize
	}
	return totalTokens
}

// loadConversationsAsync fires the INITIAL page fetch (offset 0). It sets the
// loading flag, subscribes to the animation clock so the spinner animates, and
// kicks off a goroutine that calls the metadata-only SDK path. Result lands as
// conversationsLoadedMsg in updateQueue so the Update loop is never blocked.
// Callers MUST return the animation cmd from Update() or the spinner freezes.
func (a *App) loadConversationsAsync() tea.Cmd {
	if a.sdk == nil {
		if a.debugScreen != nil {
			a.debugScreen.AddLog("[CONV] SDK is nil, cannot load conversations")
		}
		return nil
	}
	a.conversationsLoading = true
	a.storageOffset = 0
	a.conversationsHasMore = false

	animCmd := a.animationClock.Subscribe()

	sdk := a.sdk
	queue := a.updateQueue
	workspacePath := a.sdk.ProjectRoot()
	go func() {
		ctx := context.Background()
		convs, err := sdk.ListConversationsPage(ctx, workspacePath, 0, conversationsChunkSize)
		queue <- conversationsLoadedMsg{conversations: convs, err: err}
	}()

	return animCmd
}

// loadConversationsFromSDK is the synchronous-reload path used after forks,
// compactions, and workflow completion. It fetches page zero directly and
// replaces a.conversations in place. The TUI is already showing a different
// screen or a loading state is not critical, so the animation clock is not
// subscribed.
func (a *App) loadConversationsFromSDK() {
	if a.sdk == nil {
		if a.debugScreen != nil {
			a.debugScreen.AddLog("[CONV] SDK is nil, cannot load conversations")
		}
		return
	}

	ctx := context.Background()
	sdkConvs, err := a.sdk.ListConversationsPage(ctx, a.sdk.ProjectRoot(), 0, conversationsChunkSize)
	if err != nil {
		msg := fmt.Sprintf("[CONV] Failed to reload conversations: %v", err)
		logDebug("%s", msg)
		if a.debugScreen != nil {
			a.debugScreen.AddLog(msg)
		}
		return
	}

	a.applyConversationsFirstPage(sdkConvs)
}

// applyConversationsFirstPage replaces a.conversations with the converted
// metadata rows from the first storage page. Called from both the sync reload
// and the async initial-load paths once their data arrives.
func (a *App) applyConversationsFirstPage(sdkConvs []*conversation.Conversation) {
	currentWorkspace := a.sdk.ProjectRoot()
	msg := fmt.Sprintf("[CONV] Workspace: %s, first-page conversations: %d", currentWorkspace, len(sdkConvs))
	logDebug("%s", msg)
	if a.debugScreen != nil {
		a.debugScreen.AddLog(msg)
	}

	// Reset sidebar UI cache when conversation list is refreshed.
	a.cachedPreviewID = ""
	a.cachedPreviewMsgs = nil
	a.collapsedParents = make(map[string]bool)
	a.scrollOffset = 0
	a.selectedIdx = 0

	a.conversations = a.convertPage(sdkConvs, currentWorkspace)
	a.storageOffset = len(sdkConvs)
	a.conversationsHasMore = len(sdkConvs) >= conversationsChunkSize
	a.loadSelectedHistoryPreview()

	logDebug("[CONV] First page applied: ui=%d stored=%d hasMore=%v",
		len(a.conversations), a.storageOffset, a.conversationsHasMore)

	a.conversationsLoading = false
	a.animationClock.Unsubscribe()
}

// appendConversationsPage appends converted metadata rows from a later storage
// page to a.conversations. Duplicate IDs are skipped (defensive — storage
// should not return duplicates, but a concurrent Save could in theory race).
func (a *App) appendConversationsPage(sdkConvs []*conversation.Conversation, pageOffset int) {
	if len(sdkConvs) == 0 {
		a.conversationsHasMore = false
		return
	}

	// Ignore stale pages — if the user has reset/reloaded, pageOffset won't
	// line up with storageOffset and appending would duplicate.
	if pageOffset != a.storageOffset-len(sdkConvs) && pageOffset != 0 {
		logDebug("[CONV] Dropping stale page offset=%d storageOffset=%d", pageOffset, a.storageOffset)
		return
	}

	seen := make(map[string]struct{}, len(a.conversations))
	for _, c := range a.conversations {
		seen[c.ID] = struct{}{}
	}

	currentWorkspace := a.sdk.ProjectRoot()
	appended := 0
	for _, sdkConv := range sdkConvs {
		if _, dup := seen[sdkConv.ID]; dup {
			continue
		}
		uiConv := a.convertSDKConversation(sdkConv, currentWorkspace)
		if uiConv != nil {
			a.conversations = append(a.conversations, *uiConv)
			appended++
		}
	}

	a.conversationsHasMore = len(sdkConvs) >= conversationsChunkSize
	logDebug("[CONV] Appended page: +%d ui_total=%d stored_total=%d hasMore=%v",
		appended, len(a.conversations), a.storageOffset, a.conversationsHasMore)
}

// convertPage turns a slice of metadata-only SDK conversations into UI rows,
// dropping any the client-side filter rejects (workspace mismatch, branch
// filter). Used for both initial and subsequent pages.
func (a *App) convertPage(sdkConvs []*conversation.Conversation, currentWorkspace string) []Conversation {
	out := make([]Conversation, 0, len(sdkConvs))
	for _, sdkConv := range sdkConvs {
		if ui := a.convertSDKConversation(sdkConv, currentWorkspace); ui != nil {
			out = append(out, *ui)
		}
	}
	return out
}

// checkAndLoadNextPage fires a background fetch for the next storage page when
// the user scrolls near the end of the currently loaded list. Returns true if
// a fetch was kicked off (the UI won't see new rows until conversationsNextPageMsg
// arrives). Safe to call repeatedly — concurrent fetches are guarded by
// conversationsPageLoading.
func (a *App) checkAndLoadNextPage() bool {
	if a.sdk == nil || !a.conversationsHasMore || a.conversationsPageLoading {
		return false
	}

	// Trigger when the selected row is within conversationsPageSize of the end.
	threshold := len(a.conversations) - conversationsPageSize
	if threshold < 0 {
		threshold = 0
	}
	if a.selectedIdx < threshold {
		return false
	}

	a.conversationsPageLoading = true
	offset := a.storageOffset
	a.storageOffset += conversationsChunkSize // tentatively advance; rolled back on error

	sdk := a.sdk
	queue := a.updateQueue
	workspacePath := a.sdk.ProjectRoot()
	go func() {
		ctx := context.Background()
		convs, err := sdk.ListConversationsPage(ctx, workspacePath, offset, conversationsChunkSize)
		queue <- conversationsNextPageMsg{conversations: convs, offset: offset, err: err}
	}()

	logDebug("[CONV] Next-page fetch started offset=%d chunk=%d", offset, conversationsChunkSize)
	return true
}

// convertSDKConversation converts a single SDK conversation to UI conversation.
// Returns nil if conversation should be filtered out (workspace/branch mismatch).
func (a *App) convertSDKConversation(sdkConv *conversation.Conversation, currentWorkspace string) *Conversation {
	// Resolve workspace path
	convWorkspace := sdkConv.WorkspacePath
	if convWorkspace == "" && sdkConv.Metadata.Custom != nil {
		if wsPath, ok := sdkConv.Metadata.Custom["workspace_path"].(string); ok {
			convWorkspace = wsPath
		}
	}

	// Safety-net workspace check
	if convWorkspace != "" && !isWorkspaceCompatible(convWorkspace, currentWorkspace) {
		return nil
	}

	// Extract git branch from metadata
	convBranch := ""
	if sdkConv.Metadata.Custom != nil {
		if branch, ok := sdkConv.Metadata.Custom["git_branch"].(string); ok {
			convBranch = branch
		}
	}

	// Apply branch filter
	if a.branchFilter == FilterCurrentBranch {
		currentBranch, _ := a.gitHelper.CurrentBranch()
		if convBranch != currentBranch {
			return nil
		}
	} else if a.branchFilter == FilterNoBranch {
		if convBranch != "" {
			return nil
		}
	}

	// Prefer the persisted summary (cheap; attached by the metadata-only
	// loader) over scanning Messages, which is empty under ExcludeMessages
	// anyway. Legacy conversations without a summary fall back to the full
	// scan loop below.
	totalTokens := 0
	inputTokens := 0
	outputTokens := 0
	lastModel := ""
	toolCallCount := 0
	messageCount := 0
	var modelsUsed []string

	if sdkConv.Summary != nil {
		s := sdkConv.Summary
		messageCount = s.MessageCount
		inputTokens = s.InputTokens
		outputTokens = s.OutputTokens
		totalTokens = s.InputTokens + s.OutputTokens
		toolCallCount = s.ToolCallCount
		lastModel = s.LastModel
		if len(s.ModelsUsed) > 0 {
			modelsUsed = append([]string(nil), s.ModelsUsed...)
		}
	} else {
		// Legacy path: summary was never persisted on disk. Messages is
		// probably empty too (metadata-only load), so this yields zeros —
		// good enough; the row will populate on next save when the SDK
		// builds a summary.
		modelSeen := make(map[string]bool)
		for _, msg := range sdkConv.Messages {
			if msg.Tokens != nil {
				totalTokens += msg.Tokens.Total
				inputTokens += msg.Tokens.Input
				outputTokens += msg.Tokens.Output
			}
			if msg.Model != "" {
				lastModel = msg.Model
				if !modelSeen[msg.Model] {
					modelSeen[msg.Model] = true
					modelsUsed = append(modelsUsed, msg.Model)
				}
			}
			toolCallCount += len(msg.ToolCalls)
		}
		messageCount = len(sdkConv.Messages)
	}

	// Extract lineage metadata
	forkedFrom, compactedFrom := "", ""
	forkPoint, compactionCount := 0, 0
	if sdkConv.Metadata.Custom != nil {
		if ff, ok := sdkConv.Metadata.Custom["forked_from"].(string); ok {
			forkedFrom = ff
		}
		if fp, ok := sdkConv.Metadata.Custom["fork_point"].(float64); ok {
			forkPoint = int(fp)
		}
		if cf, ok := sdkConv.Metadata.Custom["compacted_from"].(string); ok {
			compactedFrom = cf
		}
		if cc, ok := sdkConv.Metadata.Custom["compaction_count"].(float64); ok {
			compactionCount = int(cc)
		}
	}

	uiConv := Conversation{
		ID:              sdkConv.ID,
		Title:           fmt.Sprintf("Chat %s", sdkConv.ID[:8]),
		Preview:         getConversationPreview(sdkConv),
		FirstUserPrompt: firstPromptFromSummary(sdkConv),
		Status:          "idle",
		LastMessage:     sdkConv.UpdatedAt,
		MessageCount:    messageCount,
		TotalTokens:     contextTokens(sdkConv.CurrentContextSize, totalTokens),
		InputTokens:     inputTokens,
		OutputTokens:    outputTokens,
		Branch:          convBranch,
		Model:           lastModel,
		ModelsUsed:      modelsUsed,
		CostUSD:         sdkConv.TotalCostUSD,
		WallTime:        sdkConv.WallTime,
		ForkedFrom:      forkedFrom,
		ForkPoint:       forkPoint,
		CompactedFrom:   compactedFrom,
		CompactionCount: compactionCount,
		ToolCallCount:   toolCallCount,
	}

	// Extract title
	uiConv.Title = a.extractConversationTitle(sdkConv)

	// Surface the LLM recap (if one has been generated) for the history menu.
	uiConv.Recap = recapFromSummary(sdkConv)

	return &uiConv
}

// recapFromSummary returns the persisted LLM recap for a conversation, or "" if
// none has been generated yet. Guards the nil Summary on legacy conversations.
func recapFromSummary(sdkConv *conversation.Conversation) string {
	if sdkConv == nil || sdkConv.Summary == nil {
		return ""
	}
	return sdkConv.Summary.Recap
}

func firstPromptFromSummary(sdkConv *conversation.Conversation) string {
	if sdkConv == nil || sdkConv.Summary == nil {
		return ""
	}
	return strings.TrimSpace(sdkConv.Summary.FirstUserPrompt)
}

// extractConversationTitle extracts the title from conversation metadata or first user message.
func (a *App) extractConversationTitle(sdkConv *conversation.Conversation) string {
	// 1. Persisted Title field — set by the AI naming agent via SetConversationTitle.
	//    This is the highest-priority source and must be checked before everything else.
	if sdkConv.Title != "" && sdkConv.Title != "New Chat" {
		return decodeJSONHTMLEscapes(sdkConv.Title)
	}

	// 2. Legacy custom_title metadata key (older sessions).
	if sdkConv.Metadata.Custom != nil {
		if customTitle, ok := sdkConv.Metadata.Custom["custom_title"].(string); ok && customTitle != "" {
			return customTitle
		}
	}

	// 3. Persisted first-user-prompt from compaction summary — cheap and works
	// under metadata-only loads where Messages is empty.
	if sdkConv.Summary != nil && sdkConv.Summary.FirstUserPrompt != "" {
		if title := extractCleanTitle(sdkConv.Summary.FirstUserPrompt); title != "" {
			if len(title) > 60 {
				title = title[:57] + "..."
			}
			return title
		}
	}

	// 4. Fall back to first meaningful user message (legacy, or full-load path).
	for _, msg := range sdkConv.Messages {
		if msg.Role == conversation.RoleUser && msg.Content != "" {
			title := extractCleanTitle(msg.Content)
			if title == "" {
				continue // Skip messages that are only system tags
			}
			if len(title) > 60 {
				title = title[:57] + "..."
			}
			return title
		}
	}

	// 5. Default: date-based fallback.
	return fmt.Sprintf("Chat %s", sdkConv.ID[:8])
}

// extractCleanTitle strips XML-like tags (system-reminder, etc.) from user message
// content and returns the first meaningful line as a title.
// It first decodes JSON unicode escapes (\u003c → <) produced by the pooled
// FirstMessagePreview loader so the tag-stripping logic works correctly.
func extractCleanTitle(content string) string {
	// Decode JSON HTML-safe unicode escapes before any other processing.
	// The pooled metadata loader returns raw JSON bytes where < > & " are
	// encoded as \u003c \u003e \u0026 \u0022 — they must be decoded before we
	// can strip <system-reminder ...> tags.
	text := decodeJSONHTMLEscapes(content)

	// Strip all XML-like tags and their content: <tag>...</tag>
	for {
		openIdx := strings.Index(text, "<")
		if openIdx == -1 {
			break
		}
		// Find matching close bracket
		closeIdx := strings.Index(text[openIdx:], ">")
		if closeIdx == -1 {
			break
		}
		closeIdx += openIdx

		// Get tag name
		tagContent := text[openIdx+1 : closeIdx]
		fields := strings.Fields(tagContent)
		if len(fields) == 0 {
			text = text[:openIdx] + text[closeIdx+1:]
			continue
		}
		tagName := fields[0]
		tagName = strings.TrimPrefix(tagName, "/")

		// Check for closing tag </tagName>
		endTag := "</" + tagName + ">"
		endIdx := strings.Index(text, endTag)
		if endIdx != -1 && endIdx > openIdx {
			// Remove the entire <tag>...</tag> block
			text = text[:openIdx] + text[endIdx+len(endTag):]
		} else {
			// No closing tag — for known system-injected block tags, treat
			// everything after the opening tag as noise (the content is the
			// reminder itself, not the user's actual message).
			if strings.HasPrefix(tagName, "system-") || tagName == "context" {
				text = text[:openIdx]
				break
			}
			// Otherwise just strip the orphaned tag.
			text = text[:openIdx] + text[closeIdx+1:]
		}
	}

	// Clean up whitespace
	text = strings.TrimSpace(text)
	if text == "" {
		return ""
	}

	// Take first non-empty line
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			return line
		}
	}
	return ""
}

// decodeJSONHTMLEscapes replaces JSON HTML-safe unicode escape sequences
// (\u003c, \u003e, \u0026, \u0022, \u0027) with their actual characters.
// This is needed because Go's json.Marshal HTML-encodes these characters by
// default, and the pooled metadata loader (FirstMessagePreview) returns the
// raw JSON bytes without going through json.Unmarshal.
var jsonHTMLEscapeReplacer = strings.NewReplacer(
	`\u003c`, "<",
	`\u003C`, "<",
	`\u003e`, ">",
	`\u003E`, ">",
	`\u0026`, "&",
	`\u0022`, `"`,
	`\u0027`, "'",
	`\/`, "/",
	`\n`, "\n",
	`\r`, "\r",
	`\t`, "\t",
)

func decodeJSONHTMLEscapes(s string) string {
	if !strings.Contains(s, `\u`) && !strings.Contains(s, `\n`) &&
		!strings.Contains(s, `\r`) && !strings.Contains(s, `\t`) &&
		!strings.Contains(s, `\/`) {
		return s // fast path — nothing to decode
	}
	return jsonHTMLEscapeReplacer.Replace(s)
}

func imageAttachmentsFromConversationContent(content []conversation.ContentBlock) []Attachment {
	var attachments []Attachment
	for contentIndex, block := range content {
		if !strings.Contains(strings.ToLower(block.Type), "image") ||
			!strings.HasPrefix(strings.ToLower(block.MimeType), "image/") ||
			len(block.Data) == 0 {
			continue
		}
		name := block.Name
		if name == "" {
			ext := strings.TrimPrefix(strings.ToLower(block.MimeType), "image/")
			if ext == "jpeg" {
				ext = "jpg"
			}
			name = fmt.Sprintf("content_image_%d.%s", contentIndex+1, ext)
		}
		attachments = append(attachments, Attachment{
			FileName: name,
			MimeType: block.MimeType,
			Content:  append([]byte(nil), block.Data...),
			Size:     int64(len(block.Data)),
		})
	}
	return attachments
}

// loadMessagesFromSDK loads messages for a conversation from SDK
func (a *App) loadMessagesFromSDK(convID string) {
	logDebug("[loadMessagesFromSDK] Loading messages for convID=%s", convID)

	if a.sdk == nil {
		logDebug("[loadMessagesFromSDK] SDK is nil, cannot load messages")
		return
	}

	if convID == "" {
		logDebug("[loadMessagesFromSDK] convID is empty, cannot load messages")
		return
	}

	ctx := context.Background()
	sdkMsgs, err := a.sdk.GetMessages(ctx, convID)
	if err != nil {
		logDebug("[loadMessagesFromSDK] Failed to load messages for %s: %v", convID, err)
		return
	}

	logDebug("[loadMessagesFromSDK] Got %d messages from SDK for %s", len(sdkMsgs), convID)

	// Convert SDK messages to UI messages
	a.messages = []Message{}
	for _, sdkMsg := range sdkMsgs {
		if isGeneratedCompactionMessage(sdkMsg) {
			// Mirrors convertSDKMessages (app_sdk_helpers.go): render as a
			// collapsed-by-default "system" message instead of dropping it, so
			// the actual post-compaction handoff is inspectable rather than a
			// silent blind spot. Built via convertCompactionHandoffMessage
			// directly (skipping tool-call/ordered-block reconstruction below,
			// which compaction handoff messages never carry) then appended,
			// matching the shape convertSDKMessages produces.
			a.messages = append(a.messages, *convertCompactionHandoffMessage(sdkMsg))
			continue
		}
		// Skip system-role messages entirely. The Anthropic API only accepts
		// "user" and "assistant" in messages[]; system is a top-level field.
		// Historic task-nudge messages (type=task_nudge*) were incorrectly
		// persisted as RoleSystem — filter them out so they don't render as
		// phantom assistant blocks in the chat view. Compaction handoff
		// messages are also Role=="system" but were already appended and
		// `continue`d above, so they never reach this filter.
		if sdkMsg.Role == "system" {
			continue
		}

		uiMsg := Message{
			Role:      string(sdkMsg.Role),
			Content:   sdkMsg.Content,
			Timestamp: sdkMsg.Timestamp,
			Metadata:  sdkMsg.Metadata,
			A2A:       sdkMsg.A2A,
		}

		// Convert tool calls
		if len(sdkMsg.ToolCalls) > 0 {
			uiMsg.ToolCalls = make([]ToolCallDisplay, len(sdkMsg.ToolCalls))
			for i, tc := range sdkMsg.ToolCalls {
				uiMsg.ToolCalls[i] = ToolCallDisplay{
					ID:         tc.ID,
					Name:       tc.Name,
					Parameters: tc.Parameters,
				}
			}
		}

		// Convert tool results
		if len(sdkMsg.ToolResults) > 0 {
			uiMsg.ToolResults = make([]ToolResultDisplay, len(sdkMsg.ToolResults))
			for i, tr := range sdkMsg.ToolResults {
				errorMsg := ""
				if tr.Error != nil {
					errorMsg = tr.Error.Message
				}
				uiMsg.ToolResults[i] = ToolResultDisplay{
					CallID:      tr.CallID,
					Output:      tr.Output,
					Error:       errorMsg,
					Attachments: imageAttachmentsFromConversationContent(tr.Content),
				}
			}
		}

		// Get thinking content (direct field takes precedence over metadata)
		if sdkMsg.Thinking != "" {
			uiMsg.Thinking = sdkMsg.Thinking
		} else if sdkMsg.Metadata != nil {
			// Fallback: check metadata for thinking
			if thinking, ok := sdkMsg.Metadata["thinking"].(string); ok {
				uiMsg.Thinking = thinking
			}
		}

		// Restore images from metadata["images"] (user-pasted images)
		// Images are stored as []map[string]interface{} with "type", "media_type", "data" fields
		if sdkMsg.Metadata != nil {
			if imagesRaw, ok := sdkMsg.Metadata["images"]; ok {
				if imagesSlice, ok := imagesRaw.([]interface{}); ok {
					for _, img := range imagesSlice {
						if imgMap, ok := img.(map[string]interface{}); ok {
							// Extract base64 data
							dataStr, _ := imgMap["data"].(string)
							mediaType, _ := imgMap["media_type"].(string)
							if mediaType == "" {
								mediaType = "image/png" // Default fallback
							}
							// Decode base64 to bytes
							if dataStr != "" {
								decoded, err := base64.StdEncoding.DecodeString(dataStr)
								if err == nil {
									uiMsg.Images = append(uiMsg.Images, ImageAttachment{
										Data:     decoded,
										MimeType: mediaType,
									})
								}
							}
						}
					}
				}
			}
		}

		// Reconstruct OrderedBlocks for consistent rendering
		// This ensures loaded conversations render the same as streaming ones
		if sdkMsg.Role == "assistant" || sdkMsg.Role == conversation.RolePeer {
			seq := 0
			uiMsg.OrderedBlocks = []MessageBlock{}

			// Add thinking block if present
			if uiMsg.Thinking != "" {
				seq++
				uiMsg.OrderedBlocks = append(uiMsg.OrderedBlocks, MessageBlock{
					Type:     "thinking",
					Content:  uiMsg.Thinking,
					Sequence: seq,
				})
			}

			// Interleave tool calls and results with content
			// We need to reconstruct the chronological order
			// Assumption: tool calls/results come before content in the final message
			for _, tc := range uiMsg.ToolCalls {
				seq++
				uiMsg.OrderedBlocks = append(uiMsg.OrderedBlocks, MessageBlock{
					Type: "tool_call",
					ToolCall: &ToolCallDisplay{
						ID:         tc.ID,
						Name:       tc.Name,
						Parameters: tc.Parameters,
					},
					Sequence: seq,
				})

				// Find matching result
				for j := range uiMsg.ToolResults {
					tr := &uiMsg.ToolResults[j]
					if tr.CallID == tc.ID {
						// Set ToolName from the matching tool call so the renderer
						// doesn't have to scan OrderedBlocks to find it every frame
						tr.ToolName = tc.Name
						seq++
						uiMsg.OrderedBlocks = append(uiMsg.OrderedBlocks, MessageBlock{
							Type:       "tool_result",
							ToolResult: tr,
							Sequence:   seq,
						})
						break
					}
				}
			}

			// Add content block last
			if uiMsg.Content != "" {
				seq++
				uiMsg.OrderedBlocks = append(uiMsg.OrderedBlocks, MessageBlock{
					Type:     "content",
					Content:  uiMsg.Content,
					Sequence: seq,
				})
			}
		}

		a.messages = append(a.messages, uiMsg)
	}

	// Pre-process tool results for efficient cached rendering.
	// During streaming this happens in real-time, but for loaded history we
	// must do it here so the renderer uses the fast cached path instead of
	// re-parsing output on every frame.
	if a.toolRegistry != nil {
		for i := range a.messages {
			msg := &a.messages[i]
			if msg.Role != "assistant" {
				continue
			}
			for _, block := range msg.OrderedBlocks {
				if block.Type != "tool_result" || block.ToolResult == nil {
					continue
				}
				tr := block.ToolResult
				if tr.Error != "" || tr.Output == "" {
					continue
				}
				// Find matching tool call for parameters
				var toolParams map[string]interface{}
				for _, b := range msg.OrderedBlocks {
					if b.Type == "tool_call" && b.ToolCall != nil && b.ToolCall.ID == tr.CallID {
						toolParams = b.ToolCall.Parameters
						break
					}
				}
				a.processToolResultForRendering(msg, tr.CallID, tr.ToolName, toolParams, tr.Output)
			}
		}
	}

	// Update token count from conversation
	if conv, err := a.sdk.ResumeConversation(ctx, convID); err == nil {
		a.tokenCount = contextTokens(conv.CurrentContextSize, conv.TotalTokens)
		logDebug("[loadMessagesFromSDK] Updated token count: %d (currentContextSize=%d totalTokens=%d)",
			a.tokenCount, conv.CurrentContextSize, conv.TotalTokens)
	}

	logDebug("[loadMessagesFromSDK] Loaded %d messages for conversation %s", len(a.messages), convID)
	// Note: caller (openConversation) handles viewport update
}

// updateFocusedMessageFromScroll updates focusedMessageIdx based on current scroll position
// This allows message highlighting to follow as the user scrolls line-by-line
func (a *App) updateFocusedMessageFromScroll() {
	if len(a.messageLinePositions) == 0 || len(a.messages) == 0 {
		return
	}

	// Calculate the center line of the viewport
	currentOffset := a.msgViewport.YOffset
	viewportHeight := a.msgViewport.Height
	centerLine := currentOffset + (viewportHeight / 2)

	// Find which message contains the center line
	// Prefer the message that has the most overlap with the viewport
	bestMatch := a.focusedMessageIdx // Keep current if no better match
	bestOverlap := 0

	visibleTop := currentOffset
	visibleBottom := currentOffset + viewportHeight

	for _, pos := range a.messageLinePositions {
		// Calculate overlap between message and viewport
		overlapStart := pos.StartLine
		if overlapStart < visibleTop {
			overlapStart = visibleTop
		}

		overlapEnd := pos.EndLine
		if overlapEnd > visibleBottom {
			overlapEnd = visibleBottom
		}

		overlap := overlapEnd - overlapStart
		if overlap > bestOverlap {
			bestOverlap = overlap
			bestMatch = pos.MessageIdx
		}

		// Also check if message contains the center line (tiebreaker)
		if centerLine >= pos.StartLine && centerLine <= pos.EndLine {
			if overlap == bestOverlap {
				bestMatch = pos.MessageIdx
			}
		}
	}

	// Only update if changed to avoid unnecessary re-renders
	if bestMatch != a.focusedMessageIdx {
		a.focusedMessageIdx = bestMatch
		logDebug("[updateFocusedMessageFromScroll] Focus changed to message %d (center line: %d)", bestMatch, centerLine)
	}
}

// findNextAssistantMessage returns the index of the next assistant message after currentIdx
func (a *App) findNextAssistantMessage(currentIdx int) int {
	if len(a.messages) == 0 {
		return currentIdx
	}

	for i := currentIdx + 1; i < len(a.messages); i++ {
		if a.messages[i].Role == "assistant" {
			return i
		}
	}
	return currentIdx // Stay at current if no next found
}

// findPreviousAssistantMessage returns the index of the previous assistant message before currentIdx
func (a *App) findPreviousAssistantMessage(currentIdx int) int {
	if len(a.messages) == 0 {
		return currentIdx
	}

	for i := currentIdx - 1; i >= 0; i-- {
		if a.messages[i].Role == "assistant" {
			return i
		}
	}
	return currentIdx // Stay at current if no previous found
}

// scrollToFocusedMessage scrolls the viewport to show the focused message
func (a *App) scrollToFocusedMessage() {
	if len(a.messageLinePositions) == 0 {
		return
	}

	for _, pos := range a.messageLinePositions {
		if pos.MessageIdx == a.focusedMessageIdx {
			// Scroll to show this message at the top with some padding
			targetOffset := pos.StartLine - 2
			if targetOffset < 0 {
				targetOffset = 0
			}
			a.msgViewport.SetYOffset(targetOffset)
			return
		}
	}
}

// ============================================================================
// EDIT MESSAGE MODE (conversation branching)
// ============================================================================

// enterEditMessageMode enters the mode where user selects a message to edit
func (a *App) enterEditMessageMode() {
	// Build list of user message indices
	a.editMessageUserIdxs = nil
	for i, msg := range a.messages {
		if msg.Role == "user" {
			a.editMessageUserIdxs = append(a.editMessageUserIdxs, i)
		}
	}

	if len(a.editMessageUserIdxs) == 0 {
		a.addNotification("info", "No user messages to edit")
		return
	}

	a.editMessageMode = true
	// Start at the last (most recent) user message
	a.editMessageIdx = a.editMessageUserIdxs[len(a.editMessageUserIdxs)-1]

	// Scroll to show the selected message
	a.scrollToEditMessage()
	a.invalidateViewportCache()
	a.updateViewportContent()

	logDebug("[enterEditMessageMode] Entered edit mode, selected message idx=%d", a.editMessageIdx)
}

// exitEditMessageMode cancels edit mode and returns to normal chat
func (a *App) exitEditMessageMode() {
	a.editMessageMode = false
	a.editMessageUserIdxs = nil

	// Scroll back to bottom
	a.msgViewport.GotoBottom()
	a.invalidateViewportCache()
	a.updateViewportContent()

	logDebug("[exitEditMessageMode] Exited edit mode")
}

// confirmEditMessage executes the edit: forks conversation and puts message in input
func (a *App) confirmEditMessage() {
	if !a.editMessageMode || a.editMessageIdx < 0 || a.editMessageIdx >= len(a.messages) {
		return
	}

	editContent := a.messages[a.editMessageIdx].Content
	logDebug("[confirmEditMessage] Editing message at idx=%d, content length=%d", a.editMessageIdx, len(editContent))

	// Stop agent if running
	if a.bgManager != nil && a.currentConvID != "" && a.bgManager.IsRunning(a.currentConvID) {
		a.bgManager.Cancel(a.currentConvID)
		a.streamingMessage = false
		a.loadingIndicator.Stop(a.animationClock)
		a.setActivityPhase(ActivityPhaseIdle, "")
		logDebug("[confirmEditMessage] Stopped running agent")
	}

	// Fork the SDK conversation at the edit point
	a.forkConversationAtMessage(a.editMessageIdx)

	// Truncate TUI messages to everything before the selected message
	a.messages = a.messages[:a.editMessageIdx]

	// Put the edited message content in the input box
	a.textInput.SetValue(editContent)

	// Exit edit mode
	a.editMessageMode = false
	a.editMessageUserIdxs = nil

	// Reset streaming state
	a.streamingInProgress = false

	// Update viewport and scroll to bottom
	a.invalidateViewportCache()
	a.updateViewport()
	a.msgViewport.GotoBottom()

	logDebug("[confirmEditMessage] Conversation branched, message in input, ready to edit")
}

// navigateEditMessage moves to the next/previous user message in edit mode
// direction: -1 for previous (up), +1 for next (down)
func (a *App) navigateEditMessage(direction int) {
	if len(a.editMessageUserIdxs) == 0 {
		return
	}

	// Find current position in the user message index list
	currentPos := -1
	for i, idx := range a.editMessageUserIdxs {
		if idx == a.editMessageIdx {
			currentPos = i
			break
		}
	}

	if currentPos < 0 {
		return
	}

	// Move in the requested direction
	newPos := currentPos + direction
	if newPos < 0 {
		newPos = 0
	}
	if newPos >= len(a.editMessageUserIdxs) {
		newPos = len(a.editMessageUserIdxs) - 1
	}

	if a.editMessageUserIdxs[newPos] != a.editMessageIdx {
		a.editMessageIdx = a.editMessageUserIdxs[newPos]
		a.scrollToEditMessage()
		a.invalidateViewportCache()
		a.updateViewportContent()
		logDebug("[navigateEditMessage] Moved to user message idx=%d", a.editMessageIdx)
	}
}

// scrollToEditMessage scrolls the viewport to show the selected edit message
func (a *App) scrollToEditMessage() {
	for _, pos := range a.messageLinePositions {
		if pos.MessageIdx == a.editMessageIdx {
			targetOffset := pos.StartLine - 2
			if targetOffset < 0 {
				targetOffset = 0
			}
			a.msgViewport.SetYOffset(targetOffset)
			return
		}
	}
}

// forkConversationAtMessage creates a new SDK conversation with messages up to the edit point
func (a *App) forkConversationAtMessage(editIdx int) {
	if a.sdk == nil || a.currentConvID == "" {
		logDebug("[forkConversationAtMessage] No SDK or conversation, skipping fork")
		return
	}

	// Calculate how many SDK messages to keep
	// TUI messages may have a system prompt at index 0 that doesn't exist in SDK
	sdkMsgCount := editIdx
	if len(a.messages) > 0 && a.messages[0].Role == "system" {
		sdkMsgCount = editIdx - 1
	}
	if sdkMsgCount < 0 {
		sdkMsgCount = 0
	}

	ctx := context.Background()
	newConv, err := a.sdk.ForkConversation(ctx, a.currentConvID, sdkMsgCount)
	if err != nil {
		logDebug("[forkConversationAtMessage] Failed to fork: %v", err)
		a.addNotification("error", "Failed to branch conversation")
		return
	}

	// Update to use the new conversation
	oldConvID := a.currentConvID
	a.setCurrentConversationID(newConv.ID)

	// Update activeConv metadata
	if a.activeConv != nil {
		a.activeConv.ID = newConv.ID
		a.activeConv.Title = a.activeConv.Title + " (edited)"
	}

	logDebug("[forkConversationAtMessage] Forked conversation %s -> %s (kept %d SDK messages)", oldConvID, newConv.ID, sdkMsgCount)

	// CRITICAL: Reload conversation list so the new fork appears with correct timestamp at the top
	// Without this, the forked conversation won't show in the sidebar until you navigate away
	a.loadConversationsFromSDK()

	// Find and select the new forked conversation in the list
	for i, conv := range a.conversations {
		if conv.ID == newConv.ID {
			a.selectedIdx = i
			logDebug("[forkConversationAtMessage] Selected new fork at index %d", i)
			break
		}
	}
}

// updateMCPSettingsData loads MCP server data when opening settings
