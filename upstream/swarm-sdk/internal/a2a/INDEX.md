# INDEX.md — a2a

> Modo: incremental | Directorios indexados: 3
> Para regenerar: `idx generate --path ./a2a`

---

## Scope

### `a2a/` — WorkspaceHub and Backend interface for agent-to-agent swarm communication


---

## Árbol de Estructura

### `a2a/` — WorkspaceHub and Backend interface for agent-to-agent swarm communication
Provides peer discovery, presence tracking, inbox messaging, and WebSocket-based hub coordination for swarm participants.
- `runtime.go` — core runtime with swarm join/leave, messaging, and peer management functions
- `websocket.go` — WebSocket-based HubClient and WorkspaceHub implementation
- `types.go` — data types for PeerPresence, SwarmMessage, HubStatus, and HubMessage
**Entry:** `runtime.go:L1`

### `a2a/newswarm/` — Client and message types for the newswarm messaging protocol
Defines `Client` for sending and receiving typed messages (`ChatMsg`, `BroadcastMsg`, `StatusUpdateMsg`, `PromptRequestMsg`, etc.) over a swarm link, along with `LinkStatus` tracking and prompt request/response handling.
- `messages.go` — Client, Message interface, and all message type definitions
- `status.go` — LinkStatus struct and IsIdle helper
- `prompt.go` — Prompt request and response message types
**Entry:** `messages.go:L1`

### `a2a/pb/` — Generated protobuf Go bindings exposing the `SendMessageConfiguration` message type.
Contains compiled protocol buffer definitions for the A2A API, including message configuration and enum descriptors.
- `a2a.pb.go` — Protobuf-generated message and enum types
**Entry:** `a2a.pb.go:L1`

---

## Señales de Cambio Reciente

- `swarm-sdk/a2a/runtime.go`
- `swarm-sdk/a2a/peer_mute.go`
- `swarm-sdk/a2a/peer_mute_test.go`
- `swarm-sdk/a2a/backend.go`
- `swarm-sdk/a2a/discovery.go`
- `swarm-sdk/a2a/poller.go`
- `swarm-sdk/a2a/poller_test.go`
- `swarm-sdk/a2a/senddm_integration_test.go`
- `swarm-sdk/a2a/simple_backend.go`
- `swarm-sdk/a2a/sqlite_backend.go`
