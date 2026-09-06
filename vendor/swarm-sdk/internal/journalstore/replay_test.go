package journalstore

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/journal"
)

// TestReplayCreationOnly proves replaying task.created alone produces the
// canonical sanitized initial state.
func TestReplayCreationOnly(t *testing.T) {
	dir := t.TempDir()
	s := mustOpen(t, dir, "daemon-1")
	defer s.Close()

	if err := s.AppendTaskCreated(context.Background(), journal.Correlation{TaskID: "t1"}, journal.TaskCreatedPayload{
		TaskID: "t1",
		Fields: map[string]string{"subject": "Do the thing", "status": "pending", "priority": "high"},
	}); err != nil {
		t.Fatalf("AppendTaskCreated: %v", err)
	}

	r, err := s.Replay()
	if err != nil {
		t.Fatalf("Replay: %v", err)
	}
	task, ok := r.Tasks["t1"]
	if !ok {
		t.Fatal("Replay: task t1 not found")
	}
	want := map[string]string{"subject": "Do the thing", "status": "pending", "priority": "high"}
	for k, v := range want {
		if task.Fields[k] != v {
			t.Errorf("task.Fields[%q] = %q, want %q", k, task.Fields[k], v)
		}
	}
	if len(r.Skipped) != 0 {
		t.Errorf("Skipped = %v, want none", r.Skipped)
	}
}

// TestReplayMultipleFieldChanges proves a sequence of field_changed
// records reconstructs the expected accumulated state, and that only the
// named field is ever touched by each record.
func TestReplayMultipleFieldChanges(t *testing.T) {
	dir := t.TempDir()
	s := mustOpen(t, dir, "daemon-1")
	defer s.Close()

	corr := journal.Correlation{TaskID: "t1"}
	if err := s.AppendTaskCreated(context.Background(), corr, journal.TaskCreatedPayload{
		TaskID: "t1",
		Fields: map[string]string{"subject": "Initial", "status": "pending"},
	}); err != nil {
		t.Fatalf("AppendTaskCreated: %v", err)
	}
	changes := []journal.TaskFieldChangedPayload{
		{TaskID: "t1", FieldName: "status", NewValue: "in_progress"},
		{TaskID: "t1", FieldName: "priority", NewValue: "critical"},
		{TaskID: "t1", FieldName: "status", NewValue: "completed"},
	}
	for _, c := range changes {
		if err := s.AppendTaskFieldChanged(context.Background(), corr, c); err != nil {
			t.Fatalf("AppendTaskFieldChanged(%s): %v", c.FieldName, err)
		}
	}

	r, err := s.Replay()
	if err != nil {
		t.Fatalf("Replay: %v", err)
	}
	task := r.Tasks["t1"]
	if task.Fields["status"] != "completed" {
		t.Errorf("status = %q, want completed", task.Fields["status"])
	}
	if task.Fields["priority"] != "critical" {
		t.Errorf("priority = %q, want critical", task.Fields["priority"])
	}
	if task.Fields["subject"] != "Initial" {
		t.Errorf("subject = %q, want Initial (must be untouched by field_changed on other fields)", task.Fields["subject"])
	}
}

// TestReplayDeletionTombstonesAndRejectsLaterChanges proves task.deleted
// removes attach-visible state and later field_changed records against
// the same task_id are reported as skipped rather than silently reviving
// or corrupting the tombstoned task.
func TestReplayDeletionTombstonesAndRejectsLaterChanges(t *testing.T) {
	dir := t.TempDir()
	s := mustOpen(t, dir, "daemon-1")
	defer s.Close()

	corr := journal.Correlation{TaskID: "t1"}
	if err := s.AppendTaskCreated(context.Background(), corr, journal.TaskCreatedPayload{
		TaskID: "t1",
		Fields: map[string]string{"subject": "Ephemeral"},
	}); err != nil {
		t.Fatalf("AppendTaskCreated: %v", err)
	}
	if err := s.AppendTaskDeleted(context.Background(), corr, journal.TaskDeletedPayload{
		TaskID: "t1",
		Reason: "user_deleted",
	}); err != nil {
		t.Fatalf("AppendTaskDeleted: %v", err)
	}
	// A later field_changed against the now-tombstoned task_id: this must
	// not error the whole replay (ADR-006/CONTRACT permit skip-with-report
	// for this specific semantic violation) but must never revive the
	// task in Tasks.
	if err := s.AppendTaskFieldChanged(context.Background(), corr, journal.TaskFieldChangedPayload{
		TaskID:    "t1",
		FieldName: "status",
		NewValue:  "in_progress",
	}); err != nil {
		t.Fatalf("AppendTaskFieldChanged: %v", err)
	}

	r, err := s.Replay()
	if err != nil {
		t.Fatalf("Replay: %v", err)
	}
	if _, ok := r.Tasks["t1"]; ok {
		t.Fatal("Replay: tombstoned task t1 is still present in Tasks")
	}
	if len(r.Skipped) != 1 {
		t.Fatalf("Skipped = %v, want exactly 1 entry (the post-tombstone field_changed)", r.Skipped)
	}
	if r.Skipped[0].TaskID != "t1" {
		t.Errorf("Skipped[0].TaskID = %q, want t1", r.Skipped[0].TaskID)
	}
	if !strings.Contains(r.Skipped[0].Reason, "tombstoned") {
		t.Errorf("Skipped[0].Reason = %q, want it to mention tombstoned", r.Skipped[0].Reason)
	}
}

// TestReplayFullLifecycle exercises create -> multi-field update ->
// completion -> deletion in one task and checks the expected end state.
func TestReplayFullLifecycle(t *testing.T) {
	dir := t.TempDir()
	s := mustOpen(t, dir, "daemon-1")
	defer s.Close()

	corr := journal.Correlation{TaskID: "life"}
	steps := []struct {
		apply func() error
	}{
		{func() error {
			return s.AppendTaskCreated(context.Background(), corr, journal.TaskCreatedPayload{
				TaskID: "life",
				Fields: map[string]string{"subject": "Lifecycle", "status": "pending"},
			})
		}},
		{func() error {
			return s.AppendTaskFieldChanged(context.Background(), corr, journal.TaskFieldChangedPayload{TaskID: "life", FieldName: "status", NewValue: "in_progress"})
		}},
		{func() error {
			return s.AppendTaskFieldChanged(context.Background(), corr, journal.TaskFieldChangedPayload{TaskID: "life", FieldName: "status", NewValue: "completed"})
		}},
		{func() error {
			return s.AppendTaskDeleted(context.Background(), corr, journal.TaskDeletedPayload{TaskID: "life", Reason: "superseded"})
		}},
	}
	for i, step := range steps {
		if err := step.apply(); err != nil {
			t.Fatalf("step %d: %v", i, err)
		}
	}

	r, err := s.Replay()
	if err != nil {
		t.Fatalf("Replay: %v", err)
	}
	if _, ok := r.Tasks["life"]; ok {
		t.Fatal("Replay: deleted task still present")
	}
	if len(r.Skipped) != 0 {
		t.Fatalf("Skipped = %v, want none", r.Skipped)
	}
}

// TestReplayCorruptedPredecessorChainFailsClosed deliberately constructs a
// broken predecessor chain (a record whose predecessor_record_id names a
// record_id that does not exist) directly on disk and asserts Replay
// returns an error rather than a best-effort partial result.
func TestReplayCorruptedPredecessorChainFailsClosed(t *testing.T) {
	dir := t.TempDir()
	s := mustOpen(t, dir, "daemon-1")
	defer s.Close()

	if err := s.AppendTaskCreated(context.Background(), journal.Correlation{TaskID: "t1"}, journal.TaskCreatedPayload{
		TaskID: "t1", Fields: map[string]string{"subject": "x"},
	}); err != nil {
		t.Fatalf("AppendTaskCreated: %v", err)
	}

	// Directly construct a second, well-formed-except-for-its-predecessor
	// record whose predecessor_record_id names a record that was never
	// written, bypassing Append entirely.
	bogusPred := "rec_this_id_was_never_written"
	rec := journal.Record{
		SchemaVersion:       journalSchemaVersion,
		RecordID:            "rec_gap_test",
		DaemonInstanceID:    "daemon-1",
		TaskID:              nonEmptyPtr("t1"),
		PredecessorRecordID: &bogusPred,
		EventType:           journal.EventTaskFieldChanged,
		Payload:             json.RawMessage(`{"task_id":"t1","field_name":"status","new_value":"in_progress"}`),
	}
	digest, err := canonicalDigest(rec)
	if err != nil {
		t.Fatalf("canonicalDigest: %v", err)
	}
	rec.Integrity = journal.Integrity{CanonicalizationID: canonicalizationID, DigestAlgorithm: digestAlgorithm, Digest: digest}
	writeRawRecordFile(t, dir, rec)

	if _, err := s.Replay(); err == nil {
		t.Fatal("Replay with a predecessor-chain gap: expected error, got nil (must fail closed, not best-effort continue)")
	}
}

// TestReplayTamperedRecordByteFailsClosed writes a valid record, then
// tampers with one byte of its stored payload on disk (without updating
// the integrity digest), and asserts Replay detects the digest mismatch
// and fails closed.
func TestReplayTamperedRecordByteFailsClosed(t *testing.T) {
	dir := t.TempDir()
	s := mustOpen(t, dir, "daemon-1")
	if err := s.AppendTaskCreated(context.Background(), journal.Correlation{TaskID: "t1"}, journal.TaskCreatedPayload{
		TaskID: "t1", Fields: map[string]string{"subject": "original"},
	}); err != nil {
		t.Fatalf("AppendTaskCreated: %v", err)
	}
	tail := *s.tailRecordID
	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	recPath := filepath.Join(dir, "records", tail+".json")
	data, err := os.ReadFile(recPath)
	if err != nil {
		t.Fatalf("read record file: %v", err)
	}
	tampered := strings.Replace(string(data), "original", "TAMPERED", 1)
	if tampered == string(data) {
		t.Fatal("setup: tamper substring not found in stored record bytes")
	}
	if err := os.WriteFile(recPath, []byte(tampered), 0o600); err != nil {
		t.Fatalf("write tampered record file: %v", err)
	}

	s2 := mustOpen(t, dir, "daemon-1-restart")
	defer s2.Close()
	if _, err := s2.Replay(); err == nil {
		t.Fatal("Replay after tampering with a record byte: expected digest-mismatch error, got nil")
	}
}

// writeRawRecordFile writes rec directly to <dir>/records/<id>.json,
// bypassing the normal staged commit path, for constructing deliberately
// corrupt on-disk fixtures.
func writeRawRecordFile(t *testing.T, dir string, rec journal.Record) {
	t.Helper()
	data, err := json.Marshal(rec)
	if err != nil {
		t.Fatalf("marshal fixture record: %v", err)
	}
	path := filepath.Join(dir, "records", rec.RecordID+".json")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("write fixture record %s: %v", path, err)
	}
}
