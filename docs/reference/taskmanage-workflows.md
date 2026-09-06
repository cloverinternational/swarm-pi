# Workflow runtime

`WorkflowEngine` is an offline, host-agnostic executor for DAG-shaped workflows. The host supplies an `AgentRunner`; the engine provides:

- group dependencies and `parallel`, `sequential`, or `first` strategies;
- per-agent retries and group timeouts;
- attempt, token, and cost budgets;
- `fail_fast` or `continue` failure policy;
- cancellation via `AbortSignal`;
- checkpoint load/save for resume after interruption;
- routing metadata on agents (`route` is passed through to the host runner);
- distinct workflow/group/agent lifecycle events, including completed, failed, and cancelled workflow events.

This is intentionally a small runtime boundary. Provider credentials, model selection, and actual agent execution remain host responsibilities. Checkpoints contain result metadata and outputs supplied by the runner, so hosts should use an appropriate protected store when outputs are sensitive.

## Unattended execution boundary

Hosts must pass an explicit autonomy policy before invoking AgentRunner: workspace and tool allowlists, network and mutation posture, approval mode, credential scope, attempt/token/cost/time budgets, and recursion limits. Cancellation is cooperative through AbortSignal and must be persisted as terminal. Checkpoints are resume metadata, not a substitute for the authoritative Absurd/Postgres task record.

## Production control-plane integration

Pi-Swarm production goal/task/run actions are registered by `.pi/extensions/00-runtime/swarm-runtime.ts` and delegated to the daemon via `DaemonRpcClient`. TaskManage remains the local workflow/session coordinator; it must not substitute a file or fake control plane when daemon configuration is missing. `PI_SWARM_DAEMON_SOCKET` and `PI_SWARM_DAEMON_TOKEN` are required for authoritative actions.
