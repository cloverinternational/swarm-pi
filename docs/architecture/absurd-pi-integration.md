# Absurd + Pi integration contract

Verified 2026-09-05 against npm and the checked-in `vendor/pi-mono` snapshot.

## Absurd SDK

`absurd-sdk@0.5.0` is installable from npm (`node >=18`, peer dependency
`pg ^8`). It is a Postgres-native durable task client, not a SQLite backend:
`new Absurd({ db, queueName })`, `registerTask({ name }, handler)`,
`spawn(name, jsonParams, options)`, `startWorker()`, `fetchTaskResult()`,
and `close()`. `TaskContext.step(name, fn)` is the durable idempotent
checkpoint; `beginStep`/`completeStep`, `awaitEvent`, `awaitTaskResult`, and
`heartbeat` are also available. The first general-agent task must therefore
use JSON-safe parameters/results and explicit step names. Production setup
requires Absurd's Postgres schema and queue; FileControlPlane remains only a
local test/fallback.

## Pi Agent SDK

The checked-in Pi packages are `@earendil-works/pi-agent-core@0.78.1` and
`@earendil-works/pi-coding-agent@0.78.1`. Pi's `Agent` owns the live transcript,
tools, provider loop, and lifecycle. `session.subscribe()` receives typed
`message_end`, `tool_execution_*`, `turn_end`, and `agent_end` events.

`message_end` is the checkpoint boundary: the AgentHarness persists the
finalized message before notifying subscribers, and pending extension/session
writes flush after messages at save points. A durable adapter should checkpoint
only after this event, with the message role/id and monotonic adapter sequence;
it must not treat `message_update` as durable completion. Do not mutate the
persisted message from a subscriber unless preserving its role.

`runAgentLoopContinue(context, config, emit, signal, streamFn)` is the low-level
retry/continuation primitive. It requires a non-empty context whose last message
is **not** an assistant message; the last message must convert to a provider
`user` or `toolResult`. It emits `agent_start` and `turn_start`, then normal
message/tool/turn events. Use it only for recovery when the durable Pi context
already ends at a resumable boundary; otherwise start a new prompt with
`runAgentLoop`. Never infer continuation from a missing/unknown transcript.

See `runtime-contracts/src/general-agent.ts` for the narrow typed task,
checkpoint, runtime, status, and tool request/response contracts. The minimal
worker is `agents/src/worker-daemon.ts`: it registers only the general-agent
task, starts Absurd with concurrency one, reports health, and closes its worker
and client on shutdown. Its runtime factory is injectable for offline tests.
The transport-neutral control/task tool contract is in
`runtime-contracts/src/control-task.ts`; `.pi/extensions/control-task-tools.ts`
adapts goal/task/run create/get/status/cancel to an injected authoritative
client. It intentionally does not implement `/goal` or `/loop`, and does not
introduce another store.

## Goal and loop controls

`runtime-contracts` exposes conservative `/goal` and `/loop` adapters through
`registerGoalLoop`. They delegate mutations to the supplied durable
`GoalLoopControlPlane`; adapters do not introduce a store. Goal creation
requires reviewed `doneWhen` criteria and remains queued. Loop creation carries
cadence and optional bounded continuation (`maxIterations`, `maxTokens`,
`maxCost`, `maxNoProgress`) and remains queued. Only `resume` explicitly starts
work; pause, completion, and stop are explicit lifecycle operations.

Examples: `/goal create {"description":"Ship the slice","doneWhen":"build and
focused tests pass","doneWhenReviewed":true}`, then `/goal
status|pause|resume|complete <goal-id>`. For loops use `/loop create
{"prompt":"Re-check the slice","cadence":"15m","continuation":{"maxIterations":3}}`,
then `/loop status|pause|resume|stop <loop-id>`. The same operations are
model-callable as `goal_*` and `loop_*` tools with closed JSON schemas.

## Unattended autonomy policy

Every unattended Absurd/Pi worker must receive an explicit reviewed policy: absolute workspace boundary; closed tool allowlist; network disabled unless hosts are allowlisted; mutations denied unless approval permits them; credentials limited to named environment variables; bounded attempts, tokens, cost, timeout, and output; and recursion depth/child-count limits. Missing fields fail closed. Enforcement belongs to the host/executor, not prompts, tool registration, or the control panel.

The durable production path is Absurd backed by Postgres. In-process and file implementations are test/fallback adapters only; no SQLite production storage is added. Recovery tests use fake/in-process or temporary file state. Optional live integration validation can be enabled with PI_SWARM_POSTGRES_URL and npm run test:integration; it is not run or claimed by default.

## Operator quick start

Configure Postgres and the Absurd schema/queue, set ABSURD_QUEUE, construct a reviewed autonomy policy, then start the daemon/worker with one stable worker identity. Use /goal for reviewed done-when work; it stays queued until /goal resume. Use /loop for bounded cadence and continuation; pause/stop are explicit. The control panel is read-only status and never an approval or mutation authority.

## Production path
Pi tools use DaemonRpcClient over authenticated Unix-socket JSON-RPC. The daemon owns authorization, idempotency, ownership, and lifecycle; production execution is AbsurdControlPlane plus Postgres and createWorkerDaemon. No FileControlPlane or in-process fallback is wired in production. DaemonUnavailableError is surfaced visibly. Absurd task headers carry job and owner-session identity; TaskContext.step checkpoints message/turn boundaries for retry resume.
