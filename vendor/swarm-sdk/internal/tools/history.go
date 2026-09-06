// Package tools provides file history tracking for version control and rollback.
// This enables tracking what changes the AI agent made to files, detecting
// when users manually edited files, and supporting rollback operations.
package tools

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/google/uuid"
)

// Common errors for history operations
var (
	ErrFileNotFound    = errors.New("file not found in history")
	ErrVersionNotFound = errors.New("version not found")
	ErrSessionRequired = errors.New("session ID is required")
)

// FileHistory defines the interface for tracking file versions.
// Implementations can store versions in memory, SQLite, or other backends.
type FileHistory interface {
	// GetByPathAndSession retrieves a file's history record by path and session.
	// Returns ErrFileNotFound if no record exists.
	ByPathAndSession(ctx context.Context, path, sessionID string) (*HistoryFile, error)

	// Create creates a new file history record.
	// The initialContent is the content before any modifications.
	Create(ctx context.Context, sessionID, path, initialContent string) (*HistoryFile, error)

	// CreateVersion adds a new version to an existing file's history.
	// If the file doesn't exist in history, it will be created first.
	CreateVersion(ctx context.Context, sessionID, path, content string) (*HistoryVersion, error)

	// GetVersions retrieves all versions for a file.
	Versions(ctx context.Context, path, sessionID string) ([]*HistoryVersion, error)

	// GetVersion retrieves a specific version by ID.
	Version(ctx context.Context, versionID string) (*HistoryVersion, error)

	// GetLatestVersion retrieves the most recent version for a file.
	LatestVersion(ctx context.Context, path, sessionID string) (*HistoryVersion, error)

	// Rollback reverts a file to a specific version.
	// Returns the content of that version.
	Rollback(ctx context.Context, path, sessionID, versionID string) (string, error)
}

// HistoryFile represents a file being tracked in history.
type HistoryFile struct {
	ID            string
	SessionID     string
	Path          string
	Content       string // The initial/base content
	CreatedAt     time.Time
	VersionCount  int
	LatestVersion *HistoryVersion
}

// HistoryVersion represents a single version of a file's content.
type HistoryVersion struct {
	ID        string
	FileID    string
	Content   string
	CreatedAt time.Time
	// Metadata can store additional info like who made the change
	Metadata map[string]string
}

// InMemoryHistory implements FileHistory using in-memory storage.
// This is useful for testing and simple deployments.
// For production, consider a persistent implementation (SQLite, etc.).
type InMemoryHistory struct {
	files    map[string]*HistoryFile    // key: sessionID:path
	versions map[string]*HistoryVersion // key: versionID
	mu       sync.RWMutex
}

// NewInMemoryHistory creates a new in-memory history service.
func NewInMemoryHistory() *InMemoryHistory {
	return &InMemoryHistory{
		files:    make(map[string]*HistoryFile),
		versions: make(map[string]*HistoryVersion),
	}
}

// fileKey generates the map key for a file
func fileKey(sessionID, path string) string {
	return sessionID + ":" + path
}

// GetByPathAndSession retrieves a file's history record.
func (h *InMemoryHistory) ByPathAndSession(ctx context.Context, path, sessionID string) (*HistoryFile, error) {
	if sessionID == "" {
		return nil, ErrSessionRequired
	}

	h.mu.RLock()
	defer h.mu.RUnlock()

	file, exists := h.files[fileKey(sessionID, path)]
	if !exists {
		return nil, ErrFileNotFound
	}

	// Return a copy to prevent external mutation
	fileCopy := *file
	return &fileCopy, nil
}

// Create creates a new file history record.
func (h *InMemoryHistory) Create(ctx context.Context, sessionID, path, initialContent string) (*HistoryFile, error) {
	if sessionID == "" {
		return nil, ErrSessionRequired
	}

	h.mu.Lock()
	defer h.mu.Unlock()

	key := fileKey(sessionID, path)

	// Check if already exists
	if _, exists := h.files[key]; exists {
		// Return existing file
		fileCopy := *h.files[key]
		return &fileCopy, nil
	}

	file := &HistoryFile{
		ID:           uuid.New().String(),
		SessionID:    sessionID,
		Path:         path,
		Content:      initialContent,
		CreatedAt:    time.Now(),
		VersionCount: 0,
	}

	h.files[key] = file

	// Return a copy
	fileCopy := *file
	return &fileCopy, nil
}

// CreateVersion adds a new version to a file's history.
func (h *InMemoryHistory) CreateVersion(ctx context.Context, sessionID, path, content string) (*HistoryVersion, error) {
	if sessionID == "" {
		return nil, ErrSessionRequired
	}

	h.mu.Lock()
	defer h.mu.Unlock()

	key := fileKey(sessionID, path)

	// Get or create file
	file, exists := h.files[key]
	if !exists {
		file = &HistoryFile{
			ID:           uuid.New().String(),
			SessionID:    sessionID,
			Path:         path,
			Content:      "", // No initial content known
			CreatedAt:    time.Now(),
			VersionCount: 0,
		}
		h.files[key] = file
	}

	// Create version
	version := &HistoryVersion{
		ID:        uuid.New().String(),
		FileID:    file.ID,
		Content:   content,
		CreatedAt: time.Now(),
		Metadata:  make(map[string]string),
	}

	h.versions[version.ID] = version
	file.VersionCount++
	file.LatestVersion = version

	// Return a copy
	versionCopy := *version
	return &versionCopy, nil
}

// GetVersions retrieves all versions for a file.
func (h *InMemoryHistory) Versions(ctx context.Context, path, sessionID string) ([]*HistoryVersion, error) {
	if sessionID == "" {
		return nil, ErrSessionRequired
	}

	h.mu.RLock()
	defer h.mu.RUnlock()

	key := fileKey(sessionID, path)
	file, exists := h.files[key]
	if !exists {
		return nil, ErrFileNotFound
	}

	// Collect all versions for this file
	var versions []*HistoryVersion
	for _, v := range h.versions {
		if v.FileID == file.ID {
			vCopy := *v
			versions = append(versions, &vCopy)
		}
	}

	return versions, nil
}

// GetVersion retrieves a specific version by ID.
func (h *InMemoryHistory) Version(ctx context.Context, versionID string) (*HistoryVersion, error) {
	h.mu.RLock()
	defer h.mu.RUnlock()

	version, exists := h.versions[versionID]
	if !exists {
		return nil, ErrVersionNotFound
	}

	versionCopy := *version
	return &versionCopy, nil
}

// GetLatestVersion retrieves the most recent version for a file.
func (h *InMemoryHistory) LatestVersion(ctx context.Context, path, sessionID string) (*HistoryVersion, error) {
	if sessionID == "" {
		return nil, ErrSessionRequired
	}

	h.mu.RLock()
	defer h.mu.RUnlock()

	key := fileKey(sessionID, path)
	file, exists := h.files[key]
	if !exists {
		return nil, ErrFileNotFound
	}

	if file.LatestVersion == nil {
		return nil, ErrVersionNotFound
	}

	versionCopy := *file.LatestVersion
	return &versionCopy, nil
}

// Rollback retrieves the content of a specific version.
// Note: This doesn't actually modify the filesystem - that's the caller's responsibility.
func (h *InMemoryHistory) Rollback(ctx context.Context, path, sessionID, versionID string) (string, error) {
	version, err := h.Version(ctx, versionID)
	if err != nil {
		return "", err
	}
	return version.Content, nil
}

// Clear removes all history data. Useful for testing.
func (h *InMemoryHistory) Clear() {
	h.mu.Lock()
	defer h.mu.Unlock()

	h.files = make(map[string]*HistoryFile)
	h.versions = make(map[string]*HistoryVersion)
}

// Count returns the number of tracked files.
func (h *InMemoryHistory) Count() int {
	h.mu.RLock()
	defer h.mu.RUnlock()

	return len(h.files)
}

// VersionCount returns the total number of versions across all files.
func (h *InMemoryHistory) VersionCount() int {
	h.mu.RLock()
	defer h.mu.RUnlock()

	return len(h.versions)
}

// NoOpHistory implements FileHistory but does nothing.
// Useful when history tracking is not needed.
type NoOpHistory struct{}

// NewNoOpHistory creates a no-op history service.
func NewNoOpHistory() *NoOpHistory {
	return &NoOpHistory{}
}

func (h *NoOpHistory) ByPathAndSession(ctx context.Context, path, sessionID string) (*HistoryFile, error) {
	return nil, ErrFileNotFound
}

func (h *NoOpHistory) Create(ctx context.Context, sessionID, path, initialContent string) (*HistoryFile, error) {
	return &HistoryFile{
		ID:        "noop",
		SessionID: sessionID,
		Path:      path,
		Content:   initialContent,
		CreatedAt: time.Now(),
	}, nil
}

func (h *NoOpHistory) CreateVersion(ctx context.Context, sessionID, path, content string) (*HistoryVersion, error) {
	return &HistoryVersion{
		ID:        "noop",
		FileID:    "noop",
		Content:   content,
		CreatedAt: time.Now(),
	}, nil
}

func (h *NoOpHistory) Versions(ctx context.Context, path, sessionID string) ([]*HistoryVersion, error) {
	return []*HistoryVersion{}, nil
}

func (h *NoOpHistory) Version(ctx context.Context, versionID string) (*HistoryVersion, error) {
	return nil, ErrVersionNotFound
}

func (h *NoOpHistory) LatestVersion(ctx context.Context, path, sessionID string) (*HistoryVersion, error) {
	return nil, ErrVersionNotFound
}

func (h *NoOpHistory) Rollback(ctx context.Context, path, sessionID, versionID string) (string, error) {
	return "", ErrVersionNotFound
}

// DefaultFileHistory is the default history service.
// By default, it's a no-op. Applications can replace this with InMemoryHistory
// or a persistent implementation.
var DefaultFileHistory FileHistory = NewNoOpHistory()
