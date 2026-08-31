# INDEX.md — hooks

> Modo: incremental | Directorios indexados: 5
> Para regenerar: `idx generate --path ./hooks`

---

## Scope

### `hooks/` — Implements the event hook system with configuration loading via `ConfigHierarchy` and result aggregation via `HookAggregator`


---

## Árbol de Estructura

### `hooks/` — Implements the event hook system with configuration loading via `ConfigHierarchy` and result aggregation via `HookAggregator`
- `integration.go` — Integration bridge for TUI and SDK components
- `hook_events.go` — Hook event definitions and aggregation logic
- `filters.go` — Hook definition compilation and matching filters
**Entry:** `integration.go:L1`

### `hooks/agentbridge/` — Adapts a `*hooks.Manager` into the `agent.HooksManager` interface using the `Bridge` struct and `New` constructor
Runtime hook toggling is supported natively via the retained manager reference.
- `bridge.go` — Adapter implementation
**Entry:** `bridge.go:L1`

### `hooks/builtin/` — Built-in hook implementations providing `AgentWaitSleepBlocker`, `AuditHook`, and `ComplianceAuditHook` for the swarm-sdk hooks system.
- `auto_mode.go` — `AgentWaitSleepBlocker` and auto-mode configuration hooks
- `logging.go` — `AuditHook` and `ComplianceAuditHook` for event auditing
- `recap.go` — Recap hook functionality
**Entry:** `auto_mode.go:L1`

### `hooks/lifecycle/` — Executor runs lifecycle hooks with configurable hook matching and execution.
Provides `Executor` for executing hooks at various lifecycle stages (pre/post tool use, session start, stop, notification, messaging) and config utilities (`LoadConfig`, `ParseConfig`, `MergeConfigs`, `SaveConfig`).
- `executor.go` — `Executor` struct and lifecycle execution methods
- `config.go` — configuration loading, parsing, merging, and saving
- `types.go` — type definitions for hooks and decisions
**Entry:** `executor.go:L1`

### `hooks/loader/` — Config and loader functions for discovering and deserializing on-disk hook configurations into hooks.Hook instances.
Supports Claude Code `settings.json` and SwarmOS `hooks.json` formats, with precedence-based file lookup across project and user directories.
- `loader.go` — hook config discovery, deserialization, and conversion
**Entry:** `loader.go:L1`

---

## Índice de Patrones Comunes

| Tarea | Dónde ir primero |
|---|---|
| Agregar hook observabilidad | `hook/server.py` |

---

## Señales de Cambio Reciente

- `swarm-sdk/hooks/builtin/steering_stream_hook.go`
- `swarm-sdk/hooks/builtin/steering_pretool_hook.go`
- `swarm-sdk/hooks/builtin/steering_stream_hook_test.go`
- `swarm-sdk/hooks/loader/loader.go`
- `swarm-sdk/hooks/builtin/nested_index_discovery.go`
- `swarm-sdk/hooks/builtin/nested_index_discovery_test.go`
- `swarm-sdk/hooks/builtin/task_enforcement.go`
- `swarm-sdk/hooks/fuzz_test.go`
- `swarm-sdk/hooks/builtin/plan_mode_first_tool.go`
- `swarm-sdk/hooks/builtin/session_start_hook.go`
