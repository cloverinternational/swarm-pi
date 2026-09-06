# INDEX.md — serve

> Modo: incremental | Directorios indexados: 1
> Para regenerar: `idx generate --path ./serve`

---

## Scope

### `serve/` — Central JSON-RPC method registry and dispatcher implemented by `Mux`


---

## Árbol de Estructura

### `serve/` — Central JSON-RPC method registry and dispatcher implemented by `Mux`
It routes method calls to `*client.Client` via transport handlers that funnel through `Mux.Dispatch`.
- `mux.go` — Core `Mux` type and dispatch logic
- `jsonrpc.go` — JSON-RPC request, response, and error types
- `stream.go` — HTTP and streaming transport handlers
**Entry:** `mux.go:L1`

---

## Señales de Cambio Reciente

- `swarm-sdk/serve/mux.go`
- `swarm-sdk/serve/websocket.go`
- `swarm-sdk/serve/jsonrpc.go`
- `swarm-sdk/serve/auth.go`
- `swarm-sdk/serve/client_methods.go`
- `swarm-sdk/serve/doc.go`
- `swarm-sdk/serve/http.go`
- `swarm-sdk/serve/method.go`
- `swarm-sdk/serve/mux_test.go`
- `swarm-sdk/serve/sse.go`
