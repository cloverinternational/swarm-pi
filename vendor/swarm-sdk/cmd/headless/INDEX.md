# INDEX.md — headless

> Modo: incremental | Directorios indexados: 1
> Para regenerar: `idx generate --path ./headless`

---

## Scope

### `cmd/headless/` — Headless CLI client for testing the SwarmOS SDK


---

## Árbol de Estructura

### `cmd/headless/` — Headless CLI client for testing the SwarmOS SDK
Provides a command-line interface for conversation management, resuming, branching, tool execution via `ToolAdapter`, and MCP integration.
- `main.go` — CLI entry point with conversation and message commands
- `cmd_config_providers.go` — Configuration provider setup
- `simple_logger.go` — Lightweight logging implementation
**Entry:** `main.go:L1`

---

## Índice de Patrones Comunes

| Tarea | Dónde ir primero |
|---|---|
| Agregar subcomando CLI | `cli.py` |

---

## Señales de Cambio Reciente

- `swarm-sdk/cmd/headless/main.go`
- `swarm-sdk/cmd/headless/cmd_config_tools.go`
