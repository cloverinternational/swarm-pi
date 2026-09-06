# INDEX.md — repomgr

> Modo: incremental | Directorios indexados: 1
> Para regenerar: `idx generate --path ./repomgr`

---

## Scope

### `workspace/repomgr/` — `Manager` manages the lifecycle of workspaces, including creation, listing, and deletion.

---

## Árbol de Estructura

### `workspace/repomgr/` — `Manager` manages the lifecycle of workspaces, including creation, listing, and deletion.
It also provides git repository cloning capabilities via `CloneRepo`.
- `manager.go` — Workspace lifecycle and state management
- `types.go` — Struct definitions for workspaces and parameters
- `git.go` — Git cloning utilities and logic
**Entry:** `manager.go:L1`

---

## Señales de Cambio Reciente

- `swarm-sdk/workspace/repomgr/git.go`
- `swarm-sdk/workspace/repomgr/git_test.go`
- `swarm-sdk/workspace/repomgr/manager.go`
- `swarm-sdk/workspace/repomgr/manager_test.go`
- `swarm-sdk/workspace/repomgr/types.go`
