# INDEX.md — manager

> Modo: incremental | Directorios indexados: 1
> Para regenerar: `idx generate --path ./manager`

---

## Scope

### `conversation/manager/` — Conversation manager coordinating persistence, context trimming strategies, and exports


---

## Árbol de Estructura

### `conversation/manager/` — Conversation manager coordinating persistence, context trimming strategies, and exports
The `Manager` type orchestrates storage, pluggable `ContextWindowStrategy` implementations, and conversation export via `Exporter`.
- `manager.go` — Core `Manager` type and coordination logic
- `context.go` — Context window strategy implementations (LRU, SlidingWindow, Priority)
- `export.go` — Conversation `Exporter` and `ExportOptions`

**Entry:** `manager.go:L1`

---

