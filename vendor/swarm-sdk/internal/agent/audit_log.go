// Package agent — audit_log.go.
//
// Phase 5 of the steering-agent-with-tools redesign
// (docs/steering-redesign/steering-redesign.pdf).
//
// AuditLog is a small JSONL append-only writer that captures the steering
// driver's audit stream to disk for forensics. It is intentionally simple:
//
//   - Writes are queued through a buffered channel
//   - A single background goroutine consumes the channel
//   - The goroutine owns the file handle (no shared mutex on writes)
//   - Stop() closes the channel, waits for the drain, then closes the file
//   - Empty path => nil AuditLog => all writes are silent no-ops
//
// JSONL format: one JSON object per line. Fields are stable so external
// log processors can rely on them:
//
//	{"ts":"<RFC3339Nano>","kind":"snapshot","concerns":N,"flushed":N,"dropped":N}
//	{"ts":"<RFC3339Nano>","kind":"halt","peer":"<handle>","reason":"...","ttl_seconds":N}
//	{"ts":"<RFC3339Nano>","kind":"ask","question":"...","urgency":"..."}
//
// All writes are best-effort: if the channel is full the record is dropped
// (and counted) rather than blocking the driver's flush hot path.
package agent

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"
)

// AuditRecord is the on-disk JSONL shape. Encoders emit only non-zero
// fields so the JSON stays compact.
type AuditRecord struct {
	Timestamp  string `json:"ts"`
	Kind       string `json:"kind"`
	Concerns   int    `json:"concerns,omitempty"`
	Flushed    uint64 `json:"flushed,omitempty"`
	Dropped    uint64 `json:"dropped,omitempty"`
	Peer       string `json:"peer,omitempty"`
	Reason     string `json:"reason,omitempty"`
	TTLSeconds int    `json:"ttl_seconds,omitempty"`
	Question   string `json:"question,omitempty"`
	Urgency    string `json:"urgency,omitempty"`
}

// AuditLog appends JSONL records to a file. Thread-safe via channel-
// serialised writes.
type AuditLog struct {
	path string

	once    sync.Once
	closing atomic.Bool
	ch      chan AuditRecord
	done    chan struct{}
	file    *os.File

	// Diagnostic counters.
	writtenTotal atomic.Uint64
	droppedTotal atomic.Uint64
}

// NewAuditLog opens (or creates) the audit log file at path and starts a
// background writer. An empty path returns nil — callers should treat nil
// as "audit logging disabled" and skip Write calls (or call Write on nil
// safely, which is a no-op).
//
// The parent directory is created if missing.
func NewAuditLog(path string) (*AuditLog, error) {
	if path == "" {
		return nil, nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return nil, err
	}
	a := &AuditLog{
		path: path,
		ch:   make(chan AuditRecord, 256),
		done: make(chan struct{}),
		file: f,
	}
	go a.run()
	return a, nil
}

// Path returns the file path the log writes to. Empty if the log is nil.
func (a *AuditLog) Path() string {
	if a == nil {
		return ""
	}
	return a.path
}

// WrittenTotal returns the cumulative number of records successfully
// written to disk.
func (a *AuditLog) WrittenTotal() uint64 {
	if a == nil {
		return 0
	}
	return a.writtenTotal.Load()
}

// DroppedTotal returns the cumulative number of records dropped due to
// channel backpressure.
func (a *AuditLog) DroppedTotal() uint64 {
	if a == nil {
		return 0
	}
	return a.droppedTotal.Load()
}

// Write enqueues a record. Non-blocking: when the channel is full the
// record is dropped and droppedTotal is incremented. Safe on a nil
// receiver — no-op.
//
// The Timestamp field is auto-populated when empty.
func (a *AuditLog) Write(rec AuditRecord) {
	if a == nil {
		return
	}
	if a.closing.Load() {
		return
	}
	if rec.Timestamp == "" {
		rec.Timestamp = time.Now().UTC().Format(time.RFC3339Nano)
	}
	select {
	case a.ch <- rec:
	default:
		a.droppedTotal.Add(1)
	}
}

// Close flushes pending records, closes the file, and returns. Idempotent.
// Safe on nil.
func (a *AuditLog) Close() error {
	if a == nil {
		return nil
	}
	var err error
	a.once.Do(func() {
		a.closing.Store(true)
		close(a.ch)
		<-a.done
		err = a.file.Close()
	})
	return err
}

// run is the background writer loop. Owns the file handle.
func (a *AuditLog) run() {
	defer close(a.done)
	enc := json.NewEncoder(a.file)
	for rec := range a.ch {
		if err := enc.Encode(&rec); err != nil {
			// Best-effort: ignore write errors. A future iteration
			// could reopen the file or surface a metric.
			continue
		}
		a.writtenTotal.Add(1)
	}
}

// ErrAuditLogClosed is reserved for future use by callers that want to
// distinguish a closed log from a never-started one.
var ErrAuditLogClosed = errors.New("agent: audit log is closed")
