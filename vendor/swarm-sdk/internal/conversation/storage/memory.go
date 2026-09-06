package storage

import (
	"context"
	"slices"
	"sync"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/conversation"
)

// MemoryStorage is an in-memory implementation of Storage.
// Thread-safe for concurrent access but not persistent (lost on restart).
type MemoryStorage struct {
	mu            sync.RWMutex
	conversations map[string]*conversation.Conversation
	closed        bool
}

// NewMemoryStorage creates a new in-memory storage.
func NewMemoryStorage() *MemoryStorage {
	return &MemoryStorage{
		conversations: make(map[string]*conversation.Conversation),
		closed:        false,
	}
}

// Save saves or updates a conversation.
func (m *MemoryStorage) Save(ctx context.Context, conv *conversation.Conversation) error {
	if conv == nil {
		return ErrConversationNotFound
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if m.closed {
		return ErrStorageClosed
	}

	// Clone the conversation to avoid external mutations
	clone := cloneConversation(conv)
	m.conversations[conv.ID] = clone

	return nil
}

// Load retrieves a conversation by ID.
func (m *MemoryStorage) Load(ctx context.Context, id string) (*conversation.Conversation, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if m.closed {
		return nil, ErrStorageClosed
	}

	conv, exists := m.conversations[id]
	if !exists {
		return nil, ErrConversationNotFound
	}

	// Return a clone to prevent external mutations
	return cloneConversation(conv), nil
}

// Delete removes a conversation by ID.
func (m *MemoryStorage) Delete(ctx context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.closed {
		return ErrStorageClosed
	}

	if _, exists := m.conversations[id]; !exists {
		return ErrConversationNotFound
	}

	delete(m.conversations, id)
	return nil
}

// Query finds conversations matching the filter.
func (m *MemoryStorage) Query(ctx context.Context, filter Filter) ([]*conversation.Conversation, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if m.closed {
		return nil, ErrStorageClosed
	}

	results := make([]*conversation.Conversation, 0)

	for _, conv := range m.conversations {
		if matchesFilter(conv, filter) {
			results = append(results, cloneConversation(conv))
		}
	}

	return results, nil
}

// List returns all conversations.
func (m *MemoryStorage) List(ctx context.Context) ([]*conversation.Conversation, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if m.closed {
		return nil, ErrStorageClosed
	}

	convs := make([]*conversation.Conversation, 0, len(m.conversations))
	for _, conv := range m.conversations {
		convs = append(convs, cloneConversation(conv))
	}

	return convs, nil
}

// Exists checks if a conversation exists.
func (m *MemoryStorage) Exists(ctx context.Context, id string) (bool, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if m.closed {
		return false, ErrStorageClosed
	}

	_, exists := m.conversations[id]
	return exists, nil
}

// Stream streams messages from a conversation as they are added.
func (m *MemoryStorage) Stream(ctx context.Context, id string) (<-chan *conversation.Message, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if m.closed {
		return nil, ErrStorageClosed
	}

	if _, exists := m.conversations[id]; !exists {
		return nil, ErrConversationNotFound
	}

	// For memory storage, streaming is not implemented
	// Return a closed channel
	ch := make(chan *conversation.Message)
	close(ch)
	return ch, nil
}

// Close closes the storage backend.
func (m *MemoryStorage) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.closed = true
	m.conversations = nil
	return nil
}

// cloneConversation creates a deep copy of a conversation.
func cloneConversation(conv *conversation.Conversation) *conversation.Conversation {
	if conv == nil {
		return nil
	}

	clone := &conversation.Conversation{
		ID:                          conv.ID,
		CreatedAt:                   conv.CreatedAt,
		UpdatedAt:                   conv.UpdatedAt,
		Mode:                        conv.Mode,
		Status:                      conv.Status,
		TraceID:                     conv.TraceID,
		WorkspacePath:               conv.WorkspacePath,
		TotalTokens:                 conv.TotalTokens,
		TotalCostUSD:                conv.TotalCostUSD,
		CurrentContextSize:          conv.CurrentContextSize, // Critical: needed for token tracking
		CurrentContextSizeEstimated: conv.CurrentContextSizeEstimated,
		Title:                       conv.Title,
		Metadata:                    cloneConversationMetadata(conv.Metadata),
		BaseSystemPrompt:            conv.BaseSystemPrompt,
	}
	clone.CompactionState = cloneCompactionState(conv.CompactionState)

	if conv.Summary != nil {
		s := *conv.Summary
		if len(conv.Summary.ModelsUsed) > 0 {
			s.ModelsUsed = append([]string(nil), conv.Summary.ModelsUsed...)
		}
		clone.Summary = &s
	}

	// Clone mode history
	if len(conv.ModeHistory) > 0 {
		clone.ModeHistory = make([]string, len(conv.ModeHistory))
		copy(clone.ModeHistory, conv.ModeHistory)
	}

	// Clone messages
	if len(conv.Messages) > 0 {
		clone.Messages = make([]*conversation.Message, len(conv.Messages))
		for i, msg := range conv.Messages {
			clone.Messages[i] = msg.Clone()
		}
	}

	// Clone agent memories, group state, mode state
	// (shallow copy for now - could be deep copied if needed)
	clone.AgentMemories = conv.AgentMemories
	clone.GroupState = conv.GroupState
	clone.ModeState = conv.ModeState

	return clone
}

func cloneCompactionState(state *conversation.CompactionState) *conversation.CompactionState {
	if state == nil {
		return nil
	}
	cloned := *state
	cloned.FileAccess = append([]conversation.FileAccessEntry(nil), state.FileAccess...)
	cloned.PromptArchive = append([]conversation.PromptArchiveEntry(nil), state.PromptArchive...)
	cloned.PlanArchive = append([]conversation.PlanArchiveEntry(nil), state.PlanArchive...)
	cloned.FileSnapshots = append([]conversation.FileSnapshotEntry(nil), state.FileSnapshots...)
	if len(state.Tasks) > 0 {
		cloned.Tasks = make([]conversation.TaskEntry, len(state.Tasks))
		for i, task := range state.Tasks {
			cloned.Tasks[i] = task
			cloned.Tasks[i].DependsOn = append([]string(nil), task.DependsOn...)
		}
	}
	if state.ModeContext != nil {
		mode := *state.ModeContext
		mode.ActiveMCPServers = append([]string(nil), state.ModeContext.ActiveMCPServers...)
		mode.ActiveHooks = append([]string(nil), state.ModeContext.ActiveHooks...)
		mode.ModifiedFiles = append([]string(nil), state.ModeContext.ModifiedFiles...)
		cloned.ModeContext = &mode
	}
	return &cloned
}

func cloneConversationMetadata(metadata conversation.ConversationMetadata) conversation.ConversationMetadata {
	cloned := metadata
	if len(metadata.Tags) > 0 {
		cloned.Tags = append([]string(nil), metadata.Tags...)
	}
	if len(metadata.Custom) > 0 {
		cloned.Custom = make(map[string]any, len(metadata.Custom))
		for key, value := range metadata.Custom {
			cloned.Custom[key] = deepCloneMetadataValue(value)
		}
	}
	return cloned
}

func deepCloneMetadataValue(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		cloned := make(map[string]any, len(typed))
		for key, nested := range typed {
			cloned[key] = deepCloneMetadataValue(nested)
		}
		return cloned
	case []any:
		cloned := make([]any, len(typed))
		for i, nested := range typed {
			cloned[i] = deepCloneMetadataValue(nested)
		}
		return cloned
	default:
		return typed
	}
}

// matchesFilter checks if a conversation matches the filter criteria.
func matchesFilter(conv *conversation.Conversation, filter Filter) bool {
	// Status filter
	if len(filter.Status) > 0 {
		matched := slices.Contains(filter.Status, conv.Status)
		if !matched {
			return false
		}
	}

	// Mode filter
	if len(filter.Modes) > 0 {
		matched := slices.Contains(filter.Modes, conv.Mode)
		if !matched {
			return false
		}
	}

	// UserID filter
	if filter.UserID != "" && conv.Metadata.UserID != filter.UserID {
		return false
	}

	// ProjectID filter
	if filter.ProjectID != "" && conv.Metadata.ProjectID != filter.ProjectID {
		return false
	}

	// Tags filter (AND logic)
	if len(filter.Tags) > 0 {
		convTags := make(map[string]bool)
		for _, tag := range conv.Metadata.Tags {
			convTags[tag] = true
		}
		for _, requiredTag := range filter.Tags {
			if !convTags[requiredTag] {
				return false
			}
		}
	}

	// ExcludeTags filter (drop if the conversation carries ANY excluded tag).
	// Keeps ephemeral/headless conversations out of listings while leaving them
	// on disk and reachable by direct ID lookup.
	if len(filter.ExcludeTags) > 0 && len(conv.Metadata.Tags) > 0 {
		excluded := make(map[string]bool, len(filter.ExcludeTags))
		for _, tag := range filter.ExcludeTags {
			excluded[tag] = true
		}
		for _, tag := range conv.Metadata.Tags {
			if excluded[tag] {
				return false
			}
		}
	}

	// Time filters (Unix timestamps)
	if filter.CreatedAfter != nil && conv.CreatedAt.Unix() < *filter.CreatedAfter {
		return false
	}
	if filter.CreatedBefore != nil && conv.CreatedAt.Unix() > *filter.CreatedBefore {
		return false
	}
	if filter.UpdatedAfter != nil && conv.UpdatedAt.Unix() < *filter.UpdatedAfter {
		return false
	}
	if filter.UpdatedBefore != nil && conv.UpdatedAt.Unix() > *filter.UpdatedBefore {
		return false
	}

	// Token filters
	if filter.MinTokens != nil && conv.TotalTokens < *filter.MinTokens {
		return false
	}
	if filter.MaxTokens != nil && conv.TotalTokens > *filter.MaxTokens {
		return false
	}

	return true
}
