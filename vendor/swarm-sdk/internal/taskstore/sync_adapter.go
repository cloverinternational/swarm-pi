// Package taskstore provides persistent task storage for conversations.
package taskstore

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/journal"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/journalredact"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/tools/ii"
)

// TodoSyncer adapts a TaskStore to implement the ii.Syncer interface.
type TodoSyncer struct{ store *Store }

func NewTodoSyncer(store *Store) *TodoSyncer         { return &TodoSyncer{store: store} }
func (s *TodoSyncer) Sync(items []ii.TodoItem) error { return SyncTodos(s.store, items) }

// RestoreTodos restores active (non-deleted) persisted tasks into the manager.
func RestoreTodos(store *Store, manager *ii.TodoManager) error {
	tasks := store.GetActiveTasks()
	if len(tasks) == 0 {
		return nil
	}
	items := make([]ii.TodoItem, 0, len(tasks))
	for _, task := range tasks {
		items = append(items, convertFromTask(task))
	}
	return manager.SetTodos(items)
}

func convertFromTask(task Task) ii.TodoItem {
	item := ii.TodoItem{
		ID: task.ID, Content: task.Subject, Description: task.Description,
		Status: fromStoreStatus(task.Status), Priority: fromStorePriority(task.Priority),
		Category: ii.TaskCategory(task.Category), ActiveForm: task.ActiveForm,
		Metadata: cloneMap(task.Metadata), Active: task.Active,
		DependsOn: cloneStrings(task.DependsOn), Blocks: cloneStrings(task.Blocks),
		Notes: cloneStrings(task.Notes), OwnerID: task.OwnerID, Sequence: task.Sequence,
		ParentID: task.ParentID, CreatedAt: task.CreatedAt, UpdatedAt: task.UpdatedAt,
		CompletedAt: cloneTime(task.CompletedAt), SourceTurn: task.SourceTurn,
		LastSeen: task.LastSeen, SiblingIndex: task.SiblingIndex,
		OriginPromptID: task.OriginPromptID, PlanID: task.PlanID,
		DecomposedBy: task.DecomposedBy, DecompositionAttempts: task.DecompositionAttempts,
		LastDecompositionError: task.LastDecompositionError,
	}
	for _, note := range task.TypedNotes {
		item.TypedNotes = append(item.TypedNotes, ii.TodoNote{Type: note.Type, Content: note.Content, CreatedAt: note.CreatedAt, Metadata: cloneMap(note.Metadata)})
	}
	for _, event := range task.AuditEvents {
		redacted, _ := redactTaskAuditEvents([]TaskAuditEvent{event})
		safe := redacted[0]
		item.AuditEvents = append(item.AuditEvents, ii.TodoAuditEvent{Type: safe.Type, Timestamp: safe.Timestamp, Actor: safe.Actor, Summary: safe.Summary, Metadata: cloneMap(safe.Metadata)})
	}
	return item
}

// SyncTodos synchronizes the complete runtime task list to disk.
func SyncTodos(store *Store, items []ii.TodoItem) error {
	store.mu.Lock()
	defer store.mu.Unlock()

	now := time.Now().UTC()
	preImageByID := make(map[string]Task, len(store.Tasks))
	for _, t := range store.Tasks {
		preImageByID[t.ID] = t
	}
	candidate := &Store{
		Version:        store.Version,
		ConversationID: store.ConversationID,
		ParentID:       store.ParentID,
		Tasks:          append([]Task(nil), store.Tasks...),
		LastUpdated:    store.LastUpdated,
		Checksum:       store.Checksum,
		filePath:       store.filePath,
	}
	indexByID := make(map[string]int, len(candidate.Tasks))
	for i := range candidate.Tasks {
		indexByID[candidate.Tasks[i].ID] = i
	}
	currentIDs := make(map[string]bool, len(items))
	for _, item := range items {
		if currentIDs[item.ID] {
			return fmt.Errorf("task with ID %s already exists", item.ID)
		}
		currentIDs[item.ID] = true
		task := convertToTask(item, now)
		if index, ok := indexByID[item.ID]; ok {
			existing := candidate.Tasks[index]
			// Legacy runtime values may be zero; never destroy stable persisted values.
			if item.CreatedAt.IsZero() {
				task.CreatedAt = existing.CreatedAt
			}
			if item.UpdatedAt.IsZero() {
				task.UpdatedAt = existing.UpdatedAt
			}
			if task.SourceTurn == 0 {
				task.SourceTurn = existing.SourceTurn
			}
			if item.LastSeen.IsZero() {
				task.LastSeen = existing.LastSeen
			}
			candidate.Tasks[index] = task
			continue
		}
		task.LastSeen = now
		indexByID[task.ID] = len(candidate.Tasks)
		candidate.Tasks = append(candidate.Tasks, task)
	}
	for i := range candidate.Tasks {
		if !currentIDs[candidate.Tasks[i].ID] && candidate.Tasks[i].Status != StatusDeleted {
			candidate.Tasks[i].Status = StatusDeleted
			candidate.Tasks[i].UpdatedAt = now
			candidate.Tasks[i].CompletedAt = cloneTime(&now)
		}
	}
	if err := validateCandidateHierarchy(candidate.Tasks); err != nil {
		return err
	}
	if err := candidate.Save(); err != nil {
		return err
	}

	// Emit additive, best-effort execution-journal records for this
	// mutation now that the authoritative task.json write has succeeded.
	// Per ADR-006/CONTRACT.md's "Hard invariants", the journal is
	// additive-only this phase: a journal-emit failure is logged but MUST
	// NEVER cause SyncTodos to fail or roll back the already-persisted
	// candidate.Save() above.
	emitSyncTodosJournalEvents(store, preImageByID, candidate.Tasks)

	store.Version = candidate.Version
	store.ConversationID = candidate.ConversationID
	store.ParentID = candidate.ParentID
	store.Tasks = candidate.Tasks
	store.LastUpdated = candidate.LastUpdated
	store.Checksum = candidate.Checksum
	return nil
}

// allowedTaskFieldNamesSorted is journal.AllowedTaskFields' keys in stable
// alphabetical order, computed once at package init. ADR-006 requires that
// "multi-field store updates produce one record per changed field in a
// stable field-name order" -- alphabetical order over this closed allowlist
// satisfies that requirement deterministically.
var allowedTaskFieldNamesSorted = func() []string {
	names := make([]string, 0, len(journal.AllowedTaskFields))
	for name := range journal.AllowedTaskFields {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}()

// taskFieldRawValue returns the raw (unredacted) string form of one
// allowlisted Task field, by explicit field name. This is intentionally
// NOT a reflection/json-map walk: it is written out field-by-field so that
// journal.AllowedTaskFields stays the single source of truth for what CAN
// be journaled, and so that Task.Metadata/TypedNotes/AuditEvents --
// deliberately absent from the allowlist -- can never leak into a journal
// payload by accident (e.g. via a generic map-of-all-fields helper).
// Unknown field names return "" (they should never be passed here; callers
// only ever iterate allowedTaskFieldNamesSorted).
func taskFieldRawValue(t Task, fieldName string) string {
	switch fieldName {
	case "subject":
		return t.Subject
	case "description":
		return t.Description
	case "status":
		return string(t.Status)
	case "priority":
		return string(t.Priority)
	case "category":
		return t.Category
	case "active_form":
		return t.ActiveForm
	case "active":
		return strconv.FormatBool(t.Active)
	case "depends_on":
		return strings.Join(t.DependsOn, ",")
	case "blocks":
		return strings.Join(t.Blocks, ",")
	case "created_at":
		return formatTaskTime(t.CreatedAt)
	case "updated_at":
		return formatTaskTime(t.UpdatedAt)
	case "completed_at":
		return formatTaskTimePtr(t.CompletedAt)
	case "source_turn":
		return strconv.Itoa(t.SourceTurn)
	case "last_seen":
		return formatTaskTime(t.LastSeen)
	case "owner_id":
		return t.OwnerID
	case "sequence":
		return strconv.Itoa(t.Sequence)
	case "parent_id":
		return t.ParentID
	case "sibling_index":
		return strconv.Itoa(t.SiblingIndex)
	case "origin_prompt_id":
		return t.OriginPromptID
	case "plan_id":
		return t.PlanID
	case "decomposed_by":
		return t.DecomposedBy
	case "decomposition_attempts":
		return strconv.Itoa(t.DecompositionAttempts)
	case "last_decomposition_error":
		return t.LastDecompositionError
	default:
		return ""
	}
}

// formatTaskTime renders a time.Time as RFC3339Nano, or "" for the zero
// value, so an unset timestamp journals as an empty string rather than
// Go's "0001-01-01..." zero-time representation.
func formatTaskTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.Format(time.RFC3339Nano)
}

// formatTaskTimePtr renders *time.Time the same way as formatTaskTime,
// treating a nil pointer the same as an unset/zero timestamp.
func formatTaskTimePtr(t *time.Time) string {
	if t == nil {
		return ""
	}
	return formatTaskTime(*t)
}

// emitSyncTodosJournalEvents diffs preImageByID (the pre-mutation task set,
// keyed by ID) against postTasks (the just-persisted candidate.Tasks) and
// emits one journal record per ADR-006 rule via store's attached
// journal.Writer:
//   - a task ID with no pre-image entry: one AppendTaskCreated record with
//     every allowlisted field's redacted value;
//   - a task ID present in both, newly transitioned to StatusDeleted (the
//     "prune missing IDs" branch above): one AppendTaskDeleted record with
//     Reason "sync_removed" (no field_changed records also emitted for this
//     transition -- the deletion tombstone supersedes it);
//   - a task ID present in both, with one or more allowlisted fields
//     changed: one AppendTaskFieldChanged record PER changed field, in
//     allowedTaskFieldNamesSorted (alphabetical) order, each carrying the
//     redacted pre-image value's digest as PreviousValueDigest.
//
// journal writes are entirely best-effort at this phase's scope: a nil
// store.journalWriter makes this a no-op, and any AppendTask* error is
// logged and swallowed -- SyncTodos' authoritative candidate.Save() has
// already succeeded by the time this runs and must never be undone by a
// journal-side failure.
func emitSyncTodosJournalEvents(store *Store, preImageByID map[string]Task, postTasks []Task) {
	writer := store.journalWriterLocked()
	if writer == nil {
		return
	}
	ctx := context.Background()
	workspaceRoot := store.directory()

	for _, post := range postTasks {
		pre, existed := preImageByID[post.ID]
		corr := journal.Correlation{
			TaskID:         post.ID,
			ConversationID: store.ConversationID,
		}

		if !existed {
			fields := make(map[string]string, len(allowedTaskFieldNamesSorted))
			for _, name := range allowedTaskFieldNamesSorted {
				fields[name] = journalredact.RedactFieldValue(name, taskFieldRawValue(post, name), workspaceRoot)
			}
			payload := journal.TaskCreatedPayload{TaskID: post.ID, Fields: fields}
			if err := writer.AppendTaskCreated(ctx, corr, payload); err != nil {
				log.Printf("taskstore: journal AppendTaskCreated failed for task %s (task.json write already succeeded, continuing): %v", post.ID, err)
			}
			continue
		}

		if pre.Status != StatusDeleted && post.Status == StatusDeleted {
			payload := journal.TaskDeletedPayload{TaskID: post.ID, Reason: "sync_removed"}
			if err := writer.AppendTaskDeleted(ctx, corr, payload); err != nil {
				log.Printf("taskstore: journal AppendTaskDeleted failed for task %s (task.json write already succeeded, continuing): %v", post.ID, err)
			}
			continue
		}

		for _, name := range allowedTaskFieldNamesSorted {
			rawPre := taskFieldRawValue(pre, name)
			rawPost := taskFieldRawValue(post, name)
			if rawPre == rawPost {
				continue
			}
			redactedPre := journalredact.RedactFieldValue(name, rawPre, workspaceRoot)
			redactedPost := journalredact.RedactFieldValue(name, rawPost, workspaceRoot)
			if redactedPre == redactedPost {
				// Raw values differed but redaction collapses them to the
				// same sanitized form (e.g. two distinct secrets both
				// becoming "[REDACTED]"); nothing observable changed in
				// the journal, so no record is warranted.
				continue
			}
			digest := journalredact.DigestOf(redactedPre)
			payload := journal.TaskFieldChangedPayload{
				TaskID:              post.ID,
				FieldName:           name,
				NewValue:            redactedPost,
				PreviousValueDigest: &digest,
			}
			if err := writer.AppendTaskFieldChanged(ctx, corr, payload); err != nil {
				log.Printf("taskstore: journal AppendTaskFieldChanged(%s) failed for task %s (task.json write already succeeded, continuing): %v", name, post.ID, err)
			}
		}
	}
}

func validateCandidateHierarchy(tasks []Task) error {
	parentByID := make(map[string]string, len(tasks))
	for _, task := range tasks {
		if _, exists := parentByID[task.ID]; exists {
			return fmt.Errorf("task with ID %s already exists", task.ID)
		}
		parentByID[task.ID] = task.ParentID
	}
	for id, parentID := range parentByID {
		if parentID != "" {
			if _, exists := parentByID[parentID]; !exists {
				return fmt.Errorf("task: parent %q not found", parentID)
			}
		}
		seen := make(map[string]bool)
		for current := id; current != ""; current = parentByID[current] {
			if seen[current] {
				return ErrCycleDetected
			}
			seen[current] = true
		}
	}
	return nil
}

func convertToTask(item ii.TodoItem, now time.Time) Task {
	createdAt := item.CreatedAt
	if createdAt.IsZero() {
		createdAt = now
	}
	updatedAt := item.UpdatedAt
	if updatedAt.IsZero() {
		updatedAt = createdAt
	}
	task := Task{
		ID: item.ID, Subject: item.Content, Description: item.Description,
		Status: toStoreStatus(item.Status), Priority: toStorePriority(item.Priority),
		Category: string(item.Category), ActiveForm: item.ActiveForm,
		Metadata: cloneMap(item.Metadata), Active: item.Active,
		DependsOn: cloneStrings(item.DependsOn), Blocks: cloneStrings(item.Blocks),
		CreatedAt: createdAt, UpdatedAt: updatedAt, CompletedAt: cloneTime(item.CompletedAt),
		SourceTurn: item.SourceTurn, LastSeen: item.LastSeen, Notes: cloneStrings(item.Notes),
		OwnerID: item.OwnerID, Sequence: item.Sequence, ParentID: item.ParentID,
		SiblingIndex: item.SiblingIndex, OriginPromptID: item.OriginPromptID,
		PlanID: item.PlanID, DecomposedBy: item.DecomposedBy,
		DecompositionAttempts:  item.DecompositionAttempts,
		LastDecompositionError: item.LastDecompositionError,
	}
	for _, note := range item.TypedNotes {
		task.TypedNotes = append(task.TypedNotes, TaskNote{Type: note.Type, Content: note.Content, CreatedAt: note.CreatedAt, Metadata: cloneMap(note.Metadata)})
	}
	for _, event := range item.AuditEvents {
		task.AuditEvents = append(task.AuditEvents, TaskAuditEvent{Type: event.Type, Timestamp: event.Timestamp, Actor: event.Actor, Summary: event.Summary, Metadata: cloneMap(event.Metadata)})
	}
	task.AuditEvents, _ = redactTaskAuditEvents(task.AuditEvents)
	return task
}

func fromStoreStatus(status Status) ii.TodoStatus {
	switch status {
	case StatusInProgress:
		return ii.TodoStatusInProgress
	case StatusCompleted:
		return ii.TodoStatusCompleted
	default:
		return ii.TodoStatusPending
	}
}
func toStoreStatus(status ii.TodoStatus) Status {
	switch status {
	case ii.TodoStatusInProgress:
		return StatusInProgress
	case ii.TodoStatusCompleted:
		return StatusCompleted
	default:
		return StatusPending
	}
}
func fromStorePriority(priority Priority) ii.TodoPriority {
	switch priority {
	case PriorityLow:
		return ii.TodoPriorityLow
	case PriorityHigh, PriorityCritical:
		return ii.TodoPriorityHigh
	default:
		return ii.TodoPriorityMedium
	}
}
func toStorePriority(priority ii.TodoPriority) Priority {
	switch priority {
	case ii.TodoPriorityLow:
		return PriorityLow
	case ii.TodoPriorityHigh:
		return PriorityHigh
	default:
		return PriorityMedium
	}
}
func cloneStrings(values []string) []string { return append([]string(nil), values...) }
func cloneTime(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}
func cloneMap(value map[string]any) map[string]any {
	if value == nil {
		return nil
	}
	out := make(map[string]any, len(value))
	for key, item := range value {
		out[key] = cloneValue(item)
	}
	return out
}
func cloneValue(value any) any {
	if value == nil {
		return nil
	}
	cloned, ok := cloneReflectValue(reflect.ValueOf(value), make(map[cloneVisit]bool), 0)
	if !ok {
		// Cyclic and otherwise non-JSON-compatible metadata cannot be safely
		// detached. Drop that value rather than retaining a mutable alias.
		return nil
	}
	return cloned.Interface()
}

type cloneVisit struct {
	typ  reflect.Type
	ptr  uintptr
	kind reflect.Kind
}

func cloneReflectValue(value reflect.Value, visiting map[cloneVisit]bool, depth int) (reflect.Value, bool) {
	if depth >= 64 {
		return reflect.Value{}, false
	}
	switch value.Kind() {
	case reflect.Interface:
		if value.IsNil() {
			return reflect.Zero(value.Type()), true
		}
		cloned, ok := cloneReflectValue(value.Elem(), visiting, depth+1)
		if !ok {
			return reflect.Value{}, false
		}
		out := reflect.New(value.Type()).Elem()
		out.Set(cloned)
		return out, true
	case reflect.Pointer:
		if value.IsNil() {
			return reflect.Zero(value.Type()), true
		}
		visit := cloneVisit{typ: value.Type(), ptr: value.Pointer(), kind: value.Kind()}
		if visiting[visit] {
			return reflect.Value{}, false
		}
		visiting[visit] = true
		defer delete(visiting, visit)
		cloned, ok := cloneReflectValue(value.Elem(), visiting, depth+1)
		if !ok {
			return reflect.Value{}, false
		}
		out := reflect.New(value.Type().Elem())
		out.Elem().Set(cloned)
		return out, true
	case reflect.Map:
		if value.IsNil() {
			return reflect.Zero(value.Type()), true
		}
		visit := cloneVisit{typ: value.Type(), ptr: value.Pointer(), kind: value.Kind()}
		if visiting[visit] {
			return reflect.Value{}, false
		}
		visiting[visit] = true
		defer delete(visiting, visit)
		out := reflect.MakeMapWithSize(value.Type(), value.Len())
		iter := value.MapRange()
		for iter.Next() {
			cloned, ok := cloneReflectValue(iter.Value(), visiting, depth+1)
			if !ok {
				return reflect.Value{}, false
			}
			out.SetMapIndex(iter.Key(), cloned)
		}
		return out, true
	case reflect.Slice:
		if value.IsNil() {
			return reflect.Zero(value.Type()), true
		}
		visit := cloneVisit{typ: value.Type(), ptr: value.Pointer(), kind: value.Kind()}
		if visiting[visit] {
			return reflect.Value{}, false
		}
		visiting[visit] = true
		defer delete(visiting, visit)
		out := reflect.MakeSlice(value.Type(), value.Len(), value.Len())
		for i := 0; i < value.Len(); i++ {
			cloned, ok := cloneReflectValue(value.Index(i), visiting, depth+1)
			if !ok {
				return reflect.Value{}, false
			}
			out.Index(i).Set(cloned)
		}
		return out, true
	case reflect.Array:
		out := reflect.New(value.Type()).Elem()
		for i := 0; i < value.Len(); i++ {
			cloned, ok := cloneReflectValue(value.Index(i), visiting, depth+1)
			if !ok {
				return reflect.Value{}, false
			}
			out.Index(i).Set(cloned)
		}
		return out, true
	case reflect.Struct:
		if !value.CanInterface() {
			return reflect.Value{}, false
		}
		encoded, err := json.Marshal(value.Interface())
		if err != nil {
			return reflect.Value{}, false
		}
		out := reflect.New(value.Type())
		if err := json.Unmarshal(encoded, out.Interface()); err != nil {
			return reflect.Value{}, false
		}
		return out.Elem(), true
	default:
		return value, true
	}
}

func cloneTask(task Task) Task {
	cloned := task
	cloned.Metadata = cloneMap(task.Metadata)
	cloned.DependsOn = cloneStrings(task.DependsOn)
	cloned.Blocks = cloneStrings(task.Blocks)
	cloned.CompletedAt = cloneTime(task.CompletedAt)
	cloned.Notes = cloneStrings(task.Notes)
	cloned.TypedNotes = make([]TaskNote, len(task.TypedNotes))
	for i, note := range task.TypedNotes {
		cloned.TypedNotes[i] = note
		cloned.TypedNotes[i].Metadata = cloneMap(note.Metadata)
	}
	cloned.AuditEvents, _ = redactTaskAuditEvents(task.AuditEvents)
	return cloned
}

func cloneTasks(tasks []Task) []Task {
	if tasks == nil {
		return nil
	}
	cloned := make([]Task, len(tasks))
	for i, task := range tasks {
		cloned[i] = cloneTask(task)
	}
	return cloned
}
