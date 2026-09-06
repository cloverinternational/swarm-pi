// Package journal defines the canonical Journal v1 record schema shared by
// the execution-journal subsystem introduced in Phase 05 (see
// docs/architecture/swarm-attach/adr-006-execution-journal-privacy.md). This
// file defines the envelope types (Record, Correlation, Integrity,
// EventType) and the NewRecord constructor that mints a fresh envelope with
// a collision-resistant record ID and the current schema version.
//
// This package intentionally implements NONE of ADR-006's dual-anchor,
// privacy-barrier, or crash-safe transaction machinery; see INDEX.md's
// "Scope" section for the authoritative list of what is deferred.
package journal

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/attachcontract"
)

// EventType is the closed, versioned discriminator selecting an allowlisted
// payload DTO for one Journal v1 Record, per ADR-006's "Journal records"
// section.
type EventType string

const (
	// EventTaskCreated starts a task stream. Its payload is
	// TaskCreatedPayload, containing the sanitized initial values of
	// journaled task fields.
	EventTaskCreated EventType = "task.created"
	// EventTaskFieldChanged records exactly one field name and its
	// sanitized new value. Its payload is TaskFieldChangedPayload.
	EventTaskFieldChanged EventType = "task.field_changed"
	// EventTaskDeleted is a tombstone with no task body. Its payload is
	// TaskDeletedPayload.
	EventTaskDeleted EventType = "task.deleted"
)

// SchemaVersion is the Journal v1 envelope schema version minted by
// NewRecord. It is "1.0" per ADR-006/ADR-004's major.minor versioning
// convention; see attachcontract.Version for the marshaling behavior (a
// JSON string, never a bare number).
var SchemaVersion = attachcontract.Version{Major: 1, Minor: 0}

// Integrity is typed metadata describing how a Record's canonical bytes were
// digested, per ADR-006's "integrity" envelope field: "Typed metadata
// containing at least canonicalization ID, digest algorithm, and record
// digest." NewRecord leaves Integrity zero-valued; see the doc comment on
// NewRecord for why.
type Integrity struct {
	// CanonicalizationID identifies the canonical-byte-encoding scheme used
	// to produce Digest (e.g. a specific canonical JSON serialization).
	CanonicalizationID string `json:"canonicalization_id"`
	// DigestAlgorithm names the digest algorithm used to compute Digest
	// (e.g. "sha256").
	DigestAlgorithm string `json:"digest_algorithm"`
	// Digest is the hex (or otherwise encoded) digest of the record's
	// canonical bytes under CanonicalizationID/DigestAlgorithm.
	Digest string `json:"digest"`
}

// Record is the canonical Journal v1 envelope. Field names/JSON tags are
// exactly ADR-006's list; do not add, rename, or drop fields.
type Record struct {
	SchemaVersion       attachcontract.Version `json:"schema_version"`
	RecordID            string                 `json:"record_id"`
	RecordedAt          time.Time              `json:"recorded_at"`
	DaemonInstanceID    string                 `json:"daemon_instance_id"`
	ClientSessionID     *string                `json:"client_session_id"`
	TaskID              *string                `json:"task_id"`
	ExecutionID         *string                `json:"execution_id"`
	AttemptID           *string                `json:"attempt_id"`
	ConversationID      *string                `json:"conversation_id"`
	WorkspaceID         *string                `json:"workspace_id"`
	PredecessorRecordID *string                `json:"predecessor_record_id"`
	EventType           EventType              `json:"event_type"`
	Payload             json.RawMessage        `json:"payload"`
	Integrity           Integrity              `json:"integrity"`
}

// Correlation carries the optional correlation identifiers for one record.
// All fields nil/empty-string means "not applicable" (encoded as JSON null).
type Correlation struct {
	ClientSessionID string
	TaskID          string
	ExecutionID     string
	AttemptID       string
	ConversationID  string
	WorkspaceID     string
}

// recordIDByteLen is the number of random bytes used to build a record_id
// (128 bits), matching ADR-006's requirement that record_id be "a globally
// unique opaque ID" that is "random or otherwise collision-resistant" --
// never a counter, PID, or timestamp-derived value.
const recordIDByteLen = 16

// newRecordID mints a fresh, globally-unique opaque record ID using
// crypto/rand, hex-encoded into a 128-bit (32 hex character) opaque token.
// It panics only if the system CSPRNG is unavailable, which indicates the
// host environment is broken beyond this package's ability to recover
// safely.
func newRecordID() string {
	buf := make([]byte, recordIDByteLen)
	if _, err := rand.Read(buf); err != nil {
		// crypto/rand.Read failing indicates a broken host CSPRNG; there is
		// no safe non-random fallback per ADR-006's collision-resistance
		// requirement, so we fail loudly rather than silently degrade to a
		// weaker ID source.
		panic(fmt.Sprintf("journal: crypto/rand unavailable: %v", err))
	}
	return hex.EncodeToString(buf)
}

// stringPtrOrNil returns nil for an empty string and a pointer to s
// otherwise, implementing ADR-006's "Correlation fields remain present and
// are encoded as null when inapplicable."
func stringPtrOrNil(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// NewRecord builds a fresh Journal v1 Record envelope for eventType with the
// given payload, daemon instance ID, optional predecessor record ID, and
// correlation identifiers. It generates a fresh collision-resistant
// record_id (crypto/rand-backed, never a counter/PID/timestamp derivation),
// sets RecordedAt to time.Now().UTC(), and sets SchemaVersion to this
// package's SchemaVersion constant.
//
// NewRecord leaves Integrity zero-valued. Integrity is populated by the
// store (swarm-sdk/internal/journalstore, P05.C) at serialization time, once
// the record's canonical bytes are known and can be digested -- NewRecord
// runs before that canonical encoding exists, so it cannot compute a
// meaningful digest here. Callers must not treat a zero-valued Integrity as
// a validated/persisted record.
func NewRecord(eventType EventType, payload json.RawMessage, daemonInstanceID string, predecessorRecordID *string, corr Correlation) Record {
	return Record{
		SchemaVersion:       SchemaVersion,
		RecordID:            newRecordID(),
		RecordedAt:          time.Now().UTC(),
		DaemonInstanceID:    daemonInstanceID,
		ClientSessionID:     stringPtrOrNil(corr.ClientSessionID),
		TaskID:              stringPtrOrNil(corr.TaskID),
		ExecutionID:         stringPtrOrNil(corr.ExecutionID),
		AttemptID:           stringPtrOrNil(corr.AttemptID),
		ConversationID:      stringPtrOrNil(corr.ConversationID),
		WorkspaceID:         stringPtrOrNil(corr.WorkspaceID),
		PredecessorRecordID: predecessorRecordID,
		EventType:           eventType,
		Payload:             payload,
		Integrity:           Integrity{},
	}
}
