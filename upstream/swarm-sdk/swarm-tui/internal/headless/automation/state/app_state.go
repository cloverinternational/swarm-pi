// Package state provides deep state introspection for TUI models.
package state

import (
	"time"
)

// AppState represents the full state of the main TUI application.
// This is designed to match the internal/chat/app.go App struct.
type AppState struct {
	// Navigation
	Screen          string `json:"screen"`
	SelectedIndex   int    `json:"selected_index"`
	ScrollOffset    int    `json:"scroll_offset"`
	HomeButton      int    `json:"home_button"`
	MessageNavMode  bool   `json:"message_nav_mode"`
	FocusedMsgIndex int    `json:"focused_msg_index"`

	// Content
	ConversationID string         `json:"conversation_id,omitempty"`
	Messages       []MessageState `json:"messages,omitempty"`
	MessageCount   int            `json:"message_count"`

	// Input
	InputText      string   `json:"input_text"`
	CursorPosition int      `json:"cursor_position"`
	InputHistory   []string `json:"input_history,omitempty"`

	// Modals
	ActiveModal string `json:"active_modal,omitempty"`
	ModalState  any    `json:"modal_state,omitempty"`

	// Display
	ShowSidePanel      bool   `json:"show_side_panel"`
	ShowThinking       bool   `json:"show_thinking"`
	ShowFullToolOutput bool   `json:"show_full_tool_output"`
	OperatingMode      string `json:"operating_mode"`

	// Streaming
	IsStreaming      bool `json:"is_streaming"`
	UserScrolledAway bool `json:"user_scrolled_away"`

	// Activity is the canonical visible turn state. It is separate from
	// IsStreaming because tool use and reasoning can be active before a
	// provider emits content.
	ActivityPhase  string `json:"activity_phase,omitempty"`
	ActivityLabel  string `json:"activity_label,omitempty"`
	ActivityStatus string `json:"activity_status,omitempty"`
	ActivityActive bool   `json:"activity_active"`

	// Debug
	DebugVisible bool   `json:"debug_visible"`
	DebugTab     string `json:"debug_tab,omitempty"`

	// Performance
	ViewportDirty bool `json:"viewport_dirty"`
	CacheValid    bool `json:"cache_valid"`

	// Git
	CurrentBranch string `json:"current_branch,omitempty"`
	BranchFilter  string `json:"branch_filter,omitempty"`
	Workspace     string `json:"workspace,omitempty"`

	// Timestamp
	Timestamp time.Time `json:"timestamp"`
}

// ConversationState represents a conversation entry.
type ConversationState struct {
	ID          string    `json:"id"`
	Title       string    `json:"title"`
	LastMessage string    `json:"last_message,omitempty"`
	UpdatedAt   time.Time `json:"updated_at"`
	Branch      string    `json:"branch,omitempty"`
}

// ToolState represents tool/command state in the UI.
type ToolState struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Status    string `json:"status"` // "pending", "running", "complete", "error"
	Expanded  bool   `json:"expanded"`
	HasOutput bool   `json:"has_output"`
}

// SettingsState represents settings screen state.
type SettingsState struct {
	ActiveSection string         `json:"active_section"`
	Values        map[string]any `json:"values,omitempty"`
	IsDirty       bool           `json:"is_dirty"`
}

// ViewerState represents file viewer state.
type ViewerState struct {
	TreeVisible bool   `json:"tree_visible"`
	TreeFocused bool   `json:"tree_focused"`
	CurrentPath string `json:"current_path,omitempty"`
	CurrentFile string `json:"current_file,omitempty"`
	ScrollY     int    `json:"scroll_y"`
	ScrollX     int    `json:"scroll_x"`
}

// AutocompleteState represents autocomplete overlay state.
type AutocompleteState struct {
	Visible       bool     `json:"visible"`
	Query         string   `json:"query"`
	SelectedIndex int      `json:"selected_index"`
	Suggestions   []string `json:"suggestions,omitempty"`
	Type          string   `json:"type"` // "command", "file", "mention"
}

// AppInspector is the interface that the main App should implement
// to expose its state for automation.
type AppInspector interface {
	Inspector

	// GetAppState returns the full application state
	GetAppState() *AppState

	// GetConversations returns conversation list state
	GetConversations() []ConversationState

	// GetTools returns tool state for current view
	GetTools() []ToolState

	// GetSettingsState returns settings state
	GetSettingsState() *SettingsState

	// GetViewerState returns file viewer state
	GetViewerState() *ViewerState

	// GetAutocompleteState returns autocomplete state
	GetAutocompleteState() *AutocompleteState
}

// Screen constants matching internal/chat/app.go
const (
	ScreenHome     = "home"
	ScreenChats    = "chats"
	ScreenChat     = "chat"
	ScreenSettings = "settings"
	ScreenViewer   = "viewer"
	ScreenDebug    = "debug"
)

// Modal constants
const (
	ModalNone               = ""
	ModalExit               = "exit"
	ModalStopAgent          = "stop_agent"
	ModalNewChat            = "new_chat"
	ModalGitCheckout        = "git_checkout"
	ModalGitInit            = "git_init"
	ModalPermissionApproval = "permission_approval"
)

// OperatingMode constants
const (
	ModeOff   = "off"
	ModePlan  = "plan"
	ModeAct   = "act"
	ModeAuto  = "auto"
	ModeDebug = "debug"
)
