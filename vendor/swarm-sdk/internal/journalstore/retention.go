package journalstore

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/journal"
)

const (
	// terminalTaskRetention is how long a terminal (completed or deleted)
	// task's records remain retained after its terminal transition.
	// ADR-006 "Retention and deletion": "When a task becomes completed or
	// deleted, its records are retained for 30 days by default."
	terminalTaskRetention = 30 * 24 * time.Hour

	// instanceOnlyRetention is how long an instance-level record (one with
	// no task_id) is retained from its recorded_at. ADR-006: "Instance-
	// level records with no task are retained for seven days."
	instanceOnlyRetention = 7 * 24 * time.Hour
)

// taskRetentionMeta tracks, for one task_id, whether the task has reached a
// terminal state (status set to "completed" or "deleted", or a
// task.deleted tombstone) and the wall-clock time of the most recent such
// transition, per the full validated record chain.
type taskRetentionMeta struct {
	terminal   bool
	terminalAt time.Time
}

// Compact applies this phase's bounded retention rules by rewriting the
// journal to durably drop expired records under the writer lock:
//   - a non-terminal task's records are retained indefinitely, while that
//     task exists;
//   - a terminal task's (status set to "completed"/"deleted", or a
//     task.deleted tombstone -- whichever most recently transitioned it)
//     records, including its task.created, are retained together for
//     terminalTaskRetention (30 days) from that terminal transition, then
//     dropped together;
//   - an instance-only record (TaskID == nil) is retained for
//     instanceOnlyRetention (7 days) from its RecordedAt.
//
// now is the caller-supplied retention clock (never wall-clock time read
// internally), so callers can drive deterministic, non-sleeping tests and
// can implement ADR-006's clock-freeze safeguards (backward/forward jump
// detection, unclean-restart uncertainty) entirely outside this package.
//
// Compaction rewrites the journal (a new retained record set and a new
// head anchor) under the same write -> fsync -> close -> atomic rename ->
// directory fsync discipline as the append path in store.go, reusing
// writeRecordFile and commitAnchor rather than duplicating that logic. Any
// surviving record whose immediate predecessor was dropped has its
// predecessor_record_id relinked to the nearest surviving predecessor (or
// nil, if it becomes the new root) and its integrity digest recomputed;
// only such relinked records are rewritten on disk, using
// writeRecordFile's allowReplace=true mode (the one place this package
// intentionally replaces an already-committed record file, because
// compaction -- unlike a fresh append -- legitimately supersedes a prior
// version of a still-retained fact's linkage).
//
// This compaction is synchronous single-anchor rewrite, not ADR-006's
// privacy-barrier rebase; see CONTRACT.md's Deferred section for the gap
// this implies for rollback-resistant deletion. In particular: this
// package has only one anchor/generation, so retention deletion here is
// NOT leak-proof against a hand-restored backup of a deleted record the
// way ADR-006's full quiesce -> purge-intent -> dual-materialization ->
// dual-anchor-rotation -> source-epoch-deletion rebase would be. A
// hand-restored backup of records/<id>.json taken before a Compact call
// could reintroduce a record this method considered deleted. See
// INDEX.md's "Scope vs ADR-006" section.
func (s *Store) Compact(now time.Time) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.closed {
		return 0, ErrStoreClosed
	}

	chain, err := s.loadValidatedChainLocked()
	if err != nil {
		return 0, err
	}
	if len(chain) == 0 {
		return 0, nil
	}

	meta := computeTaskRetentionMeta(chain)

	retained := make([]journal.Record, 0, len(chain))
	for _, rec := range chain {
		if recordSurvivesRetention(rec, meta, now) {
			retained = append(retained, rec)
		}
	}

	removed := len(chain) - len(retained)
	if removed == 0 {
		return 0, nil
	}

	if err := s.relinkAndRewrite(retained); err != nil {
		return 0, err
	}

	if err := s.removeDroppedRecords(chain, retained); err != nil {
		return 0, err
	}

	if err := s.commitCompactedAnchor(retained, now); err != nil {
		return 0, err
	}

	return removed, nil
}

// computeTaskRetentionMeta walks chain in committed order and returns,
// per task_id, whether that task is currently terminal and the wall-clock
// time of its most recent terminal transition. A task.field_changed that
// sets status back to a non-terminal value un-terminals a task (its prior
// terminal-at is cleared); this mirrors the live taskstore semantics
// closely enough for retention purposes and never shortens retention for
// a task that is genuinely still non-terminal at the current tail.
func computeTaskRetentionMeta(chain []journal.Record) map[string]*taskRetentionMeta {
	meta := make(map[string]*taskRetentionMeta)
	ensure := func(taskID string) *taskRetentionMeta {
		m, ok := meta[taskID]
		if !ok {
			m = &taskRetentionMeta{}
			meta[taskID] = m
		}
		return m
	}

	for _, rec := range chain {
		switch rec.EventType {
		case journal.EventTaskCreated:
			var p journal.TaskCreatedPayload
			if err := json.Unmarshal(rec.Payload, &p); err == nil && p.TaskID != "" {
				ensure(p.TaskID)
			}
		case journal.EventTaskFieldChanged:
			var p journal.TaskFieldChangedPayload
			if err := json.Unmarshal(rec.Payload, &p); err == nil && p.TaskID != "" {
				m := ensure(p.TaskID)
				if p.FieldName == "status" {
					if p.NewValue == "completed" || p.NewValue == "deleted" {
						m.terminal = true
						m.terminalAt = rec.RecordedAt
					} else {
						m.terminal = false
						m.terminalAt = time.Time{}
					}
				}
			}
		case journal.EventTaskDeleted:
			var p journal.TaskDeletedPayload
			if err := json.Unmarshal(rec.Payload, &p); err == nil && p.TaskID != "" {
				m := ensure(p.TaskID)
				m.terminal = true
				m.terminalAt = rec.RecordedAt
			}
		}
	}
	return meta
}

// recordSurvivesRetention applies the three retention rules described on
// Compact to a single record.
func recordSurvivesRetention(rec journal.Record, meta map[string]*taskRetentionMeta, now time.Time) bool {
	if rec.TaskID == nil {
		return now.Sub(rec.RecordedAt) < instanceOnlyRetention
	}
	m, ok := meta[*rec.TaskID]
	if !ok || !m.terminal {
		return true
	}
	return now.Sub(m.terminalAt) < terminalTaskRetention
}

// relinkAndRewrite updates, in place, the predecessor_record_id of every
// record in retained (a subsequence of the original chain, in original
// order) so the surviving records again form one gapless linear chain
// rooted at a nil predecessor. Only records whose predecessor actually
// changed are recomputed (fresh integrity digest) and rewritten to disk,
// via writeRecordFile's allowReplace=true mode.
func (s *Store) relinkAndRewrite(retained []journal.Record) error {
	var prevID *string
	for i := range retained {
		rec := &retained[i]

		changed := (rec.PredecessorRecordID == nil) != (prevID == nil)
		if !changed && rec.PredecessorRecordID != nil && prevID != nil {
			changed = *rec.PredecessorRecordID != *prevID
		}

		if changed {
			var newPred *string
			if prevID != nil {
				v := *prevID
				newPred = &v
			}
			rec.PredecessorRecordID = newPred

			digest, err := canonicalDigest(*rec)
			if err != nil {
				return fmt.Errorf("journalstore: compact: recompute digest for relinked record %s: %w", rec.RecordID, err)
			}
			rec.Integrity = journal.Integrity{
				CanonicalizationID: canonicalizationID,
				DigestAlgorithm:    digestAlgorithm,
				Digest:             digest,
			}

			data, err := json.Marshal(*rec)
			if err != nil {
				return fmt.Errorf("journalstore: compact: marshal relinked record %s: %w", rec.RecordID, err)
			}
			if err := s.writeRecordFile(rec.RecordID, data, true); err != nil {
				s.degraded = true
				return fmt.Errorf("journalstore: compact: rewrite relinked record %s: %w", rec.RecordID, joinDegraded(err))
			}
		}

		id := rec.RecordID
		prevID = &id
	}
	return nil
}

// removeDroppedRecords deletes the on-disk file for every record in chain
// that is not present in retained, then fsyncs the records directory once
// after all deletions.
func (s *Store) removeDroppedRecords(chain, retained []journal.Record) error {
	keep := make(map[string]bool, len(retained))
	for _, r := range retained {
		keep[r.RecordID] = true
	}
	for _, rec := range chain {
		if keep[rec.RecordID] {
			continue
		}
		path := filepath.Join(s.recordsDir, rec.RecordID+".json")
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			s.degraded = true
			return fmt.Errorf("journalstore: compact: remove expired record %s: %w", rec.RecordID, joinDegraded(err))
		}
	}
	if err := fsyncDir(s.recordsDir); err != nil {
		s.degraded = true
		return fmt.Errorf("journalstore: compact: fsync records dir after removals: %w", joinDegraded(err))
	}
	return nil
}

// commitCompactedAnchor publishes a new head anchor naming retained's last
// element as the tail (or no tail, if retained is empty) and updates the
// in-memory tail/count only after that anchor commit durably succeeds.
func (s *Store) commitCompactedAnchor(retained []journal.Record, now time.Time) error {
	var newTail *string
	if len(retained) > 0 {
		id := retained[len(retained)-1].RecordID
		newTail = &id
	}
	anchor := headAnchor{
		SchemaVersion:    journalSchemaVersion.String(),
		TailRecordID:     newTail,
		RecordCount:      len(retained),
		DaemonInstanceID: s.daemonInstanceID,
		UpdatedAt:        now.UTC(),
	}
	if err := s.commitAnchor(anchor); err != nil {
		s.degraded = true
		return fmt.Errorf("journalstore: compact: commit head anchor: %w", joinDegraded(err))
	}

	s.tailRecordID = newTail
	s.recordCount = len(retained)
	s.degraded = false
	return nil
}
