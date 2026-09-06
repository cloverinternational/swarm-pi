# INDEX.md — oauth

> Modo: incremental | Directorios indexados: 1
> Para regenerar: `idx generate --path ./oauth`

---

## Scope

### `tools/mcp/oauth/` — Provides OAuth callback handling via `CallbackServer` and `SingletonCallbackServer`, with persistent token storage through `TokenStorage`.

---

## Árbol de Estructura

### `tools/mcp/oauth/` — Provides OAuth callback handling via `CallbackServer` and `SingletonCallbackServer`, with persistent token storage through `TokenStorage`.
The `RunCallbackFlow` function orchestrates the full OAuth authorization code flow, while `SingletonCallbackServer` allows multiple concurrent auth requests to share a single callback listener.
- `flow.go` — OAuth callback server implementation and authorization flow orchestration
- `storage.go` — `TokenStorage` interface for persisting and loading OAuth tokens
- `types.go` — Shared OAuth data types and configuration structs
**Entry:** `flow.go:L1`

---

