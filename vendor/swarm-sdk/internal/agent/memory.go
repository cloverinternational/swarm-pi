// Package agent provides memory management for agents.
package agent

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// MemoryScope defines the lifetime/visibility of a memory entry.
type MemoryScope string

const (
	// ScopeConversation means memory lasts only for the current conversation.
	ScopeConversation MemoryScope = "conversation"

	// ScopeSession means memory lasts for the current session/day.
	ScopeSession MemoryScope = "session"

	// ScopePersistent means memory persists forever (or until explicitly deleted).
	ScopePersistent MemoryScope = "persistent"
)

// MemoryEntry represents a single memory item.
type MemoryEntry struct {
	// Key is the unique identifier for this memory.
	Key string

	// Value is the stored content.
	Value string

	// Scope determines the lifetime of this memory.
	Scope MemoryScope

	// Timestamp is when this memory was stored.
	Timestamp time.Time

	// Metadata contains additional context about this memory.
	Metadata map[string]any
}

// Memory defines the interface for agent memory management.
type Memory interface {
	// Store saves a value with the given key and scope.
	Store(ctx context.Context, key, value string, scope MemoryScope) error

	// Retrieve gets a value by key from the specified scope.
	// Returns empty string if not found.
	Retrieve(ctx context.Context, key string, scope MemoryScope) (string, error)

	// Search performs a simple search for entries matching the query.
	// Returns all entries whose key or value contains the query string.
	Search(ctx context.Context, query string, scope MemoryScope) ([]MemoryEntry, error)

	// Forget removes a memory entry by key.
	Forget(ctx context.Context, key string, scope MemoryScope) error

	// List returns all memories in the given scope.
	List(ctx context.Context, scope MemoryScope) ([]MemoryEntry, error)

	// Clear removes all memories in the given scope.
	Clear(ctx context.Context, scope MemoryScope) error

	// Summarize compresses short-term memories into long-term.
	// This is typically called at the end of a conversation.
	Summarize(ctx context.Context) error
}

// InMemoryMemory is a simple in-memory implementation of Memory.
// This is the MVP implementation - no persistence.
type InMemoryMemory struct {
	mu      sync.RWMutex
	storage map[MemoryScope]map[string]MemoryEntry
}

// NewInMemoryMemory creates a new in-memory memory instance.
func NewInMemoryMemory() Memory {
	return &InMemoryMemory{
		storage: map[MemoryScope]map[string]MemoryEntry{
			ScopeConversation: make(map[string]MemoryEntry),
			ScopeSession:      make(map[string]MemoryEntry),
			ScopePersistent:   make(map[string]MemoryEntry),
		},
	}
}

// Store saves a value with the given key and scope.
func (m *InMemoryMemory) Store(ctx context.Context, key, value string, scope MemoryScope) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, exists := m.storage[scope]; !exists {
		return fmt.Errorf("invalid memory scope: %s", scope)
	}

	m.storage[scope][key] = MemoryEntry{
		Key:       key,
		Value:     value,
		Scope:     scope,
		Timestamp: time.Now(),
		Metadata:  make(map[string]any),
	}

	return nil
}

// Retrieve gets a value by key from the specified scope.
func (m *InMemoryMemory) Retrieve(ctx context.Context, key string, scope MemoryScope) (string, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	scopeMap, exists := m.storage[scope]
	if !exists {
		return "", fmt.Errorf("invalid memory scope: %s", scope)
	}

	entry, found := scopeMap[key]
	if !found {
		return "", nil // Not an error, just not found
	}

	return entry.Value, nil
}

// Search performs a simple search for entries matching the query.
func (m *InMemoryMemory) Search(ctx context.Context, query string, scope MemoryScope) ([]MemoryEntry, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	scopeMap, exists := m.storage[scope]
	if !exists {
		return nil, fmt.Errorf("invalid memory scope: %s", scope)
	}

	var results []MemoryEntry
	for _, entry := range scopeMap {
		// Simple substring search in key or value
		if contains(entry.Key, query) || contains(entry.Value, query) {
			results = append(results, entry)
		}
	}

	return results, nil
}

// Forget removes a memory entry by key.
func (m *InMemoryMemory) Forget(ctx context.Context, key string, scope MemoryScope) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	scopeMap, exists := m.storage[scope]
	if !exists {
		return fmt.Errorf("invalid memory scope: %s", scope)
	}

	delete(scopeMap, key)
	return nil
}

// List returns all memories in the given scope.
func (m *InMemoryMemory) List(ctx context.Context, scope MemoryScope) ([]MemoryEntry, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	scopeMap, exists := m.storage[scope]
	if !exists {
		return nil, fmt.Errorf("invalid memory scope: %s", scope)
	}

	entries := make([]MemoryEntry, 0, len(scopeMap))
	for _, entry := range scopeMap {
		entries = append(entries, entry)
	}

	return entries, nil
}

// Clear removes all memories in the given scope.
func (m *InMemoryMemory) Clear(ctx context.Context, scope MemoryScope) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, exists := m.storage[scope]; !exists {
		return fmt.Errorf("invalid memory scope: %s", scope)
	}

	m.storage[scope] = make(map[string]MemoryEntry)
	return nil
}

// Summarize compresses short-term memories into long-term.
// For the MVP implementation, this is a no-op.
// In a full implementation, this would use an LLM to extract key facts.
func (m *InMemoryMemory) Summarize(ctx context.Context) error {
	// TODO: Implement actual summarization using LLM
	// For now, just a placeholder
	return nil
}

// contains performs a case-insensitive substring search.
func contains(s, substr string) bool {
	// Simple case-sensitive search for MVP
	// TODO: Make case-insensitive and support better search
	return len(s) >= len(substr) && findSubstring(s, substr)
}

func findSubstring(s, substr string) bool {
	if len(substr) == 0 {
		return true
	}
	if len(s) < len(substr) {
		return false
	}
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
