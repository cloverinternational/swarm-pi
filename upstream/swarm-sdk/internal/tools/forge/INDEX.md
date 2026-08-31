# INDEX.md — forge

> Modo: incremental | Directorios indexados: 2
> Para regenerar: `idx generate --path ./forge`

---

## Scope

### `tools/forge` — Package forge implements multi-language symbol indexing tools including call hierarchy, completion, and signature help.

---

## Árbol de Estructura

### `tools/forge` — Package forge implements multi-language symbol indexing tools including call hierarchy, completion, and signature help.
Exports `Complete`, `CallHierarchyItem`, `SnapshotContext`, and `IndexDaemonHandler` as core symbols for language-aware tooling.
- `tools.go` — main tool implementations and package entry point
- `go_symbol_index.go` — Go-specific symbol indexing
- `type_bootstrap.go` — type bootstrapping utilities
**Entry:** `tools.go:L1`

### `tools/forge/indexd/` — Client-server index daemon communicating over Unix domain sockets
Exports `Server` for running the daemon and client functions `Connect`, `Spawn`, `Query`, `IsAvailable` for interacting with it.
- `server.go` — Server type and daemon lifecycle (NewServer, Run)
- `client.go` — Client functions (Connect, Spawn, Query, IsAvailable)
- `protocol.go` — Request/Response types and SocketPath utility
**Entry:** `server.go:L1`

---

## Señales de Cambio Reciente

- `swarm-sdk/tools/forge/cache_evict_test.go`
- `swarm-sdk/tools/forge/daemon_handler.go`
- `swarm-sdk/tools/forge/go_symbol_index.go`
- `swarm-sdk/tools/forge/index_cache.go`
- `swarm-sdk/tools/forge/indexd/client.go`
- `swarm-sdk/tools/forge/indexd/protocol.go`
- `swarm-sdk/tools/forge/indexd/server.go`
- `swarm-sdk/tools/forge/indexd/sysattr_unix.go`
- `swarm-sdk/tools/forge/indexd/sysattr_windows.go`
- `swarm-sdk/tools/forge/semantic_grep.go`
