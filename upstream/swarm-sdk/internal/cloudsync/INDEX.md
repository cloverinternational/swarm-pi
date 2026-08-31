# INDEX.md — cloudsync

> Modo: incremental | Directorios indexados: 1
> Para regenerar: `idx generate --path ./cloudsync`

---

## Scope

### `cloudsync/` — Cloud sync client and configuration manager for settings and profiles


---

## Árbol de Estructura

### `cloudsync/` — Cloud sync client and configuration manager for settings and profiles
Provides `APIClient` for HTTP-based sync operations on settings, profiles, and conversations, along with `ConfigSyncManager` which wraps a `ConfigManager` to trigger syncs on config changes.
- `client.go` — `APIClient` and HTTP operations for settings, profiles, conversations
- `config_wrapper.go` — `ConfigSyncManager` wrapping `ConfigManager` with sync triggers
- `manager.go` — Sync manager orchestration
**Entry:** `client.go:L1`

---

## Señales de Cambio Reciente

- `swarm-sdk/cloudsync/allowlist.go`
- `swarm-sdk/cloudsync/canonical.go`
- `swarm-sdk/cloudsync/client.go`
- `swarm-sdk/cloudsync/client_test.go`
- `swarm-sdk/cloudsync/config_wrapper.go`
- `swarm-sdk/cloudsync/conflict.go`
- `swarm-sdk/cloudsync/conflict_test.go`
- `swarm-sdk/cloudsync/conversation_storage.go`
- `swarm-sdk/cloudsync/encryption.go`
- `swarm-sdk/cloudsync/integration_test.go`
