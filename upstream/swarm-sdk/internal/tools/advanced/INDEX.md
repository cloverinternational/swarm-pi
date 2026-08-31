# INDEX.md — advanced

> Modo: incremental | Directorios indexados: 1
> Para regenerar: `idx generate --path ./advanced`

---

## Scope

### `tools/advanced/` — `ToolBuilder` fluent API for constructing tools with advanced metadata


---

## Árbol de Estructura

### `tools/advanced/` — `ToolBuilder` fluent API for constructing tools with advanced metadata
Produces a `BuiltTool` implementing `tools.Tool` plus optional `Deferrable`, `tools.ToolWithExamples`, `tools.ParallelCapable`, and `tools.MetadataProvider` interfaces.
- `builder.go` — `ToolBuilder` type and fluent construction methods
- `state.go` — internal state management for built tools
- `deferred.go` — deferred execution support
**Entry:** `builder.go:L1`

---

