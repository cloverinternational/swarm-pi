// Package journalstore implements a single-writer, single-generation durable
// append-only journal store for Journal v1 records (see
// docs/architecture/swarm-attach/adr-006-execution-journal-privacy.md).
//
// Precondition (single-writer, single-generation): exactly one *Store value
// may be open over a given directory at a time within one process, and no
// other process may concurrently write to that directory. This package
// provides in-process (sync.Mutex) serialization only; it does NOT take an
// OS-backed exclusive lock and does NOT implement ADR-006's dual-slot
// epoch/generation lineage, multi-writer fencing, or reader-lease gate. A
// second concurrent Store instance (in this process or another) over the
// same directory is out of scope and will corrupt journal state. See
// INDEX.md's "Scope vs ADR-006" section for the complete list of deferred
// protocol pieces.
package journalstore

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/attachcontract"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/journal"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/journalredact"
)

// var _ journal.Writer = (*Store)(nil) is the compile-time assertion that
// *Store implements journal.Writer. Phase 04's reviewer flagged a missing
// assertion like this as a concrete, avoidable gap; it is intentionally
// present here.
var _ journal.Writer = (*Store)(nil)

// journalSchemaVersion is the schema_version stamped on every record this
// package writes. It is Journal v1 per ADR-006/ADR-004.
var journalSchemaVersion = attachcontract.Version{Major: 1, Minor: 0}

const (
	// anchorFileName is the single head-anchor file for this journal
	// directory. Unlike records/manifests, this file's rename target is
	// intentionally replaced on every commit: it is the sole visibility
	// point for the current tail of the journal.
	anchorFileName = "anchor.json"

	// canonicalizationID identifies the canonicalization scheme used to
	// compute a record's integrity digest: encoding/json's struct
	// marshaling (stable field order, and stable sorted-key order for any
	// map values) over the record with its Integrity field zeroed.
	canonicalizationID = "journalstore/v1/json-stable-keys"

	// digestAlgorithm identifies the digest function used for integrity.
	digestAlgorithm = "sha256"
)

// Sentinel errors returned by this package. Callers (notably P05.D's
// wiring layer) can use errors.Is against ErrJournalDegraded to implement
// gap/decision-record telemetry when a journal write did not durably
// commit.
var (
	// ErrJournalDegraded wraps any error returned after a write, fsync, or
	// rename step failed partway through an append or compaction. The
	// in-memory tail is left unchanged so a subsequent append still
	// chains from the last durably committed record; the failed attempt's
	// staged/partial artifacts are never made visible.
	ErrJournalDegraded = errors.New("journalstore: journal write degraded (a durability step failed; see wrapped error)")

	// ErrUnknownField is returned when a caller attempts to journal a task
	// field name outside journal.AllowedTaskFields. ADR-006: "Unknown
	// fields are denied by default."
	ErrUnknownField = errors.New("journalstore: field is not in the allowed task field set")

	// ErrStoreClosed is returned by any append/compact call made after
	// Close.
	ErrStoreClosed = errors.New("journalstore: store is closed")
)

// headAnchor is the single head-anchor artifact for this journal
// directory. It names the current tail record and a record count for a
// cheap consistency check; it deliberately does NOT implement ADR-006's
// dual-slot epoch/generation anchor pair (see package doc and INDEX.md).
type headAnchor struct {
	SchemaVersion    string    `json:"schema_version"`
	TailRecordID     *string   `json:"tail_record_id"`
	RecordCount      int       `json:"record_count"`
	DaemonInstanceID string    `json:"daemon_instance_id"`
	UpdatedAt        time.Time `json:"updated_at"`
}

// Store is a durable, single-writer, single-generation journal store
// rooted at one directory. See the package doc comment for the
// single-writer precondition. The zero value is not usable; construct with
// Open.
type Store struct {
	mu sync.Mutex

	dir        string
	recordsDir string
	stagingDir string
	anchorPath string

	daemonInstanceID string

	// tailRecordID is the record_id of the most recently, durably
	// committed (anchor-published) record, or nil if the journal is
	// empty. It is updated only after a successful anchor commit.
	tailRecordID *string
	recordCount  int

	closed   bool
	degraded bool
}

// Open opens or creates a single-writer, single-generation durable journal
// store rooted at dir (e.g. "<workspace-meta-dir>/journal"). daemonInstanceID
// is a fresh opaque ID for this process lifetime (never a PID).
//
// dir (and its "records" and "staging" subdirectories) are created with
// 0o700 permissions if absent. If a head anchor already exists, Open
// resolves the current tail from it (and verifies the referenced tail
// record file is present); otherwise it initializes a fresh, empty
// journal. Open does not perform full chain/integrity validation -- call
// Replay for that.
func Open(dir, daemonInstanceID string) (*Store, error) {
	if dir == "" {
		return nil, fmt.Errorf("journalstore: Open: dir must not be empty")
	}
	if daemonInstanceID == "" {
		return nil, fmt.Errorf("journalstore: Open: daemonInstanceID must not be empty")
	}

	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("journalstore: create journal dir %s: %w", dir, err)
	}
	recordsDir := filepath.Join(dir, "records")
	stagingDir := filepath.Join(dir, "staging")
	if err := os.MkdirAll(recordsDir, 0o700); err != nil {
		return nil, fmt.Errorf("journalstore: create records dir %s: %w", recordsDir, err)
	}
	if err := os.MkdirAll(stagingDir, 0o700); err != nil {
		return nil, fmt.Errorf("journalstore: create staging dir %s: %w", stagingDir, err)
	}

	s := &Store{
		dir:              dir,
		recordsDir:       recordsDir,
		stagingDir:       stagingDir,
		anchorPath:       filepath.Join(dir, anchorFileName),
		daemonInstanceID: daemonInstanceID,
	}

	anchor, ok, err := readAnchor(s.anchorPath)
	if err != nil {
		return nil, fmt.Errorf("journalstore: read head anchor: %w", err)
	}
	if ok && anchor.TailRecordID != nil {
		recPath := filepath.Join(recordsDir, *anchor.TailRecordID+".json")
		if _, statErr := os.Stat(recPath); statErr != nil {
			return nil, fmt.Errorf("journalstore: head anchor references missing tail record %s: %w", *anchor.TailRecordID, statErr)
		}
		tail := *anchor.TailRecordID
		s.tailRecordID = &tail
		s.recordCount = anchor.RecordCount
	}

	return s, nil
}

// readAnchor reads and parses the head anchor at path. It returns
// ok == false (with a nil error) when no anchor file exists yet, which is
// the normal state of a freshly initialized journal.
func readAnchor(path string) (headAnchor, bool, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return headAnchor{}, false, nil
		}
		return headAnchor{}, false, err
	}
	var a headAnchor
	if err := json.Unmarshal(data, &a); err != nil {
		return headAnchor{}, false, fmt.Errorf("malformed head anchor at %s: %w", path, err)
	}
	return a, true, nil
}

// Close marks the store closed. Further append/compact calls return
// ErrStoreClosed. Close does not delete or modify any durable state.
func (s *Store) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closed = true
	return nil
}

// Degraded reports whether the most recent append or compaction failed
// partway through a durability step (see ErrJournalDegraded). It is
// provided for callers implementing gap/decision-record telemetry.
func (s *Store) Degraded() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.degraded
}

// AppendTaskCreated implements journal.Writer. It redacts every field
// value in payload.Fields (via journalredact.RedactFieldValue) before
// constructing the on-disk journal.TaskCreatedPayload, rejects any field
// name outside journal.AllowedTaskFields, computes and sets the record's
// integrity descriptor, and durably commits the record and a new head
// anchor using write -> fsync -> close -> atomic rename -> directory fsync
// for each artifact.
func (s *Store) AppendTaskCreated(ctx context.Context, corr journal.Correlation, payload journal.TaskCreatedPayload) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if payload.TaskID == "" {
		return fmt.Errorf("journalstore: AppendTaskCreated: task_id must not be empty")
	}

	redacted := make(map[string]string, len(payload.Fields))
	for name, value := range payload.Fields {
		if !journal.AllowedTaskFields[name] {
			return fmt.Errorf("journalstore: AppendTaskCreated: field %q: %w", name, ErrUnknownField)
		}
		redacted[name] = journalredact.RedactFieldValue(name, value, "")
	}

	onDisk := journal.TaskCreatedPayload{TaskID: payload.TaskID, Fields: redacted}
	raw, err := json.Marshal(onDisk)
	if err != nil {
		return fmt.Errorf("journalstore: AppendTaskCreated: marshal payload: %w", err)
	}

	return s.append(corr, journal.EventTaskCreated, raw)
}

// AppendTaskFieldChanged implements journal.Writer. It redacts the new
// field value (via journalredact.RedactFieldValue) before constructing the
// on-disk journal.TaskFieldChangedPayload, rejects a field name outside
// journal.AllowedTaskFields, and commits under the same crash-safe
// discipline as AppendTaskCreated.
func (s *Store) AppendTaskFieldChanged(ctx context.Context, corr journal.Correlation, payload journal.TaskFieldChangedPayload) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if payload.TaskID == "" {
		return fmt.Errorf("journalstore: AppendTaskFieldChanged: task_id must not be empty")
	}
	if !journal.AllowedTaskFields[payload.FieldName] {
		return fmt.Errorf("journalstore: AppendTaskFieldChanged: field %q: %w", payload.FieldName, ErrUnknownField)
	}

	onDisk := journal.TaskFieldChangedPayload{
		TaskID:              payload.TaskID,
		FieldName:           payload.FieldName,
		NewValue:            journalredact.RedactFieldValue(payload.FieldName, payload.NewValue, ""),
		PreviousValueDigest: payload.PreviousValueDigest,
	}
	raw, err := json.Marshal(onDisk)
	if err != nil {
		return fmt.Errorf("journalstore: AppendTaskFieldChanged: marshal payload: %w", err)
	}

	return s.append(corr, journal.EventTaskFieldChanged, raw)
}

// AppendTaskDeleted implements journal.Writer. Reason is passed through
// journalredact.RedactText as defense in depth (it is expected to already
// be a closed enum-like string upstream, but this package never persists a
// value it has not itself redacted). It commits under the same crash-safe
// discipline as AppendTaskCreated.
func (s *Store) AppendTaskDeleted(ctx context.Context, corr journal.Correlation, payload journal.TaskDeletedPayload) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if payload.TaskID == "" {
		return fmt.Errorf("journalstore: AppendTaskDeleted: task_id must not be empty")
	}

	onDisk := journal.TaskDeletedPayload{
		TaskID: payload.TaskID,
		Reason: journalredact.RedactText(payload.Reason),
	}
	raw, err := json.Marshal(onDisk)
	if err != nil {
		return fmt.Errorf("journalstore: AppendTaskDeleted: marshal payload: %w", err)
	}

	return s.append(corr, journal.EventTaskDeleted, raw)
}

// append builds, integrity-stamps, and durably commits one record whose
// predecessor_record_id is the current tail, then durably commits a new
// head anchor naming that record as the new tail. On any failure the
// in-memory tail is left unchanged (the failed attempt never corrupts a
// subsequent append) and the returned error wraps ErrJournalDegraded.
func (s *Store) append(corr journal.Correlation, eventType journal.EventType, payload json.RawMessage) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.closed {
		return ErrStoreClosed
	}

	id, err := newRecordID()
	if err != nil {
		return fmt.Errorf("journalstore: generate record id: %w", errors.Join(ErrJournalDegraded, err))
	}

	var pred *string
	if s.tailRecordID != nil {
		v := *s.tailRecordID
		pred = &v
	}

	rec := journal.Record{
		SchemaVersion:       journalSchemaVersion,
		RecordID:            id,
		RecordedAt:          time.Now().UTC(),
		DaemonInstanceID:    s.daemonInstanceID,
		ClientSessionID:     nonEmptyPtr(corr.ClientSessionID),
		TaskID:              nonEmptyPtr(corr.TaskID),
		ExecutionID:         nonEmptyPtr(corr.ExecutionID),
		AttemptID:           nonEmptyPtr(corr.AttemptID),
		ConversationID:      nonEmptyPtr(corr.ConversationID),
		WorkspaceID:         nonEmptyPtr(corr.WorkspaceID),
		PredecessorRecordID: pred,
		EventType:           eventType,
		Payload:             payload,
	}

	digest, err := canonicalDigest(rec)
	if err != nil {
		return fmt.Errorf("journalstore: compute integrity digest for record %s: %w", id, err)
	}
	rec.Integrity = journal.Integrity{
		CanonicalizationID: canonicalizationID,
		DigestAlgorithm:    digestAlgorithm,
		Digest:             digest,
	}

	data, err := json.Marshal(rec)
	if err != nil {
		return fmt.Errorf("journalstore: marshal record %s: %w", id, err)
	}

	if err := s.writeRecordFile(rec.RecordID, data, false); err != nil {
		s.degraded = true
		return fmt.Errorf("journalstore: stage/commit record %s: %w", id, errors.Join(ErrJournalDegraded, err))
	}

	newTail := rec.RecordID
	newCount := s.recordCount + 1
	anchor := headAnchor{
		SchemaVersion:    journalSchemaVersion.String(),
		TailRecordID:     &newTail,
		RecordCount:      newCount,
		DaemonInstanceID: s.daemonInstanceID,
		UpdatedAt:        time.Now().UTC(),
	}
	if err := s.commitAnchor(anchor); err != nil {
		s.degraded = true
		return fmt.Errorf("journalstore: commit head anchor after record %s: %w", id, errors.Join(ErrJournalDegraded, err))
	}

	s.tailRecordID = &newTail
	s.recordCount = newCount
	s.degraded = false
	return nil
}

// writeRecordFile stages data to a transaction-unique temp path in the
// staging directory, fsyncs and closes it, then commits it to
// "<recordsDir>/<id>.json" and fsyncs the records directory.
//
// When allowReplace is false (the normal append path), the commit uses
// link-then-remove: os.Link fails with EEXIST if the destination already
// exists, giving no-replace atomic-rename semantics on POSIX filesystems
// for a single writer (os.Rename alone would silently overwrite). When
// allowReplace is true (used only by Compact, which may rewrite a
// surviving record's predecessor_record_id and integrity after removing
// an expired predecessor), the commit uses a normal atomic os.Rename that
// replaces any existing file of the same name.
func (s *Store) writeRecordFile(id string, data []byte, allowReplace bool) error {
	tmp, err := os.CreateTemp(s.stagingDir, "rec-*.tmp")
	if err != nil {
		return fmt.Errorf("create staged record file: %w", err)
	}
	tmpName := tmp.Name()
	committed := false
	defer func() {
		if !committed {
			_ = os.Remove(tmpName)
		}
	}()

	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write staged record file %s: %w", tmpName, err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("fsync staged record file %s: %w", tmpName, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close staged record file %s: %w", tmpName, err)
	}

	dst := filepath.Join(s.recordsDir, id+".json")
	if allowReplace {
		if err := os.Rename(tmpName, dst); err != nil {
			return fmt.Errorf("rename staged record to %s: %w", dst, err)
		}
		committed = true
	} else {
		if err := os.Link(tmpName, dst); err != nil {
			return fmt.Errorf("no-replace commit of record to %s: %w", dst, err)
		}
		committed = true
		// Best-effort: the record is already durably visible at dst via
		// the hard link above; a failure to remove the now-redundant
		// staging copy does not affect correctness or visibility.
		_ = os.Remove(tmpName)
	}

	if err := fsyncDir(s.recordsDir); err != nil {
		return fmt.Errorf("fsync records dir: %w", err)
	}
	return nil
}

// commitAnchor stages anchor to a transaction-unique temp path, fsyncs and
// closes it, then atomically renames it OVER the single anchor file (the
// sole visibility point for the journal's tail) and fsyncs the journal
// root directory.
func (s *Store) commitAnchor(anchor headAnchor) error {
	data, err := json.Marshal(anchor)
	if err != nil {
		return fmt.Errorf("marshal head anchor: %w", err)
	}

	tmp, err := os.CreateTemp(s.stagingDir, "anchor-*.tmp")
	if err != nil {
		return fmt.Errorf("create staged anchor file: %w", err)
	}
	tmpName := tmp.Name()
	committed := false
	defer func() {
		if !committed {
			_ = os.Remove(tmpName)
		}
	}()

	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write staged anchor file: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("fsync staged anchor file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close staged anchor file: %w", err)
	}

	if err := os.Rename(tmpName, s.anchorPath); err != nil {
		return fmt.Errorf("rename staged anchor over %s: %w", s.anchorPath, err)
	}
	committed = true

	if err := fsyncDir(s.dir); err != nil {
		return fmt.Errorf("fsync journal root dir: %w", err)
	}
	return nil
}

// fsyncDir opens path and fsyncs it, so a preceding rename within it is
// durable across a crash.
func fsyncDir(path string) error {
	d, err := os.Open(path)
	if err != nil {
		return err
	}
	defer d.Close()
	return d.Sync()
}

// canonicalDigest returns the sha256 hex digest of rec's canonical bytes.
// Canonicalization is encoding/json's struct marshaling (stable field
// order; map keys, if any appear inside Payload, are sorted by
// encoding/json automatically) applied to a copy of rec with its Integrity
// field zeroed, so the digest can be verified by any reader that
// recomputes it the same way from a stored record.
func canonicalDigest(rec journal.Record) (string, error) {
	rec.Integrity = journal.Integrity{}
	data, err := json.Marshal(rec)
	if err != nil {
		return "", fmt.Errorf("canonicalize record: %w", err)
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}

// newRecordID returns a fresh, globally-unique-enough opaque record ID.
// It is never derived from a timestamp, PID, or task text.
func newRecordID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("read random bytes: %w", err)
	}
	return "rec_" + hex.EncodeToString(b[:]), nil
}

// nonEmptyPtr returns nil for an empty string (encoded as JSON null) or a
// pointer to a copy of s otherwise.
func nonEmptyPtr(s string) *string {
	if s == "" {
		return nil
	}
	v := s
	return &v
}

// joinDegraded wraps err together with ErrJournalDegraded so callers can
// errors.Is against either the specific underlying cause or the generic
// degraded-journal sentinel. Used by both the append path (inline) and the
// compaction path (retention.go) to keep that wrapping convention in one
// place.
func joinDegraded(err error) error {
	return errors.Join(ErrJournalDegraded, err)
}
