package cloudsync

import (
	"errors"
	"sync"
	"time"
)

// ConflictMode constants
const (
	ConflictModeAuto   = "auto"
	ConflictModeManual = "manual"
)

// Resolution constants
const (
	ResolutionLocal = "local"
	ResolutionCloud = "cloud"
)

// ErrConflictNotFound is returned when a conflict ID is not found
var ErrConflictNotFound = errors.New("conflict not found")

// ErrInvalidMode is returned when an invalid conflict mode is provided
var ErrInvalidMode = errors.New("invalid conflict mode")

// ErrInvalidResolution is returned when an invalid resolution is provided
var ErrInvalidResolution = errors.New("invalid resolution")

// PendingConflict represents a sync conflict waiting for resolution
type PendingConflict struct {
	ID             string
	DataType       string
	Key            string
	LocalValue     string
	CloudValue     string
	LocalUpdatedAt time.Time
	CloudUpdatedAt time.Time
}

// ConflictResolution is the result of resolving a conflict
type ConflictResolution struct {
	Value  string
	Source string // "local" or "cloud"
}

// ConflictManager manages conflict mode and pending conflicts
type ConflictManager struct {
	mu        sync.RWMutex
	mode      string
	conflicts map[string]*PendingConflict
}

// NewConflictManager creates a new ConflictManager with default auto mode
func NewConflictManager() *ConflictManager {
	return &ConflictManager{
		mode:      ConflictModeAuto,
		conflicts: make(map[string]*PendingConflict),
	}
}

// Mode returns the current conflict resolution mode
func (cm *ConflictManager) Mode() string {
	cm.mu.RLock()
	defer cm.mu.RUnlock()
	return cm.mode
}

// SetMode sets the conflict resolution mode
func (cm *ConflictManager) SetMode(mode string) error {
	if mode != ConflictModeAuto && mode != ConflictModeManual {
		return ErrInvalidMode
	}
	cm.mu.Lock()
	defer cm.mu.Unlock()
	cm.mode = mode
	return nil
}

// AddConflict adds a pending conflict
func (cm *ConflictManager) AddConflict(c *PendingConflict) {
	cm.mu.Lock()
	defer cm.mu.Unlock()
	cm.conflicts[c.ID] = c
}

// GetConflict retrieves a conflict by ID, returns nil if not found
func (cm *ConflictManager) GetConflict(id string) *PendingConflict {
	cm.mu.RLock()
	defer cm.mu.RUnlock()
	return cm.conflicts[id]
}

// RemoveConflict removes a conflict by ID (no-op if not found)
func (cm *ConflictManager) RemoveConflict(id string) {
	cm.mu.Lock()
	defer cm.mu.Unlock()
	delete(cm.conflicts, id)
}

// PendingConflicts returns all pending conflicts
func (cm *ConflictManager) PendingConflicts() []*PendingConflict {
	cm.mu.RLock()
	defer cm.mu.RUnlock()
	result := make([]*PendingConflict, 0, len(cm.conflicts))
	for _, c := range cm.conflicts {
		result = append(result, c)
	}
	return result
}

// ClearConflicts removes all pending conflicts
func (cm *ConflictManager) ClearConflicts() {
	cm.mu.Lock()
	defer cm.mu.Unlock()
	cm.conflicts = make(map[string]*PendingConflict)
}

// ResolveConflict resolves a conflict with the specified resolution
func (cm *ConflictManager) ResolveConflict(id string, resolution string) (*ConflictResolution, error) {
	if resolution != ResolutionLocal && resolution != ResolutionCloud {
		return nil, ErrInvalidResolution
	}

	cm.mu.Lock()
	defer cm.mu.Unlock()

	conflict, exists := cm.conflicts[id]
	if !exists {
		return nil, ErrConflictNotFound
	}

	var value string
	if resolution == ResolutionLocal {
		value = conflict.LocalValue
	} else {
		value = conflict.CloudValue
	}

	// Remove the resolved conflict
	delete(cm.conflicts, id)

	return &ConflictResolution{
		Value:  value,
		Source: resolution,
	}, nil
}

// AutoResolve automatically resolves a conflict using last-write-wins strategy
func (cm *ConflictManager) AutoResolve(c *PendingConflict) *ConflictResolution {
	if c.LocalUpdatedAt.After(c.CloudUpdatedAt) {
		return &ConflictResolution{
			Value:  c.LocalValue,
			Source: ResolutionLocal,
		}
	}
	return &ConflictResolution{
		Value:  c.CloudValue,
		Source: ResolutionCloud,
	}
}
