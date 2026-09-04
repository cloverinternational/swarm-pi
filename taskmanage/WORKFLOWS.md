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
