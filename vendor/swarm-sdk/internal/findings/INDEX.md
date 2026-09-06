# INDEX.md — findings

> Modo: incremental | Directorios indexados: 1
> Para regenerar: `idx generate --path ./findings`

---

## Scope

### `findings/` — FileCache-based storage and indexing for findings with CRUD operations and debounced persistence


---

## Árbol de Estructura

### `findings/` — FileCache-based storage and indexing for findings with CRUD operations and debounced persistence
Provides a FileCache for persisting findings to disk with coalesced writes, along with query, list, and cleanup capabilities.
- `semantic_index.go` — debounced index persistence and file-based storage
- `types.go` — FileCache struct and core type definitions
- `sync.go` — concurrency helpers for cache operations
**Entry:** `semantic_index.go:L1`

---

## Señales de Cambio Reciente

- `swarm-sdk/findings/semantic_index.go`
