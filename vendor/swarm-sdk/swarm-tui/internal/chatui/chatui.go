package chatui

import (
	tea "charm.land/bubbletea/v2"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/chatui/state"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/chatui/theme"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/chatui/types"
)

// Re-export types for convenient access
type (
	// Message represents a chat message.
	Message = types.Message
	// MessageBlock represents a content block in a message.
	MessageBlock = types.MessageBlock
	// BlockType identifies the type of content block.
	BlockType = types.BlockType
	// Attachment represents a file attachment.
	Attachment = types.Attachment
	// ToolCallDisplay represents a tool invocation.
	ToolCallDisplay = types.ToolCallDisplay
	// ToolResultDisplay represents a tool result.
	ToolResultDisplay = types.ToolResultDisplay
	// PanelState represents panel state.
	PanelState = types.PanelState
	// Conversation represents conversation metadata.
	Conversation = types.Conversation
)

// Re-export block type constants
const (
	BlockThinking   = types.BlockThinking
	BlockContent    = types.BlockContent
	BlockToolCall   = types.BlockToolCall
	BlockToolResult = types.BlockToolResult
	BlockHook       = types.BlockHook
	BlockSubAgent   = types.BlockSubAgent
)

// Re-export event types
type (
	StreamChunkMsg     = types.StreamChunkMsg
	StreamDoneMsg      = types.StreamDoneMsg
	MessageAppendedMsg = types.MessageAppendedMsg
	MessagesLoadedMsg  = types.MessagesLoadedMsg
	ScrollToBottomMsg  = types.ScrollToBottomMsg
	ToggleThinkingMsg  = types.ToggleThinkingMsg
	ResizeMsg          = types.ResizeMsg
)

// Panel is the public interface for a chat UI panel.
// It implements the Bubbletea Model interface.
type Panel struct {
	model *state.Panel
}

// NewPanel creates a new chat panel with the given dimensions.
func NewPanel(width, height int, opts ...PanelOption) *Panel {
	// Convert public options to state package options
	stateOpts := make([]state.PanelOption, 0, len(opts))
	for _, opt := range opts {
		if stateOpt := opt.toStateOption(); stateOpt != nil {
			stateOpts = append(stateOpts, stateOpt)
		}
	}

	return &Panel{
		model: state.NewPanel(width, height, stateOpts...),
	}
}

// PanelOption configures a Panel.
type PanelOption struct {
	fn func(*state.Panel)
}

func (o PanelOption) toStateOption() state.PanelOption {
	if o.fn == nil {
		return nil
	}
	return func(p *state.Panel) {
		o.fn(p)
	}
}

// WithTheme sets the panel theme.
func WithTheme(th theme.Theme) PanelOption {
	return PanelOption{fn: func(p *state.Panel) {
		state.WithTheme(th)(p)
	}}
}

// WithShowThinking enables thinking block display.
func WithShowThinking(show bool) PanelOption {
	return PanelOption{fn: func(p *state.Panel) {
		state.WithShowThinking(show)(p)
	}}
}

// WithShowFullToolOutput enables full tool output display.
func WithShowFullToolOutput(show bool) PanelOption {
	return PanelOption{fn: func(p *state.Panel) {
		state.WithShowFullToolOutput(show)(p)
	}}
}

// Init initializes the panel (Bubbletea interface).
func (p *Panel) Init() tea.Cmd {
	return p.model.Init()
}

// Update handles messages (Bubbletea interface).
func (p *Panel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	model, cmd := p.model.Update(msg)
	p.model = model.(*state.Panel)
	return p, cmd
}

// View renders the panel (Bubbletea interface).
func (p *Panel) View() tea.View {
	return p.model.View()
}

// ViewString renders the panel as a string (convenience method).
func (p *Panel) ViewString() string {
	return p.model.ViewString()
}

// State returns the current panel state (read-only).
func (p *Panel) State() *PanelState {
	return p.model.State()
}

// SetConversationID sets the current conversation.
func (p *Panel) SetConversationID(id string) {
	p.model.SetConversationID(id)
}

// SetMessages sets the message list.
func (p *Panel) SetMessages(messages []Message) {
	p.model.SetMessages(messages)
}

// AppendMessage adds a message to the panel.
func (p *Panel) AppendMessage(msg Message) tea.Cmd {
	return func() tea.Msg {
		return types.MessageAppendedMsg{Message: msg}
	}
}

// StreamChunk sends a streaming content chunk.
func (p *Panel) StreamChunk(content string, block *MessageBlock) tea.Cmd {
	return func() tea.Msg {
		return types.StreamChunkMsg{
			Content: content,
			Block:   block,
		}
	}
}

// StreamDone signals end of streaming.
func (p *Panel) StreamDone(elapsed types.Duration) tea.Cmd {
	return func() tea.Msg {
		return types.StreamDoneMsg{
			ElapsedTime: elapsed,
		}
	}
}

// LoadMessages loads a conversation's messages.
func (p *Panel) LoadMessages(convID string, messages []Message) tea.Cmd {
	return func() tea.Msg {
		return types.MessagesLoadedMsg{
			ConversationID: convID,
			Messages:       messages,
		}
	}
}

// GetSelectedText returns the currently selected text.
func (p *Panel) GetSelectedText() string {
	return p.model.GetSelectedText()
}

// IsStreaming returns true if currently streaming.
func (p *Panel) IsStreaming() bool {
	return p.model.IsStreaming()
}

// MessageCount returns the number of messages.
func (p *Panel) MessageCount() int {
	return p.model.MessageCount()
}

// AddMessage directly appends a message to the panel (synchronous).
func (p *Panel) AddMessage(msg Message) {
	p.model.AppendMessage(msg)
}

// Resize updates the panel dimensions.
func (p *Panel) Resize(width, height int) {
	p.model.Update(types.ResizeMsg{Width: width, Height: height})
}

// Theme re-exports the theme package types for convenience.
var (
	DefaultTheme = theme.DefaultTheme
	LightTheme   = theme.LightTheme
)

// Duration is a type alias for time.Duration compatibility.
type Duration = types.Duration
