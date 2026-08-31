// Package state provides deep state introspection for TUI models.
//
// Models can implement various interfaces to expose their internal state
// for inspection, debugging, and automation.
package state

import (
	"fmt"
	"reflect"
	"time"
)

// Inspector provides methods to inspect model state.
// Models implement specific interfaces to expose their internals.
type Inspector interface {
	// GetScreen returns the current screen identifier
	GetScreen() string
}

// MessageInspector provides access to chat messages.
type MessageInspector interface {
	// GetMessages returns all messages in the current view
	GetMessages() []MessageState
	// GetMessageCount returns the total number of messages
	GetMessageCount() int
}

// FocusInspector provides access to focus state.
type FocusInspector interface {
	// GetFocusedComponent returns the ID of the focused component
	GetFocusedComponent() string
	// IsFocused returns true if the given component is focused
	IsFocused(componentID string) bool
}

// InputInspector provides access to input field state.
type InputInspector interface {
	// GetInputText returns the current input text
	GetInputText() string
	// GetCursorPosition returns the cursor position
	GetCursorPosition() int
	// IsInputFocused returns true if input is focused
	IsInputFocused() bool
}

// ScrollInspector provides access to scroll state.
type ScrollInspector interface {
	// GetScrollOffset returns the current scroll offset
	GetScrollOffset() int
	// GetScrollMax returns the maximum scroll value
	GetScrollMax() int
	// GetVisibleRange returns first and last visible indices
	GetVisibleRange() (first, last int)
}

// ComponentInspector provides access to component tree.
type ComponentInspector interface {
	// GetComponents returns all component IDs
	GetComponents() []string
	// GetComponentState returns state for a specific component
	GetComponentState(id string) ComponentState
}

// SelectionInspector provides access to selection state.
type SelectionInspector interface {
	// GetSelectedIndex returns the currently selected index
	GetSelectedIndex() int
	// GetSelectedItem returns the currently selected item
	GetSelectedItem() any
}

// ModalInspector provides access to modal/popup state.
type ModalInspector interface {
	// IsModalOpen returns true if a modal is open
	IsModalOpen() bool
	// GetModalID returns the ID of the open modal
	GetModalID() string
	// GetModalData returns the modal's data
	GetModalData() any
}

// AppStateInspector exposes richer application state for dashboards.
type AppStateInspector interface {
	GetAppState() *AppState
}

// MessageState represents the state of a chat message.
type MessageState struct {
	ID        string            `json:"id"`
	Role      string            `json:"role"`
	Content   string            `json:"content"`
	Blocks    []BlockState      `json:"blocks,omitempty"`
	Timestamp time.Time         `json:"timestamp"`
	Metadata  map[string]string `json:"metadata,omitempty"`
}

// BlockState represents a message block.
type BlockState struct {
	Type     string `json:"type"`
	Content  string `json:"content"`
	Language string `json:"language,omitempty"`
	Expanded bool   `json:"expanded"`
}

// ComponentState represents the state of a UI component.
type ComponentState struct {
	ID       string         `json:"id"`
	Type     string         `json:"type"`
	Focused  bool           `json:"focused"`
	Visible  bool           `json:"visible"`
	Bounds   Bounds         `json:"bounds"`
	Props    map[string]any `json:"props,omitempty"`
	Children []string       `json:"children,omitempty"`
}

// Bounds represents component boundaries.
type Bounds struct {
	X      int `json:"x"`
	Y      int `json:"y"`
	Width  int `json:"width"`
	Height int `json:"height"`
}

// FullState aggregates all inspectable state from a model.
type FullState struct {
	Screen           string           `json:"screen"`
	ConversationID   string           `json:"conversation_id,omitempty"`
	OperatingMode    string           `json:"operating_mode,omitempty"`
	Streaming        bool             `json:"streaming,omitempty"`
	Timestamp        time.Time        `json:"timestamp"`
	Messages         []MessageState   `json:"messages,omitempty"`
	FocusedComponent string           `json:"focused_component,omitempty"`
	Components       []ComponentState `json:"components,omitempty"`
	Input            *InputState      `json:"input,omitempty"`
	Scroll           *ScrollState     `json:"scroll,omitempty"`
	Selection        *SelectionState  `json:"selection,omitempty"`
	Modal            *ModalState      `json:"modal,omitempty"`
	Workspace        string           `json:"workspace,omitempty"`
	Branch           string           `json:"branch,omitempty"`
	ActivityPhase    string           `json:"activity_phase,omitempty"`
	ActivityLabel    string           `json:"activity_label,omitempty"`
	ActivityStatus   string           `json:"activity_status,omitempty"`
	ActivityActive   bool             `json:"activity_active"`
}

// InputState represents input field state.
type InputState struct {
	Text           string `json:"text"`
	CursorPosition int    `json:"cursor_position"`
	Focused        bool   `json:"focused"`
}

// ScrollState represents scroll state.
type ScrollState struct {
	Offset       int `json:"offset"`
	Max          int `json:"max"`
	VisibleFirst int `json:"visible_first"`
	VisibleLast  int `json:"visible_last"`
}

// SelectionState represents selection state.
type SelectionState struct {
	Index int `json:"index"`
	Item  any `json:"item,omitempty"`
}

// ModalState represents modal state.
type ModalState struct {
	Open bool   `json:"open"`
	ID   string `json:"id,omitempty"`
	Data any    `json:"data,omitempty"`
}

// redactSensitiveMessages strips the verbatim content of system messages (the
// system prompt is message msg_0) before the conversation is serialized over
// the automation control socket. The control socket has no auth, so dumping the
// full system prompt + conversation via `swarm attach <h> state` was an info
// leak. We keep the role, id, and a length marker so the dump stays useful for
// automation/diagnostics without exposing the prompt text.
func redactSensitiveMessages(msgs []MessageState) []MessageState {
	out := make([]MessageState, len(msgs))
	for i, m := range msgs {
		if m.Role == "system" {
			n := len(m.Content)
			m.Content = fmt.Sprintf("[redacted system prompt: %d chars]", n)
			m.Blocks = nil
		}
		out[i] = m
	}
	return out
}

// Extract extracts full state from a model by checking which interfaces it implements.
func Extract(model any) *FullState {
	state := &FullState{
		Timestamp: time.Now(),
	}

	// Screen
	if i, ok := model.(Inspector); ok {
		state.Screen = i.GetScreen()
	}
	if i, ok := model.(AppStateInspector); ok {
		if app := i.GetAppState(); app != nil {
			state.ConversationID = app.ConversationID
			state.OperatingMode = app.OperatingMode
			state.Streaming = app.IsStreaming
			state.Workspace = app.Workspace
			state.Branch = app.CurrentBranch
			state.ActivityPhase = app.ActivityPhase
			state.ActivityLabel = app.ActivityLabel
			state.ActivityStatus = app.ActivityStatus
			state.ActivityActive = app.ActivityActive
		}
	}

	// Messages
	if i, ok := model.(MessageInspector); ok {
		state.Messages = redactSensitiveMessages(i.GetMessages())
	}

	// Focus
	if i, ok := model.(FocusInspector); ok {
		state.FocusedComponent = i.GetFocusedComponent()
	}

	// Components
	if i, ok := model.(ComponentInspector); ok {
		ids := i.GetComponents()
		state.Components = make([]ComponentState, len(ids))
		for idx, id := range ids {
			state.Components[idx] = i.GetComponentState(id)
		}
	}

	// Input
	if i, ok := model.(InputInspector); ok {
		state.Input = &InputState{
			Text:           i.GetInputText(),
			CursorPosition: i.GetCursorPosition(),
			Focused:        i.IsInputFocused(),
		}
	}

	// Scroll
	if i, ok := model.(ScrollInspector); ok {
		first, last := i.GetVisibleRange()
		state.Scroll = &ScrollState{
			Offset:       i.GetScrollOffset(),
			Max:          i.GetScrollMax(),
			VisibleFirst: first,
			VisibleLast:  last,
		}
	}

	// Selection
	if i, ok := model.(SelectionInspector); ok {
		state.Selection = &SelectionState{
			Index: i.GetSelectedIndex(),
			Item:  i.GetSelectedItem(),
		}
	}

	// Modal
	if i, ok := model.(ModalInspector); ok {
		state.Modal = &ModalState{
			Open: i.IsModalOpen(),
			ID:   i.GetModalID(),
			Data: i.GetModalData(),
		}
	}

	return state
}

// ExtractField extracts a specific field using reflection.
// Path is dot-separated, e.g., "messages.0.content"
func ExtractField(model any, path string) (any, bool) {
	v := reflect.ValueOf(model)
	if v.Kind() == reflect.Pointer {
		v = v.Elem()
	}

	// Simple single-field extraction for now
	if v.Kind() == reflect.Struct {
		field := v.FieldByName(path)
		if field.IsValid() && field.CanInterface() {
			return field.Interface(), true
		}
	}

	return nil, false
}
