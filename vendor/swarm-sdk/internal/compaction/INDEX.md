# INDEX.md — compaction

> Modo: incremental | Directorios indexados: 1
> Para regenerar: `idx generate --path ./compaction`

---

## Scope

### `compaction/` — Service and utilities for compacting conversation context via summarization


---

## Árbol de Estructura

### `compaction/` — Service and utilities for compacting conversation context via summarization
The primary exported symbol is `Service`, created with `NewService`, which manages context compaction using configurable thresholds, summarization functions, and file access tracking.
- `compaction.go` — Core compaction logic, `Service`, `CompactionConfig`, and summarization pipeline
- `mcp.go` — MCP tool integration for compaction
- `todo_tool.go` — `Todo` type and todo-related tooling
**Entry:** `compaction.go:L1`

---

## Señales de Cambio Reciente

- `swarm-sdk/compaction/compaction.go`
- `swarm-sdk/compaction/compaction_integration_test.go`
- `swarm-sdk/compaction/compaction_preservation_test.go`
- `swarm-sdk/compaction/compaction_test.go`
- `swarm-sdk/compaction/hierarchical.go`
- `swarm-sdk/compaction/mcp.go`
- `swarm-sdk/compaction/micro.go`
- `swarm-sdk/compaction/verify.go`
- `swarm-sdk/compaction/prompt.go`
- `swarm-sdk/compaction/agent_compaction.go`
