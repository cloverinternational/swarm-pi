# Runtime contracts

Small dependency-free TypeScript contracts for Pi/Swarm runtime adapters. The package intentionally contains types and deterministic helpers only—hosts provide storage and execution.

- `ID` / `createId` / `newId`: validated stable identifiers.
- `RuntimeEvent` / `normalizeEvent`: ordered, timestamped normalized events.
- `Capability` and `PolicyContext`: explicit policy posture and host context.
- `Result`, `Outcome`, `RuntimeError`: typed terminal outcomes.
- `AuditRecord`, `AuditSink`, `ProfileStore`, `EventStore`, `RuntimePersistence`: persistence boundaries.
- `redact` / `redactUnknown`: non-mutating secret-key redaction for audit/explain output.

Build and test offline:

```sh
npm install
npm run build
npm test
```
