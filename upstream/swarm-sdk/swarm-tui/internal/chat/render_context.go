package chat

// MessageRenderContext carries rendering metadata for message lists.
type MessageRenderContext struct {
	StartIndex          int
	FocusedMessageIndex int
	MessageNavMode      bool
	IsActiveMessage     bool // True only for the currently streaming message
	IsPreview           bool
	Width               int

	// Edit message mode
	EditMessageMode     bool
	EditMessageIdx      int   // The selected user message index (for highlight)
	EditMessageUserIdxs []int // All user message indices (for position tracking)
}

// ActualMessageIndex maps a loop index back to the actual message index.
func (c MessageRenderContext) ActualMessageIndex(loopIndex int) int {
	return c.StartIndex + loopIndex
}

// IsFocused returns true if the message at loopIndex should be highlighted.
func (c MessageRenderContext) IsFocused(loopIndex int) bool {
	if !c.MessageNavMode {
		return false
	}
	return c.ActualMessageIndex(loopIndex) == c.FocusedMessageIndex
}

// IsEditTarget returns true if the message at loopIndex is the selected edit target.
func (c MessageRenderContext) IsEditTarget(loopIndex int) bool {
	if !c.EditMessageMode {
		return false
	}
	return c.ActualMessageIndex(loopIndex) == c.EditMessageIdx
}

// IsDimmed returns true if the message at loopIndex should be dimmed (after the edit point).
func (c MessageRenderContext) IsDimmed(loopIndex int) bool {
	if !c.EditMessageMode {
		return false
	}
	return c.ActualMessageIndex(loopIndex) > c.EditMessageIdx
}

// NewMessageRenderContext returns the default render context for the main chat view.
// IsActiveMessage is always false here — the unified renderer sets it per-message.
func (a *App) NewMessageRenderContext(width int) MessageRenderContext {
	return MessageRenderContext{
		StartIndex:          0,
		FocusedMessageIndex: a.focusedMessageIdx,
		MessageNavMode:      a.messageNavMode,
		IsActiveMessage:     false,
		IsPreview:           false,
		Width:               width,
		EditMessageMode:     a.editMessageMode,
		EditMessageIdx:      a.editMessageIdx,
		EditMessageUserIdxs: a.editMessageUserIdxs,
	}
}

// NewPreviewMessageContext returns a render context for conversation previews.
func (a *App) NewPreviewMessageContext(width int) MessageRenderContext {
	return MessageRenderContext{
		StartIndex:          0,
		FocusedMessageIndex: -1,
		MessageNavMode:      false,
		IsActiveMessage:     false,
		IsPreview:           true,
		Width:               width,
	}
}

// NewSingleMessageContext returns a render context for rendering a single message.
// isActive should be true only for the currently streaming message.
func (a *App) NewSingleMessageContext(msgIdx, width int, isActive bool) MessageRenderContext {
	return MessageRenderContext{
		StartIndex:          msgIdx,
		FocusedMessageIndex: a.focusedMessageIdx,
		MessageNavMode:      a.messageNavMode,
		IsActiveMessage:     isActive,
		IsPreview:           false,
		Width:               width,
		EditMessageMode:     a.editMessageMode,
		EditMessageIdx:      a.editMessageIdx,
		EditMessageUserIdxs: a.editMessageUserIdxs,
	}
}

// GetEditPosition returns the position of the edit target in the user messages list (1-indexed)
// Returns 0 if not in edit mode or target not found
func (c MessageRenderContext) GetEditPosition() (current int, total int) {
	if !c.EditMessageMode {
		return 0, 0
	}

	total = len(c.EditMessageUserIdxs)
	for i, idx := range c.EditMessageUserIdxs {
		if idx == c.EditMessageIdx {
			current = i + 1 // 1-indexed for display
			break
		}
	}
	return current, total
}

// CountMessagesToRemove returns how many messages will be removed if edit is confirmed
func (c MessageRenderContext) CountMessagesToRemove(totalMessages int) int {
	if !c.EditMessageMode {
		return 0
	}
	return totalMessages - c.EditMessageIdx
}
