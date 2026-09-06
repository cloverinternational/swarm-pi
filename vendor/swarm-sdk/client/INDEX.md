# INDEX.md — client

> Modo: incremental | Directorios indexados: 1
> Para regenerar: `idx generate --path ./client`

---

## Scope

### `client/` — Client type providing an ergonomic top-level interface to the Swarm SDK


---

## Árbol de Estructura

### `client/` — Client type providing an ergonomic top-level interface to the Swarm SDK
The Client type wraps access to agents, logging, tracing, storage, and conversation management, initialized via the New constructor and configurable through Option functions such as WithProvider, WithAPIKey, and WithWorkspace.
- `client.go` — Core Client type and New constructor
- `crud.go` — CRUD operations
- `events.go` — Event handling
**Entry:** `client.go:L1`

---

## Señales de Cambio Reciente

- `swarm-sdk/client/client.go`
- `swarm-sdk/client/custom_providers.go`
- `swarm-sdk/client/provider_type.go`
- `swarm-sdk/client/events.go`
- `swarm-sdk/client/source.go`
- `swarm-sdk/client/source_probe_test.go`
- `swarm-sdk/client/probe_fire_test.go`
- `swarm-sdk/client/.index-state.json`
- `swarm-sdk/client/INDEX.md`
- `swarm-sdk/client/workspace_context.go`
