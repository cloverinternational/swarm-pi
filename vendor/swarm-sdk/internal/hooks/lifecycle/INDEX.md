# INDEX.md — lifecycle

> Modo: incremental | Directorios indexados: 1
> Para regenerar: `idx generate --path ./lifecycle`

---

## Scope

### `hooks/lifecycle/` — Executor runs lifecycle hooks with configurable hook matching and execution.

---

## Árbol de Estructura

### `hooks/lifecycle/` — Executor runs lifecycle hooks with configurable hook matching and execution.
Provides `Executor` for executing hooks at various lifecycle stages (pre/post tool use, session start, stop, notification, messaging) and config utilities (`LoadConfig`, `ParseConfig`, `MergeConfigs`, `SaveConfig`).
- `executor.go` — `Executor` struct and lifecycle execution methods
- `config.go` — configuration loading, parsing, merging, and saving
- `types.go` — type definitions for hooks and decisions
**Entry:** `executor.go:L1`

---

## Índice de Patrones Comunes

| Tarea | Dónde ir primero |
|---|---|
| Agregar hook observabilidad | `hook/server.py` |

---

