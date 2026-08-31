# INDEX.md — mcp

> Modo: incremental | Directorios indexados: 1
> Para regenerar: `idx generate --path ./mcp`

---

## Scope

### `mcp/` — Provides the ConfigManager struct for managing MCP server configuration with layered loading and runtime resolution.

---

## Árbol de Estructura

### `mcp/` — Provides the ConfigManager struct for managing MCP server configuration with layered loading and runtime resolution.
- `config_manager.go` — Configuration manager and CRUD operations
- `runtime_manager.go` — Runtime server resolution
- `runtime_resources.go` — Resource tool constants and limits
**Entry:** `config_manager.go:L1`

---

## Señales de Cambio Reciente

- `swarm-sdk/mcp/fuzz_test.go`
- `swarm-sdk/mcp/config.go`
- `swarm-sdk/mcp/config_legacy.go`
- `swarm-sdk/mcp/config_manager.go`
- `swarm-sdk/mcp/config_manager_test.go`
- `swarm-sdk/mcp/config_test.go`
- `swarm-sdk/mcp/config_types.go`
- `swarm-sdk/mcp/config_validate.go`
- `swarm-sdk/mcp/config_validate_test.go`
- `swarm-sdk/mcp/credentials.go`
