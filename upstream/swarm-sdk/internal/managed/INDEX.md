# INDEX.md — managed

> Modo: incremental | Directorios indexados: 2
> Para regenerar: `idx generate --path ./managed`

---

## Scope

### `managed/` — The `managed` package provides the `ManagedBackgroundAgent` for remote asynchronous execution of agents.

---

## Árbol de Estructura

### `managed/` — The `managed` package provides the `ManagedBackgroundAgent` for remote asynchronous execution of agents.
It defines the `AgentDefinition` interface to avoid import cycles and exposes configuration via `ManagedExecutionConfig` and `ManagedBackgroundAgentConfig`.
- `background_bridge.go` — Core agent definition and bridge interface
- `client.go` — HTTP client for remote managed execution
- `provider.go` — Provider configuration and integration
**Entry:** `background_bridge.go:L1`

### `managed/mockserver/` — Provides `MockServer` to simulate the Swarm Cloud API for testing managed agents locally
Includes types for sessions, projects, messages, and API request/response payloads.
- `server.go` — Implements `MockServer` and associated mock data types
**Entry:** `server.go:L1`

---

