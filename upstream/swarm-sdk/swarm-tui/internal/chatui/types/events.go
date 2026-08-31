package types

import (
	"time"
)

// StreamChunkMsg carries streaming content updates from the agent.
type StreamChunkMsg struct {
	// PanelIndex identifies which panel receives this chunk.
	PanelIndex int
	// Content is the text content being streamed.
	Content string
	// Sequence is the arrival order for proper ordering.
	Sequence int
	// Block is the complete message block if available.
	Block *MessageBlock
}

// StreamDoneMsg signals the end of a streaming response.
type StreamDoneMsg struct {
	// PanelIndex identifies the panel that finished streaming.
	PanelIndex int
	// ElapsedTime is how long the response took.
	ElapsedTime time.Duration
	// TokenCount is the total tokens used (optional).
	TokenCount int
}

// MessageAppendedMsg signals that a new message was added.
type MessageAppendedMsg struct {
	// PanelIndex identifies which panel received the message.
	PanelIndex int
	// Message is the complete message that was added.
	Message Message
}

// MessagesLoadedMsg signals that messages were loaded for a conversation.
type MessagesLoadedMsg struct {
	// PanelIndex identifies which panel loaded messages.
	PanelIndex int
	// ConversationID is the loaded conversation.
	ConversationID string
	// Messages is the complete message list.
	Messages []Message
}

// ScrollToBottomMsg requests the viewport scroll to the bottom.
type ScrollToBottomMsg struct {
	// PanelIndex identifies which panel should scroll.
	PanelIndex int
}

// FocusMessageMsg requests focus on a specific message.
type FocusMessageMsg struct {
	// PanelIndex identifies the panel.
	PanelIndex int
	// MessageIndex is the message to focus.
	MessageIndex int
}

// ToggleThinkingMsg toggles the display of thinking blocks.
type ToggleThinkingMsg struct{}

// ToggleToolOutputMsg toggles between full and collapsed tool output.
type ToggleToolOutputMsg struct{}

// SplitPanelMsg requests a panel split.
type SplitPanelMsg struct {
	// Direction is horizontal or vertical.
	Direction SplitDirection
	// Ratio is the split ratio (0.0-1.0).
	Ratio float64
}

// ClosePanelMsg requests closing a panel.
type ClosePanelMsg struct {
	// PanelIndex is the panel to close.
	PanelIndex int
}

// FocusPanelMsg requests focus on a specific panel.
type FocusPanelMsg struct {
	// PanelIndex is the panel to focus.
	PanelIndex int
}

// SendMessageMsg requests sending a message from a panel.
type SendMessageMsg struct {
	// PanelIndex is the source panel.
	PanelIndex int
	// Content is the message content.
	Content string
	// Attachments are files to attach.
	Attachments []Attachment
}

// CancelStreamMsg requests cancellation of the current streaming response.
type CancelStreamMsg struct {
	// PanelIndex is the panel to cancel.
	PanelIndex int
}

// CopySelectionMsg requests copying the current selection to clipboard.
type CopySelectionMsg struct {
	// PanelIndex is the source panel.
	PanelIndex int
}

// SelectAllMsg requests selecting all content in the viewport.
type SelectAllMsg struct {
	// PanelIndex is the target panel.
	PanelIndex int
}

// ClearSelectionMsg clears the current selection.
type ClearSelectionMsg struct {
	// PanelIndex is the target panel.
	PanelIndex int
}

// InvalidateCacheMsg forces re-rendering of cached content.
type InvalidateCacheMsg struct {
	// PanelIndex is the panel to invalidate (-1 for all).
	PanelIndex int
}

// ResizeMsg signals a terminal resize event.
type ResizeMsg struct {
	// Width is the new terminal width.
	Width int
	// Height is the new terminal height.
	Height int
}

// ThemeChangedMsg signals that the theme has been updated.
type ThemeChangedMsg struct{}
