package types

import "time"

// Duration is an alias for time.Duration for API compatibility.
type Duration = time.Duration

// PanelState represents the complete state for a single chat panel.
// This is the Bubbletea Model for a chat panel instance.
type PanelState struct {
	// Viewport contains scroll and selection state.
	Viewport *ViewportState

	// Messages is the list of messages in this panel.
	Messages []Message

	// ConversationID identifies the current conversation.
	ConversationID string

	// Streaming state
	Streaming         bool   // True when receiving streaming content.
	StreamingMsgIndex int    // Index of the message being streamed.
	StreamBuffer      string // Buffer for partial streaming content.

	// Navigation
	MessageNavMode    bool // Tab-enabled message navigation mode.
	FocusedMessageIdx int  // Currently focused message index.

	// Display options
	ShowThinking       bool // Show extended thinking blocks.
	ShowFullToolOutput bool // Show full tool output vs. collapsed.

	// Render cache
	CachedLines []string // Pre-rendered message lines.
	CacheValid  bool     // True if cache is current.
	CacheHash   uint64   // Hash for cache invalidation.

	// Input state
	InputText    string   // Current input text.
	InputHistory []string // History of sent messages.
	HistoryIndex int      // Current position in history (-1 = new).
	CursorPos    int      // Cursor position in input.

	// Attachments being composed.
	Attachments []Attachment
}

// NewPanelState creates a new panel state with sensible defaults.
func NewPanelState() *PanelState {
	return &PanelState{
		Viewport: &ViewportState{
			AutoScroll: true,
		},
		Messages:          []Message{},
		StreamingMsgIndex: -1,
		FocusedMessageIdx: -1,
		HistoryIndex:      -1,
		CachedLines:       []string{},
	}
}

// ViewportState holds viewport-specific state for scrolling and selection.
type ViewportState struct {
	// Dimensions
	Width  int
	Height int

	// Scroll position
	YOffset int // Lines scrolled from top.
	XOffset int // Characters scrolled horizontally.

	// Selection
	Selection Selection
	Selecting bool // True when actively selecting.

	// Auto-scroll behavior
	AutoScroll       bool // Auto-scroll to bottom on new content.
	UserScrolledAway bool // User has scrolled away from bottom.

	// Performance
	FastScrollMode bool // Skip expensive wrapping during rapid scroll.
}

// Selection represents a text selection range in the viewport.
type Selection struct {
	// Start position
	StartLine int
	StartCol  int

	// End position
	EndLine int
	EndCol  int

	// Active indicates if a selection exists.
	Active bool
}

// IsEmpty returns true if the selection has no content.
func (s Selection) IsEmpty() bool {
	return !s.Active || (s.StartLine == s.EndLine && s.StartCol == s.EndCol)
}

// Normalize ensures start is before end.
func (s Selection) Normalize() Selection {
	if s.StartLine > s.EndLine || (s.StartLine == s.EndLine && s.StartCol > s.EndCol) {
		return Selection{
			StartLine: s.EndLine,
			StartCol:  s.EndCol,
			EndLine:   s.StartLine,
			EndCol:    s.StartCol,
			Active:    s.Active,
		}
	}
	return s
}

// MultiPanelState manages multiple chat panels in a split layout.
type MultiPanelState struct {
	// Panels is the list of panel states.
	Panels []*PanelState

	// ActiveIndex is the currently focused panel.
	ActiveIndex int

	// Layout configuration
	SplitDirection SplitDirection
	SplitRatio     float64 // 0.0-1.0, size of first panel.
}

// SplitDirection defines how panels are arranged.
type SplitDirection int

const (
	// SplitHorizontal arranges panels left to right.
	SplitHorizontal SplitDirection = iota
	// SplitVertical arranges panels top to bottom.
	SplitVertical
)

// NewMultiPanelState creates a single-panel state.
func NewMultiPanelState() *MultiPanelState {
	return &MultiPanelState{
		Panels:         []*PanelState{NewPanelState()},
		ActiveIndex:    0,
		SplitDirection: SplitHorizontal,
		SplitRatio:     0.5,
	}
}

// ActivePanel returns the currently focused panel.
func (m *MultiPanelState) ActivePanel() *PanelState {
	if m.ActiveIndex >= 0 && m.ActiveIndex < len(m.Panels) {
		return m.Panels[m.ActiveIndex]
	}
	return nil
}

// IsSplit returns true if there are multiple panels.
func (m *MultiPanelState) IsSplit() bool {
	return len(m.Panels) > 1
}
