package types

import "time"

// Conversation represents metadata about a chat conversation.
type Conversation struct {
	// ID is the unique identifier for this conversation.
	ID string
	// Title is the display name of the conversation.
	Title string
	// Preview is a short preview of the conversation content.
	Preview string

	// Status indicates the conversation state.
	// Values: "idle", "thinking", "waiting", "streaming"
	Status string

	// LastMessage is the timestamp of the most recent message.
	LastMessage time.Time
	// MessageCount is the total number of messages.
	MessageCount int
	// TotalTokens is the cached token count.
	TotalTokens int

	// IsActive indicates if the AI is currently processing.
	IsActive bool

	// Branch is the git branch associated with this conversation.
	Branch string
	// WorkspacePath is the workspace root for this conversation.
	WorkspacePath string

	// AgentThought shows what the agent is currently thinking about.
	AgentThought string
	// WaitingFor indicates what the agent is waiting for (if any).
	WaitingFor string
}

// ConversationFilter defines how to filter the conversation list.
type ConversationFilter int

const (
	// FilterAll shows all conversations.
	FilterAll ConversationFilter = iota
	// FilterCurrentBranch shows only conversations for the current git branch.
	FilterCurrentBranch
	// FilterNoBranch shows only conversations with no branch association.
	FilterNoBranch
	// FilterWorkspace shows only conversations for the current workspace.
	FilterWorkspace
)

// ConversationSortOrder defines how to sort conversations.
type ConversationSortOrder int

const (
	// SortByLastMessage sorts by most recent message first.
	SortByLastMessage ConversationSortOrder = iota
	// SortByCreated sorts by creation time.
	SortByCreated
	// SortByTitle sorts alphabetically by title.
	SortByTitle
	// SortByTokens sorts by token count (descending).
	SortByTokens
)

// ConversationListState holds state for the conversation list view.
type ConversationListState struct {
	// Conversations is the list of all conversations.
	Conversations []Conversation

	// FilteredConversations is the filtered/sorted view.
	FilteredConversations []Conversation

	// SelectedIndex is the currently selected conversation.
	SelectedIndex int
	// ScrollOffset is the scroll position in the list.
	ScrollOffset int

	// Filter is the current filter mode.
	Filter ConversationFilter
	// SortOrder is the current sort order.
	SortOrder ConversationSortOrder

	// SearchQuery is the current search text.
	SearchQuery string
	// Searching indicates if search mode is active.
	Searching bool
}

// NewConversationListState creates a new conversation list state.
func NewConversationListState() *ConversationListState {
	return &ConversationListState{
		Conversations:         []Conversation{},
		FilteredConversations: []Conversation{},
		SelectedIndex:         0,
		ScrollOffset:          0,
		Filter:                FilterAll,
		SortOrder:             SortByLastMessage,
	}
}

// SelectedConversation returns the currently selected conversation.
func (s *ConversationListState) SelectedConversation() *Conversation {
	if s.SelectedIndex >= 0 && s.SelectedIndex < len(s.FilteredConversations) {
		return &s.FilteredConversations[s.SelectedIndex]
	}
	return nil
}
