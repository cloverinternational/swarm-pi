// Package tools provides file tracking utilities for detecting race conditions
// between file reads and writes. This is critical for preventing AI agents from
// overwriting user changes made outside the tool system.
package tools

import (
	"sync"
	"time"
)

// FileRecord tracks when a file was last read and written by the tool system.
// This enables race condition detection: if a file's modification time is newer
// than the last read time, the file was modified externally.
type FileRecord struct {
	Path      string
	ReadTime  time.Time
	WriteTime time.Time
}

// FileTracker maintains a thread-safe registry of file access times.
// It is used to detect when files have been modified between read and write
// operations, preventing the common AI coding mistake of overwriting user changes.
type FileTracker struct {
	records map[string]FileRecord
	mu      sync.RWMutex
}

// NewFileTracker creates a new FileTracker instance.
func NewFileTracker() *FileTracker {
	return &FileTracker{
		records: make(map[string]FileRecord),
	}
}

// RecordRead marks a file as having been read at the current time.
// This should be called after any file read operation (file_read tool, etc.).
// The recorded time is used to detect if the file was modified externally
// before a subsequent write operation.
func (t *FileTracker) RecordRead(path string) {
	t.mu.Lock()
	defer t.mu.Unlock()

	record, exists := t.records[path]
	if !exists {
		record = FileRecord{Path: path}
	}
	record.ReadTime = time.Now()
	t.records[path] = record
}

// RecordWrite marks a file as having been written at the current time.
// This should be called after any file write operation.
// After a write, the file is also considered "read" since the tool knows
// the current content.
func (t *FileTracker) RecordWrite(path string) {
	t.mu.Lock()
	defer t.mu.Unlock()

	record, exists := t.records[path]
	if !exists {
		record = FileRecord{Path: path}
	}
	record.WriteTime = time.Now()
	// After writing, we implicitly know the content, so update read time too
	record.ReadTime = record.WriteTime
	t.records[path] = record
}

// GetLastReadTime returns when the file was last read by the tool system.
// Returns zero time if the file has never been read.
// This is used to compare against file modification time to detect external changes.
func (t *FileTracker) LastReadTime(path string) time.Time {
	t.mu.RLock()
	defer t.mu.RUnlock()

	record, exists := t.records[path]
	if !exists {
		return time.Time{}
	}
	return record.ReadTime
}

// GetLastWriteTime returns when the file was last written by the tool system.
// Returns zero time if the file has never been written.
func (t *FileTracker) LastWriteTime(path string) time.Time {
	t.mu.RLock()
	defer t.mu.RUnlock()

	record, exists := t.records[path]
	if !exists {
		return time.Time{}
	}
	return record.WriteTime
}

// GetRecord returns the full FileRecord for a path, or nil if not tracked.
func (t *FileTracker) Record(path string) *FileRecord {
	t.mu.RLock()
	defer t.mu.RUnlock()

	record, exists := t.records[path]
	if !exists {
		return nil
	}
	// Return a copy to prevent external mutation
	recordCopy := record
	return &recordCopy
}

// IsTracked returns true if the file has been read or written by the tool system.
func (t *FileTracker) IsTracked(path string) bool {
	t.mu.RLock()
	defer t.mu.RUnlock()

	_, exists := t.records[path]
	return exists
}

// Clear removes all tracking records. Useful for testing or session reset.
func (t *FileTracker) Clear() {
	t.mu.Lock()
	defer t.mu.Unlock()

	t.records = make(map[string]FileRecord)
}

// ClearPath removes tracking for a specific path.
func (t *FileTracker) ClearPath(path string) {
	t.mu.Lock()
	defer t.mu.Unlock()

	delete(t.records, path)
}

// Count returns the number of tracked files.
func (t *FileTracker) Count() int {
	t.mu.RLock()
	defer t.mu.RUnlock()

	return len(t.records)
}

// DefaultFileTracker is the global file tracker instance.
// Tools should use this unless they need isolated tracking (e.g., for testing).
var DefaultFileTracker = NewFileTracker()

// Convenience functions that operate on the default tracker

// RecordFileRead records a file read on the default tracker.
func RecordFileRead(path string) {
	DefaultFileTracker.RecordRead(path)
}

// RecordFileWrite records a file write on the default tracker.
func RecordFileWrite(path string) {
	DefaultFileTracker.RecordWrite(path)
}

// GetFileLastReadTime gets the last read time from the default tracker.
func GetFileLastReadTime(path string) time.Time {
	return DefaultFileTracker.LastReadTime(path)
}

// GetFileLastWriteTime gets the last write time from the default tracker.
func GetFileLastWriteTime(path string) time.Time {
	return DefaultFileTracker.LastWriteTime(path)
}

// IsFileTracked checks if a file is tracked in the default tracker.
func IsFileTracked(path string) bool {
	return DefaultFileTracker.IsTracked(path)
}
