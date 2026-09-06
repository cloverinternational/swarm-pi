# INDEX.md — websearch

> Modo: incremental | Directorios indexados: 1
> Para regenerar: `idx generate --path ./websearch`

---

## Scope

### `tools/websearch` — Package websearch provides a unified web search tool supporting Anthropic and Exa backends.

---

## Árbol de Estructura

### `tools/websearch` — Package websearch provides a unified web search tool supporting Anthropic and Exa backends.
The primary API surface includes `GetWebSearchTools`, `GetWebSearchToolsWithConfig`, and auth-detection helpers like `IsAuthConfigured`.
- `tool.go` — Package documentation, tool construction, and backend selection
- `web_search.go` — Web search tool implementations and configuration
- `stats.go` — Search statistics and observability helpers
**Entry:** `tool.go:L1`

---

## Señales de Cambio Reciente

- `swarm-sdk/tools/websearch/exa.go`
- `swarm-sdk/tools/websearch/tool.go`
