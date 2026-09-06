# INDEX.md — storage

> Modo: incremental | Directorios indexados: 1
> Para regenerar: `idx generate --path ./storage`

---

## Scope

### `conversation/storage/` — Provides `AsyncStorage` for decoupled, asynchronous storage operations and `RenderCache` for caching rendered conversations.

---

## Árbol de Estructura

### `conversation/storage/` — Provides `AsyncStorage` for decoupled, asynchronous storage operations and `RenderCache` for caching rendered conversations.
- `async_storage.go` — Defines `AsyncStorage` and async save/load/delete operations
- `dir_storage.go` — Directory-based storage backend implementation
- `file.go` — File utility helpers for storage operations
**Entry:** `async_storage.go:L1`

---

## Señales de Cambio Reciente

- `swarm-sdk/conversation/storage/dir_storage.go`
- `swarm-sdk/conversation/storage/memory.go`
- `swarm-sdk/conversation/storage/pooled_loader.go`
