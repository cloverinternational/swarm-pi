// Package ii provides shared state management for productivity tools.
// This module manages the todo list state across all productivity tools,
// providing thread-safe access to the shared todo data.
package ii

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"
)

// TodoStatus represents the status of a todo item
type TodoStatus string

const (
	// TodoStatusPending indicates the task has not been started
	TodoStatusPending TodoStatus = "pending"

	// TodoStatusInProgress indicates the task is currently being worked on
	TodoStatusInProgress TodoStatus = "in_progress"

	// TodoStatusCompleted indicates the task has been finished
	TodoStatusCompleted TodoStatus = "completed"
)

// TodoPriority represents the priority level of a todo item
type TodoPriority string

const (
	// TodoPriorityLow indicates a low priority task
	TodoPriorityLow TodoPriority = "low"

	// TodoPriorityMedium indicates a medium priority task
	TodoPriorityMedium TodoPriority = "medium"

	// TodoPriorityHigh indicates a high priority task
	TodoPriorityHigh TodoPriority = "high"
)

// TaskCategory represents the type of work a task involves.
// Categories help organize and filter tasks by their nature.
type TaskCategory string

const (
	// TaskCategoryResearching involves exploration, reading, searching, understanding
	TaskCategoryResearching TaskCategory = "researching"

	// TaskCategoryPlanning involves design, architecture, strategy
	TaskCategoryPlanning TaskCategory = "planning"

	// TaskCategoryActing involves implementation, coding, writing (default)
	TaskCategoryActing TaskCategory = "acting"

	// TaskCategoryVerifying involves testing, validation, confirmation
	TaskCategoryVerifying TaskCategory = "verifying"

	// TaskCategoryDebugging involves tracing, fixing, error resolution
	TaskCategoryDebugging TaskCategory = "debugging"

	// TaskCategoryDocumenting involves writing docs, comments, README, changelog
	TaskCategoryDocumenting TaskCategory = "documenting"
)

// ValidCategories returns all valid category values.
func ValidCategories() []TaskCategory {
	return []TaskCategory{
		TaskCategoryResearching,
		TaskCategoryPlanning,
		TaskCategoryActing,
		TaskCategoryVerifying,
		TaskCategoryDebugging,
		TaskCategoryDocumenting,
	}
}

// InferCategory attempts to infer a category from task content.
// Returns TaskCategoryActing as default if no keywords match.
// The content is lowercased before matching.
func InferCategory(content string) TaskCategory {
	content = strings.ToLower(content)

	// Priority order: planning > researching > debugging > verifying > acting (default)
	// Planning and research come first — the agent should explore and plan
	// before diving into fixes or implementation.

	// Planning keywords (highest priority — forces research-first approach)
	// NOTE: avoid short substrings that match inside other words (e.g. "spec"
	// matches "inspect"). Use compound phrases or longer forms.
	planningKw := []string{
		"plan", "design", "architect", "strategy",
		"specification", "specify", "outline", "draft", "proposal",
		"tech spec", "write spec", "create spec",
	}
	for _, kw := range planningKw {
		if strings.Contains(content, kw) {
			return TaskCategoryPlanning
		}
	}

	// Researching keywords
	researchingKw := []string{
		"research", "explore", "investigate", "search", "find",
		"analyze", "read", "understand", "study",
		"review docs", "check docs", "examine", "discover",
	}
	for _, kw := range researchingKw {
		if strings.Contains(content, kw) {
			return TaskCategoryResearching
		}
	}

	// Debugging keywords — tightened to require compound phrases so single
	// words like "error" or "fix" in normal task descriptions don't falsely
	// trigger debugging mode (which blocks write tools).
	debuggingKw := []string{
		"debug", "troubleshoot", "trace error", "trace bug",
		"resolve error", "resolve issue", "investigate error",
		"fix bug", "fix error", "fix crash", "fix panic",
		"stack trace", "segfault", "deadlock",
	}
	for _, kw := range debuggingKw {
		if strings.Contains(content, kw) {
			return TaskCategoryDebugging
		}
	}

	// Verifying keywords
	verifyingKw := []string{
		"test", "verify", "validate", "check", "confirm",
		"assert", "ensure", "prove", "inspect",
	}
	for _, kw := range verifyingKw {
		if strings.Contains(content, kw) {
			return TaskCategoryVerifying
		}
	}

	// Documenting keywords
	documentingKw := []string{
		"document", "write docs", "add docs", "update docs",
		"readme", "changelog", "comment", "comments",
		"documentation", "docstring", "javadoc", "godoc",
	}
	for _, kw := range documentingKw {
		if strings.Contains(content, kw) {
			return TaskCategoryDocumenting
		}
	}

	// Default to acting — unrecognized task descriptions should not have
	// write tools blocked. The keyword matchers above already route plan/research
	// tasks to the appropriate category.
	return TaskCategoryActing
}

// TodoNote is a structured observation attached to a todo. Notes is retained on
// TodoItem for compatibility with callers that still use plain strings.
type TodoNote struct {
	Type      string         `json:"type,omitempty"`
	Content   string         `json:"content"`
	CreatedAt time.Time      `json:"created_at"`
	Metadata  map[string]any `json:"metadata,omitempty"`
}

// TodoAuditEvent is the compact, task-local audit representation.
type TodoAuditEvent struct {
	Type      string         `json:"type"`
	Timestamp time.Time      `json:"timestamp"`
	Actor     string         `json:"actor,omitempty"`
	Summary   string         `json:"summary,omitempty"`
	Metadata  map[string]any `json:"metadata,omitempty"`
}

// Descriptive aliases for callers that prefer purpose-oriented names.
type TypedTodoNote = TodoNote
type CompactAuditEvent = TodoAuditEvent

// TodoItem represents a single todo item with all its attributes
type TodoItem struct {
	// ID is the unique identifier for the todo item (typically starts from "1")
	ID string `json:"id"`

	// Content is the description of what needs to be done
	Content string `json:"content"`
	// Description contains detailed acceptance criteria or context.
	Description string `json:"description,omitempty"`
	// ActiveForm is the present-continuous label shown while work is active.
	ActiveForm string `json:"active_form,omitempty"`
	// Metadata carries JSON-compatible runtime annotations.
	Metadata map[string]any `json:"metadata,omitempty"`

	// Status represents the current state of the todo item
	Status TodoStatus `json:"status"`

	// Priority indicates the importance level of the task
	Priority TodoPriority `json:"priority"`

	// Category indicates the type of work this task involves.
	// Valid values: researching, planning, acting, verifying, debugging.
	// Empty defaults to "acting" via InferCategory().
	Category TaskCategory `json:"category,omitempty"`

	// DependsOn lists IDs of tasks that must be completed before this task can start.
	// An empty or nil slice means no dependencies.
	DependsOn []string `json:"depends_on,omitempty"`

	// Blocks lists IDs of tasks that are waiting on this task to complete.
	// This is a computed field populated automatically from DependsOn relationships.
	Blocks []string `json:"blocks,omitempty"`

	// Notes contains observation entries appended during task execution.
	// Each note is a timestamped string capturing progress, decisions, or findings.
	Notes []string `json:"notes,omitempty"`
	// TypedNotes contains structured observations for new callers.
	TypedNotes []TodoNote `json:"typed_notes,omitempty"`
	// AuditEvents is the compact task-local audit trail.
	AuditEvents []TodoAuditEvent `json:"audit_events,omitempty"`
	// Active marks the single task currently receiving focus.
	Active bool `json:"active,omitempty"`

	// OwnerID identifies which agent/session owns this task. Empty means "user" or main agent.
	OwnerID string `json:"owner_id,omitempty"`
	// Sequence is the order within an owner group (for stable display)
	Sequence int `json:"sequence,omitempty"`
	// ParentID is the hierarchy edge and is independent of DependsOn.
	ParentID    string     `json:"parent_id,omitempty"`
	CreatedAt   time.Time  `json:"created_at,omitempty"`
	UpdatedAt   time.Time  `json:"updated_at,omitempty"`
	CompletedAt *time.Time `json:"completed_at,omitempty"`
	// Provenance fields mirror taskstore.Task so conversion is lossless.
	SourceTurn             int       `json:"source_turn,omitempty"`
	LastSeen               time.Time `json:"last_seen,omitempty"`
	SiblingIndex           int       `json:"sibling_index,omitempty"`
	OriginPromptID         string    `json:"origin_prompt_id,omitempty"`
	PlanID                 string    `json:"plan_id,omitempty"`
	DecomposedBy           string    `json:"decomposed_by,omitempty"`
	DecompositionAttempts  int       `json:"decomposition_attempts,omitempty"`
	LastDecompositionError string    `json:"last_decomposition_error,omitempty"`
}

// Validate checks if the TodoItem has valid field values
func (t *TodoItem) Validate() error {
	if strings.TrimSpace(t.ID) == "" {
		return errors.New("todo ID cannot be empty")
	}

	if strings.TrimSpace(t.Content) == "" {
		return errors.New("todo content cannot be empty")
	}

	switch t.Status {
	case TodoStatusPending, TodoStatusInProgress, TodoStatusCompleted:
		// Valid status
	default:
		return fmt.Errorf("invalid status '%s': must be 'pending', 'in_progress', or 'completed'", t.Status)
	}

	switch t.Priority {
	case TodoPriorityLow, TodoPriorityMedium, TodoPriorityHigh:
		// Valid priority
	default:
		return fmt.Errorf("invalid priority '%s': must be 'low', 'medium', or 'high'", t.Priority)
	}

	// Validate category (empty is allowed, defaults to "acting")
	if t.Category != "" {
		valid := slices.Contains(ValidCategories(), t.Category)
		if !valid {
			return fmt.Errorf("invalid category '%s': must be 'researching', 'planning', 'acting', 'verifying', or 'debugging'", t.Category)
		}
	}

	// Validate dependencies don't create self-reference
	if slices.Contains(t.DependsOn, t.ID) {
		return fmt.Errorf("task cannot depend on itself: %s", t.ID)
	}
	if t.ParentID == t.ID {
		return fmt.Errorf("task cannot be its own parent: %s", t.ID)
	}
	if t.Active && t.Status != TodoStatusInProgress {
		return fmt.Errorf("active task %s must be in_progress", t.ID)
	}
	if err := validateJSONValue("metadata", t.Metadata); err != nil {
		return err
	}
	for i := range t.TypedNotes {
		if err := validateJSONValue(fmt.Sprintf("typed_notes[%d].metadata", i), t.TypedNotes[i].Metadata); err != nil {
			return err
		}
	}
	for i := range t.AuditEvents {
		if err := validateJSONValue(fmt.Sprintf("audit_events[%d].metadata", i), t.AuditEvents[i].Metadata); err != nil {
			return err
		}
	}
	return nil
}

func validateJSONValue(name string, value any) error {
	if value == nil {
		return nil
	}
	if _, err := json.Marshal(value); err != nil {
		return fmt.Errorf("%s must contain valid JSON values: %w", name, err)
	}
	return nil
}

// Clone creates a deep copy of the TodoItem
func (t *TodoItem) Clone() TodoItem {
	clone := *t
	clone.Metadata = cloneJSONMap(t.Metadata)
	clone.Notes = slices.Clone(t.Notes)
	clone.DependsOn = slices.Clone(t.DependsOn)
	clone.Blocks = slices.Clone(t.Blocks)
	if t.CompletedAt != nil {
		completedAt := *t.CompletedAt
		clone.CompletedAt = &completedAt
	}
	if t.TypedNotes != nil {
		clone.TypedNotes = make([]TodoNote, len(t.TypedNotes))
		for i, note := range t.TypedNotes {
			clone.TypedNotes[i] = note
			clone.TypedNotes[i].Metadata = cloneJSONMap(note.Metadata)
		}
	}
	if t.AuditEvents != nil {
		clone.AuditEvents = make([]TodoAuditEvent, len(t.AuditEvents))
		for i, event := range t.AuditEvents {
			clone.AuditEvents[i] = event
			clone.AuditEvents[i].Metadata = cloneJSONMap(event.Metadata)
		}
	}
	return clone
}
func cloneJSONMap(src map[string]any) map[string]any {
	if src == nil {
		return nil
	}
	return cloneJSONValue(src).(map[string]any)
}
func cloneJSONValue(value any) any {
	switch v := value.(type) {
	case map[string]any:
		out := make(map[string]any, len(v))
		for key, item := range v {
			out[key] = cloneJSONValue(item)
		}
		return out
	case []any:
		out := make([]any, len(v))
		for i, item := range v {
			out[i] = cloneJSONValue(item)
		}
		return out
	case []map[string]any:
		out := make([]map[string]any, len(v))
		for i, item := range v {
			out[i] = cloneJSONMap(item)
		}
		return out
	default:
		return value
	}
}

// ProgressCallback is called when task progress changes for an owner.
type ProgressCallback func(ownerID string, completed, total int, current string)

// Syncer defines an interface for persisting todo state.
// Implementations can sync to disk, database, or any storage backend.
// This interface is intentionally small to make it easy to implement.
type Syncer interface {
	// Sync persists the current todo items. The implementation should
	// handle any necessary serialization and storage.
	Sync(items []TodoItem) error
}

// TodoManager manages the todo list state across productivity tools.
// It provides thread-safe operations for reading and modifying the todo list.
type TodoManager struct {
	todos []TodoItem
	mu    sync.RWMutex
	// commitMu serializes mutations across persistence while allowing readers
	// to inspect the last committed state during a potentially slow sync.
	commitMu sync.Mutex

	// contextOwner is the current owner ID for new tasks (set by subagent executor)
	contextOwner string
	// sequenceCounter tracks sequence numbers per owner
	sequenceCounter map[string]int
	// progressCallback is called when task status changes
	progressCallback ProgressCallback
	// syncer is called automatically after state changes to persist todos
	syncer Syncer

	// keyRegistryMu guards keyRegistry. Kept separate from mu/commitMu so
	// registering a TaskManage operation key never contends with todo
	// read/write locking.
	keyRegistryMu sync.RWMutex
	// keyRegistry durably maps a TaskManage operation "key" (e.g. "build")
	// to the task ID it last resolved to, across SEPARATE TaskManage tool
	// calls within this session/process. Within a single TaskManage call,
	// refs resolve via the batch-local results map first (see
	// resolveTaskOperation in task_operation.go); this registry is the
	// fallback that lets a key created in one call keep working in a later
	// call — see GitHub issue #73 ("ref-based task references silently
	// fail across separate tool calls"). A key is registered only after
	// the operation that produced it has actually committed (sequential
	// mode: immediately per operation; atomic mode: only after the whole
	// batch commits), so a rolled-back atomic operation never registers a
	// key. Last write for a given key wins, matching the natural pattern
	// of an agent reusing a short label like "task1" turn after turn.
	keyRegistry map[string]string
}

// NewTodoManager creates a new TodoManager with an empty todo list
func NewTodoManager() *TodoManager {
	return &TodoManager{
		todos:           make([]TodoItem, 0),
		sequenceCounter: make(map[string]int),
		keyRegistry:     make(map[string]string),
	}
}

// RememberTaskKey durably records that the TaskManage operation key "key"
// resolved to task ID "id", so a later, separate TaskManage call can pass
// taskId: {"ref": key} and resolve it via ResolveTaskKey. Safe to call
// with an empty key or id (a no-op) so callers don't need to guard.
func (m *TodoManager) RememberTaskKey(key, id string) {
	if key == "" || id == "" {
		return
	}
	m.keyRegistryMu.Lock()
	defer m.keyRegistryMu.Unlock()
	if m.keyRegistry == nil {
		m.keyRegistry = make(map[string]string)
	}
	m.keyRegistry[key] = id
}

// ResolveTaskKey looks up a previously remembered TaskManage operation key.
// Returns the task ID and true if found, or "" and false otherwise.
func (m *TodoManager) ResolveTaskKey(key string) (string, bool) {
	m.keyRegistryMu.RLock()
	defer m.keyRegistryMu.RUnlock()
	id, ok := m.keyRegistry[key]
	return id, ok
}

// clearTaskKeyRegistry drops all remembered TaskManage operation keys.
// Called from ClearTodos so a cleared task list doesn't leave stale key
// references resolvable to tasks that no longer exist.
func (m *TodoManager) clearTaskKeyRegistry() {
	m.keyRegistryMu.Lock()
	defer m.keyRegistryMu.Unlock()
	m.keyRegistry = make(map[string]string)
}

// forgetTaskID removes every operation key that resolves to id.
func (m *TodoManager) forgetTaskID(id string) {
	if id == "" {
		return
	}
	m.keyRegistryMu.Lock()
	defer m.keyRegistryMu.Unlock()
	for key, registeredID := range m.keyRegistry {
		if registeredID == id {
			delete(m.keyRegistry, key)
		}
	}
}

// SetSyncer sets the syncer that will be called automatically after state changes.
// Pass nil to disable automatic persistence.
// This method is not thread-safe and should only be called during initialization.
func (m *TodoManager) SetSyncer(syncer Syncer) {
	m.mu.Lock()
	m.syncer = syncer
	m.mu.Unlock()
}

func cloneTodos(items []TodoItem) []TodoItem {
	cloned := make([]TodoItem, len(items))
	for i := range items {
		cloned[i] = items[i].Clone()
	}
	return cloned
}

func cloneSequenceCounters(counters map[string]int) map[string]int {
	cloned := make(map[string]int, len(counters))
	for owner, sequence := range counters {
		cloned[owner] = sequence
	}
	return cloned
}

func (m *TodoManager) lockMutation() {
	m.commitMu.Lock()
	m.mu.Lock()
}

func (m *TodoManager) unlockMutation() {
	m.mu.Unlock()
	m.commitMu.Unlock()
}

// commitTodosLocked persists a complete candidate before publishing it in memory.
// The caller must hold both commitMu and mu. The state lock is released around
// Sync so syncers can safely call manager read methods; commitMu preserves write
// ordering until the candidate is published or rejected.
func (m *TodoManager) commitTodosLocked(candidate []TodoItem) error {
	computeBlocks(candidate)
	syncer := m.syncer
	persisted := cloneTodos(candidate)
	m.mu.Unlock()
	var err error
	if syncer != nil {
		err = syncer.Sync(persisted)
	}
	m.mu.Lock()
	if err != nil {
		return err
	}
	m.todos = candidate
	return nil
}

// mutateAtomically runs a group of manager mutations against an isolated manager
// and publishes them only after the complete candidate has been persisted.
func (m *TodoManager) mutateAtomically(updateFn func(*TodoManager) error) error {
	m.lockMutation()
	defer m.unlockMutation()

	candidate := &TodoManager{
		todos:           cloneTodos(m.todos),
		contextOwner:    m.contextOwner,
		sequenceCounter: cloneSequenceCounters(m.sequenceCounter),
	}
	if err := updateFn(candidate); err != nil {
		return err
	}
	if err := m.commitTodosLocked(candidate.todos); err != nil {
		return err
	}
	m.sequenceCounter = candidate.sequenceCounter
	return nil
}

// GetTodos returns a copy of the current todo list.
// The returned slice is a defensive copy and can be modified safely.
func (m *TodoManager) Todos() []TodoItem {
	m.mu.RLock()
	defer m.mu.RUnlock()

	// Return a defensive copy to prevent external modification
	result := make([]TodoItem, len(m.todos))
	for i, todo := range m.todos {
		result[i] = todo.Clone()
	}
	return result
}

// SetTodos replaces the entire todo list with the provided items.
// It validates all items before applying changes, including dependency validation.
// Returns an error if validation fails; the original list is unchanged on error.
func (m *TodoManager) SetTodos(todos []TodoItem) error {
	normalized := make([]TodoItem, len(todos))
	now := time.Now().UTC()
	activeIndex := -1
	firstInProgress := -1
	for i := range todos {
		normalized[i] = todos[i].Clone()
		requestedActive := normalized[i].Active
		normalized[i].Active = false
		if err := normalized[i].Validate(); err != nil {
			return fmt.Errorf("todo %d: %w", i, err)
		}
		if normalized[i].Status == TodoStatusInProgress && firstInProgress < 0 {
			firstInProgress = i
		}
		if requestedActive && normalized[i].Status == TodoStatusInProgress && activeIndex < 0 {
			activeIndex = i
		}
		normalizeTodoLifecycle(&normalized[i], now)
	}
	// Legacy lists did not carry Active. Choose exactly one focused task,
	// preferring an explicit active marker and otherwise the first in-progress.
	if activeIndex < 0 {
		activeIndex = firstInProgress
	}
	if activeIndex >= 0 {
		normalized[activeIndex].Active = true
	}
	if err := validateTodoGraph(normalized); err != nil {
		return err
	}
	statusByID := make(map[string]TodoStatus, len(normalized))
	for _, todo := range normalized {
		statusByID[todo.ID] = todo.Status
	}
	for _, todo := range normalized {
		if todo.Status == TodoStatusInProgress {
			for _, depID := range todo.DependsOn {
				if statusByID[depID] != TodoStatusCompleted {
					return fmt.Errorf("cannot set task %s to in_progress: dependency %s is not completed (status: %s)", todo.ID, depID, statusByID[depID])
				}
			}
		}
		if todo.Status == TodoStatusCompleted {
			for _, child := range normalized {
				if child.ParentID == todo.ID && child.Status != TodoStatusCompleted {
					return fmt.Errorf("cannot complete task %s: child task %s is not completed", todo.ID, child.ID)
				}
			}
		}
	}
	sequenceCounters := make(map[string]int)
	for _, todo := range normalized {
		if todo.Sequence > sequenceCounters[todo.OwnerID] {
			sequenceCounters[todo.OwnerID] = todo.Sequence
		}
	}
	m.lockMutation()
	defer m.unlockMutation()
	if err := m.commitTodosLocked(normalized); err != nil {
		return err
	}
	m.sequenceCounter = sequenceCounters
	return nil
}

func normalizeTodoLifecycle(todo *TodoItem, now time.Time) {
	if todo.CreatedAt.IsZero() {
		todo.CreatedAt = now
	}
	if todo.UpdatedAt.IsZero() {
		todo.UpdatedAt = todo.CreatedAt
	}
	if todo.Status == TodoStatusCompleted {
		if todo.CompletedAt == nil {
			completedAt := todo.UpdatedAt
			todo.CompletedAt = &completedAt
		}
	} else {
		todo.CompletedAt = nil
	}
}

func validateTodoGraph(todos []TodoItem) error {
	idSet := make(map[string]struct{}, len(todos))
	for _, todo := range todos {
		if _, exists := idSet[todo.ID]; exists {
			return fmt.Errorf("duplicate task ID %s", todo.ID)
		}
		idSet[todo.ID] = struct{}{}
	}
	for _, todo := range todos {
		for _, depID := range todo.DependsOn {
			if _, ok := idSet[depID]; !ok {
				return fmt.Errorf("task %s depends on non-existent task %s", todo.ID, depID)
			}
		}
		if todo.ParentID != "" {
			if _, ok := idSet[todo.ParentID]; !ok {
				return fmt.Errorf("task %s has non-existent parent %s", todo.ID, todo.ParentID)
			}
		}
	}
	if err := validateDirectedCycles(todos, func(todo TodoItem) []string { return todo.DependsOn }, "circular dependency"); err != nil {
		return err
	}
	return validateDirectedCycles(todos, func(todo TodoItem) []string {
		if todo.ParentID == "" {
			return nil
		}
		return []string{todo.ParentID}
	}, "hierarchy cycle")
}

func validateDirectedCycles(todos []TodoItem, edges func(TodoItem) []string, label string) error {
	graph := make(map[string][]string, len(todos))
	for _, todo := range todos {
		graph[todo.ID] = edges(todo)
	}
	state := make(map[string]uint8, len(todos))
	var visit func(string) error
	visit = func(id string) error {
		if state[id] == 1 {
			return fmt.Errorf("%s detected involving task %s", label, id)
		}
		if state[id] == 2 {
			return nil
		}
		state[id] = 1
		for _, next := range graph[id] {
			if err := visit(next); err != nil {
				return err
			}
		}
		state[id] = 2
		return nil
	}
	for id := range graph {
		if err := visit(id); err != nil {
			return err
		}
	}
	return nil
}

// ClearTodos removes all todos from the list.
func (m *TodoManager) ClearTodos() error {
	m.lockMutation()
	defer m.unlockMutation()
	if err := m.commitTodosLocked(make([]TodoItem, 0)); err != nil {
		return err
	}
	m.sequenceCounter = make(map[string]int)
	m.clearTaskKeyRegistry()
	return nil
}

// Count returns the number of todos in the list
func (m *TodoManager) Count() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.todos)
}

// GetByID returns a todo item by its ID, or nil if not found
func (m *TodoManager) ByID(id string) *TodoItem {
	m.mu.RLock()
	defer m.mu.RUnlock()

	for _, todo := range m.todos {
		if todo.ID == id {
			clone := todo.Clone()
			return &clone
		}
	}
	return nil
}

// ChildrenOf returns a parent's direct children ordered by SiblingIndex, then
// creation time for deterministic legacy ordering.
func (m *TodoManager) ChildrenOf(parentID string) []TodoItem {
	m.mu.RLock()
	defer m.mu.RUnlock()
	children := make([]TodoItem, 0)
	for i := range m.todos {
		if m.todos[i].ParentID == parentID {
			children = append(children, m.todos[i].Clone())
		}
	}
	sort.SliceStable(children, func(i, j int) bool {
		if children[i].SiblingIndex != children[j].SiblingIndex {
			return children[i].SiblingIndex < children[j].SiblingIndex
		}
		return children[i].CreatedAt.Before(children[j].CreatedAt)
	})
	return children
}

// FocusTodo atomically transfers focus to an already-in-progress task.
func (m *TodoManager) FocusTodo(id string) error {
	m.lockMutation()
	defer m.unlockMutation()
	index := -1
	for i := range m.todos {
		if m.todos[i].ID == id {
			index = i
			break
		}
	}
	if index < 0 {
		return fmt.Errorf("todo with ID %s not found", id)
	}
	if m.todos[index].Status != TodoStatusInProgress {
		return fmt.Errorf("cannot focus task %s: task is not in_progress", id)
	}
	candidate := cloneTodos(m.todos)
	now := time.Now().UTC()
	for i := range candidate {
		if candidate[i].Active != (i == index) {
			candidate[i].Active = i == index
			candidate[i].UpdatedAt = now
		}
	}
	return m.commitTodosLocked(candidate)
}

// SetActiveTodo is an alias for FocusTodo.
func (m *TodoManager) SetActiveTodo(id string) error { return m.FocusTodo(id) }

// AppendAuditEventToActive atomically appends an event to the focused task.
func (m *TodoManager) AppendAuditEventToActive(event TodoAuditEvent) error {
	if err := validateJSONValue("audit event metadata", event.Metadata); err != nil {
		return err
	}
	m.lockMutation()
	defer m.unlockMutation()
	for i := range m.todos {
		if !m.todos[i].Active {
			continue
		}
		if m.todos[i].Status != TodoStatusInProgress {
			return fmt.Errorf("active task %s is not in_progress", m.todos[i].ID)
		}
		if event.Timestamp.IsZero() {
			event.Timestamp = time.Now().UTC()
		}
		event.Metadata = cloneJSONMap(event.Metadata)
		candidate := cloneTodos(m.todos)
		candidate[i].AuditEvents = append(candidate[i].AuditEvents, event)
		candidate[i].UpdatedAt = event.Timestamp
		return m.commitTodosLocked(candidate)
	}
	return errors.New("no active todo")
}

// AppendActiveAuditEvent is a concise alias for AppendAuditEventToActive.
func (m *TodoManager) AppendActiveAuditEvent(event TodoAuditEvent) error {
	return m.AppendAuditEventToActive(event)
}

// GetByStatus returns all todos with the specified status
func (m *TodoManager) ByStatus(status TodoStatus) []TodoItem {
	m.mu.RLock()
	defer m.mu.RUnlock()

	result := make([]TodoItem, 0)
	for _, todo := range m.todos {
		if todo.Status == status {
			result = append(result, todo.Clone())
		}
	}
	return result
}

// GetByPriority returns all todos with the specified priority
func (m *TodoManager) ByPriority(priority TodoPriority) []TodoItem {
	m.mu.RLock()
	defer m.mu.RUnlock()

	result := make([]TodoItem, 0)
	for _, todo := range m.todos {
		if todo.Priority == priority {
			result = append(result, todo.Clone())
		}
	}
	return result
}

// ByCategory returns all todos with the specified category.
// Returns an empty slice if no todos match.
func (m *TodoManager) ByCategory(category TaskCategory) []TodoItem {
	m.mu.RLock()
	defer m.mu.RUnlock()

	result := make([]TodoItem, 0)
	for _, todo := range m.todos {
		if todo.Category == category {
			result = append(result, todo.Clone())
		}
	}
	return result
}

// Summary returns a summary of todo counts by status
func (m *TodoManager) Summary() map[string]int {
	m.mu.RLock()
	defer m.mu.RUnlock()

	summary := map[string]int{
		"pending":     0,
		"in_progress": 0,
		"completed":   0,
		"total":       len(m.todos),
	}

	for _, todo := range m.todos {
		switch todo.Status {
		case TodoStatusPending:
			summary["pending"]++
		case TodoStatusInProgress:
			summary["in_progress"]++
		case TodoStatusCompleted:
			summary["completed"]++
		}
	}

	return summary
}

// ToJSON serializes the todo list to a JSON string.
// Returns the JSON representation of all todos with proper formatting.
func (m *TodoManager) ToJSON() (string, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	data, err := json.MarshalIndent(m.todos, "", "  ")
	if err != nil {
		return "", fmt.Errorf("failed to serialize todos to JSON: %w", err)
	}

	return string(data), nil
}

// FromJSON deserializes a JSON string and sets the todos.
// The JSON should be an array of TodoItem objects.
// Returns an error if parsing fails or validation fails.
func (m *TodoManager) FromJSON(jsonStr string) error {
	var todos []TodoItem

	if err := json.Unmarshal([]byte(jsonStr), &todos); err != nil {
		return fmt.Errorf("failed to parse JSON: %w", err)
	}

	return m.SetTodos(todos)
}

// ComputeTodosHash computes a SHA256 hash of the todos for cache validation.
// The hash is computed from a deterministic JSON serialization of the todos.
func ComputeTodosHash(todos []TodoItem) string {
	// Serialize to JSON for deterministic string representation
	data, err := json.Marshal(todos)
	if err != nil {
		// If serialization fails, return empty hash
		return ""
	}

	hash := sha256.Sum256(data)
	return hex.EncodeToString(hash[:])
}

// ValidateDependencies checks all dependency relationships in the todo list.
// It verifies that all referenced IDs exist and detects circular dependencies.
func (m *TodoManager) ValidateDependencies(todos []TodoItem) error {
	// Build ID set for existence checks
	idSet := make(map[string]bool, len(todos))
	for _, todo := range todos {
		idSet[todo.ID] = true
	}

	// Validate all dependency references exist
	for _, todo := range todos {
		for _, depID := range todo.DependsOn {
			if !idSet[depID] {
				return fmt.Errorf("task %s depends on non-existent task %s", todo.ID, depID)
			}
		}
	}

	// Check for circular dependencies using DFS
	graph := make(map[string][]string, len(todos))
	for _, todo := range todos {
		graph[todo.ID] = todo.DependsOn
	}

	visited := make(map[string]bool)
	recStack := make(map[string]bool)

	var hasCycle func(id string) bool
	hasCycle = func(id string) bool {
		visited[id] = true
		recStack[id] = true

		for _, depID := range graph[id] {
			if !visited[depID] {
				if hasCycle(depID) {
					return true
				}
			} else if recStack[depID] {
				return true
			}
		}

		recStack[id] = false
		return false
	}

	for _, todo := range todos {
		if !visited[todo.ID] {
			if hasCycle(todo.ID) {
				return fmt.Errorf("circular dependency detected involving task %s", todo.ID)
			}
		}
	}

	return nil
}

// ComputeBlocks populates the Blocks field for all todos based on DependsOn relationships.
// Must be called after any state change that affects DependsOn.
func (m *TodoManager) ComputeBlocks() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.computeBlocksLocked()
}

// computeBlocksLocked builds the reverse dependency index. Caller must hold m.mu.
func (m *TodoManager) computeBlocksLocked() { computeBlocks(m.todos) }

func computeBlocks(todos []TodoItem) {
	// Clear all blocks
	for i := range todos {
		todos[i].Blocks = nil
	}

	// Build ID-to-index map for efficient lookup
	idxMap := make(map[string]int, len(todos))
	for i, todo := range todos {
		idxMap[todo.ID] = i
	}

	// Build reverse index: if task A depends on task B, then B blocks A
	for i := range todos {
		for _, depID := range todos[i].DependsOn {
			if j, ok := idxMap[depID]; ok {
				todos[j].Blocks = append(todos[j].Blocks, todos[i].ID)
			}
		}
	}
}

// CheckDependenciesMet verifies that all dependencies of the given task are completed.
// Returns nil if all deps are met, or an error describing the first unmet dependency.
func (m *TodoManager) CheckDependenciesMet(taskID string) error {
	m.mu.RLock()
	defer m.mu.RUnlock()

	// Build status map and find the task
	statusByID := make(map[string]TodoStatus, len(m.todos))
	var deps []string
	found := false
	for i := range m.todos {
		statusByID[m.todos[i].ID] = m.todos[i].Status
		if m.todos[i].ID == taskID {
			deps = m.todos[i].Clone().DependsOn
			found = true
		}
	}

	if !found {
		return fmt.Errorf("task %s not found", taskID)
	}

	for _, depID := range deps {
		if status, ok := statusByID[depID]; ok {
			if status != TodoStatusCompleted {
				return fmt.Errorf("cannot start task %s: dependency %s is not completed (status: %s)", taskID, depID, status)
			}
		}
	}

	return nil
}

// UpdateTodo updates a single todo by ID using the provided update function.
// It validates the update including dependency constraints before applying.
func (m *TodoManager) UpdateTodo(id string, updateFn func(*TodoItem) error) error {
	m.lockMutation()
	defer m.unlockMutation()
	index := -1
	for i := range m.todos {
		if m.todos[i].ID == id {
			index = i
			break
		}
	}
	if index == -1 {
		return fmt.Errorf("todo with ID %s not found", id)
	}
	updated := m.todos[index].Clone()
	oldStatus := updated.Status
	if err := updateFn(&updated); err != nil {
		return err
	}
	if updated.ID != id {
		return errors.New("todo ID cannot be changed")
	}
	if updated.Status != TodoStatusInProgress {
		updated.Active = false
	}
	if err := updated.Validate(); err != nil {
		return err
	}
	if updated.Status == TodoStatusInProgress && oldStatus != TodoStatusInProgress {
		for _, depID := range updated.DependsOn {
			for _, todo := range m.todos {
				if todo.ID == depID && todo.Status != TodoStatusCompleted {
					return fmt.Errorf("cannot start task %s: dependency %s is not completed (status: %s)", id, depID, todo.Status)
				}
			}
		}
	}
	if updated.Status == TodoStatusCompleted {
		for _, child := range m.todos {
			if child.ParentID == id && child.Status != TodoStatusCompleted {
				return fmt.Errorf("cannot complete task %s: child task %s is not completed", id, child.ID)
			}
		}
	}
	tempTodos := cloneTodos(m.todos)
	tempTodos[index] = updated
	if err := validateTodoGraph(tempTodos); err != nil {
		return err
	}
	now := time.Now().UTC()
	updated.UpdatedAt = now
	if updated.CreatedAt.IsZero() {
		updated.CreatedAt = now
	}
	if updated.Status == TodoStatusCompleted {
		if oldStatus != TodoStatusCompleted || updated.CompletedAt == nil {
			completedAt := now
			updated.CompletedAt = &completedAt
		}
	} else {
		updated.CompletedAt = nil
	}
	// A transition into in_progress is also an atomic focus transfer. An
	// already-in-progress task is focused via FocusTodo.
	if updated.Status == TodoStatusInProgress && oldStatus != TodoStatusInProgress {
		for i := range tempTodos {
			tempTodos[i].Active = false
		}
		updated.Active = true
	} else if updated.Status != TodoStatusInProgress {
		updated.Active = false
	} else if updated.Active {
		for i := range tempTodos {
			tempTodos[i].Active = false
		}
	}
	tempTodos[index] = updated
	return m.commitTodosLocked(tempTodos)
}

// AddTodo appends a single todo to the list after validation.
// It checks for duplicate IDs, validates dependencies, and enforces constraints.
func (m *TodoManager) AddTodo(item TodoItem) error {
	// Validate the item first
	if err := item.Validate(); err != nil {
		return err
	}

	m.lockMutation()
	defer m.unlockMutation()

	// A child inherits its parent's owner unless the caller explicitly chose one.
	if item.ParentID != "" && item.OwnerID == "" {
		for i := range m.todos {
			if m.todos[i].ID == item.ParentID {
				item.OwnerID = m.todos[i].OwnerID
				break
			}
		}
	}
	// Use context owner if not set
	if item.OwnerID == "" {
		item.OwnerID = m.contextOwner
	}
	// Auto-assign sequence without consuming it until persistence succeeds.
	autoSequence := item.Sequence == 0
	nextSequence := m.sequenceCounter[item.OwnerID]
	if autoSequence {
		nextSequence++
		item.Sequence = nextSequence
	}

	// Check for duplicate ID
	for _, t := range m.todos {
		if t.ID == item.ID {
			return fmt.Errorf("task with ID %s already exists", item.ID)
		}
	}

	// Build temporary list for dependency validation
	tempTodos := make([]TodoItem, len(m.todos)+1)
	for i, t := range m.todos {
		tempTodos[i] = t.Clone()
	}
	tempTodos[len(m.todos)] = item.Clone()

	// Validate both scheduling and hierarchy graphs.
	if err := validateTodoGraph(tempTodos); err != nil {
		return err
	}

	// Enforce in_progress constraints
	if item.Status == TodoStatusInProgress {
		// Check dependency fulfillment
		for _, depID := range item.DependsOn {
			for _, t := range m.todos {
				if t.ID == depID && t.Status != TodoStatusCompleted {
					return fmt.Errorf("cannot create task as in_progress: dependency %s is not completed (status: %s)", depID, t.Status)
				}
			}
		}
	}

	now := time.Now().UTC()
	normalizeTodoLifecycle(&item, now)
	item.UpdatedAt = now
	candidate := cloneTodos(m.todos)
	if item.Status == TodoStatusInProgress {
		for i := range candidate {
			candidate[i].Active = false
		}
		item.Active = true
	}
	candidate = append(candidate, item.Clone())
	if err := m.commitTodosLocked(candidate); err != nil {
		return err
	}
	if autoSequence {
		m.sequenceCounter[item.OwnerID] = nextSequence
	}
	return nil
}

// AddTodoAutoID appends a single todo with an auto-generated ID.
// The ID is generated atomically under the write lock to prevent race conditions.
// Returns the generated ID and any error.
func (m *TodoManager) AddTodoAutoID(item TodoItem) (string, error) {
	if err := item.Validate(); err != nil && item.ID == "" {
		// Allow empty ID since we'll generate one; re-validate after
	}

	m.lockMutation()
	defer m.unlockMutation()

	// A child inherits its parent's owner unless the caller explicitly chose one.
	if item.ParentID != "" && item.OwnerID == "" {
		for i := range m.todos {
			if m.todos[i].ID == item.ParentID {
				item.OwnerID = m.todos[i].OwnerID
				break
			}
		}
	}
	// Use context owner if not set
	if item.OwnerID == "" {
		item.OwnerID = m.contextOwner
	}
	// Auto-assign sequence without consuming it until persistence succeeds.
	autoSequence := item.Sequence == 0
	nextSequence := m.sequenceCounter[item.OwnerID]
	if autoSequence {
		nextSequence++
		item.Sequence = nextSequence
	}

	// Generate next available ID under the lock
	nextNum := len(m.todos) + 1
	for {
		candidate := fmt.Sprintf("%d", nextNum)
		exists := false
		for _, t := range m.todos {
			if t.ID == candidate {
				exists = true
				break
			}
		}
		if !exists {
			item.ID = candidate
			break
		}
		nextNum++
	}

	// Validate the complete item
	if err := item.Validate(); err != nil {
		return "", err
	}

	// Validate both scheduling and hierarchy graphs.
	tempTodos := make([]TodoItem, len(m.todos)+1)
	for i, t := range m.todos {
		tempTodos[i] = t.Clone()
	}
	tempTodos[len(m.todos)] = item.Clone()
	if err := validateTodoGraph(tempTodos); err != nil {
		return "", err
	}

	// Enforce in_progress constraints
	if item.Status == TodoStatusInProgress {
		for _, depID := range item.DependsOn {
			for _, t := range m.todos {
				if t.ID == depID && t.Status != TodoStatusCompleted {
					return "", fmt.Errorf("cannot create task as in_progress: dependency %s is not completed (status: %s)", depID, t.Status)
				}
			}
		}
	}

	now := time.Now().UTC()
	normalizeTodoLifecycle(&item, now)
	item.UpdatedAt = now
	candidate := cloneTodos(m.todos)
	if item.Status == TodoStatusInProgress {
		for i := range candidate {
			candidate[i].Active = false
		}
		item.Active = true
	}
	candidate = append(candidate, item.Clone())
	if err := m.commitTodosLocked(candidate); err != nil {
		return "", err
	}
	if autoSequence {
		m.sequenceCounter[item.OwnerID] = nextSequence
	}
	return item.ID, nil
}

// DeleteTodo removes a todo by ID. Returns an error if the task is depended on by other tasks.
func (m *TodoManager) DeleteTodo(id string) error {
	m.lockMutation()
	defer m.unlockMutation()

	index := -1
	for i := range m.todos {
		if m.todos[i].ID == id {
			index = i
			break
		}
	}
	if index == -1 {
		return fmt.Errorf("todo with ID %s not found", id)
	}

	// Parents cannot be removed while they still own child tasks.
	for _, child := range m.todos {
		if child.ParentID == id {
			return fmt.Errorf("cannot delete task %s: child task %s still exists", id, child.ID)
		}
	}
	// Check if any other task depends on this one
	for _, t := range m.todos {
		if t.ID == id {
			continue
		}
		if slices.Contains(t.DependsOn, id) {
			return fmt.Errorf("cannot delete task %s: task %s depends on it", id, t.ID)
		}
	}

	candidate := cloneTodos(m.todos)
	candidate = append(candidate[:index], candidate[index+1:]...)
	if err := m.commitTodosLocked(candidate); err != nil {
		return err
	}
	m.forgetTaskID(id)
	return nil
}

// globalManager is the singleton instance of TodoManager
var globalManager *TodoManager
var globalManagerOnce sync.Once

// GetTodoManager returns the global TodoManager instance.
// This function is thread-safe and uses lazy initialization.
func GetTodoManager() *TodoManager {
	globalManagerOnce.Do(func() {
		globalManager = NewTodoManager()
	})
	return globalManager
}

// ResetGlobalManager resets the global manager to a fresh instance.
// This is primarily useful for testing purposes.
// Note: This function is NOT thread-safe with concurrent GetTodoManager calls.
func ResetGlobalManager() {
	globalManagerOnce = sync.Once{}
	globalManager = nil
}

// TodoItemFromMap converts a map[string]any to a TodoItem.
// This is useful when parsing tool input parameters.
func TodoItemFromMap(m map[string]any) (TodoItem, error) {
	var item TodoItem

	id, ok := m["id"].(string)
	if !ok {
		return item, errors.New("id must be a string")
	}
	item.ID = id

	content, ok := m["content"].(string)
	if !ok {
		return item, errors.New("content must be a string")
	}
	item.Content = content

	status, ok := m["status"].(string)
	if !ok {
		return item, errors.New("status must be a string")
	}
	item.Status = TodoStatus(status)

	priority, ok := m["priority"].(string)
	if !ok {
		return item, errors.New("priority must be a string")
	}
	item.Priority = TodoPriority(priority)

	// Parse optional category field
	if category, ok := m["category"].(string); ok {
		item.Category = TaskCategory(category)
	}

	// Parse optional depends_on field
	if depsRaw, ok := m["depends_on"].([]any); ok {
		for _, dep := range depsRaw {
			if depStr, ok := dep.(string); ok {
				item.DependsOn = append(item.DependsOn, depStr)
			}
		}
	}

	return item, nil
}

// TodoItemsFromSlice converts a slice of maps to a slice of TodoItems.
// This is useful when parsing tool input parameters containing multiple todos.
func TodoItemsFromSlice(items []any) ([]TodoItem, error) {
	result := make([]TodoItem, 0, len(items))

	for i, item := range items {
		m, ok := item.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("item %d: expected object, got %T", i, item)
		}

		todoItem, err := TodoItemFromMap(m)
		if err != nil {
			return nil, fmt.Errorf("item %d: %w", i, err)
		}

		result = append(result, todoItem)
	}

	return result, nil
}

// TodoItemToMap converts a TodoItem to a map[string]any.
// This is useful for serializing todo items in tool results.
func TodoItemToMap(item TodoItem) map[string]any {
	m := map[string]any{
		"id":       item.ID,
		"content":  item.Content,
		"status":   string(item.Status),
		"priority": string(item.Priority),
	}
	if item.Category != "" {
		m["category"] = string(item.Category)
	}
	if len(item.Notes) > 0 {
		m["notes"] = item.Notes
	}
	if len(item.DependsOn) > 0 {
		m["depends_on"] = item.DependsOn
	}
	if len(item.Blocks) > 0 {
		m["blocks"] = item.Blocks
	}
	return m
}

// TodoItemsToSlice converts a slice of TodoItems to a slice of maps.
// This is useful for serializing todo lists in tool results.
func TodoItemsToSlice(items []TodoItem) []map[string]any {
	result := make([]map[string]any, len(items))
	for i, item := range items {
		result[i] = TodoItemToMap(item)
	}
	return result
}

// ============================================================================
// Owner-Based Task Management (for sub-agent tracking)
// ============================================================================

// SetContextOwner sets the current context owner. Any new tasks created
// will automatically be assigned this owner ID. This is used by the
// subagent executor to group tasks by sub-agent.
func (m *TodoManager) SetContextOwner(ownerID string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.contextOwner = ownerID
}

// GetContextOwner returns the current context owner.
func (m *TodoManager) ContextOwner() string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.contextOwner
}

// ClearContextOwner clears the context owner, reverting to default behavior.
func (m *TodoManager) ClearContextOwner() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.contextOwner = ""
}

// NextSequence returns the next sequence number for the given owner.
// This is used to maintain stable ordering of tasks within an owner group.
func (m *TodoManager) NextSequence(ownerID string) int {
	m.commitMu.Lock()
	defer m.commitMu.Unlock()
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sequenceCounter[ownerID]++
	return m.sequenceCounter[ownerID]
}

// GetByOwner returns all tasks for a specific owner, ordered by sequence.
func (m *TodoManager) ByOwner(ownerID string) []TodoItem {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var result []TodoItem
	for _, item := range m.todos {
		if item.OwnerID == ownerID {
			result = append(result, item)
		}
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].Sequence < result[j].Sequence
	})
	return result
}

// GetOwnerProgress returns (completed, total, current_task) for an owner.
// This is used by the TUI to show sub-agent progress.
func (m *TodoManager) OwnerProgress(ownerID string) (completed, total int, current string) {
	tasks := m.ByOwner(ownerID)
	total = len(tasks)
	for _, t := range tasks {
		if t.Status == TodoStatusCompleted {
			completed++
		}
		if t.Status == TodoStatusInProgress && current == "" {
			current = t.Content
		}
	}
	return completed, total, current
}

// ClearOwner removes all tasks for an owner. This is called automatically
// when a sub-agent finishes execution.
func (m *TodoManager) ClearOwner(ownerID string) error {
	m.lockMutation()
	defer m.unlockMutation()

	newTodos := make([]TodoItem, 0, len(m.todos))
	for _, item := range m.todos {
		if item.OwnerID != ownerID {
			newTodos = append(newTodos, item.Clone())
		}
	}
	if err := m.commitTodosLocked(newTodos); err != nil {
		return err
	}
	delete(m.sequenceCounter, ownerID)
	return nil
}

// SetProgressCallback sets the callback function for progress updates.
func (m *TodoManager) SetProgressCallback(cb ProgressCallback) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.progressCallback = cb
}

// ============================================================================
// Context-based TodoManager Access (for sub-agent integration)
// ============================================================================

// contextKey is the type for context keys in this package.
type contextKey string

// todoManagerKey is the context key for the TodoManager.
const todoManagerKey contextKey = "todo_manager"

// ContextWithTodoManager returns a new context with the TodoManager attached.
func ContextWithTodoManager(ctx context.Context, tm *TodoManager) context.Context {
	return context.WithValue(ctx, todoManagerKey, tm)
}

// TodoManagerFromContext retrieves the TodoManager from the context.
// Returns nil if not found.
func TodoManagerFromContext(ctx context.Context) *TodoManager {
	if tm, ok := ctx.Value(todoManagerKey).(*TodoManager); ok {
		return tm
	}
	return nil
}

// SetOwnerInContext is a convenience function that sets the context owner
// on the TodoManager stored in the context. Does nothing if no TodoManager is found.
func SetOwnerInContext(ctx context.Context, ownerID string) {
	if tm := TodoManagerFromContext(ctx); tm != nil {
		tm.SetContextOwner(ownerID)
	}
}

// ClearOwnerInContext is a convenience function that clears the context owner
// and all tasks for that owner on the TodoManager stored in the context.
func ClearOwnerInContext(ctx context.Context, ownerID string) error {
	if tm := TodoManagerFromContext(ctx); tm != nil {
		tm.ClearContextOwner()
		return tm.ClearOwner(ownerID)
	}
	return nil
}
