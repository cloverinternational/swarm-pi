package journalstore

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/journal"
)

// TestCompactRemovesTerminalTaskAfter30Days proves a terminal task's
// records are removed once the 30-day boundary (from its terminal
// transition) has passed, using an injected "now" rather than sleeping.
func TestCompactRemovesTerminalTaskAfter30Days(t *testing.T) {
	dir := t.TempDir()
	s := mustOpen(t, dir, "daemon-1")
	defer s.Close()

	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	corr := journal.Correlation{TaskID: "terminal-1"}
	if err := s.AppendTaskCreated(context.Background(), corr, journal.TaskCreatedPayload{
		TaskID: "terminal-1",
		Fields: map[string]string{"subject": "will finish", "status": "pending"},
	}); err != nil {
		t.Fatalf("AppendTaskCreated: %v", err)
	}
	if err := s.AppendTaskFieldChanged(context.Background(), corr, journal.TaskFieldChangedPayload{
		TaskID: "terminal-1", FieldName: "status", NewValue: "completed",
	}); err != nil {
		t.Fatalf("AppendTaskFieldChanged: %v", err)
	}

	// Overwrite recorded_at on the two committed records to a fixed base
	// time, since Compact's eligibility is driven by recorded_at/terminal
	// transition time, not wall-clock append time, and this test must be
	// fully deterministic without sleeping.
	rewriteAllRecordedAt(t, s, base)

	// Just under 30 days after the (rewritten) terminal transition: must
	// still be retained.
	justUnder := base.Add(30*24*time.Hour - time.Second)
	removed, err := s.Compact(justUnder)
	if err != nil {
		t.Fatalf("Compact(justUnder): %v", err)
	}
	if removed != 0 {
		t.Fatalf("Compact(justUnder): removed = %d, want 0 (still within 30-day window)", removed)
	}
	r, err := s.Replay()
	if err != nil {
		t.Fatalf("Replay after Compact(justUnder): %v", err)
	}
	// A "completed" status is set via task.field_changed, not
	// task.deleted; per ADR-006 only task.deleted removes attach-visible
	// state. So within the retention window, terminal-1 must still be
	// present in Tasks with status "completed".
	task, ok := r.Tasks["terminal-1"]
	if !ok {
		t.Fatal("terminal-1 missing after Compact(justUnder): still within its 30-day retention window")
	}
	if task.Fields["status"] != "completed" {
		t.Errorf("terminal-1 status = %q, want completed", task.Fields["status"])
	}

	// Just over 30 days after the terminal transition: must be removed.
	justOver := base.Add(30*24*time.Hour + time.Second)
	removed, err = s.Compact(justOver)
	if err != nil {
		t.Fatalf("Compact(justOver): %v", err)
	}
	if removed != 2 {
		t.Fatalf("Compact(justOver): removed = %d, want 2 (both records for terminal-1)", removed)
	}

	final, err := s.Replay()
	if err != nil {
		t.Fatalf("Replay after Compact(justOver): %v", err)
	}
	if len(final.Tasks) != 0 {
		t.Fatalf("Replay after Compact(justOver): got %d tasks, want 0", len(final.Tasks))
	}
}

// TestCompactRetainsNonTerminalTaskRegardlessOfAge proves a non-terminal
// task's records survive Compact no matter how old recorded_at is.
func TestCompactRetainsNonTerminalTaskRegardlessOfAge(t *testing.T) {
	dir := t.TempDir()
	s := mustOpen(t, dir, "daemon-1")
	defer s.Close()

	base := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	corr := journal.Correlation{TaskID: "forever-1"}
	if err := s.AppendTaskCreated(context.Background(), corr, journal.TaskCreatedPayload{
		TaskID: "forever-1",
		Fields: map[string]string{"subject": "never finishes", "status": "in_progress"},
	}); err != nil {
		t.Fatalf("AppendTaskCreated: %v", err)
	}
	rewriteAllRecordedAt(t, s, base)

	// Far in the future -- would exceed any retention window many times
	// over if this task were terminal.
	now := base.Add(10 * 365 * 24 * time.Hour)
	removed, err := s.Compact(now)
	if err != nil {
		t.Fatalf("Compact: %v", err)
	}
	if removed != 0 {
		t.Fatalf("Compact: removed = %d, want 0 (non-terminal task must survive regardless of age)", removed)
	}

	r, err := s.Replay()
	if err != nil {
		t.Fatalf("Replay: %v", err)
	}
	if _, ok := r.Tasks["forever-1"]; !ok {
		t.Fatal("forever-1 missing after Compact: non-terminal task must be retained")
	}
}

// TestCompactRemovesInstanceOnlyRecordAfter7Days proves an instance-only
// record (no task_id) is removed once the 7-day boundary has passed.
func TestCompactRemovesInstanceOnlyRecordAfter7Days(t *testing.T) {
	dir := t.TempDir()
	s := mustOpen(t, dir, "daemon-1")
	defer s.Close()

	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	// AppendTaskDeleted against an unknown task_id is not a valid way to
	// produce an instance-only record (every append path in this package
	// requires task_id). Instance-only records are, per ADR-006, produced
	// by execution/attempt lifecycle event types this phase does not
	// implement (see INDEX.md's "Deferred this phase"); to test retention
	// of that record shape without inventing an unreviewed event type,
	// this test constructs one directly on disk with TaskID == nil, wired
	// into the existing chain exactly as a real instance-only record
	// would be.
	corr := journal.Correlation{TaskID: "kept-1"}
	if err := s.AppendTaskCreated(context.Background(), corr, journal.TaskCreatedPayload{
		TaskID: "kept-1",
		Fields: map[string]string{"subject": "stays", "status": "in_progress"},
	}); err != nil {
		t.Fatalf("AppendTaskCreated: %v", err)
	}
	rewriteAllRecordedAt(t, s, base)

	instanceRec := appendInstanceOnlyFixture(t, s, base)

	// Just under 7 days: retained.
	removed, err := s.Compact(base.Add(7*24*time.Hour - time.Second))
	if err != nil {
		t.Fatalf("Compact(justUnder): %v", err)
	}
	if removed != 0 {
		t.Fatalf("Compact(justUnder): removed = %d, want 0", removed)
	}
	if _, err := recordFileExists(dir, instanceRec.RecordID); err != nil {
		t.Fatalf("instance-only record missing before its 7-day boundary: %v", err)
	}

	// Just over 7 days: removed. The still-non-terminal task's record
	// must survive this same Compact call.
	removed, err = s.Compact(base.Add(7*24*time.Hour + time.Second))
	if err != nil {
		t.Fatalf("Compact(justOver): %v", err)
	}
	if removed != 1 {
		t.Fatalf("Compact(justOver): removed = %d, want 1 (only the instance-only record)", removed)
	}
	if _, err := recordFileExists(dir, instanceRec.RecordID); err == nil {
		t.Fatal("instance-only record still present after its 7-day boundary")
	}

	r, err := s.Replay()
	if err != nil {
		t.Fatalf("Replay after Compact(justOver): %v", err)
	}
	if _, ok := r.Tasks["kept-1"]; !ok {
		t.Fatal("non-terminal task kept-1 must survive Compact alongside instance-only expiry")
	}
}

// rewriteAllRecordedAt rewrites recorded_at on every currently committed
// record to at, recomputing each record's integrity digest so the chain
// remains valid, without changing predecessor linkage or record_id. It
// then re-opens the store's on-disk state into s so its in-memory tail
// stays consistent. This exists solely so retention tests can be fully
// deterministic (Compact(now) driven, never time.Sleep).
func rewriteAllRecordedAt(t *testing.T, s *Store, at time.Time) {
	t.Helper()
	chain, err := s.loadValidatedChainLocked()
	if err != nil {
		t.Fatalf("rewriteAllRecordedAt: loadValidatedChainLocked: %v", err)
	}
	for _, rec := range chain {
		rec.RecordedAt = at
		digest, err := canonicalDigest(rec)
		if err != nil {
			t.Fatalf("rewriteAllRecordedAt: canonicalDigest: %v", err)
		}
		rec.Integrity = journal.Integrity{CanonicalizationID: canonicalizationID, DigestAlgorithm: digestAlgorithm, Digest: digest}
		data, err := marshalRecord(rec)
		if err != nil {
			t.Fatalf("rewriteAllRecordedAt: marshal: %v", err)
		}
		if err := s.writeRecordFile(rec.RecordID, data, true); err != nil {
			t.Fatalf("rewriteAllRecordedAt: writeRecordFile(%s): %v", rec.RecordID, err)
		}
	}
}

// appendInstanceOnlyFixture constructs and durably commits (via the same
// staged write-fsync-rename path as a real append) one syntactically valid
// instance-only record (TaskID == nil) chained onto s's current tail, and
// advances s's in-memory tail/anchor to include it -- exactly as if a
// future instance-level event type (see INDEX.md's "Deferred this phase")
// had appended it.
func appendInstanceOnlyFixture(t *testing.T, s *Store, recordedAt time.Time) journal.Record {
	t.Helper()
	s.mu.Lock()
	defer s.mu.Unlock()

	id, err := newRecordID()
	if err != nil {
		t.Fatalf("newRecordID: %v", err)
	}
	var pred *string
	if s.tailRecordID != nil {
		v := *s.tailRecordID
		pred = &v
	}
	rec := journal.Record{
		SchemaVersion:       journalSchemaVersion,
		RecordID:            id,
		RecordedAt:          recordedAt,
		DaemonInstanceID:    s.daemonInstanceID,
		PredecessorRecordID: pred,
		EventType:           journal.EventTaskCreated,
		Payload:             []byte(`{"task_id":"","fields":{}}`),
	}
	digest, err := canonicalDigest(rec)
	if err != nil {
		t.Fatalf("canonicalDigest: %v", err)
	}
	rec.Integrity = journal.Integrity{CanonicalizationID: canonicalizationID, DigestAlgorithm: digestAlgorithm, Digest: digest}
	data, err := marshalRecord(rec)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if err := s.writeRecordFile(rec.RecordID, data, false); err != nil {
		t.Fatalf("writeRecordFile: %v", err)
	}
	newCount := s.recordCount + 1
	anchor := headAnchor{
		SchemaVersion:    journalSchemaVersion.String(),
		TailRecordID:     &id,
		RecordCount:      newCount,
		DaemonInstanceID: s.daemonInstanceID,
		UpdatedAt:        recordedAt,
	}
	if err := s.commitAnchor(anchor); err != nil {
		t.Fatalf("commitAnchor: %v", err)
	}
	s.tailRecordID = &id
	s.recordCount = newCount
	return rec
}

func recordFileExists(dir, id string) (bool, error) {
	_, err := os.Stat(filepath.Join(dir, "records", id+".json"))
	if err != nil {
		return false, err
	}
	return true, nil
}

// marshalRecord is a tiny wrapper kept local to the test file so fixture
// helpers above read as intent-revealing calls rather than bare
// json.Marshal invocations.
func marshalRecord(rec journal.Record) ([]byte, error) {
	return json.Marshal(rec)
}
