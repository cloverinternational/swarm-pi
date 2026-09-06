// Package state provides state management for the chat UI.
//
// The state package contains the Bubbletea Model implementations
// for chat panels and the multi-panel coordinator.
package state

import (
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/chatui/renderer"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/chatui/theme"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/chatui/types"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/chatui/viewport"
)

// Panel is the Bubbletea Model for a single chat panel.
type Panel struct {
	state    *types.PanelState
	viewport *viewport.Viewport
	renderer *renderer.MessageRenderer
	theme    theme.Theme
	styles   *theme.StyleSet
}

// PanelOption configures a Panel.
type PanelOption func(*Panel)

// NewPanel creates a new chat panel with the given dimensions.
func NewPanel(width, height int, opts ...PanelOption) *Panel {
	th := theme.DefaultTheme()

	p := &Panel{
		state:    types.NewPanelState(),
		viewport: viewport.New(width, height-3), // Reserve space for input
		renderer: renderer.NewMessageRenderer(th, width),
		theme:    th,
		styles:   theme.NewStyleSet(th),
	}

	p.state.Viewport.Width = width
	p.state.Viewport.Height = height

	for _, opt := range opts {
		opt(p)
	}

	return p
}

// WithTheme sets the panel theme.
func WithTheme(th theme.Theme) PanelOption {
	return func(p *Panel) {
		p.theme = th
		p.styles = theme.NewStyleSet(th)
		p.renderer = renderer.NewMessageRenderer(th, p.state.Viewport.Width)
	}
}

// WithShowThinking enables thinking block display.
func WithShowThinking(show bool) PanelOption {
	return func(p *Panel) {
		p.state.ShowThinking = show
		p.renderer.SetShowThinking(show)
	}
}

// WithShowFullToolOutput enables full tool output display.
func WithShowFullToolOutput(show bool) PanelOption {
	return func(p *Panel) {
		p.state.ShowFullToolOutput = show
		p.renderer.SetShowFullToolOutput(show)
	}
}

// Init initializes the panel (Bubbletea interface).
func (p *Panel) Init() tea.Cmd {
	return nil
}

// Update handles messages (Bubbletea interface).
func (p *Panel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd

	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		return p, p.handleResize(msg.Width, msg.Height)

	case types.StreamChunkMsg:
		return p, p.handleStreamChunk(msg)

	case types.StreamDoneMsg:
		return p, p.handleStreamDone(msg)

	case types.MessageAppendedMsg:
		return p, p.handleMessageAppended(msg)

	case types.MessagesLoadedMsg:
		return p, p.handleMessagesLoaded(msg)

	case types.ToggleThinkingMsg:
		p.state.ShowThinking = !p.state.ShowThinking
		p.renderer.SetShowThinking(p.state.ShowThinking)
		p.invalidateCache()

	case types.ToggleToolOutputMsg:
		p.state.ShowFullToolOutput = !p.state.ShowFullToolOutput
		p.renderer.SetShowFullToolOutput(p.state.ShowFullToolOutput)
		p.invalidateCache()

	case types.ScrollToBottomMsg:
		p.viewport.ScrollToBottom()

	case types.FocusMessageMsg:
		p.state.FocusedMessageIdx = msg.MessageIndex
		p.invalidateCache()

	case types.InvalidateCacheMsg:
		p.invalidateCache()

	case tea.KeyMsg:
		cmd := p.handleKey(msg)
		if cmd != nil {
			cmds = append(cmds, cmd)
		}

	case tea.MouseMsg:
		cmd := p.viewport.Update(msg)
		if cmd != nil {
			cmds = append(cmds, cmd)
		}
	}

	return p, tea.Batch(cmds...)
}

// View renders the panel (Bubbletea interface).
func (p *Panel) View() tea.View {
	// Only update viewport content when cache was invalidated
	if !p.state.CacheValid {
		lines := p.renderAllMessages()
		p.viewport.SetLines(lines)
	}

	// Build tea.View
	var v tea.View
	v.SetContent(p.viewport.View())
	return v
}

// ViewString returns the rendered content as a string.
func (p *Panel) ViewString() string {
	// Only update viewport content when cache was invalidated
	if !p.state.CacheValid {
		lines := p.renderAllMessages()
		p.viewport.SetLines(lines)
	}
	return p.viewport.View()
}

// State returns the current panel state (read-only).
func (p *Panel) State() *types.PanelState {
	return p.state
}

// SetConversationID sets the current conversation.
func (p *Panel) SetConversationID(id string) {
	p.state.ConversationID = id
}

// SetMessages sets the message list.
func (p *Panel) SetMessages(messages []types.Message) {
	p.state.Messages = messages
	p.invalidateCache()
}

// AppendMessage adds a message to the panel.
func (p *Panel) AppendMessage(msg types.Message) {
	p.state.Messages = append(p.state.Messages, msg)
	p.invalidateCache()
}

// handleResize processes window resize events.
func (p *Panel) handleResize(width, height int) tea.Cmd {
	p.state.Viewport.Width = width
	p.state.Viewport.Height = height
	p.viewport.Resize(width, height-3)
	p.renderer.SetWidth(width)
	p.invalidateCache()
	return nil
}

// handleStreamChunk processes streaming content.
func (p *Panel) handleStreamChunk(msg types.StreamChunkMsg) tea.Cmd {
	if !p.state.Streaming {
		p.state.Streaming = true
		// Ensure we have a message to stream into
		if len(p.state.Messages) == 0 {
			p.state.Messages = append(p.state.Messages, types.Message{
				Role:      "assistant",
				Timestamp: time.Now(),
			})
		}
		p.state.StreamingMsgIndex = len(p.state.Messages) - 1
	}

	// Append block to current message
	if p.state.StreamingMsgIndex >= 0 && p.state.StreamingMsgIndex < len(p.state.Messages) {
		currentMsg := &p.state.Messages[p.state.StreamingMsgIndex]

		if msg.Block != nil {
			currentMsg.OrderedBlocks = append(currentMsg.OrderedBlocks, *msg.Block)
		} else if msg.Content != "" {
			// Append to last content block or create new one
			p.appendContentToMessage(currentMsg, msg.Content)
		}
	}

	p.invalidateCache()

	// Auto-scroll if at bottom
	if !p.state.Viewport.UserScrolledAway {
		return func() tea.Msg {
			return types.ScrollToBottomMsg{}
		}
	}

	return nil
}

// appendContentToMessage appends content to the last content block.
func (p *Panel) appendContentToMessage(msg *types.Message, content string) {
	// Find the last content block or create a new one
	for i := len(msg.OrderedBlocks) - 1; i >= 0; i-- {
		if msg.OrderedBlocks[i].Type == types.BlockContent {
			msg.OrderedBlocks[i].Content += content
			return
		}
	}

	// No content block found, create new one
	msg.OrderedBlocks = append(msg.OrderedBlocks, types.MessageBlock{
		Type:     types.BlockContent,
		Content:  content,
		Sequence: len(msg.OrderedBlocks),
	})
}

// handleStreamDone processes end of streaming.
func (p *Panel) handleStreamDone(msg types.StreamDoneMsg) tea.Cmd {
	p.state.Streaming = false

	if p.state.StreamingMsgIndex >= 0 && p.state.StreamingMsgIndex < len(p.state.Messages) {
		currentMsg := &p.state.Messages[p.state.StreamingMsgIndex]
		currentMsg.IsComplete = true
		currentMsg.ElapsedTime = msg.ElapsedTime
	}

	p.state.StreamingMsgIndex = -1
	p.invalidateCache()

	return nil
}

// handleMessageAppended processes a new message.
func (p *Panel) handleMessageAppended(msg types.MessageAppendedMsg) tea.Cmd {
	p.state.Messages = append(p.state.Messages, msg.Message)
	p.invalidateCache()

	if !p.state.Viewport.UserScrolledAway {
		return func() tea.Msg {
			return types.ScrollToBottomMsg{}
		}
	}

	return nil
}

// handleMessagesLoaded processes loaded messages.
func (p *Panel) handleMessagesLoaded(msg types.MessagesLoadedMsg) tea.Cmd {
	p.state.ConversationID = msg.ConversationID
	p.state.Messages = msg.Messages
	p.invalidateCache()

	return func() tea.Msg {
		return types.ScrollToBottomMsg{}
	}
}

// handleKey processes keyboard input.
func (p *Panel) handleKey(msg tea.KeyMsg) tea.Cmd {
	switch msg.String() {
	// Scrolling
	case "up", "k":
		p.viewport.ScrollUp(1)
	case "down", "j":
		p.viewport.ScrollDown(1)
	case "pgup", "ctrl+b":
		p.viewport.PageUp()
	case "pgdown", "ctrl+f":
		p.viewport.PageDown()
	case "ctrl+u":
		p.viewport.HalfPageUp()
	case "ctrl+d":
		p.viewport.HalfPageDown()
	case "home", "g":
		p.viewport.ScrollToTop()
	case "end", "G":
		p.viewport.ScrollToBottom()

	// Message navigation
	case "ctrl+n":
		p.focusNextMessage()
	case "ctrl+p":
		p.focusPreviousMessage()

	// Display toggles
	case "ctrl+t":
		return func() tea.Msg { return types.ToggleThinkingMsg{} }
	case "ctrl+o":
		return func() tea.Msg { return types.ToggleToolOutputMsg{} }

	// Selection
	case "ctrl+a":
		p.viewport.SelectAll()
	case "ctrl+c":
		return func() tea.Msg { return types.CopySelectionMsg{} }
	case "esc":
		p.viewport.ClearSelection()
	}

	return nil
}

// focusNextMessage moves focus to the next assistant message.
func (p *Panel) focusNextMessage() {
	for i := p.state.FocusedMessageIdx + 1; i < len(p.state.Messages); i++ {
		if p.state.Messages[i].Role == "assistant" {
			p.state.FocusedMessageIdx = i
			p.invalidateCache()
			// TODO: Scroll to make focused message visible
			break
		}
	}
}

// focusPreviousMessage moves focus to the previous assistant message.
func (p *Panel) focusPreviousMessage() {
	start := p.state.FocusedMessageIdx - 1
	if start < 0 {
		start = len(p.state.Messages) - 1
	}

	for i := start; i >= 0; i-- {
		if p.state.Messages[i].Role == "assistant" {
			p.state.FocusedMessageIdx = i
			p.invalidateCache()
			break
		}
	}
}

// renderAllMessages renders all messages to lines.
func (p *Panel) renderAllMessages() []string {
	if p.state.CacheValid && len(p.state.CachedLines) > 0 {
		return p.state.CachedLines
	}

	var lines []string

	for i, msg := range p.state.Messages {
		if i > 0 {
			lines = append(lines, "") // Spacing between messages
		}

		focused := p.state.MessageNavMode && i == p.state.FocusedMessageIdx
		msgLines := p.renderer.Render(msg, focused)
		lines = append(lines, msgLines...)
	}

	p.state.CachedLines = lines
	p.state.CacheValid = true

	return lines
}

// invalidateCache marks the render cache as invalid.
func (p *Panel) invalidateCache() {
	p.state.CacheValid = false
}

// GetSelectedText returns the currently selected text.
func (p *Panel) GetSelectedText() string {
	return p.viewport.GetSelectedText()
}

// IsStreaming returns true if currently streaming a response.
func (p *Panel) IsStreaming() bool {
	return p.state.Streaming
}

// MessageCount returns the number of messages.
func (p *Panel) MessageCount() int {
	return len(p.state.Messages)
}
