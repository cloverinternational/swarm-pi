// Package filetracker provides file access tracking for compaction recovery.
// This module tracks which files are read and modified during tool execution,
// enabling the compaction system to recover important files into the context window.
package filetracker

import (
	"context"
	"sync"
	"time"
)

// contextKey is the type for context keys in this package.
type contextKey string

const (
	// recorderKey is the context key for the FileAccessRecorder.
	recorderKey contextKey = "file_access_recorder"
)

// Record represents a single file access record.
type Record struct {
	// Path is the absolute file path.
	Path string `json:"path"`

	// ReadAt is when the file was last read.
	ReadAt time.Time `json:"read_at"`

	// WriteAt is when the file was last written/modified.
	WriteAt time.Time `json:"write_at"`

	// AccessCount is the total number of accesses.
	AccessCount int `json:"access_count"`

	// TokenCount is an estimate of the file's token count.
	TokenCount int `json:"token_count"`

	// Priority is calculated based on access patterns.
	Priority int `json:"priority"`
}

// Recorder tracks file access for compaction recovery.
type Recorder struct {
	records map[string]*Record
	mu      sync.RWMutex
}

// NewRecorder creates a new FileAccessRecorder.
func NewRecorder() *Recorder {
	return &Recorder{
		records: make(map[string]*Record),
	}
}

// RecordRead records a file read operation.
func (r *Recorder) RecordRead(path string, estimatedTokens int) {
	r.mu.Lock()
	defer r.mu.Unlock()

	record, exists := r.records[path]
	if !exists {
		record = &Record{Path: path}
		r.records[path] = record
	}

	record.ReadAt = time.Now()
	record.AccessCount++
	record.TokenCount = estimatedTokens
	record.Priority = r.calculatePriority(record)
}

// RecordWrite records a file write/modify operation.
func (r *Recorder) RecordWrite(path string, estimatedTokens int) {
	r.mu.Lock()
	defer r.mu.Unlock()

	record, exists := r.records[path]
	if !exists {
		record = &Record{Path: path}
		r.records[path] = record
	}

	record.WriteAt = time.Now()
	record.AccessCount++
	record.TokenCount = estimatedTokens
	record.Priority = r.calculatePriority(record)
}

// calculatePriority calculates the priority score for a file.
// Modified files get higher priority than just-read files.
func (r *Recorder) calculatePriority(record *Record) int {
	priority := 0

	// Modified files get base +10 priority
	if !record.WriteAt.IsZero() {
		priority += 10
	}

	// Each access adds +2
	priority += record.AccessCount * 2

	// Recency bonus: files accessed in last 5 minutes get +5
	if time.Since(record.ReadAt) < 5*time.Minute || time.Since(record.WriteAt) < 5*time.Minute {
		priority += 5
	}

	return priority
}

// GetRecords returns all file access records.
func (r *Recorder) GetRecords() []Record {
	r.mu.RLock()
	defer r.mu.RUnlock()

	result := make([]Record, 0, len(r.records))
	for _, record := range r.records {
		result = append(result, *record)
	}
	return result
}

// GetModifiedFiles returns paths of files that were written/modified.
func (r *Recorder) GetModifiedFiles() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()

	var result []string
	for _, record := range r.records {
		if !record.WriteAt.IsZero() {
			result = append(result, record.Path)
		}
	}
	return result
}

// GetReadFiles returns paths of files that were read (including modified ones).
func (r *Recorder) GetReadFiles() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()

	var result []string
	for _, record := range r.records {
		result = append(result, record.Path)
	}
	return result
}

// GetTopFiles returns the top N files by priority, respecting token budget.
func (r *Recorder) GetTopFiles(maxFiles int, maxTokens int) []Record {
	r.mu.RLock()
	defer r.mu.RUnlock()

	// Sort by priority (descending)
	sorted := make([]*Record, 0, len(r.records))
	for _, record := range r.records {
		sorted = append(sorted, record)
	}

	// Simple sort by priority
	for i := 0; i < len(sorted); i++ {
		for j := i + 1; j < len(sorted); j++ {
			if sorted[j].Priority > sorted[i].Priority {
				sorted[i], sorted[j] = sorted[j], sorted[i]
			}
		}
	}

	// Select files within budget
	var result []Record
	totalTokens := 0
	for i := 0; i < len(sorted) && len(result) < maxFiles; i++ {
		if totalTokens+sorted[i].TokenCount <= maxTokens {
			result = append(result, *sorted[i])
			totalTokens += sorted[i].TokenCount
		}
	}

	return result
}

// Clear removes all records.
func (r *Recorder) Clear() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.records = make(map[string]*Record)
}

// Merge combines records from another recorder.
func (r *Recorder) Merge(other *Recorder) {
	if other == nil {
		return
	}

	// Guard against self-merge
	if other == r {
		return
	}

	// Take a snapshot of other.records while holding its read lock
	other.mu.RLock()
	snapshot := make(map[string]*Record, len(other.records))
	for path, record := range other.records {
		copied := *record
		snapshot[path] = &copied
	}
	other.mu.RUnlock()

	// Now acquire our lock and merge the snapshot
	r.mu.Lock()
	defer r.mu.Unlock()

	for path, record := range snapshot {
		existing, exists := r.records[path]
		if !exists {
			r.records[path] = record
		} else {
			// Merge: take latest timestamps, sum access counts
			if record.ReadAt.After(existing.ReadAt) {
				existing.ReadAt = record.ReadAt
			}
			if record.WriteAt.After(existing.WriteAt) {
				existing.WriteAt = record.WriteAt
			}
			existing.AccessCount += record.AccessCount
			existing.Priority = r.calculatePriority(existing)
		}
	}
}

// Context functions

// WithRecorder attaches a FileAccessRecorder to the context.
func WithRecorder(ctx context.Context, recorder *Recorder) context.Context {
	return context.WithValue(ctx, recorderKey, recorder)
}

// FromContext retrieves the FileAccessRecorder from context.
// Returns nil if no recorder is attached.
func FromContext(ctx context.Context) *Recorder {
	if recorder, ok := ctx.Value(recorderKey).(*Recorder); ok {
		return recorder
	}
	return nil
}

// RecordAccess is a helper that records file access from a context.
// It does nothing if no recorder is attached to the context.
func RecordAccess(ctx context.Context, path string, isWrite bool, estimatedTokens int) {
	recorder := FromContext(ctx)
	if recorder == nil {
		return
	}

	if isWrite {
		recorder.RecordWrite(path, estimatedTokens)
	} else {
		recorder.RecordRead(path, estimatedTokens)
	}
}

// EstimateTokens provides a rough estimate of token count from byte length.
// Uses the approximation of ~4 characters per token.
func EstimateTokens(byteLength int) int {
	if byteLength <= 0 {
		return 0
	}
	// Rough approximation: 4 characters per token
	return byteLength / 4
}
