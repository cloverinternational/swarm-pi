# INDEX.md — taskstore

> Modo: incremental | Directorios indexados: 1
> Para regenerar: `idx generate --path ./taskstore`

---

## Scope

### `internal/taskstore/` — Persistent task storage via the `Store` struct with CRUD operations and session-tree tracking


---

## Árbol de Estructura

### `internal/taskstore/` — Persistent task storage via the `Store` struct with CRUD operations and session-tree tracking
Tasks are persisted to `task.json` in the conversation metadata directory, surviving compaction and sharing state across branches.
- `store.go` — Core `Store` type with `New`, `Load`, `Save`, and task query/mutation methods
- `tree.go` — Session-tree fields and tree structure helpers for `Task`
- `sync_adapter.go` — `Sync`, `FromTodoItems`, and `ToTodoItems` conversion utilities
**Entry:** `store.go:L1`

---

## Señales de Cambio Reciente

- `swarm-sdk/internal/taskstore/store.go`
- `swarm-sdk/internal/taskstore/tree.go`
- `swarm-sdk/internal/taskstore/tree_test.go`
