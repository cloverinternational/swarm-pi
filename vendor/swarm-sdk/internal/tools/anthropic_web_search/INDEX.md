# INDEX.md — anthropic_web_search

> Modo: incremental | Directorios indexados: 1
> Para regenerar: `idx generate --path ./anthropic_web_search`

---

## Scope

### `tools/anthropic_web_search` — MCPServer providing Anthropic's server-side web search tool integration via the web-search beta flag.

---

## Árbol de Estructura

### `tools/anthropic_web_search` — MCPServer providing Anthropic's server-side web search tool integration via the web-search beta flag.
Wraps the main Anthropic Chat provider with web search enabled, using OAuth tokens and automatic refresh.
- `tool.go` — MCPServer definition, NewMCPServer constructor, and Start/Stop lifecycle
- `web_search.go` — SearchRequest struct and web search request/response parsing
- `stats.go` — Rate limiting and usage tracking
**Entry:** `tool.go:L1`

---

## Señales de Cambio Reciente

- `swarm-sdk/tools/anthropic_web_search/fuzz_test.go`
