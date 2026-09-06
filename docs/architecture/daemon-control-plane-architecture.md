# Daemon-backed general-agent control plane

Status: Phase 1 architecture and contract baseline. This document records the
boundary between a long-lived Swarm daemon and Pi agent sessions. It is a
design contract, not an implementation plan disguised as source code.

## 1. Findings and boundary decisions

The repository already has the right small pieces, but they are host-local:

- `schedule/src` is a deterministic scheduler with an injected `PromptSink`.
  It validates five-field cron, supports recurring/one-shot jobs, uses an
  atomic JSON store at `.swarm/scheduled_tasks.json`, serializes mutations, and
  delivers prompts asynchronously. Its `agentId` is routing metadata, not an
  agent process or durable execution lease.
- `.pi/extensions/schedule.ts` binds that scheduler to one Pi runtime through
  `sendUserMessage(..., { deliverAs: "followUp" })`. It starts on extension
  load and stops on `session_shutdown`; it must not become the daemon.
- `taskmanage` is a Pi journal adapter for the upstream task contract. It
  preserves stable IDs, dependencies, parent links, typed notes, audit history,
  ordered sequential/atomic batches, and one active/focused task. Its state is
  rehydrated from Pi session entries, so it is session-scoped unless a daemon
  supplies a separate persistence adapter.
- `runtime-contracts` already supplies the appropriate neutral vocabulary:
  stable IDs, normalized events, policy context/capabilities, typed outcomes,
  audit records, and persistence interfaces.
- `runtime-contracts/README.md`, `runtime-contracts`, `swarm-core`, and the
  existing modular Pi architecture explicitly keep provider credentials,
  execution, sandboxing, and durable host state outside the extension layer.
- The upstream Go scheduler has two delivery modes: enqueue a prompt into an
  interactive host, or create a background agent through an `AgentFactory` and
  background manager. It claims an occurrence before asynchronous delivery,
  but its in-tree schedule model is process-local, uses the process local
  timezone, and lacks cross-process ownership. The upstream harness schedule
  declarations therefore require explicit timezone, overlap, misfire, retry,
  concurrency, target, approval, and result-sink dimensions plus runtime
  bindings.

**Decision:** the daemon owns control-plane state, leases, dispatch, policy,
recovery, and event publication. Pi remains an execution surface and thin
adapter. A local extension may provide an in-process fallback, but it must not
pretend that a session timer is a daemon-backed job.

## 2. Runtime topology

```text
Clients (Pi extension, CLI, TUI, RPC/MCP bridge)
                         |
                 authenticated transport
                         |
              Swarm control-plane daemon
       +-----------------+------------------+
       |                 |                  |
  state journal      scheduler/leases   policy/audit
       |                 |                  |
       +---------- dispatcher -----------+
                         |
       Agent runtime adapters (Pi session, headless Pi, other harness)
                         |
                 provider/tool execution
```

The daemon is the sole writer of control-plane entities. Clients submit
commands and consume events; they do not edit daemon files or infer state from
rendered output. Agent runtimes report heartbeats, tool outcomes, and terminal
results back through the daemon protocol.

### Ownership

| Concern | Owner | Non-owner |
| --- | --- | --- |
| Agent/job/task identity and lifecycle | daemon | Pi extension/UI |
| Schedule calculation and occurrence claim | daemon scheduler | session timer |
| Cross-process single firing | daemon lease store | JSON file alone |
| Tool authorization and sandbox | host policy/executor | prompt or renderer |
| Provider credentials/model calls | agent runtime/host | daemon journal |
| Conversation transcript | Pi `SessionManager` or runtime | daemon cache |
| User presentation | Pi/TUI/client | daemon |
| Durable audit/event ordering | daemon event store | log text |

## 3. Control-plane entities

All IDs are opaque, stable strings and are unique within their entity kind.
Every mutable entity has `revision`, `created_at`, and `updated_at`.
Timestamps are RFC3339 UTC; schedules additionally carry an explicit IANA
`timezone`.

### Agent binding

```json
{
  "id": "agent-...",
  "kind": "pi-session | pi-headless | external",
  "workspace": "/absolute/workspace",
  "conversation_id": "optional-runtime-id",
  "capability_profile": "profile-id",
  "status": "starting | idle | running | waiting | stopped | failed",
  "last_heartbeat_at": "2026-01-01T00:00:00Z",
  "revision": 3
}
```

`conversation_id` is an exact binding, never “latest conversation”. A stale
binding is recoverable only after the adapter positively classifies the runtime
as missing and performs one serialized replacement/retry.

### Job and attempt

A **job** is a durable requested execution. An **attempt** is one leased run.
The daemon must not conflate schedule identity, job identity, agent identity,
or conversation identity.

```json
{
  "id": "job-...",
  "request": { "prompt": "...", "target": { "agent_id": "agent-..." } },
  "policy_profile": "profile-id",
  "state": "queued | leased | running | succeeded | failed | cancelled",
  "attempt": 2,
  "idempotency_key": "client-provided-or-derived",
  "created_at": "...",
  "updated_at": "..."
}
```

Prompts are sensitive payloads: they may be stored only in an access-controlled
job store and are excluded from ordinary diagnostics and event summaries.

### Schedule

```json
{
  "id": "schedule-...",
  "name": "daily-check",
  "schedule_type": "recurring | one_time",
  "cron": "0 9 * * 1-5",
  "timezone": "America/New_York",
  "target": { "kind": "agent | profile | workflow", "id": "..." },
  "overlap": "skip | queue | allowConcurrent | cancelPrevious",
  "misfire": "drop | runImmediately | backfillAll",
  "retry": {
    "kind": "none | fixedDelay | exponentialBackoff",
    "max_attempts": 0
  },
  "concurrency": { "kind": "unlimited | maxConcurrent", "max": 1 },
  "approval_posture": "deny | broker | autoApprove",
  "result_sink": { "kind": "discard | named", "id": "optional" },
  "enabled": true,
  "revision": 1
}
```

The schedule declaration is side-effect free until daemon preflight succeeds.
A target, timezone clock, single-owner lease, durable occurrence store,
selected overlap/misfire/retry/concurrency drivers, result sink, and unattended
approval resolver are explicit runtime requirements. Preflight fails before
execution when a required binding is absent.

### Event envelope

```json
{
  "id": "event-...",
  "sequence": 1042,
  "type": "job.created | job.started | job.progress | job.completed |
           job.failed | schedule.fired | agent.heartbeat",
  "entity_id": "job-...",
  "occurred_at": "...",
  "correlation_id": "...",
  "actor": "client-id | daemon | agent-id",
  "data": {},
  "redacted": true
}
```

Events are append-only, ordered by daemon sequence, replayable from a cursor,
and safe to duplicate at transport level. Consumers acknowledge a cursor; the
daemon may replay from the last acknowledged cursor. Event data contains
bounded previews and references, not secrets or full transcripts.

## 4. External API contract

The first transport may be Unix-domain socket plus JSON-RPC or HTTP, but the
wire semantics below are transport-neutral. Mutations require authentication,
workspace binding, authorization, and an idempotency key where a retry could
create or dispatch work.

### Commands

- `daemon.get_status` → daemon version, readiness, leader/lease state, store
  revision, and capability bindings.
- `agent.register`, `agent.heartbeat`, `agent.stop`.
- `job.create`, `job.get`, `job.cancel`, `job.retry`.
- `schedule.create`, `schedule.list`, `schedule.update`, `schedule.delete`,
  `schedule.run_now`.
- `task.batch` → the TaskManage `Params` shape, with sequential/atomic mode;
  the daemon returns the existing `Batch` shape and operation-key references.
- `events.subscribe` → cursor, entity filters, and bounded replay.

Every response has `{ request_id, ok, result?, error? }`. Errors are typed:
`invalid_request`, `unauthorized`, `forbidden`, `not_found`, `conflict`,
`lease_lost`, `preflight_failed`, `already_terminal`, `unavailable`, or
`internal`. Errors include a stable code and retryability; they do not expose
provider credentials, raw prompts, or stack traces.

### Lease and exactly-once boundary

The daemon provides **at-least-once dispatch with a durable idempotency key**,
not an impossible exactly-once model call. The sequence is:

1. transactionally claim one schedule occurrence and issue an attempt/lease;
2. persist `schedule.fired` and `job.created` before external dispatch;
3. dispatch to the agent adapter with the attempt id;
4. renew the lease while running;
5. accept completion only from the current lease/attempt;
6. on lease expiry, reconcile: retry, requeue, or mark failed according to the
   declared policy.

Agent adapters must make duplicate delivery observable and safe. A retried
adapter call with the same attempt id must return the existing result or a
conflict, never silently start a second conversation turn.

## 5. Pi adapter contract

The Pi extension is a client adapter, not a second scheduler:

- On `session_start`, connect/register with the daemon using workspace and exact
  session/conversation identity; fail visibly or enter explicitly labelled
  offline mode when unavailable.
- Register thin tools that call daemon commands. Tool registration is not
  authorization; the daemon/host policy gate remains authoritative.
- Convert daemon events into bounded custom messages/status updates. Do not
  inject arbitrary daemon events as user prompts.
- For a daemon-targeted Pi session, deliver only the claimed prompt through the
  supported `sendUserMessage`/follow-up API, attach the job and attempt IDs in
  non-model-facing metadata, and report terminal outcome back to the daemon.
- On `session_shutdown`, stop heartbeats and release the binding; do not delete
  durable jobs or schedules.
- Rehydrate extension-local caches from daemon snapshots/events after reload.
  Never retain a stale `SessionManager` or runtime context across replacement.

The existing `schedule.ts` can remain a compatibility adapter for session-only
schedules. Its current `scheduler.stop()` clears in-memory tasks, while durable
records remain on disk; that behavior must not be used as daemon lifecycle
semantics. A future `daemon_schedule` extension should delegate to the daemon
and expose explicit online/offline status.

## 6. TaskManage integration

TaskManage remains the model-facing task graph contract. The daemon adapter must:

1. preserve the existing `operations`, `mode`, `Result`, and `Batch` wire shapes;
2. map task IDs and operation-key references without renaming them;
3. enforce one active/focused task per control-plane scope, not globally across
   unrelated workspaces;
4. persist task state and operation events in the daemon journal;
5. make sequential mode commit each successful prefix and atomic mode commit
   all operations or roll back the complete batch;
6. publish task lifecycle events after the authoritative commit;
7. keep Pi journal rehydration as a local cache/compatibility path, never as a
   competing source of truth when daemon mode is enabled.

The daemon must not interpret task completion as agent/job completion. A task
can coordinate work; a job attempt is the execution record. Linking is explicit
(`task_id`, `job_id`, `agent_id`) and terminal transitions are independently
validated.

## 7. Persistence, recovery, and security invariants

- Use a transactional append/journal plus materialized indexes; never rely on
  a plain shared JSON file for multi-process ownership.
- Store ownership/lease records with expiry, fencing token, and daemon instance
  ID. A stale process cannot complete a newer attempt.
- Recover on startup by replaying the journal, validating schedules, expiring
  leases, and applying the declared misfire policy. Recovery must be
  deterministic and idempotent.
- Enforce workspace identity and capability policy before dispatch. Pi prompts,
  tool schemas, and renderers are not a sandbox.
- Redact secret-shaped values from audit/event output. Keep full outputs behind
  an access-controlled result store and return bounded previews.
- Bind local transport permissions to the workspace/user. Remote transport
  requires authenticated identity, authorization, and replay protection.
- Bound prompt, output, event, and replay sizes; cancellation must propagate to
  the adapter and settle the attempt exactly once.

## 8. Verification gates for the next phase

The implementation phase is ready only after these focused tests exist:

- two daemon instances sharing a store cannot fire one occurrence twice;
- restart during claim, dispatch, heartbeat, and completion recovers according
  to lease and misfire policy;
- explicit timezone produces the same next occurrence independent of process
  local timezone;
- overlap, retry, concurrency, approval, and result-sink declarations fail
  preflight when their bindings are missing;
- duplicate `job.create` with one idempotency key returns one job;
- stale agent/conversation binding recovers only on a classified missing-runtime
  error and concurrent messages serialize to one replacement;
- TaskManage sequential/atomic behavior and operation-key references match the
  current adapter contract;
- Pi reload/shutdown does not duplicate tools, heartbeats, schedules, or event
  subscriptions;
- event replay resumes from a cursor without exposing unbounded or sensitive
  payloads.

## 9. Phase 2 status and next step

The daemon-neutral `ControlPlane` contract and `InProcessControlPlane` fake now
live in `runtime-contracts`. The fake provides agent registration/heartbeat/
stop, idempotent job creation, job lookup/cancel/retry, defensive copies, and
ordered event cursors for adapter contract tests. Background agents also accept
an injected completion callback/event sink, allowing the parent Pi adapter to
receive terminal results without polling `AgentControl`.

Implemented the focused durable slice: `FileControlPlane` provides a versioned,
atomic file-backed repository with cross-process lock-directory serialization,
restart persistence, idempotent jobs, leases, fencing tokens, completion, and
expired-lease recovery. `LocalControlPlaneServer` provides authenticated,
permissioned Unix-socket JSON-RPC with bounded line requests and typed errors,
including `daemon.get_status`. `control-panel.ts` is a read-only Pi adapter for
bounded agent/job status and intentionally excludes prompts. Production durable
workflow execution remains Absurd/Postgres; the file store is local fallback and
test infrastructure, never SQLite production storage. Focused tests cover
restart/idempotency/lease recovery, authenticated RPC, redaction, and panel
redaction.

## References audited

- `schedule/src/{cron,index,scheduler,store,tools,types}.ts`
- `.pi/extensions/schedule.ts`
- `docs/reference/taskmanage-reference-contract.md`,
  `docs/reference/taskmanage-workflows.md`, and `taskmanage/src/task-manage.ts`
- `runtime-contracts/README.md`
- `docs/architecture/modular-pi-architecture.md`,
  `docs/reference/pi-missing-tools-and-ecosystem.md`
- `upstream/swarm-sdk/internal/tools/builtin/{cron_scheduler,schedule_wakeup}.go`
- `upstream/swarm-sdk/harness/schedules.go`
- upstream Pi extension/session references under
  `upstream/pi-mono/packages/coding-agent`
- upstream scheduler/tool contract discovery: Swarm daemon REST/MCP patterns
  and typed schedule/task lifecycle APIs.

## Operator and troubleshooting notes

Production workers use Absurd/Postgres, not the local file fallback. Confirm Postgres connectivity, schema, queue name, worker identity, and policy preflight before dispatch. If jobs remain queued, inspect target routing and approval posture; if a lease repeatedly expires, inspect heartbeats, clock/connection health, and duplicate worker IDs. If events appear missing, resume from the last acknowledged cursor; delivery is at-least-once and consumers must deduplicate by event ID/sequence. Cancellation must be issued through the daemon and confirmed by terminal state. /goal and /loop are durable commands, not local timers; the control panel only renders bounded, redacted state.
