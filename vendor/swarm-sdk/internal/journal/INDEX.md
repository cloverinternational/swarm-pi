# `swarm-sdk/internal/journal`

## Purpose

This package defines the canonical Journal v1 record schema for the swarm
attach execution journal: the envelope shape (`Record`, `Correlation`,
`Integrity`, `EventType`), the three in-scope task-lifecycle payload DTOs
(`TaskCreatedPayload`, `TaskFieldChangedPayload`, `TaskDeletedPayload`), the
closed allowlist of journaled task fields (`AllowedTaskFields`), and the
narrow `Writer` append interface that downstream producers/consumers depend
on instead of a concrete store implementation. It is a leaf package: it
imports only `swarm-sdk/internal/attachcontract` and the Go standard
library, and it is imported by `internal/journalstore`, `internal/taskstore`,
and `internal/agent`, never the other way around.

## Exported symbols

- `EventType` and its constants `EventTaskCreated`, `EventTaskFieldChanged`,
  `EventTaskDeleted`.
- `SchemaVersion` -- the current Journal v1 `attachcontract.Version` (`1.0`).
- `Integrity` -- typed digest/canonicalization metadata for one record.
- `Record` -- the canonical Journal v1 envelope (exact field set and JSON
  tags fixed by `CONTRACT.md`'s "Shared type seam" and ADR-006's "Canonical
  journal envelope and identity model").
- `Correlation` -- the optional correlation-identifier bundle passed to
  `NewRecord` and the `Writer` methods; empty string fields become `nil`
  (JSON `null`) on the resulting `Record`.
- `NewRecord(eventType, payload, daemonInstanceID, predecessorRecordID, corr) Record`
  -- mints a fresh envelope with a `crypto/rand`-backed 128-bit `record_id`,
  UTC `recorded_at`, and the current `SchemaVersion`. It leaves `Integrity`
  zero-valued; see its doc comment for why (the store computes the digest
  once the record's canonical bytes exist).
- `TaskCreatedPayload`, `TaskFieldChangedPayload`, `TaskDeletedPayload` --
  the three in-scope journal payload DTOs.
- `AllowedTaskFields` -- the closed allowlist of journaled task field names.
- `ValidateFieldName(name) error` -- structural allowlist-membership and
  non-empty-name validation only; it performs no redaction (that is
  `internal/journalredact`'s job, a separate package) and no correlation- or
  payload-shape validation beyond field-name membership.
- `Writer` -- the narrow append interface
  (`AppendTaskCreated`/`AppendTaskFieldChanged`/`AppendTaskDeleted`)
  implemented by `internal/journalstore.Store`. Callers depend on this
  interface type only, never on a concrete `journalstore` type.

## Scope

This package defines the record **schema** only. Per
`.swarmflow/swarm-attach-architecture/p05-til-execution-journal/CONTRACT.md`'s
"Scope decision" and "Deferred this phase" sections, it implements none of
the following, and callers/reviewers must not infer any of it from this
package's existence:

- the dual head-anchor slot protocol, lineage epoch/generation, or
  anchor-reseed repair;
- the privacy-barrier rebase state machine (quiesce -> purge-intent -> dual
  materialization -> dual anchor rotation -> source-epoch deletion);
- the full-snapshot artifact, sanitized-projection artifact, or commit
  manifest schemas;
- the shared/exclusive OS-backed reader-lease gate;
- durable storage, fsync/rename crash-safety, replay, or retention/
  compaction -- all of that lives in `internal/journalstore` (P05.C), which
  implements the `Writer` interface defined here;
- redaction of payload values -- that is `internal/journalredact` (P05.B);
  this package's `ValidateFieldName` only checks allowlist membership of a
  *field name*, never a field *value*.

This package alone is therefore not "ADR-006 compliant" in isolation; it is
one leaf dependency of the ADR-006-consistent subset assembled by
`internal/journalstore` and the P05.D wiring work. See CONTRACT.md for the
full worker split and the explicit list of what this phase defers relative
to ADR-006's full transactional design.
