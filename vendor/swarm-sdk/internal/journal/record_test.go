package journal

import (
	"encoding/json"
	"testing"
	"time"
)

// TestNewRecordIDUniqueness is a real collision-resistance smoke test: it
// mints a large number of records and asserts no two share a record_id.
// This is not tautological -- a counter-, PID-, or timestamp-derived ID
// generator (the exact anti-patterns ADR-006 forbids) could plausibly pass
// a small N but this loop is large enough to make accidental collisions
// under crypto/rand statistically indistinguishable from zero while still
// being cheap to run in CI.
func TestNewRecordIDUniqueness(t *testing.T) {
	const n = 10000
	seen := make(map[string]bool, n)
	for i := 0; i < n; i++ {
		rec := NewRecord(EventTaskCreated, json.RawMessage(`{}`), "daemon-1", nil, Correlation{})
		if rec.RecordID == "" {
			t.Fatalf("iteration %d: record_id must not be empty", i)
		}
		if seen[rec.RecordID] {
			t.Fatalf("iteration %d: duplicate record_id %q generated", i, rec.RecordID)
		}
		seen[rec.RecordID] = true
	}
}

// TestNewRecordZeroValues asserts NewRecord's documented invariants beyond
// ID uniqueness: SchemaVersion is set, RecordedAt is populated, Integrity is
// left zero-valued (populated later by the store), and the EventType,
// DaemonInstanceID, Payload, and PredecessorRecordID are threaded through
// exactly as given.
func TestNewRecordZeroValues(t *testing.T) {
	pred := "predecessor-abc"
	payload := json.RawMessage(`{"task_id":"t1"}`)
	rec := NewRecord(EventTaskFieldChanged, payload, "daemon-xyz", &pred, Correlation{TaskID: "t1"})

	if rec.SchemaVersion != SchemaVersion {
		t.Fatalf("SchemaVersion = %v, want %v", rec.SchemaVersion, SchemaVersion)
	}
	if rec.RecordedAt.IsZero() {
		t.Fatalf("RecordedAt must not be zero")
	}
	if rec.RecordedAt.Location() != time.UTC {
		t.Fatalf("RecordedAt must be UTC, got location %v", rec.RecordedAt.Location())
	}
	if (rec.Integrity != Integrity{}) {
		t.Fatalf("Integrity must be zero-valued from NewRecord, got %+v", rec.Integrity)
	}
	if rec.EventType != EventTaskFieldChanged {
		t.Fatalf("EventType = %v, want %v", rec.EventType, EventTaskFieldChanged)
	}
	if rec.DaemonInstanceID != "daemon-xyz" {
		t.Fatalf("DaemonInstanceID = %v, want daemon-xyz", rec.DaemonInstanceID)
	}
	if rec.PredecessorRecordID == nil || *rec.PredecessorRecordID != pred {
		t.Fatalf("PredecessorRecordID = %v, want %v", rec.PredecessorRecordID, pred)
	}
	if string(rec.Payload) != string(payload) {
		t.Fatalf("Payload = %s, want %s", rec.Payload, payload)
	}
	if rec.TaskID == nil || *rec.TaskID != "t1" {
		t.Fatalf("TaskID = %v, want t1", rec.TaskID)
	}
	if rec.ClientSessionID != nil {
		t.Fatalf("ClientSessionID = %v, want nil (empty correlation field)", rec.ClientSessionID)
	}
}

// TestCorrelationFieldsMarshalNullAndRoundTrip proves that empty-string
// Correlation fields produce Record pointer fields that marshal to JSON
// null (never an empty-string leaf value), and that unmarshaling that null
// back into a fresh Record yields nil pointers again -- i.e. the Go zero
// value for *string, not a non-nil pointer to "" and not the literal string
// "null" leaking into the field.
func TestCorrelationFieldsMarshalNullAndRoundTrip(t *testing.T) {
	rec := NewRecord(EventTaskDeleted, json.RawMessage(`{}`), "daemon-1", nil, Correlation{})

	data, err := json.Marshal(rec)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("Unmarshal into map: %v", err)
	}

	nullFields := []string{
		"client_session_id", "task_id", "execution_id", "attempt_id",
		"conversation_id", "workspace_id", "predecessor_record_id",
	}
	for _, f := range nullFields {
		v, ok := raw[f]
		if !ok {
			t.Fatalf("field %q missing from marshaled JSON entirely (must be present and null)", f)
		}
		if string(v) != "null" {
			t.Fatalf("field %q = %s, want literal JSON null", f, v)
		}
	}

	var roundTripped Record
	if err := json.Unmarshal(data, &roundTripped); err != nil {
		t.Fatalf("Unmarshal into Record: %v", err)
	}
	if roundTripped.ClientSessionID != nil {
		t.Fatalf("ClientSessionID after round trip = %v, want nil", roundTripped.ClientSessionID)
	}
	if roundTripped.TaskID != nil {
		t.Fatalf("TaskID after round trip = %v, want nil", roundTripped.TaskID)
	}
	if roundTripped.ExecutionID != nil {
		t.Fatalf("ExecutionID after round trip = %v, want nil", roundTripped.ExecutionID)
	}
	if roundTripped.AttemptID != nil {
		t.Fatalf("AttemptID after round trip = %v, want nil", roundTripped.AttemptID)
	}
	if roundTripped.ConversationID != nil {
		t.Fatalf("ConversationID after round trip = %v, want nil", roundTripped.ConversationID)
	}
	if roundTripped.WorkspaceID != nil {
		t.Fatalf("WorkspaceID after round trip = %v, want nil", roundTripped.WorkspaceID)
	}
	if roundTripped.PredecessorRecordID != nil {
		t.Fatalf("PredecessorRecordID after round trip = %v, want nil", roundTripped.PredecessorRecordID)
	}
}

// TestCorrelationNonEmptyFieldsSurvive proves the inverse of the null-field
// test: a non-empty Correlation field marshals as a JSON string (not null)
// and round-trips to the same non-nil value, so the null-encoding path
// isn't accidentally swallowing real values too.
func TestCorrelationNonEmptyFieldsSurvive(t *testing.T) {
	corr := Correlation{
		ClientSessionID: "sess-1",
		TaskID:          "task-1",
		ExecutionID:     "exec-1",
		AttemptID:       "attempt-1",
		ConversationID:  "conv-1",
		WorkspaceID:     "ws-1",
	}
	rec := NewRecord(EventTaskCreated, json.RawMessage(`{}`), "daemon-1", nil, corr)

	data, err := json.Marshal(rec)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	var roundTripped Record
	if err := json.Unmarshal(data, &roundTripped); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}

	check := func(name string, got *string, want string) {
		if got == nil || *got != want {
			t.Fatalf("%s = %v, want pointer to %q", name, got, want)
		}
	}
	check("ClientSessionID", roundTripped.ClientSessionID, "sess-1")
	check("TaskID", roundTripped.TaskID, "task-1")
	check("ExecutionID", roundTripped.ExecutionID, "exec-1")
	check("AttemptID", roundTripped.AttemptID, "attempt-1")
	check("ConversationID", roundTripped.ConversationID, "conv-1")
	check("WorkspaceID", roundTripped.WorkspaceID, "ws-1")
}

// TestSchemaVersionMarshalsAsString proves SchemaVersion (an
// attachcontract.Version) always marshals as a JSON string such as "1.0",
// never a bare JSON number, per ADR-004's version model reused by ADR-006.
func TestSchemaVersionMarshalsAsString(t *testing.T) {
	rec := NewRecord(EventTaskCreated, json.RawMessage(`{}`), "daemon-1", nil, Correlation{})

	data, err := json.Marshal(rec)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("Unmarshal into map: %v", err)
	}

	sv, ok := raw["schema_version"]
	if !ok {
		t.Fatalf("schema_version field missing from marshaled JSON")
	}
	if len(sv) == 0 || sv[0] != '"' {
		t.Fatalf("schema_version = %s, want a JSON string (leading double quote), never a bare number", sv)
	}

	var s string
	if err := json.Unmarshal(sv, &s); err != nil {
		t.Fatalf("schema_version did not decode as a JSON string: %v (raw: %s)", err, sv)
	}
	if s != "1.0" {
		t.Fatalf("schema_version string = %q, want %q", s, "1.0")
	}
}
