package journalstore

// This file exercises Store's append/restart/crash-safety behavior.
// var _ journal.Writer = (*Store)(nil) is asserted in store.go, so *Store
// compiling at all here already proves that assertion holds; the
// AllowedTaskFields plumbing below additionally proves every append path
// is reachable through the journal.Writer interface, not just the
// concrete type.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/journal"
)

func mustOpen(t *testing.T, dir, daemonID string) *Store {
	t.Helper()
	s, err := Open(dir, daemonID)
	if err != nil {
		t.Fatalf("Open(%q, %q): %v", dir, daemonID, err)
	}
	return s
}

// appendTasks appends n distinct task.created records (each followed by
// one field_changed) through the journal.Writer interface, to prove the
// interface-typed call path (not just the concrete *Store methods) is
// exercised end to end. prefix must be unique per call site so IDs never
// collide across multiple appendTasks calls against the same store.
func appendTasks(t *testing.T, w journal.Writer, prefix string, n int) []string {
	t.Helper()
	ids := make([]string, 0, n)
	for i := 0; i < n; i++ {
		id := fmt.Sprintf("%s-%03d", prefix, i)
		ids = append(ids, id)
		corr := journal.Correlation{TaskID: id, WorkspaceID: "ws-1"}
		if err := w.AppendTaskCreated(context.Background(), corr, journal.TaskCreatedPayload{
			TaskID: id,
			Fields: map[string]string{"subject": fmt.Sprintf("Task %d", i), "status": "pending"},
		}); err != nil {
			t.Fatalf("AppendTaskCreated(%s): %v", id, err)
		}
		if err := w.AppendTaskFieldChanged(context.Background(), corr, journal.TaskFieldChangedPayload{
			TaskID:    id,
			FieldName: "status",
			NewValue:  "in_progress",
		}); err != nil {
			t.Fatalf("AppendTaskFieldChanged(%s): %v", id, err)
		}
	}
	return ids
}

// TestAppendAcrossRestartsReplaysIdenticalState appends records across
// several simulated process restarts (Close then re-Open the *same*
// on-disk directory) and asserts Replay reconstructs identical state
// after every restart, and that the final reconstructed state matches
// what a single continuous session would have produced.
func TestAppendAcrossRestartsReplaysIdenticalState(t *testing.T) {
	dir := t.TempDir()

	var allIDs []string
	const daemonA = "daemon-instance-a"
	const daemonB = "daemon-instance-b"
	const daemonC = "daemon-instance-c"

	// "Process 1": open, append, close (simulating a clean daemon exit).
	s1 := mustOpen(t, dir, daemonA)
	allIDs = append(allIDs, appendTasks(t, s1, "p1-task", 3)...)
	if err := s1.Close(); err != nil {
		t.Fatalf("Close (process 1): %v", err)
	}

	// "Process 2": re-open the same directory with a fresh daemon instance
	// ID (as ADR-006 requires across a restart), append more, close.
	s2 := mustOpen(t, dir, daemonB)
	r2, err := s2.Replay()
	if err != nil {
		t.Fatalf("Replay after restart 1: %v", err)
	}
	if len(r2.Tasks) != len(allIDs) {
		t.Fatalf("after restart 1: got %d tasks, want %d", len(r2.Tasks), len(allIDs))
	}
	allIDs = append(allIDs, appendTasks(t, s2, "p2-task", 2)...)
	if err := s2.Close(); err != nil {
		t.Fatalf("Close (process 2): %v", err)
	}

	// "Process 3": re-open again, verify full reconstructed state.
	s3 := mustOpen(t, dir, daemonC)
	defer s3.Close()
	r3, err := s3.Replay()
	if err != nil {
		t.Fatalf("Replay after restart 2: %v", err)
	}
	if len(r3.Tasks) != len(allIDs) {
		t.Fatalf("after restart 2: got %d tasks, want %d", len(r3.Tasks), len(allIDs))
	}
	sort.Strings(allIDs)
	gotIDs := make([]string, 0, len(r3.Tasks))
	for id := range r3.Tasks {
		gotIDs = append(gotIDs, id)
	}
	sort.Strings(gotIDs)
	for i := range allIDs {
		if allIDs[i] != gotIDs[i] {
			t.Fatalf("task id mismatch at %d: got %q want %q", i, gotIDs[i], allIDs[i])
		}
	}
	for _, id := range allIDs {
		task := r3.Tasks[id]
		if task.Fields["status"] != "in_progress" {
			t.Errorf("task %s: status = %q, want %q", id, task.Fields["status"], "in_progress")
		}
		if task.Fields["subject"] == "" {
			t.Errorf("task %s: subject field missing after replay", id)
		}
	}

	// Also verify a fresh independent Store instance opened on the same
	// directory (without ever calling appendTasks against it) resolves the
	// identical tail record ID as s3, proving Open's anchor resolution is
	// itself restart-stable.
	s4 := mustOpen(t, dir, "daemon-instance-d")
	defer s4.Close()
	if (s3.tailRecordID == nil) != (s4.tailRecordID == nil) {
		t.Fatalf("tail presence mismatch between independently opened stores")
	}
	if s3.tailRecordID != nil && *s3.tailRecordID != *s4.tailRecordID {
		t.Fatalf("tail record id mismatch: %s vs %s", *s3.tailRecordID, *s4.tailRecordID)
	}
}

// TestCrashBetweenStageAndRenameLeavesPriorConsistentState is a genuine
// fault-injection test: it builds the on-disk state directly to simulate a
// crash that occurred after a record was staged (written+fsynced in
// staging/) but before it was renamed into records/ and before the head
// anchor was updated to reference it. It asserts that Open/Replay on that
// directory afterward see only the prior consistent (pre-crash) state --
// never the half-committed staged record, and never an error caused by
// the orphaned staging leftover.
func TestCrashBetweenStageAndRenameLeavesPriorConsistentState(t *testing.T) {
	dir := t.TempDir()

	s := mustOpen(t, dir, "daemon-crash-sim")
	priorIDs := appendTasks(t, s, "prior-task", 2)
	preCrash, err := s.Replay()
	if err != nil {
		t.Fatalf("Replay before simulated crash: %v", err)
	}
	if len(preCrash.Tasks) != 2 {
		t.Fatalf("setup: expected 2 tasks before crash simulation, got %d", len(preCrash.Tasks))
	}
	preCrashTail := *s.tailRecordID
	preCrashCount := s.recordCount
	if err := s.Close(); err != nil {
		t.Fatalf("Close before crash simulation: %v", err)
	}

	// Simulate "the process crashed between writing+fsyncing the staged
	// record and renaming it into records/": construct that exact on-disk
	// artifact directly (not via a lucky race), by staging a well-formed,
	// would-be-next record's bytes into staging/ and leaving it there,
	// without ever renaming it into records/ or touching the anchor.
	stagingDir := filepath.Join(dir, "staging")
	corr := journal.Correlation{TaskID: "task-orphan"}
	orphanRec := journal.Record{
		SchemaVersion:       journalSchemaVersion,
		RecordID:            "rec_orphan_never_committed",
		DaemonInstanceID:    "daemon-crash-sim",
		TaskID:              nonEmptyPtr(corr.TaskID),
		PredecessorRecordID: &preCrashTail,
		EventType:           journal.EventTaskCreated,
		Payload:             json.RawMessage(`{"task_id":"task-orphan","fields":{}}`),
	}
	digest, err := canonicalDigest(orphanRec)
	if err != nil {
		t.Fatalf("canonicalDigest: %v", err)
	}
	orphanRec.Integrity = journal.Integrity{CanonicalizationID: canonicalizationID, DigestAlgorithm: digestAlgorithm, Digest: digest}
	data, err := json.Marshal(orphanRec)
	if err != nil {
		t.Fatalf("marshal orphan record: %v", err)
	}
	stagedPath := filepath.Join(stagingDir, "rec-simulated-crash.tmp")
	if err := os.WriteFile(stagedPath, data, 0o600); err != nil {
		t.Fatalf("write simulated staged file: %v", err)
	}
	// Deliberately do NOT rename it into records/, and do NOT touch the
	// anchor: this is exactly the observable disk state after a crash
	// that occurred after the staged-file fsync but before the
	// records-directory rename step.

	// "Restart": Open a fresh Store on the same directory.
	s2 := mustOpen(t, dir, "daemon-crash-sim-restart")
	defer s2.Close()

	if s2.tailRecordID == nil || *s2.tailRecordID != preCrashTail {
		t.Fatalf("Open after crash: tail = %v, want %q (the last durably anchored record)", s2.tailRecordID, preCrashTail)
	}
	if s2.recordCount != preCrashCount {
		t.Fatalf("Open after crash: recordCount = %d, want %d", s2.recordCount, preCrashCount)
	}

	post, err := s2.Replay()
	if err != nil {
		t.Fatalf("Replay after crash simulation: %v (must see prior consistent state, not an error)", err)
	}
	if len(post.Tasks) != len(priorIDs) {
		t.Fatalf("Replay after crash: got %d tasks, want %d (the orphaned staged record must not be visible)", len(post.Tasks), len(priorIDs))
	}
	if _, ok := post.Tasks["task-orphan"]; ok {
		t.Fatalf("Replay after crash: orphaned staged-but-never-renamed record became visible")
	}

	// A subsequent append must succeed and chain from the pre-crash tail,
	// proving the store is not stuck/corrupted by the orphaned staging
	// leftover.
	if err := s2.AppendTaskCreated(context.Background(), journal.Correlation{TaskID: "task-after-crash"}, journal.TaskCreatedPayload{
		TaskID: "task-after-crash",
		Fields: map[string]string{"subject": "post-crash task"},
	}); err != nil {
		t.Fatalf("AppendTaskCreated after crash simulation: %v", err)
	}
	final, err := s2.Replay()
	if err != nil {
		t.Fatalf("Replay after post-crash append: %v", err)
	}
	if len(final.Tasks) != len(priorIDs)+1 {
		t.Fatalf("Replay after post-crash append: got %d tasks, want %d", len(final.Tasks), len(priorIDs)+1)
	}
}

// TestAppendUnknownFieldRejected proves the closed AllowedTaskFields
// allowlist is enforced (ADR-006: "Unknown fields are denied by default").
func TestAppendUnknownFieldRejected(t *testing.T) {
	dir := t.TempDir()
	s := mustOpen(t, dir, "daemon-fields")
	defer s.Close()

	err := s.AppendTaskCreated(context.Background(), journal.Correlation{TaskID: "t1"}, journal.TaskCreatedPayload{
		TaskID: "t1",
		Fields: map[string]string{"not_a_real_field": "x"},
	})
	if err == nil {
		t.Fatal("AppendTaskCreated with unknown field: expected error, got nil")
	}
	if !errors.Is(err, ErrUnknownField) {
		t.Fatalf("AppendTaskCreated with unknown field: error = %v, want wrapping ErrUnknownField", err)
	}

	// The store must be untouched by the rejected append.
	r, err := s.Replay()
	if err != nil {
		t.Fatalf("Replay: %v", err)
	}
	if len(r.Tasks) != 0 {
		t.Fatalf("Replay after rejected append: got %d tasks, want 0", len(r.Tasks))
	}
}

// TestAppendAfterCloseFails proves append calls are refused once Close has
// been called.
func TestAppendAfterCloseFails(t *testing.T) {
	dir := t.TempDir()
	s := mustOpen(t, dir, "daemon-close")
	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	err := s.AppendTaskCreated(context.Background(), journal.Correlation{TaskID: "t1"}, journal.TaskCreatedPayload{
		TaskID: "t1",
		Fields: map[string]string{"subject": "x"},
	})
	if !errors.Is(err, ErrStoreClosed) {
		t.Fatalf("AppendTaskCreated after Close: error = %v, want ErrStoreClosed", err)
	}
}
