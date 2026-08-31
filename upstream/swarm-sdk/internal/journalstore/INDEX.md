# `swarm-sdk/internal/journalstore`

## Purpose

`journalstore` is the durable, single-writer, single-generation
append-only storage engine for Journal v1 records defined by
`swarm-sdk/internal/journal` (record/payload/`Writer` types) and sanitized
by `swarm-sdk/internal/journalredact` before this package ever writes a
byte to disk. It is worker P05.C's package in the Phase 05 "TIL execution
journal" contract
(`.swarmflow/swarm-attach-architecture/p05-til-execution-journal/CONTRACT.md`)
implementing an ADR-006-consistent SUBSET of
`docs/architecture/swarm-attach/adr-006-execution-journal-privacy.md`'s
full transaction protocol -- see "Scope vs ADR-006" below for exactly
which parts.

This package owns:

1. Opening/creating one journal directory and resolving its current tail
   (`Open`, `Close`).
2. Appending `task.created` / `task.field_changed` / `task.deleted`
   records with synchronous redaction and crash-safe commit
   (`AppendTaskCreated`, `AppendTaskFieldChanged`, `AppendTaskDeleted` --
   together implementing `journal.Writer`, asserted at compile time via
   `var _ journal.Writer = (*Store)(nil)` in `store.go`).
3. Fail-closed structural validation plus semantic reconstruction of
   sanitized, attach-visible per-task state from the committed record set
   (`Replay`).
4. Time-based retention deletion via synchronous, writer-locked journal
   rewrite (`Compact`).

`taskstore.Store`/`task.json` remain the authoritative full task state
this phase; this package is an additive, redacted, best-effort-durable
side record, never a replacement. Wiring call sites (P05.D) depend only on
the narrow `journal.Writer` interface, never on this package's concrete
`*Store` type or its filesystem/fsync internals.

## Exported symbols

| Symbol | Kind | Summary |
| --- | --- | --- |
| `Open(dir, daemonInstanceID string) (*Store, error)` | func | Opens or creates the journal directory, resolving the current head anchor (or initializing an empty journal). |
| `(*Store) Close() error` | method | Marks the store closed; further append/compact calls return `ErrStoreClosed`. |
| `(*Store) Degraded() bool` | method | Reports whether the most recent append/compaction failed partway through a durability step, for gap/decision-record telemetry. |
| `(*Store) AppendTaskCreated(ctx, corr journal.Correlation, payload journal.TaskCreatedPayload) error` | method | Implements `journal.Writer`; redacts fields, stamps integrity, crash-safely commits. |
| `(*Store) AppendTaskFieldChanged(ctx, corr journal.Correlation, payload journal.TaskFieldChangedPayload) error` | method | Implements `journal.Writer`; same discipline, one field per record. |
| `(*Store) AppendTaskDeleted(ctx, corr journal.Correlation, payload journal.TaskDeletedPayload) error` | method | Implements `journal.Writer`; tombstone record. |
| `(*Store) Replay() (*ReplayResult, error)` | method | Fail-closed structural validation, then reconstructs sanitized per-task state. |
| `ReplayResult` | type | `Tasks map[string]ReplayedTask` plus `Skipped []SkippedRecord`. |
| `ReplayedTask` | type | `TaskID string`, `Fields map[string]string`. |
| `SkippedRecord` | type | `RecordID`, `TaskID`, `Reason` -- a structurally valid record that could not be semantically applied (unknown/tombstoned task target). |
| `(*Store) Compact(now time.Time) (removed int, err error)` | method | Applies timed retention by rewriting the journal under the writer lock; `now` is caller-supplied (no internal wall-clock read), so callers control eligibility and can implement ADR-006's clock-freeze safeguards outside this package. |
| `ErrJournalDegraded` | var (sentinel error) | Wraps any error from a failed write/fsync/rename step; `errors.Is`-compatible. |
| `ErrUnknownField` | var (sentinel error) | A journaled field name outside `journal.AllowedTaskFields`. |
| `ErrStoreClosed` | var (sentinel error) | Returned by append/compact calls made after `Close`. |

`Store` itself is exported only as an opaque type (construct via `Open`);
its fields are unexported. See `store.go`'s package doc comment for the
single-writer, single-`Store`-instance-per-directory precondition this
package requires but does not itself enforce with an OS-backed lock.

## On-disk layout

```
<dir>/
  anchor.json          # single head anchor; sole visibility point; replaced-in-place
  records/
    <record_id>.json   # one immutable (except during Compact relinking) record per file
  staging/
    rec-*.tmp           # transaction-unique staged record files
    anchor-*.tmp         # transaction-unique staged anchor files
```

Every write follows: create staged temp file -> write -> `fsync` -> close
-> atomic rename to final path -> `fsync` containing directory. Record
commits (outside `Compact`) use no-replace semantics (`os.Link` then
`os.Remove` of the now-redundant staging copy; `os.Link` fails with
`EEXIST` if the destination already exists, so a record file, once
committed by a fresh append, is never silently clobbered by another
append). The anchor commit intentionally uses replacing `os.Rename`
semantics -- it is the one artifact whose whole purpose is to be
atomically swapped to a new value on every commit.

## Scope vs ADR-006

ADR-006 specifies a full crash-safe transactional storage engine (dual-slot
head-anchor epoch/generation lineage, anchor-reseed repair, a five-phase
privacy-barrier rebase state machine, a shared/exclusive OS reader-lease
gate, and a 17-item fault-injection verification matrix). Per
CONTRACT.md's "Scope decision," that full system is out of scope for this
phase's disjoint-file-ownership worker model. This package implements an
ADR-006-CONSISTENT SUBSET:

**Implemented this phase:**

- A single-writer, single-generation durable journal: one head-anchor
  file (`anchor.json`), not the dual-slot `current-manifest.0`/`.1`
  epoch/generation lineage.
- The write -> `fsync` -> close -> atomic rename -> directory `fsync`
  crash-safety PRIMITIVE for every artifact (records and the anchor),
  without the rollback-lineage/dual-anchor apparatus built on top of it in
  ADR-006.
- Fail-closed replay validation: duplicate record IDs, predecessor-chain
  gaps/cycles/forks, and integrity-digest mismatches are all detected and
  reported before any record is semantically applied (`Replay`,
  `loadValidatedChainLocked`).
- Timed retention deletion (30-day terminal-task default, 7-day
  instance-only default, unlimited while a task is non-terminal) via
  synchronous compaction under the single writer lock (`Compact`).

**Deferred this phase** (copied verbatim from CONTRACT.md's "Deferred this
phase" list, so a future reader does not need to cross-reference that
file):

- Dual head-anchor slot protocol (`current-manifest.0`/`.1`) with lineage
  epoch/generation and base-pair revalidation.
- Anchor-reseed repair sub-protocol for a single-surviving-slot state.
- Privacy-barrier rebase state machine (quiesce -> purge-intent -> dual
  materialization -> dual anchor rotation -> source-epoch deletion).
  Because this phase has only one anchor/generation, retention deletion
  here is NOT leak-proof against a hand-restored backup of a deleted
  artifact the way ADR-006's full rebase would be -- this is a known
  limitation relative to ADR-006's privacy guarantees. `Compact`'s doc
  comment repeats this warning at the call site.
- Full-snapshot artifact as the authoritative successor to `task.json`.
  `internal/taskstore.Store`/`task.json` remain exactly as they are today
  as the authoritative full state; the journal is an additive, redacted,
  best-effort-durable side record this phase, not a replacement.
- Sanitized-projection artifact as a persisted checkpoint schema distinct
  from replay; this phase replays from `task.created` forward instead
  (`Replay` always walks the full committed chain; there is no
  `task.checkpoint` event type or checkpoint artifact this phase).
- Shared/exclusive OS-backed reader-lease gate across all consumer
  classes. This package's `sync.Mutex` only serializes calls made through
  one `*Store` value in one process; it is not a cross-process or
  cross-consumer-class gate.
- Journal-activation one-way migration of existing `task.json` into
  epoch-zero generations.
- Fault-injection matrix items covering rollback/mirror non-authority,
  anchor forks/fencing, privacy-barrier crash/resurrection, and the
  activation boundary. The write/fsync/rename crash-safety primitive
  itself (no partial visibility of one record) IS in scope and IS tested
  (see `store_test.go`).

## Precondition not enforced by this package

Exactly one `*Store` may be open over a given journal directory at a time,
in one process, with no other process concurrently writing to that
directory. This package provides only in-process `sync.Mutex`
serialization; it does NOT take an OS-backed exclusive lock (unlike
ADR-006's fenced writer lock with process-start identity and a random
fencing token). A second concurrent `Store` instance over the same
directory is out of scope and will corrupt journal state.
