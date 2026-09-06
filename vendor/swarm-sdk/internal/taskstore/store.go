// Package taskstore provides persistent task storage for conversations.
// Tasks are persisted to task.json in the conversation metadata directory,
// enabling task state to survive compaction and be shared across conversation branches.
package taskstore

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/atomicfile"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/journal"
)

// Version is the current task store format version.
//
// 1.2.0 adds lossless runtime fields (active focus, metadata, typed notes,
// compact audit events) while retaining additive JSON compatibility with 1.0/1.1.
const Version = "1.2.0"

// Status represents the status of a task.
type Status string

const (
	StatusPending    Status = "pending"
	StatusInProgress Status = "in_progress"
	StatusCompleted  Status = "completed"
	StatusDeleted    Status = "deleted"
)

// Priority represents the priority level of a task.
type Priority string

const (
	PriorityLow      Priority = "low"
	PriorityMedium   Priority = "medium"
	PriorityHigh     Priority = "high"
	PriorityCritical Priority = "critical"
)

// TaskNote is a typed observation persisted with a task.
type TaskNote struct {
	Type      string         `json:"type,omitempty"`
	Content   string         `json:"content"`
	CreatedAt time.Time      `json:"created_at"`
	Metadata  map[string]any `json:"metadata,omitempty"`
}

// TaskAuditEvent is a compact, sanitized tool event persisted with a task.
type TaskAuditEvent struct {
	Type      string         `json:"type"`
	Timestamp time.Time      `json:"timestamp"`
	Actor     string         `json:"actor,omitempty"`
	Summary   string         `json:"summary,omitempty"`
	Metadata  map[string]any `json:"metadata,omitempty"`
}

// Task represents a single task with full metadata.
type Task struct {
	// ID is the unique identifier for the task.
	ID string `json:"id"`

	// Subject is the brief task title (imperative form).
	Subject string `json:"subject"`

	// Description is the detailed task description.
	Description string `json:"description"`

	// Status is the current task status.
	Status Status `json:"status"`

	// Priority is the task priority level.
	Priority Priority `json:"priority"`

	// Category is the type of work this task involves.
	// Valid values: researching, planning, acting, verifying, debugging.
	Category string `json:"category,omitempty"`

	// ActiveForm is the present continuous form for UI display (e.g., "Running tests").
	ActiveForm string `json:"active_form,omitempty"`

	// Metadata carries arbitrary JSON-compatible task annotations.
	Metadata map[string]any `json:"metadata,omitempty"`

	// Active marks the sole task currently receiving focus.
	Active bool `json:"active,omitempty"`

	// DependsOn is a list of task IDs this task depends on.
	DependsOn []string `json:"depends_on,omitempty"`

	// Blocks is a list of task IDs that depend on this task.
	Blocks []string `json:"blocks,omitempty"`

	// CreatedAt is when the task was created.
	CreatedAt time.Time `json:"created_at"`

	// UpdatedAt is when the task was last modified.
	UpdatedAt time.Time `json:"updated_at"`

	// CompletedAt is when the task was completed (if applicable).
	CompletedAt *time.Time `json:"completed_at,omitempty"`

	// SourceTurn is the conversation turn when the task was created.
	SourceTurn int `json:"source_turn"`

	// LastSeen is when the task was last accessed (for staleness detection).
	LastSeen time.Time `json:"last_seen"`

	// Notes contains legacy plain-string observations.
	Notes []string `json:"notes,omitempty"`

	// TypedNotes contains structured observations.
	TypedNotes []TaskNote `json:"typed_notes,omitempty"`

	// AuditEvents contains compact sanitized tool events.
	AuditEvents []TaskAuditEvent `json:"audit_events,omitempty"`

	// OwnerID identifies which agent/session owns this task. Empty means "user" or main agent.
	OwnerID string `json:"owner_id,omitempty"`

	// Sequence is the order within an owner group (for stable display).
	Sequence int `json:"sequence,omitempty"`

	// ParentID is the hierarchy edge: the task that decomposed into this
	// one. This is distinct from DependsOn (a wait-for / scheduling edge).
	// Empty means "top-level task".
	ParentID string `json:"parent_id,omitempty"`

	// SiblingIndex is the parent-scoped order used for x.y.z display
	// numbering. Density is enforced when AddDecomposedSubtask is the
	// caller (max+1); manually-added subtasks may have gaps.
	SiblingIndex int `json:"sibling_index,omitempty"`

	// OriginPromptID is the stable id of the user prompt that initiated
	// the subtree this task belongs to. Children inherit this from their
	// parent at creation time.
	OriginPromptID string `json:"origin_prompt_id,omitempty"`

	// PlanID is the id of the plan-mode plan this task derives from.
	// Empty when the task was created outside plan mode. Children inherit
	// from parent at creation time.
	PlanID string `json:"plan_id,omitempty"`

	// DecomposedBy records who created this task. Values:
	//   ""                          unset / legacy
	//   "user"                      created via the user-facing TaskCreate tool
	//   "agent"                     created by the main agent (not as a decomposition)
	//   "agent:<parentID>"          created as a child of <parentID> by the main agent
	//   "background-worker:<id>"    created by a TaskDecomposer subagent
	//   "steering"                  created by the steering subagent
	DecomposedBy string `json:"decomposed_by,omitempty"`

	// DecompositionAttempts counts how many times a TaskDecomposer has
	// been invoked on this task. Lets the host distinguish "never tried"
	// from "tried and failed" without inspecting LastDecompositionError.
	DecompositionAttempts int `json:"decomposition_attempts,omitempty"`

	// LastDecompositionError is the most recent error message from a
	// failed decomposition attempt on this task. Empty when no failure
	// has occurred (or after a successful retry). Surfaced in the TUI so
	// the user can choose to retry or skip.
	LastDecompositionError string `json:"last_decomposition_error,omitempty"`
}

// Store is the persistent task store.
type Store struct {
	// Version is the store format version.
	Version string `json:"version"`

	// ConversationID is the ID of the conversation this store belongs to.
	ConversationID string `json:"conversation_id"`

	// ParentID is the ID of the parent conversation (for branches).
	ParentID *string `json:"parent_id,omitempty"`

	// Tasks is the list of tasks.
	Tasks []Task `json:"tasks"`

	// LastUpdated is when the store was last modified.
	LastUpdated time.Time `json:"last_updated"`

	// Checksum is the integrity check of the data.
	Checksum string `json:"checksum"`

	// Internal state
	filePath string
	mu       sync.RWMutex

	// journalWriter is the optional additive execution-journal sink for
	// this store (see docs/architecture/swarm-attach/adr-006-execution-journal-privacy.md
	// and .swarmflow/.../p05-til-execution-journal/CONTRACT.md's "Shared
	// type seam"). It is nil for taskstores that predate this wiring, or
	// tests/call sites that never call SetJournalWriter -- callers that
	// emit journal records elsewhere in this package MUST treat a nil
	// journalWriter as a no-op and never panic on it. journalWriter is
	// never marshaled: it carries no `json` tag and Store's persisted
	// shape (task.json) is unchanged by its presence.
	journalWriter journal.Writer
}

// SetJournalWriter attaches an additive, best-effort execution-journal
// sink to the store. Passing nil clears any previously attached writer,
// reverting subsequent mutations to the pre-journal no-op behavior. This
// is purely additive: it does not affect Save/Load, the checksum, or the
// persisted task.json shape in any way, and callers may safely leave it
// unset for taskstores that don't need journaling.
func (s *Store) SetJournalWriter(w journal.Writer) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.journalWriter = w
}

// journalWriterLocked returns the store's current journal writer. Callers
// MUST already hold s.mu (read or write) before calling this; it exists so
// call sites that already hold the lock (e.g. SyncTodos) can read the
// writer without re-entering the mutex.
func (s *Store) journalWriterLocked() journal.Writer {
	return s.journalWriter
}

// directory returns the directory containing this store's task.json, or
// "" if the store has no known file path (e.g. constructed without New).
// This is used only to derive a best-effort workspace root for journal
// field redaction; it is never persisted and does not change task.json's
// shape.
func (s *Store) directory() string {
	if s.filePath == "" {
		return ""
	}
	return filepath.Dir(s.filePath)
}

// Config configures the task store.
type Config struct {
	// ConversationID is the ID of the conversation.
	ConversationID string

	// MetadataDir is the path to the conversation metadata directory.
	MetadataDir string

	// ParentID is the ID of the parent conversation (for branches).
	ParentID *string
}

var writeTaskStoreFile = func(path string, data []byte) error {
	return atomicfile.Write(path, data, atomicfile.WithPerm(0o644))
}

// New creates a new task store.
func New(cfg Config) *Store {
	return &Store{
		Version:        Version,
		ConversationID: cfg.ConversationID,
		ParentID:       cfg.ParentID,
		Tasks:          []Task{},
		LastUpdated:    time.Now(),
		filePath:       filepath.Join(cfg.MetadataDir, "task.json"),
	}
}

// Load loads a task store from disk, or creates a new one if it doesn't exist.
func Load(cfg Config) (*Store, error) {
	store := New(cfg)
	var notFound bool
	err := atomicfile.WithLock(store.filePath, func() error {
		data, err := os.ReadFile(store.filePath)
		if err != nil {
			if os.IsNotExist(err) {
				notFound = true
				return nil
			}
			return fmt.Errorf("failed to read task store: %w", err)
		}

		if err := json.Unmarshal(data, store); err != nil {
			return fmt.Errorf("failed to parse task store: %w", err)
		}

		// Verify the checksum before mutating legacy audit values.
		if store.Checksum != "" {
			expected := store.calculateChecksum()
			if store.Checksum != expected {
				return fmt.Errorf("task store checksum mismatch (data may be corrupted)")
			}
		}

		store.filePath = filepath.Join(cfg.MetadataDir, "task.json")
		if redactStoreTaskAudits(store.Tasks) {
			if err := store.saveUnlocked(); err != nil {
				return fmt.Errorf("failed to persist redacted task audit history: %w", err)
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if notFound {
		return store, nil
	}
	return store, nil
}

// Save persists the task store to disk.
func (s *Store) Save() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	return atomicfile.WithLock(s.filePath, s.saveUnlocked)
}

// saveUnlocked writes the current state while the caller holds both the
// store mutex (when the store is shared) and the per-path atomicfile lock.
func (s *Store) saveUnlocked() error {
	redactStoreTaskAudits(s.Tasks)
	s.LastUpdated = time.Now()
	s.Checksum = s.calculateChecksum()

	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal task store: %w", err)
	}

	if err := writeTaskStoreFile(s.filePath, data); err != nil {
		return fmt.Errorf("failed to atomically write task store: %w", err)
	}
	return nil
}

// calculateChecksum calculates the SHA256 checksum of the task data.
func (s *Store) calculateChecksum() string {
	// Create a copy without the checksum or mutex for hashing
	data, err := json.Marshal(&Store{
		Version:     s.Version,
		Tasks:       s.Tasks,
		Checksum:    "",
		LastUpdated: s.LastUpdated,
	})
	if err != nil {
		return ""
	}

	hash := sha256.Sum256(data)
	return hex.EncodeToString(hash[:])
}

// AddTask adds a new task to the store.
func (s *Store) AddTask(task Task) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	task = cloneTask(task)
	task.AuditEvents, _ = redactTaskAuditEvents(task.AuditEvents)

	// Check for duplicate ID
	for _, t := range s.Tasks {
		if t.ID == task.ID {
			return fmt.Errorf("task with ID %s already exists", task.ID)
		}
	}

	// Validate hierarchy: parent must exist (when set) and the new task
	// must not introduce a cycle. A task that points to itself as parent
	// is rejected as a special case of cycle.
	if task.ParentID != "" {
		if task.ParentID == task.ID {
			return ErrCycleDetected
		}
		if _, ok := s.findTaskLocked(task.ParentID); !ok {
			return fmt.Errorf("task: parent %q not found", task.ParentID)
		}
		if err := s.validateNoCycleLocked(task.ID, task.ParentID); err != nil {
			return err
		}
	}

	// Set timestamps if not set
	if task.CreatedAt.IsZero() {
		task.CreatedAt = time.Now()
	}
	if task.UpdatedAt.IsZero() {
		task.UpdatedAt = time.Now()
	}
	task.LastSeen = time.Now()

	s.Tasks = append(s.Tasks, task)
	return nil
}

// UpdateTask updates an existing task.
func (s *Store) UpdateTask(task Task) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	task = cloneTask(task)
	task.AuditEvents, _ = redactTaskAuditEvents(task.AuditEvents)

	for i, t := range s.Tasks {
		if t.ID == task.ID {
			// If the caller is changing ParentID, re-validate the
			// hierarchy: parent must exist and the move must not
			// introduce a cycle.
			if task.ParentID != t.ParentID && task.ParentID != "" {
				if task.ParentID == task.ID {
					return ErrCycleDetected
				}
				if _, ok := s.findTaskLocked(task.ParentID); !ok {
					return fmt.Errorf("task: parent %q not found", task.ParentID)
				}
				if err := s.validateNoCycleLocked(task.ID, task.ParentID); err != nil {
					return err
				}
			}
			task.UpdatedAt = time.Now()
			task.LastSeen = time.Now()
			s.Tasks[i] = task
			return nil
		}
	}

	return fmt.Errorf("task with ID %s not found", task.ID)
}

// DeleteTask marks a task as deleted.
func (s *Store) DeleteTask(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	for i, t := range s.Tasks {
		if t.ID == id {
			now := time.Now()
			s.Tasks[i].Status = StatusDeleted
			s.Tasks[i].UpdatedAt = now
			s.Tasks[i].CompletedAt = &now
			return nil
		}
	}

	return fmt.Errorf("task with ID %s not found", id)
}

// GetTask retrieves a task by ID.
func (s *Store) GetTask(id string) (Task, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	for _, t := range s.Tasks {
		if t.ID == id {
			return cloneTask(t), nil
		}
	}

	return Task{}, fmt.Errorf("task with ID %s not found", id)
}

// GetAllTasks returns all tasks.
func (s *Store) GetAllTasks() []Task {
	s.mu.RLock()
	defer s.mu.RUnlock()

	result := cloneTasks(s.Tasks)
	return result
}

// GetActiveTasks returns all non-completed, non-deleted tasks.
func (s *Store) GetActiveTasks() []Task {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var result []Task
	for _, t := range s.Tasks {
		if t.Status != StatusCompleted && t.Status != StatusDeleted {
			result = append(result, cloneTask(t))
		}
	}
	return result
}

// GetPendingTasks returns all pending tasks.
func (s *Store) GetPendingTasks() []Task {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var result []Task
	for _, t := range s.Tasks {
		if t.Status == StatusPending {
			result = append(result, cloneTask(t))
		}
	}
	return result
}

// GetInProgressTasks returns all in-progress tasks.
func (s *Store) GetInProgressTasks() []Task {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var result []Task
	for _, t := range s.Tasks {
		if t.Status == StatusInProgress {
			result = append(result, cloneTask(t))
		}
	}
	return result
}

// GetCompletedTasks returns all completed tasks.
func (s *Store) GetCompletedTasks() []Task {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var result []Task
	for _, t := range s.Tasks {
		if t.Status == StatusCompleted {
			result = append(result, cloneTask(t))
		}
	}
	return result
}

// SetTaskStatus updates the status of a task.
func (s *Store) SetTaskStatus(id string, status Status) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	for i, t := range s.Tasks {
		if t.ID == id {
			now := time.Now()
			s.Tasks[i].Status = status
			s.Tasks[i].UpdatedAt = now
			s.Tasks[i].LastSeen = now

			if status == StatusCompleted || status == StatusDeleted {
				if t.Status != status || t.CompletedAt == nil {
					s.Tasks[i].CompletedAt = &now
				}
				s.Tasks[i].Active = false
			} else {
				s.Tasks[i].CompletedAt = nil
				if status == StatusInProgress {
					for j := range s.Tasks {
						s.Tasks[j].Active = j == i
					}
				} else {
					s.Tasks[i].Active = false
				}
			}

			return nil
		}
	}

	return fmt.Errorf("task with ID %s not found", id)
}

// MarkInProgress marks a task as in progress.
func (s *Store) MarkInProgress(id string) error {
	return s.SetTaskStatus(id, StatusInProgress)
}

// MarkCompleted marks a task as completed.
func (s *Store) MarkCompleted(id string) error {
	return s.SetTaskStatus(id, StatusCompleted)
}

// Merge merges tasks from another store (for branch merging).
func (s *Store) Merge(other *Store) error {
	if other == nil {
		return nil
	}

	// Guard against self-merge
	if other == s {
		return nil
	}

	// Take a snapshot of other.Tasks while holding its read lock
	other.mu.RLock()
	otherTasks := cloneTasks(other.Tasks)
	other.mu.RUnlock()

	// Now acquire our lock and merge the snapshot
	s.mu.Lock()
	defer s.mu.Unlock()

	// Create a map of existing tasks
	existing := make(map[string]int)
	for i, t := range s.Tasks {
		existing[t.ID] = i
	}

	// Merge tasks from other store
	for _, otherTask := range otherTasks {
		if idx, ok := existing[otherTask.ID]; ok {
			// Task exists - take the newer version
			if otherTask.UpdatedAt.After(s.Tasks[idx].UpdatedAt) {
				s.Tasks[idx] = otherTask
			}
		} else {
			// New task - add it
			s.Tasks = append(s.Tasks, otherTask)
		}
	}

	return nil
}

// PruneCompleted removes completed tasks older than the specified duration.
func (s *Store) PruneCompleted(olderThan time.Duration) int {
	s.mu.Lock()
	defer s.mu.Unlock()

	cutoff := time.Now().Add(-olderThan)
	var pruned int
	var remaining []Task

	for _, t := range s.Tasks {
		if t.Status == StatusCompleted && t.CompletedAt != nil && t.CompletedAt.Before(cutoff) {
			pruned++
			continue
		}
		remaining = append(remaining, t)
	}

	s.Tasks = remaining
	return pruned
}

// ExportForCompaction exports tasks in a format suitable for compaction context.
func (s *Store) ExportForCompaction() []TaskSummary {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var summaries []TaskSummary
	for _, t := range s.Tasks {
		if t.Status != StatusDeleted {
			summaries = append(summaries, TaskSummary{
				ID:          t.ID,
				Subject:     t.Subject,
				Description: t.Description,
				Status:      string(t.Status),
				ActiveForm:  t.ActiveForm,
			})
		}
	}
	return summaries
}

// TaskSummary is a simplified task representation for compaction.
type TaskSummary struct {
	ID          string `json:"id"`
	Subject     string `json:"subject"`
	Description string `json:"description"`
	Status      string `json:"status"`
	ActiveForm  string `json:"activeForm,omitempty"`
}
