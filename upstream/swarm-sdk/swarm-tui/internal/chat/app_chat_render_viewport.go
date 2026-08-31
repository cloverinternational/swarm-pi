// app_chat_render_viewport.go
// Viewport management: scroll, positioning, anchoring, incremental updates

package chat

import (
	"strings"
	"time"

	"charm.land/lipgloss/v2"
	chatuitypes "github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/chatui/types"
)

func (a *App) findMessageAtOffset(yOffset int) int {
	if len(a.messageLinePositions) == 0 {
		return -1
	}

	for i, pos := range a.messageLinePositions {
		if pos.StartLine <= yOffset && yOffset <= pos.EndLine {
			return i
		}
		if i > 0 && a.messageLinePositions[i-1].EndLine < yOffset &&
			yOffset < pos.StartLine {
			return i - 1
		}
	}

	if len(a.messageLinePositions) > 0 &&
		yOffset >= a.messageLinePositions[len(a.messageLinePositions)-1].EndLine {
		return len(a.messageLinePositions) - 1
	}

	return -1
}

// setUserScrolledAway is the single, traced entry point for changing whether the
// user has scrolled away from the bottom of the message viewport. All mutation of
// a.userScrolledAway MUST go through this method rather than assigning the field
// directly — previously the flag was flipped from ~15 uncoordinated call sites
// with no shared tracing, making it impossible to tell which one caused a given
// scroll decision. reason is a short static string identifying the call site
// (e.g. "pgup_scroll", "mouse_wheel_at_bottom") for the canonical scroll trace.
func (a *App) setUserScrolledAway(v bool, reason string) {
	if a.userScrolledAway == v {
		return
	}
	a.userScrolledAway = v
}

func (a *App) saveScrollAnchor() *ScrollAnchor {
	if a.msgViewport == nil {
		return nil
	}

	// If user is at bottom, restore to bottom
	if a.msgViewport.AtBottom() {
		return &ScrollAnchor{
			YOffset:     a.msgViewport.YOffset,
			WasAtBottom: true,
		}
	}

	// CANONICAL SPACE CONVERSION: a.msgViewport.YOffset is a WRAPPED
	// (preWrappedLines) index whenever the pre-wrapped renderer is currently
	// active (the common steady-state case), but messageLinePositions — and
	// therefore findMessageAtOffset — are expressed in RAW (m.lines) space,
	// the same space the message-render loop counts in. Reading YOffset
	// directly here without converting compares numbers from two different
	// scales, silently anchoring to the wrong message/line. Convert to
	// raw-space first using the same rawFromWrappedIndex helper the mouse
	// hit-testing path already relies on; see SetPreWrappedLines's matching
	// raw->wrapped conversion for the other direction of this bug.
	rawYOffset := a.msgViewport.YOffset
	if a.msgViewport.prewrapActive(a.msgViewport.Width) {
		if rawLine, _, ok := rawFromWrappedIndex(a.msgViewport.preWrappedMapping, a.msgViewport.YOffset); ok {
			rawYOffset = rawLine
		}
	}

	// Anchor to the BOTTOM (last visible line) of the viewport
	// This ensures content above the input box stays in the same visual position
	bottomYOffset := rawYOffset + a.msgViewport.Height - 1

	// Find which message is at the bottom of the viewport
	anchorMessageIdx := a.findMessageAtOffset(bottomYOffset)
	if anchorMessageIdx < 0 {
		anchorMessageIdx = a.findMessageAtOffset(rawYOffset)
	}

	if anchorMessageIdx < 0 {
		return &ScrollAnchor{
			YOffset:     rawYOffset,
			WasAtBottom: false,
		}
	}

	if anchorMessageIdx >= len(a.messageLinePositions) {
		return &ScrollAnchor{
			YOffset:     rawYOffset,
			WasAtBottom: false,
		}
	}

	msgPos := a.messageLinePositions[anchorMessageIdx]

	// CORRECT APPROACH: Anchor to the exact line at the viewport bottom
	// This preserves the EXACT visual position - the same pixels at the bottom
	// This is what the user sees, so this is what we should preserve
	lineOffsetInMessage := bottomYOffset - msgPos.StartLine

	// Log the actual message content
	if anchorMessageIdx < len(a.messages) {
		msgContent := a.messages[anchorMessageIdx].GetContent()
		if len(msgContent) > 100 {
			msgContent = msgContent[:100]
		}
	}

	return &ScrollAnchor{
		YOffset:             a.msgViewport.YOffset,
		WasAtBottom:         false,
		AnchorMessageIndex:  anchorMessageIdx,
		AnchorLineInMessage: lineOffsetInMessage,
		AnchorVisualLine:    0,
	}
}

func (a *App) restoreScrollAnchor(anchor *ScrollAnchor) {
	if anchor == nil {
		return
	}
	if a.msgViewport == nil {
		return
	}

	if anchor.WasAtBottom {
		a.msgViewport.GotoBottom()
		return
	}

	if anchor.AnchorMessageIndex < 0 || anchor.AnchorMessageIndex >= len(a.messageLinePositions) {
		a.msgViewport.SetYOffset(anchor.YOffset)
		return
	}

	newMsgPos := a.messageLinePositions[anchor.AnchorMessageIndex]

	// Calculate the new top YOffset such that the anchor line is at the bottom
	// Anchor line position: newMsgPos.StartLine + anchor.AnchorLineInMessage
	anchorLineGlobalPos := newMsgPos.StartLine + anchor.AnchorLineInMessage

	// New top should be such that: newTop + Height - 1 = anchorLineGlobalPos
	// anchorLineGlobalPos and newMsgPos are RAW (m.lines) space — see the
	// CANONICAL SPACE CONVERSION comment in saveScrollAnchor above. SetYOffset
	// below interprets its argument in whichever space prewrapActive(width)
	// reports (wrapped when the pre-wrapped renderer is active), so this raw
	// offset must be converted to wrapped space here, symmetric to the
	// wrapped->raw conversion saveScrollAnchor already performs on read.
	// Skipping this was the regression: restoreScrollAnchor fed a raw offset
	// straight into SetYOffset while prewrap was active, so the viewport
	// sliced preWrappedLines at the wrong index every time an anchor was
	// restored (every scroll, since scroll goes through save+restore).
	rawNewYOffset := anchorLineGlobalPos - (a.msgViewport.Height - 1)
	newYOffset := rawNewYOffset
	if a.msgViewport.prewrapActive(a.msgViewport.Width) {
		if wrappedIdx, ok := wrappedFromRawIndex(a.msgViewport.preWrappedMapping, rawNewYOffset, 0); ok {
			newYOffset = wrappedIdx
		}
	}

	if newYOffset < 0 {
		newYOffset = 0
	}

	// Log the actual message content we're restoring to
	if anchor.AnchorMessageIndex < len(a.messages) {
		msgContent := a.messages[anchor.AnchorMessageIndex].GetContent()
		if len(msgContent) > 100 {
			msgContent = msgContent[:100]
		}
	}

	a.msgViewport.SetYOffset(newYOffset)
}

func (a *App) syncMessagesToChatPanel() {
	if a.chatPanel == nil {
		return
	}

	// Convert App messages to chatui messages
	chatuiMsgs := make([]chatuitypes.Message, 0, len(a.messages))
	for _, msg := range a.messages {
		chatuiMsg := chatuitypes.Message{
			Role:      msg.Role,
			Content:   msg.GetContent(),
			Timestamp: msg.Timestamp,
			Thinking:  msg.GetThinking(),
		}

		// Convert OrderedBlocks
		for _, block := range msg.OrderedBlocks {
			chatuiBlock := a.convertMessageBlock(block)
			chatuiMsg.OrderedBlocks = append(chatuiMsg.OrderedBlocks, chatuiBlock)
		}

		// Convert tool calls/results that aren't in OrderedBlocks
		for _, tc := range msg.ToolCalls {
			chatuiMsg.ToolCalls = append(chatuiMsg.ToolCalls, chatuitypes.ToolCallDisplay{
				ID:         tc.ID,
				Name:       tc.Name,
				Parameters: tc.Parameters,
			})
		}
		for _, tr := range msg.ToolResults {
			chatuiMsg.ToolResults = append(chatuiMsg.ToolResults, chatuitypes.ToolResultDisplay{
				CallID:   tr.CallID,
				Output:   tr.GetOutput(),
				Error:    tr.Error,
				ToolName: tr.ToolName,
			})
		}

		// Convert attachments
		for _, att := range msg.Attachments {
			chatuiMsg.Attachments = append(chatuiMsg.Attachments, chatuitypes.Attachment{
				FilePath: att.FilePath,
				FileName: att.FileName,
				MimeType: att.MimeType,
				Size:     att.Size,
			})
		}

		chatuiMsg.IsComplete = msg.IsComplete
		chatuiMsg.ElapsedTime = msg.ElapsedTime
		chatuiMsg.Model = msg.Model

		chatuiMsgs = append(chatuiMsgs, chatuiMsg)
	}

	// Set all messages at once
	a.chatPanel.SetMessages(chatuiMsgs)
}

// convertMessageBlock converts a chat/uitypes.MessageBlock to a chatui/types.MessageBlock,
// including recursive conversion of nested sub-agent blocks.
func (a *App) convertMessageBlock(block MessageBlock) chatuitypes.MessageBlock {
	chatuiBlock := chatuitypes.MessageBlock{
		Sequence: block.Sequence,
		Content:  block.GetContent(),
	}

	switch block.Type {
	case "thinking":
		chatuiBlock.Type = chatuitypes.BlockThinking
	case "content":
		chatuiBlock.Type = chatuitypes.BlockContent
	case "tool_call":
		chatuiBlock.Type = chatuitypes.BlockToolCall
		if block.ToolCall != nil {
			chatuiBlock.ToolCall = &chatuitypes.ToolCallDisplay{
				ID:         block.ToolCall.ID,
				Name:       block.ToolCall.Name,
				Parameters: block.ToolCall.Parameters,
			}
		}
	case "tool_result":
		chatuiBlock.Type = chatuitypes.BlockToolResult
		if block.ToolResult != nil {
			chatuiBlock.ToolResult = &chatuitypes.ToolResultDisplay{
				CallID:   block.ToolResult.CallID,
				Output:   block.ToolResult.GetOutput(),
				Error:    block.ToolResult.Error,
				ToolName: block.ToolResult.ToolName,
			}
		}
	case "hook_execution":
		chatuiBlock.Type = chatuitypes.BlockHook
		if block.HookExecution != nil {
			chatuiBlock.HookExecution = &chatuitypes.HookExecutionDisplay{
				HookName: block.HookExecution.HookName,
				ToolName: block.HookExecution.ToolName,
				Phase:    block.HookExecution.Phase,
				Success:  block.HookExecution.Success,
				Output:   block.HookExecution.Output,
				Blocked:  block.HookExecution.Blocked,
				Error:    block.HookExecution.Error,
			}
		}
	case "sub_agent_activity":
		chatuiBlock.Type = chatuitypes.BlockSubAgent
		if block.SubAgentActivity != nil {
			sa := block.SubAgentActivity
			chatuiBlock.SubAgentActivity = &chatuitypes.SubAgentDisplay{
				AgentID:         sa.AgentID,
				AgentName:       sa.AgentName,
				TaskInstruction: sa.TaskInstruction,
				StartTime:       sa.StartTime,
				LastHeartbeat:   sa.LastHeartbeat,
				EventCount:      sa.EventCount,
				TokenCount:      sa.TokenCount,
				SpinnerVerb:     sa.SpinnerVerb,
				CompletionVerb:  sa.CompletionVerb,
				IsComplete:      sa.IsComplete,
				EndTime:         sa.EndTime,
				TurnCount:       sa.TurnCount,
				ToolUseCount:    sa.ToolUseCount,
			}
			// Convert nested blocks recursively
			for _, nestedBlock := range sa.Blocks {
				chatuiBlock.SubAgentActivity.Blocks = append(
					chatuiBlock.SubAgentActivity.Blocks,
					a.convertMessageBlock(nestedBlock),
				)
			}
		}
	default:
		chatuiBlock.Type = chatuitypes.BlockContent
	}

	return chatuiBlock
}

func (a *App) updateViewportContent() {
	// Marking EVERY message dirty here defeated the per-message pre-render
	// cache at the top of the incremental loop (HasPreRenderLines() &&
	// !IsDirty()), so all 82 callers forced a full re-render of the whole
	// transcript. Every direct field mutation in this package targets the
	// LAST message (a.messages[len-1] / lastIdx), which is what the old
	// comment about "direct field mutations during streaming" was really
	// protecting; the one exception (error-lineage report) now marks its own
	// message dirty at the mutation site.
	//
	// ponytail: last-message-only invalidation. If a future caller mutates a
	// message in the middle of the transcript, it must call MarkDirty() at the
	// mutation site — same rule the lineage handler follows.
	if n := len(a.messages); n > 0 {
		a.messages[n-1].MarkDirty()
	}
	a.viewportContentDirty = true
	a.updateViewportIncremental()
}

func (a *App) updateStreamingMessageIncremental() {
	if a.streamingInProgress {
		now := time.Now()
		timeSinceLastUpdate := now.Sub(a.inputProtection.lastUpdateTime)
		if !a.inputProtection.lastUpdateTime.IsZero() && timeSinceLastUpdate < a.inputProtection.debounceDelay {
			a.inputProtection.pendingUpdate = true
			return
		}
		a.inputProtection.lastUpdateTime = now
		a.inputProtection.pendingUpdate = false
	}
	a.updateViewportIncremental()
}

func (a *App) updateViewportIncremental() {
	defer func() {
	}()
	// If no messages, show empty state (or TTE intro if still animating).
	if len(a.messages) == 0 {
		a.msgViewport.SetImageAnchors(nil)
		if !a.introComplete {
			content := a.renderChatIntroContent(a.msgViewport.Width, a.msgViewport.Height)
			a.msgViewport.SetContent(content)
			a.messageLinePositions = nil
			return
		}
		th := a.theme
		emptyStyle := lipgloss.NewStyle().
			Foreground(lipgloss.Color(th.TextMuted)).
			Italic(true)
		content := "\n" + emptyStyle.Render("  Start typing to begin...")
		a.msgViewport.SetContent(content)
		a.messageLinePositions = nil
		return
	}

	// Snapshot a.messages once to avoid a data race: background goroutines
	// (e.g. the auto-compaction path in sendMessage) may replace a.messages
	// with a shorter slice while the render loop is running.  Using a local
	// snapshot means every index access in this function refers to the same
	// slice header (same pointer + length), so we can never go out of bounds
	// even if a.messages is concurrently reassigned.
	messages := a.messages

	// Check if any messages are dirty
	hasDirtyMessages := false
	for i := range messages {
		if messages[i].IsDirty() {
			hasDirtyMessages = true
			break
		}
	}

	// If no dirty messages and we have cached content, skip re-rendering
	if !hasDirtyMessages && !a.viewportContentDirty {
		return
	}

	// Save scroll position before updating
	savedOffset := a.msgViewport.YOffset
	wasAtBottom := a.msgViewport.AtBottom()
	width := a.msgViewport.Width

	// Canonical position tracking: when the user is scrolled away from the
	// bottom and streaming isn't going to force a GotoBottom() anyway, capture
	// a content-identity anchor (message index + line-offset-within-message)
	// BEFORE a.messageLinePositions is rebuilt below. Content growth/shrinkage
	// (new tokens, tool output expanding/collapsing, a re-wrap) shifts raw line
	// numbers, but the anchor tracks the same visual message content, so
	// restoring from it — instead of the raw savedOffset line number — is what
	// keeps the viewport from "moving to places it shouldn't" while streaming
	// continues elsewhere off-screen. See saveScrollAnchor/restoreScrollAnchor.
	var scrollAnchor *ScrollAnchor
	needsAnchor := !wasAtBottom && !(a.streamingInProgress && !a.userScrolledAway)
	if needsAnchor {
		scrollAnchor = a.saveScrollAnchor()
	}

	// Handle new UI path (chatPanel)
	if a.useNewUI && a.chatPanel != nil {
		a.syncMessagesToChatPanel()
		a.chatPanel.Resize(width, a.msgViewport.Height)
		a.msgViewport.SetContent(a.chatPanel.ViewString())
		a.msgViewport.SetImageAnchors(nil)
		a.viewportContentDirty = false
		if a.streamingInProgress && !a.userScrolledAway {
			a.msgViewport.GotoBottom()
		} else if wasAtBottom {
			a.msgViewport.GotoBottom()
		} else {
			a.msgViewport.SetYOffset(savedOffset)
		}
		return
	}

	// Render only dirty messages (incremental rendering)
	var allLines []string
	var allImages []nativeImageAnchor
	positions := make([]MessageLinePosition, 0, len(messages))
	currentLine := 0
	lastMsgIdx := len(messages) - 1

	for i := range messages {
		if i > 0 {
			allLines = append(allLines, "")
			currentLine++
		}
		messageStartLine := currentLine

		// Check if this message has pre-rendered content we can use
		if messages[i].HasPreRenderLines() && !messages[i].IsDirty() {
			// Use pre-rendered content - this is FAST!
			lines := messages[i].GetPreRenderLines()
			for _, anchor := range messages[i].GetPreRenderImages() {
				anchor.Line += messageStartLine
				allImages = append(allImages, anchor)
			}
			allLines = append(allLines, lines...)
			currentLine += len(lines)
		} else {
			// Need to render this message
			isActive := a.streamingInProgress && i == lastMsgIdx
			ctx := a.NewSingleMessageContext(i, width, isActive)
			lines := a.renderMessageListWithContext(messages[i:i+1], ctx)
			images := append([]nativeImageAnchor(nil), a.lastRenderedImageAnchors...)

			// Cache the rendered lines for future use
			messages[i].SetPreRenderAt(width, lines, images)
			messages[i].ClearDirty()
			for _, anchor := range images {
				anchor.Line += messageStartLine
				allImages = append(allImages, anchor)
			}

			allLines = append(allLines, lines...)
			currentLine += len(lines)
		}

		messageEndLine := currentLine - 1
		if messageEndLine < messageStartLine {
			messageEndLine = messageStartLine
		}
		positions = append(positions, MessageLinePosition{
			MessageIdx: i,
			StartLine:  messageStartLine,
			EndLine:    messageEndLine,
		})
	}
	a.messageLinePositions = positions

	// Build string using strings.Builder
	var b strings.Builder
	estimatedSize := 0
	for _, line := range allLines {
		estimatedSize += len(line) + 1
	}
	b.Grow(estimatedSize)
	for i, line := range allLines {
		if i > 0 {
			b.WriteByte('\n')
		}
		b.WriteString(line)
	}
	a.msgViewport.SetContent(b.String())
	a.msgViewport.SetImageAnchors(allImages)

	// Content has been rendered - clear the dirty flag
	a.viewportContentDirty = false

	// Restore scroll position
	if a.streamingInProgress && !a.userScrolledAway {
		a.msgViewport.GotoBottom()
	} else if wasAtBottom {
		a.msgViewport.GotoBottom()
	} else if scrollAnchor != nil {
		a.restoreScrollAnchor(scrollAnchor)
	} else {
		a.msgViewport.SetYOffset(savedOffset)
	}
	// Signal that content changed — the next View() call must do a full render.
	// This ensures streaming updates and queue-drain events are visible immediately
	// even when the animation tick has not yet fired.
	a.viewNeedsRefresh = true
}

func (a *App) updateViewport() {
	// Mark dirty so this explicit call always renders (direct callers need it)
	a.viewportContentDirty = true

	if len(a.messages) == 0 {
		a.msgViewport.SetImageAnchors(nil)
		// Show TTE intro animation while it is still running, then fall back
		// to the static placeholder once the cycle completes.
		if !a.introComplete {
			content := a.renderChatIntroContent(a.msgViewport.Width, a.msgViewport.Height)
			a.msgViewport.SetContent(content)
			a.messageLinePositions = nil
			return
		}
		th := a.theme
		emptyStyle := lipgloss.NewStyle().
			Foreground(lipgloss.Color(th.TextMuted)).
			Italic(true)
		content := "\n" + emptyStyle.Render("  Start typing to begin...")
		a.msgViewport.SetContent(content)
		a.messageLinePositions = nil
		return
	}
	if a.streamingInProgress {
		now := time.Now()
		timeSinceLastUpdate := now.Sub(a.inputProtection.lastUpdateTime)
		if timeSinceLastUpdate < a.inputProtection.debounceDelay {
			a.inputProtection.pendingUpdate = true
			return
		}
		a.inputProtection.lastUpdateTime = now
		a.inputProtection.pendingUpdate = false
	}

	// Save scroll position before updating
	savedOffset := a.msgViewport.YOffset
	wasAtBottom := a.msgViewport.AtBottom()

	width := a.msgViewport.Width

	// See the matching comment in updateViewportIncremental: capture the
	// content-identity anchor before messageLinePositions is rebuilt below.
	var scrollAnchor *ScrollAnchor
	needsAnchor := !wasAtBottom && !(a.streamingInProgress && !a.userScrolledAway)
	if needsAnchor {
		scrollAnchor = a.saveScrollAnchor()
	}

	// Handle new UI path (chatPanel)
	if a.useNewUI && a.chatPanel != nil {
		a.syncMessagesToChatPanel()
		a.chatPanel.Resize(width, a.msgViewport.Height)
		a.msgViewport.SetContent(a.chatPanel.ViewString())
		a.msgViewport.SetImageAnchors(nil)
		a.viewportContentDirty = false
		if a.streamingInProgress && !a.userScrolledAway {
			a.msgViewport.GotoBottom()
		} else if wasAtBottom {
			a.msgViewport.GotoBottom()
		} else {
			a.msgViewport.SetYOffset(savedOffset)
		}
		return
	}

	// Render all messages fresh (no per-message caching — prevents stale renders
	// when collapse state, verbose mode, or theme changes)
	var allLines []string
	var allImages []nativeImageAnchor
	positions := make([]MessageLinePosition, 0, len(a.messages))
	currentLine := 0
	lastMsgIdx := len(a.messages) - 1
	for i := range a.messages {
		if i > 0 {
			allLines = append(allLines, "")
			currentLine++
		}
		messageStartLine := currentLine

		// Render this message fresh on every full viewport rebuild.
		// updateViewport() is the correctness-first path used by direct callers
		// that may have mutated message content without setting per-message dirty state.
		isActive := a.streamingInProgress && i == lastMsgIdx
		ctx := a.NewSingleMessageContext(i, width, isActive)
		lines := a.renderMessageListWithContext(a.messages[i:i+1], ctx)
		images := append([]nativeImageAnchor(nil), a.lastRenderedImageAnchors...)
		a.messages[i].SetPreRenderAt(width, lines, images)
		for _, anchor := range images {
			anchor.Line += messageStartLine
			allImages = append(allImages, anchor)
		}
		allLines = append(allLines, lines...)
		currentLine += len(lines)

		messageEndLine := currentLine - 1
		if messageEndLine < messageStartLine {
			messageEndLine = messageStartLine
		}
		positions = append(positions, MessageLinePosition{
			MessageIdx: i,
			StartLine:  messageStartLine,
			EndLine:    messageEndLine,
		})
	}
	a.messageLinePositions = positions

	a.messageLinePositions = positions

	// Build string using strings.Builder
	var b strings.Builder
	estimatedSize := 0
	for _, line := range allLines {
		estimatedSize += len(line) + 1
	}
	b.Grow(estimatedSize)
	for i, line := range allLines {
		if i > 0 {
			b.WriteByte('\n')
		}
		b.WriteString(line)
	}

	a.msgViewport.SetContent(b.String())
	a.msgViewport.SetImageAnchors(allImages)

	// Content has been rendered — clear the dirty flag
	a.viewportContentDirty = false

	// Restore scroll position
	if a.streamingInProgress && !a.userScrolledAway {
		a.msgViewport.GotoBottom()
	} else if wasAtBottom {
		a.msgViewport.GotoBottom()
	} else if scrollAnchor != nil {
		a.restoreScrollAnchor(scrollAnchor)
	} else {
		a.msgViewport.SetYOffset(savedOffset)
	}
	a.viewNeedsRefresh = true
}
