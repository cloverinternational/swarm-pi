# INDEX.md — conversation

> Modo: incremental | Directorios indexados: 4
> Para regenerar: `idx generate --path ./conversation`

---

## Scope

### `conversation/` — Defines the `Conversation` type and supporting structures for managing conversation state, metadata, and app modes.

---

## Árbol de Estructura

### `conversation/` — Defines the `Conversation` type and supporting structures for managing conversation state, metadata, and app modes.
Includes summary management, agent memory, group/mode state, and app mode tagging utilities.
- `conversation.go` — Core `Conversation` type and status constants
- `appmode.go` — App mode tag parsing and validation functions
- `message.go` — Message and metadata types
**Entry:** `conversation.go:L1`

### `conversation/manager/` — Conversation manager coordinating persistence, context trimming strategies, and exports
The `Manager` type orchestrates storage, pluggable `ContextWindowStrategy` implementations, and conversation export via `Exporter`.
- `manager.go` — Core `Manager` type and coordination logic
- `context.go` — Context window strategy implementations (LRU, SlidingWindow, Priority)
- `export.go` — Conversation `Exporter` and `ExportOptions`

**Entry:** `manager.go:L1`

### `conversation/repair/` — Provides the `RepairHistory` function and `RepairReport` struct to fix structural invariant violations in conversation message slices.
It handles orphaned leading messages and orphaned tool calls as pure functions without side-effects.
- `repair.go` — Core repair logic and definitions
**Entry:** `repair.go:L1`

### `conversation/storage/` — Provides `AsyncStorage` for decoupled, asynchronous storage operations and `RenderCache` for caching rendered conversations.
- `async_storage.go` — Defines `AsyncStorage` and async save/load/delete operations
- `dir_storage.go` — Directory-based storage backend implementation
- `file.go` — File utility helpers for storage operations
**Entry:** `async_storage.go:L1`

---

## Señales de Cambio Reciente

- `swarm-sdk/conversation/fuzz_test.go`
- `swarm-sdk/conversation/message.go`
- `swarm-sdk/conversation/preview.go`
- `swarm-sdk/conversation/repair/repair.go`
- `swarm-sdk/conversation/repair/repair_test.go`
- `swarm-sdk/conversation/conversation.go`
- `swarm-sdk/conversation/message_promptid_test.go`
- `swarm-sdk/conversation/storage/dir_storage.go`
- `swarm-sdk/conversation/storage/memory.go`
- `swarm-sdk/conversation/storage/pooled_loader.go`
