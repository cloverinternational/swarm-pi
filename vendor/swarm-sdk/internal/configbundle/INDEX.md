# INDEX.md — configbundle

> Modo: incremental | Directorios indexados: 1
> Para regenerar: `idx generate --path ./configbundle`

---

## Scope

### `configbundle/` — Unified configuration system centered on the `ConfigBundle` struct


---

## Árbol de Estructura

### `configbundle/` — Unified configuration system centered on the `ConfigBundle` struct
Consolidates system, prompts, tools, agents, hooks, skills, profiles, providers, credentials, and MCP servers into a single hierarchical config (global + project), with file-watching detection via `Detector`.
- `types.go` — Defines `ConfigBundle` struct and schema version
- `detector.go` — `Detector` for discovering and watching project config files
- `manager.go` — Configuration management logic
**Entry:** `types.go:L1`

---

## Señales de Cambio Reciente

- `swarm-sdk/configbundle/types.go`
