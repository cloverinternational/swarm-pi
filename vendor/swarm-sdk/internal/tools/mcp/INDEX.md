# INDEX.md — mcp

> Modo: incremental | Directorios indexados: 2
> Para regenerar: `idx generate --path ./mcp`

---

## Scope

### `tools/mcp/` — Adapters wrapping MCP server tools and resources as `tools.

---

## Árbol de Estructura

### `tools/mcp/` — Adapters wrapping MCP server tools and resources as `tools.Tool` implementations.
- `adapter.go` — defines `MCPToolAdapter`, `MCPResourceWrapper`, and `MCPPromptAdapter` adapting MCP primitives to the SDK tool interface
- `client.go` — MCP client for communicating with MCP servers
- `types.go` — MCP protocol type definitions
**Entry:** `adapter.go:L1`

### `tools/mcp/oauth/` — Provides OAuth callback handling via `CallbackServer` and `SingletonCallbackServer`, with persistent token storage through `TokenStorage`.
The `RunCallbackFlow` function orchestrates the full OAuth authorization code flow, while `SingletonCallbackServer` allows multiple concurrent auth requests to share a single callback listener.
- `flow.go` — OAuth callback server implementation and authorization flow orchestration
- `storage.go` — `TokenStorage` interface for persisting and loading OAuth tokens
- `types.go` — Shared OAuth data types and configuration structs
**Entry:** `flow.go:L1`

---

## Señales de Cambio Reciente

- `swarm-sdk/tools/mcp/fuzz_test.go`
