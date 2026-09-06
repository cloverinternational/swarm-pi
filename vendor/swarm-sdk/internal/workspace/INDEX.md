# INDEX.md — workspace

> Modo: incremental | Directorios indexados: 2
> Para regenerar: `idx generate --path ./workspace`

---

## Scope

### `workspace/` — `FilesystemWorkspace` implements a sandboxed local filesystem workspace for agent execution contexts.

---

## Árbol de Estructura

### `workspace/` — `FilesystemWorkspace` implements a sandboxed local filesystem workspace for agent execution contexts.
`FilesystemConfig` configures the workspace, and methods such as `NewFilesystemWorkspace`, `Resolve`, `Read`, `Write`, `Delete`, `Execute`, `Watch`, and `Close` provide isolated file operations with permission controls.
- `filesystem.go` — core `FilesystemWorkspace` implementation
- `interface.go` — `Workspace` interface definition
- `hosted_snapshot.go` — hosted snapshot support
**Entry:** `filesystem.go:L1`

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
