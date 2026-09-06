package chatui

import (
	"fmt"

	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/headless/automation/state"
)

// Verify interface implementations at compile time.
var (
	_ state.Inspector        = (*Panel)(nil)
	_ state.MessageInspector = (*Panel)(nil)
	_ state.ScrollInspector  = (*Panel)(nil)
	_ state.InputInspector   = (*Panel)(nil)
)

// GetScreen returns the current screen identifier.
// Implements state.Inspector.
func (p *Panel) GetScreen() string {
	return "chat"
}

// GetMessages returns all messages in the current view.
// Implements state.MessageInspector.
func (p *Panel) GetMessages() []state.MessageState {
	st := p.model.State()
	if st == nil {
		return nil
	}

	messages := make([]state.MessageState, len(st.Messages))
	for i, msg := range st.Messages {
		blocks := make([]state.BlockState, len(msg.OrderedBlocks))
		for j, block := range msg.OrderedBlocks {
			blocks[j] = state.BlockState{
				Type:    block.Type.String(),
				Content: block.Content,
			}
		}

		messages[i] = state.MessageState{
			ID:        fmt.Sprintf("msg_%d", i), // Generate ID from index
			Role:      msg.Role,
			Content:   msg.Content,
			Blocks:    blocks,
			Timestamp: msg.Timestamp,
		}
	}

	return messages
}

// GetMessageCount returns the total number of messages.
// Implements state.MessageInspector.
func (p *Panel) GetMessageCount() int {
	return p.model.MessageCount()
}

// GetScrollOffset returns the current scroll offset.
// Implements state.ScrollInspector.
func (p *Panel) GetScrollOffset() int {
	st := p.model.State()
	if st == nil || st.Viewport == nil {
		return 0
	}
	return st.Viewport.YOffset
}

// GetScrollMax returns the maximum scroll value.
// Implements state.ScrollInspector.
func (p *Panel) GetScrollMax() int {
	// The max scroll depends on content vs viewport height
	// For now return a reasonable estimate
	st := p.model.State()
	if st == nil {
		return 0
	}
	return len(st.CachedLines)
}

// GetVisibleRange returns first and last visible message indices.
// Implements state.ScrollInspector.
func (p *Panel) GetVisibleRange() (first, last int) {
	st := p.model.State()
	if st == nil || len(st.Messages) == 0 {
		return 0, 0
	}
	// Return all messages for now - would need viewport calculations
	return 0, len(st.Messages) - 1
}

// GetInputText returns the current input text.
// Implements state.InputInspector.
func (p *Panel) GetInputText() string {
	st := p.model.State()
	if st == nil {
		return ""
	}
	return st.InputText
}

// GetCursorPosition returns the cursor position.
// Implements state.InputInspector.
func (p *Panel) GetCursorPosition() int {
	st := p.model.State()
	if st == nil {
		return 0
	}
	return st.CursorPos
}

// IsInputFocused returns true if input is focused.
// Implements state.InputInspector.
func (p *Panel) IsInputFocused() bool {
	st := p.model.State()
	if st == nil {
		return false
	}
	// Input is focused when not in message navigation mode
	return !st.MessageNavMode
}
