// Package storage provides conversation persistence interfaces and implementations.
package storage

import (
	"context"
	"errors"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/conversation"
)

var (
	// ErrConversationNotFound is returned when a conversation ID is not found.
	ErrConversationNotFound = errors.New("conversation not found")

	// ErrConversationExists is returned when attempting to save a duplicate conversation ID.
	ErrConversationExists = errors.New("conversation already exists")

	// ErrStorageClosed is returned when operating on a closed storage.
	ErrStorageClosed = errors.New("storage is closed")

	// ErrInvalidFilter is returned when a filter has invalid parameters.
	ErrInvalidFilter = errors.New("invalid filter parameters")

	// ErrInvalidID is returned when a conversation ID is invalid.
	ErrInvalidID = errors.New("invalid conversation id")

	// ErrStorageConnected is returned when attempting to create a new connection to already connected storage.
	ErrStorageConnected = errors.New("storage already connected")

	// ErrStorageNotInitialized is returned when storage backend is not properly initialized.
	ErrStorageNotInitialized = errors.New("storage not initialized")
)

// Storage defines the interface for persisting conversations.
// Implementations can store conversations in memory, files, databases, or cloud storage.
type Storage interface {
	// Save persists a conversation.
	// Updates existing conversation if ID already exists.
	Save(ctx context.Context, conv *conversation.Conversation) error

	// Load retrieves a conversation by ID.
	// Returns error if conversation is not found.
	Load(ctx context.Context, id string) (*conversation.Conversation, error)

	// Delete removes a conversation by ID.
	// No error if conversation doesn't exist (idempotent).
	Delete(ctx context.Context, id string) error

	// Query retrieves conversations matching the given criteria.
	// Returns empty slice if no matches found.
	Query(ctx context.Context, filter Filter) ([]*conversation.Conversation, error)

	// Stream streams messages from a conversation as they are added.
	// The channel is closed when the context is cancelled.
	Stream(ctx context.Context, id string) (<-chan *conversation.Message, error)

	// List returns all conversations.
	// Use Query for more specific filtering.
	List(ctx context.Context) ([]*conversation.Conversation, error)

	// Exists checks if a conversation exists.
	Exists(ctx context.Context, id string) (bool, error)

	// Close closes the storage backend and releases resources.
	Close() error
}

// Filter defines criteria for querying conversations.
type Filter struct {
	// Status filters by conversation status.
	Status []conversation.Status

	// Mode filters by mode.
	Modes []string

	// Tags filters by tags (matches any tag).
	Tags []string

	// UserID filters by user ID.
	UserID string

	// ProjectID filters by project ID.
	ProjectID string

	// CreatedAfter filters conversations created after this timestamp (Unix).
	CreatedAfter *int64

	// CreatedBefore filters conversations created before this timestamp (Unix).
	CreatedBefore *int64

	// UpdatedAfter filters conversations updated after this timestamp (Unix).
	UpdatedAfter *int64

	// UpdatedBefore filters conversations updated before this timestamp (Unix).
	UpdatedBefore *int64

	// MinTokens filters by minimum total tokens.
	MinTokens *int

	// MaxTokens filters by maximum total tokens.
	MaxTokens *int

	// Limit limits the number of results.
	Limit int

	// Offset skips the first N results (for pagination).
	Offset int

	// SortBy specifies the field to sort by.
	// Supported: "created_at", "updated_at", "total_tokens"
	SortBy string

	// SortOrder specifies ascending or descending sort.
	SortOrder SortOrder

	// ExcludeMessages strips the Messages slice from returned conversations.
	// Use this when you only need metadata (title, timestamps, workspace) to
	// avoid loading hundreds of MB of message history into memory for sidebar
	// listings, pagination, etc.
	ExcludeMessages bool

	// FirstMessagePreview keeps only the first N messages when ExcludeMessages
	// is false. Zero means no limit. Set to 1 to get a title preview cheaply.
	FirstMessagePreview int

	// WorkspacePath scopes the query to a single workspace directory.
	// When set, only conversations whose WorkspacePath matches are returned.
	// This is O(1) directory lookup instead of O(N) full scan — use it
	// whenever the caller knows which workspace they are in.
	WorkspacePath string

	// ExcludeTags drops any conversation carrying at least one of these tags
	// from the results. Used to keep non-interactive/ephemeral conversations
	// (e.g. headless `swarm -p` runs, tagged conversation.HeadlessTag) out of
	// browsable/searchable TUI history without deleting them from disk. Empty
	// (the default) is a no-op, so existing callers are unaffected.
	ExcludeTags []string
}

// SortOrder specifies the sort direction.
type SortOrder string

const (
	// SortAscending sorts in ascending order.
	SortAscending SortOrder = "asc"

	// SortDescending sorts in descending order.
	SortDescending SortOrder = "desc"
)

// StorageMetrics provides statistics about the storage backend.
type StorageMetrics struct {
	// TotalConversations is the total number of stored conversations.
	TotalConversations int64

	// TotalMessages is the total number of messages across all conversations.
	TotalMessages int64

	// TotalSizeBytes is the total storage size in bytes.
	TotalSizeBytes int64

	// OldestConversation is the timestamp of the oldest conversation (Unix).
	OldestConversation int64

	// NewestConversation is the timestamp of the newest conversation (Unix).
	NewestConversation int64

	// BackendType identifies the storage backend.
	BackendType string
}

// StorageConfig provides configuration for storage backends.
type StorageConfig struct {
	// Type is the storage backend type.
	// Supported: "memory", "file", "sqlite", "postgres", "s3"
	Type string

	// ConnectionString is the backend-specific connection string.
	// For file: directory path
	// For sqlite: database file path
	// For postgres: connection URL
	// For s3: bucket name
	ConnectionString string

	// Options contains backend-specific configuration.
	Options map[string]any
}

// MetricsProvider is an optional interface for storage implementations that support metrics.
// Implementations may return nil if not supported.
type MetricsProvider interface {
	Metrics(ctx context.Context) (*StorageMetrics, error)
}
