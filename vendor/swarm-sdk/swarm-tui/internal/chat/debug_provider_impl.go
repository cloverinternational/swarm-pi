package chat

import (
	"fmt"
	"strings"
)

// Ensure App implements the provider interface methods
// These methods provide introspection capabilities for the DebugInspect tool

// InspectLogs returns log entries matching the criteria
func (a *App) InspectLogs(pattern string, categories []string, level string, limit, offset int) (any, error) {
	if a.debugScreen == nil || a.debugScreen.enhancedLogsView == nil {
		return &DebugInspectResult{
			Target:    "logs",
			Logs:      []DebugLogEntry{},
			LogsTotal: 0,
		}, nil
	}

	// Get all logs from the buffer
	allLogs := a.debugScreen.enhancedLogsView.buffer.GetAll()

	// Build category set for quick lookup
	categorySet := make(map[string]bool)
	for _, cat := range categories {
		categorySet[strings.ToUpper(cat)] = true
	}

	// Filter logs
	var filtered []DebugLogEntry
	for _, entry := range allLogs {
		// Check category filter
		if len(categorySet) > 0 && !categorySet[strings.ToUpper(entry.Category)] {
			continue
		}

		// Check level filter
		if !meetsMinLevel(entry.Level.String(), level) {
			continue
		}

		// Check pattern filter
		if pattern != "" && !matchesPattern(entry.Raw, pattern) {
			continue
		}

		filtered = append(filtered, DebugLogEntry{
			Timestamp: entry.Timestamp,
			Level:     entry.Level.String(),
			Category:  entry.Category,
			Message:   entry.Message,
			Raw:       entry.Raw,
		})
	}

	total := len(filtered)

	// Apply pagination
	if offset >= len(filtered) {
		filtered = []DebugLogEntry{}
	} else {
		filtered = filtered[offset:]
		if len(filtered) > limit {
			filtered = filtered[:limit]
		}
	}

	return &DebugInspectResult{
		Target:    "logs",
		Logs:      filtered,
		LogsTotal: total,
		Viewport:  a.getViewportState(),
	}, nil
}

// InspectMessages returns message info
func (a *App) InspectMessages(messageID *int, limit, offset int) (any, error) {
	var messages []DebugMessageInfo

	if messageID != nil {
		// Get specific message
		if *messageID < 0 || *messageID >= len(a.messages) {
			return nil, fmt.Errorf("message index %d out of range (0-%d)", *messageID, len(a.messages)-1)
		}
		messages = []DebugMessageInfo{a.buildMessageInfo(*messageID)}
	} else {
		// Get all messages with pagination
		total := len(a.messages)

		// Apply offset
		start := offset
		if start < 0 {
			start = 0
		}
		if start >= total {
			start = total
		}

		// Apply limit
		end := start + limit
		if end > total {
			end = total
		}

		// Build message info for the requested range
		for i := start; i < end; i++ {
			messages = append(messages, a.buildMessageInfo(i))
		}
	}

	return &DebugInspectResult{
		Target:        "messages",
		Messages:      messages,
		MessagesTotal: len(a.messages),
		Viewport:      a.getViewportState(),
	}, nil
}

// InspectRender returns detailed render info for a message
func (a *App) InspectRender(messageID int) (any, error) {
	if messageID < 0 || messageID >= len(a.messages) {
		return nil, fmt.Errorf("message index %d out of range (0-%d)", messageID, len(a.messages)-1)
	}

	msg := a.messages[messageID]
	renderInfo := a.buildRenderInfo(messageID, &msg)

	return &DebugInspectResult{
		Target:   "render",
		Render:   renderInfo,
		Viewport: a.getViewportState(),
	}, nil
}

// InspectRequests returns API request info
func (a *App) InspectRequests(pattern string, limit, offset int) (any, error) {
	if a.debugScreen == nil {
		return &DebugInspectResult{
			Target:        "requests",
			Requests:      []DebugRequestInfo{},
			RequestsTotal: 0,
		}, nil
	}

	var filtered []DebugRequestInfo
	for _, req := range a.debugScreen.requests {
		// Check pattern filter
		searchText := req.URL + " " + req.Error + " " + req.Model
		if pattern != "" && !matchesPattern(searchText, pattern) {
			continue
		}

		filtered = append(filtered, DebugRequestInfo{
			ID:           req.ID,
			Timestamp:    req.Timestamp,
			Method:       req.Method,
			URL:          req.URL,
			Status:       req.ResponseCode,
			DurationMs:   req.Duration.Milliseconds(),
			TokensIn:     req.TokensInput,
			TokensOut:    req.TokensOutput,
			Model:        req.Model,
			MessageCount: req.MessageCount,
			Error:        req.Error,
		})
	}

	total := len(filtered)

	// Apply pagination
	if offset >= len(filtered) {
		filtered = []DebugRequestInfo{}
	} else {
		filtered = filtered[offset:]
		if len(filtered) > limit {
			filtered = filtered[:limit]
		}
	}

	return &DebugInspectResult{
		Target:        "requests",
		Requests:      filtered,
		RequestsTotal: total,
		Viewport:      a.getViewportState(),
	}, nil
}

// InspectTools returns tool execution traces
func (a *App) InspectTools(pattern string, limit, offset int) (any, error) {
	var executions []DebugToolExecution

	// Extract tool calls and results from messages
	for i, msg := range a.messages {
		// Get tool calls from assistant messages
		if msg.Role == "assistant" {
			for _, tc := range msg.ToolCalls {
				if pattern != "" && !matchesPattern(tc.Name, pattern) {
					continue
				}

				exec := DebugToolExecution{
					CallID:     tc.ID,
					ToolName:   tc.Name,
					Parameters: tc.Parameters,
				}

				// Find matching result in subsequent messages
				for j := i + 1; j < len(a.messages); j++ {
					if a.messages[j].Role == "tool" {
						for _, tr := range a.messages[j].ToolResults {
							if tr.CallID == tc.ID {
								exec.OutputPreview = truncateString(tr.Output, 500)
								exec.OutputLength = len(tr.Output)
								exec.Error = tr.Error
								break
							}
						}
					}
				}

				executions = append(executions, exec)
			}
		}
	}

	// Also check for hook executions in ordered blocks
	for _, msg := range a.messages {
		for _, block := range msg.OrderedBlocks {
			if block.Type == "hook_execution" && block.HookExecution != nil {
				hook := block.HookExecution
				// Find the tool this hook is for
				for i := range executions {
					if executions[i].ToolName == hook.ToolName {
						executions[i].Hooks = append(executions[i].Hooks, DebugHookExecution{
							Name:    hook.HookName,
							Phase:   hook.Phase,
							Blocked: hook.Blocked,
							Output:  hook.Output,
							Error:   hook.Error,
						})
					}
				}
			}
		}
	}

	total := len(executions)

	// Apply pagination
	if offset >= len(executions) {
		executions = []DebugToolExecution{}
	} else {
		executions = executions[offset:]
		if len(executions) > limit {
			executions = executions[:limit]
		}
	}

	return &DebugInspectResult{
		Target:              "tools",
		ToolExecutions:      executions,
		ToolExecutionsTotal: total,
		Viewport:            a.getViewportState(),
	}, nil
}

// InspectState returns current application state
func (a *App) InspectState() (any, error) {
	state := &DebugAppState{
		Provider:       a.currentProvider,
		Model:          a.currentModel,
		ContextWindow:  a.modelContextWindow,
		TokenCount:     a.tokenCount,
		OperatingMode:  a.operatingMode,
		ConversationID: a.currentConvID,
		MessageCount:   len(a.messages),
		Streaming:      a.streamingMessage,
	}

	// Get thinking settings from SDK
	if a.sdk != nil {
		state.ThinkingEnabled = a.sdk.IsThinkingEnabled()
		if state.ThinkingEnabled {
			state.ThinkingBudget = a.sdk.GetThinkingBudget()
		}
		state.CachingEnabled = a.sdk.IsCachingEnabled()
	}

	// Add render settings
	if a.renderSettings != nil {
		state.RenderSettings = &DebugRenderSettings{
			ShowFullOutput: a.showFullToolOutput,
			ShowThinking:   a.showThinking,
			ToolColors:     make(map[string]string),
		}
		// Get tool colors
		for _, tool := range []string{"bash", "Read", "Write", "Grep", "Edit"} {
			state.RenderSettings.ToolColors[tool] = a.renderSettings.GetToolColor(tool)
		}
	}

	// Add viewport state
	state.Viewport = a.getViewportState()

	// Add queue state
	state.QueueState = &DebugQueueState{
		Capacity:        cap(a.updateQueue),
		CurrentDepth:    len(a.updateQueue),
		GlobalUpdateSeq: a.globalUpdateSequence,
		MessageSeq:      a.messageSequence,
	}

	return &DebugInspectResult{
		Target:   "state",
		State:    state,
		Viewport: a.getViewportState(),
	}, nil
}

// Helper methods

func (a *App) buildMessageInfo(idx int) DebugMessageInfo {
	msg := a.messages[idx]

	info := DebugMessageInfo{
		Index:          idx,
		Role:           msg.Role,
		ContentPreview: truncateString(msg.Content, 200),
		ContentLength:  len(msg.Content),
		Timestamp:      msg.Timestamp,
		IsComplete:     msg.IsComplete,
	}

	if msg.ElapsedTime > 0 {
		info.ElapsedTime = msg.ElapsedTime.String()
	}
	info.Model = msg.Model

	// Add thinking info
	if msg.Thinking != "" {
		info.Thinking = truncateString(msg.Thinking, 200)
		info.ThinkingLength = len(msg.Thinking)
	}

	// Add tool calls
	for _, tc := range msg.ToolCalls {
		info.ToolCalls = append(info.ToolCalls, DebugToolCallInfo{
			ID:         tc.ID,
			Name:       tc.Name,
			Parameters: tc.Parameters,
		})
	}

	// Add tool results
	for _, tr := range msg.ToolResults {
		info.ToolResults = append(info.ToolResults, DebugToolResultInfo{
			CallID:        tr.CallID,
			OutputPreview: truncateString(tr.Output, 200),
			OutputLength:  len(tr.Output),
			Error:         tr.Error,
		})
	}

	// Add attachments
	for _, att := range msg.Attachments {
		info.Attachments = append(info.Attachments, DebugAttachmentInfo{
			FileName: att.FileName,
			MimeType: att.MimeType,
			Size:     att.Size,
		})
	}

	// Add blocks
	for _, block := range msg.OrderedBlocks {
		blockInfo := DebugBlockInfo{
			Type:     block.Type,
			Sequence: block.Sequence,
		}
		if block.ToolCall != nil {
			blockInfo.Tool = block.ToolCall.Name
		}
		if block.Content != "" {
			blockInfo.Preview = truncateString(block.Content, 100)
		}
		info.Blocks = append(info.Blocks, blockInfo)
	}

	// Add render info if available
	if a.messageLinePositions != nil && idx < len(a.messageLinePositions) {
		pos := a.messageLinePositions[idx]
		info.RenderInfo = &DebugMessageRenderInfo{
			LineStart: pos.StartLine,
			LineEnd:   pos.EndLine,
			IsFocused: a.focusedMessageIdx == idx,
		}
	}

	return info
}

func (a *App) buildRenderInfo(idx int, msg *Message) *DebugRenderInfo {
	info := &DebugRenderInfo{
		MessageID: idx,
		Role:      msg.Role,
	}

	// Determine styles used based on role and content
	stylesUsed := make(map[string]bool)

	switch msg.Role {
	case "user":
		stylesUsed["user_primary"] = true
		stylesUsed["user_border"] = true
	case "assistant":
		stylesUsed["assistant_border"] = true
		stylesUsed["text"] = true
		if msg.Thinking != "" {
			stylesUsed["thinking_italic"] = true
		}
		if len(msg.ToolCalls) > 0 {
			stylesUsed["tool_call_orange"] = true
		}
	case "tool":
		stylesUsed["tool_result_connector"] = true
		for _, tr := range msg.ToolResults {
			if tr.Error != "" {
				stylesUsed["tool_error_red"] = true
			} else {
				stylesUsed["tool_result_green"] = true
			}
		}
	case "system":
		stylesUsed["system_muted"] = true
		stylesUsed["system_border"] = true
	}

	for style := range stylesUsed {
		info.StylesUsed = append(info.StylesUsed, style)
	}

	// Build blocks rendered info
	lineNum := 0
	if msg.Thinking != "" {
		thinkingLines := len(strings.Split(msg.Thinking, "\n"))
		info.BlocksRendered = append(info.BlocksRendered, DebugBlockRender{
			Type:  "thinking",
			Lines: makeLineRange(lineNum, lineNum+thinkingLines),
		})
		lineNum += thinkingLines + 1
	}

	if msg.Content != "" {
		contentLines := len(strings.Split(msg.Content, "\n"))
		info.BlocksRendered = append(info.BlocksRendered, DebugBlockRender{
			Type:  "content",
			Lines: makeLineRange(lineNum, lineNum+contentLines),
		})
		lineNum += contentLines
	}

	for _, tc := range msg.ToolCalls {
		info.BlocksRendered = append(info.BlocksRendered, DebugBlockRender{
			Type:  "tool_call",
			Lines: []int{lineNum},
			Tool:  tc.Name,
		})
		lineNum++
	}

	for _, tr := range msg.ToolResults {
		resultLines := len(strings.Split(tr.Output, "\n"))
		if resultLines > 10 && !a.showFullToolOutput {
			resultLines = 10 // Collapsed view
		}
		info.BlocksRendered = append(info.BlocksRendered, DebugBlockRender{
			Type:  "tool_result",
			Lines: makeLineRange(lineNum, lineNum+resultLines),
		})
		lineNum += resultLines
	}

	info.TotalLines = lineNum

	// Generate rendered lines preview (first 20 lines)
	previewLines := a.generateMessagePreview(idx, msg, 20)
	for i, line := range previewLines {
		style := a.detectLineStyle(line, msg.Role)
		info.RenderedLines = append(info.RenderedLines, DebugRenderLine{
			Line:    i,
			Content: line,
			Style:   style,
		})
	}

	return info
}

func (a *App) getViewportState() *DebugViewportState {
	if a.msgViewport == nil {
		return nil
	}
	return &DebugViewportState{
		Width:        a.msgViewport.Width,
		Height:       a.msgViewport.Height,
		ScrollOffset: a.msgViewport.YOffset,
		TotalLines:   a.msgViewport.TotalLineCount(),
	}
}

func (a *App) generateMessagePreview(idx int, msg *Message, maxLines int) []string {
	var lines []string
	th := a.theme

	switch msg.Role {
	case "user":
		wrapped := wrapText(msg.Content, a.msgViewport.Width-6)
		for i, line := range wrapped {
			if i == 0 {
				lines = append(lines, fmt.Sprintf("  > %s", line))
			} else {
				lines = append(lines, fmt.Sprintf("    %s", line))
			}
			if len(lines) >= maxLines {
				break
			}
		}

	case "assistant":
		if msg.Thinking != "" && a.showThinking {
			lines = append(lines, "    ◆ Thinking")
			thinkingWrapped := wrapText(msg.Thinking, a.msgViewport.Width-10)
			for i, line := range thinkingWrapped {
				if i == 0 {
					lines = append(lines, fmt.Sprintf("      ⎿ %s", line))
				} else {
					lines = append(lines, fmt.Sprintf("        %s", line))
				}
				if len(lines) >= maxLines {
					break
				}
			}
		}

		wrapped := wrapText(msg.Content, a.msgViewport.Width-6)
		for i, line := range wrapped {
			if i == 0 {
				lines = append(lines, fmt.Sprintf("    ▌ %s", line))
			} else {
				lines = append(lines, fmt.Sprintf("      %s", line))
			}
			if len(lines) >= maxLines {
				break
			}
		}

		for _, tc := range msg.ToolCalls {
			if len(lines) >= maxLines {
				break
			}
			lines = append(lines, fmt.Sprintf("        ● %s", tc.Name))
		}

	case "system":
		wrapped := wrapText(msg.Content, a.msgViewport.Width-8)
		for i, line := range wrapped {
			if i == 0 {
				lines = append(lines, fmt.Sprintf("  ◆ %s", line))
			} else {
				lines = append(lines, fmt.Sprintf("    %s", line))
			}
			if len(lines) >= maxLines {
				break
			}
		}

	case "tool":
		for _, tr := range msg.ToolResults {
			if len(lines) >= maxLines {
				break
			}
			if tr.Error != "" {
				lines = append(lines, fmt.Sprintf("            ⎿ Error: %s", truncateString(tr.Error, 50)))
			} else {
				preview := truncateString(tr.Output, 100)
				lines = append(lines, fmt.Sprintf("            ⎿ %s", preview))
			}
		}
	}

	_ = th // Silence unused variable warning (we use it for potential future styling)
	return lines
}

func (a *App) detectLineStyle(line string, role string) string {
	switch {
	case strings.HasPrefix(line, "  > "):
		return "user_primary"
	case strings.HasPrefix(line, "    ▌"):
		return "assistant_border"
	case strings.HasPrefix(line, "    ◆ Thinking"):
		return "thinking_header"
	case strings.HasPrefix(line, "      ⎿"):
		return "thinking_content"
	case strings.HasPrefix(line, "        ●"):
		return "tool_call_orange"
	case strings.HasPrefix(line, "            ⎿ Error"):
		return "tool_error_red"
	case strings.HasPrefix(line, "            ⎿"):
		return "tool_result_green"
	case strings.HasPrefix(line, "  ◆"):
		return "system_indicator"
	default:
		return "text"
	}
}

func makeLineRange(start, end int) []int {
	if end <= start {
		return []int{start}
	}
	result := make([]int, end-start)
	for i := range result {
		result[i] = start + i
	}
	return result
}

// Note: wrapText is defined in components.go, so we use that implementation
