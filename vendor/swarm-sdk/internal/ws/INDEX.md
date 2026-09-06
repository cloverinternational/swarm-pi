# INDEX.md — ws

> Modo: incremental | Directorios indexados: 1
> Para regenerar: `idx generate --path ./ws`

---

## Scope

### `ws/` — WebSocket server upgrader and connection types wrapping gorilla/websocket


---

## Árbol de Estructura

### `ws/` — WebSocket server upgrader and connection types wrapping gorilla/websocket
Provides `ServerUpgrader` for upgrading HTTP connections and `Conn` for reading/writing WebSocket messages with JSON support.
- `server.go` — ServerUpgrader and Upgrade function
- `options.go` — Upgrader option functions (WithCheckOrigin, WithBufferSizes, etc.)
- `ws.go` — Conn type and message read/write operations
**Entry:** `server.go:L1`

---

## Señales de Cambio Reciente

- `swarm-sdk/ws/options.go`
- `swarm-sdk/ws/server.go`
- `swarm-sdk/ws/ws.go`
