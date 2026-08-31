package journalstore

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/journal"
)

// ReplayedTask is the sanitized, attach-visible reconstruction of one task
// as of the end of the validated committed record set. A task that has
// been tombstoned (task.deleted) is removed entirely from ReplayResult.Tasks
// per ADR-006 ("Remove attach-visible state at task.deleted"); it is never
// represented here with a tombstoned flag still set.
type ReplayedTask struct {
	// TaskID is the opaque logical task ID (duplicated from the map key
	// for convenience when a ReplayedTask value is passed around alone).
	TaskID string

	// Fields holds the sanitized, journaled field values accumulated from
	// this task's task.created seed plus every subsequent
	// task.field_changed applied to it, keyed by journaled field name.
	Fields map[string]string
}

// SkippedRecord reports one record that was structurally valid (it passed
// duplicate-ID, predecessor-chain, and integrity-digest validation) but
// could not be semantically applied during replay -- e.g. a
// task.field_changed or task.deleted naming a task_id that does not exist
// or has already been tombstoned. ADR-006: "Apply task.field_changed only
// to a task that exists and is not tombstoned." Per the P05 contract this
// case is skip-with-report rather than fail-closed, unlike the structural
// validation performed before any record is applied (duplicate record ID,
// chain gap/cycle, and integrity-digest mismatch, which DO fail closed --
// see loadValidatedChainLocked).
type SkippedRecord struct {
	RecordID string
	TaskID   string
	Reason   string
}

// ReplayResult is the outcome of a successful Replay.
type ReplayResult struct {
	// Tasks is the reconstructed, attach-visible sanitized task state,
	// keyed by task_id. Tombstoned and never-created tasks are absent.
	Tasks map[string]ReplayedTask

	// Skipped lists every structurally valid record that could not be
	// semantically applied (see SkippedRecord).
	Skipped []SkippedRecord
}

// Replay reconstructs sanitized per-task state by reading every committed
// record in this journal and applying it in predecessor-linked order,
// starting from task.created.
//
// Before applying anything, Replay validates the complete committed record
// set and fails closed (returns an error identifying the first violation,
// rather than best-effort continuing) when: a record_id appears more than
// once, the predecessor chain has a gap, cycle, or fork, or any record's
// Integrity.Digest does not match a freshly recomputed digest of its
// canonical bytes. This matches ADR-006's "Ordering and replay semantics":
// "A duplicate ID fails closed even when both canonical byte strings
// match" and "[stop and] reject the entire selected generation" on a
// predecessor gap, duplicate ID, fork, cycle, or integrity failure.
//
// Once the record set is structurally validated, Replay applies:
//   - task.created seeds a task's sanitized field map from its Fields.
//   - task.field_changed sets exactly one field on an existing,
//     non-tombstoned task; applied to an unknown or already-tombstoned
//     task, it is skipped and reported in ReplayResult.Skipped rather than
//     failing the whole replay (see SkippedRecord's doc comment for why
//     this is a distinct, more lenient failure mode than the structural
//     validation above).
//   - task.deleted tombstones a task and removes it from
//     ReplayResult.Tasks; a later task.created for the same task_id is a
//     hard validation error (a new task requires a new, distinct task_id
//     per ADR-006), and later field changes against the tombstoned ID are
//     skip-with-reported rather than reviving it.
func (s *Store) Replay() (*ReplayResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	chain, err := s.loadValidatedChainLocked()
	if err != nil {
		return nil, err
	}

	tasks := make(map[string]*ReplayedTask)
	tombstoned := make(map[string]bool)
	var skipped []SkippedRecord

	for _, rec := range chain {
		switch rec.EventType {
		case journal.EventTaskCreated:
			var p journal.TaskCreatedPayload
			if err := json.Unmarshal(rec.Payload, &p); err != nil {
				return nil, fmt.Errorf("journalstore: replay: record %s: malformed task.created payload: %w", rec.RecordID, err)
			}
			if p.TaskID == "" {
				return nil, fmt.Errorf("journalstore: replay: record %s: task.created missing task_id", rec.RecordID)
			}
			if _, exists := tasks[p.TaskID]; exists {
				return nil, fmt.Errorf("journalstore: replay: record %s: task.created for %q: task_id already active", rec.RecordID, p.TaskID)
			}
			if tombstoned[p.TaskID] {
				return nil, fmt.Errorf("journalstore: replay: record %s: task.created for %q: task_id was previously tombstoned and cannot be reused", rec.RecordID, p.TaskID)
			}
			fields := make(map[string]string, len(p.Fields))
			for k, v := range p.Fields {
				fields[k] = v
			}
			tasks[p.TaskID] = &ReplayedTask{TaskID: p.TaskID, Fields: fields}

		case journal.EventTaskFieldChanged:
			var p journal.TaskFieldChangedPayload
			if err := json.Unmarshal(rec.Payload, &p); err != nil {
				return nil, fmt.Errorf("journalstore: replay: record %s: malformed task.field_changed payload: %w", rec.RecordID, err)
			}
			t, ok := tasks[p.TaskID]
			if !ok {
				reason := "an unknown task"
				if tombstoned[p.TaskID] {
					reason = "an already-tombstoned task"
				}
				skipped = append(skipped, SkippedRecord{
					RecordID: rec.RecordID,
					TaskID:   p.TaskID,
					Reason:   fmt.Sprintf("task.field_changed(%s) applied to %s", p.FieldName, reason),
				})
				continue
			}
			t.Fields[p.FieldName] = p.NewValue

		case journal.EventTaskDeleted:
			var p journal.TaskDeletedPayload
			if err := json.Unmarshal(rec.Payload, &p); err != nil {
				return nil, fmt.Errorf("journalstore: replay: record %s: malformed task.deleted payload: %w", rec.RecordID, err)
			}
			if _, ok := tasks[p.TaskID]; !ok {
				skipped = append(skipped, SkippedRecord{
					RecordID: rec.RecordID,
					TaskID:   p.TaskID,
					Reason:   "task.deleted applied to an unknown or already-tombstoned task",
				})
				continue
			}
			delete(tasks, p.TaskID)
			tombstoned[p.TaskID] = true

		default:
			return nil, fmt.Errorf("journalstore: replay: record %s: unsupported event_type %q", rec.RecordID, rec.EventType)
		}
	}

	result := &ReplayResult{
		Tasks:   make(map[string]ReplayedTask, len(tasks)),
		Skipped: skipped,
	}
	for id, t := range tasks {
		result.Tasks[id] = *t
	}
	return result, nil
}

// loadValidatedChainLocked reads every record file in s.recordsDir,
// validates the complete set, and returns records in predecessor-linked
// (oldest-to-newest) order. Callers must hold s.mu.
//
// Validation fails closed on the first violation found, identifying it in
// the returned error, rather than best-effort continuing:
//   - a malformed record file (invalid JSON, or a records/<id>.json file
//     whose contents' record_id does not match the file name);
//   - a duplicate record_id;
//   - an integrity-digest mismatch (recomputed canonicalDigest does not
//     equal the stored Integrity.Digest);
//   - a predecessor gap (predecessor_record_id names a record_id not
//     present in the set);
//   - a fork (two different records name the same predecessor_record_id,
//     or more than one record has a nil predecessor_record_id);
//   - a cycle (walking predecessor->child links revisits a record_id
//     before exhausting the set) or an unreachable/orphaned record (the
//     walk from the unique root does not reach every record).
//
// An empty records directory returns a nil slice and a nil error (a fresh,
// empty journal is valid).
func (s *Store) loadValidatedChainLocked() ([]journal.Record, error) {
	entries, err := os.ReadDir(s.recordsDir)
	if err != nil {
		return nil, fmt.Errorf("journalstore: list records dir: %w", err)
	}

	byID := make(map[string]journal.Record, len(entries))
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		path := filepath.Join(s.recordsDir, e.Name())
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("journalstore: read record file %s: %w", e.Name(), err)
		}
		var rec journal.Record
		if err := json.Unmarshal(data, &rec); err != nil {
			return nil, fmt.Errorf("journalstore: parse record file %s: %w", e.Name(), err)
		}

		if wantName := rec.RecordID + ".json"; e.Name() != wantName {
			return nil, fmt.Errorf("journalstore: record file %s does not match its record_id %q (corruption)", e.Name(), rec.RecordID)
		}
		if _, dup := byID[rec.RecordID]; dup {
			return nil, fmt.Errorf("journalstore: duplicate record_id %q", rec.RecordID)
		}

		gotDigest, err := canonicalDigest(rec)
		if err != nil {
			return nil, fmt.Errorf("journalstore: recompute integrity digest for record %s: %w", rec.RecordID, err)
		}
		if rec.Integrity.Digest == "" || gotDigest != rec.Integrity.Digest {
			return nil, fmt.Errorf("journalstore: record %s: integrity digest mismatch (corruption)", rec.RecordID)
		}

		byID[rec.RecordID] = rec
	}

	if len(byID) == 0 {
		return nil, nil
	}

	// childOf maps a predecessor's record_id to its single child's
	// record_id; forks (two children of one predecessor) are corruption in
	// this single-writer, single-generation store.
	childOf := make(map[string]string, len(byID))
	var rootID string
	rootCount := 0
	for id, rec := range byID {
		if rec.PredecessorRecordID == nil {
			rootCount++
			rootID = id
			continue
		}
		predID := *rec.PredecessorRecordID
		if _, exists := byID[predID]; !exists {
			return nil, fmt.Errorf("journalstore: record %s: predecessor %s not found (chain gap)", id, predID)
		}
		if existingChild, taken := childOf[predID]; taken {
			return nil, fmt.Errorf("journalstore: predecessor %s has two children (%s and %s): fork", predID, existingChild, id)
		}
		childOf[predID] = id
	}
	if rootCount == 0 {
		return nil, fmt.Errorf("journalstore: no record with a nil predecessor_record_id found (predecessor cycle)")
	}
	if rootCount > 1 {
		return nil, fmt.Errorf("journalstore: %d records have a nil predecessor_record_id (fork)", rootCount)
	}

	ordered := make([]journal.Record, 0, len(byID))
	seen := make(map[string]bool, len(byID))
	cur := rootID
	for {
		if seen[cur] {
			return nil, fmt.Errorf("journalstore: predecessor cycle detected at record %s", cur)
		}
		seen[cur] = true
		ordered = append(ordered, byID[cur])
		next, ok := childOf[cur]
		if !ok {
			break
		}
		cur = next
	}
	if len(ordered) != len(byID) {
		return nil, fmt.Errorf("journalstore: %d record(s) unreachable from the root record (fork or orphan)", len(byID)-len(ordered))
	}

	tail := ordered[len(ordered)-1]
	if s.tailRecordID != nil && *s.tailRecordID != tail.RecordID {
		return nil, fmt.Errorf("journalstore: reconstructed chain tail %s does not match head anchor tail %s (corruption)", tail.RecordID, *s.tailRecordID)
	}

	return ordered, nil
}
