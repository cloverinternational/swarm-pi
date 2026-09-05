# Runtime contracts

Small dependency-free TypeScript contracts for Pi/Swarm runtime adapters. The package intentionally contains types and deterministic helpers only—hosts provide storage and execution.

- `ID` / `createId` / `newId`: validated stable identifiers.
- `RuntimeEvent` / `normalizeEvent`: ordered, timestamped normalized events.
- `Capability` and `PolicyContext`: explicit policy posture and host context.
- `Result`, `Outcome`, `RuntimeError`: typed terminal outcomes.
- `AuditRecord`, `AuditSink`, `ProfileStore`, `EventStore`, `RuntimePersistence`: persistence boundaries.
- `redact` / `redactUnknown`: non-mutating secret-key redaction for audit/explain output.
- `GoalLoopControlPlane` and `registerGoalLoop`: typed `/goal` and `/loop` adapters. Creates require reviewed Done-when criteria and remain queued; only `resume` explicitly starts. Loop creation carries cadence and bounded continuation/budget/no-progress fields to the authoritative durable control plane.

Build and test offline:

```sh
npm install
npm run build
npm test
```

Slash command forms are intentionally conservative: `/goal create {"description":"…","doneWhen":"…","doneWhenReviewed":true}`, then `/goal status|pause|resume|complete <id>`; `/loop create {"prompt":"…","cadence":"…","continuation":{"maxIterations":5}}`, then `/loop status|pause|resume|stop <id>`. Model tools mirror these operations and never persist locally or activate silently.

## Pi production wiring

`.pi/extensions/swarm-runtime.ts` registers daemon-backed goal, loop, task, and run tools plus `/goal` and `/loop`. Configure `PI_SWARM_DAEMON_SOCKET` and `PI_SWARM_DAEMON_TOKEN` (or pass explicit options). If configuration is absent, it exposes an explicit unavailable adapter and `daemon_status`; it never falls back to fake or file control planes. Connections are opened only when used and closed at session shutdown.
