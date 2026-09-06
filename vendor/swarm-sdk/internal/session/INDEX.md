# INDEX.md — session

> Modo: incremental | Directorios indexados: 1
> Para regenerar: `idx generate --path ./session`

---

## Scope

### `session/` — Legacy shim providing `Session` interface and `New` constructor that translates `Config` into `client.

---

## Árbol de Estructura

### `session/` — Legacy shim providing `Session` interface and `New` constructor that translates `Config` into `client.Option` calls.
- `new.go` — `New` function and `Config` struct (deprecated)
- `session.go` — `Session` interface definition
- `doc.go` — Package documentation
**Entry:** `new.go:L1`

---

## Señales de Cambio Reciente

- `swarm-sdk/session/client_session.go`
- `swarm-sdk/session/client_session_crud.go`
- `swarm-sdk/session/client_session_crud_test.go`
- `swarm-sdk/session/client_session_test.go`
- `swarm-sdk/session/new.go`
- `swarm-sdk/session/session.go`
- `swarm-sdk/session/types.go`
- `swarm-sdk/session/doc.go`
