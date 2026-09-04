# Persistence slice

Task state uses Pi session entries as an append-only journal. Each new
`pi-swarm-task-state` entry carries `schemaVersion`, a monotonic `revision`,
and `savedAt`; replay selects the highest revision rather than relying on
entry ordering. Legacy unversioned entries are accepted as schema v1 and
future versions are ignored (fail closed). `TaskManager.metrics()` exposes
mutation/read/failure counters and the last durable revision for lightweight
telemetry.

The payload contains task state only. Audit events remain opt-in in the
`get` DTO (`include_audit`) so routine RPC output stays compact and does not
leak internal history. Secrets and provider payloads are not persisted by
this slice; provider/cache telemetry remains hash/header-only in the Pi
extension.

Validation and replay are offline-testable and do not require credentials or
network access. Run `cd taskmanage && npm test && npm run build`.
