# INDEX.md — builtin

> Modo: incremental | Directorios indexados: 1
> Para regenerar: `idx generate --path ./builtin`

---

## Scope

### `hooks/builtin/` — Built-in hook implementations providing `AgentWaitSleepBlocker`, `AuditHook`, and `ComplianceAuditHook` for the swarm-sdk hooks system.

---

## Árbol de Estructura

### `hooks/builtin/` — Built-in hook implementations providing `AgentWaitSleepBlocker`, `AuditHook`, and `ComplianceAuditHook` for the swarm-sdk hooks system.
- `auto_mode.go` — `AgentWaitSleepBlocker` and auto-mode configuration hooks
- `logging.go` — `AuditHook` and `ComplianceAuditHook` for event auditing
- `recap.go` — Recap hook functionality
**Entry:** `auto_mode.go:L1`

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
- `swarm-sdk/hooks/builtin/nested_index_discovery.go`
- `swarm-sdk/hooks/builtin/nested_index_discovery_test.go`
- `swarm-sdk/hooks/builtin/task_enforcement.go`
- `swarm-sdk/hooks/builtin/plan_mode_first_tool.go`
- `swarm-sdk/hooks/builtin/session_start_hook.go`
- `swarm-sdk/hooks/builtin/dream_hook.go`
- `swarm-sdk/hooks/builtin/plan_mode_first_tool_test.go`
