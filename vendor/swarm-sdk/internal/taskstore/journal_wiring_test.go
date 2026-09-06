package taskstore

import (
	"context"
	"sync"
	"testing"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/journal"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/tools/ii"
)

// fakeJournalWriter is an in-memory journal.Writer test double that records
// every AppendTask* call it receives, in call order, for assertion. It is
// safe for concurrent use (SyncTodos already holds store.mu around the
// emit path, but the mutex here keeps this double correct independent of
// that).
type fakeJournalWriter struct {
	mu sync.Mutex

	created      []journal.TaskCreatedPayload
	fieldChanged []journal.TaskFieldChangedPayload
	deleted      []journal.TaskDeletedPayload
}

func (f *fakeJournalWriter) AppendTaskCreated(ctx context.Context, corr journal.Correlation, payload journal.TaskCreatedPayload) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.created = append(f.created, payload)
	return nil
}

func (f *fakeJournalWriter) AppendTaskFieldChanged(ctx context.Context, corr journal.Correlation, payload journal.TaskFieldChangedPayload) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.fieldChanged = append(f.fieldChanged, payload)
	return nil
}

func (f *fakeJournalWriter) AppendTaskDeleted(ctx context.Context, corr journal.Correlation, payload journal.TaskDeletedPayload) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.deleted = append(f.deleted, payload)
	return nil
}

var _ journal.Writer = (*fakeJournalWriter)(nil)

// TestSyncTodos_JournalWiring_TaskCreated exercises the AppendTaskCreated
// path: creating one task via SyncTodos must produce exactly one
// AppendTaskCreated call whose Fields carries only allowlisted field
// names, with no leakage of the deliberately-unlisted Metadata,
// TypedNotes, or AuditEvents fields.
func TestSyncTodos_JournalWiring_TaskCreated(t *testing.T) {
	store := New(Config{ConversationID: "conv", MetadataDir: t.TempDir()})
	fake := &fakeJournalWriter{}
	store.SetJournalWriter(fake)

	item := ii.TodoItem{
		ID: "task-1", Content: "Ship feature", Description: "detailed spec",
		Status: ii.TodoStatusPending, Priority: ii.TodoPriorityHigh,
		Metadata:   map[string]any{"secret_internal": "should-never-journal"},
		TypedNotes: []ii.TodoNote{{Type: "decision", Content: "internal-only note"}},
	}
	if err := SyncTodos(store, []ii.TodoItem{item}); err != nil {
		t.Fatalf("SyncTodos: %v", err)
	}

	if len(fake.created) != 1 {
		t.Fatalf("expected exactly 1 AppendTaskCreated call, got %d", len(fake.created))
	}
	if len(fake.fieldChanged) != 0 || len(fake.deleted) != 0 {
		t.Fatalf("expected no field_changed/deleted calls on create, got %d/%d", len(fake.fieldChanged), len(fake.deleted))
	}

	payload := fake.created[0]
	if payload.TaskID != "task-1" {
		t.Fatalf("TaskCreatedPayload.TaskID = %q, want %q", payload.TaskID, "task-1")
	}
	for name := range journal.AllowedTaskFields {
		if _, ok := payload.Fields[name]; !ok {
			t.Fatalf("TaskCreatedPayload.Fields missing allowlisted field %q", name)
		}
	}
	if len(payload.Fields) != len(journal.AllowedTaskFields) {
		t.Fatalf("TaskCreatedPayload.Fields has %d entries, want exactly %d (the allowlist size)", len(payload.Fields), len(journal.AllowedTaskFields))
	}
	for _, leaked := range []string{"metadata", "typed_notes", "audit_events", "notes"} {
		if _, ok := payload.Fields[leaked]; ok {
			t.Fatalf("TaskCreatedPayload.Fields leaked non-allowlisted field %q", leaked)
		}
	}
	if payload.Fields["subject"] != "Ship feature" {
		t.Fatalf("subject field = %q, want %q", payload.Fields["subject"], "Ship feature")
	}
}

// TestSyncTodos_JournalWiring_FieldChanged exercises the
// AppendTaskFieldChanged path: changing two allowlisted fields on an
// existing task in one SyncTodos call must produce exactly two
// AppendTaskFieldChanged calls, ordered alphabetically by field name
// (ADR-006's explicit "stable field-name order" requirement), each with a
// PreviousValueDigest of the pre-image's redacted value.
func TestSyncTodos_JournalWiring_FieldChanged(t *testing.T) {
	store := New(Config{ConversationID: "conv", MetadataDir: t.TempDir()})
	fake := &fakeJournalWriter{}
	store.SetJournalWriter(fake)

	original := ii.TodoItem{
		ID: "task-1", Content: "Original subject", Description: "original description",
		Status: ii.TodoStatusPending, Priority: ii.TodoPriorityMedium,
	}
	if err := SyncTodos(store, []ii.TodoItem{original}); err != nil {
		t.Fatalf("SyncTodos (create): %v", err)
	}
	fake.created = nil // isolate the update phase

	updated := original
	updated.Content = "Updated subject"     // -> "subject" field
	updated.Status = ii.TodoStatusCompleted // -> "status" field

	if err := SyncTodos(store, []ii.TodoItem{updated}); err != nil {
		t.Fatalf("SyncTodos (update): %v", err)
	}

	if len(fake.created) != 0 || len(fake.deleted) != 0 {
		t.Fatalf("expected no create/delete calls on field update, got %d/%d", len(fake.created), len(fake.deleted))
	}
	if len(fake.fieldChanged) != 2 {
		t.Fatalf("expected exactly 2 AppendTaskFieldChanged calls, got %d: %+v", len(fake.fieldChanged), fake.fieldChanged)
	}

	// "status" < "subject" alphabetically.
	if fake.fieldChanged[0].FieldName != "status" {
		t.Fatalf("fieldChanged[0].FieldName = %q, want %q (alphabetical order)", fake.fieldChanged[0].FieldName, "status")
	}
	if fake.fieldChanged[1].FieldName != "subject" {
		t.Fatalf("fieldChanged[1].FieldName = %q, want %q (alphabetical order)", fake.fieldChanged[1].FieldName, "subject")
	}
	if fake.fieldChanged[1].NewValue != "Updated subject" {
		t.Fatalf("fieldChanged[1].NewValue = %q, want %q", fake.fieldChanged[1].NewValue, "Updated subject")
	}
	for i, fc := range fake.fieldChanged {
		if fc.PreviousValueDigest == nil || *fc.PreviousValueDigest == "" {
			t.Fatalf("fieldChanged[%d].PreviousValueDigest is nil/empty, want the pre-image's redacted-value digest", i)
		}
	}
}

// TestSyncTodos_JournalWiring_TaskDeleted exercises the AppendTaskDeleted
// path: omitting a previously-synced task's ID from a SyncTodos call
// prunes it to StatusDeleted and must produce exactly one
// AppendTaskDeleted call with Reason "sync_removed".
func TestSyncTodos_JournalWiring_TaskDeleted(t *testing.T) {
	store := New(Config{ConversationID: "conv", MetadataDir: t.TempDir()})
	fake := &fakeJournalWriter{}
	store.SetJournalWriter(fake)

	item := ii.TodoItem{ID: "task-1", Content: "Ephemeral", Status: ii.TodoStatusPending, Priority: ii.TodoPriorityLow}
	if err := SyncTodos(store, []ii.TodoItem{item}); err != nil {
		t.Fatalf("SyncTodos (create): %v", err)
	}
	fake.created = nil

	if err := SyncTodos(store, []ii.TodoItem{}); err != nil {
		t.Fatalf("SyncTodos (omit id): %v", err)
	}

	if len(fake.created) != 0 || len(fake.fieldChanged) != 0 {
		t.Fatalf("expected no create/field_changed calls on deletion, got %d/%d", len(fake.created), len(fake.fieldChanged))
	}
	if len(fake.deleted) != 1 {
		t.Fatalf("expected exactly 1 AppendTaskDeleted call, got %d", len(fake.deleted))
	}
	if fake.deleted[0].TaskID != "task-1" {
		t.Fatalf("deleted[0].TaskID = %q, want %q", fake.deleted[0].TaskID, "task-1")
	}
	if fake.deleted[0].Reason != "sync_removed" {
		t.Fatalf("deleted[0].Reason = %q, want %q", fake.deleted[0].Reason, "sync_removed")
	}

	got, err := store.GetTask("task-1")
	if err != nil {
		t.Fatalf("GetTask after prune: %v", err)
	}
	if got.Status != StatusDeleted {
		t.Fatalf("task status after prune = %q, want %q", got.Status, StatusDeleted)
	}

	// A second SyncTodos call that again omits the (already-deleted) task
	// ID must not re-emit a deletion record: the transition already
	// happened, and re-emitting would misrepresent the journal's history.
	if err := SyncTodos(store, []ii.TodoItem{}); err != nil {
		t.Fatalf("SyncTodos (second omit): %v", err)
	}
	if len(fake.deleted) != 1 {
		t.Fatalf("expected still exactly 1 AppendTaskDeleted call after re-sync of an already-deleted task, got %d", len(fake.deleted))
	}
}

// TestSyncTodos_NilJournalWriter_NeverPanicsAndSaveUnchanged is the
// regression test for the "must not become newly fragile" invariant: a
// Store with no journal writer attached (the pre-wiring default, and the
// common case for any taskstore that predates this feature) must behave
// exactly as before -- SyncTodos succeeds, Save's on-disk behavior is
// unaffected, and nothing panics.
func TestSyncTodos_NilJournalWriter_NeverPanicsAndSaveUnchanged(t *testing.T) {
	store := New(Config{ConversationID: "conv", MetadataDir: t.TempDir()})
	// Deliberately do NOT call store.SetJournalWriter: journalWriter stays
	// nil, matching every taskstore that predates this wiring.

	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("SyncTodos with a nil journal writer must never panic, got: %v", r)
		}
	}()

	item := ii.TodoItem{ID: "task-1", Content: "No journal", Status: ii.TodoStatusPending, Priority: ii.TodoPriorityLow}
	if err := SyncTodos(store, []ii.TodoItem{item}); err != nil {
		t.Fatalf("SyncTodos: %v", err)
	}
	got, err := store.GetTask("task-1")
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if got.Subject != "No journal" {
		t.Fatalf("task not persisted correctly: %+v", got)
	}

	// Update and delete paths must also be no-ops w.r.t. journaling, and
	// must not panic, with a nil writer.
	updated := item
	updated.Content = "Still no journal"
	if err := SyncTodos(store, []ii.TodoItem{updated}); err != nil {
		t.Fatalf("SyncTodos (update): %v", err)
	}
	if err := SyncTodos(store, []ii.TodoItem{}); err != nil {
		t.Fatalf("SyncTodos (delete): %v", err)
	}
	got, err = store.GetTask("task-1")
	if err != nil {
		t.Fatalf("GetTask after prune: %v", err)
	}
	if got.Status != StatusDeleted {
		t.Fatalf("task status after prune = %q, want %q", got.Status, StatusDeleted)
	}
}
